package tea

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	codextui "codex_go/tui"
	"codex_go/tui/pets"
	"codex_go/tui/transcript_image"
)

const transcriptImageCacheEntries = 24

// tmuxTranscriptImageSlots is the pane's transcript image share of the 256-id
// window; the last two ids belong to the ambient pet and picker preview.
const tmuxTranscriptImageSlots = 254

// transcriptImagePlan reserves rows below one rendered message for a local
// image preview (Rust transcript_image::ImagePlacement before encoding).
type transcriptImagePlan struct {
	image      *transcript_image.TranscriptImage
	contentRow int
	columns    int
	rows       int
}

type transcriptImageCacheKey struct {
	path       string
	width      int
	protocol   pets.ImageProtocol
	cellWidth  uint32
	cellHeight uint32
}

// transcriptImageCache bounds decoded thumbnails by entry count. Each entry is
// already capped by transcript_image.Load's raster and encoded-size limits.
type transcriptImageCache struct {
	entries map[transcriptImageCacheKey]transcriptImageCacheEntry
	order   []transcriptImageCacheKey
}

type transcriptImageCacheEntry struct {
	image   *transcript_image.TranscriptImage
	size    int64
	modTime time.Time
}

// transcriptImageFileState fingerprints one local preview file. The layout
// cache is keyed by message revision, but deleting or replacing an image does
// not mutate the message, so this small file list lets an idle frame notice
// that its reserved image rows are stale without rescanning all messages.
type transcriptImageFileState struct {
	path    string
	size    int64
	modTime time.Time
	exists  bool
}

func readTranscriptImageFileState(path string) transcriptImageFileState {
	state := transcriptImageFileState{path: path}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return state
	}
	state.size = info.Size()
	state.modTime = info.ModTime()
	state.exists = true
	return state
}

func transcriptImageFilesForMessages(messages []codextui.Message) []transcriptImageFileState {
	var files []transcriptImageFileState
	seen := map[string]bool{}
	for _, message := range messages {
		path := strings.TrimSpace(message.ImagePath)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		files = append(files, readTranscriptImageFileState(path))
	}
	return files
}

func (c *transcriptMessageCache) transcriptImageFilesCurrent() bool {
	if c == nil {
		return false
	}
	for _, cached := range c.imageFiles {
		current := readTranscriptImageFileState(cached.path)
		if current.exists != cached.exists || current.size != cached.size || !current.modTime.Equal(cached.modTime) {
			return false
		}
	}
	return true
}

func (c *transcriptImageCache) load(path string, width int, protocol pets.ImageProtocol, cell transcript_image.CellSize) *transcript_image.TranscriptImage {
	if c == nil || strings.TrimSpace(path) == "" || width <= 0 || protocol == "" {
		return nil
	}
	key := transcriptImageCacheKey{
		path:       path,
		width:      width,
		protocol:   protocol,
		cellWidth:  cell.Width,
		cellHeight: cell.Height,
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		delete(c.entries, key)
		return nil
	}
	if entry, ok := c.entries[key]; ok && entry.size == info.Size() && entry.modTime.Equal(info.ModTime()) {
		return entry.image
	}
	imageData, err := transcript_image.Load(path, width, protocol, cell)
	if err != nil || imageData == nil {
		delete(c.entries, key)
		return nil
	}
	if c.entries == nil {
		c.entries = map[transcriptImageCacheKey]transcriptImageCacheEntry{}
	}
	c.entries[key] = transcriptImageCacheEntry{image: imageData, size: info.Size(), modTime: info.ModTime()}
	c.order = append(c.order, key)
	for len(c.order) > transcriptImageCacheEntries {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, oldest)
	}
	return imageData
}

type transcriptImageDrawKey struct {
	image    *transcript_image.TranscriptImage
	x        int
	y        int
	columns  int
	rows     int
	firstRow int
}

type drawnTranscriptImage struct {
	id      uint32
	key     transcriptImageDrawKey
	encoded *transcript_image.EncodedImage
}

// transcriptImageOverlay is an absolutely positioned graphic, used by the
// composer thumbnail strip (Rust #58261). row is a zero-based frame row.
type transcriptImageOverlay struct {
	image *transcript_image.TranscriptImage
	x     int
	row   int
}

