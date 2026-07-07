package app

import (
	"errors"
	"testing"
)

func TestExitCodeAndShouldPrintError(t *testing.T) {
	if ExitCode(nil) != 0 {
		t.Fatal("ExitCode(nil) != 0")
	}
	plainErr := errors.New("boom")
	if ExitCode(plainErr) != 1 || !ShouldPrintError(plainErr) {
		t.Fatal("plain error should exit 1 and print")
	}
	silentErr := &ExitError{Code: 7, Message: "quiet", Silent: true}
	if ExitCode(silentErr) != 7 {
		t.Fatalf("ExitCode(silentErr) = %d, want 7", ExitCode(silentErr))
	}
	if ShouldPrintError(silentErr) {
		t.Fatal("silent ExitError should not print")
	}
	defaultErr := &ExitError{}
	if ExitCode(defaultErr) != 1 || !ShouldPrintError(defaultErr) {
		t.Fatal("default ExitError should exit 1 and print")
	}
}
