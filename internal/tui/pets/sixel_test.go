package pets

import (
	"strings"
	"testing"
)

func TestEncodeRGBARedPixelPaletteAndDataMatchRust(t *testing.T) {
	sixel, err := EncodeRGBA([]byte{255, 0, 0, 255}, 1, 1)
	if err != nil {
		t.Fatalf("EncodeRGBA() error = %v", err)
	}

	want := "\x1bP9;1;0q\"1;1;1;1#224;2;100;0;0#224@\x1b\\"
	if got := string(sixel); got != want {
		t.Fatalf("sixel = %q, want %q", got, want)
	}
}

func TestEncodeRGBATransparentPixelsOmitPaletteAndDataMatchRust(t *testing.T) {
	sixel, err := EncodeRGBA([]byte{255, 0, 0, 0}, 1, 1)
	if err != nil {
		t.Fatalf("EncodeRGBA() error = %v", err)
	}

	want := "\x1bP9;1;0q\"1;1;1;1\x1b\\"
	if got := string(sixel); got != want {
		t.Fatalf("sixel = %q, want %q", got, want)
	}
}

func TestEncodeRGBAMultiBandAdvancesMatchRust(t *testing.T) {
	rgba := []byte{}
	for i := 0; i < 7; i++ {
		rgba = append(rgba, 255, 0, 0, 255)
	}

	sixel, err := EncodeRGBA(rgba, 1, 7)
	if err != nil {
		t.Fatalf("EncodeRGBA() error = %v", err)
	}

	want := "\x1bP9;1;0q\"1;1;1;7#224;2;100;0;0#224~$-#224@\x1b\\"
	if got := string(sixel); got != want {
		t.Fatalf("sixel = %q, want %q", got, want)
	}
}

func TestEncodeRGBARepeatedCellsUseRunLengthMatchRust(t *testing.T) {
	rgba := []byte{}
	for i := 0; i < 4; i++ {
		rgba = append(rgba, 255, 0, 0, 255)
	}

	sixel, err := EncodeRGBA(rgba, 4, 1)
	if err != nil {
		t.Fatalf("EncodeRGBA() error = %v", err)
	}
	if !strings.Contains(string(sixel), "#224!4@") {
		t.Fatalf("sixel did not contain RLE red cells: %q", string(sixel))
	}
}

func TestEncodeRGBARejectsMismatchedBufferLengthMatchRust(t *testing.T) {
	_, err := EncodeRGBA([]byte{255, 0, 0}, 1, 1)
	if err == nil || err.Error() != "sixel RGBA buffer has 3 bytes, expected 4" {
		t.Fatalf("EncodeRGBA() error = %v", err)
	}
}

func TestSixelSupportedKeepsCompatibilityAndExpandedTerminals(t *testing.T) {
	for _, term := range []string{"xterm-sixel", "wezterm", "mlterm", "foot-extra"} {
		if !SixelSupported(term) {
			t.Fatalf("SixelSupported(%q) = false, want true", term)
		}
	}
	if SixelSupported("vt100") {
		t.Fatal("SixelSupported(vt100) = true, want false")
	}
}
