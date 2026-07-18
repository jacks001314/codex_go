package chatwidget

import "strings"

type SessionConfiguredDisplay string

const (
	SessionConfiguredDisplayNormal           SessionConfiguredDisplay = "normal"
	SessionConfiguredDisplayQuiet            SessionConfiguredDisplay = "quiet"
	SessionConfiguredDisplaySideConversation SessionConfiguredDisplay = "side_conversation"
)

type MessageHistoryMetadata struct {
	LogID      string
	EntryCount int
}

type ThreadSessionSnapshot struct {
	ThreadID                string
	ThreadName              string
	MessageHistory          MessageHistoryMetadata
	NetworkProxy            string
	ForkedFromID            string
	ForkParentTitle         string
	RolloutPath             string
	CWD                     string
	RuntimeWorkspaceRoots   []string
	Model                   string
	ReasoningEffort         string
	CollaborationMode       string
	ServiceTier             string
	ApprovalPolicy          string
	PermissionProfile       string
	ActivePermissionProfile string
	ApprovalsReviewer       string
	Personality             string
	InstructionSourcePaths  []string
}

type SessionConfiguredResult struct {
	PreviousThreadID           string
	ThreadChanged              bool
	ResetCopyHistory           bool
	ResetTurnLifecycle         bool
	ClearSafetyBuffering       bool
	ClearGoalStatus            bool
	QueueSubmissions           bool
	ShowSessionHeader          bool
	ClearActiveSessionHeader   bool
	RefreshPlanModeNudge       bool
	RefreshModelDisplay        bool
	RefreshStatusSurfaces      bool
	RefreshPluginMentions      bool
	RefreshSkillsForCurrentCWD bool
	PrefetchConnectors         bool
	SubmitInitialMessage       bool
	EmitForkEvent              bool
	ForkEventLine              string
	RequestRedraw              bool
}

type ThreadNameUpdateResult struct {
	Applied               bool
	ThreadName            string
	ConfirmationMessage   string
	RefreshStatusSurfaces bool
	RequestRedraw         bool
	MaybeSendQueuedInput  bool
}

type SessionFlowState struct {
	ThreadID                            string
	ThreadName                          string
	SessionConfigured                   bool
	ShutdownComplete                    bool
	HistoryLogID                        string
	HistoryEntryCount                   int
	NetworkProxy                        string
	ForkedFromID                        string
	RolloutPath                         string
	CWD                                 string
	RuntimeWorkspaceRoots               []string
	Model                               string
	ReasoningEffort                     string
	CollaborationMode                   string
	ServiceTier                         string
	ApprovalPolicy                      string
	PermissionProfile                   string
	ActivePermissionProfile             string
	ApprovalsReviewer                   string
	Personality                         string
	InstructionSourcePaths              []string
	QueueSubmissions                    bool
	ActiveSessionHeader                 bool
	InitialMessagePending               bool
	SuppressInitialMessageSubmit        bool
	ElevatedWindowsSandboxSetupRequired bool
	ShowWelcomeBanner                   bool
	ConnectorsEnabled                   bool
	SuppressConfiguredRedraw            bool
	RecentAutoReviewDenialsResetCount   int
}

func (s *SessionFlowState) Configure(threadID string) {
	if s == nil {
		return
	}
	s.ThreadID = threadID
	s.SessionConfigured = true
	s.ShutdownComplete = false
}

