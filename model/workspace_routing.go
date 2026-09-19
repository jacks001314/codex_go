package model

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"codex_go/auth"
)

// Rust parity: codex-rs/model-provider/src/workspace_routing.rs.

// AccountRoutingHeader carries the discovered account routing override on
// Responses requests (Rust `ACCOUNT_ROUTING_HEADER`).
const AccountRoutingHeader = "x-openai-account-routing-override"

// ApplyWorkspaceRouting rewrites a provider to the discovered ChatGPT backend
// origin and attaches the account-routing header (Rust apply_workspace_routing).
// Only the origin changes: the provider keeps the path, query, and credentials
// it already carried. A NO_CONSTRAINT override removes any existing header.
func ApplyWorkspaceRouting(provider *APIProvider, routing *auth.WorkspaceRouting) error {
	if provider == nil || routing == nil {
		return nil
	}
	base, err := url.Parse(strings.TrimSpace(provider.BaseURL))
	if err != nil {
		return err
	}
	backend, err := url.Parse(strings.TrimSpace(routing.BackendOrigin))
	if err != nil {
		return errors.New("invalid workspace backend origin")
	}
	if !validWorkspaceBackendOrigin(backend) {
		return errors.New("invalid workspace backend origin")
	}
	header := ""
	switch routing.AccountRoutingOverride {
	case auth.AccountRoutingOverrideNoConstraint:
	case auth.AccountRoutingOverrideUS, auth.AccountRoutingOverrideUSCR:
		header = string(routing.AccountRoutingOverride)
	default:
		return errors.New("invalid workspace routing override")
	}
	base.Scheme = backend.Scheme
	base.Host = backend.Host
	provider.BaseURL = base.String()
	if provider.Headers == nil {
		provider.Headers = http.Header{}
	}
	provider.Headers.Del(AccountRoutingHeader)
	if header != "" {
		provider.Headers.Set(AccountRoutingHeader, header)
	}
	return nil
}

// validWorkspaceBackendOrigin mirrors Rust's origin validation: HTTPS, a host,
// no credentials, and no path beyond the implicit root, query, or fragment.
func validWorkspaceBackendOrigin(backend *url.URL) bool {
	if backend == nil || backend.Scheme != "https" || backend.Host == "" {
		return false
	}
	if backend.User != nil {
		if backend.User.Username() != "" {
			return false
		}
		if _, hasPassword := backend.User.Password(); hasPassword {
			return false
		}
	}
	if backend.Path != "" && backend.Path != "/" {
		return false
	}
	if backend.RawQuery != "" || backend.Fragment != "" {
		return false
	}
	return true
}
