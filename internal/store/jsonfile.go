package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/laluowen/ChipIn/internal/poker"
)

// jsonFileVersion is bumped if the file layout ever changes incompatibly.
const jsonFileVersion = 1

// sqliteMagic is how every SQLite database file starts. ChipIn used to keep
// local data in one, and pointing the JSON store at it deserves a clear
// error rather than a confusing parse failure.
const sqliteMagic = "SQLite format 3\x00"

// JSONFile is a SessionRepository for local (single-process) use. It keeps
// every in-progress session in memory, mirroring the Firestore
// chipin_sessions collection as a map keyed by issue ID. Every SaveSession or
// DeleteSession persists the whole thing by writing a temp file, syncing it
// and renaming it over the old one, so a crash never leaves a partial file.
// That rewrite is fine at this app's scale: sessions are short-lived (a
// session is deleted the moment its estimate is set or it's cancelled), so
// the file never holds more than the currently in-progress rounds.
//
// It is safe for concurrent use within one process, but two processes must
// not share a file: the last writer would win, and there is no lock.
type JSONFile struct {
	mu   sync.Mutex
	path string // "" keeps the data in memory only (tests)
	data fileData

	// write persists the file's bytes; a field so tests can make it fail.
	write func(path string, b []byte) error
}

// fileData is the whole file.
type fileData struct {
	Version  int                      `json:"version"`
	Sessions map[string]*PokerSession `json:"sessions"`
}

// NewJSONFile opens the store at path, creating it on the first write. An
// empty path keeps everything in memory.
func NewJSONFile(path string) (*JSONFile, error) {
	s := &JSONFile{path: path, write: atomicWrite, data: newFileData()}
	if path == "" {
		return s, nil
	}
	b, err := os.ReadFile(path) //nolint:gosec // path is the configured data file
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if bytes.HasPrefix(b, []byte(sqliteMagic)) {
		return nil, fmt.Errorf("%s is an old SQLite database: ChipIn now keeps local data in a JSON file and doesn't migrate it (sessions are short-lived — one is deleted as soon as its estimate is set or it's cancelled, so there's nothing worth carrying forward); delete it or point DB_PATH at a new file", path)
	}
	var d fileData
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if d.Version != jsonFileVersion {
		return nil, fmt.Errorf("%s has version %d, this build understands %d", path, d.Version, jsonFileVersion)
	}
	if d.Sessions == nil {
		d.Sessions = map[string]*PokerSession{}
	}
	s.data = d
	return s, nil
}

func newFileData() fileData {
	return fileData{Version: jsonFileVersion, Sessions: map[string]*PokerSession{}}
}

// GetSession returns a copy of the session for issueID, or ErrNotFound. A
// copy is returned (not the stored pointer) because Votes and Scale are
// mutable and callers shouldn't be able to reach into the store's memory.
func (s *JSONFile) GetSession(_ context.Context, issueID string) (*PokerSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.data.Sessions[issueID]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneSession(sess), nil
}

// SaveSession inserts or overwrites the session. It builds the new state on a
// copy of what it touches and only swaps it into memory once the save to
// disk succeeds, so a failed save changes nothing.
func (s *JSONFile) SaveSession(_ context.Context, sess *PokerSession) error {
	if sess.Votes == nil {
		sess.Votes = map[string]string{}
	}
	cp := cloneSession(sess)

	s.mu.Lock()
	defer s.mu.Unlock()

	next := s.data.cloneShallow()
	next.Sessions[cp.IssueID] = cp
	if err := s.save(next); err != nil {
		return err
	}
	s.data = next
	return nil
}

// DeleteSession removes the session for issueID. Deleting a missing session
// is not an error, and (like SaveSession) doesn't touch the file at all in
// that case.
func (s *JSONFile) DeleteSession(_ context.Context, issueID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data.Sessions[issueID]; !ok {
		return nil
	}
	next := s.data.cloneShallow()
	delete(next.Sessions, issueID)
	if err := s.save(next); err != nil {
		return err
	}
	s.data = next
	return nil
}

// Close implements SessionRepository. Every write is already on disk.
func (s *JSONFile) Close() error { return nil }

// cloneShallow copies the sessions map but shares the *PokerSession values;
// callers replace the ones they touch rather than mutating them in place.
func (d fileData) cloneShallow() fileData {
	out := fileData{Version: d.Version, Sessions: make(map[string]*PokerSession, len(d.Sessions))}
	for k, v := range d.Sessions {
		out.Sessions[k] = v
	}
	return out
}

// cloneSession deep-copies the mutable fields (Votes, Scale) so neither the
// store nor the caller can reach into the other's memory.
func cloneSession(sess *PokerSession) *PokerSession {
	cp := *sess
	cp.Votes = maps0(sess.Votes)
	cp.Scale = append(poker.Scale(nil), sess.Scale...)
	return &cp
}

func maps0[K comparable, V any](m map[K]V) map[K]V {
	out := make(map[K]V, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (s *JSONFile) save(d fileData) error {
	if s.path == "" {
		return nil
	}
	b, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("encode data: %w", err)
	}
	if err := s.write(s.path, b); err != nil {
		return fmt.Errorf("save %s: %w", s.path, err)
	}
	return nil
}

// atomicWrite replaces path with b so that a crash leaves either the old file
// or the new one, never a partial one.
func atomicWrite(path string, b []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	// Persist the rename itself. Best effort: not every platform lets you open a directory.
	if d, derr := os.Open(dir); derr == nil { //nolint:gosec // dir of the configured data file
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
