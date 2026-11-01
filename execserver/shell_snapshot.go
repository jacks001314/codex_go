package execserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"codex_go/execpolicy"
	"codex_go/network"
	"codex_go/shell"
	"codex_go/utils"
)

const (
	maxShellSnapshotScopeBytes = 256
	// maxCachedShellSnapshots bounds the executor's snapshot cache, mirroring
	// Rust's MAX_CACHED_SNAPSHOTS (#39957).
	maxCachedShellSnapshots = 16
	// maxShellSnapshotEnvValueBytes bounds one environment chunk used to replay
	// an in-memory snapshot, mirroring Rust's MAX_SNAPSHOT_ENV_VALUE_BYTES.
	maxShellSnapshotEnvValueBytes = 60 * 1024
	// Rust #51350: file replay admits 4 MiB of shell state and 8 MiB of raw
	// capture output while the captured environment stays capped at 512 KiB.
	maxFileShellSnapshotBytes        = 4 * 1024 * 1024
	maxFileShellSnapshotCaptureBytes = 8 * 1024 * 1024
	maxShellSnapshotEnvironmentBytes = 512 * 1024
	shellSnapshotCaptureTimeout      = 10 * time.Second
	shellSnapshotRetryBackoff        = time.Second
	maxShellSnapshotAttempts         = 3
	// shellSnapshotCarrierFd is the descriptor the child receives for the replay
	// file: os/exec assigns inherited files above the three standard streams.
	shellSnapshotCarrierFd = 3
)

var shellSnapshotPlatformSupported = runtime.GOOS != "windows"

type shellSnapshotManager struct {
	mu       sync.Mutex
	cache    map[string]*shellSnapshotEntry
	order    []string
	failures map[string]shellSnapshotFailure
	inflight map[string]*shellSnapshotFlight
	capture  func(context.Context, []string, string, map[string]string) ([]byte, error)
	// probes caches whether a shell can source an inherited descriptor; the
	// answer depends only on the shell and the host (Rust #48078).
	probes map[string]bool
	now    func() time.Time
}

func newShellSnapshotManager() *shellSnapshotManager {
	return &shellSnapshotManager{
		cache:    map[string]*shellSnapshotEntry{},
		failures: map[string]shellSnapshotFailure{},
		inflight: map[string]*shellSnapshotFlight{},
		probes:   map[string]bool{},
		capture:  runShellSnapshotCapture,
		now:      time.Now,
	}
}

// shellSnapshotEntry is one cached capture. Rust keeps the captured state in
// executor memory and materializes an unnamed replay file per launch when the
// shell can source an inherited descriptor (#39957, #48078).
type shellSnapshotEntry struct {
	state string
	// fileSource selects descriptor replay instead of environment replay.
	fileSource bool
}

type shellSnapshotFailure struct {
	attempts int
	retryAt  time.Time
	err      error
}

type shellSnapshotFlight struct {
	done chan struct{}
}

