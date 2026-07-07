package codexapi

type ImageGenerationRequest struct {
	Prompt     string           `json:"prompt"`
	Background *ImageBackground `json:"background,omitempty"`
	Model      string           `json:"model"`
	N          *uint64          `json:"n,omitempty"`
	Quality    *ImageQuality    `json:"quality,omitempty"`
	Size       *string          `json:"size,omitempty"`
}

type ImageEditRequest struct {
	Images     []ImageURL       `json:"images"`
	Prompt     string           `json:"prompt"`
	Background *ImageBackground `json:"background,omitempty"`
	Model      string           `json:"model"`
	N          *uint64          `json:"n,omitempty"`
	Quality    *ImageQuality    `json:"quality,omitempty"`
	Size       *string          `json:"size,omitempty"`
}

type ImageURL struct {
	ImageURL string `json:"image_url"`
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
}
