package rollout

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestListThreadsFiltersCanonicalSessionMetaLikeRust pins Rust #61902: the
// source, provider, and cwd filters apply to the first session metadata record,
// so a copied fork record later in the rollout cannot change whether the thread
// is listed.
func TestListThreadsFiltersCanonicalSessionMetaLikeRust(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, SessionsSubdir, "2026", "11", "17")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rollout-2026-11-17T00-00-00-thread-copied.jsonl")
	lines := []string{
		`{"timestamp":"2026-11-17T00:00:00Z","type":"session_meta","payload":{"id":"thread-copied","timestamp":"2026-11-17T00:00:00Z","cwd":"/repo","source":"cli","model_provider":"openai"}}`,
		`{"timestamp":"2026-11-17T00:00:01Z","type":"session_meta","payload":{"id":"thread-copied","timestamp":"2026-11-17T00:00:01Z","cwd":"/elsewhere","source":"subagent","model_provider":"azure"}}`,
		`{"timestamp":"2026-11-17T00:00:02Z","type":"user_message","payload":{"text":"hello"}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	page, err := ListThreads(home, ListOptions{AllowedSources: []string{"cli"}})
	if err != nil {
		t.Fatalf("ListThreads(cli) error = %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ThreadID != "thread-copied" {
		t.Fatalf("ListThreads(cli) items = %#v, want the rollout listed by its canonical metadata", page.Items)
	}

	for _, options := range []ListOptions{
		{AllowedSources: []string{"subagent"}},
		{ModelProviders: []string{"azure"}},
		{CWDFilters: []string{"/elsewhere"}},
	} {
		page, err := ListThreads(home, options)
		if err != nil {
			t.Fatalf("ListThreads(%#v) error = %v", options, err)
		}
		if len(page.Items) != 0 {
			t.Fatalf("ListThreads(%#v) items = %#v, want the copied metadata ignored", options, page.Items)
		}
	}

}
