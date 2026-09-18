# ChipIn — Agent/Developer Notes

## What this is

Self-hosted async planning poker for Slack + Linear. A single static Go binary.
Currently implements **Mode A** only: local Slack Socket Mode + SQLite + a Linear
personal API key. Mode B (HTTP webhooks + Firestore) is planned but not built —
see `PLAN.md`.

## Layout

- `cmd/chipin` — entrypoint; wires config → store → slack/linear clients → handler → socket runner.
- `internal/poker` — pure domain logic (vote scale, consensus). No I/O. Test aggressively.
- `internal/store` — `SessionRepository` interface + types; `SQLite` implementation (pure-Go driver).
- `internal/linear` — minimal GraphQL client (fetch issue by identifier, set estimate).
- `internal/handler` — transport-agnostic game logic + Block Kit builders. The core.
- `internal/socket` — Socket Mode receive loop; adapts Slack events onto the handler.
- `internal/config` — env loading.

## Testing

- Run `go test ./...` after every non-trivial change. Everything except the socket
  loop and `main` is unit-tested.
- `internal/store` tests use `:memory:` SQLite — no fixtures, no cleanup files.
- `internal/linear` tests use `httptest.Server` — no live Linear calls.
- `internal/handler` tests use fake `SlackAPI`/`LinearAPI` + in-memory store. The
  handler depends on narrow interfaces precisely so it stays testable without live
  services. Keep it that way: do not import `*slack.Client` concretely into logic.
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
- **Private notices are a real DM message, updated in place.** Per-user confirmations
  (vote/retract) are NOT `chat.postEphemeral` (can't be updated — every vote would
  stack a new one) and NOT a `response_url` ephemeral either (confirmed in practice:
  posting to `response_url` does not replace a prior response from an earlier
  interaction payload — it just stacks too, since each interaction gets its own
  `response_url`). Instead: `sendPrivateNotice` opens (or resumes) a DM with the user
  via `conversations.open` once, remembers `{ChannelID, MessageTS}` on the session
  (`store.Notice`, keyed by user), and calls `chat.update` on that same message for
  every later notice. If the update fails (e.g. stale/deleted message) it falls back
  to opening a fresh DM and re-persists the new reference.
- **Context links both ways.** The vote message header hyperlinks the issue
  identifier to Linear (`Issue.url`, persisted as `PokerSession.IssueURL`). Private
  DM notices link back to the vote message via its Slack permalink
  (`chat.getPermalink`, fetched once at round start and persisted as
  `PokerSession.MessageLink`); permalink lookup is best-effort and never blocks
  starting a round. Threading the issue description/attributes under the vote
  message is planned but not built — see `PLAN.md` §6.

## Slack app scopes (Socket Mode)

Bot token needs: `commands`, `chat:write`, `im:write` (the last is required to open a
DM for private vote confirmations). App-level token needs `connections:write`.
Enable Socket Mode, add a `/chipin` slash command, and enable Interactivity.

## Env vars (local)

`SLACK_APP_TOKEN` (xapp-), `SLACK_BOT_TOKEN` (xoxb-), `LINEAR_API_KEY`, optional `DB_PATH`.
