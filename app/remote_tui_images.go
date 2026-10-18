package app

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"strings"

	codextea "codex_go/tui/tea"
	"codex_go/turn"
)

// remoteSubmitInputs snapshots local image attachments into self-contained data
// URLs so a remote app-server can consume images from the client filesystem.
func remoteSubmitInputs(request codextea.SubmitRequest) ([]turn.TurnUserInput, error) {
	inputs := interactiveSubmitInputs(request)
	for index := range inputs {
		input := &inputs[index]
		if input.Type != "localImage" {
			continue
		}
		url, err := portableLocalImageURL(input.Path)
		if err != nil {
			return nil, err
		}
		input.Type = "image"
		input.URL = url
		input.Path = ""
	}
	return inputs, nil
}

func portableLocalImageURL(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("local image path is empty")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read local image %s: %w", path, err)
	}
	mediaType := http.DetectContentType(data)
	if !strings.HasPrefix(mediaType, "image/") {
		return "", fmt.Errorf("local image %s has unsupported content type %s", path, mediaType)
	}
	return "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}
