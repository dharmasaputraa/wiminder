package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"wiminder/internal/config"
	"wiminder/internal/domain"
	"wiminder/internal/store"
)

func newTestServer(t *testing.T, admin string) (*Server, *store.Store) {
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
		AdminEmails: map[string]bool{admin: true}, TZ: "Asia/Makassar"}
	return NewServer(cfg, st, nil), st
}

func ginSet(t *testing.T) { gin.SetMode(gin.TestMode) } // via import gin

// createContact posts a contact and returns its id. Ids are opaque UUID
// strings now, so tests read the id back from the response instead of
// assuming "1".
func createContact(t *testing.T, srv *Server, email, body string) string {
	t.Helper()
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, devReq(t, "POST", "/api/v1/contacts", email, body))
	if w.Code != 201 {
		t.Fatalf("create contact: %d %s", w.Code, w.Body.String())
	}
	var c store.Contact
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	if c.ID == "" {
		t.Fatalf("created contact has no id: %s", w.Body.String())
	}
	return c.ID
}

// addOccasion posts an occasion on a contact and returns its id.
func addOccasion(t *testing.T, srv *Server, email, contactID, body string) string {
	t.Helper()
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, devReq(t, "POST", "/api/v1/contacts/"+contactID+"/occasions", email, body))
	if w.Code != 201 {
		t.Fatalf("add occasion: %d %s", w.Code, w.Body.String())
	}
	var oc store.Occasion
	if err := json.Unmarshal(w.Body.Bytes(), &oc); err != nil {
		t.Fatal(err)
	}
	if oc.ID == "" {
		t.Fatalf("created occasion has no id: %s", w.Body.String())
	}
	return oc.ID
}

// createChannel posts a channel and returns its id.
func createChannel(t *testing.T, srv *Server, email, body string) string {
	t.Helper()
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, devReq(t, "POST", "/api/v1/channels", email, body))
	if w.Code != 201 {
		t.Fatalf("create channel: %d %s", w.Code, w.Body.String())
	}
	var ch struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &ch); err != nil {
		t.Fatal(err)
	}
	if ch.ID == "" {
		t.Fatalf("created channel has no id: %s", w.Body.String())
	}
	return ch.ID
}

func devReq(t *testing.T, method, target, email, body string) *http.Request {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Dev-Email", email)
	return req
}

func TestContactFlow(t *testing.T) {
	srv, _ := newTestServer(t, "admin@x.id")
	cid := createContact(t, srv, "admin@x.id", `{"name":"Made","nickname":"De","notes":"cousin"}`)

	// Otonan base = today − 210 → the 1st occurrence falls EXACTLY today; deterministic
	// for the 30-day window (random dates often fall outside the window → flaky).
	// Pin to the server TZ (Asia/Makassar, matching DefaultSettings) — not the machine TZ —
	// so it is deterministic in all time zones.
	loc, _ := time.LoadLocation("Asia/Makassar")
	today := domain.DateFromTime(time.Now().In(loc))
	base := today.AddDays(-domain.PawukonCycleDays)
	ocBody, _ := json.Marshal(map[string]string{"type": "otonan", "date": base.String()})
	addOccasion(t, srv, "admin@x.id", cid, string(ocBody))

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, devReq(t, "GET", "/api/v1/upcoming?days=30", "admin@x.id", ""))
	if w.Code != 200 {
		t.Fatalf("upcoming: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"kind":"occasion"`) || !strings.Contains(w.Body.String(), `"pawukon"`) {
		t.Errorf("upcoming does not contain occasion+pawukon: %s", w.Body.String())
	}
}

func TestContactJSONSnakeCase(t *testing.T) {
	srv, _ := newTestServer(t, "admin@x.id")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, devReq(t, "POST", "/api/v1/contacts", "admin@x.id",
		`{"name":"Made","nickname":"De","notes":"cousin"}`))
	if w.Code != 201 {
		t.Fatalf("create contact: %d %s", w.Code, w.Body.String())
	}
	created := w.Body.String()
	for _, want := range []string{`"occasions":[]`, `"prefs":null`} {
		if !strings.Contains(created, want) {
			t.Errorf("create contact response does not contain %s: %s", want, created)
		}
	}
	var ct store.Contact
	if err := json.Unmarshal(w.Body.Bytes(), &ct); err != nil {
		t.Fatal(err)
	}
	addOccasion(t, srv, "admin@x.id", ct.ID, `{"type":"otonan","date":"1990-05-12"}`)

	w = httptest.NewRecorder()
	srv.ServeHTTP(w, devReq(t, "GET", "/api/v1/contacts/"+ct.ID, "admin@x.id", ""))
	if w.Code != 200 {
		t.Fatalf("get contact: %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{`"name":`, `"occasions"`, `"base_date"`, `"contact_id"`, `"prefs":null`} {
		if !strings.Contains(body, want) {
			t.Errorf("contact response does not contain %s: %s", want, body)
		}
	}
	for _, bad := range []string{`"OwnerID"`, `"BaseDate"`} {
		if strings.Contains(body, bad) {
			t.Errorf("contact response is still PascalCase %s: %s", bad, body)
		}
	}
}

func TestUpcomingEmpty(t *testing.T) {
	srv, _ := newTestServer(t, "admin@x.id")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, devReq(t, "GET", "/api/v1/upcoming", "admin@x.id", ""))
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	var out struct {
		Items []UpcomingItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	// may be empty or contain pawukon holidays; must not error
	_ = out
}

func TestPawukonEndpoint(t *testing.T) {
	srv, _ := newTestServer(t, "admin@x.id")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, devReq(t, "GET", "/api/v1/pawukon?date=2026-06-17", "admin@x.id", ""))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Buda Kliwon, Wuku Dunggulan") {
		t.Errorf("pawukon: %d %s", w.Code, w.Body.String())
	}
}

