package config

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestDetectExternalSessionConnectorsMatchesNameAndUUID(t *testing.T) {
	root := t.TempDir()
	manifestDir := filepath.Join(root, externalSessionManifestsDir, "nested")
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{
  "cliSessionId": "session-1",
  "remoteMcpServersConfig": [
    {"uuid": "figma-uuid", "name": " Figma "},
    {"uuid": "gmail-uuid", "name": "Gmail"},
    {"uuid": "other-uuid", "name": "Other"}
  ]
}`
	if err := os.WriteFile(filepath.Join(manifestDir, "session.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	got := detectExternalSessionConnectors([]externalSessionConnectorAttribution{{
		SessionID: "session-1",
		ServerIDs: map[string]bool{"fIgMa": true, "gmail-uuid": true},
		Roots:     []string{root},
	}})
	if !reflect.DeepEqual(got["session-1"], []string{"Figma", "Gmail"}) {
		t.Fatalf("connector names = %#v", got)
	}
}

func TestRecordExternalSessionImportsResolvesConnectorByServerName(t *testing.T) {
	userHome := t.TempDir()
	externalHome := filepath.Join(userHome, ".claude")
	sourceDir := filepath.Join(externalHome, "projects", "repo")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(sourceDir, "session-1.jsonl")
	if err := os.WriteFile(sourcePath, []byte(`{"type":"assistant","attributionMcpServer":"fIgMa"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var metadataRoot string
	switch runtime.GOOS {
	case "darwin":
		metadataRoot = filepath.Join(userHome, "Library", "Application Support", "Claude")
	case "windows":
		appData := filepath.Join(userHome, "AppData", "Roaming")
		t.Setenv("APPDATA", appData)
		t.Setenv("LOCALAPPDATA", filepath.Join(userHome, "AppData", "Local"))
		metadataRoot = filepath.Join(appData, "Claude")
	default:
		metadataRoot = filepath.Join(userHome, ".config", "Claude")
	}
	manifestDir := filepath.Join(metadataRoot, externalSessionManifestsDir)
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifestDir, "session.json"), []byte(`{"cliSessionId":"session-1","remoteMcpServersConfig":[{"uuid":"figma-uuid","name":"Figma"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	codexHome := t.TempDir()
	if err := RecordExternalSessionImports(codexHome, []ExternalSessionImportCompletion{{
		SourcePath: sourcePath, ImportedThreadID: "thread-1",
	}}); err != nil {
		t.Fatal(err)
	}
	ledger, err := loadExternalSessionImportLedger(codexHome)
	if err != nil || len(ledger.Records) != 1 || !reflect.DeepEqual(ledger.Records[0].ConnectorNames, []string{"Figma"}) {
		t.Fatalf("ledger = %#v, error = %v", ledger, err)
	}
}
