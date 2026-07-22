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

	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
	"codex_go/mcp"
	"codex_go/model"
	"codex_go/protocol"
	"codex_go/review"
	"codex_go/rollout"
	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/tool"
	"codex_go/turn"
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

func TestRunJSONRustPromptStdinGolden(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	agent := &recordingAgent{message: "fixture hello"}
	runner := NewRunner(home)
	runner.Agent = agent

	var stdout, stderr bytes.Buffer
	result, err := runner.Run(Request{
		Exec: cli.ExecOptions{
			Prompt: "Summarize this concisely",
			JSON:   true,
		},
	}, strings.NewReader("my output\n"), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v stderr=%q stdout=%q", err, stderr.String(), stdout.String())
	}
	wantPrompt := "Summarize this concisely\n\n<stdin>\nmy output\n</stdin>"
	if result.Prompt != wantPrompt || agent.request == nil || agent.request.Prompt != wantPrompt {
		t.Fatalf("prompt result=%q agent=%#v want %q", result.Prompt, agent.request, wantPrompt)
	}
	events := decodeExecJSONLines(t, stdout.String())
	if got := execEventTypes(events); strings.Join(got, ",") != "thread.started,turn.started,item.completed,turn.completed" {
		t.Fatalf("event types = %#v stdout=%q", got, stdout.String())
	}
	if events[2].Item == nil || events[2].Item.Type != "agent_message" || events[2].Item.Text != "fixture hello" {
		t.Fatalf("agent message event = %#v", events[2])
	}
	if events[3].Usage == nil || events[3].Usage.InputTokens != 2 || events[3].Usage.OutputTokens != 3 {
		t.Fatalf("turn completed usage = %#v", events[3].Usage)
	}
}

