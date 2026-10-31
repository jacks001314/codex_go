package windowsandboxservice

// Run dispatches one parsed run mode to the service host (Rust lib::run). The
// SCM dispatch and its refusal on other platforms live in the per-platform
// files.
func Run(mode RunMode) error {
	return runServiceMode(mode)
}
