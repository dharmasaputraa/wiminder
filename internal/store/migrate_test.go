package store

import (
	"context"
	"testing"

	"wiminder/internal/domain"
)

// Regression: the occasion type vocabulary must not break on schema edits.
// 001_init.sql once carried a CHECK on the fixed type list, and "otongan" was
// renamed to "otonan" in place (later papered over by 002_rename_otongan_type).
// The rewritten 001 folds that in — the rename is irrelevant on a fresh
// database (rollout is a fresh start, no data migration) — and drops the type
// CHECK entirely: built-in and custom types are free strings now. What stays is
// the recurrence CHECK. This pins the fresh-database contract: 'otonan' and
// custom types insert fine, a bogus recurrence is rejected by the store and,
// as a backstop, by the DB CHECK.
func TestMigrateFreshSchemaOccasionVocabulary(t *testing.T) {
	s, err := OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}

	u, _ := s.GetOrCreateUser(ctx, "budi@x.id", "Budi", nil)
	c, err := s.CreateContact(ctx, u.ID, "Made Wijaya", "Made", "cousin")
	if err != nil {
		t.Fatal(err)
	}
	// The renamed spelling inserts with its recurrence.
	if _, err := s.AddOccasion(ctx, c.ID, domain.Otonan, domain.RecurOtonan, domain.NewDate(1990, 5, 12), ""); err != nil {
		t.Fatalf("AddOccasion(%q): %v", domain.Otonan, err)
	}
	// Custom type: no CHECK on occasions.type anymore.
	if _, err := s.AddOccasion(ctx, c.ID, "wedding", domain.RecurAnniversary, domain.NewDate(2025, 6, 16), ""); err != nil {
		t.Fatalf("custom type: %v", err)
	}
	// Unknown recurrence: rejected before the INSERT.
	if _, err := s.AddOccasion(ctx, c.ID, "birthday", "weekly", domain.NewDate(2000, 1, 1), ""); err == nil {
		t.Error("unknown recurrence must be rejected by the store")
	}
	// The DB CHECK is the backstop for rows written around the store.
	if _, err := s.db.Exec(`INSERT INTO occasions (id, contact_id, type, recurrence, base_date)
		VALUES ('00000000-0000-7000-8000-000000000000', ?, 'birthday', 'weekly', '2000-01-01')`, c.ID); err == nil {
		t.Error("unknown recurrence must be rejected by the CHECK")
	}

	got, err := s.GetContact(ctx, u.ID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Occasions) != 2 {
		t.Fatalf("occasions = %d, want 2", len(got.Occasions))
	}
	want := map[domain.OccurrenceType]domain.Recurrence{
		domain.Otonan: domain.RecurOtonan,
		"wedding":     domain.RecurAnniversary,
	}
	for _, oc := range got.Occasions {
		if want[oc.Type] != oc.Recurrence {
			t.Errorf("%s: recurrence = %q, want %q", oc.Type, oc.Recurrence, want[oc.Type])
		}
	}
}
