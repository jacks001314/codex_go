package appserver

import (
	"strings"
	"testing"

	"codex_go/model"
	"codex_go/plugin"
)

func stringPtrForSelectedPluginTest(value string) *string { return &value }

// TestSelectedPluginMessagesOverridesLikeRust covers the template half of Rust
// #58894: valid templates replace the bundled wording, while a missing or
// duplicated placeholder, extra braces, control characters or an oversized
// value keeps the bundled template for that field only.
func TestSelectedPluginMessagesOverridesLikeRust(t *testing.T) {
	defaults := plugin.DefaultSelectedPluginMessages()
	modelInfo := &model.ModelInfo{ModelMessages: &model.ModelMessages{Plugins: &model.PluginMessages{
		Selected: &model.SelectedPluginMessages{
			Intro:          stringPtrForSelectedPluginTest("Catalog `{{ plugin_name }}` capabilities:"),
			SkillNamespace: stringPtrForSelectedPluginTest("Skill prefix `{{ skill_namespace }}`."),
			Apps:           stringPtrForSelectedPluginTest("Apps: {{ apps }}"),
			MCPServers:     stringPtrForSelectedPluginTest("Servers: {{ mcp_servers }}"),
			SearchTools:    stringPtrForSelectedPluginTest("Search first."),
			Closing:        stringPtrForSelectedPluginTest("Finish with them."),
			Omitted:        stringPtrForSelectedPluginTest(" (omitted)"),
		},
	}}}
	messages := selectedPluginMessagesForModel(modelInfo)
	if messages.Intro != "Catalog `{{ plugin_name }}` capabilities:" ||
		messages.SkillNamespace != "Skill prefix `{{ skill_namespace }}`." ||
		messages.Apps != "Apps: {{ apps }}" || messages.MCPServers != "Servers: {{ mcp_servers }}" ||
		messages.SearchTools != "Search first." || messages.Closing != "Finish with them." ||
		messages.Omitted != " (omitted)" {
		t.Fatalf("selected messages = %#v", messages)
	}

	broken := &model.ModelInfo{ModelMessages: &model.ModelMessages{Plugins: &model.PluginMessages{
		Selected: &model.SelectedPluginMessages{
			Intro:          stringPtrForSelectedPluginTest("No placeholder here"),
			SkillNamespace: stringPtrForSelectedPluginTest("{{ skill_namespace }} {{ skill_namespace }}"),
			Apps:           stringPtrForSelectedPluginTest("Apps {bad}: {{ apps }}"),
			MCPServers:     nil,
			SearchTools:    stringPtrForSelectedPluginTest("line\x00break"),
			Closing:        stringPtrForSelectedPluginTest(strings.Repeat("x", maxPluginWordingBytes+1)),
		},
	}}}
	messages = selectedPluginMessagesForModel(broken)
	if messages.Intro != defaults.Intro || messages.SkillNamespace != defaults.SkillNamespace ||
		messages.Apps != defaults.Apps || messages.MCPServers != defaults.MCPServers ||
		messages.SearchTools != defaults.SearchTools || messages.Closing != defaults.Closing ||
		messages.Omitted != defaults.Omitted {
		t.Fatalf("invalid selected messages did not fall back: %#v", messages)
	}
}

// TestSelectedPluginInjectionUsesCatalogWording checks the resolved templates
// reach the rendered hint with harness-owned facts substituted.
func TestSelectedPluginInjectionUsesCatalogWording(t *testing.T) {
	messages := plugin.SelectedPluginMessages{
		Intro:          "Catalog `{{ plugin_name }}` capabilities:",
		SearchTools:    "Search plugin apps first.",
		SkillNamespace: "Skills use `{{ skill_namespace }}`.",
		Apps:           "Apps: {{ apps }}.",
		MCPServers:     "Servers: {{ mcp_servers }}.",
		Closing:        "Use them.",
		Omitted:        " (omitted)",
	}
	rendered := plugin.BuildPluginInjectionsWithMessages(
		[]plugin.CapabilitySummary{{DisplayName: "Docs", HasSkills: true}},
		[]plugin.ToolInfo{{ServerName: "docs-server", PluginDisplayNames: []string{"Docs"}}},
		[]plugin.AppInfo{{DisplayName: "Docs App", Enabled: true, PluginDisplayNames: []string{"Docs"}}},
		false,
		messages,
	)
	if len(rendered) != 1 {
		t.Fatalf("rendered = %#v", rendered)
	}
	for _, want := range []string{
		"Catalog `Docs` capabilities:",
		"Search plugin apps first.",
		"Skills use `Docs`.",
		"Apps: `Docs App`.",
		"Servers: `docs-server`.",
		"Use them.",
	} {
		if !strings.Contains(rendered[0], want) {
			t.Fatalf("rendered hint is missing %q:\n%s", want, rendered[0])
		}
	}
}
