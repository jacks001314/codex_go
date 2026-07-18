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
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/http/httpguts"
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

type mcpHTTPStatusError struct {
	Method     string
	Status     string
	StatusCode int
	Detail     string
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

type httpClient struct {
	config      *ServerConfig
	client      *http.Client
	mu          sync.Mutex
	nextID      atomic.Int64
	initialized bool
	sessionID   string
	serverName  string
	threadID    string
	turnID      string
	itemID      string
	roots       []MCPRoot
	elicitation MCPElicitationHandler
	progress    MCPProgressHandler
	openAIForm  bool
	retrySleep  func(time.Duration)
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
	tools := []MCPToolInfo{}
	cursor := ""
	seen := map[string]bool{}
	for page := 0; page < mcpListPaginationMaxPages; page++ {
		if cursor != "" {
			if seen[cursor] {
				return nil, mcpPaginationCursorError("tools/list", cursor)
			}
			seen[cursor] = true
		}
		var response struct {
			Tools      []MCPToolInfo `json:"tools"`
			NextCursor *string       `json:"nextCursor,omitempty"`
		}
		if err := client.CallWithOptions(options, "tools/list", mcpListParams(cursor), &response); err != nil {
			return nil, err
		}
		tools = append(tools, response.Tools...)
		cursor = mcpNextCursor(response.NextCursor)
		if cursor == "" {
			return tools, nil
		}
	}
	return nil, mcpPaginationPageLimitError("tools/list")
}

func listMCPHTTPResources(client *httpClient, options *httpClientCallOptions) ([]MCPResource, error) {
	resources := []MCPResource{}
	cursor := ""
	seen := map[string]bool{}
	for page := 0; page < mcpListPaginationMaxPages; page++ {
		if cursor != "" {
			if seen[cursor] {
				return nil, mcpPaginationCursorError("resources/list", cursor)
			}
			seen[cursor] = true
		}
		var response struct {
			Resources  []MCPResource `json:"resources"`
			NextCursor *string       `json:"nextCursor,omitempty"`
		}
		if err := client.CallWithOptions(options, "resources/list", mcpListParams(cursor), &response); err != nil {
			return nil, err
		}
		resources = append(resources, response.Resources...)
		cursor = mcpNextCursor(response.NextCursor)
		if cursor == "" {
			return resources, nil
		}
	}
	return nil, mcpPaginationPageLimitError("resources/list")
}

func listMCPHTTPResourceTemplates(client *httpClient, options *httpClientCallOptions) ([]MCPResourceTemplate, error) {
	templates := []MCPResourceTemplate{}
	cursor := ""
	seen := map[string]bool{}
	for page := 0; page < mcpListPaginationMaxPages; page++ {
		if cursor != "" {
			if seen[cursor] {
				return nil, mcpPaginationCursorError("resources/templates/list", cursor)
			}
			seen[cursor] = true
		}
		var response struct {
			ResourceTemplates []MCPResourceTemplate `json:"resourceTemplates"`
			NextCursor        *string               `json:"nextCursor,omitempty"`
		}
		if err := client.CallWithOptions(options, "resources/templates/list", mcpListParams(cursor), &response); err != nil {
			return nil, err
		}
		templates = append(templates, response.ResourceTemplates...)
		cursor = mcpNextCursor(response.NextCursor)
		if cursor == "" {
			return templates, nil
		}
	}
	return nil, mcpPaginationPageLimitError("resources/templates/list")
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
	return &httpClient{
		config:     config,
		client:     &http.Client{Timeout: mcpClientTimeout(config)},
		openAIForm: openAIForm,
		retrySleep: time.Sleep,
	}
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
	c.mu.Unlock()
	if sessionID == "" {
		return nil
	}
	return c.deleteSession(sessionID)
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
	request.Header.Set(mcpHTTPProtocolVersionHeader, defaultMCPProtocol)
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
	if c == nil {
		return errors.New("HTTP MCP client is nil")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.applyCallOptions(options)
	if !c.initialized {
		if err := c.reinitialize(); err != nil {
			return err
		}
	}
	err := c.callWithTransientRetries(method, params, out)
	if err == nil || !isMCPHTTPSessionInvalidError(err) || strings.TrimSpace(c.sessionID) == "" {
		return err
	}
	if resetErr := c.reinitialize(); resetErr != nil {
		return resetErr
	}
	return c.callWithTransientRetries(method, params, out)
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

func (c *httpClient) initialize() (string, error) {
	response, requestID, err := c.doRPC("initialize", mcpClientInitializeParams(c.openAIForm), "", true)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	rpc, err := c.readRPCResponse(response, requestID, response.Header.Get(mcpHTTPSessionIDHeader))
	if err != nil {
		return "", err
	}
	if rpc.Error != nil {
		return "", newMCPRemoteError("initialize", rpc.Error)
	}
	return response.Header.Get(mcpHTTPSessionIDHeader), nil
}

func (c *httpClient) notifyInitialized(sessionID string) error {
	response, _, err := c.doRPC("notifications/initialized", map[string]any{}, sessionID, false)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	return err
}

func (c *httpClient) reinitialize() error {
	c.initialized = false
	c.sessionID = ""
	for attempt := 0; ; attempt++ {
		sessionID, err := c.initialize()
		if err == nil {
			c.sessionID = sessionID
			err = c.notifyInitialized(sessionID)
			if err == nil {
				c.initialized = true
				return nil
			}
		}
		c.sessionID = ""
		if attempt >= len(mcpStreamableHTTPRetryDelays) || !isRetryableMCPStreamableHTTPError(err) {
			return err
		}
		c.sleepBeforeRetry(mcpStreamableHTTPRetryDelays[attempt])
	}
}

func (c *httpClient) callWithTransientRetries(method string, params any, out any) error {
	for attempt := 0; ; attempt++ {
		err := c.callWithSession(method, params, out, c.sessionID)
		if err == nil {
			return nil
		}
		if method != "tools/list" || attempt >= len(mcpStreamableHTTPRetryDelays) || !isRetryableMCPStreamableHTTPError(err) {
			return err
		}
		c.sleepBeforeRetry(mcpStreamableHTTPRetryDelays[attempt])
	}
}

func (c *httpClient) sleepBeforeRetry(delay time.Duration) {
	if c.retrySleep != nil {
		c.retrySleep(delay)
		return
	}
	time.Sleep(delay)
}

func (c *httpClient) callWithSession(method string, params any, out any, sessionID string) error {
	response, requestID, err := c.doRPC(method, params, sessionID, true)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	rpc, err := c.readRPCResponse(response, requestID, sessionID)
	if err != nil {
		return err
	}
	if rpc.Error != nil {
		return newMCPRemoteError(method, rpc.Error)
	}
	if out == nil || rpc.Result == nil {
		return nil
	}
	return json.Unmarshal(*rpc.Result, out)
}

func (c *httpClient) doRPC(method string, params any, sessionID string, includeID bool) (*http.Response, int64, error) {
	if c == nil || c.config == nil {
		return nil, 0, errors.New("HTTP MCP client is nil")
	}
	endpoint := strings.TrimSpace(c.config.URL)
	if endpoint == "" {
		return nil, 0, errors.New("HTTP MCP URL is required")
	}
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
	token, oauthToken := c.authorizationBearerToken(false)
	response, err := c.doHTTPRequest(endpoint, data, sessionID, token)
	if err != nil {
		return nil, 0, err
	}
	if response.StatusCode == http.StatusUnauthorized && oauthToken && strings.TrimSpace(token) != "" {
		retryToken, retryOAuthToken := c.authorizationBearerToken(true)
		if retryOAuthToken && strings.TrimSpace(retryToken) != "" {
			_ = response.Body.Close()
			response, err = c.doHTTPRequest(endpoint, data, sessionID, retryToken)
			if err != nil {
				return nil, 0, err
			}
		}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		_ = response.Body.Close()
		detail := strings.TrimSpace(string(body))
		return nil, 0, &mcpHTTPStatusError{Method: method, Status: response.Status, StatusCode: response.StatusCode, Detail: detail}
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
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	c.applyConfiguredHTTPHeaders(request)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set(mcpHTTPProtocolVersionHeader, defaultMCPProtocol)
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

func (c *httpClient) applyConfiguredHTTPHeaders(request *http.Request) {
	if c == nil || c.config == nil || request == nil {
		return
	}
	for name, value := range c.config.HTTPHeaders {
		if !httpguts.ValidHeaderFieldName(name) || !httpguts.ValidHeaderFieldValue(value) {
			continue
		}
		request.Header.Set(name, value)
	}
	for name, envVar := range c.config.EnvHTTPHeaders {
		if !httpguts.ValidHeaderFieldName(name) || strings.TrimSpace(envVar) == "" {
			continue
		}
		if value := os.Getenv(strings.TrimSpace(envVar)); strings.TrimSpace(value) != "" && httpguts.ValidHeaderFieldValue(value) {
			request.Header.Set(name, value)
		}
	}
}

func readMCPHTTPRPCResponse(response *http.Response, id int64) (*httpRPCResponse, error) {
	return readMCPHTTPRPCResponseWithHandler(response, id, nil)
}

func (c *httpClient) readRPCResponse(response *http.Response, id int64, sessionID string) (*httpRPCResponse, error) {
	return readMCPHTTPRPCResponseWithHandler(response, id, func(envelope *stdioRPCEnvelope) error {
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
	if response == nil || response.Body == nil {
		return nil, errors.New("MCP HTTP response is empty")
	}
	contentType := response.Header.Get("Content-Type")
	if strings.Contains(strings.ToLower(contentType), "text/event-stream") {
		return readMCPHTTPSSE(response.Body, id, requestHandler)
	}
	var rpc httpRPCResponse
	if err := json.NewDecoder(response.Body).Decode(&rpc); err != nil {
		return nil, err
	}
	return &rpc, nil
}

func readMCPHTTPSSE(reader io.Reader, id int64, requestHandler func(envelope *stdioRPCEnvelope) error) (*httpRPCResponse, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 16<<20)
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
	codexHome := strings.TrimSpace(c.config.CodexHome)
	if codexHome == "" {
		return "", false
	}
	serverName := strings.TrimSpace(c.config.OAuthServerName)
	if serverName == "" {
		return "", false
	}
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
	clientID := strings.TrimSpace(tokens.ClientID)
	if clientID == "" {
		clientID = strings.TrimSpace(c.config.OAuthClientID)
	}
	refreshed, err := NewOAuthTokenClient(c.client).RefreshToken(ctx, &OAuthRefreshOptions{
		ServerName:      serverName,
		ServerURL:       c.config.URL,
		ClientID:        clientID,
		ClientSecret:    tokens.ClientSecret,
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
