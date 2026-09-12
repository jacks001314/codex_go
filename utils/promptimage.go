package utils

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"strings"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/webp"
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
	Bytes []byte
	Mime  string
	// Width/Height are the prepared (encoded) dimensions; SourceWidth and
	// SourceHeight are the decoded source dimensions (Rust EncodedImage).
	Width        uint32
	Height       uint32
	SourceWidth  uint32
	SourceHeight uint32
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

// LoadDataURLForPrompt mirrors Rust load_data_url_for_prompt: the data URL
// payload is decoded (which also yields its dimensions) and then run through the
// same load path; the declared media type is not used for format detection.
func LoadDataURLForPrompt(imageURL string, mode Mode) (EncodedImage, error) {
	_, bytes, err := ParseDataURL(imageURL)
	if err != nil {
		return EncodedImage{}, err
	}
	return loadForPromptBytes("data URL", bytes, mode, nil)
}

// LoadForPromptBytes mirrors Rust utils/image load_for_prompt_bytes for the
// ResizeToFit and Original modes: the payload is decoded, resized when it
// exceeds MaxDimension (ResizeToFit), and returned as the source bytes when the
// detected format is byte-preservable (PNG/JPEG/WebP) or re-encoded otherwise.
func LoadForPromptBytes(path string, fileBytes []byte, mode Mode) (EncodedImage, error) {
	return loadForPromptBytes(path, fileBytes, mode, nil)
}

// LoadForPromptBytesWithLimits is LoadForPromptBytes for ResizeWithLimits.
func LoadForPromptBytesWithLimits(path string, fileBytes []byte, limits ResizeLimits) (EncodedImage, error) {
	return loadForPromptBytes(path, fileBytes, ModeResizeLimits, &limits)
}

func loadForPromptBytes(path string, fileBytes []byte, mode Mode, limits *ResizeLimits) (EncodedImage, error) {
	format, decoded, sourceWidth, sourceHeight, err := decodePromptImage(path, fileBytes)
	if err != nil {
		return EncodedImage{}, err
	}
	preparedWidth, preparedHeight := OutputDimensions(sourceWidth, sourceHeight, mode, limits)
	resized := preparedWidth != sourceWidth || preparedHeight != sourceHeight
	if !resized && canPreservePromptImageSourceBytes(format) {
		return EncodedImage{
			Bytes:        append([]byte(nil), fileBytes...),
			Mime:         promptImageMIME(format),
			Width:        sourceWidth,
			Height:       sourceHeight,
			SourceWidth:  sourceWidth,
			SourceHeight: sourceHeight,
		}, nil
	}
	target := decoded
	if resized {
		target = resizePromptImage(decoded, preparedWidth, preparedHeight)
	}
	targetFormat := promptImageFormatPNG
	if !resized {
		// A non-preservable (GIF) source is converted to PNG, matching Rust.
		targetFormat = promptImageFormatPNG
	} else if format == promptImageFormatJPEG {
		targetFormat = promptImageFormatJPEG
	} else if format == promptImageFormatWebP {
		// Go has no WebP encoder; Rust re-encodes WebP losslessly. PNG keeps the
		// observable contract with a documented container difference.
		targetFormat = promptImageFormatPNG
	}
	encoded, mime, err := encodePromptImage(target, targetFormat)
	if err != nil {
		return EncodedImage{}, &ProcessingError{Kind: "encode_error", Reason: path + ": " + err.Error()}
	}
	return EncodedImage{
		Bytes:        encoded,
		Mime:         mime,
		Width:        preparedWidth,
		Height:       preparedHeight,
		SourceWidth:  sourceWidth,
		SourceHeight: sourceHeight,
	}, nil
}

type promptImageFormat int

const (
	promptImageFormatPNG promptImageFormat = iota
	promptImageFormatJPEG
	promptImageFormatGIF
	promptImageFormatWebP
)

// canPreservePromptImageSourceBytes mirrors Rust can_preserve_source_bytes.
func canPreservePromptImageSourceBytes(format promptImageFormat) bool {
	switch format {
	case promptImageFormatPNG, promptImageFormatJPEG, promptImageFormatWebP:
		return true
	default:
		return false
	}
}

// promptImageMIME mirrors Rust format_to_mime.
func promptImageMIME(format promptImageFormat) string {
	switch format {
	case promptImageFormatJPEG:
		return "image/jpeg"
	case promptImageFormatGIF:
		return "image/gif"
	case promptImageFormatWebP:
		return "image/webp"
	default:
		return "image/png"
	}
}

func decodePromptImage(path string, fileBytes []byte) (promptImageFormat, image.Image, uint32, uint32, error) {
	format, err := promptImageGuessFormat(fileBytes)
	if err != nil {
		return 0, nil, 0, 0, &ProcessingError{Kind: "decode_error", Reason: path + ": " + err.Error()}
	}
	var decoded image.Image
	switch format {
	case promptImageFormatPNG:
		decoded, err = png.Decode(bytes.NewReader(fileBytes))
	case promptImageFormatJPEG:
		decoded, err = jpeg.Decode(bytes.NewReader(fileBytes))
	case promptImageFormatGIF:
		decoded, err = gif.Decode(bytes.NewReader(fileBytes))
	case promptImageFormatWebP:
		decoded, err = webp.Decode(bytes.NewReader(fileBytes))
	}
	if err != nil {
		return 0, nil, 0, 0, &ProcessingError{Kind: "decode_error", Reason: path + ": " + err.Error()}
	}
	bounds := decoded.Bounds()
	return format, decoded, uint32(bounds.Dx()), uint32(bounds.Dy()), nil
}

func promptImageGuessFormat(payload []byte) (promptImageFormat, error) {
	switch {
	case len(payload) >= 8 && bytes.Equal(payload[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}):
		return promptImageFormatPNG, nil
	case len(payload) >= 3 && payload[0] == 0xff && payload[1] == 0xd8 && payload[2] == 0xff:
		return promptImageFormatJPEG, nil
	case len(payload) >= 6 && (string(payload[:6]) == "GIF87a" || string(payload[:6]) == "GIF89a"):
		return promptImageFormatGIF, nil
	case len(payload) >= 12 && string(payload[0:4]) == "RIFF" && string(payload[8:12]) == "WEBP":
		return promptImageFormatWebP, nil
	default:
		return 0, fmt.Errorf("unsupported image format")
	}
}

// resizePromptImage scales with bilinear interpolation (Rust FilterType::Triangle).
func resizePromptImage(src image.Image, targetWidth uint32, targetHeight uint32) image.Image {
	dst := image.NewRGBA(image.Rect(0, 0, int(targetWidth), int(targetHeight)))
	xdraw.BiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Over, nil)
	return dst
}

// encodePromptImage mirrors Rust encode_image: JPEG at quality 85, otherwise PNG.
func encodePromptImage(img image.Image, format promptImageFormat) ([]byte, string, error) {
	var buf bytes.Buffer
	if format == promptImageFormatJPEG {
		// JPEG cannot represent alpha; flatten onto white as Rust's encoder does.
		flattened := image.NewRGBA(img.Bounds())
		xdraw.Draw(flattened, flattened.Bounds(), image.NewUniform(color.White), image.Point{}, xdraw.Src)
		xdraw.Draw(flattened, flattened.Bounds(), img, img.Bounds().Min, xdraw.Over)
		if err := jpeg.Encode(&buf, flattened, &jpeg.Options{Quality: 85}); err != nil {
			return nil, "", err
		}
		return buf.Bytes(), "image/jpeg", nil
	}
	if err := png.Encode(&buf, img); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), "image/png", nil
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
