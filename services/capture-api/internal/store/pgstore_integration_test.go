// pgstore_integration_test.go proves Store's queries behave against a
// real, migrated Postgres testcontainer — same harness pattern as
// sync-gateway's pgstore_integration_test.go. Skips cleanly if no Docker
// daemon is reachable (see testsupport).
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/dhiazfathra/how-to-replicate/services/internal/db"
	"github.com/dhiazfathra/how-to-replicate/services/internal/db/sqlcgen"
	"github.com/dhiazfathra/how-to-replicate/services/internal/migrate"
	"github.com/dhiazfathra/how-to-replicate/services/internal/testsupport"
)

func TestStore_Integration(t *testing.T) {
	ctx := context.Background()
	migrationDSN := testsupport.Postgres(t, ctx)

	sqlDB, err := sql.Open("pgx", migrationDSN)
	if err != nil {
		t.Fatalf("open migration db: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	if err := migrate.Up(sqlDB); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	runtimeDSN, err := db.WithRuntimeRole(migrationDSN, testsupport.RuntimeRolePassword)
	if err != nil {
		t.Fatalf("build runtime dsn: %v", err)
	}
	pool, err := pgxpool.New(ctx, runtimeDSN)
	if err != nil {
		t.Fatalf("connect runtime pool: %v", err)
	}
	defer pool.Close()

	s := New(sqlcgen.New(pool))

	if _, err := s.UpsertUser(ctx, "user_int", "int@example.com", "Integration User"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := s.UpsertUser(ctx, "user_stranger", "stranger@example.com", "Stranger"); err != nil {
		t.Fatalf("seed stranger user: %v", err)
	}

	// CreateWorkspaceWithOwner: workspace + owner membership in one tx.
	ws, err := CreateWorkspaceWithOwner(ctx, pool, "ws_int", "Acme", "mem_int", "user_int")
	if err != nil {
		t.Fatalf("CreateWorkspaceWithOwner: %v", err)
	}
	role, ok, err := s.RoleInWorkspace(ctx, ws.ID, "user_int")
	if err != nil || !ok || role != "owner" {
		t.Fatalf("RoleInWorkspace() = (%q, %v, %v), want (owner, true, nil)", role, ok, err)
	}

	// A duplicate workspace ID fails CreateWorkspace itself (primary key
	// violation), before CreateMembership ever runs — proves the tx is
	// rolled back and the error surfaces rather than a partial write.
	if _, err := CreateWorkspaceWithOwner(ctx, pool, "ws_int", "Acme Duplicate", "mem_int_dup", "user_int"); err == nil {
		t.Fatal("CreateWorkspaceWithOwner() with duplicate id: err = nil, want error")
	}
	var membershipCount int
	if err := sqlDB.QueryRowContext(ctx, "SELECT count(*) FROM memberships WHERE id = $1", "mem_int_dup").Scan(&membershipCount); err != nil {
		t.Fatalf("count memberships: %v", err)
	}
	if membershipCount != 0 {
		t.Fatalf("membership mem_int_dup exists after failed CreateWorkspaceWithOwner, want tx rolled back")
	}

	// A different user has no membership row — same not-found shape as a
	// workspace that doesn't exist at all.
	if _, ok, err := s.RoleInWorkspace(ctx, ws.ID, "user_stranger"); err != nil || ok {
		t.Fatalf("RoleInWorkspace() for non-member = (ok=%v, err=%v), want (false, nil)", ok, err)
	}
	if _, err := s.GetWorkspaceForMember(ctx, ws.ID, "user_stranger"); err != ErrNotFound {
		t.Fatalf("GetWorkspaceForMember() for non-member = %v, want ErrNotFound", err)
	}
	if _, err := s.GetWorkspaceForMember(ctx, "ws_does_not_exist", "user_int"); err != ErrNotFound {
		t.Fatalf("GetWorkspaceForMember() for nonexistent workspace = %v, want ErrNotFound", err)
	}

	// Project CRUD, scoped by workspace.
	proj, err := s.CreateProject(ctx, "proj_int", ws.ID, "Web")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := s.GetProject(ctx, proj.ID, "ws_other_workspace"); err != ErrNotFound {
		t.Fatalf("GetProject() cross-workspace = %v, want ErrNotFound", err)
	}
	if got, err := s.GetProject(ctx, proj.ID, ws.ID); err != nil || got.ID != proj.ID {
		t.Fatalf("GetProject() = (%v, %v)", got, err)
	}

	// Policy overrides round-trip.
	if _, err := s.SetPolicyOverrides(ctx, ws.ID, []byte(`{"retentionDays":30}`)); err != nil {
		t.Fatalf("SetPolicyOverrides: %v", err)
	}
	got, err := s.GetWorkspace(ctx, ws.ID)
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	var overrides struct {
		RetentionDays int `json:"retentionDays"`
	}
	if err := json.Unmarshal(got.PolicyOverrides, &overrides); err != nil || overrides.RetentionDays != 30 {
		t.Fatalf("PolicyOverrides = %s, want retentionDays:30 (err=%v)", got.PolicyOverrides, err)
	}
}
