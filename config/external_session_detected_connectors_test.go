package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestRecordDetectedExternalSessionConnectorsFeedsCandidatesLikeRust pins the
// detected-connector ledger path: recording detection results persists them, the
// candidate read reports them (deduplicated case-insensitively per source), and a
// second recording for the same source does not double-count.
func TestRecordDetectedExternalSessionConnectorsFeedsCandidatesLikeRust(t *testing.T) {
	home := t.TempDir()
	sourceA := filepath.Join(home, "session-a.jsonl")
	sourceB := filepath.Join(home, "session-b.jsonl")
	for _, path := range []string{sourceA, sourceB} {
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := RecordDetectedExternalSessionConnectors(home, map[string][]string{
		sourceA: {"Acme MCP", "acme mcp", "Beta"},
	}); err != nil {
		t.Fatal(err)
	}
	want := []ExternalAgentImportedConnectorCandidate{
		{Name: "Acme MCP", SessionCount: 1, Source: ExternalAgentImportedConnectorSourceRemoteMCPServersConfig},
		{Name: "Beta", SessionCount: 1, Source: ExternalAgentImportedConnectorSourceRemoteMCPServersConfig},
	}
	if got := ImportedConnectorCandidates(home); !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates = %#v, want %#v", got, want)
	}

	// A second detection for the same source must not duplicate the names, while
	// another source increments the session count.
	if err := RecordDetectedExternalSessionConnectors(home, map[string][]string{
		sourceA: {"Acme MCP"},
		sourceB: {"acme mcp"},
	}); err != nil {
		t.Fatal(err)
	}
	want[0].SessionCount = 2
	if got := ImportedConnectorCandidates(home); !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates after second record = %#v, want %#v", got, want)
	}

	// The detected records are persisted in the ledger file.
	data, err := os.ReadFile(filepath.Join(home, externalSessionImportLedgerFile))
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		DetectedConnectorRecords []detectedExternalSessionConnectorRecord `json:"detected_connector_records"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw.DetectedConnectorRecords) != 2 {
		t.Fatalf("detected records = %#v", raw.DetectedConnectorRecords)
	}
}

// TestDetectExternalAgentConfigSerializesConnectors pins the detect response
// shape: connectors is always present, even when nothing was detected.
func TestDetectExternalAgentConfigSerializesConnectors(t *testing.T) {
	service := NewConfigService(t.TempDir())
	response := service.DetectExternalAgentConfig(&ExternalAgentConfigDetectParams{})
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	connectors, ok := wire["connectors"]
	if !ok {
		t.Fatalf("detect response is missing connectors: %s", data)
	}
	if string(connectors) != "[]" {
		t.Fatalf("connectors = %s, want []", connectors)
	}
}

// TestExternalMigrationItemSessionsRequiresExistingFiles pins Rust's
// `session.path.is_file()` filter.
func TestExternalMigrationItemSessionsRequiresExistingFiles(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "exists.jsonl")
	if err := os.WriteFile(existing, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	items := []ExternalAgentConfigMigrationItem{{
		Details: &MigrationDetails{Sessions: []SessionMigration{
			{Path: existing},
			{Path: filepath.Join(dir, "missing.jsonl")},
			{Path: "  "},
		}},
	}}
	sessions := externalMigrationItemSessions(items)
	if len(sessions) != 1 || sessions[0].Path != existing {
		t.Fatalf("sessions = %#v", sessions)
	}
}
