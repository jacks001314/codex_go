package execserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"codex_go/envutil"
	"codex_go/network"
	"codex_go/sandbox"
	"codex_go/shell"
	"codex_go/utils"
	"github.com/google/uuid"
)

const (
	MethodInitialize              = "initialize"
	MethodInitialized             = "initialized"
	MethodEnvironmentInfo         = "environment/info"
	MethodEnvironmentStatus       = "environment/status"
	MethodProcessStart            = "process/start"
	MethodProcessRead             = "process/read"
	MethodProcessWrite            = "process/write"
	MethodProcessTerminate        = "process/terminate"
	MethodProcessSignal           = "process/signal"
	MethodProcessOutput           = "process/output"
	MethodProcessExited           = "process/exited"
	MethodProcessClosed           = "process/closed"
	MethodFSReadFile              = "fs/readFile"
	MethodFSOpen                  = "fs/open"
	MethodFSReadBlock             = "fs/readBlock"
	MethodFSClose                 = "fs/close"
	MethodFSWriteFile             = "fs/writeFile"
	MethodFSCreateDirectory       = "fs/createDirectory"
	MethodFSGetMetadata           = "fs/getMetadata"
	MethodFSCanonicalize          = "fs/canonicalize"
	MethodFSReadDirectory         = "fs/readDirectory"
	MethodFSWalk                  = "fs/walk"
	MethodFSRemove                = "fs/remove"
	MethodFSCopy                  = "fs/copy"
	MethodHTTPRequest             = "http/request"
	MethodCapabilityRootsDiscover = "capabilityRoots/discoverV1"
	// MethodCapabilitiesDiscover is retained as a source-compatible name for Go callers.
	MethodCapabilitiesDiscover = MethodCapabilityRootsDiscover
	MethodHTTPRequestBodyDelta = "http/request/bodyDelta"
)

func (s *ProcessSandboxType) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	switch ProcessSandboxType(value) {
	case ProcessSandboxNone, ProcessSandboxMacosSeatbelt, ProcessSandboxLinuxSeccomp, ProcessSandboxWindowsRestrictedToken:
		*s = ProcessSandboxType(value)
		return nil
	default:
		return fmt.Errorf("unsupported process sandbox type %q", value)
	}
}

const (
	maxWalkDepth                  = 64
	maxWalkDirectories            = 10000
	maxWalkEntries                = 50000
	maxWalkResponseBytes          = 4 * 1024 * 1024
	walkResponseItemOverhead      = 64
	retainedOutputBytesPerProcess = 1024 * 1024
	retainedWriteIDsPerProcess    = 4096
	maxOpenFileReads              = 128
	maxFileReadHandleIDBytes      = 32
	fileReadChunkSize             = 1024 * 1024
	maxReadFileBytes              = 512 * 1024 * 1024
)

var execServerExitedProcessRetention = 30 * time.Second

type RequestID struct {
	value any
}

func (id *RequestID) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		return errors.New("request id must be a string or integer")
	}
	if data[0] == '"' {
		var value string
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		id.value = value
		return nil
	}
	var value json.Number
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	integer, err := value.Int64()
	if err != nil {
		return errors.New("request id must be a string or integer")
	}
	id.value = integer
	return nil
}

func (id RequestID) MarshalJSON() ([]byte, error) {
	switch value := id.value.(type) {
	case nil:
		return []byte("null"), nil
	case string:
		return json.Marshal(value)
	case json.Number:
		return []byte(value.String()), nil
	case int:
		return json.Marshal(value)
	case int64:
		return json.Marshal(value)
	default:
		return json.Marshal(fmt.Sprint(value))
	}
}

func (id *RequestID) IsZero() bool {
	return id == nil || id.value == nil
}

type request struct {
	ID           RequestID       `json:"id"`
	Method       string          `json:"method"`
	Params       json.RawMessage `json:"params,omitempty"`
	TraceContext *TraceContext   `json:"traceContext,omitempty"`
}

