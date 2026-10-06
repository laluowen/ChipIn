package config

import "testing"

func TestLoadReportsAllMissing(t *testing.T) {
	t.Setenv("ENVIRONMENT", "")
	t.Setenv("SLACK_APP_TOKEN", "")
	t.Setenv("SLACK_BOT_TOKEN", "")
	t.Setenv("LINEAR_API_KEY", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing vars")
	}
}

func TestLoadSucceedsWithDefaults(t *testing.T) {
	t.Setenv("ENVIRONMENT", "")
	t.Setenv("SLACK_APP_TOKEN", "xapp-1")
	t.Setenv("SLACK_BOT_TOKEN", "xoxb-1")
	t.Setenv("LINEAR_API_KEY", "lin_key")
	t.Setenv("DB_PATH", "")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Environment != EnvLocal {
		t.Errorf("Environment = %q, want %q", c.Environment, EnvLocal)
	}
	if c.DBPath != "./chipin.db" {
		t.Errorf("DBPath default = %q", c.DBPath)
	}
}

func TestLoadGCPReportsMissing(t *testing.T) {
	t.Setenv("ENVIRONMENT", "gcp")
	t.Setenv("SLACK_BOT_TOKEN", "xoxb-1")
	t.Setenv("LINEAR_API_KEY", "lin_key")
	t.Setenv("SLACK_SIGNING_SECRET", "")
	t.Setenv("GCP_PROJECT_ID", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing gcp vars")
	}
}

func TestLoadGCPSucceedsWithDefaults(t *testing.T) {
	t.Setenv("ENVIRONMENT", "gcp")
	t.Setenv("SLACK_BOT_TOKEN", "xoxb-1")
	t.Setenv("LINEAR_API_KEY", "lin_key")
	t.Setenv("SLACK_SIGNING_SECRET", "shhh")
	t.Setenv("GCP_PROJECT_ID", "my-project")
	t.Setenv("PORT", "")
	t.Setenv("FIRESTORE_DATABASE_ID", "")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Port != "8080" {
		t.Errorf("Port default = %q, want 8080", c.Port)
	}
	if c.FirestoreDatabaseID != "(default)" {
		t.Errorf("FirestoreDatabaseID default = %q", c.FirestoreDatabaseID)
	}
}

func TestLoadInvalidEnvironment(t *testing.T) {
	t.Setenv("ENVIRONMENT", "bogus")
	t.Setenv("SLACK_BOT_TOKEN", "xoxb-1")
	t.Setenv("LINEAR_API_KEY", "lin_key")

	if _, err := Load(); err == nil {
		t.Fatal("expected error for invalid ENVIRONMENT")
	}
}
