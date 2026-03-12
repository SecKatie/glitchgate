// SPDX-License-Identifier: AGPL-3.0-or-later

package oidc

import (
	"context"
	"fmt"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"codeberg.org/kglitchy/glitchgate/internal/config"
)

// Claims holds the identity claims extracted from a verified ID token.
type Claims struct {
	Subject     string
	Email       string
	DisplayName string
}

// Provider wraps go-oidc and x/oauth2 to implement the OIDC authorization
// code flow with PKCE. It has no dependency on net/http request/response types.
type Provider struct {
	verifier *gooidc.IDTokenVerifier
	oauth2   oauth2.Config
}

// NewProvider discovers the OIDC configuration from the issuer and returns a
// ready-to-use Provider. Returns an error if discovery fails.
func NewProvider(ctx context.Context, cfg *config.OIDCConfig) (*Provider, error) {
	p, err := gooidc.NewProvider(ctx, cfg.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}

	scopes := cfg.Scopes
	if len(scopes) == 0 {
		scopes = []string{gooidc.ScopeOpenID, "email", "profile"}
	}

	return &Provider{
		verifier: p.Verifier(&gooidc.Config{ClientID: cfg.ClientID}),
		oauth2: oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Endpoint:     p.Endpoint(),
			Scopes:       scopes,
		},
	}, nil
}

// Enabled satisfies the web.OIDCProvider interface. Always true for a non-nil Provider.
func (p *Provider) Enabled() bool { return p != nil }

// AuthURL generates the IDP authorization URL including the PKCE challenge.
func (p *Provider) AuthURL(state, pkceChallenge string) string {
	return p.oauth2.AuthCodeURL(state, oauth2.S256ChallengeOption(pkceChallenge))
}

// Exchange completes the authorization code flow: exchanges the code for tokens,
// verifies the ID token, and returns the resolved Claims.
func (p *Provider) Exchange(ctx context.Context, code, pkceVerifier string) (*Claims, error) {
	token, err := p.oauth2.Exchange(ctx, code, oauth2.VerifierOption(pkceVerifier))
	if err != nil {
		return nil, fmt.Errorf("oidc token exchange: %w", err)
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return nil, fmt.Errorf("oidc exchange: id_token missing from token response")
	}

	idToken, err := p.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("oidc id token verification: %w", err)
	}

	var claims struct {
		Subject string `json:"sub"`
		Email   string `json:"email"`
		Name    string `json:"name"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("oidc claims extraction: %w", err)
	}

	displayName := claims.Name
	if displayName == "" {
		displayName = claims.Email
	}

	return &Claims{
		Subject:     claims.Subject,
		Email:       claims.Email,
		DisplayName: displayName,
	}, nil
}
