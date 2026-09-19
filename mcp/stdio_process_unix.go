//go:build unix

package mcp

import (
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// mcpStdioProcessTermGracePeriod mirrors Rust's `PROCESS_GROUP_TERM_GRACE_PERIOD`:
// the grace between the graceful SIGTERM and the escalating SIGKILL.
const mcpStdioProcessTermGracePeriod = 2 * time.Second

// mcpStdioProcess contains one local stdio MCP server in its own process group
// so a server that spawns helpers is torn down as a tree.
type mcpStdioProcess struct {
	mu             sync.Mutex
	processGroupID int
	terminated     bool
}

// startMCPStdioProcess starts the command as a process-group leader, mirroring
// Rust's `ProcessMode::NewGroup` (`setpgid(0, 0)` in the child).
func startMCPStdioProcess(cmd *exec.Cmd) (*mcpStdioProcess, error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &mcpStdioProcess{processGroupID: cmd.Process.Pid}, nil
}

// terminate signals the whole group - SIGTERM first, then SIGKILL after the
// grace period - mirroring Rust's terminate_process_group/kill_process_group.
// It is idempotent and never blocks the caller.
func (p *mcpStdioProcess) terminate(cmd *exec.Cmd) {
	if p == nil {
		killMCPStdioDirectChild(cmd)
		return
	}
	p.mu.Lock()
	if p.terminated {
		p.mu.Unlock()
		return
	}
	p.terminated = true
	processGroupID := p.processGroupID
	p.mu.Unlock()
	if processGroupID <= 0 {
		killMCPStdioDirectChild(cmd)
		return
	}
	// Rust escalates only when SIGTERM reached an existing group; a group that
	// is already gone (or cannot be signalled) needs no SIGKILL.
	if err := syscall.Kill(-processGroupID, syscall.SIGTERM); err != nil {
		return
	}
	go func() {
		time.Sleep(mcpStdioProcessTermGracePeriod)
		_ = syscall.Kill(-processGroupID, syscall.SIGKILL)
	}()
}

func (p *mcpStdioProcess) release() {}

// contained reports whether the server runs in its own process group.
func (p *mcpStdioProcess) contained() bool {
	return p != nil && p.processGroupID > 0
}
