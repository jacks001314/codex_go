package model

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"codex_go/auth"
	"codex_go/chatgptapi"
)

// Rust parity: codex-rs/model-provider/src/workspace_routing.rs.

// AccountRoutingHeader carries the discovered account routing override on
// Responses requests (Rust `ACCOUNT_ROUTING_HEADER`).
const AccountRoutingHeader = "x-openai-account-routing-override"

// Workspace routing discovery errors (Rust WorkspaceRoutingError). The messages
// match Rust verbatim because callers surface them as request failures.
var (
	ErrWorkspaceRoutingMissingAccountID     = errors.New("workspace routing requires a ChatGPT account id")
	ErrWorkspaceRoutingDiscoveryFailed      = errors.New("workspace routing discovery failed")
	ErrWorkspaceRoutingUnauthorized         = errors.New("workspace routing discovery unauthorized (401)")
	ErrWorkspaceRoutingMissingWorkspace     = errors.New("selected workspace missing from routing discovery")
	ErrWorkspaceRoutingDuplicateWorkspace   = errors.New("duplicate workspace in routing discovery")
	ErrWorkspaceRoutingMissingBackendOrigin = errors.New("workspace routing discovery missing backend origin")
	ErrWorkspaceRoutingInvalidOverride      = errors.New("workspace routing discovery has invalid account routing override")
	ErrWorkspaceRoutingBackendNotOrigin     = errors.New("workspace routing discovery must return an origin")
	ErrWorkspaceRoutingBackendConflict      = errors.New("required ChatGPT backend conflicts with workspace routing")
	ErrWorkspaceRoutingInvalidBackendURL    = errors.New("invalid workspace backend URL")
	ErrWorkspaceRoutingInvalidBackendOrigin = errors.New("workspace backend must use an HTTPS origin without credentials")
)

// DiscoverWorkspaceRouting fetches accounts/check and resolves the selected
// workspace's routing (Rust AccountRequestProcessor::read_account discovery).
func DiscoverWorkspaceRouting(ctx context.Context, client *chatgptapi.CloudClient, accountID string, requiredBaseURL string, effectiveBaseURL string) (*auth.WorkspaceRouting, error) {
	if client == nil {
		return nil, ErrWorkspaceRoutingDiscoveryFailed
	}
	response, err := client.GetAccountsCheck(ctx)
	if err != nil {
		var statusErr *chatgptapi.HTTPStatusError
		if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusUnauthorized {
			return nil, ErrWorkspaceRoutingUnauthorized
		}
		return nil, ErrWorkspaceRoutingDiscoveryFailed
	}
	var selected *chatgptapi.AccountsCheckEntry
	for i := range response.Accounts {
		if strings.TrimSpace(response.Accounts[i].ID) != accountID {
			continue
		}
		if selected != nil {
			return nil, ErrWorkspaceRoutingDuplicateWorkspace
		}
		candidate := response.Accounts[i]
		selected = &candidate
	}
	if selected == nil {
		return nil, ErrWorkspaceRoutingMissingWorkspace
	}
	return ResolveWorkspaceRouting(*selected, requiredBaseURL, effectiveBaseURL)
}

// ResolveWorkspaceRouting mirrors Rust resolve_routing: validate the discovered
// backend origin and override, reconcile them with the required backend, and
// fall back to the effective base URL when neither is set.
func ResolveWorkspaceRouting(entry chatgptapi.AccountsCheckEntry, requiredBaseURL string, effectiveBaseURL string) (*auth.WorkspaceRouting, error) {
	backend := ""
	if entry.WorkspaceBackendOrigin != nil {
		backend = strings.TrimSpace(*entry.WorkspaceBackendOrigin)
	}
	if backend == "" {
		return nil, ErrWorkspaceRoutingMissingBackendOrigin
	}
	override := ""
	if entry.AccountRoutingOverride != nil {
		override = strings.TrimSpace(*entry.AccountRoutingOverride)
	}
	if !auth.ValidAccountRoutingOverride(override) {
		return nil, ErrWorkspaceRoutingInvalidOverride
	}
	var required *url.URL
	if requiredBaseURL != "" {
		parsed, err := parseWorkspaceBackendURL(requiredBaseURL)
		if err != nil {
			return nil, err
		}
		required = parsed
	}
	var discovered *url.URL
	if backend != "NO_CONSTRAINT" {
		parsed, err := parseWorkspaceBackendURL(backend)
		if err != nil {
			return nil, err
		}
		if parsed.Path != "" && parsed.Path != "/" {
			return nil, ErrWorkspaceRoutingBackendNotOrigin
		}
		if parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, ErrWorkspaceRoutingBackendNotOrigin
		}
		discovered = parsed
	}
	var origin string
	switch {
	case required != nil && discovered != nil:
		if workspaceOrigin(required) != workspaceOrigin(discovered) {
			return nil, ErrWorkspaceRoutingBackendConflict
		}
		origin = workspaceOrigin(required)
	case required != nil:
		origin = workspaceOrigin(required)
	case discovered != nil:
		origin = workspaceOrigin(discovered)
	default:
		effective, err := parseWorkspaceBackendURL(effectiveBaseURL)
		if err != nil {
			return nil, err
		}
		origin = workspaceOrigin(effective)
	}
	return &auth.WorkspaceRouting{
		ChatGPTAccountID:       entry.ID,
		BackendOrigin:          origin,
		AccountRoutingOverride: auth.AccountRoutingOverride(override),
	}, nil
}

// parseWorkspaceBackendURL mirrors Rust parse_backend_url: an absolute HTTPS
// origin without credentials and without surrounding whitespace.
func parseWorkspaceBackendURL(value string) (*url.URL, error) {
	if strings.TrimSpace(value) != value || value == "" {
		return nil, ErrWorkspaceRoutingInvalidBackendOrigin
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return nil, ErrWorkspaceRoutingInvalidBackendURL
	}
	if parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return nil, ErrWorkspaceRoutingInvalidBackendOrigin
	}
	return parsed, nil
}

// workspaceOrigin serializes an origin the way Rust's Url::origin does.
func workspaceOrigin(value *url.URL) string {
	if value == nil {
		return ""
	}
	return value.Scheme + "://" + value.Host
}

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
