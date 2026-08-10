package gateway

import (
	"context"
	"testing"

	"github.com/minio/minio-go/v7"

	"github.com/dhiazfathra/how-to-replicate/services/internal/storage"
)

// TestStorageAdapter_StatSize_GenericError exercises StatSize's fallback
// branch — an error that is not a NoSuchKey/NotFound minio.ErrorResponse —
// which the happy-path and not-found cases (covered via the asset-upload
// integration tests against real MinIO) never reach. A path-traversal key
// makes storage.Client.Stat fail before any network call, with a plain
// error rather than a minio.ErrorResponse, so no MinIO server is needed
// here.
func TestStorageAdapter_StatSize_GenericError(t *testing.T) {
	mc, err := minio.New("localhost:9000", &minio.Options{Secure: false})
	if err != nil {
		t.Fatalf("minio.New: %v", err)
	}
	client, err := storage.New(mc, "test-bucket")
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}

	os := NewObjectStore(client)
	exists, size, err := os.StatSize(context.Background(), "../escape")
	if err == nil {
		t.Fatal("expected error")
	}
	if exists {
		t.Fatal("expected exists=false on error")
	}
	if size != 0 {
		t.Fatalf("expected size=0 on error, got %d", size)
	}
}
