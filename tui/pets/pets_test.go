package pets

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBuiltinCatalogMatchesRustPets(t *testing.T) {
	catalog := BuiltinCatalog()
	gotIDs := make([]string, 0, len(catalog))
	for _, pet := range catalog {
		gotIDs = append(gotIDs, pet.ID)
	}
	wantIDs := []string{"codex", "dewey", "fireball", "rocky", "seedy", "stacky", "bsod", "null-signal"}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("builtin ids = %#v, want %#v", gotIDs, wantIDs)
	}
	dewey, ok := BuiltinPet("dewey")
	if !ok || dewey.DisplayName != "Dewey" || dewey.Description != "A tidy duck for calm workspace days" || dewey.SpritesheetFile != "dewey-spritesheet-v4.webp" {
		t.Fatalf("dewey = %#v ok=%v", dewey, ok)
	}
	if got := BuiltinPetURL(dewey); got != "https://persistent.oaistatic.com/codex/pets/v1/dewey-spritesheet-v4.webp" {
		t.Fatalf("dewey URL = %q", got)
	}
	if got := BuiltinSpritesheetPath(`C:\codex`, dewey.SpritesheetFile); !strings.Contains(filepath.ToSlash(got), "cache/tui-pets/v1/assets/dewey-spritesheet-v4.webp") {
		t.Fatalf("spritesheet path = %q", got)
	}
}

func TestDefaultAnimationsMatchRustSpriteRows(t *testing.T) {
	animations := DefaultAnimations()
	idle := animations["idle"]
	if got := spriteIndices(idle); !reflect.DeepEqual(got, []int{0, 1, 2, 3, 4, 5}) {
		t.Fatalf("idle sprite indices = %#v", got)
	}
	if got := durationsMS(idle); !reflect.DeepEqual(got, []int{1680, 660, 660, 840, 840, 1920}) {
		t.Fatalf("idle durations = %#v", got)
	}
	if idle.LoopStart == nil || *idle.LoopStart != 0 {
		t.Fatalf("idle loop start = %#v", idle.LoopStart)
	}

	running := animations["running"]
	if got := spriteIndices(running)[:6]; !reflect.DeepEqual(got, []int{56, 57, 58, 59, 60, 61}) {
		t.Fatalf("running primary = %#v", got)
	}
	if got := durationsMS(running)[:6]; !reflect.DeepEqual(got, []int{120, 120, 120, 120, 120, 220}) {
		t.Fatalf("running durations = %#v", got)
	}
	if running.LoopStart == nil || *running.LoopStart != 18 {
		t.Fatalf("running loop start = %#v", running.LoopStart)
	}

	if got := spriteIndices(animations["waiting"])[:6]; !reflect.DeepEqual(got, []int{48, 49, 50, 51, 52, 53}) {
		t.Fatalf("waiting primary = %#v", got)
	}
	if got := spriteIndices(animations["review"])[:6]; !reflect.DeepEqual(got, []int{64, 65, 66, 67, 68, 69}) {
		t.Fatalf("review primary = %#v", got)
	}
	if got := spriteIndices(animations["failed"])[:8]; !reflect.DeepEqual(got, []int{40, 41, 42, 43, 44, 45, 46, 47}) {
		t.Fatalf("failed primary = %#v", got)
	}
}

func TestCurrentAnimationFrameUsesDurationsAndLoopStart(t *testing.T) {
	animation := Animation{
		Frames: []AnimationFrame{
			{SpriteIndex: 7, Duration: 10 * time.Millisecond},
			{SpriteIndex: 8, Duration: 10 * time.Millisecond},
		},
		LoopStart: intPtr(0),
		Fallback:  "idle",
	}
	tick, ok := CurrentAnimationFrame(animation, 15*time.Millisecond)
	if !ok || tick.SpriteIndex != 8 || !tick.HasDelay || tick.Delay != 5*time.Millisecond {
		t.Fatalf("tick at 15ms = %#v ok=%v", tick, ok)
	}
	tick, ok = CurrentAnimationFrame(animation, 25*time.Millisecond)
	if !ok || tick.SpriteIndex != 7 || !tick.HasDelay || tick.Delay != 5*time.Millisecond {
		t.Fatalf("loop tick at 25ms = %#v ok=%v", tick, ok)
	}
}

