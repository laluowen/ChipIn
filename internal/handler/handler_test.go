package handler

import (
	"context"
	"errors"
	"testing"

	"github.com/laluowen/ChipIn/internal/linear"
	"github.com/laluowen/ChipIn/internal/store"
	"github.com/slack-go/slack"
)

// --- fakes ---

type fakeSlack struct {
	postCh, postTS string
	posts          int
	updates        int
	lastUpdateTS   string
	nextTS         string
}

func (f *fakeSlack) PostMessage(channelID string, _ ...slack.MsgOption) (string, string, error) {
	f.posts++
	f.postCh = channelID
	ts := f.nextTS
	if ts == "" {
		ts = "1700000000.000100"
	}
	f.postTS = ts
	return channelID, ts, nil
}

func (f *fakeSlack) UpdateMessage(channelID, timestamp string, _ ...slack.MsgOption) (string, string, string, error) {
	f.updates++
	f.lastUpdateTS = timestamp
	return channelID, timestamp, "", nil
}

type fakeLinear struct {
	issue       *linear.Issue
	fetchErr    error
	setCalled   bool
	setIssueID  string
	setEstimate float64
}

func (f *fakeLinear) FetchIssue(_ context.Context, _ string) (*linear.Issue, error) {
	return f.issue, f.fetchErr
}

func (f *fakeLinear) SetEstimate(_ context.Context, issueID string, estimate float64) error {
	f.setCalled = true
	f.setIssueID = issueID
	f.setEstimate = estimate
	return nil
}

func newTestHandler(t *testing.T, l *fakeLinear) (*PokerHandler, *fakeSlack, store.SessionRepository) {
	t.Helper()
	repo, err := store.NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { repo.Close() })
	fs := &fakeSlack{}
	return New(fs, l, repo), fs, repo
}

// --- tests ---

func TestSlashCommandCreatesSession(t *testing.T) {
	fl := &fakeLinear{issue: &linear.Issue{ID: "uuid-1", Identifier: "ENG-1", Title: "Do the thing"}}
	h, fs, repo := newTestHandler(t, fl)
	fs.nextTS = "1700000000.000200"

	cmd := slack.SlashCommand{ChannelID: "C1", UserID: "U1", Text: "ENG-1"}
	if err := h.HandleSlashCommand(context.Background(), cmd); err != nil {
		t.Fatalf("HandleSlashCommand: %v", err)
	}

	sess, err := repo.GetSession(context.Background(), "uuid-1")
	if err != nil {
		t.Fatalf("session not saved: %v", err)
	}
	if sess.Status != store.StatusVoting {
		t.Errorf("status = %q, want voting", sess.Status)
	}
	if sess.MessageTS != "1700000000.000200" {
		t.Errorf("MessageTS = %q, want the posted ts", sess.MessageTS)
	}
	if sess.ChannelID != "C1" {
		t.Errorf("ChannelID = %q", sess.ChannelID)
	}
}

func TestSlashCommandEmptyTextIsEphemeral(t *testing.T) {
	fl := &fakeLinear{}
	h, _, repo := newTestHandler(t, fl)

	cmd := slack.SlashCommand{ChannelID: "C1", UserID: "U1", Text: "  "}
	if err := h.HandleSlashCommand(context.Background(), cmd); err != nil {
		t.Fatalf("HandleSlashCommand: %v", err)
	}
	// No session should be created.
	if _, err := repo.GetSession(context.Background(), "uuid-1"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unexpected session created")
	}
}

func TestVoteRecordsAndUpdates(t *testing.T) {
	fl := &fakeLinear{}
	h, fs, repo := newTestHandler(t, fl)
	seed(t, repo, &store.PokerSession{
		IssueID: "uuid-1", Identifier: "ENG-1", Status: store.StatusVoting,
		ChannelID: "C1", MessageTS: "ts1", Votes: map[string]string{},
	})

	cb := interaction("U1", actionVotePrefix+"5", "uuid-1")
	if err := h.HandleInteraction(context.Background(), cb); err != nil {
		t.Fatalf("HandleInteraction: %v", err)
	}

	sess, _ := repo.GetSession(context.Background(), "uuid-1")
	if sess.Votes["U1"] != "5" {
		t.Errorf("vote = %q, want 5", sess.Votes["U1"])
	}
	if fs.updates != 1 || fs.lastUpdateTS != "ts1" {
		t.Errorf("expected one update to ts1, got updates=%d ts=%q", fs.updates, fs.lastUpdateTS)
	}
	// The voter gets a private ephemeral confirmation of their pick.
	if fs.posts != 1 {
		t.Errorf("expected 1 ephemeral confirmation post, got %d", fs.posts)
	}
}

func TestCancelDeletesSessionAndUpdatesMessage(t *testing.T) {
	fl := &fakeLinear{}
	h, fs, repo := newTestHandler(t, fl)
	seed(t, repo, &store.PokerSession{
		IssueID: "uuid-1", Status: store.StatusVoting, ChannelID: "C1", MessageTS: "ts1",
		Votes: map[string]string{"U1": "5"},
	})

	if err := h.HandleInteraction(context.Background(), interaction("U1", actionCancel, "uuid-1")); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := repo.GetSession(context.Background(), "uuid-1"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("session should be deleted after cancel")
	}
	if fl.setCalled {
		t.Error("cancel must not write an estimate to Linear")
	}
	if fs.updates != 1 || fs.lastUpdateTS != "ts1" {
		t.Errorf("expected the message to be updated to cancelled state, got updates=%d", fs.updates)
	}
}

