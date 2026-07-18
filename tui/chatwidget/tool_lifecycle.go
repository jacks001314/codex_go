package chatwidget

import (
	"codex_go/tui"
	historycell "codex_go/tui/history_cell"
)

type ToolLifecycleStatus string

const (
	ToolLifecycleStarted   ToolLifecycleStatus = "started"
	ToolLifecycleCompleted ToolLifecycleStatus = "completed"
	ToolLifecycleFailed    ToolLifecycleStatus = "failed"
	ToolLifecycleWaiting   ToolLifecycleStatus = "waiting"
)

type ToolLifecycleItem struct {
	ID     string
	Name   string
	Status ToolLifecycleStatus
}

type ToolLifecycleActiveCellKind string

const (
	ToolLifecycleNoActiveCell        ToolLifecycleActiveCellKind = ""
	ToolLifecycleActiveMcpToolCall   ToolLifecycleActiveCellKind = "mcp_tool_call"
	ToolLifecycleActiveWebSearchCall ToolLifecycleActiveCellKind = "web_search"
)

type ToolLifecycleHistoryKind string

const (
	ToolLifecycleHistoryPatchEvent      ToolLifecycleHistoryKind = "patch_event"
	ToolLifecycleHistoryPatchFailure    ToolLifecycleHistoryKind = "patch_failure"
	ToolLifecycleHistoryViewImage       ToolLifecycleHistoryKind = "view_image"
	ToolLifecycleHistoryImageGeneration ToolLifecycleHistoryKind = "image_generation"
	ToolLifecycleHistoryMcpToolCall     ToolLifecycleHistoryKind = "mcp_tool_call"
	ToolLifecycleHistoryWebSearch       ToolLifecycleHistoryKind = "web_search"
	ToolLifecycleHistoryCollabEvent     ToolLifecycleHistoryKind = "collab_event"
)

type ToolLifecycleAction string

const (
	ToolLifecycleActionRecordVisibleTurnActivity ToolLifecycleAction = "record_visible_turn_activity"
	ToolLifecycleActionFlushAnswerStream         ToolLifecycleAction = "flush_answer_stream_with_separator"
	ToolLifecycleActionFlushActiveCell           ToolLifecycleAction = "flush_active_cell"
	ToolLifecycleActionBumpActiveCellRevision    ToolLifecycleAction = "bump_active_cell_revision"
	ToolLifecycleActionRequestRedraw             ToolLifecycleAction = "request_redraw"
)

type ToolLifecycleHistoryEvent struct {
	Kind ToolLifecycleHistoryKind
	ID   string
	Cell historycell.HistoryCell
}

type ToolLifecycleResult struct {
	Actions                     []ToolLifecycleAction
	History                     []ToolLifecycleHistoryEvent
	ActiveCellKind              ToolLifecycleActiveCellKind
	ActiveCellRevision          int
	RecordedVisibleTurnActivity bool
	HadWorkActivity             bool
	Handled                     bool
}

type PatchApplyStatus string

const (
	PatchApplyStatusCompleted PatchApplyStatus = "completed"
	PatchApplyStatusFailed    PatchApplyStatus = "failed"
)

type ToolLifecycleThreadItemKind string

const (
	ToolLifecycleThreadItemCommandExecution ToolLifecycleThreadItemKind = "command_execution"
	ToolLifecycleThreadItemFileChange       ToolLifecycleThreadItemKind = "file_change"
	ToolLifecycleThreadItemMcpToolCall      ToolLifecycleThreadItemKind = "mcp_tool_call"
)

type ToolLifecycleThreadItem struct {
	Kind        ToolLifecycleThreadItemKind
	ID          string
	Server      string
	Tool        string
	Arguments   string
	Result      []string
	Error       string
	DurationMS  int64
	PatchStatus PatchApplyStatus
}

type ToolLifecycleState struct {
	Active             []ToolLifecycleItem
	ActiveMcpToolCall  *historycell.McpToolCallCell
	ActiveWebSearch    *historycell.WebSearchCell
	ActiveCellRevision int
	History            []ToolLifecycleHistoryEvent
	HadWorkActivity    bool
}

