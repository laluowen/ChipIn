# ChipIn — Agent/Developer Notes

## What this is

Self-hosted async planning poker for Slack + Linear. A single static Go binary
that runs in either of two modes, selected by `ENVIRONMENT` (`local` | `gcp`):
**Mode A** — local Slack Socket Mode + a JSON data file + Linear auth — and
**Mode B** — Cloud Run HTTP webhooks + Firestore. Both are implemented; see
`PLAN.md`.

## Layout

- `cmd/chipin` — entrypoint; `run()` branches on `ENVIRONMENT` into
  `runSocket` (Mode A: config → JSON file → slack/linear clients → handler →
  socket runner) or `runWebhook` (Mode B: config → Firestore → slack/linear
  clients → handler → webhook server).
- `internal/poker` — pure domain logic (vote scale, consensus). No I/O. Test aggressively.
- `internal/store` — `SessionRepository` interface + types; `JSONFile` (in-memory,
  saved atomically to one file, Mode A) and `Firestore` (Mode B) implementations.
- `internal/linear` — minimal GraphQL client (fetch issue by identifier, set estimate).
- `internal/handler` — transport-agnostic game logic + Block Kit builders. The core.
- `internal/socket` — Socket Mode receive loop; adapts Slack events onto the handler.
- `internal/webhook` — HTTP webhook server (signature-verified slash commands +
  interactions); adapts Slack HTTP requests onto the same handler.
- `internal/config` — env loading; `Config.Environment` picks which of the
  above two wire up.

## Testing

- Run `go test -race ./...` after every non-trivial change. Everything except the
  socket loop, webhook HTTP wiring smoke tests, and `main` is unit-tested.
- `internal/store/store_test.go` holds one contract test run against every
  `SessionRepository`. Any new store method gets added there. The JSON file store
  runs in memory and on a temp file; Firestore runs only with
  `FIRESTORE_EMULATOR_HOST` (`source ./firestore.sh start`) and skips otherwise —
  never hit live GCP.
- `internal/linear` tests use `httptest.Server` — no live Linear calls.
- `internal/handler` tests use fake `SlackAPI`/`LinearAPI` + an in-memory
  `JSONFile`. The handler depends on narrow interfaces precisely so it stays
  testable without live services. Keep it that way: do not import
  `*slack.Client` concretely into logic.
- `internal/webhook` tests construct a real `handler.PokerHandler` over fakes +
  an in-memory `JSONFile`, and drive it through `httptest` with hand-computed
  Slack signatures — no live Slack calls.
- There are no integration tests against live Slack/Linear yet. If added, gate them
  behind `testing.Short()` and an env var, following the opqr pattern.

## Key design decisions

- **Local mode is a JSON file (`JSONFile`), not a database.** Firestore is the
  production store, so a local SQL database was a second data model to keep in
  step with it, and `local` mode runs as one process — it doesn't need what
  SQLite is for (transactions shared between processes). `JSONFile` keeps every
  in-progress session in memory, mirroring the Firestore `chipin_sessions`
  collection as a map keyed by issue ID. Saves are write-temp, fsync, rename; a
  failed save leaves memory untouched (`SaveSession`/`DeleteSession` build the
  new state on a copy and only swap it in once the write succeeds). One process
  per file: there is no lock, since the portable options are worse than the
  failure they'd prevent. `GetSession` returns a copy (the `Votes` map and
  `Scale` slice are mutable). An old SQLite file at `DB_PATH` is refused with an
  error that says so (recognised by its header) rather than silently ignored or
  overwritten — there's no migration, since a session is deleted the moment its
  estimate is set or the round is cancelled, so there's nothing worth carrying
  forward. Dropped `modernc.org/sqlite` and ~30 packages (~6 MB off the binary).
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
  map to Fibonacci numbers. The scale is persisted on the session itself so buttons,
  validation, and consensus all agree. There is no hard-coded
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

## Committing (agents)

This assumes the owner has a git alias `agent-commit` configured (see CONTRIBUTING.md, "AI-assisted
commits"). Commit with it, not plain `git commit`, setting the model on every commit:

```sh
AGENT_MODEL="<provider>/<model>" git agent-commit -m "type(scope): subject"
```

`AGENT_MODEL` is the model you're actually running as (provider/model ID, as in your system
prompt's model info). The alias takes the same arguments as `git commit`, signs with a dedicated
agent key, and adds an `Assisted-by: OpenCode <version> (<model>)` trailer. If the alias is missing,
don't fall back to plain `git commit` (signing may need the owner to touch a hardware key); ask.

If a `gh` remote exists, fetch and push through it rather than `origin`. Only commit or push when
asked, and open a PR instead of pushing to `main`.

## Releases

Built for GitHub **immutable releases** (not yet enforced for this repo — see PLAN.md §7 and §9 —
but the pipeline is written to that shape regardless, since it also works without it):
release-please makes a *draft* release with its tag already pushed
(`release-please-config.json`: `draft` + `force-tag-creation`), GoReleaser uploads every asset to
that draft (`release.use_existing_draft`) and then publishes it (`draft: false`); after that the
release can't change. Never add a step that modifies a release after the GoReleaser job, and never
create the release published. If the job fails before the publish, re-run the failed job.

The README's install instructions carry the current release version (mise pin, binary URL, image
tag, verification examples). release-please bumps them in each release PR: `release-please-config.json`
lists README.md under `extra-files`, and the lines to bump sit between
`<!-- x-release-please-start-version -->` and `<!-- x-release-please-end -->` (or on a line marked
`x-release-please-version`). Don't edit those versions by hand, and keep other version-like numbers
(Go versions and so on) out of the marked blocks: every semver-looking string in them is replaced.

Everything GoReleaser builds is signed keylessly with cosign (`signs`, `docker_signs`, plus Syft
SBOMs and `actions/attest` provenance; see `PLAN.md` §7 and the README's "Verifying a release").
Signing needs the CI OIDC token, so locally use `goreleaser release --snapshot --clean
--skip=sign,sbom` (add `docker` without buildx). Don't pin a tag in the verification identity: the
workflow runs on `workflow_run`. Container tags are exact versions only: never add `latest`.

## Compatibility

ChipIn has published releases (see `gh release list`). Treat it as having real users: a breaking
change (an env var or default changing, `:latest` disappearing, data format changes) needs a `!`
after the commit type or a `BREAKING CHANGE:` footer so release-please makes it a major release,
and the docs must say what to do. Data must never be silently discarded or overwritten: migrate
it, or refuse to start with an error that says what to do (as the JSON store does with an old
SQLite file). Keep an old env var name working, with a warning, for at least a release when
renaming one.

## Env vars (local)

`SLACK_APP_TOKEN` (xapp-), `SLACK_BOT_TOKEN` (xoxb-), `LINEAR_API_KEY` (or `LINEAR_CLIENT_ID` +
`LINEAR_CLIENT_SECRET`), optional `DB_PATH`. See `.env.example` for the full list and Mode B's
variables.
