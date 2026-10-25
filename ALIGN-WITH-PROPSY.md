# Align ChipIn with Propsy

ChipIn is the older sibling of Propsy (`~/Documents/git/laluowen/Propsy`, https://github.com/lalu-uk/Propsy).
Propsy started as a copy of ChipIn, then moved on in a number of ways. This file lists what to bring back,
in a sensible order, so both projects share one setup: git workflow, release pipeline, local storage, CI
and docs.

**Look at the Propsy directory on disk for how each piece is done**; it's the golden standard. Read its
`AGENTS.md`, `PLAN.md` and `CONTRIBUTING.md` first. Where this file names a Propsy PR, `gh pr view <n> -R
lalu-uk/Propsy` and `git -C ~/Documents/git/laluowen/Propsy show <commit>` show the exact change.

**Where this is heading.** ChipIn is going to move into the `lalu-uk` org, and this work is the first step
that makes the move possible: bring it up to the standard everything else in the org meets, so the transfer
(section 9) is a small, mechanical change. It may also be adapted to use Jira instead of Linear at some
point (section 10), which is another reason to be at the common standard first.

Don't copy anything Propsy-specific (names, the propsy/dropsy domain, Slack manifests' commands, leaderboards,
rate limits). ChipIn keeps its own domain: planning poker, Linear, sessions.

## Ground rules

- ChipIn has published releases (latest `v1.0.2`). There's no known deployment today, but treat it as having
  real users anyway, as in Propsy: breaking changes
  (an env var or default changing, `:latest` disappearing, data format changes) get a `!` after the commit
  type or a `BREAKING CHANGE:` footer, and the docs say what to do. Never silently discard data. Keep an
  old env var name working, with a warning, for a release when renaming one.
- Read the global agent instructions (`~/.agents/AGENTS.md`). In short: commit with
  `AGENT_MODEL="<provider>/<model>" git agent-commit …`, push to the **`gh`** remote (HTTPS, already added
  here), never touch the SSH `origin`, work on a branch and open a PR with `gh pr create`. Use Conventional
  Commits: releases are generated from them.
- One branch and PR per numbered section below, in order, unless noted. Run `go test -race ./...` and
  `golangci-lint run` before every push. A push that changes `.github/workflows/` needs the `workflow` token
  scope (the `gh` token has it).
- Repo owner differs for now: ChipIn is `laluowen/ChipIn` (a personal account), Propsy is `lalu-uk/Propsy`
  (an org). Org-level things (immutable releases, rulesets, the inherited Renovate config) don't apply
  until the move in section 9, so the work below must be correct both before and after it.

## 1. Plumbing and docs

- Add `CONTRIBUTING.md` (see Propsy's: setup, PR rules, Conventional Commits, the optional AI-assisted
  commits section with the `agent-commit` alias).
- Add a short "Committing (agents)" section to `AGENTS.md` (Propsy's wording: assume the `agent-commit`
  alias exists, set `AGENT_MODEL`, use the `gh` remote, open a PR) and a "Compatibility" section (treat as
  having real users, as above).
- `.env.example` (ChipIn has none) and a `.env` fallback loader: `internal/config/dotenv.go` and its tests,
  called at the start of `config.Load`. Real environment variables win; empty values are ignored. Mirror
  Propsy's `.env.example` structure and comments, with ChipIn's variables (`SLACK_*`, `LINEAR_*`, `DB_PATH`,
  `GCP_PROJECT_ID`, ...).
- Add `Warnings []string` to `Config` (logged by `main`) and the named-Firestore-database warning
  (`FIRESTORE_DATABASE_ID` other than `(default)` forfeits the free tier). Propsy: `internal/config/config.go`.
- Tests: add `-race` to what you run (and to docs); add the `gosec` `//nolint` conventions Propsy uses only
  where lint actually asks.

## 2. Local storage: SQLite becomes a JSON file (same choice as Propsy)

Why: Firestore is production, so SQLite is a second data model to keep in step with it. `local` mode is one
process, so it doesn't need what SQLite is for. Drops `modernc.org/sqlite` and ~30 packages (~6 MB off the
binary). Propsy PR #22; read `internal/store/jsonfile.go` and `jsonfile_test.go`, and `PLAN.md` §3 "Local
storage".

