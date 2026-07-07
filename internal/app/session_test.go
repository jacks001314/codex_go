package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/internal/config"
	"codex_go/internal/session"
)

func TestSessionArchiveUnarchiveForkAndDeleteFlow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	store := session.NewStore(filepath.Join(home, "sessions"))
	now := fixedAppSessionTime()
	threadID := session.ThreadID("123e4567-e89b-12d3-a456-426614174000")
	if err := store.Save(&session.Record{
		ID:        threadID,
		SessionID: string(threadID),
		Title:     "Thread One",
		Preview:   "hello",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Items: []session.Item{
			{ID: "item-1", Type: "message", Role: "user", Text: "hello", CreatedAt: now},
		},
	}); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"archive", string(threadID)}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("archive returned error: %v", err)
	}
	archived, err := store.Read(threadID, true, false)
	if err != nil {
		t.Fatalf("read archived returned error: %v", err)
	}
	if !archived.Archived {
		t.Fatal("archive did not mark record archived")
	}

	stdout.Reset()
	if err := Run(context.Background(), []string{"unarchive", string(threadID)}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("unarchive returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Unarchived session Thread One ("+string(threadID)+").") {
		t.Fatalf("unarchive stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := Run(context.Background(), []string{"fork", string(threadID)}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("fork returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), `"action": "forked"`) {
		t.Fatalf("fork stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := Run(context.Background(), []string{"delete", "--force", string(threadID)}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("delete returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Deleted session "+string(threadID)+".") {
		t.Fatalf("delete stdout = %q", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(home, "sessions", string(threadID)+".json")); !os.IsNotExist(err) {
		t.Fatalf("deleted file stat error = %v, want not exist", err)
	}
}

func TestSessionMutationsResolveNameSelector(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	store := session.NewStore(filepath.Join(home, "sessions"))
	now := fixedAppSessionTime()
	saveAppSessionRecord(t, store, &session.Record{
		ID:        "active-thread",
		Title:     "Design",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{Source: "cli"},
	})
	saveAppSessionRecord(t, store, &session.Record{
		ID:        "archived-thread",
		Title:     "Archived Design",
		Archived:  true,
		CreatedAt: now,
		UpdatedAt: now.Add(time.Minute),
		RecencyAt: now.Add(time.Minute),
		Metadata:  session.Metadata{Source: "vscode"},
	})

	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"archive", "Design"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("archive positional name returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Archived session Design (active-thread).") {
		t.Fatalf("archive stdout = %q", stdout.String())
	}
	archived, err := store.Read("active-thread", true, false)
	if err != nil {
		t.Fatalf("read archived active-thread: %v", err)
	}
	if !archived.Archived {
		t.Fatal("archive positional name did not archive selected session")
	}

	stdout.Reset()
	if err := Run(context.Background(), []string{"unarchive", "Archived Design"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("unarchive positional name returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Unarchived session Archived Design (archived-thread).") {
		t.Fatalf("unarchive stdout = %q", stdout.String())
	}
	unarchived, err := store.Read("archived-thread", true, false)
	if err != nil {
		t.Fatalf("read unarchived archived-thread: %v", err)
	}
	if unarchived.Archived {
		t.Fatal("unarchive positional name did not unarchive selected session")
	}

	err = Run(context.Background(), []string{"delete", "--force", "--name", "Design"}, strings.NewReader(""), &stdout, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "unknown delete option --name") {
		t.Fatalf("delete --force --name error = %v", err)
	}
}

func TestSessionMutationsResolvePositionalNameLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	store := session.NewStore(filepath.Join(home, "sessions"))
	now := fixedAppSessionTime()
	saveAppSessionRecord(t, store, &session.Record{
		ID:        "active-roadmap",
		Title:     "Roadmap",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{Source: "cli"},
	})
	saveAppSessionRecord(t, store, &session.Record{
		ID:        "archived-roadmap",
		Title:     "Archived Roadmap",
		Archived:  true,
		CreatedAt: now,
		UpdatedAt: now.Add(time.Minute),
		RecencyAt: now.Add(time.Minute),
		Metadata:  session.Metadata{Source: "vscode"},
	})
	saveAppSessionRecord(t, store, &session.Record{
		ID:        "exec-only",
		Title:     "Exec Only",
		CreatedAt: now,
		UpdatedAt: now.Add(2 * time.Minute),
		RecencyAt: now.Add(2 * time.Minute),
		Metadata:  session.Metadata{Source: "exec"},
	})

	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"archive", "Roadmap"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("archive positional name returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Archived session Roadmap (active-roadmap).") {
		t.Fatalf("archive stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := Run(context.Background(), []string{"unarchive", "Archived Roadmap"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("unarchive positional name returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Unarchived session Archived Roadmap (archived-roadmap).") {
		t.Fatalf("unarchive stdout = %q", stdout.String())
	}

	err := Run(context.Background(), []string{"archive", "Exec Only"}, strings.NewReader(""), &stdout, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "No active session found matching 'Exec Only'.") {
		t.Fatalf("archive non-interactive name error = %v", err)
	}
}

func TestSessionResumeLast(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	store := session.NewStore(filepath.Join(home, "sessions"))
	now := fixedAppSessionTime()
	if err := store.Save(&session.Record{ID: "older", Preview: "old", CreatedAt: now, UpdatedAt: now, RecencyAt: now}); err != nil {
		t.Fatalf("Save older returned error: %v", err)
	}
	if err := store.Save(&session.Record{ID: "newer", Preview: "new", CreatedAt: now, UpdatedAt: now.Add(time.Minute), RecencyAt: now.Add(time.Minute)}); err != nil {
		t.Fatalf("Save newer returned error: %v", err)
	}

	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"resume", "--last"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("resume returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), `"id": "newer"`) {
		t.Fatalf("resume stdout = %q", stdout.String())
	}
}

func TestSessionResumeByName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	store := session.NewStore(filepath.Join(home, "sessions"))
	now := fixedAppSessionTime()
	if err := store.Save(&session.Record{ID: "older", Title: "Design", Preview: "old", CreatedAt: now, UpdatedAt: now, RecencyAt: now}); err != nil {
		t.Fatalf("Save older returned error: %v", err)
	}
	if err := store.Save(&session.Record{ID: "newer", Title: "Design", Preview: "new", CreatedAt: now, UpdatedAt: now.Add(time.Minute), RecencyAt: now.Add(time.Minute)}); err != nil {
		t.Fatalf("Save newer returned error: %v", err)
	}

	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"resume", "Design"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("resume positional name returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), `"id": "newer"`) {
		t.Fatalf("resume positional stdout = %q", stdout.String())
	}
}

func TestSessionRemoteFlagsValidateBeforeLocalStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	store := session.NewStore(filepath.Join(home, "sessions"))
	now := fixedAppSessionTime()
	threadID := session.ThreadID("123e4567-e89b-12d3-a456-426614174020")
	saveAppSessionRecord(t, store, &session.Record{
		ID:        threadID,
		Title:     "Remote Candidate",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{Source: "cli"},
	})

	var stdout bytes.Buffer
	err := Run(context.Background(), []string{"--remote", "unix://", "archive", string(threadID)}, strings.NewReader(""), &stdout, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "session remote app-server mode is not implemented") {
		t.Fatalf("remote archive error = %v", err)
	}
	if stdout.String() != "" {
		t.Fatalf("remote archive stdout = %q", stdout.String())
	}
	record, err := store.Read(threadID, true, false)
	if err != nil {
		t.Fatalf("read remote archive candidate: %v", err)
	}
	if record.Archived {
		t.Fatal("remote archive unexpectedly mutated the local store")
	}

	err = Run(context.Background(), []string{"--remote-auth-token-env", "CODEX_REMOTE_AUTH_TOKEN", "resume", string(threadID)}, strings.NewReader(""), &stdout, &bytes.Buffer{})
	if err == nil || err.Error() != "`--remote-auth-token-env` requires `--remote`." {
		t.Fatalf("remote auth without remote error = %v", err)
	}

	err = Run(context.Background(), []string{"--remote", "https://not-a-tui-remote", "resume", "--remote", "unix://", string(threadID)}, strings.NewReader(""), &stdout, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "session remote app-server mode is not implemented") {
		t.Fatalf("session remote override error = %v", err)
	}
}

func TestSessionForkByName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	store := session.NewStore(filepath.Join(home, "sessions"))
	now := fixedAppSessionTime()
	saveAppSessionRecord(t, store, &session.Record{
		ID:        "source-thread",
		Title:     "Fork Me",
		Preview:   "source",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Items: []session.Item{
			{ID: "item-1", Type: "message", Role: "user", Text: "hello", CreatedAt: now},
		},
	})

	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"fork", "Fork Me"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("fork positional name returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), `"action": "forked"`) || !strings.Contains(stdout.String(), `"preview": "source"`) {
		t.Fatalf("fork stdout = %q", stdout.String())
	}
}

func TestSessionForkPositionalNameCopiesAllHistory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	store := session.NewStore(filepath.Join(home, "sessions"))
	now := fixedAppSessionTime()
	saveAppSessionRecord(t, store, &session.Record{
		ID:        "source-thread",
		Title:     "Snapshot Source",
		Preview:   "source",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: session.Metadata{RolloutTurns: []session.TurnSnapshot{
			{ID: "turn-1", Status: "completed"},
			{ID: "turn-2", Status: "completed"},
			{ID: "turn-3", Status: "inProgress"},
		}},
		Items: []session.Item{
			{ID: "item-1", Type: "message", Text: "one", CreatedAt: now, Metadata: map[string]any{"turnId": "turn-1"}},
			{ID: "item-2", Type: "message", Text: "two", CreatedAt: now, Metadata: map[string]any{"turnId": "turn-1"}},
			{ID: "item-3", Type: "message", Text: "three", CreatedAt: now, Metadata: map[string]any{"turnId": "turn-2"}},
			{ID: "item-4", Type: "message", Text: "four", CreatedAt: now, Metadata: map[string]any{"turnId": "turn-2"}},
			{ID: "item-5", Type: "message", Text: "five", CreatedAt: now, Metadata: map[string]any{"turnId": "turn-3"}},
		},
	})

	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"fork", "Snapshot Source"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("fork positional name returned error: %v", err)
	}
	payload := decodeSessionSummaryPayload(t, &stdout)
	if payload.Action != "forked" || payload.ItemCount != 5 {
		t.Fatalf("fork summary = %#v", payload)
	}
	forked, err := store.Read(payload.ID, false, true)
	if err != nil {
		t.Fatalf("Read forked returned error: %v", err)
	}
	if got := sessionItemIDs(forked.Items); strings.Join(got, ",") != "item-1,item-2,item-3,item-4,item-5" {
		t.Fatalf("forked items = %v", got)
	}
	if forked.Metadata.Extra["fork_mode"] != "all" || forked.Metadata.Extra["fork_item_count"] != float64(5) {
		t.Fatalf("fork metadata = %#v", forked.Metadata.Extra)
	}
}

