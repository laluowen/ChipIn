// Package socket adapts Slack Socket Mode events onto the transport-agnostic
// handler.PokerHandler. It owns the receive loop, acknowledges requests, and
// routes slash commands and interactive actions to the handler.
package socket

import (
	"context"
	"log"

	"github.com/laluowen/chipin/internal/handler"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
)

// Runner consumes Socket Mode events and dispatches them to the handler.
type Runner struct {
	sm      *socketmode.Client
	handler *handler.PokerHandler
	log     *log.Logger
}

// New builds a Runner. api must be constructed with both the bot and app
// tokens (see slack.New with slack.OptionAppLevelToken).
func New(api *slack.Client, h *handler.PokerHandler, logger *log.Logger) *Runner {
	return &Runner{
		sm:      socketmode.New(api),
		handler: h,
		log:     logger,
	}
}

// Run connects and processes events until ctx is cancelled.
func (r *Runner) Run(ctx context.Context) error {
	go r.loop(ctx)
	return r.sm.RunContext(ctx)
}

func (r *Runner) loop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case evt := <-r.sm.Events:
			r.dispatch(ctx, evt)
		}
	}
}

func (r *Runner) dispatch(ctx context.Context, evt socketmode.Event) {
	switch evt.Type {
	case socketmode.EventTypeConnecting:
		r.log.Println("connecting to Slack via socket mode...")
	case socketmode.EventTypeConnected:
		r.log.Println("connected")
	case socketmode.EventTypeInvalidAuth:
		r.log.Println("invalid auth: check SLACK_APP_TOKEN and SLACK_BOT_TOKEN")

	case socketmode.EventTypeSlashCommand:
		cmd, ok := evt.Data.(slack.SlashCommand)
		if !ok {
			return
		}
		r.sm.Ack(*evt.Request)
		if err := r.handler.HandleSlashCommand(ctx, cmd); err != nil {
			r.log.Printf("slash command error: %v", err)
		}

	case socketmode.EventTypeInteractive:
		cb, ok := evt.Data.(slack.InteractionCallback)
		if !ok {
			return
		}
		r.sm.Ack(*evt.Request)
		if err := r.handler.HandleInteraction(ctx, cb); err != nil {
			r.log.Printf("interaction error: %v", err)
		}
	}
}
