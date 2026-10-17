//go:build !darwin

package appserverdaemon

import "os/exec"

// managedPIDCommand prepares the platform command used for a detached managed
// process. Non-macOS hosts launch the program directly; process grouping is
// applied by the caller.
func managedPIDCommand(program string, args []string) (*exec.Cmd, error) {
	return exec.Command(program, args...), nil
}
