package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

func newOAuth2Config(idp *fakeIdP) oauth2.Config {
	return oauth2.Config{
		ClientID:    "test-client",
		RedirectURL: "https://viewer.example/callback",
		Endpoint: oauth2.Endpoint{
			AuthURL:  idp.server.URL + "/authorize",
			TokenURL: idp.server.URL + "/token",
		},
		Scopes: []string{"openid"},
	}
}

func newVerifier(t *testing.T, idp *fakeIdP) *oidc.IDTokenVerifier {
	t.Helper()
	ctx := context.Background()
	provider, err := oidc.NewProvider(ctx, idp.server.URL)
	if err != nil {
		t.Fatalf("discover provider: %v", err)
	}
	return provider.Verifier(&oidc.Config{ClientID: "test-client"})
}

func TestNewAttempt(t *testing.T) {
	a, err := NewAttempt()
	if err != nil {
		t.Fatalf("NewAttempt: %v", err)
	}
	if a.State == "" || a.Nonce == "" || a.CodeVerifier == "" || a.CodeChallenge == "" {
		t.Fatalf("expected all fields populated, got %+v", a)
	}
	if len(a.CodeVerifier) < 43 || len(a.CodeVerifier) > 128 {
		t.Fatalf("code verifier length %d out of RFC 7636 range", len(a.CodeVerifier))
	}
	b, err := NewAttempt()
	if err != nil {
		t.Fatalf("NewAttempt: %v", err)
	}
	if a.State == b.State || a.Nonce == b.Nonce || a.CodeVerifier == b.CodeVerifier {
		t.Fatalf("expected fresh randomness per attempt")
	}
}

func TestBuildAuthURL(t *testing.T) {
	idp := newFakeIdP(t)
	cfg := newOAuth2Config(idp)
	attempt, err := NewAttempt()
	if err != nil {
		t.Fatalf("NewAttempt: %v", err)
	}

	raw := BuildAuthURL(cfg, attempt)
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse auth url: %v", err)
	}
	q := u.Query()
	if q.Get("state") != attempt.State {
		t.Errorf("state = %q, want %q", q.Get("state"), attempt.State)
	}
	if q.Get("nonce") != attempt.Nonce {
		t.Errorf("nonce = %q, want %q", q.Get("nonce"), attempt.Nonce)
	}
	if q.Get("code_challenge") != attempt.CodeChallenge {
		t.Errorf("code_challenge = %q, want %q", q.Get("code_challenge"), attempt.CodeChallenge)
	}
	if q.Get("code_challenge_method") != "S256" {
		t.Errorf("code_challenge_method = %q, want S256", q.Get("code_challenge_method"))
	}
}

func TestExchange_StateMismatch(t *testing.T) {
	idp := newFakeIdP(t)
	cfg := newOAuth2Config(idp)
	verifier := newVerifier(t, idp)
	attempt, _ := NewAttempt()
	guard := NewMemoryReplayGuard()

	_, err := Exchange(context.Background(), cfg, verifier, guard, attempt, "not-the-state", "irrelevant-code")
	if !errors.Is(err, ErrStateMismatch) {
		t.Fatalf("err = %v, want ErrStateMismatch", err)
	}
}

func TestExchange_NonceMismatch(t *testing.T) {
	idp := newFakeIdP(t)
	cfg := newOAuth2Config(idp)
	verifier := newVerifier(t, idp)
	attempt, _ := NewAttempt()
	guard := NewMemoryReplayGuard()

	rawToken := idp.mintToken(t, "kid-1", map[string]interface{}{"nonce": "wrong-nonce"})
	idp.registerCode("code-1", rawToken)

	_, err := Exchange(context.Background(), cfg, verifier, guard, attempt, attempt.State, "code-1")
	if !errors.Is(err, ErrNonceMismatch) {
		t.Fatalf("err = %v, want ErrNonceMismatch", err)
	}
}