func TestChannelConfigNeverLeaked(t *testing.T) {
	srv, _ := newTestServer(t, "admin@x.id")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, devReq(t, "POST", "/api/v1/channels", "admin@x.id",
		`{"type":"gotify","name":"home","config":{"base_url":"https://g.x.id","token":"SECRET-TOKEN"}}`))
	if w.Code != 201 {
		t.Fatalf("create channel: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, devReq(t, "GET", "/api/v1/channels", "admin@x.id", ""))
	if strings.Contains(w.Body.String(), "SECRET-TOKEN") {
		t.Error("channel config leaked in the response!")
	}
}

func TestSettingsValidate(t *testing.T) {
	srv, _ := newTestServer(t, "admin@x.id")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, devReq(t, "PUT", "/api/v1/settings", "admin@x.id",
		`{"timezone":"Asia/Makassar","send_time":"07:30","catch_up_hours":12,"default_offsets":[3,1,0],`+
			`"recurrence_offsets":{"event":[30],"yearly":[2],"monthly":[0],"otonan":[5]},"holiday_categories":{"pawukon":true}}`))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"catch_up_hours":12`) {
		t.Errorf("save settings: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"recurrence_offsets"`) {
		t.Errorf("settings response must carry the snake_case recurrence_offsets key: %s", w.Body.String())
	}
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, devReq(t, "PUT", "/api/v1/settings", "admin@x.id",
		`{"timezone":"Not/AZone","send_time":"07:30","catch_up_hours":12,"default_offsets":[1],"holiday_categories":{}}`))
	if w.Code != 400 {
		t.Errorf("invalid tz must be 400: %d", w.Code)
	}
}

func TestSettingsMissingCategories(t *testing.T) {
	srv, _ := newTestServer(t, "admin@x.id")
	// PUT without holiday_categories → 200 (no panic), all three categories filled
	// per the brief's semantics: a category that is not sent → false.
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, devReq(t, "PUT", "/api/v1/settings", "admin@x.id",
		`{"timezone":"Asia/Jakarta","send_time":"08:00","catch_up_hours":24,"default_offsets":[7,4,2,1,0],`+
			`"recurrence_offsets":{"event":[30],"yearly":[2],"monthly":[0],"otonan":[5]}}`))
	if w.Code != 200 {
		t.Fatalf("put without holiday_categories: %d %s", w.Code, w.Body.String())
	}
	var got Settings
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for _, cat := range []string{"pawukon", "saka", "national"} {
		v, ok := got.HolidayCategories[cat]
		if !ok {
			t.Errorf("category %q missing from response", cat)
		} else if v {
			t.Errorf("category %q must be false (not sent), got %v", cat, v)
		}
	}
}

// Recurrence offsets: the settings layer owns the seeded per-stream sets
// (event/yearly/monthly/otonan). A save must carry a complete, in-range map —
// partial maps and unknown streams are rejected — while default_offsets keeps
// its holiday-fallback role.
func TestSettingsRecurrenceOffsets(t *testing.T) {
	srv, st := newTestServer(t, "admin@x.id")

	got := srv.LoadSettings(context.Background())
	def := got.RecurrenceOffsets
	if len(def[domain.StreamMonthly]) != 1 || def[domain.StreamMonthly][0] != 0 {
		t.Fatalf("default monthly offsets: %v", def)
	}
	if len(def[domain.StreamEvent]) != 6 || def[domain.StreamEvent][0] != 30 {
		t.Fatalf("default event offsets: %v", def)
	}
	if len(def) != 4 {
		t.Fatalf("default map must hold exactly the four stream keys: %v", def)
	}
	for s := range def {
		if len(def[s]) == 0 {
			t.Errorf("default map is missing stream %q: %v", s, def)
		}
	}

	in := got
	in.RecurrenceOffsets = domain.OffsetMap{domain.StreamYearly: {2, 0}}
	if _, err := srv.SaveSettings(context.Background(), in); err == nil {
		t.Fatal("partial map must be rejected: keys event/monthly/otonan are required")
	}
	in.RecurrenceOffsets = domain.OffsetMap{
		domain.StreamEvent: {30}, domain.StreamYearly: {2, 0},
		domain.StreamMonthly: {0}, domain.StreamOtonan: {5},
	}
	out, err := srv.SaveSettings(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if o := srv.LoadSettings(context.Background()).RecurrenceOffsets[domain.StreamYearly]; len(o) != 2 || o[0] != 2 {
		t.Fatalf("stored yearly offsets: %v", o)
	}
	if len(out.DefaultOffsets) == 0 {
		t.Fatal("default_offsets still required (holiday fallback)")
	}

	// Out-of-range offsets and unknown stream keys are rejected too, and a
	// rejected save must not touch what is stored.
	for name, m := range map[string]domain.OffsetMap{
		"unknown stream": {domain.Stream("weekly"): {1}},
		"out of range": {
			domain.StreamEvent: {61}, domain.StreamYearly: {2},
			domain.StreamMonthly: {0}, domain.StreamOtonan: {5},
		},
	} {
		in.RecurrenceOffsets = m
		if _, err := srv.SaveSettings(context.Background(), in); err == nil {
			t.Errorf("%s must be rejected", name)
		}
	}
	if o := srv.LoadSettings(context.Background()).RecurrenceOffsets[domain.StreamYearly]; len(o) != 2 || o[0] != 2 {
		t.Fatalf("a rejected save must not alter the stored map: %v", o)
	}

	// A settings blob stored before this field existed (no recurrence_offsets
	// key) keeps the defaults: the merge only overrides on a non-nil map.
	if err := st.PutSettingJSON(context.Background(), "settings", map[string]any{
		"timezone": "Asia/Jakarta", "send_time": "08:00", "catch_up_hours": 24,
		"default_offsets": []int{7, 4, 2, 1, 0},
	}); err != nil {
		t.Fatal(err)
	}
	again := srv.LoadSettings(context.Background())
	if again.Timezone != "Asia/Jakarta" {
		t.Errorf("stored timezone must still win: %q", again.Timezone)
	}
	if len(again.RecurrenceOffsets[domain.StreamEvent]) != 6 {
		t.Errorf("legacy stored settings must keep the default recurrence offsets: %v", again.RecurrenceOffsets)
	}
}

// Type validation is strict but is no longer an allowlist: built-ins and custom
// types are equally valid, the type only has to be non-empty and ≤64 chars.
func TestAddOccasionTypeValidation(t *testing.T) {
	srv, _ := newTestServer(t, "admin@x.id")
	cid := createContact(t, srv, "admin@x.id", `{"name":"Made"}`)
	post := func(body string) int {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, devReq(t, "POST", "/api/v1/contacts/"+cid+"/occasions", "admin@x.id", body))
		return w.Code
	}
	if got := post(`{"type":"","date":"1990-05-12"}`); got != 400 {
		t.Errorf("empty type must be 400, got %d", got)
	}
	if got := post(`{"type":"` + strings.Repeat("x", 65) + `","date":"1990-05-12"}`); got != 400 {
		t.Errorf("type over 64 chars must be 400, got %d", got)
	}
	if got := post(`{"type":"bogus","date":"1990-05-12"}`); got != 201 {
		t.Errorf("custom type must be accepted, got %d", got)
	}
}

