// Package oidc implements OIDC Authorization Code + PKCE for Bindery.
// Providers are configured via the settings table (key "auth.oidc.providers")
// as a JSON array. Each provider gets its own go-oidc verifier with a shared
// JWKS cache so we never hit the IdP on every request.
package oidc

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// retryMinInterval is the minimum gap between on-demand re-discovery attempts
// for a single provider. Shorter gives faster recovery; longer protects the
// IdP from a hammered login button. Exposed as a package var so tests can
// override it without sleeping.
var retryMinInterval = 30 * time.Second

// ProviderConfig is the internal representation persisted to the settings
// table. It includes the client secret and must never be marshalled directly
// to an API response — use ProviderPublicConfig for that.
type ProviderConfig struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Issuer        string   `json:"issuer"`
	ClientID      string   `json:"client_id"`
	ClientSecret  string   `json:"client_secret"` // write-only: never returned to callers
	Scopes        []string `json:"scopes"`
	AllowedGroups []string `json:"allowed_groups,omitempty"`
}

// ProviderPublicConfig is the API-safe view of a provider — no secret fields.
// Used in GET /auth/oidc/providers responses.
type ProviderPublicConfig struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Issuer        string   `json:"issuer"`
	ClientID      string   `json:"client_id"`
	Scopes        []string `json:"scopes"`
	AllowedGroups []string `json:"allowed_groups,omitempty"`
}

// Public returns the API-safe view of the config with the secret stripped.
func (c ProviderConfig) Public() ProviderPublicConfig {
	return ProviderPublicConfig{
		ID:            c.ID,
		Name:          c.Name,
		Issuer:        c.Issuer,
		ClientID:      c.ClientID,
		Scopes:        c.Scopes,
		AllowedGroups: c.AllowedGroups,
	}
}

// ParseProviders deserialises the stored JSON array.
func ParseProviders(raw string) ([]ProviderConfig, error) {
	if raw == "" {
		return nil, nil
	}
	var ps []ProviderConfig
	if err := json.Unmarshal([]byte(raw), &ps); err != nil {
		return nil, fmt.Errorf("parse oidc providers: %w", err)
	}
	return ps, nil
}

// Claims extracted from a validated ID token.
type Claims struct {
	Sub               string
	Issuer            string
	Email             string
	PreferredUsername string
	Name              string
	Groups            []string
}

// Status is the runtime state of a configured provider as far as the manager
// knows. "ok" means the provider's verifier and oauth2 config are loaded and
// ready; "failed" means OIDC discovery did not succeed and the provider is
// not currently usable for login.
type Status struct {
	State       string    `json:"state"`                  // "ok" | "failed"
	LastError   string    `json:"last_error,omitempty"`   // populated when state=="failed"
	LastAttempt time.Time `json:"last_attempt,omitempty"` // last time discovery was tried
}

// Manager holds per-provider OIDC verifiers and oauth2 endpoint metadata,
// keyed by provider ID. It is rebuilt when the settings change. Providers
// whose initial discovery fails are kept in `failed` so admins can see them
// and so login attempts can trigger an on-demand retry.
//
// The OAuth2 redirect URL is resolved per-request rather than stored on the
// entry so that Bindery behind a reverse proxy doesn't strictly require
// BINDERY_OIDC_REDIRECT_BASE_URL — when that env var is unset the API layer
// derives the base URL from the request's forwarded headers.
type Manager struct {
	mu        sync.RWMutex
	providers map[string]*entry
	failed    map[string]*failedEntry
}

type entry struct {
	cfg      ProviderConfig
	verifier *gooidc.IDTokenVerifier
	endpoint oauth2.Endpoint
}

type failedEntry struct {
	cfg         ProviderConfig
	lastErr     error
	lastAttempt time.Time
}

func NewManager() *Manager {
	return &Manager{
		providers: make(map[string]*entry),
		failed:    make(map[string]*failedEntry),
	}
}

// CallbackPath returns the URL path appended to the redirect base URL for a
// given provider id. Exposed so the API handler can render the same path
// the manager uses when constructing the IdP redirect_uri parameter.
func CallbackPath(id string) string {
	return "/api/v1/auth/oidc/" + id + "/callback"
}

func (e *entry) oauth2Config(redirectBase string) oauth2.Config {
	scopes := e.cfg.Scopes
	if len(scopes) == 0 {
		scopes = []string{gooidc.ScopeOpenID, "profile", "email"}
	}
	return oauth2.Config{
		ClientID:     e.cfg.ClientID,
		ClientSecret: e.cfg.ClientSecret,
		Endpoint:     e.endpoint,
		RedirectURL:  redirectBase + CallbackPath(e.cfg.ID),
		Scopes:       scopes,
	}
}

