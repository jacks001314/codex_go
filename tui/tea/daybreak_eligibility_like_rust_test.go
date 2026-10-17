package tea

import (
	"testing"

	codextui "codex_go/tui"
)

func TestDaybreakAccountEligibilityLikeRust(t *testing.T) {
	cases := []struct {
		name       string
		features   map[string]bool
		chatgpt    bool
		display    string
		provider   string
		wantEnable bool
		wantElig   bool
	}{
		{
			name:       "cli feature off",
			features:   map[string]bool{"cli_daybreak": false, "api_key_cyber_access_programs": true},
			chatgpt:    true,
			provider:   "openai",
			wantEnable: false,
			wantElig:   false,
		},
		{
			name:       "chatgpt account",
			features:   map[string]bool{"cli_daybreak": true},
			chatgpt:    true,
			provider:   "openai",
			wantEnable: true,
			wantElig:   true,
		},
		{
			name:       "api key gate off",
			features:   map[string]bool{"cli_daybreak": true, "api_key_cyber_access_programs": false},
			display:    "API key configured (run codex login to use ChatGPT)",
			provider:   "openai",
			wantEnable: false,
			wantElig:   false,
		},
		{
			name:       "api key gate on",
			features:   map[string]bool{"cli_daybreak": true, "api_key_cyber_access_programs": true},
			display:    "API key configured (run codex login to use ChatGPT)",
			provider:   "openai",
			wantEnable: true,
			wantElig:   true,
		},
		{
			name:       "api key non-openai provider",
			features:   map[string]bool{"cli_daybreak": true, "api_key_cyber_access_programs": true},
			display:    "API key configured (run codex login to use ChatGPT)",
			provider:   "azure",
			wantEnable: true,
			wantElig:   false,
		},
		{
			name:       "unknown account preserves preference",
			features:   map[string]bool{"cli_daybreak": true},
			provider:   "openai",
			wantEnable: true,
			wantElig:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := codextui.NewState(nil)
			state.Provider = tc.provider
			state.AccountDisplay = tc.display
			state.HasChatGPTAccount = tc.chatgpt
			model := NewModel(state, Options{
				Width:             80,
				Height:            24,
				FeatureSettings:   tc.features,
				HasChatGPTAccount: tc.chatgpt,
				DaybreakDefault:   true,
			})
			if got := model.daybreakFeatureEnabled(); got != tc.wantEnable {
				t.Fatalf("daybreakFeatureEnabled = %v, want %v", got, tc.wantEnable)
			}
			if got := model.daybreakAccountEligible(); got != tc.wantElig {
				t.Fatalf("daybreakAccountEligible = %v, want %v", got, tc.wantElig)
			}
			if got := model.daybreakEnabled; got != tc.wantEnable {
				t.Fatalf("daybreakEnabled = %v, want %v", got, tc.wantEnable)
			}
		})
	}
}
