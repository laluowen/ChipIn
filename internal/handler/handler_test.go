package handler

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/laluowen/ChipIn/internal/linear"
	"github.com/laluowen/ChipIn/internal/poker"
	"github.com/laluowen/ChipIn/internal/store"
	"github.com/slack-go/slack"
)

// fibScale is the extended Fibonacci scale used by seeded test sessions.
func fibScale() poker.Scale {
	s, err := poker.ScaleFor(poker.TypeFibonacci, true, false)
	if err != nil {
		panic(err)
	}
	return s
}

// --- fakes ---

type fakeSlack struct {
	postCh, postTS string
	posts          int
	updates        int
	lastUpdateTS   string
	nextTS         string
	lastPostText   string
	lastUpdateText string
	permalinks     int
	permalinkErr   error
	nextPermalink  string
}

// msgText extracts the rendered "text" param from Block Kit MsgOptions, so
// tests can assert on message content without a live Slack API.
func msgText(options ...slack.MsgOption) string {
	_, values, err := slack.UnsafeApplyMsgOptions("tok", "C", "https://slack.test/", options...)
	if err != nil {
		return ""
	}
	return values.Get("text")
}

func (f *fakeSlack) PostMessage(channelID string, options ...slack.MsgOption) (string, string, error) {
	f.posts++
	f.postCh = channelID
	f.lastPostText = msgText(options...)
	ts := f.nextTS
	if ts == "" {
		ts = "1700000000.000100"
	}
	f.postTS = ts
	return channelID, ts, nil
}

func (f *fakeSlack) UpdateMessage(channelID, timestamp string, options ...slack.MsgOption) (string, string, string, error) {
	f.updates++
	f.lastUpdateTS = timestamp
	f.lastUpdateText = msgText(options...)
	return channelID, timestamp, "", nil
}

func (f *fakeSlack) GetPermalink(params *slack.PermalinkParameters) (string, error) {
	f.permalinks++
	if f.permalinkErr != nil {
		return "", f.permalinkErr
	}
	if f.nextPermalink != "" {
		return f.nextPermalink, nil
	}
	return "https://workspace.slack.com/archives/" + params.Channel + "/p" + params.Ts, nil
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
	fl := &fakeLinear{issue: &linear.Issue{
		ID: "uuid-1", Identifier: "ENG-1", Title: "Do the thing",
		URL:            "https://linear.app/acme/issue/ENG-1/do-the-thing",
		EstimationType: poker.TypeFibonacci, EstimationExtended: true,
	}}
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
	// The scale from the Linear team is persisted on the session.
	if got := sess.Scale.Labels(); len(got) != 7 || got[len(got)-1] != "21" {
		t.Errorf("scale = %v, want extended fibonacci", got)
	}
	// The issue's Linear URL and the vote message's Slack permalink are both
	// captured so later renders/notices can link back to their source.
	if sess.IssueURL != "https://linear.app/acme/issue/ENG-1/do-the-thing" {
		t.Errorf("IssueURL = %q", sess.IssueURL)
	}
	if fs.permalinks != 1 {
		t.Errorf("expected one permalink lookup, got %d", fs.permalinks)
	}
	if sess.MessageLink != "https://workspace.slack.com/archives/C1/p1700000000.000200" {
		t.Errorf("MessageLink = %q", sess.MessageLink)
	}
	// The requester is captured for attribution in the message header.
	if sess.RequestedBy != "U1" {
		t.Errorf("RequestedBy = %q, want U1", sess.RequestedBy)
	}
}

func TestSlashCommandToleratesPermalinkFailure(t *testing.T) {
	fl := &fakeLinear{issue: &linear.Issue{
		ID: "uuid-1", Identifier: "ENG-1", Title: "x",
		EstimationType: poker.TypeFibonacci,
	}}
	h, fs, repo := newTestHandler(t, fl)
	fs.permalinkErr = errors.New("permalink lookup failed")

	cmd := slack.SlashCommand{ChannelID: "C1", UserID: "U1", Text: "ENG-1"}
	if err := h.HandleSlashCommand(context.Background(), cmd); err != nil {
		t.Fatalf("HandleSlashCommand should tolerate a permalink failure: %v", err)
	}
	sess, err := repo.GetSession(context.Background(), "uuid-1")
	if err != nil {
		t.Fatalf("session not saved: %v", err)
	}
	if sess.MessageLink != "" {
		t.Errorf("MessageLink = %q, want empty on permalink failure", sess.MessageLink)
	}
}

