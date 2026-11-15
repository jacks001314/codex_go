package tea

// Rust #61619: archiving the displayed task returns to the command center
// before the server answers, keeps the task hidden and its draft/approvals
// intact, and restores the task when the archive request fails.

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
	agentsoverview "codex_go/tui/agents_overview"
	"codex_go/tui/keymsg"
)

func currentArchiveTestModel(t *testing.T, archive func() error) *Model {
	t.Helper()
	model := NewModel(codextui.NewState(nil), Options{
		Width:  120,
		Height: 24,
		SessionPickerItems: []codextui.SessionSummary{
			{ThreadID: "archive-me", Title: "Archive me"},
		},
		OnAgentsOverviewRefresh: func(string) ([]agentsoverview.Row, error) {
			return []agentsoverview.Row{
				{ThreadID: "archive-me", Name: "Archive me"},
				{ThreadID: "keep-me", Name: "Keep me"},
			}, nil
		},
		OnSessionAction: func(selection codextui.SessionSelection) (*codextui.SessionSummary, error) {
			if selection.Kind != codextui.SessionSelectionArchive || selection.Target.ThreadID != "archive-me" {
				return nil, errors.New("unexpected archive selection")
			}
			if archive == nil {
				return nil, nil
			}
			return nil, archive()
		},
	})
	model.remoteAppServer = true
	model.State.SetThreadID("archive-me")
	return model
}

func confirmCurrentArchive(t *testing.T, model *Model) (*Model, bubbletea.Cmd) {
	t.Helper()
	model.openCurrentSessionActionConfirmation(codextui.SessionSelectionArchive)
	if model.modal == nil {
		t.Fatal("expected the archive confirmation")
	}
	model.Update(key(keymsg.KeyDown))
	updated, cmd := model.Update(key(keymsg.KeyEnter))
	return updated.(*Model), cmd
}

// collectBatchMsgs runs every command in a batch concurrently and streams the
// resulting messages, so a blocking archive request cannot stall the others.
func collectBatchMsgs(cmd bubbletea.Cmd) <-chan bubbletea.Msg {
	out := make(chan bubbletea.Msg, 8)
	go func() {
		defer close(out)
		if cmd == nil {
			return
		}
		msg := cmd()
		batch, ok := msg.(bubbletea.BatchMsg)
		if !ok {
			if msg != nil {
				out <- msg
			}
			return
		}
		var wg sync.WaitGroup
		for _, sub := range batch {
			if sub == nil {
				continue
			}
			wg.Add(1)
			go func(command bubbletea.Cmd) {
				defer wg.Done()
				if produced := command(); produced != nil {
					out <- produced
				}
			}(sub)
		}
		wg.Wait()
	}()
	return out
}

// runTeaCmdTree also executes the commands returned by Update, which is how
// bubbletea delivers a follow-up batch (side cleanup -> archive request).
func runTeaCmdTree(t *testing.T, model *Model, cmd bubbletea.Cmd) *Model {
	t.Helper()
	queue := []bubbletea.Cmd{cmd}
	for steps := 0; len(queue) > 0 && steps < 100; steps++ {
		current := queue[0]
		queue = queue[1:]
		if current == nil {
			continue
		}
		msg := current()
		if batch, ok := msg.(bubbletea.BatchMsg); ok {
			queue = append(queue, batch...)
			continue
		}
		if msg == nil {
			continue
		}
		updated, next := model.Update(msg)
		model = updated.(*Model)
		queue = append(queue, next)
	}
	return model
}

