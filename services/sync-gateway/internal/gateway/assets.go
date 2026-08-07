// Package gateway: asset upload negotiation and manifest verification. See
// Task 5 brief and ADR-012 "Assets" — the server selects the object key, a
// presign is single-use, and sync.manifestComplete flips true only once
// every asset on a capture has verified against the size/hash declared when
// the upload was requested (not against anything reported at completion).
package gateway

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// AssetReason is the closed, machine-actionable rejection vocabulary for
// asset-upload RPCs, mirroring Reason's role for PushMutations.
type AssetReason string

const (
	// AssetReasonNone means the request succeeded; nothing was rejected.
	AssetReasonNone AssetReason = ""
	// AssetReasonNotFound covers "no such capture" and "capture/asset in a
	// different workspace" identically — same non-disclosure rationale as
	// PushMutations' ReasonNotFound.
	AssetReasonNotFound AssetReason = "not_found"
	// AssetReasonInvalidRequest covers structurally invalid input: missing
	// IDs, non-positive size, or a malformed sha256.
	AssetReasonInvalidRequest AssetReason = "invalid_request"
	// AssetReasonPresignExpired means CompleteAssetUpload arrived after the
	// presign's expiry.
	AssetReasonPresignExpired AssetReason = "presign_expired"
	// AssetReasonAlreadyConsumed means this object key's presign was already
	// consumed by an earlier CompleteAssetUpload call — a replay.
	AssetReasonAlreadyConsumed AssetReason = "presign_already_consumed"
	// AssetReasonSizeMismatch means the uploaded object's size does not
	// match what was declared at RequestAssetUpload time.
	AssetReasonSizeMismatch AssetReason = "size_mismatch"
	// AssetReasonHashMismatch means the uploaded object's sha256 does not
	// match what was declared at RequestAssetUpload time.
	AssetReasonHashMismatch AssetReason = "hash_mismatch"
	// AssetReasonNotUploaded means CompleteAssetUpload was called before any
	// object exists at the presigned key.
	AssetReasonNotUploaded AssetReason = "not_uploaded"
)

// RequestAssetUploadResult is what RequestAssetUpload returns on success.
// RequiredHeaders must be echoed exactly by the client's PUT for the
// presign's SigV4 signature to validate — see storage.PresignPutChecksummed.
type RequestAssetUploadResult struct {
	UploadURL       string
	ObjectKey       string
	RequiredHeaders map[string]string
	ExpiresAt       time.Time
	Reason          AssetReason
}

// CompleteAssetUploadResult is what CompleteAssetUpload returns.
type CompleteAssetUploadResult struct {
	Verified         bool
	ManifestComplete bool
	Reason           AssetReason
}

// presignExpiryFor scales the presign's expiry to the declared upload size,
// per the brief's "short expiry, matched to asset size" — larger uploads
// need more wall-clock time to complete the PUT, but every window stays on
// the order of minutes, never hours.
func presignExpiryFor(sizeBytes int64) time.Duration {
	const (
		minExpiry = 5 * time.Minute
		maxExpiry = 30 * time.Minute
		// mibPerExtraMinute: one extra minute of expiry per 200MiB declared,
		// on top of the 5-minute floor.
		mibPerExtraMinute = int64(200 * 1024 * 1024)
	)
	if sizeBytes <= 0 {
		return minExpiry
	}
	extra := time.Duration(sizeBytes/mibPerExtraMinute) * time.Minute
	expiry := minExpiry + extra
	if expiry > maxExpiry {
		return maxExpiry
	}
	return expiry
}

// assetKindFor derives the closed asset-kind vocabulary from a declared MIME
// type: video/* is "video", everything else is "screenshot" — the only two
// asset kinds a capture produces (spec §Phase 0).
func assetKindFor(mimeType string) string {
	if strings.HasPrefix(mimeType, "video/") {
		return "video"
	}
	return "screenshot"
}

