package utils

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"strings"

	"golang.org/x/image/webp"
)

// MaxSanitizedImageBytes mirrors Rust MAX_SANITIZED_IMAGE_BYTES: the same byte
// limit applies to the sanitizer input and output so a saved image fits when
// replayed.
const MaxSanitizedImageBytes = 79 * 1024 * 1024

// maxSanitizedDecodedPixels mirrors Rust MAX_DECODED_PIXELS (10,000 patches of
// 32x32 pixels for original detail).
const maxSanitizedDecodedPixels = 10_240_000

// Decoder selects how prompt images are decoded. The zero value is Legacy,
// which preserves the historical decode path; Sanitizer re-encodes the image
// without source metadata under strict byte and pixel limits.
type Decoder string

const (
	DecoderLegacy    Decoder = "legacy"
	DecoderSanitizer Decoder = "sanitizer"
)

// LoadForPromptBytesWithDecoder is LoadForPromptBytes for an explicit decoder.
func LoadForPromptBytesWithDecoder(path string, fileBytes []byte, mode Mode, limits *ResizeLimits, decoder Decoder) (EncodedImage, error) {
	if decoder == DecoderSanitizer {
		return sanitizePromptImage(path, fileBytes, mode, limits)
	}
	return loadForPromptBytes(path, fileBytes, mode, limits)
}

// LoadDataURLForPromptWithDecoder mirrors Rust load_data_url_for_prompt: the
// data URL payload is decoded under the decoder's byte limits and then run
// through the selected decode path.
func LoadDataURLForPromptWithDecoder(imageURL string, mode Mode, decoder Decoder) (EncodedImage, error) {
	_, payload, err := ParseDataURLForDecoder(imageURL, decoder)
	if err != nil {
		return EncodedImage{}, err
	}
	return LoadForPromptBytesWithDecoder("data URL", payload, mode, nil, decoder)
}

// ParseDataURLForDecoder is ParseDataURL with the decoder's byte limits: the
// sanitizer caps both the base64 payload and the decoded bytes at
// MaxSanitizedImageBytes, while the legacy decoder keeps the historical
// MaxPromptImageInputBytes guard.
func ParseDataURLForDecoder(imageURL string, decoder Decoder) (string, []byte, error) {
	if decoder != DecoderSanitizer {
		return ParseDataURL(imageURL)
	}
	maxBytes := MaxSanitizedImageBytes
	maxBase64Bytes := (maxBytes + 2) / 3 * 4
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
	if len(encoded) > maxBase64Bytes {
		return "", nil, &ProcessingError{Kind: "image_too_large", Reason: "base64 payload"}
	}
	payload, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", nil, &ProcessingError{Kind: "invalid_data_url", Reason: "invalid base64 payload: " + err.Error()}
	}
	if len(payload) > maxBytes {
		return "", nil, &ProcessingError{Kind: "image_too_large", Reason: "decoded input"}
	}
	mime := parts[0]
	if mime == "" {
		mime = "application/octet-stream"
	}
	return mime, payload, nil
}

