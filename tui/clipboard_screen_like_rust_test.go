package tui

import (
	"strings"
	"testing"
)

// Rust #58716 (codex-rs/tui/src/clipboard_copy.rs): GNU screen needs every DCS
// chunk to stay below its 256-byte string limit while reassembling to one
// continuous OSC 52 sequence.
func TestOSC52ScreenPassthroughChunksLikeRust(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{"short", "hello"},
		{"long unicode", strings.Repeat("复制到剪贴板 🚀 ", 40)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sequence, err := OSC52SequenceForTarget(tc.text, OSC52TargetScreen)
			if err != nil {
				t.Fatalf("OSC52SequenceForTarget() error = %v", err)
			}
			direct, err := OSC52SequenceForTarget(tc.text, OSC52TargetDirect)
			if err != nil {
				t.Fatalf("direct sequence error = %v", err)
			}
			chunks := splitScreenPassthrough(sequence)
			if len(chunks) == 0 {
				t.Fatal("screen passthrough produced no chunks")
			}
			var reassembled strings.Builder
			for index, chunk := range chunks {
				if len(chunk) > OSC52ScreenChunkSize {
					t.Fatalf("chunk %d body = %d bytes, want <= %d", index, len(chunk), OSC52ScreenChunkSize)
				}
				reassembled.WriteString(chunk)
			}
			if got := reassembled.String(); got != direct {
				t.Fatalf("reassembled OSC 52 = %q, want %q", got, direct)
			}
			if len(chunks) > 1 && !strings.Contains(tc.text, "🚀") {
				t.Fatalf("short payload split into %d chunks", len(chunks))
			}
		})
	}
}

func splitScreenPassthrough(sequence string) []string {
	var chunks []string
	for cursor := 0; cursor < len(sequence); {
		if !strings.HasPrefix(sequence[cursor:], "\x1bP") {
			return nil
		}
		start := cursor + 2
		end := strings.Index(sequence[start:], "\x1b\\")
		if end < 0 {
			return nil
		}
		chunks = append(chunks, sequence[start:start+end])
		cursor = start + end + 2
	}
	return chunks
}

func TestOSC52TargetPrecedenceLikeRust(t *testing.T) {
	if got := OSC52TargetForEnvironment(true, true); got != OSC52TargetTmux {
		t.Fatalf("tmux + screen target = %q, want tmux", got)
	}
	if got := OSC52TargetForEnvironment(false, true); got != OSC52TargetScreen {
		t.Fatalf("screen-only target = %q, want screen", got)
	}
	if got := OSC52TargetForEnvironment(false, false); got != OSC52TargetDirect {
		t.Fatalf("plain target = %q, want direct", got)
	}
}

func TestClipboardEnvironmentDetectsScreenLikeRust(t *testing.T) {
	screen := ClipboardEnvironmentFromMap(map[string]string{"STY": "1234.pts-0"}, false)
	if !screen.ScreenSession || screen.TmuxSession {
		t.Fatalf("screen environment = %+v", screen)
	}
	both := ClipboardEnvironmentFromMap(map[string]string{"STY": "1234.pts-0", "TMUX": "/tmp/tmux-1000/default,1,0"}, false)
	if both.ScreenSession || !both.TmuxSession {
		t.Fatalf("tmux environment = %+v", both)
	}
	plain := ClipboardEnvironmentFromMap(map[string]string{}, false)
	if plain.ScreenSession || plain.TmuxSession {
		t.Fatalf("plain environment = %+v", plain)
	}
}
