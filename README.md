# ChipIn

<p align="center">
  <img src="./logo.png">
</p>

Self-hosted **async planning poker** for Slack + Linear, as a single static Go binary.

Run `/chipin ENG-123` in Slack. ChipIn fetches the issue (and its team's estimate scale)
from Linear, posts a card-voting message, collects votes asynchronously, reveals a
recommended estimate (the most-voted card, not a median that can land on a value
nobody voted for), and — once you lock it in — writes that estimate back to the Linear
issue.

It runs in one of two modes, same binary, picked by the `ENVIRONMENT` variable:

| Mode | `ENVIRONMENT` | Transport | Storage | Suited to |
|---|---|---|---|---|
| **A: Socket** | `local` (default) | Outbound WebSocket (Slack Socket Mode) | SQLite file | A box you control, no public ingress needed |
| **B: Webhook** | `gcp` | HTTP (Slack sends requests in) | Firestore | Cloud Run, scale-to-zero |

See [`PLAN.md`](./PLAN.md) for the full design and [`AGENTS.md`](./AGENTS.md) for
architecture/layout notes. A full Linear OAuth app (vs. the personal API key below) is
still planned.

## The flow

1. `/chipin ENG-123` → ChipIn fetches the issue from Linear and posts a voting card using
   that team's own estimate scale (Fibonacci, exponential, linear, or T-shirt sizes).
2. Everyone clicks a card. Votes stay hidden while voting; you can change or retract
   your own vote any time.
3. **Reveal votes** → shows every vote and the recommended estimate.
4. From there: **Continue voting** (back to hidden voting for another pass), **Set
   estimate** (writes the chosen value to Linear and closes the round), or **Cancel**
   (closes the round without writing anything).

## Releases

Every merge to `main` updates a standing "release PR" (via
[release-please](https://github.com/googleapis/release-please)); merging *that* PR cuts
a release. A release publishes:

- A multi-arch (`linux/amd64` + `linux/arm64`) container image:

  ```
  ghcr.io/laluowen/chipin:latest
  ```

- Standalone binaries for Linux, macOS, and Windows (amd64 + arm64 each), named
  `chipin_<os>_<arch>` (`.exe` on Windows, no version in the filename — it's already in the
  release tag) and attached directly to the
  [GitHub Release](https://github.com/laluowen/ChipIn/releases) as raw executables, no
  tarball/zip wrapper, plus a `checksums.txt`.

Both are built by [GoReleaser](https://goreleaser.com) from `.goreleaser.yaml`. See
`.github/workflows/release.yml` for the exact pipeline, and `PLAN.md` §7 for the
reasoning. To build either locally without waiting on CI:

```sh
goreleaser release --snapshot --clean   # binaries + a locally-tagged image, no publish
```

## 1. Create a Slack app

Both modes start the same way, at <https://api.slack.com/apps>:

- Create an app.
- **OAuth & Permissions** → bot token scopes: `commands`, `chat:write`.
- **Slash Commands** → add `/chipin`.
- **Interactivity & Shortcuts** → turn it on.
- Install the app to your workspace → note the bot token (`xoxb-...`).

Where the two modes diverge is how Slack actually reaches ChipIn:

- **Mode A (Socket)** needs an **app-level token** instead of a public URL.
- **Mode B (Webhook)** needs Slack to call you over HTTP, so it needs a public
  **Request URL** and a **signing secret** to verify those requests are really from Slack.

The sections below cover each.

## 2. Get a Linear personal API key

`https://linear.app/settings/api` → create a personal API key. It needs permission to
read issues and update estimates. Both modes use this the same way.

## Mode A: Local / Socket Mode

Extra Slack setup, on top of §1:

- **Socket Mode** → enable it.
- **Basic Information** → **App-Level Tokens** → generate one with the
  `connections:write` scope → this is your `xapp-` token.

Configure and run:

```sh
export ENVIRONMENT=local       # optional, this is the default
export SLACK_APP_TOKEN=xapp-...
export SLACK_BOT_TOKEN=xoxb-...
export LINEAR_API_KEY=lin_api_...
export DB_PATH=./chipin.db     # optional, this is the default

go run ./cmd/chipin
```

Nothing inbound is required — the process dials out to Slack over a WebSocket and
keeps a local SQLite file for in-progress sessions.

## Mode B: GCP Cloud Run / Webhook Mode

Extra Slack setup, on top of §1 — these need your Cloud Run URL, so come back to them
once the service is deployed:

- **Socket Mode** → leave off (or turn off, if you flip a workspace between modes).
- **Basic Information** → copy the **Signing Secret**.
- **Slash Commands** → edit `/chipin`'s Request URL to
  `https://<cloud-run-url>/slack/commands`.
- **Interactivity & Shortcuts** → set the Request URL to
  `https://<cloud-run-url>/slack/interactions`.

The rest of this section is one-time GCP project setup. Replace `YOUR_PROJECT` and
`YOUR_REGION` throughout.

### 2.1 Enable the required APIs

```sh
gcloud services enable \
  run.googleapis.com \
  firestore.googleapis.com \
  secretmanager.googleapis.com \
  --project=YOUR_PROJECT
```

### 2.2 Create a Firestore database

A named database (not `(default)`) keeps this isolated from anything else in the
project:

```sh
gcloud firestore databases create \
  --project=YOUR_PROJECT \
  --database=chipin \
  --location=YOUR_REGION \
  --type=firestore-native
```

### 2.3 Create a service account

```sh
gcloud iam service-accounts create chipin \
  --project=YOUR_PROJECT \
  --display-name="ChipIn"
```

This gives you `chipin@YOUR_PROJECT.iam.gserviceaccount.com`.

### 2.4 Grant it access to the `chipin` Firestore database only

`roles/datastore.user`, scoped down to just the `chipin` database via an IAM condition
— so this service account can't touch any other Firestore database in the project:

```sh
gcloud projects add-iam-policy-binding YOUR_PROJECT \
  --member="serviceAccount:chipin@YOUR_PROJECT.iam.gserviceaccount.com" \
  --role="roles/datastore.user" \
  --condition='expression=resource.name == "projects/YOUR_PROJECT/databases/chipin",title=chipin-db-only'
```

### 2.5 Create the secrets

```sh
printf '%s' 'lin_api_...'    | gcloud secrets create chipin-linear-api-key       --data-file=- --project=YOUR_PROJECT
printf '%s' 'xoxb-...'       | gcloud secrets create chipin-slack-bot-token      --data-file=- --project=YOUR_PROJECT
printf '%s' 'your-signing-secret' | gcloud secrets create chipin-slack-signing-secret --data-file=- --project=YOUR_PROJECT
```

### 2.6 Let the service account read those secrets

```sh
for secret in chipin-linear-api-key chipin-slack-bot-token chipin-slack-signing-secret; do
  gcloud secrets add-iam-policy-binding "$secret" \
    --project=YOUR_PROJECT \
    --member="serviceAccount:chipin@YOUR_PROJECT.iam.gserviceaccount.com" \
    --role="roles/secretmanager.secretAccessor"
done
```

### 2.7 Deploy

```sh
gcloud run deploy chipin \
  --project=YOUR_PROJECT \
  --region=YOUR_REGION \
  --image=ghcr.io/laluowen/chipin:latest \
  --service-account="chipin@YOUR_PROJECT.iam.gserviceaccount.com" \
  --allow-unauthenticated \
  --set-env-vars="ENVIRONMENT=gcp,GCP_PROJECT_ID=YOUR_PROJECT,FIRESTORE_DATABASE_ID=chipin" \
  --set-secrets="SLACK_BOT_TOKEN=chipin-slack-bot-token:latest,LINEAR_API_KEY=chipin-linear-api-key:latest,SLACK_SIGNING_SECRET=chipin-slack-signing-secret:latest"
```

`--allow-unauthenticated` is required here — Slack calls this service directly and
can't present GCP credentials. Request authenticity is instead verified per-request
against `SLACK_SIGNING_SECRET` (see `internal/webhook`).

Now go back and set the Slack Request URLs above to this service's URL.

**Cold starts vs. Slack's 3s ack window:** requests are handled synchronously (the
handler runs to completion before the HTTP response — see `internal/webhook`'s doc
comment), and a cold start on a scaled-to-zero service eats into Slack's budget. If
that becomes a real problem, `gcloud run services update chipin --min-instances=1` is
the cheapest fix.

## Development / Testing

Toolchain is pinned with [mise](https://mise.jdx.dev) (`mise.toml`):

```sh
mise install
go test ./...          # unit tests; no live Slack/Linear/Firestore needed
golangci-lint run
```

Firestore-backed tests (`internal/store/firestore_test.go`) are gated behind
`FIRESTORE_EMULATOR_HOST` and auto-skip when it's unset. To actually exercise them,
start the bundled emulator wrapper first — it's a `source`d script because it needs to
set/unset `FIRESTORE_EMULATOR_HOST` in *your* shell, not a subshell that evaporates:

```sh
source ./firestore.sh start
go test ./internal/store/... -run Firestore -v
source ./firestore.sh stop
```

Requires the Firestore emulator component: `gcloud components install
cloud-firestore-emulator`.

See [`AGENTS.md`](./AGENTS.md) for architecture notes and design decisions.
