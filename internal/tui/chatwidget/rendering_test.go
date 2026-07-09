package chatwidget

import (
	"reflect"
	"testing"
)

func TestChatWidgetRenderPlanMatchesRustSectionOrderAndReserve(t *testing.T) {
	state := ChatWidgetRenderState{
		Width:                    80,
		Height:                   6,
		ActiveTranscriptRows:     []string{"assistant"},
		ActiveHookRows:           []string{"hook"},
		PendingTokenActivityRows: []string{"tokens"},
		PendingRateLimitHintRows: []string{"rate"},
		BottomPaneRows:           []string{"> prompt"},
		AmbientPetReservedCols:   7,
	}

	plan := state.ComposeRenderPlan()

	if plan.Width != 80 || plan.LastRenderedWidth != 80 {
		t.Fatalf("plan dimensions = %#v", plan)
	}
	kinds := make([]ChatWidgetRenderSectionKind, 0, len(plan.Sections))
	for _, section := range plan.Sections {
		kinds = append(kinds, section.Kind)
		if section.RightReserve != 7 || section.Width != 73 {
			t.Fatalf("section reserve/width = %#v", section)
		}
	}
	want := []ChatWidgetRenderSectionKind{
		RenderSectionActiveTranscript,
		RenderSectionActiveHook,
		RenderSectionPendingTokenActivity,
		RenderSectionPendingRateLimitHint,
		RenderSectionBottomPane,
	}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("section order = %#v, want %#v", kinds, want)
	}
	if plan.Sections[0].Flex != 1 || plan.Sections[1].Flex != 0 || plan.Sections[len(plan.Sections)-1].TopInset != 1 {
		t.Fatalf("section flex/top inset = %#v", plan.Sections)
	}
}

func TestChatWidgetRenderPlanOmitsInvisibleHookAndFallsBackTranscriptRows(t *testing.T) {
	state := ChatWidgetRenderState{
		Width:          20,
		Height:         4,
		TranscriptRows: []string{"one", "two"},
	}

	plan := state.ComposeRenderPlan()

	if len(plan.Sections) != 2 {
		t.Fatalf("sections = %#v", plan.Sections)
	}
	if plan.Sections[0].Kind != RenderSectionActiveTranscript || !reflect.DeepEqual(plan.Sections[0].Rows, []string{"one", "two"}) {
		t.Fatalf("active transcript section = %#v", plan.Sections[0])
	}
	if plan.Sections[1].Kind != RenderSectionBottomPane {
		t.Fatalf("bottom pane section = %#v", plan.Sections[1])
	}
}

func TestTranscriptAreaScrollAndVisibleRowsMatchRustTailBehavior(t *testing.T) {
	rows := []string{"a", "b", "c", "d"}

	if got := TranscriptAreaScrollOffset(len(rows), 2); got != 2 {
		t.Fatalf("scroll offset = %d, want 2", got)
	}
	if got := TranscriptAreaVisibleRows(rows, 2); !reflect.DeepEqual(got, []string{"c", "d"}) {
		t.Fatalf("visible rows = %#v", got)
	}
	if got := TranscriptAreaVisibleRows(rows, 0); got != nil {
		t.Fatalf("zero-height rows = %#v", got)
	}
}

func TestRenderStatusLinePartsAndCompactTranscript(t *testing.T) {
	if got := RenderStatusLineParts(" model ", "", " cwd "); got != "model | cwd" {
		t.Fatalf("status parts = %q", got)
	}
	state := ChatWidgetRenderState{TranscriptRows: []string{"1", "2", "3", "4"}}
	if got := state.CompactTranscript(3); !reflect.DeepEqual(got, []string{"...", "3", "4"}) {
		t.Fatalf("compact transcript = %#v", got)
	}
}
