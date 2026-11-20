package tea

import (
	"strings"
	"testing"
)

// Rust #61742: side-chat tabs and chooser labels carry the Cloud marker while
// the underlying chat identity stays unchanged.
func TestSidebarCloudLabelsLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	model.cloudBackends[model.backendID] = true
	openSideForTest(t, model)

	strip := model.renderSidebarTabStrip(60)
	// The Cloud marker shares the fixed cell with the title and the close glyph,
	// so the title is clipped at the cell edge exactly like Rust's strip.
	if !strings.Contains(strip, "☁️") || !strings.Contains(strip, "Side ch") {
		t.Fatalf("cloud tab strip = %q", strip)
	}

	model.sidebarChoices = []SideChatChoice{{ThreadID: "saved-1", Name: "Investigate rendering"}}
	model.sidebarChooserParent = "parent-a"
	chooser := model.renderSidebarChooser(50, 20)
	if !strings.Contains(chooser, "☁️ Investigate rendering") {
		t.Fatalf("cloud chooser = %q", chooser)
	}

	delete(model.cloudBackends, model.backendID)
	if strip := model.renderSidebarTabStrip(60); strings.Contains(strip, "☁️") {
		t.Fatalf("local tab strip gained a marker: %q", strip)
	}
	if chooser := model.renderSidebarChooser(50, 20); strings.Contains(chooser, "☁️") {
		t.Fatalf("local chooser gained a marker: %q", chooser)
	}
}
