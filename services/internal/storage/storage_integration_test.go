package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/dhiazfathra/how-to-replicate/services/internal/testsupport"
)

func TestClient_PresignPutStatDelete_AgainstRealMinIO(t *testing.T) {
	ctx := context.Background()
	creds := testsupport.MinIO(t, ctx)

	endpoint := strings.TrimPrefix(strings.TrimPrefix(creds.Endpoint, "http://"), "https://")

	mc, err := minio.New(endpoint, &minio.Options{
		Creds: credentials.NewStaticV4(creds.AccessKey, creds.SecretKey, ""),
	})
	if err != nil {
		t.Fatalf("new minio client: %v", err)
	}

	const bucket = "htr-test-bucket"
	if err := mc.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
		t.Fatalf("make bucket: %v", err)
	}

	c, err := New(mc, bucket)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	const key = "captures/evidence.txt"

	u, err := c.PresignPut(ctx, key, time.Minute)
	if err != nil {
		t.Fatalf("presign put: %v", err)
	}
	if u == nil || u.Host == "" {
		t.Fatalf("expected populated presigned url, got %v", u)
	}

	if _, err := mc.PutObject(ctx, bucket, key, strings.NewReader("evidence"), int64(len("evidence")), minio.PutObjectOptions{}); err != nil {
		t.Fatalf("seed object: %v", err)
	}

	info, err := c.Stat(ctx, key)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Key != key {
		t.Fatalf("expected key %q, got %q", key, info.Key)
	}

	if err := c.Delete(ctx, key); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err := c.Stat(ctx, key); err == nil {
		t.Fatal("expected error statting deleted object")
	}
}