func TestSessionResumePickerListsInteractiveCandidates(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	store := session.NewStore(filepath.Join(home, "sessions"))
	now := fixedAppSessionTime()
	saveAppSessionRecord(t, store, &session.Record{
		ID:        "older",
		Title:     "Older CLI",
		Preview:   "old",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{Source: "cli", CWD: "D:\\repo", Model: "gpt-5"},
	})
	saveAppSessionRecord(t, store, &session.Record{
		ID:        "newer",
		Title:     "Newer VS Code",
		Preview:   "new",
		CreatedAt: now,
		UpdatedAt: now.Add(time.Minute),
		RecencyAt: now.Add(time.Minute),
		Metadata:  session.Metadata{Source: "vscode", ModelProvider: "openai"},
	})
	saveAppSessionRecord(t, store, &session.Record{
		ID:        "exec-newest",
		Title:     "Exec",
		CreatedAt: now,
		UpdatedAt: now.Add(2 * time.Minute),
		RecencyAt: now.Add(2 * time.Minute),
		Metadata:  session.Metadata{Source: "exec"},
	})
	saveAppSessionRecord(t, store, &session.Record{
		ID:        "archived",
		Title:     "Archived",
		Archived:  true,
		CreatedAt: now,
		UpdatedAt: now.Add(3 * time.Minute),
		RecencyAt: now.Add(3 * time.Minute),
		Metadata:  session.Metadata{Source: "cli"},
	})

	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"resume"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("resume picker returned error: %v", err)
	}
	payload := decodeSessionPickerResponse(t, &stdout)
	if payload.Action != "resume" || payload.Command != "resume" {
		t.Fatalf("picker action/command = %q/%q", payload.Action, payload.Command)
	}
	if got := pickerIDs(payload.Sessions); strings.Join(got, ",") != "newer,older" {
		t.Fatalf("picker ids = %v", got)
	}
	if payload.Sessions[0].UpdatedAt == "" || payload.Sessions[0].ModelProvider != "openai" {
		t.Fatalf("first picker session = %#v", payload.Sessions[0])
	}
	if !strings.Contains(payload.Hint, "codex resume SESSION_ID") {
		t.Fatalf("picker hint = %q", payload.Hint)
	}
}

