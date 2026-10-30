package state

import (
	"fmt"
	"strings"
	"testing"
)

func TestGuardianContextProfileDefaultsLikeRust(t *testing.T) {
	syncProfile := SynchronousContextProfile()
	if syncProfile.Target != ContextTargetSync ||
		syncProfile.Transcript.EntryLimits.MessageTokens != 5_000 ||
		syncProfile.Transcript.EntryLimits.ToolTokens != 1_000 ||
		syncProfile.Retention.MaxMessageTranscriptTokens != 20_000 ||
		syncProfile.Retention.MaxToolTranscriptTokens != 10_000 ||
		syncProfile.Retention.MaxRecentNonUserEntries != 40 ||
		syncProfile.IncludeImages {
		t.Fatalf("synchronous profile = %#v", syncProfile)
	}
	asyncProfile := AsynchronousContextProfile()
	if asyncProfile.Target != ContextTargetAsync ||
		asyncProfile.Transcript.EntryLimits.MessageTokens != 2_000 ||
		asyncProfile.Transcript.EntryLimits.ToolTokens != 1_000 ||
		asyncProfile.Retention.MaxMessageTranscriptTokens != 10_000 ||
		asyncProfile.Retention.MaxToolTranscriptTokens != 10_000 ||
		asyncProfile.Retention.MaxRecentNonUserEntries != 40 ||
		!asyncProfile.IncludeImages {
		t.Fatalf("asynchronous profile = %#v", asyncProfile)
	}
	if !syncProfile.Transcript.Options.IncludeToolCalls ||
		!syncProfile.Transcript.Options.IncludeToolOutputs ||
		syncProfile.Transcript.Options.IncludeReasoning {
		t.Fatalf("default transcript options = %#v", syncProfile.Transcript.Options)
	}
}

func TestManualApprovalDeveloperPrefixLikeRust(t *testing.T) {
	const want = "The user has manually approved a specific action that was previously `Rejected`."
	if ManualApprovalDeveloperPrefix != want {
		t.Fatalf("ManualApprovalDeveloperPrefix = %q, want %q", ManualApprovalDeveloperPrefix, want)
	}
	if DeniedActionApprovalPrefix != ManualApprovalDeveloperPrefix {
		t.Fatalf("DeniedActionApprovalPrefix = %q, want the shared marker", DeniedActionApprovalPrefix)
	}
}

// Mirrors Rust guardian-context profile_tests.rs:
// profiles_preserve_distinct_retention_and_original_numbering.
func TestGuardianContextProfilesPreserveDistinctRetentionAndOriginalNumberingLikeRust(t *testing.T) {
	entries := []TranscriptEvidenceEntry{
		{Kind: GuardianTranscriptUser, Role: "user", Text: "inspect only", OriginalBytes: len("inspect only")},
		{Kind: GuardianTranscriptProtectedMessage, Role: "assistant", Text: "proposed action", OriginalBytes: len("proposed action")},
		{Kind: GuardianTranscriptMessage, Role: "assistant", Text: "working", OriginalBytes: len("working")},
	}
	synchronous := SynchronousContextProfile()
	synchronous.Retention.MaxRecentNonUserEntries = 1
	asynchronous := AsynchronousContextProfile()
	asynchronous.Retention.MaxRecentNonUserEntries = 1

	sync := synchronous.RenderTranscript(entries, 7)
	if got, want := strings.Join(sync.Items, "\n"), "[8] user: inspect only\n[10] assistant: working"; got != want {
		t.Fatalf("sync items = %q, want %q", got, want)
	}
	if sync.OmissionNote != GuardianTranscriptOmissionNotice {
		t.Fatalf("sync omission = %q, want %q", sync.OmissionNote, GuardianTranscriptOmissionNotice)
	}

	async := asynchronous.RenderTranscript(entries, 0)
	if got, want := strings.Join(async.Items, "\n"), "[1] user: inspect only\n\n[2] assistant: proposed action\n"; got != want {
		t.Fatalf("async items = %q, want %q", got, want)
	}
	if async.OmissionNote != "" {
		t.Fatalf("async omission = %q, want none", async.OmissionNote)
	}
	if len(async.Truncations) != 1 {
		t.Fatalf("async truncations = %#v, want one observation", async.Truncations)
	}
	observation := async.Truncations[0]
	if observation.Component != "transcript_message" ||
		observation.OriginalBytes != len("working") ||
		observation.RetainedBytes != 0 {
		t.Fatalf("async truncation = %#v", observation)
	}
}

