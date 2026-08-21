package eventmap

import (
	"bytes"
	"encoding/base64"
	"errors"
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
	ImagePrepProcessingErrorPlaceholder      = "image content omitted because it could not be processed"
	ImagePrepTooLargePlaceholder             = "image content omitted because it exceeded the supported size limit; use a smaller image"
	ImagePrepUnsupportedLowDetailPlaceholder = "image content omitted because detail 'low' is not supported; use 'high', 'original', or 'auto'"
	ImagePrepRemoteURLPlaceholder            = "image content omitted because remote image URLs are not supported"
)

const (
	imagePrepHighDetailMaxBytes     = 20 * 1024 * 1024
	imagePrepOriginalDetailMaxBytes = 60 * 1024 * 1024
)

// PromptImagePatchSize mirrors Rust PROMPT_IMAGE_PATCH_SIZE (utils/image):
// the tile size used by the Responses image patch budget.
const PromptImagePatchSize = 32

// PromptImageResizeLimits mirrors Rust PromptImageResizeLimits: the maximum
// image dimension and the maximum number of 32x32 patches after preparation.
type PromptImageResizeLimits struct {
	MaxDimension uint32
	MaxPatches   uint64
}

// HighDetailLimits mirrors Rust HIGH_DETAIL_LIMITS (2048 max dimension,
// 2500 patches) applied to auto/high detail requests.
var HighDetailLimits = PromptImageResizeLimits{MaxDimension: 2048, MaxPatches: 2500}

// UnifiedImageLimits mirrors Rust UNIFIED_IMAGE_LIMITS (6000 max dimension,
// 10000 patches) applied to original-detail and unified-budget requests.
var UnifiedImageLimits = PromptImageResizeLimits{MaxDimension: 6000, MaxPatches: 10000}

// PromptImageDimensionsFit mirrors Rust prompt_image_dimensions_fit: an image
// fits when both dimensions are within max_dimension and the 32x32 patch count
// is within max_patches.
func PromptImageDimensionsFit(width uint32, height uint32, limits PromptImageResizeLimits) bool {
	if width == 0 {
		width = 1
	}
	if height == 0 {
		height = 1
	}
	patchesWide := ceilDiv(width, PromptImagePatchSize)
	patchesHigh := ceilDiv(height, PromptImagePatchSize)
	patchCount := uint64(patchesWide) * uint64(patchesHigh)
	return width <= limits.MaxDimension &&
		height <= limits.MaxDimension &&
		patchCount <= limits.MaxPatches
}

// PromptImageOutputDimensionsForLimits mirrors Rust
// prompt_image_output_dimensions_for_limits: if the image fits the budget it
// is returned unchanged; otherwise it is scaled by max_dimension first (Rust
// .round()), and if the patch budget is still exceeded, scaled by area with
// the scaled patch grid floored to whole patches so integer output dimensions
// stay within the budget.
func PromptImageOutputDimensionsForLimits(width uint32, height uint32, limits PromptImageResizeLimits) (uint32, uint32) {
	if width == 0 {
		width = 1
	}
	if height == 0 {
		height = 1
	}
	if PromptImageDimensionsFit(width, height, limits) {
		return width, height
	}
	maxDimension := float64(width)
	if float64(height) > maxDimension {
		maxDimension = float64(height)
	}
	maxDimensionScale := (float64(limits.MaxDimension) / maxDimension)
	if maxDimensionScale > 1.0 {
		maxDimensionScale = 1.0
	}
	width = uint32(math.Round(float64(width) * maxDimensionScale))
	height = uint32(math.Round(float64(height) * maxDimensionScale))
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	if PromptImageDimensionsFit(width, height, limits) {
		return width, height
	}
	widthF64 := float64(width)
	heightF64 := float64(height)
	patchSize := float64(PromptImagePatchSize)
	scale := math.Sqrt(patchSize * patchSize * float64(limits.MaxPatches) / widthF64 / heightF64)
	scaledPatchesWide := widthF64 * scale / patchSize
	scaledPatchesHigh := heightF64 * scale / patchSize
	roundDownWide := math.Floor(scaledPatchesWide) / scaledPatchesWide
	roundDownHigh := math.Floor(scaledPatchesHigh) / scaledPatchesHigh
	if roundDownHigh < roundDownWide {
		scale *= roundDownHigh
	} else {
		scale *= roundDownWide
	}
	outWidth := uint32(math.Floor(widthF64 * scale))
	outHeight := uint32(math.Floor(heightF64 * scale))
	if outWidth < 1 {
		outWidth = 1
	}
	if outHeight < 1 {
		outHeight = 1
	}
	return outWidth, outHeight
}

