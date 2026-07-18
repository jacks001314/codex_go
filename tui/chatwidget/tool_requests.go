package chatwidget

import (
	"strings"

	historycell "codex_go/tui/history_cell"
)

type ToolRequestKind string

const (
	ToolRequestExecApproval   ToolRequestKind = "exec_approval"
	ToolRequestPatchApproval  ToolRequestKind = "patch_approval"
	ToolRequestMcpElicitation ToolRequestKind = "mcp_elicitation"
	ToolRequestPermissions    ToolRequestKind = "permissions"
	ToolRequestUserInput      ToolRequestKind = "user_input"
)

type ToolRequest struct {
	Kind       ToolRequestKind
	ID         string
	CallID     string
	ApprovalID string
	TurnID     string
	ServerName string
	RequestID  string
	Title      string
	Summary    string
}

type GuardianAssessmentStatus string

const (
	GuardianAssessmentInProgress GuardianAssessmentStatus = "in_progress"
	GuardianAssessmentApproved   GuardianAssessmentStatus = "approved"
	GuardianAssessmentDenied     GuardianAssessmentStatus = "denied"
	GuardianAssessmentTimedOut   GuardianAssessmentStatus = "timed_out"
)

type GuardianAssessmentActionKind string

const (
	GuardianActionCommand            GuardianAssessmentActionKind = "command"
	GuardianActionExecve             GuardianAssessmentActionKind = "execve"
	GuardianActionApplyPatch         GuardianAssessmentActionKind = "apply_patch"
	GuardianActionNetworkAccess      GuardianAssessmentActionKind = "network_access"
	GuardianActionMcpToolCall        GuardianAssessmentActionKind = "mcp_tool_call"
	GuardianActionRequestPermissions GuardianAssessmentActionKind = "request_permissions"
)

type GuardianAssessmentAction struct {
	Kind          GuardianAssessmentActionKind
	Command       string
	Program       string
	Argv          []string
	Files         []string
	Target        string
	Server        string
	ToolName      string
	ConnectorName string
	Reason        string
}

type GuardianAssessmentEvent struct {
	ID     string
	Status GuardianAssessmentStatus
	Action GuardianAssessmentAction
}

type GuardianAssessmentResult struct {
	InProgress             bool
	EnsureStatusIndicator  bool
	InterruptHintVisible   bool
	Status                 *StatusIndicatorState
	ClearGuardianStatus    bool
	HistoryMessage         string
	RecentAutoReviewDenial *AutoReviewDenialEntry
	Redraw                 bool
}

type ToolRequestRuntimeState struct {
	PendingGuardianReviewStatus PendingGuardianReviewStatus
	CurrentStatus               StatusIndicatorState
	RecentAutoReviewDenials     []AutoReviewDenialEntry
}

type ToolRequestQuestion struct {
	Header   string
	Question string
}

func ToolRequestToInterrupt(request ToolRequest) QueuedInterrupt {
	switch request.Kind {
	case ToolRequestExecApproval:
		return QueuedInterrupt{Kind: QueuedInterruptExecApproval, ID: firstNonEmptyRequestID(request.ID, request.CallID), ApprovalID: strings.TrimSpace(request.ApprovalID), Payload: request}
	case ToolRequestPatchApproval:
		return QueuedInterrupt{Kind: QueuedInterruptApplyPatchApproval, ID: firstNonEmptyRequestID(request.ID, request.CallID), Payload: request}
	case ToolRequestMcpElicitation:
		return QueuedInterrupt{Kind: QueuedInterruptElicitation, ID: strings.TrimSpace(request.ID), ServerName: strings.TrimSpace(request.ServerName), RequestID: strings.TrimSpace(request.RequestID), Payload: request}
	case ToolRequestPermissions:
		return QueuedInterrupt{Kind: QueuedInterruptRequestPermissions, ID: firstNonEmptyRequestID(request.ID, request.CallID), Payload: request}
	case ToolRequestUserInput:
		return QueuedInterrupt{Kind: QueuedInterruptRequestUserInput, ID: firstNonEmptyRequestID(request.ID, request.CallID), Payload: request}
	default:
		return QueuedInterrupt{ID: request.ID, Payload: request}
	}
}

func (s *ToolRequestRuntimeState) OnGuardianAssessment(event GuardianAssessmentEvent) GuardianAssessmentResult {
	if s == nil {
		return GuardianAssessmentResult{}
	}
	id := strings.TrimSpace(event.ID)
	detail := GuardianActionSummary(event.Action)
	if event.Status == GuardianAssessmentInProgress && detail != "" {
		s.PendingGuardianReviewStatus.StartOrUpdate(id, detail)
		status, _ := s.PendingGuardianReviewStatus.StatusIndicatorState()
		s.CurrentStatus = status
		return GuardianAssessmentResult{
			InProgress:            true,
			EnsureStatusIndicator: true,
			InterruptHintVisible:  true,
			Status:                &status,
			Redraw:                true,
		}
	}

	finished := s.PendingGuardianReviewStatus.Finish(id)
	if status, ok := s.PendingGuardianReviewStatus.StatusIndicatorState(); ok {
		s.CurrentStatus = status
	} else if finished || s.CurrentStatus.IsGuardianReview() {
		s.CurrentStatus = WorkingStatusIndicatorState()
	}

	result := GuardianAssessmentResult{
		ClearGuardianStatus: finished && s.PendingGuardianReviewStatus.IsEmpty(),
		HistoryMessage:      GuardianAssessmentHistoryMessage(event),
		Redraw:              event.Status == GuardianAssessmentApproved || event.Status == GuardianAssessmentDenied || event.Status == GuardianAssessmentTimedOut || finished,
	}
	if event.Status == GuardianAssessmentDenied {
		denial := AutoReviewDenialEntry{
			ID:        id,
			Summary:   detail,
			Rationale: "Auto-review denied this action.",
		}
		s.RecentAutoReviewDenials = append(s.RecentAutoReviewDenials, denial)
		result.RecentAutoReviewDenial = &denial
	}
	if !s.PendingGuardianReviewStatus.IsEmpty() {
		status, _ := s.PendingGuardianReviewStatus.StatusIndicatorState()
		result.Status = &status
	}
	return result
}

