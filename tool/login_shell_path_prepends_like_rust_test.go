package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"codex_go/execpolicy"
	"codex_go/execserver"
	"codex_go/sandbox"
	"codex_go/utils"

	"github.com/coder/websocket"
)

// serveLoginPathFakeExecutor starts an exec-server stand-in that reports
// `prependPathDirs` from `environment/info` and records the argv of the first
// `process/start`. Rust #49360/#49467 carry these directories from the executor
// (exec-server-protocol/src/protocol.rs) into the launch argv.
func serveLoginPathFakeExecutor(t *testing.T, dirs []string) (*httptest.Server, <-chan []string, <-chan error) {
	t.Helper()
	started := make(chan []string, 1)
	serverErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(w, request, &websocket.AcceptOptions{})
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		readRequest := func() (map[string]any, error) {
			_, data, readErr := conn.Read(context.Background())
			if readErr != nil {
				return nil, readErr
			}
			var value map[string]any
			if err := json.Unmarshal(data, &value); err != nil {
				return nil, err
			}
			return value, nil
		}
		writeJSON := func(value any) error {
			data, marshalErr := json.Marshal(value)
			if marshalErr != nil {
				return marshalErr
			}
			return conn.Write(context.Background(), websocket.MessageText, data)
		}
		initialize, err := readRequest()
		if err != nil {
			serverErr <- err
			return
		}
		if err := writeJSON(map[string]any{"id": initialize["id"], "result": map[string]any{"sessionId": "login-path-session"}}); err != nil {
			serverErr <- err
			return
		}
		for {
			message, err := readRequest()
			if err != nil {
				serverErr <- err
				return
			}
			switch message["method"] {
			case execserver.MethodInitialized:
			case execserver.MethodEnvironmentInfo:
				result := map[string]any{
					"shell": map[string]any{"name": "bash", "path": "/bin/bash"},
					"cwd":   "file:///workspace",
				}
				if len(dirs) > 0 {
					result["prependPathDirs"] = dirs
				}
				if err := writeJSON(map[string]any{"id": message["id"], "result": result}); err != nil {
					serverErr <- err
					return
				}
			case execserver.MethodProcessStart:
				params, _ := message["params"].(map[string]any)
				rawArgv, _ := params["argv"].([]any)
				argv := make([]string, 0, len(rawArgv))
				for _, item := range rawArgv {
					value, _ := item.(string)
					argv = append(argv, value)
				}
				select {
				case started <- argv:
				default:
				}
				processID, _ := params["processId"].(string)
				if err := writeJSON(map[string]any{"id": message["id"], "result": map[string]any{"processId": processID}}); err != nil {
					serverErr <- err
				}
				return
			default:
				serverErr <- fmt.Errorf("unexpected exec-server request %#v", message)
				return
			}
		}
	}))
	return server, started, serverErr
}

