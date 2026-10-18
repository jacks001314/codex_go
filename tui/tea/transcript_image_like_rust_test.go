package tea

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/protocol"
	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
	historycell "codex_go/tui/history_cell"
	"codex_go/tui/pets"
	"codex_go/tui/transcript_image"
	"codex_go/utils"
)

func writeTranscriptTestPNG(t *testing.T, width int, height int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "artifact.png")
	writeTranscriptTestPNGFile(t, path, width, height)
	return path
}

func writeTranscriptTestPNGFile(t *testing.T, path string, width int, height int) {
	t.Helper()
	source := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			source.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 90, A: 255})
		}
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create image: %v", err)
	}
	if err := png.Encode(file, source); err != nil {
		file.Close()
		t.Fatalf("encode image: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close image: %v", err)
	}
}

func transcriptImageModel(t *testing.T, cell historycell.HistoryCell) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	model := NewModel(state, Options{
		Width:               80,
		Height:              24,
		LocalSession:        true,
		PetEnv:              map[string]string{"KITTY_WINDOW_ID": "1"},
		InitialHistoryCells: []historycell.HistoryCell{cell},
	})
	model.transcriptImageCell = transcript_image.CellSize{Width: 8, Height: 16}
	model.refreshTranscript()
	return model
}

func TestGeneratedImageReservesPreviewRowsLikeRust(t *testing.T) {
	path := writeTranscriptTestPNG(t, 40, 32)
	model := transcriptImageModel(t, historycell.NewImageGenerationCall("img-1", "completed", "a red cube", path))

	if len(model.transcriptImagePlans) != 1 {
		t.Fatalf("image plans = %#v", model.transcriptImagePlans)
	}
	plan := model.transcriptImagePlans[0]
	if plan.rows <= 0 || plan.columns <= 0 {
		t.Fatalf("plan = %#v", plan)
	}
	lines := strings.Split(model.lastTranscriptContent, "\n")
	if len(lines) != plan.contentRow+plan.rows {
		t.Fatalf("lines = %d, want %d", len(lines), plan.contentRow+plan.rows)
	}
	for index := plan.contentRow; index < len(lines); index++ {
		if strings.TrimSpace(lines[index]) != "" {
			t.Fatalf("image row %d = %q, want blank", index, lines[index])
		}
	}
}

func TestViewedImagePreviewFollowsDisclosureLikeRust(t *testing.T) {
	path := writeTranscriptTestPNG(t, 40, 32)
	cell := historycell.NewViewImageToolCallWithID("view-1", path, "")
	model := transcriptImageModel(t, cell)

	if len(model.State.Messages) != 1 || model.State.Messages[0].ImagePath != path {
		t.Fatalf("message = %#v", model.State.Messages)
	}
	if got, want := model.State.Messages[0].ActivityIDs, []string{"image-view:view-1"}; len(got) != 1 || got[0] != want[0] {
		t.Fatalf("activity ids = %#v, want %#v", got, want)
	}
	if len(model.transcriptImagePlans) != 0 {
		t.Fatalf("collapsed viewed image planned a preview: %#v", model.transcriptImagePlans)
	}

	model.focusTranscriptActivity()
	if !model.setTranscriptActivityExpanded(true) {
		t.Fatal("viewed image was not expandable")
	}
	if len(model.transcriptImagePlans) != 1 {
		t.Fatalf("expanded viewed image plans = %#v", model.transcriptImagePlans)
	}
	model.setTranscriptActivityExpanded(false)
	if len(model.transcriptImagePlans) != 0 {
		t.Fatalf("collapsed viewed image kept a preview: %#v", model.transcriptImagePlans)
	}
}

