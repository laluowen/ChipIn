package linear

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// DefaultTokenEndpoint is Linear's OAuth2 token endpoint, used for both the
// client_credentials grant and (in future) authorization_code/refresh_token.
const DefaultTokenEndpoint = "https://api.linear.app/oauth/token" //nolint:gosec // URL, not a credential

// DefaultScopes covers everything ChipIn's handler needs: "read" to fetch
// issues/team estimation settings, "write" to set the final estimate.
var DefaultScopes = []string{"read", "write"}

// refreshBuffer is how far ahead of a token's actual expiry it gets
// refetched, so a request started just before expiry doesn't race it.
const refreshBuffer = 5 * time.Minute

// TokenSource supplies the Authorization header value to send with each
// Linear API request. Implementations:
//   - staticToken: wraps a personal API key, sent as-is (no "Bearer" prefix)
//   - ClientCredentialsTokenSource: OAuth2 client_credentials grant
//   - (planned) an authorization_code + refresh_token source for webhook-mode
//     installs that need access to private Linear teams — see PLAN.md §5
type TokenSource interface {
	// AuthHeader returns the full Authorization header value to send,
	// fetching or refreshing a token first if needed.
	AuthHeader(ctx context.Context) (string, error)
}

// Invalidator is implemented by TokenSources that cache a token and can
// refetch it on demand. Client.do uses this to retry once after a 401,
// rather than assuming the credentials themselves are bad.
type Invalidator interface {
	Invalidate()
}

// staticToken is a personal API key: sent as-is, never refreshed.
type staticToken string

func (s staticToken) AuthHeader(context.Context) (string, error) {
	return string(s), nil
}

// ClientCredentialsTokenSource implements Linear's OAuth2 client_credentials
// grant: no user interaction, no redirect/callback, no refresh token to
// persist. It mints an access token good for ~30 days, caches it in memory,
// and refetches it when it's close to expiry (or invalidated after a 401).
// Safe for concurrent use.
type ClientCredentialsTokenSource struct {
	ClientID     string
	ClientSecret string
	Scopes       []string // defaults to DefaultScopes if empty
	Endpoint     string   // defaults to DefaultTokenEndpoint if empty
	HTTPClient   *http.Client

	mu     sync.Mutex
	token  string
	expiry time.Time
}

// NewClientCredentialsTokenSource returns a ClientCredentialsTokenSource
// with ChipIn's default scopes, endpoint, and HTTP client.
func NewClientCredentialsTokenSource(clientID, clientSecret string) *ClientCredentialsTokenSource {
	return &ClientCredentialsTokenSource{
		ClientID:     clientID,
		ClientSecret: clientSecret,
	}
}

// AuthHeader returns "Bearer <token>", fetching a new token if there's no
// cached one or it's within refreshBuffer of expiring.
func (s *ClientCredentialsTokenSource) AuthHeader(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.token == "" || time.Now().After(s.expiry.Add(-refreshBuffer)) {
		if err := s.refresh(ctx); err != nil {
			return "", err
		}
	}
	return "Bearer " + s.token, nil
}

// Invalidate clears the cached token, forcing the next AuthHeader call to
// fetch a fresh one.
func (s *ClientCredentialsTokenSource) Invalidate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.token = ""
}

// refresh requests a new token. Caller must hold s.mu.
func (s *ClientCredentialsTokenSource) refresh(ctx context.Context) error {
	endpoint := s.Endpoint
	if endpoint == "" {
		endpoint = DefaultTokenEndpoint
	}
	scopes := s.Scopes
	if len(scopes) == 0 {
		scopes = DefaultScopes
	}
	httpClient := s.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}

	form := url.Values{
		"grant_type":    {"client_credentials"},
		"scope":         {strings.Join(scopes, ",")},
		"client_id":     {s.ClientID},
		"client_secret": {s.ClientSecret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	res, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("linear token request: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return fmt.Errorf("decode token response (status %d): %w", res.StatusCode, err)
	}
	if res.StatusCode != http.StatusOK {
		if body.ErrorDesc != "" {
			return fmt.Errorf("linear token request failed: %s", body.ErrorDesc)
		}
		return fmt.Errorf("linear token request failed with status %d", res.StatusCode)
	}
	if body.AccessToken == "" {
		return fmt.Errorf("linear token response had no access_token")
	}

	s.token = body.AccessToken
	s.expiry = time.Now().Add(time.Duration(body.ExpiresIn) * time.Second)
	return nil
}
