package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/dhiazfathra/how-to-replicate/services/internal/db"
	"github.com/dhiazfathra/how-to-replicate/services/internal/db/sqlcgen"
)

// pgStore implements Store against sqlcgen.Queries bound to one
// transaction. It is deliberately thin: every method is a single query
// plus the pgtype/JSON conversions gateway.go's Store interface doesn't
// need to know about.
type pgStore struct {
	q *sqlcgen.Queries
}

// NewTxRunner returns the WithinTx function Gateway needs, running each
// call inside its own pool transaction via db.WithTx.
func NewTxRunner(pool db.Pool) func(context.Context, func(context.Context, Store) error) error {
	return func(ctx context.Context, fn func(context.Context, Store) error) error {
		return db.WithTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
			return fn(ctx, &pgStore{q: sqlcgen.New(tx)})
		})
	}
}

func (s *pgStore) LockCaptureForWorkspace(ctx context.Context, captureID, workspaceID string) (Capture, bool, error) {
	row, err := s.q.LockCaptureForWorkspace(ctx, sqlcgen.LockCaptureForWorkspaceParams{
		ID:          captureID,
		WorkspaceID: workspaceID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Capture{}, false, nil
		}
		return Capture{}, false, fmt.Errorf("pgstore: lock capture: %w", err)
	}
	return Capture{ID: row.ID, WorkspaceID: row.WorkspaceID, Revision: row.Revision, Doc: row.Doc}, true, nil
}

func (s *pgStore) InsertMutationIfNew(ctx context.Context, mutationID, captureID, op string, payload []byte, clientT int64) (bool, error) {
	_, err := s.q.InsertMutationIfNew(ctx, sqlcgen.InsertMutationIfNewParams{
		ID:        mutationID,
		CaptureID: captureID,
		Op:        op,
		Payload:   payload,
		ClientT:   clientT,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("pgstore: insert mutation: %w", err)
	}
	return true, nil
}

func (s *pgStore) FieldVersion(ctx context.Context, captureID, field string) (FieldVersion, bool, error) {
	row, err := s.q.GetCaptureFieldVersion(ctx, sqlcgen.GetCaptureFieldVersionParams{
		CaptureID: captureID,
		Field:     field,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return FieldVersion{}, false, nil
		}
		return FieldVersion{}, false, fmt.Errorf("pgstore: get field version: %w", err)
	}
	return FieldVersion{ServerT: row.ServerT.Time, Revision: row.Revision, MutationID: row.MutationID}, true, nil
}

func (s *pgStore) SetFieldVersion(ctx context.Context, captureID, field string, v FieldVersion) error {
	_, err := s.q.UpsertCaptureFieldVersion(ctx, sqlcgen.UpsertCaptureFieldVersionParams{
		CaptureID:  captureID,
		Field:      field,
		ServerT:    pgtype.Timestamptz{Time: v.ServerT, Valid: true},
		Revision:   v.Revision,
		MutationID: v.MutationID,
	})
	if err != nil {
		return fmt.Errorf("pgstore: upsert field version: %w", err)
	}
	return nil
}

func (s *pgStore) UpdateCaptureRevisionAndDoc(ctx context.Context, captureID string, revision int64, doc []byte) error {
	_, err := s.q.UpdateCaptureRevisionAndDoc(ctx, sqlcgen.UpdateCaptureRevisionAndDocParams{
		ID:       captureID,
		Revision: revision,
		Doc:      doc,
	})
	if err != nil {
		return fmt.Errorf("pgstore: update capture: %w", err)
	}
	return nil
}

func (s *pgStore) InsertComment(ctx context.Context, commentID, captureID, authorID, body string) error {
	_, err := s.q.CreateComment(ctx, sqlcgen.CreateCommentParams{
		ID:        commentID,
		CaptureID: captureID,
		AuthorID:  authorID,
		Body:      body,
	})
	if err != nil {
		return fmt.Errorf("pgstore: insert comment: %w", err)
	}
	return nil
}

func (s *pgStore) RecordSupersededMutation(ctx context.Context, workspaceID, captureID, mutationID, field string) error {
	// map[string]string of two plain strings cannot fail to marshal.
	details, _ := json.Marshal(map[string]string{"mutation_id": mutationID, "field": field})
	_, err := s.q.CreateAuditLog(ctx, sqlcgen.CreateAuditLogParams{
		ID:          "audit_" + mutationID,
		WorkspaceID: workspaceID,
		ActorID:     pgtype.Text{Valid: false},
		Action:      "mutation_superseded",
		Subject:     captureID,
		Details:     details,
	})
	if err != nil {
		return fmt.Errorf("pgstore: record superseded mutation: %w", err)
	}
	return nil
}
