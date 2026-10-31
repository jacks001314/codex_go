package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
	"codex_go/mcp"
	"codex_go/model"
	codexnetwork "codex_go/network"
)

var openMCPLoginBrowser = auth.OpenBrowser

const (
	mcpCLIOAuthDiscoveryTimeout = 2 * time.Second
	mcpCLIOAuthCallbackTimeout  = 5 * time.Minute
	mcpCLIOAuthCallbackMaxBytes = 64 * 1024
)

type mcpCLIConfig struct {
	Servers map[string]*mcpCLIServer
}

type mcpCLIServer struct {
	Name              string            `json:"name,omitempty"`
	Type              string            `json:"type,omitempty"`
	Command           string            `json:"command,omitempty"`
	Args              []string          `json:"args,omitempty"`
	Env               map[string]string `json:"env,omitempty"`
	EnvVars           []mcpCLIEnvVar    `json:"env_vars,omitempty"`
	CWD               string            `json:"cwd,omitempty"`
	URL               string            `json:"url,omitempty"`
	BearerTokenEnvVar string            `json:"bearer_token_env_var,omitempty"`
	HTTPHeaders       map[string]string `json:"http_headers,omitempty"`
	EnvHTTPHeaders    map[string]string `json:"env_http_headers,omitempty"`
	HTTPHeadersHelper string            `json:"-"`
	OAuthClientID     string            `json:"oauth_client_id,omitempty"`
	OAuthClientSecret string            `json:"-"`
	// OAuthCallbackURL is the registered callback persisted with an existing
	// OAuth client so its provider keeps the exact redirect it expects
	// (Rust #40691).
	OAuthCallbackURL string `json:"oauth_callback_url,omitempty"`
	OAuthResource    string `json:"oauth_resource,omitempty"`
	// OAuthAuthorizationServerIssuer is the EMA resource authorization server
	// issuer carried through the `[mcp_servers.X.oauth]` table (Rust #44832).
	OAuthAuthorizationServerIssuer string                    `json:"oauth_authorization_server_issuer,omitempty"`
	Auth                           string                    `json:"auth,omitempty"`
	EnvironmentID                  string                    `json:"environment_id,omitempty"`
	Enabled                        bool                      `json:"enabled"`
	DisabledReason                 string                    `json:"disabled_reason,omitempty"`
	Required                       bool                      `json:"required,omitempty"`
	StartupReadiness               string                    `json:"startup_readiness,omitempty"`
	SupportsParallelToolCalls      bool                      `json:"supports_parallel_tool_calls,omitempty"`
	ToolInputSchemaMaxBytes        *int                      `json:"tool_input_schema_max_bytes,omitempty"`
	StartupTimeoutSec              *float64                  `json:"startup_timeout_sec,omitempty"`
	ToolTimeoutSec                 *float64                  `json:"tool_timeout_sec,omitempty"`
	EnabledTools                   []string                  `json:"enabled_tools,omitempty"`
	DisabledTools                  []string                  `json:"disabled_tools,omitempty"`
	OmitToolsFrom                  []string                  `json:"omit_tools_from,omitempty"`
	Scopes                         []string                  `json:"scopes,omitempty"`
	DefaultToolsApprovalMode       string                    `json:"default_tools_approval_mode,omitempty"`
	Tools                          map[string]map[string]any `json:"tools,omitempty"`
}

type mcpCLIEnvVar struct {
	Name   string
	Source string
}

type mcpListJSONEntry struct {
	Name              string            `json:"name"`
	Enabled           bool              `json:"enabled"`
	DisabledReason    *string           `json:"disabled_reason"`
	Transport         any               `json:"transport"`
	StartupTimeoutSec *json.Number      `json:"startup_timeout_sec"`
	ToolTimeoutSec    *json.Number      `json:"tool_timeout_sec"`
	AuthStatus        string            `json:"auth_status"`
}

type mcpGetJSONEntry struct {
	Name              string       `json:"name"`
	Enabled           bool         `json:"enabled"`
	DisabledReason    *string      `json:"disabled_reason"`
	Transport         any          `json:"transport"`
	EnabledTools      any          `json:"enabled_tools"`
	DisabledTools     any          `json:"disabled_tools"`
	StartupTimeoutSec *json.Number `json:"startup_timeout_sec"`
	ToolTimeoutSec    *json.Number `json:"tool_timeout_sec"`
}

// mcpStdioTransportJSON spells a stdio transport in the order serde_json keeps
// for the 0.166 `mcp get/list --json` payloads (the crate enables
// preserve_order, so the object is not sorted). An empty args list stays `[]`.
type mcpStdioTransportJSON struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	EnvVars any               `json:"env_vars"`
	CWD     *string           `json:"cwd"`
}

// mcpHTTPTransportJSON spells a streamable HTTP transport the same way.
type mcpHTTPTransportJSON struct {
	Type              string            `json:"type"`
	URL               string            `json:"url"`
	BearerTokenEnvVar *string           `json:"bearer_token_env_var"`
	HTTPHeaders       map[string]string `json:"http_headers"`
	EnvHTTPHeaders    map[string]string `json:"env_http_headers"`
	HTTPHeadersHelper *string           `json:"http_headers_helper"`
}

func runMCP(ctx context.Context, opts *cli.MCPOptions, stdin io.Reader, stdout io.Writer) error {
	store := newMCPCLIStore(auth.DefaultCodexHome())
	switch opts.Action {
	case "list":
		return runMCPList(ctx, store, opts, stdout)
	case "get":
		return runMCPGet(ctx, store, opts, stdout)
	case "add":
		return runMCPAdd(ctx, store, opts, stdout)
	case "remove":
		return runMCPRemove(store, opts, stdout)
	case "login":
		return runMCPLogin(ctx, store, opts, stdin, stdout)
	case "logout":
		return runMCPLogout(ctx, store, opts, stdout)
	default:
		return fmt.Errorf("unknown mcp subcommand %s", opts.Action)
	}
}

func runMCPList(ctx context.Context, store *mcpCLIStore, opts *cli.MCPOptions, stdout io.Writer) error {
	cfg, err := store.LoadManaged(ctx, opts.ConfigOverrides)
	if err != nil {
		return err
	}
	names := sortedMCPServerNames(cfg.Servers)
	oauthStore := mcp.NewOAuthStore(store.codexHome)
	if opts.JSON {
		entries := make([]mcpListJSONEntry, 0, len(names))
		for _, name := range names {
			server := cfg.Servers[name]
			entries = append(entries, mcpListJSONEntry{
				Name:              name,
				Enabled:           server.Enabled,
				DisabledReason:    stringPtrIfNotEmptyLocal(server.DisabledReason),
				Transport:         mcpServerTransportJSON(server),
				StartupTimeoutSec: mcpSecondsJSON(server.StartupTimeoutSec),
				ToolTimeoutSec:    mcpSecondsJSON(server.ToolTimeoutSec),
				AuthStatus:        mcpAuthStatusJSON(ctx, oauthStore, name, server),
			})
		}
		encoder := json.NewEncoder(stdout)
		encoder.SetEscapeHTML(false)
		encoder.SetIndent("", "  ")
		return encoder.Encode(entries)
	}
	if len(names) == 0 {
		fmt.Fprintln(stdout, "No MCP servers configured yet. Try `codex mcp add my-tool -- my-command`.")
		return nil
	}
	stdioRows := make([][]string, 0, len(names))
	httpRows := make([][]string, 0, len(names))
	for _, name := range names {
		server := cfg.Servers[name]
		authStatus := mcpAuthStatusDisplay(mcpServerAuthStatus(ctx, oauthStore, name, server))
		if server.Type == "streamable_http" {
			httpRows = append(httpRows, []string{
				name,
				server.URL,
				dashIfEmpty(server.BearerTokenEnvVar),
				mcpServerStatusDisplay(server),
				authStatus,
			})
			continue
		}
		stdioRows = append(stdioRows, []string{
			name,
			server.Command,
			mcpServerArgsDisplay(server),
			mcpServerEnvDisplay(server),
			dashIfEmpty(server.CWD),
			mcpServerStatusDisplay(server),
			authStatus,
		})
	}
	if len(stdioRows) > 0 {
		printStringTable(stdout, []string{"Name", "Command", "Args", "Env", "Cwd", "Status", "Auth"}, stdioRows)
	}
	if len(stdioRows) > 0 && len(httpRows) > 0 {
		fmt.Fprintln(stdout)
	}
	if len(httpRows) > 0 {
		printStringTable(stdout, []string{"Name", "Url", "Bearer Token Env Var", "Status", "Auth"}, httpRows)
	}
	return nil
}

