// Package store defines the persistence boundary for poker sessions and its
// concrete implementations. The SessionRepository interface decouples the core
// logic from the backing store (SQLite locally, Firestore in the future).
package store

import (
	"context"
	"errors"

	"github.com/laluowen/ChipIn/internal/poker"
)

// ErrNotFound is returned by GetSession when no session exists for the issue.
var ErrNotFound = errors.New("session not found")

// Session status values.
const (
	StatusVoting   = "voting"
	StatusRevealed = "revealed"
)

// PokerSession is the persisted state of an in-progress estimation round.
//
// Firestore struct tags are present alongside the SQLite JSON-blob encoding so
// the same type serves both store.Firestore (Mode B) and store.SQLite (Mode
// A) without a parallel DTO.
type PokerSession struct {
	IssueID     string            `firestore:"issueId"`     // Linear issue UUID (primary key)
	Identifier  string            `firestore:"identifier"`  // human identifier, e.g. "ENG-123"
	Title       string            `firestore:"title"`       // issue title, for display
	IssueURL    string            `firestore:"issueUrl"`    // Linear app URL for the issue, for linking back
	Status      string            `firestore:"status"`      // StatusVoting | StatusRevealed
	ChannelID   string            `firestore:"channelId"`   // Slack channel the round lives in
	MessageTS   string            `firestore:"messageTs"`   // Slack message timestamp, for chat.update
	MessageLink string            `firestore:"messageLink"` // permalink to the vote message, for private-notice context
	RequestedBy string            `firestore:"requestedBy"` // Slack userID of whoever ran /chipin, for attribution
	Votes       map[string]string `firestore:"votes"`       // Slack userID -> vote label
	Scale       poker.Scale       `firestore:"scale"`       // estimate points derived from the Linear team
}

// SessionRepository persists poker sessions keyed by Linear issue ID.
type SessionRepository interface {
	GetSession(ctx context.Context, issueID string) (*PokerSession, error)
	SaveSession(ctx context.Context, session *PokerSession) error
	DeleteSession(ctx context.Context, issueID string) error
	Close() error
}