For ChipIn's `SessionRepository` (`GetSession`, `SaveSession`, `DeleteSession`, `Close`):

- Add `store.JSONFile`: all sessions in memory in a `map[issueID]*PokerSession` (mirror the Firestore
  `chipin_sessions` collection), a `version` field, saved by writing a temp file, fsyncing and renaming over
  the old one. Apply a change to a copy and swap it in only after the save succeeds, so a failed save changes
  nothing. Return copies from `GetSession` (the `Votes` map and `Scale` slice are mutable). `NewJSONFile("")`
  keeps everything in memory for tests. Add `json` tags to `PokerSession` next to the `firestore` ones.
- Refuse, with a clear error, a file that's corrupt, has an unknown `version`, or is an old SQLite
  database (header `SQLite format 3\x00`): say what to do, don't overwrite it. Sessions are short-lived (a
  session is deleted when its estimate is set), so there's no migration; say so in the release notes.
- `DB_PATH` default becomes `./chipin.json`. Document it. One process per file; there is no lock.
- Replace `sqlite_test.go` and `firestore_test.go` with one **contract test** (`store_test.go`) run against
  the in-memory JSON store, a file-backed one and Firestore (skipped without `FIRESTORE_EMULATOR_HOST`), as
  Propsy does. The SQLite migration tests (`TestMigrate...`) go away with the code. Add the JSON-specific
  tests: reload from disk, bad files, failed save changes nothing, concurrent saves (`-race`).
- Update `handler_test.go` and `webhook_test.go` (`store.NewSQLite(":memory:")` becomes `store.NewJSONFile("")`),
  `cmd/chipin/main.go`, comments, README (Mode A table, `DB_PATH` example, "keeps a local SQLite file"),
  `AGENTS.md` (the "Pure-Go SQLite, NOT CGO" decision becomes the JSON-file decision), `PLAN.md`, and
  `.gitignore`/`.dockerignore` (ignore `chipin.json`; drop the `*.db` lines). `go mod tidy`.
- Commit type `feat(store)`, not breaking, unless the owner says otherwise.

## 3. CI: the Firestore emulator without gcloud, plus Renovate for it

CI installs the gcloud SDK (~24 s) only to run its Firestore emulator; nothing talks to GCP. Propsy PR #26.

- Replace `firestore.sh` with Propsy's: it downloads the pinned emulator jar, verifies its sha256, runs it
  with `java`, waits until it's healthy, and stops it after 3 h (`FIRESTORE_EMULATOR_TTL`). Change the file
  names (`chipin-firestore-emulator.*`, the cache dir `chipin`). Works under bash and zsh; keep the
  `localhost` (not `127.0.0.1`) macOS note.
- `ci.yml`: remove `setup-gcloud` and the hand-rolled start step; use `source ./firestore.sh start` and pass
  `FIRESTORE_EMULATOR_HOST` on via `$GITHUB_ENV`; add `firestore.sh` to the PR path filter. Expect the test
  job to drop from ~60 s to ~40 s.
- `mise.toml`: drop the gcloud tool and its postinstall. Update the setup docs that mention it.
- Renovate keeps the pinned jar current (Propsy PR #30): a custom datasource reading the latest published
  `firebase-tools`' `downloadableEmulatorInfo.json` via jsDelivr, and a regex manager matching the
  `EMULATOR_VERSION` / `EMULATOR_SHA256` lines as a pair. Copy the two blocks from Propsy's `renovate.json`,
  and keep those two lines adjacent in `firestore.sh`. Check it with `renovate-config-validator`.

## 4. Releases: immutable-friendly, signed, README-aware

Largest piece. Propsy PRs #11 (flow and signing), #13 (README versions, SBOM fix), #24 (build cache),
#25 (no QEMU). Read Propsy's `release.yml`, `.goreleaser.yaml`, `release-please-config.json`,
`.release-please-manifest.json`, and `PLAN.md` §5.

**Immutability.** Propsy's org enforces *immutable releases*; ChipIn will inherit that when it moves
(section 9). Until then it isn't enforced, so build the pipeline for the immutable flow now (it also works
without it), and optionally turn immutability on for this repo alone (Settings → General → Releases →
"Enable release immutability") to prove it before the move.

