package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"testing"
	"time"
)

// --- fakeStore asset methods -----------------------------------------------

func (f *fakeStore) GetAssetForWorkspace(_ context.Context, assetID, captureID, workspaceID string) (Asset, bool, error) {
	if f.getAssetErr != nil {
		return Asset{}, false, f.getAssetErr
	}
	a, ok := f.assets[assetID]
	if !ok || a.CaptureID != captureID {
		return Asset{}, false, nil
	}
	c, ok := f.captures[captureID]
	if !ok || c.WorkspaceID != workspaceID {
		return Asset{}, false, nil
	}
	return a, true, nil
}

func (f *fakeStore) UpsertAssetForUpload(_ context.Context, asset Asset) (Asset, error) {
	if f.upsertAssetErr != nil {
		return Asset{}, f.upsertAssetErr
	}
	existing, ok := f.assets[asset.ID]
	if ok {
		existing.ObjectKey = asset.ObjectKey
		f.assets[asset.ID] = existing
		return existing, nil
	}
	f.assets[asset.ID] = asset
	return asset, nil
}

func (f *fakeStore) CreateAssetUploadPresign(_ context.Context, p AssetUploadPresign) error {
	if f.createPresignErr != nil {
		return f.createPresignErr
	}
	f.presigns[p.ObjectKey] = p
	return nil
}

func (f *fakeStore) GetAssetUploadPresignByKey(_ context.Context, objectKey string) (AssetUploadPresign, bool, error) {
	if f.getPresignErr != nil {
		return AssetUploadPresign{}, false, f.getPresignErr
	}
	p, ok := f.presigns[objectKey]
	return p, ok, nil
}

func (f *fakeStore) ConsumeAssetUploadPresign(_ context.Context, objectKey string) (bool, error) {
	if f.consumePresignErr != nil {
		return false, f.consumePresignErr
	}
	if _, ok := f.presigns[objectKey]; !ok {
		return false, nil
	}
	if f.consumedPresigns[objectKey] {
		return false, nil
	}
	f.consumedPresigns[objectKey] = true
	return true, nil
}

func (f *fakeStore) MarkAssetVerified(_ context.Context, assetID, sha256Hex string, sizeBytes int64) error {
	if f.markVerifiedErr != nil {
		return f.markVerifiedErr
	}
	a := f.assets[assetID]
	a.Sha256 = sha256Hex
	a.SizeBytes = sizeBytes
	a.Verified = true
	f.assets[assetID] = a
	return nil
}

func (f *fakeStore) ManifestComplete(_ context.Context, captureID string) (bool, error) {
	if f.manifestCompleteErr != nil {
		return false, f.manifestCompleteErr
	}
	total, unverified := 0, 0
	for _, a := range f.assets {
		if a.CaptureID != captureID {
			continue
		}
		total++
		if !a.Verified {
			unverified++
		}
	}
	return total > 0 && unverified == 0, nil
}

func (f *fakeStore) SetCaptureManifestComplete(_ context.Context, captureID string, complete bool) error {
	if f.setManifestErr != nil {
		return f.setManifestErr
	}
	f.manifestComplete[captureID] = complete
	return nil
}

// --- fake ObjectStore --------------------------------------------------------

type fakeObject struct {
	body []byte
}

// fakeObjectStore is an in-memory ObjectStore: presign issuance just records
// the key/expiry, and PUT is simulated directly via put() rather than real
// HTTP, since the gateway layer's job ends at handing back a URL — actually
// exercising the presigned URL against a body is covered by the storage
// package's MinIO integration tests.
type fakeObjectStore struct {
	objects map[string]fakeObject

	presignErr error
	statErr    error
	hashErr    error
}

func newFakeObjectStore() *fakeObjectStore {
	return &fakeObjectStore{objects: map[string]fakeObject{}}
}

func (f *fakeObjectStore) PresignPutChecksummed(_ context.Context, key string, _ time.Duration, sha256Hex string) (*url.URL, map[string]string, error) {
	if f.presignErr != nil {
		return nil, nil, f.presignErr
	}
	u, _ := url.Parse("https://minio.example/" + key)
	return u, map[string]string{
		"x-amz-checksum-sha256": sha256Hex,
		"If-None-Match":         "*",
	}, nil
}

