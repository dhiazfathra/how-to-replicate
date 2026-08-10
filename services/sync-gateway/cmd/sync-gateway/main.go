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

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/dhiazfathra/how-to-replicate/proto/gen/go/sync/v1/syncv1connect"
	"github.com/dhiazfathra/how-to-replicate/services/internal/auth"
	"github.com/dhiazfathra/how-to-replicate/services/internal/db"
	"github.com/dhiazfathra/how-to-replicate/services/internal/db/sqlcgen"
	"github.com/dhiazfathra/how-to-replicate/services/internal/httpx"
	htrotel "github.com/dhiazfathra/how-to-replicate/services/internal/otel"
	"github.com/dhiazfathra/how-to-replicate/services/internal/storage"
	"github.com/dhiazfathra/how-to-replicate/services/sync-gateway/internal/authctx"
	"github.com/dhiazfathra/how-to-replicate/services/sync-gateway/internal/gateway"
	"github.com/dhiazfathra/how-to-replicate/services/sync-gateway/internal/handler"
	"github.com/dhiazfathra/how-to-replicate/services/sync-gateway/internal/realtime"
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

	objectStore, err := newObjectStore(ctx)
	if err != nil {
		return err
	}

	hub := realtime.NewHub()
	gw := &gateway.Gateway{
		WithinTx: gateway.NewTxRunner(pool),
		Storage:  objectStore,
		OnCommit: hub.Notify,
	}
	svc := &handler.SyncService{Gateway: gw}
	wsHandler := &realtime.Handler{
		Gateway:    gw,
		Hub:        hub,
		Membership: realtime.NewMembershipStore(),
	}

	authMiddleware, err := newAuthMiddleware(ctx, pool)
	if err != nil {
		return err
	}

	router := httpx.NewRouter(serviceName, slog.Default())
	path, connectHandler := syncv1connect.NewSyncServiceHandler(svc)
	router.Mount(path, authMiddleware(connectHandler))
	router.Handle("/v1/sync/deltas/ws", authMiddleware(wsHandler))

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

// newAuthMiddleware builds the real OIDC bearer-token middleware when
// HTR_OIDC_ISSUER is configured. Without it, it falls back to
// authctx.HeaderMiddleware, which trusts client-supplied identity headers
// — acceptable only for local dev and tests, never for a deployment
// reachable by untrusted callers.
func newAuthMiddleware(ctx context.Context, pool db.Pool) (func(http.Handler) http.Handler, error) {
	issuer := os.Getenv("HTR_OIDC_ISSUER")
	if issuer == "" {
		slog.Warn("sync-gateway: HTR_OIDC_ISSUER unset, using dev header auth seam")
		return authctx.HeaderMiddleware, nil
	}

	clientID := os.Getenv("HTR_OIDC_CLIENT_ID")
	_, verifier, err := auth.NewOAuth2Config(ctx, issuer, clientID, "", nil)
	if err != nil {
		return nil, err
	}

	queries := sqlcgen.New(pool.(interface {
		sqlcgen.DBTX
	}))
	resolver := authctx.DBMembershipResolver{Queries: queries}

	return authctx.OIDCMiddleware(verifier, resolver), nil
}

// newObjectStore wires the asset-upload object store against ADR-011's
// self-hosted, S3-compatible Nutanix endpoint, hardening the bucket
// (encryption on, no public policy) before returning.
func newObjectStore(ctx context.Context) (gateway.ObjectStore, error) {
	endpoint := os.Getenv("HTR_S3_ENDPOINT")
	accessKey := os.Getenv("HTR_S3_ACCESS_KEY")
	secretKey := os.Getenv("HTR_S3_SECRET_KEY")
	bucket := os.Getenv("HTR_S3_BUCKET")
	useSSL := os.Getenv("HTR_S3_USE_SSL") != "false"

	mc, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, err
	}

	client, err := storage.New(mc, bucket)
	if err != nil {
		return nil, err
	}
	if err := client.EnsureHardenedBucket(ctx); err != nil {
		return nil, err
	}

	return gateway.NewObjectStore(client), nil
}
