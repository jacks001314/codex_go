package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/session"
	"codex_go/tool"
)

func newCommandSessionsTestRouter(t *testing.T, threadID string, featureOn bool) (*RuntimeRouter, *tool.UnifiedExecManager) {
	t.Helper()
	store := session.NewStore(filepath.Join(t.TempDir(), "sessions"))
	now := time.Now().UTC()
	record := &session.Record{ID: session.ThreadID(threadID), SessionID: threadID, CreatedAt: now, UpdatedAt: now, RecencyAt: now}
	if err := store.Create(record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	home := t.TempDir()
	if featureOn {
		if err := os.WriteFile(config.ConfigPath(home), []byte("[features]\ncommand_sessions_world_state = true\n"), 0o600); err != nil {
			t.Fatalf("WriteFile config error = %v", err)
		}
	}
	manager := tool.NewUnifiedExecManager()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
		UnifiedExec:  manager,
		DefaultCWD:   t.TempDir(),
	})
	return router, manager
}

func commandSessionsText(t *testing.T, item any) string {
	t.Helper()
	texts := messageInputTextsForRole([]any{item}, "developer")
	if len(texts) != 1 {
		t.Fatalf("developer texts = %#v, want one section", texts)
	}
	return texts[0]
}

func startCommandSessionsBackgroundProcess(t *testing.T, manager *tool.UnifiedExecManager, threadID string, seconds int) int {
	t.Helper()
	command := []string{"sh", "-c", fmt.Sprintf("sleep %d", seconds)}
	if runtime.GOOS == "windows" {
		command = []string{"powershell", "-NoProfile", "-Command", fmt.Sprintf("Start-Sleep -Seconds %d", seconds)}
	}
	result, err := manager.Exec(context.Background(), &tool.ShellRequest{
		Command:             command,
		UnifiedExecThreadID: threadID,
		YieldTimeMS:         250,
		CWD:                 t.TempDir(),
	}, "call-command-sessions")
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if result == nil || result.ProcessID == nil {
		t.Fatalf("Exec() result = %#v, want a retained session", result)
	}
	return *result.ProcessID
}

// Rust #59633: a dropped command handle stays visible with its lifecycle state,
// an unchanged snapshot is not re-appended, and the section reports when
// write_stdin is unavailable.
func TestCommandSessionsWorldStateRendersRetainedSessionsLikeRust(t *testing.T) {
	const threadID = "thread-command-sessions"
	router, manager := newCommandSessionsTestRouter(t, threadID, true)
	processID := startCommandSessionsBackgroundProcess(t, manager, threadID, 30)
	defer manager.TerminateAll(threadID)

	result, err := router.commandSessionsWorldStateSection(threadID, "turn-command-sessions", router.effectiveConfigForSessionTelemetry(), true, time.Now().UTC(), false)
	if err != nil {
		t.Fatalf("commandSessionsWorldStateSection() error = %v", err)
	}
	if result.Item == nil {
		t.Fatal("running retained session was not published")
	}
	text := commandSessionsText(t, result.Item)
	for _, want := range []string{
		"<command_sessions>",
		"Current command sessions for this thread (supersedes earlier snapshots). Exited sessions retain results for write_stdin.",
		"\n" + strconv.Itoa(processID) + ": running\n",
		"</command_sessions>",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("command sessions text = %q, missing %q", text, want)
		}
	}

	again, err := router.commandSessionsWorldStateSection(threadID, "turn-command-sessions", router.effectiveConfigForSessionTelemetry(), true, time.Now().UTC(), false)
	if err != nil {
		t.Fatalf("second commandSessionsWorldStateSection() error = %v", err)
	}
	if again.Item != nil {
		t.Fatalf("unchanged snapshot re-emitted: %#v", commandSessionsText(t, again.Item))
	}

	unavailable, err := router.commandSessionsWorldStateSection(threadID, "turn-command-sessions", router.effectiveConfigForSessionTelemetry(), false, time.Now().UTC(), false)
	if err != nil {
		t.Fatalf("unavailable commandSessionsWorldStateSection() error = %v", err)
	}
	if unavailable.Item == nil {
		t.Fatal("polling-unavailable change was not published")
	}
	if got := commandSessionsText(t, unavailable.Item); !strings.Contains(got, "write_stdin is unavailable with the current tool set.") {
		t.Fatalf("polling-unavailable text = %q", got)
	}

	// Compaction starts a new window without the earlier fragment, so an
	// unchanged snapshot is re-rendered for the replacement context.
	forced, err := router.commandSessionsWorldStateSection(threadID, "turn-command-sessions", router.effectiveConfigForSessionTelemetry(), false, time.Now().UTC(), true)
	if err != nil {
		t.Fatalf("forced commandSessionsWorldStateSection() error = %v", err)
	}
	if forced.Item == nil {
		t.Fatal("compaction did not re-render the command sessions section")
	}
	if got, want := commandSessionsText(t, forced.Item), commandSessionsText(t, unavailable.Item); got != want {
		t.Fatalf("forced section = %q, want %q", got, want)
	}
}

