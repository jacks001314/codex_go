package cloudbackend

import (
	"context"
	"encoding/json"
	"testing"

	"codex_go/appserver"
	"codex_go/cloudclient"
)

// Rust #59828: the TUI forwards its local backend settings to Cloud, which owns
// the conversation environment, so those fields must not reject the start.
func TestCloudThreadStartAcceptsTUISettingsLikeRust(t *testing.T) {
	var prewarmed [2]string
	fake := &fakeCloud{
		attach: func(context.Context, string) (<-chan cloudclient.Event, <-chan error, error) {
			return pendingStream()
		},
		prewarm: func(_ context.Context, model string, modelProvider string) (*cloudclient.ThreadActivation, error) {
			prewarmed = [2]string{model, modelProvider}
			return &cloudclient.ThreadActivation{
				Thread: cloudclient.ThreadSnapshot{ID: "cloud-thread", HistoryMode: "legacy", ModelProvider: modelProvider, CWD: "/repo", Status: map[string]any{"type": "idle"}, Source: "cli"},
				Model:  model, ModelProvider: modelProvider, CWD: "/repo",
			}, nil
		},
	}
	client := Connect(context.Background(), fake, json.RawMessage("{}"))
	defer client.Close()

	payload := json.RawMessage(`{
		"model": "gpt-5",
		"modelProvider": "openai",
		"cwd": "/repo",
		"approvalPolicy": "on-request",
		"sandbox": "workspace-write",
		"developerInstructions": "be brief",
		"config": {"model_reasoning_effort": "high"},
		"ephemeral": false
	}`)
	started, rpcErr := client.Request(context.Background(), appserver.MethodThreadStart, payload)
	if rpcErr != nil {
		t.Fatalf("thread/start with TUI settings error = %#v", rpcErr)
	}
	var response appserver.ThreadStartResponse
	if err := json.Unmarshal(started, &response); err != nil || response.Thread == nil || response.Thread.ID != "cloud-thread" {
		t.Fatalf("thread/start = %s (err=%v)", started, err)
	}
	if prewarmed != [2]string{"gpt-5", "openai"} {
		t.Fatalf("prewarm = %#v", prewarmed)
	}
}

// Rust #59828: resuming a Cloud thread drops a local working-directory
// override instead of rejecting the resume.
func TestCloudThreadResumeDropsLocalCWDLikeRust(t *testing.T) {
	fake := &fakeCloud{
		activate: func(context.Context, string) (*cloudclient.ThreadActivation, error) {
			return &cloudclient.ThreadActivation{
				Thread: cloudclient.ThreadSnapshot{ID: "cloud-thread", HistoryMode: "legacy", ModelProvider: "openai", CWD: "/repo", Status: map[string]any{"type": "idle"}, Source: "cli"},
			}, nil
		},
		attach: func(context.Context, string) (<-chan cloudclient.Event, <-chan error, error) {
			return pendingStream()
		},
	}
	client := Connect(context.Background(), fake, json.RawMessage("{}"))
	defer client.Close()
	resumed, rpcErr := client.Request(context.Background(), appserver.MethodThreadResume, json.RawMessage(`{"threadId":"cloud-thread","excludeTurns":true,"cwd":"/local"}`))
	if rpcErr != nil {
		t.Fatalf("thread/resume with cwd error = %#v, body=%s", rpcErr, resumed)
	}
}

// The hosted backend answers the app-server handshake itself.
func TestCloudBackendAnswersInitializeLikeRust(t *testing.T) {
	client := Connect(context.Background(), &fakeCloud{}, json.RawMessage("{}"))
	defer client.Close()
	raw, rpcErr := client.Request(context.Background(), appserver.MethodInitialize, json.RawMessage(`{"clientInfo":{"name":"codex_vscode","version":"0.1.0"}}`))
	if rpcErr != nil {
		t.Fatalf("initialize error = %#v", rpcErr)
	}
	var response appserver.InitializeResponse
	if err := json.Unmarshal(raw, &response); err != nil || response.UserAgent != "codex-cloud-backend" {
		t.Fatalf("initialize = %s (err=%v)", raw, err)
	}
}

// Rust #60007/#60309: every thread start resolves the caller's host project,
// so the prewarm carries it and clearing the selection returns to the default.
func TestCloudThreadStartPrewarmsWithLocalProjectLikeRust(t *testing.T) {
	starts := 0
	fake := &fakeCloud{
		attach: func(context.Context, string) (<-chan cloudclient.Event, <-chan error, error) {
			return pendingStream()
		},
		prewarm: func(context.Context, string, string) (*cloudclient.ThreadActivation, error) {
			starts++
			id := "cloud-thread"
			if starts > 1 {
				id = "cloud-thread-2"
			}
			return &cloudclient.ThreadActivation{
				Thread: cloudclient.ThreadSnapshot{ID: id, HistoryMode: "legacy", ModelProvider: "openai", CWD: "/repo", Status: map[string]any{"type": "idle"}, Source: "cli"},
				CWD:    "/repo",
			}, nil
		},
	}
	client := Connect(context.Background(), fake, json.RawMessage("{}"))
	defer client.Close()

	client.SetLocalProject(&cloudclient.ProjectEnvironment{
		Name:       "repo",
		OriginURLs: []string{"https://github.com/org/repo"},
		CWD:        "/workspace/org/repo",
	})
	if _, rpcErr := client.Request(context.Background(), appserver.MethodThreadStart, json.RawMessage(`{"ephemeral":false}`)); rpcErr != nil {
		t.Fatalf("thread/start error = %#v", rpcErr)
	}
	if fake.prewarmProject == nil || fake.prewarmProject.Name != "repo" ||
		fake.prewarmProject.CWD != "/workspace/org/repo" ||
		len(fake.prewarmProject.OriginURLs) != 1 || fake.prewarmProject.OriginURLs[0] != "https://github.com/org/repo" {
		t.Fatalf("prewarm project = %#v", fake.prewarmProject)
	}

	client.SetLocalProject(nil)
	if _, rpcErr := client.Request(context.Background(), appserver.MethodThreadStart, json.RawMessage(`{"ephemeral":false}`)); rpcErr != nil {
		t.Fatalf("second thread/start error = %#v", rpcErr)
	}
	if fake.prewarmProject != nil {
		t.Fatalf("cleared project still prewarmed %#v", fake.prewarmProject)
	}
}
