package execcell

import (
	"time"

	"codex_go/tui"
)

const MaxLiveOutputBytes = 1024 * 1024

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
	// Reasoning holds transcript-only reasoning blocks attached while this
	// exploring group was the active cell (Rust #46565). They render only in
	// the expanded transcript, never in the compact preview or raw output.
	Reasoning []tui.ActivityReasoning
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
	// Rust #41893: only related exploration (adjacent read/list/search commands)
	// groups into one cell; every other command renders individually.
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

// ShouldFlush reports whether the cell is complete and must not accept more
// calls. Exploration stays open for adjacent calls, including after a failed
// read/list/search (Rust #46487).
func (c ExecCell) ShouldFlush() bool {
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

// AppendReasoning attaches a transcript-only reasoning block after the calls
// already grouped, mirroring Rust ExecCell::append_reasoning. Only an exploring
// cell accepts reasoning; other cells report false so the caller renders the
// block on its own.
func (c *ExecCell) AppendReasoning(itemID string, content string, rawContent string) bool {
	if c == nil || !c.IsExploringCell() {
		return false
	}
	group := tui.ActivityGroup[ExecCall]{Calls: c.Calls, Reasoning: c.Reasoning}
	if !group.PushReasoning(itemID, content, rawContent) {
		// The same item is already attached; treat it as accepted so the caller
		// does not render a duplicate entry.
		return true
	}
	c.Reasoning = group.Reasoning
	return true
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

// isExploringCall mirrors Rust ExecCell::is_exploring_call: an agent command
// whose parsed actions are only file reads, listings, or searches.
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
