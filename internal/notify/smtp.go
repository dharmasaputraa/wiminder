package notify

import (
	"context"
	"fmt"
	"mime"
	"net/smtp"
	"strings"
	"time"
)

type SMTPConfig struct {
	Host     string   `json:"host"`
	Port     int      `json:"port"`
	Username string   `json:"username"`
	Password string   `json:"password"`
	From     string   `json:"from"`
	To       []string `json:"to"`
}

type SMTP struct{ cfg SMTPConfig }

func NewSMTP(cfg SMTPConfig) *SMTP { return &SMTP{cfg: cfg} }

func (s *SMTP) Name() string { return "email" }

func (s *SMTP) build(msg Message) []byte {
	boundary := "wiminder-boundary-42"
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", s.cfg.From)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(s.cfg.To, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", msg.Title))
	fmt.Fprintf(&b, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%s\r\n\r\n", boundary)
	fmt.Fprintf(&b, "--%s\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n", boundary, msg.Body)
	fmt.Fprintf(&b, "--%s\r\nContent-Type: text/html; charset=utf-8\r\n\r\n", boundary)
	fmt.Fprintf(&b, "<html><body><h3>%s</h3><p>%s</p></body></html>\r\n", msg.Title, msg.Body)
	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return []byte(b.String())
}

func (s *SMTP) Send(ctx context.Context, msg Message) error {
	addr := fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port)
	var auth smtp.Auth
	if s.cfg.Username != "" {
		auth = smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)
	}
	raw := s.build(msg)
	ch := make(chan error, 1)
	go func() { ch <- smtp.SendMail(addr, auth, s.cfg.From, s.cfg.To, raw) }()
	select {
	case err := <-ch:
		if err != nil {
			return fmt.Errorf("smtp: %w", err)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("smtp: %w", ctx.Err())
	case <-time.After(30 * time.Second):
		return fmt.Errorf("smtp: timeout")
	}
}

func (s *SMTP) Test(ctx context.Context) error {
	return s.Send(ctx, Message{Title: "wiminder tes", Body: "Koneksi email OK ✅"})
}