func TestTranscriptImageFallbacksLikeRust(t *testing.T) {
	path := writeTranscriptTestPNG(t, 40, 32)
	cell := historycell.NewImageGenerationCall("img-1", "completed", "a red cube", path)

	raw := transcriptImageModel(t, cell)
	raw.rawOutput = true
	raw.refreshTranscript()
	if len(raw.transcriptImagePlans) != 0 {
		t.Fatalf("raw mode planned previews: %#v", raw.transcriptImagePlans)
	}

	remote := transcriptImageModel(t, cell)
	remote.localSession = false
	remote.refreshTranscript()
	if len(remote.transcriptImagePlans) != 0 {
		t.Fatalf("remote workspace planned previews: %#v", remote.transcriptImagePlans)
	}

	unsupported := NewModel(codextui.NewState(nil), Options{
		Width:               80,
		Height:              24,
		LocalSession:        true,
		PetEnv:              map[string]string{"TERM": "dumb"},
		InitialHistoryCells: []historycell.HistoryCell{cell},
	})
	unsupported.refreshTranscript()
	if len(unsupported.transcriptImagePlans) != 0 {
		t.Fatalf("unsupported terminal planned previews: %#v", unsupported.transcriptImagePlans)
	}

	missing := transcriptImageModel(t, historycell.NewImageGenerationCall("img-2", "completed", "gone", filepath.Join(t.TempDir(), "missing.png")))
	if len(missing.transcriptImagePlans) != 0 {
		t.Fatalf("missing artifact planned previews: %#v", missing.transcriptImagePlans)
	}
	if !strings.Contains(missing.lastTranscriptContent, "Saved to:") {
		t.Fatalf("missing artifact lost its text entry:\n%s", missing.lastTranscriptContent)
	}
}

func TestTranscriptImageOverlayHidesPlacementsLikeRust(t *testing.T) {
	path := writeTranscriptTestPNG(t, 40, 32)
	model := transcriptImageModel(t, historycell.NewImageGenerationCall("img-1", "completed", "a red cube", path))
	if len(model.transcriptImagePlans) != 1 {
		t.Fatalf("plans = %#v", model.transcriptImagePlans)
	}
	model.overlay = chatwidget.NewTranscriptOverlay(80, 20, "transcript")
	_ = model.View()
	if len(model.transcriptImages.plans) != 0 || model.transcriptImages.surface {
		t.Fatalf("overlay retained graphics: %#v surface=%v", model.transcriptImages.plans, model.transcriptImages.surface)
	}
}

func TestTranscriptImageSurvivesBottomPaneModalLikeRust(t *testing.T) {
	path := writeTranscriptTestPNG(t, 40, 32)
	model := transcriptImageModel(t, historycell.NewImageGenerationCall("img-1", "completed", "a red cube", path))
	model.Update(ApprovalRequestMsg{
		ID:      "approval-image",
		Title:   "Run command?",
		Body:    "Reason: preview stays visible",
		Command: "go test ./...",
	})
	_ = model.View()
	if !model.transcriptImages.surface || len(model.transcriptImages.plans) != 1 {
		t.Fatalf("bottom modal removed transcript graphics: surface=%v plans=%#v", model.transcriptImages.surface, model.transcriptImages.plans)
	}
}

func TestTranscriptImageRewrapsOnResizeLikeRust(t *testing.T) {
	path := writeTranscriptTestPNG(t, 200, 120)
	model := transcriptImageModel(t, historycell.NewImageGenerationCall("img-1", "completed", "a wide image", path))
	if len(model.transcriptImagePlans) != 1 {
		t.Fatalf("plans = %#v", model.transcriptImagePlans)
	}
	before := model.transcriptImagePlans[0].columns
	model.resize(20, 24)
	if len(model.transcriptImagePlans) != 1 {
		t.Fatalf("resized plans = %#v", model.transcriptImagePlans)
	}
	after := model.transcriptImagePlans[0].columns
	if after >= before || after+2 > model.transcript.Width {
		t.Fatalf("rewrapped columns = %d -> %d (width %d)", before, after, model.transcript.Width)
	}
}

