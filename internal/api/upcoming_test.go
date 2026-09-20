package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"wiminder/internal/calendarprov"
	"wiminder/internal/config"
	"wiminder/internal/domain"
	"wiminder/internal/store"
)

// stubProv: deterministic holiday provider for /upcoming tests.
type stubProv struct {
	name, cat string
	hs        []domain.Holiday
}

func (s stubProv) Name() string     { return s.name }
func (s stubProv) Category() string { return s.cat }
func (s stubProv) HolidaysBetween(_ context.Context, _ domain.Date, _ domain.Date) ([]domain.Holiday, error) {
	return s.hs, nil
}

func newUpcomingTestServer(t *testing.T, provs []calendarprov.Provider) (*Server, *store.Store) {
	t.Helper()
	ginSet(t)
	st, err := store.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{AppSecret: "super-secret-long-enough-16", AuthMode: config.AuthDev,
		AdminEmails: map[string]bool{"admin@x.id": true}, TZ: "Asia/Makassar"}
	return NewServer(cfg, st, provs), st
}

func upcomingItems(t *testing.T, srv *Server, query string) []UpcomingItem {
	t.Helper()
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, devReq(t, "GET", "/api/v1/upcoming"+query, "admin@x.id", ""))
	if w.Code != 200 {
		t.Fatalf("upcoming: %d %s", w.Code, w.Body.String())
	}
	var got struct {
		Items []UpcomingItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got.Items
}

func firstHoliday(items []UpcomingItem) *UpcomingItem {
	for i := range items {
		if items[i].Kind == "holiday" {
			return &items[i]
		}
	}
	return nil
}

// Holidays must carry their effective reminder offsets: the global defaults
// when the category has no override, with reminders_default=true.
func TestUpcomingHolidayRemindersDefault(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Makassar")
	today := domain.DateFromTime(time.Now().In(loc))
	prov := stubProv{name: "fake-national", cat: "national", hs: []domain.Holiday{
		{Date: today.AddDays(10), Name: "Hari Raya Natal", Category: "national"},
	}}
	srv, _ := newUpcomingTestServer(t, []calendarprov.Provider{prov})

	hol := firstHoliday(upcomingItems(t, srv, "?days=30"))
	if hol == nil {
		t.Fatal("no holiday item in /upcoming")
	}
	if !hol.RemindersDefault {
		t.Errorf("reminders_default = %v, want true (no category override)", hol.RemindersDefault)
	}
	if !reflect.DeepEqual(hol.Reminders, domain.DefaultOffsets) {
		t.Errorf("reminders = %v, want %v", hol.Reminders, domain.DefaultOffsets)
	}
}

// A per-category override replaces the defaults: reminders = the override,
// reminders_default=false (field omitted via omitempty).
func TestUpcomingHolidayRemindersCustom(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Makassar")
	today := domain.DateFromTime(time.Now().In(loc))
	prov := stubProv{name: "fake-national", cat: "national", hs: []domain.Holiday{
		{Date: today.AddDays(10), Name: "Hari Raya Natal", Category: "national"},
	}}
	srv, _ := newUpcomingTestServer(t, []calendarprov.Provider{prov})

	body := `{"timezone":"Asia/Makassar","send_time":"08:00","catch_up_hours":24,` +
		`"default_offsets":[7,4,2,1,0],` +
		`"recurrence_offsets":{"event":[30],"yearly":[2],"monthly":[0],"otonan":[5]},` +
		`"holiday_categories":{"pawukon":true,"saka":true,"national":true},` +
		`"holiday_offsets":{"national":[3,1]}}`
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, devReq(t, "PUT", "/api/v1/settings", "admin@x.id", body))
	if w.Code != 200 {
		t.Fatalf("settings PUT: %d %s", w.Code, w.Body.String())
	}

	hol := firstHoliday(upcomingItems(t, srv, "?days=30"))
	if hol == nil {
		t.Fatal("no holiday item in /upcoming")
	}
	if !reflect.DeepEqual(hol.Reminders, []int{3, 1}) {
		t.Errorf("reminders = %v, want [3 1]", hol.Reminders)
	}
	if hol.RemindersDefault {
		t.Error("reminders_default must be false when a category override exists")
	}
}

