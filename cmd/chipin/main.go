// Command ChipIn is a self-hosted async planning-poker bot for Slack + Linear.
// This entrypoint runs the local Socket Mode transport backed by SQLite.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/laluowen/ChipIn/internal/config"
	"github.com/laluowen/ChipIn/internal/handler"
	"github.com/laluowen/ChipIn/internal/linear"
	"github.com/laluowen/ChipIn/internal/socket"
	"github.com/laluowen/ChipIn/internal/store"
	"github.com/slack-go/slack"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ChipIn:", err)
		os.Exit(1)
	}
}

func run() error {
	logger := log.New(os.Stderr, "", log.LstdFlags)
	logger.Printf("ChipIn %s starting", version)

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	repo, err := store.NewSQLite(cfg.DBPath)
	if err != nil {
		return err
	}
	defer repo.Close()

	slackClient := slack.New(cfg.SlackBotToken, slack.OptionAppLevelToken(cfg.SlackAppToken))
	linearClient := linear.New(cfg.LinearAPIKey)

	h := handler.New(slackClient, linearClient, repo)
	runner := socket.New(slackClient, h, logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := runner.Run(ctx); err != nil && ctx.Err() == nil {
		return err
	}
	logger.Println("shutting down")
	return nil
}