- `release-please-config.json` and `.release-please-manifest.json` replace the action's `release-type: go`
  input: `draft: true`, `force-tag-creation: true`, `include-component-in-tag: false`, `extra-files` for
  README.md. **Pin the manifest at the current release** (`gh release list`: `1.0.2`), or release-please
  will propose a wrong version from the whole history.
- `.goreleaser.yaml`: attach to the draft and publish it (`release.use_existing_draft: true`,
  `draft: false`, `replace_existing_artifacts`, `preflight.fail_on_error`), `sboms` with
  `artifacts: binary` (the default catalogs nothing for raw-binary archives) and named like the binaries,
  `signs` (cosign keyless, `artifacts: all`, `.sigstore.json` bundles), `docker_signs`, `docker_digest`.
- `release.yml`: workflow default `permissions: {}`, per-job permissions (the release job needs `id-token` and
  `attestations`), cosign and Syft installers, `actions/attest` over `checksums.txt` and `digests.txt`, the
  "verify CI for the release commit" step Propsy now has, the build cache (`setup-go` with `cache: false`
  plus `actions/cache` keyed per commit with `restore-keys`, ~115 s to ~15-25 s warm). Drop QEMU only if you
  keep Propsy's reasoning: the Dockerfile must stay free of `RUN` steps (add its warning comment).
  Check with `goreleaser check`, a local `goreleaser release --snapshot --clean --skip=sign,sbom,docker`, and
  `actionlint`/`action-validator`.
- **Remove the moving `latest` image tag** (and the `:latest` image examples in README, PLAN and the Cloud
  Run deploy command; the `...:latest` in `--set-secrets` is a Secret Manager version alias, leave it).
  ghcr can't enforce tag immutability, so the image is exact-version only. There's no known deployment, so
  this isn't breaking in practice: use `fix(release)` and say so in the PR. Cleaning the old `latest` tag
  from ghcr needs a throwaway-image trick plus a `delete:packages` scope (see the Propsy history); ask the
  owner before doing it.
- README: an Install section (mise `@latest` and an exact pin, raw binary, container `ghcr.io/laluowen/chipin:<version>`)
  and "Verifying a release" (cosign `verify-blob` and `verify` with the *regexp* identity: the workflow runs
  on `workflow_run`, so the certificate names `refs/heads/main`, never a tag; and `gh attestation verify`).
  Wrap every version in `<!-- x-release-please-start-version -->` / `<!-- x-release-please-end -->` blocks.
  Only the project's own semver may appear inside them. mise hides releases younger than 24 h from `@latest`
  (`minimum_release_age`), which the README should say.
- `AGENTS.md` and `PLAN.md`: replace the old release description with the new flow and its failure modes. A
  failed run before the publish leaves an unsealed draft: re-run the failed job. After the publish a version
  is permanent.
- Expect to burn a version or two the first time. Merge the first release PR and watch it.

## 5. Renovate

