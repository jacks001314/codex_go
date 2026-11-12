package historynotes

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestHistoryNotesToolSchemasMatchSnapshotLikeRust mirrors Rust #60753: every
// history/notes tool parameter schema is pinned so a change to the typed
// request surface cannot drift silently. #60770 derives these schemas from the
// typed requests; the snapshot is the contract the derivation must reproduce.
func TestHistoryNotesToolSchemasMatchSnapshotLikeRust(t *testing.T) {
	path := filepath.Join("testdata", "tool_schemas.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	var want map[string]any
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	got := map[string]any{}
	for _, action := range allActions {
		got[action.Namespace()+"."+action.Name()] = action.Parameters()
	}
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal schemas: %v", err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if string(gotJSON) != string(wantJSON) {
		pretty, _ := json.MarshalIndent(got, "", "  ")
		t.Fatalf("history/notes tool schemas drifted from %s:\n%s", path, pretty)
	}
	if len(got) != len(allActions) {
		t.Fatalf("snapshot covers %d tools, want %d", len(got), len(allActions))
	}
}

// TestHistoryNotesEncryptedFieldsStayMarkedLikeRust pins the encrypted query
// and text fields the backend relies on (Rust requests.rs encrypted_string).
func TestHistoryNotesEncryptedFieldsStayMarkedLikeRust(t *testing.T) {
	encrypted := map[string]string{
		"history.search_contents": "query",
		"notes.search_contents":   "query",
		"notes.append_to_file":    "text",
		"notes.write_file":        "text",
	}
	for toolName, field := range encrypted {
		namespace, name, _ := cutToolName(toolName)
		schema := schemaFor(namespace, name)
		properties, _ := schema["properties"].(map[string]any)
		entry, _ := properties[field].(map[string]any)
		if entry["encrypted"] != true {
			t.Fatalf("%s.%s is not marked encrypted: %#v", toolName, field, entry)
		}
	}
}

func cutToolName(value string) (string, string, bool) {
	for i := 0; i < len(value); i++ {
		if value[i] == '.' {
			return value[:i], value[i+1:], true
		}
	}
	return "", "", false
}

func schemaFor(namespace, name string) map[string]any {
	for _, action := range allActions {
		if action.Namespace() == namespace && action.Name() == name {
			return action.Parameters()
		}
	}
	return nil
}
