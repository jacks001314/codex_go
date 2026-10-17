//go:build darwin

package appserverdaemon

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestDarwinManagedPIDCommandUsesBackgroundPriorityWhenAvailable(t *testing.T) {
	previousProbe := runTaskpolicyProbe
	t.Cleanup(func() { runTaskpolicyProbe = previousProbe })
	runTaskpolicyProbe = func(context.Context) error { return nil }

	program := os.Args[0]
	command, err := managedPIDCommand(program, []string{"--managed-daemon"})
	if err != nil {
		t.Fatalf("managedPIDCommand() error = %v", err)
	}
	if command.Path != "/usr/sbin/taskpolicy" ||
		len(command.Args) != 4 ||
		command.Args[1] != "-b" ||
		command.Args[2] != program ||
		command.Args[3] != "--managed-daemon" {
		t.Fatalf("managedPIDCommand() = path %q args %#v", command.Path, command.Args)
	}
}

func TestDarwinManagedPIDCommandFallsBackAfterProbeTimeout(t *testing.T) {
	previousProbe := runTaskpolicyProbe
	previousTimeout := taskpolicyProbeTimeout
	t.Cleanup(func() {
		runTaskpolicyProbe = previousProbe
		taskpolicyProbeTimeout = previousTimeout
	})
	taskpolicyProbeTimeout = 20 * time.Millisecond
	runTaskpolicyProbe = func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}

	program := os.Args[0]
	started := time.Now()
	command, err := managedPIDCommand(program, []string{"--managed-daemon"})
	if err != nil {
		t.Fatalf("managedPIDCommand() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("taskpolicy probe fallback took %v", elapsed)
	}
	if command.Path != program {
		t.Fatalf("fallback path = %q, want %q", command.Path, program)
	}
}
