// Package imagedecode decodes image pixels in their display orientation
// within the caller's allocation limits. It mirrors Rust's tui image_decode
// module (Rust #60455), which the clipboard paste path and the transcript
// thumbnails share.
package imagedecode

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
)

// ErrTooLarge reports that the decoded raster exceeds the caller's budget.
var ErrTooLarge = errors.New("image exceeds the allocation budget")

// DecodeFile decodes path in display orientation. maxAlloc bounds the decoded
// raster in bytes; pixels are budgeted as four bytes each, matching the
// transcript thumbnail limit.
func DecodeFile(path string, maxAlloc int64) (image.Image, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return decode(file, maxAlloc)
}

// DecodeBytes decodes data in display orientation under the same budget rules
// as DecodeFile.
func DecodeBytes(data []byte, maxAlloc int64) (image.Image, error) {
	return decode(bytes.NewReader(data), maxAlloc)
}

// Decode decodes r in display orientation under the same budget rules.
func Decode(r io.Reader, maxAlloc int64) (image.Image, error) {
	return decode(r, maxAlloc)
}

func decode(reader io.Reader, maxAlloc int64) (image.Image, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if maxAlloc > 0 && int64(config.Width)*int64(config.Height)*4 > maxAlloc {
		return nil, ErrTooLarge
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return ApplyOrientation(decoded, Orientation(data)), nil
}

// Orientation reports the EXIF orientation (1-8) carried by JPEG or PNG data.
// Anything else, including a missing tag, reports 1.
func Orientation(data []byte) int {
	exif := exifPayload(data)
	if len(exif) == 0 {
		return 1
	}
	if orientation := orientationFromTIFF(exif); orientation != 0 {
		return orientation
	}
	return 1
}

// ApplyOrientation rotates and mirrors decoded pixels into display orientation.
// The mapping matches the image crate operations Rust applies:
// 2 flip horizontally, 3 rotate 180, 4 flip vertically, 5 transpose, 6 rotate
// 90 clockwise, 7 transverse, 8 rotate 270 clockwise.
func ApplyOrientation(source image.Image, orientation int) image.Image {
	if source == nil || orientation < 2 || orientation > 8 {
		return source
	}
	switch orientation {
	case 2:
		return flipHorizontal(source)
	case 3:
		return rotate180(source)
	case 4:
		return flipVertical(source)
	case 5:
		return flipHorizontal(rotate90(source))
	case 6:
		return rotate90(source)
	case 7:
		return flipHorizontal(rotate270(source))
	default:
		return rotate270(source)
	}
}

func exifPayload(data []byte) []byte {
	switch {
	case bytes.HasPrefix(data, []byte{0xFF, 0xD8}):
		return jpegExifPayload(data)
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return pngExifPayload(data)
	default:
		return nil
	}
}

func jpegExifPayload(data []byte) []byte {
	for offset := 2; offset+4 <= len(data); {
		if data[offset] != 0xFF {
			return nil
		}
		marker := data[offset+1]
		if marker == 0xD8 || marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7) {
			offset += 2
			continue
		}
		if marker == 0xD9 || marker == 0xDA {
			return nil
		}
		length := int(binary.BigEndian.Uint16(data[offset+2 : offset+4]))
		if length < 2 || offset+2+length > len(data) {
			return nil
		}
		segment := data[offset+4 : offset+2+length]
		if marker == 0xE1 && bytes.HasPrefix(segment, []byte("Exif\x00\x00")) {
			return segment[6:]
		}
		offset += 2 + length
	}
	return nil
}

func pngExifPayload(data []byte) []byte {
	for offset := 8; offset+8 <= len(data); {
		length := int(binary.BigEndian.Uint32(data[offset : offset+4]))
		chunkType := data[offset+4 : offset+8]
		start := offset + 8
		end := start + length
		if length < 0 || end+4 > len(data) {
			return nil
		}
		if string(chunkType) == "eXIf" {
			return data[start:end]
		}
		if string(chunkType) == "IEND" {
			return nil
		}
		offset = end + 4
	}
	return nil
}

// orientationFromTIFF reads IFD0 tag 0x0112 from a TIFF or EXIF payload.
func orientationFromTIFF(data []byte) int {
	if len(data) < 8 {
		return 0
	}
	var order binary.ByteOrder
	switch {
	case data[0] == 'I' && data[1] == 'I':
		order = binary.LittleEndian
	case data[0] == 'M' && data[1] == 'M':
		order = binary.BigEndian
	default:
		return 0
	}
	if order.Uint16(data[2:4]) != 42 {
		return 0
	}
	ifdOffset := int(order.Uint32(data[4:8]))
	if ifdOffset < 8 || ifdOffset+2 > len(data) {
		return 0
	}
	count := int(order.Uint16(data[ifdOffset : ifdOffset+2]))
	for index := 0; index < count; index++ {
		entry := ifdOffset + 2 + index*12
		if entry+12 > len(data) {
			return 0
		}
		if order.Uint16(data[entry:entry+2]) != 0x0112 {
			continue
		}
		value := int(order.Uint16(data[entry+8 : entry+10]))
		if value >= 1 && value <= 8 {
			return value
		}
		return 0
	}
	return 0
}

// toRGBA copies source pixels into a new RGBA raster of the same size.
func toRGBA(source image.Image) *image.RGBA {
	rect := source.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
	draw.Draw(dst, dst.Bounds(), source, rect.Min, draw.Src)
	return dst
}

func flipHorizontal(source image.Image) image.Image {
	src := toRGBA(source)
	width, height := src.Bounds().Dx(), src.Bounds().Dy()
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			dst.SetRGBA(x, y, src.RGBAAt(width-1-x, y))
		}
	}
	return dst
}

func flipVertical(source image.Image) image.Image {
	src := toRGBA(source)
	width, height := src.Bounds().Dx(), src.Bounds().Dy()
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			dst.SetRGBA(x, y, src.RGBAAt(x, height-1-y))
		}
	}
	return dst
}

func rotate180(source image.Image) image.Image {
	src := toRGBA(source)
	width, height := src.Bounds().Dx(), src.Bounds().Dy()
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			dst.SetRGBA(x, y, src.RGBAAt(width-1-x, height-1-y))
		}
	}
	return dst
}

// rotate90 rotates the pixels 90 degrees clockwise.
func rotate90(source image.Image) image.Image {
	src := toRGBA(source)
	width, height := src.Bounds().Dx(), src.Bounds().Dy()
	dst := image.NewRGBA(image.Rect(0, 0, height, width))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			dst.SetRGBA(height-1-y, x, src.RGBAAt(x, y))
		}
	}
	return dst
}

// rotate270 rotates the pixels 90 degrees counter-clockwise.
func rotate270(source image.Image) image.Image {
	src := toRGBA(source)
	width, height := src.Bounds().Dx(), src.Bounds().Dy()
	dst := image.NewRGBA(image.Rect(0, 0, height, width))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			dst.SetRGBA(y, width-1-x, src.RGBAAt(x, y))
		}
	}
	return dst
}