func (f *fakeObjectStore) StatSize(_ context.Context, key string) (bool, int64, error) {
	if f.statErr != nil {
		return false, 0, f.statErr
	}
	obj, ok := f.objects[key]
	if !ok {
		return false, 0, nil
	}
	return true, int64(len(obj.body)), nil
}

func (f *fakeObjectStore) HashObject(_ context.Context, key string) (string, error) {
	if f.hashErr != nil {
		return "", f.hashErr
	}
	obj, ok := f.objects[key]
	if !ok {
		return "", errors.New("fakeObjectStore: no such object")
	}
	sum := sha256.Sum256(obj.body)
	return hex.EncodeToString(sum[:]), nil
}

func (f *fakeObjectStore) put(key string, body []byte) {
	f.objects[key] = fakeObject{body: body}
}

// --- test helpers ------------------------------------------------------------

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func newAssetTestGateway(store *fakeStore, os *fakeObjectStore, now time.Time) *Gateway {
	return &Gateway{
		WithinTx: withinTx(store),
		Now:      func() time.Time { return now },
		Storage:  os,
	}
}

const (
	testWorkspace = "ws_1"
	testCapture   = "cap_1"
	testAsset     = "asset_1"
)

func seedAssetCapture(store *fakeStore) {
	store.captures[testCapture] = Capture{ID: testCapture, WorkspaceID: testWorkspace, Revision: 0}
}

// --- RequestAssetUpload -------------------------------------------------------

