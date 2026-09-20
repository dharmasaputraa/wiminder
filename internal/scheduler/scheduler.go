// Package scheduler implements the scan-based reminder engine. Stateless with
// respect to the DB — send/missed decisions are computed on every scan from
// (now, settings, contacts, notification_log). Idempotent: crash/restart safe.
package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"wiminder/internal/calendarprov"
	"wiminder/internal/domain"
	"wiminder/internal/notify"
	"wiminder/internal/store"
)

type Clock interface{ Now() time.Time }

type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

type FakeClock struct{ T time.Time }

func (f *FakeClock) Now() time.Time      { return f.T }
func (f *FakeClock) Add(d time.Duration) { f.T = f.T.Add(d) }

type Snapshot struct {
	Timezone       string
	SendTime       string
	CatchUpHours   int
	DefaultOffsets []int
	// Channels used by contacts without their own selection. Empty/nil →
	// every enabled channel (the pre-default behavior).
	DefaultChannelIDs []string
	HolidayCategories map[string]bool
	// Per holiday source (pawukon/saka/national) reminder offsets. Empty/nil
	// for a category falls back to DefaultOffsets.
	HolidayOffsets map[string][]int
	// Per-stream default offset sets (settings.recurrence_offsets). Consumed by
	// the per-stream resolution in RunOnce.
	RecurrenceOffsets domain.OffsetMap
}

type Resolver func(ctx context.Context, ch store.Channel) (notify.Notifier, error)

type Result struct{ Sent, Failed, Missed int }

const failBackoff = 15 * time.Minute

var notifCounter = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "wiminder_notifications_total",
	Help: "notifications by status and kind",
}, []string{"status", "kind"})

type Service struct {
	St        *store.Store
	Clock     Clock
	Providers []calendarprov.Provider
	Resolve   Resolver

	mu        sync.Mutex
	failUntil map[string]time.Time
}

func HolidayKey(category string, h domain.Holiday) string {
	return category + ":" + strings.ToLower(strings.ReplaceAll(h.Name, " ", "-"))
}

func parseSendTime(s string) (int, int, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, 0, fmt.Errorf("send_time invalid: %q", s)
	}
	return t.Hour(), t.Minute(), nil
}

func maxOffset(offsets []int) int {
	m := 0
	for _, o := range offsets {
		if o > m {
			m = o
		}
	}
	return m
}

// filterChannels narrows a channel list to the given ids, keeping the input
// order. Ids matching nothing yield an empty list (caller decides the fallback).
func filterChannels(all []store.Channel, ids []string) []store.Channel {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var out []store.Channel
	for _, ch := range all {
		if want[ch.ID] {
			out = append(out, ch)
		}
	}
	return out
}

// targetChannels lists the destination channels for one contact: the contact's
// own selection, else the system default channels, else every enabled channel.
func (s *Service) targetChannels(ctx context.Context, cw store.ContactWithOccasions, defaultIDs []string) []store.Channel {
	all, err := s.St.ListChannels(ctx, cw.OwnerID)
	if err != nil {
		return nil
	}
	enabled := all[:0:0]
	for _, ch := range all {
		if ch.Enabled {
			enabled = append(enabled, ch)
		}
	}
	filter := func(ids []string) []store.Channel {
		want := map[string]bool{}
		for _, id := range ids {
			want[id] = true
		}
		filtered := enabled[:0:0]
		for _, ch := range enabled {
			if want[ch.ID] {
				filtered = append(filtered, ch)
			}
		}
		return filtered
	}
	if cw.Prefs != nil && len(cw.Prefs.ChannelIDs) > 0 {
		return filter(cw.Prefs.ChannelIDs)
	}
	if len(defaultIDs) > 0 {
		if filtered := filter(defaultIDs); len(filtered) > 0 {
			return filtered
		}
		// A default that matches nothing enabled → fall through to all.
	}
	return enabled
}

