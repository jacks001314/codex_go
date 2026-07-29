package tool

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"codex_go/execserver"
	"codex_go/network"
	"codex_go/sandbox"
	"codex_go/utils"
)

const (
	unifiedExecMinYieldMS            uint64 = 250
	unifiedExecWindowsInitialYieldMS uint64 = 10_000
	unifiedExecMinEmptyPollYieldMS   uint64 = 5_000
	unifiedExecMaxYieldMS            uint64 = 30_000
	unifiedExecDefaultMaxEmptyPollMS uint64 = 300_000
	unifiedExecDefaultMaxProcesses          = 64
	unifiedExecInterrupt                    = "\x03"
	unifiedExecTrailingOutputGrace          = 100 * time.Millisecond
	unifiedExecEarlyExitGrace               = 150 * time.Millisecond
	unifiedExecOutputDeltaMaxBytes          = 8192
	unifiedExecMaxOutputDeltas              = 10_000
)

var (
	unifiedExecRemoteRecoveryTimeout = 25 * time.Second
	unifiedExecRemoteRecoveryRetry   = 100 * time.Millisecond
)

var (
	ErrUnifiedExecUnknownProcess   = errors.New("unknown process id")
	ErrUnifiedExecProcessLimit     = errors.New("unified exec process limit reached")
	ErrUnifiedExecStdinClosed      = errors.New("stdin is closed for this session; rerun exec_command with tty=true to keep stdin open")
	startUnifiedExecWindowsSandbox = startUnifiedExecWindowsSandboxCommand
)

type UnifiedExecManager struct {
	mu                      sync.Mutex
	nextID                  int
	maxProcesses            int
	maxEmptyPollYieldTimeMS uint64
	processes               map[int]*unifiedExecProcess
	pausedThreads           map[string]chan struct{}
}

func (m *UnifiedExecManager) SetThreadElicitationPaused(threadID string, paused bool) {
	if m == nil || strings.TrimSpace(threadID) == "" {
		return
	}
	threadID = strings.TrimSpace(threadID)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pausedThreads == nil {
		m.pausedThreads = map[string]chan struct{}{}
	}
	gate := m.pausedThreads[threadID]
	if paused {
		if gate == nil {
			m.pausedThreads[threadID] = make(chan struct{})
		}
		return
	}
	if gate != nil {
		close(gate)
		delete(m.pausedThreads, threadID)
	}
}

func (m *UnifiedExecManager) ThreadElicitationPaused(threadID string) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pausedThreads[strings.TrimSpace(threadID)] != nil
}

func (m *UnifiedExecManager) waitForThreadElicitation(ctx context.Context, threadID string) error {
	for {
		m.mu.Lock()
		gate := m.pausedThreads[strings.TrimSpace(threadID)]
		m.mu.Unlock()
		if gate == nil {
			return nil
		}
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

type UnifiedExecEventKind string

const (
	UnifiedExecEventBegin               UnifiedExecEventKind = "begin"
	UnifiedExecEventOutputDelta         UnifiedExecEventKind = "output_delta"
	UnifiedExecEventTerminalInteraction UnifiedExecEventKind = "terminal_interaction"
	UnifiedExecEventEnd                 UnifiedExecEventKind = "end"
)

type UnifiedExecEvent struct {
	Kind        UnifiedExecEventKind
	CallID      string
	Command     []string
	HookCommand string
	CWD         string
	ProcessID   int
	Output      string
	Input       string
	ExitCode    int
	Duration    time.Duration
	StartedAt   time.Time
	TimedOut    bool
}

type UnifiedExecEventSink func(UnifiedExecEvent)

type UnifiedExecProcessInfo struct {
	ItemID    string
	ProcessID int
	Command   string
	CWD       string
	ThreadID  string
	TurnID    string
}

func unifiedExecOutputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"chunk_id":             map[string]any{"type": "string", "description": "Chunk identifier included when the response reports one."},
			"wall_time_seconds":    map[string]any{"type": "number", "description": "Elapsed wall time spent waiting for output in seconds."},
			"exit_code":            map[string]any{"type": "number", "description": "Process exit code when the command finished during this call."},
			"session_id":           map[string]any{"type": "number", "description": "Session identifier to pass to write_stdin when the process is still running."},
			"original_token_count": map[string]any{"type": "number", "description": "Approximate token count before output truncation."},
			"output":               map[string]any{"type": "string", "description": "Command output text, possibly truncated."},
		},
		"required":             []string{"wall_time_seconds", "output"},
		"additionalProperties": false,
	}
}

type unifiedExecProcess struct {
	id                   int
	callID               string
	hookCommand          string
	tty                  bool
	cmd                  *osexec.Cmd
	stdin                io.WriteCloser
	remote               *execserver.Client
	remoteURL            string
	remoteProvider       execserver.NoiseRendezvousConnectProvider
	remoteSessionID      string
	remoteID             string
	remoteWrite          uint64
	remoteEvents         *execserver.ProcessEventSubscription
	networkPolicyDecider network.ProxyPolicyDecider
	networkPolicyTimeout time.Duration
	sandbox              unifiedExecSandboxProcess
	done                 chan struct{}
	eventDone            chan struct{}
	eventSink            UnifiedExecEventSink
	command              []string
	cwd                  string
	startedAt            time.Time
	lastUsed             time.Time
	threadID             string
	turnID               string

	mu            sync.Mutex
	interactionMu sync.Mutex
	interactions  atomic.Int32
	eventMu       sync.Mutex
	output        *unifiedExecHeadTailBuffer
	transcript    *unifiedExecHeadTailBuffer
	eventPending  []byte
	emittedDeltas int
	exited        bool
	exitCode      int
	waitErr       error
	timedOut      bool
	sandboxDenied bool
	eventsStarted bool
	pendingEvents []UnifiedExecEvent
	endOnce       sync.Once
}

type unifiedExecSandboxProcess interface {
	Wait() (int, error)
	Terminate() error
	Close() error
}

