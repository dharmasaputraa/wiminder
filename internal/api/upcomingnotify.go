package api

import (
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wiminder/internal/domain"
	"wiminder/internal/notify"
	"wiminder/internal/store"
)

type upcomingNotifyIn struct {
	Kind       string   `json:"kind"` // "occasion" | "holiday"
	OccasionID string   `json:"occasion_id"`
	ContactID  string   `json:"contact_id"`
	Date       string   `json:"date"` // YYYY-MM-DD, the occurrence date from /upcoming
	Title      string   `json:"title"`
	ChannelIDs []string `json:"channel_ids"` // empty → every enabled channel of the caller
}

// handleUpcomingNotify pushes the reminder for one /upcoming item immediately,
// the manual "send now" from the event detail dialog. The message is
// reconstructed the same way the scheduler builds it (occasions from the DB,
// holidays from the providers) so the wording matches a scheduled send.
// Nothing is written to notification_log: a manual push never suppresses or
// duplicates the scheduler's own dedupe decisions.
func (s *Server) handleUpcomingNotify(c *gin.Context) {
	in, ok := bind[upcomingNotifyIn](c)
	if !ok {
		return
	}
	date, err := domain.ParseDate(in.Date)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	settings := s.LoadSettings(ctx)
	loc, locErr := time.LoadLocation(settings.Timezone)
	if locErr != nil {
		loc = time.UTC
	}
	today := domain.DateFromTime(time.Now().In(loc))

	// ±2 days around the target: occurrences/holidays are matched by exact date,
	// the window only bounds the provider/store scan.
	from, to := date.AddDays(-2), date.AddDays(2)

	var msg notify.Message
	switch in.Kind {
	case "holiday":
		hs, err := s.multiProvider().HolidaysBetween(ctx, from, to, settings.HolidayCategories)
		if err != nil {
			c.JSON(502, gin.H{"error": "holiday provider failed"})
			return
		}
		var h *domain.Holiday
		for i := range hs {
			if hs[i].Date.Equal(date) && strings.EqualFold(hs[i].Name, in.Title) {
				h = &hs[i]
				break
			}
		}
		if h == nil {
			c.JSON(404, gin.H{"error": "holiday not found for this date"})
			return
		}
		msg = notify.HolidayMessage(*h, h.Date.JDN()-today.JDN(), false)
	case "occasion":
		if in.OccasionID == "" || in.ContactID == "" {
			c.JSON(400, gin.H{"error": "occasion_id and contact_id are required"})
			return
		}
		cw, err := s.st.GetContact(ctx, s.scope(c), in.ContactID)
		if err != nil {
			respondErr(c, err)
			return
		}
		occ, err := s.st.OccasionByID(ctx, s.scope(c), in.OccasionID)
		if err != nil {
			respondErr(c, err)
			return
		}
		if occ.ContactID != cw.ID {
			c.JSON(404, gin.H{"error": "occasion not found on this contact"})
			return
		}
		occs, err := domain.OccurrencesBetween(occ.BaseDate, occ.Type, occ.Recurrence, from, to)
		if err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		var o *domain.Occurrence
		for i := range occs {
			if occs[i].Date.Equal(date) {
				o = &occs[i]
				break
			}
		}
		if o == nil {
			c.JSON(404, gin.H{"error": "occurrence not found for this date"})
			return
		}
		msg = notify.OccurrenceMessage(cw.Name, *o, o.Date.JDN()-today.JDN(), false)
	default:
		c.JSON(400, gin.H{"error": "kind must be occasion or holiday"})
		return
	}

	// Targets: the CALLER's enabled channels — deliberately not the admin's
	// scope-0 view, a manual push from the dialog must not broadcast to other
	// users' channels.
	all, err := s.st.ListChannels(ctx, mustUser(c).ID)
	if err != nil {
		respondErr(c, err)
		return
	}
	want := make(map[string]bool, len(in.ChannelIDs))
	for _, id := range in.ChannelIDs {
		want[id] = true
	}
	var channels []store.Channel
	for _, ch := range all {
		if !ch.Enabled || (len(want) > 0 && !want[ch.ID]) {
			continue
		}
		channels = append(channels, ch)
	}
	if len(channels) == 0 {
		c.JSON(400, gin.H{"error": "no enabled channel matches"})
		return
	}

	sent, failed := 0, 0
	var lastErr string
	for _, ch := range channels {
		n, err := notify.NewFromChannel(ch, s.key)
		if err == nil {
			err = n.Send(ctx, msg)
		}
		if err != nil {
			failed++
			lastErr = err.Error()
			continue
		}
		sent++
	}
	if sent == 0 {
		c.JSON(502, gin.H{"error": "send failed: " + lastErr})
		return
	}
	c.JSON(200, gin.H{"sent": sent, "failed": failed})
}
