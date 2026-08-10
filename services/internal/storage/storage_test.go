package storage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestClient_PresignPutChecksummed_Validation(t *testing.T) {
	c := newTestClient(t)
	validSha := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	if _, _, err := c.PresignPutChecksummed(context.Background(), "", time.Minute, validSha); err == nil {
		t.Fatal("expected error for empty key")
	}
	if _, _, err := c.PresignPutChecksummed(context.Background(), "key", 0, validSha); err == nil {
		t.Fatal("expected error for non-positive expiry")
	}
	if _, _, err := c.PresignPutChecksummed(context.Background(), "key", time.Minute, "not-hex"); err == nil {
		t.Fatal("expected error for non-hex sha256")
	}
	if _, _, err := c.PresignPutChecksummed(context.Background(), "key", time.Minute, "abcd"); err == nil {
		t.Fatal("expected error for short sha256")
	}

	// Valid inputs, but nothing is listening on localhost:9000: exercises
	// the wrapped-error return path. The success path (and the required
	// headers map) is covered by the real-MinIO integration test.
	if _, _, err := c.PresignPutChecksummed(context.Background(), "captures/evidence.mp4", time.Minute, validSha); err == nil {
		t.Fatal("expected error from unreachable server")
	}
}

func TestClient_HashObject_Validation(t *testing.T) {
	c := newTestClient(t)

	if _, err := c.HashObject(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty key")
	}
	if _, err := c.HashObject(context.Background(), "captures/evidence.mp4"); err == nil {
		t.Fatal("expected error from unreachable server")
	}

	// An object name over 1024 bytes fails minio-go's client-side name
	// validation synchronously, inside GetObject itself, before any network
	// round trip — exercises the "get %q" wrapped-error branch specifically
	// (the unreachable-server cases above surface their error later, from
	// the io.Copy read, per GetObject's lazy-read design).
	if _, err := c.HashObject(context.Background(), strings.Repeat("a", 1025)); err == nil {
		t.Fatal("expected error for object name over 1024 characters")
	}
}

// fakeS3Server returns an httptest server that answers HEAD /bucket with
// headStatus and any other request (MakeBucket's bucket-creating PUT, or
// SetBucketEncryption's PUT ?encryption) with otherStatus — enough to drive
// EnsureHardenedBucket down a specific error branch without a real MinIO.
func fakeS3Server(t *testing.T, headStatus, otherStatus int) *Client {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(headStatus)
			return
		}
		w.WriteHeader(otherStatus)
	}))
	t.Cleanup(srv.Close)

	mc, err := minio.New(strings.TrimPrefix(srv.URL, "http://"), &minio.Options{
		Creds:  credentials.NewStaticV4("x", "y", ""),
		Secure: false,
		Region: "us-east-1",
	})
	if err != nil {
		t.Fatalf("new minio client: %v", err)
	}
	c, err := New(mc, "bucket")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return c
}

func TestClient_EnsureHardenedBucket_MakeBucketError(t *testing.T) {
	// HEAD reports the bucket absent (404), so EnsureHardenedBucket takes
	// the MakeBucket branch; MakeBucket itself is made to fail (500).
	c := fakeS3Server(t, http.StatusNotFound, http.StatusInternalServerError)
	if err := c.EnsureHardenedBucket(context.Background()); err == nil {
		t.Fatal("expected error from failing MakeBucket")
	}
}

func TestClient_EnsureHardenedBucket_SetBucketEncryptionError(t *testing.T) {
	// HEAD reports the bucket present (200), so MakeBucket is skipped
	// entirely; SetBucketEncryption's PUT is made to fail (500).
	c := fakeS3Server(t, http.StatusOK, http.StatusInternalServerError)
	if err := c.EnsureHardenedBucket(context.Background()); err == nil {
		t.Fatal("expected error from failing SetBucketEncryption")
	}
}

func TestClient_EnsureHardenedBucket_UnreachableServer(t *testing.T) {
	c := newTestClient(t)
	if err := c.EnsureHardenedBucket(context.Background()); err == nil {
		t.Fatal("expected error from unreachable server")
	}
}

func TestClient_CurrentBucketPolicy_UnreachableServer(t *testing.T) {
	c := newTestClient(t)
	if _, err := c.CurrentBucketPolicy(context.Background()); err == nil {
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
