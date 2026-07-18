package chatwidget

import "strings"

type ExecCommandSource string

const (
	ExecCommandSourceAgent                  ExecCommandSource = "agent"
	ExecCommandSourceUserShell              ExecCommandSource = "user_shell"
	ExecCommandSourceUnifiedExecStartup     ExecCommandSource = "unified_exec_startup"
	ExecCommandSourceUnifiedExecInteraction ExecCommandSource = "unified_exec_interaction"
)

type RunningCommand struct {
	Command []string
	Parsed  []string
	Source  ExecCommandSource
}

type UnifiedExecProcessSummary struct {
	Key            string
	CallID         string
	CommandDisplay string
	RecentChunks   []string
}

type UnifiedExecWaitState struct {
	CommandDisplay string
}

type UnifiedExecWaitStreak struct {
	ProcessID      string
	CommandDisplay string
}

func NewUnifiedExecWaitState(commandDisplay string) UnifiedExecWaitState {
	return UnifiedExecWaitState{CommandDisplay: strings.TrimSpace(commandDisplay)}
}

func (s UnifiedExecWaitState) IsDuplicate(commandDisplay string) bool {
	return strings.TrimSpace(s.CommandDisplay) == strings.TrimSpace(commandDisplay)
}

func NewUnifiedExecWaitStreak(processID string, commandDisplay string) UnifiedExecWaitStreak {
	return UnifiedExecWaitStreak{ProcessID: strings.TrimSpace(processID), CommandDisplay: strings.TrimSpace(commandDisplay)}
}

func (s *UnifiedExecWaitStreak) UpdateCommandDisplay(commandDisplay string) {
	if s == nil || strings.TrimSpace(s.CommandDisplay) != "" {
		return
	}
	s.CommandDisplay = strings.TrimSpace(commandDisplay)
}

func IsUnifiedExecSource(source ExecCommandSource) bool {
	return source == ExecCommandSourceUnifiedExecStartup || source == ExecCommandSourceUnifiedExecInteraction
}

func IsStandardToolCall(parsed []string) bool {
	if len(parsed) == 0 {
		return false
	}
	for _, item := range parsed {
		if strings.EqualFold(strings.TrimSpace(item), "unknown") || strings.TrimSpace(item) == "" {
			return false
		}
	}
	return true
}
