package pets

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Rust #59579: only virtual placements add U=1; cursor placements keep their
// previous sequence shape.
func TestKittyVirtualPlacementArgsLikeRust(t *testing.T) {
	path := filepath.Join(t.TempDir(), "frame.png")
	if err := os.WriteFile(path, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	virtual, err := KittyTransmitPNGWithID(path, 4, 3, VirtualKittyPlacement(7), nil)
	if err != nil {
		t.Fatalf("virtual transmit: %v", err)
	}
	if !strings.HasPrefix(virtual, "\x1b_Ga=T,t=d,f=100,c=4,r=3,q=2,i=7,U=1,m=0,C=1;") {
		t.Fatalf("virtual transmit = %q", virtual)
	}
	cursor, err := KittyTransmitPNGWithID(path, 4, 3, CursorKittyPlacement(7), nil)
	if err != nil {
		t.Fatalf("cursor transmit: %v", err)
	}
	if !strings.HasPrefix(cursor, "\x1b_Ga=T,t=d,f=100,c=4,r=3,q=2,i=7,m=0,C=1;") || strings.Contains(cursor, "U=1") {
		t.Fatalf("cursor transmit = %q", cursor)
	}
	fileCommand, err := KittyTransmitPNGFileWithID(path, 2, 1, VirtualKittyPlacement(9), nil)
	if err != nil {
		t.Fatalf("virtual file transmit: %v", err)
	}
	if !strings.Contains(fileCommand, ",i=9,U=1,C=1;") {
		t.Fatalf("virtual file transmit = %q", fileCommand)
	}
}

// Rust #59580: a pet inside a tmux pane takes the pane's id window, switches to
// a virtual placement and paints the placeholder grid that carries the image.
func TestRenderPetImageUsesPaneImageBaseLikeRust(t *testing.T) {
	path := filepath.Join(t.TempDir(), "frame.png")
	if err := os.WriteFile(path, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	request := AmbientPetDraw{
		Frame:    path,
		Protocol: ImageProtocolKitty,
		X:        2,
		Y:        3,
		Columns:  4,
		Rows:     3,
	}
	base := uint32(0x800000 + (3 << 8))
	paneID := base + AmbientPetPaneSlot
	var state PetImageRenderState
	var output bytes.Buffer
	if err := RenderAmbientPetImage(&output, &state, &request, nil, PetImageOptions{PaneBase: &base}); err != nil {
		t.Fatalf("RenderAmbientPetImage() error = %v", err)
	}
	text := output.String()
	if !strings.Contains(text, ",i="+uintToString(paneID)+",U=1,") {
		t.Fatalf("pane transmit missing: %q", text)
	}
	if state.LastKittyID == nil || *state.LastKittyID != paneID {
		t.Fatalf("last kitty id = %v, want %d", state.LastKittyID, paneID)
	}
	if !strings.Contains(text, "\x1b[38;2;128;3;254m") || !strings.Contains(text, "\x1b[4;3H") {
		t.Fatalf("placeholder grid missing: %q", text)
	}
	if got, want := strings.Count(text, placeholderRune), int(request.Columns)*int(request.Rows); got != want {
		t.Fatalf("placeholder cells = %d, want %d: %q", got, want, text)
	}
	if !strings.HasSuffix(text, "\x1b[39m\x1b8") {
		t.Fatalf("placeholder colour was not reset: %q", text)
	}

	output.Reset()
	if err := RenderAmbientPetImage(&output, &state, nil, nil, PetImageOptions{PaneBase: &base}); err != nil {
		t.Fatalf("clear RenderAmbientPetImage() error = %v", err)
	}
	if got := output.String(); !strings.Contains(got, ",i="+uintToString(paneID)+",q=2;") {
		t.Fatalf("clear did not delete the pane image: %q", got)
	}
	if state.LastKittyID != nil {
		t.Fatalf("last kitty id after clear = %d", *state.LastKittyID)
	}
}

// The picker preview owns the last slot of the pane window so it never collides
// with transcript images.
func TestPickerPreviewUsesLastPaneSlotLikeRust(t *testing.T) {
	path := filepath.Join(t.TempDir(), "frame.png")
	if err := os.WriteFile(path, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	request := AmbientPetDraw{Frame: path, Protocol: ImageProtocolKitty, Columns: 2, Rows: 1}
	base := uint32(0x800000)
	var state PetImageRenderState
	var output bytes.Buffer
	if err := RenderPetPickerPreviewImage(&output, &state, &request, nil, PetImageOptions{PaneBase: &base}); err != nil {
		t.Fatalf("RenderPetPickerPreviewImage() error = %v", err)
	}
	if want := uint32(0x800000) + PickerPetPaneSlot; state.LastKittyID == nil || *state.LastKittyID != want {
		t.Fatalf("picker id = %v, want %d", state.LastKittyID, want)
	}
	if !strings.Contains(output.String(), ",U=1,") {
		t.Fatalf("picker preview was not virtually placed: %q", output.String())
	}
}
