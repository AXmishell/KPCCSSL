// Package store owns the SQLite schema and every SQL statement in the
// application. Handlers and services never build SQL themselves.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"github.com/axmishell/kpccssl/migrations"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// ErrConflict is returned for optimistic-lock or uniqueness conflicts.
var ErrConflict = errors.New("conflict")

// Store is the database handle.
type Store struct {
	db *sql.DB
}

// Open opens (and creates) the SQLite database in WAL mode.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create database dir: %w", err)
	}
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite + single writer; avoids SQLITE_BUSY storms.
	db.SetConnMaxLifetime(0)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// DB exposes the handle for the migration CLI only.
func (s *Store) DB() *sql.DB { return s.db }

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Migrate applies all embedded migrations.
func (s *Store) Migrate(ctx context.Context) error {
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		return err
	}
	return goose.UpContext(ctx, s.db, ".")
}

// Now returns the current Unix time in seconds.
func Now() int64 { return time.Now().UTC().Unix() }

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func ns(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func ni(i int64) *int64 {
	if i == 0 {
		return nil
	}
	return &i
}
