package tea

import (
	"bytes"
	"codex_go/protocol"
	codextui "codex_go/tui"
	"codex_go/tui/markdown"
	"codex_go/tui/sidebar_image"
	"codex_go/tui/transcript_image"
	"errors"
	"os"
	"strings"
	"testing"
)

func sidebarTestImage(t *testing.T) *sidebar_image.Image {
	t.Helper()
	data, err := os.ReadFile(writeTranscriptTestPNG(t, 320, 160))
	if err != nil {
		t.Fatal(err)
	}
	img, err := sidebar_image.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func TestSidebarImagePreviewAndTerminalFallbackLikeRust(t *testing.T) {
	for _, test := range []struct {
		name      string
		env       map[string]string
		cell      transcript_image.CellSize
		supported bool
	}{
		{"kitty", map[string]string{"KITTY_WINDOW_ID": "1"}, transcript_image.CellSize{}, true},
		{"iterm2", map[string]string{"TERM_PROGRAM": "iTerm.app", "TERM_PROGRAM_VERSION": "3.6.0"}, transcript_image.CellSize{}, true},
		{"old-iterm2", map[string]string{"TERM_PROGRAM": "iTerm.app", "TERM_PROGRAM_VERSION": "3.5.0"}, transcript_image.CellSize{}, false},
		{"zellij", map[string]string{"ZELLIJ": "1", "KITTY_WINDOW_ID": "1"}, transcript_image.CellSize{}, false},
		{"raw-output", map[string]string{"KITTY_WINDOW_ID": "1"}, transcript_image.CellSize{}, false},
		{"sixel", map[string]string{"TERM": "foot"}, transcript_image.CellSize{Width: 8, Height: 16}, true},
		{"sixel-no-cell", map[string]string{"TERM": "foot"}, transcript_image.CellSize{}, false},
		{"unsupported", map[string]string{"TERM": "dumb"}, transcript_image.CellSize{}, false},
		{"tmux-no-passthrough", map[string]string{"TMUX": "test", "TERM": "xterm-kitty"}, transcript_image.CellSize{}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			m, _ := newSidebarTabsModel(t)
			m.localSession = false // remote bytes must work without opening a client-local path
			m.rawOutput = test.name == "raw-output"
			m.petEnv = test.env
			m.transcriptImageCell = test.cell
			openChooserWithFiles(t, m, []markdown.SidebarFileLink{{Path: "/server/generated.png"}}, "")
			m.onSidebarDocument = func(SidebarDocumentRequest) SidebarDocumentView {
				return SidebarDocumentView{Path: "/server/generated.png", Image: sidebarTestImage(t)}
			}
			m.sidebarChoice = m.sidebarChooserFileStart()
			runTeaCmd(t, m, m.acceptSidebarChooser())
			painted := m.ViewContent()
			if !strings.Contains(painted, "PNG") {
				t.Fatalf("missing metadata: %s", painted)
			}
			m.transcriptImages.mu.Lock()
			targets := m.transcriptImages.targetsLocked()
			m.transcriptImages.mu.Unlock()
			if (len(targets) > 0) != test.supported {
				t.Fatalf("graphics targets=%d supported=%t", len(targets), test.supported)
			}
			if !test.supported {
				if !strings.Contains(painted, "Image preview unavailable") {
					t.Fatal("missing fallback")
				}
				return
			}
			var output bytes.Buffer
			m.transcriptImages.setOutput(&output)
			m.transcriptImages.beforeWrite()
			m.transcriptImages.afterWrite()
			if output.Len() == 0 {
				t.Fatal("runtime emitted no terminal graphics")
			}
			m.sideZoomed = true
			m.ViewContent()
			m.transcriptImages.mu.Lock()
			zoom := m.transcriptImages.targetsLocked()
			m.transcriptImages.mu.Unlock()
			if len(zoom) != 1 || zoom[0].key.x < 0 || zoom[0].key.x+zoom[0].key.columns > m.width {
				t.Fatalf("zoom placement: %#v", zoom)
			}
			m.closeSidebarDocumentTab(m.sidebarDocumentActive)
			m.ViewContent()
			m.transcriptImages.beforeWrite()
			m.transcriptImages.afterWrite()
			m.transcriptImages.mu.Lock()
			remaining := len(m.transcriptImages.drawn)
			m.transcriptImages.mu.Unlock()
			if remaining != 0 {
				t.Fatal("closed tab retained image placement")
			}
		})
	}
}

