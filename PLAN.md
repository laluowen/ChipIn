# PLAN.md: Self-Hosted Async Planning Poker

## 1. System Architecture

A single, statically compiled Go binary capable of operating in two distinct deployment modes. The application abstracts its storage and transport layers to allow seamless transition between local testing, GCP Cloud Run, and AWS Lambda without rewriting core business logic.

### 1.1 Core Stack

- **Language:** Go (Golang 1.21+)
- **Base Container Image:** `gcr.io/distroless/static-debian12`
- **Slack Integration:** [github.com/slack-go/slack](https://github.com/slack-go/slack)
- **Linear Integration:** Standard `net/http` client executing raw GraphQL queries.

### 1.2 Deployment Modes

Both modes ship in the same binary, selected at startup by the `ENVIRONMENT`
environment variable (`local` | `gcp`; see `internal/config`). `cmd/chipin`'s
`run()` branches once into `runSocket` or `runWebhook`; everything below that
(`internal/handler`, `internal/poker`, `internal/linear`) is shared and
doesn't know which mode it's running under.

- **Mode A: Local / Private Network (Socket Mode)** — `ENVIRONMENT=local` (default)

    - **Transport:** Outbound WebSockets (`internal/socket`).

    - **State:** Local JSON data file (`store.JSONFile`).
- **Mode B: Serverless / Cloud Run (Webhook Mode)** — `ENVIRONMENT=gcp`

    - **Transport:** HTTP Webhooks (`internal/webhook`), signature-verified
      with the Slack signing secret.

    - **State:** Google Cloud Firestore (`store.Firestore`).

    - **Ack strategy:** requests are handled **synchronously** — the handler
      runs to completion before the HTTP response is written, same as Mode
      A's immediate-ack-then-process shape in spirit but inverted (there's no
      separate ack step over HTTP; the response *is* the ack). Slack requires
      a response within 3s; on Cloud Run a cold start eats into that budget.
      This was a deliberate simplicity-over-robustness tradeoff for the first
      cut — no goroutine-after-response (which needs Cloud Run's "CPU always
      allocated" setting to avoid being frozen mid-flight) and no Cloud Tasks
      queue. If timeouts show up in practice, the escape hatches are, in
      order of effort: set `min-instances=1` to kill cold starts, enable
      CPU-always-allocated and move the handler call into a goroutine, or
      defer the real work to a Cloud Tasks queue and a second endpoint.

## 2. Abstraction Boundaries

To decouple the core poker logic from the infrastructure, we define interfaces for both State and Transport.

### 2.1 Storage Interface (`internal/store`)

```go
package store
import "context"

type PokerSession struct {
    IssueID     string            // Linear issue UUID (primary key)
    Identifier  string            // human identifier, e.g. "ENG-123"
    Title       string            // issue title, for display
    IssueURL    string            // Linear app URL for the issue, for linking back
    Status      string            // "voting" | "revealed"
    ChannelID   string            // Slack channel
    MessageTS   string            // Slack message ts, for chat.update
    MessageLink string            // permalink to the vote message, for private-notice context
    RequestedBy string            // Slack userID of whoever ran /chipin, for attribution
    Votes       map[string]string // Slack userID -> vote label
    Scale       poker.Scale       // estimate points, derived from the Linear team
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
  number written to Linear. The scale is persisted alongside the rest of the session
  so buttons, vote validation, and consensus all use the same points.
- Added `IssueURL`, `MessageLink`, and `RequestedBy` for context/attribution — see
  §6.
- **A DM-based private-notice design (`Notices map[string]Notice`, keyed per user)
  was tried and reverted.** It let private vote confirmations be updated in place
  via `chat.update` instead of reposted every vote, but needed `conversations.open`,
  a persisted per-user `{ChannelID, MessageTS}` reference, and stale-reference
  fallback logic — too many moving parts for the win. Reverted to plain
  `chat.postEphemeral` (one new message per vote; Slack has no way to update or
  dedupe these). (When local storage was still SQLite, the abandoned design's
  `notices` column was dropped via an `ALTER TABLE ... DROP COLUMN` migration on
  databases that already had it; `store.JSONFile` has no such column and no
  migration path at all — see "Local storage" below.)
- Added `Identifier`, `Title`, and `MessageTS`. `MessageTS` is required to
  `chat.update` the existing message; `Identifier`/`Title` are for display.
- Added `Close()` so `main` can release the store cleanly (a no-op for
  `store.JSONFile`, since every write is already on disk by the time it returns;
  meaningful for `store.Firestore`'s client).

### Local storage: a JSON file (implemented; replaced SQLite)

Firestore is the production store, so SQLite was a second data model to keep in step
with it. `local` mode runs as one process, so it doesn't need what SQLite is for
(transactions shared between processes), and its SQL bought nothing the Firestore
store doesn't already do with a plain map lookup. The driver also added ~6 MB to the
binary and ~30 `modernc.org` packages; both are gone.

`JSONFile` (`internal/store/jsonfile.go`) keeps every in-progress session in memory,
mirroring Firestore's `chipin_sessions` collection as a `map[issueID]*PokerSession`.
`SaveSession`/`DeleteSession` build the new state on a copy of what they touch, save,
and only then swap it into memory, so a failed save changes nothing. Saving writes a
temp file, fsyncs it and renames it over the old one, so a crash never leaves a
partial file. `GetSession` returns a copy (`Votes` and `Scale` are mutable), so
neither the store nor the caller can reach into the other's memory. Rewriting the
whole file per save is fine at this app's scale: sessions are short-lived (a session
is deleted the moment its estimate is set or the round is cancelled), so the file
never holds more than the currently in-progress rounds.

Limits of the design: one process per file (a second would clobber the first's
writes; there is no lock, since the portable options are worse than the failure
they'd prevent), and the default path is `./chipin.json` (`DB_PATH`).

Migration: none. Sessions are short-lived, so there was nothing worth carrying
forward from the old SQLite store. An old SQLite file at `DB_PATH` is refused with an
error that says so (recognised by its header) rather than silently ignored or
overwritten. Handler and webhook tests use an in-memory `JSONFile`.


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

    - Post a Slack Block Kit message with one button per scale point. Its header
      hyperlinks the issue identifier to Linear via `Issue.url`, and attributes the
      round to the requester (`Started by <@user>`). Fetch the message's Slack
      permalink (`chat.getPermalink`) and create a `PokerSession` (status
      `voting`, carrying the scale, `IssueURL`, `MessageLink`, and `RequestedBy`).
2. **Cast Votes (Async)**

    - Slack interaction routes to `HandleInteraction`. The issue UUID rides in the
      button `Value`; the vote label rides in the `action_id` (`vote:<label>`).

    - App loads the `PokerSession`, updates the `Votes` map, saves it, and fires
      `chat.update` to refresh the UI (individual votes stay hidden while voting).
      The voter gets a private confirmation via `chat.postEphemeral` — a fresh
      ephemeral message per vote/retract (Slack cannot update or dedupe these; see
      §2.1). It's prefixed with the issue key, hyperlinked to the vote message
      (`<MessageLink|Identifier>`) when a permalink is on file, so the round's
      context is a click away without extra vertical space. Voters can change or
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

- `ENVIRONMENT=local` (default — can be omitted)
- `SLACK_APP_TOKEN` (xapp-...)
- `SLACK_BOT_TOKEN` (xoxb-...)
- `LINEAR_API_KEY` (Linear personal API key)
- `DB_PATH` (optional; defaults to `./chipin.json`)

### For Cloud Run / Webhook Mode (implemented)

- `ENVIRONMENT=gcp`
- `PORT` (optional; defaults to `8080` — Cloud Run provides this automatically)
- `SLACK_BOT_TOKEN` (xoxb-...)
- `SLACK_SIGNING_SECRET` (verifies inbound requests; from the Slack app's Basic
  Information page)
- `LINEAR_API_KEY`
- `GCP_PROJECT_ID` (Firestore project)
- `FIRESTORE_DATABASE_ID` (optional; defaults to `"(default)"`)

Point the Slack app's Slash Command and Interactivity request URLs at
`https://<cloud-run-url>/slack/commands` and `.../slack/interactions`
respectively. Socket Mode must be *disabled* for this mode (Slack treats
Socket Mode and HTTP request URLs as mutually exclusive per app). A `GET
/healthz` endpoint is included for Cloud Run's startup/liveness probes.

## 5. Linear OAuth App

ChipIn supports three ways to authenticate to Linear, all behind the same
`linear.TokenSource` interface (`internal/linear/oauth.go`) so `handler.LinearAPI`
never needs to know which one is active:

- **Personal API key** (`LINEAR_API_KEY`) — implemented from the start. Sent
  as-is in the `Authorization` header (`staticToken`, no refresh, no expiry).
  Still the zero-config path for local/dev.
- **OAuth2 client_credentials** (`LINEAR_CLIENT_ID` + `LINEAR_CLIENT_SECRET`) —
  **implemented.** Chosen over the authorization_code flow as the primary OAuth
  path because it needs no redirect/callback endpoint, no persisted refresh
  token, and works identically in both Mode A and Mode B — client_credentials
  is exactly the grant Linear's own docs recommend for "a user-initiated OAuth
  flow involving refresh tokens" being impractical, which describes a
  long-running headless service like ChipIn. `ClientCredentialsTokenSource`
  mints an access token good for ~30 days, caches it in memory, and refetches
  it when within 5 minutes of expiry or after a 401 (`Client.do` retries once).
  **Caveat:** tokens from this grant only see the workspace's *public* Linear
  teams — not private ones.
- **Authorization code + refresh token** (planned) — needed only for private
  Linear teams, since that's the one thing client_credentials can't reach.
  Requires a public HTTPS callback, so it only makes sense in Mode B: redirect
  an admin to `https://linear.app/oauth/authorize` (`response_type=code`,
  `scope=read,write`, a CSRF `state`), exchange the returned `code` at
  `https://api.linear.app/oauth/token`, then persist the access + refresh
  token pair — Linear **rotates the refresh token on every use**, so the new
  one must be re-saved each time or the chain breaks. Access tokens last 24h.
  Slots into the existing `TokenSource` interface without touching `Client`
  again; the only new work is the HTTP install/callback pair and the token
  store. Not yet built — nothing in this codebase has a per-workspace concept
  to key the stored token against, so building this now would mean
  speculating about a multi-tenant shape nothing else here needs yet.

### Creating the Linear OAuth2 application (manual, one-time)

1. Go to <https://linear.app/settings/api/applications/new> and create an app.
2. Set a redirect URI. For client_credentials this is never actually used (no
   redirect happens), but Linear's form requires one — any placeholder
   (e.g. `https://example.com/unused`) is fine until the authorization_code
   flow above is built, at which point it needs to be the real callback URL.
3. **Settings → API → your app → toggle on "Client credentials tokens".**
   This is required — without it, the `client_credentials` grant is rejected
   with "Client does not support the client_credentials grant type", and it's
   not implied by just registering the app.
4. Copy the **Client ID** and **Client Secret** → `LINEAR_CLIENT_ID` /
   `LINEAR_CLIENT_SECRET`.
5. Do **not** also set `LINEAR_API_KEY` — `config.Load` rejects having both a
   personal key and client credentials configured at once (ambiguous which
   one should win), and having only one of `LINEAR_CLIENT_ID`/`_SECRET` set
   (required: both together).

## 6. Issue Context

Every surface where a user encounters a round should carry a path back to its

source, so nobody has to hunt for "which issue was this again?" or "where's the
actual vote".

**Implemented:**

- The vote message header hyperlinks the issue identifier to the issue's Linear
  URL (`Issue.url` from the GraphQL API, persisted as `PokerSession.IssueURL`)
  and attributes the round to whoever ran `/chipin` (`Started by <@RequestedBy>`,
  persisted as `PokerSession.RequestedBy`).
- Private vote/retract confirmations (`chat.postEphemeral`) are prefixed with the
  issue key, hyperlinked to the vote message via its Slack permalink
  (`chat.getPermalink`, fetched once when the round starts and persisted as
  `PokerSession.MessageLink`) when available — `<permalink|IDENTIFIER>: <body>`.
  Permalink lookup is best-effort: if it fails, notices fall back to a plain
  `*<identifier>*:` prefix rather than blocking the round from starting.
  **Caveat:** `chat.postEphemeral` documents no `unfurl_links`/`unfurl_media`
  params at all (unlike `chat.postMessage`), and Slack's classic-unfurl docs
  scope automatic unfurling to `chat.postMessage`/incoming webhooks only —
  strong circumstantial evidence (not a written guarantee) that ephemeral
  messages aren't part of that pipeline, i.e. this link shouldn't trigger an
  unfurled preview. If that assumption is ever wrong in practice, drop the
  hyperlink in `privateNotice` (one-line change).

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
  `MessageLink`, which are read again on every re-render/notice).

