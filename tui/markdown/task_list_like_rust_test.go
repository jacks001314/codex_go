package markdown

import (
	"strings"
	"testing"

	"codex_go/utils"
)

func TestTaskListMarkersLikeRust(t *testing.T) {
	out, err := Render("- [x] done\n- [ ] todo\n1. [x] numbered\n> - [x] quoted\n", 60)
	if err != nil {
		t.Fatal(err)
	}
	plain := utils.StripANSI(out)
	for _, want := range []string{"☑ done", "☐ todo", "1. ☑ numbered", "☑ quoted"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("task-list render missing %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "• ") || strings.Contains(plain, "[x]") || strings.Contains(plain, "[ ]") {
		t.Fatalf("task-list render retained pre-Rust markers:\n%s", plain)
	}
}
