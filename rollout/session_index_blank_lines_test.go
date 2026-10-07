package rollout

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRemoveThreadNameEntriesPreservesBlankLinesLikeRust mirrors Rust's
// `contents.lines()` rewrite: interior blank lines survive the removal of a
// thread's entries and a single trailing newline is not doubled.
func TestRemoveThreadNameEntriesPreservesBlankLinesLikeRust(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, SessionIndexFilename)
	contents := "{\"id\":\"remove\",\"thread_name\":\"old\",\"updated_at\":\"1\"}\n" +
		"\n" +
		"{\"id\":\"keep\",\"thread_name\":\"kept\",\"updated_at\":\"2\"}\n" +
		"\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveThreadNameEntries(home, "remove"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "\n{\"id\":\"keep\",\"thread_name\":\"kept\",\"updated_at\":\"2\"}\n\n"
	if string(data) != want {
		t.Fatalf("remaining index = %q, want %q", data, want)
	}
}
