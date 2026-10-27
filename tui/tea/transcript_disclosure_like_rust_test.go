package tea

import (
	"fmt"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/protocol"
	codextui "codex_go/tui"
	"codex_go/utils"
)

func disclosureFixture(t *testing.T, detailLines int) *Model {
	t.Helper()
	details := []string{"$ run command"}
	for index := 0; index < detailLines; index++ {
		details = append(details, fmt.Sprintf("detail line %02d", index))
	}
	state := codextui.NewState(nil)
	state.AddHistoryLines(
		[]string{"Ran command", "  \u2514 partial output", "  +2 lines (ctrl+t to view transcript)"},
		details,
	)
	state.Messages[0].ActivityIDs = []string{"call-disclosure"}
	state.Messages[0].ActivityHiddenLines = 2
	return NewModel(state, Options{Width: 60, Height: 12})
}

type disclosureControlPoint struct{ x int }

func transcriptDisclosureControlPoint(t *testing.T, model *Model) (int, disclosureControlPoint) {
	t.Helper()
	_ = model.View()
	if len(model.transcriptDisclosureHits) == 0 {
		t.Fatalf("no disclosure controls painted:\n%s", utils.StripANSI(model.View()))
	}
	row := -1
	for candidate := range model.transcriptDisclosureHits {
		if row < 0 || candidate < row {
			row = candidate
		}
	}
	hit := model.transcriptDisclosureHits[row]
	return model.transcriptContentTopRow + row - model.transcript.YOffset, disclosureControlPoint{x: hit.start}
}

func clickTranscriptRow(model *Model, x, y int) {
	model.Update(bubbletea.MouseMsg{X: x, Y: y, Action: bubbletea.MouseActionPress, Button: bubbletea.MouseButtonLeft})
}

func TestTranscriptDisclosureClickExpandsLikeRust(t *testing.T) {
	model := disclosureFixture(t, 2)
	view := utils.StripANSI(model.View())
	if !strings.Contains(view, "+ 2 lines (ctrl+t to expand)") {
		t.Fatalf("collapsed control missing:\n%s", view)
	}
	if strings.Contains(view, "to view transcript") {
		t.Fatalf("legacy marker was not replaced by the control:\n%s", view)
	}
	y, hit := transcriptDisclosureControlPoint(t, model)
	clickTranscriptRow(model, hit.x, y)
	view = utils.StripANSI(model.View())
	if !strings.Contains(view, "detail line 01") {
		t.Fatalf("click did not expand details:\n%s", view)
	}
	if !strings.Contains(view, "\u2212 Show less") {
		t.Fatalf("expanded control missing:\n%s", view)
	}
	if !model.activityFocus.active {
		t.Fatal("click did not enter activity focus")
	}

	y, hit = transcriptDisclosureControlPoint(t, model)
	clickTranscriptRow(model, hit.x, y)
	view = utils.StripANSI(model.View())
	if strings.Contains(view, "detail line 01") {
		t.Fatalf("second click did not collapse details:\n%s", view)
	}
	if !strings.Contains(view, "+ 2 lines (ctrl+t to expand)") {
		t.Fatalf("collapsed control missing after toggle:\n%s", view)
	}
	if strings.Contains(view, "to view transcript") {
		t.Fatalf("legacy marker returned after collapse:\n%s", view)
	}
}

