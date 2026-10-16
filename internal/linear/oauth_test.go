package linear

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

const testClientSecret = "secret"

func TestStaticTokenSentAsIs(t *testing.T) {
	h, err := staticToken("personal-key").AuthHeader(context.Background())
	if err != nil {
		t.Fatalf("AuthHeader: %v", err)
	}
	if h != "personal-key" {
		t.Errorf("AuthHeader = %q, want the raw key with no Bearer prefix", h)
	}
}

func tokenServer(t *testing.T, expiresIn int) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if got := r.FormValue("grant_type"); got != "client_credentials" {
			t.Errorf("grant_type = %q", got)
		}
		if got := r.FormValue("scope"); got != "read,write" {
			t.Errorf("scope = %q, want read,write", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": fmt.Sprintf("token-%d", calls),
			"token_type":   "Bearer",
			"expires_in":   expiresIn,
			"scope":        "read write",
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestClientCredentialsFetchesAndCachesToken(t *testing.T) {
	srv, calls := tokenServer(t, 86399)
	ts := &ClientCredentialsTokenSource{ClientID: "id", ClientSecret: testClientSecret, Endpoint: srv.URL}

	h1, err := ts.AuthHeader(context.Background())
	if err != nil {
		t.Fatalf("AuthHeader: %v", err)
	}
	if h1 != "Bearer token-1" {
		t.Errorf("AuthHeader = %q", h1)
	}

	h2, err := ts.AuthHeader(context.Background())
	if err != nil {
		t.Fatalf("AuthHeader (cached): %v", err)
	}
	if h2 != h1 {
		t.Errorf("second AuthHeader = %q, want cached %q", h2, h1)
	}
	if got := atomic.LoadInt32(calls); got != 1 {
		t.Errorf("token endpoint called %d times, want 1 (second call should be cached)", got)
	}
}

func TestClientCredentialsRefetchesNearExpiry(t *testing.T) {
	// expires_in=1 is well within refreshBuffer, so every call should refetch.
	srv, calls := tokenServer(t, 1)
	ts := &ClientCredentialsTokenSource{ClientID: "id", ClientSecret: testClientSecret, Endpoint: srv.URL}

	if _, err := ts.AuthHeader(context.Background()); err != nil {
		t.Fatalf("AuthHeader: %v", err)
	}
	if _, err := ts.AuthHeader(context.Background()); err != nil {
		t.Fatalf("AuthHeader: %v", err)
	}
	if got := atomic.LoadInt32(calls); got != 2 {
		t.Errorf("token endpoint called %d times, want 2 (near-expiry token should refetch)", got)
	}
}

func TestClientCredentialsInvalidateForcesRefetch(t *testing.T) {
	srv, calls := tokenServer(t, 86399)
	ts := &ClientCredentialsTokenSource{ClientID: "id", ClientSecret: testClientSecret, Endpoint: srv.URL}

	if _, err := ts.AuthHeader(context.Background()); err != nil {
		t.Fatalf("AuthHeader: %v", err)
	}
	ts.Invalidate()
	if _, err := ts.AuthHeader(context.Background()); err != nil {
		t.Fatalf("AuthHeader after Invalidate: %v", err)
	}
	if got := atomic.LoadInt32(calls); got != 2 {
		t.Errorf("token endpoint called %d times, want 2 (Invalidate should force a refetch)", got)
	}
}

func TestClientCredentialsErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":             "invalid_client",
			"error_description": "client not found",
		})
	}))
	defer srv.Close()

	ts := &ClientCredentialsTokenSource{ClientID: "bad", ClientSecret: "bad", Endpoint: srv.URL}
	_, err := ts.AuthHeader(context.Background())
	if err == nil {
		t.Fatal("expected error for invalid client credentials")
	}
}

// TestClientRetriesOnceAfter401 exercises the full Client.do path: a 401 on
// the GraphQL endpoint should invalidate a refreshable TokenSource and retry
// exactly once, not loop or give up without trying.
func TestClientRetriesOnceAfter401(t *testing.T) {
	var graphqlCalls int32
	graphql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&graphqlCalls, 1)
		if n == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"issues":{"nodes":[]}}}`))
	}))
	defer graphql.Close()

	tokenSrv, tokenCalls := tokenServer(t, 86399)
	ts := &ClientCredentialsTokenSource{ClientID: "id", ClientSecret: testClientSecret, Endpoint: tokenSrv.URL}

	c := NewWithTokenSource(ts, WithEndpoint(graphql.URL))
	_, err := c.FetchIssue(context.Background(), testIdentifier)
	// "not found" is expected (empty nodes); what matters is it didn't fail
	// on the 401 itself.
	if err == nil || err.Error() == "" {
		t.Fatalf("FetchIssue: %v", err)
	}
	if got := atomic.LoadInt32(&graphqlCalls); got != 2 {
		t.Errorf("graphql endpoint called %d times, want 2 (one 401, one retry)", got)
	}
	if got := atomic.LoadInt32(tokenCalls); got != 2 {
		t.Errorf("token endpoint called %d times, want 2 (initial fetch + post-401 Invalidate refetch)", got)
	}
}

func TestClientCredentialsConcurrentCallsDoNotRace(t *testing.T) {
	srv, _ := tokenServer(t, 86399)
	ts := &ClientCredentialsTokenSource{ClientID: "id", ClientSecret: testClientSecret, Endpoint: srv.URL}

	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			if _, err := ts.AuthHeader(context.Background()); err != nil {
				t.Errorf("AuthHeader: %v", err)
			}
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}
}
