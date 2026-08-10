package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

// fakeIdP is a minimal OIDC provider for tests: discovery + JWKS + a token
// endpoint that mints an ID token from whatever claims the test registered
// for the "code" it receives. It also counts JWKS fetches so the JWKS
// rotation tests can assert exactly one refetch.
type fakeIdP struct {
	t        *testing.T
	server   *httptest.Server
	mu       sync.Mutex
	keys     []jose.JSONWebKey // keys currently served at /jwks
	signers  map[string]*rsa.PrivateKey
	codes    map[string]map[string]interface{}
	jwksHits int32
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	idp := &fakeIdP{
		t:       t,
		signers: make(map[string]*rsa.PrivateKey),
		codes:   make(map[string]map[string]interface{}),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", idp.discovery)
	mux.HandleFunc("/jwks", idp.jwks)
	mux.HandleFunc("/token", idp.token)
	idp.server = httptest.NewServer(mux)
	t.Cleanup(idp.server.Close)
	return idp
}

func (idp *fakeIdP) discovery(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"issuer":                                idp.server.URL,
		"authorization_endpoint":                idp.server.URL + "/authorize",
		"token_endpoint":                        idp.server.URL + "/token",
		"jwks_uri":                              idp.server.URL + "/jwks",
		"id_token_signing_alg_values_supported": []string{"RS256"},
	})
}

func (idp *fakeIdP) jwks(w http.ResponseWriter, _ *http.Request) {
	atomic.AddInt32(&idp.jwksHits, 1)
	idp.mu.Lock()
	keys := append([]jose.JSONWebKey(nil), idp.keys...)
	idp.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: keys})
}

func (idp *fakeIdP) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	code := r.FormValue("code")
	idp.mu.Lock()
	claims, ok := idp.codes[code]
	idp.mu.Unlock()
	if !ok {
		http.Error(w, "unknown code", http.StatusBadRequest)
		return
	}
	rawIDToken, ok := claims["__raw_id_token__"].(string)
	if !ok {
		http.Error(w, "code registered without a token", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"access_token": "test-access-token",
		"token_type":   "Bearer",
		"expires_in":   3600,
		"id_token":     rawIDToken,
	})
}

// addKey generates a fresh RSA key with the given kid and adds it to the
// set served at /jwks.
func (idp *fakeIdP) addKey(kid string) *rsa.PrivateKey {
	idp.t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		idp.t.Fatalf("generate key: %v", err)
	}
	idp.mu.Lock()
	idp.signers[kid] = key
	idp.keys = append(idp.keys, jose.JSONWebKey{
		Key: &key.PublicKey, KeyID: kid, Algorithm: "RS256", Use: "sig",
	})
	idp.mu.Unlock()
	return key
}

// mintToken signs claims (merged over defaults for iss/aud/exp/iat) with
// the named kid and returns the compact JWS. It does not require that kid
// currently be served at /jwks — tests use that to simulate an
// as-yet-unseen kid.
func (idp *fakeIdP) mintToken(t *testing.T, kid string, extra map[string]interface{}) string {
	t.Helper()
	idp.mu.Lock()
	key, ok := idp.signers[kid]
	idp.mu.Unlock()
	if !ok {
		key = idp.addKey(kid)
	}
	return idp.sign(t, key, kid, extra)
}

// mintOrphanToken signs a token with a brand-new key never added to /jwks,
// simulating a kid that will never resolve.
func (idp *fakeIdP) mintOrphanToken(t *testing.T, extra map[string]interface{}) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return idp.sign(t, key, "orphan-kid", extra)
}

func (idp *fakeIdP) sign(t *testing.T, key *rsa.PrivateKey, kid string, extra map[string]interface{}) string {
	t.Helper()
	now := time.Now()
	claims := map[string]interface{}{
		"iss": idp.server.URL,
		"aud": "test-client",
		"sub": "user-123",
		"iat": now.Unix(),
		"exp": now.Add(time.Hour).Unix(),
	}
	for k, v := range extra {
		claims[k] = v
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key},
		(&jose.SignerOptions{}).WithHeader("kid", kid))
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
	return compact
}

// registerCode makes the token endpoint return rawIDToken when code is
// exchanged.
func (idp *fakeIdP) registerCode(code, rawIDToken string) {
	idp.mu.Lock()
	idp.codes[code] = map[string]interface{}{"__raw_id_token__": rawIDToken}
	idp.mu.Unlock()
}

func (idp *fakeIdP) jwksHitCount() int32 {
	return atomic.LoadInt32(&idp.jwksHits)
}
