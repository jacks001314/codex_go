package context

import (
	"testing"

	"codex_go/eventmap"
)

// typedImageFragment mirrors an extension-owned world-state contribution that
// carries non-text content (Rust #54776).
type typedImageFragment struct {
	*SimpleFragment
	items []eventmap.ContentItem
}

func (f *typedImageFragment) BodyItems() []eventmap.ContentItem { return f.items }

// TestRenderAppliesMarkersOnlyToInputTextLikeRust covers Rust #54776's render()
// rule: markers wrap only the InputText body, while other content passes through
// unchanged.
func TestRenderAppliesMarkersOnlyToInputTextLikeRust(t *testing.T) {
	image := eventmap.ContentItem{Kind: eventmap.ContentInputImage, ImageURL: "data:image/png;base64,AAA", Detail: "high"}
	fragment := &typedImageFragment{
		SimpleFragment: NewSimpleFragmentWithKind(RoleUser, "<world_state>", "</world_state>", "ignored", "world_state"),
		items: []eventmap.ContentItem{
			{Kind: eventmap.ContentInputText, Text: "state snapshot"},
			image,
		},
	}
	rendered := Render(fragment)
	if rendered == nil {
		t.Fatal("Render() = nil")
	}
	if len(rendered.ContentItems) != 2 {
		t.Fatalf("ContentItems = %#v, want 2", rendered.ContentItems)
	}
	if got := rendered.ContentItems[0]; got.Kind != eventmap.ContentInputText || got.Text != "<world_state>state snapshot</world_state>" {
		t.Fatalf("text item = %#v", got)
	}
	if got := rendered.ContentItems[1]; got != image {
		t.Fatalf("image item = %#v, want passthrough %#v", got, image)
	}
	parts := ToolOutputParts(rendered.ContentItems)
	if len(parts) != 2 || parts[1]["image_url"] != image.ImageURL || parts[1]["detail"] != "high" {
		t.Fatalf("ToolOutputParts() = %#v", parts)
	}
}

// TestRenderSimpleFragmentStaysSingleInputTextLikeRust keeps the string-only
// fragment path on one InputText item.
func TestRenderSimpleFragmentStaysSingleInputTextLikeRust(t *testing.T) {
	fragment := NewSimpleFragmentWithKind(RoleUser, "", "", "plain body", "generic")
	rendered := Render(fragment)
	if len(rendered.ContentItems) != 1 {
		t.Fatalf("ContentItems = %#v", rendered.ContentItems)
	}
	if got := rendered.ContentItems[0]; got.Kind != eventmap.ContentInputText || got.Text != "plain body" {
		t.Fatalf("item = %#v", got)
	}
}

// TestToolOutputPartsPreservesAudioLikeRust covers the shared content-to-tool
// output conversion for audio content. Rust's InputAudio carries only
// `audio_url`, so the item now supplies it directly instead of overloading the
// image URL field.
func TestToolOutputPartsPreservesAudioLikeRust(t *testing.T) {
	parts := ToolOutputParts([]eventmap.ContentItem{{Kind: eventmap.ContentInputAudio, AudioURL: "file:///tmp/a.wav"}})
	if len(parts) != 1 || parts[0]["type"] != "input_audio" || parts[0]["audio_url"] != "file:///tmp/a.wav" {
		t.Fatalf("ToolOutputParts() = %#v", parts)
	}
}
