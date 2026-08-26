package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/appserver"
	"codex_go/config"
	codextui "codex_go/tui"
)

func TestInteractiveHooksReaderAndWriterPersistState(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	projectKey := strings.ReplaceAll(filepath.Clean(cwd), `\`, `\\`)
	if err := os.WriteFile(config.ConfigPath(home), []byte("[projects.\""+projectKey+"\"]\ntrust_level = \"trusted\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(config.toml) error = %v", err)
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

	reader := interactiveHooksReader(nil)
	listed, err := reader(cwd)
	if err != nil {
		t.Fatalf("interactiveHooksReader error = %v", err)
	}
	if len(listed.Data) != 1 || len(listed.Data[0].Hooks) != 1 {
		t.Fatalf("initial hooks/list = %#v", listed)
	}
	hook := listed.Data[0].Hooks[0]
	if hook.EventName != appserver.HookEventPreToolUse || hook.TrustStatus != appserver.HookTrustUntrusted {
		t.Fatalf("initial hook = %#v", hook)
	}

	writer := interactiveHookConfigWriter(nil)
	if err := writer(codextui.BuildSingleHookTrustWriteParams(hook.Key, hook.CurrentHash)); err != nil {
		t.Fatalf("trust hook error = %v", err)
	}
	listed, err = reader(cwd)
	if err != nil {
		t.Fatalf("interactiveHooksReader after trust error = %v", err)
	}
	hook = listed.Data[0].Hooks[0]
	if hook.TrustStatus != appserver.HookTrustTrusted {
		t.Fatalf("trusted hook = %#v", hook)
	}

	if err := writer(config.ConfigBatchWriteParams{
		Edits: []config.ConfigEdit{{
			KeyPath:       "hooks.state",
			Value:         map[string]any{hook.Key: map[string]any{"enabled": false}},
			MergeStrategy: config.MergeUpsert,
		}},
		ReloadUserConfig: true,
	}); err != nil {
		t.Fatalf("disable hook error = %v", err)
	}
	listed, err = reader(cwd)
	if err != nil {
		t.Fatalf("interactiveHooksReader after disable error = %v", err)
	}
	hook = listed.Data[0].Hooks[0]
	if hook.Enabled || hook.TrustStatus != appserver.HookTrustTrusted {
		t.Fatalf("disabled hook = %#v", hook)
	}
}
