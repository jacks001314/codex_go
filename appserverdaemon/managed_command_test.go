package appserverdaemon

import (
	"os"
	"runtime"
	"testing"
	"time"
)

func TestManagedPIDCommandDirectLaunchOffDarwin(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS uses the taskpolicy wrapper")
	}
	program := os.Args[0]
	command, err := managedPIDCommand(program, []string{"--version"})
	if err != nil {
		t.Fatalf("managedPIDCommand() error = %v", err)
	}
	if command.Args[0] != program || len(command.Args) != 2 || command.Args[1] != "--version" {
		t.Fatalf("managedPIDCommand() = %#v", command.Args)
	}
}

func TestPIDStartTimeoutMatchesPlatformLikeRust(t *testing.T) {
	want := 10 * time.Second
	if runtime.GOOS == "darwin" {
		want = 30 * time.Second
	}
	if PIDStartTimeout != want {
		t.Fatalf("PIDStartTimeout = %v, want %v", PIDStartTimeout, want)
	}
}
