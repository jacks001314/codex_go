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
	model := NewModel(state, Options{
		Width:  80,
		Height: 24,
		OnDaybreakNotice: func(model string) codextui.DaybreakNotice {
			if model != "gpt-5.6-sol" {
				t.Fatalf("model = %q", model)
			}
			return codextui.DaybreakNoticeApply
		},
	})

	model.Update(CyberPolicyErrorMsg{ThreadID: "thread-1"})

	text := modelRawMessageText(model)
	if !strings.Contains(text, "This content can\u2019t be shown") ||
		!strings.Contains(text, "apply for Daybreak to get broader access") ||
		!strings.Contains(text, "Apply for Daybreak: https://openai.com/form/enterprise-trusted-access-for-cyber/") {
		t.Fatalf("missing apply refusal cell:\n%s", text)
	}
}

func TestModelCyberPolicyErrorDefaultsToLimitedCopy(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	model := NewModel(state, Options{Width: 80, Height: 24})

	model.Update(CyberPolicyErrorMsg{ThreadID: "thread-1"})

	text := modelRawMessageText(model)
	// Pending or failed Daybreak discovery uses the neutral copy, which never
	// offers the Daybreak application.
	if !strings.Contains(text, "We take extra care with some cybersecurity requests.") {
		t.Fatalf("missing limited refusal cell:\n%s", text)
	}
	if strings.Contains(text, "Apply for Daybreak:") {
		t.Fatalf("the limited copy must not offer the Daybreak application:\n%s", text)
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
