package tea

// Live voice transcripts rendered with the split-flap board. This is the TUI
// wiring half of Rust #43656: chatwidget owns the tile animation, the model
// swaps in a fresh cell per delta so settled tiles survive an append, and a
// frame tick keeps redrawing until the board settles.

import (
	"strings"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/tui/chatwidget"
	historycell "codex_go/tui/history_cell"
)

// voiceTranscriptFrameMsg advances the live caption animation.
type voiceTranscriptFrameMsg struct {
	generation uint64
}

// liveVoiceTranscriptState holds the animated cell for the streaming caption.
type liveVoiceTranscriptState struct {
	cell       *chatwidget.SplitFlapTranscriptCell
	role       chatwidget.VoiceTranscriptRole
	text       string
	generation uint64
}

// voiceTranscriptFrameCmd schedules the next animation frame.
func voiceTranscriptFrameCmd(generation uint64) bubbletea.Cmd {
	return bubbletea.Tick(chatwidget.SplitFlapFrameInterval, func(time.Time) bubbletea.Msg {
		return voiceTranscriptFrameMsg{generation: generation}
	})
}

// syncLiveVoiceTranscript rebuilds the animated cell for the caption that is
// still streaming and returns the next animation frame while tiles flip.
func (m *Model) syncLiveVoiceTranscript() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	live := m.VoiceConversation.Live
	if live == nil || strings.TrimSpace(live.Text) == "" || !m.liveVoiceTranscriptVisibleThread() ||
		(live.Role == chatwidget.VoiceTranscriptUser && !m.animationsEnabled) {
		return m.clearLiveVoiceTranscript()
	}
	if m.liveVoiceTranscript.cell != nil && m.liveVoiceTranscript.role == live.Role && m.liveVoiceTranscript.text == live.Text {
		return nil
	}
	previous := m.liveVoiceTranscript.cell
	inner := historycell.NewSpokenHistoryCell(string(live.Role), live.Text)
	cell := chatwidget.NewSplitFlapTranscriptCell(inner, string(live.Role), previous, 0, m.animationsEnabled, m.currentTime())
	cell.Now = m.currentTime
	m.liveVoiceTranscript = liveVoiceTranscriptState{
		cell:       &cell,
		role:       live.Role,
		text:       live.Text,
		generation: m.liveVoiceTranscript.generation + 1,
	}
	m.refreshTranscript()
	if _, animating := cell.AnimationTick(); !animating {
		return nil
	}
	return voiceTranscriptFrameCmd(m.liveVoiceTranscript.generation)
}

// liveVoiceTranscriptVisibleThread keeps a parked session's caption off another
// thread's transcript.
func (m *Model) liveVoiceTranscriptVisibleThread() bool {
	if m == nil || m.State == nil {
		return false
	}
	return strings.TrimSpace(m.VoiceConversation.ThreadID) == strings.TrimSpace(m.State.ThreadID)
}

// clearLiveVoiceTranscript drops the streaming caption once it is sealed.
func (m *Model) clearLiveVoiceTranscript() bubbletea.Cmd {
	if m == nil || m.liveVoiceTranscript.cell == nil {
		return nil
	}
	m.liveVoiceTranscript = liveVoiceTranscriptState{generation: m.liveVoiceTranscript.generation}
	m.refreshTranscript()
	return nil
}

// handleVoiceTranscriptFrame redraws the current frame and schedules the next
// one until the board settles (Rust: stop requesting frames once settled).
func (m *Model) handleVoiceTranscriptFrame(msg voiceTranscriptFrameMsg) bubbletea.Cmd {
	if m == nil || m.liveVoiceTranscript.cell == nil || msg.generation != m.liveVoiceTranscript.generation {
		return nil
	}
	if _, animating := m.liveVoiceTranscript.cell.AnimationTick(); !animating {
		return nil
	}
	m.refreshTranscript()
	if _, animating := m.liveVoiceTranscript.cell.AnimationTick(); !animating {
		return nil
	}
	return voiceTranscriptFrameCmd(msg.generation)
}

// liveVoiceTranscriptLines projects the streaming caption for the transcript.
func (m *Model) liveVoiceTranscriptLines(width int) []string {
	if m == nil || m.liveVoiceTranscript.cell == nil {
		return nil
	}
	if m.rawOutput {
		return m.liveVoiceTranscript.cell.RawLines()
	}
	if width < 20 {
		width = 20
	}
	return m.liveVoiceTranscript.cell.DisplayLines(width)
}
