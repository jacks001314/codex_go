package tui

import "testing"

// Rust #61742: the marker is a display-only prefix with its own width so label
// truncation does not overwrite thread names.
func TestCloudThreadLabelPrefixLikeRust(t *testing.T) {
	if got := ThreadLabelPrefix(false); got != "" {
		t.Fatalf("plain prefix = %q", got)
	}
	if got := ThreadLabelPrefix(true); got != CloudThreadLabelPrefix {
		t.Fatalf("cloud prefix = %q", got)
	}
	if got := ThreadLabelPrefixWidth(true); got != 2 {
		t.Fatalf("cloud prefix width = %d, want 2", got)
	}
	if got := DecorateThreadLabel("Fix cache", true); got != "☁️ Fix cache" {
		t.Fatalf("decorated label = %q", got)
	}
	if got := DecorateThreadLabel("Fix cache", false); got != "Fix cache" {
		t.Fatalf("plain label = %q", got)
	}
}