type startedUnifiedExecSandboxCommand struct {
	process unifiedExecSandboxProcess
	stdin   io.WriteCloser
	readers []io.ReadCloser
}

type WriteStdinArgs struct {
	SessionID       int    `json:"session_id"`
	Chars           string `json:"chars,omitempty"`
	YieldTimeMS     uint64 `json:"yield_time_ms,omitempty"`
	MaxOutputTokens *int   `json:"max_output_tokens,omitempty"`
}

func NewUnifiedExecManager() *UnifiedExecManager {
	return NewUnifiedExecManagerWithOptions(unifiedExecDefaultMaxProcesses, unifiedExecDefaultMaxEmptyPollMS)
}

func NewUnifiedExecManagerWithOptions(maxProcesses int, maxEmptyPollYieldTimeMS uint64) *UnifiedExecManager {
	if maxProcesses <= 0 {
		maxProcesses = unifiedExecDefaultMaxProcesses
	}
	if maxEmptyPollYieldTimeMS < unifiedExecMinEmptyPollYieldMS {
		maxEmptyPollYieldTimeMS = unifiedExecMinEmptyPollYieldMS
	}
	return &UnifiedExecManager{
		nextID:                  1,
		maxProcesses:            maxProcesses,
		maxEmptyPollYieldTimeMS: maxEmptyPollYieldTimeMS,
		processes:               map[int]*unifiedExecProcess{},
	}
}

func (m *UnifiedExecManager) Exec(ctx context.Context, req *ShellRequest, callID string) (*ShellResult, error) {
	if m == nil {
		return nil, errors.New("unified exec manager is nil")
	}
	if req == nil || len(req.Command) == 0 {
		return nil, errors.New("command is required")
	}
	if ctx != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
	}
	processID, err := m.allocateProcessID()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.UnifiedExecRemoteURL) != "" || req.UnifiedExecNoiseProvider != nil {
		return m.execRemote(ctx, req, callID, processID)
	}
	if runtime.GOOS == "windows" && req.PermissionProfile != nil && !req.PermissionProfile.Disabled {
		return m.execWindowsSandbox(ctx, req, callID, processID)
	}
	cmd := osexec.Command(req.Command[0], req.Command[1:]...)
	cmd.Dir = req.CWD
	if len(req.Env) > 0 {
		cmd.Env = envSlice(shellRunnerEnvMap(os.Environ(), req.Env))
	}
	started, err := startUnifiedExecCommand(cmd, req.TTY)
	if err != nil {
		m.releaseProcessID(processID)
		return nil, err
	}
	process := &unifiedExecProcess{
		id:            processID,
		callID:        callID,
		hookCommand:   req.HookCommand,
		tty:           req.TTY,
		cmd:           cmd,
		stdin:         started.stdin,
		done:          make(chan struct{}),
		eventDone:     make(chan struct{}),
		output:        newUnifiedExecHeadTailBuffer(unifiedExecOutputMaxBytes),
		transcript:    newUnifiedExecHeadTailBuffer(unifiedExecOutputMaxBytes),
		eventSink:     req.UnifiedExecEventSink,
		command:       append([]string(nil), req.Command...),
		cwd:           req.CWD,
		startedAt:     time.Now(),
		threadID:      req.UnifiedExecThreadID,
		turnID:        req.UnifiedExecTurnID,
		eventsStarted: true,
	}
	process.lastUsed = process.startedAt
	m.mu.Lock()
	m.processes[processID] = process
	m.mu.Unlock()
	process.emitEvent(UnifiedExecEvent{
		Kind:        UnifiedExecEventBegin,
		CallID:      process.callID,
		Command:     append([]string(nil), process.command...),
		HookCommand: process.hookCommand,
		CWD:         process.cwd,
		ProcessID:   process.id,
		StartedAt:   process.startedAt,
	})

	var readers sync.WaitGroup
	for _, reader := range started.readers {
		if reader == nil {
			continue
		}
		readers.Add(1)
		go func(reader io.ReadCloser) {
			defer readers.Done()
			defer reader.Close()
			_, _ = io.Copy(unifiedExecOutputWriter{process: process}, reader)
		}(reader)
	}
	go func() {
		err := cmd.Wait()
		readers.Wait()
		process.finish(err)
	}()
	if req.TimeoutMS > 0 {
		go process.enforceTimeout(time.Duration(req.TimeoutMS) * time.Millisecond)
	}

	yield := clampUnifiedExecInitialYield(req.YieldTimeMS)
	return m.collect(ctx, process, yield, req.MaxOutputTokens)
}

func (m *UnifiedExecManager) execWindowsSandbox(ctx context.Context, req *ShellRequest, callID string, processID int) (*ShellResult, error) {
	started, err := startUnifiedExecWindowsSandbox(req)
	if err != nil {
		m.releaseProcessID(processID)
		return nil, err
	}
	process := &unifiedExecProcess{
		id:            processID,
		callID:        callID,
		hookCommand:   req.HookCommand,
		tty:           req.TTY,
		stdin:         started.stdin,
		sandbox:       started.process,
		done:          make(chan struct{}),
		eventDone:     make(chan struct{}),
		output:        newUnifiedExecHeadTailBuffer(unifiedExecOutputMaxBytes),
		transcript:    newUnifiedExecHeadTailBuffer(unifiedExecOutputMaxBytes),
		eventSink:     req.UnifiedExecEventSink,
		command:       append([]string(nil), req.Command...),
		cwd:           req.CWD,
		startedAt:     time.Now(),
		threadID:      req.UnifiedExecThreadID,
		turnID:        req.UnifiedExecTurnID,
		eventsStarted: true,
	}
	process.lastUsed = process.startedAt
	m.mu.Lock()
	m.processes[processID] = process
	m.mu.Unlock()
	process.emitEvent(UnifiedExecEvent{
		Kind:        UnifiedExecEventBegin,
		CallID:      process.callID,
		Command:     append([]string(nil), process.command...),
		HookCommand: process.hookCommand,
		CWD:         process.cwd,
		ProcessID:   process.id,
		StartedAt:   process.startedAt,
	})
	var readers sync.WaitGroup
	for _, reader := range started.readers {
		if reader == nil {
			continue
		}
		readers.Add(1)
		go func(reader io.ReadCloser) {
			defer readers.Done()
			defer reader.Close()
			_, _ = io.Copy(unifiedExecOutputWriter{process: process}, reader)
		}(reader)
	}
	go func() {
		exitCode, waitErr := started.process.Wait()
		readers.Wait()
		if closeErr := started.process.Close(); waitErr == nil && closeErr != nil {
			waitErr = closeErr
		}
		process.finishWithExit(waitErr, &exitCode)
	}()
	if req.TimeoutMS > 0 {
		go process.enforceTimeout(time.Duration(req.TimeoutMS) * time.Millisecond)
	}
	yield := clampUnifiedExecInitialYield(req.YieldTimeMS)
	return m.collect(ctx, process, yield, req.MaxOutputTokens)
}

