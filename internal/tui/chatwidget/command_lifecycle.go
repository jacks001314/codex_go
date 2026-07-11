package chatwidget

import (
	"strings"

	"codex_go/internal/shell"
)

type CommandLifecycleState struct {
	UnifiedExecProcesses []UnifiedExecProcessSummary
	UnifiedExecWait      *UnifiedExecWaitStreak
	LastUnifiedWait      *UnifiedExecWaitState
	RunningCommands      map[string]RunningCommand
	SuppressedExecCalls  map[string]bool
}

type UnifiedExecInteractionEvent struct {
	ProcessID      string
	CommandDisplay string
	Stdin          string
}

type TerminalInteractionResult struct {
	WaitingStatus              bool
	Status                     string
	StatusDetails              string
	EnsureStatusIndicator      bool
	InterruptHintVisible       bool
	NeedsFinalMessageSeparator bool
	FlushedWait                *UnifiedExecInteractionEvent
	InsertedInteraction        *UnifiedExecInteractionEvent
	Redraw                     bool
}

type CommandStartDecision struct {
	Recorded              bool
	Suppressed            bool
	EnsureStatusIndicator bool
	CommandDisplay        string
}

type CommandCompletionDecision struct {
	KnownRunning             bool
	Suppressed               bool
	Running                  RunningCommand
	HadWorkActivity          bool
	MaybeSendNextQueuedInput bool
}

func (s *CommandLifecycleState) TrackUnifiedExecProcessBegin(callID string, processID string, commandDisplay string) {
	if s == nil {
		return
	}
	key := callID
	if processID != "" {
		key = processID
	}
	if key == "" {
		return
	}
	commandDisplay = UnifiedExecCommandDisplay(commandDisplay)
	for i := range s.UnifiedExecProcesses {
		if s.UnifiedExecProcesses[i].Key == key {
			s.UnifiedExecProcesses[i].CallID = callID
			s.UnifiedExecProcesses[i].CommandDisplay = commandDisplay
			s.UnifiedExecProcesses[i].RecentChunks = nil
			return
		}
	}
	s.UnifiedExecProcesses = append(s.UnifiedExecProcesses, UnifiedExecProcessSummary{
		Key:            key,
		CallID:         callID,
		CommandDisplay: commandDisplay,
	})
}

func (s *CommandLifecycleState) TrackUnifiedExecProcessEnd(callID string, processID string) bool {
	if s == nil {
		return false
	}
	key := callID
	if processID != "" {
		key = processID
	}
	before := len(s.UnifiedExecProcesses)
	out := s.UnifiedExecProcesses[:0]
	for _, process := range s.UnifiedExecProcesses {
		if process.Key != key {
			out = append(out, process)
		}
	}
	s.UnifiedExecProcesses = out
	return len(s.UnifiedExecProcesses) != before
}

func (s *CommandLifecycleState) TrackUnifiedExecOutputChunk(callID string, chunk string, maxChunks int) {
	if s == nil || maxChunks <= 0 {
		return
	}
	for i := range s.UnifiedExecProcesses {
		if s.UnifiedExecProcesses[i].CallID != callID {
			continue
		}
		for _, line := range strings.Split(chunk, "\n") {
			line = strings.TrimRight(line, "\r")
			line = strings.TrimRight(line, " \t")
			if line == "" {
				continue
			}
			s.UnifiedExecProcesses[i].RecentChunks = append(s.UnifiedExecProcesses[i].RecentChunks, line)
		}
		if over := len(s.UnifiedExecProcesses[i].RecentChunks) - maxChunks; over > 0 {
			s.UnifiedExecProcesses[i].RecentChunks = s.UnifiedExecProcesses[i].RecentChunks[over:]
		}
		return
	}
}

func (s CommandLifecycleState) FooterCommands() []string {
	out := make([]string, 0, len(s.UnifiedExecProcesses))
	for _, process := range s.UnifiedExecProcesses {
		if strings.TrimSpace(process.CommandDisplay) != "" {
			out = append(out, process.CommandDisplay)
		}
	}
	return out
}

func (s *CommandLifecycleState) FlushUnifiedExecWaitStreak() (UnifiedExecInteractionEvent, bool) {
	if s == nil || s.UnifiedExecWait == nil {
		return UnifiedExecInteractionEvent{}, false
	}
	wait := *s.UnifiedExecWait
	s.UnifiedExecWait = nil
	return UnifiedExecInteractionEvent{
		ProcessID:      wait.ProcessID,
		CommandDisplay: wait.CommandDisplay,
	}, true
}

