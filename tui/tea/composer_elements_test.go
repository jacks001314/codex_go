package tea

import (
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

// TestTransformComposerElementsMirrorsTextareaEdits covers the range rules:
// edits away from an element shift it, edits touching it drop it, and any range
// whose placeholder no longer matches the text is discarded.
func TestTransformComposerElementsMirrorsTextareaEdits(t *testing.T) {
	single := []ComposerTextElement{{Start: 6, End: 11, Placeholder: "world"}}

	shifted := transformComposerElements(single, "hello world", "hello big world")
	if len(shifted) != 1 || shifted[0].Start != 10 || shifted[0].End != 15 {
		t.Fatalf("insertion before the element = %#v", shifted)
	}

	afterDelete := transformComposerElements(single, "hello world", "world")
	if len(afterDelete) != 1 || afterDelete[0].Start != 0 || afterDelete[0].End != 5 {
		t.Fatalf("deletion before the element = %#v", afterDelete)
	}

	unchanged := transformComposerElements(single, "hello world", "hello world!")
	if len(unchanged) != 1 || unchanged[0].Start != 6 {
		t.Fatalf("insertion after the element = %#v", unchanged)
	}

	if dropped := transformComposerElements(single, "hello world", "hello wor"); len(dropped) != 0 {
		t.Fatalf("deletion touching the element = %#v, want dropped", dropped)
	}
	if dropped := transformComposerElements(single, "hello world", "hello wXorld"); len(dropped) != 0 {
		t.Fatalf("insertion inside the element = %#v, want dropped", dropped)
	}
}

// TestComposerTextElementsForPromptTrimsLeadingWhitespace covers the mapping of
// tracked ranges onto the trimmed prompt that is submitted.
func TestComposerTextElementsForPromptTrimsLeadingWhitespace(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 80, Height: 24})
	value := "  $imagegen make a chart"
	model.composer.SetValue(value)
	model.registerComposerElement(value, 2, len("  $imagegen"))
	elements := model.composerTextElementsForPromptValue(value, "$imagegen make a chart")
	if len(elements) != 1 || elements[0].Start != 0 || elements[0].End != len("$imagegen") || elements[0].Placeholder != "$imagegen" {
		t.Fatalf("mapped elements = %#v", elements)
	}

	// A prompt that no longer contains the placeholder drops the element.
	if elements := model.composerTextElementsForPromptValue(value, "renamed prompt"); len(elements) != 0 {
		t.Fatalf("stale elements = %#v, want none", elements)
	}
}

// TestSubmittedRequestCarriesComposerTextElements covers the end-to-end submit
// path: a registered mention reaches the submit request as a text element.
func TestSubmittedRequestCarriesComposerTextElements(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 80, Height: 24})
	value := "$imagegen make a chart"
	typeText(t, model, value)
	model.registerComposerElement(value, 0, len("$imagegen"))
	updated, _ := model.Update(key(bubbletea.KeyEnter))
	model = updated.(*Model)
	requests := model.SubmittedRequests()
	if len(requests) != 1 {
		t.Fatalf("submitted requests = %#v", requests)
	}
	elements := requests[0].TextElements
	if len(elements) != 1 || elements[0].Start != 0 || elements[0].End != len("$imagegen") || elements[0].Placeholder != "$imagegen" {
		t.Fatalf("submitted text elements = %#v", elements)
	}
}
