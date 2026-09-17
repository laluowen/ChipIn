# PLAN.md: Self-Hosted Async Planning Poker

## 1. System Architecture

A single, statically compiled Go binary capable of operating in two distinct deployment modes. The application abstracts its storage and transport layers to allow seamless transition between local testing, GCP Cloud Run, and AWS Lambda without rewriting core business logic.

### 1.1 Core Stack

- **Language:** Go (Golang 1.21+)
- **Base Container Image:** `gcr.io/distroless/static-debian12`
- **Slack Integration:** `[github.com/slack-go/slack](https://github.com/slack-go/slack)`
- **Linear Integration:** Standard `net/http` client executing raw GraphQL queries.

### 1.2 Deployment Modes

- **Mode A: Local / Private Network (Socket Mode)**

    - **Transport:** Outbound WebSockets.

    - **State:** Local SQLite database.
- **Mode B: Serverless / Cloud Run (Webhook Mode)**

    - **Transport:** HTTP Webhooks.

    - **State:** Google Cloud Firestore.

## 2. Abstraction Boundaries

To decouple the core poker logic from the infrastructure, we define interfaces for both State and Transport.

### 2.1 Storage Interface (`internal/store`)

```go
package store
import "context"

type PokerSession struct {
    IssueID    string            // Linear issue UUID (primary key)
    Identifier string            // human identifier, e.g. "ENG-123"
    Title      string            // issue title, for display
    Status     string            // "voting" | "revealed"
    ChannelID  string            // Slack channel
    MessageTS  string            // Slack message ts, for chat.update
    Votes      map[string]string // Slack userID -> vote label
}

type SessionRepository interface {
    GetSession(ctx context.Context, issueID string) (*PokerSession, error)
    SaveSession(ctx context.Context, session *PokerSession) error
    DeleteSession(ctx context.Context, issueID string) error
    Close() error
}
```

**Notes / deviations from the original sketch (as built):**

- `Votes` is `map[string]string`, not `map[string]int`. `"?"` is a valid vote, and
  storing the raw label keeps the summary display faithful. Consensus parses the
  numeric labels and ignores the rest.
- Added `Identifier`, `Title`, and `MessageTS`. `MessageTS` is required to
  `chat.update` the existing message; `Identifier`/`Title` are for display.
- Added `Close()` so `main` can release the SQLite handle cleanly.
- The concrete driver is **pure-Go `modernc.org/sqlite`** (`CGO_ENABLED=0`), chosen
  so the distroless static binary cross-compiles cleanly on free GitHub runners.

### 2.2 Transport Boundary (`handler.go`)

A centralized handler struct processes the game logic regardless of whether the inbound request came from a WebSocket or an HTTP endpoint.

```go
package handler

// SlackAPI / LinearAPI are the minimal interfaces the handler needs; the real
// *slack.Client and *linear.Client satisfy them, and fakes stand in for tests.
type PokerHandler struct {
    Slack  SlackAPI
    Linear LinearAPI
    Store  store.SessionRepository
    Scale  poker.Scale
}

func (h *PokerHandler) HandleSlashCommand(ctx context.Context, cmd slack.SlashCommand) error
func (h *PokerHandler) HandleInteraction(ctx context.Context, cb slack.InteractionCallback) error
```

The handler holds no concrete Slack/Linear client and no raw token — those live
behind interfaces so the core stays unit-testable without live services.

## 3. The Execution Flow

1. **Start Session (`/poker ENG-123`)**

    - Parse the identifier from the Slack command text (`TEAM-123` → team key + number).

    - Query `https://api.linear.app/graphql` for the issue (UUID, title, estimate).

    - Post a Slack Block Kit message with estimation buttons, then create a
      `PokerSession` (status `voting`) recording the returned message `ts`.
2. **Cast Votes (Async)**

    - Slack interaction routes to `HandleInteraction`. The issue UUID rides in the
      button `Value`; the vote label rides in the `action_id` (`vote:<label>`).

    - App loads the `PokerSession`, updates the `Votes` map, saves it, and fires
      `chat.update` to refresh the UI (individual votes stay hidden while voting).
3. **Reveal ⇄ Continue (toggle)**

    - "Reveal votes" flips status to `revealed`: the UI shows every vote and the
      recommended consensus (median of numeric votes, snapped to the nearest scale
      point). This is a **toggle**, not a teardown.

    - From the summary the round can go back to `voting` ("Continue voting") for
      another pass, or be locked in with "Set estimate".
4. **Set Estimate (lock in)**

    - "Set estimate" executes a GraphQL mutation to Linear to set the issue's
      estimate to the consensus value, updates the message to the final state, and
      deletes the `PokerSession` from the store. Stale clicks on a deleted session
      are ignored.

## 4. Environment Configuration

### For Local / Socket Mode (implemented)

- `SLACK_APP_TOKEN` (xapp-...)
- `SLACK_BOT_TOKEN` (xoxb-...)
- `LINEAR_API_KEY` (Linear personal API key)
- `DB_PATH` (optional; defaults to `./chipin.db`)

### For Cloud Run / Webhook Mode (planned)

- `PORT` (Provided automatically by Cloud Run)
- `SLACK_BOT_TOKEN` (xoxb-...)
- `SLACK_SIGNING_SECRET`
- `LINEAR_API_KEY`
- `DB_TYPE=firestore`
- `GCP_PROJECT_ID`

## 5. Linear OAuth App (planned)

The proof-of-concept authenticates to Linear with a single **personal API key**
(`LINEAR_API_KEY`), sent as-is in the `Authorization` header. This is fine for one
user/workspace but does not scale to distributing chipin as an installable app.

The intended end state is a full **Linear OAuth 2.0 application** so each workspace
grants its own scoped access:

- **Register** an OAuth application in Linear (`https://linear.app/settings/api`),
  obtaining a client ID/secret and setting the redirect URI.
- **Authorization code flow:** redirect the installer to
  `https://linear.app/oauth/authorize` with the needed scopes (at minimum
  `read` and `write` for reading issues and updating estimates; `issues:create`
  is not needed), then exchange the code at `https://api.linear.app/oauth/token`.
- **Token storage:** persist per-workspace access/refresh tokens in the store
  (new table/collection), keyed by Linear workspace/org ID. Refresh on expiry.
- **Auth header:** OAuth access tokens use the `Bearer <token>` prefix, unlike
  personal keys. `linear.Client` should grow an option to select the scheme, or
  accept a token source that yields a fresh bearer token per request.
- **Abstraction:** introduce a `TokenSource` (per workspace) so the handler picks
  the right Linear credentials for the workspace an interaction came from. The
  existing `LinearAPI` interface already isolates the handler from this change.
- **Webhook signature / state:** OAuth install + Slack "Add to workspace" pair
  naturally with **Mode B** (HTTP webhooks); build them together.

Until then, keep the personal-key path as the zero-config local/dev mode.

## 6. Build, Release & CI (planned)

- **Toolchain** pinned via `mise` (`mise.toml`): Go + `golangci-lint`.
- **Lint** with golangci-lint v2 (`.golangci.yaml`: gofumpt/gci formatters +
  errcheck/govet/staticcheck/gosec/revive/…).
- **Release** a static `CGO_ENABLED=0` binary + distroless image via GoReleaser.
  Because SQLite is pure-Go, Linux/arm64+amd64 cross-compiles need no C toolchain.
- **CI on GitHub Actions** (free hosted runners): `go test ./...` on PRs; tag →
  GoReleaser release. (Not built yet — local testing first, per project scope.)
