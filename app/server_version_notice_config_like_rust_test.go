package app

import "testing"

// Rust #43698: `tui.show_server_version_notice` defaults to enabled and an
// explicit false disables it.
func TestShowServerVersionNoticeConfigLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		values map[string]any
		want   bool
	}{
		{name: "default", values: nil, want: true},
		{name: "enabled", values: map[string]any{"tui": map[string]any{"show_server_version_notice": true}}, want: true},
		{name: "disabled", values: map[string]any{"tui": map[string]any{"show_server_version_notice": false}}, want: false},
		{name: "malformed keeps default", values: map[string]any{"tui": map[string]any{"show_server_version_notice": "no"}}, want: true},
	} {
		got := interactiveShowServerVersionNotice(testCase.values)
		if got == nil || *got != testCase.want {
			t.Fatalf("%s: notice setting = %#v, want %v", testCase.name, got, testCase.want)
		}
	}
}
