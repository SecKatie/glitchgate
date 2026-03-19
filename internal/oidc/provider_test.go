package oidc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/stretchr/testify/require"

	"github.com/seckatie/glitchgate/internal/config"
)

func TestProviderEnabled(t *testing.T) {
	var nilProvider *Provider
	require.False(t, nilProvider.Enabled())
	require.True(t, (&Provider{}).Enabled())
}

func TestNewProviderUsesDefaultScopesAndAuthURL(t *testing.T) {
	server := newOIDCTestServer(t)
	defer server.Close()

	provider, err := NewProvider(context.Background(), &config.OIDCConfig{
		IssuerURL:    server.URL,
		ClientID:     "glitchgate-test",
		ClientSecret: "secret",
		RedirectURL:  "https://app.example.com/callback",
	})
	require.NoError(t, err)

	authURL := provider.AuthURL("state-123", "challenge-456")
	parsed, err := url.Parse(authURL)
	require.NoError(t, err)
	require.Equal(t, server.URL+"/authorize", parsed.Scheme+"://"+parsed.Host+parsed.Path)

	query := parsed.Query()
	require.Equal(t, "glitchgate-test", query.Get("client_id"))
	require.Equal(t, "https://app.example.com/callback", query.Get("redirect_uri"))
	require.Equal(t, "code", query.Get("response_type"))
	require.Equal(t, "state-123", query.Get("state"))
	require.Equal(t, "challenge-456", query.Get("code_challenge"))
	require.Equal(t, "S256", query.Get("code_challenge_method"))
	require.Equal(t, "openid email profile", query.Get("scope"))
}

func TestNewProviderPreservesConfiguredScopes(t *testing.T) {
	server := newOIDCTestServer(t)
	defer server.Close()

	provider, err := NewProvider(context.Background(), &config.OIDCConfig{
		IssuerURL:    server.URL,
		ClientID:     "glitchgate-test",
		ClientSecret: "secret",
		RedirectURL:  "https://app.example.com/callback",
		Scopes:       []string{"openid", "groups"},
	})
	require.NoError(t, err)

	authURL := provider.AuthURL("state-123", "challenge-456")
	parsed, err := url.Parse(authURL)
	require.NoError(t, err)
	require.Equal(t, "openid groups", parsed.Query().Get("scope"))
}

func TestProviderExchangeSuccessFallsBackDisplayNameToEmail(t *testing.T) {
	server := newOIDCTestServer(t)
	defer server.Close()

	provider, err := NewProvider(context.Background(), &config.OIDCConfig{
		IssuerURL:    server.URL,
		ClientID:     "glitchgate-test",
		ClientSecret: "secret",
		RedirectURL:  "https://app.example.com/callback",
	})
	require.NoError(t, err)

	claims, err := provider.Exchange(context.Background(), "good-code", "verifier-123")
	require.NoError(t, err)
	require.Equal(t, &Claims{
		Subject:     "user-123",
		Email:       "user@example.com",
		DisplayName: "user@example.com",
	}, claims)

	tokenRequest := server.lastTokenRequest()
	require.NotNil(t, tokenRequest)
	require.Equal(t, "authorization_code", tokenRequest.Get("grant_type"))
	require.Equal(t, "good-code", tokenRequest.Get("code"))
	require.Equal(t, "verifier-123", tokenRequest.Get("code_verifier"))
	require.Equal(t, "https://app.example.com/callback", tokenRequest.Get("redirect_uri"))
}

func TestProviderExchangeMissingIDToken(t *testing.T) {
	server := newOIDCTestServer(t)
	server.missingIDToken = true
	defer server.Close()

	provider, err := NewProvider(context.Background(), &config.OIDCConfig{
		IssuerURL:    server.URL,
		ClientID:     "glitchgate-test",
		ClientSecret: "secret",
		RedirectURL:  "https://app.example.com/callback",
	})
	require.NoError(t, err)

	claims, err := provider.Exchange(context.Background(), "good-code", "verifier-123")
	require.Nil(t, claims)
	require.EqualError(t, err, "oidc exchange: id_token missing from token response")
}

