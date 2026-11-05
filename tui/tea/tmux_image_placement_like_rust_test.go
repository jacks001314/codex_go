package tea

import (
	"bytes"
	"image"
	"strings"
	"testing"

	"codex_go/tui/pets"
	"codex_go/tui/transcript_image"
)

// Rust #59579: inside a fullscreen tmux pane transcript previews take the
// pane's image window and switch to virtual placements.
func TestTranscriptImagesUsePaneIdsAndVirtualPlacementsLikeRust(t *testing.T) {
	path := writeTranscriptTestPNG(t, 40, 32)
	imageData, err := transcript_image.Load(path, 80, pets.ImageProtocolKitty, transcript_image.CellSize{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var output bytes.Buffer
	images := newTranscriptImageRuntime(&output, nil)
	base := uint32(0x800000 + (7 << 8))
	images.setTmuxBase(&base)
	plans := []transcriptImagePlan{{
		image:      imageData,
		contentRow: 2,
		columns:    int(imageData.Columns),
		rows:       int(imageData.Rows),
	}}
	images.setFrame(plans, 1, 0, 10, 80, 24, true)
	repaints := 0
	writer := &petOutputWriter{runtime: newPetRuntime(&output, nil), images: images}
	writer.repaint = func() { repaints++ }

	if _, err := writer.Write([]byte("frame")); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	text := output.String()
	if !strings.Contains(text, ",i="+uintToStringForTest(base)+",U=1,") {
		t.Fatalf("pane transmit missing: %q", text)
	}
	if !strings.Contains(text, "\U0010EEEE") {
		t.Fatalf("placeholder cells missing: %q", text)
	}
	if repaints != 0 {
		t.Fatalf("first paint requested %d repaints", repaints)
	}

	// Moving a placement frees its old placeholder cells, so the text
	// underneath has to be rewritten (Rust invalidate_viewport).
	output.Reset()
	images.setFrame(plans, 2, 0, 10, 80, 24, true)
	if _, err := writer.Write([]byte("frame2")); err != nil {
		t.Fatalf("write moved frame: %v", err)
	}
	if repaints != 1 {
		t.Fatalf("moved placement requested %d repaints, want 1", repaints)
	}
	// Outside tmux the same move stays cursor-placed and needs no repaint.
	output.Reset()
	images.setTmuxBase(nil)
	images.setFrame(plans, 1, 0, 10, 80, 24, true)
	if _, err := writer.Write([]byte("frame3")); err != nil {
		t.Fatalf("write cursor frame: %v", err)
	}
	if strings.Contains(output.String(), "U=1") {
		t.Fatalf("cursor placement emitted a virtual placement: %q", output.String())
	}
	if repaints != 1 {
		t.Fatalf("cursor placement requested %d repaints, want 1", repaints)
	}
}

// The last two ids of a pane window belong to the pet and the picker preview.
func TestTranscriptImagesCapPaneSlotsLikeRust(t *testing.T) {
	imageData := &transcript_image.TranscriptImage{
		Protocol:   pets.ImageProtocolKitty,
		Columns:    2,
		Rows:       1,
		Pixels:     image.NewRGBA(image.Rect(0, 0, 2, 1)),
		CellWidth:  8,
		CellHeight: 16,
	}
	plans := make([]transcriptImagePlan, 0, 300)
	for index := 0; index < 300; index++ {
		plans = append(plans, transcriptImagePlan{image: imageData, contentRow: index, columns: 2, rows: 1})
	}
	runtime := newTranscriptImageRuntime(&bytes.Buffer{}, nil)
	base := uint32(0x800000)
	runtime.setTmuxBase(&base)
	runtime.setFrame(plans, 0, 0, 400, 80, 400, true)
	runtime.beforeWrite()
	if len(runtime.pending) != tmuxTranscriptImageSlots {
		t.Fatalf("pending placements = %d, want %d", len(runtime.pending), tmuxTranscriptImageSlots)
	}
	if got := runtime.pending[len(runtime.pending)-1].id; got != base+tmuxTranscriptImageSlots-1 {
		t.Fatalf("last id = %#x", got)
	}
}

func uintToStringForTest(value uint32) string {
	if value == 0 {
		return "0"
	}
	var buf [10]byte
	index := len(buf)
	for value > 0 {
		index--
		buf[index] = byte('0' + value%10)
		value /= 10
	}
	return string(buf[index:])
}
