package notify

import (
	"strings"
	"testing"

	"wiminder/internal/domain"
)

func TestTanggalIndo(t *testing.T) {
	got := TanggalIndo(domain.NewDate(2026, 6, 17))
	if got != "Wednesday, 17 June 2026" {
		t.Errorf("TanggalIndo = %q", got)
	}
}

func TestOccurrenceMessageOtonan(t *testing.T) {
	occ := domain.Occurrence{Date: domain.NewDate(2026, 6, 17), Type: domain.Otonan,
		Number: 12, Label: "Otonan #12 — Buda Kliwon, Wuku Dunggulan"}
	m := OccurrenceMessage("Made", occ, 3, false)
	if !strings.Contains(m.Title, "🛕") || !strings.Contains(m.Title, "Made") ||
		!strings.Contains(m.Title, "Otonan #12") {
		t.Errorf("title = %q", m.Title)
	}
	if !strings.Contains(m.Body, "in 3 days") || !strings.Contains(m.Body, "Wednesday, 17 June 2026") {
		t.Errorf("body = %q", m.Body)
	}
	if m.Priority != 5 {
		t.Errorf("priority = %d", m.Priority)
	}
}

func TestOccurrenceMessageBirthdayToday(t *testing.T) {
	occ := domain.Occurrence{Date: domain.NewDate(2026, 6, 17), Type: domain.Birthday,
		Stream: domain.StreamYearly, Number: 36, Label: "Birthday #36"}
	m := OccurrenceMessage("Budi", occ, 0, false)
	if !strings.Contains(m.Title, "🎂") || !strings.Contains(m.Title, "today") {
		t.Errorf("title = %q", m.Title)
	}
	if m.Priority != 8 {
		t.Errorf("today must have priority 8, got %d", m.Priority)
	}
}

func TestOccurrenceMessagesStreams(t *testing.T) {
	anniv := domain.Occurrence{Date: domain.NewDate(2026, 8, 16), Type: domain.Anniversary,
		Stream: domain.StreamMonthly, Number: 14, Label: "Anniversary 1 year 2 months"}
	m := OccurrenceMessage("Ani", anniv, 3, false)
	if m.Title != "🎊 Ani — Anniversary 1 year 2 months in 3 days" {
		t.Errorf("monthly title: %q", m.Title)
	}
	y := OccurrenceMessage("Ani", anniv, 0, false) // same struct, daysUntil 0
	if y.Priority != 8 {
		t.Errorf("day-of priority: %d", y.Priority)
	}
	b := OccurrenceMessage("Ben", domain.Occurrence{Date: domain.NewDate(2026, 1, 1), Type: domain.Birthday,
		Stream: domain.StreamYearly, Number: 30, Label: "Birthday #30"}, 7, false)
	if b.Title != "🎂 Ben — Birthday #30 in 7 days" {
		t.Errorf("birthday title: %q", b.Title)
	}
	g := OccurrenceMessage("Cy", domain.Occurrence{Date: domain.NewDate(2026, 5, 10), Type: "graduation",
		Stream: domain.StreamEvent, Label: "Graduation"}, 2, false)
	if g.Title != "🎉 Cy — Graduation in 2 days" {
		t.Errorf("once title: %q", g.Title)
	}
}

func TestLateSuffix(t *testing.T) {
	m := OccurrenceMessage("Budi", domain.Occurrence{Date: domain.NewDate(2026, 6, 17),
		Type: domain.Birthday, Stream: domain.StreamYearly, Number: 30, Label: "Birthday #30"}, 1, true)
	if !strings.Contains(m.Body, "Sent late") {
		t.Errorf("late flag not visible: %q", m.Body)
	}
}

func TestHolidayMessage(t *testing.T) {
	m := HolidayMessage(domain.Holiday{Date: domain.NewDate(2026, 6, 17), Name: "Galungan"}, 10, false)
	if !strings.Contains(m.Title, "Galungan") || !strings.Contains(m.Title, "in 10 days") {
		t.Errorf("title = %q", m.Title)
	}
	if !strings.Contains(m.Body, "Wednesday, 17 June 2026") {
		t.Errorf("body = %q", m.Body)
	}
	if m.Priority != 5 {
		t.Errorf("in 10 days must have priority 5, got %d", m.Priority)
	}
	today := HolidayMessage(domain.Holiday{Date: domain.NewDate(2026, 6, 17), Name: "Galungan"}, 0, false)
	if today.Priority != 8 {
		t.Errorf("today must have priority 8, got %d", today.Priority)
	}
}