func (m *UnifiedExecManager) execRemote(ctx context.Context, req *ShellRequest, callID string, processID int) (*ShellResult, error) {
	connectCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var client *execserver.Client
	var err error
	if req.UnifiedExecNoiseProvider != nil {
		client, err = execserver.DialNoiseRendezvousClient(connectCtx, req.UnifiedExecNoiseProvider, execserver.DialClientOptions{ClientName: "codex-go-unified-exec"})
	} else {
		client, err = execserver.DialClient(connectCtx, req.UnifiedExecRemoteURL, "codex-go-unified-exec")
	}
	if err != nil {
		m.releaseProcessID(processID)
		return nil, err
	}
	if req.RemoteNetworkProxy != nil && req.RemoteNetworkProxy.Proxy.Enabled {
		capabilityCtx, capabilityCancel := context.WithTimeout(context.Background(), 10*time.Second)
		environmentInfo, capabilityErr := client.EnvironmentInfo(capabilityCtx)
		capabilityCancel()
		if capabilityErr != nil {
			_ = client.Close()
			m.releaseProcessID(processID)
			return nil, fmt.Errorf("failed to query exec-server capabilities: %w", capabilityErr)
		}
		if !environmentInfo.Capabilities.NetworkProxyLaunch {
			_ = client.Close()
			m.releaseProcessID(processID)
			return nil, errors.New("selected exec-server does not support executor-local network proxy launches")
		}
	}
	remoteID := strconv.Itoa(processID)
	events, err := client.SubscribeProcessEvents(remoteID)
	if err != nil {
		_ = client.Close()
		m.releaseProcessID(processID)
		return nil, err
	}
	if req.NetworkPolicyDecider != nil {
		if err := client.RegisterNetworkPolicyController(remoteID, req.NetworkPolicyDecisionTimeout, req.NetworkPolicyDecider); err != nil {
			events.Close()
			_ = client.Close()
			m.releaseProcessID(processID)
			return nil, err
		}
	}
	startCtx, startCancel := context.WithTimeout(context.Background(), 30*time.Second)
	remoteCWD, err := unifiedExecPathURI(req.CWD)
	if err != nil {
		startCancel()
		events.Close()
		_ = client.Close()
		m.releaseProcessID(processID)
		return nil, err
	}
	var sandboxContext *execserver.FileSystemSandboxContext
	if req.PermissionProfile != nil && !req.PermissionProfile.Disabled {
		sandboxContext, err = unifiedExecSandboxContext(req)
		if err != nil {
			startCancel()
			events.Close()
			_ = client.Close()
			m.releaseProcessID(processID)
			return nil, err
		}
	}
	var sandboxJSON []byte
	if sandboxContext != nil {
		sandboxJSON, err = json.Marshal(sandboxContext)
		if err != nil {
			startCancel()
			events.Close()
			_ = client.Close()
			m.releaseProcessID(processID)
			return nil, err
		}
	}
	_, err = client.Start(startCtx, &execserver.ExecParams{
		ProcessID:             remoteID,
		Argv:                  append([]string(nil), req.Command...),
		CWD:                   remoteCWD,
		EnvPolicy:             &execserver.ExecEnvPolicy{Inherit: "all", IgnoreDefaultExcludes: true},
		Env:                   cloneEnv(req.Env),
		TTY:                   req.TTY,
		PipeStdin:             false,
		Sandbox:               sandboxJSON,
		EnforceManagedNetwork: req.EnforceManagedNetwork,
		ManagedNetwork:        unifiedExecManagedNetworkContext(req.ManagedNetwork),
		NetworkProxy:          req.RemoteNetworkProxy,
	})
	startCancel()
	if err != nil {
		events.Close()
		_ = client.Close()
		m.releaseProcessID(processID)
		return nil, err
	}
	process := &unifiedExecProcess{
		id:                   processID,
		callID:               callID,
		hookCommand:          req.HookCommand,
		tty:                  req.TTY,
		remote:               client,
		remoteURL:            req.UnifiedExecRemoteURL,
		remoteProvider:       req.UnifiedExecNoiseProvider,
		remoteSessionID:      client.SessionID(),
		remoteID:             remoteID,
		remoteEvents:         events,
		networkPolicyDecider: req.NetworkPolicyDecider,
		networkPolicyTimeout: req.NetworkPolicyDecisionTimeout,
		done:                 make(chan struct{}),
		eventDone:            make(chan struct{}),
		output:               newUnifiedExecHeadTailBuffer(unifiedExecOutputMaxBytes),
		transcript:           newUnifiedExecHeadTailBuffer(unifiedExecOutputMaxBytes),
		eventSink:            req.UnifiedExecEventSink,
		command:              append([]string(nil), req.Command...),
		cwd:                  req.CWD,
		startedAt:            time.Now(),
		threadID:             req.UnifiedExecThreadID,
		turnID:               req.UnifiedExecTurnID,
	}
	process.lastUsed = process.startedAt
	m.mu.Lock()
	m.processes[processID] = process
	m.mu.Unlock()
	go process.pollRemote()
	startupTimer := time.NewTimer(unifiedExecEarlyExitGrace)
	select {
	case <-process.done:
		if !startupTimer.Stop() {
			<-startupTimer.C
		}
	case <-startupTimer.C:
	}
	process.mu.Lock()
	sandboxDenied := process.sandboxDenied
	process.mu.Unlock()
	if !sandboxDenied {
		process.startEventStream(UnifiedExecEvent{
			Kind:        UnifiedExecEventBegin,
			CallID:      process.callID,
			Command:     append([]string(nil), process.command...),
			HookCommand: process.hookCommand,
			CWD:         process.cwd,
			ProcessID:   process.id,
			StartedAt:   process.startedAt,
		})
	}
	if req.TimeoutMS > 0 {
		go process.enforceTimeout(time.Duration(req.TimeoutMS) * time.Millisecond)
	}
	yield := clampUnifiedExecInitialYield(req.YieldTimeMS)
	return m.collect(ctx, process, yield, req.MaxOutputTokens)
}

