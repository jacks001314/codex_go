package utils

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
	"io"
)

// Rust parity: codex-rs/utils/image `apply_image_metadata` plus the decoder's
// metadata extraction. Re-encoded prompt images keep the source's RGB ICC
// profile and EXIF payload (orientation); Go's stdlib encoders cannot write
// them, so the container chunks/segments are inserted after encoding.
type promptImageMetadata struct {
	iccProfile []byte
	exif       []byte
}

// PromptImageSourceMetadata mirrors Rust's ImageMetadata: the RGB ICC profile
// and EXIF payload (orientation) read from a decoded source container. Other
// format-specific metadata is intentionally not copied.
type PromptImageSourceMetadata struct {
	ICCProfile []byte
	EXIF       []byte
}

// Empty reports whether any preserved metadata is present.
func (m PromptImageSourceMetadata) Empty() bool {
	return len(m.ICCProfile) == 0 && len(m.EXIF) == 0
}

// ExtractPromptImageSourceMetadata reads the RGB ICC profile and EXIF payload
// from a PNG or JPEG source container, mirroring Rust's decoder accessors. Go has
// no container reader for the remaining formats, so they report no metadata.
func ExtractPromptImageSourceMetadata(payload []byte) PromptImageSourceMetadata {
	format, err := promptImageGuessFormat(payload)
	if err != nil {
		return PromptImageSourceMetadata{}
	}
	metadata := extractPromptImageMetadata(format, payload)
	return PromptImageSourceMetadata{ICCProfile: metadata.iccProfile, EXIF: metadata.exif}
}

// ApplyPromptImageMetadataToContainer inserts the source metadata into freshly
// encoded PNG or JPEG bytes (Rust's apply_image_metadata); mime selects the
// container and any other value leaves the bytes untouched.
func ApplyPromptImageMetadataToContainer(mime string, encoded []byte, metadata PromptImageSourceMetadata) []byte {
	if metadata.Empty() {
		return encoded
	}
	restored := promptImageMetadata{iccProfile: metadata.ICCProfile, exif: metadata.EXIF}
	switch mime {
	case "image/png":
		return applyPromptImageMetadata(promptImageFormatPNG, encoded, restored)
	case "image/jpeg":
		return applyPromptImageMetadata(promptImageFormatJPEG, encoded, restored)
	default:
		return encoded
	}
}

func (m promptImageMetadata) empty() bool {
	return len(m.iccProfile) == 0 && len(m.exif) == 0
}

// extractPromptImageMetadata reads the RGB ICC profile and EXIF payload from a
// decoded source container, mirroring Rust's decoder accessors.
func extractPromptImageMetadata(format promptImageFormat, payload []byte) promptImageMetadata {
	switch format {
	case promptImageFormatPNG:
		return extractPNGPromptImageMetadata(payload)
	case promptImageFormatJPEG:
		return extractJPEGPromptImageMetadata(payload)
	case promptImageFormatWebP:
		// Rust's decoder exposes the WebP container's ICCP and EXIF chunks the
		// same way it does for PNG/JPEG, so a WebP source keeps its metadata when
		// the re-encode falls back to another container (#44027).
		return extractWebPPromptImageMetadata(payload)
	default:
		return promptImageMetadata{}
	}
}

// applyPromptImageMetadata writes the metadata into freshly encoded bytes.
func applyPromptImageMetadata(format promptImageFormat, encoded []byte, metadata promptImageMetadata) []byte {
	if metadata.empty() {
		return encoded
	}
	switch format {
	case promptImageFormatPNG:
		return insertPNGMetadata(encoded, metadata)
	case promptImageFormatJPEG:
		return insertJPEGMetadata(encoded, metadata)
	default:
		return encoded
	}
}

// rgbICCProfile mirrors Rust's filter: only RGB profiles are safe to copy across
// re-encoding paths (bytes 16..20 are the ICC data color space signature).
func rgbICCProfile(profile []byte) []byte {
	if len(profile) < 20 || string(profile[16:20]) != "RGB " {
		return nil
	}
	return profile
}

type pngChunk struct {
	kind string
	data []byte
}