func TestImageSupportDetectionMatchesRustBranches(t *testing.T) {
	if got := DetectImageSupport(map[string]string{"TMUX": "1"}); got.Reason != PetImageUnsupportedTmux {
		t.Fatalf("tmux support = %#v", got)
	}
	if got := DetectImageSupport(map[string]string{"ZELLIJ": "1"}); got.Reason != PetImageUnsupportedZellij {
		t.Fatalf("zellij support = %#v", got)
	}
	if got := DetectImageSupport(map[string]string{"KITTY_WINDOW_ID": "1"}); got.Protocol != ImageProtocolKitty {
		t.Fatalf("kitty support = %#v", got)
	}
	if got := DetectImageSupport(map[string]string{"TERM_PROGRAM": "iTerm.app", "TERM_PROGRAM_VERSION": "3.6.0"}); got.Protocol != ImageProtocolKittyLocalFile {
		t.Fatalf("iterm support = %#v", got)
	}
	if got := DetectImageSupport(map[string]string{"TERM_PROGRAM": "iTerm.app", "TERM_PROGRAM_VERSION": "3.5.9"}); got.Reason != PetImageUnsupportedIterm2Old {
		t.Fatalf("old iterm support = %#v", got)
	}
	if got := ResolveProtocolSelection(ProtocolSelectionSixel, nil); got.Protocol != ImageProtocolSixel {
		t.Fatalf("forced sixel support = %#v", got)
	}
}

func TestKittyImageProtocolSequences(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "frame.png")
	if err := os.WriteFile(path, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	id := uint32(0xC0DE)
	command, err := KittyTransmitPNGWithID(path, 4, 3, &id, nil)
	if err != nil {
		t.Fatalf("KittyTransmitPNGWithID() error = %v", err)
	}
	if !strings.HasPrefix(command, "\x1b_Ga=T,t=d,f=100,c=4,r=3,q=2,i=49374,m=0;") || !strings.Contains(command, "cG5n") || !strings.HasSuffix(command, "\x1b\\") {
		t.Fatalf("kitty inline command = %q", command)
	}
	if got := KittyDeleteImage(id, nil); got != "\x1b_Ga=d,d=I,i=49374,q=2;\x1b\\" {
		t.Fatalf("delete image = %q", got)
	}
	fileCommand, err := KittyTransmitPNGFileWithID(path, 2, 1, nil, nil)
	if err != nil {
		t.Fatalf("KittyTransmitPNGFileWithID() error = %v", err)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fileCommand, base64.StdEncoding.EncodeToString([]byte(absolute))) {
		t.Fatalf("kitty file command = %q", fileCommand)
	}
	if got := WrapForTmuxIfNeeded("\x1b_Gx;\x1b\\", map[string]string{"TMUX": "session"}); got != "\x1bPtmux;\x1b\x1b_Gx;\x1b\x1b\\\x1b\\" {
		t.Fatalf("tmux wrapper = %q", got)
	}
}

func TestAmbientPetNotificationAndDrawRequest(t *testing.T) {
	pet := Pet{
		ID:          "test",
		FrameWidth:  DefaultFrameWidth,
		FrameHeight: DefaultFrameHeight,
		Animations:  DefaultAnimations(),
	}
	frames := make([]string, DefaultFrameCount())
	for i := range frames {
		frames[i] = filepath.Join("frames", "frame_"+string(rune('a'+i%26))+".png")
	}
	now := time.Unix(100, 0)
	state := NewAmbientPetState(pet, frames, "sixel-cache", PetImageSupport{Protocol: ImageProtocolKitty}, true, now)
	state.SetNotification(PetNotificationWaiting, "", now)
	if notification := state.VisibleNotification(now.Add(time.Second)); notification == nil || notification.Body != "Needs input" || notification.Height() != 1 {
		t.Fatalf("visible notification = %#v", notification)
	}
	if notification := state.VisibleNotification(now.Add(WaitingLifetime)); notification != nil {
		t.Fatalf("expired notification = %#v", notification)
	}
	draw, ok := state.DrawRequest(Rect{X: 0, Y: 0, Width: 100, Height: 30}, 29, now.Add(time.Second))
	if !ok {
		t.Fatal("draw request was not produced")
	}
	if draw.Protocol != ImageProtocolKitty || draw.Columns != 9 || draw.Rows != 5 || draw.HeightPX != PetTargetHeightPX || draw.X != 91 || draw.Y != 23 {
		t.Fatalf("draw request = %#v", draw)
	}
	if delay, ok := state.NextFrameDelay(now.Add(time.Second)); !ok || delay <= 0 {
		t.Fatalf("next frame delay = %v ok=%v", delay, ok)
	}
}