// sanitizePromptImage mirrors Rust utils/image/sanitizer.rs: decode a supported
// prompt image, apply JPEG orientation and an RGB ICC profile, resize for the
// mode, and write fresh PNG (PNG/GIF inputs) or JPEG (JPEG/WebP inputs) bytes
// without source metadata.
func sanitizePromptImage(path string, fileBytes []byte, mode Mode, limits *ResizeLimits) (EncodedImage, error) {
	if len(fileBytes) > MaxSanitizedImageBytes {
		return EncodedImage{}, &ProcessingError{Kind: "image_too_large", Reason: "input"}
	}
	format, err := promptImageGuessFormat(fileBytes)
	if err != nil {
		return EncodedImage{}, &ProcessingError{Kind: "unsupported_image_format", Reason: "unknown"}
	}
	outputFormat := promptImageFormatPNG
	switch format {
	case promptImageFormatPNG, promptImageFormatGIF:
		outputFormat = promptImageFormatPNG
	case promptImageFormatJPEG, promptImageFormatWebP:
		outputFormat = promptImageFormatJPEG
	default:
		return EncodedImage{}, &ProcessingError{Kind: "unsupported_image_format", Reason: promptImageMIME(format)}
	}
	decoded, err := decodeSanitizedImage(format, fileBytes)
	if err != nil {
		return EncodedImage{}, &ProcessingError{Kind: "decode_error", Reason: path + ": " + err.Error()}
	}
	bounds := decoded.Bounds()
	pixels := uint64(bounds.Dx()) * uint64(bounds.Dy())
	if pixels > maxSanitizedDecodedPixels {
		return EncodedImage{}, &ProcessingError{Kind: "image_too_many_pixels"}
	}
	iccProfile := extractRawPromptImageICCProfile(format, fileBytes)
	if format == promptImageFormatJPEG {
		if orientation := promptImageEXIFOrientation(fileBytes); orientation > 1 {
			decoded = applyPromptImageOrientation(decoded, orientation)
		}
	}
	sourceWidth := uint32(decoded.Bounds().Dx())
	sourceHeight := uint32(decoded.Bounds().Dy())
	if format == promptImageFormatWebP {
		decoded = dropPromptImageAlpha(decoded)
	}
	preparedWidth, preparedHeight := OutputDimensions(sourceWidth, sourceHeight, mode, limits)
	if preparedWidth != sourceWidth || preparedHeight != sourceHeight {
		decoded = resizePromptImage(decoded, preparedWidth, preparedHeight)
	} else {
		preparedWidth, preparedHeight = sourceWidth, sourceHeight
	}
	if len(iccProfile) > 0 {
		if converted := convertPromptImageICCToSRGB(decoded, iccProfile); converted != nil {
			decoded = converted
		}
	}
	var encoded []byte
	if outputFormat == promptImageFormatPNG {
		var buf bytes.Buffer
		if err := png.Encode(&buf, decoded); err != nil {
			return EncodedImage{}, &ProcessingError{Kind: "encode_error", Reason: path + ": " + err.Error()}
		}
		encoded = buf.Bytes()
	} else {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, decoded, &jpeg.Options{Quality: 90}); err != nil {
			return EncodedImage{}, &ProcessingError{Kind: "encode_error", Reason: path + ": " + err.Error()}
		}
		encoded = buf.Bytes()
	}
	if len(encoded) > MaxSanitizedImageBytes {
		return EncodedImage{}, &ProcessingError{Kind: "image_too_large", Reason: "sanitized output"}
	}
	return EncodedImage{
		Bytes:        encoded,
		Mime:         promptImageMIME(outputFormat),
		Width:        preparedWidth,
		Height:       preparedHeight,
		SourceWidth:  sourceWidth,
		SourceHeight: sourceHeight,
	}, nil
}

func decodeSanitizedImage(format promptImageFormat, payload []byte) (image.Image, error) {
	reader := bytes.NewReader(payload)
	switch format {
	case promptImageFormatPNG:
		return png.Decode(reader)
	case promptImageFormatJPEG:
		return jpeg.Decode(reader)
	case promptImageFormatGIF:
		return gif.Decode(reader)
	case promptImageFormatWebP:
		return webp.Decode(reader)
	default:
		return nil, &ProcessingError{Kind: "unsupported_image_format"}
	}
}

// dropPromptImageAlpha mirrors Rust's `img.into_rgb8()` for WebP: the alpha
// channel is discarded because the JPEG output has none.
func dropPromptImageAlpha(src image.Image) image.Image {
	bounds := src.Bounds()
	dst := image.NewRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := src.At(x, y).RGBA()
			dst.SetRGBA(x, y, color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: 255})
		}
	}
	return dst
}

// applyPromptImageOrientation mirrors image crate apply_orientation for the
// eight EXIF orientation values.
func applyPromptImageOrientation(src image.Image, orientation int) image.Image {
	srcBounds := src.Bounds()
	width := srcBounds.Dx()
	height := srcBounds.Dy()
	if orientation < 2 || orientation > 8 {
		return src
	}
	dstWidth, dstHeight := width, height
	if orientation >= 5 {
		dstWidth, dstHeight = height, width
	}
	dst := image.NewRGBA(image.Rect(0, 0, dstWidth, dstHeight))
	for y := 0; y < dstHeight; y++ {
		for x := 0; x < dstWidth; x++ {
			var sx, sy int
			switch orientation {
			case 2: // mirror horizontal
				sx, sy = width-1-x, y
			case 3: // rotate 180
				sx, sy = width-1-x, height-1-y
			case 4: // mirror vertical
				sx, sy = x, height-1-y
			case 5: // transpose
				sx, sy = y, x
			case 6: // rotate 90 CW
				sx, sy = y, height-1-x
			case 7: // transverse
				sx, sy = width-1-y, height-1-x
			case 8: // rotate 270 CW
				sx, sy = width-1-y, x
			default:
				sx, sy = x, y
			}
			dst.Set(x, y, src.At(srcBounds.Min.X+sx, srcBounds.Min.Y+sy))
		}
	}
	return dst
}

