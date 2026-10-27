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
	// ServerCapabilities is the initialized server's advertised capabilities
	// object (Rust #44826), independent of tool discovery success.
	ServerCapabilities json.RawMessage
	// ToolListMeta is the response-level `_meta` of the first `tools/list` page
	// that produced Tools (Rust #54686); nil when tool discovery failed, so the
	// status wire field serializes as null.
	ToolListMeta json.RawMessage
}

// mcpStdioStderrDrainGrace bounds how long teardown waits for queued stderr
// diagnostics before closing the pipe, even when an escaped descendant keeps it
// open (Rust #43870).
const mcpStdioStderrDrainGrace = 250 * time.Millisecond

type stdioClient struct {
	mu               sync.Mutex
	capabilitiesMu   sync.Mutex
	writeMu          sync.Mutex
	closed           bool
	config           *ServerConfig
	cmd              *exec.Cmd
	process          *mcpStdioProcess
	stdin            io.WriteCloser
	reader           *bufio.Reader
	stderr           *stdioOutputBuffer
	nextID           int64
	started          bool
	initialized      bool
	initializing     bool
	initDone         chan struct{}
	initErr          error
	pending          map[int64]*stdioPendingCall
	pendingOrder     []int64
	openAIForm       bool
	userVerification bool
	// networkPolicy advertises the experimental call-scoped network policy
	// capability to a trusted ChatGPT origin (Rust #56278).
	networkPolicy bool
	protocolMode  MCPProtocolMode
	// messageLimit is the bounded framing limit selected at launch. Discovery
	// keeps the modern limit even after falling back to the legacy handshake,
	// matching Rust's bounded local transport (Rust #57210).
	messageLimit int64
	// legacyFallback is a previously working legacy connection for the same
	// account, kept while this connection attempts a protocol upgrade. Once the
	// upgrade fails for a non-authentication reason every call is delegated to
	// it (Rust #57210).
	legacyFallback *stdioClient
	// delegate is the retained legacy connection this client hands all traffic
	// to after a failed upgrade.
	delegate *stdioClient
	// startupInputReceived records that the helper asked its chat for input
	// before it finished starting, which binds it to that chat and keeps it out
	// of the shared pool (Rust #57211).
	startupInputReceived               bool
	supportsSandboxStateMetaCapability bool
	// elicitationCancellations remembers server cancellations for this
	// connection so elicitations return `cancel` instead of hanging
	// (Rust #44238). It is reset for every new connection attempt.
	elicitationCancellations mcpElicitationCancellationMemory
	// authChanges, when set, lets this stdio connection notify an opted-in
	// server about credential/ownership changes (Rust #43428). authChangeStop
	// tears down the watcher for the current connection.
	authChanges    MCPAuthChangeSource
	authChangeStop func()
	// serverCapabilities is the initialized server's advertised capabilities
	// object (Rust #44826); nil when unavailable or before initialization.
	serverCapabilities json.RawMessage
	// toolListChanges counts tools/list_changed notifications received on this
	// connection, so a catalog captured before one is never reused (Rust #54656).
	toolListChanges mcpToolListRevision
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
	tools, toolListMeta, err := listMCPStdioTools(client, options)
	// Capabilities are captured during the initialization that the tools call
	// triggers, so read them after the attempt even when discovery failed.
	if client != nil {
		result.ServerCapabilities = client.serverCapabilitiesRaw()
	}
	if err != nil {
		return result, err
	}
	result.Tools = tools
	// Only a successful listing carries catalog metadata (Rust #54686).
	result.ToolListMeta = toolListMeta
	if resources, err := listMCPStdioResources(client, options); err == nil {
		result.Resources = resources
	}
	if templates, err := listMCPStdioResourceTemplates(client, options); err == nil {
		result.ResourceTemplates = templates
	}
	return result, nil
}