func unifiedExecManagedNetworkContext(value *network.ProxyManagedNetworkSandboxContext) *execserver.ManagedNetworkSandboxContext {
	if value == nil {
		return nil
	}
	return &execserver.ManagedNetworkSandboxContext{
		LoopbackPorts:     append([]uint16(nil), value.LoopbackPorts...),
		AllowLocalBinding: value.AllowLocalBinding,
	}
}

func unifiedExecSandboxContext(req *ShellRequest) (*execserver.FileSystemSandboxContext, error) {
	profileJSON := strings.TrimSpace(req.PermissionProfileJSON)
	if profileJSON == "" {
		var err error
		profileJSON, err = sandbox.RuntimePermissionProfileJSON(*req.PermissionProfile)
		if err != nil {
			return nil, err
		}
	}
	portableJSON, err := portablePermissionProfileJSON(profileJSON, req.CWD)
	if err != nil {
		return nil, err
	}
	cwd, err := unifiedExecPathURI(req.CWD)
	if err != nil {
		return nil, err
	}
	windowsSandboxLevel := req.WindowsSandboxLevel
	if windowsSandboxLevel == "" {
		windowsSandboxLevel = sandbox.WindowsSandboxDisabled
	}
	return &execserver.FileSystemSandboxContext{
		Permissions:                     json.RawMessage(portableJSON),
		CWD:                             cwd,
		WindowsSandboxLevel:             string(windowsSandboxLevel),
		WindowsSandboxPrivateDesktop:    req.WindowsSandboxPrivateDesktop,
		WindowsSandboxProxySettingsMode: req.WindowsSandboxProxySettingsMode,
	}, nil
}

func portablePermissionProfileJSON(raw string, cwd string) (string, error) {
	var profile any
	if err := json.Unmarshal([]byte(raw), &profile); err != nil {
		return "", err
	}
	if err := rewritePortablePermissionProfilePaths(profile, cwd); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(profile)
	return string(encoded), err
}

func rewritePortablePermissionProfilePaths(value any, cwd string) error {
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
		if !isUnifiedExecAbsolutePath(path) {
			path = unifiedExecJoinPath(cwd, path)
		}
		uri, err := unifiedExecPathURI(path)
		if err != nil {
			return err
		}
		pathObject["path"] = uri
	}
	return nil
}

func unifiedExecPathURI(path string) (string, error) {
	legacy := utils.NewLegacyAppPathString(path)
	uri, ok := legacy.ToInferredPathURI()
	if !ok || uri == nil {
		return "", fmt.Errorf("path `%s` does not use absolute POSIX or Windows path syntax", path)
	}
	return uri.String(), nil
}

func isUnifiedExecAbsolutePath(path string) bool {
	legacy := utils.NewLegacyAppPathString(path)
	_, ok := legacy.InferAbsolutePathConvention()
	return ok
}

func unifiedExecJoinPath(base string, relative string) string {
	resolved, err := resolveRemoteUnifiedExecCWD(base, relative)
	if err != nil {
		return relative
	}
	return resolved
}