func TestSessionResumePickerIncludeNonInteractive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	store := session.NewStore(filepath.Join(home, "sessions"))
	now := fixedAppSessionTime()
	saveAppSessionRecord(t, store, &session.Record{
		ID:        "interactive",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{Source: "cli"},
	})
	saveAppSessionRecord(t, store, &session.Record{
		ID:        "exec-newer",
		CreatedAt: now,
		UpdatedAt: now.Add(time.Minute),
		RecencyAt: now.Add(time.Minute),
		Metadata:  session.Metadata{Source: "exec"},
	})

	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"resume", "--include-non-interactive"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("resume picker returned error: %v", err)
	}
	payload := decodeSessionPickerResponse(t, &stdout)
	if got := pickerIDs(payload.Sessions); strings.Join(got, ",") != "exec-newer,interactive" {
		t.Fatalf("picker ids = %v", got)
	}
}

func TestSessionResumePickerAllIncludesArchived(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	store := session.NewStore(filepath.Join(home, "sessions"))
	now := fixedAppSessionTime()
	saveAppSessionRecord(t, store, &session.Record{
		ID:        "active",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{Source: "cli"},
	})
	saveAppSessionRecord(t, store, &session.Record{
		ID:        "archived-newer",
		Archived:  true,
		CreatedAt: now,
		UpdatedAt: now.Add(time.Minute),
		RecencyAt: now.Add(time.Minute),
		Metadata:  session.Metadata{Source: "cli"},
	})

	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"resume", "--all"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("resume picker returned error: %v", err)
	}
	payload := decodeSessionPickerResponse(t, &stdout)
	if got := pickerIDs(payload.Sessions); strings.Join(got, ",") != "archived-newer,active" {
		t.Fatalf("picker ids = %v", got)
	}
	if !payload.Sessions[0].Archived {
		t.Fatalf("first picker session = %#v, want archived", payload.Sessions[0])
	}
}