func TestDefaultSettingsDefensiveCopy(t *testing.T) {
	srv, _ := newTestServer(t, "admin@x.id")
	ds := DefaultSettings()
	ds.DefaultOffsets[0] = 99
	ds.RecurrenceOffsets[domain.StreamEvent][0] = 99
	ls := srv.LoadSettings(context.Background())
	ls.DefaultOffsets[0] = 99
	ls.RecurrenceOffsets[domain.StreamEvent][0] = 99
	if domain.DefaultOffsets[0] != 7 {
		t.Errorf("domain.DefaultOffsets mutated via api.Settings: %v", domain.DefaultOffsets)
	}
	if v := domain.DefaultRecurrenceOffsets()[domain.StreamEvent][0]; v != 30 {
		t.Errorf("domain.DefaultRecurrenceOffsets mutated via api.Settings: %d", v)
	}
}

func TestSchedulerRunWithoutRunner(t *testing.T) {
	srv, _ := newTestServer(t, "admin@x.id")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, devReq(t, "POST", "/api/v1/scheduler/run", "admin@x.id", ""))
	if w.Code != 503 {
		t.Errorf("without runner: %d", w.Code)
	}
	_ = context.Background()
}

// offsets:{} is a RESET signal to the global default (not "keep the old value"):
// the per-stream map is persisted verbatim — {} → an empty map, not null/dropped,
// so the reset is PERSISTED. Consumers — internal/api/upcoming.go and
// internal/scheduler/scheduler.go — treat a stream without a list as "use the
// resolved default"; this test locks down both sides of that contract.
func TestPrefsOffsetsResetToDefault(t *testing.T) {
	srv, _ := newTestServer(t, "admin@x.id")
	cid := createContact(t, srv, "admin@x.id", `{"name":"Made"}`)

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, devReq(t, "PUT", "/api/v1/contacts/"+cid+"/prefs", "admin@x.id",
		`{"offsets":{},"channel_ids":[],"enabled":true}`))
	if w.Code != 200 {
		t.Fatalf("put prefs with empty offsets: %d %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	srv.ServeHTTP(w, devReq(t, "GET", "/api/v1/contacts/"+cid, "admin@x.id", ""))
	if w.Code != 200 {
		t.Fatalf("get contact: %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	var got store.ContactWithOccasions
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Prefs == nil {
		t.Fatalf("prefs missing after reset: %s", body)
	}
	if !got.Prefs.Enabled {
		t.Errorf("enabled must stay true: %+v", *got.Prefs)
	}
	if len(got.Prefs.Offsets) != 0 {
		t.Errorf("offsets must be empty (reset to default), got %v", got.Prefs.Offsets)
	}
	if !strings.Contains(body, `"offsets":{}`) {
		t.Errorf(`prefs must contain "offsets":{} (not null/missing): %s`, body)
	}

	// Consumer side: an otonan occurrence exactly today (base = today − 210) must
	// use the global default reminders because prefs.offsets is empty.
	loc, _ := time.LoadLocation("Asia/Makassar")
	today := domain.DateFromTime(time.Now().In(loc))
	ocBody, _ := json.Marshal(map[string]string{"type": "otonan", "date": today.AddDays(-domain.PawukonCycleDays).String()})
	addOccasion(t, srv, "admin@x.id", cid, string(ocBody))
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, devReq(t, "GET", "/api/v1/upcoming?days=30", "admin@x.id", ""))
	if w.Code != 200 {
		t.Fatalf("upcoming: %d %s", w.Code, w.Body.String())
	}
	var up struct {
		Items []UpcomingItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &up); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range up.Items {
		if it.Kind == "occasion" && it.ContactID == cid {
			found = true
			if !reflect.DeepEqual(it.Reminders, domain.DefaultOffsets) {
				t.Errorf("reminders must be the global default %v, got %v", domain.DefaultOffsets, it.Reminders)
			}
		}
	}
	if !found {
		t.Errorf("contact occasion did not appear in /upcoming: %s", w.Body.String())
	}
}

// The contact-level prefs wire shape is the per-stream map: a sent map replaces
// the stored one, an omitted map keeps it (contact-level PUT merges — see
// TestSetPrefsPartialMerge), while the old flat list and unknown streams /
// out-of-range offsets are rejected.
func TestSetPrefsMapShape(t *testing.T) {
	srv, _ := newTestServer(t, "admin@x.id")
	cid := createContact(t, srv, "admin@x.id", `{"name":"Made"}`)

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, devReq(t, "PUT", "/api/v1/contacts/"+cid+"/prefs", "admin@x.id",
		`{"offsets":{"monthly":[2,0]},"enabled":true}`))
	if w.Code != 200 {
		t.Fatalf("put prefs: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Offsets domain.OffsetMap `json:"offsets"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out.Offsets[domain.StreamMonthly], []int{2, 0}) {
		t.Errorf("offsets[monthly] = %v, want [2 0]", out.Offsets[domain.StreamMonthly])
	}
	if len(out.Offsets[domain.StreamYearly]) != 0 {
		t.Errorf("a stream without a list must stay unset (inherit): %v", out.Offsets)
	}

	// The old flat list is not part of the wire shape anymore.
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, devReq(t, "PUT", "/api/v1/contacts/"+cid+"/prefs", "admin@x.id",
		`{"offsets":[3,1,0],"enabled":true}`))
	if w.Code != 400 {
		t.Errorf("flat offsets list must be 400, got %d %s", w.Code, w.Body.String())
	}
	// Unknown streams and out-of-range offsets are rejected.
	for _, body := range []string{
		`{"offsets":{"weekly":[1]},"enabled":true}`,
		`{"offsets":{"monthly":[61]},"enabled":true}`,
	} {
		w = httptest.NewRecorder()
		srv.ServeHTTP(w, devReq(t, "PUT", "/api/v1/contacts/"+cid+"/prefs", "admin@x.id", body))
		if w.Code != 400 {
			t.Errorf("%s must be 400, got %d %s", body, w.Code, w.Body.String())
		}
	}
	// An omitted offsets map keeps the stored one (merge); an explicit {} is
	// the reset to inherit-all. Fresh decodes: json.Unmarshal merges into an
	// existing map.
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, devReq(t, "PUT", "/api/v1/contacts/"+cid+"/prefs", "admin@x.id", `{"enabled":true}`))
	if w.Code != 200 {
		t.Fatalf("put prefs without offsets: %d %s", w.Code, w.Body.String())
	}
	var after struct {
		Offsets domain.OffsetMap `json:"offsets"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after.Offsets[domain.StreamMonthly], []int{2, 0}) {
		t.Errorf("an omitted offsets map must keep the stored one, got %v", after.Offsets)
	}
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, devReq(t, "PUT", "/api/v1/contacts/"+cid+"/prefs", "admin@x.id", `{"offsets":{}}`))
	if w.Code != 200 {
		t.Fatalf("reset prefs: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"offsets":{}`) {
		t.Errorf(`the reset response must contain "offsets":{}: %s`, w.Body.String())
	}
}