func TestGeneratedImagesFollowAnswerAndDeduplicateLikeRust(t *testing.T) {
	m := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30, LocalSession: true, PetEnv: map[string]string{"KITTY_WINDOW_ID": "1"}})
	m.ownedScreen = true
	path := writeTranscriptTestPNG(t, 32, 32)
	m.Update(ThreadEventMsg{Event: protocol.TurnStartedWithID("turn-1")})
	m.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.ImageGenerationItem("img", "completed", "old", path))})
	m.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.ImageGenerationItem("img", "completed", "new", path))})
	for _, msg := range m.State.Messages {
		if msg.ImagePath != "" {
			t.Fatal("image rendered before answer completion")
		}
	}
	m.Update(ThreadEventMsg{Event: protocol.ThreadEvent{Type: "item.completed", Item: &protocol.ThreadItem{ID: "answer", Type: "agent_message", Text: "Here is your picture.", Phase: "final_answer"}}})
	m.Update(ThreadEventMsg{Event: protocol.TurnCompletedWithID(protocol.Usage{}, "unrelated")})
	for _, msg := range m.State.Messages {
		if msg.ImagePath != "" {
			t.Fatal("unrelated completion flushed image")
		}
	}
	m.Update(ThreadEventMsg{Event: protocol.TurnCompletedWithID(protocol.Usage{}, "turn-1")})
	answer, image, count := -1, -1, 0
	for index, msg := range m.State.Messages {
		if strings.Contains(msg.Text, "Here is your picture.") {
			answer = index
		}
		if msg.ImagePath != "" {
			image = index
			count++
			if !strings.Contains(msg.Text, "new") {
				t.Fatal("duplicate did not replace image metadata")
			}
		}
	}
	if answer < 0 || image <= answer || count != 1 {
		t.Fatalf("ordering answer=%d image=%d count=%d", answer, image, count)
	}
	m.Update(TurnCompletedMsg{})
	if len(m.pendingGeneratedImages) != 0 {
		t.Fatal("completion retained pending image")
	}
}

// Failure/no-artifact results stay visible immediately; terminal mode preserves
// chronological scrollback, matching the Rust owned-transcript guard.
func TestGeneratedImageImmediateBranchesLikeRust(t *testing.T) {
	for _, owned := range []bool{true, false} {
		m := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 25})
		m.ownedScreen = owned
		m.currentTurnID = "turn"
		m.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.ImageGenerationItem("fail", "failed", "failure", "/missing.png"))})
		if len(m.State.Messages) == 0 {
			t.Fatal("failed generation hidden")
		}
		if !owned {
			m.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.ImageGenerationItem("ok", "completed", "output", "/missing.png"))})
			if len(m.pendingGeneratedImages) != 0 {
				t.Fatal("terminal scrollback queued output")
			}
		}
	}
}

func TestGeneratedImageFlushesOnFailedAndInterruptedTurnsLikeRust(t *testing.T) {
	for _, end := range []string{"error", "failed", "interrupted"} {
		t.Run(end, func(t *testing.T) {
			m := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30})
			m.ownedScreen = true
			m.Update(ThreadEventMsg{Event: protocol.TurnStartedWithID("turn")})
			m.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.ImageGenerationItem("img", "completed", "output", "/saved/image.png"))})
			switch end {
			case "error":
				m.Update(TurnCompletedMsg{Err: errors.New("upstream failed")})
			case "failed":
				m.Update(ThreadEventMsg{Event: protocol.TurnFailed("upstream failed")})
			case "interrupted":
				m.Update(TurnInterruptedMsg{})
			}
			count := 0
			for _, message := range m.State.Messages {
				if message.ImagePath != "" {
					count++
				}
			}
			if count != 1 || len(m.pendingGeneratedImages) != 0 {
				t.Fatalf("saved output lost: images=%d pending=%d", count, len(m.pendingGeneratedImages))
			}
		})
	}
}
