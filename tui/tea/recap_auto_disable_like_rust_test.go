package tea

import (
	"errors"
	"strings"
	"testing"
)

// Mirrors Rust #42101: disabling `tui.auto_recap` discards a pending automatic
// result without retrying it, while `/recap` stays available.
func TestModelDiscardsPendingAutomaticRecapWhenDisabled(t *testing.T) {
	model := recapTestModel(t, func(string, RecapThreadOptions, string, map[string]any) (string, error) {
		return `{"summary":"Automatic recap that arrived late.","next_action":null}`, nil
	})
	model.disableAutoRecap = true
	model.recapInFlight = true
	msg := RecapGeneratedMsg{
		ThreadID:       "thread-1",
		Response:       `{"summary":"Automatic recap that arrived late.","next_action":null}`,
		Trigger:        RecapTriggerAutomatic,
		TurnRevision:   model.recap.TurnRevision,
		CompletedTurns: model.recap.CompletedTurns,
	}
	if cmd := model.applyRecapGeneratedMsg(msg); cmd != nil {
		t.Fatal("a discarded automatic recap must not schedule a retry")
	}
	if model.recapInFlight {
		t.Fatal("the pending automatic request was not cleared")
	}
	if text := modelMessageText(model); strings.Contains(text, "Automatic recap that arrived late.") {
		t.Fatalf("disabled automatic recap was rendered:\n%s", text)
	}

	enabled := recapTestModel(t, func(string, RecapThreadOptions, string, map[string]any) (string, error) {
		return `{"summary":"Automatic recap that arrived late.","next_action":null}`, nil
	})
	enabled.recapInFlight = true
	enabled.applyRecapGeneratedMsg(RecapGeneratedMsg{
		ThreadID:       "thread-1",
		Response:       msg.Response,
		Trigger:        RecapTriggerAutomatic,
		TurnRevision:   enabled.recap.TurnRevision,
		CompletedTurns: enabled.recap.CompletedTurns,
	})
	if text := modelMessageText(enabled); !strings.Contains(text, "Automatic recap that arrived late.") {
		t.Fatalf("enabled automatic recap was not rendered:\n%s", text)
	}

	failing := recapTestModel(t, func(string, RecapThreadOptions, string, map[string]any) (string, error) {
		return "", errors.New("temporary failure")
	})
	failing.disableAutoRecap = true
	failing.recapInFlight = true
	if cmd := failing.applyRecapGeneratedMsg(RecapGeneratedMsg{
		ThreadID:       "thread-1",
		Err:            errors.New("temporary failure"),
		Trigger:        RecapTriggerAutomatic,
		TurnRevision:   failing.recap.TurnRevision,
		CompletedTurns: failing.recap.CompletedTurns,
	}); cmd != nil {
		t.Fatal("a discarded automatic failure must not schedule a retry")
	}
	if failing.recapInFlight {
		t.Fatal("the failing automatic request was not cleared")
	}
}

// Mirrors Rust #42101: the opt-out suppresses automatic scheduling only; a
// manual `/recap` still runs and renders.
func TestModelManualRecapRunsWhileAutomaticRecapDisabled(t *testing.T) {
	var generated int
	model := recapTestModel(t, func(string, RecapThreadOptions, string, map[string]any) (string, error) {
		generated++
		return `{"summary":"Manual recap summary.","next_action":null}`, nil
	})
	model.disableAutoRecap = true

	cmd := model.applyRecapCommand()
	if cmd == nil {
		t.Fatal("manual recap must remain available while automatic recaps are disabled")
	}
	runTeaCmd(t, model, cmd)
	if generated != 1 {
		t.Fatalf("manual recap generations = %d, want 1", generated)
	}
	if text := modelMessageText(model); !strings.Contains(text, "Manual recap summary.") {
		t.Fatalf("manual recap was not rendered:\n%s", text)
	}
}
