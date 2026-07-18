package chatwidget

import "strings"

type IdeContextState struct {
	Enabled           bool
	PromptFetchWarned bool
}

type IdeCommandResult struct {
	Enabled bool
	Message string
	Hint    string
	Error   bool
}

func (s *IdeContextState) HandleCommand(args string, contextAvailable bool, hasPromptContext bool, unavailableHint string) IdeCommandResult {
	if s == nil {
		return IdeCommandResult{}
	}
	switch strings.ToLower(args) {
	case "":
		if s.Enabled {
			return s.disableResult()
		}
		return s.enableResult(contextAvailable, hasPromptContext, unavailableHint)
	case "on":
		return s.enableResult(contextAvailable, hasPromptContext, unavailableHint)
	case "off":
		return s.disableResult()
	case "status":
		if !s.Enabled {
			return IdeCommandResult{Message: "IDE context is off."}
		}
		return s.enableResult(contextAvailable, hasPromptContext, unavailableHint)
	default:
		return IdeCommandResult{Message: "Usage: /ide [on|off|status]", Error: true, Enabled: s.Enabled}
	}
}

func (s *IdeContextState) MarkAvailable() {
	if s == nil {
		return
	}
	s.PromptFetchWarned = false
}

func (s *IdeContextState) MarkPromptFetchSkipped(hint string) (IdeCommandResult, bool) {
	if s == nil || !s.Enabled || s.PromptFetchWarned {
		return IdeCommandResult{}, false
	}
	s.PromptFetchWarned = true
	return IdeCommandResult{
		Enabled: true,
		Message: "IDE context was skipped for this message.",
		Hint:    hint,
	}, true
}

func (s *IdeContextState) enableResult(contextAvailable bool, hasPromptContext bool, unavailableHint string) IdeCommandResult {
	if !contextAvailable {
		s.Enabled = false
		s.PromptFetchWarned = false
		return IdeCommandResult{
			Message: "IDE context could not be enabled.",
			Hint:    unavailableHint,
		}
	}
	s.Enabled = true
	s.PromptFetchWarned = false
	return s.enabledStatus(hasPromptContext)
}

func (s *IdeContextState) enabledStatus(hasPromptContext bool) IdeCommandResult {
	hint := "Connected to your IDE."
	if hasPromptContext {
		hint = "Future messages will include your current IDE selection and open tabs."
	}
	return IdeCommandResult{Enabled: true, Message: "IDE context is on.", Hint: hint}
}

func (s *IdeContextState) disableResult() IdeCommandResult {
	s.Enabled = false
	s.PromptFetchWarned = false
	return IdeCommandResult{Message: "IDE context is off."}
}