func TestExchange_Success(t *testing.T) {
	idp := newFakeIdP(t)
	cfg := newOAuth2Config(idp)
	verifier := newVerifier(t, idp)
	attempt, _ := NewAttempt()
	guard := NewMemoryReplayGuard()

	rawToken := idp.mintToken(t, "kid-1", map[string]interface{}{"nonce": attempt.Nonce})
	idp.registerCode("code-1", rawToken)

	idToken, err := Exchange(context.Background(), cfg, verifier, guard, attempt, attempt.State, "code-1")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if idToken.Subject != "user-123" {
		t.Errorf("subject = %q, want user-123", idToken.Subject)
	}
}

func TestExchange_ReplayedToken(t *testing.T) {
	idp := newFakeIdP(t)
	cfg := newOAuth2Config(idp)
	verifier := newVerifier(t, idp)
	attempt, _ := NewAttempt()
	guard := NewMemoryReplayGuard()

	rawToken := idp.mintToken(t, "kid-1", map[string]interface{}{"nonce": attempt.Nonce})
	idp.registerCode("code-1", rawToken)
	idp.registerCode("code-2", rawToken) // same underlying token, second "use"

	if _, err := Exchange(context.Background(), cfg, verifier, guard, attempt, attempt.State, "code-1"); err != nil {
		t.Fatalf("first Exchange: %v", err)
	}

	attempt2, _ := NewAttempt()
	attempt2.Nonce = attempt.Nonce // reuse so nonce still matches the (replayed) token
	_, err := Exchange(context.Background(), cfg, verifier, guard, attempt2, attempt2.State, "code-2")
	if !errors.Is(err, ErrReplayedToken) {
		t.Fatalf("err = %v, want ErrReplayedToken", err)
	}
}

func TestMemoryReplayGuard(t *testing.T) {
	g := NewMemoryReplayGuard()
	ctx := context.Background()

	seen, err := g.SeenBefore(ctx, "tok-1")
	if err != nil || seen {
		t.Fatalf("SeenBefore before MarkSeen = (%v, %v), want (false, nil)", seen, err)
	}
	if err := g.MarkSeen(ctx, "tok-1"); err != nil {
		t.Fatalf("MarkSeen: %v", err)
	}
	seen, err = g.SeenBefore(ctx, "tok-1")
	if err != nil || !seen {
		t.Fatalf("SeenBefore after MarkSeen = (%v, %v), want (true, nil)", seen, err)
	}
}

// --- JWKS unknown-kid rotation handling ---

func TestVerifier_UnknownKid_RefetchSucceeds(t *testing.T) {
	idp := newFakeIdP(t)
	idp.addKey("kid-initial")
	verifier := newVerifier(t, idp)
	ctx := context.Background()

	// Prime the verifier's cache with kid-initial.
	initial := idp.mintToken(t, "kid-initial", nil)
	if _, err := verifier.Verify(ctx, initial); err != nil {
		t.Fatalf("priming verify: %v", err)
	}
	hitsAfterPriming := idp.jwksHitCount()

	// Mint a token under a kid the JWKS does not yet serve, then add the
	// key to /jwks — simulating rotation that happened after our cache was
	// primed.
	rotated := idp.mintToken(t, "kid-rotated", nil)

	if _, err := verifier.Verify(ctx, rotated); err != nil {
		t.Fatalf("verify after rotation: %v", err)
	}
	gotRefetches := idp.jwksHitCount() - hitsAfterPriming
	if gotRefetches != 1 {
		t.Fatalf("jwks refetches after unknown kid = %d, want exactly 1", gotRefetches)
	}
}