func TestSlashCommandEstimationDisabled(t *testing.T) {
	fl := &fakeLinear{issue: &linear.Issue{
		ID: "uuid-1", Identifier: "ENG-1", Title: "x", EstimationType: poker.TypeNotUsed,
	}}
	h, _, repo := newTestHandler(t, fl)

	cmd := slack.SlashCommand{ChannelID: "C1", UserID: "U1", Text: "ENG-1"}
	if err := h.HandleSlashCommand(context.Background(), cmd); err != nil {
		t.Fatalf("HandleSlashCommand: %v", err)
	}
	if _, err := repo.GetSession(context.Background(), "uuid-1"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("no session should be created when estimation is disabled")
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
		t.Errorf("expected one board update to ts1, got updates=%d ts=%q", fs.updates, fs.lastUpdateTS)
	}
	// The voter gets a private ephemeral confirmation of their pick.
	if fs.posts != 1 {
		t.Errorf("expected 1 private confirmation post, got %d", fs.posts)
	}
}

func TestPrivateNoticeLinksBackToVoteMessage(t *testing.T) {
	fl := &fakeLinear{}
	h, fs, repo := newTestHandler(t, fl)
	seed(t, repo, &store.PokerSession{
		IssueID: "uuid-1", Identifier: "ENG-1", Status: store.StatusVoting,
		ChannelID: "C1", MessageTS: "ts1", Votes: map[string]string{},
		MessageLink: "https://workspace.slack.com/archives/C1/p1700000000000100",
	})

	if err := h.HandleInteraction(context.Background(), interaction("U1", actionVotePrefix+"5", "uuid-1")); err != nil {
		t.Fatalf("vote: %v", err)
	}

	want := "<https://workspace.slack.com/archives/C1/p1700000000000100|ENG-1>"
	if !strings.Contains(fs.lastPostText, want) {
		t.Errorf("private notice text = %q, want it to contain %q", fs.lastPostText, want)
	}
}

func TestPrivateNoticeFallsBackToPlainIdentifierWithoutLink(t *testing.T) {
	fl := &fakeLinear{}
	h, fs, repo := newTestHandler(t, fl)
	seed(t, repo, &store.PokerSession{
		IssueID: "uuid-1", Identifier: "ENG-1", Status: store.StatusVoting,
		ChannelID: "C1", MessageTS: "ts1", Votes: map[string]string{},
		// No MessageLink set.
	})

	if err := h.HandleInteraction(context.Background(), interaction("U1", actionVotePrefix+"5", "uuid-1")); err != nil {
		t.Fatalf("vote: %v", err)
	}

	if !strings.Contains(fs.lastPostText, "*ENG-1*:") {
		t.Errorf("private notice text = %q, want a plain issue-key prefix", fs.lastPostText)
	}
	if strings.Contains(fs.lastPostText, "<|") {
		t.Errorf("private notice text = %q, should not render an empty-URL hyperlink", fs.lastPostText)
	}
}

func TestRetractRemovesOwnVote(t *testing.T) {
	fl := &fakeLinear{}
	h, fs, repo := newTestHandler(t, fl)
	seed(t, repo, &store.PokerSession{
		IssueID: "uuid-1", Status: store.StatusVoting, ChannelID: "C1", MessageTS: "ts1",
		Votes: map[string]string{"U1": "5", "U2": "8"},
	})

	if err := h.HandleInteraction(context.Background(), interaction("U1", actionRetract, "uuid-1")); err != nil {
		t.Fatalf("retract: %v", err)
	}

	sess, _ := repo.GetSession(context.Background(), "uuid-1")
	if _, still := sess.Votes["U1"]; still {
		t.Errorf("U1 vote should be removed, got %v", sess.Votes)
	}
	if sess.Votes["U2"] != "8" {
		t.Errorf("U2 vote should be untouched, got %v", sess.Votes)
	}
	if fs.updates != 1 {
		t.Errorf("expected the shared board to refresh once, got %d", fs.updates)
	}
	if fs.posts != 1 {
		t.Errorf("expected one private retract confirmation, got %d", fs.posts)
	}
}