// transcriptImageRuntime owns terminal graphics state for the owned fullscreen
// transcript. The Bubble Tea output wrapper calls beforeWrite before each text
// frame and afterWrite afterwards, so displaced graphics never erase new text.
type transcriptImageRuntime struct {
	mu     sync.Mutex
	out    io.Writer
	env    map[string]string
	plans  []transcriptImagePlan
	top    int
	offset int
	height int
	width  int
	// terminalHeight anchors the frame at the bottom of an inline terminal,
	// where Bubble Tea renders the last View row on the last console row.
	terminalHeight int
	surface        bool
	drawn          []drawnTranscriptImage
	pending        []drawnTranscriptImage
	overlays       []transcriptImageOverlay
	// tmuxBase is the pane's Kitty image-id base while fullscreen tmux
	// passthrough is available. Placements then become virtual so tmux moves
	// them with the pane (Rust #59579).
	tmuxBase *uint32
	// repaintRequested asks the owner to rewrite every text row after a
	// placeholder cell was released; tmux keeps placeholder text otherwise.
	repaintRequested bool
}

func newTranscriptImageRuntime(out io.Writer, env map[string]string) *transcriptImageRuntime {
	return &transcriptImageRuntime{out: out, env: env}
}

// setTmuxBase records the pane-specific Kitty image-id base, or clears it when
// images are cursor-placed (Rust custom_terminal::tmux_kitty_image_base).
func (r *transcriptImageRuntime) setTmuxBase(base *uint32) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tmuxBase = nil
	if base != nil {
		value := *base
		r.tmuxBase = &value
	}
}

// takeRepaintRequest consumes the pending full-frame repaint request.
func (r *transcriptImageRuntime) takeRepaintRequest() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	requested := r.repaintRequested
	r.repaintRequested = false
	return requested
}

// imageIDForIndex returns the image id for one placement, offset into the
// pane's window when tmux passthrough owns the ids.
func (r *transcriptImageRuntime) imageIDForIndex(index int) uint32 {
	if r.tmuxBase != nil {
		return *r.tmuxBase + uint32(index)
	}
	return transcript_image.FirstImageID + uint32(index)
}

func (r *transcriptImageRuntime) setOutput(out io.Writer) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.out = out
}

func (r *transcriptImageRuntime) setFrame(plans []transcriptImagePlan, top int, offset int, height int, width int, terminalHeight int, surface bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.plans = append([]transcriptImagePlan(nil), plans...)
	r.top = top
	r.offset = offset
	r.height = height
	r.width = width
	r.terminalHeight = terminalHeight
	r.surface = surface
}

// setOverlays publishes absolutely positioned graphics such as composer
// thumbnails. It replaces any previous composer strip.
func (r *transcriptImageRuntime) setOverlays(overlays []transcriptImageOverlay) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.overlays = append([]transcriptImageOverlay(nil), overlays...)
}

func (r *transcriptImageRuntime) hide() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.plans = nil
	r.overlays = nil
	r.surface = false
}

// releaseFiles removes retained local-file transports when the program exits.
// The terminal keeps the already-transmitted pixels, so no delete sequence is
// needed here.
func (r *transcriptImageRuntime) releaseFiles() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, drawn := range r.drawn {
		if drawn.encoded != nil {
			_ = drawn.encoded.Close()
		}
	}
	for _, drawn := range r.pending {
		if drawn.encoded != nil {
			_ = drawn.encoded.Close()
		}
	}
	r.drawn = nil
	r.pending = nil
}

