package appserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/plugin"
	"codex_go/turn"
)

func TestHookDiscoveryLoadsProjectHooksJSON(t *testing.T) {
	cwd := t.TempDir()
	hooksDir := filepath.Join(cwd, ".gcode")
	if err := os.MkdirAll(hooksDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	body := `{
		"hooks": {
			"PreToolUse": [{
				"matcher": "Bash",
				"hooks": [{
					"type": "command",
					"command": "echo unix",
					"commandWindows": "echo windows",
					"timeout": 3,
					"statusMessage": "checking"
				}]
			}]
		}
	}`
	sourcePath := filepath.Join(hooksDir, "hooks.json")
	if err := os.WriteFile(sourcePath, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	service := NewHookDiscoveryService("")
	response := service.Discover(&HookListParams{CWDs: []string{cwd}}, "")
	if len(response.Data) != 1 || len(response.Data[0].Hooks) != 1 {
		t.Fatalf("Discover() = %+v", response)
	}
	hook := response.Data[0].Hooks[0]
	if hook.EventName != HookEventPreToolUse || hook.HandlerType != HookHandlerCommand {
		t.Fatalf("hook event/type = %+v", hook)
	}
	if hook.Matcher == nil || *hook.Matcher != "Bash" {
		t.Fatalf("matcher = %+v", hook.Matcher)
	}
	wantCommand := "echo unix"
	if runtime.GOOS == "windows" {
		wantCommand = "echo windows"
	}
	if hook.Command == nil || *hook.Command != wantCommand {
		t.Fatalf("command = %+v, want %q", hook.Command, wantCommand)
	}
	if hook.TimeoutSec != 3 || hook.StatusMessage == nil || *hook.StatusMessage != "checking" {
		t.Fatalf("timeout/status = %+v", hook)
	}
	absPath, err := filepath.Abs(sourcePath)
	if err != nil {
		t.Fatalf("Abs() error = %v", err)
	}
	wantKey := "file:" + absPath + ":pre_tool_use:0:0"
	if hook.Key != wantKey || hook.SourcePath != absPath || hook.Source != HookSourceProject {
		t.Fatalf("source/key = %+v, want key %q path %q", hook, wantKey, absPath)
	}
	if !hook.Enabled || hook.IsManaged || hook.TrustStatus != HookTrustUntrusted || !strings.HasPrefix(hook.CurrentHash, "sha256:") {
		t.Fatalf("state/hash = %+v", hook)
	}

	enabled := false
	service.States = map[string]*HookState{
		hook.Key: {Enabled: &enabled, TrustedHash: &hook.CurrentHash},
	}
	response = service.Discover(&HookListParams{CWDs: []string{cwd}}, "")
	hook = response.Data[0].Hooks[0]
	if hook.Enabled || hook.TrustStatus != HookTrustTrusted {
		t.Fatalf("state-applied hook = %+v", hook)
	}
}

func TestHookDiscoveryAppendsManagedRequirementHooks(t *testing.T) {
	service := NewHookDiscoveryService("")
	cfg := config.NewConfigService(t.TempDir())
	managedDir := t.TempDir()
	cfg.SetRequirements(&config.ConfigRequirements{Hooks: &config.ManagedHooksRequirements{
		ManagedDir: &managedDir,
		PreToolUse: []config.ConfiguredHookGroup{{Matcher: stringPtr("Bash"), Hooks: []config.ConfiguredHookHandler{{Type: "command", Command: "echo managed"}}}},
	}})
	service.Config = cfg
	response := service.Discover(&HookListParams{CWDs: []string{t.TempDir()}}, "")
	if len(response.Data) != 1 {
		t.Fatalf("discovery response = %#v", response.Data)
	}
	entry := response.Data[0]
	var found bool
	for _, hook := range entry.Hooks {
		if hook.IsManaged && hook.Source == HookSourceCloudRequirements && hook.Command != nil && *hook.Command == "echo managed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("managed hook not appended: %#v", entry.Hooks)
	}
}

func TestHookDiscoveryRequiredLoadErrorsForUnsupportedManagedHook(t *testing.T) {
	service := NewHookDiscoveryService("")
	cfg := config.NewConfigService(t.TempDir())
	cfg.SetRequirements(&config.ConfigRequirements{Hooks: &config.ManagedHooksRequirements{
		PreToolUse: []config.ConfiguredHookGroup{{Hooks: []config.ConfiguredHookHandler{{Type: "prompt"}}}},
	}})
	service.Config = cfg
	response := service.Discover(&HookListParams{CWDs: []string{t.TempDir()}}, "")
	if len(response.Data) != 1 || len(response.Data[0].RequiredLoadErrors) == 0 {
		t.Fatalf("required load errors = %#v", response.Data)
	}
	if !strings.Contains(response.Data[0].RequiredLoadErrors[0], "unsupported managed hook prompt") {
		t.Fatalf("required load error = %#v", response.Data[0].RequiredLoadErrors)
	}
}

func TestHookDiscoveryRequiredLoadErrorsForEmptyManagedCommand(t *testing.T) {
	service := NewHookDiscoveryService("")
	cfg := config.NewConfigService(t.TempDir())
	cfg.SetRequirements(&config.ConfigRequirements{Hooks: &config.ManagedHooksRequirements{
		PreToolUse: []config.ConfiguredHookGroup{{Hooks: []config.ConfiguredHookHandler{{Type: "command", Command: ""}}}},
	}})
	service.Config = cfg
	response := service.Discover(&HookListParams{CWDs: []string{t.TempDir()}}, "")
	if len(response.Data) != 1 || len(response.Data[0].RequiredLoadErrors) == 0 || !strings.Contains(response.Data[0].RequiredLoadErrors[0], "empty hook command") {
		t.Fatalf("required load errors = %#v", response.Data)
	}
}

func TestHookDiscoveryManagedRequirementsWithoutConfigAreNoop(t *testing.T) {
	service := NewHookDiscoveryService("")
	response := service.Discover(&HookListParams{CWDs: []string{t.TempDir()}}, "")
	if len(response.Data) != 1 || len(response.Data[0].Hooks) != 0 || len(response.Data[0].RequiredLoadErrors) != 0 {
		t.Fatalf("managed requirements should be a no-op without config: %#v", response.Data)
	}
}

func TestHookDiscoveryManagedRequirementsSkippedWhenFeatureDisabled(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("[features]\nhooks = false\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config error = %v", err)
	}
	cfg := config.NewConfigService(home)
	cfg.SetRequirements(&config.ConfigRequirements{Hooks: &config.ManagedHooksRequirements{
		PreToolUse: []config.ConfiguredHookGroup{{Hooks: []config.ConfiguredHookHandler{{Type: "prompt"}}}},
	}})
	service := &HookDiscoveryService{CodexHome: home, Config: cfg}
	response := service.Discover(&HookListParams{CWDs: []string{t.TempDir()}}, "")
	if len(response.Data) != 1 || len(response.Data[0].RequiredLoadErrors) != 0 {
		t.Fatalf("managed requirements should be skipped while hooks feature disabled: %#v", response.Data)
	}
}

func TestManagedRequiredHookLoadErrorsHelper(t *testing.T) {
	service := NewHookDiscoveryService("")
	cfg := config.NewConfigService(t.TempDir())
	cfg.SetRequirements(&config.ConfigRequirements{Hooks: &config.ManagedHooksRequirements{
		PreToolUse: []config.ConfiguredHookGroup{{Hooks: []config.ConfiguredHookHandler{{Type: "prompt"}}}},
	}})
	service.Config = cfg
	errors := service.ManagedRequiredHookLoadErrors(t.TempDir())
	if len(errors) != 1 || !strings.Contains(errors[0], "unsupported managed hook prompt") {
		t.Fatalf("required load errors = %#v", errors)
	}
}

func TestHookDiscoveryWarnsWhenMcpToolHooksUnavailable(t *testing.T) {
	cwd := t.TempDir()
	hooksDir := filepath.Join(cwd, ".gcode")
	if err := os.MkdirAll(hooksDir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"hooks":{"PreToolUse":[{"hooks":[{"type":"mcp_tool","server":"linear","tool":"get_issue","input":{"id":"ENG-1"}}]}]}}`
	if err := os.WriteFile(filepath.Join(hooksDir, "hooks.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	response := NewHookDiscoveryService("").Discover(&HookListParams{CWDs: []string{cwd}}, "")
	if len(response.Data) != 1 || len(response.Data[0].Hooks) != 0 || !warningsContain(response.Data[0].Warnings, "MCP invocation is not available yet") {
		t.Fatalf("MCP tool hook discovery = %+v", response)
	}
}

func TestHookDiscoveryListsMcpToolHooksWhenEnabled(t *testing.T) {
	cwd := t.TempDir()
	hooksDir := filepath.Join(cwd, ".gcode")
	if err := os.MkdirAll(hooksDir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"hooks":{
		"PreToolUse":[{"matcher":"linear.get_issue","hooks":[{"type":"mcp_tool","server":"linear","tool":"get_issue","input":{"issue_id":"${tool_input.id}","label":"issue-${tool_input.id}"}}]}],
		"SessionEnd":[{"hooks":[{"type":"mcp_tool","server":"linear","tool":"ping"}]}]
	}}`
	if err := os.WriteFile(filepath.Join(hooksDir, "hooks.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	service := NewHookDiscoveryService("")
	service.McpToolHooksEnabled = true
	response := service.Discover(&HookListParams{CWDs: []string{cwd}}, "")
	if len(response.Data) != 1 {
		t.Fatalf("Discover() = %+v", response)
	}
	entry := response.Data[0]
	if len(entry.Hooks) != 1 {
		t.Fatalf("hooks = %+v, want the PreToolUse mcp_tool hook only (SessionEnd must be skipped)", entry.Hooks)
	}
	hook := entry.Hooks[0]
	if hook.HandlerType != HookHandlerMCPTool || hook.Server == nil || *hook.Server != "linear" || hook.Tool == nil || *hook.Tool != "get_issue" {
		t.Fatalf("mcp_tool hook = %+v", hook)
	}
	if hook.Input["issue_id"] != "${tool_input.id}" || hook.Input["label"] != "issue-${tool_input.id}" {
		t.Fatalf("argument template = %#v", hook.Input)
	}
	if hook.EventName != HookEventPreToolUse || hook.ExecutionMode != HookExecutionSync || !strings.HasPrefix(hook.CurrentHash, "sha256:") {
		t.Fatalf("mcp_tool hook metadata = %+v", hook)
	}
	if !warningsContain(entry.Warnings, "SessionEnd") {
		t.Fatalf("warnings = %#v, want SessionEnd MCP tool hook warning", entry.Warnings)
	}
}

func TestHookDiscoveryReflectsPluginHookChangesLikeRust(t *testing.T) {
	pluginA := t.TempDir()
	pluginB := t.TempDir()
	writePluginHook := func(t *testing.T, root string, command string) string {
		t.Helper()
		hooksDir := filepath.Join(root, "hooks")
		if err := os.MkdirAll(hooksDir, 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(hooksDir, "hooks.json")
		body := `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"` + command + `"}]}]}}`
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	firstPath := writePluginHook(t, pluginA, "echo first")
	service := NewHookDiscoveryService("")
	service.PluginHookSources = []plugin.HookSource{{
		PluginID:           "plugin-a",
		PluginRoot:         pluginA,
		SourcePath:         firstPath,
		SourceRelativePath: "hooks/hooks.json",
	}}
	cwd := t.TempDir()
	first := service.Discover(&HookListParams{CWDs: []string{cwd}}, "")
	if len(first.Data) != 1 || len(first.Data[0].Hooks) != 1 || *first.Data[0].Hooks[0].Command != "echo first" {
		t.Fatalf("first discovery = %+v", first.Data)
	}

	// Plugin changes refresh the discovered hook runtimes: no stale cache from
	// the previous plugin source (Rust #38703 effective_plugin_change refresh).
	secondPath := writePluginHook(t, pluginB, "echo second")
	service.PluginHookSources = []plugin.HookSource{{
		PluginID:           "plugin-b",
		PluginRoot:         pluginB,
		SourcePath:         secondPath,
		SourceRelativePath: "hooks/hooks.json",
	}}
	second := service.Discover(&HookListParams{CWDs: []string{cwd}}, "")
	if len(second.Data) != 1 || len(second.Data[0].Hooks) != 1 || *second.Data[0].Hooks[0].Command != "echo second" {
		t.Fatalf("second discovery = %+v, want refreshed plugin hook", second.Data)
	}
	if *second.Data[0].Hooks[0].PluginID != "plugin-b" {
		t.Fatalf("plugin id = %q, want plugin-b", *second.Data[0].Hooks[0].PluginID)
	}
}

func TestHookDiscoveryLoadsUserHooksJSON(t *testing.T) {
	home := t.TempDir()
	body := `{
		"hooks": {
			"SessionStart": [{
				"matcher": "startup",
				"hooks": [{"type": "command", "command": "echo hi", "timeoutSec": 1}]
			}]
		}
	}`
	if err := os.WriteFile(filepath.Join(home, "hooks.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cwd := t.TempDir()
	response := NewHookDiscoveryService(home).Discover(&HookListParams{CWDs: []string{cwd}}, "")
	if len(response.Data) != 1 || len(response.Data[0].Hooks) != 1 {
		t.Fatalf("Discover() = %+v", response)
	}
	hook := response.Data[0].Hooks[0]
	if response.Data[0].CWD != cwd || hook.Source != HookSourceUser || hook.Matcher == nil || *hook.Matcher != "startup" {
		t.Fatalf("user hook = %+v", response.Data[0])
	}
}

func TestHookDiscoveryLoadsUserHooksConfigTOML(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	body := `[hooks]

[[hooks.PreToolUse]]
matcher = "Bash"

[[hooks.PreToolUse.hooks]]
type = "command"
command = "python3 /tmp/listed-hook.py"
timeout = 5
statusMessage = "running listed hook"
`
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	response := NewHookDiscoveryService(home).Discover(&HookListParams{CWDs: []string{cwd}}, "")
	if len(response.Data) != 1 || len(response.Data[0].Hooks) != 1 {
		t.Fatalf("Discover() = %+v", response)
	}
	hook := response.Data[0].Hooks[0]
	sourcePath, err := filepath.Abs(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatalf("Abs() error = %v", err)
	}
	wantKey := sourcePath + ":pre_tool_use:0:0"
	if hook.Key != wantKey || hook.SourcePath != sourcePath || hook.Source != HookSourceUser {
		t.Fatalf("hook source/key = %+v, want key %q path %q", hook, wantKey, sourcePath)
	}
	if hook.Matcher == nil || *hook.Matcher != "Bash" || hook.Command == nil || *hook.Command != "python3 /tmp/listed-hook.py" {
		t.Fatalf("hook matcher/command = %+v", hook)
	}
	if hook.TimeoutSec != 5 || hook.StatusMessage == nil || *hook.StatusMessage != "running listed hook" {
		t.Fatalf("hook timeout/status = %+v", hook)
	}
	if hook.CurrentHash != hookDiscoveryHash(HookEventPreToolUse, hook.Matcher, *hook.Command, false, 5, hook.StatusMessage, nil) {
		t.Fatalf("hash = %q, want normalized hook hash", hook.CurrentHash)
	}
}

func TestHookDiscoveryReturnsEmptyEntryForRequestedCWD(t *testing.T) {
	cwd := t.TempDir()
	response := NewHookDiscoveryService("").Discover(&HookListParams{CWDs: []string{cwd}}, "")
	if len(response.Data) != 1 {
		t.Fatalf("Discover() = %+v, want one empty entry", response)
	}
	entry := response.Data[0]
	if entry.CWD != cwd || len(entry.Hooks) != 0 || len(entry.Warnings) != 0 || len(entry.Errors) != 0 {
		t.Fatalf("entry = %+v, want empty hooks entry for cwd", entry)
	}
}

func TestHookDiscoveryUsesTrustedProjectConfigLayers(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	projectTrust := strings.ReplaceAll(filepath.Clean(cwd), `\`, `\\`)
	if err := os.WriteFile(config.ConfigPath(home), []byte("[projects.\""+projectTrust+"\"]\ntrust_level = \"trusted\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config error = %v", err)
	}
	hooksDir := filepath.Join(cwd, ".gcode")
	if err := os.MkdirAll(hooksDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(hooksDir, "config.toml"), []byte("model = \"gpt-project\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile project config error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(hooksDir, "hooks.json"), []byte(`{
		"hooks": {
			"PostToolUse": [{"hooks": [{"type": "command", "command": "echo trusted"}]}]
		}
	}`), 0o600); err != nil {
		t.Fatalf("WriteFile hooks error = %v", err)
	}
	service := &HookDiscoveryService{CodexHome: home, Config: config.NewConfigService(home)}

	response := service.Discover(&HookListParams{CWDs: []string{cwd}}, "")
	if len(response.Data) != 1 || len(response.Data[0].Hooks) != 1 {
		t.Fatalf("Discover() = %+v", response)
	}
	hook := response.Data[0].Hooks[0]
	if hook.Source != HookSourceProject || hook.Command == nil || *hook.Command != "echo trusted" {
		t.Fatalf("hook = %+v", hook)
	}
}

func TestHookDiscoveryUsesEachCWDEffectiveFeatureEnablementLikeRust(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("[features]\nhooks = false\n\n[projects.\""+strings.ReplaceAll(filepath.Clean(workspace), `\`, `\\`)+"\"]\ntrust_level = \"trusted\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config error = %v", err)
	}
	hooksDir := filepath.Join(workspace, ".gcode")
	if err := os.MkdirAll(hooksDir, 0o700); err != nil {
		t.Fatalf("MkdirAll hooks dir error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(hooksDir, "config.toml"), []byte(`[features]
hooks = true

[hooks]

[[hooks.PreToolUse]]
matcher = "Bash"

[[hooks.PreToolUse.hooks]]
type = "command"
command = "echo project hook"
timeout = 5
`), 0o600); err != nil {
		t.Fatalf("WriteFile project config error = %v", err)
	}
	service := &HookDiscoveryService{CodexHome: home, Config: config.NewConfigService(home)}

	response := service.Discover(&HookListParams{CWDs: []string{home, workspace}}, "")
	if len(response.Data) != 2 {
		t.Fatalf("Discover() = %+v", response)
	}
	if response.Data[0].CWD != home || len(response.Data[0].Hooks) != 0 {
		t.Fatalf("home entry = %+v, want hooks disabled", response.Data[0])
	}
	if response.Data[1].CWD != workspace || len(response.Data[1].Hooks) != 1 {
		t.Fatalf("workspace entry = %+v, want project hook", response.Data[1])
	}
	hook := response.Data[1].Hooks[0]
	if hook.Source != HookSourceProject || hook.Matcher == nil || *hook.Matcher != "Bash" || hook.Command == nil || *hook.Command != "echo project hook" {
		t.Fatalf("workspace hook = %+v", hook)
	}
}

func TestHookDiscoveryUsesTrustedProjectDotCodexWithoutConfigToml(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	projectTrust := strings.ReplaceAll(filepath.Clean(cwd), `\`, `\\`)
	if err := os.WriteFile(config.ConfigPath(home), []byte("[projects.\""+projectTrust+"\"]\ntrust_level = \"trusted\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config error = %v", err)
	}
	hooksDir := filepath.Join(cwd, ".gcode")
	if err := os.MkdirAll(hooksDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(hooksDir, "hooks.json"), []byte(`{
		"hooks": {
			"PostToolUse": [{"hooks": [{"type": "command", "command": "echo hooks only"}]}]
		}
	}`), 0o600); err != nil {
		t.Fatalf("WriteFile hooks error = %v", err)
	}
	service := &HookDiscoveryService{CodexHome: home, Config: config.NewConfigService(home)}

	response := service.Discover(&HookListParams{CWDs: []string{cwd}}, "")
	if len(response.Data) != 1 || len(response.Data[0].Hooks) != 1 {
		t.Fatalf("Discover() = %+v", response)
	}
	hook := response.Data[0].Hooks[0]
	if hook.Source != HookSourceProject || hook.Command == nil || *hook.Command != "echo hooks only" {
		t.Fatalf("hook = %+v", hook)
	}
}

func TestHookDiscoveryLinkedWorktreeUsesRootCheckoutHooks(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(t.TempDir(), "repo")
	worktree := filepath.Join(t.TempDir(), "worktree")
	if err := os.MkdirAll(filepath.Join(root, ".git", "worktrees", "feature"), 0o755); err != nil {
		t.Fatalf("MkdirAll root gitdir error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".gcode"), 0o700); err != nil {
		t.Fatalf("MkdirAll root .gcode error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(worktree, ".gcode"), 0o700); err != nil {
		t.Fatalf("MkdirAll worktree .gcode error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+filepath.Join(root, ".git", "worktrees", "feature")+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile worktree .git error = %v", err)
	}
	worktreeGitDir := filepath.Join(root, ".git", "worktrees", "feature")
	if err := os.WriteFile(filepath.Join(worktreeGitDir, "gitdir"), []byte(filepath.Join(worktree, ".git")+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile worktree gitdir backlink error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktreeGitDir, "commondir"), []byte("../..\n"), 0o600); err != nil {
		t.Fatalf("WriteFile worktree commondir error = %v", err)
	}
	projectTrust := strings.ReplaceAll(filepath.Clean(root), `\`, `\\`)
	if err := os.WriteFile(config.ConfigPath(home), []byte("[projects.\""+projectTrust+"\"]\ntrust_level = \"trusted\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gcode", "hooks.json"), []byte(`{
		"hooks": {
			"PostToolUse": [{"hooks": [{"type": "command", "command": "echo root checkout"}]}]
		}
	}`), 0o600); err != nil {
		t.Fatalf("WriteFile root hooks error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktree, ".gcode", "hooks.json"), []byte(`{
		"hooks": {
			"PostToolUse": [{"hooks": [{"type": "command", "command": "echo worktree local"}]}]
		}
	}`), 0o600); err != nil {
		t.Fatalf("WriteFile worktree hooks error = %v", err)
	}
	service := &HookDiscoveryService{CodexHome: home, Config: config.NewConfigService(home)}

	response := service.Discover(&HookListParams{CWDs: []string{worktree}}, "")
	if len(response.Data) != 1 || len(response.Data[0].Hooks) != 1 {
		t.Fatalf("Discover() = %+v", response)
	}
	hook := response.Data[0].Hooks[0]
	if hook.Command == nil || *hook.Command != "echo root checkout" {
		t.Fatalf("hook = %+v, want root checkout hook", hook)
	}
	if !strings.Contains(filepath.Clean(hook.SourcePath), filepath.Clean(filepath.Join(root, ".gcode"))) {
		t.Fatalf("source path = %q, want root checkout .gcode", hook.SourcePath)
	}
}

func TestHookDiscoverySkipsUntrustedProjectHooksWhenConfigServicePresent(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("model = \"gpt-user\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config error = %v", err)
	}
	cwd := t.TempDir()
	hooksDir := filepath.Join(cwd, ".gcode")
	if err := os.MkdirAll(hooksDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(hooksDir, "hooks.json"), []byte(`{
		"hooks": {
			"PostToolUse": [{"hooks": [{"type": "command", "command": "echo untrusted"}]}]
		}
	}`), 0o600); err != nil {
		t.Fatalf("WriteFile hooks error = %v", err)
	}
	service := &HookDiscoveryService{CodexHome: home, Config: config.NewConfigService(home)}

	response := service.Discover(&HookListParams{CWDs: []string{cwd}}, "")
	if len(response.Data) != 1 || response.Data[0].CWD != cwd || len(response.Data[0].Hooks) != 0 {
		t.Fatalf("Discover() = %+v, want empty project hook entry", response)
	}
}

func TestHookDiscoveryWarningsForUnsupportedHandlers(t *testing.T) {
	cwd := t.TempDir()
	hooksDir := filepath.Join(cwd, ".gcode")
	if err := os.MkdirAll(hooksDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	body := `{
		"hooks": {
			"PreToolUse": [{
				"hooks": [
					{"type": "command", "command": "echo async", "async": true},
					{"type": "command", "command": "   "},
					{"type": "prompt"},
					{"type": "agent"},
					{"type": "other"}
				]
			}],
			"MadeUp": [{"hooks": [{"type": "command", "command": "echo nope"}]}]
		}
	}`
	if err := os.WriteFile(filepath.Join(hooksDir, "hooks.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	response := NewHookDiscoveryService("").Discover(&HookListParams{CWDs: []string{cwd}}, "")
	if len(response.Data) != 1 {
		t.Fatalf("Discover() = %+v", response)
	}
	entry := response.Data[0]
	if len(entry.Hooks) != 1 || entry.Hooks[0].ExecutionMode != HookExecutionAsync {
		t.Fatalf("hooks = %+v, want one async hook", entry.Hooks)
	}
	for _, want := range []string{"empty hook command", "prompt hook", "agent hook", "unsupported hook handler", "unsupported hook event"} {
		if !warningsContain(entry.Warnings, want) {
			t.Fatalf("warnings = %+v, want substring %q", entry.Warnings, want)
		}
	}
}

func TestRuntimeRouterHooksListMergesRegistryAndDiscovery(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	hooksDir := filepath.Join(cwd, ".gcode")
	if err := os.MkdirAll(hooksDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(hooksDir, "hooks.json"), []byte(`{
		"hooks": {
			"PostToolUse": [{"hooks": [{"type": "command", "command": "echo after"}]}]
		}
	}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		DefaultCWD:     cwd,
		Hooks:          NewHookRegistry(),
		HooksDiscovery: NewHookDiscoveryService(home),
	})
	if err := router.services.Hooks.Add(cwd, sampleMetadata("manual", 20)); err != nil {
		t.Fatalf("Hooks.Add() error = %v", err)
	}

	response := router.Handle(requestWithParams(t, IntID(1), MethodHooksList, HookListParams{}))
	if response.Error != nil {
		t.Fatalf("hooks/list error = %+v", response.Error)
	}
	result := response.Result.(*HookListResponse)
	if len(result.Data) != 1 || len(result.Data[0].Hooks) != 2 {
		t.Fatalf("hooks/list = %+v", result)
	}
	if result.Data[0].Hooks[0].Key == result.Data[0].Hooks[1].Key {
		t.Fatalf("expected distinct hooks, got %+v", result.Data[0].Hooks)
	}
	for _, hook := range result.Data[0].Hooks {
		if hook.ExecutionMode != HookExecutionSync {
			t.Fatalf("hooks/list executionMode = %q, want sync (Rust 3aae5d885b)", hook.ExecutionMode)
		}
	}
}

func TestRuntimeRouterHooksListIncludesPluginHooks(t *testing.T) {
	cwd := t.TempDir()
	pluginRoot := t.TempDir()
	hooksDir := filepath.Join(pluginRoot, "hooks")
	if err := os.MkdirAll(hooksDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(hooksDir, "hooks.json"), []byte(`{
		"hooks": {
			"SessionStart": [{"hooks": [{"type": "command", "command": "echo ${PLUGIN_ROOT}", "timeout": 0}]}]
		}
	}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	plugins := plugin.NewPluginService()
	plugins.AddPlugin(plugin.PluginDetail{
		Summary:         plugin.PluginSummary{Name: "sample", MarketplaceName: "local", Installed: true, Enabled: true},
		Hooks:           []plugin.PluginHookSummary{{Key: "hook-1", Enabled: true}},
		MarketplaceRoot: pluginRoot,
	})
	router := NewRuntimeRouter(RuntimeServices{
		DefaultCWD:     cwd,
		Plugins:        plugins,
		HooksDiscovery: NewHookDiscoveryService(""),
	})

	response := router.Handle(requestWithParams(t, IntID(1), MethodHooksList, HookListParams{}))
	if response.Error != nil {
		t.Fatalf("hooks/list error = %+v", response.Error)
	}
	result := response.Result.(*HookListResponse)
	if len(result.Data) != 1 || len(result.Data[0].Hooks) != 1 {
		t.Fatalf("hooks/list = %+v", result)
	}
	hook := result.Data[0].Hooks[0]
	if hook.Source != HookSourcePlugin || hook.PluginID == nil || *hook.PluginID != "sample@local" {
		t.Fatalf("plugin hook identity = %+v", hook)
	}
	wantKey := "sample@local:hooks/hooks.json:session_start:0:0"
	if hook.Key != wantKey || hook.Command == nil || *hook.Command != "echo "+pluginRoot {
		t.Fatalf("plugin hook = %+v, want key %q expanded command", hook, wantKey)
	}
	if hook.TimeoutSec != 1 {
		t.Fatalf("timeout = %d, want Rust minimum of 1", hook.TimeoutSec)
	}
	if !strings.HasPrefix(hook.CurrentHash, "sha256:") {
		t.Fatalf("hash = %q", hook.CurrentHash)
	}
}

func TestRuntimeRouterHooksListAppliesConfigHookState(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	projectTrust := strings.ReplaceAll(filepath.Clean(cwd), `\`, `\\`)
	if err := os.WriteFile(config.ConfigPath(home), []byte("[projects.\""+projectTrust+"\"]\ntrust_level = \"trusted\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config error = %v", err)
	}
	hooksDir := filepath.Join(cwd, ".gcode")
	if err := os.MkdirAll(hooksDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(hooksDir, "hooks.json"), []byte(`{
		"hooks": {
			"PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "echo before"}]}]
		}
	}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	configService := config.NewConfigService(home)
	router := NewRuntimeRouter(RuntimeServices{
		DefaultCWD:     cwd,
		Config:         configService,
		HooksDiscovery: NewHookDiscoveryService(home),
	})

	first := router.Handle(requestWithParams(t, IntID(1), MethodHooksList, HookListParams{}))
	if first.Error != nil {
		t.Fatalf("first hooks/list error = %+v", first.Error)
	}
	hook := first.Result.(*HookListResponse).Data[0].Hooks[0]
	if hook.TrustStatus != HookTrustUntrusted {
		t.Fatalf("initial hook = %+v", hook)
	}
	if _, err := configService.BatchWrite(&config.ConfigBatchWriteParams{Edits: []config.ConfigEdit{{
		KeyPath: "hooks.state",
		Value: map[string]any{
			hook.Key: map[string]any{"trusted_hash": hook.CurrentHash},
		},
		MergeStrategy: config.MergeUpsert,
	}}}); err != nil {
		t.Fatalf("BatchWrite() error = %v", err)
	}

	second := router.Handle(requestWithParams(t, IntID(2), MethodHooksList, HookListParams{}))
	if second.Error != nil {
		t.Fatalf("second hooks/list error = %+v", second.Error)
	}
	hook = second.Result.(*HookListResponse).Data[0].Hooks[0]
	if hook.TrustStatus != HookTrustTrusted {
		t.Fatalf("trusted hook = %+v", hook)
	}

	if _, err := configService.BatchWrite(&config.ConfigBatchWriteParams{Edits: []config.ConfigEdit{{
		KeyPath: "hooks.state",
		Value: map[string]any{
			hook.Key: map[string]any{"enabled": false, "trusted_hash": hook.CurrentHash},
		},
		MergeStrategy: config.MergeUpsert,
	}}}); err != nil {
		t.Fatalf("BatchWrite(disabled) error = %v", err)
	}
	third := router.Handle(requestWithParams(t, IntID(3), MethodHooksList, HookListParams{}))
	if third.Error != nil {
		t.Fatalf("third hooks/list error = %+v", third.Error)
	}
	hook = third.Result.(*HookListResponse).Data[0].Hooks[0]
	if hook.Enabled || hook.TrustStatus != HookTrustTrusted {
		t.Fatalf("disabled trusted hook = %+v", hook)
	}
}

func TestRuntimeRouterBypassHookTrustKeepsStatusAndMarksExecutionBypass(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	projectTrust := strings.ReplaceAll(filepath.Clean(cwd), `\`, `\\`)
	if err := os.WriteFile(config.ConfigPath(home), []byte("bypass_hook_trust = true\n[projects.\""+projectTrust+"\"]\ntrust_level = \"trusted\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config error = %v", err)
	}
	hooksDir := filepath.Join(cwd, ".gcode")
	if err := os.MkdirAll(hooksDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(hooksDir, "hooks.json"), []byte(`{
		"hooks": {
			"PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "echo bypass"}]}]
		}
	}`), 0o600); err != nil {
		t.Fatalf("WriteFile hooks error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		DefaultCWD:     cwd,
		Config:         config.NewConfigService(home),
		HooksDiscovery: NewHookDiscoveryService(home),
		HookRunner:     NewHookRunner(),
	})

	response := router.Handle(requestWithParams(t, IntID(1), MethodHooksList, HookListParams{}))
	if response.Error != nil {
		t.Fatalf("hooks/list error = %+v", response.Error)
	}
	hook := response.Result.(*HookListResponse).Data[0].Hooks[0]
	if hook.TrustStatus != HookTrustUntrusted {
		t.Fatalf("hook status = %+v, want untrusted display status", hook)
	}
	adapter, ok := router.turnHookAdapter(&turn.TurnStartParams{ThreadID: "thread-1", CWD: cwd}, "turn-1").(*ToolHookAdapter)
	if !ok || adapter == nil || len(adapter.Hooks) != 1 || !adapter.Hooks[0].BypassTrust {
		t.Fatalf("adapter hooks = %+v", adapter)
	}
}

func warningsContain(warnings []string, value string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, value) {
			return true
		}
	}
	return false
}
func TestHookDiscoveryParsesAdditionalContextLimitFromJSON(t *testing.T) {
	// Mirrors Rust hooks_tests.rs hooks_file_deserializes_existing_json_shape:
	// additionalContextLimit is parsed from hooks.json and surfaced on the
	// v2 hooks_list contract.
	cwd := t.TempDir()
	hooksDir := filepath.Join(cwd, ".gcode")
	if err := os.MkdirAll(hooksDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	body := `{
		"description": "Optional stop-time review gate for Codex Companion.",
		"hooks": {
			"PreToolUse": [{
				"matcher": "^Bash$",
				"hooks": [{
					"type": "command",
					"command": "python3 /tmp/pre.py",
					"timeout": 10,
					"statusMessage": "checking",
					"additionalContextLimit": 4096
				}]
			}]
		}
	}`
	sourcePath := filepath.Join(hooksDir, "hooks.json")
	if err := os.WriteFile(sourcePath, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	service := NewHookDiscoveryService("")
	response := service.Discover(&HookListParams{CWDs: []string{cwd}}, "")
	if len(response.Data) != 1 || len(response.Data[0].Hooks) != 1 {
		t.Fatalf("Discover() = %+v", response)
	}
	hook := response.Data[0].Hooks[0]
	if hook.AdditionalContextLimit == nil || *hook.AdditionalContextLimit != 4096 {
		t.Fatalf("additionalContextLimit = %#v, want 4096", hook.AdditionalContextLimit)
	}
	// The hash must include the limit (Rust NormalizedHookIdentity), so a hook
	// with a different limit hashes differently.
	withLimit := hookDiscoveryHash(HookEventPreToolUse, hook.Matcher, *hook.Command, false, hook.TimeoutSec, hook.StatusMessage, hook.AdditionalContextLimit)
	limit := int64(1)
	otherLimit := hookDiscoveryHash(HookEventPreToolUse, hook.Matcher, *hook.Command, false, hook.TimeoutSec, hook.StatusMessage, &limit)
	if withLimit == otherLimit {
		t.Fatal("additionalContextLimit must participate in the hook identity hash")
	}
}

func TestHookDiscoveryParsesAdditionalContextLimitFromTOML(t *testing.T) {
	// Mirrors Rust hooks_tests.rs hook_events_deserialize_from_toml_arrays_of_
	// tables: additionalContextLimit is accepted in TOML handler tables under
	// the user config layer.
	home := t.TempDir()
	body := `
[[hooks.PreToolUse]]
matcher = "^Bash$"

[[hooks.PreToolUse.hooks]]
type = "command"
command = "python3 /tmp/pre.py"
timeout = 10
statusMessage = "checking"
additionalContextLimit = 4096
`
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	service := NewHookDiscoveryService(home)
	response := service.Discover(&HookListParams{}, "")
	if len(response.Data) != 1 || len(response.Data[0].Hooks) != 1 {
		t.Fatalf("Discover() = %+v", response)
	}
	hook := response.Data[0].Hooks[0]
	if hook.AdditionalContextLimit == nil || *hook.AdditionalContextLimit != 4096 {
		t.Fatalf("additionalContextLimit = %#v, want 4096", hook.AdditionalContextLimit)
	}
}

func TestHookMetadataMarshalOmitsUnsetAdditionalContextLimitLikeRust(t *testing.T) {
	// Mirrors Rust hooks_tests.rs hook_handler_omits_unset_additional_context_
	// limit: the field is omitted from JSON when unset.
	metadata := HookMetadata{
		Key:           "k",
		EventName:     HookEventPreToolUse,
		HandlerType:   HookHandlerCommand,
		ExecutionMode: HookExecutionSync,
		TimeoutSec:    1,
		SourcePath:    "/tmp/hooks.json",
		Source:        HookSourceUser,
	}
	data, err := json.Marshal(&metadata)
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}
	if strings.Contains(string(data), "additionalContextLimit") {
		t.Fatalf("unset additionalContextLimit leaked into JSON: %s", data)
	}
	metadata.AdditionalContextLimit = int64Ptr(2500)
	data, err = json.Marshal(&metadata)
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}
	if !strings.Contains(string(data), `"additionalContextLimit":2500`) {
		t.Fatalf("set additionalContextLimit missing from JSON: %s", data)
	}
}

func int64Ptr(value int64) *int64 {
	return &value
}

// TestHookDiscoveryCompilesRegexMatcherLikeRust mirrors Rust #49379: a regex
// matcher is compiled once during discovery and reused on dispatch, while
// match-all and exact-name matchers carry no compiled regex.
func TestHookDiscoveryCompilesRegexMatcherLikeRust(t *testing.T) {
	cwd := t.TempDir()
	hooksDir := filepath.Join(cwd, ".gcode")
	if err := os.MkdirAll(hooksDir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{
		"hooks": {
			"PreToolUse": [
				{"matcher": "^Bash$", "hooks": [{"type": "command", "command": "echo regex"}]},
				{"matcher": "Bash", "hooks": [{"type": "command", "command": "echo exact"}]},
				{"matcher": "*", "hooks": [{"type": "command", "command": "echo all"}]}
			]
		}
	}`
	if err := os.WriteFile(filepath.Join(hooksDir, "hooks.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	service := NewHookDiscoveryService("")
	response := service.Discover(&HookListParams{CWDs: []string{cwd}}, "")
	if len(response.Data) != 1 || len(response.Data[0].Hooks) != 3 {
		t.Fatalf("Discover() = %+v", response)
	}
	byCommand := map[string]HookMetadata{}
	for _, hook := range response.Data[0].Hooks {
		byCommand[ptrStringValue(hook.Command)] = hook
	}
	regexHook, ok := byCommand["echo regex"]
	if !ok || regexHook.compiledMatcher == nil {
		t.Fatalf("regex matcher was not compiled during discovery: %#v", byCommand)
	}
	if !hookMatches(HookEventPreToolUse, regexHook.Matcher, regexHook.compiledMatcher, []string{"Bash"}) {
		t.Fatal("compiled matcher did not match Bash")
	}
	if hookMatches(HookEventPreToolUse, regexHook.Matcher, regexHook.compiledMatcher, []string{"Read"}) {
		t.Fatal("compiled matcher matched Read")
	}
	for _, command := range []string{"echo exact", "echo all"} {
		hook, ok := byCommand[command]
		if !ok || hook.compiledMatcher != nil {
			t.Fatalf("matcher %q should not carry a compiled regex: %#v", command, byCommand[command])
		}
	}
}

// TestHookTrustHashMatchesRustCanonicalFingerprint pins the hook trust identity to the
// Rust canonical fingerprint of #49295. Each expected hash was produced by running
// Rust's hook_hash (codex-rs/hooks/src/engine/discovery.rs) on the same hook definition,
// which serializes the normalized identity through codex_config::version_for_toml
// (codex-rs/config/src/fingerprint.rs) and hashes the canonical JSON with SHA-256:
//
//	{ event_name, matcher?, hooks: [handler] }
//
// The tables below cover the handler shapes and normalizations Rust applies before
// hashing: command vs mcp_tool handlers (only command handlers carry `async`), an
// omitted mcp_tool input (an empty table is still hashed), the matcher-less events, the
// SessionEnd/Interrupt timeout default and clamp, and the additionalContextLimit
// normalization.
// Rust hooks/src/events/common.rs::matcher_pattern_for_event (SessionEnd arm, added by
// #33895) keeps the configured matcher for SessionEnd, so the discovered entry reports
// it verbatim and dispatch can evaluate it against SESSION_END_REASON.
func TestDiscoveredSessionEndHookKeepsItsMatcher(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "hooks.json"), []byte(`{"hooks":{"SessionEnd":[{"matcher":"clear|other","hooks":[{"type":"command","command":"echo hi"}]}],"Stop":[{"matcher":"clear","hooks":[{"type":"command","command":"echo hi"}]}]}}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	service := &HookDiscoveryService{CodexHome: home}
	response := service.Discover(&HookListParams{CWDs: []string{t.TempDir()}}, "")
	if response == nil || len(response.Data) != 1 || len(response.Data[0].Hooks) != 2 {
		t.Fatalf("response = %+v", response)
	}
	hooks := map[HookEventName]*string{}
	for i := range response.Data[0].Hooks {
		hooks[response.Data[0].Hooks[i].EventName] = response.Data[0].Hooks[i].Matcher
	}
	if matcher := hooks[HookEventSessionEnd]; matcher == nil || *matcher != "clear|other" {
		t.Fatalf("SessionEnd matcher = %v, want \"clear|other\"", matcher)
	}
	if matcher := hooks[HookEventStop]; matcher != nil {
		t.Fatalf("Stop matcher = %v, want nil (matcher-less event)", matcher)
	}
}

func TestHookTrustHashMatchesRustCanonicalFingerprint(t *testing.T) {
	commandCases := []struct {
		name        string
		body        string
		wantHash    string
		wantTimeout int64
	}{
		{
			name: "command PreToolUse async with status and limit",
			body: `[hooks]

[[hooks.PreToolUse]]
matcher = "Bash"

[[hooks.PreToolUse.hooks]]
type = "command"
command = "python3 /tmp/listed-hook.py"
timeout = 5
async = true
statusMessage = "running listed hook"
additionalContextLimit = 4096
`,
			wantHash:    "sha256:77af324465e1bd97065037ee4fb25d0d8f60bd6aa701379f5a6749d96dbae897",
			wantTimeout: 5,
		},
		{
			name: "command PreToolUse default timeout",
			body: `[hooks]

[[hooks.PreToolUse]]
matcher = "Bash"

[[hooks.PreToolUse.hooks]]
type = "command"
command = "echo hi"
`,
			wantHash:    "sha256:3bbce6504b48cc6f0d7fe24bd36273633d1bfa5879bc87f4ed7ccd16f24d1415",
			wantTimeout: 600,
		},
		{
			name: "command PreToolUse limit equal to the 2500 default is normalized away",
			body: `[hooks]

[[hooks.PreToolUse]]
matcher = "Bash"

[[hooks.PreToolUse.hooks]]
type = "command"
command = "echo hi"
timeout = 600
additionalContextLimit = 2500
`,
			wantHash:    "sha256:3bbce6504b48cc6f0d7fe24bd36273633d1bfa5879bc87f4ed7ccd16f24d1415",
			wantTimeout: 600,
		},
		{
			name: "command SessionEnd default timeout is one second",
			body: `[hooks]

[[hooks.SessionEnd.hooks]]
type = "command"
command = "echo hi"
`,
			wantHash:    "sha256:c6e3af8b1627dcaa3a876a10d15499bd53226a6b955017999d0eaabae8e2fd46",
			wantTimeout: 1,
		},
		{
			name: "command SessionEnd keeps its matcher",
			body: `[hooks]

[[hooks.SessionEnd]]
matcher = "clear|other"

[[hooks.SessionEnd.hooks]]
type = "command"
command = "echo hi"
`,
			wantHash:    "sha256:57175c124df70b6856653dbd75f44f5bd038e4c622688d0ab699b2bd3b472b4d",
			wantTimeout: 1,
		},
		{
			name: "command SessionEnd timeout clamps to three seconds",
			body: `[hooks]

[[hooks.SessionEnd.hooks]]
type = "command"
command = "echo hi"
timeout = 600
`,
			wantHash:    "sha256:823aeab1cf10994923a9a4c0236d0b3c702cfbafc61158f8adceebe65399f28d",
			wantTimeout: 3,
		},
		{
			name: "command Interrupt drops its matcher",
			body: `[hooks]

[[hooks.Interrupt]]
matcher = "^interrupted$"

[[hooks.Interrupt.hooks]]
type = "command"
command = "echo hi"
`,
			wantHash:    "sha256:954a9910f8d7089fe12d362d16c1f0e39597fbb05e205a2ead31b938a2d25afa",
			wantTimeout: 1,
		},
		{
			name: "command Stop drops additionalContextLimit",
			body: `[hooks]

[[hooks.Stop]]
matcher = "Bash"

[[hooks.Stop.hooks]]
type = "command"
command = "echo hi"
timeout = 600
additionalContextLimit = 1000
`,
			wantHash:    "sha256:f2aa169c7a1cf9b7cc9b61ce5ecec0315e0f7d13ac35935b1aa74440f9a839f8",
			wantTimeout: 600,
		},
		{
			name: "command UserPromptSubmit drops its matcher",
			body: `[hooks]

[[hooks.UserPromptSubmit]]
matcher = "Bash"

[[hooks.UserPromptSubmit.hooks]]
type = "command"
command = "echo hi"
timeout = 600
`,
			wantHash:    "sha256:4ac11110e7e52a7ace4a63994f6a554e0c891264e3e3733d1f0541b1cd0b3b3e",
			wantTimeout: 600,
		},
	}
	for _, test := range commandCases {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			cwd := t.TempDir()
			if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(test.body), 0o600); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}
			response := NewHookDiscoveryService(home).Discover(&HookListParams{CWDs: []string{cwd}}, "")
			if len(response.Data) != 1 || len(response.Data[0].Hooks) != 1 {
				t.Fatalf("Discover() = %+v", response)
			}
			hook := response.Data[0].Hooks[0]
			if hook.CurrentHash != test.wantHash {
				t.Fatalf("CurrentHash = %s, want %s", hook.CurrentHash, test.wantHash)
			}
			if hook.TimeoutSec != test.wantTimeout {
				t.Fatalf("TimeoutSec = %d, want %d", hook.TimeoutSec, test.wantTimeout)
			}
		})
	}

	mcpCases := []struct {
		name     string
		body     string
		wantHash string
	}{
		{
			name:     "mcp_tool hashes server/tool/input without async",
			body:     `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"mcp_tool","server":"security","tool":"inspect","input":{"path":"${tool_input.path}"},"timeout":9,"statusMessage":"checking security policy"}]}]}}`,
			wantHash: "sha256:bf65d7061da938acf0d44d7c58e6011a854e67d6a3625940b5f40b0f97259aab",
		},
		{
			name:     "mcp_tool without input hashes an empty table",
			body:     `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"mcp_tool","server":"security","tool":"inspect"}]}]}}`,
			wantHash: "sha256:59b5ad65a4dde7b4e024c13ae6fbdb658773007c78f873d7c1902d788569c357",
		},
	}
	for _, test := range mcpCases {
		t.Run(test.name, func(t *testing.T) {
			cwd := t.TempDir()
			hooksDir := filepath.Join(cwd, ".gcode")
			if err := os.MkdirAll(hooksDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(hooksDir, "hooks.json"), []byte(test.body), 0o600); err != nil {
				t.Fatal(err)
			}
			service := NewHookDiscoveryService("")
			service.McpToolHooksEnabled = true
			response := service.Discover(&HookListParams{CWDs: []string{cwd}}, "")
			if len(response.Data) != 1 || len(response.Data[0].Hooks) != 1 {
				t.Fatalf("Discover() = %+v", response)
			}
			if got := response.Data[0].Hooks[0].CurrentHash; got != test.wantHash {
				t.Fatalf("CurrentHash = %s, want %s", got, test.wantHash)
			}
		})
	}
}