func (m *shellSnapshotManager) rewrite(ctx context.Context, params *ExecParams, captureEnv map[string]string, credentialPairs []network.CredentialMaskPair, credentialTextUnsafe func(string) bool, restoreFallback func(map[string]string)) (*ExecParams, *os.File, error) {
	if m == nil || params == nil || params.ShellSnapshot == nil {
		return params, nil, nil
	}
	if !shellSnapshotPlatformSupported {
		return nil, nil, requestError(-32602, "shell snapshots are unsupported on this platform")
	}
	request := params.ShellSnapshot
	scope := strings.TrimSpace(request.ScopeID)
	if scope == "" || len(scope) > maxShellSnapshotScopeBytes {
		return nil, nil, requestError(-32602, fmt.Sprintf("shell snapshot scope must be non-empty and at most %d bytes", maxShellSnapshotScopeBytes))
	}
	shellType, ok := shellSnapshotType(request.Shell)
	if !ok {
		return nil, nil, requestError(-32602, fmt.Sprintf("shell snapshots are unsupported for shell `%s`", request.Shell.Name))
	}
	scriptIndex := shellSnapshotScriptIndex(params.Argv, request.Shell)
	if scriptIndex < 0 {
		return nil, nil, requestError(-32602, "shell snapshot requires a shell -c or -lc invocation")
	}
	entry, err := m.snapshotEntry(ctx, params, shellType, captureEnv, credentialPairs, credentialTextUnsafe)
	if err != nil {
		rewritten := *params
		rewritten.Argv = append([]string(nil), params.Argv...)
		rewritten.Env = cloneShellSnapshotEnv(params.Env)
		if restoreFallback != nil {
			// Rust's credentials::restore_fallback restores every brokered
			// placeholder the proxy knows about and disables brokering for this
			// child, not only the pairs this capture masked.
			restoreFallback(rewritten.Env)
		} else {
			network.RestoreSnapshotCredentials(rewritten.Env, credentialPairs)
		}
		warning := "Credential masking is unavailable for this shell snapshot; running with unmasked credentials."
		rewritten.Argv[scriptIndex] = "\\builtin printf '%s\\n' " + shellSingleQuote(warning) + " >&2\n" + params.Argv[scriptIndex]
		return &rewritten, nil, nil
	}
	rewritten := *params
	rewritten.Argv = append([]string(nil), params.Argv...)
	rewritten.Env = cloneShellSnapshotEnv(params.Env)
	if rewritten.Env == nil {
		rewritten.Env = map[string]string{}
	}
	// Rust #48078 materializes the replay file per launch and hands the reader to
	// the child; a transport that cannot be prepared falls back to normal startup.
	var reader *os.File
	if entry.fileSource {
		materialized, materializeErr := materializeShellSnapshotTransport(shellType, entry.state)
		if materializeErr != nil {
			return params, nil, nil
		}
		reader = materialized
	}
	protected := len(credentialPairs) > 0
	restore := shellSnapshotReplayScript(&rewritten, entry, protected, reader != nil)
	flag, startup := shellSnapshotStartupScript(shellType)
	rewritten.Argv[scriptIndex-1] = flag
	restoreGate := "if ! " + restore + " >/dev/null; then printf 'failed to restore shell snapshot\\n' >&2; fi"
	if protected {
		restoreGate = "if ! " + restore + " >/dev/null 2>&1; then printf 'failed to restore shell snapshot\\n' >&2; exit 1; fi"
	}
	rewritten.Argv[scriptIndex] = startup + restoreGate + "\n" +
		shellSnapshotLaunchEnvExports(params.Env) +
		params.Argv[scriptIndex]
	return &rewritten, reader, nil
}

// shellSnapshotStartupScript returns the launch flag and the startup line that
// keeps automatic startup files from re-running after the snapshot restores the
// user's state, mirroring Rust's bash -pc / zsh -fc switch (#39957).
func shellSnapshotStartupScript(shellType shell.ShellType) (string, string) {
	switch shellType {
	case shell.ShellBash:
		return "-pc", "set +o privileged\n"
	case shell.ShellZsh:
		return "-fc", "setopt RCS\n"
	default:
		return "-c", ""
	}
}

// shellSnapshotReplayScript builds the restore command for one snapshot. Small
// states stay entirely in executor memory and are replayed through chunked
// __CODEX_SHELL_SNAPSHOT_STATE_* environment variables, while large states use
// the materialized replay file (Rust #39957).
func shellSnapshotReplayScript(params *ExecParams, entry *shellSnapshotEntry, protected bool, descriptor bool) string {
	if entry == nil || entry.state == "" {
		return ":"
	}
	if descriptor {
		// Rust #48078 sources the inherited descriptor; the replay file closes
		// the carrier before it restores the user's functions.
		return fmt.Sprintf(". /dev/fd/%d", shellSnapshotCarrierFd)
	}
	names := make([]string, 0, 8)
	state := entry.state
	for start := 0; start < len(state); {
		end := start + maxShellSnapshotEnvValueBytes
		if end > len(state) {
			end = len(state)
		}
		for end > start && end < len(state) && !utf8.RuneStart(state[end]) {
			end--
		}
		name := fmt.Sprintf("__CODEX_SHELL_SNAPSHOT_STATE_%d", len(names))
		params.Env[name] = state[start:end]
		names = append(names, name)
		start = end
	}
	var expansion strings.Builder
	for _, name := range names {
		if protected {
			expansion.WriteString("${" + name + ":?missing shell snapshot chunk}")
			continue
		}
		expansion.WriteString("${" + name + "}")
	}
	return "eval \"unset " + strings.Join(names, " ") + "\n" + expansion.String() + "\""
}

