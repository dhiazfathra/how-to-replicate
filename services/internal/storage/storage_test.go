package storage

import (
	"context"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

func TestNew_Validation(t *testing.T) {
	if _, err := New(nil, "bucket"); err == nil {
		t.Fatal("expected error for nil client")
	}

	mc, err := minio.New("localhost:9000", &minio.Options{
		Creds: credentials.NewStaticV4("x", "y", ""),
	})
	if err != nil {
		t.Fatalf("unexpected error constructing minio client: %v", err)
	}

	if _, err := New(mc, ""); err == nil {
		t.Fatal("expected error for empty bucket")
	}

	if _, err := New(mc, "bucket"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestObjectKey_Validation(t *testing.T) {
	cases := []struct {
		name    string
		key     string
		wantErr bool
	}{
		{name: "empty", key: "", wantErr: true},
		{name: "dot segment", key: "captures/./evidence", wantErr: true},
		{name: "dotdot segment", key: "../secret", wantErr: true},
		{name: "valid", key: "captures/01H/evidence.mp4", wantErr: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := objectKey(tc.key)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error for key %q", tc.key)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error for key %q: %v", tc.key, err)
			}
		})
	}
}

func newTestClient(t *testing.T) *Client {
	t.Helper()
	mc, err := minio.New("localhost:9000", &minio.Options{
		Creds: credentials.NewStaticV4("x", "y", ""),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c, err := New(mc, "bucket")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return c
}

func TestClient_PresignPut_Validation(t *testing.T) {
	c := newTestClient(t)

	if _, err := c.PresignPut(context.Background(), "", time.Minute); err == nil {
		t.Fatal("expected error for empty key")
	}
	if _, err := c.PresignPut(context.Background(), "key", 0); err == nil {
		t.Fatal("expected error for non-positive expiry")
	}

	// Valid key and expiry, but nothing is listening on localhost:9000 in
	// this test: exercises the wrapped-error return path. The success path
	// is covered by the real-MinIO integration test.
	if _, err := c.PresignPut(context.Background(), "captures/evidence.mp4", time.Minute); err == nil {
		t.Fatal("expected error from unreachable server")
	}
}

func TestClient_Stat_Validation(t *testing.T) {
	c := newTestClient(t)

	if _, err := c.Stat(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty key")
	}

	// Valid key, but nothing is listening on localhost:9000 in this test:
	// exercises the wrapped-error return path.
	if _, err := c.Stat(context.Background(), "captures/evidence.mp4"); err == nil {
		t.Fatal("expected error from unreachable server")
	}
}

func TestClient_Delete_Validation(t *testing.T) {
	c := newTestClient(t)

	if err := c.Delete(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty key")
	}

	if err := c.Delete(context.Background(), "captures/evidence.mp4"); err == nil {
		t.Fatal("expected error from unreachable server")
	}
}
