package auth

import (
	"context"
	"crypto/subtle"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// BuildAuthURL returns the authorization-endpoint URL for attempt, carrying
// state, nonce, and the PKCE code_challenge/method per RFC 7636. cfg's
// ClientSecret must be empty: the web viewer is a public client and never
// holds one.
func BuildAuthURL(cfg oauth2.Config, attempt *Attempt) string {
	return cfg.AuthCodeURL(attempt.State,
		oidc.Nonce(attempt.Nonce),
		oauth2.S256ChallengeOption(attempt.CodeVerifier),
	)
}

// Exchange completes the authorization-code callback: it checks gotState
// against attempt (the CSRF control), exchanges code for tokens using the
// PKCE verifier, verifies the returned ID token's signature/issuer/
// audience/expiry via verifier.Verify, then checks the nonce claim itself
// — go-oidc parses it onto IDToken.Nonce but does not compare it against
// anything, so that comparison is this package's job — and finally rejects
// a token already consumed according to guard.
func Exchange(
	ctx context.Context,
	cfg oauth2.Config,
	verifier *oidc.IDTokenVerifier,
	guard ReplayGuard,
	attempt *Attempt,
	gotState, code string,
) (*oidc.IDToken, error) {
	if subtle.ConstantTimeCompare([]byte(attempt.State), []byte(gotState)) != 1 {
		return nil, ErrStateMismatch
	}

	token, err := cfg.Exchange(ctx, code, oauth2.SetAuthURLParam("code_verifier", attempt.CodeVerifier))
	if err != nil {
		return nil, fmt.Errorf("auth: exchanging code: %w", err)
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return nil, fmt.Errorf("auth: token response missing id_token")
	}

	idToken, err := verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("auth: verifying id token: %w", err)
	}

	if subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(attempt.Nonce)) != 1 {
		return nil, ErrNonceMismatch
	}

	seen, err := guard.SeenBefore(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("auth: checking replay guard: %w", err)
	}
	if seen {
		return nil, ErrReplayedToken
	}
	if err := guard.MarkSeen(ctx, rawIDToken); err != nil {
		return nil, fmt.Errorf("auth: marking token seen: %w", err)
	}

	return idToken, nil
}
