# PLAN.md: Self-Hosted Async Planning Poker

## 1. System Architecture

A single, statically compiled Go binary capable of operating in two distinct deployment modes. The application abstracts its storage and transport layers to allow seamless transition between local testing, GCP Cloud Run, and AWS Lambda without rewriting core business logic.

### 1.1 Core Stack

- **Language:** Go (Golang 1.21+)
- **Base Container Image:** `gcr.io/distroless/static-debian13:nonroot`
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

      **Re-checked (ALIGN-WITH-PROPSY.md §6):** Propsy hit this limit for real
      — its Home tab refresh needed up to 11 sequential Slack calls on the 3s
      path, and the fix was to stop refreshing it live rather than add
      concurrency. ChipIn's handlers don't have an equivalent: the worst case
      is `HandleSlashCommand` (Linear `FetchIssue`, Slack `PostMessage`, a
      best-effort `GetPermalink`, one store save — 4 calls) and `finalize`
      (Linear `SetEstimate`, one store delete, Slack `UpdateMessage` — 3
      calls), both a single straight-line sequence with no goroutines or
      deferred work spawned past the response. Nothing changed here; this is
      a confirmation, not a fix.

### 1.3 Firestore cost (budget)

Firestore's free tier (50k reads / 20k writes per day, 1 GiB — only the `(default)`
database; `config.Config.Warnings` flags a named `FIRESTORE_DATABASE_ID`, see §4)
comfortably covers ChipIn's access pattern, which is far lighter than a typical
multi-user app's: every operation touches exactly one document, the session keyed by
the Linear issue UUID.

