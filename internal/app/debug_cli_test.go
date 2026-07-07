package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDebugModelsOutputsBundledCatalog(t *testing.T) {
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"debug", "models", "--bundled"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("debug models returned error: %v", err)
	}
	var payload struct {
		Models []struct {
			Slug string `json:"slug"`
		} `json:"models"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal debug models output %q: %v", stdout.String(), err)
	}
	found := false
	for _, model := range payload.Models {
		if model.Slug == "gpt-5.5" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("models output missing gpt-5.5: %q", stdout.String())
	}
}

func TestDebugAppServerSendMessageV2(t *testing.T) {
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"debug", "app-server", "send-message-v2", "hello"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("debug app-server returned error: %v", err)
	}
	var payload struct {
		Method string `json:"method"`
		Params struct {
			Prompt string `json:"prompt"`
		} `json:"params"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal debug app-server output %q: %v", stdout.String(), err)
	}
	if payload.Method != "thread/start" || payload.Params.Prompt != "hello" {
		t.Fatalf("debug app-server payload = %#v", payload)
	}
}

func TestDebugTraceReduce(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"trace_id":"trace-1"}`), 0o600); err != nil {
		t.Fatalf("WriteFile manifest error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "trace.jsonl"), []byte(`{"seq":1,"type":"thread_started","thread_id":"thread-1"}`+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile trace error = %v", err)
	}
	outputPath := filepath.Join(dir, "custom-state.json")
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"debug", "trace-reduce", "--output", outputPath, dir}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("debug trace-reduce returned error: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != outputPath {
		t.Fatalf("stdout = %q", stdout.String())
	}
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("ReadFile output error = %v", err)
	}
	if !strings.Contains(string(data), `"thread-1"`) {
		t.Fatalf("state = %q", string(data))
	}
}

func TestDebugClearMemories(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	memoriesDir := filepath.Join(home, "memories")
	if err := os.MkdirAll(memoriesDir, 0o755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(memoriesDir, "memory.md"), []byte("stale"), 0o600); err != nil {
		t.Fatalf("WriteFile memory error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "memories.sqlite"), []byte("fake"), 0o600); err != nil {
		t.Fatalf("WriteFile db error = %v", err)
	}
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"debug", "clear-memories"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("debug clear-memories returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Cleared memory state") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	entries, err := os.ReadDir(memoriesDir)
	if err != nil {
		t.Fatalf("ReadDir memories error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("memories entries = %#v", entries)
	}
}

func TestDebugConfigOutputsEffectiveConfigAndLayers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("model = \"gpt-debug\"\n[features]\nweb_search = true\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config error = %v", err)
	}
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"debug", "config"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("debug config returned error: %v", err)
	}
	var payload struct {
		Config  map[string]any `json:"config"`
		Origins map[string]any `json:"origins"`
		Layers  []any          `json:"layers"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal debug config output %q: %v", stdout.String(), err)
	}
	if payload.Config["model"] != "gpt-debug" {
		t.Fatalf("config = %#v", payload.Config)
	}
	if _, ok := payload.Origins["features.web_search"]; !ok {
		t.Fatalf("origins = %#v", payload.Origins)
	}
	if len(payload.Layers) != 1 {
		t.Fatalf("layers = %#v", payload.Layers)
	}
}
