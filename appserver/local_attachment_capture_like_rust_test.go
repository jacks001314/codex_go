package appserver

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

// Mirrors Rust #61962: a client local image/audio is captured on the
// submitting host before the turn waits for admission, so deleting the source
// files while the turn is queued cannot drop the media from the model request.
func TestTurnStartCapturesLocalAttachmentsBeforeAdmissionLikeRust(t *testing.T) {
	router, agent, _, store := newAdditionalContextTestRouter(t, "")
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "shot.png")
	audioPath := filepath.Join(dir, "clip.wav")
	writeTestPNG(t, imagePath)
	audioBytes := []byte("captured-audio-bytes")
	if err := os.WriteFile(audioPath, audioBytes, 0o600); err != nil {
		t.Fatalf("WriteFile(audio) error = %v", err)
	}
	threadID := session.ThreadID("thread-local-attachments")
	record := &session.Record{
		ID:        threadID,
		SessionID: string(threadID),
		Metadata:  session.Metadata{HistoryMode: "legacy", CWD: dir},
	}
	if err := store.Create(record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	response := router.Handle(requestWithParams(t, IntID(1), MethodTurnStart, turn.TurnStartParams{
		ThreadID: string(threadID),
		Prompt:   "look at these",
		Input: []turn.TurnUserInput{
			{Type: "localImage", Path: imagePath},
			{Type: "localAudio", Path: audioPath},
		},
	}))
	if response.Error != nil {
		t.Fatalf("turn/start error = %+v", response.Error)
	}
	// The submitting host already holds the payloads; the admitted turn must
	// render from them instead of re-reading these paths.
	if err := os.Remove(imagePath); err != nil {
		t.Fatalf("Remove(image) error = %v", err)
	}
	if err := os.Remove(audioPath); err != nil {
		t.Fatalf("Remove(audio) error = %v", err)
	}

	var request model.AgentRequest
	select {
	case request = <-agent.requests:
	case <-time.After(10 * time.Second):
		t.Fatal("turn runtime did not issue a model request")
	}
	content := userMessageContentForCapturedAttachmentTest(t, request.InputItems)
	imageURL := ""
	audioURL := ""
	texts := make([]string, 0, len(content))
	for _, block := range content {
		switch strings.TrimSpace(stringFromAny(block["type"])) {
		case "input_image":
			imageURL = stringFromAny(block["image_url"])
		case "input_audio":
			audioURL = stringFromAny(block["audio_url"])
		case "input_text":
			texts = append(texts, stringFromAny(block["text"]))
		}
	}
	if !strings.HasPrefix(imageURL, "data:image/png;base64,") || len(imageURL) <= len("data:image/png;base64,") {
		t.Fatalf("captured image block = %q, want a png data URL (content = %#v)", imageURL, content)
	}
	wantAudioURL := "data:audio/wav;base64," + base64.StdEncoding.EncodeToString(audioBytes)
	if audioURL != wantAudioURL {
		t.Fatalf("captured audio block = %q, want %q", audioURL, wantAudioURL)
	}
	if !containsText(texts, `<image name=[Image #1] path="`+imagePath+`">`) {
		t.Fatalf("image label missing from %#v", texts)
	}
	if !containsText(texts, `<audio name=[Audio #1] path="`+audioPath+`">`) || !containsText(texts, "</audio>") {
		t.Fatalf("audio labels missing from %#v", texts)
	}
	for _, text := range texts {
		if strings.HasPrefix(text, "Codex could not read the local") || strings.HasPrefix(text, "Codex cannot attach audio") {
			t.Fatalf("captured attachment degraded to an error placeholder: %q", text)
		}
	}
}

// Rust renders a local audio input as an open label, the audio block, and a
// close label. An unsupported extension becomes the dedicated format error.
func TestLocalAudioInputContentBlocksMatchRustLikeRust(t *testing.T) {
	dir := t.TempDir()
	audioPath := filepath.Join(dir, "clip.wav")
	if err := os.WriteFile(audioPath, []byte("audio"), 0o600); err != nil {
		t.Fatalf("WriteFile(audio) error = %v", err)
	}
	unsupported := filepath.Join(dir, "clip.flac")
	if err := os.WriteFile(unsupported, []byte("audio"), 0o600); err != nil {
		t.Fatalf("WriteFile(audio) error = %v", err)
	}
	content := inputContentFromTurnUserInputs("", []turn.TurnUserInput{
		{Type: "localAudio", Path: audioPath},
		{Type: "localAudio", Path: unsupported},
	})
	if len(content) != 4 {
		t.Fatalf("content = %#v, want four blocks", content)
	}
	if stringFromAny(content[0]["text"]) != `<audio name=[Audio #1] path="`+audioPath+`">` {
		t.Fatalf("audio open label = %#v", content[0])
	}
	if stringFromAny(content[1]["type"]) != "input_audio" ||
		stringFromAny(content[1]["audio_url"]) != "data:audio/wav;base64,"+base64.StdEncoding.EncodeToString([]byte("audio")) {
		t.Fatalf("audio block = %#v", content[1])
	}
	if stringFromAny(content[2]["text"]) != "</audio>" {
		t.Fatalf("audio close label = %#v", content[2])
	}
	if want := "Codex cannot attach audio at `" + unsupported + "`: unsupported audio format; use wav, mp3, m4a, webm, or ogg."; stringFromAny(content[3]["text"]) != want {
		t.Fatalf("unsupported audio placeholder = %q, want %q", content[3]["text"], want)
	}
}

func writeTestPNG(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatalf("png.Encode() error = %v", err)
	}
	if err := os.WriteFile(path, buffer.Bytes(), 0o600); err != nil {
		t.Fatalf("WriteFile(image) error = %v", err)
	}
}

func userMessageContentForCapturedAttachmentTest(t *testing.T, items []any) []map[string]any {
	t.Helper()
	content := make([]map[string]any, 0, len(items))
	for _, item := range items {
		message, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if stringFromAny(message["type"]) != "message" || stringFromAny(message["role"]) != "user" {
			continue
		}
		blocks, _ := message["content"].([]map[string]any)
		content = append(content, blocks...)
	}
	if len(content) == 0 {
		t.Fatalf("no user message in model request items: %#v", items)
	}
	return content
}

func containsText(texts []string, want string) bool {
	for _, text := range texts {
		if text == want {
			return true
		}
	}
	return false
}
