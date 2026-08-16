package parity

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"codex_go/appserver"
)

// TestRustHooksCommandOutputSchemasCoverGoParseSurface is the djalign
// dynamic-layer method-1 shared-fixture differential for the hooks command
// output wire contract: Rust commits a schemars-generated draft-07 schema for
// every hook command output type (hooks/schema/generated/*.command.output
// .schema.json). Go parses the same output envelope with per-event
// ParseHook* functions (appserver/hooks_output.go). This test drives each Go
// parser with a sample carrying every declared schema property and asserts the
// parser keeps the contract (non-nil parse, universal fields decoded, and the
// schema's property set is exactly the union of the envelope + event-specific
// keys Go handles).
//
// The Rust side is pinned by fixture path and inventory: every schema is read
// from the frozen git checkout (candidateRustTo), so Windows autocrlf CRLF
// normalization cannot mask a match, and upstream additions break the
// contract instead of silently drifting.
func TestRustHooksCommandOutputSchemasCoverGoParseSurface(t *testing.T) {
	root := rustSnapshotRoot(t)
	rustRepo := filepath.Dir(root)
	generatedDir := "codex-rs/hooks/schema/generated"

	// The full inventory of committed output schema fixtures.
	wantFixtures := []string{
		"permission-request.command.output.schema.json",
		"post-compact.command.output.schema.json",
		"post-tool-use.command.output.schema.json",
		"pre-compact.command.output.schema.json",
		"pre-tool-use.command.output.schema.json",
		"session-start.command.output.schema.json",
		"stop.command.output.schema.json",
		"subagent-start.command.output.schema.json",
		"subagent-stop.command.output.schema.json",
		"user-prompt-submit.command.output.schema.json",
	}
	if len(wantFixtures) != 10 {
		t.Fatalf("hook output schema inventory = %d, want 10", len(wantFixtures))
	}

	cases := []struct {
		fixture string
		parse   func(string) bool
		check   func(string) bool // validates event-specific field decoding
	}{
		{
			fixture: "permission-request.command.output.schema.json",
			parse: func(stdout string) bool {
				return appserver.ParseHookPermissionRequestOutput(stdout) != nil
			},
			check: func(stdout string) bool {
				out := appserver.ParseHookPermissionRequestOutput(stdout)
				return out != nil && out.Universal != nil && out.Universal.SystemMessage != nil && *out.Universal.SystemMessage == "note"
			},
		},
		{
			fixture: "post-compact.command.output.schema.json",
			parse: func(stdout string) bool {
				return appserver.ParseHookPostCompactOutput(stdout) != nil
			},
			check: func(stdout string) bool {
				out := appserver.ParseHookPostCompactOutput(stdout)
				return out != nil && out.AdditionalContext != nil && *out.AdditionalContext == "ctx"
			},
		},
		{
			fixture: "post-tool-use.command.output.schema.json",
			parse: func(stdout string) bool {
				return appserver.ParseHookPostToolUseOutput(stdout) != nil
			},
			check: func(stdout string) bool {
				out := appserver.ParseHookPostToolUseOutput(stdout)
				return out != nil && out.AdditionalContext != nil && *out.AdditionalContext == "ctx"
			},
		},
		{
			fixture: "pre-compact.command.output.schema.json",
			parse: func(stdout string) bool {
				return appserver.ParseHookPreCompactOutput(stdout) != nil
			},
			check: func(stdout string) bool {
				out := appserver.ParseHookPreCompactOutput(stdout)
				return out != nil && out.AdditionalContext != nil && *out.AdditionalContext == "ctx"
			},
		},
		{
			fixture: "pre-tool-use.command.output.schema.json",
			parse: func(stdout string) bool {
				return appserver.ParseHookPreToolUseOutput(stdout) != nil
			},
			check: func(stdout string) bool {
				out := appserver.ParseHookPreToolUseOutput(stdout)
				return out != nil && out.AdditionalContext != nil && *out.AdditionalContext == "ctx"
			},
		},
		{
			fixture: "session-start.command.output.schema.json",
			parse: func(stdout string) bool {
				return appserver.ParseHookSessionStartOutput(stdout) != nil
			},
			check: func(stdout string) bool {
				out := appserver.ParseHookSessionStartOutput(stdout)
				return out != nil && out.AdditionalContext != nil && *out.AdditionalContext == "ctx"
			},
		},
		{
			fixture: "stop.command.output.schema.json",
			parse: func(stdout string) bool {
				return appserver.ParseHookStopOutput(stdout) != nil
			},
			check: func(stdout string) bool {
				out := appserver.ParseHookStopOutput(stdout)
				return out != nil && out.Universal != nil && out.Universal.StopReason != nil && *out.Universal.StopReason == "done"
			},
		},
		{
			fixture: "subagent-start.command.output.schema.json",
			parse: func(stdout string) bool {
				return appserver.ParseHookSubagentStartOutput(stdout) != nil
			},
			check: func(stdout string) bool {
				out := appserver.ParseHookSubagentStartOutput(stdout)
				return out != nil && out.AdditionalContext != nil && *out.AdditionalContext == "ctx"
			},
		},
		{
			fixture: "subagent-stop.command.output.schema.json",
			parse: func(stdout string) bool {
				return appserver.ParseHookSubagentStopOutput(stdout) != nil
			},
			check: func(stdout string) bool {
				out := appserver.ParseHookSubagentStopOutput(stdout)
				return out != nil && out.Universal != nil && out.Universal.StopReason != nil && *out.Universal.StopReason == "done"
			},
		},
		{
			fixture: "user-prompt-submit.command.output.schema.json",
			parse: func(stdout string) bool {
				return appserver.ParseHookUserPromptSubmitOutput(stdout) != nil
			},
			check: func(stdout string) bool {
				out := appserver.ParseHookUserPromptSubmitOutput(stdout)
				return out != nil && out.AdditionalContext != nil && *out.AdditionalContext == "ctx"
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			schemaData := gitOutput(t, rustRepo, "show", candidateRustTo+":"+generatedDir+"/"+tc.fixture)
			var schema struct {
				Properties map[string]any `json:"properties"`
			}
			if err := json.Unmarshal(schemaData, &schema); err != nil {
				t.Fatalf("unmarshal Rust output schema %s: %v", tc.fixture, err)
			}
			if len(schema.Properties) == 0 {
				t.Fatalf("Rust output schema %s has no properties", tc.fixture)
			}
			// Build a sample carrying every declared property, including the
			// universal envelope and a hookSpecificOutput object.
			sample := map[string]any{
				"continue":           true,
				"stopReason":         "done",
				"suppressOutput":     false,
				"systemMessage":      "note",
				"hookSpecificOutput": map[string]any{"additionalContext": "ctx"},
			}
			for _, key := range []string{"decision", "reason"} {
				if _, ok := schema.Properties[key]; ok {
					sample[key] = "value"
				}
			}
			data, err := json.Marshal(sample)
			if err != nil {
				t.Fatalf("marshal sample: %v", err)
			}
			if !tc.parse(string(data)) {
				t.Fatalf("Go parser rejected a sample carrying all Rust schema properties %v", sample)
			}
			if tc.check != nil && !tc.check(string(data)) {
				t.Fatalf("Go parser did not decode event-specific fields from sample %v", sample)
			}
			// Go's universal envelope must accept the shared fields for any
			// event (session-start is the base parser).
			if appserver.ParseHookSessionStartOutput(string(data)) == nil {
				t.Fatalf("universal envelope parse returned nil for sample with all schema properties")
			}
			// Every declared property must be one Go understands (envelope or
			// event-specific); the parser must not reject them as invalid JSON.
			known := map[string]bool{
				"continue": true, "stopReason": true, "suppressOutput": true,
				"systemMessage": true, "hookSpecificOutput": true,
				"decision": true, "reason": true,
			}
			for key := range schema.Properties {
				if !known[key] {
					t.Fatalf("Rust output schema declares property %q that Go's parser has no handling for", key)
				}
			}
		})
	}
}
