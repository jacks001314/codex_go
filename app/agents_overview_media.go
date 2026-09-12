package app

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	bottompane "codex_go/tui/bottom_pane"
	codextea "codex_go/tui/tea"
	"codex_go/turn"

	"codex_go/appserver"
	modelpkg "codex_go/model"
)

// maxPromptImageInputBytes bounds the local image snapshot the dashboard sends
// to a remote workspace (Rust #44027 read_bounded_local_media). Rust resizes
// before encoding; Go sends the source bytes under this sanity guard.
const maxPromptImageInputBytes = 32 * 1024 * 1024

// agentsOverviewTaskInputs builds the first-turn inputs for a dashboard
// background task: image attachments first, then the prompt text, matching
// Rust's submit_agents_overview_prompt (Rust #44027). Local images are
// snapshotted into data URLs when the workspace is remote.
func agentsOverviewTaskInputs(request codextea.SubmitRequest, snapshotLocalImages bool) ([]turn.TurnUserInput, error) {
	inputs := make([]turn.TurnUserInput, 0, len(request.Attachments)+1)
	for _, attachment := range request.Attachments {
		switch attachment.Kind {
		case bottompane.AttachmentImage:
			path := strings.TrimSpace(attachment.Path)
			if path == "" {
				continue
			}
			if snapshotLocalImages {
				url, err := localImageDataURL(path)
				if err != nil {
					return nil, err
				}
				inputs = append(inputs, turn.TurnUserInput{Type: "image", URL: url})
				continue
			}
			inputs = append(inputs, turn.TurnUserInput{Type: "localImage", Path: path})
		case bottompane.AttachmentRemoteImage:
			if url := strings.TrimSpace(attachment.URL); url != "" {
				inputs = append(inputs, turn.TurnUserInput{Type: "image", URL: url})
			}
		default:
			if path := strings.TrimSpace(attachment.Path); path != "" {
				inputs = append(inputs, turn.TurnUserInput{Type: "text", Text: "Attached file: " + path})
			}
		}
	}
	if prompt := strings.TrimSpace(request.Prompt); prompt != "" {
		inputs = append(inputs, turn.TurnUserInput{Type: "text", Text: prompt})
	}
	return inputs, nil
}

// localImageDataURL snapshots a local image into a portable data URL so a
// remote workspace can read it (Rust #44027 snapshot_local_user_input).
func localImageDataURL(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.Size() > maxPromptImageInputBytes {
		return "", fmt.Errorf("image input exceeds %d bytes", maxPromptImageInputBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(data) > maxPromptImageInputBytes {
		return "", fmt.Errorf("image input exceeds %d bytes", maxPromptImageInputBytes)
	}
	return "data:" + imageMIMEForData(path, data) + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

// imageMIMEForData resolves the image MIME type from the extension, falling
// back to content sniffing for unknown extensions.
func imageMIMEForData(path string, data []byte) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	}
	if detected := strings.TrimSpace(strings.Split(http.DetectContentType(data), ";")[0]); strings.HasPrefix(detected, "image/") {
		return detected
	}
	return "application/octet-stream"
}

// rejectTextOnlyModelForImages fails the dashboard task before a thread starts
// when the resolved model is known to be text-only (Rust #44027). Servers that
// cannot list models skip the check.
func (s *remoteAgentsDashboardSource) rejectTextOnlyModelForImages(ctx context.Context, modelID string) error {
	if s == nil || s.client == nil {
		return nil
	}
	var listed modelpkg.ModelListResponse
	if err := remoteSessionRequest(ctx, s.client, appserver.MethodModelList, modelpkg.ModelListParams{}, &listed); err != nil {
		return nil
	}
	presets := listed.Data
	if len(presets) == 0 {
		presets = listed.Models
	}
	target := strings.TrimSpace(modelID)
	var preset *modelpkg.ModelSummary
	if target == "" {
		for index := range presets {
			if presets[index].IsDefault {
				preset = &presets[index]
				break
			}
		}
		if preset == nil && len(presets) > 0 {
			preset = &presets[0]
		}
	} else {
		for index := range presets {
			if presets[index].Model == target || presets[index].ID == target {
				preset = &presets[index]
				break
			}
		}
	}
	if preset == nil {
		return nil
	}
	for _, modality := range preset.InputModalities {
		if strings.EqualFold(strings.TrimSpace(modality), "image") {
			return nil
		}
	}
	return fmt.Errorf("Model %s does not support image inputs. Remove images or switch models.", preset.Model)
}
