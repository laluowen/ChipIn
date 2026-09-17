# PLAN.md: Self-Hosted Async Planning Poker

## 1. System Architecture

A single, statically compiled Go binary capable of operating in two distinct deployment modes. The application abstracts its storage and transport layers to allow seamless transition between local testing, GCP Cloud Run, and AWS Lambda without rewriting core business logic.

### 1.1 Core Stack

- **Language:** Go (Golang 1.21+)
- **Base Container Image:** `gcr.io/distroless/static-debian12`
- **Slack Integration:** `[github.com/slack-go/slack](https://github.com/slack-go/slack)`
- **Linear Integration:** Standard `net/http` client executing raw GraphQL queries.

### 1.2 Deployment Modes

- **Mode A: Local / Private Network (Socket Mode)**

    - **Transport:** Outbound WebSockets.

    - **State:** Local SQLite database.
- **Mode B: Serverless / Cloud Run (Webhook Mode)**

    - **Transport:** HTTP Webhooks.

    - **State:** Google Cloud Firestore.

## 2. Abstraction Boundaries

To decouple the core poker logic from the infrastructure, we define interfaces for both State and Transport.

### 2.1 Storage Interface (`repository.go`)

```go
package store
import "context"

type PokerSession struct {
    IssueID   string         
    Status    string         
    ChannelID string         
    Votes     map[string]int 
}

type SessionRepository interface {
    GetSession(ctx context.Context, issueID string) (*PokerSession, error)
    SaveSession(ctx context.Context, session *PokerSession) error
    DeleteSession(ctx context.Context, issueID string) error
}
```

### 2.2 Transport Boundary (`handler.go`)

A centralized handler struct processes the game logic regardless of whether the inbound request came from a WebSocket or an HTTP endpoint.

```go
package handler
import "github.com/slack-go/slack"

type PokerHandler struct {
    SlackClient *slack.Client
    DB          store.SessionRepository
    LinearToken string
}

func (h *PokerHandler) HandleSlashCommand(cmd slack.SlashCommand) error { return nil }
func (h *PokerHandler) HandleBlockAction(action slack.BlockAction) error { return nil }
```

## 3. The Execution Flow

1. **Start Session (`/poker ENG-123`)**

    - Parse the issue ID from the Slack command text.

    - Execute an HTTP POST to `[https://api.linear.app/graphql](https://api.linear.app/graphql)` fetching issue data.

    - Create a new `PokerSession` in the database.

    - Post a Slack Block Kit message featuring estimation buttons.
2. **Cast Votes (Async)**

    - Slack payload routes to `HandleBlockAction`.

    - App fetches the `PokerSession`, updates the `Votes` map, and saves it.

    - App fires a `chat.update` API call to refresh the Block Kit UI.
3. **Reveal & Sync**

    - User clicks the "Reveal Votes" button.

    - App updates the UI to display the calculated consensus.

    - App executes a GraphQL mutation to Linear to update the issue's estimate.

    - App deletes `PokerSession` from the database.

## 4. Environment Configuration

### For Local / Socket Mode

- `SLACK_APP_TOKEN` (xapp-...)
- `SLACK_BOT_TOKEN` (xoxb-...)
- `LINEAR_API_KEY`
- `DB_TYPE=sqlite`
- `DB_PATH=./poker.db`

### For Cloud Run / Webhook Mode

- `PORT` (Provided automatically by Cloud Run)
- `SLACK_BOT_TOKEN` (xoxb-...)
- `SLACK_SIGNING_SECRET`
- `LINEAR_API_KEY`
- `DB_TYPE=firestore`
- `GCP_PROJECT_ID`
