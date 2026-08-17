package execcell

import "time"

const MaxLiveOutputBytes = 1024 * 1024

// MaxGroupedCommands mirrors Rust's MAX_GROUPED_COMMANDS
// (codex-rs/tui/src/exec_cell/model.rs): a compact command group flushes once
// this many completed commands accumulate while inactive.
const MaxGroupedCommands = 32

// Rust parity: codex-rs/tui/src/exec_cell/model.rs.

type ExecCommandSource int

const (
	ExecSourceAgent ExecCommandSource = iota
	ExecSourceUserShell
	ExecSourceUnifiedExecStartup
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
	ExitCode            int
	AggregatedOutput    string
	FormattedOutput     string
	LiveOutputTruncated bool
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
	hasFailedCall := false
	for _, existing := range c.Calls {
		if existing.Output != nil && existing.Output.ExitCode != 0 {
			hasFailedCall = true
			break
		}
	}
	if (len(c.Calls) >= MaxGroupedCommands && !c.IsActive()) ||
		(!IsGroupableSource(source) && !c.IsActive()) ||
		(hasFailedCall && !c.IsActive()) {
		return ExecCell{}, false
	}
	continuesExploration := isExploringCall(call) &&
		(c.IsExploringCell() || (len(c.Calls) > 0 && c.Calls[len(c.Calls)-1].Duration == nil && isExploringCall(c.Calls[len(c.Calls)-1]))) &&
		(c.IsActive() || allCallsGroupable(c.Calls))
	continuesCompactGroup := allCallsCompleteGroupableSuccess(c.Calls)
	if continuesExploration || continuesCompactGroup {
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
	for _, call := range c.Calls {
		if !IsGroupableSource(call.Source) || (call.Output != nil && call.Output.ExitCode != 0) {
			return !c.IsActive()
		}
	}
	if len(c.Calls) >= MaxGroupedCommands {
		return !c.IsActive()
	}
	if allCallsCompleteGroupableSuccess(c.Calls) {
		return false
	}
	return !c.IsExploringCell() && allCallsHaveDuration(c.Calls)
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

func allCallsCompleteGroupableSuccess(calls []ExecCall) bool {
	for _, call := range calls {
		if !IsGroupableSource(call.Source) || call.Duration == nil || call.Output == nil || call.Output.ExitCode != 0 {
			return false
		}
	}
	return true
}

func allCallsGroupable(calls []ExecCall) bool {
	for _, call := range calls {
		if !IsGroupableSource(call.Source) {
			return false
		}
	}
	return true
}

func allCallsHaveDuration(calls []ExecCall) bool {
	for _, call := range calls {
		if call.Duration == nil {
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
		if len(c.Calls[i].Output.AggregatedOutput) > MaxLiveOutputBytes {
			value := c.Calls[i].Output.AggregatedOutput
			c.Calls[i].Output.AggregatedOutput = value[len(value)-MaxLiveOutputBytes:]
			c.Calls[i].Output.LiveOutputTruncated = true
		}
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

// IsGroupableSource mirrors Rust ExecCell::is_groupable_source: only agent and
// unified-exec startup commands may accumulate into a compact "Ran N commands"
// group. Manual shell commands and unified-exec interactions stay visible.
func IsGroupableSource(source ExecCommandSource) bool {
	return source == ExecSourceAgent || source == ExecSourceUnifiedExecStartup
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