func TestClient_ListKeys_AgainstRealMinIO(t *testing.T) {
	ctx := context.Background()
	creds := testsupport.MinIO(t, ctx)
	endpoint := strings.TrimPrefix(strings.TrimPrefix(creds.Endpoint, "http://"), "https://")

	mc, err := minio.New(endpoint, &minio.Options{
		Creds: credentials.NewStaticV4(creds.AccessKey, creds.SecretKey, ""),
	})
	if err != nil {
		t.Fatalf("new minio client: %v", err)
	}

	const bucket = "htr-listkeys-bucket"
	if err := mc.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
		t.Fatalf("make bucket: %v", err)
	}
	c, err := New(mc, bucket)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	// An empty bucket lists no keys — the purge pipeline's orphan detector
	// depends on this returning an empty (not error) result rather than
	// treating "nothing here" as a failure.
	keys, err := c.ListKeys(ctx, "")
	if err != nil {
		t.Fatalf("list keys (empty bucket): %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("keys = %v, want none in an empty bucket", keys)
	}

	for _, key := range []string{"captures/a/video.webm", "captures/b/screenshot.png", "other/unrelated.txt"} {
		if _, err := mc.PutObject(ctx, bucket, key, strings.NewReader("x"), 1, minio.PutObjectOptions{}); err != nil {
			t.Fatalf("seed object %s: %v", key, err)
		}
	}

	all, err := c.ListKeys(ctx, "")
	if err != nil {
		t.Fatalf("list keys (no prefix): %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("keys = %v, want 3", all)
	}

	captures, err := c.ListKeys(ctx, "captures/")
	if err != nil {
		t.Fatalf("list keys (prefix): %v", err)
	}
	if len(captures) != 2 {
		t.Fatalf("keys under captures/ = %v, want 2", captures)
	}

	// A client pointed at a bucket that doesn't exist surfaces the
	// underlying listing error rather than an empty, misleadingly
	// successful result — important for the orphan detector, which must
	// not read "bucket unreachable" as "bucket empty".
	missing, err := New(mc, "htr-listkeys-bucket-does-not-exist")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if _, err := missing.ListKeys(ctx, ""); err == nil {
		t.Fatal("expected error listing keys in a nonexistent bucket")
	}
}

func TestClient_EnsureHardenedBucket_AgainstRealMinIO(t *testing.T) {
	ctx := context.Background()
	creds := testsupport.MinIO(t, ctx)
	endpoint := strings.TrimPrefix(strings.TrimPrefix(creds.Endpoint, "http://"), "https://")

	mc, err := minio.New(endpoint, &minio.Options{
		Creds: credentials.NewStaticV4(creds.AccessKey, creds.SecretKey, ""),
	})
	if err != nil {
		t.Fatalf("new minio client: %v", err)
	}

	const bucket = "htr-hardened-bucket"
	c, err := New(mc, bucket)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	if err := c.EnsureHardenedBucket(ctx); err != nil {
		t.Fatalf("ensure hardened bucket: %v", err)
	}
	// Idempotent: calling it again on an already-hardened bucket must not
	// error (e.g. re-creating an existing bucket).
	if err := c.EnsureHardenedBucket(ctx); err != nil {
		t.Fatalf("ensure hardened bucket (second call): %v", err)
	}

	policy, err := c.CurrentBucketPolicy(ctx)
	if err != nil {
		t.Fatalf("current bucket policy: %v", err)
	}
	if policy != "" {
		t.Fatalf("expected no public bucket policy, got %q", policy)
	}

	enc, err := mc.GetBucketEncryption(ctx, bucket)
	if err != nil {
		t.Fatalf("get bucket encryption: %v", err)
	}
	if len(enc.Rules) == 0 {
		t.Fatalf("expected server-side encryption to be configured, got %+v", enc)
	}
}

func TestClient_PresignPutChecksummedAndHashObject_AgainstRealMinIO(t *testing.T) {
	ctx := context.Background()
	creds := testsupport.MinIO(t, ctx)
	endpoint := strings.TrimPrefix(strings.TrimPrefix(creds.Endpoint, "http://"), "https://")

	mc, err := minio.New(endpoint, &minio.Options{
		Creds: credentials.NewStaticV4(creds.AccessKey, creds.SecretKey, ""),
	})
	if err != nil {
		t.Fatalf("new minio client: %v", err)
	}

	const bucket = "htr-checksum-bucket"
	if err := mc.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
		t.Fatalf("make bucket: %v", err)
	}
	c, err := New(mc, bucket)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	const key = "captures/evidence.mp4"
	body := []byte("checksummed evidence body")
	sum := sha256.Sum256(body)
	sha256Hex := hex.EncodeToString(sum[:])

	u, headers, err := c.PresignPutChecksummed(ctx, key, time.Minute, sha256Hex)
	if err != nil {
		t.Fatalf("presign checksummed put: %v", err)
	}
	if headers[HeaderChecksumSHA256] == "" || headers[HeaderIfNoneMatch] != "*" {
		t.Fatalf("expected populated required headers, got %v", headers)
	}

	// A PUT missing the signed headers must fail signature validation.
	reqNoHeaders, err := http.NewRequest(http.MethodPut, u.String(), bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	respNoHeaders, err := http.DefaultClient.Do(reqNoHeaders)
	if err != nil {
		t.Fatalf("do put without headers: %v", err)
	}
	_ = respNoHeaders.Body.Close()
	if respNoHeaders.StatusCode/100 == 2 {
		t.Fatalf("expected a PUT missing the signed headers to fail, got status %d", respNoHeaders.StatusCode)
	}

	// A PUT with exactly the required headers succeeds.
	req, err := http.NewRequest(http.MethodPut, u.String(), bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do put: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("put failed: status=%d body=%s", resp.StatusCode, respBody)
	}

	gotHash, err := c.HashObject(ctx, key)
	if err != nil {
		t.Fatalf("hash object: %v", err)
	}
	if gotHash != sha256Hex {
		t.Fatalf("expected hash %q, got %q", sha256Hex, gotHash)
	}

	if _, err := c.HashObject(ctx, "no-such-key"); err == nil {
		t.Fatal("expected error hashing a nonexistent object")
	}
}
