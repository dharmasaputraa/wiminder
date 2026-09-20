package calendarprov

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"wiminder/internal/domain"
	"wiminder/internal/store"
)

const hariliburFixture = `[{"holiday_date":"2026-12-25","holiday_name":"Hari Raya Natal","is_national_holiday":true},
	{"holiday_date":"2026-10-31","holiday_name":"Hari Saraswati","is_national_holiday":false},
	{"holiday_date":"2026-06-1","holiday_name":"Purnama Kapat","is_national_holiday":false}]`

func TestKresnaParseWrapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":`+hariliburFixture+`}`)
	}))
	defer srv.Close()
	k := NewKresna(srv.URL)
	hs, err := k.HolidaysBetween(context.Background(), domain.NewDate(2026, 6, 1), domain.NewDate(2026, 12, 31))
	if err != nil {
		t.Fatal(err)
	}
	// 3 items — including the non-zero-padded "2026-06-1" (live API quirk).
	if len(hs) != 3 || hs[0].Name != "Hari Raya Natal" {
		t.Errorf("hs = %+v", hs)
	}
}

// TestKresnaNationalFilter: one source, two categories — nationalOnly=true
// keeps is_national_holiday items, false keeps the Bali/Saka remainder.
func TestKresnaNationalFilter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, hariliburFixture)
	}))
	defer srv.Close()
	nat := NewKresnaFiltered(srv.URL, "", true)
	saka := NewKresnaFiltered(srv.URL, "", false)
	if nat.Name() != "harilibur-national" || nat.Category() != "national" {
		t.Errorf("nat name/category = %q/%q", nat.Name(), nat.Category())
	}
	if saka.Name() != "harilibur-saka" || saka.Category() != "saka" {
		t.Errorf("saka name/category = %q/%q", saka.Name(), saka.Category())
	}
	ctx := context.Background()
	nhs, err := nat.HolidaysBetween(ctx, domain.NewDate(2026, 12, 1), domain.NewDate(2026, 12, 31))
	if err != nil {
		t.Fatal(err)
	}
	if len(nhs) != 1 || nhs[0].Name != "Hari Raya Natal" {
		t.Errorf("national hs = %+v", nhs)
	}
	shs, err := saka.HolidaysBetween(ctx, domain.NewDate(2026, 10, 1), domain.NewDate(2026, 10, 31))
	if err != nil {
		t.Fatal(err)
	}
	if len(shs) != 1 || shs[0].Name != "Hari Saraswati" {
		t.Errorf("saka hs = %+v", shs)
	}
}

// TestKresnaFallbackMirror: primary mirror down (e.g. 402/5xx) → one retry on
// the fallback mirror before the provider reports failure.
func TestKresnaFallbackMirror(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Payment required\n\nDEPLOYMENT_DISABLED", http.StatusPaymentRequired)
	}))
	defer primary.Close()
	fallbackCalled := false
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackCalled = true
		io.WriteString(w, `[{"holiday_date":"2026-12-25","holiday_name":"Hari Raya Natal","is_national_holiday":true}]`)
	}))
	defer fallback.Close()
	k := NewKresnaFiltered(primary.URL, fallback.URL, true)
	hs, err := k.HolidaysBetween(context.Background(), domain.NewDate(2026, 12, 1), domain.NewDate(2026, 12, 31))
	if err != nil {
		t.Fatalf("fallback must recover: %v", err)
	}
	if !fallbackCalled {
		t.Error("fallback mirror was not called")
	}
	if len(hs) != 1 || hs[0].Name != "Hari Raya Natal" {
		t.Errorf("hs = %+v", hs)
	}
}

// TestKresnaBothMirrorsFail: no fallback configured (or both down) → error,
// which CachedRemote degrades per its own policy.
func TestKresnaBothMirrorsFail(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer down.Close()
	k := NewKresnaFiltered(down.URL, down.URL, true)
	_, err := k.HolidaysBetween(context.Background(), domain.NewDate(2026, 12, 1), domain.NewDate(2026, 12, 31))
	if err == nil {
		t.Fatal("both mirrors down must produce an error")
	}
	if !strings.Contains(err.Error(), "status 500") {
		t.Errorf("err = %v, want it to contain \"status 500\"", err)
	}
}

// newTestRemote: a Kresna wired to a test server, national category, no
// fallback (offline-safe; "" disables the mirror retry).
func newTestRemote(url string) *Kresna {
	return NewKresnaFiltered(url, "", true)
}