func GuardianActionSummary(action GuardianAssessmentAction) string {
	switch action.Kind {
	case GuardianActionCommand:
		return strings.TrimSpace(action.Command)
	case GuardianActionExecve:
		if len(action.Argv) > 0 {
			return strings.TrimSpace(strings.Join(action.Argv, " "))
		}
		return strings.TrimSpace(action.Program)
	case GuardianActionApplyPatch:
		files := cleanedGuardianFiles(action.Files)
		if len(files) == 1 {
			return "apply_patch touching " + files[0]
		}
		return "apply_patch touching " + formatInt64(int64(len(files))) + " files"
	case GuardianActionNetworkAccess:
		return "network access to " + strings.TrimSpace(action.Target)
	case GuardianActionMcpToolCall:
		label := strings.TrimSpace(action.ConnectorName)
		if label == "" {
			label = strings.TrimSpace(action.Server)
		}
		return "MCP " + strings.TrimSpace(action.ToolName) + " on " + label
	case GuardianActionRequestPermissions:
		return permissionRequestSummary("permission request", action.Reason)
	default:
		return ""
	}
}

func GuardianAssessmentHistoryMessage(event GuardianAssessmentEvent) string {
	action := event.Action
	command := GuardianCommand(action)
	switch event.Status {
	case GuardianAssessmentApproved:
		if len(command) > 0 {
			return firstRawHistoryLine(historycell.NewApprovalDecisionCell(historycell.NewCommandApprovalSubject(command), historycell.ReviewApproved, historycell.ApprovalActorGuardian))
		}
		return firstRawHistoryLine(historycell.NewGuardianApprovedActionRequest(GuardianActionSummary(action)))
	case GuardianAssessmentTimedOut:
		if len(command) > 0 {
			return firstRawHistoryLine(historycell.NewApprovalDecisionCell(historycell.NewCommandApprovalSubject(command), historycell.ReviewTimedOut, historycell.ApprovalActorGuardian))
		}
		if action.Kind == GuardianActionApplyPatch {
			return firstRawHistoryLine(historycell.NewGuardianTimedOutPatchRequest(action.Files))
		}
		return firstRawHistoryLine(historycell.NewGuardianTimedOutActionRequest(guardianTimedOutSummary(action)))
	case GuardianAssessmentDenied:
		if len(command) > 0 {
			return firstRawHistoryLine(historycell.NewApprovalDecisionCell(historycell.NewCommandApprovalSubject(command), historycell.ReviewDenied, historycell.ApprovalActorGuardian))
		}
		if action.Kind == GuardianActionApplyPatch {
			return firstRawHistoryLine(historycell.NewGuardianDeniedPatchRequest(action.Files))
		}
		return firstRawHistoryLine(historycell.NewGuardianDeniedActionRequest(guardianDeniedSummary(action)))
	default:
		return ""
	}
}

func GuardianCommand(action GuardianAssessmentAction) []string {
	switch action.Kind {
	case GuardianActionCommand:
		return SplitCommandString(action.Command)
	case GuardianActionExecve:
		if len(action.Argv) > 0 {
			return append([]string(nil), action.Argv...)
		}
		if strings.TrimSpace(action.Program) != "" {
			return []string{strings.TrimSpace(action.Program)}
		}
	}
	return nil
}

func RequestUserInputNotificationTitle(questions []ToolRequestQuestion) string {
	if len(questions) == 1 {
		if summary, ok := UserInputRequestSummary(questions[0].Header, questions[0].Question); ok {
			return summary
		}
		return "Question requested"
	}
	return formatInt64(int64(len(questions))) + " questions requested"
}

func permissionRequestSummary(subject string, reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return strings.TrimSpace(subject)
	}
	return strings.TrimSpace(subject) + ": " + reason
}

func guardianDeniedSummary(action GuardianAssessmentAction) string {
	switch action.Kind {
	case GuardianActionMcpToolCall:
		return "codex to call MCP tool " + strings.TrimSpace(action.Server) + "." + strings.TrimSpace(action.ToolName)
	case GuardianActionNetworkAccess:
		return "codex to access " + strings.TrimSpace(action.Target)
	case GuardianActionRequestPermissions:
		return permissionRequestSummary("codex to request permissions", action.Reason)
	default:
		return GuardianActionSummary(action)
	}
}

func guardianTimedOutSummary(action GuardianAssessmentAction) string {
	switch action.Kind {
	case GuardianActionMcpToolCall:
		return "codex could call MCP tool " + strings.TrimSpace(action.Server) + "." + strings.TrimSpace(action.ToolName)
	case GuardianActionNetworkAccess:
		return "codex could access " + strings.TrimSpace(action.Target)
	case GuardianActionRequestPermissions:
		return permissionRequestSummary("codex could request permissions", action.Reason)
	default:
		return GuardianActionSummary(action)
	}
}

func cleanedGuardianFiles(files []string) []string {
	out := make([]string, 0, len(files))
	for _, file := range files {
		if trimmed := strings.TrimSpace(file); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func firstRawHistoryLine(cell interface{ RawLines() []string }) string {
	lines := cell.RawLines()
	if len(lines) == 0 {
		return ""
	}
	return strings.TrimSpace(lines[0])
}
