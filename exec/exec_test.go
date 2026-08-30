package exec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/auth"
	"codex_go/cli"
	"codex_go/codexapi"
	"codex_go/compact"
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

	"github.com/google/uuid"
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
	if len(lines) != 5 {
		t.Fatalf("json lines = %d, want 5: %q", len(lines), stdout.String())
	}
	var first protocol.ThreadEvent
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("Unmarshal first event returned error: %v", err)
	}
	if first.Type != "thread.started" || first.ThreadID == "" {
		t.Fatalf("first event = %#v", first)
	}
	var warning protocol.ThreadEvent
	if err := json.Unmarshal([]byte(lines[2]), &warning); err != nil {
		t.Fatalf("Unmarshal warning event returned error: %v", err)
	}
	if warning.Item == nil || warning.Item.Type != "error" || !strings.Contains(warning.Item.Message, "Code Mode is unavailable") {
		t.Fatalf("warning event = %#v", warning)
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
	if record.Metadata.Source != "exec" || record.Metadata.ThreadSource != "user" {
		t.Fatalf("session metadata = %#v", record.Metadata)
	}
	if result.TokenUsage == nil || result.TokenUsage.Total.TotalTokens <= 0 || result.TokenUsage.Last.TotalTokens <= 0 || result.TokenUsage.ModelContextWindow == nil || *result.TokenUsage.ModelContextWindow <= 0 {
		t.Fatalf("result token usage = %#v", result.TokenUsage)
	}
	stored := execStoredTokenUsage(record.Metadata.Extra)
	if stored.Total.TotalTokens != result.TokenUsage.Total.TotalTokens || stored.Last.TotalTokens != result.TokenUsage.Last.TotalTokens || stored.ModelContextWindow == nil {
		t.Fatalf("stored token usage = %#v, result = %#v", stored, result.TokenUsage)
	}
}

func TestRunResumeCompactsBeforeTurnAndEmitsActivity(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	if err := os.WriteFile(config.ConfigPath(home), []byte("model = \"gpt-5.4\"\nmodel_auto_compact_token_limit = 1\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}
	record := &session.Record{
		ID:        "thread-long",
		SessionID: "thread-long",
		CreatedAt: fixedExecTime(),
		UpdatedAt: fixedExecTime(),
		RecencyAt: fixedExecTime(),
		Metadata:  session.Metadata{Model: "gpt-5.4", ModelProvider: model.OpenAIProviderID},
		Items: []session.Item{
			{ID: "user-old", Type: "message", Role: "user", Text: strings.Repeat("很长的历史上下文", 20), CreatedAt: fixedExecTime()},
			{ID: "assistant-old", Type: "message", Role: "assistant", Text: "旧回复", CreatedAt: fixedExecTime()},
		},
	}
	if err := session.NewStore(filepath.Join(home, "sessions")).Save(record); err != nil {
		t.Fatalf("Save session returned error: %v", err)
	}
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome:     home,
		SessionID:     "thread-long",
		ThreadID:      "thread-long",
		Source:        "cli",
		ThreadSource:  string(model.AgentTaskRegular),
		Model:         "gpt-5.4",
		ModelProvider: model.OpenAIProviderID,
		HistoryMode:   "legacy",
		Now:           fixedExecTime(),
	})
	if err != nil {
		t.Fatalf("NewRecorder error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close recorder error = %v", err)
	}
	agent := &preTurnCompactAgent{}
	runner := NewRunner(home)
	runner.Agent = agent
	runner.Now = fixedExecTime
	var stdout, stderr bytes.Buffer
	result, err := runner.Run(Request{Exec: cli.ExecOptions{
		Subcommand: "resume",
		Resume:     cli.ExecResumeOptions{SessionID: "thread-long", Prompt: "继续"},
		JSON:       true,
	}}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v stderr=%q", err, stderr.String())
	}
	events := decodeExecJSONLines(t, stdout.String())
	types := execEventTypes(events)
	if slices.Contains(types, "turn.compacting") || slices.Contains(types, "turn.compacted") {
		t.Fatalf("Rust exec JSON does not emit compaction lifecycle events: %#v", types)
	}
	agentIndex, warningIndex, completedIndex := -1, -1, -1
	for i := range events {
		switch {
		case events[i].Type == "item.completed" && events[i].Item != nil && events[i].Item.Type == "agent_message":
			agentIndex = i
		case events[i].Type == "item.completed" && events[i].Item != nil && events[i].Item.Type == "error" && strings.Contains(events[i].Item.Message, "Heads up: Long threads"):
			warningIndex = i
		case events[i].Type == "turn.completed":
			completedIndex = i
		}
	}
	if agentIndex < 0 || warningIndex <= agentIndex || completedIndex <= warningIndex {
		t.Fatalf("expected agent message before compaction warning before turn.completed: %#v", types)
	}
	if len(agent.requests) != 2 || !agentRequestInputItemsContainText(&agent.requests[1], compact.SummaryPrefix) {
		t.Fatalf("agent requests after compaction = %#v", agent.requests)
	}
	if result.TokenUsage == nil || result.TokenUsage.Total.TotalTokens <= result.TokenUsage.Last.TotalTokens {
		t.Fatalf("token usage should include compaction and resumed turn: %#v", result.TokenUsage)
	}
	reloaded := loadSessionRecord(t, home, "thread-long")
	if reloaded.Metadata.Extra["compaction_phase"] != string(compact.PhasePreTurn) {
		t.Fatalf("compaction metadata = %#v", reloaded.Metadata.Extra)
	}
	path, err := rollout.FindThreadPath(home, "thread-long", false)
	if err != nil {
		t.Fatalf("FindThreadPath error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile rollout error = %v", err)
	}
	sawCompacted := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry rollout.Line
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("Unmarshal rollout line error = %v", err)
		}
		if entry.Type == "compacted" {
			sawCompacted = true
		}
	}
	if !sawCompacted {
		t.Fatalf("rollout has no compacted marker: %s", data)
	}
}

func TestRunPersistedHistoryModeUsesPaginatedForFreshPersistentThread(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	runner := NewRunner(home)
	runner.Agent = &staticResponseAgent{response: &model.AgentResponse{
		Message: "ok",
		Items:   []model.AgentItem{{ID: "msg-1", Type: "agent_message", Text: "ok"}},
		Usage:   model.AgentUsage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	}}
	var stdout, stderr bytes.Buffer
	result, err := runner.Run(Request{Exec: cli.ExecOptions{Prompt: "hello", JSON: true}}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v stderr=%q", err, stderr.String())
	}
	record := loadSessionRecord(t, home, result.ThreadID)
	if record.Metadata.HistoryMode != "paginated" {
		t.Fatalf("history mode = %q, want paginated (Rust #38774)", record.Metadata.HistoryMode)
	}
	path, err := rollout.FindThreadPath(home, result.ThreadID, false)
	if err != nil {
		t.Fatalf("FindThreadPath error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile rollout error = %v", err)
	}
	foundMeta := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry rollout.Line
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.Type == "session_meta" && entry.Meta != nil {
			if entry.Meta.HistoryMode != "paginated" {
				t.Fatalf("rollout session_meta history mode = %q, want paginated", entry.Meta.HistoryMode)
			}
			foundMeta = true
			break
		}
		if entry.Type == "session_meta" && len(entry.Payload) > 0 {
			var meta rollout.SessionMeta
			if err := json.Unmarshal(entry.Payload, &meta); err == nil {
				if meta.HistoryMode != "paginated" {
					t.Fatalf("rollout session_meta history mode = %q, want paginated", meta.HistoryMode)
				}
				foundMeta = true
				break
			}
		}
	}
	if !foundMeta {
		t.Fatalf("rollout has no session_meta line: %s", data)
	}
}

func TestRunResumePersistsExistingHistoryMode(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	base := &session.Record{
		ID:        "thread-mode",
		SessionID: "thread-mode",
		CreatedAt: fixedExecTime(),
		UpdatedAt: fixedExecTime(),
		RecencyAt: fixedExecTime(),
		Metadata:  session.Metadata{Model: "gpt-5.4", ModelProvider: model.OpenAIProviderID, HistoryMode: "paginated"},
		Items:     []session.Item{{ID: "u", Type: "message", Role: "user", Text: "history", CreatedAt: fixedExecTime()}},
	}
	if err := session.NewStore(filepath.Join(home, "sessions")).Save(base); err != nil {
		t.Fatalf("Save session returned error: %v", err)
	}
	runner := NewRunner(home)
	runner.Agent = &staticResponseAgent{response: &model.AgentResponse{
		Message: "ok",
		Items:   []model.AgentItem{{ID: "msg-1", Type: "agent_message", Text: "ok"}},
		Usage:   model.AgentUsage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	}}
	var stdout, stderr bytes.Buffer
	_, err := runner.Run(Request{Exec: cli.ExecOptions{
		Subcommand: "resume",
		Resume:     cli.ExecResumeOptions{SessionID: "thread-mode", Prompt: "continue"},
		JSON:       true,
	}}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v stderr=%q", err, stderr.String())
	}
	reloaded := loadSessionRecord(t, home, "thread-mode")
	if reloaded.Metadata.HistoryMode != "paginated" {
		t.Fatalf("history mode = %q, want paginated preserved", reloaded.Metadata.HistoryMode)
	}
}

func TestExecPersistedHistoryModeMatchesRust(t *testing.T) {
	if got := execPersistedHistoryMode(nil, true); got != "paginated" {
		t.Fatalf("fresh session history mode = %q, want paginated (Rust #38774)", got)
	}
	if got := execPersistedHistoryMode(&session.Record{}, false); got != "legacy" {
		t.Fatalf("legacy-imported session history mode = %q, want legacy preserved", got)
	}
	if got := execPersistedHistoryMode(&session.Record{Metadata: session.Metadata{HistoryMode: "paginated"}}, false); got != "paginated" {
		t.Fatalf("existing paginated history mode = %q, want paginated preserved", got)
	}
	if got := execPersistedHistoryMode(&session.Record{Metadata: session.Metadata{HistoryMode: "all"}}, false); got != "legacy" {
		t.Fatalf("invalid history mode = %q, want legacy fallback", got)
	}
}

func TestRunContextWindowExceededMarksResumeForNextPreTurnCompact(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	record := &session.Record{ID: "thread-overflow", SessionID: "thread-overflow", CreatedAt: fixedExecTime(), UpdatedAt: fixedExecTime(), RecencyAt: fixedExecTime(), Metadata: session.Metadata{Model: "gpt-5.4", ModelProvider: model.OpenAIProviderID}, Items: []session.Item{{ID: "u", Type: "message", Role: "user", Text: "history", CreatedAt: fixedExecTime()}}}
	if err := session.NewStore(filepath.Join(home, "sessions")).Save(record); err != nil {
		t.Fatalf("Save session returned error: %v", err)
	}
	runner := NewRunner(home)
	runner.Agent = &failingAgent{err: &codexapi.APIError{Kind: codexapi.ErrorContextWindowExceeded, Status: http.StatusBadRequest}}
	_, err := runner.Run(Request{Exec: cli.ExecOptions{Subcommand: "resume", Resume: cli.ExecResumeOptions{SessionID: "thread-overflow", Prompt: "continue"}}}, strings.NewReader(""), io.Discard, io.Discard)
	if err == nil {
		t.Fatal("Run returned nil error")
	}
	reloaded := loadSessionRecord(t, home, "thread-overflow")
	if !execStoredContextWindowRequired(reloaded.Metadata.Extra) {
		t.Fatalf("token status = %#v", reloaded.Metadata.Extra["token_status"])
	}
}

