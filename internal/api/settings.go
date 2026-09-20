package api

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"wiminder/internal/domain"
)

type Settings struct {
	Timezone       string `json:"timezone"`
	SendTime       string `json:"send_time"`
	CatchUpHours   int    `json:"catch_up_hours"`
	DefaultOffsets []int  `json:"default_offsets"`
	// Channels used by contacts without their own selection. Empty/nil →
	// every enabled channel (the pre-default behavior).
	DefaultChannelIDs []string         `json:"default_channel_ids"`
	HolidayCategories map[string]bool  `json:"holiday_categories"`
	HolidayOffsets    map[string][]int `json:"holiday_offsets"`
	// Per-stream default offset sets, keyed by recurrence stream (event,
	// yearly, monthly, otonan). Always complete after SaveSettings.
	RecurrenceOffsets domain.OffsetMap `json:"recurrence_offsets"`
}

func DefaultSettings() Settings {
	return Settings{
		Timezone:       "Asia/Makassar",
		SendTime:       "08:00",
		CatchUpHours:   24,
		DefaultOffsets: append([]int(nil), domain.DefaultOffsets...),
		HolidayCategories: map[string]bool{
			"pawukon": true, "saka": true, "national": true,
		},
		// Empty per-category lists fall back to DefaultOffsets at scheduling time.
		HolidayOffsets:    map[string][]int{},
		RecurrenceOffsets: domain.DefaultRecurrenceOffsets(),
	}
}

var sendTimeRe = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

// LoadSettings: defaults ← JSON override from the DB (key "settings").
func (s *Server) LoadSettings(ctx context.Context) Settings {
	out := DefaultSettings()
	var stored Settings
	if err := s.st.GetSettingJSON(ctx, "settings", &stored); err != nil {
		return out // ErrNotFound or an old decode → defaults
	}
	if stored.Timezone != "" {
		out.Timezone = stored.Timezone
	}
	if stored.SendTime != "" {
		out.SendTime = stored.SendTime
	}
	if stored.CatchUpHours > 0 {
		out.CatchUpHours = stored.CatchUpHours
	}
	if len(stored.DefaultOffsets) > 0 {
		out.DefaultOffsets = append([]int(nil), stored.DefaultOffsets...)
	}
	if stored.DefaultChannelIDs != nil {
		out.DefaultChannelIDs = append([]string(nil), stored.DefaultChannelIDs...)
	}
	if stored.HolidayCategories != nil {
		out.HolidayCategories = stored.HolidayCategories
	}
	if stored.HolidayOffsets != nil {
		out.HolidayOffsets = stored.HolidayOffsets
	}
	if stored.RecurrenceOffsets != nil {
		out.RecurrenceOffsets = stored.RecurrenceOffsets
	}
	return out
}

func (s *Server) SaveSettings(ctx context.Context, in Settings) (Settings, error) {
	if _, err := time.LoadLocation(in.Timezone); err != nil {
		return Settings{}, fmt.Errorf("unknown timezone: %q", in.Timezone)
	}
	if !sendTimeRe.MatchString(in.SendTime) {
		return Settings{}, fmt.Errorf("send_time must be HH:MM, got %q", in.SendTime)
	}
	if in.CatchUpHours < 1 || in.CatchUpHours > 168 {
		return Settings{}, fmt.Errorf("catch_up_hours must be 1..168")
	}
	if err := domain.ValidateOffsets(in.DefaultOffsets); err != nil {
		return Settings{}, err
	}
	if in.HolidayCategories == nil {
		in.HolidayCategories = map[string]bool{}
	}
	for _, cat := range []string{"pawukon", "saka", "national"} {
		if _, ok := in.HolidayCategories[cat]; !ok {
			in.HolidayCategories[cat] = false
		}
	}
	if in.HolidayOffsets == nil {
		in.HolidayOffsets = map[string][]int{}
	}
	for cat, offs := range in.HolidayOffsets {
		if err := domain.ValidateOffsets(offs); err != nil {
			return Settings{}, fmt.Errorf("holiday_offsets[%q]: %w", cat, err)
		}
	}
	if err := domain.ValidateOffsetMap(in.RecurrenceOffsets); err != nil {
		return Settings{}, err
	}
	for _, s := range []domain.Stream{domain.StreamEvent, domain.StreamYearly, domain.StreamMonthly, domain.StreamOtonan} {
		if len(in.RecurrenceOffsets[s]) == 0 {
			return Settings{}, fmt.Errorf("recurrence_offsets[%s] is required (use a non-empty list)", s)
		}
	}
	if err := s.st.PutSettingJSON(ctx, "settings", in); err != nil {
		return Settings{}, err
	}
	return in, nil
}
