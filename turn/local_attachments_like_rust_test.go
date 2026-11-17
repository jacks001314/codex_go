package turn

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Mirrors Rust #61962 ResolvedUserInput::from_client: local images and audio
// are captured on the submitting host, so the payload survives the source file
// disappearing while the input is queued. The path stays on the input for
// display.
func TestResolveTurnUserInputsCapturesLocalMediaLikeRust(t *testing.T) {
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "shot.png")
	audioPath := filepath.Join(dir, "clip.wav")
	imageBytes := []byte("\x89PNG\r\n\x1a\ncaptured-image")
	audioBytes := []byte("captured-audio")
	if err := os.WriteFile(imagePath, imageBytes, 0o600); err != nil {
		t.Fatalf("WriteFile(image) error = %v", err)
	}
	if err := os.WriteFile(audioPath, audioBytes, 0o600); err != nil {
		t.Fatalf("WriteFile(audio) error = %v", err)
	}
	original := []TurnUserInput{
		{Type: "localImage", Path: imagePath},
		{Type: "localAudio", Path: audioPath},
		{Type: "text", Text: "keep"},
	}
	resolved := ResolveTurnUserInputs(original)
	if original[0].LocalMediaResolved || original[1].LocalMediaResolved {
		t.Fatalf("ResolveTurnUserInputs mutated its caller's slice: %#v", original)
	}
	if err := os.Remove(imagePath); err != nil {
		t.Fatalf("Remove(image) error = %v", err)
	}
	if err := os.Remove(audioPath); err != nil {
		t.Fatalf("Remove(audio) error = %v", err)
	}
	if !resolved[0].LocalMediaResolved || resolved[0].LocalImageError != "" {
		t.Fatalf("image capture = %#v", resolved[0])
	}
	if !bytes.Equal(resolved[0].LocalImageBytes, imageBytes) {
		t.Fatalf("captured image bytes = %q, want %q", resolved[0].LocalImageBytes, imageBytes)
	}
	if resolved[0].Path != imagePath {
		t.Fatalf("image display path = %q, want %q", resolved[0].Path, imagePath)
	}
	wantAudioURL := "data:audio/wav;base64," + base64.StdEncoding.EncodeToString(audioBytes)
	if !resolved[1].LocalMediaResolved || resolved[1].LocalAudioURL != wantAudioURL {
		t.Fatalf("audio capture = %#v, want %q", resolved[1], wantAudioURL)
	}
	if resolved[1].Path != audioPath {
		t.Fatalf("audio display path = %q, want %q", resolved[1].Path, audioPath)
	}
	if resolved[2].Type != "text" || resolved[2].Text != "keep" {
		t.Fatalf("text input changed: %#v", resolved[2])
	}
}

// Mirrors Rust #61962's read-error placeholders, including the dedicated
// unsupported-format message that wins over the read error.
func TestResolveTurnUserInputsKeepsRustErrorTextLikeRust(t *testing.T) {
	dir := t.TempDir()
	missingImage := filepath.Join(dir, "missing.png")
	unsupportedAudio := filepath.Join(dir, "clip.flac")
	missingAudio := filepath.Join(dir, "missing.mp3")
	if err := os.WriteFile(unsupportedAudio, []byte("flac"), 0o600); err != nil {
		t.Fatalf("WriteFile(audio) error = %v", err)
	}
	resolved := ResolveTurnUserInputs([]TurnUserInput{
		{Type: "localImage", Path: missingImage},
		{Type: "localAudio", Path: unsupportedAudio},
		{Type: "localAudio", Path: missingAudio},
	})
	if !resolved[0].LocalMediaResolved {
		t.Fatalf("missing image was not captured as an error: %#v", resolved[0])
	}
	if want := "Codex could not read the local image at `" + missingImage + "`: "; !strings.HasPrefix(resolved[0].LocalImageError, want) {
		t.Fatalf("image error = %q, want prefix %q", resolved[0].LocalImageError, want)
	}
	wantUnsupported := "Codex cannot attach audio at `" + unsupportedAudio + "`: unsupported audio format; use wav, mp3, m4a, webm, or ogg."
	if resolved[1].LocalAudioError != wantUnsupported {
		t.Fatalf("unsupported audio error = %q, want %q", resolved[1].LocalAudioError, wantUnsupported)
	}
	if want := "Codex could not read the local audio at `" + missingAudio + "`: "; !strings.HasPrefix(resolved[2].LocalAudioError, want) {
		t.Fatalf("audio error = %q, want prefix %q", resolved[2].LocalAudioError, want)
	}
}

func TestReadBoundedLocalMediaEnforcesRustBound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(path, []byte("12345"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := readBoundedLocalMedia(path, 4, "image"); err == nil || !strings.Contains(err.Error(), "image input exceeds 4 bytes") {
		t.Fatalf("oversized read error = %v", err)
	}
	data, err := readBoundedLocalMedia(path, 5, "image")
	if err != nil || string(data) != "12345" {
		t.Fatalf("bounded read = %q, %v", data, err)
	}
}

func TestAudioMimeForPathMatchesRustExtensionTable(t *testing.T) {
	cases := map[string]string{
		"a.wav":         "audio/wav",
		"A.WAV":         "audio/wav",
		"a.mp3":         "audio/mpeg",
		"a.m4a":         "audio/mp4",
		"a.webm":        "audio/webm",
		"a.ogg":         "audio/ogg",
		"a.flac":        "",
		"audio":         "",
		".wav":          "",
		"nested/a.m4a":  "audio/mp4",
		"trailing.wav.": "",
	}
	for name, want := range cases {
		got, ok := AudioMimeForPath(name)
		if want == "" {
			if ok {
				t.Fatalf("AudioMimeForPath(%q) = %q, want no mime", name, got)
			}
			continue
		}
		if !ok || got != want {
			t.Fatalf("AudioMimeForPath(%q) = %q, %v; want %q", name, got, ok, want)
		}
	}
}

// Captured payloads are host-only: the wire form keeps the path and never
// carries the bytes.
func TestTurnUserInputCapturedPayloadStaysOffTheWire(t *testing.T) {
	encoded, err := json.Marshal(TurnUserInput{
		Type:               "localImage",
		Path:               "/tmp/shot.png",
		LocalImageBytes:    []byte("secret-bytes"),
		LocalAudioURL:      "data:audio/wav;base64,c2VjcmV0",
		LocalMediaResolved: true,
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if strings.Contains(string(encoded), base64.StdEncoding.EncodeToString([]byte("secret-bytes"))) || strings.Contains(string(encoded), "c2VjcmV0") {
		t.Fatalf("captured payload leaked to the wire: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"path":"/tmp/shot.png"`) {
		t.Fatalf("wire form dropped the display path: %s", encoded)
	}
	var decoded TurnUserInput
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded.LocalMediaResolved || len(decoded.LocalImageBytes) != 0 || decoded.LocalAudioURL != "" {
		t.Fatalf("decoded wire input kept captured state: %#v", decoded)
	}
}
