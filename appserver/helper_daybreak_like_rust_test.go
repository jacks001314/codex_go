package appserver

import (
	"strings"
	"testing"

	modelpkg "codex_go/model"
)

func helperDaybreakModel(slug string, cyber ...modelpkg.CyberAccessProgram) *modelpkg.ModelInfo {
	return &modelpkg.ModelInfo{Slug: slug, AvailableAccessPrograms: &modelpkg.ModelAccessPrograms{Cyber: cyber}}
}

// TestHelperDaybreakSelectProgramLikeRust pins Rust #60773 select_program: a
// saved choice selects only an advertised program, and an incompatible choice
// fails instead of silently falling back.
func TestHelperDaybreakSelectProgramLikeRust(t *testing.T) {
	both := helperDaybreakModel("gpt-both", modelpkg.CyberAccessProgramStandard, modelpkg.CyberAccessProgramDaybreakBlue, modelpkg.CyberAccessProgramDaybreakRed)
	if got, err := helperDaybreakSelectProgram(both, modelpkg.SavedDaybreakChoiceOn); err != nil || got != string(modelpkg.CyberAccessProgramDaybreakBlue) {
		t.Fatalf("on = %q, %v", got, err)
	}
	if got, err := helperDaybreakSelectProgram(both, modelpkg.SavedDaybreakChoiceOff); err != nil || got != string(modelpkg.CyberAccessProgramStandard) {
		t.Fatalf("off = %q, %v", got, err)
	}
	if got, err := helperDaybreakSelectProgram(both, modelpkg.SavedDaybreakChoiceUnset); err != nil || got != "" {
		t.Fatalf("unset = %q, %v", got, err)
	}

	daybreakOnly := helperDaybreakModel("gpt-daybreak", modelpkg.CyberAccessProgramDaybreakRed)
	if got, err := helperDaybreakSelectProgram(daybreakOnly, modelpkg.SavedDaybreakChoiceOff); err == nil || !strings.Contains(err.Error(), "does not advertise Standard") || got != "" {
		t.Fatalf("off without standard = %q, %v", got, err)
	}
	if got, err := helperDaybreakSelectProgram(daybreakOnly, modelpkg.SavedDaybreakChoiceUnset); err != nil || got != "" {
		t.Fatalf("unset without standard = %q, %v", got, err)
	}

	standardOnly := helperDaybreakModel("gpt-standard", modelpkg.CyberAccessProgramStandard)
	if got, err := helperDaybreakSelectProgram(standardOnly, modelpkg.SavedDaybreakChoiceOn); err == nil || !strings.Contains(err.Error(), "does not advertise Daybreak support") || got != "" {
		t.Fatalf("on without daybreak = %q, %v", got, err)
	}

	unknown := &modelpkg.ModelInfo{Slug: "gpt-unknown"}
	if got, err := helperDaybreakSelectProgram(unknown, modelpkg.SavedDaybreakChoiceOff); err != nil || got != "" {
		t.Fatalf("missing metadata = %q, %v", got, err)
	}
}

// TestHelperDaybreakAdaptInheritedLikeRust pins Rust #60773: an inherited
// Daybreak Blue/Red selection is adapted to the child model, and dropped when
// that model cannot use Daybreak.
func TestHelperDaybreakAdaptInheritedLikeRust(t *testing.T) {
	both := helperDaybreakModel("gpt-both", modelpkg.CyberAccessProgramStandard, modelpkg.CyberAccessProgramDaybreakBlue, modelpkg.CyberAccessProgramDaybreakRed)
	if got := helperDaybreakAdaptInherited(both, string(modelpkg.CyberAccessProgramDaybreakRed)); got != string(modelpkg.CyberAccessProgramDaybreakRed) {
		t.Fatalf("advertised program = %q", got)
	}
	redOnly := helperDaybreakModel("gpt-red", modelpkg.CyberAccessProgramDaybreakRed)
	if got := helperDaybreakAdaptInherited(redOnly, string(modelpkg.CyberAccessProgramDaybreakBlue)); got != string(modelpkg.CyberAccessProgramDaybreakRed) {
		t.Fatalf("adapted program = %q", got)
	}
	standardOnly := helperDaybreakModel("gpt-standard", modelpkg.CyberAccessProgramStandard)
	if got := helperDaybreakAdaptInherited(standardOnly, string(modelpkg.CyberAccessProgramDaybreakBlue)); got != "" {
		t.Fatalf("unusable inherited program = %q", got)
	}
	if got := helperDaybreakAdaptInherited(standardOnly, string(modelpkg.CyberAccessProgramStandard)); got != string(modelpkg.CyberAccessProgramStandard) {
		t.Fatalf("standard program = %q", got)
	}
	unknown := &modelpkg.ModelInfo{Slug: "gpt-unknown"}
	if got := helperDaybreakAdaptInherited(unknown, string(modelpkg.CyberAccessProgramDaybreakBlue)); got != string(modelpkg.CyberAccessProgramDaybreakBlue) {
		t.Fatalf("missing metadata program = %q", got)
	}
}

// TestHelperDaybreakIntentStoreLikeRust keeps the spawn-time choice and owner
// available for later helper turns.
func TestHelperDaybreakIntentStoreLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	if _, ok := router.helperDaybreakIntentFor("child"); ok {
		t.Fatal("new router reported a helper intent")
	}
	intent := helperDaybreakIntent{choice: modelpkg.SavedDaybreakChoiceOn, accountID: "account-1", inherited: string(modelpkg.CyberAccessProgramDaybreakBlue)}
	router.recordHelperDaybreakIntent("child", intent)
	got, ok := router.helperDaybreakIntentFor("child")
	if !ok || got.choice != modelpkg.SavedDaybreakChoiceOn || got.accountID != "account-1" || got.inherited != string(modelpkg.CyberAccessProgramDaybreakBlue) {
		t.Fatalf("intent = %#v, %v", got, ok)
	}
	if !got.recorded {
		t.Fatal("recorded intent lost its marker")
	}
	if _, ok := router.helperDaybreakIntentFor("other"); ok {
		t.Fatal("intent leaked to another helper")
	}
}

// TestHelperDaybreakAccountChangeMessageLikeRust pins the rejection text used
// when a helper's saved choice belongs to a different account.
func TestHelperDaybreakAccountChangeMessageLikeRust(t *testing.T) {
	if !strings.Contains(helperDaybreakAccountChanged, "has changed. Start a new subagent.") {
		t.Fatalf("account change message = %q", helperDaybreakAccountChanged)
	}
}
