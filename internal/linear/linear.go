// Package linear is a minimal client for the Linear GraphQL API covering the
// operations chipin needs: looking up an issue by its human identifier and
// updating its estimate. It uses net/http directly rather than a GraphQL SDK.
package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DefaultEndpoint is Linear's GraphQL endpoint.
const DefaultEndpoint = "https://api.linear.app/graphql"

// Client talks to the Linear GraphQL API.
type Client struct {
	token    string
	endpoint string
	http     *http.Client
}

// Option customises a Client.
type Option func(*Client)

// WithEndpoint overrides the GraphQL endpoint (useful in tests).
func WithEndpoint(url string) Option {
	return func(c *Client) { c.endpoint = url }
}

// WithHTTPClient sets the underlying HTTP client.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.http = h }
}

// New returns a Client authenticated with the given token. A personal API key
// is sent as-is in the Authorization header, which is what Linear expects for
// personal keys (OAuth access tokens use the "Bearer " prefix; see PLAN.md).
func New(token string, opts ...Option) *Client {
	c := &Client{
		token:    token,
		endpoint: DefaultEndpoint,
		http:     &http.Client{Timeout: 15 * time.Second},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Issue is the subset of a Linear issue chipin cares about.
type Issue struct {
	ID         string // UUID
	Identifier string // e.g. "ENG-123"
	Title      string
	Estimate   *float64 // nil when unestimated
}

// FetchIssue looks up an issue by its human identifier (e.g. "ENG-123").
func (c *Client) FetchIssue(ctx context.Context, identifier string) (*Issue, error) {
	teamKey, number, err := parseIdentifier(identifier)
	if err != nil {
		return nil, err
	}

	const query = `query($team:String!,$number:Float!){
		issues(filter:{team:{key:{eq:$team}},number:{eq:$number}}, first:1){
			nodes{ id identifier title estimate }
		}
	}`
	vars := map[string]any{"team": teamKey, "number": number}

	var resp struct {
		Issues struct {
			Nodes []struct {
				ID         string   `json:"id"`
				Identifier string   `json:"identifier"`
				Title      string   `json:"title"`
				Estimate   *float64 `json:"estimate"`
			} `json:"nodes"`
		} `json:"issues"`
	}
	if err := c.do(ctx, query, vars, &resp); err != nil {
		return nil, err
	}
	if len(resp.Issues.Nodes) == 0 {
		return nil, fmt.Errorf("issue %q not found", identifier)
	}
	n := resp.Issues.Nodes[0]
	return &Issue{ID: n.ID, Identifier: n.Identifier, Title: n.Title, Estimate: n.Estimate}, nil
}

// SetEstimate updates the estimate of the issue with the given UUID.
func (c *Client) SetEstimate(ctx context.Context, issueID string, estimate float64) error {
	const mutation = `mutation($id:String!,$estimate:Int!){
		issueUpdate(id:$id, input:{estimate:$estimate}){ success }
	}`
	vars := map[string]any{"id": issueID, "estimate": int(estimate)}

	var resp struct {
		IssueUpdate struct {
			Success bool `json:"success"`
		} `json:"issueUpdate"`
	}
	if err := c.do(ctx, mutation, vars, &resp); err != nil {
		return err
	}
	if !resp.IssueUpdate.Success {
		return fmt.Errorf("linear reported estimate update unsuccessful for %s", issueID)
	}
	return nil
}

// parseIdentifier splits "ENG-123" into team key and issue number.
func parseIdentifier(identifier string) (teamKey string, number float64, err error) {
	identifier = strings.TrimSpace(identifier)
	i := strings.LastIndex(identifier, "-")
	if i <= 0 || i == len(identifier)-1 {
		return "", 0, fmt.Errorf("invalid issue identifier %q, expected form TEAM-123", identifier)
	}
	teamKey = strings.ToUpper(identifier[:i])
	n, convErr := strconv.Atoi(identifier[i+1:])
	if convErr != nil {
		return "", 0, fmt.Errorf("invalid issue number in %q: %w", identifier, convErr)
	}
	return teamKey, float64(n), nil
}

// do executes a GraphQL request and unmarshals the "data" object into out.
func (c *Client) do(ctx context.Context, query string, vars map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", c.token)

	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("linear request: %w", err)
	}
	defer res.Body.Close()

	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(res.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("decode response (status %d): %w", res.StatusCode, err)
	}
	if len(envelope.Errors) > 0 {
		return fmt.Errorf("linear graphql error: %s", envelope.Errors[0].Message)
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("linear returned status %d", res.StatusCode)
	}
	if out != nil && len(envelope.Data) > 0 {
		if err := json.Unmarshal(envelope.Data, out); err != nil {
			return fmt.Errorf("decode data: %w", err)
		}
	}
	return nil
}
