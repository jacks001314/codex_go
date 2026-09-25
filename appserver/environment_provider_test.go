package appserver

import (
	"context"
	"os"
	"strings"
	"testing"

	execserverclient "codex_go/execserver"
)

const appserverStdioEnvironmentHelperEnv = "CODEX_GO_APPSERVER_STDIO_ENV_HELPER"

// TestAppServerStdioEnvironmentHelperProcess serves the exec-server protocol
// over stdio so the app-server's stdio environment path can be exercised.
func TestAppServerStdioEnvironmentHelperProcess(t *testing.T) {
	if os.Getenv(appserverStdioEnvironmentHelperEnv) != "1" {
		return
	}
	if err := execserverclient.NewServer().Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

// Rust parity: codex-exec-server's environment_provider.rs consumers: the
// host pre-registers the provider's environments, so remote entries carry their
// headers and stdio entries their command.
func TestEnvironmentManagerAppliesProviderSnapshotLikeRust(t *testing.T) {
	token := "private-token"
	snapshot := execserverclient.EnvironmentProviderSnapshot{
		IncludeLocal: true,
		Default:      execserverclient.EnvironmentDefault{Kind: execserverclient.EnvironmentDefaultID, ID: "local"},
		Environments: []execserverclient.NamedEnvironment{
			{
				ID: "devbox",
				Transport: execserverclient.EnvironmentTransport{
					Kind:           execserverclient.EnvironmentTransportWebSocket,
					WebSocketURL:   "wss://executor.example/exec",
					HTTPHeaders:    map[string][]string{"Authorization": {"Bearer " + token}},
					ConnectTimeout: 12 * 1e9,
				},
			},
			{
				ID: "ssh-dev",
				Transport: execserverclient.EnvironmentTransport{
					Kind:    execserverclient.EnvironmentTransportStdio,
					Command: &execserverclient.StdioExecServerCommand{Program: "ssh", Args: []string{"dev"}},
				},
			},
		},
	}
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "/workspace")
	if err := manager.ApplyProviderSnapshot(snapshot); err != nil {
		t.Fatalf("ApplyProviderSnapshot() error = %v", err)
	}
	devbox, ok := manager.Record("devbox")
	if !ok || devbox == nil {
		t.Fatal("devbox environment was not registered")
	}
	if devbox.ExecServerURL != "wss://executor.example/exec" ||
		devbox.ExecServerHeaders.Get("Authorization") != "Bearer "+token {
		t.Fatalf("devbox record = %#v", devbox)
	}
	if devbox.ConnectTimeoutMS == nil || *devbox.ConnectTimeoutMS != 12000 {
		t.Fatalf("devbox connect timeout = %#v, want 12000ms", devbox.ConnectTimeoutMS)
	}
	ssh, ok := manager.Record("ssh-dev")
	if !ok || ssh == nil || ssh.StdioCommand == nil || ssh.StdioCommand.Program != "ssh" {
		t.Fatalf("ssh-dev record = %#v", ssh)
	}
	if ssh.ExecServerURL != "" {
		t.Fatalf("ssh-dev URL = %q, want the stdio transport only", ssh.ExecServerURL)
	}
	// The local environment stays implicit and is never a record.
	if _, ok := manager.Record(execserverclient.LocalEnvironmentID); ok {
		t.Fatal("the local environment must not be registered as a record")
	}
}

// TestEnvironmentInfoServesStdioEnvironmentLikeRust drives a stdio environment
// end to end: the record's command is spawned and environment/info answers from
// the child process.
func TestEnvironmentInfoServesStdioEnvironmentLikeRust(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "/workspace")
	if err := manager.ApplyProviderSnapshot(execserverclient.EnvironmentProviderSnapshot{
		IncludeLocal: true,
		Environments: []execserverclient.NamedEnvironment{{
			ID: "ssh-dev",
			Transport: execserverclient.EnvironmentTransport{
				Kind: execserverclient.EnvironmentTransportStdio,
				Command: &execserverclient.StdioExecServerCommand{
					Program: executable,
					Args:    []string{"-test.run=^TestAppServerStdioEnvironmentHelperProcess$"},
					Env:     map[string]string{appserverStdioEnvironmentHelperEnv: "1"},
				},
			},
		}},
	}); err != nil {
		t.Fatalf("ApplyProviderSnapshot() error = %v", err)
	}
	info, err := manager.InfoContext(context.Background(), &EnvironmentInfoParams{EnvironmentID: "ssh-dev"})
	if err != nil {
		t.Fatalf("InfoContext(stdio) error = %v", err)
	}
	if strings.TrimSpace(info.Shell.Name) == "" || strings.TrimSpace(info.Shell.Path) == "" {
		t.Fatalf("environment info = %#v", info)
	}
	status, err := manager.StatusContext(context.Background(), &EnvironmentStatusParams{EnvironmentID: "ssh-dev"})
	if err != nil {
		t.Fatalf("StatusContext(stdio) error = %v", err)
	}
	if status.Status != EnvironmentStatusReady {
		t.Fatalf("status = %q, want ready", status.Status)
	}
}
