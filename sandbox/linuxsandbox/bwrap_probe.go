package linuxsandbox

import "os/exec"

// probeBwrapHelp reads a launcher's `--help` output and reports which
// capabilities it advertises (Rust system_bwrap_capabilities).
func probeBwrapHelp(path string) (bwrapCapabilities, bool) {
	output, err := exec.Command(path, "--help").CombinedOutput()
	if err != nil && len(output) == 0 {
		return bwrapCapabilities{}, false
	}
	return parseBwrapHelp(string(output))
}