// PUT at the contact level MERGES: only the fields actually sent change and the
// stored row supplies the rest (the SPA toggles a channel with a
// channel_ids-only PUT). An explicit "offsets":{} is the inherit-all reset.
func TestSetPrefsPartialMerge(t *testing.T) {
	srv, _ := newTestServer(t, "admin@x.id")
	cid := createContact(t, srv, "admin@x.id", `{"name":"Made"}`)
	chID := createChannel(t, srv, "admin@x.id",
		`{"type":"gotify","name":"home","config":{"base_url":"https://g.x.id","token":"t"}}`)

	put := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, devReq(t, "PUT", "/api/v1/contacts/"+cid+"/prefs", "admin@x.id", body))
		return w
	}
	prefs := func() store.ReminderPrefs {
		t.Helper()
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, devReq(t, "GET", "/api/v1/contacts/"+cid, "admin@x.id", ""))
		if w.Code != 200 {
			t.Fatalf("get contact: %d %s", w.Code, w.Body.String())
		}
		var cw store.ContactWithOccasions
		if err := json.Unmarshal(w.Body.Bytes(), &cw); err != nil {
			t.Fatal(err)
		}
		if cw.Prefs == nil {
			t.Fatal("prefs missing")
		}
		return *cw.Prefs
	}

	// Stored row: a custom yearly override on a PAUSED contact.
	if w := put(`{"offsets":{"yearly":[1,0]},"enabled":false}`); w.Code != 200 {
		t.Fatalf("seed prefs: %d %s", w.Code, w.Body.String())
	}

	// Channel-only PUT (the frontend's toggle): offsets and enabled survive.
	if w := put(`{"channel_ids":["` + chID + `"]}`); w.Code != 200 {
		t.Fatalf("channel-only put: %d %s", w.Code, w.Body.String())
	}
	p := prefs()
	if !reflect.DeepEqual(p.Offsets[domain.StreamYearly], []int{1, 0}) {
		t.Errorf("channel-only PUT wiped the stored offsets: %v", p.Offsets)
	}
	if p.Enabled {
		t.Error("channel-only PUT must not re-enable a paused prefs row")
	}
	if !reflect.DeepEqual(p.ChannelIDs, []string{chID}) {
		t.Errorf("channel_ids = %v, want [%s]", p.ChannelIDs, chID)
	}

	// Offsets-only PUT: channels and enabled stay untouched.
	if w := put(`{"offsets":{"monthly":[0]}}`); w.Code != 200 {
		t.Fatalf("offsets-only put: %d %s", w.Code, w.Body.String())
	}
	p = prefs()
	if !reflect.DeepEqual(p.Offsets[domain.StreamMonthly], []int{0}) {
		t.Errorf("offsets.monthly = %v, want [0]", p.Offsets[domain.StreamMonthly])
	}
	if _, ok := p.Offsets[domain.StreamYearly]; ok {
		t.Errorf("a sent map must replace the stored one: %v", p.Offsets)
	}
	if !reflect.DeepEqual(p.ChannelIDs, []string{chID}) {
		t.Errorf("offsets-only PUT wiped channel_ids: %v", p.ChannelIDs)
	}
	if p.Enabled {
		t.Error("offsets-only PUT must not re-enable a paused prefs row")
	}

	// Explicit {} is the reset to inherit-all.
	if w := put(`{"offsets":{}}`); w.Code != 200 {
		t.Fatalf("reset put: %d %s", w.Code, w.Body.String())
	}
	if p = prefs(); len(p.Offsets) != 0 {
		t.Errorf(`explicit "offsets":{} must reset to inherit-all, got %v`, p.Offsets)
	}
}

