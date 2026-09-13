package tea

import (
	"errors"
	"strings"
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	tuiapp "codex_go/tui/app"
)

func recapTestModel(t *testing.T, generate RecapGenerateFunc) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	state.AddMessage(codextui.RoleUser, "do the thing")
	state.AddMessage(codextui.RoleAssistant, "did the thing")
	return NewModel(state, Options{
		Width:             80,
		Height:            24,
		DisablePasteBurst: true,
		OnGenerateRecap:   generate,
	})
}

func TestModelRecapCommandShowsLoadingThenRecap(t *testing.T) {
	var gotPrompt string
	var gotSchema map[string]any
	model := recapTestModel(t, func(threadID string, options RecapThreadOptions, prompt string, schema map[string]any) (string, error) {
		if threadID != "thread-1" {
			t.Fatalf("threadID = %q", threadID)
		}
		gotPrompt = prompt
		gotSchema = schema
		return `{"summary":"Fixed the parser.","next_action":null}`, nil
	})

	typeText(t, model, "/recap")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if !strings.Contains(modelMessageText(model), "Generating conversation recap") {
		t.Fatalf("loading row missing:\n%s", modelMessageText(model))
	}

	runTeaCmd(t, model, cmd)
	if !strings.Contains(gotPrompt, "User: do the thing") || !strings.Contains(gotPrompt, "Assistant: did the thing") {
		t.Fatalf("prompt = %q", gotPrompt)
	}
	if !strings.HasPrefix(gotPrompt, tuiapp.RecapPromptPrefix) {
		t.Fatalf("prompt missing the recap instructions:\n%s", gotPrompt)
	}
	if gotSchema == nil || gotSchema["type"] != "object" {
		t.Fatalf("schema = %#v", gotSchema)
	}
	text := modelMessageText(model)
	if strings.Contains(text, "Generating conversation recap") {
		t.Fatalf("loading row was not cleared:\n%s", text)
	}
	if !strings.Contains(text, "\u21b3 Recap:") || !strings.Contains(text, "Fixed the parser.") {
		t.Fatalf("recap cell missing:\n%s", text)
	}
	if model.recapInFlight {
		t.Fatal("recap should not remain in flight")
	}
}

func TestModelRecapCommandEmptyHistory(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	model := NewModel(state, Options{
		Width:             80,
		Height:            24,
		DisablePasteBurst: true,
		OnGenerateRecap: func(string, RecapThreadOptions, string, map[string]any) (string, error) {
			t.Fatal("recap generation should not run without history")
			return "", nil
		},
	})
	typeText(t, model, "/recap")
	model.Update(key(bubbletea.KeyEnter))
	if !strings.Contains(modelMessageText(model), tuiapp.ManualRecapEmptyHistoryMessage) {
		t.Fatalf("missing empty-history notice:\n%s", modelMessageText(model))
	}
}

func TestModelRecapCommandInProgress(t *testing.T) {
	model := recapTestModel(t, func(string, RecapThreadOptions, string, map[string]any) (string, error) {
		t.Fatal("a second recap should not run while one is in flight")
		return "", nil
	})
	model.recapInFlight = true
	typeText(t, model, "/recap")
	model.Update(key(bubbletea.KeyEnter))
	if !strings.Contains(modelMessageText(model), tuiapp.ManualRecapInProgressMessage) {
		t.Fatalf("missing in-progress notice:\n%s", modelMessageText(model))
	}
}

func TestModelRecapCommandFailureShowsMessage(t *testing.T) {
	model := recapTestModel(t, func(string, RecapThreadOptions, string, map[string]any) (string, error) {
		return "", errors.New("temporary structured turn timed out")
	})
	typeText(t, model, "/recap")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	text := modelMessageText(model)
	if strings.Contains(text, "Generating conversation recap") {
		t.Fatalf("loading row was not cleared:\n%s", text)
	}
	if !strings.Contains(text, tuiapp.ManualRecapFailureMessage) {
		t.Fatalf("missing failure notice:\n%s", text)
	}
}

func TestModelRecapCommandInvalidResponseShowsFailure(t *testing.T) {
	model := recapTestModel(t, func(string, RecapThreadOptions, string, map[string]any) (string, error) {
		return "not json", nil
	})
	typeText(t, model, "/recap")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if !strings.Contains(modelMessageText(model), tuiapp.ManualRecapFailureMessage) {
		t.Fatalf("missing failure notice:\n%s", modelMessageText(model))
	}
}

