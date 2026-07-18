package appserver

import "testing"

func TestProposedPlanStreamParserSplitsTagsAcrossChunks(t *testing.T) {
	parser := newProposedPlanStreamParser()
	segments := append(parser.push("Intro\n<prop"), parser.push("osed_plan>\n- step\n")...)
	segments = append(segments, parser.push("</proposed_plan>\nOutro")...)
	segments = append(segments, parser.finish()...)

	if got := segmentText(segments, proposedPlanSegmentNormal); got != "Intro\nOutro" {
		t.Fatalf("normal text = %q", got)
	}
	if got := segmentText(segments, proposedPlanSegmentDelta); got != "- step\n" {
		t.Fatalf("plan text = %q", got)
	}
}

func TestProposedPlanStreamParserIgnoresInlineTag(t *testing.T) {
	visible, plan, ok := splitProposedPlanText("  <proposed_plan> extra\n")
	if ok {
		t.Fatalf("plan detected with text %q", plan)
	}
	if visible != "  <proposed_plan> extra\n" {
		t.Fatalf("visible = %q", visible)
	}
}

func TestProposedPlanStreamParserClosesUnterminatedBlockOnFinish(t *testing.T) {
	visible, plan, ok := splitProposedPlanText("<proposed_plan>\n- step\n")
	if !ok {
		t.Fatal("plan not detected")
	}
	if visible != "" || plan != "- step\n" {
		t.Fatalf("visible=%q plan=%q", visible, plan)
	}
}

func segmentText(segments []proposedPlanSegment, kind proposedPlanSegmentKind) string {
	out := ""
	for _, segment := range segments {
		if segment.Kind == kind {
			out += segment.Text
		}
	}
	return out
}