func runMCPGet(ctx context.Context, store *mcpCLIStore, opts *cli.MCPOptions, stdout io.Writer) error {
	cfg, err := store.LoadManaged(ctx, opts.ConfigOverrides)
	if err != nil {
		return err
	}
	server := cfg.Servers[opts.Name]
	if server == nil {
		return fmt.Errorf("No MCP server named '%s' found.", opts.Name)
	}
	if opts.JSON {
		entry := mcpGetJSONEntry{
			Name:              opts.Name,
			Enabled:           server.Enabled,
			DisabledReason:    stringPtrIfNotEmptyLocal(server.DisabledReason),
			Transport:         mcpServerTransportJSON(server),
			EnabledTools:      stringSliceJSONValue(server.EnabledTools),
			DisabledTools:     stringSliceJSONValue(server.DisabledTools),
			StartupTimeoutSec: mcpSecondsJSON(server.StartupTimeoutSec),
			ToolTimeoutSec:    mcpSecondsJSON(server.ToolTimeoutSec),
		}
		encoder := json.NewEncoder(stdout)
		encoder.SetEscapeHTML(false)
		encoder.SetIndent("", "  ")
		return encoder.Encode(&entry)
	}
	if !server.Enabled {
		if strings.TrimSpace(server.DisabledReason) != "" {
			fmt.Fprintf(stdout, "%s (disabled: %s)\n", opts.Name, server.DisabledReason)
		} else {
			fmt.Fprintf(stdout, "%s (disabled)\n", opts.Name)
		}
		return nil
	}
	fmt.Fprintln(stdout, opts.Name)
	fmt.Fprintf(stdout, "  enabled: %t\n", server.Enabled)
	if server.EnabledTools != nil {
		fmt.Fprintf(stdout, "  enabled_tools: %s\n", mcpServerToolListDisplay(server.EnabledTools))
	}
	if server.DisabledTools != nil {
		fmt.Fprintf(stdout, "  disabled_tools: %s\n", mcpServerToolListDisplay(server.DisabledTools))
	}
	if len(server.OmitToolsFrom) > 0 {
		fmt.Fprintf(stdout, "  omit_tools_from: %s\n", mcpServerToolListDisplay(server.OmitToolsFrom))
	}
	fmt.Fprintf(stdout, "  transport: %s\n", mcpServerTransportLabel(server))
	if server.Type == "streamable_http" {
		fmt.Fprintf(stdout, "  url: %s\n", server.URL)
		fmt.Fprintf(stdout, "  bearer_token_env_var: %s\n", dashIfEmpty(server.BearerTokenEnvVar))
		fmt.Fprintf(stdout, "  http_headers: %s\n", mcpServerHeaderDisplay(server.HTTPHeaders, true))
		fmt.Fprintf(stdout, "  env_http_headers: %s\n", mcpServerHeaderDisplay(server.EnvHTTPHeaders, false))
		helperDisplay := "-"
		if strings.TrimSpace(server.HTTPHeadersHelper) != "" {
			helperDisplay = "<redacted>"
		}
		fmt.Fprintf(stdout, "  http_headers_helper: %s\n", helperDisplay)
	} else {
		fmt.Fprintf(stdout, "  command: %s\n", server.Command)
		fmt.Fprintf(stdout, "  args: %s\n", mcpServerArgsDisplay(server))
		fmt.Fprintf(stdout, "  cwd: %s\n", dashIfEmpty(server.CWD))
		fmt.Fprintf(stdout, "  env: %s\n", mcpServerEnvDisplay(server))
	}
	if server.StartupTimeoutSec != nil {
		fmt.Fprintf(stdout, "  startup_timeout_sec: %s\n", floatDisplay(*server.StartupTimeoutSec))
	}
	if server.ToolTimeoutSec != nil {
		fmt.Fprintf(stdout, "  tool_timeout_sec: %s\n", floatDisplay(*server.ToolTimeoutSec))
	}
	if server.DefaultToolsApprovalMode != "" {
		fmt.Fprintf(stdout, "  default_tools_approval_mode: %s\n", server.DefaultToolsApprovalMode)
	}
	fmt.Fprintf(stdout, "  remove: codex mcp remove %s\n", opts.Name)
	return nil
}

func runMCPAdd(ctx context.Context, store *mcpCLIStore, opts *cli.MCPOptions, stdout io.Writer) error {
	if err := validateMCPServerName(opts.Name); err != nil {
		return err
	}
	// Rust validates the pair after clap: an empty secret is rejected outright,
	// and a present-but-empty client ID cannot carry a secret.
	if opts.OAuthClientSecretSet {
		if strings.TrimSpace(opts.OAuthClientSecret) == "" {
			return errors.New("--oauth-client-secret must not be empty")
		}
		if !opts.OAuthClientIDSet || strings.TrimSpace(opts.OAuthClientID) == "" {
			return errors.New("--oauth-client-secret requires a nonempty --oauth-client-id")
		}
	}
	server := &mcpCLIServer{
		Name:    opts.Name,
		Enabled: true,
	}
	if opts.URLSet || strings.TrimSpace(opts.URL) != "" {
		server.Type = "streamable_http"
		// Rust persists the raw --url value; an empty value still selects the
		// streamable-HTTP transport and writes `url = ""`.
		server.URL = opts.URL
		server.BearerTokenEnvVar = strings.TrimSpace(opts.BearerTokenEnvVar)
		server.OAuthClientID = strings.TrimSpace(opts.OAuthClientID)
		server.OAuthClientSecret = strings.TrimSpace(opts.OAuthClientSecret)
		server.OAuthResource = strings.TrimSpace(opts.OAuthResource)
	} else {
		server.Type = "stdio"
		server.Command = opts.Command[0]
		server.Args = append([]string(nil), opts.Command[1:]...)
		server.Env = cloneStringMap(opts.Env)
	}
	// Rust discovers before saving so the persisted callback matches what the
	// provider is registered for, then persists the callback URL whenever a
	// client ID was supplied (even an empty one).
	support := mcpOAuthLoginSupport(ctx, server, store.httpClient)
	if opts.OAuthClientIDSet {
		callbackMode := mcp.MCPOAuthCallbackSpecific
		if support.Kind == mcpOAuthLoginSupported && support.Discovery != nil && support.Discovery.CallbackMode != "" {
			callbackMode = support.Discovery.CallbackMode
		}
		callbackURL, err := mcp.ResolveMCPOAuthCallbackURL(server.URL, nil, callbackMode)
		if err != nil {
			return err
		}
		server.OAuthCallbackURL = callbackURL
	}
	if err := store.Upsert(opts.ConfigOverrides, server); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Added global MCP server '%s'.\n", opts.Name)
	if server.OAuthCallbackURL != "" {
		fmt.Fprintf(stdout, "OAuth callback URL: %s\n", server.OAuthCallbackURL)
	}
	switch support.Kind {
	case mcpOAuthLoginSupported:
		fmt.Fprintln(stdout, "Detected OAuth support. Starting OAuth flow…")
		resolvedScopes := resolveMCPOAuthScopes(nil, false, nil, false, support.Discovery.ScopesSupported)
		if err := performMCPCLIOAuthLoginRetryWithoutScopes(ctx, store, opts.Name, server, support.Discovery, resolvedScopes, mcp.MCPServerOauthClientRegistration(opts.OAuthClientRegistration), nil, stdout); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "Successfully logged in.")
	case mcpOAuthLoginUnknown:
		fmt.Fprintf(stdout, "MCP server may or may not require login. Run `codex mcp login %s` to login.\n", opts.Name)
	}
	return nil
}

func runMCPRemove(store *mcpCLIStore, opts *cli.MCPOptions, stdout io.Writer) error {
	if err := validateMCPServerName(opts.Name); err != nil {
		return err
	}
	removed, err := store.Remove(opts.ConfigOverrides, opts.Name)
	if err != nil {
		return err
	}
	if removed {
		fmt.Fprintf(stdout, "Removed global MCP server '%s'.\n", opts.Name)
		return nil
	}
	fmt.Fprintf(stdout, "No MCP server named '%s' found.\n", opts.Name)
	return nil
}