// Rust render_disclosure styles only the focused control columns with the
// accent foreground/bold (removing dim) and underlines the activity header.
func TestTranscriptActivityFocusStylesDisclosureLikeRust(t *testing.T) {
	restore := codextui.SetStdoutColorLevelForTest(codextui.ColorTrue)
	t.Cleanup(restore)

	model := disclosureFixture(t, 2)
	y, hit := transcriptDisclosureControlPoint(t, model)
	clickTranscriptRow(model, hit.x, y)
	if !model.activityFocus.active {
		t.Fatal("click did not focus the activity")
	}
	lines := strings.Split(model.lastTranscriptContent, "\n")
	span := model.transcriptMessageRanges[0]
	if span[1] <= span[0] || span[0] >= len(lines) {
		t.Fatalf("invalid activity range %v for %d lines", span, len(lines))
	}
	if header := lines[span[0]]; !strings.Contains(header, "\x1b[4m") {
		t.Fatalf("focused header is not underlined: %q", header)
	}
	control := ""
	for row := span[0]; row < min(span[1], len(lines)); row++ {
		if isTranscriptDisclosureControlLine(lines[row]) {
			control = lines[row]
		}
	}
	if control == "" {
		t.Fatalf("focused activity painted no disclosure control:\n%s", utils.StripANSI(model.lastTranscriptContent))
	}
	if strings.Contains(control, "\x1b[2m") {
		t.Fatalf("focused control kept the dim style: %q", control)
	}
	if !strings.Contains(control, "\x1b[1;38;2;") {
		t.Fatalf("focused control is missing the bold accent foreground: %q", control)
	}
	if plain := utils.StripANSI(control); !strings.HasPrefix(plain, "    ") {
		t.Fatalf("control indent lost its column offset: %q", plain)
	}
}

// Rust pointer_down clears activity focus on a left press that misses the
// control, while the explicit expansion remains visible.
func TestTranscriptDisclosureClickOutsideClearsFocusLikeRust(t *testing.T) {
	model := disclosureFixture(t, 8)
	y, hit := transcriptDisclosureControlPoint(t, model)
	clickTranscriptRow(model, hit.x, y)
	if !model.activityFocus.active || !model.transcriptActivityExpanded(0) {
		t.Fatal("control click did not focus and expand the activity")
	}
	headerRow := model.transcriptMessageRanges[0][0]
	headerY := model.transcriptContentTopRow + headerRow - model.transcript.YOffset
	clickTranscriptRow(model, 0, headerY)
	if model.activityFocus.active {
		t.Fatal("click outside the disclosure kept activity focus")
	}
	if !model.transcriptActivityExpanded(0) {
		t.Fatal("click outside the disclosure collapsed the expanded details")
	}
	if view := utils.StripANSI(model.View()); !strings.Contains(view, "detail line 07") {
		t.Fatalf("expanded details disappeared after focus dismissal:\n%s", view)
	}
}

func TestTranscriptDisclosureClickRequiresControlColumnLikeRust(t *testing.T) {
	model := disclosureFixture(t, 8)
	y, hit := transcriptDisclosureControlPoint(t, model)
	clickTranscriptRow(model, 0, y)
	if model.activityFocus.active {
		t.Fatal("click outside the control entered activity focus")
	}
	model.Update(bubbletea.MouseMsg{X: hit.x, Y: y, Action: bubbletea.MouseActionPress, Button: bubbletea.MouseButtonLeft, Shift: true})
	if model.activityFocus.active {
		t.Fatal("modified click entered activity focus")
	}
}

// Rust activity_layout truncates the painted control to the transcript width
// without an ellipsis, so a narrow terminal never wraps it into a second row.
func TestTranscriptDisclosureControlTruncatesToWidthLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		disclosure codextui.ActivityDisclosure
		expanded   bool
		width      int
		want       string
	}{
		{name: "full", disclosure: codextui.ActivityDisclosure{HiddenLines: 2}, width: 80, want: "    + 2 lines (ctrl+t to expand)"},
		{name: "single line", disclosure: codextui.ActivityDisclosure{HiddenLines: 1}, width: 80, want: "    + 1 line (ctrl+t to expand)"},
		{name: "generic collapsed", disclosure: codextui.ActivityDisclosure{Generic: true}, width: 80, want: "    + Show details"},
		{name: "generic expanded", disclosure: codextui.ActivityDisclosure{Generic: true}, expanded: true, width: 80, want: "    \u2212 Hide details"},
		{name: "output expanded", disclosure: codextui.ActivityDisclosure{HiddenLines: 2}, expanded: true, width: 80, want: "    \u2212 Show less"},
		{name: "hint does not fit", disclosure: codextui.ActivityDisclosure{HiddenLines: 2}, width: 24, want: "    + 2 lines"},
		{name: "narrow", disclosure: codextui.ActivityDisclosure{HiddenLines: 2}, width: 8, want: "  + 2 li"},
		{name: "zero", disclosure: codextui.ActivityDisclosure{HiddenLines: 2}, width: 0, want: ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := utils.StripANSI(transcriptDisclosureControlLine(testCase.disclosure, testCase.expanded, testCase.width))
			if got != testCase.want {
				t.Fatalf("control at width %d = %q, want %q", testCase.width, got, testCase.want)
			}
		})
	}
}

