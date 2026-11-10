package appserver

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	codexctx "codex_go/context"
	"codex_go/session"
	"codex_go/state"
	"codex_go/utils"
)

// TestGuardianCollectionPreservesCapturedOriginalsLikeRust covers Rust #60498:
// collection keeps the captured text, and the caller's budgeting mode decides
// whether a request entry cap applies.
func TestGuardianCollectionPreservesCapturedOriginalsLikeRust(t *testing.T) {
	router, _, _, store := newAdditionalContextTestRouter(t, "")
	threadID := session.ThreadID("123e4567-e89b-42d3-a456-426614174298")
	now := fixedTime()
	// Collection trims surrounding whitespace, so the captured body has none.
	oversized := strings.Repeat("tool evidence ", 3999) + "tool evidence"
	record := &session.Record{
		ID: threadID, SessionID: string(threadID), CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{Source: "cli", ModelProvider: "openai"},
		Items: []session.Item{
			{
				ID: "request", Type: "message", Role: "user", Text: "inspect the repository",
				Metadata: map[string]any{"turnId": "turn-1"}, CreatedAt: now,
			},
			{
				ID: "output", Type: "function_call_output", CallID: "call-1", Name: "exec_command",
				Text: oversized, Metadata: map[string]any{"turnId": "turn-1"}, CreatedAt: now,
			},
		},
	}
	if err := store.Create(record); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}
	profile := state.SynchronousContextProfile()
	collected := router.guardianCollectTranscriptWithProfile(string(threadID), profile)
	if len(collected) != 2 {
		t.Fatalf("collected entries = %d, want 2", len(collected))
	}
	var captured *guardianCollectedTranscriptEntry
	for index := range collected {
		if strings.HasPrefix(collected[index].entry.Text, "tool evidence") {
			captured = &collected[index]
		}
	}
	if captured == nil {
		t.Fatalf("collected transcript lost the tool output: %#v", collected)
	}
	if captured.entry.Text != oversized {
		t.Fatalf("collection stored %d bytes, want the captured %d", len(captured.entry.Text), len(oversized))
	}
	if captured.evidence.OriginalBytes != len(oversized) {
		t.Fatalf("collected original bytes = %d, want %d", captured.evidence.OriginalBytes, len(oversized))
	}

	// Fixed preparation applies the request entry cap.
	fixed := prepareGuardianTranscriptSelection(collected, profile, guardianTranscriptBudgetingFixed)
	var fixedEntry *state.GuardianTranscriptEntry
	for index := range fixed.Entries {
		if strings.HasPrefix(fixed.Entries[index].Text, "tool evidence") {
			fixedEntry = &fixed.Entries[index]
		}
	}
	if fixedEntry == nil {
		t.Fatalf("fixed preparation dropped the tool output: %#v", fixed.Entries)
	}
	if len(fixedEntry.Text) >= len(oversized) {
		t.Fatalf("fixed preparation kept %d bytes of a %d byte entry", len(fixedEntry.Text), len(oversized))
	}

	// Elastic preparation defers the cap to the configured allocation.
	elastic := prepareGuardianTranscriptSelection(collected, profile, guardianTranscriptBudgetingElastic)
	if len(elastic.Entries) != len(collected) {
		t.Fatalf("elastic entries = %d, want all %d captured entries", len(elastic.Entries), len(collected))
	}
	if elastic.OmissionNote != "" {
		t.Fatalf("elastic preparation reported an omission before allocation: %q", elastic.OmissionNote)
	}
	var elasticEntry *state.GuardianTranscriptEntry
	for index := range elastic.Entries {
		if strings.HasPrefix(elastic.Entries[index].Text, "tool evidence") {
			elasticEntry = &elastic.Entries[index]
		}
	}
	if elasticEntry == nil || elasticEntry.Text != oversized {
		t.Fatalf("elastic preparation lost the captured entry body")
	}
}

// TestGuardianFixedPreparationOmitsOversizedNativeLikeRust covers the fixed
// request policy for native evidence: ciphertext is delivered whole until the
// entry cap forces an omission notice at the same position (Rust #60498).
func TestGuardianFixedPreparationOmitsOversizedNativeLikeRust(t *testing.T) {
	profile := state.SynchronousContextProfile()
	native := json.RawMessage(strings.Repeat("c", utils.ApproxBytesForTokens(profile.Transcript.EntryLimits.MessageTokens)+64))
	collected := []guardianCollectedTranscriptEntry{{
		entry:      state.GuardianTranscriptEntry{Author: "assistant", Native: native},
		evidence:   state.TranscriptEvidenceEntry{Kind: state.GuardianTranscriptProtectedMessage, Role: "assistant", Tokens: utils.ApproxTokenCount(string(native))},
		nativeKind: guardianNativeEntryProtectedMessage,
		nativeSize: len(native),
		capKind:    guardianTranscriptCapNone,
	}}

	fixed := prepareGuardianTranscriptSelection(collected, profile, guardianTranscriptBudgetingFixed)
	if len(fixed.Entries) != 1 {
		t.Fatalf("fixed entries = %d, want 1", len(fixed.Entries))
	}
	if fixed.Entries[0].Text != guardianTranscriptOmissionNotice || len(fixed.Entries[0].Native) != 0 {
		t.Fatalf("fixed preparation kept oversized native evidence: %#v", fixed.Entries[0])
	}

	elastic := prepareGuardianTranscriptSelection(collected, profile, guardianTranscriptBudgetingElastic)
	if len(elastic.Entries) != 1 || len(elastic.Entries[0].Native) != len(native) {
		t.Fatalf("elastic preparation dropped oversized native evidence")
	}
	if len(collected[0].entry.Native) != len(native) {
		t.Fatalf("preparation mutated the collected evidence")
	}
}

