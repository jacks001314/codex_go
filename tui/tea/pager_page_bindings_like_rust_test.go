package tea

import (
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

func pagerPageBindingsConfig(t *testing.T, pageUp, pageDown, halfPageUp, halfPageDown, scrollUp, scrollDown []string) *codextui.KeymapConfig {
	t.Helper()
	config := codextui.NewKeymapConfig()
	for action, bindings := range map[string][]string{
		"page_up":        pageUp,
		"page_down":      pageDown,
		"half_page_up":   halfPageUp,
		"half_page_down": halfPageDown,
		"scroll_up":      scrollUp,
		"scroll_down":    scrollDown,
	} {
		if err := config.Set("pager", action, bindings); err != nil {
			t.Fatalf("set pager.%s: %v", action, err)
		}
	}
	return config
}

// Rust #50396: PageUp/PageDown follow pager scroll bindings instead of always
// moving a full page.
func TestTranscriptPageKeysFollowPagerBindingsLikeRust(t *testing.T) {
	model := NewModel(navKeyPriorityState(30), Options{Width: 60, Height: 10})
	page := max(model.transcript.Height-1, 1)
	half := (model.transcript.Height + 1) / 2
	tests := []struct {
		name   string
		config *codextui.KeymapConfig
		delta  int
	}{
		{name: "default page", config: codextui.NewKeymapConfig(), delta: page},
		{
			name: "half page",
			config: pagerPageBindingsConfig(t,
				[]string{}, []string{}, []string{"page-up"}, []string{"page-down"},
				[]string{"up", "k"}, []string{"down", "j"},
			),
			delta: half,
		},
		{
			name: "single row",
			config: pagerPageBindingsConfig(t,
				[]string{}, []string{}, []string{"ctrl-u"}, []string{"ctrl-d"},
				[]string{"page-up"}, []string{"page-down"},
			),
			delta: 1,
		},
		{
			name: "unbound",
			config: pagerPageBindingsConfig(t,
				[]string{}, []string{}, []string{"ctrl-u"}, []string{"ctrl-d"},
				[]string{"up", "k"}, []string{"down", "j"},
			),
			delta: 0,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			model := NewModel(navKeyPriorityState(30), Options{
				Width:        60,
				Height:       10,
				KeymapConfig: testCase.config,
			})
			bottom := model.transcript.YOffset
			if bottom <= 0 {
				t.Fatalf("initial transcript offset = %d, want scrollable bottom", bottom)
			}
			model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyPgUp})
			if got, want := model.transcript.YOffset, max(bottom-testCase.delta, 0); got != want {
				t.Fatalf("PageUp offset = %d, want %d (bottom %d, delta %d)", got, want, bottom, testCase.delta)
			}
			model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyPgDown})
			if got := model.transcript.YOffset; got != bottom {
				t.Fatalf("PageDown offset = %d, want bottom %d", got, bottom)
			}
		})
	}
}
