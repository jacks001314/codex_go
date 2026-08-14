package mcp

import (
	"reflect"
	"testing"

	"codex_go/apps"
)

func TestRuntimeConfigAppToolApproval(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]any
		keys   []string
		want   *apps.AppToolApproval
	}{
		{
			name:   "nil values",
			values: nil,
			keys:   []string{"approval_mode"},
			want:   nil,
		},
		{
			name:   "missing key",
			values: map[string]any{"other": "value"},
			keys:   []string{"approval_mode"},
			want:   nil,
		},
		{
			name:   "auto",
			values: map[string]any{"approval_mode": "auto"},
			keys:   []string{"approval_mode"},
			want:   ptrAppToolApproval(apps.AppToolApprovalAuto),
		},
		{
			name:   "prompt",
			values: map[string]any{"approval_mode": "prompt"},
			keys:   []string{"approval_mode"},
			want:   ptrAppToolApproval(apps.AppToolApprovalPrompt),
		},
		{
			name:   "approve",
			values: map[string]any{"approval_mode": "approve"},
			keys:   []string{"approval_mode"},
			want:   ptrAppToolApproval(apps.AppToolApprovalApprove),
		},
		{
			name:   "camelCase key",
			values: map[string]any{"approvalMode": "prompt"},
			keys:   []string{"approval_mode", "approvalMode"},
			want:   ptrAppToolApproval(apps.AppToolApprovalPrompt),
		},
		{
			name:   "invalid value",
			values: map[string]any{"approval_mode": "invalid"},
			keys:   []string{"approval_mode"},
			want:   nil,
		},
		{
			name:   "empty string",
			values: map[string]any{"approval_mode": ""},
			keys:   []string{"approval_mode"},
			want:   nil,
		},
		{
			name:   "non-string value",
			values: map[string]any{"approval_mode": 123},
			keys:   []string{"approval_mode"},
			want:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runtimeConfigAppToolApproval(tt.values, tt.keys...)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRuntimeConfigToolsMap(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]any
		key    string
		want   map[string]ToolConfig
	}{
		{
			name:   "nil values",
			values: nil,
			key:    "tools",
			want:   nil,
		},
		{
			name:   "missing key",
			values: map[string]any{"other": "value"},
			key:    "tools",
			want:   nil,
		},
		{
			name: "single tool with approval_mode",
			values: map[string]any{
				"tools": map[string]any{
					"read": map[string]any{
						"approval_mode": "approve",
					},
				},
			},
			key: "tools",
			want: map[string]ToolConfig{
				"read": {
					ApprovalMode: ptrAppToolApproval(apps.AppToolApprovalApprove),
				},
			},
		},
		{
			name: "multiple tools",
			values: map[string]any{
				"tools": map[string]any{
					"read": map[string]any{
						"approval_mode": "auto",
					},
					"write": map[string]any{
						"approval_mode": "prompt",
					},
				},
			},
			key: "tools",
			want: map[string]ToolConfig{
				"read": {
					ApprovalMode: ptrAppToolApproval(apps.AppToolApprovalAuto),
				},
				"write": {
					ApprovalMode: ptrAppToolApproval(apps.AppToolApprovalPrompt),
				},
			},
		},
		{
			name: "tool with no approval_mode",
			values: map[string]any{
				"tools": map[string]any{
					"read": map[string]any{},
				},
			},
			key: "tools",
			want: map[string]ToolConfig{
				"read": {
					ApprovalMode: nil,
				},
			},
		},
		{
			name: "empty tool name skipped",
			values: map[string]any{
				"tools": map[string]any{
					"  ": map[string]any{
						"approval_mode": "auto",
					},
					"read": map[string]any{
						"approval_mode": "prompt",
					},
				},
			},
			key: "tools",
			want: map[string]ToolConfig{
				"read": {
					ApprovalMode: ptrAppToolApproval(apps.AppToolApprovalPrompt),
				},
			},
		},
		{
			name: "non-map tool value skipped",
			values: map[string]any{
				"tools": map[string]any{
					"invalid": "not a map",
					"read": map[string]any{
						"approval_mode": "auto",
					},
				},
			},
			key: "tools",
			want: map[string]ToolConfig{
				"read": {
					ApprovalMode: ptrAppToolApproval(apps.AppToolApprovalAuto),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runtimeConfigToolsMap(tt.values, tt.key)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRuntimeServerConfigFromValuesWithApprovalMode(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]any
		check  func(*testing.T, *ServerConfig)
	}{
		{
			name: "default_tools_approval_mode",
			values: map[string]any{
				"command":                     "test",
				"default_tools_approval_mode": "prompt",
			},
			check: func(t *testing.T, config *ServerConfig) {
				if config.DefaultToolsApprovalMode == nil {
					t.Fatal("DefaultToolsApprovalMode is nil")
				}
				if *config.DefaultToolsApprovalMode != apps.AppToolApprovalPrompt {
					t.Errorf("DefaultToolsApprovalMode = %v, want %v", *config.DefaultToolsApprovalMode, apps.AppToolApprovalPrompt)
				}
			},
		},
		{
			name: "tools with approval_mode",
			values: map[string]any{
				"command": "test",
				"tools": map[string]any{
					"read": map[string]any{
						"approval_mode": "approve",
					},
				},
			},
			check: func(t *testing.T, config *ServerConfig) {
				if config.Tools == nil {
					t.Fatal("Tools is nil")
				}
				readTool, ok := config.Tools["read"]
				if !ok {
					t.Fatal("read tool not found")
				}
				if readTool.ApprovalMode == nil {
					t.Fatal("read tool ApprovalMode is nil")
				}
				if *readTool.ApprovalMode != apps.AppToolApprovalApprove {
					t.Errorf("read tool ApprovalMode = %v, want %v", *readTool.ApprovalMode, apps.AppToolApprovalApprove)
				}
			},
		},
		{
			name: "both default and per-tool approval modes",
			values: map[string]any{
				"command":                     "test",
				"default_tools_approval_mode": "auto",
				"tools": map[string]any{
					"read": map[string]any{
						"approval_mode": "prompt",
					},
					"write": map[string]any{
						"approval_mode": "approve",
					},
				},
			},
			check: func(t *testing.T, config *ServerConfig) {
				if config.DefaultToolsApprovalMode == nil {
					t.Fatal("DefaultToolsApprovalMode is nil")
				}
				if *config.DefaultToolsApprovalMode != apps.AppToolApprovalAuto {
					t.Errorf("DefaultToolsApprovalMode = %v, want %v", *config.DefaultToolsApprovalMode, apps.AppToolApprovalAuto)
				}
				if config.Tools == nil {
					t.Fatal("Tools is nil")
				}
				readTool, ok := config.Tools["read"]
				if !ok {
					t.Fatal("read tool not found")
				}
				if readTool.ApprovalMode == nil || *readTool.ApprovalMode != apps.AppToolApprovalPrompt {
					t.Errorf("read tool ApprovalMode = %v, want %v", readTool.ApprovalMode, apps.AppToolApprovalPrompt)
				}
				writeTool, ok := config.Tools["write"]
				if !ok {
					t.Fatal("write tool not found")
				}
				if writeTool.ApprovalMode == nil || *writeTool.ApprovalMode != apps.AppToolApprovalApprove {
					t.Errorf("write tool ApprovalMode = %v, want %v", writeTool.ApprovalMode, apps.AppToolApprovalApprove)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := runtimeServerConfigFromValues(tt.values)
			tt.check(t, config)
		})
	}
}

func TestCloneServerConfigWithApprovalMode(t *testing.T) {
	original := &ServerConfig{
		Command:                  "test",
		DefaultToolsApprovalMode: ptrAppToolApproval(apps.AppToolApprovalPrompt),
		Tools: map[string]ToolConfig{
			"read": {
				ApprovalMode: ptrAppToolApproval(apps.AppToolApprovalApprove),
			},
		},
	}

	cloned := cloneServerConfig(original)

	if cloned.DefaultToolsApprovalMode == nil {
		t.Fatal("cloned DefaultToolsApprovalMode is nil")
	}
	if *cloned.DefaultToolsApprovalMode != apps.AppToolApprovalPrompt {
		t.Errorf("cloned DefaultToolsApprovalMode = %v, want %v", *cloned.DefaultToolsApprovalMode, apps.AppToolApprovalPrompt)
	}

	if cloned.Tools == nil {
		t.Fatal("cloned Tools is nil")
	}
	readTool, ok := cloned.Tools["read"]
	if !ok {
		t.Fatal("read tool not found in cloned config")
	}
	if readTool.ApprovalMode == nil || *readTool.ApprovalMode != apps.AppToolApprovalApprove {
		t.Errorf("cloned read tool ApprovalMode = %v, want %v", readTool.ApprovalMode, apps.AppToolApprovalApprove)
	}

	// Verify deep clone - modifying cloned should not affect original
	cloned.Tools["write"] = ToolConfig{ApprovalMode: ptrAppToolApproval(apps.AppToolApprovalAuto)}
	if _, ok := original.Tools["write"]; ok {
		t.Error("modifying cloned Tools affected original")
	}
}

func ptrAppToolApproval(a apps.AppToolApproval) *apps.AppToolApproval {
	return &a
}