// TestUnifiedExecRestoresExecutorPathDirsForLoginShellLikeRust drives the real
// remote-exec wiring (tool/unified_exec.go execRemote -> exec-server
// process/start) and pins Rust #49467's gate: a POSIX login-shell launch is
// re-derived with the executor-reported directories only when the
// `login_shell_package_path` feature is on, the policy does not set PATH itself,
// and the executor reports usable directories.
func TestUnifiedExecRestoresExecutorPathDirsForLoginShellLikeRust(t *testing.T) {
	first := filepath.Join(t.TempDir(), "codex-path")
	second := filepath.Join(t.TempDir(), "extra-tools")
	firstURI, err := utils.FromHostNativePath(first)
	if err != nil {
		t.Fatalf("FromHostNativePath(%q) error = %v", first, err)
	}
	secondURI, err := utils.FromHostNativePath(second)
	if err != nil {
		t.Fatalf("FromHostNativePath(%q) error = %v", second, err)
	}
	dirs := []string{firstURI.String(), secondURI.String()}
	hookCommand := "sh -c 'command -v rg'"
	loginCommand := []string{"/bin/sh", "-lc", hookCommand}

	explicitPATH := &execpolicy.EnvPolicy{Set: map[string]string{"PATH": "/user/configured/bin"}}
	cases := []struct {
		name          string
		feature       bool
		command       []string
		envPolicy     *execpolicy.EnvPolicy
		dirs          []string
		wantRewritten bool
	}{
		{name: "restores executor directories", feature: true, command: loginCommand, dirs: dirs, wantRewritten: true},
		{name: "feature disabled", feature: false, command: loginCommand, dirs: dirs},
		{name: "explicit PATH override", feature: true, command: loginCommand, envPolicy: explicitPATH, dirs: dirs},
		{name: "executor reports no directories", feature: true, command: loginCommand},
		{name: "non-login shell", feature: true, command: []string{"/bin/sh", "-c", hookCommand}, dirs: dirs},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server, started, serverErr := serveLoginPathFakeExecutor(t, testCase.dirs)
			defer server.Close()

			manager := NewUnifiedExecManagerWithOptions(1, unifiedExecMinEmptyPollYieldMS)
			defer manager.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go func() {
				_, _ = manager.Exec(ctx, &ShellRequest{
					Command:                  append([]string(nil), testCase.command...),
					HookCommand:              hookCommand,
					CWD:                      t.TempDir(),
					UnifiedExecRemoteURL:     "ws" + strings.TrimPrefix(server.URL, "http"),
					UnifiedExecEnvironmentID: "remote",
					LoginShellPackagePath:    testCase.feature,
					EnvPolicy:                testCase.envPolicy,
				}, "login-path-call")
			}()

			var argv []string
			select {
			case argv = <-started:
			case err := <-serverErr:
				if err != nil {
					t.Fatalf("exec-server error = %v", err)
				}
				t.Fatal("exec-server did not receive process/start")
			case <-time.After(5 * time.Second):
				t.Fatal("timed out waiting for process/start")
			}

			if !testCase.wantRewritten {
				if !reflect.DeepEqual(argv, testCase.command) {
					t.Fatalf("process/start argv = %#v, want the requested command %#v", argv, testCase.command)
				}
				return
			}
			if len(argv) != 3 || argv[0] != "/bin/sh" || argv[1] != "-lc" {
				t.Fatalf("process/start argv = %#v, want the login shell with the rewritten script", argv)
			}
			script := argv[2]
			if !strings.HasSuffix(script, "; "+hookCommand) {
				t.Fatalf("script = %q, want the requested command last", script)
			}
			if strings.Count(script, "\n") != 0 {
				t.Fatalf("script = %q, want a single input line", script)
			}
			if !strings.Contains(script, "export PATH="+first+`${PATH:+:"$PATH"}`) ||
				!strings.Contains(script, "export PATH="+second+`${PATH:+:"$PATH"}`) {
				t.Fatalf("script = %q, want both executor directories restored", script)
			}
			// Rust prepends backwards so the newly added directories keep the
			// executor's priority order.
			if strings.Index(script, second) > strings.Index(script, first) {
				t.Fatalf("script = %q, want %q before %q", script, second, first)
			}
		})
	}
}

