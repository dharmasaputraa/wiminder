package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"wiminder/internal/domain"
)

// SPA contract: GET contact always carries an occasions array; `null` makes
// `c.occasions.map/length` on the contact page throw a TypeError.
func TestContactJSONOccasionsEmpty(t *testing.T) {
	s, _ := OpenInMemory()
	defer s.Close()
	ctx := context.Background()
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	u, _ := s.GetOrCreateUser(ctx, "budi@x.id", "Budi", nil)
	c, err := s.CreateContact(ctx, u.ID, "Without Occasion", "", "")
	if err != nil {
		t.Fatal(err)
	}

	// Contact detail without occasions.
	gw, err := s.GetContact(ctx, u.ID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	detail, _ := json.Marshal(gw)
	if got := string(detail); !strings.Contains(got, `"occasions":[]`) || strings.Contains(got, `"occasions":null`) {
		t.Errorf("contact detail without occasion must be \"occasions\":[], got %s", got)
	}

	// List contains the same contact (via fill()).
	list, err := s.ListContacts(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	lb, _ := json.Marshal(list)
	if got := string(lb); !strings.Contains(got, `"occasions":[]`) || strings.Contains(got, `"occasions":null`) {
		t.Errorf("contact list without occasion must be \"occasions\":[], got %s", got)
	}

	// User with no contacts: the list is still an empty array, not null.
	v, _ := s.GetOrCreateUser(ctx, "empty@x.id", "Empty", nil)
	empty, err := s.ListContacts(ctx, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	eb, _ := json.Marshal(empty)
	if string(eb) != "[]" {
		t.Errorf("empty contact list must be [], got %s", eb)
	}
}

func seedContact(t *testing.T, s *Store) (User, ContactWithOccasions) {
	t.Helper()
	ctx := context.Background()
	// Note: the brief does not call Migrate(); the Task 2 store needs an explicit migration.
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	u, _ := s.GetOrCreateUser(ctx, "budi@x.id", "Budi", nil)
	c, err := s.CreateContact(ctx, u.ID, "Made Wijaya", "Made", "cousin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddOccasion(ctx, c.ID, domain.Otonan, domain.RecurOtonan, domain.NewDate(1990, 5, 12), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddOccasion(ctx, c.ID, domain.Birthday, domain.RecurYearly, domain.NewDate(1990, 5, 20), ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetReminderPrefs(ctx, ReminderPrefs{ContactID: c.ID,
		Offsets: domain.OffsetMap{domain.StreamEvent: {1, 0}}, ChannelIDs: []string{}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	cw, err := s.GetContact(ctx, u.ID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	return u, *cw
}

func TestContactCRUD(t *testing.T) {
	s, _ := OpenInMemory()
	defer s.Close()
	u, cw := seedContact(t, s)
	if len(cw.Occasions) != 2 {
		t.Fatalf("occasions = %d", len(cw.Occasions))
	}
	if _, err := uuid.Parse(cw.ID); err != nil {
		t.Errorf("contact id not a uuid: %v", cw.ID)
	}
	for _, oc := range cw.Occasions {
		if _, err := uuid.Parse(oc.ID); err != nil {
			t.Errorf("occasion id not a uuid: %v", oc.ID)
		}
	}
	if cw.Prefs == nil || len(cw.Prefs.Offsets[domain.StreamEvent]) != 2 {
		t.Fatalf("wrong prefs: %+v", cw.Prefs)
	}
	if cw.Nickname != "Made" {
		t.Errorf("nickname = %q", cw.Nickname)
	}

	if err := s.UpdateContact(context.Background(), u.ID, cw.ID, "Made W.", "", "new note"); err != nil {
		t.Fatal(err)
	}
	ls, _ := s.ListContacts(context.Background(), u.ID)
	if ls[0].Name != "Made W." {
		t.Errorf("update failed: %q", ls[0].Name)
	}

	// another owner cannot see it
	v, _ := s.GetOrCreateUser(context.Background(), "other@x.id", "Other", nil)
	if _, err := s.GetContact(context.Background(), v.ID, cw.ID); err == nil {
		t.Error("accessing another user's contact must error")
	}
	// admin (ownerID "") can
	if _, err := s.GetContact(context.Background(), "", cw.ID); err != nil {
		t.Errorf("admin must be able to access: %v", err)
	}

	if err := s.DeleteContact(context.Background(), u.ID, cw.ID); err != nil {
		t.Fatal(err)
	}
	ls, _ = s.ListContacts(context.Background(), u.ID)
	if len(ls) != 0 {
		t.Errorf("delete failed: %d left", len(ls))
	}
}

func TestAddOccasionValidatesTypeAndRecurrence(t *testing.T) {
	s, _ := OpenInMemory()
	defer s.Close()
	// Note: the brief does not call Migrate(); the Task 2 store needs an explicit migration.
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	u, _ := s.GetOrCreateUser(context.Background(), "budi@x.id", "Budi", nil)
	c, _ := s.CreateContact(context.Background(), u.ID, "X", "", "")
	// Custom types are allowed now (the fixed-type check lives in the API
	// layer); the store rejects an empty type, an over-long type and an
	// unknown recurrence.
	if _, err := s.AddOccasion(context.Background(), c.ID, "", domain.RecurYearly, domain.NewDate(2000, 1, 1), ""); err == nil {
		t.Error("empty type must be rejected")
	}
	tooLong := domain.OccurrenceType(strings.Repeat("x", 65))
	if _, err := s.AddOccasion(context.Background(), c.ID, tooLong, domain.RecurYearly, domain.NewDate(2000, 1, 1), ""); err == nil {
		t.Error("over-long type must be rejected")
	}
	if _, err := s.AddOccasion(context.Background(), c.ID, "wedding", "weekly", domain.NewDate(2000, 1, 1), ""); err == nil {
		t.Error("illegal recurrence must be rejected")
	}
	if _, err := s.AddOccasion(context.Background(), c.ID, "wedding", domain.RecurAnniversary, domain.NewDate(2000, 1, 1), ""); err != nil {
		t.Errorf("custom type with a valid recurrence must be accepted: %v", err)
	}
}

func TestDeleteOccasionOwnerScope(t *testing.T) {
	s, _ := OpenInMemory()
	defer s.Close()
	ctx := context.Background()
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	a, _ := s.GetOrCreateUser(ctx, "a@x.id", "A", nil)
	b, _ := s.GetOrCreateUser(ctx, "b@x.id", "B", nil)
	ca, _ := s.CreateContact(ctx, a.ID, "Contact A", "", "")
	cb, _ := s.CreateContact(ctx, b.ID, "Contact B", "", "")
	oa, err := s.AddOccasion(ctx, ca.ID, domain.Otonan, domain.RecurOtonan, domain.NewDate(1990, 5, 12), "")
	if err != nil {
		t.Fatal(err)
	}
	ob, err := s.AddOccasion(ctx, cb.ID, domain.Otonan, domain.RecurOtonan, domain.NewDate(1991, 6, 13), "")
	if err != nil {
		t.Fatal(err)
	}

	// owner A cannot delete B's occasion (IDOR)
	if err := s.DeleteOccasion(ctx, a.ID, ob.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("owner A deleting B's occasion must be ErrNotFound, got %v", err)
	}
	// B's occasion is still there
	if _, err := s.GetContact(ctx, b.ID, cb.ID); err != nil {
		t.Fatalf("B's occasion missing: %v", err)
	}

	// owner B deletes their own occasion: allowed
	if err := s.DeleteOccasion(ctx, b.ID, ob.ID); err != nil {
		t.Fatalf("owner B deleting own occasion failed: %v", err)
	}
	// deleting twice → ErrNotFound
	if err := s.DeleteOccasion(ctx, b.ID, ob.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting an already deleted occasion must be ErrNotFound, got %v", err)
	}

	// admin (ownerID "") can delete anyone's occasion
	if err := s.DeleteOccasion(ctx, "", oa.ID); err != nil {
		t.Errorf("admin deleting occasion failed: %v", err)
	}
}

func TestOccasionPrefsRoundTrip(t *testing.T) {
	ctx := context.Background()
	st, _ := OpenInMemory()
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	u, err := st.GetOrCreateUser(ctx, "a@b.c", "A", nil)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := st.CreateContact(ctx, u.ID, "Ani", "", "")
	if err != nil {
		t.Fatal(err)
	}
	oc, err := st.AddOccasion(ctx, ct.ID, "anniversary", domain.RecurAnniversary, domain.NewDate(2025, 6, 16), "wedding")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := uuid.Parse(oc.ID); err != nil {
		t.Fatalf("occasion id not a uuid: %v", oc.ID)
	}
	if err := st.SetOccasionPrefs(ctx, OccasionPrefs{OccasionID: oc.ID,
		Offsets: domain.OffsetMap{domain.StreamMonthly: {1, 0}}, ChannelIDs: []string{}, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	got, err := st.OccasionByID(ctx, u.ID, oc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Prefs == nil || got.Prefs.Enabled || len(got.Prefs.Offsets[domain.StreamMonthly]) != 2 {
		t.Fatalf("prefs round-trip: %+v", got.Prefs)
	}
	if err := st.DeleteOccasionPrefs(ctx, oc.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.OccasionByID(ctx, u.ID, oc.ID); got.Prefs != nil {
		t.Fatalf("delete override: %+v", got.Prefs)
	}
}

// Toggling custom off persists the row: custom=false keeps offsets and
// channel_ids in place (they reactivate when custom flips back to true).
func TestOccasionPrefsCustomRetainedWhenOff(t *testing.T) {
	ctx := context.Background()
	st, _ := OpenInMemory()
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	u, err := st.GetOrCreateUser(ctx, "custom-off@b.c", "A", nil)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := st.CreateContact(ctx, u.ID, "Ani", "", "")
	if err != nil {
		t.Fatal(err)
	}
	oc, err := st.AddOccasion(ctx, ct.ID, "birthday", domain.RecurYearly, domain.NewDate(2025, 6, 16), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetOccasionPrefs(ctx, OccasionPrefs{OccasionID: oc.ID, Custom: true, Enabled: true,
		Offsets:    domain.OffsetMap{domain.StreamYearly: {7, 3, 0}},
		ChannelIDs: []string{"ch-1", "ch-2"}}); err != nil {
		t.Fatal(err)
	}
	// Toggle custom OFF: the row survives with its values.
	if err := st.SetOccasionPrefs(ctx, OccasionPrefs{OccasionID: oc.ID, Custom: false, Enabled: true,
		Offsets:    domain.OffsetMap{domain.StreamYearly: {7, 3, 0}},
		ChannelIDs: []string{"ch-1", "ch-2"}}); err != nil {
		t.Fatal(err)
	}
	got, err := st.OccasionByID(ctx, u.ID, oc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Prefs == nil {
		t.Fatal("prefs row must exist after custom=false")
	}
	if got.Prefs.Custom {
		t.Error("custom = true, want false")
	}
	if !got.Prefs.Enabled {
		t.Error("enabled must be independent of custom")
	}
	if !reflect.DeepEqual(got.Prefs.Offsets[domain.StreamYearly], []int{7, 3, 0}) {
		t.Errorf("offsets = %v, want [7 3 0] retained", got.Prefs.Offsets[domain.StreamYearly])
	}
	if !reflect.DeepEqual(got.Prefs.ChannelIDs, []string{"ch-1", "ch-2"}) {
		t.Errorf("channel_ids = %v, want retained", got.Prefs.ChannelIDs)
	}
}

// Backfill: a row written the pre-002 way (explicit column list, no `custom`)
// must come back custom=true — migration 002's DEFAULT 1 is what makes the
// old toggle-on rows active instead of silently retained-but-off.
func TestOccasionPrefsCustomDefaultsTrueOnBackfill(t *testing.T) {
	ctx := context.Background()
	st, _ := OpenInMemory()
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	u, err := st.GetOrCreateUser(ctx, "backfill@x.id", "A", nil)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := st.CreateContact(ctx, u.ID, "Ani", "", "")
	if err != nil {
		t.Fatal(err)
	}
	oc, err := st.AddOccasion(ctx, ct.ID, "birthday", domain.RecurYearly, domain.NewDate(2025, 6, 16), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx,
		`INSERT INTO occasion_prefs (occasion_id, offsets, channel_ids, enabled) VALUES (?,?,?,?)`,
		oc.ID, `{"yearly":[7]}`, `[]`, 1); err != nil {
		t.Fatal(err)
	}
	got, err := st.OccasionByID(ctx, u.ID, oc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Prefs == nil {
		t.Fatal("prefs row missing after raw INSERT")
	}
	if !got.Prefs.Custom {
		t.Error("custom = false, want true (migration DEFAULT 1 backfill)")
	}
	if !reflect.DeepEqual(got.Prefs.Offsets[domain.StreamYearly], []int{7}) {
		t.Errorf("offsets = %v, want [7]", got.Prefs.Offsets[domain.StreamYearly])
	}
}

// fill() must load occasion prefs even when the contact has no contact-level
// reminder_prefs row: the ErrNoRows case is a plain skip, not an early return.
func TestOccasionPrefsWithoutContactPrefs(t *testing.T) {
	ctx := context.Background()
	st, _ := OpenInMemory()
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	u, err := st.GetOrCreateUser(ctx, "noprefs@x.id", "A", nil)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := st.CreateContact(ctx, u.ID, "Ani", "", "")
	if err != nil {
		t.Fatal(err)
	}
	oc, err := st.AddOccasion(ctx, ct.ID, "anniversary", domain.RecurAnniversary, domain.NewDate(2025, 6, 16), "wedding")
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately no SetReminderPrefs: c.Prefs stays nil for this contact.
	if err := st.SetOccasionPrefs(ctx, OccasionPrefs{OccasionID: oc.ID,
		Offsets: domain.OffsetMap{domain.StreamMonthly: {1, 0}}, ChannelIDs: []string{}, Enabled: false}); err != nil {
		t.Fatal(err)
	}

	got, err := st.GetContact(ctx, u.ID, ct.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Prefs != nil {
		t.Fatalf("contact prefs must stay nil: %+v", got.Prefs)
	}
	if len(got.Occasions) != 1 || got.Occasions[0].Prefs == nil {
		t.Fatalf("occasion prefs must load without contact prefs: %+v", got.Occasions)
	}
	if got.Occasions[0].Prefs.Enabled || len(got.Occasions[0].Prefs.Offsets[domain.StreamMonthly]) != 2 {
		t.Fatalf("occasion prefs round-trip: %+v", got.Occasions[0].Prefs)
	}

	// The list path goes through the same fill().
	ls, err := st.ListContacts(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	var found *ContactWithOccasions
	for i := range ls {
		if ls[i].ID == ct.ID {
			found = &ls[i]
		}
	}
	if found == nil || found.Prefs != nil || len(found.Occasions) != 1 || found.Occasions[0].Prefs == nil {
		t.Fatalf("list fill mismatch: %+v", ls)
	}
}

// OccasionByID is owner-scoped: another owner gets ErrNotFound, admin "" sees it.
func TestOccasionByIDOwnerScope(t *testing.T) {
	ctx := context.Background()
	st, _ := OpenInMemory()
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	a, _ := st.GetOrCreateUser(ctx, "a@x.id", "A", nil)
	b, _ := st.GetOrCreateUser(ctx, "b@x.id", "B", nil)
	ca, _ := st.CreateContact(ctx, a.ID, "Contact A", "", "")
	oc, err := st.AddOccasion(ctx, ca.ID, domain.Otonan, domain.RecurOtonan, domain.NewDate(1990, 5, 12), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.OccasionByID(ctx, b.ID, oc.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("other owner must get ErrNotFound, got %v", err)
	}
	got, err := st.OccasionByID(ctx, "", oc.ID)
	if err != nil {
		t.Fatalf("admin lookup failed: %v", err)
	}
	if got.Recurrence != domain.RecurOtonan || got.Type != domain.Otonan {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if _, err := st.OccasionByID(ctx, a.ID, "not-a-uuid"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id must be ErrNotFound, got %v", err)
	}
}