type notification struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type response struct {
	ID           RequestID     `json:"id"`
	Result       any           `json:"result,omitempty"`
	Error        *rpcError     `json:"error,omitempty"`
	TraceContext *TraceContext `json:"traceContext,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type requestFailure struct {
	code    int
	message string
}

func (e *requestFailure) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

type Server struct {
	mu         sync.Mutex
	processes  map[string]*processState
	handles    map[string]*os.File
	httpClient *http.Client
	requests   *serverRequestSender

	registryMu         sync.Mutex
	sessions           map[string]*serverSessionEntry
	detachedSessionTTL time.Duration
}

type serverSessionEntry struct {
	id                   string
	server               *Server
	connectionID         string
	detachedConnectionID string
	detachedExpiresAt    time.Time
}

type processState struct {
	id               string
	cmd              *exec.Cmd
	stdin            io.WriteCloser
	starting         bool
	terminateFn      func() error
	signalFn         func() error
	pipeStdin        bool
	tty              bool
	mu               sync.Mutex
	cond             *sync.Cond
	chunks           []outputChunk
	retainedBytes    int
	nextSeq          uint64
	exited           bool
	exitSequenced    bool
	exitCode         *int
	sandboxType      sandbox.SandboxType
	sandboxDenied    bool
	failure          string
	closed           bool
	closedSequenced  bool
	openStreams      int
	seenWriteIDs     map[string]bool
	seenWriteOrder   []string
	notify           processNotifier
	onClosed         func()
	retention        time.Duration
	retentionOnce    sync.Once
	networkProxy     *network.PreparedProxyManagedNetwork
	policyCancel     context.CancelFunc
	networkCloseOnce sync.Once
}

type processNotifier func(method string, params any)

type CapabilityRootDiscoverRequest struct {
	ID      string                    `json:"id"`
	Path    string                    `json:"path"`
	Sandbox *FileSystemSandboxContext `json:"sandbox,omitempty"`
}

type CapabilityRootsDiscoverParams struct {
	Roots []CapabilityRootDiscoverRequest `json:"roots"`
}

type CapabilityTextFile struct {
	Path     string `json:"path"`
	Contents string `json:"contents"`
}

type DiscoveredPluginFiles struct {
	Manifest   CapabilityTextFile  `json:"manifest"`
	MCPConfig  *CapabilityTextFile `json:"mcpConfig,omitempty"`
	AppsConfig *CapabilityTextFile `json:"appsConfig,omitempty"`
}

type DiscoveredSkillFiles struct {
	Instructions CapabilityTextFile  `json:"instructions"`
	Metadata     *CapabilityTextFile `json:"metadata,omitempty"`
}

type CapabilityRootDiscovery struct {
	ID                 string                 `json:"id"`
	Path               string                 `json:"path"`
	Plugin             *DiscoveredPluginFiles `json:"plugin,omitempty"`
	Skills             []DiscoveredSkillFiles `json:"skills"`
	NamespaceManifests []CapabilityTextFile   `json:"namespaceManifests"`
	Warnings           []string               `json:"warnings"`
	Error              *string                `json:"error,omitempty"`
}

type CapabilityRootsDiscoverResponse struct {
	Roots []CapabilityRootDiscovery `json:"roots"`
}

// Compatibility aliases keep the existing Go API while its wire contract follows Rust V1.
type CapabilityDiscoveryRoot = CapabilityRootDiscoverRequest
type CapabilityDiscoveryParams = CapabilityRootsDiscoverParams
type CapabilityDiscoveryResponse = CapabilityRootsDiscoverResponse

type startedExecServerSandboxProcess struct {
	stdin     io.WriteCloser
	readers   []io.ReadCloser
	wait      func() (int, error)
	terminate func() error
	close     func() error
}

type outputChunk struct {
	Seq    uint64 `json:"seq"`
	Stream string `json:"stream"`
	Chunk  string `json:"chunk"`
}

type ProcessOutputNotification struct {
	ProcessID string `json:"processId"`
	Seq       uint64 `json:"seq"`
	Stream    string `json:"stream"`
	Chunk     string `json:"chunk"`
}

type ProcessExitedNotification struct {
	ProcessID     string `json:"processId"`
	Seq           uint64 `json:"seq"`
	ExitCode      int    `json:"exitCode"`
	SandboxDenied *bool  `json:"sandboxDenied,omitempty"`
}

type ProcessClosedNotification struct {
	ProcessID string `json:"processId"`
	Seq       uint64 `json:"seq"`
}

type InitializeParams struct {
	ClientName      string  `json:"clientName"`
	ResumeSessionID *string `json:"resumeSessionId,omitempty"`
}

type InitializeResponse struct {
	SessionID string `json:"sessionId"`
}

type EnvironmentInfo struct {
	Shell                ShellInfo               `json:"shell"`
	CWD                  *string                 `json:"cwd"`
	TemporaryDirectories []string                `json:"temporaryDirectories,omitempty"`
	Capabilities         EnvironmentCapabilities `json:"capabilities"`
}

type EnvironmentCapabilities struct {
	NetworkProxyLaunch         bool `json:"networkProxyLaunch"`
	CapabilityDiscoverySandbox bool `json:"capabilityDiscoverySandbox"`
	// Rust 646f7c0a91: whether this executor supports environmentConfig/read;
	// defaults to false when deserializing legacy executor responses.
	EnvironmentConfigRead bool `json:"environmentConfigRead"`
	// Rust #38356: whether this executor can stream files while enforcing
	// sandboxed filesystem reads.
	SandboxedFileStreaming bool `json:"sandboxedFileStreaming"`
}

type EnvironmentStatus struct {
	Status EnvironmentStatusKind `json:"status"`
}

type EnvironmentStatusKind string

const (
	EnvironmentStatusReady EnvironmentStatusKind = "ready"
)

type ShellInfo struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type ExecParams struct {
	ProcessID             string                          `json:"processId"`
	Argv                  []string                        `json:"argv"`
	CWD                   string                          `json:"cwd"`
	EnvPolicy             *ExecEnvPolicy                  `json:"envPolicy,omitempty"`
	Env                   map[string]string               `json:"env"`
	TTY                   bool                            `json:"tty"`
	PipeStdin             bool                            `json:"pipeStdin"`
	Arg0                  *string                         `json:"arg0"`
	Sandbox               json.RawMessage                 `json:"sandbox,omitempty"`
	EnforceManagedNetwork bool                            `json:"enforceManagedNetwork,omitempty"`
	ManagedNetwork        *ManagedNetworkSandboxContext   `json:"managedNetwork,omitempty"`
	NetworkProxy          *RemoteNetworkProxyLaunchConfig `json:"networkProxy,omitempty"`
}

type ManagedNetworkSandboxContext struct {
	LoopbackPorts     []uint16 `json:"loopbackPorts"`
	AllowLocalBinding bool     `json:"allowLocalBinding"`
}

type FileSystemSandboxContext struct {
	Permissions                     json.RawMessage                 `json:"permissions"`
	CWD                             string                          `json:"cwd,omitempty"`
	WorkspaceRoots                  []string                        `json:"workspaceRoots,omitempty"`
	WindowsSandboxLevel             string                          `json:"windowsSandboxLevel"`
	WindowsSandboxPrivateDesktop    bool                            `json:"windowsSandboxPrivateDesktop,omitempty"`
	WindowsSandboxProxySettingsMode WindowsSandboxProxySettingsMode `json:"windowsSandboxProxySettingsMode,omitempty"`
	UseLegacyLandlock               bool                            `json:"useLegacyLandlock,omitempty"`
}

type WindowsSandboxProxySettingsMode string

const (
	WindowsSandboxProxySettingsReconcile WindowsSandboxProxySettingsMode = "reconcile"
	WindowsSandboxProxySettingsPreserve  WindowsSandboxProxySettingsMode = "preserve"
)

func (m WindowsSandboxProxySettingsMode) Validate() error {
	switch m {
	case "", WindowsSandboxProxySettingsReconcile, WindowsSandboxProxySettingsPreserve:
		return nil
	default:
		return fmt.Errorf("unsupported Windows sandbox proxy settings mode %q", m)
	}
}

type ExecEnvPolicy struct {
	Inherit               string            `json:"inherit"`
	IgnoreDefaultExcludes bool              `json:"ignoreDefaultExcludes"`
	Exclude               []string          `json:"exclude"`
	Set                   map[string]string `json:"set"`
	IncludeOnly           []string          `json:"includeOnly"`
}

type ExecResponse struct {
	ProcessID   string              `json:"processId"`
	SandboxType *ProcessSandboxType `json:"sandboxType"`
}

type ProcessSandboxType string

const (
	ProcessSandboxNone                   ProcessSandboxType = "none"
	ProcessSandboxMacosSeatbelt          ProcessSandboxType = "macosSeatbelt"
	ProcessSandboxLinuxSeccomp           ProcessSandboxType = "linuxSeccomp"
	ProcessSandboxWindowsRestrictedToken ProcessSandboxType = "windowsRestrictedToken"
)

type ReadParams struct {
	ProcessID string  `json:"processId"`
	AfterSeq  *uint64 `json:"afterSeq"`
	MaxBytes  *int    `json:"maxBytes"`
	WaitMS    *uint64 `json:"waitMs"`
}

type ReadResponse struct {
	Chunks        []outputChunk `json:"chunks"`
	NextSeq       uint64        `json:"nextSeq"`
	Exited        bool          `json:"exited"`
	ExitCode      *int          `json:"exitCode"`
	Closed        bool          `json:"closed"`
	Failure       *string       `json:"failure"`
	SandboxDenied bool          `json:"sandboxDenied"`
}

type WriteParams struct {
	ProcessID string `json:"processId"`
	Chunk     string `json:"chunk"`
	WriteID   string `json:"writeId"`
}

type WriteResponse struct {
	Status string `json:"status"`
}

type TerminateParams struct {
	ProcessID string `json:"processId"`
}

type TerminateResponse struct {
	Running bool `json:"running"`
}

type SignalParams struct {
	ProcessID string `json:"processId"`
	Signal    string `json:"signal"`
}

type SignalResponse struct{}

type FSReadFileParams struct {
	Path           string                    `json:"path"`
	Sandbox        *FileSystemSandboxContext `json:"sandbox,omitempty"`
	FollowSymlinks *bool                     `json:"followSymlinks,omitempty"`
}

type FSReadFileResponse struct {
	DataBase64 string `json:"dataBase64"`
}

type FSOpenParams struct {
	HandleID string                    `json:"handleId"`
	Path     string                    `json:"path"`
	Sandbox  *FileSystemSandboxContext `json:"sandbox,omitempty"`
	// FollowSymlinks controls whether traversal follows links in path
	// components (Rust #39659). Defaults to true; unsandboxed callers can
	// disable it to reject links.
	FollowSymlinks *bool `json:"followSymlinks,omitempty"`
}

type FSOpenResponse struct {
	HandleID string `json:"handleId"`
}

type FSReadBlockParams struct {
	HandleID string `json:"handleId"`
	Offset   uint64 `json:"offset"`
	Len      int    `json:"len"`
}

type FSReadBlockResponse struct {
	Chunk string `json:"chunk"`
	EOF   bool   `json:"eof"`
}

type FSCloseParams struct {
	HandleID string `json:"handleId"`
}

type FSCloseResponse struct{}

type FSWriteFileParams struct {
	Path           string                    `json:"path"`
	DataBase64     string                    `json:"dataBase64"`
	Sandbox        *FileSystemSandboxContext `json:"sandbox,omitempty"`
	FollowSymlinks *bool                     `json:"followSymlinks,omitempty"`
}

type FSWriteFileResponse struct{}

type FSCreateDirectoryParams struct {
	Path      string                    `json:"path"`
	Recursive *bool                     `json:"recursive,omitempty"`
	Sandbox   *FileSystemSandboxContext `json:"sandbox,omitempty"`
}

type FSCreateDirectoryResponse struct{}

type FSGetMetadataParams struct {
	Path    string                    `json:"path"`
	Sandbox *FileSystemSandboxContext `json:"sandbox,omitempty"`
}

type FSGetMetadataResponse struct {
	IsDirectory  bool  `json:"isDirectory"`
	IsFile       bool  `json:"isFile"`
	IsSymlink    bool  `json:"isSymlink"`
	Size         int64 `json:"size"`
	CreatedAtMS  int64 `json:"createdAtMs"`
	ModifiedAtMS int64 `json:"modifiedAtMs"`
}

type FSCanonicalizeParams struct {
	Path    string                    `json:"path"`
	Sandbox *FileSystemSandboxContext `json:"sandbox,omitempty"`
}

type FSCanonicalizeResponse struct {
	Path string `json:"path"`
}

type FSReadDirectoryParams struct {
	Path    string                    `json:"path"`
	Sandbox *FileSystemSandboxContext `json:"sandbox,omitempty"`
}

type FSReadDirectoryEntry struct {
	FileName    string `json:"fileName"`
	IsDirectory bool   `json:"isDirectory"`
	IsFile      bool   `json:"isFile"`
}

type FSReadDirectoryResponse struct {
	Entries []FSReadDirectoryEntry `json:"entries"`
}

type FSWalkParams struct {
	Path    string                    `json:"path"`
	Options FSWalkOptions             `json:"options"`
	Sandbox *FileSystemSandboxContext `json:"sandbox,omitempty"`
}

type FSWalkOptions struct {
	MaxDepth                int  `json:"maxDepth"`
	MaxDirectories          int  `json:"maxDirectories"`
	MaxEntries              int  `json:"maxEntries"`
	FollowDirectorySymlinks bool `json:"followDirectorySymlinks"`
}

type FSWalkEntry struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

type FSWalkError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

type FSWalkResponse struct {
	Entries   []FSWalkEntry `json:"entries"`
	Errors    []FSWalkError `json:"errors"`
	Truncated bool          `json:"truncated"`
}

type FSRemoveParams struct {
	Path      string                    `json:"path"`
	Recursive *bool                     `json:"recursive,omitempty"`
	Force     *bool                     `json:"force,omitempty"`
	Sandbox   *FileSystemSandboxContext `json:"sandbox,omitempty"`
}

type FSRemoveResponse struct{}

type FSCopyParams struct {
	SourcePath      string                    `json:"sourcePath"`
	DestinationPath string                    `json:"destinationPath"`
	Recursive       bool                      `json:"recursive"`
	Sandbox         *FileSystemSandboxContext `json:"sandbox,omitempty"`
}

type FSCopyResponse struct{}

type HTTPHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type HTTPRequestParams struct {
	Method         string       `json:"method"`
	URL            string       `json:"url"`
	Headers        []HTTPHeader `json:"headers"`
	BodyBase64     *string      `json:"bodyBase64,omitempty"`
	TimeoutMS      *uint64      `json:"timeoutMs,omitempty"`
	RedirectPolicy string       `json:"redirectPolicy,omitempty"`
	RequestID      string       `json:"requestId"`
	StreamResponse bool         `json:"streamResponse"`
}

type HTTPRequestResponse struct {
	Status     int          `json:"status"`
	Headers    []HTTPHeader `json:"headers"`
	BodyBase64 string       `json:"bodyBase64"`
}

type HTTPRequestBodyDeltaNotification struct {
	RequestID   string  `json:"requestId"`
	Seq         uint64  `json:"seq"`
	DeltaBase64 string  `json:"deltaBase64"`
	Done        bool    `json:"done"`
	Error       *string `json:"error"`
}

type afterResponseActions struct {
	mu      sync.Mutex
	actions []func()
}

type afterResponseContextKey struct{}

type httpBodyStreamRegistry struct {
	mu     sync.Mutex
	active map[string]bool
}

type httpBodyStreamRegistryContextKey struct{}

type connectionProtocolState struct {
	mu                  sync.Mutex
	initializeRequested bool
	initialized         bool
	connectionID        string
	session             *serverSessionEntry
	detached            chan struct{}
	detachOnce          sync.Once
}

type connectionProtocolStateContextKey struct{}

func withConnectionProtocolState(ctx context.Context) context.Context {
	return context.WithValue(ctx, connectionProtocolStateContextKey{}, &connectionProtocolState{
		connectionID: uuid.NewString(),
		detached:     make(chan struct{}),
	})
}

func protocolStateFromContext(ctx context.Context) *connectionProtocolState {
	if ctx == nil {
		return nil
	}
	state, _ := ctx.Value(connectionProtocolStateContextKey{}).(*connectionProtocolState)
	return state
}

func requireInitializedFor(ctx context.Context, family string) error {
	state := protocolStateFromContext(ctx)
	if state == nil {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.initializeRequested {
		return requestError(-32600, fmt.Sprintf("client must call initialize before using %s methods", family))
	}
	if !state.initialized {
		return requestError(-32600, fmt.Sprintf("client must send initialized before using %s methods", family))
	}
	return nil
}

func withHTTPBodyStreamRegistry(ctx context.Context) context.Context {
	return context.WithValue(ctx, httpBodyStreamRegistryContextKey{}, &httpBodyStreamRegistry{active: map[string]bool{}})
}

func reserveHTTPBodyStream(ctx context.Context, requestID string) (func(), error) {
	registry, _ := ctx.Value(httpBodyStreamRegistryContextKey{}).(*httpBodyStreamRegistry)
	if registry == nil {
		return nil, requestError(-32603, "http/request streaming requires a notification transport")
	}
	registry.mu.Lock()
	if registry.active[requestID] {
		registry.mu.Unlock()
		return nil, requestError(-32600, fmt.Sprintf("http response stream already registered for request %s", requestID))
	}
	registry.active[requestID] = true
	registry.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			registry.mu.Lock()
			delete(registry.active, requestID)
			registry.mu.Unlock()
		})
	}, nil
}

func withAfterResponseActions(ctx context.Context) (context.Context, *afterResponseActions) {
	actions := &afterResponseActions{}
	return context.WithValue(ctx, afterResponseContextKey{}, actions), actions
}

func registerAfterResponseAction(ctx context.Context, action func()) bool {
	if ctx == nil || action == nil {
		return false
	}
	actions, _ := ctx.Value(afterResponseContextKey{}).(*afterResponseActions)
	if actions == nil {
		return false
	}
	actions.mu.Lock()
	actions.actions = append(actions.actions, action)
	actions.mu.Unlock()
	return true
}

func (a *afterResponseActions) run() {
	if a == nil {
		return
	}
	a.mu.Lock()
	actions := append([]func(){}, a.actions...)
	a.actions = nil
	a.mu.Unlock()
	for _, action := range actions {
		action()
	}
}

func NewServer() *Server {
	return NewServerWithHTTPClient(nil)
}

func NewServerWithHTTPClient(httpClient *http.Client) *Server {
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	return &Server{
		processes:          map[string]*processState{},
		handles:            map[string]*os.File{},
		httpClient:         httpClient,
		sessions:           map[string]*serverSessionEntry{},
		detachedSessionTTL: 30 * time.Second,
	}
}

func newSessionServer(httpClient *http.Client) *Server {
	return &Server{
		processes:  map[string]*processState{},
		handles:    map[string]*os.File{},
		httpClient: httpClient,
	}
}

func (s *Server) serverForConnection(ctx context.Context) *Server {
	state := protocolStateFromContext(ctx)
	if state == nil {
		return s
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.session == nil || state.session.server == nil {
		return s
	}
	return state.session.server
}

func (s *Server) attachSession(ctx context.Context, resumeSessionID *string) (*serverSessionEntry, error) {
	state := protocolStateFromContext(ctx)
	if state == nil {
		return nil, nil
	}
	state.mu.Lock()
	connectionID := state.connectionID
	state.mu.Unlock()
	if connectionID == "" {
		connectionID = uuid.NewString()
	}

	s.registryMu.Lock()
	defer s.registryMu.Unlock()
	now := time.Now()
	if resumeSessionID != nil && *resumeSessionID != "" {
		sessionID := *resumeSessionID
		entry := s.sessions[sessionID]
		if entry == nil || (!entry.detachedExpiresAt.IsZero() && !now.Before(entry.detachedExpiresAt)) {
			if entry != nil {
				delete(s.sessions, sessionID)
				go entry.server.shutdown()
			}
			return nil, requestError(-32600, fmt.Sprintf("unknown session id %s", sessionID))
		}
		if entry.connectionID != "" {
			return nil, requestError(-32010, fmt.Sprintf("session %s is already attached to another connection", sessionID))
		}
		entry.connectionID = connectionID
		entry.detachedConnectionID = ""
		entry.detachedExpiresAt = time.Time{}
		entry.server.setConnectionState(processNotifierFromContext(ctx), serverRequestSenderFromContext(ctx))
		state.mu.Lock()
		state.session = entry
		state.mu.Unlock()
		return entry, nil
	}

	sessionID := uuid.NewString()
	entry := &serverSessionEntry{
		id:           sessionID,
		server:       newSessionServer(s.httpClient),
		connectionID: connectionID,
	}
	s.sessions[sessionID] = entry
	state.mu.Lock()
	state.session = entry
	state.mu.Unlock()
	return entry, nil
}

func (s *Server) detachConnection(ctx context.Context) {
	state := protocolStateFromContext(ctx)
	if state == nil {
		return
	}
	state.detachOnce.Do(func() {
		close(state.detached)
	})
	state.mu.Lock()
	entry := state.session
	connectionID := state.connectionID
	state.session = nil
	state.mu.Unlock()
	if entry == nil {
		return
	}

	s.registryMu.Lock()
	if s.sessions[entry.id] != entry || entry.connectionID != connectionID {
		s.registryMu.Unlock()
		return
	}
	entry.connectionID = ""
	entry.detachedConnectionID = connectionID
	entry.detachedExpiresAt = time.Now().Add(s.detachedSessionTTL)
	entry.server.setConnectionState(nil, nil)
	ttl := s.detachedSessionTTL
	s.registryMu.Unlock()

	go func() {
		timer := time.NewTimer(ttl)
		defer timer.Stop()
		<-timer.C
		s.expireDetachedSession(entry.id, connectionID)
	}()
}

func (s *Server) expireDetachedSession(sessionID string, connectionID string) {
	s.registryMu.Lock()
	entry := s.sessions[sessionID]
	if entry == nil || entry.connectionID != "" || entry.detachedConnectionID != connectionID || time.Now().Before(entry.detachedExpiresAt) {
		s.registryMu.Unlock()
		return
	}
	delete(s.sessions, sessionID)
	s.registryMu.Unlock()
	entry.server.shutdown()
}

func (s *Server) setProcessNotifier(notify processNotifier) {
	s.setConnectionState(notify, s.currentRequestSender())
}

func (s *Server) setConnectionState(notify processNotifier, requests *serverRequestSender) {
	s.mu.Lock()
	previousRequests := s.requests
	s.requests = requests
	processes := make([]*processState, 0, len(s.processes))
	for _, process := range s.processes {
		processes = append(processes, process)
	}
	s.mu.Unlock()
	if previousRequests != nil && previousRequests != requests {
		previousRequests.close()
	}
	for _, process := range processes {
		process.mu.Lock()
		process.notify = notify
		process.mu.Unlock()
	}
}

func (s *Server) currentRequestSender() *serverRequestSender {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests
}

func (s *Server) shutdown() {
	s.mu.Lock()
	requests := s.requests
	s.requests = nil
	processes := make([]*processState, 0, len(s.processes))
	for _, process := range s.processes {
		processes = append(processes, process)
	}
	handles := make([]*os.File, 0, len(s.handles))
	for _, handle := range s.handles {
		handles = append(handles, handle)
	}
	s.processes = map[string]*processState{}
	s.handles = map[string]*os.File{}
	s.mu.Unlock()
	if requests != nil {
		requests.close()
	}
	for _, process := range processes {
		process.mu.Lock()
		terminate := process.terminateFn
		if terminate == nil && process.cmd != nil && process.cmd.Process != nil {
			terminate = process.cmd.Process.Kill
		}
		process.notify = nil
		process.mu.Unlock()
		if terminate != nil {
			_ = terminate()
		}
		process.closeNetworkResources()
	}
	for _, handle := range handles {
		_ = handle.Close()
	}
}

func (s *Server) shutdownSessions() {
	s.registryMu.Lock()
	sessions := s.sessions
	s.sessions = map[string]*serverSessionEntry{}
	s.registryMu.Unlock()
	for _, entry := range sessions {
		entry.server.shutdown()
	}
}

func (s *Server) Serve(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
	return s.serveStream(ctx, stdin, stdout, true, false)
}

func (s *Server) serveConnectionStream(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
	return s.serveStream(ctx, stdin, stdout, false, true)
}

func (s *Server) serveStream(ctx context.Context, stdin io.Reader, stdout io.Writer, shutdownSessions bool, concurrentRequests bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if shutdownSessions {
		defer s.shutdownSessions()
	}
	scanner := bufio.NewScanner(stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024*1024+4)
	encoder := json.NewEncoder(stdout)
	var requests sync.WaitGroup
	var writeMu sync.Mutex
	var notifyErr error
	notifyActive := true
	defer func() {
		writeMu.Lock()
		notifyActive = false
		writeMu.Unlock()
	}()
	writeServerMessage := func(value any) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		if !notifyActive {
			return errors.New("exec-server stdio connection is closed")
		}
		if notifyErr != nil {
			return notifyErr
		}
		return encoder.Encode(value)
	}
	requestsSender := newServerRequestSender(writeServerMessage)
	defer requestsSender.close()
	notifyCtx := withServerRequestSender(withConnectionProtocolState(withHTTPBodyStreamRegistry(context.WithValue(ctx, processNotifierContextKey{}, processNotifier(func(method string, params any) {
		writeMu.Lock()
		defer writeMu.Unlock()
		if notifyActive && notifyErr == nil {
			notifyErr = encoder.Encode(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
		}
	})))), requestsSender)
	detached := false
	detach := func() {
		if !detached {
			detached = true
			s.detachConnection(notifyCtx)
		}
	}
	defer detach()
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		requestData := append([]byte(nil), line...)
		if response, accepted := consumeClientResponse(notifyCtx, requestData); response {
			if !accepted {
				break
			}
			continue
		}
		if clientMessageClosesConnection(notifyCtx, requestData) {
			break
		}
		hasID, idErr := lineHasTopLevelID(requestData)
		var envelope struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(requestData, &envelope)
		if idErr != nil || !hasID || envelope.Method == MethodInitialize || !concurrentRequests {
			out, ok := s.handleLineWithLabel(notifyCtx, requestData, "exec-server stdio")
			if !ok {
				continue
			}
			writeMu.Lock()
			err := encoder.Encode(out)
			writeMu.Unlock()
			if err != nil {
				detach()
				requests.Wait()
				return err
			}
			continue
		}
		requests.Add(1)
		go func() {
			defer requests.Done()
			requestCtx, afterResponse := withAfterResponseActions(notifyCtx)
			out, ok := s.handleLineWithLabel(requestCtx, requestData, "exec-server stdio")
			if !ok {
				return
			}
			writeMu.Lock()
			err := encoder.Encode(out)
			writeMu.Unlock()
			afterResponse.run()
			if err != nil {
				writeMu.Lock()
				if notifyErr == nil {
					notifyErr = err
				}
				writeMu.Unlock()
			}
		}()
	}
	detach()
	requests.Wait()
	if err := scanner.Err(); err != nil {
		return err
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	return notifyErr
}

func clientMessageClosesConnection(ctx context.Context, line []byte) bool {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(line, &envelope); err != nil {
		return false
	}
	methodJSON, hasMethod := envelope["method"]
	_, hasID := envelope["id"]
	if hasMethod {
		var method string
		if err := json.Unmarshal(methodJSON, &method); err != nil || hasID {
			return false
		}
		if method != MethodInitialized {
			return true
		}
		state := protocolStateFromContext(ctx)
		if state == nil {
			return true
		}
		state.mu.Lock()
		initializedAllowed := state.initializeRequested && state.session != nil
		state.mu.Unlock()
		return !initializedAllowed
	}
	if !hasID {
		return false
	}
	_, hasResult := envelope["result"]
	_, hasError := envelope["error"]
	return hasResult || hasError
}

func (s *Server) handleLine(ctx context.Context, line []byte) (any, bool) {
	return s.handleLineWithLabel(ctx, line, "exec-server stdio")
}

func (s *Server) handleLineWithLabel(ctx context.Context, line []byte, connectionLabel string) (any, bool) {
	hasID, err := lineHasTopLevelID(line)
	if err != nil {
		return malformedRPCResponse(connectionLabel, err), true
	}
	if hasID {
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			return malformedRPCResponse(connectionLabel, err), true
		}
		if req.Method == "" {
			return malformedRPCResponse(connectionLabel, errors.New("JSON-RPC request is missing method")), true
		}
		trace := TraceContext{}
		if req.TraceContext != nil {
			trace = *req.TraceContext
		}
		if trace.IsZero() {
			trace = NewTraceContext()
		}
		requestCtx := withTraceContext(ctx, trace)
		result, err := s.handleRequest(requestCtx, &req)
		if err != nil {
			return errorResponseForRequest(req.ID, err), true
		}
		return response{ID: req.ID, Result: result, TraceContext: &trace}, true
	}
	var note notification
	if err := json.Unmarshal(line, &note); err != nil {
		return malformedRPCResponse(connectionLabel, err), true
	}
	if note.Method == "" {
		return malformedRPCResponse(connectionLabel, errors.New("JSON-RPC notification is missing method")), true
	}
	if note.Method == MethodInitialized {
		state := protocolStateFromContext(ctx)
		if state != nil {
			state.mu.Lock()
			if state.initializeRequested {
				state.initialized = true
			}
			state.mu.Unlock()
		}
	}
	return nil, false
}

func malformedRPCResponse(connectionLabel string, err error) response {
	messageKind := "JSON-RPC"
	if connectionLabel == "exec-server websocket" {
		messageKind = "websocket JSON-RPC"
	}
	return errorResponse(RequestID{value: -1}, -32600, fmt.Sprintf("failed to parse %s message from %s: %v", messageKind, connectionLabel, err))
}

func lineHasTopLevelID(line []byte) (bool, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(line, &envelope); err != nil {
		return false, err
	}
	_, ok := envelope["id"]
	return ok, nil
}

func (s *Server) handleRequest(ctx context.Context, req *request) (any, error) {
	if family := execServerMethodFamily(req.Method); family != "" {
		if err := requireInitializedFor(ctx, family); err != nil {
			return nil, err
		}
	}
	switch req.Method {
	case MethodInitialize:
		var params InitializeParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		if state := protocolStateFromContext(ctx); state != nil {
			state.mu.Lock()
			if state.initializeRequested {
				state.mu.Unlock()
				return nil, requestError(-32600, "initialize may only be sent once per connection")
			}
			state.initializeRequested = true
			state.mu.Unlock()
		}
		entry, err := s.attachSession(ctx, params.ResumeSessionID)
		if err != nil {
			if state := protocolStateFromContext(ctx); state != nil {
				state.mu.Lock()
				state.initializeRequested = false
				state.mu.Unlock()
			}
			return nil, err
		}
		if entry == nil {
			return InitializeResponse{SessionID: uuid.NewString()}, nil
		}
		return InitializeResponse{SessionID: entry.id}, nil
	case MethodEnvironmentInfo:
		logEnvironmentTrace(ctx, MethodEnvironmentInfo)
		return localEnvironmentInfo(), nil
	case MethodEnvironmentStatus:
		logEnvironmentTrace(ctx, MethodEnvironmentStatus)
		return EnvironmentStatus{Status: EnvironmentStatusReady}, nil
	case MethodCapabilityRootsDiscover:
		var params CapabilityRootsDiscoverParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		return discoverCapabilityRoots(&params)
	case MethodProcessStart:
		var params ExecParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		if err := validateWirePathURI(params.CWD); err != nil {
			return nil, requestError(-32602, "invalid params: cwd must be an absolute file URI: "+err.Error())
		}
		return s.serverForConnection(ctx).startProcess(ctx, &params)
	case MethodProcessRead:
		var params ReadParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.serverForConnection(ctx).readProcessForConnection(ctx, &params)
	case MethodProcessWrite:
		var params WriteParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.serverForConnection(ctx).writeProcess(&params)
	case MethodProcessSignal:
		var params SignalParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.serverForConnection(ctx).signalProcess(&params)
	case MethodProcessTerminate:
		var params TerminateParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.serverForConnection(ctx).terminateProcess(&params)
	case MethodFSReadFile:
		var params FSReadFileParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		if err := validateFSWirePath("path", params.Path, params.Sandbox); err != nil {
			return nil, err
		}
		result, err := readFile(&params)
		return result, mapFSRequestError(err)
	case MethodFSOpen:
		var params FSOpenParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		if err := validateFSWirePath("path", params.Path, params.Sandbox); err != nil {
			return nil, err
		}
		result, err := s.serverForConnection(ctx).openFile(&params)
		return result, mapFSRequestError(err)
	case MethodFSReadBlock:
		var params FSReadBlockParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		result, err := s.serverForConnection(ctx).readBlock(&params)
		return result, mapFSRequestError(err)
	case MethodFSClose:
		var params FSCloseParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		result, err := s.serverForConnection(ctx).closeFile(&params)
		return result, mapFSRequestError(err)
	case MethodFSWriteFile:
		var params FSWriteFileParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		if err := validateFSWirePath("path", params.Path, params.Sandbox); err != nil {
			return nil, err
		}
		result, err := writeFile(&params)
		return result, mapFSRequestError(err)
	case MethodFSCreateDirectory:
		var params FSCreateDirectoryParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		if err := validateFSWirePath("path", params.Path, params.Sandbox); err != nil {
			return nil, err
		}
		result, err := createDirectory(&params)
		return result, mapFSRequestError(err)
	case MethodFSGetMetadata:
		var params FSGetMetadataParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		if err := validateFSWirePath("path", params.Path, params.Sandbox); err != nil {
			return nil, err
		}
		result, err := getMetadata(&params)
		return result, mapFSRequestError(err)
	case MethodFSCanonicalize:
		var params FSCanonicalizeParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		if err := validateFSWirePath("path", params.Path, params.Sandbox); err != nil {
			return nil, err
		}
		result, err := canonicalize(&params)
		return result, mapFSRequestError(err)
	case MethodFSReadDirectory:
		var params FSReadDirectoryParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		if err := validateFSWirePath("path", params.Path, params.Sandbox); err != nil {
			return nil, err
		}
		result, err := readDirectory(&params)
		return result, mapFSRequestError(err)
	case MethodFSWalk:
		var params FSWalkParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		if err := validateFSWirePath("path", params.Path, params.Sandbox); err != nil {
			return nil, err
		}
		result, err := walkPath(&params)
		return result, mapFSRequestError(err)
	case MethodFSRemove:
		var params FSRemoveParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		if err := validateFSWirePath("path", params.Path, params.Sandbox); err != nil {
			return nil, err
		}
		result, err := removePath(&params)
		return result, mapFSRequestError(err)
	case MethodFSCopy:
		var params FSCopyParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		if err := validateFSWirePath("sourcePath", params.SourcePath, params.Sandbox); err != nil {
			return nil, err
		}
		if err := validateWirePathURI(params.DestinationPath); err != nil {
			return nil, requestError(-32602, "invalid params: destinationPath must be an absolute file URI: "+err.Error())
		}
		result, err := copyPath(&params)
		return result, mapFSRequestError(err)
	case MethodHTTPRequest:
		var params HTTPRequestParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		return doHTTPRequest(ctx, &params, s.serverForConnection(ctx).httpClient)
	default:
		return nil, requestError(-32601, fmt.Sprintf("exec-server stub does not implement `%s` yet", req.Method))
	}
}

func execServerMethodFamily(method string) string {
	switch method {
	case MethodEnvironmentInfo:
		return "environment info"
	case MethodEnvironmentStatus:
		return "environment status"
	case MethodCapabilityRootsDiscover:
		return "capability discovery"
	case MethodProcessStart, MethodProcessRead, MethodProcessWrite, MethodProcessTerminate, MethodProcessSignal:
		return "exec"
	case MethodHTTPRequest:
		return "http"
	case MethodFSReadFile, MethodFSOpen, MethodFSReadBlock, MethodFSClose, MethodFSWriteFile, MethodFSCreateDirectory,
		MethodFSGetMetadata, MethodFSCanonicalize, MethodFSReadDirectory, MethodFSWalk, MethodFSRemove, MethodFSCopy:
		return "filesystem"
	default:
		return ""
	}
}

func (s *Server) startProcess(ctx context.Context, params *ExecParams) (*ExecResponse, error) {
	if params == nil {
		return nil, errors.New("process/start params are required")
	}
	if len(params.Argv) == 0 {
		return nil, requestError(-32602, "argv must not be empty")
	}
	if params.EnvPolicy != nil {
		switch params.EnvPolicy.Inherit {
		case "all", "core", "none":
		default:
			return nil, requestError(-32602, fmt.Sprintf("invalid envPolicy.inherit %q", params.EnvPolicy.Inherit))
		}
	}
	preparedParams, preparedProxy, policyCancel, err := s.prepareExecutorNetworkProxy(ctx, params)
	if err != nil {
		return nil, err
	}
	params = preparedParams
	preparedNetworkOwned := false
	defer func() {
		if preparedNetworkOwned {
			return
		}
		if policyCancel != nil {
			policyCancel()
		}
		if preparedProxy != nil {
			_ = preparedProxy.Close()
		}
	}()
	if hasJSONValue(params.Sandbox) {
		state, err := s.reserveProcessState(ctx, params)
		if err != nil {
			return nil, err
		}
		started, supported, startErr := startExecServerSandboxProcess(params)
		if startErr != nil {
			s.releaseStartingProcess(params.ProcessID, state)
			return nil, startErr
		}
		if supported {
			state.mu.Lock()
			state.sandboxType = sandbox.SandboxTypeWindowsRestrictedToken
			state.mu.Unlock()
			state.networkProxy = preparedProxy
			state.policyCancel = policyCancel
			preparedNetworkOwned = true
			if ctx.Err() != nil {
				s.releaseStartingProcess(params.ProcessID, state)
			}
			if !s.activateProcessState(params.ProcessID, state, nil, started.stdin, params.PipeStdin || params.TTY, params.TTY, len(started.readers)) {
				if started.terminate != nil {
					_ = started.terminate()
				}
				if started.close != nil {
					_ = started.close()
				}
				return nil, requestError(-32600, fmt.Sprintf("process %s start was cancelled", params.ProcessID))
			}
			state.mu.Lock()
			state.terminateFn = started.terminate
			if params.TTY && started.stdin != nil {
				state.signalFn = func() error {
					_, err := started.stdin.Write([]byte{3})
					return err
				}
			}
			state.mu.Unlock()
			var readers sync.WaitGroup
			for index, reader := range started.readers {
				if reader == nil {
					state.finishStream()
					continue
				}
				stream := "pty"
				if !params.TTY {
					stream = "stdout"
					if index > 0 {
						stream = "stderr"
					}
				}
				readers.Add(1)
				go func(stream string, reader io.ReadCloser) {
					defer readers.Done()
					defer reader.Close()
					state.capture(stream, reader)
				}(stream, reader)
			}
			go func() {
				code, waitErr := started.wait()
				readers.Wait()
				if started.close != nil {
					if closeErr := started.close(); waitErr == nil && closeErr != nil {
						waitErr = closeErr
					}
				}
				state.finishWithCode(waitErr, &code)
			}()
			return newExecResponse(params.ProcessID, sandbox.SandboxTypeWindowsRestrictedToken), nil
		}
		s.releaseStartingProcess(params.ProcessID, state)
	}
	command, cwd, sandboxType, err := prepareExecProcess(params)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(command[0], command[1:]...)
	if params.Arg0 != nil {
		cmd.Args[0] = *params.Arg0
	}
	cmd.Dir = cwd
	cmd.Env = envPairs(childEnv(params))
	if params.TTY {
		state, err := s.reserveProcessState(ctx, params)
		if err != nil {
			return nil, err
		}
		startedTTY, supported, startErr := startExecServerTTY(cmd)
		if startErr != nil {
			s.releaseStartingProcess(params.ProcessID, state)
			return nil, startErr
		}
		if supported {
			state.mu.Lock()
			state.sandboxType = sandboxType
			state.mu.Unlock()
			state.networkProxy = preparedProxy
			state.policyCancel = policyCancel
			preparedNetworkOwned = true
			if ctx.Err() != nil {
				s.releaseStartingProcess(params.ProcessID, state)
			}
			if !s.activateProcessState(params.ProcessID, state, cmd, startedTTY.stdin, params.PipeStdin || params.TTY, params.TTY, 1) {
				if startedTTY.kill != nil {
					_ = startedTTY.kill()
				}
				if startedTTY.wait != nil {
					_, _ = startedTTY.wait()
				}
				if startedTTY.closePTY != nil {
					_ = startedTTY.closePTY()
				}
				_ = startedTTY.reader.Close()
				if startedTTY.cleanup != nil {
					_ = startedTTY.cleanup()
				}
				return nil, requestError(-32600, fmt.Sprintf("process %s start was cancelled", params.ProcessID))
			}
			state.mu.Lock()
			state.terminateFn = startedTTY.kill
			state.signalFn = func() error {
				_, err := startedTTY.stdin.Write([]byte{3})
				return err
			}
			state.mu.Unlock()
			captureDone := make(chan struct{})
			go func() {
				defer close(captureDone)
				state.capture("pty", startedTTY.reader)
				_ = startedTTY.reader.Close()
			}()
			go func() {
				code, waitErr := startedTTY.wait()
				if startedTTY.closePTY != nil {
					_ = startedTTY.closePTY()
				}
				select {
				case <-captureDone:
				case <-time.After(2 * time.Second):
					_ = startedTTY.reader.Close()
					<-captureDone
				}
				if startedTTY.cleanup != nil {
					_ = startedTTY.cleanup()
				}
				state.finishWithCode(waitErr, &code)
			}()
			return newExecResponse(params.ProcessID, sandboxType), nil
		}
		s.releaseStartingProcess(params.ProcessID, state)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	var stdin io.WriteCloser
	if params.PipeStdin || params.TTY {
		stdin, err = cmd.StdinPipe()
		if err != nil {
			return nil, err
		}
	}
	state, err := s.reserveProcessState(ctx, params)
	if err != nil {
		if stdin != nil {
			_ = stdin.Close()
		}
		return nil, err
	}
	state.networkProxy = preparedProxy
	state.policyCancel = policyCancel
	state.sandboxType = sandboxType
	preparedNetworkOwned = true
	if ctx.Err() != nil {
		s.releaseStartingProcess(params.ProcessID, state)
		if stdin != nil {
			_ = stdin.Close()
		}
		_ = stdoutPipe.Close()
		_ = stderrPipe.Close()
		return nil, requestError(-32600, fmt.Sprintf("process %s start was cancelled", params.ProcessID))
	}
	if err := cmd.Start(); err != nil {
		s.releaseStartingProcess(params.ProcessID, state)
		if stdin != nil {
			_ = stdin.Close()
		}
		return nil, err
	}
	if ctx.Err() != nil {
		s.releaseStartingProcess(params.ProcessID, state)
	}
	if !s.activateProcessState(params.ProcessID, state, cmd, stdin, params.PipeStdin || params.TTY, params.TTY, 2) {
		if stdin != nil {
			_ = stdin.Close()
		}
		_ = stdoutPipe.Close()
		_ = stderrPipe.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, requestError(-32600, fmt.Sprintf("process %s start was cancelled", params.ProcessID))
	}
	stdoutStream := "stdout"
	stderrStream := "stderr"
	if params.TTY {
		stdoutStream = "pty"
		stderrStream = "pty"
	}
	go state.capture(stdoutStream, stdoutPipe)
	go state.capture(stderrStream, stderrPipe)
	go func() {
		err := cmd.Wait()
		state.finish(err)
	}()
	return newExecResponse(params.ProcessID, sandboxType), nil
}

func newExecResponse(processID string, sandboxType sandbox.SandboxType) *ExecResponse {
	wire := processSandboxTypeToProtocol(sandboxType)
	return &ExecResponse{ProcessID: processID, SandboxType: &wire}
}

func processSandboxTypeToProtocol(sandboxType sandbox.SandboxType) ProcessSandboxType {
	switch sandboxType {
	case sandbox.SandboxTypeMacosSeatbelt:
		return ProcessSandboxMacosSeatbelt
	case sandbox.SandboxTypeLinuxSeccomp:
		return ProcessSandboxLinuxSeccomp
	case sandbox.SandboxTypeWindowsRestrictedToken:
		return ProcessSandboxWindowsRestrictedToken
	default:
		return ProcessSandboxNone
	}
}

func SandboxTypeFromProtocol(sandboxType *ProcessSandboxType) *sandbox.SandboxType {
	if sandboxType == nil {
		return nil
	}
	value := sandbox.SandboxTypeNone
	switch *sandboxType {
	case ProcessSandboxMacosSeatbelt:
		value = sandbox.SandboxTypeMacosSeatbelt
	case ProcessSandboxLinuxSeccomp:
		value = sandbox.SandboxTypeLinuxSeccomp
	case ProcessSandboxWindowsRestrictedToken:
		value = sandbox.SandboxTypeWindowsRestrictedToken
	}
	return &value
}

func (s *Server) reserveProcessState(ctx context.Context, params *ExecParams) (*processState, error) {
	state := &processState{
		id:           params.ProcessID,
		starting:     true,
		nextSeq:      1,
		seenWriteIDs: map[string]bool{},
		notify:       processNotifierFromContext(ctx),
		retention:    execServerExitedProcessRetention,
	}
	state.onClosed = func() {
		timer := time.NewTimer(state.retention)
		defer timer.Stop()
		<-timer.C
		s.mu.Lock()
		if s.processes[params.ProcessID] == state {
			delete(s.processes, params.ProcessID)
		}
		s.mu.Unlock()
	}
	state.cond = sync.NewCond(&state.mu)
	s.mu.Lock()
	if s.processes[params.ProcessID] != nil {
		s.mu.Unlock()
		return nil, requestError(-32600, fmt.Sprintf("process %s already exists", params.ProcessID))
	}
	s.processes[params.ProcessID] = state
	s.mu.Unlock()
	return state, nil
}

func (s *Server) activateProcessState(processID string, state *processState, cmd *exec.Cmd, stdin io.WriteCloser, pipeStdin bool, tty bool, openStreams int) bool {
	s.mu.Lock()
	if s.processes[processID] != state {
		s.mu.Unlock()
		return false
	}
	state.mu.Lock()
	state.cmd = cmd
	state.stdin = stdin
	state.pipeStdin = pipeStdin
	state.tty = tty
	state.openStreams = openStreams
	state.starting = false
	state.cond.Broadcast()
	state.mu.Unlock()
	s.mu.Unlock()
	return true
}

func (s *Server) releaseStartingProcess(processID string, state *processState) {
	s.mu.Lock()
	if s.processes[processID] == state {
		delete(s.processes, processID)
	}
	s.mu.Unlock()
	state.closeNetworkResources()
}

func (s *Server) prepareExecutorNetworkProxy(ctx context.Context, params *ExecParams) (*ExecParams, *network.PreparedProxyManagedNetwork, context.CancelFunc, error) {
	if params == nil || params.NetworkProxy == nil {
		return params, nil, nil, nil
	}
	launch := params.NetworkProxy
	if launch.PolicyDecisionTimeoutMS != nil && *launch.PolicyDecisionTimeoutMS == 0 {
		return nil, nil, nil, requestError(-32602, "network policy decision callback timeout must be nonzero")
	}
	if launch.PolicyDecisionTimeoutMS != nil && (params.ProcessID == "" || len(params.ProcessID) > MaxNetworkPolicyProcessIDBytes) {
		return nil, nil, nil, requestError(-32602, fmt.Sprintf("callback-enabled process ID must be non-empty and at most %d bytes", MaxNetworkPolicyProcessIDBytes))
	}
	preparedParams := *params
	preparedParams.Env = childEnv(params)
	preparedParams.EnvPolicy = nil
	if runtime.GOOS == "windows" && !hasJSONValue(params.Sandbox) {
		stripManagedProxyEnv(preparedParams.Env)
		preparedParams.NetworkProxy = nil
		preparedParams.ManagedNetwork = nil
		preparedParams.EnforceManagedNetwork = false
		return &preparedParams, nil, nil, nil
	}
	proxyConfig, err := launch.Proxy.ProxyConfig()
	if err != nil {
		return nil, nil, nil, requestError(-32602, "invalid network proxy config: "+err.Error())
	}
	proxyConfig.EnvironmentID = stringValue(launch.EnvironmentID)
	proxyConfig.AuditMetadata = network.ProxyAuditMetadata{
		ConversationID: launch.AuditMetadata.ConversationID,
		AppVersion:     launch.AuditMetadata.AppVersion,
		UserAccountID:  launch.AuditMetadata.UserAccountID,
		AuthMode:       launch.AuditMetadata.AuthMode,
		Originator:     launch.AuditMetadata.Originator,
		UserEmail:      launch.AuditMetadata.UserEmail,
		TerminalType:   launch.AuditMetadata.TerminalType,
		Model:          launch.AuditMetadata.Model,
		Slug:           launch.AuditMetadata.Slug,
	}
	policyCtx, policyCancel := context.WithCancel(context.Background())
	if launch.PolicyDecisionTimeoutMS != nil {
		controllerTimeout := time.Duration(*launch.PolicyDecisionTimeoutMS) * time.Millisecond
		processID := params.ProcessID
		proxyConfig.PolicyDecider = network.ProxyPolicyDeciderFunc(func(requestCtx context.Context, request network.ProxyPolicyRequest) network.ProxyDecision {
			if request.Host == "" || len(request.Host) > MaxNetworkPolicyHostBytes || containsControlOrWhitespace(request.Host) || request.Port == 0 {
				return network.DenyProxyDecision(NetworkPolicyDenialReason)
			}
			sender := s.currentRequestSender()
			if sender == nil {
				return network.DenyProxyDecision(NetworkPolicyDenialReason)
			}
			callCtx, cancel := context.WithCancel(requestCtx)
			defer cancel()
			go func() {
				select {
				case <-policyCtx.Done():
					cancel()
				case <-callCtx.Done():
				}
			}()
			var response NetworkPolicyRequestResponse
			err := sender.call(callCtx, MethodNetworkPolicyRequest, NetworkPolicyRequestParams{
				ProcessID: processID,
				Request:   NetworkPolicyRequestFromProxy(request),
			}, controllerTimeout+networkPolicyTransportTimeoutMargin, &response)
			if err != nil {
				return network.DenyProxyDecision(NetworkPolicyDenialReason)
			}
			switch response.Decision.Type {
			case "allow":
				return network.AllowProxyDecision()
			case "ask":
				return network.AskProxyDecision(response.Decision.Reason)
			case "deny":
				return network.DenyProxyDecision(response.Decision.Reason)
			default:
				return network.DenyProxyDecision(NetworkPolicyDenialReason)
			}
		})
	}
	// Rust #38670: forward final executor-local proxy policy decisions to the
	// controller as best-effort network/policyDecision notifications so audit
	// events carry controller-trusted session and execution metadata.
	processID := params.ProcessID
	proxyConfig.AuditSink = func(event network.ProxyPolicyAuditEvent) {
		notify := processNotifierFromContext(ctx)
		if notify == nil || strings.TrimSpace(processID) == "" {
			return
		}
		var method *string
		if strings.TrimSpace(event.Method) != "" {
			value := event.Method
			method = &value
		}
		var client *string
		if strings.TrimSpace(event.Client) != "" {
			value := event.Client
			client = &value
		}
		notify(MethodNetworkPolicyDecision, NetworkPolicyDecisionNotification{
			ProcessID:      processID,
			Timestamp:      event.Timestamp,
			Scope:          event.Scope,
			Decision:       event.Decision,
			Source:         string(event.Source),
			Reason:         event.Reason,
			Protocol:       NetworkPolicyRequestFromProxy(event.Request).Protocol,
			Host:           event.Request.Host,
			Port:           event.Request.Port,
			Method:         method,
			Client:         client,
			PolicyOverride: event.PolicyOverride,
		})
	}
	prepared, err := network.StartProxyManagedNetwork(context.Background(), proxyConfig, preparedParams.Env)
	if err != nil {
		policyCancel()
		return nil, nil, nil, requestError(-32603, "failed to start executor network proxy: "+err.Error())
	}
	preparedParams.Env = prepared.EnvSnapshot()
	sandboxContext := prepared.SandboxContextSnapshot()
	preparedParams.ManagedNetwork = &ManagedNetworkSandboxContext{
		LoopbackPorts:     append([]uint16(nil), sandboxContext.LoopbackPorts...),
		AllowLocalBinding: sandboxContext.AllowLocalBinding,
	}
	preparedParams.EnforceManagedNetwork = true
	return &preparedParams, prepared, policyCancel, nil
}

func stripManagedProxyEnv(env map[string]string) {
	for _, key := range append(append([]string{}, network.ProxyURLEnvKeys...), network.NoProxyEnvKeys...) {
		delete(env, key)
		delete(env, strings.ToLower(key))
	}
	delete(env, network.ProxyActiveEnvKey)
	delete(env, network.ProxyAllowLocalBindingEnvKey)
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func prepareExecProcess(params *ExecParams) ([]string, string, sandbox.SandboxType, error) {
	command := append([]string(nil), params.Argv...)
	cwd, err := nativeExecServerPath(params.CWD, "cwd")
	if err != nil {
		return nil, "", sandbox.SandboxTypeNone, err
	}
	if cwd == "" {
		cwd, err = os.Getwd()
		if err != nil {
			return nil, "", sandbox.SandboxTypeNone, err
		}
	}
	if !hasJSONValue(params.Sandbox) {
		return command, cwd, sandbox.SandboxTypeNone, nil
	}
	if params.EnforceManagedNetwork && params.ManagedNetwork == nil {
		return nil, "", sandbox.SandboxTypeNone, requestError(-32602, "managed network enforcement requires managedNetwork context")
	}
	if runtime.GOOS == "windows" {
		return nil, "", sandbox.SandboxTypeNone, requestError(-32602, "sandboxed remote process launch is not supported on Windows")
	}
	var sandboxContext FileSystemSandboxContext
	if err := json.Unmarshal(params.Sandbox, &sandboxContext); err != nil {
		return nil, "", sandbox.SandboxTypeNone, requestError(-32602, fmt.Sprintf("invalid sandbox context: %v", err))
	}
	if !hasJSONValue(sandboxContext.Permissions) {
		return nil, "", sandbox.SandboxTypeNone, requestError(-32602, "invalid sandbox context: permissions are required")
	}
	if err := sandboxContext.WindowsSandboxProxySettingsMode.Validate(); err != nil {
		return nil, "", sandbox.SandboxTypeNone, requestError(-32602, "invalid sandbox context: "+err.Error())
	}
	sandboxCWD := cwd
	if strings.TrimSpace(sandboxContext.CWD) != "" {
		sandboxCWD, err = nativeExecServerPath(sandboxContext.CWD, "sandbox cwd")
		if err != nil {
			return nil, "", sandbox.SandboxTypeNone, err
		}
	}
	profileJSON, err := nativePermissionProfileJSON(sandboxContext.Permissions)
	if err != nil {
		return nil, "", sandbox.SandboxTypeNone, requestError(-32602, fmt.Sprintf("invalid sandbox permission path URI: %v", err))
	}
	profile, err := sandbox.ParseRuntimePermissionProfileJSON(profileJSON)
	if err != nil {
		return nil, "", sandbox.SandboxTypeNone, requestError(-32602, fmt.Sprintf("invalid sandbox permission profile: %v", err))
	}
	plan, err := sandbox.BuildCommandRunPlan(&sandbox.CommandRunRequest{
		ResolvedPermissionProfile:     profile,
		ResolvedPermissionProfileID:   "exec-server",
		ResolvedPermissionProfileJSON: profileJSON,
		CWD:                           cwd,
		UseLegacyLandlock:             sandboxContext.UseLegacyLandlock,
		AllowNetworkForProxy:          params.EnforceManagedNetwork,
		Command:                       command,
	})
	if err != nil {
		return nil, "", sandbox.SandboxTypeNone, requestError(-32602, fmt.Sprintf("failed to prepare process sandbox: %v", err))
	}
	if err := plan.UnsupportedError(); err != nil {
		return nil, "", sandbox.SandboxTypeNone, requestError(-32602, "sandbox intent cannot be enforced on this executor")
	}
	if profile.Disabled {
		return nil, "", sandbox.SandboxTypeNone, requestError(-32602, "sandbox intent cannot be enforced on this executor")
	}
	plan.CWD = cwd
	_ = sandboxCWD
	if runtime.GOOS == "linux" && sandboxCWD != cwd {
		wrapped, wrapErr := sandbox.CreateLinuxSandboxCommandArgsForPermissionProfileJSON(
			command,
			cwd,
			profileJSON,
			sandboxCWD,
			sandboxContext.UseLegacyLandlock,
			params.EnforceManagedNetwork,
		)
		if wrapErr != nil {
			return nil, "", sandbox.SandboxTypeNone, requestError(-32602, fmt.Sprintf("failed to prepare process sandbox: %v", wrapErr))
		}
		plan.Command = wrapped
	}
	return plan.Command, plan.CWD, plan.SandboxType, nil
}

func nativeExecServerPath(raw string, label string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if len(raw) < len("file:") || !strings.EqualFold(raw[:len("file:")], "file:") {
		return raw, nil
	}
	uri, err := utils.Parse(raw)
	if err != nil {
		return "", requestError(-32602, fmt.Sprintf("%s URI `%s` is not valid on this exec-server host: %v", label, raw, err))
	}
	native, err := uri.HostNativePath()
	if err != nil {
		return "", requestError(-32602, fmt.Sprintf("%s URI `%s` is not valid on this exec-server host: %v", label, raw, err))
	}
	return native, nil
}

func nativePermissionProfileJSON(raw json.RawMessage) (string, error) {
	var profile any
	if err := json.Unmarshal(raw, &profile); err != nil {
		return "", err
	}
	if err := rewritePermissionProfilePaths(profile, func(path string) (string, error) {
		if len(path) < len("file:") || !strings.EqualFold(path[:len("file:")], "file:") {
			return path, nil
		}
		uri, err := utils.Parse(path)
		if err != nil {
			return "", err
		}
		return uri.HostNativePath()
	}); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(profile)
	return string(encoded), err
}

func rewritePermissionProfilePaths(value any, rewrite func(string) (string, error)) error {
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	fileSystem, _ := object["file_system"].(map[string]any)
	entries, _ := fileSystem["entries"].([]any)
	for _, entryValue := range entries {
		entry, _ := entryValue.(map[string]any)
		pathObject, _ := entry["path"].(map[string]any)
		if pathObject["type"] != "path" {
			continue
		}
		path, _ := pathObject["path"].(string)
		if path == "" {
			continue
		}
		rewritten, err := rewrite(path)
		if err != nil {
			return err
		}
		pathObject["path"] = rewritten
	}
	return nil
}

func (s *Server) readProcess(params *ReadParams) (*ReadResponse, error) {
	return s.readProcessForConnection(context.Background(), params)
}

func (s *Server) readProcessForConnection(ctx context.Context, params *ReadParams) (*ReadResponse, error) {
	if params.MaxBytes != nil && *params.MaxBytes < 0 {
		return nil, requestError(-32602, "maxBytes must not be negative")
	}
	state := s.lookup(params.ProcessID)
	if state == nil {
		return nil, requestError(-32600, fmt.Sprintf("unknown process id %s", params.ProcessID))
	}
	state.mu.Lock()
	starting := state.starting
	state.mu.Unlock()
	if starting {
		return nil, requestError(-32600, fmt.Sprintf("process id %s is starting", params.ProcessID))
	}
	after := uint64(0)
	if params.AfterSeq != nil {
		after = *params.AfterSeq
	}
	var detached <-chan struct{}
	if protocol := protocolStateFromContext(ctx); protocol != nil {
		detached = protocol.detached
	}
	response, attached := state.read(after, params.MaxBytes, params.WaitMS, detached)
	if !attached {
		return nil, requestError(-32600, "session has been resumed by another connection")
	}
	return response, nil
}

func (s *Server) writeProcess(params *WriteParams) (*WriteResponse, error) {
	if params.WriteID == "" {
		return nil, requestError(-32602, "writeId must not be empty")
	}
	if _, err := base64.StdEncoding.DecodeString(params.Chunk); err != nil {
		return nil, requestError(-32602, "invalid base64 process input: "+err.Error())
	}
	state := s.lookup(params.ProcessID)
	if state == nil {
		return &WriteResponse{Status: "unknownProcess"}, nil
	}
	return state.write(params)
}

func (s *Server) terminateProcess(params *TerminateParams) (*TerminateResponse, error) {
	s.mu.Lock()
	state := s.processes[params.ProcessID]
	if state == nil {
		s.mu.Unlock()
		return &TerminateResponse{Running: false}, nil
	}
	state.mu.Lock()
	starting := state.starting
	state.mu.Unlock()
	if starting {
		delete(s.processes, params.ProcessID)
		s.mu.Unlock()
		state.closeNetworkResources()
		return &TerminateResponse{Running: true}, nil
	}
	s.mu.Unlock()
	return state.terminate(), nil
}

func (s *Server) signalProcess(params *SignalParams) (*SignalResponse, error) {
	if params.Signal != "interrupt" {
		return nil, requestError(-32602, fmt.Sprintf("unsupported process signal %s", params.Signal))
	}
	state := s.lookup(params.ProcessID)
	if state == nil {
		return &SignalResponse{}, nil
	}
	return state.signal(params)
}

func (s *Server) lookup(processID string) *processState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.processes[processID]
}

func readFile(params *FSReadFileParams) (*FSReadFileResponse, error) {
	var sandboxed FSReadFileResponse
	if ran, err := runSandboxedFSOperation(params.Sandbox, MethodFSReadFile, params, &sandboxed); ran {
		if err != nil {
			return nil, err
		}
		return &sandboxed, nil
	}
	path, err := resolvePath(params.Path)
	if err != nil {
		return nil, err
	}
	if params != nil && params.FollowSymlinks != nil && !*params.FollowSymlinks {
		if linkPath, err := noFollowSymlinkComponent(path); err != nil {
			return nil, err
		} else if linkPath != "" {
			return nil, requestError(-32600, fmt.Sprintf("fs/open path %s traverses symlink %s", params.Path, linkPath))
		}
	}
	file, err := openRegularFileForRead(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > maxReadFileBytes {
		return nil, requestError(-32600, fmt.Sprintf("file is too large to read: limit is %d bytes", maxReadFileBytes))
	}
	data, err := io.ReadAll(io.LimitReader(file, maxReadFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxReadFileBytes {
		return nil, requestError(-32600, fmt.Sprintf("file is too large to read: limit is %d bytes", maxReadFileBytes))
	}
	return &FSReadFileResponse{DataBase64: base64.StdEncoding.EncodeToString(data)}, nil
}

func (s *Server) openFile(params *FSOpenParams) (*FSOpenResponse, error) {
	if params == nil {
		return nil, errors.New("fs/open params are required")
	}
	if err := validateFileReadHandleID(params.HandleID); err != nil {
		return nil, err
	}
	if required, _, _, _, _, err := prepareFSSandbox(params.Sandbox); err != nil {
		return nil, err
	} else if required {
		return nil, requestError(-32600, "streaming file reads do not support platform sandboxing")
	}
	path, err := resolvePath(params.Path)
	if err != nil {
		return nil, err
	}
	if params != nil && params.FollowSymlinks != nil && !*params.FollowSymlinks {
		if linkPath, err := noFollowSymlinkComponent(path); err != nil {
			return nil, err
		} else if linkPath != "" {
			return nil, requestError(-32600, fmt.Sprintf("fs/open path %s traverses symlink %s", params.Path, linkPath))
		}
	}
	file, err := openRegularFileForRead(path)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.handles[params.HandleID] != nil {
		s.mu.Unlock()
		_ = file.Close()
		return nil, requestError(-32600, fmt.Sprintf("file read handle `%s` already exists", params.HandleID))
	}
	if len(s.handles) >= maxOpenFileReads {
		s.mu.Unlock()
		_ = file.Close()
		return nil, requestError(-32600, fmt.Sprintf("at most %d file reads may be open per connection", maxOpenFileReads))
	}
	s.handles[params.HandleID] = file
	s.mu.Unlock()
	return &FSOpenResponse{HandleID: params.HandleID}, nil
}

func (s *Server) readBlock(params *FSReadBlockParams) (*FSReadBlockResponse, error) {
	if params == nil {
		return nil, errors.New("fs/readBlock params are required")
	}
	if err := validateFileReadHandleID(params.HandleID); err != nil {
		return nil, err
	}
	if params.Len < 1 || params.Len > fileReadChunkSize {
		return nil, requestError(-32600, fmt.Sprintf("file read block length must be between 1 and %d", fileReadChunkSize))
	}
	if params.Offset > uint64(^uint64(0)>>1) {
		return nil, requestError(-32600, "file read offset overflowed")
	}
	s.mu.Lock()
	file := s.handles[params.HandleID]
	s.mu.Unlock()
	if file == nil {
		return nil, requestError(-32004, fmt.Sprintf("unknown file read handle `%s`", params.HandleID))
	}
	buffer := make([]byte, params.Len)
	bytesRead := 0
	for bytesRead < params.Len {
		readOffset := params.Offset + uint64(bytesRead)
		if readOffset > uint64(^uint64(0)>>1) {
			s.closeHandleAfterReadError(params.HandleID, file)
			return nil, requestError(-32600, "file read offset overflowed")
		}
		n, err := file.ReadAt(buffer[bytesRead:], int64(readOffset))
		bytesRead += n
		if errors.Is(err, io.EOF) || n == 0 {
			break
		}
		if err != nil {
			s.closeHandleAfterReadError(params.HandleID, file)
			return nil, err
		}
	}
	return &FSReadBlockResponse{
		Chunk: base64.StdEncoding.EncodeToString(buffer[:bytesRead]),
		EOF:   bytesRead < params.Len,
	}, nil
}

func (s *Server) closeFile(params *FSCloseParams) (*FSCloseResponse, error) {
	if params == nil {
		return nil, errors.New("fs/close params are required")
	}
	if err := validateFileReadHandleID(params.HandleID); err != nil {
		return nil, err
	}
	s.mu.Lock()
	file := s.handles[params.HandleID]
	delete(s.handles, params.HandleID)
	s.mu.Unlock()
	if file != nil {
		if err := file.Close(); err != nil {
			return nil, err
		}
	}
	return &FSCloseResponse{}, nil
}

func (s *Server) closeHandleAfterReadError(handleID string, file *os.File) {
	s.mu.Lock()
	if s.handles[handleID] == file {
		delete(s.handles, handleID)
	}
	s.mu.Unlock()
	_ = file.Close()
}

func validateFileReadHandleID(handleID string) error {
	if len(handleID) > maxFileReadHandleIDBytes {
		return requestError(-32600, fmt.Sprintf("file read handle ID must not exceed %d bytes", maxFileReadHandleIDBytes))
	}
	return nil
}

func writeFile(params *FSWriteFileParams) (*FSWriteFileResponse, error) {
	var sandboxed FSWriteFileResponse
	if ran, err := runSandboxedFSOperation(params.Sandbox, MethodFSWriteFile, params, &sandboxed); ran {
		if err != nil {
			return nil, err
		}
		return &sandboxed, nil
	}
	path, err := resolvePath(params.Path)
	if err != nil {
		return nil, err
	}
	if params != nil && params.FollowSymlinks != nil && !*params.FollowSymlinks {
		if linkPath, err := noFollowSymlinkComponent(path); err != nil {
			return nil, err
		} else if linkPath != "" {
			return nil, requestError(-32600, fmt.Sprintf("fs/writeFile path %s traverses symlink %s", params.Path, linkPath))
		}
	}
	data, err := base64.StdEncoding.DecodeString(params.DataBase64)
	if err != nil {
		return nil, fmt.Errorf("fs/writeFile requires valid base64 dataBase64: %w", err)
	}
	if err := os.WriteFile(path, data, 0o666); err != nil {
		return nil, err
	}
	return &FSWriteFileResponse{}, nil
}

func createDirectory(params *FSCreateDirectoryParams) (*FSCreateDirectoryResponse, error) {
	var sandboxed FSCreateDirectoryResponse
	if ran, err := runSandboxedFSOperation(params.Sandbox, MethodFSCreateDirectory, params, &sandboxed); ran {
		if err != nil {
			return nil, err
		}
		return &sandboxed, nil
	}
	path, err := resolvePath(params.Path)
	if err != nil {
		return nil, err
	}
	recursive := true
	if params.Recursive != nil {
		recursive = *params.Recursive
	}
	if recursive {
		err = os.MkdirAll(path, 0o777)
	} else {
		err = os.Mkdir(path, 0o777)
	}
	if err != nil {
		return nil, err
	}
	return &FSCreateDirectoryResponse{}, nil
}

func getMetadata(params *FSGetMetadataParams) (*FSGetMetadataResponse, error) {
	var sandboxed FSGetMetadataResponse
	if ran, err := runSandboxedFSOperation(params.Sandbox, MethodFSGetMetadata, params, &sandboxed); ran {
		if err != nil {
			return nil, err
		}
		return &sandboxed, nil
	}
	path, err := resolvePath(params.Path)
	if err != nil {
		return nil, err
	}
	linkInfo, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	info, statErr := os.Stat(path)
	if statErr != nil {
		info = linkInfo
	}
	return &FSGetMetadataResponse{
		IsDirectory:  info.IsDir(),
		IsFile:       info.Mode().IsRegular(),
		IsSymlink:    linkInfo.Mode()&os.ModeSymlink != 0,
		Size:         info.Size(),
		CreatedAtMS:  createdAtMillisForPath(info, path),
		ModifiedAtMS: info.ModTime().UnixMilli(),
	}, nil
}

func canonicalize(params *FSCanonicalizeParams) (*FSCanonicalizeResponse, error) {
	var sandboxed FSCanonicalizeResponse
	if ran, err := runSandboxedFSOperation(params.Sandbox, MethodFSCanonicalize, params, &sandboxed); ran {
		if err != nil {
			return nil, err
		}
		return &sandboxed, nil
	}
	path, err := resolvePath(params.Path)
	if err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	uri, err := utils.FromHostNativePath(resolved)
	if err != nil {
		return nil, err
	}
	return &FSCanonicalizeResponse{Path: uri.String()}, nil
}

func readDirectory(params *FSReadDirectoryParams) (*FSReadDirectoryResponse, error) {
	var sandboxed FSReadDirectoryResponse
	if ran, err := runSandboxedFSOperation(params.Sandbox, MethodFSReadDirectory, params, &sandboxed); ran {
		if err != nil {
			return nil, err
		}
		return &sandboxed, nil
	}
	path, err := resolvePath(params.Path)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	out := make([]FSReadDirectoryEntry, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		out = append(out, FSReadDirectoryEntry{
			FileName:    entry.Name(),
			IsDirectory: info.IsDir(),
			IsFile:      info.Mode().IsRegular(),
		})
	}
	return &FSReadDirectoryResponse{Entries: out}, nil
}

func removePath(params *FSRemoveParams) (*FSRemoveResponse, error) {
	var sandboxed FSRemoveResponse
	if ran, err := runSandboxedFSOperation(params.Sandbox, MethodFSRemove, params, &sandboxed); ran {
		if err != nil {
			return nil, err
		}
		return &sandboxed, nil
	}
	path, err := resolvePath(params.Path)
	if err != nil {
		return nil, err
	}
	recursive := true
	if params.Recursive != nil {
		recursive = *params.Recursive
	}
	force := true
	if params.Force != nil {
		force = *params.Force
	}
	if recursive {
		err = os.RemoveAll(path)
	} else {
		err = os.Remove(path)
	}
	if err != nil && !(force && os.IsNotExist(err)) {
		return nil, err
	}
	return &FSRemoveResponse{}, nil
}

func copyPath(params *FSCopyParams) (*FSCopyResponse, error) {
	var sandboxed FSCopyResponse
	if ran, err := runSandboxedFSOperation(params.Sandbox, MethodFSCopy, params, &sandboxed); ran {
		if err != nil {
			return nil, err
		}
		return &sandboxed, nil
	}
	source, err := resolvePath(params.SourcePath)
	if err != nil {
		return nil, err
	}
	destination, err := resolvePath(params.DestinationPath)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(source)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		if !params.Recursive {
			return nil, errors.New("fs/copy requires recursive: true when sourcePath is a directory")
		}
		descendant, err := destinationIsSameOrDescendant(source, destination)
		if err != nil {
			return nil, err
		}
		if descendant {
			return nil, errors.New("fs/copy cannot copy a directory to itself or one of its descendants")
		}
		if err := copyDirectory(source, destination); err != nil {
			return nil, err
		}
		return &FSCopyResponse{}, nil
	}
	if info.Mode()&os.ModeSymlink != 0 {
		if err := copySymlink(source, destination); err != nil {
			return nil, err
		}
		return &FSCopyResponse{}, nil
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("fs/copy only supports regular files, directories, and symlinks")
	}
	if err := copyFile(source, destination, info.Mode()); err != nil {
		return nil, err
	}
	return &FSCopyResponse{}, nil
}

func walkPath(params *FSWalkParams) (*FSWalkResponse, error) {
	var sandboxed FSWalkResponse
	if ran, err := runSandboxedFSOperation(params.Sandbox, MethodFSWalk, params, &sandboxed); ran {
		if err != nil {
			return nil, err
		}
		return &sandboxed, nil
	}
	path, err := resolvePath(params.Path)
	if err != nil {
		return nil, err
	}
	options := params.Options
	if options.MaxDirectories <= 0 || options.MaxEntries <= 0 {
		return nil, errors.New("filesystem walk limits must be greater than zero")
	}
	if options.MaxDepth > maxWalkDepth || options.MaxDirectories > maxWalkDirectories || options.MaxEntries > maxWalkEntries {
		return nil, fmt.Errorf("filesystem walk limits exceed maximums: depth=%d, directories=%d, entries=%d", maxWalkDepth, maxWalkDirectories, maxWalkEntries)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	linkInfo, linkErr := os.Lstat(path)
	if linkErr != nil {
		return nil, linkErr
	}
	if !info.IsDir() || (linkInfo.Mode()&os.ModeSymlink != 0 && !options.FollowDirectorySymlinks) {
		return &FSWalkResponse{Entries: []FSWalkEntry{}, Errors: []FSWalkError{}}, nil
	}
	walker := &walkState{
		options: options,
		seen:    map[string]bool{},
	}
	identity := path
	if options.FollowDirectorySymlinks {
		identity, err = filepath.EvalSymlinks(path)
		if err != nil {
			return nil, err
		}
	}
	walker.seen[filepath.Clean(identity)] = true
	walker.directoryCount = 1
	walker.walk(path)
	entries := walker.entries
	if entries == nil {
		entries = []FSWalkEntry{}
	}
	errors := walker.errors
	if errors == nil {
		errors = []FSWalkError{}
	}
	return &FSWalkResponse{
		Entries:   entries,
		Errors:    errors,
		Truncated: walker.truncated,
	}, nil
}

func doHTTPRequest(ctx context.Context, params *HTTPRequestParams, configuredClient ...*http.Client) (*HTTPRequestResponse, error) {
	if params == nil {
		return nil, errors.New("http/request params are required")
	}
	method := params.Method
	if method == "" || !validHTTPHeaderName(method) {
		return nil, requestError(-32602, fmt.Sprintf("http/request method is invalid: %q is not a valid HTTP method", method))
	}
	if params.RedirectPolicy != "" && params.RedirectPolicy != "follow" && params.RedirectPolicy != "stop" {
		return nil, requestError(-32602, fmt.Sprintf("invalid http/request redirectPolicy %q", params.RedirectPolicy))
	}
	var releaseBodyStream func()
	if params.StreamResponse {
		var err error
		releaseBodyStream, err = reserveHTTPBodyStream(ctx, params.RequestID)
		if err != nil {
			return nil, err
		}
	}
	parsedURL, err := url.Parse(params.URL)
	if err != nil {
		return nil, requestError(-32602, "http/request url is invalid: "+err.Error())
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return nil, requestError(-32602, fmt.Sprintf("http/request only supports http and https URLs, got %s", parsedURL.Scheme))
	}
	if parsedURL.Host == "" {
		return nil, requestError(-32602, "http/request url is invalid: missing host")
	}
	releaseOnError := true
	defer func() {
		if releaseOnError && releaseBodyStream != nil {
			releaseBodyStream()
		}
	}()
	var body io.Reader
	if params.BodyBase64 != nil {
		data, err := base64.StdEncoding.DecodeString(*params.BodyBase64)
		if err != nil {
			return nil, requestError(-32602, "invalid params: invalid bodyBase64: "+err.Error())
		}
		body = bytes.NewReader(data)
	}
	requestCtx := ctx
	var cancel context.CancelFunc
	cancelOnReturn := false
	if params.TimeoutMS != nil {
		requestCtx, cancel = context.WithTimeout(ctx, time.Duration(*params.TimeoutMS)*time.Millisecond)
		cancelOnReturn = true
	}
	defer func() {
		if cancelOnReturn && cancel != nil {
			cancel()
		}
	}()
	req, err := http.NewRequestWithContext(requestCtx, method, params.URL, body)
	if err != nil {
		return nil, requestError(-32602, "http/request method or url is invalid: "+err.Error())
	}
	for _, header := range params.Headers {
		if !validHTTPHeaderName(header.Name) {
			return nil, requestError(-32602, "http/request header name is invalid")
		}
		if !validHTTPHeaderValue(header.Value) {
			return nil, requestError(-32602, fmt.Sprintf("http/request header value is invalid for %s", header.Name))
		}
		req.Header.Add(header.Name, header.Value)
	}
	var baseClient *http.Client
	if len(configuredClient) > 0 {
		baseClient = configuredClient[0]
	}
	client, err := newExecServerHTTPClient(baseClient)
	if err != nil {
		return nil, err
	}
	if params.RedirectPolicy == "stop" {
		client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, requestError(-32603, "http/request failed: "+err.Error())
	}
	headers := make([]HTTPHeader, 0, len(resp.Header))
	for name, values := range resp.Header {
		for _, value := range values {
			headers = append(headers, HTTPHeader{Name: name, Value: value})
		}
	}
	sort.Slice(headers, func(i, j int) bool {
		leftName := strings.ToLower(headers[i].Name)
		rightName := strings.ToLower(headers[j].Name)
		if leftName != rightName {
			return leftName < rightName
		}
		if headers[i].Name != headers[j].Name {
			return headers[i].Name < headers[j].Name
		}
		return headers[i].Value < headers[j].Value
	})
	response := &HTTPRequestResponse{
		Status:     resp.StatusCode,
		Headers:    headers,
		BodyBase64: "",
	}
	if params.StreamResponse {
		notify := processNotifierFromContext(ctx)
		if notify == nil || !registerAfterResponseAction(ctx, func() {
			go streamHTTPResponseBody(params.RequestID, resp.Body, notify, releaseBodyStream, cancel)
		}) {
			_ = resp.Body.Close()
			return nil, requestError(-32603, "http/request streaming requires a notification transport")
		}
		releaseOnError = false
		cancelOnReturn = false
		return response, nil
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, requestError(-32603, "failed to read http/request response body: "+err.Error())
	}
	response.BodyBase64 = base64.StdEncoding.EncodeToString(data)
	return response, nil
}

func streamHTTPResponseBody(requestID string, body io.ReadCloser, notify processNotifier, release func(), cancel context.CancelFunc) {
	if cancel != nil {
		defer cancel()
	}
	if release != nil {
		defer release()
	}
	defer body.Close()
	buffer := make([]byte, 32*1024)
	seq := uint64(1)
	for {
		n, err := body.Read(buffer)
		if n > 0 {
			notify(MethodHTTPRequestBodyDelta, HTTPRequestBodyDeltaNotification{
				RequestID:   requestID,
				Seq:         seq,
				DeltaBase64: base64.StdEncoding.EncodeToString(buffer[:n]),
			})
			seq++
		}
		if err != nil {
			var streamErr *string
			if !errors.Is(err, io.EOF) {
				message := err.Error()
				streamErr = &message
			}
			notify(MethodHTTPRequestBodyDelta, HTTPRequestBodyDeltaNotification{
				RequestID:   requestID,
				Seq:         seq,
				DeltaBase64: "",
				Done:        true,
				Error:       streamErr,
			})
			return
		}
	}
}

func validHTTPHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			continue
		}
		switch c {
		case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
			continue
		default:
			return false
		}
	}
	return true
}

func validHTTPHeaderValue(value string) bool {
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c == '\t' {
			continue
		}
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

func (p *processState) capture(stream string, reader io.Reader) {
	buffer := make([]byte, 32*1024)
	for {
		n, err := reader.Read(buffer)
		if n > 0 {
			data := append([]byte(nil), buffer[:n]...)
			p.mu.Lock()
			chunk := p.appendLocked(stream, data)
			notify := p.notify
			p.mu.Unlock()
			if notify != nil {
				notify(MethodProcessOutput, ProcessOutputNotification{ProcessID: p.id, Seq: chunk.Seq, Stream: chunk.Stream, Chunk: chunk.Chunk})
			}
		}
		if err != nil {
			p.finishStream()
			return
		}
	}
}

func (p *processState) finish(waitErr error) {
	p.finishWithCode(waitErr, nil)
}

func (p *processState) finishWithCode(waitErr error, explicitCode *int) {
	p.mu.Lock()
	var exitErr *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exitErr) {
		p.failure = waitErr.Error()
	}
	if explicitCode != nil {
		code := *explicitCode
		p.exitCode = &code
	} else if p.cmd != nil && p.cmd.ProcessState != nil {
		code := p.cmd.ProcessState.ExitCode()
		p.exitCode = &code
	}
	if p.exitCode != nil {
		p.sandboxDenied = sandbox.IsLikelySandboxDenied(p.sandboxType, p.sandboxExecOutputLocked(*p.exitCode))
	}
	p.exited = true
	var exited *ProcessExitedNotification
	if !p.exitSequenced {
		seq := p.nextSeq
		p.nextSeq++
		p.exitSequenced = true
		code := -1
		if p.exitCode != nil {
			code = *p.exitCode
		}
		denied := p.sandboxDenied
		exited = &ProcessExitedNotification{ProcessID: p.id, Seq: seq, ExitCode: code, SandboxDenied: &denied}
	}
	var closed *ProcessClosedNotification
	if p.openStreams == 0 {
		p.closed = true
		if !p.closedSequenced {
			seq := p.nextSeq
			p.nextSeq++
			p.closedSequenced = true
			closed = &ProcessClosedNotification{ProcessID: p.id, Seq: seq}
		}
	}
	p.cond.Broadcast()
	notify := p.notify
	p.mu.Unlock()
	if notify != nil {
		if exited != nil {
			notify(MethodProcessExited, *exited)
		}
		if closed != nil {
			notify(MethodProcessClosed, *closed)
		}
	}
	if closed != nil {
		p.closeNetworkResources()
		p.scheduleRetention()
	}
}

func (p *processState) finishStream() {
	p.mu.Lock()
	if p.openStreams > 0 {
		p.openStreams--
	}
	var closed *ProcessClosedNotification
	if p.exited && p.openStreams == 0 {
		p.closed = true
		if !p.closedSequenced {
			seq := p.nextSeq
			p.nextSeq++
			p.closedSequenced = true
			closed = &ProcessClosedNotification{ProcessID: p.id, Seq: seq}
		}
	}
	p.cond.Broadcast()
	notify := p.notify
	p.mu.Unlock()
	if notify != nil && closed != nil {
		notify(MethodProcessClosed, *closed)
	}
	if closed != nil {
		p.closeNetworkResources()
		p.scheduleRetention()
	}
}

func (p *processState) appendLocked(stream string, data []byte) outputChunk {
	seq := p.nextSeq
	p.nextSeq++
	chunk := outputChunk{
		Seq:    seq,
		Stream: stream,
		Chunk:  base64.StdEncoding.EncodeToString(data),
	}
	p.chunks = append(p.chunks, chunk)
	p.retainedBytes += len(data)
	for p.retainedBytes > retainedOutputBytesPerProcess && len(p.chunks) > 0 {
		decoded, _ := base64.StdEncoding.DecodeString(p.chunks[0].Chunk)
		p.retainedBytes -= len(decoded)
		p.chunks = p.chunks[1:]
	}
	p.cond.Broadcast()
	return chunk
}

func (p *processState) sandboxExecOutputLocked(exitCode int) sandbox.SandboxExecOutput {
	var stdout strings.Builder
	var stderr strings.Builder
	var aggregated strings.Builder
	for _, chunk := range p.chunks {
		decoded, err := base64.StdEncoding.DecodeString(chunk.Chunk)
		if err != nil {
			continue
		}
		aggregated.Write(decoded)
		switch chunk.Stream {
		case "stderr":
			stderr.Write(decoded)
		case "stdout":
			stdout.Write(decoded)
		}
	}
	return sandbox.SandboxExecOutput{
		ExitCode:         exitCode,
		Stdout:           stdout.String(),
		Stderr:           stderr.String(),
		AggregatedOutput: aggregated.String(),
	}
}

func (p *processState) read(after uint64, maxBytes *int, waitMS *uint64, detached <-chan struct{}) (*ReadResponse, bool) {
	deadline := time.Time{}
	if waitMS != nil && *waitMS > 0 {
		deadline = time.Now().Add(time.Duration(*waitMS) * time.Millisecond)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for {
		if channelClosed(detached) {
			return nil, false
		}
		response := p.readLocked(after, maxBytes)
		hasTerminalEvent := response.Exited && after < response.NextSeq-1
		if len(response.Chunks) > 0 || response.Closed || response.Failure != nil || hasTerminalEvent || waitMS == nil || *waitMS == 0 || !time.Now().Before(deadline) {
			return response, true
		}
		remaining := time.Until(deadline)
		timer := time.AfterFunc(remaining, func() {
			p.mu.Lock()
			p.cond.Broadcast()
			p.mu.Unlock()
		})
		waitDone := make(chan struct{})
		if detached != nil {
			go func() {
				select {
				case <-detached:
					p.mu.Lock()
					p.cond.Broadcast()
					p.mu.Unlock()
				case <-waitDone:
				}
			}()
		}
		p.cond.Wait()
		close(waitDone)
		timer.Stop()
	}
}

func channelClosed(ch <-chan struct{}) bool {
	if ch == nil {
		return false
	}
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func (p *processState) readLocked(after uint64, maxBytes *int) *ReadResponse {
	chunks := []outputChunk{}
	totalBytes := 0
	for _, chunk := range p.chunks {
		if chunk.Seq <= after {
			continue
		}
		decoded, _ := base64.StdEncoding.DecodeString(chunk.Chunk)
		if maxBytes != nil {
			if len(chunks) > 0 && totalBytes+len(decoded) > *maxBytes {
				break
			}
		}
		chunks = append(chunks, chunk)
		totalBytes += len(decoded)
		if maxBytes != nil && totalBytes >= *maxBytes {
			break
		}
	}
	nextSeq := p.nextSeq
	if maxBytes != nil && len(chunks) > 0 {
		nextSeq = chunks[len(chunks)-1].Seq + 1
	}
	var failure *string
	if p.failure != "" {
		value := p.failure
		failure = &value
	}
	return &ReadResponse{
		Chunks:        chunks,
		NextSeq:       nextSeq,
		Exited:        p.exited,
		ExitCode:      p.exitCode,
		Closed:        p.closed,
		Failure:       failure,
		SandboxDenied: p.sandboxDenied,
	}
}

func (p *processState) write(params *WriteParams) (*WriteResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if params.WriteID == "" {
		return nil, errors.New("writeId must not be empty")
	}
	if p.starting {
		return &WriteResponse{Status: "starting"}, nil
	}
	if p.exited {
		return &WriteResponse{Status: "stdinClosed"}, nil
	}
	if !p.pipeStdin || p.stdin == nil {
		return &WriteResponse{Status: "stdinClosed"}, nil
	}
	if p.seenWriteIDs[params.WriteID] {
		return &WriteResponse{Status: "accepted"}, nil
	}
	data, err := base64.StdEncoding.DecodeString(params.Chunk)
	if err != nil {
		return nil, err
	}
	if _, err := p.stdin.Write(data); err != nil {
		return &WriteResponse{Status: "stdinClosed"}, nil
	}
	p.seenWriteIDs[params.WriteID] = true
	p.seenWriteOrder = append(p.seenWriteOrder, params.WriteID)
	for len(p.seenWriteOrder) > retainedWriteIDsPerProcess {
		evicted := p.seenWriteOrder[0]
		p.seenWriteOrder = p.seenWriteOrder[1:]
		delete(p.seenWriteIDs, evicted)
	}
	return &WriteResponse{Status: "accepted"}, nil
}

func (p *processState) scheduleRetention() {
	if p == nil || p.onClosed == nil {
		return
	}
	p.retentionOnce.Do(func() { go p.onClosed() })
}

func (p *processState) terminate() *TerminateResponse {
	if p != nil && p.policyCancel != nil {
		p.policyCancel()
	}
	p.mu.Lock()
	cmd := p.cmd
	terminateFn := p.terminateFn
	running := !p.exited && (terminateFn != nil || (cmd != nil && cmd.Process != nil))
	p.mu.Unlock()
	if running {
		if terminateFn != nil {
			_ = terminateFn()
		} else {
			_ = cmd.Process.Kill()
		}
	}
	return &TerminateResponse{Running: running}
}

func (p *processState) closeNetworkResources() {
	if p == nil {
		return
	}
	p.networkCloseOnce.Do(func() {
		if p.policyCancel != nil {
			p.policyCancel()
		}
		if p.networkProxy != nil {
			_ = p.networkProxy.Close()
		}
	})
}

func (p *processState) signal(params *SignalParams) (*SignalResponse, error) {
	p.mu.Lock()
	cmd := p.cmd
	signalFn := p.signalFn
	terminateFn := p.terminateFn
	tty := p.tty
	running := !p.exited && (signalFn != nil || terminateFn != nil || (cmd != nil && cmd.Process != nil))
	p.mu.Unlock()
	if !running {
		return &SignalResponse{}, nil
	}
	if params != nil && params.Signal != "" && !strings.EqualFold(params.Signal, "interrupt") {
		return nil, fmt.Errorf("unsupported process signal %s", params.Signal)
	}
	if runtime.GOOS == "windows" && !tty {
		if terminateFn != nil {
			if err := terminateFn(); err != nil {
				return nil, fmt.Errorf("failed to terminate process on interrupt: %w", err)
			}
			return &SignalResponse{}, nil
		}
		if cmd != nil && cmd.Process != nil {
			if err := cmd.Process.Kill(); err != nil {
				return nil, fmt.Errorf("failed to terminate process on interrupt: %w", err)
			}
		}
		return &SignalResponse{}, nil
	}
	if signalFn != nil {
		if err := signalFn(); err != nil {
			return nil, fmt.Errorf("failed to signal process: %w", err)
		}
		return &SignalResponse{}, nil
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		return nil, fmt.Errorf("failed to signal process: %w", err)
	}
	return &SignalResponse{}, nil
}

func decodeParams(data json.RawMessage, target any) error {
	if len(bytes.TrimSpace(data)) == 0 {
		data = []byte("{}")
	}
	if err := json.Unmarshal(data, target); err != nil {
		return requestError(-32602, "invalid params: "+err.Error())
	}
	return nil
}

func errorResponse(id RequestID, code int, message string) response {
	return response{ID: id, Error: &rpcError{Code: code, Message: message}}
}

func errorResponseForRequest(id RequestID, err error) response {
	var failure *requestFailure
	if errors.As(err, &failure) {
		return errorResponse(id, failure.code, failure.message)
	}
	return errorResponse(id, -32603, err.Error())
}

func requestError(code int, message string) error {
	return &requestFailure{code: code, message: message}
}

func hasJSONValue(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

func localEnvironmentInfo() *EnvironmentInfo {
	detected := shell.UltimateFallbackShell()
	cwd := ""
	var cwdPath string
	if current, err := os.Getwd(); err == nil {
		cwdPath = current
		if uri, err := utils.FromHostNativePath(current); err == nil {
			cwd = uri.String()
		} else {
			cwd = current
		}
	}
	return &EnvironmentInfo{
		Shell:                ShellInfo{Name: detected.Name(), Path: detected.ShellPath},
		CWD:                  stringPtr(cwd),
		TemporaryDirectories: localTemporaryDirectories(cwdPath),
		Capabilities: EnvironmentCapabilities{
			NetworkProxyLaunch:         true,
			CapabilityDiscoverySandbox: true,
			// Rust 646f7c0a91: local executors advertise environmentConfig/read.
			EnvironmentConfigRead:  true,
			SandboxedFileStreaming: true,
		},
	}
}

// localTemporaryDirectories mirrors Rust 92fb33b758: executor-local default
// directories for resolving `:tmpdir`, populated from TMPDIR on Unix and
// TEMP/TMP on Windows, deduplicated as file URIs.
func localTemporaryDirectories(cwd string) []string {
	names := []string{"TMPDIR"}
	if runtime.GOOS == "windows" {
		names = []string{"TEMP", "TMP"}
	}
	seen := map[string]bool{}
	var out []string
	for _, name := range names {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			continue
		}
		path := value
		if runtime.GOOS != "windows" && !filepath.IsAbs(path) {
			if cwd == "" {
				continue
			}
			path = filepath.Join(cwd, path)
		} else if runtime.GOOS == "windows" && !filepath.IsAbs(path) {
			continue
		}
		uri, err := utils.FromHostNativePath(path)
		if err != nil {
			continue
		}
		encoded := uri.String()
		if seen[encoded] {
			continue
		}
		seen[encoded] = true
		out = append(out, encoded)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func envPairs(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for key, value := range env {
		if strings.TrimSpace(key) == "" {
			continue
		}
		out = append(out, key+"="+value)
	}
	return out
}

func childEnv(params *ExecParams) map[string]string {
	if params == nil {
		return map[string]string{}
	}
	var env map[string]string
	if params.EnvPolicy == nil {
		env = copyEnv(params.Env)
	} else {
		env = populateEnv(params.EnvPolicy, os.Environ())
		for key, value := range params.Env {
			env[key] = value
		}
	}
	deleteEnvKey(env, CodexExecServerExitOnStdinCloseEnvVar)
	if runtime.GOOS == "windows" {
		if !hasEnvKey(env, "PATHEXT") {
			env["PATHEXT"] = ".COM;.EXE;.BAT;.CMD"
		}
	}
	// Rust c4513cb982: model-reachable child processes must not inherit Codex
	// launch context (OPENAI_FEDERATION_RULE_ID / OPENAI_IDENTITY_TOKEN_FILE).
	envutil.ScrubMap(env)
	return env
}

func deleteEnvKey(env map[string]string, key string) {
	for existing := range env {
		if strings.EqualFold(existing, key) {
			delete(env, existing)
		}
	}
}

func populateEnv(policy *ExecEnvPolicy, environ []string) map[string]string {
	env := map[string]string{}
	inherit := "all"
	if policy != nil {
		inherit = policy.Inherit
	}
	switch inherit {
	case "none":
	case "core":
		for _, pair := range environ {
			key, value, ok := splitEnv(pair)
			if ok && isCoreEnv(key) {
				env[key] = value
			}
		}
	default:
		for _, pair := range environ {
			key, value, ok := splitEnv(pair)
			if ok {
				env[key] = value
			}
		}
	}
	if policy == nil {
		return env
	}
	if !policy.IgnoreDefaultExcludes {
		for key := range env {
			if wildcardMatchAny([]string{"*KEY*", "*SECRET*", "*TOKEN*"}, key) {
				delete(env, key)
			}
		}
	}
	if len(policy.Exclude) > 0 {
		for key := range env {
			if wildcardMatchAny(policy.Exclude, key) {
				delete(env, key)
			}
		}
	}
	for key, value := range policy.Set {
		env[key] = value
	}
	if len(policy.IncludeOnly) > 0 {
		for key := range env {
			if !wildcardMatchAny(policy.IncludeOnly, key) {
				delete(env, key)
			}
		}
	}
	return env
}

func copyEnv(env map[string]string) map[string]string {
	out := make(map[string]string, len(env))
	for key, value := range env {
		out[key] = value
	}
	return out
}

func splitEnv(pair string) (string, string, bool) {
	index := strings.Index(pair, "=")
	if index <= 0 {
		return "", "", false
	}
	return pair[:index], pair[index+1:], true
}

func hasEnvKey(env map[string]string, key string) bool {
	for existing := range env {
		if strings.EqualFold(existing, key) {
			return true
		}
	}
	return false
}

func isCoreEnv(key string) bool {
	var core []string
	if runtime.GOOS == "windows" {
		core = []string{
			"PATH", "PATHEXT", "SHELL", "COMSPEC", "SYSTEMROOT", "WINDIR", "SYSTEMDRIVE",
			"USERNAME", "USERDOMAIN", "USERPROFILE", "HOMEDRIVE", "HOMEPATH",
			"PROGRAMFILES", "PROGRAMFILES(X86)", "PROGRAMW6432", "PROGRAMDATA",
			"LOCALAPPDATA", "APPDATA", "TEMP", "TMP", "TMPDIR", "POWERSHELL", "PWSH",
		}
	} else {
		core = []string{"PATH", "SHELL", "TMPDIR", "TEMP", "TMP", "HOME", "LANG", "LC_ALL", "LC_CTYPE", "LOGNAME", "USER"}
	}
	for _, allowed := range core {
		if strings.EqualFold(allowed, key) {
			return true
		}
	}
	return false
}

func wildcardMatchAny(patterns []string, key string) bool {
	for _, pattern := range patterns {
		if wildcardMatch(strings.ToLower(pattern), strings.ToLower(key)) {
			return true
		}
	}
	return false
}

func wildcardMatch(pattern string, value string) bool {
	if pattern == "" {
		return value == ""
	}
	ok, err := filepath.Match(pattern, value)
	if err == nil {
		return ok
	}
	return pattern == value
}

func resolvePath(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("path is required")
	}
	if len(raw) >= len("file:") && strings.EqualFold(raw[:len("file:")], "file:") {
		uri, err := utils.Parse(raw)
		if err != nil {
			return "", err
		}
		raw, err = uri.HostNativePath()
		if err != nil {
			return "", err
		}
	}
	if filepath.IsAbs(raw) {
		return raw, nil
	}
	return filepath.Abs(raw)
}

func validateWirePathURI(raw string) error {
	if raw == "" {
		return errors.New("path is required")
	}
	_, err := utils.Parse(raw)
	return err
}

func validateFSWirePath(field string, raw string, sandboxContext *FileSystemSandboxContext) error {
	if err := validateWirePathURI(raw); err != nil {
		return requestError(-32602, "invalid params: "+field+" must be an absolute file URI: "+err.Error())
	}
	if sandboxContext == nil {
		return nil
	}
	if sandboxContext.CWD != "" {
		if err := validateWirePathURI(sandboxContext.CWD); err != nil {
			return requestError(-32602, "invalid params: sandbox.cwd must be an absolute file URI: "+err.Error())
		}
	}
	for index, root := range sandboxContext.WorkspaceRoots {
		if err := validateWirePathURI(root); err != nil {
			return requestError(-32602, fmt.Sprintf("invalid params: sandbox.workspaceRoots[%d] must be an absolute file URI: %v", index, err))
		}
	}
	return nil
}

func normalizeExecCWDForWire(raw string) (string, error) {
	if raw == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		raw = cwd
	}
	if len(raw) >= len("file:") && strings.EqualFold(raw[:len("file:")], "file:") {
		uri, err := utils.Parse(raw)
		if err != nil {
			return "", err
		}
		return uri.String(), nil
	}
	if !filepath.IsAbs(raw) {
		return "", fmt.Errorf("cwd must be absolute: %s", raw)
	}
	uri, err := utils.FromHostNativePath(raw)
	if err != nil {
		return "", err
	}
	return uri.String(), nil
}

func normalizeFSPathForWire(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("path is required")
	}
	if len(raw) >= len("file:") && strings.EqualFold(raw[:len("file:")], "file:") {
		uri, err := utils.Parse(raw)
		if err != nil {
			return "", err
		}
		return uri.String(), nil
	}
	if !filepath.IsAbs(raw) {
		return "", fmt.Errorf("path must be absolute: %s", raw)
	}
	uri, err := utils.FromHostNativePath(raw)
	if err != nil {
		return "", err
	}
	return uri.String(), nil
}

func normalizeFSSandboxForWire(sandboxContext *FileSystemSandboxContext) (*FileSystemSandboxContext, error) {
	if sandboxContext == nil {
		return nil, nil
	}
	normalized := *sandboxContext
	normalized.Permissions = append(json.RawMessage(nil), sandboxContext.Permissions...)
	if normalized.CWD != "" {
		cwd, err := normalizeFSPathForWire(normalized.CWD)
		if err != nil {
			return nil, fmt.Errorf("sandbox cwd: %w", err)
		}
		normalized.CWD = cwd
	}
	normalized.WorkspaceRoots = make([]string, len(sandboxContext.WorkspaceRoots))
	for index, root := range sandboxContext.WorkspaceRoots {
		normalizedRoot, err := normalizeFSPathForWire(root)
		if err != nil {
			return nil, fmt.Errorf("sandbox workspace root %d: %w", index, err)
		}
		normalized.WorkspaceRoots[index] = normalizedRoot
	}
	return &normalized, nil
}

func copyFile(source string, destination string, mode os.FileMode) error {
	sourceFile, err := os.Open(source)
	if err != nil {
		return err
	}
	defer sourceFile.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0o777); err != nil {
		return err
	}
	destinationFile, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(destinationFile, sourceFile)
	chmodErr := destinationFile.Chmod(mode.Perm())
	closeErr := destinationFile.Close()
	if copyErr != nil {
		return copyErr
	}
	if chmodErr != nil {
		return chmodErr
	}
	return closeErr
}

func copyDirectory(source string, destination string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o777)
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return copySymlink(path, target)
		}
		if info.Mode().IsRegular() {
			return copyFile(path, target, info.Mode())
		}
		return nil
	})
}

func copySymlink(source string, destination string) error {
	target, err := os.Readlink(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o777); err != nil {
		return err
	}
	return os.Symlink(target, destination)
}

func destinationIsSameOrDescendant(source string, destination string) (bool, error) {
	resolvedSource, err := filepath.Abs(source)
	if err != nil {
		return false, err
	}
	if canonical, err := filepath.EvalSymlinks(resolvedSource); err == nil {
		resolvedSource = canonical
	}
	resolvedDestination, err := resolveExistingPath(destination)
	if err != nil {
		return false, err
	}
	rel, err := filepath.Rel(resolvedSource, resolvedDestination)
	if err != nil {
		return false, err
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))), nil
}

func resolveExistingPath(path string) (string, error) {
	clean := filepath.Clean(path)
	suffix := []string{}
	for {
		if _, err := os.Lstat(clean); err == nil {
			resolved, err := filepath.EvalSymlinks(clean)
			if err != nil {
				resolved, err = filepath.Abs(clean)
			}
			if err != nil {
				return "", err
			}
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return resolved, nil
		}
		parent := filepath.Dir(clean)
		if parent == clean {
			return filepath.Abs(path)
		}
		suffix = append(suffix, filepath.Base(clean))
		clean = parent
	}
}

type walkState struct {
	options        FSWalkOptions
	entries        []FSWalkEntry
	errors         []FSWalkError
	truncated      bool
	seen           map[string]bool
	directoryCount int
	entryCount     int
	responseBytes  int
}

func (w *walkState) walk(root string) {
	type queuedDirectory struct {
		path  string
		depth int
	}
	queue := []queuedDirectory{{path: root}}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		entries, err := os.ReadDir(current.path)
		if err != nil {
			if !w.pushError(current.path, err) {
				return
			}
			continue
		}
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Name() < entries[j].Name()
		})
		for _, entry := range entries {
			if w.entryCount == w.options.MaxEntries {
				w.truncated = true
				return
			}
			w.entryCount++
			path := filepath.Join(current.path, entry.Name())
			info, statErr := os.Stat(path)
			linkInfo, linkErr := os.Lstat(path)
			if statErr != nil {
				if !w.pushError(path, statErr) {
					return
				}
				continue
			}
			if linkErr != nil {
				if !w.pushError(path, linkErr) {
					return
				}
				continue
			}
			if linkInfo.Mode()&os.ModeSymlink != 0 && (!w.options.FollowDirectorySymlinks || !info.IsDir()) {
				continue
			}
			kind := ""
			if info.IsDir() {
				kind = "directory"
			} else if info.Mode().IsRegular() {
				kind = "file"
			} else {
				continue
			}
			uri := pathToURI(path)
			if !w.reserve(len(uri)) {
				return
			}
			w.entries = append(w.entries, FSWalkEntry{Path: uri, Kind: kind})
			if kind != "directory" || current.depth >= w.options.MaxDepth {
				continue
			}
			identity := path
			if w.options.FollowDirectorySymlinks {
				identity, err = filepath.EvalSymlinks(path)
				if err != nil {
					if !w.pushError(path, err) {
						return
					}
					continue
				}
			}
			identity = filepath.Clean(identity)
			if w.seen[identity] {
				continue
			}
			w.seen[identity] = true
			if w.directoryCount == w.options.MaxDirectories {
				w.truncated = true
				continue
			}
			w.directoryCount++
			queue = append(queue, queuedDirectory{path: path, depth: current.depth + 1})
		}
	}
}

func (w *walkState) pushError(path string, err error) bool {
	message := err.Error()
	uri := pathToURI(path)
	if !w.reserve(len(uri) + len(message)) {
		return false
	}
	w.errors = append(w.errors, FSWalkError{Path: uri, Message: message})
	return true
}

func (w *walkState) reserve(contentBytes int) bool {
	total := w.responseBytes + contentBytes + walkResponseItemOverhead
	if total > maxWalkResponseBytes {
		w.truncated = true
		return false
	}
	w.responseBytes = total
	return true
}

func pathToURI(path string) string {
	uri, err := utils.FromHostNativePath(path)
	if err != nil {
		return path
	}
	return uri.String()
}

func stringPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func ListenURL(listen string) string {
	if strings.TrimSpace(listen) == "" || listen == DefaultListenURL {
		return DefaultListenURL
	}
	if listen == "stdio" || listen == "stdio://" {
		return "stdio"
	}
	if runtime.GOOS == "windows" && strings.EqualFold(listen, "unix://") {
		return "stdio"
	}
	return listen
}

func ResolveCWD(cwd string) string {
	if cwd == "" {
		current, _ := os.Getwd()
		return current
	}
	if strings.HasPrefix(cwd, "file://") {
		uri, err := utils.Parse(cwd)
		if err == nil {
			return uri.NativePathString()
		}
	}
	if filepath.IsAbs(cwd) {
		return cwd
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return cwd
	}
	return abs
}
