package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"time"
)

type TelegramConfig struct {
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
}

type Telegram struct {
	cfg     TelegramConfig
	baseURL string
	hc      *http.Client
}

func NewTelegram(cfg TelegramConfig) *Telegram {
	return &Telegram{cfg: cfg, baseURL: "https://api.telegram.org",
		hc: &http.Client{Timeout: 10 * time.Second}}
}

func (t *Telegram) Name() string { return "telegram" }

func (t *Telegram) Send(ctx context.Context, msg Message) error {
	payload, err := json.Marshal(map[string]string{
		"chat_id":    t.cfg.ChatID,
		"text":       html.EscapeString(msg.Title) + "\n" + html.EscapeString(msg.Body),
		"parse_mode": "HTML",
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		t.baseURL+"/bot"+t.cfg.BotToken+"/sendMessage", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.hc.Do(req)
	if err != nil {
		return fmt.Errorf("telegram: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return fmt.Errorf("telegram decode: %w", err)
	}
	if !out.OK {
		return fmt.Errorf("telegram: %s", out.Description)
	}
	return nil
}

func (t *Telegram) Test(ctx context.Context) error {
	return t.Send(ctx, Message{Title: "wiminder tes", Body: "Koneksi Telegram OK ✅"})
}
