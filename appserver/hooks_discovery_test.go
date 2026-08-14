package appserver

import (
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
	hooksDir := filepath.Join(cwd, ".codex")
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

func TestHookDiscoveryRecognizesUnsupportedMCPToolHook(t *testing.T) {
	cwd := t.TempDir()
	hooksDir := filepath.Join(cwd, ".codex")
	if err := os.MkdirAll(hooksDir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"hooks":{"PreToolUse":[{"hooks":[{"type":"mcp_tool","server":"linear","tool":"get_issue","input":{"id":"ENG-1"}}]}]}}`
	if err := os.WriteFile(filepath.Join(hooksDir, "hooks.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	response := NewHookDiscoveryService("").Discover(&HookListParams{CWDs: []string{cwd}}, "")
	if len(response.Data) != 1 || len(response.Data[0].Hooks) != 0 || !warningsContain(response.Data[0].Warnings, "MCP tool hooks are not supported yet") {
		t.Fatalf("MCP tool hook discovery = %+v", response)
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
	if hook.CurrentHash != hookDiscoveryHash(HookEventPreToolUse, hook.Matcher, *hook.Command, 5, hook.StatusMessage) {
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
	hooksDir := filepath.Join(cwd, ".codex")
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
	hooksDir := filepath.Join(workspace, ".codex")
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
	hooksDir := filepath.Join(cwd, ".codex")
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
	if err := os.MkdirAll(filepath.Join(root, ".codex"), 0o700); err != nil {
		t.Fatalf("MkdirAll root .codex error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(worktree, ".codex"), 0o700); err != nil {
		t.Fatalf("MkdirAll worktree .codex error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+filepath.Join(root, ".git", "worktrees", "feature")+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile worktree .git error = %v", err)
	}
	projectTrust := strings.ReplaceAll(filepath.Clean(root), `\`, `\\`)
	if err := os.WriteFile(config.ConfigPath(home), []byte("[projects.\""+projectTrust+"\"]\ntrust_level = \"trusted\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".codex", "hooks.json"), []byte(`{
		"hooks": {
			"PostToolUse": [{"hooks": [{"type": "command", "command": "echo root checkout"}]}]
		}
	}`), 0o600); err != nil {
		t.Fatalf("WriteFile root hooks error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktree, ".codex", "hooks.json"), []byte(`{
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
	if !strings.Contains(filepath.Clean(hook.SourcePath), filepath.Clean(filepath.Join(root, ".codex"))) {
		t.Fatalf("source path = %q, want root checkout .codex", hook.SourcePath)
	}
}

func TestHookDiscoverySkipsUntrustedProjectHooksWhenConfigServicePresent(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("model = \"gpt-user\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config error = %v", err)
	}
	cwd := t.TempDir()
	hooksDir := filepath.Join(cwd, ".codex")
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
	hooksDir := filepath.Join(cwd, ".codex")
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
	hooksDir := filepath.Join(cwd, ".codex")
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
	hooksDir := filepath.Join(cwd, ".codex")
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
	hooksDir := filepath.Join(cwd, ".codex")
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
