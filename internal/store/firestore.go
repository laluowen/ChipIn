package store

import (
	"context"
	"fmt"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// sessionsCollection is the Firestore collection poker sessions live in,
// keyed by document ID = PokerSession.IssueID.
const sessionsCollection = "chipin_sessions"

// Firestore is a SessionRepository backed by Google Cloud Firestore, for
// Mode B (serverless / Cloud Run webhook) deployments. Unlike SQLite, no
// JSON-blob encoding step is needed: PokerSession carries `firestore:"..."`
// tags so the client (de)serializes it directly.
type Firestore struct {
	client *firestore.Client
}

// NewFirestore opens a Firestore client for projectID/databaseID. Pass
// databaseID as "(default)" for the default database. If the
// FIRESTORE_EMULATOR_HOST environment variable is set, the client connects to
// the emulator instead of production Firestore (handled inside the firestore
// package itself).
func NewFirestore(ctx context.Context, projectID, databaseID string) (*Firestore, error) {
	client, err := firestore.NewClientWithDatabase(ctx, projectID, databaseID)
	if err != nil {
		return nil, fmt.Errorf("open firestore client (project %q, database %q): %w", projectID, databaseID, err)
	}
	return &Firestore{client: client}, nil
}

func (f *Firestore) doc(issueID string) *firestore.DocumentRef {
	return f.client.Collection(sessionsCollection).Doc(issueID)
}

// GetSession returns the session for issueID, or ErrNotFound.
func (f *Firestore) GetSession(ctx context.Context, issueID string) (*PokerSession, error) {
	snap, err := f.doc(issueID).Get(ctx)
	if status.Code(err) == codes.NotFound {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	var sess PokerSession
	if err := snap.DataTo(&sess); err != nil {
		return nil, fmt.Errorf("decode session: %w", err)
	}
	return &sess, nil
}

// SaveSession inserts or overwrites the session document for sess.IssueID.
func (f *Firestore) SaveSession(ctx context.Context, sess *PokerSession) error {
	if sess.Votes == nil {
		sess.Votes = map[string]string{}
	}
	if _, err := f.doc(sess.IssueID).Set(ctx, sess); err != nil {
		return fmt.Errorf("save session: %w", err)
	}
	return nil
}

// DeleteSession removes the session for issueID. Deleting a missing session
// is not an error (matches store.SQLite's behavior).
func (f *Firestore) DeleteSession(ctx context.Context, issueID string) error {
	if _, err := f.doc(issueID).Delete(ctx); err != nil && status.Code(err) != codes.NotFound {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// Close releases the underlying Firestore client.
func (f *Firestore) Close() error {
	return f.client.Close()
}
