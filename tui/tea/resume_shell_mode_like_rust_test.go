package tea

import (
	codextui "codex_go/tui"
	"testing"
)

func TestResumePickerPreservesDraftShellModeLikeRust(t *testing.T) {
	for _, shell := range []bool{false, true} {
		model := resumePickerTestModel(t, func(selection codextui.SessionSelection) (SessionResumeResponse, error) {
			return SessionResumeResponse{Status: "idle", Summary: &codextui.SessionSummary{ThreadID: selection.Target.ThreadID, CWD: `D:\repo`}}, nil
		})
		model.setComposerPromptWithMode("!echo original", shell)
		for _, target := range []string{"thread-new", "root-1"} {
			decision, notice, complete := model.applyResumePickerSelection(resumeThreadSelection(target))
			if decision == nil || !complete {
				t.Fatalf("resume %s: %s", target, notice)
			}
		}
		if model.composerText() != "!echo original" || model.isBangShellCommand() != shell {
			t.Fatalf("shell=%v restored text=%q shell=%v", shell, model.composerText(), model.isBangShellCommand())
		}
	}
}

func TestDashboardRestorePreservesDraftShellModeLikeRust(t *testing.T) {
	for _, shell := range []bool{false, true} {
		state := codextui.NewState(nil)
		state.SetThreadID("source")
		model := NewModel(state, Options{Width: 120, Height: 24, OnSwitchAgent: func(threadID string) (AgentThreadSwitchResponse, error) {
			return AgentThreadSwitchResponse{Entry: codextui.AgentThreadEntry{ThreadID: threadID}, Status: "idle"}, nil
		}})
		model.setComposerPromptWithMode("!echo original", shell)
		model.captureAgentsOverviewDraft("target")
		model.resetComposer()
		cmd := model.openAgentsOverviewThread("target")
		if cmd == nil {
			t.Fatal("missing switch command")
		}
		model.Update(cmd())
		if model.composerText() != "!echo original" || model.isBangShellCommand() != shell {
			t.Fatalf("shell=%v restored text=%q shell=%v", shell, model.composerText(), model.isBangShellCommand())
		}
	}
}
