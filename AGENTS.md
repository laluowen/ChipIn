# chipin — Agent/Developer Notes

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
- **Vote values are strings**, not ints (PLAN.md originally said `map[string]int`).
  `"?"` is a valid vote; consensus ignores non-numeric votes. See `PLAN.md` §2.1 note.
- **Reveal is a toggle**, not a teardown. `voting ⇄ revealed`. Only "Set estimate"
  writes to Linear and deletes the session.

## Slack app scopes (Socket Mode)

Bot token needs: `commands`, `chat:write`. App-level token needs `connections:write`.
Enable Socket Mode, add a `/poker` slash command, and enable Interactivity.

## Env vars (local)

`SLACK_APP_TOKEN` (xapp-), `SLACK_BOT_TOKEN` (xoxb-), `LINEAR_API_KEY`, optional `DB_PATH`.
