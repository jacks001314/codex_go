package appserver

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/plugin"
	"codex_go/session"
	"codex_go/telemetry"
)

func TestAttributeCommandExecutionItemUsesVerifiedRemotePlugin(t *testing.T) {
	home := t.TempDir()
	id, err := plugin.ParsePluginId("sample@openai-curated-remote")
	if err != nil {
		t.Fatal(err)
	}

	store, err := plugin.NewPluginStore(home)
	if err != nil {
		t.Fatal(err)
	}
	root := store.PluginRoot(id, "1.2.3")
	script := filepath.Join(root, "scripts", "run.py")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("print('ok')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteRemotePluginID(id, "plugins~Plugin_sample"); err != nil {
		t.Fatal(err)
	}
	plugins := plugin.NewPluginService()
	plugins.SetRuntimeRoute("chatgpt", "openai")
	plugins.AddPlugin(plugin.PluginDetail{Summary: plugin.PluginSummary{
		ID:        id.Key(),
		Name:      id.PluginName,
		Installed: true,
		Enabled:   true,
	}})
	router := NewRuntimeRouter(RuntimeServices{
		Config:  config.NewConfigService(home),
		Plugins: plugins,
	})
	item := ThreadItem{
		ID:   "cmd-1",
		Type: "commandExecution",
		Data: map[string]any{
			"command": "python " + filepath.ToSlash(script),
			"cwd":     root,
			"status":  string(CommandExecutionInProgress),
		},
	}

	router.attributeCommandExecutionItem(&item)

	if item.Data["pluginId"] != id.Key() || item.Data["scriptPath"] != "scripts/run.py" {
		t.Fatalf("attributed item data = %#v", item.Data)
	}
}

func TestAttributeCommandExecutionItemRejectsUnverifiedCache(t *testing.T) {
	home := t.TempDir()
	id, _ := plugin.ParsePluginId("sample@openai-curated-remote")
	store, _ := plugin.NewPluginStore(home)
	root := store.PluginRoot(id, "1.2.3")
	script := filepath.Join(root, "scripts", "run.py")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	plugins := plugin.NewPluginService()
	plugins.SetRuntimeRoute("chatgpt", "openai")
	plugins.AddPlugin(plugin.PluginDetail{Summary: plugin.PluginSummary{
		ID:        id.Key(),
		Name:      id.PluginName,
		Installed: true,
		Enabled:   true,
	}})
	router := NewRuntimeRouter(RuntimeServices{
		Config:  config.NewConfigService(home),
		Plugins: plugins,
	})
	item := ThreadItem{
		ID:   "cmd-1",
		Type: "commandExecution",
		Data: map[string]any{"command": "python " + script, "cwd": root},
	}

	router.attributeCommandExecutionItem(&item)

	if item.Data["pluginId"] != nil || item.Data["scriptPath"] != nil {
		t.Fatalf("unverified cache was attributed: %#v", item.Data)
	}
}

func TestAttributeSessionCommandItemsPreservesHistoryFields(t *testing.T) {
	home := t.TempDir()
	id, _ := plugin.ParsePluginId("sample@openai-curated-remote")
	store, _ := plugin.NewPluginStore(home)
	root := store.PluginRoot(id, "1.2.3")
	script := filepath.Join(root, "scripts", "run.py")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteRemotePluginID(id, "plugins~Plugin_sample"); err != nil {
		t.Fatal(err)
	}
	plugins := plugin.NewPluginService()
	plugins.SetRuntimeRoute("chatgpt", "openai")
	plugins.AddPlugin(plugin.PluginDetail{Summary: plugin.PluginSummary{
		ID: id.Key(), Name: id.PluginName, Installed: true, Enabled: true,
	}})
	router := NewRuntimeRouter(RuntimeServices{
		Config: config.NewConfigService(home), Plugins: plugins,
	})
	items := []session.Item{{
		ID:   "cmd-1",
		Type: "commandExecution",
		Data: map[string]any{
			"command": "python " + filepath.ToSlash(script),
			"cwd":     root,
			"status":  string(CommandExecutionDeclined),
		},
	}}

	router.attributeSessionCommandItems("", "", items)

	if items[0].Data["pluginId"] != id.Key() || items[0].Data["scriptPath"] != "scripts/run.py" {
		t.Fatalf("persisted item data = %#v", items[0].Data)
	}
}

func TestEmitArtifactOperationForCommandItemLikeRust(t *testing.T) {
	analytics := newRecordingTurnEventSink()
	router := &RuntimeRouter{services: RuntimeServices{Analytics: analytics}}
	item := &ThreadItem{
		ID: "item-artifact",
		Data: map[string]any{
			"pluginId":    "presentations@openai-primary-runtime",
			"scriptPath":  "skills/presentations/container_tools/mark_artifact_operation_started.mjs",
			"command":     "python /plugins/presentations/skills/presentations/container_tools/mark_artifact_operation_started.mjs --operation-kind create --expected-output-count 2 --output-format pptx",
			"startedAtMs": int64(1786000000000),
		},
	}
	router.emitArtifactOperationForCommandItem("thread-art", "turn-art", item)
	select {
	case event := <-analytics.artifactOperation:
		if event.EventType != telemetry.ArtifactOperationEventType {
			t.Fatalf("event type = %q", event.EventType)
		}
		params := event.EventParams
		if params.ThreadID != "thread-art" || params.TurnID != "turn-art" || params.ItemID != "item-artifact" {
			t.Fatalf("context = %+v", params)
		}
		if params.Lifecycle != telemetry.ArtifactOperationLifecycleStarted || params.Skill != "presentations" ||
			params.ArtifactType != "presentation" || params.OperationKind != "create" ||
			params.ExpectedOutputCount != 2 || params.OutputFormat != "pptx" || params.ExecutionBackend != "unified_exec" {
			t.Fatalf("artifact operation params = %+v", params)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for artifact operation analytics")
	}
}