func TestUpcomingDateRange(t *testing.T) {
	srv, _ := newTestServer(t, "admin@x.id")
	cid := createContact(t, srv, "admin@x.id", `{"name":"Made"}`)
	addOccasion(t, srv, "admin@x.id", cid, `{"type":"birthday","date":"2003-06-03"}`)

	get := func(query string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, devReq(t, "GET", "/api/v1/upcoming"+query, "admin@x.id", ""))
		return w
	}
	hasBirthday := func(w *httptest.ResponseRecorder) bool {
		if w.Code != 200 {
			t.Fatalf("upcoming: %d %s", w.Code, w.Body.String())
		}
		var up struct {
			Items []UpcomingItem `json:"items"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &up); err != nil {
			t.Fatal(err)
		}
		for _, it := range up.Items {
			if it.Kind == "occasion" && it.Type == "birthday" && it.Date.String() == "2027-06-03" {
				return true
			}
		}
		return false
	}

	// Full year: the June 3, 2027 birthday must appear even though it falls
	// far outside the 30-day window from today.
	if w := get("?from=2027-01-01&to=2027-12-31"); !hasBirthday(w) {
		t.Errorf("birthday 2027-06-03 missing from the one-year range: %s", w.Body.String())
	}
	// `to` is optional: defaults to one year from `from`.
	if w := get("?from=2027-06-01"); !hasBirthday(w) {
		t.Errorf("birthday 2027-06-03 missing from from without to: %s", w.Body.String())
	}
	// Reversed range → 400.
	if w := get("?from=2027-12-31&to=2027-01-01"); w.Code != 400 {
		t.Errorf("inverted range must be 400, got %d", w.Code)
	}
	// Range > 400 days → 400.
	if w := get("?from=2027-01-01&to=2028-03-01"); w.Code != 400 {
		t.Errorf("range >400 days must be 400, got %d", w.Code)
	}
	// from is not a date → 400.
	if w := get("?from=not-a-date"); w.Code != 400 {
		t.Errorf("invalid from must be 400, got %d", w.Code)
	}
}

// ---- Task 8: occasion recurrence, occasion-prefs endpoints, stream-aware upcoming ----

// postOccasion posts an occasion and decodes the response (empty on error).
func postOccasion(t *testing.T, srv *Server, email, contactID, body string) (int, store.Occasion) {
	t.Helper()
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, devReq(t, "POST", "/api/v1/contacts/"+contactID+"/occasions", email, body))
	var oc store.Occasion
	_ = json.Unmarshal(w.Body.Bytes(), &oc)
	return w.Code, oc
}

// contactOccasion reads one occasion (with its prefs) back through GET /contacts/{id}.
func contactOccasion(t *testing.T, srv *Server, email, cid, occID string) store.Occasion {
	t.Helper()
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, devReq(t, "GET", "/api/v1/contacts/"+cid, email, ""))
	if w.Code != 200 {
		t.Fatalf("get contact: %d %s", w.Code, w.Body.String())
	}
	var cw store.ContactWithOccasions
	if err := json.Unmarshal(w.Body.Bytes(), &cw); err != nil {
		t.Fatal(err)
	}
	for _, o := range cw.Occasions {
		if o.ID == occID {
			return o
		}
	}
	t.Fatalf("occasion %s not on contact %s: %s", occID, cid, w.Body.String())
	return store.Occasion{}
}

// Custom types are first-class: any non-empty type ≤64 chars is accepted, the
// recurrence defaults per type (custom/unknown → yearly) when omitted and an
// explicit valid recurrence is stored verbatim.
func TestAddOccasionCustomTypeAndRecurrence(t *testing.T) {
	srv, _ := newTestServer(t, "admin@x.id")
	cid := createContact(t, srv, "admin@x.id", `{"name":"Made"}`)

	code, oc := postOccasion(t, srv, "admin@x.id", cid,
		`{"type":"wedding","date":"2025-06-16","recurrence":"anniversary","label":"wedding"}`)
	if code != 201 {
		t.Fatalf("custom type + recurrence: %d", code)
	}
	if oc.Recurrence != domain.RecurAnniversary {
		t.Errorf("recurrence = %q, want anniversary", oc.Recurrence)
	}
	if _, err := uuid.Parse(oc.ID); err != nil {
		t.Errorf("occasion id is not a uuid: %q", oc.ID)
	}
	if oc.Label != "wedding" || oc.Type != "wedding" {
		t.Errorf("type/label = %q/%q, want wedding/wedding", oc.Type, oc.Label)
	}

	code, oc = postOccasion(t, srv, "admin@x.id", cid, `{"type":"graduation","date":"2025-06-16"}`)
	if code != 201 {
		t.Fatalf("custom type without recurrence: %d", code)
	}
	if oc.Recurrence != domain.RecurYearly {
		t.Errorf("custom type default recurrence = %q, want yearly", oc.Recurrence)
	}

	// Built-in types keep their own defaults.
	code, oc = postOccasion(t, srv, "admin@x.id", cid, `{"type":"otonan","date":"2025-06-16"}`)
	if code != 201 || oc.Recurrence != domain.RecurOtonan {
		t.Errorf("otonan default recurrence = %q (code %d), want otonan", oc.Recurrence, code)
	}

	// Unknown recurrence → 400, empty type → 400, over-long type → 400.
	for _, body := range []string{
		`{"type":"wedding","date":"2025-06-16","recurrence":"weekly"}`,
		`{"type":"","date":"2025-06-16"}`,
		`{"type":"` + strings.Repeat("x", 65) + `","date":"2025-06-16"}`,
	} {
		if code, _ := postOccasion(t, srv, "admin@x.id", cid, body); code != 400 {
			t.Errorf("%s must be 400, got %d", body, code)
		}
	}
}

// The per-occasion prefs endpoints: default payload without a row, round-trip
// through GET /contacts/{id}, strict offsets validation, DELETE, and 404 for
// unknown or malformed occasion ids.
func TestOccasionPrefsEndpoints(t *testing.T) {
	srv, _ := newTestServer(t, "admin@x.id")
	cid := createContact(t, srv, "admin@x.id", `{"name":"Made"}`)
	occID := addOccasion(t, srv, "admin@x.id", cid,
		`{"type":"anniversary","date":"2025-06-16","recurrence":"anniversary","label":"wedding"}`)

	prefsReq := func(method, id, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, devReq(t, method, "/api/v1/occasions/"+id+"/prefs", "admin@x.id", body))
		return w
	}

	// No row yet → the inherit-all default, not a 404.
	w := prefsReq("GET", occID, "")
	if w.Code != 200 {
		t.Fatalf("get prefs without a row: %d %s", w.Code, w.Body.String())
	}
	var p store.OccasionPrefs
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if !p.Enabled || len(p.Offsets) != 0 || p.ChannelIDs == nil {
		t.Errorf("default prefs payload = %+v, want enabled/empty offsets/[]", p)
	}

	// PUT → 200, visible on the contact with the map shape intact.
	w = prefsReq("PUT", occID, `{"offsets":{"monthly":[1,0]},"channel_ids":[],"enabled":false}`)
	if w.Code != 200 {
		t.Fatalf("put prefs: %d %s", w.Code, w.Body.String())
	}
	oc := contactOccasion(t, srv, "admin@x.id", cid, occID)
	if oc.Prefs == nil {
		t.Fatalf("occasion.prefs missing after PUT")
	}
	if oc.Prefs.Enabled {
		t.Errorf("occasion.prefs.enabled = true, want false")
	}
	if !reflect.DeepEqual(oc.Prefs.Offsets[domain.StreamMonthly], []int{1, 0}) {
		t.Errorf("occasion.prefs.offsets.monthly = %v, want [1 0]", oc.Prefs.Offsets[domain.StreamMonthly])
	}

	// Out-of-range offset → 400 and the stored row is left untouched.
	if w := prefsReq("PUT", occID, `{"offsets":{"monthly":[61]}}`); w.Code != 400 {
		t.Errorf("offset 61 must be 400, got %d %s", w.Code, w.Body.String())
	}
	if oc := contactOccasion(t, srv, "admin@x.id", cid, occID); !reflect.DeepEqual(oc.Prefs.Offsets[domain.StreamMonthly], []int{1, 0}) {
		t.Errorf("rejected PUT must not alter the stored prefs: %v", oc.Prefs.Offsets)
	}
	// Unknown stream key → 400.
	if w := prefsReq("PUT", occID, `{"offsets":{"weekly":[1]}}`); w.Code != 400 {
		t.Errorf("unknown stream must be 400, got %d %s", w.Code, w.Body.String())
	}

	// DELETE → 200, the occasion is back to inherit (prefs null).
	if w := prefsReq("DELETE", occID, ""); w.Code != 200 {
		t.Errorf("delete prefs: %d %s", w.Code, w.Body.String())
	}
	if oc := contactOccasion(t, srv, "admin@x.id", cid, occID); oc.Prefs != nil {
		t.Errorf("occasion.prefs must be null after DELETE, got %+v", *oc.Prefs)
	}
	if w := prefsReq("GET", occID, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"enabled":true`) {
		t.Errorf("prefs after DELETE must fall back to the default payload: %d %s", w.Code, w.Body.String())
	}

	// Unknown and malformed ids are 404 on every verb.
	unknown := uuid.NewString()
	for _, tc := range []struct{ method, id, body string }{
		{"GET", unknown, ""},
		{"PUT", unknown, `{"offsets":{}}`},
		{"DELETE", unknown, ""},
		{"GET", "not-a-uuid", ""},
		{"PUT", "not-a-uuid", `{"offsets":{}}`},
		{"DELETE", "not-a-uuid", ""},
	} {
		if w := prefsReq(tc.method, tc.id, tc.body); w.Code != 404 {
			t.Errorf("%s /occasions/%s/prefs = %d, want 404", tc.method, tc.id, w.Code)
		}
	}
}