func (m *UnifiedExecManager) WriteStdin(ctx context.Context, args *WriteStdinArgs, policyMaxOutputTokens *int) (*ShellResult, error) {
	if m == nil {
		return nil, errors.New("unified exec manager is nil")
	}
	if args == nil {
		return nil, errors.New("write_stdin arguments are required")
	}
	process, err := m.processForInteraction(args.SessionID)
	if err != nil {
		return nil, err
	}
	defer process.interactions.Add(-1)
	process.interactionMu.Lock()
	defer process.interactionMu.Unlock()
	if args.Chars != "" {
		if !process.tty {
			if args.Chars != unifiedExecInterrupt {
				return nil, ErrUnifiedExecStdinClosed
			}
			if err := process.interrupt(); err != nil {
				return nil, err
			}
		} else {
			if err := process.writeStdin(args.Chars); err != nil {
				if process.hasExited() {
					// Continue to collect the exit metadata below.
				} else {
					return nil, err
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	yield := m.clampWriteYield(args.YieldTimeMS, args.Chars == "")
	maxOutputTokens := clampShellMaxOutputTokens(args.MaxOutputTokens, policyMaxOutputTokens)
	result, err := m.collect(ctx, process, yield, maxOutputTokens)
	if err != nil {
		return nil, err
	}
	if args.Chars != "" || result.ProcessID != nil {
		processID := args.SessionID
		if result.ProcessID != nil {
			processID = *result.ProcessID
		}
		process.emitEvent(UnifiedExecEvent{
			Kind:      UnifiedExecEventTerminalInteraction,
			CallID:    process.callID,
			ProcessID: processID,
			Input:     args.Chars,
		})
	}
	return result, nil
}

func (m *UnifiedExecManager) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	processes := make([]*unifiedExecProcess, 0, len(m.processes))
	for id, process := range m.processes {
		delete(m.processes, id)
		processes = append(processes, process)
	}
	m.mu.Unlock()
	var firstErr error
	for _, process := range processes {
		if process == nil {
			continue
		}
		if !process.hasExited() {
			if err := process.terminate(); firstErr == nil && err != nil {
				firstErr = err
			}
		}
		select {
		case <-process.done:
		case <-time.After(2 * time.Second):
			continue
		}
		select {
		case <-process.eventDone:
		case <-time.After(2 * time.Second):
		}
	}
	return firstErr
}

func (m *UnifiedExecManager) ListProcesses(threadID string) []UnifiedExecProcessInfo {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]UnifiedExecProcessInfo, 0, len(m.processes))
	for _, process := range m.processes {
		if process == nil || process.hasExited() || (threadID != "" && process.threadID != threadID) {
			continue
		}
		out = append(out, UnifiedExecProcessInfo{
			ItemID:    process.callID,
			ProcessID: process.id,
			Command:   process.hookCommand,
			CWD:       process.cwd,
			ThreadID:  process.threadID,
			TurnID:    process.turnID,
		})
	}
	sort.Slice(out, func(i int, j int) bool { return out[i].ProcessID < out[j].ProcessID })
	return out
}

func (m *UnifiedExecManager) TerminateProcess(threadID string, processID int) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	process := m.processes[processID]
	if process == nil || (threadID != "" && process.threadID != threadID) {
		m.mu.Unlock()
		return false
	}
	m.mu.Unlock()
	if !process.hasExited() {
		if err := process.terminate(); err != nil {
			return false
		}
		select {
		case <-process.done:
		case <-time.After(2 * time.Second):
			return false
		}
	}
	m.mu.Lock()
	if m.processes[processID] == process {
		delete(m.processes, processID)
	}
	m.mu.Unlock()
	return true
}

func (m *UnifiedExecManager) TerminateAll(threadID string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	processIDs := make([]int, 0, len(m.processes))
	for id, process := range m.processes {
		if process != nil && (threadID == "" || process.threadID == threadID) {
			processIDs = append(processIDs, id)
		}
	}
	m.mu.Unlock()
	for _, processID := range processIDs {
		_ = m.TerminateProcess(threadID, processID)
	}
}

func (m *UnifiedExecManager) collect(ctx context.Context, process *unifiedExecProcess, yieldMS uint64, maxOutputTokens *int) (*ShellResult, error) {
	started := time.Now()
	timer := time.NewTimer(time.Duration(yieldMS) * time.Millisecond)
	defer timer.Stop()
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-process.done:
	case <-timer.C:
	case <-ctx.Done():
	}
	if err := m.waitForThreadElicitation(ctx, process.threadID); err != nil {
		return nil, err
	}
	output, exited, exitCode, waitErr, timedOut := process.snapshotAndDrain()
	result := &ShellResult{
		Stdout:              output,
		Duration:            time.Since(started),
		TimedOut:            timedOut,
		ChunkID:             generateShellChunkID(),
		EventCallID:         process.callID,
		HookCommand:         process.hookCommand,
		MaxOutputTokensUsed: cloneNonNegativeInt(maxOutputTokens),
		UnifiedExecEvented:  process.eventSink != nil,
	}
	if exited {
		// Rust emits ExecCommandEnd before returning the tool output for a
		// short-lived command. The asynchronous watcher is only allowed to own
		// completion after a command has yielded into the background. Keep the
		// process exit signal prompt, but do not let a foreground collect return
		// while its canonical end event is still pending.
		if process.eventSink != nil {
			<-process.eventDone
		}
		result.ExitCode = exitCode
		result.HasExitCode = true
		m.releaseProcessID(process.id)
		if waitErr != nil {
			var exitErr *osexec.ExitError
			if !errors.As(waitErr, &exitErr) && !timedOut {
				return nil, waitErr
			}
		}
		return result, nil
	}
	processID := process.id
	result.ProcessID = &processID
	return result, nil
}

func (m *UnifiedExecManager) allocateProcessID() (int, error) {
	m.mu.Lock()
	if m.processes == nil {
		m.processes = map[int]*unifiedExecProcess{}
	}
	var pruned *unifiedExecProcess
	if len(m.processes) >= m.maxProcesses {
		meta := make([]unifiedExecProcessMeta, 0, len(m.processes))
		for id, process := range m.processes {
			if process != nil {
				meta = append(meta, unifiedExecProcessMeta{
					ID:          id,
					LastUsed:    process.lastUsed,
					Exited:      process.hasExited(),
					Interacting: process.interactions.Load() > 0,
				})
			}
		}
		candidate, ok := unifiedExecProcessIDToPrune(meta)
		if !ok {
			m.mu.Unlock()
			return 0, fmt.Errorf("%w: max=%d", ErrUnifiedExecProcessLimit, m.maxProcesses)
		}
		pruned = m.processes[candidate]
		delete(m.processes, candidate)
	}
	id := m.nextID
	m.nextID++
	m.processes[id] = nil
	m.mu.Unlock()
	if pruned != nil && !pruned.hasExited() {
		_ = pruned.terminate()
	}
	return id, nil
}

func (m *UnifiedExecManager) process(id int) (*unifiedExecProcess, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	process := m.processes[id]
	if process == nil {
		return nil, fmt.Errorf("%w %d", ErrUnifiedExecUnknownProcess, id)
	}
	process.lastUsed = time.Now()
	return process, nil
}

func (m *UnifiedExecManager) processForInteraction(id int) (*unifiedExecProcess, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	process := m.processes[id]
	if process == nil {
		return nil, fmt.Errorf("%w %d", ErrUnifiedExecUnknownProcess, id)
	}
	process.interactions.Add(1)
	process.lastUsed = time.Now()
	return process, nil
}

