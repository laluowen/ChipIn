package handler

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/laluowen/ChipIn/internal/store"
	"github.com/slack-go/slack"
)

func TestHeaderLinksToLinearIssue(t *testing.T) {
	h := &PokerHandler{}
	sess := &store.PokerSession{
		Identifier: "ENG-1", Title: "Do the thing",
		IssueURL: "https://linear.app/acme/issue/ENG-1/do-the-thing",
		Scale:    fibScale(),
	}

	if !blocksContain(t, h.votingBlocks(sess), "<https://linear.app/acme/issue/ENG-1/do-the-thing|ENG-1>") {
		t.Errorf("expected header to hyperlink the issue identifier to Linear")
	}
}

func TestHeaderFallsBackWithoutIssueURL(t *testing.T) {
	h := &PokerHandler{}
	sess := &store.PokerSession{Identifier: "ENG-1", Title: "Do the thing", Scale: fibScale()}

	if !blocksContain(t, h.votingBlocks(sess), "ENG-1") {
		t.Errorf("expected the plain identifier to still appear")
	}
	if blocksContain(t, h.votingBlocks(sess), "<|ENG-1>") {
		t.Errorf("should not render an empty-URL hyperlink when IssueURL is unset")
	}
}

func TestHeaderAttributesRequester(t *testing.T) {
	h := &PokerHandler{}
	sess := &store.PokerSession{
		Identifier: "ENG-1", Title: "Do the thing", RequestedBy: "U123", Scale: fibScale(),
	}

	if !blocksContain(t, h.votingBlocks(sess), "Started by <@U123>") {
		t.Errorf("expected header to attribute the round to its requester")
	}
}

func TestHeaderOmitsAttributionWithoutRequester(t *testing.T) {
	h := &PokerHandler{}
	sess := &store.PokerSession{Identifier: "ENG-1", Title: "Do the thing", Scale: fibScale()}

	if blocksContain(t, h.votingBlocks(sess), "Started by") {
		t.Errorf("should not render an attribution line when RequestedBy is unset")
	}
}

// blocksContain reports whether any text value in the rendered blocks
// contains substr — a light way to assert on Block Kit content without a
// live Slack API. It marshals then unmarshals into generic values rather than
// substring-matching the raw JSON, because some slack-go types (e.g.
// ContextElements) have a MarshalJSON that always HTML-escapes "<"/">"
// regardless of the encoder used; that's harmless on the wire (JSON decodes
// \u003c back to a literal "<" losslessly) but would false-negative a raw
// string search.
func blocksContain(t *testing.T, blocks []slack.Block, substr string) bool {
	t.Helper()
	raw, err := json.Marshal(blocks)
	if err != nil {
		t.Fatalf("marshal blocks: %v", err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal blocks: %v", err)
	}
	return containsString(decoded, substr)
}

// containsString recursively searches decoded JSON (maps/slices/strings) for substr.
func containsString(v any, substr string) bool {
	switch x := v.(type) {
	case string:
		return strings.Contains(x, substr)
	case []any:
		for _, e := range x {
			if containsString(e, substr) {
				return true
			}
		}
	case map[string]any:
		for _, e := range x {
			if containsString(e, substr) {
				return true
			}
		}
	}
	return false
}
