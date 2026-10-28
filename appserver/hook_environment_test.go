package appserver

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/session"
	"codex_go/tool"
	"codex_go/turn"
)

type fakeEnvironmentHookCall struct {
	EnvironmentID string
	Command       string
	Env           map[string]string
	InputJSON     string
	Timeout       time.Duration
}

// Rust environment_hook_sources: environment-owned handlers only run for the
// environments the call targets (`target_environments`).
func TestFilterHooksForTargetEnvironmentsLikeRust(t *testing.T) {
	environmentA := "env-a"
	environmentB := "env-b"
	hooks := []HookMetadata{
		{EventName: HookEventPreToolUse},
		{EventName: HookEventPreToolUse, EnvironmentID: &environmentA},
		{EventName: HookEventPreToolUse, EnvironmentID: &environmentB},
	}
	filtered := filterHooksForTargetEnvironments(hooks, []string{"env-a"})
	if len(filtered) != 2 || filtered[0].EnvironmentID != nil || ptrStringValue(filtered[1].EnvironmentID) != "env-a" {
		t.Fatalf("filtered hooks = %#v", filtered)
	}
	unscoped := filterHooksForTargetEnvironments(hooks, nil)
	if len(unscoped) != 3 {
		t.Fatalf("unscoped hooks = %#v, want every environment hook", unscoped)
	}
}

// The tool hook adapter must not run another environment's managed hook for a
// call that targets one environment.
func TestToolHookAdapterFiltersEnvironmentHooksByTargetLikeRust(t *testing.T) {
	sources := []environmentHookSource{
		{EnvironmentID: "env-a", CWD: "/repo-a", Hooks: HookEvents{PreToolUse: []config.ConfiguredHookGroup{{Matcher: stringPointer("^Bash$"), Hooks: []config.ConfiguredHookHandler{environmentCommandHook("echo a", nil)}}}}},
		{EnvironmentID: "env-b", CWD: "/repo-b", Hooks: HookEvents{PreToolUse: []config.ConfiguredHookGroup{{Matcher: stringPointer("^Bash$"), Hooks: []config.ConfiguredHookHandler{environmentCommandHook("echo b", nil)}}}}},
	}
	executorA := &fakeEnvironmentHookExecutor{}
	executorB := &fakeEnvironmentHookExecutor{}
	discovered := discoverEnvironmentHooks(sources, 0, map[string]HookEnvironmentExecutor{"env-a": executorA, "env-b": executorB})
	adapter := NewToolHookAdapter(NewHookRunner(), discovered.Handlers, "thread-1", "turn-1", "/host")
	if _, err := adapter.RunPreToolUse(context.Background(), &tool.Invocation{CallID: "call-1", ToolName: tool.PlainName("exec_command")}, &tool.PreToolUsePayload{
		ToolName:           &tool.HookToolName{Name: "Bash"},
		ToolInput:          map[string]any{"command": "ls"},
		TargetEnvironments: []string{"env-b"},
	}); err != nil {
		t.Fatalf("RunPreToolUse() error = %v", err)
	}
	if len(executorA.calls) != 0 {
		t.Fatalf("env-a hooks ran for an env-b call: %#v", executorA.calls)
	}
	if len(executorB.calls) != 1 {
		t.Fatalf("env-b hooks = %#v", executorB.calls)
	}
}

// Rust thread.list_hooks reads the session's retained environment snapshot, so
// the listing keeps environment declarations after the turn finishes.
func TestHooksListThreadIncludesPersistedEnvironmentHooksAfterTurnLikeRust(t *testing.T) {
	home := t.TempDir()
	threadCWD := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	now := time.Now().UTC()
	record := &session.Record{
		ID: "thread-1", SessionID: "thread-1",
		CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: threadCWD},
	}
	if err := store.Create(record); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "bash", Path: "bash"}, t.TempDir())
	if _, err := manager.Add(&EnvironmentAddParams{EnvironmentID: "env-1", ExecServerURL: "ws://127.0.0.1:1"}); err != nil {
		t.Fatalf("add environment: %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		DefaultCWD:     threadCWD,
		Hooks:          NewHookRegistry(),
		HooksDiscovery: NewHookDiscoveryService(home),
		ThreadRouter:   NewRouter(store),
		Environment:    manager,
	})
	t.Cleanup(func() { router.Close() })
	if err := router.threads.RetainLiveThread(store, record); err != nil {
		t.Fatalf("retain live thread: %v", err)
	}
	params := &turn.TurnStartParams{
		ThreadID: "thread-1",
		CWD:      threadCWD,
		Environments: []map[string]any{{
			"environmentId": "env-1",
			"cwd":           "/remote",
			"config": map[string]any{"state": "ready", "config": map[string]any{
				"environment_hooks": map[string]any{
					"UserPromptSubmit": []any{map[string]any{
						"hooks": []any{map[string]any{"type": "command", "command": "echo listed"}},
					}},
				},
			}},
		}},
	}
	// The turn finished: only the persisted selections remain.
	if err := router.persistTurnEnvironmentSelections(params); err != nil {
		t.Fatalf("persist environment selections: %v", err)
	}
	threadID := "thread-1"
	response := router.Handle(requestWithParams(t, IntID(1), MethodHooksList, HookListParams{ThreadID: &threadID}))
	if response.Error != nil {
		t.Fatalf("hooks/list threadId error = %+v", response.Error)
	}
	result := response.Result.(*HookListResponse)
	var listed *HookMetadata
	for i := range result.Data {
		for j := range result.Data[i].Hooks {
			if ptrStringValue(result.Data[i].Hooks[j].EnvironmentID) == "env-1" {
				listed = &result.Data[i].Hooks[j]
			}
		}
	}
	if listed == nil {
		t.Fatalf("environment hook missing from %+v", result.Data)
	}
	if listed.SourcePath != "<environment:env-1>/requirements.toml" || listed.EventName != HookEventUserPromptSubmit {
		t.Fatalf("listed environment hook = %#v", *listed)
	}
}