func TestCachedRemoteCacheFirst(t *testing.T) {
	st, _ := store.OpenInMemory()
	defer st.Close()
	_ = st.Migrate()

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		io.WriteString(w, `[{"holiday_date":"2026-03-19","holiday_name":"Nyepi","is_national_holiday":true}]`)
	}))
	defer srv.Close()
	inner := newTestRemote(srv.URL)
	c := NewCachedRemote(inner, st)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		hs, err := c.HolidaysBetween(ctx, domain.NewDate(2026, 3, 10), domain.NewDate(2026, 3, 20))
		if err != nil {
			t.Fatal(err)
		}
		if len(hs) != 1 {
			t.Fatalf("hs = %+v", hs)
		}
	}
	if calls != 1 {
		t.Errorf("remote called %d×, want 1 (cache-first)", calls)
	}
}

// TestCachedRemoteStaleFallback: cache >24 hours old + failed refresh (server 500)
// → still returns stale data, the scheduler must not die.
func TestCachedRemoteStaleFallback(t *testing.T) {
	st, _ := store.OpenInMemory()
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	stale := cachePayload{
		FetchedAt: time.Now().Add(-48 * time.Hour),
		Holidays:  []domain.Holiday{{Date: domain.NewDate(2026, 3, 19), Name: "Nyepi"}},
	}

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer srv.Close()
	inner := newTestRemote(srv.URL)
	c := NewCachedRemote(inner, st)
	if err := st.PutHolidayCache(context.Background(), 2026, c.Inner.Name(), stale); err != nil {
		t.Fatal(err)
	}

	hs, err := c.HolidaysBetween(context.Background(), domain.NewDate(2026, 3, 10), domain.NewDate(2026, 3, 20))
	if err != nil {
		t.Fatalf("stale fallback must succeed: %v", err)
	}
	if len(hs) != 1 || hs[0].Name != "Nyepi" {
		t.Errorf("hs = %+v", hs)
	}
	if calls != 1 {
		t.Errorf("refresh must be attempted once, calls = %d", calls)
	}
}

// TestCachedRemoteFailureBackoff: remote down + empty cache → the first call
// tries the remote once (empty result set, no error); subsequent calls must be
// held back by the backoff (negative cache), not retried on every
// scan/request. After the window passes (FailBackoff = 0) → tried again.
func TestCachedRemoteFailureBackoff(t *testing.T) {
	st, _ := store.OpenInMemory()
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer srv.Close()
	inner := newTestRemote(srv.URL)
	c := NewCachedRemote(inner, st)
	ctx := context.Background()
	ran := domain.NewDate(2026, 3, 10)

	// (a) first call: the only remote attempt.
	hs, err := c.HolidaysBetween(ctx, ran, domain.NewDate(2026, 3, 20))
	if err != nil {
		t.Fatalf("remote down + empty cache must be a no-op, not an error: %v", err)
	}
	if len(hs) != 0 {
		t.Errorf("hs = %+v, want empty", hs)
	}
	// (a) immediate second call: backoff → no new HTTP call.
	if _, err := c.HolidaysBetween(ctx, ran, domain.NewDate(2026, 3, 20)); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (backoff must hold back the retry)", calls)
	}

	// (b) backoff window passed (FailBackoff = 0) → the remote is tried again.
	c.FailBackoff = 0
	if _, err := c.HolidaysBetween(ctx, ran, domain.NewDate(2026, 3, 20)); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (backoff passed → retry)", calls)
	}
}

// TestCachedRemoteBackoffClearedOnSuccess: a successful refresh must clear the
// failure memory, so the next failure backs off from zero again.
func TestCachedRemoteBackoffClearedOnSuccess(t *testing.T) {
	st, _ := store.OpenInMemory()
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}

	healthy := false
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if !healthy {
			http.Error(w, "down", http.StatusInternalServerError)
			return
		}
		io.WriteString(w, `[{"holiday_date":"2026-03-19","holiday_name":"Nyepi","is_national_holiday":true}]`)
	}))
	defer srv.Close()
	inner := newTestRemote(srv.URL)
	c := NewCachedRemote(inner, st)
	ran := domain.NewDate(2026, 3, 10)

	if _, err := c.HolidaysBetween(context.Background(), ran, domain.NewDate(2026, 3, 20)); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !c.inBackoff(2026) {
		t.Fatalf("after failure: calls=%d inBackoff=%v, want 1 & true", calls, c.inBackoff(2026))
	}

	healthy = true
	c.FailBackoff = 0
	if _, err := c.HolidaysBetween(context.Background(), ran, domain.NewDate(2026, 3, 20)); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2 (retry after the window opens)", calls)
	}
	if c.inBackoff(2026) {
		t.Error("success must clear fail memory (inBackoff=false)")
	}
}

func TestKresnaStatusCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()
	k := NewKresna(srv.URL) // no fallback → single attempt
	_, err := k.HolidaysBetween(context.Background(), domain.NewDate(2026, 6, 1), domain.NewDate(2026, 6, 30))
	if err == nil {
		t.Fatal("404 must produce an error")
	}
	if !strings.Contains(err.Error(), "status 404") {
		t.Errorf("err = %v, want it to contain \"status 404\"", err)
	}
}
