package storage

import (
	"context"
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
