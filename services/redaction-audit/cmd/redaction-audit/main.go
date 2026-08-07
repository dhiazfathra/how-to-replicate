// Command redaction-audit is the server-side alarm (spec §11, CLAUDE.md
// invariant 7): it periodically re-evaluates every "ready" capture in every
// workspace against each workspace's current redaction ruleset, and alerts
// Security on any finding. It is a background job, not a client-facing
// RPC — there is no HTTP handler here, just a poll loop, matching the
// brief's guidance not to build message-queue infrastructure this stack
// doesn't otherwise have.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dhiazfathra/how-to-replicate/services/internal/db"
	"github.com/dhiazfathra/how-to-replicate/services/internal/db/sqlcgen"
	"github.com/dhiazfathra/how-to-replicate/services/redaction-audit/internal/audit"
)

func main() {
	if err := run(); err != nil {
		slog.Error("redaction-audit: fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dsn := os.Getenv("HTR_POSTGRES_DSN")
	pass := os.Getenv("HTR_RUNTIME_PASSWORD")
	runtimeDSN, err := db.WithRuntimeRole(dsn, pass)
	if err != nil {
		return err
	}

	pool, err := db.NewPool(ctx, runtimeDSN)
	if err != nil {
		return err
	}
	defer pool.Close()

	sink, err := alertSink()
	if err != nil {
		return err
	}

	svc := audit.New(sqlcgen.New(pool), sink)
	interval := pollInterval()

	slog.Info("redaction-audit: starting poll loop", "interval", interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Run once immediately on startup, then on every tick, so an operator
	// doesn't wait a full interval to see the first pass after a deploy.
	runOnce(ctx, sqlcgen.New(pool), svc)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			runOnce(ctx, sqlcgen.New(pool), svc)
		}
	}
}

// runOnce evaluates every workspace on record. evaluationVersion is always
// 0 (latest) here — pinning to an older version is a reproduction tool
// (audit.Service.RunWorkspace's second argument), not something the poll
// loop itself needs.
func runOnce(ctx context.Context, q *sqlcgen.Queries, svc *audit.Service) {
	workspaces, err := q.ListWorkspaces(ctx)
	if err != nil {
		slog.Error("redaction-audit: list workspaces", "error", err)
		return
	}
	for _, ws := range workspaces {
		findings, err := svc.RunWorkspace(ctx, ws.ID, 0)
		if err != nil {
			slog.Error("redaction-audit: run workspace", "workspace", ws.ID, "error", err)
			continue
		}
		if len(findings) > 0 {
			slog.Warn("redaction-audit: findings raised", "workspace", ws.ID, "count", len(findings))
		}
	}
}

// pollInterval reads HTR_REDACTION_AUDIT_INTERVAL (a Go duration string,
// e.g. "5m"); defaults to 5 minutes.
func pollInterval() time.Duration {
	const defaultInterval = 5 * time.Minute
	raw := os.Getenv("HTR_REDACTION_AUDIT_INTERVAL")
	if raw == "" {
		return defaultInterval
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		slog.Warn("redaction-audit: invalid HTR_REDACTION_AUDIT_INTERVAL, using default", "value", raw, "default", defaultInterval)
		return defaultInterval
	}
	return d
}

// alertSink builds the Sink from environment config: HTR_ALERT_WEBHOOK_URL
// is required — the audit's entire value is paging Security, so a missing
// sink is a misconfiguration, not something to silently no-op.
func alertSink() (audit.Sink, error) {
	url := os.Getenv("HTR_ALERT_WEBHOOK_URL")
	if url == "" {
		return nil, errRequiredEnv("HTR_ALERT_WEBHOOK_URL")
	}
	return audit.NewWebhookSink(url), nil
}

type errRequiredEnv string

func (e errRequiredEnv) Error() string {
	return "redaction-audit: required environment variable " + string(e) + " is unset"
}
