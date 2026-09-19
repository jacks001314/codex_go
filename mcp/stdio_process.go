package mcp

import "os/exec"

// Local stdio MCP servers are launched into their own containment so shutdown
// tears down descendants the server spawned instead of only the direct child
// (Rust `82c981cafc`, "Process-group cleanup for stdio MCP servers to prevent
// orphan process storms"). #46660 moved the launcher onto the shared pty
// command, which keeps `ProcessMode::NewGroup` on Unix and the suspended
// spawn plus Job Object on Windows.

// killMCPStdioDirectChild is the last-resort teardown when no containment was
// established: it terminates only the direct child, matching Rust's
// `LocalProcessTerminator::Process` fallback.
func killMCPStdioDirectChild(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
