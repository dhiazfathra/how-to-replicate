// Command sync-gateway serves the ConnectRPC sync surface: mutation
// intake, revision numbering, and last-write-wins conflict resolution
// (spec §10, ADR-012).
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dhiazfathra/how-to-replicate/proto/gen/go/sync/v1/syncv1connect"
	"github.com/dhiazfathra/how-to-replicate/services/internal/db"
	"github.com/dhiazfathra/how-to-replicate/services/internal/httpx"
	htrotel "github.com/dhiazfathra/how-to-replicate/services/internal/otel"
	"github.com/dhiazfathra/how-to-replicate/services/sync-gateway/internal/authctx"
	"github.com/dhiazfathra/how-to-replicate/services/sync-gateway/internal/gateway"
	"github.com/dhiazfathra/how-to-replicate/services/sync-gateway/internal/handler"
)

const serviceName = "sync-gateway"

func main() {
	if err := run(); err != nil {
		slog.Error("sync-gateway: fatal", "error", err)
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

	providers, err := htrotel.NewProviders(ctx, serviceName)
	if err != nil {
		return err
	}
	defer func() { _ = providers.Shutdown(context.Background()) }()

	gw := &gateway.Gateway{WithinTx: gateway.NewTxRunner(pool)}
	svc := &handler.SyncService{Gateway: gw}

	router := httpx.NewRouter(serviceName, slog.Default())
	path, connectHandler := syncv1connect.NewSyncServiceHandler(svc)
	router.Mount(path, authctx.HeaderMiddleware(connectHandler))

	addr := os.Getenv("HTR_LISTEN_ADDR")
	if addr == "" {
		addr = ":8081"
	}

	server := &http.Server{Addr: addr, Handler: router, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		_ = server.Close()
	}()

	slog.Info("sync-gateway: listening", "addr", addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
