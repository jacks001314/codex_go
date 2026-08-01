package parity

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestStateMigrationsMatchRustTarget(t *testing.T) {
	rustWorkspace := rustSnapshotRoot(t)
	rustRepo := filepath.Dir(rustWorkspace)
	mappings := []struct {
		rustDir string
		goDir   string
	}{
		{rustDir: "codex-rs/state/migrations", goDir: filepath.Join("..", "state", "migrations", "state")},
		{rustDir: "codex-rs/state/logs_migrations", goDir: filepath.Join("..", "state", "migrations", "logs")},
		{rustDir: "codex-rs/state/goals_migrations", goDir: filepath.Join("..", "state", "migrations", "goals")},
		{rustDir: "codex-rs/state/memory_migrations", goDir: filepath.Join("..", "state", "migrations", "memories")},
		{rustDir: "codex-rs/state/thread_history_migrations", goDir: filepath.Join("..", "state", "migrations", "thread_history")},
	}
	for _, mapping := range mappings {
		rustFiles := rustMigrationFilesAtTarget(t, rustRepo, mapping.rustDir)
		goFiles := localSQLFilenames(t, mapping.goDir)
		if strings.Join(goFiles, "\n") != strings.Join(rustFiles, "\n") {
			t.Fatalf("migration inventory differs for %s: Go=%v Rust=%v", mapping.rustDir, goFiles, rustFiles)
		}
		for _, name := range rustFiles {
			gitPath := mapping.rustDir + "/" + name
			rustData := gitOutput(t, rustRepo, "show", candidateRustTo+":"+gitPath)
			goPath := filepath.Join(mapping.goDir, name)
			goData, err := os.ReadFile(goPath)
			if err != nil {
				t.Fatalf("ReadFile(%s): %v", goPath, err)
			}
			if !bytes.Equal(goData, rustData) {
				t.Fatalf("Go migration %s differs byte-for-byte from Rust target %s", goPath, gitPath)
			}
		}
	}
}

func rustMigrationFilesAtTarget(t *testing.T, rustRepo string, directory string) []string {
	t.Helper()
	output := gitOutput(t, rustRepo, "ls-tree", "-r", "--name-only", candidateRustTo, "--", directory)
	files := make([]string, 0)
	for _, file := range strings.Fields(string(output)) {
		if strings.HasSuffix(file, ".sql") {
			files = append(files, filepath.Base(filepath.FromSlash(file)))
		}
	}
	sort.Strings(files)
	return files
}

func localSQLFilenames(t *testing.T, directory string) []string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", directory, err)
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			files = append(files, entry.Name())
		}
	}
	sort.Strings(files)
	return files
}

func gitOutput(t *testing.T, repo string, args ...string) []byte {
	t.Helper()
	commandArgs := append([]string{"-C", repo}, args...)
	output, err := exec.Command("git", commandArgs...).Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(commandArgs, " "), err)
	}
	return output
}
