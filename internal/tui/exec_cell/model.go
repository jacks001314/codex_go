package execcell

import "time"

// Rust parity: codex-rs/tui/src/exec_cell/model.rs.

type ExecCommandSource int

const (
	ExecSourceAgent ExecCommandSource = iota
	ExecSourceUserShell
	ExecSourceUnifiedExecInteraction
)

type ParsedCommandKind int

const (
	ParsedRead ParsedCommandKind = iota
	ParsedListFiles
	ParsedSearch
	ParsedUnknown
)

type ParsedCommand struct {
	Kind  ParsedCommandKind
	Name  string
	Cmd   string
	Path  string
	Query string
}

type CommandOutput struct {
	ExitCode         int
	AggregatedOutput string
	FormattedOutput  string
}

type ExecCall struct {
	CallID           string
	Command          []string
	Parsed           []ParsedCommand
	Output           *CommandOutput
	Source           ExecCommandSource
	StartTime        *time.Time
	Duration         *time.Duration
	InteractionInput string
}

type ExecCell struct {
	Calls             []ExecCall
	AnimationsEnabled bool
}

func NewExecCell(call ExecCall, animationsEnabled bool) ExecCell {
	return ExecCell{Calls: []ExecCall{call}, AnimationsEnabled: animationsEnabled}
}

func (c ExecCell) WithAddedCall(callID string, command []string, parsed []ParsedCommand, source ExecCommandSource, interactionInput string) (ExecCell, bool) {
	now := time.Now()
	call := ExecCall{
		CallID:           callID,
		Command:          append([]string(nil), command...),
		Parsed:           append([]ParsedCommand(nil), parsed...),
		Source:           source,
		StartTime:        &now,
		InteractionInput: interactionInput,
	}
	if c.IsExploringCell() && isExploringCall(call) {
		next := c
		next.Calls = append(append([]ExecCall(nil), c.Calls...), call)
		return next, true
	}
	return ExecCell{}, false
}

func (c *ExecCell) CompleteCall(callID string, output CommandOutput, duration time.Duration) bool {
	for i := len(c.Calls) - 1; i >= 0; i-- {
		if c.Calls[i].CallID != callID {
			continue
		}
		c.Calls[i].Output = &output
		c.Calls[i].Duration = &duration
		c.Calls[i].StartTime = nil
		return true
	}
	return false
}

func (c ExecCell) ShouldFlush() bool {
	if c.IsExploringCell() {
		return false
	}
	for _, call := range c.Calls {
		if call.Output == nil {
			return false
		}
	}
	return true
}

func (c *ExecCell) MarkFailed() {
	for i := range c.Calls {
		if c.Calls[i].Output != nil {
			continue
		}
		var elapsed time.Duration
		if c.Calls[i].StartTime != nil {
			elapsed = time.Since(*c.Calls[i].StartTime)
		}
		c.Calls[i].StartTime = nil
		c.Calls[i].Duration = &elapsed
		c.Calls[i].Output = &CommandOutput{ExitCode: 1}
	}
}

func (c ExecCell) IsExploringCell() bool {
	if len(c.Calls) == 0 {
		return false
	}
	for _, call := range c.Calls {
		if !isExploringCall(call) {
			return false
		}
	}
	return true
}

func (c ExecCell) IsActive() bool {
	for _, call := range c.Calls {
		if call.Output == nil {
			return true
		}
	}
	return false
}

func (c ExecCell) ActiveStartTime() *time.Time {
	for _, call := range c.Calls {
		if call.Output == nil {
			return call.StartTime
		}
	}
	return nil
}

func (c *ExecCell) AppendOutput(callID string, chunk string) bool {
	if chunk == "" {
		return false
	}
	for i := len(c.Calls) - 1; i >= 0; i-- {
		if c.Calls[i].CallID != callID {
			continue
		}
		if c.Calls[i].Output == nil {
			c.Calls[i].Output = &CommandOutput{}
		}
		c.Calls[i].Output.AggregatedOutput += chunk
		return true
	}
	return false
}

func (c ExecCall) IsUserShellCommand() bool {
	return c.Source == ExecSourceUserShell
}

func (c ExecCall) IsUnifiedExecInteraction() bool {
	return c.Source == ExecSourceUnifiedExecInteraction
}

func isExploringCall(call ExecCall) bool {
	if call.Source == ExecSourceUserShell || len(call.Parsed) == 0 {
		return false
	}
	for _, parsed := range call.Parsed {
		switch parsed.Kind {
		case ParsedRead, ParsedListFiles, ParsedSearch:
		default:
			return false
		}
	}
	return true
}