func TestInvalidVoteLabelIgnored(t *testing.T) {
	fl := &fakeLinear{}
	h, _, repo := newTestHandler(t, fl)
	seed(t, repo, &store.PokerSession{
		IssueID: "uuid-1", Status: store.StatusVoting, ChannelID: "C1", MessageTS: "ts1",
		Votes: map[string]string{},
	})

	cb := interaction("U1", actionVotePrefix+"999", "uuid-1")
	if err := h.HandleInteraction(context.Background(), cb); err != nil {
		t.Fatalf("HandleInteraction: %v", err)
	}
	sess, _ := repo.GetSession(context.Background(), "uuid-1")
	if len(sess.Votes) != 0 {
		t.Errorf("invalid vote should be ignored, got %v", sess.Votes)
	}
}

func TestRevealThenContinueTogglesStatus(t *testing.T) {
	fl := &fakeLinear{}
	h, _, repo := newTestHandler(t, fl)
	seed(t, repo, &store.PokerSession{
		IssueID: "uuid-1", Status: store.StatusVoting, ChannelID: "C1", MessageTS: "ts1",
		Votes: map[string]string{"U1": "3", "U2": "8"},
	})

	if err := h.HandleInteraction(context.Background(), interaction("U1", actionReveal, "uuid-1")); err != nil {
		t.Fatalf("reveal: %v", err)
	}
	sess, _ := repo.GetSession(context.Background(), "uuid-1")
	if sess.Status != store.StatusRevealed {
		t.Fatalf("status = %q, want revealed", sess.Status)
	}

	if err := h.HandleInteraction(context.Background(), interaction("U1", actionContinue, "uuid-1")); err != nil {
		t.Fatalf("continue: %v", err)
	}
	sess, _ = repo.GetSession(context.Background(), "uuid-1")
	if sess.Status != store.StatusVoting {
		t.Fatalf("status = %q, want voting", sess.Status)
	}
}

func TestSetEstimateWritesLinearAndDeletes(t *testing.T) {
	fl := &fakeLinear{}
	h, fs, repo := newTestHandler(t, fl)
	seed(t, repo, &store.PokerSession{
		IssueID: "uuid-1", Status: store.StatusRevealed, ChannelID: "C1", MessageTS: "ts1",
		Votes: map[string]string{"U1": "5", "U2": "5", "U3": "8"}, // median 5
	})

	if err := h.HandleInteraction(context.Background(), interaction("U1", actionSetEstimate, "uuid-1")); err != nil {
		t.Fatalf("set estimate: %v", err)
	}
	if !fl.setCalled || fl.setIssueID != "uuid-1" || fl.setEstimate != 5 {
		t.Errorf("linear set = %+v", fl)
	}
	if _, err := repo.GetSession(context.Background(), "uuid-1"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("session should be deleted after estimate set")
	}
	if fs.updates != 1 {
		t.Errorf("expected final message update, got %d", fs.updates)
	}
}

func TestSetEstimateWithNoNumericVotesKeepsSession(t *testing.T) {
	fl := &fakeLinear{}
	h, _, repo := newTestHandler(t, fl)
	seed(t, repo, &store.PokerSession{
		IssueID: "uuid-1", Status: store.StatusRevealed, ChannelID: "C1", MessageTS: "ts1",
		Votes: map[string]string{"U1": "?"},
	})

	if err := h.HandleInteraction(context.Background(), interaction("U1", actionSetEstimate, "uuid-1")); err != nil {
		t.Fatalf("set estimate: %v", err)
	}
	if fl.setCalled {
		t.Error("linear should not be called without numeric votes")
	}
	if _, err := repo.GetSession(context.Background(), "uuid-1"); err != nil {
		t.Error("session should be kept when nothing to estimate")
	}
}

func TestStaleInteractionIgnored(t *testing.T) {
	fl := &fakeLinear{}
	h, fs, _ := newTestHandler(t, fl)
	// No session seeded.
	if err := h.HandleInteraction(context.Background(), interaction("U1", actionReveal, "gone")); err != nil {
		t.Fatalf("stale interaction should be a no-op, got %v", err)
	}
	if fs.updates != 0 {
		t.Errorf("stale interaction should not update Slack")
	}
}

// --- helpers ---

func seed(t *testing.T, repo store.SessionRepository, sess *store.PokerSession) {
	t.Helper()
	if err := repo.SaveSession(context.Background(), sess); err != nil {
		t.Fatalf("seed session: %v", err)
	}
}

func interaction(userID, actionID, value string) slack.InteractionCallback {
	return slack.InteractionCallback{
		User: slack.User{ID: userID},
		ActionCallback: slack.ActionCallbacks{
			BlockActions: []*slack.BlockAction{
				{ActionID: actionID, Value: value},
			},
		},
	}
}
