package appserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/realtime"
	"codex_go/session"
)

func TestRealtimeCurrentThreadSectionMatchesRustNewestFirst(t *testing.T) {
	items := []session.Item{
		{Role: "user", Text: "user turn 1"},
		{Role: "assistant", Text: "assistant turn 1"},
		{Role: "user", Text: "<environment_context>ignored</environment_context>"},
		{Role: "user", Text: "user turn 2"},
		{Role: "assistant", Text: "assistant turn 2"},
	}
	want := `Most recent user/assistant turns from this exact thread. Use them for continuity when responding.

### Latest turn
User:
user turn 2

Assistant:
assistant turn 2

### Previous turn 1
User:
user turn 1

Assistant:
assistant turn 1`
	if got := buildRealtimeCurrentThreadSection(items); got != want {
		t.Fatalf("current thread section:\n%s\nwant:\n%s", got, want)
	}
}

func TestRealtimeContextTruncationPreservesHeadTailWithinBudget(t *testing.T) {
	text := "turn-start " + strings.Repeat("middle filler ", 500) + " turn-end"
	got := truncateRealtimeTextToTokenBudget(text, realtimeTurnBudget)
	if !strings.Contains(got, "turn-start") || !strings.Contains(got, "turn-end") || !strings.Contains(got, "tokens truncated") {
		t.Fatalf("truncated turn did not preserve Rust shape: %q", got)
	}
	if tokens := realtimeApproxTokenCount(got); tokens > realtimeTurnBudget {
		t.Fatalf("truncated turn uses %d tokens, budget %d", tokens, realtimeTurnBudget)
	}
}

func TestRealtimeWorkspaceSectionUsesBoundedFilteredTree(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "repo", "workspace")
	userRoot := filepath.Join(root, "home")
	for _, path := range []string{filepath.Join(root, "repo", ".git"), filepath.Join(cwd, "docs"), filepath.Join(cwd, "node_modules"), filepath.Join(userRoot, "code")} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
	}
	if err := os.WriteFile(filepath.Join(cwd, "README.md"), []byte("hello"), 0o600); err != nil {
		t.Fatalf("write README: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userRoot, ".hidden"), []byte("secret"), 0o600); err != nil {
		t.Fatalf("write hidden file: %v", err)
	}

	section := buildRealtimeWorkspaceSection(cwd, userRoot)
	for _, expected := range []string{"Git root: " + filepath.Join(root, "repo"), "Working directory tree:", "- docs/", "- README.md", "User root tree:", "- code/"} {
		if !strings.Contains(section, expected) {
			t.Fatalf("workspace section missing %q:\n%s", expected, section)
		}
	}
	if strings.Contains(section, "node_modules") || strings.Contains(section, ".hidden") || strings.Contains(section, ".git") {
		t.Fatalf("workspace section contains filtered entry:\n%s", section)
	}
}

func TestRealtimeStartupContextGeneratedUnlessExplicitlyOverridden(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "workspace")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "README.md"), []byte("hello"), 0o600); err != nil {
		t.Fatalf("write README: %v", err)
	}
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	record := &session.Record{
		ID:        "thread-current",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{CWD: cwd},
		Items: []session.Item{
			{Role: "user", Text: "Investigate realtime startup context", CreatedAt: now},
			{Role: "assistant", Text: "I will inspect it.", CreatedAt: now},
		},
	}
	store := session.NewStore(filepath.Join(root, "sessions"))
	if err := store.Save(record); err != nil {
		t.Fatalf("save current thread: %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	t.Cleanup(func() { _ = router.Close() })

	generated := router.realtimeInstructions(&config.Config{Values: map[string]any{}}, record, &realtime.StartParams{})
	for _, expected := range []string{"<startup_context>", "## Current Thread", "Investigate realtime startup context", "## Recent Work", "## Machine / Workspace Map", "## Notes", "</startup_context>"} {
		if !strings.Contains(generated, expected) {
			t.Fatalf("generated realtime instructions missing %q", expected)
		}
	}

	overridden := router.realtimeInstructions(&config.Config{Values: map[string]any{"experimental_realtime_ws_startup_context": "explicit startup"}}, record, &realtime.StartParams{})
	if !strings.Contains(overridden, "explicit startup") || strings.Contains(overridden, "<startup_context>") {
		t.Fatalf("explicit startup override = %q", overridden)
	}

	emptyOverride := router.realtimeInstructions(&config.Config{Values: map[string]any{"experimental_realtime_ws_startup_context": ""}}, record, &realtime.StartParams{})
	if strings.Contains(emptyOverride, "<startup_context>") {
		t.Fatalf("explicit empty startup context generated fallback: %q", emptyOverride)
	}
}
