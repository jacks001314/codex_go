package welcome

import "testing"

// Rust parity: the renderer projects both signed-distance fields into Braille
// cells and the morph changes the visible pattern over time.
func TestBlossomFrameProjectsCells(t *testing.T) {
	renderer := NewRenderer()
	frame := renderer.Frame(40, 12, 0.0, DarkLighting())
	if len(frame) != 40*12 {
		t.Fatalf("frame cells = %d, want %d", len(frame), 40*12)
	}
	lit := 0
	for _, cell := range frame {
		if cell.Dots != 0 {
			lit++
		}
	}
	if lit < 40 {
		t.Fatalf("lit cells = %d, want a visible blossom", lit)
	}
}

func TestBlossomMorphChangesPattern(t *testing.T) {
	renderer := NewRenderer()
	first := append([]Cell(nil), renderer.Frame(36, 10, 0.10, DarkLighting())...)
	second := append([]Cell(nil), renderer.Frame(36, 10, 0.80, DarkLighting())...)
	different := 0
	for i := range first {
		if first[i].Dots != second[i].Dots {
			different++
		}
	}
	if different < 10 {
		t.Fatalf("morph changed only %d cells", different)
	}
}

func TestBlossomFrameClampsToRendererBounds(t *testing.T) {
	renderer := NewRenderer()
	frame := renderer.Frame(MaxColumns+20, MaxRows+20, 0.3, DarkLighting())
	if len(frame) != MaxColumns*MaxRows {
		t.Fatalf("clamped frame cells = %d, want %d", len(frame), MaxColumns*MaxRows)
	}
}
