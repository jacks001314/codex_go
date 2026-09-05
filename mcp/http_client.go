package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/oauth2"
)

const (
	mcpHTTPProtocolVersionHeader = "MCP-Protocol-Version"
	mcpHTTPSessionIDHeader       = "Mcp-Session-Id"
	mcpJSONRPCInternalErrorCode  = int64(-32603)
)

var mcpStreamableHTTPRetryDelays = []time.Duration{250 * time.Millisecond, time.Second}

type httpRPCResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      int64            `json:"id,omitempty"`
	Result  *json.RawMessage `json:"result,omitempty"`
	Error   *stdioRPCError   `json:"error,omitempty"`
}

// mcpDeadlineResponseBody keeps a handshake deadline context alive until the
// caller consumes the response body. Cancelling the context inside doRPC
// (before initialize/discover read the JSON body) surfaces spurious
// "context canceled" failures whenever the server does not deliver the body in
// the same packet as the headers.
type mcpDeadlineResponseBody struct {
	io.ReadCloser
	cancelOnce sync.Once
	cancel     context.CancelFunc
}

func (b *mcpDeadlineResponseBody) Close() error {
	if b == nil {
		return nil
	}
	b.cancelOnce.Do(b.cancel)
	if b.ReadCloser == nil {
		return nil
	}
	return b.ReadCloser.Close()
}

type mcpHTTPStatusError struct {
	Method     string
	Status     string
	StatusCode int
	Detail     string
	// WWWAuthenticate carries the challenge header values returned with a 401
	// Unauthorized response, so callers can surface interactive login hints
	// (Rust #42552).
	WWWAuthenticate []string
}

func (e *mcpHTTPStatusError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Detail) != "" {
		return fmt.Sprintf("MCP HTTP %s failed: %s: %s", e.Method, e.Status, strings.TrimSpace(e.Detail))
	}
	return fmt.Sprintf("MCP HTTP %s failed: %s", e.Method, e.Status)
}

func (e *mcpHTTPStatusError) IsStatus(statusCode int) bool {
	return e != nil && e.StatusCode == statusCode
}

func (e *mcpHTTPStatusError) CombinedWWWAuthenticate() string {
	if e == nil {
		return ""
	}
	return strings.Join(e.WWWAuthenticate, ", ")
}

type httpClient struct {
	config                             *ServerConfig
	client                             *http.Client
	mu                                 sync.Mutex
	closed                             bool
	nextID                             atomic.Int64
	initialized                        bool
	sessionID                          string
	serverName                         string
	threadID                           string
	turnID                             string
	itemID                             string
	roots                              []MCPRoot
	elicitation                        MCPElicitationHandler
	progress                           MCPProgressHandler
	openAIForm                         bool
	protocolMode                       MCPProtocolMode
	negotiatedProtocolVersion          string
	retrySleep                         func(time.Duration)
	supportsSandboxStateMetaCapability bool
	initializeDeadline                 time.Time
	initializeDeadlineOn               bool
}

type httpClientCallOptions struct {
	ServerName  string
	ThreadID    string
	TurnID      string
	ItemID      string
	Roots       []MCPRoot
	Elicitation MCPElicitationHandler
	Progress    MCPProgressHandler
}

type mcpOAuthHeaderTransport struct {
	base           http.RoundTripper
	httpHeaders    map[string]string
	envHTTPHeaders map[string]string
}