func parsePNGChunks(payload []byte) ([]pngChunk, bool) {
	signature := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	if len(payload) < len(signature) || !bytes.Equal(payload[:len(signature)], signature) {
		return nil, false
	}
	chunks := []pngChunk{}
	offset := len(signature)
	for offset+8 <= len(payload) {
		length := int(binary.BigEndian.Uint32(payload[offset : offset+4]))
		if length < 0 || offset+12+length > len(payload) {
			return nil, false
		}
		kind := string(payload[offset+4 : offset+8])
		data := payload[offset+8 : offset+8+length]
		chunks = append(chunks, pngChunk{kind: kind, data: data})
		offset += 12 + length
		if kind == "IEND" {
			break
		}
	}
	return chunks, true
}

func extractPNGPromptImageMetadata(payload []byte) promptImageMetadata {
	chunks, ok := parsePNGChunks(payload)
	if !ok {
		return promptImageMetadata{}
	}
	metadata := promptImageMetadata{}
	for _, chunk := range chunks {
		switch chunk.kind {
		case "iCCP":
			separator := bytes.IndexByte(chunk.data, 0)
			if separator < 0 || separator+2 > len(chunk.data) {
				continue
			}
			if method := chunk.data[separator+1]; method != 0 {
				continue
			}
			reader, err := zlib.NewReader(bytes.NewReader(chunk.data[separator+2:]))
			if err != nil {
				continue
			}
			profile, err := io.ReadAll(io.LimitReader(reader, MaxPromptImageInputBytes))
			_ = reader.Close()
			if err == nil {
				metadata.iccProfile = rgbICCProfile(profile)
			}
		case "eXIf":
			metadata.exif = append([]byte(nil), chunk.data...)
		}
	}
	return metadata
}

// insertPNGMetadata inserts iCCP/eXIf chunks after IHDR, matching the encoder
// metadata placement the image crate produces.
func insertPNGMetadata(encoded []byte, metadata promptImageMetadata) []byte {
	signature := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	if len(encoded) < len(signature) || !bytes.Equal(encoded[:len(signature)], signature) {
		return encoded
	}
	insertAt := len(signature)
	if insertAt+8 <= len(encoded) && string(encoded[insertAt+4:insertAt+8]) == "IHDR" {
		length := int(binary.BigEndian.Uint32(encoded[insertAt : insertAt+4]))
		if insertAt+12+length <= len(encoded) {
			insertAt += 12 + length
		}
	}
	var out bytes.Buffer
	out.Write(encoded[:insertAt])
	if len(metadata.iccProfile) > 0 {
		var compressed bytes.Buffer
		writer := zlib.NewWriter(&compressed)
		_, _ = writer.Write(metadata.iccProfile)
		_ = writer.Close()
		payload := append([]byte("icc\x00\x00"), compressed.Bytes()...)
		out.Write(encodePNGChunk("iCCP", payload))
	}
	if len(metadata.exif) > 0 {
		out.Write(encodePNGChunk("eXIf", metadata.exif))
	}
	out.Write(encoded[insertAt:])
	return out.Bytes()
}

func encodePNGChunk(kind string, data []byte) []byte {
	var buf bytes.Buffer
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(data)))
	buf.Write(length[:])
	buf.WriteString(kind)
	buf.Write(data)
	crc := crc32.NewIEEE()
	_, _ = crc.Write([]byte(kind))
	_, _ = crc.Write(data)
	var sum [4]byte
	binary.BigEndian.PutUint32(sum[:], crc.Sum32())
	buf.Write(sum[:])
	return buf.Bytes()
}

// webpChunk is one RIFF chunk of a WebP container.
type webpChunk struct {
	kind string
	data []byte
}

// parseWebPChunks walks a RIFF/WEBP container's chunks, stopping at a truncated
// chunk or the declared container end.
func parseWebPChunks(payload []byte) ([]webpChunk, bool) {
	if len(payload) < 12 || !bytes.Equal(payload[:4], []byte("RIFF")) || !bytes.Equal(payload[8:12], []byte("WEBP")) {
		return nil, false
	}
	end := 8 + int(binary.LittleEndian.Uint32(payload[4:8]))
	if end > len(payload) || end < 12 {
		end = len(payload)
	}
	chunks := []webpChunk{}
	offset := 12
	for offset+8 <= end {
		kind := string(payload[offset : offset+4])
		length := int(binary.LittleEndian.Uint32(payload[offset+4 : offset+8]))
		if length < 0 || offset+8+length > end {
			return chunks, true
		}
		chunks = append(chunks, webpChunk{kind: kind, data: payload[offset+8 : offset+8+length]})
		offset += 8 + length
		// RIFF chunks are padded to an even size.
		if length%2 == 1 {
			offset++
		}
	}
	return chunks, true
}

