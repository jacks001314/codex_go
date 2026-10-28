package rollout

import (
	"encoding/json"
	"strings"
	"testing"

	"codex_go/session"
)

func dynamicToolRaw(name string) json.RawMessage {
	return json.RawMessage(`{"type":"function","name":"` + name + `","description":"Check ` + name + `.","inputSchema":{"type":"object","properties":{}}}`)
}

func TestAppendDynamicToolsUpdatedWritesRustWireLikeRust(t *testing.T) {
	now := fixedTime()
	recorder, err := NewRecorder(&CreateParams{
		CodexHome:     t.TempDir(),
		ThreadID:      "thread-dynamic-tools",
		SessionID:     "thread-dynamic-tools",
		Source:        "cli",
		ModelProvider: "openai",
		Now:           now,
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	path := recorder.Path()
	tools := []json.RawMessage{dynamicToolRaw("release")}
	if err := recorder.AppendDynamicToolsUpdated("thread-dynamic-tools", tools, now); err != nil {
		t.Fatalf("AppendDynamicToolsUpdated() error = %v", err)
	}
	if err := recorder.AppendDynamicToolsUpdated("thread-dynamic-tools", nil, now); err != nil {
		t.Fatalf("AppendDynamicToolsUpdated(clear) error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	data, err := json.Marshal(Line{Type: "dynamic_tools_updated", Payload: mustRawJSONForDynamicToolsTest(t, DynamicToolsSnapshot{
		ThreadID: "thread-dynamic-tools",
		Tools:    tools,
	})})
	if err != nil {
		t.Fatalf("marshal dynamic tools line: %v", err)
	}
	if !strings.Contains(string(data), `"type":"dynamic_tools_updated"`) {
		t.Fatalf("dynamic tools line = %s", data)
	}
	lines, _, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	var updates []*DynamicToolsSnapshot
	for i := range lines {
		if lines[i].Type != "dynamic_tools_updated" {
			continue
		}
		var snapshot DynamicToolsSnapshot
		if err := json.Unmarshal(lines[i].Payload, &snapshot); err != nil {
			t.Fatalf("decode update %d: %v", i, err)
		}
		updates = append(updates, &snapshot)
	}
	if len(updates) != 2 {
		t.Fatalf("updates = %d, want 2", len(updates))
	}
	if updates[0].ThreadID != "thread-dynamic-tools" || len(updates[0].Tools) != 1 {
		t.Fatalf("first update = %+v", updates[0])
	}
	// An explicit clear is persisted as an empty catalog, never null.
	if updates[1].Tools == nil || len(updates[1].Tools) != 0 {
		t.Fatalf("clear update tools = %#v", updates[1].Tools)
	}
}

func TestLatestDynamicToolsPrefersNewestUpdateLikeRust(t *testing.T) {
	lines := []Line{
		{Meta: &SessionMeta{ID: "thread-1", DynamicTools: []json.RawMessage{dynamicToolRaw("initial")}}},
		{Type: "dynamic_tools_updated", Payload: mustRawJSONForDynamicToolsTest(t, DynamicToolsSnapshot{ThreadID: "thread-1", Tools: []json.RawMessage{dynamicToolRaw("release")}})},
		// A snapshot belonging to an ancestor/forked thread is ignored.
		{Type: "dynamic_tools_updated", Payload: mustRawJSONForDynamicToolsTest(t, DynamicToolsSnapshot{ThreadID: "thread-other", Tools: []json.RawMessage{dynamicToolRaw("other")}})},
	}
	tools, ok := LatestDynamicTools(lines)
	if !ok || len(tools) != 1 || !strings.Contains(string(tools[0]), `"release"`) {
		t.Fatalf("LatestDynamicTools() = %s, %t", tools, ok)
	}

	lines = append(lines, Line{Type: "dynamic_tools_updated", Payload: mustRawJSONForDynamicToolsTest(t, DynamicToolsSnapshot{ThreadID: "thread-1", Tools: []json.RawMessage{}})})
	tools, ok = LatestDynamicTools(lines)
	if !ok || tools == nil || len(tools) != 0 {
		t.Fatalf("LatestDynamicTools() after clear = %#v, %t", tools, ok)
	}
}

func TestLatestDynamicToolsReadsCompactionSnapshotLikeRust(t *testing.T) {
	windowNumber := uint64(1)
	lines := []Line{
		{Meta: &SessionMeta{ID: "thread-1", DynamicTools: []json.RawMessage{dynamicToolRaw("initial")}}},
	}
	recorder, err := NewRecorder(&CreateParams{
		CodexHome: t.TempDir(),
		ThreadID:  "thread-1",
		SessionID: "thread-1",
		Now:       fixedTime(),
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	resume := &CompactionResumeMetadata{DynamicTools: &DynamicToolsSnapshot{ThreadID: "thread-1", Tools: []json.RawMessage{dynamicToolRaw("deployment")}}}
	contextItem := ItemFromSessionItem(&session.Item{ID: "context", Type: "message", Role: "user", Text: "compacted"})
	if err := recorder.AppendCompactedWithResumeMetadata("summary", []Item{*contextItem}, nil, resume, &windowNumber, fixedTime()); err != nil {
		t.Fatalf("AppendCompactedWithResumeMetadata() error = %v", err)
	}
	path := recorder.Path()
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	compactedLines, _, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	lines = append(lines, compactedLines...)
	tools, ok := LatestDynamicTools(lines)
	if !ok || len(tools) != 1 || !strings.Contains(string(tools[0]), `"deployment"`) {
		t.Fatalf("LatestDynamicTools() = %s, %t", tools, ok)
	}
}

func TestRecordFromPathRestoresLatestDynamicToolsLikeRust(t *testing.T) {
	now := fixedTime()
	recorder, err := NewRecorder(&CreateParams{
		CodexHome:     t.TempDir(),
		ThreadID:      "thread-restore-tools",
		SessionID:     "thread-restore-tools",
		Source:        "cli",
		ModelProvider: "openai",
		DynamicTools:  []json.RawMessage{dynamicToolRaw("initial")},
		Now:           now,
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	path := recorder.Path()
	if err := recorder.AppendDynamicToolsUpdated("thread-restore-tools", []json.RawMessage{dynamicToolRaw("deployment")}, now); err != nil {
		t.Fatalf("AppendDynamicToolsUpdated() error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	record, err := RecordFromPath(path, false)
	if err != nil {
		t.Fatalf("RecordFromPath() error = %v", err)
	}
	if len(record.Metadata.DynamicTools) != 1 || !strings.Contains(string(record.Metadata.DynamicTools[0]), `"deployment"`) {
		t.Fatalf("restored dynamic tools = %s", record.Metadata.DynamicTools)
	}
}

func mustRawJSONForDynamicToolsTest(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}
