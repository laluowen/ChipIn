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
		Votes:       map[string]string{"u1": "5", "u2": "?"},
		Scale:       poker.Scale{{Label: "1", Value: 1}, {Label: "M", Value: 3}},
		Notices:     map[string]Notice{"u1": {ChannelID: "D1", MessageTS: "dm-ts-1"}},
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
		got.MessageLink != want.MessageLink {
		t.Errorf("scalar fields mismatch:\n got %+v\nwant %+v", got, want)
	}
	if len(got.Votes) != 2 || got.Votes["u1"] != "5" || got.Votes["u2"] != "?" {
		t.Errorf("votes = %v, want %v", got.Votes, want.Votes)
	}
	if len(got.Scale) != 2 || got.Scale[1].Label != "M" || got.Scale[1].Value != 3 {
		t.Errorf("scale = %v, want round-tripped points", got.Scale)
	}
	if n := got.Notices["u1"]; n.ChannelID != "D1" || n.MessageTS != "dm-ts-1" {
		t.Errorf("notices = %v, want round-tripped notice", got.Notices)
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

// TestMigrateAddsScaleColumn simulates opening a database created before the
// "scale" and "notices" columns existed (CREATE TABLE IF NOT EXISTS is a
// no-op on an existing table, so this needs its own migration path; see the
// "no such column: scale" bug this test guards against).
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
	if got.Votes["u1"] != "5" || len(got.Scale) != 0 || len(got.Notices) != 0 ||
		got.IssueURL != "" || got.MessageLink != "" {
		t.Errorf("legacy row = %+v, want votes u1=5 and empty scale/notices/urls", got)
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
