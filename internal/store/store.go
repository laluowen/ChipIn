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
type PokerSession struct {
	IssueID     string            // Linear issue UUID (primary key)
	Identifier  string            // human identifier, e.g. "ENG-123"
	Title       string            // issue title, for display
	IssueURL    string            // Linear app URL for the issue, for linking back
	Status      string            // StatusVoting | StatusRevealed
	ChannelID   string            // Slack channel the round lives in
	MessageTS   string            // Slack message timestamp, for chat.update
	MessageLink string            // permalink to the vote message, for private-notice context
	RequestedBy string            // Slack userID of whoever ran /chipin, for attribution
	Votes       map[string]string // Slack userID -> vote label
	Scale       poker.Scale       // estimate points derived from the Linear team
}

// SessionRepository persists poker sessions keyed by Linear issue ID.
type SessionRepository interface {
	GetSession(ctx context.Context, issueID string) (*PokerSession, error)
	SaveSession(ctx context.Context, session *PokerSession) error
	DeleteSession(ctx context.Context, issueID string) error
	Close() error
}