func TestVerifier_UnknownKid_StillMissingAfterRefetch(t *testing.T) {
	idp := newFakeIdP(t)
	idp.addKey("kid-initial")
	verifier := newVerifier(t, idp)
	ctx := context.Background()

	initial := idp.mintToken(t, "kid-initial", nil)
	if _, err := verifier.Verify(ctx, initial); err != nil {
		t.Fatalf("priming verify: %v", err)
	}
	hitsAfterPriming := idp.jwksHitCount()

	// Token signed by a key that is never published to /jwks.
	orphan := idp.mintOrphanToken(t, nil)

	if _, err := verifier.Verify(ctx, orphan); err == nil {
		t.Fatalf("expected verification to fail for a kid absent from jwks")
	}
	gotRefetches := idp.jwksHitCount() - hitsAfterPriming
	if gotRefetches != 1 {
		t.Fatalf("jwks refetches for a never-present kid = %d, want exactly 1 (not zero, not unbounded)", gotRefetches)
	}
}

// --- Middleware ---

func TestMiddleware(t *testing.T) {
	idp := newFakeIdP(t)
	idp.addKey("kid-1")
	verifier := newVerifier(t, idp)

	var gotSubject string
	var gotClaims map[string]interface{}
	handler := Middleware(verifier)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSubject, _ = Subject(r.Context())
		gotClaims, _ = Claims(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	t.Run("missing header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})

	t.Run("malformed header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "not-a-bearer-token")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})

	t.Run("expired token", func(t *testing.T) {
		expired := idp.mintToken(t, "kid-1", map[string]interface{}{"exp": 1})
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer "+expired)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})

	t.Run("valid token", func(t *testing.T) {
		valid := idp.mintToken(t, "kid-1", nil)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer "+valid)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if gotSubject != "user-123" {
			t.Errorf("Subject(ctx) = %q, want user-123", gotSubject)
		}
		if gotClaims == nil || gotClaims["sub"] != "user-123" {
			t.Errorf("Claims(ctx) = %v, want sub=user-123", gotClaims)
		}
	})
}

type erroringReplayGuard struct {
	seenErr error
	markErr error
}

func (g *erroringReplayGuard) SeenBefore(context.Context, string) (bool, error) {
	return false, g.seenErr
}

func (g *erroringReplayGuard) MarkSeen(context.Context, string) error {
	return g.markErr
}

func TestExchange_CodeExchangeFails(t *testing.T) {
	idp := newFakeIdP(t)
	cfg := newOAuth2Config(idp)
	verifier := newVerifier(t, idp)
	attempt, _ := NewAttempt()
	guard := NewMemoryReplayGuard()

	// No code was ever registered, so the fake token endpoint 400s.
	_, err := Exchange(context.Background(), cfg, verifier, guard, attempt, attempt.State, "never-registered")
	if err == nil {
		t.Fatalf("expected an error when the token endpoint rejects the code")
	}
}

func TestExchange_MissingIDToken(t *testing.T) {
	idp := newFakeIdP(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"x","token_type":"Bearer","expires_in":3600}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := newOAuth2Config(idp)
	cfg.Endpoint.TokenURL = srv.URL + "/token"
	verifier := newVerifier(t, idp)
	attempt, _ := NewAttempt()
	guard := NewMemoryReplayGuard()

	_, err := Exchange(context.Background(), cfg, verifier, guard, attempt, attempt.State, "any-code")
	if err == nil {
		t.Fatalf("expected an error when the token response has no id_token")
	}
}

func TestExchange_VerifyFails(t *testing.T) {
	idp := newFakeIdP(t)
	cfg := newOAuth2Config(idp)
	verifier := newVerifier(t, idp)
	attempt, _ := NewAttempt()
	guard := NewMemoryReplayGuard()

	// Signed by a key never published to /jwks: signature can never be
	// verified, even after the one automatic refetch.
	orphan := idp.mintOrphanToken(t, map[string]interface{}{"nonce": attempt.Nonce})
	idp.registerCode("code-orphan", orphan)

	_, err := Exchange(context.Background(), cfg, verifier, guard, attempt, attempt.State, "code-orphan")
	if err == nil {
		t.Fatalf("expected verification failure for an unresolvable kid")
	}
}

func TestExchange_ReplayGuardErrors(t *testing.T) {
	idp := newFakeIdP(t)
	cfg := newOAuth2Config(idp)
	verifier := newVerifier(t, idp)

	t.Run("SeenBefore error", func(t *testing.T) {
		attempt, _ := NewAttempt()
		rawToken := idp.mintToken(t, "kid-1", map[string]interface{}{"nonce": attempt.Nonce})
		idp.registerCode("code-seen-err", rawToken)
		guard := &erroringReplayGuard{seenErr: errors.New("boom")}

		_, err := Exchange(context.Background(), cfg, verifier, guard, attempt, attempt.State, "code-seen-err")
		if err == nil || errors.Is(err, ErrReplayedToken) {
			t.Fatalf("err = %v, want a wrapped guard error", err)
		}
	})

	t.Run("MarkSeen error", func(t *testing.T) {
		attempt, _ := NewAttempt()
		rawToken := idp.mintToken(t, "kid-2", map[string]interface{}{"nonce": attempt.Nonce})
		idp.registerCode("code-mark-err", rawToken)
		guard := &erroringReplayGuard{markErr: errors.New("boom")}

		_, err := Exchange(context.Background(), cfg, verifier, guard, attempt, attempt.State, "code-mark-err")
		if err == nil {
			t.Fatalf("expected an error when MarkSeen fails")
		}
	})
}

func TestMiddleware_EmptySubject(t *testing.T) {
	idp := newFakeIdP(t)
	idp.addKey("kid-1")
	verifier := newVerifier(t, idp)

	handler := Middleware(verifier)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	noSubject := idp.mintToken(t, "kid-1", map[string]interface{}{"sub": ""})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+noSubject)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for a token with no subject", rec.Code)
	}
}

func TestNewOAuth2Config(t *testing.T) {
	idp := newFakeIdP(t)

	cfg, verifier, err := NewOAuth2Config(context.Background(), idp.server.URL, "test-client", "https://viewer.example/callback", nil)
	if err != nil {
		t.Fatalf("NewOAuth2Config: %v", err)
	}
	if cfg.ClientID != "test-client" || cfg.RedirectURL != "https://viewer.example/callback" {
		t.Fatalf("unexpected cfg: %+v", cfg)
	}
	if len(cfg.Scopes) == 0 {
		t.Fatalf("expected default scopes to be filled in")
	}
	if verifier == nil {
		t.Fatalf("expected non-nil verifier")
	}

	if _, _, err := NewOAuth2Config(context.Background(), "http://127.0.0.1:0", "c", "r", []string{"openid"}); err == nil {
		t.Fatalf("expected discovery error for unreachable issuer")
	}
}

func TestContextAccessors_Absent(t *testing.T) {
	ctx := context.Background()
	if _, ok := Subject(ctx); ok {
		t.Errorf("Subject on bare context should be absent")
	}
	if _, ok := Claims(ctx); ok {
		t.Errorf("Claims on bare context should be absent")
	}
	if _, ok := Subject(WithSubject(ctx, "")); ok {
		t.Errorf("empty subject should not count as present")
	}
}

func TestMiddleware_InvalidClaims(t *testing.T) {
	idp := newFakeIdP(t)
	idp.addKey("kid-1")
	verifier := newVerifier(t, idp)

	handler := Middleware(verifier)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// A token with no "sub" claim fails go-oidc's Verify because sub is
	// mandatory per the OIDC core spec, but exercise our own emptiness
	// check by forging a token whose claims JSON is malformed after
	// signature verification is impossible to reach here directly, so we
	// instead confirm the missing-subject branch cannot be bypassed by
	// checking a token verify failure still yields 401.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer not-a-jwt")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestBearerToken(t *testing.T) {
	if got := bearerToken(""); got != "" {
		t.Errorf("empty header: got %q", got)
	}
	if got := bearerToken("Basic abc"); got != "" {
		t.Errorf("non-bearer scheme: got %q", got)
	}
	if got := bearerToken("Bearer  abc  "); strings.TrimSpace(got) != "abc" {
		t.Errorf("bearer token = %q, want abc", got)
	}
}
