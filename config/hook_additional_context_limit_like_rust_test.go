package config

import (
	"encoding/json"
	"testing"
)

func TestHookAdditionalContextLimitConfigLikeRust(t *testing.T) {
	requirements, err := ParseRequirementsTOML([]byte(`
[[hooks.PreToolUse]]
hooks = [
  { type = "command", command = "echo ok", additional_context_limit = 2500 },
  { type = "mcp_tool", server = "scan", tool = "check", additionalContextLimit = 10001 },
]
`))
	if err != nil {
		t.Fatalf("ParseRequirementsTOML() error = %v", err)
	}
	if requirements == nil || requirements.Hooks == nil || len(requirements.Hooks.PreToolUse) != 1 {
		t.Fatalf("requirements hooks = %#v", requirements)
	}
	handlers := requirements.Hooks.PreToolUse[0].Hooks
	if len(handlers) != 2 {
		t.Fatalf("handlers = %#v, want 2", handlers)
	}
	if handlers[0].AdditionalContextLimit == nil || *handlers[0].AdditionalContextLimit != 2500 {
		t.Fatalf("command additionalContextLimit = %#v, want 2500", handlers[0].AdditionalContextLimit)
	}
	if handlers[1].AdditionalContextLimit == nil || *handlers[1].AdditionalContextLimit != 10001 {
		t.Fatalf("MCP additionalContextLimit = %#v, want raw 10001 before discovery clamping", handlers[1].AdditionalContextLimit)
	}
}

func TestConfiguredHookHandlerSerializesAdditionalContextLimitLikeRust(t *testing.T) {
	tests := []struct {
		name     string
		handler  ConfiguredHookHandler
		wantSet  bool
		wantJSON float64
	}{
		{
			name:    "command unset omits field",
			handler: ConfiguredHookHandler{Type: "command", Command: "echo ok"},
		},
		{
			name:     "command configured",
			handler:  ConfiguredHookHandler{Type: "command", Command: "echo ok", AdditionalContextLimit: uint64Ptr(2500)},
			wantSet:  true,
			wantJSON: 2500,
		},
		{
			name:     "MCP tool configured",
			handler:  ConfiguredHookHandler{Type: "mcp_tool", Server: "scan", Tool: "check", AdditionalContextLimit: uint64Ptr(10000)},
			wantSet:  true,
			wantJSON: 10000,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := json.Marshal(&tt.handler)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			var shape map[string]any
			if err := json.Unmarshal(encoded, &shape); err != nil {
				t.Fatalf("json.Unmarshal(%s) error = %v", encoded, err)
			}
			value, ok := shape["additionalContextLimit"]
			if !tt.wantSet {
				if ok {
					t.Fatalf("additionalContextLimit present for unset handler: %s", encoded)
				}
				return
			}
			if !ok || value != tt.wantJSON {
				t.Fatalf("additionalContextLimit = %#v (present=%t), want %v: %s", value, ok, tt.wantJSON, encoded)
			}
		})
	}
}

func uint64Ptr(value uint64) *uint64 { return &value }
