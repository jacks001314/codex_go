package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type stdioInventory struct {
	Tools             []MCPToolInfo
	Resources         []MCPResource
	ResourceTemplates []MCPResourceTemplate
}

type stdioClient struct {
	mu                                 sync.Mutex
	writeMu                            sync.Mutex
	closed                             bool
	config                             *ServerConfig
	cmd                                *exec.Cmd
	stdin                              io.WriteCloser
	reader                             *bufio.Reader
	stderr                             *stdioOutputBuffer
	nextID                             int64
	started                            bool
	initialized                        bool
	initializing                       bool
	initDone                           chan struct{}
	initErr                            error
	pending                            map[int64]*stdioPendingCall
	pendingOrder                       []int64
	openAIForm                         bool
	protocolMode                       MCPProtocolMode
	supportsSandboxStateMetaCapability bool
}

// os/exec copies a child's output from background goroutines. Keep each
// process's buffer independent and safe to inspect while those copies finish.
type stdioOutputBuffer struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (b *stdioOutputBuffer) Write(p []byte) (int, error) {
	if b == nil {
		return len(p), nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.Write(p)
}

func (b *stdioOutputBuffer) String() string {
	if b == nil {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.String()
}

type stdioPendingCall struct {
	ID      int64
	Method  string
	Context context.Context
	Options *stdioCallOptions
	Result  chan stdioCallResult
}

type stdioCallResult struct {
	Response *stdioRPCResponse
	Error    error
}

type stdioCallOptions struct {
	ServerName  string
	ThreadID    string
	TurnID      string
	ItemID      string
	Roots       []MCPRoot
	Elicitation MCPElicitationHandler
	Progress    MCPProgressHandler
}

type stdioRPCResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      int64            `json:"id,omitempty"`
	Result  *json.RawMessage `json:"result,omitempty"`
	Error   *stdioRPCError   `json:"error,omitempty"`
}

type stdioRPCError struct {
	Code    int64           `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type stdioRPCEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func listMCPStdioInventory(config *ServerConfig) (*stdioInventory, error) {
	client := newMCPStdioClient(config)
	defer client.Close()
	return listMCPStdioInventoryWithClient(client)
}

func listMCPStdioInventoryWithClient(client *stdioClient) (*stdioInventory, error) {
	return listMCPStdioInventoryWithOptions(client, "", "", nil)
}

func listMCPStdioInventoryWithOptions(client *stdioClient, serverName string, threadID string, roots []MCPRoot) (*stdioInventory, error) {
	result := &stdioInventory{}
	options := &stdioCallOptions{ServerName: serverName, ThreadID: threadID, Roots: roots}
	tools, err := listMCPStdioTools(client, options)
	if err != nil {
		return nil, err
	}
	result.Tools = tools
	if resources, err := listMCPStdioResources(client, options); err == nil {
		result.Resources = resources
	}
	if templates, err := listMCPStdioResourceTemplates(client, options); err == nil {
		result.ResourceTemplates = templates
	}
	return result, nil
}

func listMCPStdioTools(client *stdioClient, options *stdioCallOptions) ([]MCPToolInfo, error) {
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

func listMCPStdioResources(client *stdioClient, options *stdioCallOptions) ([]MCPResource, error) {
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

func listMCPStdioResourceTemplates(client *stdioClient, options *stdioCallOptions) ([]MCPResourceTemplate, error) {
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

func callMCPStdioTool(config *ServerConfig, serverName string, threadID string, turnID string, itemID string, elicitation MCPElicitationHandler, progress MCPProgressHandler, tool string, arguments any, meta any) (*MCPToolCallResponse, error) {
	client := newMCPStdioClient(config)
	defer client.Close()
	return callMCPStdioToolWithClient(client, serverName, threadID, turnID, itemID, nil, elicitation, progress, tool, arguments, meta)
}

func callMCPStdioToolWithClient(client *stdioClient, serverName string, threadID string, turnID string, itemID string, roots []MCPRoot, elicitation MCPElicitationHandler, progress MCPProgressHandler, tool string, arguments any, meta any) (*MCPToolCallResponse, error) {
	params := map[string]any{
		"name":      tool,
		"arguments": arguments,
	}
	if meta != nil {
		params["_meta"] = meta
	}
	var response MCPToolCallResponse
	if err := client.CallWithOptions(&stdioCallOptions{ServerName: serverName, ThreadID: threadID, TurnID: turnID, ItemID: itemID, Roots: roots, Elicitation: elicitation, Progress: progress}, "tools/call", params, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func readMCPStdioResource(config *ServerConfig, serverName string, elicitation MCPElicitationHandler, progress MCPProgressHandler, params *MCPResourceReadParams) (*MCPResourceReadResponse, error) {
	client := newMCPStdioClient(config)
	defer client.Close()
	return readMCPStdioResourceWithClient(client, serverName, nil, elicitation, progress, params)
}

func readMCPStdioResourceWithClient(client *stdioClient, serverName string, roots []MCPRoot, elicitation MCPElicitationHandler, progress MCPProgressHandler, params *MCPResourceReadParams) (*MCPResourceReadResponse, error) {
	if params == nil || strings.TrimSpace(params.URI) == "" {
		return nil, invalidMCPRequest("server and uri are required")
	}
	threadID := ""
	if params != nil && params.ThreadID != nil {
		threadID = strings.TrimSpace(*params.ThreadID)
	}
	var response MCPResourceReadResponse
	if err := client.CallWithOptions(&stdioCallOptions{ServerName: serverName, ThreadID: threadID, Roots: roots, Elicitation: elicitation, Progress: progress}, "resources/read", map[string]any{"uri": strings.TrimSpace(params.URI)}, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func newMCPStdioClient(config *ServerConfig) *stdioClient {
	return newMCPStdioClientWithOpenAIForm(config, false)
}

func newMCPStdioClientWithOpenAIForm(config *ServerConfig, openAIForm bool) *stdioClient {
	cloned := cloneServerConfig(config)
	return &stdioClient{config: &cloned, nextID: 1, openAIForm: openAIForm, protocolMode: cloned.ProtocolMode}
}

func (c *stdioClient) Call(method string, params any, out any) error {
	return c.CallWithOptions(nil, method, params, out)
}

func (c *stdioClient) CallWithOptions(options *stdioCallOptions, method string, params any, out any) error {
	return c.CallWithOptionsContext(context.Background(), options, method, params, out)
}

func (c *stdioClient) CallWithOptionsContext(ctx context.Context, options *stdioCallOptions, method string, params any, out any) error {
	if c == nil || c.config == nil {
		return errors.New("stdio MCP client is nil")
	}
	if strings.TrimSpace(c.config.Command) == "" {
		return errors.New("stdio MCP command is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, c.callTimeout())
	defer cancel()
	if options != nil {
		ctx = contextWithMCPClientContextAndRoots(ctx, options.ThreadID, options.TurnID, options.ItemID, options.Roots)
	}
	if err := c.ensureInitialized(ctx, options); err != nil {
		return err
	}
	stderr := c.currentStderr()
	baseParams := params
	requestParams := params
	for round := 0; ; round++ {
		response, err := c.doRequest(ctx, options, method, requestParams)
		if err != nil {
			return decorateMCPStdioError(err, stderr)
		}
		if response.Error != nil {
			return newMCPRemoteError(method, response.Error)
		}
		if c.protocolModeSnapshot() == MCPProtocol20260728 {
			nextParams, inputRequired, err := nextMCP2026RequestParams(ctx, serverNameFromStdioOptions(options), elicitationFromStdioOptions(options), baseParams, response.Result)
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
		if out == nil || response.Result == nil {
			return nil
		}
		return json.Unmarshal(*response.Result, out)
	}
}

func (c *stdioClient) doRequest(ctx context.Context, options *stdioCallOptions, method string, params any) (*stdioRPCResponse, error) {
	params = mcpParamsWithProtocolMetadata(params, c.protocolModeSnapshot(), c.openAIForm)
	cmd := c.currentCommand()
	id := c.nextRequestID()
	pending := &stdioPendingCall{
		ID:      id,
		Method:  method,
		Context: ctx,
		Options: cloneStdioCallOptions(options),
		Result:  make(chan stdioCallResult, 1),
	}
	c.registerPending(pending)
	if err := c.writeFrame(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}); err != nil {
		c.unregisterPending(id)
		c.failTransportFor(cmd, err)
		return nil, err
	}
	select {
	case result := <-pending.Result:
		if result.Error != nil {
			return nil, result.Error
		}
		if result.Response == nil {
			return nil, io.ErrUnexpectedEOF
		}
		return result.Response, nil
	case <-ctx.Done():
		c.unregisterPending(id)
		c.failTransportFor(cmd, ctx.Err())
		return nil, ctx.Err()
	}
}

func isMCPRemoteError(err error) bool {
	var remoteErr *MCPRemoteError
	return errors.As(err, &remoteErr)
}

func (c *stdioClient) ensureInitialized(ctx context.Context, options *stdioCallOptions) error {
	for {
		c.mu.Lock()
		if c.started && c.initialized && c.cmd != nil && c.stdin != nil && c.reader != nil {
			c.mu.Unlock()
			return nil
		}
		if c.initializing {
			done := c.initDone
			c.mu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		initDone := make(chan struct{})
		c.initializing = true
		c.initDone = initDone
		c.initErr = nil
		c.mu.Unlock()
		err := c.startAndInitialize(ctx, options)
		c.mu.Lock()
		closeInit := c.initDone == initDone
		if closeInit {
			c.initErr = err
			c.initializing = false
			c.initDone = nil
		}
		c.mu.Unlock()
		if closeInit {
			close(initDone)
		}
		return err
	}
}

func (c *stdioClient) startAndInitialize(ctx context.Context, options *stdioCallOptions) error {
	protocolMode, launchEnv, stripProtocolMarker, err := mcpStdioLaunchConfig(c.config)
	if err != nil {
		return err
	}
	command := resolveMCPStdioCommand(c.config.Command, launchEnv)
	cmd := newMCPStdioCommand(command, c.config.Args...)
	if cwd := strings.TrimSpace(c.config.CWD); cwd != "" {
		cmd.Dir = cwd
	}
	baseEnv := os.Environ()
	if stripProtocolMarker {
		baseEnv = withoutEnvironmentVariable(baseEnv, mcpProtocolVersionEnvVar)
	}
	baseEnv = append(baseEnv, mcpCustomCAEnvPairs(launchEnv)...)
	cmd.Env = append(baseEnv, envPairs(launchEnv)...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr := &stdioOutputBuffer{}
	cmd.Stderr = stderr
	c.mu.Lock()
	if err := cmd.Start(); err != nil {
		c.mu.Unlock()
		return err
	}
	c.cmd = cmd
	c.stdin = stdin
	c.reader = bufio.NewReader(stdout)
	c.stderr = stderr
	c.started = true
	c.initialized = false
	c.protocolMode = protocolMode
	c.pending = map[int64]*stdioPendingCall{}
	c.pendingOrder = nil
	c.mu.Unlock()
	go c.readLoop(cmd, stdout)

	if protocolMode == MCPProtocol20260728 {
		response, err := c.doRequest(ctx, options, "server/discover", map[string]any{})
		if err == nil && response != nil && response.Error != nil {
			err = newMCPRemoteError("server/discover", response.Error)
		}
		if err == nil {
			err = validateMCPModernDiscovery(response.Result)
		}
		if err == nil {
			c.supportsSandboxStateMetaCapability = checkSandboxStateMetaCapability(response.Result)
			c.mu.Lock()
			c.initialized = true
			c.mu.Unlock()
			return nil
		}
		if !isMCPDiscoveryFallbackError(err) {
			_ = c.Close()
			return decorateMCPStdioError(err, stderr)
		}
		c.mu.Lock()
		c.protocolMode = MCPProtocolLegacy
		c.mu.Unlock()
	}

	response, err := c.doRequest(ctx, options, "initialize", mcpClientInitializeParams(c.openAIForm))
	if err != nil {
		_ = c.Close()
		return decorateMCPStdioError(err, stderr)
	}
	if response != nil && response.Error != nil {
		err := newMCPRemoteError("initialize", response.Error)
		_ = c.Close()
		return decorateMCPStdioError(err, stderr)
	}
	c.supportsSandboxStateMetaCapability = checkSandboxStateMetaCapability(response.Result)
	if err := c.writeFrame(map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
		"params":  map[string]any{},
	}); err != nil {
		c.Close()
		return decorateMCPStdioError(err, stderr)
	}
	c.mu.Lock()
	c.initialized = true
	c.mu.Unlock()
	return nil
}

func resolveMCPStdioCommand(command string, env map[string]string) string {
	command = strings.TrimSpace(command)
	if runtime.GOOS != "windows" || command == "" || hasPathSeparator(command) || filepath.Ext(command) != "" {
		return command
	}
	pathValue := ""
	if env != nil {
		pathValue = firstNonEmptyMCP(env["PATH"], env["Path"], env["path"])
	}
	if pathValue == "" {
		pathValue = os.Getenv("PATH")
	}
	for _, dir := range filepath.SplitList(pathValue) {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		for _, ext := range []string{".exe", ".cmd", ".bat", ".com"} {
			candidate := filepath.Join(dir, command+ext)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate
			}
		}
	}
	return command
}

func hasPathSeparator(value string) bool {
	return strings.ContainsAny(value, `/\`)
}

func (c *stdioClient) callTimeout() time.Duration {
	if c != nil && c.config != nil {
		if c.config.ToolTimeout > 0 {
			return c.config.ToolTimeout
		}
		if c.config.StartupTimeout > 0 {
			return c.config.StartupTimeout
		}
	}
	return 15 * time.Second
}

func (c *stdioClient) currentStderr() *stdioOutputBuffer {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stderr
}

func (c *stdioClient) currentCommand() *exec.Cmd {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cmd
}

func (c *stdioClient) isCurrentCommand(cmd *exec.Cmd) bool {
	if c == nil || cmd == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cmd == cmd
}

func (c *stdioClient) readLoop(cmd *exec.Cmd, reader io.Reader) {
	buffered := bufio.NewReader(reader)
	for {
		data, err := readMCPFrameWithLimit(buffered, modernMCPMessageLimit(c.protocolModeSnapshot()))
		if err != nil {
			c.failTransportFor(cmd, err)
			return
		}
		if !c.isCurrentCommand(cmd) {
			return
		}
		if err := c.handleReadFrame(data); err != nil {
			c.failTransportFor(cmd, err)
			return
		}
	}
}

func (c *stdioClient) handleReadFrame(data []byte) error {
	var envelope stdioRPCEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}
	if strings.TrimSpace(envelope.Method) != "" {
		pending := c.activePendingCall()
		ctx, serverName, elicitation, progress := pendingStdioHandlers(pending)
		if len(envelope.ID) == 0 {
			return handleMCPClientNotification(ctx, serverName, progress, envelope.Method, envelope.Params)
		}
		result, rpcErr := mcpClientRequestResult(ctx, serverName, elicitation, envelope.Method, envelope.ID, envelope.Params)
		response := map[string]any{
			"jsonrpc": "2.0",
			"id":      envelope.ID,
		}
		if rpcErr != nil {
			response["error"] = rpcErr
		} else {
			response["result"] = result
		}
		return c.writeFrame(response)
	}
	var response stdioRPCResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return err
	}
	if response.ID == 0 {
		return nil
	}
	c.deliverResponse(&response)
	return nil
}

func (c *stdioClient) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closeLocked()
}

func (c *stdioClient) isClosed() bool {
	if c == nil {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *stdioClient) closeLocked() error {
	c.closed = true
	pending := c.pendingCallsLocked()
	stderr := c.stderr
	var waitErr error
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		waitErr = waitMCPStdioCommand(c.cmd)
	}
	if message := strings.TrimSpace(stderr.String()); waitErr != nil && message != "" {
		waitErr = fmt.Errorf("%w: %s", waitErr, message)
	}
	c.cmd = nil
	c.stdin = nil
	c.reader = nil
	c.stderr = nil
	c.started = false
	c.initialized = false
	c.initializing = false
	c.pending = map[int64]*stdioPendingCall{}
	c.pendingOrder = nil
	if c.initDone != nil {
		close(c.initDone)
		c.initDone = nil
	}
	for _, call := range pending {
		call.Result <- stdioCallResult{Error: io.ErrClosedPipe}
	}
	return waitErr
}

func (c *stdioClient) nextRequestID() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.nextID <= 0 {
		c.nextID = 1
	}
	id := c.nextID
	c.nextID++
	return id
}

func (c *stdioClient) registerPending(call *stdioPendingCall) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending == nil {
		c.pending = map[int64]*stdioPendingCall{}
	}
	c.pending[call.ID] = call
	c.pendingOrder = append(c.pendingOrder, call.ID)
}

func (c *stdioClient) unregisterPending(id int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.removePendingLocked(id)
}

func (c *stdioClient) deliverResponse(response *stdioRPCResponse) {
	c.mu.Lock()
	call := c.removePendingLocked(response.ID)
	c.mu.Unlock()
	if call != nil {
		call.Result <- stdioCallResult{Response: response}
	}
}

func (c *stdioClient) removePendingLocked(id int64) *stdioPendingCall {
	call := c.pending[id]
	delete(c.pending, id)
	if call != nil {
		for index, pendingID := range c.pendingOrder {
			if pendingID == id {
				c.pendingOrder = append(c.pendingOrder[:index], c.pendingOrder[index+1:]...)
				break
			}
		}
	}
	return call
}

func (c *stdioClient) activePendingCall() *stdioPendingCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.pendingOrder) - 1; i >= 0; i-- {
		if call := c.pending[c.pendingOrder[i]]; call != nil {
			return call
		}
	}
	return nil
}

func (c *stdioClient) pendingCallsLocked() []*stdioPendingCall {
	if len(c.pending) == 0 {
		return nil
	}
	calls := make([]*stdioPendingCall, 0, len(c.pending))
	for _, call := range c.pending {
		calls = append(calls, call)
	}
	return calls
}

func (c *stdioClient) failTransportFor(owner *exec.Cmd, err error) {
	if err == nil {
		err = io.ErrUnexpectedEOF
	}
	c.mu.Lock()
	if owner != nil && c.cmd != owner {
		c.mu.Unlock()
		return
	}
	pending := c.pendingCallsLocked()
	c.pending = map[int64]*stdioPendingCall{}
	c.pendingOrder = nil
	cmd := c.cmd
	stdin := c.stdin
	c.cmd = nil
	c.stdin = nil
	c.reader = nil
	c.started = false
	c.initialized = false
	c.initializing = false
	if c.initDone != nil {
		close(c.initDone)
		c.initDone = nil
	}
	c.mu.Unlock()
	if stdin != nil {
		_ = stdin.Close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		_ = waitMCPStdioCommand(cmd)
	}
	for _, call := range pending {
		call.Result <- stdioCallResult{Error: err}
	}
}

func waitMCPStdioCommand(cmd *exec.Cmd) error {
	if cmd == nil {
		return nil
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(time.Second):
		return context.DeadlineExceeded
	}
}

func (c *stdioClient) writeFrame(value any) error {
	c.mu.Lock()
	writer := c.stdin
	c.mu.Unlock()
	if writer == nil {
		return io.ErrClosedPipe
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return writeMCPFrame(writer, value)
}

func cloneStdioCallOptions(options *stdioCallOptions) *stdioCallOptions {
	if options == nil {
		return nil
	}
	clone := *options
	clone.Roots = cloneMCPRoots(options.Roots)
	return &clone
}

func pendingStdioHandlers(call *stdioPendingCall) (context.Context, string, MCPElicitationHandler, MCPProgressHandler) {
	if call == nil {
		return context.Background(), "", nil, nil
	}
	ctx := call.Context
	if ctx == nil {
		ctx = context.Background()
	}
	serverName, elicitation, progress := stdioClientHandlers(call.Options)
	return ctx, serverName, elicitation, progress
}

func stdioClientHandlers(options *stdioCallOptions) (string, MCPElicitationHandler, MCPProgressHandler) {
	if options == nil {
		return "", nil, nil
	}
	return options.ServerName, options.Elicitation, options.Progress
}

func serverNameFromStdioOptions(options *stdioCallOptions) string {
	serverName, _, _ := stdioClientHandlers(options)
	return serverName
}

func elicitationFromStdioOptions(options *stdioCallOptions) MCPElicitationHandler {
	_, elicitation, _ := stdioClientHandlers(options)
	return elicitation
}

func (c *stdioClient) protocolModeSnapshot() MCPProtocolMode {
	if c == nil {
		return MCPProtocolLegacy
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.protocolMode
}

func mcpClientInitializeParams(openAIForm bool) map[string]any {
	return map[string]any{
		"protocolVersion": defaultMCPProtocol,
		"capabilities":    mcpClientCapabilities(openAIForm),
		"clientInfo": map[string]string{
			"name":    "codex-go",
			"version": "go-port",
		},
	}
}

func writeMCPFrame(writer io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	// MCP stdio transports use one JSON-RPC message per line. Accepting the
	// legacy Content-Length form on reads is useful for older local servers, but
	// emitting it prevents standards-compliant SDKs from seeing initialize.
	_, err = writer.Write(append(data, '\n'))
	return err
}

func readMCPResult(ctx context.Context, reader *bufio.Reader, writer io.Writer, id int64, method string, serverName string, elicitation MCPElicitationHandler, progress MCPProgressHandler, out any) error {
	for {
		data, err := readMCPFrame(reader)
		if err != nil {
			return err
		}
		if handled, err := handleMCPClientRequestFrame(ctx, writer, serverName, elicitation, progress, data); handled || err != nil {
			if err != nil {
				return err
			}
			continue
		}
		var response stdioRPCResponse
		if err := json.Unmarshal(data, &response); err != nil {
			return err
		}
		if response.ID != id {
			continue
		}
		if response.Error != nil {
			return newMCPRemoteError(method, response.Error)
		}
		if out == nil || response.Result == nil {
			return nil
		}
		return json.Unmarshal(*response.Result, out)
	}
}

func handleMCPClientRequestFrame(ctx context.Context, writer io.Writer, serverName string, elicitation MCPElicitationHandler, progress MCPProgressHandler, data []byte) (bool, error) {
	var envelope stdioRPCEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return false, err
	}
	if strings.TrimSpace(envelope.Method) == "" {
		return false, nil
	}
	if len(envelope.ID) == 0 {
		return true, handleMCPClientNotification(ctx, serverName, progress, envelope.Method, envelope.Params)
	}
	result, rpcErr := mcpClientRequestResult(ctx, serverName, elicitation, envelope.Method, envelope.ID, envelope.Params)
	response := map[string]any{
		"jsonrpc": "2.0",
		"id":      envelope.ID,
	}
	if rpcErr != nil {
		response["error"] = rpcErr
	} else {
		response["result"] = result
	}
	return true, writeMCPFrame(writer, response)
}

func mcpClientRequestResult(ctx context.Context, serverName string, elicitation MCPElicitationHandler, method string, id json.RawMessage, params json.RawMessage) (any, *stdioRPCError) {
	switch method {
	case "roots/list":
		return map[string]any{"roots": mcpRootsFromContext(ctx)}, nil
	case "elicitation/create", "openai/form":
		return mcpElicitationResult(ctx, serverName, elicitation, method, id, params), nil
	default:
		return nil, &stdioRPCError{Code: -32601, Message: "method not found: " + method}
	}
}

func readMCPFrame(reader *bufio.Reader) ([]byte, error) {
	return readMCPFrameWithLimit(reader, 0)
}

func readMCPFrameWithLimit(reader *bufio.Reader, maxBytes int64) ([]byte, error) {
	contentLength := -1
	line, err := reader.ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && len(line) > 0) {
		return nil, err
	}
	if maxBytes > 0 && int64(len(line)) > maxBytes {
		return nil, fmt.Errorf("MCP stdio message exceeds %d bytes", maxBytes)
	}
	line = strings.TrimRight(line, "\r\n")
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return []byte(trimmed), nil
	}
	for {
		if line == "" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(key), "content-length") {
			parsed, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, err
			}
			contentLength = parsed
		}
		line, err = reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if maxBytes > 0 && int64(len(line)) > maxBytes {
			return nil, fmt.Errorf("MCP stdio header exceeds %d bytes", maxBytes)
		}
		line = strings.TrimRight(line, "\r\n")
	}
	if contentLength < 0 {
		return nil, errors.New("MCP response missing Content-Length")
	}
	if maxBytes > 0 && int64(contentLength) > maxBytes {
		return nil, fmt.Errorf("MCP stdio message exceeds %d bytes", maxBytes)
	}
	data := make([]byte, contentLength)
	_, err = io.ReadFull(reader, data)
	return data, err
}

func mcpStdioLaunchConfig(config *ServerConfig) (MCPProtocolMode, map[string]string, bool, error) {
	if config == nil {
		return MCPProtocolLegacy, nil, false, nil
	}
	env := cloneStringMap(config.Env)
	if config.ProtocolMode != MCPProtocol20260728 {
		return MCPProtocolLegacy, env, false, nil
	}
	requestedVersion, configured := env[mcpProtocolVersionEnvVar]
	delete(env, mcpProtocolVersionEnvVar)
	if !configured {
		return MCPProtocolLegacy, env, true, nil
	}
	if requestedVersion != modernMCPProtocol {
		return MCPProtocolLegacy, nil, true, fmt.Errorf("unsupported %s `%s` for stdio MCP server; expected `%s`", mcpProtocolVersionEnvVar, requestedVersion, modernMCPProtocol)
	}
	return MCPProtocol20260728, env, true, nil
}

func withoutEnvironmentVariable(env []string, target string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(name, target) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// mcpCustomCAEnvKeys mirrors Rust codex_network_proxy::CUSTOM_CA_ENV_KEYS: the
// curated set of CA-bundle environment variables forwarded to child toolchains.
var mcpCustomCAEnvKeys = []string{
	"CODEX_CA_CERTIFICATE",
	"SSL_CERT_FILE",
	"REQUESTS_CA_BUNDLE",
	"CURL_CA_BUNDLE",
	"NODE_EXTRA_CA_CERTS",
	"GIT_SSL_CAINFO",
	"CARGO_HTTP_CAINFO",
	"PIP_CERT",
	"BUNDLE_SSL_CA_CERT",
	"npm_config_cafile",
	"NPM_CONFIG_CAFILE",
}

// mcpCustomCAEnvPairs forwards inherited custom CA settings to local stdio MCP
// servers, converting non-empty relative paths to absolute paths (the server
// may start in a different working directory). Explicit MCP environment
// overrides take precedence and suppress the inherited entry
// (Rust b2543af02b).
func mcpCustomCAEnvPairs(launchEnv map[string]string) []string {
	var pairs []string
	for _, key := range mcpCustomCAEnvKeys {
		if mcpEnvHasKey(launchEnv, key) {
			continue
		}
		value, ok := os.LookupEnv(key)
		if !ok || value == "" {
			continue
		}
		if absolute, err := filepath.Abs(value); err == nil {
			value = absolute
		}
		pairs = append(pairs, key+"="+value)
	}
	return pairs
}

func mcpEnvHasKey(env map[string]string, key string) bool {
	for name := range env {
		if sameMCPEnvKey(name, key) {
			return true
		}
	}
	return false
}

func sameMCPEnvKey(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func envPairs(values map[string]string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for key, value := range values {
		out = append(out, key+"="+value)
	}
	return out
}

func decorateMCPStdioError(err error, stderr *stdioOutputBuffer) error {
	if stderr == nil || strings.TrimSpace(stderr.String()) == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
}
