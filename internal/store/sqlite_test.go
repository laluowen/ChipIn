package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/laluowen/ChipIn/internal/poker"
)

func newTestStore(t *testing.T) *SQLite {
	t.Helper()
	s, err := NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestGetMissingReturnsNotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetSession(context.Background(), "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestSaveAndGetRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	want := &PokerSession{
		IssueID:     "uuid-1",
		Identifier:  "ENG-123",
		Title:       "Make it faster",
		IssueURL:    "https://linear.app/acme/issue/ENG-123",
		Status:      StatusVoting,
		ChannelID:   "C123",
		MessageTS:   "1700000000.000100",
		MessageLink: "https://workspace.slack.com/archives/C123/p1700000000000100",
		RequestedBy: "U1",
		Votes:       map[string]string{"u1": "5", "u2": "?"},
		Scale:       poker.Scale{{Label: "1", Value: 1}, {Label: "M", Value: 3}},
	}
	if err := s.SaveSession(ctx, want); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	got, err := s.GetSession(ctx, "uuid-1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.Identifier != want.Identifier || got.Title != want.Title ||
		got.Status != want.Status || got.ChannelID != want.ChannelID ||
		got.MessageTS != want.MessageTS || got.IssueURL != want.IssueURL ||
		got.MessageLink != want.MessageLink || got.RequestedBy != want.RequestedBy {
		t.Errorf("scalar fields mismatch:\n got %+v\nwant %+v", got, want)
	}
	if len(got.Votes) != 2 || got.Votes["u1"] != "5" || got.Votes["u2"] != "?" {
		t.Errorf("votes = %v, want %v", got.Votes, want.Votes)
	}
	if len(got.Scale) != 2 || got.Scale[1].Label != "M" || got.Scale[1].Value != 3 {
		t.Errorf("scale = %v, want round-tripped points", got.Scale)
	}
}

func TestSaveUpserts(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	sess := &PokerSession{IssueID: "uuid-1", Identifier: "ENG-1", Status: StatusVoting}
	if err := s.SaveSession(ctx, sess); err != nil {
		t.Fatalf("first save: %v", err)
	}
	sess.Status = StatusRevealed
	sess.Votes = map[string]string{"u1": "8"}
	if err := s.SaveSession(ctx, sess); err != nil {
		t.Fatalf("second save: %v", err)
	}

	got, err := s.GetSession(ctx, "uuid-1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.Status != StatusRevealed || got.Votes["u1"] != "8" {
		t.Errorf("upsert failed: %+v", got)
	}
}

func TestDelete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	sess := &PokerSession{IssueID: "uuid-1", Identifier: "ENG-1", Status: StatusVoting}
	if err := s.SaveSession(ctx, sess); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	if err := s.DeleteSession(ctx, "uuid-1"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := s.GetSession(ctx, "uuid-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete err = %v, want ErrNotFound", err)
	}
	// Deleting again is a no-op.
	if err := s.DeleteSession(ctx, "uuid-1"); err != nil {
		t.Errorf("second delete: %v", err)
	}
}

// compile-time check that *SQLite satisfies the interface.
var _ SessionRepository = (*SQLite)(nil)

