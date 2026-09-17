package config

import "testing"

func TestLoadReportsAllMissing(t *testing.T) {
	t.Setenv("SLACK_APP_TOKEN", "")
	t.Setenv("SLACK_BOT_TOKEN", "")
	t.Setenv("LINEAR_API_KEY", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing vars")
	}
}

func TestLoadSucceedsWithDefaults(t *testing.T) {
	t.Setenv("SLACK_APP_TOKEN", "xapp-1")
	t.Setenv("SLACK_BOT_TOKEN", "xoxb-1")
	t.Setenv("LINEAR_API_KEY", "lin_key")
	t.Setenv("DB_PATH", "")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.DBPath != "./chipin.db" {
		t.Errorf("DBPath default = %q", c.DBPath)
	}
}
