package appserver

import (
	"context"
	"encoding/base64"
	"runtime"
	"strings"
	"testing"
	"time"

	"codex_go/execserver"
	"codex_go/tool"
)

// TestEnvironmentHookPushedOutputCompletesWithoutReadsLikeRust covers Rust
// #58996: a finished hook can deliver its ordered output and exit status from
// pushed process events alone, so a queued read RPC cannot delay the decision.
func TestEnvironmentHookPushedOutputCompletesWithoutReadsLikeRust(t *testing.T) {
	serverURL, stop := startHookEnvironmentExecServer(t)
	defer stop()
	client, err := execserver.DialClient(context.Background(), serverURL, "hook-pushed-output-test")
	if err != nil {
		t.Fatalf("DialClient() error = %v", err)
	}
	defer client.Close()

	shell, command := hookTestShellAndCommand()
	script, err := environmentHookScript(shell.Type, command)
	if err != nil {
		t.Fatalf("environmentHookScript() error = %v", err)
	}
	processID := "hook-pushed"
	events, err := client.SubscribeProcessEvents(processID)
	if err != nil {
		t.Fatalf("SubscribeProcessEvents() error = %v", err)
	}
	defer events.Close()
	if _, err := client.Start(context.Background(), &execserver.ExecParams{
		ProcessID: processID,
		Argv:      shell.DeriveExecArgs(script, false),
		CWD:       environmentExecCWD(t.TempDir()),
		EnvPolicy: &execserver.ExecEnvPolicy{Inherit: "all", IgnoreDefaultExcludes: true},
		PipeStdin: true,
	}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if _, err := client.Write(context.Background(), &execserver.WriteParams{
		ProcessID: processID,
		Chunk:     base64.StdEncoding.EncodeToString([]byte("{\"hook\":\"input\"}\n")),
		WriteID:   "hook-pushed-write",
	}); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	output, ok := pushedHookEnvironmentOutput(ctx, events, processID)
	if !ok || output == nil {
		t.Fatal("pushedHookEnvironmentOutput() did not complete from events alone")
	}
	if output.ExitCode != 0 || !strings.Contains(output.Stdout, "hook-pushed-allow") {
		t.Fatalf("pushed output = %#v", output)
	}
}

// TestEnvironmentHookExecutorDeliversDecisionLikeRust runs the real executor
// against an in-process exec-server and checks that the decision is returned
// with the hook's exit status.
func TestEnvironmentHookExecutorDeliversDecisionLikeRust(t *testing.T) {
	serverURL, stop := startHookEnvironmentExecServer(t)
	defer stop()
	shell, command := hookTestShellAndCommand()
	target := tool.UnifiedExecEnvironment{
		ID:            "env-1",
		CWD:           t.TempDir(),
		PlatformOS:    runtime.GOOS,
		Shell:         shell,
		ExecServerURL: serverURL,
	}
	executor := newAppServerHookEnvironmentExecutor(map[string]tool.UnifiedExecEnvironment{"env-1": target})
	if executor == nil {
		t.Fatal("hook environment executor was not created")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := executor.Execute(ctx, "env-1", command, nil, "{\"hook\":\"input\"}", 20*time.Second)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output.ExitCode != 0 || !strings.Contains(output.Stdout, "hook-pushed-allow") {
		t.Fatalf("hook output = %#v", output)
	}
}

func hookTestShellAndCommand() (*tool.Shell, string) {
	if runtime.GOOS == "windows" {
		return &tool.Shell{Type: tool.ShellPowerShell, Path: "powershell.exe"}, "ForEach-Object { 'hook-pushed-allow' }"
	}
	return &tool.Shell{Type: tool.ShellBash, Path: "/bin/bash"}, "echo hook-pushed-allow"
}

func startHookEnvironmentExecServer(t *testing.T) (string, func()) {
	t.Helper()
	serverCtx, cancel := context.WithCancel(context.Background())
	urlCh := make(chan string, 1)
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- execserver.NewServer().ServeTransport(serverCtx, "ws://127.0.0.1:0", nil, &appServerExecServerURLWriter{url: urlCh})
	}()
	var serverURL string
	select {
	case serverURL = <-urlCh:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("exec-server URL was not reported")
	}
	return serverURL, func() {
		cancel()
		<-serverDone
	}
}
