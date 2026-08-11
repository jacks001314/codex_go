package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"

	"codex_go/utils"
)

const ViewImageToolName = "view_image"

const viewImageInvalidMessage = "unable to process image: invalid or unsupported image data"

type ViewImageOptions struct {
	CWD                      string
	CanRequestOriginalDetail bool
	IncludeEnvironmentID     bool
}

type ViewImageHandler struct {
	options ViewImageOptions
}

type ViewImageArgs struct {
	Path          string `json:"path"`
	Detail        string `json:"detail,omitempty"`
	EnvironmentID string `json:"environment_id,omitempty"`
}

type ViewImageResult struct {
	ImageURL string `json:"image_url"`
	Detail   string `json:"detail"`
}

func NewViewImageHandler(options ViewImageOptions) *ViewImageHandler {
	return &ViewImageHandler{options: options}
}

func (h *ViewImageHandler) Spec() Spec {
	properties := map[string]any{
		"path": map[string]any{"type": "string", "description": "Local filesystem path to an image file."},
	}
	if h != nil && h.options.CanRequestOriginalDetail {
		properties["detail"] = map[string]any{
			"type": "string", "enum": []string{"high", "original"},
			"description": "Image detail level. Defaults to `high`; use `original` to preserve exact resolution.",
		}
	}
	if h != nil && h.options.IncludeEnvironmentID {
		properties["environment_id"] = map[string]any{
			"type": "string", "description": "Environment id from <environment_context>. Omit to use the primary environment.",
		}
	}
	return Spec{
		Name:        PlainName(ViewImageToolName),
		Description: "View a local image file from the filesystem when visual inspection is needed. Use this for images already available on disk.",
		InputSchema: map[string]any{"type": "object", "properties": properties, "required": []string{"path"}, "additionalProperties": false},
		OutputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"image_url": map[string]any{"type": "string", "description": "Data URL for the loaded image."},
				"detail":    map[string]any{"type": "string", "enum": []string{"high", "original"}, "description": "Image detail hint returned by view_image. Returns `high` for default resized behavior or `original` when original resolution is preserved."},
			},
			"required": []string{"image_url", "detail"}, "additionalProperties": false,
		},
	}
}

func (h *ViewImageHandler) Execute(ctx context.Context, invocation *Invocation) (*Output, error) {
	_ = ctx
	if invocation == nil {
		return nil, fmt.Errorf("%w: invocation is nil", ErrToolInvalidCall)
	}
	if invocation.Payload.Kind != PayloadFunction {
		return nil, RespondToModel("view_image handler received unsupported payload")
	}
	var args ViewImageArgs
	if err := invocation.DecodeArguments(&args); err != nil {
		return nil, err
	}
	detail := strings.TrimSpace(args.Detail)
	if detail == "" {
		detail = "high"
	}
	if detail != "high" && detail != "original" {
		return nil, RespondToModel(fmt.Sprintf("view_image.detail only supports `high` or `original`; omit `detail` for default high resized behavior, got `%s`", detail))
	}
	if detail == "original" && (h == nil || !h.options.CanRequestOriginalDetail) {
		detail = "high"
	}
	path := strings.TrimSpace(args.Path)
	if path == "" {
		return nil, RespondToModel("path must not be empty")
	}
	if !filepath.IsAbs(path) {
		cwd := ""
		if h != nil {
			cwd = strings.TrimSpace(h.options.CWD)
		}
		if cwd == "" {
			cwd, _ = os.Getwd()
		}
		path = filepath.Join(cwd, path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, RespondToModel(fmt.Sprintf("unable to locate image at `%s`: %v", path, err))
	}
	if !info.Mode().IsRegular() {
		return nil, RespondToModel(fmt.Sprintf("image path `%s` is not a file", path))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, RespondToModel(fmt.Sprintf("unable to read image at `%s`: %v", path, err))
	}
	// Reject non-images before their bytes can reach code mode without changing
	// valid image bytes, metadata, or centralized image preparation.
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return nil, RespondToModel(viewImageInvalidMessage)
	}
	// The history insertion path owns image preparation and resizing.
	result := ViewImageResult{ImageURL: utils.DataURLFromBytes("application/octet-stream", data), Detail: detail}
	body, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	contentItems := []map[string]any{{
		"type":      "input_image",
		"image_url": result.ImageURL,
		"detail":    result.Detail,
	}}
	return &Output{
		Success: true,
		Body:    string(body),
		Data: map[string]any{
			"image_url":     result.ImageURL,
			"detail":        result.Detail,
			"content_items": contentItems,
		},
		LogPreview: fmt.Sprintf("<image data URL omitted: %d bytes>", len(result.ImageURL)),
	}, nil
}

var _ Executor = (*ViewImageHandler)(nil)