// Mirrors Rust #44570: user messages stay complete through profile retention
// even when their aggregate cost exceeds the message allowance. The complete
// request budget, not transcript collection, decides whether to shorten them.
func TestGuardianSynchronousProfileKeepsCompleteUserInstructionsLikeRust(t *testing.T) {
	entries := []TranscriptEvidenceEntry{
		{Kind: GuardianTranscriptUser, Text: strings.Repeat("x", 400), OriginalBytes: 400},
		{Kind: GuardianTranscriptUser, Text: strings.Repeat("y", 400), OriginalBytes: 400},
		{Kind: GuardianTranscriptUser, Text: "latest", OriginalBytes: len("latest")},
	}
	profile := SynchronousContextProfile()
	profile.Retention.MaxMessageTranscriptTokens = 50
	rendered := profile.RenderTranscript(entries, 0)
	if len(rendered.Indices) != len(entries) {
		t.Fatalf("retained indices = %#v, want every complete user instruction", rendered.Indices)
	}
	if rendered.OmissionNote != "" {
		t.Fatalf("omission note = %q, want none before complete-request budgeting", rendered.OmissionNote)
	}
	if rendered.Items[0] != fmt.Sprintf("[1] user: %s", entries[0].Text) {
		t.Fatalf("first user instruction was changed: %q", rendered.Items[0])
	}
}

// Mirrors Rust #44570: a manual developer approval is authorization evidence,
// so neither retention limits nor source order may drop it.
func TestGuardianProfilesKeepManualApprovalsCompleteLikeRust(t *testing.T) {
	approval := strings.Repeat("approved action ", 500)
	entries := []TranscriptEvidenceEntry{
		{Kind: GuardianTranscriptUser, Role: "user", Text: "edit scratch files", OriginalBytes: len("edit scratch files")},
		{Kind: GuardianTranscriptManualApproval, Role: "developer", Text: approval, OriginalBytes: len(approval)},
		{Kind: GuardianTranscriptUser, Role: "user", Text: "only scratch files", OriginalBytes: len("only scratch files")},
	}
	for _, profile := range []ContextProfile{SynchronousContextProfile(), AsynchronousContextProfile()} {
		profile.Retention.MaxMessageTranscriptTokens = 1
		profile.Retention.MaxRecentNonUserEntries = 0
		rendered := profile.RenderTranscript(entries, 0)
		if got, want := len(rendered.Indices), len(entries); got != want {
			t.Fatalf("target %v retained indices = %#v, want all authorization evidence", profile.Target, rendered.Indices)
		}
		if !strings.Contains(rendered.Items[1], approval) {
			t.Fatalf("target %v manual approval was shortened during retention", profile.Target)
		}
		if !strings.Contains(rendered.Items[2], "only scratch files") {
			t.Fatalf("target %v later restriction was dropped: %#v", profile.Target, rendered.Items)
		}
	}
}

func TestGuardianAsynchronousProfileEvictsProtectedMessagesInChunksLikeRust(t *testing.T) {
	entries := make([]TranscriptEvidenceEntry, 0, 4)
	for _, text := range []string{"first", "second", "third", "fourth"} {
		entries = append(entries, TranscriptEvidenceEntry{
			Kind:          GuardianTranscriptProtectedMessage,
			Role:          "assistant",
			Text:          text,
			OriginalBytes: len(text),
			Tokens:        4,
		})
	}
	profile := AsynchronousContextProfile()
	profile.Retention.MaxMessageTranscriptTokens = 10
	profile.Retention.MaxToolTranscriptTokens = 10
	profile.Retention.MaxRecentNonUserEntries = 40
	rendered := profile.RenderTranscript(entries, 0)
	if len(rendered.Indices) != 2 || rendered.Indices[0] != 2 || rendered.Indices[1] != 3 {
		t.Fatalf("retained indices = %#v, want the newest two protected messages", rendered.Indices)
	}
}

func TestGuardianAsynchronousProfileReservesRecentToolsLikeRust(t *testing.T) {
	entries := make([]TranscriptEvidenceEntry, 0, 12)
	for index := 0; index < 6; index++ {
		text := "protected"
		entries = append(entries, TranscriptEvidenceEntry{
			Kind:          GuardianTranscriptProtectedMessage,
			Role:          "assistant",
			Text:          text,
			OriginalBytes: len(text),
			Tokens:        1,
		})
	}
	for index := 0; index < 6; index++ {
		text := "tool"
		entries = append(entries, TranscriptEvidenceEntry{
			Kind:          GuardianTranscriptTool,
			Role:          "tool result",
			Text:          text,
			OriginalBytes: len(text),
			Tokens:        1,
		})
	}
	profile := AsynchronousContextProfile()
	profile.Retention.MaxRecentNonUserEntries = 6
	rendered := profile.RenderTranscript(entries, 0)
	want := []int{5, 7, 8, 9, 10, 11}
	if len(rendered.Indices) != len(want) {
		t.Fatalf("retained indices = %#v, want %#v", rendered.Indices, want)
	}
	for index := range want {
		if rendered.Indices[index] != want[index] {
			t.Fatalf("retained indices = %#v, want %#v", rendered.Indices, want)
		}
	}
}

func TestGuardianPromptRendersTranscriptOmissionNotice(t *testing.T) {
	prompt, err := BuildPromptWithOptions(Action{
		Type:    "command",
		Command: "ls",
		CWD:     "/tmp",
	}, []string{"user: inspect only"}, BuildPromptOptions{
		TranscriptOmissionNote: GuardianTranscriptOmissionNotice,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "Recent transcript:\n- user: inspect only\n"+GuardianTranscriptOmissionNotice) {
		t.Fatalf("prompt missing omission notice:\n%s", prompt)
	}
}
