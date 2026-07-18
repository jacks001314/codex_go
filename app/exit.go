package app

import (
	"errors"
	"fmt"
)

type ExitError struct {
	Code    int
	Message string
	Silent  bool
}

func (e *ExitError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("exit status %d", e.exitCode())
}

func (e *ExitError) exitCode() int {
	if e != nil && e.Code != 0 {
		return e.Code
	}
	return 1
}

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		return exitErr.exitCode()
	}
	return 1
}

func ShouldPrintError(err error) bool {
	if err == nil {
		return false
	}
	var exitErr *ExitError
	if errors.As(err, &exitErr) && exitErr.Silent {
		return false
	}
	return true
}
