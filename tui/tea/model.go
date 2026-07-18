package tea

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	sysclipboard "github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	appsapi "codex_go/apps"
	"codex_go/appserver"
	"codex_go/plugin"
	"codex_go/protocol"
	"codex_go/review"
	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
	chatwidget "codex_go/tui/chatwidget"
	execcell "codex_go/tui/exec_cell"
	historycell "codex_go/tui/history_cell"
	"codex_go/tui/markdown"
)

const (
	defaultWidth          = 80
	defaultHeight         = 24
	defaultComposerHeight = 3
	minTranscriptHeight   = 3
	maxBottomLines        = 6
	footerHelpText        = "Enter send | Ctrl+J newline | Ctrl+G editor | Ctrl+C quit | /help commands"
	mcpStartupFinishLag   = 4 * time.Second
)

// SubmitFunc lets the runtime layer attach prompt execution without coupling
// the terminal model to app or turn packages.
type SubmitFunc func(prompt string) bubbletea.Cmd

type SubmitRequest struct {
	Prompt          string
	Attachments     []bottompane.ComposerAttachment
	MentionBindings []string
	MentionCatalog  chatwidget.SubmissionMentionCatalog
}

type SubmitRequestFunc func(request SubmitRequest) bubbletea.Cmd

type InterruptFunc func() bubbletea.Cmd

type ExternalEditorFunc func(seed string) bubbletea.Cmd

type KeymapEditFunc func(edit codextui.KeymapEdit) (*codextui.KeymapConfig, string, error)

type ExternalEditorFinishedMsg struct {
	Text string
	Err  error
}

type queuedSubmission struct {
	Request      SubmitRequest
	ParseCommand bool
}

type SessionActionFunc func(selection codextui.SessionSelection) (*codextui.SessionSummary, error)

type SessionResumeFunc func(selection codextui.SessionSelection) (SessionResumeResponse, error)

type SessionResumeResponse struct {
	Summary  *codextui.SessionSummary
	Messages []codextui.Message
	Status   string
}

type AgentThreadReaderFunc func(currentThreadID string) ([]codextui.AgentThreadEntry, error)

type AgentThreadSwitchFunc func(threadID string) (AgentThreadSwitchResponse, error)

type AgentThreadSwitchResponse struct {
	Entry    codextui.AgentThreadEntry
	Messages []codextui.Message
	Status   string
}

type TokenActivityReaderFunc func(view chatwidget.TokenActivityView) (chatwidget.TokenActivityResponse, error)

type RateLimitResetCreditsReaderFunc func() (int64, error)

type RateLimitResetCreditConsumerFunc func(idempotencyKey string) (chatwidget.RateLimitResetConsumeOutcome, error)

type TerminalTitleWriterFunc func(sequence string) bubbletea.Cmd

type NotificationPostFunc func(message string, method codextui.NotificationMethod) bubbletea.Cmd

type GitDiffReaderFunc func(cwd string) (string, bool, error)

type StopBackgroundTerminalsFunc func() bubbletea.Cmd

type DebugConfigReaderFunc func() ([]string, error)

type GoalReaderFunc func(threadID string) (*appserver.Goal, error)

type GoalSetterFunc func(threadID string, objective *string, tokenBudget *int64, status *appserver.GoalStatus) (appserver.Goal, error)

type GoalClearerFunc func(threadID string) (bool, error)

type SettingsEdit struct {
	KeyPath string
	Value   any
}

type SettingsWriteResult struct {
	FeatureSettings         map[string]bool
	Personality             chatwidget.Personality
	Notifications           *chatwidget.NotificationsSetting
	NotificationMethod      codextui.NotificationMethod
	NotificationCondition   codextui.NotificationCondition
	PermissionRequirements  *chatwidget.PermissionRequirements
	HideRateLimitModelNudge *bool
	TUITheme                string
	TUIPet                  string
	SessionPickerView       string
	FilePath                string
}

type SettingsWriteFunc func(edits []SettingsEdit) (SettingsWriteResult, error)

type WindowsSandboxSetupFunc func(mode chatwidget.WindowsSandboxMode, cwd string) (WindowsSandboxSetupOutcome, error)

type HooksListReaderFunc func(cwd string) ([]chatwidget.HookRun, error)

type PluginListReaderFunc func() (plugin.PluginListResponse, error)

type SkillsListReaderFunc func(cwd string) (appserver.SkillsListResponse, error)

type AppListReaderFunc func(threadID string, forceRefetch bool) (appsapi.AppListResponse, error)

type ReviewStartFunc func(params review.StartParams) (review.StartResponse, error)

type ReviewBranchesReaderFunc func(cwd string) (currentBranch string, branches []string, err error)

type ReviewCommitsReaderFunc func(cwd string, limit int) ([]chatwidget.ReviewCommitEntry, error)

type StatusMsg struct {
	Status string
}

type TurnCompletedMsg struct {
	ThreadID         string
	AssistantMessage string
	Err              error
}

type TurnInterruptedMsg struct {
	Err error
}

type MCPStartupUpdateMsg struct {
	Name   string
	Status chatwidget.McpStartupStatus
}

type MCPStartupInventoryMsg struct {
	Servers []historycell.McpServerStatus
}

type MCPStartupFinishAfterLagMsg struct{}

type mcpStartupFinishAfterLagMsg struct {
	Generation uint64
}

type ThreadEventMsg struct {
	Event protocol.ThreadEvent
}

type ThreadScopedEventMsg struct {
	ThreadID string
	Event    protocol.ThreadEvent
}

type HookOutputEntry struct {
	Kind string
	Text string
}

type HookRunMsg struct {
	ID            string
	ThreadID      string
	TurnID        string
	EventName     string
	Status        string
	StatusMessage string
	Entries       []HookOutputEntry
	Running       bool
}

type HistoryCellMsg struct {
	Cell historycell.HistoryCell
}

type RateLimitSnapshotMsg struct {
	Snapshot chatwidget.RateLimitSnapshot
}

type TokenActivityResultMsg struct {
	RequestID uint64
	View      chatwidget.TokenActivityView
	Response  chatwidget.TokenActivityResponse
	Err       error
}

type RateLimitResetCreditsResultMsg struct {
	RequestID      uint64
	AvailableCount int64
	Err            error
}

type RateLimitResetConsumeResultMsg struct {
	RequestID      uint64
	IdempotencyKey string
	Outcome        chatwidget.RateLimitResetConsumeOutcome
	Err            error
}

type DiffResultMsg struct {
	RequestID uint64
	Text      string
	IsGitRepo bool
	Err       error
}

type DebugConfigResultMsg struct {
	Lines []string
	Err   error
}

type GoalResultMsg struct {
	RequestID uint64
	Action    string
	ThreadID  string
	Goal      *appserver.Goal
	Cleared   bool
	Err       error
}

type GoalUpdatedMsg struct {
	Goal appserver.Goal
}

type GoalClearedMsg struct {
	ThreadID string
}

type SettingsWriteResultMsg struct {
	RequestID uint64
	Kind      string
	Result    SettingsWriteResult
	Err       error
}

type WindowsSandboxSetupResultMsg struct {
	Mode    chatwidget.WindowsSandboxMode
	Outcome WindowsSandboxSetupOutcome
	Err     error
}

type WindowsSandboxSetupCompletedMsg struct {
	Completion WindowsSandboxSetupCompletion
}

type HooksListResultMsg struct {
	Runs []chatwidget.HookRun
	Err  error
}

type PluginListResultMsg struct {
	Response plugin.PluginListResponse
	Err      error
}

type AppListResultMsg struct {
	ThreadID     string
	ForceRefetch bool
	Response     appsapi.AppListResponse
	Err          error
}

type AgentListResultMsg struct {
	CurrentThreadID string
	Entries         []codextui.AgentThreadEntry
	Err             error
}

type AgentSwitchResultMsg struct {
	ThreadID string
	Response AgentThreadSwitchResponse
	Err      error
}

type ReviewStartResultMsg struct {
	Target   chatwidget.ReviewTarget
	Response review.StartResponse
	Err      error
}

type ReviewBranchesResultMsg struct {
	CurrentBranch string
	Branches      []string
	Err           error
}

type ReviewCommitsResultMsg struct {
	Entries []chatwidget.ReviewCommitEntry
	Err     error
}

type SkillsListResultMsg struct {
	CWD      string
	Response appserver.SkillsListResponse
	Err      error
}

type SkillsInventoryResultMsg struct {
	CWD      string
	Response appserver.SkillsListResponse
	Err      error
}

type StreamStartedMsg struct {
	Messages <-chan bubbletea.Msg
}

type streamEnvelopeMsg struct {
	Message  bubbletea.Msg
	Messages <-chan bubbletea.Msg
	Done     bool
}

type Options struct {
	Width                         int
	Height                        int
	NoAltScreen                   bool
	Placeholder                   string
	ModelPickerOptions            []codextui.ModelPickerOption
	SessionPickerItems            []codextui.SessionSummary
	SessionPickerCWD              string
	SessionPickerView             string
	ShowSessionHeader             bool
	SessionHeaderVersion          string
	OnSubmit                      SubmitFunc
	OnSubmitRequest               SubmitRequestFunc
	OnInterrupt                   InterruptFunc
	OnInterruptMCPStartup         InterruptFunc
	OnExternalEditor              ExternalEditorFunc
	KeymapConfig                  *codextui.KeymapConfig
	OnKeymapEdit                  KeymapEditFunc
	OnModalResponse               ModalResponseFunc
	OnSessionAction               SessionActionFunc
	OnResumeSession               SessionResumeFunc
	OnReadAgents                  AgentThreadReaderFunc
	OnSwitchAgent                 AgentThreadSwitchFunc
	OnClipboardWrite              func(text string) error
	OnReadTokenActivity           TokenActivityReaderFunc
	OnReadRateLimitResetCredits   RateLimitResetCreditsReaderFunc
	OnConsumeRateLimitResetCredit RateLimitResetCreditConsumerFunc
	OnWriteTerminalTitle          TerminalTitleWriterFunc
	OnPostNotification            NotificationPostFunc
	OnReadGitDiff                 GitDiffReaderFunc
	OnStopBackgroundTerminals     StopBackgroundTerminalsFunc
	OnReadDebugConfig             DebugConfigReaderFunc
	OnReadGoal                    GoalReaderFunc
	OnSetGoal                     GoalSetterFunc
	OnClearGoal                   GoalClearerFunc
	OnWriteSettings               SettingsWriteFunc
	OnStartWindowsSandboxSetup    WindowsSandboxSetupFunc
	OnReadHooks                   HooksListReaderFunc
	OnReadPlugins                 PluginListReaderFunc
	OnReadSkills                  SkillsListReaderFunc
	OnReadApps                    AppListReaderFunc
	OnStartReview                 ReviewStartFunc
	OnStartSide                   SideStartFunc
	OnCloseSide                   SideCloseFunc
	OnReadReviewBranches          ReviewBranchesReaderFunc
	OnReadReviewCommits           ReviewCommitsReaderFunc

	HasChatGPTAccount              bool
	ChatGPTPlanType                string
	AvailableRateLimitResetCredits *int64
	StatusLineItems                []string
	TerminalTitleItems             []string
	Notifications                  *chatwidget.NotificationsSetting
	NotificationMethod             codextui.NotificationMethod
	NotificationCondition          codextui.NotificationCondition
	PermissionRequirements         *chatwidget.PermissionRequirements
	BackgroundProcesses            []historycell.UnifiedExecProcessDetails
	MCPServers                     []historycell.McpServerStatus
	MCPStartupExpectedServers      []string
	InitialMessages                <-chan bubbletea.Msg
	FeatureSettings                map[string]bool
	Personality                    chatwidget.Personality
	HideRateLimitModelNudge        *bool
	TUITheme                       string
	TUIPet                         string
}

