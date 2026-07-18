package eventmap

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
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
	if IsImagePrepRemoteURL(imageURL) {
		return "", ErrImagePrepRemoteURLUnsupported
	}
	if !IsImagePrepDataURL(imageURL) {
		return imageURL, nil
	}
	if detail == "" || detail == ImagePrepDetailAuto {
		detail = ImagePrepDetailHigh
	}
	if detail == ImagePrepDetailLow {
		return "", ErrImagePrepUnsupportedLowDetail
	}
	mediaType, payload, err := splitImagePrepDataURL(imageURL)
	if err != nil {
		return "", err
	}
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrImagePrepInvalidDataURL, err)
	}
	limit := imagePrepHighDetailMaxBytes
	if detail == ImagePrepDetailOriginal {
		limit = imagePrepOriginalDetailMaxBytes
	}
	if len(decoded) > limit {
		return "", fmt.Errorf("%w: %d > %d", ErrImagePrepTooLarge, len(decoded), limit)
	}
	return "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(decoded), nil
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
