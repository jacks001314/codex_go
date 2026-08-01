package config

import (
	"encoding/json"
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

func TestDetectExternalClaudeSessionConnectorCandidatesCountsEachSessionOnce(t *testing.T) {
	root := t.TempDir()
	manifestDir := filepath.Join(root, externalSessionManifestsDir)
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifests := map[string]string{
		"first.json":  `{"cliSessionId":"session-1","remoteMcpServersConfig":[{"uuid":"figma-id","name":"Figma"},{"uuid":"gmail-id","name":"Gmail"}]}`,
		"second.json": `{"cliSessionId":"session-2","remoteMcpServersConfig":[{"uuid":"figma-id","name":"figma"}]}`,
	}
	for name, body := range manifests {
		if err := os.WriteFile(filepath.Join(manifestDir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sessionDir := t.TempDir()
	first := filepath.Join(sessionDir, "session-1.jsonl")
	second := filepath.Join(sessionDir, "session-2.jsonl")
	if err := os.WriteFile(first, []byte("{\"attributionMcpServer\":\"FIGMA\"}\n{\"attributionMcpServer\":\"figma-id\"}\n{\"attributionMcpServer\":\"gmail-id\"}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte(`{"attributionMcpServer":"figma-id"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got := detectExternalClaudeSessionConnectorCandidates([]SessionMigration{{Path: first}, {Path: second}}, []string{root})
	want := []ExternalAgentDetectedConnectorCandidate{
		{Name: "Figma", SessionCount: 2, Source: ExternalAgentConnectorRemoteMCPServersConfig},
		{Name: "Gmail", SessionCount: 1, Source: ExternalAgentConnectorRemoteMCPServersConfig},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("connector candidates = %#v, want %#v", got, want)
	}
}

func TestDetectExternalCursorSessionConnectorCandidatesUsesCachedPluginMetadata(t *testing.T) {
	home := t.TempDir()
	writeExternalCursorConnectorTestPlugin(t, home, "default", "Default", nil, map[string]any{
		".mcp.json": map[string]any{"mcpServers": map[string]any{"alpha": map[string]any{}}},
	})
	writeExternalCursorConnectorTestPlugin(t, home, "inline", "Inline", map[string]any{
		"mcpServers": map[string]any{"beta": map[string]any{}},
	}, nil)
	writeExternalCursorConnectorTestPlugin(t, home, "path", "Path", "config/mcp.json", map[string]any{
		"config/mcp.json": map[string]any{"mcpServers": map[string]any{"gamma": map[string]any{}}},
	})
	writeExternalCursorConnectorTestPlugin(t, home, "list", "List", []any{
		map[string]any{"mcpServers": map[string]any{"delta": map[string]any{}}},
		"second.json",
	}, map[string]any{
		"second.json": map[string]any{"epsilon": map[string]any{}},
	})
	writeExternalCursorConnectorTestPlugin(t, home, "escape", "Escape", "../outside.json", nil)
	escapeVersion := filepath.Join(home, "plugins", "cache", "market", "escape", "version")
	writeExternalCursorConnectorTestJSON(t, filepath.Join(escapeVersion, "..", "outside.json"), map[string]any{"escaped": map[string]any{}})

	first := filepath.Join(t.TempDir(), "first.jsonl")
	second := filepath.Join(t.TempDir(), "second.jsonl")
	firstRecords := []any{
		map[string]any{"message": map[string]any{"content": []any{
			"malformed-neighbor",
			map[string]any{"type": "tool_use", "name": "GetMcpTools", "input": map[string]any{"server": "plugin-inline-beta"}},
			map[string]any{"type": "tool_use", "name": "CallMcpTool", "input": map[string]any{"server": " PLUGIN-INLINE-BETA "}},
			map[string]any{"type": "tool_use", "name": "CallMcpTool", "input": map[string]any{"server": "plugin-inline-beta"}},
			map[string]any{"type": "tool_use", "name": "CallMcpTool", "input": map[string]any{"server": "plugin-default-alpha"}},
		}}},
	}
	secondRecords := []any{
		map[string]any{"message": map[string]any{"content": []any{
			map[string]any{"type": "tool_use", "name": "CallMcpTool", "input": map[string]any{"server": "plugin-inline-beta"}},
			map[string]any{"type": "tool_use", "name": "CallMcpTool", "input": map[string]any{"server": "plugin-path-gamma"}},
			map[string]any{"type": "tool_use", "name": "CallMcpTool", "input": map[string]any{"server": "plugin-list-delta"}},
			map[string]any{"type": "tool_use", "name": "CallMcpTool", "input": map[string]any{"server": "plugin-list-epsilon"}},
			map[string]any{"type": "tool_use", "name": "CallMcpTool", "input": map[string]any{"server": "plugin-escape-escaped"}},
		}}},
	}
	writeExternalCursorConnectorTestJSONLines(t, first, firstRecords)
	writeExternalCursorConnectorTestJSONLines(t, second, secondRecords)

	got := detectExternalCursorSessionConnectorCandidates([]SessionMigration{{Path: first}, {Path: second}}, home)
	want := []ExternalAgentDetectedConnectorCandidate{
		{Name: "Default", SessionCount: 1, Source: ExternalAgentConnectorSessionToolUse},
		{Name: "Inline", SessionCount: 2, Source: ExternalAgentConnectorSessionToolUse},
		{Name: "List", SessionCount: 1, Source: ExternalAgentConnectorSessionToolUse},
		{Name: "Path", SessionCount: 1, Source: ExternalAgentConnectorSessionToolUse},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("connector candidates = %#v, want %#v", got, want)
	}
}

func TestDetectExternalSessionConnectorsSelectsCursorSiblingHome(t *testing.T) {
	userHome := t.TempDir()
	cursorHome := filepath.Join(userHome, ".cursor")
	writeExternalCursorConnectorTestPlugin(t, cursorHome, "figma", "Figma", nil, map[string]any{
		".mcp.json": map[string]any{"mcpServers": map[string]any{"figma": map[string]any{}}},
	})
	session := filepath.Join(t.TempDir(), "session.jsonl")
	writeExternalCursorConnectorTestJSONLines(t, session, []any{
		map[string]any{"message": map[string]any{"content": []any{
			map[string]any{"type": "tool_use", "name": "CallMcpTool", "input": map[string]any{"server": "plugin-figma-figma"}},
		}}},
	})
	service := NewConfigService(t.TempDir())
	service.SetExternalAgentHome(filepath.Join(userHome, ".claude"))
	source := externalMigrationSourceCursor
	got := service.DetectExternalSessionConnectors(&source, []SessionMigration{{Path: session}})
	want := []ExternalAgentDetectedConnectorCandidate{{Name: "Figma", SessionCount: 1, Source: ExternalAgentConnectorSessionToolUse}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("connector candidates = %#v, want %#v", got, want)
	}
}

func TestSafeExternalCursorRelativePath(t *testing.T) {
	for _, path := range []string{".mcp.json", "config/mcp.json", `config\mcp.json`, "./config.json"} {
		if !safeExternalCursorRelativePath(path) {
			t.Errorf("safe path %q rejected", path)
		}
	}
	for _, path := range []string{"", "../mcp.json", "config/../mcp.json", "/mcp.json", `\mcp.json`, `C:\mcp.json`} {
		if safeExternalCursorRelativePath(path) {
			t.Errorf("unsafe path %q accepted", path)
		}
	}
}

func writeExternalCursorConnectorTestPlugin(t *testing.T, home, name, displayName string, declaration any, files map[string]any) {
	t.Helper()
	versionRoot := filepath.Join(home, "plugins", "cache", "market", name, "version")
	manifest := map[string]any{"name": name, "displayName": displayName}
	if declaration != nil {
		manifest["mcpServers"] = declaration
	}
	writeExternalCursorConnectorTestJSON(t, filepath.Join(versionRoot, ".cursor-plugin", "plugin.json"), manifest)
	for path, value := range files {
		writeExternalCursorConnectorTestJSON(t, filepath.Join(versionRoot, filepath.FromSlash(path)), value)
	}
}

func writeExternalCursorConnectorTestJSON(t *testing.T, path string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeExternalCursorConnectorTestJSONLines(t *testing.T, path string, values []any) {
	t.Helper()
	data := make([]byte, 0)
	for index, value := range values {
		line, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if index != 0 {
			data = append(data, '\n')
		}
		data = append(data, line...)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
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