// beforeWrite removes graphics whose placement changed or disappeared. Running
// before the frame text lets the frame repaint any area the old graphic used.
func (r *transcriptImageRuntime) beforeWrite() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.out == nil {
		return
	}
	targets := r.targetsLocked()
	if r.tmuxBase != nil && len(targets) > tmuxTranscriptImageSlots {
		targets = targets[:tmuxTranscriptImageSlots]
	}
	oldByID := make(map[uint32]drawnTranscriptImage, len(r.drawn))
	for _, drawn := range r.drawn {
		oldByID[drawn.id] = drawn
	}
	changed := len(targets) != len(r.drawn)
	next := make([]drawnTranscriptImage, 0, len(targets))
	for index, target := range targets {
		id := r.imageIDForIndex(index)
		if old, ok := oldByID[id]; ok {
			delete(oldByID, id)
			if old.key == target.key {
				next = append(next, old)
				continue
			}
			r.clearLocked(old)
			changed = true
		}
		next = append(next, drawnTranscriptImage{id: id, key: target.key})
	}
	for _, old := range oldByID {
		r.clearLocked(old)
		changed = true
	}
	if r.tmuxBase != nil && changed && len(r.drawn) > 0 {
		// Rust invalidates the viewport once a tmux placement is replaced or
		// removed: Ghostty drops the pixels but tmux keeps the placeholder
		// cells, so the text underneath has to be written again.
		r.repaintRequested = true
	}
	r.pending = next
}

// afterWrite paints the current placements over the freshly written frame.
func (r *transcriptImageRuntime) afterWrite() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.out == nil {
		return
	}
	drawn := r.pending
	r.pending = nil
	for index := range drawn {
		entry := &drawn[index]
		if entry.encoded == nil {
			placement := pets.CursorKittyPlacement(entry.id)
			if r.tmuxBase != nil {
				placement = pets.VirtualKittyPlacement(entry.id)
			}
			encoded, err := entry.key.image.Encode(
				entry.key.x,
				entry.key.y,
				entry.key.firstRow,
				entry.key.rows,
				placement,
				r.env,
			)
			if err != nil {
				continue
			}
			entry.encoded = encoded
		}
		// Graphics are best-effort; terminal write failures leave the text
		// entry intact and are retried on the next frame.
		_ = entry.encoded.Write(r.out, entry.key.x, entry.key.y)
	}
	r.drawn = drawn
}

func (r *transcriptImageRuntime) targetsLocked() []drawnTranscriptImage {
	if !r.surface || r.height <= 0 || r.width <= 0 || len(r.plans) == 0 {
		return nil
	}
	targets := make([]drawnTranscriptImage, 0, len(r.plans))
	for _, plan := range r.plans {
		if plan.image == nil || plan.rows <= 0 || plan.columns <= 0 {
			continue
		}
		placement := transcript_image.Placement{
			Image:    plan.image,
			FirstRow: plan.contentRow,
			Height:   plan.rows,
		}
		firstRow, rows, ok := placement.VisibleRows(r.offset, r.height)
		if !ok {
			continue
		}
		visibleStart := max(plan.contentRow, r.offset)
		x := max((r.width-plan.columns)/2, 0)
		y := r.top + visibleStart - r.offset
		if y < 0 {
			hidden := min(-y, rows)
			firstRow += hidden
			rows -= hidden
			y = 0
		}
		if r.terminalHeight > 0 && y+rows > r.terminalHeight {
			rows = r.terminalHeight - y
		}
		if rows <= 0 {
			continue
		}
		targets = append(targets, drawnTranscriptImage{
			key: transcriptImageDrawKey{
				image:    plan.image,
				x:        x,
				y:        y,
				columns:  plan.columns,
				rows:     rows,
				firstRow: firstRow,
			},
		})
	}
	for _, overlay := range r.overlays {
		if overlay.image == nil || overlay.row < 0 {
			continue
		}
		rows := int(overlay.image.Rows)
		if r.terminalHeight > 0 && overlay.row+rows > r.terminalHeight {
			rows = r.terminalHeight - overlay.row
		}
		if rows <= 0 || int(overlay.image.Columns) <= 0 {
			continue
		}
		targets = append(targets, drawnTranscriptImage{
			key: transcriptImageDrawKey{
				image:    overlay.image,
				x:        max(overlay.x, 0),
				y:        overlay.row,
				columns:  int(overlay.image.Columns),
				rows:     rows,
				firstRow: 0,
			},
		})
	}
	return targets
}

