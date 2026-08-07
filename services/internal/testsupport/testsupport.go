// Package testsupport provides shared testcontainers-go harnesses for
// Postgres and MinIO, used by every service's integration tests. Tests that
// call these helpers are skipped automatically when no Docker daemon is
// reachable rather than failing the suite.
package testsupport

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/minio"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// RuntimeRolePassword is the password the schema migration assigns the
// htr_runtime role (services/internal/migrate/migrations/00002_schema.sql).
// It is fixed and non-secret: it only ever protects a throwaway test
// container, never a real deployment (which sets its own).
const RuntimeRolePassword = "htr_runtime"

// dockerAvailable does a best-effort, short-timeout check for a reachable
// Docker daemon so integration tests can skip cleanly in environments
// without one instead of hanging or failing.
func dockerAvailable(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	provider, err := testcontainers.NewDockerProvider()
	if err != nil {
		return false
	}
	defer func() { _ = provider.Close() }()

	return provider.Health(ctx) == nil
}

// Postgres starts a Postgres testcontainer and returns its connection DSN.
// The container is terminated automatically via t.Cleanup. The test is
// skipped if no Docker daemon is reachable.
func Postgres(t *testing.T, ctx context.Context) string {
	t.Helper()

	if !dockerAvailable(ctx) {
		t.Skip("testsupport: no docker daemon reachable, skipping postgres integration test")
	}

	container, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("htr_test"),
		postgres.WithUsername("htr"),
		postgres.WithPassword("htr"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	if err != nil {
		t.Fatalf("testsupport: start postgres container: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Logf("testsupport: terminate postgres container: %v", err)
		}
	})

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("testsupport: postgres connection string: %v", err)
	}

	return dsn
}

// MinIOCreds holds the endpoint and static credentials for a running MinIO
// testcontainer.
type MinIOCreds struct {
	Endpoint  string
	AccessKey string
	SecretKey string
}

// MinIO starts a MinIO testcontainer and returns its endpoint and static
// credentials. The container is terminated automatically via t.Cleanup. The
// test is skipped if no Docker daemon is reachable.
func MinIO(t *testing.T, ctx context.Context) MinIOCreds {
	t.Helper()

	if !dockerAvailable(ctx) {
		t.Skip("testsupport: no docker daemon reachable, skipping minio integration test")
	}

	const accessKey = "htr-test"
	const secretKey = "htr-test-secret"

	container, err := minio.Run(ctx, "minio/minio:RELEASE.2024-10-13T13-34-11Z",
		minio.WithUsername(accessKey),
		minio.WithPassword(secretKey),
	)
	if err != nil {
		t.Fatalf("testsupport: start minio container: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Logf("testsupport: terminate minio container: %v", err)
		}
	})

	endpoint, err := container.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("testsupport: minio connection string: %v", err)
	}

	return MinIOCreds{Endpoint: endpoint, AccessKey: accessKey, SecretKey: secretKey}
}
