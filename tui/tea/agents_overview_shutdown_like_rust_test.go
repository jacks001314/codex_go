package tea

import (
	"errors"
	"strings"
	"testing"

	codextui "codex_go/tui"
)

// TestAgentsOverviewServerShutdownErrorLikeRust pins #52721: the command center
// explains that new sessions are paused and only promises automatic reconnect
// for non-embedded servers.
func TestAgentsOverviewServerShutdownErrorLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name              string
		options           Options
		wantReconnectText bool
	}{
		{name: "embedded", options: Options{Width: 100, Height: 30, LocalSession: true}},
		{name: "remote", options: Options{Width: 100, Height: 30, RemoteAppServer: true}, wantReconnectText: true},
		{name: "local daemon", options: Options{Width: 100, Height: 30, LocalDaemonSession: true}, wantReconnectText: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			model := NewModel(codextui.NewState(nil), testCase.options)
			model.applyAgentsOverviewNewSession(agentsOverviewNewSessionMsg{
				err: errors.New("Server is draining; retry after reconnecting"),
			})
			notice := model.agentsOverviewNotice
			if !strings.Contains(notice, "Codex's background server is shutting down") {
				t.Fatalf("notice = %q, want shutdown title", notice)
			}
			if !strings.Contains(notice, "New sessions are paused while active turns finish or are canceled. The default shutdown wait is up to one minute.") {
				t.Fatalf("notice = %q, want shutdown wait guidance", notice)
			}
			if got := strings.Contains(notice, "If the server restarts, Codex will reconnect automatically."); got != testCase.wantReconnectText {
				t.Fatalf("reconnect guidance = %v, want %v; notice=%q", got, testCase.wantReconnectText, notice)
			}
		})
	}
}

func TestAgentsOverviewShutdownErrorPreservesRetainedWorktreeGuidance(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30, RemoteAppServer: true})
	model.applyAgentsOverviewNewWorktree(agentsOverviewNewWorktreeMsg{
		err: errors.New("Failed to start session: Server is draining; retry after reconnecting A checkout was retained at C:\\worktrees\\task; remove it with `git worktree remove <checkout-path>` from the source repository if it is no longer needed."),
	})
	notice := model.agentsOverviewNotice
	if !strings.Contains(notice, "Codex's background server is shutting down") ||
		!strings.Contains(notice, "A checkout was retained at C:\\worktrees\\task") ||
		!strings.Contains(notice, "git worktree remove") {
		t.Fatalf("retained worktree shutdown notice = %q", notice)
	}
}