func (s *SessionFlowState) ConfigureSession(session ThreadSessionSnapshot, display SessionConfiguredDisplay) SessionConfiguredResult {
	if s == nil {
		return SessionConfiguredResult{}
	}
	previousThreadID := s.ThreadID
	threadChanged := previousThreadID != "" && previousThreadID != session.ThreadID
	if previousThreadID == "" && session.ThreadID != "" {
		threadChanged = true
	}

	s.ThreadID = session.ThreadID
	s.ThreadName = session.ThreadName
	s.SessionConfigured = strings.TrimSpace(session.ThreadID) != ""
	s.ShutdownComplete = false
	s.HistoryLogID = session.MessageHistory.LogID
	s.HistoryEntryCount = session.MessageHistory.EntryCount
	s.NetworkProxy = session.NetworkProxy
	s.ForkedFromID = session.ForkedFromID
	s.RolloutPath = session.RolloutPath
	s.CWD = session.CWD
	s.RuntimeWorkspaceRoots = append([]string(nil), session.RuntimeWorkspaceRoots...)
	s.Model = session.Model
	s.ReasoningEffort = session.ReasoningEffort
	s.CollaborationMode = session.CollaborationMode
	s.ServiceTier = session.ServiceTier
	s.ApprovalPolicy = session.ApprovalPolicy
	s.PermissionProfile = session.PermissionProfile
	s.ActivePermissionProfile = session.ActivePermissionProfile
	s.ApprovalsReviewer = session.ApprovalsReviewer
	s.Personality = session.Personality
	s.InstructionSourcePaths = append([]string(nil), session.InstructionSourcePaths...)
	s.QueueSubmissions = false
	if threadChanged {
		s.RecentAutoReviewDenialsResetCount++
	}

	showHeader := display == "" || display == SessionConfiguredDisplayNormal
	clearHeader := !showHeader && s.ActiveSessionHeader
	s.ActiveSessionHeader = showHeader
	submitInitial := s.SubmitInitialUserMessageIfPending()
	emitFork := showHeader && strings.TrimSpace(session.ForkedFromID) != ""
	result := SessionConfiguredResult{
		PreviousThreadID:           previousThreadID,
		ThreadChanged:              threadChanged,
		ResetCopyHistory:           true,
		ResetTurnLifecycle:         true,
		ClearSafetyBuffering:       true,
		ClearGoalStatus:            true,
		QueueSubmissions:           false,
		ShowSessionHeader:          showHeader,
		ClearActiveSessionHeader:   clearHeader,
		RefreshPlanModeNudge:       true,
		RefreshModelDisplay:        true,
		RefreshStatusSurfaces:      true,
		RefreshPluginMentions:      true,
		RefreshSkillsForCurrentCWD: true,
		PrefetchConnectors:         s.ConnectorsEnabled,
		SubmitInitialMessage:       submitInitial,
		EmitForkEvent:              emitFork,
		RequestRedraw:              !s.SuppressConfiguredRedraw,
	}
	if emitFork {
		result.ForkEventLine = ForkedThreadEventLine(session.ForkedFromID, session.ForkParentTitle)
	}
	return result
}

func (s *SessionFlowState) MarkShutdownComplete() {
	if s == nil {
		return
	}
	s.ShutdownComplete = true
}

func (s SessionFlowState) CanSubmitInitialMessage() bool {
	return s.SessionConfigured &&
		!s.ShutdownComplete &&
		!s.SuppressInitialMessageSubmit &&
		!s.ElevatedWindowsSandboxSetupRequired
}

func (s *SessionFlowState) SetInitialUserMessageSubmitSuppressed(suppressed bool) {
	if s == nil {
		return
	}
	s.SuppressInitialMessageSubmit = suppressed
}

func (s *SessionFlowState) SubmitInitialUserMessageIfPending() bool {
	if s == nil || !s.InitialMessagePending || !s.CanSubmitInitialMessage() {
		return false
	}
	s.InitialMessagePending = false
	return true
}

func (s *SessionFlowState) OnThreadNameUpdated(threadID string, threadName *string) ThreadNameUpdateResult {
	if s == nil || s.ThreadID != threadID {
		return ThreadNameUpdateResult{}
	}
	name := ""
	if threadName != nil {
		name = strings.TrimSpace(*threadName)
	}
	s.ThreadName = name
	result := ThreadNameUpdateResult{
		Applied:               true,
		ThreadName:            name,
		RefreshStatusSurfaces: true,
		RequestRedraw:         true,
		MaybeSendQueuedInput:  true,
	}
	if name != "" {
		result.ConfirmationMessage = RenameConfirmationText(name)
	}
	return result
}

func ForkedThreadEventLine(forkedFromID string, forkParentTitle string) string {
	forkedFromID = strings.TrimSpace(forkedFromID)
	forkParentTitle = strings.TrimSpace(forkParentTitle)
	if forkParentTitle != "" {
		return "• Thread forked from " + forkParentTitle + " (" + forkedFromID + ")"
	}
	return "• Thread forked from " + forkedFromID
}

func RenameConfirmationText(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	return "Thread renamed to " + name + "."
}