func runMCPLogin(ctx context.Context, store *mcpCLIStore, opts *cli.MCPOptions, stdin io.Reader, stdout io.Writer) error {
	cfg, err := store.LoadManaged(ctx, opts.ConfigOverrides)
	if err != nil {
		return err
	}
	server := cfg.Servers[opts.Name]
	if server == nil {
		return fmt.Errorf("No MCP server named '%s' found.", opts.Name)
	}
	if server.Type != "streamable_http" {
		return fmt.Errorf("OAuth login is only supported for streamable HTTP servers.")
	}
	runtimeConfig := mcpServerRuntimeConfig(opts.Name, server, store.codexHome)
	if runtimeConfig.EffectiveAuth() == mcp.ServerAuthEMAAuth {
		// Enterprise MCP authorization is managed by Codex account sign-in
		// (Rust #44832); ordinary MCP OAuth login is blocked for EMA.
		return fmt.Errorf("Enterprise MCP authorization is managed by Codex account sign-in. Open Codex to sign in.")
	}
	if runtimeConfig.EffectiveAuth() == mcp.ServerAuthChatGPT && !runtimeConfig.IsLocalEnvironment() {
		return fmt.Errorf("OAuth login is not supported for executor-owned ChatGPT MCP servers")
	}
	explicitScopes := append([]string(nil), opts.Scopes...)
	explicitScopesSet := len(explicitScopes) > 0
	configuredScopesSet := server.Scopes != nil
	var support mcpOAuthLoginSupportResult
	var discoveredScopes []string
	if !explicitScopesSet && !configuredScopesSet {
		support = mcpOAuthLoginSupport(ctx, server, store.httpClient)
		if support.Kind == mcpOAuthLoginSupported {
			discoveredScopes = support.Discovery.ScopesSupported
		}
	}
	resolvedScopes := resolveMCPOAuthScopes(explicitScopes, explicitScopesSet, server.Scopes, configuredScopesSet, discoveredScopes)
	if support.Kind == mcpOAuthLoginSupported {
		if err := performMCPCLIOAuthLoginRetryWithoutScopes(ctx, store, opts.Name, server, support.Discovery, resolvedScopes, mcp.MCPServerOauthClientRegistration(opts.OAuthClientRegistration), loginCallbackInput(opts, stdin), stdout); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Successfully logged in to MCP server '%s'.\n", opts.Name)
		return nil
	}
	service := mcp.NewMCPService(&mcp.RuntimeConfig{Servers: map[string]mcp.ServerRegistration{
		opts.Name: {Config: mcpServerRuntimeConfig(opts.Name, server, store.codexHome)},
	}, CodexHome: store.codexHome, HTTPClient: store.httpClient})
	loginParams := &mcp.MCPServerOauthLoginParams{Name: opts.Name, Scopes: resolvedScopes.Scopes}
	if opts.OAuthClientRegistration != "" {
		registration := mcp.MCPServerOauthClientRegistration(opts.OAuthClientRegistration)
		loginParams.ClientRegistration = &registration
	}
	response, err := service.OauthLogin(loginParams)
	if err != nil {
		return err
	}
	if err := openMCPLoginBrowser(response.AuthorizationURL); err != nil {
		fmt.Fprintf(stdout, "Open this URL to authenticate MCP server '%s': %s\n", opts.Name, response.AuthorizationURL)
		return nil
	}
	fmt.Fprintf(stdout, "Opening browser to authenticate MCP server '%s'.\nIf it did not open, visit:\n%s\n", opts.Name, response.AuthorizationURL)
	return nil
}

type mcpOAuthLoginSupportKind int

const (
	mcpOAuthLoginUnsupported mcpOAuthLoginSupportKind = iota
	mcpOAuthLoginSupported
	mcpOAuthLoginUnknown
)

type mcpOAuthLoginSupportResult struct {
	Kind      mcpOAuthLoginSupportKind
	Discovery *mcp.StreamableHTTPOAuthDiscovery
	Err       error
}

type mcpOAuthScopesSource int

const (
	mcpOAuthScopesExplicit mcpOAuthScopesSource = iota
	mcpOAuthScopesConfigured
	mcpOAuthScopesDiscovered
	mcpOAuthScopesEmpty
)

type mcpResolvedOAuthScopes struct {
	Scopes []string
	Source mcpOAuthScopesSource
}

func mcpOAuthLoginSupport(ctx context.Context, server *mcpCLIServer, client *http.Client) mcpOAuthLoginSupportResult {
	if server == nil || server.Type != "streamable_http" || strings.TrimSpace(server.BearerTokenEnvVar) != "" {
		return mcpOAuthLoginSupportResult{Kind: mcpOAuthLoginUnsupported}
	}
	timeoutCtx, cancel := context.WithTimeout(contextOrBackground(ctx), mcpCLIOAuthDiscoveryTimeout)
	defer cancel()
	client = mcpCLIHTTPClientWithTimeout(client, mcpCLIOAuthDiscoveryTimeout)
	discovery, err := mcp.DiscoverStreamableHTTPOAuth(timeoutCtx, server.URL, client)
	if err != nil {
		return mcpOAuthLoginSupportResult{Kind: mcpOAuthLoginUnknown, Err: err}
	}
	if discovery == nil || strings.TrimSpace(discovery.AuthorizationEndpoint) == "" || strings.TrimSpace(discovery.TokenEndpoint) == "" {
		return mcpOAuthLoginSupportResult{Kind: mcpOAuthLoginUnsupported}
	}
	return mcpOAuthLoginSupportResult{Kind: mcpOAuthLoginSupported, Discovery: discovery}
}

func resolveMCPOAuthScopes(explicit []string, explicitSet bool, configured []string, configuredSet bool, discovered []string) mcpResolvedOAuthScopes {
	if explicitSet {
		return mcpResolvedOAuthScopes{Scopes: append([]string(nil), explicit...), Source: mcpOAuthScopesExplicit}
	}
	if configuredSet {
		return mcpResolvedOAuthScopes{Scopes: append([]string(nil), configured...), Source: mcpOAuthScopesConfigured}
	}
	if len(discovered) > 0 {
		return mcpResolvedOAuthScopes{Scopes: append([]string(nil), discovered...), Source: mcpOAuthScopesDiscovered}
	}
	return mcpResolvedOAuthScopes{Source: mcpOAuthScopesEmpty}
}

// loginCallbackInput returns the reader for a `--no-browser` login, or nil to
// use the browser flow (Rust #44629). Manual input is preserved across the
// discovered-scope retry because the same reader is passed to both attempts.
func loginCallbackInput(opts *cli.MCPOptions, stdin io.Reader) io.Reader {
	if opts == nil || !opts.NoBrowser {
		return nil
	}
	return stdin
}

func performMCPCLIOAuthLoginRetryWithoutScopes(ctx context.Context, store *mcpCLIStore, name string, server *mcpCLIServer, discovery *mcp.StreamableHTTPOAuthDiscovery, resolvedScopes mcpResolvedOAuthScopes, clientRegistration mcp.MCPServerOauthClientRegistration, callbackInput io.Reader, stdout io.Writer) error {
	err := performMCPCLIOAuthLogin(ctx, store, name, server, discovery, resolvedScopes, clientRegistration, callbackInput, stdout)
	if err == nil {
		return nil
	}
	if !shouldRetryMCPLoginWithoutScopes(resolvedScopes, err) {
		return err
	}
	fmt.Fprintln(stdout, "OAuth provider rejected discovered scopes. Retrying without scopes…")
	return performMCPCLIOAuthLogin(ctx, store, name, server, discovery, mcpResolvedOAuthScopes{Source: mcpOAuthScopesEmpty}, clientRegistration, callbackInput, stdout)
}

