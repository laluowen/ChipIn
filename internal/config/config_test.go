package config

import "testing"

// clearLinearEnv resets every Linear auth env var to empty for this test, so
// no test accidentally inherits LINEAR_CLIENT_ID/SECRET from a real
// developer shell or leaks between subtests.
func clearLinearEnv(t *testing.T) {
	t.Helper()
	t.Setenv("LINEAR_API_KEY", "")
	t.Setenv("LINEAR_CLIENT_ID", "")
	t.Setenv("LINEAR_CLIENT_SECRET", "")
}

func TestLoadReportsAllMissing(t *testing.T) {
	t.Setenv("ENVIRONMENT", "")
	t.Setenv("SLACK_APP_TOKEN", "")
	t.Setenv("SLACK_BOT_TOKEN", "")
	clearLinearEnv(t)

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing vars")
	}
}

func TestLoadSucceedsWithDefaults(t *testing.T) {
	t.Setenv("ENVIRONMENT", "")
	t.Setenv("SLACK_APP_TOKEN", "xapp-1")
	t.Setenv("SLACK_BOT_TOKEN", "xoxb-1")
	clearLinearEnv(t)
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
	if c.UsesLinearOAuth() {
		t.Error("UsesLinearOAuth() = true with only LINEAR_API_KEY set")
	}
}

func TestLoadGCPReportsMissing(t *testing.T) {
	t.Setenv("ENVIRONMENT", "gcp")
	t.Setenv("SLACK_BOT_TOKEN", "xoxb-1")
	clearLinearEnv(t)
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
	clearLinearEnv(t)
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
	if len(c.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none for the (default) database", c.Warnings)
	}
}

func TestLoadWarnsOnNamedFirestoreDatabase(t *testing.T) {
	t.Setenv("ENVIRONMENT", "gcp")
	t.Setenv("SLACK_BOT_TOKEN", "xoxb-1")
	clearLinearEnv(t)
	t.Setenv("LINEAR_API_KEY", "lin_key")
	t.Setenv("SLACK_SIGNING_SECRET", "shhh")
	t.Setenv("GCP_PROJECT_ID", "my-project")
	t.Setenv("FIRESTORE_DATABASE_ID", "chipin")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.Warnings) != 1 {
		t.Fatalf("Warnings = %v, want exactly one for a named database", c.Warnings)
	}
}

func TestLoadInvalidEnvironment(t *testing.T) {
	t.Setenv("ENVIRONMENT", "bogus")
	t.Setenv("SLACK_BOT_TOKEN", "xoxb-1")
	clearLinearEnv(t)
	t.Setenv("LINEAR_API_KEY", "lin_key")

	if _, err := Load(); err == nil {
		t.Fatal("expected error for invalid ENVIRONMENT")
	}
}

func TestLoadLinearClientCredentials(t *testing.T) {
	t.Setenv("ENVIRONMENT", "")
	t.Setenv("SLACK_APP_TOKEN", "xapp-1")
	t.Setenv("SLACK_BOT_TOKEN", "xoxb-1")
	clearLinearEnv(t)
	t.Setenv("LINEAR_CLIENT_ID", "client-id")
	t.Setenv("LINEAR_CLIENT_SECRET", "client-secret")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !c.UsesLinearOAuth() {
		t.Error("UsesLinearOAuth() = false with LINEAR_CLIENT_ID/SECRET set")
	}
}

func TestLoadRejectsBothLinearAuthMethods(t *testing.T) {
	t.Setenv("ENVIRONMENT", "")
	t.Setenv("SLACK_APP_TOKEN", "xapp-1")
	t.Setenv("SLACK_BOT_TOKEN", "xoxb-1")
	clearLinearEnv(t)
	t.Setenv("LINEAR_API_KEY", "lin_key")
	t.Setenv("LINEAR_CLIENT_ID", "client-id")
	t.Setenv("LINEAR_CLIENT_SECRET", "client-secret")

	if _, err := Load(); err == nil {
		t.Fatal("expected error when both LINEAR_API_KEY and LINEAR_CLIENT_ID/SECRET are set")
	}
}

func TestLoadRejectsPartialLinearClientCredentials(t *testing.T) {
	t.Setenv("ENVIRONMENT", "")
	t.Setenv("SLACK_APP_TOKEN", "xapp-1")
	t.Setenv("SLACK_BOT_TOKEN", "xoxb-1")
	clearLinearEnv(t)
	t.Setenv("LINEAR_CLIENT_ID", "client-id")
	// LINEAR_CLIENT_SECRET deliberately left unset.

	if _, err := Load(); err == nil {
		t.Fatal("expected error when only LINEAR_CLIENT_ID is set")
	}
}
