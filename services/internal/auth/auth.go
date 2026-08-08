// Package auth implements OIDC authentication for How to Replicate: the
// PKCE authorization-code flow the web viewer (a public client, no secret)
// uses to sign in, and Bearer-token verification middleware for services.
//
// This package stops at identity. It resolves who the caller is (the
// token's subject and claims) and puts that on the request context. It
// does NOT resolve workspace role or membership — callers such as
// sync-gateway and capture-api read Subject(ctx) and do their own
// `memberships` lookup downstream. Conflating identity with authorization
// here would mean every service that changes its role model must also
// touch this shared package.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
)

// Sentinel errors returned by Exchange. Callers switch on these rather than
// string-matching, and the distinct identities are required by the redaction
// corpus's negative-auth tests.
var (
	// ErrStateMismatch is returned when the state returned by the IdP on
	// callback does not match the state minted for this attempt.
	ErrStateMismatch = errors.New("auth: state mismatch")
	// ErrNonceMismatch is returned when the verified ID token's nonce claim
	// does not match the nonce minted for this attempt. go-oidc verifies
	// signature/iss/aud/exp but does not check nonce — that's on us.
	ErrNonceMismatch = errors.New("auth: nonce mismatch")
	// ErrReplayedToken is returned when the same ID token (by raw value) has
	// already been consumed by a prior Exchange call.
	ErrReplayedToken = errors.New("auth: replayed id token")
)

// Attempt holds the per-authentication-attempt secrets: the state and nonce
// to detect CSRF/replay on the authorization redirect, and the PKCE
// verifier/challenge pair proving the client completing the flow is the one
// that started it. Callers store an Attempt in the user's session between
// BuildAuthURL and Exchange.
type Attempt struct {
	State         string
	Nonce         string
	CodeVerifier  string
	CodeChallenge string
}

// codeVerifierBytes is chosen so the base64url-encoded verifier lands in the
// RFC 7636 required range of 43-128 characters (32 bytes -> 43 chars).
const codeVerifierBytes = 32

// randRead is a seam over crypto/rand.Read so tests can exercise randToken's
// (and therefore NewAttempt's) error path — the real reader does not fail in
// practice, so there is no other way to reach that branch.
var randRead = rand.Read

// randToken returns a base64url (no padding) encoding of n cryptographically
// random bytes.
func randToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := randRead(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// NewAttempt generates a fresh state, nonce, and PKCE verifier/challenge
// (S256) for one authorization-code attempt.
func NewAttempt() (*Attempt, error) {
	state, err := randToken(16)
	if err != nil {
		return nil, err
	}
	nonce, err := randToken(16)
	if err != nil {
		return nil, err
	}
	verifier, err := randToken(codeVerifierBytes)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	return &Attempt{
		State:         state,
		Nonce:         nonce,
		CodeVerifier:  verifier,
		CodeChallenge: challenge,
	}, nil
}

// ReplayGuard tracks ID tokens already consumed by Exchange so a captured
// and replayed token is rejected. tokenID is whatever the caller keys on in
// practice this package uses the raw ID token string, which is unique per
// issuance.
type ReplayGuard interface {
	SeenBefore(ctx context.Context, tokenID string) (bool, error)
	MarkSeen(ctx context.Context, tokenID string) error
}
