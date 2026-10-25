// Package store defines the persistence boundary for poker sessions and its
// concrete implementations. The SessionRepository interface decouples the core
// logic from the backing store (a JSON file locally, Firestore on GCP).
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
// Firestore and json struct tags are both present so the same type serves
// store.Firestore (Mode B), store.JSONFile (Mode A), and JSON-blob encoding
// without a parallel DTO.
type PokerSession struct {
	IssueID     string            `firestore:"issueId" json:"issueId"`         // Linear issue UUID (primary key)
	Identifier  string            `firestore:"identifier" json:"identifier"`   // human identifier, e.g. "ENG-123"
	Title       string            `firestore:"title" json:"title"`             // issue title, for display
	IssueURL    string            `firestore:"issueUrl" json:"issueUrl"`       // Linear app URL for the issue, for linking back
	Status      string            `firestore:"status" json:"status"`           // StatusVoting | StatusRevealed
	ChannelID   string            `firestore:"channelId" json:"channelId"`     // Slack channel the round lives in
	MessageTS   string            `firestore:"messageTs" json:"messageTs"`     // Slack message timestamp, for chat.update
	MessageLink string            `firestore:"messageLink" json:"messageLink"` // permalink to the vote message, for private-notice context
	RequestedBy string            `firestore:"requestedBy" json:"requestedBy"` // Slack userID of whoever ran /chipin, for attribution
	Votes       map[string]string `firestore:"votes" json:"votes"`             // Slack userID -> vote label
	Scale       poker.Scale       `firestore:"scale" json:"scale"`             // estimate points derived from the Linear team
}

// SessionRepository persists poker sessions keyed by Linear issue ID.
type SessionRepository interface {
	GetSession(ctx context.Context, issueID string) (*PokerSession, error)
	SaveSession(ctx context.Context, session *PokerSession) error
	DeleteSession(ctx context.Context, issueID string) error
	Close() error
}

var (
	_ SessionRepository = (*JSONFile)(nil)
	_ SessionRepository = (*Firestore)(nil)
)