// The custom flag: default GET payload says inherit (custom=false), a legacy
// PUT without the field lands as custom=true, and PUT custom=false retains
// the stored offsets/channels instead of wiping them.
func TestOccasionPrefsCustomFlag(t *testing.T) {
	srv, _ := newTestServer(t, "admin@x.id")
	cid := createContact(t, srv, "admin@x.id", `{"name":"Made"}`)
	occID := addOccasion(t, srv, "admin@x.id", cid,
		`{"type":"birthday","date":"2025-06-16","recurrence":"yearly"}`)

	prefsReq := func(method, id, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, devReq(t, method, "/api/v1/occasions/"+id+"/prefs", "admin@x.id", body))
		return w
	}

	// No row → the inherit default carries custom:false.
	w := prefsReq("GET", occID, "")
	if w.Code != 200 {
		t.Fatalf("rowless prefs GET = %d, want 200: %s", w.Code, w.Body.String())
	}
	var d store.OccasionPrefs
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if d.Custom {
		t.Errorf("default payload custom = true, want false: %s", w.Body.String())
	}

	// Legacy payload (no custom field) → custom=true on the stored row.
	if w := prefsReq("PUT", occID, `{"offsets":{"yearly":[7]},"channel_ids":[],"enabled":true}`); w.Code != 200 {
		t.Fatalf("legacy put: %d %s", w.Code, w.Body.String())
	}
	if oc := contactOccasion(t, srv, "admin@x.id", cid, occID); oc.Prefs == nil || !oc.Prefs.Custom {
		t.Errorf("legacy put must store custom=true, got %+v", oc.Prefs)
	}

	// PUT custom=false keeps the values (they are retained, not wiped).
	// Limit: this proves the flag write does not wipe; preservation of values
	// across omitted fields is full-replace semantics (the store test covers
	// row survival).
	w = prefsReq("PUT", occID, `{"offsets":{"yearly":[7]},"channel_ids":[],"enabled":true,"custom":false}`)
	if w.Code != 200 {
		t.Fatalf("put custom=false: %d %s", w.Code, w.Body.String())
	}
	oc := contactOccasion(t, srv, "admin@x.id", cid, occID)
	if oc.Prefs == nil || oc.Prefs.Custom {
		t.Fatalf("custom = %+v, want false with values retained", oc.Prefs)
	}
	if !reflect.DeepEqual(oc.Prefs.Offsets[domain.StreamYearly], []int{7}) {
		t.Errorf("offsets = %v, want [7] retained", oc.Prefs.Offsets[domain.StreamYearly])
	}

	// Flipping back on reactivates the retained values.
	if w := prefsReq("PUT", occID, `{"offsets":{"yearly":[7]},"channel_ids":[],"enabled":true,"custom":true}`); w.Code != 200 {
		t.Fatalf("put custom=true: %d %s", w.Code, w.Body.String())
	}
	if oc := contactOccasion(t, srv, "admin@x.id", cid, occID); oc.Prefs == nil || !oc.Prefs.Custom {
		t.Errorf("custom = %+v, want true", oc.Prefs)
	}
}

