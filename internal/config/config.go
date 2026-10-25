// Package config loads ChipIn's runtime configuration from the environment.
package config

import (
	"fmt"
	"os"
)

// Environment selects which deployment mode ChipIn runs in (see PLAN.md §1.2).
const (
	EnvLocal = "local" // Mode A: Socket Mode + SQLite (default)
	EnvGCP   = "gcp"   // Mode B: HTTP webhooks + Firestore, for Cloud Run
)

// Config holds ChipIn's runtime settings. Which fields are required depends
// on Environment: Mode A needs the Socket Mode / SQLite fields, Mode B needs
// the webhook / Firestore fields. Fields unused by the active mode are left
// zero-valued.
type Config struct {
	Environment string // EnvLocal | EnvGCP

	// Shared by both modes.
	SlackBotToken string // xoxb-...

	// Linear auth: exactly one of these two is required (validated in Load).
	// Personal key is the zero-config path; client credentials is the OAuth
	// path — see PLAN.md §5 for why client_credentials rather than
	// authorization_code (works in both modes, no callback endpoint, no
	// refresh token to persist). If both LinearClientID and
	// LinearClientSecret are set, they take precedence over LinearAPIKey.
	LinearAPIKey       string // Linear personal API key
	LinearClientID     string // Linear OAuth2 app client ID
	LinearClientSecret string // Linear OAuth2 app client secret

	// Warnings are non-fatal notes about the configuration (a value that was
	// adjusted or should be double-checked) for the caller to log.
	Warnings []string

	// Mode A (EnvLocal): Socket Mode + SQLite.
	SlackAppToken string // xapp-... (Socket Mode)
	DBPath        string // SQLite file path

	// Mode B (EnvGCP): HTTP webhooks + Firestore.
	Port                string // HTTP listen port; Cloud Run sets this
	SlackSigningSecret  string // verifies inbound Slack webhook requests
	GCPProjectID        string // Firestore project
	FirestoreDatabaseID string // Firestore database ID, usually "(default)"
}

// UsesLinearOAuth reports whether Linear auth is configured for the OAuth2
// client_credentials grant (LinearClientID/LinearClientSecret) rather than a
// personal API key.
func (c *Config) UsesLinearOAuth() bool {
	return c.LinearClientID != ""
}

// Load reads configuration from environment variables, falling back to a
// ".env" file in the working directory for anything unset (real environment
// variables always win). It returns an error that names every variable
// missing for the selected Environment at once.
func Load() (*Config, error) {
	if err := loadDotEnv(".env"); err != nil {
		return nil, err
	}
	c := &Config{
		Environment:        envOr("ENVIRONMENT", EnvLocal),
		SlackBotToken:      os.Getenv("SLACK_BOT_TOKEN"),
		LinearAPIKey:       os.Getenv("LINEAR_API_KEY"),
		LinearClientID:     os.Getenv("LINEAR_CLIENT_ID"),
		LinearClientSecret: os.Getenv("LINEAR_CLIENT_SECRET"),
	}

	var missing []string
	if c.SlackBotToken == "" {
		missing = append(missing, "SLACK_BOT_TOKEN")
	}

	hasKey := c.LinearAPIKey != ""
	hasClientID := c.LinearClientID != ""
	hasClientSecret := c.LinearClientSecret != ""
	switch {
	case hasKey && (hasClientID || hasClientSecret):
		return nil, fmt.Errorf("specify either LINEAR_API_KEY or LINEAR_CLIENT_ID+LINEAR_CLIENT_SECRET, not both")
	case hasClientID != hasClientSecret:
		return nil, fmt.Errorf("LINEAR_CLIENT_ID and LINEAR_CLIENT_SECRET must both be set")
	case !hasKey && !hasClientID:
		missing = append(missing, "LINEAR_API_KEY (or LINEAR_CLIENT_ID+LINEAR_CLIENT_SECRET)")
	}

	switch c.Environment {
	case EnvGCP:
		c.Port = envOr("PORT", "8080")
		c.SlackSigningSecret = os.Getenv("SLACK_SIGNING_SECRET")
		c.GCPProjectID = os.Getenv("GCP_PROJECT_ID")
		c.FirestoreDatabaseID = envOr("FIRESTORE_DATABASE_ID", "(default)")
		if c.FirestoreDatabaseID != "(default)" {
			c.Warnings = append(c.Warnings, fmt.Sprintf("FIRESTORE_DATABASE_ID=%q is a named database: Firestore's free quota only covers (default), so every read and write is billed",
				c.FirestoreDatabaseID))
		}
		if c.SlackSigningSecret == "" {
			missing = append(missing, "SLACK_SIGNING_SECRET")
		}
		if c.GCPProjectID == "" {
			missing = append(missing, "GCP_PROJECT_ID")
		}
	case EnvLocal:
		c.SlackAppToken = os.Getenv("SLACK_APP_TOKEN")
		c.DBPath = envOr("DB_PATH", "./chipin.db")
		if c.SlackAppToken == "" {
			missing = append(missing, "SLACK_APP_TOKEN")
		}
	default:
		return nil, fmt.Errorf("invalid ENVIRONMENT %q: want %q or %q", c.Environment, EnvLocal, EnvGCP)
	}

	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required environment variables for ENVIRONMENT=%s: %v", c.Environment, missing)
	}
	return c, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
