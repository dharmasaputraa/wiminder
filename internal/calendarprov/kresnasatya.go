package calendarprov

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"wiminder/internal/domain"
)

// Kresna: Indonesian holidays from github.com/kresnasatya/api-harilibur — one
// source covering national holidays (is_national_holiday=true) and Bali/Saka
// regional ones (false). Official mirrors: api-harilibur.pages.dev (primary)
// and api-harilibur.netlify.app (fallback). The old artworks.kresna.me host
// 404s and dayoffapi.vercel.app is dead (402 DEPLOYMENT_DISABLED).
type Kresna struct {
	BaseURL         string
	FallbackBaseURL string
	// NationalOnly: nil → keep every holiday (tests / simple use); non-nil →
	// keep only items whose is_national_holiday matches the pointed value.
	NationalOnly *bool
	hc           *http.Client
}

// NewKresna: all holidays, no national/saka split and no fallback mirror.
// Production uses NewKresnaFiltered to derive the two categories.
func NewKresna(baseURL string) *Kresna { return newKresna(baseURL, "", nil) }

// NewKresnaFiltered: one category of the shared source — nationalOnly=true
// keeps national holidays, false keeps the Bali/Saka remainder. An empty
// fallbackBaseURL disables the fallback retry.
func NewKresnaFiltered(baseURL, fallbackBaseURL string, nationalOnly bool) *Kresna {
	return newKresna(baseURL, fallbackBaseURL, &nationalOnly)
}

func newKresna(baseURL, fallbackBaseURL string, nationalOnly *bool) *Kresna {
	if baseURL == "" {
		baseURL = "https://api-harilibur.pages.dev"
	}
	return &Kresna{BaseURL: baseURL, FallbackBaseURL: fallbackBaseURL,
		NationalOnly: nationalOnly, hc: &http.Client{Timeout: 15 * time.Second}}
}

func (k *Kresna) Name() string {
	if k.NationalOnly != nil && *k.NationalOnly {
		return "harilibur-national"
	}
	return "harilibur-saka"
}

func (k *Kresna) Category() string {
	if k.NationalOnly != nil && *k.NationalOnly {
		return "national"
	}
	return "saka"
}

type kresnaItem struct {
	HolidayDate       string `json:"holiday_date"`
	HolidayName       string `json:"holiday_name"`
	IsNationalHoliday bool   `json:"is_national_holiday"`
}

// keep: the national/saka split of the shared source.
func (k *Kresna) keep(it kresnaItem) bool {
	if k.NationalOnly == nil {
		return true
	}
	return it.IsNationalHoliday == *k.NationalOnly
}

func (k *Kresna) fetchYear(ctx context.Context, year int) ([]domain.Holiday, error) {
	hs, err := k.fetchYearFrom(ctx, k.BaseURL, year)
	if err != nil && k.FallbackBaseURL != "" && k.FallbackBaseURL != k.BaseURL {
		// primary mirror down → one retry on the fallback mirror
		hs, err = k.fetchYearFrom(ctx, k.FallbackBaseURL, year)
	}
	return hs, err
}

func (k *Kresna) fetchYearFrom(ctx context.Context, base string, year int) ([]domain.Holiday, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/api?year=%d", base, year), nil)
	if err != nil {
		return nil, err
	}
	resp, err := k.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("harilibur: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("harilibur status %d", resp.StatusCode)
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	// the source response may be a bare array or wrapped in {"data":[...]}
	var items []kresnaItem
	if err := json.Unmarshal(raw, &items); err != nil {
		var wrapped struct {
			Data []kresnaItem `json:"data"`
		}
		if err2 := json.Unmarshal(raw, &wrapped); err2 != nil {
			return nil, fmt.Errorf("harilibur decode: %w / %w", err, err2)
		}
		items = wrapped.Data
	}
	var out []domain.Holiday
	for _, it := range items {
		if !k.keep(it) {
			continue
		}
		// The source emits non-zero-padded dates ("2026-06-1") alongside
		// padded ones; layout "2006-1-2" accepts both day and month widths.
		dt, err := time.Parse("2006-1-2", it.HolidayDate)
		if err != nil {
			return nil, fmt.Errorf("harilibur date %q: %w", it.HolidayDate, err)
		}
		out = append(out, domain.Holiday{Date: domain.DateFromTime(dt), Name: it.HolidayName})
	}
	return out, nil
}

func (k *Kresna) HolidaysBetween(ctx context.Context, from, to domain.Date) ([]domain.Holiday, error) {
	var out []domain.Holiday
	for y := from.Year; y <= to.Year; y++ {
		hs, err := k.fetchYear(ctx, y)
		if err != nil {
			return nil, err
		}
		for _, h := range hs {
			if !h.Date.Before(from) && !h.Date.After(to) {
				out = append(out, h)
			}
		}
	}
	return out, nil
}
