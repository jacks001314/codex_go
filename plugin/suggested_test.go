package plugin

import (
	"strings"
	"testing"
)

func TestDecodeSuggestedPluginResponseCompactShapeLikeRust(t *testing.T) {
	// Rust #39143: the Codex-specific /ps/plugins/suggested/codex endpoint
	// returns a compact response without per-plugin status/policy/release.
	data := []byte(`{
		"enabled": true,
		"plugins": [
			{"id": "remote-1", "name": "docs", "display_name": "Docs Connector"},
			{"id": "remote-2", "name": "calendar", "display_name": "Calendar"}
		]
	}`)
	list, err := decodeSuggestedPluginResponse(data)
	if err != nil {
		t.Fatalf("decode error = %v", err)
	}
	if !list.Enabled || len(list.Plugins) != 2 {
		t.Fatalf("list = %#v", list)
	}
	byName := map[string]SuggestedPlugin{}
	for _, plugin := range list.Plugins {
		byName[plugin.Name] = plugin
	}
	docs, ok := byName["docs"]
	if !ok || docs.RemotePluginID != "remote-1" || docs.DisplayName != "Docs Connector" {
		t.Fatalf("docs plugin = %#v ok=%v", docs, ok)
	}
	if !strings.HasPrefix(docs.ID, "docs@") {
		t.Fatalf("docs plugin id = %q", docs.ID)
	}
}

func TestDecodeSuggestedPluginResponseDisabledLikeRust(t *testing.T) {
	list, err := decodeSuggestedPluginResponse([]byte(`{"enabled": false, "plugins": []}`))
	if err != nil {
		t.Fatalf("decode error = %v", err)
	}
	if list.Enabled || len(list.Plugins) != 0 {
		t.Fatalf("disabled list = %#v", list)
	}
}

func TestSuggestedPluginsURLCodexRouteLikeRust(t *testing.T) {
	url := suggestedPluginsURL("https://chatgpt.com/backend-api")
	if !strings.Contains(url, "/ps/plugins/suggested/codex") || !strings.Contains(url, "scope=GLOBAL") {
		t.Fatalf("suggested plugins URL = %q", url)
	}
}
