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
}

// LinearAPI is the subset of *linear.Client the handler needs.
type LinearAPI interface {
	FetchIssue(ctx context.Context, identifier string) (*linear.Issue, error)
	SetEstimate(ctx context.Context, issueID string, estimate float64) error
}

// Action IDs used on interactive buttons. Vote buttons carry the chosen label
// as a suffix ("vote:5"); every button's Value carries the issue UUID so an
// interaction can be routed back to its session.
const (
	actionVotePrefix  = "vote:"
	actionReveal      = "reveal"
	actionContinue    = "continue"
	actionSetEstimate = "set_estimate"
	actionCancel      = "cancel"
	actionRetract     = "retract"
)

// PokerHandler processes poker game logic independent of transport.
type PokerHandler struct {
	Slack  SlackAPI
	Linear LinearAPI
	Store  store.SessionRepository
	Scale  poker.Scale
}

// New builds a PokerHandler with the Fibonacci scale.
func New(s SlackAPI, l LinearAPI, repo store.SessionRepository) *PokerHandler {
	return &PokerHandler{Slack: s, Linear: l, Store: repo, Scale: poker.Fibonacci}
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

	sess := &store.PokerSession{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Title:      issue.Title,
		Status:     store.StatusVoting,
		ChannelID:  cmd.ChannelID,
		Votes:      map[string]string{},
	}

	_, ts, err := h.Slack.PostMessage(cmd.ChannelID, slack.MsgOptionBlocks(h.votingBlocks(sess)...))
	if err != nil {
		return fmt.Errorf("post poker message: %w", err)
	}
	sess.MessageTS = ts

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
	responseURL := cb.ResponseURL

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
		if !h.Scale.Contains(label) {
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
		return h.privateNotice(responseURL,
			fmt.Sprintf("Your vote: *%s* — hidden until reveal. Pick another card to change it, or retract it.", label))

	case act.ActionID == actionRetract:
		if _, voted := sess.Votes[userID]; !voted {
			return h.privateNotice(responseURL, "You have no vote to retract.")
		}
		delete(sess.Votes, userID)
		sess.Status = store.StatusVoting
		if err := h.saveAndRender(ctx, sess); err != nil {
			return err
		}
		return h.privateNotice(responseURL, "Your vote was retracted.")

	case act.ActionID == actionReveal:
		sess.Status = store.StatusRevealed
		return h.saveAndRender(ctx, sess)

	case act.ActionID == actionContinue:
		sess.Status = store.StatusVoting
		return h.saveAndRender(ctx, sess)

	case act.ActionID == actionSetEstimate:
		return h.finalize(ctx, sess)

	case act.ActionID == actionCancel:
		return h.cancel(ctx, sess)
	}
	return nil
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

// finalize writes the consensus estimate to Linear, posts the final state, and
// deletes the session.
func (h *PokerHandler) finalize(ctx context.Context, sess *store.PokerSession) error {
	label, value, ok := h.Scale.Consensus(sess.Votes)
	if !ok {
		// No numeric votes to set; keep the round open and tell the channel.
		return h.render(ctx, sess)
	}
	if err := h.Linear.SetEstimate(ctx, sess.IssueID, value); err != nil {
		return fmt.Errorf("set linear estimate: %w", err)
	}
	if err := h.Store.DeleteSession(ctx, sess.IssueID); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	_, _, _, err := h.Slack.UpdateMessage(sess.ChannelID, sess.MessageTS,
		slack.MsgOptionBlocks(h.finalBlocks(sess, label)...))
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

// privateNotice shows a note visible only to the acting user by responding to
// the interaction's response_url as an ephemeral message. Unlike a fresh
// chat.postEphemeral on every click, responding through the response_url of the
// same source message lets Slack refresh the user's single "only visible to
// you" note in place instead of stacking a new one each vote.
func (h *PokerHandler) privateNotice(responseURL, text string) error {
	if responseURL == "" {
		return nil // e.g. Socket Mode payloads without a response_url; nothing to do
	}
	_, _, err := h.Slack.PostMessage("",
		slack.MsgOptionResponseURL(responseURL, slack.ResponseTypeEphemeral),
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
