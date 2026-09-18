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
    Scale      poker.Scale       // estimate points, derived from the Linear team
    Notices    map[string]Notice // Slack userID -> their private DM notice message
}

// Notice locates a previously-sent private DM message so it can be updated in
// place instead of sending a new one on every vote.
type Notice struct {
    ChannelID string
    MessageTS string
}

type SessionRepository interface {
    GetSession(ctx context.Context, issueID string) (*PokerSession, error)
    SaveSession(ctx context.Context, session *PokerSession) error
    DeleteSession(ctx context.Context, issueID string) error
    Close() error
}
```

**Notes / deviations from the original sketch (as built):**

- `Votes` is `map[string]string`, not `map[string]int`. The value is the display
  label of the chosen point (e.g. `"5"` or `"M"` for T-shirt scales). There is no
  `"?"` card — an abstention is simply a missing vote.
- `Scale` is derived per team from Linear's estimation settings, not hard-coded (see
  §3). A `poker.Point` is `{Label, Value}`: `Label` is what voters see, `Value` is the
  number written to Linear. The scale is persisted (SQLite `scale` column) so buttons,
  vote validation, and consensus all use the same points.
- Added `Notices`, keyed by Slack user ID, so private vote confirmations can be
  updated in place (`chat.update`) rather than reposted every vote. Neither
  `chat.postEphemeral` nor an interaction's `response_url` can be updated/deduped
  across separate interaction payloads in practice — a real DM message is the only
  mechanism that supports it. Persisted as the `notices` SQLite column.
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

1. **Start Session (`/chipin ENG-123`)**

    - Parse the identifier from the Slack command text (`TEAM-123` → team key +
      number), or from a pasted Linear issue URL.

    - Query `https://api.linear.app/graphql` for the issue (UUID, title, estimate)
      **and its team's estimation settings** (`issueEstimationType`,
      `issueEstimationExtended`, `issueEstimationAllowZero`).

    - Build the vote scale with `poker.ScaleFor` (exponential / fibonacci / linear /
      T-shirt, ± extended, ± zero). If estimation is disabled for the team, reply
      with an ephemeral error and stop.

    - Post a Slack Block Kit message with one button per scale point (its header
      hyperlinks the issue identifier to Linear via `Issue.url`), then fetch the
      message's Slack permalink (`chat.getPermalink`) and create a `PokerSession`
      (status `voting`, carrying the scale, `IssueURL`, and `MessageLink`).
2. **Cast Votes (Async)**

    - Slack interaction routes to `HandleInteraction`. The issue UUID rides in the
      button `Value`; the vote label rides in the `action_id` (`vote:<label>`).

    - App loads the `PokerSession`, updates the `Votes` map, saves it, and fires
      `chat.update` to refresh the UI (individual votes stay hidden while voting).
      The voter gets a private confirmation via a DM the bot opens with them once
      and then updates in place on every later vote/retract (see §2.1 `Notices`).
      The DM links back to the vote message (`<MessageLink|Identifier>`) so the
      round's context is never more than a click away. Voters can change or
      **retract** their own vote at any time.
3. **Reveal ⇄ Continue (toggle)**

    - "Reveal votes" flips status to `revealed`: the UI shows every vote, the
      recommended consensus (the **mode** — the most-voted point, since a median
      can snap to a value nobody actually cast on a non-linear scale — **ties
      breaking upward** so we overestimate), and a dropdown prefilled to that
      recommendation. This is a **toggle**, not a teardown.

    - From the summary the round can go back to `voting` ("Continue voting") for
      another pass, be locked in with "Set estimate", or abandoned with "Cancel".
4. **Set Estimate (lock in)**

    - "Set estimate" reads whatever value is currently selected in the dropdown
      (defaulting to the consensus if untouched — the team can override after
      discussion), executes a GraphQL mutation to set the issue's estimate, updates
      the message to the final state, and deletes the `PokerSession`. "Cancel"
      deletes the session without writing. Stale clicks on a deleted session are
      ignored.

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
user/workspace but does not scale to distributing ChipIn as an installable app.

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

## 6. Issue Context

Every surface where a user encounters a round should carry a path back to its
source, so nobody has to hunt for "which issue was this again?" or "where's the
actual vote".

**Implemented:**

- The vote message header hyperlinks the issue identifier to the issue's Linear
  URL (`Issue.url` from the GraphQL API, persisted as `PokerSession.IssueURL`).
- Private DM notices (vote confirmations, retractions) link back to the vote
  message itself via its Slack permalink (`chat.getPermalink`, fetched once when
  the round starts and persisted as `PokerSession.MessageLink`), rendered as
  `<permalink|IDENTIFIER>`. Permalink lookup is best-effort: if it fails, notices
  just fall back to plain text rather than blocking the round from starting.

**Planned: issue description in a thread.** Post the Linear issue's description
(and other useful attributes — assignee, priority, labels, current estimate if
any) as a **threaded reply** under the vote message (`thread_ts` = the vote
message's `ts`), posted once when the round starts. This keeps the primary
message focused on voting while giving anyone who wants more context a reply to
expand, without re-fetching Linear or leaving Slack. Notes for whoever picks
this up:

- Needs `Issue.description` (Markdown) plus whichever attributes are wanted from
  the existing `FetchIssue` query — extend the GraphQL selection rather than a
  second round-trip.
- Linear's description Markdown isn't 1:1 with Slack mrkdwn (e.g. Linear supports
  nested docs, `+++` collapsible sections, mentions-via-URL); a naive dump will
  render oddly for anything beyond plain paragraphs/lists. Worth a small
  Markdown-to-mrkdwn pass rather than posting raw.
- Post via `chat.postMessage` with `ThreadTimestamp` (`slack.MsgOptionTS`) set to
  `sess.MessageTS`, right after the main vote message is posted in
  `HandleSlashCommand`.
- Not persisted on `PokerSession` — it's a one-time post at round start, nothing
  to update later, so no new session field is needed (unlike `IssueURL` and
  `MessageLink`, which are read again on every re-render/DM).

## 7. Build, Release & CI (planned)

- **Toolchain** pinned via `mise` (`mise.toml`): Go + `golangci-lint`.
- **Lint** with golangci-lint v2 (`.golangci.yaml`: gofumpt/gci formatters +
  errcheck/govet/staticcheck/gosec/revive/…).
- **Release** a static `CGO_ENABLED=0` binary + distroless image via GoReleaser.
  Because SQLite is pure-Go, Linux/arm64+amd64 cross-compiles need no C toolchain.
- **CI on GitHub Actions** (free hosted runners): `go test ./...` on PRs; tag →
  GoReleaser release. (Not built yet — local testing first, per project scope.)