// randomKeySuffix returns 16 random bytes hex-encoded, used to make every
// issued object key unique — a re-request never reuses a key, per the
// brief.
func randomKeySuffix() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("gateway: generate key suffix: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// RequestAssetUpload issues a fresh, server-selected object key and a
// checksum-bound presigned PUT for it. It never reuses a prior key, even for
// a re-request against an already-verified asset — the old key (and its
// content, if any) is simply orphaned.
func (g *Gateway) RequestAssetUpload(
	ctx context.Context,
	workspaceID, captureID, assetID, mimeType string,
	sizeBytes int64,
	sha256Hex string,
) (RequestAssetUploadResult, error) {
	if captureID == "" || assetID == "" || sizeBytes <= 0 || !isValidSHA256Hex(sha256Hex) {
		return RequestAssetUploadResult{Reason: AssetReasonInvalidRequest}, nil
	}

	var result RequestAssetUploadResult
	err := g.WithinTx(ctx, func(ctx context.Context, s Store) error {
		_, found, err := s.LockCaptureForWorkspace(ctx, captureID, workspaceID)
		if err != nil {
			return fmt.Errorf("gateway: lock capture: %w", err)
		}
		if !found {
			result = RequestAssetUploadResult{Reason: AssetReasonNotFound}
			return nil
		}

		suffix, err := randomKeySuffix()
		if err != nil {
			return err
		}
		objectKey := fmt.Sprintf("captures/%s/assets/%s/%s", captureID, assetID, suffix)

		if _, err := s.UpsertAssetForUpload(ctx, Asset{
			ID:        assetID,
			CaptureID: captureID,
			Kind:      assetKindFor(mimeType),
			MimeType:  mimeType,
			SizeBytes: sizeBytes,
			ObjectKey: objectKey,
		}); err != nil {
			return fmt.Errorf("gateway: upsert asset: %w", err)
		}

		// Adding/repointing an asset can only ever invalidate completeness
		// (a fresh or re-requested asset is unverified), so recompute and
		// persist manifest_complete in the same transaction as the upsert —
		// never as a follow-up call that could race or be skipped.
		complete, err := s.ManifestComplete(ctx, captureID)
		if err != nil {
			return fmt.Errorf("gateway: check manifest complete: %w", err)
		}
		if err := s.SetCaptureManifestComplete(ctx, captureID, complete); err != nil {
			return fmt.Errorf("gateway: set manifest complete: %w", err)
		}

		expiry := presignExpiryFor(sizeBytes)
		expiresAt := g.now().Add(expiry)

		if err := s.CreateAssetUploadPresign(ctx, AssetUploadPresign{
			ID:             objectKey,
			AssetID:        assetID,
			ObjectKey:      objectKey,
			ChecksumSHA256: sha256Hex,
			SizeBytes:      sizeBytes,
			ExpiresAt:      expiresAt,
		}); err != nil {
			return fmt.Errorf("gateway: create presign record: %w", err)
		}

		u, headers, err := g.Storage.PresignPutChecksummed(ctx, objectKey, expiry, sha256Hex)
		if err != nil {
			return fmt.Errorf("gateway: presign put: %w", err)
		}

		result = RequestAssetUploadResult{
			UploadURL:       u.String(),
			ObjectKey:       objectKey,
			RequiredHeaders: headers,
			ExpiresAt:       expiresAt,
		}
		return nil
	})
	if err != nil {
		return RequestAssetUploadResult{}, err
	}
	return result, nil
}

// CompleteAssetUpload verifies the object at the asset's currently
// outstanding presigned key against the size and hash declared when the
// upload was requested, using a server-side read-and-hash rather than any
// value the client (or the store's echoed metadata) could have supplied. A
// mismatch marks the asset unverified and never flips manifest_complete; it
// never deletes the uploaded object.
func (g *Gateway) CompleteAssetUpload(ctx context.Context, workspaceID, captureID, assetID string) (CompleteAssetUploadResult, error) {
	if captureID == "" || assetID == "" {
		return CompleteAssetUploadResult{Reason: AssetReasonInvalidRequest}, nil
	}

	var (
		result    CompleteAssetUploadResult
		objectKey string
		presign   AssetUploadPresign
	)

	err := g.WithinTx(ctx, func(ctx context.Context, s Store) error {
		asset, found, err := s.GetAssetForWorkspace(ctx, assetID, captureID, workspaceID)
		if err != nil {
			return fmt.Errorf("gateway: get asset: %w", err)
		}
		if !found {
			result = CompleteAssetUploadResult{Reason: AssetReasonNotFound}
			return nil
		}
		objectKey = asset.ObjectKey

		var found2 bool
		presign, found2, err = s.GetAssetUploadPresignByKey(ctx, objectKey)
		if err != nil {
			return fmt.Errorf("gateway: get presign: %w", err)
		}
		if !found2 {
			result = CompleteAssetUploadResult{Reason: AssetReasonNotFound}
			return nil
		}
		if !g.now().Before(presign.ExpiresAt) {
			result = CompleteAssetUploadResult{Reason: AssetReasonPresignExpired}
			return nil
		}

		consumed, err := s.ConsumeAssetUploadPresign(ctx, objectKey)
		if err != nil {
			return fmt.Errorf("gateway: consume presign: %w", err)
		}
		if !consumed {
			// Already consumed by a prior CompleteAssetUpload call — a
			// replay. Report the outcome without touching verification
			// state or manifest_complete again.
			result = CompleteAssetUploadResult{Reason: AssetReasonAlreadyConsumed}
			return nil
		}
		return nil
	})
	if err != nil {
		return CompleteAssetUploadResult{}, err
	}
	if result.Reason != AssetReasonNone {
		return result, nil
	}

	// Verification itself happens outside the DB transaction (it makes a
	// network call to object storage), guarded by the presign consumption
	// above: only the call that won the consume race reaches here, so two
	// concurrent CompleteAssetUpload calls can never both verify.
	exists, sizeBytes, err := g.Storage.StatSize(ctx, objectKey)
	if err != nil {
		return CompleteAssetUploadResult{}, fmt.Errorf("gateway: stat uploaded object: %w", err)
	}
	if !exists {
		return CompleteAssetUploadResult{Reason: AssetReasonNotUploaded}, nil
	}
	if sizeBytes != presign.SizeBytes {
		return CompleteAssetUploadResult{Reason: AssetReasonSizeMismatch}, nil
	}

	observedHash, err := g.Storage.HashObject(ctx, objectKey)
	if err != nil {
		return CompleteAssetUploadResult{}, fmt.Errorf("gateway: hash uploaded object: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(observedHash), []byte(presign.ChecksumSHA256)) != 1 {
		return CompleteAssetUploadResult{Reason: AssetReasonHashMismatch}, nil
	}

	var manifestComplete bool
	err = g.WithinTx(ctx, func(ctx context.Context, s Store) error {
		if err := s.MarkAssetVerified(ctx, assetID, observedHash, sizeBytes); err != nil {
			return fmt.Errorf("gateway: mark verified: %w", err)
		}
		complete, err := s.ManifestComplete(ctx, captureID)
		if err != nil {
			return fmt.Errorf("gateway: check manifest complete: %w", err)
		}
		if err := s.SetCaptureManifestComplete(ctx, captureID, complete); err != nil {
			return fmt.Errorf("gateway: set manifest complete: %w", err)
		}
		manifestComplete = complete
		return nil
	})
	if err != nil {
		return CompleteAssetUploadResult{}, err
	}

	return CompleteAssetUploadResult{Verified: true, ManifestComplete: manifestComplete}, nil
}

func isValidSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
