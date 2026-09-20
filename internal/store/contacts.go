package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"wiminder/internal/domain"
)

type Contact struct {
	ID       string `json:"id"`
	OwnerID  string `json:"owner_id"`
	Name     string `json:"name"`
	Nickname string `json:"nickname"`
	Notes    string `json:"notes"`
}

type Occasion struct {
	ID         string                `json:"id"`
	ContactID  string                `json:"contact_id"`
	Type       domain.OccurrenceType `json:"type"`
	Recurrence domain.Recurrence     `json:"recurrence"`
	BaseDate   domain.Date           `json:"base_date"`
	Label      string                `json:"label"`
	Prefs      *OccasionPrefs        `json:"prefs,omitempty"`
}

type ReminderPrefs struct {
	ContactID  string           `json:"contact_id"`
	Offsets    domain.OffsetMap `json:"offsets"`
	ChannelIDs []string         `json:"channel_ids"`
	Enabled    bool             `json:"enabled"`
}

type OccasionPrefs struct {
	OccasionID string           `json:"occasion_id"`
	Offsets    domain.OffsetMap `json:"offsets"`
	ChannelIDs []string         `json:"channel_ids"`
	Enabled    bool             `json:"enabled"`
	Custom     bool             `json:"custom"`
}

type ContactWithOccasions struct {
	Contact
	Occasions []Occasion     `json:"occasions"`
	Prefs     *ReminderPrefs `json:"prefs"`
}

func (s *Store) CreateContact(ctx context.Context, ownerID string, name, nickname, notes string) (Contact, error) {
	id := uuid.Must(uuid.NewV7()).String()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO contacts (id, owner_id, name, nickname, notes) VALUES (?,?,?,?,?)`,
		id, ownerID, name, nickname, notes)
	if err != nil {
		return Contact{}, err
	}
	return Contact{ID: id, OwnerID: ownerID, Name: name, Nickname: nickname, Notes: notes}, nil
}

// ownerScope returns (clause, args): ownerID "" = admin (no filter).
func ownerScope(ownerID string) (string, []any) {
	if ownerID == "" {
		return "1=1", nil
	}
	return "owner_id = ?", []any{ownerID}
}

func (s *Store) ListContacts(ctx context.Context, ownerID string) ([]ContactWithOccasions, error) {
	clause, args := ownerScope(ownerID)
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, owner_id, name, nickname, notes FROM contacts WHERE `+clause+` ORDER BY name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ContactWithOccasions{}
	for rows.Next() {
		var c ContactWithOccasions
		if err := rows.Scan(&c.ID, &c.OwnerID, &c.Name, &c.Nickname, &c.Notes); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if err := s.fill(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) GetContact(ctx context.Context, ownerID, contactID string) (*ContactWithOccasions, error) {
	clause, args := ownerScope(ownerID)
	all := append([]any{contactID}, args...)
	c := &ContactWithOccasions{}
	err := s.db.QueryRowContext(ctx,
		`SELECT id, owner_id, name, nickname, notes FROM contacts WHERE id = ? AND `+clause, all...).
		Scan(&c.ID, &c.OwnerID, &c.Name, &c.Nickname, &c.Notes)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := s.fill(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

func (s *Store) fill(ctx context.Context, c *ContactWithOccasions) error {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, contact_id, type, recurrence, base_date, label FROM occasions WHERE contact_id = ? ORDER BY base_date`, c.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	c.Occasions = []Occasion{} // SPA contract: always an array, never null
	for rows.Next() {
		var o Occasion
		var base string
		if err := rows.Scan(&o.ID, &o.ContactID, &o.Type, &o.Recurrence, &base, &o.Label); err != nil {
			return err
		}
		if o.BaseDate, err = domain.ParseDate(base); err != nil {
			return err
		}
		c.Occasions = append(c.Occasions, o)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	var offsets, channelIDs string
	var enabled int
	err = s.db.QueryRowContext(ctx,
		`SELECT offsets, channel_ids, enabled FROM reminder_prefs WHERE contact_id = ?`, c.ID).
		Scan(&offsets, &channelIDs, &enabled)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// no contact-level prefs: a plain skip — per-occasion overrides are
		// still loaded below
	case err != nil:
		return err
	default:
		p := &ReminderPrefs{ContactID: c.ID, Enabled: enabled == 1, Offsets: domain.OffsetMap{}, ChannelIDs: []string{}}
		if err := json.Unmarshal([]byte(offsets), &p.Offsets); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(channelIDs), &p.ChannelIDs); err != nil {
			return err
		}
		c.Prefs = p
	}

	for i := range c.Occasions {
		op, err := s.getOccasionPrefsRow(ctx, c.Occasions[i].ID)
		if err != nil {
			return err
		}
		c.Occasions[i].Prefs = op
	}
	return nil
}