// Rust resolves the expand hint from the configured global.open_transcript
// binding on every render; an unbound action drops it entirely.
func TestTranscriptDisclosureControlFollowsOpenTranscriptBindingLikeRust(t *testing.T) {
	defer codextui.SetOpenTranscriptHintProvider(nil)
	control := func(width int) string {
		return strings.TrimSpace(utils.StripANSI(transcriptDisclosureControlLine(
			codextui.ActivityDisclosure{HiddenLines: 2}, false, width,
		)))
	}
	if got, want := control(80), "+ 2 lines (ctrl+t to expand)"; got != want {
		t.Fatalf("default control = %q, want %q", got, want)
	}
	codextui.SetOpenTranscriptHintProvider(func() string { return "f12" })
	if got, want := control(80), "+ 2 lines (f12 to expand)"; got != want {
		t.Fatalf("remapped control = %q, want %q", got, want)
	}
	codextui.SetOpenTranscriptHintProvider(func() string { return "" })
	if got, want := control(80), "+ 2 lines"; got != want {
		t.Fatalf("unbound control = %q, want %q", got, want)
	}
}

// The real Bubble Tea program must decode SGR presses and the wheel, expand
// the clicked activity, scroll its details, and collapse it again on a second
// click. Coordinates come from the same layout pass the program paints.
func TestTranscriptDisclosureProgramMouseLikeRust(t *testing.T) {
	newState := func() *codextui.State {
		details := []string{"$ run command"}
		for index := 0; index < 30; index++ {
			details = append(details, fmt.Sprintf("detail line %02d", index))
		}
		state := codextui.NewState(nil)
		state.AddHistoryLines(
			[]string{"Ran command", "  \u2514 partial output", "  +2 lines (ctrl+t to view transcript)"},
			details,
		)
		state.Messages[0].ActivityIDs = []string{"call-disclosure"}
		state.Messages[0].ActivityHiddenLines = 2
		return state
	}
	options := Options{Width: 80, Height: 20}
	probe := NewModel(newState(), options)
	_ = probe.View()
	y, hit := transcriptDisclosureControlPoint(t, probe)

	running := startRealProgramWithState(t, newState(), options)
	waitForProgramOutput(t, running.output, "\x1b[?1049h")
	waitForProgramOutput(t, running.output, "+ 2 lines")
	writeMouse := func(button int, x, y int, release bool) {
		t.Helper()
		suffix := "M"
		if release {
			suffix = "m"
		}
		if _, err := running.input.Write([]byte(fmt.Sprintf("\x1b[<%d;%d;%d%s", button, x+1, y+1, suffix))); err != nil {
			t.Fatalf("write SGR mouse: %v", err)
		}
	}
	writeMouse(0, hit.x, y, false)
	writeMouse(0, hit.x, y, true)
	// The expanded details start at the anchored header; the collapse control is
	// below the viewport until the wheel moves it into view.
	waitForProgramOutput(t, running.output, "detail line 00")

	for range 20 {
		writeMouse(65, hit.x, y, false)
	}
	waitForProgramOutput(t, running.output, "detail line 29")

	simulated := NewModel(newState(), options)
	_ = simulated.View()
	simulated.Update(bubbletea.MouseMsg{X: hit.x, Y: y, Action: bubbletea.MouseActionPress, Button: bubbletea.MouseButtonLeft})
	for range 20 {
		simulated.Update(wheelPress(bubbletea.MouseButtonWheelDown))
	}
	_ = simulated.View()
	collapseY, collapseHit := transcriptDisclosureControlPoint(t, simulated)
	writeMouse(0, collapseHit.x, collapseY, false)
	writeMouse(0, collapseHit.x, collapseY, true)

	running.quit()
	final := running.final(t)
	if final.transcriptActivityExpanded(0) {
		t.Fatalf("second click did not collapse details:\n%s", utils.StripANSI(final.View()))
	}
	if view := utils.StripANSI(final.View()); strings.Contains(view, "detail line 29") {
		t.Fatalf("collapsed activity still paints retained details:\n%s", view)
	}
}

