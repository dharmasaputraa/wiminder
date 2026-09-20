package store

import (
	"context"
	"database/sql"
	"errors"

	_ "modernc.org/sqlite" // driver "sqlite", pure-Go
)

var ErrNotFound = errors.New("not found")

type Store struct{ db *sql.DB }

const pragmas = `PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(pragmas); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// OpenInMemory is used by tests — one shared DB via cache=shared.
// Pragmas go through the DSN (_pragma=...) so they apply to EVERY pool
// connection; PRAGMA foreign_keys is scoped per connection.
func OpenInMemory() (*Store, error) {
	db, err := sql.Open("sqlite", "file:wimindertest?mode=memory&cache=shared&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(pragmas); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }
func (s *Store) Close() error                   { return s.db.Close() }
