//go:build !unix && !windows

package mcp

import "os/exec"

// mcpStdioProcess is a no-op on platforms without process-group or Job Object
// containment; teardown falls back to the direct child, like Rust's
// `LocalProcessTerminator` no-op arm.
type mcpStdioProcess struct{}

func startMCPStdioProcess(cmd *exec.Cmd) (*mcpStdioProcess, error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &mcpStdioProcess{}, nil
}

func (p *mcpStdioProcess) terminate(cmd *exec.Cmd) { killMCPStdioDirectChild(cmd) }

func (p *mcpStdioProcess) release() {}

func (p *mcpStdioProcess) contained() bool { return false }
