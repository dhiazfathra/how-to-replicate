// Package sharelink mints and verifies capture-api's no-login share-link
// tokens.
//
// Token format: base64url(payload-json) + "." + base64url(HMAC-SHA256(key,
// payload-json)), where payload is {keyID, shareLinkID, captureID,
// expiresUnix}. The signature is over the raw payload bytes so verification
// never needs to re-derive them.
//
// The payload carries a shareLinkID (a random ID minted at share-link
// creation, stored on the share_links row) rather than a bare capture ID:
// knowing or guessing a capture ID must not be enough to forge a
// plausible-looking token, and two share links pointing at the same
// capture must be independently revocable. shareLinkID is that
// independent, opaque revocation handle.
//
// Key rotation: Signer holds a current key (used to sign new tokens) plus
// zero or more previous keys (tried only for verification), each
// identified by a keyID embedded in the token payload. Rotating the
// current key without discarding the previous one lets already-issued
// tokens keep verifying until they expire.
package sharelink

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"
)

// ErrInvalidToken is returned for any malformed, unsigned, or
// wrong-signature token. It deliberately doesn't distinguish "malformed"
// from "bad signature" from "unknown key" — none of that should be
// observable to a caller trying to forge a token.
var ErrInvalidToken = errors.New("sharelink: invalid token")

// ErrExpired is returned when the token's embedded expiry has passed. It is
// distinct from ErrInvalidToken because a genuinely-issued, correctly
// signed token can expire; that's not a forgery attempt.
var ErrExpired = errors.New("sharelink: token expired")

type payload struct {
	KeyID       string `json:"kid"`
	ShareLinkID string `json:"sid"`
	CaptureID   string `json:"cid"`
	ExpiresUnix int64  `json:"exp"`
}

// Key is one HMAC signing key, identified by an opaque ID so a token can
// name which key signed it.
type Key struct {
	ID     string
	Secret []byte
}

// Signer mints and verifies share-link tokens. Current signs new tokens;
// Previous is consulted (by keyID) only when verifying, so keys can be
// rotated without invalidating tokens issued under the outgoing key before
// it's removed from Previous too.
type Signer struct {
	Current  Key
	Previous []Key
}

// Sign mints a token for shareLinkID/captureID, expiring at expiresAt.
func (s Signer) Sign(shareLinkID, captureID string, expiresAt time.Time) (string, error) {
	p := payload{
		KeyID:       s.Current.ID,
		ShareLinkID: shareLinkID,
		CaptureID:   captureID,
		ExpiresUnix: expiresAt.Unix(),
	}
	// payload is a plain struct of strings/ints — json.Marshal on it cannot
	// fail, so there is no error branch here to test (same reasoning as
	// capture-api's updatePolicy handler for policy.Overrides).
	raw, _ := json.Marshal(p)
	sig := sign(s.Current.Secret, raw)
	return encode(raw) + "." + encode(sig), nil
}

// Verify checks signature, key validity, and expiry, and returns the
// shareLinkID/captureID the token names. It does NOT check revocation or
// capture state — those require a database read and are the resolver's
// job, checked against the live share_links/captures rows, not anything
// baked into the token.
func (s Signer) Verify(token string, now time.Time) (shareLinkID, captureID string, err error) {
	rawB64, sigB64, ok := splitOnce(token, '.')
	if !ok {
		return "", "", ErrInvalidToken
	}
	raw, err := decode(rawB64)
	if err != nil {
		return "", "", ErrInvalidToken
	}
	sig, err := decode(sigB64)
	if err != nil {
		return "", "", ErrInvalidToken
	}

	var p payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", "", ErrInvalidToken
	}

	key, ok := s.keyByID(p.KeyID)
	if !ok || !hmac.Equal(sig, sign(key.Secret, raw)) {
		return "", "", ErrInvalidToken
	}

	if now.Unix() > p.ExpiresUnix {
		return "", "", ErrExpired
	}
	return p.ShareLinkID, p.CaptureID, nil
}

func (s Signer) keyByID(id string) (Key, bool) {
	if subtle.ConstantTimeCompare([]byte(s.Current.ID), []byte(id)) == 1 {
		return s.Current, true
	}
	for _, k := range s.Previous {
		if subtle.ConstantTimeCompare([]byte(k.ID), []byte(id)) == 1 {
			return k, true
		}
	}
	return Key{}, false
}

func sign(secret, msg []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write(msg)
	return mac.Sum(nil)
}

func encode(b []byte) string          { return base64.RawURLEncoding.EncodeToString(b) }
func decode(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(s) }

func splitOnce(s string, sep byte) (before, after string, ok bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			return s[:i], s[i+1:], true
		}
	}
	return "", "", false
}
