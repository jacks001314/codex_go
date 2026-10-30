//go:build unix

package tool

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// TestUnifiedExecPipeLaunchHelperProcess is the re-executed child for the pipe
// launcher regression test. It reports the observable launch contract only.
func TestUnifiedExecPipeLaunchHelperProcess(t *testing.T) {
	separator := -1
	for index, arg := range os.Args {
		if arg == "--pipe-launch-helper" {
			separator = index
			break
		}
	}
	if separator < 0 {
		return
	}
	pid := os.Getpid()
	sid, sidErr := unix.Getsid(0)
	pgid := unix.Getpgrp()
	cwd, cwdErr := os.Getwd()
	leaked := false
	if raw := os.Getenv("PIPE_HELPER_EXTRA_FD"); raw != "" {
		if fd, err := strconv.Atoi(raw); err == nil {
			_, fcntlErr := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
			leaked = fcntlErr == nil
		}
	}
	fmt.Fprintf(os.Stdout, "pid|%d\n", pid)
	fmt.Fprintf(os.Stdout, "session|%t\n", sidErr == nil && sid == pid && pgid == pid)
	fmt.Fprintf(os.Stdout, "cwd|%s|%v\n", cwd, cwdErr)
	fmt.Fprintf(os.Stdout, "parent-only|%s\n", os.Getenv("PIPE_HELPER_PARENT_ONLY"))
	fmt.Fprintf(os.Stdout, "fd-leaked|%t\n", leaked)
	os.Exit(0)
}

// TestUnifiedExecPipeLaunchUsesNewSessionLikeRust mirrors the Rust
// macos_pipe_spawn contract for #47605: the piped child leads its own session,
// keeps the requested cwd, receives only the explicit environment, and does not
// inherit unrelated descriptors.
func TestUnifiedExecPipeLaunchUsesNewSessionLikeRust(t *testing.T) {
	cwd := t.TempDir()
	duplicate, err := syscall.Dup(int(os.Stdout.Fd()))
	if err != nil {
		t.Fatalf("Dup(stdout) error = %v", err)
	}
	defer syscall.Close(duplicate)
	t.Setenv("PIPE_HELPER_PARENT_ONLY", "leaked")

	cmd := exec.Command(os.Args[0], "-test.run=^TestUnifiedExecPipeLaunchHelperProcess$", "--", "--pipe-launch-helper")
	cmd.Dir = cwd
	cmd.Env = []string{"PIPE_HELPER_EXTRA_FD=" + strconv.Itoa(duplicate)}
	started, err := startUnifiedExecCommand(cmd, false)
	if err != nil {
		t.Fatalf("startUnifiedExecCommand() error = %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var readers sync.WaitGroup
	for index, reader := range started.readers {
		target := &stdout
		if index > 0 {
			target = &stderr
		}
		readers.Add(1)
		go func(reader io.ReadCloser, target *bytes.Buffer) {
			defer readers.Done()
			defer reader.Close()
			_, _ = io.Copy(target, reader)
		}(reader, target)
	}
	readers.Wait()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("Wait() error = %v, stderr=%q", err, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "session|true") {
		t.Fatalf("pipe child output = %q, want a new session and group", output)
	}
	if !strings.Contains(output, "cwd|"+cwd+"|") {
		t.Fatalf("pipe child output = %q, want cwd %q", output, cwd)
	}
	if !strings.Contains(output, "parent-only|\n") {
		t.Fatalf("pipe child output = %q, want only the explicit environment", output)
	}
	if !strings.Contains(output, "fd-leaked|false") {
		t.Fatalf("pipe child output = %q, want unrelated descriptors closed", output)
	}
}
