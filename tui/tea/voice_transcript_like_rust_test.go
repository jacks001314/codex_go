package tea

import (
	"strings"
	"testing"
	"time"

	chatwidget "codex_go/tui/chatwidget"
)

// activeVoiceModel starts a voice session and marks the realtime backend as
// connected so transcript notifications become live captions.
func activeVoiceModel(t *testing.T) *Model {
	t.Helper()
	model, _, _ := newVoiceTestModel(t)
	model.handleVoiceNotification(chatwidget.VoiceNotification{Kind: chatwidget.VoiceNotificationStarted})
	model.handleVoiceAnswerResult(VoiceAnswerResultMsg{AttemptID: 7})
	return model
}

func voiceDelta(role chatwidget.VoiceTranscriptRole, text string) chatwidget.VoiceNotification {
	return chatwidget.VoiceNotification{Kind: chatwidget.VoiceNotificationTranscriptDelta, Role: role, Text: text}
}

func voiceDone(role chatwidget.VoiceTranscriptRole, text string) chatwidget.VoiceNotification {
	return chatwidget.VoiceNotification{Kind: chatwidget.VoiceNotificationTranscriptDone, Role: role, Text: text}
}

// Rust #43656 renders a streaming caption on the split-flap board, keeps
// requesting frames while tiles flip, and stops once the board settles.
func TestLiveVoiceTranscriptAnimatesAndStopsFramesLikeRust(t *testing.T) {
	base := time.Now()
	model := activeVoiceModel(t)
	model.now = func() time.Time { return base }

	if tick := model.handleVoiceNotification(voiceDelta(chatwidget.VoiceTranscriptAssistant, "GATE")); tick == nil {
		t.Fatal("the live caption did not schedule an animation frame")
	}
	if model.liveVoiceTranscript.cell == nil {
		t.Fatal("the streaming caption has no split-flap cell")
	}
	if strings.TrimSpace(model.lastTranscriptContent) == "" {
		t.Fatal("live caption missing from transcript")
	}

	model.now = func() time.Time { return base.Add(285 * time.Millisecond) }
	generation := model.liveVoiceTranscript.generation
	if next := model.handleVoiceTranscriptFrame(voiceTranscriptFrameMsg{generation: generation}); next == nil {
		t.Fatal("the frame chain stopped before the board settled")
	}
	if !strings.Contains(model.lastTranscriptContent, "Assistant (voice): GATE") {
		t.Fatalf("animated caption never showed its glyphs: %q", model.lastTranscriptContent)
	}

	model.now = func() time.Time { return base.Add(chatwidget.SplitFlapAnimationDuration) }
	if next := model.handleVoiceTranscriptFrame(voiceTranscriptFrameMsg{generation: generation}); next != nil {
		t.Fatal("the settled board kept requesting animation frames")
	}
}

// Appending text reuses the settled tiles instead of restarting the whole row.
func TestLiveVoiceTranscriptKeepsSettledTilesOnAppendLikeRust(t *testing.T) {
	base := time.Now()
	model := activeVoiceModel(t)
	model.now = func() time.Time { return base }
	model.handleVoiceNotification(voiceDelta(chatwidget.VoiceTranscriptUser, "GATE"))

	first := model.liveVoiceTranscript.cell
	if first == nil {
		t.Fatal("the streaming caption has no split-flap cell")
	}
	model.now = func() time.Time { return base.Add(300 * time.Millisecond) }
	model.handleVoiceNotification(voiceDelta(chatwidget.VoiceTranscriptUser, " 73"))

	next := model.liveVoiceTranscript.cell
	if next == nil || next == first {
		t.Fatal("an appended delta did not rebuild the live cell")
	}
	if len(next.Board.TileArrivals) <= len(first.Board.TileArrivals) ||
		next.Board.TileArrivals[0] != first.Board.TileArrivals[0] {
		t.Fatalf("appended caption restarted settled tiles: first=%#v next=%#v", first.Board.TileArrivals, next.Board.TileArrivals)
	}
}

// A sealed caption becomes an ordinary history cell and leaves no live board.
func TestLiveVoiceTranscriptSealsIntoHistoryCellLikeRust(t *testing.T) {
	model := activeVoiceModel(t)
	model.handleVoiceNotification(voiceDelta(chatwidget.VoiceTranscriptUser, "GATE"))
	model.handleVoiceNotification(voiceDone(chatwidget.VoiceTranscriptUser, "GATE 73"))

	if model.liveVoiceTranscript.cell != nil {
		t.Fatal("the sealed caption kept its live cell")
	}
	message := model.State.Messages[len(model.State.Messages)-1]
	if message.Role != "history" || !strings.Contains(message.Text, "GATE 73") {
		t.Fatalf("sealed caption = %#v", message)
	}
	if got := message.RawText; strings.Contains(got, "TEG") || !strings.Contains(got, "GATE 73") {
		t.Fatalf("sealed caption raw text = %q", got)
	}
}

// Reduced motion keeps assistant captions static and drops streamed user
// captions entirely, matching the Rust animations=false branch.
func TestLiveVoiceTranscriptRespectsReducedMotionLikeRust(t *testing.T) {
	model := activeVoiceModel(t)
	model.animationsEnabled = false

	if tick := model.handleVoiceNotification(voiceDelta(chatwidget.VoiceTranscriptAssistant, "GATE")); tick != nil {
		t.Fatal("reduced motion scheduled animation frames")
	}
	if !strings.Contains(model.lastTranscriptContent, "GATE") {
		t.Fatalf("static assistant caption missing: %q", model.lastTranscriptContent)
	}
	if _, animating := model.liveVoiceTranscript.cell.AnimationTick(); animating {
		t.Fatal("reduced-motion caption still animates")
	}

	model.handleVoiceNotification(voiceDelta(chatwidget.VoiceTranscriptUser, "spoken prompt"))
	if model.liveVoiceTranscript.cell != nil {
		t.Fatal("reduced motion rendered a streamed user caption")
	}
	if strings.Contains(model.lastTranscriptContent, "spoken prompt") {
		t.Fatalf("streamed user caption leaked into the transcript: %q", model.lastTranscriptContent)
	}
}
