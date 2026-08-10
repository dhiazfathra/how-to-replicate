// assets_integration_test.go drives RequestAssetUpload/CompleteAssetUpload
// end to end against real Postgres and real MinIO testcontainers: request,
// PUT (real HTTP, real presigned URL), complete, verify. It also empirically
// checks two MinIO-version-dependent behaviors the brief asks about
// directly — whether a same-key replay PUT is rejected at the store level,
// and whether the bucket policy actually denies an anonymous read — rather
// than assuming either. See the task report for what was and wasn't
// confirmed against this MinIO release. Skips cleanly if no Docker daemon
// is reachable (see testsupport).
package gateway

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/dhiazfathra/how-to-replicate/services/internal/storage"
	"github.com/dhiazfathra/how-to-replicate/services/internal/testsupport"
)

func setupRealObjectStore(t *testing.T, ctx context.Context) (ObjectStore, *storage.Client, *minio.Client, string) {
	t.Helper()
	creds := testsupport.MinIO(t, ctx)
	endpoint := strings.TrimPrefix(strings.TrimPrefix(creds.Endpoint, "http://"), "https://")

	mc, err := minio.New(endpoint, &minio.Options{
		Creds: credentials.NewStaticV4(creds.AccessKey, creds.SecretKey, ""),
	})
	if err != nil {
		t.Fatalf("new minio client: %v", err)
	}

	const bucket = "htr-assets-test"
	client, err := storage.New(mc, bucket)
	if err != nil {
		t.Fatalf("new storage client: %v", err)
	}
	if err := client.EnsureHardenedBucket(ctx); err != nil {
		t.Fatalf("ensure hardened bucket: %v", err)
	}

	return NewObjectStore(client), client, mc, bucket
}

func putViaPresign(t *testing.T, uploadURL string, headers map[string]string, body []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, uploadURL, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new put request: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do put: %v", err)
	}
	return resp
}

func TestAssetUpload_EndToEnd_RealPostgresAndMinIO(t *testing.T) {
	ctx := context.Background()
	pool, workspaceID := setupRuntimePool(t, ctx)
	objStore, storageClient, mc, bucket := setupRealObjectStore(t, ctx)

	gw := &Gateway{
		WithinTx: NewTxRunner(pool),
		Now:      func() time.Time { return time.Unix(1_700_000_000, 0) },
		Storage:  objStore,
	}

	body := []byte("this is the recorded evidence video, for real this time")

	// --- request -------------------------------------------------------
	reqRes, err := gw.RequestAssetUpload(ctx, workspaceID, "cap_int", "asset_int", "video/mp4", int64(len(body)), sha256Hex(body))
	if err != nil {
		t.Fatalf("RequestAssetUpload: %v", err)
	}
	if reqRes.Reason != AssetReasonNone {
		t.Fatalf("unexpected rejection: %q", reqRes.Reason)
	}

	// --- PUT, real HTTP against the real presigned URL ------------------
	resp := putViaPresign(t, reqRes.UploadURL, reqRes.RequiredHeaders, body)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT with required headers failed: status=%d body=%s", resp.StatusCode, respBody)
	}

	// --- complete + verify -----------------------------------------------
	completeRes, err := gw.CompleteAssetUpload(ctx, workspaceID, "cap_int", "asset_int")
	if err != nil {
		t.Fatalf("CompleteAssetUpload: %v", err)
	}
	if !completeRes.Verified {
		t.Fatalf("expected verified, got %+v", completeRes)
	}
	if !completeRes.ManifestComplete {
		t.Fatalf("expected manifest complete with the only asset verified, got %+v", completeRes)
	}

	// --- replay: a second PUT to the same key -----------------------------
	// Whether MinIO rejects this at the HTTP layer depends on this build's
	// support for If-None-Match conditional writes; the guarantee the
	// service itself makes does not depend on that — CompleteAssetUpload's
	// presign-consumption check already refuses to re-verify. What must
	// hold either way is that the object bytes remain exactly what was
	// verified.
	replayResp := putViaPresign(t, reqRes.UploadURL, reqRes.RequiredHeaders, []byte("attempted overwrite, different length"))
	defer func() { _ = replayResp.Body.Close() }()
	t.Logf("replay PUT to already-uploaded key: status=%d (informational — see task report for what this MinIO build enforces)", replayResp.StatusCode)

	readBack, err := objStore.HashObject(ctx, reqRes.ObjectKey)
	if err != nil {
		t.Fatalf("hash object after replay attempt: %v", err)
	}
	if readBack != sha256Hex(body) {
		t.Fatalf("object content changed after replay PUT: the verified upload must be left exactly as verified")
	}

	// The replay must never re-verify or re-flip manifest_complete via the
	// service layer, independent of what the store did with the second PUT.
	replayComplete, err := gw.CompleteAssetUpload(ctx, workspaceID, "cap_int", "asset_int")
	if err != nil {
		t.Fatalf("CompleteAssetUpload replay: %v", err)
	}
	if replayComplete.Verified || replayComplete.Reason != AssetReasonAlreadyConsumed {
		t.Fatalf("want presign_already_consumed on replay, got %+v", replayComplete)
	}

	// --- no public bucket policy, and anonymous reads are denied -----------
	policy, err := storageClient.CurrentBucketPolicy(ctx)
	if err != nil {
		t.Fatalf("get bucket policy: %v", err)
	}
	if policy != "" {
		t.Fatalf("expected no bucket policy set, got %q", policy)
	}

	anonMC, err := minio.New(mc.EndpointURL().Host, &minio.Options{Creds: credentials.NewStaticV4("", "", "")})
	if err != nil {
		t.Fatalf("new anonymous minio client: %v", err)
	}
	obj, err := anonMC.GetObject(ctx, bucket, reqRes.ObjectKey, minio.GetObjectOptions{})
	if err == nil {
		_, err = obj.Stat()
	}
	if err == nil {
		t.Fatalf("expected anonymous read to be denied")
	}
}

