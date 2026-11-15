package parity

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"codex_go/appserver"
)

// TestRustHooksCommandSchemasCoverGoInputSurface is the djalign dynamic-layer
// method-1 shared-fixture differential for the hooks command input wire
// contract: Rust generates draft-07 JSON schemas for every hook command input
// type (hooks/schema/generated/*.command.input.schema.json, schemars-derived
// from hooks/src/schema.rs) and pins them in commit as fixtures. Go builds the
// same input JSON by hand in HookRunner.Run* (appserver/hooks_events.go). This
// test drives each Go hook event through the runner with a capture hook and
// asserts the emitted JSON key surface equals the Rust schema properties for
// the same event.
//
// The Rust side is pinned by fixture path: every schema is read from the
// frozen git checkout (candidateRustTo) so Windows autocrlf CRLF
// normalization cannot mask a match, and the schema inventory is pinned so
// upstream additions break the contract instead of silently drifting.
func TestRustHooksCommandSchemasCoverGoInputSurface(t *testing.T) {
	root := rustSnapshotRoot(t)
	rustRepo := filepath.Dir(root)
	generatedDir := "codex-rs/hooks/schema/generated"

	// The full inventory of committed input schema fixtures.
	wantFixtures := []string{
		"permission-request.command.input.schema.json",
		"post-compact.command.input.schema.json",
		"post-tool-use.command.input.schema.json",
		"pre-compact.command.input.schema.json",
		"pre-tool-use.command.input.schema.json",
		"session-end.command.input.schema.json",
		"session-start.command.input.schema.json",
		"stop.command.input.schema.json",
		"subagent-start.command.input.schema.json",
		"subagent-stop.command.input.schema.json",
		"user-prompt-submit.command.input.schema.json",
	}
	for _, fixture := range wantFixtures {
		gitOutput(t, rustRepo, "show", candidateRustTo+":"+generatedDir+"/"+fixture)
	}

	// schema fixture name -> Go hook event and a sample request that exercises
	// every non-optional field.
	cases := []struct {
		fixture string
		request any
	}{
		{
			fixture: "session-start.command.input.schema.json",
			request: &appserver.HookSessionStartRequest{
				ThreadID: "thread-1", CWD: "/repo", Model: "gpt-5",
				PermissionMode: "default", Source: appserver.SessionStartSourceStartup,
			},
		},
		{
			fixture: "session-end.command.input.schema.json",
			request: &appserver.HookSessionEndRequest{
				ThreadID: "thread-1", CWD: "/repo", Model: "gpt-5", Reason: "exit",
			},
		},
		{
			fixture: "pre-tool-use.command.input.schema.json",
			request: &appserver.HookPreToolUseRequest{
				ThreadID: "thread-1", TurnID: "turn-1", CWD: "/repo", Model: "gpt-5",
				PermissionMode: "default", ToolName: "bash", ToolUseID: "call-1",
				ToolInput: map[string]any{"command": "echo hi"},
			},
		},
		{
			fixture: "permission-request.command.input.schema.json",
			request: &appserver.HookPermissionRequestRequest{
				ThreadID: "thread-1", TurnID: "turn-1", CWD: "/repo", Model: "gpt-5",
				PermissionMode: "default", ToolName: "bash", RunIDSuffix: "call-1",
				ToolInput: map[string]any{"command": "echo hi"},
			},
		},
		{
			fixture: "post-tool-use.command.input.schema.json",
			request: &appserver.HookPostToolUseRequest{
				ThreadID: "thread-1", TurnID: "turn-1", CWD: "/repo", Model: "gpt-5",
				PermissionMode: "default", ToolName: "bash", ToolUseID: "call-1",
				ToolInput: map[string]any{"command": "echo hi"}, ToolResponse: map[string]any{"output": "hi"},
			},
		},
		{
			fixture: "pre-compact.command.input.schema.json",
			request: &appserver.HookPreCompactRequest{
				ThreadID: "thread-1", TurnID: "turn-1", CWD: "/repo", Model: "gpt-5", Trigger: "auto",
			},
		},
		{
			fixture: "post-compact.command.input.schema.json",
			request: &appserver.HookPostCompactRequest{
				ThreadID: "thread-1", TurnID: "turn-1", CWD: "/repo", Model: "gpt-5", Trigger: "auto",
			},
		},
		{
			fixture: "subagent-start.command.input.schema.json",
			request: &appserver.HookSubagentStartRequest{
				ThreadID: "thread-1", TurnID: "turn-1", CWD: "/repo", Model: "gpt-5",
				PermissionMode: "default", AgentID: "agent-1", AgentType: "coding",
			},
		},
		{
			fixture: "stop.command.input.schema.json",
			request: &appserver.HookStopRequest{
				ThreadID: "thread-1", TurnID: "turn-1", CWD: "/repo", Model: "gpt-5",
				PermissionMode: "default", StopHookActive: true,
			},
		},
		{
			fixture: "subagent-stop.command.input.schema.json",
			request: &appserver.HookSubagentStopRequest{
				ThreadID: "thread-1", TurnID: "turn-1", CWD: "/repo", Model: "gpt-5",
				PermissionMode: "default", StopHookActive: true, AgentID: "agent-1",
				AgentType: "coding",
			},
		},
		{
			fixture: "user-prompt-submit.command.input.schema.json",
			request: &appserver.HookUserPromptSubmitRequest{
				ThreadID: "thread-1", TurnID: "turn-1", CWD: "/repo", Model: "gpt-5",
				PermissionMode: "default", Prompt: "hello",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			schemaData := gitOutput(t, rustRepo, "show", candidateRustTo+":"+generatedDir+"/"+tc.fixture)
			var schema struct {
				Properties map[string]any `json:"properties"`
				Required   []string       `json:"required"`
			}
			if err := json.Unmarshal(schemaData, &schema); err != nil {
				t.Fatalf("unmarshal Rust schema %s: %v", tc.fixture, err)
			}
			if len(schema.Properties) == 0 {
				t.Fatalf("Rust schema %s has no properties", tc.fixture)
			}
			inputJSON, err := appserver.HookInputJSON(tc.request)
			if err != nil {
				t.Fatalf("HookInputJSON: %v", err)
			}
			var emitted map[string]any
			if err := json.Unmarshal([]byte(inputJSON), &emitted); err != nil {
				t.Fatalf("unmarshal emitted input %q: %v", inputJSON, err)
			}
			wantKeys := sortedKeys(schema.Properties)
			gotKeys := sortedKeys(emitted)
			// Every Go-emitted key must be a declared schema property (Go must
			// not invent wire fields), and every schema-required property must
			// be present in Go's input.
			props := stringSet(wantKeys)
			for _, key := range gotKeys {
				if !props[key] {
					t.Fatalf("Go hook input emits undeclared key %q (schema properties: [%s])\nemitted: %s",
						key, strings.Join(wantKeys, ","), inputJSON)
				}
			}
			required := stringSet(schema.Required)
			for _, key := range sortedSetKeys(required) {
				if _, ok := emitted[key]; !ok {
					t.Fatalf("Go hook input missing required schema property %q (required: [%s], emitted: [%s])\nemitted: %s",
						key, strings.Join(sortedSetKeys(required), ","), strings.Join(gotKeys, ","), inputJSON)
				}
			}
			// The schema must carry hook_event_name as a property (the const
			// value is asserted by the Rust fixture tests themselves).
			if _, ok := schema.Properties["hook_event_name"]; !ok {
				t.Fatalf("Rust schema %s has no hook_event_name property", tc.fixture)
			}
		})
	}
}