## 7. Build, Release & CI (implemented)

- **Toolchain** pinned via `mise` (`mise.toml`): Go, `golangci-lint`, GoReleaser, and
  the `gcloud` CLI (+ Firestore emulator component).
- **CI** (`.github/workflows/ci.yml`), on every PR and push to `main`: `golangci-lint`
  (v2, `.golangci.yaml`: gofumpt/gci formatters + errcheck/govet/staticcheck/gosec/
  revive/…) and `go test ./...` against a real Firestore emulator (started in-job via
  `google-github-actions/setup-gcloud`'s `cloud-firestore-emulator` component).
- **Release** (`.github/workflows/release.yml` + `.goreleaser.yaml`), gated on CI
  passing on `main`:
  1. [release-please](https://github.com/googleapis/release-please) maintains a
     standing "release PR" from Conventional Commits since the last release (`fix:`
     → patch, `feat:` → minor, `!`/`BREAKING CHANGE:` → major; `chore:`/`docs:`/`ci:`
     -only changes get no PR). Merging that PR is what cuts a release — tag, GitHub
     Release, and generated `CHANGELOG.md`.
  2. GoReleaser then attaches artifacts to that same release (`release.mode:
     keep-existing` — it doesn't touch release-please's notes): static
     `CGO_ENABLED=0` binaries for linux/darwin/windows × amd64/arm64, uploaded as raw
     executables (`archives: formats: [binary]`, named `chipin_<os>_<arch>` — no
     version in the filename, no tarball/zip wrapper; a single static binary doesn't
     need one, and the version-free name is friendlier to tools like `mise`/aqua that
     resolve a fixed filename per release tag) plus `checksums.txt`, and a multi-arch
     (`linux/amd64` + `linux/arm64`) container image pushed to
     `ghcr.io/laluowen/chipin`. Because the Go binaries are cross-compiled ahead of
     time, the image build needs no QEMU-emulated compiler — only `docker buildx`
     assembling trivial per-arch COPY layers (QEMU is still set up in CI for the
     arm64 manifest itself).
- Every action in both workflows is pinned by commit SHA with a version comment
  (`uses: owner/repo@<sha> # vX.Y.Z`); Renovate's `config:best-practices` preset
  (`helpers:pinGitHubActionDigests`) keeps them updated.
