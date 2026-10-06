// Command ChipIn is a self-hosted async planning-poker bot for Slack + Linear.
// It runs in one of two modes, selected by the ENVIRONMENT variable (see
// PLAN.md §1.2 and internal/config):
//
//   - ENVIRONMENT=local (default): Socket Mode transport + SQLite storage.
//     Suited to a single long-running process with outbound-only networking.
//   - ENVIRONMENT=gcp: HTTP webhook transport + Firestore storage. Suited to
//     Cloud Run, including scale-to-zero.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/laluowen/ChipIn/internal/config"
	"github.com/laluowen/ChipIn/internal/handler"
	"github.com/laluowen/ChipIn/internal/linear"
	"github.com/laluowen/ChipIn/internal/socket"
	"github.com/laluowen/ChipIn/internal/store"
	"github.com/laluowen/ChipIn/internal/webhook"
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

	switch cfg.Environment {
	case config.EnvGCP:
		return runWebhook(cfg, logger)
	default:
		return runSocket(cfg, logger)
	}
}

// runSocket runs Mode A: Socket Mode transport + SQLite storage.
func runSocket(cfg *config.Config, logger *log.Logger) error {
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

// runWebhook runs Mode B: HTTP webhook transport + Firestore storage, for
// Cloud Run. Requests are handled synchronously (handler runs to completion
// before the HTTP response is written) — see internal/webhook's doc comment
// for the cold-start/3s-ack tradeoff this implies.
func runWebhook(cfg *config.Config, logger *log.Logger) error {
	ctx := context.Background()
	repo, err := store.NewFirestore(ctx, cfg.GCPProjectID, cfg.FirestoreDatabaseID)
	if err != nil {
		return err
	}
	defer repo.Close()

	slackClient := slack.New(cfg.SlackBotToken)
	linearClient := linear.New(cfg.LinearAPIKey)

	h := handler.New(slackClient, linearClient, repo)
	srv := webhook.New(h, cfg.SlackSigningSecret, logger)

	httpServer := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	runCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Printf("listening on :%s (webhook mode)", cfg.Port)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-runCtx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return err
		}
	case err := <-errCh:
		if err != nil {
			return err
		}
	}
	logger.Println("shutting down")
	return nil
}
