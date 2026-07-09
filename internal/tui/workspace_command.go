package tui

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Rust parity: codex-rs/tui/src/workspace_command.rs.

const (
	DefaultWorkspaceCommandTimeout        = 5 * time.Second
	DefaultWorkspaceCommandOutputBytesCap = 64 * 1024
)

type WorkspaceCommandRunner interface {
	RunWorkspaceCommand(ctx context.Context, command WorkspaceCommand) (WorkspaceCommandOutput, error)
}

type WorkspaceCommand struct {
	Name string
	CWD  string

	Argv             []string
	Env              map[string]*string
	Timeout          time.Duration
	OutputBytesCap   int
	DisableOutputCap bool
}

func NewWorkspaceCommand(argv ...string) WorkspaceCommand {
	command := WorkspaceCommand{
		Argv:           cleanWorkspaceArgv(argv),
		Env:            map[string]*string{},
		Timeout:        DefaultWorkspaceCommandTimeout,
		OutputBytesCap: DefaultWorkspaceCommandOutputBytesCap,
	}
	if len(command.Argv) > 0 {
		command.Name = command.Argv[0]
	}
	return command
}

func (c WorkspaceCommand) WithCWD(cwd string) WorkspaceCommand {
	c.CWD = strings.TrimSpace(cwd)
	return c
}

func (c WorkspaceCommand) WithEnv(key string, value string) WorkspaceCommand {
	c.ensureEnv()
	value = strings.TrimSpace(value)
	c.Env[strings.TrimSpace(key)] = &value
	return c
}

func (c WorkspaceCommand) WithoutEnv(key string) WorkspaceCommand {
	c.ensureEnv()
	c.Env[strings.TrimSpace(key)] = nil
	return c
}

func (c WorkspaceCommand) WithTimeout(timeout time.Duration) WorkspaceCommand {
	if timeout > 0 {
		c.Timeout = timeout
	}
	return c
}

func (c WorkspaceCommand) WithOutputBytesCap(cap int) WorkspaceCommand {
	if cap > 0 {
		c.OutputBytesCap = cap
	}
	return c
}

func (c WorkspaceCommand) WithDisabledOutputCap() WorkspaceCommand {
	c.DisableOutputCap = true
	return c
}

func (c WorkspaceCommand) TimeoutMillis() int64 {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultWorkspaceCommandTimeout
	}
	return int64(timeout / time.Millisecond)
}

func (c *WorkspaceCommand) ensureEnv() {
	if c.Env == nil {
		c.Env = map[string]*string{}
	}
}

type WorkspaceCommandOutput struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

func (o WorkspaceCommandOutput) Success() bool {
	return o.ExitCode == 0
}

type WorkspaceCommandError struct {
	Message string
}

func NewWorkspaceCommandError(message string) WorkspaceCommandError {
	return WorkspaceCommandError{Message: strings.TrimSpace(message)}
}

func (e WorkspaceCommandError) Error() string {
	if e.Message == "" {
		return "workspace command failed"
	}
	return e.Message
}

func (c WorkspaceCommand) Validate() error {
	if len(c.Argv) == 0 {
		return fmt.Errorf("workspace command argv is empty")
	}
	for _, arg := range c.Argv {
		if strings.TrimSpace(arg) == "" {
			return fmt.Errorf("workspace command argv contains an empty argument")
		}
	}
	return nil
}

func cleanWorkspaceArgv(argv []string) []string {
	out := make([]string, 0, len(argv))
	for _, arg := range argv {
		arg = strings.TrimSpace(arg)
		if arg != "" {
			out = append(out, arg)
		}
	}
	return out
}