func TestPetPickerPreviewStateRendersRustStatuses(t *testing.T) {
	var state PreviewState
	state.SetLoading()
	lines, area, ok := state.RenderLines(Rect{X: 5, Y: 10, Width: 20, Height: 8})
	if !ok || !reflect.DeepEqual(lines, []string{"Loading preview..."}) || area != (Rect{X: 5, Y: 13, Width: 20, Height: 1}) {
		t.Fatalf("loading preview lines=%#v area=%#v ok=%v", lines, area, ok)
	}
	if last, ok := state.Area(); !ok || last != (Rect{X: 5, Y: 10, Width: 20, Height: 8}) {
		t.Fatalf("last area = %#v ok=%v", last, ok)
	}
	state.SetDisabled()
	lines, area, ok = state.RenderLines(Rect{X: 0, Y: 0, Width: 12, Height: 4})
	if !ok || !reflect.DeepEqual(lines, []string{"Terminal pets disabled", "No pet will be shown."}) || area != (Rect{X: 0, Y: 1, Width: 12, Height: 2}) {
		t.Fatalf("disabled preview lines=%#v area=%#v ok=%v", lines, area, ok)
	}
	state.SetError("missing spritesheet")
	lines, _, ok = state.RenderLines(Rect{Width: 12, Height: 4})
	if !ok || !reflect.DeepEqual(lines, []string{"Preview unavailable", "missing spritesheet"}) {
		t.Fatalf("error preview lines=%#v ok=%v", lines, ok)
	}
	state.SetReady()
	if lines, _, ok = state.RenderLines(Rect{Width: 12, Height: 4}); ok || len(lines) != 0 {
		t.Fatalf("ready preview should render image only: lines=%#v ok=%v", lines, ok)
	}
	state.Clear()
	if _, ok := state.Area(); ok {
		t.Fatal("clear should forget last area")
	}
}

func TestRenderPetImageWritesKittyPayloadAndDeletesOnClear(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "frame.png")
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
	var state PetImageRenderState
	var output bytes.Buffer
	if err := RenderAmbientPetImage(&output, &state, &request, nil); err != nil {
		t.Fatalf("RenderAmbientPetImage() error = %v", err)
	}
	text := output.String()
	if !strings.Contains(text, "\x1b_Ga=d,d=I,i=49374,q=2;\x1b\\") ||
		!strings.Contains(text, "\x1b[4;3H") ||
		!strings.Contains(text, "cG5n") ||
		!strings.HasPrefix(text, "\x1b_Ga=d") ||
		!strings.HasSuffix(text, "\x1b8") {
		t.Fatalf("kitty render output = %q", text)
	}
	if state.LastProtocol != ImageProtocolKitty {
		t.Fatalf("last protocol = %q", state.LastProtocol)
	}
	output.Reset()
	if err := RenderAmbientPetImage(&output, &state, nil, nil); err != nil {
		t.Fatalf("clear RenderAmbientPetImage() error = %v", err)
	}
	if got := output.String(); got != "\x1b_Ga=d,d=I,i=49374,q=2;\x1b\\" {
		t.Fatalf("clear kitty output = %q", got)
	}
	if state.LastProtocol != "" {
		t.Fatalf("last protocol after clear = %q", state.LastProtocol)
	}
}

func TestRenderPetImageClearsSixelArea(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "frame.six")
	if err := os.WriteFile(path, []byte("SIXEL"), 0o600); err != nil {
		t.Fatal(err)
	}
	request := AmbientPetDraw{
		Frame:     path,
		Protocol:  ImageProtocolSixel,
		X:         1,
		Y:         2,
		ClearTopY: 0,
		Columns:   3,
		Rows:      2,
	}
	var state PetImageRenderState
	var output bytes.Buffer
	if err := RenderPetImage(&output, &state, 7, &request, nil); err != nil {
		t.Fatalf("RenderPetImage() error = %v", err)
	}
	if state.LastSixelClearArea == nil || *state.LastSixelClearArea != (SixelClearArea{X: 1, ClearTopY: 0, ClearBottomY: 4, Columns: 3}) {
		t.Fatalf("sixel clear area = %#v", state.LastSixelClearArea)
	}
	if got := output.String(); !strings.Contains(got, "\x1b[1;2H   ") || !strings.Contains(got, "\x1b[3;2HSIXEL") {
		t.Fatalf("sixel render output = %q", got)
	}
	output.Reset()
	if err := RenderPetImage(&output, &state, 7, nil, nil); err != nil {
		t.Fatalf("clear RenderPetImage() error = %v", err)
	}
	if state.LastSixelClearArea != nil {
		t.Fatalf("sixel clear area after clear = %#v", state.LastSixelClearArea)
	}
	if got := output.String(); !strings.Contains(got, "\x1b[1;2H   ") || !strings.HasPrefix(got, "\x1b7") || !strings.HasSuffix(got, "\x1b8") {
		t.Fatalf("sixel clear output = %q", got)
	}
}

func spriteIndices(animation Animation) []int {
	out := make([]int, 0, len(animation.Frames))
	for _, frame := range animation.Frames {
		out = append(out, frame.SpriteIndex)
	}
	return out
}

func durationsMS(animation Animation) []int {
	out := make([]int, 0, len(animation.Frames))
	for _, frame := range animation.Frames {
		out = append(out, int(frame.Duration/time.Millisecond))
	}
	return out
}
