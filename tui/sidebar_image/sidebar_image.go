// Package sidebar_image decodes authorized preview bytes once and fits them
// into a sidebar using the transcript's terminal graphics lifecycle (#61919).
package sidebar_image

import (
	"bytes"
	"fmt"
	"image"
	"math"
	"path/filepath"
	"strings"

	"codex_go/tui/imagedecode"
	"codex_go/tui/pets"
	"codex_go/tui/transcript_image"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const MaxBytes = 32 << 20

type Image struct {
	Label         string
	Width, Height int
	raster        image.Image
	fitted        *transcript_image.TranscriptImage
}

func SupportsPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	}
	return false
}

// Decode checks the format and allocation budget before allocating the raster.
// GIF previews use the first frame, like the static Rust sidebar preview.
func Decode(data []byte) (*Image, error) {
	if len(data) > MaxBytes {
		return nil, fmt.Errorf("image exceeds 32 MiB preview limit")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	switch format {
	case "png", "jpeg", "gif", "webp":
	default:
		return nil, fmt.Errorf("unsupported image format: %s", format)
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > MaxBytes/4 {
		return nil, imagedecode.ErrTooLarge
	}
	decoded, err := imagedecode.DecodeBytes(data, MaxBytes)
	if err != nil {
		return nil, err
	}
	w, h := decoded.Bounds().Dx(), decoded.Bounds().Dy()
	raster := decoded
	if w > 768 || h > 768 {
		scale := math.Min(768/float64(w), 768/float64(h))
		dst := image.NewRGBA(image.Rect(0, 0, max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale))))
		xdraw.CatmullRom.Scale(dst, dst.Bounds(), decoded, decoded.Bounds(), xdraw.Over, nil)
		raster = dst
	}
	return &Image{Label: fmt.Sprintf("%s · %d × %d", strings.ToUpper(format), w, h), Width: w, Height: h, raster: raster}, nil
}

// Fit preserves aspect ratio without upscaling and caches the current geometry.
// Sixel needs measured cells; Kitty can fall back to 8×16 pixels.
func (i *Image) Fit(columns, rows int, protocol pets.ImageProtocol, cell transcript_image.CellSize) *transcript_image.TranscriptImage {
	if i == nil || columns <= 0 || rows <= 0 {
		return nil
	}
	cw, ch := cell.Width, cell.Height
	switch protocol {
	case pets.ImageProtocolKitty, pets.ImageProtocolKittyLocalFile:
		if cw == 0 || ch == 0 {
			cw, ch = 8, 16
		}
	case pets.ImageProtocolSixel:
		if cw == 0 || ch == 0 {
			return nil
		}
	default:
		return nil
	}
	maxW, maxH := float64(columns)*float64(cw), float64(rows)*float64(ch)
	if protocol == pets.ImageProtocolSixel {
		maxW = math.Min(maxW, 160)
		maxH = math.Min(maxH, 80)
	}
	scale := math.Min(1, math.Min(maxW/float64(i.Width), maxH/float64(i.Height)))
	w, h := max(1, int(math.Floor(float64(i.Width)*scale))), max(1, int(math.Floor(float64(i.Height)*scale)))
	cols, lines := uint16((w+int(cw)-1)/int(cw)), uint16((h+int(ch)-1)/int(ch))
	if f := i.fitted; f != nil && f.Protocol == protocol && f.CellWidth == cw && f.CellHeight == ch && f.Columns == cols && f.Rows == lines {
		return f
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), i.raster, i.raster.Bounds(), xdraw.Over, nil)
	i.fitted = &transcript_image.TranscriptImage{Protocol: protocol, Columns: cols, Rows: lines, Pixels: dst, CellWidth: cw, CellHeight: ch}
	return i.fitted
}
