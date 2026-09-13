package utils

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/jpeg"
	"image/png"
	"testing"
)

func testRGBProfile() []byte {
	profile := make([]byte, 128)
	copy(profile, "fake icc profile")
	copy(profile[16:20], "RGB ")
	return profile
}

func testExifPayload() []byte {
	// A minimal little-endian TIFF header plus an orientation tag payload.
	return []byte{'I', 'I', 42, 0, 8, 0, 0, 0, 1, 0, 0x12, 0x01, 3, 0, 1, 0, 0, 0, 6, 0, 0, 0, 0, 0, 0, 0}
}

func encodeTestJpeg(t *testing.T, width int, height int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, width, height)), &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

// TestLoadForPromptBytesPreservesPNGMetadata covers Rust's encoder metadata:
// a resized PNG keeps the source's RGB ICC profile and EXIF payload in iCCP and
// eXIf chunks.
func TestLoadForPromptBytesPreservesPNGMetadata(t *testing.T) {
	source := promptImagePNG(t, 3000, 100)
	source = insertPNGMetadata(source, promptImageMetadata{iccProfile: testRGBProfile(), exif: testExifPayload()})

	encoded, err := LoadForPromptBytes("wide.png", source, ModeResizeToFit)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if encoded.Width != 2048 || encoded.Mime != "image/png" {
		t.Fatalf("encoded = %#v", encoded)
	}
	metadata := extractPromptImageMetadata(promptImageFormatPNG, encoded.Bytes)
	if !bytes.Equal(metadata.iccProfile, testRGBProfile()) {
		t.Fatalf("icc profile not preserved: %d bytes", len(metadata.iccProfile))
	}
	if !bytes.Equal(metadata.exif, testExifPayload()) {
		t.Fatalf("exif not preserved: %x", metadata.exif)
	}
	if config, err := png.DecodeConfig(bytes.NewReader(encoded.Bytes)); err != nil || config.Width != 2048 {
		t.Fatalf("resized png unreadable: %#v (err=%v)", config, err)
	}
	if err := verifyPNGChunkCRCs(encoded.Bytes); err != nil {
		t.Fatalf("chunk crc: %v", err)
	}
}

func webpTestContainer(chunks ...[2][]byte) []byte {
	body := []byte("WEBP")
	for _, chunk := range chunks {
		kind, data := chunk[0], chunk[1]
		body = append(body, kind...)
		var size [4]byte
		binary.LittleEndian.PutUint32(size[:], uint32(len(data)))
		body = append(body, size[:]...)
		body = append(body, data...)
		if len(data)%2 == 1 {
			body = append(body, 0)
		}
	}
	out := []byte("RIFF")
	var size [4]byte
	binary.LittleEndian.PutUint32(size[:], uint32(len(body)))
	out = append(out, size[:]...)
	return append(out, body...)
}

// TestExtractWebPMetadataReadsICCPAndEXIF covers Rust's WebP decoder accessors:
// a WebP source keeps its RGB ICC profile and EXIF payload when the re-encode
// falls back to PNG (Go has no WebP encoder).
func TestExtractWebPMetadataReadsICCPAndEXIF(t *testing.T) {
	source := webpTestContainer(
		[2][]byte{[]byte("VP8L"), {0x2f, 0x01, 0x00, 0x00, 0x00}},
		[2][]byte{[]byte("ICCP"), testRGBProfile()},
		[2][]byte{[]byte("EXIF"), testExifPayload()},
	)
	metadata := ExtractPromptImageSourceMetadata(source)
	if !bytes.Equal(metadata.ICCProfile, testRGBProfile()) {
		t.Fatalf("webp icc profile = %d bytes", len(metadata.ICCProfile))
	}
	if !bytes.Equal(metadata.EXIF, testExifPayload()) {
		t.Fatalf("webp exif = %x", metadata.EXIF)
	}

	// A non-RGB profile is dropped and non-WebP bytes report no metadata.
	cmykProfile := make([]byte, 128)
	copy(cmykProfile, "fake icc profile")
	copy(cmykProfile[16:20], "CMYK")
	cmyk := ExtractPromptImageSourceMetadata(webpTestContainer([2][]byte{[]byte("ICCP"), cmykProfile}))
	if len(cmyk.ICCProfile) != 0 {
		t.Fatalf("non-RGB webp profile survived: %d bytes", len(cmyk.ICCProfile))
	}
	if other := ExtractPromptImageSourceMetadata([]byte("RIFFxxxxNOPE")); !other.Empty() {
		t.Fatalf("non-WebP container reported metadata: %#v", other)
	}
}

