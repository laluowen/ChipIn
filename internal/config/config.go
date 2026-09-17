// Package config loads chipin's runtime configuration from the environment.
package config

import (
	"fmt"
	"os"
)

// Config holds the settings needed to run in local Socket Mode. Webhook/Cloud
// Run fields will be added when that transport lands (see PLAN.md).
type Config struct {
	SlackAppToken string // xapp-... (Socket Mode)
	SlackBotToken string // xoxb-...
	LinearAPIKey  string // Linear personal API key
	DBPath        string // SQLite file path
}

// Load reads configuration from environment variables, returning an error that
// names every missing required variable at once.
func Load() (*Config, error) {
	c := &Config{
		SlackAppToken: os.Getenv("SLACK_APP_TOKEN"),
		SlackBotToken: os.Getenv("SLACK_BOT_TOKEN"),
		LinearAPIKey:  os.Getenv("LINEAR_API_KEY"),
		DBPath:        envOr("DB_PATH", "./chipin.db"),
	}

	var missing []string
	if c.SlackAppToken == "" {
		missing = append(missing, "SLACK_APP_TOKEN")
	}
	if c.SlackBotToken == "" {
		missing = append(missing, "SLACK_BOT_TOKEN")
	}
	if c.LinearAPIKey == "" {
		missing = append(missing, "LINEAR_API_KEY")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required environment variables: %v", missing)
	}
	return c, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
