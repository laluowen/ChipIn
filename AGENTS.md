# ChipIn — Agent/Developer Notes

## What this is

Self-hosted async planning poker for Slack + Linear. A single static Go binary
that runs in either of two modes, selected by `ENVIRONMENT` (`local` | `gcp`):
**Mode A** — local Slack Socket Mode + SQLite + a Linear personal API key —
and **Mode B** — Cloud Run HTTP webhooks + Firestore. Both are implemented;
see `PLAN.md`. The Linear OAuth app (§5) and CI/release pipeline (§7) are
still planned.

## Layout

- `cmd/chipin` — entrypoint; `run()` branches on `ENVIRONMENT` into
  `runSocket` (Mode A: config → SQLite → slack/linear clients → handler →
  socket runner) or `runWebhook` (Mode B: config → Firestore → slack/linear
  clients → handler → webhook server).
- `internal/poker` — pure domain logic (vote scale, consensus). No I/O. Test aggressively.
- `internal/store` — `SessionRepository` interface + types; `SQLite` (pure-Go driver, Mode A)
  and `Firestore` (Mode B) implementations.
- `internal/linear` — minimal GraphQL client (fetch issue by identifier, set estimate).
- `internal/handler` — transport-agnostic game logic + Block Kit builders. The core.
- `internal/socket` — Socket Mode receive loop; adapts Slack events onto the handler.
- `internal/webhook` — HTTP webhook server (signature-verified slash commands +
  interactions); adapts Slack HTTP requests onto the same handler.
- `internal/config` — env loading; `Config.Environment` picks which of the
  above two wire up.

## Testing

- Run `go test ./...` after every non-trivial change. Everything except the socket
  loop, webhook HTTP wiring smoke tests, and `main` is unit-tested.
- `internal/store` SQLite tests use `:memory:` — no fixtures, no cleanup files.
  Firestore tests are gated behind `FIRESTORE_EMULATOR_HOST`
  (`gcloud emulators firestore start --host-port=localhost:8219`) and
  auto-skip when it's unset — no live Firestore calls.
- `internal/linear` tests use `httptest.Server` — no live Linear calls.
- `internal/handler` tests use fake `SlackAPI`/`LinearAPI` + in-memory store. The
  handler depends on narrow interfaces precisely so it stays testable without live
  services. Keep it that way: do not import `*slack.Client` concretely into logic.
- `internal/webhook` tests construct a real `handler.PokerHandler` over fakes +
  `:memory:` SQLite, and drive it through `httptest` with hand-computed Slack
  signatures — no live Slack calls.
- There are no integration tests against live Slack/Linear yet. If added, gate them
  behind `testing.Short()` and an env var, following the opqr pattern.

## Key design decisions

- **Pure-Go SQLite** (`modernc.org/sqlite`), NOT CGO. This keeps `CGO_ENABLED=0`
  builds and a distroless static binary with zero cross-compile pain on free
  GitHub runners. Do not switch to `mattn/go-sqlite3` — it reintroduces the CGO/zig
  toolchain dance.
- **Interfaces at the boundary.** `handler.SlackAPI` and `handler.LinearAPI` are the
  minimal subsets the handler needs; the real clients satisfy them as drop-ins.
  `handler.UpdateMessage` MUST keep the 4-return signature to match `*slack.Client`.
- **Session identity.** Sessions are keyed by the Linear issue UUID. Interactions
  carry that UUID in every button's `Value`; the vote label rides in the `action_id`
  as `vote:<label>`. This is how a click routes back to its session.
- **The scale comes from Linear, per team.** `poker.ScaleFor` builds the vote scale
  from the team's `issueEstimationType` / `issueEstimationExtended` /
  `issueEstimationAllowZero` (fetched in `linear.FetchIssue`). A `poker.Point` has a
  display `Label` ("5" or "M") and a numeric `Value` written to Linear. T-shirt sizes
  map to Fibonacci numbers. The scale is persisted on the session (SQLite `scale`
  column) so buttons, validation, and consensus all agree. There is no hard-coded
  scale and no `"?"` card — an abstention is just a missing vote.
- **Consensus is the mode, not the median.** `Scale.Consensus` picks the most-voted
  point, so the suggestion is always a value someone actually cast — a median can
  snap to a value nobody voted for once mapped onto a non-linear scale. Ties
  (including "every vote is different") break toward the higher point.
- **The final estimate is editable.** The revealed view has a `static_select`
  prefilled to the consensus. "Set estimate" writes whatever is currently selected
  (read from `InteractionCallback.BlockActionState`), falling back to the consensus.
- **Reveal is a toggle**, not a teardown. `voting ⇄ revealed`. "Set estimate" writes to
  Linear and deletes the session; "Cancel" deletes without writing.
- **Private notices are plain `chat.postEphemeral`, one per vote.** A DM-based
  "one message, updated in place" design was tried and reverted (too many moving
  parts: `conversations.open`, a persisted per-user `{ChannelID, MessageTS}`
  reference, stale-message fallback logic — not worth it). Ephemeral messages
  genuinely cannot be updated or deduped by Slack, so each vote/retract does post
  a new one; that's accepted as the simpler tradeoff.
- **Context links both ways, compactly.** The vote message header hyperlinks the
  issue identifier to Linear (`Issue.url`, persisted as `PokerSession.IssueURL`)
  and attributes the round to whoever ran `/chipin` (`Started by <@RequestedBy>`).
  Private notices are prefixed with the issue key, hyperlinked to the vote
  message's Slack permalink (`chat.getPermalink`, fetched once at round start,
  persisted as `PokerSession.MessageLink`) when available. **Caveat:**
  `chat.postEphemeral` documents no `unfurl_links`/`unfurl_media` params at all
  (unlike `chat.postMessage`), and Slack's classic-unfurl docs scope automatic
  unfurling to `chat.postMessage`/incoming webhooks only — strong circumstantial
  evidence ephemeral messages aren't part of that pipeline, but not a guarantee
  in writing. If a live ephemeral notice ever shows an unfurled preview of the
  linked message, strip the hyperlink in `privateNotice` (one-line change) and
  fall back to a plain `*<identifier>*:` prefix. Threading the issue
  description/attributes under the vote message is a separate planned feature,
  not built — see `PLAN.md` §6.

## Slack app scopes (Socket Mode)

Bot token needs: `commands`, `chat:write`. App-level token needs `connections:write`.
Enable Socket Mode, add a `/chipin` slash command, and enable Interactivity.
(`chat.getPermalink`, used for notice context, requires no scopes at all.)

## Env vars (local)

`SLACK_APP_TOKEN` (xapp-), `SLACK_BOT_TOKEN` (xoxb-), `LINEAR_API_KEY`, optional `DB_PATH`.
