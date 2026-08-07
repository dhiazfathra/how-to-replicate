// Package gateway implements sync-gateway's mutation intake: idempotent
// ULID-keyed insert, server-owned revision numbering, and per-field
// last-write-wins conflict resolution ordered by (server-received
// timestamp, revision, mutation ID). See spec §10 and ADR-012.
package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	syncv1 "github.com/dhiazfathra/how-to-replicate/proto/gen/go/sync/v1"
)

// Reason is a closed, machine-actionable rejection code. The wire type
// (PushMutationsResult.error) is a plain string because the proto shipped
// in Task 3 defines it that way; Reason values are the stable vocabulary
// clients match on rather than a free-form message, which is what the
// brief's "typed, not silent or generic" requirement actually needs.
type Reason string

const (
	// ReasonNone means the mutation was accepted.
	ReasonNone Reason = ""
	// ReasonNotFound covers both "no such capture" and "capture exists in a
	// different workspace" — deliberately the same reason for both, so a
	// rejection never discloses which one happened.
	ReasonNotFound Reason = "not_found"
	// ReasonInvalidMutation covers a structurally invalid mutation: missing
	// id, missing capture_id, or no op set.
	ReasonInvalidMutation Reason = "invalid_mutation"
)

// Result is the outcome of applying one mutation.
type Result struct {
	MutationID string
	Applied    bool
	Reason     Reason
}

// The four mutable capture fields set/assign mutations can target, plus
// the JSON keys they occupy in captures.doc. Named per ADR-012's field
// vocabulary (title, summary, tags, assignment).
const (
	fieldTitle      = "title"
	fieldSummary    = "summary"
	fieldTags       = "tags"
	fieldAssignment = "assignment"
)

// Gateway applies PushMutations batches against Store.
type Gateway struct {
	// WithinTx runs fn against a Store bound to one transaction, committing
	// on success. Production wiring supplies pgstore's transactional Store;
	// tests supply an in-process fake, no database required.
	WithinTx func(ctx context.Context, fn func(context.Context, Store) error) error

	// Now returns the server-received timestamp for a mutation. Defaults to
	// time.Now; tests override it to construct exact ties across the
	// (timestamp, revision, mutation ID) tuple, and to construct expired
	// presigns deterministically in asset-upload tests.
	Now func() time.Time

	// Storage issues and verifies asset-upload presigns (Task 5). Nil unless
	// the caller wires RequestAssetUpload/CompleteAssetUpload.
	Storage ObjectStore
}

func (g *Gateway) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now().UTC()
}

// PushMutations applies each mutation in order, one per transaction so a
// lock held for one capture never blocks unrelated captures in the same
// batch. It returns one Result per input mutation, in order. An error
// return means an infrastructure failure (the store itself failed) rather
// than a rejected mutation — rejections are reported as Results, never as
// errors, per the brief's "rejection is explicit and typed" requirement.
func (g *Gateway) PushMutations(ctx context.Context, workspaceID, actorID string, mutations []*syncv1.Mutation) ([]Result, error) {
	results := make([]Result, 0, len(mutations))
	for _, m := range mutations {
		res, err := g.applyOne(ctx, workspaceID, actorID, m)
		if err != nil {
			return nil, err
		}
		results = append(results, res)
	}
	return results, nil
}

