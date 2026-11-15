package model

import (
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"testing"

	"codex_go/eventmap"
	"codex_go/utils"
)

func replayTestJPEGDataURL(t *testing.T) string {
	t.Helper()
	source := image.NewRGBA(image.Rect(0, 0, 81, 57))
	for y := 0; y < 57; y++ {
		for x := 0; x < 81; x++ {
			source.SetRGBA(x, y, color.RGBA{R: uint8(19*x + y), G: uint8(11*y + x), B: uint8(x * y), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, source, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestSanitizerReplayReproducesRecordedJPEGLikeRust(t *testing.T) {
	sourceURL := replayTestJPEGDataURL(t)
	_, sourcePayload, _ := bytes.Cut([]byte(sourceURL), []byte(","))
	message := map[string]any{
		"type": "message",
		"role": "user",
		"content": []any{
			map[string]any{"type": "input_image", "image_url": sourceURL, "detail": "high"},
		},
	}
	first := prepareResponseInputImagesWithStoreModeDecoder([]any{message}, nil, eventmap.ImagePrepOrigin{}, eventmap.ImagePrepModeDetailBased, utils.DecoderSanitizer)
	firstBlock := first[0].(map[string]any)["content"].([]any)[0].(map[string]any)
	recordedURL, _ := firstBlock["image_url"].(string)
	if recordedURL == "" || recordedURL == sourceURL {
		t.Fatalf("first prepared URL = %q", recordedURL)
	}
	digest := sha1.Sum([]byte(recordedURL))
	numbers := make([]any, len(digest))
	for i, value := range digest {
		numbers[i] = int(value)
	}
	message["internal_chat_message_metadata_passthrough"] = map[string]any{
		"image_replay_sources": []any{
			map[string]any{
				"prepared_url_digest": numbers,
				"detail":              "high",
				"original_data_url":   "data:application/octet-stream;base64," + string(sourcePayload),
			},
		},
	}
	second := prepareResponseInputImagesWithStoreModeDecoder([]any{message}, nil, eventmap.ImagePrepOrigin{}, eventmap.ImagePrepModeDetailBased, utils.DecoderSanitizer)
	secondBlock := second[0].(map[string]any)["content"].([]any)[0].(map[string]any)
	if got, _ := secondBlock["image_url"].(string); got != recordedURL {
		t.Fatalf("replayed URL = %q, want %q", got, recordedURL)
	}
	stripped := stripImageReplaySources(second)
	strippedMessage := stripped[0].(map[string]any)
	metadata, _ := strippedMessage["internal_chat_message_metadata_passthrough"].(map[string]any)
	if _, present := metadata["image_replay_sources"]; present {
		t.Fatalf("replay sources leaked into the request: %#v", metadata)
	}
}
