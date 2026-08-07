package auth

import (
	"context"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// NewOAuth2Config discovers issuerURL's OIDC configuration and returns the
// oauth2.Config for a public client (clientSecret is deliberately not a
// parameter — the web viewer never holds one, PKCE replaces it) plus the
// ID-token verifier for clientID.
//
// The verifier's key set is fetched over the network via
// oidc.Provider.Verifier, which wraps oidc.NewRemoteKeySet: it caches JWKS,
// and on a token whose kid isn't in the cache it refetches once before
// rejecting — key rotation is honoured without any code here.
func NewOAuth2Config(ctx context.Context, issuerURL, clientID, redirectURL string, scopes []string) (oauth2.Config, *oidc.IDTokenVerifier, error) {
	provider, err := oidc.NewProvider(ctx, issuerURL)
	if err != nil {
		return oauth2.Config{}, nil, fmt.Errorf("auth: discover provider %q: %w", issuerURL, err)
	}

	if len(scopes) == 0 {
		scopes = []string{oidc.ScopeOpenID, "profile", "email"}
	}

	cfg := oauth2.Config{
		ClientID:    clientID,
		RedirectURL: redirectURL,
		Endpoint:    provider.Endpoint(),
		Scopes:      scopes,
	}
	verifier := provider.Verifier(&oidc.Config{ClientID: clientID})

	return cfg, verifier, nil
}
