// Package transcript_image owns bounded image previews for the fullscreen
// transcript. It mirrors the layout half of Rust #54216; terminal graphics are
// drawn by the caller.
package transcript_image

import (
	"errors"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"

	xdraw "golang.org/x/image/draw"

	"codex_go/tui/pets"
)

const (
	// FirstImageID is separate from the two IDs reserved for pets.
	FirstImageID uint32 = 0xC100
	// MaxEncodedBytes bounds the file before decoding.
	MaxEncodedBytes int64 = 32 << 20
	// MaxAllocBytes bounds the decoded raster allocation, mirroring Rust's
	// image::Limits max_alloc (32 MiB).
	MaxAllocBytes int64 = 32 << 20
	// MaxPixels is the RGBA-safe pixel count within MaxAllocBytes.
	MaxPixels = MaxAllocBytes / 4
)

var (
	ErrUnsupported = errors.New("transcript image preview is unsupported")
	ErrTooLarge    = errors.New("transcript image is too large")
)

// CellSize is the terminal cell dimensions in pixels.
type CellSize struct {
	Width  uint32
	Height uint32
}

// TranscriptImage is a bounded thumbnail reserved in cell coordinates.
type TranscriptImage struct {
	Protocol   pets.ImageProtocol
	Columns    uint16
	Rows       uint16
	Pixels     *image.RGBA
	CellWidth  uint32
	CellHeight uint32
}

// Load decodes path into a bounded thumbnail. width is the transcript body
// width; cell is required for Sixel and ignored for Kitty protocols.
func Load(path string, width int, protocol pets.ImageProtocol, cell CellSize) (*TranscriptImage, error) {
	columns := min(max(width-2, 0), 48)
	if columns == 0 {
		return nil, fmt.Errorf("%w: no preview columns", ErrUnsupported)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: not a regular file", ErrUnsupported)
	}
	if info.Size() > MaxEncodedBytes {
		return nil, fmt.Errorf("%w: encoded size %d", ErrTooLarge, info.Size())
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	config, _, err := image.DecodeConfig(file)
	if err != nil {
		return nil, err
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > MaxPixels {
		return nil, fmt.Errorf("%w: %dx%d", ErrTooLarge, config.Width, config.Height)
	}
	if _, err := file.Seek(0, 0); err != nil {
		return nil, err
	}
	source, _, err := image.Decode(file)
	if err != nil {
		return nil, err
	}
	if bounds := source.Bounds(); int64(bounds.Dx())*int64(bounds.Dy()) > MaxPixels {
		return nil, fmt.Errorf("%w: decoded %dx%d", ErrTooLarge, bounds.Dx(), bounds.Dy())
	}
	cellWidth, cellHeight := uint32(8), uint32(16)
	maxWidth, maxHeight := uint32(384), uint32(160)
	if protocol == pets.ImageProtocolSixel {
		if cell.Width == 0 || cell.Height == 0 {
			return nil, fmt.Errorf("%w: Sixel needs a terminal cell size", ErrUnsupported)
		}
		cellWidth, cellHeight = cell.Width, cell.Height
		maxWidth, maxHeight = 160, 80
	}
	bounds := source.Bounds()
	sourceWidth, sourceHeight := uint32(bounds.Dx()), uint32(bounds.Dy())
	targetWidth := min(uint32(columns)*cellWidth, maxWidth, sourceWidth)
	targetHeight := min(6*cellHeight, maxHeight, sourceHeight)
	scale := min(
		float64(targetWidth)/float64(sourceWidth),
		float64(targetHeight)/float64(sourceHeight),
	)
	if scale > 1 {
		scale = 1
	}
	dstWidth := max(int(float64(sourceWidth)*scale), 1)
	dstHeight := max(int(float64(sourceHeight)*scale), 1)
	pixels := image.NewRGBA(image.Rect(0, 0, dstWidth, dstHeight))
	xdraw.CatmullRom.Scale(pixels, pixels.Bounds(), source, bounds, draw.Src, nil)
	return &TranscriptImage{
		Protocol:   protocol,
		Columns:    uint16((dstWidth + int(cellWidth) - 1) / int(cellWidth)),
		Rows:       uint16((dstHeight + int(cellHeight) - 1) / int(cellHeight)),
		Pixels:     pixels,
		CellWidth:  cellWidth,
		CellHeight: cellHeight,
	}, nil
}

// Placement reserves rows in the transcript viewport.
type Placement struct {
	Image    *TranscriptImage
	FirstRow int
	Height   int
	X        int
	Y        int
}

// VisibleRows clips the placement to the current viewport.
func (p Placement) VisibleRows(viewportTop int, viewportHeight int) (firstRow int, rows int, ok bool) {
	if p.Image == nil || viewportHeight <= 0 || p.Height <= 0 {
		return 0, 0, false
	}
	start := max(p.FirstRow, viewportTop)
	end := min(p.FirstRow+p.Height, viewportTop+viewportHeight)
	if end <= start {
		return 0, 0, false
	}
	return start - p.FirstRow, end - start, true
}

// ClipSourceRows maps visible cell rows to source pixel rows.
func (p Placement) ClipSourceRows(firstVisibleRow int, visibleRows int) (top int, bottom int, ok bool) {
	if p.Image == nil || p.Image.Pixels == nil || p.Height <= 0 || visibleRows <= 0 {
		return 0, 0, false
	}
	height := p.Image.Pixels.Bounds().Dy()
	if p.Image.Protocol == pets.ImageProtocolSixel {
		top = firstVisibleRow * int(p.Image.CellHeight)
		bottom = (firstVisibleRow + visibleRows) * int(p.Image.CellHeight)
	} else {
		top = firstVisibleRow * height / p.Height
		bottom = (firstVisibleRow + visibleRows) * height / p.Height
	}
	top = min(max(top, 0), height)
	bottom = min(max(bottom, top), height)
	if bottom <= top {
		return 0, 0, false
	}
	return top, bottom, true
}