func ceilDiv(value uint32, divisor uint32) uint32 {
	return (value + divisor - 1) / divisor
}

// ImagePrepResize reports the source and prepared dimensions of one image that
// changed during preparation, mirroring Rust PreparedImageResize plus the
// ResizedImage numbering used by ImageResizeNotice.
type ImagePrepResize struct {
	ImageNumber    int
	ImageCount     int
	SourceWidth    uint32
	SourceHeight   uint32
	PreparedWidth  uint32
	PreparedHeight uint32
}

// ImagePrepResult is the outcome of preparing one image: the replacement data
// URL plus the resize record (nil when the image was unchanged) and the
// effective detail used.
type ImagePrepResult struct {
	URL             string
	Resize          *ImagePrepResize
	EffectiveDetail ImagePrepDetail
}

var (
	ErrImagePrepRemoteURLUnsupported = errors.New("remote image URLs are not supported")
	ErrImagePrepUnsupportedLowDetail = errors.New("image detail low is not supported")
	ErrImagePrepTooLarge             = errors.New("image exceeded supported size limit")
	ErrImagePrepInvalidDataURL       = errors.New("invalid image data URL")
)

type ImagePrepDetail string

const (
	ImagePrepDetailAuto     ImagePrepDetail = "auto"
	ImagePrepDetailHigh     ImagePrepDetail = "high"
	ImagePrepDetailOriginal ImagePrepDetail = "original"
	ImagePrepDetailLow      ImagePrepDetail = "low"
)

type ImagePrepContentKind string

const (
	ImagePrepContentText  ImagePrepContentKind = "text"
	ImagePrepContentImage ImagePrepContentKind = "image"
)

type ImagePrepContentItem struct {
	Kind     ImagePrepContentKind
	Text     string
	ImageURL string
	Detail   ImagePrepDetail
}

type ImagePrepResponseItemKind string

const (
	ImagePrepResponseMessage        ImagePrepResponseItemKind = "message"
	ImagePrepResponseToolCallOutput ImagePrepResponseItemKind = "tool_call_output"
	ImagePrepResponseOther          ImagePrepResponseItemKind = "other"
)

type ImagePrepResponseItem struct {
	Kind    ImagePrepResponseItemKind
	Content []ImagePrepContentItem
}

func PrepareImagePrepResponseItems(items []ImagePrepResponseItem) []ImagePrepResponseItem {
	out := make([]ImagePrepResponseItem, len(items))
	for i := range items {
		out[i] = items[i]
		out[i].Content = prepareImagePrepContent(items[i].Content)
	}
	return out
}

func PrepareImagePrepContent(items []ImagePrepContentItem) []ImagePrepContentItem {
	return prepareImagePrepContent(items)
}

// PrepareImagePrepContentWithNotices prepares image content items and reports
// the resize records for images whose dimensions changed, mirroring Rust
// prepare_message_content with ImageResizeNoticeMode::Enabled. The returned
// resized list is indexed by the image's 1-based position within the content
// (image_number), so callers can build the ImageResizeNotice fragment with the
// same numbering Rust uses.
func PrepareImagePrepContentWithNotices(items []ImagePrepContentItem) ([]ImagePrepContentItem, []ImagePrepResize, error) {
	out := make([]ImagePrepContentItem, len(items))
	var resized []ImagePrepResize
	imageCount := 0
	for _, item := range items {
		if item.Kind == ImagePrepContentImage {
			imageCount++
		}
	}
	imageNumber := 0
	for i := range items {
		item := items[i]
		if item.Kind != ImagePrepContentImage {
			out[i] = item
			continue
		}
		imageNumber++
		prepared, err := PrepareImagePrepImageWithResult(item.ImageURL, item.Detail)
		if err != nil {
			out[i] = ImagePrepContentItem{Kind: ImagePrepContentText, Text: ImagePrepPlaceholderForError(err)}
			continue
		}
		if prepared == nil {
			out[i] = item
			continue
		}
		item.ImageURL = prepared.URL
		out[i] = item
		if prepared.Resize != nil {
			resized = append(resized, ImagePrepResize{
				ImageNumber:    imageNumber,
				ImageCount:     imageCount,
				SourceWidth:    prepared.Resize.SourceWidth,
				SourceHeight:   prepared.Resize.SourceHeight,
				PreparedWidth:  prepared.Resize.PreparedWidth,
				PreparedHeight: prepared.Resize.PreparedHeight,
			})
		}
	}
	return out, resized, nil
}