// TestGuardianHeartbeatCompressionRunsAtPreparationLikeRust covers the deferred
// heartbeat compression (Rust #60498): repeated scheduler instructions become a
// reference during preparation, the collected evidence keeps its originals, and
// a second preparation of the same snapshot yields the same reference numbers.
func TestGuardianHeartbeatCompressionRunsAtPreparationLikeRust(t *testing.T) {
	profile := state.SynchronousContextProfile()
	const instructions = "re-run the nightly build"
	scheduled := func() guardianCollectedTranscriptEntry {
		text := "Scheduled automation a1 ran at t0. Instructions: " + instructions
		return guardianCollectedTranscriptEntry{
			entry: state.GuardianTranscriptEntry{Author: "user", Label: "", LineRole: "user", Text: text},
			evidence: state.TranscriptEvidenceEntry{
				Kind:      state.GuardianTranscriptUser,
				Role:      "user",
				Text:      text,
				Scheduled: true,
				Heartbeat: &state.TranscriptHeartbeat{AutomationID: "a1", Timestamp: "t0", Instructions: instructions},
			},
			capKind: guardianTranscriptCapNone,
		}
	}
	collected := []guardianCollectedTranscriptEntry{{
		entry:    state.GuardianTranscriptEntry{Author: "user", LineRole: "user", Text: "hello"},
		evidence: state.TranscriptEvidenceEntry{Kind: state.GuardianTranscriptUser, Role: "user", Text: "hello"},
		capKind:  guardianTranscriptCapNone,
	}, scheduled(), scheduled()}

	first := prepareGuardianTranscriptSelection(collected, profile, guardianTranscriptBudgetingFixed)
	if len(first.Entries) != 3 {
		t.Fatalf("prepared entries = %d, want 3", len(first.Entries))
	}
	if !strings.Contains(first.Entries[1].Text, instructions) {
		t.Fatalf("first heartbeat lost its instructions: %q", first.Entries[1].Text)
	}
	reference := first.Entries[2].Text
	if !strings.Contains(reference, "transcript entry [2]") || strings.Contains(reference, instructions) {
		t.Fatalf("repeated heartbeat was not compressed against entry [2]: %q", reference)
	}

	// Collection keeps the captured scheduler evidence intact.
	if !collected[2].evidence.Scheduled || collected[2].evidence.Heartbeat == nil {
		t.Fatalf("preparation mutated the collected scheduler evidence")
	}
	if !strings.Contains(collected[2].evidence.Text, instructions) {
		t.Fatalf("preparation rewrote the collected scheduler text: %q", collected[2].evidence.Text)
	}

	// A second preparation of the same snapshot keeps absolute reference numbers.
	second := prepareGuardianTranscriptSelection(collected, profile, guardianTranscriptBudgetingElastic)
	if len(second.Entries) != 3 || !strings.Contains(second.Entries[2].Text, "transcript entry [2]") {
		t.Fatalf("elastic preparation changed heartbeat numbering: %#v", second.Entries)
	}
}

// TestGuardianElasticPreparationDefersEntryCapsForBothProfilesLikeRust covers
// Rust #60498: elastic preparation is supported for both reviewer profiles and
// keeps every captured entry so the configured allocation decides delivery.
func TestGuardianElasticPreparationDefersEntryCapsForBothProfilesLikeRust(t *testing.T) {
	evidence := make([]state.TranscriptEvidenceEntry, 0, 60)
	for index := 0; index < 60; index++ {
		evidence = append(evidence, state.TranscriptEvidenceEntry{
			Kind: state.GuardianTranscriptTool,
			Role: "tool exec_command result",
			Text: fmt.Sprintf("%d %s", index, strings.Repeat("tool evidence ", 20)),
		})
	}
	for _, profile := range []state.ContextProfile{state.SynchronousContextProfile(), state.AsynchronousContextProfile()} {
		elastic := profile.RenderTranscriptForBudgeting(evidence, 0, codexctx.GuardianContextBudgetingElastic)
		if len(elastic.Indices) != len(evidence) || len(elastic.Items) != len(evidence) {
			t.Fatalf("target %d elastic preparation kept %d of %d entries", profile.Target, len(elastic.Indices), len(evidence))
		}
		if elastic.OmissionNote != "" {
			t.Fatalf("target %d elastic preparation reported %q", profile.Target, elastic.OmissionNote)
		}
		fixed := profile.RenderTranscriptForBudgeting(evidence, 0, codexctx.GuardianContextBudgetingFixed)
		if len(fixed.Indices) >= len(evidence) {
			t.Fatalf("target %d fixed preparation kept every entry: %d", profile.Target, len(fixed.Indices))
		}
	}
}