type Model struct {
	State *codextui.State

	transcript viewport.Model
	composer   textarea.Model
	overlay    *chatwidget.TranscriptOverlay
	slashPopup slashCommandPopup

	width             int
	height            int
	noAltScreen       bool
	overlayAltScreen  bool
	overlayTranscript bool
	rateLimitWarnings chatwidget.RateLimitWarningState
	warningDisplay    chatwidget.WarningDisplayState

	terminalFocused          bool
	rawOutput                bool
	rateLimitSwitchPrompt    chatwidget.RateLimitSwitchPromptState
	hideRateLimitModelNudge  bool
	rateLimitSwitchModel     string
	rateLimitSwitchReasoning string

	statusStyle lipgloss.Style
	footerStyle lipgloss.Style
	bottomStyle lipgloss.Style

	lastTurnError                    string
	needsFinalMessageSeparator       bool
	activeAssistantDeltaItemID       string
	mcpStartup                       chatwidget.McpStartupRoundState
	mcpStartupHeader                 string
	mcpStartupActive                 bool
	mcpStartupGeneration             uint64
	mcpStartupFinishPending          bool
	initialMessages                  <-chan bubbletea.Msg
	notice                           string
	bottom                           []string
	attachments                      []bottompane.ComposerAttachment
	composerMentionBindings          []string
	modal                            *modalState
	skillPopup                       skillPopupState
	modelPickerOpts                  []codextui.ModelPickerOption
	sessionItems                     []codextui.SessionSummary
	sessionCWD                       string
	sessionPickerDensity             codextui.SessionListDensity
	skillsInventory                  *appserver.SkillsListResponse
	skillsInventoryCWD               string
	skillsInventoryErr               string
	skillsInventoryLoading           bool
	agentItems                       []codextui.AgentThreadEntry
	activeAgentLabel                 string
	backgroundProcesses              []historycell.UnifiedExecProcessDetails
	mcpServers                       []historycell.McpServerStatus
	featureSettings                  map[string]bool
	personality                      chatwidget.Personality
	tuiTheme                         string
	tuiPet                           string
	vimMode                          bool
	onSubmit                         SubmitFunc
	onSubmitRequest                  SubmitRequestFunc
	onInterrupt                      InterruptFunc
	onInterruptMCPStartup            InterruptFunc
	onExternalEditor                 ExternalEditorFunc
	keymapConfig                     *codextui.KeymapConfig
	onKeymapEdit                     KeymapEditFunc
	onModalResponse                  ModalResponseFunc
	onSessionAction                  SessionActionFunc
	onResumeSession                  SessionResumeFunc
	onReadAgents                     AgentThreadReaderFunc
	onSwitchAgent                    AgentThreadSwitchFunc
	clipboardWrite                   func(text string) error
	onReadTokenActivity              TokenActivityReaderFunc
	onReadRateLimitResetCredits      RateLimitResetCreditsReaderFunc
	onConsumeRateLimitResetCredit    RateLimitResetCreditConsumerFunc
	terminalTitleWriter              TerminalTitleWriterFunc
	notificationPost                 NotificationPostFunc
	notifications                    chatwidget.NotificationState
	notificationSettings             chatwidget.NotificationsSetting
	notificationMethod               codextui.NotificationMethod
	notificationCondition            codextui.NotificationCondition
	onReadGitDiff                    GitDiffReaderFunc
	onStopBackgroundTerminals        StopBackgroundTerminalsFunc
	onReadDebugConfig                DebugConfigReaderFunc
	onReadGoal                       GoalReaderFunc
	onSetGoal                        GoalSetterFunc
	onClearGoal                      GoalClearerFunc
	onWriteSettings                  SettingsWriteFunc
	windowsSandboxSetup              WindowsSandboxSetupFunc
	windowsSandboxSetupActive        bool
	windowsSandboxSetupStatus        chatwidget.WindowsSandboxSetupStatus
	onReadHooks                      HooksListReaderFunc
	hookLifecycle                    chatwidget.HookLifecycleState
	onReadPlugins                    PluginListReaderFunc
	onReadSkills                     SkillsListReaderFunc
	onReadApps                       AppListReaderFunc
	onStartReview                    ReviewStartFunc
	onStartSide                      SideStartFunc
	onCloseSide                      SideCloseFunc
	onReadReviewBranches             ReviewBranchesReaderFunc
	onReadReviewCommits              ReviewCommitsReaderFunc
	activeSide                       *activeSideConversation
	sideStartPending                 bool
	statusControls                   *chatwidget.StatusControlsState
	statusLineConfiguredByUser       bool
	lastTerminalTitleSequence        string
	rateLimitSnapshots               map[string]chatwidget.RateLimitSnapshot
	approvalsReviewer                chatwidget.ApprovalsReviewer
	permissionRequirements           chatwidget.PermissionRequirements
	permissionItems                  []chatwidget.PermissionMenuItem
	pendingPermissionItem            *chatwidget.PermissionMenuItem
	hideFullAccessWarning            bool
	experimentalItems                []chatwidget.ExperimentalFeatureOption
	currentGoal                      *appserver.Goal
	goalObservedAt                   time.Time
	hasChatGPTAccount                bool
	chatGPTPlanType                  string
	availableRateLimitResetCredits   *int64
	nextUsageRequestID               uint64
	pendingTokenActivityRequestID    uint64
	pendingRateLimitResetRequestID   uint64
	pendingRateLimitResetForPopup    bool
	pendingRateLimitResetPostConsume bool
	nextDiffRequestID                uint64
	pendingDiffRequestID             uint64
	nextGoalRequestID                uint64
	pendingGoalRequestID             uint64
	nextSettingsRequestID            uint64
	pendingSettingsRequestID         uint64
	submitted                        []string
	submitRequests                   []SubmitRequest
	queued                           []queuedSubmission
	editorActive                     bool
	toolCalls                        map[string]*toolCallDisplayState
	mcpToolCalls                     map[string]*mcpToolCallDisplayState
	startedThreadIDs                 map[string]bool
	completedThreadIDs               map[string]bool
	taskStartedAt                    time.Time

	composerPasteEnterUntil *time.Time
	now                     func() time.Time
}

type toolCallDisplayState struct {
	ID           string
	CallID       string
	ToolName     string
	Input        string
	MessageIndex int
	StartedAt    time.Time
	Completed    bool
	PlanUpdate   bool
}

type mcpToolCallDisplayState struct {
	ID           string
	Invocation   historycell.McpInvocation
	MessageIndex int
}

func NewModel(state *codextui.State, options Options) *Model {
	if state == nil {
		state = codextui.NewState(nil)
	}
	composer := textarea.New()
	composer.Prompt = "> "
	composer.Placeholder = firstNonEmpty(options.Placeholder, "Ask Codex")
	composer.ShowLineNumbers = false
	composer.CharLimit = 0
	composer.SetHeight(defaultComposerHeight)
	composer.SetWidth(defaultWidth)
	composer.Focus()
	transcript := viewport.New(defaultWidth, defaultHeight-defaultComposerHeight-2)
	transcript.MouseWheelEnabled = true
	transcript.MouseWheelDelta = 3
	clipboardWrite := options.OnClipboardWrite
	if clipboardWrite == nil {
		clipboardWrite = sysclipboard.WriteAll
	}

	model := &Model{
		State:                          state,
		transcript:                     transcript,
		composer:                       composer,
		noAltScreen:                    options.NoAltScreen,
		terminalFocused:                true,
		statusStyle:                    lipgloss.NewStyle().Bold(true),
		footerStyle:                    lipgloss.NewStyle(),
		bottomStyle:                    lipgloss.NewStyle(),
		modelPickerOpts:                append([]codextui.ModelPickerOption(nil), options.ModelPickerOptions...),
		sessionItems:                   append([]codextui.SessionSummary(nil), options.SessionPickerItems...),
		sessionCWD:                     strings.TrimSpace(options.SessionPickerCWD),
		sessionPickerDensity:           normalizeSessionPickerDensityTea(options.SessionPickerView),
		backgroundProcesses:            cloneUnifiedExecProcessDetails(options.BackgroundProcesses),
		mcpServers:                     cloneMcpServerStatuses(options.MCPServers),
		mcpStartup:                     chatwidget.NewMcpStartupRoundState(options.MCPStartupExpectedServers),
		initialMessages:                options.InitialMessages,
		featureSettings:                cloneBoolMapTea(options.FeatureSettings),
		personality:                    initialPersonality(state, options.Personality),
		tuiTheme:                       strings.TrimSpace(options.TUITheme),
		tuiPet:                         normalizePetIDTea(options.TUIPet),
		onSubmit:                       options.OnSubmit,
		onSubmitRequest:                options.OnSubmitRequest,
		onInterrupt:                    options.OnInterrupt,
		onInterruptMCPStartup:          options.OnInterruptMCPStartup,
		onExternalEditor:               options.OnExternalEditor,
		keymapConfig:                   options.KeymapConfig.Clone(),
		onKeymapEdit:                   options.OnKeymapEdit,
		onModalResponse:                options.OnModalResponse,
		onSessionAction:                options.OnSessionAction,
		onResumeSession:                options.OnResumeSession,
		onReadAgents:                   options.OnReadAgents,
		onSwitchAgent:                  options.OnSwitchAgent,
		clipboardWrite:                 clipboardWrite,
		onReadTokenActivity:            options.OnReadTokenActivity,
		onReadRateLimitResetCredits:    options.OnReadRateLimitResetCredits,
		onConsumeRateLimitResetCredit:  options.OnConsumeRateLimitResetCredit,
		terminalTitleWriter:            terminalTitleWriterOrDefault(options.OnWriteTerminalTitle),
		notificationPost:               options.OnPostNotification,
		notificationSettings:           notificationSettingsOrDefault(options.Notifications),
		notificationMethod:             notificationMethodOrDefault(options.NotificationMethod),
		notificationCondition:          notificationConditionOrDefault(options.NotificationCondition),
		onReadGitDiff:                  options.OnReadGitDiff,
		onStopBackgroundTerminals:      options.OnStopBackgroundTerminals,
		onReadDebugConfig:              options.OnReadDebugConfig,
		onReadGoal:                     options.OnReadGoal,
		onSetGoal:                      options.OnSetGoal,
		onClearGoal:                    options.OnClearGoal,
		onWriteSettings:                options.OnWriteSettings,
		windowsSandboxSetup:            options.OnStartWindowsSandboxSetup,
		onReadHooks:                    options.OnReadHooks,
		onReadPlugins:                  options.OnReadPlugins,
		onReadSkills:                   options.OnReadSkills,
		onReadApps:                     options.OnReadApps,
		onStartReview:                  options.OnStartReview,
		onStartSide:                    options.OnStartSide,
		onCloseSide:                    options.OnCloseSide,
		onReadReviewBranches:           options.OnReadReviewBranches,
		onReadReviewCommits:            options.OnReadReviewCommits,
		statusLineConfiguredByUser:     options.StatusLineItems != nil,
		rateLimitSnapshots:             map[string]chatwidget.RateLimitSnapshot{},
		rateLimitSwitchPrompt:          chatwidget.RateLimitSwitchPromptIdle,
		hideRateLimitModelNudge:        boolPtrValueTea(options.HideRateLimitModelNudge),
		approvalsReviewer:              chatwidget.ApprovalsReviewerUser,
		permissionRequirements:         clonePermissionRequirementsTea(options.PermissionRequirements),
		hasChatGPTAccount:              options.HasChatGPTAccount,
		chatGPTPlanType:                strings.TrimSpace(options.ChatGPTPlanType),
		availableRateLimitResetCredits: cloneInt64PtrTea(options.AvailableRateLimitResetCredits),
		toolCalls:                      map[string]*toolCallDisplayState{},
		mcpToolCalls:                   map[string]*mcpToolCallDisplayState{},
		startedThreadIDs:               map[string]bool{},
		completedThreadIDs:             map[string]bool{},
		now:                            time.Now,
	}
	if threadID := strings.TrimSpace(state.ThreadID); threadID != "" {
		model.markThreadCompleted(threadID)
	}
	if strings.TrimSpace(state.Personality) == "" && strings.TrimSpace(string(options.Personality)) != "" {
		state.Personality = string(model.personality)
	}
	model.syncTaskRunningTimer()
	model.statusControls = chatwidget.NewStatusControlsState(model.statusControlsRuntime())
	if options.StatusLineItems != nil {
		model.statusControls.StatusLineConfigured = true
		model.statusControls.StatusLineIDs = append([]string(nil), options.StatusLineItems...)
	}
	if options.TerminalTitleItems != nil {
		model.statusControls.TerminalTitleConfigured = true
		model.statusControls.TerminalTitleIDs = append([]string(nil), options.TerminalTitleItems...)
	}
	model.resize(firstPositive(options.Width, defaultWidth), firstPositive(options.Height, defaultHeight))
	if options.ShowSessionHeader {
		model.addStartupSessionHeader(options.SessionHeaderVersion)
	}
	model.refreshTranscript()
	return model
}

func NewProgram(ctx context.Context, state *codextui.State, options Options, input io.Reader, output io.Writer) *bubbletea.Program {
	model := NewModel(state, options)
	programOptions := []bubbletea.ProgramOption{}
	if ctx != nil {
		programOptions = append(programOptions, bubbletea.WithContext(ctx))
	}
	if input != nil {
		programOptions = append(programOptions, bubbletea.WithInput(input))
	}
	if output != nil {
		programOptions = append(programOptions, bubbletea.WithOutput(output))
	}
	programOptions = append(programOptions, bubbletea.WithReportFocus())
	if !options.NoAltScreen {
		programOptions = append(programOptions, bubbletea.WithAltScreen())
	}
	return bubbletea.NewProgram(model, programOptions...)
}

func Run(ctx context.Context, state *codextui.State, options Options, input io.Reader, output io.Writer) (*Model, error) {
	final, err := NewProgram(ctx, state, options, input, output).Run()
	if err != nil {
		return nil, err
	}
	model, ok := final.(*Model)
	if !ok {
		return nil, nil
	}
	return model, nil
}

func (m *Model) Init() bubbletea.Cmd {
	commands := []bubbletea.Cmd{m.composer.Focus()}
	if m.initialMessages != nil {
		commands = append(commands, waitForStream(m.initialMessages))
	}
	return bubbletea.Batch(commands...)
}

