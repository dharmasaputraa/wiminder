package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type GotifyConfig struct {
	BaseURL  string `json:"base_url"`
	Token    string `json:"token"`
	Priority int    `json:"priority,omitempty"`
}

type Gotify struct {
	cfg GotifyConfig
	hc  *http.Client
}

func NewGotify(cfg GotifyConfig) *Gotify {
	if cfg.Priority == 0 {
		cfg.Priority = 5
	}
	if !strings.Contains(cfg.BaseURL, "://") {
		cfg.BaseURL = "https://" + cfg.BaseURL
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &Gotify{cfg: cfg, hc: &http.Client{Timeout: 10 * time.Second}}
}

func (g *Gotify) Name() string { return "gotify" }

func (g *Gotify) Send(ctx context.Context, msg Message) error {
	priority := msg.Priority
	if priority == 0 {
		priority = g.cfg.Priority
	}
	payload, err := json.Marshal(map[string]any{
		"title": msg.Title, "message": msg.Body, "priority": priority,
	})
	if err != nil {
		return err
	}
	endpoint := strings.TrimRight(g.cfg.BaseURL, "/") + "/message?token=" + url.QueryEscape(g.cfg.Token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := g.hc.Do(req)
	if err != nil {
		return fmt.Errorf("gotify: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("gotify status %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

func (g *Gotify) Test(ctx context.Context) error {
	return g.Send(ctx, Message{Title: "wiminder tes", Body: "Koneksi Gotify OK ✅", Priority: 5})
}