// Occasions: no prefs → default offsets with reminders_default=true; prefs
// offsets → those offsets with reminders_default=false.
func TestUpcomingOccasionRemindersFlag(t *testing.T) {
	srv, st := newUpcomingTestServer(t, nil)
	cid := createContact(t, srv, "admin@x.id", `{"name":"Made","nickname":"De"}`)
	loc, _ := time.LoadLocation("Asia/Makassar")
	today := domain.DateFromTime(time.Now().In(loc))
	ocBody, _ := json.Marshal(map[string]string{"type": "otonan", "date": today.AddDays(-domain.PawukonCycleDays).String()})
	addOccasion(t, srv, "admin@x.id", cid, string(ocBody))

	occ := func(items []UpcomingItem) *UpcomingItem {
		for i := range items {
			if items[i].Kind == "occasion" {
				return &items[i]
			}
		}
		return nil
	}

	o := occ(upcomingItems(t, srv, "?days=30"))
	if o == nil {
		t.Fatal("no occasion item in /upcoming")
	}
	if !o.RemindersDefault || !reflect.DeepEqual(o.Reminders, domain.DefaultOffsets) {
		t.Errorf("default case: default=%v reminders=%v, want true/%v",
			o.RemindersDefault, o.Reminders, domain.DefaultOffsets)
	}

	ctx := context.Background()
	// The otonan occurrence's stream is otonan, so the contact-level override
	// lives under that key (Task 8 resolves the item's stream the same way).
	if err := st.SetReminderPrefs(ctx, store.ReminderPrefs{
		ContactID: cid, Enabled: true, Offsets: domain.OffsetMap{domain.StreamOtonan: {2, 0}},
	}); err != nil {
		t.Fatal(err)
	}
	o = occ(upcomingItems(t, srv, "?days=30"))
	if o == nil {
		t.Fatal("no occasion item after prefs")
	}
	if o.RemindersDefault {
		t.Error("reminders_default must be false with prefs offsets")
	}
	if !reflect.DeepEqual(o.Reminders, []int{2, 0}) {
		t.Errorf("reminders = %v, want [2 0]", o.Reminders)
	}
}

// A custom=false occasion_prefs row holds retained-but-inactive values:
// /upcoming must resolve through the contact chain (same gate as the
// scheduler), not surface the retained offsets. Flipping custom back on
// re-activates them — proving the gate, not a seeding accident.
func TestUpcomingInactiveCustomOccasionPrefsInheritContact(t *testing.T) {
	srv, st := newUpcomingTestServer(t, nil)
	cid := createContact(t, srv, "admin@x.id", `{"name":"Made","nickname":"De"}`)
	loc, _ := time.LoadLocation("Asia/Makassar")
	today := domain.DateFromTime(time.Now().In(loc))
	ocBody, _ := json.Marshal(map[string]string{"type": "otonan", "date": today.AddDays(-domain.PawukonCycleDays).String()})
	occID := addOccasion(t, srv, "admin@x.id", cid, string(ocBody))

	ctx := context.Background()
	if err := st.SetReminderPrefs(ctx, store.ReminderPrefs{
		ContactID: cid, Enabled: true, Offsets: domain.OffsetMap{domain.StreamOtonan: {2, 0}},
	}); err != nil {
		t.Fatal(err)
	}
	// Retained-but-inactive: custom=false with DIFFERENT offsets.
	if err := st.SetOccasionPrefs(ctx, store.OccasionPrefs{OccasionID: occID, Custom: false, Enabled: true,
		Offsets: domain.OffsetMap{domain.StreamOtonan: {9, 7, 5}}}); err != nil {
		t.Fatal(err)
	}

	occ := func(items []UpcomingItem) *UpcomingItem {
		for i := range items {
			if items[i].Kind == "occasion" {
				return &items[i]
			}
		}
		return nil
	}

	o := occ(upcomingItems(t, srv, "?days=30"))
	if o == nil {
		t.Fatal("no occasion item in /upcoming")
	}
	if o.RemindersDefault {
		t.Error("reminders_default must be false: the contact chain supplies the offsets")
	}
	if !reflect.DeepEqual(o.Reminders, []int{2, 0}) {
		t.Errorf("custom=false reminders = %v, want contact chain [2 0]", o.Reminders)
	}

	// Same row, custom flipped on: the occasion offsets are active again.
	if err := st.SetOccasionPrefs(ctx, store.OccasionPrefs{OccasionID: occID, Custom: true, Enabled: true,
		Offsets: domain.OffsetMap{domain.StreamOtonan: {9, 7, 5}}}); err != nil {
		t.Fatal(err)
	}
	o = occ(upcomingItems(t, srv, "?days=30"))
	if o == nil {
		t.Fatal("no occasion item after custom=true")
	}
	if !reflect.DeepEqual(o.Reminders, []int{9, 7, 5}) {
		t.Errorf("custom=true reminders = %v, want occasion offsets [9 7 5]", o.Reminders)
	}
}
