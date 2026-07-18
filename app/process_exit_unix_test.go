//go:build unix

package app

import (
	"errors"
	"os/exec"
	"testing"
)

func TestExitCodeFromExitErrorMapsUnixSignalLikeRust(t *testing.T) {
	err := exec.Command("sh", "-c", "kill -TERM $$").Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("command error = %v, want exec.ExitError", err)
	}
	if got := exitCodeFromExitError(exitErr); got != 143 {
		t.Fatalf("exitCodeFromExitError() = %d, want 143", got)
	}
}