- The org's inherited Renovate config
  (`lalu-uk/devops-renovate`) requires Dependency Dashboard approval for everything except GitHub Actions.
  Override it in `renovate.json` with `"dependencyDashboardApproval": false`, as Propsy does (PR #27); leave
  the rate limits at Renovate's defaults. Do it now: it's harmless while the repo is personal, and it's
  needed the moment the repo moves (section 9).
- Keep `config:best-practices` (it pins actions by SHA). Add the emulator manager from section 3.
- Renovate PRs for modules, mise tools and the Docker digest then arrive by themselves, so expect a first
  burst; consider grouping if it's noisy.

## 6. Runtime hardening from Propsy's review

- **Bounded Socket Mode dispatch** (Propsy PRs #15 and #17, `internal/socket/socket.go` and
  `socket_test.go`): a bounded worker pool and queue instead of handling events inline, with an event left
  unacknowledged (so Slack retries it) when the queue is full. Port the pattern and tests; the events are
  different (slash command, interactivity).
- Re-check ChipIn's synchronous webhook handling against Slack's 3 s ack limit as Propsy did (Propsy dropped
  live Home refreshes for this reason): nothing slow should sit between receiving a request and responding.
- Firestore cost: Propsy's `PLAN.md` §3 has a budget and an alternatives review (free tier, why Firestore).
  Do the equivalent for ChipIn's access pattern (a few reads and writes per vote) and record it.

## 7. Also worth doing (not in the sections above)

- **Slack app manifests.** ChipIn has none. Propsy keeps `slack/manifest.socket.yaml` and
  `slack/manifest.http.yaml` (and a test keeping variants in step). Add the equivalents for ChipIn's
  `/chipin` command, scopes and interactivity, and link them from the README setup steps.
- **GitHub settings** (the owner does these, an agent can't): "Allow GitHub Actions to create and approve pull
  requests" (release-please needs it), and, until the move, optionally release immutability and a ruleset
  requiring a PR into `main` like the org has.
- `PLAN.md` hygiene: keep it the source of design decisions, updated in the same PR as the change, with the
  Mode A/B description, deployment notes and roadmap current.
- Dependency alignment: `go.mod` and `mise.toml` pins drift from Propsy's (Renovate will move them); the
  Go version in `go.mod` and the Java/Go/golangci-lint pins should match where it's cheap.
- Link rot: after each of the changes above, grep the docs for the old things (`gcloud`, `SQLite`,
  `chipin.db`, `:latest`, `release-type: go`).

## 8. README parity with Propsy

Do this alongside section 4 (the install/verify parts need the release flow). Both READMEs should read alike.
Canonical outline (Propsy's is the reference):

1. Title, intro, mode table.
2. **Install**: mise (`@latest` and an exact pin), binary, container image `ghcr.io/<owner>/chipin:<version>`.
3. **Configuration**: env-var table, pointing at `.env.example`.
4. Usage / the flow.
5. **Setup**: Create a Slack app (link the manifests), Authenticate to Linear, **Run locally (Socket Mode)**,
   **Run on Cloud Run (Webhook Mode)** (the current 2.1-2.7 steps, with the exact image version).
6. **Contributing**: dev commands (`go test -race ./...`, `golangci-lint run`, `source ./firestore.sh start`)
   and a link to `CONTRIBUTING.md`. (Replaces "Development / Testing".)
7. **Releases**, with **Verifying a release** beneath it.

Every version in install and run examples (mise pin, binary URL, image tag, verification examples, the Cloud
Run `--image`) sits inside `<!-- x-release-please-start-version -->` / `<!-- x-release-please-end -->` so
release-please bumps it; `README.md` goes in `extra-files`. Only ChipIn's own semver in those blocks.

## 9. Move into `lalu-uk`

After sections 1-8 are merged. Transfer the repo (owner), then in one PR:

- Module path `github.com/laluowen/ChipIn` to `github.com/lalu-uk/ChipIn` (imports, `go.mod`, ldflags).
- Lowercase GHCR path `ghcr.io/lalu-uk/chipin` (Propsy needed `fix(release): lowercase docker image repository
  path`). The old `ghcr.io/laluowen/chipin` images stay where they are.
- cosign identity regexps, README/PLAN/CHANGELOG links, git remote URLs (`gh` and `origin`), the
  release-please and Renovate configs.
- Confirm the org's rulesets, immutable releases and the Renovate override (section 5) work; make the GHCR
  package public (it follows the repo's visibility in the org).

## 10. Possible Jira adapter (no work now)

ChipIn may later talk to Jira instead of Linear. Keep the tracker behind a small interface defined where it's
used (`handler.LinearAPI`), and avoid Linear types leaking into the handler, store or `PokerSession`.

## Gotchas learned the hard way (Propsy)

- Pin the release-please manifest to the current version. `force-tag-creation` is required with `draft`.
- GoReleaser's default SBOM step catalogs nothing for raw-binary archives; use `artifacts: binary`.
- Signing needs the CI OIDC token, so a local snapshot must `--skip=sign,sbom` (and `docker` without buildx).
- cosign verification identity: match the workflow path by regexp, never a ref.
- `setup-go`'s cache is saved once per `go.sum` from whichever job gets there first and never holds the
  cross-compiled targets; use a separate cache for the release job.
- Keep the `x-release-please-*` blocks free of other version-like numbers.
- In the `lalu-uk` org, ghcr packages can be public and take their repo's visibility, so a public repo
  gets a public image. (A personal account's new packages default to private.)
- The emulator script must be `source`d (it exports an env var); the jar's sha256 is checked, so a changed
  jar is refused.
- A new SQLite-to-JSON store must be tested against the same contract as Firestore, including failed saves.
