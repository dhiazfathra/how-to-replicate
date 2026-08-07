package authctx

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/jackc/pgx/v5"

	"github.com/dhiazfathra/how-to-replicate/services/internal/auth"
	"github.com/dhiazfathra/how-to-replicate/services/internal/db/sqlcgen"
)

// minimal fake OIDC provider: discovery + jwks + token endpoint that returns
// whatever raw ID token was registered for the code.
type fakeIdP struct {
	server *httptest.Server
	key    *rsa.PrivateKey
	kid    string
	code   string
	token  string
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	idp := &fakeIdP{key: key, kid: "kid-1", code: "test-code"}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"issuer":                                idp.server.URL,
			"authorization_endpoint":                idp.server.URL + "/authorize",
			"token_endpoint":                        idp.server.URL + "/token",
			"jwks_uri":                               idp.server.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
			{Key: &idp.key.PublicKey, KeyID: idp.kid, Algorithm: "RS256", Use: "sig"},
		}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.FormValue("code") != idp.code {
			http.Error(w, "unknown code", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "test-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
			"id_token":     idp.token,
		})
	})

	idp.server = httptest.NewServer(mux)
	t.Cleanup(idp.server.Close)
	return idp
}

func (idp *fakeIdP) mintToken(t *testing.T, subject string) string {
	t.Helper()
	now := time.Now()
	claims := map[string]interface{}{
		"iss": idp.server.URL,
		"aud": "test-client",
		"sub": subject,
		"iat": now.Unix(),
		"exp": now.Add(time.Hour).Unix(),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: idp.key},
		(&jose.SignerOptions{}).WithHeader("kid", idp.kid))
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	jws, err := signer.Sign(payload)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	compact, err := jws.CompactSerialize()
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	idp.token = compact
	return compact
}

// fakeResolver is a MembershipResolver test double.
type fakeResolver struct {
	role string
	ok   bool
	err  error
}

func (f fakeResolver) Resolve(_ context.Context, _, _ string) (string, bool, error) {
	return f.role, f.ok, f.err
}

func TestOIDCMiddleware(t *testing.T) {
	idp := newFakeIdP(t)
	_, verifier, err := auth.NewOAuth2Config(t.Context(), idp.server.URL, "test-client", "", nil)
	if err != nil {
		t.Fatalf("NewOAuth2Config: %v", err)
	}
	token := idp.mintToken(t, "user-42")

	tests := []struct {
		name       string
		authHeader string
		resolver   MembershipResolver
		wantStatus int
		wantOK     bool
	}{
		{
			name:       "missing token",
			resolver:   fakeResolver{ok: true, role: "member"},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "member found",
			authHeader: "Bearer " + token,
			resolver:   fakeResolver{ok: true, role: "member"},
			wantStatus: http.StatusOK,
			wantOK:     true,
		},
		{
			name:       "not a member",
			authHeader: "Bearer " + token,
			resolver:   fakeResolver{ok: false},
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "resolver error",
			authHeader: "Bearer " + token,
			resolver:   fakeResolver{err: errors.New("db down")},
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotWorkspace, gotUser, gotRole string
			var called bool
			next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				called = true
				gotWorkspace, _ = WorkspaceID(r.Context())
				gotUser, _ = UserID(r.Context())
				gotRole, _ = Role(r.Context())
			})

			req := httptest.NewRequest(http.MethodPost, "/sync.v1.SyncService/PushMutations", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			req.Header.Set(WorkspaceHeader, "ws_1")
			rec := httptest.NewRecorder()

			OIDCMiddleware(verifier, tt.resolver)(next).ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if called != tt.wantOK {
				t.Fatalf("next called = %v, want %v", called, tt.wantOK)
			}
			if tt.wantOK {
				if gotUser != "user-42" || gotWorkspace != "ws_1" || gotRole != "member" {
					t.Fatalf("got (user=%q ws=%q role=%q)", gotUser, gotWorkspace, gotRole)
				}
			}
		})
	}
}

// fakeQuerier implements only GetMembershipByWorkspaceAndUser; every other
// method panics if called, since DBMembershipResolver only uses that one.
type fakeQuerier struct {
	sqlcgen.Querier
	membership sqlcgen.Membership
	err        error
}

func (f fakeQuerier) GetMembershipByWorkspaceAndUser(_ context.Context, _ sqlcgen.GetMembershipByWorkspaceAndUserParams) (sqlcgen.Membership, error) {
	return f.membership, f.err
}

func TestDBMembershipResolver(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		r := DBMembershipResolver{Queries: fakeQuerier{membership: sqlcgen.Membership{Role: "owner"}}}
		role, ok, err := r.Resolve(t.Context(), "user-1", "ws-1")
		if err != nil || !ok || role != "owner" {
			t.Fatalf("Resolve() = (%q, %v, %v)", role, ok, err)
		}
	})

	t.Run("empty workspace id short-circuits", func(t *testing.T) {
		r := DBMembershipResolver{Queries: fakeQuerier{err: errors.New("should not be called")}}
		_, ok, err := r.Resolve(t.Context(), "user-1", "")
		if err != nil || ok {
			t.Fatalf("Resolve() = (ok=%v, err=%v), want (false, nil)", ok, err)
		}
	})

	t.Run("no rows", func(t *testing.T) {
		r := DBMembershipResolver{Queries: fakeQuerier{err: pgx.ErrNoRows}}
		_, ok, err := r.Resolve(t.Context(), "user-1", "ws-1")
		if err != nil || ok {
			t.Fatalf("Resolve() = (ok=%v, err=%v), want (false, nil)", ok, err)
		}
	})

	t.Run("query error", func(t *testing.T) {
		r := DBMembershipResolver{Queries: fakeQuerier{err: errors.New("boom")}}
		_, ok, err := r.Resolve(t.Context(), "user-1", "ws-1")
		if err == nil || ok {
			t.Fatalf("Resolve() = (ok=%v, err=%v), want an error", ok, err)
		}
	})
}
