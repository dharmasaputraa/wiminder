package notify

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeSMTP: a minimal SMTP server for tests — just enough of the basic
// protocol (220/250/354/221) and it captures the DATA contents.
type fakeSMTP struct {
	addr     string
	data     string
	mailFrom string
	rcptTo   []string
	quit     func()
}

func startFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{addr: ln.Addr().String()}
	done := make(chan struct{})
	f.quit = func() {
		close(done)
		ln.Close()
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				w := bufio.NewWriter(c)
				r := bufio.NewReader(c)
				write := func(s string) {
					w.WriteString(s + "\r\n")
					w.Flush()
				}
				write("220 wiminder-test ESMTP")
				inData := false
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					trimmed := strings.TrimRight(line, "\r\n")
					switch {
					case inData:
						if trimmed == "." {
							inData = false
							write("250 OK")
						} else {
							f.data += line
						}
					case strings.HasPrefix(strings.ToUpper(trimmed), "EHLO"),
						strings.HasPrefix(strings.ToUpper(trimmed), "HELO"):
						write("250 wiminder-test")
					case strings.HasPrefix(strings.ToUpper(trimmed), "MAIL FROM:"):
						f.mailFrom = trimmed
						write("250 OK")
					case strings.HasPrefix(strings.ToUpper(trimmed), "RCPT TO:"):
						f.rcptTo = append(f.rcptTo, trimmed)
						write("250 OK")
					case strings.HasPrefix(strings.ToUpper(trimmed), "DATA"):
						write("354 end with <CR><LF>.<CR><LF>")
						inData = true
					case strings.HasPrefix(strings.ToUpper(trimmed), "QUIT"):
						write("221 bye")
						return
					default:
						write("250 OK")
					}
				}
			}(conn)
		}
	}()
	t.Cleanup(f.quit)
	return f
}

// startSilentSMTP: a listener that accepts connections but never replies —
// the connection is held until cleanup, so smtp.SendMail hangs waiting for
// the 220 greeting; the only way Send can exit is the ctx.Done branch.
// Returns the listener port.
func startSilentSMTP(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	t.Cleanup(func() {
		close(done)
		ln.Close()
	})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				<-done
				c.Close()
			}(conn)
		}
	}()
	port, _ := strconv.Atoi(strings.Split(ln.Addr().String(), ":")[1])
	return port
}

func TestSMTPSend(t *testing.T) {
	f := startFakeSMTP(t)
	port, _ := strconv.Atoi(strings.Split(f.addr, ":")[1])
	s := NewSMTP(SMTPConfig{Host: "127.0.0.1", Port: port, From: "wiminder@x.id",
		To: []string{"budi@x.id"}}) // no auth — the fake accepts anything
	if err := s.Send(context.Background(), Message{Title: "🎂 birthday", Body: "message body"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.data, "Subject: =?utf-8?") {
		t.Errorf("subject must be RFC 2047 encoded: %q", f.data)
	}
	// body stays raw UTF-8: the emoji is only encoded in the header.
	if !strings.Contains(f.data, "🎂 birthday") {
		t.Errorf("emoji must stay raw in the HTML body: %q", f.data)
	}
	if !strings.Contains(f.data, "message body") {
		t.Errorf("body text: %q", f.data)
	}
	if !strings.Contains(f.data, "multipart/alternative") {
		t.Errorf("must be multipart: %q", f.data)
	}
	if len(f.rcptTo) != 1 || !strings.Contains(f.rcptTo[0], "budi@x.id") {
		t.Errorf("rcpt: %v", f.rcptTo)
	}
}

func TestSMTPContextTimeout(t *testing.T) {
	// a port that is guaranteed dead: the connection will fail/time out
	s := NewSMTP(SMTPConfig{Host: "127.0.0.1", Port: 1, From: "a@b.c", To: []string{"d@e.f"}})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Send(ctx, Message{Title: "x"}); err == nil {
		t.Error("must fail")
	}
}

func TestSMTPContextCancel(t *testing.T) {
	// the server accepts the connection but never replies: the TCP connection
	// succeeds, so Send can only exit through ctx.Done — validating the
	// <-ctx.Done() branch in the select.
	port := startSilentSMTP(t)
	s := NewSMTP(SMTPConfig{Host: "127.0.0.1", Port: port, From: "a@b.c", To: []string{"d@e.f"}})
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	err := s.Send(ctx, Message{Title: "x"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("must be DeadlineExceeded, got: %v", err)
	}
}
