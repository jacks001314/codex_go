package tea

import (
	"fmt"
	"strings"
	"testing"

	codextui "codex_go/tui"
)

// These benchmarks track the per-frame cost that long sessions accumulate.
// Rust renders only the visible rows (transcript_view.rs) with a bounded
// LayoutCache, so the numbers here show how far the Go transcript still is from
// that shape.

func benchmarkTranscriptModel(tb testing.TB, messages int) *Model {
	tb.Helper()
	state := codextui.NewState(nil)
	for i := 0; i < messages; i++ {
		message := codextui.Message{
			Role: codextui.RoleUser,
			Text: fmt.Sprintf("user turn %d: %s", i, strings.Repeat("context ", 8)),
		}
		if i%2 == 1 {
			message = codextui.Message{
				Role: codextui.RoleAssistant,
				Text: fmt.Sprintf("## Answer %d\n\n%s\n\n- first\n- second\n", i, strings.Repeat("streamed prose ", 20)),
			}
		}
		message.RawText = message.Text
		state.Messages = append(state.Messages, message)
	}
	animations := false
	model := NewModel(state, Options{Width: 120, Height: 40, AnimationsEnabled: &animations})
	_ = model.View()
	return model
}

// The viewport paints the rows the renderer produced, so those rows must stay
// byte-identical to the content string the scroll anchor and find paths use.
func TestTranscriptViewportRowsMatchContent(t *testing.T) {
	model := benchmarkTranscriptModel(t, 40)
	assertTranscriptRowsMatch(t, model)

	model.State.Messages = append(model.State.Messages, codextui.Message{
		Role: codextui.RoleAssistant,
		Text: "partial answer that keeps streaming",
	})
	model.State.BumpMessagesRevision()
	_ = model.View()
	assertTranscriptRowsMatch(t, model)

	model.State.Messages = nil
	model.State.BumpMessagesRevision()
	_ = model.View()
	assertTranscriptRowsMatch(t, model)
}

func assertTranscriptRowsMatch(t *testing.T, model *Model) {
	t.Helper()
	if got := strings.Join(model.transcript.lines, "\n"); got != model.transcriptContent() {
		t.Fatalf("viewport rows diverge from transcript content:\nrows=%q\ncontent=%q", got, model.transcriptContent())
	}
}

func BenchmarkTranscriptViewIdle(b *testing.B) {
	for _, messages := range []int{100, 1000, 5000} {
		b.Run(fmt.Sprintf("messages=%d", messages), func(b *testing.B) {
			model := benchmarkTranscriptModel(b, messages)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = model.View()
			}
		})
	}
}

func BenchmarkTranscriptViewStreamingDelta(b *testing.B) {
	model := benchmarkTranscriptModel(b, 1000)
	last := len(model.State.Messages) - 1
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		model.State.Messages[last].Text += "x"
		model.State.BumpMessagesRevisionFrom(last)
		_ = model.View()
	}
}

// BenchmarkTranscriptViewStreamingScale tracks the per-frame cost against the
// length of the thread. Rust re-lays out only the live cell and paints one
// screenful, so a long session must not make a streamed delta linearly more
// expensive.
func BenchmarkTranscriptViewStreamingScale(b *testing.B) {
	for _, messages := range []int{100, 1000, 5000} {
		b.Run(fmt.Sprintf("messages=%d", messages), func(b *testing.B) {
			model := benchmarkTranscriptModel(b, messages)
			last := len(model.State.Messages) - 1
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				model.State.Messages[last].Text += "x"
				model.State.BumpMessagesRevisionFrom(last)
				_ = model.View()
			}
		})
	}
}

// BenchmarkStatusControlsRuntime guards the cached project-root lookup: the
// status line renders on every frame, so this must not walk ancestor
// directories per call.
func BenchmarkStatusControlsRuntime(b *testing.B) {
	model := benchmarkTranscriptModel(b, 0)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = model.statusControlsRuntime()
	}
}
