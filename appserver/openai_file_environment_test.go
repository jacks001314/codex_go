package appserver

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/execserver"
	"codex_go/sandbox"
	"codex_go/turn"
	"codex_go/utils"
)

func TestEnvironmentOpenAIFileSystemReadsThroughExecServer(t *testing.T) {
	serverCtx, cancelServer := context.WithCancel(context.Background())
	urlCh := make(chan string, 1)
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- execserver.NewServer().ServeTransport(serverCtx, "ws://127.0.0.1:0", nil, &appServerExecServerURLWriter{url: urlCh})
	}()
	var serverURL string
	select {
	case serverURL = <-urlCh:
	case <-time.After(3 * time.Second):
		cancelServer()
		t.Fatal("exec-server URL was not reported")
	}
	defer func() {
		cancelServer()
		select {
		case err := <-serverDone:
			if err != nil {
				t.Errorf("exec-server shutdown error = %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("exec-server did not stop")
		}
	}()

	path := t.TempDir() + string(os.PathSeparator) + "report.txt"
	if err := os.WriteFile(path, []byte("remote report"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	pathURI, err := utils.FromHostNativePath(path)
	if err != nil {
		t.Fatalf("FromHostNativePath() error = %v", err)
	}
	fileSystem := &environmentOpenAIFileSystem{record: EnvironmentRecord{ExecServerURL: serverURL}, requiresSandbox: true}
	metadata, err := fileSystem.Metadata(context.Background(), pathURI.String())
	if err != nil || metadata == nil || !metadata.IsFile || metadata.Size != int64(len("remote report")) {
		t.Fatalf("Metadata() = %#v, %v", metadata, err)
	}
	reader, err := fileSystem.Open(context.Background(), pathURI.String())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	contents, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil || string(contents) != "remote report" {
		t.Fatalf("ReadAll() = %q, readErr=%v closeErr=%v", contents, readErr, closeErr)
	}
}

func TestPrimaryTurnOpenAIFileSystemUsesFirstRemoteEnvironment(t *testing.T) {
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "/workspace")
	if _, err := manager.Add(&EnvironmentAddParams{EnvironmentID: "primary", ExecServerURL: "ws://primary.test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Add(&EnvironmentAddParams{EnvironmentID: "secondary", ExecServerURL: "ws://secondary.test"}); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{Environment: manager})
	fileSystem := router.primaryTurnOpenAIFileSystem(&turn.TurnStartParams{Environments: []map[string]any{
		{"environmentId": "primary", "cwd": "/primary"},
		{"environmentId": "secondary", "cwd": "/secondary"},
	}})
	selected, ok := fileSystem.(*environmentOpenAIFileSystem)
	if !ok || selected.record.EnvironmentID != "primary" || selected.record.ExecServerURL != "ws://primary.test" {
		t.Fatalf("primary file system = %#v", fileSystem)
	}
}

func TestOpenAIFileReadPolicyForLocalFallback(t *testing.T) {
	denied := filepath.Join("workspace", ".env")
	profile := &sandbox.PermissionProfile{DeniedReadEntries: []sandbox.FileSystemSandboxEntry{
		{Path: sandbox.FileSystemPath{Type: "path", Path: denied}, Access: sandbox.FileSystemAccessDeny},
	}}
	resolution := &config.SandboxPermissionProfileResolution{Profile: profile}

	policy := openAIFileReadPolicy(nil, resolution)
	if policy == nil {
		t.Fatal("local fallback should produce a read policy")
	}
	if err := policy(denied); err == nil {
		t.Fatal("denied path should be rejected")
	}
	if err := policy(filepath.Join("workspace", "ok.txt")); err != nil {
		t.Fatalf("allowed path rejected: %v", err)
	}

	if policy := openAIFileReadPolicy(&environmentOpenAIFileSystem{}, resolution); policy != nil {
		t.Fatal("remote executor file system should not need a local read policy")
	}
	if policy := openAIFileReadPolicy(nil, nil); policy != nil {
		t.Fatal("missing permission profile should not produce a read policy")
	}
}

func TestEnvironmentOpenAIFileSystemRejectsUnsupportedSandboxedFileStreaming(t *testing.T) {
	fs := &environmentOpenAIFileSystem{requiresSandbox: true}
	err := fs.requireSandboxedFileStreaming(context.Background(), sandboxedFileStreamingInfoFunc(func(context.Context) (*execserver.EnvironmentInfo, error) {
		return &execserver.EnvironmentInfo{Capabilities: execserver.EnvironmentCapabilities{SandboxedFileStreaming: false}}, nil
	}))
	if err == nil || !strings.Contains(err.Error(), "does not support sandboxed file streaming") {
		t.Fatalf("unsupported capability error = %v", err)
	}
	if err := (&environmentOpenAIFileSystem{}).requireSandboxedFileStreaming(context.Background(), nil); err != nil {
		t.Fatalf("non-sandbox file system should skip capability check: %v", err)
	}
}

type sandboxedFileStreamingInfoFunc func(context.Context) (*execserver.EnvironmentInfo, error)

func (f sandboxedFileStreamingInfoFunc) EnvironmentInfo(ctx context.Context) (*execserver.EnvironmentInfo, error) {
	return f(ctx)
}