// TestLoadForPromptBytesPreservesJPEGMetadata covers the JPEG path: a resized
// JPEG keeps APP1 (Exif) and APP2 (ICC) segments and stays decodable.
func TestLoadForPromptBytesPreservesJPEGMetadata(t *testing.T) {
	source := encodeTestJpeg(t, 3000, 100)
	source = insertJPEGMetadata(source, promptImageMetadata{iccProfile: testRGBProfile(), exif: testExifPayload()})

	encoded, err := LoadForPromptBytes("wide.jpg", source, ModeResizeToFit)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if encoded.Mime != "image/jpeg" || encoded.Width != 2048 {
		t.Fatalf("encoded = %#v", encoded)
	}
	metadata := extractPromptImageMetadata(promptImageFormatJPEG, encoded.Bytes)
	if !bytes.Equal(metadata.iccProfile, testRGBProfile()) {
		t.Fatalf("icc profile not preserved: %d bytes", len(metadata.iccProfile))
	}
	if !bytes.Equal(metadata.exif, testExifPayload()) {
		t.Fatalf("exif not preserved: %x", metadata.exif)
	}
	if _, err := jpeg.Decode(bytes.NewReader(encoded.Bytes)); err != nil {
		t.Fatalf("resized jpeg unreadable: %v", err)
	}
}

// TestNonRGBICCProfileIsDropped covers Rust's RGB-only ICC filter.
func TestNonRGBICCProfileIsDropped(t *testing.T) {
	profile := make([]byte, 128)
	copy(profile, "fake icc profile")
	copy(profile[16:20], "CMYK")
	source := promptImagePNG(t, 3000, 100)
	source = insertPNGMetadata(source, promptImageMetadata{iccProfile: profile})
	encoded, err := LoadForPromptBytes("wide.png", source, ModeResizeToFit)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if metadata := extractPromptImageMetadata(promptImageFormatPNG, encoded.Bytes); len(metadata.iccProfile) != 0 {
		t.Fatalf("non-RGB profile must be dropped: %d bytes", len(metadata.iccProfile))
	}
}

// TestExtractJPEGMetadataConcatenatesICCChunks covers multi-segment ICC
// reassembly.
func TestExtractJPEGMetadataConcatenatesICCChunks(t *testing.T) {
	profile := bytes.Repeat([]byte{7}, 70000)
	copy(profile[16:20], "RGB ")
	source := encodeTestJpeg(t, 8, 4)
	source = insertJPEGMetadata(source, promptImageMetadata{iccProfile: profile})
	metadata := extractPromptImageMetadata(promptImageFormatJPEG, source)
	if !bytes.Equal(metadata.iccProfile, profile) {
		t.Fatalf("icc reassembly = %d bytes, want %d", len(metadata.iccProfile), len(profile))
	}
}

// verifyPNGChunkCRCs independently recomputes every PNG chunk CRC.
func verifyPNGChunkCRCs(payload []byte) error {
	signature := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	offset := len(signature)
	for offset+12 <= len(payload) {
		length := int(binary.BigEndian.Uint32(payload[offset : offset+4]))
		if offset+12+length > len(payload) {
			return nil
		}
		kind := payload[offset+4 : offset+8]
		data := payload[offset+8 : offset+8+length]
		want := binary.BigEndian.Uint32(payload[offset+8+length : offset+12+length])
		crc := crc32.NewIEEE()
		_, _ = crc.Write(kind)
		_, _ = crc.Write(data)
		if got := crc.Sum32(); got != want {
			return errCRCMismatch
		}
		offset += 12 + length
	}
	return nil
}

var errCRCMismatch = errorString("png chunk crc mismatch")

type errorString string

func (e errorString) Error() string { return string(e) }
