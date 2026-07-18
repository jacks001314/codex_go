package chatwidget

import "strings"

const StatusDetailsDefaultMaxLines = 3

type StatusIndicatorState struct {
	Header          string
	Details         string
	DetailsMaxLines int
}

func WorkingStatusIndicatorState() StatusIndicatorState {
	return StatusIndicatorState{
		Header:          "Working",
		DetailsMaxLines: StatusDetailsDefaultMaxLines,
	}
}

func (s StatusIndicatorState) IsGuardianReview() bool {
	header := strings.TrimSpace(s.Header)
	return header == "Reviewing approval request" || strings.HasPrefix(header, "Reviewing ")
}

type TerminalTitleStatusKind string

const (
	TerminalTitleStatusWorking                      TerminalTitleStatusKind = "working"
	TerminalTitleStatusWaitingForBackgroundTerminal TerminalTitleStatusKind = "waiting_for_background_terminal"
	TerminalTitleStatusThinking                     TerminalTitleStatusKind = "thinking"
)

type PendingGuardianReviewStatus struct {
	entries []pendingGuardianReviewStatusEntry
}

type pendingGuardianReviewStatusEntry struct {
	id     string
	detail string
}

func (s *PendingGuardianReviewStatus) StartOrUpdate(id string, detail string) {
	if s == nil {
		return
	}
	id = strings.TrimSpace(id)
	detail = strings.TrimSpace(detail)
	for i := range s.entries {
		if s.entries[i].id == id {
			s.entries[i].detail = detail
			return
		}
	}
	s.entries = append(s.entries, pendingGuardianReviewStatusEntry{id: id, detail: detail})
}

func (s *PendingGuardianReviewStatus) Finish(id string) bool {
	if s == nil {
		return false
	}
	original := len(s.entries)
	id = strings.TrimSpace(id)
	s.entries = removeGuardianEntry(s.entries, id)
	return len(s.entries) != original
}

func (s *PendingGuardianReviewStatus) IsEmpty() bool {
	return s == nil || len(s.entries) == 0
}

func (s *PendingGuardianReviewStatus) StatusIndicatorState() (StatusIndicatorState, bool) {
	if s == nil || len(s.entries) == 0 {
		return StatusIndicatorState{}, false
	}
	details := ""
	detailsMaxLines := 1
	if len(s.entries) == 1 {
		details = s.entries[0].detail
	} else {
		limit := len(s.entries)
		if limit > 3 {
			limit = 3
		}
		lines := make([]string, 0, limit+1)
		for _, entry := range s.entries[:limit] {
			lines = append(lines, "- "+entry.detail)
		}
		if remaining := len(s.entries) - limit; remaining > 0 {
			lines = append(lines, "+"+formatInt64(int64(remaining))+" more")
		}
		details = strings.Join(lines, "\n")
		detailsMaxLines = 4
	}
	if strings.TrimSpace(details) == "" {
		return StatusIndicatorState{}, false
	}
	header := "Reviewing approval request"
	if len(s.entries) != 1 {
		header = "Reviewing " + formatInt64(int64(len(s.entries))) + " approval requests"
	}
	return StatusIndicatorState{
		Header:          header,
		Details:         details,
		DetailsMaxLines: detailsMaxLines,
	}, true
}

type StatusState struct {
	CurrentStatus                 StatusIndicatorState
	PendingGuardianReviewStatus   PendingGuardianReviewStatus
	TerminalTitleStatusKind       TerminalTitleStatusKind
	RetryStatusHeader             string
	PendingStatusIndicatorRestore bool
}

func NewStatusState() StatusState {
	return StatusState{
		CurrentStatus:           WorkingStatusIndicatorState(),
		TerminalTitleStatusKind: TerminalTitleStatusWorking,
	}
}

func (s *StatusState) SetStatus(status StatusIndicatorState) {
	if s == nil {
		return
	}
	if strings.TrimSpace(status.Header) == "" {
		status.Header = "Working"
	}
	if status.DetailsMaxLines <= 0 {
		status.DetailsMaxLines = StatusDetailsDefaultMaxLines
	}
	s.CurrentStatus = status
}

func (s *StatusState) RememberRetryStatusHeader() {
	if s == nil || strings.TrimSpace(s.RetryStatusHeader) != "" {
		return
	}
	s.RetryStatusHeader = s.CurrentStatus.Header
}

func (s *StatusState) TakeRetryStatusHeader() (string, bool) {
	if s == nil || strings.TrimSpace(s.RetryStatusHeader) == "" {
		return "", false
	}
	value := s.RetryStatusHeader
	s.RetryStatusHeader = ""
	return value, true
}

func removeGuardianEntry(entries []pendingGuardianReviewStatusEntry, id string) []pendingGuardianReviewStatusEntry {
	out := entries[:0]
	for _, entry := range entries {
		if entry.id != id {
			out = append(out, entry)
		}
	}
	return out
}
