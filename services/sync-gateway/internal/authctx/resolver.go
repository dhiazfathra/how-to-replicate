package authctx

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/dhiazfathra/how-to-replicate/services/internal/db/sqlcgen"
)

// DBMembershipResolver resolves workspace membership directly against
// sqlcgen — sync-gateway needs only this one lookup from capture-api's
// identity domain, so it queries the shared schema itself rather than
// depending on the capture-api service module for one query.
type DBMembershipResolver struct {
	Queries sqlcgen.Querier
}

// Resolve implements MembershipResolver.
func (r DBMembershipResolver) Resolve(ctx context.Context, subject, workspaceID string) (string, bool, error) {
	if workspaceID == "" {
		return "", false, nil
	}
	m, err := r.Queries.GetMembershipByWorkspaceAndUser(ctx, sqlcgen.GetMembershipByWorkspaceAndUserParams{
		WorkspaceID: workspaceID,
		UserID:      subject,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("authctx: resolve membership: %w", err)
	}
	return m.Role, true, nil
}
