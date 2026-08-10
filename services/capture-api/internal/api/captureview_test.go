package api

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/dhiazfathra/how-to-replicate/services/internal/db/sqlcgen"
)

func TestGateCapture_ReadyWithEmptyContent(t *testing.T) {
	c := sqlcgen.Capture{
		ID:       "cap_1",
		State:    captureReadyState,
		Fidelity: "full",
	}
	v := gateCapture(c)
	if string(v.Doc) != "null" || string(v.Metadata) != "null" || string(v.Env) != "null" {
		t.Fatalf("expected null placeholders for empty JSON columns, got %+v", v)
	}
	if v.CreatedAt != "" {
		t.Fatalf("expected empty createdAt for invalid timestamp, got %q", v.CreatedAt)
	}
}

func TestGateCapture_ValidCreatedAt(t *testing.T) {
	c := sqlcgen.Capture{
		ID:        "cap_1",
		State:     "recording",
		CreatedAt: pgtype.Timestamptz{Valid: false},
	}
	v := gateCapture(c)
	if v.CreatedAt != "" {
		t.Fatalf("createdAt = %q, want empty for invalid timestamp", v.CreatedAt)
	}
}