func (m *Model) Update(message bubbletea.Msg) (bubbletea.Model, bubbletea.Cmd) {
	if m == nil {
		return m, nil
	}
	switch msg := message.(type) {
	case bubbletea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
		return m, nil
	case bubbletea.FocusMsg:
		m.terminalFocused = true
		return m, nil
	case bubbletea.BlurMsg:
		m.terminalFocused = false
		return m, nil
	case StatusMsg:
		if warning, ok := warningMessageFromStatus(msg.Status); ok {
			m.setStatus("warning")
			m.applyWarningMessage(warning)
			return m, m.refreshStatusControlsCmd()
		}
		m.setStatus(msg.Status)
		m.refreshTranscript()
		return m, m.refreshStatusControlsCmd()
	case TurnCompletedMsg:
		cmd := m.applyTurnCompleted(msg)
		return m, bubbletea.Batch(cmd, m.refreshStatusControlsCmd(), m.submitNextQueued())
	case TurnInterruptedMsg:
		m.applyTurnInterrupted(msg)
		return m, bubbletea.Batch(m.refreshStatusControlsCmd(), m.submitNextQueued())
	case MCPStartupUpdateMsg:
		return m, m.applyMCPStartupUpdate(msg)
	case MCPStartupInventoryMsg:
		m.mcpServers = cloneMcpServerStatuses(msg.Servers)
		return m, nil
	case MCPStartupFinishAfterLagMsg:
		return m, m.finishMCPStartupAfterLag(0)
	case mcpStartupFinishAfterLagMsg:
		return m, m.finishMCPStartupAfterLag(msg.Generation)
	case ExternalEditorFinishedMsg:
		m.applyExternalEditorFinished(msg)
		return m, nil
	case ThreadEventMsg:
		cmd := m.applyThreadEvent(msg.Event)
		return m, bubbletea.Batch(cmd, m.refreshStatusControlsCmd())
	case ThreadScopedEventMsg:
		cmd := m.applyThreadScopedEvent(msg)
		return m, bubbletea.Batch(cmd, m.refreshStatusControlsCmd())
	case HookRunMsg:
		m.applyHookRun(msg)
		return m, nil
	case HistoryCellMsg:
		m.applyHistoryCell(msg.Cell)
		return m, nil
	case RateLimitSnapshotMsg:
		cmd := m.applyRateLimitSnapshot(msg.Snapshot)
		return m, bubbletea.Batch(m.refreshStatusControlsCmd(), cmd)
	case TokenActivityResultMsg:
		m.applyTokenActivityResult(msg)
		return m, nil
	case RateLimitResetCreditsResultMsg:
		m.applyRateLimitResetCreditsResult(msg)
		return m, nil
	case RateLimitResetConsumeResultMsg:
		return m, m.applyRateLimitResetConsumeResult(msg)
	case DiffResultMsg:
		return m, m.applyDiffResult(msg)
	case DebugConfigResultMsg:
		m.applyDebugConfigResult(msg)
		return m, nil
	case GoalResultMsg:
		m.applyGoalResult(msg)
		return m, m.refreshStatusControlsCmd()
	case ReviewStartResultMsg:
		m.applyReviewStartResult(msg)
		return m, m.refreshStatusControlsCmd()
	case SideStartResultMsg:
		return m, m.applySideStartResult(msg)
	case SideCloseResultMsg:
		return m, m.applySideCloseResult(msg)
	case SideParentStatusChangeMsg:
		m.applySideParentStatusChange(msg)
		return m, nil
	case ReviewBranchesResultMsg:
		m.applyReviewBranchesResult(msg)
		return m, nil
	case ReviewCommitsResultMsg:
		m.applyReviewCommitsResult(msg)
		return m, nil
	case GoalUpdatedMsg:
		m.applyGoalUpdated(msg.Goal, true)
		return m, m.refreshStatusControlsCmd()
	case GoalClearedMsg:
		m.applyGoalCleared(msg.ThreadID, true)
		return m, m.refreshStatusControlsCmd()
	case SettingsWriteResultMsg:
		m.applySettingsWriteResult(msg)
		return m, m.refreshStatusControlsCmd()
	case WindowsSandboxSetupResultMsg:
		m.applyWindowsSandboxSetupResult(msg)
		return m, nil
	case WindowsSandboxSetupCompletedMsg:
		m.applyWindowsSandboxSetupCompleted(msg.Completion)
		return m, nil
	case HooksListResultMsg:
		m.applyHooksListResult(msg)
		return m, nil
	case PluginListResultMsg:
		m.applyPluginListResult(msg)
		return m, nil
	case AppListResultMsg:
		m.applyAppListResult(msg)
		return m, nil
	case AgentListResultMsg:
		m.applyAgentListResult(msg)
		return m, nil
	case AgentSwitchResultMsg:
		m.applyAgentSwitchResult(msg)
		return m, m.refreshStatusControlsCmd()
	case SkillsListResultMsg:
		m.applySkillsListResult(msg)
		return m, nil
	case SkillsInventoryResultMsg:
		m.applySkillsInventoryResult(msg)
		return m, nil
	case StreamStartedMsg:
		return m, waitForStream(msg.Messages)
	case ModalRequestMsg:
		m.openModal(msg)
		return m, nil
	case ApprovalRequestMsg:
		return m, m.openApprovalModal(msg)
	case ElicitationRequestMsg:
		return m, m.openElicitationModal(msg)
	case RequestUserInputMsg:
		return m, m.openRequestUserInputModal(msg)
	case requestUserInputTimeoutMsg:
		return m, m.applyRequestUserInputTimeout(msg)
	case streamEnvelopeMsg:
		if msg.Done {
			return m, nil
		}
		cmd := m.applyStreamMessage(msg.Message)
		return m, bubbletea.Batch(cmd, waitForStream(msg.Messages))
	case bubbletea.KeyMsg:
		if msg.Type == bubbletea.KeyRunes && msg.Paste {
			// Handle bracketed paste before overlays, popups and keymaps. Windows
			// Terminal commonly delivers Ctrl+V/right-click paste through this
			// path rather than as KeyCtrlV.
			if pasted := string(msg.Runes); pasted != "" && m.modal == nil {
				m.composer.InsertString(pasted)
				m.extendComposerPasteWindow(m.currentTime())
				m.refreshSlashPopup()
				return m, m.refreshSkillPopup()
			}
		}
		if m.overlay != nil {
			return m, m.updateTranscriptOverlayKey(msg)
		}
		switch msg.Type {
		case bubbletea.KeyCtrlC:
			if m.modal != nil && m.modal.sessionPicker != nil {
				return m, m.respondModal(true)
			}
			if m.isTaskRunning() {
				return m, m.interruptRunningTask()
			}
			if m.inSideConversation() {
				return m, m.returnFromSideConversation()
			}
			return m, bubbletea.Quit
		case bubbletea.KeyCtrlD:
			return m, bubbletea.Quit
		case bubbletea.KeyCtrlV:
			// Bubble Tea does not provide a portable text-paste message on all
			// terminals. Read the native clipboard explicitly and insert it into
			// the focused composer.
			if m.modal == nil && m.overlay == nil {
				if path, err := pasteImageFromClipboard(); err == nil {
					m.attachments = append(m.attachments, bottompane.ComposerAttachment{Kind: bottompane.AttachmentImage, Path: path})
					m.notice = "Attached image " + path
					return m, nil
				}
				text, err := sysclipboard.ReadAll()
				if err != nil {
					m.notice = "Paste failed: " + err.Error()
					return m, nil
				}
				if text != "" {
					m.composer.InsertString(text)
					m.extendComposerPasteWindow(m.currentTime())
					m.refreshSlashPopup()
				}
				return m, nil
			}
		}
		if m.modal != nil {
			return m, m.updateModal(msg)
		}
		if m.windowsSandboxSetupActive {
			return m, nil
		}
		if cmd, handled := m.updateSkillPopupKey(msg); handled {
			return m, cmd
		}
		if cmd, handled := m.updateSlashPopupKey(msg); handled {
			return m, cmd
		}
		keySpec := keySpecFromKeyMsg(msg)
		if m.keyMatches("global", "open_transcript", keySpec) {
			return m, m.openTranscriptOverlay()
		}
		if m.keyMatches("global", "copy", keySpec) {
			m.copyLastAgentResponse()
			return m, nil
		}
		if m.keyMatches("global", "toggle_raw_output", keySpec) {
			return m, m.toggleRawOutputMode()
		}
		if m.applyTranscriptNavigationKey(msg) {
			return m, nil
		}
		now := m.currentTime()
		if msg.Type == bubbletea.KeyRunes {
			m.noteComposerRunes(msg.Runes, now)
		}
		if m.isTaskRunning() && m.keyMatches("chat", "interrupt_turn", keySpec) {
			m.clearComposerPasteWindow()
			return m, m.interruptRunningTask()
		}
		if m.keyMatches("global", "open_external_editor", keySpec) {
			m.clearComposerPasteWindow()
			return m, m.openExternalEditor()
		}
		if msg.Type == bubbletea.KeyEnter {
			if m.shouldPasteBurstEnterInsertNewline(now) {
				m.extendComposerPasteWindow(now)
				return m, m.insertComposerNewline()
			}
		}
		if m.keyMatches("composer", "submit", keySpec) {
			m.clearComposerPasteWindow()
			if m.isTaskRunning() {
				if cmd, handled := m.submitRunningSlashCommand(); handled {
					return m, cmd
				}
				return m, m.queueComposer(true)
			}
			return m, m.submitComposer()
		}
		if m.keyMatches("composer", "queue", keySpec) {
			m.clearComposerPasteWindow()
			if m.isTaskRunning() {
				if cmd, handled := m.submitRunningSlashCommand(); handled {
					return m, cmd
				}
				return m, m.queueComposer(true)
			}
			if m.shouldSubmitOnTab() {
				return m, m.submitComposer()
			}
		}
		if msg.Type != bubbletea.KeyRunes {
			m.clearComposerPasteWindow()
		}
		if m.keyMatches("editor", "insert_newline", keySpec) {
			return m, m.insertComposerNewline()
		}
	case bubbletea.MouseMsg:
		if m.overlay != nil {
			return m, m.updateTranscriptOverlayMouse(msg)
		}
	}

	var cmd bubbletea.Cmd
	m.transcript, cmd = m.transcript.Update(message)
	var composerCmd bubbletea.Cmd
	m.composer, composerCmd = m.composer.Update(message)
	m.refreshSlashPopup()
	skillPopupCmd := m.refreshSkillPopup()
	return m, bubbletea.Batch(cmd, composerCmd, skillPopupCmd)
}

func (m *Model) View() string {
	if m == nil {
		return ""
	}
	m.ensureSize()
	if m.overlay != nil {
		if m.overlayTranscript {
			m.syncTranscriptOverlay()
		}
		return m.overlay.View()
	}
	m.refreshTranscript()

	sections := []string{
		m.statusStyle.Render(fitTerminalLine(m.renderStatusHeader(), m.width)),
		m.transcript.View(),
	}
	if bottom := m.renderBottomPane(); bottom != "" {
		sections = append(sections, m.bottomStyle.Render(bottom))
	}
	if modal := m.renderModal(); modal != "" {
		sections = append(sections, m.bottomStyle.Render(modal))
	} else if m.editorActive {
		sections = append(sections, m.bottomStyle.Render("Save and close external editor to continue."))
	} else if m.windowsSandboxSetupActive {
		sections = append(sections, m.bottomStyle.Render(m.renderWindowsSandboxSetupStatus()))
	} else {
		if working := m.renderWorkingIndicator(); working != "" {
			sections = append(sections, m.bottomStyle.Render(working))
		}
		sections = append(sections, m.composer.View())
		if popup := m.renderSkillPopup(); popup != "" {
			sections = append(sections, popup)
		}
		if popup := m.renderSlashPopup(); popup != "" {
			sections = append(sections, popup)
		}
	}
	if strings.TrimSpace(m.notice) != "" {
		sections = append(sections, m.footerStyle.Render(m.notice))
	}
	if sideLabel := strings.TrimSpace(m.sideContextLabel()); sideLabel != "" && sideLabel != strings.TrimSpace(m.notice) {
		sections = append(sections, m.footerStyle.Render(fitTerminalLine(sideLabel, m.width)))
	}
	if agentLabel := strings.TrimSpace(m.activeAgentLabel); agentLabel != "" && agentLabel != strings.TrimSpace(m.notice) {
		sections = append(sections, m.footerStyle.Render(fitTerminalLine(agentLabel, m.width)))
	}
	sections = append(sections, m.footerStyle.Render(fitTerminalLine(footerHelpText, m.width)))
	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

func fitTerminalLine(line string, width int) string {
	line = strings.ReplaceAll(line, "\r", " ")
	line = strings.ReplaceAll(line, "\n", " ")
	if width <= 0 {
		return line
	}
	runes := []rune(line)
	if len(runes) <= width {
		return line
	}
	if width <= 3 {
		return string(runes[:width])
	}
	return string(runes[:width-3]) + "..."
}

func (m *Model) SubmittedPrompts() []string {
	if m == nil || len(m.submitted) == 0 {
		return nil
	}
	out := make([]string, len(m.submitted))
	copy(out, m.submitted)
	return out
}

func (m *Model) SubmittedRequests() []SubmitRequest {
	if m == nil || len(m.submitRequests) == 0 {
		return nil
	}
	out := make([]SubmitRequest, len(m.submitRequests))
	for i := range m.submitRequests {
		out[i] = cloneSubmitRequest(m.submitRequests[i])
	}
	return out
}

func (m *Model) QueuedRequests() []SubmitRequest {
	if m == nil || len(m.queued) == 0 {
		return nil
	}
	out := make([]SubmitRequest, len(m.queued))
	for i := range m.queued {
		out[i] = cloneSubmitRequest(m.queued[i].Request)
	}
	return out
}

func (m *Model) ComposerValue() string {
	if m == nil {
		return ""
	}
	return m.composer.Value()
}

func (m *Model) insertComposerNewline() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	m.composer.InsertString("\n")
	return nil
}

func (m *Model) currentTime() time.Time {
	if m != nil && m.now != nil {
		return m.now()
	}
	return time.Now()
}

func (m *Model) noteComposerRunes(runes []rune, now time.Time) {
	if m == nil || len(runes) <= 1 {
		return
	}
	m.extendComposerPasteWindow(now)
}

func (m *Model) extendComposerPasteWindow(now time.Time) {
	if m == nil {
		return
	}
	until := now.Add(bottompane.PasteEnterSuppressWindow)
	m.composerPasteEnterUntil = &until
}

func (m *Model) clearComposerPasteWindow() {
	if m == nil {
		return
	}
	m.composerPasteEnterUntil = nil
}

func (m *Model) shouldPasteBurstEnterInsertNewline(now time.Time) bool {
	if m == nil || m.composerPasteEnterUntil == nil || m.composerStartsSlashContext() {
		return false
	}
	return !now.After(*m.composerPasteEnterUntil)
}

func (m *Model) composerStartsSlashContext() bool {
	if m == nil {
		return false
	}
	input := m.composer.Value()
	if newline := strings.IndexByte(input, '\n'); newline >= 0 {
		input = input[:newline]
	}
	return strings.HasPrefix(input, "/")
}

func (m *Model) Size() (int, int) {
	if m == nil {
		return 0, 0
	}
	return m.width, m.height
}

func (m *Model) TerminalFocused() bool {
	if m == nil {
		return false
	}
	return m.terminalFocused
}

func (m *Model) submitComposer() bubbletea.Cmd {
	input := strings.TrimSpace(m.composer.Value())
	m.composer.Reset()
	m.slashPopup = slashCommandPopup{}
	m.skillPopup = skillPopupState{}
	if input == "" && len(m.attachments) == 0 {
		m.composerMentionBindings = nil
		return nil
	}
	if invocation, ok := codextui.ParseCommand(input); ok {
		m.composerMentionBindings = nil
		return m.applyCommand(invocation)
	}
	request := SubmitRequest{
		Prompt:          input,
		Attachments:     cloneComposerAttachments(m.attachments),
		MentionBindings: m.activeComposerMentionBindings(input),
		MentionCatalog:  m.submissionMentionCatalog(),
	}
	m.attachments = nil
	m.composerMentionBindings = nil
	return m.submitRequest(request, false)
}