func TestSessionResumeLastUsesPickerFilters(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	store := session.NewStore(filepath.Join(home, "sessions"))
	now := fixedAppSessionTime()
	saveAppSessionRecord(t, store, &session.Record{
		ID:        "interactive",
		Preview:   "interactive",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{Source: "cli"},
	})
	saveAppSessionRecord(t, store, &session.Record{
		ID:        "exec-newer",
		Preview:   "exec",
		CreatedAt: now,
		UpdatedAt: now.Add(time.Minute),
		RecencyAt: now.Add(time.Minute),
		Metadata:  session.Metadata{Source: "exec"},
	})
	saveAppSessionRecord(t, store, &session.Record{
		ID:        "archived-newest",
		Preview:   "archived",
		Archived:  true,
		CreatedAt: now,
		UpdatedAt: now.Add(2 * time.Minute),
		RecencyAt: now.Add(2 * time.Minute),
		Metadata:  session.Metadata{Source: "cli"},
	})

	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"resume", "--last"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("resume --last returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), `"id": "interactive"`) {
		t.Fatalf("resume --last stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := Run(context.Background(), []string{"resume", "--last", "--include-non-interactive"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("resume --last --include-non-interactive returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), `"id": "exec-newer"`) {
		t.Fatalf("resume --last --include-non-interactive stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := Run(context.Background(), []string{"resume", "--last", "--all"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("resume --last --all returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), `"id": "archived-newest"`) {
		t.Fatalf("resume --last --all stdout = %q", stdout.String())
	}
}