func prepareImagePrepContent(items []ImagePrepContentItem) []ImagePrepContentItem {
	out := make([]ImagePrepContentItem, len(items))
	for i := range items {
		item := items[i]
		if item.Kind != ImagePrepContentImage {
			out[i] = item
			continue
		}
		preparedURL, err := PrepareImagePrepImage(item.ImageURL, item.Detail)
		if err != nil {
			out[i] = ImagePrepContentItem{Kind: ImagePrepContentText, Text: ImagePrepPlaceholderForError(err)}
			continue
		}
		item.ImageURL = preparedURL
		out[i] = item
	}
	return out
}

func PrepareImagePrepImage(imageURL string, detail ImagePrepDetail) (string, error) {
	result, err := PrepareImagePrepImageWithResult(imageURL, detail)
	if err != nil {
		return "", err
	}
	if result == nil {
		return imageURL, nil
	}
	return result.URL, nil
}

// PrepareImagePrepImageWithResult prepares one image with the Rust
// detail-based budget: auto/high uses HighDetailLimits (2048 max dimension,
// 2500 patches), original uses UnifiedImageLimits (6000/10000). When the image
// exceeds the budget it is decoded, resized and re-encoded; otherwise the
// source bytes are preserved for PNG/JPEG/WebP and re-encoded as PNG for
// formats that cannot be passed through (GIF). The returned resize record is
// non-nil only when the dimensions actually changed (mirroring Rust
// PreparedImageResize); callers use it to emit the feature-gated
// ImageResizeNotice fragment.
func PrepareImagePrepImageWithResult(imageURL string, detail ImagePrepDetail) (*ImagePrepResult, error) {
	if IsImagePrepRemoteURL(imageURL) {
		return nil, ErrImagePrepRemoteURLUnsupported
	}
	if !IsImagePrepDataURL(imageURL) {
		return nil, nil
	}
	if detail == "" || detail == ImagePrepDetailAuto {
		detail = ImagePrepDetailHigh
	}
	if detail == ImagePrepDetailLow {
		return nil, ErrImagePrepUnsupportedLowDetail
	}
	_, payload, err := splitImagePrepDataURL(imageURL)
	if err != nil {
		return nil, err
	}
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrImagePrepInvalidDataURL, err)
	}
	limit := imagePrepHighDetailMaxBytes
	if detail == ImagePrepDetailOriginal {
		limit = imagePrepOriginalDetailMaxBytes
	}
	if len(decoded) > limit {
		return nil, fmt.Errorf("%w: %d > %d", ErrImagePrepTooLarge, len(decoded), limit)
	}
	limits := HighDetailLimits
	if detail == ImagePrepDetailOriginal {
		limits = UnifiedImageLimits
	}
	prepared, err := prepareImagePrepDecoded(decoded, limits)
	if err != nil {
		return nil, err
	}
	return prepared, nil
}

// prepareImagePrepDecoded decodes the image bytes, computes the target
// dimensions against the limits and re-encodes when the dimensions change.
// Images that already fit the budget are returned byte-for-byte for
// PNG/JPEG/WebP (Rust can_preserve_source_bytes) and re-encoded as PNG for
// other decodable formats (GIF), mirroring Rust load_for_prompt_bytes.
func prepareImagePrepDecoded(payload []byte, limits PromptImageResizeLimits) (*ImagePrepResult, error) {
	format, img, sourceWidth, sourceHeight, err := decodeImagePrepBytes(payload)
	if err != nil {
		return nil, err
	}
	targetWidth, targetHeight := PromptImageOutputDimensionsForLimits(sourceWidth, sourceHeight, limits)
	if targetWidth == sourceWidth && targetHeight == sourceHeight {
		if canImagePrepPreserveSourceBytes(format) {
			return &ImagePrepResult{
				// Use the canonical mime for the detected format rather than the
				// media type declared in the original data URL. Sources such as
				// the view_image tool label bytes application/octet-stream even
				// when the payload is a valid PNG/JPEG/WebP; preserving that
				// label would make the Responses API reject the image as an
				// unsupported format (it only accepts webp/png/jpeg/gif).
				URL: "data:" + imagePrepMIME(format) + ";base64," + base64.StdEncoding.EncodeToString(payload),
			}, nil
		}
		resized := resizeImagePrep(img, targetWidth, targetHeight)
		encoded, mime, err := encodeImagePrep(resized, imagePrepFormatPNG)
		if err != nil {
			return nil, err
		}
		return &ImagePrepResult{
			URL: "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(encoded),
		}, nil
	}
	resized := resizeImagePrep(img, targetWidth, targetHeight)
	targetFormat := imagePrepFormatPNG
	switch format {
	case imagePrepFormatJPEG:
		targetFormat = imagePrepFormatJPEG
	case imagePrepFormatWebP:
		// Go has no WebP encoder in the standard library or x/image; Rust
		// re-encodes WebP losslessly. Re-encoding as PNG keeps the observable
		// contract (resized dimensions, notice numbers) with a documented
		// container difference for this rare path.
		targetFormat = imagePrepFormatPNG
	default:
		targetFormat = imagePrepFormatPNG
	}
	encoded, mime, err := encodeImagePrep(resized, targetFormat)
	if err != nil {
		return nil, err
	}
	return &ImagePrepResult{
		URL: "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(encoded),
		Resize: &ImagePrepResize{
			SourceWidth:    sourceWidth,
			SourceHeight:   sourceHeight,
			PreparedWidth:  targetWidth,
			PreparedHeight: targetHeight,
		},
	}, nil
}

