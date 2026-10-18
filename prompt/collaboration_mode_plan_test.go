package prompt

import (
	"os"
	"strings"
	"testing"
)

func TestBuiltinPlanModeInstructionsMatchClientPreset(t *testing.T) {
	// The TUI still owns its copy of the preset. Keep the two byte-for-byte
	// aligned so built-in-preset matching in app-server cannot drift.
	client, err := os.ReadFile("../tui/chatwidget/prompt_for_plan_mode.md")
	if err != nil {
		t.Fatalf("read the client Plan preset: %v", err)
	}
	if BuiltinPlanModeInstructions() != string(client) {
		t.Fatal("app-server Plan preset differs from the TUI client preset")
	}
}

func TestBuiltinPlanModeInstructionsDropOnlyBundledPlanToolSection(t *testing.T) {
	stripped := WithoutUpdatePlanInstructions(BuiltinPlanModeInstructions())
	if strings.Contains(stripped, "## Plan Mode vs update_plan tool") {
		t.Fatalf("bundled plan-tool section survived:\n%s", stripped)
	}
	for _, want := range []string{
		"# Plan Mode (Conversational)",
		"## Execution vs. mutation in Plan Mode",
		"<proposed_plan>",
	} {
		if !strings.Contains(stripped, want) {
			t.Fatalf("stripped preset lost %q:\n%s", want, stripped)
		}
	}
}