func (s *Store) UpdateContact(ctx context.Context, ownerID, contactID string, name, nickname, notes string) error {
	clause, args := ownerScope(ownerID)
	all := append([]any{name, nickname, notes, contactID}, args...)
	r, err := s.db.ExecContext(ctx,
		`UPDATE contacts SET name=?, nickname=?, notes=? WHERE id = ? AND `+clause, all...)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteContact(ctx context.Context, ownerID, contactID string) error {
	clause, args := ownerScope(ownerID)
	all := append([]any{contactID}, args...)
	r, err := s.db.ExecContext(ctx,
		`DELETE FROM contacts WHERE id = ? AND `+clause, all...)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DistinctOccasionTypes: the caller's used types plus the built-ins, sorted
// (built-ins first, then the owner's own types alphabetically). Owner-scoped;
// ownerID "" = admin (all contacts).
func (s *Store) DistinctOccasionTypes(ctx context.Context, ownerID string) ([]string, error) {
	clause, args := ownerScope(ownerID)
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT o.type FROM occasions o
		 JOIN contacts c ON c.id = o.contact_id WHERE `+clause+` ORDER BY o.type`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{string(domain.Birthday): true, string(domain.Otonan): true, string(domain.Anniversary): true}
	out := []string{string(domain.Birthday), string(domain.Otonan), string(domain.Anniversary)}
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out, rows.Err()
}

func (s *Store) AddOccasion(ctx context.Context, contactID string, typ domain.OccurrenceType, rec domain.Recurrence, base domain.Date, label string) (Occasion, error) {
	if err := domain.ValidateRecurrence(rec); err != nil {
		return Occasion{}, err
	}
	if typ == "" {
		return Occasion{}, fmt.Errorf("occasion type is required")
	}
	if len(typ) > 64 {
		return Occasion{}, fmt.Errorf("occasion type too long (max 64)")
	}
	id := uuid.Must(uuid.NewV7()).String()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO occasions (id, contact_id, type, recurrence, base_date, label) VALUES (?,?,?,?,?,?)`,
		id, contactID, typ, rec, base.String(), label)
	if err != nil {
		return Occasion{}, err
	}
	return Occasion{ID: id, ContactID: contactID, Type: typ, Recurrence: rec, BaseDate: base, Label: label}, nil
}

// UpdateOccasion is owner-scoped — ownerID "" = admin (all contacts). Full
// replace of the editable fields (type, recurrence, base date, label); the
// row's prefs are untouched.
func (s *Store) UpdateOccasion(ctx context.Context, ownerID, id string, typ domain.OccurrenceType, rec domain.Recurrence, base domain.Date, label string) error {
	if err := domain.ValidateRecurrence(rec); err != nil {
		return err
	}
	if typ == "" {
		return fmt.Errorf("occasion type is required")
	}
	if len(typ) > 64 {
		return fmt.Errorf("occasion type too long (max 64)")
	}
	clause, args := ownerScope(ownerID)
	all := append([]any{typ, rec, base.String(), label, id}, args...)
	r, err := s.db.ExecContext(ctx,
		`UPDATE occasions SET type = ?, recurrence = ?, base_date = ?, label = ? WHERE id = ? AND contact_id IN
			(SELECT id FROM contacts WHERE `+clause+`)`, all...)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteOccasion is owner-scoped — ownerID "" = admin (all contacts).
func (s *Store) DeleteOccasion(ctx context.Context, ownerID, id string) error {
	clause, args := ownerScope(ownerID)
	all := append([]any{id}, args...)
	r, err := s.db.ExecContext(ctx,
		`DELETE FROM occasions WHERE id = ? AND contact_id IN
			(SELECT id FROM contacts WHERE `+clause+`)`, all...)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetReminderPrefs(ctx context.Context, p ReminderPrefs) error {
	off, err := json.Marshal(p.Offsets)
	if err != nil {
		return err
	}
	ch, err := json.Marshal(p.ChannelIDs)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO reminder_prefs (contact_id, offsets, channel_ids, enabled)
		VALUES (?,?,?,?) ON CONFLICT(contact_id) DO UPDATE SET offsets=excluded.offsets,
		channel_ids=excluded.channel_ids, enabled=excluded.enabled`,
		p.ContactID, string(off), string(ch), boolInt(p.Enabled))
	return err
}

// OccasionByID: owner-scoped single occasion ("" ownerID = admin).
func (s *Store) OccasionByID(ctx context.Context, ownerID, occasionID string) (*Occasion, error) {
	clause, args := ownerScope(ownerID)
	all := append([]any{occasionID}, args...)
	var o Occasion
	var base string
	err := s.db.QueryRowContext(ctx,
		`SELECT o.id, o.contact_id, o.type, o.recurrence, o.base_date, o.label
		 FROM occasions o JOIN contacts c ON c.id = o.contact_id
		 WHERE o.id = ? AND `+clause, all...).
		Scan(&o.ID, &o.ContactID, &o.Type, &o.Recurrence, &base, &o.Label)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if o.BaseDate, err = domain.ParseDate(base); err != nil {
		return nil, err
	}
	if o.Prefs, err = s.getOccasionPrefsRow(ctx, o.ID); err != nil {
		return nil, err
	}
	return &o, nil
}

func (s *Store) getOccasionPrefsRow(ctx context.Context, occasionID string) (*OccasionPrefs, error) {
	var offsets, channelIDs string
	var enabled, custom int
	err := s.db.QueryRowContext(ctx,
		`SELECT offsets, channel_ids, enabled, custom FROM occasion_prefs WHERE occasion_id = ?`, occasionID).
		Scan(&offsets, &channelIDs, &enabled, &custom)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil // inherit — not an error
	}
	if err != nil {
		return nil, err
	}
	p := &OccasionPrefs{OccasionID: occasionID, Enabled: enabled == 1, Custom: custom == 1,
		Offsets: domain.OffsetMap{}, ChannelIDs: []string{}}
	if err := json.Unmarshal([]byte(offsets), &p.Offsets); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(channelIDs), &p.ChannelIDs); err != nil {
		return nil, err
	}
	return p, nil
}

func (s *Store) SetOccasionPrefs(ctx context.Context, p OccasionPrefs) error {
	off, err := json.Marshal(p.Offsets)
	if err != nil {
		return err
	}
	ch, err := json.Marshal(p.ChannelIDs)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO occasion_prefs (occasion_id, offsets, channel_ids, enabled, custom)
		VALUES (?,?,?,?,?) ON CONFLICT(occasion_id) DO UPDATE SET offsets=excluded.offsets,
		channel_ids=excluded.channel_ids, enabled=excluded.enabled, custom=excluded.custom`,
		p.OccasionID, string(off), string(ch), boolInt(p.Enabled), boolInt(p.Custom))
	return err
}

func (s *Store) DeleteOccasionPrefs(ctx context.Context, occasionID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM occasion_prefs WHERE occasion_id = ?`, occasionID)
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
