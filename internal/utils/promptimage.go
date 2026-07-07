package utils

import (
	"encoding/base64"
	"fmt"
	"math"
	"strings"
)

const (
	DataURLPrefix                   = "data:"
	PromptImagePatchSize     uint32 = 32
	MaxDimension             uint32 = 2048
	MaxPromptImageInputBytes        = 1024 * 1024 * 1024
)

type Mode string

const (
	ModeResizeToFit  Mode = "resize_to_fit"
	ModeOriginal     Mode = "original"
	ModeResizeLimits Mode = "resize_with_limits"
)

type ResizeLimits struct {
	MaxDimension uint32
	MaxPatches   int
}

type EncodedImage struct {
	Bytes  []byte
	Mime   string
	Width  uint32
	Height uint32
}

type ProcessingError struct {
	Kind   string
	Reason string
}

func (e *ProcessingError) Error() string {
	if e == nil {
		return ""
	}
	if e.Reason == "" {
		return e.Kind
	}
	return e.Kind + ": " + e.Reason
}

func DataURLFromBytes(mime string, bytes []byte) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(bytes)
}

func ParseDataURL(imageURL string) (string, []byte, error) {
	if len(imageURL) < len(DataURLPrefix) || !strings.EqualFold(imageURL[:len(DataURLPrefix)], DataURLPrefix) {
		return "", nil, &ProcessingError{Kind: "invalid_data_url", Reason: "missing data: prefix"}
	}
	rest := imageURL[len(DataURLPrefix):]
	metadata, encoded, ok := strings.Cut(rest, ",")
	if !ok {
		return "", nil, &ProcessingError{Kind: "invalid_data_url", Reason: "missing comma separator"}
	}
	hasBase64 := false
	parts := strings.Split(metadata, ";")
	for _, part := range parts {
		if strings.EqualFold(part, "base64") {
			hasBase64 = true
			break
		}
	}
	if !hasBase64 {
		return "", nil, &ProcessingError{Kind: "invalid_data_url", Reason: "only base64 data URLs are supported"}
	}
	if len(encoded) > MaxPromptImageInputBytes {
		return "", nil, &ProcessingError{Kind: "image_too_large", Reason: "base64 payload"}
	}
	bytes, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", nil, &ProcessingError{Kind: "invalid_data_url", Reason: fmt.Sprintf("invalid base64 payload: %v", err)}
	}
	if len(bytes) > MaxPromptImageInputBytes {
		return "", nil, &ProcessingError{Kind: "image_too_large", Reason: "decoded input"}
	}
	mime := parts[0]
	if mime == "" {
		mime = "application/octet-stream"
	}
	return mime, bytes, nil
}

func LoadDataURLForPrompt(imageURL string, mode Mode, width uint32, height uint32, limits *ResizeLimits) (EncodedImage, error) {
	mime, bytes, err := ParseDataURL(imageURL)
	if err != nil {
		return EncodedImage{}, err
	}
	outWidth, outHeight := OutputDimensions(width, height, mode, limits)
	return EncodedImage{Bytes: bytes, Mime: mime, Width: outWidth, Height: outHeight}, nil
}

func OutputDimensions(width uint32, height uint32, mode Mode, limits *ResizeLimits) (uint32, uint32) {
	width = max(width, 1)
	height = max(height, 1)
	switch mode {
	case ModeOriginal:
		return width, height
	case ModeResizeLimits:
		if limits == nil {
			return width, height
		}
		return OutputDimensionsForLimits(width, height, *limits)
	default:
		if width <= MaxDimension && height <= MaxDimension {
			return width, height
		}
		scale := math.Min(float64(MaxDimension)/float64(width), float64(MaxDimension)/float64(height))
		return max(uint32(math.Round(float64(width)*scale)), 1), max(uint32(math.Round(float64(height)*scale)), 1)
	}
}

func OutputDimensionsForLimits(width uint32, height uint32, limits ResizeLimits) (uint32, uint32) {
	width = max(width, 1)
	height = max(height, 1)
	if DimensionsFit(width, height, limits) {
		return width, height
	}
	maxDimension := limits.MaxDimension
	if maxDimension == 0 {
		maxDimension = MaxDimension
	}
	maxPatches := limits.MaxPatches
	if maxPatches <= 0 {
		maxPatches = 1
	}
	scale := math.Min(float64(maxDimension)/float64(max(width, height)), 1.0)
	width = max(uint32(math.Round(float64(width)*scale)), 1)
	height = max(uint32(math.Round(float64(height)*scale)), 1)
	if DimensionsFit(width, height, ResizeLimits{MaxDimension: maxDimension, MaxPatches: maxPatches}) {
		return width, height
	}
	widthF := float64(width)
	heightF := float64(height)
	patchSize := float64(PromptImagePatchSize)
	areaScale := math.Sqrt(patchSize * patchSize * float64(maxPatches) / widthF / heightF)
	patchesWide := widthF * areaScale / patchSize
	patchesHigh := heightF * areaScale / patchSize
	areaScale *= math.Min(math.Floor(patchesWide)/patchesWide, math.Floor(patchesHigh)/patchesHigh)
	return max(uint32(math.Floor(widthF*areaScale)), 1), max(uint32(math.Floor(heightF*areaScale)), 1)
}

func DimensionsFit(width uint32, height uint32, limits ResizeLimits) bool {
	maxDimension := limits.MaxDimension
	if maxDimension == 0 {
		maxDimension = MaxDimension
	}
	maxPatches := limits.MaxPatches
	if maxPatches <= 0 {
		maxPatches = 1
	}
	patchesWide := ceilDiv(width, PromptImagePatchSize)
	patchesHigh := ceilDiv(height, PromptImagePatchSize)
	return width <= maxDimension && height <= maxDimension && uint64(patchesWide)*uint64(patchesHigh) <= uint64(maxPatches)
}

func ceilDiv(left uint32, right uint32) uint32 {
	if right == 0 {
		return 0
	}
	return (left + right - 1) / right
}

func max(left uint32, right uint32) uint32 {
	if left > right {
		return left
	}
	return right
}