type unifiedExecProcessMeta struct {
	ID          int
	LastUsed    time.Time
	Exited      bool
	Interacting bool
}

func unifiedExecProcessIDToPrune(meta []unifiedExecProcessMeta) (int, bool) {
	if len(meta) == 0 {
		return 0, false
	}
	byRecency := append([]unifiedExecProcessMeta(nil), meta...)
	sort.Slice(byRecency, func(i int, j int) bool { return byRecency[i].LastUsed.After(byRecency[j].LastUsed) })
	protectedCount := min(8, len(byRecency))
	protected := make(map[int]bool, protectedCount)
	for _, entry := range byRecency[:protectedCount] {
		protected[entry.ID] = true
	}
	lru := append([]unifiedExecProcessMeta(nil), meta...)
	sort.Slice(lru, func(i int, j int) bool { return lru[i].LastUsed.Before(lru[j].LastUsed) })
	for _, entry := range lru {
		if !protected[entry.ID] && !entry.Interacting && entry.Exited {
			return entry.ID, true
		}
	}
	for _, entry := range lru {
		if !protected[entry.ID] && !entry.Interacting {
			return entry.ID, true
		}
	}
	return 0, false
}

func (m *UnifiedExecManager) releaseProcessID(id int) {
	m.mu.Lock()
	delete(m.processes, id)
	m.mu.Unlock()
}

func (m *UnifiedExecManager) clampWriteYield(value uint64, empty bool) uint64 {
	if value < unifiedExecMinYieldMS {
		value = unifiedExecMinYieldMS
	}
	if empty {
		if value < unifiedExecMinEmptyPollYieldMS {
			value = unifiedExecMinEmptyPollYieldMS
		}
		if value > m.maxEmptyPollYieldTimeMS {
			value = m.maxEmptyPollYieldTimeMS
		}
		return value
	}
	if value > unifiedExecMaxYieldMS {
		return unifiedExecMaxYieldMS
	}
	return value
}

func clampUnifiedExecInitialYield(value uint64) uint64 {
	if runtime.GOOS == "windows" && value < unifiedExecWindowsInitialYieldMS {
		value = unifiedExecWindowsInitialYieldMS
	}
	if value < unifiedExecMinYieldMS {
		value = unifiedExecMinYieldMS
	}
	if value > unifiedExecMaxYieldMS {
		value = unifiedExecMaxYieldMS
	}
	return value
}

func (p *unifiedExecProcess) finish(err error) {
	p.finishWithExit(err, nil)
}

func (p *unifiedExecProcess) finishWithExit(err error, exitCode *int) {
	p.mu.Lock()
	if p.exited {
		p.mu.Unlock()
		return
	}
	p.exited = true
	p.waitErr = err
	if exitCode != nil {
		p.exitCode = *exitCode
	} else if p.cmd != nil && p.cmd.ProcessState != nil {
		p.exitCode = unifiedExecExitCode(p.cmd.ProcessState, err)
	} else if err != nil {
		p.exitCode = -1
	}
	remote := p.remote
	p.remote = nil
	remoteEvents := p.remoteEvents
	p.remoteEvents = nil
	if p.stdin != nil {
		_ = p.stdin.Close()
		p.stdin = nil
	}
	p.mu.Unlock()
	if remoteEvents != nil {
		remoteEvents.Close()
	}
	if remote != nil {
		_ = remote.Close()
	}
	close(p.done)
	p.endOnce.Do(func() {
		go func() {
			defer close(p.eventDone)
			time.Sleep(unifiedExecTrailingOutputGrace)
			p.mu.Lock()
			transcript, _ := p.transcript.Snapshot()
			event := UnifiedExecEvent{
				Kind:        UnifiedExecEventEnd,
				CallID:      p.callID,
				Command:     append([]string(nil), p.command...),
				HookCommand: p.hookCommand,
				CWD:         p.cwd,
				ProcessID:   p.id,
				Output:      string(transcript),
				ExitCode:    p.exitCode,
				Duration:    time.Since(p.startedAt),
				StartedAt:   p.startedAt,
				TimedOut:    p.timedOut,
			}
			p.mu.Unlock()
			p.emitEvent(event)
		}()
	})
}

func (p *unifiedExecProcess) pollRemote() {
	if p == nil {
		return
	}
	lastSeq := uint64(0)
	var exitCode *int
	for {
		p.mu.Lock()
		events := p.remoteEvents
		p.mu.Unlock()
		if events == nil {
			p.finishWithExit(errors.New("exec-server process event stream is unavailable"), exitCode)
			return
		}
		eventCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		event, err := events.Next(eventCtx)
		cancel()
		if err != nil {
			if !errors.Is(err, context.DeadlineExceeded) {
				finished, recoveryErr := p.recoverRemote(&lastSeq, &exitCode, err)
				if recoveryErr != nil {
					p.finishWithExit(recoveryErr, exitCode)
					return
				}
				if finished {
					return
				}
				continue
			}
			finished, reconcileErr := p.reconcileRemote(&lastSeq, &exitCode)
			if reconcileErr != nil {
				p.finishWithExit(reconcileErr, exitCode)
				return
			}
			if finished {
				return
			}
			continue
		}
		if event.Kind == execserver.ProcessEventLagged {
			// The next retained sequenced event tells us whether the dropped
			// events are newer than the process/read cursor.
			continue
		}
		if event.Seq > lastSeq+1 || (event.Kind == execserver.ProcessEventExited && event.SandboxDenied == nil) {
			finished, reconcileErr := p.reconcileRemote(&lastSeq, &exitCode)
			if reconcileErr != nil {
				p.finishWithExit(reconcileErr, exitCode)
				return
			}
			if finished {
				return
			}
			if event.Seq <= lastSeq {
				continue
			}
		}
		if event.Seq <= lastSeq {
			continue
		}
		switch event.Kind {
		case execserver.ProcessEventOutput:
			data, decodeErr := base64.StdEncoding.DecodeString(event.Chunk)
			if decodeErr != nil {
				p.finishWithExit(decodeErr, exitCode)
				return
			}
			lastSeq = event.Seq
			p.appendOutput(data)
		case execserver.ProcessEventExited:
			lastSeq = event.Seq
			code := event.ExitCode
			exitCode = &code
			if event.SandboxDenied != nil && *event.SandboxDenied {
				p.markSandboxDenied()
				p.finishWithExit(nil, exitCode)
				return
			}
		case execserver.ProcessEventClosed:
			lastSeq = event.Seq
			p.finishWithExit(nil, exitCode)
			return
		}
	}
}

