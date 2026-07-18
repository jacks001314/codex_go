package prompt

import "testing"

func TestPreparePrefersConfigPrompt(t *testing.T) {
	request := "request"
	got := PrepareRealtime(&RealtimeRequestPrompt{Set: true, Value: &request}, "config")
	if got != "config" {
		t.Fatalf("Prepare() = %q", got)
	}
}

func TestPrepareUsesRequestPrompt(t *testing.T) {
	request := "request"
	got := PrepareRealtime(&RealtimeRequestPrompt{Set: true, Value: &request}, "")
	if got != "request" {
		t.Fatalf("Prepare() = %q", got)
	}
}

func TestPreparePreservesNullRequestPrompt(t *testing.T) {
	got := PrepareRealtime(&RealtimeRequestPrompt{Set: true}, "")
	if got != "" {
		t.Fatalf("Prepare() = %q", got)
	}
}

func TestPrepareRendersDefault(t *testing.T) {
	got := PrepareRealtime(nil, "")
	if !IsDefaultBackendPrompt(got) {
		t.Fatalf("default prompt not rendered: %q", got)
	}
}