func shellSnapshotLaunchEnvExports(env map[string]string) string {
	keys := make([]string, 0, 32)
	seen := map[string]struct{}{}
	addKey := func(key string) {
		key = strings.TrimSpace(key)
		if key == "" {
			return
		}
		if _, ok := env[key]; !ok {
			return
		}
		if _, duplicate := seen[key]; duplicate {
			return
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	for _, key := range network.ProxyURLEnvKeys {
		addKey(key)
	}
	for _, key := range network.NoProxyEnvKeys {
		addKey(key)
	}
	addKey(network.ProxyActiveEnvKey)
	addKey(network.ProxyAllowLocalBindingEnvKey)
	addKey(network.CredentialBrokerActiveEnvKey)
	addKey(network.BrokeredCredentialsEnvKey)
	if raw := env[network.BrokeredCredentialsEnvKey]; raw != "" {
		var entries [][]string
		if json.Unmarshal([]byte(raw), &entries) == nil {
			for _, entry := range entries {
				if len(entry) > 0 {
					addKey(entry[0])
				}
			}
		}
	}
	for _, key := range network.ProxyCredentialBrokerEnvKeys() {
		addKey(key)
	}
	var exports strings.Builder
	for _, key := range keys {
		exports.WriteString("\\builtin export ")
		exports.WriteString(key)
		exports.WriteString("=")
		exports.WriteString(shellSingleQuote(env[key]))
		exports.WriteString(" || exit 1\n")
	}
	return exports.String()
}

func (m *shellSnapshotManager) snapshotEntry(ctx context.Context, params *ExecParams, shellType shell.ShellType, captureEnv map[string]string, credentialPairs []network.CredentialMaskPair, credentialTextUnsafe func(string) bool) (*shellSnapshotEntry, error) {
	return m.snapshotEntryMode(ctx, params, shellType, captureEnv, credentialPairs, credentialTextUnsafe, true)
}

// prewarmSnapshotEntry captures speculatively. Rust's CapturePurpose::Prewarm
// leaves the cache cell uninitialized on failure, so a failed prewarm must not
// make the next real launch wait for the retry backoff or spend its attempts.
func (m *shellSnapshotManager) prewarmSnapshotEntry(ctx context.Context, params *ExecParams, shellType shell.ShellType, captureEnv map[string]string, credentialPairs []network.CredentialMaskPair, credentialTextUnsafe func(string) bool) (*shellSnapshotEntry, error) {
	return m.snapshotEntryMode(ctx, params, shellType, captureEnv, credentialPairs, credentialTextUnsafe, false)
}

func (m *shellSnapshotManager) snapshotEntryMode(ctx context.Context, params *ExecParams, shellType shell.ShellType, captureEnv map[string]string, credentialPairs []network.CredentialMaskPair, credentialTextUnsafe func(string) bool, recordFailures bool) (*shellSnapshotEntry, error) {
	key, err := shellSnapshotKey(params, shellType)
	if err != nil {
		return nil, err
	}
	if len(credentialPairs) > 0 {
		// Rust prepares protected captures once per launch and never caches
		// them: the replay carries this launch's broker dummies, so reusing it
		// under a later broker state would restore stale placeholders.
		return m.captureSnapshot(ctx, params, shellType, captureEnv, credentialPairs, credentialTextUnsafe, key)
	}
	attempts := 1
	for {
		m.mu.Lock()
		if entry := m.cache[key]; entry != nil {
			m.mu.Unlock()
			return entry, nil
		}
		if failure, ok := m.failures[key]; ok {
			if failure.attempts >= maxShellSnapshotAttempts || m.now().Before(failure.retryAt) {
				m.mu.Unlock()
				return nil, failure.err
			}
			attempts = failure.attempts + 1
			delete(m.failures, key)
		}
		if flight := m.inflight[key]; flight != nil {
			done := flight.done
			m.mu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		flight := &shellSnapshotFlight{done: make(chan struct{})}
		m.inflight[key] = flight
		m.mu.Unlock()

		entry, captureErr := m.captureSnapshot(ctx, params, shellType, captureEnv, credentialPairs, credentialTextUnsafe, key)
		m.mu.Lock()
		delete(m.inflight, key)
		close(flight.done)
		if captureErr != nil {
			if recordFailures {
				m.failures[key] = shellSnapshotFailure{
					attempts: attempts,
					retryAt:  m.now().Add(shellSnapshotRetryBackoff),
					err:      captureErr,
				}
			}
		} else {
			m.storeEntryLocked(key, entry)
			delete(m.failures, key)
		}
		m.mu.Unlock()
		return entry, captureErr
	}
}

// descriptorReplay reports whether this shell can source an inherited
// descriptor. Rust probes inside the capture sandbox; the exec server's capture
// is not sandboxed, so the probe establishes that the shell resolves /dev/fd/N
// on this host, and the answer is cached per shell (#48078).
func (m *shellSnapshotManager) descriptorReplay(shellType shell.ShellType, shellPath string) bool {
	key := string(shellType) + "\x00" + shellPath
	m.mu.Lock()
	cached, ok := m.probes[key]
	m.mu.Unlock()
	if ok {
		return cached
	}
	reader, err := materializeShellSnapshotTransport(shellType, "")
	available := err == nil && shellSnapshotDescriptorReplay(shellPath, reader)
	if err == nil {
		_ = reader.Close()
	}
	m.mu.Lock()
	m.probes[key] = available
	m.mu.Unlock()
	return available
}

// storeEntryLocked caches one capture and evicts the oldest entry past the
// bounded cache size. An entry only holds bytes; replay files live per launch.
func (m *shellSnapshotManager) storeEntryLocked(key string, entry *shellSnapshotEntry) {
	if entry == nil {
		return
	}
	m.cache[key] = entry
	m.forgetOrderLocked(key)
	m.order = append(m.order, key)
	for len(m.order) > maxCachedShellSnapshots {
		oldest := m.order[0]
		m.order = m.order[1:]
		delete(m.cache, oldest)
	}
}

func (m *shellSnapshotManager) forgetOrderLocked(key string) {
	for index, tracked := range m.order {
		if tracked != key {
			continue
		}
		m.order = append(m.order[:index], m.order[index+1:]...)
		return
	}
}

// shellSnapshotRenderPolicy converts the launch's wire environment policy into
// the filter the replay render applies. Rust's parse_snapshot renders the
// executor-owned snapshot under params.env_policy, so a variable the launch
// excludes is never re-exported from the captured startup state. The capture
// already holds exactly what the shell exported, so Rust forces inherit=all and
// treats the launch policy as a narrowing filter over the captured names.
func shellSnapshotRenderPolicy(params *ExecParams) *execpolicy.EnvPolicy {
	if params == nil || params.EnvPolicy == nil {
		return nil
	}
	table := map[string]any{
		"inherit":                 "all",
		"ignore_default_excludes": params.EnvPolicy.IgnoreDefaultExcludes,
	}
	if patterns := snapshotPolicyPatterns(params.EnvPolicy.Exclude); len(patterns) > 0 {
		table["exclude"] = patterns
	}
	if patterns := snapshotPolicyPatterns(params.EnvPolicy.IncludeOnly); len(patterns) > 0 {
		table["include_only"] = patterns
	}
	if len(params.EnvPolicy.Set) > 0 {
		set := make(map[string]any, len(params.EnvPolicy.Set))
		for name, value := range params.EnvPolicy.Set {
			set[name] = value
		}
		table["set"] = set
	}
	return execpolicy.EnvPolicyFromShellEnvironmentPolicy(table, "")
}

// snapshotPolicyPatterns re-types the wire pattern list for the config-table
// parser, which reads untyped JSON slices.
func snapshotPolicyPatterns(values []string) []any {
	if len(values) == 0 {
		return nil
	}
	patterns := make([]any, 0, len(values))
	for _, value := range values {
		patterns = append(patterns, value)
	}
	return patterns
}

// validateMaskedShellSnapshot rejects a capture that still carries a real
// broker credential, either verbatim or reassembled by shell quoting. Rust
// #54432 refuses such a replay script; the caller then restores the launch
// environment and warns that credential masking is unavailable.
func validateMaskedShellSnapshot(text string, shellType shell.ShellType, credentialPairs []network.CredentialMaskPair, credentialTextUnsafe func(string) bool) error {
	if len(credentialPairs) == 0 && credentialTextUnsafe == nil {
		return nil
	}
	containsCredential := func(value string) bool {
		return network.SnapshotTextContainsUnmaskedCredential(value, credentialPairs) ||
			(credentialTextUnsafe != nil && credentialTextUnsafe(value))
	}
	if containsCredential(text) {
		return errors.New("shell snapshot capture retains a real credential")
	}
	for _, word := range shell.SnapshotLiteralWords(shellType, text) {
		if containsCredential(word) {
			return errors.New("shell snapshot capture reassembles a real credential")
		}
	}
	return nil
}

func (m *shellSnapshotManager) captureSnapshot(ctx context.Context, params *ExecParams, shellType shell.ShellType, captureEnv map[string]string, credentialPairs []network.CredentialMaskPair, credentialTextUnsafe func(string) bool, key string) (*shellSnapshotEntry, error) {

	ctx, captureSpan := startExecServerSpan(
		ctx,
		execServerCaptureSnapshotSpan,
		execServerProcessSpanAttributes(params.ProcessID, nil),
	)
	if captureSpan != nil {
		defer captureSpan.End()
	}
	// Rust's exec-server captures with the source-capture variant: options and
	// aliases are emitted inside one brace group, so replaying the state cannot
	// let an alias or quoting option reinterpret the records that follow it
	// (snapshot_source_capture_script, #48078).
	captureScript, ok := shell.SnapshotSourceCaptureScript(shellType, shell.SnapshotCaptureOptions{
		Startup: shell.SnapshotStartupInteractive, Declarations: true, Environment: true,
	})
	if !ok {
		return nil, errors.New("unsupported snapshot shell")
	}
	cwd := shellSnapshotNativeCWD(params.CWD)
	env := shellSnapshotEnvironment(captureEnv)
	captureCtx, cancel := context.WithTimeout(ctx, shellSnapshotCaptureTimeout)
	defer cancel()
	output, err := m.capture(captureCtx, []string{params.ShellSnapshot.Shell.Path, "-lc", captureScript}, cwd, env)
	if err != nil {
		return nil, err
	}
	if len(output) > maxFileShellSnapshotCaptureBytes {
		return nil, fmt.Errorf("shell snapshot capture exceeds %d bytes", maxFileShellSnapshotCaptureBytes)
	}
	captured := shell.ParseCapturedSnapshot(shellType, output)
	if captured == nil {
		return nil, errors.New("invalid captured shell state")
	}
	rendered := network.MaskSnapshotText(captured.RenderScript(shellSnapshotRenderPolicy(params)), credentialPairs)
	if err := validateMaskedShellSnapshot(rendered, shellType, credentialPairs, credentialTextUnsafe); err != nil {
		return nil, err
	}
	if err := validateShellSnapshotSizes(captured, rendered); err != nil {
		return nil, err
	}
	// Rust #39957 keeps the state in executor memory; #48078 materializes an
	// unnamed file per launch instead of caching a named one. Bash and zsh source
	// the descriptor whenever the capture sandbox allows it, and only `sh` or a
	// host without /dev/fd falls back to environment replay.
	descriptor := m.descriptorReplay(shellType, params.ShellSnapshot.Shell.Path)
	return &shellSnapshotEntry{state: rendered, fileSource: descriptor}, nil
}

// capturedSnapshotEnvironmentBytes is the captured environment's replay cost:
// Rust adds it to the in-memory state budget and caps it on its own.
func capturedSnapshotEnvironmentBytes(captured *shell.CapturedSnapshot) int {
	if captured == nil {
		return 0
	}
	environmentBytes := len(captured.Environment)
	if captured.StartupEnvironment != nil && len(captured.StartupEnvironment.Environment) > environmentBytes {
		environmentBytes = len(captured.StartupEnvironment.Environment)
	}
	return environmentBytes
}

// validateShellSnapshotSizes mirrors Rust #51350's replay limits: the captured
// environment is capped independently from the 4 MiB file-replay state budget.
func validateShellSnapshotSizes(captured *shell.CapturedSnapshot, renderedState string) error {
	if captured == nil {
		return errors.New("invalid captured shell state")
	}
	environmentBytes := capturedSnapshotEnvironmentBytes(captured)
	if environmentBytes > maxShellSnapshotEnvironmentBytes {
		return fmt.Errorf("shell snapshot environment exceeds %d bytes", maxShellSnapshotEnvironmentBytes)
	}
	if len(renderedState) > maxFileShellSnapshotBytes {
		return fmt.Errorf("shell snapshot state exceeds %d bytes", maxFileShellSnapshotBytes)
	}
	return nil
}

func (m *shellSnapshotManager) close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.cache = map[string]*shellSnapshotEntry{}
	m.order = nil
	m.failures = map[string]shellSnapshotFailure{}
	m.inflight = map[string]*shellSnapshotFlight{}
	m.mu.Unlock()
}

func shellSnapshotType(info ShellInfo) (shell.ShellType, bool) {
	name := strings.ToLower(filepath.Base(strings.TrimSpace(info.Name)))
	name = strings.TrimSuffix(name, ".exe")
	if pathName := strings.ToLower(filepath.Base(strings.TrimSpace(info.Path))); pathName != "" {
		pathName = strings.TrimSuffix(pathName, ".exe")
		if pathName == "zsh" || pathName == "bash" || pathName == "sh" || pathName == "dash" || pathName == "ash" {
			name = pathName
		}
	}
	switch name {
	case "zsh":
		return shell.ShellZsh, true
	case "bash":
		return shell.ShellBash, true
	case "sh", "dash", "ash":
		return shell.ShellSh, true
	default:
		return "", false
	}
}

func shellSnapshotScriptIndex(argv []string, info ShellInfo) int {
	if len(argv) < 3 {
		return -1
	}
	base := strings.ToLower(strings.TrimSuffix(filepath.Base(strings.TrimSpace(argv[0])), ".exe"))
	expected := strings.ToLower(strings.TrimSuffix(filepath.Base(strings.TrimSpace(info.Path)), ".exe"))
	if base != expected && base != strings.ToLower(strings.TrimSpace(info.Name)) {
		return -1
	}
	for index := 1; index < len(argv)-1 && index < 3; index++ {
		switch argv[index] {
		case "-c", "-lc":
			return index + 1
		}
	}
	return -1
}

// shellSnapshotKey mirrors Rust's cache entry identity
// (exec-server/src/shell_snapshot.rs `ShellSnapshotCache::prepare`): the scope
// request, the working directory, the environment policy and the sandbox. The
// resolved launch environment is deliberately excluded, exactly like upstream,
// because replay re-exports the launch's own environment over the restored
// state; keying on it would make every tool call (which carries its own call
// id) capture again.
func shellSnapshotKey(params *ExecParams, shellType shell.ShellType) (string, error) {
	policyData, err := json.Marshal(params.EnvPolicy)
	if err != nil {
		return "", err
	}
	sandboxData, err := json.Marshal(params.Sandbox)
	if err != nil {
		return "", err
	}
	cwd := params.CWD
	if key, ok := utils.PathIdentityKey(cwd); ok {
		cwd = key
	}
	return strings.Join([]string{
		params.ShellSnapshot.ScopeID,
		string(shellType),
		params.ShellSnapshot.Shell.Path,
		cwd,
		string(policyData),
		string(sandboxData),
	}, "\x00"), nil
}

func shellSnapshotNativeCWD(value string) string {
	if parsed, err := utils.Parse(strings.TrimSpace(value)); err == nil {
		return parsed.NativePathString()
	}
	return strings.TrimSpace(value)
}

func shellSnapshotEnvironment(values map[string]string) map[string]string {
	if values != nil {
		cloned := make(map[string]string, len(values))
		for key, value := range values {
			cloned[key] = value
		}
		return cloned
	}
	env := map[string]string{}
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if ok && key != "" {
			env[key] = value
		}
	}
	return env
}

func cloneShellSnapshotEnv(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func runShellSnapshotCapture(ctx context.Context, command []string, cwd string, env map[string]string) ([]byte, error) {
	if len(command) == 0 {
		return nil, errors.New("shell snapshot command is required")
	}
	process := exec.CommandContext(ctx, command[0], command[1:]...)
	process.Dir = cwd
	process.Env = envPairs(env)
	return process.Output()
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