func TestRunJSONEmitsLegacyFeatureDeprecationBeforeTurnStarted(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	if err := os.WriteFile(config.ConfigPath(home), []byte("[features]\nweb_search_request = true\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}
	var stdout, stderr bytes.Buffer
	_, err := NewLocalRunner(home).Run(Request{
		Exec: cli.ExecOptions{Prompt: "hello", JSON: true},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v stderr=%q", err, stderr.String())
	}
	events := decodeExecJSONLines(t, stdout.String())
	wantTypes := "thread.started,item.completed,turn.started,item.completed,item.completed,turn.completed"
	if got := strings.Join(execEventTypes(events), ","); got != wantTypes {
		t.Fatalf("event types = %q, want %q", got, wantTypes)
	}
	if events[1].Item == nil || events[1].Item.ID != "item_0" || events[1].Item.Type != "error" {
		t.Fatalf("deprecation item = %#v", events[1].Item)
	}
	wantMessage := "`[features].web_search_request` is deprecated because web search is enabled by default. (Set `web_search` to `\"live\"`, `\"indexed\"`, `\"cached\"`, or `\"disabled\"` at the top level (or under a profile) in config.toml if you want to override it.)"
	if events[1].Item.Message != wantMessage {
		t.Fatalf("deprecation message = %q, want %q", events[1].Item.Message, wantMessage)
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
	if got := execEventTypes(events); strings.Join(got, ",") != "thread.started,turn.started,item.completed,item.completed,turn.completed" {
		t.Fatalf("event types = %#v stdout=%q", got, stdout.String())
	}
	if events[3].Item == nil || events[3].Item.Type != "agent_message" || events[3].Item.Text != "fixture hello" {
		t.Fatalf("agent message event = %#v", events[3])
	}
	if events[4].Usage == nil || events[4].Usage.InputTokens != 2 || events[4].Usage.OutputTokens != 3 {
		t.Fatalf("turn completed usage = %#v", events[4].Usage)
	}
}

func TestExecDefaultRouterUsesStandaloneCodeModePolicy(t *testing.T) {
	for _, testCase := range []struct {
		name            string
		toolMode        string
		disableFallback bool
		wantExec        bool
		wantDirect      bool
		warningCause    string
		warningBehavior string
	}{
		{name: "optional-falls-back-to-direct", toolMode: model.ToolModeCodeMode, wantDirect: true, warningCause: "code-mode host is disabled", warningBehavior: "Falling back to direct tools"},
		{name: "code-mode-only-fails-closed", toolMode: model.ToolModeCodeModeOnly, wantExec: true, warningCause: "code-mode host is disabled", warningBehavior: "Code mode will fail closed"},
		{name: "disabled-fallback-fails-closed", toolMode: model.ToolModeCodeMode, disableFallback: true, wantExec: true, warningCause: "failed to spawn code-mode host", warningBehavior: "Code mode will fail closed"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			agent := &recordingAgent{message: "done"}
			collector := &execStreamEventCollector{}
			runner := NewRunner(t.TempDir())
			_, err := runner.runAgentTurn(context.Background(), &Request{}, agent, &agentRunConfig{
				Prompt:                  "run",
				Model:                   "test-model",
				ToolMode:                testCase.toolMode,
				DisableCodeModeFallback: testCase.disableFallback,
				StreamEvents:            collector,
			})
			if err != nil {
				t.Fatal(err)
			}
			if got := agentRequestToolsContainResponsesTool(agent.request, "custom", tool.CodeModeExecToolName); got != testCase.wantExec {
				t.Fatalf("exec visible = %t, want %t; tools = %#v", got, testCase.wantExec, agent.request.Tools)
			}
			if got := agentRequestToolsContainPlainFunction(agent.request, tool.DefaultShellCommandToolName); got != testCase.wantDirect {
				t.Fatalf("direct shell visible = %t, want %t; tools = %#v", got, testCase.wantDirect, agent.request.Tools)
			}
			events := collector.Events()
			if len(events) != 1 || events[0].Item == nil || events[0].Item.Type != "error" ||
				!strings.Contains(events[0].Item.Message, testCase.warningCause) ||
				!strings.Contains(events[0].Item.Message, testCase.warningBehavior) {
				t.Fatalf("warning events = %#v", events)
			}
		})
	}
}

func TestExecEmptyToolModeResolvesToDirectLikeRust(t *testing.T) {
	// Custom providers such as DeepSeek have no tool_mode in model metadata.
	// The exec path must resolve that to direct mode instead of exposing the
	// code-mode exec freeform tool, which those providers reject with a 400.
	agent := &recordingAgent{message: "done"}
	collector := &execStreamEventCollector{}
	runner := NewRunner(t.TempDir())
	_, err := runner.runAgentTurn(context.Background(), &Request{}, agent, &agentRunConfig{
		Prompt:       "run",
		Model:        "deepseek-v4-flash",
		ToolMode:     "",
		StreamEvents: collector,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := agentRequestToolsContainResponsesTool(agent.request, "custom", tool.CodeModeExecToolName); got {
		t.Fatalf("code-mode exec visible for unset tool mode: %#v", agent.request.Tools)
	}
	if got := agentRequestToolsContainPlainFunction(agent.request, tool.DefaultShellCommandToolName); !got {
		t.Fatalf("direct shell tool missing: %#v", agent.request.Tools)
	}
	if events := collector.Events(); len(events) != 0 {
		t.Fatalf("unexpected warning events for direct mode: %#v", events)
	}
}

func TestExecHumanRendererRendersRuntimeWarningLikeRust(t *testing.T) {
	var stderr bytes.Buffer
	renderer := newExecHumanRenderer(&stderr, "never")
	renderer.HandleEvent(protocol.ItemCompleted(protocol.ErrorItem("warning-1", "host missing")))
	if got := stderr.String(); got != "warning: host missing\n" {
		t.Fatalf("warning output = %q", got)
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

func TestRunJSONCodeModeLegacyShellCommandEmitsLifecycleBeforeSingleFinal(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{
		Name:     tool.PlainName(tool.DefaultShellCommandToolName),
		Exposure: tool.ExposureHidden,
	}, func(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
		return &tool.Output{
			CallID:   invocation.CallID,
			ToolName: invocation.ToolName,
			Success:  false,
			Body:     "curl: (6) Could not resolve host: sdk-weather.invalid",
			Data:     map[string]any{"exit_code": 1},
		}, nil
	})); err != nil {
		t.Fatalf("register shell_command: %v", err)
	}
	codeModeExec, codeModeWait := tool.NewCodeModeExecutors(registry, tool.PlainName(tool.DefaultShellCommandToolName))
	if err := registry.Register(codeModeExec); err != nil {
		t.Fatalf("register code-mode exec: %v", err)
	}
	if err := registry.Register(codeModeWait); err != nil {
		t.Fatalf("register code-mode wait: %v", err)
	}
	runner := NewRunner(home)
	runner.Agent = &codeModeLegacyShellLoopAgent{}
	runner.ToolRouter = tool.NewRouter(registry)

	var stdout, stderr bytes.Buffer
	_, err := runner.Run(Request{
		Exec: cli.ExecOptions{Prompt: "tell me today's weather in Yunnan", JSON: true},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v stderr=%q stdout=%q", err, stderr.String(), stdout.String())
	}
	events := decodeExecJSONLines(t, stdout.String())
	commandStarted, commandCompleted, finalCompleted, turnCompleted := -1, -1, -1, -1
	commandID := ""
	commandStarts, commandCompletions, finalCompletions := 0, 0, 0
	for i := range events {
		event := events[i]
		if event.Type == "turn.completed" {
			turnCompleted = i
		}
		if event.Item == nil {
			continue
		}
		switch {
		case event.Item.Type == "command_execution" && event.Type == "item.started":
			commandStarts++
			commandStarted = i
			commandID = event.Item.ID
		case event.Item.Type == "command_execution" && event.Type == "item.completed":
			commandCompletions++
			commandCompleted = i
			if event.Item.ID != commandID || event.Item.Status != "failed" || event.Item.ExitCode == nil || *event.Item.ExitCode != 1 {
				t.Fatalf("command completion = %#v, started ID = %q", event.Item, commandID)
			}
		case event.Item.Type == "agent_message" && event.Item.Text == "Weather lookup failed once; no duplicate final.":
			finalCompletions++
			finalCompleted = i
		}
	}
	if commandStarts != 1 || commandCompletions != 1 || finalCompletions != 1 {
		t.Fatalf("lifecycle counts: command started=%d completed=%d final=%d events=%#v", commandStarts, commandCompletions, finalCompletions, events)
	}
	if commandStarted < 0 || commandCompleted <= commandStarted || finalCompleted <= commandCompleted || turnCompleted <= finalCompleted {
		t.Fatalf("event order: command started=%d completed=%d final=%d turn completed=%d events=%#v", commandStarted, commandCompleted, finalCompleted, turnCompleted, events)
	}
}

func TestRunJSONCodeModeLegacyShellCommandFailureCanRecoverBeforeSingleFinal(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	registry := tool.NewRegistry()
	var calls int
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{
		Name:     tool.PlainName(tool.DefaultShellCommandToolName),
		Exposure: tool.ExposureHidden,
	}, func(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
		calls++
		if calls == 1 {
			return &tool.Output{
				CallID: invocation.CallID, ToolName: invocation.ToolName, Success: true,
				Body: "Process exited with code 1\nOutput:\ncontrolled failure",
				Data: map[string]any{"exit_code": 1, "timed_out": false},
			}, nil
		}
		return &tool.Output{
			CallID: invocation.CallID, ToolName: invocation.ToolName, Success: true,
			Body: "Process exited with code 0\nOutput:\nRECOVERY_OK\n",
			Data: map[string]any{"exit_code": 0, "timed_out": false},
		}, nil
	})); err != nil {
		t.Fatalf("register shell_command: %v", err)
	}
	codeModeExec, codeModeWait := tool.NewCodeModeExecutors(registry, tool.PlainName(tool.DefaultShellCommandToolName))
	if err := registry.Register(codeModeExec); err != nil {
		t.Fatalf("register code-mode exec: %v", err)
	}
	if err := registry.Register(codeModeWait); err != nil {
		t.Fatalf("register code-mode wait: %v", err)
	}
	runner := NewRunner(home)
	runner.Agent = &codeModeLegacyShellRecoveryAgent{}
	runner.ToolRouter = tool.NewRouter(registry)

	var stdout, stderr bytes.Buffer
	_, err := runner.Run(Request{
		Exec: cli.ExecOptions{Prompt: "recover from a failed nested command", JSON: true},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v stderr=%q stdout=%q", err, stderr.String(), stdout.String())
	}
	events := decodeExecJSONLines(t, stdout.String())
	commandIDs := []string{}
	commandStarted := map[string]int{}
	commandCompleted := map[string]int{}
	exitCodes := []int{}
	finalCompletions := 0
	finalIndex, lastCommandIndex, turnCompleted := -1, -1, -1
	for index, event := range events {
		if event.Type == "turn.completed" {
			turnCompleted = index
		}
		if event.Item == nil {
			continue
		}
		switch {
		case event.Item.Type == "command_execution" && event.Type == "item.started":
			commandIDs = append(commandIDs, event.Item.ID)
			commandStarted[event.Item.ID]++
		case event.Item.Type == "command_execution" && event.Type == "item.completed":
			commandCompleted[event.Item.ID]++
			lastCommandIndex = index
			if event.Item.ExitCode == nil {
				t.Fatalf("command completion missing exit code: %#v", event.Item)
			}
			exitCodes = append(exitCodes, *event.Item.ExitCode)
		case event.Item.Type == "agent_message" && event.Item.Text == "CODE_MODE_RECOVERY_DONE":
			finalCompletions++
			finalIndex = index
		}
	}
	if len(commandIDs) != 2 || len(exitCodes) != 2 || exitCodes[0] != 1 || exitCodes[1] != 0 {
		t.Fatalf("commands = %#v exitCodes = %#v events = %#v", commandIDs, exitCodes, events)
	}
	for _, id := range commandIDs {
		if id == "" || commandStarted[id] != 1 || commandCompleted[id] != 1 {
			t.Fatalf("command %q lifecycle started=%d completed=%d events=%#v", id, commandStarted[id], commandCompleted[id], events)
		}
	}
	if finalCompletions != 1 || finalIndex <= lastCommandIndex || turnCompleted <= finalIndex {
		t.Fatalf("final count=%d final=%d last command=%d turn=%d events=%#v", finalCompletions, finalIndex, lastCommandIndex, turnCompleted, events)
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
		"gcode v",
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

func TestExecPermissionsInstructionsForNeverForbidsSandboxOverridesLikeRust(t *testing.T) {
	never := execPermissionsInstructions(nil, sandbox.ApprovalNever)
	if !strings.Contains(never, "Approval policy is currently never. Do not provide the `sandbox_permissions` for any reason, commands will be rejected.") {
		t.Fatalf("never permissions instructions = %q", never)
	}
	onRequest := execPermissionsInstructions(nil, sandbox.ApprovalOnRequest)
	if strings.Contains(onRequest, "Do not provide the `sandbox_permissions`") {
		t.Fatalf("on-request permissions instructions unexpectedly forbid overrides: %q", onRequest)
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
		"gcode v",
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

func TestFreshRunsWithSamePromptUseDistinctThreadAndPromptCacheKeys(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	var firstThreadID, firstCacheKey string
	for run := 0; run < 2; run++ {
		agent := &recordingAgent{message: "ok"}
		runner := NewRunner(home)
		runner.Agent = agent
		var stdout, stderr bytes.Buffer
		result, err := runner.Run(Request{Exec: cli.ExecOptions{Prompt: "same prompt"}}, strings.NewReader(""), &stdout, &stderr)
		if err != nil {
			t.Fatalf("run %d returned error: %v", run, err)
		}
		if _, err := uuid.Parse(result.ThreadID); err != nil {
			t.Fatalf("run %d ThreadID = %q, want UUID: %v", run, result.ThreadID, err)
		}
		if agent.request.PromptCacheKey != result.ThreadID {
			t.Fatalf("run %d PromptCacheKey = %q, want %q", run, agent.request.PromptCacheKey, result.ThreadID)
		}
		if run == 0 {
			firstThreadID, firstCacheKey = result.ThreadID, agent.request.PromptCacheKey
		} else if result.ThreadID == firstThreadID || agent.request.PromptCacheKey == firstCacheKey {
			t.Fatalf("fresh runs reused identity: first thread/cache=%q/%q second=%q/%q", firstThreadID, firstCacheKey, result.ThreadID, agent.request.PromptCacheKey)
		}
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
	if record.Metadata.ThreadSource != "user" {
		t.Fatalf("ThreadSource = %q, want user like Rust exec", record.Metadata.ThreadSource)
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
	if agent.request == nil || !strings.HasPrefix(agent.request.Instructions, review.ReviewPrompt) {
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
	if agent.request.PreviousResponseID != "" {
		t.Fatalf("PreviousResponseID = %q, want empty for fresh-process resume like Rust", agent.request.PreviousResponseID)
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

func TestRunExecResumePersistsUserBeforeAgentCompletes(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	now := fixedExecTime()
	threadID := "thread-active-desktop"
	store := session.NewStore(filepath.Join(home, "sessions"))
	if err := store.Save(&session.Record{
		ID:        session.ThreadID(threadID),
		SessionID: threadID,
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: session.Metadata{
			CWD:           t.TempDir(),
			Model:         "gpt-5.4",
			ModelProvider: model.OpenAIProviderID,
			Source:        "cli",
			HistoryMode:   "legacy",
		},
	}); err != nil {
		t.Fatalf("Save session returned error: %v", err)
	}
	blocking := &firstTurnBlockingAgent{started: make(chan struct{})}
	runner := NewRunner(home)
	runner.Agent = blocking
	runner.Now = func() time.Time { return now.Add(time.Minute) }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := runner.RunContext(ctx, &Request{Exec: cli.ExecOptions{
			Subcommand: "resume",
			Resume:     cli.ExecResumeOptions{SessionID: threadID, Prompt: "show this while running"},
		}}, strings.NewReader(""), io.Discard, io.Discard)
		done <- err
	}()
	select {
	case <-blocking.started:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("agent did not start")
	}
	record, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		cancel()
		t.Fatalf("Read active session error = %v", err)
	}
	if len(record.Items) != 1 || record.Items[0].Role != "user" || record.Items[0].Text != "show this while running" {
		cancel()
		t.Fatalf("active session items = %#v", record.Items)
	}
	if len(record.Metadata.RolloutTurns) != 1 || record.Metadata.RolloutTurns[0].Status != "running" {
		cancel()
		t.Fatalf("active turn state = %#v", record.Metadata.RolloutTurns)
	}
	rolloutPath, err := rollout.FindThreadPath(home, threadID, false)
	if err != nil {
		cancel()
		t.Fatalf("FindThreadPath() error = %v", err)
	}
	data, err := os.ReadFile(rolloutPath)
	if err != nil {
		cancel()
		t.Fatalf("ReadFile rollout error = %v", err)
	}
	if !bytes.Contains(data, []byte(`"type":"task_started"`)) || !bytes.Contains(data, []byte(`"type":"user_message"`)) || !bytes.Contains(data, []byte(`"message":"show this while running"`)) {
		cancel()
		t.Fatalf("active rollout is not Desktop-readable: %s", data)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("RunContext error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled run did not finish")
	}
}

func TestResumeInputItemsKeepsLocalHistoryWithResponseID(t *testing.T) {
	ctx := &execResumeContext{Record: &session.Record{
		Metadata: session.Metadata{CWD: t.TempDir(), LastResponseID: "resp-last"},
		Items: []session.Item{
			{ID: "old-user", Type: "message", Role: "user", Text: "云南天气"},
			{ID: "old-assistant", Type: "agent_message", Role: "assistant", Text: "云南天气回答"},
		},
	}}
	items := resumeInputItems(ctx)
	request := &model.AgentRequest{InputItems: items}
	if !agentRequestInputItemsHaveText(request, "云南天气") || !agentRequestInputItemsHaveText(request, "云南天气回答") {
		t.Fatalf("resume input items = %#v, want local history retained", items)
	}
}

func TestSessionItemForToolOutputPreservesToolSearchPayloadKind(t *testing.T) {
	now := fixedExecTime()
	execution := &turn.ToolExecutionResult{
		Invocation: &tool.Invocation{
			CallID:   "search-1",
			ToolName: tool.PlainName("tool_search"),
			Payload:  tool.Payload{Kind: tool.PayloadToolSearch},
		},
		Output: &tool.Output{Body: `{"tools":[]}`, Success: true},
	}
	item, ok := sessionItemForToolOutput("turn-1", "1", execution, now, nil)
	if !ok {
		t.Fatal("sessionItemForToolOutput() = false")
	}
	if item.Metadata["payloadKind"] != string(tool.PayloadToolSearch) {
		t.Fatalf("metadata = %#v", item.Metadata)
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

func TestResolveExecResumeLastImportsRolloutOnlyThread(t *testing.T) {
	home := t.TempDir()
	cwdA := t.TempDir()
	cwdB := t.TempDir()
	now := fixedExecTime()
	older := createExecResumeRolloutOnly(t, home, "rollout-cwd-a", cwdA, now, "old a")
	newer := createExecResumeRolloutOnly(t, home, "rollout-cwd-b", cwdB, now.Add(time.Minute), "old b")
	if err := os.Chtimes(older, now, now); err != nil {
		t.Fatalf("Chtimes older rollout returned error: %v", err)
	}
	if err := os.Chtimes(newer, now.Add(time.Minute), now.Add(time.Minute)); err != nil {
		t.Fatalf("Chtimes newer rollout returned error: %v", err)
	}
	allRecord, err := latestExecResumeRolloutRecord(home, &cli.ExecResumeOptions{Last: true, All: true}, cwdA)
	if err != nil {
		t.Fatalf("rollout-only --all resolve returned error: %v", err)
	}
	if allRecord.ID != "rollout-cwd-b" {
		t.Fatalf("rollout-only --all selected = %q, want newest rollout", allRecord.ID)
	}

	runner := NewRunner(home)
	record, err := runner.resolveExecResumeRecord(&Request{Exec: cli.ExecOptions{
		Shared: cli.SharedOptions{CWD: cwdA},
		Resume: cli.ExecResumeOptions{Last: true},
	}})
	if err != nil {
		t.Fatalf("cwd-filtered resolve returned error: %v", err)
	}
	if record.ID != "rollout-cwd-a" || len(record.Items) == 0 || record.Items[0].Text != "old a" {
		t.Fatalf("cwd-filtered record = %#v", record)
	}

	store := session.NewStore(filepath.Join(home, "sessions"))
	if _, err := store.Read("rollout-cwd-a", true, true); err != nil {
		t.Fatalf("imported store record returned error: %v", err)
	}
	record, err = runner.resolveExecResumeRecord(&Request{Exec: cli.ExecOptions{
		Shared: cli.SharedOptions{CWD: cwdA},
		Resume: cli.ExecResumeOptions{Last: true, All: true},
	}})
	if err != nil {
		t.Fatalf("--all resolve returned error: %v", err)
	}
	if record.ID != "rollout-cwd-a" {
		t.Fatalf("existing indexed record must stay authoritative, got %q", record.ID)
	}
}

func TestResolveExecResumeByNameImportsRolloutOnlyThread(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	createExecResumeRolloutOnly(t, home, "rollout-named", cwd, fixedExecTime(), "named history")
	if err := rollout.AppendThreadName(home, "rollout-named", "Design Review"); err != nil {
		t.Fatalf("AppendThreadName returned error: %v", err)
	}

	runner := NewRunner(home)
	record, err := runner.resolveExecResumeRecord(&Request{Exec: cli.ExecOptions{
		Shared: cli.SharedOptions{CWD: cwd},
		Resume: cli.ExecResumeOptions{SessionID: "Design Review"},
	}})
	if err != nil {
		t.Fatalf("named resolve returned error: %v", err)
	}
	if record.ID != "rollout-named" || record.Items[0].Text != "named history" {
		t.Fatalf("named record = %#v", record)
	}
	if _, err := session.NewStore(filepath.Join(home, "sessions")).Read("rollout-named", true, true); err != nil {
		t.Fatalf("named import store record returned error: %v", err)
	}
}

func TestRunExecForkCreatesDistinctThreadsWithAndWithoutPrompt(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	now := fixedExecTime()
	store := session.NewStore(filepath.Join(home, "sessions"))
	source := &session.Record{
		ID:        "fork-source",
		SessionID: "fork-source",
		Preview:   "source question",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: session.Metadata{
			CWD:           cwd,
			Model:         "gpt-5.4",
			ModelProvider: model.OpenAIProviderID,
			Source:        "exec",
			ThreadSource:  "user",
			HistoryMode:   "legacy",
		},
		Items: []session.Item{
			{ID: "source-user", Type: "message", Role: "user", Text: "source question", CreatedAt: now},
			{ID: "source-assistant", Type: "message", Role: "assistant", Text: "source answer", CreatedAt: now},
		},
	}
	if err := store.Save(source); err != nil {
		t.Fatalf("Save source returned error: %v", err)
	}
	runner := NewRunner(home)
	agent := &recordingAgent{message: "fork completed"}
	runner.Agent = agent
	runner.Now = func() time.Time { return now.Add(time.Minute) }
	if err := runner.createExecRollout(source, now); err != nil {
		t.Fatalf("createExecRollout source returned error: %v", err)
	}

	var promptlessOut bytes.Buffer
	promptless, err := runner.Run(Request{Exec: cli.ExecOptions{
		Subcommand: "fork",
		JSON:       true,
		Shared:     cli.SharedOptions{CWD: cwd},
		Fork:       cli.ExecForkOptions{SessionID: "fork-source"},
	}}, strings.NewReader("stdin must remain unread"), &promptlessOut, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("promptless fork returned error: %v", err)
	}
	if promptless.ThreadID == "" || promptless.ThreadID == "fork-source" {
		t.Fatalf("promptless thread id = %q", promptless.ThreadID)
	}
	events := decodeExecJSONLines(t, promptlessOut.String())
	if len(events) != 1 || events[0].Type != "thread.started" || events[0].ThreadID != promptless.ThreadID {
		t.Fatalf("promptless events = %#v", events)
	}
	if agent.request != nil {
		t.Fatalf("promptless fork unexpectedly called agent: %#v", agent.request)
	}
	promptlessRecord := loadSessionRecord(t, home, promptless.ThreadID)
	if promptlessRecord.ForkedFromID != source.ID || len(promptlessRecord.Items) != len(source.Items) {
		t.Fatalf("promptless record = %#v", promptlessRecord)
	}
	source.Title = "Named Fork Source"
	if err := store.Save(source); err != nil {
		t.Fatalf("name source returned error: %v", err)
	}
	if err := rollout.AppendThreadName(home, string(source.ID), source.Title); err != nil {
		t.Fatalf("AppendThreadName returned error: %v", err)
	}

	runner.Now = func() time.Time { return now.Add(2 * time.Minute) }
	var promptedOut bytes.Buffer
	prompted, err := runner.Run(Request{Exec: cli.ExecOptions{
		Subcommand: "fork",
		JSON:       true,
		Shared:     cli.SharedOptions{CWD: cwd},
		Fork:       cli.ExecForkOptions{SessionID: "Named Fork Source", Prompt: "-"},
	}}, strings.NewReader("continue on the fork"), &promptedOut, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("prompted fork returned error: %v", err)
	}
	if prompted.ThreadID == "fork-source" || prompted.ThreadID == promptless.ThreadID {
		t.Fatalf("prompted thread id = %q", prompted.ThreadID)
	}
	if agent.request == nil || !agentRequestInputItemsContainText(agent.request, "source question") || agent.request.Prompt != "continue on the fork" {
		t.Fatalf("prompted fork agent request = %#v", agent.request)
	}
	promptedRecord := loadSessionRecord(t, home, prompted.ThreadID)
	if promptedRecord.ForkedFromID != source.ID || !sessionItemsContainText(promptedRecord.Items, "source question") || !sessionItemsContainText(promptedRecord.Items, "continue on the fork") {
		t.Fatalf("prompted record = %#v", promptedRecord)
	}
	reloadedSource := loadSessionRecord(t, home, string(source.ID))
	if len(reloadedSource.Items) != len(source.Items) || !reloadedSource.UpdatedAt.Equal(source.UpdatedAt) {
		t.Fatalf("source changed after fork: before=%#v after=%#v", source, reloadedSource)
	}
}

func TestRunExecPromptlessForkRejectsTurnOnlyOptions(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	tests := []struct {
		name string
		exec cli.ExecOptions
		want string
	}{
		{name: "images", exec: cli.ExecOptions{Fork: cli.ExecForkOptions{SessionID: "source", Images: []string{"unused.png"}}}, want: "Forking with images requires a prompt"},
		{name: "output schema", exec: cli.ExecOptions{Fork: cli.ExecForkOptions{SessionID: "source"}, OutputSchema: "unused.json"}, want: "Forking with output options requires a prompt"},
		{name: "last message", exec: cli.ExecOptions{Fork: cli.ExecForkOptions{SessionID: "source"}, LastMessageFile: "unused.md"}, want: "Forking with output options requires a prompt"},
		{name: "ephemeral", exec: cli.ExecOptions{Fork: cli.ExecForkOptions{SessionID: "source"}, Ephemeral: true}, want: "Ephemeral forks require a prompt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.exec.Subcommand = "fork"
			_, err := NewLocalRunner(home).Run(Request{Exec: tt.exec}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
			if err == nil || err.Error() != tt.want {
				t.Fatalf("Run error = %v, want %q", err, tt.want)
			}
		})
	}
}

func sessionItemsContainText(items []session.Item, text string) bool {
	for i := range items {
		if strings.Contains(items[i].Text, text) {
			return true
		}
	}
	return false
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

func TestEffectiveExecApprovalPolicyReviewForcesNeverLikeRust(t *testing.T) {
	// Rust tasks/review.rs + 95aada11c4 (#38205): review delegates always run
	// with approval policy `never`, rejecting any prompt-capable policy.
	req := &Request{
		Exec: cli.ExecOptions{
			Subcommand: "review",
			Shared:     cli.SharedOptions{ApprovalPolicy: string(sandbox.ApprovalOnRequest)},
		},
		Root: cli.RootOptions{Shared: cli.SharedOptions{ApprovalPolicy: string(sandbox.ApprovalGranular)}},
	}
	cfg := &config.Config{Values: map[string]any{
		"approval_policy":    string(sandbox.ApprovalOnRequest),
		"approvals_reviewer": string(config.ApprovalsReviewerAutoReview),
	}}
	if got := effectiveExecApprovalPolicy(cfg, req); got != sandbox.ApprovalNever {
		t.Fatalf("review approval policy = %q, want never", got)
	}
}

func TestToolRouterUsesExecHeadlessApprovalPolicyLikeRust(t *testing.T) {
	runner := NewLocalRunner(t.TempDir())
	req := &Request{Exec: cli.ExecOptions{Prompt: "hello"}}
	invocation := &tool.Invocation{
		CallID:   "call-approval",
		ToolName: tool.PlainName(tool.DefaultShellCommandToolName),
		Payload: tool.Payload{
			Kind:      tool.PayloadFunction,
			Arguments: `{"command":"echo hi","sandbox_permissions":"require_escalated","justification":"need more access"}`,
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

func TestToolRouterRegistersRealMultiAgentV2Tools(t *testing.T) {
	home := t.TempDir()
	runner := NewLocalRunner(home)
	req := &Request{Exec: cli.ExecOptions{Prompt: "delegate", Shared: cli.SharedOptions{CWD: t.TempDir()}}}
	cfg := &config.Config{Values: map[string]any{
		"features": map[string]any{"multi_agent_v2": map[string]any{"enabled": true, "wait_agent_enabled": true}},
		"agents":   map[string]any{"max_concurrent_threads_per_session": int64(4)},
	}}
	tools, err := runner.multiAgentToolsForRun(context.Background(), req, cfg, "thread-root", "turn-root", nil)
	if err != nil {
		t.Fatalf("multiAgentToolsForRun() error = %v", err)
	}
	if tools == nil || tools.controller == nil || tools.disableWait {
		t.Fatalf("multi-agent tools = %#v", tools)
	}
	if tools.maxConcurrency != 5 {
		t.Fatalf("max concurrency = %d, want 5", tools.maxConcurrency)
	}
	router, err := runner.toolRouterForRequest(req, &agentRunConfig{
		AgentController: tools.controller, AgentExposure: tools.exposure, AgentVersion: tools.version, AgentNamespace: tools.namespace,
		AgentRoles: tools.roles, AgentDefaults: tools.defaults, DisableWaitAgent: tools.disableWait,
		AgentWaitDefault: tools.waitDefault, AgentWaitMin: tools.waitMin, AgentWaitMax: tools.waitMax, AgentWaitConfigured: true,
		AgentHideSpawnMetadata: tools.hideSpawnMetadata, AgentExposeSpawnModelOverrides: tools.exposeSpawnModelOverrides,
	})
	if err != nil {
		t.Fatalf("toolRouterForRequest() error = %v", err)
	}
	visible := map[string]bool{}
	for _, spec := range router.ModelVisibleSpecs() {
		visible[spec.Name.Key()] = true
	}
	for _, name := range []string{
		"collaboration.spawn_agent", "collaboration.send_message", "collaboration.followup_task",
		"collaboration.wait_agent", "collaboration.interrupt_agent", "collaboration.list_agents",
	} {
		if !visible[name] {
			t.Fatalf("model-visible tools = %#v, missing %s", visible, name)
		}
	}
	if visible["multi_agent_v1.spawn_agent"] {
		t.Fatalf("legacy multi_agent_v1 namespace leaked into v2 tools: %#v", visible)
	}
}

func TestExecMultiAgentVersionForRunMatchesCatalogAndConfigPrecedence(t *testing.T) {
	modelsManager := model.NewStaticModelsManager(model.ModelsResponse{Models: []model.ModelInfo{
		{Slug: "catalog-v2", DisplayName: "catalog-v2", Visibility: model.VisibilityVisible, SupportedInAPI: true, MultiAgentVersion: "v2"},
		{Slug: "catalog-v1", DisplayName: "catalog-v1", Visibility: model.VisibilityVisible, SupportedInAPI: true, MultiAgentVersion: "v1"},
		{Slug: "catalog-disabled", DisplayName: "catalog-disabled", Visibility: model.VisibilityVisible, SupportedInAPI: true, MultiAgentVersion: "disabled"},
		{Slug: "catalog-unspecified", DisplayName: "catalog-unspecified", Visibility: model.VisibilityVisible, SupportedInAPI: true},
	}})
	enabled := true
	disabled := false
	tests := []struct {
		name          string
		model         string
		features      map[string]any
		agentsEnabled *bool
		inherited     agent.MultiAgentVersion
		want          agent.MultiAgentVersion
	}{
		{name: "catalog default", want: agent.VersionV2},
		{name: "catalog v2", model: "catalog-v2", want: agent.VersionV2},
		{name: "catalog v1", model: "catalog-v1", want: agent.VersionV1},
		{name: "catalog disabled", model: "catalog-disabled", want: ""},
		{name: "stable fallback", model: "catalog-unspecified", want: agent.VersionV1},
		{name: "agents disabled overrides catalog", model: "catalog-v2", agentsEnabled: &disabled, want: ""},
		{name: "explicit v2 overrides agents disabled", model: "catalog-v1", features: map[string]any{"multi_agent_v2": map[string]any{"enabled": true}}, agentsEnabled: &disabled, want: agent.VersionV2},
		{name: "inherited session version overrides catalog", model: "catalog-v2", inherited: agent.VersionV1, want: agent.VersionV1},
		{name: "stable fallback can be disabled", model: "catalog-unspecified", features: map[string]any{"multi_agent": map[string]any{"enabled": false}}, want: ""},
		{name: "agents explicitly enabled keeps catalog", model: "catalog-v2", agentsEnabled: &enabled, want: agent.VersionV2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			values := map[string]any{}
			if tc.features != nil {
				values["features"] = tc.features
			}
			cfg := &config.Config{Values: values}
			req := &Request{
				Exec:              cli.ExecOptions{Shared: cli.SharedOptions{Model: tc.model}},
				multiAgentVersion: tc.inherited,
			}
			agentsConfig := &config.AgentsConfig{Enabled: tc.agentsEnabled}
			if got := execMultiAgentVersionForRun(req, cfg, agentsConfig, modelsManager); got != tc.want {
				t.Fatalf("execMultiAgentVersionForRun() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestToolRouterUsesCatalogSelectedV2WithoutFeatureFlag(t *testing.T) {
	home := t.TempDir()
	runner := NewLocalRunner(home)
	modelsManager := model.NewStaticModelsManager(model.ModelsResponse{Models: []model.ModelInfo{{
		Slug: "catalog-v2", DisplayName: "catalog-v2", Visibility: model.VisibilityVisible, SupportedInAPI: true, MultiAgentVersion: "v2",
	}}})
	req := &Request{Exec: cli.ExecOptions{Prompt: "delegate", Shared: cli.SharedOptions{CWD: t.TempDir(), Model: "catalog-v2"}}}
	cfg := &config.Config{Values: map[string]any{}}
	tools, err := runner.multiAgentToolsForRun(context.Background(), req, cfg, "thread-root", "turn-root", &model.ResponsesAgentRunner{ModelsManager: modelsManager})
	if err != nil {
		t.Fatalf("multiAgentToolsForRun() error = %v", err)
	}
	if tools == nil || tools.version != agent.VersionV2 || tools.controller == nil {
		t.Fatalf("catalog-selected multi-agent tools = %#v", tools)
	}
	t.Cleanup(func() { closeExecMultiAgentTools(tools) })
	router, err := runner.toolRouterForRequest(req, &agentRunConfig{
		AgentController: tools.controller, AgentExposure: tools.exposure, AgentVersion: tools.version, AgentNamespace: tools.namespace,
		AgentRoles: tools.roles, AgentDefaults: tools.defaults, DisableWaitAgent: tools.disableWait,
		AgentWaitDefault: tools.waitDefault, AgentWaitMin: tools.waitMin, AgentWaitMax: tools.waitMax, AgentWaitConfigured: true,
		AgentHideSpawnMetadata: tools.hideSpawnMetadata, AgentExposeSpawnModelOverrides: tools.exposeSpawnModelOverrides,
	})
	if err != nil {
		t.Fatalf("toolRouterForRequest() error = %v", err)
	}
	visible := map[string]bool{}
	for _, spec := range router.ModelVisibleSpecs() {
		visible[spec.Name.Key()] = true
	}
	for _, name := range []string{
		"collaboration.spawn_agent", "collaboration.send_message", "collaboration.followup_task",
		"collaboration.wait_agent", "collaboration.interrupt_agent", "collaboration.list_agents",
	} {
		if !visible[name] {
			t.Fatalf("model-visible tools = %#v, missing %s", visible, name)
		}
	}
	if got := execMultiAgentVersionForRequest(req); got != string(agent.VersionV2) {
		t.Fatalf("persisted request multi-agent version = %q, want v2", got)
	}
}

func TestToolRouterUsesCatalogSelectedV1WithoutFeatureFlag(t *testing.T) {
	runner := NewLocalRunner(t.TempDir())
	modelsManager := model.NewStaticModelsManager(model.ModelsResponse{Models: []model.ModelInfo{{
		Slug: "catalog-v1", DisplayName: "catalog-v1", Visibility: model.VisibilityVisible, SupportedInAPI: true, MultiAgentVersion: "v1",
	}}})
	req := &Request{Exec: cli.ExecOptions{Prompt: "delegate", Shared: cli.SharedOptions{CWD: t.TempDir(), Model: "catalog-v1"}}}
	tools, err := runner.multiAgentToolsForRun(
		context.Background(),
		req,
		&config.Config{Values: map[string]any{}},
		"thread-root",
		"turn-root",
		&model.ResponsesAgentRunner{ModelsManager: modelsManager},
	)
	if err != nil {
		t.Fatalf("multiAgentToolsForRun() error = %v", err)
	}
	if tools == nil || tools.version != agent.VersionV1 || tools.namespace != agent.MultiAgentV1Namespace {
		t.Fatalf("catalog-selected V1 tools = %#v", tools)
	}
	t.Cleanup(func() { closeExecMultiAgentTools(tools) })
	router, err := runner.toolRouterForRequest(req, &agentRunConfig{
		AgentController: tools.controller, AgentExposure: tools.exposure, AgentVersion: tools.version, AgentNamespace: tools.namespace,
		AgentRoles: tools.roles, AgentDefaults: tools.defaults, DisableWaitAgent: tools.disableWait,
		AgentWaitDefault: tools.waitDefault, AgentWaitMin: tools.waitMin, AgentWaitMax: tools.waitMax, AgentWaitConfigured: true,
	})
	if err != nil {
		t.Fatalf("toolRouterForRequest() error = %v", err)
	}
	visible := map[string]bool{}
	for _, spec := range router.ModelVisibleSpecs() {
		visible[spec.Name.Key()] = true
	}
	for _, name := range []string{"multi_agent_v1.spawn_agent", "multi_agent_v1.send_input", "multi_agent_v1.wait_agent", "multi_agent_v1.resume_agent", "multi_agent_v1.close_agent"} {
		if !visible[name] {
			t.Fatalf("model-visible tools = %#v, missing %s", visible, name)
		}
	}
	if visible["collaboration.spawn_agent"] {
		t.Fatalf("V2 namespace leaked into V1 tools: %#v", visible)
	}
}

func TestExecMultiAgentV2UsageHintMatchesRootAndSubagentContract(t *testing.T) {
	options := &execMultiAgentTools{
		version: agent.VersionV2, maxConcurrency: 4, exposeSpawnModelOverrides: true,
	}
	root := execMultiAgentV2UsageHint(&Request{}, options)
	for _, fragment := range []string{
		"You are `/root`", "Message Type: MESSAGE | FINAL_ANSWER", "functions.collaboration.spawn_agent",
		"There are 4 available concurrency slots", "Only set `model` or `reasoning_effort` when explicitly requested",
		"When calling `wait_agent`, prefer longer waits (minutes) to avoid busy polling.",
	} {
		if !strings.Contains(root, fragment) {
			t.Fatalf("root usage hint missing %q:\n%s", fragment, root)
		}
	}
	subagent := execMultiAgentV2UsageHint(&Request{subagent: &execSubagentContext{AgentPath: "/root/worker"}}, options)
	for _, fragment := range []string{"You are an agent in a team", "Message Type: NEW_TASK | MESSAGE | FINAL_ANSWER", "delivered back to your parent agent"} {
		if !strings.Contains(subagent, fragment) {
			t.Fatalf("subagent usage hint missing %q:\n%s", fragment, subagent)
		}
	}
}

func TestExecMultiAgentV2UsageHintOmitsWaitAgentGuidanceWhenDisabledLikeRust(t *testing.T) {
	options := &execMultiAgentTools{
		version: agent.VersionV2, maxConcurrency: 4, exposeSpawnModelOverrides: true, disableWait: true,
	}
	hint := execMultiAgentV2UsageHint(&Request{}, options)
	if strings.Contains(hint, "prefer longer waits") {
		t.Fatalf("usage hint includes wait_agent guidance while disabled (Rust 92b83e226d):\n%s", hint)
	}
	if !strings.Contains(hint, "There are 4 available concurrency slots") {
		t.Fatalf("usage hint missing concurrency slots while wait_agent disabled:\n%s", hint)
	}
}

func TestExecAgentControllerValidatesV2SpawnModelOverrides(t *testing.T) {
	controller := newExecAgentController(NewLocalRunner(t.TempDir()), context.Background(), &Request{}, "thread-root", 3).(*execAgentController)
	controller.parentModel = "catalog-v2"
	controller.modelsManager = model.NewStaticModelsManager(model.ModelsResponse{Models: []model.ModelInfo{
		{
			Slug: "catalog-v2", DisplayName: "catalog-v2", Visibility: model.VisibilityVisible, SupportedInAPI: true,
			MultiAgentVersion: "v2", DefaultReasoningLevel: "medium", SupportedReasoningLevels: []string{"low", "medium", "high"}, ServiceTiers: []string{"priority"},
		},
		{
			Slug: "catalog-v1", DisplayName: "catalog-v1", Visibility: model.VisibilityVisible, SupportedInAPI: true,
			MultiAgentVersion: "v1", DefaultReasoningLevel: "medium", SupportedReasoningLevels: []string{"medium"},
		},
	}})
	unsupportedModel := "catalog-v1"
	if err := controller.resolveSpawnModelOverrides(&agent.SpawnAgentArgs{Model: &unsupportedModel}); err == nil || !strings.Contains(err.Error(), "Unknown model `catalog-v1`") || !strings.Contains(err.Error(), "Available models: catalog-v2") {
		t.Fatalf("unsupported model error = %v", err)
	}
	unsupportedEffort := "ultra"
	if err := controller.resolveSpawnModelOverrides(&agent.SpawnAgentArgs{ReasoningEffort: &unsupportedEffort}); err == nil || !strings.Contains(err.Error(), "Supported reasoning efforts: low, medium, high") {
		t.Fatalf("unsupported reasoning error = %v", err)
	}
	validModel := "catalog-v2"
	args := &agent.SpawnAgentArgs{Model: &validModel}
	if err := controller.resolveSpawnModelOverrides(args); err != nil {
		t.Fatal(err)
	}
	if args.ReasoningEffort == nil || *args.ReasoningEffort != "medium" {
		t.Fatalf("default reasoning effort = %#v, want medium", args.ReasoningEffort)
	}
	unsupportedTier := "flex"
	// Rust #41308: per-spawn service-tier overrides are removed; subagents follow
	// the root thread's tier. resolveSpawnModelOverrides must ignore a provided
	// service_tier rather than validating or resolving it.
	if err := controller.resolveSpawnModelOverrides(&agent.SpawnAgentArgs{ServiceTier: &unsupportedTier}); err != nil {
		t.Fatalf("per-spawn service tier should be ignored, got error = %v", err)
	}
	fastTier := "fast"
	tierArgs := &agent.SpawnAgentArgs{ServiceTier: &fastTier}
	if err := controller.resolveSpawnModelOverrides(tierArgs); err != nil {
		t.Fatal(err)
	}
	if tierArgs.ServiceTier == nil || *tierArgs.ServiceTier != "fast" {
		t.Fatalf("per-spawn service tier should be left unmodified, got %#v", tierArgs.ServiceTier)
	}
}

func TestExecAgentControllerV1DepthLimitMatchesRust(t *testing.T) {
	controller := newExecAgentController(NewLocalRunner(t.TempDir()), context.Background(), &Request{}, "thread-root", 4).(*execAgentController)
	t.Cleanup(controller.shutdown)
	controller.multiAgentVersion = agent.VersionV1
	controller.maxDepth = 1
	// A depth-1 agent cannot spawn or resume (Rust: "Agent depth limit reached.
	// Solve the task yourself.").
	childView := controller.scoped("/root/worker", "worker-id")
	childView.depth = 1
	message := "task"
	if _, err := childView.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{TaskName: "nested", Message: &message, ForkTurns: execStringPointer("none")}); !errors.Is(err, agent.ErrAgentDepthLimitReached) {
		t.Fatalf("nested spawn error = %v", err)
	}
	if _, err := childView.ResumeAgent(context.Background(), &agent.ResumeAgentArgs{ID: "worker-id"}); !errors.Is(err, agent.ErrAgentDepthLimitReached) {
		t.Fatalf("nested resume error = %v", err)
	}
	// Root (depth 0) can spawn a depth-1 agent.
	spawned, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{TaskName: "worker", Message: &message, ForkTurns: execStringPointer("none")})
	if err != nil {
		t.Fatalf("root spawn error = %v", err)
	}
	if task := controller.task(spawned.AgentID); task == nil || task.depth != 1 {
		t.Fatalf("task depth = %#v", task)
	}
}

func TestMultiAgentToolsForRunHidesV1ToolsBeyondDepthLimit(t *testing.T) {
	runner := NewLocalRunner(t.TempDir())
	cfg := &config.Config{Values: map[string]any{
		"agents": map[string]any{"max_concurrent_threads_per_session": int64(4)},
	}}
	// A depth-1 subagent with default max_depth=1 must not get V1 collab tools.
	req := &Request{subagent: &execSubagentContext{Depth: 1, Version: agent.VersionV1}}
	tools, err := runner.multiAgentToolsForRun(context.Background(), req, cfg, "thread-child", "turn-child", nil)
	if err != nil {
		t.Fatalf("multiAgentToolsForRun() error = %v", err)
	}
	if tools != nil {
		t.Fatalf("depth-limited V1 subagent still received agent tools: %#v", tools)
	}
	// The same subagent under V2 keeps the tools (max_depth is V1-only).
	req.multiAgentVersion = ""
	req.subagent.Version = agent.VersionV2
	req.subagent.Controller = newExecAgentController(runner, context.Background(), &Request{}, "thread-child", 4)
	tools, err = runner.multiAgentToolsForRun(context.Background(), req, cfg, "thread-child", "turn-child", nil)
	if err != nil || tools == nil || tools.controller == nil {
		t.Fatalf("V2 depth-limited subagent tools = %#v, %v", tools, err)
	}
}

func TestExecAgentControllerV2LifecyclePathsAndSingleRollout(t *testing.T) {
	home := t.TempDir()
	runner := NewLocalRunner(home)
	recording := &recordingStaticAgent{
		requests: make(chan model.AgentRequest, 4),
		response: &model.AgentResponse{
			Message: "done",
			Items: []model.AgentItem{{
				ID: "final", Type: "agent_message", Text: "done", Data: map[string]any{"phase": "final_answer"},
			}},
			Usage: model.AgentUsage{InputTokens: 1, OutputTokens: 1}, Model: "gpt-test", ProviderID: "local",
		},
	}
	runner.Agent = recording
	controller := newExecAgentController(runner, context.Background(), &Request{Exec: cli.ExecOptions{Prompt: "root", Shared: cli.SharedOptions{CWD: t.TempDir()}}}, "thread-root", 3).(*execAgentController)
	t.Cleanup(controller.shutdown)
	message := "first task"
	spawned, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{TaskName: "worker", Message: &message, ForkTurns: execStringPointer("none")})
	if err != nil || spawned.TaskName != "/root/worker" {
		t.Fatalf("SpawnAgent() = %#v, %v", spawned, err)
	}
	firstRequest := awaitRecordedAgentRequest(t, recording.requests)
	assertEncryptedAgentCommunication(t, &firstRequest, "/root", "/root/worker", "NEW_TASK", "first task")
	activity, err := controller.WaitForActivity(context.Background(), nil)
	if err != nil || activity.TimedOut || activity.Message != "Wait completed." {
		t.Fatalf("first activity = %#v, %v", activity, err)
	}
	if err := controller.SendMessage(context.Background(), &agent.SendMessageArgs{Target: "worker", Message: "queued context"}); err != nil {
		t.Fatal(err)
	}
	if err := controller.FollowupTask(context.Background(), &agent.FollowupTaskArgs{Target: "/root/worker", Message: "second task"}); err != nil {
		t.Fatal(err)
	}
	secondRequest := awaitRecordedAgentRequest(t, recording.requests)
	assertNoEmptyInputTextBlocks(t, &secondRequest)
	assertEncryptedAgentCommunication(t, &secondRequest, "/root", "/root/worker", "MESSAGE", "queued context")
	assertEncryptedAgentCommunication(t, &secondRequest, "/root", "/root/worker", "NEW_TASK", "second task")
	activity, err = controller.WaitForActivity(context.Background(), nil)
	if err != nil || activity.TimedOut {
		t.Fatalf("followup activity = %#v, %v", activity, err)
	}
	scoped := controller.scoped("/root/worker", spawned.AgentID)
	childMessage := "nested task"
	nested, err := scoped.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{TaskName: "nested", Message: &childMessage, ForkTurns: execStringPointer("none")})
	if err != nil || nested.TaskName != "/root/worker/nested" {
		t.Fatalf("nested spawn = %#v, %v", nested, err)
	}
	list, err := controller.ListAgents(context.Background(), &agent.ListAgentsArgs{})
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(list.Agents))
	for _, item := range list.Agents {
		names = append(names, item.AgentName)
	}
	if !slices.Contains(names, "/root") || !slices.Contains(names, "/root/worker") || !slices.Contains(names, "/root/worker/nested") {
		t.Fatalf("agent paths = %#v", names)
	}
	if got := countExecRolloutsForThread(t, home, spawned.AgentID); got != 1 {
		t.Fatalf("rollout files for %s = %d, want 1", spawned.AgentID, got)
	}
}

func TestExecAgentControllerRoutesChildMessageToActiveRootTurn(t *testing.T) {
	controller := newExecAgentController(NewLocalRunner(t.TempDir()), context.Background(), &Request{}, "thread-root", 3).(*execAgentController)
	controller.setActiveTurn("turn-root")
	child := controller.scoped("/root/worker", "thread-worker")

	if err := child.SendMessage(context.Background(), &agent.SendMessageArgs{Target: "/root", Message: "verified result", Plaintext: true}); err != nil {
		t.Fatal(err)
	}
	activity, err := controller.WaitForActivity(context.Background(), nil)
	if err != nil || activity.TimedOut || !strings.Contains(activity.Message, "/root/worker") {
		t.Fatalf("root activity = %#v, %v", activity, err)
	}
	drained := controller.steerMailbox.Drain(&turn.SteerDrainParams{ThreadID: "thread-root", TurnID: "turn-root"})
	if len(drained) != 1 || !inputItemsContainText(drained, "Message Type: MESSAGE") || !inputItemsContainText(drained, "Sender: /root/worker") || !inputItemsContainText(drained, "verified result") {
		t.Fatalf("root steer input = %#v", drained)
	}
}

func TestExecAgentControllerRoutesRootMessageAndFollowupToActiveChildTurn(t *testing.T) {
	controller := newExecAgentController(NewLocalRunner(t.TempDir()), context.Background(), &Request{}, "thread-root", 3).(*execAgentController)
	task := &execAgentTask{id: "thread-worker", taskName: "worker", path: "/root/worker", status: agent.AgentMessageStatus{Kind: agent.AgentMessageStatusRunning}}
	controller.tasks[task.id] = task
	child := controller.scoped(task.path, task.id)
	child.setActiveTurn("turn-worker")

	if err := controller.SendMessage(context.Background(), &agent.SendMessageArgs{Target: "/root/worker", Message: "queued context", Plaintext: true}); err != nil {
		t.Fatal(err)
	}
	activity, err := child.WaitForActivity(context.Background(), nil)
	if err != nil || activity.TimedOut || !strings.Contains(activity.Message, "message from /root") {
		t.Fatalf("message activity = %#v, %v", activity, err)
	}
	drained := controller.steerMailbox.Drain(&turn.SteerDrainParams{ThreadID: task.id, TurnID: "turn-worker"})
	if len(drained) != 1 || !inputItemsContainText(drained, "Message Type: MESSAGE") || !inputItemsContainText(drained, "queued context") {
		t.Fatalf("message steer input = %#v", drained)
	}

	if err := controller.FollowupTask(context.Background(), &agent.FollowupTaskArgs{Target: "worker", Message: "new direction", Plaintext: true}); err != nil {
		t.Fatal(err)
	}
	activity, err = child.WaitForActivity(context.Background(), nil)
	if err != nil || activity.TimedOut || !strings.Contains(activity.Message, "follow-up from /root") {
		t.Fatalf("followup activity = %#v, %v", activity, err)
	}
	drained = controller.steerMailbox.Drain(&turn.SteerDrainParams{ThreadID: task.id, TurnID: "turn-worker"})
	if len(drained) != 1 || !inputItemsContainText(drained, "Message Type: NEW_TASK") || !inputItemsContainText(drained, "new direction") {
		t.Fatalf("followup steer input = %#v", drained)
	}
}

func TestExecAgentControllerFlushesMessagesQueuedBeforeChildTurnRegistration(t *testing.T) {
	controller := newExecAgentController(NewLocalRunner(t.TempDir()), context.Background(), &Request{}, "thread-root", 3).(*execAgentController)
	task := &execAgentTask{id: "thread-worker", taskName: "worker", path: "/root/worker", status: agent.AgentMessageStatus{Kind: agent.AgentMessageStatusRunning}}
	controller.tasks[task.id] = task

	if err := controller.SendMessage(context.Background(), &agent.SendMessageArgs{Target: "worker", Message: "early message", Plaintext: true}); err != nil {
		t.Fatal(err)
	}
	if err := controller.FollowupTask(context.Background(), &agent.FollowupTaskArgs{Target: "worker", Message: "early followup", Plaintext: true}); err != nil {
		t.Fatal(err)
	}
	if len(task.pendingMessages) != 1 || len(task.pendingFollowup) != 1 {
		t.Fatalf("pending messages=%#v followups=%#v", task.pendingMessages, task.pendingFollowup)
	}
	child := controller.scoped(task.path, task.id)
	child.setActiveTurn("turn-worker")
	activity, err := child.WaitForActivity(context.Background(), nil)
	if err != nil || activity.TimedOut || !strings.Contains(activity.Message, "queued input delivered") {
		t.Fatalf("queued activity = %#v, %v", activity, err)
	}
	drained := controller.steerMailbox.Drain(&turn.SteerDrainParams{ThreadID: task.id, TurnID: "turn-worker"})
	if len(drained) != 2 || !inputItemsContainText(drained, "early message") || !inputItemsContainText(drained, "early followup") {
		t.Fatalf("queued steer input = %#v", drained)
	}
	if len(task.pendingMessages) != 0 || len(task.pendingFollowup) != 0 {
		t.Fatalf("pending queues not drained: %#v/%#v", task.pendingMessages, task.pendingFollowup)
	}
}

func TestExecAgentControllerDoesNotLeakChildEventsToRootHandler(t *testing.T) {
	runner := NewLocalRunner(t.TempDir())
	runner.Agent = &recordingStaticAgent{response: &model.AgentResponse{
		Message: "done",
		Items:   []model.AgentItem{{ID: "final", Type: "agent_message", Text: "done", Data: map[string]any{"phase": "final_answer"}}},
		Usage:   model.AgentUsage{InputTokens: 1, OutputTokens: 1},
	}}
	var childEvents atomic.Int64
	controller := newExecAgentController(runner, context.Background(), &Request{
		Exec:                 cli.ExecOptions{Shared: cli.SharedOptions{CWD: t.TempDir()}},
		InternalEventHandler: func(protocol.ThreadEvent) { childEvents.Add(1) },
	}, "thread-root", 1).(*execAgentController)
	t.Cleanup(controller.shutdown)
	message := "finish"
	if _, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{TaskName: "worker", Message: &message, ForkTurns: execStringPointer("none")}); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.WaitForActivity(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if got := childEvents.Load(); got != 0 {
		t.Fatalf("root handler received %d child events", got)
	}
}

func TestExecAgentControllerDeliversV2CompletionToDirectParentTurn(t *testing.T) {
	controller := newExecAgentController(NewLocalRunner(t.TempDir()), context.Background(), &Request{}, "thread-root", 3).(*execAgentController)
	controller.multiAgentVersion = agent.VersionV2
	parent := controller.scoped("/root/parent", "thread-parent")
	parent.setActiveTurn("turn-parent")
	parent.deliverCompletion(&execAgentTask{path: "/root/parent/worker"}, agent.AgentMessageStatus{Kind: agent.AgentMessageStatusCompleted, Message: "nested result"})

	activity, err := parent.WaitForActivity(context.Background(), nil)
	if err != nil || activity.TimedOut || activity.Message != "Wait completed." {
		t.Fatalf("parent activity = %#v, %v", activity, err)
	}
	drained := controller.steerMailbox.Drain(&turn.SteerDrainParams{ThreadID: "thread-parent", TurnID: "turn-parent"})
	if len(drained) != 1 || !inputItemsContainText(drained, "Message Type: FINAL_ANSWER") || !inputItemsContainText(drained, "Task name: /root/parent") || !inputItemsContainText(drained, "Sender: /root/parent/worker") || !inputItemsContainText(drained, "nested result") {
		t.Fatalf("parent completion input = %#v", drained)
	}
	if inputItemsContainText(drained, "<subagent_notification>") {
		t.Fatalf("legacy subagent notification leaked: %#v", drained)
	}
}

func TestSessionItemsPersistInterAgentCompletionBeforeFinalAnswer(t *testing.T) {
	createdAt := fixedExecTime()
	completion := execAgentCompletionInputItem("/root", "/root/worker", "Message Type: FINAL_ANSWER\nTask name: /root\nSender: /root/worker\nPayload:\nchild result")
	result := &turn.AgentLoopResult{
		InputItems: []any{completion}, InitialInputCount: 0,
		Response: &model.AgentResponse{Items: []model.AgentItem{{
			ID: "final", Type: "agent_message", Text: "root result", Data: map[string]any{"phase": "final_answer"},
		}}},
	}
	items := sessionItemsForTurnWithMode("turn-root", "delegate", nil, result, createdAt, nil, nil, false)
	completionIndex, finalIndex := -1, -1
	for index := range items {
		if items[index].Type == "agent_message" && strings.Contains(string(items[index].Raw), "Message Type: FINAL_ANSWER") {
			completionIndex = index
		}
		if items[index].Type == "agent_message" && items[index].Text == "root result" {
			finalIndex = index
		}
	}
	if completionIndex < 0 || finalIndex < 0 || completionIndex >= finalIndex {
		t.Fatalf("session items = %#v, completion=%d final=%d", items, completionIndex, finalIndex)
	}
}

func TestSessionItemsDoNotRepersistHistoricalInterAgentCompletion(t *testing.T) {
	createdAt := fixedExecTime()
	historical := execAgentCompletionInputItem("/root", "/root/old_worker", "Message Type: FINAL_ANSWER\nTask name: /root\nSender: /root/old_worker\nPayload:\nold result")
	current := execAgentCompletionInputItem("/root", "/root/new_worker", "Message Type: FINAL_ANSWER\nTask name: /root\nSender: /root/new_worker\nPayload:\nnew result")
	result := &turn.AgentLoopResult{
		InputItems: []any{historical, current}, InitialInputCount: 1,
		Response: &model.AgentResponse{Items: []model.AgentItem{{ID: "final", Type: "agent_message", Text: "root result"}}},
	}
	items := sessionItemsForTurnWithMode("turn-root", "continue", nil, result, createdAt, nil, nil, false)
	encoded, _ := json.Marshal(items)
	text := string(encoded)
	if strings.Contains(text, "old_worker") || !strings.Contains(text, "new_worker") {
		t.Fatalf("session items repersisted history or lost current completion: %s", text)
	}
}

func awaitRecordedAgentRequest(t *testing.T, requests <-chan model.AgentRequest) model.AgentRequest {
	t.Helper()
	select {
	case request := <-requests:
		return request
	case <-time.After(5 * time.Second):
		t.Fatal("subagent request was not recorded")
		return model.AgentRequest{}
	}
}

func assertEncryptedAgentCommunication(t *testing.T, request *model.AgentRequest, author, recipient, messageType, encrypted string) {
	t.Helper()
	if request == nil {
		t.Fatal("agent request is nil")
	}
	if request.Prompt != "" {
		t.Fatalf("agent request prompt = %q, want encrypted agent_message input", request.Prompt)
	}
	for _, input := range request.InputItems {
		item, ok := input.(map[string]any)
		if !ok || item["type"] != "agent_message" || item["author"] != author || item["recipient"] != recipient {
			continue
		}
		content, _ := item["content"].([]any)
		if len(content) != 2 {
			continue
		}
		envelope, _ := content[0].(map[string]any)
		payload, _ := content[1].(map[string]any)
		if strings.Contains(fmt.Sprint(envelope["text"]), "Message Type: "+messageType+"\n") &&
			payload["type"] == "encrypted_content" && payload["encrypted_content"] == encrypted {
			return
		}
	}
	t.Fatalf("encrypted communication %s/%q missing from %#v", messageType, encrypted, request.InputItems)
}

func TestExecAgentCommunicationInputItemSupportsStructuredPlaintext(t *testing.T) {
	item := execAgentCommunicationInputItem(execAgentCommunication{
		author: "/root", recipient: "/root/worker", message: "inspect the rollout", trigger: true, plaintext: true,
	})
	content, ok := item["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("plaintext content = %#v", item["content"])
	}
	block, ok := content[0].(map[string]any)
	want := "Message Type: NEW_TASK\nTask name: /root/worker\nSender: /root\nPayload:\ninspect the rollout"
	if !ok || block["type"] != "input_text" || block["text"] != want {
		t.Fatalf("plaintext block = %#v, want %q", block, want)
	}
	encoded, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "encrypted_content") {
		t.Fatalf("plaintext communication contains encrypted block: %s", encoded)
	}
}

func TestExecCompactionPreservesStructuredAgentRawAndForkStripsIt(t *testing.T) {
	raw := execAgentCommunicationInputItem(execAgentCommunication{
		author: "/root", recipient: "/root/worker", message: "encrypted delegated task", trigger: true,
	})
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	sessionItem := session.Item{ID: "agent-task", Type: "agent_message", Raw: encoded, CreatedAt: fixedExecTime()}
	compacted := execCompactItemsFromSession([]session.Item{sessionItem})
	if len(compacted) != 1 || string(compacted[0].Raw) != string(encoded) {
		t.Fatalf("compact items = %#v", compacted)
	}
	restored := execSessionItemsFromCompact(compacted, fixedExecTime())
	if len(restored) != 1 || string(restored[0].Raw) != string(encoded) {
		t.Fatalf("restored items = %#v", restored)
	}
	inputs := session.InputItemsFromItems(restored, &session.HistoryBuildOptions{IncludeToolOutputs: true})
	if len(inputs) != 1 {
		t.Fatalf("restored model inputs = %#v", inputs)
	}
	filtered := stripParentAgentMessages(append(inputs, map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "keep"}}}))
	if len(filtered) != 1 || filtered[0].(map[string]any)["type"] != "message" {
		t.Fatalf("fork-filtered inputs = %#v", filtered)
	}
}

func assertNoEmptyInputTextBlocks(t *testing.T, request *model.AgentRequest) {
	t.Helper()
	for _, input := range request.InputItems {
		item, ok := input.(map[string]any)
		if !ok {
			continue
		}
		content := []any{}
		switch typed := item["content"].(type) {
		case []any:
			content = typed
		case []map[string]any:
			for _, block := range typed {
				content = append(content, block)
			}
		}
		for _, raw := range content {
			block, ok := raw.(map[string]any)
			text, exists := block["text"].(string)
			if ok && block["type"] == "input_text" && (!exists || strings.TrimSpace(text) == "") {
				t.Fatalf("request contains empty input_text block: %#v", item)
			}
		}
	}
}

func TestExecAgentControllerInterruptGenerationAndConcurrencyLimit(t *testing.T) {
	runner := NewLocalRunner(t.TempDir())
	blocking := &firstTurnBlockingAgent{started: make(chan struct{})}
	runner.Agent = blocking
	controller := newExecAgentController(runner, context.Background(), &Request{Exec: cli.ExecOptions{Prompt: "root", Shared: cli.SharedOptions{CWD: t.TempDir()}}}, "thread-root", 1).(*execAgentController)
	t.Cleanup(controller.shutdown)
	message := "block"
	spawned, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{TaskName: "worker", Message: &message, ForkTurns: execStringPointer("none")})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-blocking.started:
	case <-time.After(5 * time.Second):
		t.Fatal("first child turn did not start")
	}
	secondMessage := "second"
	if _, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{TaskName: "second", Message: &secondMessage, ForkTurns: execStringPointer("none")}); !errors.Is(err, agent.ErrAgentLimitReached) {
		t.Fatalf("second spawn error = %v", err)
	}
	interrupted, err := controller.InterruptAgent(context.Background(), &agent.InterruptAgentArgs{Target: spawned.TaskName})
	if err != nil || interrupted.PreviousStatus != string(agent.AgentMessageStatusRunning) {
		t.Fatalf("InterruptAgent() = %#v, %v", interrupted, err)
	}
	if _, err := controller.WaitForActivity(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := controller.FollowupTask(context.Background(), &agent.FollowupTaskArgs{Target: "worker", Message: "finish"}); err != nil {
		t.Fatal(err)
	}
	activity, err := controller.WaitForActivity(context.Background(), nil)
	if err != nil || activity.TimedOut || !strings.Contains(activity.Message, "completed") {
		t.Fatalf("followup activity = %#v, %v", activity, err)
	}
	list, err := controller.ListAgents(context.Background(), &agent.ListAgentsArgs{})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range list.Agents {
		if item.AgentName == spawned.TaskName {
			encoded, _ := json.Marshal(item.AgentStatus)
			if !strings.Contains(string(encoded), "completed") {
				t.Fatalf("old interrupted generation overwrote status: %s", encoded)
			}
			return
		}
	}
	t.Fatalf("spawned agent missing from %#v", list)
}

func TestExecAgentControllerV2InterruptMissingTargetReturnsError(t *testing.T) {
	runner := NewLocalRunner(t.TempDir())
	controller := newExecAgentController(runner, context.Background(), &Request{Exec: cli.ExecOptions{Prompt: "root", Shared: cli.SharedOptions{CWD: t.TempDir()}}}, "thread-root", 1).(*execAgentController)
	t.Cleanup(controller.shutdown)
	result, err := controller.InterruptAgent(context.Background(), &agent.InterruptAgentArgs{Target: "/root/missing_worker"})
	if err == nil || !strings.Contains(err.Error(), "agent /root/missing_worker not found") {
		t.Fatalf("InterruptAgent() result=%#v error=%v", result, err)
	}
}

func countExecRolloutsForThread(t *testing.T, home, threadID string) int {
	t.Helper()
	count := 0
	err := filepath.WalkDir(home, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || filepath.Ext(path) != ".jsonl" {
			return err
		}
		if strings.Contains(filepath.Base(path), threadID) {
			count++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return count
}

func TestExecAgentControllerRunsPersistedSubagent(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	runner := NewLocalRunner(home)
	runner.Agent = &staticResponseAgent{
		response: &model.AgentResponse{
			Message: "child calculation complete",
			Items:   []model.AgentItem{{ID: "child-final", Type: "agent_message", Text: "child calculation complete", Data: map[string]any{"phase": "final_answer"}}},
			Usage:   model.AgentUsage{InputTokens: 1, OutputTokens: 1},
			Model:   "gpt-test", ProviderID: "local",
		},
	}
	controller := newExecAgentController(runner, context.Background(), &Request{
		Exec: cli.ExecOptions{Prompt: "root", Shared: cli.SharedOptions{CWD: cwd}},
	}, "thread-root", 4)
	t.Cleanup(controller.(*execAgentController).shutdown)
	message := "compute one segment"
	spawned, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{Message: &message})
	if err != nil {
		t.Fatalf("SpawnAgent() error = %v", err)
	}
	timeoutMS := int64(5000)
	waited, err := controller.WaitAgent(context.Background(), &agent.WaitAgentArgs{Targets: []string{spawned.AgentID}, TimeoutMS: &timeoutMS})
	if err != nil {
		t.Fatalf("WaitAgent() error = %v", err)
	}
	status := waited.Status[spawned.AgentID]
	if waited.TimedOut || status.Kind != agent.AgentMessageStatusCompleted || status.Message != "child calculation complete" {
		t.Fatalf("WaitAgent() = %#v", waited)
	}
	record := loadSessionRecord(t, home, spawned.AgentID)
	if record.ParentThreadID != "thread-root" || record.SessionID != "thread-root" || record.Metadata.ThreadSource != "subagent" ||
		record.Metadata.Source != "subagent:thread_spawn" || record.Metadata.MultiAgentVersion != string(agent.VersionV2) {
		t.Fatalf("subagent lineage = %#v", record)
	}
	rolloutPath, err := rollout.FindThreadPath(home, spawned.AgentID, false)
	if err != nil {
		t.Fatalf("FindThreadPath() error = %v", err)
	}
	data, err := os.ReadFile(rolloutPath)
	if err != nil {
		t.Fatal(err)
	}
	firstLine := strings.SplitN(string(data), "\n", 2)[0]
	if !strings.Contains(firstLine, `"thread_source":"subagent"`) || !strings.Contains(firstLine, `"parent_thread_id":"thread-root"`) {
		t.Fatalf("rollout session metadata = %s", firstLine)
	}
}

func TestToolRouterRegistersViewImageForImageCapableModel(t *testing.T) {
	cwd := t.TempDir()
	info := &model.ModelInfo{
		InputModalities:             []string{"text", "image"},
		SupportsImageDetailOriginal: true,
	}
	viewImage := execViewImageOptions(cwd, info)
	if viewImage == nil || viewImage.CWD != cwd || !viewImage.CanRequestOriginalDetail {
		t.Fatalf("execViewImageOptions() = %#v", viewImage)
	}
	if got := execViewImageOptions(cwd, &model.ModelInfo{InputModalities: []string{"text"}}); got != nil {
		t.Fatalf("text-only execViewImageOptions() = %#v, want nil", got)
	}

	runner := NewLocalRunner(cwd)
	router, err := runner.toolRouterForRequest(
		&Request{Exec: cli.ExecOptions{Shared: cli.SharedOptions{CWD: cwd}, Prompt: "inspect image"}},
		&agentRunConfig{ViewImage: viewImage},
	)
	if err != nil {
		t.Fatalf("toolRouterForRequest returned error: %v", err)
	}
	found := false
	for _, spec := range router.ModelVisibleSpecs() {
		if spec.Name.Key() == tool.ViewImageToolName {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("view_image is missing from the exec model-visible tool surface")
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
	router, err := runner.toolRouterForRequest(req, &agentRunConfig{
		ApprovalPolicy:          policy,
		ExecPermissionApprovals: true,
	})
	if err != nil {
		t.Fatalf("toolRouterForRequest returned error: %v", err)
	}
	output, err := router.Dispatch(context.Background(), &tool.Invocation{
		CallID:   "call-additional-permissions",
		ToolName: tool.PlainName(tool.DefaultShellCommandToolName),
		Payload: tool.Payload{
			Kind: tool.PayloadFunction,
			Arguments: `{
				"command":"mkdir ../test",
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
	if err := os.MkdirAll(filepath.Join(project, ".gcode"), 0o755); err != nil {
		t.Fatalf("MkdirAll project .gcode returned error: %v", err)
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
	if err := os.MkdirAll(filepath.Join(project, ".gcode"), 0o755); err != nil {
		t.Fatalf("MkdirAll project .gcode returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, ".gcode", "instructions.md"), []byte("\nproject instructions\n"), 0o600); err != nil {
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
	if agent.request == nil || !strings.HasPrefix(agent.request.Instructions, "project instructions") {
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
		!agentRequestToolsContainPlainFunction(&agent.requests[0], "view_image") ||
		!agentRequestToolsContainPlainFunction(&agent.requests[1], "view_image") {
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
	if !strings.HasPrefix(output.ID, "fco_") {
		t.Fatalf("tool output response item id = %q", output.ID)
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
	if call := execSessionItemByID(items, "call-1"); call == nil || call.Raw == nil || call.ResponseID != "resp-first" {
		t.Fatalf("original tool call response item was not preserved, items = %#v", items)
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
	if err := emitFinalEventsFromAgentResult(sink, result, false); err != nil {
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

func TestExecSessionItemsSplitProposedPlanInPlanMode(t *testing.T) {
	createdAt := time.Unix(1700000000, 0).UTC()
	result := &turn.AgentLoopResult{
		Responses: []*model.AgentResponse{{
			ResponseID: "resp-plan",
			Items: []model.AgentItem{{
				ID:   "msg-plan",
				Type: "agent_message",
				Text: "Intro\n<proposed_plan>\n- Step 1\n</proposed_plan>\nOutro",
			}},
		}},
	}
	result.Response = result.Responses[0]

	items := sessionItemsForTurnWithMode("turn-plan", "make a plan", nil, result, createdAt, nil, nil, true)
	agent := execSessionItemByID(items, "msg-plan")
	if agent == nil || agent.Text != "Intro\nOutro" || len(agent.Content) != 1 || agent.Content[0].Text != "Intro\nOutro" {
		t.Fatalf("visible agent item = %#v", agent)
	}
	planIndex := execSessionItemIndexByType(items, "plan")
	if planIndex < 0 || items[planIndex].Text != "- Step 1\n" || items[planIndex].ResponseID != "resp-plan" {
		t.Fatalf("plan items = %#v", items)
	}
	for _, item := range items {
		if strings.Contains(item.Text, "proposed_plan") {
			t.Fatalf("persisted item leaked plan tags: %#v", item)
		}
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

	if err := emitFinalEventsFromAgentResult(sink, result, false); err != nil {
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

	if err := emitFinalEventsFromAgentResult(sink, result, false); err != nil {
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
						"diff": "+hi",
					}, {
						"path": "b/deleted.txt",
						"kind": map[string]any{"type": "delete"},
					}, {
						"path": "c/modified.txt",
						"kind": map[string]any{"type": "update", "move_path": "c/renamed.txt"},
						"diff": "@@ -1 +1 @@\n-old\n+new",
					}},
				},
			},
		}},
		Usage: model.AgentUsage{InputTokens: 1, OutputTokens: 2},
	}
	sink := newExecEventSink(nil, false)

	if err := emitFinalEventsFromAgentResult(sink, result, false); err != nil {
		t.Fatalf("emitFinalEventsFromAgentResult() error = %v", err)
	}
	events := sink.Events()
	fileChangeIndex := execEventItemIndex(events, "file-change-call-apply")
	if fileChangeIndex < 0 {
		t.Fatalf("file_change event missing: %#v", events)
	}
	if events[fileChangeIndex].Type != "item.started" || events[fileChangeIndex].Item.Status != "in_progress" {
		t.Fatalf("file_change start event = %#v", events[fileChangeIndex])
	}
	fileChangeIndex++
	if fileChangeIndex >= len(events) || events[fileChangeIndex].Type != "item.completed" {
		t.Fatalf("file_change completion event missing after start: %#v", events)
	}
	item := events[fileChangeIndex].Item
	if item.Type != "file_change" || item.Status != "completed" || len(item.Changes) != 3 {
		t.Fatalf("file_change item = %#v", item)
	}
	if item.Changes[0].Kind != "add" || item.Changes[1].Kind != "delete" || item.Changes[2].Kind != "update" {
		t.Fatalf("file_change changes = %#v", item.Changes)
	}
	if item.Changes[0].Diff != "+hi" || item.Changes[2].MovePath != "c/renamed.txt" {
		t.Fatalf("file_change internal details = %#v", item.Changes)
	}
	data, err := json.Marshal(events[fileChangeIndex])
	if err != nil {
		t.Fatalf("json.Marshal(file_change) error = %v", err)
	}
	if bytes.Contains(data, []byte(`"diff"`)) || bytes.Contains(data, []byte(`"move_path"`)) {
		t.Fatalf("SDK file_change leaked non-Rust fields: %s", data)
	}
	if execEventItemIndex(events, "tool-call-call-apply") >= 0 || execEventItemIndex(events, "tool-output-call-apply") >= 0 {
		t.Fatalf("file change should not emit generic apply_patch tool events: %#v", events)
	}
}

func TestExecCommentaryCompletesBeforeToolLifecycleLikeRust(t *testing.T) {
	sink := newExecEventSink(nil, false)
	collector := &execStreamEventCollector{sink: sink}
	response := &model.AgentResponse{Items: []model.AgentItem{
		{ID: "commentary-1", Type: "agent_message", Text: "I will run the command."},
		{ID: "call-1", Type: "function_call", Name: tool.DefaultExecCommandToolName, CallID: "call-1"},
	}}
	collector.AssistantMessage(response, 0, true)
	collector.ToolStarted(context.Background(), &tool.Invocation{
		CallID: "call-1", ToolName: tool.PlainName(tool.DefaultExecCommandToolName),
		Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"cmd":"printf ok"}`},
	}, time.Now())
	events := sink.Events()
	if len(events) != 2 || events[0].Type != "item.completed" || events[0].Item == nil || events[0].Item.ID != "commentary-1" ||
		events[1].Type != "item.started" || events[1].Item == nil || events[1].Item.ID != "call-1" {
		t.Fatalf("event order = %#v, want commentary completed before command started", events)
	}
}

func TestExecV2SubAgentActivityEmitsInternalStartedAndCompletedLifecycle(t *testing.T) {
	internal := []protocol.ThreadEvent{}
	sink := newExecEventSink(nil, false)
	sink.internalHandler = func(event protocol.ThreadEvent) {
		internal = append(internal, event)
	}
	collector := &execStreamEventCollector{sink: sink}
	collector.ToolCompleted(context.Background(), &turn.ToolExecutionResult{
		Invocation: &tool.Invocation{
			CallID: "spawn-activity", ToolName: tool.NamespacedName(agent.MultiAgentV2Namespace, "spawn_agent"),
		},
		Output: &tool.Output{Success: true, Data: map[string]any{
			"subAgentActivity": map[string]any{"kind": "started", "agent_thread_id": "child-thread", "agent_path": "/root/worker"},
		}},
	})
	if len(internal) != 2 || internal[0].Type != "item.started" || internal[1].Type != "item.completed" {
		t.Fatalf("internal activity lifecycle = %#v", internal)
	}
	for index, event := range internal {
		if event.Item == nil || event.Item.ID != "spawn-activity" || event.Item.Type != "sub_agent_activity" ||
			event.Item.ActivityKind != "started" || event.Item.AgentThreadID != "child-thread" || event.Item.AgentPath != "/root/worker" {
			t.Fatalf("internal activity event %d = %#v", index, event)
		}
	}
	if events := sink.Events(); len(events) != 0 {
		t.Fatalf("v2 activity leaked into public SDK events: %#v", events)
	}
}

func TestExecToolCompletesBeforeNextCommentaryLikeRust(t *testing.T) {
	sink := newExecEventSink(nil, false)
	collector := &execStreamEventCollector{sink: sink}
	invocation := &tool.Invocation{
		CallID: "call-1", ToolName: tool.PlainName(tool.DefaultExecCommandToolName),
		Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"cmd":"printf ok"}`},
	}
	collector.ToolStarted(context.Background(), invocation, time.Now())
	collector.ToolCompleted(context.Background(), &turn.ToolExecutionResult{
		Invocation: invocation,
		Output:     &tool.Output{CallID: "call-1", ToolName: tool.PlainName(tool.DefaultExecCommandToolName), Success: true, Body: "ok", Data: map[string]any{"exit_code": 0}},
	})
	collector.AssistantMessage(&model.AgentResponse{Items: []model.AgentItem{{
		ID: "commentary-2", Type: "agent_message", Text: "I will run the next command.",
	}}}, 1, true)
	events := sink.Events()
	if len(events) != 3 || events[0].Type != "item.started" || events[1].Type != "item.completed" ||
		events[1].Item == nil || events[1].Item.ID != "call-1" || events[2].Type != "item.completed" ||
		events[2].Item == nil || events[2].Item.ID != "commentary-2" {
		t.Fatalf("event order = %#v, want command lifecycle closed before next commentary", events)
	}
}

func TestExecLegacyShellCommandEmitsOneCommandLifecycleLikeRust(t *testing.T) {
	sink := newExecEventSink(nil, false)
	collector := &execStreamEventCollector{sink: sink}
	invocation := &tool.Invocation{
		CallID:   "legacy-weather",
		ToolName: tool.PlainName(tool.DefaultShellCommandToolName),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"command":"curl.exe -sS https://sdk-weather.invalid/Yunnan?format=j1","workdir":"C:\\workspace"}`},
	}
	collector.ToolStarted(context.Background(), invocation, time.Now())
	collector.ToolCompleted(context.Background(), &turn.ToolExecutionResult{
		Invocation: invocation,
		Output: &tool.Output{
			CallID: "legacy-weather", ToolName: tool.PlainName(tool.DefaultShellCommandToolName), Success: true,
			Body: "curl: (6) Could not resolve host", Data: map[string]any{"exit_code": 1},
		},
	})
	events := sink.Events()
	if len(events) != 2 || events[0].Type != "item.started" || events[1].Type != "item.completed" ||
		events[0].Item == nil || events[1].Item == nil || events[0].Item.ID != "legacy-weather" ||
		events[1].Item.ID != "legacy-weather" || events[0].Item.Command != "curl.exe -sS https://sdk-weather.invalid/Yunnan?format=j1" ||
		events[1].Item.Status != "failed" || events[1].Item.ExitCode == nil || *events[1].Item.ExitCode != 1 {
		t.Fatalf("legacy command lifecycle = %#v", events)
	}
}

func TestExecRejectedCommandStillClosesCommandLifecycle(t *testing.T) {
	sink := newExecEventSink(nil, false)
	collector := &execStreamEventCollector{sink: sink}
	invocation := &tool.Invocation{
		CallID: "call-rejected", ToolName: tool.PlainName(tool.DefaultExecCommandToolName),
		Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"cmd":"curl https://example.com","sandbox_permissions":"with_additional_permissions"}`},
	}
	collector.ToolStarted(context.Background(), invocation, time.Now())
	collector.ToolCompleted(context.Background(), &turn.ToolExecutionResult{
		Invocation: invocation,
		Output: &tool.Output{
			CallID: "call-rejected", ToolName: tool.PlainName(tool.DefaultExecCommandToolName),
			Success: false, Body: "approval policy is never; reject command",
		},
	})
	events := sink.Events()
	if len(events) != 2 || events[0].Type != "item.started" || events[1].Type != "item.completed" ||
		events[0].Item == nil || events[1].Item == nil || events[0].Item.ID != "call-rejected" ||
		events[1].Item.ID != "call-rejected" || events[1].Item.Type != "command_execution" ||
		events[1].Item.Status != "failed" {
		t.Fatalf("rejected command lifecycle = %#v, want one command started then failed", events)
	}
}

func TestExecCompletedApplyPatchEmitsFileChangeLifecycleInOrder(t *testing.T) {
	sink := newExecEventSink(nil, false)
	collector := &execStreamEventCollector{sink: sink}
	collector.ToolCompleted(context.Background(), &turn.ToolExecutionResult{
		Invocation: &tool.Invocation{
			CallID: "patch-1", ToolName: tool.PlainName(tool.DefaultApplyPatchToolName),
			Payload: tool.Payload{Kind: tool.PayloadCustom, Input: "*** Begin Patch\n*** Add File: quicksort.java\n+class quicksort {}\n*** End Patch"},
		},
		Output: &tool.Output{
			CallID: "patch-1", ToolName: tool.PlainName(tool.DefaultApplyPatchToolName), Success: true,
			Data: map[string]any{
				"fileChange": true, "status": "completed",
				"changes": []map[string]any{{"path": "quicksort.java", "kind": map[string]any{"type": "add"}}},
			},
		},
	})
	events := sink.Events()
	if len(events) != 2 || events[0].Type != "item.started" || events[1].Type != "item.completed" ||
		events[0].Item == nil || events[1].Item == nil || events[0].Item.Type != "file_change" ||
		events[0].Item.ID != events[1].Item.ID {
		t.Fatalf("file change lifecycle = %#v, want started then completed for one item", events)
	}
}

func TestEmitFinalEventsSuppressesApplyPatchValidationFailureLikeRust(t *testing.T) {
	result := &turn.AgentLoopResult{
		Response: &model.AgentResponse{},
		ToolExecutions: []turn.ToolExecutionResult{{
			Invocation: &tool.Invocation{
				CallID: "patch-validation", ToolName: tool.PlainName(tool.DefaultApplyPatchToolName),
				Payload: tool.Payload{Kind: tool.PayloadCustom, Input: "*** Begin Patch\n*** Update File: state.txt\n@@\n-MISSING\n+VALUE\n*** End Patch"},
			},
			Output: &tool.Output{
				CallID: "patch-validation", ToolName: tool.PlainName(tool.DefaultApplyPatchToolName),
				Success: false, Body: "apply_patch verification failed: missing context",
				Error: "apply_patch verification failed: missing context",
			},
		}},
	}
	sink := newExecEventSink(nil, false)

	if err := emitFinalEventsFromAgentResult(sink, result, false); err != nil {
		t.Fatalf("emitFinalEventsFromAgentResult() error = %v", err)
	}
	for _, event := range sink.Events() {
		if event.Item != nil && (event.Item.Type == "file_change" || event.Item.Type == "tool_call" || event.Item.Type == "tool_output") {
			t.Fatalf("validation failure leaked public tool event: %#v", event)
		}
	}
}

func TestExecEventSinkKeepsInternalFileChangeDiffWhileJSONStaysRustCompatible(t *testing.T) {
	var stdout bytes.Buffer
	var internal protocol.ThreadEvent
	sink := newExecEventSink(&stdout, true)
	sink.internalHandler = func(event protocol.ThreadEvent) { internal = event }
	event := protocol.ItemCompleted(protocol.FileChangeItem("patch-1", []protocol.FileChange{{
		Path: "a.go", Kind: "update", Diff: "@@ -1 +1 @@\n-old\n+new", MovePath: "b.go",
	}}, "completed"))
	if err := sink.Emit(event); err != nil {
		t.Fatalf("Emit() error = %v", err)
	}
	if internal.Item == nil || len(internal.Item.Changes) != 1 || internal.Item.Changes[0].Diff == "" || internal.Item.Changes[0].MovePath != "b.go" {
		t.Fatalf("internal event lost file-change details: %#v", internal)
	}
	encoded := stdout.String()
	if strings.Contains(encoded, "@@ -1 +1 @@") || strings.Contains(encoded, "b.go") || strings.Contains(encoded, "\"diff\"") {
		t.Fatalf("public JSON leaked internal file-change details: %s", encoded)
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

	if err := emitFinalEventsFromAgentResult(sink, result, false); err != nil {
		t.Fatalf("emitFinalEventsFromAgentResult() error = %v", err)
	}
	events := sink.Events()
	fileChangeIndex := execEventItemIndex(events, "file-change-patch-2")
	if fileChangeIndex < 0 {
		t.Fatalf("file_change event missing: %#v", events)
	}
	if events[fileChangeIndex].Type != "item.started" || events[fileChangeIndex].Item.Status != "in_progress" {
		t.Fatalf("file_change start event = %#v", events[fileChangeIndex])
	}
	fileChangeIndex++
	if fileChangeIndex >= len(events) || events[fileChangeIndex].Type != "item.completed" {
		t.Fatalf("file_change completion event missing after start: %#v", events)
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

	if err := emitFinalEventsFromAgentResult(sink, result, false); err != nil {
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

	if err := emitFinalEventsFromAgentResult(sink, result, false); err != nil {
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
			Namespace: agent.MultiAgentV1Namespace,
			Name:      "spawn_agent",
			CallID:    "collab-1",
			Arguments: `{"message":"draft a plan"}`,
		}}},
		ToolExecutions: []turn.ToolExecutionResult{{
			Invocation: &tool.Invocation{
				CallID:   "collab-1",
				ToolName: tool.NamespacedName(agent.MultiAgentV1Namespace, "spawn_agent"),
				Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"message":"draft a plan"}`},
				Context:  map[string]any{"thread_id": "thread-parent"},
			},
			Output: &tool.Output{
				CallID:   "collab-1",
				ToolName: tool.NamespacedName(agent.MultiAgentV1Namespace, "spawn_agent"),
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

	if err := emitFinalEventsFromAgentResult(sink, result, false); err != nil {
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
				ToolName: tool.NamespacedName(agent.MultiAgentV1Namespace, "wait_agent"),
				Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"targets":["thread-child"]}`},
				Context:  map[string]any{"thread_id": "thread-parent"},
			},
			Output: &tool.Output{
				CallID:   "collab-wait",
				ToolName: tool.NamespacedName(agent.MultiAgentV1Namespace, "wait_agent"),
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

	if err := emitFinalEventsFromAgentResult(sink, result, false); err != nil {
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

func TestV2OnlyWaitMapsToSDKCollabItem(t *testing.T) {
	waitExecution := &turn.ToolExecutionResult{Invocation: &tool.Invocation{
		CallID: "wait-v2", ToolName: tool.NamespacedName(agent.MultiAgentV2Namespace, "wait_agent"),
		Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: `{}`},
	}, Output: &tool.Output{Success: true, Body: `{"message":"done","timed_out":false}`}}
	if !isCollabExecution(waitExecution) {
		t.Fatal("v2 wait_agent must map to a collab SDK item")
	}
	for _, name := range []string{"spawn_agent", "send_message", "followup_task", "interrupt_agent", "list_agents"} {
		execution := &turn.ToolExecutionResult{Invocation: &tool.Invocation{ToolName: tool.NamespacedName(agent.MultiAgentV2Namespace, name)}}
		if isCollabExecution(execution) {
			t.Fatalf("v2 %s must not map to legacy collab SDK item", name)
		}
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

	if err := emitFinalEventsFromAgentResult(sink, result, false); err != nil {
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

func TestCommandExecutionNonZeroExitIsFailedEvenWhenToolOutputSucceeded(t *testing.T) {
	event, ok := eventFromToolOutputExecution(&turn.ToolExecutionResult{
		Invocation: &tool.Invocation{
			CallID:   "call-failed-command",
			ToolName: tool.PlainName(tool.DefaultExecCommandToolName),
			Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"cmd":"exit 7"}`},
		},
		Output: &tool.Output{
			Success: true,
			Body:    "SDK_EXIT_7\n",
			Data: map[string]any{
				"exit_code": 1,
			},
		},
	})
	if !ok || event.Item == nil {
		t.Fatalf("event = %#v ok=%v", event, ok)
	}
	if event.Item.Type != "command_execution" || event.Item.Status != "failed" || event.Item.ExitCode == nil || *event.Item.ExitCode != 1 {
		t.Fatalf("command item = %#v", event.Item)
	}
}

func TestCommandExecutionUsesRawStreamsInsteadOfUnifiedExecMetadata(t *testing.T) {
	output := &tool.Output{
		Body: "Chunk ID: abc123\nWall time: 1 second\nOutput:\n",
		Data: map[string]any{
			"stdout":        "real stdout\n",
			"stderr":        "real stderr\n",
			"hook_response": "Chunk ID: abc123\nWall time: 1 second\nOutput:\n",
		},
	}
	if got := commandExecutionAggregatedOutput(output); got != "real stdout\nreal stderr\n" {
		t.Fatalf("commandExecutionAggregatedOutput() = %q", got)
	}
}

func TestRunContextRejectsMissingWorkingDirectoryBeforeStartingThread(t *testing.T) {
	runner := NewRunner(t.TempDir())
	missing := filepath.Join(t.TempDir(), "missing")
	var stdout bytes.Buffer
	_, err := runner.RunContext(context.Background(), &Request{
		Root: cli.RootOptions{},
		Exec: cli.ExecOptions{
			Shared: cli.SharedOptions{CWD: missing},
			Prompt: "must not run",
			JSON:   true,
		},
	}, strings.NewReader(""), &stdout, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "working directory") {
		t.Fatalf("RunContext() error = %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want no lifecycle events", stdout.String())
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

func TestCodeModeDoesNotExposeNestedCommandsAsTopLevelEvents(t *testing.T) {
	execution := &turn.ToolExecutionResult{
		Invocation: &tool.Invocation{
			CallID:   "code-call",
			ToolName: tool.PlainName(tool.CodeModeExecToolName),
			Context: map[string]any{
				"code_mode_nested_tool_started":   tool.CodeModeNestedToolStartedFunc(func(context.Context, *tool.Invocation, time.Time) {}),
				"code_mode_nested_tool_completed": tool.CodeModeNestedToolCompletedFunc(func(context.Context, *tool.Invocation, *tool.Output, error, time.Time, time.Time) {}),
			},
		},
		Output: &tool.Output{Success: true, Data: map[string]any{
			"nested_commands":   []string{"curl weather"},
			"nested_outputs":    []string{"sunny"},
			"nested_exit_codes": []int{0},
		}},
	}
	if events := eventsFromToolExecution(execution); len(events) != 0 {
		t.Fatalf("nested command events = %#v, want none", events)
	}
}

func TestViewImageExecutionDoesNotExposeImageDataAsGenericSDKToolOutput(t *testing.T) {
	execution := &turn.ToolExecutionResult{
		Invocation: &tool.Invocation{
			CallID:   "view-image-call",
			ToolName: tool.PlainName(tool.ViewImageToolName),
			Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"path":"screenshot.png"}`},
		},
		Output: &tool.Output{
			Success: true,
			Body:    `{"image_url":"data:image/png;base64,large"}`,
		},
	}
	if events := eventsFromToolExecution(execution); len(events) != 0 {
		t.Fatalf("view_image SDK events = %#v, want none", events)
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

	if err := emitFinalEventsFromAgentResult(sink, result, false); err != nil {
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
	if err := emitFinalEventsFromAgentResult(sink, result, false); err != nil {
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
	if err := emitFinalEventsFromAgentResult(sink, result, false); err != nil {
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
	if err := emitFinalEventsFromAgentResult(sink, result, false); err != nil {
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

func TestExecStreamEventCollectorClearsRetryBeforeRecoveredDelta(t *testing.T) {
	collector := &execStreamEventCollector{streamAssistantDeltas: true}
	collector.Handle(&model.ResponsesStreamEvent{Kind: model.ResponsesStreamEventRetrying, RetryAttempt: 2, RetryMax: 5})
	collector.Handle(&model.ResponsesStreamEvent{Kind: model.ResponsesStreamEventOutputText, ItemID: "msg-1", Delta: "recovered"})

	events := collector.Events()
	if len(events) != 3 || events[0].Type != "turn.reconnecting" || events[1].Type != "turn.reconnected" || events[2].Type != "item.delta" {
		t.Fatalf("recovery event order = %#v", execEventTypes(events))
	}
}

func TestExecStreamEventCollectorDefersApplyPatchBeginUntilValidation(t *testing.T) {
	collector := &execStreamEventCollector{streamAssistantDeltas: true, workingDirectory: t.TempDir()}
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
	if events := collector.Events(); len(events) != 0 {
		t.Fatalf("pre-validation apply_patch events = %#v, want none", events)
	}
}

func TestExecStreamEventCollectorPreservesCommentaryBeforeToolStart(t *testing.T) {
	collector := &execStreamEventCollector{streamAssistantDeltas: true}
	collector.Handle(&model.ResponsesStreamEvent{
		Kind:   model.ResponsesStreamEventOutputAdded,
		ItemID: "msg-commentary",
		Item: &model.AgentItem{ID: "msg-commentary", Type: "agent_message", Data: map[string]any{
			"phase": "commentary",
		}},
	})
	collector.Handle(&model.ResponsesStreamEvent{
		Kind:   model.ResponsesStreamEventOutputText,
		ItemID: "msg-commentary",
		Delta:  "我查询一下天气。",
	})
	collector.Handle(&model.ResponsesStreamEvent{
		Kind:   model.ResponsesStreamEventOutputDone,
		ItemID: "msg-commentary",
		Item: &model.AgentItem{ID: "msg-commentary", Type: "agent_message", Text: "我查询一下天气。", Data: map[string]any{
			"phase": "commentary",
		}},
	})
	collector.ToolStarted(context.Background(), &tool.Invocation{
		CallID:   "call-weather",
		ToolName: tool.PlainName(tool.DefaultExecCommandToolName),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"cmd":"curl weather"}`},
	}, time.Now())
	events := collector.Events()
	if len(events) != 3 || events[0].Type != "item.delta" || events[1].Type != "item.completed" || events[2].Type != "item.started" {
		t.Fatalf("event order = %#v", events)
	}
	if events[0].Delta == nil || events[0].Delta.Text != "我查询一下天气。" {
		t.Fatalf("commentary event = %#v", events[0])
	}
	if events[1].Item == nil || events[1].Item.Phase != "commentary" {
		t.Fatalf("commentary completion = %#v", events[1])
	}
	if events[2].Item == nil || events[2].Item.Type != "command_execution" {
		t.Fatalf("tool event = %#v", events[2])
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

func TestExecStreamEventCollectorEmitsCanonicalStandaloneWebSearchLifecycle(t *testing.T) {
	collector := &execStreamEventCollector{}
	invocation := &tool.Invocation{
		CallID:   "call-web-search",
		ToolName: tool.NamespacedName(turn.WebSearchNamespace, turn.WebSearchRunTool),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"search_query":[{"q":"rust"}]}`},
	}
	collector.Handle(&model.ResponsesStreamEvent{
		Kind: model.ResponsesStreamEventOutputAdded,
		Item: &model.AgentItem{
			ID:        "fc-web-search",
			Type:      "function_call",
			Name:      turn.WebSearchRunTool,
			Namespace: turn.WebSearchNamespace,
			CallID:    invocation.CallID,
			Arguments: invocation.Payload.Arguments,
		},
	})
	if events := collector.Events(); len(events) != 0 {
		t.Fatalf("model function call should not emit a generic tool item: %#v", events)
	}

	collector.ToolStarted(context.Background(), invocation, time.Now())
	collector.ToolCompleted(context.Background(), &turn.ToolExecutionResult{
		Invocation: invocation,
		Output: &tool.Output{
			CallID:   invocation.CallID,
			ToolName: invocation.ToolName,
			Success:  true,
			Data: map[string]any{
				"web_search": true,
				"web_search_action": map[string]any{
					"type":  "search",
					"query": "rust",
				},
			},
		},
	})
	events := collector.Events()
	if len(events) != 2 {
		t.Fatalf("web search lifecycle = %#v", events)
	}
	if events[0].Type != "item.started" || events[1].Type != "item.completed" {
		t.Fatalf("web search event types = %#v", events)
	}
	for _, event := range events {
		if event.Item == nil || event.Item.Type != "web_search" || event.Item.ID != invocation.CallID {
			t.Fatalf("web search item = %#v", event.Item)
		}
	}
	if events[1].Item.Query != "rust" || events[1].Item.Action["type"] != "search" {
		t.Fatalf("completed web search item = %#v", events[1].Item)
	}

	flattened := *invocation
	flattened.ToolName = tool.PlainName("web.run")
	flattenedEvents := eventsFromToolExecution(&turn.ToolExecutionResult{
		Invocation: &flattened,
		Output: &tool.Output{
			CallID:   flattened.CallID,
			ToolName: flattened.ToolName,
			Success:  true,
			Data: map[string]any{
				"web_search_action": map[string]any{"type": "search", "query": "rust"},
			},
		},
	})
	if len(flattenedEvents) != 1 || flattenedEvents[0].Item == nil || flattenedEvents[0].Item.Type != "web_search" {
		t.Fatalf("flattened web.run lifecycle = %#v", flattenedEvents)
	}
	flattenedStart := eventsFromToolCallExecution(&turn.ToolExecutionResult{Invocation: &flattened})
	if len(flattenedStart) != 1 || flattenedStart[0].Item == nil || flattenedStart[0].Item.Type != "web_search" {
		t.Fatalf("flattened web.run start mapping = %#v", flattenedStart)
	}
	flattenedCompleted, ok := eventFromToolOutputExecution(&turn.ToolExecutionResult{
		Invocation: &flattened,
		Output: &tool.Output{
			CallID:   flattened.CallID,
			ToolName: flattened.ToolName,
			Success:  true,
			Data: map[string]any{
				"web_search_action": map[string]any{"type": "search", "query": "rust"},
			},
		},
	})
	if !ok || flattenedCompleted.Item == nil || flattenedCompleted.Item.Type != "web_search" {
		t.Fatalf("flattened web.run completion mapping = %#v, ok=%v", flattenedCompleted, ok)
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

func TestCodeModeWaitIsHiddenAndNotCollaborationTool(t *testing.T) {
	execution := &turn.ToolExecutionResult{
		Invocation: &tool.Invocation{CallID: "wait-cell", ToolName: tool.PlainName("wait"), Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"cell_id":"1"}`}},
		Output:     &tool.Output{CallID: "wait-cell", ToolName: tool.PlainName("wait"), Success: true, Body: "done"},
	}
	if isCollabExecution(execution) {
		t.Fatal("plain code-mode wait must not be classified as a collaboration tool")
	}
	if events := eventsFromToolExecution(execution); len(events) != 0 {
		t.Fatalf("code-mode wait events = %#v, want hidden", events)
	}
	if _, ok := normalizeCollabToolString("wait"); ok {
		t.Fatal("plain wait must not normalize to wait_agent")
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

func TestRunPersistsNewThreadBeforeInterruptedTurnCompletes(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	runner := NewRunner(home)
	runner.Agent = &failingAgent{err: context.Canceled}
	var stdout, stderr bytes.Buffer
	_, err := runner.RunContext(context.Background(), &Request{Exec: cli.ExecOptions{Prompt: "interrupt me", JSON: true}}, strings.NewReader(""), &stdout, &stderr)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RunContext error = %v, want context.Canceled", err)
	}
	var started protocol.ThreadEvent
	if err := json.Unmarshal(bytes.Split(bytes.TrimSpace(stdout.Bytes()), []byte("\n"))[0], &started); err != nil {
		t.Fatalf("decode thread.started: %v", err)
	}
	threadID := started.ThreadID
	record, readErr := session.NewStore(filepath.Join(home, "sessions")).Read(session.ThreadID(threadID), true, true)
	if readErr != nil {
		t.Fatalf("interrupted thread was not persisted: %v", readErr)
	}
	if record.Metadata.CWD == "" || len(record.Items) != 1 || record.Items[0].Role != "user" || record.Items[0].Text != "interrupt me" {
		t.Fatalf("interrupted record = %#v", record)
	}
	if len(record.Metadata.RolloutTurns) != 1 || record.Metadata.RolloutTurns[0].Status != "interrupted" || record.Metadata.RolloutTurns[0].CompletedAt == nil {
		t.Fatalf("interrupted turn state = %#v", record.Metadata.RolloutTurns)
	}
	if _, rolloutErr := rollout.FindThreadPath(home, threadID, false); rolloutErr != nil {
		t.Fatalf("interrupted rollout was not persisted: %v", rolloutErr)
	}
	rolloutPath, _ := rollout.FindThreadPath(home, threadID, false)
	lines, _, rolloutErr := rollout.Load(rolloutPath)
	if rolloutErr != nil {
		t.Fatalf("Load interrupted rollout error: %v", rolloutErr)
	}
	foundAborted := false
	for i := range lines {
		if lines[i].Type == "event_msg" && strings.Contains(string(lines[i].Payload), `"type":"turn_aborted"`) {
			foundAborted = true
		}
	}
	if !foundAborted {
		t.Fatalf("interrupted rollout missing turn_aborted: %#v", lines)
	}
}

func TestExecCompactStatusCountsItemsAfterLastModelItemLikeRust(t *testing.T) {
	cfg, _ := config.Load("")
	record := &session.Record{
		ID: "thread-compact-active",
		Metadata: session.Metadata{
			Model: "gpt-5.4",
			Extra: map[string]any{
				"last_token_usage": map[string]any{
					"input_tokens": 244790, "output_tokens": 10, "total_tokens": 244800,
				},
				"model_context_window": int64(258400),
			},
		},
		Items: []session.Item{
			{ID: "u1", Type: "message", Role: "user", Text: "first", CreatedAt: fixedExecTime()},
			{ID: "a1", Type: "agent_message", Role: "assistant", Text: "answer", CreatedAt: fixedExecTime()},
			// Trailing user prompt persisted by an interrupted turn is not
			// reflected in last_token_usage and must be counted like Rust does.
			{ID: "u2", Type: "message", Role: "user", Text: strings.Repeat("resume prompt ", 50), CreatedAt: fixedExecTime()},
		},
	}
	status := execCompactStatus(record, "gpt-5.4", cfg)
	if !status.ShouldCompact || status.Reason != compact.ReasonTokenLimit {
		t.Fatalf("stored usage at limit plus trailing prompt should compact: %#v", status)
	}
	if status.ActiveContextTokens <= 244800 {
		t.Fatalf("active context tokens should exceed stored usage: %#v", status)
	}
}

func TestExecCompactStatusReadsSnakeCaseStoredUsage(t *testing.T) {
	cfg, _ := config.Load("")
	record := &session.Record{
		ID: "thread-snake-usage",
		Metadata: session.Metadata{
			Model: "gpt-5.4",
			Extra: map[string]any{
				"last_token_usage": map[string]any{
					"total_tokens": 245000,
				},
				"model_context_window": int64(258400),
			},
		},
	}
	status := execCompactStatus(record, "gpt-5.4", cfg)
	if !status.ShouldCompact {
		t.Fatalf("snake_case stored usage should compact: %#v", status)
	}
}

func TestExecCompactStatusBodyAfterPrefixChargesGrowthOnly(t *testing.T) {
	cfg, _ := config.Load("")
	cfg.Values["model_auto_compact_token_limit_scope"] = "body_after_prefix"
	record := &session.Record{
		ID: "thread-body-after-prefix",
		Metadata: session.Metadata{
			Model: "gpt-5.4",
			Extra: map[string]any{
				"last_token_usage": map[string]any{
					"input_tokens": 244790, "output_tokens": 10, "total_tokens": 244800,
				},
				"model_context_window":        int64(258400),
				"auto_compact_window_prefill": int64(220000),
			},
		},
		Items: []session.Item{
			{ID: "u1", Type: "message", Role: "user", Text: "first", CreatedAt: fixedExecTime()},
			{ID: "a1", Type: "agent_message", Role: "assistant", Text: "answer", CreatedAt: fixedExecTime()},
		},
	}
	// Rust apply_rollout_reconstruction re-estimates the carried prefix from
	// the reconstructed history on every resume; the persisted server-observed
	// marker from a previous process does not survive.
	estimated := compact.EstimateTokens(execCompactItemsFromSession(record.Items))
	status := execCompactStatus(record, "gpt-5.4", cfg)
	if got, want := status.AutoCompactScopeTokens, 244800-estimated; got != want {
		t.Fatalf("scope tokens = %d, want %d (active minus history estimate)", got, want)
	}
	if status.ShouldCompact {
		t.Fatalf("scoped charge stays below the limit: %#v", status)
	}
	// Removing the marker does not change the estimate: it is ignored.
	delete(record.Metadata.Extra, "auto_compact_window_prefill")
	delete(record.Metadata.Extra, "auto_compact_window_prefill_server_observed")
	status = execCompactStatus(record, "gpt-5.4", cfg)
	if got, want := status.AutoCompactScopeTokens, 244800-estimated; got != want {
		t.Fatalf("marker-independent scope tokens = %d, want %d", got, want)
	}
	if status.ShouldCompact {
		t.Fatalf("estimated prefix should keep scope charge below limit: %#v", status)
	}
}

func TestExecAutoCompactFallbackFollowUp(t *testing.T) {
	cfg, _ := config.Load("")
	cfg.Values["features"] = map[string]any{
		"token_budget": map[string]any{
			"enabled": true, "auto_compact_fallback_prompt": "wrap up and summarize",
			"auto_compact_fallback_buffer_tokens": 30000,
		},
	}
	runner := &Runner{}
	followUp := runner.execAutoCompactFallbackFollowUp(cfg, "gpt-5.4")
	if followUp == nil {
		t.Fatal("expected a fallback follow-up when token budget is configured")
	}
	limit := int64(244800) // 9/10 of the fallback 272000 window for gpt-5.4
	if items := followUp(&turn.SamplingFollowUpContext{Usage: model.AgentUsage{TotalTokens: limit}}); len(items) != 1 {
		t.Fatalf("follow-up at base-window exhaustion should inject the prompt, got %d items", len(items))
	}
	if items := followUp(&turn.SamplingFollowUpContext{Usage: model.AgentUsage{TotalTokens: limit}}); len(items) != 0 {
		t.Fatalf("follow-up should deliver only once, got %d items", len(items))
	}
	fresh := runner.execAutoCompactFallbackFollowUp(cfg, "gpt-5.4")
	if items := fresh(&turn.SamplingFollowUpContext{Usage: model.AgentUsage{TotalTokens: limit - 1000}}); len(items) != 0 {
		t.Fatalf("follow-up below the base-window limit should not inject, got %d items", len(items))
	}
	if items := fresh(&turn.SamplingFollowUpContext{Usage: model.AgentUsage{TotalTokens: limit + 30000}}); len(items) != 0 {
		t.Fatalf("follow-up when compaction is due should not inject, got %d items", len(items))
	}
}

func TestExecAutoCompactFallbackFollowUpReminder(t *testing.T) {
	cfg, _ := config.Load("")
	cfg.Values["features"] = map[string]any{
		"token_budget": map[string]any{
			"enabled":                             true,
			"reminder_threshold_tokens":           12000,
			"reminder_message_template":           "only {n_remaining} tokens remaining",
			"auto_compact_fallback_prompt":        "wrap up and summarize",
			"auto_compact_fallback_buffer_tokens": 30000,
		},
	}
	runner := &Runner{}
	followUp := runner.execAutoCompactFallbackFollowUp(cfg, "gpt-5.4")
	if followUp == nil {
		t.Fatal("expected a follow-up when token budget is configured")
	}
	limit := int64(244800) // 9/10 of the fallback 272000 window for gpt-5.4
	// Reminder only: remaining 5000 is below the threshold but above zero.
	items := followUp(&turn.SamplingFollowUpContext{Usage: model.AgentUsage{TotalTokens: limit - 5000}})
	if len(items) != 1 {
		t.Fatalf("reminder should inject once, got %d items", len(items))
	}
	if !inputItemsContainText(items, "only 5000 tokens remaining") {
		t.Fatalf("reminder item = %#v, want substituted template", items)
	}
	// Once per window: a later sample does not redeliver.
	if items := followUp(&turn.SamplingFollowUpContext{Usage: model.AgentUsage{TotalTokens: limit - 4000}}); len(items) != 0 {
		t.Fatalf("reminder should deliver only once, got %d items", len(items))
	}
	// Base-window exhaustion delivers the fallback prompt.
	if items := followUp(&turn.SamplingFollowUpContext{Usage: model.AgentUsage{TotalTokens: limit}}); len(items) != 1 {
		t.Fatalf("fallback at exhaustion should inject once, got %d items", len(items))
	}
	fresh := runner.execAutoCompactFallbackFollowUp(cfg, "gpt-5.4")
	// Both fire together when the reminder threshold and exhaustion coincide.
	if items := fresh(&turn.SamplingFollowUpContext{Usage: model.AgentUsage{TotalTokens: limit}}); len(items) != 2 {
		t.Fatalf("reminder + fallback at exhaustion should inject 2 items, got %d", len(items))
	}
	// The reminder fires even when compaction is already due (Rust records it
	// before the roll-over check); the fallback does not.
	due := runner.execAutoCompactFallbackFollowUp(cfg, "gpt-5.4")
	if items := due(&turn.SamplingFollowUpContext{Usage: model.AgentUsage{TotalTokens: limit + 30000}}); len(items) != 1 {
		t.Fatalf("reminder when compaction is due should inject once, got %d items", len(items))
	}
}

type recordingAgent struct {
	message string
	usage   model.AgentUsage
	request *model.AgentRequest
}

func TestExecCompactResumeBeforeTurnIgnoresPersistedPrefill(t *testing.T) {
	home := t.TempDir()
	now := fixedExecTime()
	store := session.NewStore(filepath.Join(home, "sessions"))
	// A stale server-observed marker from a previous process must not survive
	// a resume: Rust apply_rollout_reconstruction re-estimates the carried
	// prefix from the reconstructed history.
	record := &session.Record{
		ID: "thread-exec-compact-marker", SessionID: "thread-exec-compact-marker",
		CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{
			Model:         "gpt-5.4",
			ModelProvider: model.OpenAIProviderID,
			CWD:           ".",
			Source:        "exec",
			ThreadSource:  "user",
			HistoryMode:   "legacy",
			Extra: map[string]any{
				"last_token_usage": map[string]any{
					"input_tokens": 264790, "output_tokens": 10, "total_tokens": 270000,
				},
				"model_context_window":                        int64(258400),
				"auto_compact_window_prefill":                 int64(260000),
				"auto_compact_window_prefill_server_observed": true,
			},
		},
		Items: []session.Item{
			{ID: "u1", Type: "message", Role: "user", Text: "first", CreatedAt: now},
			{ID: "a1", Type: "agent_message", Role: "assistant", Text: "answer", CreatedAt: now},
		},
	}
	if err := store.Save(record); err != nil {
		t.Fatalf("Save record error = %v", err)
	}
	runner := NewRunner(home)
	runner.Now = func() time.Time { return now }
	if err := runner.createExecRollout(record, now); err != nil {
		t.Fatalf("createExecRollout error = %v", err)
	}
	cfg, _ := config.Load("")
	cfg.Values["model_auto_compact_token_limit_scope"] = "body_after_prefix"
	agent := &recordingAgent{message: "compacted summary"}
	loaded, err := store.Read(session.ThreadID("thread-exec-compact-marker"), true, true)
	if err != nil {
		t.Fatalf("Read record error = %v", err)
	}
	// Pre-turn status: the scoped charge is active minus the history estimate,
	// not the stale persisted baseline.
	before := execCompactStatus(loaded, "gpt-5.4", cfg)
	estimateBefore := compact.EstimateTokens(execCompactItemsFromSession(loaded.Items))
	if got, want := before.AutoCompactScopeTokens, int(before.ActiveContextTokens)-estimateBefore; got != want {
		t.Fatalf("scope tokens before compact = %d, want %d (active minus history estimate)", got, want)
	}
	compacted, err := runner.compactResumeBeforeTurn(context.Background(), &execResumeContext{Record: loaded}, "thread-exec-compact-marker", "turn-compact", "gpt-5.4", model.OpenAIProviderID, cfg, agent, nil)
	if err != nil {
		t.Fatalf("compactResumeBeforeTurn error = %v", err)
	}
	if !compacted {
		t.Fatal("expected pre-turn compaction")
	}
	// After compaction the decision is re-derived from the compacted history;
	// the stale marker is ignored.
	saved, err := session.NewStore(filepath.Join(home, "sessions")).Read(session.ThreadID("thread-exec-compact-marker"), true, true)
	if err != nil {
		t.Fatalf("Read saved record error = %v", err)
	}
	after := execCompactStatus(saved, "gpt-5.4", cfg)
	estimateAfter := compact.EstimateTokens(execCompactItemsFromSession(saved.Items))
	if got, want := after.AutoCompactScopeTokens, max(0, int(after.ActiveContextTokens)-estimateAfter); got != want {
		t.Fatalf("scope tokens after compact = %d, want %d (active minus compacted-history estimate, clamped)", got, want)
	}
	if after.ShouldCompact {
		t.Fatalf("post-compaction status should not demand another compact: %#v", after)
	}
}

type execMidTurnRolloverAgent struct {
	mu    sync.Mutex
	calls int
}

func (a *execMidTurnRolloverAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	if request != nil && strings.Contains(request.Prompt, "Summarize the conversation") {
		return &model.AgentResponse{
			ResponseID: "resp-compact",
			Message:    "compacted summary",
			Items:      []model.AgentItem{{ID: "compact-1", Type: "agent_message", Text: "compacted summary"}},
			Usage:      model.AgentUsage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
		}, nil
	}
	a.mu.Lock()
	a.calls++
	call := a.calls
	a.mu.Unlock()
	if call == 1 {
		return &model.AgentResponse{
			ResponseID: "resp-tool",
			Usage:      model.AgentUsage{InputTokens: 300000, OutputTokens: 128000, TotalTokens: 428000},
			Items: []model.AgentItem{{
				ID: "call-1", Type: "function_call", Name: "echo", CallID: "call-1", Arguments: `{}`,
			}},
		}, nil
	}
	return &model.AgentResponse{
		ResponseID: "resp-final",
		Message:    "done",
		Items:      []model.AgentItem{{ID: "final-1", Type: "agent_message", Text: "done"}},
		Usage:      model.AgentUsage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	}, nil
}

func TestExecMidTurnRollOverCompactsWhileSamplingLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	runner := NewRunner(home)
	runner.Agent = &execMidTurnRolloverAgent{}
	var stdout, stderr bytes.Buffer
	result, err := runner.Run(Request{
		Exec: cli.ExecOptions{Prompt: "run the tool loop", JSON: true},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v stderr=%q", err, stderr.String())
	}
	record := loadSessionRecord(t, home, result.ThreadID)
	if record.Metadata.Extra["compaction_trigger"] != string(compact.TriggerAuto) ||
		record.Metadata.Extra["compaction_phase"] != string(compact.PhaseMidTurn) ||
		record.Metadata.Extra["compaction_summary"] != "compacted summary" {
		t.Fatalf("mid-turn compaction metadata = %#v", record.Metadata.Extra)
	}
	persisted := ""
	for _, item := range record.Items {
		persisted += item.Text
	}
	if strings.Contains(persisted, "call-1") {
		t.Fatalf("pre-compaction tool call persisted after roll-over: %q", persisted)
	}
	if !strings.Contains(persisted, "compacted summary") || !strings.Contains(persisted, "done") {
		t.Fatalf("compacted history missing summary or final response: %q", persisted)
	}
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

func agentRequestToolsContainResponsesTool(request *model.AgentRequest, toolType string, name string) bool {
	if request == nil {
		return false
	}
	for _, toolValue := range request.Tools {
		toolMap, ok := toolValue.(map[string]any)
		if ok && fmt.Sprint(toolMap["type"]) == toolType && fmt.Sprint(toolMap["name"]) == name {
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

type recordingStaticAgent struct {
	requests chan model.AgentRequest
	response *model.AgentResponse
}

type firstTurnBlockingAgent struct {
	mu      sync.Mutex
	calls   int
	started chan struct{}
}

func (a *firstTurnBlockingAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	a.mu.Lock()
	a.calls++
	call := a.calls
	a.mu.Unlock()
	if call == 1 {
		close(a.started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &model.AgentResponse{
		Message: "completed after interrupt",
		Items:   []model.AgentItem{{ID: "final", Type: "agent_message", Text: "completed after interrupt", Data: map[string]any{"phase": "final_answer"}}},
		Usage:   model.AgentUsage{InputTokens: 1, OutputTokens: 1}, Model: request.Model, ProviderID: request.ProviderID,
	}, nil
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

func (a *recordingStaticAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if request != nil && a != nil && a.requests != nil {
		a.requests <- *request
	}
	if a == nil || a.response == nil {
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

type codeModeLegacyShellLoopAgent struct {
	requests []model.AgentRequest
}

type codeModeLegacyShellRecoveryAgent struct {
	requests []model.AgentRequest
}

type preTurnCompactAgent struct {
	requests []model.AgentRequest
}

func (a *preTurnCompactAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.requests = append(a.requests, *request)
	if len(a.requests) == 1 {
		return &model.AgentResponse{Message: "压缩摘要", Usage: model.AgentUsage{InputTokens: 100, OutputTokens: 10, TotalTokens: 110}, Model: request.Model, ProviderID: request.ProviderID}, nil
	}
	return &model.AgentResponse{Message: "继续完成", Items: []model.AgentItem{{ID: "msg", Type: "agent_message", Text: "继续完成"}}, Usage: model.AgentUsage{InputTokens: 40, OutputTokens: 5, TotalTokens: 45}, Model: request.Model, ProviderID: request.ProviderID}, nil
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

func (a *codeModeLegacyShellLoopAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.requests = append(a.requests, *request)
	if len(a.requests) == 1 {
		return &model.AgentResponse{Items: []model.AgentItem{{
			ID: "commentary-weather", Type: "agent_message", Text: "Checking Yunnan weather.", Data: map[string]any{"phase": "commentary"},
		}, {
			ID: "code-weather", Type: "custom_tool_call", Name: tool.CodeModeExecToolName, CallID: "code-weather",
			Input: `const r = await tools.shell_command({"command":"curl.exe -sS https://sdk-weather.invalid/Yunnan?format=j1","workdir":"C:\\workspace"}); text(r.output)`,
		}}}, nil
	}
	return &model.AgentResponse{
		Message: "Weather lookup failed once; no duplicate final.",
		Items: []model.AgentItem{{
			ID: "final-weather", Type: "agent_message", Text: "Weather lookup failed once; no duplicate final.", Data: map[string]any{"phase": "final_answer"},
		}},
	}, nil
}

func (a *codeModeLegacyShellRecoveryAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.requests = append(a.requests, *request)
	if len(a.requests) == 1 {
		return &model.AgentResponse{Items: []model.AgentItem{{
			ID: "code-recovery", Type: "custom_tool_call", Name: tool.CodeModeExecToolName, CallID: "code-recovery",
			Input: `try { await tools.shell_command({command: "fail"}); } catch (error) { text("CAUGHT_FAILURE"); } const recovered = await tools.shell_command({command: "recover"}); text(recovered.output);`,
		}}}, nil
	}
	return &model.AgentResponse{
		Message: "CODE_MODE_RECOVERY_DONE",
		Items: []model.AgentItem{{
			ID: "final-recovery", Type: "agent_message", Text: "CODE_MODE_RECOVERY_DONE", Data: map[string]any{"phase": "final_answer"},
		}},
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

func createExecResumeRolloutOnly(t *testing.T, home string, threadID string, cwd string, now time.Time, text string) string {
	t.Helper()
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome:     home,
		SessionID:     threadID,
		ThreadID:      threadID,
		Source:        "exec",
		ThreadSource:  "user",
		Originator:    "codex_cli_rs",
		CWD:           cwd,
		ModelProvider: model.OpenAIProviderID,
		HistoryMode:   "legacy",
		Now:           now,
	})
	if err != nil {
		t.Fatalf("NewRecorder returned error: %v", err)
	}
	if err := rollout.AppendSessionItems(recorder, []session.Item{{
		ID: "user-" + threadID, Type: "message", Role: "user", Text: text, CreatedAt: now,
	}}, now); err != nil {
		_ = recorder.Close()
		t.Fatalf("AppendSessionItems returned error: %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close recorder returned error: %v", err)
	}
	return recorder.Path()
}

func TestExecAgentOriginatorUsesSDKOverride(t *testing.T) {
	t.Setenv("CODEX_INTERNAL_ORIGINATOR_OVERRIDE", "codex_sdk_ts")
	if got := execAgentOriginator(&Request{}); got != "codex_sdk_ts" {
		t.Fatalf("execAgentOriginator() = %q, want codex_sdk_ts", got)
	}
}

func TestExecAgentOriginatorDefaultsToRustCLI(t *testing.T) {
	t.Setenv("CODEX_INTERNAL_ORIGINATOR_OVERRIDE", "")
	if got := execAgentOriginator(&Request{}); got != "codex_cli_rs" {
		t.Fatalf("execAgentOriginator() = %q, want codex_cli_rs", got)
	}
}
