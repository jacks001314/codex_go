package codexapi

import (
	"bytes"
	"encoding/json"
)

// GifOptions mirrors Rust codex_api::GifOptions (#59073): optional controls for
// animated GIF output. Nil fields use the API defaults (9 frames, 200 ms).
type GifOptions struct {
	FrameCount      *uint32 `json:"frame_count,omitempty"`
	FrameDurationMS *uint32 `json:"frame_duration_ms,omitempty"`
}

// ImageGenerationRequest mirrors Rust codex_api::ImageGenerationRequest. Rust
// flattens ImageOutput into `output_format` and the GIF controls; Go keeps the
// same sibling wire fields on the request.
type ImageGenerationRequest struct {
	Prompt     string           `json:"prompt"`
	Background *ImageBackground `json:"background,omitempty"`
	Model      string           `json:"model"`
	N          *uint64          `json:"n,omitempty"`
	Quality    *ImageQuality    `json:"quality,omitempty"`
	Size       *string          `json:"size,omitempty"`
	// OutputFormat is `gif` for animated output; nil keeps the PNG default.
	OutputFormat    *string `json:"output_format,omitempty"`
	FrameCount      *uint32 `json:"frame_count,omitempty"`
	FrameDurationMS *uint32 `json:"frame_duration_ms,omitempty"`
}

type ImageEditRequest struct {
	Images     []ImageReference `json:"images"`
	Prompt     string           `json:"prompt"`
	Background *ImageBackground `json:"background,omitempty"`
	Model      string           `json:"model"`
	N          *uint64          `json:"n,omitempty"`
	Quality    *ImageQuality    `json:"quality,omitempty"`
	Size       *string          `json:"size,omitempty"`
	// OutputFormat is `gif` for animated output; nil keeps the PNG default.
	OutputFormat    *string `json:"output_format,omitempty"`
	FrameCount      *uint32 `json:"frame_count,omitempty"`
	FrameDurationMS *uint32 `json:"frame_duration_ms,omitempty"`
}

// ImageReference mirrors Rust codex_protocol::models::ImageReference: an image
// is either an inline URL/data URL or a server-side file id. Callers set
// exactly one field, and the zero value is skipped before serialization.
type ImageReference struct {
	ImageURL string `json:"image_url,omitempty"`
	FileID   string `json:"file_id,omitempty"`
}

type ImageBackground string

const (
	ImageBackgroundTransparent ImageBackground = "transparent"
	ImageBackgroundOpaque      ImageBackground = "opaque"
	ImageBackgroundAuto        ImageBackground = "auto"
)

type ImageQuality string

const (
	ImageQualityLow    ImageQuality = "low"
	ImageQualityMedium ImageQuality = "medium"
	ImageQualityHigh   ImageQuality = "high"
	ImageQualityAuto   ImageQuality = "auto"
)

type ImageResponse struct {
	Created    uint64           `json:"created"`
	Data       []ImageData      `json:"data"`
	Background *ImageBackground `json:"background,omitempty"`
	Quality    *ImageQuality    `json:"quality,omitempty"`
	Size       *string          `json:"size,omitempty"`
}

type ImageData struct {
	B64JSON string `json:"b64_json"`
	// GenerationID is the backend-provided image generation identifier
	// (Rust #57552: `ImageData::generation_id` parsed from `generation_id`).
	GenerationID *string `json:"generation_id,omitempty"`
	// ImageMetadata is the per-image metadata the backend selected for the
	// receiving surface (Rust #62871). Empty, non-object, oversized, or too
	// deeply nested metadata is dropped without discarding the image.
	ImageMetadata map[string]any `json:"image_metadata,omitempty"`
}

const (
	maxImageMetadataBytes = 1024 * 1024
	// Rust limits the API response field to 24 levels: protobuf Struct objects
	// consume three decode levels per nesting level and the enclosing CCA and
	// app-server event/history messages need room above the metadata.
	maxImageMetadataDepth = 24
	// Persisted rollout history is decoded by serde_json, whose default
	// recursion limit is 128; the tighter API limit does not apply there.
	maxStoredImageMetadataDepth = 128
)

func (d *ImageData) UnmarshalJSON(data []byte) error {
	var raw struct {
		B64JSON       string          `json:"b64_json"`
		GenerationID  *string         `json:"generation_id"`
		ImageMetadata json.RawMessage `json:"image_metadata"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	d.B64JSON = raw.B64JSON
	d.GenerationID = raw.GenerationID
	d.ImageMetadata = ParseImageMetadataJSON(raw.ImageMetadata)
	return nil
}

// ParseImageMetadataJSON decodes the image backend's `image_metadata` field.
// Rust reads the field through RawValue with `DefaultOnError`, so a bad value
// can never invalidate the image it belongs to; Go decodes the raw field
// independently and returns nil on any problem.
func ParseImageMetadataJSON(raw []byte) map[string]any {
	if len(bytes.TrimSpace(raw)) == 0 || len(raw) > maxImageMetadataBytes {
		return nil
	}
	metadata := decodeImageMetadataObject(raw)
	if len(metadata) == 0 {
		return nil
	}
	return sanitizeImageMetadataMap(metadata, maxImageMetadataDepth)
}

// ParseStoredImageMetadataJSON decodes metadata from persisted history, which
// Rust reads with serde_json's default recursion limit and no size cap.
func ParseStoredImageMetadataJSON(raw []byte) map[string]any {
	metadata := decodeImageMetadataObject(raw)
	if len(metadata) == 0 {
		return nil
	}
	return sanitizeImageMetadataMap(metadata, maxStoredImageMetadataDepth)
}

// StoredImageMetadata normalizes metadata that history replay already decoded.
func StoredImageMetadata(value any) map[string]any {
	metadata, ok := value.(map[string]any)
	if !ok || len(metadata) == 0 {
		return nil
	}
	return sanitizeImageMetadataMap(metadata, maxStoredImageMetadataDepth)
}

func decodeImageMetadataObject(raw []byte) map[string]any {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var metadata map[string]any
	if err := decoder.Decode(&metadata); err != nil {
		return nil
	}
	return metadata
}

func sanitizeImageMetadataMap(metadata map[string]any, maxDepth int) map[string]any {
	if len(metadata) == 0 {
		return nil
	}
	for _, value := range metadata {
		if !imageMetadataFitsDepth(value, 1, maxDepth) {
			return nil
		}
	}
	return metadata
}

func imageMetadataFitsDepth(value any, depth int, maxDepth int) bool {
	if depth > maxDepth {
		return false
	}
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			if !imageMetadataFitsDepth(item, depth+1, maxDepth) {
				return false
			}
		}
	case map[string]any:
		for _, item := range typed {
			if !imageMetadataFitsDepth(item, depth+1, maxDepth) {
				return false
			}
		}
	}
	return true
}
