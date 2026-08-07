// Package storage wraps an S3-compatible object client (minio-go) with the
// three operations services need: presigned uploads, existence/metadata
// checks, and deletes. Bucket naming and key layout live here so callers
// never construct object keys themselves.
package storage

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
)

// Client wraps a minio.Client scoped to a single bucket.
type Client struct {
	inner  *minio.Client
	bucket string
}

// New wraps mc for operations against bucket. bucket must be non-empty.
func New(mc *minio.Client, bucket string) (*Client, error) {
	if mc == nil {
		return nil, fmt.Errorf("storage: minio client is required")
	}
	if bucket == "" {
		return nil, fmt.Errorf("storage: bucket is required")
	}
	return &Client{inner: mc, bucket: bucket}, nil
}

// objectKey rejects keys that could escape the intended bucket layout
// (empty, or containing path traversal segments).
func objectKey(key string) (string, error) {
	if key == "" {
		return "", fmt.Errorf("storage: key is required")
	}
	for _, segment := range strings.Split(key, "/") {
		if segment == "." || segment == ".." {
			return "", fmt.Errorf("storage: key %q contains an invalid path segment", key)
		}
	}
	return key, nil
}

// PresignPut returns a presigned URL that allows a client to PUT an object
// at key directly to the bucket, valid for expiry.
func (c *Client) PresignPut(ctx context.Context, key string, expiry time.Duration) (*url.URL, error) {
	key, err := objectKey(key)
	if err != nil {
		return nil, err
	}
	if expiry <= 0 {
		return nil, fmt.Errorf("storage: expiry must be positive")
	}

	u, err := c.inner.PresignedPutObject(ctx, c.bucket, key, expiry)
	if err != nil {
		return nil, fmt.Errorf("storage: presign put %q: %w", key, err)
	}
	return u, nil
}

// Stat returns the object metadata for key.
func (c *Client) Stat(ctx context.Context, key string) (minio.ObjectInfo, error) {
	key, err := objectKey(key)
	if err != nil {
		return minio.ObjectInfo{}, err
	}

	info, err := c.inner.StatObject(ctx, c.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return minio.ObjectInfo{}, fmt.Errorf("storage: stat %q: %w", key, err)
	}
	return info, nil
}

// Delete removes the object at key. Deleting a nonexistent key is not an
// error, matching S3 semantics.
func (c *Client) Delete(ctx context.Context, key string) error {
	key, err := objectKey(key)
	if err != nil {
		return err
	}

	if err := c.inner.RemoveObject(ctx, c.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("storage: delete %q: %w", key, err)
	}
	return nil
}
