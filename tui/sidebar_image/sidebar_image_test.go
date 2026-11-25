package sidebar_image

import (
	"bytes"
	"codex_go/tui/pets"
	"codex_go/tui/transcript_image"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

func TestSidebarImageDecodeAndFitLikeRust(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 320, 160))
	for _, format := range []string{"png", "jpeg", "gif"} {
		t.Run(format, func(t *testing.T) {
			var b bytes.Buffer
			switch format {
			case "png":
				_ = png.Encode(&b, src)
			case "jpeg":
				_ = jpeg.Encode(&b, src, nil)
			case "gif":
				_ = gif.Encode(&b, src, nil)
			}
			i, err := Decode(b.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			if i.Width != 320 || i.Height != 160 {
				t.Fatalf("dimensions: %dx%d", i.Width, i.Height)
			}
			for _, protocol := range []pets.ImageProtocol{pets.ImageProtocolKitty, pets.ImageProtocolKittyLocalFile, pets.ImageProtocolSixel} {
				cell := transcript_image.CellSize{Width: 8, Height: 16}
				f := i.Fit(12, 3, protocol, cell)
				if f == nil || f.Columns > 12 || f.Rows > 3 {
					t.Fatalf("fit %s: %#v", protocol, f)
				}
				if f != i.Fit(12, 3, protocol, cell) {
					t.Fatal("same fit was decoded/scaled again")
				}
				enc, err := f.Encode(0, 0, 0, int(f.Rows), pets.CursorKittyPlacement(123), nil)
				if err != nil {
					t.Fatal(err)
				}
				if len(enc.Payload) == 0 {
					t.Fatal("empty graphics payload")
				}
				_ = enc.Close()
			}
			if i.Fit(12, 3, "", transcript_image.CellSize{}) != nil {
				t.Fatal("unsupported terminal got graphics")
			}
			if i.Fit(12, 3, pets.ImageProtocolSixel, transcript_image.CellSize{}) != nil {
				t.Fatal("Sixel accepted unmeasured cells")
			}
			if i.Fit(0, 3, pets.ImageProtocolKitty, transcript_image.CellSize{}) != nil {
				t.Fatal("empty body got graphics")
			}
		})
	}
}
func TestSidebarImageRejectsOversizedRasterBeforeDecodeLikeRust(t *testing.T) {
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	header := make([]byte, 13)
	binary.BigEndian.PutUint32(header, 100000)
	binary.BigEndian.PutUint32(header[4:], 100000)
	header[8] = 8
	header[9] = 6
	_ = binary.Write(&b, binary.BigEndian, uint32(len(header)))
	b.WriteString("IHDR")
	b.Write(header)
	_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(append([]byte("IHDR"), header...)))
	if _, err := Decode(b.Bytes()); err == nil {
		t.Fatal("oversized IHDR accepted")
	}
	if _, err := Decode([]byte("not an image")); err == nil {
		t.Fatal("malformed image accepted")
	}
	for _, path := range []string{"photo.PNG", "photo.jpeg", "photo.jpg", "photo.gif", "photo.webp"} {
		if !SupportsPath(path) {
			t.Fatalf("unsupported: %s", path)
		}
	}
	for _, path := range []string{"photo.svg", "photo.bmp", "README.md"} {
		if SupportsPath(path) {
			t.Fatalf("unsupported extension accepted: %s", path)
		}
	}
}

// WebP fixture: golang.org/x/image v0.35.0 testdata/gopher-doc.1bpp.lossless.webp.
// Copyright The Go Authors; BSD license in testdata/LICENSE.
func TestSidebarImageWebPDecodeLikeRust(t *testing.T) {
	data, err := base64.StdEncoding.DecodeString("UklGRrIBAABXRUJQVlA4TKUBAAAvSsAYAA8w//M///MfeJAkbXvaSG7m8Q3GfYSBJekwQztm/IcZlgwnmWImn2BK7aFmBtnVir6q//8VOkFE/xm4baTIu8c48ArEo6+B3zFKYln3pqClSCKX0begFTAXFOLXHSyF8cCNcZEG4OywuA4KVVfJCiArU7GAgJI8+lJP/OKMT/fBAjevg1cYB7YVkFuWga2lyPi5I0HFy5YTpWIHg0RZpkniRVW9odHAKOwosWuOGdxIyn2OvaCDvhg/we6TwadPBPbqBV58MsLmMJ8yZnOWk8SRz4N+QoyPL+MnamzMvcE1rHNEr91F9GKZPVUcS9w7PhhH36suB9qPeYb/oLk6cuTiJ0wOK3m5h1cKjW6EVZCYMK7dxcKCBdgP9HkKr9gkAO2P8GKZGWVdIAatQa+1IDpt6qyorVwdy01xdW8Jkfk6xjEXmVQQ+HQdFr6OKhIN34dXWq0+0qr6EJSCeeVLH9+gvGTLyqM65PQ44ihzlTXxQKjKbAvshXgir7Lil9w4L2bvMycmjQcqXaMCO6BlY28i+FOLzbfI1vEqxAhotocAAA==")
	if err != nil {
		t.Fatal(err)
	}
	preview, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Width <= 0 || preview.Height <= 0 || !strings.HasPrefix(preview.Label, "WEBP") {
		t.Fatalf("WebP preview: %#v", preview)
	}
}
