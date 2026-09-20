package scheduler

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"wiminder/internal/calendarprov"
	"wiminder/internal/domain"
	"wiminder/internal/notify"
	"wiminder/internal/store"
)

type stubNotifier struct {
	sent []notify.Message
	err  error
}

func (s *stubNotifier) Name() string { return "stub" }
func (s *stubNotifier) Send(_ context.Context, m notify.Message) error {
	if s.err != nil {
		return s.err
	}
	s.sent = append(s.sent, m)
	return nil
}
func (s *stubNotifier) Test(_ context.Context) error { return nil }

type stubProvider struct {
	hs  []domain.Holiday
	cat string // optional; "" → pawukon
}

func (s *stubProvider) Name() string { return "stub" }
func (s *stubProvider) Category() string {
	if s.cat == "" {
		return "pawukon"
	}
	return s.cat
}
func (s *stubProvider) HolidaysBetween(_ context.Context, _, _ domain.Date) ([]domain.Holiday, error) {
	return s.hs, nil
}

// failProvider: always errors — models the remote being down.
type failProvider struct{ cat string }

func (f failProvider) Name() string     { return "fail" }
func (f failProvider) Category() string { return f.cat }
func (f failProvider) HolidaysBetween(_ context.Context, _, _ domain.Date) ([]domain.Holiday, error) {
	return nil, errors.New("remote down")
}

func snapUTC() Snapshot {
	return Snapshot{Timezone: "UTC", SendTime: "08:00", CatchUpHours: 24,
		DefaultOffsets:    domain.DefaultOffsets,
		HolidayCategories: map[string]bool{"pawukon": true, "saka": true, "national": true}}
}