func (p *unifiedExecProcess) reconcileRemote(lastSeq *uint64, exitCode **int) (bool, error) {
	p.mu.Lock()
	remote := p.remote
	p.mu.Unlock()
	if remote == nil {
		return false, errors.New("exec-server client is unavailable")
	}
	waitMS := uint64(0)
	response, err := remote.Read(context.Background(), &execserver.ReadParams{
		ProcessID: p.remoteID,
		AfterSeq:  lastSeq,
		WaitMS:    &waitMS,
	})
	if err != nil {
		return false, err
	}
	return p.applyRemoteReadResponse(response, lastSeq, exitCode)
}

func (p *unifiedExecProcess) applyRemoteReadResponse(response *execserver.ReadResponse, lastSeq *uint64, exitCode **int) (bool, error) {
	if response == nil {
		return false, errors.New("exec-server returned an empty process/read response")
	}
	for _, chunk := range response.Chunks {
		if chunk.Seq <= *lastSeq {
			continue
		}
		data, decodeErr := base64.StdEncoding.DecodeString(chunk.Chunk)
		if decodeErr != nil {
			return false, decodeErr
		}
		p.appendOutput(data)
	}
	if response.NextSeq > 0 {
		*lastSeq = max(*lastSeq, response.NextSeq-1)
	}
	if response.ExitCode != nil {
		code := *response.ExitCode
		*exitCode = &code
	}
	if response.Failure != nil {
		return false, errors.New(*response.Failure)
	}
	if response.SandboxDenied {
		p.markSandboxDenied()
		p.finishWithExit(nil, *exitCode)
		return true, nil
	}
	if response.Closed {
		p.finishWithExit(nil, *exitCode)
		return true, nil
	}
	return false, nil
}

func (p *unifiedExecProcess) recoverRemote(lastSeq *uint64, exitCode **int, disconnectErr error) (bool, error) {
	p.mu.Lock()
	remoteURL := p.remoteURL
	remoteProvider := p.remoteProvider
	sessionID := p.remoteSessionID
	processID := p.remoteID
	exited := p.exited
	p.mu.Unlock()
	if exited || (strings.TrimSpace(remoteURL) == "" && remoteProvider == nil) || strings.TrimSpace(sessionID) == "" {
		return false, disconnectErr
	}
	deadline := time.Now().Add(unifiedExecRemoteRecoveryTimeout)
	lastErr := disconnectErr
	for time.Now().Before(deadline) {
		attemptCtx, cancel := context.WithDeadline(context.Background(), deadline)
		options := execserver.DialClientOptions{ClientName: "codex-go-unified-exec", ResumeSessionID: sessionID}
		var client *execserver.Client
		var err error
		if remoteProvider != nil {
			client, err = execserver.DialNoiseRendezvousClient(attemptCtx, remoteProvider, options)
		} else {
			client, err = execserver.DialClientWithOptions(attemptCtx, remoteURL, options)
		}
		cancel()
		if err == nil {
			events, subscribeErr := client.SubscribeProcessEvents(processID)
			if subscribeErr != nil {
				_ = client.Close()
				lastErr = subscribeErr
			} else {
				p.mu.Lock()
				policyDecider := p.networkPolicyDecider
				policyTimeout := p.networkPolicyTimeout
				p.mu.Unlock()
				if policyDecider != nil {
					if registerErr := client.RegisterNetworkPolicyController(processID, policyTimeout, policyDecider); registerErr != nil {
						events.Close()
						_ = client.Close()
						lastErr = registerErr
						continue
					}
				}
				waitMS := uint64(0)
				response, readErr := client.Read(context.Background(), &execserver.ReadParams{
					ProcessID: processID,
					AfterSeq:  lastSeq,
					WaitMS:    &waitMS,
				})
				if readErr == nil {
					p.mu.Lock()
					if p.exited {
						p.mu.Unlock()
						events.Close()
						_ = client.Close()
						return true, nil
					}
					oldClient := p.remote
					oldEvents := p.remoteEvents
					p.remote = client
					p.remoteEvents = events
					p.mu.Unlock()
					if oldEvents != nil {
						oldEvents.Close()
					}
					if oldClient != nil {
						_ = oldClient.Close()
					}
					return p.applyRemoteReadResponse(response, lastSeq, exitCode)
				}
				events.Close()
				_ = client.Close()
				lastErr = readErr
			}
		} else {
			lastErr = err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		delay := unifiedExecRemoteRecoveryRetry
		if delay > remaining {
			delay = remaining
		}
		time.Sleep(delay)
	}
	return false, fmt.Errorf("%v; failed to resume exec-server session: recovery timed out after %s: %w", disconnectErr, unifiedExecRemoteRecoveryTimeout, lastErr)
}

func (p *unifiedExecProcess) writeStdin(input string) error {
	p.mu.Lock()
	stdin := p.stdin
	remote := p.remote
	remoteID := p.remoteID
	p.remoteWrite++
	writeID := p.remoteWrite
	exited := p.exited
	p.mu.Unlock()
	if exited {
		return ErrUnifiedExecStdinClosed
	}
	if remote != nil {
		response, err := remote.Write(context.Background(), &execserver.WriteParams{
			ProcessID: remoteID,
			Chunk:     base64.StdEncoding.EncodeToString([]byte(input)),
			WriteID:   fmt.Sprintf("write-%d", writeID),
		})
		if err != nil {
			return err
		}
		if response.Status != "accepted" {
			return ErrUnifiedExecStdinClosed
		}
		return nil
	}
	if stdin == nil {
		return ErrUnifiedExecStdinClosed
	}
	_, err := io.WriteString(stdin, input)
	return err
}

func (p *unifiedExecProcess) interrupt() error {
	if p == nil {
		return ErrUnifiedExecStdinClosed
	}
	p.mu.Lock()
	remote := p.remote
	remoteID := p.remoteID
	tty := p.tty
	process := (*os.Process)(nil)
	if p.cmd != nil {
		process = p.cmd.Process
	}
	p.mu.Unlock()
	if runtime.GOOS == "windows" && !tty {
		return p.terminate()
	}
	if remote != nil {
		_, err := remote.Signal(context.Background(), &execserver.SignalParams{ProcessID: remoteID, Signal: "interrupt"})
		return err
	}
	return interruptUnifiedExecProcess(process)
}

func (p *unifiedExecProcess) terminate() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	remote := p.remote
	remoteID := p.remoteID
	sandboxProcess := p.sandbox
	process := (*os.Process)(nil)
	if p.cmd != nil {
		process = p.cmd.Process
	}
	p.mu.Unlock()
	if remote != nil {
		_, err := remote.Terminate(context.Background(), &execserver.TerminateParams{ProcessID: remoteID})
		return err
	}
	if sandboxProcess != nil {
		return sandboxProcess.Terminate()
	}
	if process == nil {
		return nil
	}
	return process.Kill()
}