func (s *ToolLifecycleState) Start(item ToolLifecycleItem) {
	if s == nil || item.ID == "" {
		return
	}
	item.Status = ToolLifecycleStarted
	for i := range s.Active {
		if s.Active[i].ID == item.ID {
			s.Active[i] = item
			return
		}
	}
	s.Active = append(s.Active, item)
}

func (s *ToolLifecycleState) Finish(id string, status ToolLifecycleStatus) bool {
	if s == nil {
		return false
	}
	for i := range s.Active {
		if s.Active[i].ID == id {
			s.Active[i].Status = status
			s.Active = append(s.Active[:i], s.Active[i+1:]...)
			return true
		}
	}
	return false
}

func (s *ToolLifecycleState) OnPatchApplyBegin(changes map[string]tui.FileChange, cwd string) ToolLifecycleResult {
	if s == nil {
		return ToolLifecycleResult{}
	}
	result := s.recordVisibleTurnActivity()
	event := ToolLifecycleHistoryEvent{
		Kind: ToolLifecycleHistoryPatchEvent,
		Cell: historycell.NewPatchEvent(changes, cwd),
	}
	s.addHistory(event, &result)
	result.Handled = true
	return result
}

func (s *ToolLifecycleState) OnViewImageToolCall(path string, cwd string) ToolLifecycleResult {
	if s == nil {
		return ToolLifecycleResult{}
	}
	result := s.recordVisibleTurnActivity()
	s.appendAction(&result, ToolLifecycleActionFlushAnswerStream)
	event := ToolLifecycleHistoryEvent{
		Kind: ToolLifecycleHistoryViewImage,
		Cell: historycell.NewViewImageToolCall(path, cwd),
	}
	s.addHistory(event, &result)
	s.appendAction(&result, ToolLifecycleActionRequestRedraw)
	result.Handled = true
	return result
}

func (s *ToolLifecycleState) OnImageGenerationBegin() ToolLifecycleResult {
	if s == nil {
		return ToolLifecycleResult{}
	}
	result := s.recordVisibleTurnActivity()
	s.appendAction(&result, ToolLifecycleActionFlushAnswerStream)
	result.Handled = true
	return result
}

func (s *ToolLifecycleState) OnImageGenerationEnd(callID string, status string, revisedPrompt string, savedPath string) ToolLifecycleResult {
	if s == nil {
		return ToolLifecycleResult{}
	}
	result := ToolLifecycleResult{}
	s.appendAction(&result, ToolLifecycleActionFlushAnswerStream)
	event := ToolLifecycleHistoryEvent{
		Kind: ToolLifecycleHistoryImageGeneration,
		ID:   callID,
		Cell: historycell.NewImageGenerationCall(callID, status, revisedPrompt, savedPath),
	}
	s.addHistory(event, &result)
	s.appendAction(&result, ToolLifecycleActionRequestRedraw)
	result.Handled = true
	return result
}

func (s *ToolLifecycleState) OnFileChangeCompleted(item ToolLifecycleThreadItem) ToolLifecycleResult {
	return s.HandleFileChangeCompletedNow(item)
}

func (s *ToolLifecycleState) OnMcpToolCallStarted(item ToolLifecycleThreadItem) ToolLifecycleResult {
	return s.HandleMcpToolCallStartedNow(item)
}

func (s *ToolLifecycleState) OnMcpToolCallCompleted(item ToolLifecycleThreadItem) ToolLifecycleResult {
	return s.HandleMcpToolCallCompletedNow(item)
}

func (s *ToolLifecycleState) OnWebSearchBegin(callID string) ToolLifecycleResult {
	if s == nil {
		return ToolLifecycleResult{}
	}
	result := s.recordVisibleTurnActivity()
	s.appendAction(&result, ToolLifecycleActionFlushAnswerStream)
	s.flushActiveCell(&result)
	cell := historycell.NewActiveWebSearchCall(callID, "")
	s.ActiveWebSearch = &cell
	s.ActiveMcpToolCall = nil
	s.bumpActiveCellRevision(&result)
	s.appendAction(&result, ToolLifecycleActionRequestRedraw)
	result.ActiveCellKind = s.ActiveCellKind()
	result.Handled = true
	return result
}

