package gateway

import (
	"context"
	"time"
)

// Capture is the subset of a captures row the gateway needs to advance
// revision and merge field writes.
type Capture struct {
	ID          string
	WorkspaceID string
	Revision    int64
	Doc         []byte // JSON object of the four mutable fields
}

// FieldVersion is the last-write-wins tuple currently recorded for one
// mutable field on one capture.
type FieldVersion struct {
	ServerT    time.Time
	Revision   int64
	MutationID string
}

// Store is the narrow persistence seam gateway logic runs against. It is
// implemented in production by pgStore (pgstore.go), wrapping
// sqlcgen.Queries bound to a transaction; tests implement it directly with
// an in-memory fake, so the conflict-resolution and idempotency logic in
// gateway.go is unit-testable without a database.
type Store interface {
	// LockCaptureForWorkspace locks and returns the capture, or found=false
	// if no capture with that ID exists in that workspace — deliberately the
	// same outcome whether the capture doesn't exist at all or exists in a
	// different workspace, so callers cannot distinguish the two.
	LockCaptureForWorkspace(ctx context.Context, captureID, workspaceID string) (capture Capture, found bool, err error)

	// InsertMutationIfNew records mutationID durably and reports whether
	// this call is the one that inserted it. inserted=false means a prior
	// call already applied this exact mutation ID — the caller must not
	// reprocess it. workspaceID is stamped onto the row (Task 6) so
	// PullMutationsSince can filter the delta log without joining back to
	// captures.
	InsertMutationIfNew(ctx context.Context, mutationID, captureID, workspaceID, op string, payload []byte, clientT int64) (inserted bool, err error)

	// PullMutationsSince returns every mutation applied in workspaceID with
	// a sequence number greater than since, ordered by sequence ascending,
	// capped at limit rows. This is the delta log PullDeltas and the
	// WebSocket fan-out both read from (Task 6) — see DeltaMutation.
	PullMutationsSince(ctx context.Context, workspaceID string, since int64, limit int32) ([]DeltaMutation, error)

	// FieldVersion returns the current LWW-winning tuple for field on
	// captureID, or found=false if the field has never been written.
	FieldVersion(ctx context.Context, captureID, field string) (v FieldVersion, found bool, err error)

	// SetFieldVersion records the winning tuple for field on captureID.
	SetFieldVersion(ctx context.Context, captureID, field string, v FieldVersion) error

	// UpdateCaptureRevisionAndDoc persists the capture's new revision and
	// (possibly unchanged) doc.
	UpdateCaptureRevisionAndDoc(ctx context.Context, captureID string, revision int64, doc []byte) error

	// InsertComment appends a comment. Comments never conflict.
	InsertComment(ctx context.Context, commentID, captureID, authorID, body string) error

	// RecordSupersededMutation audits a field write that lost the LWW race
	// (ADR-012: "the loser's value is recorded in the audit log").
	RecordSupersededMutation(ctx context.Context, workspaceID, captureID, mutationID, field string) error

	// GetAssetForWorkspace returns the asset row, or found=false if no asset
	// with that ID exists on that capture in that workspace — same
	// no-existence-leak shape as LockCaptureForWorkspace.
	GetAssetForWorkspace(ctx context.Context, assetID, captureID, workspaceID string) (asset Asset, found bool, err error)

	// UpsertAssetForUpload creates the manifest entry on first request, or
	// repoints its object_key at a freshly minted key on re-request, without
	// touching sha256/size_bytes/verified_at.
	UpsertAssetForUpload(ctx context.Context, asset Asset) (Asset, error)

	// CreateAssetUploadPresign records one presign issuance.
	CreateAssetUploadPresign(ctx context.Context, p AssetUploadPresign) error

	// GetAssetUploadPresignByKey returns the presign issuance for objectKey,
	// or found=false if none exists.
	GetAssetUploadPresignByKey(ctx context.Context, objectKey string) (p AssetUploadPresign, found bool, err error)

	// ConsumeAssetUploadPresign marks objectKey's presign consumed and
	// reports ok=true only if this call is the one that consumed it — a
	// second call (replay, or a presign never issued) reports ok=false and
	// must not be treated as verification succeeding again.
	ConsumeAssetUploadPresign(ctx context.Context, objectKey string) (ok bool, err error)

	// MarkAssetVerified stamps an asset verified with the hash/size the
	// server itself observed.
	MarkAssetVerified(ctx context.Context, assetID, sha256Hex string, sizeBytes int64) error

	// ManifestComplete reports whether every asset on captureID is verified.
	// A capture with zero assets is not complete — there is nothing to be
	// complete about yet.
	ManifestComplete(ctx context.Context, captureID string) (bool, error)

	// SetCaptureManifestComplete persists the capture's manifest_complete
	// flag — the field client eviction reads (invariant 8), never
	// lastPushedAt.
	SetCaptureManifestComplete(ctx context.Context, captureID string, complete bool) error

	// GetCaptureManifestState returns captureID's current manifest_complete
	// flag and revision, or found=false if no capture with that ID exists.
	// PullDeltas uses this to surface the capture-level sync state
	// SetCaptureManifestComplete maintains — the only channel by which
	// manifest_complete reaches the client.
	GetCaptureManifestState(ctx context.Context, captureID string) (manifestComplete bool, revision int64, found bool, err error)
}

// DeltaMutation is one row of the delta log: a mutation as recorded in
// Postgres, with the sequence number PullDeltas pages on. Op/Payload are
// the same closed vocabulary applyOne wrote (see decodePayload for the
// reverse of decodeOp/opName).
type DeltaMutation struct {
	Seq       int64
	ID        string
	CaptureID string
	Op        string
	Payload   []byte
	ClientT   int64
}

// Asset is the subset of an assets row the gateway needs to issue and
// verify uploads.
type Asset struct {
	ID         string
	CaptureID  string
	Kind       string
	MimeType   string
	SizeBytes  int64
	ObjectKey  string
	Sha256     string
	VerifiedAt time.Time
	Verified   bool
}

// AssetUploadPresign is one presign issuance: the declared size/checksum
// bound at RequestAssetUpload time, and its expiry and consumption state.
type AssetUploadPresign struct {
	ID             string
	AssetID        string
	ObjectKey      string
	ChecksumSHA256 string
	SizeBytes      int64
	ExpiresAt      time.Time
}
