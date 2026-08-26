package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExternalCoreMigrationDetectsAndImportsSkillsWithoutOverwriting(t *testing.T) {
	root := t.TempDir()
	codexHome := filepath.Join(root, ".gcode")
	externalHome := filepath.Join(root, ".claude")
	sourceSkill := filepath.Join(externalHome, "skills", "reviewer")
	if err := os.MkdirAll(sourceSkill, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceSkill, "SKILL.md"), []byte("Use Claude Code and CLAUDE.md.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := NewConfigService(codexHome)
	service.SetExternalAgentHome(externalHome)
	detected := service.DetectExternalAgentConfig(&ExternalAgentConfigDetectParams{IncludeHome: true})
	if len(detected.Items) != 1 || detected.Items[0].ItemType != MigrationSkills || detected.Items[0].Details == nil || len(detected.Items[0].Details.Skills) != 1 {
		t.Fatalf("detected skills = %#v", detected.Items)
	}
	_, completed := service.ImportExternalAgentConfig(&ExternalAgentConfigImportParams{MigrationItems: detected.Items})
	if len(completed.ItemTypeResults) != 1 || len(completed.ItemTypeResults[0].Successes) != 1 {
		t.Fatalf("completed = %#v", completed)
	}
	target := filepath.Join(root, ".agents", "skills", "reviewer", "SKILL.md")
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "Use Codex and AGENTS.md.\n" {
		t.Fatalf("imported skill = %q err=%v", data, err)
	}
	if detectedAgain := service.DetectExternalAgentConfig(&ExternalAgentConfigDetectParams{IncludeHome: true}); len(detectedAgain.Items) != 0 {
		t.Fatalf("already imported skill detected again = %#v", detectedAgain.Items)
	}
	if err := os.WriteFile(target, []byte("user-owned\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, completed = service.ImportExternalAgentConfig(&ExternalAgentConfigImportParams{MigrationItems: detected.Items})
	data, err = os.ReadFile(target)
	if err != nil || strings.TrimSpace(string(data)) != "user-owned" || len(completed.ItemTypeResults[0].Successes) != 0 {
		t.Fatalf("existing skill overwritten: data=%q completed=%#v err=%v", data, completed, err)
	}
}

func TestExternalCoreMigrationMergesLocalSettingsAndPreservesExistingConfig(t *testing.T) {
	root := t.TempDir()
	codexHome := filepath.Join(root, ".gcode")
	externalHome := filepath.Join(root, ".claude")
	if err := os.MkdirAll(externalHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(externalHome, "settings.json"), []byte(`{"env":{"BASE":"one","OVERRIDE":"old"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(externalHome, "settings.local.json"), []byte(`{"env":{"OVERRIDE":"new"},"sandbox":{"enabled":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte("model = \"gpt-existing\"\n[shell_environment_policy.set]\nBASE = \"keep\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := NewConfigService(codexHome)
	service.SetExternalAgentHome(externalHome)
	detected := service.DetectExternalAgentConfig(&ExternalAgentConfigDetectParams{IncludeHome: true})
	if len(detected.Items) != 1 || detected.Items[0].ItemType != MigrationConfig {
		t.Fatalf("detected config = %#v", detected.Items)
	}
	_, completed := service.ImportExternalAgentConfig(&ExternalAgentConfigImportParams{MigrationItems: detected.Items})
	if len(completed.ItemTypeResults) != 1 || len(completed.ItemTypeResults[0].Successes) != 1 || len(completed.ItemTypeResults[0].Failures) != 0 {
		t.Fatalf("completed = %#v", completed)
	}
	data, err := os.ReadFile(filepath.Join(codexHome, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"gpt-existing", "BASE", "keep", "OVERRIDE", "new", "workspace-write"} {
		if !strings.Contains(text, want) {
			t.Fatalf("merged config missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "BASE = 'one'") || strings.Contains(text, `BASE = "one"`) {
		t.Fatalf("existing config value was overwritten:\n%s", text)
	}
}

func TestExternalToolsMigrationDetectsAndImportsMCPCommandsAndSubagents(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	codexHome := filepath.Join(root, ".gcode")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	commandDir := filepath.Join(repo, ".claude", "commands", "pr")
	agentDir := filepath.Join(repo, ".claude", "agents")
	if err := os.MkdirAll(commandDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".mcp.json"), []byte(`{
  "mcpServers": {
    "docs": {"command":"docs-new"},
    "api": {"url":"https://example.test/mcp","headers":{"Authorization":"Bearer ${API_TOKEN}","X-Team":"${TEAM}"}},
    "stdio": {"command":"stdio-server","args":["--stdio"],"env":{"TOKEN":"${TOKEN}","STATIC":"yes"}}
  }
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(commandDir, "review.md"), []byte("---\ndescription: Review with Claude Code\n---\nReview CLAUDE.md carefully.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".claude", "commands", "unsupported.md"), []byte("---\ndescription: Skip\n---\nUse $ARGUMENTS.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "researcher.md"), []byte("---\nname: researcher\ndescription: Claude research role\npermissionMode: acceptEdits\neffort: max\n---\nResearch with Claude Code.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".gcode"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gcode", "config.toml"), []byte("[mcp_servers.docs]\ncommand = \"docs-existing\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	service := NewConfigService(codexHome)
	detected := service.DetectExternalAgentConfig(&ExternalAgentConfigDetectParams{CWDs: []string{repo}})
	if len(detected.Items) != 3 {
		t.Fatalf("detected items = %#v", detected.Items)
	}
	wantTypes := []MigrationItemType{MigrationMCPServerConfig, MigrationCommands, MigrationSubagents}
	for index, want := range wantTypes {
		if detected.Items[index].ItemType != want {
			t.Fatalf("detected order = %#v", detected.Items)
		}
	}
	if got := detected.Items[0].Details.MCPServers; len(got) != 2 || got[0].Name != "api" || got[1].Name != "stdio" {
		t.Fatalf("MCP details = %#v", got)
	}
	if got := detected.Items[1].Details.Commands; len(got) != 1 || got[0].Name != "source-command-pr-review" {
		t.Fatalf("command details = %#v", got)
	}
	if got := detected.Items[2].Details.Subagents; len(got) != 1 || got[0].Name != "researcher" {
		t.Fatalf("subagent details = %#v", got)
	}

	_, completed := service.ImportExternalAgentConfig(&ExternalAgentConfigImportParams{MigrationItems: detected.Items})
	if len(completed.ItemTypeResults) != 3 {
		t.Fatalf("completed = %#v", completed)
	}
	configData, err := os.ReadFile(filepath.Join(repo, ".gcode", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	configText := string(configData)
	for _, want := range []string{"docs-existing", "stdio-server", "https://example.test/mcp", "bearer_token_env_var", "API_TOKEN", "env_http_headers", "TEAM", "env_vars", "TOKEN"} {
		if !strings.Contains(configText, want) {
			t.Fatalf("MCP config missing %q:\n%s", want, configText)
		}
	}
	if strings.Contains(configText, "docs-new") {
		t.Fatalf("existing MCP server overwritten:\n%s", configText)
	}
	commandData, err := os.ReadFile(filepath.Join(repo, ".agents", "skills", "source-command-pr-review", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`name: "source-command-pr-review"`, `description: "Review with Codex"`, "Review AGENTS.md carefully."} {
		if !strings.Contains(string(commandData), want) {
			t.Fatalf("command skill missing %q:\n%s", want, commandData)
		}
	}
	if pathExists(filepath.Join(repo, ".agents", "skills", "source-command-unsupported")) {
		t.Fatal("unsupported command template was imported")
	}
	agentData, err := os.ReadFile(filepath.Join(repo, ".gcode", "agents", "researcher.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"researcher", "Codex research role", "workspace-write", "xhigh", "Research with Codex."} {
		if !strings.Contains(string(agentData), want) {
			t.Fatalf("subagent config missing %q:\n%s", want, agentData)
		}
	}
	if detectedAgain := service.DetectExternalAgentConfig(&ExternalAgentConfigDetectParams{CWDs: []string{repo}}); len(detectedAgain.Items) != 0 {
		t.Fatalf("imported tools detected again = %#v", detectedAgain.Items)
	}
}

func TestExternalHooksMigrationFiltersRewritesCopiesAndPreservesTargets(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	codexHome := filepath.Join(root, ".codex-home")
	sourceDir := filepath.Join(repo, ".claude")
	sourceHooks := filepath.Join(sourceDir, "hooks")
	targetHooks := filepath.Join(repo, ".gcode", "hooks")
	for _, dir := range []string{filepath.Join(repo, ".git"), sourceHooks, targetHooks} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	settings := `{
  "hooks": {
    "PreToolUse": [
      {"matcher":"Bash","hooks":[{"type":"command","command":"python3 \"${CLAUDE_PROJECT_DIR}/.claude/hooks/check.sh\"","timeout":"7","statusMessage":"Claude check","async":false}]},
      {"matcher":"Edit","if":"blocked","hooks":[{"type":"command","command":"echo skipped"}]},
      {"matcher":"Write","hooks":[{"type":"prompt","prompt":"skip"},{"type":"command","command":"echo skipped","if":"bad"}]}
    ],
    "Stop": [{"matcher":"ignored","hooks":[{"command":"echo done","timeoutSec":3},{"command":"echo async","async":true}]}],
    "UserPromptSubmit": [{"hooks":[{"type":"prompt","prompt":"skip"}]}],
    "UnknownEvent": [{"hooks":[{"command":"echo unknown"}]}]
  }
}`
	localSettings := `{"disableAllHooks":false,"hooks":{"SessionEnd":[{"matcher":"clear","hooks":[{"command":"sh .claude/hooks/new.sh"}]}]}}`
	if err := os.WriteFile(filepath.Join(sourceDir, "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "settings.local.json"), []byte(localSettings), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceHooks, "check.sh"), []byte("source check\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceHooks, "new.sh"), []byte("source new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetHooks, "check.sh"), []byte("user owned\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	service := NewConfigService(codexHome)
	detected := service.DetectExternalAgentConfig(&ExternalAgentConfigDetectParams{CWDs: []string{repo}})
	if len(detected.Items) != 1 || detected.Items[0].ItemType != MigrationHooks {
		t.Fatalf("detected hooks = %#v", detected.Items)
	}
	gotNames := detected.Items[0].Details.Hooks
	wantNames := []string{"PreToolUse", "SessionEnd", "Stop"}
	if len(gotNames) != len(wantNames) {
		t.Fatalf("hook names = %#v", gotNames)
	}
	for index, want := range wantNames {
		if gotNames[index].Name != want {
			t.Fatalf("hook names = %#v", gotNames)
		}
	}

	_, completed := service.ImportExternalAgentConfig(&ExternalAgentConfigImportParams{MigrationItems: detected.Items})
	if len(completed.ItemTypeResults) != 1 || len(completed.ItemTypeResults[0].Successes) != 3 || len(completed.ItemTypeResults[0].Failures) != 0 {
		t.Fatalf("completed = %#v", completed)
	}
	data, err := os.ReadFile(filepath.Join(repo, ".gcode", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var hooksFile struct {
		Hooks map[string][]struct {
			Matcher *string               `json:"matcher"`
			Hooks   []externalHookHandler `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &hooksFile); err != nil {
		t.Fatal(err)
	}
	if len(hooksFile.Hooks) != 3 || len(hooksFile.Hooks["PreToolUse"]) != 1 || len(hooksFile.Hooks["SessionEnd"]) != 1 || len(hooksFile.Hooks["Stop"]) != 1 {
		t.Fatalf("migrated hooks = %s", data)
	}
	pre := hooksFile.Hooks["PreToolUse"][0]
	if pre.Matcher == nil || *pre.Matcher != "Bash" || len(pre.Hooks) != 1 {
		t.Fatalf("PreToolUse = %#v", pre)
	}
	wantCheckCommand := "python3 " + externalShellSingleQuote(filepath.Join(targetHooks, "check.sh"))
	if pre.Hooks[0].Command != wantCheckCommand || pre.Hooks[0].Timeout == nil || *pre.Hooks[0].Timeout != 7 || pre.Hooks[0].StatusMessage == nil || *pre.Hooks[0].StatusMessage != "Codex check" {
		t.Fatalf("PreToolUse handler = %#v", pre.Hooks[0])
	}
	stop := hooksFile.Hooks["Stop"][0]
	if stop.Matcher != nil || len(stop.Hooks) != 1 || stop.Hooks[0].Command != "echo done" || stop.Hooks[0].Timeout == nil || *stop.Hooks[0].Timeout != 3 {
		t.Fatalf("Stop = %#v", stop)
	}
	for path, want := range map[string]string{
		filepath.Join(targetHooks, "check.sh"): "user owned\n",
		filepath.Join(targetHooks, "new.sh"):   "source new\n",
	} {
		copied, err := os.ReadFile(path)
		if err != nil || string(copied) != want {
			t.Fatalf("copied hook %s = %q err=%v", path, copied, err)
		}
	}
	if detectedAgain := service.DetectExternalAgentConfig(&ExternalAgentConfigDetectParams{CWDs: []string{repo}}); len(detectedAgain.Items) != 0 {
		t.Fatalf("hooks detected after import = %#v", detectedAgain.Items)
	}
	original := string(data)
	_, completed = service.ImportExternalAgentConfig(&ExternalAgentConfigImportParams{MigrationItems: detected.Items})
	after, err := os.ReadFile(filepath.Join(repo, ".gcode", "hooks.json"))
	if err != nil || string(after) != original || len(completed.ItemTypeResults[0].Successes) != 0 {
		t.Fatalf("existing hooks overwritten: data=%q completed=%#v err=%v", after, completed, err)
	}
}

func TestExternalHooksMigrationHonorsLocalDisableOverride(t *testing.T) {
	root := t.TempDir()
	sourceDir := filepath.Join(root, ".claude")
	if err := os.MkdirAll(sourceDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "settings.json"), []byte(`{"disableAllHooks":true,"hooks":{"SessionStart":[{"matcher":"project","hooks":[{"command":"echo project"}]}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "settings.local.json"), []byte(`{"disableAllHooks":false,"hooks":{"SessionStart":[{"matcher":"local","hooks":[{"command":"echo local"}]}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	migration, err := buildExternalHooksMigration(sourceDir, filepath.Join(root, ".gcode"))
	if err != nil || len(migration.Groups["SessionStart"]) != 2 {
		t.Fatalf("local enable override = %#v err=%v", migration, err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "settings.local.json"), []byte(`{"disableAllHooks":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	migration, err = buildExternalHooksMigration(sourceDir, filepath.Join(root, ".gcode"))
	if err != nil || len(migration.Groups) != 0 {
		t.Fatalf("local disable override = %#v err=%v", migration, err)
	}
}

func TestExternalRepoMigrationNeverOverwritesSymlinkTargets(t *testing.T) {
	root := t.TempDir()
	codexHome := filepath.Join(root, ".codex-home")
	empty := ""
	for _, test := range []struct {
		name          string
		linkedContent *string
	}{
		{name: "existing", linkedContent: &empty},
		{name: "dangling"},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := filepath.Join(root, "repo-"+test.name)
			if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(repo, ".gcode"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(repo, "CLAUDE.md"), []byte("Claude guidance\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(repo, ".claude", "settings.json"), []byte(`{"hooks":{"Stop":[{"hooks":[{"command":"echo done"}]}]}}`), 0o600); err != nil {
				t.Fatal(err)
			}

			for _, name := range []string{"AGENTS.md", "hooks.json"} {
				linkedTarget := filepath.Join(root, test.name+"-"+name+"-target")
				migrationTarget := filepath.Join(repo, name)
				if name == "hooks.json" {
					migrationTarget = filepath.Join(repo, ".gcode", name)
				}
				if test.linkedContent != nil {
					if err := os.WriteFile(linkedTarget, []byte(*test.linkedContent), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Symlink(linkedTarget, migrationTarget); err != nil {
					t.Skipf("symlinks are unavailable: %v", err)
				}
			}

			service := NewConfigService(codexHome)
			detected := service.DetectExternalAgentConfig(&ExternalAgentConfigDetectParams{CWDs: []string{repo}})
			if len(detected.Items) != 0 {
				t.Fatalf("symlink targets detected for migration = %#v", detected.Items)
			}

			cwd := repo
			items := []ExternalAgentConfigMigrationItem{
				{ItemType: MigrationAgentsMD, CWD: &cwd},
				{ItemType: MigrationHooks, CWD: &cwd},
			}
			_, completed := service.ImportExternalAgentConfig(&ExternalAgentConfigImportParams{MigrationItems: items})
			if len(completed.ItemTypeResults) != 2 {
				t.Fatalf("completed = %#v", completed)
			}
			for _, result := range completed.ItemTypeResults {
				if len(result.Successes) != 0 || len(result.Failures) != 0 {
					t.Fatalf("symlink target import result = %#v", result)
				}
			}

			for _, name := range []string{"AGENTS.md", "hooks.json"} {
				linkedTarget := filepath.Join(root, test.name+"-"+name+"-target")
				migrationTarget := filepath.Join(repo, name)
				if name == "hooks.json" {
					migrationTarget = filepath.Join(repo, ".gcode", name)
				}
				gotLink, err := os.Readlink(migrationTarget)
				if err != nil || gotLink != linkedTarget {
					t.Fatalf("migration target symlink = %q, want %q, err=%v", gotLink, linkedTarget, err)
				}
				data, err := os.ReadFile(linkedTarget)
				if test.linkedContent == nil {
					if !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("dangling target was created: data=%q err=%v", data, err)
					}
				} else if err != nil || string(data) != *test.linkedContent {
					t.Fatalf("linked target changed: data=%q err=%v", data, err)
				}
			}
		})
	}
}

func TestExternalPluginsMigrationDetectsAndInstallsLocalMarketplace(t *testing.T) {
	root := t.TempDir()
	codexHome := filepath.Join(root, ".gcode")
	externalHome := filepath.Join(root, ".claude")
	marketplace := filepath.Join(externalHome, "my-marketplace")
	pluginRoot := filepath.Join(marketplace, "plugins", "cloudflare")
	for _, dir := range []string{
		externalHome,
		filepath.Join(marketplace, ".claude-plugin"),
		filepath.Join(pluginRoot, ".codex-plugin"),
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	settings := fmt.Sprintf(`{
  "enabledPlugins": {"cloudflare@my-plugins": true, "disabled@my-plugins": false},
  "extraKnownMarketplaces": {"my-plugins": {"source":"local","path":%q}}
}`, marketplace)
	manifest := `{
  "name":"my-plugins",
  "plugins":[{"name":"cloudflare","source":{"source":"local","path":"./plugins/cloudflare"}}]
}`
	if err := os.WriteFile(filepath.Join(externalHome, "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(marketplace, ".claude-plugin", "marketplace.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, ".codex-plugin", "plugin.json"), []byte(`{"name":"cloudflare","version":"0.1.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	service := NewConfigService(codexHome)
	service.SetExternalAgentHome(externalHome)
	detected := service.DetectExternalAgentConfig(&ExternalAgentConfigDetectParams{IncludeHome: true})
	if len(detected.Items) != 1 || detected.Items[0].ItemType != MigrationPlugins || detected.Items[0].Details == nil || len(detected.Items[0].Details.Plugins) != 1 {
		t.Fatalf("detected plugins = %#v", detected.Items)
	}
	group := detected.Items[0].Details.Plugins[0]
	if group.MarketplaceName != "my-plugins" || len(group.PluginNames) != 1 || group.PluginNames[0] != "cloudflare" {
		t.Fatalf("plugin details = %#v", group)
	}
	_, completed := service.ImportExternalAgentConfig(&ExternalAgentConfigImportParams{MigrationItems: detected.Items})
	if len(completed.ItemTypeResults) != 1 || len(completed.ItemTypeResults[0].Successes) != 1 || len(completed.ItemTypeResults[0].Failures) != 0 {
		t.Fatalf("completed = %#v", completed)
	}
	data, err := os.ReadFile(filepath.Join(codexHome, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"my-plugins", "cloudflare@my-plugins", "installed = true", "enabled = true"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("plugin config missing %q:\n%s", want, data)
		}
	}
	if detectedAgain := service.DetectExternalAgentConfig(&ExternalAgentConfigDetectParams{IncludeHome: true}); len(detectedAgain.Items) != 0 {
		t.Fatalf("installed plugin detected again = %#v", detectedAgain.Items)
	}
}

func TestExternalPluginsMigrationUsesLocalSettingsAndValidSources(t *testing.T) {
	root := t.TempDir()
	codexHome := filepath.Join(root, ".gcode")
	externalHome := filepath.Join(root, ".claude")
	if err := os.MkdirAll(externalHome, 0o700); err != nil {
		t.Fatal(err)
	}
	settings := `{
  "enabledPlugins":{"formatter@acme-tools":true,"legacy@acme-tools":true,"missing@unknown":true},
  "extraKnownMarketplaces":{"acme-tools":{"source":"acme/tools"}}
}`
	local := `{"enabledPlugins":{"formatter@acme-tools":false,"deployer@acme-tools":true}}`
	if err := os.WriteFile(filepath.Join(externalHome, "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(externalHome, "settings.local.json"), []byte(local), 0o600); err != nil {
		t.Fatal(err)
	}
	service := NewConfigService(codexHome)
	service.SetExternalAgentHome(externalHome)
	detected := service.DetectExternalAgentConfig(&ExternalAgentConfigDetectParams{IncludeHome: true})
	if len(detected.Items) != 1 || detected.Items[0].ItemType != MigrationPlugins {
		t.Fatalf("detected plugins = %#v", detected.Items)
	}
	plugins := detected.Items[0].Details.Plugins
	if len(plugins) != 1 || plugins[0].MarketplaceName != "acme-tools" || strings.Join(plugins[0].PluginNames, ",") != "deployer,legacy" {
		t.Fatalf("plugin details = %#v", plugins)
	}
}
