package tea

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	sysclipboard "github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
	"github.com/google/uuid"

	appsapi "codex_go/apps"
	"codex_go/appserver"
	"codex_go/config"
	"codex_go/eventmap"
	"codex_go/filesearch"
	"codex_go/plugin"
	"codex_go/protocol"
	"codex_go/review"
	codextui "codex_go/tui"
	agentsoverview "codex_go/tui/agents_overview"
	"codex_go/tui/anim"
	bottompane "codex_go/tui/bottom_pane"
	mentionsv2 "codex_go/tui/bottom_pane/mentions_v2"
	chatwidget "codex_go/tui/chatwidget"
	execcell "codex_go/tui/exec_cell"
	historycell "codex_go/tui/history_cell"
	idecontext "codex_go/tui/ide_context"
	"codex_go/tui/markdown"
	"codex_go/tui/overlay"
	pets "codex_go/tui/pets"
	streamingpkg "codex_go/tui/streaming"
	"codex_go/tui/styles"
)

const (
	defaultWidth          = 80
	defaultHeight         = 24
	defaultComposerHeight = 3
	minTranscriptHeight   = 3
	maxBottomLines        = 6
	regionChromeMinWidth  = 100
	footerHelpText        = "Enter send | Ctrl+J newline | Ctrl+G editor | Ctrl+C quit | /help commands"
	mcpStartupFinishLag   = 4 * time.Second
)

// SubmitFunc lets the runtime layer attach prompt execution without coupling
// the terminal model to app or turn packages.
type SubmitFunc func(prompt string) bubbletea.Cmd

type SubmitRequest struct {
	Prompt                 string
	ServiceTier            string
	AdditionalInstructions string
	Attachments            []bottompane.ComposerAttachment
	MentionBindings        []string
	MentionCatalog         chatwidget.SubmissionMentionCatalog
	IDEContext             *idecontext.IdeContext
	CollaborationMode      *chatwidget.CollaborationMode
	// InternalInputItems are turn input items that reach the model but are not
	// rendered as a user message. Goal continuations use them to start work on
	// an active objective without a visible prompt (mirrors Rust's internal
	// goal steering input).
	InternalInputItems []any
	// LiteralInput marks queued input whose leading `!` was revealed only by
	// paste expansion. It must be submitted as literal model input with shell
	// escapes disabled (Rust #39604 QueuedInputAction::Literal).
	LiteralInput bool
}

type SubmitRequestFunc func(request SubmitRequest) bubbletea.Cmd

type SteerRequestFunc func(request SubmitRequest, clientID string) error

type InterruptFunc func() bubbletea.Cmd

type ExternalEditorFunc func(seed string) bubbletea.Cmd

// ExternalEditorDirectoryFunc resolves a protected directory for external
// editor buffer files at editor-open time (Rust #38830). The directory must
// not be writable under the session's filesystem policy.
type ExternalEditorDirectoryFunc func(cwd string) (string, error)

type KeymapEditFunc func(edit codextui.KeymapEdit) (*codextui.KeymapConfig, string, error)

type ExternalEditorFinishedMsg struct {
	Text string
	Err  error
}

type queuedSubmission struct {
	Request      SubmitRequest
	ParseCommand bool
	Literal      bool
}

type pendingSteerSubmission struct {
	ID      string
	Request SubmitRequest
}

type SessionActionFunc func(selection codextui.SessionSelection) (*codextui.SessionSummary, error)

// WorkingDirectoryChangeFunc changes the active local session's working
// directory, preserving conversation history (Rust #38894 /cd). The
// implementation forks the current thread at the new cwd (or starts a fresh
// thread when there is no resumable rollout), archives the old thread, and
// returns the replacement session summary for the model to attach.
type WorkingDirectoryChangeFunc func(threadID string, cwd string) (*codextui.SessionSummary, error)

type SessionResumeFunc func(selection codextui.SessionSelection) (SessionResumeResponse, error)

type ThreadRenameFunc func(threadID string, name string) error

type LogoutFunc func() error

