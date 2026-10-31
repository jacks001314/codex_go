package windowsandboxservice

import (
	"errors"
	"fmt"
)

// RunMode selects how the service process runs (Rust main::RunMode).
type RunMode int

// RunModeService is the SCM-dispatched service.
const RunModeService RunMode = 0

// ParseRunMode parses the service arguments. Foreground mode only exists in
// debug builds of the Rust service and is not accepted here.
func ParseRunMode(arguments []string) (RunMode, error) {
	switch len(arguments) {
	case 0:
		return RunModeService, nil
	case 1:
		if arguments[0] == "--service" {
			return RunModeService, nil
		}
		return RunModeService, fmt.Errorf("unrecognized service argument %q", arguments[0])
	default:
		return RunModeService, errors.New("expected at most one service argument")
	}
}
