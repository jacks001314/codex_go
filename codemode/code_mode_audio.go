package codemode

import (
	"encoding/base64"
	"encoding/binary"
	"strings"

	"codex_go/audioutil"

	"github.com/grafana/sobek"
)

const (
	audioHelperExpectsMessage = "audio expects a non-empty audio URL string, an object with audio_url, or a raw MCP audio block"
	invalidAudioURLMessage    = "Tool call failed: invalid audio output. Pass a base64 data URI instead"
	shortAudioOmissionText    = "Audio output omitted because the clip is shorter than 25 ms; use a longer clip."
)

// normalizeSobekAudio mirrors Rust's normalize_output_audio: non-data URLs are
// rejected, and WAV clips shorter than 25 ms become text instead of audio.
// Unknown or malformed audio formats retain the existing passthrough behavior.
func normalizeSobekAudio(value sobek.Value) (ContentItem, string) {
	audioURL := ""
	if _, ok := value.Export().(string); ok {
		audioURL = value.String()
	} else {
		object, ok := value.Export().(map[string]any)
		if !ok {
			return ContentItem{}, audioHelperExpectsMessage
		}
		if raw, exists := object["audio_url"]; exists {
			var valid bool
			audioURL, valid = raw.(string)
			if !valid {
				return ContentItem{}, audioHelperExpectsMessage
			}
		} else {
			itemType, ok := object["type"].(string)
			if !ok {
				return ContentItem{}, audioHelperExpectsMessage
			}
			if itemType != "audio" {
				return ContentItem{}, `audio only accepts MCP audio blocks, got "` + itemType + `"`
			}
			data, ok := object["data"].(string)
			if !ok || data == "" {
				return ContentItem{}, "audio expected MCP audio data"
			}
			if strings.HasPrefix(strings.ToLower(data), "data:") {
				audioURL = data
			} else {
				mimeType, _ := object["mimeType"].(string)
				if mimeType == "" {
					mimeType, _ = object["mime_type"].(string)
				}
				if mimeType == "" {
					mimeType = "application/octet-stream"
				}
				audioURL = "data:" + mimeType + ";base64," + data
			}
		}
	}
	if audioURL == "" {
		return ContentItem{}, audioHelperExpectsMessage
	}
	if !audioutil.IsDataURL(audioURL) {
		return ContentItem{}, invalidAudioURLMessage
	}
	if duration, ok := wavDurationSeconds(audioURL); ok && duration < 0.025 {
		return InputText(shortAudioOmissionText), ""
	}
	return InputAudio(audioURL), ""
}

// wavDurationSeconds measures the audio bytes actually present, using the WAV
// sample rate and block alignment. Unknown WAV encodings and malformed or
// oversized payloads return false so callers preserve the original audio.
func wavDurationSeconds(audioURL string) (float64, bool) {
	metadata, payload, ok := strings.Cut(audioURL, ",")
	if !ok {
		return 0, false
	}
	base64Encoded := false
	for _, part := range strings.Split(metadata, ";")[1:] {
		if strings.EqualFold(part, "base64") {
			base64Encoded = true
			break
		}
	}
	maxPayloadBytes := (audioutil.MaxPromptAudioInputBytes + 2) / 3 * 4
	if !base64Encoded || len(payload) > maxPayloadBytes {
		return 0, false
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil || len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return 0, false
	}

	var sampleRate uint32
	var blockAlign uint16
	formatKnown := false
	chunks := data[12:]
	for len(chunks) >= 8 {
		size := binary.LittleEndian.Uint32(chunks[4:8])
		remaining := chunks[8:]
		bodyLength := int64(size)
		if bodyLength > int64(len(remaining)) {
			bodyLength = int64(len(remaining))
		}
		chunk := remaining[:bodyLength]
		switch string(chunks[:4]) {
		case "fmt ":
			if len(chunk) < 16 {
				return 0, false
			}
			encoding := binary.LittleEndian.Uint16(chunk[:2])
			if encoding == 0xfffe {
				if len(chunk) < 40 || !isWaveExtensibleSubtype(chunk[26:40]) {
					return 0, false
				}
				encoding = binary.LittleEndian.Uint16(chunk[24:26])
			}
			if encoding != 1 && encoding != 3 {
				return 0, false
			}
			sampleRate = binary.LittleEndian.Uint32(chunk[4:8])
			blockAlign = binary.LittleEndian.Uint16(chunk[12:14])
			if sampleRate == 0 || blockAlign == 0 {
				return 0, false
			}
			formatKnown = true
		case "data":
			if !formatKnown {
				return 0, false
			}
			frames := int64(len(chunk)) / int64(blockAlign)
			return float64(frames) / float64(sampleRate), true
		}
		next := int64(size) + int64(size%2)
		if next > int64(len(remaining)) {
			return 0, false
		}
		chunks = remaining[next:]
	}
	return 0, false
}

func isWaveExtensibleSubtype(subtype []byte) bool {
	want := [...]byte{0, 0, 0, 0, 0x10, 0, 0x80, 0, 0, 0xaa, 0, 0x38, 0x9b, 0x71}
	if len(subtype) != len(want) {
		return false
	}
	for i := range want {
		if subtype[i] != want[i] {
			return false
		}
	}
	return true
}
