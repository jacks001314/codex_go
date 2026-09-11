package memories

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/config"
)

// Mirrors the Rust memory_read v2 suite: the selected memory version contributes
// only its own summary to the model request.
func TestBuildMemoryToolDeveloperInstructionsForVersionSelectsRoot(t *testing.T) {
	home := t.TempDir()
	writeSummary := func(root string, body string) {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", root, err)
		}
		if err := os.WriteFile(filepath.Join(root, MemorySummaryFilename), []byte(body), 0o600); err != nil {
			t.Fatalf("write summary: %v", err)
		}
	}
	writeSummary(RootForVersion(home, config.MemoryVersionV1), "legacy-only-marker")
	writeSummary(RootForVersion(home, config.MemoryVersionV2), "v2-only-marker")

	v1 := BuildMemoryToolDeveloperInstructionsForVersion(home, config.MemoryVersionV1)
	if !strings.Contains(v1, "legacy-only-marker") || strings.Contains(v1, "v2-only-marker") {
		t.Fatalf("v1 instructions = %q", v1)
	}
	v2 := BuildMemoryToolDeveloperInstructionsForVersion(home, config.MemoryVersionV2)
	if !strings.Contains(v2, "v2-only-marker") || strings.Contains(v2, "legacy-only-marker") {
		t.Fatalf("v2 instructions = %q", v2)
	}
	if !strings.Contains(v2, "Memory citations:") || !strings.Contains(v2, "<oai-mem-citation>") {
		t.Fatalf("v2 instructions did not use the dedicated v2 template: %q", v2)
	}
	if strings.Contains(v1, "Memory citations:") {
		t.Fatalf("v1 instructions must not use the v2 template: %q", v1)
	}
	if got := BuildMemoryToolDeveloperInstructions(home); !strings.Contains(got, "legacy-only-marker") {
		t.Fatalf("default instructions = %q, want v1", got)
	}
}

func TestStageOneExtractionContractForVersion(t *testing.T) {
	v1System := StageOneSystemPromptForVersion(config.MemoryVersionV1)
	v2System := StageOneSystemPromptForVersion(config.MemoryVersionV2)
	if v1System == v2System {
		t.Fatal("v1 and v2 extraction system prompts must differ")
	}
	if !strings.Contains(v2System, "Return exactly one JSON object with string fields `rollout_summary` and") ||
		!strings.Contains(v2System, "`rollout_slug`, and no other fields or prose.") {
		t.Fatalf("v2 system prompt = %q", v2System)
	}

	v2Schema := StageOneOutputSchemaForVersion(config.MemoryVersionV2)
	properties, _ := v2Schema["properties"].(map[string]any)
	if _, hasRawMemory := properties["raw_memory"]; hasRawMemory {
		t.Fatalf("v2 schema must not include raw_memory: %#v", v2Schema)
	}
	if required, _ := v2Schema["required"].([]any); len(required) != 2 {
		t.Fatalf("v2 schema required = %#v", v2Schema["required"])
	}
	v1Schema := StageOneOutputSchemaForVersion(config.MemoryVersionV1)
	if _, hasRawMemory := v1Schema["properties"].(map[string]any)["raw_memory"]; !hasRawMemory {
		t.Fatalf("v1 schema must include raw_memory: %#v", v1Schema)
	}
}