type fakeEnvironmentHookExecutor struct {
	calls  []fakeEnvironmentHookCall
	output *EnvironmentHookOutput
	err    error
}

func (e *fakeEnvironmentHookExecutor) Execute(_ context.Context, environmentID string, command string, env map[string]string, inputJSON string, timeout time.Duration) (*EnvironmentHookOutput, error) {
	e.calls = append(e.calls, fakeEnvironmentHookCall{
		EnvironmentID: environmentID,
		Command:       command,
		Env:           env,
		InputJSON:     inputJSON,
		Timeout:       timeout,
	})
	if e.err != nil {
		return nil, e.err
	}
	if e.output == nil {
		return &EnvironmentHookOutput{}, nil
	}
	return e.output, nil
}

func environmentCommandHook(command string, commandWindows *string) config.ConfiguredHookHandler {
	return config.ConfiguredHookHandler{Type: string(HookHandlerCommand), Command: command, CommandWindows: commandWindows}
}

// Rust append_environment_hooks (#56304): environment hooks are a required,
// managed, system source under a synthetic requirements path, and their
// command_windows override resolves against the environment's path convention.
func TestDiscoverEnvironmentHooksScopesHandlersLikeRust(t *testing.T) {
	windowsCommand := "Write-Output windows"
	sources := []environmentHookSource{{
		EnvironmentID: "env-1",
		CWD:           `C:\repo`,
		Hooks: HookEvents{PreToolUse: []config.ConfiguredHookGroup{{
			Matcher: stringPointer("^Bash$"),
			Hooks:   []config.ConfiguredHookHandler{environmentCommandHook("echo unix", &windowsCommand)},
		}}},
	}}
	discovered := discoverEnvironmentHooks(sources, 7, nil)
	if len(discovered.RequiredLoadErrors) != 0 {
		t.Fatalf("required load errors = %v", discovered.RequiredLoadErrors)
	}
	if len(discovered.Handlers) != 1 || len(discovered.Entries) != 1 {
		t.Fatalf("handlers = %d, entries = %d", len(discovered.Handlers), len(discovered.Entries))
	}
	entry := discovered.Entries[0]
	if entry.CWD != `C:\repo` {
		t.Fatalf("entry cwd = %q", entry.CWD)
	}
	hook := discovered.Handlers[0]
	if ptrStringValue(hook.Command) != windowsCommand {
		t.Fatalf("command = %q, want the Windows override", ptrStringValue(hook.Command))
	}
	if hook.SourcePath != "<environment:env-1>/requirements.toml" {
		t.Fatalf("source path = %q", hook.SourcePath)
	}
	if hook.Source != HookSourceSystem || !hook.IsManaged || !hook.Enabled || hook.TrustStatus != HookTrustManaged {
		t.Fatalf("environment hook metadata = %#v", hook)
	}
	if ptrStringValue(hook.EnvironmentID) != "env-1" {
		t.Fatalf("environment id = %q", ptrStringValue(hook.EnvironmentID))
	}
	if hook.DisplayOrder != 7 {
		t.Fatalf("display order = %d", hook.DisplayOrder)
	}
	if hook.environmentCWD != `C:\repo` || !hook.withholdTranscript {
		t.Fatalf("environment scoping = (%q, %v)", hook.environmentCWD, hook.withholdTranscript)
	}
}