func performMCPCLIOAuthLogin(ctx context.Context, store *mcpCLIStore, name string, server *mcpCLIServer, discovery *mcp.StreamableHTTPOAuthDiscovery, resolvedScopes mcpResolvedOAuthScopes, clientRegistration mcp.MCPServerOauthClientRegistration, callbackInput io.Reader, stdout io.Writer) error {
	if store == nil || server == nil || discovery == nil {
		return fmt.Errorf("OAuth login requires a discovered streamable HTTP server")
	}
	httpClient := mcpCLIHTTPClientWithTimeout(store.httpClient, mcpCLIOAuthDiscoveryTimeout)
	startCtx, cancelStart := context.WithTimeout(contextOrBackground(ctx), mcpCLIOAuthDiscoveryTimeout)
	runtimeConfig := mcpServerRuntimeConfig(name, server, store.codexHome)
	// A registered client keeps the exact callback its provider expects; an
	// existing client ID without a stored callback resolves the mix-up defense
	// from the discovered metadata (Rust #40691).
	redirectURL := strings.TrimSpace(server.OAuthCallbackURL)
	if redirectURL == "" && strings.TrimSpace(server.OAuthClientID) != "" {
		if resolved, resolveErr := mcp.ResolveMCPOAuthCallbackURL(server.URL, nil, discovery.CallbackMode); resolveErr == nil {
			redirectURL = resolved
		}
	}
	login, err := mcp.StartOAuthLoginServer(startCtx, &mcp.OAuthLoginServerOptions{
		ServerName:                 runtimeConfig.OAuthCredentialName(name),
		ServerURL:                  server.URL,
		ClientID:                   strings.TrimSpace(server.OAuthClientID),
		ClientSecret:               strings.TrimSpace(server.OAuthClientSecret),
		OmitClientSecretFromRecord: strings.TrimSpace(server.OAuthClientSecret) != "",
		Issuer:                     strings.TrimSpace(discovery.Issuer),
		RegistrationEndpoint:       discovery.RegistrationEndpoint,
		ClientName:                 "Codex",
		AuthorizationEndpoint:      discovery.AuthorizationEndpoint,
		TokenEndpoint:              discovery.TokenEndpoint,
		Resource:                   firstNonEmptyLocal(server.OAuthResource, discovery.Resource),
		Scopes:                     append([]string(nil), resolvedScopes.Scopes...),
		CallbackMode:               discovery.CallbackMode,
		ClientRegistration:         clientRegistration,
		RedirectURL:                redirectURL,
		Store:                      mcp.NewOAuthStore(store.codexHome),
		HTTPClient:                 httpClient,
	})
	cancelStart()
	if err != nil {
		return err
	}
	defer func() {
		_ = login.Cancel(context.Background())
	}()
	fmt.Fprintf(stdout, "Authorize `%s` by opening this URL in your browser:\n%s\n\n", name, login.AuthorizationURL)
	if callbackInput != nil {
		// Rust #44629: HTTP callbacks remain supported while waiting for a
		// pasted redirect URL.
		fmt.Fprintln(stdout, "Or paste the full redirect URL here:")
		go completeMCPCLIOAuthLoginFromInput(ctx, login, callbackInput, stdout)
	} else if err := openMCPLoginBrowser(login.AuthorizationURL); err != nil {
		fmt.Fprintln(stdout, "(Browser launch failed; please copy the URL above manually.)")
	}
	waitCtx, cancelWait := context.WithTimeout(contextOrBackground(ctx), mcpCLIOAuthCallbackTimeout)
	defer cancelWait()
	select {
	case result := <-login.Done():
		if result != nil && result.Error != nil {
			return result.Error
		}
		return nil
	case <-waitCtx.Done():
		_ = login.Cancel(context.Background())
		if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
			return errors.New("timed out waiting for OAuth callback")
		}
		return waitCtx.Err()
	}
}

// completeMCPCLIOAuthLoginFromInput reads one pasted redirect URL and completes
// the login, still validating the URL and state before exchanging the code
// (Rust #44629).
func completeMCPCLIOAuthLoginFromInput(ctx context.Context, login *mcp.OAuthLoginServer, reader io.Reader, stdout io.Writer) {
	line, err := readMCPOAuthCallbackInput(reader)
	if err != nil {
		fmt.Fprintf(stdout, "Unable to read the OAuth callback URL: %v\n", err)
		_ = login.Cancel(context.Background())
		return
	}
	if _, err := login.CompleteWithCallbackURL(contextOrBackground(ctx), line); err != nil {
		fmt.Fprintf(stdout, "OAuth login failed: %v\n", err)
	}
}

func readMCPOAuthCallbackInput(reader io.Reader) (string, error) {
	if reader == nil {
		return "", errors.New("OAuth callback input is unavailable")
	}
	line, err := bufio.NewReader(io.LimitReader(reader, mcpCLIOAuthCallbackMaxBytes+1)).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	if len(line) > mcpCLIOAuthCallbackMaxBytes {
		return "", fmt.Errorf("OAuth callback URL exceeds %d KiB", mcpCLIOAuthCallbackMaxBytes/1024)
	}
	return strings.TrimSpace(line), nil
}

func shouldRetryMCPLoginWithoutScopes(resolvedScopes mcpResolvedOAuthScopes, err error) bool {
	if resolvedScopes.Source != mcpOAuthScopesDiscovered {
		return false
	}
	var providerError *mcp.OAuthProviderError
	return errors.As(err, &providerError)
}

func runMCPLogout(ctx context.Context, store *mcpCLIStore, opts *cli.MCPOptions, stdout io.Writer) error {
	cfg, err := store.LoadManaged(ctx, opts.ConfigOverrides)
	if err != nil {
		return err
	}
	server := cfg.Servers[opts.Name]
	if server == nil {
		return fmt.Errorf("No MCP server named '%s' found in configuration.", opts.Name)
	}
	if server.Type != "streamable_http" {
		return fmt.Errorf("OAuth logout is only supported for streamable_http transports.")
	}
	runtimeConfig := mcpServerRuntimeConfig(opts.Name, server, store.codexHome)
	credentialName := runtimeConfig.OAuthCredentialName(opts.Name)
	removed, err := mcp.NewOAuthStore(store.codexHome).Delete(credentialName, server.URL)
	if err != nil {
		return fmt.Errorf("failed to delete OAuth credentials: %w", err)
	}
	if removed {
		fmt.Fprintf(stdout, "Removed OAuth credentials for '%s'.\n", opts.Name)
		return nil
	}
	fmt.Fprintf(stdout, "No OAuth credentials stored for '%s'.\n", opts.Name)
	return nil
}

func validateMCPServerName(name string) error {
	const allowed = "letters, numbers, '-', '_', ':', '@', '/', '.'"
	if name == "" {
		return fmt.Errorf("invalid server name '%s' (use %s)", name, allowed)
	}
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			r == '-' || r == '_' || r == ':' || r == '@' || r == '/' || r == '.' {
			continue
		}
		return fmt.Errorf("invalid server name '%s' (use %s)", name, allowed)
	}
	return nil
}

