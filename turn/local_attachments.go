package turn

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"codex_go/audioutil"
	"codex_go/utils"
)

// Local attachments are captured by the submitting host before queued input
// enters the session (Rust #61962 ResolvedUserInput::from_client). The payload
// lives next to the original path: the path is what display and diagnostics
// report, the payload is what a model message renders.

const unsupportedLocalAudioFormatText = "unsupported audio format"

// LocalImageReadErrorText is the placeholder Rust renders when a client local
// image could not be captured.
func LocalImageReadErrorText(path string, err error) string {
	return fmt.Sprintf("Codex could not read the local image at `%s`: %v", path, err)
}

// LocalAudioReadErrorText is the placeholder Rust renders when a client local
// audio file could not be captured. An unsupported extension keeps its own
// message even when the file is also unreadable, because the extension is
// rejected before the read happens.
func LocalAudioReadErrorText(path string, err error) string {
	if _, ok := AudioMimeForPath(path); !ok {
		return fmt.Sprintf("Codex cannot attach audio at `%s`: unsupported audio format; use wav, mp3, m4a, webm, or ogg.", path)
	}
	return fmt.Sprintf("Codex could not read the local audio at `%s`: %v", path, err)
}

// AudioMimeForPath mirrors Rust user_input::local_media::audio_mime_for_path:
// the client-facing audio MIME comes from the file extension, not content
// sniffing, and an unsupported extension is an error rather than a guess.
func AudioMimeForPath(path string) (string, bool) {
	switch strings.ToLower(rustPathExtension(path)) {
	case "wav":
		return "audio/wav", true
	case "mp3":
		return "audio/mpeg", true
	case "m4a":
		return "audio/mp4", true
	case "webm":
		return "audio/webm", true
	case "ogg":
		return "audio/ogg", true
	default:
		return "", false
	}
}

// rustPathExtension matches std::path::Path::extension for the cases that
// matter here: a leading dot alone is not an extension, and "name." has an
// empty extension.
func rustPathExtension(path string) string {
	base := filepath.Base(strings.TrimSpace(path))
	if base == "" || base == "." || base == ".." {
		return ""
	}
	if base[0] == '.' && strings.IndexByte(base[1:], '.') < 0 {
		return ""
	}
	index := strings.LastIndexByte(base, '.')
	if index < 0 {
		return ""
	}
	return base[index+1:]
}

// ReadLocalImageBytes reads one client local image within Rust's prompt image
// size bound.
func ReadLocalImageBytes(path string) ([]byte, error) {
	return readBoundedLocalMedia(path, int64(utils.MaxPromptImageInputBytes), "image")
}

// ReadLocalAudioURL reads one client local audio file within Rust's prompt
// audio size bound and returns its `data:` URL.
func ReadLocalAudioURL(path string) (string, error) {
	mime, ok := AudioMimeForPath(path)
	if !ok {
		return "", fmt.Errorf("%s", unsupportedLocalAudioFormatText)
	}
	data, err := readBoundedLocalMedia(path, int64(audioutil.MaxPromptAudioInputBytes), "audio")
	if err != nil {
		return "", err
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

func readBoundedLocalMedia(path string, maxBytes int64, kind string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > maxBytes {
		return nil, fmt.Errorf("%s input exceeds %d bytes", kind, maxBytes)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("%s input exceeds %d bytes", kind, maxBytes)
	}
	return data, nil
}

// ResolveTurnUserInputs captures client local attachments on the submitting
// host (Rust #61962 codex_thread::resolve_client_inputs). Local image bytes and
// local audio data URLs are recorded on the input, and read failures are kept
// as the exact placeholder text the model message will carry. Inputs are
// returned as a copy so the caller's slice is never rewritten in place.
func ResolveTurnUserInputs(inputs []TurnUserInput) []TurnUserInput {
	if len(inputs) == 0 {
		return inputs
	}
	resolved := make([]TurnUserInput, len(inputs))
	copy(resolved, inputs)
	for i := range resolved {
		input := &resolved[i]
		if input.LocalMediaResolved {
			continue
		}
		inputType := strings.TrimSpace(input.Type)
		path := strings.TrimSpace(input.Path)
		if path == "" {
			continue
		}
		switch {
		case strings.EqualFold(inputType, "localImage"):
			input.LocalMediaResolved = true
			data, err := ReadLocalImageBytes(path)
			if err != nil {
				input.LocalImageError = LocalImageReadErrorText(path, err)
				continue
			}
			input.LocalImageBytes = data
		case strings.EqualFold(inputType, "localAudio"):
			input.LocalMediaResolved = true
			audioURL, err := ReadLocalAudioURL(path)
			if err != nil {
				input.LocalAudioError = LocalAudioReadErrorText(path, err)
				continue
			}
			input.LocalAudioURL = audioURL
		}
	}
	return resolved
}
