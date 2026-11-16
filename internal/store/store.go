// Package store is the SQLite persistence layer: accounts (one per SSH public
// key), uploaded bots, finished matches with their replay logs, and win counts.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, no cgo
)

// Limits enforced here so every caller gets them.
const (
	MaxBotsPerAccount = 8
	KeepMatches       = 500 // older matches and their replays are pruned
)

const schema = `
CREATE TABLE IF NOT EXISTS accounts (
	key     TEXT PRIMARY KEY,           -- SHA256 fingerprint of the SSH public key
	name    TEXT NOT NULL,
	wins    INTEGER NOT NULL DEFAULT 0,
	matches INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS bots (
	key    TEXT NOT NULL REFERENCES accounts(key),
	name   TEXT NOT NULL,
	script TEXT NOT NULL,
	PRIMARY KEY (key, name)
);
CREATE TABLE IF NOT EXISTS matches (
	id     INTEGER PRIMARY KEY AUTOINCREMENT,
	ended  INTEGER NOT NULL,            -- unix seconds
	ticks  INTEGER NOT NULL,
	winner TEXT NOT NULL,
	replay BLOB NOT NULL
);`

// Store wraps the database. It is safe for concurrent use.
type Store struct{ db *sql.DB }

// Open opens (and creates if needed) the database at path. Use ":memory:" in tests.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // one writer; also keeps ":memory:" a single database
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	return &Store{db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Touch creates the account for key, or updates its display name.
func (s *Store) Touch(key, name string) error {
	_, err := s.db.Exec(`INSERT INTO accounts (key, name) VALUES (?, ?)
		ON CONFLICT (key) DO UPDATE SET name = excluded.name`, key, name)
	return err
}

// SaveBot stores or replaces one of the account's bots.
func (s *Store) SaveBot(key, name, script string) error {
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM bots WHERE key = ? AND name != ?`, key, name).Scan(&n); err != nil {
		return err
	}
	if n >= MaxBotsPerAccount {
		return fmt.Errorf("bot limit reached (%d per account)", MaxBotsPerAccount)
	}
	_, err := s.db.Exec(`INSERT INTO bots (key, name, script) VALUES (?, ?, ?)
		ON CONFLICT (key, name) DO UPDATE SET script = excluded.script`, key, name, script)
	return err
}

// Bot returns the script of one of the account's bots.
func (s *Store) Bot(key, name string) (string, error) {
	var script string
	err := s.db.QueryRow(`SELECT script FROM bots WHERE key = ? AND name = ?`, key, name).Scan(&script)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("no bot named %q; upload one with: upload-bot %s < file.lua", name, name)
	}
	return script, err
}

// Bots lists the account's bot names.
func (s *Store) Bots(key string) ([]string, error) {
	return s.strings(`SELECT name FROM bots WHERE key = ? ORDER BY name`, key)
}

func (s *Store) strings(q string, args ...any) ([]string, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// SaveMatch records a finished match, credits the accounts that took part
// (winnerKey may be empty), prunes old matches and returns the match ID.
func (s *Store) SaveMatch(ticks int, winner, winnerKey string, players []string, replay []byte) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.Exec(`INSERT INTO matches (ended, ticks, winner, replay) VALUES (?, ?, ?, ?)`,
		time.Now().Unix(), ticks, winner, replay)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	for _, key := range players {
		if _, err := tx.Exec(`UPDATE accounts SET matches = matches + 1 WHERE key = ?`, key); err != nil {
			return 0, err
		}
	}
	if _, err := tx.Exec(`UPDATE accounts SET wins = wins + 1 WHERE key = ?`, winnerKey); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM matches WHERE id <= ?`, id-KeepMatches); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// Replay returns the encoded replay log of a match.
func (s *Store) Replay(id int64) ([]byte, error) {
	var b []byte
	err := s.db.QueryRow(`SELECT replay FROM matches WHERE id = ?`, id).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("no match %d (only the last %d are kept)", id, KeepMatches)
	}
	return b, err
}

// Recent lists the latest matches as display lines.
func (s *Store) Recent(n int) ([]string, error) {
	return s.strings(`SELECT printf('#%-6d %s  %4.0fs  winner: %s', id, datetime(ended, 'unixepoch'), ticks / 20.0, winner)
		FROM matches ORDER BY id DESC LIMIT ?`, n)
}

// Leaderboard lists the accounts with the most wins as display lines.
func (s *Store) Leaderboard(n int) ([]string, error) {
	return s.strings(`SELECT printf('%2d. %-20s %4d wins  %4d matches', row_number() OVER (ORDER BY wins DESC, matches, name), name, wins, matches)
		FROM accounts WHERE matches > 0 ORDER BY wins DESC, matches, name LIMIT ?`, n)
}