// Rust record_load_failure: an unloadable environment hook is reported as a
// required load error, the signal readiness uses to fail closed.
func TestDiscoverEnvironmentHooksRecordsRequiredLoadErrorsLikeRust(t *testing.T) {
	sources := []environmentHookSource{{
		EnvironmentID: "env-2",
		CWD:           "/repo",
		Hooks: HookEvents{PreToolUse: []config.ConfiguredHookGroup{
			{Matcher: stringPointer("["), Hooks: []config.ConfiguredHookHandler{environmentCommandHook("echo ok", nil)}},
			{Hooks: []config.ConfiguredHookHandler{environmentCommandHook("   ", nil)}},
		}},
	}}
	discovered := discoverEnvironmentHooks(sources, 0, nil)
	if len(discovered.Handlers) != 0 {
		t.Fatalf("handlers = %#v", discovered.Handlers)
	}
	joined := strings.Join(discovered.RequiredLoadErrors, "; ")
	if !strings.Contains(joined, "invalid matcher") {
		t.Fatalf("required load errors = %q", joined)
	}
	if !strings.Contains(joined, "skipping empty hook command") {
		t.Fatalf("required load errors = %q", joined)
	}
}

// Rust HandlerSourcePath::Environment::event_input: environment hooks see their
// own cwd and never controller-owned transcript paths.
func TestEnvironmentHookInputJSONScopesCWDAndTranscripts(t *testing.T) {
	input := `{"cwd":"/host","transcript_path":"/t.jsonl","agent_transcript_path":"/a.jsonl","prompt":"hello"}`
	rewritten := environmentHookInputJSON(input, "/workspace")
	var parsed map[string]any
	if err := json.Unmarshal([]byte(rewritten), &parsed); err != nil {
		t.Fatalf("unmarshal rewritten input: %v", err)
	}
	if parsed["cwd"] != "/workspace" {
		t.Fatalf("cwd = %#v", parsed["cwd"])
	}
	if parsed["transcript_path"] != nil {
		t.Fatalf("transcript_path = %#v", parsed["transcript_path"])
	}
	if parsed["agent_transcript_path"] != nil {
		t.Fatalf("agent_transcript_path = %#v", parsed["agent_transcript_path"])
	}
	if parsed["prompt"] != "hello" {
		t.Fatalf("prompt = %#v", parsed["prompt"])
	}
}

// A remote executor may run another platform, so the hook cwd is sent as a
// file URI whose grammar follows the environment's path spelling.
func TestEnvironmentExecCWDRendersPathURIs(t *testing.T) {
	if got := environmentExecCWD(`C:\repo`); got != "file:///C:/repo" {
		t.Fatalf("Windows cwd = %q", got)
	}
	if got := environmentExecCWD("/repo"); got != "file:///repo" {
		t.Fatalf("POSIX cwd = %q", got)
	}
	if got := environmentExecCWD("file:///C:/repo"); got != "file:///C:/repo" {
		t.Fatalf("file URI cwd = %q", got)
	}
}

