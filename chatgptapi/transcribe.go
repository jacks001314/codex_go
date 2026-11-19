package chatgptapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/google/uuid"
)

// maxTranscriptionResponseBytes bounds the dictation endpoint's reply so a
// broken server cannot stream an unbounded body (Rust #62142).
const maxTranscriptionResponseBytes = 64 * 1024

// transcribeAudioExtensions maps an accepted clip MIME type to the filename
// extension the dictation endpoint expects (Rust #62142).
var transcribeAudioExtensions = map[string]string{
	"audio/wav":  "wav",
	"audio/mpeg": "mp3",
	"audio/mp4":  "m4a",
	"audio/webm": "webm",
	"audio/ogg":  "ogg",
}

// TranscribeAudio submits one audio clip as the dictation endpoint's multipart
// file field and returns the transcript text (Rust backend-client
// Client::transcribe_audio, #62142). The request carries the client's shared
// credentials, including runtime credentials resolved for this destination.
func (c *CloudClient) TranscribeAudio(ctx context.Context, mime string, audio []byte) (string, error) {
	if c == nil {
		return "", errors.New("transcribe audio: client is nil")
	}
	extension, ok := transcribeAudioExtensions[mime]
	if !ok {
		return "", errors.New("unsupported audio format")
	}
	target := c.baseURL + "/transcribe"
	boundary := uuid.NewString()
	var body bytes.Buffer
	fmt.Fprintf(&body, "--%s\r\n", boundary)
	fmt.Fprintf(&body, "Content-Disposition: form-data; name=\"file\"; filename=\"audio.%s\"\r\n", extension)
	fmt.Fprintf(&body, "Content-Type: %s\r\n\r\n", mime)
	body.Write(audio)
	fmt.Fprintf(&body, "\r\n--%s--\r\n", boundary)

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body.Bytes()))
	if err != nil {
		return "", err
	}
	for key, values := range c.headers {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	if c.runtimeAuth != nil {
		runtimeHeaders, err := c.runtimeAuth.HeadersForDestination(ctx, target)
		if err != nil {
			return "", err
		}
		for key, values := range runtimeHeaders {
			request.Header.Del(key)
			for _, value := range values {
				request.Header.Add(key, value)
			}
		}
	}
	request.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)

	response, err := c.httpClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", errors.New("transcription failed")
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxTranscriptionResponseBytes+1))
	if err != nil {
		return "", err
	}
	if len(payload) > maxTranscriptionResponseBytes {
		return "", errors.New("transcription response too large")
	}
	var transcript struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(payload, &transcript); err != nil {
		return "", err
	}
	return transcript.Text, nil
}