// Path ids are canonicalized before they reach the store: SQLite compares ids
// with the BINARY collation, so an uppercase (pasted) UUID must resolve to the
// same row as the stored lowercase form instead of 404ing — on contacts and on
// the per-occasion endpoints alike.
func TestPathIDUppercaseResolves(t *testing.T) {
	srv, _ := newTestServer(t, "admin@x.id")
	cid := createContact(t, srv, "admin@x.id", `{"name":"Made"}`)
	occID := addOccasion(t, srv, "admin@x.id", cid,
		`{"type":"birthday","date":"1990-05-12","recurrence":"yearly"}`)

	// GET contact with the id uppercased → the same contact.
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, devReq(t, "GET", "/api/v1/contacts/"+strings.ToUpper(cid), "admin@x.id", ""))
	if w.Code != 200 {
		t.Fatalf("GET contact with uppercase id: %d %s", w.Code, w.Body.String())
	}
	var c store.Contact
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	if c.ID != cid {
		t.Errorf("contact id = %q, want %q", c.ID, cid)
	}

	// A write through the uppercased occasion id lands on the stored row
	// (read back through the canonical id).
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, devReq(t, "PUT", "/api/v1/occasions/"+strings.ToUpper(occID)+"/prefs", "admin@x.id",
		`{"offsets":{"yearly":[1,0]}}`))
	if w.Code != 200 {
		t.Fatalf("PUT occasion prefs with uppercase id: %d %s", w.Code, w.Body.String())
	}
	oc := contactOccasion(t, srv, "admin@x.id", cid, occID)
	if oc.Prefs == nil || !reflect.DeepEqual(oc.Prefs.Offsets[domain.StreamYearly], []int{1, 0}) {
		t.Errorf("uppercase-id PUT did not reach the stored occasion: %+v", oc.Prefs)
	}
}

// Suggestions: the caller's used types plus the built-ins, deduped and stable.
func TestOccasionTypesSuggestions(t *testing.T) {
	srv, _ := newTestServer(t, "admin@x.id")
	cid := createContact(t, srv, "admin@x.id", `{"name":"Made"}`)
	addOccasion(t, srv, "admin@x.id", cid, `{"type":"wedding","date":"2025-06-16"}`)
	addOccasion(t, srv, "admin@x.id", cid, `{"type":"birthday","date":"1990-05-12"}`)

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, devReq(t, "GET", "/api/v1/occasions/types", "admin@x.id", ""))
	if w.Code != 200 {
		t.Fatalf("occasion types: %d %s", w.Code, w.Body.String())
	}
	var got struct {
		Types []string `json:"types"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := []string{"birthday", "otonan", "anniversary", "wedding"}
	if !reflect.DeepEqual(got.Types, want) {
		t.Errorf("types = %v, want %v", got.Types, want)
	}
}

// Every occasion endpoint is owner-scoped: another user gets 404 on the
// occasion's prefs and never sees the owner's custom type in the suggestions.
func TestOccasionEndpointsOwnerScoped(t *testing.T) {
	srv, _ := newTestServer(t, "admin@x.id")
	cid := createContact(t, srv, "bob@x.id", `{"name":"Bob"}`)
	occID := addOccasion(t, srv, "bob@x.id", cid, `{"type":"wedding","date":"2025-06-16"}`)

	for _, tc := range []struct{ method, body string }{
		{"GET", ""},
		{"PUT", `{"offsets":{"monthly":[0]}}`},
		{"DELETE", ""},
	} {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, devReq(t, tc.method, "/api/v1/occasions/"+occID+"/prefs", "eve@x.id", tc.body))
		if w.Code != 404 {
			t.Errorf("%s another owner's occasion prefs = %d, want 404", tc.method, w.Code)
		}
	}
	// A PUT from another owner must not write anything.
	if oc := contactOccasion(t, srv, "bob@x.id", cid, occID); oc.Prefs != nil {
		t.Errorf("another user's PUT leaked into the owner's prefs: %+v", *oc.Prefs)
	}

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, devReq(t, "GET", "/api/v1/occasions/types", "eve@x.id", ""))
	if w.Code != 200 {
		t.Fatalf("occasion types: %d %s", w.Code, w.Body.String())
	}
	var got struct {
		Types []string `json:"types"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for _, t2 := range got.Types {
		if t2 == "wedding" {
			t.Errorf("another owner's custom type leaked into the suggestions: %v", got.Types)
		}
	}
}

