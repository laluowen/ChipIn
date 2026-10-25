package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/laluowen/ChipIn/internal/poker"
)

const testIssueID = "uuid-1"

func jsonStore(t *testing.T, path string) *JSONFile {
	t.Helper()
	s, err := NewJSONFile(path)
	if err != nil {
		t.Fatalf("NewJSONFile(%q): %v", path, err)
	}
	return s
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // a file in the test's temp dir
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestJSONFilePersistsAndReloads(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "nested", "dir", "chipin.json") // the directories don't exist yet

	s := jsonStore(t, path)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a new store shouldn't write anything until the first save: %v", err)
	}

	sess := &PokerSession{
		IssueID:    testIssueID,
		Identifier: "ENG-1",
		Title:      "Reload me",
		Status:     StatusVoting,
		ChannelID:  "C1",
		MessageTS:  "ts1",
		Votes:      map[string]string{"u1": "5"},
		Scale:      poker.Scale{{Label: "5", Value: 5}},
	}
	if err := s.SaveSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	b := readFile(t, path)
	var raw struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(b, &raw); err != nil || raw.Version != jsonFileVersion {
		t.Fatalf("file isn't valid JSON with a version: %v %s", err, b)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("expected only the data file, no leftover temp files: %v", entries)
	}

	r := jsonStore(t, path) // reopen from disk
	defer func() { _ = r.Close() }()
	got, err := r.GetSession(ctx, testIssueID)
	if err != nil {
		t.Fatalf("GetSession after reload: %v", err)
	}
	if got.Title != "Reload me" || got.Votes["u1"] != "5" || len(got.Scale) != 1 {
		t.Errorf("session after reload = %+v", got)
	}
}

func TestJSONFileRejectsBadFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for name, tc := range map[string]struct{ content, want string }{
		"corrupt":     {`{"version": 1, "sessions": {`, "parse"},
		"version":     {`{"version": 99}`, "version 99"},
		"sqlite":      {"SQLite format 3\x00\x10\x00\x01\x01", "old SQLite database"},
		"unversioned": {`{"sessions": {}}`, "version 0"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NewJSONFile(write(name+".json", tc.content))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

func TestJSONFileFailedSaveChangesNothing(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "chipin.json")
	s := jsonStore(t, path)
	sess := &PokerSession{IssueID: testIssueID, Identifier: "ENG-1", Status: StatusVoting}
	if err := s.SaveSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	before := readFile(t, path)

	s.write = func(string, []byte) error { return errors.New("disk full") }
	sess.Status = StatusRevealed
	if err := s.SaveSession(ctx, sess); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("err = %v, want the write failure", err)
	}
	got, err := s.GetSession(ctx, testIssueID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusVoting {
		t.Errorf("a failed save must leave memory unchanged, got status = %q", got.Status)
	}
	if string(readFile(t, path)) != string(before) {
		t.Error("the file changed despite the failed save")
	}

	s.write = atomicWrite // and it recovers
	if err := s.SaveSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetSession(ctx, testIssueID); got.Status != StatusRevealed {
		t.Errorf("status = %q, want revealed after recovering", got.Status)
	}
}

func TestJSONFileFailedDeleteChangesNothing(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "chipin.json")
	s := jsonStore(t, path)
	if err := s.SaveSession(ctx, &PokerSession{IssueID: testIssueID, Status: StatusVoting}); err != nil {
		t.Fatal(err)
	}

	s.write = func(string, []byte) error { return errors.New("disk full") }
	if err := s.DeleteSession(ctx, testIssueID); err == nil {
		t.Fatal("expected the write failure to surface")
	}
	s.write = atomicWrite
	if _, err := s.GetSession(ctx, testIssueID); err != nil {
		t.Errorf("a failed delete must leave the session in place: %v", err)
	}
}

func TestJSONFileConcurrentSaves(t *testing.T) {
	s := jsonStore(t, filepath.Join(t.TempDir(), "chipin.json"))
	const n = 25
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("uuid-%02d", i)
			if err := s.SaveSession(context.Background(), &PokerSession{IssueID: id, Status: StatusVoting}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()

	s.mu.Lock()
	got := len(s.data.Sessions)
	s.mu.Unlock()
	if got != n {
		t.Errorf("sessions = %d, want %d: concurrent saves were lost", got, n)
	}
}