// imagePrepMIME maps a detected image format to the mime type used in the data
// URL sent to the Responses API, mirroring Rust format_to_mime. Only the
// formats the API accepts (webp/png/jpeg/gif) are produced.
func imagePrepMIME(format imagePrepFormat) string {
	switch format {
	case imagePrepFormatJPEG:
		return "image/jpeg"
	case imagePrepFormatGIF:
		return "image/gif"
	case imagePrepFormatWebP:
		return "image/webp"
	default:
		return "image/png"
	}
}

type imagePrepFormat int

const (
	imagePrepFormatPNG imagePrepFormat = iota
	imagePrepFormatJPEG
	imagePrepFormatGIF
	imagePrepFormatWebP
)

// canImagePrepPreserveSourceBytes mirrors Rust can_preserve_source_bytes:
// PNG/JPEG/WebP pass through byte-for-byte when no resize is needed; GIF is
// always re-encoded because animated GIF support is out of the passthrough
// contract.
func canImagePrepPreserveSourceBytes(format imagePrepFormat) bool {
	switch format {
	case imagePrepFormatPNG, imagePrepFormatJPEG, imagePrepFormatWebP:
		return true
	default:
		return false
	}
}

// decodeImagePrepBytes decodes PNG/JPEG/GIF/WebP payloads and returns the
// decoded image plus its source dimensions. Format is detected by magic bytes
// (matching Rust image::guess_format); decode failures surface as
// ErrImagePrepInvalidDataURL so callers emit the processing-error placeholder.
func decodeImagePrepBytes(payload []byte) (imagePrepFormat, image.Image, uint32, uint32, error) {
	format, err := imagePrepGuessFormat(payload)
	if err != nil {
		return 0, nil, 0, 0, err
	}
	var decoded image.Image
	switch format {
	case imagePrepFormatPNG:
		decoded, err = png.Decode(bytes.NewReader(payload))
	case imagePrepFormatJPEG:
		decoded, err = jpeg.Decode(bytes.NewReader(payload))
	case imagePrepFormatGIF:
		decoded, err = gif.Decode(bytes.NewReader(payload))
	case imagePrepFormatWebP:
		decoded, err = webp.Decode(bytes.NewReader(payload))
	default:
		err = fmt.Errorf("%w: unsupported image format", ErrImagePrepInvalidDataURL)
	}
	if err != nil {
		return 0, nil, 0, 0, fmt.Errorf("%w: decode: %v", ErrImagePrepInvalidDataURL, err)
	}
	bounds := decoded.Bounds()
	return format, decoded, uint32(bounds.Dx()), uint32(bounds.Dy()), nil
}

func imagePrepGuessFormat(payload []byte) (imagePrepFormat, error) {
	switch {
	case len(payload) >= 8 && bytes.Equal(payload[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}):
		return imagePrepFormatPNG, nil
	case len(payload) >= 3 && payload[0] == 0xff && payload[1] == 0xd8 && payload[2] == 0xff:
		return imagePrepFormatJPEG, nil
	case len(payload) >= 6 && (string(payload[:6]) == "GIF87a" || string(payload[:6]) == "GIF89a"):
		return imagePrepFormatGIF, nil
	case len(payload) >= 12 && string(payload[0:4]) == "RIFF" && string(payload[8:12]) == "WEBP":
		return imagePrepFormatWebP, nil
	default:
		return 0, fmt.Errorf("%w: unsupported image format", ErrImagePrepInvalidDataURL)
	}
}

