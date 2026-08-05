package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex_go/rollout"
	"codex_go/session"
	"codex_go/state"
)

func TestReconcileStateForDesktopHandoffProjectsThread(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_SQLITE_HOME", "")
	threadID := "019fd0e3-05cc-78e4-b04e-91247f3befc1"
	now := time.Now().UTC()
	path := filepath.Join(home, "sessions", "2026", "08", "05", "rollout-2026-08-05T07-46-06-"+threadID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create rollout dir error = %v", err)
	}
	metaLine, err := json.Marshal(rollout.Line{
		Type: "session_meta",
		Meta: &rollout.SessionMeta{
			ID:            threadID,
			SessionID:     threadID,
			Timestamp:     now.UTC().Format(time.RFC3339Nano),
			CWD:           `D:\repo`,
			Source:        "cli",
			ThreadSource:  "user",
			ModelProvider: "deepseek",
			CLIVersion:    "0.146.0",
			HistoryMode:   "legacy",
		},
	})
	if err != nil {
		t.Fatalf("marshal session_meta error = %v", err)
	}
	itemLine, err := json.Marshal(rollout.Line{
		Type:    "event_msg",
		Payload: json.RawMessage(`{"type":"user_message","message":"hello desktop"}`),
	})
	if err != nil {
		t.Fatalf("marshal event error = %v", err)
	}
	data := append(append(append(metaLine, '\n'), itemLine...), '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write rollout error = %v", err)
	}

	if err := reconcileStateForDesktopHandoff(home, path); err != nil {
		t.Fatalf("reconcileStateForDesktopHandoff() error = %v", err)
	}

	sqliteConfig, err := state.SqliteConfigForCodexHome(home)
	if err != nil {
		t.Fatalf("SqliteConfigForCodexHome() error = %v", err)
	}
	runtime, err := state.InitStateRuntime(context.Background(), sqliteConfig, "openai")
	if err != nil {
		t.Fatalf("InitStateRuntime() error = %v", err)
	}
	defer runtime.Close()
	rows, err := runtime.ListThreadRows(context.Background())
	if err != nil {
		t.Fatalf("ListThreadRows() error = %v", err)
	}
	found := false
	for i := range rows {
		if rows[i].ID != threadID {
			continue
		}
		found = true
		if rows[i].Source != "cli" {
			t.Fatalf("projected source = %q, want cli", rows[i].Source)
		}
		if rows[i].HistoryMode != "legacy" {
			t.Fatalf("projected history mode = %q, want legacy", rows[i].HistoryMode)
		}
		if !rows[i].CWD.Valid || rows[i].CWD.String != `D:\repo` {
			t.Fatalf("projected cwd = %#v, want D:\\repo", rows[i].CWD)
		}
		if !rows[i].ModelProvider.Valid || rows[i].ModelProvider.String != "deepseek" {
			t.Fatalf("projected model provider = %#v, want deepseek", rows[i].ModelProvider)
		}
		if !rows[i].Preview.Valid || rows[i].Preview.String != "hello desktop" {
			t.Fatalf("projected preview = %#v, want hello desktop", rows[i].Preview)
		}
	}
	if !found {
		t.Fatalf("reconciled thread %s missing from state DB rows", threadID)
	}
}

func TestInteractiveReconcileDesktopThreadMaterializesFreshSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_SQLITE_HOME", "")
	t.Setenv("CODEX_HOME", home)
	threadID := "019fd130-0000-7000-8000-000000000099"
	now := fixedAppSessionTime()
	store := session.NewStore(filepath.Join(home, "sessions"))
	if err := store.Create(&session.Record{
		ID:        session.ThreadID(threadID),
		SessionID: threadID,
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: session.Metadata{
			CWD:           `D:\repo`,
			ModelProvider: "deepseek",
			Source:        "cli",
			ThreadSource:  "user",
			HistoryMode:   "legacy",
			SessionPrefix: session.PrefixForSessionID(threadID),
		},
	}); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}

	if err := interactiveReconcileDesktopThread(threadID); err != nil {
		t.Fatalf("interactiveReconcileDesktopThread() error = %v", err)
	}
	path, err := rollout.FindThreadPath(home, threadID, false)
	if err != nil {
		t.Fatalf("FindThreadPath() after materialization error = %v", err)
	}
	meta, err := rollout.FirstSessionMeta(path)
	if err != nil {
		t.Fatalf("FirstSessionMeta() error = %v", err)
	}
	if meta == nil || meta.ID != threadID || meta.HistoryMode != "legacy" {
		t.Fatalf("materialized meta = %#v", meta)
	}

	sqliteConfig, err := state.SqliteConfigForCodexHome(home)
	if err != nil {
		t.Fatalf("SqliteConfigForCodexHome() error = %v", err)
	}
	runtime, err := state.InitStateRuntime(context.Background(), sqliteConfig, "openai")
	if err != nil {
		t.Fatalf("InitStateRuntime() error = %v", err)
	}
	defer runtime.Close()
	rows, err := runtime.ListThreadRows(context.Background())
	if err != nil {
		t.Fatalf("ListThreadRows() error = %v", err)
	}
	found := false
	for i := range rows {
		if rows[i].ID == threadID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("materialized session %s missing from state DB", threadID)
	}
}

func TestInteractiveReconcileDesktopThreadUnknownThreadFallsThrough(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_SQLITE_HOME", "")
	t.Setenv("CODEX_HOME", home)
	if err := interactiveReconcileDesktopThread("019fd130-0000-7000-8000-0000000000aa"); err != nil {
		t.Fatalf("interactiveReconcileDesktopThread() unknown thread error = %v", err)
	}
	if err := interactiveReconcileDesktopThread(""); err != nil {
		t.Fatalf("interactiveReconcileDesktopThread() empty id error = %v", err)
	}
}
