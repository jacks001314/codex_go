package bottompane

import "testing"

func TestBuildActionRequiredTitleTextMatchesRust(t *testing.T) {
	got := BuildActionRequiredTitleTextFromValues(
		ActionRequiredPreviewPrefix,
		[]string{"activity", "project-name", "run-state", "git-branch"},
		[]string{"run-state"},
		map[string]string{
			"project-name": "repo",
			"run-state":    "Working",
			"git-branch":   "main",
		},
	)
	want := "[ ! ] Action Required | repo | main"
	if got != want {
		t.Fatalf("title = %q, want %q", got, want)
	}
}

func TestBuildActionRequiredTitleTextPreservesRustEmptyPrefixAndValues(t *testing.T) {
	got := BuildActionRequiredTitleText(
		"",
		[]string{"spinner", "model", "missing", "thread-title"},
		nil,
		func(item string) (string, bool) {
			switch item {
			case "model":
				return "", true
			case "thread-title":
				return "Implement TUI", true
			default:
				return "", false
			}
		},
	)
	want := " |  | Implement TUI"
	if got != want {
		t.Fatalf("title = %q, want %q", got, want)
	}
}

func TestActionRequiredTitleTextCompatibility(t *testing.T) {
	if got := (ActionRequiredTitle{Title: "Custom title"}).Text(); got != "Custom title" {
		t.Fatalf("custom title = %q", got)
	}
	if got := (ActionRequiredTitle{}).Text(); got != "Action required" {
		t.Fatalf("default title = %q", got)
	}
	if got := (ActionRequiredTitle{Count: 2}).Text(); got != "Action required" {
		t.Fatalf("count title = %q", got)
	}
}