func (m *Model) submitRunningSlashCommand() (bubbletea.Cmd, bool) {
	if m == nil {
		return nil, false
	}
	input := strings.TrimSpace(m.composer.Value())
	if input == "" || len(m.attachments) > 0 {
		return nil, false
	}
	invocation, ok := codextui.ParseCommand(input)
	if !ok {
		return nil, false
	}
	m.composer.Reset()
	m.slashPopup = slashCommandPopup{}
	m.skillPopup = skillPopupState{}
	m.composerMentionBindings = nil
	if !commandAvailableDuringTask(invocation.Command) {
		message := "'" + strings.TrimSpace(invocation.Name) + "' is disabled while a task is in progress."
		m.State.AddHistoryLines([]string{message}, []string{message})
		m.notice = message
		m.refreshTranscript()
		return nil, true
	}
	return m.applyCommand(invocation), true
}

func (m *Model) submitRequest(request SubmitRequest, parseCommand bool) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	request = cloneSubmitRequest(request)
	request.Prompt = strings.TrimSpace(request.Prompt)
	if request.Prompt == "" && len(request.Attachments) == 0 {
		return nil
	}
	if parseCommand && len(request.Attachments) == 0 {
		if invocation, ok := codextui.ParseCommand(request.Prompt); ok {
			return m.applyCommand(invocation)
		}
	}
	displayPrompt := m.promptWithRequestAttachments(request)
	m.notice = ""
	m.lastTurnError = ""
	m.needsFinalMessageSeparator = false
	m.activeAssistantDeltaItemID = ""
	m.State.AddMessage(codextui.RoleUser, displayPrompt)
	if m.onSubmit == nil && m.onSubmitRequest == nil {
		m.setStatus("pending")
	} else {
		m.setStatus("running")
	}
	m.submitted = append(m.submitted, displayPrompt)
	m.submitRequests = append(m.submitRequests, cloneSubmitRequest(request))
	m.refreshTranscript()
	if m.onSubmitRequest != nil {
		return m.onSubmitRequest(request)
	}
	if m.onSubmit != nil {
		return m.onSubmit(displayPrompt)
	}
	return nil
}

func (m *Model) queueComposer(parseCommand bool) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	input := strings.TrimSpace(m.composer.Value())
	m.composer.Reset()
	m.slashPopup = slashCommandPopup{}
	m.skillPopup = skillPopupState{}
	if input == "" && len(m.attachments) == 0 {
		return nil
	}
	request := SubmitRequest{
		Prompt:          input,
		Attachments:     cloneComposerAttachments(m.attachments),
		MentionBindings: m.activeComposerMentionBindings(input),
		MentionCatalog:  m.submissionMentionCatalog(),
	}
	m.attachments = nil
	m.composerMentionBindings = nil
	m.queued = append(m.queued, queuedSubmission{
		Request:      cloneSubmitRequest(request),
		ParseCommand: parseCommand,
	})
	m.notice = "Queued input."
	m.addBottomLine("Queued: " + queuedSubmissionSummary(request))
	m.refreshTranscript()
	return nil
}

func (m *Model) submitNextQueued() bubbletea.Cmd {
	if m == nil || len(m.queued) == 0 || !m.isIdle() {
		return nil
	}
	next := m.queued[0]
	copy(m.queued, m.queued[1:])
	m.queued = m.queued[:len(m.queued)-1]
	return m.submitRequest(next.Request, next.ParseCommand)
}

func (m *Model) applyMCPStartupUpdate(message MCPStartupUpdateMsg) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	status := message.Status
	if status.Kind == chatwidget.McpStartupFailed && status.Error == "" {
		status.Error = "MCP client for `" + message.Name + "` failed to start"
	}
	result := m.mcpStartup.Update(message.Name, status, false)
	for _, warning := range result.Warnings {
		m.applyHistoryCell(historycell.NewWarningEvent(warning))
		m.notice = warning
	}
	if result.Finished {
		m.mcpStartupActive = false
		m.mcpStartupHeader = ""
		m.mcpStartupFinishPending = false
	} else if result.Active {
		m.mcpStartupActive = true
		if strings.TrimSpace(result.Header) != "" {
			m.mcpStartupHeader = result.Header
		}
	}
	m.syncTaskRunningTimer()
	m.refreshTranscript()
	if result.Finished {
		return m.submitNextQueued()
	}
	if result.Settled && !m.mcpStartupFinishPending {
		m.mcpStartupGeneration++
		generation := m.mcpStartupGeneration
		m.mcpStartupFinishPending = true
		return bubbletea.Tick(mcpStartupFinishLag, func(time.Time) bubbletea.Msg {
			return mcpStartupFinishAfterLagMsg{Generation: generation}
		})
	}
	return nil
}

func (m *Model) finishMCPStartupAfterLag(generation uint64) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if generation != 0 && (!m.mcpStartupFinishPending || generation != m.mcpStartupGeneration) {
		return nil
	}
	result := m.mcpStartup.FinishAfterLag()
	for _, warning := range result.Warnings {
		m.applyHistoryCell(historycell.NewWarningEvent(warning))
		m.notice = warning
	}
	if result.Finished {
		m.mcpStartupActive = false
		m.mcpStartupHeader = ""
		m.mcpStartupFinishPending = false
	}
	m.syncTaskRunningTimer()
	m.refreshTranscript()
	if result.Finished {
		return m.submitNextQueued()
	}
	return nil
}

func (m *Model) isTaskRunning() bool {
	return m != nil && ((m.State != nil && strings.EqualFold(strings.TrimSpace(m.State.Status), "running")) || m.mcpStartupActive)
}

func (m *Model) isIdle() bool {
	return m != nil && m.State != nil && strings.EqualFold(strings.TrimSpace(m.State.Status), "idle") && !m.mcpStartupActive
}

func (m *Model) setStatus(status string) {
	if m == nil || m.State == nil {
		return
	}
	m.State.SetStatus(status)
	m.syncTaskRunningTimer()
}

func (m *Model) syncTaskRunningTimer() {
	if m == nil {
		return
	}
	if m.isTaskRunning() {
		if m.taskStartedAt.IsZero() {
			m.taskStartedAt = m.currentTime()
		}
		return
	}
	m.taskStartedAt = time.Time{}
}

func (m *Model) renderWorkingIndicator() string {
	if m == nil || !m.isTaskRunning() {
		return ""
	}
	now := m.currentTime()
	if m.taskStartedAt.IsZero() {
		m.taskStartedAt = now
	}
	indicator := codextui.NewStatusIndicator(m.taskStartedAt)
	if m.mcpStartupActive && strings.TrimSpace(m.mcpStartupHeader) != "" {
		indicator.Header = m.mcpStartupHeader
	}
	indicator.InterruptHint = m.interruptHintBinding()
	indicator.SetInterruptHintVisible(indicator.InterruptHint != "")
	width := m.width
	if width > 2 {
		width -= 2
	}
	lines := indicator.Render(width, now)
	if len(lines) == 0 {
		return ""
	}
	lines[0] = "\u2022 " + lines[0]
	lines[0] = fitTerminalLine(lines[0], m.width)
	return strings.Join(lines, "\n")
}

func (m *Model) interruptHintBinding() string {
	if m == nil {
		return ""
	}
	bindings, _, _ := codextui.ResolvedKeymapBindings(m.keymapConfig, "chat", "interrupt_turn")
	if len(bindings) == 0 {
		return ""
	}
	return strings.TrimSpace(bindings[0])
}

func (m *Model) markThreadStarted(threadID string) {
	if m == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	if m.startedThreadIDs == nil {
		m.startedThreadIDs = map[string]bool{}
	}
	m.startedThreadIDs[threadID] = true
}

func (m *Model) markThreadCompleted(threadID string) {
	if m == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	if m.completedThreadIDs == nil {
		m.completedThreadIDs = map[string]bool{}
	}
	m.completedThreadIDs[threadID] = true
}

func (m *Model) clearCurrentThreadAfterFailure(message string) {
	if m == nil || m.State == nil {
		return
	}
	threadID := strings.TrimSpace(m.State.ThreadID)
	if threadID == "" {
		return
	}
	lower := strings.ToLower(strings.TrimSpace(message))
	if strings.Contains(lower, "thread not found") || (m.startedThreadIDs[threadID] && !m.completedThreadIDs[threadID]) {
		m.State.SetThreadID("")
	}
}

func (m *Model) addErrorHistoryMessage(message string) {
	if m == nil || m.State == nil {
		return
	}
	message = normalizedErrorHistoryMessage(message)
	m.addHistoryCell(historycell.NewErrorEvent(message))
}

func (m *Model) addTurnErrorHistoryMessage(message string) {
	if m == nil || m.State == nil {
		return
	}
	message = normalizedErrorHistoryMessage(message)
	if message == m.lastTurnError {
		return
	}
	m.lastTurnError = message
	m.addHistoryCell(historycell.NewErrorEvent(message))
}

func normalizedErrorHistoryMessage(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		message = "Unknown error"
	}
	if !strings.HasPrefix(strings.ToLower(message), "error:") {
		message = "Error: " + message
	}
	return message
}

func (m *Model) addInfoHistoryMessage(message string) {
	if m == nil || m.State == nil {
		return
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	m.addHistoryCell(historycell.NewInfoEvent(message, ""))
}

func (m *Model) addHistoryCell(cell historycell.HistoryCell) {
	if m == nil || m.State == nil {
		return
	}
	width := m.width
	if width < 20 {
		width = 20
	}
	m.State.AddHistoryLines(cell.DisplayLines(width), cell.RawLines())
}

func (m *Model) shouldSubmitOnTab() bool {
	if m == nil {
		return false
	}
	input := strings.TrimSpace(m.composer.Value())
	if input == "" && len(m.attachments) == 0 {
		return false
	}
	return !strings.HasPrefix(input, "!")
}

func (m *Model) applyTurnCompleted(message TurnCompletedMsg) bubbletea.Cmd {
	if message.Err != nil {
		m.setStatus("error")
		errorMessage := message.Err.Error()
		m.markActiveToolCallsFailed(errorMessage)
		m.clearCurrentThreadAfterFailure(errorMessage)
		m.addTurnErrorHistoryMessage(errorMessage)
		m.notice = errorMessage
		m.refreshTranscript()
		return nil
	}
	if strings.TrimSpace(message.ThreadID) != "" {
		m.State.SetThreadID(message.ThreadID)
		m.markThreadCompleted(message.ThreadID)
	} else if m.State != nil {
		m.markThreadCompleted(m.State.ThreadID)
	}
	if strings.TrimSpace(message.AssistantMessage) != "" {
		m.mergeAssistantFinal(message.AssistantMessage)
	}
	m.setStatus("idle")
	m.lastTurnError = ""
	m.notice = ""
	m.refreshTranscript()
	if len(m.queued) > 0 || (m.currentGoal != nil && m.currentGoal.Status == appserver.GoalActive) {
		return nil
	}
	response := message.AssistantMessage
	if strings.TrimSpace(response) == "" {
		response = m.lastAssistantNotificationResponse()
	}
	return m.queueNotification(chatwidget.AgentTurnCompleteNotification(response))
}

func (m *Model) applyTurnInterrupted(message TurnInterruptedMsg) {
	if m == nil || !m.isTaskRunning() {
		return
	}
	m.setStatus("idle")
	text := "Interrupted current turn."
	if message.Err != nil {
		text = "Interrupted current turn: " + message.Err.Error()
	}
	m.notice = text
	m.markActiveToolCallsFailed(text)
	m.clearCurrentThreadAfterFailure(text)
	m.addInfoHistoryMessage(text)
	m.refreshTranscript()
}

func (m *Model) interruptRunningTask() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	m.clearComposerPasteWindow()
	if m.mcpStartupActive && (m.State == nil || !strings.EqualFold(strings.TrimSpace(m.State.Status), "running")) {
		if m.onInterruptMCPStartup != nil {
			return m.onInterruptMCPStartup()
		}
		return func() bubbletea.Msg { return MCPStartupFinishAfterLagMsg{} }
	}
	if m.onInterrupt != nil {
		if cmd := m.onInterrupt(); cmd != nil {
			return cmd
		}
	}
	return func() bubbletea.Msg {
		return TurnInterruptedMsg{}
	}
}

func (m *Model) applyThreadEvent(event protocol.ThreadEvent) bubbletea.Cmd {
	var cmd bubbletea.Cmd
	switch event.Type {
	case "thread.started":
		m.State.SetThreadID(event.ThreadID)
		m.markThreadStarted(event.ThreadID)
	case "turn.started":
		m.setStatus("running")
		m.lastTurnError = ""
	case "item.started":
		m.applyItemStarted(event.Item)
	case "item.completed":
		cmd = m.applyItemCompleted(event.Item)
	case "item.delta":
		m.applyDelta(event.Delta)
	case "turn.completed":
		m.setStatus("idle")
		m.markThreadCompleted(m.State.ThreadID)
		m.lastTurnError = ""
	case "turn.failed", "error":
		message := "Unknown error"
		if event.Error != nil && strings.TrimSpace(event.Error.Message) != "" {
			message = strings.TrimSpace(event.Error.Message)
		}
		m.setStatus("error")
		m.markActiveToolCallsFailed(message)
		m.clearCurrentThreadAfterFailure(message)
		m.addTurnErrorHistoryMessage(message)
		m.notice = message
	case "response.rate_limits":
		if event.RateLimit != nil {
			cmd = m.applyRateLimitSnapshot(rateLimitSnapshotFromProtocol(event.RateLimit))
		}
	}
	m.refreshTranscript()
	return cmd
}

func (m *Model) applyItemStarted(item *protocol.ThreadItem) {
	if item == nil {
		return
	}
	switch item.Type {
	case "command_execution":
		m.renderCommandExecutionItem(item)
	case "mcp_tool_call":
		m.renderMCPToolCallItem(item, false)
	case "tool_call":
		m.startOrUpdateToolCall(item)
	case "agent_message":
		// Streaming deltas create the visible assistant message.
	case "imageGeneration":
		// The completed event carries the saved path.
	}
}

func (m *Model) applyItemCompleted(item *protocol.ThreadItem) bubbletea.Cmd {
	if item == nil {
		return nil
	}
	switch item.Type {
	case "agent_message":
		m.mergeAssistantFinal(item.Text)
	case "command_execution":
		m.renderCommandExecutionItem(item)
	case "mcp_tool_call":
		m.renderMCPToolCallItem(item, true)
	case "tool_call":
		m.startOrUpdateToolCall(item)
	case "tool_output":
		if request, ok := approvalRequestFromToolOutput(item); ok {
			m.notice = "Approval required: " + displayValue(item.ToolName, item.ID)
			return m.openApprovalModal(request)
		}
		m.completeToolOutput(item)
	case "todo_list":
		m.applyPlanUpdateItem(item)
	case "imageGeneration":
		m.applyImageGenerationItem(item)
	}
	return nil
}