func TestDecodeStageOneOutputForVersion(t *testing.T) {
	slug := "rollout-slug"
	v1, err := DecodeStageOneOutputForVersion(`{"raw_memory":"raw","rollout_summary":"summary","rollout_slug":"rollout-slug"}`, config.MemoryVersionV1)
	if err != nil {
		t.Fatalf("v1 decode error = %v", err)
	}
	if v1.RawMemory != "raw" || v1.RolloutSummary != "summary" || v1.RolloutSlug == nil || *v1.RolloutSlug != slug {
		t.Fatalf("v1 decode = %#v", v1)
	}

	v2, err := DecodeStageOneOutputForVersion(`{"rollout_summary":"summary","rollout_slug":"rollout-slug"}`, config.MemoryVersionV2)
	if err != nil {
		t.Fatalf("v2 decode error = %v", err)
	}
	if v2.RawMemory != "" || v2.RolloutSummary != "summary" || v2.RolloutSlug == nil || *v2.RolloutSlug != slug {
		t.Fatalf("v2 decode = %#v", v2)
	}

	// v2 rejects the v1-only raw_memory field and a null slug.
	if _, err := DecodeStageOneOutputForVersion(`{"raw_memory":"raw","rollout_summary":"summary","rollout_slug":"slug"}`, config.MemoryVersionV2); err == nil {
		t.Fatal("v2 decode must reject raw_memory")
	}
	if _, err := DecodeStageOneOutputForVersion(`{"rollout_summary":"summary","rollout_slug":null}`, config.MemoryVersionV2); err == nil {
		t.Fatal("v2 decode must reject a null rollout_slug")
	}

	// Redaction runs before the 9,000-byte v2 truncation.
	secret := "sk-" + strings.Repeat("a", 24)
	long := strings.Repeat("context ", 1_500) + secret
	decoded, err := DecodeStageOneOutputForVersion(`{"rollout_summary":"`+long+`","rollout_slug":"slug"}`, config.MemoryVersionV2)
	if err != nil {
		t.Fatalf("v2 long decode error = %v", err)
	}
	// Go's truncation helper appends a marker after the byte budget, so allow a
	// small margin while proving the summary is bounded.
	if len(decoded.RolloutSummary) > 9200 {
		t.Fatalf("v2 summary length = %d, want a bounded summary", len(decoded.RolloutSummary))
	}
	if strings.Contains(decoded.RolloutSummary, secret) {
		t.Fatal("v2 summary must be redacted")
	}
}

func TestBuildConsolidationPromptForVersionSelectsTemplate(t *testing.T) {
	root := t.TempDir()
	v1 := BuildConsolidationPromptForVersion(root, config.MemoryVersionV1)
	if !strings.Contains(v1, "## Memory Writing Agent: Phase 2 (Consolidation)") ||
		strings.Contains(v1, "Consolidate the supplied rollout summaries") {
		t.Fatalf("v1 consolidation prompt = %q", v1)
	}
	v2 := BuildConsolidationPromptForVersion(root, config.MemoryVersionV2)
	if !strings.Contains(v2, "Consolidate the supplied rollout summaries") ||
		strings.Contains(v2, "## Memory Writing Agent: Phase 2 (Consolidation)") {
		t.Fatalf("v2 consolidation prompt = %q", v2)
	}
	if !strings.Contains(v2, root) {
		t.Fatalf("v2 consolidation prompt did not substitute memory_root: %q", v2)
	}
	if got := BuildConsolidationPrompt(root); !strings.Contains(got, "## Memory Writing Agent: Phase 2 (Consolidation)") {
		t.Fatalf("default consolidation prompt = %q, want v1", got)
	}
}

func TestValidateConsolidationArtifactsForVersion(t *testing.T) {
	root := t.TempDir()
	summaryPath := filepath.Join(root, MemorySummaryFilename)
	if err := os.WriteFile(summaryPath, []byte(validV2Summary()), 0o600); err != nil {
		t.Fatalf("write summary: %v", err)
	}
	// v2 does not require MEMORY.md or raw_memories.md.
	if err := ValidateConsolidationArtifactsForVersion(root, config.MemoryVersionV2); err != nil {
		t.Fatalf("v2 validation error = %v", err)
	}
	// v1 still requires the MEMORY.md handbook.
	if err := ValidateConsolidationArtifactsForVersion(root, config.MemoryVersionV1); err == nil {
		t.Fatal("v1 validation must require MEMORY.md")
	}
	// Missing section and oversized summary are rejected for v2.
	if err := os.WriteFile(summaryPath, []byte(strings.Replace(validV2Summary(), "## General Tips\n", "", 1)), 0o600); err != nil {
		t.Fatalf("write summary: %v", err)
	}
	if err := ValidateConsolidationArtifactsForVersion(root, config.MemoryVersionV2); err == nil ||
		!strings.Contains(err.Error(), "missing ## General Tips") {
		t.Fatalf("missing-heading error = %v", err)
	}
	if err := os.WriteFile(summaryPath, []byte(validV2Summary()+strings.Repeat("x", maxV2SummaryBytes)), 0o600); err != nil {
		t.Fatalf("write summary: %v", err)
	}
	if err := ValidateConsolidationArtifactsForVersion(root, config.MemoryVersionV2); err == nil ||
		!strings.Contains(err.Error(), "under 10000 UTF-8 bytes") {
		t.Fatalf("oversized error = %v", err)
	}
}

