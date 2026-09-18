package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (no CGO)
)

// SQLite is a SessionRepository backed by a local SQLite database.
type SQLite struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
	issue_id     TEXT PRIMARY KEY,
	identifier   TEXT NOT NULL,
	title        TEXT NOT NULL,
	status       TEXT NOT NULL,
	channel_id   TEXT NOT NULL,
	message_ts   TEXT NOT NULL,
	votes        TEXT NOT NULL,
	scale        TEXT NOT NULL DEFAULT '[]',
	issue_url    TEXT NOT NULL DEFAULT '',
	message_link TEXT NOT NULL DEFAULT '',
	requested_by TEXT NOT NULL DEFAULT ''
);`

// migratedColumns lists columns added after the table's initial creation,
// each with the default to backfill on existing rows.
var migratedColumns = map[string]string{
	"scale":        "TEXT NOT NULL DEFAULT '[]'",
	"issue_url":    "TEXT NOT NULL DEFAULT ''",
	"message_link": "TEXT NOT NULL DEFAULT ''",
	"requested_by": "TEXT NOT NULL DEFAULT ''",
}

// obsoleteColumns lists columns from a since-abandoned design (a DM-based
// private-notice mechanism, keyed per user) that should be dropped if found on
// an existing database. SQLite has supported DROP COLUMN since 3.35 (2021),
// well within what modernc.org/sqlite bundles.
var obsoleteColumns = []string{"notices"}

// NewSQLite opens (creating if needed) a SQLite database at path and ensures
// the schema exists, migrating older databases forward. Use ":memory:" for an
// ephemeral store.
func NewSQLite(path string) (*SQLite, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", path, err)
	}
	// modernc.org/sqlite serialises access poorly under concurrent writers;
	// a single connection keeps writes ordered and avoids "database is locked".
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate schema: %w", err)
	}
	return &SQLite{db: db}, nil
}

// migrate reconciles the sessions table with the current schema.
// CREATE TABLE IF NOT EXISTS is a no-op against an existing table, so columns
// added or removed after a database's initial creation need explicit ALTER
// TABLE statements to catch up: ADD COLUMN for new fields, DROP COLUMN for
// abandoned ones (see obsoleteColumns).
func migrate(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(sessions)`)
	if err != nil {
		return fmt.Errorf("inspect sessions table: %w", err)
	}
	existing := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			rows.Close()
			return fmt.Errorf("scan table_info: %w", err)
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("read table_info: %w", err)
	}
	rows.Close()

	for col, def := range migratedColumns {
		if existing[col] {
			continue
		}
		if _, err := db.Exec(fmt.Sprintf(`ALTER TABLE sessions ADD COLUMN %s %s`, col, def)); err != nil {
			return fmt.Errorf("add %s column: %w", col, err)
		}
	}

	for _, col := range obsoleteColumns {
		if !existing[col] {
			continue
		}
		if _, err := db.Exec(fmt.Sprintf(`ALTER TABLE sessions DROP COLUMN %s`, col)); err != nil {
			return fmt.Errorf("drop %s column: %w", col, err)
		}
	}
	return nil
}

// GetSession returns the session for issueID, or ErrNotFound.
func (s *SQLite) GetSession(ctx context.Context, issueID string) (*PokerSession, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT issue_id, identifier, title, status, channel_id, message_ts, votes, scale, issue_url, message_link, requested_by
		 FROM sessions WHERE issue_id = ?`, issueID)

	var sess PokerSession
	var votesJSON, scaleJSON string
	err := row.Scan(&sess.IssueID, &sess.Identifier, &sess.Title,
		&sess.Status, &sess.ChannelID, &sess.MessageTS, &votesJSON, &scaleJSON,
		&sess.IssueURL, &sess.MessageLink, &sess.RequestedBy)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan session: %w", err)
	}
	if err := json.Unmarshal([]byte(votesJSON), &sess.Votes); err != nil {
		return nil, fmt.Errorf("decode votes: %w", err)
	}
	if err := json.Unmarshal([]byte(scaleJSON), &sess.Scale); err != nil {
		return nil, fmt.Errorf("decode scale: %w", err)
	}
	return &sess, nil
}

// SaveSession inserts or updates the session.
func (s *SQLite) SaveSession(ctx context.Context, sess *PokerSession) error {
	if sess.Votes == nil {
		sess.Votes = map[string]string{}
	}
	votesJSON, err := json.Marshal(sess.Votes)
	if err != nil {
		return fmt.Errorf("encode votes: %w", err)
	}
	scaleJSON, err := json.Marshal(sess.Scale)
	if err != nil {
		return fmt.Errorf("encode scale: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO sessions
			(issue_id, identifier, title, status, channel_id, message_ts, votes, scale, issue_url, message_link, requested_by)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(issue_id) DO UPDATE SET
			identifier   = excluded.identifier,
			title        = excluded.title,
			status       = excluded.status,
			channel_id   = excluded.channel_id,
			message_ts   = excluded.message_ts,
			votes        = excluded.votes,
			scale        = excluded.scale,
			issue_url    = excluded.issue_url,
			message_link = excluded.message_link,
			requested_by = excluded.requested_by`,
		sess.IssueID, sess.Identifier, sess.Title, sess.Status,
		sess.ChannelID, sess.MessageTS, string(votesJSON), string(scaleJSON),
		sess.IssueURL, sess.MessageLink, sess.RequestedBy)
	if err != nil {
		return fmt.Errorf("save session: %w", err)
	}
	return nil
}

// DeleteSession removes the session for issueID. Deleting a missing session is
// not an error.
func (s *SQLite) DeleteSession(ctx context.Context, issueID string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE issue_id = ?`, issueID); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// Close releases the underlying database handle.
func (s *SQLite) Close() error {
	return s.db.Close()
}