func TestProviderExchangePropagatesTokenErrors(t *testing.T) {
	server := newOIDCTestServer(t)
	server.tokenStatusCode = http.StatusBadGateway
	defer server.Close()

	provider, err := NewProvider(context.Background(), &config.OIDCConfig{
		IssuerURL:    server.URL,
		ClientID:     "glitchgate-test",
		ClientSecret: "secret",
		RedirectURL:  "https://app.example.com/callback",
	})
	require.NoError(t, err)

	claims, err := provider.Exchange(context.Background(), "bad-code", "verifier-123")
	require.Nil(t, claims)
	require.Error(t, err)
	require.Contains(t, err.Error(), "oidc token exchange")
}

type oidcTestServer struct {
	*httptest.Server
	t               *testing.T
	signer          jose.Signer
	keySet          jose.JSONWebKeySet
	clientID        string
	missingIDToken  bool
	tokenStatusCode int
	lastForm        url.Values
}

func newOIDCTestServer(t *testing.T) *oidcTestServer {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: privateKey}, nil)
	require.NoError(t, err)

	srv := &oidcTestServer{
		t:        t,
		signer:   signer,
		keySet:   jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &privateKey.PublicKey, KeyID: "test-key", Algorithm: string(jose.RS256), Use: "sig"}}},
		clientID: "glitchgate-test",
	}

	handler := http.NewServeMux()
	handler.HandleFunc("/.well-known/openid-configuration", srv.handleDiscovery)
	handler.HandleFunc("/authorize", srv.handleAuthorize)
	handler.HandleFunc("/token", srv.handleToken)
	handler.HandleFunc("/keys", srv.handleKeys)

	srv.Server = httptest.NewServer(handler)
	return srv
}

func (s *oidcTestServer) handleDiscovery(w http.ResponseWriter, r *http.Request) {
	_ = r
	writeJSON(s.t, w, map[string]any{
		"issuer":                 s.URL,
		"authorization_endpoint": s.URL + "/authorize",
		"token_endpoint":         s.URL + "/token",
		"jwks_uri":               s.URL + "/keys",
		"id_token_signing_alg_values_supported": []string{
			string(jose.RS256),
		},
	})
}

func (s *oidcTestServer) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	_ = r
	http.NotFound(w, r)
}

func (s *oidcTestServer) handleToken(w http.ResponseWriter, r *http.Request) {
	require.NoError(s.t, r.ParseForm()) //nolint:gosec // test handler; request body comes from test client
	s.lastForm = cloneValues(r.PostForm)

	if s.tokenStatusCode != 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(s.tokenStatusCode)
		writeJSON(s.t, w, map[string]any{
			"error":             "temporarily_unavailable",
			"error_description": "upstream unavailable",
		})
		return
	}

	response := map[string]any{
		"access_token": "access-token",
		"token_type":   "Bearer",
		"expires_in":   3600,
	}
	if !s.missingIDToken {
		response["id_token"] = s.mustSignedToken()
	}
	writeJSON(s.t, w, response)
}

func (s *oidcTestServer) handleKeys(w http.ResponseWriter, r *http.Request) {
	_ = r
	writeJSON(s.t, w, s.keySet)
}

func (s *oidcTestServer) mustSignedToken() string {
	raw, err := jwt.Signed(s.signer).Claims(jwt.Claims{
		Issuer:   s.URL,
		Subject:  "user-123",
		Audience: jwt.Audience{s.clientID},
		Expiry:   jwt.NewNumericDate(time.Now().Add(time.Hour)),
		IssuedAt: jwt.NewNumericDate(time.Now()),
	}).Claims(map[string]any{
		"email": "user@example.com",
		"name":  "",
	}).Serialize()
	require.NoError(s.t, err)

	return raw
}

func (s *oidcTestServer) lastTokenRequest() url.Values {
	if s.lastForm == nil {
		return nil
	}

	return cloneValues(s.lastForm)
}

func writeJSON(t *testing.T, w http.ResponseWriter, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(payload))
}

func cloneValues(values url.Values) url.Values {
	clone := url.Values{}
	for key, entries := range values {
		clone[key] = append([]string(nil), entries...)
	}
	return clone
}

func TestNewProviderDiscoveryError(t *testing.T) {
	provider, err := NewProvider(context.Background(), &config.OIDCConfig{
		IssuerURL:    "http://127.0.0.1:1",
		ClientID:     "glitchgate-test",
		ClientSecret: "secret",
		RedirectURL:  "https://app.example.com/callback",
	})
	require.Nil(t, provider)
	require.Error(t, err)
	require.True(t, strings.HasPrefix(err.Error(), "oidc discovery:"))
}