func TestRetractWithNoVoteIsNoOp(t *testing.T) {
	fl := &fakeLinear{}
	h, fs, repo := newTestHandler(t, fl)
	seed(t, repo, &store.PokerSession{
		IssueID: "uuid-1", Status: store.StatusVoting, ChannelID: "C1", MessageTS: "ts1",
		Votes: map[string]string{"U2": "8"},
	})

	if err := h.HandleInteraction(context.Background(), interaction("U1", actionRetract, "uuid-1")); err != nil {
		t.Fatalf("retract: %v", err)
	}
	// The board is not refreshed when there was nothing to retract, but the
	// user still gets a private note.
	if fs.updates != 0 {
		t.Errorf("expected no board refresh, got %d", fs.updates)
	}
	if fs.posts != 1 {
		t.Errorf("expected one private note, got %d", fs.posts)
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
		Votes: map[string]string{"U1": "5", "U2": "5", "U3": "8"}, // mode 5
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

func TestSetEstimateHonorsDropdownOverride(t *testing.T) {
	fl := &fakeLinear{}
	h, _, repo := newTestHandler(t, fl)
	seed(t, repo, &store.PokerSession{
		IssueID: "uuid-1", Status: store.StatusRevealed, ChannelID: "C1", MessageTS: "ts1",
		Votes: map[string]string{"U1": "5", "U2": "5"}, // consensus would be 5
	})

	// The team discussed and picked 13 in the dropdown before setting.
	cb := interactionWithEstimate("U1", actionSetEstimate, "uuid-1", "13")
	if err := h.HandleInteraction(context.Background(), cb); err != nil {
		t.Fatalf("set estimate: %v", err)
	}
	if !fl.setCalled || fl.setEstimate != 13 {
		t.Errorf("expected override estimate 13, got %+v", fl)
	}
}

func TestSetEstimateWithNoVotesKeepsSession(t *testing.T) {
	fl := &fakeLinear{}
	h, _, repo := newTestHandler(t, fl)
	seed(t, repo, &store.PokerSession{
		IssueID: "uuid-1", Status: store.StatusRevealed, ChannelID: "C1", MessageTS: "ts1",
		Votes: map[string]string{},
	})

	if err := h.HandleInteraction(context.Background(), interaction("U1", actionSetEstimate, "uuid-1")); err != nil {
		t.Fatalf("set estimate: %v", err)
	}
	if fl.setCalled {
		t.Error("linear should not be called with no votes and no selection")
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
	if sess.Scale == nil {
		sess.Scale = fibScale()
	}
	if err := repo.SaveSession(context.Background(), sess); err != nil {
		t.Fatalf("seed session: %v", err)
	}
}

func interaction(userID, actionID, value string) slack.InteractionCallback {
	return slack.InteractionCallback{
		User:        slack.User{ID: userID},
		ResponseURL: "https://hooks.slack.test/response",
		ActionCallback: slack.ActionCallbacks{
			BlockActions: []*slack.BlockAction{
				{ActionID: actionID, Value: value},
			},
		},
	}
}

// interactionWithEstimate is like interaction but also carries the estimate
// dropdown's current selection in the block-action state, as Slack includes it
// when a button in the same message is pressed.
func interactionWithEstimate(userID, actionID, value, selectedLabel string) slack.InteractionCallback {
	cb := interaction(userID, actionID, value)
	cb.BlockActionState = &slack.BlockActionStates{
		Values: map[string]map[string]slack.BlockAction{
			blockEstimate: {
				actionEstimateSelect: {SelectedOption: slack.OptionBlockObject{Value: selectedLabel}},
			},
		},
	}
	return cb
}