func TestTranscriptImageKeepsCompletionTipBelowPreviewLikeRust(t *testing.T) {
	enabled := true
	path := writeTranscriptTestPNG(t, 40, 32)
	model := NewModel(codextui.NewState(nil), Options{
		Width:        120,
		Height:       30,
		ShowTooltips: &enabled,
		LocalSession: true,
		PetEnv:       map[string]string{"KITTY_WINDOW_ID": "1"},
	})
	model.transcriptImageCell = transcript_image.CellSize{Width: 8, Height: 16}
	for index := 0; index < turnTipCompletionInterval; index++ {
		turnID := turnIDForTest(index)
		model.Update(ThreadEventMsg{Event: protocol.TurnStartedWithID(turnID)})
		model.Update(ThreadEventMsg{Event: protocol.ThreadEvent{
			Type: "item.completed",
			Item: &protocol.ThreadItem{ID: "final-" + turnID, Type: "agent_message", Text: "done", Phase: "final_answer"},
		}})
		if index == turnTipCompletionInterval-1 {
			model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.ImageGenerationItem("img-1", "completed", "a red cube", path))})
		}
		model.Update(ThreadEventMsg{Event: protocol.TurnCompletedWithID(protocol.Usage{}, turnID)})
	}
	if len(model.transcriptImagePlans) != 1 {
		t.Fatalf("image plans = %#v", model.transcriptImagePlans)
	}
	plan := model.transcriptImagePlans[0]
	lines := strings.Split(utils.StripANSI(model.lastTranscriptContent), "\n")
	tipRow := -1
	for index, line := range lines {
		if strings.Contains(line, "Tip: ") {
			tipRow = index
			break
		}
	}
	if tipRow < 0 {
		t.Fatalf("completion tip missing:\n%s", strings.Join(lines, "\n"))
	}
	if tipRow < plan.contentRow+plan.rows {
		t.Fatalf("completion tip row %d overlaps image rows %d..%d:\n%s", tipRow, plan.contentRow, plan.contentRow+plan.rows, numberedTranscriptLines(lines))
	}
}

func TestTranscriptImageFindDoesNotRevealViewedPixelsLikeRust(t *testing.T) {
	path := writeTranscriptTestPNG(t, 40, 32)
	model := transcriptImageModel(t, historycell.NewViewImageToolCallWithID("view-1", path, ""))
	model.mainFind.editing = true
	model.mainFind.query = "Viewed"
	model.refreshMainTranscriptFindMatches(true)
	model.refreshTranscript()
	if len(model.mainFind.matches) == 0 {
		t.Fatal("find did not match the viewed-image text")
	}
	if len(model.transcriptImagePlans) != 0 {
		t.Fatalf("find revealed inspected pixels: %#v", model.transcriptImagePlans)
	}
	model.clearMainTranscriptFind(false)
	model.focusTranscriptActivity()
	if !model.setTranscriptActivityExpanded(true) || len(model.transcriptImagePlans) != 1 {
		t.Fatalf("manual expansion plans = %#v", model.transcriptImagePlans)
	}
}

func TestTranscriptImageCacheInvalidatesDeletedFileLikeRust(t *testing.T) {
	path := writeTranscriptTestPNG(t, 40, 32)
	model := transcriptImageModel(t, historycell.NewImageGenerationCall("img-1", "completed", "a red cube", path))
	if len(model.transcriptImagePlans) != 1 {
		t.Fatalf("initial plans = %#v", model.transcriptImagePlans)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove image: %v", err)
	}
	model.refreshTranscript()
	if len(model.transcriptImagePlans) != 0 {
		t.Fatalf("deleted image kept a preview: %#v", model.transcriptImagePlans)
	}
	if strings.HasSuffix(model.lastTranscriptContent, "\n") {
		t.Fatalf("deleted image kept reserved rows:\n%q", model.lastTranscriptContent)
	}
	writeTranscriptTestPNGFile(t, path, 40, 32)
	model.refreshTranscript()
	if len(model.transcriptImagePlans) != 1 {
		t.Fatalf("restored image plans = %#v", model.transcriptImagePlans)
	}
}

func numberedTranscriptLines(lines []string) string {
	var builder strings.Builder
	for index, line := range lines {
		fmt.Fprintf(&builder, "%02d %q\n", index, line)
	}
	return builder.String()
}