func (m *Model) applyImageGenerationItem(item *protocol.ThreadItem) {
	if m == nil || item == nil {
		return
	}
	status := firstNonEmpty(strings.TrimSpace(item.Status), metadataString(item.Metadata, "status"))
	revisedPrompt := firstNonEmpty(strings.TrimSpace(item.RevisedPrompt), metadataString(item.Metadata, "revisedPrompt"), metadataString(item.Metadata, "revised_prompt"))
	savedPath := firstNonEmpty(strings.TrimSpace(item.SavedPath), metadataString(item.Metadata, "savedPath"), metadataString(item.Metadata, "saved_path"))
	m.applyHistoryCell(historycell.NewImageGenerationCall(item.ID, status, revisedPrompt, savedPath))
}

func (m *Model) applyDelta(delta *protocol.Delta) {
	if delta == nil {
		return
	}
	if delta.Text != "" {
		m.appendAssistantDelta(delta.ItemID, delta.Text)
		return
	}
	if strings.TrimSpace(delta.Input) != "" {
		m.appendToolCallInputDelta(delta)
	}
}

func (m *Model) startOrUpdateToolCall(item *protocol.ThreadItem) {
	state := m.toolCallStateForItem(item, true)
	if state == nil {
		return
	}
	if strings.TrimSpace(item.ToolName) != "" {
		state.ToolName = strings.TrimSpace(item.ToolName)
	}
	if strings.TrimSpace(item.CallID) != "" {
		state.CallID = strings.TrimSpace(item.CallID)
	}
	if item.Input != "" {
		state.Input = item.Input
	}
	if state.StartedAt.IsZero() {
		state.StartedAt = m.currentTime()
	}
	m.registerToolCallState(state, toolCallAliasesFromItem(item)...)
	if state.PlanUpdate || isPlanUpdateToolName(state.ToolName) {
		state.PlanUpdate = true
		if m.renderPlanUpdateToolCall(state) {
			return
		}
		return
	}
	if !toolCallStateReadyForDisplay(state, nil) {
		return
	}
	m.renderToolCallState(state, nil)
}

func (m *Model) appendToolCallInputDelta(delta *protocol.Delta) {
	if m == nil || delta == nil || delta.Input == "" {
		return
	}
	state := m.toolCallStateForDelta(delta, true)
	if state == nil {
		return
	}
	state.Input += delta.Input
	if strings.TrimSpace(delta.CallID) != "" {
		state.CallID = strings.TrimSpace(delta.CallID)
	}
	if state.StartedAt.IsZero() {
		state.StartedAt = m.currentTime()
	}
	m.registerToolCallState(state, toolCallAliasesFromDelta(delta)...)
	if state.PlanUpdate || isPlanUpdateToolName(state.ToolName) {
		state.PlanUpdate = true
		if m.renderPlanUpdateToolCall(state) {
			return
		}
		return
	}
	if !toolCallStateReadyForDisplay(state, nil) {
		return
	}
	m.renderToolCallState(state, nil)
}

func (m *Model) completeToolOutput(item *protocol.ThreadItem) {
	if m == nil || item == nil {
		return
	}
	state := m.toolCallStateForItem(item, false)
	if state == nil {
		state = m.findToolCallByToolName(item.ToolName)
	}
	if state == nil {
		state = &toolCallDisplayState{
			ID:           strings.TrimSpace(item.ID),
			CallID:       firstNonEmpty(strings.TrimSpace(item.CallID), metadataString(item.Metadata, "call_id")),
			ToolName:     strings.TrimSpace(item.ToolName),
			MessageIndex: -1,
			StartedAt:    m.currentTime(),
		}
	}
	if strings.TrimSpace(item.ToolName) != "" {
		state.ToolName = strings.TrimSpace(item.ToolName)
	}
	if strings.TrimSpace(item.CallID) != "" {
		state.CallID = strings.TrimSpace(item.CallID)
	} else if callID := metadataString(item.Metadata, "call_id"); callID != "" {
		state.CallID = callID
	}
	m.registerToolCallState(state, toolCallAliasesFromItem(item)...)
	if state.PlanUpdate || isPlanUpdateToolName(state.ToolName) || metadataBool(item.Metadata, "planUpdate") {
		state.PlanUpdate = true
		if !m.renderPlanUpdateToolCall(state) {
			state.Completed = true
		}
		return
	}
	if !toolCallStateReadyForDisplay(state, item) {
		state.Completed = true
		return
	}
	m.renderToolCallState(state, item)
}

func (m *Model) renderCommandExecutionItem(item *protocol.ThreadItem) {
	if m == nil || item == nil || strings.TrimSpace(item.Command) == "" {
		return
	}
	state := m.toolCallStateForItem(item, true)
	if state == nil {
		return
	}
	state.ToolName = "exec_command"
	state.Input = strings.TrimSpace(item.Command)
	state.CallID = firstNonEmpty(strings.TrimSpace(item.CallID), strings.TrimSpace(item.ID))
	if state.StartedAt.IsZero() {
		state.StartedAt = m.currentTime()
	}
	m.registerToolCallState(state, toolCallAliasesFromItem(item)...)

	width := m.width
	if width < 20 {
		width = 20
	}
	call := execcell.ExecCall{
		CallID:  firstNonEmpty(state.CallID, state.ID),
		Command: shellScriptCommandForDisplay(item.Command),
		Source:  execcell.ExecSourceAgent,
	}
	if commandExecutionInProgress(item.Status) {
		started := state.StartedAt
		call.StartTime = &started
	} else {
		exitCode := 0
		if item.ExitCode != nil {
			exitCode = *item.ExitCode
		} else if strings.EqualFold(strings.TrimSpace(item.Status), "failed") || strings.EqualFold(strings.TrimSpace(item.Status), "declined") {
			exitCode = 1
		}
		output := ""
		if item.AggregatedOutput != nil {
			output = *item.AggregatedOutput
		}
		call.Output = &execcell.CommandOutput{
			ExitCode:         exitCode,
			AggregatedOutput: output,
			FormattedOutput:  output,
		}
		state.Completed = true
	}
	cell := execcell.NewExecCell(call, false)
	state.MessageIndex = m.upsertHistoryMessage(state.MessageIndex, cell.DisplayLinesWithTheme(width, m.activeTUITheme()), cell.RawLines())
	if state.Completed {
		m.needsFinalMessageSeparator = true
	}
}

func (m *Model) renderMCPToolCallItem(item *protocol.ThreadItem, completed bool) {
	if m == nil || item == nil {
		return
	}
	id := firstNonEmpty(strings.TrimSpace(item.ID), strings.TrimSpace(item.CallID))
	if id == "" {
		return
	}
	if m.mcpToolCalls == nil {
		m.mcpToolCalls = map[string]*mcpToolCallDisplayState{}
	}
	state := m.mcpToolCalls[id]
	if state == nil {
		state = &mcpToolCallDisplayState{ID: id, MessageIndex: -1}
		m.mcpToolCalls[id] = state
	}
	if server := strings.TrimSpace(item.Server); server != "" {
		state.Invocation.Server = server
	}
	if toolName := strings.TrimSpace(item.Tool); toolName != "" {
		state.Invocation.Tool = toolName
	}
	if item.Arguments != nil {
		state.Invocation.Arguments = compactMCPArguments(*item.Arguments)
	}

	var cell historycell.McpToolCallCell
	if completed || !mcpToolCallInProgress(item.Status) {
		cell = historycell.NewMcpToolCall(id, state.Invocation, mcpToolResultFromProtocolItem(item))
		m.needsFinalMessageSeparator = true
	} else {
		cell = historycell.NewActiveMcpToolCall(id, state.Invocation)
	}
	width := m.width
	if width < 20 {
		width = 20
	}
	state.MessageIndex = m.upsertHistoryMessage(state.MessageIndex, cell.DisplayLines(width), cell.RawLines())
}

func mcpToolCallInProgress(status string) bool {
	status = strings.ToLower(strings.TrimSpace(status))
	return status == "" || status == "in_progress" || status == "inprogress" || status == "running"
}

func compactMCPArguments(arguments any) string {
	if arguments == nil {
		return ""
	}
	if raw, ok := arguments.(string); ok {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return ""
		}
		var decoded any
		if json.Unmarshal([]byte(raw), &decoded) == nil {
			arguments = decoded
		} else {
			return raw
		}
	}
	data, err := json.Marshal(arguments)
	if err != nil {
		return strings.TrimSpace(fmt.Sprint(arguments))
	}
	return string(data)
}

func mcpToolResultFromProtocolItem(item *protocol.ThreadItem) historycell.McpToolResult {
	if item == nil {
		return historycell.McpToolResult{Error: "MCP tool call completed without a result", IsError: true}
	}
	if item.CallError != nil && strings.TrimSpace(item.CallError.Message) != "" {
		return historycell.McpToolResult{Error: strings.TrimSpace(item.CallError.Message), IsError: true}
	}
	if strings.EqualFold(strings.TrimSpace(item.Status), "failed") {
		return historycell.McpToolResult{Error: "MCP tool call failed", IsError: true}
	}
	if item.Result == nil {
		return historycell.McpToolResult{}
	}
	content := make([]string, 0, len(item.Result.Content))
	for _, block := range item.Result.Content {
		if text := mcpContentBlockText(block); text != "" {
			content = append(content, text)
		}
	}
	return historycell.McpToolResult{Content: content}
}

func mcpContentBlockText(block any) string {
	switch value := block.(type) {
	case nil:
		return ""
	case string:
		return value
	case map[string]any:
		switch strings.ToLower(strings.TrimSpace(fmt.Sprint(value["type"]))) {
		case "text":
			return strings.TrimSpace(fmt.Sprint(value["text"]))
		case "image", "image_url":
			return "<image content>"
		case "audio":
			return "<audio content>"
		case "resource_link", "resourcelink":
			return "link: " + strings.TrimSpace(fmt.Sprint(value["uri"]))
		}
	}
	data, err := json.Marshal(block)
	if err != nil {
		return strings.TrimSpace(fmt.Sprint(block))
	}
	return string(data)
}

func commandExecutionInProgress(status string) bool {
	status = strings.ToLower(strings.TrimSpace(status))
	return status == "" || status == "in_progress" || status == "inprogress" || status == "running"
}

func (m *Model) applyPlanUpdateItem(item *protocol.ThreadItem) {
	if m == nil || item == nil {
		return
	}
	plan := make([]historycell.PlanItemArg, 0, len(item.Items))
	for _, protocolItem := range item.Items {
		step := strings.TrimSpace(protocolItem.Text)
		if step == "" {
			continue
		}
		status := historycell.StepPending
		if protocolItem.Completed {
			status = historycell.StepCompleted
		}
		plan = append(plan, historycell.PlanItemArg{Step: step, Status: status})
	}
	m.applyHistoryCell(historycell.NewPlanUpdate("", plan))
}

type planUpdateToolInput struct {
	Explanation string                    `json:"explanation,omitempty"`
	Plan        []planUpdateToolInputItem `json:"plan"`
}

type planUpdateToolInputItem struct {
	Step   string `json:"step"`
	Status string `json:"status"`
}

func (m *Model) renderPlanUpdateToolCall(state *toolCallDisplayState) bool {
	if m == nil || state == nil {
		return false
	}
	explanation, plan, ok := planUpdateFromToolInput(state.Input)
	if !ok {
		return false
	}
	width := m.width
	if width < 20 {
		width = 20
	}
	cell := historycell.NewPlanUpdate(explanation, plan)
	state.MessageIndex = m.upsertHistoryMessage(state.MessageIndex, cell.DisplayLines(width), cell.RawLines())
	state.Completed = true
	return true
}

func planUpdateFromToolInput(input string) (string, []historycell.PlanItemArg, bool) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", nil, false
	}
	var args planUpdateToolInput
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return "", nil, false
	}
	if args.Plan == nil && strings.TrimSpace(args.Explanation) == "" {
		return "", nil, false
	}
	plan := make([]historycell.PlanItemArg, 0, len(args.Plan))
	for _, item := range args.Plan {
		step := strings.TrimSpace(item.Step)
		if step == "" {
			continue
		}
		plan = append(plan, historycell.PlanItemArg{
			Step:   step,
			Status: planStepStatusFromString(item.Status),
		})
	}
	return strings.TrimSpace(args.Explanation), plan, true
}

func planStepStatusFromString(status string) historycell.StepStatus {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "complete", "done":
		return historycell.StepCompleted
	case "in_progress", "in-progress", "active", "running":
		return historycell.StepInProgress
	default:
		return historycell.StepPending
	}
}

func (m *Model) renderToolCallState(state *toolCallDisplayState, outputItem *protocol.ThreadItem) {
	if m == nil || state == nil {
		return
	}
	width := m.width
	if width < 20 {
		width = 20
	}
	command := commandForToolDisplay(state.ToolName, state.Input, outputMetadata(outputItem))
	output, duration := commandOutputForToolOutput(outputItem)
	callID := firstNonEmpty(state.CallID, state.ID)
	call := execcell.ExecCall{
		CallID:   callID,
		Command:  command,
		Output:   output,
		Source:   execcell.ExecSourceAgent,
		Duration: duration,
	}
	if output == nil && !state.StartedAt.IsZero() {
		started := state.StartedAt
		call.StartTime = &started
	}
	cell := execcell.NewExecCell(call, false)
	state.MessageIndex = m.upsertHistoryMessage(state.MessageIndex, cell.DisplayLinesWithTheme(width, m.activeTUITheme()), cell.RawLines())
	if output != nil {
		state.Completed = true
	}
}

func (m *Model) markActiveToolCallsFailed(message string) {
	if m == nil || len(m.toolCalls) == 0 {
		return
	}
	seen := map[*toolCallDisplayState]bool{}
	for _, state := range m.toolCalls {
		if state == nil || seen[state] || state.Completed || state.PlanUpdate {
			continue
		}
		seen[state] = true
		m.renderToolCallFailure(state, message)
	}
}