func TestTranscriptExpandedDetailsScrollWithWheelLikeRust(t *testing.T) {
	model := disclosureFixture(t, 30)
	y, hit := transcriptDisclosureControlPoint(t, model)
	clickTranscriptRow(model, hit.x, y)
	_ = model.View()
	before := model.transcript.YOffset
	model.Update(wheelPress(bubbletea.MouseButtonWheelDown))
	if got, want := model.transcript.YOffset, before+1; got != want {
		t.Fatalf("wheel offset = %d, want %d", got, want)
	}
	scrolled := model.transcript.YOffset
	_ = model.View()
	if got := model.transcript.YOffset; got != scrolled {
		t.Fatalf("redraw snapped wheel offset from %d to %d", scrolled, got)
	}
	for range 20 {
		model.Update(wheelPress(bubbletea.MouseButtonWheelDown))
	}
	deeper := model.transcript.YOffset
	if deeper <= scrolled {
		t.Fatalf("wheel did not keep scrolling details: %d -> %d", scrolled, deeper)
	}
	_ = model.View()
	if got := model.transcript.YOffset; got != deeper {
		t.Fatalf("redraw snapped details at %d back to %d", deeper, got)
	}
}

// TestTranscriptDisclosureHitsSkipStartupTipsLikeRust keeps click rows aligned
// when fullscreen hides a retained startup tooltip entry from the paint order.
func TestTranscriptDisclosureHitsSkipStartupTipsLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.AddHistoryLines([]string{"startup tip row"}, []string{"startup tip row"})
	state.Messages[0].StartupTip = true
	state.AddHistoryLines(
		[]string{"Ran command", "  +2 lines (ctrl+t to view transcript)"},
		[]string{"$ run command", "hidden detail"},
	)
	state.Messages[1].ActivityIDs = []string{"call-after-tip"}
	state.Messages[1].ActivityHiddenLines = 2
	model := NewModel(state, Options{Width: 60, Height: 12})
	view := utils.StripANSI(model.View())
	if strings.Contains(view, "startup tip row") {
		t.Fatalf("fullscreen transcript kept the startup tip:\n%s", view)
	}
	y, hit := transcriptDisclosureControlPoint(t, model)
	clickTranscriptRow(model, hit.x, y)
	if !model.activityFocus.active {
		t.Fatalf("click missed the control after a hidden startup tip:\n%s", utils.StripANSI(model.View()))
	}
}

