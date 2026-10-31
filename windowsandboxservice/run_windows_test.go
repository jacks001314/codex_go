//go:build windows

package windowsandboxservice

import (
	"strings"
	"testing"
)

func TestRunRefusesAnUnsupportedMode(t *testing.T) {
	err := Run(RunMode(7))
	if err == nil || !strings.Contains(err.Error(), "unsupported sandbox service run mode") {
		t.Fatalf("Run() error = %v", err)
	}
}
