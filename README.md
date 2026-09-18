# ChipIn

Self-hosted **async planning poker** for Slack + Linear, as a single static Go binary.

Run `/chipin ENG-123` in Slack. Chip posts a card-voting message, collects votes
asynchronously, reveals a recommended estimate (median of the votes, snapped to the
Fibonacci scale), and — once you lock it in — writes that estimate back to the Linear
issue.

This repo currently implements **Mode A**: local Slack **Socket Mode** + **SQLite** +
a Linear **personal API key**. HTTP-webhook / Cloud Run + Firestore mode, and a full
Linear OAuth app, are planned — see [`PLAN.md`](./PLAN.md).

## The flow

1. `/chipin ENG-123` → Chip fetches the issue from Linear and posts a voting card.
2. Everyone clicks a card (`1 2 3 5 8 13 21 ?`). Votes stay hidden while voting.
3. **Reveal votes** → shows every vote and the recommended estimate.
4. From there, either **Continue voting** (back to hidden voting for another round)
   or **Set estimate** (writes it to Linear and closes the round).

## Running locally

### 1. Create a Slack app (Socket Mode)

- Enable **Socket Mode**.
- **OAuth scopes** (bot): `commands`, `chat:write`.
- **App-level token** scope: `connections:write` → gives you the `xapp-` token.
- Add a **slash command** `/chipin` and enable **Interactivity**.
- Install to your workspace to get the `xoxb-` bot token.

### 2. Get a Linear personal API key

`https://linear.app/settings/api` → create a personal API key. It needs permission
to read issues and update estimates.

### 3. Configure and run

```sh
export SLACK_APP_TOKEN=xapp-...
export SLACK_BOT_TOKEN=xoxb-...
export LINEAR_API_KEY=lin_api_...
export DB_PATH=./chipin.db   # optional, this is the default

go run ./cmd/chipin
```

## Development

Toolchain is pinned with [mise](https://mise.jdx.dev) (`mise.toml`):

```sh
mise install
go test ./...          # unit tests; no live Slack/Linear needed
golangci-lint run
```

See [`AGENTS.md`](./AGENTS.md) for architecture notes and design decisions.