// TestDeriveExecArgsWithPathPrependsRestoresPATHLikeRust mirrors Rust
// shell_tests.rs::codex_path_setup_preserves_shell_script_line_numbers (#49467):
// running the derived arguments inside a real POSIX login shell leaves the
// executor's directories at the front of PATH, keeps existing entries, and does
// not change the script's diagnostics or line numbers.
func TestDeriveExecArgsWithPathPrependsRestoresPATHLikeRust(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX login-shell PATH setup")
	}
	shellPath, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("no POSIX shell: %v", err)
	}
	first := filepath.Join(t.TempDir(), "codex-path")
	// Quoting coverage: a directory with a space and a single quote must survive.
	second := filepath.Join(t.TempDir(), "codex 'quoted' dir")
	for _, dir := range []string{first, second} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", dir, err)
		}
	}
	firstURI, err := utils.FromHostNativePath(first)
	if err != nil {
		t.Fatalf("FromHostNativePath(%q) error = %v", first, err)
	}
	secondURI, err := utils.FromHostNativePath(second)
	if err != nil {
		t.Fatalf("FromHostNativePath(%q) error = %v", second, err)
	}
	shell := &Shell{Type: ShellBash, Path: shellPath}

	command := `printf '%s' "$PATH"`
	argv, ok := shell.DeriveExecArgsWithPathPrepends(command, []string{firstURI.String(), secondURI.String()}, true)
	if !ok {
		t.Fatal("DeriveExecArgsWithPathPrepends() ok = false, want usable POSIX login arguments")
	}
	if len(argv) != 3 || argv[0] != shellPath || argv[1] != "-lc" || !strings.HasSuffix(argv[2], "; "+command) {
		t.Fatalf("argv = %#v", argv)
	}
	stdout, stderr, _ := runLoginShellArgv(t, argv)
	if got := strings.Split(stdout, ":"); len(got) < 2 || got[0] != first || got[1] != second {
		t.Fatalf("PATH = %q (stderr %q), want %q before %q", stdout, stderr, first, second)
	}
	// An entry already on PATH keeps its position instead of being duplicated.
	argv, ok = shell.DeriveExecArgsWithPathPrepends(command, []string{firstURI.String()}, true)
	if !ok {
		t.Fatal("DeriveExecArgsWithPathPrepends() ok = false for a single directory")
	}
	stdout, _, _ = runLoginShellArgv(t, argv, "PATH="+first+":/usr/bin:/bin")
	if got := strings.Split(stdout, ":"); len(got) == 0 || got[0] != first || strings.Count(stdout, first) != 1 {
		t.Fatalf("PATH = %q, want the existing %q entry kept once", stdout, first)
	}

	// Diagnostics and line numbers still refer to the original script.
	multiline := ":\nif then"
	original := shell.DeriveExecArgs(multiline, true)
	wrapped, ok := shell.DeriveExecArgsWithPathPrepends(multiline, []string{firstURI.String()}, true)
	if !ok {
		t.Fatal("DeriveExecArgsWithPathPrepends() ok = false for a multiline script")
	}
	if strings.Count(wrapped[2], "\n") != strings.Count(multiline, "\n") {
		t.Fatalf("script = %q, want %d input lines", wrapped[2], strings.Count(multiline, "\n")+1)
	}
	_, wantStderr, _ := runLoginShellArgv(t, original)
	_, gotStderr, _ := runLoginShellArgv(t, wrapped)
	if gotStderr != wantStderr {
		t.Fatalf("stderr = %q, want the unwrapped script's diagnostics %q", gotStderr, wantStderr)
	}

	// Directories the setup cannot express keep the original arguments.
	if _, ok := shell.DeriveExecArgsWithPathPrepends(command, []string{"file:///C:/tools/bin"}, true); ok {
		t.Fatal("a Windows directory must not be prepended inside a POSIX login shell")
	}
	if _, ok := shell.DeriveExecArgsWithPathPrepends(command, []string{"file:///opt/a:b"}, true); ok {
		t.Fatal("a directory containing a colon must not be prepended")
	}
	if _, ok := shell.DeriveExecArgsWithPathPrepends(command, nil, true); ok {
		t.Fatal("no directories must keep the original arguments")
	}
	if _, ok := shell.DeriveExecArgsWithPathPrepends(command, []string{firstURI.String()}, false); ok {
		t.Fatal("a non-login shell must keep the original arguments")
	}
	if _, ok := (&Shell{Type: ShellPowerShell, Path: "pwsh"}).DeriveExecArgsWithPathPrepends(command, []string{firstURI.String()}, true); ok {
		t.Fatal("a non-POSIX shell must keep the original arguments")
	}
}

