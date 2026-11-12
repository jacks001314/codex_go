package state

import "testing"

// TestGuardianElasticSelectedExecSectionLikeRust covers Rust #60914: the
// selected exec output priority allocates to its own section while ordinary
// tool output keeps the tool section.
func TestGuardianElasticSelectedExecSectionLikeRust(t *testing.T) {
	section, err := guardianElasticAllocationSection("conversation_transcript", GuardianElasticOptional(GuardianElasticPrioritySelectedExecResult))
	if err != nil {
		t.Fatalf("guardianElasticAllocationSection() error = %v", err)
	}
	if section != GuardianElasticSectionTranscriptSelectedExecResults {
		t.Fatalf("selected exec section = %q", section)
	}
	for retention, want := range map[GuardianElasticRetention]GuardianElasticSection{
		GuardianElasticOptional(GuardianElasticPriorityTool):       GuardianElasticSectionTranscriptTools,
		GuardianElasticOptional(GuardianElasticPriorityCommentary): GuardianElasticSectionTranscriptMessages,
	} {
		got, err := guardianElasticAllocationSection("conversation_transcript", retention)
		if err != nil || got != want {
			t.Fatalf("section for %q = %q, %v, want %q", retention.Priority, got, err, want)
		}
	}
}

// TestGuardianTranscriptEntrySelectedExecRetentionLikeRust keeps a selected
// exec output out of the ordinary tool pool even though its role reads "tool".
func TestGuardianTranscriptEntrySelectedExecRetentionLikeRust(t *testing.T) {
	entry := GuardianTranscriptEntry{Author: "tool", Label: "code", LineRole: "tool code result", Optional: true, SelectedExecResult: true}
	retention := entry.Retention()
	if !retention.Optional || retention.Priority != GuardianElasticPrioritySelectedExecResult {
		t.Fatalf("retention = %#v", retention)
	}
	entry.SelectedExecResult = false
	if retention := entry.Retention(); retention.Priority != GuardianElasticPriorityTool {
		t.Fatalf("ordinary tool retention = %#v", retention)
	}
	if retention := (GuardianTranscriptEntry{SelectedExecResult: true}).Retention(); retention.Optional {
		t.Fatalf("required evidence became optional: %#v", retention)
	}
}

// TestGuardianElasticSelectedExecSectionParseLikeRust accepts the canonical
// spelled name parsed from configuration.
func TestGuardianElasticSelectedExecSectionParseLikeRust(t *testing.T) {
	section, err := ParseGuardianElasticSection("transcript_selected_exec_results")
	if err != nil || section != GuardianElasticSectionTranscriptSelectedExecResults {
		t.Fatalf("ParseGuardianElasticSection() = %q, %v", section, err)
	}
}
