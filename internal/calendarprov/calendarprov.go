// Package calendarprov provides holiday sources. Computed holidays are calculated
// locally from the Pawukon engine; remote providers (Plan 3) add API sources with caching.
package calendarprov

import (
	"context"

	"wiminder/internal/domain"
)

type Provider interface {
	Name() string
	Category() string
	HolidaysBetween(ctx context.Context, from, to domain.Date) ([]domain.Holiday, error)
}

type computedPawukon struct{}

func NewComputedPawukon() Provider       { return computedPawukon{} }
func (computedPawukon) Name() string     { return "pawukon-computed" }
func (computedPawukon) Category() string { return "pawukon" }
func (computedPawukon) HolidaysBetween(_ context.Context, from, to domain.Date) ([]domain.Holiday, error) {
	return domain.PawukonHolidaysBetween(from, to), nil
}

// DeduplicateHolidays merges holidays sharing a DedupeKey (same date, same
// normalized name): the FIRST occurrence wins and keeps its name, category,
// and position; later duplicates are dropped. Callers pass slices in provider
// priority order (pawukon → national → saka), so the locally computed entry
// wins. The input slice is not modified.
func DeduplicateHolidays(hs []domain.Holiday) []domain.Holiday {
	seen := make(map[string]bool, len(hs))
	var out []domain.Holiday
	for _, h := range hs {
		k := h.DedupeKey()
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, h)
	}
	return out
}

// MultiProvider combines providers and filters them by the settings categories.
// Cross-source duplicates (same date, same normalized name — pawukon
// "Saraswati" vs saka "Hari Saraswati") are merged first-wins, so provider
// slice order is the priority: pawukon must come first.
type MultiProvider struct{ Providers []Provider }

func (m MultiProvider) HolidaysBetween(ctx context.Context, from, to domain.Date, enabled map[string]bool) ([]domain.Holiday, error) {
	var out []domain.Holiday
	for _, p := range m.Providers {
		if !enabled[p.Category()] {
			continue
		}
		hs, err := p.HolidaysBetween(ctx, from, to)
		if err != nil {
			return nil, err
		}
		for _, h := range hs {
			h.Category = p.Category()
			out = append(out, h)
		}
	}
	return DeduplicateHolidays(out), nil
}