// seed: user@1, contact, otonan base = today-210 (occurrence EXACTLY on `today`),
// 1 gotify channel.
func seed(t *testing.T, st *store.Store, today domain.Date) {
	t.Helper()
	ctx := context.Background()
	u, err := st.GetOrCreateUser(ctx, "budi@x.id", "Budi", nil)
	if err != nil {
		t.Fatal(err)
	}
	c, err := st.CreateContact(ctx, u.ID, "Made", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddOccasion(ctx, c.ID, domain.Otonan, domain.RecurOtonan, today.AddDays(-domain.PawukonCycleDays), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateChannel(ctx, u.ID, "gotify", "home", []byte("enc")); err != nil {
		t.Fatal(err)
	}
}

type harness struct {
	st    *store.Store
	fc    *FakeClock
	notif *stubNotifier
	svc   *Service
}

// newBareHarness wires store, clock, notifier and service without seed(): the
// per-occasion scenarios bring their own contact so Result counters and
// captured pushes stay exact.
func newBareHarness(t *testing.T, now time.Time) *harness {
	t.Helper()
	st, err := store.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	fc := &FakeClock{T: now}
	n := &stubNotifier{}
	svc := &Service{St: st, Clock: fc, Providers: []calendarprov.Provider{&stubProvider{}},
		Resolve:   func(_ context.Context, ch store.Channel) (notify.Notifier, error) { return n, nil },
		failUntil: map[string]time.Time{}}
	return &harness{st: st, fc: fc, notif: n, svc: svc}
}

func newHarness(t *testing.T, now time.Time) *harness {
	t.Helper()
	h := newBareHarness(t, now)
	seed(t, h.st, domain.DateFromTime(now))
	return h
}

// today at 08:02 UTC → the D offset is sent; D-1..D-7 (the 4 other offsets) → missed.
func TestRunOnceOnTime(t *testing.T) {
	now := time.Date(2026, 6, 17, 8, 2, 0, 0, time.UTC)
	h := newHarness(t, now)
	res, err := h.svc.RunOnce(context.Background(), snapUTC())
	if err != nil {
		t.Fatal(err)
	}
	if res.Sent != 1 || res.Failed != 0 || res.Missed != 4 {
		t.Fatalf("res = %+v, want Sent1 Missed4", res)
	}
	if len(h.notif.sent) != 1 {
		t.Fatalf("notif = %d", len(h.notif.sent))
	}
	if strings.Contains(h.notif.sent[0].Body, "Sent late") {
		t.Error("must not be late")
	}

	// 2nd run → everything is deduped
	res, _ = h.svc.RunOnce(context.Background(), snapUTC())
	if res.Sent != 0 || res.Missed != 0 {
		t.Errorf("dedupe failed: %+v", res)
	}
	// PRE-SEND dedupe: the stub must not be called again — the per-minute
	// scanner must not re-push the same reminder over and over.
	if len(h.notif.sent) != 1 {
		t.Errorf("stub called %d times after run 2, must stay 1 (double spam)", len(h.notif.sent))
	}
}

// at 07:00 → the D-1 offset (yesterday 08:00) is still in the window → sent late;
// D-2..D-7 → missed; D is not due yet.
func TestRunOnceCatchUpLate(t *testing.T) {
	now := time.Date(2026, 6, 17, 7, 0, 0, 0, time.UTC)
	h := newHarness(t, now)
	res, err := h.svc.RunOnce(context.Background(), snapUTC())
	if err != nil {
		t.Fatal(err)
	}
	if res.Sent != 1 || res.Missed != 3 {
		t.Fatalf("res = %+v, want Sent1 Missed3", res)
	}
	if !strings.Contains(h.notif.sent[0].Body, "Sent late") {
		t.Errorf("must be late: %q", h.notif.sent[0].Body)
	}
}

// send fails → not recorded → retried after the 15-minute backoff passes.
func TestRunOnceRetryAfterFailure(t *testing.T) {
	now := time.Date(2026, 6, 17, 8, 2, 0, 0, time.UTC)
	h := newHarness(t, now)
	h.notif.err = context.DeadlineExceeded
	res, _ := h.svc.RunOnce(context.Background(), snapUTC())
	if res.Failed != 1 {
		t.Fatalf("failed = %d", res.Failed)
	}

	// 1 minute later: still in backoff → no attempt
	h.fc.Add(time.Minute)
	res, _ = h.svc.RunOnce(context.Background(), snapUTC())
	if res.Failed != 0 || res.Sent != 0 {
		t.Errorf("backoff leaked: %+v", res)
	}

	// 16 minutes later + now successful → sent
	h.fc.Add(16 * time.Minute)
	h.notif.err = nil
	res, _ = h.svc.RunOnce(context.Background(), snapUTC())
	if res.Sent != 1 {
		t.Errorf("retry failed: %+v", res)
	}
}

// targetChannels: the contact's own selection wins; without one, the system
// default channels apply; a default matching nothing enabled falls back to
// every enabled channel.
//
// OpenInMemory shares one in-memory DB across the package's tests (dedupe is
// what keeps re-runs quiet), so this test brings its own user, contact and
// channels instead of relying on seed()'s state.
func TestTargetChannelsSystemDefault(t *testing.T) {
	h := newHarness(t, time.Date(2026, 6, 17, 8, 2, 0, 0, time.UTC))
	ctx := context.Background()
	u, err := h.st.GetOrCreateUser(ctx, "target-channels@x.id", "Tc", nil)
	if err != nil {
		t.Fatal(err)
	}
	chA, err := h.st.CreateChannel(ctx, u.ID, "gotify", "a", []byte("enc"))
	if err != nil {
		t.Fatal(err)
	}
	chB, err := h.st.CreateChannel(ctx, u.ID, "telegram", "b", []byte("enc"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := h.st.CreateContact(ctx, u.ID, "Made", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.AddOccasion(ctx, c.ID, domain.Otonan, domain.RecurOtonan, domain.NewDate(2026, 6, 17), ""); err != nil {
		t.Fatal(err)
	}

	cw := func(t *testing.T) store.ContactWithOccasions {
		t.Helper()
		got, err := h.st.GetContact(ctx, u.ID, c.ID)
		if err != nil {
			t.Fatal(err)
		}
		return *got
	}
	same := func(got []store.Channel, want ...string) bool {
		if len(got) != len(want) {
			return false
		}
		set := map[string]bool{}
		for _, ch := range got {
			set[ch.ID] = true
		}
		for _, id := range want {
			if !set[id] {
				return false
			}
		}
		return true
	}

	// no default configured → every enabled channel
	if got := h.svc.targetChannels(ctx, cw(t), nil); !same(got, chA.ID, chB.ID) {
		t.Errorf("no default: got %v", got)
	}
	// system default → just that channel
	if got := h.svc.targetChannels(ctx, cw(t), []string{chB.ID}); !same(got, chB.ID) {
		t.Errorf("default [B]: got %v", got)
	}
	// default matching nothing enabled → falls back to every enabled channel
	if got := h.svc.targetChannels(ctx, cw(t), []string{uuid.NewString()}); !same(got, chA.ID, chB.ID) {
		t.Errorf("unknown default: got %v", got)
	}

	// the contact's own selection beats the system default
	if err := h.st.SetReminderPrefs(ctx, store.ReminderPrefs{
		ContactID: c.ID, ChannelIDs: []string{chA.ID}, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if got := h.svc.targetChannels(ctx, cw(t), []string{chB.ID}); !same(got, chA.ID) {
		t.Errorf("explicit selection: got %v", got)
	}
}

func TestHolidayReminder(t *testing.T) {
	now := time.Date(2026, 6, 17, 8, 2, 0, 0, time.UTC)
	h := newHarness(t, now)
	h.svc.Providers = []calendarprov.Provider{
		&stubProvider{hs: []domain.Holiday{{Date: domain.NewDate(2026, 6, 17), Name: "Galungan"}}}}
	res, err := h.svc.RunOnce(context.Background(), snapUTC())
	if err != nil {
		t.Fatal(err)
	}
	if res.Sent != 2 {
		t.Fatalf("sent = %d, want 2 (otonan + Galungan)", res.Sent)
	}
	found := false
	for _, m := range h.notif.sent {
		if strings.Contains(m.Title, "Galungan") {
			found = true
		}
	}
	if !found {
		t.Error("Galungan message not sent")
	}
	// dedupe holiday
	res, _ = h.svc.RunOnce(context.Background(), snapUTC())
	if res.Sent != 0 {
		t.Errorf("holiday dedupe failed: %+v", res)
	}
	// PRE-SEND dedupe: total stub calls stay 2 (otonan + Galungan).
	if len(h.notif.sent) != 2 {
		t.Errorf("stub called %d times after run 2, must stay 2 (double spam)", len(h.notif.sent))
	}
}

// Two providers emitting the same normalized holiday — pawukon "Saraswati"
// vs saka "Hari Saraswati" on 2026-10-31. Dedup keeps the FIRST provider's
// entry: exactly one holiday row, using the WINNER's offsets (pawukon {1}
// → sendAt beyond the 24h catch-up window → recorded "missed"); saka's {0}
// copy must not exist. Offsets {0} for the otonan seed → 1 sent.
func TestHolidayDedupAcrossProviders(t *testing.T) {
	now := time.Date(2026, 10, 31, 8, 2, 0, 0, time.UTC)
	h := newHarness(t, now)
	h.svc.Providers = []calendarprov.Provider{
		&stubProvider{cat: "pawukon", hs: []domain.Holiday{
			{Date: domain.NewDate(2026, 10, 31), Name: "Saraswati"}}},
		&stubProvider{cat: "saka", hs: []domain.Holiday{
			{Date: domain.NewDate(2026, 10, 31), Name: "Hari Saraswati"}}},
	}
	snap := snapUTC()
	snap.DefaultOffsets = []int{0}
	snap.HolidayOffsets = map[string][]int{"pawukon": {1}, "saka": {0}}
	// Occasions read their per-stream sets from settings.recurrence_offsets
	// (Task 6), not the legacy global — pin the otonan to [0] so this test
	// keeps asserting only the holiday dedup.
	snap.RecurrenceOffsets = domain.OffsetMap{domain.StreamOtonan: {0}}
	res, err := h.svc.RunOnce(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	if res.Sent != 1 || res.Missed != 1 {
		t.Fatalf("res = %+v, want Sent 1 (otonan) Missed 1 (pawukon Saraswati D-1)", res)
	}
	res, _ = h.svc.RunOnce(context.Background(), snap)
	if res.Sent != 0 || res.Missed != 0 {
		t.Errorf("second run must be fully deduped: %+v", res)
	}
}

// A failing provider must not take the others down: saka errors → skipped,
// pawukon still delivers (and would still win any dedup against it).
func TestHolidayProviderFailureStillSendsOthers(t *testing.T) {
	now := time.Date(2026, 10, 31, 8, 2, 0, 0, time.UTC)
	h := newHarness(t, now)
	h.svc.Providers = []calendarprov.Provider{
		failProvider{cat: "saka"},
		&stubProvider{cat: "pawukon", hs: []domain.Holiday{
			{Date: domain.NewDate(2026, 10, 31), Name: "Saraswati"}}},
	}
	snap := snapUTC()
	snap.DefaultOffsets = []int{0}
	// per-stream settings (Task 6): the otonan seed reminds on the day only.
	snap.RecurrenceOffsets = domain.OffsetMap{domain.StreamOtonan: {0}}
	res, err := h.svc.RunOnce(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	if res.Sent != 2 || res.Missed != 0 {
		t.Fatalf("res = %+v, want Sent 2 (otonan + pawukon Saraswati), Missed 0", res)
	}
}

// The HIGHER-priority provider is absent entirely (only saka configured): the
// lower-priority duplicate must not be swallowed by dedup — it is scheduled
// with its OWN category's offsets (saka {0} → on-time D0). Pawukon normally
// wins this same-day collision (TestHolidayDedupAcrossProviders); this pins
// the degenerate case where nothing outranks the saka copy.
func TestHolidaySakaSurvivesWithoutPawukon(t *testing.T) {
	now := time.Date(2026, 10, 31, 8, 2, 0, 0, time.UTC)
	h := newHarness(t, now)
	h.svc.Providers = []calendarprov.Provider{
		&stubProvider{cat: "saka", hs: []domain.Holiday{
			{Date: domain.NewDate(2026, 10, 31), Name: "Hari Saraswati"}}},
	}
	snap := snapUTC()
	snap.DefaultOffsets = []int{0}
	snap.HolidayOffsets = map[string][]int{"saka": {0}}
	// per-stream settings (Task 6): the otonan seed reminds on the day only.
	snap.RecurrenceOffsets = domain.OffsetMap{domain.StreamOtonan: {0}}
	res, err := h.svc.RunOnce(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	if res.Sent != 2 || res.Missed != 0 {
		t.Fatalf("res = %+v, want Sent 2 (otonan + saka Hari Saraswati D0 on time), Missed 0", res)
	}
	res, _ = h.svc.RunOnce(context.Background(), snap)
	if res.Sent != 0 || res.Missed != 0 {
		t.Errorf("second run must be fully deduped: %+v", res)
	}
}

// Service is built exactly like in main.go (Plan 3 Task 9): struct literal
// from outside the package — the unexported failUntil field cannot be initialized,
// so lazy-init in RunOnce is mandatory; the first failed send must not panic
// on the nil map and kill the scan loop.
func TestRunOnceExternalLiteralNoPanicOnFail(t *testing.T) {
	now := time.Date(2026, 6, 17, 8, 2, 0, 0, time.UTC)
	st, err := store.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	seed(t, st, domain.DateFromTime(now))
	n := &stubNotifier{err: context.DeadlineExceeded}
	svc := &Service{St: st, Clock: &FakeClock{T: now}, // NO failUntil — nil map
		Providers: []calendarprov.Provider{&stubProvider{}},
		Resolve:   func(_ context.Context, ch store.Channel) (notify.Notifier, error) { return n, nil }}

	res, err := svc.RunOnce(context.Background(), snapUTC())
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed != 1 {
		t.Fatalf("failed = %d", res.Failed)
	}

	// 1 minute later: still in backoff → no attempt
	svc.Clock.(*FakeClock).Add(time.Minute)
	res, _ = svc.RunOnce(context.Background(), snapUTC())
	if res.Failed != 0 || res.Sent != 0 {
		t.Errorf("backoff leaked: %+v", res)
	}

	// 16 minutes later + success → sent
	svc.Clock.(*FakeClock).Add(16 * time.Minute)
	n.err = nil
	res, _ = svc.RunOnce(context.Background(), snapUTC())
	if res.Sent != 1 {
		t.Errorf("retry failed: %+v", res)
	}
}

func TestHolidayKey(t *testing.T) {
	got := HolidayKey("pawukon", domain.Holiday{Name: "Batu Kuning"})
	if got != "pawukon:batu-kuning" {
		t.Errorf("key = %q", got)
	}
}

// ---- per-occasion reminders: recurrence, prefs, per-stream offsets ----

// occasionFixture: user + contact "Made" + one anniversary occasion + one
// enabled gotify channel, all owned by the same user. No prefs anywhere, so
// every layer inherits until a test sets one.
type occasionFixture struct {
	User     store.User
	Contact  store.Contact
	Occasion store.Occasion
	Channel  store.Channel
}

// seedAnniversary creates the fixture with base 2025-06-16: monthly marks on
// the 16th (k=6 → 2025-12-16, k=7 → 2026-01-16), the k=12 mark on 2026-06-16
// emitted as the first yearly mark (StreamYearly), and the initial event
// (k=0) on the base date itself.
func seedAnniversary(t *testing.T, st *store.Store, email string) occasionFixture {
	t.Helper()
	ctx := context.Background()
	u, err := st.GetOrCreateUser(ctx, email, "Budi", nil)
	if err != nil {
		t.Fatal(err)
	}
	c, err := st.CreateContact(ctx, u.ID, "Made", "", "")
	if err != nil {
		t.Fatal(err)
	}
	occ, err := st.AddOccasion(ctx, c.ID, domain.Anniversary, domain.RecurAnniversary, domain.NewDate(2025, 6, 16), "")
	if err != nil {
		t.Fatal(err)
	}
	ch, err := st.CreateChannel(ctx, u.ID, "gotify", "home", []byte("enc"))
	if err != nil {
		t.Fatal(err)
	}
	return occasionFixture{User: u, Contact: c, Occasion: occ, Channel: ch}
}

// sentMessage: one landed push stamped with its destination channel — the
// shared stubNotifier cannot answer "which channel received this".
type sentMessage struct {
	ChannelID string
	Message   notify.Message
}

type recorder struct {
	mu   sync.Mutex
	sent []sentMessage
}

func (r *recorder) record(channelID string, m notify.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, sentMessage{ChannelID: channelID, Message: m})
	return nil
}

func (r *recorder) messages() []sentMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]sentMessage(nil), r.sent...)
}

type recorderNotifier struct {
	channelID string
	r         *recorder
}

func (n *recorderNotifier) Name() string               { return "recorder" }
func (n *recorderNotifier) Test(context.Context) error { return nil }
func (n *recorderNotifier) Send(_ context.Context, m notify.Message) error {
	return n.r.record(n.channelID, m)
}

// recordChannels swaps the Resolver for a channel-stamping recorder.
func (h *harness) recordChannels() *recorder {
	r := &recorder{}
	h.svc.Resolve = func(_ context.Context, ch store.Channel) (notify.Notifier, error) {
		return &recorderNotifier{channelID: ch.ID, r: r}, nil
	}
	return r
}

// hasNotif: the notification_log row for (occasion, occurrence date, offset,
// channel) — the durable evidence of what the scan decided.
func hasNotif(t *testing.T, st *store.Store, occID, channelID string, date domain.Date, offset int) bool {
	t.Helper()
	ok, err := st.HasNotification(context.Background(), store.NotificationEntry{
		OccasionID: &occID, OccurrenceDate: date, OffsetDays: offset, ChannelID: channelID})
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

// The per-occasion kill switch: occasion_prefs.enabled = false skips the
// occasion entirely — reminders that would otherwise fire produce neither a
// push nor a missed row.
func TestOccasionDisabledByOccasionPrefs(t *testing.T) {
	// 2026-06-16 08:01 WITA: one minute after the yearly D-0 (08:00) of the
	// first anniversary mark, so the inherited yearly set alone would fire.
	now := time.Date(2026, 6, 16, 8, 1, 0, 0, time.FixedZone("WITA", 8*60*60))
	h := newBareHarness(t, now)
	f := seedAnniversary(t, h.st, "disabled-occasion@x.id")
	if err := h.st.SetOccasionPrefs(context.Background(), store.OccasionPrefs{
		OccasionID: f.Occasion.ID, Enabled: false, Custom: true,
	}); err != nil {
		t.Fatal(err)
	}
	snap := snapUTC()
	snap.Timezone = "Asia/Makassar"
	snap.RecurrenceOffsets = domain.DefaultRecurrenceOffsets()

	res, err := h.svc.RunOnce(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	if res != (Result{}) {
		t.Fatalf("res = %+v, want zero (occasion disabled)", res)
	}
	if len(h.notif.sent) != 0 {
		t.Fatalf("pushes = %d, want 0", len(h.notif.sent))
	}

	// Proof the fixture was live: dropping the prefs row sends the D-0.
	if err := h.st.DeleteOccasionPrefs(context.Background(), f.Occasion.ID); err != nil {
		t.Fatal(err)
	}
	res, err = h.svc.RunOnce(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	if res.Sent != 1 || len(h.notif.sent) != 1 {
		t.Fatalf("after clearing prefs: res = %+v pushes = %d, want the yearly D-0", res, len(h.notif.sent))
	}
	if got := h.notif.sent[0].Title; got != "🎊 Made — Anniversary 1 year today" {
		t.Errorf("title = %q, want the first yearly mark", got)
	}
}

// Monthly marks remind with the monthly stream set: on 2025-12-16 the k=6
// mark (StreamMonthly, offset 0 from settings) is the day's only push.
func TestMonthlyMarkSentOnTheDay(t *testing.T) {
	h := newBareHarness(t, time.Date(2025, 12, 16, 8, 1, 0, 0, time.UTC))
	f := seedAnniversary(t, h.st, "monthly-mark@x.id")
	snap := snapUTC()
	snap.RecurrenceOffsets = domain.DefaultRecurrenceOffsets()

	res, err := h.svc.RunOnce(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	// Missed 1: the k=5 mark (2025-11-16) D-0 fell out of the 24h catch-up.
	if res.Sent != 1 || res.Missed != 1 || res.Failed != 0 {
		t.Fatalf("res = %+v, want Sent 1 (k=6 D-0) Missed 1 (k=5 D-0)", res)
	}
	if len(h.notif.sent) != 1 {
		t.Fatalf("pushes = %d, want 1", len(h.notif.sent))
	}
	// The mark is the 6-month one: the notifier renders the domain label.
	m := h.notif.sent[0]
	if m.Title != "🎊 Made — Anniversary 6 months today" {
		t.Errorf("title = %q, want the 6-month mark", m.Title)
	}
	if !strings.Contains(m.Body, "Tuesday, 16 December 2025") {
		t.Errorf("body = %q, want the 16 Dec 2025 occurrence date", m.Body)
	}
	// The monthly set is [0] alone: no D-1 row for the k=6 mark.
	if hasNotif(t, h.st, f.Occasion.ID, f.Channel.ID, domain.NewDate(2025, 12, 16), 1) {
		t.Error("k=6 must not have a D-1 row (monthly set is [0])")
	}
	if !hasNotif(t, h.st, f.Occasion.ID, f.Channel.ID, domain.NewDate(2025, 11, 16), 0) {
		t.Error("k=5 D-0 missed row missing")
	}
}

// Yearly marks use the event/yearly set from settings: D-30 fires 30 days
// before the first anniversary, and on the mark itself only the D-0 is due.
func TestYearlyOffsetsUseEventYearlySet(t *testing.T) {
	h := newBareHarness(t, time.Date(2026, 5, 17, 8, 1, 0, 0, time.UTC))
	f := seedAnniversary(t, h.st, "yearly-set@x.id")
	snap := snapUTC()
	snap.RecurrenceOffsets = domain.DefaultRecurrenceOffsets()

	// D-30 of the 2026-06-16 mark = 2026-05-17 08:00, clock one minute later.
	res, err := h.svc.RunOnce(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	// Missed 2: the out-of-window D-0 of the monthly marks k=10 and k=11.
	if res.Sent != 1 || res.Missed != 2 {
		t.Fatalf("D-30 run: res = %+v, want Sent 1 (yearly D-30) Missed 2", res)
	}
	if len(h.notif.sent) != 1 {
		t.Fatalf("D-30 pushes = %d, want 1", len(h.notif.sent))
	}
	if got := h.notif.sent[0].Title; got != "🎊 Made — Anniversary 1 year in 30 days" {
		t.Errorf("D-30 title = %q", got)
	}
	if !hasNotif(t, h.st, f.Occasion.ID, f.Channel.ID, domain.NewDate(2026, 6, 16), 30) {
		t.Error("yearly D-30 row (reminder 2026-05-17) missing")
	}

	// On the mark day: exactly one push, the yearly D-0. The k%12==0 date is
	// emitted as StreamYearly only, so there is no second (monthly) occurrence
	// to double-push; a same-date/same-offset pair would collapse into one
	// notification_log row anyway (dedupe key = occasion+date+offset+channel).
	h.fc.T = time.Date(2026, 6, 16, 8, 1, 0, 0, time.UTC)
	res, err = h.svc.RunOnce(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	// Missed 4: the yearly D-7/D-4/D-2/D-1 of the mark; D-30 was already sent.
	if res.Sent != 1 || res.Missed != 4 {
		t.Fatalf("mark-day run: res = %+v, want Sent 1 (D-0) Missed 4 (yearly D-7..D-1)", res)
	}
	if len(h.notif.sent) != 2 {
		t.Fatalf("total pushes = %d, want 2 (D-30 then D-0)", len(h.notif.sent))
	}
	if got := h.notif.sent[1].Title; got != "🎊 Made — Anniversary 1 year today" {
		t.Errorf("D-0 title = %q", got)
	}
	if !hasNotif(t, h.st, f.Occasion.ID, f.Channel.ID, domain.NewDate(2026, 6, 16), 0) {
		t.Error("yearly D-0 row (reminder 2026-06-16) missing")
	}
}

// Per-occasion channel override: occasion_prefs.channel_ids narrows the
// contact cascade to channel B — the push and the missed bookkeeping both land
// on B, while the other enabled channel stays untouched.
func TestOccasionChannelOverride(t *testing.T) {
	h := newBareHarness(t, time.Date(2025, 12, 16, 8, 1, 0, 0, time.UTC))
	f := seedAnniversary(t, h.st, "channel-override@x.id")
	chB, err := h.st.CreateChannel(context.Background(), f.User.ID, "telegram", "b", []byte("enc"))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.st.SetOccasionPrefs(context.Background(), store.OccasionPrefs{
		OccasionID: f.Occasion.ID, ChannelIDs: []string{chB.ID}, Enabled: true, Custom: true,
	}); err != nil {
		t.Fatal(err)
	}
	rec := h.recordChannels()
	snap := snapUTC()
	snap.RecurrenceOffsets = domain.DefaultRecurrenceOffsets()

	res, err := h.svc.RunOnce(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	// k=6 D-0 → one push; k=5 D-0 → one missed row (on B only, so Missed 1).
	if res.Sent != 1 || res.Missed != 1 {
		t.Fatalf("res = %+v, want Sent 1 Missed 1", res)
	}
	got := rec.messages()
	if len(got) != 1 {
		t.Fatalf("pushes = %d, want 1 (channel A must be skipped)", len(got))
	}
	if got[0].ChannelID != chB.ID {
		t.Errorf("pushed to %q, want channel B %q", got[0].ChannelID, chB.ID)
	}
	if got[0].Message.Title != "🎊 Made — Anniversary 6 months today" {
		t.Errorf("title = %q, want the k=6 mark", got[0].Message.Title)
	}
	if !hasNotif(t, h.st, f.Occasion.ID, chB.ID, domain.NewDate(2025, 11, 16), 0) {
		t.Error("missed row for the k=5 mark missing on channel B")
	}
	if hasNotif(t, h.st, f.Occasion.ID, f.Channel.ID, domain.NewDate(2025, 11, 16), 0) {
		t.Errorf("reminder leaked onto channel A (%s)", f.Channel.ID)
	}
}

// custom=false suspends the occasion overrides without deleting them: the
// occasion falls back to the contact chain (settings monthly [0], channel A)
// even though the retained row points at offset D-1 and channel B — and the
// enabled kill switch still wins on the same row.
func TestOccasionCustomFalseInheritsContactChain(t *testing.T) {
	h := newBareHarness(t, time.Date(2025, 12, 16, 8, 1, 0, 0, time.UTC))
	f := seedAnniversary(t, h.st, "custom-off-inherit@x.id")
	chB, err := h.st.CreateChannel(context.Background(), f.User.ID, "telegram", "b", []byte("enc"))
	if err != nil {
		t.Fatal(err)
	}
	// The contact chain selects channel A (no contact offsets: settings
	// monthly [0] still applies), so the retained channel B must be ignored.
	if err := h.st.SetReminderPrefs(context.Background(), store.ReminderPrefs{
		ContactID: f.Contact.ID, ChannelIDs: []string{f.Channel.ID}, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	// Retained-but-inactive overrides: D-1 on the monthly stream, channel B.
	if err := h.st.SetOccasionPrefs(context.Background(), store.OccasionPrefs{
		OccasionID: f.Occasion.ID, Enabled: true, Custom: false,
		Offsets:    domain.OffsetMap{domain.StreamMonthly: {1, 0}},
		ChannelIDs: []string{chB.ID},
	}); err != nil {
		t.Fatal(err)
	}
	rec := h.recordChannels()
	snap := snapUTC()
	snap.RecurrenceOffsets = domain.DefaultRecurrenceOffsets()

	res, err := h.svc.RunOnce(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	// The k=6 monthly mark D-0 fires on the cascade channel A, not B; no D-1
	// row exists because the retained monthly [1,0] never became live.
	if res.Sent != 1 || res.Missed != 1 {
		t.Fatalf("res = %+v, want Sent 1 Missed 1 (contact-chain behavior)", res)
	}
	got := rec.messages()
	if len(got) != 1 || got[0].ChannelID != f.Channel.ID {
		t.Fatalf("pushes = %+v, want exactly one on channel A %s", got, f.Channel.ID)
	}
	if !hasNotif(t, h.st, f.Occasion.ID, f.Channel.ID, domain.NewDate(2025, 12, 16), 0) {
		t.Error("contact-chain D-0 row missing on channel A")
	}
	if hasNotif(t, h.st, f.Occasion.ID, chB.ID, domain.NewDate(2025, 12, 16), 0) {
		t.Error("inactive custom override leaked onto channel B")
	}
	if hasNotif(t, h.st, f.Occasion.ID, f.Channel.ID, domain.NewDate(2025, 12, 16), 1) {
		t.Error("retained D-1 must not fire while custom=false")
	}

	// Phase 2 recomposes the flags (enabled=false + custom=false): the scan
	// still produces nothing new, i.e. composition does not regress. The kill
	// switch itself is covered by TestOccasionDisabledByOccasionPrefs.
	if err := h.st.SetOccasionPrefs(context.Background(), store.OccasionPrefs{
		OccasionID: f.Occasion.ID, Enabled: false, Custom: false,
		Offsets:    domain.OffsetMap{domain.StreamMonthly: {1, 0}},
		ChannelIDs: []string{chB.ID},
	}); err != nil {
		t.Fatal(err)
	}
	res, err = h.svc.RunOnce(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	if res != (Result{}) || len(rec.messages()) != 1 {
		t.Fatalf("res = %+v, want zero once enabled=false (pushes stay at 1)", res)
	}
}

// The channel gate follows custom: on one and the same contact, a custom
// occasion narrows to its retained channel B, while an identical inherit
// occasion (custom=false, same retained channel B) falls back to the contact
// cascade [A, B]. A single scan proves both branches — and that the inactive
// channel_ids never leak onto the cascade.
func TestOccasionChannelGateFollowsCustom(t *testing.T) {
	h := newBareHarness(t, time.Date(2025, 12, 16, 8, 1, 0, 0, time.UTC))
	f := seedAnniversary(t, h.st, "channel-gate-custom@x.id")
	chB, err := h.st.CreateChannel(context.Background(), f.User.ID, "telegram", "b", []byte("enc"))
	if err != nil {
		t.Fatal(err)
	}
	// The contact cascade is [A, B]: the inherit occasion must push to both.
	if err := h.st.SetReminderPrefs(context.Background(), store.ReminderPrefs{
		ContactID: f.Contact.ID, ChannelIDs: []string{f.Channel.ID, chB.ID}, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	// A second identical occasion on the same contact, differing only in custom.
	inh, err := h.st.AddOccasion(context.Background(), f.Contact.ID, domain.Anniversary, domain.RecurAnniversary, domain.NewDate(2025, 6, 16), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []store.OccasionPrefs{
		{OccasionID: f.Occasion.ID, Enabled: true, Custom: true, ChannelIDs: []string{chB.ID}},
		{OccasionID: inh.ID, Enabled: true, Custom: false, ChannelIDs: []string{chB.ID}},
	} {
		if err := h.st.SetOccasionPrefs(context.Background(), p); err != nil {
			t.Fatal(err)
		}
	}
	rec := h.recordChannels()
	snap := snapUTC()
	snap.RecurrenceOffsets = domain.DefaultRecurrenceOffsets()

	res, err := h.svc.RunOnce(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	// k=6 D-0: custom → 1 push (B); inherit → 2 pushes (A and B). The k=5
	// D-0 is missed on the same channels: 1 + 2.
	if res.Sent != 3 || res.Missed != 3 {
		t.Fatalf("res = %+v, want Sent 3 (B + A,B) Missed 3", res)
	}
	perCh := map[string]int{}
	for _, m := range rec.messages() {
		perCh[m.ChannelID]++
	}
	if len(perCh) != 2 || perCh[f.Channel.ID] != 1 || perCh[chB.ID] != 2 {
		t.Fatalf("pushes per channel = %v, want A=1 B=2", perCh)
	}
	// Durable evidence: the custom occasion only on B; the inherit one on both.
	if !hasNotif(t, h.st, f.Occasion.ID, chB.ID, domain.NewDate(2025, 12, 16), 0) {
		t.Error("custom occasion D-0 row missing on channel B")
	}
	if hasNotif(t, h.st, f.Occasion.ID, f.Channel.ID, domain.NewDate(2025, 12, 16), 0) {
		t.Error("custom occasion leaked onto channel A")
	}
	if !hasNotif(t, h.st, inh.ID, f.Channel.ID, domain.NewDate(2025, 12, 16), 0) {
		t.Error("inherit occasion D-0 row missing on channel A")
	}
	if !hasNotif(t, h.st, inh.ID, chB.ID, domain.NewDate(2025, 12, 16), 0) {
		t.Error("inherit occasion D-0 row missing on channel B")
	}
	if hasNotif(t, h.st, inh.ID, chB.ID, domain.NewDate(2025, 12, 16), 1) {
		t.Error("retained D-1 must not fire on the inherit occasion")
	}
}

// Per-occasion offset override: occasion_prefs.offsets = {monthly: [1, 0]}
// replaces the inherited monthly set, so the k=6 mark reminds the day before
// (15 Dec) and on the day (16 Dec) instead of only on the day.
func TestOccasionOffsetsOverride(t *testing.T) {
	h := newBareHarness(t, time.Date(2025, 12, 16, 7, 0, 0, 0, time.UTC))
	f := seedAnniversary(t, h.st, "offsets-override@x.id")
	if err := h.st.SetOccasionPrefs(context.Background(), store.OccasionPrefs{
		OccasionID: f.Occasion.ID, Enabled: true, Custom: true,
		Offsets: domain.OffsetMap{domain.StreamMonthly: {1, 0}},
	}); err != nil {
		t.Fatal(err)
	}
	snap := snapUTC()
	snap.RecurrenceOffsets = domain.DefaultRecurrenceOffsets()

	// 07:00 on the mark day: the D-1 (15 Dec 08:00, 23h ago) is inside the
	// catch-up window → sent late; the D-0 (16 Dec 08:00) is not due yet.
	res, err := h.svc.RunOnce(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	// Missed 2: the k=5 mark (2025-11-16) D-1/D-0, pulled into the scan window
	// by the widest resolved stream (yearly [30]) — window sizing per occasion.
	if res.Sent != 1 || res.Missed != 2 {
		t.Fatalf("D-1 run: res = %+v, want Sent 1 Missed 2", res)
	}
	if len(h.notif.sent) != 1 {
		t.Fatalf("D-1 pushes = %d, want 1", len(h.notif.sent))
	}
	if m := h.notif.sent[0]; !strings.Contains(m.Body, "Sent late") {
		t.Errorf("D-1 push must be late: %q", m.Body)
	}
	if !hasNotif(t, h.st, f.Occasion.ID, f.Channel.ID, domain.NewDate(2025, 12, 16), 1) {
		t.Error("D-1 row (reminder 2025-12-15) missing for the k=6 mark")
	}
	if hasNotif(t, h.st, f.Occasion.ID, f.Channel.ID, domain.NewDate(2025, 12, 16), 0) {
		t.Error("D-0 must not fire at 07:00 (reminder is 2025-12-16 08:00)")
	}

	// Next morning the D-0 fires (the 16 Dec reminder, 23h late).
	h.fc.T = time.Date(2025, 12, 17, 7, 0, 0, 0, time.UTC)
	res, err = h.svc.RunOnce(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	if res.Sent != 1 || res.Missed != 0 {
		t.Fatalf("D-0 run: res = %+v, want Sent 1 Missed 0", res)
	}
	if len(h.notif.sent) != 2 {
		t.Fatalf("total pushes = %d, want 2 (D-1 then D-0)", len(h.notif.sent))
	}
	if !hasNotif(t, h.st, f.Occasion.ID, f.Channel.ID, domain.NewDate(2025, 12, 16), 0) {
		t.Error("D-0 row (reminder 2025-12-16) missing for the k=6 mark")
	}
}

// Contact layer of the chain: reminder_prefs.offsets[monthly] is consumed
// per stream when the occasion carries no override — the D-2 fires instead of
// the settings' monthly [0].
func TestContactOffsetsInheritedByOccasion(t *testing.T) {
	h := newBareHarness(t, time.Date(2025, 12, 14, 8, 1, 0, 0, time.UTC))
	f := seedAnniversary(t, h.st, "contact-offsets@x.id")
	if err := h.st.SetReminderPrefs(context.Background(), store.ReminderPrefs{
		ContactID: f.Contact.ID, Enabled: true,
		Offsets: domain.OffsetMap{domain.StreamMonthly: {2, 0}},
	}); err != nil {
		t.Fatal(err)
	}
	snap := snapUTC()
	snap.RecurrenceOffsets = domain.DefaultRecurrenceOffsets()

	res, err := h.svc.RunOnce(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	// D-2 of the k=6 mark (reminder 2025-12-14 08:00, one minute ago) → sent;
	// the k=5 mark's D-2/D-0 are out of window → 2 missed.
	if res.Sent != 1 || res.Missed != 2 {
		t.Fatalf("res = %+v, want Sent 1 (contact D-2) Missed 2 (k=5)", res)
	}
	if !hasNotif(t, h.st, f.Occasion.ID, f.Channel.ID, domain.NewDate(2025, 12, 16), 2) {
		t.Error("contact D-2 row (reminder 2025-12-14) missing for the k=6 mark")
	}
	if hasNotif(t, h.st, f.Occasion.ID, f.Channel.ID, domain.NewDate(2025, 12, 16), 1) {
		t.Error("D-1 fired — the contact monthly set [2,0] was not the one used")
	}
}

// filterChannels: keeps input order, ignores ids that match no channel.
func TestFilterChannels(t *testing.T) {
	a := store.Channel{ID: "a"}
	b := store.Channel{ID: "b"}
	c := store.Channel{ID: "c"}
	got := filterChannels([]store.Channel{a, b, c}, []string{"c", "a"})
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "c" {
		t.Errorf("filter = %v, want [a c] in input order", got)
	}
	if got := filterChannels([]store.Channel{a, b}, []string{"nope"}); len(got) != 0 {
		t.Errorf("unknown ids must select nothing: %v", got)
	}
	if got := filterChannels(nil, []string{"a"}); len(got) != 0 {
		t.Errorf("no channels: %v", got)
	}
}
