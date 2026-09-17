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
	issue_id   TEXT PRIMARY KEY,
	identifier TEXT NOT NULL,
	title      TEXT NOT NULL,
	status     TEXT NOT NULL,
	channel_id TEXT NOT NULL,
	message_ts TEXT NOT NULL,
	votes      TEXT NOT NULL
);`

// NewSQLite opens (creating if needed) a SQLite database at path and ensures
// the schema exists. Use ":memory:" for an ephemeral store.
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
	return &SQLite{db: db}, nil
}

// GetSession returns the session for issueID, or ErrNotFound.
func (s *SQLite) GetSession(ctx context.Context, issueID string) (*PokerSession, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT issue_id, identifier, title, status, channel_id, message_ts, votes
		 FROM sessions WHERE issue_id = ?`, issueID)

	var sess PokerSession
	var votesJSON string
	err := row.Scan(&sess.IssueID, &sess.Identifier, &sess.Title,
		&sess.Status, &sess.ChannelID, &sess.MessageTS, &votesJSON)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan session: %w", err)
	}
	if err := json.Unmarshal([]byte(votesJSON), &sess.Votes); err != nil {
		return nil, fmt.Errorf("decode votes: %w", err)
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
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO sessions
			(issue_id, identifier, title, status, channel_id, message_ts, votes)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(issue_id) DO UPDATE SET
			identifier = excluded.identifier,
			title      = excluded.title,
			status     = excluded.status,
			channel_id = excluded.channel_id,
			message_ts = excluded.message_ts,
			votes      = excluded.votes`,
		sess.IssueID, sess.Identifier, sess.Title, sess.Status,
		sess.ChannelID, sess.MessageTS, string(votesJSON))
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
