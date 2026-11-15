package utils

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func sanitizerTestPNG(t *testing.T, img image.Image, metadata PromptImageSourceMetadata) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return ApplyPromptImageMetadataToContainer("image/png", buf.Bytes(), metadata)
}

func sanitizerTestTIFFOrientation(value uint16) []byte {
	data := make([]byte, 26)
	copy(data, []byte("II"))
	binary.LittleEndian.PutUint16(data[2:4], 42)
	binary.LittleEndian.PutUint32(data[4:8], 8)
	binary.LittleEndian.PutUint16(data[8:10], 1)
	binary.LittleEndian.PutUint16(data[10:12], 0x0112)
	binary.LittleEndian.PutUint16(data[12:14], 3)
	binary.LittleEndian.PutUint32(data[14:18], 1)
	binary.LittleEndian.PutUint16(data[18:20], value)
	return data
}

func TestSanitizerAppliesJPEGOrientationAndStripsMetadataLikeRust(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 7, 11))
	for y := 0; y < 11; y++ {
		for x := 0; x < 7; x++ {
			source.SetRGBA(x, y, color.RGBA{R: uint8(x * 30), G: uint8(y * 20), B: 64, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, source, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	payload := insertJPEGMetadata(buf.Bytes(), promptImageMetadata{
		iccProfile: []byte("0123456789abcdefRGB "),
		exif:       sanitizerTestTIFFOrientation(6),
	})
	encoded, err := LoadForPromptBytesWithDecoder("oriented.jpg", payload, ModeOriginal, nil, DecoderSanitizer)
	if err != nil {
		t.Fatalf("sanitize: %v", err)
	}
	if encoded.Mime != "image/jpeg" {
		t.Fatalf("mime = %q", encoded.Mime)
	}
	if encoded.SourceWidth != 11 || encoded.SourceHeight != 7 {
		t.Fatalf("source dims = %dx%d, want 11x7", encoded.SourceWidth, encoded.SourceHeight)
	}
	if encoded.Width != 11 || encoded.Height != 7 {
		t.Fatalf("prepared dims = %dx%d, want 11x7", encoded.Width, encoded.Height)
	}
	restored := ExtractPromptImageSourceMetadata(encoded.Bytes)
	if !restored.Empty() {
		t.Fatalf("sanitized output kept metadata: %#v", restored)
	}
	if got := promptImageEXIFOrientation(encoded.Bytes); got != 1 {
		t.Fatalf("sanitized output orientation = %d, want 1", got)
	}
}

func TestSanitizerConvertsMatrixTRCProfileToSRGB(t *testing.T) {
	profile := sanitizerTestICCProfile()
	source := image.NewRGBA(image.Rect(0, 0, 1, 1))
	source.SetRGBA(0, 0, color.RGBA{R: 128, G: 64, B: 32, A: 255})
	payload := sanitizerTestPNG(t, source, PromptImageSourceMetadata{ICCProfile: profile})
	encoded, err := LoadForPromptBytesWithDecoder("profile.png", payload, ModeOriginal, nil, DecoderSanitizer)
	if err != nil {
		t.Fatalf("sanitize: %v", err)
	}
	decoded, err := png.Decode(bytes.NewReader(encoded.Bytes))
	if err != nil {
		t.Fatalf("decode output: %v", err)
	}
	r, _, _, _ := decoded.At(0, 0).RGBA()
	if r>>8 <= 150 {
		t.Fatalf("converted red = %d, want the linear curve to brighten mid-gray", r>>8)
	}
}

func TestSanitizerRejectsOversizedInput(t *testing.T) {
	payload := make([]byte, MaxSanitizedImageBytes+1)
	_, err := LoadForPromptBytesWithDecoder("huge.png", payload, ModeOriginal, nil, DecoderSanitizer)
	processing, ok := err.(*ProcessingError)
	if !ok || processing.Kind != "image_too_large" {
		t.Fatalf("error = %v, want image_too_large", err)
	}
}

func sanitizerTestICCProfile() []byte {
	type tag struct {
		signature string
		data      []byte
	}
	xyz := func(x, y, z float64) []byte {
		data := make([]byte, 20)
		copy(data, "XYZ ")
		binary.BigEndian.PutUint32(data[8:12], uint32(int32(x*65536)))
		binary.BigEndian.PutUint32(data[12:16], uint32(int32(y*65536)))
		binary.BigEndian.PutUint32(data[16:20], uint32(int32(z*65536)))
		return data
	}
	gamma := func(value uint16) []byte {
		data := make([]byte, 14)
		copy(data, "curv")
		binary.BigEndian.PutUint32(data[8:12], 1)
		binary.BigEndian.PutUint16(data[12:14], value)
		return data
	}
	tags := []tag{
		{"rXYZ", xyz(0.4360747, 0.2225045, 0.0139322)},
		{"gXYZ", xyz(0.3850649, 0.7168786, 0.0971045)},
		{"bXYZ", xyz(0.1430804, 0.0606169, 0.7141733)},
		{"rTRC", gamma(256)},
		{"gTRC", gamma(256)},
		{"bTRC", gamma(256)},
	}
	headerSize := 128
	tableSize := 4 + len(tags)*12
	offset := headerSize + tableSize
	payload := make([]byte, offset)
	copy(payload[16:20], "RGB ")
	copy(payload[20:24], "XYZ ")
	copy(payload[36:40], "acsp")
	binary.BigEndian.PutUint32(payload[128:132], uint32(len(tags)))
	var body []byte
	for i, entry := range tags {
		base := 132 + i*12
		copy(payload[base:base+4], entry.signature)
		binary.BigEndian.PutUint32(payload[base+4:base+8], uint32(offset+len(body)))
		binary.BigEndian.PutUint32(payload[base+8:base+12], uint32(len(entry.data)))
		body = append(body, entry.data...)
	}
	payload = append(payload, body...)
	binary.BigEndian.PutUint32(payload[0:4], uint32(len(payload)))
	return payload
}