type SessionResumeResponse struct {
	Summary    *codextui.SessionSummary
	Messages   []codextui.Message
	Status     string
	TokenUsage *protocol.ThreadTokenUsage
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

type RateLimitsReaderFunc func() ([]codextui.RateLimitStatus, error)

type TerminalTitleWriterFunc func(sequence string) bubbletea.Cmd

type NotificationPostFunc func(message string, method codextui.NotificationMethod) bubbletea.Cmd

type GitDiffReaderFunc func(cwd string) (string, bool, error)

type StopBackgroundTerminalsFunc func() bubbletea.Cmd

type DebugConfigReaderFunc func() ([]string, error)

type GoalReaderFunc func(threadID string) (*appserver.Goal, error)

type GoalSetterFunc func(threadID string, objective *string, tokenBudget *int64, status *appserver.GoalStatus) (appserver.Goal, error)

type GoalClearerFunc func(threadID string) (bool, error)

// GoalEditTextFunc resolves the text shown in the /goal edit prompt from the
// stored objective, loading materialized goal objective files when the stored
// objective is a managed file reference (mirrors Rust objective_text_for_edit).
type GoalEditTextFunc func(threadID string, objective string) (string, error)

// GoalDraftMaterializeFunc materializes a goal draft (images, remote URLs,
// oversized objective) into app-server files and returns the objective to
// persist, mirroring Rust goal_files::materialize_goal_draft.
type GoalDraftMaterializeFunc func(draft codextui.GoalDraft) (string, error)

// GoalContinuationFunc starts an automatic continuation turn toward an active
// goal, mirroring the Rust app-server's goal runtime effects after /goal set
// or resume. The TUI supplies the internal continuation input so the model
// starts working without a visible user message.
type GoalContinuationFunc func(goal appserver.Goal) bubbletea.Cmd

type SettingsEdit struct {
	KeyPath string
	Value   any
}

type SettingsWriteResult struct {
	FeatureSettings         map[string]bool
	UseMemories             *bool
	GenerateMemories        *bool
	FeedbackEnabled         *bool
	DisablePasteBurst       bool
	Personality             chatwidget.Personality
	Notifications           *chatwidget.NotificationsSetting
	NotificationMethod      codextui.NotificationMethod
	NotificationCondition   codextui.NotificationCondition
	PermissionRequirements  *chatwidget.PermissionRequirements
	HideRateLimitModelNudge *bool
	TUITheme                string
	TUIPet                  string
	SessionPickerView       string
	PluginUserMarketplaces  map[string]bool
	PluginGitMarketplaces   map[string]bool
	FilePath                string
}

type SettingsWriteFunc func(edits []SettingsEdit) (SettingsWriteResult, error)

type CollaborationModeUpdateFunc func(threadID string, mode chatwidget.CollaborationMode) error

type MemorySettingsWriteFunc func(threadID string, useMemories bool, generateMemories bool, generateChanged bool) (SettingsWriteResult, error)

type MemoryResetFunc func() error

type FeedbackSubmitFunc func(params appserver.FeedbackUploadParams) (appserver.FeedbackUploadResponse, error)

type IDEContextReaderFunc func(cwd string) (*idecontext.IdeContext, error)

type AutoReviewDenialApproveFunc func(threadID string, entry chatwidget.AutoReviewDenialEntry) error

type WindowsSandboxSetupFunc func(mode chatwidget.WindowsSandboxMode, cwd string) (WindowsSandboxSetupOutcome, error)
type DesktopThreadOpenFunc func(threadID string) error
type SandboxReadDirFunc func(path string) (canonicalPath string, err error)
type ExternalAgentDetectFunc func(cwd string, migrationSource string) (config.ExternalAgentConfigDetectResponse, error)
type ExternalAgentImportCompletion struct {
	Completed config.ExternalAgentConfigImportCompletedNotification
	Err       error
}
type ExternalAgentImportFunc func(items []config.ExternalAgentConfigMigrationItem, migrationSource string) (config.ExternalAgentConfigImportResponse, <-chan ExternalAgentImportCompletion, error)
type RolloutPathReaderFunc func(threadID string) (string, error)

type HooksListReaderFunc func(cwd string) (appserver.HookListResponse, error)

type HookConfigWriteFunc func(params config.ConfigBatchWriteParams) error

type PluginListReaderFunc func(cwd string, forceRefetch bool) (plugin.PluginListResponse, error)

type PluginReadFunc func(params plugin.PluginReadParams) (plugin.PluginReadResponse, error)

type PluginInstallFunc func(params plugin.PluginInstallParams) (plugin.PluginInstallResponse, error)

type PluginUninstallFunc func(params plugin.PluginUninstallParams) (plugin.PluginUninstallResponse, error)

type PluginEnabledWriteFunc func(pluginID string, enabled bool) error

type MarketplaceAddFunc func(params plugin.MarketplaceAddParams) (plugin.MarketplaceAddResponse, error)

type MarketplaceRemoveFunc func(params plugin.MarketplaceRemoveParams) (plugin.MarketplaceRemoveResponse, error)

type MarketplaceUpgradeFunc func(params plugin.MarketplaceUpgradeParams) (plugin.MarketplaceUpgradeResponse, error)

type PluginOpenURLFunc func(target string) error

type SkillsListReaderFunc func(cwd string, forceReload bool) (appserver.SkillsListResponse, error)

type SkillEnabledWriteFunc func(path string, enabled bool) (effectiveEnabled bool, err error)

type FuzzyFileSearchReaderFunc func(query string, cwd string, cancellationToken string) (appserver.FuzzyFileSearchResponse, error)

type AppListReaderFunc func(threadID string, forceRefetch bool) (appsapi.AppListResponse, error)

type ReviewStartFunc func(params review.StartParams) (review.StartResponse, error)

type ReviewStartCommandFunc func(params review.StartParams) bubbletea.Cmd

type CompactStartCommandFunc func(threadID string) bubbletea.Cmd

type ReviewBranchesReaderFunc func(cwd string) (currentBranch string, branches []string, err error)

type ReviewCommitsReaderFunc func(cwd string, limit int) ([]chatwidget.ReviewCommitEntry, error)

type StatusMsg struct {
	Status string
}

// ModelRetryStatusMsg is a transient model transport status. Unlike warnings,
// it is an Activity row and must not be persisted in conversation history.
type ModelRetryStatusMsg struct {
	Message string
	Active  bool
}

type ModelCompactionStatusMsg struct {
	Message string
	Active  bool
}

type CompactStartResultMsg struct {
	Err error
}

type TurnCompletedMsg struct {
	ThreadID         string
	AssistantMessage string
	Err              error
}

type TurnInterruptedMsg struct {
	ThreadID string
	Err      error
}

type reviewTokenSnapshot struct {
	Total         codextui.TokenUsage
	Last          codextui.TokenUsage
	ContextWindow *int64
}

type MCPStartupUpdateMsg struct {
	Name   string
	Status chatwidget.McpStartupStatus
}

type MCPStartupInventoryMsg struct {
	Servers []historycell.McpServerStatus
}

type MCPInventoryResultMsg struct {
	RequestID uint64
	Servers   []historycell.McpServerStatus
	Err       error
}

// ModelsResultMsg is delivered when an async model-list refresh completes.
type ModelsResultMsg struct {
	RequestID uint64
	Options   []codextui.ModelPickerOption
	Err       error
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

type RateLimitsResultMsg struct {
	RequestID uint64
	Limits    []codextui.RateLimitStatus
	Err       error
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
	Objective string
	Goal      *appserver.Goal
	Cleared   bool
	Replacing bool
	Err       error
}

type GoalEditTextMsg struct {
	ThreadID  string
	Objective string
	Goal      appserver.Goal
	Err       error
}

type GoalDraftMaterializeMsg struct {
	RequestID   uint64
	Action      string
	ThreadID    string
	Objective   string
	TokenBudget *int64
	Status      *appserver.GoalStatus
	Replacing   bool
	Err         error
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

type SandboxReadDirResultMsg struct {
	RequestedPath string
	CanonicalPath string
	Err           error
}

type MemoryResetResultMsg struct {
	Err error
}

type FeedbackSubmitResultMsg struct {
	Category    feedbackCategory
	IncludeLogs bool
	Response    appserver.FeedbackUploadResponse
	Err         error
}

type GuardianReviewMsg struct {
	ThreadID string
	Event    chatwidget.GuardianAssessmentEvent
}

type AutoReviewDenialApproveResultMsg struct {
	Entry chatwidget.AutoReviewDenialEntry
	Err   error
}

type WindowsSandboxSetupCompletedMsg struct {
	Completion WindowsSandboxSetupCompletion
}

type HooksListResultMsg struct {
	CWD      string
	Response appserver.HookListResponse
	Err      error
}

type HookConfigWriteResultMsg struct {
	RequestID   uint64
	ErrorPrefix string
	Err         error
}

type PluginListResultMsg struct {
	CWD      string
	Response plugin.PluginListResponse
	Err      error
}

type MentionPluginInventoryResultMsg struct {
	Response plugin.PluginListResponse
	Err      error
}

type MentionFileSearchResultMsg struct {
	Generation uint64
	Query      string
	Matches    []filesearch.FileMatch
	Err        error
}

type AppListResultMsg struct {
	ThreadID        string
	ScopeGeneration uint64
	ForceRefetch    bool
	Response        appsapi.AppListResponse
	Err             error
}

type AgentListResultMsg struct {
	CurrentThreadID string
	RequestID       uint64
	Entries         []codextui.AgentThreadEntry
	Err             error
}

type AgentSwitchResultMsg struct {
	ThreadID   string
	Response   AgentThreadSwitchResponse
	Err        error
	closedSide *activeSideConversation
}

type AgentNavigateResultMsg struct {
	CurrentThreadID string
	Entries         []codextui.AgentThreadEntry
	Direction       int
	Err             error
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

type SkillEnabledWriteResultMsg struct {
	RequestID uint64
	Path      string
	Enabled   bool
	Err       error
}

type ExternalAgentDetectResultMsg struct {
	Results []ExternalAgentSourceDetectResult
}

type ExternalAgentSourceDetectResult struct {
	MigrationSource string
	Label           string
	Response        config.ExternalAgentConfigDetectResponse
	Err             error
}

type ExternalAgentImportResultMsg struct {
	Selected   []config.ExternalAgentConfigMigrationItem
	Source     string
	Response   config.ExternalAgentConfigImportResponse
	Completion <-chan ExternalAgentImportCompletion
	Err        error
}

type ExternalAgentImportCompletedResultMsg struct {
	ImportID string
	Result   ExternalAgentImportCompletion
}

type SkillsInventoryResultMsg struct {
	CWD      string
	Response appserver.SkillsListResponse
	Err      error
}

type LogoutResultMsg struct {
	Err error
}

type StreamStartedMsg struct {
	Messages <-chan bubbletea.Msg
}

type SteerResultMsg struct {
	ClientID string
	Err      error
}

type SteerCommittedMsg struct {
	Count int
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
	ServiceTierCommands           []bottompane.ServiceTierCommand
	SessionPickerItems            []codextui.SessionSummary
	SessionPickerCWD              string
	SessionPickerView             string
	ShowSessionHeader             bool
	SessionHeaderVersion          string
	OnSubmit                      SubmitFunc
	OnSubmitRequest               SubmitRequestFunc
	OnSteerRequest                SteerRequestFunc
	OnInterrupt                   InterruptFunc
	OnInterruptMCPStartup         InterruptFunc
	OnExternalEditor              ExternalEditorFunc
	OnExternalEditorDirectory     ExternalEditorDirectoryFunc
	KeymapConfig                  *codextui.KeymapConfig
	OnKeymapEdit                  KeymapEditFunc
	OnModalResponse               ModalResponseFunc
	OnSessionAction               SessionActionFunc
	OnWorkingDirectoryChange      WorkingDirectoryChangeFunc
	OnResumeSession               SessionResumeFunc
	OnRenameThread                ThreadRenameFunc
	OnLogout                      LogoutFunc
	OnReadAgents                  AgentThreadReaderFunc
	OnSwitchAgent                 AgentThreadSwitchFunc
	AgentsOverviewEmbedded        bool
	OnAgentsOverviewRefresh       AgentsOverviewRefreshFunc
	OnAgentsOverviewDispatch      AgentsOverviewDispatchFunc
	OnAgentsOverviewStop          AgentsOverviewStopFunc
	OnAgentsOverviewRename        AgentsOverviewRenameFunc
	OnStartAgentsDaemon           AgentsDaemonStartFunc
	OnClipboardWrite              func(text string) error
	OnReadTokenActivity           TokenActivityReaderFunc
	OnReadRateLimitResetCredits   RateLimitResetCreditsReaderFunc
	OnConsumeRateLimitResetCredit RateLimitResetCreditConsumerFunc
	OnReadRateLimits              RateLimitsReaderFunc
	OnWriteTerminalTitle          TerminalTitleWriterFunc
	OnPostNotification            NotificationPostFunc
	OnReadGitDiff                 GitDiffReaderFunc
	OnStopBackgroundTerminals     StopBackgroundTerminalsFunc
	LocalDaemonSession            bool
	OnReadDebugConfig             DebugConfigReaderFunc
	OnReadGoal                    GoalReaderFunc
	OnSetGoal                     GoalSetterFunc
	OnClearGoal                   GoalClearerFunc
	OnGoalEditText                GoalEditTextFunc
	OnGoalDraftMaterialize        GoalDraftMaterializeFunc
	OnGoalContinuation            GoalContinuationFunc
	OnWriteSettings               SettingsWriteFunc
	OnUpdateCollaborationMode     CollaborationModeUpdateFunc
	OnWriteMemorySettings         MemorySettingsWriteFunc
	OnResetMemories               MemoryResetFunc
	OnSubmitFeedback              FeedbackSubmitFunc
	OnReadIDEContext              IDEContextReaderFunc
	OnApproveAutoReviewDenial     AutoReviewDenialApproveFunc
	OnStartWindowsSandboxSetup    WindowsSandboxSetupFunc
	WindowsSandboxStartupPrompt   *WindowsSandboxStartupPrompt
	OnOpenDesktopThread           DesktopThreadOpenFunc
	OnSandboxReadDir              SandboxReadDirFunc
	OnDetectExternalAgent         ExternalAgentDetectFunc
	OnImportExternalAgent         ExternalAgentImportFunc
	OnReadRolloutPath             RolloutPathReaderFunc
	OnReadHooks                   HooksListReaderFunc
	OnWriteHookConfig             HookConfigWriteFunc
	OnReadPlugins                 PluginListReaderFunc
	OnReadPlugin                  PluginReadFunc
	OnInstallPlugin               PluginInstallFunc
	OnUninstallPlugin             PluginUninstallFunc
	OnWritePluginEnabled          PluginEnabledWriteFunc
	OnAddMarketplace              MarketplaceAddFunc
	OnRemoveMarketplace           MarketplaceRemoveFunc
	OnUpgradeMarketplace          MarketplaceUpgradeFunc
	OnOpenPluginURL               PluginOpenURLFunc
	PluginUserMarketplaces        map[string]bool
	PluginGitMarketplaces         map[string]bool
	OnReadSkills                  SkillsListReaderFunc
	OnWriteSkillEnabled           SkillEnabledWriteFunc
	OnFuzzyFileSearch             FuzzyFileSearchReaderFunc
	OnReadApps                    AppListReaderFunc
	OnStartReview                 ReviewStartFunc
	OnStartReviewCommand          ReviewStartCommandFunc
	OnStartCompactCommand         CompactStartCommandFunc
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
	OnReadMCPInventory             func(detail bool) ([]historycell.McpServerStatus, error)
	OnListModels                   func(includeHidden bool) ([]codextui.ModelPickerOption, error)
	MCPStartupExpectedServers      []string
	InitialMessages                <-chan bubbletea.Msg
	InitialHistoryCells            []historycell.HistoryCell
	FeatureSettings                map[string]bool
	UseMemories                    *bool
	GenerateMemories               *bool
	FeedbackEnabled                *bool
	DisablePasteBurst              bool
	Personality                    chatwidget.Personality
	HideRateLimitModelNudge        *bool
	TUITheme                       string
	TUIPet                         string
	CodexHome                      string
	PetEnv                         map[string]string
	PetFetch                       pets.AssetFetchFunc
	TUIThemeStyles                 *styles.Styles
}

type Model struct {
	State *codextui.State

	// Styles is the centralized theme configuration.
	Styles styles.Styles

	// Sub-components (new architecture — being incrementally adopted).
	Transcript TranscriptComponent
	Composer   ComposerComponent
	StatusBar  StatusBarComponent

	// Animation
	animEngine *anim.Engine

	// Overlay stack (new architecture)
	overlays *overlay.Overlay

	transcript                 viewport.Model
	composer                   textarea.Model
	activityFollow             bool
	overlay                    *chatwidget.TranscriptOverlay
	slashPopup                 slashCommandPopup
	agentsOverview             *agentsoverview.View
	agentsOverviewNotice       string
	agentsOverviewBusy         bool
	agentsOverviewRefresh      int
	agentsOverviewPending      bool
	agentsOverviewInflight     bool
	agentsOverviewDrafts       map[string]string
	agentsOverviewPendingDraft *string
	transcriptMessages         transcriptMessageCache
	overlayMessages            transcriptMessageCache
	lastTranscriptContent      string
	lastTranscriptHeight       int

	width                  int
	height                 int
	noAltScreen            bool
	overlayAltScreen       bool
	sessionPickerAltScreen bool
	overlayTranscript      bool
	rateLimitWarnings      chatwidget.RateLimitWarningState
	warningDisplay         chatwidget.WarningDisplayState

	terminalFocused          bool
	rawOutput                bool
	rateLimitSwitchPrompt    chatwidget.RateLimitSwitchPromptState
	hideRateLimitModelNudge  bool
	rateLimitSwitchModel     string
	rateLimitSwitchReasoning string

	statusStyle lipgloss.Style
	footerStyle lipgloss.Style
	bottomStyle lipgloss.Style

	lastTurnError              string
	needsFinalMessageSeparator bool
	activeAssistantDeltaItemID string
	// compactCommandGroup tracks consecutive successful Agent / unified-exec
	// startup command executions so they render as one compact "Ran N commands"
	// history cell (Rust #38921). It is reset at interaction boundaries and
	// whenever a non-groupable or failed command breaks the run.
	compactCommandGroup             *compactCommandGroupState
	mcpStartup                      chatwidget.McpStartupRoundState
	mcpStartupHeader                string
	mcpStartupActive                bool
	mcpStartupGeneration            uint64
	mcpStartupFinishPending         bool
	initialMessages                 <-chan bubbletea.Msg
	notice                          string
	retryMessageIndex               int
	retryActivityMessage            string
	retryActivityActive             bool
	compactionActive                bool
	compactionID                    string
	compactionStartedAt             time.Time
	bottom                          []string
	attachments                     []bottompane.ComposerAttachment
	composerMentionBindings         []string
	misalignmentPolicyStopped       bool
	modal                           *modalState
	skillPopup                      skillPopupState
	mentionPopup                    *mentionsv2.Popup
	mentionDismissedToken           string
	mentionFileSearchGeneration     uint64
	mentionPluginInventory          []plugin.PluginSummary
	mentionPluginInventoryReady     bool
	mentionPluginInventoryLoading   bool
	mentionPluginInventoryErr       string
	modelPickerOpts                 []codextui.ModelPickerOption
	serviceTierCommands             []bottompane.ServiceTierCommand
	sessionItems                    []codextui.SessionSummary
	sessionCWD                      string
	sessionPickerDensity            codextui.SessionListDensity
	skillsInventory                 *appserver.SkillsListResponse
	skillsInventoryCWD              string
	skillsInventoryErr              string
	skillsInventoryLoading          bool
	agentItems                      []codextui.AgentThreadEntry
	activeAgentLabel                string
	nextAgentRefreshRequestID       uint64
	pendingAgentRefreshRequestID    uint64
	pendingAgentRefreshThreadID     string
	backgroundProcesses             []historycell.UnifiedExecProcessDetails
	mcpServers                      []historycell.McpServerStatus
	onReadMCPInventory              func(detail bool) ([]historycell.McpServerStatus, error)
	onListModels                    func(includeHidden bool) ([]codextui.ModelPickerOption, error)
	nextModelsRequestID             uint64
	pendingModelsRequestID          uint64
	nextMCPInventoryRequestID       uint64
	pendingMCPInventoryRequestID    uint64
	pendingMCPInventoryMessageIndex int
	pendingMCPInventoryDetail       bool
	featureSettings                 map[string]bool
	disablePasteBurst               bool
	personality                     chatwidget.Personality
	tuiTheme                        string
	tuiPet                          string
	vimMode                         bool
	// vimInsert is the Vim mode state when vimMode is enabled: false = normal
	// mode (keys dispatch vim_normal actions), true = insert mode (keys type
	// normally until Esc). Rust starts the composer in Vim normal mode when
	// /vim is enabled (bottom_pane/chat_composer.rs).
	vimInsert bool
	// vimYank is the line yank buffer for Vim normal mode (Y yanks the current
	// line, p pastes it after the cursor).
	vimYank string
	// composerKillBuffer is the single-entry editor kill buffer for the
	// composer (ctrl-k / ctrl-u / kill_whole_line cut into it; ctrl-y yanks it
	// back), mirroring Rust bottom_pane/textarea.rs kill_buffer.
	composerKillBuffer string
	// vimPendingOp tracks a pending Vim line operator (d or y) waiting for its
	// repeat key, enabling dd / yy / cc.
	vimPendingOp string
	// vimPendingObject tracks a pending Vim text-object selection after an
	// operator: "inner" (i) or "around" (a), waiting for the object key
	// (w / W / ( / ) / b).
	vimPendingObject string
	// vimPendingReplace tracks the Vim `r` replacement: the next typed
	// character replaces the grapheme under the cursor without leaving normal
	// mode (Rust #39661 vim_normal.replace_char).
	vimPendingReplace bool
	// vimReplaceMode tracks Vim replace mode (R): typed characters overwrite
	// the draft under the cursor until Esc returns to normal mode (Rust #42194).
	vimReplaceMode bool
	// vimPendingFind tracks a pending Vim find/till motion (f/F/t/T): the next
	// typed character is the search target. vimFindKind: 0=find, 1=till;
	// vimFindForward is true for f/t; vimFindOperator carries a pending d/y/c
	// when the find is an operator motion ("" for plain navigation).
	vimPendingFind  bool
	vimFindKind     int
	vimFindForward  bool
	vimFindOperator string
	// vimPendingG tracks the first key of the `gg` buffer-top jump chord
	// (mirrors Rust vim_commands.rs jump_top). Once pending, the next `g`
	// completes the chord and jumps to the top buffer line; any other key
	// cancels the chord and is dispatched normally.
	vimPendingG bool
	// vimSearchMode tracks active Vim composer search query entry (Rust #41586
	// bottom_pane/textarea/vim_search.rs). While active, typed runes accumulate
	// into vimSearchQuery; Enter performs the search and Esc cancels it.
	vimSearchMode bool
	// vimSearchQuery is the query being entered (or the last executed query).
	vimSearchQuery string
	// vimSearchForward is true for a forward search (/), false for backward (?).
	vimSearchForward bool
	// vimSearchMatches caches the byte offsets of the current query's matches.
	vimSearchMatches []vimSearchRange
	// vimSearchIndex is the current match index into vimSearchMatches.
	vimSearchIndex int
	// vimSearchOp is the pending d/y/c operator carried into a search so the
	// accepted search applies it over the cursor->match range (Rust #41586
	// bottom_pane/textarea/vim_search.rs operator transaction). Empty when the
	// search is a plain motion.
	vimSearchOp string
	// vimHasLastDelete and vimLastDeleteWord record the last completed Vim
	// delete for dot-repeat (`.`): vimLastDeleteWord mirrors the Rust
	// VimCommandState::last_change word-delete edit (#40521).
	vimHasLastDelete         bool
	vimLastDeleteWord        bool
	petRuntime               *petRuntime
	petCodexHome             string
	petEnv                   map[string]string
	petFetch                 pets.AssetFetchFunc
	petLoadPending           string
	onSubmit                 SubmitFunc
	onSubmitRequest          SubmitRequestFunc
	onSteerRequest           SteerRequestFunc
	onInterrupt              InterruptFunc
	onInterruptMCPStartup    InterruptFunc
	localDaemonSession       bool
	agentsOverviewEmbedded   bool
	onAgentsOverviewRefresh  AgentsOverviewRefreshFunc
	onAgentsOverviewDispatch AgentsOverviewDispatchFunc
	onAgentsOverviewStop     AgentsOverviewStopFunc
	onAgentsOverviewRename   AgentsOverviewRenameFunc
	onStartAgentsDaemon      AgentsDaemonStartFunc
	onExternalEditor         ExternalEditorFunc
	externalEditorDirectory  ExternalEditorDirectoryFunc
	keymapConfig             *codextui.KeymapConfig
	keymapSelectedContext    string
	keymapSelectedAction     string
	copyTargets              []chatwidget.CopyTarget
	onKeymapEdit             KeymapEditFunc
	onModalResponse          ModalResponseFunc
	onSessionAction          SessionActionFunc
	onWorkingDirectoryChange WorkingDirectoryChangeFunc
	onResumeSession          SessionResumeFunc
	onRenameThread           ThreadRenameFunc
	onLogout                 LogoutFunc
	onReadAgents             AgentThreadReaderFunc
	onSwitchAgent            AgentThreadSwitchFunc
	// backgroundThreadEvents buffers app-server notifications for non-active
	// (subagent) threads so switching to them can replay in-progress activity
	// instead of showing an empty transcript (Rust parity: ThreadEventStore).
	backgroundThreadEvents            map[string][]protocol.ThreadEvent
	clipboardWrite                    func(text string) error
	onReadTokenActivity               TokenActivityReaderFunc
	onReadRateLimitResetCredits       RateLimitResetCreditsReaderFunc
	onConsumeRateLimitResetCredit     RateLimitResetCreditConsumerFunc
	onReadRateLimits                  RateLimitsReaderFunc
	nextStatusRateLimitRequestID      uint64
	pendingStatusRateLimitRequests    map[uint64]pendingStatusRateLimitRequest
	terminalTitleWriter               TerminalTitleWriterFunc
	notificationPost                  NotificationPostFunc
	notifications                     chatwidget.NotificationState
	notificationSettings              chatwidget.NotificationsSetting
	notificationMethod                codextui.NotificationMethod
	notificationCondition             codextui.NotificationCondition
	onReadGitDiff                     GitDiffReaderFunc
	onStopBackgroundTerminals         StopBackgroundTerminalsFunc
	onReadDebugConfig                 DebugConfigReaderFunc
	onReadGoal                        GoalReaderFunc
	onSetGoal                         GoalSetterFunc
	onClearGoal                       GoalClearerFunc
	onGoalEditText                    GoalEditTextFunc
	onGoalDraftMaterialize            GoalDraftMaterializeFunc
	onGoalContinuation                GoalContinuationFunc
	pendingGoalDraft                  *codextui.GoalDraft
	onWriteSettings                   SettingsWriteFunc
	onUpdateCollaborationMode         CollaborationModeUpdateFunc
	onWriteMemorySettings             MemorySettingsWriteFunc
	onResetMemories                   MemoryResetFunc
	onSubmitFeedback                  FeedbackSubmitFunc
	onReadIDEContext                  IDEContextReaderFunc
	ideContext                        chatwidget.IdeContextState
	onApproveAutoReviewDenial         AutoReviewDenialApproveFunc
	toolRequestRuntime                chatwidget.ToolRequestRuntimeState
	useMemories                       bool
	generateMemories                  bool
	feedbackEnabled                   bool
	windowsSandboxSetup               WindowsSandboxSetupFunc
	windowsSandboxSetupChoiceRequired bool
	onOpenDesktopThread               DesktopThreadOpenFunc
	onSandboxReadDir                  SandboxReadDirFunc
	appsScopeGeneration               uint64
	onDetectExternalAgent             ExternalAgentDetectFunc
	onImportExternalAgent             ExternalAgentImportFunc
	pendingExternalAgentImports       map[string]bool
	onReadRolloutPath                 RolloutPathReaderFunc
	windowsSandboxSetupActive         bool
	windowsSandboxSetupStatus         chatwidget.WindowsSandboxSetupStatus
	onReadHooks                       HooksListReaderFunc
	onWriteHookConfig                 HookConfigWriteFunc
	hookWriteQueue                    []hookConfigWriteOperation
	hookWriteActive                   bool
	nextHookWriteRequestID            uint64
	hookLifecycle                     chatwidget.HookLifecycleState
	onReadPlugins                     PluginListReaderFunc
	onReadPlugin                      PluginReadFunc
	onInstallPlugin                   PluginInstallFunc
	onUninstallPlugin                 PluginUninstallFunc
	onWritePluginEnabled              PluginEnabledWriteFunc
	onAddMarketplace                  MarketplaceAddFunc
	onRemoveMarketplace               MarketplaceRemoveFunc
	onUpgradeMarketplace              MarketplaceUpgradeFunc
	onOpenPluginURL                   PluginOpenURLFunc
	pluginUserMarketplaces            map[string]bool
	pluginGitMarketplaces             map[string]bool
	pluginRuntime                     chatwidget.PluginsRuntimeState
	pluginToggleDesired               map[string]bool
	pluginToggleActive                map[string]bool
	onReadSkills                      SkillsListReaderFunc
	onWriteSkillEnabled               SkillEnabledWriteFunc
	nextSkillWriteRequestID           uint64
	onFuzzyFileSearch                 FuzzyFileSearchReaderFunc
	onReadApps                        AppListReaderFunc
	onStartReview                     ReviewStartFunc
	onStartReviewCommand              ReviewStartCommandFunc
	onStartCompactCommand             CompactStartCommandFunc
	reviewState                       chatwidget.ReviewState
	reviewTurnID                      string
	reviewTokenSnapshot               *reviewTokenSnapshot
	onStartSide                       SideStartFunc
	onCloseSide                       SideCloseFunc
	onReadReviewBranches              ReviewBranchesReaderFunc
	onReadReviewCommits               ReviewCommitsReaderFunc
	activeSide                        *activeSideConversation
	abandonedSideThreads              map[string]struct{}
	sideStartPending                  bool
	statusControls                    *chatwidget.StatusControlsState
	statusLineConfiguredByUser        bool
	lastTerminalTitleSequence         string
	rateLimitSnapshots                map[string]chatwidget.RateLimitSnapshot
	approvalsReviewer                 chatwidget.ApprovalsReviewer
	permissionRequirements            chatwidget.PermissionRequirements
	permissionItems                   []chatwidget.PermissionMenuItem
	pendingPermissionItem             *chatwidget.PermissionMenuItem
	hideFullAccessWarning             bool
	experimentalItems                 []chatwidget.ExperimentalFeatureOption
	currentGoal                       *appserver.Goal
	goalObservedAt                    time.Time
	pendingGoalObjective              string
	hasChatGPTAccount                 bool
	chatGPTPlanType                   string
	availableRateLimitResetCredits    *int64
	nextUsageRequestID                uint64
	pendingTokenActivityRequestID     uint64
	pendingRateLimitResetRequestID    uint64
	pendingRateLimitResetForPopup     bool
	pendingRateLimitResetPostConsume  bool
	nextDiffRequestID                 uint64
	pendingDiffRequestID              uint64
	nextGoalRequestID                 uint64
	pendingGoalRequestID              uint64
	nextSettingsRequestID             uint64
	pendingSettingsRequestID          uint64
	submitted                         []string
	inputHistory                      []string
	inputHistoryIndex                 int
	inputHistoryDraft                 string
	inputHistoryActive                bool
	submitRequests                    []SubmitRequest
	queued                            []queuedSubmission
	pendingSteers                     []pendingSteerSubmission
	rejectedSteers                    []queuedSubmission
	editorActive                      bool
	toolCalls                         map[string]*toolCallDisplayState
	mcpToolCalls                      map[string]*mcpToolCallDisplayState
	webSearches                       map[string]*webSearchDisplayState
	renderedFileChanges               map[string]bool
	activeProposedPlans               map[string]*proposedPlanDisplayState
	startedThreadIDs                  map[string]bool
	completedThreadIDs                map[string]bool
	pendingThreadName                 bool
	taskStartedAt                     time.Time

	composerPasteEnterUntil *time.Time
	now                     func() time.Time
}

// transcriptMessageKey identifies the render inputs for a single transcript
// message. Two messages with the same key always render to the same display
// lines, so an unchanged message can reuse its cached lines instead of being
// re-wrapped and re-rendered on every frame.
type transcriptMessageKey struct {
	role     codextui.MessageRole
	text     string
	rawText  string
	width    int
	themeID  string
	raw      bool
	expanded bool
}

type transcriptMessageCacheEntry struct {
	key   transcriptMessageKey
	lines []string
}

// transcriptMessageCache stores per-message display lines indexed by message
// position. It is invalidated lazily by comparing the message key: a message
// whose content or render inputs changed is re-rendered, while unchanged
// messages reuse their cached lines. This keeps the per-frame cost of a
// streaming turn proportional to the changed message instead of the whole
// history (Rust parity: committed history cells cache their display lines).
type transcriptMessageCache struct {
	messages []transcriptMessageCacheEntry
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

// compactCommandGroupState is the live accumulation state for a compact
// "Ran N commands" history cell. The cell is re-rendered at MessageIndex as
// commands start and complete (Rust #38921 chatwidget command lifecycle).
type compactCommandGroupState struct {
	MessageIndex int
	Cell         execcell.ExecCell
}

type mcpToolCallDisplayState struct {
	ID           string
	Invocation   historycell.McpInvocation
	MessageIndex int
}

type webSearchDisplayState struct {
	ID           string
	MessageIndex int
}

type proposedPlanDisplayState struct {
	Text         strings.Builder
	MessageIndex int
}

func NewModel(state *codextui.State, options Options) *Model {
	if state == nil {
		state = codextui.NewState(nil)
	}
	composer := textarea.New()
	composer.Prompt = "> "
	composer.Placeholder = firstNonEmpty(options.Placeholder, "Ask gcode")
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
		State:                           state,
		Styles:                          resolveStyles(options.TUIThemeStyles),
		Transcript:                      newTranscriptComponent(),
		Composer:                        newComposerComponent(options.Placeholder),
		StatusBar:                       newStatusBarComponent(),
		transcript:                      transcript,
		activityFollow:                  true,
		retryMessageIndex:               -1,
		composer:                        composer,
		noAltScreen:                     options.NoAltScreen,
		terminalFocused:                 true,
		statusStyle:                     lipgloss.NewStyle().Bold(true),
		footerStyle:                     lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
		bottomStyle:                     lipgloss.NewStyle(),
		modelPickerOpts:                 append([]codextui.ModelPickerOption(nil), options.ModelPickerOptions...),
		serviceTierCommands:             append([]bottompane.ServiceTierCommand(nil), options.ServiceTierCommands...),
		sessionItems:                    append([]codextui.SessionSummary(nil), options.SessionPickerItems...),
		sessionCWD:                      strings.TrimSpace(options.SessionPickerCWD),
		sessionPickerDensity:            normalizeSessionPickerDensityTea(options.SessionPickerView),
		backgroundProcesses:             cloneUnifiedExecProcessDetails(options.BackgroundProcesses),
		mcpServers:                      cloneMcpServerStatuses(options.MCPServers),
		onReadMCPInventory:              options.OnReadMCPInventory,
		onListModels:                    options.OnListModels,
		pendingMCPInventoryMessageIndex: -1,
		mcpStartup:                      chatwidget.NewMcpStartupRoundState(options.MCPStartupExpectedServers),
		initialMessages:                 options.InitialMessages,
		featureSettings:                 cloneBoolMapTea(options.FeatureSettings),
		disablePasteBurst:               options.DisablePasteBurst,
		personality:                     initialPersonality(state, options.Personality),
		tuiTheme:                        strings.TrimSpace(options.TUITheme),
		tuiPet:                          normalizePetIDTea(options.TUIPet),
		onSubmit:                        options.OnSubmit,
		onSubmitRequest:                 options.OnSubmitRequest,
		onSteerRequest:                  options.OnSteerRequest,
		onInterrupt:                     options.OnInterrupt,
		onInterruptMCPStartup:           options.OnInterruptMCPStartup,
		localDaemonSession:              options.LocalDaemonSession,
		agentsOverviewEmbedded:          options.AgentsOverviewEmbedded,
		onAgentsOverviewRefresh:         options.OnAgentsOverviewRefresh,
		onAgentsOverviewDispatch:        options.OnAgentsOverviewDispatch,
		onAgentsOverviewStop:            options.OnAgentsOverviewStop,
		onAgentsOverviewRename:          options.OnAgentsOverviewRename,
		onStartAgentsDaemon:             options.OnStartAgentsDaemon,
		agentsOverviewDrafts:            map[string]string{},
		onExternalEditor:                options.OnExternalEditor,
		externalEditorDirectory:         options.OnExternalEditorDirectory,
		keymapConfig:                    options.KeymapConfig.Clone(),
		onKeymapEdit:                    options.OnKeymapEdit,
		onModalResponse:                 options.OnModalResponse,
		onSessionAction:                 options.OnSessionAction,
		onWorkingDirectoryChange:        options.OnWorkingDirectoryChange,
		onResumeSession:                 options.OnResumeSession,
		onRenameThread:                  options.OnRenameThread,
		onLogout:                        options.OnLogout,
		onReadAgents:                    options.OnReadAgents,
		onSwitchAgent:                   options.OnSwitchAgent,
		backgroundThreadEvents:          map[string][]protocol.ThreadEvent{},
		clipboardWrite:                  clipboardWrite,
		onReadTokenActivity:             options.OnReadTokenActivity,
		onReadRateLimitResetCredits:     options.OnReadRateLimitResetCredits,
		onConsumeRateLimitResetCredit:   options.OnConsumeRateLimitResetCredit,
		onReadRateLimits:                options.OnReadRateLimits,
		pendingStatusRateLimitRequests:  map[uint64]pendingStatusRateLimitRequest{},
		terminalTitleWriter:             terminalTitleWriterOrDefault(options.OnWriteTerminalTitle),
		notificationPost:                options.OnPostNotification,
		notificationSettings:            notificationSettingsOrDefault(options.Notifications),
		notificationMethod:              notificationMethodOrDefault(options.NotificationMethod),
		notificationCondition:           notificationConditionOrDefault(options.NotificationCondition),
		onReadGitDiff:                   options.OnReadGitDiff,
		onStopBackgroundTerminals:       options.OnStopBackgroundTerminals,
		onReadDebugConfig:               options.OnReadDebugConfig,
		onReadGoal:                      options.OnReadGoal,
		onSetGoal:                       options.OnSetGoal,
		onClearGoal:                     options.OnClearGoal,
		onGoalEditText:                  options.OnGoalEditText,
		onGoalDraftMaterialize:          options.OnGoalDraftMaterialize,
		onGoalContinuation:              options.OnGoalContinuation,
		onWriteSettings:                 options.OnWriteSettings,
		onUpdateCollaborationMode:       options.OnUpdateCollaborationMode,
		onWriteMemorySettings:           options.OnWriteMemorySettings,
		onResetMemories:                 options.OnResetMemories,
		onSubmitFeedback:                options.OnSubmitFeedback,
		onReadIDEContext:                options.OnReadIDEContext,
		onApproveAutoReviewDenial:       options.OnApproveAutoReviewDenial,
		useMemories:                     boolPtrValueTeaDefault(options.UseMemories, true),
		generateMemories:                boolPtrValueTeaDefault(options.GenerateMemories, true),
		feedbackEnabled:                 boolPtrValueTeaDefault(options.FeedbackEnabled, true),
		windowsSandboxSetup:             options.OnStartWindowsSandboxSetup,
		onOpenDesktopThread:             options.OnOpenDesktopThread,
		onSandboxReadDir:                options.OnSandboxReadDir,
		onDetectExternalAgent:           options.OnDetectExternalAgent,
		onImportExternalAgent:           options.OnImportExternalAgent,
		pendingExternalAgentImports:     map[string]bool{},
		onReadRolloutPath:               options.OnReadRolloutPath,
		onReadHooks:                     options.OnReadHooks,
		onWriteHookConfig:               options.OnWriteHookConfig,
		onReadPlugins:                   options.OnReadPlugins,
		onReadPlugin:                    options.OnReadPlugin,
		onInstallPlugin:                 options.OnInstallPlugin,
		onUninstallPlugin:               options.OnUninstallPlugin,
		onWritePluginEnabled:            options.OnWritePluginEnabled,
		onAddMarketplace:                options.OnAddMarketplace,
		onRemoveMarketplace:             options.OnRemoveMarketplace,
		onUpgradeMarketplace:            options.OnUpgradeMarketplace,
		onOpenPluginURL:                 options.OnOpenPluginURL,
		pluginUserMarketplaces:          cloneBoolMapTea(options.PluginUserMarketplaces),
		pluginGitMarketplaces:           cloneBoolMapTea(options.PluginGitMarketplaces),
		onReadSkills:                    options.OnReadSkills,
		onWriteSkillEnabled:             options.OnWriteSkillEnabled,
		onFuzzyFileSearch:               options.OnFuzzyFileSearch,
		onReadApps:                      options.OnReadApps,
		onStartReview:                   options.OnStartReview,
		onStartReviewCommand:            options.OnStartReviewCommand,
		onStartCompactCommand:           options.OnStartCompactCommand,
		onStartSide:                     options.OnStartSide,
		onCloseSide:                     options.OnCloseSide,
		onReadReviewBranches:            options.OnReadReviewBranches,
		onReadReviewCommits:             options.OnReadReviewCommits,
		statusLineConfiguredByUser:      options.StatusLineItems != nil,
		rateLimitSnapshots:              map[string]chatwidget.RateLimitSnapshot{},
		rateLimitSwitchPrompt:           chatwidget.RateLimitSwitchPromptIdle,
		hideRateLimitModelNudge:         boolPtrValueTea(options.HideRateLimitModelNudge),
		approvalsReviewer:               chatwidget.ApprovalsReviewerUser,
		permissionRequirements:          clonePermissionRequirementsTea(options.PermissionRequirements),
		hasChatGPTAccount:               options.HasChatGPTAccount,
		chatGPTPlanType:                 strings.TrimSpace(options.ChatGPTPlanType),
		availableRateLimitResetCredits:  cloneInt64PtrTea(options.AvailableRateLimitResetCredits),
		toolCalls:                       map[string]*toolCallDisplayState{},
		mcpToolCalls:                    map[string]*mcpToolCallDisplayState{},
		renderedFileChanges:             map[string]bool{},
		webSearches:                     map[string]*webSearchDisplayState{},
		activeProposedPlans:             map[string]*proposedPlanDisplayState{},
		startedThreadIDs:                map[string]bool{},
		completedThreadIDs:              map[string]bool{},
		now:                             time.Now,
	}
	if threadID := strings.TrimSpace(state.ThreadID); threadID != "" {
		model.markThreadCompleted(threadID)
	}
	if strings.TrimSpace(state.CLIVersion) == "" {
		state.CLIVersion = strings.TrimSpace(options.SessionHeaderVersion)
	}
	state.HasChatGPTAccount = options.HasChatGPTAccount
	if strings.TrimSpace(state.Personality) == "" && strings.TrimSpace(string(options.Personality)) != "" {
		state.Personality = string(model.personality)
	}
	model.syncTaskRunningTimer()
	model.animEngine = anim.NewEngine(20)
	model.overlays = overlay.NewOverlay(true)
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
	for _, cell := range options.InitialHistoryCells {
		if cell != nil {
			model.addHistoryCell(cell)
		}
	}
	if options.WindowsSandboxStartupPrompt != nil {
		model.openWindowsSandboxEnablePrompt(*options.WindowsSandboxStartupPrompt)
	}
	model.refreshTranscript()
	model.petCodexHome = strings.TrimSpace(options.CodexHome)
	model.petEnv = options.PetEnv
	model.petFetch = options.PetFetch
	if model.petFetch == nil {
		model.petFetch = pets.HTTPAssetFetch(nil)
	}
	model.petRuntime = newPetRuntime(nil, model.petEnv)
	if petID := normalizePetIDTea(options.TUIPet); petID != "" && petID != chatwidget.DisabledPetID && model.petImageSupport().Supported() && model.petCodexHome != "" {
		model.petLoadPending = petID
	}
	return model
}

func NewProgram(ctx context.Context, state *codextui.State, options Options, input io.Reader, output io.Writer) *bubbletea.Program {
	options = resolveProgramSize(options, input, output)
	model := NewModel(state, options)
	programOptions := []bubbletea.ProgramOption{}
	if ctx != nil {
		programOptions = append(programOptions, bubbletea.WithContext(ctx))
	}
	if input != nil {
		programOptions = append(programOptions, bubbletea.WithInput(input))
	}
	if output != nil {
		model.petRuntime.setOutput(output)
		programOptions = append(programOptions, bubbletea.WithOutput(&petOutputWriter{runtime: model.petRuntime}))
	}
	programOptions = append(programOptions, bubbletea.WithReportFocus())
	// Rust parity: normal chat runs in the terminal's inline buffer so finalized
	// conversation rows remain in native scrollback. Alternate screen is reserved
	// for temporary overlays; starting the whole chat there makes terminals map
	// the mouse wheel to Up/Down keys, which incorrectly navigates composer history.
	return bubbletea.NewProgram(model, programOptions...)
}

// resolveProgramSize fills in the terminal size when the caller did not
// provide explicit dimensions.
func resolveProgramSize(options Options, input io.Reader, output io.Writer) Options {
	// Bubble Tea only queries the terminal size when it can detect the output
	// as a real terminal. The ambient pet wraps the output writer, so that
	// detection never fires and the Model would otherwise render at the
	// default 80x24 size. Query the terminal up front so the initial layout
	// matches the actual pane, preventing composer/status row wrapping in
	// narrow terminals.
	if options.Width <= 0 || options.Height <= 0 {
		if width, height, ok := detectTerminalSize(input, output); ok {
			options.Width = width
			options.Height = height
		}
	}
	return options
}

// detectTerminalSize reports the terminal dimensions for the given input and
// output handles. It prefers the output handle (mirroring Bubble Tea's own
// resize check), then the input handle, then stderr. On Windows the console
// size can only be queried through an output handle: GetConsoleScreenBufferInfo
// fails for console input handles, so stderr is the fallback when stdout is
// not a terminal (for example when it is redirected).
func detectTerminalSize(input io.Reader, output io.Writer) (int, int, bool) {
	probe := func(fd uintptr) (int, int, bool) {
		width, height, err := term.GetSize(fd)
		if err != nil || width <= 0 || height <= 0 {
			return 0, 0, false
		}
		return width, height, true
	}
	if file, ok := output.(*os.File); ok {
		if width, height, ok := probe(file.Fd()); ok {
			return width, height, true
		}
	}
	if file, ok := input.(*os.File); ok {
		if width, height, ok := probe(file.Fd()); ok {
			return width, height, true
		}
	}
	if width, height, ok := probe(os.Stderr.Fd()); ok {
		return width, height, true
	}
	return 0, 0, false
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
	if model.petRuntime != nil {
		model.petRuntime.clearImmediately()
	}
	return model, nil
}

func resolveStyles(custom *styles.Styles) styles.Styles {
	if custom != nil {
		return *custom
	}
	return styles.DefaultDark()
}
func (m *Model) Init() bubbletea.Cmd {
	commands := []bubbletea.Cmd{m.composer.Focus()}
	if m.animEngine != nil {
		commands = append(commands, m.animEngine.TickCmd())
	}
	if m.petLoadPending != "" {
		commands = append(commands, m.loadPetCmd(m.petLoadPending))
		m.petLoadPending = ""
	}
	if m.initialMessages != nil {
		commands = append(commands, waitForStream(m.initialMessages))
	}
	return bubbletea.Batch(commands...)
}

func (m *Model) Update(message bubbletea.Msg) (bubbletea.Model, bubbletea.Cmd) {
	if m == nil {
		return m, nil
	}
	// Route through overlay when dialog is active
	if m.overlays != nil && m.overlays.Active() {
		if _, ok := message.(bubbletea.KeyMsg); ok {
			return m, m.overlays.Update(message)
		}
	}
	switch msg := message.(type) {
	case anim.TickMsg:
		if m.animEngine != nil {
			cmd := m.animEngine.Advance()
			if m.retryActivityActive {
				m.renderRetryActivity()
			}
			return m, cmd
		}
		return m, nil
	case petLoadMsg:
		return m, m.applyPetLoad(msg)
	case petTickMsg:
		return m, bubbletea.Batch(m.petDrawCmd(), m.petTickCmd())
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
	case ModelRetryStatusMsg:
		if msg.Active {
			m.retryActivityMessage = strings.TrimSpace(msg.Message)
			m.retryActivityActive = m.retryActivityMessage != ""
			m.renderRetryActivity()
		} else {
			m.clearRetryActivity()
		}
		return m, nil
	case ModelCompactionStatusMsg:
		if msg.Active {
			m.startCompactionActivity("", 0, msg.Message)
		} else {
			m.finishCompactionActivity("", false)
		}
		return m, nil
	case TurnCompletedMsg:
		if m.applyInactiveThreadTurnCompleted(msg) {
			return m, m.refreshStatusControlsCmd()
		}
		cmd := m.applyTurnCompleted(msg)
		return m, bubbletea.Batch(cmd, m.refreshStatusControlsCmd(), m.submitNextQueued())
	case TurnInterruptedMsg:
		if m.applyInactiveThreadTurnInterrupted(msg) {
			return m, m.refreshStatusControlsCmd()
		}
		m.applyTurnInterrupted(msg)
		return m, bubbletea.Batch(m.refreshStatusControlsCmd(), m.submitNextQueued())
	case SteerResultMsg:
		m.applySteerResult(msg)
		return m, nil
	case SteerCommittedMsg:
		m.commitPendingSteers(msg.Count)
		return m, nil
	case MCPStartupUpdateMsg:
		return m, m.applyMCPStartupUpdate(msg)
	case MCPStartupInventoryMsg:
		m.mcpServers = cloneMcpServerStatuses(msg.Servers)
		return m, nil
	case MCPInventoryResultMsg:
		m.applyMCPInventoryResult(msg)
		return m, nil
	case ModelsResultMsg:
		m.applyModelsResult(msg)
		return m, nil
	case MCPStartupFinishAfterLagMsg:
		return m, m.finishMCPStartupAfterLag(0)
	case mcpStartupFinishAfterLagMsg:
		return m, m.finishMCPStartupAfterLag(msg.Generation)
	case ExternalEditorFinishedMsg:
		m.applyExternalEditorFinished(msg)
		return m, m.refreshSkillPopup()
	case ThreadEventMsg:
		cmd := m.applyThreadEvent(msg.Event)
		if m.agentsOverview != nil {
			cmd = bubbletea.Batch(cmd, m.refreshAgentsOverviewCmd())
		}
		return m, bubbletea.Batch(cmd, m.refreshStatusControlsCmd())
	case ThreadScopedEventMsg:
		cmd := m.applyThreadScopedEvent(msg)
		if m.agentsOverview != nil {
			cmd = bubbletea.Batch(cmd, m.refreshAgentsOverviewCmd())
		}
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
	case RateLimitsResultMsg:
		return m, m.applyRateLimitsResult(msg)
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
		return m, bubbletea.Batch(m.applyGoalResult(msg), m.refreshStatusControlsCmd())
	case GoalEditTextMsg:
		return m, bubbletea.Batch(m.applyGoalEditTextResult(msg), m.refreshStatusControlsCmd())
	case GoalDraftMaterializeMsg:
		return m, bubbletea.Batch(m.applyGoalDraftMaterializeResult(msg), m.refreshStatusControlsCmd())
	case ReviewStartResultMsg:
		m.applyReviewStartResult(msg)
		return m, m.refreshStatusControlsCmd()
	case CompactStartResultMsg:
		return m, m.applyCompactStartResult(msg)
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
	case MemoryResetResultMsg:
		m.applyMemoryResetResult(msg)
		return m, nil
	case FeedbackSubmitResultMsg:
		m.applyFeedbackSubmitResult(msg)
		return m, nil
	case GuardianReviewMsg:
		m.applyGuardianReview(msg)
		return m, nil
	case AutoReviewDenialApproveResultMsg:
		m.applyAutoReviewDenialApproveResult(msg)
		return m, nil
	case WindowsSandboxSetupResultMsg:
		m.applyWindowsSandboxSetupResult(msg)
		return m, nil
	case WindowsSandboxSetupCompletedMsg:
		m.applyWindowsSandboxSetupCompleted(msg.Completion)
		return m, nil
	case SandboxReadDirResultMsg:
		m.applySandboxReadDirResult(msg)
		return m, nil
	case HooksListResultMsg:
		m.applyHooksListResult(msg)
		return m, nil
	case HookConfigWriteResultMsg:
		return m, m.applyHookConfigWriteResult(msg)
	case PluginListResultMsg:
		return m, m.applyPluginListResult(msg)
	case PluginReadResultMsg:
		return m, m.applyPluginReadResult(msg)
	case PluginInstallResultMsg:
		return m, m.applyPluginInstallResult(msg)
	case PluginUninstallResultMsg:
		return m, m.applyPluginUninstallResult(msg)
	case PluginEnabledWriteResultMsg:
		return m, m.applyPluginEnabledWriteResult(msg)
	case MarketplaceAddResultMsg:
		return m, m.applyMarketplaceAddResult(msg)
	case MarketplaceRemoveResultMsg:
		return m, m.applyMarketplaceRemoveResult(msg)
	case MarketplaceUpgradeResultMsg:
		return m, m.applyMarketplaceUpgradeResult(msg)
	case PluginOpenURLResultMsg:
		m.applyPluginOpenURLResult(msg)
		return m, nil
	case MentionPluginInventoryResultMsg:
		m.applyMentionPluginInventoryResult(msg)
		return m, nil
	case MentionFileSearchResultMsg:
		m.applyMentionFileSearchResult(msg)
		return m, nil
	case AppListResultMsg:
		m.applyAppListResult(msg)
		return m, nil
	case AgentListResultMsg:
		m.applyAgentListResult(msg)
		return m, nil
	case agentsOverviewListMsg:
		return m, m.applyAgentsOverviewList(msg)
	case agentsOverviewDispatchMsg:
		if msg.err != nil {
			m.agentsOverviewNotice = "Failed to start background task: " + strings.TrimSpace(msg.err.Error())
		} else if strings.TrimSpace(msg.threadID) != "" {
			m.agentsOverviewNotice = "Dispatched task " + msg.threadID
		}
		m.agentsOverviewBusy = false
		return m, m.refreshAgentsOverviewCmd()
	case agentsOverviewStopMsg:
		if msg.err != nil {
			m.agentsOverviewNotice = "Failed to stop background task: " + strings.TrimSpace(msg.err.Error())
		} else {
			m.agentsOverviewNotice = ""
		}
		m.agentsOverviewBusy = false
		return m, m.refreshAgentsOverviewCmd()
	case agentsOverviewRenameMsg:
		if msg.err != nil {
			m.agentsOverviewNotice = "Failed to rename task: " + strings.TrimSpace(msg.err.Error())
		} else {
			m.agentsOverviewNotice = ""
		}
		m.agentsOverviewBusy = false
		return m, m.refreshAgentsOverviewCmd()
	case agentsOverviewDaemonMsg:
		if msg.err != nil {
			m.notice = "Failed to start background server: " + strings.TrimSpace(msg.err.Error())
		} else {
			m.notice = "Background server started. Open `codex agents` in another terminal."
		}
		return m, nil
	case AgentSwitchResultMsg:
		m.applyAgentSwitchResult(msg)
		return m, m.refreshStatusControlsCmd()
	case WorkingDirectoryChangeResultMsg:
		m.applyWorkingDirectoryChangeResult(msg)
		return m, nil
	case AgentNavigateResultMsg:
		return m, m.applyAgentNavigateResult(msg)
	case SkillsListResultMsg:
		m.applySkillsListResult(msg)
		return m, nil
	case SkillEnabledWriteResultMsg:
		return m, m.applySkillEnabledWriteResult(msg)
	case ExternalAgentDetectResultMsg:
		return m, m.applyExternalAgentDetectResult(msg)
	case ExternalAgentImportResultMsg:
		return m, m.applyExternalAgentImportResult(msg)
	case ExternalAgentImportCompletedResultMsg:
		m.applyExternalAgentImportCompletedResult(msg)
		return m, nil
	case SkillsInventoryResultMsg:
		m.applySkillsInventoryResult(msg)
		return m, nil
	case LogoutResultMsg:
		return m, m.applyLogoutResult(msg)
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
		if m.agentsOverview != nil {
			return m, m.updateAgentsOverviewKey(msg)
		}
		switch msg.Type {
		case bubbletea.KeyCtrlC:
			if m.modal != nil && (m.modal.customPrompt != nil || m.modal.manageSkills != nil || m.modal.externalAgentMigration != nil || m.modal.hooksBrowser != nil || m.modal.pluginBrowser != nil) {
				return m, m.updateModal(msg)
			}
			if m.modal != nil && m.modal.sessionPicker != nil {
				return m, m.respondModal(true)
			}
			if m.isTaskRunning() {
				if m.localDaemonSession && m.composer.Value() == "" && !m.inSideConversation() {
					m.openRunningTaskExitMenu()
					return m, nil
				}
				return m, m.interruptRunningTask()
			}
			if m.inSideConversation() {
				return m, m.returnFromSideConversation()
			}
			return m, bubbletea.Quit
		case bubbletea.KeyCtrlD:
			if m.modal != nil && (m.modal.externalAgentMigration != nil || m.modal.hooksBrowser != nil || m.modal.pluginBrowser != nil) {
				return m, m.updateModal(msg)
			}
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
		if msg.Alt && m.composer.Value() == "" && len(m.attachments) == 0 {
			switch msg.Type {
			case bubbletea.KeyLeft:
				return m, m.navigateAgent(-1)
			case bubbletea.KeyRight:
				return m, m.navigateAgent(1)
			}
		}
		keySpec := keySpecFromKeyMsg(msg)
		if m.keyMatches("global", "toggle_side_conversation", keySpec) ||
			(keySpec == "ctrl-7" && m.keyMatches("global", "toggle_side_conversation", "ctrl-/")) {
			return m, m.toggleSideConversation()
		}
		if m.keyMatches("global", "open_transcript", keySpec) {
			return m, m.openTranscriptOverlay()
		}
		if m.keyMatches("global", "open_agents", keySpec) {
			return m, m.applyAgentsCommand()
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
		if m.applyEditQueuedMessageKey(msg, keySpec) {
			return m, nil
		}
		if m.applyEditorKillYankKey(msg, keySpec) {
			return m, nil
		}
		if m.applyVimModeKey(msg, keySpec) {
			return m, nil
		}
		if m.applyInputHistoryKey(msg) {
			return m, m.refreshSkillPopup()
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
			if m.isUserTurnPendingOrRunning() {
				if cmd, handled := m.submitRunningSlashCommand(); handled {
					return m, cmd
				}
				return m, m.steerComposer()
			}
			return m, m.submitComposer()
		}
		if m.keyMatches("composer", "queue", keySpec) {
			m.clearComposerPasteWindow()
			if m.isUserTurnPendingOrRunning() {
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
		// Rust parity: do not enable or consume terminal mouse tracking. Leaving
		// mouse input to the terminal preserves scrollback, text selection/copy,
		// and native paste behavior.
		return m, nil
	}

	var cmd bubbletea.Cmd
	m.transcript, cmd = m.transcript.Update(message)
	if _, mouse := message.(bubbletea.MouseMsg); mouse {
		m.activityFollow = m.transcript.AtBottom()
	}
	var composerCmd bubbletea.Cmd
	m.composer, composerCmd = m.composer.Update(message)
	m.refreshSlashPopup()
	skillPopupCmd := m.refreshSkillPopup()
	return m, bubbletea.Batch(cmd, composerCmd, skillPopupCmd)
}

func (m *Model) View() string {
	streamingpkg.SetStreamTheme(m.activeTUITheme())
	if m == nil {
		return ""
	}
	m.ensureSize()
	m.syncTranscriptHeight()
	m.storePetDrawRequest()
	if m.overlay != nil {
		if m.overlayTranscript {
			m.syncTranscriptOverlay()
		}
		return m.overlay.View()
	}
	if m.agentsOverview != nil {
		return m.renderAgentsOverview()
	}
	// Rust renders the session picker as a temporary full-screen surface. It
	// must not be appended below the transcript, where the transcript viewport
	// would push the picker to the bottom of the terminal and clip its rows.
	if m.modal != nil && m.modal.sessionPicker != nil {
		return m.renderSessionPickerModal()
	}
	m.refreshTranscript()

	chrome := m.regionChromeEnabled()
	status := m.statusStyle.Render(fitTerminalLine(m.renderStatusHeader(), m.width))
	transcript := m.transcript.View()
	if chrome {
		status = m.renderStatusRegion(status)
		transcript = m.renderActivityRegion(transcript)
	}
	sections := []string{status, transcript}
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
		composer := m.composer.View()
		composer = annotateComposerHyperlinks(composer)
		if chrome {
			composer = m.renderComposerRegion(composer)
		}
		sections = append(sections, composer)
		if m.vimSearchMode {
			sections = append(sections, m.renderVimSearchFooter())
		}
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
	if m.ideContext.Enabled {
		sections = append(sections, m.footerStyle.Render("IDE context"))
	}
	sections = append(sections, m.footerStyle.Render(fitTerminalLine(footerHelpText, m.width)))
	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

func (m *Model) regionChromeEnabled() bool {
	return m != nil && m.width >= regionChromeMinWidth && m.height >= 16
}

func (m *Model) renderStatusRegion(status string) string {
	return lipgloss.NewStyle().
		Width(max(m.width-2, 1)).
		Padding(0, 1).
		Background(lipgloss.Color("236")).
		Render(status)
}

func (m *Model) renderActivityRegion(content string) string {
	label := " ACTIVITY "
	lineWidth := max(m.width-lipgloss.Width(label), 0)
	header := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(label + strings.Repeat("─", lineWidth))
	return lipgloss.JoinVertical(lipgloss.Left, header, content)
}

func (m *Model) renderComposerRegion(content string) string {
	return lipgloss.NewStyle().
		Width(max(m.width-4, 1)).
		Padding(0, 1).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("8")).
		Render(content)
}

// annotateComposerHyperlinks wraps visible HTTP(S) URLs in each rendered
// composer line with OSC-8 terminal hyperlinks so links the user types are
// clickable, mirroring the Rust composer hyperlink handling (#40720). URLs
// split across soft-wrapped lines may be annotated as fragments, which is an
// acknowledged rough edge until the full wrap-aware cache is wired.
func annotateComposerHyperlinks(content string) string {
	if !strings.Contains(content, "http") {
		return content
	}
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	for i, line := range lines {
		lines[i] = codextui.AnnotateWebURLsInLine(line)
	}
	return strings.Join(lines, "\n")
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
	if m == nil || len(runes) <= 1 || m.disablePasteBurst {
		return
	}
	m.extendComposerPasteWindow(now)
}

func (m *Model) extendComposerPasteWindow(now time.Time) {
	if m == nil || m.disablePasteBurst {
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
	if m == nil || m.disablePasteBurst || m.composerPasteEnterUntil == nil || m.composerStartsSlashContext() {
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
	m.flushCompactCommandGroup()
	if input == "" && len(m.attachments) == 0 {
		m.composerMentionBindings = nil
		return nil
	}
	if invocation, ok := codextui.ParseCommand(input); ok && slashInvocationDispatchable(invocation) {
		m.composerMentionBindings = nil
		if invocation.Command == codextui.CommandGoal && len(m.attachments) > 0 {
			if draft := m.collectGoalDraft(invocation.Args); draft != nil {
				m.pendingGoalDraft = draft
			}
		}
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
	m.flushCompactCommandGroup()
	input := strings.TrimSpace(m.composer.Value())
	if input == "" {
		return nil, false
	}
	invocation, ok := codextui.ParseCommand(input)
	if !ok || !slashInvocationDispatchable(invocation) {
		return nil, false
	}
	if len(m.attachments) > 0 {
		if invocation.Command != codextui.CommandGoal {
			return nil, false
		}
		if draft := m.collectGoalDraft(invocation.Args); draft != nil {
			m.pendingGoalDraft = draft
		}
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

// collectGoalDraft converts the composer attachments into a goal draft,
// assigning sequential [Image #N] placeholders to local images so they match
// either the user-typed placeholder in the objective or the appended
// "Referenced image files:" section (mirrors Rust's GoalDraft construction).
// The attachments are consumed.
func (m *Model) collectGoalDraft(args string) *codextui.GoalDraft {
	draft := codextui.GoalDraft{Objective: args}
	imageIndex := 0
	for _, attachment := range m.attachments {
		switch attachment.Kind {
		case bottompane.AttachmentImage:
			imageIndex++
			draft.LocalImages = append(draft.LocalImages, codextui.GoalLocalImage{
				Placeholder: "[Image #" + strconv.Itoa(imageIndex) + "]",
				Path:        attachment.Path,
			})
		case bottompane.AttachmentRemoteImage:
			draft.RemoteImageURLs = append(draft.RemoteImageURLs, attachment.URL)
		}
	}
	m.attachments = nil
	if len(draft.LocalImages) == 0 && len(draft.RemoteImageURLs) == 0 {
		return nil
	}
	return &draft
}

func (m *Model) submitRequest(request SubmitRequest, parseCommand bool) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if m.misalignmentPolicyStopped {
		m.notice = "Chat stopped as a precaution. Start or resume another chat to continue."
		return nil
	}
	request = cloneSubmitRequest(request)
	if request.CollaborationMode == nil {
		request.CollaborationMode = m.effectiveSubmissionCollaborationMode()
	}
	if request.ServiceTier == "" && m.State != nil {
		request.ServiceTier = strings.TrimSpace(m.State.ServiceTier)
	}
	request.Prompt = strings.TrimSpace(request.Prompt)
	if request.Prompt == "" && len(request.Attachments) == 0 {
		return nil
	}
	if parseCommand && len(request.Attachments) == 0 {
		if invocation, ok := codextui.ParseCommand(request.Prompt); ok && slashInvocationDispatchable(invocation) {
			return m.applyCommand(invocation)
		}
	}
	m.captureIDEContext(&request)
	displayPrompt := m.promptWithRequestAttachments(request)
	m.notice = ""
	m.Transcript.lastTurnError = ""
	m.Transcript.needsFinalMessageSeparator = false
	m.Transcript.activeAssistantDeltaItemID = ""
	m.State.AddMessage(codextui.RoleUser, displayPrompt)
	if m.onSubmit == nil && m.onSubmitRequest == nil {
		m.setStatus("pending")
	} else {
		m.setStatus("running")
	}
	m.submitted = append(m.submitted, displayPrompt)
	if strings.TrimSpace(request.Prompt) != "" {
		m.inputHistory = append(m.inputHistory, request.Prompt)
	}
	m.resetInputHistoryNavigation()
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

func slashInvocationDispatchable(invocation *codextui.CommandInvocation) bool {
	if invocation == nil || strings.TrimSpace(invocation.Args) == "" {
		return invocation != nil
	}
	if chatwidget.CommandSupportsInlineArgs(invocation.Command) {
		return true
	}
	// Go-only compatibility commands keep their established direct-argument form.
	switch invocation.Command {
	case codextui.CommandApproval,
		codextui.CommandSandbox,
		codextui.CommandAttach,
		codextui.CommandImage,
		codextui.CommandURLImage:
		return true
	default:
		return false
	}
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
	m.notice = ""
	m.refreshTranscript()
	return nil
}

func (m *Model) steerComposer() bubbletea.Cmd {
	if m == nil || m.onSteerRequest == nil {
		return m.queueComposer(true)
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
	m.captureIDEContext(&request)
	clientID := "tui-steer-" + uuid.NewString()
	m.pendingSteers = append(m.pendingSteers, pendingSteerSubmission{ID: clientID, Request: cloneSubmitRequest(request)})
	if strings.TrimSpace(request.Prompt) != "" {
		m.inputHistory = append(m.inputHistory, request.Prompt)
	}
	m.resetInputHistoryNavigation()
	m.notice = ""
	m.refreshTranscript()
	return func() bubbletea.Msg {
		return SteerResultMsg{ClientID: clientID, Err: m.onSteerRequest(request, clientID)}
	}
}

func (m *Model) applySteerResult(message SteerResultMsg) {
	if m == nil || message.Err == nil {
		return
	}
	for index := range m.pendingSteers {
		if m.pendingSteers[index].ID != message.ClientID {
			continue
		}
		pending := m.pendingSteers[index]
		m.pendingSteers = append(m.pendingSteers[:index], m.pendingSteers[index+1:]...)
		m.rejectedSteers = append(m.rejectedSteers, queuedSubmission{Request: cloneSubmitRequest(pending.Request), ParseCommand: true})
		m.notice = ""
		m.refreshTranscript()
		return
	}
}

func (m *Model) commitPendingSteers(count int) {
	if m == nil || len(m.pendingSteers) == 0 {
		return
	}
	if count <= 0 || count > len(m.pendingSteers) {
		count = min(max(count, 1), len(m.pendingSteers))
	}
	for _, pending := range m.pendingSteers[:count] {
		m.State.AddMessage(codextui.RoleUser, m.promptWithRequestAttachments(pending.Request))
	}
	m.pendingSteers = append([]pendingSteerSubmission(nil), m.pendingSteers[count:]...)
	m.refreshTranscript()
}

func (m *Model) deferPendingSteers() {
	if m == nil || len(m.pendingSteers) == 0 {
		return
	}
	deferred := make([]queuedSubmission, 0, len(m.pendingSteers))
	for _, pending := range m.pendingSteers {
		deferred = append(deferred, queuedSubmission{Request: cloneSubmitRequest(pending.Request), ParseCommand: true})
	}
	m.pendingSteers = nil
	m.rejectedSteers = append(m.rejectedSteers, deferred...)
}

func (m *Model) submitNextQueued() bubbletea.Cmd {
	if m == nil || !m.isIdle() {
		return nil
	}
	if len(m.rejectedSteers) > 0 {
		next := m.rejectedSteers[0]
		m.rejectedSteers = append([]queuedSubmission(nil), m.rejectedSteers[1:]...)
		return m.submitQueuedRequest(next)
	}
	if len(m.queued) == 0 {
		return nil
	}
	next := m.queued[0]
	copy(m.queued, m.queued[1:])
	m.queued = m.queued[:len(m.queued)-1]
	return m.submitQueuedRequest(next)
}

func (m *Model) submitQueuedRequest(next queuedSubmission) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	request := cloneSubmitRequest(next.Request)
	if next.Literal {
		request.LiteralInput = true
	}
	return m.submitRequest(request, next.ParseCommand)
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

func (m *Model) isUserTurnPendingOrRunning() bool {
	return m != nil && ((m.State != nil && strings.EqualFold(strings.TrimSpace(m.State.Status), "running")) || m.reviewState.IsReviewMode)
}

func (m *Model) isIdle() bool {
	return m != nil && m.State != nil && strings.EqualFold(strings.TrimSpace(m.State.Status), "idle") && !m.reviewState.IsReviewMode
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
	header := strings.TrimSpace(indicator.Header)
	lines[0] = "\u2022 " + lines[0]
	lines[0] = fitTerminalLine(lines[0], m.width)
	if !m.mcpStartupActive {
		plainPrefix := "\u2022 " + header
		if strings.HasPrefix(lines[0], plainPrefix) {
			lines[0] = "\u2022 " + m.renderWorkingHeader(header) + strings.TrimPrefix(lines[0], plainPrefix)
		}
	}
	return strings.Join(lines, "\n")
}

const workingHighlightTicksPerLetter = 3

func (m *Model) renderWorkingHeader(header string) string {
	runes := []rune(header)
	if len(runes) == 0 {
		return ""
	}
	tick := 0
	if m != nil && m.animEngine != nil {
		tick = m.animEngine.CurrentTick()
	}
	active := (tick / workingHighlightTicksPerLetter) % len(runes)
	reset := "\x1b[0m"
	dim := "\x1b[2m"
	if m != nil {
		if m.Styles.ExecCell.Reset != "" {
			reset = m.Styles.ExecCell.Reset
		}
		if m.Styles.Chat.DimText != "" {
			dim = m.Styles.Chat.DimText
		}
	}

	var out strings.Builder
	if active > 0 {
		out.WriteString(dim)
		out.WriteString(string(runes[:active]))
		out.WriteString(reset)
	}
	out.WriteString("\x1b[1m")
	out.WriteRune(runes[active])
	out.WriteString(reset)
	if active+1 < len(runes) {
		out.WriteString(dim)
		out.WriteString(string(runes[active+1:]))
		out.WriteString(reset)
	}
	return out.String()
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
	m.Transcript.markThreadStarted(threadID)
}

func (m *Model) markThreadCompleted(threadID string) {
	if m == nil {
		return
	}
	m.Transcript.markThreadCompleted(threadID)
}

func (m *Model) clearCurrentThreadAfterFailure(message string) {
	if m == nil || m.State == nil {
		return
	}
	m.Transcript.clearCurrentThreadAfterFailure(m.State, message)
}

func (m *Model) addErrorHistoryMessage(message string) {
	if m == nil || m.State == nil {
		return
	}
	m.Transcript.addErrorHistoryMessage(m.State, message, m.width)
}

func (m *Model) addTurnErrorHistoryMessage(message string) {
	if m == nil || m.State == nil {
		return
	}
	m.Transcript.addTurnErrorHistoryMessage(m.State, message, m.width)
}

func (m *Model) addInfoHistoryMessage(message string) {
	if m == nil || m.State == nil {
		return
	}
	m.Transcript.addInfoHistoryMessage(m.State, message, m.width)
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
	m.flushCompactCommandGroup()
	m.deferPendingSteers()
	if message.Err != nil {
		m.setStatus("error")
		errorMessage := message.Err.Error()
		if chatwidget.IsMisalignmentPolicyViolationMessage(errorMessage) {
			return m.applyMisalignmentPolicyViolation(errorMessage)
		}
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
	m.Transcript.lastTurnError = ""
	m.notice = ""
	m.refreshTranscript()
	if len(m.queued) > 0 || len(m.rejectedSteers) > 0 {
		return nil
	}
	// Rust's goal runtime continues an active goal whenever the thread is
	// idle. Refresh the persisted goal first so the model's update_goal
	// completion/block decisions during the turn are honored instead of
	// looping on a stale in-memory status.
	if m.currentGoal != nil && m.currentGoal.Status == appserver.GoalActive &&
		m.onGoalContinuation != nil && m.onReadGoal != nil {
		return m.readGoalForAction(goalActionRefresh, m.goalThreadID(), "")
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
	m.flushCompactCommandGroup()
	m.deferPendingSteers()
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
		if current := strings.TrimSpace(m.State.ThreadID); current != "" && current != strings.TrimSpace(event.ThreadID) {
			m.toolRequestRuntime = chatwidget.ToolRequestRuntimeState{}
			m.resetReviewModeState()
		}
		m.State.SetThreadID(event.ThreadID)
		m.markThreadStarted(event.ThreadID)
		m.persistPendingThreadName()
		if objective := strings.TrimSpace(m.pendingGoalObjective); objective != "" {
			m.pendingGoalObjective = ""
			cmd = m.prepareGoalSet(objective)
		}
	case "turn.started":
		m.setStatus("running")
		m.Transcript.lastTurnError = ""
		m.retryMessageIndex = -1
		m.retryActivityActive = false
		m.clearCompactionActivity()
	case "turn.reconnecting":
		if event.Item != nil {
			message := event.Item.Message
			if event.Item.Output != "" {
				message += "\n└ " + event.Item.Output
			}
			m.retryActivityMessage = message
			m.retryActivityActive = true
			m.renderRetryActivity()
		}
	case "turn.reconnected":
		m.clearRetryActivity()
	case "turn.compacting":
		message := "Compacting context..."
		if event.Item != nil && strings.TrimSpace(event.Item.Message) != "" {
			message = strings.TrimSpace(event.Item.Message)
		}
		m.startCompactionActivity("", 0, message)
	case "turn.compacted":
		m.finishCompactionActivity("", true)
	case "item.started":
		startedAtMS := int64(0)
		if event.StartedAtMS != nil {
			startedAtMS = *event.StartedAtMS
		}
		m.applyItemStarted(event.Item, startedAtMS)
	case "item.completed":
		cmd = m.applyItemCompleted(event.Item)
	case "item.delta":
		m.applyDelta(event.Delta)
	case "item.plan.delta":
		m.applyProposedPlanDelta(event.Delta)
	case "turn.completed":
		m.setStatus("idle")
		m.markThreadCompleted(m.State.ThreadID)
		m.Transcript.lastTurnError = ""
		m.clearRetryActivity()
		m.clearCompactionActivity()
	case "turn.failed", "error":
		message := "Unknown error"
		if event.Error != nil && strings.TrimSpace(event.Error.Message) != "" {
			message = strings.TrimSpace(event.Error.Message)
		}
		m.setStatus("error")
		m.clearRetryActivity()
		m.clearCompactionActivity()
		m.markActiveToolCallsFailed(message)
		m.clearCurrentThreadAfterFailure(message)
		m.addTurnErrorHistoryMessage(message)
		m.notice = message
	case "response.rate_limits":
		if event.RateLimit != nil {
			cmd = m.applyRateLimitSnapshot(rateLimitSnapshotFromProtocol(event.RateLimit))
		}
	case "thread.token_usage.updated":
		m.applyTokenUsage(event.TokenUsage)
	}
	m.refreshTranscript()
	return cmd
}

func (m *Model) applyTokenUsage(info *protocol.ThreadTokenUsage) {
	if m == nil || m.State == nil || info == nil {
		return
	}
	m.State.TotalTokenUsage = tokenUsageFromProtocol(info.Total)
	m.State.LastTokenUsage = tokenUsageFromProtocol(info.Last)
	if info.ModelContextWindow == nil {
		m.State.ModelContextWindow = nil
	} else {
		value := *info.ModelContextWindow
		m.State.ModelContextWindow = &value
	}
}

func tokenUsageFromProtocol(usage protocol.Usage) codextui.TokenUsage {
	return codextui.TokenUsage{InputTokens: usage.InputTokens, CachedInputTokens: usage.CachedInputTokens, OutputTokens: usage.OutputTokens, ReasoningOutputTokens: usage.ReasoningOutputTokens, TotalTokens: usage.TotalTokens}
}

func (m *Model) applyItemStarted(item *protocol.ThreadItem, startedAtMS int64) {
	if item == nil {
		return
	}
	if item.Type != "command_execution" {
		// A new non-command item is an interaction boundary for compact command
		// groups (Rust #38921 chatwidget add_to_history / tool_requests).
		m.flushCompactCommandGroup()
	}
	switch item.Type {
	case "command_execution":
		m.Transcript.finishAssistantPreambleBeforeTool()
		m.renderCommandExecutionItem(item)
	case "mcp_tool_call":
		m.Transcript.finishAssistantPreambleBeforeTool()
		m.renderMCPToolCallItem(item, false)
	case "web_search", "webSearch":
		m.Transcript.finishAssistantPreambleBeforeTool()
		m.renderWebSearchItem(item, false)
	case "tool_call":
		m.Transcript.finishAssistantPreambleBeforeTool()
		m.startOrUpdateToolCall(item)
	case "file_change":
		m.Transcript.finishAssistantPreambleBeforeTool()
		m.renderFileChangeItem(item, false)
	case "agent_message":
		// Streaming deltas create the visible assistant message.
	case "plan":
		m.proposedPlanState(item.ID)
	case "imageGeneration":
		// The completed event carries the saved path.
	case "contextCompaction", "context_compaction":
		m.startCompactionActivity(strings.TrimSpace(item.ID), startedAtMS, "Compacting context...")
	case "enteredReviewMode", "entered_review_mode":
		m.enterReviewMode(firstNonEmpty(strings.TrimSpace(item.Text), strings.TrimSpace(item.Message)))
	case "collab_tool_call", "collabAgentToolCall", "collab_agent_tool_call":
		m.renderCollabAgentToolCall(item, false)
	case "sub_agent_activity", "subAgentActivity":
		m.renderSubAgentActivity(item)
	}
}

func (m *Model) applyItemCompleted(item *protocol.ThreadItem) bubbletea.Cmd {
	if item == nil {
		return nil
	}
	if item.Type != "command_execution" {
		m.flushCompactCommandGroup()
	}
	switch item.Type {
	case "user_message", "userMessage":
		if len(m.pendingSteers) > 0 && m.pendingSteers[0].ID == strings.TrimSpace(item.ID) {
			m.commitPendingSteers(1)
		}
	case "agent_message":
		if strings.EqualFold(strings.TrimSpace(item.Delivery), "async") {
			// Rust #39312: an async agent message is user-visible but does not
			// end the turn, so it renders as a standalone assistant message
			// without becoming the turn's final answer.
			m.Transcript.finishAssistantPreambleBeforeTool()
			m.applyHistoryCell(historycell.NewAgentMessageCell([]string{item.Text}, true))
		} else if strings.EqualFold(strings.TrimSpace(item.Phase), "commentary") {
			m.Transcript.completeAssistantCommentary(m.State, item.ID, item.Text, m.width)
		} else {
			m.mergeAssistantFinal(item.Text)
		}
	case "plan":
		m.completeProposedPlan(item)
	case "command_execution":
		m.renderCommandExecutionItem(item)
	case "mcp_tool_call":
		m.renderMCPToolCallItem(item, true)
	case "web_search", "webSearch":
		m.renderWebSearchItem(item, true)
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
	case "file_change":
		m.renderFileChangeItem(item, true)
	case "imageGeneration":
		m.applyImageGenerationItem(item)
	case "exitedReviewMode", "exited_review_mode":
		m.exitReviewMode()
	case "enteredReviewMode", "entered_review_mode":
		// The started lifecycle event owns the live banner.
	case "contextCompaction", "context_compaction":
		message, live := m.finishCompactionActivity(strings.TrimSpace(item.ID), false)
		if !live || strings.TrimSpace(message) == "" {
			message = "Context compacted"
		}
		m.applyHistoryCell(historycell.NewPlainHistoryCell([]string{message}))
	case "collab_tool_call", "collabAgentToolCall", "collab_agent_tool_call":
		m.renderCollabAgentToolCall(item, true)
	case "sub_agent_activity", "subAgentActivity":
		m.renderSubAgentActivity(item)
	}
	return nil
}

// applyMisalignmentPolicyViolation stops the affected chat: the active turn
// is finalized, queued and draft input are cleared, the composer is disabled,
// and a non-dismissible precaution view directs the user to start or resume
// another chat (Rust #39261 misalignment_policy.rs).
func (m *Model) applyMisalignmentPolicyViolation(message string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	m.clearCurrentThreadAfterFailure(message)
	m.Transcript.clearCurrentThreadAfterFailure(m.State, message)
	m.addTurnErrorHistoryMessage(message)
	m.notice = "Chat stopped as a precaution"
	m.deferPendingSteers()
	m.queued = nil
	m.rejectedSteers = nil
	m.composer.SetValue("")
	m.composer.Blur()
	m.misalignmentPolicyStopped = true
	m.refreshTranscript()
	return nil
}

func (m *Model) renderCollabAgentToolCall(item *protocol.ThreadItem, completed bool) {
	if m == nil || item == nil {
		return
	}
	cell, ok := historycell.NewCollabAgentToolCall(item, completed)
	if !ok {
		return
	}
	m.Transcript.finishAssistantPreambleBeforeTool()
	m.applyHistoryCell(cell)
}

func (m *Model) renderSubAgentActivity(item *protocol.ThreadItem) {
	if m == nil || item == nil {
		return
	}
	cell, ok := historycell.NewSubAgentActivity(item.ActivityKind, item.AgentPath)
	if !ok {
		return
	}
	m.Transcript.finishAssistantPreambleBeforeTool()
	m.applyHistoryCell(cell)
}

func (m *Model) renderFileChangeItem(item *protocol.ThreadItem, completed bool) {
	if m == nil || item == nil {
		return
	}
	changes := make(map[string]codextui.FileChange, len(item.Changes))
	for _, change := range item.Changes {
		switch change.Kind {
		case "add":
			changes[change.Path] = codextui.NewAddFileChange(change.Diff)
		case "delete":
			changes[change.Path] = codextui.NewDeleteFileChange(change.Diff)
		case "update":
			changes[change.Path] = codextui.NewUpdateFileChange(change.Diff, change.MovePath)
		}
	}
	if completed && strings.EqualFold(item.Status, "failed") {
		message := firstNonEmpty(strings.TrimSpace(item.Stderr), strings.TrimSpace(item.Stdout), "Patch application failed.")
		m.applyHistoryCell(historycell.NewPatchApplyFailure(message))
		m.Transcript.needsFinalMessageSeparator = true
		return
	}
	if len(changes) > 0 {
		id := strings.TrimSpace(item.ID)
		if completed && id != "" && m.renderedFileChanges[id] {
			m.Transcript.needsFinalMessageSeparator = true
			return
		}
		m.applyHistoryCell(historycell.NewPatchEventWithTheme(changes, m.State.CWD, m.activeTUITheme()))
		if id != "" {
			if m.renderedFileChanges == nil {
				m.renderedFileChanges = map[string]bool{}
			}
			m.renderedFileChanges[id] = true
		}
		if completed {
			m.Transcript.needsFinalMessageSeparator = true
		}
	}
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

func (m *Model) proposedPlanState(itemID string) *proposedPlanDisplayState {
	if m == nil {
		return nil
	}
	if m.activeProposedPlans == nil {
		m.activeProposedPlans = map[string]*proposedPlanDisplayState{}
	}
	itemID = strings.TrimSpace(itemID)
	if state := m.activeProposedPlans[itemID]; state != nil {
		return state
	}
	state := &proposedPlanDisplayState{MessageIndex: -1}
	m.activeProposedPlans[itemID] = state
	return state
}

func (m *Model) applyProposedPlanDelta(delta *protocol.Delta) {
	if m == nil || delta == nil || delta.Text == "" {
		return
	}
	state := m.proposedPlanState(delta.ItemID)
	if state == nil {
		return
	}
	state.Text.WriteString(delta.Text)
	m.renderProposedPlanState(state)
}

func (m *Model) completeProposedPlan(item *protocol.ThreadItem) {
	if m == nil || item == nil {
		return
	}
	state := m.proposedPlanState(item.ID)
	if state == nil {
		return
	}
	if item.Text != "" {
		state.Text.Reset()
		state.Text.WriteString(item.Text)
	}
	m.renderProposedPlanState(state)
	delete(m.activeProposedPlans, strings.TrimSpace(item.ID))
}

func (m *Model) renderProposedPlanState(state *proposedPlanDisplayState) {
	if m == nil || state == nil {
		return
	}
	width := m.width
	if width < 20 {
		width = 20
	}
	cell := historycell.NewProposedPlan(state.Text.String())
	state.MessageIndex = m.upsertHistoryMessage(state.MessageIndex, cell.DisplayLines(width), cell.RawLines())
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
		Source:  execCommandSourceForItem(item),
	}
	inProgress := commandExecutionInProgress(item.Status)
	if inProgress {
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
		call.Duration = m.commandExecutionDurationForItem(state)
		state.Completed = true
	}

	// Compact command grouping (Rust #38921): consecutive successful Agent and
	// unified-exec startup commands accumulate into one "Ran N commands" cell.
	// A running command joins the active group; a completion completes its call
	// in the group; anything else breaks the group and renders on its own.
	if m.compactCommandGroup != nil {
		group := *m.compactCommandGroup
		handled := false
		if inProgress {
			next, ok := group.Cell.WithAddedCall(call.CallID, call.Command, call.Parsed, call.Source, call.InteractionInput)
			if ok {
				handled = true
				m.compactCommandGroup.Cell = next
				m.compactCommandGroup.MessageIndex = m.upsertHistoryMessage(group.MessageIndex, next.DisplayLinesWithTheme(width, m.activeTUITheme()), next.RawLines())
			}
		} else if call.Output != nil {
			duration := time.Duration(0)
			if call.Duration != nil {
				duration = *call.Duration
			}
			if group.Cell.CompleteCall(call.CallID, *call.Output, duration) {
				handled = true
				m.compactCommandGroup.MessageIndex = m.upsertHistoryMessage(group.MessageIndex, group.Cell.DisplayLinesWithTheme(width, m.activeTUITheme()), group.Cell.RawLines())
				if group.Cell.ShouldFlush() {
					m.flushCompactCommandGroup()
				} else {
					m.compactCommandGroup.Cell = group.Cell
				}
			}
		}
		if handled {
			state.Completed = true
			return
		}
		m.flushCompactCommandGroup()
	}

	cell := execcell.NewExecCell(call, false)
	state.MessageIndex = m.upsertHistoryMessage(state.MessageIndex, cell.DisplayLinesWithTheme(width, m.activeTUITheme()), cell.RawLines())
	if !inProgress && execcell.IsGroupableSource(call.Source) && call.Output != nil && call.Output.ExitCode == 0 {
		// Seed a compact group with the completed command so the next groupable
		// command joins it (Rust keeps the completed compact cell un-flushed).
		m.compactCommandGroup = &compactCommandGroupState{MessageIndex: state.MessageIndex, Cell: cell}
	}
	if state.Completed {
		m.Transcript.needsFinalMessageSeparator = true
	}
}

// flushCompactCommandGroup ends a compact command group. The rendered history
// cell stays in place; only the live accumulation state is reset so subsequent
// commands start their own cell. Groups with running commands are preserved
// (Rust flush_completed_command_activity only flushes inactive exec cells).
func (m *Model) flushCompactCommandGroup() {
	if m == nil || m.compactCommandGroup == nil || m.compactCommandGroup.Cell.IsActive() {
		return
	}
	m.compactCommandGroup = nil
	m.Transcript.needsFinalMessageSeparator = true
}

// execCommandSourceForItem reads the command execution source from item
// metadata (set by the app layer when translating app-server payloads) with a
// default of Agent, mirroring the wire default in Rust protocol.rs.
func execCommandSourceForItem(item *protocol.ThreadItem) execcell.ExecCommandSource {
	if item == nil {
		return execcell.ExecSourceAgent
	}
	switch appserver.CommandExecutionSource(strings.TrimSpace(metadataString(item.Metadata, "source"))) {
	case appserver.CommandExecutionSourceUserShell:
		return execcell.ExecSourceUserShell
	case appserver.CommandExecutionSourceUnifiedExecStartup:
		return execcell.ExecSourceUnifiedExecStartup
	case appserver.CommandExecutionSourceUnifiedExecInteraction:
		return execcell.ExecSourceUnifiedExecInteraction
	default:
		return execcell.ExecSourceAgent
	}
}

func (m *Model) commandExecutionDurationForItem(state *toolCallDisplayState) *time.Duration {
	if state == nil || state.StartedAt.IsZero() {
		return nil
	}
	elapsed := m.currentTime().Sub(state.StartedAt)
	if elapsed < 0 {
		elapsed = 0
	}
	return &elapsed
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
		m.Transcript.needsFinalMessageSeparator = true
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

func (m *Model) renderWebSearchItem(item *protocol.ThreadItem, completed bool) {
	if m == nil || item == nil {
		return
	}
	id := firstNonEmpty(strings.TrimSpace(item.ID), strings.TrimSpace(item.CallID))
	if id == "" {
		return
	}
	if m.webSearches == nil {
		m.webSearches = map[string]*webSearchDisplayState{}
	}
	state := m.webSearches[id]
	if state == nil {
		state = &webSearchDisplayState{ID: id, MessageIndex: -1}
		m.webSearches[id] = state
	}

	var cell historycell.WebSearchCell
	if completed {
		cell = historycell.NewWebSearchCall(id, item.Query, webSearchActionFromProtocolItem(item))
		m.Transcript.needsFinalMessageSeparator = true
	} else {
		cell = historycell.NewActiveWebSearchCall(id, item.Query)
	}
	width := m.width
	if width < 20 {
		width = 20
	}
	state.MessageIndex = m.upsertHistoryMessage(state.MessageIndex, cell.DisplayLines(width), cell.RawLines())
}

func webSearchActionFromProtocolItem(item *protocol.ThreadItem) historycell.WebSearchAction {
	if item == nil {
		return historycell.WebSearchAction{Kind: historycell.WebSearchActionOther}
	}
	action := item.Action
	kind := historycell.WebSearchActionKind(metadataString(action, "type"))
	switch kind {
	case historycell.WebSearchActionSearch:
		return historycell.WebSearchAction{
			Kind:    kind,
			Query:   metadataString(action, "query"),
			Queries: metadataStringSlice(action, "queries"),
		}
	case historycell.WebSearchActionOpenPage, historycell.WebSearchActionKind("open_page"):
		return historycell.WebSearchAction{
			Kind: historycell.WebSearchActionOpenPage,
			URL:  metadataString(action, "url"),
		}
	case historycell.WebSearchActionFindInPage, historycell.WebSearchActionKind("find_in_page"):
		return historycell.WebSearchAction{
			Kind:    historycell.WebSearchActionFindInPage,
			URL:     metadataString(action, "url"),
			Pattern: metadataString(action, "pattern"),
		}
	}
	if query := strings.TrimSpace(item.Query); query != "" {
		return historycell.WebSearchAction{Kind: historycell.WebSearchActionSearch, Query: query}
	}
	return historycell.WebSearchAction{Kind: historycell.WebSearchActionOther}
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
		m.State.BumpMessagesRevision()
		return index
	}
	m.State.Messages = append(m.State.Messages, message)
	m.State.BumpMessagesRevision()
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

func (m *Model) renderRetryActivity() {
	if m == nil {
		return
	}
	message := strings.TrimSpace(m.retryActivityMessage)
	if message == "" {
		return
	}
	if m.compactionActive && strings.HasPrefix(message, "Compacting context") {
		message = m.compactionActivityText(message)
	}
	frames := []string{"◐", "◓", "◑", "◒"}
	frame := frames[0]
	if m.animEngine != nil {
		frame = frames[m.animEngine.CurrentTick()%len(frames)]
	}
	message = frame + " " + message
	width := m.width
	if width < 20 {
		width = 20
	}
	cell := historycell.NewInfoEvent(message, "")
	m.retryMessageIndex = m.upsertHistoryMessage(m.retryMessageIndex, cell.DisplayLines(width), cell.RawLines())
	m.refreshTranscript()
}

func (m *Model) clearRetryActivity() {
	if m == nil {
		return
	}
	m.retryActivityActive = false
	m.retryActivityMessage = ""
	if m.State != nil && m.retryMessageIndex >= 0 && m.retryMessageIndex < len(m.State.Messages) {
		m.State.Messages = append(m.State.Messages[:m.retryMessageIndex], m.State.Messages[m.retryMessageIndex+1:]...)
		m.State.BumpMessagesRevision()
	}
	m.retryMessageIndex = -1
	// A retry/reconnect status is transient and must not hide an in-flight
	// compaction: restore the compaction row once the transient status ends
	// (Rust #42319 status_controls precedence).
	if m.compactionActive {
		m.retryActivityMessage = "Compacting context..."
		m.retryActivityActive = true
		m.renderRetryActivity()
	}
}

// startCompactionActivity enters (or refreshes) the live context-compaction
// status. A matching in-flight compaction keeps its wall clock; a new id (or a
// first status update) restores the clock from the item's startedAtMS when the
// timestamp predates the current time (Rust #42319 compaction.rs).
func (m *Model) startCompactionActivity(id string, startedAtMS int64, message string) {
	if m == nil {
		return
	}
	if m.compactionActive {
		if id != "" && m.compactionID == id {
			m.retryActivityMessage = "Compacting context..."
			m.retryActivityActive = true
			m.renderRetryActivity()
			return
		}
		if id == "" && m.compactionID != "" {
			return
		}
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = "Compacting context..."
	}
	if !m.compactionActive {
		m.compactionID = id
		m.compactionStartedAt = m.currentTime()
		if startedAtMS > 0 {
			if restored := time.UnixMilli(startedAtMS); restored.Before(m.compactionStartedAt) {
				m.compactionStartedAt = restored
			}
		}
		m.compactionActive = true
	}
	m.retryActivityMessage = message
	m.retryActivityActive = true
	m.renderRetryActivity()
}

// finishCompactionActivity clears a matching live compaction and returns the
// completion marker with its measured duration. Non-matching completions are
// ignored so late or out-of-order notifications cannot clear a newer phase.
func (m *Model) finishCompactionActivity(id string, fromReplay bool) (string, bool) {
	if m == nil || !m.compactionActive {
		return "", false
	}
	if id != "" && m.compactionID != id {
		return "", false
	}
	live := !fromReplay && !m.compactionStartedAt.IsZero()
	message := "Context compacted"
	if live {
		elapsed := int64(m.currentTime().Sub(m.compactionStartedAt).Seconds())
		message += " \u00b7 " + codextui.FormatElapsedCompact(elapsed)
	}
	m.clearCompactionActivity()
	return message, live
}

// clearCompactionActivity drops live compaction state and removes the status
// row only when compaction still owns it (a retry/reconnect status takes
// precedence while compaction is active, mirroring Rust status_controls).
func (m *Model) clearCompactionActivity() {
	if m == nil {
		return
	}
	compactionOwnsRow := m.retryActivityActive && strings.HasPrefix(strings.TrimSpace(m.retryActivityMessage), "Compacting context")
	m.compactionActive = false
	m.compactionID = ""
	m.compactionStartedAt = time.Time{}
	if compactionOwnsRow {
		m.clearRetryActivity()
	}
}

// compactionActivityText appends the live elapsed time to a compaction status
// row so the timer ticks independently of the turn's running time (#42319).
func (m *Model) compactionActivityText(message string) string {
	if m == nil || !m.compactionActive || m.compactionStartedAt.IsZero() {
		return message
	}
	elapsed := m.currentTime().Sub(m.compactionStartedAt)
	return message + " (" + codextui.FormatElapsedCompact(int64(elapsed.Seconds())) + ")"
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
	m.syncStateRateLimits()
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

func (m *Model) syncStateRateLimits() {
	if m == nil || m.State == nil {
		return
	}
	m.State.RateLimits = nil
	snapshot, ok := m.rateLimitSnapshots["codex"]
	if !ok {
		snapshot, ok = m.rateLimitSnapshots[""]
	}
	if !ok {
		return
	}
	if snapshot.Primary != nil {
		m.State.RateLimits = append(m.State.RateLimits, codextui.RateLimitStatus{Label: rateLimitLabel(snapshot.Primary, "5h"), UsedPercent: snapshot.Primary.UsedPercent})
	}
	if snapshot.Secondary != nil {
		m.State.RateLimits = append(m.State.RateLimits, codextui.RateLimitStatus{Label: rateLimitLabel(snapshot.Secondary, "weekly"), UsedPercent: snapshot.Secondary.UsedPercent})
	}
}

func rateLimitLabel(window *chatwidget.RateLimitWindow, fallback string) string {
	if window == nil || window.WindowDurationMins == nil {
		return fallback
	}
	switch *window.WindowDurationMins {
	case 300:
		return "5h"
	case 10080:
		return "weekly"
	case 43200, 43800, 44640:
		return "monthly"
	default:
		return fallback
	}
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

func boolPtrValueTeaDefault(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
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
	m.flushCompactCommandGroup()
	if m.inSideConversation() && !sideSlashCommandAllowed(invocation.Command) {
		message := sideSlashUnavailableMessage(invocation.Name)
		if invocation.Command == codextui.CommandRename {
			message = SideRenameBlockMessage
		}
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
		return m.applyStatusCommand()
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
		m.startFreshNamedSession(invocation.Args, "Started a new local thread.")
	case codextui.CommandInit:
		return m.submitRequest(SubmitRequest{Prompt: initCommandPrompt()}, false)
	case codextui.CommandCompact:
		return m.startCompaction()
	case codextui.CommandClear:
		m.startFreshNamedSession(invocation.Args, "Started a fresh session.")
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
		return m.applyModelSetting(invocation.Args)
	case codextui.CommandFast:
		return m.applyFastServiceTier()
	case codextui.CommandPersonality:
		return m.applyPersonalityCommand(invocation.Args)
	case codextui.CommandPlan:
		return m.applyPlanCommand(invocation.Args)
	case codextui.CommandAgent:
		return m.applyAgentCommand()
	case codextui.CommandAgents:
		return m.applyAgentsCommand()
	case codextui.CommandSide:
		return m.applySideCommand(invocation.Name, invocation.Args)
	case codextui.CommandResume:
		return m.applyResumeCommand(invocation.Args)
	case codextui.CommandFork:
		return m.applyForkCurrentSession(invocation.Args)
	case codextui.CommandCd:
		return m.applyWorkingDirectoryChangeCommand(invocation.Args)
	case codextui.CommandPwd:
		m.applyWorkingDirectoryDisplayCommand()
	case codextui.CommandArchive:
		m.openCurrentSessionActionConfirmation(codextui.SessionSelectionArchive)
	case codextui.CommandUnarchive:
		return m.openSessionPicker(codextui.SessionPickerUnarchive)
	case codextui.CommandDelete:
		m.openCurrentSessionActionConfirmation(codextui.SessionSelectionDelete)
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
		return m.applyRenameCommand(invocation.Args)
	case codextui.CommandMention:
		m.composer.InsertString("@")
		m.notice = ""
		m.refreshTranscript()
		return m.refreshSkillPopup()
	case codextui.CommandSkills:
		m.openSkillsMenu()
	case codextui.CommandHooks:
		return m.applyHooksCommand()
	case codextui.CommandMcp:
		return m.applyMCPCommand(invocation.Args)
	case codextui.CommandApps:
		return m.applyAppsCommand()
	case codextui.CommandPlugins:
		return m.applyPluginsCommand()
	case codextui.CommandTheme:
		return m.applyThemeCommand(invocation.Args)
	case codextui.CommandPets:
		return m.applyPetsCommand(invocation.Args)
	case codextui.CommandIde:
		m.applyIDECommand(invocation.Args)
	case codextui.CommandVim:
		m.toggleVimMode()
	case codextui.CommandAutoReview:
		m.openAutoReviewDenials()
	case codextui.CommandMemories:
		m.openMemoriesSettings()
	case codextui.CommandFeedback:
		m.openFeedbackFlow()
	case codextui.CommandApp:
		threadID := strings.TrimSpace(m.State.ThreadID)
		if threadID == "" {
			m.notice = "Session is still starting; try /app again in a moment."
			m.addErrorHistoryMessage(m.notice)
			break
		}
		if m.onOpenDesktopThread == nil {
			m.notice = "Failed to open this session in the Desktop app: Desktop handoff is unavailable in this runtime. Install or launch the Desktop app and try again."
			m.addErrorHistoryMessage(m.notice)
			break
		}
		if err := m.onOpenDesktopThread(threadID); err != nil {
			m.notice = "Failed to open this session in the Desktop app: " + err.Error() + ". Install or launch the Desktop app and try again."
			m.addErrorHistoryMessage(m.notice)
		} else {
			m.notice = "Opened this session in the Desktop app."
			m.addInfoHistoryMessage(m.notice)
		}
	case codextui.CommandImport:
		return m.applyExternalAgentImportCommand()
	case codextui.CommandElevateSandbox:
		return m.applyWindowsSandboxSetupCommand(chatwidget.WindowsSandboxModeElevated)
	case codextui.CommandSandboxReadRoot:
		path := strings.TrimSpace(invocation.Args)
		if path == "" {
			m.notice = "Usage: /sandbox-add-read-dir <absolute-directory-path>"
			m.addErrorHistoryMessage(m.notice)
		} else if m.onSandboxReadDir == nil {
			m.notice = "Sandbox read directory request is unavailable in this runtime."
			m.addErrorHistoryMessage(m.notice)
		} else {
			m.notice = "Granting sandbox read access to " + path + " ..."
			m.addInfoHistoryMessage(m.notice)
			grant := m.onSandboxReadDir
			m.refreshTranscript()
			return func() bubbletea.Msg {
				canonicalPath, err := grant(path)
				return SandboxReadDirResultMsg{RequestedPath: path, CanonicalPath: canonicalPath, Err: err}
			}
		}
	case codextui.CommandRollout:
		path := ""
		if m.onReadRolloutPath != nil {
			var err error
			path, err = m.onReadRolloutPath(m.State.ThreadID)
			if err != nil {
				m.notice = "Failed to read rollout path: " + err.Error()
				m.addErrorHistoryMessage(m.notice)
				break
			}
		}
		if strings.TrimSpace(path) == "" {
			m.notice = "Rollout path is not available yet."
		} else {
			m.notice = "Current rollout path: " + path
		}
		m.addInfoHistoryMessage(m.notice)
	case codextui.CommandTestApproval:
		m.openApprovalModal(ApprovalRequestMsg{
			ID:    "1",
			Title: "Would you like to make the following edits?",
			Options: []ModalOption{
				{ID: "accept", Label: "Yes, proceed", Shortcut: "y"},
				{ID: "accept_session", Label: "Yes, and don't ask again for these files", Shortcut: "a"},
				{ID: "cancel", Label: "No, and tell Codex what to do differently", Shortcut: "esc"},
			},
		})
	case codextui.CommandMemoryDrop, codextui.CommandMemoryUpdate:
		m.applyHistoryCell(historycell.NewPlainHistoryCell([]string{invocation.Name, "", "Memory maintenance requires app-server support."}))
		m.notice = "Memory maintenance"
	case codextui.CommandLogout:
		return m.applyLogoutCommand()
	default:
		m.notice = "Unknown command " + invocation.Name + ". Type /help for commands."
	}
	m.refreshTranscript()
	return nil
}

func (m *Model) applyModelSetting(args string) bubbletea.Cmd {
	value := strings.TrimSpace(args)
	if value != "" {
		m.State.Model = value
		m.refreshServiceTierCommands()
		m.notice = strings.TrimSpace(m.State.RenderSetting("Model", m.State.Model))
		return nil
	}
	return m.openModelPicker()
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
	m.transcript.Width = m.width
	m.transcript.Height = m.transcriptHeightForLayout()
	composerWidth := m.width
	if m.regionChromeEnabled() {
		composerWidth = max(m.width-6, 1)
	}
	m.composer.SetWidth(composerWidth)
	m.composer.SetHeight(defaultComposerHeight)
	m.refreshTranscript()
}

// transcriptHeightForLayout returns the transcript viewport height for the
// current terminal size, modal, and popup state. Rows used by the modal or by
// the composer plus slash/skill popups are reserved so the full view never
// exceeds the terminal height; otherwise the renderer overflows the screen and
// the terminal auto-scrolls the frame.
func (m *Model) transcriptHeightForLayout() int {
	transcriptHeight := m.height - 3 - m.bottomReservedRows()
	if m.regionChromeEnabled() {
		// The activity heading and composer border use three additional rows.
		transcriptHeight -= 3
	}
	return max(transcriptHeight, minTranscriptHeight)
}

// bottomReservedRows returns the rows the bottom sections occupy below the
// transcript. A modal replaces the composer, popups, and working indicator;
// otherwise those sections are reserved together.
func (m *Model) bottomReservedRows() int {
	if m == nil {
		return defaultComposerHeight
	}
	if modal := m.renderModal(); modal != "" {
		return len(strings.Split(strings.TrimRight(modal, "\n"), "\n"))
	}
	rows := defaultComposerHeight
	if working := m.renderWorkingIndicator(); working != "" {
		rows += len(strings.Split(strings.TrimRight(working, "\n"), "\n"))
	}
	return rows + m.popupRowCount()
}

// popupRowCount returns the number of rows the slash and skill popups
// currently occupy in the rendered view.
func (m *Model) popupRowCount() int {
	return m.slashPopupRows() + m.skillPopupRows()
}

// syncTranscriptHeight recomputes the transcript height for the current popup
// state, which can change between window resizes.
func (m *Model) syncTranscriptHeight() {
	if m == nil {
		return
	}
	m.transcript.Height = m.transcriptHeightForLayout()
}

func (m *Model) transcriptRenderCached(state *codextui.State, width int) string {
	if state == nil {
		return "No messages yet."
	}
	return renderTranscriptWithCache(&m.transcriptMessages, state, m.rawOutput, width, m.activeTUITheme(), false, m.sessionCWD)
}

func (m *Model) refreshTranscript() {
	if m == nil {
		return
	}
	m.transcript.Height = m.transcriptHeightForLayout()
	content := m.transcriptRenderCached(m.State, m.transcript.Width)
	if content == m.lastTranscriptContent {
		if m.activityFollow && m.transcript.Height != m.lastTranscriptHeight {
			m.transcript.GotoBottom()
		}
		m.lastTranscriptHeight = m.transcript.Height
		return
	}
	m.lastTranscriptContent = content
	wasAtBottom := m.activityFollow || m.transcript.AtBottom()
	yOffset := m.transcript.YOffset
	m.transcript.SetContent(content)
	if wasAtBottom {
		m.transcript.GotoBottom()
	} else {
		m.transcript.SetYOffset(yOffset)
	}
	m.lastTranscriptHeight = m.transcript.Height
}

func (m *Model) openTranscriptOverlay() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	m.ensureSize()
	if m.overlay == nil {
		m.overlay = chatwidget.NewTranscriptOverlay(m.width, m.height, m.renderTranscriptOverlayCached())
		m.overlayTranscript = true
	} else {
		m.overlayTranscript = true
		m.syncTranscriptOverlay()
	}
	return m.openPagerTerminalMode()
}

func (m *Model) closeTranscriptOverlay() bubbletea.Cmd {
	if m == nil || m.overlay == nil {
		return nil
	}
	m.overlay = nil
	m.overlayTranscript = false
	// Restore the wheel-to-terminal behavior once the overlay is gone; mouse
	// tracking was never enabled, so the alternate screen is the only mode to
	// leave.
	m.setAlternateScroll(false)
	var commands []bubbletea.Cmd
	if m.overlayAltScreen {
		m.overlayAltScreen = false
		commands = append(commands, bubbletea.ExitAltScreen)
	}
	if len(commands) == 0 {
		return nil
	}
	return bubbletea.Batch(commands...)
}

func (m *Model) openPagerTerminalMode() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	// Rust parity: temporary overlay surfaces (Ctrl+T transcript pager) render
	// on the alternate screen and enable alternate scroll so the mouse wheel
	// scrolls the overlay content rather than the terminal's native scrollback.
	// Alternate scroll maps the wheel to up/down key presses, which the pager
	// keymap turns into scroll, so mouse tracking stays off and native text
	// selection/copy keeps working. `--no-alt-screen` keeps the overlay inline.
	m.setAlternateScroll(true)
	var commands []bubbletea.Cmd
	if !m.noAltScreen && !m.overlayAltScreen {
		m.overlayAltScreen = true
		commands = append(commands, bubbletea.EnterAltScreen)
	}
	if len(commands) == 0 {
		return nil
	}
	return bubbletea.Batch(commands...)
}

// setAlternateScroll toggles the terminal's alternate-scroll mode (DEC private
// mode 1007). When enabled, the terminal translates the mouse wheel into
// up/down key presses instead of scrolling the scrollback, letting the overlay
// scroll without capturing the mouse. The write is serialized through the pet
// runtime output mutex so it never interleaves with frame text.
func (m *Model) setAlternateScroll(enabled bool) {
	if m == nil || m.petRuntime == nil || m.petRuntime.out == nil {
		return
	}
	seq := "\x1b[?1007l"
	if enabled {
		seq = "\x1b[?1007h"
	}
	m.petRuntime.mu.Lock()
	defer m.petRuntime.mu.Unlock()
	_, _ = m.petRuntime.out.Write([]byte(seq))
}

func (m *Model) syncTranscriptOverlay() {
	if m == nil || m.overlay == nil {
		return
	}
	m.overlay.Resize(m.width, m.height)
	if !m.overlayTranscript {
		return
	}
	if content := m.renderTranscriptOverlayCached(); content != m.overlay.Content() {
		m.overlay.SetContent(content)
	}
}

// renderTranscriptOverlayCached renders the expanded transcript overlay
// content, reusing per-message display lines so a streaming tail does not
// re-render the whole history. It always recomputes the joined content so
// direct in-place message mutations are reflected even when the state revision
// was not bumped.
func (m *Model) renderTranscriptOverlayCached() string {
	if m == nil {
		return ""
	}
	return renderTranscriptWithCache(&m.overlayMessages, m.State, m.rawOutput, m.width, m.activeTUITheme(), true, m.sessionCWD)
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
		m.notice = "No agent response to copy"
		m.addErrorHistoryMessage(m.notice)
		m.refreshTranscript()
		return
	}
	if m.clipboardWrite == nil {
		m.notice = "Copy failed: clipboard is unavailable"
		m.addErrorHistoryMessage(m.notice)
		m.refreshTranscript()
		return
	}
	// Rust #39997: /copy opens a picker with the whole response plus each fenced
	// code block and blockquote so the user can copy an individual target.
	targets := chatwidget.CopyTargetsFromMarkdown(text)
	if len(targets) == 0 {
		m.notice = "No agent response to copy"
		m.addErrorHistoryMessage(m.notice)
		m.refreshTranscript()
		return
	}
	m.copyTargets = targets
	m.openSelectionViewModal(ModalKindGeneric, chatwidget.NewCopyTargetPickerView(text))
}

func (m *Model) applyCopyTargetModalOption(optionID string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	target, ok := chatwidget.CopyTargetForID(m.copyTargets, optionID)
	if !ok {
		m.notice = "Copy failed: selected target not found"
		m.addErrorHistoryMessage(m.notice)
		m.refreshTranscript()
		return nil
	}
	if m.clipboardWrite == nil {
		m.notice = "Copy failed: clipboard is unavailable"
		m.addErrorHistoryMessage(m.notice)
		m.refreshTranscript()
		return nil
	}
	if err := m.clipboardWrite(target.Text); err != nil {
		m.notice = "Copy failed: " + err.Error()
		m.addErrorHistoryMessage(m.notice)
		m.refreshTranscript()
		return nil
	}
	m.notice = "Copied " + target.Label + " to clipboard"
	m.addInfoHistoryMessage(m.notice)
	m.refreshTranscript()
	return nil
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
		m.addErrorHistoryMessage(m.notice)
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
	m.addInfoHistoryMessage(m.notice)
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
		text = "`/diff` \u2014 _not inside a git repository_"
	case strings.TrimSpace(text) == "":
		text = "No changes detected."
	}
	m.notice = "Diff"
	m.ensureSize()
	m.overlay = chatwidget.NewTranscriptOverlayWithTitle(m.width, m.height, text, "D I F F")
	m.overlayTranscript = false
	return m.openPagerTerminalMode()
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
	m.notice = "Stopping all background terminals."
	m.addInfoHistoryMessage(m.notice)
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
	m.activityFollow = m.transcript.AtBottom()
	return true
}

func (m *Model) applyInputHistoryKey(msg bubbletea.KeyMsg) bool {
	if m == nil || len(m.inputHistory) == 0 || m.modal != nil || m.slashPopup.Active || m.skillPopup.Active {
		return false
	}
	switch msg.Type {
	case bubbletea.KeyUp:
		if !m.inputHistoryActive {
			m.inputHistoryDraft = m.composer.Value()
			m.inputHistoryIndex = len(m.inputHistory)
			m.inputHistoryActive = true
		}
		if m.inputHistoryIndex > 0 {
			m.inputHistoryIndex--
		}
		m.composer.SetValue(m.inputHistory[m.inputHistoryIndex])
		m.composer.SetCursor(len(m.composer.Value()))
		return true
	case bubbletea.KeyDown:
		if !m.inputHistoryActive {
			return false
		}
		if m.inputHistoryIndex < len(m.inputHistory)-1 {
			m.inputHistoryIndex++
			m.composer.SetValue(m.inputHistory[m.inputHistoryIndex])
		} else {
			m.composer.SetValue(m.inputHistoryDraft)
			m.resetInputHistoryNavigation()
		}
		m.composer.SetCursor(len(m.composer.Value()))
		return true
	default:
		if m.inputHistoryActive && msg.Type != bubbletea.KeyShiftTab {
			m.resetInputHistoryNavigation()
		}
		return false
	}
}

// applyEditQueuedMessageKey restores the latest queued follow-up into the
// composer for editing, mirroring Rust 2bc43d516e (#38907 "Edit queued
// messages with Vim history-up"):
//
//   - the chat edit_queued_message binding (default alt-up / shift-left)
//     restores whenever queued follow-ups exist and no modal or popup is
//     active;
//   - in Vim normal mode, an empty composer's history-up binding (vim_normal
//     move_up, default k/up) triggers the same restore.
//
// The restored message is removed from the queue, so submitting the edited
// version replaces it instead of creating a duplicate (edit-and-requeue
// cycles keep a single queue entry). Normal history navigation is preserved
// when the composer has text (the empty-composer gate on the Vim path), and
// remapped bindings are honored through the resolved keymap.
func (m *Model) applyEditQueuedMessageKey(msg bubbletea.KeyMsg, keySpec string) bool {
	if m == nil || len(m.queued) == 0 || m.modal != nil || m.slashPopup.Active || m.skillPopup.Active {
		return false
	}
	editQueued := m.keyMatches("chat", "edit_queued_message", keySpec)
	vimHistoryUp := m.vimMode && !m.vimInsert && m.composer.Value() == "" && m.keyMatches("vim_normal", "move_up", keySpec)
	if !editQueued && !vimHistoryUp {
		return false
	}
	last := m.queued[len(m.queued)-1]
	m.queued = m.queued[:len(m.queued)-1]
	m.composer.SetValue(last.Request.Prompt)
	m.composer.SetCursor(len(last.Request.Prompt))
	m.attachments = cloneComposerAttachments(last.Request.Attachments)
	m.composerMentionBindings = append([]string(nil), last.Request.MentionBindings...)
	m.slashPopup = slashCommandPopup{}
	m.skillPopup = skillPopupState{}
	m.refreshTranscript()
	return true
}

func (m *Model) resetInputHistoryNavigation() {
	if m == nil {
		return
	}
	m.inputHistoryActive = false
	m.inputHistoryIndex = len(m.inputHistory)
	m.inputHistoryDraft = ""
}

func (m *Model) appendAssistantDelta(itemID string, delta string) {
	if m == nil || delta == "" {
		return
	}
	m.Transcript.appendAssistantDelta(m.State, itemID, delta, m.width)
}

func (m *Model) mergeAssistantFinal(text string) {
	if m == nil {
		return
	}
	m.Transcript.mergeAssistantFinal(m.State, text, m.width)
}

func (m *Model) insertFinalMessageSeparatorIfNeeded() {
	if m == nil || m.State == nil {
		return
	}
	m.Transcript.insertFinalMessageSeparatorIfNeeded(m.State, m.width)
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
	if len(m.pendingSteers) > 0 || len(m.rejectedSteers) > 0 || len(m.queued) > 0 {
		preview := bottompane.NewPendingInputPreview()
		preview.PendingSteers = make([]string, 0, len(m.pendingSteers))
		for _, pending := range m.pendingSteers {
			preview.PendingSteers = append(preview.PendingSteers, queuedSubmissionSummary(pending.Request))
		}
		preview.RejectedSteers = make([]string, 0, len(m.rejectedSteers))
		for _, rejected := range m.rejectedSteers {
			preview.RejectedSteers = append(preview.RejectedSteers, queuedSubmissionSummary(rejected.Request))
		}
		preview.QueuedMessages = make([]string, 0, len(m.queued))
		for _, queued := range m.queued {
			preview.QueuedMessages = append(preview.QueuedMessages, queuedSubmissionSummary(queued.Request))
		}
		lines = append(lines, preview.RenderLines(max(m.width-2, 4))...)
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
	return renderTranscriptWithHistoryMode(state, raw, width, themeID, false)
}

func renderTranscriptWithHistoryMode(state *codextui.State, raw bool, width int, themeID string, expandedHistory bool) string {
	return renderTranscriptWithCache(nil, state, raw, width, themeID, expandedHistory, "")
}

// renderTranscriptWithCache renders the transcript into display lines, reusing
// the per-message cache when a message's render inputs are unchanged. Passing a
// nil cache disables caching and renders the whole history from scratch.
func renderTranscriptWithCache(cache *transcriptMessageCache, state *codextui.State, raw bool, width int, themeID string, expandedHistory bool, cwd string) string {
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
	for i, message := range state.Messages {
		key := transcriptMessageKey{
			role:     message.Role,
			text:     message.Text,
			rawText:  message.RawText,
			width:    width,
			themeID:  themeID,
			raw:      raw,
			expanded: expandedHistory,
		}
		var lines []string
		if cache != nil && i < len(cache.messages) && cache.messages[i].key == key {
			lines = cache.messages[i].lines
		} else {
			lines = transcriptMessageDisplayLines(message, width, themeID, expandedHistory, cwd)
			if cache != nil {
				entry := transcriptMessageCacheEntry{key: key, lines: lines}
				if i < len(cache.messages) {
					cache.messages[i] = entry
				} else {
					cache.messages = append(cache.messages, entry)
				}
			}
		}
		if len(lines) == 0 {
			continue
		}
		if !first {
			builder.WriteString("\n\n")
		}
		builder.WriteString(strings.TrimRight(strings.Join(lines, "\n"), "\r\n"))
		first = false
	}
	if cache != nil && len(cache.messages) > len(state.Messages) {
		cache.messages = cache.messages[:len(state.Messages)]
	}
	if builder.Len() == 0 {
		return "No messages yet."
	}
	return builder.String()
}

func transcriptMessageDisplayLines(message codextui.Message, width int, themeID string, expandedHistory bool, cwd string) []string {
	if expandedHistory && message.Role == codextui.RoleHistory {
		text := strings.TrimRight(message.RawText, "\r\n")
		if strings.TrimSpace(text) == "" {
			text = strings.TrimRight(message.Text, "\r\n")
		}
		return codextui.ReflowTranscriptLines(rawLinesTrimmed(text), width)
	}
	return richMessageDisplayLines(message, width, themeID, cwd)
}

func richMessageDisplayLines(message codextui.Message, width int, themeID string, cwd string) []string {
	text := strings.TrimRight(message.Text, "\r\n")
	if message.Role == codextui.RoleAssistant {
		text = eventmap.StripHiddenAssistantMarkup(text, false)
	}
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
		renderText, _ := codextui.RewriteInlineVisualizations(text, nil)
		rendered, err := markdown.RenderWithThemeCwd(renderText, contentWidth, themeID, cwd)
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
