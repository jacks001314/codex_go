package plugin

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestTrustedGitRepositoryChild(t *testing.T) {
	home := os.Getenv("CODEX_GO_TEST_TRUSTED_GIT_HOME")
	if home == "" {
		return
	}
	guard, err := configureTrustedGitRepository(exec.Command("git"), home)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	fmt.Println(guard.Path())
	// The parent closes stdin to simulate a process that exits without Close.
	_, _ = bufio.NewReader(os.Stdin).ReadByte()
	os.Exit(0)
}

func TestTrustedGitRepositoryCrossProcessReclamation(t *testing.T) {
	home := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestTrustedGitRepositoryChild$")
	command.Env = append(os.Environ(), "CODEX_GO_TEST_TRUSTED_GIT_HOME="+home)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		_ = stdin.Close()
		if !waited {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() {
		t.Fatalf("child did not publish repository: %v", scanner.Err())
	}
	childPath := scanner.Text()
	live, err := configureTrustedGitRepository(exec.Command("git"), home)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	if _, err := os.Stat(filepath.Join(childPath, "HEAD")); err != nil {
		t.Fatalf("live child reclaimed: %v", err)
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	waited = true
	next, err := configureTrustedGitRepository(exec.Command("git"), home)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if _, err := os.Stat(childPath); !os.IsNotExist(err) {
		t.Fatalf("dead child repository retained: %v", err)
	}
	for _, path := range []string{live.Path(), next.Path()} {
		if _, err := os.Stat(filepath.Join(path, "HEAD")); err != nil {
			t.Fatalf("active repository reclaimed: %v", err)
		}
	}
}