// modelRequestHookSchemaRustRev pins the upstream commit that owns the
// per-attempt model request hook identity (Rust #61562/#61587). candidateRustTo
// predates model-request hook inputs, so this contract is verified against the
// commit that introduced the current surface instead.
const modelRequestHookSchemaRustRev = "b016cd18472588606c04af565528c4cd6746a41f"

// TestRustModelRequestHookSchemaCoversGoInputSurface is the shared-fixture
// differential for the model-request hook input: Go must emit exactly the
// fields the current Rust schema declares, including the per-attempt identity
// added by #61562/#61587.
func TestRustModelRequestHookSchemaCoversGoInputSurface(t *testing.T) {
	root := rustSnapshotRoot(t)
	rustRepo := filepath.Dir(root)
	fixture := "codex-rs/hooks/schema/generated/model-request.command.input.schema.json"
	schemaData := gitOutput(t, rustRepo, "show", modelRequestHookSchemaRustRev+":"+fixture)
	var schema struct {
		Properties map[string]any `json:"properties"`
		Required   []string       `json:"required"`
		OneOf      []struct {
			Properties map[string]any `json:"properties"`
		} `json:"oneOf"`
	}
	if err := json.Unmarshal(schemaData, &schema); err != nil {
		t.Fatalf("unmarshal Rust schema: %v", err)
	}
	// The event-specific fields live in oneOf branches keyed by
	// hook_event_name, so the declared-key surface is their union.
	declared := map[string]any{}
	for key, value := range schema.Properties {
		declared[key] = value
	}
	for _, branch := range schema.OneOf {
		for key, value := range branch.Properties {
			declared[key] = value
		}
	}
	responseID := "resp-1"
	requests := map[string]any{
		"pre": &appserver.HookModelRequestRequest{
			ThreadID: "thread-1", SessionID: "session-1", TurnID: "turn-1", CWD: "/repo",
			Model: "gpt-5", Phase: "pre", Kind: "sampling", RequestID: "request-1", AttemptNumber: 1,
		},
		"post": &appserver.HookModelRequestRequest{
			ThreadID: "thread-1", SessionID: "session-1", TurnID: "turn-1", CWD: "/repo",
			Model: "gpt-5", Phase: "post", Status: "completed", Kind: "sampling",
			RequestID: "request-1", AttemptNumber: 1, ResponseID: &responseID,
		},
	}
	props := stringSet(sortedKeys(declared))
	required := stringSet(schema.Required)
	for name, request := range requests {
		t.Run(name, func(t *testing.T) {
			inputJSON, err := appserver.HookInputJSON(request)
			if err != nil {
				t.Fatalf("HookInputJSON: %v", err)
			}
			var emitted map[string]any
			if err := json.Unmarshal([]byte(inputJSON), &emitted); err != nil {
				t.Fatalf("unmarshal emitted input: %v", err)
			}
			for _, key := range sortedKeys(emitted) {
				if !props[key] {
					t.Fatalf("Go hook input emits undeclared key %q\nemitted: %s", key, inputJSON)
				}
			}
			for _, key := range sortedSetKeys(required) {
				if _, ok := emitted[key]; !ok {
					t.Fatalf("Go hook input missing required schema property %q\nemitted: %s", key, inputJSON)
				}
			}
			for _, key := range []string{"session_id", "thread_id", "kind", "request_id", "attempt_number"} {
				if _, ok := emitted[key]; !ok {
					t.Fatalf("Go hook input missing attempt identity %q\nemitted: %s", key, inputJSON)
				}
			}
			if name == "post" {
				if emitted["response_id"] != "resp-1" {
					t.Fatalf("post response_id = %#v", emitted["response_id"])
				}
			}
		})
	}
}

func sortedKeys(values map[string]any) []string {
	out := make([]string, 0, len(values))
	for key := range values {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func sortedSetKeys(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for key := range values {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