func TestSessionForkPickerListsCandidates(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	store := session.NewStore(filepath.Join(home, "sessions"))
	now := fixedAppSessionTime()
	saveAppSessionRecord(t, store, &session.Record{
		ID:        "fork-target",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{Source: "cli"},
	})

	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"fork"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("fork picker returned error: %v", err)
	}
	payload := decodeSessionPickerResponse(t, &stdout)
	if payload.Action != "fork" || payload.Command != "fork" {
		t.Fatalf("picker action/command = %q/%q", payload.Action, payload.Command)
	}
	if got := pickerIDs(payload.Sessions); strings.Join(got, ",") != "fork-target" {
		t.Fatalf("picker ids = %v", got)
	}
	if !strings.Contains(payload.Hint, "codex fork SESSION_ID") {
		t.Fatalf("picker hint = %q", payload.Hint)
	}
}

func TestSessionRuntimeLoadsStrictConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(config.ConfigPath(home), []byte("foo = \"bar\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	for _, args := range [][]string{
		{"--strict-config", "resume", "--last"},
		{"archive", "--strict-config", "missing-session"},
	} {
		err := Run(context.Background(), args, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "unknown configuration field `foo`") {
			t.Fatalf("Run(%v) error = %v, want strict config error", args, err)
		}
	}
}

func TestSessionRuntimeLoadsConfigOverrides(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(config.ConfigPath(home), []byte("model = \"gpt-5\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	for _, args := range [][]string{
		{"--strict-config", "-c", "foo=bar", "resume", "--last"},
		{"resume", "--strict-config", "-c", "foo=bar", "--last"},
	} {
		err := Run(context.Background(), args, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "unknown configuration field `foo`") {
			t.Fatalf("Run(%v) error = %v, want strict override error", args, err)
		}
	}
}

func TestSessionDeleteRequiresForce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	store := session.NewStore(filepath.Join(home, "sessions"))
	threadID := session.ThreadID("123e4567-e89b-12d3-a456-426614174001")
	saveAppSessionRecord(t, store, &session.Record{ID: threadID, Title: "Delete Me", CreatedAt: fixedAppSessionTime(), UpdatedAt: fixedAppSessionTime(), RecencyAt: fixedAppSessionTime()})

	err := Run(context.Background(), []string{"delete", string(threadID)}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("delete returned nil error, want failure")
	}
	if !strings.Contains(err.Error(), "cannot confirm session deletion without an interactive terminal") {
		t.Fatalf("error = %v", err)
	}
}