// promptImageEXIFOrientation reads the EXIF Orientation tag (0x0112) from a
// JPEG payload, defaulting to 1 when absent or unreadable.
func promptImageEXIFOrientation(payload []byte) int {
	metadata := extractJPEGPromptImageMetadata(payload)
	return exifOrientationFromTIFF(metadata.exif)
}

func exifOrientationFromTIFF(tiff []byte) int {
	if len(tiff) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	if order.Uint16(tiff[2:4]) != 42 {
		return 1
	}
	ifdOffset := int(order.Uint32(tiff[4:8]))
	if ifdOffset < 8 || ifdOffset+2 > len(tiff) {
		return 1
	}
	count := int(order.Uint16(tiff[ifdOffset : ifdOffset+2]))
	offset := ifdOffset + 2
	for i := 0; i < count; i++ {
		entry := offset + i*12
		if entry+12 > len(tiff) {
			return 1
		}
		if order.Uint16(tiff[entry:entry+2]) == 0x0112 {
			if order.Uint16(tiff[entry+2:entry+4]) != 3 {
				return 1
			}
			value := int(order.Uint16(tiff[entry+8 : entry+10]))
			if value >= 1 && value <= 8 {
				return value
			}
			return 1
		}
	}
	return 1
}

// convertPromptImageICCToSRGB converts an 8-bit RGB/RGBA image tagged with a
// matrix/TRC ICC profile to sRGB. Profiles whose conversion is unsupported
// return nil so the caller keeps the decoded pixels.
func convertPromptImageICCToSRGB(img image.Image, profile []byte) image.Image {
	parsed, ok := parseICCProfile(profile)
	if !ok {
		return nil
	}
	bounds := img.Bounds()
	dst := image.NewNRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			linearR := parsed.rTRC(float64(r) / 65535)
			linearG := parsed.gTRC(float64(g) / 65535)
			linearB := parsed.bTRC(float64(b) / 65535)
			// RGB -> XYZ (D50 PCS).
			xyzX := parsed.matrix[0][0]*linearR + parsed.matrix[0][1]*linearG + parsed.matrix[0][2]*linearB
			xyzY := parsed.matrix[1][0]*linearR + parsed.matrix[1][1]*linearG + parsed.matrix[1][2]*linearB
			xyzZ := parsed.matrix[2][0]*linearR + parsed.matrix[2][1]*linearG + parsed.matrix[2][2]*linearB
			// XYZ (D50) -> linear sRGB.
			srgbR := 3.1338561*xyzX - 1.6168667*xyzY - 0.4906146*xyzZ
			srgbG := -0.9787684*xyzX + 1.9161415*xyzY + 0.0334540*xyzZ
			srgbB := 0.0719453*xyzX - 0.2289914*xyzY + 1.4052427*xyzZ
			dst.SetNRGBA(x, y, color.NRGBA{
				R: srgbEncode(srgbR),
				G: srgbEncode(srgbG),
				B: srgbEncode(srgbB),
				A: uint8(a >> 8),
			})
		}
	}
	return dst
}

func srgbEncode(linear float64) uint8 {
	if linear <= 0 {
		return 0
	}
	if linear >= 1 {
		return 255
	}
	var encoded float64
	if linear <= 0.0031308 {
		encoded = 12.92 * linear
	} else {
		encoded = 1.055*math.Pow(linear, 1/2.4) - 0.055
	}
	return uint8(math.Round(encoded * 255))
}

// linearToSRGB uses the standard sRGB transfer function for the ICC matrix/TRC
// input side when a curve is described as gamma only; ICC curves are encoded
// (device) values, so the curve table maps encoded -> linear directly.
type iccProfile struct {
	matrix [3][3]float64
	rTRC   func(float64) float64
	gTRC   func(float64) float64
	bTRC   func(float64) float64
}

