package slack_test

import (
	"os"
	"strings"
	"testing"
)

// normalize drops comments and the lines that must legitimately differ
// between the Socket Mode and HTTP manifests (the request URLs, and
// socket_mode_enabled's value), so what's left can be compared for drift.
func normalize(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // fixed repo paths
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "#"):
		case strings.Contains(line, "url:"): // slash_commands' url: and interactivity's request_url:
		case strings.Contains(line, "socket_mode_enabled:"):
		default:
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// The two manifests must differ only by their request URLs and
// socket_mode_enabled, so they can't silently drift apart (a scope added to
// one and forgotten in the other, say).
func TestManifestsMatchExceptTransport(t *testing.T) {
	if normalize(t, "manifest.socket.yaml") != normalize(t, "manifest.http.yaml") {
		t.Error("manifest.socket.yaml and manifest.http.yaml differ by more than their request URLs and socket_mode_enabled")
	}
}

func TestSocketManifestHasNoRequestURLs(t *testing.T) {
	b, err := os.ReadFile("manifest.socket.yaml") //nolint:gosec // fixed repo path
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "url:") {
		t.Error("manifest.socket.yaml should have no request URLs: Socket Mode doesn't use them")
	}
	if !strings.Contains(string(b), "socket_mode_enabled: true") {
		t.Error("manifest.socket.yaml should have socket_mode_enabled: true")
	}
}

func TestHTTPManifestHasRequestURLs(t *testing.T) {
	b, err := os.ReadFile("manifest.http.yaml") //nolint:gosec // fixed repo path
	if err != nil {
		t.Fatal(err)
	}
	content := string(b)
	if !strings.Contains(content, "url: https://YOUR-SERVICE.run.app/slack/commands") {
		t.Error("manifest.http.yaml is missing the slash command request URL")
	}
	if !strings.Contains(content, "request_url: https://YOUR-SERVICE.run.app/slack/interactions") {
		t.Error("manifest.http.yaml is missing the interactivity request URL")
	}
	if !strings.Contains(content, "socket_mode_enabled: false") {
		t.Error("manifest.http.yaml should have socket_mode_enabled: false")
	}
}