func TestSessionDeleteInteractiveConfirmation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	store := session.NewStore(filepath.Join(home, "sessions"))
	now := fixedAppSessionTime()
	cancelID := session.ThreadID("123e4567-e89b-12d3-a456-426614174010")
	deleteID := session.ThreadID("123e4567-e89b-12d3-a456-426614174011")
	saveAppSessionRecord(t, store, &session.Record{ID: cancelID, Title: "Cancel Me", CreatedAt: now, UpdatedAt: now, RecencyAt: now})
	saveAppSessionRecord(t, store, &session.Record{ID: deleteID, Title: "Delete Me", CreatedAt: now, UpdatedAt: now, RecencyAt: now})

	var stdout bytes.Buffer
	cancelStderr := &terminalBuffer{}
	if err := Run(context.Background(), []string{"delete", string(cancelID)}, newTerminalReader("n\n"), &stdout, cancelStderr); err != nil {
		t.Fatalf("delete cancel returned error: %v", err)
	}
	if !strings.Contains(cancelStderr.String(), "Permanently delete session 'Cancel Me' ("+string(cancelID)+")?") || !strings.Contains(cancelStderr.String(), "Continue? [y/N]: ") {
		t.Fatalf("cancel stderr = %q", cancelStderr.String())
	}
	if !strings.Contains(stdout.String(), "Delete cancelled.") {
		t.Fatalf("cancel stdout = %q", stdout.String())
	}
	if _, err := store.Read(cancelID, true, false); err != nil {
		t.Fatalf("cancelled delete removed record: %v", err)
	}

	stdout.Reset()
	deleteStderr := &terminalBuffer{}
	if err := Run(context.Background(), []string{"delete", string(deleteID)}, newTerminalReader("yes\n"), &stdout, deleteStderr); err != nil {
		t.Fatalf("delete confirm returned error: %v", err)
	}
	if !strings.Contains(deleteStderr.String(), "This cannot be undone. Subagent threads will also be deleted.") {
		t.Fatalf("delete stderr = %q", deleteStderr.String())
	}
	if !strings.Contains(stdout.String(), "Deleted session "+string(deleteID)+".") {
		t.Fatalf("delete stdout = %q", stdout.String())
	}
	if _, err := store.Read(deleteID, true, false); err == nil {
		t.Fatal("confirmed delete left record behind")
	}
}

func fixedAppSessionTime() time.Time {
	return time.Date(2026, 6, 30, 1, 2, 3, 0, time.UTC)
}

func saveAppSessionRecord(t *testing.T, store *session.Store, record *session.Record) {
	t.Helper()
	if err := store.Save(record); err != nil {
		t.Fatalf("Save %s returned error: %v", record.ID, err)
	}
}

func decodeSessionPickerResponse(t *testing.T, stdout *bytes.Buffer) *sessionPickerResponse {
	t.Helper()
	var payload sessionPickerResponse
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode picker response %q: %v", stdout.String(), err)
	}
	return &payload
}

type sessionSummaryPayload struct {
	Action    string           `json:"action"`
	ID        session.ThreadID `json:"id"`
	ItemCount int              `json:"itemCount"`
}

func decodeSessionSummaryPayload(t *testing.T, stdout *bytes.Buffer) *sessionSummaryPayload {
	t.Helper()
	var payload sessionSummaryPayload
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode session summary %q: %v", stdout.String(), err)
	}
	return &payload
}

func pickerIDs(entries []*sessionPickerEntry) []string {
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		ids = append(ids, string(entry.ID))
	}
	return ids
}

func sessionItemIDs(items []session.Item) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

type terminalReader struct {
	*strings.Reader
}

func newTerminalReader(value string) *terminalReader {
	return &terminalReader{Reader: strings.NewReader(value)}
}

func (r *terminalReader) IsTerminal() bool {
	return true
}

type terminalBuffer struct {
	bytes.Buffer
}

func (b *terminalBuffer) IsTerminal() bool {
	return true
}

func (b *terminalBuffer) Flush() error {
	return nil
}
