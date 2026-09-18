package handler

import (
	"fmt"
	"strings"

	"github.com/laluowen/ChipIn/internal/store"
	"github.com/slack-go/slack"
)

// plainText is a shorthand for a plain_text block object with emoji enabled.
func plainText(s string) *slack.TextBlockObject {
	return slack.NewTextBlockObject(slack.PlainTextType, s, true, false)
}

// markdown is a shorthand for an mrkdwn block object.
func markdown(s string) *slack.TextBlockObject {
	return slack.NewTextBlockObject(slack.MarkdownType, s, false, false)
}

// headerBlocks are the title/context lines common to every render. The issue
// identifier links back to Linear when a URL is known, so the round always
// carries a path back to the source issue. It also attributes the round to
// whoever started it via /chipin.
func (h *PokerHandler) headerBlocks(sess *store.PokerSession) []slack.Block {
	id := sess.Identifier
	if sess.IssueURL != "" {
		id = fmt.Sprintf("<%s|%s>", sess.IssueURL, sess.Identifier)
	}
	title := fmt.Sprintf("%s — %s", id, sess.Title)
	blocks := []slack.Block{
		slack.NewHeaderBlock(plainText("Planning Poker")),
		slack.NewSectionBlock(markdown("*"+title+"*"), nil, nil),
	}
	if sess.RequestedBy != "" {
		blocks = append(blocks, slack.NewContextBlock("",
			markdown(fmt.Sprintf("Started by <@%s>", sess.RequestedBy))))
	}
	return blocks
}

// votingBlocks renders the vote-in-progress UI: hidden tallies, one button per
// scale value, plus a Reveal button.
func (h *PokerHandler) votingBlocks(sess *store.PokerSession) []slack.Block {
	blocks := h.headerBlocks(sess)

	voters := sortedVoters(sess.Votes)
	var status string
	if len(voters) == 0 {
		status = "_No votes yet. Pick a card below._"
	} else {
		mentions := make([]string, len(voters))
		for i, id := range voters {
			mentions[i] = "<@" + id + ">"
		}
		status = fmt.Sprintf("*%d voted:* %s", len(voters), joinMentions(mentions))
	}
	blocks = append(blocks, slack.NewContextBlock("", markdown(status)))

	// One vote button per scale point, chunked into rows of five.
	var elems []slack.BlockElement
	for _, p := range sess.Scale {
		btn := slack.NewButtonBlockElement(actionVotePrefix+p.Label, sess.IssueID, plainText(p.Label))
		elems = append(elems, btn)
	}
	blocks = append(blocks, chunkActions(elems, 5)...)

	reveal := slack.NewButtonBlockElement(actionReveal, sess.IssueID, plainText("Reveal votes"))
	reveal.Style = slack.StylePrimary
	retract := slack.NewButtonBlockElement(actionRetract, sess.IssueID, plainText("Retract my vote"))
	cancel := slack.NewButtonBlockElement(actionCancel, sess.IssueID, plainText("Cancel"))
	cancel.Style = slack.StyleDanger
	blocks = append(blocks, slack.NewActionBlock("controls", reveal, retract, cancel))

	return blocks
}

// summaryBlocks renders the revealed state: every vote shown, the recommended
// consensus, and Continue / Set-estimate buttons.
func (h *PokerHandler) summaryBlocks(sess *store.PokerSession) []slack.Block {
	blocks := h.headerBlocks(sess)

	voters := sortedVoters(sess.Votes)
	if len(voters) == 0 {
		blocks = append(blocks, slack.NewSectionBlock(markdown("_No votes were cast._"), nil, nil))
	} else {
		lines := make([]string, len(voters))
		for i, id := range voters {
			lines[i] = fmt.Sprintf("• <@%s>: *%s*", id, sess.Votes[id])
		}
		blocks = append(blocks, slack.NewSectionBlock(markdown(joinLines(lines)), nil, nil))
	}

	consensus, ok := sess.Scale.Consensus(sess.Votes)
	if ok {
		blocks = append(blocks, slack.NewContextBlock("",
			markdown(fmt.Sprintf("Recommended estimate: *%s* — adjust below if you like.", consensus.Label))))
	} else {
		blocks = append(blocks, slack.NewContextBlock("",
			markdown("_No votes cast — pick an estimate below to set one anyway._")))
	}

	// Estimate dropdown, prefilled to the recommendation (or the first point
	// when there were no votes). The picker lets the team override the computed
	// value after discussion; whatever is selected here is what "Set estimate"
	// writes to Linear.
	initial := consensus
	if !ok && len(sess.Scale) > 0 {
		initial = sess.Scale[0]
	}
	if len(sess.Scale) > 0 {
		opts := make([]*slack.OptionBlockObject, 0, len(sess.Scale))
		var initialOpt *slack.OptionBlockObject
		for _, p := range sess.Scale {
			o := slack.NewOptionBlockObject(p.Label, plainText(p.Label), nil)
			opts = append(opts, o)
			if p.Label == initial.Label {
				initialOpt = o
			}
		}
		sel := slack.NewOptionsSelectBlockElement(slack.OptTypeStatic,
			plainText("Choose estimate"), actionEstimateSelect, opts...)
		if initialOpt != nil {
			sel = sel.WithInitialOption(initialOpt)
		}
		blocks = append(blocks, slack.NewActionBlock(blockEstimate, sel))
	}

	cont := slack.NewButtonBlockElement(actionContinue, sess.IssueID, plainText("Continue voting"))
	set := slack.NewButtonBlockElement(actionSetEstimate, sess.IssueID, plainText("Set estimate"))
	set.Style = slack.StylePrimary
	cancel := slack.NewButtonBlockElement(actionCancel, sess.IssueID, plainText("Cancel"))
	cancel.Style = slack.StyleDanger
	blocks = append(blocks, slack.NewActionBlock("controls", cont, set, cancel))

	return blocks
}

// cancelBlocks renders the abandoned state after a round is cancelled.
func (h *PokerHandler) cancelBlocks(sess *store.PokerSession) []slack.Block {
	blocks := h.headerBlocks(sess)
	blocks = append(blocks, slack.NewSectionBlock(
		markdown(":x: Estimation cancelled. No estimate was saved."), nil, nil))
	return blocks
}

// finalBlocks renders the locked-in state after an estimate is written.
func (h *PokerHandler) finalBlocks(sess *store.PokerSession, label string) []slack.Block {
	blocks := h.headerBlocks(sess)
	blocks = append(blocks, slack.NewSectionBlock(
		markdown(fmt.Sprintf(":white_check_mark: Estimate *%s* saved to Linear.", label)), nil, nil))
	return blocks
}

// chunkActions splits button elements into action blocks of at most size each.
func chunkActions(elems []slack.BlockElement, size int) []slack.Block {
	var out []slack.Block
	for i := 0; i < len(elems); i += size {
		end := i + size
		if end > len(elems) {
			end = len(elems)
		}
		out = append(out, slack.NewActionBlock("", elems[i:end]...))
	}
	return out
}

func joinMentions(m []string) string { return strings.Join(m, "  ") }
func joinLines(l []string) string    { return strings.Join(l, "\n") }
