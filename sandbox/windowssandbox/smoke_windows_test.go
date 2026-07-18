//go:build windows

package windowssandbox

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	coresandbox "codex_go/sandbox"
)

func TestWindowsSandboxRestrictedSmoke(t *testing.T) {
	if !windowsSandboxSmokeEnabled("restricted") {
		t.Skip("set CODEX_WINDOWS_SANDBOX_SMOKE=restricted or all to run")
	}
	cwd := windowsSandboxSmokeTempDir(t, "restricted-workspace")
	tmp := filepath.Join(cwd, "tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		t.Fatalf("MkdirAll(tmp) error = %v", err)
	}
	userHome := filepath.Join(cwd, "user")
	if err := os.MkdirAll(userHome, 0o700); err != nil {
		t.Fatalf("MkdirAll(userHome) error = %v", err)
	}
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	codexHome := strings.TrimSpace(os.Getenv("CODEX_WINDOWS_SANDBOX_SMOKE_CODEX_HOME"))
	if codexHome == "" {
		codexHome = windowsSandboxSmokeTempDir(t, "restricted-codex-home")
	}
	profile := coresandbox.WorkspaceWritePermissionProfile()
	env := windowsSandboxSmokeEnv(tmp)
	env["HOME"] = userHome
	env["USERPROFILE"] = userHome
	result, err := RunWindowsSandboxCapture(&CaptureRequest{
		PermissionProfile: &profile,
		WorkspaceRoots:    []string{cwd},
		CodexHome:         codexHome,
		CWD:               cwd,
		Env:               env,
		Command:           windowsSandboxSmokeCommand("restricted-ok"),
		TimeoutMS:         int64Ptr(15000),
	})
	if err != nil {
		if IsUnsupported(err) {
			t.Skipf("Windows sandbox restricted backend unsupported on this host: %v", err)
		}
		t.Fatalf("RunWindowsSandboxCapture() error = %v", err)
	}
	assertWindowsSandboxSmokeResult(t, result, "restricted-ok", cwd, codexHome)
	if windowsSandboxSmokeCommandWritesFile() {
		assertWindowsSandboxSmokeFile(t, filepath.Join(cwd, "smoke.txt"), "restricted")
	}
}

func TestWindowsSandboxElevatedSmoke(t *testing.T) {
	if !windowsSandboxSmokeEnabled("elevated") {
		t.Skip("set CODEX_WINDOWS_SANDBOX_SMOKE=elevated or all to run")
	}
	cwd := windowsSandboxSmokeTempDir(t, "elevated-workspace")
	tmp := filepath.Join(cwd, "tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		t.Fatalf("MkdirAll(tmp) error = %v", err)
	}
	exe := strings.TrimSpace(os.Getenv("CODEX_WINDOWS_SANDBOX_SMOKE_CODEX_EXE"))
	if exe == "" {
		t.Skip("set CODEX_WINDOWS_SANDBOX_SMOKE_CODEX_EXE to a built codex.exe for elevated smoke")
	}
	ensureSmokeCommandRunnerAlias(t, exe)
	codexHome := strings.TrimSpace(os.Getenv("CODEX_WINDOWS_SANDBOX_SMOKE_CODEX_HOME"))
	if codexHome == "" {
		codexHome = windowsSandboxSmokeDefaultCodexHome()
	}
	profile := coresandbox.WorkspaceWritePermissionProfile()
	args, err := CreateWindowsSandboxCommandArgsForPermissionProfile(WindowsSandboxCommandArgsRequest{
		PermissionProfile:   &profile,
		WorkspaceRoots:      []string{cwd},
		CodexHome:           codexHome,
		CommandCWD:          cwd,
		Env:                 windowsSandboxSmokeEnv(tmp),
		Command:             windowsSandboxSmokeCommand("elevated-ok"),
		WindowsSandboxLevel: WindowsSandboxLevelElevated,
	})
	if err != nil {
		t.Fatalf("CreateWindowsSandboxCommandArgsForPermissionProfile() error = %v", err)
	}
	result := runWindowsSandboxSmokeExe(t, exe, args)
	assertWindowsSandboxSmokeResult(t, result, "elevated-ok", cwd, codexHome)
	if windowsSandboxSmokeCommandWritesFile() {
		assertWindowsSandboxSmokeFile(t, filepath.Join(cwd, "smoke.txt"), "elevated")
	}
}

func windowsSandboxSmokeDefaultCodexHome() string {
	if value := strings.TrimSpace(os.Getenv("CODEX_HOME")); value != "" {
		return value
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".codex"
	}
	return filepath.Join(home, ".codex")
}

func windowsSandboxSmokeEnabled(kind string) bool {
	raw := strings.ToLower(os.Getenv("CODEX_WINDOWS_SANDBOX_SMOKE"))
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n'
	}) {
		if part == "all" || part == kind {
			return true
		}
	}
	return false
}

