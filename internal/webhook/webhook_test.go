package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/laluowen/ChipIn/internal/handler"
	"github.com/laluowen/ChipIn/internal/linear"
	"github.com/laluowen/ChipIn/internal/store"
	"github.com/slack-go/slack"
)

const testSigningSecret = "test-signing-secret"

// --- fakes (mirrors internal/handler's test fakes; kept local since those
// aren't exported) ---

type fakeSlack struct{}

func (fakeSlack) PostMessage(channelID string, _ ...slack.MsgOption) (string, string, error) {
	return channelID, "1700000000.000100", nil
}

func (fakeSlack) UpdateMessage(channelID, ts string, _ ...slack.MsgOption) (string, string, string, error) {
	return channelID, ts, "", nil
}

func (fakeSlack) GetPermalink(params *slack.PermalinkParameters) (string, error) {
	return "https://workspace.slack.com/archives/" + params.Channel + "/p" + params.Ts, nil
}

type fakeLinear struct{}

func (fakeLinear) FetchIssue(_ context.Context, identifier string) (*linear.Issue, error) {
	return &linear.Issue{
		ID: "uuid-1", Identifier: identifier, Title: "Do the thing",
		URL: "https://linear.app/acme/issue/uuid-1", EstimationType: "fibonacci",
	}, nil
}

func (fakeLinear) SetEstimate(_ context.Context, _ string, _ float64) error { return nil }

func newTestServer(t *testing.T) *Server {
	t.Helper()
	repo, err := store.NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	h := handler.New(fakeSlack{}, fakeLinear{}, repo)
	logger := log.New(testWriter{t}, "", 0)
	return New(h, testSigningSecret, logger)
}

// testWriter routes the server's internal log lines to t.Log so failures
// show context without polluting normal test output.
type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

// sign computes the valid Slack signature headers for body, per Slack's
// signing-secret scheme: v0=HMAC-SHA256("v0:"+timestamp+":"+body, secret).
func sign(secret string, body []byte) http.Header {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "v0:%s:", ts)
	mac.Write(body)
	h := http.Header{}
	h.Set("X-Slack-Request-Timestamp", ts)
	h.Set("X-Slack-Signature", "v0="+hex.EncodeToString(mac.Sum(nil)))
	return h
}

func newSignedRequest(t *testing.T, method, path string, form url.Values) *http.Request {
	t.Helper()
	body := []byte(form.Encode())
	req := httptest.NewRequest(method, path, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range sign(testSigningSecret, body) {
		req.Header[k] = v
	}
	return req
}

func TestHealthz(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestHandleCommandRejectsBadSignature(t *testing.T) {
	srv := newTestServer(t)
	form := url.Values{"command": {"/chipin"}, "text": {"ENG-1"}, "channel_id": {"C1"}, "user_id": {"U1"}}
	req := httptest.NewRequest(http.MethodPost, "/slack/commands", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Slack-Request-Timestamp", strconv.FormatInt(time.Now().Unix(), 10))
	req.Header.Set("X-Slack-Signature", "v0=deadbeef")

	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestHandleCommandAcceptsValidSignature(t *testing.T) {
	srv := newTestServer(t)
	form := url.Values{"command": {"/chipin"}, "text": {"ENG-1"}, "channel_id": {"C1"}, "user_id": {"U1"}}
	req := newSignedRequest(t, http.MethodPost, "/slack/commands", form)

	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleInteractionAcceptsValidSignature(t *testing.T) {
	srv := newTestServer(t)
	payload := `{"type":"block_actions","user":{"id":"U1"},"actions":[{"action_id":"estimate_select","value":""}]}`
	form := url.Values{"payload": {payload}}
	req := newSignedRequest(t, http.MethodPost, "/slack/interactions", form)

	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
}