// The embedded v2 template is byte-pinned to Rust's
// ext/memories/templates/memories/read_path_v2.md (#43813). The Rust checkout
// may be CRLF-converted by git on Windows; the canonical content is LF.
func TestMemoryV2TemplateMatchesRust(t *testing.T) {
	root := ""
	for _, candidate := range []string{
		filepath.Join("..", "..", "git", "codex", "codex-rs"),
		filepath.Join("..", "..", "..", "git", "codex", "codex-rs"),
	} {
		if _, err := os.Stat(filepath.Join(candidate, "Cargo.toml")); err == nil {
			root = candidate
			break
		}
	}
	if root == "" {
		t.Skip("Rust checkout not available")
	}
	for _, tc := range []struct {
		name string
		path string
		got  string
	}{
		{
			name: "read_path_v2.md",
			path: filepath.Join(root, "ext", "memories", "templates", "memories", "read_path_v2.md"),
			got:  memoryToolDeveloperInstructionsV2Template,
		},
		{
			name: "consolidation_v2.md",
			path: filepath.Join(root, "memories", "write", "templates", "memories", "consolidation_v2.md"),
			got:  consolidationV2PromptTemplate,
		},
		{
			name: "stage_one_system_v2.md",
			path: filepath.Join(root, "memories", "write", "templates", "memories", "stage_one_system_v2.md"),
			got:  stageOneSystemV2Prompt,
		},
		{
			name: "stage_one_input_v2.md",
			path: filepath.Join(root, "memories", "write", "templates", "memories", "stage_one_input_v2.md"),
			got:  stageOneInputV2Template,
		},
	} {
		data, err := os.ReadFile(tc.path)
		if err != nil {
			t.Skipf("Rust template %s unavailable: %v", tc.name, err)
		}
		want := strings.ReplaceAll(string(data), "\r\n", "\n")
		if tc.got != want {
			t.Fatalf("embedded %s differs from Rust:\n--- go ---\n%s\n--- rust ---\n%s", tc.name, tc.got, want)
		}
	}
}

func TestRootForVersion(t *testing.T) {
	home := t.TempDir()
	if got := RootForVersion(home, config.MemoryVersionV1); got != filepath.Join(home, "memories") {
		t.Fatalf("v1 root = %q", got)
	}
	if got := RootForVersion(home, config.MemoryVersionV2); got != filepath.Join(home, "memories_v2") {
		t.Fatalf("v2 root = %q", got)
	}
}

// Both memory roots feed shell memory-usage telemetry (Rust #43797).
func TestUsageKindFromPathRecognizesBothMemoryRoots(t *testing.T) {
	for path, want := range map[string]UsageKind{
		"/home/user/.codex/memories/memory_summary.md":         UsageKindMemorySummary,
		"/home/user/.codex/memories_v2/memory_summary.md":      UsageKindMemorySummary,
		`C:\Users\user\.codex\memories_v2\raw_memories.md`:     UsageKindRawMemories,
		"/home/user/.codex/memories_v2/rollout_summaries/x.md": UsageKindRolloutSummaries,
	} {
		kind, ok := UsageKindFromPath(path)
		if !ok || kind != want {
			t.Fatalf("UsageKindFromPath(%q) = %q,%v want %q", path, kind, ok, want)
		}
	}
}