func (s *Service) RunOnce(ctx context.Context, snap Snapshot) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Lazy-init: Service is usually built via a struct literal from outside
	// the package (main.go), which cannot fill the unexported failUntil field —
	// without this, the first failed send = nil-map panic inside the mutex → scan dies.
	if s.failUntil == nil {
		s.failUntil = make(map[string]time.Time)
	}
	var res Result

	loc, err := time.LoadLocation(snap.Timezone)
	if err != nil {
		loc = time.UTC
	}
	now := s.Clock.Now().In(loc)
	sendHH, sendMM, err := parseSendTime(snap.SendTime)
	if err != nil {
		return res, err
	}
	// The catch-up window is computed from now: reminders due within the last
	// CatchUpHours may still be sent (catch-up); older than that → missed. The
	// `now` time base (not today@SendTime) is needed for test anchor consistency:
	// at 08:02 with CatchUp 24 hours, H-1 (yesterday 08:00 = 24h2m ago) is already
	// outside the window → missed; at 07:00 (23 hours ago) it still fits → sent late.
	dueStart := now.Add(-time.Duration(snap.CatchUpHours) * time.Hour)
	today := domain.DateFromTime(now)

	maxOff := maxOffset(snap.DefaultOffsets)
	// Holiday sources can remind further out than the default offsets — widen
	// the scan window to cover the widest enabled category.
	holidayMaxOff := maxOff
	for _, p := range s.Providers {
		if !snap.HolidayCategories[p.Category()] {
			continue
		}
		if offs := snap.HolidayOffsets[p.Category()]; len(offs) > 0 {
			if m := maxOffset(offs); m > holidayMaxOff {
				holidayMaxOff = m
			}
		}
	}
	catchUpDays := (snap.CatchUpHours + 23) / 24
	lookback := maxOff + catchUpDays + 2
	from := today.AddDays(-lookback)
	horizon := today.AddDays(holidayMaxOff + 2)

	// ---- occasions ----
	contacts, err := s.St.ListContacts(ctx, "") // admin scope: all contacts
	if err != nil {
		return res, err
	}
	for _, cw := range contacts {
		if cw.Prefs != nil && !cw.Prefs.Enabled {
			continue
		}
		defaultChannels := s.targetChannels(ctx, cw, snap.DefaultChannelIDs)
		for _, occ := range cw.Occasions {
			if occ.Prefs != nil && !occ.Prefs.Enabled {
				continue // per-occasion kill switch
			}
			// Channels: occasion override → contact cascade (already resolved).
			// An override row with custom=false holds retained-but-inactive
			// values: the occasion inherits as if the row were absent.
			channels := defaultChannels
			if occ.Prefs != nil && occ.Prefs.Custom && len(occ.Prefs.ChannelIDs) > 0 {
				if byID := filterChannels(defaultChannels, occ.Prefs.ChannelIDs); len(byID) > 0 {
					channels = byID
				}
			}
			// Offsets per stream: occasion → contact → settings → DefaultOffsets.
			// Prefs rows are optional, so the offsets maps are read through the
			// pointers (nil prefs = pure inherit); custom=false ignores occOff.
			var occOff, contactOff domain.OffsetMap
			if occ.Prefs != nil && occ.Prefs.Custom {
				occOff = occ.Prefs.Offsets
			}
			if cw.Prefs != nil {
				contactOff = cw.Prefs.Offsets
			}
			resolved := domain.ResolveOccasionStreams(occ.Recurrence, occOff, contactOff, snap.RecurrenceOffsets)
			// The scan window covers the widest resolved stream: a monthly [0]
			// does not widen it, a yearly [30] does.
			maxOff := 0
			for _, offs := range resolved {
				if m := maxOffset(offs); m > maxOff {
					maxOff = m
				}
			}
			catchUpDays := (snap.CatchUpHours + 23) / 24
			fromO := today.AddDays(-(maxOff + catchUpDays + 2))
			toO := today.AddDays(maxOff + 2)
			occs, err := domain.OccurrencesBetween(occ.BaseDate, occ.Type, occ.Recurrence, fromO, toO)
			if err != nil {
				continue
			}
			for _, o := range occs {
				offs := resolved[o.Stream]
				if o.Stream == domain.StreamEvent && len(offs) == 0 {
					offs = domain.DefaultOffsets
				}
				for _, off := range offs {
					rDate := o.Date.AddDays(-off)
					sendAt := time.Date(rDate.Year, time.Month(rDate.Month), rDate.Day, sendHH, sendMM, 0, 0, loc)
					if sendAt.After(now) {
						continue
					}
					occID := occ.ID
					entry := store.NotificationEntry{OccasionID: &occID,
						OccurrenceDate: o.Date, OffsetDays: off}
					if sendAt.Before(dueStart) {
						for _, ch := range channels {
							entry.ChannelID, entry.Status = ch.ID, "missed"
							s.record(ctx, entry, &res, "occasion")
						}
						continue
					}
					late := now.Sub(sendAt) > time.Hour
					msg := notify.OccurrenceMessage(cw.Name, o, o.Date.JDN()-today.JDN(), late)
					s.deliver(ctx, channels, entry, msg, &res, "occasion")
				}
			}
		}
	}

	// ---- holidays ----
	// Gather: every enabled provider contributes its holidays stamped with its
	// category and effective offsets. A failing provider is skipped (computed
	// pawukon keeps working). Dedup below keeps the FIRST occurrence, so the
	// provider slice order is the priority: pawukon wins over API sources
	// (spec 2026-09-15-holiday-dedup).
	type holidayWithOffsets struct {
		h    domain.Holiday
		offs []int
	}
	var gathered []holidayWithOffsets
	for _, p := range s.Providers {
		if !snap.HolidayCategories[p.Category()] {
			continue
		}
		// Per-category offsets; a category without its own list falls back to
		// the global default offsets (pre-customization behavior).
		offs := snap.HolidayOffsets[p.Category()]
		if len(offs) == 0 {
			offs = snap.DefaultOffsets
		}
		hs, err := p.HolidaysBetween(ctx, from, horizon)
		if err != nil {
			// remote provider failed → skip; computed pawukon keeps working
			continue
		}
		for _, h := range hs {
			h.Category = p.Category()
			gathered = append(gathered, holidayWithOffsets{h: h, offs: offs})
		}
	}
	// Schedule: the same send loop as before, over the deduped list. The
	// winner's category/offsets drive the reminders; its HolidayKey is
	// identical to the pre-dedup key, so notification_log dedupe never
	// re-sends, and dropped duplicates simply stop being generated.
	seen := map[string]bool{}
	for _, g := range gathered {
		dk := g.h.DedupeKey()
		if seen[dk] {
			continue
		}
		seen[dk] = true
		h := g.h
		hkey := HolidayKey(h.Category, h)
		for _, off := range g.offs {
			rDate := h.Date.AddDays(-off)
			sendAt := time.Date(rDate.Year, time.Month(rDate.Month), rDate.Day, sendHH, sendMM, 0, 0, loc)
			if sendAt.After(now) {
				continue
			}
			entry := store.NotificationEntry{HolidayKey: &hkey,
				OccurrenceDate: h.Date, OffsetDays: off}
			if sendAt.Before(dueStart) {
				// holiday → all channels of ALL users (broadcast)
				users, err := s.St.ListUsers(ctx)
				if err != nil {
					continue
				}
				for _, u := range users {
					chs, _ := s.St.ListChannels(ctx, u.ID)
					for _, ch := range chs {
						if ch.Enabled {
							entry.ChannelID, entry.Status = ch.ID, "missed"
							s.record(ctx, entry, &res, "holiday")
						}
					}
				}
				continue
			}
			late := now.Sub(sendAt) > time.Hour
			msg := notify.HolidayMessage(h, h.Date.JDN()-today.JDN(), late)
			users, err := s.St.ListUsers(ctx)
			if err != nil {
				continue
			}
			for _, u := range users {
				chs, _ := s.St.ListChannels(ctx, u.ID)
				var enabled []store.Channel
				for _, ch := range chs {
					if ch.Enabled {
						enabled = append(enabled, ch)
					}
				}
				s.deliver(ctx, enabled, entry, msg, &res, "holiday")
			}
		}
	}
	return res, nil
}

