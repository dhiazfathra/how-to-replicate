// handlers_integration_test.go exercises createWorkspace's success path
// against a real, migrated Postgres testcontainer — the fakeQuerier-based
// tests in handlers_more_test.go can't reach it because createWorkspace
// runs its own transaction via db.Pool/store.CreateWorkspaceWithOwner,
// which needs a real pgx.Tx, not a fake Querier. Skips cleanly if no
// Docker daemon is reachable (see testsupport).
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/dhiazfathra/how-to-replicate/services/capture-api/internal/store"
	"github.com/dhiazfathra/how-to-replicate/services/internal/auth"
	"github.com/dhiazfathra/how-to-replicate/services/internal/db"
	"github.com/dhiazfathra/how-to-replicate/services/internal/db/sqlcgen"
	"github.com/dhiazfathra/how-to-replicate/services/internal/migrate"
	"github.com/dhiazfathra/how-to-replicate/services/internal/testsupport"
)

func TestCreateWorkspace_Success_Integration(t *testing.T) {
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

	st := store.New(sqlcgen.New(pool))
	if _, err := st.UpsertUser(ctx, "user-int", "int@example.com", "Integration User"); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	h := &handlers{store: st, pool: pool}
	r := httptest.NewRequest(http.MethodPost, "/v1/workspaces", strings.NewReader(`{"name":"Acme"}`))
	r = r.WithContext(auth.WithSubject(r.Context(), "user-int"))
	rec := httptest.NewRecorder()

	h.createWorkspace(rec, r)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Name != "Acme" || got.ID == "" {
		t.Fatalf("got %+v, want a minted id and name Acme", got)
	}

	role, ok, err := st.RoleInWorkspace(ctx, got.ID, "user-int")
	if err != nil || !ok || role != "owner" {
		t.Fatalf("RoleInWorkspace() = (%q, %v, %v), want (owner, true, nil)", role, ok, err)
	}
}