func (r *transcriptImageRuntime) clearLocked(drawn drawnTranscriptImage) {
	if r.out == nil {
		return
	}
	switch {
	case drawn.key.image != nil && (drawn.key.image.Protocol == pets.ImageProtocolKitty || drawn.key.image.Protocol == pets.ImageProtocolKittyLocalFile):
		_, _ = io.WriteString(r.out, pets.KittyDeleteImage(drawn.id, r.env))
	case drawn.key.image != nil && drawn.key.image.Protocol == pets.ImageProtocolSixel:
		var builder strings.Builder
		builder.WriteString("\x1b7")
		blank := strings.Repeat(" ", max(drawn.key.columns, 1))
		for row := 0; row < drawn.key.rows; row++ {
			fmt.Fprintf(&builder, "\x1b[%d;%dH", max(drawn.key.y, 0)+row+1, max(drawn.key.x, 0)+1)
			builder.WriteString(blank)
		}
		builder.WriteString("\x1b8")
		_, _ = io.WriteString(r.out, builder.String())
	}
	if drawn.encoded != nil {
		_ = drawn.encoded.Close()
	}
}

// Model helpers -------------------------------------------------------------

func (m *Model) transcriptImageProtocol() pets.ImageProtocol {
	if m == nil || m.rawOutput || !m.localSession {
		return ""
	}
	support := m.petImageSupport()
	if !support.Supported() {
		return ""
	}
	switch support.Protocol {
	case pets.ImageProtocolKitty, pets.ImageProtocolKittyLocalFile:
		return support.Protocol
	case pets.ImageProtocolSixel:
		if m.transcriptImageCell.Width == 0 || m.transcriptImageCell.Height == 0 {
			return ""
		}
		return support.Protocol
	default:
		return ""
	}
}

func (m *Model) transcriptImagePreview(message codextui.Message, width int, expanded bool) *transcript_image.TranscriptImage {
	if m == nil || width <= 0 || strings.TrimSpace(message.ImagePath) == "" {
		return nil
	}
	protocol := m.transcriptImageProtocol()
	if protocol == "" {
		return nil
	}
	if len(normalizeActivityIDs(message.ActivityIDs)) > 0 && !expanded {
		return nil
	}
	imageData := m.transcriptImageCache.load(message.ImagePath, width, protocol, m.transcriptImageCell)
	if imageData == nil || int(imageData.Columns)+2 > width {
		return nil
	}
	return imageData
}

func (m *Model) transcriptImageResolver(width int, revealViewed bool) func(codextui.Message, bool) *transcript_image.TranscriptImage {
	if m == nil || m.transcriptImageProtocol() == "" {
		return nil
	}
	return func(message codextui.Message, expanded bool) *transcript_image.TranscriptImage {
		return m.transcriptImagePreview(message, width, expanded && revealViewed)
	}
}

func (m *Model) transcriptImagesSurfaceActive() bool {
	if m == nil || m.overlay != nil || m.agentsOverview != nil {
		return false
	}
	// Bottom-pane modals keep the transcript visible; only the full-screen
	// session picker replaces it.
	if m.modal != nil && m.modal.sessionPicker != nil {
		return false
	}
	return m.transcriptImageProtocol() != ""
}

// publishTranscriptImages hands the current layout to the output runtime. View
// calls this after every transcript refresh and hides the surface for overlays.
func (m *Model) publishTranscriptImages() {
	if m == nil || m.transcriptImages == nil {
		return
	}
	if !m.transcriptImagesSurfaceActive() {
		m.transcriptImages.hide()
		return
	}
	frameRows := m.transcriptImageFrameRows
	if frameRows <= 0 {
		frameRows = m.height
	}
	top := 1
	if m.regionChromeEnabled() {
		top = 2
	}
	// Bubble Tea's inline renderer ends the frame on the current console row,
	// so the frame's first row is height-frameRows rather than row zero.
	top += m.height - frameRows
	m.transcriptImages.setTmuxBase(m.tmuxImageBase())
	m.transcriptImages.setFrame(
		m.transcriptImagePlans,
		top,
		m.transcript.YOffset,
		m.transcript.Height,
		m.transcript.Width,
		m.height,
		true,
	)
	// Rust #58261: the composer thumbnail strip is part of the same frame, so
	// it is published and cleared with the transcript graphics.
	m.composerImageOverlays = m.publishComposerImageOverlays()
	m.transcriptImages.setOverlays(m.composerImageOverlays)
}

func (m *Model) hideTranscriptImages() {
	if m == nil || m.transcriptImages == nil {
		return
	}
	m.transcriptImages.hide()
}
