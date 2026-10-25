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
| **A: Socket** | `local` (default) | Outbound WebSocket (Slack Socket Mode) | JSON file | A box you control, no public ingress needed |
| **B: Webhook** | `gcp` | HTTP (Slack sends requests in) | Firestore | Cloud Run, scale-to-zero |

See [`PLAN.md`](./PLAN.md) for the full design and [`AGENTS.md`](./AGENTS.md) for
architecture/layout notes.

## Install

Latest release: **v1.0.2** <!-- x-release-please-version -->

See the [releases page](https://github.com/laluowen/ChipIn/releases) for notes.

<!-- Everything between the start and end markers is bumped to each new version by release-please
     (see release-please-config.json). Keep other version-like numbers out of the marked blocks. -->

### mise

[mise](https://mise.jdx.dev) installs the binary straight from the GitHub release:

<!-- x-release-please-start-version -->
```sh
mise use -g github:laluowen/ChipIn@latest   # the newest release
mise use -g github:laluowen/ChipIn@1.0.2    # or pin this exact version
```
<!-- x-release-please-end -->

Then run `chipin`. By default mise holds releases back for 24 hours (`minimum_release_age`),
so `@latest` only picks a release up a day after it's published; pin the exact version to get
it straight away.

### Binary

<!-- x-release-please-start-version -->
```sh
# linux/amd64 shown; the other files are chipin_{linux,darwin,windows}_{amd64,arm64}[.exe]
curl -fsSLO https://github.com/laluowen/ChipIn/releases/download/v1.0.2/chipin_linux_amd64
chmod +x chipin_linux_amd64
```
<!-- x-release-please-end -->

### Container image

<!-- x-release-please-start-version -->
```sh
docker pull ghcr.io/laluowen/chipin:v1.0.2
```
<!-- x-release-please-end -->

There is no `latest` tag: always use an exact version. To check what you've downloaded, see
[Verifying a release](#verifying-a-release).

## Configuration

| Variable | Default | |
|---|---|---|
| `ENVIRONMENT` | `local` | Selects the transport and storage: `local` (Socket Mode + a JSON file) or `gcp` (HTTP webhooks + Firestore, Cloud Run). |
| `SLACK_BOT_TOKEN` | — | Bot token from OAuth & Permissions, after installing the app (`xoxb-...`). Both modes. |
| `LINEAR_API_KEY` | — | Linear personal API key. Set this **or** the two below — never both. |
| `LINEAR_CLIENT_ID` / `LINEAR_CLIENT_SECRET` | — | Linear OAuth2 `client_credentials` app, set as a pair. See "Authenticate to Linear" below. |
| `SLACK_APP_TOKEN` | — | Mode A only: app-level token with `connections:write`, from Basic Information → App-Level Tokens (`xapp-...`). |
| `DB_PATH` | `./chipin.json` | Mode A only: where the local JSON data file lives. One process per file. |
| `SLACK_SIGNING_SECRET` | — | Mode B only: verifies inbound Slack requests, from Basic Information → App Credentials. |
| `GCP_PROJECT_ID` | — | Mode B only: the GCP project hosting Firestore. |
| `FIRESTORE_DATABASE_ID` | `(default)` | Mode B only: keep it — Firestore's free quota only covers `(default)`, a named database bills every read and write (ChipIn warns at startup if you change this). |
| `PORT` | `8080` | Mode B only: Cloud Run sets this automatically. |

A fillable template with the same variables (and comments) lives in
[`.env.example`](./.env.example) — copy it to `.env` and ChipIn reads it at startup for
anything not already set in the real environment (real environment variables always win,
and `.env` is gitignored).

## The flow

1. `/chipin ENG-123` → ChipIn fetches the issue from Linear and posts a voting card using
   that team's own estimate scale (Fibonacci, exponential, linear, or T-shirt sizes).
2. Everyone clicks a card. Votes stay hidden while voting; you can change or retract
   your own vote any time.
3. **Reveal votes** → shows every vote and the recommended estimate.
4. From there: **Continue voting** (back to hidden voting for another pass), **Set
   estimate** (writes the chosen value to Linear and closes the round), or **Cancel**
   (closes the round without writing anything).

## 1. Create a Slack app

Both modes start the same way, at <https://api.slack.com/apps>:

- Create an app — fastest from a manifest:
  [`slack/manifest.socket.yaml`](./slack/manifest.socket.yaml) for Mode A,
  [`slack/manifest.http.yaml`](./slack/manifest.http.yaml) for Mode B (replace its
  placeholder `https://YOUR-SERVICE.run.app` with your Cloud Run URL once you have
  one). Manifests already set the bot scopes, slash command, and interactivity
  below, so skip straight to installing. Starting from scratch instead:
  - **OAuth & Permissions** → bot token scopes: `commands`, `chat:write`.
  - **Slash Commands** → add `/chipin`.
  - **Interactivity & Shortcuts** → turn it on.
- Install the app to your workspace → note the bot token (`xoxb-...`).

Where the two modes diverge is how Slack actually reaches ChipIn:

- **Mode A (Socket)** needs an **app-level token** instead of a public URL.
- **Mode B (Webhook)** needs Slack to call you over HTTP, so it needs a public
  **Request URL** and a **signing secret** to verify those requests are really from Slack.

The sections below cover each.

## 2. Authenticate to Linear

Two options — pick one, not both (ChipIn refuses to start if it sees both):

**Personal API key** (simplest, good for local/dev): `https://linear.app/settings/api`
→ create a personal API key → `LINEAR_API_KEY`.

**OAuth2 client_credentials** (recommended beyond solo/dev use — scoped,
revocable/rotatable independent of any one person's account):

1. <https://linear.app/settings/api/applications/new> → create an app.
2. Set a redirect URI — required by the form, but unused by this grant type; any
   placeholder (e.g. `https://example.com/unused`) works.
3. On the app's settings page, toggle on **"Client credentials tokens"**. Easy to
   miss — without it you'll get `Client does not support the client_credentials
   grant type` the first time ChipIn tries to fetch a token.
4. Copy the **Client ID** and **Client Secret** → `LINEAR_CLIENT_ID` /
   `LINEAR_CLIENT_SECRET` (both required together).

Note: client_credentials tokens only see the workspace's *public* Linear teams. If
your planning poker rounds live on a private team, use the personal API key for now
(an authorization_code-based flow for private-team access is planned — see `PLAN.md` §5).

Both modes use whichever you choose the same way.

## Run locally (Socket Mode)

Extra Slack setup, on top of §1:

- **Socket Mode** → enable it.
- **Basic Information** → **App-Level Tokens** → generate one with the
  `connections:write` scope → this is your `xapp-` token.

Configure and run (either export the variables, or copy [`.env.example`](./.env.example)
to `.env` and fill it in):

```sh
export ENVIRONMENT=local       # optional, this is the default
export SLACK_APP_TOKEN=xapp-...
export SLACK_BOT_TOKEN=xoxb-...
export LINEAR_API_KEY=lin_api_...      # or LINEAR_CLIENT_ID + LINEAR_CLIENT_SECRET, not both
export DB_PATH=./chipin.json   # optional, this is the default

go run ./cmd/chipin
```

Nothing inbound is required — the process dials out to Slack over a WebSocket and
keeps a local JSON file for in-progress sessions.

## Run on Cloud Run (Webhook Mode)

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

Cloud Run runs the public image straight from ghcr (use an exact version: there's no `latest`):

<!-- x-release-please-start-version -->
```sh
gcloud run deploy chipin \
  --project=YOUR_PROJECT \
  --region=YOUR_REGION \
  --image=ghcr.io/laluowen/chipin:v1.0.2 \
  --service-account="chipin@YOUR_PROJECT.iam.gserviceaccount.com" \
  --allow-unauthenticated \
  --set-env-vars="ENVIRONMENT=gcp,GCP_PROJECT_ID=YOUR_PROJECT,FIRESTORE_DATABASE_ID=chipin" \
  --set-secrets="SLACK_BOT_TOKEN=chipin-slack-bot-token:latest,LINEAR_API_KEY=chipin-linear-api-key:latest,SLACK_SIGNING_SECRET=chipin-slack-signing-secret:latest"
```
<!-- x-release-please-end -->

(The `:latest` in `--set-secrets` is a Secret Manager *version* alias, unrelated to
container image tags — it just means "the newest version of this secret".)

`--allow-unauthenticated` is required here — Slack calls this service directly and
can't present GCP credentials. Request authenticity is instead verified per-request
against `SLACK_SIGNING_SECRET` (see `internal/webhook`).

If using Linear OAuth client_credentials instead of a personal key (§2), create
`chipin-linear-client-id`/`chipin-linear-client-secret` secrets the same way as
§2.5–2.6, and swap the `LINEAR_API_KEY=...` entry above for
`LINEAR_CLIENT_ID=chipin-linear-client-id:latest,LINEAR_CLIENT_SECRET=chipin-linear-client-secret:latest`.

Now go back and set the Slack Request URLs above to this service's URL.

**Cold starts vs. Slack's 3s ack window:** requests are handled synchronously (the
handler runs to completion before the HTTP response — see `internal/webhook`'s doc
comment), and a cold start on a scaled-to-zero service eats into Slack's budget. If
that becomes a real problem, `gcloud run services update chipin --min-instances=1` is
the cheapest fix.

## Contributing

```sh
mise install                    # pinned toolchain
source ./firestore.sh start     # optional: enables the Firestore tests (needs Java; ~130 MB once; stops itself after 3h)
go test -race ./... && golangci-lint run
```

See [`CONTRIBUTING.md`](./CONTRIBUTING.md) for the contribution workflow.

## Releases

Releases: Conventional Commits → release-please PR → GoReleaser publishes binaries and a
versioned image, `ghcr.io/laluowen/chipin:vX.Y.Z`. There is deliberately no `latest` tag (or
floating `1` / `1.2`): pin an exact version, or better its digest.

### Verifying a release

Everything is signed with [cosign](https://github.com/sigstore/cosign) using Sigstore
**keyless** signing, so there are no keys to trust or rotate: the signature proves the artifact
was produced by this repository's release workflow. Each binary, `checksums.txt` and SBOM has
a `<name>.sigstore.json` bundle beside it on the release.

<!-- x-release-please-start-version -->
```sh
# A binary (swap in your platform's file name):
cosign verify-blob chipin_linux_amd64 \
  --bundle chipin_linux_amd64.sigstore.json \
  --certificate-identity-regexp '(?i)^https://github\.com/laluowen/chipin/\.github/workflows/release\.yml@' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com

# The container image (best: verify, then pin the digest it resolves to):
cosign verify ghcr.io/laluowen/chipin:v1.0.2 \
  --certificate-identity-regexp '(?i)^https://github\.com/laluowen/chipin/\.github/workflows/release\.yml@' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com

# Build provenance (which workflow run built it, from which commit):
gh attestation verify chipin_linux_amd64 --repo laluowen/ChipIn
gh attestation verify oci://ghcr.io/laluowen/chipin:v1.0.2 --repo laluowen/ChipIn
```
<!-- x-release-please-end -->

The identity is matched by regexp because the release workflow runs on `workflow_run`, so the
certificate names the branch it ran on, not the tag.

Releases are built to work with GitHub's immutable releases: release-please creates a draft
release (with its tag), GoReleaser attaches every asset to it, and only then publishes it, at
which point the assets and tag are frozen. A published version is permanent: a bad release is
fixed by cutting a new version, not by editing the old one.

To try a release build locally, skip the steps that need CI credentials:
`goreleaser release --snapshot --clean --skip=sign,sbom` (add `docker` without buildx).
