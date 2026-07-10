package exec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/internal/auth"
	"codex_go/internal/cli"
	"codex_go/internal/config"
	"codex_go/internal/model"
	"codex_go/internal/protocol"
	"codex_go/internal/rollout"
	"codex_go/internal/sandbox"
	"codex_go/internal/session"
	"codex_go/internal/tool"
	"codex_go/internal/turn"
)

type execTerminalBuffer struct {
	bytes.Buffer
}

func (b *execTerminalBuffer) IsTerminal() bool {
	return true
}

func TestRunJSONAndLastMessage(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	lastMessage := filepath.Join(t.TempDir(), "last.txt")
	var stdout, stderr bytes.Buffer
	result, err := NewLocalRunner(home).Run(Request{
		Exec: cli.ExecOptions{
			Prompt:          "hello",
			JSON:            true,
			LastMessageFile: lastMessage,
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.LastMessage == "" {
		t.Fatal("LastMessage is empty")
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("json lines = %d, want 4: %q", len(lines), stdout.String())
	}
	var first protocol.ThreadEvent
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("Unmarshal first event returned error: %v", err)
	}
	if first.Type != "thread.started" || first.ThreadID == "" {
		t.Fatalf("first event = %#v", first)
	}
	data, err := os.ReadFile(lastMessage)
	if err != nil {
		t.Fatalf("ReadFile last message returned error: %v", err)
	}
	if strings.TrimSpace(string(data)) != result.LastMessage {
		t.Fatalf("last message file = %q, want %q", string(data), result.LastMessage)
	}
	record := loadSessionRecord(t, home, result.ThreadID)
	if got := record.Items; len(got) != 2 || got[0].Role != "user" || got[1].Role != "assistant" {
		t.Fatalf("session items = %#v", got)
	}
	if record.Items[1].Metadata["timingProfile"] == nil || record.Items[1].Metadata["timing_profile"] == nil {
		t.Fatalf("assistant timing profile metadata = %#v", record.Items[1].Metadata)
	}
	if record.Metadata.Source != "cli" || record.Metadata.ThreadSource != string(model.AgentTaskRegular) {
		t.Fatalf("session metadata = %#v", record.Metadata)
	}
}

func TestNewRunnerDefaultsToResponsesAPI(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	type observedRequest struct {
		path          string
		authorization string
		accept        string
	}
	seen := make(chan observedRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- observedRequest{
			path:          r.URL.Path,
			authorization: r.Header.Get("Authorization"),
			accept:        r.Header.Get("Accept"),
		}
		w.Header().Set("Content-Type", "text/event-stream")
		writeExecResponseSSE(w, `{"type":"response.created","response":{"id":"resp-default"}}`)
		writeExecResponseSSE(w, `{"type":"response.output_item.added","item":{"id":"msg-default","type":"message","role":"assistant","content":[{"type":"output_text","text":""}]}}`)
		writeExecResponseSSE(w, `{"type":"response.output_text.delta","item_id":"msg-default","delta":"real default"}`)
		writeExecResponseSSE(w, `{"type":"response.output_item.done","item":{"id":"msg-default","type":"message","role":"assistant","content":[{"type":"output_text","text":"real default"}]}}`)
		writeExecResponseSSE(w, `{"type":"response.completed","response":{"id":"resp-default","usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}}`)
	}))
	defer server.Close()
	if err := os.WriteFile(config.ConfigPath(home), []byte("openai_base_url = \""+server.URL+"/v1\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}

	var stdout, stderr bytes.Buffer
	result, err := NewRunner(home).Run(Request{
		Exec: cli.ExecOptions{
			Prompt:    "hello",
			JSON:      true,
			Ephemeral: true,
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v stderr=%q stdout=%q", err, stderr.String(), stdout.String())
	}
	if result.LastMessage != "real default" || strings.Contains(stdout.String(), "Go Codex exec stub received") {
		t.Fatalf("result = %#v stdout = %q", result, stdout.String())
	}
	select {
	case request := <-seen:
		if request.path != "/v1/responses" || request.authorization != "Bearer sk-test" || request.accept != "text/event-stream" {
			t.Fatalf("request = %#v", request)
		}
	default:
		t.Fatal("Responses API server did not receive a request")
	}
}

func TestNewRunnerReadsRustAuthModeAliasAndConfiguredProvider(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"auth_mode":"apikey","OPENAI_API_KEY":"sk-rust"}`), 0o600); err != nil {
		t.Fatalf("WriteFile auth returned error: %v", err)
	}
	type observedRequest struct {
		path          string
		authorization string
	}
	seen := make(chan observedRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- observedRequest{
			path:          r.URL.Path,
			authorization: r.Header.Get("Authorization"),
		}
		w.Header().Set("Content-Type", "text/event-stream")
		writeExecResponseSSE(w, `{"type":"response.created","response":{"id":"resp-rust-auth"}}`)
		writeExecResponseSSE(w, `{"type":"response.output_item.added","item":{"id":"msg-rust-auth","type":"message","role":"assistant","content":[{"type":"output_text","text":""}]}}`)
		writeExecResponseSSE(w, `{"type":"response.output_text.delta","item_id":"msg-rust-auth","delta":"configured auth"}`)
		writeExecResponseSSE(w, `{"type":"response.output_item.done","item":{"id":"msg-rust-auth","type":"message","role":"assistant","content":[{"type":"output_text","text":"configured auth"}]}}`)
		writeExecResponseSSE(w, `{"type":"response.completed","response":{"id":"resp-rust-auth","usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}}`)
	}))
	defer server.Close()
	configBody := `
model = "gpt-5.5"
model_provider = "OpenAI"

[model_providers.OpenAI]
name = "OpenAI"
base_url = "` + server.URL + `/v1"
requires_openai_auth = true
wire_api = "responses"
`
	if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}

	var stdout, stderr bytes.Buffer
	result, err := NewRunner(home).Run(Request{
		Exec: cli.ExecOptions{
			Prompt:    "hello",
			JSON:      true,
			Ephemeral: true,
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v stderr=%q stdout=%q", err, stderr.String(), stdout.String())
	}
	if result.LastMessage != "configured auth" {
		t.Fatalf("LastMessage = %q", result.LastMessage)
	}
	select {
	case request := <-seen:
		if request.path != "/v1/responses" || request.authorization != "Bearer sk-rust" {
			t.Fatalf("request = %#v", request)
		}
	default:
		t.Fatal("Responses API server did not receive a request")
	}
}

func TestNewRunnerFailsBeforeRequestWhenOpenAIAuthMissing(t *testing.T) {
	home := t.TempDir()
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	if err := os.WriteFile(config.ConfigPath(home), []byte("openai_base_url = \""+server.URL+"/v1\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}

	var stdout, stderr bytes.Buffer
	_, err := NewRunner(home).Run(Request{
		Exec: cli.ExecOptions{
			Prompt:    "hello",
			JSON:      true,
			Ephemeral: true,
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "OpenAI authentication is required") {
		t.Fatalf("Run error = %v", err)
	}
	if requests != 0 {
		t.Fatalf("server requests = %d, want 0", requests)
	}
}

func TestRunHumanPrintsTokenUsageToStderr(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	runner := NewRunner(home)
	runner.Agent = &recordingAgent{
		message: "done",
		usage: model.AgentUsage{
			InputTokens:       1234,
			CachedInputTokens: 200,
			OutputTokens:      66,
		},
	}
	var stdout, stderr bytes.Buffer
	result, err := runner.Run(Request{
		Exec: cli.ExecOptions{Prompt: "hello"},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.LastMessage != "done" || stdout.String() != "done\n" {
		t.Fatalf("stdout = %q, result = %#v", stdout.String(), result)
	}
	if got := stderr.String(); got != "approval: never\ntokens used\n1,100\n" {
		t.Fatalf("stderr = %q", got)
	}
}

func TestRunHumanApprovalSummaryPreservesAutoReviewPolicyLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	if err := os.WriteFile(config.ConfigPath(home), []byte("approval_policy = \"on-request\"\napprovals_reviewer = \"auto_review\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}
	runner := NewRunner(home)
	runner.Agent = &recordingAgent{message: "done"}
	var stdout, stderr bytes.Buffer
	_, err := runner.Run(Request{
		Exec: cli.ExecOptions{Prompt: "check approval mode"},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(stderr.String(), "approval: on-request") {
		t.Fatalf("stderr = %q, want preserved auto-review approval mode", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	_, err = runner.Run(Request{
		Exec: cli.ExecOptions{
			Prompt: "check bypass approval mode",
			Shared: cli.SharedOptions{DangerouslyBypassApprovalsAndSandbox: true},
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run bypass returned error: %v", err)
	}
	if !strings.Contains(stderr.String(), "approval: never") {
		t.Fatalf("stderr = %q, want bypass approval mode", stderr.String())
	}
}

func TestRunStructuredInputItemsAndSessionContent(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	imagePath := filepath.Join(t.TempDir(), "diagram.png")
	if err := os.WriteFile(imagePath, []byte("fake image bytes"), 0o600); err != nil {
		t.Fatalf("WriteFile image returned error: %v", err)
	}
	agent := &recordingAgent{message: "done"}
	runner := NewRunner(home)
	runner.Agent = agent

	var stdout, stderr bytes.Buffer
	result, err := runner.Run(Request{
		Exec: cli.ExecOptions{JSON: true},
		Input: []turn.TurnUserInput{{
			Type: "image",
			URL:  "https://example.test/preview.png",
		}, {
			Type: "localImage",
			Path: imagePath,
		}, {
			Type: "text",
			Text: "review these",
		}},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.Prompt != "" {
		t.Fatalf("result prompt = %q, want empty structured prompt", result.Prompt)
	}
	if agent.request == nil {
		t.Fatal("agent request was not captured")
	}
	if agent.request.Prompt != "" {
		t.Fatalf("agent prompt = %q, want empty when input items carry user input", agent.request.Prompt)
	}
	item := agentRequestInputItemWithText(agent.request, "review these")
	if item == nil {
		t.Fatalf("input items = %#v", agent.request.InputItems)
	}
	if item["role"] != "user" {
		t.Fatalf("user input item = %#v", item)
	}
	content, ok := item["content"].([]map[string]any)
	if !ok || len(content) != 5 {
		t.Fatalf("content = %#v", item["content"])
	}
	if content[0]["type"] != "input_image" || content[0]["image_url"] != "https://example.test/preview.png" {
		t.Fatalf("remote image content = %#v", content[0])
	}
	if content[2]["type"] != "input_image" || !strings.HasPrefix(fmt.Sprint(content[2]["image_url"]), "data:") {
		t.Fatalf("local image content = %#v", content[2])
	}
	if content[4]["type"] != "input_text" || content[4]["text"] != "review these" {
		t.Fatalf("text content = %#v", content[4])
	}

	record := loadSessionRecord(t, home, result.ThreadID)
	if len(record.Items) == 0 || len(record.Items[0].Content) != 3 {
		t.Fatalf("session user content = %#v", record.Items)
	}
	if record.Items[0].Content[0].Type != "image" || record.Items[0].Content[0].ImageURL != "https://example.test/preview.png" {
		t.Fatalf("session remote image = %#v", record.Items[0].Content[0])
	}
	if record.Items[0].Content[1].Type != "localImage" || record.Items[0].Content[1].ImageURL != imagePath {
		t.Fatalf("session local image = %#v", record.Items[0].Content[1])
	}
	if record.Items[0].Content[2].Type != "input_text" || record.Items[0].Content[2].Text != "review these" {
		t.Fatalf("session text = %#v", record.Items[0].Content[2])
	}
}

func TestRunAddsStartupEnvironmentContextInputItems(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	project := t.TempDir()
	agent := &recordingAgent{message: "done"}
	runner := NewRunner(home)
	runner.Agent = agent
	runner.Now = func() time.Time {
		return time.Date(2026, 7, 9, 10, 0, 0, 0, time.FixedZone("Asia/Hong_Kong", 8*60*60))
	}

	var stdout, stderr bytes.Buffer
	_, err := runner.Run(Request{
		Exec: cli.ExecOptions{
			Prompt: "hello",
			Shared: cli.SharedOptions{
				CWD:                                  project,
				DangerouslyBypassApprovalsAndSandbox: true,
			},
			Ephemeral: true,
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !agentRequestInputItemsContainText(agent.request, "<permissions instructions>") ||
		!agentRequestInputItemsContainText(agent.request, "danger-full-access") ||
		!agentRequestInputItemsContainText(agent.request, "<environment_context>") ||
		!agentRequestInputItemsContainText(agent.request, "<cwd>"+filepath.Clean(project)+"</cwd>") ||
		!agentRequestInputItemsContainText(agent.request, "<current_date>2026-07-09</current_date>") ||
		!agentRequestInputItemsContainText(agent.request, `<permission_profile type="disabled"><file_system type="unrestricted" /></permission_profile>`) {
		t.Fatalf("startup input items = %#v", agent.request.InputItems)
	}
}

func TestRunHumanPrintsFinalMessageToStderrWhenBothStreamsAreTTY(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	runner := NewRunner(home)
	runner.Agent = &recordingAgent{
		message: "done",
		usage:   model.AgentUsage{InputTokens: 10, OutputTokens: 5},
	}
	var stdout, stderr execTerminalBuffer
	result, err := runner.Run(Request{
		Exec: cli.ExecOptions{Prompt: "hello"},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.LastMessage != "done" {
		t.Fatalf("result = %#v", result)
	}
	if stdout.String() != "" {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if got := stderr.String(); got != "approval: never\ntokens used\n15\ncodex\ndone\n" {
		t.Fatalf("stderr = %q", got)
	}
}

func TestResolveExecSandboxPermissionProfileUsesCLIOverride(t *testing.T) {
	cfg := &config.Config{Values: map[string]any{"sandbox_mode": "read-only"}}
	req := &Request{Exec: cli.ExecOptions{Shared: cli.SharedOptions{Sandbox: "workspace-write", CWD: t.TempDir()}}}

	resolved, err := resolveExecSandboxPermissionProfile(cfg, req)
	if err != nil {
		t.Fatalf("resolveExecSandboxPermissionProfile() error = %v", err)
	}
	if resolved == nil || resolved.Profile == nil || resolved.Profile.LegacySandboxPolicy().Kind != sandbox.SandboxWorkspaceWrite {
		t.Fatalf("resolved = %#v, want workspace-write", resolved)
	}
}

func TestResolveExecSandboxPermissionProfileBypassUsesFullAccess(t *testing.T) {
	cfg := &config.Config{Values: map[string]any{"sandbox_mode": "read-only"}}
	req := &Request{Root: cli.RootOptions{Shared: cli.SharedOptions{DangerouslyBypassApprovalsAndSandbox: true}}}

	resolved, err := resolveExecSandboxPermissionProfile(cfg, req)
	if err != nil {
		t.Fatalf("resolveExecSandboxPermissionProfile() error = %v", err)
	}
	if resolved == nil || resolved.Profile == nil || !resolved.Profile.Disabled {
		t.Fatalf("resolved = %#v, want full access", resolved)
	}
}

func TestRunRejectsInvalidOutputSchema(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	schema := filepath.Join(t.TempDir(), "schema.json")
	if err := os.WriteFile(schema, []byte("{"), 0o600); err != nil {
		t.Fatalf("WriteFile schema returned error: %v", err)
	}
	var stdout, stderr bytes.Buffer
	_, err := NewRunner(home).Run(Request{
		Exec: cli.ExecOptions{
			Prompt:       "hello",
			OutputSchema: schema,
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err == nil {
		t.Fatal("Run returned nil error, want failure")
	}
	if !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunPassesOutputSchemaToAgent(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	schema := filepath.Join(t.TempDir(), "schema.json")
	if err := os.WriteFile(schema, []byte(`{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}`), 0o600); err != nil {
		t.Fatalf("WriteFile schema returned error: %v", err)
	}
	agent := &recordingAgent{message: "ok"}
	runner := NewRunner(home)
	runner.Agent = agent
	var stdout, stderr bytes.Buffer
	_, err := runner.Run(Request{
		Exec: cli.ExecOptions{
			Prompt:       "hello",
			OutputSchema: schema,
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if agent.request == nil {
		t.Fatal("agent request is nil")
	}
	schemaBody, ok := agent.request.OutputSchema.(map[string]any)
	if !ok || schemaBody["type"] != "object" || schemaBody["additionalProperties"] != false {
		t.Fatalf("OutputSchema = %#v", agent.request.OutputSchema)
	}
}

func TestRunPassesResponsesAPIClientMetadataToAgent(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	configBody := `
[responsesapi_client_metadata]
workspace_kind = "git"
too_long = "` + strings.Repeat("x", 513) + `"
`
	if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}
	agent := &recordingAgent{message: "ok"}
	runner := NewRunner(home)
	runner.Agent = agent
	var stdout, stderr bytes.Buffer
	result, err := runner.Run(Request{
		Exec: cli.ExecOptions{Prompt: "hello"},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if agent.request == nil {
		t.Fatalf("ClientMetadata = %#v", agent.request)
	}
	if agent.request.PromptCacheKey != result.ThreadID {
		t.Fatalf("PromptCacheKey = %q, want %q", agent.request.PromptCacheKey, result.ThreadID)
	}
	var turnMetadata map[string]any
	if err := json.Unmarshal([]byte(agent.request.ClientMetadata["x-codex-turn-metadata"]), &turnMetadata); err != nil {
		t.Fatalf("turn metadata json error = %v metadata=%#v", err, agent.request.ClientMetadata)
	}
	if turnMetadata["workspace_kind"] != "git" || turnMetadata["thread_id"] == "" || turnMetadata["turn_id"] == "" {
		t.Fatalf("turn metadata = %#v client=%#v", turnMetadata, agent.request.ClientMetadata)
	}
	if agent.request.ClientMetadata["workspace_kind"] != "" {
		t.Fatalf("workspace_kind should not be top-level client metadata: %#v", agent.request.ClientMetadata)
	}
	if _, ok := agent.request.ClientMetadata["too_long"]; ok {
		t.Fatalf("too_long metadata should be filtered: %#v", agent.request.ClientMetadata)
	}
}

func TestRunWarnsForRemovedFullAuto(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	var stdout, stderr bytes.Buffer
	_, err := NewLocalRunner(home).Run(Request{
		Exec: cli.ExecOptions{
			Prompt:          "hello",
			RemovedFullAuto: true,
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(stderr.String(), "--full-auto") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunExecReview(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	var stdout, stderr bytes.Buffer
	result, err := NewLocalRunner(home).Run(Request{
		Exec: cli.ExecOptions{
			Subcommand: "review",
			Review: cli.ReviewOptions{
				Prompt: "check concurrency",
			},
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(result.Prompt, "custom instructions: check concurrency") {
		t.Fatalf("Prompt = %q", result.Prompt)
	}
	if !strings.Contains(stdout.String(), "Review with custom instructions: check concurrency") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	record := loadSessionRecord(t, home, result.ThreadID)
	if record.Metadata.ThreadSource != string(model.AgentTaskReview) {
		t.Fatalf("ThreadSource = %q, want review", record.Metadata.ThreadSource)
	}
}

func TestRunExecResumeAppendsToExistingSession(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	now := fixedExecTime()
	store := session.NewStore(filepath.Join(home, "sessions"))
	if err := store.Save(&session.Record{
		ID:        "thread-existing",
		SessionID: "thread-existing",
		Title:     "Existing",
		Preview:   "old user",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Items: []session.Item{
			{ID: "old-user", Type: "message", Role: "user", Text: "old user", CreatedAt: now},
			{ID: "old-assistant", Type: "agent_message", Role: "assistant", Text: "old answer", CreatedAt: now},
		},
	}); err != nil {
		t.Fatalf("Save session returned error: %v", err)
	}
	agent := &recordingAgent{message: "resume answer"}
	runner := NewRunner(home)
	runner.Agent = agent
	runner.Now = func() time.Time { return now.Add(time.Minute) }
	var stdout, stderr bytes.Buffer
	result, err := runner.Run(Request{
		Exec: cli.ExecOptions{
			Subcommand: "resume",
			Resume: cli.ExecResumeOptions{
				SessionID: "thread-existing",
				Prompt:    "new question",
			},
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.ThreadID != "thread-existing" {
		t.Fatalf("ThreadID = %q", result.ThreadID)
	}
	if agent.request.Prompt != "new question" {
		t.Fatalf("agent prompt = %q, want new question", agent.request.Prompt)
	}
	if !agentRequestInputItemsHaveText(agent.request, "old user") || !agentRequestInputItemsHaveText(agent.request, "old answer") {
		t.Fatalf("agent input items = %#v", agent.request.InputItems)
	}
	record := loadSessionRecord(t, home, "thread-existing")
	if len(record.Items) != 4 {
		t.Fatalf("items = %#v", record.Items)
	}
	if record.Items[2].Text != "new question" || record.Items[3].Text != "resume answer" {
		t.Fatalf("new items = %#v", record.Items[2:])
	}
	if record.Metadata.SessionPrefix == "" {
		t.Fatalf("session prefix was not recorded: %#v", record.Metadata)
	}
	rolloutPath, err := rollout.FindThreadPath(home, "thread-existing", false)
	if err != nil {
		t.Fatalf("rollout path error: %v", err)
	}
	lines, _, err := rollout.Load(rolloutPath)
	if err != nil {
		t.Fatalf("rollout load error: %v", err)
	}
	if len(lines) < 3 {
		t.Fatalf("rollout lines = %+v", lines)
	}
}

func TestRunExecResumeLastSelectsNewestSession(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	now := fixedExecTime()
	store := session.NewStore(filepath.Join(home, "sessions"))
	if err := store.Save(&session.Record{ID: "old", CreatedAt: now, UpdatedAt: now, RecencyAt: now}); err != nil {
		t.Fatalf("Save old returned error: %v", err)
	}
	if err := store.Save(&session.Record{ID: "new", CreatedAt: now, UpdatedAt: now.Add(time.Minute), RecencyAt: now.Add(time.Minute)}); err != nil {
		t.Fatalf("Save new returned error: %v", err)
	}
	runner := NewRunner(home)
	runner.Agent = &recordingAgent{message: "ok"}
	var stdout, stderr bytes.Buffer
	result, err := runner.Run(Request{
		Exec: cli.ExecOptions{
			Subcommand: "resume",
			Resume: cli.ExecResumeOptions{
				Last:   true,
				Prompt: "continue",
			},
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.ThreadID != "new" {
		t.Fatalf("ThreadID = %q", result.ThreadID)
	}
}

func TestRunExecResumeLastAllSelectsNewestArchivedSession(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	now := fixedExecTime()
	store := session.NewStore(filepath.Join(home, "sessions"))
	if err := store.Save(&session.Record{ID: "active", CreatedAt: now, UpdatedAt: now, RecencyAt: now}); err != nil {
		t.Fatalf("Save active returned error: %v", err)
	}
	if err := store.Save(&session.Record{
		ID:        "archived-new",
		Archived:  true,
		CreatedAt: now,
		UpdatedAt: now.Add(time.Minute),
		RecencyAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("Save archived returned error: %v", err)
	}
	runner := NewRunner(home)
	runner.Agent = &recordingAgent{message: "ok"}
	var stdout, stderr bytes.Buffer
	result, err := runner.Run(Request{
		Exec: cli.ExecOptions{
			Subcommand: "resume",
			Resume: cli.ExecResumeOptions{
				Last:   true,
				All:    true,
				Prompt: "continue",
			},
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.ThreadID != "archived-new" {
		t.Fatalf("ThreadID = %q", result.ThreadID)
	}
}

func TestRequestCWDUsesExecBeforeRoot(t *testing.T) {
	got := requestCWD(&Request{
		Root: cli.RootOptions{Shared: cli.SharedOptions{CWD: "root-dir"}},
		Exec: cli.ExecOptions{Shared: cli.SharedOptions{CWD: "exec-dir"}},
	})
	if got != "exec-dir" {
		t.Fatalf("requestCWD = %q", got)
	}
}

func TestEffectiveExecApprovalPolicyMatchesRustHeadless(t *testing.T) {
	autoReviewConfig := &config.Config{Values: map[string]any{
		"approval_policy":    string(sandbox.ApprovalOnRequest),
		"approvals_reviewer": string(config.ApprovalsReviewerAutoReview),
	}}
	if got := effectiveExecApprovalPolicy(autoReviewConfig, &Request{}); got != sandbox.ApprovalOnRequest {
		t.Fatalf("auto-review approval policy = %q, want on-request", got)
	}

	defaultConfig := &config.Config{Values: map[string]any{
		"approval_policy": string(sandbox.ApprovalOnRequest),
	}}
	if got := effectiveExecApprovalPolicy(defaultConfig, &Request{}); got != sandbox.ApprovalNever {
		t.Fatalf("headless approval policy = %q, want never", got)
	}

	req := &Request{
		Exec: cli.ExecOptions{
			RemovedFullAuto: true,
		},
	}
	if got := effectiveExecApprovalPolicy(autoReviewConfig, req); got != sandbox.ApprovalNever {
		t.Fatalf("full-auto approval policy = %q, want never", got)
	}

	req = &Request{
		Exec: cli.ExecOptions{Shared: cli.SharedOptions{DangerouslyBypassApprovalsAndSandbox: true}},
	}
	if got := effectiveExecApprovalPolicy(autoReviewConfig, req); got != sandbox.ApprovalNever {
		t.Fatalf("bypass approval policy = %q, want never", got)
	}
}

func TestToolRouterUsesExecHeadlessApprovalPolicyLikeRust(t *testing.T) {
	runner := NewLocalRunner(t.TempDir())
	req := &Request{Exec: cli.ExecOptions{Prompt: "hello"}}
	invocation := &tool.Invocation{
		CallID:   "call-approval",
		ToolName: tool.PlainName(tool.DefaultExecCommandToolName),
		Payload: tool.Payload{
			Kind:      tool.PayloadFunction,
			Arguments: `{"cmd":"echo hi","sandbox_permissions":"require_escalated","justification":"need more access"}`,
		},
	}

	router, err := runner.toolRouterForRequest(req, &agentRunConfig{
		ApprovalPolicy: effectiveExecApprovalPolicy(&config.Config{Values: map[string]any{}}, req),
	})
	if err != nil {
		t.Fatalf("toolRouterForRequest returned error: %v", err)
	}
	_, err = router.Dispatch(context.Background(), invocation)
	var callErr *tool.FunctionCallError
	if !tool.AsFunctionCallError(err, &callErr) || !callErr.RespondsToModel() || !strings.Contains(callErr.ModelMessage(), "approval policy is never") {
		t.Fatalf("Dispatch error = %#v, want model-visible never-policy rejection", err)
	}

	router, err = runner.toolRouterForRequest(req, &agentRunConfig{
		ApprovalPolicy: sandbox.ApprovalOnRequest,
	})
	if err != nil {
		t.Fatalf("toolRouterForRequest auto-review returned error: %v", err)
	}
	output, err := router.Dispatch(context.Background(), invocation)
	if err != nil {
		t.Fatalf("Dispatch auto-review returned error: %v", err)
	}
	if output == nil || output.Success || output.Data["approval_required"] != true {
		t.Fatalf("auto-review output = %#v, want approval request", output)
	}
}

func TestRunRejectsUnknownExecSubcommandWithoutGoPortMessage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	_, err := NewLocalRunner(t.TempDir()).Run(Request{
		Exec: cli.ExecOptions{
			Subcommand: "unsupported",
			Prompt:     "hello",
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err == nil || err.Error() != "unknown exec subcommand unsupported" {
		t.Fatalf("Run error = %v", err)
	}
	if strings.Contains(err.Error(), "Go port") || strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("Run exposed stale Go-port wording: %v", err)
	}
}

func TestEffectiveProviderRejectsLegacyLocalProvider(t *testing.T) {
	_, err := effectiveProvider(&Request{
		Exec: cli.ExecOptions{Shared: cli.SharedOptions{OSSProvider: model.LegacyOllamaChatProviderID}},
	}, nil)
	if err == nil {
		t.Fatal("effectiveProvider returned nil error, want ollama-chat removed failure")
	}
	if !strings.Contains(err.Error(), model.OllamaChatProviderRemovedMessage) {
		t.Fatalf("error = %v", err)
	}
}

func TestEffectiveProviderUsesOSSProviderConfigForOSSMode(t *testing.T) {
	cfg := &config.Config{Values: map[string]any{"oss_provider": model.LMStudioOSSProviderID}}
	provider, err := effectiveProvider(&Request{
		Exec: cli.ExecOptions{Shared: cli.SharedOptions{OSS: true}},
	}, cfg)
	if err != nil {
		t.Fatalf("effectiveProvider returned error: %v", err)
	}
	if provider != model.LMStudioOSSProviderID {
		t.Fatalf("provider = %q, want lmstudio", provider)
	}

	provider, err = effectiveProvider(&Request{
		Exec: cli.ExecOptions{Shared: cli.SharedOptions{OSS: true, OSSProvider: model.OllamaOSSProviderID}},
	}, cfg)
	if err != nil {
		t.Fatalf("effectiveProvider explicit override returned error: %v", err)
	}
	if provider != model.OllamaOSSProviderID {
		t.Fatalf("explicit provider = %q, want ollama", provider)
	}

	cfg.Values["oss_provider"] = model.LegacyOllamaChatProviderID
	_, err = effectiveProvider(&Request{
		Exec: cli.ExecOptions{Shared: cli.SharedOptions{OSS: true}},
	}, cfg)
	if err == nil || !strings.Contains(err.Error(), model.OllamaChatProviderRemovedMessage) {
		t.Fatalf("legacy configured oss provider error = %v", err)
	}
}

func TestEffectiveReasoningEffortPrefersSharedOptions(t *testing.T) {
	cfg := &config.Config{Values: map[string]any{
		"model_reasoning_effort": "low",
	}}
	req := &Request{
		Root: cli.RootOptions{Shared: cli.SharedOptions{ModelReasoningEffort: "medium"}},
		Exec: cli.ExecOptions{Shared: cli.SharedOptions{ModelReasoningEffort: "high"}},
	}
	if got := effectiveReasoningEffort(req, cfg); got != "high" {
		t.Fatalf("reasoning effort = %q, want high", got)
	}

	req.Exec.Shared.ModelReasoningEffort = ""
	if got := effectiveReasoningEffort(req, cfg); got != "medium" {
		t.Fatalf("root reasoning effort = %q, want medium", got)
	}
}

func TestRunUsesInjectedAgentAndPersistsModelMetadata(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	runner := NewRunner(home)
	runner.Agent = &recordingAgent{message: "custom agent response"}
	runner.Now = func() time.Time { return fixedExecTime() }
	var stdout, stderr bytes.Buffer
	result, err := runner.Run(Request{
		Exec: cli.ExecOptions{
			Prompt: "hello",
			Shared: cli.SharedOptions{Model: "gpt-custom", OSSProvider: "lmstudio"},
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != "custom agent response" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	record := loadSessionRecord(t, home, result.ThreadID)
	if record.Metadata.Model != "gpt-custom" || record.Metadata.ModelProvider != "lmstudio" {
		t.Fatalf("session metadata = %#v", record.Metadata)
	}
	if !record.CreatedAt.Equal(fixedExecTime()) {
		t.Fatalf("CreatedAt = %s, want %s", record.CreatedAt, fixedExecTime())
	}
}

func TestRunLoadsProjectConfigFromCWD(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	project := t.TempDir()
	projectTrust := strings.ReplaceAll(filepath.Clean(project), `\`, `\\`)
	if err := os.WriteFile(config.ConfigPath(home), []byte("model_provider = \"lmstudio\"\n\n[projects.\""+projectTrust+"\"]\ntrust_level = \"trusted\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile user config returned error: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(project, ".codex"), 0o755); err != nil {
		t.Fatalf("MkdirAll project .codex returned error: %v", err)
	}
	if err := os.WriteFile(config.ProjectConfigPath(project), []byte("model = \"gpt-project\"\nmodel_provider = \"attacker\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile project config returned error: %v", err)
	}
	agent := &recordingAgent{message: "ok"}
	runner := NewRunner(home)
	runner.Agent = agent
	var stdout, stderr bytes.Buffer
	result, err := runner.Run(Request{
		Root: cli.RootOptions{Shared: cli.SharedOptions{CWD: project}},
		Exec: cli.ExecOptions{
			Prompt:    "hello",
			Ephemeral: true,
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result == nil {
		t.Fatal("Run result is nil")
	}
	if agent.request == nil || agent.request.Model != "gpt-project" || agent.request.ProviderID != "lmstudio" {
		t.Fatalf("agent request = %#v", agent.request)
	}
}

func TestRunLoadsProjectModelInstructionsFileFromCWD(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	project := t.TempDir()
	projectTrust := strings.ReplaceAll(filepath.Clean(project), `\`, `\\`)
	if err := os.WriteFile(config.ConfigPath(home), []byte("[projects.\""+projectTrust+"\"]\ntrust_level = \"trusted\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile user config returned error: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(project, ".codex"), 0o755); err != nil {
		t.Fatalf("MkdirAll project .codex returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, ".codex", "instructions.md"), []byte("\nproject instructions\n"), 0o600); err != nil {
		t.Fatalf("WriteFile instructions returned error: %v", err)
	}
	if err := os.WriteFile(config.ProjectConfigPath(project), []byte("model_instructions_file = \"instructions.md\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile project config returned error: %v", err)
	}
	agent := &recordingAgent{message: "ok"}
	runner := NewRunner(home)
	runner.Agent = agent
	var stdout, stderr bytes.Buffer
	_, err := runner.Run(Request{
		Root: cli.RootOptions{Shared: cli.SharedOptions{CWD: project}},
		Exec: cli.ExecOptions{
			Prompt:    "hello",
			Ephemeral: true,
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if agent.request == nil || agent.request.Instructions != "project instructions" {
		t.Fatalf("agent request = %#v", agent.request)
	}
}

func TestRunEphemeralSkipsSessionPersistence(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	var stdout, stderr bytes.Buffer
	result, err := NewLocalRunner(home).Run(Request{
		Exec: cli.ExecOptions{
			Prompt:    "hello",
			Ephemeral: true,
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.SessionPath != "" {
		t.Fatalf("SessionPath = %q, want empty", result.SessionPath)
	}
	if _, err := os.Stat(filepath.Join(home, "sessions")); !os.IsNotExist(err) {
		t.Fatalf("sessions stat error = %v, want not exist", err)
	}
}

func TestRunExecutesToolLoopWithInjectedRouter(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("echo")}, func(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
		return &tool.Output{Success: true, Body: "tool says hi"}, nil
	})); err != nil {
		t.Fatalf("register echo: %v", err)
	}
	agent := &toolLoopRecordingAgent{}
	runner := NewRunner(home)
	runner.Agent = agent
	runner.ToolRouter = tool.NewRouter(registry)
	runner.Now = fixedExecTime

	var stdout, stderr bytes.Buffer
	result, err := runner.Run(Request{
		Exec: cli.ExecOptions{
			Prompt:    "use echo",
			Ephemeral: true,
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.LastMessage != "done" || strings.TrimSpace(stdout.String()) != "done" {
		t.Fatalf("result=%#v stdout=%q", result, stdout.String())
	}
	if len(agent.requests) != 2 || len(agent.requests[1].InputItems) != 5 {
		t.Fatalf("agent requests = %#v", agent.requests)
	}
	if len(agent.requests[0].Tools) != 1 || len(agent.requests[1].Tools) != 1 {
		t.Fatalf("agent tools = %#v / %#v", agent.requests[0].Tools, agent.requests[1].Tools)
	}
}

func TestRunPersistsToolExecutions(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("echo")}, func(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
		return &tool.Output{Success: true, Body: "tool says hi"}, nil
	})); err != nil {
		t.Fatalf("register echo: %v", err)
	}
	runner := NewRunner(home)
	runner.Agent = &toolLoopRecordingAgent{}
	runner.ToolRouter = tool.NewRouter(registry)
	runner.Now = fixedExecTime

	var stdout, stderr bytes.Buffer
	result, err := runner.Run(Request{Exec: cli.ExecOptions{Prompt: "use echo"}}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	record := loadSessionRecord(t, home, result.ThreadID)
	if got := itemTypes(record.Items); !containsAll(got, []string{"message", "function_call", "tool_output", "agent_message"}) {
		t.Fatalf("item types = %#v", got)
	}
	var output session.Item
	for i := range record.Items {
		if record.Items[i].Type == "tool_output" {
			output = record.Items[i]
			break
		}
	}
	if output.Text != "tool says hi" || output.Metadata["toolName"] != "echo" || output.Metadata["success"] != true {
		t.Fatalf("tool output item = %#v", output)
	}
	if output.Metadata["startedAtMs"] == nil || output.Metadata["completedAtMs"] == nil || output.Metadata["durationMs"] == nil {
		t.Fatalf("tool timing metadata missing = %#v", output.Metadata)
	}
}

func TestSessionItemsForTurnPersistsAllModelResponses(t *testing.T) {
	createdAt := fixedExecTime()
	result := &turn.AgentLoopResult{
		Responses: []*model.AgentResponse{{
			ResponseID:  "resp-first",
			RequestID:   "req-first",
			ServerModel: "gpt-server-first",
			Items: []model.AgentItem{{
				ID:   "reasoning-1",
				Type: "reasoning",
				Data: map[string]any{"summary": []string{"thinking"}},
			}, {
				ID:     "call-1",
				Type:   "function_call",
				Name:   "echo",
				CallID: "call-1",
			}, {
				ID:     "call-2",
				Type:   "function_call",
				Name:   "echo",
				CallID: "call-2",
			}},
		}, {
			ResponseID: "resp-final",
			Items: []model.AgentItem{{
				ID:   "msg-final",
				Type: "agent_message",
				Text: "done",
			}},
		}},
		ToolExecutions: []turn.ToolExecutionResult{{
			Invocation: &tool.Invocation{
				CallID:   "call-1",
				ToolName: tool.PlainName("echo"),
				Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{}`},
			},
			Output: &tool.Output{CallID: "call-1", Success: true, Body: "tool ok"},
		}, {
			Invocation: &tool.Invocation{
				CallID:   "call-2",
				ToolName: tool.PlainName("echo"),
				Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{}`},
			},
			Output: &tool.Output{CallID: "call-2", Success: true, Body: "tool ok 2"},
		}},
	}
	result.Response = result.Responses[1]
	items := sessionItemsForTurn("turn-exec", "hello", nil, result, createdAt, nil)

	reasoning := execSessionItemByID(items, "reasoning-1")
	if reasoning == nil || reasoning.ResponseID != "resp-first" {
		t.Fatalf("reasoning item = %#v", reasoning)
	}
	if reasoning.Metadata["requestId"] != "req-first" || reasoning.Metadata["server_model"] != "gpt-server-first" {
		t.Fatalf("reasoning metadata = %#v", reasoning.Metadata)
	}
	final := execSessionItemByID(items, "msg-final")
	if final == nil || final.ResponseID != "resp-final" {
		t.Fatalf("final item = %#v", final)
	}
	if execSessionItemByID(items, "call-1") != nil {
		t.Fatalf("tool call response item should be represented by tool execution, items = %#v", items)
	}
	reasoningIndex := execSessionItemIndexByID(items, "reasoning-1")
	call1Index := execSessionItemIndexByTypeAndCallID(items, "function_call", "call-1")
	call2Index := execSessionItemIndexByTypeAndCallID(items, "function_call", "call-2")
	output1Index := execSessionItemIndexByTypeAndCallID(items, "tool_output", "call-1")
	output2Index := execSessionItemIndexByTypeAndCallID(items, "tool_output", "call-2")
	finalIndex := execSessionItemIndexByID(items, "msg-final")
	if reasoningIndex < 0 || call1Index < 0 || call2Index < 0 || output1Index < 0 || output2Index < 0 || finalIndex < 0 ||
		!(reasoningIndex < call1Index && call1Index < call2Index && call2Index < output1Index && output1Index < output2Index && output2Index < finalIndex) {
		t.Fatalf("item order = %#v", items)
	}
	if items[call1Index].Metadata["request_id"] != "req-first" || items[output1Index].Metadata["requestId"] != "req-first" {
		t.Fatalf("tool response metadata call=%#v output=%#v", items[call1Index].Metadata, items[output1Index].Metadata)
	}
}

func TestEmitFinalEventsFromAgentResultPreservesLoopOrder(t *testing.T) {
	result := &turn.AgentLoopResult{
		Responses: []*model.AgentResponse{{
			ResponseID: "resp-first",
			Items: []model.AgentItem{{
				ID:   "reasoning-1",
				Type: "reasoning",
				Data: map[string]any{"summary": []string{"thinking"}},
			}, {
				ID:     "call-1",
				Type:   "function_call",
				Name:   "echo",
				CallID: "call-1",
			}, {
				ID:     "call-2",
				Type:   "function_call",
				Name:   "echo",
				CallID: "call-2",
			}},
		}, {
			ResponseID: "resp-final",
			Items: []model.AgentItem{{
				ID:   "msg-final",
				Type: "agent_message",
				Text: "done",
			}},
		}},
		ToolExecutions: []turn.ToolExecutionResult{{
			Invocation: &tool.Invocation{
				CallID:   "call-1",
				ToolName: tool.PlainName("echo"),
				Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{}`},
			},
			Output: &tool.Output{CallID: "call-1", Success: true, Body: "tool ok"},
		}, {
			Invocation: &tool.Invocation{
				CallID:   "call-2",
				ToolName: tool.PlainName("echo"),
				Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{}`},
			},
			Output: &tool.Output{CallID: "call-2", Success: true, Body: "tool ok 2"},
		}},
		Usage: model.AgentUsage{InputTokens: 1, OutputTokens: 2},
	}
	result.Response = result.Responses[1]
	sink := newExecEventSink(nil, false)

	if err := emitFinalEventsFromAgentResult(sink, result); err != nil {
		t.Fatalf("emitFinalEventsFromAgentResult() error = %v", err)
	}
	events := sink.Events()
	reasoningIndex := execEventItemIndex(events, "reasoning-1")
	call1Index := execEventItemIndex(events, "tool-call-call-1")
	call2Index := execEventItemIndex(events, "tool-call-call-2")
	output1Index := execEventItemIndex(events, "tool-output-call-1")
	output2Index := execEventItemIndex(events, "tool-output-call-2")
	finalIndex := execEventItemIndex(events, "msg-final")
	turnCompletedIndex := execEventTypeIndex(events, "turn.completed")
	if reasoningIndex < 0 || call1Index < 0 || call2Index < 0 || output1Index < 0 || output2Index < 0 || finalIndex < 0 || turnCompletedIndex < 0 ||
		!(reasoningIndex < call1Index && call1Index < call2Index && call2Index < output1Index && output1Index < output2Index && output2Index < finalIndex && finalIndex < turnCompletedIndex) {
		t.Fatalf("events = %#v", events)
	}
	if got := events[reasoningIndex].Item.Text; got != "thinking" {
		t.Fatalf("reasoning text = %q", got)
	}
}

func TestEmitFinalEventsDropsReasoningWithoutSummary(t *testing.T) {
	result := &turn.AgentLoopResult{
		Response: &model.AgentResponse{
			Items: []model.AgentItem{{
				ID:   "reasoning-empty",
				Type: "reasoning",
				Text: "raw reasoning should not be emitted",
				Data: map[string]any{"content": []string{"raw reasoning should not be emitted"}},
			}, {
				ID:   "reasoning-blank",
				Type: "reasoning",
				Data: map[string]any{"summary": []string{"  "}},
			}, {
				ID:   "msg-final",
				Type: "agent_message",
				Text: "done",
			}},
		},
		Usage: model.AgentUsage{InputTokens: 1, OutputTokens: 2},
	}
	sink := newExecEventSink(nil, false)
	if err := emitFinalEventsFromAgentResult(sink, result); err != nil {
		t.Fatalf("emitFinalEventsFromAgentResult() error = %v", err)
	}
	events := sink.Events()
	if execEventItemIndex(events, "reasoning-empty") >= 0 || execEventItemIndex(events, "reasoning-blank") >= 0 {
		t.Fatalf("empty reasoning should not emit item events: %#v", events)
	}
	if execEventItemIndex(events, "msg-final") < 0 {
		t.Fatalf("final message missing from events: %#v", events)
	}
}

func TestEmitFinalEventsMapsUpdatePlanToTodoList(t *testing.T) {
	result := &turn.AgentLoopResult{
		Response: &model.AgentResponse{},
		ToolExecutions: []turn.ToolExecutionResult{{
			Invocation: &tool.Invocation{
				CallID:   "plan-1",
				ToolName: tool.PlainName("update_plan"),
				Payload: tool.Payload{
					Kind:      tool.PayloadFunction,
					Arguments: `{"plan":[{"step":"step one","status":"in_progress"},{"step":"step two","status":"completed"}]}`,
				},
			},
			Output: &tool.Output{
				CallID:   "plan-1",
				ToolName: tool.PlainName("update_plan"),
				Success:  true,
				Data: map[string]any{
					"planUpdate": true,
					"plan": []tool.PlanItem{{
						Step:   "step one",
						Status: tool.PlanInProgress,
					}, {
						Step:   "step two",
						Status: tool.PlanCompleted,
					}},
				},
			},
		}},
		Usage: model.AgentUsage{InputTokens: 1, OutputTokens: 2},
	}
	sink := newExecEventSink(nil, false)
	if err := emitFinalEventsFromAgentResult(sink, result); err != nil {
		t.Fatalf("emitFinalEventsFromAgentResult() error = %v", err)
	}
	events := sink.Events()
	todoIndex := execEventItemIndex(events, "todo-list-plan-1")
	if todoIndex < 0 {
		t.Fatalf("todo list event missing: %#v", events)
	}
	todo := events[todoIndex].Item
	if todo.Type != "todo_list" || len(todo.Items) != 2 || todo.Items[0].Text != "step one" || todo.Items[0].Completed || !todo.Items[1].Completed {
		t.Fatalf("todo list item = %#v", todo)
	}
	if execEventItemIndex(events, "tool-call-plan-1") >= 0 || execEventItemIndex(events, "tool-output-plan-1") >= 0 {
		t.Fatalf("update_plan should not emit generic tool events: %#v", events)
	}
}

func TestExecStreamEventCollectorBuildsProtocolEvents(t *testing.T) {
	collector := &execStreamEventCollector{}
	collector.Handle(&model.ResponsesStreamEvent{
		Kind: model.ResponsesStreamEventOutputAdded,
		Item: &model.AgentItem{ID: "msg-1", Type: "agent_message"},
	})
	collector.Handle(&model.ResponsesStreamEvent{
		Kind:   model.ResponsesStreamEventOutputText,
		ItemID: "msg-1",
		Delta:  "hello ",
	})
	collector.Handle(&model.ResponsesStreamEvent{
		Kind:   model.ResponsesStreamEventToolInputDelta,
		ItemID: "call-1",
		CallID: "call-1",
		Delta:  "patch",
	})
	events := collector.Events()
	if len(events) != 3 {
		t.Fatalf("events = %#v", events)
	}
	if events[0].Type != "item.started" || events[0].Item.ID != "msg-1" {
		t.Fatalf("started event = %#v", events[0])
	}
	if events[1].Type != "item.delta" || events[1].Delta.Text != "hello " {
		t.Fatalf("text delta = %#v", events[1])
	}
	if events[2].Type != "item.delta" || events[2].Delta.Input != "patch" || events[2].Delta.CallID != "call-1" {
		t.Fatalf("tool delta = %#v", events[2])
	}
}

func TestExecStreamEventCollectorBuildsRateLimitProtocolEvent(t *testing.T) {
	minutes := int64(5 * 60)
	reset := int64(1710000000)
	balance := "0"
	collector := &execStreamEventCollector{}
	collector.Handle(&model.ResponsesStreamEvent{
		Kind: model.ResponsesStreamEventRateLimits,
		RateLimit: &model.ResponsesRateLimitSnapshot{
			LimitID:   "codex",
			LimitName: "Codex",
			Primary: &model.ResponsesRateLimitWindow{
				UsedPercent:        90,
				WindowDurationMins: &minutes,
				ResetsAt:           &reset,
			},
			Credits: &model.ResponsesCreditsSnapshot{
				HasCredits: true,
				Unlimited:  false,
				Balance:    &balance,
			},
			PlanType:             "plus",
			RateLimitReachedType: "primary",
		},
	})

	events := collector.Events()
	if len(events) != 1 {
		t.Fatalf("events = %#v", events)
	}
	event := events[0]
	if event.Type != "response.rate_limits" || event.RateLimit == nil {
		t.Fatalf("rate limit event = %#v", event)
	}
	if event.RateLimit.LimitID != "codex" || event.RateLimit.Primary == nil || event.RateLimit.Primary.UsedPercent != 90 {
		t.Fatalf("rate limit snapshot = %#v", event.RateLimit)
	}
	if event.RateLimit.Primary.WindowDurationMins == nil || *event.RateLimit.Primary.WindowDurationMins != minutes {
		t.Fatalf("rate limit window minutes = %#v", event.RateLimit.Primary)
	}
	if event.RateLimit.Primary.ResetsAt == nil || *event.RateLimit.Primary.ResetsAt != reset {
		t.Fatalf("rate limit reset = %#v", event.RateLimit.Primary)
	}
	if event.RateLimit.Credits == nil || event.RateLimit.Credits.Balance == nil || *event.RateLimit.Credits.Balance != balance {
		t.Fatalf("credits = %#v", event.RateLimit.Credits)
	}
}

func TestExecStreamEventCollectorSkipsReasoningStarted(t *testing.T) {
	collector := &execStreamEventCollector{}
	collector.Handle(&model.ResponsesStreamEvent{
		Kind: model.ResponsesStreamEventOutputAdded,
		Item: &model.AgentItem{
			ID:   "reasoning-1",
			Type: "reasoning",
			Data: map[string]any{"summary": []string{"thinking"}},
		},
	})
	if events := collector.Events(); len(events) != 0 {
		t.Fatalf("reasoning started events = %#v", events)
	}
}

func TestEventFromToolOutputExecutionCarriesOutputDataMetadata(t *testing.T) {
	event, ok := eventFromToolOutputExecution(&turn.ToolExecutionResult{
		Invocation: &tool.Invocation{CallID: "call-approval", ToolName: tool.PlainName("exec_command")},
		Output: &tool.Output{
			Success: false,
			Body:    "Approval required before running command.",
			Data: map[string]any{
				"approval_required": true,
				"reason":            "command requested sandbox permissions",
			},
		},
	})
	if !ok || event.Item == nil {
		t.Fatalf("event = %#v ok=%v", event, ok)
	}
	if event.Item.Metadata["approval_required"] != true || event.Item.Metadata["reason"] != "command requested sandbox permissions" {
		t.Fatalf("metadata = %#v", event.Item.Metadata)
	}
}

func TestRunJSONStreamsResponsesEventsImmediately(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	deltaSent := make(chan struct{})
	releaseCompletion := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		writeExecResponseSSE(w, `{"type":"response.created","response":{"id":"resp-1"}}`)
		writeExecResponseSSE(w, `{"type":"response.output_item.added","item":{"id":"msg-1","type":"message","role":"assistant","content":[{"type":"output_text","text":""}]}}`)
		writeExecResponseSSE(w, `{"type":"response.output_text.delta","item_id":"msg-1","delta":"hello "}`)
		if flusher != nil {
			flusher.Flush()
		}
		close(deltaSent)
		<-releaseCompletion
		writeExecResponseSSE(w, `{"type":"response.output_text.delta","item_id":"msg-1","delta":"world"}`)
		writeExecResponseSSE(w, `{"type":"response.output_item.done","item":{"id":"msg-1","type":"message","role":"assistant","content":[{"type":"output_text","text":"hello world"}]}}`)
		writeExecResponseSSE(w, `{"type":"response.completed","response":{"id":"resp-1","usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}}`)
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer server.Close()

	runner := NewRunner(home)
	runner.Agent = model.NewResponsesAgentRunner(&model.ResponsesAgentOptions{
		Provider: &model.APIProvider{BaseURL: server.URL + "/v1"},
	})
	stdout := &synchronizedBuffer{}
	var stderr bytes.Buffer
	done := make(chan execRunOutcome, 1)
	go func() {
		result, err := runner.Run(Request{
			Exec: cli.ExecOptions{
				Prompt:    "hello",
				JSON:      true,
				Ephemeral: true,
			},
		}, strings.NewReader(""), stdout, &stderr)
		done <- execRunOutcome{result: result, err: err}
	}()

	select {
	case <-deltaSent:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not send delta")
	}
	if !waitForBufferContains(stdout, `"type":"item.delta"`, `"hello "`) {
		t.Fatalf("stdout before completion = %q", stdout.String())
	}
	close(releaseCompletion)

	select {
	case outcome := <-done:
		if outcome.err != nil {
			t.Fatalf("Run returned error: %v", outcome.err)
		}
		if outcome.result == nil || outcome.result.LastMessage != "hello world" {
			t.Fatalf("result = %#v", outcome.result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not finish")
	}
	if !strings.Contains(stdout.String(), `"type":"turn.completed"`) ||
		strings.Contains(stdout.String(), `"total_tokens"`) ||
		!strings.Contains(stdout.String(), `"output_tokens":2`) {
		t.Fatalf("stdout after completion = %q", stdout.String())
	}
}

func TestRunJSONEmitsErrorEventWhenTurnFails(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	runner := NewRunner(home)
	runner.Agent = &failingAgent{err: errors.New("model exploded")}
	lastMessage := filepath.Join(t.TempDir(), "last-message.txt")
	if err := os.WriteFile(lastMessage, []byte("keep existing contents"), 0o600); err != nil {
		t.Fatalf("seed last message file: %v", err)
	}
	var stdout, stderr bytes.Buffer
	_, err := runner.Run(Request{
		Exec: cli.ExecOptions{
			Prompt:          "hello",
			JSON:            true,
			Ephemeral:       true,
			LastMessageFile: lastMessage,
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "model exploded") {
		t.Fatalf("Run error = %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, `"type":"thread.started"`) || !strings.Contains(output, `"type":"turn.started"`) {
		t.Fatalf("stdout missing lifecycle events: %q", output)
	}
	if !strings.Contains(output, `"type":"error"`) || !strings.Contains(output, `"message":"model exploded"`) {
		t.Fatalf("stdout missing error event: %q", output)
	}
	if !strings.Contains(output, `"type":"turn.failed"`) {
		t.Fatalf("stdout missing Rust turn.failed event: %q", output)
	}
	data, readErr := os.ReadFile(lastMessage)
	if readErr != nil {
		t.Fatalf("read last message file: %v", readErr)
	}
	if string(data) != "keep existing contents" {
		t.Fatalf("last message file = %q, want unchanged after failed turn", data)
	}
}

type recordingAgent struct {
	message string
	usage   model.AgentUsage
	request *model.AgentRequest
}

func agentRequestInputItemsHaveText(request *model.AgentRequest, text string) bool {
	return agentRequestInputItemWithText(request, text) != nil
}

func agentRequestInputItemsContainText(request *model.AgentRequest, text string) bool {
	if request == nil {
		return false
	}
	for _, raw := range request.InputItems {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		content, ok := item["content"].([]map[string]any)
		if !ok {
			continue
		}
		for i := range content {
			if strings.Contains(fmt.Sprint(content[i]["text"]), text) {
				return true
			}
		}
	}
	return false
}

func agentRequestInputItemWithText(request *model.AgentRequest, text string) map[string]any {
	if request == nil {
		return nil
	}
	for _, raw := range request.InputItems {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		content, ok := item["content"].([]map[string]any)
		if !ok {
			continue
		}
		for i := range content {
			if content[i]["text"] == text {
				return item
			}
		}
	}
	return nil
}

func (a *recordingAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.request = request
	usage := a.usage
	if usage == (model.AgentUsage{}) {
		usage = model.AgentUsage{InputTokens: 2, OutputTokens: 3}
	}
	return &model.AgentResponse{
		Message: a.message,
		Items: []model.AgentItem{{
			ID:   "custom-item",
			Type: "agent_message",
			Text: a.message,
		}},
		Usage:      usage,
		Model:      request.Model,
		ProviderID: request.ProviderID,
	}, nil
}

type failingAgent struct {
	err error
}

func (a *failingAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if a == nil || a.err == nil {
		return nil, errors.New("agent failed")
	}
	return nil, a.err
}

type toolLoopRecordingAgent struct {
	requests []model.AgentRequest
}

func (a *toolLoopRecordingAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.requests = append(a.requests, *request)
	if len(a.requests) == 1 {
		return &model.AgentResponse{
			Items: []model.AgentItem{{
				ID:        "call-1",
				Type:      "function_call",
				Name:      "echo",
				CallID:    "call-1",
				Arguments: `{}`,
			}},
			Model:      request.Model,
			ProviderID: request.ProviderID,
		}, nil
	}
	return &model.AgentResponse{
		Message: "done",
		Items: []model.AgentItem{{
			ID:   "msg-1",
			Type: "agent_message",
			Text: "done",
		}},
		Model:      request.Model,
		ProviderID: request.ProviderID,
	}, nil
}

func loadSessionRecord(t *testing.T, home string, threadID string) *session.Record {
	t.Helper()
	record, err := session.NewStore(filepath.Join(home, "sessions")).Load(session.ThreadID(threadID))
	if err != nil {
		t.Fatalf("Load session record returned error: %v", err)
	}
	return record
}

func itemTypes(items []session.Item) []string {
	out := make([]string, 0, len(items))
	for i := range items {
		out = append(out, items[i].Type)
	}
	return out
}

func execSessionItemByID(items []session.Item, id string) *session.Item {
	for i := range items {
		if items[i].ID == id {
			return &items[i]
		}
	}
	return nil
}

func execSessionItemIndexByID(items []session.Item, id string) int {
	for i := range items {
		if items[i].ID == id {
			return i
		}
	}
	return -1
}

func execSessionItemIndexByType(items []session.Item, itemType string) int {
	for i := range items {
		if items[i].Type == itemType {
			return i
		}
	}
	return -1
}

func execSessionItemIndexByTypeAndCallID(items []session.Item, itemType string, callID string) int {
	for i := range items {
		if items[i].Type == itemType && items[i].CallID == callID {
			return i
		}
	}
	return -1
}

func execEventItemIndex(events []protocol.ThreadEvent, itemID string) int {
	for i := range events {
		if events[i].Item != nil && events[i].Item.ID == itemID {
			return i
		}
	}
	return -1
}

func execEventTypeIndex(events []protocol.ThreadEvent, eventType string) int {
	for i := range events {
		if events[i].Type == eventType {
			return i
		}
	}
	return -1
}

func containsAll(values []string, required []string) bool {
	seen := map[string]bool{}
	for _, value := range values {
		seen[value] = true
	}
	for _, value := range required {
		if !seen[value] {
			return false
		}
	}
	return true
}

type execRunOutcome struct {
	result *Result
	err    error
}

type synchronizedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *synchronizedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(data)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func waitForBufferContains(buffer *synchronizedBuffer, parts ...string) bool {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		text := buffer.String()
		ok := true
		for _, part := range parts {
			if !strings.Contains(text, part) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func writeExecResponseSSE(w http.ResponseWriter, payload string) {
	_, _ = w.Write([]byte("data: " + payload + "\n\n"))
}

func fixedExecTime() time.Time {
	return time.Date(2026, 6, 29, 1, 2, 3, 0, time.UTC)
}