func TestTranscriptImagePlacementClipsToViewportLikeRust(t *testing.T) {
	imageData := &transcript_image.TranscriptImage{
		Protocol:   pets.ImageProtocolKitty,
		Columns:    10,
		Rows:       4,
		Pixels:     image.NewRGBA(image.Rect(0, 0, 100, 80)),
		CellWidth:  8,
		CellHeight: 16,
	}
	runtime := newTranscriptImageRuntime(&bytes.Buffer{}, nil)
	runtime.setFrame([]transcriptImagePlan{{
		image:      imageData,
		contentRow: 10,
		columns:    10,
		rows:       4,
	}}, 2, 11, 3, 80, 24, true)

	targets := runtime.targetsLocked()
	if len(targets) != 1 {
		t.Fatalf("targets = %#v", targets)
	}
	target := targets[0]
	if target.key.y != 2 || target.key.x != 35 || target.key.rows != 3 || target.key.firstRow != 1 {
		t.Fatalf("clipped target = %#v", target.key)
	}

	runtime.setFrame(runtime.plans, 2, 20, 3, 80, 24, true)
	if targets := runtime.targetsLocked(); len(targets) != 0 {
		t.Fatalf("offscreen targets = %#v", targets)
	}
}

func TestTranscriptImageWriterRetainsAndRemovesGraphicsLikeRust(t *testing.T) {
	path := writeTranscriptTestPNG(t, 40, 32)
	imageData, err := transcript_image.Load(path, 80, pets.ImageProtocolKitty, transcript_image.CellSize{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var output bytes.Buffer
	images := newTranscriptImageRuntime(&output, nil)
	images.setFrame([]transcriptImagePlan{{
		image:      imageData,
		contentRow: 2,
		columns:    int(imageData.Columns),
		rows:       int(imageData.Rows),
	}}, 1, 0, 10, 80, 24, true)
	writer := &petOutputWriter{runtime: newPetRuntime(&output, nil), images: images}

	if _, err := writer.Write([]byte("frame")); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	if !strings.Contains(output.String(), "\x1b_Ga=T") || !strings.Contains(output.String(), ",i=49408") {
		t.Fatalf("kitty transmit missing:\n%q", output.String())
	}
	output.Reset()
	if _, err := writer.Write([]byte("frame2")); err != nil {
		t.Fatalf("write redraw: %v", err)
	}
	if !strings.Contains(output.String(), "\x1b_Ga=T") {
		t.Fatalf("kitty graphics were not repainted after a redraw:\n%q", output.String())
	}
	output.Reset()
	images.hide()
	if _, err := writer.Write([]byte("overlay")); err != nil {
		t.Fatalf("write overlay: %v", err)
	}
	if !strings.Contains(output.String(), "\x1b_Ga=d,d=I,i=49408") {
		t.Fatalf("kitty delete missing after hide:\n%q", output.String())
	}
}

func TestTranscriptImageLocalFileCleansTransportLikeRust(t *testing.T) {
	path := writeTranscriptTestPNG(t, 40, 32)
	imageData, err := transcript_image.Load(path, 80, pets.ImageProtocolKittyLocalFile, transcript_image.CellSize{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var output bytes.Buffer
	images := newTranscriptImageRuntime(&output, nil)
	images.setFrame([]transcriptImagePlan{{
		image:      imageData,
		contentRow: 0,
		columns:    int(imageData.Columns),
		rows:       int(imageData.Rows),
	}}, 1, 0, 10, 80, 24, true)
	writer := &petOutputWriter{runtime: newPetRuntime(&output, nil), images: images}
	if _, err := writer.Write([]byte("frame")); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	if len(images.drawn) != 1 || images.drawn[0].encoded == nil || images.drawn[0].encoded.TempPath == "" {
		t.Fatalf("local-file transport = %#v", images.drawn)
	}
	transport := images.drawn[0].encoded.TempPath
	if _, err := os.Stat(transport); err != nil {
		t.Fatalf("transport file missing: %v", err)
	}
	images.releaseFiles()
	if _, err := os.Stat(transport); !os.IsNotExist(err) {
		t.Fatalf("releaseFiles kept transport: %v", err)
	}
	if _, err := writer.Write([]byte("frame2")); err != nil {
		t.Fatalf("write frame2: %v", err)
	}
	if len(images.drawn) != 1 || images.drawn[0].encoded == nil || images.drawn[0].encoded.TempPath == "" {
		t.Fatalf("local-file redraw transport = %#v", images.drawn)
	}
	transport = images.drawn[0].encoded.TempPath
	images.hide()
	if _, err := writer.Write([]byte("overlay")); err != nil {
		t.Fatalf("write overlay: %v", err)
	}
	if _, err := os.Stat(transport); !os.IsNotExist(err) {
		t.Fatalf("transport file survived removal: %v", err)
	}
}

func TestTranscriptImageChromeTopOffsetLikeRust(t *testing.T) {
	path := writeTranscriptTestPNG(t, 40, 32)
	state := codextui.NewState(nil)
	model := NewModel(state, Options{
		Width:               120,
		Height:              30,
		LocalSession:        true,
		PetEnv:              map[string]string{"KITTY_WINDOW_ID": "1"},
		InitialHistoryCells: []historycell.HistoryCell{historycell.NewImageGenerationCall("img-1", "completed", "cube", path)},
	})
	model.transcriptImageCell = transcript_image.CellSize{Width: 8, Height: 16}
	_ = model.View()
	if len(model.transcriptImages.plans) != 1 {
		t.Fatalf("plans = %#v", model.transcriptImages.plans)
	}
	if model.transcriptImageFrameRows <= 0 {
		t.Fatal("frame row count was not recorded")
	}
	wantTop := model.height - model.transcriptImageFrameRows + 2
	if model.transcriptImages.top != wantTop {
		t.Fatalf("transcript image top = %d, want %d", model.transcriptImages.top, wantTop)
	}
}

func TestTranscriptImageSixelClearsOnRemovalLikeRust(t *testing.T) {
	imageData := &transcript_image.TranscriptImage{
		Protocol:   pets.ImageProtocolSixel,
		Columns:    2,
		Rows:       1,
		Pixels:     image.NewRGBA(image.Rect(0, 0, 16, 16)),
		CellWidth:  8,
		CellHeight: 16,
	}
	for index := range imageData.Pixels.Pix {
		imageData.Pixels.Pix[index] = 255
	}
	var output bytes.Buffer
	images := newTranscriptImageRuntime(&output, nil)
	images.setFrame([]transcriptImagePlan{{
		image:      imageData,
		contentRow: 0,
		columns:    2,
		rows:       1,
	}}, 3, 0, 5, 80, 24, true)
	writer := &petOutputWriter{runtime: newPetRuntime(&output, nil), images: images}
	if _, err := writer.Write([]byte("frame")); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	if !strings.Contains(output.String(), "\x1bP") {
		t.Fatalf("sixel payload missing:\n%q", output.String())
	}
	output.Reset()
	images.hide()
	if _, err := writer.Write([]byte("text")); err != nil {
		t.Fatalf("write hide frame: %v", err)
	}
	if !strings.Contains(output.String(), "\x1b[4;") || !strings.Contains(output.String(), "  ") {
		t.Fatalf("sixel clear missing:\n%q", output.String())
	}
}

func TestViewImageToolCallBecomesPreviewCellLikeRust(t *testing.T) {
	path := writeTranscriptTestPNG(t, 40, 32)
	model := transcriptImageModel(t, historycell.NewPlainHistoryCell([]string{"seed"}))
	started := &protocol.ThreadItem{
		ID:       "tool-call-view",
		Type:     "tool_call",
		ToolName: "view_image",
		Input:    `{"path":"` + strings.ReplaceAll(path, `\`, `\\`) + `"}`,
	}
	model.applyItemStarted(started, 0)
	if len(model.State.Messages) != 1 {
		t.Fatalf("view_image rendered an in-progress tool row: %#v", model.State.Messages)
	}
	model.applyItemCompleted(started)
	if len(model.State.Messages) != 2 {
		t.Fatalf("view_image completion messages = %#v", model.State.Messages)
	}
	message := model.State.Messages[1]
	if message.ImagePath != path {
		t.Fatalf("view_image preview path = %q, want %q", message.ImagePath, path)
	}
	if ids := message.ActivityIDs; len(ids) != 1 || ids[0] != "image-view:tool-call-view" {
		t.Fatalf("view_image activity ids = %#v", ids)
	}
	if len(model.transcriptImagePlans) != 0 {
		t.Fatalf("collapsed view_image planned a preview: %#v", model.transcriptImagePlans)
	}
	model.focusTranscriptActivity()
	if !model.setTranscriptActivityExpanded(true) || len(model.transcriptImagePlans) != 1 {
		t.Fatalf("expanded view_image plans = %#v", model.transcriptImagePlans)
	}
}