// runLoginShellArgv runs a derived launch in an isolated login environment so
// the host's login files cannot influence PATH.
func runLoginShellArgv(t *testing.T, argv []string, extraEnv ...string) (string, string, error) {
	t.Helper()
	home := t.TempDir()
	command := exec.Command(argv[0], argv[1:]...)
	env := make([]string, 0, len(os.Environ())+3)
	for _, entry := range os.Environ() {
		switch {
		case strings.HasPrefix(entry, "HOME="),
			strings.HasPrefix(entry, "ZDOTDIR="),
			strings.HasPrefix(entry, "BASH_ENV="),
			strings.HasPrefix(entry, "ENV="):
			continue
		}
		env = append(env, entry)
	}
	env = append(env, "HOME="+home, "ZDOTDIR="+home)
	env = append(env, extraEnv...)
	command.Env = env
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return stdout.String(), stderr.String(), err
}

// TestBuildShellRequestCarriesLoginShellPackagePathLikeRust pins the plumbing
// between a turn's shell validation options and the launch request the remote
// exec gate reads (Rust #49467 keeps this flag on the launch).
func TestBuildShellRequestCarriesLoginShellPackagePathLikeRust(t *testing.T) {
	shell := &Shell{Type: ShellBash, Path: "/bin/bash"}
	request, err := BuildShellRequest(&ExecCommandArgs{Cmd: "echo hi"}, shell, ShellValidationOptions{
		AllowLoginShell:       true,
		LoginShellPackagePath: true,
		CWD:                   t.TempDir(),
	})
	if err != nil {
		t.Fatalf("BuildShellRequest() error = %v", err)
	}
	if !request.LoginShellPackagePath {
		t.Fatal("ShellRequest.LoginShellPackagePath = false, want the validation option")
	}
	if request.HookCommand != "echo hi" {
		t.Fatalf("HookCommand = %q, want the raw script", request.HookCommand)
	}
	defaulted, err := BuildShellRequest(&ExecCommandArgs{Cmd: "echo hi"}, shell, ShellValidationOptions{
		AllowLoginShell: true,
		CWD:             t.TempDir(),
	})
	if err != nil {
		t.Fatalf("BuildShellRequest() error = %v", err)
	}
	if defaulted.LoginShellPackagePath {
		t.Fatal("ShellRequest.LoginShellPackagePath = true without the feature option")
	}
}

