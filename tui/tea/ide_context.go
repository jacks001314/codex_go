package tea

import (
	"errors"
	"strings"

	chatwidget "codex_go/tui/chatwidget"
	historycell "codex_go/tui/history_cell"
	idecontext "codex_go/tui/ide_context"
)

func (m *Model) applyIDECommand(args string) {
	if m == nil {
		return
	}
	args = strings.ToLower(strings.TrimSpace(args))
	if args != "" && args != "on" && args != "off" && args != "status" {
		m.applyIDECommandResult(m.ideContext.HandleCommand(args, false, false, ""))
		return
	}

	shouldFetch := args == "on" || (args == "" && !m.ideContext.Enabled) || (args == "status" && m.ideContext.Enabled)
	if !shouldFetch {
		m.applyIDECommandResult(m.ideContext.HandleCommand(args, false, false, ""))
		return
	}

	context, err := m.readIDEContext()
	result := m.ideContext.HandleCommand(args, err == nil, idecontext.HasPromptContext(context), ideContextUserFacingHint(err))
	m.applyIDECommandResult(result)
}

func (m *Model) captureIDEContext(request *SubmitRequest) {
	if m == nil || request == nil || !m.ideContext.Enabled {
		return
	}
	context, err := m.readIDEContext()
	if err == nil {
		m.ideContext.MarkAvailable()
		request.IDEContext = cloneIDEContext(context)
		return
	}
	result, ok := m.ideContext.MarkPromptFetchSkipped(ideContextPromptSkipHint(err))
	if ok {
		m.applyIDECommandResult(result)
	}
}

func (m *Model) readIDEContext() (*idecontext.IdeContext, error) {
	if m == nil || m.onReadIDEContext == nil {
		return nil, &idecontext.IdeContextError{Kind: idecontext.IdeContextErrorConnect}
	}
	return m.onReadIDEContext(m.ideContextCWD())
}

func (m *Model) ideContextCWD() string {
	if m == nil {
		return ""
	}
	if m.State != nil {
		if cwd := strings.TrimSpace(m.State.CWD); cwd != "" {
			return cwd
		}
	}
	return strings.TrimSpace(m.sessionCWD)
}

func (m *Model) applyIDECommandResult(result chatwidget.IdeCommandResult) {
	if m == nil || strings.TrimSpace(result.Message) == "" {
		return
	}
	m.notice = ""
	if result.Error {
		m.applyHistoryCell(historycell.NewErrorEvent(result.Message))
		return
	}
	m.applyHistoryCell(historycell.NewInfoEvent(result.Message, result.Hint))
}

func ideContextUserFacingHint(err error) string {
	if err == nil {
		return ""
	}
	var contextErr *idecontext.IdeContextError
	if errors.As(err, &contextErr) {
		return contextErr.UserFacingHint()
	}
	return strings.TrimSpace(err.Error())
}

func ideContextPromptSkipHint(err error) string {
	if err == nil {
		return ""
	}
	var contextErr *idecontext.IdeContextError
	if errors.As(err, &contextErr) {
		return contextErr.PromptSkipHint()
	}
	return strings.TrimSpace(err.Error())
}
