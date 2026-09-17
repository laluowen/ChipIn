package store

import (
	"context"
	"errors"
	"testing"
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
		IssueID:    "uuid-1",
		Identifier: "ENG-123",
		Title:      "Make it faster",
		Status:     StatusVoting,
		ChannelID:  "C123",
		MessageTS:  "1700000000.000100",
		Votes:      map[string]string{"u1": "5", "u2": "?"},
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
		got.MessageTS != want.MessageTS {
		t.Errorf("scalar fields mismatch:\n got %+v\nwant %+v", got, want)
	}
	if len(got.Votes) != 2 || got.Votes["u1"] != "5" || got.Votes["u2"] != "?" {
		t.Errorf("votes = %v, want %v", got.Votes, want.Votes)
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
