package appserver

import (
	"context"
	"io"
	"os"
	"testing"
	"time"

	"codex_go/execserver"
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
	fileSystem := &environmentOpenAIFileSystem{record: EnvironmentRecord{ExecServerURL: serverURL}}
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
