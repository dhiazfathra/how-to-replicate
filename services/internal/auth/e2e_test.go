package auth

// e2e_test.go drives the OIDC flow through the actual HTTP surface end to
// end: a real net/http server exposing /login, /callback, and a Middleware-
// protected /protected route, exercised by a real http.Client over real
// TCP connections — not direct in-process calls into Exchange/Middleware
// the way auth_test.go's per-function unit tests do. BuildAuthURL, Exchange,
// and Middleware each get a real client-facing HTTP round trip in this one
// scenario.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestOIDC_EndToEnd_HTTP(t *testing.T) {
	idp := newFakeIdP(t)
	idp.addKey("kid-1")

	ctx := context.Background()
	cfg, verifier, err := NewOAuth2Config(ctx, idp.server.URL, "test-client", "http://app.example/callback", nil)
	if err != nil {
		t.Fatalf("NewOAuth2Config: %v", err)
	}

	// One attempt shared between /login and /callback, standing in for the
	// per-session storage a real web server would use.
	var attempt *Attempt
	guard := NewMemoryReplayGuard()

	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		a, err := NewAttempt()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		attempt = a
		http.Redirect(w, r, BuildAuthURL(cfg, a), http.StatusFound)
	})
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		idToken, err := Exchange(r.Context(), cfg, verifier, guard, attempt, r.URL.Query().Get("state"), r.URL.Query().Get("code"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"subject": idToken.Subject})
	})
	mux.Handle("/protected", Middleware(verifier)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		subject, _ := Subject(r.Context())
		_, _ = w.Write([]byte("hello " + subject))
	})))

	app := httptest.NewServer(mux)
	defer app.Close()

	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	// 1. BuildAuthURL, over real HTTP: GET /login must redirect somewhere
	// carrying the PKCE challenge and pointing at the IdP's authorize
	// endpoint.
	loginResp, err := client.Get(app.URL + "/login")
	if err != nil {
		t.Fatalf("GET /login: %v", err)
	}
	_ = loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusFound {
		t.Fatalf("GET /login status = %d, want 302", loginResp.StatusCode)
	}
	location := loginResp.Header.Get("Location")
	redirectURL, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse redirect url %q: %v", location, err)
	}
	if got := redirectURL.Query().Get("code_challenge"); got != attempt.CodeChallenge {
		t.Fatalf("redirect code_challenge = %q, want %q", got, attempt.CodeChallenge)
	}

	// 2. Simulate the IdP redirecting back with an authorization code: mint
	// and register the ID token the fake token endpoint will hand back for
	// this code, then hit /callback over real HTTP. Exchange runs for real
	// against the fake IdP's /token and /jwks endpoints over the network.
	rawIDToken := idp.mintToken(t, "kid-1", map[string]interface{}{"nonce": attempt.Nonce})
	idp.registerCode("auth-code-1", rawIDToken)

	callbackResp, err := client.Get(app.URL + "/callback?state=" + attempt.State + "&code=auth-code-1")
	if err != nil {
		t.Fatalf("GET /callback: %v", err)
	}
	defer func() { _ = callbackResp.Body.Close() }()
	if callbackResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(callbackResp.Body)
		t.Fatalf("GET /callback status = %d, body = %s", callbackResp.StatusCode, body)
	}
	var callbackBody struct{ Subject string }
	if err := json.NewDecoder(callbackResp.Body).Decode(&callbackBody); err != nil {
		t.Fatalf("decode callback body: %v", err)
	}
	if callbackBody.Subject != "user-123" {
		t.Fatalf("callback subject = %q, want user-123", callbackBody.Subject)
	}

	// 3. Middleware, over real HTTP: the same raw ID token, presented as a
	// Bearer credential to a real protected route, must resolve to the same
	// subject Exchange verified.
	protectedReq, err := http.NewRequest(http.MethodGet, app.URL+"/protected", nil)
	if err != nil {
		t.Fatalf("build protected request: %v", err)
	}
	protectedReq.Header.Set("Authorization", "Bearer "+rawIDToken)
	protectedResp, err := client.Do(protectedReq)
	if err != nil {
		t.Fatalf("GET /protected: %v", err)
	}
	defer func() { _ = protectedResp.Body.Close() }()
	body, _ := io.ReadAll(protectedResp.Body)
	if protectedResp.StatusCode != http.StatusOK || string(body) != "hello user-123" {
		t.Fatalf("GET /protected = (%d, %q), want (200, %q)", protectedResp.StatusCode, body, "hello user-123")
	}

	// 4. Replaying the same authorization code's ID token through /callback
	// again must be rejected — the replay guard is real, not stubbed, in
	// this end-to-end wiring.
	replayResp, err := client.Get(app.URL + "/callback?state=" + attempt.State + "&code=auth-code-1")
	if err != nil {
		t.Fatalf("GET /callback (replay): %v", err)
	}
	_ = replayResp.Body.Close()
	if replayResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("replayed /callback status = %d, want 401", replayResp.StatusCode)
	}
}
