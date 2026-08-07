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

func (s *pgStore) GetAssetForWorkspace(ctx context.Context, assetID, captureID, workspaceID string) (Asset, bool, error) {
	row, err := s.q.GetAssetForWorkspace(ctx, sqlcgen.GetAssetForWorkspaceParams{
		ID:          assetID,
		CaptureID:   captureID,
		WorkspaceID: workspaceID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Asset{}, false, nil
		}
		return Asset{}, false, fmt.Errorf("pgstore: get asset: %w", err)
	}
	return assetFromRow(row), true, nil
}

func (s *pgStore) UpsertAssetForUpload(ctx context.Context, asset Asset) (Asset, error) {
	row, err := s.q.UpsertAssetForUpload(ctx, sqlcgen.UpsertAssetForUploadParams{
		ID:        asset.ID,
		CaptureID: asset.CaptureID,
		Kind:      asset.Kind,
		MimeType:  asset.MimeType,
		SizeBytes: asset.SizeBytes,
		ObjectKey: asset.ObjectKey,
	})
	if err != nil {
		return Asset{}, fmt.Errorf("pgstore: upsert asset: %w", err)
	}
	return assetFromRow(row), nil
}

func (s *pgStore) CreateAssetUploadPresign(ctx context.Context, p AssetUploadPresign) error {
	_, err := s.q.CreateAssetUploadPresign(ctx, sqlcgen.CreateAssetUploadPresignParams{
		ID:             p.ID,
		AssetID:        p.AssetID,
		ObjectKey:      p.ObjectKey,
		ChecksumSha256: p.ChecksumSHA256,
		SizeBytes:      p.SizeBytes,
		ExpiresAt:      pgtype.Timestamptz{Time: p.ExpiresAt, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("pgstore: create asset upload presign: %w", err)
	}
	return nil
}

func (s *pgStore) GetAssetUploadPresignByKey(ctx context.Context, objectKey string) (AssetUploadPresign, bool, error) {
	row, err := s.q.GetAssetUploadPresignByKey(ctx, objectKey)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AssetUploadPresign{}, false, nil
		}
		return AssetUploadPresign{}, false, fmt.Errorf("pgstore: get asset upload presign: %w", err)
	}
	return AssetUploadPresign{
		ID:             row.ID,
		AssetID:        row.AssetID,
		ObjectKey:      row.ObjectKey,
		ChecksumSHA256: row.ChecksumSha256,
		SizeBytes:      row.SizeBytes,
		ExpiresAt:      row.ExpiresAt.Time,
	}, true, nil
}

func (s *pgStore) ConsumeAssetUploadPresign(ctx context.Context, objectKey string) (bool, error) {
	_, err := s.q.ConsumeAssetUploadPresign(ctx, objectKey)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("pgstore: consume asset upload presign: %w", err)
	}
	return true, nil
}

func (s *pgStore) MarkAssetVerified(ctx context.Context, assetID, sha256Hex string, sizeBytes int64) error {
	_, err := s.q.MarkAssetVerified(ctx, sqlcgen.MarkAssetVerifiedParams{
		ID:        assetID,
		Sha256:    pgtype.Text{String: sha256Hex, Valid: true},
		SizeBytes: sizeBytes,
	})
	if err != nil {
		return fmt.Errorf("pgstore: mark asset verified: %w", err)
	}
	return nil
}

func (s *pgStore) ManifestComplete(ctx context.Context, captureID string) (bool, error) {
	total, err := s.q.CountAssetsForCapture(ctx, captureID)
	if err != nil {
		return false, fmt.Errorf("pgstore: count assets: %w", err)
	}
	if total == 0 {
		return false, nil
	}
	unverified, err := s.q.CountUnverifiedAssetsForCapture(ctx, captureID)
	if err != nil {
		return false, fmt.Errorf("pgstore: count unverified assets: %w", err)
	}
	return unverified == 0, nil
}

func (s *pgStore) SetCaptureManifestComplete(ctx context.Context, captureID string, complete bool) error {
	_, err := s.q.SetCaptureManifestComplete(ctx, sqlcgen.SetCaptureManifestCompleteParams{
		ID:               captureID,
		ManifestComplete: complete,
	})
	if err != nil {
		return fmt.Errorf("pgstore: set capture manifest complete: %w", err)
	}
	return nil
}

func assetFromRow(row sqlcgen.Asset) Asset {
	return Asset{
		ID:         row.ID,
		CaptureID:  row.CaptureID,
		Kind:       row.Kind,
		MimeType:   row.MimeType,
		SizeBytes:  row.SizeBytes,
		ObjectKey:  row.ObjectKey,
		Sha256:     row.Sha256.String,
		VerifiedAt: row.VerifiedAt.Time,
		Verified:   row.VerifiedAt.Valid,
	}
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
