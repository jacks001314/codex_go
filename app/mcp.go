package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sort"
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
)

type mcpCLIConfig struct {
	Servers map[string]*mcpCLIServer
}

type mcpCLIServer struct {
	Name                      string                    `json:"name,omitempty"`
	Type                      string                    `json:"type,omitempty"`
	Command                   string                    `json:"command,omitempty"`
	Args                      []string                  `json:"args,omitempty"`
	Env                       map[string]string         `json:"env,omitempty"`
	EnvVars                   []mcpCLIEnvVar            `json:"env_vars,omitempty"`
	CWD                       string                    `json:"cwd,omitempty"`
	URL                       string                    `json:"url,omitempty"`
	BearerTokenEnvVar         string                    `json:"bearer_token_env_var,omitempty"`
	HTTPHeaders               map[string]string         `json:"http_headers,omitempty"`
	EnvHTTPHeaders            map[string]string         `json:"env_http_headers,omitempty"`
	OAuthClientID             string                    `json:"oauth_client_id,omitempty"`
	OAuthResource             string                    `json:"oauth_resource,omitempty"`
	Auth                      string                    `json:"auth,omitempty"`
	EnvironmentID             string                    `json:"environment_id,omitempty"`
	Enabled                   bool                      `json:"enabled"`
	DisabledReason            string                    `json:"disabled_reason,omitempty"`
	Required                  bool                      `json:"required,omitempty"`
	SupportsParallelToolCalls bool                      `json:"supports_parallel_tool_calls,omitempty"`
	StartupTimeoutSec         *float64                  `json:"startup_timeout_sec,omitempty"`
	ToolTimeoutSec            *float64                  `json:"tool_timeout_sec,omitempty"`
	EnabledTools              []string                  `json:"enabled_tools,omitempty"`
	DisabledTools             []string                  `json:"disabled_tools,omitempty"`
	Scopes                    []string                  `json:"scopes,omitempty"`
	DefaultToolsApprovalMode  string                    `json:"default_tools_approval_mode,omitempty"`
	Tools                     map[string]map[string]any `json:"tools,omitempty"`
}

type mcpCLIEnvVar struct {
	Name   string
	Source string
}

type mcpListJSONEntry struct {
	Name              string            `json:"name"`
	Enabled           bool              `json:"enabled"`
	DisabledReason    *string           `json:"disabled_reason"`
	Transport         map[string]any    `json:"transport"`
	StartupTimeoutSec *float64          `json:"startup_timeout_sec"`
	ToolTimeoutSec    *float64          `json:"tool_timeout_sec"`
	AuthStatus        mcp.MCPAuthStatus `json:"auth_status"`
}

type mcpGetJSONEntry struct {
	Name              string         `json:"name"`
	Enabled           bool           `json:"enabled"`
	DisabledReason    *string        `json:"disabled_reason"`
	Transport         map[string]any `json:"transport"`
	EnabledTools      any            `json:"enabled_tools"`
	DisabledTools     any            `json:"disabled_tools"`
	StartupTimeoutSec *float64       `json:"startup_timeout_sec"`
	ToolTimeoutSec    *float64       `json:"tool_timeout_sec"`
}

