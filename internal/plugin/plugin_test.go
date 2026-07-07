package plugin

import (
	"reflect"
	"strings"
	"testing"
)

func TestListDiscoverablePluginsFiltersAndSorts(t *testing.T) {
	available := []DiscoverableInfo{
		{ID: "b", RemotePluginID: "remote-b", Name: "Beta", AppConnectorIDs: []string{"app-b"}},
		{ID: "a", RemotePluginID: "remote-a", Name: "Alpha"},
		{ID: "c", RemotePluginID: "remote-c", Name: "Gamma"},
	}
	got := ListDiscoverablePlugins(available, &DiscoverableConfig{
		ConfiguredPluginIDs:   []string{"a", "b", "c"},
		DisabledPluginIDs:     []string{"c"},
		LoadedAppConnectorIDs: []string{"app-b"},
	})
	if ids(got) == nil {
		t.Fatalf("ids() unexpectedly nil")
	}
	if !reflect.DeepEqual(ids(got), []string{"a"}) {
		t.Fatalf("ListDiscoverablePlugins() = %v, want [a]", ids(got))
	}
}

func TestListDiscoverablePluginsTrimsFilterIDs(t *testing.T) {
	available := []DiscoverableInfo{
		{ID: "a", RemotePluginID: "remote-a", Name: "Alpha"},
		{ID: "b", RemotePluginID: "remote-b", Name: "Beta"},
		{ID: "c", RemotePluginID: "remote-c", Name: "Gamma", AppConnectorIDs: []string{" app-c "}},
	}
	got := ListDiscoverablePlugins(available, &DiscoverableConfig{
		ConfiguredPluginIDs:   []string{" remote-a ", " b ", " c "},
		DisabledPluginIDs:     []string{" b "},
		LoadedAppConnectorIDs: []string{" app-c "},
	})
	if !reflect.DeepEqual(ids(got), []string{"a"}) {
		t.Fatalf("ListDiscoverablePlugins() = %v, want [a]", ids(got))
	}
}

func TestCollectToolMentionsFromMessages(t *testing.T) {
	mentions := CollectToolMentionsFromMessages([]string{
		"run $shell and $app://calendar but ignore @plugin://docs",
	})
	if !mentions.PlainNames["shell"] {
		t.Fatalf("plain mention shell missing")
	}
	if !mentions.Paths["app://calendar"] {
		t.Fatalf("path mention app://calendar missing")
	}
	if mentions.Paths["plugin://docs"] {
		t.Fatalf("plugin mention collected with tool sigil")
	}
}

func TestCollectExplicitAppIDs(t *testing.T) {
	got := CollectExplicitAppIDs([]UserInput{
		{Type: "text", Text: "open $app://calendar"},
		{Type: "mention", Path: "app://mail"},
		{Type: "text", Text: "then $app://team%20drive"},
		{Type: "mention", Path: "plugin://docs"},
	})
	if !reflect.DeepEqual(got, map[string]bool{"calendar": true, "mail": true, "team drive": true}) {
		t.Fatalf("CollectExplicitAppIDs() = %v", got)
	}
}

func TestCollectExplicitPluginMentions(t *testing.T) {
	plugins := []CapabilitySummary{
		{ConfigName: "docs", DisplayName: "Docs"},
		{ConfigName: "issues", DisplayName: "Issues"},
		{ConfigName: "sample@debug", DisplayName: "Sample"},
	}
	got := CollectExplicitPluginMentions([]UserInput{
		{Type: "text", Text: "use @plugin://docs"},
		{Type: "mention", Path: "plugin://issues"},
		{Type: "text", Text: "and @plugin://sample%40debug"},
	}, plugins)
	if names(got) == nil {
		t.Fatalf("names() unexpectedly nil")
	}
	if !reflect.DeepEqual(names(got), []string{"Docs", "Issues", "Sample"}) {
		t.Fatalf("CollectExplicitPluginMentions() = %v, want Docs Issues Sample", names(got))
	}
}

func TestCollectExplicitPluginMentionsFromAppMention(t *testing.T) {
	plugins := []CapabilitySummary{
		{ConfigName: "docs", DisplayName: "Docs", AppConnectors: []string{"docs-app"}},
		{ConfigName: "issues", DisplayName: "Issues", AppConnectors: []string{"issues-app"}},
	}
	got := CollectExplicitPluginMentions([]UserInput{
		{Type: "mention", Path: "app://docs-app"},
	}, plugins)
	if !reflect.DeepEqual(names(got), []string{"Docs"}) {
		t.Fatalf("CollectExplicitPluginMentions(app) = %v, want Docs", names(got))
	}
}

func TestRenderExplicitPluginInstructions(t *testing.T) {
	text, ok := RenderExplicitPluginInstructions(&CapabilitySummary{
		DisplayName: "Docs",
		HasSkills:   true,
	}, []string{"docs-mcp"}, []string{"Docs App"})
	if !ok {
		t.Fatalf("RenderExplicitPluginInstructions() ok = false, want true")
	}
	for _, want := range []string{
		"Capabilities from the `Docs` plugin:",
		"prefixed with `Docs:`",
		"`docs-mcp`",
		"`Docs App`",
		"Use these plugin-associated capabilities",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("RenderExplicitPluginInstructions() missing %q in:\n%s", want, text)
		}
	}
}

func TestRenderExplicitPluginInstructionsReturnsFalseForNoCapabilities(t *testing.T) {
	if text, ok := RenderExplicitPluginInstructions(&CapabilitySummary{DisplayName: "Docs"}, nil, nil); ok || text != "" {
		t.Fatalf("RenderExplicitPluginInstructions(no capabilities) = %q/%v, want empty/false", text, ok)
	}
}

func TestBuildPluginInjections(t *testing.T) {
	got := BuildPluginInjections(
		[]CapabilitySummary{{DisplayName: "Docs", HasSkills: true}},
		[]ToolInfo{
			{ServerName: "docs-mcp", PluginDisplayNames: []string{"Docs"}},
			{ServerName: AppsMCPServerName, PluginDisplayNames: []string{"Docs"}},
			{ServerName: "other", PluginDisplayNames: []string{"Other"}},
		},
		[]AppInfo{
			{ID: "docs", DisplayName: "Docs App", Enabled: true, PluginDisplayNames: []string{"Docs"}},
			{ID: "disabled", DisplayName: "Disabled", Enabled: false, PluginDisplayNames: []string{"Docs"}},
		},
	)
	if len(got) != 1 {
		t.Fatalf("BuildPluginInjections() len = %d, want 1", len(got))
	}
	if !strings.Contains(got[0], "`docs-mcp`") || !strings.Contains(got[0], "`Docs App`") {
		t.Fatalf("BuildPluginInjections() = %q", got[0])
	}
	if strings.Contains(got[0], AppsMCPServerName) || strings.Contains(got[0], "Disabled") {
		t.Fatalf("BuildPluginInjections() included filtered capability: %q", got[0])
	}
}

func ids(plugins []DiscoverableInfo) []string {
	out := make([]string, len(plugins))
	for i := range plugins {
		out[i] = plugins[i].ID
	}
	return out
}

func names(plugins []CapabilitySummary) []string {
	out := make([]string, len(plugins))
	for i := range plugins {
		out[i] = plugins[i].DisplayName
	}
	return out
}
