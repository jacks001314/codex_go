package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func TestReconcileStateForDesktopHandoffRepairsLegacyGoSessionMeta(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_SQLITE_HOME", "")
	threadID := "019fd130-0000-7000-8000-000000000098"
	path := filepath.Join(home, "sessions", "2026", "08", "06", "rollout-2026-08-06T15-00-00-"+threadID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create rollout dir error = %v", err)
	}
	legacy := `{"timestamp":"2026-08-06T15:00:00Z","type":"session_meta","payload":{"id":"` + threadID + `","timestamp":"2026-08-06T15:00:00Z","cwd":"D:\\repo","source":"cli","model_provider":"deepseek"}}` + "\n"
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy rollout error = %v", err)
	}

	if err := reconcileStateForDesktopHandoff(home, path); err != nil {
		t.Fatalf("reconcileStateForDesktopHandoff() error = %v", err)
	}
	afterReconcile, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat repaired rollout error = %v", err)
	}
	if _, err := rollout.EnsureRustCompatibleSessionMeta(path); err != nil {
		t.Fatalf("EnsureRustCompatibleSessionMeta() error = %v", err)
	}
	afterEnsure, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat ensured rollout error = %v", err)
	}
	if afterEnsure.Size() != afterReconcile.Size() {
		t.Fatalf("reconcile did not repair rollout before ensure: %d -> %d", afterReconcile.Size(), afterEnsure.Size())
	}
}

func TestReconcileStateForDesktopHandoffBackfillsLegacyHistoryEvents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_SQLITE_HOME", "")
	threadID := "019fd130-0000-7000-8000-000000000097"
	now := fixedAppSessionTime()
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome: home, ThreadID: threadID, SessionID: threadID, CWD: `D:\repo`, Source: "cli", Originator: "codex_cli_rs", ModelProvider: "deepseek", HistoryMode: "legacy", CLIVersion: "test", Now: now,
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	item := session.Item{ID: "user-1", Type: "message", Role: "user", Text: "legacy visible", Content: []session.ContentPart{{Type: "input_text", Text: "legacy visible"}}, CreatedAt: now, Metadata: map[string]any{"turnId": "turn-1"}}
	if err := recorder.AppendItem(*rollout.ItemFromSessionItem(&item)); err != nil {
		t.Fatalf("AppendItem() error = %v", err)
	}
	path := recorder.Path()
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	store := session.NewStore(filepath.Join(home, "sessions"))
	if err := store.Save(&session.Record{
		ID: session.ThreadID(threadID), SessionID: threadID, Preview: item.Text, CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: `D:\repo`, Source: "cli", ModelProvider: "deepseek", HistoryMode: "legacy", RolloutTurns: []session.TurnSnapshot{{ID: "turn-1", Status: "completed"}}},
		Items:    []session.Item{item},
	}); err != nil {
		t.Fatalf("Save session error = %v", err)
	}
	if err := reconcileStateForDesktopHandoff(home, path); err != nil {
		t.Fatalf("reconcileStateForDesktopHandoff() error = %v", err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(first), `"type":"user_message"`) || !strings.Contains(string(first), `"message":"legacy visible"`) {
		t.Fatalf("reconciled rollout is not Desktop-readable: %s", first)
	}
	if err := reconcileStateForDesktopHandoff(home, path); err != nil {
		t.Fatalf("second reconcileStateForDesktopHandoff() error = %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("second ReadFile() error = %v", err)
	}
	if string(first) != string(second) {
		t.Fatal("second Desktop handoff changed the rollout")
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
