package appserver

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"codex_go/state"
	"codex_go/telemetry"
)

// Mirrors Rust's THREAD_STARTED_METRIC: one counter per thread, tagged by
// whether the checkout is a git repository and whether it is a linked worktree.
func TestEmitThreadStartedMetricLikeRust(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	primary := filepath.Join(root, "primary")
	if err := os.MkdirAll(primary, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit := func(dir string, args ...string) {
		t.Helper()
		command := exec.Command("git", args...)
		command.Dir = dir
		command.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
			"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull,
		)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v error = %v\n%s", args, err, output)
		}
	}
	runGit(primary, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(primary, "file.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(primary, "add", ".")
	runGit(primary, "commit", "-m", "init")
	linked := filepath.Join(root, "linked")
	runGit(primary, "worktree", "add", "--detach", linked)

	for _, testCase := range []struct {
		name       string
		cwd        string
		wantGit    string
		wantWorktr string
	}{
		{"primary checkout", primary, "true", "false"},
		{"linked worktree", linked, "true", "true"},
		{"not a repository", t.TempDir(), "false", "unknown"},
	} {
		metrics := state.NewTaskMetrics()
		router := NewRuntimeRouter(RuntimeServices{TurnMetrics: metrics})
		router.emitThreadStartedMetric(context.Background(), &Thread{ID: "thread-1", CWD: testCase.cwd})
		records := metrics.Records()
		if len(records) != 1 {
			t.Fatalf("%s: records = %#v", testCase.name, records)
		}
		record := records[0]
		if record.Name != telemetry.ThreadStartedMetric || record.Kind != "counter" || record.Inc != 1 {
			t.Fatalf("%s: record = %#v", testCase.name, record)
		}
		if record.Tags["is_git"] != testCase.wantGit || record.Tags["is_worktree"] != testCase.wantWorktr {
			t.Fatalf("%s: tags = %#v, want is_git=%s is_worktree=%s", testCase.name, record.Tags, testCase.wantGit, testCase.wantWorktr)
		}
	}

	// No sink or no thread records nothing.
	router := NewRuntimeRouter(RuntimeServices{})
	router.emitThreadStartedMetric(context.Background(), &Thread{ID: "thread-1", CWD: primary})
	NewRuntimeRouter(RuntimeServices{TurnMetrics: state.NewTaskMetrics()}).emitThreadStartedMetric(context.Background(), nil)
}
