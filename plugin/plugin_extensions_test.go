package plugin

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestParsePluginExtensionsNormalizesHostedDeclarationsLikeRust(t *testing.T) {
	raw := json.RawMessage(`{
		"entrypoints": [{
			"type": "global",
			"app_id": "parts-app",
			"tool_name": "parts.open",
			"title": "Parts",
			"resource_uri": "ui://parts/library",
			"icons": [{"src": "https://example.com/parts.svg", "mimeType": "image/svg+xml", "sizes": ["any"], "theme": "light"}],
			"quick_action": {"title": "Add part", "icons": [{"src": "https://example.com/add.svg"}], "target": {"type": "tool", "name": "parts.add", "arguments": {}}}
		}],
		"settings_entrypoints": [{
			"type": "settings",
			"app_id": "parts-app",
			"tool_name": "parts.settings",
			"title": "Parts settings",
			"resource_uri": "ui://parts/settings",
			"icons": [],
			"search_terms": ["part"]
		}],
		"settings": [{"app_id": "parts-app", "read_tool_name": "settings.read", "update_tool_name": "settings.update"}],
		"thread_entrypoints": [{
			"type": "thread",
			"app_id": "parts-app",
			"tool_name": "parts.thread",
			"title": "Parts thread",
			"resource_uri": "ui://parts/thread",
			"icons": []
		}],
		"file_handlers": [{
			"type": "file",
			"app_id": "parts-app",
			"tool_name": "parts.file",
			"title": "Parts files",
			"resource_uri": "ui://parts/files",
			"icons": [],
			"extensions": ["parts"]
		}],
		"search_mention_providers": [{
			"app_id": "parts-app",
			"tool_name": "parts.search",
			"link_id": "parts",
			"title": "Parts search",
			"call": {"name": "parts.search", "arguments": {"query": "bolt"}, "_meta": {"source": "parts"}}
		}]
	}`)
	extensions := ParsePluginExtensions(raw)
	if extensions == nil {
		t.Fatal("valid hosted plugin extensions were rejected")
	}
	encoded, err := json.Marshal(extensions)
	if err != nil {
		t.Fatalf("marshal extensions: %v", err)
	}
	var got any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("decode normalized extensions: %v", err)
	}
	want := map[string]any{
		"entrypoints": []any{map[string]any{
			"type":        "global",
			"appId":       "parts-app",
			"toolName":    "parts.open",
			"title":       "Parts",
			"resourceUri": "ui://parts/library",
			"icons": []any{map[string]any{
				"src":      "https://example.com/parts.svg",
				"mimeType": "image/svg+xml",
				"sizes":    []any{"any"},
				"theme":    "light",
			}},
			"quickAction": map[string]any{
				"title": "Add part",
				"icons": []any{map[string]any{
					"src":      "https://example.com/add.svg",
					"mimeType": nil,
					"sizes":    nil,
					"theme":    nil,
				}},
				"target": map[string]any{
					"type":      "tool",
					"name":      "parts.add",
					"arguments": map[string]any{},
				},
			},
		}},
		"settingsEntrypoints": []any{map[string]any{
			"type":        "settings",
			"appId":       "parts-app",
			"toolName":    "parts.settings",
			"title":       "Parts settings",
			"resourceUri": "ui://parts/settings",
			"icons":       []any{},
			"searchTerms": []any{"part"},
		}},
		"settings": []any{map[string]any{
			"appId":          "parts-app",
			"readToolName":   "settings.read",
			"updateToolName": "settings.update",
		}},
		"threadEntrypoints": []any{map[string]any{
			"type":        "thread",
			"appId":       "parts-app",
			"toolName":    "parts.thread",
			"title":       "Parts thread",
			"resourceUri": "ui://parts/thread",
			"icons":       []any{},
		}},
		"fileHandlers": []any{map[string]any{
			"type":        "file",
			"appId":       "parts-app",
			"toolName":    "parts.file",
			"title":       "Parts files",
			"resourceUri": "ui://parts/files",
			"icons":       []any{},
			"extensions":  []any{"parts"},
		}},
		"searchMentionProviders": []any{map[string]any{
			"appId":    "parts-app",
			"toolName": "parts.search",
			"linkId":   "parts",
			"title":    "Parts search",
			"call": map[string]any{
				"name":      "parts.search",
				"arguments": map[string]any{"query": "bolt"},
				"_meta":     map[string]any{"source": "parts"},
			},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalized extensions = %#v, want %#v", got, want)
	}
}

func TestParsePluginExtensionsDropsIncompatibleDeclarationsLikeRust(t *testing.T) {
	valid := `{
		"entrypoints": [{
			"type": "global",
			"appId": "parts-app",
			"toolName": "parts.open",
			"title": "Parts",
			"resourceUri": "ui://parts/library",
			"icons": [],
			"quickAction": {"title": "Add part", "icons": [], "target": {"type": "tool", "name": "parts.add"}}
		}]
	}`
	if extensions := ParsePluginExtensions(json.RawMessage(valid)); extensions == nil {
		t.Fatal("valid fixture was rejected")
	}
	for name, body := range map[string]string{
		"unknown entrypoint": `{
			"entrypoints": [{
				"type": "futurePlacement",
				"appId": "parts-app",
				"toolName": "parts.open",
				"title": "Parts",
				"resourceUri": "ui://parts/library",
				"icons": []
			}]
		}`,
		"unknown quick action": `{
			"entrypoints": [{
				"type": "global",
				"appId": "parts-app",
				"toolName": "parts.open",
				"title": "Parts",
				"resourceUri": "ui://parts/library",
				"icons": [],
				"quickAction": {"title": "Future", "icons": [], "target": {"type": "futureAction", "name": "parts.future"}}
			}]
		}`,
		"missing presentation": `{"entrypoints": [{"type": "thread", "appId": "parts-app"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if extensions := ParsePluginExtensions(json.RawMessage(body)); extensions != nil {
				t.Fatalf("incompatible extensions = %#v, want nil", extensions)
			}
		})
	}
}

func TestPluginSummaryMarshalsExtensionsLikeRust(t *testing.T) {
	extensions := ParsePluginExtensions(json.RawMessage(`{
		"entrypoints": [{
			"type": "thread",
			"appId": "parts-app",
			"toolName": "parts.thread",
			"title": "Parts",
			"resourceUri": "ui://parts/thread",
			"icons": []
		}]
	}`))
	if extensions == nil {
		t.Fatal("valid fixture was rejected")
	}
	encoded, err := json.Marshal(PluginSummary{ID: "parts@remote", Name: "parts", Extensions: extensions})
	if err != nil {
		t.Fatalf("marshal summary: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	value, ok := payload["extensions"].(map[string]any)
	if !ok || len(value["threadEntrypoints"].([]any)) != 0 || len(value["entrypoints"].([]any)) != 1 {
		t.Fatalf("summary extensions = %#v", payload["extensions"])
	}
	empty, err := json.Marshal(PluginSummary{ID: "local@market", Name: "local"})
	if err != nil {
		t.Fatalf("marshal summary without extensions: %v", err)
	}
	if err := json.Unmarshal(empty, &payload); err != nil {
		t.Fatalf("decode summary without extensions: %v", err)
	}
	if value, ok := payload["extensions"]; !ok || value != nil {
		t.Fatalf("absent extensions = %#v, want null", value)
	}
}
