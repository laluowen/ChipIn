// Package handler contains the transport-agnostic planning-poker game logic. It
// reacts to Slack slash commands and interactive block actions, persists state
// via a store.SessionRepository, and syncs final estimates to Linear. The same
// handler serves both Socket Mode and (future) HTTP webhook transports.
package handler

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/laluowen/ChipIn/internal/linear"
	"github.com/laluowen/ChipIn/internal/poker"
	"github.com/laluowen/ChipIn/internal/store"
	"github.com/slack-go/slack"
)

// SlackAPI is the subset of *slack.Client the handler needs. Defining it here
// keeps the handler testable with fakes; *slack.Client satisfies it directly.
type SlackAPI interface {
	PostMessage(channelID string, options ...slack.MsgOption) (string, string, error)
	UpdateMessage(channelID, timestamp string, options ...slack.MsgOption) (string, string, string, error)
	GetPermalink(params *slack.PermalinkParameters) (string, error)
}

// LinearAPI is the subset of *linear.Client the handler needs.
type LinearAPI interface {
	FetchIssue(ctx context.Context, identifier string) (*linear.Issue, error)
	SetEstimate(ctx context.Context, issueID string, estimate float64) error
}

// Action and block IDs used on interactive elements. Vote buttons carry the
// chosen label as a suffix ("vote:5"); every button's Value carries the issue
// UUID so an interaction can be routed back to its session.
const (
	actionVotePrefix     = "vote:"
	actionReveal         = "reveal"
	actionContinue       = "continue"
	actionSetEstimate    = "set_estimate"
	actionCancel         = "cancel"
	actionRetract        = "retract"
	actionEstimateSelect = "estimate_select"
	blockEstimate        = "estimate"
)

// PokerHandler processes poker game logic independent of transport.
type PokerHandler struct {
	Slack  SlackAPI
	Linear LinearAPI
	Store  store.SessionRepository
}

// New builds a PokerHandler. The vote scale is derived per session from the
// Linear team's estimation settings, not configured here.
func New(s SlackAPI, l LinearAPI, repo store.SessionRepository) *PokerHandler {
	return &PokerHandler{Slack: s, Linear: l, Store: repo}
}

// HandleSlashCommand starts a new estimation round from `/chipin ENG-123`.
func (h *PokerHandler) HandleSlashCommand(ctx context.Context, cmd slack.SlashCommand) error {
	identifier := strings.TrimSpace(cmd.Text)
	if identifier == "" {
		return h.ephemeral(cmd, "Usage: `/chipin ENG-123`")
	}

	issue, err := h.Linear.FetchIssue(ctx, identifier)
	if err != nil {
		return h.ephemeral(cmd, fmt.Sprintf("Could not load %s: %v", identifier, err))
	}

	scale, err := poker.ScaleFor(issue.EstimationType, issue.EstimationExtended, issue.EstimationAllowZero)
	if err != nil {
		return h.ephemeral(cmd, fmt.Sprintf("Cannot start estimation for %s: %v", issue.Identifier, err))
	}

	sess := &store.PokerSession{
		IssueID:     issue.ID,
		Identifier:  issue.Identifier,
		Title:       issue.Title,
		IssueURL:    issue.URL,
		Status:      store.StatusVoting,
		ChannelID:   cmd.ChannelID,
		RequestedBy: cmd.UserID,
		Votes:       map[string]string{},
		Scale:       scale,
	}

	_, ts, err := h.Slack.PostMessage(cmd.ChannelID, slack.MsgOptionBlocks(h.votingBlocks(sess)...))
	if err != nil {
		return fmt.Errorf("post poker message: %w", err)
	}
	sess.MessageTS = ts

	// Best-effort: a permalink lets private vote notices compactly link back to
	// this message (as the hyperlinked issue key) instead of just a bare
	// mention. If it fails, notices just fall back to plain text.
	if link, err := h.Slack.GetPermalink(&slack.PermalinkParameters{Channel: cmd.ChannelID, Ts: ts}); err == nil {
		sess.MessageLink = link
	}

	if err := h.Store.SaveSession(ctx, sess); err != nil {
		return fmt.Errorf("save session: %w", err)
	}
	return nil
}

// HandleInteraction dispatches a block action button press.
func (h *PokerHandler) HandleInteraction(ctx context.Context, cb slack.InteractionCallback) error {
	actions := cb.ActionCallback.BlockActions
	if len(actions) == 0 {
		return nil
	}
	act := actions[0]
	issueID := act.Value
	userID := cb.User.ID

	sess, err := h.Store.GetSession(ctx, issueID)
	if errors.Is(err, store.ErrNotFound) {
		return nil // round already finished; ignore stale click
	}
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}

	switch {
	case strings.HasPrefix(act.ActionID, actionVotePrefix):
		label := strings.TrimPrefix(act.ActionID, actionVotePrefix)
		if !sess.Scale.Contains(label) {
			return nil
		}
		if sess.Votes == nil {
			sess.Votes = map[string]string{}
		}
		sess.Votes[userID] = label
		sess.Status = store.StatusVoting
		if err := h.saveAndRender(ctx, sess); err != nil {
			return err
		}
		return h.privateNotice(sess, userID,
			fmt.Sprintf("Your vote: *%s* — hidden until reveal. Pick another card to change it, or retract it.", label))

	case act.ActionID == actionRetract:
		if _, voted := sess.Votes[userID]; !voted {
			return h.privateNotice(sess, userID, "You have no vote to retract.")
		}
		delete(sess.Votes, userID)
		sess.Status = store.StatusVoting
		if err := h.saveAndRender(ctx, sess); err != nil {
			return err
		}
		return h.privateNotice(sess, userID, "Your vote was retracted.")

	case act.ActionID == actionReveal:
		sess.Status = store.StatusRevealed
		return h.saveAndRender(ctx, sess)

	case act.ActionID == actionContinue:
		sess.Status = store.StatusVoting
		return h.saveAndRender(ctx, sess)

	case act.ActionID == actionEstimateSelect:
		// Changing the estimate dropdown carries no issue UUID and needs no
		// state change; the chosen value is read from the payload when
		// "Set estimate" is pressed. Nothing to do here.
		return nil

	case act.ActionID == actionSetEstimate:
		point, ok := h.chosenEstimate(cb, sess)
		if !ok {
			// Nothing to estimate (no votes and no selection); leave it open.
			return h.render(ctx, sess)
		}
		return h.finalize(ctx, sess, point)

	case act.ActionID == actionCancel:
		return h.cancel(ctx, sess)
	}
	return nil
}

