// Package gitutil runs git metadata commands with whole-process-tree
// termination on timeout or cancellation, mirroring Rust git-utils
// git_process.rs (Rust 3149fa4b99).
package gitutil

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"codex_go/envutil"
)

const defaultGitTimeout = 5 * time.Second

// Run executes `git args...` in cwd. On context cancellation or the default
// timeout the entire spawned process tree is terminated, not just the direct
// git process.
func Run(ctx context.Context, cwd string, args ...string) (string, error) {
	return RunWithTimeout(ctx, defaultGitTimeout, cwd, args...)
}

// RunWithTimeout is Run with an explicit timeout.
func RunWithTimeout(ctx context.Context, timeout time.Duration, cwd string, args ...string) (string, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	newCommand := func() *exec.Cmd {
		cmd := exec.Command("git", args...)
		cmd.Dir = cwd
		cmd.Stdin = nil
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		// Rust c4513cb982: git helper processes must not inherit Codex launch
		// context (OPENAI_FEDERATION_RULE_ID / OPENAI_IDENTITY_TOKEN_FILE).
		envutil.ScrubCommandEnv(cmd)
		return cmd
	}
	tree, err := startGitTree(newCommand(), newCommand)
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	done := make(chan error, 1)
	go func() { done <- tree.wait() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		if err != nil {
			detail := strings.TrimSpace(stderr.String())
			if detail != "" {
				return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, detail)
			}
			return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return strings.TrimSpace(stdout.String()), nil
	case <-ctx.Done():
		tree.kill()
		<-done
		return "", ctx.Err()
	case <-timer.C:
		tree.kill()
		<-done
		return "", fmt.Errorf("git %s timed out", strings.Join(args, " "))
	}
}

// IsCancellation reports whether err came from the caller's context or the
// command timeout.
func IsCancellation(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "timed out")
}
