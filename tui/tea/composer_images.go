package tea

import (
	"os"
	"sort"
	"strings"
	"time"

	"codex_go/tui/bottom_pane"
	"codex_go/tui/pets"
	"codex_go/tui/transcript_image"
)

// Rust parity: codex-rs/tui/src/transcript_image/composer.rs (#58261). The
// composer drafts its local image attachments as a strip of thumbnails above
// the textarea, in textarea reference order, with a +N badge for the ones that
// do not fit.

// composerThumbnailBackground is the fallback dark terminal background Rust
// uses when the palette does not report one (terminal_palette::default_bg).
var composerThumbnailBackground = transcript_image.RGB{R: 48, G: 48, B: 48}

type composerThumbnail struct {
	placeholder string
	path        string
	image       *transcript_image.TranscriptImage
}

type composerTileCacheEntry struct {
	image   *transcript_image.TranscriptImage
	size    int64
	modTime time.Time
}

// composerThumbnailAttachments lists the draft's local image attachments in
// label order across local and remote images, then reorders them to the
// textarea's placeholder order like Rust's text_element_ranges sort.
func (m *Model) composerThumbnailAttachments() []composerThumbnail {
	if m == nil || len(m.attachments) == 0 {
		return nil
	}
	remote := 0
	local := 0
	var attachments []composerThumbnail
	for _, attachment := range m.attachments {
		switch attachment.Kind {
		case bottompane.AttachmentRemoteImage:
			remote++
		case bottompane.AttachmentImage:
			if strings.TrimSpace(attachment.Path) == "" {
				continue
			}
			local++
			attachments = append(attachments, composerThumbnail{
				placeholder: localImageLabelText(remote + local),
				path:        attachment.Path,
			})
		}
	}
	if len(attachments) < 2 {
		return attachments
	}
	positions := make(map[string]int, len(m.composerElements))
	for _, element := range m.composerElements {
		if _, ok := positions[element.Placeholder]; ok {
			continue
		}
		positions[element.Placeholder] = element.Start
	}
	const unreferenced = int(^uint(0) >> 1)
	sort.SliceStable(attachments, func(i int, j int) bool {
		left, ok := positions[attachments[i].placeholder]
		if !ok {
			left = unreferenced
		}
		right, ok := positions[attachments[j].placeholder]
		if !ok {
			right = unreferenced
		}
		return left < right
	})
	return attachments
}

// composerThumbnailTiles returns the visible tiles and the hidden count for the
// current width. It is empty unless the terminal graphics surface is active.
func (m *Model) composerThumbnailTiles(width int) ([]composerThumbnail, int) {
	if m == nil || width <= 0 || !m.transcriptImagesSurfaceActive() {
		return nil, 0
	}
	attachments := m.composerThumbnailAttachments()
	if len(attachments) == 0 {
		return nil, 0
	}
	protocol := m.transcriptImageProtocol()
	if protocol == "" {
		return nil, 0
	}
	visible, hidden := transcript_image.ComposerVisibleThumbnails(width, len(attachments))
	tiles := attachments[:visible]
	for index := range tiles {
		tiles[index].image = m.composerThumbnailTile(tiles[index].path, protocol)
	}
	return tiles, hidden
}

func (m *Model) composerThumbnailTile(path string, protocol pets.ImageProtocol) *transcript_image.TranscriptImage {
	if m == nil || strings.TrimSpace(path) == "" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		delete(m.composerImageCache, path)
		return nil
	}
	if entry, ok := m.composerImageCache[path]; ok && entry.size == info.Size() && entry.modTime.Equal(info.ModTime()) {
		return entry.image
	}
	imageData, err := transcript_image.LoadComposerTile(path, transcript_image.ComposerTileOptions{
		Protocol:   protocol,
		Cell:       transcript_image.CellSize{Width: m.transcriptImageCell.Width, Height: m.transcriptImageCell.Height},
		Background: composerThumbnailBackground,
	})
	if err != nil || imageData == nil {
		delete(m.composerImageCache, path)
		return nil
	}
	if m.composerImageCache == nil {
		m.composerImageCache = map[string]composerTileCacheEntry{}
	}
	m.composerImageCache[path] = composerTileCacheEntry{image: imageData, size: info.Size(), modTime: info.ModTime()}
	return imageData
}

// composerThumbnailStrip renders the text half of the strip: placeholder cells
// at each tile position (the raster is painted over them), and the +N badge.
// The returned string ends with a newline so it can prefix the draft.
func (m *Model) composerThumbnailStrip(width int) (string, []composerThumbnail) {
	tiles, hidden := m.composerThumbnailTiles(width)
	height := transcript_image.ComposerStripHeight(width, len(tiles)+hidden)
	if height == 0 {
		return "", nil
	}
	cellWidth := int(transcript_image.ComposerTileColumns)
	if len(tiles) == 0 {
		lines := []string{strings.Repeat(" ", width)}
		return strings.Join(lines, "\n") + "\n", nil
	}
	// Rust renders the placeholder with a wrapping Paragraph inside each 6x3
	// tile, so a label longer than the tile continues on the next row. Those
	// cells stay editable and are painted over by the raster when it loads.
	wrapped := make([][][]rune, len(tiles))
	for index, tile := range tiles {
		remaining := []rune(tile.placeholder)
		for row := 0; row < int(transcript_image.ComposerTileRows) && len(remaining) > 0; row++ {
			count := min(cellWidth, len(remaining))
			wrapped[index] = append(wrapped[index], remaining[:count])
			remaining = remaining[count:]
		}
	}
	var builder strings.Builder
	for row := 0; row < int(transcript_image.ComposerTileRows); row++ {
		line := make([]rune, 0, width)
		for index := range tiles {
			cell := make([]rune, 0, cellWidth)
			if row < len(wrapped[index]) {
				cell = append(cell, wrapped[index][row]...)
			}
			line = append(line, cell...)
			line = append(line, []rune(strings.Repeat(" ", cellWidth-len(cell)))...)
			line = append(line, ' ')
		}
		if row == 0 && hidden > 0 {
			line = append(line, []rune("+"+itoa(hidden))...)
		}
		builder.WriteString(fitTerminalLine(string(line), width))
		builder.WriteString("\n")
	}
	// One blank row separates a visible strip from the draft.
	builder.WriteString(fitTerminalLine("", width))
	builder.WriteString("\n")
	return builder.String(), tiles
}

// publishComposerImageOverlays places the loaded tiles over the strip rows the
// last View reserved.
func (m *Model) publishComposerImageOverlays() []transcriptImageOverlay {
	if m == nil || len(m.composerImageTiles) == 0 {
		return nil
	}
	baseRow := m.composerTopRow + 1
	overlays := make([]transcriptImageOverlay, 0, len(m.composerImageTiles))
	for index, tile := range m.composerImageTiles {
		if tile.image == nil {
			continue
		}
		overlays = append(overlays, transcriptImageOverlay{
			image: tile.image,
			x:     index * (int(transcript_image.ComposerTileColumns) + 1),
			row:   baseRow,
		})
	}
	return overlays
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits []byte
	for value > 0 {
		digits = append(digits, byte('0'+value%10))
		value /= 10
	}
	for i, j := 0, len(digits)-1; i < j; i, j = i+1, j-1 {
		digits[i], digits[j] = digits[j], digits[i]
	}
	return string(digits)
}
