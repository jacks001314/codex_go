package mcp

import (
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
)

// McpRequestAuth supplies credentials for host-local MCP HTTP requests. The
// owner key identifies the credential source without exposing credential
// values, and it must stay stable across refreshes (Rust codex-mcp auth.rs
// McpRequestAuth, #63532).
//
// AddAuthHeaders must apply the current headers and report the owner from the
// same identity snapshot, so the caller can reject a request that no longer
// belongs to the session that opened the connection.
type McpRequestAuth interface {
	OwnerKey() (string, error)
	AddAuthHeaders(headers http.Header) (string, error)
}

// McpRequestAuthProvider identifies an in-process credential source. Two
// connections share credential state only when they hold the same provider.
type McpRequestAuthProvider struct {
	auth McpRequestAuth
}

// NewMcpRequestAuthProvider wraps a provider implementation.
func NewMcpRequestAuthProvider(auth McpRequestAuth) *McpRequestAuthProvider {
	if auth == nil {
		return nil
	}
	return &McpRequestAuthProvider{auth: auth}
}

// Identity reports a stable identity for the connection cache. It never exposes
// credential values.
func (p *McpRequestAuthProvider) Identity() string {
	if p == nil || p.auth == nil {
		return ""
	}
	return mcpRequestAuthIdentity(p.auth)
}

// Bind captures the owner lookup for one connection attempt. A failed lookup is
// preserved so startup reports it instead of falling back to another
// credential source.
func (p *McpRequestAuthProvider) Bind() *McpRequestAuthSession {
	if p == nil {
		return nil
	}
	owner, err := p.auth.OwnerKey()
	return &McpRequestAuthSession{provider: p, owner: strings.TrimSpace(owner), ownerErr: err}
}

// McpRequestAuthSession is one connection's bound credential source (Rust
// McpRequestAuthSession). Every request re-reads the provider, and a request
// whose owner differs from the bound owner is refused so a replacement
// reviewer or account cannot send on the previous owner's connection.
type McpRequestAuthSession struct {
	provider *McpRequestAuthProvider
	owner    string
	ownerErr error
}

// Owner returns the owner bound at connection time.
func (s *McpRequestAuthSession) Owner() (string, error) {
	if s == nil || s.provider == nil {
		return "", errors.New("MCP request auth provider is unavailable")
	}
	if s.ownerErr != nil {
		return "", s.ownerErr
	}
	return s.owner, nil
}

// NeedsOwnerRefresh reports whether the provider's active owner changed (or
// became available/unavailable) since this connection bound it. The caller
// rebuilds the connection so requests use the new owner's credentials.
func (s *McpRequestAuthSession) NeedsOwnerRefresh() bool {
	if s == nil || s.provider == nil {
		return false
	}
	owner, err := s.provider.auth.OwnerKey()
	current := strings.TrimSpace(owner)
	if (err == nil) != (s.ownerErr == nil) {
		return true
	}
	if err != nil {
		return false
	}
	return current != s.owner
}

// AddAuthHeaders applies fresh headers and rejects an owner change.
func (s *McpRequestAuthSession) AddAuthHeaders(headers http.Header) error {
	if s == nil || s.provider == nil || s.provider.auth == nil {
		return errors.New("MCP request auth provider is unavailable")
	}
	sessionOwner, err := s.Owner()
	if err != nil {
		return err
	}
	requestOwner, err := s.provider.auth.AddAuthHeaders(headers)
	if err != nil {
		return err
	}
	if strings.TrimSpace(requestOwner) != sessionOwner {
		return errors.New("MCP request auth owner changed")
	}
	return nil
}

// Scope is the connection-cache identity of this attempt: the provider plus the
// owner bound when it started. A credential refresh keeps the scope; an owner
// change moves it and forces a new connection.
func (s *McpRequestAuthSession) Scope() string {
	if s == nil || s.provider == nil {
		return ""
	}
	owner, err := s.Owner()
	if err != nil {
		return s.provider.Identity() + "|ownerError=" + err.Error()
	}
	return s.provider.Identity() + "|owner=" + owner
}