func (s *ToolLifecycleState) OnWebSearchEnd(callID string, query string, action historycell.WebSearchAction) ToolLifecycleResult {
	if s == nil {
		return ToolLifecycleResult{}
	}
	result := ToolLifecycleResult{}
	s.appendAction(&result, ToolLifecycleActionFlushAnswerStream)
	handledActive := false
	if s.ActiveWebSearch != nil && s.ActiveWebSearch.CallID == callID {
		s.ActiveWebSearch.Update(action, query)
		s.ActiveWebSearch.Complete()
		s.bumpActiveCellRevision(&result)
		s.flushActiveCell(&result)
		handledActive = true
	}
	if !handledActive {
		event := ToolLifecycleHistoryEvent{
			Kind: ToolLifecycleHistoryWebSearch,
			ID:   callID,
			Cell: historycell.NewWebSearchCall(callID, query, action),
		}
		s.addHistory(event, &result)
	}
	s.HadWorkActivity = true
	result.HadWorkActivity = true
	result.Handled = true
	return result
}

func (s *ToolLifecycleState) OnCollabEvent(cell historycell.PlainHistoryCell) ToolLifecycleResult {
	if s == nil {
		return ToolLifecycleResult{}
	}
	result := ToolLifecycleResult{}
	s.appendAction(&result, ToolLifecycleActionFlushAnswerStream)
	s.addHistory(ToolLifecycleHistoryEvent{Kind: ToolLifecycleHistoryCollabEvent, Cell: cell}, &result)
	s.appendAction(&result, ToolLifecycleActionRequestRedraw)
	result.Handled = true
	return result
}

func (s *ToolLifecycleState) HandleFileChangeCompletedNow(item ToolLifecycleThreadItem) ToolLifecycleResult {
	if s == nil || item.Kind != ToolLifecycleThreadItemFileChange {
		return ToolLifecycleResult{}
	}
	result := ToolLifecycleResult{Handled: true}
	if item.PatchStatus == PatchApplyStatusFailed {
		s.addHistory(ToolLifecycleHistoryEvent{
			Kind: ToolLifecycleHistoryPatchFailure,
			ID:   item.ID,
			Cell: historycell.NewPatchApplyFailure(""),
		}, &result)
	}
	s.HadWorkActivity = true
	result.HadWorkActivity = true
	return result
}

func (s *ToolLifecycleState) HandleMcpToolCallStartedNow(item ToolLifecycleThreadItem) ToolLifecycleResult {
	if s == nil || item.Kind != ToolLifecycleThreadItemMcpToolCall {
		return ToolLifecycleResult{}
	}
	result := s.recordVisibleTurnActivity()
	s.appendAction(&result, ToolLifecycleActionFlushAnswerStream)
	s.flushActiveCell(&result)
	cell := historycell.NewActiveMcpToolCall(item.ID, historycell.McpInvocation{
		Server:    item.Server,
		Tool:      item.Tool,
		Arguments: item.Arguments,
	})
	s.ActiveMcpToolCall = &cell
	s.ActiveWebSearch = nil
	s.Start(ToolLifecycleItem{ID: item.ID, Name: item.Tool})
	s.bumpActiveCellRevision(&result)
	s.appendAction(&result, ToolLifecycleActionRequestRedraw)
	result.ActiveCellKind = s.ActiveCellKind()
	result.Handled = true
	return result
}

func (s *ToolLifecycleState) HandleMcpToolCallCompletedNow(item ToolLifecycleThreadItem) ToolLifecycleResult {
	if s == nil || item.Kind != ToolLifecycleThreadItemMcpToolCall {
		return ToolLifecycleResult{}
	}
	result := ToolLifecycleResult{Handled: true}
	s.appendAction(&result, ToolLifecycleActionFlushAnswerStream)
	mcpResult := mcpToolResultFromItem(item)
	if s.ActiveMcpToolCall != nil && s.ActiveMcpToolCall.CallID == item.ID {
		s.ActiveMcpToolCall.Complete(mcpResult)
		s.flushActiveCell(&result)
	} else {
		s.flushActiveCell(&result)
		cell := historycell.NewMcpToolCall(item.ID, historycell.McpInvocation{
			Server:    item.Server,
			Tool:      item.Tool,
			Arguments: item.Arguments,
		}, mcpResult)
		s.addHistory(ToolLifecycleHistoryEvent{
			Kind: ToolLifecycleHistoryMcpToolCall,
			ID:   item.ID,
			Cell: cell,
		}, &result)
	}
	s.Finish(item.ID, ToolLifecycleCompleted)
	s.HadWorkActivity = true
	result.HadWorkActivity = true
	result.ActiveCellKind = s.ActiveCellKind()
	return result
}

