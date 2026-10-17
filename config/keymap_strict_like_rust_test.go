package config

import (
	"os"
	"strings"
	"testing"
)

// Rust #54766: unknown keymap fields and profile TUI settings deserialize
// (unknown fields ignored) and are only rejected by the shared strict-config
// policy, with the ignored path in the message.
func TestStrictConfigReportsUnknownTuiKeymapFieldsLikeRust(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "unknown action",
			body: "[tui.keymap.global]\nbogus_action = \"ctrl-g\"\n",
			want: "unknown configuration field `tui.keymap.global.bogus_action`",
		},
		{
			name: "unknown context",
			body: "[tui.keymap.future_context]\nopen = \"ctrl-g\"\n",
			want: "unknown configuration field `tui.keymap.future_context`",
		},
		{
			name: "action misplaced at keymap root",
			body: "[tui.keymap]\nopen_transcript = \"ctrl-s\"\n",
			want: "unknown configuration field `tui.keymap.open_transcript`",
		},
		{
			name: "unknown profile tui setting",
			body: "profile = \"work\"\n\n[profiles.work.tui]\ntheme = \"dark\"\n",
			want: "unknown configuration field `profiles.work.tui.theme`",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(ConfigPath(dir), []byte(testCase.body), 0o600); err != nil {
				t.Fatalf("WriteFile error = %v", err)
			}
			if _, err := LoadEffectiveWithOptions(dir, nil); err != nil {
				t.Fatalf("non-strict load error = %v, want unknown fields ignored", err)
			}
			_, err := LoadEffectiveWithOptions(dir, &EffectiveOptions{StrictConfig: true})
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("strict load error = %v, want %q", err, testCase.want)
			}
		})
	}
}

func TestStrictConfigAcceptsKnownTuiKeymapFieldsLikeRust(t *testing.T) {
	dir := t.TempDir()
	body := "profile = \"work\"\n\n" +
		"[tui.keymap.global]\nopen_external_editor = \"ctrl-e\"\n\n" +
		"[tui.keymap.vim_text_object]\ndouble_quote = \"shift-quote\"\n\n" +
		"[profiles.work.tui]\nsession_picker_view = \"fuzzy\"\n"
	if err := os.WriteFile(ConfigPath(dir), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	if _, err := LoadEffectiveWithOptions(dir, &EffectiveOptions{StrictConfig: true}); err != nil {
		t.Fatalf("strict load error = %v", err)
	}
}