// Reload replaces the provider set from a fresh config slice. Providers that
// haven't changed keep their verifier (and therefore JWKS cache). Providers
// whose discovery fails are recorded in the failed map (instead of silently
// dropped) so the admin UI can surface them and EnsureLoaded can retry.
func (m *Manager) Reload(ctx context.Context, cfgs []ProviderConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()

	next := make(map[string]*entry, len(cfgs))
	nextFailed := make(map[string]*failedEntry)
	for _, cfg := range cfgs {
		// Re-use the existing entry if config is identical (preserves JWKS cache).
		if e, ok := m.providers[cfg.ID]; ok && configEqual(e.cfg, cfg) {
			next[cfg.ID] = e
			continue
		}
		e, err := m.buildEntry(ctx, cfg)
		if err != nil {
			slog.Error("oidc: failed to initialise provider", "id", cfg.ID, "error", err)
			nextFailed[cfg.ID] = &failedEntry{cfg: cfg, lastErr: err, lastAttempt: time.Now()}
			continue
		}
		next[cfg.ID] = e
	}
	m.providers = next
	m.failed = nextFailed
}

func (m *Manager) buildEntry(ctx context.Context, cfg ProviderConfig) (*entry, error) {
	p, err := gooidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery for %q: %w", cfg.Issuer, err)
	}
	verifier := p.Verifier(&gooidc.Config{ClientID: cfg.ClientID})
	return &entry{cfg: cfg, verifier: verifier, endpoint: p.Endpoint()}, nil
}

// EnsureLoaded attempts on-demand re-discovery for a provider that is in the
// failed map, rate-limited by retryMinInterval to protect the IdP from a
// hammered login button. No-op if the provider is already loaded or unknown.
// Errors are recorded on the failed entry; this function never returns one.
func (m *Manager) EnsureLoaded(ctx context.Context, id string) {
	m.mu.RLock()
	if _, ok := m.providers[id]; ok {
		m.mu.RUnlock()
		return
	}
	f, isFailed := m.failed[id]
	m.mu.RUnlock()
	if !isFailed {
		return
	}
	if time.Since(f.lastAttempt) < retryMinInterval {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	// Re-check after acquiring the write lock — another goroutine may have
	// won the race and either loaded the provider or just attempted retry.
	if _, ok := m.providers[id]; ok {
		return
	}
	f, isFailed = m.failed[id]
	if !isFailed || time.Since(f.lastAttempt) < retryMinInterval {
		return
	}

	f.lastAttempt = time.Now()
	e, err := m.buildEntry(ctx, f.cfg)
	if err != nil {
		f.lastErr = err
		slog.Warn("oidc: on-demand re-discovery failed", "id", id, "error", err)
		return
	}
	m.providers[id] = e
	delete(m.failed, id)
	slog.Info("oidc: provider recovered via on-demand re-discovery", "id", id)
}

// Status returns the runtime status of a configured provider. For providers
// that aren't configured at all, returns nil.
func (m *Manager) Status(id string) *Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.providers[id]; ok {
		return &Status{State: "ok"}
	}
	if f, ok := m.failed[id]; ok {
		s := &Status{State: "failed", LastAttempt: f.lastAttempt}
		if f.lastErr != nil {
			s.LastError = f.lastErr.Error()
		}
		return s
	}
	return nil
}

// Get returns the entry for a provider ID, or nil if not found.
func (m *Manager) Get(id string) *entry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.providers[id]
}

// List returns all configured provider configs (for the login page). Includes
// only providers that successfully loaded — failed providers are intentionally
// hidden from the login page since clicking their button would error out.
func (m *Manager) List() []ProviderConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ProviderConfig, 0, len(m.providers))
	for _, e := range m.providers {
		out = append(out, e.cfg)
	}
	return out
}

// AuthURL returns the Authorization Code + PKCE redirect URL for the provider.
// state and codeVerifier are caller-generated; nonce is embedded in the URL.
// redirectBase is the public-facing scheme://host prefix that Bindery is
// reachable at — the OAuth2 redirect_uri is constructed by appending the
// per-provider callback path. If the provider is in the failed map, AuthURL
// first triggers an on-demand re-discovery attempt (rate-limited) before
// checking again.
func (m *Manager) AuthURL(ctx context.Context, redirectBase, id, state, nonce, codeVerifier string) (string, error) {
	m.EnsureLoaded(ctx, id)
	e := m.Get(id)
	if e == nil {
		return "", fmt.Errorf("unknown oidc provider %q", id)
	}
	oc := e.oauth2Config(redirectBase)
	challenge := pkceChallenge(codeVerifier)
	return oc.AuthCodeURL(state,
		oauth2.SetAuthURLParam("nonce", nonce),
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	), nil
}