// chosenEstimate resolves the estimate to write: the value currently selected in
// the estimate dropdown if the user changed it, otherwise the computed
// consensus. ok is false when there is nothing to estimate.
func (h *PokerHandler) chosenEstimate(cb slack.InteractionCallback, sess *store.PokerSession) (poker.Point, bool) {
	if cb.BlockActionState != nil {
		if m, ok := cb.BlockActionState.Values[blockEstimate]; ok {
			if ba, ok := m[actionEstimateSelect]; ok && ba.SelectedOption.Value != "" {
				if p, ok := sess.Scale.Find(ba.SelectedOption.Value); ok {
					return p, true
				}
			}
		}
	}
	return sess.Scale.Consensus(sess.Votes)
}

// cancel abandons the round: it deletes the session and replaces the message
// with a cancelled state. No estimate is written to Linear.
func (h *PokerHandler) cancel(ctx context.Context, sess *store.PokerSession) error {
	if err := h.Store.DeleteSession(ctx, sess.IssueID); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	_, _, _, err := h.Slack.UpdateMessage(sess.ChannelID, sess.MessageTS,
		slack.MsgOptionBlocks(h.cancelBlocks(sess)...))
	if err != nil {
		return fmt.Errorf("update cancelled message: %w", err)
	}
	return nil
}

// finalize writes the chosen estimate to Linear, posts the final state, and
// deletes the session.
func (h *PokerHandler) finalize(ctx context.Context, sess *store.PokerSession, point poker.Point) error {
	if err := h.Linear.SetEstimate(ctx, sess.IssueID, point.Value); err != nil {
		return fmt.Errorf("set linear estimate: %w", err)
	}
	if err := h.Store.DeleteSession(ctx, sess.IssueID); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	_, _, _, err := h.Slack.UpdateMessage(sess.ChannelID, sess.MessageTS,
		slack.MsgOptionBlocks(h.finalBlocks(sess, point.Label)...))
	if err != nil {
		return fmt.Errorf("update final message: %w", err)
	}
	return nil
}

func (h *PokerHandler) saveAndRender(ctx context.Context, sess *store.PokerSession) error {
	if err := h.Store.SaveSession(ctx, sess); err != nil {
		return fmt.Errorf("save session: %w", err)
	}
	return h.render(ctx, sess)
}

// render refreshes the Slack message for the session's current status.
func (h *PokerHandler) render(ctx context.Context, sess *store.PokerSession) error {
	var blocks []slack.Block
	if sess.Status == store.StatusRevealed {
		blocks = h.summaryBlocks(sess)
	} else {
		blocks = h.votingBlocks(sess)
	}
	_, _, _, err := h.Slack.UpdateMessage(sess.ChannelID, sess.MessageTS, slack.MsgOptionBlocks(blocks...))
	if err != nil {
		return fmt.Errorf("update message: %w", err)
	}
	return nil
}

func (h *PokerHandler) ephemeral(cmd slack.SlashCommand, text string) error {
	_, _, err := h.Slack.PostMessage(cmd.ChannelID,
		slack.MsgOptionPostEphemeral(cmd.UserID),
		slack.MsgOptionText(text, false))
	return err
}

// privateNotice shows userID a note only they can see.
//
// This is a plain chat.postEphemeral on every call — Slack gives us no way to
// avoid that. A DM-based "one message, updated in place" approach was tried
// and reverted: it added a real message + conversations.open + a persisted
// per-user reference just to work around ephemeral's limitations, and that
// was more moving parts than the win was worth.
//
// The message is prefixed with the issue key, hyperlinked to the vote message
// when a permalink is on file. NOTE: chat.postEphemeral has no
// unfurl_links/unfurl_media parameters at all (unlike chat.postMessage), so
// there's no *documented, guaranteed* way to control unfurling here. We link
// anyway on the strength of: (a) chat.postEphemeral's own docs list no
// unfurl-related fields, and (b) Slack's classic-unfurl docs explicitly scope
// automatic unfurling to chat.postMessage and incoming webhooks, never
// mentioning postEphemeral. If that assumption turns out wrong in practice
// (an unfurled preview shows up), drop the hyperlink and fall back to a plain
// "*<identifier>*: " prefix — a one-line revert, see git history.
func (h *PokerHandler) privateNotice(sess *store.PokerSession, userID, body string) error {
	prefix := sess.Identifier
	if sess.MessageLink != "" {
		prefix = fmt.Sprintf("<%s|%s>", sess.MessageLink, sess.Identifier)
	}
	text := fmt.Sprintf("*%s*: %s", prefix, body)
	_, _, err := h.Slack.PostMessage(sess.ChannelID,
		slack.MsgOptionPostEphemeral(userID),
		slack.MsgOptionText(text, false))
	return err
}

// sortedVoters returns the userIDs that have voted, sorted for stable display.
func sortedVoters(votes map[string]string) []string {
	ids := make([]string, 0, len(votes))
	for id := range votes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