func sortedMCPServerNames(servers map[string]*mcpCLIServer) []string {
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func mcpServerTransportJSON(server *mcpCLIServer) any {
	if server.Type == "streamable_http" {
		var helper *string
		if strings.TrimSpace(server.HTTPHeadersHelper) != "" {
			redacted := "<redacted>"
			helper = &redacted
		}
		return mcpHTTPTransportJSON{
			Type:              "streamable_http",
			URL:               server.URL,
			BearerTokenEnvVar: stringPtrIfNotEmptyLocal(server.BearerTokenEnvVar),
			HTTPHeaders:       cloneStringMap(server.HTTPHeaders),
			EnvHTTPHeaders:    cloneStringMap(server.EnvHTTPHeaders),
			HTTPHeadersHelper: helper,
		}
	}
	args := make([]string, len(server.Args))
	copy(args, server.Args)
	return mcpStdioTransportJSON{
		Type:    "stdio",
		Command: server.Command,
		Args:    args,
		Env:     cloneStringMap(server.Env),
		EnvVars: mcpServerEnvVarsJSONValue(server.EnvVars),
		CWD:     stringPtrIfNotEmptyLocal(server.CWD),
	}
}

// mcpSecondsJSON renders a timeout the way serde_json prints f64 seconds: an
// integral value keeps its fractional `.0`.
func mcpSecondsJSON(value *float64) *json.Number {
	if value == nil {
		return nil
	}
	text := strconv.FormatFloat(*value, 'f', -1, 64)
	if !strings.ContainsAny(text, ".eE") {
		text += ".0"
	}
	number := json.Number(text)
	return &number
}

func mcpServerRuntimeConfig(name string, server *mcpCLIServer, codexHome string) mcp.ServerConfig {
	if server == nil {
		return mcp.ServerConfig{}
	}
	return mcp.ServerConfig{
		Command:                        server.Command,
		Args:                           append([]string(nil), server.Args...),
		Env:                            cloneStringMap(server.Env),
		URL:                            server.URL,
		BearerTokenEnvVar:              server.BearerTokenEnvVar,
		HTTPHeaders:                    cloneStringMap(server.HTTPHeaders),
		EnvHTTPHeaders:                 cloneStringMap(server.EnvHTTPHeaders),
		HTTPHeadersHelper:              server.HTTPHeadersHelper,
		OAuthClientID:                  server.OAuthClientID,
		OAuthClientSecret:              server.OAuthClientSecret,
		OAuthResource:                  server.OAuthResource,
		OAuthAuthorizationServerIssuer: server.OAuthAuthorizationServerIssuer,
		OAuthServerName:                name,
		ToolInputSchemaMaxBytes:        server.ToolInputSchemaMaxBytes,
		CodexHome:                      codexHome,
		Enabled:                        server.Enabled,
		Required:                       server.Required,
		StartupReadiness:               mcp.MCPStartupReadiness(server.StartupReadiness),
		EnvironmentID:                  server.EnvironmentID,
		Auth:                           server.Auth,
	}
}

func mcpServerAuthStatus(ctx context.Context, store *mcp.OAuthStore, name string, server *mcpCLIServer) mcp.MCPAuthStatus {
	if server == nil || !server.Enabled || server.Type != "streamable_http" || strings.TrimSpace(server.URL) == "" {
		return mcp.MCPAuthUnsupported
	}
	config := mcpServerRuntimeConfig(name, server, "")
	// compute_auth_status mirrors rmcp-client's auth_status_before_discovery:
	// a configured bearer token or Authorization header decides before any
	// store or discovery work, and stored credentials decide the rest.
	if strings.TrimSpace(config.BearerTokenEnvVar) != "" {
		return mcp.MCPAuthBearerToken
	}
	if mcpServerHasAuthorizationHeader(server) {
		return mcp.MCPAuthBearerToken
	}
	tokens, err := store.Load(config.OAuthCredentialName(name), config.URL)
	if err != nil {
		// compute_auth_statuses reports Unknown when the probe itself fails, for
		// example when the credential store cannot be read.
		return mcp.MCPAuthUnknown
	}
	if tokens != nil {
		if tokens.Usable(time.Now()) {
			return mcp.MCPAuthOAuth
		}
		return mcp.MCPAuthNotLoggedIn
	}
	if strings.TrimSpace(server.HTTPHeadersHelper) != "" {
		// Status inspection never runs an arbitrary local helper, so a server
		// without stored credentials stays unknown until startup or login.
		return mcp.MCPAuthUnknown
	}
	return mcpDiscoverStreamableHTTPAuthStatus(ctx, server)
}

// mcpDiscoverStreamableHTTPAuthStatus reports what OAuth metadata discovery says
// about a server that carries no configured credentials.
func mcpDiscoverStreamableHTTPAuthStatus(ctx context.Context, server *mcpCLIServer) mcp.MCPAuthStatus {
	client := &http.Client{Timeout: 5 * time.Second}
	discovery, err := mcp.DiscoverStreamableHTTPOAuth(ctx, server.URL, client)
	if err != nil {
		return mcp.MCPAuthUnknown
	}
	if discovery != nil {
		return mcp.MCPAuthNotLoggedIn
	}
	return mcp.MCPAuthUnsupported
}

// mcpServerHasAuthorizationHeader reports whether the server carries an explicit
// Authorization header, which makes the transport bearer-token authenticated
// before any discovery (auth_status_before_discovery).
func mcpServerHasAuthorizationHeader(server *mcpCLIServer) bool {
	for name, value := range server.HTTPHeaders {
		if strings.EqualFold(strings.TrimSpace(name), "Authorization") && strings.TrimSpace(value) != "" {
			return true
		}
	}
	for name, envVar := range server.EnvHTTPHeaders {
		if strings.EqualFold(strings.TrimSpace(name), "Authorization") && strings.TrimSpace(os.Getenv(strings.TrimSpace(envVar))) != "" {
			return true
		}
	}
	return false
}

func mcpServerTransportLabel(server *mcpCLIServer) string {
	if server.Type != "" {
		return server.Type
	}
	return "stdio"
}

func mcpServerCommandDisplay(server *mcpCLIServer) string {
	if server.Type == "streamable_http" {
		return server.URL
	}
	return server.Command
}

func mcpServerArgsDisplay(server *mcpCLIServer) string {
	if len(server.Args) == 0 {
		return "-"
	}
	return strings.Join(server.Args, " ")
}

func mcpServerStatusDisplay(server *mcpCLIServer) string {
	if server.Enabled {
		return "enabled"
	}
	if strings.TrimSpace(server.DisabledReason) != "" {
		return "disabled: " + server.DisabledReason
	}
	return "disabled"
}

func mcpServerEnvDisplay(server *mcpCLIServer) string {
	if server == nil || len(server.Env) == 0 && len(server.EnvVars) == 0 {
		return "-"
	}
	parts := []string{}
	keys := make([]string, 0, len(server.Env))
	for key := range server.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts = append(parts, key+"=*****")
	}
	for _, envVar := range server.EnvVars {
		if name := strings.TrimSpace(envVar.Name); name != "" {
			parts = append(parts, name+"=*****")
		}
	}
	return strings.Join(parts, ", ")
}

func mcpServerHeaderDisplay(headers map[string]string, redact bool) string {
	if len(headers) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := headers[key]
		if redact {
			value = "*****"
		}
		parts = append(parts, key+"="+value)
	}
	return strings.Join(parts, ", ")
}

func mcpServerToolListDisplay(tools []string) string {
	if len(tools) == 0 {
		return "[]"
	}
	return strings.Join(tools, ", ")
}

// mcpAuthStatusJSON renders the status the way the 0.166 `mcp list --json`
// payload does: the core `McpAuthStatus` enum is serialized with
// `rename_all = "snake_case"` (`o_auth`, `not_logged_in`, ...), while the
// app-server protocol uses the camelCase variant names.
func mcpAuthStatusJSON(ctx context.Context, store *mcp.OAuthStore, name string, server *mcpCLIServer) string {
	switch mcpServerAuthStatus(ctx, store, name, server) {
	case mcp.MCPAuthUnknown:
		return "unknown"
	case mcp.MCPAuthUnsupported:
		return "unsupported"
	case mcp.MCPAuthNotLoggedIn:
		return "not_logged_in"
	case mcp.MCPAuthBearerToken:
		return "bearer_token"
	case mcp.MCPAuthOAuth:
		return "o_auth"
	default:
		return string(mcp.MCPAuthUnsupported)
	}
}

func mcpAuthStatusDisplay(status mcp.MCPAuthStatus) string {
	switch status {
	case mcp.MCPAuthNotLoggedIn:
		return "Not logged in"
	case mcp.MCPAuthBearerToken:
		return "Bearer token"
	case mcp.MCPAuthOAuth:
		return "OAuth"
	case mcp.MCPAuthUnknown:
		return "Unknown"
	default:
		return "Unsupported"
	}
}

func floatDisplay(value float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.6f", value), "0"), ".")
}

func cloneFloat64Ptr(value *float64) *float64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func stringMapJSONValue(values map[string]string) any {
	if values == nil {
		return nil
	}
	return cloneStringMap(values)
}

func stringSliceJSONValue(values []string) any {
	if values == nil {
		return nil
	}
	return append([]string(nil), values...)
}

func printStringTable(stdout io.Writer, headers []string, rows [][]string) {
	widths := make([]int, len(headers))
	for i := range headers {
		widths[i] = len(headers[i])
	}
	for _, row := range rows {
		for i := range headers {
			if i < len(row) && len(row[i]) > widths[i] {
				widths[i] = len(row[i])
			}
		}
	}
	printRow := func(cells []string) {
		for i := range headers {
			if i > 0 {
				fmt.Fprint(stdout, "  ")
			}
			cell := ""
			if i < len(cells) {
				cell = cells[i]
			}
			fmt.Fprintf(stdout, "%-*s", widths[i], cell)
		}
		fmt.Fprintln(stdout)
	}
	printRow(headers)
	for _, row := range rows {
		printRow(row)
	}
}

func printTable(stdout io.Writer, headers []string, rows [][7]string) {
	widths := make([]int, len(headers))
	for i := range headers {
		widths[i] = len(headers[i])
	}
	for _, row := range rows {
		for i := range headers {
			if len(row[i]) > widths[i] {
				widths[i] = len(row[i])
			}
		}
	}
	printRow := func(cells []string) {
		for i, cell := range cells {
			// Rows are declared as [7]string so every table shares one width
			// shape; tables with fewer headers print only the columns they
			// declare instead of indexing past widths.
			if i >= len(widths) {
				break
			}
			if i > 0 {
				fmt.Fprint(stdout, "  ")
			}
			fmt.Fprintf(stdout, "%-*s", widths[i], cell)
		}
		fmt.Fprintln(stdout)
	}
	printRow(headers)
	for _, row := range rows {
		printRow(row[:])
	}
}

