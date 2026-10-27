package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex_go/cli"
	"codex_go/session"
)

// TestLatestSessionLookupStaysDirectoryScopedLikeRust covers Rust #56308:
// `resume --last`/`fork --last` pick the newest session in the launch
// directory, and --all is the only way to widen the search.
func TestLatestSessionLookupStaysDirectoryScopedLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := time.Now()
	records := []*session.Record{
		{
			ID:        "thread-here",
			Title:     "Here",
			CreatedAt: now.Add(-2 * time.Hour),
			UpdatedAt: now.Add(-time.Hour),
			RecencyAt: now.Add(-time.Hour),
			Metadata:  session.Metadata{CWD: `D:\repo\here`, Source: "cli"},
		},
		{
			ID:        "thread-elsewhere",
			Title:     "Elsewhere",
			CreatedAt: now.Add(-time.Hour),
			UpdatedAt: now,
			RecencyAt: now,
			Metadata:  session.Metadata{CWD: `D:\repo\elsewhere`, Source: "cli"},
		},
	}
	for _, record := range records {
		if err := store.Save(record); err != nil {
			t.Fatalf("Save(%s) error = %v", record.ID, err)
		}
	}

	scoped := &cli.SessionOptions{Last: true, Shared: cli.SharedOptions{CWD: `D:\repo\here`}}
	if id, err := latestSessionID(store, scoped); err != nil || id != "thread-here" {
		t.Fatalf("scoped latest = %q, %v; want thread-here", id, err)
	}

	widened := &cli.SessionOptions{Last: true, All: true, Shared: cli.SharedOptions{CWD: `D:\repo\here`}}
	if id, err := latestSessionID(store, widened); err != nil || id != "thread-elsewhere" {
		t.Fatalf("--all latest = %q, %v; want thread-elsewhere", id, err)
	}

	missing := &cli.SessionOptions{Last: true, Shared: cli.SharedOptions{CWD: `D:\repo\missing`}}
	if id, err := latestSessionID(store, missing); err == nil || id != "" {
		t.Fatalf("missing-directory latest = %q, %v; want ErrThreadNotFound", id, err)
	}
}

// TestLatestSessionCWDLikeRust covers Rust latest_session_cwd_filter: --all
// clears the scope, an explicit launch directory wins, and the process working
// directory is the fallback.
func TestLatestSessionCWDLikeRust(t *testing.T) {
	if got := latestSessionCWD(nil); got == "" {
		t.Fatal("default latest cwd is empty")
	}
	if got := latestSessionCWD(&cli.SessionOptions{All: true, Shared: cli.SharedOptions{CWD: "/repo"}}); got != "" {
		t.Fatalf("--all cwd = %q, want empty", got)
	}
	if got := latestSessionCWD(&cli.SessionOptions{Shared: cli.SharedOptions{CWD: "/repo"}}); got != "/repo" {
		t.Fatalf("explicit cwd = %q, want /repo", got)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Skipf("getwd: %v", err)
	}
	if got := latestSessionCWD(&cli.SessionOptions{}); filepath.Clean(got) != filepath.Clean(wd) {
		t.Fatalf("fallback cwd = %q, want %q", got, wd)
	}
}