func TestModelAutomaticRecapRunsWhenUnfocusedDeadlinePasses(t *testing.T) {
	base := time.Now()
	calls := 0
	model := recapTestModel(t, func(string, RecapThreadOptions, string, map[string]any) (string, error) {
		calls++
		return `{"summary":"Auto recap.","next_action":null}`, nil
	})
	now := base
	model.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		model.Update(TurnCompletedMsg{ThreadID: "thread-1"})
	}
	if model.recap.CompletedTurns != 3 || model.recap.TurnRevision != 3 {
		t.Fatalf("recap state = %#v", model.recap)
	}
	if _, cmd := model.Update(bubbletea.BlurMsg{}); cmd == nil {
		t.Fatal("blurring with three completed turns should schedule a recap check")
	}
	if cmd := model.applyRecapCheck(base.Add(tuiapp.RecapDelay - time.Second)); cmd != nil {
		t.Fatal("the automatic recap should wait for the deadline")
	}

	now = base.Add(tuiapp.RecapDelay)
	_, cmd := model.Update(recapCheckMsg{})
	if calls != 0 {
		t.Fatal("generation must run in the returned command, not synchronously")
	}
	runTeaCmd(t, model, cmd)
	if calls != 1 {
		t.Fatalf("recap calls = %d, want 1", calls)
	}
	text := modelMessageText(model)
	if strings.Contains(text, "Generating conversation recap") {
		t.Fatalf("automatic recaps must not show the loading row:\n%s", text)
	}
	if !strings.Contains(text, "Auto recap.") {
		t.Fatalf("missing automatic recap:\n%s", text)
	}
}

func TestModelAutomaticRecapCancelledByFocusGain(t *testing.T) {
	base := time.Now()
	model := recapTestModel(t, func(string, RecapThreadOptions, string, map[string]any) (string, error) {
		t.Fatal("regaining focus must cancel the scheduled recap")
		return "", nil
	})
	now := base
	model.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		model.Update(TurnCompletedMsg{ThreadID: "thread-1"})
	}
	model.Update(bubbletea.BlurMsg{})
	model.Update(bubbletea.FocusMsg{})
	now = base.Add(tuiapp.RecapDelay)
	if cmd := model.applyRecapCheck(now); cmd != nil {
		t.Fatal("a pending check after focus gain must be inert")
	}
}

func TestModelAutomaticRecapFailureSchedulesOneRetryPerRevision(t *testing.T) {
	model := recapTestModel(t, func(string, RecapThreadOptions, string, map[string]any) (string, error) {
		return "", errors.New("temporary structured turn timed out")
	})
	for i := 0; i < 3; i++ {
		model.Update(TurnCompletedMsg{ThreadID: "thread-1"})
	}
	model.recapInFlight = true
	msg := RecapGeneratedMsg{
		ThreadID:       "thread-1",
		Err:            errors.New("temporary structured turn timed out"),
		Trigger:        RecapTriggerAutomatic,
		TurnRevision:   model.recap.TurnRevision,
		CompletedTurns: model.recap.CompletedTurns,
	}
	if cmd := model.applyRecapGeneratedMsg(msg); cmd == nil {
		t.Fatal("an automatic failure should schedule a retry")
	}
	model.recapInFlight = true
	if cmd := model.applyRecapGeneratedMsg(msg); cmd != nil {
		t.Fatal("a retry must only be scheduled once per turn revision")
	}
}

func TestModelAutoRecapHonorsConfiguredDisable(t *testing.T) {
	base := time.Now()
	disabled := false
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	state.AddMessage(codextui.RoleUser, "hi")
	state.AddMessage(codextui.RoleAssistant, "hello")
	model := NewModel(state, Options{
		Width:     80,
		Height:    24,
		AutoRecap: &disabled,
		OnGenerateRecap: func(string, RecapThreadOptions, string, map[string]any) (string, error) {
			t.Fatal("a disabled automatic recap must not run")
			return "", nil
		},
	})
	if !model.disableAutoRecap {
		t.Fatal("AutoRecap=false should disable scheduled recaps")
	}
	now := base
	model.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		model.Update(TurnCompletedMsg{ThreadID: "thread-1"})
	}
	if _, cmd := model.Update(bubbletea.BlurMsg{}); cmd != nil {
		t.Fatal("a disabled automatic recap must not schedule a check")
	}
	now = base.Add(tuiapp.RecapDelay)
	if cmd := model.applyRecapCheck(now); cmd != nil {
		t.Fatal("a disabled automatic recap must not run at the deadline")
	}
}
