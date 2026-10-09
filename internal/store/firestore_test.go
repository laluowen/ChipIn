package store

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/laluowen/ChipIn/internal/poker"
)

// newTestFirestore connects to a local Firestore emulator
// (`gcloud emulators firestore start` / `firebase emulators:start`) when
// FIRESTORE_EMULATOR_HOST is set, and skips the test otherwise. These tests
// never touch live GCP Firestore.
func newTestFirestore(t *testing.T) *Firestore {
	t.Helper()
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("FIRESTORE_EMULATOR_HOST not set; skipping Firestore emulator test")
	}
	s, err := NewFirestore(context.Background(), "chipin-test", "(default)")
	if err != nil {
		t.Fatalf("NewFirestore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestFirestoreGetMissingReturnsNotFound(t *testing.T) {
	s := newTestFirestore(t)
	_, err := s.GetSession(context.Background(), "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestFirestoreSaveAndGetRoundTrip(t *testing.T) {
	s := newTestFirestore(t)
	ctx := context.Background()

	want := &PokerSession{
		IssueID:     "fs-uuid-1",
		Identifier:  "ENG-123",
		Title:       "Make it faster",
		IssueURL:    "https://linear.app/acme/issue/ENG-123",
		Status:      StatusVoting,
		ChannelID:   "C123",
		MessageTS:   "1700000000.000100",
		MessageLink: "https://workspace.slack.com/archives/C123/p1700000000000100",
		RequestedBy: "U1",
		Votes:       map[string]string{"u1": "5", "u2": "8"},
		Scale:       poker.Scale{{Label: "1", Value: 1}, {Label: "M", Value: 3}},
	}
	t.Cleanup(func() { _ = s.DeleteSession(ctx, want.IssueID) })

	if err := s.SaveSession(ctx, want); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	got, err := s.GetSession(ctx, want.IssueID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.Identifier != want.Identifier || got.Title != want.Title ||
		got.Status != want.Status || got.ChannelID != want.ChannelID ||
		got.MessageTS != want.MessageTS || got.IssueURL != want.IssueURL ||
		got.MessageLink != want.MessageLink || got.RequestedBy != want.RequestedBy {
		t.Errorf("scalar fields mismatch:\n got %+v\nwant %+v", got, want)
	}
	if len(got.Votes) != len(want.Votes) {
		t.Errorf("votes = %v, want %v", got.Votes, want.Votes)
	}
	for k, v := range want.Votes {
		if got.Votes[k] != v {
			t.Errorf("votes[%s] = %q, want %q", k, got.Votes[k], v)
		}
	}
	if len(got.Scale) != len(want.Scale) {
		t.Errorf("scale = %v, want %v", got.Scale, want.Scale)
	}

	if err := s.DeleteSession(ctx, want.IssueID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := s.GetSession(ctx, want.IssueID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetSession after delete: err = %v, want ErrNotFound", err)
	}
}

func TestFirestoreDeleteMissingIsNotError(t *testing.T) {
	s := newTestFirestore(t)
	if err := s.DeleteSession(context.Background(), "never-existed"); err != nil {
		t.Fatalf("DeleteSession on missing doc: %v", err)
	}
}