func (p *unifiedExecProcess) enforceTimeout(timeout time.Duration) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-p.done:
		return
	case <-timer.C:
		p.mu.Lock()
		p.timedOut = true
		p.mu.Unlock()
		_ = p.terminate()
	}
}

func (p *unifiedExecProcess) hasExited() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exited
}

func (p *unifiedExecProcess) appendOutput(data []byte) {
	p.mu.Lock()
	if p.output == nil {
		p.output = newUnifiedExecHeadTailBuffer(unifiedExecOutputMaxBytes)
	}
	_, _ = p.output.Write(data)
	if p.transcript == nil {
		p.transcript = newUnifiedExecHeadTailBuffer(unifiedExecOutputMaxBytes)
	}
	_, _ = p.transcript.Write(data)
	deltas := p.outputDeltasLocked(data)
	p.mu.Unlock()
	for _, delta := range deltas {
		p.emitEvent(UnifiedExecEvent{
			Kind:      UnifiedExecEventOutputDelta,
			CallID:    p.callID,
			ProcessID: p.id,
			Output:    delta,
		})
	}
}

func (p *unifiedExecProcess) outputDeltasLocked(data []byte) []string {
	if p == nil || p.eventSink == nil || p.emittedDeltas >= unifiedExecMaxOutputDeltas || len(data) == 0 {
		return nil
	}
	p.eventPending = append(p.eventPending, data...)
	remaining := unifiedExecMaxOutputDeltas - p.emittedDeltas
	deltas := make([]string, 0, min(remaining, 4))
	for len(p.eventPending) > 0 && len(deltas) < remaining {
		prefix, rest, ok := splitUnifiedExecValidUTF8Prefix(p.eventPending, unifiedExecOutputDeltaMaxBytes)
		if !ok {
			break
		}
		deltas = append(deltas, string(prefix))
		p.eventPending = rest
		p.emittedDeltas++
	}
	return deltas
}

func splitUnifiedExecValidUTF8Prefix(buffer []byte, maxBytes int) ([]byte, []byte, bool) {
	if len(buffer) == 0 || maxBytes <= 0 {
		return nil, buffer, false
	}
	maxLen := min(len(buffer), maxBytes)
	for split := maxLen; split > 0; split-- {
		if utf8.Valid(buffer[:split]) {
			prefix := append([]byte(nil), buffer[:split]...)
			rest := append([]byte(nil), buffer[split:]...)
			return prefix, rest, true
		}
		if maxLen-split > utf8.UTFMax {
			break
		}
	}
	return nil, buffer, false
}

func (p *unifiedExecProcess) emitEvent(event UnifiedExecEvent) {
	if p == nil {
		return
	}
	p.eventMu.Lock()
	defer p.eventMu.Unlock()
	p.mu.Lock()
	if p.eventSink == nil {
		p.mu.Unlock()
		return
	}
	if !p.eventsStarted {
		p.pendingEvents = append(p.pendingEvents, event)
		p.mu.Unlock()
		return
	}
	sink := p.eventSink
	p.mu.Unlock()
	sink(event)
}

func (p *unifiedExecProcess) startEventStream(begin UnifiedExecEvent) {
	if p == nil {
		return
	}
	p.eventMu.Lock()
	defer p.eventMu.Unlock()
	p.mu.Lock()
	if p.eventsStarted || p.eventSink == nil {
		p.mu.Unlock()
		return
	}
	sink := p.eventSink
	p.eventsStarted = true
	pending := append([]UnifiedExecEvent(nil), p.pendingEvents...)
	p.pendingEvents = nil
	p.mu.Unlock()
	sink(begin)
	for _, event := range pending {
		sink(event)
	}
}

func (p *unifiedExecProcess) markSandboxDenied() {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.sandboxDenied = true
	p.mu.Unlock()
}

func (p *unifiedExecProcess) snapshotAndDrain() (string, bool, int, error, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var output []byte
	if p.output != nil {
		output, _ = p.output.Drain()
	}
	return string(output), p.exited, p.exitCode, p.waitErr, p.timedOut
}

type unifiedExecOutputWriter struct {
	process *unifiedExecProcess
}

func (w unifiedExecOutputWriter) Write(data []byte) (int, error) {
	if w.process != nil {
		w.process.appendOutput(data)
	}
	return len(data), nil
}
