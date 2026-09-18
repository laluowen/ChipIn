package handler

import (
	"bytes"
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

// blocksContain reports whether the rendered JSON of blocks contains substr —
// a light way to assert on Block Kit content without a live Slack API.
func blocksContain(t *testing.T, blocks []slack.Block, substr string) bool {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // otherwise "<"/">" in our hyperlinks get \u003c-escaped
	if err := enc.Encode(blocks); err != nil {
		t.Fatalf("marshal blocks: %v", err)
	}
	return strings.Contains(buf.String(), substr)
}