func windowsSandboxSmokeTempDir(t *testing.T, name string) string {
	t.Helper()
	if os.Getenv("CODEX_WINDOWS_SANDBOX_SMOKE_KEEP") == "" {
		return t.TempDir()
	}
	dir, err := os.MkdirTemp("", name+"-*")
	if err != nil {
		t.Fatalf("MkdirTemp(%s) error = %v", name, err)
	}
	t.Logf("keeping smoke temp dir %s", dir)
	return dir
}

func windowsSandboxSmokeEnv(tmp string) map[string]string {
	env := map[string]string{
		"ComSpec": os.Getenv("ComSpec"),
		"PATH":    os.Getenv("PATH"),
		"TEMP":    tmp,
		"TMP":     tmp,
	}
	for _, key := range []string{"SystemRoot", "WINDIR", "ProgramData", "ProgramFiles", "ProgramFiles(x86)"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			env[key] = value
		}
	}
	return env
}

func windowsSandboxSmokeCommand(marker string) []string {
	comspec := strings.TrimSpace(os.Getenv("ComSpec"))
	if comspec == "" {
		comspec = filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	}
	if override := strings.TrimSpace(os.Getenv("CODEX_WINDOWS_SANDBOX_SMOKE_COMMAND")); override != "" {
		override = strings.ReplaceAll(override, "{marker}", marker)
		return []string{comspec, "/d", "/c", override}
	}
	return []string{comspec, "/d", "/c", "echo " + marker + ">smoke.txt && type smoke.txt"}
}

func windowsSandboxSmokeCommandWritesFile() bool {
	override := strings.TrimSpace(os.Getenv("CODEX_WINDOWS_SANDBOX_SMOKE_COMMAND"))
	return override == "" || strings.Contains(strings.ToLower(override), "smoke.txt")
}

func assertWindowsSandboxSmokeResult(t *testing.T, result *CaptureResult, marker string, cwd string, codexHome string) {
	t.Helper()
	if result == nil {
		t.Fatalf("smoke result is nil")
	}
	if result.ExitCode != 0 {
		t.Fatalf("smoke exit code = %d, stderr=%q stdout=%q cwd=%s codexHome=%s", result.ExitCode, result.Stderr, result.Stdout, cwd, codexHome)
	}
	if !strings.Contains(string(result.Stdout), marker) {
		t.Fatalf("smoke stdout = %q, want marker %q", result.Stdout, marker)
	}
}

func assertWindowsSandboxSmokeFile(t *testing.T, path string, kind string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("%s smoke did not create workspace file: %v", kind, err)
	}
}

func runWindowsSandboxSmokeExe(t *testing.T, exe string, args []string) *CaptureResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), windowsSandboxSmokeExeTimeout())
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("codex smoke timed out after %s, stderr=%q stdout=%q", windowsSandboxSmokeExeTimeout(), stderr.String(), stdout.String())
	}
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	if err != nil && cmd.ProcessState == nil {
		t.Fatalf("codex smoke launch error = %v, stderr=%q", err, stderr.String())
	}
	return &CaptureResult{
		ExitCode: exitCode,
		Stdout:   stdout.Bytes(),
		Stderr:   stderr.Bytes(),
	}
}

func windowsSandboxSmokeExeTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv("CODEX_WINDOWS_SANDBOX_SMOKE_EXE_TIMEOUT_MS"))
	if raw == "" {
		return 60 * time.Second
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return 60 * time.Second
	}
	return time.Duration(value) * time.Millisecond
}

func ensureSmokeCommandRunnerAlias(t *testing.T, exe string) {
	t.Helper()
	source := filepath.Clean(exe)
	destination := filepath.Join(filepath.Dir(source), "codex-command-runner.exe")
	if strings.EqualFold(source, destination) {
		return
	}
	if sameSizeAndModTime(source, destination) {
		return
	}
	src, err := os.Open(source)
	if err != nil {
		t.Fatalf("open smoke exe source: %v", err)
	}
	defer src.Close()
	dst, err := os.Create(destination)
	if err != nil {
		t.Fatalf("create smoke command-runner alias: %v", err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		t.Fatalf("copy smoke command-runner alias: %v", err)
	}
	if err := dst.Close(); err != nil {
		t.Fatalf("close smoke command-runner alias: %v", err)
	}
	if sourceInfo, err := os.Stat(source); err == nil {
		_ = os.Chtimes(destination, sourceInfo.ModTime(), sourceInfo.ModTime())
	}
}

func sameSizeAndModTime(left string, right string) bool {
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	return leftErr == nil &&
		rightErr == nil &&
		leftInfo.Size() == rightInfo.Size() &&
		leftInfo.ModTime().Equal(rightInfo.ModTime())
}

func int64Ptr(value int64) *int64 {
	return &value
}
