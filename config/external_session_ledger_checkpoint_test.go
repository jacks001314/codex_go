package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestExternalSessionImportCheckpointPreservesMetadataAndGuardsOldState(t *testing.T) {
	codexHome := t.TempDir()
	sourcePath := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(sourcePath, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	title := "Imported title"
	if err := RecordExternalSessionImports(codexHome, []ExternalSessionImportCompletion{
		{SourcePath: sourcePath, ImportedThreadID: "thread-1", ConnectorNames: []string{"Figma"}, Title: &title},
	}); err != nil {
		t.Fatal(err)
	}
	mapping, err := FindExternalSessionImport(codexHome, sourcePath)
	if err != nil || !mapping.Found || mapping.Ambiguous {
		t.Fatalf("initial mapping = %#v err=%v", mapping, err)
	}
	oldHash := mapping.SourceContentSHA256
	if err := os.WriteFile(sourcePath, []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, newHash, err := ExternalSessionContentSHA256(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if checkpointed, err := CheckpointExternalSessionImport(codexHome, sourcePath, "wrong-thread", oldHash, newHash); err != nil || checkpointed {
		t.Fatalf("wrong-thread checkpoint = %v err=%v", checkpointed, err)
	}
	if checkpointed, err := CheckpointExternalSessionImport(codexHome, sourcePath, "thread-1", "wrong-hash", newHash); err != nil || checkpointed {
		t.Fatalf("wrong-hash checkpoint = %v err=%v", checkpointed, err)
	}
	if checkpointed, err := CheckpointExternalSessionImport(codexHome, sourcePath, "thread-1", oldHash, newHash); err != nil || !checkpointed {
		t.Fatalf("valid checkpoint = %v err=%v", checkpointed, err)
	}
	ledger, err := loadExternalSessionImportLedger(codexHome)
	if err != nil || len(ledger.Records) != 1 {
		t.Fatalf("ledger = %#v err=%v", ledger, err)
	}
	record := ledger.Records[0]
	if record.ContentSHA256 != newHash || record.ImportedThreadID != "thread-1" || record.Title == nil || *record.Title != title || !reflect.DeepEqual(record.ConnectorNames, []string{"Figma"}) {
		t.Fatalf("checkpointed record = %#v", record)
	}
}

func TestExternalSessionImportCheckpointRejectsAmbiguousAndChangedSource(t *testing.T) {
	codexHome := t.TempDir()
	sourcePath := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(sourcePath, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RecordExternalSessionImport(codexHome, sourcePath, "thread-1"); err != nil {
		t.Fatal(err)
	}
	mapping, err := FindExternalSessionImport(codexHome, sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	oldHash := mapping.SourceContentSHA256
	if err := os.WriteFile(sourcePath, []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, secondHash, err := ExternalSessionContentSHA256(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, []byte("third"), 0o600); err != nil {
		t.Fatal(err)
	}
	if checkpointed, err := CheckpointExternalSessionImport(codexHome, sourcePath, "thread-1", oldHash, secondHash); err != nil || checkpointed {
		t.Fatalf("changed-source checkpoint = %v err=%v", checkpointed, err)
	}

	ledger, err := loadExternalSessionImportLedger(codexHome)
	if err != nil {
		t.Fatal(err)
	}
	duplicate := ledger.Records[0]
	duplicate.ImportedThreadID = "thread-2"
	ledger.Records = append(ledger.Records, duplicate)
	if err := saveExternalSessionImportLedger(codexHome, ledger); err != nil {
		t.Fatal(err)
	}
	ambiguous, err := FindExternalSessionImport(codexHome, sourcePath)
	if err != nil || !ambiguous.Found || !ambiguous.Ambiguous {
		t.Fatalf("ambiguous mapping = %#v err=%v", ambiguous, err)
	}
	_, thirdHash, err := ExternalSessionContentSHA256(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if checkpointed, err := CheckpointExternalSessionImport(codexHome, sourcePath, "thread-1", oldHash, thirdHash); err != nil || checkpointed {
		t.Fatalf("ambiguous checkpoint = %v err=%v", checkpointed, err)
	}
}

// TestImportedConnectorCandidatesLikeRust ports Rust
// read_imported_connector_candidates: names are grouped per source path, one
// spelling per source is counted, and the candidates are sorted by name.
func TestImportedConnectorCandidatesLikeRust(t *testing.T) {
	home := t.TempDir()
	ledger := map[string]any{"records": []any{
		map[string]any{
			"source_path":        "/sessions/one.jsonl",
			"content_sha256":     "one",
			"imported_thread_id": "thread-1",
			"imported_at":        1,
			"connector_names":    []any{"Linear", "  ", "linear", "GitHub"},
		},
		map[string]any{
			"source_path":        "/sessions/two.jsonl",
			"content_sha256":     "two",
			"imported_thread_id": "thread-2",
			"imported_at":        2,
			"connector_names":    []any{"linear", "Notion"},
		},
	}}
	data, err := json.Marshal(ledger)
	if err != nil {
		t.Fatalf("Marshal(ledger) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, externalSessionImportLedgerFile), data, 0o600); err != nil {
		t.Fatalf("WriteFile(ledger) error = %v", err)
	}
	got := ImportedConnectorCandidates(home)
	want := []ExternalAgentImportedConnectorCandidate{
		{Name: "GitHub", SessionCount: 1, Source: ExternalAgentImportedConnectorSourceRemoteMCPServersConfig},
		{Name: "Linear", SessionCount: 2, Source: ExternalAgentImportedConnectorSourceRemoteMCPServersConfig},
		{Name: "Notion", SessionCount: 1, Source: ExternalAgentImportedConnectorSourceRemoteMCPServersConfig},
	}
	if len(got) != len(want) {
		t.Fatalf("candidates = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("candidate[%d] = %#v, want %#v", i, got[i], want[i])
		}
	}
}

// TestImportHistoriesResponseIncludesConnectorsLikeRust pins the required
// `connectors` field (an empty array, never missing or null).
func TestImportHistoriesResponseIncludesConnectorsLikeRust(t *testing.T) {
	service := NewConfigService(t.TempDir())
	response := service.ImportHistories()
	if response == nil || response.Connectors == nil {
		t.Fatalf("connectors = %#v, want an empty slice", response)
	}
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("Marshal(response) error = %v", err)
	}
	if !strings.Contains(string(data), `"connectors":[]`) {
		t.Fatalf("marshaled response = %s, want an empty connectors array", data)
	}
}
