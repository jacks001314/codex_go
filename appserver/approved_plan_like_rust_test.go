package appserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPrepareApprovedPlanReferencesReadableAttachmentLikeRust mirrors Rust
// #60763: when the app-server copy matches the markdown byte for byte, the
// first prompt points at that file and nothing is injected.
func TestPrepareApprovedPlanReferencesReadableAttachmentLikeRust(t *testing.T) {
	dir := t.TempDir()
	plan := "# Plan\n\n1. Do the thing.\n"
	path := filepath.Join(dir, "plan.md")
	if err := os.WriteFile(path, []byte(plan), 0o600); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	prompt, items, err := prepareApprovedPlanPayload(plan, &path)
	if err != nil {
		t.Fatalf("prepareApprovedPlanPayload: %v", err)
	}
	if prompt != "Read the approved plan at "+path+" and implement it." {
		t.Fatalf("prompt = %q", prompt)
	}
	if len(items) != 0 {
		t.Fatalf("items = %#v, want none", items)
	}
}

// TestPrepareApprovedPlanFallsBackToContextLikeRust checks a mismatched or
// unreadable attachment: the plan travels as agent.proposed_plan outputs and
// the prompt asks the model to implement it.
func TestPrepareApprovedPlanFallsBackToContextLikeRust(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.md")
	if err := os.WriteFile(path, []byte("different"), 0o600); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	prompt, items, err := prepareApprovedPlanPayload("# Plan\n", &path)
	if err != nil {
		t.Fatalf("prepareApprovedPlanPayload: %v", err)
	}
	if prompt != "Implement the plan." {
		t.Fatalf("prompt = %q", prompt)
	}
	if len(items) != 1 || items[0]["namespace"] != "agent" || items[0]["name"] != "proposed_plan" || items[0]["output"] != "# Plan\n" {
		t.Fatalf("items = %#v", items)
	}
}

// TestPrepareApprovedPlanRejectsBadInputLikeRust pins the three Rust
// rejections: empty plan, relative attachment path and an oversized plan.
func TestPrepareApprovedPlanRejectsBadInputLikeRust(t *testing.T) {
	if _, _, err := prepareApprovedPlanPayload("   ", nil); err != errApprovedPlanEmpty {
		t.Fatalf("empty plan error = %v", err)
	}
	relative := "plan.md"
	if _, _, err := prepareApprovedPlanPayload("# Plan", &relative); err != errApprovedPlanPathBad {
		t.Fatalf("relative path error = %v", err)
	}
	tooLarge := strings.Repeat("x", approvedPlanFallbackTokens*approvedPlanBytesPerToken+1)
	if _, _, err := prepareApprovedPlanPayload(tooLarge, nil); err != errApprovedPlanTooBig {
		t.Fatalf("oversized plan error = %v", err)
	}
}
