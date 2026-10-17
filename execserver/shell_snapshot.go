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

	"codex_go/network"
	"codex_go/shell"
	"codex_go/utils"
)

const (
	maxShellSnapshotScopeBytes  = 256
	shellSnapshotCaptureTimeout = 10 * time.Second
)

var shellSnapshotPlatformSupported = runtime.GOOS != "windows"

type shellSnapshotManager struct {
	mu      sync.Mutex
	cache   map[string]string
	capture func(context.Context, []string, string, map[string]string) ([]byte, error)
}

func newShellSnapshotManager() *shellSnapshotManager {
	return &shellSnapshotManager{cache: map[string]string{}, capture: runShellSnapshotCapture}
}

func (m *shellSnapshotManager) rewrite(ctx context.Context, params *ExecParams, captureEnv map[string]string, credentialPairs []network.CredentialMaskPair) (*ExecParams, error) {
	if m == nil || params == nil || params.ShellSnapshot == nil {
		return params, nil
	}
	if !shellSnapshotPlatformSupported {
		return nil, requestError(-32602, "shell snapshots are unsupported on this platform")
	}
	request := params.ShellSnapshot
	scope := strings.TrimSpace(request.ScopeID)
	if scope == "" || len(scope) > maxShellSnapshotScopeBytes {
		return nil, requestError(-32602, fmt.Sprintf("shell snapshot scope must be non-empty and at most %d bytes", maxShellSnapshotScopeBytes))
	}
	shellType, ok := shellSnapshotType(request.Shell)
	if !ok {
		return nil, requestError(-32602, fmt.Sprintf("shell snapshots are unsupported for shell `%s`", request.Shell.Name))
	}
	scriptIndex := shellSnapshotScriptIndex(params.Argv, request.Shell)
	if scriptIndex < 0 {
		return nil, requestError(-32602, "shell snapshot requires a shell -c or -lc invocation")
	}
	path, err := m.snapshotPath(ctx, params, shellType, captureEnv, credentialPairs)
	if err != nil {
		rewritten := *params
		rewritten.Argv = append([]string(nil), params.Argv...)
		rewritten.Env = cloneShellSnapshotEnv(params.Env)
		network.RestoreSnapshotCredentials(rewritten.Env, credentialPairs)
		warning := "Credential masking is unavailable for this shell snapshot; running with unmasked credentials."
		rewritten.Argv[scriptIndex] = "\\builtin printf '%s\\n' " + shellSingleQuote(warning) + " >&2\n" + params.Argv[scriptIndex]
		return &rewritten, nil
	}
	rewritten := *params
	rewritten.Argv = append([]string(nil), params.Argv...)
	rewritten.Argv[scriptIndex] = ". " + shellSingleQuote(path) + " || exit 1\n" +
		shellSnapshotLaunchEnvExports(params.Env) +
		params.Argv[scriptIndex]
	return &rewritten, nil
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

func (m *shellSnapshotManager) snapshotPath(ctx context.Context, params *ExecParams, shellType shell.ShellType, captureEnv map[string]string, credentialPairs []network.CredentialMaskPair) (string, error) {
	key, err := shellSnapshotKey(params, shellType)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	if path := m.cache[key]; path != "" {
		if _, statErr := os.Stat(path); statErr == nil {
			m.mu.Unlock()
			return path, nil
		}
		delete(m.cache, key)
	}
	m.mu.Unlock()

	ctx, captureSpan := startExecServerSpan(
		ctx,
		execServerCaptureSnapshotSpan,
		execServerProcessSpanAttributes(params.ProcessID, nil),
	)
	if captureSpan != nil {
		defer captureSpan.End()
	}
	captureScript, ok := shell.SnapshotCaptureScript(shellType, shell.SnapshotCaptureOptions{
		Startup: shell.SnapshotStartupInteractive, Declarations: true,
	})
	if !ok {
		return "", errors.New("unsupported snapshot shell")
	}
	cwd := shellSnapshotNativeCWD(params.CWD)
	env := shellSnapshotEnvironment(captureEnv)
	captureCtx, cancel := context.WithTimeout(ctx, shellSnapshotCaptureTimeout)
	defer cancel()
	output, err := m.capture(captureCtx, []string{params.ShellSnapshot.Shell.Path, "-lc", captureScript}, cwd, env)
	if err != nil {
		return "", err
	}
	captured := shell.ParseCapturedSnapshot(shellType, output)
	if captured == nil {
		return "", errors.New("invalid captured shell state")
	}
	dir := filepath.Join(os.TempDir(), "codex-exec-server-shell-snapshots")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, safeSnapshotFile(key)+"."+string(shellType))
	temp, err := os.CreateTemp(dir, ".snapshot-*")
	if err != nil {
		return "", err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return "", err
	}
	rendered := network.MaskSnapshotText(captured.RenderScript(nil), credentialPairs)
	if _, err := temp.WriteString(rendered); err != nil {
		_ = temp.Close()
		return "", err
	}
	if err := temp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return "", err
	}
	m.mu.Lock()
	m.cache[key] = path
	m.mu.Unlock()
	return path, nil
}

func (m *shellSnapshotManager) close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	paths := make([]string, 0, len(m.cache))
	for _, path := range m.cache {
		paths = append(paths, path)
	}
	m.cache = map[string]string{}
	m.mu.Unlock()
	for _, path := range paths {
		_ = os.Remove(path)
	}
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

func shellSnapshotKey(params *ExecParams, shellType shell.ShellType) (string, error) {
	envData, err := json.Marshal(params.Env)
	if err != nil {
		return "", err
	}
	policyData, err := json.Marshal(params.EnvPolicy)
	if err != nil {
		return "", err
	}
	return strings.Join([]string{
		params.ShellSnapshot.ScopeID,
		string(shellType),
		params.ShellSnapshot.Shell.Path,
		params.CWD,
		string(envData),
		string(policyData),
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

func safeSnapshotFile(value string) string {
	const alphabet = "0123456789abcdef"
	var builder strings.Builder
	builder.Grow(32)
	var sum uint64 = 1469598103934665603
	for index := 0; index < len(value); index++ {
		sum ^= uint64(value[index])
		sum *= 1099511628211
	}
	for index := 0; index < 16; index++ {
		builder.WriteByte(alphabet[(sum>>(index*4))&0xf])
	}
	return builder.String()
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