func (s *ToolLifecycleState) HandleQueuedItemStartedNow(item ToolLifecycleThreadItem) ToolLifecycleResult {
	switch item.Kind {
	case ToolLifecycleThreadItemMcpToolCall:
		return s.HandleMcpToolCallStartedNow(item)
	default:
		return ToolLifecycleResult{}
	}
}

func (s *ToolLifecycleState) HandleQueuedItemCompletedNow(item ToolLifecycleThreadItem) ToolLifecycleResult {
	switch item.Kind {
	case ToolLifecycleThreadItemFileChange:
		return s.HandleFileChangeCompletedNow(item)
	case ToolLifecycleThreadItemMcpToolCall:
		return s.HandleMcpToolCallCompletedNow(item)
	default:
		return ToolLifecycleResult{}
	}
}

func (s *ToolLifecycleState) ActiveCellKind() ToolLifecycleActiveCellKind {
	if s == nil {
		return ToolLifecycleNoActiveCell
	}
	if s.ActiveMcpToolCall != nil {
		return ToolLifecycleActiveMcpToolCall
	}
	if s.ActiveWebSearch != nil {
		return ToolLifecycleActiveWebSearchCall
	}
	return ToolLifecycleNoActiveCell
}

func (s *ToolLifecycleState) recordVisibleTurnActivity() ToolLifecycleResult {
	result := ToolLifecycleResult{
		RecordedVisibleTurnActivity: true,
	}
	s.appendAction(&result, ToolLifecycleActionRecordVisibleTurnActivity)
	return result
}

func (s *ToolLifecycleState) addHistory(event ToolLifecycleHistoryEvent, result *ToolLifecycleResult) {
	if s == nil || result == nil {
		return
	}
	s.History = append(s.History, event)
	result.History = append(result.History, event)
}

func (s *ToolLifecycleState) flushActiveCell(result *ToolLifecycleResult) {
	if s == nil || result == nil {
		return
	}
	if s.ActiveMcpToolCall != nil {
		s.addHistory(ToolLifecycleHistoryEvent{
			Kind: ToolLifecycleHistoryMcpToolCall,
			ID:   s.ActiveMcpToolCall.CallID,
			Cell: *s.ActiveMcpToolCall,
		}, result)
		s.ActiveMcpToolCall = nil
		s.appendAction(result, ToolLifecycleActionFlushActiveCell)
	}
	if s.ActiveWebSearch != nil {
		s.addHistory(ToolLifecycleHistoryEvent{
			Kind: ToolLifecycleHistoryWebSearch,
			ID:   s.ActiveWebSearch.CallID,
			Cell: *s.ActiveWebSearch,
		}, result)
		s.ActiveWebSearch = nil
		s.appendAction(result, ToolLifecycleActionFlushActiveCell)
	}
	result.ActiveCellKind = s.ActiveCellKind()
}

func (s *ToolLifecycleState) bumpActiveCellRevision(result *ToolLifecycleResult) {
	if s == nil || result == nil {
		return
	}
	s.ActiveCellRevision++
	result.ActiveCellRevision = s.ActiveCellRevision
	s.appendAction(result, ToolLifecycleActionBumpActiveCellRevision)
}

func (s *ToolLifecycleState) appendAction(result *ToolLifecycleResult, action ToolLifecycleAction) {
	if result == nil {
		return
	}
	result.Actions = append(result.Actions, action)
}

func mcpToolResultFromItem(item ToolLifecycleThreadItem) historycell.McpToolResult {
	if item.Error != "" {
		return historycell.McpToolResult{Error: item.Error, IsError: true}
	}
	if item.Result == nil {
		return historycell.McpToolResult{Error: "MCP tool call completed without a result", IsError: true}
	}
	return historycell.McpToolResult{Content: append([]string(nil), item.Result...)}
}
