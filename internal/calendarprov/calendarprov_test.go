package calendarprov

import (
	"context"
	"testing"

	"wiminder/internal/domain"
)

func TestComputedPawukon(t *testing.T) {
	p := NewComputedPawukon()
	if p.Category() != "pawukon" {
		t.Errorf("category = %q", p.Category())
	}
	hs, err := p.HolidaysBetween(context.Background(), domain.NewDate(2026, 6, 1), domain.NewDate(2026, 7, 31))
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, h := range hs {
		found[h.Name] = true
	}
	if !found["Galungan"] || !found["Kuningan"] {
		t.Errorf("Galungan/Kuningan missing: %v", hs)
	}
}

func TestMultiProviderFilter(t *testing.T) {
	m := MultiProvider{Providers: []Provider{NewComputedPawukon()}}
	hs, err := m.HolidaysBetween(context.Background(),
		domain.NewDate(2026, 6, 1), domain.NewDate(2026, 6, 30), map[string]bool{"pawukon": false})
	if err != nil {
		t.Fatal(err)
	}
	if len(hs) != 0 {
		t.Errorf("category off must be empty: %v", hs)
	}
	hs, _ = m.HolidaysBetween(context.Background(),
		domain.NewDate(2026, 6, 1), domain.NewDate(2026, 6, 30), map[string]bool{"pawukon": true})
	if len(hs) != 2 {
		t.Errorf("category on: %v", hs)
	}
}

// stubProvider: minimal in-memory provider for MultiProvider tests.
type stubProvider struct {
	name, cat string
	hs        []domain.Holiday
}

func (s stubProvider) Name() string     { return s.name }
func (s stubProvider) Category() string { return s.cat }
func (s stubProvider) HolidaysBetween(_ context.Context, _ domain.Date, _ domain.Date) ([]domain.Holiday, error) {
	return s.hs, nil
}

// TestMultiProviderTagsCategory: MultiProvider stamps each holiday with the
// producing provider's category so consumers (/upcoming) can resolve
// per-category reminder offsets without their own provider loop.
func TestMultiProviderTagsCategory(t *testing.T) {
	m := MultiProvider{Providers: []Provider{stubProvider{name: "stub", cat: "pawukon",
		hs: []domain.Holiday{{Date: domain.NewDate(2026, 6, 17), Name: "Galungan"}}}}}
	hs, err := m.HolidaysBetween(context.Background(),
		domain.NewDate(2026, 6, 1), domain.NewDate(2026, 6, 30), map[string]bool{"pawukon": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(hs) != 1 || hs[0].Category != "pawukon" {
		t.Errorf("hs = %+v, want one holiday tagged Category=pawukon", hs)
	}
}

func TestDeduplicateHolidaysFirstWins(t *testing.T) {
	in := []domain.Holiday{
		{Date: domain.NewDate(2026, 10, 31), Name: "Saraswati", Category: "pawukon"},
		{Date: domain.NewDate(2026, 10, 31), Name: "Hari Saraswati", Category: "saka"},
		{Date: domain.NewDate(2026, 12, 25), Name: "Hari Raya Natal", Category: "national"},
	}
	out := DeduplicateHolidays(in)
	if len(out) != 2 {
		t.Fatalf("out = %+v, want 2 survivors", out)
	}
	if out[0].Name != "Saraswati" || out[0].Category != "pawukon" {
		t.Errorf("first occurrence must win: %+v", out[0])
	}
	if out[1].Name != "Hari Raya Natal" {
		t.Errorf("unrelated holiday must survive: %+v", out[1])
	}
	if len(in) != 3 {
		t.Errorf("input must not be modified: %+v", in)
	}
}

// The dedup key is (date + normalized name): two genuinely DIFFERENT holidays
// sharing a date must not be over-merged. Pins the "same day ≠ duplicate" edge
// of the merge that first-wins semantics could otherwise swallow.
func TestDeduplicateHolidaysKeepsDistinctSameDay(t *testing.T) {
	in := []domain.Holiday{
		{Date: domain.NewDate(2026, 1, 1), Name: "Tahun Baru Masehi", Category: "national"},
		{Date: domain.NewDate(2026, 1, 1), Name: "Hari Saraswati", Category: "saka"},
	}
	out := DeduplicateHolidays(in)
	if len(out) != 2 {
		t.Fatalf("out = %+v, want both distinct same-day holidays to survive", out)
	}
	if out[0].Name != "Tahun Baru Masehi" || out[0].Category != "national" {
		t.Errorf("first holiday must be preserved in order: %+v", out[0])
	}
	if out[1].Name != "Hari Saraswati" || out[1].Category != "saka" {
		t.Errorf("second holiday must be preserved in order: %+v", out[1])
	}
}

func TestMultiProviderDeduplicatesAcrossSources(t *testing.T) {
	m := MultiProvider{Providers: []Provider{
		stubProvider{name: "paw", cat: "pawukon", hs: []domain.Holiday{
			{Date: domain.NewDate(2026, 10, 31), Name: "Saraswati"}}},
		stubProvider{name: "saka", cat: "saka", hs: []domain.Holiday{
			{Date: domain.NewDate(2026, 10, 31), Name: "Hari Saraswati"},
			{Date: domain.NewDate(2026, 1, 17), Name: "Hari Siwa Ratri"}}},
	}}
	hs, err := m.HolidaysBetween(context.Background(),
		domain.NewDate(2026, 1, 1), domain.NewDate(2026, 12, 31),
		map[string]bool{"pawukon": true, "saka": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(hs) != 2 || hs[0].Name != "Saraswati" || hs[0].Category != "pawukon" {
		t.Errorf("pawukon entry must win the merge: %+v", hs)
	}
	if hs[1].Name != "Hari Siwa Ratri" {
		t.Errorf("non-colliding saka holiday must survive: %+v", hs)
	}
	// Settings-aware: pawukon off → saka's copy of the same day survives.
	hs, err = m.HolidaysBetween(context.Background(),
		domain.NewDate(2026, 1, 1), domain.NewDate(2026, 12, 31),
		map[string]bool{"pawukon": false, "saka": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(hs) != 2 || hs[0].Name != "Hari Saraswati" || hs[0].Category != "saka" {
		t.Errorf("saka copy must survive when pawukon is disabled: %+v", hs)
	}
}