func TestRunJSONRustToolCallGolden(t *testing.T) {
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

	var stdout, stderr bytes.Buffer
	_, err := runner.Run(Request{
		Exec: cli.ExecOptions{
			Prompt: "use echo",
			JSON:   true,
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v stderr=%q stdout=%q", err, stderr.String(), stdout.String())
	}
	events := decodeExecJSONLines(t, stdout.String())
	want := []string{"thread.started", "turn.started", "item.started", "item.completed", "item.completed", "item.completed", "turn.completed"}
	if got := execEventTypes(events); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("event types = %#v, want %#v stdout=%q", got, want, stdout.String())
	}
	if events[2].Item == nil || events[2].Item.Type != "tool_call" || events[2].Item.CallID != "call-1" {
		t.Fatalf("tool call started = %#v", events[2])
	}
	if events[4].Item == nil || events[4].Item.Type != "tool_output" || events[4].Item.Output != "tool says hi" {
		t.Fatalf("tool output completed = %#v", events[4])
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
	runner := NewRunner(home)
	runner.MCPService = mcp.NewMCPService(&mcp.RuntimeConfig{Servers: map[string]mcp.ServerRegistration{}})
	_, err := runner.Run(Request{
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
	for _, want := range []string{
		"OpenAI Codex v",
		"workdir:",
		"approval: never",
		"sandbox: read-only",
		"session id:",
		"user\nhello",
		"tokens used\n1,100\n",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr missing %q: %q", want, stderr.String())
		}
	}
}

func TestRunHumanRecoversFinalMessageFromAgentItemsLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	lastMessage := filepath.Join(t.TempDir(), "last.txt")
	runner := NewRunner(home)
	runner.Agent = &staticResponseAgent{response: &model.AgentResponse{
		Items: []model.AgentItem{{
			ID:   "msg-1",
			Type: "agent_message",
			Text: "item final",
		}},
		Usage: model.AgentUsage{InputTokens: 1, OutputTokens: 2},
	}}
	var stdout, stderr bytes.Buffer
	result, err := runner.Run(Request{
		Exec: cli.ExecOptions{Prompt: "hello", LastMessageFile: lastMessage},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v stderr=%q stdout=%q", err, stderr.String(), stdout.String())
	}
	if result.LastMessage != "item final" || stdout.String() != "item final\n" {
		t.Fatalf("result=%#v stdout=%q", result, stdout.String())
	}
	data, err := os.ReadFile(lastMessage)
	if err != nil {
		t.Fatalf("ReadFile last message returned error: %v", err)
	}
	if string(data) != "item final" {
		t.Fatalf("last message file = %q", string(data))
	}
}

func TestRunHumanMissingFinalMessageWritesEmptyLastMessageLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	lastMessage := filepath.Join(t.TempDir(), "last.txt")
	runner := NewRunner(home)
	runner.Agent = &staticResponseAgent{response: &model.AgentResponse{}}
	var stdout, stderr bytes.Buffer
	result, err := runner.Run(Request{
		Exec: cli.ExecOptions{Prompt: "hello", LastMessageFile: lastMessage},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v stderr=%q stdout=%q", err, stderr.String(), stdout.String())
	}
	if result.LastMessage != "" || stdout.String() != "" {
		t.Fatalf("result=%#v stdout=%q", result, stdout.String())
	}
	data, err := os.ReadFile(lastMessage)
	if err != nil {
		t.Fatalf("ReadFile last message returned error: %v", err)
	}
	if string(data) != "" {
		t.Fatalf("last message file = %q, want empty", string(data))
	}
	if !strings.Contains(stderr.String(), "Warning: no last agent message; wrote empty content to "+lastMessage) {
		t.Fatalf("stderr = %q", stderr.String())
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
	if err := os.WriteFile(imagePath, minimalPNGBytes(), 0o600); err != nil {
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

func TestLocalImageInputResolvesRelativePathFromRequestCWD(t *testing.T) {
	cwd := t.TempDir()
	imageDir := filepath.Join(filepath.Dir(cwd), "outside-images")
	if err := os.MkdirAll(imageDir, 0o700); err != nil {
		t.Fatalf("MkdirAll image dir: %v", err)
	}
	imagePath := filepath.Join(imageDir, "diagram.png")
	if err := os.WriteFile(imagePath, minimalPNGBytes(), 0o600); err != nil {
		t.Fatalf("WriteFile image: %v", err)
	}
	relativePath, err := filepath.Rel(cwd, imagePath)
	if err != nil {
		t.Fatalf("Rel image path: %v", err)
	}

	content := localImageInputContentBlocks(relativePath, cwd, "high", 1)
	if len(content) != 3 || content[1]["type"] != "input_image" {
		t.Fatalf("content = %#v", content)
	}
	imageURL := fmt.Sprint(content[1]["image_url"])
	if !strings.HasPrefix(imageURL, "data:image/png;base64,") {
		t.Fatalf("image URL = %q", imageURL)
	}
}

func TestLocalImageInputRejectsInvalidImageBeforeRequest(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, "invalid.png")
	if err := os.WriteFile(path, []byte("not an image"), 0o600); err != nil {
		t.Fatalf("WriteFile invalid image: %v", err)
	}
	content := localImageInputContentBlocks("invalid.png", cwd, "high", 1)
	if len(content) != 1 || content[0]["type"] != "input_text" || !strings.Contains(fmt.Sprint(content[0]["text"]), "could not decode") {
		t.Fatalf("content = %#v", content)
	}
}

func minimalPNGBytes() []byte {
	return []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
		0x89, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x44, 0x41,
		0x54, 0x08, 0xd7, 0x63, 0xf8, 0xcf, 0xc0, 0xf0, 0x1f,
		0x00, 0x05, 0x00, 0x01, 0xff, 0x89, 0x99, 0x3d, 0x1d,
		0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44,
		0xae, 0x42, 0x60, 0x82,
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

func TestRunIncludesAdditionalInstructionsAndInputItems(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	agent := &recordingAgent{message: "done"}
	runner := NewRunner(home)
	runner.Agent = agent

	var stdout, stderr bytes.Buffer
	_, err := runner.Run(Request{
		Exec:                   cli.ExecOptions{Prompt: "use it", Ephemeral: true},
		AdditionalInstructions: "## Skills\n- imagegen: Generate images",
		AdditionalInputItems: []any{map[string]any{
			"type": "message",
			"role": "user",
			"content": []map[string]any{{
				"type": "input_text",
				"text": "<skill>\n<name>imagegen</name>\nUse images.\n</skill>",
			}},
		}},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.HasPrefix(agent.request.Instructions, "## Skills\n- imagegen") {
		t.Fatalf("instructions = %q", agent.request.Instructions)
	}
	if !agentRequestInputItemsContainText(agent.request, "<name>imagegen</name>") {
		t.Fatalf("input items = %#v", agent.request.InputItems)
	}
}

func TestRunPersistsAdditionalSkillInputItemsForHistory(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	agent := &recordingAgent{message: "done"}
	runner := NewRunner(home)
	runner.Agent = agent

	skillItem := map[string]any{
		"type": "message",
		"role": "user",
		"content": []map[string]any{{
			"type": "input_text",
			"text": "<skill>\n<name>imagegen</name>\nUse hosted image generation.\n</skill>",
		}},
	}
	var stdout, stderr bytes.Buffer
	result, err := runner.Run(Request{
		Exec:                 cli.ExecOptions{Prompt: "use it"},
		AdditionalInputItems: []any{skillItem},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	store := session.NewStore(filepath.Join(home, "sessions"))
	record, err := store.Read(session.ThreadID(result.ThreadID), true, true)
	if err != nil {
		t.Fatalf("Read session error = %v", err)
	}
	var found bool
	for i := range record.Items {
		if record.Items[i].Data["kind"] == execSkillInstructionsKind && strings.Contains(record.Items[i].Text, "<name>imagegen</name>") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("record missing persisted skill instructions: %#v", record.Items)
	}
	if !inputItemsContainText(session.InputItemsFromRecord(record, &session.HistoryBuildOptions{IncludeToolOutputs: true}), "Use hosted image generation.") {
		t.Fatalf("history input items missing persisted skill: %#v", record.Items)
	}
}

func TestRunExposesStandaloneImageGenerationByDefaultLikeRust(t *testing.T) {
	t.Setenv(auth.OpenAIAPIKeyEnv, "")
	t.Setenv(auth.CodexAPIKeyEnv, "")
	t.Setenv(auth.CodexAccessTokenEnv, "")
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromChatGPTAuthTokens("access-token", "account-1", nil)); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	if err := os.WriteFile(config.ConfigPath(home), []byte(`
model = "gpt-5.5"
`), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}
	agent := &recordingAgent{message: "ok"}
	runner := NewRunner(home)
	runner.Agent = agent
	runner.MCPService = mcp.NewMCPService(&mcp.RuntimeConfig{Servers: map[string]mcp.ServerRegistration{}})

	var stdout, stderr bytes.Buffer
	_, err := runner.Run(Request{
		Exec: cli.ExecOptions{
			Prompt:    "draw a square",
			Ephemeral: true,
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v stderr=%q stdout=%q", err, stderr.String(), stdout.String())
	}
	if agent.request == nil {
		t.Fatal("agent request is nil")
	}
	if !agentRequestToolsContainNamespaceFunction(agent.request, turn.ImageGenerationNamespace, turn.ImageGenerationToolName) {
		t.Fatalf("default exec runtime should expose standalone image generation namespace like Rust: %#v", agent.request.Tools)
	}
	if agentRequestToolsContainType(agent.request, "image_generation") {
		t.Fatalf("standalone image generation should suppress hosted image_generation: %#v", agent.request.Tools)
	}
}

func TestRunResponsesRequestIncludesStandaloneImageGenerationByDefaultLikeRust(t *testing.T) {
	t.Setenv(auth.OpenAIAPIKeyEnv, "")
	t.Setenv(auth.CodexAPIKeyEnv, "")
	t.Setenv(auth.CodexAccessTokenEnv, "")
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromChatGPTAuthTokens("access-token", "account-1", nil)); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	var recordedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("request path = %q, want /v1/responses", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&recordedBody); err != nil {
			t.Fatalf("Decode request body returned error: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		writeExecResponseSSE(w, `{"type":"response.created","response":{"id":"resp-image-tools"}}`)
		writeExecResponseSSE(w, `{"type":"response.output_item.added","item":{"id":"msg-image-tools","type":"message","role":"assistant","content":[{"type":"output_text","text":""}]}}`)
		writeExecResponseSSE(w, `{"type":"response.output_text.delta","item_id":"msg-image-tools","delta":"ready"}`)
		writeExecResponseSSE(w, `{"type":"response.output_item.done","item":{"id":"msg-image-tools","type":"message","role":"assistant","content":[{"type":"output_text","text":"ready"}]}}`)
		writeExecResponseSSE(w, `{"type":"response.completed","response":{"id":"resp-image-tools","model":"gpt-5.5","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`)
	}))
	defer server.Close()
	configBody := "model = \"gpt-5.5\"\nopenai_base_url = \"" + server.URL + "/v1\"\n\n[features]\nenable_request_compression = false\n"
	if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}

	var stdout, stderr bytes.Buffer
	runner := NewRunner(home)
	runner.MCPService = mcp.NewMCPService(&mcp.RuntimeConfig{Servers: map[string]mcp.ServerRegistration{}})
	_, err := runner.Run(Request{
		Exec: cli.ExecOptions{
			Prompt:    "draw a square",
			JSON:      true,
			Ephemeral: true,
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v stderr=%q stdout=%q", err, stderr.String(), stdout.String())
	}
	tools, ok := recordedBody["tools"].([]any)
	if !ok {
		t.Fatalf("tools = %#v, body = %#v", recordedBody["tools"], recordedBody)
	}
	if !responseToolsContainNamespaceFunctionForExecTest(tools, turn.ImageGenerationNamespace, turn.ImageGenerationToolName) {
		t.Fatalf("tools missing standalone image generation namespace: %#v", tools)
	}
	if responseToolsContainTypeForExecTest(tools, "image_generation") {
		t.Fatalf("standalone image generation should suppress hosted image_generation: %#v", tools)
	}
}

func TestRunResponsesReviewRequestDisablesImageGenerationToolsLikeRust(t *testing.T) {
	t.Setenv(auth.OpenAIAPIKeyEnv, "")
	t.Setenv(auth.CodexAPIKeyEnv, "")
	t.Setenv(auth.CodexAccessTokenEnv, "")
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromChatGPTAuthTokens("access-token", "account-1", nil)); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	var recordedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&recordedBody); err != nil {
			t.Fatalf("Decode request body returned error: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		writeExecResponseSSE(w, `{"type":"response.created","response":{"id":"resp-review-tools"}}`)
		writeExecResponseSSE(w, `{"type":"response.output_item.added","item":{"id":"msg-review-tools","type":"message","role":"assistant","content":[{"type":"output_text","text":""}]}}`)
		writeExecResponseSSE(w, `{"type":"response.output_text.delta","item_id":"msg-review-tools","delta":"ready"}`)
		writeExecResponseSSE(w, `{"type":"response.output_item.done","item":{"id":"msg-review-tools","type":"message","role":"assistant","content":[{"type":"output_text","text":"ready"}]}}`)
		writeExecResponseSSE(w, `{"type":"response.completed","response":{"id":"resp-review-tools","model":"gpt-5.5","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`)
	}))
	defer server.Close()
	configBody := "model = \"gpt-5.5\"\nopenai_base_url = \"" + server.URL + "/v1\"\n\n[features]\nenable_request_compression = false\nimage_generation = true\n"
	if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}

	var stdout, stderr bytes.Buffer
	runner := NewRunner(home)
	runner.MCPService = mcp.NewMCPService(&mcp.RuntimeConfig{Servers: map[string]mcp.ServerRegistration{}})
	_, err := runner.Run(Request{
		Exec: cli.ExecOptions{
			Subcommand: "review",
			Review:     cli.ReviewOptions{Prompt: "check image tools"},
			JSON:       true,
			Ephemeral:  true,
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v stderr=%q stdout=%q", err, stderr.String(), stdout.String())
	}
	tools, ok := recordedBody["tools"].([]any)
	if !ok {
		t.Fatalf("tools = %#v, body = %#v", recordedBody["tools"], recordedBody)
	}
	if responseToolsContainTypeForExecTest(tools, "image_generation") ||
		responseToolsContainNamespaceFunctionForExecTest(tools, turn.ImageGenerationNamespace, turn.ImageGenerationToolName) {
		t.Fatalf("review request should disable image generation tools like Rust: %#v", tools)
	}
}

func TestRunResponsesRequestIncludesHostedImageGenerationForOpenAIAPIKeyProvider(t *testing.T) {
	t.Setenv(auth.OpenAIAPIKeyEnv, "")
	t.Setenv(auth.CodexAPIKeyEnv, "")
	t.Setenv(auth.CodexAccessTokenEnv, "")
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	var recordedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&recordedBody); err != nil {
			t.Fatalf("Decode request body returned error: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		writeExecResponseSSE(w, `{"type":"response.created","response":{"id":"resp-image-tools"}}`)
		writeExecResponseSSE(w, `{"type":"response.output_item.added","item":{"id":"msg-image-tools","type":"message","role":"assistant","content":[{"type":"output_text","text":""}]}}`)
		writeExecResponseSSE(w, `{"type":"response.output_text.delta","item_id":"msg-image-tools","delta":"ready"}`)
		writeExecResponseSSE(w, `{"type":"response.output_item.done","item":{"id":"msg-image-tools","type":"message","role":"assistant","content":[{"type":"output_text","text":"ready"}]}}`)
		writeExecResponseSSE(w, `{"type":"response.completed","response":{"id":"resp-image-tools","model":"gpt-5.5","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`)
	}))
	defer server.Close()
	configBody := "model = \"gpt-5.5\"\nmodel_provider = \"OpenAI\"\n\n[model_providers.OpenAI]\nname = \"OpenAI\"\nbase_url = \"" + server.URL + "/v1\"\nwire_api = \"responses\"\nrequires_openai_auth = true\n\n[features]\nenable_request_compression = false\n"
	if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}

	var stdout, stderr bytes.Buffer
	_, err := NewRunner(home).Run(Request{
		Exec: cli.ExecOptions{
			Prompt:    "draw a square",
			JSON:      true,
			Ephemeral: true,
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v stderr=%q stdout=%q", err, stderr.String(), stdout.String())
	}
	tools, ok := recordedBody["tools"].([]any)
	if !ok {
		t.Fatalf("tools = %#v, body = %#v", recordedBody["tools"], recordedBody)
	}
	if !responseToolsContainTypeForExecTest(tools, "image_generation") {
		t.Fatalf("api-key OpenAI provider tools missing hosted image_generation: %#v", tools)
	}
}

func TestRunExposesStandaloneImageGenerationWhenImageGenerationEnabledLikeRust(t *testing.T) {
	t.Setenv(auth.OpenAIAPIKeyEnv, "")
	t.Setenv(auth.CodexAPIKeyEnv, "")
	t.Setenv(auth.CodexAccessTokenEnv, "")
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromChatGPTAuthTokens("access-token", "account-1", nil)); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	if err := os.WriteFile(config.ConfigPath(home), []byte(`
model = "gpt-5.5"

[features]
image_generation = true
`), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}
	agent := &recordingAgent{message: "ok"}
	runner := NewRunner(home)
	runner.Agent = agent
	runner.MCPService = mcp.NewMCPService(&mcp.RuntimeConfig{Servers: map[string]mcp.ServerRegistration{}})

	var stdout, stderr bytes.Buffer
	_, err := runner.Run(Request{
		Exec: cli.ExecOptions{
			Prompt:    "draw a square",
			Ephemeral: true,
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v stderr=%q stdout=%q", err, stderr.String(), stdout.String())
	}
	if agent.request == nil {
		t.Fatal("agent request is nil")
	}
	if !agentRequestToolsContainNamespaceFunction(agent.request, turn.ImageGenerationNamespace, turn.ImageGenerationToolName) {
		t.Fatalf("tools missing standalone image generation namespace: %#v", agent.request.Tools)
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
	for _, want := range []string{
		"OpenAI Codex v",
		"approval: never",
		"tokens used\n15\n",
		"codex\ndone\n",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr missing %q: %q", want, stderr.String())
		}
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
	if !strings.Contains(err.Error(), "Output schema file "+schema+" is not valid JSON") {
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

func TestRunResponsesRequestIncludesOutputSchemaLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	schema := filepath.Join(t.TempDir(), "schema.json")
	schemaJSON := `{
  "type": "object",
  "properties": {
    "answer": { "type": "string" }
  },
  "required": ["answer"],
  "additionalProperties": false
}`
	if err := os.WriteFile(schema, []byte(schemaJSON), 0o600); err != nil {
		t.Fatalf("WriteFile schema returned error: %v", err)
	}

	var recordedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("request path = %q, want /v1/responses", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&recordedBody); err != nil {
			t.Fatalf("Decode request body returned error: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		writeExecResponseSSE(w, `{"type":"response.created","response":{"id":"resp-output-schema"}}`)
		writeExecResponseSSE(w, `{"type":"response.output_item.added","item":{"id":"msg-output-schema","type":"message","role":"assistant","content":[{"type":"output_text","text":""}]}}`)
		writeExecResponseSSE(w, `{"type":"response.output_text.delta","item_id":"msg-output-schema","delta":"fixture hello"}`)
		writeExecResponseSSE(w, `{"type":"response.output_item.done","item":{"id":"msg-output-schema","type":"message","role":"assistant","content":[{"type":"output_text","text":"fixture hello"}]}}`)
		writeExecResponseSSE(w, `{"type":"response.completed","response":{"id":"resp-output-schema","model":"gpt-5.1","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`)
	}))
	defer server.Close()
	configBody := "model = \"gpt-5.1\"\nopenai_base_url = \"" + server.URL + "/v1\"\n\n[features]\nenable_request_compression = false\n"
	if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}

	var stdout, stderr bytes.Buffer
	_, err := NewRunner(home).Run(Request{
		Exec: cli.ExecOptions{
			Prompt:       "tell me a joke",
			OutputSchema: schema,
			JSON:         true,
			Ephemeral:    true,
			Shared:       cli.SharedOptions{CWD: t.TempDir()},
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v stderr=%q stdout=%q", err, stderr.String(), stdout.String())
	}

	text, ok := recordedBody["text"].(map[string]any)
	if !ok {
		t.Fatalf("request missing text field: %#v", recordedBody)
	}
	format, ok := text["format"].(map[string]any)
	if !ok {
		t.Fatalf("request missing text.format field: %#v", text)
	}
	if format["name"] != "codex_output_schema" || format["type"] != "json_schema" || format["strict"] != true {
		t.Fatalf("text.format metadata = %#v", format)
	}
	schemaBody, ok := format["schema"].(map[string]any)
	if !ok {
		t.Fatalf("text.format.schema = %#v", format["schema"])
	}
	properties, ok := schemaBody["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema.properties = %#v", schemaBody["properties"])
	}
	answer, ok := properties["answer"].(map[string]any)
	if !ok || answer["type"] != "string" {
		t.Fatalf("schema.properties.answer = %#v", properties["answer"])
	}
	required, ok := schemaBody["required"].([]any)
	if !ok || len(required) != 1 || required[0] != "answer" {
		t.Fatalf("schema.required = %#v", schemaBody["required"])
	}
	if schemaBody["type"] != "object" || schemaBody["additionalProperties"] != false {
		t.Fatalf("schema body = %#v", schemaBody)
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
	if result.Prompt != "check concurrency" {
		t.Fatalf("Prompt = %q", result.Prompt)
	}
	if !strings.Contains(stdout.String(), "check concurrency") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	record := loadSessionRecord(t, home, result.ThreadID)
	if record.Metadata.ThreadSource != string(model.AgentTaskReview) {
		t.Fatalf("ThreadSource = %q, want review", record.Metadata.ThreadSource)
	}
}

func TestRunExecReviewUsesReviewModelFromConfigLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	if err := os.WriteFile(config.ConfigPath(home), []byte("model = \"gpt-session\"\nreview_model = \"gpt-review\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}
	agent := &recordingAgent{message: "done"}
	runner := NewRunner(home)
	runner.Agent = agent
	var stdout, stderr bytes.Buffer
	_, err := runner.Run(Request{
		Exec: cli.ExecOptions{
			Subcommand: "review",
			Shared:     cli.SharedOptions{Model: "gpt-cli"},
			Review:     cli.ReviewOptions{Prompt: "check concurrency"},
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if agent.request == nil || agent.request.Model != "gpt-review" {
		t.Fatalf("agent model = %#v, want review_model", agent.request)
	}
}

func TestRunExecReviewFallsBackToSessionModelWhenReviewModelUnsetLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	if err := os.WriteFile(config.ConfigPath(home), []byte("model = \"gpt-session\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}
	agent := &recordingAgent{message: "done"}
	runner := NewRunner(home)
	runner.Agent = agent
	var stdout, stderr bytes.Buffer
	_, err := runner.Run(Request{
		Exec: cli.ExecOptions{
			Subcommand: "review",
			Review:     cli.ReviewOptions{Prompt: "check concurrency"},
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if agent.request == nil || agent.request.Model != "gpt-session" {
		t.Fatalf("agent model = %#v, want session model", agent.request)
	}
}

func TestRunExecReviewUsesRustReviewRubricInstructions(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	instructionsFile := filepath.Join(t.TempDir(), "instructions.md")
	if err := os.WriteFile(instructionsFile, []byte("project instructions"), 0o600); err != nil {
		t.Fatalf("WriteFile instructions returned error: %v", err)
	}
	if err := os.WriteFile(config.ConfigPath(home), []byte("model_instructions_file = \""+strings.ReplaceAll(instructionsFile, "\\", "\\\\")+"\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}
	agent := &recordingAgent{message: "done"}
	runner := NewRunner(home)
	runner.Agent = agent
	var stdout, stderr bytes.Buffer
	_, err := runner.Run(Request{
		Exec: cli.ExecOptions{
			Subcommand: "review",
			Review:     cli.ReviewOptions{Prompt: "check concurrency"},
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if agent.request == nil || agent.request.Instructions != review.ReviewPrompt {
		t.Fatalf("instructions = %q, want Rust review rubric", agent.request.Instructions)
	}
	if strings.Contains(agent.request.Instructions, "project instructions") {
		t.Fatalf("review instructions should not use project instructions: %q", agent.request.Instructions)
	}
}

func TestRunExecReviewAddsReviewSubagentMetadataLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	agent := &recordingAgent{message: "done"}
	runner := NewRunner(home)
	runner.Agent = agent
	var stdout, stderr bytes.Buffer
	_, err := runner.Run(Request{
		Exec: cli.ExecOptions{
			Subcommand: "review",
			Review:     cli.ReviewOptions{Prompt: "check concurrency"},
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if agent.request == nil || agent.request.ClientMetadata["x-openai-subagent"] != "review" {
		t.Fatalf("client metadata = %#v", agent.request)
	}
	var turnMetadata map[string]any
	if err := json.Unmarshal([]byte(agent.request.ClientMetadata["x-codex-turn-metadata"]), &turnMetadata); err != nil {
		t.Fatalf("turn metadata json error = %v metadata=%#v", err, agent.request.ClientMetadata)
	}
	if turnMetadata["subagent_kind"] != "review" {
		t.Fatalf("turn metadata = %#v", turnMetadata)
	}
}

func TestRunExecReviewRendersStructuredOutputLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	message := `{"findings":[{"title":"Bug","body":"details","confidence_score":0.7,"priority":1,"code_location":{"absolute_file_path":"/repo/a.go","line_range":{"start":10,"end":12}}}],"overall_correctness":"patch is incorrect","overall_explanation":"summary","overall_confidence_score":0.8}`
	agent := &recordingAgent{message: message}
	runner := NewRunner(home)
	runner.Agent = agent
	lastMessagePath := filepath.Join(t.TempDir(), "last.txt")
	var stdout, stderr bytes.Buffer
	result, err := runner.Run(Request{
		Exec: cli.ExecOptions{
			Subcommand:      "review",
			LastMessageFile: lastMessagePath,
			Review:          cli.ReviewOptions{Prompt: "check concurrency"},
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if strings.Contains(stdout.String(), "overall_correctness") || !strings.Contains(stdout.String(), "summary") || !strings.Contains(stdout.String(), "Review comment:") || !strings.Contains(stdout.String(), "Bug") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if result.LastMessage != strings.TrimSpace(stdout.String()) {
		t.Fatalf("LastMessage = %q stdout = %q", result.LastMessage, stdout.String())
	}
	data, err := os.ReadFile(lastMessagePath)
	if err != nil {
		t.Fatalf("ReadFile last message returned error: %v", err)
	}
	if string(data) != result.LastMessage {
		t.Fatalf("last message file = %q, want %q", string(data), result.LastMessage)
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
		Metadata: session.Metadata{
			LastResponseID:     "resp-last",
			PreviousResponseID: "resp-previous",
		},
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
	if agent.request.PreviousResponseID != "resp-last" {
		t.Fatalf("PreviousResponseID = %q, want latest response id", agent.request.PreviousResponseID)
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
	if err := store.Save(&session.Record{ID: "old", CreatedAt: now, UpdatedAt: now, RecencyAt: now, Metadata: session.Metadata{CWD: "."}}); err != nil {
		t.Fatalf("Save old returned error: %v", err)
	}
	if err := store.Save(&session.Record{ID: "new", CreatedAt: now, UpdatedAt: now.Add(time.Minute), RecencyAt: now.Add(time.Minute), Metadata: session.Metadata{CWD: "."}}); err != nil {
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

func TestRunExecResumeLastFiltersCWDUnlessAllLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	cwdA := t.TempDir()
	cwdB := t.TempDir()
	now := fixedExecTime()
	store := session.NewStore(filepath.Join(home, "sessions"))
	if err := store.Save(&session.Record{
		ID:        "cwd-a",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{CWD: cwdA},
	}); err != nil {
		t.Fatalf("Save cwd-a returned error: %v", err)
	}
	if err := store.Save(&session.Record{
		ID:        "cwd-b-newer",
		CreatedAt: now,
		UpdatedAt: now.Add(time.Minute),
		RecencyAt: now.Add(time.Minute),
		Metadata:  session.Metadata{CWD: cwdB},
	}); err != nil {
		t.Fatalf("Save cwd-b returned error: %v", err)
	}
	selected, err := latestExecResumeRecord(store, &cli.ExecResumeOptions{Last: true}, cwdA)
	if err != nil {
		t.Fatalf("latest cwd-filtered returned error: %v", err)
	}
	if selected.ID != "cwd-a" {
		t.Fatalf("cwd-filtered selected = %q", selected.ID)
	}
	selected, err = latestExecResumeRecord(store, &cli.ExecResumeOptions{Last: true, All: true}, cwdA)
	if err != nil {
		t.Fatalf("latest --all returned error: %v", err)
	}
	if selected.ID != "cwd-b-newer" {
		t.Fatalf("--all selected = %q", selected.ID)
	}

	runner := NewRunner(home)
	runner.Agent = &recordingAgent{message: "ok"}
	runner.Now = func() time.Time { return now.Add(2 * time.Minute) }
	var stdout, stderr bytes.Buffer
	result, err := runner.Run(Request{
		Exec: cli.ExecOptions{
			Shared:     cli.SharedOptions{CWD: cwdA},
			Subcommand: "resume",
			Resume: cli.ExecResumeOptions{
				Last:   true,
				All:    true,
				Prompt: "continue",
			},
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run --all returned error: %v", err)
	}
	if result.ThreadID != "cwd-b-newer" {
		t.Fatalf("--all ThreadID = %q", result.ThreadID)
	}
}

func TestRunExecResumeLastAllIgnoresArchivedLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	now := fixedExecTime()
	store := session.NewStore(filepath.Join(home, "sessions"))
	if err := store.Save(&session.Record{ID: "active", CreatedAt: now, UpdatedAt: now, RecencyAt: now, Metadata: session.Metadata{CWD: "."}}); err != nil {
		t.Fatalf("Save active returned error: %v", err)
	}
	if err := store.Save(&session.Record{
		ID:        "archived-new",
		Archived:  true,
		CreatedAt: now,
		UpdatedAt: now.Add(time.Minute),
		RecencyAt: now.Add(time.Minute),
		Metadata:  session.Metadata{CWD: "."},
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
	if result.ThreadID != "active" {
		t.Fatalf("ThreadID = %q", result.ThreadID)
	}
}

func TestRunExecResumeAcceptsImagesAfterSubcommandLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	imageDir := t.TempDir()
	imageA := filepath.Join(imageDir, "resume-a.png")
	imageB := filepath.Join(imageDir, "resume-b.png")
	if err := os.WriteFile(imageA, minimalPNGBytes(), 0o600); err != nil {
		t.Fatalf("WriteFile imageA returned error: %v", err)
	}
	if err := os.WriteFile(imageB, minimalPNGBytes(), 0o600); err != nil {
		t.Fatalf("WriteFile imageB returned error: %v", err)
	}
	now := fixedExecTime()
	store := session.NewStore(filepath.Join(home, "sessions"))
	if err := store.Save(&session.Record{
		ID:        "thread-image-resume",
		SessionID: "thread-image-resume",
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
			Shared:     cli.SharedOptions{Images: []string{imageA, imageB}},
			Subcommand: "resume",
			Resume: cli.ExecResumeOptions{
				SessionID: "thread-image-resume",
				Prompt:    "inspect these",
			},
		},
	}, strings.NewReader("stdin should be ignored for resume prompt\n"), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v stderr=%q stdout=%q", err, stderr.String(), stdout.String())
	}
	if result.ThreadID != "thread-image-resume" {
		t.Fatalf("ThreadID = %q", result.ThreadID)
	}
	item := agentRequestInputItemWithText(agent.request, "inspect these")
	if item == nil {
		t.Fatalf("input items missing resume prompt = %#v", agent.request.InputItems)
	}
	content, ok := item["content"].([]map[string]any)
	if !ok {
		t.Fatalf("content = %#v", item["content"])
	}
	imageBlocks := 0
	for i := range content {
		if content[i]["type"] == "input_image" {
			imageBlocks++
			if !strings.HasPrefix(fmt.Sprint(content[i]["image_url"]), "data:") {
				t.Fatalf("image block = %#v", content[i])
			}
		}
		if strings.Contains(fmt.Sprint(content[i]["text"]), "stdin should be ignored") {
			t.Fatalf("resume prompt should not append stdin like root exec: %#v", content)
		}
	}
	if imageBlocks != 2 {
		t.Fatalf("image block count = %d content=%#v", imageBlocks, content)
	}

	record := loadSessionRecord(t, home, "thread-image-resume")
	if len(record.Items) != 4 {
		t.Fatalf("items = %#v", record.Items)
	}
	userItem := record.Items[2]
	if len(userItem.Content) != 3 ||
		userItem.Content[0].Text != "inspect these" ||
		userItem.Content[1].Type != "localImage" ||
		userItem.Content[1].ImageURL != imageA ||
		userItem.Content[2].Type != "localImage" ||
		userItem.Content[2].ImageURL != imageB {
		t.Fatalf("session resumed user content = %#v", userItem.Content)
	}
}

func TestRunExecResumeByExactNameFiltersCWDUnlessAllLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	cwdA := t.TempDir()
	cwdB := t.TempDir()
	now := fixedExecTime()
	store := session.NewStore(filepath.Join(home, "sessions"))
	if err := store.Save(&session.Record{
		ID:        "name-cwd-a",
		SessionID: "name-cwd-a",
		Title:     "Design Review",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{CWD: cwdA},
		Items: []session.Item{
			{ID: "old-a", Type: "message", Role: "user", Text: "old a", CreatedAt: now},
		},
	}); err != nil {
		t.Fatalf("Save cwd-a returned error: %v", err)
	}
	if err := store.Save(&session.Record{
		ID:        "name-cwd-b-newer",
		SessionID: "name-cwd-b-newer",
		Title:     "Design Review",
		CreatedAt: now,
		UpdatedAt: now.Add(time.Minute),
		RecencyAt: now.Add(time.Minute),
		Metadata:  session.Metadata{CWD: cwdB},
		Items: []session.Item{
			{ID: "old-b", Type: "message", Role: "user", Text: "old b", CreatedAt: now},
		},
	}); err != nil {
		t.Fatalf("Save cwd-b returned error: %v", err)
	}
	runner := NewRunner(home)
	runner.Agent = &recordingAgent{message: "ok"}
	runner.Now = func() time.Time { return now.Add(2 * time.Minute) }

	var stdout, stderr bytes.Buffer
	result, err := runner.Run(Request{
		Exec: cli.ExecOptions{
			Shared:     cli.SharedOptions{CWD: cwdA},
			Subcommand: "resume",
			Resume: cli.ExecResumeOptions{
				SessionID: "Design Review",
				All:       true,
				Prompt:    "continue newest",
			},
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run --all returned error: %v stderr=%q stdout=%q", err, stderr.String(), stdout.String())
	}
	if result.ThreadID != "name-cwd-b-newer" {
		t.Fatalf("--all ThreadID = %q", result.ThreadID)
	}

	stdout.Reset()
	stderr.Reset()
	runner.Now = func() time.Time { return now.Add(3 * time.Minute) }
	result, err = runner.Run(Request{
		Exec: cli.ExecOptions{
			Shared:     cli.SharedOptions{CWD: cwdA},
			Subcommand: "resume",
			Resume: cli.ExecResumeOptions{
				SessionID: "Design Review",
				Prompt:    "continue cwd",
			},
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run cwd-filtered returned error: %v stderr=%q stdout=%q", err, stderr.String(), stdout.String())
	}
	if result.ThreadID != "name-cwd-a" {
		t.Fatalf("cwd-filtered ThreadID = %q", result.ThreadID)
	}
}

func TestRunExecResumeHumanConfigSummaryUsesCLIOverridesLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	cwd := t.TempDir()
	now := fixedExecTime()
	store := session.NewStore(filepath.Join(home, "sessions"))
	if err := store.Save(&session.Record{
		ID:        "resume-config",
		SessionID: "resume-config",
		Title:     "Config",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{CWD: cwd},
		Items: []session.Item{
			{ID: "old", Type: "message", Role: "user", Text: "seed", CreatedAt: now},
		},
	}); err != nil {
		t.Fatalf("Save session returned error: %v", err)
	}
	runner := NewRunner(home)
	runner.Agent = &recordingAgent{message: "ok"}
	runner.Now = func() time.Time { return now.Add(time.Minute) }

	var stdout, stderr bytes.Buffer
	_, err := runner.Run(Request{
		Exec: cli.ExecOptions{
			Shared: cli.SharedOptions{
				CWD:     cwd,
				Model:   "gpt-5.1-high",
				Sandbox: "workspace-write",
			},
			Subcommand: "resume",
			Resume: cli.ExecResumeOptions{
				SessionID: "resume-config",
				Prompt:    "continue with overrides",
			},
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v stderr=%q stdout=%q", err, stderr.String(), stdout.String())
	}
	for _, want := range []string{
		"model: gpt-5.1-high",
		"approval: never",
		"sandbox: workspace-write",
		"user\ncontinue with overrides",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr missing %q: %q", want, stderr.String())
		}
	}
}

func TestExecResumeTargetUUIDTakesPrecedenceOverNameLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := fixedExecTime()
	uuidTarget := "11111111-2222-3333-4444-555555555555"
	if err := store.Save(&session.Record{
		ID:        session.ThreadID(uuidTarget),
		SessionID: uuidTarget,
		Title:     "Direct UUID",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
	}); err != nil {
		t.Fatalf("Save uuid record returned error: %v", err)
	}
	if err := store.Save(&session.Record{
		ID:        "name-matches-uuid-newer",
		SessionID: "name-matches-uuid-newer",
		Title:     uuidTarget,
		CreatedAt: now,
		UpdatedAt: now.Add(time.Minute),
		RecencyAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("Save name match returned error: %v", err)
	}

	threadID, err := execResumeThreadIDForTarget(store, &cli.ExecResumeOptions{}, uuidTarget, ".")
	if err != nil {
		t.Fatalf("execResumeThreadIDForTarget returned error: %v", err)
	}
	if threadID != session.ThreadID(uuidTarget) {
		t.Fatalf("threadID = %q, want UUID target", threadID)
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

func TestEffectiveExecApprovalPolicyHonorsExplicitInteractivePolicy(t *testing.T) {
	req := &Request{
		Root: cli.RootOptions{Shared: cli.SharedOptions{ApprovalPolicy: string(sandbox.ApprovalOnRequest)}},
	}
	if got := effectiveExecApprovalPolicy(&config.Config{Values: map[string]any{}}, req); got != sandbox.ApprovalOnRequest {
		t.Fatalf("explicit root approval policy = %q, want on-request", got)
	}

	req.Exec.Shared.ApprovalPolicy = string(sandbox.ApprovalNever)
	if got := effectiveExecApprovalPolicy(&config.Config{Values: map[string]any{}}, req); got != sandbox.ApprovalNever {
		t.Fatalf("explicit exec approval policy = %q, want never", got)
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

func TestInteractiveExplicitOnRequestReachesShellApproval(t *testing.T) {
	runner := NewLocalRunner(t.TempDir())
	req := &Request{
		Root: cli.RootOptions{Shared: cli.SharedOptions{ApprovalPolicy: string(sandbox.ApprovalOnRequest)}},
		Exec: cli.ExecOptions{Prompt: "hello"},
	}
	policy := effectiveExecApprovalPolicy(&config.Config{Values: map[string]any{}}, req)
	if policy != sandbox.ApprovalOnRequest {
		t.Fatalf("effective approval policy = %q, want on-request", policy)
	}
	router, err := runner.toolRouterForRequest(req, &agentRunConfig{ApprovalPolicy: policy})
	if err != nil {
		t.Fatalf("toolRouterForRequest returned error: %v", err)
	}
	output, err := router.Dispatch(context.Background(), &tool.Invocation{
		CallID:   "call-additional-permissions",
		ToolName: tool.PlainName(tool.DefaultExecCommandToolName),
		Payload: tool.Payload{
			Kind: tool.PayloadFunction,
			Arguments: `{
				"cmd":"mkdir ../test",
				"sandbox_permissions":"with_additional_permissions",
				"additional_permissions":{"file_system":{"write":["../test"]}}
			}`,
		},
	})
	if err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}
	if output == nil || output.Success || output.Data["approval_required"] != true {
		t.Fatalf("output = %#v, want approval request", output)
	}
	if output.Data["sandbox_permissions"] != sandbox.SandboxPermissionsWithAdditionalPermissions {
		t.Fatalf("sandbox permissions = %#v", output.Data["sandbox_permissions"])
	}
}

func TestToolRouterUsesConfiguredMCPRuntimeLikeRust(t *testing.T) {
	runner := NewRunner(t.TempDir())
	service := mcp.NewMCPService(nil)
	router, err := runner.toolRouterForRequest(&Request{Exec: cli.ExecOptions{Prompt: "hello"}}, &agentRunConfig{
		MCPService: service,
		MCPTools: []mcp.RuntimeToolInfo{{
			ServerName: "docs",
			Tool: mcp.RuntimeTool{
				Name:        "search",
				Description: "Search docs",
				InputSchema: map[string]any{"type": "object"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("toolRouterForRequest returned error: %v", err)
	}
	invocation, ok, err := router.BuildToolCall(tool.ResponseItem{
		Type:      "function_call",
		Namespace: "mcp__docs",
		Name:      "search",
		CallID:    "call-mcp",
		Arguments: `{"q":"rust parity"}`,
	})
	if err != nil {
		t.Fatalf("BuildToolCall MCP tool returned error: %v", err)
	}
	if !ok {
		t.Fatal("BuildToolCall MCP tool returned ok=false")
	}
	output, err := router.Dispatch(context.Background(), invocation)
	if err != nil {
		t.Fatalf("Dispatch MCP tool returned error: %v", err)
	}
	if output == nil || !output.Success || !strings.Contains(output.Body, "rust parity") {
		t.Fatalf("MCP output = %#v", output)
	}
}

func TestConfiguredMCPRuntimeReadsConfigServersLikeRust(t *testing.T) {
	runner := NewRunner(t.TempDir())
	service, tools, connectors := runner.configuredMCPRuntimeForConfig(&config.Config{Values: map[string]any{
		"features": map[string]any{"apps": false},
		"mcp_servers": map[string]any{
			"angr": map[string]any{
				"command": "codex-go-missing-mcp-test",
			},
		},
	}})
	if service == nil {
		t.Fatal("configuredMCPRuntimeForConfig returned nil service for configured MCP server")
	}
	defer service.Close()
	if len(tools) != 0 || len(connectors) != 0 {
		t.Fatalf("tools/connectors = %#v/%#v, want none for missing helper", tools, connectors)
	}
	status, err := service.ListStatusChecked(&mcp.MCPListServerStatusParams{
		Detail: &mcp.MCPServerStatusDetail{Mode: mcp.MCPServerStatusDetailToolsAndAuthOnly},
	})
	if err != nil {
		t.Fatalf("ListStatusChecked returned error: %v", err)
	}
	if len(status.Data) != 1 || status.Data[0].Name != "angr" {
		t.Fatalf("status = %#v, want configured angr server", status.Data)
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
	if !agentRequestToolsContainPlainFunction(&agent.requests[0], "echo") ||
		!agentRequestToolsContainPlainFunction(&agent.requests[1], "echo") ||
		!agentRequestToolsContainType(&agent.requests[0], "image_generation") ||
		!agentRequestToolsContainType(&agent.requests[1], "image_generation") {
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
	items := sessionItemsForTurn("turn-exec", "hello", nil, result, createdAt, nil, nil)

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

func TestExecSessionItemsPersistHostedImageGeneration(t *testing.T) {
	home := t.TempDir()
	createdAt := time.Unix(1700000000, 0).UTC()
	result := &turn.AgentLoopResult{
		Responses: []*model.AgentResponse{{
			ResponseID: "resp-image",
			Items: []model.AgentItem{{
				ID:     "ig_123",
				Type:   "image_generation_call",
				Status: "generating",
				Text:   "Zm9v",
				Data: map[string]any{
					"revised_prompt": "A tiny blue square",
				},
			}},
		}},
	}
	result.Response = result.Responses[0]

	items := sessionItemsForTurn("turn-exec", "draw", nil, result, createdAt, nil, &execImageGenerationContext{
		CodexHome: home,
		ThreadID:  "thread-1",
	})
	imageIndex := execSessionItemIndexByType(items, "imageGeneration")
	if imageIndex < 0 {
		t.Fatalf("missing imageGeneration item: %#v", items)
	}
	imageItem := items[imageIndex]
	if imageItem.Status != "completed" || imageItem.Text != "A tiny blue square" {
		t.Fatalf("image item = %#v", imageItem)
	}
	if imageItem.Content != nil || strings.Contains(imageItem.Text, "Zm9v") {
		t.Fatalf("image base64 leaked into visible text/content: %#v", imageItem)
	}
	savedPath := firstNonEmpty(execStringFromAny(imageItem.Data["savedPath"]), execStringFromAny(imageItem.Data["saved_path"]))
	if savedPath == "" {
		t.Fatalf("missing saved path in image data: %#v", imageItem.Data)
	}
	bytes, err := os.ReadFile(savedPath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", savedPath, err)
	}
	if string(bytes) != "foo" {
		t.Fatalf("saved bytes = %q", string(bytes))
	}
	instructionsIndex := execSessionItemIndexByID(items, "image-generation-instructions-ig_123")
	if instructionsIndex < 0 || items[instructionsIndex].Role != "developer" || !strings.Contains(items[instructionsIndex].Text, filepath.Dir(savedPath)) {
		t.Fatalf("missing image generation instructions item: %#v", items)
	}

	sink := newExecEventSink(nil, false)
	if err := emitFinalEventsFromAgentResult(sink, result); err != nil {
		t.Fatalf("emitFinalEventsFromAgentResult() error = %v", err)
	}
	var eventItem *protocol.ThreadItem
	for _, event := range sink.Events() {
		if event.Type == "item.completed" && event.Item != nil && event.Item.Type == "imageGeneration" {
			eventItem = event.Item
			break
		}
	}
	if eventItem == nil || eventItem.SavedPath != savedPath || eventItem.RevisedPrompt != "A tiny blue square" {
		t.Fatalf("image generation event item = %#v", eventItem)
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

func TestEmitFinalEventsMapsToolSearchCallToWebSearchLikeRust(t *testing.T) {
	result := &turn.AgentLoopResult{
		Response: &model.AgentResponse{
			Items: []model.AgentItem{{
				ID:     "search-1",
				Type:   "tool_search_call",
				CallID: "search-1",
				Search: map[string]any{"query": "rust async await"},
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
	searchIndex := execEventItemIndex(events, "search-1")
	if searchIndex < 0 {
		t.Fatalf("web search event missing: %#v", events)
	}
	search := events[searchIndex].Item
	if search.Type != "web_search" || search.Query != "rust async await" {
		t.Fatalf("web search item = %#v", search)
	}
	if search.Action["type"] != "search" || search.Action["query"] != "rust async await" {
		t.Fatalf("web search action = %#v", search.Action)
	}
	for _, event := range events {
		if event.Item != nil && event.Item.Type == "tool_call" && event.Item.ID == "search-1" {
			t.Fatalf("tool_search_call should not emit generic tool_call: %#v", events)
		}
	}
}

func TestEmitFinalEventsMapsApplyPatchToFileChangeLikeRust(t *testing.T) {
	result := &turn.AgentLoopResult{
		Response: &model.AgentResponse{Items: []model.AgentItem{{
			ID:     "call-apply",
			Type:   "custom_tool_call",
			Name:   tool.DefaultApplyPatchToolName,
			CallID: "call-apply",
			Input:  "*** Begin Patch\n*** Add File: a/added.txt\n+hi\n*** End Patch",
		}}},
		ToolExecutions: []turn.ToolExecutionResult{{
			Invocation: &tool.Invocation{
				CallID:   "call-apply",
				ToolName: tool.PlainName(tool.DefaultApplyPatchToolName),
				Payload:  tool.Payload{Kind: tool.PayloadCustom, Input: "patch"},
			},
			Output: &tool.Output{
				CallID:   "call-apply",
				ToolName: tool.PlainName(tool.DefaultApplyPatchToolName),
				Success:  true,
				Data: map[string]any{
					"fileChange": true,
					"status":     "completed",
					"changes": []map[string]any{{
						"path": "a/added.txt",
						"kind": map[string]any{"type": "add"},
					}, {
						"path": "b/deleted.txt",
						"kind": map[string]any{"type": "delete"},
					}, {
						"path": "c/modified.txt",
						"kind": map[string]any{"type": "update", "move_path": nil},
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
	fileChangeIndex := execEventItemIndex(events, "file-change-call-apply")
	if fileChangeIndex < 0 {
		t.Fatalf("file_change event missing: %#v", events)
	}
	item := events[fileChangeIndex].Item
	if item.Type != "file_change" || item.Status != "completed" || len(item.Changes) != 3 {
		t.Fatalf("file_change item = %#v", item)
	}
	if item.Changes[0].Kind != "add" || item.Changes[1].Kind != "delete" || item.Changes[2].Kind != "update" {
		t.Fatalf("file_change changes = %#v", item.Changes)
	}
	if execEventItemIndex(events, "tool-call-call-apply") >= 0 || execEventItemIndex(events, "tool-output-call-apply") >= 0 {
		t.Fatalf("file change should not emit generic apply_patch tool events: %#v", events)
	}
}

func TestEmitFinalEventsPreservesDeclinedFileChangeLikeRust(t *testing.T) {
	result := &turn.AgentLoopResult{
		Response: &model.AgentResponse{},
		ToolExecutions: []turn.ToolExecutionResult{{
			Invocation: &tool.Invocation{
				CallID:   "patch-2",
				ToolName: tool.PlainName(tool.DefaultApplyPatchToolName),
			},
			Output: &tool.Output{
				CallID:   "patch-2",
				ToolName: tool.PlainName(tool.DefaultApplyPatchToolName),
				Success:  false,
				Data: map[string]any{
					"fileChange": true,
					"status":     "declined",
					"changes": []any{map[string]any{
						"path": "file.txt",
						"kind": map[string]any{"type": "update"},
					}},
				},
			},
		}},
	}
	sink := newExecEventSink(nil, false)

	if err := emitFinalEventsFromAgentResult(sink, result); err != nil {
		t.Fatalf("emitFinalEventsFromAgentResult() error = %v", err)
	}
	events := sink.Events()
	fileChangeIndex := execEventItemIndex(events, "file-change-patch-2")
	if fileChangeIndex < 0 {
		t.Fatalf("file_change event missing: %#v", events)
	}
	if got := events[fileChangeIndex].Item.Status; got != "declined" {
		t.Fatalf("file_change status = %q, want declined", got)
	}
}

func TestEmitFinalEventsMapsMCPToolCallLikeRust(t *testing.T) {
	result := &turn.AgentLoopResult{
		Response: &model.AgentResponse{Items: []model.AgentItem{{
			ID:        "call-mcp",
			Type:      "function_call",
			Name:      "search",
			Namespace: "docs",
			CallID:    "call-mcp",
			Arguments: `{"q":"rust parity"}`,
		}}},
		ToolExecutions: []turn.ToolExecutionResult{{
			Invocation: &tool.Invocation{
				CallID:   "call-mcp",
				ToolName: tool.NamespacedName("docs", "search"),
				Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"q":"rust parity"}`},
			},
			Output: &tool.Output{
				CallID:   "call-mcp",
				ToolName: tool.NamespacedName("docs", "search"),
				Success:  true,
				Body:     "done",
				Data: map[string]any{
					"mcpToolCall": true,
					"content": []map[string]any{{
						"type": "text",
						"text": "done",
					}},
					"structuredContent": map[string]any{"status": "ok"},
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
	startedIndex := execEventTypeAndItemIndex(events, "item.started", "call-mcp")
	completedIndex := execEventTypeAndItemIndex(events, "item.completed", "call-mcp")
	if startedIndex < 0 || completedIndex < 0 || !(startedIndex < completedIndex) {
		t.Fatalf("mcp start/completed events missing or out of order: %#v", events)
	}
	started := events[startedIndex].Item
	if started.Type != "mcp_tool_call" || started.Server != "docs" || started.Tool != "search" || started.Status != "in_progress" {
		t.Fatalf("mcp started item = %#v", started)
	}
	if started.Arguments == nil {
		t.Fatalf("mcp started arguments missing: %#v", started)
	}
	startedArgs, ok := (*started.Arguments).(map[string]any)
	if !ok || startedArgs["q"] != "rust parity" {
		t.Fatalf("mcp started arguments = %#v", started.Arguments)
	}
	completed := events[completedIndex].Item
	if completed.Status != "completed" || completed.Result == nil || len(completed.Result.Content) != 1 {
		t.Fatalf("mcp completed item = %#v", completed)
	}
	if structured, ok := completed.Result.StructuredContent.(map[string]any); !ok || structured["status"] != "ok" {
		t.Fatalf("mcp structured content = %#v", completed.Result.StructuredContent)
	}
	if execEventItemIndex(events, "tool-call-call-mcp") >= 0 || execEventItemIndex(events, "tool-output-call-mcp") >= 0 {
		t.Fatalf("mcp tool call should not emit generic tool events: %#v", events)
	}
}

func TestEmitFinalEventsMapsFailedMCPToolCallLikeRust(t *testing.T) {
	result := &turn.AgentLoopResult{
		Response: &model.AgentResponse{},
		ToolExecutions: []turn.ToolExecutionResult{{
			Invocation: &tool.Invocation{
				CallID:   "mcp-2",
				ToolName: tool.NamespacedName("server_b", "tool_y"),
				Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"param":42}`},
			},
			Output: &tool.Output{
				CallID:   "mcp-2",
				ToolName: tool.NamespacedName("server_b", "tool_y"),
				Success:  false,
				Body:     "tool exploded",
				Data:     map[string]any{"mcpToolCall": true},
			},
		}},
	}
	sink := newExecEventSink(nil, false)

	if err := emitFinalEventsFromAgentResult(sink, result); err != nil {
		t.Fatalf("emitFinalEventsFromAgentResult() error = %v", err)
	}
	events := sink.Events()
	completedIndex := execEventTypeAndItemIndex(events, "item.completed", "mcp-2")
	if completedIndex < 0 {
		t.Fatalf("mcp completed event missing: %#v", events)
	}
	item := events[completedIndex].Item
	if item.Status != "failed" || item.CallError == nil || item.CallError.Message != "tool exploded" {
		t.Fatalf("mcp failed item = %#v", item)
	}
}

func TestEmitFinalEventsMapsCollabToolCallLikeRust(t *testing.T) {
	result := &turn.AgentLoopResult{
		Response: &model.AgentResponse{Items: []model.AgentItem{{
			ID:        "collab-1",
			Type:      "function_call",
			Namespace: "agent",
			Name:      "spawn_agent",
			CallID:    "collab-1",
			Arguments: `{"message":"draft a plan"}`,
		}}},
		ToolExecutions: []turn.ToolExecutionResult{{
			Invocation: &tool.Invocation{
				CallID:   "collab-1",
				ToolName: tool.NamespacedName("agent", "spawn_agent"),
				Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"message":"draft a plan"}`},
				Context:  map[string]any{"thread_id": "thread-parent"},
			},
			Output: &tool.Output{
				CallID:   "collab-1",
				ToolName: tool.NamespacedName("agent", "spawn_agent"),
				Success:  true,
				Body:     `{"agent_id":"thread-child"}`,
				Data: map[string]any{
					"result": map[string]any{"agent_id": "thread-child"},
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
	startedIndex := execEventTypeAndItemIndex(events, "item.started", "collab-1")
	completedIndex := execEventTypeAndItemIndex(events, "item.completed", "collab-1")
	if startedIndex < 0 || completedIndex < 0 || !(startedIndex < completedIndex) {
		t.Fatalf("collab start/completed events missing or out of order: %#v", events)
	}
	started := events[startedIndex].Item
	if started.Type != "collab_tool_call" || started.Tool != "spawn_agent" || started.Status != "in_progress" ||
		started.SenderThreadID != "thread-parent" || started.Prompt == nil || *started.Prompt != "draft a plan" {
		t.Fatalf("collab started item = %#v", started)
	}
	if started.ReceiverThreadIDs == nil || len(*started.ReceiverThreadIDs) != 0 {
		t.Fatalf("collab started receivers = %#v", started.ReceiverThreadIDs)
	}
	completed := events[completedIndex].Item
	if completed.Type != "collab_tool_call" || completed.Tool != "spawn_agent" || completed.Status != "completed" {
		t.Fatalf("collab completed item = %#v", completed)
	}
	if completed.ReceiverThreadIDs == nil || len(*completed.ReceiverThreadIDs) != 1 || (*completed.ReceiverThreadIDs)[0] != "thread-child" {
		t.Fatalf("collab completed receivers = %#v", completed.ReceiverThreadIDs)
	}
	if completed.AgentsStates == nil || (*completed.AgentsStates)["thread-child"].Status != "running" {
		t.Fatalf("collab completed states = %#v", completed.AgentsStates)
	}
	if execEventItemIndex(events, "tool-call-collab-1") >= 0 || execEventItemIndex(events, "tool-output-collab-1") >= 0 {
		t.Fatalf("collab tool call should not emit generic tool events: %#v", events)
	}
}

func TestEmitFinalEventsMapsCollabWaitAgentToRustWait(t *testing.T) {
	result := &turn.AgentLoopResult{
		Response: &model.AgentResponse{},
		ToolExecutions: []turn.ToolExecutionResult{{
			Invocation: &tool.Invocation{
				CallID:   "collab-wait",
				ToolName: tool.NamespacedName("agent", "wait_agent"),
				Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"targets":["thread-child"]}`},
				Context:  map[string]any{"thread_id": "thread-parent"},
			},
			Output: &tool.Output{
				CallID:   "collab-wait",
				ToolName: tool.NamespacedName("agent", "wait_agent"),
				Success:  true,
				Data: map[string]any{
					"result": map[string]any{
						"status": map[string]any{
							"thread-child": map[string]any{"status": "notFound", "message": "gone"},
						},
						"timed_out": false,
					},
				},
			},
		}},
	}
	sink := newExecEventSink(nil, false)

	if err := emitFinalEventsFromAgentResult(sink, result); err != nil {
		t.Fatalf("emitFinalEventsFromAgentResult() error = %v", err)
	}
	events := sink.Events()
	completedIndex := execEventTypeAndItemIndex(events, "item.completed", "collab-wait")
	if completedIndex < 0 {
		t.Fatalf("collab wait completed event missing: %#v", events)
	}
	item := events[completedIndex].Item
	if item.Type != "collab_tool_call" || item.Tool != "wait" || item.Status != "completed" {
		t.Fatalf("collab wait item = %#v", item)
	}
	if item.AgentsStates == nil || (*item.AgentsStates)["thread-child"].Status != "not_found" {
		t.Fatalf("collab wait states = %#v", item.AgentsStates)
	}
}

func TestEmitFinalEventsMapsExecCommandToCommandExecutionLikeRust(t *testing.T) {
	result := &turn.AgentLoopResult{
		Response: &model.AgentResponse{Items: []model.AgentItem{{
			ID:        "call-cmd",
			Type:      "function_call",
			Name:      tool.DefaultExecCommandToolName,
			CallID:    "call-cmd",
			Arguments: `{"cmd":"ls"}`,
		}}},
		ToolExecutions: []turn.ToolExecutionResult{{
			Invocation: &tool.Invocation{
				CallID:   "call-cmd",
				ToolName: tool.PlainName(tool.DefaultExecCommandToolName),
				Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"cmd":"ls"}`},
			},
			Output: &tool.Output{
				CallID:   "call-cmd",
				ToolName: tool.PlainName(tool.DefaultExecCommandToolName),
				Success:  true,
				Body:     "a.txt\n",
				Data: map[string]any{
					"exit_code":     0,
					"hook_response": "a.txt\n",
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
	startedIndex := execEventTypeAndItemIndex(events, "item.started", "call-cmd")
	completedIndex := execEventTypeAndItemIndex(events, "item.completed", "call-cmd")
	if startedIndex < 0 || completedIndex < 0 || !(startedIndex < completedIndex) {
		t.Fatalf("command_execution start/completed events missing or out of order: %#v", events)
	}
	started := events[startedIndex].Item
	if started.Type != "command_execution" || started.Command != "ls" || started.Status != "in_progress" || started.AggregatedOutput == nil || *started.AggregatedOutput != "" {
		t.Fatalf("command started item = %#v", started)
	}
	completed := events[completedIndex].Item
	if completed.Type != "command_execution" || completed.Status != "completed" || completed.ExitCode == nil || *completed.ExitCode != 0 {
		t.Fatalf("command completed item = %#v", completed)
	}
	if completed.AggregatedOutput == nil || *completed.AggregatedOutput != "a.txt\n" {
		t.Fatalf("command aggregated output = %#v", completed.AggregatedOutput)
	}
	if execEventItemIndex(events, "tool-call-call-cmd") >= 0 || execEventItemIndex(events, "tool-output-call-cmd") >= 0 {
		t.Fatalf("exec_command should not emit generic tool events after command_execution mapping: %#v", events)
	}
}

func TestEmitFinalEventsKeepsApprovalRequiredExecCommandAsToolOutput(t *testing.T) {
	event, ok := eventFromToolOutputExecution(&turn.ToolExecutionResult{
		Invocation: &tool.Invocation{CallID: "call-approval", ToolName: tool.PlainName(tool.DefaultExecCommandToolName)},
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
	if event.Item.Type != "tool_output" || event.Item.Type == "command_execution" {
		t.Fatalf("approval required should remain tool_output: %#v", event.Item)
	}
	if event.Item.Metadata["approval_required"] != true || event.Item.Metadata["reason"] != "command requested sandbox permissions" {
		t.Fatalf("metadata = %#v", event.Item.Metadata)
	}
}

func TestWriteStdinCompletionMapsToOriginalCommandExecutionLikeRust(t *testing.T) {
	running := &turn.ToolExecutionResult{
		Invocation: &tool.Invocation{CallID: "write-running", ToolName: tool.PlainName(tool.DefaultWriteStdinToolName)},
		Output: &tool.Output{Success: true, Data: map[string]any{
			"process_id":    42,
			"event_call_id": "exec-original",
			"hook_command":  "sleep then print",
		}},
	}
	if events := eventsFromToolExecution(running); len(events) != 0 {
		t.Fatalf("running write_stdin events = %#v, want none", events)
	}

	completed := &turn.ToolExecutionResult{
		Invocation: &tool.Invocation{CallID: "write-complete", ToolName: tool.PlainName(tool.DefaultWriteStdinToolName)},
		Output: &tool.Output{Success: true, Data: map[string]any{
			"exit_code":     0,
			"event_call_id": "exec-original",
			"hook_command":  "sleep then print",
			"hook_response": "done\n",
		}},
	}
	events := eventsFromToolExecution(completed)
	if len(events) != 1 || events[0].Type != "item.completed" || events[0].Item == nil {
		t.Fatalf("completed write_stdin events = %#v", events)
	}
	item := events[0].Item
	if item.Type != "command_execution" || item.ID != "exec-original" || item.Command != "sleep then print" || item.ExitCode == nil || *item.ExitCode != 0 || item.AggregatedOutput == nil || *item.AggregatedOutput != "done\n" {
		t.Fatalf("command completion item = %#v", item)
	}
}

func TestEmitFinalEventsIncludesAgentMessagesAfterStreaming(t *testing.T) {
	result := &turn.AgentLoopResult{
		Responses: []*model.AgentResponse{{
			ResponseID: "resp-first",
			Items: []model.AgentItem{{
				ID:   "msg-skill",
				Type: "agent_message",
				Text: "Using the openai-docs skill because you invoked it.",
			}, {
				ID:     "call-1",
				Type:   "function_call",
				Name:   "exec_command",
				CallID: "call-1",
			}},
		}, {
			ResponseID: "resp-final",
			Items: []model.AgentItem{{
				ID:   "msg-ready",
				Type: "agent_message",
				Text: "Ready - I'll use official OpenAI docs/manual sources.",
			}},
		}},
		ToolExecutions: []turn.ToolExecutionResult{{
			Invocation: &tool.Invocation{
				CallID:   "call-1",
				ToolName: tool.PlainName("exec_command"),
				Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"cmd":"Get-Content SKILL.md"}`},
			},
			Output: &tool.Output{CallID: "call-1", Success: true, Body: "skill contents"},
		}},
	}
	result.Response = result.Responses[1]
	sink := newExecEventSink(nil, false)

	if err := emitFinalEventsFromAgentResult(sink, result); err != nil {
		t.Fatalf("emitFinalEventsFromAgentResult() error = %v", err)
	}

	events := sink.Events()
	if execEventItemIndex(events, "msg-skill") < 0 || execEventItemIndex(events, "msg-ready") < 0 {
		t.Fatalf("agent message completed events missing: %#v", events)
	}
	if execEventItemIndex(events, "tool-output-call-1") < 0 {
		t.Fatalf("tool output event missing: %#v", events)
	}
	if execEventTypeIndex(events, "turn.completed") < 0 {
		t.Fatalf("turn.completed missing: %#v", events)
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
	startedIndex := execEventTypeAndItemIndex(events, "item.started", "todo-list-plan-1")
	completedIndex := execEventTypeAndItemIndex(events, "item.completed", "todo-list-plan-1")
	turnCompletedIndex := execEventTypeIndex(events, "turn.completed")
	if startedIndex < 0 || completedIndex < 0 || turnCompletedIndex < 0 || !(startedIndex < completedIndex && completedIndex < turnCompletedIndex) {
		t.Fatalf("todo list lifecycle missing or out of order: %#v", events)
	}
	todo := events[completedIndex].Item
	if todo.Type != "todo_list" || len(todo.Items) != 2 || todo.Items[0].Text != "step one" || todo.Items[0].Completed || !todo.Items[1].Completed {
		t.Fatalf("todo list item = %#v", todo)
	}
	if execEventItemIndex(events, "tool-call-plan-1") >= 0 || execEventItemIndex(events, "tool-output-plan-1") >= 0 {
		t.Fatalf("update_plan should not emit generic tool events: %#v", events)
	}
}

func TestEmitFinalEventsMapsMultipleUpdatePlansToTodoListLifecycleLikeRust(t *testing.T) {
	result := &turn.AgentLoopResult{
		Response: &model.AgentResponse{},
		ToolExecutions: []turn.ToolExecutionResult{{
			Invocation: &tool.Invocation{
				CallID:   "plan-1",
				ToolName: tool.PlainName("update_plan"),
			},
			Output: &tool.Output{
				CallID:   "plan-1",
				ToolName: tool.PlainName("update_plan"),
				Success:  true,
				Data: map[string]any{
					"planUpdate": true,
					"plan": []tool.PlanItem{{
						Step:   "step one",
						Status: tool.PlanPending,
					}, {
						Step:   "step two",
						Status: tool.PlanInProgress,
					}},
				},
			},
		}, {
			Invocation: &tool.Invocation{
				CallID:   "plan-2",
				ToolName: tool.PlainName("update_plan"),
			},
			Output: &tool.Output{
				CallID:   "plan-2",
				ToolName: tool.PlainName("update_plan"),
				Success:  true,
				Data: map[string]any{
					"planUpdate": true,
					"plan": []tool.PlanItem{{
						Step:   "step one",
						Status: tool.PlanCompleted,
					}, {
						Step:   "step two",
						Status: tool.PlanInProgress,
					}},
				},
			},
		}},
	}
	sink := newExecEventSink(nil, false)
	if err := emitFinalEventsFromAgentResult(sink, result); err != nil {
		t.Fatalf("emitFinalEventsFromAgentResult() error = %v", err)
	}
	events := sink.Events()
	startedIndex := execEventTypeAndItemIndex(events, "item.started", "todo-list-plan-1")
	updatedIndex := execEventTypeAndItemIndex(events, "item.updated", "todo-list-plan-1")
	completedIndex := execEventTypeAndItemIndex(events, "item.completed", "todo-list-plan-1")
	if startedIndex < 0 || updatedIndex < 0 || completedIndex < 0 || !(startedIndex < updatedIndex && updatedIndex < completedIndex) {
		t.Fatalf("todo list lifecycle missing or out of order: %#v", events)
	}
	updated := events[updatedIndex].Item
	if len(updated.Items) != 2 || !updated.Items[0].Completed || updated.Items[1].Completed {
		t.Fatalf("updated todo items = %#v", updated.Items)
	}
	completed := events[completedIndex].Item
	if len(completed.Items) != 2 || !completed.Items[0].Completed || completed.Items[1].Completed {
		t.Fatalf("completed todo items = %#v", completed.Items)
	}
	if execEventItemIndex(events, "tool-call-plan-1") >= 0 ||
		execEventItemIndex(events, "tool-output-plan-1") >= 0 ||
		execEventItemIndex(events, "tool-call-plan-2") >= 0 ||
		execEventItemIndex(events, "tool-output-plan-2") >= 0 {
		t.Fatalf("update_plan should not emit generic tool events: %#v", events)
	}
}

func TestExecStreamEventCollectorDefersExecCommandUntilToolStarted(t *testing.T) {
	collector := &execStreamEventCollector{streamAssistantDeltas: true}
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
	collector.Handle(&model.ResponsesStreamEvent{
		Kind: model.ResponsesStreamEventRateLimits,
		RateLimit: &model.ResponsesRateLimitSnapshot{
			LimitID: "codex",
		},
	})
	collector.Handle(&model.ResponsesStreamEvent{
		Kind: model.ResponsesStreamEventOutputAdded,
		Item: &model.AgentItem{
			ID:        "call-1",
			Type:      "function_call",
			Name:      "exec_command",
			CallID:    "call-1",
			Arguments: `{"cmd":"date"}`,
		},
	})
	events := collector.Events()
	if len(events) != 1 || events[0].Type != "item.delta" || events[0].Delta == nil || events[0].Delta.Text != "hello " {
		t.Fatalf("assistant stream event = %#v", events)
	}

	collector.ToolStarted(context.Background(), &tool.Invocation{
		CallID:   "call-1",
		ToolName: tool.PlainName(tool.DefaultExecCommandToolName),
		Payload: tool.Payload{
			Kind:      tool.PayloadFunction,
			Arguments: `{"cmd":"date"}`,
		},
	}, time.Now())
	events = collector.Events()
	if len(events) != 2 || events[1].Type != "item.started" || events[1].Item == nil {
		t.Fatalf("tool start event = %#v", events)
	}
	if events[1].Item.Type != "command_execution" || events[1].Item.ID != "call-1" || events[1].Item.Command != "date" {
		t.Fatalf("command start item = %#v", events[1].Item)
	}
}

func TestExecStreamEventCollectorSuppressesGenericApplyPatchAndEmitsFileChangeBegin(t *testing.T) {
	collector := &execStreamEventCollector{streamAssistantDeltas: true}
	patch := "*** Begin Patch\n*** Add File: a.txt\n+hello\n*** End Patch"
	collector.Handle(&model.ResponsesStreamEvent{
		Kind: model.ResponsesStreamEventOutputAdded,
		Item: &model.AgentItem{ID: "patch-1", Type: "custom_tool_call", Name: tool.DefaultApplyPatchToolName, CallID: "patch-1", Input: patch},
	})
	if events := collector.Events(); len(events) != 0 {
		t.Fatalf("generic apply_patch events = %#v, want none", events)
	}

	collector.ToolStarted(context.Background(), &tool.Invocation{
		CallID:   "patch-1",
		ToolName: tool.PlainName(tool.DefaultApplyPatchToolName),
		Payload:  tool.Payload{Kind: tool.PayloadCustom, Input: patch},
	}, time.Now())
	events := collector.Events()
	if len(events) != 1 || events[0].Type != "item.started" || events[0].Item == nil || events[0].Item.Type != "file_change" {
		t.Fatalf("apply_patch start events = %#v", events)
	}
	if events[0].Item.ID != "patch-1" || events[0].Item.Status != "in_progress" || len(events[0].Item.Changes) != 1 {
		t.Fatalf("apply_patch start item = %#v", events[0].Item)
	}
}

func TestExecStreamEventCollectorPreservesCommentaryBeforeToolStart(t *testing.T) {
	collector := &execStreamEventCollector{streamAssistantDeltas: true}
	collector.Handle(&model.ResponsesStreamEvent{
		Kind:   model.ResponsesStreamEventOutputText,
		ItemID: "msg-commentary",
		Delta:  "我查询一下天气。",
	})
	collector.ToolStarted(context.Background(), &tool.Invocation{
		CallID:   "call-weather",
		ToolName: tool.PlainName(tool.DefaultExecCommandToolName),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"cmd":"curl weather"}`},
	}, time.Now())
	events := collector.Events()
	if len(events) != 2 || events[0].Type != "item.delta" || events[1].Type != "item.started" {
		t.Fatalf("event order = %#v", events)
	}
	if events[0].Delta == nil || events[0].Delta.Text != "我查询一下天气。" {
		t.Fatalf("commentary event = %#v", events[0])
	}
	if events[1].Item == nil || events[1].Item.Type != "command_execution" {
		t.Fatalf("tool event = %#v", events[1])
	}
}

func TestExecStreamEventCollectorPreservesWhitespaceOnlyAssistantDeltas(t *testing.T) {
	collector := &execStreamEventCollector{streamAssistantDeltas: true}
	for _, delta := range []string{"first line", "\n\n", "second line"} {
		collector.Handle(&model.ResponsesStreamEvent{
			Kind:   model.ResponsesStreamEventOutputText,
			ItemID: "msg-1",
			Delta:  delta,
		})
	}
	collector.Handle(&model.ResponsesStreamEvent{
		Kind:   model.ResponsesStreamEventOutputDone,
		ItemID: "msg-1",
		Item: &model.AgentItem{
			ID:   "msg-1",
			Type: "agent_message",
			Text: "first line\n\nsecond line",
		},
	})

	events := collector.Events()
	if len(events) != 4 {
		t.Fatalf("events = %#v, want three deltas and one completed event", events)
	}
	var streamed strings.Builder
	for _, event := range events[:3] {
		if event.Type != "item.delta" || event.Delta == nil {
			t.Fatalf("stream event = %#v, want item.delta", event)
		}
		streamed.WriteString(event.Delta.Text)
	}
	if got, want := streamed.String(), "first line\n\nsecond line"; got != want {
		t.Fatalf("streamed text = %q, want %q", got, want)
	}
	if event := events[3]; event.Type != "item.completed" || event.Item == nil || event.Item.Text != streamed.String() {
		t.Fatalf("completed event = %#v, want final text matching streamed text", event)
	}
}

func TestExecStreamEventCollectorCompletesMessageWithoutTextDeltas(t *testing.T) {
	collector := &execStreamEventCollector{streamAssistantDeltas: true}
	collector.Handle(&model.ResponsesStreamEvent{
		Kind:   model.ResponsesStreamEventOutputDone,
		ItemID: "msg-commentary",
		Item: &model.AgentItem{
			ID:   "msg-commentary",
			Type: "agent_message",
			Text: "我查询一下天气。",
		},
	})
	events := collector.Events()
	if len(events) != 2 || events[0].Type != "item.delta" || events[1].Type != "item.completed" {
		t.Fatalf("completed-only events = %#v", events)
	}
	if events[0].Delta == nil || events[0].Delta.Text != "我查询一下天气。" {
		t.Fatalf("completed-only delta = %#v", events[0])
	}
}

func TestExecStreamEventCollectorDefersMCPUntilToolStarted(t *testing.T) {
	collector := &execStreamEventCollector{}
	collector.Handle(&model.ResponsesStreamEvent{
		Kind: model.ResponsesStreamEventOutputAdded,
		Item: &model.AgentItem{
			ID:        "call-mcp",
			Type:      "function_call",
			Name:      "geogebra_create_point",
			Namespace: "mcp__geogebra",
			CallID:    "call-mcp",
			Arguments: `{"label":"A"}`,
		},
	})
	if events := collector.Events(); len(events) != 0 {
		t.Fatalf("model output-added should not create a generic MCP exec cell: %#v", events)
	}

	collector.ToolStarted(context.Background(), &tool.Invocation{
		CallID:   "call-mcp",
		ToolName: tool.NamespacedName("mcp__geogebra", "geogebra_create_point"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"label":"A"}`},
	}, time.Now())
	events := collector.Events()
	if len(events) != 1 || events[0].Item == nil || events[0].Item.Type != "mcp_tool_call" {
		t.Fatalf("MCP start event = %#v", events)
	}
	if events[0].Item.ID != "call-mcp" || events[0].Item.Server != "geogebra" || events[0].Item.Tool != "geogebra_create_point" {
		t.Fatalf("MCP start item = %#v", events[0].Item)
	}
}

func TestExecStreamEventCollectorEmitsWebSearchStartedOnToolStarted(t *testing.T) {
	collector := &execStreamEventCollector{}
	collector.ToolStarted(context.Background(), &tool.Invocation{
		CallID:   "call-web-search",
		ToolName: tool.NamespacedName(turn.WebSearchNamespace, turn.WebSearchRunTool),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"search_query":[{"q":"rust"}]}`},
	}, time.Now())
	events := collector.Events()
	if len(events) != 1 || events[0].Type != "item.started" || events[0].Item == nil {
		t.Fatalf("web search start event = %#v", events)
	}
	if events[0].Item.Type != "web_search" || events[0].Item.ID != "call-web-search" {
		t.Fatalf("web search start item = %#v", events[0].Item)
	}
}

func TestExecStreamEventCollectorEmitsImageGenerationStartedOnToolStarted(t *testing.T) {
	collector := &execStreamEventCollector{}
	collector.ToolStarted(context.Background(), &tool.Invocation{
		CallID:   "call-image-gen",
		ToolName: tool.NamespacedName(turn.ImageGenerationNamespace, turn.ImageGenerationToolName),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"prompt":"a red cube"}`},
	}, time.Now())
	events := collector.Events()
	if len(events) != 1 || events[0].Type != "item.started" || events[0].Item == nil {
		t.Fatalf("image generation start event = %#v", events)
	}
	if events[0].Item.Type != "imageGeneration" || events[0].Item.ID != "call-image-gen" || events[0].Item.Status != "in_progress" {
		t.Fatalf("image generation start item = %#v", events[0].Item)
	}
}

func TestEventsFromToolExecutionHideToolSearchLikeRust(t *testing.T) {
	execution := &turn.ToolExecutionResult{
		Invocation: &tool.Invocation{
			CallID:   "search-tools",
			ToolName: tool.PlainName(tool.ToolSearchName),
			Payload:  tool.Payload{Kind: tool.PayloadToolSearch, Search: map[string]any{"query": "geogebra"}},
		},
		Output: &tool.Output{CallID: "search-tools", ToolName: tool.PlainName(tool.ToolSearchName), Success: true},
	}
	if events := eventsFromToolExecution(execution); len(events) != 0 {
		t.Fatalf("tool_search should stay out of the Rust-style transcript: %#v", events)
	}

	collector := &execStreamEventCollector{}
	collector.Handle(&model.ResponsesStreamEvent{
		Kind: model.ResponsesStreamEventOutputAdded,
		Item: &model.AgentItem{ID: "search-tools", Type: "tool_search_call", CallID: "search-tools"},
	})
	if events := collector.Events(); len(events) != 0 {
		t.Fatalf("streamed tool_search should stay hidden: %#v", events)
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

func TestExecStreamEventCollectorMapsModelRerouteToErrorItemLikeRust(t *testing.T) {
	collector := &execStreamEventCollector{}
	collector.Handle(&model.ResponsesStreamEvent{
		Kind: model.ResponsesStreamEventModelReroute,
		Reroute: &model.ResponsesModelReroute{
			FromModel: "gpt-5",
			ToModel:   "gpt-5-mini",
			Reason:    "high_risk_cyber_activity",
		},
	})

	events := collector.Events()
	if len(events) != 1 || events[0].Type != "item.completed" || events[0].Item == nil {
		t.Fatalf("events = %#v", events)
	}
	if events[0].Item.Type != "error" ||
		events[0].Item.Message != "model rerouted: gpt-5 -> gpt-5-mini (HighRiskCyberActivity)" {
		t.Fatalf("error item = %#v", events[0].Item)
	}
}

func TestRunJSONSuppressesResponsesDeltasUntilFinalEvents(t *testing.T) {
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
	if strings.Contains(stdout.String(), `"type":"item.delta"`) || strings.Contains(stdout.String(), "hello ") {
		t.Fatalf("stdout before completion leaked stream delta = %q", stdout.String())
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
	if !strings.Contains(stdout.String(), `"type":"item.completed","item":{"id":"msg-1","type":"agent_message","text":"hello world"}`) {
		t.Fatalf("stdout missing final assistant item = %q", stdout.String())
	}
	if strings.Contains(stdout.String(), `"type":"item.delta"`) {
		t.Fatalf("stdout emitted non-Rust delta event: %q", stdout.String())
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
	return inputItemsContainText(request.InputItems, text)
}

func inputItemsContainText(items []any, text string) bool {
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		switch content := item["content"].(type) {
		case []map[string]any:
			for i := range content {
				if strings.Contains(fmt.Sprint(content[i]["text"]), text) {
					return true
				}
			}
		case []any:
			for _, block := range content {
				if strings.Contains(fmt.Sprint(block), text) {
					return true
				}
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

func agentRequestToolsContainType(request *model.AgentRequest, toolType string) bool {
	if request == nil {
		return false
	}
	for _, toolValue := range request.Tools {
		toolMap, ok := toolValue.(map[string]any)
		if ok && fmt.Sprint(toolMap["type"]) == toolType {
			return true
		}
	}
	return false
}

func agentRequestToolsContainPlainFunction(request *model.AgentRequest, name string) bool {
	if request == nil {
		return false
	}
	for _, toolValue := range request.Tools {
		toolMap, ok := toolValue.(map[string]any)
		if ok && fmt.Sprint(toolMap["type"]) == "function" && fmt.Sprint(toolMap["name"]) == name {
			return true
		}
	}
	return false
}

func agentRequestToolsContainNamespaceFunction(request *model.AgentRequest, namespace string, name string) bool {
	if request == nil {
		return false
	}
	return responseToolsContainNamespaceFunctionForExecTest(request.Tools, namespace, name)
}

func responseToolsContainTypeForExecTest(tools []any, toolType string) bool {
	for _, toolValue := range tools {
		toolMap, ok := toolValue.(map[string]any)
		if ok && fmt.Sprint(toolMap["type"]) == toolType {
			return true
		}
	}
	return false
}

func responseToolsContainNamespaceFunctionForExecTest(tools []any, namespace string, name string) bool {
	for _, toolValue := range tools {
		toolMap, ok := toolValue.(map[string]any)
		if !ok || fmt.Sprint(toolMap["type"]) != "namespace" || fmt.Sprint(toolMap["name"]) != namespace {
			continue
		}
		switch children := toolMap["tools"].(type) {
		case []map[string]any:
			for _, child := range children {
				if fmt.Sprint(child["name"]) == name {
					return true
				}
			}
		case []any:
			for _, childValue := range children {
				child, ok := childValue.(map[string]any)
				if ok && fmt.Sprint(child["name"]) == name {
					return true
				}
			}
		}
	}
	return false
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

type staticResponseAgent struct {
	response *model.AgentResponse
	request  *model.AgentRequest
}

func (a *staticResponseAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.request = request
	if a.response == nil {
		return &model.AgentResponse{}, nil
	}
	response := *a.response
	response.Items = append([]model.AgentItem(nil), a.response.Items...)
	return &response, nil
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

func decodeExecJSONLines(t *testing.T, output string) []protocol.ThreadEvent {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	events := make([]protocol.ThreadEvent, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var event protocol.ThreadEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("Unmarshal event line %q error = %v", line, err)
		}
		events = append(events, event)
	}
	return events
}

func execEventTypes(events []protocol.ThreadEvent) []string {
	out := make([]string, 0, len(events))
	for i := range events {
		out = append(out, events[i].Type)
	}
	return out
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

func execEventTypeAndItemIndex(events []protocol.ThreadEvent, eventType string, itemID string) int {
	for i := range events {
		if events[i].Type == eventType && events[i].Item != nil && events[i].Item.ID == itemID {
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

func writeExecResponseSSE(w http.ResponseWriter, payload string) {
	_, _ = w.Write([]byte("data: " + payload + "\n\n"))
}

func fixedExecTime() time.Time {
	return time.Date(2026, 6, 29, 1, 2, 3, 0, time.UTC)
}