func (g *Gateway) applyOne(ctx context.Context, workspaceID, actorID string, m *syncv1.Mutation) (Result, error) {
	field, value, isAppend, ok := decodeOp(m)
	if !ok || m.GetId() == "" || m.GetCaptureId() == "" {
		return Result{MutationID: m.GetId(), Applied: false, Reason: ReasonInvalidMutation}, nil
	}

	serverT := g.now()
	var result Result

	err := g.WithinTx(ctx, func(ctx context.Context, s Store) error {
		capture, found, err := s.LockCaptureForWorkspace(ctx, m.GetCaptureId(), workspaceID)
		if err != nil {
			return fmt.Errorf("gateway: lock capture: %w", err)
		}
		if !found {
			result = Result{MutationID: m.GetId(), Applied: false, Reason: ReasonNotFound}
			return nil
		}

		// value is always a string, []string, or commentValue (decodeOp's
		// closed output set) — none of which json.Marshal can fail on, so
		// the error is not worth a branch. payload is audit data only; it
		// is never read back and parsed by this service.
		payload, _ := json.Marshal(value)

		inserted, err := s.InsertMutationIfNew(ctx, m.GetId(), capture.ID, opName(m), payload, m.GetClientT())
		if err != nil {
			return fmt.Errorf("gateway: insert mutation: %w", err)
		}
		if !inserted {
			// Already applied by a prior delivery. ADR-012: a redelivered
			// mutation is resolved against the revision it was originally
			// assigned, not re-raced against current state — so a replay
			// changes nothing and reports the same outcome it always would.
			result = Result{MutationID: m.GetId(), Applied: true, Reason: ReasonNone}
			return nil
		}

		newRevision := capture.Revision + 1

		if isAppend {
			// Safe: decodeOp only sets isAppend=true alongside a commentValue.
			comment := value.(commentValue)
			if err := s.InsertComment(ctx, comment.id, capture.ID, actorID, comment.body); err != nil {
				return fmt.Errorf("gateway: insert comment: %w", err)
			}
			if err := s.UpdateCaptureRevisionAndDoc(ctx, capture.ID, newRevision, capture.Doc); err != nil {
				return fmt.Errorf("gateway: advance revision: %w", err)
			}
			result = Result{MutationID: m.GetId(), Applied: true, Reason: ReasonNone}
			return nil
		}

		candidate := FieldVersion{ServerT: serverT, Revision: newRevision, MutationID: m.GetId()}
		existingVersion, hasVersion, err := s.FieldVersion(ctx, capture.ID, field)
		if err != nil {
			return fmt.Errorf("gateway: get field version: %w", err)
		}

		doc := capture.Doc
		if !hasVersion || wins(candidate, existingVersion) {
			doc, err = setDocField(capture.Doc, field, value)
			if err != nil {
				return fmt.Errorf("gateway: apply field write: %w", err)
			}
			if err := s.SetFieldVersion(ctx, capture.ID, field, candidate); err != nil {
				return fmt.Errorf("gateway: set field version: %w", err)
			}
		} else {
			if err := s.RecordSupersededMutation(ctx, workspaceID, capture.ID, m.GetId(), field); err != nil {
				return fmt.Errorf("gateway: record superseded mutation: %w", err)
			}
		}

		if err := s.UpdateCaptureRevisionAndDoc(ctx, capture.ID, newRevision, doc); err != nil {
			return fmt.Errorf("gateway: advance revision: %w", err)
		}
		result = Result{MutationID: m.GetId(), Applied: true, Reason: ReasonNone}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

// wins reports whether candidate beats existing under the tie-break order
// the brief specifies: server-received timestamp, then revision, then
// mutation ID.
func wins(candidate, existing FieldVersion) bool {
	if !candidate.ServerT.Equal(existing.ServerT) {
		return candidate.ServerT.After(existing.ServerT)
	}
	if candidate.Revision != existing.Revision {
		return candidate.Revision > existing.Revision
	}
	return candidate.MutationID > existing.MutationID
}

// decodeOp extracts the field name (for set/assign ops) or reports
// isAppend for AppendComment, plus the value to store. ok is false for a
// mutation with no oneof branch set.
func decodeOp(m *syncv1.Mutation) (field string, value any, isAppend, ok bool) {
	switch op := m.GetOp().(type) {
	case *syncv1.Mutation_SetTitle:
		return fieldTitle, op.SetTitle.GetTitle(), false, true
	case *syncv1.Mutation_SetSummary:
		return fieldSummary, op.SetSummary.GetSummary(), false, true
	case *syncv1.Mutation_SetTags:
		return fieldTags, op.SetTags.GetTags(), false, true
	case *syncv1.Mutation_Assign:
		return fieldAssignment, op.Assign.GetAssigneeUserId(), false, true
	case *syncv1.Mutation_AppendComment:
		return "", commentValue{
			id:   op.AppendComment.GetCommentId(),
			body: op.AppendComment.GetBody(),
		}, true, op.AppendComment.GetCommentId() != ""
	default:
		return "", nil, false, false
	}
}

func opName(m *syncv1.Mutation) string {
	switch m.GetOp().(type) {
	case *syncv1.Mutation_SetTitle:
		return "set_title"
	case *syncv1.Mutation_SetSummary:
		return "set_summary"
	case *syncv1.Mutation_SetTags:
		return "set_tags"
	case *syncv1.Mutation_Assign:
		return "assign"
	case *syncv1.Mutation_AppendComment:
		return "append_comment"
	default:
		return "unknown"
	}
}

// commentValue carries an AppendComment's fields through decodeOp's value
// slot. The author is not carried here — it's the RPC caller's identity
// (internal/authctx), threaded in separately as actorID.
type commentValue struct {
	id   string
	body string
}

// setDocField sets field to value inside a capture's JSON doc, returning
// the re-marshaled document. An empty/nil doc is treated as {}. The only
// failure mode is a corrupt existing doc (unmarshal); the re-marshal of
// m cannot fail since every value ever stored is JSON-safe by
// construction (decodeOp's closed output set).
func setDocField(doc []byte, field string, value any) ([]byte, error) {
	m := map[string]any{}
	if len(doc) > 0 {
		if err := json.Unmarshal(doc, &m); err != nil {
			return nil, fmt.Errorf("gateway: unmarshal doc: %w", err)
		}
	}
	m[field] = value
	out, _ := json.Marshal(m)
	return out, nil
}
