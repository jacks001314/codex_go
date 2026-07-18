package appserver

import "testing"

func TestParseHookPermissionRequestRejectsReservedFields(t *testing.T) {
	cases := []struct {
		name string
		json string
		want string
	}{
		{
			name: "updated input",
			json: `{"continue":true,"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow","updatedInput":{}}}}`,
			want: "PermissionRequest hook returned unsupported updatedInput",
		},
		{
			name: "updated permissions",
			json: `{"continue":true,"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow","updatedPermissions":{}}}}`,
			want: "PermissionRequest hook returned unsupported updatedPermissions",
		},
		{
			name: "interrupt",
			json: `{"continue":true,"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow","interrupt":true}}}`,
			want: "PermissionRequest hook returned unsupported interrupt:true",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed := ParseHookPermissionRequestOutput(tc.json)
			if parsed == nil || parsed.InvalidReason == nil || *parsed.InvalidReason != tc.want {
				t.Fatalf("ParseHookPermissionRequestOutput() = %#v, want %q", parsed, tc.want)
			}
		})
	}
}

func TestParseHookPermissionRequestDecision(t *testing.T) {
	parsed := ParseHookPermissionRequestOutput(`{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"deny","message":" no "}}}`)
	if parsed == nil || parsed.InvalidReason != nil || parsed.Decision == nil {
		t.Fatalf("ParseHookPermissionRequestOutput() = %#v", parsed)
	}
	if parsed.Decision.Kind != HookPermissionRequestDeny || parsed.Decision.Message == nil || *parsed.Decision.Message != "no" {
		t.Fatalf("decision = %#v", parsed.Decision)
	}
}

func TestParseHookPreToolUseOutput(t *testing.T) {
	allow := ParseHookPreToolUseOutput(`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","updatedInput":{"command":"echo ok"},"additionalContext":"ctx"}}`)
	if allow == nil || allow.InvalidReason != nil || allow.UpdatedInput == nil || allow.AdditionalContext == nil || *allow.AdditionalContext != "ctx" {
		t.Fatalf("allow = %#v", allow)
	}

	deny := ParseHookPreToolUseOutput(`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":" blocked "}}`)
	if deny == nil || deny.InvalidReason != nil || deny.BlockReason == nil || *deny.BlockReason != "blocked" {
		t.Fatalf("deny = %#v", deny)
	}

	invalid := ParseHookPreToolUseOutput(`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask"}}`)
	if invalid == nil || invalid.InvalidReason == nil || *invalid.InvalidReason != "PreToolUse hook returned unsupported permissionDecision:ask" {
		t.Fatalf("invalid = %#v", invalid)
	}
}

func TestParseHookLegacyPreToolUseBlocksWithReason(t *testing.T) {
	parsed := ParseHookPreToolUseOutput(`{"decision":"block","reason":" stop "}`)
	if parsed == nil || parsed.InvalidReason != nil || parsed.BlockReason == nil || *parsed.BlockReason != "stop" {
		t.Fatalf("parsed = %#v", parsed)
	}
	missingReason := ParseHookPreToolUseOutput(`{"decision":"block"}`)
	if missingReason == nil || missingReason.InvalidReason == nil || *missingReason.InvalidReason != "PreToolUse hook returned decision:block without a non-empty reason" {
		t.Fatalf("missingReason = %#v", missingReason)
	}
}

func TestParseHookPostToolUseOutput(t *testing.T) {
	parsed := ParseHookPostToolUseOutput(`{"decision":"block","reason":"bad","hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":"ctx"}}`)
	if parsed == nil || !parsed.ShouldBlock || parsed.InvalidBlockReason != nil || parsed.AdditionalContext == nil || *parsed.AdditionalContext != "ctx" {
		t.Fatalf("parsed = %#v", parsed)
	}
	invalid := ParseHookPostToolUseOutput(`{"decision":"block","reason":" "}`)
	if invalid == nil || invalid.ShouldBlock || invalid.InvalidBlockReason == nil || *invalid.InvalidBlockReason != "PostToolUse hook returned decision:block without a non-empty reason" {
		t.Fatalf("invalid = %#v", invalid)
	}
}

func TestParseHookStopOutput(t *testing.T) {
	parsed := ParseHookSubagentStopOutput(`{"decision":"block","reason":"wait"}`)
	if parsed == nil || !parsed.ShouldBlock || parsed.Reason == nil || *parsed.Reason != "wait" {
		t.Fatalf("parsed = %#v", parsed)
	}
	invalid := ParseHookStopOutput(`{"decision":"block"}`)
	if invalid == nil || invalid.ShouldBlock || invalid.InvalidBlockReason == nil || *invalid.InvalidBlockReason != "Stop hook returned decision:block without a non-empty reason" {
		t.Fatalf("invalid = %#v", invalid)
	}
}

func TestHookOutputLooksLikeJSON(t *testing.T) {
	if !HookOutputLooksLikeJSON("  {\"continue\":true}") || HookOutputLooksLikeJSON("plain text") {
		t.Fatalf("HookOutputLooksLikeJSON returned unexpected result")
	}
}