func (s *Service) record(ctx context.Context, e store.NotificationEntry, res *Result, kind string) {
	inserted, err := s.St.RecordNotification(ctx, e)
	if err != nil {
		// don't silently drop it: the reminder is still sent, but the log trail
		// is missing from dedupe — warn so it shows up in observability.
		slog.Warn("record_notification failed", "kind", kind, "channel_id", e.ChannelID, "err", err)
		return
	}
	if !inserted {
		return
	}
	res.Missed++
	notifCounter.WithLabelValues("missed", kind).Inc()
}

func (s *Service) deliver(ctx context.Context, channels []store.Channel,
	e store.NotificationEntry, msg notify.Message, res *Result, kind string) {
	now := s.Clock.Now()
	for _, ch := range channels {
		if until, ok := s.failUntil[ch.ID]; ok && now.Before(until) {
			continue // backoff
		}
		e.ChannelID = ch.ID
		// PRE-SEND dedupe: a row with the same dedupe key already exists → do not
		// send again. Without this, the per-minute scanner re-pushes the same
		// reminder all day — INSERT OR IGNORE only holds back the counter,
		// not the push (dedupe logging happens AFTER n.Send).
		exists, err := s.St.HasNotification(ctx, e)
		if err != nil {
			// Fail open (intentional): a failed dedupe check must not
			// silence the reminder — a double push is better than a lost
			// reminder. INSERT OR IGNORE in the log still prevents duplicate
			// records/counters.
			slog.Warn("has_notification failed, sending anyway (fail open)",
				"channel_id", ch.ID, "err", err)
		} else if exists {
			continue
		}
		n, err := s.Resolve(ctx, ch)
		if err != nil {
			res.Failed++
			notifCounter.WithLabelValues("resolve_error", kind).Inc()
			continue
		}
		if err := n.Send(ctx, msg); err != nil {
			res.Failed++ // NOT recorded → retried on the next scan
			s.failUntil[ch.ID] = now.Add(failBackoff)
			notifCounter.WithLabelValues("failed", kind).Inc()
			continue
		}
		e.Status = "sent"
		inserted, err := s.St.RecordNotification(ctx, e)
		if err != nil {
			continue
		}
		if inserted {
			res.Sent++
		}
		notifCounter.WithLabelValues("sent", kind).Inc()
	}
}
