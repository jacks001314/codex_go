package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExternalCursorHomeMigrationDetectsImportsAndDoesNotRedetect(t *testing.T) {
	root := t.TempDir()
	codexHome := filepath.Join(root, ".codex")
	claudeHome := filepath.Join(root, ".claude")
	cursorHome := filepath.Join(root, ".cursor")
	writable := filepath.Join(root, "generated")
	marketplace := filepath.Join(cursorHome, "plugins", "marketplaces", "acme-cache")
	pluginRoot := filepath.Join(marketplace, "plugins", "sample")
	for _, dir := range []string{
		filepath.Join(cursorHome, "hooks"),
		filepath.Join(cursorHome, "skills", "cursor-review"),
		filepath.Join(cursorHome, "commands"),
		filepath.Join(cursorHome, "agents"),
		filepath.Join(cursorHome, "plugins", "cache", "acme-cache", "sample"),
		filepath.Join(marketplace, ".cursor-plugin"),
		filepath.Join(pluginRoot, ".codex-plugin"),
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writes := map[string]string{
		filepath.Join(cursorHome, "cli-config.json"):                     `{"env":{"CURSOR_ENV":"yes"}}`,
		filepath.Join(cursorHome, "sandbox.json"):                        `{"type":"workspace_readwrite","additionalReadwritePaths":[` + jsonQuoted(writable) + `,"relative"],"disableTmpWrite":true,"networkPolicy":{"default":"allow"}}`,
		filepath.Join(cursorHome, "mcp.json"):                            `{"mcpServers":{"cursor-docs":{"command":"cursor-docs","args":["--stdio"]}}}`,
		filepath.Join(cursorHome, "hooks.json"):                          `{"hooks":{"preToolUse":[{"command":"sh .cursor/hooks/check.sh","matcher":"Shell","statusMessage":"Cursor check","timeoutSec":"7","failClosed":false}]}}`,
		filepath.Join(cursorHome, "hooks", "check.sh"):                   "echo check\n",
		filepath.Join(cursorHome, "skills", "cursor-review", "SKILL.md"): "Use Cursor with .cursorrules, but keep lowercase cursor.\n",
		filepath.Join(cursorHome, "commands", "review.md"):               "Review the Cursor changes in .cursorrules.\n",
		filepath.Join(cursorHome, "agents", "reviewer.md"):               "---\nname: reviewer\ndescription: Cursor reviewer\n---\nCheck .cursorrules carefully.\n",
		filepath.Join(marketplace, ".cursor-plugin", "marketplace.json"): `{"name":"acme","plugins":[{"name":"sample","source":{"source":"local","path":"./plugins/sample"}}]}`,
		filepath.Join(pluginRoot, ".codex-plugin", "plugin.json"):        `{"name":"sample","version":"1.0.0"}`,
	}
	for path, contents := range writes {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	service := NewConfigService(codexHome)
	service.SetExternalAgentHome(claudeHome)
	source := externalMigrationSourceCursor
	detected := service.DetectExternalAgentConfig(&ExternalAgentConfigDetectParams{IncludeHome: true, MigrationSource: &source})
	wantTypes := []MigrationItemType{MigrationConfig, MigrationMCPServerConfig, MigrationHooks, MigrationSkills, MigrationCommands, MigrationSubagents, MigrationPlugins}
	if got := cursorMigrationTypes(detected.Items); strings.Join(got, ",") != strings.Join(migrationTypeStrings(wantTypes), ",") {
		t.Fatalf("detected types = %v, items=%#v", got, detected.Items)
	}

	_, completed := service.ImportExternalAgentConfig(&ExternalAgentConfigImportParams{MigrationItems: detected.Items, MigrationSource: &source})
	if len(completed.ItemTypeResults) != len(wantTypes) {
		t.Fatalf("completed results = %#v", completed.ItemTypeResults)
	}
	for _, result := range completed.ItemTypeResults {
		if len(result.Failures) != 0 || len(result.Successes) == 0 {
			t.Fatalf("migration result = %#v", result)
		}
	}

	configText := mustReadExternalCursorTestFile(t, filepath.Join(codexHome, "config.toml"))
	for _, want := range []string{"CURSOR_ENV", "cursor-docs", "sample@acme", "installed = true", "enabled = true"} {
		if !strings.Contains(configText, want) {
			t.Fatalf("config missing %q:\n%s", want, configText)
		}
	}
	for _, unwanted := range []string{"workspace-write", "writable_roots", "exclude_slash_tmp", "network_access"} {
		if strings.Contains(configText, unwanted) {
			t.Fatalf("config must not migrate Cursor sandbox settings, found %q:\n%s", unwanted, configText)
		}
	}

	hooksText := mustReadExternalCursorTestFile(t, filepath.Join(codexHome, "hooks.json"))
	for _, want := range []string{"PreToolUse", `"matcher": "Shell"`, `"timeout": 7`, "Codex check"} {
		if !strings.Contains(hooksText, want) {
			t.Fatalf("hooks missing %q:\n%s", want, hooksText)
		}
	}
	var hooksPayload struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(hooksText), &hooksPayload); err != nil || len(hooksPayload.Hooks["PreToolUse"]) != 1 || len(hooksPayload.Hooks["PreToolUse"][0].Hooks) != 1 {
		t.Fatalf("hooks payload = %#v err=%v", hooksPayload, err)
	}
	wantHookCommand := "sh '" + filepath.Join(codexHome, "hooks", "check.sh") + "'"
	if got := hooksPayload.Hooks["PreToolUse"][0].Hooks[0].Command; got != wantHookCommand {
		t.Fatalf("hook command = %q, want %q", got, wantHookCommand)
	}
	if got := mustReadExternalCursorTestFile(t, filepath.Join(root, ".agents", "skills", "cursor-review", "SKILL.md")); got != "Use Codex with AGENTS.md, but keep lowercase cursor.\n" {
		t.Fatalf("migrated skill = %q", got)
	}
	command := mustReadExternalCursorTestFile(t, filepath.Join(root, ".agents", "skills", "source-command-review", "SKILL.md"))
	for _, want := range []string{"Migrated source command `review`", "Review the Codex changes in AGENTS.md."} {
		if !strings.Contains(command, want) {
			t.Fatalf("command missing %q:\n%s", want, command)
		}
	}
	agent := mustReadExternalCursorTestFile(t, filepath.Join(codexHome, "agents", "reviewer.toml"))
	for _, want := range []string{"Codex reviewer", "Check AGENTS.md carefully"} {
		if !strings.Contains(agent, want) {
			t.Fatalf("agent missing %q:\n%s", want, agent)
		}
	}
	if detectedAgain := service.DetectExternalAgentConfig(&ExternalAgentConfigDetectParams{IncludeHome: true, MigrationSource: &source}); len(detectedAgain.Items) != 0 {
		t.Fatalf("already imported Cursor setup detected again = %#v", detectedAgain.Items)
	}
}

func TestExternalCursorRepositoryMigrationUsesProjectFilesAndLegacyRules(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	cursorDir := filepath.Join(repo, ".cursor")
	codexHome := filepath.Join(root, ".codex")
	for _, dir := range []string{filepath.Join(repo, ".git"), cursorDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(cursorDir, "cli.json"), []byte(`{"env":{"REPO_CURSOR":"yes"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cursorDir, "sandbox.json"), []byte(`{"type":"read_only"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".cursorrules"), []byte("Use Cursor and .cursorrules.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := NewConfigService(codexHome)
	source := externalMigrationSourceCursor
	detected := service.DetectExternalAgentConfig(&ExternalAgentConfigDetectParams{CWDs: []string{repo}, MigrationSource: &source})
	if got := strings.Join(cursorMigrationTypes(detected.Items), ","); got != "CONFIG,AGENTS_MD" {
		t.Fatalf("detected types = %s, items=%#v", got, detected.Items)
	}
	service.ImportExternalAgentConfig(&ExternalAgentConfigImportParams{MigrationItems: detected.Items, MigrationSource: &source})
	configText := mustReadExternalCursorTestFile(t, filepath.Join(repo, ".codex", "config.toml"))
	if !strings.Contains(configText, "REPO_CURSOR") || strings.Contains(configText, "read-only") {
		t.Fatalf("repo config = %s", configText)
	}
	if got := mustReadExternalCursorTestFile(t, filepath.Join(repo, "AGENTS.md")); got != "Use Codex and AGENTS.md.\n" {
		t.Fatalf("repo AGENTS.md = %q", got)
	}
}

func jsonQuoted(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func cursorMigrationTypes(items []ExternalAgentConfigMigrationItem) []string {
	out := make([]string, len(items))
	for i := range items {
		out[i] = string(items[i].ItemType)
	}
	return out
}

func migrationTypeStrings(values []MigrationItemType) []string {
	out := make([]string, len(values))
	for i := range values {
		out[i] = string(values[i])
	}
	return out
}

func mustReadExternalCursorTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestExternalCursorSessionDetectionUsesCursorShapeAndLimits(t *testing.T) {
	root := t.TempDir()
	cursorHome := filepath.Join(root, ".cursor")
	cwd := filepath.Join(root, "workspace")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	encodedProject := strings.Replace(externalCursorPathSlug(cwd), "--", "-", 1)
	transcript := filepath.Join(cursorHome, "projects", encodedProject, "agent-transcripts", "session", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"role":"user","timestamp_ms":1800000000000,"message":{"content":[{"type":"text","text":"<user_query>Fix Cursor import</user_query>"}]}}
{"role":"assistant","message":{"content":[{"type":"text","text":"done"}]}}`
	if err := os.WriteFile(transcript, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	service := NewConfigService(filepath.Join(root, ".codex"))
	service.SetExternalAgentHome(filepath.Join(root, ".claude"))
	source := externalMigrationSourceCursor
	maxSessions := uint32(1)
	detected := service.DetectExternalAgentConfig(&ExternalAgentConfigDetectParams{IncludeHome: true, MigrationSource: &source, MaxSessions: &maxSessions})
	if len(detected.Items) != 1 || detected.Items[0].ItemType != MigrationSessions || detected.Items[0].Details == nil || len(detected.Items[0].Details.Sessions) != 1 {
		t.Fatalf("detected sessions = %#v", detected.Items)
	}
	session := detected.Items[0].Details.Sessions[0]
	if session.Path != transcript || session.CWD != cwd || session.Title == nil || *session.Title != "Fix Cursor import" {
		t.Fatalf("session = %#v", session)
	}
	if err := RecordExternalSessionImport(filepath.Join(root, ".codex"), transcript, "thread-cursor"); err != nil {
		t.Fatal(err)
	}
	if detectedAgain := service.DetectExternalAgentConfig(&ExternalAgentConfigDetectParams{IncludeHome: true, MigrationSource: &source}); len(detectedAgain.Items) != 0 {
		t.Fatalf("imported Cursor session detected again = %#v", detectedAgain.Items)
	}
	if err := os.WriteFile(transcript, []byte(body+"\n"+`{"role":"assistant","message":{"content":"updated"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if changed := service.DetectExternalAgentConfig(&ExternalAgentConfigDetectParams{IncludeHome: true, MigrationSource: &source}); len(changed.Items) != 1 || changed.Items[0].ItemType != MigrationSessions {
		t.Fatalf("changed Cursor session not redetected = %#v", changed.Items)
	}
}
