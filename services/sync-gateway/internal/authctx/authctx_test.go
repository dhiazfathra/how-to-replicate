package authctx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWorkspaceID(t *testing.T) {
	tests := []struct {
		name   string
		value  string
		set    bool
		want   string
		wantOK bool
	}{
		{name: "present", value: "ws_1", set: true, want: "ws_1", wantOK: true},
		{name: "absent", set: false, want: "", wantOK: false},
		{name: "empty string treated as absent", value: "", set: true, want: "", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			if tt.set {
				ctx = WithWorkspaceID(ctx, tt.value)
			}
			got, ok := WorkspaceID(ctx)
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("WorkspaceID() = (%q, %v), want (%q, %v)", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestUserID(t *testing.T) {
	tests := []struct {
		name   string
		value  string
		set    bool
		want   string
		wantOK bool
	}{
		{name: "present", value: "user_1", set: true, want: "user_1", wantOK: true},
		{name: "absent", set: false, want: "", wantOK: false},
		{name: "empty string treated as absent", value: "", set: true, want: "", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			if tt.set {
				ctx = WithUserID(ctx, tt.value)
			}
			got, ok := UserID(ctx)
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("UserID() = (%q, %v), want (%q, %v)", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestRole(t *testing.T) {
	tests := []struct {
		name   string
		value  string
		set    bool
		want   string
		wantOK bool
	}{
		{name: "present", value: "member", set: true, want: "member", wantOK: true},
		{name: "absent", set: false, want: "", wantOK: false},
		{name: "empty string treated as absent", value: "", set: true, want: "", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			if tt.set {
				ctx = WithRole(ctx, tt.value)
			}
			got, ok := Role(ctx)
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("Role() = (%q, %v), want (%q, %v)", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestHeaderMiddleware(t *testing.T) {
	var gotWorkspace, gotUser, gotRole string
	var gotWorkspaceOK, gotUserOK, gotRoleOK bool

	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotWorkspace, gotWorkspaceOK = WorkspaceID(r.Context())
		gotUser, gotUserOK = UserID(r.Context())
		gotRole, gotRoleOK = Role(r.Context())
	})

	req := httptest.NewRequest(http.MethodPost, "/sync.v1.SyncService/PushMutations", nil)
	req.Header.Set(WorkspaceHeader, "ws_42")
	req.Header.Set(UserHeader, "user_42")
	req.Header.Set(RoleHeader, "member")
	rec := httptest.NewRecorder()

	HeaderMiddleware(next).ServeHTTP(rec, req)

	if !gotWorkspaceOK || gotWorkspace != "ws_42" {
		t.Fatalf("workspace id not propagated: %q, %v", gotWorkspace, gotWorkspaceOK)
	}
	if !gotUserOK || gotUser != "user_42" {
		t.Fatalf("user id not propagated: %q, %v", gotUser, gotUserOK)
	}
	if !gotRoleOK || gotRole != "member" {
		t.Fatalf("role not propagated: %q, %v", gotRole, gotRoleOK)
	}
}

func TestHeaderMiddleware_MissingHeaders(t *testing.T) {
	var gotOK bool
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, gotOK = WorkspaceID(r.Context())
	})

	req := httptest.NewRequest(http.MethodPost, "/sync.v1.SyncService/PushMutations", nil)
	rec := httptest.NewRecorder()

	HeaderMiddleware(next).ServeHTTP(rec, req)

	if gotOK {
		t.Fatalf("want no workspace id when header absent")
	}
}
