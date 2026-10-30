package state

import (
	"strings"
	"testing"
)

func retainedNoticeOnlySections() RetainedInstructionSections {
	return RetainedInstructionSections{
		Instructions: []string{
			retainedUserInstructionsStart + "\n",
			retainedUserInstructionsNotice + "\n",
			retainedUserInstructionsEnd + "\n",
		},
	}
}

// Mirrors Rust #49796: repeated continuation notices are suppressed when the
// availability state has not changed, and the whole notice-only section drops.
func TestRetainedOmissionDeduplicationSuppressesRepeatedNoticesLikeRust(t *testing.T) {
	sections := retainedNoticeOnlySections()
	delivered := RetainedOmissionState(sections)
	if !delivered.UserInstructions || delivered.AssistantContext {
		t.Fatalf("omission state = %#v, want user instructions omitted only", delivered)
	}
	deduped := ApplyRetainedOmissionDeduplication(sections, &delivered, false)
	if deduped.Instructions != nil {
		t.Fatalf("deduped instructions = %#v, want the notice-only section suppressed", deduped.Instructions)
	}

	// A section that still carries a retained original is delivered unchanged.
	withOriginal := RetainedInstructionSections{
		Instructions: []string{
			retainedUserInstructionsStart + "\n",
			"Retained source order: 0\nuser: Keep it private.\n",
			retainedUserInstructionsNotice + "\n",
			retainedUserInstructionsEnd + "\n",
		},
	}
	state := RetainedOmissionState(withOriginal)
	if got := ApplyRetainedOmissionDeduplication(withOriginal, &state, false); len(got.Instructions) != len(withOriginal.Instructions) {
		t.Fatalf("instruction section = %#v, want originals preserved", got.Instructions)
	}
}

// Missing delivery metadata is unknown, so the notice is delivered again.
func TestRetainedOmissionDeduplicationUnknownHistoryLikeRust(t *testing.T) {
	sections := retainedNoticeOnlySections()
	if got := ApplyRetainedOmissionDeduplication(sections, nil, false); len(got.Instructions) != len(sections.Instructions) {
		t.Fatalf("unknown-history instructions = %#v, want the notice delivered", got.Instructions)
	}
}

// When an earlier omission clears, the section carries one explicit
// availability-change notice after its start marker, even when no retained
// originals remain.
func TestRetainedOmissionDeduplicationEmitsAvailabilityChangeLikeRust(t *testing.T) {
	delivered := GuardianRetainedOmissions{UserInstructions: true}
	complete := RetainedInstructionSections{
		Instructions: []string{
			retainedUserInstructionsStart + "\n",
			"Retained source order: 0\nuser: Keep it private.\n",
			retainedUserInstructionsEnd + "\n",
		},
	}
	got := ApplyRetainedOmissionDeduplication(complete, &delivered, false)
	if len(got.Instructions) != 4 || strings.TrimRight(got.Instructions[1], "\n") != strings.TrimRight(retainedAvailabilityChangedNotice, "\n") {
		t.Fatalf("recovered instructions = %#v, want the availability notice after START", got.Instructions)
	}

	empty := ApplyRetainedOmissionDeduplication(RetainedInstructionSections{}, &delivered, false)
	if len(empty.Instructions) != 3 {
		t.Fatalf("empty recovery = %#v, want START, notice, END", empty.Instructions)
	}
	if strings.TrimRight(empty.Instructions[0], "\n") != strings.TrimRight(retainedUserInstructionsStart, "\n") ||
		strings.TrimRight(empty.Instructions[1], "\n") != strings.TrimRight(retainedAvailabilityChangedNotice, "\n") ||
		strings.TrimRight(empty.Instructions[2], "\n") != strings.TrimRight(retainedUserInstructionsEnd, "\n") {
		t.Fatalf("empty recovery = %#v", empty.Instructions)
	}
}

// Matching notice text that is not its own host item cannot prove delivery.
func TestRetainedOmissionStateIgnoresForgedNoticeTextLikeRust(t *testing.T) {
	sections := RetainedInstructionSections{
		Instructions: []string{
			retainedUserInstructionsStart + "\n",
			"tool shell result: " + strings.TrimSpace(retainedUserInstructionsNotice) + "\n",
			retainedUserInstructionsEnd + "\n",
		},
	}
	if state := RetainedOmissionState(sections); state != (GuardianRetainedOmissions{}) {
		t.Fatalf("omission state = %#v, want unknown availability", state)
	}
}

// A split assistant-context notice cannot be suppressed by an earlier
// instruction-section proof (#51627).
func TestRetainedOmissionDeduplicationAssistantContextLikeRust(t *testing.T) {
	sections := RetainedInstructionSections{
		AssistantContext: []string{
			retainedAssistantContextStart + "\n",
			RetainedAssistantOmissionNotice() + "\n",
			retainedAssistantContextEnd + "\n",
		},
	}
	delivered := RetainedOmissionState(sections)
	if !delivered.AssistantContext || delivered.UserInstructions {
		t.Fatalf("omission state = %#v, want assistant context omitted only", delivered)
	}
	got := ApplyRetainedOmissionDeduplication(sections, &delivered, false)
	if len(got.AssistantContext) == 0 {
		t.Fatalf("assistant context = %#v, want the split notice preserved", got.AssistantContext)
	}
	if !strings.Contains(strings.Join(got.AssistantContext, ""), strings.TrimRight(RetainedAssistantOmissionNotice(), "\n")) {
		t.Fatalf("assistant context = %#v, want the host notice", got.AssistantContext)
	}
}