// TestCompletedExecCellPublishesDisclosureLikeRust pins the live command path:
// a completed non-exploring exec cell must carry its activity IDs and compact
// output count so the transcript can paint and click the disclosure control.
func TestCompletedExecCellPublishesDisclosureLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 80, Height: 20})
	lines := make([]string, 12)
	for index := range lines {
		lines[index] = fmt.Sprintf("line %02d", index+1)
	}
	output := strings.Join(lines, "\n")
	exitCode := 0
	model.renderCommandExecutionItem(&protocol.ThreadItem{
		ID:               "call-completed",
		Type:             "command_execution",
		Command:          "1..12",
		Status:           "completed",
		ExitCode:         &exitCode,
		AggregatedOutput: &output,
	})
	if len(state.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(state.Messages))
	}
	message := state.Messages[0]
	if len(message.ActivityIDs) == 0 || message.ActivityHiddenLines != 9 {
		t.Fatalf("activity metadata = ids=%v hidden=%d, want one id and 9 hidden lines", message.ActivityIDs, message.ActivityHiddenLines)
	}
	view := utils.StripANSI(model.View())
	if !strings.Contains(view, "+ 9 lines (ctrl+t to expand)") {
		t.Fatalf("completed exec cell did not paint the disclosure control:\n%s", view)
	}
	if strings.Contains(view, "to view transcript") {
		t.Fatalf("completed exec cell kept the legacy marker:\n%s", view)
	}
	y, hit := transcriptDisclosureControlPoint(t, model)
	clickTranscriptRow(model, hit.x, y)
	if !model.activityFocus.active {
		t.Fatalf("click did not expand the completed exec cell:\n%s", utils.StripANSI(model.View()))
	}
}

// Rust paints the disclosure control for every expanded activity, even when the
// cell reports no retained details: the control reads "− Hide details" and a
// click collapses it again. A collapsed activity without details has no control.
func TestExpandedActivityWithoutDetailsShowsHideControlLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.AddHistoryMessage(codextui.Message{
		Role:        codextui.RoleHistory,
		Text:        "Ran echo\n  \u2514 a",
		RawText:     "Ran echo\n  \u2514 a",
		ActivityIDs: []string{"call-no-details"},
	})
	model := NewModel(state, Options{Width: 60, Height: 12})
	view := utils.StripANSI(model.View())
	if strings.Contains(view, "Show details") || strings.Contains(view, "Hide details") {
		t.Fatalf("collapsed activity without details painted a control:\n%s", view)
	}
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyF4})
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRight})
	view = utils.StripANSI(model.View())
	if !strings.Contains(view, "\u2212 Hide details") {
		t.Fatalf("expanded activity without details lost its hide control:\n%s", view)
	}
	y, hit := transcriptDisclosureControlPoint(t, model)
	clickTranscriptRow(model, hit.x, y)
	view = utils.StripANSI(model.View())
	if strings.Contains(view, "Hide details") {
		t.Fatalf("click did not collapse the activity:\n%s", view)
	}
	if !model.activityFocus.active {
		t.Fatal("collapse click cleared activity focus")
	}
}

// Rust's ExecCell reports no disclosure when a completed command fits in its
// compact preview. The transcript must not infer one from the styled compact
// text differing from the raw command lines.
func TestCompletedShortExecCellHasNoDisclosureLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 80, Height: 20})
	output := "hello"
	exitCode := 0
	model.renderCommandExecutionItem(&protocol.ThreadItem{
		ID:               "call-short",
		Type:             "command_execution",
		Command:          "echo hello",
		Status:           "completed",
		ExitCode:         &exitCode,
		AggregatedOutput: &output,
	})
	if len(state.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(state.Messages))
	}
	message := state.Messages[0]
	if !message.ActivityDisclosureKnown || message.ActivityHiddenLines != 0 || message.ActivityGenericDetails {
		t.Fatalf("short command disclosure = known=%v hidden=%d generic=%v, want known empty",
			message.ActivityDisclosureKnown, message.ActivityHiddenLines, message.ActivityGenericDetails)
	}
	view := utils.StripANSI(model.View())
	if strings.Contains(view, "Show details") || strings.Contains(view, "to expand") {
		t.Fatalf("short command painted a disclosure control:\n%s", view)
	}
	if len(model.transcriptDisclosureHits) != 0 {
		t.Fatalf("short command registered %d disclosure hits", len(model.transcriptDisclosureHits))
	}
}
