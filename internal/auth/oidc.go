package auth

import (
	"context"
	"fmt"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
)

type OIDCVerifier struct {
	verifier    *oidc.IDTokenVerifier
	environment string
}

type accessTokenClaims struct {
	Subject       string `json:"sub"`
	TenantID      string `json:"tenant_id"`
	ApplicationID string `json:"application_id"`
	Environment   string `json:"environment"`
	Scope         string `json:"scope"`
	RealmAccess   struct {
		Roles []string `json:"roles"`
	} `json:"realm_access"`
}

func NewOIDCVerifier(ctx context.Context, issuer, audience, environment string) (*OIDCVerifier, error) {
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("discover OIDC provider: %w", err)
	}
	return &OIDCVerifier{verifier: provider.Verifier(&oidc.Config{ClientID: audience}), environment: environment}, nil
}

func (v *OIDCVerifier) Verify(ctx context.Context, raw string) (Principal, error) {
	token, err := v.verifier.Verify(ctx, raw)
	if err != nil {
		return Principal{}, fmt.Errorf("%w: signature, issuer, audience, or expiry rejected", ErrInvalidToken)
	}
	var claims accessTokenClaims
	if err := token.Claims(&claims); err != nil {
		return Principal{}, fmt.Errorf("%w: claims rejected", ErrInvalidToken)
	}
	if claims.Subject == "" || claims.TenantID == "" || claims.Environment != v.environment {
		return Principal{}, ErrInvalidToken
	}
	p := Principal{Subject: claims.Subject, TenantID: claims.TenantID, ApplicationID: claims.ApplicationID, Environment: claims.Environment, Scopes: map[string]struct{}{}, Roles: map[string]struct{}{}}
	for _, scope := range strings.Fields(claims.Scope) {
		p.Scopes[scope] = struct{}{}
	}
	for _, role := range claims.RealmAccess.Roles {
		p.Roles[role] = struct{}{}
	}
	return p, nil
}
