package chatwidget

import "strings"

const MaxAgentCopyHistory = 32

type AgentTurnMarkdown struct {
	UserTurnCount int
	Markdown      string
}

type PlanProgress struct {
	Completed int
	Total     int
}

type TranscriptState struct {
	LastAssistantMarkdown        string
	Overlay                      *TranscriptOverlay
	ActiveCellRevision           uint64
	AgentTurnMarkdowns           []AgentTurnMarkdown
	VisibleUserTurnCount         int
	CopyHistoryEvictedByRollback bool
	LatestProposedPlanMarkdown   string
	SawCopySourceThisTurn        bool
	NeedsFinalMessageSeparator   bool
	HadWorkActivity              bool
	SawPlanUpdateThisTurn        bool
	SawPlanItemThisTurn          bool
	LastPlanProgress             *PlanProgress
	PlanDeltaBuffer              string
	PlanItemActive               bool
}

func (s *TranscriptState) SetLastAssistantMarkdown(markdown string) {
	if s == nil {
		return
	}
	s.LastAssistantMarkdown = markdown
}

func (s TranscriptState) LastAssistantMarkdownForCopy() (string, bool) {
	return stringsTrimmedNonEmpty(s.LastAssistantMarkdown)
}

func (s *TranscriptState) BumpActiveCellRevision() {
	if s != nil {
		s.ActiveCellRevision++
	}
}

func (s *TranscriptState) RecordAgentMarkdown(markdown string) {
	if s == nil {
		return
	}
	switch {
	case len(s.AgentTurnMarkdowns) > 0 && s.AgentTurnMarkdowns[len(s.AgentTurnMarkdowns)-1].UserTurnCount == s.VisibleUserTurnCount:
		s.AgentTurnMarkdowns[len(s.AgentTurnMarkdowns)-1].Markdown = markdown
	default:
		s.AgentTurnMarkdowns = append(s.AgentTurnMarkdowns, AgentTurnMarkdown{
			UserTurnCount: s.VisibleUserTurnCount,
			Markdown:      markdown,
		})
		if len(s.AgentTurnMarkdowns) > MaxAgentCopyHistory {
			s.AgentTurnMarkdowns = append([]AgentTurnMarkdown(nil), s.AgentTurnMarkdowns[len(s.AgentTurnMarkdowns)-MaxAgentCopyHistory:]...)
		}
	}
	s.LastAssistantMarkdown = markdown
	s.CopyHistoryEvictedByRollback = false
	s.SawCopySourceThisTurn = true
}

func (s *TranscriptState) RecordVisibleUserTurn() {
	if s != nil {
		s.VisibleUserTurnCount++
	}
}

func (s *TranscriptState) ResetCopyHistory() {
	if s == nil {
		return
	}
	s.LastAssistantMarkdown = ""
	s.AgentTurnMarkdowns = nil
	s.VisibleUserTurnCount = 0
	s.CopyHistoryEvictedByRollback = false
	s.SawCopySourceThisTurn = false
}

func (s *TranscriptState) TruncateCopyHistoryToUserTurnCount(userTurnCount int) {
	if s == nil {
		return
	}
	if userTurnCount < 0 {
		userTurnCount = 0
	}
	s.VisibleUserTurnCount = userTurnCount
	hadCopyHistory := len(s.AgentTurnMarkdowns) > 0
	out := s.AgentTurnMarkdowns[:0]
	for _, entry := range s.AgentTurnMarkdowns {
		if entry.UserTurnCount <= userTurnCount {
			out = append(out, entry)
		}
	}
	s.AgentTurnMarkdowns = out
	s.LastAssistantMarkdown = ""
	if len(s.AgentTurnMarkdowns) > 0 {
		s.LastAssistantMarkdown = s.AgentTurnMarkdowns[len(s.AgentTurnMarkdowns)-1].Markdown
	}
	s.CopyHistoryEvictedByRollback = hadCopyHistory && len(s.AgentTurnMarkdowns) == 0
	s.SawCopySourceThisTurn = false
}

func (s *TranscriptState) ResetTurnFlags() {
	if s == nil {
		return
	}
	s.SawCopySourceThisTurn = false
	s.SawPlanUpdateThisTurn = false
	s.SawPlanItemThisTurn = false
	s.HadWorkActivity = false
	s.LatestProposedPlanMarkdown = ""
	s.PlanDeltaBuffer = ""
	s.PlanItemActive = false
}

func (s *TranscriptState) RecordPlanProgress(completed int, total int) {
	if s == nil {
		return
	}
	if completed < 0 {
		completed = 0
	}
	if total <= 0 {
		s.LastPlanProgress = nil
		return
	}
	if completed > total {
		completed = total
	}
	progress := PlanProgress{Completed: completed, Total: total}
	s.LastPlanProgress = &progress
	s.SawPlanUpdateThisTurn = true
}

func stringsTrimmedNonEmpty(value string) (string, bool) {
	value = strings.TrimSpace(value)
	return value, value != ""
}
