package transcript_image

import (
	"image"
	"image/color"
	"image/draw"
	"strconv"
	"strings"

	"codex_go/tui/pets"
)

// Rust parity: codex-rs/tui/src/transcript_image/composer.rs (#58261). Draft
// thumbnails keep only visible rasters between draws and place them in textarea
// reference order; attachment numbering and submission order stay independent.

const (
	// ComposerTileColumns is the nominal thumbnail width. Six columns by three
	// rows is square with typical 8x16 pixel cells.
	ComposerTileColumns uint16 = 6
	// ComposerTileRows is the nominal thumbnail height.
	ComposerTileRows uint16 = 3
)

// RGB is an opaque terminal background color.
type RGB struct {
	R uint8
	G uint8
	B uint8
}

// IsLight mirrors Rust color::is_light.
func (c RGB) IsLight() bool {
	return 0.299*float64(c.R)+0.587*float64(c.G)+0.114*float64(c.B) > 128.0
}

// Blend mirrors Rust color::blend.
func Blend(fg RGB, bg RGB, alpha float64) RGB {
	channel := func(f uint8, b uint8) uint8 {
		return uint8(float64(f)*alpha + float64(b)*(1.0-alpha))
	}
	return RGB{R: channel(fg.R, bg.R), G: channel(fg.G, bg.G), B: channel(fg.B, bg.B)}
}

// ComposerVisibleThumbnails reports how many thumbnails fit on one strip row and
// how many are hidden behind the +N badge.
func ComposerVisibleThumbnails(width int, count int) (visible int, hidden int) {
	if width <= 0 || count <= 0 {
		return 0, max(count, 0)
	}
	visible = min((width+1)/(int(ComposerTileColumns)+1), count)
	for visible > 0 && visible < count {
		badge := 2 + len(strconv.Itoa(count-visible))
		if visible*(int(ComposerTileColumns)+1)+badge <= width {
			break
		}
		visible--
	}
	return visible, count - visible
}

// ComposerStripHeight mirrors ComposerImages::height: a visible strip reserves
// the tile rows plus one blank row between the strip and the draft, and a
// badge-only strip reserves two rows.
func ComposerStripHeight(width int, count int) int {
	visible, hidden := ComposerVisibleThumbnails(width, count)
	if visible > 0 {
		return int(ComposerTileRows) + 1
	}
	if hidden > 0 && width >= 2+len(strconv.Itoa(hidden)) {
		return 2
	}
	return 0
}

// ComposerAttachmentOrder sorts attachment indexes by the position of their
// placeholder in the draft (textarea reference order). Placeholders missing
// from the draft keep their relative order after every referenced one, matching
// Rust's usize::MAX fallback.
func ComposerAttachmentOrder(placeholders []string, draft string) []int {
	order := make([]int, len(placeholders))
	positions := make([]int, len(placeholders))
	for index, placeholder := range placeholders {
		order[index] = index
		position := -1
		if placeholder != "" {
			position = strings.Index(draft, placeholder)
		}
		if position < 0 {
			position = int(^uint(0) >> 1)
		}
		positions[index] = position
	}
	for i := 1; i < len(order); i++ {
		for j := i; j > 0 && positions[order[j]] < positions[order[j-1]]; j-- {
			order[j], order[j-1] = order[j-1], order[j]
		}
	}
	return order
}

// ComposerTileOptions carries the terminal graphics facts a tile needs.
type ComposerTileOptions struct {
	Protocol   pets.ImageProtocol
	Cell       CellSize
	Background RGB
}

// LoadComposerTile renders one draft thumbnail: a fixed 6x3 cell tile with a
// blended border and the scaled image centered on the composer background.
// Sixel cannot rescale into a nominal raster, so an oversized tile is
// unsupported (Rust ComposerImages::load_tile).
func LoadComposerTile(path string, options ComposerTileOptions) (*TranscriptImage, error) {
	cellWidth, cellHeight := options.Cell.Width, options.Cell.Height
	if cellWidth == 0 || cellHeight == 0 {
		if options.Protocol == pets.ImageProtocolSixel {
			return nil, ErrUnsupported
		}
		cellWidth, cellHeight = 8, 16
	}
	width := int(ComposerTileColumns) * int(cellWidth)
	height := int(ComposerTileRows) * int(cellHeight)
	scale := max(divideCeil(width, 384), divideCeil(height, 160))
	if scale > 1 && options.Protocol == pets.ImageProtocolSixel {
		return nil, ErrUnsupported
	}
	width = max(width/scale, 3)
	height = max(height/scale, 3)
	pixels, err := LoadPixels(path, width-2, height-2)
	if err != nil {
		return nil, err
	}
	background := options.Background
	contrast := uint8(255)
	if background.IsLight() {
		contrast = 0
	}
	edge := Blend(RGB{R: contrast, G: contrast, B: contrast}, background, 0.45)
	tile := image.NewRGBA(image.Rect(0, 0, width, height))
	fill := color.RGBA{R: background.R, G: background.G, B: background.B, A: 255}
	border := color.RGBA{R: edge.R, G: edge.G, B: edge.B, A: 255}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			if x == 0 || y == 0 || x == width-1 || y == height-1 {
				tile.SetRGBA(x, y, border)
				continue
			}
			tile.SetRGBA(x, y, fill)
		}
	}
	bounds := pixels.Bounds()
	offset := image.Pt((width-bounds.Dx())/2, (height-bounds.Dy())/2)
	draw.Draw(tile, image.Rect(offset.X, offset.Y, offset.X+bounds.Dx(), offset.Y+bounds.Dy()), pixels, bounds.Min, draw.Over)
	return &TranscriptImage{
		Protocol:   options.Protocol,
		Columns:    ComposerTileColumns,
		Rows:       ComposerTileRows,
		Pixels:     tile,
		CellWidth:  cellWidth,
		CellHeight: cellHeight,
	}, nil
}

func divideCeil(value int, divisor int) int {
	if divisor <= 0 {
		return value
	}
	return (value + divisor - 1) / divisor
}
