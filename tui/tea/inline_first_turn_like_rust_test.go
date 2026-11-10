package tea

import (
	"strings"
	"testing"

	bubbletea "charm.land/bubbletea/v2"

	"codex_go/protocol"
	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
)

// TestProgramStatusWriterUsesRawOutputLikeRust pins the writer used for the
// OSC 7501/21337 lifecycle status: the bytes have to reach the terminal without
// passing through the renderer. bubbletea.Printf wraps its body in a
// printLineMessage, and Bubble Tea feeds that to renderer.insertAbove, which
// scrolls the inline viewport by one row per state flip. Every scroll shifts
// the transcript mid-turn and drags the freshly painted first reply with it,
// so the reply ends up on screen twice.
func TestProgramStatusWriterUsesRawOutputLikeRust(t *testing.T) {
	const sequence = "\x1b]7501;state=working:app=codex\x1b\\"
	writer := terminalRawSequenceWriterOrDefault(nil)
	if cmd := writer(""); cmd != nil {
		t.Fatalf("empty sequence returned a command")
	}
	cmd := writer(sequence)
	if cmd == nil {
		t.Fatal("lifecycle sequence returned no command")
	}
	msg := cmd()
	raw, ok := msg.(bubbletea.RawMsg)
	if !ok {
		t.Fatalf("lifecycle sequence produced %T, want bubbletea.RawMsg", msg)
	}
	if got, want := raw.Msg, any(sequence); got != want {
		t.Fatalf("raw body = %q, want %q", got, want)
	}
}

// TestProgramStatusWriterHonoursInjectedWriterLikeRust keeps the injection
// point that tests and alternate front ends rely on.
func TestProgramStatusWriterHonoursInjectedWriterLikeRust(t *testing.T) {
	var writes []string
	writer := terminalRawSequenceWriterOrDefault(func(sequence string) bubbletea.Cmd {
		writes = append(writes, sequence)
		return nil
	})
	if cmd := writer("\x1b]7501;state=idle:app=codex\x1b\\"); cmd != nil {
		t.Fatalf("injected writer result was replaced")
	}
	if len(writes) != 1 {
		t.Fatalf("injected writer calls = %d, want 1", len(writes))
	}
}

// TestInlineFirstTurnRendersAnswerOnceLikeRust replays the first turn of an
// inline session, warning included: the reply must appear exactly once and the
// frame must stay inside the terminal so Bubble Tea never has to scroll while
// painting it.
func TestInlineFirstTurnRendersAnswerOnceLikeRust(t *testing.T) {
	const answer = "\u4f60\u597d\uff01\u6211\u5728 codex_go \u4ed3\u5e93\u91cc\u5f85\u547d\u3002\u9700\u8981\u6211\u505a\u4ec0\u4e48\uff1f\u6bd4\u5982\u5bf9\u9f50 Rust \u4e0a\u6e38\u3001\u8dd1\u5dee\u5206\u6d4b\u8bd5\u3001\u5199/\u6539 Go \u4ee3\u7801\uff0c\u8bf4\u4e00\u58f0\u5c31\u884c\u3002"
	state := codextui.NewState(nil)
	state.SetStatus("idle")
	model := NewModel(state, Options{
		Width: 188, Height: 45, AlternateScreenMode: "never",
		OnSubmitRequest: func(SubmitRequest) bubbletea.Cmd { return nil },
	})
	typeText(t, model, "ni hao a")
	model.Update(key(keymsg.KeyEnter))
	feedThreadEvents(model, protocol.TurnStarted())
	model.Update(WarningEventMsg{Message: "Model metadata for `deepseek-v4-flash` not found. Defaulting to fallback metadata; this can degrade performance and cause issues."})
	feedThreadEvents(model, protocol.AgentMessageDelta("msg_1", answer))
	feedThreadEvents(model, protocol.ItemCompleted(protocol.AgentMessageItem("msg_1", answer)))
	duration := int64(6000)
	model.Update(TurnCompletedMsg{ThreadID: "t1", DurationMS: &duration})

	view := model.viewString()
	if got := strings.Count(view, answer); got != 1 {
		t.Fatalf("answer appears %d times in the inline first-turn view, want 1", got)
	}
	if rows := frameRows(view); rows > model.height-1 {
		t.Fatalf("inline first-turn frame rows = %d, want at most %d in a %d-row terminal", rows, model.height-1, model.height)
	}
}