func listMCPStdioTools(client *stdioClient, options *stdioCallOptions) ([]MCPToolInfo, json.RawMessage, error) {
	return collectMCPPaginatedWithLimitAndMeta(context.Background(), "tools/list", mcpPaginationTimeout(client.config), mcpCatalogItemLimit(client.config), func(ctx context.Context, cursor *string) ([]MCPToolInfo, json.RawMessage, *string, error) {
		var response struct {
			Tools      []MCPToolInfo   `json:"tools"`
			NextCursor *string         `json:"nextCursor,omitempty"`
			Meta       json.RawMessage `json:"_meta,omitempty"`
		}
		if err := client.CallWithOptionsContext(ctx, options, "tools/list", mcpListParamsForCursorWithConfig(client.config, cursor), &response); err != nil {
			return nil, nil, nil, err
		}
		return response.Tools, response.Meta, response.NextCursor, nil
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
	params := mcpToolCallParams(client.config, tool, arguments, meta)
	var response MCPToolCallResponse
	if err := client.CallWithOptions(&stdioCallOptions{ServerName: serverName, ThreadID: threadID, TurnID: turnID, ItemID: itemID, Roots: roots, Elicitation: elicitation, Progress: progress}, "tools/call", params, &response); err != nil {
		return nil, withMCPRequestErrorCode(err)
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
	if err := client.CallWithOptions(&stdioCallOptions{ServerName: serverName, ThreadID: threadID, Roots: roots, Elicitation: elicitation, Progress: progress}, "resources/read", mcpResourceReadRequestParams(params), &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func newMCPStdioClient(config *ServerConfig) *stdioClient {
	return newMCPStdioClientWithOpenAIForm(config, false)
}

func newMCPStdioClientWithOpenAIForm(config *ServerConfig, openAIForm bool) *stdioClient {
	return newMCPStdioClientWithFeatures(config, openAIForm, false)
}

func newMCPStdioClientWithFeatures(config *ServerConfig, openAIForm bool, userVerification bool) *stdioClient {
	return newMCPStdioClientWithCapabilities(config, mcpClientFeatures{
		OpenAIForm:       openAIForm,
		UserVerification: userVerification,
	})
}

// newMCPStdioClientWithCapabilities builds a client that advertises exactly the
// capabilities projected for its server.
func newMCPStdioClientWithCapabilities(config *ServerConfig, features mcpClientFeatures) *stdioClient {
	cloned := cloneServerConfig(config)
	return &stdioClient{
		config:           &cloned,
		nextID:           1,
		openAIForm:       features.OpenAIForm,
		userVerification: features.UserVerification,
		networkPolicy:    features.NetworkPolicy,
		protocolMode:     cloned.ProtocolMode,
	}
}

// clientFeatures describes the capabilities this connection advertised.
func (c *stdioClient) clientFeatures() mcpClientFeatures {
	if c == nil {
		return mcpClientFeatures{}
	}
	return mcpClientFeatures{
		OpenAIForm:       c.openAIForm,
		AuthChange:       c.authChanges != nil,
		UserVerification: c.userVerification,
		NetworkPolicy:    c.networkPolicy,
	}
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
	if options != nil {
		ctx = contextWithMCPClientContextAndRoots(ctx, options.ThreadID, options.TurnID, options.ItemID, options.Roots)
	}
	// Rust #57196: a shared connection must pause only the calling chat's
	// timeout while it awaits input, so every transport attempt gets the budget
	// that is left after subtracting time spent waiting on the user.
	budget := newMCPCallBudget(c.callTimeout())
	if delegate := c.legacyDelegate(); delegate != nil {
		return delegate.CallWithOptionsContext(ctx, options, method, params, out)
	}
	initCtx, cancelInit := budget.context(ctx)
	err := c.ensureInitialized(initCtx, options)
	cancelInit()
	if err != nil {
		fallbackCtx, cancelFallback := budget.context(ctx)
		fallback := c.tryLegacyFallback(fallbackCtx, options, err)
		cancelFallback()
		if fallback != nil {
			return fallback.CallWithOptionsContext(ctx, options, method, params, out)
		}
		return err
	}
	stderr := c.currentStderr()
	baseParams := params
	requestParams := params
	for round := 0; ; round++ {
		requestCtx, cancelRequest := budget.context(ctx)
		response, err := c.doRequest(requestCtx, options, method, requestParams)
		cancelRequest()
		if err != nil {
			return decorateMCPStdioError(err, stderr)
		}
		if response.Error != nil {
			return newMCPRemoteError(method, response.Error)
		}
		if c.protocolModeSnapshot() == MCPProtocol20260728 {
			// The caller's own timeout is paused while the user answers, so the
			// waiting time is not charged to the request (Rust #57196).
			inputStarted := time.Now()
			nextParams, inputRequired, err := nextMCP2026RequestParams(ctx, serverNameFromStdioOptions(options), elicitationFromStdioOptions(options), baseParams, response.Result)
			if err != nil {
				return err
			}
			if inputRequired {
				// Awaiting the caller's answer must not consume the request's
				// own timeout.
				budget.pause(time.Since(inputStarted))
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

// mcpCallBudget hands out a per-attempt deadline while excluding time a call
// spent waiting for the user's input (Rust #57196).
type mcpCallBudget struct {
	timeout time.Duration
	started time.Time
	paused  time.Duration
}

func newMCPCallBudget(timeout time.Duration) *mcpCallBudget {
	return &mcpCallBudget{timeout: timeout, started: time.Now()}
}

func (b *mcpCallBudget) context(parent context.Context) (context.Context, context.CancelFunc) {
	if b == nil {
		return context.WithCancel(parent)
	}
	remaining := b.timeout - time.Since(b.started) + b.paused
	if remaining <= 0 {
		remaining = time.Nanosecond
	}
	return context.WithTimeout(parent, remaining)
}

func (b *mcpCallBudget) pause(elapsed time.Duration) {
	if b == nil || elapsed <= 0 {
		return
	}
	b.paused += elapsed
}

func (c *stdioClient) doRequest(ctx context.Context, options *stdioCallOptions, method string, params any) (*stdioRPCResponse, error) {
	params = mcpParamsWithProtocolMetadataForFeatures(params, c.protocolModeSnapshot(), c.clientFeatures())
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
		if c.isInitialized() {
			// A ready (possibly shared) connection keeps serving other callers;
			// only this request is cancelled (Rust #57196).
			c.cancelRemoteRequest(cmd, id, ctx.Err())
			return nil, ctx.Err()
		}
		c.failTransportFor(cmd, ctx.Err())
		return nil, ctx.Err()
	}
}

// cancelRemoteRequest asks the server to stop work for one request without
// disturbing the connection. A failed write still tears the transport down.
func (c *stdioClient) cancelRemoteRequest(owner *exec.Cmd, id int64, reason error) {
	message := "cancelled"
	if reason != nil {
		message = reason.Error()
	}
	err := c.writeFrame(map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/cancelled",
		"params": map[string]any{
			"requestId": id,
			"reason":    message,
		},
	})
	if err != nil {
		c.failTransportFor(owner, err)
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

func (c *stdioClient) startAndInitialize(ctx context.Context, options *stdioCallOptions) (err error) {
	started := time.Now()
	negotiatedModern := false
	var configuredMode MCPProtocolMode
	defer func() {
		outcome := "failure"
		if err == nil {
			if negotiatedModern {
				outcome = "modern"
			} else {
				outcome = "legacy"
			}
		}
		recordProtocolDiscoveryMetrics(mcpProtocolDiscoveryModeLabel(configuredMode), outcome, time.Since(started))
	}()
	defer func() {
		// Start auth-change notifications only after initialization succeeds so
		// an opted-in server receives the current revisions (Rust #43428).
		if err == nil {
			c.startMCPAuthChangeWatcher()
		}
	}()
	protocolMode, launchEnv, _, err := mcpStdioLaunchConfig(c.config)
	if err != nil {
		return err
	}
	configuredMode = protocolMode
	if err := c.startProcess(protocolMode, launchEnv, modernMCPMessageLimit(protocolMode)); err != nil {
		return err
	}
	stderr := c.currentStderr()
	discoveryFellBack := false
	if protocolMode.usesModernMCPLifecycle() {
		response, discoverErr := c.discoverModernProtocol(ctx, options)
		switch {
		case discoverErr == nil:
			c.finishModernInitialization(response)
			negotiatedModern = true
			return nil
		case isMCPAuthenticationFailure(discoverErr):
			// Authentication failures are reported as such; neither the legacy
			// handshake nor a fresh helper can recover them (Rust #57210).
			_ = c.Close()
			return decorateMCPStdioError(discoverErr, stderr)
		case isMCPDiscoveryFallbackError(discoverErr):
			// A well-behaved legacy helper answers the fallback initialize on the
			// same pipe, so the working helper is kept (Rust #57210).
			discoveryFellBack = true
			c.mu.Lock()
			c.protocolMode = MCPProtocolLegacy
			c.mu.Unlock()
		case shouldRestartLegacyHandshake(discoverErr):
			// Failed or silent discovery leaves the helper unusable; only a fresh
			// process can run the legacy handshake.
			return c.restartLegacyHandshake(ctx, options, launchEnv)
		default:
			_ = c.Close()
			return decorateMCPStdioError(discoverErr, stderr)
		}
	}

	initErr := c.initializeLegacyHandshake(ctx, options)
	if initErr != nil && discoveryFellBack && shouldRestartLegacyHandshake(initErr) {
		// The legacy helper rejected the in-place initialize because discovery
		// already consumed its first request.
		return c.restartLegacyHandshake(ctx, options, launchEnv)
	}
	if initErr != nil {
		_ = c.Close()
		return decorateMCPStdioError(initErr, stderr)
	}
	return nil
}

// startProcess launches the local helper and installs its transport state. The
// framing limit is chosen by the launch policy, so a restarted legacy handshake
// keeps the bounded framing of the original discovery attempt.
func (c *stdioClient) startProcess(protocolMode MCPProtocolMode, launchEnv map[string]string, messageLimit int64) error {
	// Rust create_env_for_mcp_server: a local stdio server receives only the
	// default variables, the config's env_vars, the custom CA settings, and the
	// explicit env overrides - never the whole parent environment.
	childEnv, err := createEnvForMCPServer(launchEnv, c.config.EnvVars)
	if err != nil {
		return err
	}
	command := resolveMCPStdioCommand(c.config.Command, childEnv)
	cmd := newMCPStdioCommand(command, c.config.Args...)
	// Rust #43870: an escaped descendant can keep the stderr pipe open after the
	// server exits. Drain queued diagnostics briefly, then let Wait close the
	// pipes so descriptors are released.
	cmd.WaitDelay = mcpStdioStderrDrainGrace
	if cwd := strings.TrimSpace(c.config.CWD); cwd != "" {
		cmd.Dir = cwd
	}
	// A non-nil empty slice clears the environment; a nil slice would inherit it.
	cmd.Env = append([]string{}, envPairs(childEnv)...)
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
	process, err := startMCPStdioProcess(cmd)
	if err != nil {
		c.mu.Unlock()
		return err
	}
	c.cmd = cmd
	c.process = process
	c.stdin = stdin
	c.reader = bufio.NewReader(stdout)
	c.stderr = stderr
	c.started = true
	c.initialized = false
	c.startupInputReceived = false
	c.capabilitiesMu.Lock()
	c.serverCapabilities = nil
	c.capabilitiesMu.Unlock()
	c.protocolMode = protocolMode
	c.messageLimit = messageLimit
	c.pending = map[int64]*stdioPendingCall{}
	c.pendingOrder = nil
	// Cancellation state belongs to one connection attempt (Rust #44238).
	c.elicitationCancellations.reset()
	c.mu.Unlock()
	go c.readLoop(cmd, stdout)
	return nil
}

// discoverModernProtocol probes `server/discover` with the probe budget Rust
// reserves so a fresh handshake can still run afterwards.
func (c *stdioClient) discoverModernProtocol(ctx context.Context, options *stdioCallOptions) (*stdioRPCResponse, error) {
	probeCtx, cancel := context.WithTimeout(ctx, mcpDiscoveryProbeTimeout(c.callTimeout()))
	defer cancel()
	response, err := c.doRequest(probeCtx, options, "server/discover", map[string]any{})
	if err != nil {
		return nil, err
	}
	if response != nil && response.Error != nil {
		return nil, newMCPRemoteError("server/discover", response.Error)
	}
	if err := validateMCPModernDiscovery(response.Result); err != nil {
		if isMCPLegacyInitializeResult(response.Result) {
			// A helper that treats its first request as initialize has already
			// spent its startup budget.
			return nil, errMCPModernProtocolUnsupported
		}
		return nil, err
	}
	return response, nil
}

func (c *stdioClient) finishModernInitialization(response *stdioRPCResponse) {
	c.supportsSandboxStateMetaCapability = checkSandboxStateMetaCapability(response.Result)
	c.mu.Lock()
	c.protocolMode = MCPProtocol20260728
	c.initialized = true
	c.capabilitiesMu.Lock()
	c.serverCapabilities = mcpServerCapabilitiesFromInitializeResult(response.Result)
	c.capabilitiesMu.Unlock()
	c.mu.Unlock()
}

func (c *stdioClient) initializeLegacyHandshake(ctx context.Context, options *stdioCallOptions) error {
	response, err := c.doRequest(ctx, options, "initialize", mcpClientInitializeParamsForFeatures(c.clientFeatures()))
	if err != nil {
		return err
	}
	if response != nil && response.Error != nil {
		return newMCPRemoteError("initialize", response.Error)
	}
	c.supportsSandboxStateMetaCapability = checkSandboxStateMetaCapability(response.Result)
	if err := c.writeFrame(map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
		"params":  map[string]any{},
	}); err != nil {
		return err
	}
	c.mu.Lock()
	c.initialized = true
	c.capabilitiesMu.Lock()
	c.serverCapabilities = mcpServerCapabilitiesFromInitializeResult(response.Result)
	c.capabilitiesMu.Unlock()
	c.mu.Unlock()
	return nil
}

// restartLegacyHandshake replaces a helper whose discovery attempt could not be
// completed, waiting for the previous process to release its resources before
// starting the legacy handshake (Rust #57210).
func (c *stdioClient) restartLegacyHandshake(ctx context.Context, options *stdioCallOptions, launchEnv map[string]string) error {
	c.stopProcessForRestart()
	if err := c.startProcess(MCPProtocolLegacy, launchEnv, c.messageLimitSnapshot()); err != nil {
		_ = c.Close()
		return err
	}
	stderr := c.currentStderr()
	if err := c.initializeLegacyHandshake(ctx, options); err != nil {
		_ = c.Close()
		return decorateMCPStdioError(err, stderr)
	}
	return nil
}

// stopProcessForRestart tears down only the current helper so this client can
// start its replacement.
func (c *stdioClient) stopProcessForRestart() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.stopAuthChangeWatcherLocked()
	cmd := c.cmd
	process := c.process
	stdin := c.stdin
	c.cmd = nil
	c.process = nil
	c.stdin = nil
	c.reader = nil
	c.stderr = nil
	c.started = false
	c.initialized = false
	c.mu.Unlock()
	if stdin != nil {
		_ = stdin.Close()
	}
	if cmd != nil && cmd.Process != nil {
		if process != nil {
			process.terminate(cmd)
		} else {
			_ = cmd.Process.Kill()
		}
		_ = waitMCPStdioCommand(cmd)
	}
	if process != nil {
		process.release()
	}
}

// mcpDiscoveryProbeTimeout mirrors Rust's local stdio discovery budget: three
// seconds clamped between half and three quarters of the startup timeout, so a
// fresh legacy handshake still has time to run.
func mcpDiscoveryProbeTimeout(limit time.Duration) time.Duration {
	const preferred = 3 * time.Second
	if limit <= 0 {
		return preferred
	}
	lower := limit / 2
	upper := limit - limit/4
	if preferred < lower {
		return lower
	}
	if preferred > upper {
		return upper
	}
	return preferred
}

// shouldRestartLegacyHandshake reports whether a discovery or fallback failure
// left the helper unusable. Authentication failures never restart: a second
// process cannot fix them, and callers must see the auth error (Rust #57210).
func shouldRestartLegacyHandshake(err error) bool {
	if err == nil || isMCPAuthenticationFailure(err) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.ErrClosedPipe) {
		return true
	}
	if errors.Is(err, errMCPModernProtocolUnsupported) {
		return false
	}
	var remoteErr *MCPRemoteError
	return errors.As(err, &remoteErr) && remoteErr.Code != 0
}

// isMCPAuthenticationFailure recognizes failures that must not be retried
// against a fresh helper.
func isMCPAuthenticationFailure(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, errMCPAuthenticationRequired) || mcpErrAuthenticationRequired(err) {
		return true
	}
	var remoteErr *MCPRemoteError
	return errors.As(err, &remoteErr) && (remoteErr.Code == 401 || remoteErr.Code == 407)
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

// serverCapabilitiesRaw returns the advertised capabilities captured during
// initialization (Rust #44826), or nil when unavailable.
func (c *stdioClient) serverCapabilitiesRaw() json.RawMessage {
	if c == nil {
		return nil
	}
	if delegate := c.legacyDelegate(); delegate != nil {
		return delegate.serverCapabilitiesRaw()
	}
	c.capabilitiesMu.Lock()
	defer c.capabilitiesMu.Unlock()
	return cloneMCPRawMessage(c.serverCapabilities)
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
		data, err := readMCPFrameWithLimit(buffered, c.messageLimitSnapshot())
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
			return c.handleClientNotification(ctx, serverName, progress, envelope.Method, envelope.Params)
		}
		if isMCPElicitationMethod(envelope.Method) {
			// Handle elicitations off the read loop so a server
			// `notifications/cancelled` frame still reaches the pending
			// elicitation instead of waiting behind it (Rust #44238).
			c.dispatchElicitationRequest(ctx, serverName, elicitation, envelope.Method, envelope.ID, envelope.Params)
			return nil
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

// handleClientNotification routes server notifications, remembering
// cancellations that belong to an elicitation before delegating to the shared
// progress handler.
func (c *stdioClient) handleClientNotification(ctx context.Context, serverName string, progress MCPProgressHandler, method string, params json.RawMessage) error {
	// Rust #54656: count a tools/list_changed notification in transport order,
	// before this read loop can deliver any later response.
	recordMCPToolListChangedNotification(&c.toolListChanges, method)
	if strings.TrimSpace(method) == "notifications/cancelled" {

		if id := mcpCancelledNotificationRequestID(params); len(id) > 0 {
			c.elicitationCancellations.remember(id)
		}
		return nil
	}
	return handleMCPClientNotification(ctx, serverName, progress, method, params)
}

// toolsListRevision reports this connection's tool catalog invalidation
// sequence (Rust #54656).
func (c *stdioClient) toolsListRevision() uint64 {
	if c == nil {
		return 0
	}
	if delegate := c.legacyDelegate(); delegate != nil {
		return delegate.toolsListRevision()
	}
	return c.toolListChanges.current()
}

// dispatchElicitationRequest answers a server elicitation on its own goroutine
// so the read loop can keep observing cancellation notifications.
func (c *stdioClient) dispatchElicitationRequest(ctx context.Context, serverName string, elicitation MCPElicitationHandler, method string, id json.RawMessage, params json.RawMessage) {
	if !c.isInitialized() {
		// Client input during startup binds this helper to the chat that
		// answered it, so it must stay private (Rust #57211).
		c.mu.Lock()
		c.startupInputReceived = true
		c.mu.Unlock()
	}
	cmd := c.currentCommand()
	requestID := append(json.RawMessage(nil), id...)
	go func() {
		result := c.elicitationRequestResult(ctx, serverName, elicitation, method, requestID, params)
		if err := c.writeFrame(map[string]any{
			"jsonrpc": "2.0",
			"id":      requestID,
			"result":  result,
		}); err != nil {
			c.failTransportFor(cmd, err)
		}
	}()
}

// elicitationRequestResult returns `cancel` when the elicitation was cancelled
// by its client (ctx) or by a server `notifications/cancelled` for its request
// id, for every elicitation kind rather than only user verification
// (Rust #44238). Remembered cancellations are consumed so a reused request id
// starts fresh.
func (c *stdioClient) elicitationRequestResult(ctx context.Context, serverName string, elicitation MCPElicitationHandler, method string, id json.RawMessage, params json.RawMessage) any {
	userVerification := IsUserVerificationElicitation(parseMCPElicitationRequest(serverName, method, id, params))
	if cancelled, saturated := c.elicitationCancellations.takeWithSaturation(id); cancelled {
		if userVerification {
			reason := UserVerificationReasonInterrupted
			if saturated {
				reason = UserVerificationReasonApprovalUnavailable
			}
			return mcpUserVerificationCancel(reason)
		}
		return &MCPElicitationResponse{Action: MCPElicitationActionCancel}
	}
	if mcpContextCancelled(ctx) {
		if userVerification {
			return mcpUserVerificationCancel(UserVerificationReasonInterrupted)
		}
		return &MCPElicitationResponse{Action: MCPElicitationActionCancel}
	}
	result := mcpElicitationResult(ctx, serverName, elicitation, method, id, params)
	if c.elicitationCancellations.cancelled(id) || mcpContextCancelled(ctx) {
		c.elicitationCancellations.forget(id)
		if userVerification {
			return mcpUserVerificationCancel(UserVerificationReasonInterrupted)
		}
		return &MCPElicitationResponse{Action: MCPElicitationActionCancel}
	}
	return result
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

// isInitialized reports whether the MCP handshake completed for this
// connection (Rust #40636 startup_complete). A ready connection can be reused
// across a startup-timeout config change; a pending startup cannot.
// isShareable reports whether another chat may join this connection: it must
// have negotiated MCP 2026-07-28 and must not depend on the chat that started
// it (Rust #57211).
func (c *stdioClient) isShareable() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.closed && c.initialized && c.delegate == nil && !c.startupInputReceived &&
		c.protocolMode == MCPProtocol20260728
}

func (c *stdioClient) isInitialized() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	delegate := c.delegate
	initialized := c.initialized
	c.mu.Unlock()
	if delegate != nil {
		return delegate.isInitialized()
	}
	return initialized
}

func (c *stdioClient) closeLocked() error {
	c.closed = true
	c.stopAuthChangeWatcherLocked()
	pending := c.pendingCallsLocked()
	stderr := c.stderr
	var waitErr error
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		if c.process != nil {
			c.process.terminate(c.cmd)
		} else {
			_ = c.cmd.Process.Kill()
		}
		waitErr = waitMCPStdioCommand(c.cmd)
	}
	if c.process != nil {
		c.process.release()
	}
	if message := strings.TrimSpace(stderr.String()); waitErr != nil && message != "" {
		waitErr = fmt.Errorf("%w: %s", waitErr, message)
	}
	c.cmd = nil
	c.process = nil
	c.stdin = nil
	c.reader = nil
	c.stderr = nil
	c.started = false
	c.initialized = false
	c.initializing = false
	c.startupInputReceived = false
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
	process := c.process
	stdin := c.stdin
	c.cmd = nil
	c.process = nil
	c.stdin = nil
	c.reader = nil
	c.started = false
	c.initialized = false
	c.initializing = false
	c.stopAuthChangeWatcherLocked()
	if c.initDone != nil {
		close(c.initDone)
		c.initDone = nil
	}
	c.mu.Unlock()
	if stdin != nil {
		_ = stdin.Close()
	}
	if cmd != nil && cmd.Process != nil {
		if process != nil {
			process.terminate(cmd)
		} else {
			_ = cmd.Process.Kill()
		}
		_ = waitMCPStdioCommand(cmd)
	}
	if process != nil {
		process.release()
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
		// WaitDelay closing the pipes after the grace period is not a shutdown
		// failure (Rust #43870).
		if errors.Is(err, exec.ErrWaitDelay) {
			return nil
		}
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
	delegate := c.delegate
	mode := c.protocolMode
	c.mu.Unlock()
	if delegate != nil {
		return delegate.protocolModeSnapshot()
	}
	return mode
}

func (c *stdioClient) messageLimitSnapshot() int64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.messageLimit
}

// legacyDelegate returns the retained legacy connection once an upgrade failed.
func (c *stdioClient) legacyDelegate() *stdioClient {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.delegate
}

// tryLegacyFallback keeps a working legacy connection when the upgrade attempt
// fails for the same account, except on authentication errors (Rust #57210).
func (c *stdioClient) tryLegacyFallback(ctx context.Context, options *stdioCallOptions, err error) *stdioClient {
	if c == nil || err == nil || isMCPAuthenticationFailure(err) {
		return nil
	}
	c.mu.Lock()
	fallback := c.legacyFallback
	c.mu.Unlock()
	if fallback == nil || fallback == c || fallback.isClosed() {
		return nil
	}
	if fallbackErr := fallback.ensureInitialized(ctx, options); fallbackErr != nil {
		return nil
	}
	c.mu.Lock()
	if c.delegate == nil {
		c.delegate = fallback
	}
	delegate := c.delegate
	c.mu.Unlock()
	return delegate
}

// takeLegacyFallback detaches the retained legacy connection for shutdown.
func (c *stdioClient) takeLegacyFallback() *stdioClient {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	fallback := c.legacyFallback
	c.legacyFallback = nil
	return fallback
}

func mcpClientInitializeParams(openAIForm bool) map[string]any {
	return mcpClientInitializeParamsWithAuthChange(openAIForm, false)
}

func mcpClientInitializeParamsWithAuthChange(openAIForm bool, advertiseAuthChange bool) map[string]any {
	return mcpClientInitializeParamsWithFeatures(openAIForm, advertiseAuthChange, false)
}

func mcpClientInitializeParamsWithFeatures(openAIForm bool, advertiseAuthChange bool, userVerification bool) map[string]any {
	return mcpClientInitializeParamsForFeatures(mcpClientFeatures{
		OpenAIForm:       openAIForm,
		AuthChange:       advertiseAuthChange,
		UserVerification: userVerification,
	})
}

func mcpClientInitializeParamsForFeatures(features mcpClientFeatures) map[string]any {
	return map[string]any{
		"protocolVersion": defaultMCPProtocol,
		"capabilities":    mcpClientCapabilitiesForFeatures(features),
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
	case "openai/elicitation/create":
		// Rust dispatches the OpenAI elicitation by its payload mode: the form
		// mode elicits, while a mode this client cannot serve falls through to the
		// default handler (rmcp-client's capability-gated arms).
		if mcpElicitationRequestMode(params) != "form" {
			return nil, &stdioRPCError{Code: -32601, Message: "method not found: " + method}
		}
		return mcpElicitationResult(ctx, serverName, elicitation, method, id, params), nil
	default:
		return nil, &stdioRPCError{Code: -32601, Message: "method not found: " + method}
	}
}

// mcpElicitationRequestMode reads an elicitation payload's `mode`, or "".
func mcpElicitationRequestMode(params json.RawMessage) string {
	var payload struct {
		Mode string `json:"mode"`
	}
	if len(params) == 0 || json.Unmarshal(params, &payload) != nil {
		return ""
	}
	return strings.TrimSpace(payload.Mode)
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
	mode := config.ProtocolMode
	declared, configured := env[mcpProtocolVersionEnvVar]
	handshake, valid := mcpStdioHandshakeMode(mode, env)
	if !valid {
		return MCPProtocolLegacy, nil, true, fmt.Errorf("unsupported %s `%s` for stdio MCP server; expected `%s`", mcpProtocolVersionEnvVar, declared, modernMCPProtocol)
	}
	if configured {
		delete(env, mcpProtocolVersionEnvVar)
	}
	if mode != MCPProtocol20260728 && mode != MCPProtocolDiscoverLocalStdio {
		return MCPProtocolLegacy, env, false, nil
	}
	return handshake, env, true, nil
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