func runMCP(ctx context.Context, opts *cli.MCPOptions, stdout io.Writer) error {
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
		return runMCPLogin(ctx, store, opts, stdout)
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
				StartupTimeoutSec: cloneFloat64Ptr(server.StartupTimeoutSec),
				ToolTimeoutSec:    cloneFloat64Ptr(server.ToolTimeoutSec),
				AuthStatus:        mcpServerAuthStatus(oauthStore, name, server),
			})
		}
		encoder := json.NewEncoder(stdout)
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
		authStatus := mcpAuthStatusDisplay(mcpServerAuthStatus(oauthStore, name, server))
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
			StartupTimeoutSec: cloneFloat64Ptr(server.StartupTimeoutSec),
			ToolTimeoutSec:    cloneFloat64Ptr(server.ToolTimeoutSec),
		}
		encoder := json.NewEncoder(stdout)
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
	fmt.Fprintf(stdout, "  transport: %s\n", mcpServerTransportLabel(server))
	if server.Type == "streamable_http" {
		fmt.Fprintf(stdout, "  url: %s\n", server.URL)
		fmt.Fprintf(stdout, "  bearer_token_env_var: %s\n", dashIfEmpty(server.BearerTokenEnvVar))
		fmt.Fprintf(stdout, "  http_headers: %s\n", mcpServerHeaderDisplay(server.HTTPHeaders, true))
		fmt.Fprintf(stdout, "  env_http_headers: %s\n", mcpServerHeaderDisplay(server.EnvHTTPHeaders, false))
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
	server := &mcpCLIServer{
		Name:    opts.Name,
		Enabled: true,
	}
	if strings.TrimSpace(opts.URL) != "" {
		server.Type = "streamable_http"
		server.URL = strings.TrimSpace(opts.URL)
		server.BearerTokenEnvVar = strings.TrimSpace(opts.BearerTokenEnvVar)
		server.OAuthClientID = strings.TrimSpace(opts.OAuthClientID)
		server.OAuthResource = strings.TrimSpace(opts.OAuthResource)
	} else {
		server.Type = "stdio"
		server.Command = opts.Command[0]
		server.Args = append([]string(nil), opts.Command[1:]...)
		server.Env = cloneStringMap(opts.Env)
	}
	if err := store.Upsert(opts.ConfigOverrides, server); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Added global MCP server '%s'.\n", opts.Name)
	support := mcpOAuthLoginSupport(ctx, server, store.httpClient)
	switch support.Kind {
	case mcpOAuthLoginSupported:
		fmt.Fprintln(stdout, "Detected OAuth support. Starting OAuth flow…")
		resolvedScopes := resolveMCPOAuthScopes(nil, false, nil, false, support.Discovery.ScopesSupported)
		if err := performMCPCLIOAuthLoginRetryWithoutScopes(ctx, store, opts.Name, server, support.Discovery, resolvedScopes, stdout); err != nil {
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

func runMCPLogin(ctx context.Context, store *mcpCLIStore, opts *cli.MCPOptions, stdout io.Writer) error {
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
		if err := performMCPCLIOAuthLoginRetryWithoutScopes(ctx, store, opts.Name, server, support.Discovery, resolvedScopes, stdout); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Successfully logged in to MCP server '%s'.\n", opts.Name)
		return nil
	}
	service := mcp.NewMCPService(&mcp.RuntimeConfig{Servers: map[string]mcp.ServerRegistration{
		opts.Name: {Config: mcpServerRuntimeConfig(opts.Name, server, store.codexHome)},
	}, CodexHome: store.codexHome, HTTPClient: store.httpClient})
	response, err := service.OauthLogin(&mcp.MCPServerOauthLoginParams{Name: opts.Name, Scopes: resolvedScopes.Scopes})
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

func performMCPCLIOAuthLoginRetryWithoutScopes(ctx context.Context, store *mcpCLIStore, name string, server *mcpCLIServer, discovery *mcp.StreamableHTTPOAuthDiscovery, resolvedScopes mcpResolvedOAuthScopes, stdout io.Writer) error {
	err := performMCPCLIOAuthLogin(ctx, store, name, server, discovery, resolvedScopes, stdout)
	if err == nil {
		return nil
	}
	if !shouldRetryMCPLoginWithoutScopes(resolvedScopes, err) {
		return err
	}
	fmt.Fprintln(stdout, "OAuth provider rejected discovered scopes. Retrying without scopes…")
	return performMCPCLIOAuthLogin(ctx, store, name, server, discovery, mcpResolvedOAuthScopes{Source: mcpOAuthScopesEmpty}, stdout)
}

func performMCPCLIOAuthLogin(ctx context.Context, store *mcpCLIStore, name string, server *mcpCLIServer, discovery *mcp.StreamableHTTPOAuthDiscovery, resolvedScopes mcpResolvedOAuthScopes, stdout io.Writer) error {
	if store == nil || server == nil || discovery == nil {
		return fmt.Errorf("OAuth login requires a discovered streamable HTTP server")
	}
	httpClient := mcpCLIHTTPClientWithTimeout(store.httpClient, mcpCLIOAuthDiscoveryTimeout)
	startCtx, cancelStart := context.WithTimeout(contextOrBackground(ctx), mcpCLIOAuthDiscoveryTimeout)
	login, err := mcp.StartOAuthLoginServer(startCtx, &mcp.OAuthLoginServerOptions{
		ServerName:            name,
		ServerURL:             server.URL,
		ClientID:              strings.TrimSpace(server.OAuthClientID),
		RegistrationEndpoint:  discovery.RegistrationEndpoint,
		ClientName:            "Codex",
		AuthorizationEndpoint: discovery.AuthorizationEndpoint,
		TokenEndpoint:         discovery.TokenEndpoint,
		Resource:              firstNonEmptyLocal(server.OAuthResource, discovery.Resource),
		Scopes:                append([]string(nil), resolvedScopes.Scopes...),
		Store:                 mcp.NewOAuthStore(store.codexHome),
		HTTPClient:            httpClient,
	})
	cancelStart()
	if err != nil {
		return err
	}
	defer func() {
		_ = login.Cancel(context.Background())
	}()
	fmt.Fprintf(stdout, "Authorize `%s` by opening this URL in your browser:\n%s\n\n", name, login.AuthorizationURL)
	if err := openMCPLoginBrowser(login.AuthorizationURL); err != nil {
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
	removed, err := mcp.NewOAuthStore(store.codexHome).Delete(opts.Name, server.URL)
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
	if name == "" {
		return fmt.Errorf("invalid server name '%s' (use letters, numbers, '-', '_')", name)
	}
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			continue
		}
		return fmt.Errorf("invalid server name '%s' (use letters, numbers, '-', '_')", name)
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

func mcpServerTransportJSON(server *mcpCLIServer) map[string]any {
	if server.Type == "streamable_http" {
		return map[string]any{
			"type":                 "streamable_http",
			"url":                  server.URL,
			"bearer_token_env_var": stringPtrIfNotEmptyLocal(server.BearerTokenEnvVar),
			"http_headers":         stringMapJSONValue(server.HTTPHeaders),
			"env_http_headers":     stringMapJSONValue(server.EnvHTTPHeaders),
		}
	}
	return map[string]any{
		"type":     "stdio",
		"command":  server.Command,
		"args":     append([]string(nil), server.Args...),
		"env":      stringMapJSONValue(server.Env),
		"env_vars": mcpServerEnvVarsJSONValue(server.EnvVars),
		"cwd":      stringPtrIfNotEmptyLocal(server.CWD),
	}
}

func mcpServerRuntimeConfig(name string, server *mcpCLIServer, codexHome string) mcp.ServerConfig {
	if server == nil {
		return mcp.ServerConfig{}
	}
	return mcp.ServerConfig{
		Command:           server.Command,
		Args:              append([]string(nil), server.Args...),
		Env:               cloneStringMap(server.Env),
		URL:               server.URL,
		BearerTokenEnvVar: server.BearerTokenEnvVar,
		OAuthClientID:     server.OAuthClientID,
		OAuthResource:     server.OAuthResource,
		OAuthServerName:   name,
		CodexHome:         codexHome,
		Enabled:           server.Enabled,
		Required:          server.Required,
		EnvironmentID:     server.EnvironmentID,
	}
}

func mcpServerAuthStatus(store *mcp.OAuthStore, name string, server *mcpCLIServer) mcp.MCPAuthStatus {
	config := mcpServerRuntimeConfig(name, server, "")
	status, err := store.AuthStatus(name, &config)
	if err != nil {
		return mcp.MCPAuthUnsupported
	}
	return status
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

func mcpAuthStatusDisplay(status mcp.MCPAuthStatus) string {
	switch status {
	case mcp.MCPAuthNotLoggedIn:
		return "Not logged in"
	case mcp.MCPAuthBearerToken:
		return "Bearer token"
	case mcp.MCPAuthOAuth:
		return "OAuth"
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
}

func newMCPCLIStore(codexHome string) *mcpCLIStore {
	return &mcpCLIStore{codexHome: codexHome}
}

func (s *mcpCLIStore) Load(overrides []string) (*mcpCLIConfig, error) {
	loaded, err := config.LoadEffective(s.codexHome, overrides, nil, nil)
	if err != nil {
		return nil, err
	}
	s.httpClient = codexnetwork.NewHTTPClient(loaded.RespectSystemProxyEnabled(), 0)
	return mcpCLIConfigFromValues(loaded.Values), nil
}

func (s *mcpCLIStore) LoadManaged(ctx context.Context, overrides []string) (*mcpCLIConfig, error) {
	bootstrap, err := config.LoadEffective(s.codexHome, overrides, nil, nil)
	if err != nil {
		return nil, err
	}
	s.httpClient = codexnetwork.NewHTTPClient(bootstrap.RespectSystemProxyEnabled(), 0)
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
	loader := config.NewCloudConfigLoader(func() (*config.CloudConfigBundle, error) {
		loadCtx, cancel := context.WithTimeout(contextOrBackground(ctx), 15*time.Second)
		defer cancel()
		return config.LoadCloudConfigBundle(loadCtx, config.CloudConfigFetchOptions{
			CodexHome:     s.codexHome,
			BaseURL:       bootstrap.ChatGPTBaseURL(),
			ChatGPTUserID: auth.ChatGPTUserIDFromAuth(snapshot),
			AccountID:     auth.AccountIDFromAuthForRestrictions(snapshot),
			HTTPClient:    s.httpClient,
			Authorize: func(requestCtx context.Context, request *http.Request) error {
				return authHeaders.Apply(requestCtx, request, nil)
			},
		})
	})
	loaded, err := config.LoadEffectiveWithOptions(s.codexHome, &config.EffectiveOptions{
		RawOverrides:         append([]string(nil), overrides...),
		IncludeManagedConfig: true,
		ManagedConfigPath:    filepath.Join(s.codexHome, "managed_config.toml"),
		CloudConfigBundle:    loader,
	})
	if err != nil {
		return nil, err
	}
	s.httpClient = codexnetwork.NewHTTPClient(loaded.RespectSystemProxyEnabled(), 0)
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
	cfg, err := s.Load(overrides)
	if err != nil {
		return false, err
	}
	_, existed := cfg.Servers[name]
	if existed {
		delete(cfg.Servers, name)
	}
	return existed, s.write(cfg)
}

func (s *mcpCLIStore) write(cfg *mcpCLIConfig) error {
	service := config.NewConfigService(s.codexHome)
	value := map[string]any{}
	for _, name := range sortedMCPServerNames(cfg.Servers) {
		value[name] = mcpServerToConfigValue(cfg.Servers[name])
	}
	_, err := service.WriteValue(&config.ConfigValueWriteParams{
		KeyPath:       "mcp_servers",
		Value:         value,
		MergeStrategy: config.MergeReplace,
	})
	return err
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
	server.SupportsParallelToolCalls = boolFromAny(table["supports_parallel_tool_calls"])
	server.Auth = stringFromAny(table["auth"])
	server.EnvironmentID = stringFromAny(table["environment_id"])
	server.StartupTimeoutSec = floatPtrFromAny(table["startup_timeout_sec"])
	server.ToolTimeoutSec = floatPtrFromAny(table["tool_timeout_sec"])
	server.DefaultToolsApprovalMode = stringFromAny(table["default_tools_approval_mode"])
	server.EnabledTools = stringSliceFromAny(table["enabled_tools"])
	server.DisabledTools = stringSliceFromAny(table["disabled_tools"])
	server.Scopes = stringSliceFromAny(table["scopes"])
	server.Tools = nestedAnyMapFromAny(table["tools"])
	if url := stringFromAny(table["url"]); url != "" {
		server.Type = "streamable_http"
		server.URL = url
		server.BearerTokenEnvVar = stringFromAny(table["bearer_token_env_var"])
		server.HTTPHeaders = stringMapFromAny(table["http_headers"])
		server.EnvHTTPHeaders = stringMapFromAny(table["env_http_headers"])
		server.OAuthClientID = mcpOAuthClientIDFromConfig(table)
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

func mcpServerToConfigValue(server *mcpCLIServer) map[string]any {
	value := map[string]any{}
	if server.Type == "streamable_http" {
		value["url"] = server.URL
		if server.BearerTokenEnvVar != "" {
			value["bearer_token_env_var"] = server.BearerTokenEnvVar
		}
		if len(server.HTTPHeaders) > 0 {
			value["http_headers"] = stringMapToAnyMap(server.HTTPHeaders)
		}
		if len(server.EnvHTTPHeaders) > 0 {
			value["env_http_headers"] = stringMapToAnyMap(server.EnvHTTPHeaders)
		}
		if server.OAuthClientID != "" {
			value["oauth"] = map[string]any{"client_id": server.OAuthClientID}
		}
		if server.OAuthResource != "" {
			value["oauth_resource"] = server.OAuthResource
		}
		mcpServerAddSharedConfigValue(value, server)
		return value
	}
	value["command"] = server.Command
	if len(server.Args) > 0 {
		value["args"] = stringSliceToAnySlice(server.Args)
	}
	if len(server.Env) > 0 {
		value["env"] = stringMapToAnyMap(server.Env)
	}
	if len(server.EnvVars) > 0 {
		value["env_vars"] = mcpEnvVarsConfigValue(server.EnvVars)
	}
	if server.CWD != "" {
		value["cwd"] = server.CWD
	}
	mcpServerAddSharedConfigValue(value, server)
	return value
}

func mcpServerAddSharedConfigValue(value map[string]any, server *mcpCLIServer) {
	if !server.Enabled {
		value["enabled"] = false
	}
	if server.Auth != "" && server.Auth != "oauth" {
		value["auth"] = server.Auth
	}
	if server.EnvironmentID != "" && server.EnvironmentID != "local" {
		value["environment_id"] = server.EnvironmentID
	}
	if server.Required {
		value["required"] = true
	}
	if server.SupportsParallelToolCalls {
		value["supports_parallel_tool_calls"] = true
	}
	if server.StartupTimeoutSec != nil {
		value["startup_timeout_sec"] = *server.StartupTimeoutSec
	}
	if server.ToolTimeoutSec != nil {
		value["tool_timeout_sec"] = *server.ToolTimeoutSec
	}
	if server.DefaultToolsApprovalMode != "" {
		value["default_tools_approval_mode"] = server.DefaultToolsApprovalMode
	}
	if len(server.EnabledTools) > 0 {
		value["enabled_tools"] = stringSliceToAnySlice(server.EnabledTools)
	}
	if len(server.DisabledTools) > 0 {
		value["disabled_tools"] = stringSliceToAnySlice(server.DisabledTools)
	}
	if len(server.Scopes) > 0 {
		value["scopes"] = stringSliceToAnySlice(server.Scopes)
	}
	if len(server.Tools) > 0 {
		value["tools"] = nestedAnyMapToAnyMap(server.Tools)
	}
}

func mcpOAuthClientIDFromConfig(table map[string]any) string {
	if value := stringFromAny(table["oauth_client_id"]); value != "" {
		return value
	}
	oauth, ok := table["oauth"].(map[string]any)
	if !ok {
		return ""
	}
	return stringFromAny(oauth["client_id"])
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
