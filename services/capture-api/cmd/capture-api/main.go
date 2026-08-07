// Command capture-api serves workspace and project CRUD and workspace
// policy, gated by OIDC identity and workspace-scoped RBAC (Task 10).
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dhiazfathra/how-to-replicate/services/capture-api/internal/api"
	"github.com/dhiazfathra/how-to-replicate/services/capture-api/internal/sharelink"
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

	signer, err := shareLinkSigner()
	if err != nil {
		return err
	}

	st := store.New(sqlcgen.New(pool))
	limiter := &sharelink.Limiter{Max: 20, Window: time.Minute}

	router := httpx.NewRouter(serviceName, slog.Default())
	router.Mount("/", api.NewRouter(verifier, st, pool, signer))
	router.Mount("/", api.NewShareRouter(st, signer, limiter))

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

// shareLinkSigner builds the share-link Signer from environment config:
// HTR_SHARE_LINK_KEY_ID/HTR_SHARE_LINK_KEY are the current signing key;
// HTR_SHARE_LINK_PREVIOUS_KEYS is an optional comma-separated list of
// "id:secret" pairs still accepted for verification during rotation, so an
// operator can introduce a new current key without invalidating tokens
// signed under the outgoing one until it's removed here too.
func shareLinkSigner() (sharelink.Signer, error) {
	keyID := os.Getenv("HTR_SHARE_LINK_KEY_ID")
	secret := os.Getenv("HTR_SHARE_LINK_KEY")
	if keyID == "" || secret == "" {
		return sharelink.Signer{}, errRequiredEnv("HTR_SHARE_LINK_KEY_ID/HTR_SHARE_LINK_KEY")
	}

	signer := sharelink.Signer{Current: sharelink.Key{ID: keyID, Secret: []byte(secret)}}
	for _, pair := range strings.Split(os.Getenv("HTR_SHARE_LINK_PREVIOUS_KEYS"), ",") {
		id, sec, ok := strings.Cut(pair, ":")
		if !ok || id == "" || sec == "" {
			continue
		}
		signer.Previous = append(signer.Previous, sharelink.Key{ID: id, Secret: []byte(sec)})
	}
	return signer, nil
}

type errRequiredEnv string

func (e errRequiredEnv) Error() string {
	return "capture-api: required environment variable " + string(e) + " is unset"
}
