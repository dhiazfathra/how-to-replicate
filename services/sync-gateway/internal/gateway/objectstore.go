package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/minio/minio-go/v7"

	"github.com/dhiazfathra/how-to-replicate/services/internal/storage"
)

// ObjectStore is the narrow object-storage seam asset-upload logic runs
// against. Defined here (not imported from internal/storage directly) so
// gateway logic depends on an interface it owns, not a concrete minio-go
// wrapper — tests supply an in-memory fake, no MinIO required.
type ObjectStore interface {
	// PresignPutChecksummed returns a presigned PUT URL for key, expiring
	// after expiry, with sha256Hex bound into the request signature, plus
	// the header values the client's PUT must send for that signature to
	// validate.
	PresignPutChecksummed(ctx context.Context, key string, expiry time.Duration, sha256Hex string) (*url.URL, map[string]string, error)

	// StatSize reports whether key exists and, if so, its size in bytes.
	StatSize(ctx context.Context, key string) (exists bool, sizeBytes int64, err error)

	// HashObject returns the sha256 (hex) of key's current content, computed
	// by reading the object back — never a value the client could have
	// supplied.
	HashObject(ctx context.Context, key string) (sha256Hex string, err error)
}

// storageAdapter adapts *storage.Client to ObjectStore.
type storageAdapter struct {
	client *storage.Client
}

// NewObjectStore wraps a storage.Client for use as an asset-upload
// ObjectStore.
func NewObjectStore(client *storage.Client) ObjectStore {
	return &storageAdapter{client: client}
}

func (a *storageAdapter) PresignPutChecksummed(ctx context.Context, key string, expiry time.Duration, sha256Hex string) (*url.URL, map[string]string, error) {
	return a.client.PresignPutChecksummed(ctx, key, expiry, sha256Hex)
}

func (a *storageAdapter) StatSize(ctx context.Context, key string) (bool, int64, error) {
	info, err := a.client.Stat(ctx, key)
	if err != nil {
		var errResp minio.ErrorResponse
		if errors.As(err, &errResp) && (errResp.Code == "NoSuchKey" || errResp.Code == "NotFound") {
			return false, 0, nil
		}
		return false, 0, fmt.Errorf("gateway: stat object: %w", err)
	}
	return true, info.Size, nil
}

func (a *storageAdapter) HashObject(ctx context.Context, key string) (string, error) {
	return a.client.HashObject(ctx, key)
}
