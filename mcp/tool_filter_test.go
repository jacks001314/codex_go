package mcp

import "testing"

func TestToolFilterNilAllowsAll(t *testing.T) {
	var filter *ToolFilter
	if !filter.Allows("any_tool") {
		t.Fatal("nil filter should allow all tools")
	}
	tools := FilterTools([]RuntimeToolInfo{
		{Tool: RuntimeTool{Name: "a"}},
		{Tool: RuntimeTool{Name: "b"}},
	}, nil)
	if len(tools) != 2 {
		t.Fatalf("nil filter should not filter: got %d tools", len(tools))
	}
}

func TestToolFilterEmptyAllowsAll(t *testing.T) {
	filter := &ToolFilter{}
	if !filter.Allows("any_tool") {
		t.Fatal("empty filter should allow all tools")
	}
}

func TestToolFilterEnabledAllowlist(t *testing.T) {
	filter := NewToolFilter([]string{"read", "write"}, nil)
	if !filter.Allows("read") {
		t.Fatal("read should be allowed")
	}
	if !filter.Allows("write") {
		t.Fatal("write should be allowed")
	}
	if filter.Allows("delete") {
		t.Fatal("delete should not be allowed when allowlist is set")
	}
}

func TestToolFilterDisabledDenylist(t *testing.T) {
	filter := NewToolFilter(nil, []string{"dangerous", "experimental"})
	if !filter.Allows("read") {
		t.Fatal("read should be allowed with only denylist")
	}
	if filter.Allows("dangerous") {
		t.Fatal("dangerous should be denied by denylist")
	}
}

func TestToolFilterEnabledTakesPrecedenceOverDisabled(t *testing.T) {
	filter := NewToolFilter([]string{"safe_read"}, []string{"safe_read"})
	if filter.Allows("safe_read") {
		t.Fatal("disabled should override enabled: safe_read should be denied")
	}
}

func TestToolFilterEnabledImpliesDeny(t *testing.T) {
	filter := NewToolFilter([]string{"only_this"}, nil)
	if filter.Allows("only_this") {
	} else {
		t.Fatal("only_this should be allowed")
	}
	if filter.Allows("any_other") {
		t.Fatal("any_other should be denied when allowlist is set")
	}
}

func TestToolFilterFromServerConfigNilOrEmpty(t *testing.T) {
	if ToolFilterFromServerConfig(nil) != nil {
		t.Fatal("nil config should yield nil filter")
	}
	if ToolFilterFromServerConfig(&ServerConfig{}) != nil {
		t.Fatal("empty config should yield nil filter")
	}
}

func TestToolFilterFromServerConfigBuildsFilter(t *testing.T) {
	config := &ServerConfig{
		EnabledTools:  []string{"a", "b", ""},
		DisabledTools: []string{"c"},
	}
	filter := ToolFilterFromServerConfig(config)
	if filter == nil {
		t.Fatal("config with enabled/disabled should yield filter")
	}
	if !filter.Allows("a") || !filter.Allows("b") {
		t.Fatal("enabled tools should be allowed")
	}
	if filter.Allows("c") {
		t.Fatal("disabled tool should be denied")
	}
	if filter.Allows("unknown") {
		t.Fatal("unknown tool should be denied when allowlist is set")
	}
}

func TestFilterToolsAppliesFilter(t *testing.T) {
	tools := []RuntimeToolInfo{
		{Tool: RuntimeTool{Name: "read"}},
		{Tool: RuntimeTool{Name: "write"}},
		{Tool: RuntimeTool{Name: "dangerous"}},
	}
	filter := NewToolFilter(nil, []string{"dangerous"})
	filtered := FilterTools(tools, filter)
	if len(filtered) != 2 {
		t.Fatalf("expected 2 tools after filtering, got %d", len(filtered))
	}
	if filtered[0].Tool.Name != "read" || filtered[1].Tool.Name != "write" {
		t.Fatalf("wrong tools kept: %#v", filtered)
	}
}

func TestFilterMCPToolsAppliesFilter(t *testing.T) {
	tools := []MCPToolInfo{
		{Name: "read"},
		{Name: "write"},
		{Name: "secret_admin"},
	}
	filter := NewToolFilter([]string{"read", "write"}, nil)
	filtered := FilterMCPTools(tools, filter)
	if len(filtered) != 2 {
		t.Fatalf("expected 2 tools after allowlist filtering, got %d", len(filtered))
	}
	if filtered[0].Name != "read" || filtered[1].Name != "write" {
		t.Fatalf("wrong tools kept: %#v", filtered)
	}
}

func TestToolFilterTrimsWhitespace(t *testing.T) {
	filter := NewToolFilter([]string{"  read  ", "write"}, []string{"\texperimental\n"})
	if !filter.Allows("read") {
		t.Fatal("read should be allowed after trimming")
	}
	if filter.Allows("experimental") {
		t.Fatal("experimental should be denied after trimming")
	}
}

func TestToolFilterAllowsMatchesRustSemantics(t *testing.T) {
	// Rust behavior: If enabled is Some and tool is not in it, deny.
	// If disabled contains the tool, deny. Otherwise allow.
	filter := NewToolFilter([]string{"explicit"}, []string{"denied"})
	if filter.Allows("explicit") {
	} else {
		t.Fatal("explicit should be allowed (in enabled set)")
	}
	if filter.Allows("denied") {
		t.Fatal("denied should not be allowed (in disabled set)")
	}
	if filter.Allows("implicit") {
		t.Fatal("implicit should not be allowed (not in enabled set)")
	}
}
