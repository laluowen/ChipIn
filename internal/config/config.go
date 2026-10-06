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
	LinearAPIKey  string // Linear personal API key

	// Mode A (EnvLocal): Socket Mode + SQLite.
	SlackAppToken string // xapp-... (Socket Mode)
	DBPath        string // SQLite file path

	// Mode B (EnvGCP): HTTP webhooks + Firestore.
	Port                string // HTTP listen port; Cloud Run sets this
	SlackSigningSecret  string // verifies inbound Slack webhook requests
	GCPProjectID        string // Firestore project
	FirestoreDatabaseID string // Firestore database ID, usually "(default)"
}

// Load reads configuration from environment variables, returning an error
// that names every variable missing for the selected Environment at once.
func Load() (*Config, error) {
	c := &Config{
		Environment:   envOr("ENVIRONMENT", EnvLocal),
		SlackBotToken: os.Getenv("SLACK_BOT_TOKEN"),
		LinearAPIKey:  os.Getenv("LINEAR_API_KEY"),
	}

	var missing []string
	if c.SlackBotToken == "" {
		missing = append(missing, "SLACK_BOT_TOKEN")
	}
	if c.LinearAPIKey == "" {
		missing = append(missing, "LINEAR_API_KEY")
	}

	switch c.Environment {
	case EnvGCP:
		c.Port = envOr("PORT", "8080")
		c.SlackSigningSecret = os.Getenv("SLACK_SIGNING_SECRET")
		c.GCPProjectID = os.Getenv("GCP_PROJECT_ID")
		c.FirestoreDatabaseID = envOr("FIRESTORE_DATABASE_ID", "(default)")
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