// PATCH /occasions/{id} replaces the editable fields, keeps prefs, falls back
// to the type's default recurrence when the body omits one, rejects bad
// input with 400, and is owner-scoped like every other occasion endpoint.
func TestUpdateOccasion(t *testing.T) {
	srv, _ := newTestServer(t, "admin@x.id")
	cid := createContact(t, srv, "admin@x.id", `{"name":"Made"}`)
	occID := addOccasion(t, srv, "admin@x.id", cid, `{"type":"birthday","date":"2000-02-29"}`)

	patch := func(email, body string) int {
		t.Helper()
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, devReq(t, "PATCH", "/api/v1/occasions/"+occID, email, body))
		return w.Code
	}

	// Full edit: type, date, recurrence and label all change.
	if code := patch("admin@x.id", `{"type":"anniversary","date":"1999-06-16","recurrence":"anniversary","label":"Wedding"}`); code != 200 {
		t.Fatalf("patch: %d", code)
	}
	oc := contactOccasion(t, srv, "admin@x.id", cid, occID)
	if oc.Type != "anniversary" || oc.Label != "Wedding" {
		t.Errorf("patched occasion = %q/%q, want anniversary/Wedding", oc.Type, oc.Label)
	}
	if oc.BaseDate.String() != "1999-06-16" {
		t.Errorf("patched base_date = %s, want 1999-06-16", oc.BaseDate)
	}
	if oc.Recurrence != domain.RecurAnniversary {
		t.Errorf("patched recurrence = %q, want anniversary", oc.Recurrence)
	}

	// Omitted recurrence → the new type's default (birthday → yearly).
	if code := patch("admin@x.id", `{"type":"birthday","date":"2000-02-29"}`); code != 200 {
		t.Fatalf("patch without recurrence: %d", code)
	}
	if oc := contactOccasion(t, srv, "admin@x.id", cid, occID); oc.Recurrence != domain.RecurYearly {
		t.Errorf("recurrence = %q, want default yearly", oc.Recurrence)
	}

	// Bad input → 400, stored row unchanged.
	for _, tc := range []struct{ name, body string }{
		{"missing type", `{"date":"2000-01-01"}`},
		{"type too long", `{"type":"` + strings.Repeat("x", 65) + `","date":"2000-01-01"}`},
		{"bad recurrence", `{"type":"birthday","date":"2000-01-01","recurrence":"weekly"}`},
		{"bad date", `{"type":"birthday","date":"not-a-date"}`},
	} {
		if code := patch("admin@x.id", tc.body); code != 400 {
			t.Errorf("%s: code = %d, want 400", tc.name, code)
		}
	}
	if oc := contactOccasion(t, srv, "admin@x.id", cid, occID); oc.Recurrence != domain.RecurYearly {
		t.Errorf("failed patches changed the stored row: recurrence = %q", oc.Recurrence)
	}

	// Prefs set before a patch survive it.
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, devReq(t, "PUT", "/api/v1/occasions/"+occID+"/prefs", "admin@x.id", `{"offsets":{"yearly":[0]}}`))
	if w.Code != 200 {
		t.Fatalf("set prefs: %d %s", w.Code, w.Body.String())
	}
	if code := patch("admin@x.id", `{"type":"otonan","date":"2000-01-01","recurrence":"otonan"}`); code != 200 {
		t.Fatalf("patch with prefs: %d", code)
	}
	if oc := contactOccasion(t, srv, "admin@x.id", cid, occID); oc.Prefs == nil || !reflect.DeepEqual(oc.Prefs.Offsets, domain.OffsetMap{"yearly": {0}}) {
		t.Errorf("patch clobbered prefs: %+v", oc.Prefs)
	}

	// Another owner gets 404 and writes nothing; unknown id → 404.
	if code := patch("eve@x.id", `{"type":"birthday","date":"2000-01-01"}`); code != 404 {
		t.Errorf("another owner's patch = %d, want 404", code)
	}
	if oc := contactOccasion(t, srv, "admin@x.id", cid, occID); oc.Type != "otonan" {
		t.Errorf("another owner's patch leaked: type = %q", oc.Type)
	}
	w2 := httptest.NewRecorder()
	srv.ServeHTTP(w2, devReq(t, "PATCH", "/api/v1/occasions/"+uuid.NewString(), "admin@x.id", `{"type":"birthday","date":"2000-01-01"}`))
	if w2.Code != 404 {
		t.Errorf("patch unknown occasion = %d, want 404", w2.Code)
	}
}

// Upcoming items carry the occasion's recurrence and the stream-resolved
// reminders: an anniversary's monthly mark uses the monthly set, its yearly
// mark the yearly set, and occasion prefs override per stream only.
func TestUpcomingAnniversaryStreams(t *testing.T) {
	srv, _ := newTestServer(t, "admin@x.id")
	cid := createContact(t, srv, "admin@x.id", `{"name":"Made"}`)
	occID := addOccasion(t, srv, "admin@x.id", cid,
		`{"type":"anniversary","date":"2025-06-16","recurrence":"anniversary","label":"wedding"}`)

	itemOn := func(items []UpcomingItem, date string) *UpcomingItem {
		for i := range items {
			if items[i].Kind == "occasion" && items[i].Date.String() == date {
				return &items[i]
			}
		}
		return nil
	}

	// Monthly mark k=6 (2025-12-16): stream monthly → the monthly default [0].
	m := itemOn(upcomingItems(t, srv, "?from=2025-12-01&to=2025-12-31"), "2025-12-16")
	if m == nil {
		t.Fatal("monthly mark missing from /upcoming")
	}
	if m.Recurrence != domain.RecurAnniversary {
		t.Errorf("recurrence = %q, want anniversary", m.Recurrence)
	}
	if !reflect.DeepEqual(m.Reminders, []int{0}) {
		t.Errorf("monthly reminders = %v, want [0]", m.Reminders)
	}
	if !m.RemindersDefault {
		t.Error("monthly stream with no overrides must be reminders_default")
	}
	if m.Title != "Anniversary 6 months" {
		t.Errorf("monthly title = %q, want \"Anniversary 6 months\"", m.Title)
	}

	// Yearly mark k=12 (2026-06-16): stream yearly → the yearly default set.
	y := itemOn(upcomingItems(t, srv, "?from=2026-06-01&to=2026-06-30"), "2026-06-16")
	if y == nil {
		t.Fatal("yearly mark missing from /upcoming")
	}
	if !reflect.DeepEqual(y.Reminders, []int{30, 7, 4, 2, 1, 0}) {
		t.Errorf("yearly reminders = %v, want [30 7 4 2 1 0]", y.Reminders)
	}
	if !y.RemindersDefault {
		t.Error("yearly stream with no overrides must be reminders_default")
	}
	if y.Title != "Anniversary 1 year" {
		t.Errorf("yearly title = %q, want \"Anniversary 1 year\"", y.Title)
	}

	// Occasion prefs apply to their stream only: monthly [1,0], yearly stays default.
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, devReq(t, "PUT", "/api/v1/occasions/"+occID+"/prefs", "admin@x.id",
		`{"offsets":{"monthly":[1,0]},"channel_ids":[],"enabled":true}`))
	if w.Code != 200 {
		t.Fatalf("put occasion prefs: %d %s", w.Code, w.Body.String())
	}
	m = itemOn(upcomingItems(t, srv, "?from=2025-12-01&to=2025-12-31"), "2025-12-16")
	if m == nil || !reflect.DeepEqual(m.Reminders, []int{1, 0}) || m.RemindersDefault {
		t.Errorf("monthly with occasion prefs = %+v, want reminders [1 0] and default=false", m)
	}
	y = itemOn(upcomingItems(t, srv, "?from=2026-06-01&to=2026-06-30"), "2026-06-16")
	if y == nil || !reflect.DeepEqual(y.Reminders, []int{30, 7, 4, 2, 1, 0}) || !y.RemindersDefault {
		t.Errorf("yearly must keep the default set = %+v", y)
	}
}