// resizeImagePrep scales the image to the target dimensions with bilinear
// interpolation (Rust uses FilterType::Triangle; pixel-level output is out of
// the observable contract - only dimensions and notice numbers are).
func resizeImagePrep(src image.Image, targetWidth uint32, targetHeight uint32) image.Image {
	srcBounds := src.Bounds()
	if uint32(srcBounds.Dx()) == targetWidth && uint32(srcBounds.Dy()) == targetHeight {
		return src
	}
	dst := image.NewRGBA(image.Rect(0, 0, int(targetWidth), int(targetHeight)))
	xdraw.BiLinear.Scale(dst, dst.Bounds(), src, srcBounds, xdraw.Over, nil)
	return dst
}

// encodeImagePrep encodes the resized image as PNG (lossless RGBA) or JPEG
// (quality 85), mirroring Rust encode_image (PNG RGBA8 / JPEG q85).
func encodeImagePrep(img image.Image, format imagePrepFormat) ([]byte, string, error) {
	var buf bytes.Buffer
	switch format {
	case imagePrepFormatJPEG:
		// JPEG cannot represent alpha; flatten onto white like Rust's image
		// crate JPEG encoder does for RGBA sources.
		flattened := image.NewRGBA(img.Bounds())
		xdraw.Draw(flattened, flattened.Bounds(), image.NewUniform(color.White), image.Point{}, xdraw.Src)
		xdraw.Draw(flattened, flattened.Bounds(), img, img.Bounds().Min, xdraw.Over)
		if err := jpeg.Encode(&buf, flattened, &jpeg.Options{Quality: 85}); err != nil {
			return nil, "", fmt.Errorf("encode jpeg: %w", err)
		}
		return buf.Bytes(), "image/jpeg", nil
	default:
		if err := png.Encode(&buf, img); err != nil {
			return nil, "", fmt.Errorf("encode png: %w", err)
		}
		return buf.Bytes(), "image/png", nil
	}
}

func ImagePrepPlaceholderForError(err error) string {
	switch {
	case errors.Is(err, ErrImagePrepRemoteURLUnsupported):
		return ImagePrepRemoteURLPlaceholder
	case errors.Is(err, ErrImagePrepUnsupportedLowDetail):
		return ImagePrepUnsupportedLowDetailPlaceholder
	case errors.Is(err, ErrImagePrepTooLarge):
		return ImagePrepTooLargePlaceholder
	default:
		return ImagePrepProcessingErrorPlaceholder
	}
}

func IsImagePrepRemoteURL(imageURL string) bool {
	scheme, _, ok := strings.Cut(imageURL, ":")
	return ok && (strings.EqualFold(scheme, "http") || strings.EqualFold(scheme, "https"))
}

func IsImagePrepDataURL(imageURL string) bool {
	return len(imageURL) >= len("data:") && strings.EqualFold(imageURL[:len("data:")], "data:")
}

func CanRequestOriginalImagePrepDetail(model string) bool {
	model = strings.ToLower(model)
	return strings.Contains(model, "gpt-5") || strings.Contains(model, "gpt-4.1") || strings.Contains(model, "o3")
}

func SanitizeOriginalImagePrepDetail(detail ImagePrepDetail, model string) ImagePrepDetail {
	if detail == ImagePrepDetailOriginal && !CanRequestOriginalImagePrepDetail(model) {
		return ImagePrepDetailHigh
	}
	if detail == "" {
		return ImagePrepDetailAuto
	}
	return detail
}

func splitImagePrepDataURL(imageURL string) (string, string, error) {
	if !IsImagePrepDataURL(imageURL) {
		return "", "", ErrImagePrepInvalidDataURL
	}
	header, payload, ok := strings.Cut(imageURL, ",")
	if !ok {
		return "", "", ErrImagePrepInvalidDataURL
	}
	mediaType := header[len("data:"):]
	if !strings.HasSuffix(strings.ToLower(mediaType), ";base64") {
		return "", "", ErrImagePrepInvalidDataURL
	}
	mediaType = mediaType[:len(mediaType)-len(";base64")]
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	return mediaType, payload, nil
}
