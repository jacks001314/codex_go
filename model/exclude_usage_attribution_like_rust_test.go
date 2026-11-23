package model

import (
	"net/http"
	"testing"

	"codex_go/codexapi"
)

// TestExcludeHeaderUsageAttributionPolicyLikeRust pins Rust #63219: per-item
// usage attribution is excluded by default and retained only when the thread
// asked for it.
func TestExcludeHeaderUsageAttributionPolicyLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		retain bool
		want   string
	}{
		{name: "default excludes attribution", retain: false, want: "tools,usage.attribution"},
		{name: "raw-event thread retains attribution", retain: true, want: "tools"},
	} {
		headers := http.Header{}
		addExcludeHeader(headers, testCase.retain)
		if got := headers.Get(codexapi.ClientCodexExcludeHeader); got != testCase.want {
			t.Fatalf("%s: x-codex-exclude = %q, want %q", testCase.name, got, testCase.want)
		}
	}
}