func TestAssetUpload_SizeAndHashMismatch_RealMinIO(t *testing.T) {
	ctx := context.Background()
	pool, workspaceID := setupRuntimePool(t, ctx)
	objStore, _, _, _ := setupRealObjectStore(t, ctx)
	gw := &Gateway{WithinTx: NewTxRunner(pool), Now: func() time.Time { return time.Unix(1_700_000_000, 0) }, Storage: objStore}

	t.Run("size mismatch", func(t *testing.T) {
		// The checksum header binds a HASH, not a length, so a body of the
		// "wrong" size that also has the "wrong" hash would be rejected at
		// PUT time for the wrong reason (checksum mismatch, not size). To
		// isolate a pure size mismatch, the client here declares a size
		// that doesn't match its own actual (truthfully-hashed) body — the
		// PUT succeeds because the hash it claims is honest, but
		// CompleteAssetUpload must still catch the size lie.
		actual := []byte("short")
		res, err := gw.RequestAssetUpload(ctx, workspaceID, "cap_int", "asset_size", "video/mp4", 999, sha256Hex(actual))
		if err != nil {
			t.Fatalf("RequestAssetUpload: %v", err)
		}
		resp := putViaPresign(t, res.UploadURL, res.RequiredHeaders, actual)
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode/100 != 2 {
			t.Fatalf("PUT failed: status=%d", resp.StatusCode)
		}

		complete, err := gw.CompleteAssetUpload(ctx, workspaceID, "cap_int", "asset_size")
		if err != nil {
			t.Fatalf("CompleteAssetUpload: %v", err)
		}
		if complete.Verified || complete.ManifestComplete || complete.Reason != AssetReasonSizeMismatch {
			t.Fatalf("want size_mismatch, unverified, incomplete; got %+v", complete)
		}
	})

	t.Run("hash mismatch", func(t *testing.T) {
		declared := []byte("same-length-declared!!!")
		tampered := []byte("same-length-TAMPERED!!!")
		res, err := gw.RequestAssetUpload(ctx, workspaceID, "cap_int", "asset_hash", "video/mp4", int64(len(declared)), sha256Hex(declared))
		if err != nil {
			t.Fatalf("RequestAssetUpload: %v", err)
		}
		// The required checksum header is bound to the DECLARED hash; a
		// tampered body under that same signed header still uploads (the
		// signature only proves the client claimed that checksum, not that
		// MinIO validated the body against it in every configuration) — so
		// this exercises CompleteAssetUpload's own read-and-hash check,
		// which is what the brief requires to be trustworthy regardless.
		resp := putViaPresign(t, res.UploadURL, res.RequiredHeaders, tampered)
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode/100 == 2 {
			complete, err := gw.CompleteAssetUpload(ctx, workspaceID, "cap_int", "asset_hash")
			if err != nil {
				t.Fatalf("CompleteAssetUpload: %v", err)
			}
			if complete.Verified || complete.ManifestComplete || complete.Reason != AssetReasonHashMismatch {
				t.Fatalf("want hash_mismatch, unverified, incomplete; got %+v", complete)
			}
		} else {
			// This MinIO build rejected the mismatched body at PUT time
			// (checksum-header validation) — stronger than what the brief
			// requires, not a failure of it. Confirm CompleteAssetUpload
			// still correctly reports not-uploaded rather than crashing.
			complete, err := gw.CompleteAssetUpload(ctx, workspaceID, "cap_int", "asset_hash")
			if err != nil {
				t.Fatalf("CompleteAssetUpload: %v", err)
			}
			if complete.Verified {
				t.Fatalf("want unverified, got %+v", complete)
			}
		}
	})
}
