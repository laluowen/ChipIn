package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/laluowen/ChipIn/internal/poker"
)

// testRepository is the contract every SessionRepository implementation must
// meet. uniq prefixes the issue ID so a shared (emulator) database can be
// reused across runs without cleanup.
func testRepository(t *testing.T, r SessionRepository, uniq string) {
	t.Helper()
	ctx := context.Background()
	id := uniq + "-uuid-1"

	if _, err := r.GetSession(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetSession(missing) err = %v, want ErrNotFound", err)
	}

	want := &PokerSession{
		IssueID:     id,
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
	if err := r.SaveSession(ctx, want); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	got, err := r.GetSession(ctx, id)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.Identifier != want.Identifier || got.Title != want.Title ||
		got.Status != want.Status || got.ChannelID != want.ChannelID ||
		got.MessageTS != want.MessageTS || got.IssueURL != want.IssueURL ||
		got.MessageLink != want.MessageLink || got.RequestedBy != want.RequestedBy {
		t.Errorf("scalar fields mismatch:\n got %+v\nwant %+v", got, want)
	}
	if len(got.Votes) != 2 || got.Votes["u1"] != "5" || got.Votes["u2"] != "8" {
		t.Errorf("votes = %v, want %v", got.Votes, want.Votes)
	}
	if len(got.Scale) != 2 || got.Scale[1].Label != "M" || got.Scale[1].Value != 3 {
		t.Errorf("scale = %v, want round-tripped points", got.Scale)
	}

	// GetSession must return a copy: mutating it must not affect the store.
	got.Votes["u3"] = "13"
	got.Scale = append(got.Scale, poker.Point{Label: "21", Value: 21})
	again, err := r.GetSession(ctx, id)
	if err != nil {
		t.Fatalf("GetSession (second): %v", err)
	}
	if len(again.Votes) != 2 || len(again.Scale) != 2 {
		t.Errorf("mutating a returned session leaked into the store: votes=%v scale=%v", again.Votes, again.Scale)
	}

	// SaveSession upserts.
	want.Status = StatusRevealed
	want.Votes = map[string]string{"u1": "8"}
	if err := r.SaveSession(ctx, want); err != nil {
		t.Fatalf("SaveSession (upsert): %v", err)
	}
	got, err = r.GetSession(ctx, id)
	if err != nil {
		t.Fatalf("GetSession after upsert: %v", err)
	}
	if got.Status != StatusRevealed || len(got.Votes) != 1 || got.Votes["u1"] != "8" {
		t.Errorf("upsert failed: %+v", got)
	}

	if err := r.DeleteSession(ctx, id); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := r.GetSession(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetSession after delete: err = %v, want ErrNotFound", err)
	}
	// Deleting again (or deleting something that never existed) is a no-op.
	if err := r.DeleteSession(ctx, id); err != nil {
		t.Errorf("second delete: %v", err)
	}
	if err := r.DeleteSession(ctx, uniq+"-never-existed"); err != nil {
		t.Errorf("delete of a never-existing session: %v", err)
	}
}

func TestJSONFileRepository(t *testing.T) {
	for name, path := range map[string]string{
		"memory": "",
		"file":   filepath.Join(t.TempDir(), "chipin.json"),
	} {
		t.Run(name, func(t *testing.T) {
			s, err := NewJSONFile(path)
			if err != nil {
				t.Fatalf("NewJSONFile: %v", err)
			}
			t.Cleanup(func() { _ = s.Close() })
			testRepository(t, s, "json-"+name)
		})
	}
}

// TestFirestoreRepository talks to a local emulator
// (`source ./firestore.sh start`) and skips when FIRESTORE_EMULATOR_HOST is
// unset. It never touches live GCP.
func TestFirestoreRepository(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("FIRESTORE_EMULATOR_HOST not set; skipping Firestore emulator test")
	}
	f, err := NewFirestore(context.Background(), "chipin-test", "(default)")
	if err != nil {
		t.Fatalf("NewFirestore: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	uniq := time.Now().Format("150405.000000")
	testRepository(t, f, uniq)
}