// extractWebPPromptImageMetadata reads the ICCP (ICC profile) and EXIF chunks,
// mirroring Rust's WebP decoder accessors.
func extractWebPPromptImageMetadata(payload []byte) promptImageMetadata {
	chunks, ok := parseWebPChunks(payload)
	if !ok {
		return promptImageMetadata{}
	}
	metadata := promptImageMetadata{}
	for _, chunk := range chunks {
		switch chunk.kind {
		case "ICCP":
			metadata.iccProfile = rgbICCProfile(append([]byte(nil), chunk.data...))
		case "EXIF":
			metadata.exif = append([]byte(nil), chunk.data...)
		}
	}
	return metadata
}

// jpegSegments walks the marker segments before the entropy-coded data.
func jpegSegments(payload []byte) ([]jpegSegment, bool) {
	if len(payload) < 2 || payload[0] != 0xff || payload[1] != 0xd8 {
		return nil, false
	}
	segments := []jpegSegment{}
	offset := 2
	for offset+4 <= len(payload) {
		if payload[offset] != 0xff {
			return segments, true
		}
		marker := payload[offset+1]
		if marker == 0xd9 || marker == 0xda { // EOI / start of scan
			return segments, true
		}
		length := int(binary.BigEndian.Uint16(payload[offset+2 : offset+4]))
		if length < 2 || offset+2+length > len(payload) {
			return segments, true
		}
		segments = append(segments, jpegSegment{marker: marker, data: payload[offset+4 : offset+2+length]})
		offset += 2 + length
	}
	return segments, true
}

type jpegSegment struct {
	marker byte
	data   []byte
}

func extractJPEGPromptImageMetadata(payload []byte) promptImageMetadata {
	segments, ok := jpegSegments(payload)
	if !ok {
		return promptImageMetadata{}
	}
	metadata := promptImageMetadata{}
	var icc []byte
	for _, segment := range segments {
		switch segment.marker {
		case 0xe1:
			if len(segment.data) > 6 && bytes.Equal(segment.data[:6], []byte("Exif\x00\x00")) {
				metadata.exif = append([]byte(nil), segment.data[6:]...)
			}
		case 0xe2:
			if len(segment.data) > 14 && bytes.Equal(segment.data[:12], []byte("ICC_PROFILE\x00")) {
				icc = append(icc, segment.data[14:]...)
			}
		}
	}
	metadata.iccProfile = rgbICCProfile(icc)
	return metadata
}

// insertJPEGMetadata writes APP1 (Exif) and APP2 (ICC) segments right after SOI.
func insertJPEGMetadata(encoded []byte, metadata promptImageMetadata) []byte {
	if len(encoded) < 2 || encoded[0] != 0xff || encoded[1] != 0xd8 {
		return encoded
	}
	var out bytes.Buffer
	out.Write(encoded[:2])
	if len(metadata.exif) > 0 {
		out.Write(encodeJPEGSegment(0xe1, append([]byte("Exif\x00\x00"), metadata.exif...)))
	}
	if len(metadata.iccProfile) > 0 {
		const maxChunk = 65519
		chunks := (len(metadata.iccProfile) + maxChunk - 1) / maxChunk
		if chunks == 0 {
			chunks = 1
		}
		for index := 0; index < chunks; index++ {
			start := index * maxChunk
			end := start + maxChunk
			if end > len(metadata.iccProfile) {
				end = len(metadata.iccProfile)
			}
			payload := []byte("ICC_PROFILE\x00")
			payload = append(payload, byte(index+1), byte(chunks))
			payload = append(payload, metadata.iccProfile[start:end]...)
			out.Write(encodeJPEGSegment(0xe2, payload))
		}
	}
	out.Write(encoded[2:])
	return out.Bytes()
}

func encodeJPEGSegment(marker byte, payload []byte) []byte {
	var buf bytes.Buffer
	buf.Write([]byte{0xff, marker})
	var length [2]byte
	binary.BigEndian.PutUint16(length[:], uint16(len(payload)+2))
	buf.Write(length[:])
	buf.Write(payload)
	return buf.Bytes()
}