func (m *Model) renderToolCallFailure(state *toolCallDisplayState, message string) {
	if m == nil || state == nil {
		return
	}
	if !toolCallStateReadyForDisplay(state, nil) {
		state.Completed = true
		return
	}
	width := m.width
	if width < 20 {
		width = 20
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = "tool call did not complete"
	}
	if !strings.HasPrefix(strings.ToLower(message), "error:") && !strings.HasPrefix(strings.ToLower(message), "interrupted") {
		message = "Error: " + message
	}
	var duration *time.Duration
	if !state.StartedAt.IsZero() {
		elapsed := m.currentTime().Sub(state.StartedAt)
		if elapsed < 0 {
			elapsed = 0
		}
		duration = &elapsed
	}
	call := execcell.ExecCall{
		CallID:  firstNonEmpty(state.CallID, state.ID),
		Command: commandForToolDisplay(state.ToolName, state.Input, nil),
		Output: &execcell.CommandOutput{
			ExitCode:         1,
			AggregatedOutput: message,
			FormattedOutput:  message,
		},
		Source:   execcell.ExecSourceAgent,
		Duration: duration,
	}
	cell := execcell.NewExecCell(call, false)
	state.MessageIndex = m.upsertHistoryMessage(state.MessageIndex, cell.DisplayLinesWithTheme(width, m.activeTUITheme()), cell.RawLines())
	state.Completed = true
}

func (m *Model) upsertHistoryMessage(index int, displayLines []string, rawLines []string) int {
	if m == nil || m.State == nil {
		return -1
	}
	display := strings.TrimRight(strings.Join(displayLines, "\n"), "\r\n")
	if strings.TrimSpace(display) == "" {
		return index
	}
	raw := strings.TrimRight(strings.Join(rawLines, "\n"), "\r\n")
	message := codextui.Message{Role: codextui.RoleHistory, Text: display, RawText: raw}
	if index >= 0 && index < len(m.State.Messages) && m.State.Messages[index].Role == codextui.RoleHistory {
		m.State.Messages[index] = message
		return index
	}
	m.State.Messages = append(m.State.Messages, message)
	return len(m.State.Messages) - 1
}

func (m *Model) toolCallStateForItem(item *protocol.ThreadItem, create bool) *toolCallDisplayState {
	if m == nil || item == nil {
		return nil
	}
	m.ensureToolCallState()
	aliases := toolCallAliasesFromItem(item)
	for _, alias := range aliases {
		if state := m.toolCalls[alias]; state != nil {
			m.registerToolCallState(state, aliases...)
			return state
		}
	}
	if !create {
		return nil
	}
	state := &toolCallDisplayState{
		ID:           strings.TrimSpace(item.ID),
		CallID:       firstNonEmpty(strings.TrimSpace(item.CallID), metadataString(item.Metadata, "call_id")),
		ToolName:     strings.TrimSpace(item.ToolName),
		Input:        item.Input,
		MessageIndex: -1,
		StartedAt:    m.currentTime(),
	}
	m.registerToolCallState(state, aliases...)
	return state
}

func (m *Model) toolCallStateForDelta(delta *protocol.Delta, create bool) *toolCallDisplayState {
	if m == nil || delta == nil {
		return nil
	}
	m.ensureToolCallState()
	aliases := toolCallAliasesFromDelta(delta)
	for _, alias := range aliases {
		if state := m.toolCalls[alias]; state != nil {
			m.registerToolCallState(state, aliases...)
			return state
		}
	}
	if !create {
		return nil
	}
	state := &toolCallDisplayState{
		ID:           strings.TrimSpace(delta.ItemID),
		CallID:       strings.TrimSpace(delta.CallID),
		MessageIndex: -1,
		StartedAt:    m.currentTime(),
	}
	m.registerToolCallState(state, aliases...)
	return state
}

func (m *Model) findToolCallByToolName(toolName string) *toolCallDisplayState {
	if m == nil || len(m.toolCalls) == 0 {
		return nil
	}
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return nil
	}
	for _, state := range m.toolCalls {
		if state != nil && strings.EqualFold(strings.TrimSpace(state.ToolName), toolName) {
			return state
		}
	}
	return nil
}

func (m *Model) ensureToolCallState() {
	if m != nil && m.toolCalls == nil {
		m.toolCalls = map[string]*toolCallDisplayState{}
	}
}

func (m *Model) registerToolCallState(state *toolCallDisplayState, aliases ...string) {
	if m == nil || state == nil {
		return
	}
	m.ensureToolCallState()
	if strings.TrimSpace(state.ID) != "" {
		aliases = append(aliases, state.ID)
	}
	if strings.TrimSpace(state.CallID) != "" {
		aliases = append(aliases, state.CallID)
	}
	for _, alias := range aliases {
		for _, variant := range toolCallAliasVariants(alias) {
			m.toolCalls[variant] = state
		}
	}
}

func toolCallAliasesFromItem(item *protocol.ThreadItem) []string {
	if item == nil {
		return nil
	}
	aliases := []string{item.ID, item.CallID}
	if callID := metadataString(item.Metadata, "call_id"); callID != "" {
		aliases = append(aliases, callID)
	}
	return aliases
}

func toolCallAliasesFromDelta(delta *protocol.Delta) []string {
	if delta == nil {
		return nil
	}
	return []string{delta.ItemID, delta.CallID}
}

func toolCallAliasVariants(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	variants := []string{value}
	for _, prefix := range []string{"tool-call-", "tool-output-"} {
		if strings.HasPrefix(value, prefix) {
			trimmed := strings.TrimSpace(strings.TrimPrefix(value, prefix))
			if trimmed != "" {
				variants = append(variants, trimmed)
			}
		}
	}
	return variants
}

func outputMetadata(item *protocol.ThreadItem) map[string]any {
	if item == nil {
		return nil
	}
	return item.Metadata
}

func toolCallStateReadyForDisplay(state *toolCallDisplayState, outputItem *protocol.ThreadItem) bool {
	if state == nil {
		return false
	}
	if !isShellToolName(state.ToolName) {
		return true
	}
	metadata := outputMetadata(outputItem)
	if command := metadataStringSlice(metadata, "command"); len(command) > 0 {
		return true
	}
	if command := metadataString(metadata, "hook_command"); command != "" {
		return true
	}
	input := strings.TrimSpace(state.Input)
	if execCommandInputCommand(input) != "" {
		return true
	}
	if input == "" || strings.HasPrefix(input, "{") {
		return false
	}
	return input != ""
}

func commandForToolDisplay(toolName string, input string, metadata map[string]any) []string {
	if command := metadataStringSlice(metadata, "command"); len(command) > 0 {
		return command
	}
	if command := metadataString(metadata, "hook_command"); command != "" {
		return []string{command}
	}
	if command := execCommandInputCommand(input); command != "" {
		return shellScriptCommandForDisplay(command)
	}
	toolName = strings.TrimSpace(toolName)
	input = strings.TrimSpace(input)
	switch {
	case toolName == "" && input == "":
		return []string{"tool call"}
	case input == "":
		return []string{toolName}
	case isShellToolName(toolName):
		return shellScriptCommandForDisplay(input)
	default:
		return []string{strings.TrimSpace(toolName + " " + input)}
	}
}

func shellScriptCommandForDisplay(command string) []string {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}
	return []string{"bash", "-lc", command}
}

func execCommandInputCommand(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(input), &payload); err != nil {
		return ""
	}
	for _, key := range []string{"cmd", "command"} {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func isShellToolName(toolName string) bool {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "exec_command", "shell":
		return true
	default:
		return false
	}
}

func isPlanUpdateToolName(toolName string) bool {
	return strings.EqualFold(strings.TrimSpace(toolName), "update_plan")
}

func commandOutputForToolOutput(item *protocol.ThreadItem) (*execcell.CommandOutput, *time.Duration) {
	if item == nil {
		return nil, nil
	}
	exitCode := 0
	if item.Success != nil && !*item.Success {
		exitCode = 1
	}
	if code, ok := metadataInt(item.Metadata, "exit_code"); ok {
		exitCode = code
	} else if code, ok := shellExitCodeFromBody(item.Output); ok {
		exitCode = code
	}
	text := firstNonEmpty(
		metadataString(item.Metadata, "hook_response"),
		combinedShellOutput(metadataString(item.Metadata, "stdout"), metadataString(item.Metadata, "stderr")),
		shellOutputFromBody(item.Output),
		strings.TrimRight(item.Output, "\r\n"),
	)
	output := &execcell.CommandOutput{
		ExitCode:         exitCode,
		AggregatedOutput: text,
		FormattedOutput:  text,
	}
	var duration *time.Duration
	if durationMS, ok := metadataInt(item.Metadata, "duration_ms"); ok {
		value := time.Duration(durationMS) * time.Millisecond
		duration = &value
	}
	return output, duration
}

func combinedShellOutput(stdout string, stderr string) string {
	stdout = strings.TrimRight(stdout, "\r\n")
	stderr = strings.TrimRight(stderr, "\r\n")
	switch {
	case stdout == "":
		return stderr
	case stderr == "":
		return stdout
	default:
		return stdout + "\n\nstderr:\n" + stderr
	}
}

func shellOutputFromBody(body string) string {
	body = strings.TrimRight(body, "\r\n")
	if body == "" {
		return ""
	}
	for _, marker := range []string{"\nOutput:\n", "\r\nOutput:\r\n", "\r\nOutput:\n"} {
		if index := strings.Index(body, marker); index >= 0 {
			return strings.TrimRight(body[index+len(marker):], "\r\n")
		}
	}
	return ""
}

func shellExitCodeFromBody(body string) (int, bool) {
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "Process exited with code ") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, "Process exited with code "))
		code, err := strconv.Atoi(value)
		return code, err == nil
	}
	return 0, false
}

func metadataInt(metadata map[string]any, key string) (int, bool) {
	if metadata == nil {
		return 0, false
	}
	switch value := metadata[key].(type) {
	case int:
		return value, true
	case int8:
		return int(value), true
	case int16:
		return int(value), true
	case int32:
		return int(value), true
	case int64:
		return int(value), true
	case uint:
		return int(value), true
	case uint8:
		return int(value), true
	case uint16:
		return int(value), true
	case uint32:
		return int(value), true
	case uint64:
		return int(value), true
	case float32:
		return int(value), true
	case float64:
		return int(value), true
	case json.Number:
		parsed, err := value.Int64()
		return int(parsed), err == nil
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		return parsed, err == nil
	default:
		return 0, false
	}
}

func (m *Model) applyHookRun(message HookRunMsg) {
	if m == nil {
		return
	}
	if strings.TrimSpace(message.ThreadID) != "" {
		m.State.SetThreadID(strings.TrimSpace(message.ThreadID))
	}
	entries := make([]historycell.HookOutputEntry, 0, len(message.Entries))
	for _, entry := range message.Entries {
		if strings.TrimSpace(entry.Kind) == "" && strings.TrimSpace(entry.Text) == "" {
			continue
		}
		entries = append(entries, historycell.HookOutputEntry{
			Kind: historycell.HookOutputKind(strings.TrimSpace(entry.Kind)),
			Text: strings.TrimSpace(entry.Text),
		})
	}
	var cell historycell.HookRunCell
	if message.Running {
		cell = historycell.NewRunningHookRun(message.EventName, message.StatusMessage)
	} else {
		cell = historycell.NewHookRun(message.EventName, message.Status, message.StatusMessage, entries)
	}
	m.trackHookLifecycle(message, entries)
	width := m.width
	if width < 20 {
		width = 20
	}
	if message.Running {
		m.addBottomLines(cell.DisplayLines(width)...)
	} else {
		m.State.AddHistoryLines(cell.DisplayLines(width), cell.RawLines())
	}
	m.refreshTranscript()
}

func (m *Model) trackHookLifecycle(message HookRunMsg, entries []historycell.HookOutputEntry) {
	if m == nil {
		return
	}
	id := strings.TrimSpace(message.ID)
	if id == "" {
		id = strings.TrimSpace(message.EventName)
	}
	if id == "" {
		return
	}
	output := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.TrimSpace(entry.Text) != "" {
			output = append(output, strings.TrimSpace(entry.Text))
		}
	}
	run := chatwidget.HookRun{
		ID:      id,
		Name:    firstNonEmpty(strings.TrimSpace(message.EventName), id),
		Status:  hookStatusFromMessage(message.Status, message.Running),
		Output:  strings.Join(output, "\n"),
		Issue:   strings.TrimSpace(message.StatusMessage),
		Managed: false,
	}
	if message.Running {
		m.hookLifecycle.Start(run)
		return
	}
	m.hookLifecycle.Complete(run)
}

func hookStatusFromMessage(status string, running bool) chatwidget.HookStatus {
	if running {
		return chatwidget.HookStatusStarted
	}
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running", "started":
		return chatwidget.HookStatusStarted
	case "completed", "success", "succeeded":
		return chatwidget.HookStatusCompleted
	case "failed", "error":
		return chatwidget.HookStatusFailed
	case "blocked":
		return chatwidget.HookStatusBlocked
	case "stopped", "cancelled", "canceled":
		return chatwidget.HookStatusStopped
	default:
		return chatwidget.HookStatus(status)
	}
}

func (m *Model) addStartupSessionHeader(version string) {
	if m == nil || m.State == nil || len(m.State.Messages) > 0 {
		return
	}
	cwd := strings.TrimSpace(m.State.CWD)
	if cwd == "" {
		cwd = strings.TrimSpace(m.sessionCWD)
	}
	if cwd == "" {
		cwd = strings.TrimSpace(m.statusControlsRuntime().CWD)
	}
	header := historycell.NewSessionHeader(
		strings.TrimSpace(m.State.Model),
		m.State.EffectiveReasoningEffort(),
		false,
		cwd,
		firstNonEmpty(strings.TrimSpace(version), "dev"),
	)
	cell := historycell.NewSessionInfo(header, false, "")
	width := m.width
	if width < 20 {
		width = 20
	}
	m.State.AddHistoryLines(cell.DisplayLines(width), cell.RawLines())
}

