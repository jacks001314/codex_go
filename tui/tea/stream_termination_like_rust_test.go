package tea

import (
	"errors"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/protocol"
	codextui "codex_go/tui"
	"codex_go/utils"
)

// Rust #45549: interrupting a turn keeps the already-received answer and plan
// source so an unfinished math block can reflow after a later resize or raw
// output switch.
func TestTurnInterruptionPreservesStreamedAnswerAndPlanLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{})
	state.SetStatus("running")

	model.applyThreadEvent(protocol.ThreadEvent{
		Type:  "item.delta",
		Delta: &protocol.Delta{ItemID: "answer-1", Text: "answer with $x +"},
	})
	model.applyThreadEvent(protocol.ThreadEvent{
		Type:  "item.plan.delta",
		Delta: &protocol.Delta{ItemID: "plan-1", Text: "plan with $y -"},
	})

	model.Update(TurnInterruptedMsg{})

	foundAnswer := false
	for _, message := range state.Messages {
		if message.Role == codextui.RoleAssistant && strings.Contains(message.Text, "answer with $x +") {
			foundAnswer = true
			break
		}
	}
	if !foundAnswer {
		t.Fatalf("interrupted transcript lost the streamed answer: %#v", state.Messages)
	}
	view := utils.StripANSI(model.View())
	if !strings.Contains(view, "plan with $y -") {
		t.Fatalf("interrupted transcript lost the streamed plan:\n%s", view)
	}
}

func TestTurnTerminationPreservesStreamSourceAcrossModesLikeRust(t *testing.T) {
	tests := []struct {
		name     string
		plan     bool
		finish   func(*Model) bubbletea.Cmd
		wantText string
	}{
		{
			name: "interrupted answer",
			finish: func(model *Model) bubbletea.Cmd {
				_, cmd := model.Update(TurnInterruptedMsg{})
				return cmd
			},
			wantText: "answer source",
		},
		{
			name: "failed plan",
			plan: true,
			finish: func(model *Model) bubbletea.Cmd {
				_, cmd := model.Update(TurnCompletedMsg{ThreadID: "thread-math", Err: errors.New("Server error")})
				return cmd
			},
			wantText: "plan source",
		},
		{
			name: "completed answer",
			finish: func(model *Model) bubbletea.Cmd {
				_, cmd := model.Update(TurnCompletedMsg{ThreadID: "thread-math", AssistantMessage: "done"})
				return cmd
			},
			wantText: "answer source",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := codextui.NewState(nil)
			state.SetThreadID("thread-math")
			state.SetStatus("running")
			model := NewModel(state, Options{Width: 80, Height: 30})
			if tc.plan {
				model.applyThreadEvent(protocol.ThreadEvent{
					Type:  "item.plan.delta",
					Delta: &protocol.Delta{ItemID: "plan-1", Text: "plan source $x +"},
				})
			} else {
				model.applyThreadEvent(protocol.ThreadEvent{
					Type:  "item.delta",
					Delta: &protocol.Delta{ItemID: "answer-1", Text: "answer source $x +"},
				})
			}
			tc.finish(model)

			if !modelTranscriptHasText(model, tc.wantText) {
				t.Fatalf("terminated transcript lost %q: %#v", tc.wantText, state.Messages)
			}
			narrow := utils.StripANSI(model.View())
			if !strings.Contains(narrow, tc.wantText) {
				t.Fatalf("narrow view lost %q:\n%s", tc.wantText, narrow)
			}
			model.toggleRawOutputMode()
			raw := utils.StripANSI(model.View())
			if !strings.Contains(raw, tc.wantText) {
				t.Fatalf("raw view lost %q:\n%s", tc.wantText, raw)
			}
		})
	}
}

func modelTranscriptHasText(model *Model, text string) bool {
	if model == nil || model.State == nil {
		return false
	}
	for _, message := range model.State.Messages {
		if strings.Contains(message.Text, text) || strings.Contains(message.RawText, text) {
			return true
		}
	}
	return false
}
