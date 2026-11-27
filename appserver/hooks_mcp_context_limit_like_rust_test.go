package appserver

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMCPHookContextLimitsAreBoundedAndHashedByEffectiveValueLikeRust(t *testing.T) {
	home := t.TempDir()
	body := `{"hooks":{"SessionStart":[
		{"hooks":[{"type":"mcp_tool","server":"scanner","tool":"check"}]},
		{"hooks":[{"type":"mcp_tool","server":"scanner","tool":"check","additionalContextLimit":2500}]},
		{"hooks":[{"type":"mcp_tool","server":"scanner","tool":"check","additionalContextLimit":0}]},
		{"hooks":[{"type":"mcp_tool","server":"scanner","tool":"check","additionalContextLimit":10000}]},
		{"hooks":[{"type":"mcp_tool","server":"scanner","tool":"check","additionalContextLimit":10001}]}
	]}}`
	if err := os.WriteFile(filepath.Join(home, "hooks.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	service := NewHookDiscoveryService(home)
	service.McpToolHooksEnabled = true
	response := service.Discover(&HookListParams{CWDs: []string{t.TempDir()}}, "")
	if len(response.Data) != 1 || len(response.Data[0].Hooks) != 5 {
		t.Fatalf("Discover() = %+v", response)
	}
	hooks := response.Data[0].Hooks
	want := []*int64{nil, int64Ptr(2500), int64Ptr(10000), int64Ptr(10000), int64Ptr(10000)}
	for i := range hooks {
		if want[i] == nil {
			if hooks[i].AdditionalContextLimit != nil {
				t.Fatalf("hook %d limit = %v, want unset", i, *hooks[i].AdditionalContextLimit)
			}
			continue
		}
		if hooks[i].AdditionalContextLimit == nil || *hooks[i].AdditionalContextLimit != *want[i] {
			t.Fatalf("hook %d limit = %v, want %d", i, hooks[i].AdditionalContextLimit, *want[i])
		}
	}
	if hooks[0].CurrentHash != hooks[1].CurrentHash {
		t.Fatal("the explicit 2,500 default must hash like an unset limit")
	}
	if hooks[0].CurrentHash == hooks[2].CurrentHash {
		t.Fatal("the clamped MCP limit must participate in the hook identity")
	}
	if hooks[2].CurrentHash != hooks[3].CurrentHash || hooks[3].CurrentHash != hooks[4].CurrentHash {
		t.Fatal("MCP limits 0 and 10,001 must hash like the effective 10,000 cap")
	}
	warnings := response.Data[0].Warnings
	if len(warnings) != 2 || !warningsContain(warnings, "from 0 to 10000") || !warningsContain(warnings, "from 10001 to 10000") {
		t.Fatalf("warnings = %#v", warnings)
	}
}