func parseICCProfile(data []byte) (*iccProfile, bool) {
	if len(data) < 132 || string(data[36:40]) != "acsp" || string(data[16:20]) != "RGB " {
		return nil, false
	}
	tagCount := int(binary.BigEndian.Uint32(data[128:132]))
	tags := map[string][]byte{}
	for i := 0; i < tagCount; i++ {
		entry := 132 + i*12
		if entry+12 > len(data) {
			break
		}
		signature := string(data[entry : entry+4])
		offset := int(binary.BigEndian.Uint32(data[entry+4 : entry+8]))
		size := int(binary.BigEndian.Uint32(data[entry+8 : entry+12]))
		if offset < 0 || size < 0 || offset+size > len(data) {
			continue
		}
		tags[signature] = data[offset : offset+size]
	}
	readXYZ := func(signature string) ([3]float64, bool) {
		tag, ok := tags[signature]
		if !ok || len(tag) < 20 || string(tag[:4]) != "XYZ " {
			return [3]float64{}, false
		}
		return [3]float64{
			iccS15Fixed16(tag[8:12]),
			iccS15Fixed16(tag[12:16]),
			iccS15Fixed16(tag[16:20]),
		}, true
	}
	rXYZ, okR := readXYZ("rXYZ")
	gXYZ, okG := readXYZ("gXYZ")
	bXYZ, okB := readXYZ("bXYZ")
	if !okR || !okG || !okB {
		return nil, false
	}
	readTRC := func(signature string) (func(float64) float64, bool) {
		tag, ok := tags[signature]
		if !ok {
			return nil, false
		}
		return parseICCCurve(tag)
	}
	rTRC, okR := readTRC("rTRC")
	gTRC, okG := readTRC("gTRC")
	bTRC, okB := readTRC("bTRC")
	if !okR || !okG || !okB {
		return nil, false
	}
	return &iccProfile{
		matrix: [3][3]float64{
			{rXYZ[0], gXYZ[0], bXYZ[0]},
			{rXYZ[1], gXYZ[1], bXYZ[1]},
			{rXYZ[2], gXYZ[2], bXYZ[2]},
		},
		rTRC: rTRC,
		gTRC: gTRC,
		bTRC: bTRC,
	}, true
}

func iccS15Fixed16(value []byte) float64 {
	return float64(int32(binary.BigEndian.Uint32(value))) / 65536
}

func parseICCCurve(tag []byte) (func(float64) float64, bool) {
	if len(tag) < 12 {
		return nil, false
	}
	switch string(tag[:4]) {
	case "curv":
		count := int(binary.BigEndian.Uint32(tag[8:12]))
		if count == 0 {
			return func(value float64) float64 { return clampUnit(value) }, true
		}
		if count == 1 {
			if len(tag) < 14 {
				return nil, false
			}
			gamma := float64(binary.BigEndian.Uint16(tag[12:14])) / 256
			return func(value float64) float64 { return math.Pow(clampUnit(value), gamma) }, true
		}
		if len(tag) < 12+count*2 {
			return nil, false
		}
		table := make([]float64, count)
		for i := 0; i < count; i++ {
			table[i] = float64(binary.BigEndian.Uint16(tag[12+i*2:14+i*2])) / 65535
		}
		return func(value float64) float64 {
			value = clampUnit(value)
			position := value * float64(count-1)
			index := int(position)
			if index >= count-1 {
				return table[count-1]
			}
			fraction := position - float64(index)
			return table[index]*(1-fraction) + table[index+1]*fraction
		}, true
	case "para":
		curveType := binary.BigEndian.Uint16(tag[8:10])
		paramCount := 1
		switch curveType {
		case 0:
			paramCount = 1
		case 1:
			paramCount = 3
		case 2:
			paramCount = 4
		case 3:
			paramCount = 5
		case 4:
			paramCount = 7
		default:
			return nil, false
		}
		if len(tag) < 12+paramCount*4 {
			return nil, false
		}
		params := make([]float64, paramCount)
		for i := range params {
			params[i] = iccS15Fixed16(tag[12+i*4 : 16+i*4])
		}
		return func(value float64) float64 {
			value = clampUnit(value)
			g := params[0]
			switch curveType {
			case 0:
				return math.Pow(value, g)
			case 1:
				a, b := params[1], params[2]
				if value >= -b/a {
					return math.Pow(a*value+b, g)
				}
				return 0
			case 2:
				a, b, c := params[1], params[2], params[3]
				if value >= -b/a {
					return math.Pow(a*value+b, g) + c
				}
				return c
			case 3:
				a, b, c, d := params[1], params[2], params[3], params[4]
				if value >= d {
					return math.Pow(a*value+b, g)
				}
				return c * value
			case 4:
				a, b, c, d, e, f := params[1], params[2], params[3], params[4], params[5], params[6]
				if value >= d {
					return math.Pow(a*value+b, g) + e
				}
				return c*value + f
			default:
				return value
			}
		}, true
	default:
		return nil, false
	}
}

func clampUnit(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}