func dashIfEmpty(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func contextOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func stringPtrIfNotEmptyLocal(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

type mcpCLIStore struct {
	codexHome  string
	httpClient *http.Client
	// policy is the host-owned live policy every config this store loads
	// republishes into (Rust `EmbeddedNetworkPolicy`), so the clients already
	// built from an earlier load observe later reloads and failures.
	policy *config.EmbeddedNetworkPolicy
}

func newMCPCLIStore(codexHome string) *mcpCLIStore {
	policy := config.NewEmbeddedNetworkPolicy()
	_ = policy.LoadLocal(codexHome)
	return &mcpCLIStore{codexHome: codexHome, policy: policy}
}

func (s *mcpCLIStore) Load(overrides []string) (*mcpCLIConfig, error) {
	if err := s.validateUserConfigShape(); err != nil {
		s.policy.Unavailable()
		return nil, err
	}
	if err := s.validateUserConfigSemantics(); err != nil {
		s.policy.Unavailable()
		return nil, err
	}
	loaded, err := config.LoadEffective(s.codexHome, overrides, nil, nil)
	if err != nil {
		s.policy.Unavailable()
		return nil, mcpLoadConfigurationError(err)
	}
	// The host publishes its application network policy for the config and every
	// client it builds from that config binds it.
	publishHostApplicationNetworkPolicy(s.policy, loaded)
	s.httpClient = policyHTTPClient(loaded, codexnetwork.NewHTTPClient(loaded.RespectSystemProxyEnabled(), 0))
	return mcpCLIConfigFromValues(loaded.Values), nil
}

func (s *mcpCLIStore) LoadManaged(ctx context.Context, overrides []string) (*mcpCLIConfig, error) {
	if err := s.validateUserConfigShape(); err != nil {
		s.policy.Unavailable()
		return nil, err
	}
	if err := s.validateUserConfigSemantics(); err != nil {
		s.policy.Unavailable()
		return nil, err
	}
	bootstrap, err := config.LoadEffective(s.codexHome, overrides, nil, nil)
	if err != nil {
		s.policy.Unavailable()
		return nil, mcpLoadConfigurationError(err)
	}
	publishHostApplicationNetworkPolicy(s.policy, bootstrap)
	s.httpClient = policyHTTPClient(bootstrap, codexnetwork.NewHTTPClient(bootstrap.RespectSystemProxyEnabled(), 0))
	var loader *config.CloudConfigLoader
	if bootstrap.RequiresRemoteConfig() {
		loader, err = configServerCloudConfigBundleLoaderForRun(ctx, s.codexHome, bootstrap, bootstrap.Requirements.ConfigServer)
		if err != nil {
			return nil, err
		}
	} else {
		snapshot, err := auth.NewStoreWithOptions(s.codexHome, authStoreOptionsFromLoadedConfig(bootstrap)).Load()
		if err != nil {
			return nil, err
		}
		if !mcpCLICloudConfigEligibleAuth(snapshot) {
			return mcpCLIConfigFromValues(bootstrap.Values), nil
		}
		authHeaders, err := model.AuthHeadersFromAuth(*snapshot)
		if err != nil {
			return nil, err
		}
		loader = config.NewCloudConfigLoader(func() (*config.CloudConfigBundle, error) {
			loadCtx, cancel := context.WithTimeout(contextOrBackground(ctx), 15*time.Second)
			defer cancel()
			policy := publishHostApplicationNetworkPolicy(s.policy, bootstrap)
			return config.LoadCloudConfigBundle(loadCtx, config.CloudConfigFetchOptions{
				CodexHome:          s.codexHome,
				BaseURL:            bootstrap.ChatGPTBaseURL(),
				ChatGPTUserID:      auth.ChatGPTUserIDFromAuth(snapshot),
				AccountID:          auth.AccountIDFromAuthForRestrictions(snapshot),
				HTTPClient:         s.httpClient,
				FallbackHTTPClient: bootstrapFallbackHTTPClient(bootstrap, 0),
				NetworkPolicy:      &policy,
				Authorize: func(requestCtx context.Context, request *http.Request) error {
					return authHeaders.Apply(requestCtx, request, nil)
				},
			})
		})
	}
	loaded, err := config.LoadEffectiveWithOptions(s.codexHome, &config.EffectiveOptions{
		RawOverrides:         append([]string(nil), overrides...),
		IncludeManagedConfig: true,
		ManagedConfigPath:    filepath.Join(s.codexHome, "managed_config.toml"),
		CloudConfigBundle:    loader,
	})
	if err != nil {
		return nil, err
	}
	publishHostApplicationNetworkPolicy(s.policy, loaded)
	s.httpClient = policyHTTPClient(loaded, codexnetwork.NewHTTPClient(loaded.RespectSystemProxyEnabled(), 0))
	return mcpCLIConfigFromValues(loaded.Values), nil
}

func mcpCLICloudConfigEligibleAuth(snapshot *auth.AuthDotJSON) bool {
	if snapshot == nil {
		return false
	}
	switch snapshot.Mode() {
	case "chatgpt", "chatgptAuthTokens", "personal-access-token", "agent-identity":
		return auth.AccountFromAuth(snapshot) != nil
	default:
		return false
	}
}

func mcpCLIHTTPClientWithTimeout(client *http.Client, timeout time.Duration) *http.Client {
	if client == nil {
		return &http.Client{Timeout: timeout}
	}
	cloned := *client
	cloned.Timeout = timeout
	return &cloned
}

func (s *mcpCLIStore) Upsert(overrides []string, server *mcpCLIServer) error {
	cfg, err := s.Load(overrides)
	if err != nil {
		return err
	}
	cfg.Servers[server.Name] = server
	return s.write(cfg)
}

func (s *mcpCLIStore) Remove(overrides []string, name string) (bool, error) {
	cfg, err := s.loadServersForRemoval(overrides)
	if err != nil {
		return false, err
	}
	_, existed := cfg.Servers[name]
	if !existed {
		// Rust only edits the document when the server was configured, so a
		// missing name leaves config.toml byte-for-byte untouched.
		return false, nil
	}
	delete(cfg.Servers, name)
	return true, s.write(cfg)
}

// loadServersForRemoval reads the configured servers the way Rust's remove
// does: the raw document is decoded into the server map only, so unrelated
// top-level type errors do not stop a removal, while a malformed server table
// reports `failed to load MCP servers from <codex home>`.
func (s *mcpCLIStore) loadServersForRemoval(overrides []string) (*mcpCLIConfig, error) {
	if err := s.validateUserConfigServersShape(); err != nil {
		return nil, err
	}
	// Rust decodes only the mcp_servers table here, so the layer merge must not
	// run the semantic checks `Config::load` performs on unrelated fields.
	loaded, err := config.Load(s.codexHome)
	if err != nil {
		return nil, fmt.Errorf("failed to load MCP servers from %s\n\nCaused by:\n%s", s.codexHome, mcpIndentedCauses(err))
	}
	return mcpCLIConfigFromValues(loaded.Values), nil
}

// validateUserConfigShape replays serde's pass over the user config before the
// loader's own semantic checks run: Rust deserializes ConfigToml first, so a
// type or variant mismatch reports `failed to load bootstrap configuration`
// even when the same field also has a Go-side check with different wording.
func (s *mcpCLIStore) validateUserConfigShape() error {
	path := filepath.Join(s.codexHome, "config.toml")
	values, document, err := config.ReadConfigDocumentFile(path)
	if err != nil {
		// An unreadable document falls through to the loader's own diagnostic; a
		// syntax failure fails the bootstrap load with the position and the toml
		// crate's snippet, which ReadConfigDocumentFile already renders.
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return mcpBootstrapConfigError(err)
	}
	if err := config.ValidateConfigDocumentValues(values, &config.ConfigDocumentLocator{Path: path, Text: document}); err != nil {
		return mcpBootstrapConfigError(err)
	}
	return nil
}

// validateUserConfigSemantics replays Config::load's post-merge rejections, so
// a document the full Rust loader refuses also fails `codex mcp list` instead of
// being reported as an empty server list.
func (s *mcpCLIStore) validateUserConfigSemantics() error {
	loaded, err := config.Load(s.codexHome)
	if err != nil {
		return nil
	}
	if err := config.ValidateLoadedConfigSemantics(loaded.Values); err != nil {
		return mcpLoadConfigurationError(err)
	}
	// Config::load also reads the path-valued settings, so a missing
	// instructions or prompt file fails `codex mcp list` in Rust.
	if err := config.ValidateLoadedConfigFiles(loaded.Values, s.codexHome); err != nil {
		return mcpLoadConfigurationError(err)
	}
	return nil
}

// validateUserConfigServersShape is the narrower check `codex mcp remove` needs:
// Rust decodes only the server map there, so an unrelated top-level type error
// does not stop a removal.
func (s *mcpCLIStore) validateUserConfigServersShape() error {
	values, _, err := config.ReadConfigDocumentFile(filepath.Join(s.codexHome, "config.toml"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("failed to load MCP servers from %s\n\nCaused by:\n%s", s.codexHome, mcpIndentedCauses(err))
	}
	if err := config.ValidateMCPServersValues(values); err != nil {
		return fmt.Errorf("failed to load MCP servers from %s\n\nCaused by:\n%s", s.codexHome, mcpIndentedCauses(err))
	}
	return nil
}

// mcpBootstrapConfigError reports a config rejection the way Rust's bootstrap
// load does: the context line, then anyhow's indented `Caused by:` block.
// mcpLoadConfigurationError wraps a semantic load failure the way Rust's
// mcp_cmd does with anyhow's `failed to load configuration` context.
func mcpLoadConfigurationError(err error) error {
	return fmt.Errorf("failed to load configuration\n\nCaused by:\n%s", mcpIndentedCauses(err))
}

func mcpBootstrapConfigError(err error) error {
	return fmt.Errorf("failed to load bootstrap configuration\n\nCaused by:\n%s", mcpIndentedCauses(err))
}

func mcpIndentedCauses(err error) string {
	return "    " + strings.ReplaceAll(err.Error(), "\n", "\n    ")
}

func (s *mcpCLIStore) write(cfg *mcpCLIConfig) error {
	service := config.NewConfigService(s.codexHome)
	if len(cfg.Servers) == 0 {
		// Rust's replace_mcp_servers clears the whole table when nothing is left,
		// so `mcp remove` on the last server leaves no `[mcp_servers]` header.
		_, err := service.WriteValue(&config.ConfigValueWriteParams{
			KeyPath:       "mcp_servers",
			Value:         nil,
			MergeStrategy: config.MergeReplace,
		})
		return err
	}
	servers := config.NewOrderedTable()
	servers.Implicit = true
	for _, name := range s.serverDocumentOrder(cfg) {
		servers.Set(name, mcpServerToConfigValue(cfg.Servers[name]))
	}
	_, err := service.WriteValue(&config.ConfigValueWriteParams{
		KeyPath:       "mcp_servers",
		Value:         servers,
		MergeStrategy: config.MergeReplace,
	})
	return err
}

// serverDocumentOrder keeps the order toml_edit preserves when it edits a
// document in place: servers already in config.toml stay where they are and new
// names are appended.
func (s *mcpCLIStore) serverDocumentOrder(cfg *mcpCLIConfig) []string {
	order := make([]string, 0, len(cfg.Servers))
	seen := map[string]bool{}
	for _, name := range mcpServerSectionOrder(filepath.Join(s.codexHome, "config.toml")) {
		if _, ok := cfg.Servers[name]; ok && !seen[name] {
			seen[name] = true
			order = append(order, name)
		}
	}
	for _, name := range sortedMCPServerNames(cfg.Servers) {
		if !seen[name] {
			seen[name] = true
			order = append(order, name)
		}
	}
	return order
}

func mcpCLIConfigFromValues(values map[string]any) *mcpCLIConfig {
	cfg := &mcpCLIConfig{Servers: map[string]*mcpCLIServer{}}
	rawServers, ok := values["mcp_servers"].(map[string]any)
	if !ok {
		return cfg
	}
	for name, raw := range rawServers {
		table, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		server := mcpServerFromConfigValue(name, table)
		if server != nil {
			cfg.Servers[name] = server
		}
	}
	return cfg
}

func mcpServerFromConfigValue(name string, table map[string]any) *mcpCLIServer {
	server := &mcpCLIServer{Name: name, Enabled: true}
	if enabled, ok := table["enabled"].(bool); ok {
		server.Enabled = enabled
	}
	server.DisabledReason = stringFromAny(table["disabled_reason"])
	server.Required = boolFromAny(table["required"])
	server.StartupReadiness = stringFromAny(table["startup_readiness"])
	server.SupportsParallelToolCalls = boolFromAny(table["supports_parallel_tool_calls"])
	server.ToolInputSchemaMaxBytes = positiveIntPtrFromAny(table["tool_input_schema_max_bytes"])
	server.Auth = stringFromAny(table["auth"])
	server.EnvironmentID = stringFromAny(table["environment_id"])
	server.StartupTimeoutSec = floatPtrFromAny(table["startup_timeout_sec"])
	server.ToolTimeoutSec = floatPtrFromAny(table["tool_timeout_sec"])
	server.DefaultToolsApprovalMode = stringFromAny(table["default_tools_approval_mode"])
	server.EnabledTools = stringSliceFromAny(table["enabled_tools"])
	server.DisabledTools = stringSliceFromAny(table["disabled_tools"])
	server.OmitToolsFrom = stringSliceFromAny(table["omit_tools_from"])
	server.Scopes = stringSliceFromAny(table["scopes"])
	server.Tools = nestedAnyMapFromAny(table["tools"])
	if url := stringFromAny(table["url"]); url != "" {
		server.Type = "streamable_http"
		server.URL = url
		server.BearerTokenEnvVar = stringFromAny(table["bearer_token_env_var"])
		server.HTTPHeaders = stringMapFromAny(table["http_headers"])
		server.EnvHTTPHeaders = stringMapFromAny(table["env_http_headers"])
		server.HTTPHeadersHelper = stringFromAny(table["http_headers_helper"])
		server.OAuthClientID = mcpOAuthClientIDFromConfig(table)
		server.OAuthClientSecret = mcpOAuthClientSecretFromConfig(table)
		server.OAuthCallbackURL = mcpOAuthCallbackURLFromConfig(table)
		server.OAuthAuthorizationServerIssuer = mcpOAuthAuthorizationServerIssuerFromConfig(table)
		server.OAuthResource = stringFromAny(table["oauth_resource"])
		return server
	}
	server.Type = "stdio"
	server.Command = stringFromAny(table["command"])
	server.Args = stringSliceFromAny(table["args"])
	server.Env = stringMapFromAny(table["env"])
	server.EnvVars = mcpEnvVarsFromAny(table["env_vars"])
	server.CWD = stringFromAny(table["cwd"])
	return server
}

// mcpServerSectionOrder returns the server names in the order their tables
// appear in the config file, so a write keeps toml_edit's document order: Rust
// leaves existing tables where they are and appends new ones.
func mcpServerSectionOrder(path string) []string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var order []string
	seen := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "[mcp_servers.") {
			continue
		}
		end := strings.Index(trimmed, "]")
		if end < 0 {
			continue
		}
		rest := strings.TrimPrefix(trimmed[1:end], "mcp_servers.")
		name, ok := mcpTOMLKeyPrefix(rest)
		if !ok || name == "" || seen[name] {
			continue
		}
		seen[name] = true
		order = append(order, name)
	}
	return order
}

