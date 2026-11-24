package context

import "testing"

// Mirrors Rust #63605
// core/src/plugins/render_tests.rs
// ::render_plugins_section_keeps_plugin_usage_guidance_without_listing_plugins:
// generic plugin guidance renders even when a plugin is present, and the
// bundled text lists no plugin.
func TestAvailablePluginsInstructionsKeepsGuidanceWithoutListingPluginsLikeRust(t *testing.T) {
	rendered := Render(NewAvailablePluginsInstructions([]PluginSummary{{ConfigName: "sample@test", DisplayName: "sample", HasSkills: true}}))
	if rendered == nil {
		t.Fatal("Render(AvailablePluginsInstructions) = nil")
	}
	expected := "<plugins_instructions>\n" +
		"## Plugins\n" +
		"A plugin is a local bundle of skills, MCP servers, and apps.\n" +
		"### How to use plugins\n" +
		"- Skill naming: If a plugin contributes skills, those skill entries are prefixed with `plugin_name:` in the Skills list.\n" +
		"- MCP naming: Plugin-provided MCP tools keep standard MCP identifiers such as `mcp__server__tool`; use tool provenance to tell which plugin they come from.\n" +
		"- Trigger rules: If the user explicitly names a plugin, prefer capabilities associated with that plugin for that turn.\n" +
		"- Relationship to capabilities: Plugins are not invoked directly. Use their underlying skills, MCP tools, and app tools to help solve the task.\n" +
		"- Relevance: Determine what a plugin can help with from explicit user mention or from the plugin-associated skills, MCP tools, and apps exposed elsewhere in this turn.\n" +
		"- Missing/blocked: If the user requests a plugin that does not have relevant callable capabilities for the task, say so briefly and continue with the best fallback.\n" +
		"</plugins_instructions>"
	if rendered.Content != expected {
		t.Fatalf("rendered plugin guidance =\n%s\nwant\n%s", rendered.Content, expected)
	}
}