// The snapshot keeps the lowest 16 session ids and reports the rest, and marks
// a session held by an interaction as in use (Rust CommandSessionsState::body).
func TestCommandSessionsWorldStateBodyLimitsLikeRust(t *testing.T) {
	sessions := make([]commandSessionSnapshot, 0, 18)
	for id := 1000; id < 1018; id++ {
		sessions = append(sessions, commandSessionSnapshot{SessionID: id, Status: commandSessionStatusRunning})
	}
	sessions[1].InUse = true
	kept, omitted := boundedCommandSessions(sessions)
	body := renderCommandSessionsBody(commandSessionsSnapshot{Sessions: kept, Omitted: omitted})
	if !strings.Contains(body, "1001: running; in use\n") {
		t.Fatalf("in-use session body = %q", body)
	}
	if strings.Contains(body, "1016: running") {
		t.Fatalf("body kept more than 16 sessions: %q", body)
	}
	if !strings.Contains(body, "2 additional sessions omitted; this snapshot is incomplete.") {
		t.Fatalf("omission notice = %q", body)
	}
	if strings.HasSuffix(body, "\n") {
		t.Fatalf("body must be trimmed like Rust: %q", body)
	}
}

// A retained session that exits without being polled reports its uncollected
// result, and an unchanged force=false snapshot stays silent.
func TestCommandSessionsWorldStateReportsExitedSessionLikeRust(t *testing.T) {
	const threadID = "thread-command-sessions-exited"
	router, manager := newCommandSessionsTestRouter(t, threadID, true)
	defer manager.TerminateAll(threadID)
	processID := startCommandSessionsBackgroundProcess(t, manager, threadID, 11)
	deadline := time.Now().Add(40 * time.Second)
	for {
		sessions := manager.CommandSessions(threadID)
		if len(sessions) == 1 && sessions[0].Exited {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("session %d never reported exited: %#v", processID, sessions)
		}
		time.Sleep(50 * time.Millisecond)
	}
	result, err := router.commandSessionsWorldStateSection(threadID, "turn-command-sessions-exited", router.effectiveConfigForSessionTelemetry(), true, time.Now().UTC(), false)
	if err != nil {
		t.Fatalf("commandSessionsWorldStateSection() error = %v", err)
	}
	if result.Item == nil {
		t.Fatal("exited retained session was not published")
	}
	if got := commandSessionsText(t, result.Item); !strings.Contains(got, strconv.Itoa(processID)+": exited; poll to collect") {
		t.Fatalf("exited session text = %q", got)
	}
}

// Turning the feature off (or restarting with no live sessions) still clears
// handles the model already saw (Rust DisabledCommandSessionsState).
func TestCommandSessionsWorldStateClearsStaleHandlesLikeRust(t *testing.T) {
	const threadID = "thread-command-sessions-stale"
	router, _ := newCommandSessionsTestRouter(t, threadID, false)
	record, err := router.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("threadRecord() error = %v", err)
	}
	state, err := session.DecodeWorldState(record.Metadata.WorldState)
	if err != nil {
		t.Fatalf("DecodeWorldState() error = %v", err)
	}
	stale, err := json.Marshal(commandSessionsSnapshot{Sessions: []commandSessionSnapshot{{SessionID: 1000, Status: commandSessionStatusRunning}}})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	state.CommandSessions = stale
	record.Metadata.WorldState, err = session.EncodeWorldState(state)
	if err != nil {
		t.Fatalf("EncodeWorldState() error = %v", err)
	}
	if err := router.runtimeSaveThreadRecord(record); err != nil {
		t.Fatalf("runtimeSaveThreadRecord() error = %v", err)
	}

	result, err := router.commandSessionsWorldStateSection(threadID, "turn-command-sessions-stale", router.effectiveConfigForSessionTelemetry(), true, time.Now().UTC(), false)
	if err != nil {
		t.Fatalf("commandSessionsWorldStateSection() error = %v", err)
	}
	if result.Item == nil {
		t.Fatal("stale handles were not cleared")
	}
	text := commandSessionsText(t, result.Item)
	if !strings.Contains(text, "None.") || strings.Contains(text, "1000: running") {
		t.Fatalf("cleared command sessions text = %q", text)
	}

	again, err := router.commandSessionsWorldStateSection(threadID, "turn-command-sessions-stale", router.effectiveConfigForSessionTelemetry(), true, time.Now().UTC(), false)
	if err != nil {
		t.Fatalf("second commandSessionsWorldStateSection() error = %v", err)
	}
	if again.Item != nil {
		t.Fatal("cleared snapshot was published twice")
	}
}
