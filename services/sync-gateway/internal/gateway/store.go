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
	// reprocess it.
	InsertMutationIfNew(ctx context.Context, mutationID, captureID, op string, payload []byte, clientT int64) (inserted bool, err error)

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
}
