package api

import (
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"wiminder/internal/calendarprov"
	"wiminder/internal/domain"
	"wiminder/internal/store"
)

type UpcomingItem struct {
	Date        domain.Date `json:"date"`
	Kind        string      `json:"kind"` // "occasion" | "holiday"
	OccasionID  string      `json:"occasion_id,omitempty"`
	ContactID   string      `json:"contact_id,omitempty"`
	ContactName string      `json:"contact_name,omitempty"`
	Type        string      `json:"type,omitempty"`
	// Recurrence of the occasion this occurrence belongs to.
	Recurrence domain.Recurrence `json:"recurrence"`
	Number     int               `json:"number,omitempty"`
	Title      string            `json:"title"`
	Pawukon    string            `json:"pawukon,omitempty"`
	DaysUntil  int               `json:"days_until"`
	Reminders  []int             `json:"reminders,omitempty"`
	// RemindersDefault: the offsets above came from the settings/global
	// fallback — neither the occasion nor the contact had a list for this
	// stream.
	RemindersDefault bool `json:"reminders_default,omitempty"`
}

func (s *Server) handleUpcoming(c *gin.Context) {
	ctx := c.Request.Context()
	settings := s.LoadSettings(ctx)

	loc, locErr := time.LoadLocation(settings.Timezone)
	if locErr != nil {
		loc = time.UTC
	}
	now := time.Now().In(loc)
	today := domain.DateFromTime(now)

	// Range: `days` mode (1..90 from today, default 30) or explicit
	// `from`/`to` mode (max 400 days) for calendars that scan across
	// years. An empty `to` means one year from `from`.
	rangeStart, horizon := today, today
	if fromQ := c.Query("from"); fromQ != "" {
		from, err := domain.ParseDate(fromQ)
		if err != nil {
			c.JSON(400, gin.H{"error": "invalid from (must be YYYY-MM-DD)"})
			return
		}
		rangeStart = from
		horizon = from.AddDays(365)
		if toQ := c.Query("to"); toQ != "" {
			to, err := domain.ParseDate(toQ)
			if err != nil {
				c.JSON(400, gin.H{"error": "invalid to (must be YYYY-MM-DD)"})
				return
			}
			if to.Before(from) {
				c.JSON(400, gin.H{"error": "inverted range: to < from"})
				return
			}
			horizon = to
		}
		if horizon.JDN()-rangeStart.JDN() > 400 {
			c.JSON(400, gin.H{"error": "range limited to 400 days"})
			return
		}
	} else {
		days, err := strconv.Atoi(c.DefaultQuery("days", "30"))
		if err != nil || days < 1 || days > 90 {
			days = 30
		}
		horizon = today.AddDays(days)
	}

	ownerID := s.scope(c)
	contacts, err := s.st.ListContacts(ctx, ownerID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to load contacts"})
		return
	}

	var items []UpcomingItem
	for _, cw := range contacts {
		contactMap := contactPrefsOf(cw)
		for _, occ := range cw.Occasions {
			occMap := occasionPrefsOf(occ)
			// Same precedence as the scheduler: occasion → contact → settings
			// recurrence_offsets → domain.DefaultOffsets, per stream.
			streamOffs := domain.ResolveOccasionStreams(occ.Recurrence, occMap, contactMap, settings.RecurrenceOffsets)
			occs, err := domain.OccurrencesBetween(occ.BaseDate, occ.Type, occ.Recurrence, rangeStart, horizon)
			if err != nil {
				continue
			}
			for _, o := range occs {
				item := UpcomingItem{
					Date: o.Date, Kind: "occasion", OccasionID: occ.ID, ContactID: cw.ID,
					ContactName: cw.Name, Type: string(o.Type), Number: o.Number,
					Recurrence: occ.Recurrence, Title: o.Label,
					DaysUntil: o.Date.JDN() - today.JDN(), Reminders: streamOffs[o.Stream],
					// No per-stream list at either prefs layer → the resolution
					// fell through to settings/DefaultOffsets.
					RemindersDefault: len(contactMap[o.Stream]) == 0 && len(occMap[o.Stream]) == 0,
				}
				if occ.Type == domain.Otonan {
					item.Pawukon = domain.Pawukon(o.Date).Label()
				}
				items = append(items, item)
			}
		}
	}

	hs, err := s.multiProvider().HolidaysBetween(ctx, rangeStart, horizon, settings.HolidayCategories)
	if err != nil {
		c.JSON(502, gin.H{"error": "holiday provider failed"})
		return
	}
	for _, h := range hs {
		// Same resolution as the scheduler: per-category offsets, global
		// default fallback (an empty Category also lands on the fallback).
		offs := settings.HolidayOffsets[h.Category]
		remindersDefault := len(offs) == 0
		if remindersDefault {
			offs = settings.DefaultOffsets
		}
		items = append(items, UpcomingItem{Date: h.Date, Kind: "holiday",
			Title: h.Name, DaysUntil: h.Date.JDN() - today.JDN(),
			Reminders: offs, RemindersDefault: remindersDefault})
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Date != items[j].Date {
			return items[i].Date.Before(items[j].Date)
		}
		return items[i].Kind < items[j].Kind
	})
	c.JSON(http.StatusOK, gin.H{"today": today.String(), "items": items})
}

func (s *Server) multiProvider() calendarprov.MultiProvider {
	return calendarprov.MultiProvider{Providers: s.providers}
}

// contactPrefsOf / occasionPrefsOf: the per-stream override maps of one prefs
// layer, nil when the row is absent (pure inherit). Enabled is a delivery kill
// switch handled by the scheduler; it does not select which offsets apply.
// A custom=false occasion row holds retained-but-inactive values: it inherits
// as if the row were absent (same gate as the scheduler).
func contactPrefsOf(cw store.ContactWithOccasions) domain.OffsetMap {
	if cw.Prefs == nil {
		return nil
	}
	return cw.Prefs.Offsets
}

func occasionPrefsOf(o store.Occasion) domain.OffsetMap {
	if o.Prefs == nil || !o.Prefs.Custom {
		return nil
	}
	return o.Prefs.Offsets
}
