package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	bottompane "codex_go/tui/bottom_pane"
	codextea "codex_go/tui/tea"
	"codex_go/turn"
	"codex_go/utils"

	"codex_go/appserver"
	modelpkg "codex_go/model"
)

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
		inputs = append(inputs, turn.TurnUserInput{
			Type:         "text",
			Text:         prompt,
			TextElements: turnTextElementsFromComposer(request.TextElements),
		})
	}
	return inputs, nil
}

// turnTextElementsFromComposer maps the composer's byte-range elements onto the
// turn input's text_elements (Rust UserTextElement: range plus placeholder).
func turnTextElementsFromComposer(elements []codextea.ComposerTextElement) []turn.TextElement {
	if len(elements) == 0 {
		return nil
	}
	out := make([]turn.TextElement, 0, len(elements))
	for _, element := range elements {
		if element.Start < 0 || element.End <= element.Start {
			continue
		}
		placeholder := element.Placeholder
		out = append(out, turn.TextElement{
			ByteRange:   turn.ByteRange{Start: uint(element.Start), End: uint(element.End)},
			Placeholder: &placeholder,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// localImageDataURL snapshots a local image into a portable data URL so a
// remote workspace can read it (Rust #44027 snapshot_local_user_input): the
// bytes are read under Rust's prompt-image bound, then decoded and either
// preserved byte-for-byte (PNG/JPEG/WebP within 2048px) or resized/encoded.
func localImageDataURL(path string) (string, error) {
	data, err := readBoundedPromptImage(path)
	if err != nil {
		return "", err
	}
	encoded, err := utils.LoadForPromptBytes(path, data, utils.ModeResizeToFit)
	if err != nil {
		return "", err
	}
	return utils.DataURLFromBytes(encoded.Mime, encoded.Bytes), nil
}

// readBoundedPromptImage mirrors Rust read_bounded_local_media for image input:
// a file larger than the prompt-image bound fails without being loaded whole.
func readBoundedPromptImage(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	maxBytes := int64(utils.MaxPromptImageInputBytes)
	if info.Size() > maxBytes {
		return nil, fmt.Errorf("image input exceeds %d bytes", maxBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("image input exceeds %d bytes", maxBytes)
	}
	return data, nil
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
