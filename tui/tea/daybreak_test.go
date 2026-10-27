package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
)

func TestModelCyberPolicyErrorRendersDaybreakRefusalCell(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	state.Model = "gpt-5.6-sol"
	state.Provider = "openai"
	state.HasChatGPTAccount = true
	model := NewModel(state, Options{
		Width:             80,
		Height:            24,
		HasChatGPTAccount: true,
		ModelPickerOptions: []codextui.ModelPickerOption{{
			ID:                     "gpt-5.6-sol",
			AvailableCyberPrograms: []string{"standard"},
		}},
	})

	model.Update(CyberPolicyErrorMsg{ThreadID: "thread-1"})

	text := modelRawMessageText(model)
	if !strings.Contains(text, "This content can\u2019t be shown") ||
		!strings.Contains(text, "apply for Daybreak to get broader access") ||
		!strings.Contains(text, "Apply for Daybreak: https://openai.com/form/enterprise-trusted-access-for-cyber/") {
		t.Fatalf("missing apply refusal cell:\n%s", text)
	}
}

func TestModelCyberPolicyErrorDefaultsToApplicationCopy(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	model := NewModel(state, Options{Width: 80, Height: 24})

	model.Update(CyberPolicyErrorMsg{ThreadID: "thread-1"})

	text := modelRawMessageText(model)
	if !strings.Contains(text, "apply for Daybreak to get broader access") {
		t.Fatalf("missing application refusal cell:\n%s", text)
	}
}

func TestModelCyberPolicyNoticeUsesOnlyCurrentModelProgramsLikeRust(t *testing.T) {
	tests := []struct {
		name       string
		model      string
		programs   []string
		wantNotice codextui.DaybreakNotice
		wantText   string
	}{
		{
			name:       "astra standard",
			model:      "gpt-6-astra",
			programs:   []string{"standard"},
			wantNotice: codextui.DaybreakNoticeAstra,
			wantText:   "Daybreak isn\u2019t available for Astra",
		},
		{
			name:       "astra with daybreak",
			model:      "gpt-6-astra",
			programs:   []string{"standard", "daybreak_blue"},
			wantNotice: codextui.DaybreakNoticeDisabled,
			wantText:   "Daybreak is currently off.",
		},
		{
			name:       "sol standard",
			model:      "gpt-5.6-sol",
			programs:   []string{"standard"},
			wantNotice: codextui.DaybreakNoticeApply,
			wantText:   "apply for Daybreak to get broader access",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := codextui.NewState(nil)
			state.SetThreadID("thread-catalog")
			state.Model = tc.model
			state.Provider = "openai"
			state.HasChatGPTAccount = true
			model := NewModel(state, Options{
				Width:             80,
				Height:            24,
				HasChatGPTAccount: true,
				ModelPickerOptions: []codextui.ModelPickerOption{
					{ID: tc.model, AvailableCyberPrograms: tc.programs},
					{ID: "other-model", AvailableCyberPrograms: []string{"daybreak_red"}},
				},
			})
			gotNotice := model.cyberPolicyNotice()
			if gotNotice != tc.wantNotice {
				t.Fatalf("notice = %v, want %v", gotNotice, tc.wantNotice)
			}
			model.Update(CyberPolicyErrorMsg{ThreadID: "thread-catalog"})
			if text := modelRawMessageText(model); !strings.Contains(text, tc.wantText) {
				t.Fatalf("refusal copy missing %q:\n%s", tc.wantText, text)
			}
		})
	}
}

func TestModelCyberPolicyErrorIgnoresOtherThreads(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	model := NewModel(state, Options{Width: 80, Height: 24})

	model.Update(CyberPolicyErrorMsg{ThreadID: "thread-2"})

	if strings.Contains(modelMessageText(model), "This content can\u2019t be shown") {
		t.Fatalf("a background thread's refusal must not enter the active transcript:\n%s", modelRawMessageText(model))
	}
}

// modelRawMessageText joins the unrendered transcript entries, where the
// refusal copy is not wrapped.
func modelRawMessageText(model *Model) string {
	if model == nil || model.State == nil {
		return ""
	}
	texts := make([]string, 0, len(model.State.Messages))
	for _, message := range model.State.Messages {
		texts = append(texts, message.RawText)
	}
	return strings.Join(texts, "\n")
}
