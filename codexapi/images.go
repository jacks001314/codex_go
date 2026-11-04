package codexapi

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
}
