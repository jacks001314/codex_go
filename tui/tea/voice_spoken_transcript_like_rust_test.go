package tea

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
	historycell "codex_go/tui/history_cell"
)

// Rust 3caf9f9586 renders real voice captions through new_spoken_user_prompt
// and the spoken agent cell instead of the emoji-labelled preview cell. The
// live user prompt keeps its bold red chevron through the split-flap
// animation, and the sealed cell keeps the raw transcript text.
func TestVoiceTranscriptUsesSpokenUserPromptLikeRust(t *testing.T) {
	base := time.Now()
	model := activeVoiceModel(t)
	model.now = func() time.Time { return base }

	model.handleVoiceNotification(voiceDelta(chatwidget.VoiceTranscriptUser, "  hello"))
	if !strings.Contains(model.lastTranscriptContent, historycell.SpokenPromptMarkerSGR+"\u203a") {
		t.Fatalf("live spoken prompt lost its red chevron: %q", model.lastTranscriptContent)
	}
	if strings.Contains(model.lastTranscriptContent, "User (voice)") {
		t.Fatalf("live caption still used the emoji preview cell: %q", model.lastTranscriptContent)
	}

	model.now = func() time.Time { return base.Add(chatwidget.SplitFlapAnimationDuration) }
	model.refreshTranscript()
	if !strings.Contains(model.lastTranscriptContent, "hello") {
		t.Fatalf("settled spoken prompt missing its text: %q", model.lastTranscriptContent)
	}

	model.handleVoiceNotification(voiceDone(chatwidget.VoiceTranscriptUser, "  hello"))
	message := model.State.Messages[len(model.State.Messages)-1]
	if !strings.Contains(message.Text, historycell.SpokenPromptMarkerSGR+"\u203a") {
		t.Fatalf("sealed spoken prompt lost its red chevron: %q", message.Text)
	}
	if strings.Contains(message.Text, "User (voice)") {
		t.Fatalf("sealed spoken prompt used the emoji preview cell: %q", message.Text)
	}
	// Display drops the leading whitespace; the raw transcript keeps it.
	if got := strings.TrimSpace(message.RawText); got != "hello" {
		t.Fatalf("sealed spoken prompt raw text = %q", got)
	}
}

// Rust links spoken assistant artifacts only inside the local workspace: an
// existing allowed-extension file becomes an OSC 8 link, and an SSH session
// never offers local file links.
func TestVoiceAssistantTranscriptLinksWorkspaceArtifactsLikeRust(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "src", "lib.go"), []byte("package lib"), 0o644); err != nil {
		t.Fatal(err)
	}
	expected := ""
	if file, ok := historycell.ValidateTrustedWorkspaceFile(workspace, "src/lib.go"); ok {
		expected = tui.OSC8Hyperlink(file.URL, "src/lib.go")
	} else {
		t.Fatal("the fixture file was rejected")
	}

	model := activeVoiceModel(t)
	model.sessionCWD = workspace
	model.handleVoiceNotification(voiceDone(chatwidget.VoiceTranscriptAssistant, "I updated src/lib.go:12"))
	message := model.State.Messages[len(model.State.Messages)-1]
	if !strings.Contains(message.Text, expected) {
		t.Fatalf("assistant artifact was not linked:\n%q", message.Text)
	}
	if strings.Contains(message.RawText, "\x1b]8;;") {
		t.Fatalf("raw assistant transcript carried a hyperlink: %q", message.RawText)
	}

	remote := activeVoiceModel(t)
	remote.sessionCWD = workspace
	remote.clipboardEnvironment = &tui.ClipboardEnvironment{SSHSession: true}
	remote.handleVoiceNotification(voiceDone(chatwidget.VoiceTranscriptAssistant, "I updated src/lib.go:12"))
	remoteMessage := remote.State.Messages[len(remote.State.Messages)-1]
	if strings.Contains(remoteMessage.Text, "\x1b]8;;") {
		t.Fatalf("an SSH session linked a local workspace file:\n%q", remoteMessage.Text)
	}
	if !strings.Contains(remoteMessage.Text, "src/lib.go") {
		t.Fatalf("the SSH caption lost its text:\n%q", remoteMessage.Text)
	}
}