// mcpTOMLKeyPrefix reads the first key of a dotted TOML path and reports whether
// what follows is a nested table rather than a stray suffix.
func mcpTOMLKeyPrefix(text string) (string, bool) {
	if strings.HasPrefix(text, "\"") {
		end := strings.Index(text[1:], "\"")
		if end < 0 {
			return "", false
		}
		name := text[1 : 1+end]
		rest := text[1+end+1:]
		if rest == "" || strings.HasPrefix(rest, ".") {
			return name, true
		}
		return "", false
	}
	name := text
	if idx := strings.IndexAny(name, ". \t"); idx >= 0 {
		name = name[:idx]
	}
	if name == "" {
		return "", false
	}
	return name, true
}

// mcpServerToConfigValue mirrors serialize_mcp_server_table: the fields keep
// Rust's declaration order so the written config.toml matches byte for byte.
func mcpServerToConfigValue(server *mcpCLIServer) *config.OrderedTable {
	value := config.NewOrderedTable()
	if server.Type == "streamable_http" {
		value.Set("url", server.URL)
		if server.BearerTokenEnvVar != "" {
			value.Set("bearer_token_env_var", server.BearerTokenEnvVar)
		}
		if len(server.HTTPHeaders) > 0 {
			value.Set("http_headers", stringMapToAnyMap(server.HTTPHeaders))
		}
		if len(server.EnvHTTPHeaders) > 0 {
			value.Set("env_http_headers", stringMapToAnyMap(server.EnvHTTPHeaders))
		}
		if server.HTTPHeadersHelper != "" {
			value.Set("http_headers_helper", server.HTTPHeadersHelper)
		}
		mcpServerAddSharedOrderedConfigValue(value, server)
		return value
	}
	value.Set("command", server.Command)
	if len(server.Args) > 0 {
		value.Set("args", stringSliceToAnySlice(server.Args))
	}
	if len(server.Env) > 0 {
		value.Set("env", stringMapToAnyMap(server.Env))
	}
	if len(server.EnvVars) > 0 {
		value.Set("env_vars", mcpEnvVarsConfigValue(server.EnvVars))
	}
	if server.CWD != "" {
		value.Set("cwd", server.CWD)
	}
	mcpServerAddSharedOrderedConfigValue(value, server)
	return value
}

