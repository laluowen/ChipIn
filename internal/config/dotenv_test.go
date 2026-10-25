package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeEnv(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadDotEnv(t *testing.T) {
	for _, k := range []string{"DE_PLAIN", "DE_EXPORT", "DE_DQ", "DE_SQ", "DE_INLINE", "DE_EMPTY", "DE_SET", "DE_EMPTYSET", "DE_HASH"} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
	t.Setenv("DE_SET", "real")
	t.Setenv("DE_EMPTYSET", "")

	p := writeEnv(t, `# comment

DE_PLAIN=plain
export DE_EXPORT=exported
DE_DQ="has # hash and spaces"
DE_SQ='single'
DE_INLINE=value # trailing comment
DE_HASH=a#b
DE_EMPTY=
DE_SET=from-file
DE_EMPTYSET=from-file
`)
	if err := loadDotEnv(p); err != nil {
		t.Fatalf("loadDotEnv: %v", err)
	}

	want := map[string]string{
		"DE_PLAIN": "plain", "DE_EXPORT": "exported", "DE_DQ": "has # hash and spaces",
		"DE_SQ": "single", "DE_INLINE": "value", "DE_HASH": "a#b", "DE_EMPTY": "",
		"DE_SET": "real", "DE_EMPTYSET": "from-file",
	}
	for k, v := range want {
		if got := os.Getenv(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
}

func TestLoadDotEnvMissingFile(t *testing.T) {
	if err := loadDotEnv(filepath.Join(t.TempDir(), "nope")); err != nil {
		t.Errorf("missing file should be ignored, got %v", err)
	}
}

func TestLoadDotEnvMalformed(t *testing.T) {
	if err := loadDotEnv(writeEnv(t, "not a pair\n")); err == nil {
		t.Error("expected error for malformed line")
	}
}

func TestLoadReadsDotEnvFromCwd(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("SLACK_BOT_TOKEN=xoxb-file\nSLACK_APP_TOKEN=xapp-file\nLINEAR_API_KEY=lin_file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("ENVIRONMENT", "")
	t.Setenv("SLACK_APP_TOKEN", "xapp-real")
	t.Setenv("SLACK_BOT_TOKEN", "")
	_ = os.Unsetenv("SLACK_BOT_TOKEN")
	clearLinearEnv(t)
	_ = os.Unsetenv("LINEAR_API_KEY")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.SlackBotToken != "xoxb-file" || c.SlackAppToken != "xapp-real" {
		t.Errorf("got bot=%q app=%q; want file bot token, real app token", c.SlackBotToken, c.SlackAppToken)
	}
	if c.LinearAPIKey != "lin_file" {
		t.Errorf("LinearAPIKey = %q, want lin_file (from .env)", c.LinearAPIKey)
	}
}