func (m *Model) applyHistoryCell(cell historycell.HistoryCell) {
	if m == nil || cell == nil {
		return
	}
	width := m.width
	if width < 20 {
		width = 20
	}
	m.State.AddHistoryLines(cell.DisplayLines(width), cell.RawLines())
	m.refreshTranscript()
}

func (m *Model) applyWarningMessage(message string) {
	if m == nil {
		return
	}
	message = strings.TrimSpace(message)
	if message == "" || !m.warningDisplay.ShouldDisplay(message) {
		return
	}
	width := m.width
	if width < 20 {
		width = 20
	}
	cell := historycell.NewWarningEvent(message)
	m.State.AddHistoryLines(cell.DisplayLines(width), cell.RawLines())
	m.notice = message
	m.refreshTranscript()
}

func warningMessageFromStatus(status string) (string, bool) {
	status = strings.TrimSpace(status)
	if len(status) < len("warning:") || !strings.EqualFold(status[:len("warning:")], "warning:") {
		return "", false
	}
	message := strings.TrimSpace(status[len("warning:"):])
	return message, message != ""
}

func (m *Model) applyRateLimitSnapshot(snapshot chatwidget.RateLimitSnapshot) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	limitID := strings.TrimSpace(snapshot.LimitID)
	if limitID == "" {
		limitID = "codex"
	}
	if m.rateLimitSnapshots == nil {
		m.rateLimitSnapshots = map[string]chatwidget.RateLimitSnapshot{}
	}
	if strings.TrimSpace(snapshot.PlanType) != "" {
		m.chatGPTPlanType = strings.TrimSpace(snapshot.PlanType)
	} else if m.chatGPTPlanType != "" {
		snapshot.PlanType = m.chatGPTPlanType
	}
	m.rateLimitSnapshots[limitID] = snapshot
	warnings := m.rateLimitWarnings.TakeWarnings(snapshot)
	if len(warnings) > 0 {
		width := m.width
		if width < 20 {
			width = 20
		}
		for _, warning := range warnings {
			cell := historycell.NewWarningEvent(warning)
			m.State.AddHistoryLines(cell.DisplayLines(width), cell.RawLines())
		}
		m.refreshTranscript()
	}
	return m.maybeOpenRateLimitSwitchPrompt(snapshot)
}

func rateLimitSnapshotFromProtocol(snapshot *protocol.RateLimitSnapshot) chatwidget.RateLimitSnapshot {
	if snapshot == nil {
		return chatwidget.RateLimitSnapshot{}
	}
	return chatwidget.RateLimitSnapshot{
		LimitID:   snapshot.LimitID,
		Primary:   rateLimitWindowFromProtocol(snapshot.Primary),
		Secondary: rateLimitWindowFromProtocol(snapshot.Secondary),
		Credits:   rateLimitCreditsFromProtocol(snapshot.Credits),
		PlanType:  snapshot.PlanType,
	}
}

func rateLimitWindowFromProtocol(window *protocol.RateLimitWindow) *chatwidget.RateLimitWindow {
	if window == nil {
		return nil
	}
	return &chatwidget.RateLimitWindow{
		UsedPercent:        window.UsedPercent,
		WindowDurationMins: cloneInt64PtrTea(window.WindowDurationMins),
	}
}

func rateLimitCreditsFromProtocol(credits *protocol.CreditsSnapshot) *chatwidget.RateLimitCredits {
	if credits == nil {
		return nil
	}
	return &chatwidget.RateLimitCredits{
		HasCredits: credits.HasCredits,
		Unlimited:  credits.Unlimited,
		Balance:    cloneStringPtrTea(credits.Balance),
	}
}

func cloneInt64PtrTea(value *int64) *int64 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneStringPtrTea(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneBoolPtrTea(value *bool) *bool {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func boolPtrValueTea(value *bool) bool {
	return value != nil && *value
}

func cloneUnifiedExecProcessDetails(values []historycell.UnifiedExecProcessDetails) []historycell.UnifiedExecProcessDetails {
	if values == nil {
		return nil
	}
	out := make([]historycell.UnifiedExecProcessDetails, len(values))
	for i := range values {
		out[i] = historycell.UnifiedExecProcessDetails{
			CommandDisplay: values[i].CommandDisplay,
			RecentChunks:   append([]string(nil), values[i].RecentChunks...),
		}
	}
	return out
}

func (m *Model) applyStreamMessage(message bubbletea.Msg) bubbletea.Cmd {
	if message == nil {
		return nil
	}
	_, cmd := m.Update(message)
	return cmd
}

func (m *Model) applyCommand(invocation *codextui.CommandInvocation) bubbletea.Cmd {
	if invocation == nil {
		return nil
	}
	if m.inSideConversation() && !sideSlashCommandAllowed(invocation.Command) {
		message := sideSlashUnavailableMessage(invocation.Name)
		m.State.AddHistoryLines([]string{message}, []string{message})
		m.notice = message
		m.refreshTranscript()
		return nil
	}
	switch invocation.Command {
	case codextui.CommandExit:
		return bubbletea.Quit
	case codextui.CommandHelp:
		m.State.AddMessage(codextui.RoleSystem, strings.TrimSpace(m.State.RenderHelp()))
		m.notice = ""
	case codextui.CommandKeymap:
		m.applyKeymapCommand(invocation.Args)
	case codextui.CommandStatus:
		m.State.AddMessage(codextui.RoleSystem, m.State.RenderStatusLine())
		m.notice = ""
	case codextui.CommandUsage:
		return m.applyUsageCommand(invocation.Args)
	case codextui.CommandGoal:
		return m.applyGoalCommand(invocation.Args)
	case codextui.CommandStatusline:
		return m.applyStatusLineCommand(invocation.Args)
	case codextui.CommandTitle:
		return m.applyTerminalTitleCommand(invocation.Args)
	case codextui.CommandDebugConfig:
		return m.applyDebugConfigCommand()
	case codextui.CommandNew:
		m.State.ResetThread()
		m.notice = "Started a new local thread."
	case codextui.CommandInit:
		return m.submitRequest(SubmitRequest{Prompt: initCommandPrompt()}, false)
	case codextui.CommandCompact:
		m.State.AddMessage(codextui.RoleSystem, "Compaction requested.")
		m.notice = "Compaction requested."
	case codextui.CommandClear:
		m.State.ClearMessages()
		m.notice = "Cleared visible transcript."
	case codextui.CommandCopy:
		m.copyLastAgentResponse()
	case codextui.CommandRaw:
		return m.applyRawOutputCommand(invocation.Args)
	case codextui.CommandDiff:
		return m.applyDiffCommand()
	case codextui.CommandPs:
		m.applyPsCommand()
	case codextui.CommandStop:
		return m.applyStopCommand()
	case codextui.CommandModel:
		m.applyModelSetting(invocation.Args)
	case codextui.CommandPersonality:
		return m.applyPersonalityCommand(invocation.Args)
	case codextui.CommandPlan:
		return m.applyPlanCommand(invocation.Args)
	case codextui.CommandAgent:
		return m.applyAgentCommand()
	case codextui.CommandSide:
		return m.applySideCommand(invocation.Name, invocation.Args)
	case codextui.CommandResume:
		return m.applyResumeCommand(invocation.Args)
	case codextui.CommandFork:
		m.openSessionPicker(codextui.SessionPickerFork)
	case codextui.CommandArchive:
		m.openSessionPicker(codextui.SessionPickerArchive)
	case codextui.CommandUnarchive:
		m.openSessionPicker(codextui.SessionPickerUnarchive)
	case codextui.CommandDelete:
		m.openSessionPicker(codextui.SessionPickerDelete)
	case codextui.CommandAttach:
		m.applyAttachmentCommand(invocation.Args, bottompane.AttachmentFile)
	case codextui.CommandImage:
		m.applyAttachmentCommand(invocation.Args, bottompane.AttachmentImage)
	case codextui.CommandURLImage:
		m.applyAttachmentCommand(invocation.Args, bottompane.AttachmentRemoteImage)
	case codextui.CommandClearAttachments:
		m.clearAttachments()
	case codextui.CommandEditor:
		return m.openExternalEditor()
	case codextui.CommandPermissions:
		m.openPermissionsMenu()
	case codextui.CommandApproval:
		m.applyApprovalSetting(invocation.Args)
	case codextui.CommandSandbox:
		m.applySandboxSetting(invocation.Args)
	case codextui.CommandExperimental:
		return m.applyExperimentalCommand(invocation.Args)
	case codextui.CommandReview:
		return m.applyReviewCommand(invocation.Args)
	case codextui.CommandRename:
		m.applyRenameCommand(invocation.Args)
	case codextui.CommandMention:
		m.composer.InsertString("@")
		m.notice = "Mention"
	case codextui.CommandSkills:
		m.openSkillsMenu()
	case codextui.CommandHooks:
		return m.applyHooksCommand()
	case codextui.CommandMcp:
		m.applyMCPCommand(invocation.Args)
	case codextui.CommandApps:
		return m.applyAppsCommand()
	case codextui.CommandPlugins:
		return m.applyPluginsCommand()
	case codextui.CommandTheme:
		return m.applyThemeCommand(invocation.Args)
	case codextui.CommandPets:
		return m.applyPetsCommand(invocation.Args)
	case codextui.CommandIde:
		m.applyHistoryCell(historycell.NewPlainHistoryCell([]string{"/ide", "", "IDE context is not connected in this runtime."}))
		m.notice = "IDE"
	case codextui.CommandVim:
		m.toggleVimMode()
	case codextui.CommandAutoReview:
		m.notice = "No recent auto-review denials in this thread."
	case codextui.CommandMemories:
		m.applyHistoryCell(historycell.NewPlainHistoryCell([]string{"/memories", "", "Memory configuration requires app-server support."}))
		m.notice = "Memories"
	case codextui.CommandFeedback:
		m.notice = "Feedback flow requires app-server support."
	case codextui.CommandApp:
		m.notice = "Codex Desktop handoff requires a remote app session."
	case codextui.CommandImport:
		m.applyHistoryCell(historycell.NewPlainHistoryCell([]string{"/import", "", "External agent config migration requires app-server support."}))
		m.notice = "Import"
	case codextui.CommandElevateSandbox:
		return m.applyWindowsSandboxSetupCommand(chatwidget.WindowsSandboxModeElevated)
	case codextui.CommandSandboxReadRoot:
		if strings.TrimSpace(invocation.Args) == "" {
			m.notice = "Usage: /sandbox-add-read-dir <absolute-directory-path>"
		} else {
			m.notice = "Sandbox read directory request requires Windows sandbox runtime."
		}
	case codextui.CommandRollout:
		m.notice = "Rollout path is not available yet."
	case codextui.CommandTestApproval:
		m.openApprovalModal(ApprovalRequestMsg{
			ID:      "test-approval",
			Title:   "Approval request",
			Body:    "Test approval request",
			Command: "echo test approval",
		})
	case codextui.CommandMemoryDrop, codextui.CommandMemoryUpdate:
		m.applyHistoryCell(historycell.NewPlainHistoryCell([]string{invocation.Name, "", "Memory maintenance requires app-server support."}))
		m.notice = "Memory maintenance"
	case codextui.CommandLogout:
		m.notice = "Logout requested."
	default:
		m.notice = "Unknown command " + invocation.Name + ". Type /help for commands."
	}
	m.refreshTranscript()
	return nil
}

func (m *Model) applyModelSetting(args string) {
	value := strings.TrimSpace(args)
	if value != "" {
		m.State.Model = value
		m.notice = strings.TrimSpace(m.State.RenderSetting("Model", m.State.Model))
		return
	}
	m.openModelPicker()
}

func (m *Model) applyApprovalSetting(args string) {
	value := strings.TrimSpace(args)
	if value != "" {
		if !codextui.ValidApprovalPolicy(value) {
			m.notice = "Approval must be one of untrusted, on-request, never."
			return
		}
		m.State.ApprovalPolicy = value
	}
	m.notice = strings.TrimSpace(m.State.RenderSetting("Approval", m.State.ApprovalPolicy))
}

func (m *Model) applySandboxSetting(args string) {
	value := strings.TrimSpace(args)
	if value != "" {
		m.State.Sandbox = value
	}
	m.notice = strings.TrimSpace(m.State.RenderSetting("Sandbox", m.State.Sandbox))
}

func (m *Model) ensureSize() {
	if m.width <= 0 || m.height <= 0 {
		m.resize(firstPositive(m.width, defaultWidth), firstPositive(m.height, defaultHeight))
	}
}

func (m *Model) resize(width int, height int) {
	m.width = firstPositive(width, defaultWidth)
	m.height = firstPositive(height, defaultHeight)
	composerHeight := defaultComposerHeight
	transcriptHeight := m.height - composerHeight - 3
	if transcriptHeight < minTranscriptHeight {
		transcriptHeight = minTranscriptHeight
	}
	m.transcript.Width = m.width
	m.transcript.Height = transcriptHeight
	m.composer.SetWidth(m.width)
	m.composer.SetHeight(composerHeight)
	m.refreshTranscript()
}

func (m *Model) refreshTranscript() {
	if m == nil {
		return
	}
	wasAtBottom := m.transcript.AtBottom()
	yOffset := m.transcript.YOffset
	m.transcript.SetContent(renderTranscript(m.State, m.rawOutput, m.transcript.Width, m.activeTUITheme()))
	if wasAtBottom {
		m.transcript.GotoBottom()
		return
	}
	m.transcript.SetYOffset(yOffset)
}

func (m *Model) openTranscriptOverlay() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	m.ensureSize()
	if m.overlay == nil {
		m.overlay = chatwidget.NewTranscriptOverlay(m.width, m.height, renderTranscript(m.State, m.rawOutput, m.width, m.activeTUITheme()))
		m.overlayTranscript = true
	} else {
		m.overlayTranscript = true
		m.syncTranscriptOverlay()
	}
	if m.noAltScreen && !m.overlayAltScreen {
		m.overlayAltScreen = true
		return bubbletea.EnterAltScreen
	}
	return nil
}

func (m *Model) closeTranscriptOverlay() bubbletea.Cmd {
	if m == nil || m.overlay == nil {
		return nil
	}
	m.overlay = nil
	m.overlayTranscript = false
	if m.overlayAltScreen {
		m.overlayAltScreen = false
		return bubbletea.ExitAltScreen
	}
	return nil
}

func (m *Model) syncTranscriptOverlay() {
	if m == nil || m.overlay == nil {
		return
	}
	m.overlay.Resize(m.width, m.height)
	if !m.overlayTranscript {
		return
	}
	m.overlay.SetContent(renderTranscript(m.State, m.rawOutput, m.width, m.activeTUITheme()))
}

func (m *Model) activeTUITheme() string {
	if m == nil {
		return ""
	}
	if m.modal != nil && m.modal.themePicker != nil {
		if themeID := strings.TrimSpace(m.modal.themePicker.PreviewThemeID()); themeID != "" {
			return themeID
		}
	}
	return m.tuiTheme
}

func (m *Model) updateTranscriptOverlayKey(msg bubbletea.KeyMsg) bubbletea.Cmd {
	if m == nil || m.overlay == nil {
		return nil
	}
	keySpec := keySpecFromKeyMsg(msg)
	if m.keyMatches("pager", "close", keySpec) || m.keyMatches("pager", "close_transcript", keySpec) {
		return m.closeTranscriptOverlay()
	}
	actions := []string{
		chatwidget.PagerScrollUp,
		chatwidget.PagerScrollDown,
		chatwidget.PagerPageUp,
		chatwidget.PagerPageDown,
		chatwidget.PagerHalfPageUp,
		chatwidget.PagerHalfPageDown,
		chatwidget.PagerJumpTop,
		chatwidget.PagerJumpBottom,
	}
	for _, action := range actions {
		if m.keyMatches("pager", action, keySpec) {
			m.overlay.ApplyPagerAction(action)
			return nil
		}
	}
	return nil
}

func (m *Model) updateTranscriptOverlayMouse(msg bubbletea.MouseMsg) bubbletea.Cmd {
	if m == nil || m.overlay == nil {
		return nil
	}
	return m.overlay.Update(msg)
}

func (m *Model) copyLastAgentResponse() {
	if m == nil {
		return
	}
	text, ok := chatwidget.LastAssistantMarkdown(m.State.Messages)
	if !ok {
		m.notice = "No agent response to copy."
		return
	}
	if m.clipboardWrite == nil {
		m.notice = "Clipboard is unavailable."
		return
	}
	if err := m.clipboardWrite(text); err != nil {
		m.notice = "Copy failed: " + err.Error()
		return
	}
	m.notice = "Copied last agent response."
}

const rawOutputModeOnNotice = "Raw output mode on: transcript text is shown for clean terminal selection."
const rawOutputModeOffNotice = "Raw output mode off: rich transcript rendering restored."
const rawOutputUsage = "Usage: /raw [on|off]"

func (m *Model) applyRawOutputCommand(args string) bubbletea.Cmd {
	value := strings.ToLower(strings.TrimSpace(args))
	switch value {
	case "":
		return m.toggleRawOutputMode()
	case "on":
		return m.setRawOutputMode(true)
	case "off":
		return m.setRawOutputMode(false)
	default:
		m.notice = rawOutputUsage
		m.refreshTranscript()
		return nil
	}
}

func (m *Model) toggleRawOutputMode() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	return m.setRawOutputMode(!m.rawOutput)
}

