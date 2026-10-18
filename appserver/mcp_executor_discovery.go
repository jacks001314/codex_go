package appserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"codex_go/config"
	execserverclient "codex_go/execserver"
	"codex_go/mcp"
	"codex_go/turn"

	"github.com/pelletier/go-toml/v2"
)

const executorMCPDiscoveryTimeout = 10 * time.Second

type executorMCPDiscoveryResult struct {
	key     string
	servers []executorMCPServer
}

type executorMCPServer struct {
	name   string
	config mcp.ServerConfig
}

// discoverExecutorHTTPMCPServers reads executor-owned MCP configuration from
// each selected remote environment. Discovery is best effort and never makes an
// executor-local server required at startup (Rust #39941).
func (r *RuntimeRouter) discoverExecutorHTTPMCPServers(ctx context.Context, threadID string, params *turn.TurnStartParams, cache *runtimeCapabilityDiscoveryV2Cache) {
	if r == nil || r.services.Environment == nil || cache == nil || strings.TrimSpace(threadID) == "" {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	selections := mcpRuntimeEnvironmentSelections(r, threadID, params)
	for _, selection := range selections {
		environmentID := selectionEnvironmentID(selection)
		if environmentID == "" || environmentID == execserverclient.LocalEnvironmentID {
			continue
		}
		if state, err := environmentConfigStateFromAnyMap(selection); err == nil &&
			(state.Kind == EnvironmentConfigPending || state.Kind == EnvironmentConfigFailed) {
			continue
		}
		record, ok := r.services.Environment.Record(environmentID)
		if !ok || record == nil {
			continue
		}
		cwd := executorMCPDiscoveryCWD(selection, *record)
		if !strings.HasPrefix(cwd, "file://") {
			continue
		}
		key, err := executorMCPDiscoveryKey(*record, cwd, selection)
		if err != nil {
			continue
		}
		if cache.hasExecutorMCPDiscovery(environmentID, key) {
			continue
		}
		if info, ok := r.services.Environment.CachedInfo(environmentID); ok && info != nil && !info.Capabilities.EnvironmentConfigRead {
			continue
		}
		client, err := cache.client(ctx, *record)
		if err != nil {
			slog.Warn("failed to connect for executor MCP discovery", "environment_id", environmentID, "error", err)
			continue
		}
		capabilities, err := client.EnvironmentCapabilities(ctx)
		if err != nil || capabilities == nil || !capabilities.EnvironmentConfigRead {
			continue
		}
		response, err := client.ReadEnvironmentConfigWithTimeout(ctx, &execserverclient.EnvironmentConfigReadParams{
			CWD:               cwd,
			ConfigPaths:       [][]string{{"mcp_servers"}},
			RequirementsPaths: [][]string{{"mcp_servers"}},
		}, executorMCPDiscoveryTimeout)
		if err != nil {
			slog.Warn("failed to read executor-local MCP configuration", "environment_id", environmentID, "error", err)
			continue
		}
		servers, err := executorHTTPMCPServers(response, environmentID, executorHTTPDoer{client: client})
		if err != nil {
			slog.Warn("failed to parse executor-local MCP configuration", "environment_id", environmentID, "error", err)
			continue
		}
		cache.storeExecutorMCPDiscovery(environmentID, executorMCPDiscoveryResult{key: key, servers: servers})
	}
}

func mcpRuntimeEnvironmentSelections(r *RuntimeRouter, threadID string, params *turn.TurnStartParams) []map[string]any {
	if params != nil && params.Environments != nil {
		return params.Environments
	}
	if r == nil {
		return nil
	}
	return r.threadEnvironmentSelections(threadID)
}

func executorMCPDiscoveryCWD(selection map[string]any, record EnvironmentRecord) string {
	if cwd := environmentSelectionCWD(selection); cwd != "" {
		return cwd
	}
	if record.CWD != nil {
		return strings.TrimSpace(*record.CWD)
	}
	return ""
}

func executorMCPDiscoveryKey(record EnvironmentRecord, cwd string, selection map[string]any) (string, error) {
	selectionJSON, err := json.Marshal(selection)
	if err != nil {
		return "", err
	}
	return strings.Join([]string{
		strings.TrimSpace(record.InstanceID),
		strings.TrimSpace(record.EnvironmentID),
		strings.TrimSpace(cwd),
		string(selectionJSON),
	}, "\x00"), nil
}

func (s *runtimeCapabilityDiscoveryV2Cache) hasExecutorMCPDiscovery(environmentID, key string) bool {
	if s == nil {
		return false
	}
	s.executorMCPMu.Lock()
	defer s.executorMCPMu.Unlock()
	result, ok := s.executorMCPDiscovery[environmentID]
	return ok && result.key == key
}

func (s *runtimeCapabilityDiscoveryV2Cache) storeExecutorMCPDiscovery(environmentID string, result executorMCPDiscoveryResult) {
	if s == nil {
		return
	}
	s.executorMCPMu.Lock()
	defer s.executorMCPMu.Unlock()
	if s.executorMCPDiscovery == nil {
		s.executorMCPDiscovery = map[string]executorMCPDiscoveryResult{}
	}
	s.executorMCPDiscovery[environmentID] = result
}

// applyExecutorMCPServers projects discovered executor declarations into the
// thread runtime. Existing non-plugin servers and servers owned by another
// environment win, while a same-environment plugin registration may be
// supplemented by the executor's effective config (Rust #39941).
func (s *runtimeCapabilityDiscoveryV2Cache) applyExecutorMCPServers(router *RuntimeRouter, runtimeConfig *mcp.RuntimeConfig, selections []map[string]any, requirements *config.ConfigRequirements) {
	if s == nil || runtimeConfig == nil {
		return
	}
	if runtimeConfig.Servers == nil {
		runtimeConfig.Servers = map[string]mcp.ServerRegistration{}
	}
	registered := map[string]struct{}{}
	for _, selection := range selections {
		environmentID := selectionEnvironmentID(selection)
		if environmentID == "" || environmentID == execserverclient.LocalEnvironmentID {
			continue
		}
		if router == nil || router.services.Environment == nil {
			continue
		}
		record, ok := router.services.Environment.Record(environmentID)
		if !ok || record == nil {
			continue
		}
		key, err := executorMCPDiscoveryKey(*record, executorMCPDiscoveryCWD(selection, *record), selection)
		if err != nil {
			continue
		}
		s.executorMCPMu.Lock()
		result, ok := s.executorMCPDiscovery[environmentID]
		s.executorMCPMu.Unlock()
		if !ok || result.key != key {
			continue
		}
		for _, discovered := range result.servers {
			name := strings.TrimSpace(discovered.name)
			if name == "" || name == mcp.CodexAppsServerName {
				continue
			}
			if _, duplicate := registered[name]; duplicate {
				continue
			}
			if existing, ok := runtimeConfig.Servers[name]; ok {
				source := mcp.SourceFromRegistration(&existing)
				if existing.Config.EffectiveEnvironmentID() != environmentID ||
					(source != mcp.CatalogSourcePlugin && source != mcp.CatalogSourceSelectedPlugin) {
					continue
				}
			}
			serverConfig := discovered.config
			serverConfig.EnvironmentID = environmentID
			serverConfig.Required = false
			serverConfig.CredentialPolicy = mcp.CredentialPolicyExecutorOnly
			applyExecutorMCPRequirements(name, &serverConfig, requirements)
			runtimeConfig.Servers[name] = mcp.ServerRegistration{
				Name:             name,
				Config:           serverConfig,
				Source:           string(mcp.CatalogSourceConfig),
				CredentialPolicy: mcp.CredentialPolicyExecutorOnly,
			}
			registered[name] = struct{}{}
		}
	}
}

func executorHTTPMCPServers(response *execserverclient.EnvironmentConfigReadResponse, environmentID string, httpClient executorHTTPDoer) ([]executorMCPServer, error) {
	if response == nil {
		return nil, nil
	}
	configValues, err := mergeExecutorTOMLayers(response.Config.Layers)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	requirementValues, err := mergeExecutorTOMLayers(response.Requirements.Layers)
	if err != nil {
		return nil, fmt.Errorf("requirements: %w", err)
	}
	hostname := ""
	if response.Hostname != nil {
		hostname = strings.TrimSpace(*response.Hostname)
	}
	requirements, err := config.ConfigRequirementsFromMapWithHostname(requirementValues, hostname)
	if err != nil {
		return nil, fmt.Errorf("requirements: %w", err)
	}
	rawServers, _ := configValues["mcp_servers"].(map[string]any)
	servers := make([]executorMCPServer, 0, len(rawServers))
	for name, raw := range rawServers {
		name = strings.TrimSpace(name)
		table, ok := raw.(map[string]any)
		if name == "" || !ok {
			continue
		}
		serverConfig := mcp.ServerConfigFromValues(table)
		if serverConfig == nil || strings.TrimSpace(serverConfig.URL) == "" || strings.TrimSpace(serverConfig.Command) != "" {
			continue
		}
		// Stdio is executor-local and is intentionally not projectable. Generic
		// HTTP environment headers and helper commands remain unsupported; bearer
		// environment references are resolved by the executor per request.
		if !serverConfig.IsLocalEnvironment() ||
			strings.TrimSpace(serverConfig.HTTPHeadersHelper) != "" ||
			len(serverConfig.EnvHTTPHeaders) > 0 {
			continue
		}
		serverConfig.EnvironmentID = environmentID
		serverConfig.Required = false
		serverConfig.CredentialPolicy = mcp.CredentialPolicyExecutorOnly
		httpClient.bearerTokenEnvVar = serverConfig.BearerTokenEnvVar
		serverConfig.HTTPClient = httpClient
		applyExecutorMCPRequirements(name, serverConfig, requirements)
		servers = append(servers, executorMCPServer{name: name, config: *serverConfig})
	}
	return servers, nil
}

func applyExecutorMCPRequirements(name string, serverConfig *mcp.ServerConfig, requirements *config.ConfigRequirements) {
	if serverConfig == nil || requirements == nil || requirements.MCPServers == nil {
		return
	}
	requirement, ok := requirements.MCPServers[strings.TrimSpace(name)]
	if !ok || !requirement.Matches(strings.TrimSpace(serverConfig.Command), serverConfig.Args, strings.TrimSpace(serverConfig.URL)) {
		serverConfig.Enabled = false
		serverConfig.DisabledReason = config.MCPDisabledByRequirements
	}
}

func mergeExecutorTOMLayers(layers []execserverclient.EnvironmentConfigLayer) (map[string]any, error) {
	merged := map[string]any{}
	for _, layer := range layers {
		if strings.TrimSpace(layer.TOML) == "" {
			continue
		}
		values := map[string]any{}
		if err := toml.Unmarshal([]byte(layer.TOML), &values); err != nil {
			return nil, err
		}
		mergeExecutorTOMLValues(merged, values)
	}
	return merged, nil
}

func mergeExecutorTOMLValues(dst, src map[string]any) {
	for key, value := range src {
		if sourceTable, ok := value.(map[string]any); ok {
			if destinationTable, ok := dst[key].(map[string]any); ok {
				mergeExecutorTOMLValues(destinationTable, sourceTable)
				continue
			}
			destinationTable := make(map[string]any, len(sourceTable))
			mergeExecutorTOMLValues(destinationTable, sourceTable)
			dst[key] = destinationTable
			continue
		}
		dst[key] = value
	}
}

type executorHTTPDoer struct {
	client            *execserverclient.Client
	bearerTokenEnvVar string
}

func (d executorHTTPDoer) Do(request *http.Request) (*http.Response, error) {
	if d.client == nil {
		return nil, fmt.Errorf("executor HTTP client is unavailable")
	}
	if request == nil {
		return nil, fmt.Errorf("HTTP request is required")
	}
	if request.URL == nil {
		return nil, fmt.Errorf("HTTP request URL is required")
	}
	var body []byte
	var err error
	if request.Body != nil {
		body, err = io.ReadAll(request.Body)
		_ = request.Body.Close()
		if err != nil {
			return nil, err
		}
	}
	headers := make([]execserverclient.HTTPHeader, 0, len(request.Header))
	for name, values := range request.Header {
		if d.bearerTokenEnvVar != "" && strings.EqualFold(name, "Authorization") {
			continue
		}
		for _, value := range values {
			headers = append(headers, execserverclient.HTTPHeader{Name: name, Value: value})
		}
	}
	if d.bearerTokenEnvVar != "" {
		envVar := d.bearerTokenEnvVar
		headers = append(headers, execserverclient.HTTPHeader{
			Name:        "Authorization",
			Value:       "Bearer ",
			ValueEnvVar: &envVar,
		})
	}
	params := &execserverclient.HTTPRequestParams{
		Method:         request.Method,
		URL:            request.URL.String(),
		Headers:        headers,
		RedirectPolicy: "stop",
		StreamResponse: true,
	}
	if len(body) > 0 {
		encoded := base64.StdEncoding.EncodeToString(body)
		params.BodyBase64 = &encoded
	}
	response, stream, err := d.client.HTTPRequest(request.Context(), params)
	if err != nil {
		return nil, err
	}
	if response == nil || stream == nil {
		return nil, fmt.Errorf("executor HTTP request returned no response stream")
	}
	headersOut := make(http.Header, len(response.Headers))
	for _, header := range response.Headers {
		headersOut.Add(header.Name, header.Value)
	}
	return &http.Response{
		Status:        fmt.Sprintf("%d %s", response.Status, http.StatusText(response.Status)),
		StatusCode:    response.Status,
		Header:        headersOut,
		Body:          &executorHTTPBody{ctx: request.Context(), stream: stream},
		ContentLength: -1,
		Request:       request,
	}, nil
}

type executorHTTPBody struct {
	ctx     context.Context
	stream  *execserverclient.HTTPBodyStream
	pending []byte
	done    bool
}

func (b *executorHTTPBody) Read(target []byte) (int, error) {
	if b == nil || b.stream == nil {
		return 0, io.EOF
	}
	for len(b.pending) == 0 {
		if b.done {
			return 0, io.EOF
		}
		data, done, err := b.stream.Next(b.ctx)
		if err != nil {
			return 0, err
		}
		b.pending = data
		b.done = done
		if len(data) == 0 && done {
			return 0, io.EOF
		}
	}
	n := copy(target, b.pending)
	b.pending = b.pending[n:]
	return n, nil
}

func (b *executorHTTPBody) Close() error {
	if b != nil && b.stream != nil {
		b.stream.Close()
	}
	return nil
}

var _ io.ReadCloser = (*executorHTTPBody)(nil)
var _ mcp.HTTPDoer = executorHTTPDoer{}