func TestCurrentArchiveOpensCommandCenterBeforeServerRepliesLikeRust(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	model := currentArchiveTestModel(t, func() error {
		close(started)
		<-release
		return nil
	})
	typeText(t, model, "Unsent draft")

	model, cmd := confirmCurrentArchive(t, model)
	if cmd == nil {
		t.Fatal("confirmed archive returned no command")
	}
	// The confirmation must not wait for the archive RPC.
	if model.agentsOverview == nil {
		t.Fatal("archive did not open the command center")
	}
	if got := strings.TrimSpace(model.State.ThreadID); got != "" {
		t.Fatalf("displayed thread after archive = %q, want detached", got)
	}
	if got := model.agentsOverviewDrafts["archive-me"]; got != "Unsent draft" {
		t.Fatalf("preserved draft = %q, want the unsent input", got)
	}
	for _, visible := range model.agentsOverview.VisibleIndices() {
		if model.agentsOverview.Rows[visible].ThreadID == "archive-me" {
			t.Fatal("pending archive stayed visible in the command center")
		}
	}

	msgs := collectBatchMsgs(cmd)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("archive request never started")
	}
	earlyDeadline := time.After(20 * time.Millisecond)
	early := false
	for !early {
		select {
		case msg, ok := <-msgs:
			if !ok {
				early = true
				continue
			}
			if _, isArchive := msg.(currentThreadArchiveMsg); isArchive {
				t.Fatalf("archive finished before the server replied: %#v", msg)
			}
		case <-earlyDeadline:
			early = true
		}
	}
	close(release)

	for msg := range msgs {
		updated, _ := model.Update(msg)
		model = updated.(*Model)
	}
	if model.currentThreadArchivePending("archive-me") {
		t.Fatal("archive stayed pending after completion")
	}
	if _, ok := model.agentsOverviewDrafts["archive-me"]; ok {
		t.Fatal("successful archive kept the task draft")
	}
	if !model.agentsOverviewRemoved["archive-me"] {
		t.Fatal("successful archive did not retire the task")
	}
	found := false
	for _, item := range model.sessionItems {
		if item.ThreadID == "archive-me" && item.Archived {
			found = true
		}
	}
	if !found {
		t.Fatal("archived task missing from the session list")
	}
}

func TestCurrentArchiveFailureRestoresTaskAndDraftLikeRust(t *testing.T) {
	model := currentArchiveTestModel(t, func() error {
		return errors.New("Archive rejected")
	})
	typeText(t, model, "Unsent draft")

	model, cmd := confirmCurrentArchive(t, model)
	if cmd == nil {
		t.Fatal("confirmed archive returned no command")
	}
	model = runTeaCmdTree(t, model, cmd)

	if model.agentsOverview == nil {
		t.Fatal("failed archive left the command center")
	}
	if got := strings.TrimSpace(model.State.ThreadID); got != "" {
		t.Fatalf("failed archive attached thread = %q, want the command center", got)
	}
	if !strings.Contains(model.agentsOverviewNotice, "Failed to archive thread archive-me: Archive rejected") {
		t.Fatalf("archive failure notice = %q", model.agentsOverviewNotice)
	}
	if visible := model.agentsOverview.VisibleIndices(); len(visible) != 2 {
		t.Fatalf("failed archive left the task hidden: visible=%v", visible)
	}
	if got := model.agentsOverviewDrafts["archive-me"]; got != "Unsent draft" {
		t.Fatalf("failed archive dropped the draft: %q", got)
	}
	if model.currentThreadArchivePending("archive-me") {
		t.Fatal("failed archive stayed pending")
	}
}

func TestCurrentArchiveWaitsForSideChatCleanupLikeRust(t *testing.T) {
	for _, closeErr := range []error{nil, errors.New("Unsubscribe rejected")} {
		name := "success"
		if closeErr != nil {
			name = "failure"
		}
		t.Run(name, func(t *testing.T) {
			archives := 0
			model := currentArchiveTestModel(t, func() error {
				archives++
				return nil
			})
			model.onCloseSide = func(params SideCloseParams) (SideCloseResponse, error) {
				if params.ParentThreadID != "archive-me" || params.SideThreadID != "side-1" {
					t.Fatalf("side close params = %#v", params)
				}
				if closeErr != nil {
					return SideCloseResponse{}, closeErr
				}
				return SideCloseResponse{}, nil
			}
			model.retainSideConversation(&activeSideConversation{
				ParentThreadID: "archive-me",
				SideThreadID:   "side-1",
			})
			typeText(t, model, "Unsent draft")

			model, cmd := confirmCurrentArchive(t, model)
			if cmd == nil {
				t.Fatal("confirmed archive returned no command")
			}
			if model.footerHintOverride != "Archiving\u2026" {
				t.Fatalf("footer override = %q, want archive progress", model.footerHintOverride)
			}
			if got := strings.TrimSpace(model.State.ThreadID); got != "archive-me" {
				t.Fatalf("side cleanup detached the task early: %q", got)
			}
			model = runTeaCmdTree(t, model, cmd)

			if closeErr != nil {
				if archives != 0 {
					t.Fatalf("archive ran after a failed side cleanup: %d", archives)
				}
				if got := strings.TrimSpace(model.State.ThreadID); got != "archive-me" {
					t.Fatalf("failed side cleanup lost the task: %q", got)
				}
				if !strings.Contains(model.notice, "Failed to archive current thread: Unsubscribe rejected") {
					t.Fatalf("cleanup failure notice = %q", model.notice)
				}
				if model.footerHintOverride != "" {
					t.Fatalf("cleanup failure kept footer override %q", model.footerHintOverride)
				}
				if model.retainedSideConversationForParent("archive-me") == nil {
					t.Fatal("failed cleanup dropped the side chat")
				}
				if got := model.composerText(); got != "Unsent draft" {
					t.Fatalf("failed cleanup dropped the draft: %q", got)
				}
				return
			}
			if archives != 1 {
				t.Fatalf("archive calls = %d, want one", archives)
			}
			if model.agentsOverview == nil {
				t.Fatal("cleaned side chat did not open the command center")
			}
			if model.retainedSideConversationForParent("archive-me") != nil {
				t.Fatal("archived task kept its side chat")
			}
			if model.footerHintOverride != "" {
				t.Fatalf("footer override = %q after cleanup", model.footerHintOverride)
			}
		})
	}
}