func (m *Model) setRawOutputMode(enabled bool) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	m.rawOutput = enabled
	if enabled {
		m.notice = rawOutputModeOnNotice
	} else {
		m.notice = rawOutputModeOffNotice
	}
	m.refreshTranscript()
	if m.overlay != nil {
		m.syncTranscriptOverlay()
	}
	return m.refreshStatusControlsCmd()
}

func (m *Model) applyDiffCommand() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	reader := m.onReadGitDiff
	if reader == nil {
		reader = defaultGitDiffReader
	}
	requestID := m.nextDiffRequest()
	m.pendingDiffRequestID = requestID
	m.notice = "Computing diff..."
	m.refreshTranscript()
	cwd := strings.TrimSpace(m.sessionCWD)
	return func() bubbletea.Msg {
		text, isGitRepo, err := reader(cwd)
		return DiffResultMsg{
			RequestID: requestID,
			Text:      text,
			IsGitRepo: isGitRepo,
			Err:       err,
		}
	}
}

func (m *Model) nextDiffRequest() uint64 {
	if m == nil {
		return 0
	}
	m.nextDiffRequestID++
	if m.nextDiffRequestID == 0 {
		m.nextDiffRequestID = 1
	}
	return m.nextDiffRequestID
}

func (m *Model) applyDiffResult(msg DiffResultMsg) bubbletea.Cmd {
	if m == nil || m.pendingDiffRequestID != msg.RequestID {
		return nil
	}
	m.pendingDiffRequestID = 0
	text := strings.TrimRight(msg.Text, "\r\n")
	switch {
	case msg.Err != nil:
		text = "Failed to compute diff: " + msg.Err.Error()
	case !msg.IsGitRepo:
		text = "`/diff` - not inside a git repository"
	case strings.TrimSpace(text) == "":
		text = "No changes detected."
	}
	m.notice = "Diff"
	m.ensureSize()
	m.overlay = chatwidget.NewTranscriptOverlayWithTitle(m.width, m.height, text, "D I F F")
	m.overlayTranscript = false
	if m.noAltScreen && !m.overlayAltScreen {
		m.overlayAltScreen = true
		return bubbletea.EnterAltScreen
	}
	return nil
}

func defaultGitDiffReader(cwd string) (string, bool, error) {
	return codextui.ReadGitDiff(strings.TrimSpace(cwd))
}

func (m *Model) applyPsCommand() {
	if m == nil {
		return
	}
	m.applyHistoryCell(historycell.NewUnifiedExecProcessesOutput(m.backgroundProcesses))
	m.notice = ""
}

func (m *Model) applyStopCommand() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	m.backgroundProcesses = nil
	m.State.AddHistoryLines([]string{"Stopping all background terminals."}, []string{"Stopping all background terminals."})
	m.notice = "Stopping all background terminals."
	m.refreshTranscript()
	if m.onStopBackgroundTerminals != nil {
		return m.onStopBackgroundTerminals()
	}
	return nil
}

func (m *Model) applyTranscriptNavigationKey(msg bubbletea.KeyMsg) bool {
	if m == nil {
		return false
	}
	switch msg.Type {
	case bubbletea.KeyPgUp:
		m.transcript.PageUp()
	case bubbletea.KeyPgDown:
		m.transcript.PageDown()
	case bubbletea.KeyHome, bubbletea.KeyCtrlHome:
		m.transcript.GotoTop()
	case bubbletea.KeyEnd, bubbletea.KeyCtrlEnd:
		m.transcript.GotoBottom()
	default:
		return false
	}
	return true
}

func (m *Model) appendAssistantDelta(itemID string, delta string) {
	if m == nil || delta == "" {
		return
	}
	m.insertFinalMessageSeparatorIfNeeded()
	itemID = strings.TrimSpace(itemID)
	if itemID != "" && m.activeAssistantDeltaItemID != "" && itemID != m.activeAssistantDeltaItemID {
		m.State.Messages = append(m.State.Messages, codextui.Message{Role: codextui.RoleAssistant, Text: delta})
		m.activeAssistantDeltaItemID = itemID
		return
	}
	m.State.Messages = appendAssistantDeltaToMessages(m.State.Messages, delta)
	if itemID != "" {
		m.activeAssistantDeltaItemID = itemID
	}
}

func (m *Model) mergeAssistantFinal(text string) {
	if m == nil {
		return
	}
	if strings.TrimSpace(text) != "" {
		m.insertFinalMessageSeparatorIfNeeded()
	}
	m.State.Messages = mergeAssistantFinalToMessages(m.State.Messages, text)
}

func (m *Model) insertFinalMessageSeparatorIfNeeded() {
	if m == nil || m.State == nil || !m.needsFinalMessageSeparator {
		return
	}
	if index := len(m.State.Messages) - 1; index >= 0 && m.State.Messages[index].Role == codextui.RoleAssistant {
		return
	}
	width := m.width
	if width < 20 {
		width = 20
	}
	cell := historycell.NewFinalMessageSeparator(nil, nil)
	m.State.AddHistoryLines(cell.DisplayLines(width), cell.RawLines())
	m.needsFinalMessageSeparator = false
}

func appendAssistantDeltaToMessages(messages []codextui.Message, delta string) []codextui.Message {
	if delta == "" {
		return messages
	}
	index := len(messages) - 1
	if index >= 0 && messages[index].Role == codextui.RoleAssistant {
		messages[index].Text += delta
		return messages
	}
	return append(messages, codextui.Message{Role: codextui.RoleAssistant, Text: delta})
}

func mergeAssistantFinalToMessages(messages []codextui.Message, text string) []codextui.Message {
	text = strings.TrimSpace(text)
	if text == "" {
		return messages
	}
	index := len(messages) - 1
	if index >= 0 && messages[index].Role == codextui.RoleAssistant {
		current := strings.TrimSpace(messages[index].Text)
		switch {
		case current == text:
			return messages
		case strings.Contains(text, current):
			messages[index].Text = text
			return messages
		}
	}
	if assistantFinalExistsInCurrentTurn(messages, text) {
		return messages
	}
	return append(messages, codextui.Message{Role: codextui.RoleAssistant, Text: text})
}

func assistantFinalExistsInCurrentTurn(messages []codextui.Message, text string) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		switch message.Role {
		case codextui.RoleUser:
			return false
		case codextui.RoleAssistant:
			if strings.TrimSpace(message.Text) == text {
				return true
			}
		}
	}
	return false
}

func (m *Model) addBottomLine(line string) {
	if m == nil {
		return
	}
	line = strings.TrimRight(strings.ReplaceAll(line, "\r", " "), " \t")
	if strings.TrimSpace(line) == "" {
		return
	}
	m.bottom = append(m.bottom, line)
	if len(m.bottom) > maxBottomLines {
		m.bottom = m.bottom[len(m.bottom)-maxBottomLines:]
	}
}

func (m *Model) addBottomLines(lines ...string) {
	if m == nil {
		return
	}
	for _, line := range lines {
		m.addBottomLine(line)
	}
}

func (m *Model) renderBottomPane() string {
	if m == nil {
		return ""
	}
	lines := []string{}
	if len(m.attachments) > 0 {
		lines = append(lines, m.renderAttachmentLine())
	}
	if len(m.queued) > 0 {
		lines = append(lines, fmt.Sprintf("Queued inputs: %d", len(m.queued)))
	}
	lines = append(lines, m.bottom...)
	return strings.Join(lines, "\n")
}

func waitForStream(messages <-chan bubbletea.Msg) bubbletea.Cmd {
	if messages == nil {
		return nil
	}
	return func() bubbletea.Msg {
		message, ok := <-messages
		return streamEnvelopeMsg{Message: message, Messages: messages, Done: !ok}
	}
}

func renderTranscript(state *codextui.State, raw bool, width int, themeID string) string {
	if state == nil || len(state.Messages) == 0 {
		return "No messages yet."
	}
	if raw {
		return renderRawTranscript(state)
	}
	if width < 20 {
		width = 20
	}
	var builder strings.Builder
	first := true
	for _, message := range state.Messages {
		lines := richMessageDisplayLines(message, width, themeID)
		if len(lines) == 0 {
			continue
		}
		if !first {
			builder.WriteString("\n\n")
		}
		builder.WriteString(strings.TrimRight(strings.Join(lines, "\n"), "\r\n"))
		first = false
	}
	if builder.Len() == 0 {
		return "No messages yet."
	}
	return builder.String()
}

func richMessageDisplayLines(message codextui.Message, width int, themeID string) []string {
	text := strings.TrimRight(message.Text, "\r\n")
	switch message.Role {
	case codextui.RoleHistory:
		return rawLinesTrimmed(text)
	case codextui.RoleUser:
		cell := historycell.NewUserPrompt(text, nil, nil, nil)
		return trimBlankDisplayEdges(cell.DisplayLines(width))
	case codextui.RoleAssistant:
		contentWidth := width - 2
		if contentWidth < 1 {
			contentWidth = 1
		}
		rendered, err := markdown.RenderWithTheme(text, contentWidth, themeID)
		if err == nil && strings.TrimSpace(rendered) != "" {
			lines := trimBlankDisplayEdges(rawLinesTrimmed(rendered))
			return prefixPrewrappedAgentLines(lines, true)
		}
		lines := rawLinesTrimmed(text)
		if len(lines) == 0 {
			return nil
		}
		cell := historycell.NewAgentMessageCell(lines, true)
		return trimBlankDisplayEdges(cell.DisplayLines(width))
	default:
		role := string(message.Role)
		if role == "" {
			role = string(codextui.RoleSystem)
		}
		return []string{roleTitle(role) + ":", indentLines(text, "  ")}
	}
}

func prefixPrewrappedAgentLines(lines []string, isFirstLine bool) []string {
	if len(lines) == 0 {
		return nil
	}
	out := make([]string, 0, len(lines))
	for index, line := range lines {
		prefix := "  "
		if index == 0 && isFirstLine {
			prefix = "\u2022 "
		}
		out = append(out, prefix+line)
	}
	return out
}

func rawLinesTrimmed(text string) []string {
	text = strings.TrimRight(text, "\r\n")
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func trimBlankDisplayEdges(lines []string) []string {
	start := 0
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	end := len(lines)
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return append([]string(nil), lines[start:end]...)
}

func renderRawTranscript(state *codextui.State) string {
	if state == nil || len(state.Messages) == 0 {
		return "No messages yet."
	}
	var builder strings.Builder
	for _, message := range state.Messages {
		text := strings.TrimRight(message.RawText, "\r\n")
		if strings.TrimSpace(text) == "" {
			text = strings.TrimRight(message.Text, "\r\n")
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteString("\n\n")
		}
		builder.WriteString(text)
	}
	if builder.Len() == 0 {
		return "No messages yet."
	}
	return builder.String()
}

func indentLines(value string, prefix string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return prefix
	}
	lines := strings.Split(value, "\n")
	for i, line := range lines {
		lines[i] = prefix + strings.TrimRight(line, " \t")
	}
	return strings.Join(lines, "\n")
}

func roleTitle(role string) string {
	role = strings.TrimSpace(role)
	if role == "" {
		return "System"
	}
	return strings.ToUpper(role[:1]) + role[1:]
}

func firstPositive(value int, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func displayValue(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
