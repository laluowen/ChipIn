// Package webhook adapts inbound Slack HTTP requests (slash commands and
// interactive block actions) onto the transport-agnostic
// handler.PokerHandler, for Mode B (Cloud Run / serverless) deployments. It
// is the HTTP counterpart to internal/socket's Socket Mode receive loop.
//
// Every request is verified against the Slack signing secret
// (slack.NewSecretsVerifier) before being parsed, then handled
// synchronously: the handler runs to completion and only then does the
// handler respond, same as internal/socket. Slack requires an ack within 3s;
// on Cloud Run a cold start can eat into that budget. If timeouts become a
// problem in practice, set min-instances=1 to keep an instance warm, or
// consider deferring work via Cloud Tasks (see PLAN.md).
package webhook

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"

	"github.com/laluowen/ChipIn/internal/handler"
	"github.com/slack-go/slack"
)

// Server handles inbound Slack webhook requests.
type Server struct {
	handler       *handler.PokerHandler
	signingSecret string
	log           *log.Logger
}

// New builds a Server. signingSecret is the Slack app's signing secret, used
// to verify every inbound request came from Slack.
func New(h *handler.PokerHandler, signingSecret string, logger *log.Logger) *Server {
	return &Server{handler: h, signingSecret: signingSecret, log: logger}
}

// Routes returns the HTTP handler to serve. Mount it directly, e.g.
// http.ListenAndServe(":"+port, srv.Routes()).
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/slack/commands", s.handleCommand)
	mux.HandleFunc("/slack/interactions", s.handleInteraction)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

// handleCommand processes `/chipin ENG-123`.
func (s *Server) handleCommand(w http.ResponseWriter, r *http.Request) {
	body, ok := s.verified(w, r)
	if !ok {
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	cmd, err := slack.SlashCommandParse(r)
	if err != nil {
		s.log.Printf("parse slash command: %v", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if err := s.handler.HandleSlashCommand(r.Context(), cmd); err != nil {
		s.log.Printf("slash command error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// handleInteraction processes block action button presses (vote, reveal,
// set estimate, etc). The payload rides as a JSON string in the "payload"
// form field, per Slack's interactivity request format.
func (s *Server) handleInteraction(w http.ResponseWriter, r *http.Request) {
	body, ok := s.verified(w, r)
	if !ok {
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	if err := r.ParseForm(); err != nil {
		s.log.Printf("parse interaction form: %v", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	var cb slack.InteractionCallback
	if err := json.Unmarshal([]byte(r.FormValue("payload")), &cb); err != nil {
		s.log.Printf("decode interaction payload: %v", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if err := s.handler.HandleInteraction(r.Context(), cb); err != nil {
		s.log.Printf("interaction error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// verified reads the request body and checks it against Slack's signature
// headers. On failure it writes the error response itself and returns
// ok=false; callers should return immediately in that case.
func (s *Server) verified(w http.ResponseWriter, r *http.Request) (body []byte, ok bool) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return nil, false
	}
	if err := verifySignature(r.Header, s.signingSecret, body); err != nil {
		s.log.Printf("signature verification failed: %v", err)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return nil, false
	}
	return body, true
}

func verifySignature(header http.Header, signingSecret string, body []byte) error {
	verifier, err := slack.NewSecretsVerifier(header, signingSecret)
	if err != nil {
		return err
	}
	if _, err := verifier.Write(body); err != nil {
		return err
	}
	if err := verifier.Ensure(); err != nil {
		return errors.New("signature mismatch")
	}
	return nil
}