// Exchange completes the code exchange and validates the ID token.
// Returns the verified Claims on success. redirectBase must match the value
// passed to AuthURL during the original authorize request — the IdP echoes
// the redirect_uri back during the token request and rejects mismatches.
func (m *Manager) Exchange(ctx context.Context, redirectBase, id, code, nonce, codeVerifier string) (*Claims, error) {
	m.EnsureLoaded(ctx, id)
	e := m.Get(id)
	if e == nil {
		return nil, fmt.Errorf("unknown oidc provider %q", id)
	}
	oc := e.oauth2Config(redirectBase)
	token, err := oc.Exchange(ctx, code,
		oauth2.SetAuthURLParam("code_verifier", codeVerifier),
	)
	if err != nil {
		return nil, fmt.Errorf("oidc token exchange: %w", err)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return nil, fmt.Errorf("oidc: no id_token in response")
	}
	idToken, err := e.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("oidc: id_token verification failed: %w", err)
	}
	if idToken.Nonce != nonce {
		return nil, fmt.Errorf("oidc: nonce mismatch")
	}
	var claims struct {
		Sub               string   `json:"sub"`
		Email             string   `json:"email"`
		PreferredUsername string   `json:"preferred_username"`
		Name              string   `json:"name"`
		Groups            []string `json:"groups"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("oidc: parse claims: %w", err)
	}
	return &Claims{
		Sub:               claims.Sub,
		Issuer:            idToken.Issuer,
		Email:             claims.Email,
		PreferredUsername: claims.PreferredUsername,
		Name:              claims.Name,
		Groups:            claims.Groups,
	}, nil
}

// --- PKCE helpers ------------------------------------------------------------

func pkceChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// NewVerifier generates a cryptographically random PKCE code verifier.
func NewVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// NewState generates a random state string for CSRF protection in the OAuth flow.
func NewState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// NewNonce generates a random nonce for replay protection.
func NewNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// --- State cookie value (state + nonce + codeVerifier packed as JSON) --------

// flowState is the encrypted-at-rest blob the browser carries between the
// /login and /callback hops. RedirectBase is included so the token-exchange
// in /callback uses the exact redirect_uri the IdP saw at /login — a
// requirement of the OAuth2 spec.
type flowState struct {
	State        string    `json:"s"`
	Nonce        string    `json:"n"`
	CodeVerifier string    `json:"cv"`
	RedirectBase string    `json:"rb,omitempty"`
	Expiry       time.Time `json:"exp"`
}

// FlowState exposes the decoded flow-cookie fields callers care about. The
// internal struct stays unexported so wire-format changes don't leak.
type FlowState struct {
	State        string
	Nonce        string
	CodeVerifier string
	RedirectBase string
}

func EncodeFlowState(state, nonce, codeVerifier, redirectBase string) (string, error) {
	fs := flowState{
		State:        state,
		Nonce:        nonce,
		CodeVerifier: codeVerifier,
		RedirectBase: redirectBase,
		Expiry:       time.Now().Add(10 * time.Minute),
	}
	return encodeFlowStateRaw(fs)
}

func configEqual(a, b ProviderConfig) bool {
	aj, _ := json.Marshal(a) // #nosec G117 -- persisted server-side only, never returned via API (see ProviderPublicConfig split)
	bj, _ := json.Marshal(b) // #nosec G117 -- persisted server-side only, never returned via API (see ProviderPublicConfig split)
	return bytes.Equal(aj, bj)
}

func encodeFlowStateRaw(fs flowState) (string, error) {
	b, err := json.Marshal(fs)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func DecodeFlowState(encoded string) (*FlowState, error) {
	b, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode flow state: %w", err)
	}
	var fs flowState
	if err := json.Unmarshal(b, &fs); err != nil {
		return nil, fmt.Errorf("parse flow state: %w", err)
	}
	if time.Now().After(fs.Expiry) {
		return nil, fmt.Errorf("flow state expired")
	}
	return &FlowState{
		State:        fs.State,
		Nonce:        fs.Nonce,
		CodeVerifier: fs.CodeVerifier,
		RedirectBase: fs.RedirectBase,
	}, nil
}
