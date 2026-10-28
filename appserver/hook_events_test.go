package appserver

import (
	"reflect"
	"strings"
	"testing"

	"codex_go/config"
)

func TestEnvironmentConfigParsesAndRoundTripsHookEvents(t *testing.T) {
	matcher := "Bash"
	status := "Scanning"
	timeout := uint64(30)
	configMap := map[string]any{
		"environment_hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"matcher": "Bash",
					"hooks": []any{
						map[string]any{
							"type":           "command",
							"command":        "echo managed",
							"commandWindows": "Write-Output managed",
							"timeoutSec":     30,
							"async":          true,
							"statusMessage":  "Scanning",
						},
					},
				},
			},
			"PostToolUse": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{
							"type":   "mcp_tool",
							"server": "security",
							"tool":   "scan",
							"input":  map[string]any{"paths": []any{"a.go"}, "limit": 5},
						},
					},
				},
			},
		},
	}

	parsed, err := environmentConfigFromAny(configMap)
	if err != nil {
		t.Fatalf("environmentConfigFromAny: %v", err)
	}
	if got := parsed.EnvironmentHooks.HandlerCount(); got != 2 {
		t.Fatalf("handler count = %d, want 2", got)
	}
	preToolUse := parsed.EnvironmentHooks.PreToolUse
	if len(preToolUse) != 1 || len(preToolUse[0].Hooks) != 1 {
		t.Fatalf("PreToolUse = %#v, want one group with one handler", preToolUse)
	}
	command := preToolUse[0].Hooks[0]
	if command.Type != "command" || command.Command != "echo managed" {
		t.Fatalf("command handler = %#v", command)
	}
	if command.CommandWindows == nil || *command.CommandWindows != "Write-Output managed" {
		t.Fatalf("commandWindows = %#v", command.CommandWindows)
	}
	if command.TimeoutSec == nil || *command.TimeoutSec != 30 || !command.Async {
		t.Fatalf("timeout/async = %#v/%v", command.TimeoutSec, command.Async)
	}
	if preToolUse[0].Matcher == nil || *preToolUse[0].Matcher != matcher {
		t.Fatalf("matcher = %#v, want %q", preToolUse[0].Matcher, matcher)
	}
	if statusMessage := command.StatusMessage; statusMessage == nil || *statusMessage != status {
		t.Fatalf("statusMessage = %#v, want %q", command.StatusMessage, status)
	}
	if command.TimeoutSec != nil && *command.TimeoutSec != timeout {
		t.Fatalf("timeout = %d, want %d", *command.TimeoutSec, timeout)
	}
	mcpInput := parsed.EnvironmentHooks.PostToolUse[0].Hooks[0].Input
	if got := mcpInput["limit"]; got != int64(5) {
		t.Fatalf("mcp input limit = %#v, want int64(5)", got)
	}

	serialized := environmentConfigToAny(parsed)
	hooksAny, ok := serialized["environment_hooks"].(map[string]any)
	if !ok {
		t.Fatalf("serialized environment_hooks = %#v", serialized["environment_hooks"])
	}
	if _, present := hooksAny["PreToolUse"]; !present {
		t.Fatalf("serialized hooks = %#v, want PascalCase event keys", hooksAny)
	}
	if _, present := hooksAny["preToolUse"]; present {
		t.Fatalf("serialized hooks = %#v, want no camelCase event keys", hooksAny)
	}
	roundTripped, err := environmentConfigFromAny(serialized)
	if err != nil {
		t.Fatalf("environmentConfigFromAny(round trip): %v", err)
	}
	if !reflect.DeepEqual(parsed.EnvironmentHooks, roundTripped.EnvironmentHooks) {
		t.Fatalf("hook events did not round trip:\n got %#v\nwant %#v", roundTripped.EnvironmentHooks, parsed.EnvironmentHooks)
	}
}

func TestEnvironmentConfigRejectsMalformedHookEvents(t *testing.T) {
	tests := []struct {
		name    string
		handler map[string]any
		want    string
	}{
		{
			name: "null mcp input",
			handler: map[string]any{
				"type": "mcp_tool", "server": "security", "tool": "scan",
				"input": map[string]any{"metadata": map[string]any{"optional": nil}},
			},
			want: "representable as TOML",
		},
		{
			name: "unsigned overflow in mcp input",
			handler: map[string]any{
				"type": "mcp_tool", "server": "security", "tool": "scan",
				"input": map[string]any{"value": uint64(^uint64(0))},
			},
			want: "representable as TOML",
		},
		{
			name:    "unknown handler type",
			handler: map[string]any{"type": "webhook"},
			want:    "unknown hook handler type",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := environmentConfigFromAny(map[string]any{
				"environment_hooks": map[string]any{
					"PostToolUse": []any{map[string]any{"hooks": []any{test.handler}}},
				},
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestEnvironmentConfigCloneIsolatesHookEvents(t *testing.T) {
	matcher := "Bash"
	original := &EnvironmentConfig{EnvironmentHooks: HookEvents{
		PreToolUse: []config.ConfiguredHookGroup{{
			Matcher: &matcher,
			Hooks: []config.ConfiguredHookHandler{{
				Type:       "mcp_tool",
				Server:     "security",
				Tool:       "scan",
				Input:      map[string]any{"paths": []any{"a.go"}},
				TimeoutSec: func() *uint64 { value := uint64(1); return &value }(),
			}},
		}},
	}}

	cloned := cloneEnvironmentConfig(original)
	*cloned.EnvironmentHooks.PreToolUse[0].Matcher = "Edit"
	cloned.EnvironmentHooks.PreToolUse[0].Hooks[0].Input["paths"].([]any)[0] = "b.go"
	cloned.EnvironmentHooks.PreToolUse[0].Hooks[0].TimeoutSec = nil

	if *original.EnvironmentHooks.PreToolUse[0].Matcher != "Bash" {
		t.Fatal("clone shared the matcher pointer")
	}
	if original.EnvironmentHooks.PreToolUse[0].Hooks[0].Input["paths"].([]any)[0] != "a.go" {
		t.Fatal("clone shared the MCP input map")
	}
	if original.EnvironmentHooks.PreToolUse[0].Hooks[0].TimeoutSec == nil {
		t.Fatal("clone shared the timeout pointer")
	}
}

func TestEnvironmentConfigStringReportsHookHandlerCount(t *testing.T) {
	config := &EnvironmentConfig{
		EnvironmentHooks:       HookEvents{PreToolUse: []config.ConfiguredHookGroup{{Hooks: []config.ConfiguredHookHandler{{Type: "command", Command: "echo"}}}}},
		ShellEnvironmentPolicy: map[string]any{"set": map[string]any{"SECRET": "value"}},
	}
	rendered := config.String()
	if !strings.Contains(rendered, "environment_hooks: 1 handlers") {
		t.Fatalf("String() = %q, want hook handler count", rendered)
	}
	if !strings.Contains(rendered, "shell_environment_policy: <redacted>") {
		t.Fatalf("String() = %q, want redacted shell policy", rendered)
	}
	if strings.Contains(rendered, "SECRET") {
		t.Fatalf("String() = %q leaked shell policy values", rendered)
	}
}
