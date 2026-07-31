package auth

import (
	"context"
	"errors"
)

var ErrInvalidToken = errors.New("invalid access token")

type Principal struct {
	Subject, TenantID, ApplicationID, Environment string
	Scopes                                        map[string]struct{}
	Roles                                         map[string]struct{}
}

func (p Principal) HasScope(scope string) bool { _, ok := p.Scopes[scope]; return ok }
func (p Principal) HasRole(role string) bool   { _, ok := p.Roles[role]; return ok }

type Verifier interface {
	Verify(context.Context, string) (Principal, error)
}