func TestRequestAssetUpload_HappyPath(t *testing.T) {
	store := newFakeStore()
	seedAssetCapture(store)
	os := newFakeObjectStore()
	body := []byte("video bytes")
	gw := newAssetTestGateway(store, os, time.Unix(1000, 0).UTC())

	res, err := gw.RequestAssetUpload(context.Background(), testWorkspace, testCapture, testAsset, "video/mp4", int64(len(body)), sha256Hex(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Reason != AssetReasonNone {
		t.Fatalf("expected no rejection, got %q", res.Reason)
	}
	if res.ObjectKey == "" || res.UploadURL == "" {
		t.Fatalf("expected populated object key and url, got %+v", res)
	}
	if res.RequiredHeaders["x-amz-checksum-sha256"] == "" || res.RequiredHeaders["If-None-Match"] != "*" {
		t.Fatalf("expected required headers to be populated, got %v", res.RequiredHeaders)
	}
	if !res.ExpiresAt.After(time.Unix(1000, 0).UTC()) {
		t.Fatalf("expected expiry in the future")
	}

	asset, ok := store.assets[testAsset]
	if !ok || asset.ObjectKey != res.ObjectKey {
		t.Fatalf("expected asset row created with returned object key, got %+v", asset)
	}
	if asset.Kind != "video" {
		t.Fatalf("expected kind video for video/mp4, got %q", asset.Kind)
	}
}

func TestRequestAssetUpload_NonVideoMimeIsScreenshot(t *testing.T) {
	store := newFakeStore()
	seedAssetCapture(store)
	gw := newAssetTestGateway(store, newFakeObjectStore(), time.Unix(0, 0))

	_, err := gw.RequestAssetUpload(context.Background(), testWorkspace, testCapture, testAsset, "image/png", 10, sha256Hex([]byte("x")))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.assets[testAsset].Kind != "screenshot" {
		t.Fatalf("expected kind screenshot, got %q", store.assets[testAsset].Kind)
	}
}

func TestRequestAssetUpload_InvalidRequest(t *testing.T) {
	store := newFakeStore()
	seedAssetCapture(store)
	gw := newAssetTestGateway(store, newFakeObjectStore(), time.Unix(0, 0))

	cases := []struct {
		name      string
		captureID string
		assetID   string
		size      int64
		sha       string
	}{
		{"missing capture", "", testAsset, 10, sha256Hex([]byte("x"))},
		{"missing asset", testCapture, "", 10, sha256Hex([]byte("x"))},
		{"zero size", testCapture, testAsset, 0, sha256Hex([]byte("x"))},
		{"negative size", testCapture, testAsset, -1, sha256Hex([]byte("x"))},
		{"bad sha256", testCapture, testAsset, 10, "not-hex"},
		{"short sha256", testCapture, testAsset, 10, "abcd"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := gw.RequestAssetUpload(context.Background(), testWorkspace, tc.captureID, tc.assetID, "video/mp4", tc.size, tc.sha)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res.Reason != AssetReasonInvalidRequest {
				t.Fatalf("expected invalid_request, got %q", res.Reason)
			}
		})
	}
}

func TestRequestAssetUpload_CaptureNotFound(t *testing.T) {
	store := newFakeStore()
	gw := newAssetTestGateway(store, newFakeObjectStore(), time.Unix(0, 0))

	res, err := gw.RequestAssetUpload(context.Background(), testWorkspace, "missing-capture", testAsset, "video/mp4", 10, sha256Hex([]byte("x")))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Reason != AssetReasonNotFound {
		t.Fatalf("expected not_found, got %q", res.Reason)
	}
}

func TestRequestAssetUpload_CaptureInOtherWorkspaceIsNotFound(t *testing.T) {
	store := newFakeStore()
	seedAssetCapture(store)
	gw := newAssetTestGateway(store, newFakeObjectStore(), time.Unix(0, 0))

	res, err := gw.RequestAssetUpload(context.Background(), "other-workspace", testCapture, testAsset, "video/mp4", 10, sha256Hex([]byte("x")))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Reason != AssetReasonNotFound {
		t.Fatalf("expected not_found, got %q", res.Reason)
	}
}

func TestRequestAssetUpload_ReRequestYieldsFreshKeyNeverReused(t *testing.T) {
	store := newFakeStore()
	seedAssetCapture(store)
	gw := newAssetTestGateway(store, newFakeObjectStore(), time.Unix(0, 0))
	ctx := context.Background()
	body := []byte("v")

	first, err := gw.RequestAssetUpload(ctx, testWorkspace, testCapture, testAsset, "video/mp4", int64(len(body)), sha256Hex(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, err := gw.RequestAssetUpload(ctx, testWorkspace, testCapture, testAsset, "video/mp4", int64(len(body)), sha256Hex(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if first.ObjectKey == second.ObjectKey {
		t.Fatalf("expected a fresh object key on re-request, got the same key twice: %q", first.ObjectKey)
	}
	if store.assets[testAsset].ObjectKey != second.ObjectKey {
		t.Fatalf("expected asset row to point at the newest key")
	}
}

func TestRequestAssetUpload_ReRequestAfterVerifiedDoesNotOverwriteVerifiedState(t *testing.T) {
	store := newFakeStore()
	seedAssetCapture(store)
	os := newFakeObjectStore()
	gw := newAssetTestGateway(store, os, time.Unix(0, 0))
	ctx := context.Background()
	body := []byte("v")

	first, err := gw.RequestAssetUpload(ctx, testWorkspace, testCapture, testAsset, "video/mp4", int64(len(body)), sha256Hex(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	os.put(first.ObjectKey, body)
	completeRes, err := gw.CompleteAssetUpload(ctx, testWorkspace, testCapture, testAsset)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !completeRes.Verified || !completeRes.ManifestComplete {
		t.Fatalf("expected verified+complete, got %+v", completeRes)
	}

	second, err := gw.RequestAssetUpload(ctx, testWorkspace, testCapture, testAsset, "video/mp4", int64(len(body)), sha256Hex(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if second.ObjectKey == first.ObjectKey {
		t.Fatalf("expected a fresh key even after verification")
	}
	// Nothing has flipped verification off — the asset is still verified in
	// the store until (and unless) the new key is itself completed.
	if !store.assets[testAsset].Verified {
		t.Fatalf("expected asset to remain verified after a bare re-request")
	}
}

func TestRequestAssetUpload_InfraErrors(t *testing.T) {
	body := []byte("x")

	t.Run("lock error", func(t *testing.T) {
		store := newFakeStore()
		store.lockErr = errors.New("boom")
		gw := newAssetTestGateway(store, newFakeObjectStore(), time.Unix(0, 0))
		if _, err := gw.RequestAssetUpload(context.Background(), testWorkspace, testCapture, testAsset, "video/mp4", int64(len(body)), sha256Hex(body)); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("upsert asset error", func(t *testing.T) {
		store := newFakeStore()
		seedAssetCapture(store)
		store.upsertAssetErr = errors.New("boom")
		gw := newAssetTestGateway(store, newFakeObjectStore(), time.Unix(0, 0))
		if _, err := gw.RequestAssetUpload(context.Background(), testWorkspace, testCapture, testAsset, "video/mp4", int64(len(body)), sha256Hex(body)); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("manifest complete check error", func(t *testing.T) {
		store := newFakeStore()
		seedAssetCapture(store)
		store.manifestCompleteErr = errors.New("boom")
		gw := newAssetTestGateway(store, newFakeObjectStore(), time.Unix(0, 0))
		if _, err := gw.RequestAssetUpload(context.Background(), testWorkspace, testCapture, testAsset, "video/mp4", int64(len(body)), sha256Hex(body)); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("set manifest complete error", func(t *testing.T) {
		store := newFakeStore()
		seedAssetCapture(store)
		store.setManifestErr = errors.New("boom")
		gw := newAssetTestGateway(store, newFakeObjectStore(), time.Unix(0, 0))
		if _, err := gw.RequestAssetUpload(context.Background(), testWorkspace, testCapture, testAsset, "video/mp4", int64(len(body)), sha256Hex(body)); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("create presign error", func(t *testing.T) {
		store := newFakeStore()
		seedAssetCapture(store)
		store.createPresignErr = errors.New("boom")
		gw := newAssetTestGateway(store, newFakeObjectStore(), time.Unix(0, 0))
		if _, err := gw.RequestAssetUpload(context.Background(), testWorkspace, testCapture, testAsset, "video/mp4", int64(len(body)), sha256Hex(body)); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("presign put error", func(t *testing.T) {
		store := newFakeStore()
		seedAssetCapture(store)
		os := newFakeObjectStore()
		os.presignErr = errors.New("boom")
		gw := newAssetTestGateway(store, os, time.Unix(0, 0))
		if _, err := gw.RequestAssetUpload(context.Background(), testWorkspace, testCapture, testAsset, "video/mp4", int64(len(body)), sha256Hex(body)); err == nil {
			t.Fatal("expected error")
		}
	})
}

// --- CompleteAssetUpload -------------------------------------------------------

func requestAndUpload(t *testing.T, gw *Gateway, os *fakeObjectStore, body []byte) string {
	t.Helper()
	res, err := gw.RequestAssetUpload(context.Background(), testWorkspace, testCapture, testAsset, "video/mp4", int64(len(body)), sha256Hex(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Reason != AssetReasonNone {
		t.Fatalf("unexpected rejection: %q", res.Reason)
	}
	os.put(res.ObjectKey, body)
	return res.ObjectKey
}

func TestCompleteAssetUpload_HappyPath(t *testing.T) {
	store := newFakeStore()
	seedAssetCapture(store)
	os := newFakeObjectStore()
	gw := newAssetTestGateway(store, os, time.Unix(0, 0))
	body := []byte("evidence bytes")
	requestAndUpload(t, gw, os, body)

	res, err := gw.CompleteAssetUpload(context.Background(), testWorkspace, testCapture, testAsset)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Verified {
		t.Fatalf("expected verified, got %+v", res)
	}
	if !res.ManifestComplete {
		t.Fatalf("expected manifest complete with the only asset verified, got %+v", res)
	}
	if !store.manifestComplete[testCapture] {
		t.Fatalf("expected manifest_complete persisted true")
	}
}

// TestRequestAssetUpload_ExtendingManifestAfterCompleteGoesStale guards
// against manifest_complete going stale after the manifest is extended: once
// one asset is verified and manifest_complete flips true, requesting a
// second asset on the same capture must immediately re-flip it false — not
// leave it stale until the next CompleteAssetUpload — and it must flip back
// true once that second asset is also verified.
func TestRequestAssetUpload_ExtendingManifestAfterCompleteGoesStale(t *testing.T) {
	store := newFakeStore()
	seedAssetCapture(store)
	os := newFakeObjectStore()
	gw := newAssetTestGateway(store, os, time.Unix(0, 0))
	body := []byte("evidence bytes")
	requestAndUpload(t, gw, os, body)

	res, err := gw.CompleteAssetUpload(context.Background(), testWorkspace, testCapture, testAsset)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.ManifestComplete || !store.manifestComplete[testCapture] {
		t.Fatalf("expected manifest complete after sole asset verified, got %+v", res)
	}

	const secondAsset = "asset_2"
	body2 := []byte("more evidence")
	if _, err := gw.RequestAssetUpload(context.Background(), testWorkspace, testCapture, secondAsset, "video/mp4", int64(len(body2)), sha256Hex(body2)); err != nil {
		t.Fatalf("unexpected error requesting second asset: %v", err)
	}
	if store.manifestComplete[testCapture] {
		t.Fatalf("expected manifest_complete to go false once an unverified second asset is added")
	}

	res2, err := gw.RequestAssetUpload(context.Background(), testWorkspace, testCapture, secondAsset, "video/mp4", int64(len(body2)), sha256Hex(body2))
	if err != nil {
		t.Fatalf("unexpected error re-requesting second asset: %v", err)
	}
	os.put(res2.ObjectKey, body2)

	completeRes, err := gw.CompleteAssetUpload(context.Background(), testWorkspace, testCapture, secondAsset)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !completeRes.ManifestComplete || !store.manifestComplete[testCapture] {
		t.Fatalf("expected manifest complete again once both assets verified, got %+v", completeRes)
	}
}

func TestCompleteAssetUpload_PartialManifestStaysIncomplete(t *testing.T) {
	store := newFakeStore()
	seedAssetCapture(store)
	os := newFakeObjectStore()
	gw := newAssetTestGateway(store, os, time.Unix(0, 0))

	// A second, never-uploaded asset on the same capture.
	store.assets["asset_2"] = Asset{ID: "asset_2", CaptureID: testCapture, ObjectKey: "captures/cap_1/assets/asset_2/x"}

	body := []byte("evidence bytes")
	requestAndUpload(t, gw, os, body)

	res, err := gw.CompleteAssetUpload(context.Background(), testWorkspace, testCapture, testAsset)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Verified {
		t.Fatalf("expected this asset verified, got %+v", res)
	}
	if res.ManifestComplete {
		t.Fatalf("expected manifest incomplete while asset_2 is unverified")
	}
}

func TestCompleteAssetUpload_SizeMismatchLeavesManifestIncomplete(t *testing.T) {
	store := newFakeStore()
	seedAssetCapture(store)
	os := newFakeObjectStore()
	gw := newAssetTestGateway(store, os, time.Unix(0, 0))
	declared := []byte("declared body, sixteen")
	res0, err := gw.RequestAssetUpload(context.Background(), testWorkspace, testCapture, testAsset, "video/mp4", int64(len(declared)), sha256Hex(declared))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Upload a body of a DIFFERENT size than declared.
	os.put(res0.ObjectKey, []byte("short"))

	res, err := gw.CompleteAssetUpload(context.Background(), testWorkspace, testCapture, testAsset)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Verified {
		t.Fatalf("expected unverified on size mismatch")
	}
	if res.Reason != AssetReasonSizeMismatch {
		t.Fatalf("expected size_mismatch, got %q", res.Reason)
	}
	if res.ManifestComplete || store.manifestComplete[testCapture] {
		t.Fatalf("expected manifest to stay incomplete on mismatch")
	}
	if store.assets[testAsset].Verified {
		t.Fatalf("expected asset to remain unverified")
	}
}

func TestCompleteAssetUpload_HashMismatchLeavesManifestIncomplete(t *testing.T) {
	store := newFakeStore()
	seedAssetCapture(store)
	os := newFakeObjectStore()
	gw := newAssetTestGateway(store, os, time.Unix(0, 0))
	declared := []byte("declared body same len!")
	res0, err := gw.RequestAssetUpload(context.Background(), testWorkspace, testCapture, testAsset, "video/mp4", int64(len(declared)), sha256Hex(declared))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Same length, different content -> same size, different hash.
	tampered := []byte(string(declared[:len(declared)-1]) + "?")
	os.put(res0.ObjectKey, tampered)

	res, err := gw.CompleteAssetUpload(context.Background(), testWorkspace, testCapture, testAsset)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Verified {
		t.Fatalf("expected unverified on hash mismatch")
	}
	if res.Reason != AssetReasonHashMismatch {
		t.Fatalf("expected hash_mismatch, got %q", res.Reason)
	}
	if store.manifestComplete[testCapture] {
		t.Fatalf("expected manifest to stay incomplete on mismatch")
	}
	// The client's copy is never touched — object still present, unchanged.
	obj, ok := os.objects[res0.ObjectKey]
	if !ok || string(obj.body) != string(tampered) {
		t.Fatalf("expected mismatched object to be left exactly as uploaded")
	}
}

func TestCompleteAssetUpload_ReplayAfterVerificationDoesNotReflip(t *testing.T) {
	store := newFakeStore()
	seedAssetCapture(store)
	os := newFakeObjectStore()
	gw := newAssetTestGateway(store, os, time.Unix(0, 0))
	body := []byte("evidence bytes")
	requestAndUpload(t, gw, os, body)

	first, err := gw.CompleteAssetUpload(context.Background(), testWorkspace, testCapture, testAsset)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !first.Verified || !first.ManifestComplete {
		t.Fatalf("expected first completion to verify and complete, got %+v", first)
	}

	replay, err := gw.CompleteAssetUpload(context.Background(), testWorkspace, testCapture, testAsset)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if replay.Verified {
		t.Fatalf("expected replay to report unverified, got %+v", replay)
	}
	if replay.Reason != AssetReasonAlreadyConsumed {
		t.Fatalf("expected presign_already_consumed, got %q", replay.Reason)
	}
	// manifest_complete is unaffected by the replay either way.
	if !store.manifestComplete[testCapture] {
		t.Fatalf("expected manifest to remain complete after a no-op replay")
	}
}

func TestCompleteAssetUpload_ExpiredPresignIsRefused(t *testing.T) {
	store := newFakeStore()
	seedAssetCapture(store)
	os := newFakeObjectStore()
	start := time.Unix(1_700_000_000, 0).UTC()
	gw := newAssetTestGateway(store, os, start)
	body := []byte("evidence bytes")
	objectKey := requestAndUpload(t, gw, os, body)

	// Advance the clock past the presign's expiry (min expiry is 5 minutes).
	gw.Now = func() time.Time { return start.Add(time.Hour) }

	res, err := gw.CompleteAssetUpload(context.Background(), testWorkspace, testCapture, testAsset)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Verified {
		t.Fatalf("expected unverified for an expired presign")
	}
	if res.Reason != AssetReasonPresignExpired {
		t.Fatalf("expected presign_expired, got %q", res.Reason)
	}
	if _, ok := store.presigns[objectKey]; !ok {
		t.Fatalf("expected presign row to still exist, unconsumed")
	}
}

func TestCompleteAssetUpload_NotUploadedYet(t *testing.T) {
	store := newFakeStore()
	seedAssetCapture(store)
	os := newFakeObjectStore()
	gw := newAssetTestGateway(store, os, time.Unix(0, 0))

	res0, err := gw.RequestAssetUpload(context.Background(), testWorkspace, testCapture, testAsset, "video/mp4", 5, sha256Hex([]byte("hello")))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = res0

	res, err := gw.CompleteAssetUpload(context.Background(), testWorkspace, testCapture, testAsset)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Reason != AssetReasonNotUploaded {
		t.Fatalf("expected not_uploaded, got %q", res.Reason)
	}
}

func TestCompleteAssetUpload_InvalidRequest(t *testing.T) {
	gw := newAssetTestGateway(newFakeStore(), newFakeObjectStore(), time.Unix(0, 0))
	res, err := gw.CompleteAssetUpload(context.Background(), testWorkspace, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Reason != AssetReasonInvalidRequest {
		t.Fatalf("expected invalid_request, got %q", res.Reason)
	}
}

func TestCompleteAssetUpload_AssetNotFound(t *testing.T) {
	store := newFakeStore()
	seedAssetCapture(store)
	gw := newAssetTestGateway(store, newFakeObjectStore(), time.Unix(0, 0))

	res, err := gw.CompleteAssetUpload(context.Background(), testWorkspace, testCapture, "missing-asset")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Reason != AssetReasonNotFound {
		t.Fatalf("expected not_found, got %q", res.Reason)
	}
}

func TestCompleteAssetUpload_AssetInOtherWorkspaceIsNotFound(t *testing.T) {
	store := newFakeStore()
	seedAssetCapture(store)
	os := newFakeObjectStore()
	gw := newAssetTestGateway(store, os, time.Unix(0, 0))
	requestAndUpload(t, gw, os, []byte("x"))

	res, err := gw.CompleteAssetUpload(context.Background(), "other-workspace", testCapture, testAsset)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Reason != AssetReasonNotFound {
		t.Fatalf("expected not_found, got %q", res.Reason)
	}
}

func TestCompleteAssetUpload_MissingPresignRowIsNotFound(t *testing.T) {
	store := newFakeStore()
	seedAssetCapture(store)
	store.assets[testAsset] = Asset{ID: testAsset, CaptureID: testCapture, ObjectKey: "orphan-key"}
	gw := newAssetTestGateway(store, newFakeObjectStore(), time.Unix(0, 0))

	res, err := gw.CompleteAssetUpload(context.Background(), testWorkspace, testCapture, testAsset)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Reason != AssetReasonNotFound {
		t.Fatalf("expected not_found for a presign-less object key, got %q", res.Reason)
	}
}

func TestCompleteAssetUpload_InfraErrors(t *testing.T) {
	t.Run("get asset error", func(t *testing.T) {
		store := newFakeStore()
		seedAssetCapture(store)
		store.getAssetErr = errors.New("boom")
		gw := newAssetTestGateway(store, newFakeObjectStore(), time.Unix(0, 0))
		if _, err := gw.CompleteAssetUpload(context.Background(), testWorkspace, testCapture, testAsset); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("get presign error", func(t *testing.T) {
		store := newFakeStore()
		seedAssetCapture(store)
		os := newFakeObjectStore()
		requestAndUpload(t, &Gateway{WithinTx: withinTx(store), Now: func() time.Time { return time.Unix(0, 0) }, Storage: os}, os, []byte("x"))
		store.getPresignErr = errors.New("boom")
		gw := newAssetTestGateway(store, os, time.Unix(0, 0))
		if _, err := gw.CompleteAssetUpload(context.Background(), testWorkspace, testCapture, testAsset); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("consume presign error", func(t *testing.T) {
		store := newFakeStore()
		seedAssetCapture(store)
		os := newFakeObjectStore()
		requestAndUpload(t, &Gateway{WithinTx: withinTx(store), Now: func() time.Time { return time.Unix(0, 0) }, Storage: os}, os, []byte("x"))
		store.consumePresignErr = errors.New("boom")
		gw := newAssetTestGateway(store, os, time.Unix(0, 0))
		if _, err := gw.CompleteAssetUpload(context.Background(), testWorkspace, testCapture, testAsset); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("stat error", func(t *testing.T) {
		store := newFakeStore()
		seedAssetCapture(store)
		os := newFakeObjectStore()
		requestAndUpload(t, &Gateway{WithinTx: withinTx(store), Now: func() time.Time { return time.Unix(0, 0) }, Storage: os}, os, []byte("x"))
		os.statErr = errors.New("boom")
		gw := newAssetTestGateway(store, os, time.Unix(0, 0))
		if _, err := gw.CompleteAssetUpload(context.Background(), testWorkspace, testCapture, testAsset); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("hash error", func(t *testing.T) {
		store := newFakeStore()
		seedAssetCapture(store)
		os := newFakeObjectStore()
		requestAndUpload(t, &Gateway{WithinTx: withinTx(store), Now: func() time.Time { return time.Unix(0, 0) }, Storage: os}, os, []byte("x"))
		os.hashErr = errors.New("boom")
		gw := newAssetTestGateway(store, os, time.Unix(0, 0))
		if _, err := gw.CompleteAssetUpload(context.Background(), testWorkspace, testCapture, testAsset); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("mark verified error", func(t *testing.T) {
		store := newFakeStore()
		seedAssetCapture(store)
		os := newFakeObjectStore()
		requestAndUpload(t, &Gateway{WithinTx: withinTx(store), Now: func() time.Time { return time.Unix(0, 0) }, Storage: os}, os, []byte("x"))
		store.markVerifiedErr = errors.New("boom")
		gw := newAssetTestGateway(store, os, time.Unix(0, 0))
		if _, err := gw.CompleteAssetUpload(context.Background(), testWorkspace, testCapture, testAsset); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("manifest complete check error", func(t *testing.T) {
		store := newFakeStore()
		seedAssetCapture(store)
		os := newFakeObjectStore()
		requestAndUpload(t, &Gateway{WithinTx: withinTx(store), Now: func() time.Time { return time.Unix(0, 0) }, Storage: os}, os, []byte("x"))
		store.manifestCompleteErr = errors.New("boom")
		gw := newAssetTestGateway(store, os, time.Unix(0, 0))
		if _, err := gw.CompleteAssetUpload(context.Background(), testWorkspace, testCapture, testAsset); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("set manifest complete error", func(t *testing.T) {
		store := newFakeStore()
		seedAssetCapture(store)
		os := newFakeObjectStore()
		requestAndUpload(t, &Gateway{WithinTx: withinTx(store), Now: func() time.Time { return time.Unix(0, 0) }, Storage: os}, os, []byte("x"))
		store.setManifestErr = errors.New("boom")
		gw := newAssetTestGateway(store, os, time.Unix(0, 0))
		if _, err := gw.CompleteAssetUpload(context.Background(), testWorkspace, testCapture, testAsset); err == nil {
			t.Fatal("expected error")
		}
	})
}

// --- pure helper functions ----------------------------------------------------

func TestPresignExpiryFor(t *testing.T) {
	cases := []struct {
		name string
		size int64
		want time.Duration
	}{
		{"zero", 0, 5 * time.Minute},
		{"negative", -1, 5 * time.Minute},
		{"small", 1024, 5 * time.Minute},
		{"200mib", 200 * 1024 * 1024, 6 * time.Minute},
		{"huge caps at max", 100 * 1024 * 1024 * 1024, 30 * time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := presignExpiryFor(tc.size); got != tc.want {
				t.Fatalf("presignExpiryFor(%d) = %v, want %v", tc.size, got, tc.want)
			}
		})
	}
}

func TestAssetKindFor(t *testing.T) {
	if assetKindFor("video/mp4") != "video" {
		t.Fatal("expected video")
	}
	if assetKindFor("image/png") != "screenshot" {
		t.Fatal("expected screenshot")
	}
	if assetKindFor("") != "screenshot" {
		t.Fatal("expected screenshot for empty mime type")
	}
}

func TestIsValidSHA256Hex(t *testing.T) {
	if !isValidSHA256Hex(sha256Hex([]byte("x"))) {
		t.Fatal("expected valid")
	}
	if isValidSHA256Hex("") {
		t.Fatal("expected invalid for empty")
	}
	if isValidSHA256Hex("zz") {
		t.Fatal("expected invalid for too short")
	}
	if isValidSHA256Hex("gg" + sha256Hex([]byte("x"))[2:]) {
		t.Fatal("expected invalid for non-hex characters")
	}
}
