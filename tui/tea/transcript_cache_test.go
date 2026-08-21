package tea

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	codextui "codex_go/tui"
)

func TestRenderTranscriptCacheReusesUnchangedMessages(t *testing.T) {
	state := codextui.NewState(nil)
	for i := 0; i < 20; i++ {
		state.AddMessage(codextui.RoleUser, fmt.Sprintf("user %d", i))
		state.AddMessage(codextui.RoleAssistant, fmt.Sprintf("assistant %d", i))
	}
	var cache transcriptMessageCache
	first := renderTranscriptWithCache(&cache, state, false, 80, "dark", false)
	if !strings.Contains(first, "user 19") || !strings.Contains(first, "assistant 19") {
		t.Fatalf("first render missing tail:\n%s", first)
	}

	// Mutate only the streaming tail. Earlier messages should reuse cached lines.
	state.Messages[len(state.Messages)-1].Text += " +delta"
	second := renderTranscriptWithCache(&cache, state, false, 80, "dark", false)
	if !strings.Contains(second, "assistant 19 +delta") {
		t.Fatalf("cache render missing streamed delta:\n%s", second)
	}
	if strings.Contains(second, "user 19") && !strings.Contains(first, "user 19") {
		t.Fatalf("cache render lost an earlier message:\n%s", second)
	}

	// Re-rendering with no change must be byte-identical.
	third := renderTranscriptWithCache(&cache, state, false, 80, "dark", false)
	if third != second {
		t.Fatalf("cache render not stable across no-op renders:\n%s\n---\n%s", second, third)
	}
}

func TestRenderTranscriptCacheReflectsDirectMutation(t *testing.T) {
	state := codextui.NewState(nil)
	state.AddHistoryLines(
		[]string{"collapsed"},
		[]string{"$ command", "hidden detail"},
	)
	var cache transcriptMessageCache
	first := renderTranscriptWithCache(&cache, state, false, 80, "dark", true)
	if !strings.Contains(first, "hidden detail") {
		t.Fatalf("expanded render missing raw detail:\n%s", first)
	}

	// Mutate the message in place without bumping MessagesRevision; the cache
	// must still notice the content change.
	state.Messages[0].RawText += "\nlate running detail"
	second := renderTranscriptWithCache(&cache, state, false, 80, "dark", true)
	if !strings.Contains(second, "late running detail") {
		t.Fatalf("cache render did not reflect direct RawText mutation:\n%s", second)
	}
}

func TestSetAlternateScrollWritesDECPrivateMode(t *testing.T) {
	var buf bytes.Buffer
	m := NewModel(codextui.NewState(nil), Options{})
	m.petRuntime = newPetRuntime(&buf, nil)

	m.setAlternateScroll(true)
	if got := buf.String(); got != "\x1b[?1007h" {
		t.Fatalf("enable alternate scroll = %q, want %q", got, "\x1b[?1007h")
	}

	buf.Reset()
	m.setAlternateScroll(false)
	if got := buf.String(); got != "\x1b[?1007l" {
		t.Fatalf("disable alternate scroll = %q, want %q", got, "\x1b[?1007l")
	}
}
