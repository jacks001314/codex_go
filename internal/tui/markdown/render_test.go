package markdown

import (
	"strings"
	"testing"
)

func TestRenderMarkdown(t *testing.T) {
	out, err := Render("# Title\n\n- one\n- two", 40)
	if err != nil {
		t.Fatalf("Render returned error: %v", err)
	}
	for _, want := range []string{"Title", "one", "two"} {
		if !strings.Contains(out, want) {
			t.Fatalf("Render output = %q, missing %q", out, want)
		}
	}
}

func TestRenderEmptyMarkdown(t *testing.T) {
	out, err := Render("  ", 40)
	if err != nil {
		t.Fatalf("Render returned error: %v", err)
	}
	if out != "" {
		t.Fatalf("Render empty = %q, want empty", out)
	}
}
