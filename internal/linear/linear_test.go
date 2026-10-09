package linear

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

const (
	testTeamKey    = "ENG"
	testIssueUUID  = "uuid-9"
	testIdentifier = "ENG-123"
)

func TestParseIdentifier(t *testing.T) {
	ok := []struct {
		in       string
		wantTeam string
		wantNum  float64
	}{
		{testIdentifier, testTeamKey, 123},
		{"eng-1", testTeamKey, 1},
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

	bad := []string{"", testTeamKey, "ENG-", "-123", "ENG-abc", "123"}
	for _, in := range bad {
		if _, _, err := parseIdentifier(in); err == nil {
			t.Errorf("parseIdentifier(%q) expected error, got nil", in)
		}
	}
}

func TestExtractIdentifierFromURL(t *testing.T) {
	cases := map[string]string{
		"https://linear.app/lalu-uk/issue/LALU-321":                                              "LALU-321",
		"https://linear.app/lalu-uk/issue/LALU-321/finish-the-mobile-login-demo-and-local-setup": "LALU-321",
		"linear.app/acme/issue/ENG-7":                                                            "ENG-7",
		"  https://linear.app/lalu-uk/issue/LALU-1  ":                                            "LALU-1",
		testIdentifier: testIdentifier, // bare passes through
		"not a url":    "not a url",
	}
	for in, want := range cases {
		if got := extractIdentifier(in); got != want {
			t.Errorf("extractIdentifier(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseIdentifierAcceptsURL(t *testing.T) {
	team, num, err := parseIdentifier("https://linear.app/lalu-uk/issue/LALU-321/some-slug")
	if err != nil {
		t.Fatalf("parseIdentifier(url): %v", err)
	}
	if team != "LALU" || num != 321 {
		t.Errorf("got %q,%v want LALU,321", team, num)
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
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Variables["team"] != testTeamKey {
			t.Errorf("team var = %v, want ENG", req.Variables["team"])
		}
		resp := map[string]any{
			"data": map[string]any{
				"issues": map[string]any{
					"nodes": []map[string]any{
						{
							"id": testIssueUUID, "identifier": testIdentifier, "title": "Speed up", "estimate": est,
							"url": "https://linear.app/acme/issue/ENG-123/speed-up",
							"team": map[string]any{
								"issueEstimationType":      "fibonacci",
								"issueEstimationExtended":  true,
								"issueEstimationAllowZero": false,
							},
						},
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := New("test-token", WithEndpoint(srv.URL))
	issue, err := c.FetchIssue(context.Background(), testIdentifier)
	if err != nil {
		t.Fatalf("FetchIssue: %v", err)
	}
	if issue.ID != testIssueUUID || issue.Title != "Speed up" {
		t.Errorf("issue = %+v", issue)
	}
	if issue.Estimate == nil || *issue.Estimate != 3.0 {
		t.Errorf("estimate = %v, want 3", issue.Estimate)
	}
	if issue.URL != "https://linear.app/acme/issue/ENG-123/speed-up" {
		t.Errorf("URL = %q", issue.URL)
	}
	if issue.EstimationType != "fibonacci" || !issue.EstimationExtended || issue.EstimationAllowZero {
		t.Errorf("estimation settings = %+v", issue)
	}
}

func TestFetchIssueNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":{"issues":{"nodes":[]}}}`)
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
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Variables["id"] != testIssueUUID {
			t.Errorf("id var = %v", req.Variables["id"])
		}
		// JSON numbers decode to float64.
		if req.Variables["estimate"].(float64) != 5 {
			t.Errorf("estimate var = %v, want 5", req.Variables["estimate"])
		}
		_, _ = io.WriteString(w, `{"data":{"issueUpdate":{"success":true}}}`)
	}))
	defer srv.Close()

	c := New("t", WithEndpoint(srv.URL))
	if err := c.SetEstimate(context.Background(), testIssueUUID, 5); err != nil {
		t.Fatalf("SetEstimate: %v", err)
	}
}

func TestGraphQLErrorSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"errors":[{"message":"boom"}]}`)
	}))
	defer srv.Close()

	c := New("t", WithEndpoint(srv.URL))
	err := c.SetEstimate(context.Background(), "x", 1)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