func (t *mcpOAuthHeaderTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	cloned := request.Clone(request.Context())
	cloned.Header = request.Header.Clone()
	defaults := http.Header{"User-Agent": []string{mcpUserAgent()}}
	applyMCPHTTPHeaders(&http.Request{Header: defaults}, t.httpHeaders, t.envHTTPHeaders)
	for name, values := range defaults {
		if httpHeaderContainsKey(cloned.Header, name) {
			continue
		}
		cloned.Header[name] = append([]string(nil), values...)
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(cloned)
}

func httpHeaderContainsKey(headers http.Header, target string) bool {
	for name := range headers {
		if strings.EqualFold(name, target) {
			return true
		}
	}
	return false
}

func listMCPInventory(config *ServerConfig) (*stdioInventory, error) {
	if strings.TrimSpace(config.URL) != "" {
		return listMCPHTTPInventory(config)
	}
	if strings.TrimSpace(config.Command) != "" {
		return listMCPStdioInventory(config)
	}
	return &stdioInventory{}, nil
}

func isRunnableMCPConfig(config *ServerConfig) bool {
	return config != nil && (strings.TrimSpace(config.Command) != "" || strings.TrimSpace(config.URL) != "")
}

func listMCPHTTPInventory(config *ServerConfig) (*stdioInventory, error) {
	client := newMCPHTTPClient(config)
	return listMCPHTTPInventoryWithClient(client)
}

func listMCPHTTPInventoryWithClient(client *httpClient) (*stdioInventory, error) {
	return listMCPHTTPInventoryWithOptions(client, "", "", nil)
}

func listMCPHTTPInventoryWithOptions(client *httpClient, serverName string, threadID string, roots []MCPRoot) (*stdioInventory, error) {
	result := &stdioInventory{}
	options := &httpClientCallOptions{ServerName: serverName, ThreadID: threadID, Roots: roots}
	tools, err := listMCPHTTPTools(client, options)
	if err != nil {
		return nil, err
	}
	result.Tools = tools
	if resources, err := listMCPHTTPResources(client, options); err == nil {
		result.Resources = resources
	}
	if templates, err := listMCPHTTPResourceTemplates(client, options); err == nil {
		result.ResourceTemplates = templates
	}
	return result, nil
}

func listMCPHTTPTools(client *httpClient, options *httpClientCallOptions) ([]MCPToolInfo, error) {
	return collectMCPPaginatedWithLimit(context.Background(), "tools/list", mcpPaginationTimeout(client.config), mcpCatalogItemLimit(client.config), func(ctx context.Context, cursor *string) ([]MCPToolInfo, *string, error) {
		var response struct {
			Tools      []MCPToolInfo `json:"tools"`
			NextCursor *string       `json:"nextCursor,omitempty"`
		}
		if err := client.CallWithOptionsContext(ctx, options, "tools/list", mcpListParamsForCursor(cursor), &response); err != nil {
			return nil, nil, err
		}
		return response.Tools, response.NextCursor, nil
	})
}

func listMCPHTTPResources(client *httpClient, options *httpClientCallOptions) ([]MCPResource, error) {
	return collectMCPPaginatedWithLimit(context.Background(), "resources/list", mcpPaginationTimeout(client.config), mcpCatalogItemLimit(client.config), func(ctx context.Context, cursor *string) ([]MCPResource, *string, error) {
		var response struct {
			Resources  []MCPResource `json:"resources"`
			NextCursor *string       `json:"nextCursor,omitempty"`
		}
		if err := client.CallWithOptionsContext(ctx, options, "resources/list", mcpListParamsForCursor(cursor), &response); err != nil {
			return nil, nil, err
		}
		return response.Resources, response.NextCursor, nil
	})
}

func listMCPHTTPResourceTemplates(client *httpClient, options *httpClientCallOptions) ([]MCPResourceTemplate, error) {
	return collectMCPPaginatedWithLimit(context.Background(), "resources/templates/list", mcpPaginationTimeout(client.config), mcpCatalogItemLimit(client.config), func(ctx context.Context, cursor *string) ([]MCPResourceTemplate, *string, error) {
		var response struct {
			ResourceTemplates []MCPResourceTemplate `json:"resourceTemplates"`
			NextCursor        *string               `json:"nextCursor,omitempty"`
		}
		if err := client.CallWithOptionsContext(ctx, options, "resources/templates/list", mcpListParamsForCursor(cursor), &response); err != nil {
			return nil, nil, err
		}
		return response.ResourceTemplates, response.NextCursor, nil
	})
}

func callMCPHTTPTool(config *ServerConfig, serverName string, threadID string, elicitation MCPElicitationHandler, progress MCPProgressHandler, tool string, arguments any, meta any) (*MCPToolCallResponse, error) {
	client := newMCPHTTPClient(config)
	return callMCPHTTPToolWithClient(client, serverName, threadID, "", "", nil, elicitation, progress, tool, arguments, meta)
}

func callMCPHTTPToolWithClient(client *httpClient, serverName string, threadID string, turnID string, itemID string, roots []MCPRoot, elicitation MCPElicitationHandler, progress MCPProgressHandler, tool string, arguments any, meta any) (*MCPToolCallResponse, error) {
	params := map[string]any{
		"name":      tool,
		"arguments": arguments,
	}
	if meta != nil {
		params["_meta"] = meta
	}
	var response MCPToolCallResponse
	if err := client.CallWithOptions(&httpClientCallOptions{
		ServerName:  serverName,
		ThreadID:    threadID,
		TurnID:      turnID,
		ItemID:      itemID,
		Roots:       roots,
		Elicitation: elicitation,
		Progress:    progress,
	}, "tools/call", params, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func readMCPHTTPResource(config *ServerConfig, serverName string, elicitation MCPElicitationHandler, progress MCPProgressHandler, params *MCPResourceReadParams) (*MCPResourceReadResponse, error) {
	client := newMCPHTTPClient(config)
	return readMCPHTTPResourceWithClient(client, serverName, nil, elicitation, progress, params)
}

func readMCPHTTPResourceWithClient(client *httpClient, serverName string, roots []MCPRoot, elicitation MCPElicitationHandler, progress MCPProgressHandler, params *MCPResourceReadParams) (*MCPResourceReadResponse, error) {
	if params == nil || strings.TrimSpace(params.URI) == "" {
		return nil, invalidMCPRequest("server and uri are required")
	}
	threadID := ""
	if params != nil && params.ThreadID != nil {
		threadID = strings.TrimSpace(*params.ThreadID)
	}
	var response MCPResourceReadResponse
	if err := client.CallWithOptions(&httpClientCallOptions{
		ServerName:  serverName,
		ThreadID:    threadID,
		Roots:       roots,
		Elicitation: elicitation,
		Progress:    progress,
	}, "resources/read", map[string]any{"uri": strings.TrimSpace(params.URI)}, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func newMCPHTTPClient(config *ServerConfig) *httpClient {
	return newMCPHTTPClientWithOpenAIForm(config, false)
}

func newMCPHTTPClientWithOpenAIForm(config *ServerConfig, openAIForm bool) *httpClient {
	return newMCPHTTPClientWithShared(config, openAIForm, nil)
}

func newMCPHTTPClientWithShared(config *ServerConfig, openAIForm bool, shared HTTPDoer) *httpClient {
	client := mcpHTTPClientFromShared(shared, mcpClientTimeout(config))
	if config != nil {
		if policy := mcpRedirectPolicy(config.URL); policy != nil {
			client.CheckRedirect = policy
		}
	}
	if config != nil && strings.TrimSpace(config.URL) != "" && strings.TrimSpace(config.HTTPHeadersHelper) != "" {
		cwd, _ := os.Getwd()
		helperTransport, helperErr := newMCPHTTPHeadersHelperTransport(client.Transport, config.URL, config.HTTPHeadersHelper, cwd)
		if helperErr == nil {
			client.Transport = helperTransport
			client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		}
	}
	protocolMode := MCPProtocolLegacy
	if config != nil {
		protocolMode = config.ProtocolMode
	}
	return &httpClient{
		config:       config,
		client:       mcpHTTPClientWithDefaultHeaders(client, config),
		openAIForm:   openAIForm,
		protocolMode: protocolMode,
		retrySleep:   time.Sleep,
	}
}

// mcpRedirectPolicy returns an http.Client CheckRedirect that follows only
// same-origin redirects (every hop stays on the configured server's origin) and
// requires HTTPS for redirects on non-loopback hostnames (Rust #39046). A nil
// policy (unparseable or empty configured URL) leaves the default behavior.
func mcpRedirectPolicy(configuredURL string) func(*http.Request, []*http.Request) error {
	origin, err := url.Parse(strings.TrimSpace(configuredURL))
	if err != nil || origin.Scheme == "" || origin.Host == "" {
		return nil
	}
	return func(request *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("mcp: stopped after 10 redirects")
		}
		if !sameMCPRedirectOrigin(request.URL, origin) {
			return fmt.Errorf("mcp: redirect to a different origin rejected: %s", request.URL)
		}
		if !loopbackMCPHost(request.URL.Hostname()) && !strings.EqualFold(request.URL.Scheme, "https") {
			return fmt.Errorf("mcp: insecure redirect rejected: %s", request.URL)
		}
		return nil
	}
}

func sameMCPRedirectOrigin(target *url.URL, origin *url.URL) bool {
	return strings.EqualFold(target.Scheme, origin.Scheme) &&
		strings.EqualFold(target.Hostname(), origin.Hostname()) &&
		effectiveMCPPort(target) == effectiveMCPPort(origin)
}

func effectiveMCPPort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

func loopbackMCPHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "localhost" || host == "localhost." {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

type httpDoerRoundTripper struct {
	doer HTTPDoer
}

func (t httpDoerRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return t.doer.Do(request)
}

func mcpHTTPClientFromShared(shared HTTPDoer, timeout time.Duration) *http.Client {
	if client, ok := shared.(*http.Client); ok && client != nil {
		cloned := *client
		cloned.Timeout = timeout
		return &cloned
	}
	if shared != nil {
		return &http.Client{Timeout: timeout, Transport: httpDoerRoundTripper{doer: shared}}
	}
	return &http.Client{Timeout: timeout}
}

// oauthHTTPClient reuses the final MCP runtime client's routing, proxy, TLS,
// cookie, and redirect behavior while keeping OAuth request timeouts isolated
// from the active streamable HTTP session. Configured server headers are
// defaults: OAuth-specific request headers win when both provide the same key.
func (c *httpClient) oauthHTTPClient(timeout time.Duration) *http.Client {
	if c == nil || c.client == nil {
		client := *http.DefaultClient
		if timeout > 0 {
			client.Timeout = timeout
		}
		return &client
	}
	client := *c.client
	if timeout > 0 {
		client.Timeout = timeout
	}
	var httpHeaders map[string]string
	var envHTTPHeaders map[string]string
	if c.config != nil {
		httpHeaders = cloneStringMap(c.config.HTTPHeaders)
		envHTTPHeaders = cloneStringMap(c.config.EnvHTTPHeaders)
	}
	base := c.client.Transport
	if headerTransport, ok := base.(*mcpHeaderTransport); ok {
		base = headerTransport.base
	}
	client.Transport = &mcpOAuthHeaderTransport{
		base:           base,
		httpHeaders:    httpHeaders,
		envHTTPHeaders: envHTTPHeaders,
	}
	return &client
}

func mcpClientTimeout(config *ServerConfig) time.Duration {
	if config != nil {
		if config.ToolTimeout > 0 {
			return config.ToolTimeout
		}
		if config.StartupTimeout > 0 {
			return config.StartupTimeout
		}
	}
	return 15 * time.Second
}

func (c *httpClient) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	sessionID := strings.TrimSpace(c.sessionID)
	c.sessionID = ""
	c.initialized = false
	c.closed = true
	c.mu.Unlock()
	if sessionID == "" {
		return nil
	}
	return c.deleteSession(sessionID)
}

func (c *httpClient) isClosed() bool {
	if c == nil {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *httpClient) deleteSession(sessionID string) error {
	if c == nil || c.config == nil {
		return nil
	}
	endpoint := strings.TrimSpace(c.config.URL)
	if endpoint == "" {
		return nil
	}
	request, err := http.NewRequestWithContext(context.Background(), http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	c.applyConfiguredHTTPHeaders(request)
	request.Header.Set("Accept", "application/json")
	request.Header.Set(mcpHTTPProtocolVersionHeader, c.effectiveProtocolVersion())
	request.Header.Set(mcpHTTPSessionIDHeader, strings.TrimSpace(sessionID))
	if token := c.bearerToken(); token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if c.config.ApplyHTTPRequest != nil && strings.TrimSpace(c.config.BearerTokenEnvVar) == "" {
		if err := c.config.ApplyHTTPRequest(request, nil); err != nil {
			return err
		}
	}
	response, err := c.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusGone {
		return nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		detail := strings.TrimSpace(string(body))
		if detail != "" {
			return fmt.Errorf("MCP HTTP session delete failed: %s: %s", response.Status, detail)
		}
		return fmt.Errorf("MCP HTTP session delete failed: %s", response.Status)
	}
	return nil
}

func (c *httpClient) Call(method string, params any, out any) error {
	return c.CallWithOptions(nil, method, params, out)
}

func (c *httpClient) CallWithOptions(options *httpClientCallOptions, method string, params any, out any) error {
	return c.CallWithOptionsContext(context.Background(), options, method, params, out)
}

func (c *httpClient) CallWithOptionsContext(ctx context.Context, options *httpClientCallOptions, method string, params any, out any) error {
	if c == nil {
		return errors.New("HTTP MCP client is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.applyCallOptions(options)
	if !c.initialized {
		if err := c.reinitialize(ctx); err != nil {
			return err
		}
	}
	err := c.callWithTransientRetries(ctx, method, params, out)
	if err == nil || !isMCPHTTPSessionInvalidError(err) || strings.TrimSpace(c.sessionID) == "" {
		return err
	}
	if resetErr := c.reinitialize(ctx); resetErr != nil {
		return resetErr
	}
	return c.callWithTransientRetries(ctx, method, params, out)
}

// CallStream performs a long-lived streamable HTTP request (Rust
// CancellableEventStreamRequest): every server notification envelope from the
// SSE stream is delivered to onNotification, and the call returns when the
// matching-id response arrives, the stream ends, or ctx is cancelled. The
// caller must consume notifications promptly or drop the stream.
func (c *httpClient) CallStream(ctx context.Context, options *httpClientCallOptions, method string, params any, onNotification func(envelope *stdioRPCEnvelope) error) error {
	if c == nil {
		return errors.New("HTTP MCP client is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.applyCallOptions(options)
	if !c.initialized {
		if err := c.reinitialize(ctx); err != nil {
			return err
		}
	}
	response, requestID, err := c.doRPC(ctx, method, params, c.sessionID, true)
	if err != nil {
		return err
	}
	rpc, readErr := c.readRPCResponseWithStreamHandler(response, requestID, c.sessionID, onNotification)
	_ = response.Body.Close()
	if readErr != nil {
		return readErr
	}
	if rpc != nil && rpc.Error != nil {
		return newMCPRemoteError(method, rpc.Error)
	}
	return nil
}

func (c *httpClient) readRPCResponseWithStreamHandler(response *http.Response, id int64, sessionID string, onNotification func(envelope *stdioRPCEnvelope) error) (*httpRPCResponse, error) {
	contentType := ""
	if response != nil {
		contentType = response.Header.Get("Content-Type")
	}
	if strings.Contains(strings.ToLower(contentType), "text/event-stream") {
		return readMCPHTTPSSEWithLimit(response.Body, id, 0, onNotification)
	}
	return c.readRPCResponse(response, id, sessionID)
}

func (c *httpClient) applyCallOptions(options *httpClientCallOptions) {
	c.serverName = ""
	c.threadID = ""
	c.turnID = ""
	c.itemID = ""
	c.roots = nil
	c.elicitation = nil
	c.progress = nil
	if options == nil {
		return
	}
	c.serverName = strings.TrimSpace(options.ServerName)
	c.threadID = strings.TrimSpace(options.ThreadID)
	c.turnID = strings.TrimSpace(options.TurnID)
	c.itemID = strings.TrimSpace(options.ItemID)
	c.roots = cloneMCPRoots(options.Roots)
	c.elicitation = options.Elicitation
	c.progress = options.Progress
}

func (c *httpClient) initialize(ctx context.Context) (string, error) {
	started := time.Now()
	response, requestID, err := c.doRPC(ctx, "initialize", mcpClientInitializeParams(c.openAIForm), "", true)
	if err != nil {
		recordProtocolDiscoveryMetrics(mcpProtocolDiscoveryModeLabel(c.protocolMode), "failure", time.Since(started))
		return "", err
	}
	defer response.Body.Close()
	rpc, err := c.readRPCResponse(response, requestID, response.Header.Get(mcpHTTPSessionIDHeader))
	if err != nil {
		recordProtocolDiscoveryMetrics(mcpProtocolDiscoveryModeLabel(c.protocolMode), "failure", time.Since(started))
		return "", err
	}
	if rpc.Error != nil {
		recordProtocolDiscoveryMetrics(mcpProtocolDiscoveryModeLabel(c.protocolMode), "failure", time.Since(started))
		return "", newMCPRemoteError("initialize", rpc.Error)
	}
	var initialized struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if rpc.Result != nil && json.Unmarshal(*rpc.Result, &initialized) == nil {
		c.negotiatedProtocolVersion = strings.TrimSpace(initialized.ProtocolVersion)
	}
	outcome := "legacy"
	if c.negotiatedProtocolVersion == modernMCPProtocol {
		outcome = "modern"
	}
	recordProtocolDiscoveryMetrics(mcpProtocolDiscoveryModeLabel(c.protocolMode), outcome, time.Since(started))
	c.supportsSandboxStateMetaCapability = checkSandboxStateMetaCapability(rpc.Result)
	return response.Header.Get(mcpHTTPSessionIDHeader), nil
}

func (c *httpClient) discover(ctx context.Context) (string, error) {
	started := time.Now()
	response, requestID, err := c.doRPC(ctx, "server/discover", map[string]any{}, "", true)
	if err != nil {
		recordProtocolDiscoveryMetrics("auto", "failure", time.Since(started))
		return "", err
	}
	defer response.Body.Close()
	rpc, err := c.readRPCResponse(response, requestID, response.Header.Get(mcpHTTPSessionIDHeader))
	if err != nil {
		recordProtocolDiscoveryMetrics("auto", "failure", time.Since(started))
		return "", err
	}
	if rpc.Error != nil {
		recordProtocolDiscoveryMetrics("auto", "failure", time.Since(started))
		return "", newMCPRemoteError("server/discover", rpc.Error)
	}
	if err := validateMCPModernDiscovery(rpc.Result); err != nil {
		recordProtocolDiscoveryMetrics("auto", "failure", time.Since(started))
		return "", err
	}
	recordProtocolDiscoveryMetrics("auto", "modern", time.Since(started))
	c.supportsSandboxStateMetaCapability = checkSandboxStateMetaCapability(rpc.Result)
	return response.Header.Get(mcpHTTPSessionIDHeader), nil
}

func (c *httpClient) notifyInitialized(ctx context.Context, sessionID string) error {
	response, _, err := c.doRPC(ctx, "notifications/initialized", map[string]any{}, sessionID, false)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	return err
}

func (c *httpClient) reinitialize(ctx context.Context) error {
	c.initialized = false
	c.sessionID = ""
	c.beginHandshake(ctx)
	defer c.endHandshake()
	for attempt := 0; ; attempt++ {
		c.protocolMode = MCPProtocolLegacy
		c.negotiatedProtocolVersion = ""
		if c.config != nil {
			c.protocolMode = c.config.ProtocolMode
		}
		var sessionID string
		var err error
		if c.protocolMode == MCPProtocol20260728 {
			sessionID, err = c.discover(ctx)
			if err == nil {
				c.sessionID = sessionID
				c.initialized = true
				return nil
			}
			if isMCPDiscoveryFallbackError(err) {
				c.protocolMode = MCPProtocolLegacy
				sessionID, err = c.initialize(ctx)
			}
		} else {
			sessionID, err = c.initialize(ctx)
		}
		if err == nil {
			c.sessionID = sessionID
			err = c.notifyInitialized(ctx, sessionID)
			if err == nil {
				c.initialized = true
				return nil
			}
		}
		c.sessionID = ""
		if attempt >= len(mcpStreamableHTTPRetryDelays) || !isRetryableMCPStreamableHTTPError(err) {
			return err
		}
		if err := c.sleepBeforeRetryContext(ctx, mcpStreamableHTTPRetryDelays[attempt]); err != nil {
			return err
		}
	}
}

// beginHandshake records the remaining initialization deadline so handshake
// HTTP requests (initialize, notifications/initialized, server/discover) stay
// bounded even when the endpoint stalls (Rust e244a9d94e, #37168).
func (c *httpClient) beginHandshake(ctx context.Context) {
	c.initializeDeadlineOn = false
	if ctx != nil {
		if deadline, ok := ctx.Deadline(); ok {
			c.initializeDeadline = deadline
			c.initializeDeadlineOn = true
			return
		}
	}
	if c.config != nil && c.config.StartupTimeout > 0 {
		c.initializeDeadline = time.Now().Add(c.config.StartupTimeout)
		c.initializeDeadlineOn = true
		return
	}
	c.initializeDeadline = time.Now().Add(15 * time.Second)
	c.initializeDeadlineOn = true
}

func (c *httpClient) endHandshake() {
	c.initializeDeadlineOn = false
}

func (c *httpClient) handshakeRequestTimeout(method string) time.Duration {
	if c == nil || !c.initializeDeadlineOn {
		return 0
	}
	switch method {
	case "initialize", "notifications/initialized", "server/discover":
	default:
		return 0
	}
	remaining := time.Until(c.initializeDeadline)
	if remaining <= 0 {
		return time.Millisecond
	}
	return remaining
}

func (c *httpClient) callWithTransientRetries(ctx context.Context, method string, params any, out any) error {
	for attempt := 0; ; attempt++ {
		err := c.callWithSession(ctx, method, params, out, c.sessionID)
		if err == nil {
			return nil
		}
		if method != "tools/list" || attempt >= len(mcpStreamableHTTPRetryDelays) || !isRetryableMCPStreamableHTTPError(err) {
			return err
		}
		if err := c.sleepBeforeRetryContext(ctx, mcpStreamableHTTPRetryDelays[attempt]); err != nil {
			return err
		}
	}
}

func (c *httpClient) sleepBeforeRetry(delay time.Duration) {
	_ = c.sleepBeforeRetryContext(context.Background(), delay)
}

func (c *httpClient) sleepBeforeRetryContext(ctx context.Context, delay time.Duration) error {
	if c.retrySleep != nil {
		c.retrySleep(delay)
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *httpClient) callWithSession(ctx context.Context, method string, params any, out any, sessionID string) error {
	baseParams := params
	requestParams := params
	for round := 0; ; round++ {
		response, requestID, err := c.doRPC(ctx, method, requestParams, sessionID, true)
		if err != nil {
			return err
		}
		rpc, readErr := c.readRPCResponse(response, requestID, sessionID)
		_ = response.Body.Close()
		if readErr != nil {
			return readErr
		}
		if rpc.Error != nil {
			return newMCPRemoteError(method, rpc.Error)
		}
		if c.protocolMode == MCPProtocol20260728 {
			requestCtx := contextWithMCPClientContextAndRoots(ctx, c.threadID, c.turnID, c.itemID, c.roots)
			nextParams, inputRequired, err := nextMCP2026RequestParams(requestCtx, c.serverName, c.elicitation, baseParams, rpc.Result)
			if err != nil {
				return err
			}
			if inputRequired {
				if round >= maxMCPInputRequiredRounds {
					return fmt.Errorf("MCP %s exceeded %d input-required rounds", method, maxMCPInputRequiredRounds)
				}
				requestParams = nextParams
				continue
			}
		}
		if out == nil || rpc.Result == nil {
			return nil
		}
		return json.Unmarshal(*rpc.Result, out)
	}
}

func (c *httpClient) doRPC(ctx context.Context, method string, params any, sessionID string, includeID bool) (*http.Response, int64, error) {
	if c == nil || c.config == nil {
		return nil, 0, errors.New("HTTP MCP client is nil")
	}
	endpoint := strings.TrimSpace(c.config.URL)
	if endpoint == "" {
		return nil, 0, errors.New("HTTP MCP URL is required")
	}
	params = mcpParamsWithProtocolMetadata(params, c.protocolMode, c.openAIForm)
	payload := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	var id int64
	if includeID {
		id = c.nextID.Add(1)
		payload["id"] = id
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}
	var handshakeCancel context.CancelFunc
	if timeout := c.handshakeRequestTimeout(method); timeout > 0 {
		ctx, handshakeCancel = context.WithTimeout(ctx, timeout)
	}
	token, oauthToken := c.authorizationBearerToken(false)
	response, err := c.doHTTPRequestContext(ctx, endpoint, data, sessionID, token)
	if err != nil {
		if handshakeCancel != nil {
			handshakeCancel()
		}
		return nil, 0, err
	}
	if response.StatusCode == http.StatusUnauthorized && oauthToken && strings.TrimSpace(token) != "" {
		retryToken, retryOAuthToken := c.authorizationBearerToken(true)
		if retryOAuthToken && strings.TrimSpace(retryToken) != "" {
			_ = response.Body.Close()
			response, err = c.doHTTPRequestContext(ctx, endpoint, data, sessionID, retryToken)
			if err != nil {
				if handshakeCancel != nil {
					handshakeCancel()
				}
				return nil, 0, err
			}
		}
	}
	if handshakeCancel != nil {
		response.Body = &mcpDeadlineResponseBody{ReadCloser: response.Body, cancel: handshakeCancel}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		readLimit := int64(4096)
		if method == "server/discover" {
			readLimit = maxModernMCPMessageBytes + 1
		}
		body, _ := io.ReadAll(io.LimitReader(response.Body, readLimit))
		_ = response.Body.Close()
		if method == "server/discover" {
			var rpc httpRPCResponse
			if json.Unmarshal(body, &rpc) == nil && rpc.ID == id && rpc.Error != nil && rpc.Error.Code == mcpJSONRPCMethodNotFoundCode {
				return nil, id, newMCPRemoteError(method, rpc.Error)
			}
			if response.StatusCode == http.StatusBadRequest && strings.TrimSpace(response.Header.Get(mcpHTTPSessionIDHeader)) == "" {
				var uncorrelated struct {
					JSONRPC string          `json:"jsonrpc"`
					ID      json.RawMessage `json:"id"`
					Error   *stdioRPCError  `json:"error"`
				}
				if json.Unmarshal(body, &uncorrelated) == nil &&
					uncorrelated.JSONRPC == "2.0" &&
					(len(bytes.TrimSpace(uncorrelated.ID)) == 0 || bytes.Equal(bytes.TrimSpace(uncorrelated.ID), []byte("null"))) &&
					uncorrelated.Error != nil &&
					uncorrelated.Error.Code == mcpLegacyPrevalidationErrorCode &&
					hasMCPLegacyFallbackEvidence(uncorrelated.Error.Message) {
					return nil, id, errMCPModernProtocolUnsupported
				}
			}
		}
		detail := strings.TrimSpace(string(body))
		return nil, 0, &mcpHTTPStatusError{Method: method, Status: response.Status, StatusCode: response.StatusCode, Detail: detail, WWWAuthenticate: response.Header.Values("WWW-Authenticate")}
	}
	return response, id, nil
}

func isMCPHTTPSessionInvalidError(err error) bool {
	var statusErr *mcpHTTPStatusError
	if !errors.As(err, &statusErr) {
		return false
	}
	return statusErr.IsStatus(http.StatusNotFound) || statusErr.IsStatus(http.StatusGone)
}

func isRetryableMCPStreamableHTTPError(err error) bool {
	if err == nil {
		return false
	}
	var statusErr *mcpHTTPStatusError
	if errors.As(err, &statusErr) {
		switch statusErr.StatusCode {
		case http.StatusRequestTimeout,
			http.StatusTooManyRequests,
			http.StatusInternalServerError,
			http.StatusBadGateway,
			http.StatusServiceUnavailable,
			http.StatusGatewayTimeout:
			return true
		default:
			return false
		}
	}
	var remoteErr *MCPRemoteError
	if errors.As(err, &remoteErr) {
		return remoteErr.Code == mcpJSONRPCInternalErrorCode && strings.HasPrefix(remoteErr.Message, "http/request failed:")
	}
	var networkErr net.Error
	if errors.As(err, &networkErr) {
		return true
	}
	return errors.Is(err, io.ErrUnexpectedEOF)
}

func (c *httpClient) doHTTPRequest(endpoint string, data []byte, sessionID string, bearerToken string) (*http.Response, error) {
	return c.doHTTPRequestContext(context.Background(), endpoint, data, sessionID, bearerToken)
}

func (c *httpClient) doHTTPRequestContext(ctx context.Context, endpoint string, data []byte, sessionID string, bearerToken string) (*http.Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	c.applyConfiguredHTTPHeaders(request)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set(mcpHTTPProtocolVersionHeader, c.effectiveProtocolVersion())
	if strings.TrimSpace(sessionID) != "" {
		request.Header.Set(mcpHTTPSessionIDHeader, strings.TrimSpace(sessionID))
	}
	if token := strings.TrimSpace(bearerToken); token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if c.config != nil && c.config.ApplyHTTPRequest != nil && strings.TrimSpace(c.config.BearerTokenEnvVar) == "" {
		if err := c.config.ApplyHTTPRequest(request, data); err != nil {
			return nil, err
		}
	}
	return c.client.Do(request)
}

func (c *httpClient) effectiveProtocolVersion() string {
	if c != nil && strings.TrimSpace(c.negotiatedProtocolVersion) != "" {
		return strings.TrimSpace(c.negotiatedProtocolVersion)
	}
	if c == nil {
		return defaultMCPProtocol
	}
	return c.protocolMode.protocolVersion()
}

func (c *httpClient) applyConfiguredHTTPHeaders(request *http.Request) {
	if c == nil || c.config == nil || request == nil {
		return
	}
	applyMCPHTTPHeaders(request, c.config.HTTPHeaders, c.config.EnvHTTPHeaders)
}

func readMCPHTTPRPCResponse(response *http.Response, id int64) (*httpRPCResponse, error) {
	return readMCPHTTPRPCResponseWithHandler(response, id, nil)
}

func (c *httpClient) readRPCResponse(response *http.Response, id int64, sessionID string) (*httpRPCResponse, error) {
	return readMCPHTTPRPCResponseWithHandlerAndLimit(response, id, modernMCPMessageLimit(c.protocolMode), func(envelope *stdioRPCEnvelope) error {
		if envelope == nil {
			return nil
		}
		ctx := contextWithMCPClientContextAndRoots(context.Background(), c.threadID, c.turnID, c.itemID, c.roots)
		if len(envelope.ID) == 0 {
			return handleMCPClientNotification(ctx, c.serverName, c.progress, envelope.Method, envelope.Params)
		}
		result, rpcErr := mcpClientRequestResult(ctx, c.serverName, c.elicitation, envelope.Method, envelope.ID, envelope.Params)
		payload := map[string]any{
			"jsonrpc": "2.0",
			"id":      envelope.ID,
		}
		if rpcErr != nil {
			payload["error"] = rpcErr
		} else {
			payload["result"] = result
		}
		return c.doClientResponse(payload, sessionID)
	})
}

func readMCPHTTPRPCResponseWithHandler(response *http.Response, id int64, requestHandler func(envelope *stdioRPCEnvelope) error) (*httpRPCResponse, error) {
	return readMCPHTTPRPCResponseWithHandlerAndLimit(response, id, 0, requestHandler)
}

func readMCPHTTPRPCResponseWithHandlerAndLimit(response *http.Response, id int64, maxBytes int64, requestHandler func(envelope *stdioRPCEnvelope) error) (*httpRPCResponse, error) {
	if response == nil || response.Body == nil {
		return nil, errors.New("MCP HTTP response is empty")
	}
	contentType := response.Header.Get("Content-Type")
	if strings.Contains(strings.ToLower(contentType), "text/event-stream") {
		return readMCPHTTPSSEWithLimit(response.Body, id, maxBytes, requestHandler)
	}
	var rpc httpRPCResponse
	if maxBytes <= 0 {
		if err := json.NewDecoder(response.Body).Decode(&rpc); err != nil {
			return nil, err
		}
		return &rpc, nil
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("MCP HTTP JSON response exceeds %d bytes", maxBytes)
	}
	if err := json.Unmarshal(data, &rpc); err != nil {
		return nil, err
	}
	return &rpc, nil
}

func readMCPHTTPSSE(reader io.Reader, id int64, requestHandler func(envelope *stdioRPCEnvelope) error) (*httpRPCResponse, error) {
	return readMCPHTTPSSEWithLimit(reader, id, 0, requestHandler)
}

func readMCPHTTPSSEWithLimit(reader io.Reader, id int64, maxBytes int64, requestHandler func(envelope *stdioRPCEnvelope) error) (*httpRPCResponse, error) {
	scanner := bufio.NewScanner(reader)
	scannerLimit := 16 << 20
	if maxBytes > 0 && maxBytes < int64(scannerLimit) {
		scannerLimit = int(maxBytes)
	}
	scanner.Buffer(make([]byte, 0, 64*1024), scannerLimit)
	var data bytes.Buffer
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			if rpc, done, err := decodeMCPHTTPSSEData(data.Bytes(), id, requestHandler); done || err != nil {
				return rpc, err
			}
			data.Reset()
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, ok := strings.Cut(line, ":")
		if !ok || field != "data" {
			continue
		}
		value = strings.TrimPrefix(value, " ")
		if data.Len() > 0 {
			data.WriteByte('\n')
		}
		data.WriteString(value)
		if maxBytes > 0 && int64(data.Len()) > maxBytes {
			return nil, fmt.Errorf("MCP HTTP SSE event exceeds %d bytes", maxBytes)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if rpc, done, err := decodeMCPHTTPSSEData(data.Bytes(), id, requestHandler); done || err != nil {
		return rpc, err
	}
	return nil, io.EOF
}

func decodeMCPHTTPSSEData(data []byte, id int64, requestHandler func(envelope *stdioRPCEnvelope) error) (*httpRPCResponse, bool, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
		return nil, false, nil
	}
	var envelope stdioRPCEnvelope
	if err := json.Unmarshal(data, &envelope); err == nil && strings.TrimSpace(envelope.Method) != "" {
		if requestHandler != nil {
			if err := requestHandler(&envelope); err != nil {
				return nil, false, err
			}
		}
		return nil, false, nil
	}
	var rpc httpRPCResponse
	if err := json.Unmarshal(data, &rpc); err != nil {
		return nil, false, err
	}
	if rpc.ID != id {
		return nil, false, nil
	}
	return &rpc, true, nil
}

func (c *httpClient) doClientResponse(payload map[string]any, sessionID string) error {
	if c == nil || c.config == nil {
		return errors.New("HTTP MCP client is nil")
	}
	endpoint := strings.TrimSpace(c.config.URL)
	if endpoint == "" {
		return errors.New("HTTP MCP URL is required")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	token, oauthToken := c.authorizationBearerToken(false)
	response, err := c.doHTTPRequest(endpoint, data, sessionID, token)
	if err != nil {
		return err
	}
	if response.StatusCode == http.StatusUnauthorized && oauthToken && strings.TrimSpace(token) != "" {
		retryToken, retryOAuthToken := c.authorizationBearerToken(true)
		if retryOAuthToken && strings.TrimSpace(retryToken) != "" {
			_ = response.Body.Close()
			response, err = c.doHTTPRequest(endpoint, data, sessionID, retryToken)
			if err != nil {
				return err
			}
		}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		detail := strings.TrimSpace(string(body))
		if detail != "" {
			return fmt.Errorf("MCP HTTP client response failed: %s: %s", response.Status, detail)
		}
		return fmt.Errorf("MCP HTTP client response failed: %s", response.Status)
	}
	return nil
}

func (c *httpClient) bearerToken() string {
	token, _ := c.authorizationBearerToken(false)
	return token
}

func (c *httpClient) authorizationBearerToken(forceRefresh bool) (string, bool) {
	name := strings.TrimSpace(c.config.BearerTokenEnvVar)
	if name != "" {
		return strings.TrimSpace(os.Getenv(name)), false
	}
	if c.config.ApplyHTTPRequest != nil || configuredAuthorizationHeader(c.config) {
		return "", false
	}
	if c.config.EffectiveAuth() == ServerAuthChatGPT && !c.config.IsLocalEnvironment() {
		return "", false
	}
	codexHome := strings.TrimSpace(c.config.CodexHome)
	if codexHome == "" {
		return "", false
	}
	serverName := strings.TrimSpace(c.config.OAuthServerName)
	if serverName == "" {
		return "", false
	}
	serverName = c.config.OAuthCredentialName(serverName)
	tokens, err := NewOAuthStore(codexHome).Load(serverName, c.config.URL)
	if err != nil || tokens == nil {
		return "", false
	}
	if forceRefresh {
		if strings.TrimSpace(tokens.RefreshToken) == "" {
			return "", true
		}
		refreshed, err := c.refreshOAuthTokenForRequest(tokens, serverName, codexHome)
		if err != nil || refreshed == nil {
			return "", true
		}
		return refreshed.AccessTokenForRequest(time.Now()), true
	}
	now := time.Now()
	if token := tokens.AccessTokenForRequest(now); token != "" {
		return token, true
	}
	if strings.TrimSpace(tokens.RefreshToken) == "" {
		return "", true
	}
	refreshed, err := c.refreshOAuthTokenForRequest(tokens, serverName, codexHome)
	if err != nil || refreshed == nil {
		return "", true
	}
	return refreshed.AccessTokenForRequest(time.Now()), true
}

func (c *httpClient) refreshOAuthTokenForRequest(tokens *OAuthTokenSet, serverName string, codexHome string) (*OAuthTokenSet, error) {
	if c == nil || c.config == nil || tokens == nil {
		return nil, errors.New("HTTP MCP OAuth refresh requires client and tokens")
	}
	ctx, cancel := context.WithTimeout(context.Background(), mcpOAuthLoginDiscoveryMaxTimeout)
	defer cancel()
	discovery, err := DiscoverStreamableHTTPOAuth(ctx, c.config.URL, c.client)
	if err != nil {
		return nil, err
	}
	if discovery == nil || strings.TrimSpace(discovery.TokenEndpoint) == "" {
		return nil, errors.New("MCP OAuth token endpoint was not discovered")
	}
	// Rust #39615: a stored refresh token must only be used with the issuer
	// that originally granted it. A missing or changed issuer requires
	// reauthentication, and an unexpired access token can still be used
	// without exposing the refresh token.
	discoveredIssuer := strings.TrimSpace(discovery.Issuer)
	storedIssuer := strings.TrimSpace(tokens.Issuer)
	if storedIssuer != "" && discoveredIssuer != "" && !strings.EqualFold(storedIssuer, discoveredIssuer) {
		return nil, fmt.Errorf("MCP OAuth refresh issuer changed: stored %q, discovered %q", storedIssuer, discoveredIssuer)
	}
	clientID := strings.TrimSpace(tokens.ClientID)
	if clientID == "" {
		clientID = strings.TrimSpace(c.config.OAuthClientID)
	}
	refreshed, err := NewOAuthTokenClient(c.client).RefreshToken(ctx, &OAuthRefreshOptions{
		ServerName:      serverName,
		ServerURL:       c.config.URL,
		ClientID:        clientID,
		ClientSecret:    tokens.ClientSecret,
		Issuer:          discoveredIssuer,
		TokenEndpoint:   discovery.TokenEndpoint,
		AccessToken:     tokens.AccessToken,
		RefreshToken:    tokens.RefreshToken,
		Scopes:          tokens.Scopes,
		ExpiresAtMillis: tokens.ExpiresAtMillis,
	})
	if err != nil {
		if isPermanentMCPOAuthRefreshError(err) {
			_, _ = NewOAuthStore(codexHome).Delete(serverName, c.config.URL)
		}
		return nil, err
	}
	if err := NewOAuthStore(codexHome).Save(refreshed); err != nil {
		return nil, err
	}
	return refreshed, nil
}

func isPermanentMCPOAuthRefreshError(err error) bool {
	var retrieveErr *oauth2.RetrieveError
	if !errors.As(err, &retrieveErr) {
		text := strings.ToLower(err.Error())
		return strings.Contains(text, "invalid_grant") || strings.Contains(text, "invalid_client") || strings.Contains(text, "unauthorized_client")
	}
	errorCode := strings.ToLower(strings.TrimSpace(retrieveErr.ErrorCode))
	if errorCode == "" {
		errorCode = strings.ToLower(string(retrieveErr.Body))
	}
	switch {
	case strings.Contains(errorCode, "invalid_grant"), strings.Contains(errorCode, "invalid_client"), strings.Contains(errorCode, "unauthorized_client"):
		return true
	default:
		return false
	}
}

func checkSandboxStateMetaCapability(result *json.RawMessage) bool {
	if result == nil {
		return false
	}
	var initResult struct {
		Capabilities struct {
			Experimental map[string]any `json:"experimental"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(*result, &initResult); err != nil {
		return false
	}
	if initResult.Capabilities.Experimental == nil {
		return false
	}
	_, ok := initResult.Capabilities.Experimental[mcpSandboxStateMetaCapability]
	return ok
}