// CurrentScope is the live scope; it differs from Scope after an owner change,
// which is what rebuilds the cached HTTP connection.
func (s *McpRequestAuthSession) CurrentScope() string {
	if s == nil || s.provider == nil {
		return ""
	}
	owner, err := s.provider.auth.OwnerKey()
	if err != nil {
		return s.provider.Identity() + "|ownerError=" + err.Error()
	}
	return s.provider.Identity() + "|owner=" + strings.TrimSpace(owner)
}

func mcpRequestAuthIdentity(auth McpRequestAuth) string {
	value := reflect.ValueOf(auth)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Map, reflect.Pointer, reflect.Slice, reflect.UnsafePointer:
		if value.IsNil() {
			return ""
		}
		return fmt.Sprintf("%T:%x", auth, value.Pointer())
	default:
		return fmt.Sprintf("%T", auth)
	}
}

// SetMcpRequestAuthProvider binds a host-local provider to an HTTP MCP server.
// Rust #63532 only allows it for host-local Streamable HTTP servers with no
// other credential configuration, and the connection must not follow redirects
// because the provider's headers stay bound to the original origin.
func SetMcpRequestAuthProvider(config *ServerConfig, provider *McpRequestAuthProvider) (*ServerConfig, error) {
	if config == nil {
		return nil, errors.New("MCP request auth requires a server config")
	}
	if provider == nil {
		return nil, errors.New("MCP request auth requires a provider")
	}
	if !config.IsLocalEnvironment() || strings.TrimSpace(config.URL) == "" {
		return nil, errors.New("MCP request auth is only supported for host-local HTTP servers")
	}
	if err := mcpRequestAuthConflictingConfig(config); err != nil {
		return nil, err
	}
	session := provider.Bind()
	cloned := cloneServerConfig(config)
	cloned.RequestAuthProvider = provider
	cloned.RequestAuthSession = session
	cloned.DisableRedirects = true
	return &cloned, nil
}

// mcpRequestAuthConflictingConfig rejects another credential source on a
// provider-backed server (Rust #63532 "reject conflicting credential
// configuration").
func mcpRequestAuthConflictingConfig(config *ServerConfig) error {
	if config == nil {
		return nil
	}
	var configured []string
	add := func(name string, present bool) {
		if present {
			configured = append(configured, name)
		}
	}
	add("bearer_token_env_var", strings.TrimSpace(config.BearerTokenEnvVar) != "")
	add("http_headers", len(config.HTTPHeaders) > 0)
	add("env_http_headers", len(config.EnvHTTPHeaders) > 0)
	add("http_headers_helper", strings.TrimSpace(config.HTTPHeadersHelper) != "")
	add("oauth_client_id", strings.TrimSpace(config.OAuthClientID) != "")
	add("oauth_client_secret", strings.TrimSpace(config.OAuthClientSecret) != "")
	add("auth", strings.TrimSpace(config.Auth) != "")
	add("ApplyHTTPRequest", config.ApplyHTTPRequest != nil)
	if len(configured) == 0 {
		return nil
	}
	return fmt.Errorf("MCP request auth provider cannot be combined with %s", strings.Join(configured, ", "))
}

// mcpRequestAuthTransport applies the bound session's fresh headers to every
// HTTP request, including the session-delete call. Keeping the transport alive
// is what lets a credential refresh reuse the connection.
type mcpRequestAuthTransport struct {
	base    http.RoundTripper
	session *McpRequestAuthSession
}

func (t *mcpRequestAuthTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if t == nil || t.session == nil || request == nil {
		return nil, errors.New("MCP request auth transport is unavailable")
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	clone := request.Clone(request.Context())
	if request.Header != nil {
		clone.Header = request.Header.Clone()
	}
	if clone.Header == nil {
		clone.Header = http.Header{}
	}
	if err := t.session.AddAuthHeaders(clone.Header); err != nil {
		return nil, err
	}
	return base.RoundTrip(clone)
}