func TestPendingArchiveRefusesNavigationLikeRust(t *testing.T) {
	model := currentArchiveTestModel(t, nil)
	model.pendingCurrentArchives = map[string]*pendingCurrentArchive{
		"archive-me": {threadID: "archive-me", done: make(chan struct{})},
	}
	model.agentsOverview = agentsoverview.New([]agentsoverview.Row{
		{ThreadID: "archive-me", Name: "Archive me"},
		{ThreadID: "keep-me", Name: "Keep me"},
	}, "keep-me", false)

	if cmd := model.openAgentsOverviewThread("archive-me"); cmd != nil {
		t.Fatal("pending archive accepted a dashboard selection")
	}
	if model.agentsOverview == nil {
		t.Fatal("pending archive selection closed the dashboard")
	}
	if !strings.Contains(model.notice, "This task is still being archived.") {
		t.Fatalf("selection notice = %q", model.notice)
	}

	decision, notice, complete := model.applyResumePickerSelection(codextui.SessionSelection{
		Kind:   codextui.SessionSelectionResume,
		Target: codextui.SessionTarget{ThreadID: "archive-me"},
	})
	if complete || decision != nil {
		t.Fatalf("pending archive resumed: decision=%#v complete=%v", decision, complete)
	}
	if !strings.Contains(notice, "This task is still being archived.") {
		t.Fatalf("resume notice = %q", notice)
	}
}

func TestQuitWaitsForPendingArchivesLikeRust(t *testing.T) {
	previous := currentArchiveExitWait
	currentArchiveExitWait = 20 * time.Millisecond
	t.Cleanup(func() { currentArchiveExitWait = previous })

	model := NewModel(codextui.NewState(nil), Options{Width: 80, Height: 24})
	unconfirmed := &pendingCurrentArchive{threadID: "pending", done: make(chan struct{})}
	failed := &pendingCurrentArchive{threadID: "failed", done: make(chan struct{})}
	failed.err = errors.New("permission denied")
	close(failed.done)
	model.pendingCurrentArchives = map[string]*pendingCurrentArchive{
		"pending": unconfirmed,
		"failed":  failed,
	}

	cmd := model.quitCmd()
	if cmd == nil {
		t.Fatal("quit with pending archives returned no command")
	}
	msg, ok := cmd().(archiveWaitFinishedMsg)
	if !ok {
		t.Fatalf("quit message = %T, want archiveWaitFinishedMsg", msg)
	}
	joined := strings.Join(msg.warnings, "\n")
	if !strings.Contains(joined, "Failed to archive thread failed: permission denied") {
		t.Fatalf("failed archive warning missing: %q", joined)
	}
	if !strings.Contains(joined, "Archive of thread pending was not confirmed.") {
		t.Fatalf("unconfirmed archive warning missing: %q", joined)
	}
	_, quit := model.Update(msg)
	if quit == nil {
		t.Fatal("archive wait did not finish with a quit")
	}
	if _, ok := quit().(bubbletea.QuitMsg); !ok {
		t.Fatalf("archive wait finished with %T, want QuitMsg", quit())
	}
}

func TestQuitWithoutPendingArchivesQuitsImmediately(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 80, Height: 24})
	cmd := model.quitCmd()
	if cmd == nil {
		t.Fatal("quit returned no command")
	}
	if _, ok := cmd().(bubbletea.QuitMsg); !ok {
		t.Fatalf("quit returned %T, want QuitMsg", cmd())
	}
}
