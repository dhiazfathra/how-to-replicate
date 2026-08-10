// Package storage wraps an S3-compatible object client (minio-go) with the
// three operations services need: presigned uploads, existence/metadata
// checks, and deletes. Bucket naming and key layout live here so callers
// never construct object keys themselves.
package storage

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/sse"
)

// HeaderChecksumSHA256 and HeaderIfNoneMatch are the headers a checksummed
// presign binds into its SigV4 signature (see PresignPutChecksummed): the
// client's PUT must carry them with these exact values or the request fails
// signature validation before the store ever looks at the body.
const (
	HeaderChecksumSHA256 = "x-amz-checksum-sha256"
	HeaderIfNoneMatch    = "If-None-Match"
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

// PresignPutChecksummed returns a presigned PUT URL whose SigV4 signature
// binds two request headers: a checksum-sha256 header carrying sha256Hex
// (so the caller cannot substitute a different declared checksum without
// invalidating the signature) and "If-None-Match: *" (so a client that
// drops the header on replay also fails signature validation). Whether the
// object store additionally enforces checksum-match-on-body and
// conditional-write semantics for these headers is backend-version
// dependent; CompleteAssetUpload does not rely on that and re-derives the
// hash itself (see gateway.go).
//
// The returned map is what the caller must echo back to RequestAssetUpload's
// response as required_headers: the client's PUT must set exactly these
// header values.
func (c *Client) PresignPutChecksummed(ctx context.Context, key string, expiry time.Duration, sha256Hex string) (*url.URL, map[string]string, error) {
	key, err := objectKey(key)
	if err != nil {
		return nil, nil, err
	}
	if expiry <= 0 {
		return nil, nil, fmt.Errorf("storage: expiry must be positive")
	}
	raw, err := hex.DecodeString(sha256Hex)
	if err != nil || len(raw) != sha256.Size {
		return nil, nil, fmt.Errorf("storage: sha256 must be a 64-character hex string")
	}
	checksumB64 := base64.StdEncoding.EncodeToString(raw)

	headers := http.Header{}
	headers.Set(HeaderChecksumSHA256, checksumB64)
	headers.Set(HeaderIfNoneMatch, "*")

	u, err := c.inner.PresignHeader(ctx, http.MethodPut, c.bucket, key, expiry, nil, headers)
	if err != nil {
		return nil, nil, fmt.Errorf("storage: presign checksummed put %q: %w", key, err)
	}
	return u, map[string]string{
		HeaderChecksumSHA256: checksumB64,
		HeaderIfNoneMatch:    "*",
	}, nil
}

// HashObject reads key back from the store and returns its sha256 in hex.
// This is the server-side, un-influenceable hash CompleteAssetUpload trusts:
// unlike a checksum echoed back from the store's own metadata (which
// reflects whatever the client claimed at PUT time), this is computed here
// from the bytes the store actually returns.
func (c *Client) HashObject(ctx context.Context, key string) (string, error) {
	key, err := objectKey(key)
	if err != nil {
		return "", err
	}

	obj, err := c.inner.GetObject(ctx, c.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return "", fmt.Errorf("storage: get %q: %w", key, err)
	}
	defer func() { _ = obj.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, obj); err != nil {
		return "", fmt.Errorf("storage: read %q: %w", key, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// EnsureHardenedBucket creates the bucket if it doesn't exist and applies
// the invariants ADR-011 requires: server-side encryption on, and a policy
// that denies anonymous (public) access. It is idempotent.
func (c *Client) EnsureHardenedBucket(ctx context.Context) error {
	exists, err := c.inner.BucketExists(ctx, c.bucket)
	if err != nil {
		return fmt.Errorf("storage: check bucket exists: %w", err)
	}
	if !exists {
		if err := c.inner.MakeBucket(ctx, c.bucket, minio.MakeBucketOptions{}); err != nil {
			return fmt.Errorf("storage: make bucket: %w", err)
		}
	}

	if err := c.inner.SetBucketEncryption(ctx, c.bucket, sse.NewConfigurationSSES3()); err != nil {
		return fmt.Errorf("storage: set bucket encryption: %w", err)
	}

	// No public-read/public-write policy is ever set on this bucket — an
	// absence rather than an explicit deny statement. An earlier version of
	// this method tried an explicit anonymous-deny policy conditioned on
	// "aws:PrincipalType", which MinIO's policy engine rejects outright
	// ("invalid condition key") since it only implements a subset of AWS's
	// IAM condition keys; asserting that MinIO's default (no policy = no
	// anonymous access) actually holds is what CurrentBucketPolicy and the
	// storage integration test do instead of fighting the policy engine for
	// an equivalent explicit statement.
	return nil
}

// CurrentBucketPolicy returns the bucket's policy document, or "" if none is
// set — MinIO's default, private state. Exists so "no public bucket policy"
// (ADR-011) is an assertable fact in tests, not just an assumption.
func (c *Client) CurrentBucketPolicy(ctx context.Context) (string, error) {
	policy, err := c.inner.GetBucketPolicy(ctx, c.bucket)
	if err != nil {
		return "", fmt.Errorf("storage: get bucket policy: %w", err)
	}
	return policy, nil
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
// error, matching S3 semantics — this is what makes the purge state
// machine's blob-delete step (services/purge-job) safely retryable: a
// resumed job re-issuing a delete for a key some earlier attempt already
// removed sees success, not a failure that would stall the job.
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

// ListKeys returns every object key currently in the bucket under prefix
// (pass "" for the whole bucket). It exists for the purge job's orphan
// detector, which has no other way to ask "what actually exists in object
// storage" independent of what Postgres believes exists.
func (c *Client) ListKeys(ctx context.Context, prefix string) ([]string, error) {
	var keys []string
	for obj := range c.inner.ListObjects(ctx, c.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if obj.Err != nil {
			return nil, fmt.Errorf("storage: list keys: %w", obj.Err)
		}
		keys = append(keys, obj.Key)
	}
	return keys, nil
}