- **Start a round** (`/chipin ENG-123`): 1 write (`SaveSession`).
- **Cast or retract a vote, reveal, or continue voting**: 1 read + 1 write
  (`saveAndRender`'s `GetSession` + `SaveSession`) each.
- **Set the estimate, or cancel**: 1 read + 1 delete (`GetSession` +
  `DeleteSession`).

A full round with N voters — one start, N votes, one reveal, one set-estimate — costs
roughly `(N + 2)` reads and `(N + 2)` writes/deletes. For a 10-person team, that's
~12 reads and ~12 writes per round: the daily free tier covers roughly 4,000 such
rounds on reads alone and ~1,600 on writes (the tighter bound), i.e. far more
planning-poker rounds than any one team runs in a day. An idle ChipIn costs $0.

This is deliberately not re-litigating Firestore vs. alternatives (Datastore mode,
Cloud Storage, SQLite-on-Cloud-Run, Cloud SQL/AlloyDB/Spanner/Bigtable/Memorystore):
see Propsy's `PLAN.md` §3 for that review, which applies unchanged — ChipIn's access
pattern is lighter, not differently shaped, so the verdict (Firestore `(default)`
database) carries over directly.

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

- **Toolchain** pinned via `mise` (`mise.toml`): Go, `golangci-lint`, GoReleaser, and Java
  (for the Firestore emulator; see `firestore.sh`).
- **CI** (`.github/workflows/ci.yml`), on every PR and push to `main`: `golangci-lint`
  (v2, `.golangci.yaml`: gofumpt/gci formatters + errcheck/govet/staticcheck/gosec/
  revive/…) and `go test ./...` against a real Firestore emulator, started in-job by
  `./firestore.sh` (no `gcloud`: the emulator is just a jar, downloaded directly and
  verified by sha256 — see "Local storage" §2.1 and `firestore.sh`'s own comments).
- **Release** (`.github/workflows/release.yml` + `.goreleaser.yaml`), gated on CI
  passing on `main`, and built to work with GitHub **immutable releases** (not yet
  enforced for this repo — `laluowen/ChipIn` is a personal account, and org-level
  rulesets only apply after the move in §9 — but the pipeline is written to the
  immutable shape regardless, since it also works fine without it; immutability can
  optionally be turned on for this repo alone first, under Settings → General →
  Releases):
  1. [release-please](https://github.com/googleapis/release-please) maintains a
     standing "release PR" from Conventional Commits since the last release (`fix:`
     → patch, `feat:` → minor, `!`/`BREAKING CHANGE:` → major; `chore:`/`docs:`/`ci:`
     -only changes get no PR). Merging that PR is what cuts a release. Because a
     published immutable release's assets and tag are frozen, release-please only
     creates a **draft** release with its tag already pushed
     (`release-please-config.json`: `"draft": true` plus `"force-tag-creation": true`
     — without the latter GitHub withholds a draft's tag until publish, so
     release-please can't see the previous release and proposes a bogus next-major
     release). `release-please-config.json`/`.release-please-manifest.json` replace
     the action's old bare `release-type: go` input, and the manifest is pinned at
     the version current when this pipeline was built (`1.0.2`) so release-please
     doesn't try to infer a version from the whole history.
  2. GoReleaser then builds everything, signs it, and uploads it all to that draft
     (`release.use_existing_draft`), then publishes it (`draft: false`) — only then
     does the release become immutable. A failure before the publish leaves an
     unsealed draft: fix it and re-run the failed job (the release-please outputs
     are reused, so it gets the same tag; `replace_existing_artifacts` covers a
     partial upload). `release.preflight.fail_on_error` stops the run early if the
     tag is already published as an immutable release, instead of discovering that
     after building everything.

  What GoReleaser attaches: static `CGO_ENABLED=0` binaries for linux/darwin/windows
  × amd64/arm64, uploaded as raw executables (`archives: formats: [binary]`, named
  `chipin_<os>_<arch>` — no version in the filename, no tarball/zip wrapper; a
  single static binary doesn't need one, and the version-free name is friendlier to
  tools like `mise`/aqua that resolve a fixed filename per release tag) plus
  `checksums.txt`, a multi-arch (`linux/amd64` + `linux/arm64`) container image
  pushed to `ghcr.io/laluowen/chipin` as an **exact version tag only** (no `latest`,
  no floating `1`/`1.2` — a moving tag isn't immutable; ghcr has no tag-immutability
  setting of its own, so this is by convention, backed by the cosign signature
  below), and per-binary SBOMs (Syft; `sboms: artifacts: binary`, since the default
  only catalogs archives, and raw binaries aren't "archives" to GoReleaser). Because
  the Go binaries are cross-compiled ahead of time and the Dockerfile has no `RUN`
  steps, the image build needs no QEMU-emulated compiler — just `docker buildx`
  assembling trivial per-arch `COPY` layers on one amd64 runner.

  **Supply chain: keyless signing, no keys or secrets.** Everything GoReleaser
  produces is signed with [cosign](https://github.com/sigstore/cosign) using
  Sigstore keyless signing: the release job's GitHub OIDC token is exchanged for a
  short-lived certificate naming this repo's `release.yml`, and the signature is
  logged in the public Rekor transparency log. Binaries, `checksums.txt` and the
  SBOMs each get a `.sigstore.json` bundle (`signs`); the container image is signed
  by digest (`docker_signs`). Build provenance (SLSA) attestations
  (`actions/attest`, over `checksums.txt` and the image digests in `digests.txt`)
  complement the signatures: a signature says who published an artifact, provenance
  says which workflow run built it from which commit. Verifiable with
  `cosign verify-blob`/`cosign verify` and `gh attestation verify` — see README
  "Verifying a release". The release workflow runs on `workflow_run`, so the
  certificate's workflow ref is the default branch (`…/release.yml@refs/heads/main`),
  not the tag: verification must match the repo + workflow path by regexp, not pin a
  ref. Signing/SBOM generation need a GitHub OIDC token, so a local snapshot build
  uses `goreleaser release --snapshot --clean --skip=sign,sbom` (add `docker` too if
  the machine has no buildx).
  - The release job's build cache (`actions/cache`, keyed per commit with
    restore-keys) is separate from `setup-go`'s: cross-compiling six targets
    recompiles grpc/protobuf/Firestore once per target (~115 s cold), and
    `setup-go`'s cache is saved once per `go.sum` from whichever job gets there
    first (CI's host-only build), so it would never hold the other five targets.
  - A `Verify CI for release commit` step polls the GitHub API until CI has reported
    success for the exact commit release-please tagged, before GoReleaser runs —
    belt-and-braces alongside the `workflow_run: branches: [main]` gate.
- Every action in both workflows is pinned by commit SHA with a version comment
  (`uses: owner/repo@<sha> # vX.Y.Z`); Renovate's `config:best-practices` preset
  (`helpers:pinGitHubActionDigests`) keeps them updated.
