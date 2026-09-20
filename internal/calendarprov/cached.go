package calendarprov

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"wiminder/internal/domain"
	"wiminder/internal/store"
)

type cachePayload struct {
	FetchedAt time.Time        `json:"fetched_at"`
	Holidays  []domain.Holiday `json:"holidays"`
}

// failBackoffDefault: after a failed refresh, wait this long before trying
// the remote again (negative cache / failure backoff) — the scheduler scan
// and /upcoming must not hit the remote on every request while it is down.
const failBackoffDefault = 10 * time.Minute

// CachedRemote: cache-first against holiday_cache (SQLite). Refresh when the
// payload is > 24 hours old; if refetch fails → use the stale cache (degrade,
// don't die). Remote failures are recorded (negative cache): during the backoff
// window the remote is not tried at all — requests are served from the stale
// cache if present, or an empty set.
type CachedRemote struct {
	Inner Provider
	St    *store.Store
	// FailBackoff: backoff window after a remote failure. Filled with the default
	// by NewCachedRemote; zero/negative means always retry (for tests).
	FailBackoff time.Duration

	mu       sync.Mutex
	lastFail map[int]time.Time // year → last failed refresh
}

func NewCachedRemote(inner Provider, st *store.Store) *CachedRemote {
	return &CachedRemote{Inner: inner, St: st, FailBackoff: failBackoffDefault,
		lastFail: map[int]time.Time{}}
}

func (c *CachedRemote) Name() string     { return c.Inner.Name() }
func (c *CachedRemote) Category() string { return c.Inner.Category() }

// inBackoff reports whether the refresh for year y is still held back by a
// previous failure. Called concurrently from the scheduler loop and API
// handlers → guarded by a mutex.
func (c *CachedRemote) inBackoff(y int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	last, ok := c.lastFail[y]
	if !ok {
		return false
	}
	window := c.FailBackoff
	if window <= 0 {
		return false // zero/negative: always retry
	}
	return time.Since(last) < window
}

func (c *CachedRemote) markFail(y int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lastFail == nil {
		c.lastFail = map[int]time.Time{}
	}
	c.lastFail[y] = time.Now()
}

func (c *CachedRemote) clearFail(y int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.lastFail, y)
}

func (c *CachedRemote) loadYear(ctx context.Context, y int) ([]domain.Holiday, bool, error) {
	var p cachePayload
	err := c.St.GetHolidayCache(ctx, y, c.Inner.Name(), &p)
	if err == nil && time.Since(p.FetchedAt) < 24*time.Hour {
		return p.Holidays, true, nil
	}
	// miss or stale → try to refresh, unless still in failure backoff
	if c.inBackoff(y) {
		// The remote just failed: don't retry on every scan/request. Use the
		// stale cache if present, otherwise → no-op (empty set), with no
		// HTTP call and no log spam.
		if err == nil {
			return p.Holidays, true, nil
		}
		return nil, false, nil
	}
	fresh, ferr := c.Inner.HolidaysBetween(ctx, domain.NewDate(y, 1, 1), domain.NewDate(y, 12, 31))
	if ferr == nil {
		c.clearFail(y)
		_ = c.St.PutHolidayCache(ctx, y, c.Inner.Name(), cachePayload{
			FetchedAt: time.Now(), Holidays: fresh,
		})
		return fresh, true, nil
	}
	c.markFail(y)
	if err == nil { // stale cache exists → use it, don't fail the scheduler
		return p.Holidays, true, nil
	}
	// Empty cache + remote down → no-op (empty set), NOT an error: computed
	// sources (pawukon) keep working and /upcoming must not 5xx just because
	// a third-party API is down.
	slog.Warn("remote provider failed, cache empty → skipping",
		"provider", c.Inner.Name(), "year", y, "err", ferr)
	return nil, false, nil
}

func (c *CachedRemote) HolidaysBetween(ctx context.Context, from, to domain.Date) ([]domain.Holiday, error) {
	var out []domain.Holiday
	for y := from.Year; y <= to.Year; y++ {
		hs, ok, err := c.loadYear(ctx, y)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		for _, h := range hs {
			if !h.Date.Before(from) && !h.Date.After(to) {
				out = append(out, h)
			}
		}
	}
	return out, nil
}
