package tool

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestUnifiedExecNULHelperProcess records that an invalid command ran.
func TestUnifiedExecNULHelperProcess(t *testing.T) {
	separator := -1
	for index, arg := range os.Args {
		if arg == "--nul-helper" {
			separator = index
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		return
	}
	_ = os.WriteFile(os.Args[separator+1], []byte("ran"), 0o600)
	os.Exit(0)
}

// TestUnifiedExecRejectsNULInputsWithoutRunningLikeRust mirrors the Rust
// command_validation_tests contract: embedded NUL bytes fail before a child can
// run, so the command has no side effects.
func TestUnifiedExecRejectsNULInputsWithoutRunningLikeRust(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "ran")
	cases := []struct {
		name string
		cmd  func() *exec.Cmd
	}{
		{
			name: "argument",
			cmd: func() *exec.Cmd {
				return exec.Command(os.Args[0], "-test.run=^TestUnifiedExecNULHelperProcess$", "--", "--nul-helper", marker, "bad\x00arg")
			},
		},
		{
			name: "cwd",
			cmd: func() *exec.Cmd {
				cmd := exec.Command(os.Args[0], "-test.run=^TestUnifiedExecNULHelperProcess$", "--", "--nul-helper", marker)
				cmd.Dir = filepath.Join(root, "bad\x00cwd")
				return cmd
			},
		},
		{
			name: "environment value",
			cmd: func() *exec.Cmd {
				cmd := exec.Command(os.Args[0], "-test.run=^TestUnifiedExecNULHelperProcess$", "--", "--nul-helper", marker)
				cmd.Env = []string{"PIPE_HELPER_VALUE=bad\x00value"}
				return cmd
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := startUnifiedExecCommand(testCase.cmd(), false)
			if err == nil {
				t.Fatal("startUnifiedExecCommand() error = nil, want an embedded NUL rejection")
			}
			if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
				t.Fatalf("invalid command ran: Stat(marker) error = %v", statErr)
			}
		})
	}
}