// mcpServerAddSharedOrderedConfigValue appends the transport-independent fields
// in Rust's order (auth, enabled, environment_id, ..., oauth, oauth_resource,
// tools).
func mcpServerAddSharedOrderedConfigValue(value *config.OrderedTable, server *mcpCLIServer) {
	if server.Auth != "" && server.Auth != "oauth" {
		value.Set("auth", server.Auth)
	}
	if !server.Enabled {
		value.Set("enabled", false)
	}
	if server.EnvironmentID != "" && server.EnvironmentID != "local" {
		value.Set("environment_id", server.EnvironmentID)
	}
	if server.Required {
		value.Set("required", true)
	}
	if server.StartupReadiness == string(mcp.MCPStartupReadinessCatalog) {
		value.Set("startup_readiness", string(mcp.MCPStartupReadinessCatalog))
	}
	if server.SupportsParallelToolCalls {
		value.Set("supports_parallel_tool_calls", true)
	}
	if server.ToolInputSchemaMaxBytes != nil && *server.ToolInputSchemaMaxBytes > 0 {
		value.Set("tool_input_schema_max_bytes", *server.ToolInputSchemaMaxBytes)
	}
	if len(server.OmitToolsFrom) > 0 {
		value.Set("omit_tools_from", stringSliceToAnySlice(server.OmitToolsFrom))
	}
	if server.StartupTimeoutSec != nil {
		value.Set("startup_timeout_sec", *server.StartupTimeoutSec)
	}
	if server.ToolTimeoutSec != nil {
		value.Set("tool_timeout_sec", *server.ToolTimeoutSec)
	}
	if server.DefaultToolsApprovalMode != "" {
		value.Set("default_tools_approval_mode", server.DefaultToolsApprovalMode)
	}
	if len(server.EnabledTools) > 0 {
		value.Set("enabled_tools", stringSliceToAnySlice(server.EnabledTools))
	}
	if len(server.DisabledTools) > 0 {
		value.Set("disabled_tools", stringSliceToAnySlice(server.DisabledTools))
	}
	if len(server.Scopes) > 0 {
		value.Set("scopes", stringSliceToAnySlice(server.Scopes))
	}
	if server.OAuthClientID != "" || server.OAuthClientSecret != "" || server.OAuthAuthorizationServerIssuer != "" || server.OAuthCallbackURL != "" {
		oauth := config.NewOrderedTable()
		if server.OAuthClientID != "" {
			oauth.Set("client_id", server.OAuthClientID)
		}
		if server.OAuthClientSecret != "" {
			oauth.Set("client_secret", server.OAuthClientSecret)
		}
		if server.OAuthCallbackURL != "" {
			oauth.Set("callback_url", server.OAuthCallbackURL)
		}
		if server.OAuthAuthorizationServerIssuer != "" {
			oauth.Set("authorization_server_issuer", server.OAuthAuthorizationServerIssuer)
		}
		value.Set("oauth", oauth)
	}
	if server.OAuthResource != "" {
		value.Set("oauth_resource", server.OAuthResource)
	}
	if len(server.Tools) > 0 {
		tools := config.NewOrderedTable()
		tools.Implicit = true
		names := make([]string, 0, len(server.Tools))
		for name := range server.Tools {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			tools.Set(name, mcpServerToolToConfigValue(server.Tools[name]))
		}
		value.Set("tools", tools)
	}
}

// mcpServerToolToConfigValue mirrors serialize_mcp_server_tool.
func mcpServerToolToConfigValue(tool map[string]any) *config.OrderedTable {
	value := config.NewOrderedTable()
	if mode := stringFromAny(tool["approval_mode"]); mode != "" {
		value.Set("approval_mode", mode)
	}
	if limit := positiveIntPtrFromAny(tool["output_token_limit"]); limit != nil {
		value.Set("output_token_limit", *limit)
	}
	return value
}

// mcpConfigTable resolves the plain maps read from the file and the ordered
// tables mcpServerToConfigValue builds, so readers accept both shapes.
func mcpConfigTable(value any) (map[string]any, bool) {
	switch table := value.(type) {
	case map[string]any:
		return table, true
	case *config.OrderedTable:
		if table == nil {
			return nil, false
		}
		return table.Values, true
	default:
		return nil, false
	}
}

func mcpOAuthClientIDFromConfig(table map[string]any) string {
	if value := stringFromAny(table["oauth_client_id"]); value != "" {
		return value
	}
	oauth, ok := mcpConfigTable(table["oauth"])
	if !ok {
		return ""
	}
	return stringFromAny(oauth["client_id"])
}

func mcpOAuthClientSecretFromConfig(table map[string]any) string {
	if value := stringFromAny(table["oauth_client_secret"]); value != "" {
		return value
	}
	oauth, ok := mcpConfigTable(table["oauth"])
	if !ok {
		return ""
	}
	return stringFromAny(oauth["client_secret"])
}

func mcpOAuthCallbackURLFromConfig(table map[string]any) string {
	if value := stringFromAny(table["oauth_callback_url"]); value != "" {
		return value
	}
	oauth, ok := mcpConfigTable(table["oauth"])
	if !ok {
		return ""
	}
	return stringFromAny(oauth["callback_url"])
}

func mcpOAuthAuthorizationServerIssuerFromConfig(table map[string]any) string {
	if value := stringFromAny(table["oauth_authorization_server_issuer"]); value != "" {
		return value
	}
	oauth, ok := mcpConfigTable(table["oauth"])
	if !ok {
		return ""
	}
	return stringFromAny(oauth["authorization_server_issuer"])
}

func mcpEnvVarsFromAny(value any) []mcpCLIEnvVar {
	switch typed := value.(type) {
	case []mcpCLIEnvVar:
		return append([]mcpCLIEnvVar(nil), typed...)
	case []string:
		out := make([]mcpCLIEnvVar, 0, len(typed))
		for _, name := range typed {
			if name = strings.TrimSpace(name); name != "" {
				out = append(out, mcpCLIEnvVar{Name: name})
			}
		}
		return out
	case []any:
		out := make([]mcpCLIEnvVar, 0, len(typed))
		for _, item := range typed {
			switch entry := item.(type) {
			case string:
				if name := strings.TrimSpace(entry); name != "" {
					out = append(out, mcpCLIEnvVar{Name: name})
				}
			case map[string]any:
				name := stringFromAny(entry["name"])
				if name == "" {
					continue
				}
				out = append(out, mcpCLIEnvVar{Name: name, Source: stringFromAny(entry["source"])})
			case map[string]string:
				name := strings.TrimSpace(entry["name"])
				if name == "" {
					continue
				}
				out = append(out, mcpCLIEnvVar{Name: name, Source: strings.TrimSpace(entry["source"])})
			}
		}
		return out
	default:
		return nil
	}
}

func mcpEnvVarsConfigValue(values []mcpCLIEnvVar) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		name := strings.TrimSpace(value.Name)
		if name == "" {
			continue
		}
		source := strings.TrimSpace(value.Source)
		if source == "" {
			out = append(out, name)
			continue
		}
		out = append(out, map[string]any{"name": name, "source": source})
	}
	return out
}

func mcpServerEnvVarsJSONValue(values []mcpCLIEnvVar) []any {
	if values == nil {
		return []any{}
	}
	return mcpEnvVarsConfigValue(values)
}

func stringFromAny(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func boolFromAny(value any) bool {
	text, ok := value.(bool)
	return ok && text
}

func floatPtrFromAny(value any) *float64 {
	switch typed := value.(type) {
	case float64:
		return &typed
	case float32:
		value := float64(typed)
		return &value
	case int:
		value := float64(typed)
		return &value
	case int64:
		value := float64(typed)
		return &value
	case int32:
		value := float64(typed)
		return &value
	default:
		return nil
	}
}

func positiveIntPtrFromAny(value any) *int {
	switch typed := value.(type) {
	case int:
		if typed > 0 {
			return &typed
		}
	case int64:
		if typed > 0 {
			value := int(typed)
			return &value
		}
	case float64:
		if typed > 0 && typed == float64(int(typed)) {
			value := int(typed)
			return &value
		}
	}
	return nil
}

func stringSliceFromAny(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func stringMapFromAny(value any) map[string]string {
	switch table := value.(type) {
	case map[string]string:
		out := map[string]string{}
		for key, text := range table {
			out[key] = text
		}
		return out
	case map[string]any:
		out := map[string]string{}
		for key, raw := range table {
			if text, ok := raw.(string); ok {
				out[key] = text
			}
		}
		return out
	default:
		return nil
	}
}

func stringMapToAnyMap(values map[string]string) map[string]any {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func stringSliceToAnySlice(values []string) []any {
	if len(values) == 0 {
		return nil
	}
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

func nestedAnyMapFromAny(value any) map[string]map[string]any {
	table, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]map[string]any{}
	for key, raw := range table {
		switch nested := raw.(type) {
		case map[string]any:
			out[key] = cloneAnyMapLocal(nested)
		case map[string]string:
			converted := map[string]any{}
			for nestedKey, nestedValue := range nested {
				converted[nestedKey] = nestedValue
			}
			out[key] = converted
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func nestedAnyMapToAnyMap(values map[string]map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = cloneAnyMapLocal(value)
	}
	return out
}

func cloneAnyMapLocal(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		if nested, ok := value.(map[string]any); ok {
			out[key] = cloneAnyMapLocal(nested)
			continue
		}
		out[key] = value
	}
	return out
}
