// Command capture-api serves workspace and project CRUD and workspace
// policy, gated by OIDC identity and workspace-scoped RBAC (Task 10).
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dhiazfathra/how-to-replicate/services/capture-api/internal/api"
	"github.com/dhiazfathra/how-to-replicate/services/capture-api/internal/store"
	"github.com/dhiazfathra/how-to-replicate/services/internal/auth"
	"github.com/dhiazfathra/how-to-replicate/services/internal/db"
	"github.com/dhiazfathra/how-to-replicate/services/internal/db/sqlcgen"
	"github.com/dhiazfathra/how-to-replicate/services/internal/httpx"
)

const serviceName = "capture-api"

func main() {
	if err := run(); err != nil {
		slog.Error("capture-api: fatal", "error", err)
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

	issuer := os.Getenv("HTR_OIDC_ISSUER")
	if issuer == "" {
		return errRequiredEnv("HTR_OIDC_ISSUER")
	}
	clientID := os.Getenv("HTR_OIDC_CLIENT_ID")
	_, verifier, err := auth.NewOAuth2Config(ctx, issuer, clientID, "", nil)
	if err != nil {
		return err
	}

	st := store.New(sqlcgen.New(pool))
	router := httpx.NewRouter(serviceName, slog.Default())
	router.Mount("/", api.NewRouter(verifier, st, pool))

	addr := os.Getenv("HTR_LISTEN_ADDR")
	if addr == "" {
		addr = ":8082"
	}

	server := &http.Server{Addr: addr, Handler: router, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		_ = server.Close()
	}()

	slog.Info("capture-api: listening", "addr", addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

type errRequiredEnv string

func (e errRequiredEnv) Error() string {
	return "capture-api: required environment variable " + string(e) + " is unset"
}