// #56304: command hooks owned by a remote environment run through the
// environment executor with the environment-scoped input, and report the
// executor's exit status and output.
func TestEnvironmentHookRunsOnOwningExecutorWithScopedInput(t *testing.T) {
	executor := &fakeEnvironmentHookExecutor{output: &EnvironmentHookOutput{ExitCode: 0, Stdout: "from-executor\n"}}
	command := "echo hook"
	sources := []environmentHookSource{{
		EnvironmentID: "remote-1",
		CWD:           "/workspace",
		Hooks: HookEvents{UserPromptSubmit: []config.ConfiguredHookGroup{{
			Hooks: []config.ConfiguredHookHandler{environmentCommandHook(command, nil)},
		}}},
	}}
	discovered := discoverEnvironmentHooks(sources, 0, map[string]HookEnvironmentExecutor{"remote-1": executor})
	if len(discovered.Handlers) != 1 {
		t.Fatalf("handlers = %#v", discovered.Handlers)
	}
	runner := NewHookRunner()
	result, err := runner.Run(context.Background(), &HookRunRequest{
		ThreadID:  "thread-1",
		CWD:       "/host",
		EventName: HookEventUserPromptSubmit,
		InputJSON: `{"cwd":"/host","transcript_path":"/t.jsonl","prompt":"hello"}`,
		Hooks:     discovered.Handlers,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(executor.calls) != 1 {
		t.Fatalf("executor calls = %d", len(executor.calls))
	}
	call := executor.calls[0]
	if call.EnvironmentID != "remote-1" || call.Command != command {
		t.Fatalf("executor call = %#v", call)
	}
	var input map[string]any
	if err := json.Unmarshal([]byte(call.InputJSON), &input); err != nil {
		t.Fatalf("executor input = %q: %v", call.InputJSON, err)
	}
	if input["cwd"] != "/workspace" || input["transcript_path"] != nil {
		t.Fatalf("executor input = %#v", input)
	}
	if len(result.Runs) != 1 || result.Runs[0].Status != HookRunCompleted {
		t.Fatalf("runs = %#v", result.Runs)
	}
	if len(result.AdditionalContexts) != 1 || result.AdditionalContexts[0] != "from-executor" {
		t.Fatalf("additional contexts = %#v", result.AdditionalContexts)
	}
}

// Rust permission_request.rs (#56304): environment authority may restrict the
// controller's policy, never grant it; a deny still applies.
func TestEnvironmentHookPermissionRequestAllowIsIgnored(t *testing.T) {
	run := func(t *testing.T, output string) *HookRunResult {
		t.Helper()
		executor := &fakeEnvironmentHookExecutor{output: &EnvironmentHookOutput{ExitCode: 0, Stdout: output}}
		command := "hook"
		sources := []environmentHookSource{{
			EnvironmentID: "remote-1",
			CWD:           "/workspace",
			Hooks: HookEvents{PermissionRequest: []config.ConfiguredHookGroup{{
				Hooks: []config.ConfiguredHookHandler{environmentCommandHook(command, nil)},
			}}},
		}}
		discovered := discoverEnvironmentHooks(sources, 0, map[string]HookEnvironmentExecutor{"remote-1": executor})
		result, err := NewHookRunner().Run(context.Background(), &HookRunRequest{
			ThreadID:      "thread-1",
			CWD:           "/host",
			EventName:     HookEventPermissionRequest,
			MatcherInputs: []string{"Bash"},
			InputJSON:     "{}",
			Hooks:         discovered.Handlers,
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		return result
	}
	allow := run(t, `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}`)
	if allow.PermissionRequestDecision != nil {
		t.Fatalf("environment allow produced a verdict: %#v", allow.PermissionRequestDecision)
	}
	deny := run(t, `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"deny","message":"owner says no"}}}`)
	if deny.PermissionRequestDecision == nil || deny.PermissionRequestDecision.Kind != HookPermissionRequestDeny {
		t.Fatalf("environment deny = %#v", deny.PermissionRequestDecision)
	}
	if deny.PermissionRequestDecision.Message == nil || *deny.PermissionRequestDecision.Message != "owner says no" {
		t.Fatalf("environment deny message = %#v", deny.PermissionRequestDecision.Message)
	}
}

// Rust thread.list_hooks (#56304) adds the thread's ready environment hook
// declarations to the cwd inventory.
func TestHooksListThreadIncludesEnvironmentHooksLikeRust(t *testing.T) {
	home := t.TempDir()
	threadCWD := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	now := time.Now().UTC()
	record := &session.Record{
		ID: "thread-1", SessionID: "thread-1",
		CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: threadCWD},
	}
	if err := store.Create(record); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "bash", Path: "bash"}, t.TempDir())
	if _, err := manager.Add(&EnvironmentAddParams{EnvironmentID: "env-1", ExecServerURL: "ws://127.0.0.1:1"}); err != nil {
		t.Fatalf("add environment: %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		DefaultCWD:     threadCWD,
		Hooks:          NewHookRegistry(),
		HooksDiscovery: NewHookDiscoveryService(home),
		ThreadRouter:   NewRouter(store),
		Environment:    manager,
	})
	t.Cleanup(func() { router.Close() })
	if err := router.threads.RetainLiveThread(store, record); err != nil {
		t.Fatalf("retain live thread: %v", err)
	}
	params := &turn.TurnStartParams{
		ThreadID: "thread-1",
		CWD:      threadCWD,
		Environments: []map[string]any{{
			"environmentId": "env-1",
			"cwd":           "/remote",
			"config": map[string]any{"state": "ready", "config": map[string]any{
				"environment_hooks": map[string]any{
					"UserPromptSubmit": []any{map[string]any{
						"hooks": []any{map[string]any{"type": "command", "command": "echo listed"}},
					}},
				},
			}},
		}},
	}
	if err := router.registerActiveRuntimeTurn("thread-1", "turn-1", nil, now.UnixMilli(), params); err != nil {
		t.Fatalf("register active turn: %v", err)
	}

	threadID := "thread-1"
	response := router.Handle(requestWithParams(t, IntID(1), MethodHooksList, HookListParams{ThreadID: &threadID}))
	if response.Error != nil {
		t.Fatalf("hooks/list threadId error = %+v", response.Error)
	}
	result := response.Result.(*HookListResponse)
	var listed *HookMetadata
	for i := range result.Data {
		for j := range result.Data[i].Hooks {
			if ptrStringValue(result.Data[i].Hooks[j].EnvironmentID) == "env-1" {
				listed = &result.Data[i].Hooks[j]
			}
		}
	}
	if listed == nil {
		t.Fatalf("environment hook missing from %+v", result.Data)
	}
	if listed.SourcePath != "<environment:env-1>/requirements.toml" || listed.EventName != HookEventUserPromptSubmit {
		t.Fatalf("listed environment hook = %#v", *listed)
	}
}