// TestLocalShellLaunchRestoresExecutorPathDirsLikeRust drives the local launch
// path (ShellExecutor -> ShellRequest.Command -> local runner) and pins Rust
// #49467's local half: the same feature gate, login-mode check and explicit-PATH
// check rewrite the launch, and the restore actually puts the executor's
// directories on PATH for the model's script.
func TestLocalShellLaunchRestoresExecutorPathDirsLikeRust(t *testing.T) {
	first := filepath.Join(t.TempDir(), "codex-path")
	second := filepath.Join(t.TempDir(), "extra-tools")
	for _, dir := range []string{first, second} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", dir, err)
		}
	}
	firstURI, err := utils.FromHostNativePath(first)
	if err != nil {
		t.Fatalf("FromHostNativePath(%q) error = %v", first, err)
	}
	secondURI, err := utils.FromHostNativePath(second)
	if err != nil {
		t.Fatalf("FromHostNativePath(%q) error = %v", second, err)
	}
	dirs := []string{firstURI.String(), secondURI.String()}
	previous := localLaunchPrependPathDirs
	defer func() { localLaunchPrependPathDirs = previous }()
	localLaunchPrependPathDirs = func() []string { return dirs }

	// End to end through the real local runner: the model's script sees the
	// executor's directories at the front of PATH.
	if runtime.GOOS != "windows" {
		if _, err := exec.LookPath("sh"); err == nil {
			t.Run("end to end through the local runner", func(t *testing.T) {
				localLaunchPrependPathDirs = func() []string { return dirs }
				executor := NewShellExecutor(&ShellExecutorOptions{
					Runner: NewLocalShellRunner(),
					Shell:  &Shell{Type: ShellBash, Path: "/bin/sh"},
					Validation: ShellValidationOptions{
						ApprovalPolicy:        sandbox.ApprovalOnRequest,
						AllowLoginShell:       true,
						CWD:                   t.TempDir(),
						DefaultTimeoutMS:      5000,
						LoginShellPackagePath: true,
					},
				})
				arguments, err := json.Marshal(map[string]any{"cmd": `printf '%s' "$PATH"`})
				if err != nil {
					t.Fatalf("Marshal(arguments) error = %v", err)
				}
				output, err := executor.Execute(context.Background(), &Invocation{
					CallID:   "call-local-login-path",
					ToolName: PlainName(DefaultExecCommandToolName),
					Payload:  Payload{Kind: PayloadFunction, Arguments: string(arguments)},
				})
				if err != nil {
					t.Fatalf("Execute() error = %v", err)
				}
				parts := strings.Split(output.Data["hook_response"].(string), ":")
				if len(parts) < 2 || parts[0] != first || parts[1] != second {
					t.Fatalf("PATH = %q, want %q before %q", output.Data["hook_response"], first, second)
				}
			})
		}
	}

	cases := []struct {
		name          string
		feature       bool
		loginFalse    bool
		noDirs        bool
		envPolicy     map[string]any
		wantRewritten bool
	}{
		{name: "restores executor directories", feature: true, wantRewritten: true},
		{name: "feature disabled"},
		{name: "explicit PATH override", feature: true, envPolicy: map[string]any{"set": map[string]any{"PATH": "/user/configured/bin"}}},
		{name: "non-login shell", feature: true, loginFalse: true},
		{name: "executor reports no directories", feature: true, noDirs: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if testCase.noDirs {
				localLaunchPrependPathDirs = func() []string { return nil }
			} else {
				localLaunchPrependPathDirs = func() []string { return dirs }
			}
			runner := &fakeShellRunner{result: &ShellResult{}}
			executor := NewShellExecutor(&ShellExecutorOptions{
				Runner: runner,
				Shell:  &Shell{Type: ShellBash, Path: "/bin/sh"},
				Validation: ShellValidationOptions{
					ApprovalPolicy:        sandbox.ApprovalOnRequest,
					AllowLoginShell:       true,
					CWD:                   t.TempDir(),
					DefaultTimeoutMS:      5000,
					LoginShellPackagePath: testCase.feature,
				},
				ShellEnvironmentPolicy: testCase.envPolicy,
			})
			arguments := `{"cmd":"echo hi"}`
			if testCase.loginFalse {
				arguments = `{"cmd":"echo hi","login":false}`
			}
			if _, err := executor.Execute(context.Background(), &Invocation{
				CallID:   "call-local-login-path",
				ToolName: PlainName(DefaultExecCommandToolName),
				Payload:  Payload{Kind: PayloadFunction, Arguments: arguments},
			}); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if runner.request == nil {
				t.Fatal("the shell runner did not see the launch request")
			}
			command := runner.request.Command
			if !testCase.wantRewritten {
				want := []string{"/bin/sh", "-lc", "echo hi"}
				if testCase.loginFalse {
					want = []string{"/bin/sh", "-c", "echo hi"}
				}
				if !reflect.DeepEqual(command, want) {
					t.Fatalf("launch command = %#v, want the requested command %#v", command, want)
				}
				return
			}
			if len(command) != 3 || command[0] != "/bin/sh" || command[1] != "-lc" {
				t.Fatalf("launch command = %#v, want the login shell with the rewritten script", command)
			}
			if !strings.HasSuffix(command[2], "; echo hi") || strings.Count(command[2], "\n") != 0 {
				t.Fatalf("script = %q, want the requested command last on one line", command[2])
			}
			if !strings.Contains(command[2], "export PATH="+first+`${PATH:+:"$PATH"}`) ||
				!strings.Contains(command[2], "export PATH="+second+`${PATH:+:"$PATH"}`) {
				t.Fatalf("script = %q, want both executor directories restored", command[2])
			}
		})
	}
}
