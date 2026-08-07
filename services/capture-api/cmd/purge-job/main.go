// Command purge-job is the durable hard-delete pipeline's driver (Task
// 13): a background poll loop, not a client-facing RPC — same shape as
// redaction-audit's cmd, since a purge is a periodic sweep/reconcile, not
// something a request waits on. Each tick it sweeps every workspace for
// ready captures past their resolved retention window (minting a
// purge_jobs row per capture), then reconciles every job not yet at the
// terminal "purged" state, driving it forward from wherever it was left —
// including a job this same loop crashed partway through last tick.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/dhiazfathra/how-to-replicate/services/capture-api/internal/purge"
	"github.com/dhiazfathra/how-to-replicate/services/capture-api/internal/store"
	"github.com/dhiazfathra/how-to-replicate/services/internal/db"
	"github.com/dhiazfathra/how-to-replicate/services/internal/db/sqlcgen"
	"github.com/dhiazfathra/how-to-replicate/services/internal/storage"
)

func main() {
	if err := run(); err != nil {
		slog.Error("purge-job: fatal", "error", err)
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

	blobs, err := objectStore()
	if err != nil {
		return err
	}

	pipeline := &purge.Pipeline{
		Store: sqlcgen.New(pool),
		Tx:    store.PurgeTxStore{Pool: pool},
		Blobs: blobs,
		NewID: newID,
	}

	interval := pollInterval()
	slog.Info("purge-job: starting poll loop", "interval", interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	runOnce(ctx, pipeline)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			runOnce(ctx, pipeline)
		}
	}
}

// runOnce sweeps for newly-expired captures, then reconciles every job not
// yet purged — including ones this process itself left mid-flight on a
// previous tick before an ungraceful exit.
func runOnce(ctx context.Context, p *purge.Pipeline) {
	created, err := p.Sweep(ctx, time.Now())
	if err != nil {
		slog.Error("purge-job: sweep", "error", err)
	} else if len(created) > 0 {
		slog.Info("purge-job: created purge jobs", "count", len(created))
	}

	if err := p.Reconcile(ctx); err != nil {
		slog.Error("purge-job: reconcile", "error", err)
	}

	orphans, err := p.DetectOrphans(ctx, "")
	if err != nil {
		slog.Error("purge-job: detect orphans", "error", err)
		return
	}
	if len(orphans) > 0 {
		slog.Warn("purge-job: orphaned objects detected", "count", len(orphans), "keys", orphans)
	}
}

// pollInterval reads HTR_PURGE_JOB_INTERVAL (a Go duration string, e.g.
// "1h"); defaults to 1 hour — retention sweeps have no reason to run as
// often as redaction-audit's 5-minute default.
func pollInterval() time.Duration {
	const defaultInterval = time.Hour
	raw := os.Getenv("HTR_PURGE_JOB_INTERVAL")
	if raw == "" {
		return defaultInterval
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		slog.Warn("purge-job: invalid HTR_PURGE_JOB_INTERVAL, using default", "value", raw, "default", defaultInterval)
		return defaultInterval
	}
	return d
}

// objectStore wires the same HTR_S3_* environment convention
// sync-gateway's newObjectStore uses (ADR-011's self-hosted, S3-compatible
// endpoint) — this process only ever deletes and lists, so it does not
// re-harden the bucket; that's sync-gateway's job at asset-upload time.
func objectStore() (*storage.Client, error) {
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
	return storage.New(mc, bucket)
}

// newID mints purge_jobs row IDs — server-side, no client of record, same
// convention (and implementation) as capture-api/internal/api.newID.
func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "purge_" + base32.HexEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
}
