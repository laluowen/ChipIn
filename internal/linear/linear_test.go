package linear

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseIdentifier(t *testing.T) {
	ok := []struct {
		in       string
		wantTeam string
		wantNum  float64
	}{
		{"ENG-123", "ENG", 123},
		{"eng-1", "ENG", 1},
		{"  ABC-42 ", "ABC", 42},
		{"MULTI-WORD-9", "MULTI-WORD", 9},
	}
	for _, tc := range ok {
		team, num, err := parseIdentifier(tc.in)
		if err != nil {
			t.Errorf("parseIdentifier(%q) unexpected err: %v", tc.in, err)
			continue
		}
		if team != tc.wantTeam || num != tc.wantNum {
			t.Errorf("parseIdentifier(%q) = %q,%v want %q,%v", tc.in, team, num, tc.wantTeam, tc.wantNum)
		}
	}

	bad := []string{"", "ENG", "ENG-", "-123", "ENG-abc", "123"}
	for _, in := range bad {
		if _, _, err := parseIdentifier(in); err == nil {
			t.Errorf("parseIdentifier(%q) expected error, got nil", in)
		}
	}
}

func TestFetchIssue(t *testing.T) {
	est := 3.0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "test-token" {
			t.Errorf("Authorization = %q, want test-token", got)
		}
		var req struct {
			Variables map[string]any `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if req.Variables["team"] != "ENG" {
			t.Errorf("team var = %v, want ENG", req.Variables["team"])
		}
		resp := map[string]any{
			"data": map[string]any{
				"issues": map[string]any{
					"nodes": []map[string]any{
						{"id": "uuid-9", "identifier": "ENG-123", "title": "Speed up", "estimate": est},
					},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := New("test-token", WithEndpoint(srv.URL))
	issue, err := c.FetchIssue(context.Background(), "ENG-123")
	if err != nil {
		t.Fatalf("FetchIssue: %v", err)
	}
	if issue.ID != "uuid-9" || issue.Title != "Speed up" {
		t.Errorf("issue = %+v", issue)
	}
	if issue.Estimate == nil || *issue.Estimate != 3.0 {
		t.Errorf("estimate = %v, want 3", issue.Estimate)
	}
}

func TestFetchIssueNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":{"issues":{"nodes":[]}}}`)
	}))
	defer srv.Close()

	c := New("t", WithEndpoint(srv.URL))
	if _, err := c.FetchIssue(context.Background(), "ENG-999"); err == nil {
		t.Fatal("expected not-found error, got nil")
	}
}

func TestSetEstimate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Variables map[string]any `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if req.Variables["id"] != "uuid-9" {
			t.Errorf("id var = %v", req.Variables["id"])
		}
		// JSON numbers decode to float64.
		if req.Variables["estimate"].(float64) != 5 {
			t.Errorf("estimate var = %v, want 5", req.Variables["estimate"])
		}
		io.WriteString(w, `{"data":{"issueUpdate":{"success":true}}}`)
	}))
	defer srv.Close()

	c := New("t", WithEndpoint(srv.URL))
	if err := c.SetEstimate(context.Background(), "uuid-9", 5); err != nil {
		t.Fatalf("SetEstimate: %v", err)
	}
}

func TestGraphQLErrorSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"errors":[{"message":"boom"}]}`)
	}))
	defer srv.Close()

	c := New("t", WithEndpoint(srv.URL))
	err := c.SetEstimate(context.Background(), "x", 1)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