// TestMigrateAddsScaleColumn simulates opening a database created before any
// of the later columns existed (CREATE TABLE IF NOT EXISTS is a no-op on an
// existing table, so this needs its own migration path; see the "no such
// column: scale" bug this test guards against).
func TestMigrateAddsScaleColumn(t *testing.T) {
	path := t.TempDir() + "/legacy.db"

	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	if _, err := raw.Exec(`CREATE TABLE sessions (
		issue_id   TEXT PRIMARY KEY,
		identifier TEXT NOT NULL,
		title      TEXT NOT NULL,
		status     TEXT NOT NULL,
		channel_id TEXT NOT NULL,
		message_ts TEXT NOT NULL,
		votes      TEXT NOT NULL
	)`); err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	if _, err := raw.Exec(
		`INSERT INTO sessions (issue_id, identifier, title, status, channel_id, message_ts, votes)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"uuid-1", "ENG-1", "Legacy issue", StatusVoting, "C1", "ts1", `{"u1":"5"}`); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw db: %v", err)
	}

	s, err := NewSQLite(path)
	if err != nil {
		t.Fatalf("NewSQLite on legacy db: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	// The pre-existing row should now be readable, with an empty scale.
	got, err := s.GetSession(ctx, "uuid-1")
	if err != nil {
		t.Fatalf("GetSession on legacy row: %v", err)
	}
	if got.Votes["u1"] != "5" || len(got.Scale) != 0 || got.IssueURL != "" || got.MessageLink != "" {
		t.Errorf("legacy row = %+v, want votes u1=5 and empty scale/urls", got)
	}

	// New sessions with a scale should also save and load fine post-migration.
	newSess := &PokerSession{
		IssueID: "uuid-2", Identifier: "ENG-2", Status: StatusVoting,
		Scale: poker.Scale{{Label: "1", Value: 1}},
	}
	if err := s.SaveSession(ctx, newSess); err != nil {
		t.Fatalf("SaveSession after migration: %v", err)
	}
	got2, err := s.GetSession(ctx, "uuid-2")
	if err != nil {
		t.Fatalf("GetSession after migration: %v", err)
	}
	if len(got2.Scale) != 1 || got2.Scale[0].Label != "1" {
		t.Errorf("scale after migration = %v", got2.Scale)
	}
}

// TestMigrateDropsObsoleteNoticesColumn simulates opening a database created
// by an abandoned design (DM-based private notices, keyed per user) that was
// reverted in favour of plain ephemeral messages. The "notices" column from
// that era should be dropped on open, and unrelated data must survive.
func TestMigrateDropsObsoleteNoticesColumn(t *testing.T) {
	path := t.TempDir() + "/dm-era.db"

	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	if _, err := raw.Exec(`CREATE TABLE sessions (
		issue_id     TEXT PRIMARY KEY,
		identifier   TEXT NOT NULL,
		title        TEXT NOT NULL,
		status       TEXT NOT NULL,
		channel_id   TEXT NOT NULL,
		message_ts   TEXT NOT NULL,
		votes        TEXT NOT NULL,
		scale        TEXT NOT NULL DEFAULT '[]',
		notices      TEXT NOT NULL DEFAULT '{}',
		issue_url    TEXT NOT NULL DEFAULT '',
		message_link TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatalf("create dm-era table: %v", err)
	}
	if _, err := raw.Exec(
		`INSERT INTO sessions
			(issue_id, identifier, title, status, channel_id, message_ts, votes, scale, notices, issue_url, message_link)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"uuid-1", "ENG-1", "Old issue", StatusVoting, "C1", "ts1", `{"u1":"5"}`, `[]`,
		`{"u1":{"channel_id":"D1","message_ts":"dm-ts-1"}}`,
		"https://linear.app/acme/issue/ENG-1", "https://workspace.slack.com/archives/C1/pts1"); err != nil {
		t.Fatalf("insert dm-era row: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw db: %v", err)
	}

	s, err := NewSQLite(path)
	if err != nil {
		t.Fatalf("NewSQLite on dm-era db: %v", err)
	}
	defer s.Close()

	rows, err := s.db.Query(`PRAGMA table_info(sessions)`)
	if err != nil {
		t.Fatalf("table_info: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scan table_info: %v", err)
		}
		if name == "notices" {
			t.Error("obsolete 'notices' column should have been dropped")
		}
	}

	// Unrelated data on the row must survive the column drop.
	got, err := s.GetSession(context.Background(), "uuid-1")
	if err != nil {
		t.Fatalf("GetSession after dropping notices: %v", err)
	}
	if got.Votes["u1"] != "5" || got.IssueURL != "https://linear.app/acme/issue/ENG-1" ||
		got.MessageLink != "https://workspace.slack.com/archives/C1/pts1" {
		t.Errorf("row after migration = %+v", got)
	}
	// requested_by is new; it should be backfilled empty rather than error.
	if got.RequestedBy != "" {
		t.Errorf("RequestedBy = %q, want empty backfill", got.RequestedBy)
	}
}
