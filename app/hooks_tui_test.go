package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/appserver"
	"codex_go/config"
	"codex_go/session"
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
	listed, err := reader(codextui.HooksListTargetForCWD(cwd))
	if err != nil {
		t.Fatalf("interactiveHooksReader error = %v", err)
	}
	if len(listed.Hooks) != 1 {
		t.Fatalf("initial hooks/list = %#v", listed)
	}
	hook := listed.Hooks[0]
	if hook.EventName != appserver.HookEventPreToolUse || hook.TrustStatus != appserver.HookTrustUntrusted {
		t.Fatalf("initial hook = %#v", hook)
	}

	writer := interactiveHookConfigWriter(nil)
	if err := writer(codextui.BuildSingleHookTrustWriteParams(hook.Key, hook.CurrentHash)); err != nil {
		t.Fatalf("trust hook error = %v", err)
	}
	listed, err = reader(codextui.HooksListTargetForCWD(cwd))
	if err != nil {
		t.Fatalf("interactiveHooksReader after trust error = %v", err)
	}
	hook = listed.Hooks[0]
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
	listed, err = reader(codextui.HooksListTargetForCWD(cwd))
	if err != nil {
		t.Fatalf("interactiveHooksReader after disable error = %v", err)
	}
	hook = listed.Hooks[0]
	if hook.Enabled || hook.TrustStatus != appserver.HookTrustTrusted {
		t.Fatalf("disabled hook = %#v", hook)
	}
}

// TestInteractiveHooksReaderThreadTargetUsesThreadCWD mirrors Rust #56553's TUI
// carrier: /hooks asks for the active thread, and the in-process reader resolves
// that thread's own working directory before listing its inventory.
func TestInteractiveHooksReaderThreadTargetUsesThreadCWD(t *testing.T) {
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

	store := session.NewStore(filepath.Join(home, "sessions"))
	now := time.Now().UTC()
	record := &session.Record{
		ID: "thread-1", SessionID: "thread-1",
		CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: cwd},
	}
	if err := store.Create(record); err != nil {
		t.Fatalf("create thread: %v", err)
	}

	entry, err := interactiveHooksReader(nil)(codextui.HooksListTargetForThread("thread-1"))
	if err != nil {
		t.Fatalf("thread hooks/list error = %v", err)
	}
	if filepath.Clean(entry.CWD) != filepath.Clean(cwd) || len(entry.Hooks) != 1 {
		t.Fatalf("thread hooks/list = %#v", entry)
	}
}

// TestInteractiveHooksReaderUnknownThreadFails mirrors Rust #56553: a thread
// target that cannot be resolved must report "thread not found" instead of
// silently listing the launcher's working directory.
func TestInteractiveHooksReaderUnknownThreadFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)

	_, err := interactiveHooksReader(nil)(codextui.HooksListTargetForThread("missing-thread"))
	want := "hooks/list failed in TUI: hooks/list failed: thread not found: missing-thread (code -32600)"
	if err == nil || err.Error() != want {
		t.Fatalf("unknown thread error = %v", err)
	}
}