func (s *CommandLifecycleState) OnTerminalInteraction(processID string, stdin string, taskRunning bool) TerminalInteractionResult {
	if s == nil || !taskRunning {
		return TerminalInteractionResult{}
	}
	commandDisplay := s.commandDisplayForProcess(processID)
	if stdin == "" && commandDisplay == "" {
		return TerminalInteractionResult{}
	}

	result := TerminalInteractionResult{
		EnsureStatusIndicator: true,
		Redraw:                true,
	}
	if stdin == "" {
		result.WaitingStatus = true
		result.Status = "Waiting for background terminal"
		result.StatusDetails = commandDisplay
		result.InterruptHintVisible = true
		if s.UnifiedExecWait != nil && s.UnifiedExecWait.ProcessID != processID {
			if flushed, ok := s.FlushUnifiedExecWaitStreak(); ok {
				result.FlushedWait = &flushed
				result.NeedsFinalMessageSeparator = true
			}
		}
		if s.UnifiedExecWait == nil {
			wait := NewUnifiedExecWaitStreak(processID, commandDisplay)
			s.UnifiedExecWait = &wait
		} else {
			s.UnifiedExecWait.UpdateCommandDisplay(commandDisplay)
		}
		return result
	}

	if s.UnifiedExecWait != nil && s.UnifiedExecWait.ProcessID == processID {
		if flushed, ok := s.FlushUnifiedExecWaitStreak(); ok {
			result.FlushedWait = &flushed
			result.NeedsFinalMessageSeparator = true
		}
	}
	result.InsertedInteraction = &UnifiedExecInteractionEvent{
		ProcessID:      processID,
		CommandDisplay: commandDisplay,
		Stdin:          stdin,
	}
	return result
}

func (s *CommandLifecycleState) RecordCommandExecutionStarted(callID string, command []string, parsed []string, source ExecCommandSource) CommandStartDecision {
	if s == nil {
		return CommandStartDecision{}
	}
	if callID == "" {
		return CommandStartDecision{}
	}
	s.ensureCommandMaps()
	commandCopy := append([]string(nil), command...)
	parsedCopy := append([]string(nil), parsed...)
	s.RunningCommands[callID] = RunningCommand{Command: commandCopy, Parsed: parsedCopy, Source: source}
	commandDisplay := strings.Join(commandCopy, " ")
	decision := CommandStartDecision{
		Recorded:              true,
		EnsureStatusIndicator: true,
		CommandDisplay:        commandDisplay,
	}
	if source == ExecCommandSourceUnifiedExecInteraction {
		wait := NewUnifiedExecWaitState(commandDisplay)
		if s.LastUnifiedWait != nil && s.LastUnifiedWait.IsDuplicate(commandDisplay) {
			s.SuppressedExecCalls[callID] = true
			decision.Suppressed = true
			return decision
		}
		s.LastUnifiedWait = &wait
		return decision
	}
	s.LastUnifiedWait = nil
	return decision
}

func (s *CommandLifecycleState) RecordCommandExecutionCompleted(callID string) CommandCompletionDecision {
	if s == nil {
		return CommandCompletionDecision{}
	}
	running, known := s.RunningCommands[callID]
	delete(s.RunningCommands, callID)
	_, suppressed := s.SuppressedExecCalls[callID]
	delete(s.SuppressedExecCalls, callID)
	if suppressed {
		return CommandCompletionDecision{KnownRunning: known, Running: running, Suppressed: true}
	}
	return CommandCompletionDecision{
		KnownRunning:             known,
		Running:                  running,
		HadWorkActivity:          true,
		MaybeSendNextQueuedInput: known && running.Source == ExecCommandSourceUserShell,
	}
}

func UnifiedExecCommandDisplay(command string) string {
	parts := SplitCommandString(command)
	if len(parts) == 0 {
		return strings.TrimSpace(command)
	}
	return shell.StripShellCommandAndEscape(parts)
}

func SplitCommandString(command string) []string {
	parts, ok := splitCommandStringLoose(command)
	if !ok {
		return []string{command}
	}
	if len(parts) == 0 {
		return nil
	}
	roundTrip := shell.ShlexJoin(parts)
	if roundTrip == command || (!strings.Contains(command, `:\`) && stringSlicesEqual(splitCommandStringLooseMust(roundTrip), parts)) {
		return parts
	}
	return []string{command}
}

func splitCommandStringLooseMust(command string) []string {
	parts, ok := splitCommandStringLoose(command)
	if !ok {
		return []string{command}
	}
	return parts
}

func splitCommandStringLoose(command string) ([]string, bool) {
	var parts []string
	var builder strings.Builder
	var quote rune
	escaped := false
	for _, r := range command {
		switch {
		case escaped:
			builder.WriteRune(r)
			escaped = false
		case r == '\\' && quote != 0:
			escaped = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				builder.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
		case r == ' ' || r == '\t' || r == '\r' || r == '\n':
			if builder.Len() > 0 {
				parts = append(parts, builder.String())
				builder.Reset()
			}
		default:
			builder.WriteRune(r)
		}
	}
	if escaped {
		builder.WriteRune('\\')
	}
	if quote != 0 {
		return nil, false
	}
	if builder.Len() > 0 {
		parts = append(parts, builder.String())
	}
	return parts, true
}

func (s *CommandLifecycleState) commandDisplayForProcess(processID string) string {
	for _, process := range s.UnifiedExecProcesses {
		if process.Key == processID {
			return process.CommandDisplay
		}
	}
	return ""
}

func (s *CommandLifecycleState) ensureCommandMaps() {
	if s.RunningCommands == nil {
		s.RunningCommands = map[string]RunningCommand{}
	}
	if s.SuppressedExecCalls == nil {
		s.SuppressedExecCalls = map[string]bool{}
	}
}

func stringSlicesEqual(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
