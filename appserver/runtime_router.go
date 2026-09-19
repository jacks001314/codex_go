package appserver

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"codex_go/agent"
	"codex_go/apps"
	"codex_go/auth"
	"codex_go/chatgptapi"
	"codex_go/codemode"
	"codex_go/compact"
	"codex_go/config"
	codexctx "codex_go/context"
	"codex_go/execserver"
	"codex_go/features"
	"codex_go/historynotes"
	"codex_go/install"
	"codex_go/mcp"
	"codex_go/model"
	"codex_go/network"
	"codex_go/otelinit"
	"codex_go/plugin"
	promptctx "codex_go/prompt"
	"codex_go/protocol"
	"codex_go/realtime"
	"codex_go/remotecontrol"
	"codex_go/review"
	"codex_go/rollout"
	"codex_go/runtimeutil"
	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/skillprovider"
	"codex_go/state"
	"codex_go/telemetry"
	"codex_go/tool"
	"codex_go/turn"
)

type RuntimeServices struct {
	ThreadRouter           *Router
	StateRuntime           *state.StateRuntime
	CloseStateRuntime      bool
	LogDBHandler           *state.LogDBHandler
	LogDBInstallation      *state.LogDBInstallation
	CloseLogDBInstallation bool
	ThreadExtras           *ThreadExtraService
	Realtime               *realtime.Manager
	FS                     *FSService
	Remote                 *remotecontrol.Manager
	Environment            *EnvironmentManager
	Windows                *sandbox.WindowsManager
	WindowsSetupRunner     WindowsSandboxSetupRunner
	Feedback               *FeedbackSnapshot
	Config                 *config.ConfigService
	Account                *auth.AccountManager
	AccountOAuthOptions    *auth.OAuthOptions
	Hooks                  *HookRegistry
	HooksDiscovery         *HookDiscoveryService
	HookRunner             *HookRunner
	Skills                 *SkillsService
	Plugins                *plugin.PluginService
	Models                 *model.ModelService
	Permissions            *sandbox.PermissionProfileService
	Collaboration          *CollaborationModeService
	MCP                    *mcp.MCPService
	Features               *features.FeatureService
	Apps                   *apps.AppService
	Turns                  *turn.TurnService
	SteerMailbox           *turn.SteerMailbox
	ThreadStatus           *ThreadStatusManager
	Agent                  model.AgentRunner
	GuardianReviewer       GuardianReviewer
	CompactRunner          compact.RemoteRunner
	ToolRouter             *tool.Router
	TurnRuntime            *turn.Runtime
	Reviews                *review.Service
	Misc                   *MiscService
	CommandExec            *CommandExecService
	Processes              *ProcessService
	ServerRequests         *ServerRequestBroker
	AccountHTTP            chatgptapi.HTTPDoer
	HTTPClient             model.HTTPDoer
	SpawnGraph             agent.Store
	Analytics              telemetry.TurnEventSink
	SkillShadowMetrics     SkillShadowMetricSink
	SkillInjectionMetrics  telemetry.MemoryUsageMetricSink
	TurnMetrics            telemetry.TurnMetricSink
	// VoiceMetrics receives the voice session lifecycle counters. The Go port
	// reports product counters from the app-server, which owns the session.
	VoiceMetrics                 telemetry.MemoryUsageMetricSink
	AnalyticsRPCTransport        telemetry.AppServerRPCTransport
	BrowserOpen                  func(string) error
	CustomSkills                 *skillprovider.Registry
	UnifiedExec                  *tool.UnifiedExecManager
	CodeModeProvider             tool.CodeModeRemoteProvider
	DisableCodeModeFallback      bool
	ManagedNetwork               *network.PreparedProxyManagedNetwork
	ManagedNetworkRequirements   *config.NetworkRequirements
	DefaultCWD                   string
	LocalEnvironmentEnabled      *bool
	WorkspaceCodexPluginsEnabled *bool
	WaitForEnvironmentToolConfig *tool.WaitForEnvironmentToolConfig
	ExternalAgentSessionImporter func(*config.ExternalAgentConfigImportParams) []config.ExternalAgentConfigImportTypeResult

	RemoteControlDisabledByRequirements bool

	// UserVerificationProvider installs the platform backend for the
	// experimental user-verification RPCs. Nil keeps the unsupported provider,
	// which reports providerUnavailable like a Rust build without a native
	// backend (Rust resolves the same seam through platform_provider).
	UserVerificationProvider UserVerificationProvider
}

func accountThreadUsageFromBackend(usage *chatgptapi.ThreadUsage) *auth.ThreadUsage {
	if usage == nil {
		return nil
	}
	groups := make([]auth.ThreadUsageBreakdownGroup, 0, len(usage.Groups))
	for _, group := range usage.Groups {
		groups = append(groups, auth.ThreadUsageBreakdownGroup{
			Model:                       cloneStringPtrAppserver(group.Model),
			ReasoningEffort:             cloneStringPtrAppserver(group.ReasoningEffort),
			Speed:                       cloneStringPtrAppserver(group.Speed),
			EstimatedUsageCreditsMicros: group.EstimatedUsageCreditsMicros,
			NetNewInputTokens:           cloneInt64PtrAppserver(group.NetNewInputTokens),
			CachedInputTokens:           cloneInt64PtrAppserver(group.CachedInputTokens),
			InputTokens:                 cloneInt64PtrAppserver(group.InputTokens),
			OutputTokens:                cloneInt64PtrAppserver(group.OutputTokens),
			TotalTokens:                 cloneInt64PtrAppserver(group.TotalTokens),
		})
	}
	return &auth.ThreadUsage{
		ThreadID:                    usage.ThreadID,
		EstimatedUsageCreditsMicros: usage.EstimatedUsageCreditsMicros,
		EstimatedUsageUSDMicros:     cloneInt64PtrAppserver(usage.EstimatedUsageUSDMicros),
		Groups:                      groups,
	}
}

func guardianTurnStart(params *turn.TurnStartParams) bool {
	if params == nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(params.Originator), "guardian") {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(params.ResponsesAPIMetadata["x-openai-subagent"]), "guardian")
}

func (r *RuntimeRouter) configureEnvironmentHTTPPolicy() {
	if r == nil || r.services.Environment == nil || r.services.Config == nil {
		return
	}
	read, err := r.services.Config.Read(&config.ConfigReadParams{})
	if err != nil || read == nil {
		return
	}
	cfg := &config.Config{Values: read.Config}
	client, _ := r.httpClientForConfig(cfg).(*http.Client)
	r.services.Environment.SetHTTPClient(client)
}

type SkillShadowMetricSink interface {
	Counter(name string, inc int, tags map[string]string)
	Histogram(name string, value int, tags map[string]string)
	RecordDuration(name string, duration time.Duration, tags map[string]string)
}

type RemoteControlStartupMode string

const (
	RemoteControlStartupResolvePersisted  RemoteControlStartupMode = "resolvePersisted"
	RemoteControlStartupDisabledEphemeral RemoteControlStartupMode = "disabledEphemeral"
	RemoteControlStartupEnabledEphemeral  RemoteControlStartupMode = "enabledEphemeral"
)

type RuntimeRouterOptions struct {
	RemoteControlStartupMode            RemoteControlStartupMode
	Requirements                        *config.ConfigRequirements
	RemoteControlDisabledByRequirements bool
	AnalyticsDefaultEnabled             bool
	RemoteControlURL                    string
	RemoteControlInstallationID         string
	RemoteControlEnrollmentStore        *remotecontrol.EnrollmentStore
	RemoteControlAuthLoader             remotecontrol.RemoteControlAuthLoader
	RemoteControlAuthRecovery           remotecontrol.RemoteControlAuthRecovery
	RemoteControlServerAPIOptions       *remotecontrol.ServerAPIOptions
	RemoteControlAppServerClientName    *string
	RemoteControlBackendEnabled         bool
	CodeModeHostURL                     string
	CodeModeHostHTTPClient              *http.Client
	CodeModeHostEnabled                 bool
	CodeModeHostProgram                 string
	DisableCodeModeInProcessFallback    bool
	FeatureEnablement                   map[string]bool
	StateRuntime                        *state.StateRuntime
	EnableLogDB                         bool
	logDBInstallation                   *state.LogDBInstallation
	closeStateRuntimeOnRouterClose      bool
	closeLogDBInstallationOnRouterClose bool
}

type RuntimeRouter struct {
	services              RuntimeServices
	config                *config.ConfigService
	threads               *ThreadManager
	servicesMu            sync.Mutex
	mu                    sync.RWMutex
	configWarningsMu      sync.Mutex
	emittedConfigWarnings map[string]bool
	sink                  NotificationSink
	requests              ServerRequestSink
	turnsMu               *sync.Mutex
	active                map[string]*activeRuntimeTurn
	diffs                 map[string]*runtimeutil.DiffTracker
	ephemeralMu           *sync.RWMutex
	ephemeralThreads      map[string]*session.Record
	subscriptionsMu       *sync.Mutex
	threadSubscriptions   map[string]map[string]struct{}
	clientInfoMu          sync.RWMutex
	clientInfo            map[string]ClientInfo
	notificationOptOut    map[string]map[NotificationMethod]struct{}
	experimentalAPI       map[string]bool
	diagnosticsGauges     *serverDiagnosticsGaugeRegistry
	requestAttestation    map[string]bool
	mcpOpenAIForm         map[string]bool
	mcpStandardFormInput  map[string]bool
	// mcpToolApprovalsMu guards mcpToolApprovals, the per-thread store of
	// session-remembered custom MCP tool approvals (Rust
	// session.tool_approvals).
	mcpToolApprovalsMu sync.Mutex
	mcpToolApprovals   map[string]map[mcp.MCPToolApprovalKey]bool
	authRevisionMu     sync.Mutex
	authRevision       uint64
	// authOwnerRevision is the ownership half of Rust's AuthChangeState
	// (#43428): it advances only when the credential owner (auth mode or the
	// ChatGPT user/workspace pair) changes, unlike authRevision which advances
	// on any refresh-relevant credential change.
	authOwnerRevision uint64
	authChangeTracker *auth.AuthChangeTracker
	authChanged       chan struct{}
	// workspaceRouting caches workspace routing discovery for account/read
	// (Rust AccountRequestProcessor::workspace_routing).
	workspaceRoutingOnce sync.Once
	workspaceRouting     *workspaceRoutingState
	// otelProvider is the process OTEL provider built from config at startup.
	// It forwards the task metrics, the exported log records, and the request
	// spans, and is shut down with the router.
	otelProviderMu sync.RWMutex
	otelProvider   *telemetry.OtelProvider
	// otelReloadStop ends the account-change provider reloader at close.
	otelReloadMu   sync.Mutex
	otelReloadStop chan struct{}
	// requestTransport is the transport name the request span reports; the
	// stdio, unix-socket, and websocket servers install their own name and an
	// in-process router keeps the "in-process" default.
	requestTransport string
	// requestSpans holds the open request span per (connection, request) while a
	// request is being dispatched, the way Rust's OutgoingMessageSender keeps a
	// RequestContext per request id. The turn start copies its W3C trace context
	// into the turn so the model request continues the caller's trace.
	requestSpansMu sync.Mutex
	requestSpans   map[requestSpanKey]*telemetry.Span
	// mcpCallSpans holds the `mcp.tools.call` span of every in-flight MCP tool
	// call, keyed by thread, turn, and call id.
	mcpCallSpansMu sync.Mutex
	mcpCallSpans   map[mcpToolCallSpanKey]*telemetry.Span
	// threadSessionSpans holds the thread-lifetime `session_loop` span of every
	// live session, keyed by thread id. Rust opens it around the session's
	// submission loop (session/mod.rs), so every turn's spans nest under it.
	threadSessionSpansMu    sync.Mutex
	threadSessionSpans      map[string]*telemetry.Span
	mcpEventStreams         *mcpEventStreamManager
	skillShadowMu           sync.Mutex
	skillShadowState        map[string]*skillShadowThreadState
	startupPrewarmMu        sync.Mutex
	startupPrewarms         map[string]*startupPrewarmState
	mcpRuntimes             *mcpRuntimeCoordinator
	mcpConfigManaged        atomic.Bool
	loginRuntimeMu          sync.Mutex
	loginRuntimeCancels     map[string]context.CancelFunc
	approvalSessionsMu      sync.RWMutex
	commandApprovals        map[string]struct{}
	fileApprovals           map[string]struct{}
	serverRequestGuardsMu   sync.Mutex
	serverRequestGuards     map[string]*ThreadStatusActiveGuard
	executedToolCallsMu     sync.Mutex
	executedToolCalls       map[string]*turn.ExecutedToolCallRecorder
	newContextWindowMu      sync.Mutex
	newContextWindowReq     map[string]bool
	contextWindowMu         sync.Mutex
	contextWindowIDs        map[string]string
	windowNumbers           map[string]uint64
	codeModeRuntimesMu      sync.Mutex
	codeModeRuntimes        map[string]*tool.CodeModeRuntime
	pendingGoalMu           sync.Mutex
	pendingGoalByConn       map[string][]*Notification
	deferredGoalMode        atomic.Bool
	toolItemReviews         map[string]toolItemReviewSummary
	skillMCPPromptMu        sync.Mutex
	skillMCPPrompted        map[string]struct{}
	orchestratorSkillMu     sync.Mutex
	orchestratorSkills      map[string]*runtimeOrchestratorSkillCatalog
	orchestratorWarned      map[string]bool
	skillWarningsMu         sync.Mutex
	skillWarnings           map[string]map[string]struct{}
	selectedSkillMu         sync.Mutex
	selectedSkills          map[string]map[string]*runtimeSelectedSkillCatalog
	unifiedExecPersistMu    sync.Mutex
	unifiedExecPendingMu    sync.Mutex
	unifiedExecPending      map[string][]session.Item
	unifiedExecAnalyticsMu  sync.Mutex
	unifiedExecAnalytics    map[string]unifiedExecAnalyticsContext
	networkApproval         *networkApprovalService
	execPolicySaved         *execPolicySavedState
	managedNetworkReloadMu  sync.Mutex
	startupErr              error
	managedNetworkReload    *managedNetworkReloadWatcher
	managedNetworksMu       sync.Mutex
	managedNetworks         map[string]*network.PreparedProxyManagedNetwork
	managedNetworkInputs    map[string]managedNetworkReloadInput
	goalAccountingMu        sync.Mutex
	goalAccountingTurns     map[string]stateGoalTurnSnapshot
	goalTurnUsage           map[string]model.AgentUsage
	execFailureTurns        map[string]goalExecutionFailureState
	emptyResponseTurns      map[string]goalEmptyResponseState
	descendantTokenUsage    map[string]int64
	lastAccountedDescendant map[string]int64
	goalProgressMu          sync.Mutex
	goalIdleMu              sync.Mutex
	goalIdleGoalID          string
	goalIdleLastAccounted   time.Time
	goalStateMu             sync.Mutex
	externalSessionSyncMu   sync.Mutex
	memoryStartupMu         sync.Mutex
	memoryStartupClosing    bool
	memoryStartupCtx        context.Context
	memoryStartupCancel     context.CancelFunc
	memoryStartupWG         sync.WaitGroup
	realtimeOpsMu           sync.Mutex
	realtimeOpsClosing      bool
	realtimeOpsCtx          context.Context
	realtimeOpsCancel       context.CancelFunc
	realtimeOpsWG           sync.WaitGroup
	realtimeOpsQueues       map[string]chan func(context.Context)
	realtimeEventMu         sync.Mutex
	realtimeEventLocks      map[string]*sync.Mutex
	// voiceDurationRecorded deduplicates the voice session duration metric,
	// which is recorded once per completed session.
	voiceDurationRecorded  map[string]bool
	internalMemoryThreads  sync.Map
	sessionEndMu           sync.Mutex
	sessionEnded           map[string]struct{}
	agentRegistry          *agent.Registry
	agentRegistryMu        sync.Mutex
	agentRegistries        map[string]*agent.Registry
	agentActivityMu        sync.Mutex
	agentActivity          map[string]chan string
	agentMessagesMu        sync.Mutex
	agentMessages          map[string][]any
	nodeReplEvidenceMu     sync.Mutex
	nodeReplEvidence       map[string]*codexctx.NodeReplReviewEvidence
	rolloutBudgetOnce      sync.Once
	rolloutBudget          *runtimeutil.Budget
	rolloutBudgetCharged   atomic.Bool
	rolloutBudgetExhausted atomic.Bool
	reasoningEffortMu      sync.Mutex
	reasoningEffortPins    map[string]reasoningEffortPin
	closeOnce              sync.Once
	closeErr               error
	codexHomeScanCancel    func()
}

type unifiedExecAnalyticsContext struct {
	ConnectionID string
	RunConfig    *appTurnRunConfig
}

type runtimeOrchestratorSkillCatalog struct {
	once          sync.Once
	catalog       turn.OrchestratorSkillCatalog
	err           error
	resourceMu    sync.Mutex
	resources     map[string]string
	resourceBytes int
}

type runtimeSelectedSkillCatalog struct {
	once     sync.Once
	entries  []SkillsListEntry
	warnings []string
	err      error
}

const runtimeSeedRolloutExtraKey = "runtime_seed_rollout"
const remoteControlExternalAuthRecoveryTimeout = 30 * time.Second

// rolloutBudgetForSession lazily resolves and configures the shared rollout
// budget from `features.rollout_budget` (mirrors Rust's per-root-thread-session
// RolloutBudget configured from resolve_rollout_budget_config). Returns nil
// when the feature is disabled, the config is absent, or resolution fails.
func (r *RuntimeRouter) rolloutBudgetForSession() *runtimeutil.Budget {
	if r == nil {
		return nil
	}
	r.rolloutBudgetOnce.Do(func() {
		if r.services.Config == nil {
			return
		}
		read, err := r.services.Config.Read(&config.ConfigReadParams{})
		if err != nil {
			return
		}
		resolved, err := (&config.Config{Values: read.Config}).RolloutBudgetConfig()
		if err != nil || resolved == nil {
			return
		}
		budget := runtimeutil.NewBudget()
		budget.Configure(runtimeutil.BudgetConfig{
			LimitTokens:               resolved.LimitTokens,
			PrefillTokenWeight:        resolved.PrefillTokenWeight,
			SamplingTokenWeight:       resolved.SamplingTokenWeight,
			ReminderAtRemainingTokens: resolved.ReminderAtRemainingTokens,
		})
		r.rolloutBudget = budget
	})
	return r.rolloutBudget
}

// rolloutBudgetReminderMessage builds the developer-context reminder injected
// into the model when a rollout-budget threshold is crossed, mirroring Rust's
// RolloutBudgetContext (core/src/context/rollout_budget.rs) with its
// `<rollout_budget>` markers and exact body wording.
func rolloutBudgetReminderMessage(remainingTokens int64) any {
	return model.DeveloperMessageInputItem(fmt.Sprintf(
		"<rollout_budget>\nYou have %d weighted tokens left in the shared session token budget.\n</rollout_budget>",
		remainingTokens,
	))
}

// rolloutBudgetFollowUp returns a SamplingFollowUp that charges each completed
// model response against the shared rollout budget (mirroring Rust
// Session::record_rollout_budget_usage) and injects the `<rollout_budget>`
// developer reminder when a remaining-tokens threshold is crossed (mirroring
// Rust maybe_record_reminder, core/src/session/rollout_budget.rs). Exhaustion
// is recorded on the router and surfaced as ErrSessionBudgetExceeded at turn
// completion (Go's agent loop cannot abort a sampling step mid-loop).
func (r *RuntimeRouter) rolloutBudgetFollowUp(threadID string) turn.SamplingFollowUp {
	return func(ctx *turn.SamplingFollowUpContext) []any {
		if ctx == nil {
			return nil
		}
		budget := r.rolloutBudgetForSession()
		if budget == nil {
			return nil
		}
		r.rolloutBudgetCharged.Store(true)
		exhausted, err := budget.RecordUsage(runtimeutil.TokenUsage{
			InputTokens:             ctx.Usage.InputTokens,
			CachedInputTokens:       ctx.Usage.CachedInputTokens,
			OutputTokens:            ctx.Usage.OutputTokens,
			CodexRolloutBudgetUnits: ctx.Usage.CodexRolloutBudgetUnits,
		})
		if err != nil {
			return nil
		}
		if exhausted {
			r.rolloutBudgetExhausted.Store(true)
		}
		windowID := threadID
		reminder := budget.PendingReminder(threadID, windowID)
		if reminder == nil {
			return nil
		}
		budget.MarkReminderDelivered(threadID, windowID, *reminder)
		if item := rolloutBudgetReminderMessage(reminder.RemainingTokens); item != nil {
			return []any{item}
		}
		return nil
	}
}

func NewRuntimeRouter(services RuntimeServices) *RuntimeRouter {
	if services.SpawnGraph == nil && services.StateRuntime != nil {
		services.SpawnGraph = &stateSpawnGraph{runtime: services.StateRuntime}
	}
	configService := services.Config
	if configService == nil {
		// Keep the fallback private to the router: a nil RuntimeServices.Config
		// is meaningful to feature-specific services, while config RPC methods
		// still need a stable default service.
		configService = config.NewConfigService("")
	}
	if services.ThreadExtras == nil {
		// Thread settings are read by background MCP prewarm workers, so publish
		// their owner during construction rather than lazily from a request.
		services.ThreadExtras = NewThreadExtraService()
	}
	threads := NewThreadManager(services.ThreadStatus)
	if services.ThreadRouter != nil {
		threads = services.ThreadRouter.threadManager()
		threads.SetStatusManager(services.ThreadStatus)
	}
	// ThreadManager is the single owner of thread-status state. Keep the
	// compatibility service pointer as an immutable constructor snapshot.
	services.ThreadStatus = threads.StatusManager()
	memoryStartupCtx, memoryStartupCancel := context.WithCancel(context.Background())
	realtimeOpsCtx, realtimeOpsCancel := context.WithCancel(context.Background())
	router := &RuntimeRouter{
		services:                services,
		config:                  configService,
		threads:                 threads,
		turnsMu:                 &threads.turnsMu,
		active:                  threads.active,
		diffs:                   threads.diffs,
		ephemeralMu:             &threads.ephemeralMu,
		ephemeralThreads:        threads.ephemeral,
		subscriptionsMu:         &threads.subscriptionsMu,
		threadSubscriptions:     threads.subscriptions,
		clientInfo:              map[string]ClientInfo{},
		notificationOptOut:      map[string]map[NotificationMethod]struct{}{},
		experimentalAPI:         map[string]bool{},
		diagnosticsGauges:       newServerDiagnosticsGaugeRegistry(),
		requestAttestation:      map[string]bool{},
		mcpOpenAIForm:           map[string]bool{},
		mcpStandardFormInput:    map[string]bool{},
		mcpRuntimes:             newMCPRuntimeCoordinator(),
		authChanged:             make(chan struct{}),
		authChangeTracker:       auth.NewAuthChangeTracker(nil),
		mcpEventStreams:         newMCPEventStreamManager(),
		commandApprovals:        map[string]struct{}{},
		fileApprovals:           map[string]struct{}{},
		serverRequestGuards:     map[string]*ThreadStatusActiveGuard{},
		executedToolCalls:       map[string]*turn.ExecutedToolCallRecorder{},
		codeModeRuntimes:        map[string]*tool.CodeModeRuntime{},
		toolItemReviews:         map[string]toolItemReviewSummary{},
		skillMCPPrompted:        map[string]struct{}{},
		orchestratorSkills:      map[string]*runtimeOrchestratorSkillCatalog{},
		orchestratorWarned:      map[string]bool{},
		skillWarnings:           map[string]map[string]struct{}{},
		selectedSkills:          map[string]map[string]*runtimeSelectedSkillCatalog{},
		unifiedExecPending:      map[string][]session.Item{},
		unifiedExecAnalytics:    map[string]unifiedExecAnalyticsContext{},
		managedNetworks:         map[string]*network.PreparedProxyManagedNetwork{},
		managedNetworkInputs:    map[string]managedNetworkReloadInput{},
		goalAccountingTurns:     map[string]stateGoalTurnSnapshot{},
		goalTurnUsage:           map[string]model.AgentUsage{},
		execFailureTurns:        map[string]goalExecutionFailureState{},
		emptyResponseTurns:      map[string]goalEmptyResponseState{},
		descendantTokenUsage:    map[string]int64{},
		lastAccountedDescendant: map[string]int64{},
		memoryStartupCtx:        memoryStartupCtx,
		memoryStartupCancel:     memoryStartupCancel,
		realtimeOpsCtx:          realtimeOpsCtx,
		realtimeOpsCancel:       realtimeOpsCancel,
		realtimeOpsQueues:       map[string]chan func(context.Context){},
		realtimeEventLocks:      map[string]*sync.Mutex{},
		agentRegistry:           agent.NewRegistry(),
		agentRegistries:         map[string]*agent.Registry{},
		agentActivity:           map[string]chan string{},
		agentMessages:           map[string][]any{},
	}
	if router.services.ServerRequests == nil {
		router.services.ServerRequests = NewServerRequestBroker()
	}
	router.networkApproval = newNetworkApprovalService(router)
	router.execPolicySaved = newExecPolicySavedState()
	if router.services.UnifiedExec == nil {
		router.services.UnifiedExec = tool.NewUnifiedExecManager()
	}
	router.services.UnifiedExec.SetWriteStdinApproval(router.writeStdinApproval)
	router.services.ServerRequests.SetRequestedCallback(router.noteServerRequestPending)
	router.services.ServerRequests.SetResolvedCallback(router.notifyServerRequestResolved)
	router.services.ServerRequests.SetResolvedResponseCallback(router.handleServerRequestResolvedResponse)
	if router.services.ThreadRouter != nil && router.services.SpawnGraph != nil {
		router.services.ThreadRouter.SetSpawnGraph(router.services.SpawnGraph)
	}
	if router.services.ThreadRouter != nil {
		router.services.ThreadRouter.SetStateRuntime(router.services.StateRuntime)
	}
	if router.services.ThreadRouter != nil && router.services.Config != nil {
		router.services.ThreadRouter.retainClientDeveloperMessages = func() bool {
			read, err := router.services.Config.Read(&config.ConfigReadParams{})
			if err != nil || read == nil || read.Config == nil {
				return false
			}
			return features.Enabled((&config.Config{Values: read.Config}).FeatureSettings(), "retain_client_developer_messages")
		}
	}
	router.configureFSChangedCallback()
	if router.services.Skills != nil {
		router.services.Skills.SetChangedCallback(func() {
			router.notify(NotificationSkillsChanged, &SkillsChangedNotification{})
		})
	}
	return router
}

func (r *RuntimeRouter) configureFSChangedCallback() {
	if r == nil || r.services.FS == nil {
		return
	}
	r.services.FS.SetChangedCallback(func(connectionID string, notification *ChangedNotification) {
		r.notifyToConnection(connectionID, NotificationFSChanged, notification)
	})
}

func (r *RuntimeRouter) SetNotificationSink(sink NotificationSink) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.sink = sink
	r.mu.Unlock()
	r.requireRealtime().SetNotificationSink(func(notification realtime.Notification) {
		r.notifyRealtime([]realtime.Notification{notification})
	})
	r.requireRealtime().SetEventSink(r.handleRealtimeEvent)
}

func (r *RuntimeRouter) SetServerRequestSink(sink ServerRequestSink) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.requests = sink
	r.mu.Unlock()
	r.requireServerRequests().SetSink(sink)
}

func (r *RuntimeRouter) notify(method NotificationMethod, params any) {
	if r == nil {
		return
	}
	if r.isInternalMemoryNotification(params) {
		return
	}
	// Rust #41416: omit inline image/audio content from app-server item
	// notifications when the feature is enabled, keeping it in model input.
	if isOmitMediaNotificationMethod(method) && r.notificationMediaFilterEnabled() {
		params = withoutNotificationMedia(params)
	}
	r.handleNotificationAnalytics(method, params)
	if r.notificationMethodOptedOut(method) {
		return
	}
	r.mu.RLock()
	sink := r.sink
	r.mu.RUnlock()
	if sink != nil {
		sink.Notify(NewNotification(method, params))
	}
}

// notificationMediaFilterEnabled resolves the app-server-wide
// omit_app_server_notification_media feature (Rust #41416).
func (r *RuntimeRouter) notificationMediaFilterEnabled() bool {
	if r == nil || r.services.Config == nil {
		return false
	}
	read, err := r.services.Config.Read(&config.ConfigReadParams{})
	if err != nil || read == nil {
		return false
	}
	cfg := &config.Config{Values: read.Config}
	return features.Enabled(cfg.FeatureSettings(), "omit_app_server_notification_media")
}

func (r *RuntimeRouter) notifyTurnCompletedOnce(notification *TurnCompletedNotification) bool {
	if r == nil || notification == nil || strings.TrimSpace(notification.ThreadID) == "" || strings.TrimSpace(notification.Turn.ID) == "" {
		return false
	}
	if !r.threads.ClaimTerminal(notification.ThreadID, notification.Turn.ID) {
		return false
	}
	r.requireRealtime().CompleteHandoff(notification.ThreadID)
	r.notify(NotificationTurnCompleted, notification)
	return true
}

func (r *RuntimeRouter) notifyToConnection(connectionID string, method NotificationMethod, params any) {
	if r == nil {
		return
	}
	if r.isInternalMemoryNotification(params) {
		return
	}
	connectionID = normalizeConnectionID(connectionID)
	if strings.TrimSpace(connectionID) == "" {
		r.notify(method, params)
		return
	}
	r.handleNotificationAnalytics(method, params)
	if r.connectionNotificationMethodOptedOut(connectionID, method) {
		return
	}
	r.mu.RLock()
	sink := r.sink
	r.mu.RUnlock()
	if sink == nil {
		return
	}
	notification := NewNotification(method, params)
	if targeted, ok := sink.(TargetedNotificationSink); ok {
		targeted.NotifyToConnection(connectionID, notification)
		return
	}
	sink.Notify(notification)
}

func (r *RuntimeRouter) authRevisionSnapshot(context.Context) (uint64, error) {
	if r == nil {
		return 0, nil
	}
	r.authRevisionMu.Lock()
	defer r.authRevisionMu.Unlock()
	return r.authRevision, nil
}

func (r *RuntimeRouter) noteAuthChanged() {
	if r == nil {
		return
	}
	// Track ownership separately from the monotonic revision so consumers can
	// drop cached credentials/connections only when the account identity
	// changes (Rust AuthChangeState, #43428). Reading the snapshot before
	// taking authRevisionMu keeps the two locks independent.
	snapshot := r.requireAccount().AuthSnapshot()
	state := r.authChangeTracker.NoteAuth(snapshot)
	r.authRevisionMu.Lock()
	r.authRevision++
	ownerChanged := state.OwnerGeneration != r.authOwnerRevision
	r.authOwnerRevision = state.OwnerGeneration
	close(r.authChanged)
	r.authChanged = make(chan struct{})
	r.authRevisionMu.Unlock()
	if ownerChanged {
		// Credential changes keep the cached model client, but a new account
		// owner must not reuse its websocket connection or routing state
		// (Rust #44489).
		r.resetAuthOwnedModelState()
	}
	if r.mcpRuntimes != nil {
		r.mcpRuntimes.invalidateAll()
	}
	r.prewarmLoadedMCPThreads()
}

// resetAuthOwnedModelState drops a cached responses agent built by this router
// (so the next turn rebuilds it from the current credential snapshot) and
// clears the current agent's auth-owned websocket/routing caches. Injected or
// custom agent runners are left in place: they own their credential lifecycle
// and are not ours to discard.
func (r *RuntimeRouter) resetAuthOwnedModelState() {
	if r == nil {
		return
	}
	r.servicesMu.Lock()
	agent := r.services.Agent
	if _, builtByRouter := agent.(*model.ResponsesAgentRunner); builtByRouter {
		r.services.Agent = nil
	}
	r.servicesMu.Unlock()
	if resettable, ok := agent.(interface{ ResetAuthOwnedCaches() }); ok && resettable != nil {
		resettable.ResetAuthOwnedCaches()
	}
}

// authOwnerRevisionSnapshot returns the credential-ownership revision, which
// advances only on login, logout, or a change of user, workspace, or auth mode
// (Rust AuthChangeState.owner_generation).
func (r *RuntimeRouter) authOwnerRevisionSnapshot() uint64 {
	if r == nil {
		return 0
	}
	r.authRevisionMu.Lock()
	defer r.authRevisionMu.Unlock()
	return r.authOwnerRevision
}

// remoteControlAuthRevision is the login-lifetime revision the remote-control
// websocket loop watches (Rust #44341). Unlike authRevision it does not advance
// on a same-owner token refresh, so a refresh preserves the live relay
// connection (and its reconnect backoff) instead of churning it; logout, login,
// and identity changes still advance it and wake the loop.
func (r *RuntimeRouter) remoteControlAuthRevision(context.Context) (uint64, error) {
	return r.authOwnerRevisionSnapshot(), nil
}

func (r *RuntimeRouter) authChangedChannel() <-chan struct{} {
	if r == nil {
		return nil
	}
	r.authRevisionMu.Lock()
	defer r.authRevisionMu.Unlock()
	return r.authChanged
}

func (r *RuntimeRouter) notifyRemoteControlStatusChanged(notification *remotecontrol.StatusChangedNotification) {
	if notification == nil {
		return
	}
	r.notify(NotificationRemoteControlStatusChanged, RemoteControlStatusChangedFromManager(notification))
}

func (r *RuntimeRouter) initializeRemoteControlStatusNotification() *Notification {
	if r == nil || r.notificationMethodOptedOut(NotificationRemoteControlStatusChanged) {
		return nil
	}
	return NewRemoteControlStatusChangedNotification(r.requireRemote().StatusChanged())
}

func (r *RuntimeRouter) resolveServerResponse(response *Response) bool {
	if r == nil || response == nil || response.ID.IsZero() {
		return false
	}
	resolved, _ := r.requireServerRequests().Resolve(response)
	return resolved
}

func (r *RuntimeRouter) notifyServerRequestResolved(request *ServerRequest) {
	if r == nil || request == nil {
		return
	}
	threadID := serverRequestThreadID(request)
	if threadID == "" {
		return
	}
	if guard := r.takeServerRequestGuard(request.ID); guard != nil {
		r.notifyThreadStatus(guard.Release())
	}
	r.notify(NotificationServerRequestResolved, &ServerRequestResolvedNotification{
		ThreadID:  threadID,
		RequestID: request.ID,
	})
}

func (r *RuntimeRouter) noteServerRequestPending(request *ServerRequest) {
	if r == nil || request == nil || request.ID.IsZero() {
		return
	}
	threadID := serverRequestThreadID(request)
	if threadID == "" {
		return
	}
	var guard *ThreadStatusActiveGuard
	var notification *ThreadStatusNotification
	switch request.Method {
	case ServerRequestCommandExecutionApproval, ServerRequestFileChangeApproval, ServerRequestPermissionsApproval,
		ServerRequestApplyPatchApproval, ServerRequestExecCommandApproval:
		guard, notification = r.services.ThreadStatus.NotePermissionRequestedWithNotification(threadID)
	case ServerRequestToolUserInput:
		guard, notification = r.services.ThreadStatus.NoteUserInputRequestedWithNotification(threadID)
	default:
		return
	}
	if guard == nil {
		return
	}
	r.serverRequestGuardsMu.Lock()
	if previous := r.serverRequestGuards[request.ID.String()]; previous != nil {
		previous.Release()
	}
	r.serverRequestGuards[request.ID.String()] = guard
	r.serverRequestGuardsMu.Unlock()
	r.notifyThreadStatus(notification)
}

func (r *RuntimeRouter) takeServerRequestGuard(requestID RequestID) *ThreadStatusActiveGuard {
	if r == nil || requestID.IsZero() {
		return nil
	}
	r.serverRequestGuardsMu.Lock()
	defer r.serverRequestGuardsMu.Unlock()
	guard := r.serverRequestGuards[requestID.String()]
	delete(r.serverRequestGuards, requestID.String())
	return guard
}

func (r *RuntimeRouter) authStore(codexHome string) *auth.Store {
	return auth.NewStoreWithOptions(codexHome, r.authStoreOptions())
}

func (r *RuntimeRouter) authStoreOptions() *auth.StoreOptions {
	if r == nil || r.services.Config == nil {
		return auth.StoreOptionsFromConfig("", false)
	}
	read, err := r.services.Config.Read(&config.ConfigReadParams{})
	if err != nil || read == nil {
		return auth.StoreOptionsFromConfig("", false)
	}
	cfg := &config.Config{Values: read.Config}
	options := auth.StoreOptionsFromConfig(cfg.CLIAuthCredentialsStoreMode(), cfg.SecretAuthStorageEnabled())
	options.WorkloadIdentity = &auth.WorkloadIdentityAuthOptions{
		ChatGPTBaseURL: cfg.ChatGPTBaseURL(),
	}
	return options
}

// accountScopedModelsManager builds a lazily-resolved account-scoped model
// catalog manager for the app-server (Rust #41467 "Refresh the TUI model picker
// from the app server"). The underlying RemoteModelsManager is constructed only
// on first use, once the auth store is resolvable. The config catalog is the
// manager's base so a failed refresh preserves configured models; a ChatGPT
// account treats the fetched account catalog as the source of truth. Any
// auth/config error keeps the bundled static catalog (the LazyModelsManager
// fallback), matching the previous NewModelService(nil) behavior.
func accountScopedModelsManager(codexHome string, configService *config.ConfigService) model.ModelsManager {
	build := func() (model.ModelsManager, error) {
		read, err := configService.Read(&config.ConfigReadParams{})
		if err != nil || read == nil {
			return nil, err
		}
		return buildAccountScopedModelsManager(codexHome, read, configService.Requirements())
	}
	return model.NewLazyModelsManager(build)
}

// accountScopedCatalogProvider is the provider/auth state both the catalog
// manager and its identity are derived from.
type accountScopedCatalogProvider struct {
	providerID   string
	providerInfo *model.ProviderInfo
	apiProvider  *model.APIProvider
	authHeaders  *model.AuthHeaders
	authSnapshot *auth.AuthDotJSON
	residency    string
}

// resolveAccountScopedCatalogProvider reads the effective provider and
// credentials the account-scoped catalog uses, resolving lazy command
// credentials the same way Rust does before comparing catalog identities
// (#46508).
func resolveAccountScopedCatalogProvider(codexHome string, read *config.ConfigReadResponse, requirements *config.ConfigRequirementsReadResponse) (*accountScopedCatalogProvider, error) {
	if read == nil {
		return nil, errors.New("config read is required")
	}
	cfg := &config.Config{Values: read.Config}
	providerID := strings.TrimSpace(stringFromMap(read.Config, "model_provider"))
	providerInfo, err := model.ProviderForConfigID(read.Config, providerID, strings.TrimSpace(stringFromMap(read.Config, "openai_base_url")))
	if err != nil || providerInfo == nil {
		return nil, err
	}
	storeOptions := auth.StoreOptionsFromConfig(cfg.CLIAuthCredentialsStoreMode(), cfg.SecretAuthStorageEnabled())
	storeOptions.WorkloadIdentity = &auth.WorkloadIdentityAuthOptions{ChatGPTBaseURL: cfg.ChatGPTBaseURL()}
	resolved, err := auth.NewStoreWithOptions(codexHome, storeOptions).Resolve()
	if err != nil || resolved == nil {
		return nil, err
	}
	// Rust sets the process-wide residency requirement from the resolved
	// managed config and enforces it on the catalog request and identity.
	residency := managedResidencyFromRequirements(requirements)
	provider := model.CreateRuntimeProviderWithResidency(
		providerID,
		*providerInfo,
		&resolved.Auth,
		residency,
	)
	apiProvider, err := provider.APIProvider()
	if err != nil {
		return nil, err
	}
	authHeaders, err := provider.APIAuth()
	if err != nil {
		return nil, err
	}
	return &accountScopedCatalogProvider{
		providerID:   providerID,
		providerInfo: providerInfo,
		apiProvider:  &apiProvider,
		authHeaders:  &authHeaders,
		authSnapshot: &resolved.Auth,
		residency:    residency,
	}, nil
}

// accountScopedCatalogIdentity resolves the identity the account-scoped catalog
// belongs to (Rust #46508 resolves the effective credentials before comparing
// identities).
func accountScopedCatalogIdentity(codexHome string, read *config.ConfigReadResponse, requirements *config.ConfigRequirementsReadResponse) (string, error) {
	inputs, err := resolveAccountScopedCatalogProvider(codexHome, read, requirements)
	if err != nil || inputs == nil {
		return "", err
	}
	return model.ModelsCatalogIdentity(inputs.providerInfo, inputs.authSnapshot, inputs.authHeaders, inputs.residency), nil
}

// buildAccountScopedModelsManager resolves the account-scoped catalog manager
// from an already-read effective config plus the managed requirements.
func buildAccountScopedModelsManager(codexHome string, read *config.ConfigReadResponse, requirements *config.ConfigRequirementsReadResponse) (model.ModelsManager, error) {
	inputs, err := resolveAccountScopedCatalogProvider(codexHome, read, requirements)
	if err != nil || inputs == nil {
		return nil, err
	}
	providerInfo := inputs.providerInfo
	apiProvider := *inputs.apiProvider
	authHeaders := *inputs.authHeaders
	authSnapshot := inputs.authSnapshot
	cfg := &config.Config{Values: read.Config}
	var base *model.ModelsResponse
	if catalog := model.ModelsCatalogFromConfigValues(read.Config); catalog != nil {
		base = catalog
	}
	account := auth.AccountFromAuth(authSnapshot)
	hasChatGPTAccount := account != nil && account.Type == auth.AccountChatGPT
	supportsAPIKeyModels := providerInfo.SupportsAPIKeyModels()
	// Rust `uses_api_key_auth` (#46561): an explicit provider key joins API-key
	// logins as a discovery opt-in.
	usesAPIKeyAuth := providerInfo.HasProviderAPIKey() || authSnapshot.Mode() == "api-key"
	if supportsAPIKeyModels && usesAPIKeyAuth && strings.TrimSpace(providerInfo.ModelCatalogURL) == "" && strings.TrimSpace(providerInfo.BaseURL) == "" {
		// Codex model metadata is served by the Codex backend, not the
		// public /v1/models API (Rust #44392). Inference is unaffected.
		apiProvider.BaseURL = model.ChatGPTCodexBaseURL
	}
	endpoint := model.NewHTTPModelsEndpoint(&apiProvider, &authHeaders, nil)
	endpoint.CatalogURL = strings.TrimSpace(providerInfo.ModelCatalogURL)
	manager := model.NewRemoteModelsManagerWithOptions(&model.RemoteModelsManagerOptions{
		ModelCatalog:                    base,
		Endpoint:                        endpoint,
		UseRemoteCatalogAsSourceOfTruth: hasChatGPTAccount,
		Identity:                        model.ModelsCatalogIdentity(providerInfo, authSnapshot, &authHeaders, inputs.residency),
		SupportsAPIKeyModels:            supportsAPIKeyModels,
		APIKeyAuth:                      usesAPIKeyAuth,
		HasAuth:                         authSnapshot != nil,
		CommandAuth:                     providerInfo.HasCommandAuth(),
	})
	model.SetAPIKeyModelDiscoveryEnabled(manager, features.Enabled(cfg.FeatureSettings(), "api_key_model_discovery"))
	return manager, nil
}

func (r *RuntimeRouter) resolveAuthWithLoginRestrictions(codexHome string) (*auth.ResolvedAuth, error) {
	store := r.authStore(codexHome)
	resolved, err := store.Resolve()
	if err != nil || resolved == nil {
		return resolved, err
	}
	violation, err := r.loginRestrictionViolation(&resolved.Auth)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(violation) == "" {
		return resolved, nil
	}
	if authSourceIsEnvironment(resolved.Source) {
		return nil, errors.New(violation)
	}
	_, deleteErr := store.Delete()
	if deleteErr != nil {
		return nil, deleteErr
	}
	r.requireAccount().Logout()
	return nil, nil
}

func (r *RuntimeRouter) loginRestrictionViolation(snapshot *auth.AuthDotJSON) (string, error) {
	if snapshot == nil || r == nil || r.services.Config == nil {
		return "", nil
	}
	read, err := r.services.Config.Read(&config.ConfigReadParams{})
	if err != nil || read == nil {
		return "", err
	}
	cfg := &config.Config{Values: read.Config}
	switch cfg.ForcedLoginMethod() {
	case config.ForcedLoginMethodAPI:
		switch snapshot.Mode() {
		case "chatgpt", "chatgptAuthTokens", "agent-identity", "personal-access-token":
			return "API key login is required, but ChatGPT is currently being used. Logging out.", nil
		}
	case config.ForcedLoginMethodChatGPT:
		switch snapshot.Mode() {
		case "api-key", "bedrock-api-key":
			return "ChatGPT login is required, but an API key is currently being used. Logging out.", nil
		}
	}
	workspaces := cfg.ForcedChatGPTWorkspaceIDs()
	if len(workspaces) == 0 || snapshot.Mode() == "api-key" || snapshot.Mode() == "bedrock-api-key" {
		return "", nil
	}
	accountID := auth.AccountIDFromAuthForRestrictions(snapshot)
	if accountID == "" && snapshot.Mode() == "personal-access-token" {
		metadata, err := auth.LoadPersonalAccessTokenMetadata(context.Background(), snapshot.PersonalAccessToken)
		if err != nil {
			return "", err
		}
		accountID = strings.TrimSpace(metadata.ChatGPTAccountID)
	}
	if err := auth.EnsureWorkspaceAccountAllowed(workspaces, accountID); err == nil {
		return "", nil
	}
	expected := strings.Join(workspaces, ", ")
	if accountID != "" {
		return fmt.Sprintf("Login is restricted to workspace(s) %s, but current credentials belong to %s. Logging out.", expected, accountID), nil
	}
	return fmt.Sprintf("Login is restricted to workspace(s) %s, but current credentials lack a workspace identifier. Logging out.", expected), nil
}

func authSourceIsEnvironment(source string) bool {
	switch strings.TrimSpace(source) {
	case auth.OpenAIAPIKeyEnv, auth.CodexAPIKeyEnv, auth.CodexAccessTokenEnv, auth.WorkloadIdentitySource:
		return true
	default:
		return false
	}
}

func NewDefaultRuntimeRouter(store *session.Store, codexHome string) *RuntimeRouter {
	return NewDefaultRuntimeRouterWithOptions(store, codexHome, nil)
}

func NewDefaultRuntimeRouterWithOptions(store *session.Store, codexHome string, options *RuntimeRouterOptions) *RuntimeRouter {
	codexHome = strings.TrimSpace(codexHome)
	if codexHome == "" {
		codexHome = ".gcode"
	}
	var stateRuntime *state.StateRuntime
	closeStateRuntime := false
	var logDBInstallation *state.LogDBInstallation
	closeLogDBInstallation := false
	if options != nil {
		stateRuntime = options.StateRuntime
		closeStateRuntime = options.closeStateRuntimeOnRouterClose
		logDBInstallation = options.logDBInstallation
		closeLogDBInstallation = options.closeLogDBInstallationOnRouterClose
	}
	account := auth.NewAccountManager()
	configService := config.NewConfigService(codexHome)
	if options != nil && len(options.FeatureEnablement) > 0 {
		// Seed the CLI --enable/--disable feature toggles into the config
		// service so feature gates (for example goals) apply to the protocol
		// layer, mirroring Rust's app-server config with CLI overrides.
		configService.SetFeatureEnablementDefaults(options.FeatureEnablement)
	}
	if options != nil && options.Requirements != nil {
		configService.SetRequirementsIfDifferentFromLoaded(options.Requirements)
	}
	pluginService := plugin.NewPluginService()
	pluginService.SetCodexHome(codexHome)
	runtimeMetrics := state.NewTaskMetrics()
	// Rust records the curated-plugin startup-sync counters from the sync
	// itself; the plugin package reports them through this observer.
	pluginService.SetCuratedSyncMetricsObserver(func(sample plugin.CuratedSyncMetrics) {
		metric := telemetry.CuratedPluginsStartupSyncMetric
		if sample.Final {
			metric = telemetry.CuratedPluginsStartupSyncFinalMetric
		}
		runtimeMetrics.Counter(metric, 1, map[string]string{
			"transport": sample.Transport,
			"status":    sample.Status,
		})
	})
	if stateRuntime != nil {
		stateRuntime.SetMetrics(runtimeMetrics)
	}
	// Rust #41360: measure local Codex home storage once at startup (background
	// so it does not delay process initialization); cancel the scan on shutdown.
	var codexHomeScanCanceled int32
	scanCancel := func() bool { return atomic.LoadInt32(&codexHomeScanCanceled) == 1 }
	go recordCodexHomeMetrics(runtimeMetrics, codexHome, scanCancel)
	services := RuntimeServices{
		ThreadRouter:           NewRouter(store),
		StateRuntime:           stateRuntime,
		CloseStateRuntime:      closeStateRuntime,
		LogDBInstallation:      logDBInstallation,
		CloseLogDBInstallation: closeLogDBInstallation,
		ThreadExtras:           NewThreadExtraService(),
		Realtime:               realtime.NewManager(),
		FS:                     NewFSService(),
		Remote:                 newRemoteControlManagerForStartup(codexHome, options),
		Environment:            NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "sh"}, ""),
		Windows:                sandbox.NewWindowsManager(sandbox.WindowsReadinessNotConfigured),
		Config:                 configService,
		Account:                account,
		Hooks:                  NewHookRegistry(),
		HooksDiscovery:         &HookDiscoveryService{CodexHome: codexHome, Config: configService},
		HookRunner:             NewHookRunner(),
		Skills: NewSkillsServiceWithOptions(&SkillsServiceOptions{
			Config:              configService,
			CodexHome:           codexHome,
			IncludeDefaultRoots: true,
		}),
		Plugins:               pluginService,
		Models:                model.NewModelService(accountScopedModelsManager(codexHome, configService)),
		Permissions:           sandbox.NewPermissionProfileService(nil),
		MCP:                   mcp.NewMCPService(nil),
		Features:              features.NewFeatureService(nil),
		Apps:                  apps.NewAppService(nil),
		Turns:                 turn.NewTurnService(),
		ThreadStatus:          NewThreadStatusManager(),
		Reviews:               review.NewService(),
		Misc:                  NewMiscService(),
		CommandExec:           NewCommandExecService(),
		Processes:             NewProcessService(),
		Feedback:              &FeedbackSnapshot{Diagnostics: NewFeedbackDiagnostics(nil)},
		SkillShadowMetrics:    runtimeMetrics,
		SkillInjectionMetrics: runtimeMetrics,
		TurnMetrics:           runtimeMetrics,
		VoiceMetrics:          runtimeMetrics,
		DefaultCWD:            codexHome,

		RemoteControlDisabledByRequirements: remoteControlDisabledByRequirements(options),
	}
	if logDBInstallation != nil {
		services.LogDBHandler = logDBInstallation.Handler
	}
	if options != nil {
		services.DisableCodeModeFallback = options.DisableCodeModeInProcessFallback
	}
	switch {
	case options != nil && strings.TrimSpace(options.CodeModeHostURL) != "":
		hostURL := strings.TrimSpace(options.CodeModeHostURL)
		if codemode.UsesGrpcCodeModeEndpoint(hostURL) {
			// Rust 1e557a554e/85f331772f (#38041/#38087): http/https code-mode
			// host URLs open gRPC sessions; ws/wss continue over WebSocket.
			services.CodeModeProvider = codemode.NewGrpcCodeModeSessionProvider(hostURL, options.CodeModeHostHTTPClient)
		} else {
			services.CodeModeProvider = codemode.NewWebSocketProvider(hostURL, options.CodeModeHostHTTPClient)
		}
	case options != nil && (options.CodeModeHostEnabled || options.DisableCodeModeInProcessFallback):
		program := strings.TrimSpace(options.CodeModeHostProgram)
		if program == "" {
			program = install.Current().CodeModeHostProgram()
		}
		services.CodeModeProvider = codemode.NewProcessProvider(program)
	default:
		services.CodeModeProvider = codemode.NewDisabledProvider()
	}
	if options != nil && options.Requirements != nil {
		services.ManagedNetworkRequirements = cloneRuntimeNetworkRequirements(options.Requirements.Network)
	}
	// The hook runner reports completed-hook-run metrics through the session
	// metrics sink (Rust's emit_hook_completed_metrics).
	services.HookRunner.SetMetrics(runtimeMetrics)
	router := NewRuntimeRouter(services)
	router.codexHomeScanCancel = func() { atomic.StoreInt32(&codexHomeScanCanceled, 1) }
	router.configureEnvironmentHTTPPolicy()
	router.configureAnalyticsFromConfig(codexHome, options)
	router.configureOtelMetrics(codexHome, options, runtimeMetrics)
	router.configureRemoteControlBackendForStartup(codexHome, options)
	router.configureManagedNetworkFromConfig()
	if resolved, err := router.resolveAuthWithLoginRestrictions(codexHome); err == nil && resolved != nil {
		account.ApplyAuthSnapshot(&resolved.Auth)
	}
	router.configureMCPFromConfig()
	if stateRuntime != nil {
		// Mirrors Rust thread_manager.rs: when background_paginated_rollout_
		// migration is enabled, inspect rollouts on startup and migrate legacy
		// ones in the background.
		MaybeMigrateRolloutsOnStartup(codexHome, stateRuntime, configService)
	}
	// Mirrors Rust thread_manager.rs: local thread store compression starts its
	// background pass on startup when the feature is enabled.
	MaybeSpawnRolloutCompressionOnStartup(codexHome, configService)
	return router
}

func (r *RuntimeRouter) StartupError() error {
	if r == nil {
		return errors.New("app-server runtime router is not configured")
	}
	return r.startupErr
}

func resolveDefaultStateRuntime(ctx context.Context, codexHome string, options *RuntimeRouterOptions, sqliteHomeOverride string) (*state.StateRuntime, bool, error) {
	if options != nil && options.StateRuntime != nil {
		return options.StateRuntime, false, nil
	}
	sqliteConfig, err := state.SqliteConfigForCodexHomeWithOverride(codexHome, sqliteHomeOverride)
	if err != nil {
		return nil, false, err
	}
	runtime, err := initStateRuntimeWithFreshStartOnCorruption(ctx, sqliteConfig, "openai")
	if err != nil {
		return nil, false, fmt.Errorf("failed to initialize sqlite state runtime under %s: %w", sqliteConfig.Home(), err)
	}
	if err := state.WaitForRolloutBackfill(ctx, runtime, codexHome, state.RolloutBackfillOptions{}); err != nil {
		_ = runtime.Close()
		return nil, false, fmt.Errorf("failed to complete sqlite rollout backfill under %s: %w", sqliteConfig.Home(), err)
	}
	return runtime, true, nil
}

func initStateRuntimeWithFreshStartOnCorruption(ctx context.Context, sqliteConfig state.SqliteConfig, defaultProvider string) (*state.StateRuntime, error) {
	attempted := map[string]bool{}
	for {
		runtime, err := state.InitStateRuntime(ctx, sqliteConfig, defaultProvider)
		if err == nil {
			return runtime, nil
		}
		databasePath, corrupt := state.RuntimeDBPathForCorruptionError(err)
		if databasePath == "" {
			databasePath = sqliteConfig.StateDBPath()
		}
		blockingHome := false
		if info, statErr := os.Stat(sqliteConfig.Home()); statErr == nil {
			blockingHome = !info.IsDir()
		}
		if !corrupt && !blockingHome {
			return nil, err
		}
		if attempted[databasePath] {
			return nil, fmt.Errorf("failed to initialize sqlite state runtime after moving damaged database file into a backup folder: %w", err)
		}
		attempted[databasePath] = true
		if _, backupErr := state.BackupDBFilesForFreshStart(&state.DBRecoveryStartupError{
			DatabasePath: databasePath,
			Detail:       err.Error(),
		}, time.Time{}); backupErr != nil {
			return nil, fmt.Errorf("failed to move damaged sqlite state database files into a backup folder: %v; original error: %w", backupErr, err)
		}
	}
}

func (r *RuntimeRouter) configureManagedNetworkFromConfig() {
	if r == nil || r.services.ManagedNetwork != nil || r.services.Config == nil {
		return
	}
	read, err := r.services.Config.Read(&config.ConfigReadParams{})
	if err != nil || read == nil {
		return
	}
	if err := validateManagedNetworkRequirements(r.services.ManagedNetworkRequirements); err != nil {
		slog.Warn("failed to validate managed network requirements", "error", err)
		return
	}
	proxyConfig, shouldStart, err := r.buildManagedNetworkProxyConfig(read.Config)
	if err != nil || !shouldStart {
		if err != nil {
			slog.Warn("failed to build managed network proxy", "error", err)
		}
		return
	}
	baseEnv := map[string]string{}
	for _, item := range os.Environ() {
		key, value, ok := strings.Cut(item, "=")
		if ok && key != "" {
			baseEnv[key] = value
		}
	}
	prepared, err := network.StartProxyManagedNetwork(context.Background(), *proxyConfig, baseEnv)
	if err == nil {
		r.services.ManagedNetwork = prepared
		r.startManagedNetworkReloadWatcher()
	}
}

func (r *RuntimeRouter) configureAnalyticsFromConfig(codexHome string, options *RuntimeRouterOptions) {
	if r == nil || r.services.Analytics != nil || r.services.Config == nil {
		return
	}
	read, err := r.services.Config.Read(&config.ConfigReadParams{})
	if err != nil || read == nil {
		return
	}
	cfg := &config.Config{Values: read.Config}
	defaultEnabled := false
	if options != nil {
		defaultEnabled = options.AnalyticsDefaultEnabled
	}
	if !cfg.AnalyticsEnabled(defaultEnabled) {
		return
	}
	r.services.Analytics = telemetry.NewAnalyticsEventsClient(telemetry.AnalyticsEventsClientOptions{
		BaseURL:          cfg.ChatGPTBaseURL(),
		HTTPClient:       r.httpClientForConfig(cfg),
		AuthorizeRequest: r.analyticsAuthorizeRequest(codexHome),
	})
}

// otelAppServerServiceName mirrors app-server's OTEL_SERVICE_NAME.
const otelAppServerServiceName = "codex-app-server"

// configureOtelMetrics mirrors the app-server's otel_init wiring: build the
// OTEL provider from the effective config, forward the task metrics to its
// metrics client (Rust's sqlite telemetry recorder over the metrics client),
// and record the process start once. Exports stay off unless an OTLP metrics
// exporter is configured and analytics is enabled, so the default Statsig route
// is inert in development builds.
func (r *RuntimeRouter) configureOtelMetrics(codexHome string, options *RuntimeRouterOptions, metrics *state.TaskMetrics) {
	if r == nil || r.services.Config == nil || metrics == nil {
		return
	}
	provider, err := r.buildOtelProvider(codexHome, options)
	if err != nil {
		// Rust fails app-server startup on an OTEL provider error; the Go
		// constructor cannot, so the failure is reported and startup continues
		// without export.
		slog.Warn("failed to build the OTEL provider", "error", err)
		return
	}
	r.installOtelProvider(provider, metrics)
	// Rust spawns the reloader next to the initial provider; it rebuilds the
	// exporters after an account change even while no exporter is configured.
	r.startOtelReloader(codexHome, options, metrics)
}

// buildOtelProvider resolves the latest config into an OTEL provider
// (otel_init::build_provider).
func (r *RuntimeRouter) buildOtelProvider(codexHome string, options *RuntimeRouterOptions) (*telemetry.OtelProvider, error) {
	if r == nil || r.services.Config == nil {
		return nil, nil
	}
	read, err := r.services.Config.Read(&config.ConfigReadParams{})
	if err != nil || read == nil {
		return nil, err
	}
	defaultAnalyticsEnabled := false
	if options != nil {
		defaultAnalyticsEnabled = options.AnalyticsDefaultEnabled
	}
	return otelinit.BuildProvider(otelinit.Options{
		Config:                  &config.Config{Values: read.Config},
		ServiceName:             otelAppServerServiceName,
		ServiceVersion:          appServerVersion(),
		DefaultAnalyticsEnabled: defaultAnalyticsEnabled,
	})
}

// installOtelProvider publishes a provider's pipelines: the task metrics
// exporter, the exported log records, and the request tracer. A nil provider
// only records that tracing is off.
func (r *RuntimeRouter) installOtelProvider(provider *telemetry.OtelProvider, metrics *state.TaskMetrics) {
	if r == nil {
		return
	}
	r.otelProviderMu.Lock()
	r.otelProvider = provider
	r.otelProviderMu.Unlock()
	if provider == nil {
		return
	}
	if provider.Metrics() != nil {
		if metrics != nil {
			metrics.SetExporter(provider.Metrics())
		}
		telemetry.RecordProcessStartOnce(provider.Metrics(), otelAppServerServiceName)
	}
	// Rust installs the reloadable OTEL log layer on the app-server's tracing
	// subscriber; Go chains an slog handler around the current default handler
	// so the process's log records reach the exporter without dropping the
	// handlers installed before it (the sqlite log handler wraps the same way).
	if logsHandler := provider.LogsHandler(state.SlogTerminalHandler()); logsHandler != nil {
		slog.SetDefault(slog.New(logsHandler))
	}
	// Rust instruments every app-server request with an `app_server.request`
	// span; the router owns the transport-level request entry point.
	if tracer := provider.Tracer(); tracer != nil && r.services.ThreadRouter != nil {
		r.services.ThreadRouter.SetTracer(tracer)
	}
}

// currentOtelProvider returns the installed provider, or nil when tracing,
// logging, and metrics export are all off.
func (r *RuntimeRouter) currentOtelProvider() *telemetry.OtelProvider {
	if r == nil {
		return nil
	}
	r.otelProviderMu.RLock()
	defer r.otelProviderMu.RUnlock()
	return r.otelProvider
}

// startOtelReloader mirrors app-server/src/otel_reloader.rs::spawn: after an
// account change the exporters are rebuilt from the latest config and the
// previous provider is shut down in the background. Rust waits out the account
// handlers that install the new cloud loader before the config is re-read.
func (r *RuntimeRouter) startOtelReloader(codexHome string, options *RuntimeRouterOptions, metrics *state.TaskMetrics) {
	if r == nil {
		return
	}
	r.otelReloadMu.Lock()
	if r.otelReloadStop != nil {
		r.otelReloadMu.Unlock()
		return
	}
	stop := make(chan struct{})
	r.otelReloadStop = stop
	r.otelReloadMu.Unlock()
	go func() {
		for {
			changed := r.authChangeChannel()
			select {
			case <-stop:
				return
			case <-changed:
			}
			// Account handlers install the new cloud loader after publishing
			// auth changes.
			select {
			case <-stop:
				return
			case <-time.After(otelReloadDelay):
			}
			r.reloadOtelProvider(codexHome, options, metrics)
		}
	}()
}

// otelReloadDelay mirrors otel_reloader's post-auth-change sleep.
const otelReloadDelay = 50 * time.Millisecond

// reloadOtelProvider rebuilds and installs the provider, then shuts the
// previous one down in the background (Rust's spawn_blocking shutdown).
func (r *RuntimeRouter) reloadOtelProvider(codexHome string, options *RuntimeRouterOptions, metrics *state.TaskMetrics) {
	if r == nil {
		return
	}
	next, err := r.buildOtelProvider(codexHome, options)
	if err != nil {
		slog.Warn("failed to rebuild telemetry exporters after account change", "error", err)
		return
	}
	previous := r.currentOtelProvider()
	r.installOtelProvider(next, metrics)
	if previous != nil {
		go func() {
			if err := previous.Shutdown(context.Background()); err != nil {
				slog.Warn("failed to shut down the previous telemetry exporters", "error", err)
			}
		}()
	}
	slog.Info("reloaded telemetry exporters after account change", "event.name", "codex.app_server.otel_reloaded")
}

// authChangeChannel returns the channel closed on the next auth change, or a
// channel that never fires while the router is closing.
func (r *RuntimeRouter) authChangeChannel() <-chan struct{} {
	r.authRevisionMu.Lock()
	defer r.authRevisionMu.Unlock()
	return r.authChanged
}

// stopOtelReloader ends the reloader goroutine at router close.
func (r *RuntimeRouter) stopOtelReloader() {
	if r == nil {
		return
	}
	r.otelReloadMu.Lock()
	defer r.otelReloadMu.Unlock()
	if r.otelReloadStop != nil {
		close(r.otelReloadStop)
		r.otelReloadStop = nil
	}
}

// SetRequestTransport stamps the transport name the request span reports
// (Rust's `rpc.transport`). Transports call this when they serve a router; an
// in-process router keeps the "in-process" default.
func (r *RuntimeRouter) SetRequestTransport(transport string) {
	if r == nil {
		return
	}
	r.requestTransport = strings.TrimSpace(transport)
	if r.services.ThreadRouter != nil {
		r.services.ThreadRouter.SetRequestTransport(transport)
	}
}

// requestTracer reports the tracer the app-server installed, or nil when the
// tracing pipeline is disabled.
func (r *RuntimeRouter) requestTracer() *telemetry.Tracer {
	provider := r.currentOtelProvider()
	if provider == nil {
		return nil
	}
	return provider.Tracer()
}

// requestSpanKey identifies one in-flight transport request.
type requestSpanKey struct {
	connectionID string
	requestID    string
}

// registerRequestSpan keeps the open request span reachable while its request is
// dispatched (Rust's OutgoingMessageSender::register_request_context).
func (r *RuntimeRouter) registerRequestSpan(request *Request, span *telemetry.Span) {
	if r == nil || request == nil || span == nil {
		return
	}
	r.requestSpansMu.Lock()
	defer r.requestSpansMu.Unlock()
	if r.requestSpans == nil {
		r.requestSpans = map[requestSpanKey]*telemetry.Span{}
	}
	r.requestSpans[requestSpanKey{connectionID: request.normalizedConnectionID(), requestID: request.ID.String()}] = span
}

// unregisterRequestSpan drops the request's span once it has been dispatched.
func (r *RuntimeRouter) unregisterRequestSpan(request *Request) {
	if r == nil || request == nil {
		return
	}
	r.requestSpansMu.Lock()
	defer r.requestSpansMu.Unlock()
	delete(r.requestSpans, requestSpanKey{connectionID: request.normalizedConnectionID(), requestID: request.ID.String()})
}

// requestSpanTrace reports the W3C trace context of the request's span
// (Rust's RequestContext::request_trace), or nil when the request was not
// traced.
func (r *RuntimeRouter) requestSpanTrace(request *Request) *protocol.W3CTraceContext {
	if r == nil || request == nil {
		return nil
	}
	r.requestSpansMu.Lock()
	span := r.requestSpans[requestSpanKey{connectionID: request.normalizedConnectionID(), requestID: request.ID.String()}]
	r.requestSpansMu.Unlock()
	if span == nil {
		return nil
	}
	traceparent, tracestate, ok := span.W3CTraceContext()
	if !ok {
		return nil
	}
	return &protocol.W3CTraceContext{Traceparent: traceparent, Tracestate: tracestate}
}

func (r *RuntimeRouter) analyticsAuthorizeRequest(codexHome string) telemetry.AnalyticsAuthorizeRequestFunc {
	return func(ctx context.Context, request *http.Request, body []byte) (bool, error) {
		if r == nil {
			return false, nil
		}
		resolved, err := r.resolveAuthWithLoginRestrictions(codexHome)
		if err != nil || resolved == nil {
			return false, err
		}
		if !authUsesCodexBackend(&resolved.Auth) {
			return false, nil
		}
		headers, err := model.AuthHeadersFromAuth(resolved.Auth)
		if err != nil {
			return false, err
		}
		if err := headers.Apply(ctx, request, body); err != nil {
			return false, err
		}
		return true, nil
	}
}

func newRemoteControlManagerForStartup(codexHome string, options *RuntimeRouterOptions) *remotecontrol.Manager {
	installationID := ""
	if options != nil && strings.TrimSpace(options.RemoteControlInstallationID) != "" {
		installationID = strings.TrimSpace(options.RemoteControlInstallationID)
	} else if strings.TrimSpace(codexHome) != "" {
		if id, err := install.ResolveInstallationID(codexHome); err == nil {
			installationID = id
		}
	}
	manager := remotecontrol.NewManager("codex", installationID)
	if options == nil {
		return manager
	}
	switch options.RemoteControlStartupMode {
	case RemoteControlStartupEnabledEphemeral:
		manager.Enable(&remotecontrol.EnableParams{Ephemeral: true})
	case RemoteControlStartupDisabledEphemeral:
		manager.Disable(&remotecontrol.DisableParams{Ephemeral: true})
	}
	return manager
}

func (r *RuntimeRouter) configureRemoteControlBackendForStartup(codexHome string, options *RuntimeRouterOptions) {
	if r == nil {
		return
	}
	if backend := r.remoteControlManagerBackend(codexHome, options); backend != nil {
		r.requireRemote().ConfigureBackend(backend)
	}
}

func (r *RuntimeRouter) remoteControlManagerBackend(codexHome string, options *RuntimeRouterOptions) *remotecontrol.ManagerBackendOptions {
	if options == nil {
		return nil
	}
	explicitBackend := options.RemoteControlBackendEnabled ||
		options.RemoteControlEnrollmentStore != nil ||
		options.RemoteControlAuthLoader != nil ||
		options.RemoteControlAuthRecovery != nil ||
		strings.TrimSpace(options.RemoteControlURL) != "" ||
		options.RemoteControlServerAPIOptions != nil ||
		options.RemoteControlAppServerClientName != nil
	if !explicitBackend {
		return nil
	}
	store := options.RemoteControlEnrollmentStore
	closeStoreOnClose := false
	if store == nil && strings.TrimSpace(codexHome) != "" {
		if opened, err := remotecontrol.OpenEnrollmentStore(remotecontrol.RemoteControlStateDBPath(codexHome)); err == nil {
			store = opened
			closeStoreOnClose = true
			_ = store.EnsureSchema(context.Background())
		}
	}
	authLoader := options.RemoteControlAuthLoader
	if authLoader == nil && strings.TrimSpace(codexHome) != "" {
		authLoader = remotecontrol.NewRemoteControlAuthLoaderForCodexHome(codexHome)
	}
	authRecovery, authRecoveryReset, authRecoveryChanged := r.remoteControlAuthRecovery(codexHome, options)
	remoteControlURL := strings.TrimSpace(options.RemoteControlURL)
	if remoteControlURL == "" {
		remoteControlURL = config.DefaultChatGPTBaseURL
	}
	return &remotecontrol.ManagerBackendOptions{
		RemoteControlURL:    remoteControlURL,
		Store:               store,
		CloseStoreOnClose:   closeStoreOnClose,
		AuthLoader:          authLoader,
		AuthRecovery:        authRecovery,
		AuthRecoveryReset:   authRecoveryReset,
		AuthRecoveryChanged: authRecoveryChanged,
		ServerAPIOptions:    options.RemoteControlServerAPIOptions,
		AppServerClientName: options.RemoteControlAppServerClientName,
	}
}

func (r *RuntimeRouter) remoteControlAuthRecovery(codexHome string, options *RuntimeRouterOptions) (remotecontrol.RemoteControlAuthRecovery, func(), func() bool) {
	if options != nil && options.RemoteControlAuthRecovery != nil {
		return options.RemoteControlAuthRecovery, nil, nil
	}
	codexHome = strings.TrimSpace(codexHome)
	if codexHome == "" {
		return nil, nil, nil
	}
	controller := remotecontrol.NewUnauthorizedRecoveryControllerForCodexHome(codexHome, &remotecontrol.UnauthorizedRecoveryOptions{
		ExternalRefresh: r.remoteControlExternalAuthRecovery,
		Observer:        remoteControlAuthRecoveryLogObserver,
	})
	return controller.Recover, controller.Reset, controller.ConsumeAuthStateChanged
}

func remoteControlAuthRecoveryLogObserver(event remotecontrol.RemoteControlAuthRecoveryEvent) {
	attrs := []any{
		"mode", event.Mode,
		"step", event.Step,
		"unavailable_reason", event.UnavailableReason,
	}
	if event.AuthStateChanged != nil {
		attrs = append(attrs, "auth_state_changed", *event.AuthStateChanged)
	}
	if event.Err != nil {
		attrs = append(attrs, "error", event.Err)
		slog.Warn("remote control unauthorized recovery failed", attrs...)
		return
	}
	slog.Info("remote control unauthorized recovery step", attrs...)
}

func (r *RuntimeRouter) remoteControlExternalAuthRecovery(ctx context.Context, previous *remotecontrol.RemoteControlConnectionAuth) (*remotecontrol.RemoteControlConnectionAuth, bool, error) {
	if r == nil {
		return nil, false, fmt.Errorf("%w: runtime router is nil", ErrInvalidRequest)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, remoteControlExternalAuthRecoveryTimeout)
		defer cancel()
	}
	previousAccountID := ""
	if previous != nil {
		previousAccountID = previous.AccountID
	}
	response, err := r.externalAuthRefresh(ctx, &model.ExternalAuthRefreshRequest{
		Reason:            model.ExternalAuthRefreshUnauthorized,
		PreviousAccountID: previousAccountID,
	})
	if err != nil {
		return nil, false, err
	}
	snapshot := auth.FromChatGPTAuthTokens(response.AccessToken, response.ChatGPTAccountID, response.ChatGPTPlanType)
	recovered, err := remotecontrol.NewRemoteControlConnectionAuthFromSnapshot(&snapshot)
	if err != nil {
		return nil, false, err
	}
	return recovered, true, nil
}

func remoteControlDisabledByRequirements(options *RuntimeRouterOptions) bool {
	if options == nil {
		return false
	}
	if options.RemoteControlDisabledByRequirements {
		return true
	}
	return options.Requirements != nil &&
		options.Requirements.AllowRemoteControl != nil &&
		!*options.Requirements.AllowRemoteControl
}

func (r *RuntimeRouter) Handle(request *Request) *Response {
	if err := request.Validate(); err != nil {
		if request == nil {
			return ErrorResponse(RequestID{}, -32600, err.Error(), nil)
		}
		return ErrorResponse(request.ID, requestValidationErrorCode(err), err.Error(), nil)
	}
	// Rust records an `app_server.request` span per transport request
	// (app_server_tracing.rs::request_span), wrapping the dispatch below, and
	// keeps its context reachable for the turn it starts.
	if span := startRequestSpan(r.requestTracer(), request, r.requestTransport); span != nil {
		r.registerRequestSpan(request, span)
		defer func() {
			r.unregisterRequestSpan(request)
			span.End()
		}()
	}
	result, err := r.dispatch(request)
	if err != nil {
		return ErrorResponse(request.ID, runtimeErrorCode(err), err.Error(), jsonRPCErrorData(err))
	}
	return OK(request.ID, result)
}

func (r *RuntimeRouter) ConnectionClosed(connectionID string) {
	connectionID = normalizeConnectionID(connectionID)
	if r == nil {
		return
	}
	r.pendingGoalMu.Lock()
	delete(r.pendingGoalByConn, connectionID)
	r.pendingGoalMu.Unlock()
	if r.networkApproval != nil {
		r.networkApproval.cancelPendingForConnection(connectionID)
	}
	r.clearConnectionSubscriptions(connectionID)
	r.clearConnectionClientInfo(connectionID)
	r.clearConnectionNotificationOptOut(connectionID)
	r.clearConnectionExperimentalAPI(connectionID)
	r.clearConnectionRequestAttestation(connectionID)
	r.clearConnectionMCPOpenAIForm(connectionID)
	r.clearConnectionMCPStandardFormInput(connectionID)
	if r.mcpEventStreams != nil {
		r.mcpEventStreams.stopConnection(connectionID)
	}
	r.syncMCPOpenAIFormElicitationCapability()
	r.syncMCPStandardFormInputCapability()
	if r.services.CommandExec != nil {
		r.services.CommandExec.ConnectionClosed(connectionID)
	}
	if r.services.Processes != nil {
		r.services.Processes.ConnectionClosed(connectionID)
	}
	if r.services.FS != nil {
		r.services.FS.ConnectionClosed(connectionID)
	}
	if r.services.ServerRequests != nil {
		r.services.ServerRequests.RejectPending(connectionID, fmt.Errorf("server request failed: connection closed"))
	}
}

func (r *RuntimeRouter) Close() error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		r.closeErr = r.close()
	})
	return r.closeErr
}

func (r *RuntimeRouter) close() error {
	if r.codexHomeScanCancel != nil {
		r.codexHomeScanCancel()
	}
	r.memoryStartupMu.Lock()
	r.memoryStartupClosing = true
	if r.memoryStartupCancel != nil {
		r.memoryStartupCancel()
	}
	r.memoryStartupMu.Unlock()
	r.realtimeOpsMu.Lock()
	r.realtimeOpsClosing = true
	if r.realtimeOpsCancel != nil {
		r.realtimeOpsCancel()
	}
	r.realtimeOpsMu.Unlock()
	activeTurns := r.threads.BeginShutdown()
	for _, active := range activeTurns {
		if active == nil {
			continue
		}
		if active.Cancel != nil {
			active.Cancel()
		}
		if active.TurnID == "" {
			continue
		}
		if r.networkApproval != nil {
			r.networkApproval.cancelPendingForTurn(active.ThreadID, active.TurnID)
			r.networkApproval.clearActiveCallsForTurn(active.ThreadID, active.TurnID)
		}
		r.clearPendingUnifiedExecItems(active.ThreadID, active.TurnID)
		_, _ = r.requireTurns().Interrupt(&turn.TurnInterruptParams{ThreadID: active.ThreadID, TurnID: active.TurnID})
		analytics := analyticsContextFromActiveRuntimeTurn(active)
		if active.Params != nil && turnStartReviewRuntime(active.Params) {
			r.finishReviewRuntimeInterrupted(active.ThreadID, active.TurnID, active.StartedAtMS, analytics)
		} else {
			r.finishTurnInterruptedAnalytics(active.ThreadID, active.TurnID, active.StartedAtMS, analytics)
		}
	}
	waitCtx, cancelWait := context.WithTimeout(context.Background(), 5*time.Second)
	waitErr := r.threads.WaitForTurnWorkers(waitCtx)
	cancelWait()
	memoryWaitErr := waitForWaitGroup(&r.memoryStartupWG, 5*time.Second)
	if waitErr == nil {
		waitErr = memoryWaitErr
	}
	realtimeWaitErr := waitForWaitGroup(&r.realtimeOpsWG, 5*time.Second)
	if waitErr == nil {
		waitErr = realtimeWaitErr
	}
	r.cancelAllAccountLoginRuntimes()
	r.runLoadedSessionEndHooks()
	if r.services.Account != nil {
		r.services.Account.CancelActiveLogins()
	}
	closeErr := waitErr
	for _, notification := range r.threads.ShutdownStatuses() {
		r.notifyThreadStatus(notification)
	}
	r.threads.ClearSubscriptions()
	r.threads.ClearEphemeralRecords()
	// Every live session's `session_loop` span closes with the router (Rust ends
	// them when the sessions' submission loops terminate).
	r.endAllThreadSessionSpans()
	if r.services.ThreadRouter != nil {
		if err := r.services.ThreadRouter.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	if r.services.Realtime != nil {
		realtimeCtx, cancelRealtime := context.WithTimeout(context.Background(), 5*time.Second)
		if err := r.services.Realtime.Shutdown(realtimeCtx); err != nil && closeErr == nil {
			closeErr = err
		}
		cancelRealtime()
	}
	if closer, ok := r.services.Analytics.(interface{ Close() error }); ok && closer != nil {
		if err := closer.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	if r.services.Remote != nil {
		if err := r.services.Remote.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	if r.services.Skills != nil {
		if err := r.services.Skills.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	if r.services.UnifiedExec != nil {
		if err := r.services.UnifiedExec.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	if err := r.closeCodeModeRuntimes(); err != nil && closeErr == nil {
		closeErr = err
	}
	if r.services.CodeModeProvider != nil {
		if closer, ok := r.services.CodeModeProvider.(interface{ Close() error }); ok {
			if err := closer.Close(); err != nil && closeErr == nil {
				closeErr = err
			}
		}
	}
	if r.mcpRuntimes != nil {
		if err := r.mcpRuntimes.close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	if r.mcpEventStreams != nil {
		r.mcpEventStreams.clear()
	}
	if r.services.MCP != nil {
		if err := r.services.MCP.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	if err := r.closeManagedNetworkReloadWatcher(); err != nil && closeErr == nil {
		closeErr = err
	}
	if err := r.closeThreadManagedNetworks(); err != nil && closeErr == nil {
		closeErr = err
	}
	if r.services.ManagedNetwork != nil {
		if err := r.services.ManagedNetwork.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	if r.services.CloseLogDBInstallation && r.services.LogDBInstallation != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := r.services.LogDBInstallation.Close(closeCtx); err != nil && closeErr == nil {
			closeErr = err
		}
		cancel()
	}
	if r.services.CloseStateRuntime && r.services.StateRuntime != nil {
		if err := r.services.StateRuntime.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	r.stopOtelReloader()
	if provider := r.currentOtelProvider(); provider != nil {
		otelCtx, cancelOtel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := provider.Shutdown(otelCtx); err != nil && closeErr == nil {
			closeErr = err
		}
		cancelOtel()
	}
	return closeErr
}

func (r *RuntimeRouter) runLoadedSessionEndHooks() {
	if r == nil {
		return
	}
	for _, threadID := range r.requireThreadStatus().LoadedThreadIDs() {
		record, err := r.threadRecord(session.ThreadID(threadID), true, true)
		if err != nil || record == nil {
			continue
		}
		r.runSessionEndHookOnce(cloneRuntimeSessionRecord(record), "other")
	}
}

func (r *RuntimeRouter) rememberConnectionClientInfo(connectionID string, info ClientInfo) bool {
	if r == nil {
		return false
	}
	connectionID = normalizeConnectionID(connectionID)
	r.clientInfoMu.Lock()
	defer r.clientInfoMu.Unlock()
	if r.clientInfo == nil {
		r.clientInfo = map[string]ClientInfo{}
	}
	if _, exists := r.clientInfo[connectionID]; exists {
		return false
	}
	r.clientInfo[connectionID] = info
	return true
}

func (r *RuntimeRouter) rememberConnectionNotificationOptOut(connectionID string, capabilities *InitializeCapabilities) {
	if r == nil {
		return
	}
	connectionID = normalizeConnectionID(connectionID)
	methods := map[NotificationMethod]struct{}{}
	if capabilities != nil {
		for _, method := range capabilities.OptOutNotificationMethods {
			method = strings.TrimSpace(method)
			if method == "" {
				continue
			}
			methods[NotificationMethod(method)] = struct{}{}
		}
	}
	r.clientInfoMu.Lock()
	defer r.clientInfoMu.Unlock()
	if r.notificationOptOut == nil {
		r.notificationOptOut = map[string]map[NotificationMethod]struct{}{}
	}
	r.notificationOptOut[connectionID] = methods
}

func (r *RuntimeRouter) rememberConnectionExperimentalAPI(connectionID string, capabilities *InitializeCapabilities) {
	if r == nil {
		return
	}
	connectionID = normalizeConnectionID(connectionID)
	r.clientInfoMu.Lock()
	defer r.clientInfoMu.Unlock()
	if r.experimentalAPI == nil {
		r.experimentalAPI = map[string]bool{}
	}
	if capabilities == nil {
		r.experimentalAPI[connectionID] = false
		return
	}
	r.experimentalAPI[connectionID] = capabilities.ExperimentalAPI
}

func (r *RuntimeRouter) rememberConnectionMCPOpenAIForm(connectionID string, capabilities *InitializeCapabilities) {
	if r == nil {
		return
	}
	connectionID = normalizeConnectionID(connectionID)
	enabled := capabilities != nil && capabilities.MCPServerOpenAIFormElicitation
	r.clientInfoMu.Lock()
	defer r.clientInfoMu.Unlock()
	if r.mcpOpenAIForm == nil {
		r.mcpOpenAIForm = map[string]bool{}
	}
	r.mcpOpenAIForm[connectionID] = enabled
}

func (r *RuntimeRouter) rememberConnectionMCPStandardFormInput(connectionID string, capabilities *InitializeCapabilities) {
	if r == nil {
		return
	}
	connectionID = normalizeConnectionID(connectionID)
	enabled := capabilities != nil && capabilities.MCPServerStandardFormInput
	r.clientInfoMu.Lock()
	defer r.clientInfoMu.Unlock()
	if r.mcpStandardFormInput == nil {
		r.mcpStandardFormInput = map[string]bool{}
	}
	r.mcpStandardFormInput[connectionID] = enabled
}

func (r *RuntimeRouter) rememberConnectionRequestAttestation(connectionID string, capabilities *InitializeCapabilities) {
	if r == nil {
		return
	}
	connectionID = normalizeConnectionID(connectionID)
	enabled := capabilities != nil && capabilities.RequestAttestation
	r.clientInfoMu.Lock()
	defer r.clientInfoMu.Unlock()
	if r.requestAttestation == nil {
		r.requestAttestation = map[string]bool{}
	}
	r.requestAttestation[connectionID] = enabled
}

func (r *RuntimeRouter) clearConnectionClientInfo(connectionID string) {
	if r == nil {
		return
	}
	connectionID = normalizeConnectionID(connectionID)
	r.clientInfoMu.Lock()
	defer r.clientInfoMu.Unlock()
	delete(r.clientInfo, connectionID)
}

func (r *RuntimeRouter) clearConnectionNotificationOptOut(connectionID string) {
	if r == nil {
		return
	}
	connectionID = normalizeConnectionID(connectionID)
	r.clientInfoMu.Lock()
	defer r.clientInfoMu.Unlock()
	delete(r.notificationOptOut, connectionID)
}

func (r *RuntimeRouter) clearConnectionExperimentalAPI(connectionID string) {
	if r == nil {
		return
	}
	connectionID = normalizeConnectionID(connectionID)
	r.clientInfoMu.Lock()
	defer r.clientInfoMu.Unlock()
	delete(r.experimentalAPI, connectionID)
}

func (r *RuntimeRouter) clearConnectionMCPOpenAIForm(connectionID string) {
	if r == nil {
		return
	}
	connectionID = normalizeConnectionID(connectionID)
	r.clientInfoMu.Lock()
	defer r.clientInfoMu.Unlock()
	delete(r.mcpOpenAIForm, connectionID)
}

func (r *RuntimeRouter) clearConnectionMCPStandardFormInput(connectionID string) {
	if r == nil {
		return
	}
	connectionID = normalizeConnectionID(connectionID)
	r.clientInfoMu.Lock()
	defer r.clientInfoMu.Unlock()
	delete(r.mcpStandardFormInput, connectionID)
}

func (r *RuntimeRouter) clearConnectionRequestAttestation(connectionID string) {
	if r == nil {
		return
	}
	connectionID = normalizeConnectionID(connectionID)
	r.clientInfoMu.Lock()
	defer r.clientInfoMu.Unlock()
	delete(r.requestAttestation, connectionID)
}

func (r *RuntimeRouter) connectionClientInfo(connectionID string) (ClientInfo, bool) {
	if r == nil {
		return ClientInfo{}, false
	}
	connectionID = normalizeConnectionID(connectionID)
	r.clientInfoMu.RLock()
	defer r.clientInfoMu.RUnlock()
	info, ok := r.clientInfo[connectionID]
	return info, ok
}

func (r *RuntimeRouter) connectionExperimentalAPIDisabled(connectionID string) bool {
	if r == nil {
		return false
	}
	connectionID = normalizeConnectionID(connectionID)
	r.clientInfoMu.RLock()
	defer r.clientInfoMu.RUnlock()
	enabled, ok := r.experimentalAPI[connectionID]
	return ok && !enabled
}

func (r *RuntimeRouter) connectionExperimentalAPI(connectionID string) *bool {
	if r == nil {
		return nil
	}
	connectionID = normalizeConnectionID(connectionID)
	r.clientInfoMu.RLock()
	defer r.clientInfoMu.RUnlock()
	enabled, ok := r.experimentalAPI[connectionID]
	if !ok {
		return nil
	}
	return &enabled
}

func (r *RuntimeRouter) anyConnectionMCPOpenAIFormElicitation() bool {
	if r == nil {
		return false
	}
	r.clientInfoMu.RLock()
	defer r.clientInfoMu.RUnlock()
	for _, enabled := range r.mcpOpenAIForm {
		if enabled {
			return true
		}
	}
	return false
}

func (r *RuntimeRouter) anyConnectionMCPStandardFormInput() bool {
	if r == nil {
		return false
	}
	r.clientInfoMu.RLock()
	defer r.clientInfoMu.RUnlock()
	for _, enabled := range r.mcpStandardFormInput {
		if enabled {
			return true
		}
	}
	return false
}

func (r *RuntimeRouter) syncMCPOpenAIFormElicitationCapability() {
	if r == nil {
		return
	}
	enabled := r.anyConnectionMCPOpenAIFormElicitation()
	if r.services.MCP != nil {
		r.services.MCP.SetOpenAIFormElicitationEnabled(enabled)
	}
	if r.mcpRuntimes != nil {
		r.mcpRuntimes.setOpenAIFormElicitationEnabled(enabled)
	}
	r.prewarmLoadedMCPThreads()
}

func (r *RuntimeRouter) syncMCPStandardFormInputCapability() {
	if r == nil {
		return
	}
	if r.services.MCP != nil {
		if handler, ok := r.services.MCP.ElicitationHandler().(*appserverMCPElicitationHandler); ok && r.anyConnectionMCPStandardFormInput() {
			handler.EnableFullAccessFormInput()
		}
	}
}

func (r *RuntimeRouter) notificationMethodOptedOut(method NotificationMethod) bool {
	if r == nil || strings.TrimSpace(string(method)) == "" {
		return false
	}
	r.clientInfoMu.RLock()
	defer r.clientInfoMu.RUnlock()
	if len(r.notificationOptOut) == 0 {
		return false
	}
	for _, methods := range r.notificationOptOut {
		if _, ok := methods[method]; !ok {
			return false
		}
	}
	return true
}

func (r *RuntimeRouter) connectionNotificationMethodOptedOut(connectionID string, method NotificationMethod) bool {
	if r == nil || strings.TrimSpace(string(method)) == "" {
		return false
	}
	connectionID = normalizeConnectionID(connectionID)
	r.clientInfoMu.RLock()
	defer r.clientInfoMu.RUnlock()
	methods := r.notificationOptOut[connectionID]
	if len(methods) == 0 {
		return false
	}
	_, ok := methods[method]
	return ok
}

func (r *RuntimeRouter) rejectUninitializedConnection(request *Request) error {
	if r == nil || request == nil || requestAllowsUninitializedConnection(request.Method) {
		return nil
	}
	if strings.TrimSpace(request.ConnectionID) == "" {
		return nil
	}
	if _, ok := r.connectionClientInfo(request.ConnectionID); !ok {
		return jsonRPCInvalidRequest("Not initialized")
	}
	return nil
}

func requestAllowsUninitializedConnection(method Method) bool {
	switch method {
	case MethodInitialize,
		MethodFSWatch,
		MethodFSUnwatch,
		MethodThreadStart,
		MethodThreadResume,
		MethodThreadFork,
		MethodThreadUnsubscribe:
		return true
	default:
		return false
	}
}

func (r *RuntimeRouter) rejectExperimentalAPIDisabled(request *Request) error {
	if r == nil || request == nil || request.Method == MethodInitialize {
		return nil
	}
	if !r.connectionExperimentalAPIDisabled(request.normalizedConnectionID()) {
		return nil
	}
	reason := experimentalAPIReasonForRequest(request)
	if reason == "" {
		return nil
	}
	return jsonRPCInvalidRequest(fmt.Sprintf("%s requires experimentalApi capability", reason))
}

func experimentalAPIReasonForRequest(request *Request) string {
	if request == nil {
		return ""
	}
	if reason := experimentalAPIFieldReasonForRequest(request); reason != "" {
		return reason
	}
	if experimentalAPIMethod(request.Method) {
		return string(request.Method)
	}
	return ""
}

func experimentalAPIFieldReasonForRequest(request *Request) string {
	if request == nil {
		return ""
	}
	switch request.Method {
	case MethodThreadStart:
		if rawParamPresentNonNull(request, "mockExperimentalField") {
			return "thread/start.mockExperimentalField"
		}
		if rawParamArrayNonEmpty(request, "dynamicTools") {
			return "thread/start.dynamicTools"
		}
	case MethodTurnStart:
		if rawParamArrayNonEmpty(request, "dynamicTools") {
			return "turn/start.dynamicTools"
		}
	}
	switch request.Method {
	case MethodThreadStart, MethodThreadResume, MethodThreadFork, MethodTurnStart, MethodThreadSettingsUpdate:
		if rawApprovalPolicyGranular(request) {
			return "askForApproval.granular"
		}
	}
	return ""
}

func (r *RuntimeRouter) rejectRemoteControlDisabledByRequirements(request *Request) error {
	if r == nil || request == nil || !r.services.RemoteControlDisabledByRequirements {
		return nil
	}
	if !isRemoteControlMethod(request.Method) {
		return nil
	}
	return jsonRPCInvalidRequest("remote control is disabled by managed requirements")
}

func isRemoteControlMethod(method Method) bool {
	switch method {
	case MethodRemoteControlClientsList,
		MethodRemoteControlClientsRevoke,
		MethodRemoteControlDisable,
		MethodRemoteControlEnable,
		MethodRemoteControlPairingStart,
		MethodRemoteControlPairingStatus,
		MethodRemoteControlStatusRead:
		return true
	default:
		return false
	}
}

func experimentalAPIMethod(method Method) bool {
	switch method {
	case MethodCollaborationModeList,
		MethodEnvironmentAdd,
		MethodEnvironmentInfo,
		MethodEnvironmentStatus,
		MethodFuzzyFileSearchStart,
		MethodFuzzyFileSearchStop,
		MethodFuzzyFileSearchUpdate,
		MethodMemoryReset,
		MethodRolloutCompress,
		MethodMCPServerEventStreamStart,
		MethodMCPServerEventStreamStop,
		MethodMockExperimentalMethod,
		MethodProcessKill,
		MethodProcessResizePty,
		MethodProcessSpawn,
		MethodProcessWriteStdin,
		MethodRemoteControlClientsList,
		MethodRemoteControlClientsRevoke,
		MethodRemoteControlDisable,
		MethodRemoteControlEnable,
		MethodRemoteControlPairingStart,
		MethodRemoteControlPairingStatus,
		MethodRemoteControlStatusRead,
		MethodServerDiagnostics,
		MethodThreadBackgroundTerminalsClean,
		MethodThreadBackgroundTerminalsList,
		MethodThreadBackgroundTerminalsTerminate,
		MethodThreadDecrementElicitation,
		MethodThreadDecrementElicitationLegacy,
		MethodThreadIncrementElicitation,
		MethodThreadIncrementElicitationLegacy,
		MethodThreadItemsList,
		MethodThreadMemoryModeSet,
		MethodMemoryStatus,
		MethodThreadRealtimeAppendAudio,
		MethodThreadRealtimeAppendSpeech,
		MethodThreadRealtimeAppendText,
		MethodThreadRealtimeListVoices,
		MethodThreadRealtimeStart,
		MethodThreadRealtimeStop,
		MethodThreadSearch,
		MethodThreadSettingsUpdate,
		MethodThreadTurnsList,
		MethodUserVerificationStatus,
		MethodUserVerificationEnroll,
		MethodUserVerificationDelete,
		MethodUserVerificationVerify,
		MethodUserVerificationCancel:
		return true
	default:
		return false
	}
}

func rawParamPresentNonNull(request *Request, key string) bool {
	raw, ok := rawParam(request, key)
	if !ok {
		return false
	}
	return strings.TrimSpace(string(raw)) != "null"
}

func rawParamArrayNonEmpty(request *Request, key string) bool {
	raw, ok := rawParam(request, key)
	if !ok || strings.TrimSpace(string(raw)) == "null" {
		return false
	}
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return false
	}
	return len(values) > 0
}

func rawApprovalPolicyGranular(request *Request) bool {
	raw, ok := rawParam(request, "approvalPolicy")
	if !ok || strings.TrimSpace(string(raw)) == "null" {
		return false
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.EqualFold(strings.TrimSpace(text), "granular")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return false
	}
	if _, ok := object["granular"]; ok {
		return true
	}
	if kindRaw, ok := object["type"]; ok {
		var kind string
		if err := json.Unmarshal(kindRaw, &kind); err == nil && strings.EqualFold(strings.TrimSpace(kind), "granular") {
			return true
		}
	}
	return false
}

func rawParam(request *Request, key string) (json.RawMessage, bool) {
	if request == nil || len(request.Params) == 0 || strings.TrimSpace(key) == "" {
		return nil, false
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(request.Params, &raw); err != nil {
		return nil, false
	}
	value, ok := raw[key]
	return value, ok
}

func (r *RuntimeRouter) dispatch(request *Request) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("%w: runtime router is nil", ErrInvalidRequest)
	}
	if err := r.rejectUninitializedConnection(request); err != nil {
		return nil, err
	}
	if err := r.rejectExperimentalAPIDisabled(request); err != nil {
		return nil, err
	}
	if err := r.rejectRemoteControlDisabledByRequirements(request); err != nil {
		return nil, err
	}
	if isThreadExtraMethod(request.Method) {
		return r.dispatchThreadExtra(request)
	}
	if isRealtimeMethod(request.Method) {
		return r.dispatchRealtime(request)
	}
	if isProjectMethod(request.Method) {
		return r.dispatchProject(request)
	}
	if request.Method == MethodThreadSectionList {
		if r.services.ThreadRouter == nil {
			return nil, fmt.Errorf("%w: thread router is not configured", ErrInvalidRequest)
		}
		return r.services.ThreadRouter.dispatch(request)
	}
	if isThreadMethod(request.Method) {
		if r.services.ThreadRouter == nil {
			return nil, fmt.Errorf("%w: thread router is not configured", ErrInvalidRequest)
		}
		if request.Method == MethodThreadCompactStart {
			return r.handleThreadCompactStartRuntime(request)
		}
		if request.Method == MethodThreadApproveGuardianDeniedAction {
			if err := r.rejectNotLoadedThreadRuntimeRequest(request); err != nil {
				return nil, err
			}
			return r.handleThreadApproveGuardianDeniedActionRuntime(request)
		}
		if request.Method == MethodThreadInjectItems {
			if err := r.rejectNotLoadedThreadRuntimeRequest(request); err != nil {
				return nil, err
			}
			return r.handleThreadInjectItemsRuntime(request)
		}
		if request.Method == MethodThreadIncrementElicitation || request.Method == MethodThreadIncrementElicitationLegacy || request.Method == MethodThreadDecrementElicitation || request.Method == MethodThreadDecrementElicitationLegacy {
			if err := r.rejectNotLoadedThreadRuntimeRequest(request); err != nil {
				return nil, err
			}
			return r.handleThreadElicitationCountRuntime(request)
		}
		if request.Method == MethodThreadRead {
			return r.handleThreadReadRuntime(request)
		}
		if request.Method == MethodThreadItemsList {
			return r.handleThreadItemsListRuntime(request)
		}
		if request.Method == MethodThreadTurnsList {
			return r.handleThreadTurnsListRuntime(request)
		}
		if request.Method == MethodThreadLoadedList {
			return r.handleThreadLoadedListRuntime(request)
		}
		if request.Method == MethodThreadList {
			return r.handleThreadListRuntime(request)
		}
		if request.Method == MethodThreadSearch {
			return r.handleThreadSearchRuntime(request)
		}
		if request.Method == MethodThreadUnsubscribe {
			return r.handleThreadUnsubscribeRuntime(request)
		}
		if request.Method == MethodThreadMetadataUpdate {
			return r.handleThreadMetadataUpdateRuntime(request)
		}
		if isThreadAttachmentMethod(request.Method) {
			return r.handleThreadAttachmentRuntime(request)
		}
		if request.Method == MethodThreadRevert {
			return r.handleThreadRevertRuntime(request)
		}
		if request.Method == MethodThreadQueueStart {
			return r.handleThreadQueueStartRuntime(request)
		}
		if isThreadQueueMethod(request.Method) {
			return r.handleThreadQueueRuntime(request)
		}
		if threadMethodRequiresLoadedRuntime(request.Method) {
			if err := r.rejectNotLoadedThreadRuntimeRequest(request); err != nil {
				return nil, err
			}
		}
		if request.Method == MethodThreadResume {
			updated, updateErr := r.applyConfiguredResumeCWD(request)
			if updateErr != nil {
				return nil, updateErr
			}
			request = updated
			if err := r.rejectRunningThreadResumeHistory(request); err != nil {
				return nil, err
			}
			if err := r.rejectRunningThreadResumeStalePath(request); err != nil {
				return nil, err
			}
			result, err := r.services.ThreadRouter.dispatch(request)
			if err != nil {
				return nil, err
			}
			if response, ok := result.(*ThreadResumeResponse); ok && response.Thread != nil {
				// Rust #44655: a resumed session re-derives its active plugin
				// selection from the resumed settings until the next task.
				r.clearActiveDisabledPluginIDs(response.Thread.ID)
			}
			// resumeConfig is the resumed session's effective configuration; the
			// restored collaboration mode takes its model and reasoning effort from
			// it (Rust #45519).
			var resumeConfig *config.Config
			if response, ok := result.(*ThreadResumeResponse); ok && response.Thread != nil && !r.threadIsLoaded(response.Thread.ID) {
				cfg, configErr := r.effectiveMCPConfigForThreadResume(response, request)
				if configErr != nil {
					r.rollbackThreadResumeInitialization(response.Thread.ID, request)
					return nil, configErr
				}
				resumeConfig = cfg
				if validateErr := r.validateRequiredMCPServers(response.Thread.ID, cfg); validateErr != nil {
					r.rollbackThreadResumeInitialization(response.Thread.ID, request)
					return nil, validateErr
				}
				if snapshotErr := r.persistThreadResumeConfigSnapshot(response.Thread.ID, request); snapshotErr != nil {
					r.rollbackThreadResumeInitialization(response.Thread.ID, request)
					return nil, snapshotErr
				}
			}
			r.applyThreadResumeRuntimeWorkspaceRoots(result, request)
			r.applyThreadResumeSettingsUpdate(result, request)
			r.applyThreadResumeCollaborationMode(result, resumeConfig)
			if err := r.applyRunningThreadResumeSnapshot(result, request); err != nil {
				return nil, err
			}
			r.markThreadResumeSessionStartSource(result, request)
			r.markResponseThreadLoaded(result, request.normalizedConnectionID())
			if response, ok := result.(*ThreadResumeResponse); ok && response.Thread != nil {
				// A resume that loads a cold thread spawns a session in Rust
				// (ThreadManager::spawn_thread), so it opens the same thread_spawn
				// and session_loop spans; resuming a live thread attaches without a
				// second spawn.
				if r.threadSessionSpan(response.Thread.ID) == nil {
					if spawnSpan := r.startThreadSpawnSpan(request); spawnSpan != nil {
						r.startThreadSessionSpan(response.Thread.ID, spawnSpan)
						spawnSpan.End()
					}
				}
				r.captureThreadModelProviderRouteByID(response.Thread.ID)
				r.emitThreadResumeAnalytics(context.Background(), request.normalizedConnectionID(), response, request)
				r.emitThreadStartedMetric(context.Background(), response.Thread)
			}
			r.replayPendingServerRequestsForThread(result)
			r.notifyRestoredTokenUsage(result)
			if response, ok := result.(*ThreadResumeResponse); ok && response.Thread != nil {
				// Rust #43795: a resumed session re-establishes the selected effort.
				r.clearReasoningEffortPin(response.Thread.ID)
				r.continueThreadGoalIfIdle(response.Thread.ID)
				r.maybeDispatchQueuedSubmissionIfIdle(response.Thread.ID)
			}
			return result, nil
		}
		if isThreadLifecycleNotificationMethod(request.Method) {
			return r.handleThreadLifecycleRuntime(request)
		}
		return r.services.ThreadRouter.dispatch(request)
	}
	switch request.Method {
	case MethodInitialize:
		return r.handleInitialize(request)
	case MethodMemoryStatus:
		return r.handleMemoryStatus(request)
	case MethodTurnStart:
		return r.handleTurnStart(request)
	case MethodTurnSteer:
		return r.handleTurnSteer(request)
	case MethodTurnSettingsUpdate:
		return r.handleTurnSettingsUpdate(request)
	case MethodTurnInterrupt:
		return r.handleTurnInterrupt(request)
	case MethodReviewStart:
		return r.handleReviewStart(request)
	case MethodExperimentalFeatureList:
		return r.handleExperimentalFeatureList(request)
	case MethodExperimentalFeatureSet:
		return r.handleExperimentalFeatureSet(request)
	case MethodUserVerificationStatus, MethodUserVerificationEnroll,
		MethodUserVerificationDelete, MethodUserVerificationVerify,
		MethodUserVerificationCancel:
		return r.handleUserVerificationRuntime(request)
	case MethodAppList:
		return r.handleAppList(request)
	case MethodAppRead:
		return r.handleAppRead(request)
	case MethodAppInstalled:
		return r.handleAppInstalled(request)
	case MethodGetAuthStatus:
		return r.handleGetAuthStatus(request)
	case MethodGetConversationSummary:
		return r.handleGetConversationSummary(request)
	case MethodGitDiffToRemote:
		return r.handleGitDiffToRemote(request)
	case MethodFuzzyFileSearch:
		return r.handleFuzzyFileSearch(request)
	case MethodFuzzyFileSearchStart:
		return r.handleFuzzyFileSearchSessionStart(request)
	case MethodFuzzyFileSearchUpdate:
		return r.handleFuzzyFileSearchSessionUpdate(request)
	case MethodFuzzyFileSearchStop:
		return r.handleFuzzyFileSearchSessionStop(request)
	case MethodHooksList:
		return r.handleHooksList(request)
	case MethodSkillsList:
		return r.handleSkillsList(request)
	case MethodSkillsExtraRootsSet:
		return r.handleSkillsExtraRootsSet(request)
	case MethodSkillsConfigWrite:
		return r.handleSkillsConfigWrite(request)
	case MethodMarketplaceAdd:
		return r.handleMarketplaceAdd(request)
	case MethodMarketplaceRemove:
		return r.handleMarketplaceRemove(request)
	case MethodMarketplaceUpgrade:
		return r.handleMarketplaceUpgrade(request)
	case MethodPluginList:
		return r.handlePluginList(request)
	case MethodPluginInstalled:
		return r.handlePluginInstalled(request)
	case MethodPluginReconcile:
		return r.handlePluginReconcile(request)
	case MethodPluginRead:
		return r.handlePluginRead(request)
	case MethodPluginSkillRead:
		return r.handlePluginSkillRead(request)
	case MethodPluginShareSave:
		return r.handlePluginShareSave(request)
	case MethodPluginShareUpdateTargets:
		return r.handlePluginShareUpdateTargets(request)
	case MethodPluginShareList:
		return r.handlePluginShareList(request)
	case MethodPluginShareCheckout:
		return r.handlePluginShareCheckout(request)
	case MethodPluginShareDelete:
		return r.handlePluginShareDelete(request)
	case MethodPluginInstall:
		return r.handlePluginInstall(request)
	case MethodPluginUninstall:
		return r.handlePluginUninstall(request)
	case MethodModelList:
		return r.handleModelList(request)
	case MethodModelProviderCapabilitiesRead:
		return r.handleModelProviderCapabilitiesRead(request)
	case MethodPermissionProfileList:
		return r.handlePermissionProfileList(request)
	case MethodCollaborationModeList:
		return r.handleCollaborationModeList(request)
	case MethodMockExperimentalMethod:
		return r.handleMockExperimentalMethod(request)
	case MethodMCPServerOauthLogin:
		return r.handleMCPServerOauthLogin(request)
	case MethodMCPServerOauthCancel:
		return r.handleMCPServerOauthCancel(request)
	case MethodMCPServerRefresh, MethodConfigMCPServerReload:
		return r.handleMCPServerRefresh(request)
	case MethodMCPServerStatusList:
		return r.handleMCPServerStatusList(request)
	case MethodMCPServerResourceRead:
		return r.handleMCPServerResourceRead(request)
	case MethodMCPServerToolCall:
		return r.handleMCPServerToolCall(request)
	case MethodMCPServerEventStreamStart:
		return r.handleMCPServerEventStreamStart(request)
	case MethodMCPServerEventStreamStop:
		return r.handleMCPServerEventStreamStop(request)
	case MethodFSReadFile:
		return r.handleFSReadFile(request)
	case MethodFSWriteFile:
		return r.handleFSWriteFile(request)
	case MethodFSCreateDirectory:
		return r.handleFSCreateDirectory(request)
	case MethodFSGetMetadata:
		return r.handleFSGetMetadata(request)
	case MethodFSReadDirectory:
		return r.handleFSReadDirectory(request)
	case MethodFSRemove:
		return r.handleFSRemove(request)
	case MethodFSCopy:
		return r.handleFSCopy(request)
	case MethodFSWatch:
		return r.handleFSWatch(request)
	case MethodFSUnwatch:
		return r.handleFSUnwatch(request)
	case MethodRemoteControlEnable:
		return r.handleRemoteControlEnable(request)
	case MethodRemoteControlDisable:
		return r.handleRemoteControlDisable(request)
	case MethodRemoteControlStatusRead:
		return r.requireRemote().Status(), nil
	case MethodRemoteControlPairingStart:
		return r.handleRemoteControlPairingStart(request)
	case MethodRemoteControlPairingStatus:
		return r.handleRemoteControlPairingStatus(request)
	case MethodRemoteControlClientsList:
		return r.handleRemoteControlClientsList(request)
	case MethodRemoteControlClientsRevoke:
		return r.handleRemoteControlClientsRevoke(request)
	case MethodEnvironmentAdd:
		return r.handleEnvironmentAdd(request)
	case MethodEnvironmentInfo:
		return r.handleEnvironmentInfo(request)
	case MethodEnvironmentStatus:
		return r.handleEnvironmentStatus(request)
	case MethodWindowsSandboxSetupStart:
		return r.handleWindowsSandboxSetupStart(request)
	case MethodWindowsSandboxReadiness:
		return r.windowsSandboxReadiness()
	case MethodFeedbackUpload:
		return r.handleFeedbackUpload(request)
	case MethodConfigRead:
		return r.handleConfigRead(request)
	case MethodConfigValueWrite:
		return r.handleConfigValueWrite(request)
	case MethodConfigBatchWrite:
		return r.handleConfigBatchWrite(request)
	case MethodConfigRequirementsRead:
		return r.configRequirementsRead(), nil
	case MethodServerDiagnostics:
		return r.handleServerDiagnostics(request)
	case MethodExternalAgentConfigDetect:
		return r.handleExternalAgentConfigDetect(request)
	case MethodExternalAgentConfigImport:
		return r.handleExternalAgentConfigImport(request)
	case MethodExternalAgentConfigImportHistoryRecord:
		return r.handleExternalAgentConfigImportHistoryRecord(request)
	case MethodExternalAgentConfigImportHistoriesRead:
		return r.requireConfig().ImportHistories(), nil
	case MethodLoginAccount:
		return r.handleLoginAccount(request)
	case MethodCancelLoginAccount:
		return r.handleCancelLoginAccount(request)
	case MethodAccountSessionsAdd:
		return r.handleAccountSessionsAdd(request)
	case MethodAccountSessionsList:
		return r.handleAccountSessionsList(request)
	case MethodAccountSessionsLogout:
		return r.handleAccountSessionsLogout(request)
	case MethodAccountSessionsSwitch:
		return r.handleAccountSessionsSwitch(request)
	case MethodLogoutAccount:
		return r.handleLogoutAccount(request)
	case MethodGetAccount:
		return r.handleGetAccount(request)
	case MethodGetAccountRateLimits:
		return r.handleGetAccountRateLimits(request)
	case MethodConsumeAccountRateLimitResetCredit:
		return r.handleConsumeResetCredit(request)
	case MethodGetAccountTokenUsage:
		return r.handleGetAccountTokenUsage(request)
	case MethodGetWorkspaceMessages:
		return r.handleGetWorkspaceMessages(request)
	case MethodSendAddCreditsNudgeEmail:
		return r.handleSendAddCreditsNudgeEmail(request)
	case MethodProcessSpawn:
		return r.handleProcessSpawn(request)
	case MethodProcessWriteStdin:
		return r.handleProcessWriteStdin(request)
	case MethodProcessKill:
		return r.handleProcessKill(request)
	case MethodProcessResizePty:
		return r.handleProcessResizePty(request)
	case MethodCommandExec:
		return r.handleCommandExec(request)
	case MethodCommandExecWrite:
		return r.handleCommandExecWrite(request)
	case MethodCommandExecTerminate:
		return r.handleCommandExecTerminate(request)
	case MethodCommandExecResize:
		return r.handleCommandExecResize(request)
	case MethodRolloutCompress:
		// Rust's `rollout/compress` request processor is not thread scoped
		// (#46020); it schedules the local background pass and acknowledges.
		if r.services.ThreadRouter == nil {
			return nil, fmt.Errorf("%w: thread router is not configured", ErrInvalidRequest)
		}
		return r.services.ThreadRouter.handleRolloutCompress(request)
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnknownMethod, request.Method)
	}
}

func (r *RuntimeRouter) applyConfiguredResumeCWD(request *Request) (*Request, error) {
	if r == nil || request == nil || r.services.Config == nil {
		return request, nil
	}
	var params ThreadResumeParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if params.CWD != nil {
		return request, nil
	}
	record, err := r.threadRecord(session.ThreadID(strings.TrimSpace(params.ThreadID)), true, false)
	if err != nil {
		return request, nil
	}
	read, err := r.services.Config.Read(&config.ConfigReadParams{CWD: stringPtrIfNotEmpty(record.Metadata.CWD)})
	if err != nil {
		return nil, err
	}
	mode, err := (&config.Config{Values: read.Config}).ResumeCWDMode()
	if err != nil {
		return nil, jsonRPCInvalidRequest(err.Error())
	}
	if mode == "" {
		return request, nil
	}
	cwd := config.ResolveResumeCWD(mode, r.services.DefaultCWD, record.Metadata.CWD)
	params.CWD = stringPtrIfNotEmpty(cwd)
	return requestWithRuntimeParams(request, &params)
}

func (r *RuntimeRouter) handleThreadCompactStartRuntime(request *Request) (*ThreadCompactStartResponse, error) {
	var params ThreadCompactStartParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if err := r.requireLoadedThreadForRuntimeOp(params.ThreadID); err != nil {
		return nil, err
	}
	// Rust #44944: manual compaction is gated on the retained provider route.
	if err := r.checkThreadModelProviderForID(params.ThreadID); err != nil {
		return nil, err
	}
	started, err := r.requireTurns().Start(&turn.TurnStartParams{ThreadID: params.ThreadID, Originator: "compact"})
	if err != nil {
		return nil, err
	}
	turnRecord := &started.Turn
	turnID := strings.TrimSpace(turnRecord.ID)
	startedAt := time.Unix(turnRecord.StartedAt, 0).UTC()
	if turnRecord.StartedAt == 0 {
		startedAt = time.Now().UTC()
	}
	appTurn := appTurnFromTurnRecord(turnRecord, nil, TurnStatusInProgress, nil, nil)
	appTurn.Items = []ThreadItem{}
	appTurn.ItemsView = TurnItemsNotLoaded
	r.notifyThreadStatus(r.requireThreadStatus().NoteTurnStarted(params.ThreadID))
	r.notify(NotificationTurnStarted, &TurnStartedNotification{ThreadID: params.ThreadID, Turn: appTurn})
	_ = r.appendRuntimeTurnStarted(params.ThreadID, turnID, rootTurnIDForTurn(nil, turnID), startedAt)
	_, err = r.compactThread(context.Background(), &runtimeCompactRequest{
		ThreadID:     params.ThreadID,
		TurnID:       turnID,
		ConnectionID: request.normalizedConnectionID(),
		Trigger:      compact.TriggerManual,
		Reason:       compact.ReasonUserRequested,
		Phase:        compact.PhaseStandaloneTurn,
	})
	if err != nil {
		r.finishTurnWithError(params.ThreadID, turnID, startedAt.UnixMilli(), err)
		return nil, err
	}
	completedAt := time.Now().UTC()
	completedAtUnix := completedAt.Unix()
	durationMS := completedAt.UnixMilli() - startedAt.UnixMilli()
	_ = r.appendRuntimeTurnComplete(params.ThreadID, turnID, completedAt, durationMS)
	r.completeTurnRecord(params.ThreadID, turnID, TurnStatusCompleted)
	r.notifyTurnCompletedOnce(&TurnCompletedNotification{
		ThreadID: params.ThreadID,
		Turn: completedTurnNotificationTurn(
			turnID,
			TurnStatusCompleted,
			nil,
			&turnRecord.StartedAt,
			&completedAtUnix,
			&durationMS,
		),
	})
	r.notifyThreadStatus(r.requireThreadStatus().NoteTurnCompleted(params.ThreadID))
	return &ThreadCompactStartResponse{}, nil
}

func (r *RuntimeRouter) handleThreadTurnsListRuntime(request *Request) (*TurnsPage, error) {
	var params ThreadTurnsListParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if _, ok := r.ephemeralThreadRecord(session.ThreadID(params.ThreadID), false); ok {
		return nil, jsonRPCInvalidRequest("ephemeral threads do not support thread/turns/list")
	}
	if r.services.ThreadRouter != nil && r.services.StateRuntime != nil {
		mode, found, err := r.services.ThreadRouter.threadHistoryModeWithRepair(session.ThreadID(params.ThreadID))
		if err != nil {
			return nil, err
		}
		if found && strings.EqualFold(strings.TrimSpace(mode), string(ThreadHistoryPaginated)) {
			activeTurn := r.activeRuntimeTurnSnapshot(params.ThreadID)
			return r.services.ThreadRouter.buildPaginatedThreadTurnsResponse(&params, &turnsResponseOptions{
				LoadedStatus:         r.requireThreadStatus().LoadedStatusForThread(params.ThreadID),
				HasLiveRunningThread: activeTurn != nil && activeTurn.Status == TurnStatusInProgress,
			})
		}
	}
	record, err := r.threadRecord(session.ThreadID(params.ThreadID), true, true)
	if err != nil {
		return nil, threadTurnsListReadError(params.ThreadID, err)
	}
	if unmaterializedThread(record) {
		return nil, jsonRPCInvalidRequest(fmt.Sprintf("thread %s is not materialized yet; thread/turns/list is unavailable before first user message", record.ID))
	}
	activeTurn := r.activeRuntimeTurnSnapshot(params.ThreadID)
	return buildTurnsResponse(record, &params, &turnsResponseOptions{
		ActiveTurn:   activeTurn,
		LoadedStatus: r.requireThreadStatus().LoadedStatusForThread(params.ThreadID),
	})
}

func (r *RuntimeRouter) handleThreadItemsListRuntime(request *Request) (*ThreadItemsListResponse, error) {
	var params ThreadItemsListParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if _, ok := r.ephemeralThreadRecord(session.ThreadID(params.ThreadID), false); ok {
		return nil, jsonRPCInvalidRequest("ephemeral threads do not support thread/items/list")
	}
	if r.services.ThreadRouter != nil && r.services.StateRuntime != nil {
		return r.services.ThreadRouter.handleThreadItemsList(request)
	}
	record, err := r.threadRecord(session.ThreadID(params.ThreadID), true, true)
	if err != nil {
		return nil, threadItemsListReadError(params.ThreadID, err)
	}
	if unmaterializedThread(record) {
		return nil, jsonRPCInvalidRequest(fmt.Sprintf("thread %s is not materialized yet; thread/items/list is unavailable before first user message", record.ID))
	}
	return BuildItemsResponse(record, &params)
}

// runInterruptHook discovers and runs the managed Interrupt hooks for a turn
// that is being interrupted, mirroring Rust #40511.
func (r *RuntimeRouter) runInterruptHook(active *activeRuntimeTurn) {
	if r == nil || active == nil {
		return
	}
	cwd := ""
	model := ""
	permissionMode := ""
	var params *turn.TurnStartParams
	if active.Params != nil {
		params = active.Params
		cwd = strings.TrimSpace(params.CWD)
	}
	model, permissionMode = r.hookTurnAttribution(params)
	hooks := r.interruptHooksForCWD(cwd, active.ThreadID)
	if len(hooks) == 0 {
		return
	}
	_, err := r.requireHookRunner().RunInterrupt(context.Background(), &HookInterruptRequest{
		ThreadID:       active.ThreadID,
		TurnID:         active.TurnID,
		CWD:            cwd,
		Model:          model,
		PermissionMode: permissionMode,
		Hooks:          hooks,
	})
	if err != nil {
		slog.Warn("interrupt hook failed", "thread_id", active.ThreadID, "turn_id", active.TurnID, "error", err)
	}
}

func (r *RuntimeRouter) interruptHooksForCWD(cwd string, threadID string) []HookMetadata {
	if r == nil {
		return nil
	}
	hooks := r.hooksForCWD(cwd, threadID)
	out := make([]HookMetadata, 0, len(hooks))
	for _, hook := range hooks {
		if hook.EventName == HookEventInterrupt {
			out = append(out, hook)
		}
	}
	return out
}

func (r *RuntimeRouter) handleThreadRevertRuntime(request *Request) (*ThreadRevertResponse, error) {
	if r == nil || r.services.ThreadRouter == nil {
		return nil, fmt.Errorf("%w: thread router is not configured", ErrInvalidRequest)
	}
	var params ThreadRevertParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if err := r.requireLoadedThreadForRuntimeOp(params.ThreadID); err != nil {
		return nil, err
	}
	record, err := r.threadRecord(session.ThreadID(params.ThreadID), true, false)
	if err != nil {
		return nil, threadReadError(params.ThreadID, err)
	}
	if !threadUsesPaginatedHistory(record) {
		return nil, jsonRPCInvalidRequest("thread/revert only supports paginated threads")
	}
	if active := r.activeRuntimeTurnSnapshot(params.ThreadID); active != nil {
		if _, err := r.handleTurnInterrupt(requestWithInternalParams(MethodTurnInterrupt, turn.TurnInterruptParams{ThreadID: params.ThreadID, TurnID: active.ID})); err != nil {
			return nil, fmt.Errorf("failed to interrupt active turn before revert: %w", err)
		}
	}
	result, err := r.services.ThreadRouter.dispatch(request)
	if err != nil {
		return nil, err
	}
	response, ok := result.(*ThreadRevertResponse)
	if !ok {
		return nil, fmt.Errorf("%w: unexpected thread/revert response %T", ErrInvalidRequest, result)
	}
	r.clearNodeReplReviewEvidence(params.ThreadID)
	r.notify(NotificationThreadReverted, &ThreadIDNotification{ThreadID: params.ThreadID})
	return response, nil
}

func isThreadQueueMethod(method Method) bool {
	switch method {
	case MethodThreadQueueAdd, MethodThreadQueueList, MethodThreadQueueUpdate, MethodThreadQueueDelete, MethodThreadQueueReorder:
		return true
	default:
		return false
	}
}

func (r *RuntimeRouter) handleThreadQueueRuntime(request *Request) (any, error) {
	if r == nil || r.services.ThreadRouter == nil {
		return nil, fmt.Errorf("%w: thread router is not configured", ErrInvalidRequest)
	}
	var threadID string
	directInputGuarded := false
	switch request.Method {
	case MethodThreadQueueAdd:
		var params ThreadQueueAddParams
		if err := request.DecodeParams(&params); err != nil {
			return nil, err
		}
		if err := params.Validate(); err != nil {
			return nil, err
		}
		threadID = strings.TrimSpace(params.ThreadID)
		directInputGuarded = true
	case MethodThreadQueueList:
		var params ThreadQueueListParams
		if err := request.DecodeParams(&params); err != nil {
			return nil, err
		}
		if err := params.Validate(); err != nil {
			return nil, err
		}
		threadID = strings.TrimSpace(params.ThreadID)
	case MethodThreadQueueUpdate:
		var params ThreadQueueUpdateParams
		if err := request.DecodeParams(&params); err != nil {
			return nil, err
		}
		if err := params.Validate(); err != nil {
			return nil, err
		}
		threadID = strings.TrimSpace(params.ThreadID)
		directInputGuarded = true
	case MethodThreadQueueDelete:
		var params ThreadQueueDeleteParams
		if err := request.DecodeParams(&params); err != nil {
			return nil, err
		}
		if err := params.Validate(); err != nil {
			return nil, err
		}
		threadID = strings.TrimSpace(params.ThreadID)
	case MethodThreadQueueReorder:
		var params ThreadQueueReorderParams
		if err := request.DecodeParams(&params); err != nil {
			return nil, err
		}
		if err := params.Validate(); err != nil {
			return nil, err
		}
		threadID = strings.TrimSpace(params.ThreadID)
	}
	if directInputGuarded {
		if err := r.ensureDirectInputAllowed(request, threadID); err != nil {
			return nil, err
		}
	}
	if err := r.requireLoadedThreadForRuntimeOp(threadID); err != nil {
		return nil, err
	}
	result, err := r.services.ThreadRouter.dispatch(request)
	if err != nil {
		return nil, err
	}
	if request.Method != MethodThreadQueueList {
		r.notify(NotificationThreadQueueChanged, &ThreadIDNotification{ThreadID: threadID})
	}
	return result, nil
}

func (r *RuntimeRouter) handleThreadQueueStartRuntime(request *Request) (*ThreadQueueStartResponse, error) {
	if r == nil || r.services.ThreadRouter == nil {
		return nil, fmt.Errorf("%w: thread router is not configured", ErrInvalidRequest)
	}
	var params ThreadQueueStartParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	threadID := strings.TrimSpace(params.ThreadID)
	if err := r.ensureDirectInputAllowed(request, threadID); err != nil {
		return nil, err
	}
	// Rust #44944: starting a queued message admits a turn, so the retained
	// provider route must still match managed requirements.
	if err := r.checkThreadModelProviderForID(threadID); err != nil {
		return nil, err
	}
	if err := r.requireLoadedThreadForRuntimeOp(threadID); err != nil {
		return nil, err
	}
	submissionID := ""
	if params.QueuedSubmissionID != nil {
		submissionID = strings.TrimSpace(*params.QueuedSubmissionID)
	}
	submission, err := r.services.ThreadRouter.store.DequeueSubmission(session.ThreadID(threadID), submissionID)
	if err != nil {
		return nil, threadQueueError(err)
	}
	if submission == nil {
		return nil, jsonRPCInvalidRequest("queue is empty")
	}
	input, err := turnUserInputsFromQueuedSubmission(submission.Input)
	if err != nil {
		return nil, jsonRPCInvalidRequest("queued submission payload is invalid")
	}
	startParams, err := json.Marshal(turn.TurnStartParams{ThreadID: threadID, Input: input})
	if err != nil {
		return nil, err
	}
	startRequest := &Request{
		JSONRPC:  "2.0",
		ID:       request.ID,
		Method:   MethodTurnStart,
		Params:   startParams,
		Internal: request.Internal,
	}
	response, err := r.handleTurnStart(startRequest)
	if err != nil {
		return nil, err
	}
	r.notify(NotificationThreadQueueChanged, &ThreadIDNotification{ThreadID: threadID})
	return &ThreadQueueStartResponse{Turn: &response.Turn}, nil
}

func turnUserInputsFromQueuedSubmission(input []any) ([]turn.TurnUserInput, error) {
	if len(input) == 0 {
		return nil, nil
	}
	data, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	var items []turn.TurnUserInput
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	return items, nil
}

// maybeDispatchNextQueuedSubmission dispatches the next queued submission in
// FIFO order after a turn completes or fails (Rust #38456). It does nothing for
// interrupted turns, which leave the queue paused.
func (r *RuntimeRouter) maybeDispatchNextQueuedSubmission(threadID string) {
	if r == nil || r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil {
		return
	}
	submission, err := r.services.ThreadRouter.store.DequeueFirstSubmission(session.ThreadID(strings.TrimSpace(threadID)))
	if err != nil || submission == nil {
		return
	}
	input, err := turnUserInputsFromQueuedSubmission(submission.Input)
	if err != nil {
		return
	}
	r.notify(NotificationThreadQueueChanged, &ThreadIDNotification{ThreadID: threadID})
	_, _ = r.handleTurnStart(requestWithInternalParams(MethodTurnStart, turn.TurnStartParams{ThreadID: threadID, Input: input}))
}

// maybeDispatchQueuedSubmissionIfIdle discovers pending queued work when a
// thread is loaded or resumed and idle (Rust #39034: wake loaded idle threads
// with pending external messages). The per-queue revision table and
// PRAGMA data_version polling are structural N/A for Go, whose durable queue
// lives in the thread record and is re-read from the store on every check, so
// cross-process writes are observed on load/resume.
// refreshModelCatalogAfterAuthChange mirrors Rust #46508's pre-turn best-effort
// catalog refresh (ModelsManager::refresh_after_auth_change): a credential
// change must not leave the in-memory catalog associated with the previous
// identity, or a turn would sample with the wrong model metadata.
//
// Go's account-scoped manager captures its identity and endpoint auth when it is
// built, while Rust's endpoint client reads the live auth manager, so Go's
// equivalent of Rust's in-place refresh is rebuilding the manager under the
// current credentials. A matching identity, an unresolvable identity, or a
// failed rebuild all keep the running catalog.
func (r *RuntimeRouter) refreshModelCatalogAfterAuthChange() {
	if r == nil || r.services.Models == nil || r.services.Config == nil {
		return
	}
	codexHome := strings.TrimSpace(r.services.Config.CodexHome())
	if codexHome == "" {
		return
	}
	read, err := r.services.Config.Read(&config.ConfigReadParams{})
	if err != nil || read == nil {
		return
	}
	requirements := r.services.Config.Requirements()
	current, err := accountScopedCatalogIdentity(codexHome, read, requirements)
	running := r.services.Models.CatalogIdentity()
	// An empty identity on either side cannot be compared: Go skips the
	// credential check and leaves the manager's own claim check to
	// RefreshAfterAuthChange below.
	if err != nil || current == "" || running == "" || current == running {
		r.services.Models.RefreshAfterAuthChange()
		return
	}
	manager, err := buildAccountScopedModelsManager(codexHome, read, requirements)
	if err != nil || manager == nil {
		// Rust keeps the existing cache or bundled fallback when discovery fails.
		r.services.Models.RefreshAfterAuthChange()
		return
	}
	// Claim the rebuilt catalog under the current credentials before the turn
	// samples, mirroring Rust's refresh-before-use.
	service := model.NewModelService(manager)
	service.RefreshAfterAuthChange()
	r.servicesMu.Lock()
	r.services.Models = service
	r.servicesMu.Unlock()
}

func (r *RuntimeRouter) maybeDispatchQueuedSubmissionIfIdle(threadID string) {
	if r == nil || r.threads == nil || r.threads.IsClosing() {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" || r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil {
		return
	}
	if r.threads.ActiveTurn(threadID) != nil {
		return
	}
	if status := r.requireThreadStatus().LoadedStatusForThread(threadID); status.Type != IdleStatus().Type {
		return
	}
	// Rust #46508: refresh before the wakeup starts a queued turn, then make
	// sure the wakeup is still the one that will start it.
	r.refreshModelCatalogAfterAuthChange()
	if r.threads.ActiveTurn(threadID) != nil {
		return
	}
	pending, _, err := r.services.ThreadRouter.store.ListQueueSubmissions(session.ThreadID(threadID), "", 1)
	if err != nil || len(pending) == 0 {
		return
	}
	r.maybeDispatchNextQueuedSubmission(threadID)
}

func (r *RuntimeRouter) handleExternalAgentConfigImportHistoryRecord(request *Request) (*config.ExternalAgentConfigImportHistoryRecordResponse, error) {
	var params config.ExternalAgentConfigImportHistoryRecordParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	return r.requireConfig().RecordExternalAgentImportHistory(&params), nil
}

func threadMethodRequiresLoadedRuntime(method Method) bool {
	switch method {
	case MethodThreadIncrementElicitation, MethodThreadIncrementElicitationLegacy,
		MethodThreadDecrementElicitation, MethodThreadDecrementElicitationLegacy,
		MethodThreadApproveGuardianDeniedAction,
		MethodThreadInjectItems:
		return true
	default:
		return false
	}
}

func (r *RuntimeRouter) rejectNotLoadedThreadRuntimeRequest(request *Request) error {
	var params struct {
		ThreadID string `json:"threadId"`
	}
	if request == nil {
		return nil
	}
	if err := request.DecodeParams(&params); err != nil {
		return err
	}
	if strings.TrimSpace(params.ThreadID) == "" {
		return nil
	}
	return r.requireLoadedThreadForRuntimeOp(params.ThreadID)
}

func (r *RuntimeRouter) requireLoadedThreadForRuntimeOp(threadID string) error {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	if r.requireThreadStatus().LoadedStatusForThread(threadID).Type == NotLoadedStatus().Type {
		return jsonRPCInvalidRequest(fmt.Sprintf("thread not found: %s", threadID))
	}
	return nil
}

func (r *RuntimeRouter) handleThreadLoadedListRuntime(request *Request) (*ThreadLoadedListResponse, error) {
	var params ThreadLoadedListParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	ids := r.requireThreadStatus().LoadedThreadIDs()
	if len(ids) == 0 {
		return &ThreadLoadedListResponse{Data: []string{}}, nil
	}
	sort.Strings(ids)
	total := len(ids)
	start := 0
	if params.Cursor != nil && strings.TrimSpace(*params.Cursor) != "" {
		cursor := strings.TrimSpace(*params.Cursor)
		index := sort.SearchStrings(ids, cursor)
		if index < total && ids[index] == cursor {
			start = index + 1
		} else {
			start = index
		}
	}
	if start >= total {
		return &ThreadLoadedListResponse{Data: []string{}}, nil
	}
	limit := total - start
	if params.Limit != nil {
		limit = *params.Limit
		if limit < 1 {
			limit = 1
		}
	}
	end := start + limit
	if end > total {
		end = total
	}
	page := append([]string(nil), ids[start:end]...)
	var next *string
	if end < total && len(page) > 0 {
		value := page[len(page)-1]
		next = &value
	}
	return &ThreadLoadedListResponse{Data: page, NextCursor: next}, nil
}

func (r *RuntimeRouter) handleThreadReadRuntime(request *Request) (*ThreadReadResponse, error) {
	var params ThreadReadParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if record, ok := r.ephemeralThreadRecord(session.ThreadID(params.ThreadID), false); ok {
		if params.IncludeTurns {
			return nil, jsonRPCInvalidRequest("ephemeral threads do not support includeTurns")
		}
		thread := BuildThread(record, "", false)
		if thread != nil {
			thread.Status = r.requireThreadStatus().LoadedStatusForThread(params.ThreadID)
		}
		return &ThreadReadResponse{Thread: thread}, nil
	}
	result, err := r.services.ThreadRouter.dispatch(request)
	if err != nil {
		return nil, err
	}
	response, ok := result.(*ThreadReadResponse)
	if !ok || response == nil || response.Thread == nil {
		return response, nil
	}
	loadedStatus := r.requireThreadStatus().LoadedStatusForThread(params.ThreadID)
	response.Thread.Status = loadedStatus
	if params.IncludeTurns {
		normalizeThreadTurnsStatus(response.Thread.Turns, loadedStatus, r.activeRuntimeTurnSnapshot(params.ThreadID) != nil)
	}
	return response, nil
}

func (r *RuntimeRouter) handleThreadUnsubscribeRuntime(request *Request) (*ThreadUnsubscribeResponse, error) {
	var params ThreadUnsubscribeParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if r.requireThreadStatus().LoadedStatusForThread(params.ThreadID).Type == NotLoadedStatus().Type {
		r.clearThreadSubscriptions(params.ThreadID)
		return &ThreadUnsubscribeResponse{Status: ThreadUnsubscribeStatusNotLoaded}, nil
	}
	if r.unsubscribeThreadConnection(params.ThreadID, request.normalizedConnectionID()) {
		if r.mcpEventStreams != nil {
			r.mcpEventStreams.stopThreadConnection(params.ThreadID, request.normalizedConnectionID())
		}
		return &ThreadUnsubscribeResponse{Status: ThreadUnsubscribeStatusUnsubscribed}, nil
	}
	return &ThreadUnsubscribeResponse{Status: ThreadUnsubscribeStatusNotSubscribed}, nil
}

func (r *RuntimeRouter) handleThreadMetadataUpdateRuntime(request *Request) (*ThreadMetadataUpdateResponse, error) {
	var params ThreadMetadataUpdateParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if _, ok := r.ephemeralThreadRecord(session.ThreadID(params.ThreadID), false); ok {
		return nil, jsonRPCInvalidRequest(fmt.Sprintf("ephemeral thread does not support metadata updates: %s", params.ThreadID))
	}
	projectUpdate, projectChanged, projectUpdateErr := MetadataProjectUpdate(&params)
	if projectUpdateErr != nil {
		return nil, projectUpdateErr
	}
	if projectChanged && projectUpdate != nil {
		if r.services.StateRuntime != nil {
			project, projectErr := r.services.StateRuntime.GetProject(context.Background(), *projectUpdate)
			if projectErr != nil {
				return nil, fmt.Errorf("failed to read project: %w", projectErr)
			}
			if project == nil {
				return nil, jsonRPCInvalidRequest(fmt.Sprintf("project not found: %s", *projectUpdate))
			}
		}
	}
	result, err := r.services.ThreadRouter.dispatch(request)
	if err != nil {
		return nil, err
	}
	response, _ := result.(*ThreadMetadataUpdateResponse)
	if projectChanged && r.services.StateRuntime != nil {
		if _, _, projectErr := r.services.StateRuntime.SetThreadProject(context.Background(), params.ThreadID, projectUpdate); projectErr != nil {
			return nil, fmt.Errorf("failed to update thread project: %w", projectErr)
		}
		r.notifyThreadProjectUpdated(request, params.ThreadID, projectUpdate)
	}
	return response, nil
}

func isThreadLifecycleNotificationMethod(method Method) bool {
	switch method {
	case MethodThreadStart, MethodThreadFork, MethodThreadArchive, MethodThreadUnarchive, MethodThreadDelete, MethodThreadSetName, MethodThreadNameSet:
		return true
	default:
		return false
	}
}

func (r *RuntimeRouter) rejectRunningThreadResumeHistory(request *Request) error {
	if r == nil || request == nil {
		return nil
	}
	var params ThreadResumeParams
	if err := request.DecodeParams(&params); err != nil {
		return err
	}
	threadID := strings.TrimSpace(params.ThreadID)
	if threadID == "" || len(params.History) == 0 {
		return nil
	}
	if r.activeRuntimeTurnSnapshot(threadID) == nil {
		return nil
	}
	return jsonRPCInvalidRequest(fmt.Sprintf("cannot resume thread %s with history while it is already running", threadID))
}

func (r *RuntimeRouter) rejectRunningThreadResumeStalePath(request *Request) error {
	if r == nil || request == nil {
		return nil
	}
	var params ThreadResumeParams
	if err := request.DecodeParams(&params); err != nil {
		return err
	}
	threadID := strings.TrimSpace(params.ThreadID)
	if threadID == "" || params.Path == nil || strings.TrimSpace(*params.Path) == "" {
		return nil
	}
	if r.activeRuntimeTurnSnapshot(threadID) == nil {
		return nil
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, false)
	if err != nil || record == nil || r.services.ThreadRouter == nil {
		return err
	}
	activePath := canonicalThreadLifecyclePath(r.services.ThreadRouter.threadRolloutPath(record), codexHomeFromSessionStore(r.services.ThreadRouter.store))
	requestPath := canonicalThreadLifecyclePath(*params.Path, codexHomeFromSessionStore(r.services.ThreadRouter.store))
	if activePath == "" || requestPath == "" || sameAppPath(activePath, requestPath) {
		return nil
	}
	return jsonRPCInvalidRequest(fmt.Sprintf("cannot resume running thread %s with stale path: requested `%s`, active `%s`", threadID, strings.TrimSpace(*params.Path), activePath))
}

func canonicalThreadLifecyclePath(path string, codexHome string) string {
	path = normalizeWindowsExtendedPathPrefix(strings.TrimSpace(path))
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) && strings.TrimSpace(codexHome) != "" {
		path = filepath.Join(codexHome, path)
	}
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	if canonical, err := filepath.EvalSymlinks(path); err == nil {
		path = canonical
	}
	return filepath.Clean(path)
}

func normalizeWindowsExtendedPathPrefix(path string) string {
	switch {
	case strings.HasPrefix(path, `\\?\UNC\`):
		return `\\` + strings.TrimPrefix(path, `\\?\UNC\`)
	case strings.HasPrefix(path, `\\?\`):
		return strings.TrimPrefix(path, `\\?\`)
	default:
		return path
	}
}

func (r *RuntimeRouter) handleThreadListRuntime(request *Request) (any, error) {
	if r == nil || request == nil || r.services.ThreadRouter == nil {
		return nil, fmt.Errorf("%w: thread router is not configured", ErrInvalidRequest)
	}
	var params ThreadListParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if runtimeThreadListShouldDefaultModelProvider(request, &params) {
		if provider := strings.TrimSpace(r.runtimeThreadListDefaultModelProvider()); provider != "" {
			params.ModelProviders = []string{provider}
			updated, err := requestWithRuntimeParams(request, params)
			if err != nil {
				return nil, err
			}
			request = updated
		}
	}
	result, err := r.services.ThreadRouter.dispatch(request)
	if err != nil {
		return nil, err
	}
	r.applyRuntimeThreadStatuses(result)
	return result, nil
}

func (r *RuntimeRouter) handleThreadSearchRuntime(request *Request) (any, error) {
	if r == nil || request == nil || r.services.ThreadRouter == nil {
		return nil, fmt.Errorf("%w: thread router is not configured", ErrInvalidRequest)
	}
	result, err := r.services.ThreadRouter.dispatch(request)
	if err != nil {
		return nil, err
	}
	r.applyRuntimeThreadStatuses(result)
	return result, nil
}

func (r *RuntimeRouter) applyRuntimeThreadStatuses(result any) {
	if r == nil {
		return
	}
	switch response := result.(type) {
	case *ThreadListResponse:
		if response == nil {
			return
		}
		for i := range response.Data {
			r.applyRuntimeThreadStatus(&response.Data[i])
		}
	case *ThreadSearchResponse:
		if response == nil {
			return
		}
		for i := range response.Data {
			r.applyRuntimeThreadStatus(&response.Data[i].Thread)
		}
	}
}

func (r *RuntimeRouter) applyRuntimeThreadStatus(thread *Thread) {
	if r == nil || thread == nil || thread.ID == "" {
		return
	}
	status := r.requireThreadStatus().LoadedStatusForThread(thread.ID)
	if status.Type == NotLoadedStatus().Type {
		return
	}
	thread.Status = status
	record, err := r.threadRecord(session.ThreadID(thread.ID), true, false)
	if err != nil || record == nil || !runtimeRecordIsThreadSpawn(record) {
		return
	}
	canAccept := !strings.EqualFold(strings.TrimSpace(record.Metadata.MultiAgentVersion), string(agent.VersionV2))
	thread.CanAcceptDirectInput = &canAccept
}

func runtimeRecordIsThreadSpawn(record *session.Record) bool {
	if record == nil {
		return false
	}
	threadSource := strings.ToLower(strings.NewReplacer("_", "", "-", "", "/", "", ":", "", " ", "").Replace(strings.TrimSpace(record.Metadata.ThreadSource)))
	source := strings.ToLower(strings.NewReplacer("_", "", "-", "", "/", "", ":", "", " ", "").Replace(strings.TrimSpace(record.Metadata.Source)))
	return threadSource == "subagentthreadspawn" || source == "subagentthreadspawn"
}

// ensureDirectInputAllowed mirrors Rust's ensure_direct_input_allowed: direct
// app-server input (turn start, steer, queued input, settings updates) is
// rejected for multi-agent V2 thread-spawn subagents unless the request is
// internal. The same policy drives thread CanAcceptDirectInput reporting.
func (r *RuntimeRouter) ensureDirectInputAllowed(request *Request, threadID string) error {
	if r == nil || request == nil || request.Internal {
		return nil
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, false)
	if err != nil || record == nil || !runtimeRecordIsThreadSpawn(record) {
		return nil
	}
	if strings.EqualFold(strings.TrimSpace(record.Metadata.MultiAgentVersion), string(agent.VersionV2)) {
		return jsonRPCInvalidRequest("direct app-server input is not allowed for multi-agent v2 sub-agents")
	}
	return nil
}

// runtimeRecordIsSubagent reports whether a thread is a subagent (delegate)
// thread: agent-tool spawns (subAgentThreadSpawn / Originator=subagent), the
// generic subagent source, and the subagent review/compact/other and memory
// consolidation kinds. Rust 95aada11c4 (#38205) requires every delegated
// session to run with the `never` approval policy; this predicate feeds the
// central enforcement in prepareTurnStartParams so approval-requiring commands
// and MCP tool calls are denied within the delegate instead of being forwarded
// to the parent session.
func runtimeRecordIsSubagent(record *session.Record) bool {
	if record == nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(record.Metadata.Originator), "subagent") {
		return true
	}
	normalize := func(value string) string {
		return strings.ToLower(strings.NewReplacer("_", "", "-", "", "/", "", ":", "", " ", "").Replace(strings.TrimSpace(value)))
	}
	switch normalize(record.Metadata.ThreadSource) {
	case "subagent", "subagentreview", "subagentcompact", "subagentother", "subagentthreadspawn", "memoryconsolidation":
		return true
	}
	switch normalize(record.Metadata.Source) {
	case "subagent", "subagentreview", "subagentcompact", "subagentother", "subagentthreadspawn":
		return true
	}
	return false
}

// turnThreadIsSubagent reports whether a turn's thread is a delegated subagent,
// mirroring Rust's SessionSource::is_non_root_agent(). A missing store, unknown
// thread, or lookup error fails open (treated as a root agent) so a transient
// store issue never silently strips root-only tools.
func (r *RuntimeRouter) turnThreadIsSubagent(threadID string) bool {
	if r == nil || r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil {
		return false
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return false
	}
	record, err := r.threadRecord(session.ThreadID(threadID), false, false)
	if err != nil || record == nil {
		return false
	}
	return runtimeRecordIsSubagent(record)
}

// freeformAsyncToolAvailable mirrors Rust #45124's condition for registering
// the free-form send_message_to_user_async tool: the turn must belong to a root
// agent, and either the model catalog must advertise the tool or the
// send_message_to_user_async feature must be enabled.
func freeformAsyncToolAvailable(rootAgent bool, catalogTools []string, featureEnabled bool) bool {
	if !rootAgent {
		return false
	}
	if featureEnabled {
		return true
	}
	for _, name := range catalogTools {
		if name == tool.DefaultSendMessageToUserAsyncToolName {
			return true
		}
	}
	return false
}

func runtimeThreadListShouldDefaultModelProvider(request *Request, params *ThreadListParams) bool {
	if request == nil || params == nil {
		return false
	}
	if params.ParentThreadID != nil || params.AncestorThreadID != nil {
		return false
	}
	if len(params.ModelProviders) > 0 {
		return false
	}
	raw, ok := rawParam(request, "modelProviders")
	if !ok {
		return true
	}
	return strings.TrimSpace(string(raw)) == "null"
}

func (r *RuntimeRouter) runtimeThreadListDefaultModelProvider() string {
	if r == nil {
		return model.OpenAIProviderID
	}
	read, err := r.requireConfig().Read(&config.ConfigReadParams{})
	if err != nil || read == nil {
		return model.OpenAIProviderID
	}
	return firstNonEmpty(stringFromMap(read.Config, "model_provider"), stringFromMap(read.Config, "modelProvider"), model.OpenAIProviderID)
}

func requestWithRuntimeParams(request *Request, params any) (*Request, error) {
	if request == nil {
		return nil, fmt.Errorf("%w: request is nil", ErrInvalidRequest)
	}
	data, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	updated := *request
	updated.Params = data
	return &updated, nil
}

func (r *RuntimeRouter) handleThreadLifecycleRuntime(request *Request) (any, error) {
	lifecycleIDs := r.lifecycleSubtreeIDs(request)
	lifecycleRecords := r.lifecycleRecordSnapshots(lifecycleIDsWithFallback(request, lifecycleIDs))
	var result any
	var err error
	// Rust spawns (or resumes) a session inside `thread_spawn`, parented to the
	// spawning request's W3C carrier, and keeps the session's `session_loop` span
	// open for the thread's life (session/mod.rs), so every turn's spans nest
	// under it.
	spawning := r.threadSpawnTarget(request)
	var threadSpawnSpan *telemetry.Span
	if spawning {
		threadSpawnSpan = r.startThreadSpawnSpan(request)
		if threadSpawnSpan != nil {
			defer threadSpawnSpan.End()
		}
	}
	// Rust app-server thread_processor: the thread/start startup phase
	// breakdown ("thread_start_create_thread" and "thread_start_total").
	threadStartStartedAt := time.Now().UTC()
	createThreadStartedAt := threadStartStartedAt
	if request.Method == MethodThreadStart {
		var params ThreadStartParams
		if err := request.DecodeParams(&params); err != nil {
			return nil, err
		}
		if err := threadLifecycleSandboxPermissionsError(params.Permissions, params.Sandbox); err != nil {
			return nil, err
		}
		if err := r.validateThreadStartEnvironments(&params); err != nil {
			return nil, err
		}
		if params.ProjectID != nil {
			if strings.TrimSpace(*params.ProjectID) == "" {
				return nil, jsonRPCInvalidRequest("projectId must not be empty")
			}
			if r.services.StateRuntime != nil {
				project, projectErr := r.services.StateRuntime.GetProject(context.Background(), *params.ProjectID)
				if projectErr != nil {
					return nil, fmt.Errorf("failed to read project: %w", projectErr)
				}
				if project == nil {
					return nil, jsonRPCInvalidRequest(fmt.Sprintf("project not found: %s", *params.ProjectID))
				}
			}
		}
		if errors := r.requireHooksDiscovery().ManagedRequiredHookLoadErrors(r.effectiveThreadStartCWD(&params)); len(errors) > 0 {
			return nil, fmt.Errorf("failed to load required managed hooks: %s", strings.Join(errors, "; "))
		}
		var handled bool
		result, handled, err = r.handleEphemeralThreadStartRuntime(request)
		if err != nil {
			return nil, err
		}
		if !handled {
			result, err = r.services.ThreadRouter.dispatch(request)
		}
		if err == nil {
			r.recordStartupPhase("thread_start_create_thread", time.Since(createThreadStartedAt), "ready")
		}
	} else if request.Method == MethodThreadFork {
		var handled bool
		result, handled, err = r.handleActiveThreadForkRuntime(request)
		if err != nil {
			return nil, err
		}
		if !handled {
			result, handled, err = r.handleEphemeralThreadForkRuntime(request)
			if err != nil {
				return nil, err
			}
		}
		if !handled {
			result, err = r.services.ThreadRouter.dispatch(request)
		}
	} else if request.Method == MethodThreadDelete {
		if err := r.rejectEphemeralThreadDelete(request); err != nil {
			return nil, err
		}
		result, err = r.services.ThreadRouter.dispatch(request)
	} else {
		result, err = r.services.ThreadRouter.dispatch(request)
	}
	if err != nil {
		return nil, err
	}
	switch request.Method {
	case MethodThreadStart, MethodThreadFork:
		if response, ok := result.(*ThreadStartResponse); ok && response.Thread != nil {
			// The new thread's session loop starts with the spawn and outlives
			// this request.
			r.startThreadSessionSpan(response.Thread.ID, threadSpawnSpan)
			if request.Method == MethodThreadStart {
				cfg, configErr := r.effectiveMCPConfigForThreadStartRequest(request)
				if configErr != nil {
					r.rollbackThreadStartInitialization(response.Thread.ID)
					return nil, configErr
				}
				if validateErr := r.validateRequiredMCPServers(response.Thread.ID, cfg); validateErr != nil {
					r.rollbackThreadStartInitialization(response.Thread.ID)
					return nil, validateErr
				}
			}
			r.applyThreadStartConfigSnapshot(response, request)
			if err := r.applyThreadStartInstructionSources(response, request); err != nil {
				r.rollbackThreadStartInitialization(response.Thread.ID)
				return nil, err
			}
			r.applyThreadStartOriginator(response, request)
			r.markRuntimeSeedRollout(response, request)
			r.markResponseThreadLoaded(response, request.normalizedConnectionID())
			if request.Method == MethodThreadStart {
				// Rust reports the session-start telemetry when the core session is
				// constructed, before the thread-started event.
				var startParams ThreadStartParams
				_ = request.DecodeParams(&startParams)
				r.emitConversationStartsRecords(context.Background(), response.Thread.ID,
					r.effectiveConfigForSessionTelemetry(), &startParams)
			}
			// Rust #44944: retain the provider route this thread was admitted with.
			r.captureThreadModelProviderRouteByID(response.Thread.ID)
			if request.Method == MethodThreadStart && r.services.StateRuntime != nil {
				var startParams ThreadStartParams
				if request.DecodeParams(&startParams) == nil && startParams.ProjectID != nil {
					projectID := strings.TrimSpace(*startParams.ProjectID)
					if _, _, projectErr := r.services.StateRuntime.SetThreadProject(context.Background(), response.Thread.ID, &projectID); projectErr != nil {
						r.rollbackThreadStartInitialization(response.Thread.ID)
						return nil, fmt.Errorf("failed to assign thread project: %w", projectErr)
					}
					r.notifyThreadProjectUpdated(request, response.Thread.ID, &projectID)
				}
			}
			if r.services.Skills != nil && r.localEnvironmentEnabled() {
				r.services.Skills.WatchCWDs([]string{response.Thread.CWD})
			}
			r.emitThreadStartAnalytics(context.Background(), request.normalizedConnectionID(), response, request)
			r.emitThreadStartedMetric(context.Background(), response.Thread)
			r.emitThreadConfigWarnings(response.Thread.CWD)
			if shouldEmitThreadStartedNotification(response.Thread) {
				r.notify(NotificationThreadStarted, &ThreadStartedNotification{Thread: threadStartedNotificationThread(response.Thread)})
			}
			if request.Method == MethodThreadStart {
				r.startMemoriesStartupTask(response, request)
				r.scheduleStartupPrewarm(response)
				// Rust records the whole thread/start latency once the client has
				// been notified and the post-start work has been scheduled.
				r.recordStartupPhase("thread_start_total", time.Since(threadStartStartedAt), "ready")
			}
		} else if response, ok := result.(*ThreadForkResponse); ok && response.Thread != nil {
			r.startThreadSessionSpan(response.Thread.ID, threadSpawnSpan)
			var forkParams ThreadForkParams
			if request.DecodeParams(&forkParams) == nil && r.networkApproval != nil {
				r.networkApproval.syncApprovedHostsForFork(forkParams.ThreadID, response.Thread.ID)
			}
			r.markResponseThreadLoaded(response, request.normalizedConnectionID())
			// Rust #44944: the fork inherits its own retained provider route.
			r.captureThreadModelProviderRouteByID(response.Thread.ID)
			r.emitThreadForkAnalytics(context.Background(), request.normalizedConnectionID(), response, request)
			r.emitThreadStartedMetric(context.Background(), response.Thread)
			r.notifyRestoredTokenUsage(response)
			if shouldEmitThreadStartedNotification(response.Thread) {
				r.notify(NotificationThreadStarted, &ThreadStartedNotification{Thread: threadStartedNotificationThread(response.Thread)})
			}
		}
	case MethodThreadArchive:
		archivedIDs := lifecycleIDsWithFallback(request, lifecycleIDs)
		if response, ok := result.(*ThreadArchiveResponse); ok {
			archivedIDs = response.archivedThreadIDs
		}
		for _, threadID := range session.ArchiveNotificationOrder(archivedIDs) {
			r.runSessionEndHookOnce(lifecycleRecords[string(threadID)], "archive")
			r.markThreadUnloaded(string(threadID))
			r.notify(NotificationThreadArchived, &ThreadIDNotification{ThreadID: string(threadID)})
		}
	case MethodThreadUnarchive:
		if response, ok := result.(*ThreadUnarchiveResponse); ok && response.Thread != nil {
			r.notify(NotificationThreadUnarchived, &ThreadIDNotification{ThreadID: response.Thread.ID})
		}
	case MethodThreadDelete:
		for _, threadID := range session.DeleteOrderForSubtree(lifecycleIDsWithFallback(request, lifecycleIDs)) {
			r.deleteExecutedToolCallRecorder(string(threadID))
			r.runSessionEndHookOnce(lifecycleRecords[string(threadID)], "delete")
			r.markThreadUnloaded(string(threadID))
			r.notify(NotificationThreadDeleted, &ThreadIDNotification{ThreadID: string(threadID)})
		}
	case MethodThreadSetName, MethodThreadNameSet:
		if params, ok := lifecycleSetNameParams(request); ok {
			r.notify(NotificationThreadNameUpdated, &ThreadNameUpdatedNotification{ThreadID: params.ThreadID, ThreadName: &params.Name})
		}
	}
	return result, nil
}

func (r *RuntimeRouter) lifecycleRecordSnapshots(ids []session.ThreadID) map[string]*session.Record {
	out := make(map[string]*session.Record, len(ids))
	for _, id := range ids {
		record, err := r.threadRecord(id, true, true)
		if err == nil && record != nil {
			out[string(id)] = cloneRuntimeSessionRecord(record)
		}
	}
	return out
}

func (r *RuntimeRouter) runSessionEndHookOnce(record *session.Record, reason string) {
	if r == nil || record == nil || strings.TrimSpace(string(record.ID)) == "" {
		return
	}
	threadID := string(record.ID)
	r.sessionEndMu.Lock()
	if r.sessionEnded == nil {
		r.sessionEnded = map[string]struct{}{}
	}
	if _, ok := r.sessionEnded[threadID]; ok {
		r.sessionEndMu.Unlock()
		return
	}
	r.sessionEnded[threadID] = struct{}{}
	r.sessionEndMu.Unlock()
	hooks := r.hooksForCWD(record.Metadata.CWD, threadID)
	if len(hooks) == 0 {
		return
	}
	path := ""
	if r.services.ThreadRouter != nil {
		path = r.services.ThreadRouter.threadRolloutPath(record)
	}
	var transcriptPath *string
	if strings.TrimSpace(path) != "" {
		transcriptPath = &path
	}
	_, err := r.requireHookRunner().RunSessionEnd(context.Background(), &HookSessionEndRequest{
		ThreadID: threadID, CWD: record.Metadata.CWD, TranscriptPath: transcriptPath,
		Model: record.Metadata.Model, PermissionMode: record.Metadata.ApprovalPolicy,
		Reason: reason, Hooks: hooks,
	})
	if err != nil {
		slog.Warn("session end hook failed", "thread_id", threadID, "reason", reason, "error", err)
	}
	// Rust 6f647caa9b: abort outstanding background hook work during shutdown.
	r.requireHookRunner().ShutdownAsync(threadID)
}

func (r *RuntimeRouter) handleEphemeralThreadStartRuntime(request *Request) (*ThreadStartResponse, bool, error) {
	if r == nil || request == nil || request.Method != MethodThreadStart {
		return nil, false, nil
	}
	var params ThreadStartParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, true, err
	}
	if !params.Ephemeral {
		return nil, false, nil
	}
	if err := params.Validate(); err != nil {
		return nil, true, err
	}
	if err := threadStartHistoryModeError(&params); err != nil {
		return nil, true, err
	}
	if params.DaybreakEnabled != nil {
		// Rust #45513: an ephemeral thread cannot save the initial Daybreak
		// preference.
		return nil, true, jsonRPCInvalidRequest("daybreakEnabled is not supported for ephemeral threads")
	}
	threadID := newThreadID()
	if _, ok := r.ephemeralThreadRecord(threadID, false); ok {
		return nil, true, fmt.Errorf("%w: thread %s already exists", session.ErrConflict, threadID)
	}
	if r.services.ThreadRouter != nil && r.services.ThreadRouter.store != nil {
		if _, err := r.services.ThreadRouter.store.Load(threadID); err == nil {
			return nil, true, fmt.Errorf("%w: thread %s already exists", session.ErrConflict, threadID)
		} else if !errors.Is(err, session.ErrThreadNotFound) {
			return nil, true, err
		}
	}
	now := runtimeRouterNow(r).UTC()
	historyMode := string(params.HistoryMode)
	if historyMode == "" {
		historyMode = string(ThreadHistoryLegacy)
	}
	threadSource := ""
	if params.ThreadSource != nil {
		threadSource = string(*params.ThreadSource)
	}
	extra := ensureRecordExtra(threadStartExtra(&params))
	extra["ephemeral"] = true
	cwd := r.effectiveThreadStartCWD(&params)
	runtimeWorkspaceRoots := threadRuntimeWorkspaceRoots(cwd, params.RuntimeWorkspaceRoots)
	if len(runtimeWorkspaceRoots) > 0 {
		extra["runtime_workspace_roots"] = append([]string(nil), runtimeWorkspaceRoots...)
	}
	serviceTier := threadLifecycleServiceTier(params.ServiceTierSet, params.ServiceTier)
	record := &session.Record{
		ID:        threadID,
		SessionID: string(threadID),
		Preview:   params.Prompt,
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: session.Metadata{
			CWD:                     cwd,
			Model:                   params.Model,
			ModelProvider:           params.ModelProvider,
			ServiceTier:             serviceTier,
			Source:                  string(SessionSourceAppServer),
			ThreadSource:            threadSource,
			HistoryMode:             historyMode,
			SessionPrefix:           session.PrefixForSessionID(string(threadID)),
			DynamicTools:            cloneRawMessages(params.DynamicTools),
			SelectedCapabilityRoots: rawSelectedCapabilityRoots(params.SelectedCapabilityRoots),
			Extra:                   extra,
		},
	}
	if params.Prompt != "" {
		record.Items = []session.Item{{
			ID:        "item-" + safeIdentifier(request.ID.String()),
			Type:      "message",
			Role:      "user",
			Text:      params.Prompt,
			CreatedAt: now,
			Metadata:  map[string]any{"turnId": "turn-" + safeIdentifier(request.ID.String())},
		}}
	}
	r.saveEphemeralThreadRecord(record)
	thread := BuildThread(record, "", true)
	if thread != nil {
		thread.Status = IdleStatus()
	}
	return &ThreadStartResponse{
		Thread:                  thread,
		CWD:                     cwd,
		RuntimeWorkspaceRoots:   runtimeWorkspaceRoots,
		ActivePermissionProfile: activePermissionProfileFromID(params.Permissions),
		ServiceTier:             stringPtrIfNotEmpty(serviceTier),
		DisabledPluginIDs:       disabledPluginIDsFromRecord(record),
	}, true, nil
}

func (r *RuntimeRouter) markRuntimeSeedRollout(response *ThreadStartResponse, request *Request) {
	if r == nil || response == nil || response.Thread == nil || request == nil {
		return
	}
	var params ThreadStartParams
	if err := request.DecodeParams(&params); err != nil {
		return
	}
	if params.Ephemeral || strings.TrimSpace(params.Prompt) == "" {
		return
	}
	record, err := r.threadRecord(session.ThreadID(response.Thread.ID), true, true)
	if err != nil || record == nil || runtimeRecordEphemeral(record) {
		return
	}
	record.Metadata.Extra = ensureRecordExtra(record.Metadata.Extra)
	record.Metadata.Extra[runtimeSeedRolloutExtraKey] = true
	_ = r.runtimeSaveThreadRecord(record)
}

func (r *RuntimeRouter) handleEphemeralThreadForkRuntime(request *Request) (*ThreadForkResponse, bool, error) {
	if r == nil || request == nil || request.Method != MethodThreadFork || r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil {
		return nil, false, nil
	}
	var params ThreadForkParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, true, err
	}
	if !params.Ephemeral {
		return nil, false, nil
	}
	if err := params.Validate(); err != nil {
		return nil, true, err
	}
	if err := threadLifecycleSandboxPermissionsError(params.Permissions, params.Sandbox); err != nil {
		return nil, true, err
	}
	mode := params.HistoryMode
	if mode == "" {
		mode = session.ForkAll
	}
	sourceID, err := threadForkSourceID(&params)
	if err != nil {
		return nil, true, err
	}
	sourceLocks, err := r.services.ThreadRouter.acquireLifecycleWriters([]session.ThreadID{sourceID})
	if err != nil {
		return nil, true, err
	}
	defer closeTemporaryWriters(sourceLocks)
	var sourceRecord *session.Record
	if params.Path != nil && strings.TrimSpace(*params.Path) != "" {
		sourceRecord, err = r.services.ThreadRouter.readThreadRecordFromRolloutPath(*params.Path, true, true)
		if err != nil {
			return nil, true, err
		}
	} else {
		sourceRecord, err = r.threadRecord(sourceID, true, true)
		if err != nil {
			return nil, true, err
		}
	}
	if sourceRecord != nil && sourceRecord.Archived {
		return nil, true, threadResumeArchivedError(sourceRecord.ID)
	}
	if unmaterializedThread(sourceRecord) && !threadUsesPaginatedHistory(sourceRecord) {
		return nil, true, jsonRPCInvalidRequest(fmt.Sprintf("no rollout found for thread id %s", sourceRecord.ID))
	}
	if err := validatePaginatedForkParams(sourceRecord, &params); err != nil {
		return nil, true, err
	}
	r.attachRolloutTurnSnapshots(sourceRecord)
	forkOptions := session.ForkOptions{
		Mode:         mode,
		LastN:        params.LastN,
		LastTurnID:   params.LastTurnID,
		BeforeTurnID: params.BeforeTurnID,
		Ephemeral:    true,
		Now:          runtimeRouterNow(r).UTC(),
	}
	if historyBase, prepared, prepareErr := r.services.ThreadRouter.preparePaginatedForkHistoryBase(sourceRecord, &params); prepareErr != nil {
		return nil, true, prepareErr
	} else if prepared {
		forkOptions.HistoryBase, forkOptions.HistoryBaseSet = historyBase, true
	}
	record, err := r.services.ThreadRouter.store.ForkRecord(sourceRecord, forkOptions)
	if err != nil {
		return nil, true, threadForkRecordError(err)
	}
	if !params.Ephemeral {
		if err := r.services.ThreadRouter.retainLiveThread(record); err != nil {
			_ = r.services.ThreadRouter.store.Delete(record.ID)
			return nil, true, err
		}
	}
	applyThreadForkName(record, sourceRecord)
	if params.ThreadSource != nil {
		value := string(*params.ThreadSource)
		record.Metadata.ThreadSource = value
	}
	applyThreadForkOverrides(record, &params)
	// Rust #44349: forked threads report `fork`, not `startup`.
	setThreadRecordPendingSessionStartSource(record, SessionStartSourceFork)
	record.Metadata.Extra = ensureRecordExtra(record.Metadata.Extra)
	record.Metadata.Extra["ephemeral"] = true
	r.saveEphemeralThreadRecord(record)
	responseRecord := record
	if params.ExcludeTurns {
		responseRecord = cloneRuntimeSessionRecord(record)
		responseRecord.Items = nil
	}
	thread := BuildThread(responseRecord, "", !params.ExcludeTurns)
	if thread != nil {
		thread.Status = IdleStatus()
	}
	return &ThreadForkResponse{
		Thread:                  thread,
		ApprovalPolicy:          params.ApprovalPolicy,
		ApprovalsReviewer:       cloneString(params.ApprovalsReviewer),
		CWD:                     record.Metadata.CWD,
		Model:                   record.Metadata.Model,
		ModelProvider:           record.Metadata.ModelProvider,
		Sandbox:                 params.Sandbox,
		ServiceTier:             stringPtrIfNotEmpty(record.Metadata.ServiceTier),
		RuntimeWorkspaceRoots:   threadRecordRuntimeWorkspaceRoots(record, record.Metadata.CWD, nil),
		ActivePermissionProfile: activePermissionProfileFromID(params.Permissions),
		DisabledPluginIDs:       disabledPluginIDsFromRecord(record),
	}, true, nil
}

func (r *RuntimeRouter) rejectEphemeralThreadDelete(request *Request) error {
	var params ThreadDeleteParams
	if request == nil || request.DecodeParams(&params) != nil || strings.TrimSpace(params.ThreadID) == "" {
		return nil
	}
	if _, ok := r.ephemeralThreadRecord(session.ThreadID(params.ThreadID), false); ok {
		return jsonRPCInvalidRequest(fmt.Sprintf("thread is not persisted and cannot be deleted: %s", params.ThreadID))
	}
	return nil
}

func runtimeRouterNow(r *RuntimeRouter) time.Time {
	if r != nil && r.services.ThreadRouter != nil && r.services.ThreadRouter.now != nil {
		if now := r.services.ThreadRouter.now().UTC(); !now.IsZero() {
			return now
		}
	}
	return time.Now().UTC()
}

func (r *RuntimeRouter) applyThreadStartInstructionSources(response *ThreadStartResponse, request *Request) error {
	if r == nil || response == nil || response.Thread == nil || request == nil {
		return nil
	}
	var params ThreadStartParams
	if err := request.DecodeParams(&params); err != nil {
		return nil
	}
	var cfg *config.Config
	if loaded, cfgErr := r.effectiveConfigForThreadStart(&params); cfgErr == nil {
		cfg = loaded
	}
	parts, err := r.threadStartInstructionParts(&params)
	if err != nil {
		return err
	}
	instructions := joinInstructionsParts(parts.global, parts.project)
	sources := parts.sources
	response.InstructionSources = sources
	configuredBaseInstructions := ""
	if cfg != nil {
		configuredBaseInstructions, _ = appBaseInstructionsForConfig(cfg)
	}
	baseInstructions := firstNonEmpty(stringPtrValue(params.BaseInstructions), instructions, configuredBaseInstructions)
	record, err := r.threadRecord(session.ThreadID(response.Thread.ID), true, true)
	if err != nil || record == nil {
		return nil
	}
	if strings.TrimSpace(baseInstructions) != "" {
		record.Metadata.BaseInstructions = baseInstructions
		record.Metadata.BaseInstructionsProvenance = &session.BaseInstructionsProvenance{Type: session.BaseInstructionsProvenanceCustom}
		// Rust #44675: remember the AGENTS.md split so a running session can
		// refresh the global part without re-discovering the repository.
		if stringPtrValue(params.BaseInstructions) == "" && strings.TrimSpace(instructions) != "" {
			record.Metadata.Extra = ensureRecordExtra(record.Metadata.Extra)
			record.Metadata.Extra["instructions_global"] = strings.TrimSpace(parts.global)
			record.Metadata.Extra["instructions_project"] = strings.TrimSpace(parts.project)
			record.Metadata.Extra["instructions_project_cwd"] = strings.TrimSpace(parts.projectCWD)
			record.Metadata.Extra["instructions_project_trust"] = strings.TrimSpace(parts.projectTrust)
			record.Metadata.Extra["instructions_from_agents_md"] = true
		}
	} else if params.BaseInstructions != nil {
		// An explicit empty value suppresses model instructions and is still a
		// caller-owned choice that must survive a resumed turn.
		record.Metadata.BaseInstructionsProvenance = &session.BaseInstructionsProvenance{Type: session.BaseInstructionsProvenanceCustom}
	} else if len(sources) == 0 {
		modelID := firstNonEmpty(strings.TrimSpace(params.Model), strings.TrimSpace(record.Metadata.Model))
		personality := stringPtrValue(params.Personality)
		if cfg != nil {
			personality = firstNonEmpty(personality, stringConfigValue(cfg, "personality"))
		}
		if info := r.modelInfoForRuntimeWithPersonality(modelID, cfg, personality); info != nil {
			if generated := strings.TrimSpace(info.ModelInstructions(personality)); generated != "" {
				record.Metadata.BaseInstructions = generated
				record.Metadata.BaseInstructionsProvenance = &session.BaseInstructionsProvenance{Type: session.BaseInstructionsProvenanceModel, Model: info.Slug}
			}
		}
	}
	r.emitInstructionWarnings(response.Thread.ID, parts.warnings)
	setThreadRecordInstructionSources(record, sources)
	_ = r.runtimeSaveThreadRecord(record)
	return nil
}

func (r *RuntimeRouter) applyThreadStartOriginator(response *ThreadStartResponse, request *Request) {
	if r == nil || response == nil || response.Thread == nil || request == nil {
		return
	}
	var params ThreadStartParams
	if err := request.DecodeParams(&params); err != nil {
		return
	}
	originator := stringPtrValue(params.ServiceName)
	var clientInfo ClientInfo
	if strings.TrimSpace(originator) == "" {
		if info, ok := r.connectionClientInfo(request.normalizedConnectionID()); ok {
			clientInfo = info
			originator = strings.TrimSpace(info.Name)
		}
	}
	if strings.TrimSpace(originator) == "" {
		return
	}
	record, err := r.threadRecord(session.ThreadID(response.Thread.ID), true, true)
	if err != nil || record == nil {
		return
	}
	record.Metadata.Originator = strings.TrimSpace(originator)
	// Rust attributes threads started by the VS Code client to `vscode` and
	// reports the running CLI version on the thread. The extension uses these
	// fields when grouping and diagnosing sessions.
	if strings.EqualFold(strings.TrimSpace(clientInfo.Name), "codex_vscode") || strings.EqualFold(strings.TrimSpace(clientInfo.Name), "vscode") {
		record.Metadata.Source = string(SessionSourceVsCode)
		response.Thread.Source = SessionSourceVsCode
	}
	record.Metadata.CLIVersion = appServerVersion()
	response.Thread.CLIVersion = record.Metadata.CLIVersion
	_ = r.runtimeSaveThreadRecord(record)
}

func (r *RuntimeRouter) applyThreadStartConfigSnapshot(response *ThreadStartResponse, request *Request) {
	if r == nil || response == nil || response.Thread == nil || request == nil {
		return
	}
	var params ThreadStartParams
	if err := request.DecodeParams(&params); err != nil {
		return
	}
	cfg, err := r.effectiveConfigForThreadStart(&params)
	if err != nil || cfg == nil {
		return
	}
	modelID := firstNonEmpty(strings.TrimSpace(params.Model), stringConfigValue(cfg, "model"))
	providerID := firstNonEmpty(strings.TrimSpace(params.ModelProvider), stringConfigValue(cfg, "model_provider"), stringConfigValue(cfg, "modelProvider"), model.OpenAIProviderID)
	modelID = threadStartProviderFallbackModel(modelID, providerID, params.AllowProviderModelFallback)
	reasoningEffort := firstNonEmpty(stringConfigValue(cfg, "model_reasoning_effort"), stringConfigValue(cfg, "modelReasoningEffort"))
	serviceTier := r.threadStartServiceTier(cfg, &params, modelID)
	response.ApprovalPolicy = params.ApprovalPolicy
	response.ApprovalsReviewer = cloneString(params.ApprovalsReviewer)
	response.Sandbox = threadStartResponseSandbox(params.Sandbox)
	cwd := r.effectiveThreadStartCWD(&params)
	runtimeWorkspaceRoots := threadRuntimeWorkspaceRoots(cwd, params.RuntimeWorkspaceRoots)
	if modelID != "" {
		response.Model = modelID
		response.Thread.Model = stringPtrIfNotEmpty(modelID)
	}
	if providerID != "" {
		response.ModelProvider = providerID
		response.Thread.ModelProvider = providerID
	}
	if reasoningEffort != "" {
		response.ReasoningEffort = stringPtrIfNotEmpty(reasoningEffort)
		response.Thread.ReasoningEffort = reasoningEffortPtr(reasoningEffort)
	}
	response.ServiceTier = stringPtrIfNotEmpty(serviceTier)
	response.RuntimeWorkspaceRoots = append([]string(nil), runtimeWorkspaceRoots...)
	if cwd != "" {
		response.CWD = cwd
		response.Thread.CWD = cwd
	}
	record, err := r.threadRecord(session.ThreadID(response.Thread.ID), true, true)
	if err != nil || record == nil {
		return
	}
	if modelID != "" {
		record.Metadata.Model = modelID
	}
	if providerID != "" {
		record.Metadata.ModelProvider = providerID
	}
	record.Metadata.ServiceTier = serviceTier
	record.Metadata.Extra = ensureRecordExtra(record.Metadata.Extra)
	sessionDefaults := threadRecordConfigOverrides(record)
	if sessionDefaults == nil {
		sessionDefaults = map[string]any{}
	}
	for key, value := range map[string]string{
		"model":                      modelID,
		"model_reasoning_effort":     reasoningEffort,
		"plan_mode_reasoning_effort": stringConfigValue(cfg, "plan_mode_reasoning_effort"),
		"service_tier":               serviceTier,
		"personality":                firstNonEmpty(stringPtrValue(params.Personality), stringConfigValue(cfg, "personality")),
	} {
		if strings.TrimSpace(value) != "" {
			sessionDefaults[key] = strings.TrimSpace(value)
		}
	}
	if params.ApprovalPolicy != nil {
		sessionDefaults["approval_policy"] = params.ApprovalPolicy
	}
	if params.ApprovalsReviewer != nil {
		sessionDefaults["approvals_reviewer"] = *params.ApprovalsReviewer
	}
	if params.Sandbox != nil {
		sessionDefaults["sandbox_policy"] = params.Sandbox
	}
	if params.Permissions != nil {
		sessionDefaults["permissions"] = *params.Permissions
	}
	if len(sessionDefaults) > 0 {
		record.Metadata.Extra["config"] = sessionDefaults
	}
	if tokenBudget, tokenBudgetErr := cfg.TokenBudgetConfig(); tokenBudgetErr == nil && tokenBudget != nil {
		record.Metadata.Extra["token_budget_enabled"] = tokenBudget.Enabled
		if tokenBudget.AutoCompactFallbackPrompt != "" {
			record.Metadata.Extra["auto_compact_fallback_prompt"] = tokenBudget.AutoCompactFallbackPrompt
		} else {
			delete(record.Metadata.Extra, "auto_compact_fallback_prompt")
		}
		if tokenBudget.AutoCompactFallbackBufferTokens != nil {
			record.Metadata.Extra["auto_compact_fallback_buffer_tokens"] = *tokenBudget.AutoCompactFallbackBufferTokens
		} else {
			delete(record.Metadata.Extra, "auto_compact_fallback_buffer_tokens")
		}
		if tokenBudget.ReminderThresholdTokens != nil {
			record.Metadata.Extra["token_budget_reminder_threshold_tokens"] = *tokenBudget.ReminderThresholdTokens
		} else {
			delete(record.Metadata.Extra, "token_budget_reminder_threshold_tokens")
		}
		if strings.TrimSpace(tokenBudget.ReminderMessageTemplate) != "" {
			record.Metadata.Extra["token_budget_reminder_message_template"] = strings.TrimSpace(tokenBudget.ReminderMessageTemplate)
		} else {
			delete(record.Metadata.Extra, "token_budget_reminder_message_template")
		}
	}
	if len(runtimeWorkspaceRoots) > 0 {
		record.Metadata.Extra["runtime_workspace_roots"] = append([]string(nil), runtimeWorkspaceRoots...)
	} else {
		delete(record.Metadata.Extra, "runtime_workspace_roots")
	}
	if cwd != "" {
		record.Metadata.CWD = cwd
	}
	if err := r.runtimeSaveThreadRecord(record); err != nil {
		return
	}
}

func threadStartResponseSandbox(value any) any {
	if text, ok := value.(string); ok {
		return threadSettingsSandboxPolicy(text)
	}
	return value
}

func (r *RuntimeRouter) effectiveThreadStartCWD(params *ThreadStartParams) string {
	if r == nil {
		return effectiveThreadStartCWD(params, processCWD())
	}
	return effectiveThreadStartCWD(params, r.threadStartDefaultCWD())
}

func (r *RuntimeRouter) threadStartDefaultCWD() string {
	if r != nil {
		if cwd := strings.TrimSpace(r.services.DefaultCWD); cwd != "" {
			return cwd
		}
		if r.services.ThreadRouter != nil {
			if cwd := routerDefaultCWD(r.services.ThreadRouter); cwd != "" {
				return cwd
			}
		}
	}
	return processCWD()
}

// windowsSandboxPrivateDesktopForTurn mirrors Rust #46554: legacy Windows
// sandboxes always launch on a private desktop, and the removed
// `windows.sandbox_private_desktop` setting cannot opt out.
func windowsSandboxPrivateDesktopForTurn() bool {
	return true
}

func (r *RuntimeRouter) effectiveConfigForThreadStart(params *ThreadStartParams) (*config.Config, error) {
	cfg := &config.Config{Values: map[string]any{}}
	if r == nil || r.services.Config == nil {
		applyRuntimeConfigOverrides(cfg, threadStartConfigOverrides(params))
		if err := config.ValidateMCPServerValues(cfg.Values); err != nil {
			return nil, err
		}
		return cfg, nil
	}
	codexHome := strings.TrimSpace(r.services.Config.CodexHome())
	if codexHome == "" {
		applyRuntimeConfigOverrides(cfg, threadStartConfigOverrides(params))
		if err := config.ValidateMCPServerValues(cfg.Values); err != nil {
			return nil, err
		}
		return cfg, nil
	}
	cwd := r.effectiveThreadStartCWD(params)
	loaded, err := config.LoadWithOptions(codexHome, &config.LoadOptions{CWD: cwd})
	if err != nil {
		return nil, err
	}
	if loaded != nil {
		cfg = loaded
	}
	applyRuntimeConfigOverrides(cfg, threadStartConfigOverrides(params))
	// Rust converts the request's `config` overrides through the typed config
	// layer, so a transport-mismatched `mcp_servers` entry fails thread/start.
	if err := config.ValidateMCPServerValues(cfg.Values); err != nil {
		return nil, err
	}
	// Rust #46328: a directory where discovery found no project-root marker,
	// Git checkout or project-local config directory is projectless, so
	// thread/start must not persist implicit trust there.
	if threadStartEffectivePermissionsTrustProject(cfg, cwd, params) && !cfg.IsProjectless() {
		r.trustThreadStartProject(cwd)
	}
	return cfg, nil
}

func (r *RuntimeRouter) effectiveMCPConfigForThreadStartRequest(request *Request) (*config.Config, error) {
	if request == nil {
		return nil, fmt.Errorf("%w: request is nil", ErrInvalidRequest)
	}
	var params ThreadStartParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	cfg, err := r.effectiveConfigForThreadStart(&params)
	if err != nil {
		return nil, err
	}
	applyThreadLifecycleMCPConfig(cfg, params.Model, params.ModelProvider, params.ApprovalPolicy, params.ApprovalsReviewer, params.Sandbox, params.Permissions, params.ServiceTierSet, params.ServiceTier, params.Personality)
	return cfg, nil
}

func (r *RuntimeRouter) effectiveMCPConfigForThreadResume(response *ThreadResumeResponse, request *Request) (*config.Config, error) {
	if r == nil || response == nil || response.Thread == nil || request == nil {
		return nil, fmt.Errorf("%w: thread resume response is incomplete", ErrInvalidRequest)
	}
	var params ThreadResumeParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	record, err := r.threadRecord(session.ThreadID(response.Thread.ID), true, false)
	if err != nil {
		return nil, err
	}
	resumeApprovalPolicy := params.ApprovalPolicy
	if resumeApprovalPolicy == nil {
		resumeApprovalPolicy = response.ApprovalPolicy
	}
	turnParams := &turn.TurnStartParams{
		ThreadID:              response.Thread.ID,
		CWD:                   firstNonEmpty(stringPtrValue(params.CWD), response.CWD, record.Metadata.CWD),
		Model:                 firstNonEmpty(stringPtrValue(params.Model), response.Model, record.Metadata.Model),
		ApprovalPolicy:        resumeApprovalPolicy,
		ApprovalsReviewer:     cloneString(params.ApprovalsReviewer),
		SandboxPolicy:         params.Sandbox,
		Permissions:           cloneString(params.Permissions),
		RuntimeWorkspaceRoots: append([]string(nil), response.RuntimeWorkspaceRoots...),
		ServiceTier:           cloneString(params.ServiceTier),
		ServiceTierSet:        params.ServiceTierSet,
		Personality:           cloneString(params.Personality),
		PersonalitySet:        params.Personality != nil,
		Config:                mergeTurnConfigOverrides(threadRecordConfigOverrides(record), params.Config),
	}
	providerID := firstNonEmpty(stringPtrValue(params.ModelProvider), response.ModelProvider, record.Metadata.ModelProvider)
	if providerID != "" {
		turnParams.Config["model_provider"] = providerID
	}
	cfg, err := r.effectiveConfigForTurn(turnParams)
	if err != nil {
		return nil, err
	}
	applyThreadLifecycleMCPConfig(cfg, turnParams.Model, providerID, resumeApprovalPolicy, params.ApprovalsReviewer, params.Sandbox, params.Permissions, params.ServiceTierSet, params.ServiceTier, params.Personality)
	return cfg, nil
}

func applyThreadLifecycleMCPConfig(
	cfg *config.Config,
	modelID string,
	providerID string,
	approvalPolicy any,
	approvalsReviewer *string,
	sandboxPolicy any,
	permissions *string,
	serviceTierSet bool,
	serviceTier *string,
	personality *string,
) {
	if cfg == nil {
		return
	}
	overrides := map[string]any{}
	if modelID = strings.TrimSpace(modelID); modelID != "" {
		overrides["model"] = modelID
	}
	if providerID = strings.TrimSpace(providerID); providerID != "" {
		overrides["model_provider"] = providerID
	}
	if approvalPolicy != nil {
		overrides["approval_policy"] = approvalPolicy
	}
	if approvalsReviewer != nil {
		overrides["approvals_reviewer"] = *approvalsReviewer
	}
	if sandboxPolicy != nil {
		overrides["sandbox_policy"] = sandboxPolicy
	}
	if permissions != nil {
		overrides["permissions"] = *permissions
	}
	if serviceTierSet || serviceTier != nil {
		overrides["service_tier"] = stringPtrValue(serviceTier)
	}
	if personality != nil {
		overrides["personality"] = *personality
	}
	applyRuntimeConfigOverrides(cfg, overrides)
}

func (r *RuntimeRouter) validateRequiredMCPServers(threadID string, cfg *config.Config) error {
	threadID = strings.TrimSpace(threadID)
	if r == nil || threadID == "" {
		return fmt.Errorf("%w: thread id is required", ErrInvalidRequest)
	}
	service := r.mcpServiceForThread(threadID, cfg)
	if service == nil {
		return fmt.Errorf("%w: MCP runtime is not available", ErrInvalidRequest)
	}
	return service.ValidateRequiredServers(threadID)
}

func (r *RuntimeRouter) persistThreadResumeConfigSnapshot(threadID string, request *Request) error {
	threadID = strings.TrimSpace(threadID)
	if r == nil || threadID == "" || request == nil {
		return nil
	}
	var params ThreadResumeParams
	if err := request.DecodeParams(&params); err != nil {
		return err
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil {
		return err
	}
	overrides := mergeTurnConfigOverrides(threadRecordConfigOverrides(record), params.Config)
	if modelID := stringPtrValue(params.Model); modelID != "" {
		overrides["model"] = modelID
	}
	if providerID := stringPtrValue(params.ModelProvider); providerID != "" {
		overrides["model_provider"] = providerID
	}
	if params.ApprovalPolicy != nil {
		overrides["approval_policy"] = params.ApprovalPolicy
	}
	if params.ApprovalsReviewer != nil {
		overrides["approvals_reviewer"] = *params.ApprovalsReviewer
	}
	if params.Sandbox != nil {
		overrides["sandbox_policy"] = params.Sandbox
	}
	if params.Permissions != nil {
		overrides["permissions"] = *params.Permissions
	}
	if params.ServiceTierSet || params.ServiceTier != nil {
		overrides["service_tier"] = stringPtrValue(params.ServiceTier)
	}
	if params.Personality != nil {
		overrides["personality"] = *params.Personality
	}
	if len(overrides) == 0 {
		return nil
	}
	record.Metadata.Extra = ensureRecordExtra(record.Metadata.Extra)
	record.Metadata.Extra["config"] = overrides
	return r.runtimeSaveThreadRecord(record)
}

func threadStartConfigOverrides(params *ThreadStartParams) map[string]any {
	if params == nil {
		return nil
	}
	return params.Config
}

func (r *RuntimeRouter) trustThreadStartProject(cwd string) {
	if r == nil || r.services.Config == nil {
		return
	}
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return
	}
	codexHome := strings.TrimSpace(r.services.Config.CodexHome())
	if codexHome == "" {
		return
	}
	trustTarget := threadStartTrustTarget(cwd)
	if trustTarget == "" || projectTrustLevel(codexHome, trustTarget) != "" {
		return
	}
	_ = appendProjectTrustLevel(codexHome, trustTarget)
}

// threadStartEffectivePermissionsTrustProject mirrors Rust #38390: project
// trust is derived from the effective permission profile after managed
// constraints, not from the requested sandbox mode.
func threadStartEffectivePermissionsTrustProject(cfg *config.Config, cwd string, params *ThreadStartParams) bool {
	if cfg == nil || params == nil || strings.TrimSpace(params.CWD) == "" || strings.TrimSpace(cwd) == "" {
		return false
	}
	var profile *sandbox.PermissionProfile
	if turnStartSandboxPolicyPresent(params.Sandbox) {
		_, resolved, err := turnSandboxPolicyPermissionProfile(params.Sandbox)
		if err != nil || resolved == nil {
			return false
		}
		profile = resolved
	} else {
		profileID := ""
		if params.Permissions != nil {
			profileID = strings.TrimSpace(*params.Permissions)
		}
		resolution, err := cfg.ResolveSandboxPermissionProfile(profileID, cwd)
		if err != nil || resolution == nil {
			return false
		}
		profile = resolution.Profile
	}
	if profile == nil || profile.Disabled {
		return true
	}
	if profile.SandboxPolicy == nil || profile.SandboxPolicy.HasFullDiskWriteAccess() {
		return true
	}
	return len(profile.SandboxPolicy.GetWritableRootsWithCWD(cwd)) > 0
}

// rejectDisallowedPermissionProfileOverride mirrors the Rust app-server checks
// (turn_processor build_thread_settings_overrides and
// command_exec_processor): an explicit permission-profile override that managed
// requirements disallow is rejected with the requirement warning, while the
// startup config path is allowed to fall back to the required default.
func rejectDisallowedPermissionProfileOverride(cfg *config.Config, prefix string, explicit *string) error {
	if cfg == nil || explicit == nil {
		return nil
	}
	selected := strings.TrimSpace(*explicit)
	if selected == "" {
		return nil
	}
	if warning := cfg.PermissionProfileRequirementWarningFor(selected); warning != "" {
		return jsonRPCInvalidRequest(prefix + ": " + warning)
	}
	return nil
}

func threadStartTrustTarget(cwd string) string {
	cwd = cleanRuntimeWorkspaceRoot(cwd)
	if cwd == "" {
		return ""
	}
	for dir := cwd; dir != ""; dir = parentAppPath(dir) {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
	}
	return cwd
}

func parentAppPath(path string) string {
	parent := filepath.Dir(path)
	if parent == path || parent == "." {
		return ""
	}
	return parent
}

func projectTrustLevel(codexHome string, trustTarget string) string {
	cfg, err := config.Load(codexHome)
	if err != nil || cfg == nil {
		return ""
	}
	projects, _ := cfg.Values["projects"].(map[string]any)
	for key, value := range projects {
		if !sameAppPath(key, trustTarget) {
			continue
		}
		if project, ok := value.(map[string]any); ok {
			return strings.TrimSpace(stringFromMap(project, "trust_level"))
		}
	}
	return ""
}

func appendProjectTrustLevel(codexHome string, trustTarget string) error {
	path := config.ConfigPath(codexHome)
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	body := strings.TrimRight(string(data), "\r\n")
	if body != "" {
		body += "\n\n"
	}
	body += fmt.Sprintf("[projects.%q]\ntrust_level = \"trusted\"\n", filepath.Clean(trustTarget))
	return os.WriteFile(path, []byte(body), 0o600)
}

func threadStartProviderFallbackModel(modelID string, providerID string, allowFallback bool) string {
	modelID = strings.TrimSpace(modelID)
	if !allowFallback || strings.TrimSpace(providerID) != model.AmazonBedrockProviderID {
		return modelID
	}
	manager := model.NewStaticModelsManager(model.AmazonBedrockModelCatalog())
	return manager.GetDefaultModel(modelID, true, model.RefreshOffline)
}

func (r *RuntimeRouter) threadStartServiceTier(cfg *config.Config, params *ThreadStartParams, modelID string) string {
	settings := map[string]bool{}
	if cfg != nil {
		settings = cfg.FeatureSettings()
	}
	if !features.Enabled(settings, "fast_mode") {
		return ""
	}
	if params != nil && (params.ServiceTierSet || params.ServiceTier != nil) {
		return threadLifecycleServiceTierForModel(r.requireModels(), params.ServiceTierSet, params.ServiceTier, modelID)
	}
	requested := firstNonEmpty(
		stringConfigValue(cfg, "service_tier"),
		stringConfigValue(cfg, "serviceTier"),
	)
	return threadLifecycleServiceTierForRequest(r.requireModels(), requested, modelID)
}

func (r *RuntimeRouter) threadStartInstructions(params *ThreadStartParams) (string, []string, error) {
	parts, err := r.threadStartInstructionParts(params)
	if err != nil {
		return "", nil, err
	}
	return joinInstructionsParts(parts.global, parts.project), parts.sources, nil
}

// threadStartInstructions holds the separately resolved global and repository
// instruction sources so a turn can refresh the global part without re-reading
// the repository snapshot (Rust #44675).
type threadStartInstructions struct {
	global   string
	project  string
	sources  []string
	warnings []string
	// projectCWD and projectTrust record the environment facts the repository
	// snapshot was resolved with, so a turn can re-discover it when either
	// changes (Rust #44675).
	projectCWD   string
	projectTrust string
}

func (r *RuntimeRouter) threadStartInstructionParts(params *ThreadStartParams) (*threadStartInstructions, error) {
	if r == nil || params == nil {
		return &threadStartInstructions{}, nil
	}
	parts := &threadStartInstructions{}
	if codexHome := r.codexHomeForInstructions(); codexHome != "" {
		loaded := r.refreshGlobalInstructions(codexHome)
		if text := instructionsText(loaded); text != "" {
			parts.global = text
			if loaded != nil && loaded.Instructions != nil {
				parts.sources = appendInstructionSource(parts.sources, loaded.Instructions.Source)
			}
		}
		if loaded != nil {
			parts.warnings = append(parts.warnings, loaded.Warnings...)
		}
	}
	if params.Environments == nil {
		cwd := r.effectiveThreadStartCWD(params)
		cfg, _ := r.effectiveConfigForThreadStart(params)
		projectText, projectSources, err := r.loadProjectInstructionsFor(cwd, cfg)
		if err != nil {
			return nil, err
		}
		parts.project = projectText
		parts.sources = append(parts.sources, projectSources...)
		parts.projectCWD = cwd
		parts.projectTrust = projectTrustLabel(cfg, cwd)
	}
	return parts, nil
}

// loadProjectInstructionsFor resolves repository-scoped instructions for a cwd
// using the supplied configuration. Rust #39837: project discovery is skipped
// for untrusted projects while user-level instructions are preserved.
func (r *RuntimeRouter) loadProjectInstructionsFor(cwd string, cfg *config.Config) (string, []string, error) {
	maxBytes := config.DefaultProjectDocMaxBytes
	var rootMarkers, fallbackNames []string
	if cfg != nil {
		maxBytes = cfg.ProjectDocMaxBytes()
		// Rust core/src/agents_md.rs: project-root markers and the configured
		// fallback filenames participate in project-doc discovery.
		rootMarkers = cfg.ProjectRootMarkers()
		fallbackNames = cfg.ProjectDocFallbackFilenames()
	}
	var denyRead func(string) bool
	if cfg != nil {
		if resolution, resolveErr := turnSandboxPermissionProfile(cfg, cwd, nil); resolveErr == nil && resolution != nil && resolution.Profile != nil {
			profile := resolution.Profile
			denyRead = func(path string) bool { return profile.DeniesReadPath(path) }
		}
	}
	loaded, err := promptctx.LoadProjectInstructions(promptctx.InstructionsLoadConfig{
		CWD:              cwd,
		RootMarkers:      rootMarkers,
		FallbackNames:    fallbackNames,
		MaxBytes:         maxBytes,
		DenyRead:         denyRead,
		UntrustedProject: strings.EqualFold(projectTrustLabel(cfg, cwd), "untrusted"),
	})
	if err != nil {
		return "", nil, err
	}
	if loaded == nil {
		return "", nil, nil
	}
	var sources []string
	for _, entry := range loaded.Entries {
		if entry.Provenance != promptctx.InstructionsProvenanceProject || strings.TrimSpace(entry.Contents) == "" {
			continue
		}
		sources = appendInstructionSource(sources, entry.SourcePath)
	}
	return strings.TrimSpace(loaded.Text()), sources, nil
}

// projectTrustLabel returns the active project's trust level for a cwd, or an
// empty label when no trust decision applies.
func projectTrustLabel(cfg *config.Config, cwd string) string {
	if cfg == nil {
		return ""
	}
	root := config.ActiveProjectRoot(cwd)
	if root == "" {
		return ""
	}
	if level, ok := config.ProjectTrustLevelForTarget(cfg.Values, root); ok {
		return strings.ToLower(strings.TrimSpace(level))
	}
	return ""
}

func (r *RuntimeRouter) codexHomeForInstructions() string {
	if r == nil {
		return ""
	}
	if r.services.Config != nil {
		if codexHome := strings.TrimSpace(r.services.Config.CodexHome()); codexHome != "" {
			return codexHome
		}
	}
	if r.services.ThreadRouter != nil && r.services.ThreadRouter.store != nil {
		return codexHomeFromSessionStore(r.services.ThreadRouter.store)
	}
	return ""
}

func appendInstructionSource(sources []string, path string) []string {
	path = canonicalInstructionSource(path)
	if path == "" {
		return sources
	}
	for _, existing := range sources {
		if sameAppPath(existing, path) {
			return sources
		}
	}
	return append(sources, path)
}

func canonicalInstructionSource(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	if canonical, err := filepath.EvalSymlinks(path); err == nil {
		path = canonical
	}
	return filepath.Clean(path)
}

func sameAppPath(a string, b string) bool {
	a = filepath.Clean(strings.TrimSpace(a))
	b = filepath.Clean(strings.TrimSpace(b))
	if a == "" || b == "" {
		return a == b
	}
	if strings.EqualFold(a, b) {
		return true
	}
	return a == b
}

func lifecycleIDsWithFallback(request *Request, ids []session.ThreadID) []session.ThreadID {
	if len(ids) > 0 {
		return ids
	}
	threadID := strings.TrimSpace(lifecycleThreadID(request))
	if threadID == "" {
		return nil
	}
	return []session.ThreadID{session.ThreadID(threadID)}
}

func (r *RuntimeRouter) markResponseThreadLoaded(result any, connectionID string) {
	if r == nil {
		return
	}
	var thread *Thread
	switch response := result.(type) {
	case *ThreadStartResponse:
		thread = response.Thread
	case *ThreadResumeResponse:
		thread = response.Thread
	case *ThreadForkResponse:
		thread = response.Thread
	default:
		return
	}
	if thread == nil || strings.TrimSpace(thread.ID) == "" {
		return
	}
	r.requireThreadStatus().UpsertThread(thread.ID, false)
	r.subscribeThreadConnection(thread.ID, connectionID)
	thread.Status = r.requireThreadStatus().LoadedStatusForThread(thread.ID)
	r.prewarmMCPThread(thread.ID)
}

func (r *RuntimeRouter) threadIsLoaded(threadID string) bool {
	threadID = strings.TrimSpace(threadID)
	return r != nil && threadID != "" && r.requireThreadStatus().LoadedStatusForThread(threadID).Type != NotLoadedStatus().Type
}

func (r *RuntimeRouter) rollbackThreadStartInitialization(threadID string) {
	threadID = strings.TrimSpace(threadID)
	if r == nil || threadID == "" {
		return
	}
	threadSessionID := session.ThreadID(threadID)
	if !r.threads.DeleteEphemeralRecord(threadSessionID) && r.services.ThreadRouter != nil && r.services.ThreadRouter.store != nil {
		r.services.ThreadRouter.releaseLiveThreads([]session.ThreadID{threadSessionID})
		if err := r.services.ThreadRouter.store.Delete(threadSessionID); err != nil && !errors.Is(err, session.ErrThreadNotFound) {
			slog.Warn("failed to roll back thread after MCP initialization failure", "thread_id", threadID, "error", err)
		}
	}
	r.closeThreadMCPRuntime(threadID)
}

func (r *RuntimeRouter) rollbackThreadResumeInitialization(threadID string, request *Request) {
	threadID = strings.TrimSpace(threadID)
	if r == nil || threadID == "" {
		return
	}
	threadSessionID := session.ThreadID(threadID)
	if r.services.ThreadRouter != nil {
		r.services.ThreadRouter.releaseLiveThreads([]session.ThreadID{threadSessionID})
		var params ThreadResumeParams
		if request != nil && request.DecodeParams(&params) == nil && params.HistorySet {
			if r.services.ThreadRouter.store != nil {
				if err := r.services.ThreadRouter.store.Delete(threadSessionID); err != nil && !errors.Is(err, session.ErrThreadNotFound) {
					slog.Warn("failed to roll back history-resume record after MCP initialization failure", "thread_id", threadID, "error", err)
				}
			}
			r.services.ThreadRouter.deleteThreadRollouts(threadSessionID)
		}
	}
	r.closeThreadMCPRuntime(threadID)
}

func (r *RuntimeRouter) closeThreadMCPRuntime(threadID string) {
	if r == nil || r.mcpRuntimes == nil {
		return
	}
	if err := r.mcpRuntimes.closeThread(threadID); err != nil {
		slog.Warn("failed to close thread MCP runtime after initialization failure", "thread_id", threadID, "error", err)
	}
}

func (r *RuntimeRouter) markThreadUnloaded(threadID string) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	// Rust ends a session's `session_loop` span when the submission loop
	// terminates; Go's thread unload is the same lifecycle point.
	r.endThreadSessionSpan(threadID)
	r.requireThreadStatus().RemoveThread(threadID)
	r.clearThreadSubscriptions(threadID)
	r.skillWarningsMu.Lock()
	delete(r.skillWarnings, threadID)
	r.skillWarningsMu.Unlock()
	r.forgetMCPToolApprovals(threadID)
	if err := r.deleteCodeModeRuntime(threadID); err != nil {
		slog.Warn("failed to close thread code-mode runtime", "thread_id", threadID, "error", err)
	}
	if r.mcpRuntimes != nil {
		if err := r.mcpRuntimes.closeThread(threadID); err != nil {
			slog.Warn("failed to close thread MCP runtime", "thread_id", threadID, "error", err)
		}
	}
	if err := r.closeThreadManagedNetwork(threadID); err != nil {
		slog.Warn("failed to close thread managed network", "thread_id", threadID, "error", err)
	}
	if r.networkApproval != nil {
		r.networkApproval.clearThread(threadID)
	}
	r.clearActiveDisabledPluginIDs(threadID)
}

func (r *RuntimeRouter) clearActiveDisabledPluginIDs(threadID string) {
	if r == nil || r.services.ThreadExtras == nil {
		return
	}
	r.services.ThreadExtras.ClearActiveDisabledPluginIDs(threadID)
}

func (r *RuntimeRouter) subscribeThreadConnection(threadID string, connectionID string) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	r.threads.Subscribe(threadID, connectionID)
}

func (r *RuntimeRouter) firstAttestationCapableConnectionForThread(threadID string) (string, bool) {
	if r == nil {
		return "", false
	}
	connectionIDs := r.subscribedConnectionIDsForThread(threadID)
	if len(connectionIDs) == 0 {
		return "", false
	}
	r.clientInfoMu.RLock()
	defer r.clientInfoMu.RUnlock()
	for _, connectionID := range connectionIDs {
		if r.requestAttestation[connectionID] {
			return connectionID, true
		}
	}
	return "", false
}

func (r *RuntimeRouter) subscribedConnectionIDsForThread(threadID string) []string {
	if r == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	return r.threads.Subscribers(threadID)
}

func (r *RuntimeRouter) unsubscribeThreadConnection(threadID string, connectionID string) bool {
	if r == nil {
		return false
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return false
	}
	return r.threads.Unsubscribe(threadID, connectionID)
}

func (r *RuntimeRouter) clearConnectionSubscriptions(connectionID string) {
	if r == nil {
		return
	}
	r.threads.ClearConnection(connectionID)
}

func (r *RuntimeRouter) clearThreadSubscriptions(threadID string) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	r.threads.ClearThread(threadID)
}

func (r *RuntimeRouter) notifyRestoredTokenUsage(result any) {
	if r == nil || result == nil {
		return
	}
	var thread *Thread
	switch typed := result.(type) {
	case *ThreadResumeResponse:
		thread = typed.Thread
	case *ThreadForkResponse:
		thread = typed.Thread
	default:
		return
	}
	if thread == nil || len(thread.Turns) == 0 {
		return
	}
	record, err := r.threadRecord(session.ThreadID(thread.ID), true, true)
	if err != nil || record == nil {
		return
	}
	usage := restoredTokenUsageFromRecord(record)
	if usage == nil {
		return
	}
	turnID := strings.TrimSpace(stringFromMap(record.Metadata.Extra, "token_usage_turn_id"))
	if turnID == "" || !threadHasTurnID(thread, turnID) {
		turnID = latestRestoredTokenUsageTurnID(thread)
	}
	if turnID == "" {
		return
	}
	r.notify(NotificationThreadTokenUsageUpdated, &ThreadTokenUsageUpdatedNotification{
		ThreadID:   thread.ID,
		TurnID:     turnID,
		TokenUsage: *usage,
	})
}

func (r *RuntimeRouter) replayPendingServerRequestsForThread(result any) {
	if r == nil {
		return
	}
	response, ok := result.(*ThreadResumeResponse)
	if !ok || response == nil || response.Thread == nil {
		return
	}
	threadID := strings.TrimSpace(response.Thread.ID)
	if threadID == "" {
		return
	}
	r.requireServerRequests().ReplayThread(threadID)
}

func restoredTokenUsageFromRecord(record *session.Record) *TokenUsage {
	if record == nil {
		return nil
	}
	extra := record.Metadata.Extra
	var info map[string]any
	if rawInfo, ok := extra["token_usage_info"].(map[string]any); ok {
		info = rawInfo
	}
	var totalRaw any
	var lastRaw any
	var windowRaw any
	if info != nil {
		totalRaw = firstMapValue(info, "total_token_usage", "totalTokenUsage", "total")
		lastRaw = firstMapValue(info, "last_token_usage", "lastTokenUsage", "last")
		windowRaw = firstMapValue(info, "model_context_window", "modelContextWindow")
	}
	if totalRaw == nil {
		totalRaw = firstMapValue(extra, "total_token_usage", "totalTokenUsage", "total")
	}
	if lastRaw == nil {
		lastRaw = firstMapValue(extra, "last_token_usage", "lastTokenUsage", "last")
	}
	if windowRaw == nil {
		windowRaw = firstMapValue(extra, "model_context_window", "modelContextWindow")
	}
	total := tokenUsageBreakdownFromMetadata(totalRaw)
	last := tokenUsageBreakdownFromMetadata(lastRaw)
	if total == nil && last == nil && len(record.Items) > 0 {
		estimated := int64(compact.EstimateTokens(compactItemsFromSessionItems(record.Items)))
		if estimated > 0 {
			last = &TokenUsageBreakdown{TotalTokens: estimated}
			total = &TokenUsageBreakdown{TotalTokens: estimated}
		}
	}
	if total == nil && last == nil {
		return nil
	}
	if total == nil {
		total = cloneTokenUsageBreakdownPtr(last)
	}
	usage := &TokenUsage{Total: total}
	if last != nil {
		usage.Last = last
		usage.InputTokens = last.InputTokens
		usage.CachedInputTokens = last.CachedInputTokens
		usage.CacheWriteInputTokens = last.CacheWriteInputTokens
		usage.OutputTokens = last.OutputTokens
		usage.ReasoningOutputTokens = last.ReasoningOutputTokens
		usage.TotalTokens = last.TotalTokens
	}
	if window := int64FromAnyValue(windowRaw); window > 0 {
		usage.ModelContextWindow = &window
	} else if window := effectiveRestoredModelContextWindow(record.Metadata.Model); window > 0 {
		usage.ModelContextWindow = &window
	}
	return usage
}

func effectiveRestoredModelContextWindow(modelID string) int64 {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return 0
	}
	info := model.NewStaticModelsManager(model.BundledModelsResponse()).GetModelInfo(modelID, nil)
	window := info.ContextWindow
	if window <= 0 {
		window = info.MaxContextWindow
	}
	if window <= 0 {
		return 0
	}
	percent := info.EffectiveContextWindowPercent
	if percent <= 0 {
		percent = 95
	}
	return window * int64(percent) / 100
}

// runtimeMultiAgentVersionForThread resolves the multi-agent version for a
// turn in the same precedence order as Rust's
// Config::multi_agent_version_for_model:
//  1. the MultiAgentV2 feature flag (override),
//  2. the thread model's declared multi-agent version,
//  3. the stable `multi_agent` (Collab) feature fallback (V1).
//
// `agents.enabled=false` disables the whole surface regardless of model or
// features, and an empty result means the collaboration tools are withheld.
func (r *RuntimeRouter) runtimeMultiAgentVersionForThread(threadID string, cfg *config.Config, agentsConfig *config.AgentsConfig) agent.MultiAgentVersion {
	if cfg == nil {
		return ""
	}
	settings := cfg.FeatureSettings()
	// The MultiAgentV2 feature is a hard override in Rust's
	// `multi_agent_version_override`: it wins even when agents.enabled=false.
	if features.Enabled(settings, "multi_agent_v2") {
		return agent.VersionV2
	}
	if agentsConfig != nil && agentsConfig.Enabled != nil && !*agentsConfig.Enabled {
		return ""
	}
	modelID := ""
	if r != nil {
		if record, recordErr := r.threadRecord(session.ThreadID(strings.TrimSpace(threadID)), true, false); recordErr == nil && record != nil {
			modelID = strings.TrimSpace(record.Metadata.Model)
		}
	}
	if version, declared := runtimeModelMultiAgentVersion(r, modelID); declared {
		return version
	}
	if features.Enabled(settings, "multi_agent") {
		return agent.VersionV1
	}
	return ""
}

// runtimeMultiAgentVersionForTurn resolves the multi-agent version for a turn
// like Rust's per-session resolved version: a version persisted on the thread
// record (set when the thread was spawned/resumed) wins over re-resolving from
// features/model each turn. This keeps V2 sub-agent threads on V2 even when the
// model catalog lookup is unavailable, and matches Rust's
// `resolve_multi_agent_version` (history/inherited version takes precedence).
func (r *RuntimeRouter) runtimeMultiAgentVersionForTurn(threadID string, cfg *config.Config, agentsConfig *config.AgentsConfig) agent.MultiAgentVersion {
	if r != nil {
		if record, recordErr := r.threadRecord(session.ThreadID(strings.TrimSpace(threadID)), true, false); recordErr == nil && record != nil {
			if version := knownRuntimeMultiAgentVersion(strings.TrimSpace(record.Metadata.MultiAgentVersion)); version != "" {
				return version
			}
		}
	}
	return r.runtimeMultiAgentVersionForThread(threadID, cfg, agentsConfig)
}

func knownRuntimeMultiAgentVersion(version string) agent.MultiAgentVersion {
	switch strings.ToLower(strings.TrimSpace(version)) {
	case string(agent.VersionV1):
		return agent.VersionV1
	case string(agent.VersionV2):
		return agent.VersionV2
	default:
		return ""
	}
}

// runtimeV2SubagentToolsEnabled mirrors Rust's collab_tools_enabled V2 branch
// for sub-agent threads: the spawn surface stays visible only when the
// sub-agent's current model declares multi_agent_version v2.
func runtimeV2SubagentToolsEnabled(r *RuntimeRouter, record *session.Record, cfg *config.Config) bool {
	if record == nil {
		return true
	}
	modelID := strings.TrimSpace(record.Metadata.Model)
	if modelID == "" && cfg != nil {
		modelID = stringConfigValue(cfg, "model")
	}
	modelVersion, declared := runtimeModelMultiAgentVersion(r, modelID)
	return declared && modelVersion == agent.VersionV2
}

// runtimeModelMultiAgentVersion maps a model's declared multi-agent version to
// the version used for tool exposure, mirroring Rust's model catalog handling:
// unknown values fall back to the feature-derived version. The bool reports
// whether the model declared a version at all; a declared "disabled" stops the
// resolution instead of falling back to V1.
func runtimeModelMultiAgentVersion(r *RuntimeRouter, modelID string) (agent.MultiAgentVersion, bool) {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return "", false
	}
	version := ""
	if r != nil && r.services.Models != nil {
		info := r.services.Models.Info(&model.ModelInfoReadParams{Model: modelID})
		if info != nil {
			version = strings.TrimSpace(info.MultiAgentVersion)
		}
	} else {
		info := model.NewStaticModelsManager(model.BundledModelsResponse()).GetModelInfo(modelID, nil)
		version = strings.TrimSpace(info.MultiAgentVersion)
	}
	switch strings.ToLower(version) {
	case string(agent.VersionV2):
		return agent.VersionV2, true
	case string(agent.VersionV1):
		return agent.VersionV1, true
	case "disabled":
		return "", true
	default:
		return "", false
	}
}

// RestoredTokenUsageForRecord exposes the Rust-compatible restored token
// snapshot to front-ends that resume through thread/read rather than
// thread/resume.
func RestoredTokenUsageForRecord(record *session.Record) *TokenUsage {
	return restoredTokenUsageFromRecord(record)
}

func tokenUsageBreakdownFromMetadata(value any) *TokenUsageBreakdown {
	values, ok := value.(map[string]any)
	if !ok || len(values) == 0 {
		return nil
	}
	breakdown := &TokenUsageBreakdown{
		InputTokens:           int64FromAnyValue(firstMapValue(values, "input_tokens", "inputTokens")),
		CachedInputTokens:     int64FromAnyValue(firstMapValue(values, "cached_input_tokens", "cachedInputTokens")),
		CacheWriteInputTokens: int64FromAnyValue(firstMapValue(values, "cache_write_input_tokens", "cacheWriteInputTokens")),
		OutputTokens:          int64FromAnyValue(firstMapValue(values, "output_tokens", "outputTokens")),
		ReasoningOutputTokens: int64FromAnyValue(firstMapValue(values, "reasoning_output_tokens", "reasoningOutputTokens")),
		TotalTokens:           int64FromAnyValue(firstMapValue(values, "total_tokens", "totalTokens")),
	}
	if breakdown.TotalTokens == 0 {
		breakdown.TotalTokens = breakdown.InputTokens + breakdown.OutputTokens
	}
	if breakdown.InputTokens == 0 && breakdown.CachedInputTokens == 0 && breakdown.CacheWriteInputTokens == 0 && breakdown.OutputTokens == 0 && breakdown.ReasoningOutputTokens == 0 && breakdown.TotalTokens == 0 {
		return nil
	}
	return breakdown
}

func firstMapValue(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			return value
		}
	}
	return nil
}

func int64FromAnyValue(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case int32:
		return int64(typed)
	case float64:
		return int64(typed)
	case float32:
		return int64(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	default:
		return 0
	}
}

func threadHasTurnID(thread *Thread, turnID string) bool {
	if thread == nil || strings.TrimSpace(turnID) == "" {
		return false
	}
	for i := range thread.Turns {
		if thread.Turns[i].ID == turnID {
			return true
		}
	}
	return false
}

func latestRestoredTokenUsageTurnID(thread *Thread) string {
	if thread == nil {
		return ""
	}
	for i := len(thread.Turns) - 1; i >= 0; i-- {
		if thread.Turns[i].Status == TurnStatusCompleted || thread.Turns[i].Status == TurnStatusFailed {
			return thread.Turns[i].ID
		}
	}
	if len(thread.Turns) == 0 {
		return ""
	}
	return thread.Turns[len(thread.Turns)-1].ID
}

type activeRuntimeTurnForkSnapshot struct {
	ThreadID    string
	TurnID      string
	StartedAtMS int64
	Params      *turn.TurnStartParams
}

func (r *RuntimeRouter) handleActiveThreadForkRuntime(request *Request) (any, bool, error) {
	if r == nil || request == nil || request.Method != MethodThreadFork || r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil {
		return nil, false, nil
	}
	var params ThreadForkParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, true, err
	}
	if err := params.Validate(); err != nil {
		return nil, true, err
	}
	if err := threadLifecycleSandboxPermissionsError(params.Permissions, params.Sandbox); err != nil {
		return nil, true, err
	}
	if strings.TrimSpace(params.LastTurnID) != "" || strings.TrimSpace(params.BeforeTurnID) != "" {
		return nil, false, nil
	}
	sourceID, err := threadForkSourceID(&params)
	if err != nil {
		return nil, true, err
	}
	active, ok := r.activeRuntimeTurnForkSnapshot(string(sourceID))
	if !ok {
		return nil, false, nil
	}
	sourceLocks, err := r.services.ThreadRouter.acquireLifecycleWriters([]session.ThreadID{sourceID})
	if err != nil {
		return nil, true, err
	}
	defer closeTemporaryWriters(sourceLocks)
	mode := params.HistoryMode
	if mode == "" {
		mode = session.ForkAll
	}
	now := r.services.ThreadRouter.now().UTC()
	sourceRecord, err := r.threadRecord(sourceID, true, true)
	if err != nil {
		return nil, true, err
	}
	if sourceRecord != nil && sourceRecord.Archived {
		return nil, true, threadResumeArchivedError(sourceRecord.ID)
	}
	if err := validatePaginatedForkParams(sourceRecord, &params); err != nil {
		return nil, true, err
	}
	forkOptions := session.ForkOptions{
		Mode:         mode,
		LastN:        params.LastN,
		LastTurnID:   params.LastTurnID,
		BeforeTurnID: params.BeforeTurnID,
		Ephemeral:    params.Ephemeral,
		Now:          now,
	}
	if historyBase, prepared, prepareErr := r.services.ThreadRouter.preparePaginatedForkHistoryBase(sourceRecord, &params); prepareErr != nil {
		return nil, true, prepareErr
	} else if prepared {
		forkOptions.HistoryBase, forkOptions.HistoryBaseSet = historyBase, true
	}
	record, err := r.services.ThreadRouter.store.ForkRecord(sourceRecord, forkOptions)
	if err != nil {
		return nil, true, threadForkRecordError(err)
	}
	if !params.Ephemeral {
		if err := r.services.ThreadRouter.retainLiveThread(record); err != nil {
			_ = r.services.ThreadRouter.store.Delete(record.ID)
			return nil, true, err
		}
	}
	annotateActiveForkSourceSnapshot(record, active, now)
	applyThreadForkName(record, sourceRecord)
	record.Metadata.RolloutTurns = activeForkTurnSnapshots(record, active, now)
	if params.ThreadSource != nil {
		value := string(*params.ThreadSource)
		record.Metadata.ThreadSource = value
	}
	applyThreadForkOverrides(record, &params)
	// Rust #44349: forked threads report `fork`, not `startup`.
	setThreadRecordPendingSessionStartSource(record, SessionStartSourceFork)
	if !params.Ephemeral {
		if err := r.runtimeSaveThreadRecord(record); err != nil {
			r.rollbackRuntimeForkInitialization(record)
			return nil, true, err
		}
	}
	if !params.Ephemeral {
		if err := r.createActiveForkThreadRollout(record, active, now); err != nil {
			r.rollbackRuntimeForkInitialization(record)
			return nil, true, err
		}
	} else {
		r.saveEphemeralThreadRecord(record)
	}
	responseRecord := record
	if params.ExcludeTurns {
		responseRecord = cloneRuntimeSessionRecord(record)
		responseRecord.Items = nil
		responseRecord.Metadata.RolloutTurns = nil
	}
	path := ""
	if !params.Ephemeral {
		path = r.services.ThreadRouter.threadRolloutPath(record)
	}
	thread := BuildThread(responseRecord, path, !params.ExcludeTurns)
	if thread != nil {
		thread.Status = IdleStatus()
	}
	return &ThreadForkResponse{
		Thread:                  thread,
		ApprovalPolicy:          params.ApprovalPolicy,
		ApprovalsReviewer:       cloneString(params.ApprovalsReviewer),
		CWD:                     record.Metadata.CWD,
		Model:                   record.Metadata.Model,
		ModelProvider:           record.Metadata.ModelProvider,
		Sandbox:                 params.Sandbox,
		ServiceTier:             stringPtrIfNotEmpty(record.Metadata.ServiceTier),
		RuntimeWorkspaceRoots:   threadRecordRuntimeWorkspaceRoots(record, record.Metadata.CWD, nil),
		ActivePermissionProfile: activePermissionProfileFromID(params.Permissions),
		DisabledPluginIDs:       disabledPluginIDsFromRecord(record),
	}, true, nil
}

func (r *RuntimeRouter) activeRuntimeTurnForkSnapshot(threadID string) (activeRuntimeTurnForkSnapshot, bool) {
	if r == nil {
		return activeRuntimeTurnForkSnapshot{}, false
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return activeRuntimeTurnForkSnapshot{}, false
	}
	active := r.threads.ActiveTurn(threadID)
	if active == nil || strings.TrimSpace(active.TurnID) == "" {
		return activeRuntimeTurnForkSnapshot{}, false
	}
	return activeRuntimeTurnForkSnapshot{
		ThreadID:    threadID,
		TurnID:      strings.TrimSpace(active.TurnID),
		StartedAtMS: active.StartedAtMS,
		Params:      cloneTurnStartParams(active.Params),
	}, true
}

func (r *RuntimeRouter) rollbackRuntimeForkInitialization(record *session.Record) {
	if r == nil || record == nil || r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil {
		return
	}
	r.services.ThreadRouter.rollbackThreadForkInitialization(record)
}

func annotateActiveForkSourceSnapshot(record *session.Record, active activeRuntimeTurnForkSnapshot, now time.Time) {
	if record == nil || strings.TrimSpace(active.TurnID) == "" {
		return
	}
	startedAt := activeForkStartedAt(active, now)
	if promptItem, ok := runtimeUserPromptSessionItem(active.TurnID, active.Params, startedAt); ok && !sessionRecordHasItemID(record, promptItem.ID) {
		record.Items = append(record.Items, promptItem)
	}
	marker := interruptedTurnMarkerSessionItem(active.TurnID, now)
	if !sessionRecordHasItemID(record, marker.ID) && !sessionRecordHasTurnAbortedMarker(record, active.TurnID) {
		record.Items = append(record.Items, marker)
	}
}

func sessionRecordHasItemID(record *session.Record, itemID string) bool {
	if record == nil || strings.TrimSpace(itemID) == "" {
		return false
	}
	for i := range record.Items {
		if record.Items[i].ID == itemID {
			return true
		}
	}
	return false
}

func sessionRecordHasTurnAbortedMarker(record *session.Record, turnID string) bool {
	if record == nil || strings.TrimSpace(turnID) == "" {
		return false
	}
	for i := range record.Items {
		item := &record.Items[i]
		if firstNonEmpty(stringFromMap(item.Metadata, "kind"), stringFromMap(item.Data, "kind")) != "turn_aborted" {
			continue
		}
		if firstNonEmpty(stringFromMap(item.Metadata, "turnId"), stringFromMap(item.Metadata, "turn_id"), stringFromMap(item.Data, "turnId"), stringFromMap(item.Data, "turn_id")) == turnID {
			return true
		}
	}
	return false
}

func (r *RuntimeRouter) createActiveForkThreadRollout(record *session.Record, active activeRuntimeTurnForkSnapshot, now time.Time) error {
	if r == nil || r.services.ThreadRouter == nil || record == nil {
		return nil
	}
	recorder, err := r.services.ThreadRouter.newThreadRolloutRecorder(record, record.CreatedAt)
	if err != nil || recorder == nil {
		return err
	}
	defer recorder.Close()
	return appendActiveForkRolloutItems(recorder, record, active, now)
}

type runtimeForkTurnItemGroup struct {
	TurnID      string
	Items       []session.Item
	StartedAt   time.Time
	CompletedAt time.Time
}

func appendActiveForkRolloutItems(recorder *rollout.Recorder, record *session.Record, active activeRuntimeTurnForkSnapshot, now time.Time) error {
	if recorder == nil || record == nil {
		return nil
	}
	for _, group := range runtimeForkTurnItemGroups(record.Items, record.CreatedAt) {
		startedAt := group.StartedAt
		completedAt := group.CompletedAt
		durationMS := runtimeForkDurationMS(startedAt, completedAt)
		if group.TurnID == active.TurnID {
			startedAt = activeForkStartedAt(active, group.StartedAt)
			completedAt = now.UTC()
			durationMS = runtimeForkDurationMS(startedAt, completedAt)
		}
		if err := recorder.AppendTurnStarted(group.TurnID, startedAt); err != nil {
			return err
		}
		if err := rollout.AppendSessionItems(recorder, group.Items, now); err != nil {
			return err
		}
		if group.TurnID == active.TurnID {
			if err := recorder.AppendTurnAborted(group.TurnID, "interrupted", completedAt, durationMS); err != nil {
				return err
			}
			continue
		}
		if err := recorder.AppendTurnComplete(group.TurnID, completedAt, durationMS); err != nil {
			return err
		}
	}
	return nil
}

func activeForkTurnSnapshots(record *session.Record, active activeRuntimeTurnForkSnapshot, now time.Time) []session.TurnSnapshot {
	if record == nil {
		return nil
	}
	groups := runtimeForkTurnItemGroups(record.Items, record.CreatedAt)
	if len(groups) == 0 {
		return nil
	}
	snapshots := make([]session.TurnSnapshot, 0, len(groups))
	for _, group := range groups {
		status := string(TurnStatusCompleted)
		startedAt := group.StartedAt
		completedAt := group.CompletedAt
		if group.TurnID == active.TurnID {
			status = string(TurnStatusInterrupted)
			startedAt = activeForkStartedAt(active, group.StartedAt)
			completedAt = now.UTC()
		}
		durationMS := runtimeForkDurationMS(startedAt, completedAt)
		snapshots = append(snapshots, session.TurnSnapshot{
			ID:          group.TurnID,
			Status:      status,
			StartedAt:   runtimeInt64Ptr(startedAt.UTC().Unix()),
			CompletedAt: runtimeInt64Ptr(completedAt.UTC().Unix()),
			DurationMS:  runtimeInt64Ptr(durationMS),
		})
	}
	return snapshots
}

func runtimeForkTurnItemGroups(items []session.Item, fallback time.Time) []runtimeForkTurnItemGroup {
	if len(items) == 0 {
		return nil
	}
	groups := []runtimeForkTurnItemGroup{}
	for i := range items {
		item := items[i]
		turnID := runtimeSessionItemTurnID(&item, i)
		createdAt := runtimeItemCreatedAt(item, fallback)
		if len(groups) == 0 || groups[len(groups)-1].TurnID != turnID {
			groups = append(groups, runtimeForkTurnItemGroup{
				TurnID:      turnID,
				Items:       []session.Item{},
				StartedAt:   createdAt,
				CompletedAt: createdAt,
			})
		}
		group := &groups[len(groups)-1]
		group.Items = append(group.Items, item)
		if createdAt.Before(group.StartedAt) {
			group.StartedAt = createdAt
		}
		if createdAt.After(group.CompletedAt) {
			group.CompletedAt = createdAt
		}
	}
	return groups
}

func runtimeSessionItemTurnID(item *session.Item, index int) string {
	if item != nil {
		if turnID := firstNonEmpty(stringFromMap(item.Metadata, "turnId"), stringFromMap(item.Metadata, "turn_id"), stringFromMap(item.Data, "turnId"), stringFromMap(item.Data, "turn_id")); turnID != "" {
			return turnID
		}
	}
	return fmt.Sprintf("turn-%d", index+1)
}

func runtimeItemCreatedAt(item session.Item, fallback time.Time) time.Time {
	if !item.CreatedAt.IsZero() {
		return item.CreatedAt.UTC()
	}
	if !fallback.IsZero() {
		return fallback.UTC()
	}
	return time.Now().UTC()
}

func activeForkStartedAt(active activeRuntimeTurnForkSnapshot, fallback time.Time) time.Time {
	if active.StartedAtMS > 0 {
		return time.UnixMilli(active.StartedAtMS).UTC()
	}
	if !fallback.IsZero() {
		return fallback.UTC()
	}
	return time.Now().UTC()
}

func runtimeForkDurationMS(startedAt time.Time, completedAt time.Time) int64 {
	if startedAt.IsZero() || completedAt.IsZero() {
		return 0
	}
	durationMS := completedAt.UTC().Sub(startedAt.UTC()).Milliseconds()
	if durationMS < 0 {
		return 0
	}
	return durationMS
}

func runtimeInt64Ptr(value int64) *int64 {
	return &value
}

func (r *RuntimeRouter) lifecycleSubtreeIDs(request *Request) []session.ThreadID {
	if r == nil || r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil || request == nil {
		return nil
	}
	threadID := lifecycleThreadID(request)
	if threadID == "" {
		return nil
	}
	var (
		ids []session.ThreadID
		err error
	)
	switch request.Method {
	case MethodThreadArchive:
		ids, err = r.services.ThreadRouter.archiveSubtreeThreadIDs(session.ThreadID(threadID))
	case MethodThreadDelete:
		ids, err = r.services.ThreadRouter.deleteSubtreeThreadIDs(session.ThreadID(threadID))
	default:
		return nil
	}
	if err != nil {
		return nil
	}
	return ids
}

// shouldEmitThreadStartedNotification reports whether a thread/started
// notification should be emitted. Ephemeral internal helper threads (for
// example memory consolidation) are hidden from TUI routing so they do not
// enter the agents overview or refresh it (Rust #40494 HiddenSystemThread).
func shouldEmitThreadStartedNotification(thread *Thread) bool {
	if thread == nil {
		return true
	}
	if !thread.Ephemeral {
		return true
	}
	if thread.ThreadSource != nil && *thread.ThreadSource == ThreadSourceMemoryConsolidation {
		return false
	}
	return true
}

func threadStartedNotificationThread(thread *Thread) *Thread {
	if thread == nil {
		return nil
	}
	clone := *thread
	clone.Turns = nil
	return &clone
}

func lifecycleThreadID(request *Request) string {
	if request == nil {
		return ""
	}
	var payload struct {
		ThreadID string `json:"threadId"`
	}
	if err := request.DecodeParams(&payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.ThreadID)
}

func lifecycleSetNameParams(request *Request) (ThreadSetNameParams, bool) {
	var params ThreadSetNameParams
	if request == nil || request.DecodeParams(&params) != nil || strings.TrimSpace(params.ThreadID) == "" {
		return ThreadSetNameParams{}, false
	}
	return params, true
}

func (r *RuntimeRouter) handleInitialize(request *Request) (*InitializeResponse, error) {
	var params InitializeParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	connectionID := request.normalizedConnectionID()
	if !r.rememberConnectionClientInfo(connectionID, params.ClientInfo) {
		return nil, jsonRPCInvalidRequest("Already initialized")
	}
	r.rememberConnectionNotificationOptOut(connectionID, params.Capabilities)
	r.rememberConnectionExperimentalAPI(connectionID, params.Capabilities)
	r.rememberConnectionRequestAttestation(connectionID, params.Capabilities)
	r.rememberConnectionMCPOpenAIForm(connectionID, params.Capabilities)
	r.rememberConnectionMCPStandardFormInput(connectionID, params.Capabilities)
	r.syncMCPOpenAIFormElicitationCapability()
	r.syncMCPStandardFormInputCapability()
	codexHome := ""
	if r.services.Config != nil {
		codexHome = r.services.Config.CodexHome()
	}
	if codexHome == "" {
		codexHome = r.services.DefaultCWD
	}
	userAgent := InitializeUserAgent(params.ClientInfo)
	response := NewInitializeResponse(codexHome, userAgent)
	warnings := r.configWarningsForInitialize()
	for i := range warnings {
		warning := warnings[i]
		r.notify(NotificationConfigWarning, &warning)
	}
	return response, nil
}

func (r *RuntimeRouter) configWarningsForInitialize() []config.ConfigWarningNotification {
	if r == nil || r.services.Config == nil {
		return nil
	}
	warnings := r.services.Config.Warnings()
	// Rust #44691: surface unrecognized settings from the startup layers
	// (packaged defaults, user, and managed configuration).
	if ignored := r.services.Config.IgnoredSettingsWarning(""); strings.TrimSpace(ignored) != "" {
		warnings = append(warnings, config.ConfigWarningNotification{Summary: ignored})
	}
	r.rememberEmittedConfigWarnings(warnings)
	return warnings
}

func (r *RuntimeRouter) rememberEmittedConfigWarnings(warnings []config.ConfigWarningNotification) {
	if r == nil || len(warnings) == 0 {
		return
	}
	r.configWarningsMu.Lock()
	defer r.configWarningsMu.Unlock()
	if r.emittedConfigWarnings == nil {
		r.emittedConfigWarnings = map[string]bool{}
	}
	for _, warning := range warnings {
		if summary := strings.TrimSpace(warning.Summary); summary != "" {
			r.emittedConfigWarnings[summary] = true
		}
	}
}

// emitThreadConfigWarnings emits ignored-configuration warnings for the thread's
// project layers that were not already delivered at startup (Rust #44691).
func (r *RuntimeRouter) emitThreadConfigWarnings(cwd string) {
	if r == nil || r.services.Config == nil {
		return
	}
	var summaries []string
	// Rust thread_processor emits the thread config's startup_warnings, which
	// include the loader diagnostics (unrecognized settings, unsupported
	// project-local keys, malformed agent roles) and the requirement-driven
	// warnings for the thread's layers.
	summaries = append(summaries, r.services.Config.ConfigWarningsForCWD(cwd)...)
	if len(summaries) == 0 {
		return
	}
	r.configWarningsMu.Lock()
	if r.emittedConfigWarnings == nil {
		r.emittedConfigWarnings = map[string]bool{}
	}
	pending := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		if r.emittedConfigWarnings[summary] {
			continue
		}
		r.emittedConfigWarnings[summary] = true
		pending = append(pending, summary)
	}
	r.configWarningsMu.Unlock()
	for _, summary := range pending {
		r.notify(NotificationConfigWarning, &config.ConfigWarningNotification{Summary: summary})
	}
}

func (r *RuntimeRouter) handleTurnStart(request *Request) (*turn.TurnStartResponse, error) {
	params, err := decodeTurnStartParams(request)
	if err != nil {
		return nil, err
	}
	if err := r.ensureDirectInputAllowed(request, params.ThreadID); err != nil {
		return nil, err
	}
	// Rust #44944: reject input to a thread whose retained provider route no
	// longer matches managed model provider requirements.
	if err := r.checkThreadModelProviderForID(params.ThreadID); err != nil {
		return nil, err
	}
	if err := validateTurnUserInputImageURLs(params.Input); err != nil {
		return nil, err
	}
	r.inheritTurnEnvironmentSelections(params)
	if err := r.validateTurnStartEnvironments(params); err != nil {
		return nil, err
	}
	if params.Permissions != nil && turnStartSandboxPolicyPresent(params.SandboxPolicy) {
		return nil, jsonRPCInvalidRequest("`permissions` cannot be combined with `sandboxPolicy`")
	}
	// Rust turn_processor build_thread_settings_overrides: an explicit settings
	// update whose permission profile managed requirements disallow is rejected
	// before the turn starts, instead of accepting the requirement-constrained
	// fallback the startup config path allows.
	if params.Permissions != nil {
		cfg, cfgErr := r.effectiveConfigForTurn(params)
		if cfgErr != nil {
			return nil, cfgErr
		}
		if err := rejectDisallowedPermissionProfileOverride(cfg, "invalid thread settings override", params.Permissions); err != nil {
			return nil, err
		}
	}
	settingsUpdate, hasSettingsUpdate := turnStartSettingsUpdateParams(params)
	if err := r.prepareTurnStartParams(params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if err := r.runPendingSessionStartHook(context.Background(), params); err != nil {
		return nil, err
	}
	reservedRuntime := false
	if r.hasRuntimeThreadStore() {
		if err := r.reserveRuntimeThread(params.ThreadID); err != nil {
			return nil, err
		}
		reservedRuntime = true
	}
	response, err := r.requireTurns().Start(params)
	if err != nil {
		if reservedRuntime {
			r.clearActiveRuntimeTurn(params.ThreadID, "")
		}
		return nil, err
	}
	_ = r.persistTurnStartRuntimeWorkspaceRoots(params)
	_ = r.persistTurnEnvironmentSelections(params)
	if hasSettingsUpdate {
		r.applyTurnStartSettingsUpdate(settingsUpdate)
	}
	r.startTurnRuntimeAsync(params, response, request.normalizedConnectionID(), r.requestSpanTrace(request))
	return response, nil
}

// decodeTurnStartParams decodes turn/start params, preferring the in-process
// params object for internal requests so internal-only fields
// (ParentTurnID/RootTurnID/AdditionalInputItems/TurnTrigger/Environments) are
// not lost at the JSON boundary.
func decodeTurnStartParams(request *Request) (*turn.TurnStartParams, error) {
	if request != nil {
		switch params := request.InternalParams.(type) {
		case *turn.TurnStartParams:
			if params != nil {
				clone := *params
				return &clone, nil
			}
		case turn.TurnStartParams:
			clone := params
			return &clone, nil
		}
	}
	var params turn.TurnStartParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	return &params, nil
}

const runtimeEnvironmentSelectionsExtraKey = "runtime_environments"

func (r *RuntimeRouter) inheritTurnEnvironmentSelections(params *turn.TurnStartParams) {
	if r == nil || params == nil || params.Environments != nil || strings.TrimSpace(params.ThreadID) == "" {
		return
	}
	record, err := r.threadRecord(session.ThreadID(params.ThreadID), true, false)
	if err != nil || record == nil {
		return
	}
	params.Environments = environmentSelectionsFromAny(record.Metadata.Extra[runtimeEnvironmentSelectionsExtraKey])
}

func (r *RuntimeRouter) persistTurnEnvironmentSelections(params *turn.TurnStartParams) error {
	if params == nil || params.Environments == nil {
		return nil
	}
	return r.persistThreadEnvironmentSelections(params.ThreadID, params.Environments)
}

func (r *RuntimeRouter) persistThreadEnvironmentSelections(threadID string, environments []map[string]any) error {
	threadID = strings.TrimSpace(threadID)
	if r == nil || threadID == "" || r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil {
		return nil
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, false)
	if err != nil || record == nil {
		return err
	}
	extra := cloneAnyMap(record.Metadata.Extra)
	if extra == nil {
		extra = map[string]any{}
	}
	if len(environments) == 0 {
		delete(extra, runtimeEnvironmentSelectionsExtraKey)
	} else {
		extra[runtimeEnvironmentSelectionsExtraKey] = cloneMapSlice(environments)
	}
	if runtimeRecordEphemeral(record) {
		record.Metadata.Extra = extra
		r.saveEphemeralThreadRecord(record)
		return nil
	}
	_, err = r.runtimeUpdateThreadMetadata(session.ThreadID(threadID), &session.MetadataPatch{Extra: extra}, true)
	return err
}

func environmentSelectionsFromAny(value any) []map[string]any {
	switch values := value.(type) {
	case []map[string]any:
		return cloneMapSlice(values)
	case []any:
		out := make([]map[string]any, 0, len(values))
		for _, value := range values {
			if selected, ok := value.(map[string]any); ok {
				out = append(out, cloneAnyMap(selected))
			}
		}
		return out
	default:
		return nil
	}
}

func (r *RuntimeRouter) persistTurnStartRuntimeWorkspaceRoots(params *turn.TurnStartParams) error {
	if params == nil {
		return nil
	}
	return r.persistThreadRecordRuntimeWorkspaceRoots(params.ThreadID, threadRuntimeWorkspaceRoots(params.CWD, params.RuntimeWorkspaceRoots))
}

func (r *RuntimeRouter) applyThreadResumeRuntimeWorkspaceRoots(result any, request *Request) {
	response, ok := result.(*ThreadResumeResponse)
	if !ok || response == nil || response.Thread == nil || request == nil {
		return
	}
	var params ThreadResumeParams
	if err := request.DecodeParams(&params); err != nil {
		return
	}
	if params.CWD == nil && len(params.RuntimeWorkspaceRoots) == 0 {
		return
	}
	roots := append([]string(nil), response.RuntimeWorkspaceRoots...)
	if len(roots) == 0 {
		roots = threadRuntimeWorkspaceRoots(response.CWD, params.RuntimeWorkspaceRoots)
	}
	_ = r.persistThreadRecordRuntimeWorkspaceRoots(response.Thread.ID, roots)
}

func (r *RuntimeRouter) applyThreadResumeSettingsUpdate(result any, request *Request) {
	response, ok := result.(*ThreadResumeResponse)
	if !ok || response == nil || response.Thread == nil || request == nil {
		return
	}
	var params ThreadResumeParams
	if err := request.DecodeParams(&params); err != nil {
		return
	}
	if threadID := strings.TrimSpace(params.ThreadID); threadID != "" && r.activeRuntimeTurnSnapshot(threadID) != nil {
		return
	}
	settingsUpdate, hasSettingsUpdate := threadResumeSettingsUpdateParams(&params, response.Thread.ID)
	if !hasSettingsUpdate {
		return
	}
	r.applyTurnStartSettingsUpdate(settingsUpdate)
}

func (r *RuntimeRouter) applyRunningThreadResumeSnapshot(result any, request *Request) error {
	response, ok := result.(*ThreadResumeResponse)
	if !ok || response == nil || response.Thread == nil || request == nil {
		return nil
	}
	var params ThreadResumeParams
	if err := request.DecodeParams(&params); err != nil {
		return nil
	}
	threadID := strings.TrimSpace(params.ThreadID)
	if threadID == "" {
		return nil
	}
	// Rust #46510: only a resume that includes turns or requests an initial page
	// with items needs the active turn's items; everything else observes turn
	// metadata alone, so the items are never cloned.
	activeTurnMetadata := r.activeRuntimeTurnSnapshotMetadata(threadID)
	if activeTurnMetadata == nil {
		return nil
	}
	needsTurnItems := !params.ExcludeTurns ||
		(params.InitialTurnsPage != nil && params.InitialTurnsPage.ItemsView != TurnItemsNotLoaded)
	activeTurn := activeTurnMetadata
	if needsTurnItems {
		if full := r.activeRuntimeTurnSnapshot(threadID); full != nil {
			activeTurn = full
		}
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, params.InitialTurnsPage != nil)
	if err != nil || record == nil {
		return err
	}
	loadedStatus := r.requireThreadStatus().LoadedStatusForThread(threadID)
	paginatedResume := false
	if r.services.ThreadRouter != nil && r.services.StateRuntime != nil {
		mode, found, modeErr := r.services.ThreadRouter.threadHistoryModeWithRepair(session.ThreadID(threadID))
		if modeErr != nil {
			return modeErr
		}
		paginatedResume = found && strings.EqualFold(strings.TrimSpace(mode), string(ThreadHistoryPaginated))
	}
	activeParams := r.activeTurnParams(threadID)
	response.CWD = ""
	response.Model = ""
	response.ModelProvider = ""
	if activeParams != nil {
		response.CWD = strings.TrimSpace(activeParams.CWD)
		response.Model = strings.TrimSpace(activeParams.Model)
		response.ModelProvider = strings.TrimSpace(providerFromTurnStart(activeParams))
	}
	response.CWD = firstNonEmpty(response.CWD, strings.TrimSpace(record.Metadata.CWD))
	response.Model = firstNonEmpty(response.Model, strings.TrimSpace(record.Metadata.Model))
	response.ModelProvider = firstNonEmpty(response.ModelProvider, strings.TrimSpace(record.Metadata.ModelProvider))
	if !params.ExcludeTurns {
		if paginatedResume {
			turns, loadErr := r.services.ThreadRouter.loadPaginatedThreadFullTurnsWithOptions(threadID, &turnsResponseOptions{
				LoadedStatus: loadedStatus, HasLiveRunningThread: true,
			})
			if loadErr != nil {
				return loadErr
			}
			response.Thread.Turns = turns
		}
		response.Thread.Turns = mergeTurnHistoryWithActiveTurn(response.Thread.Turns, *activeTurn)
	}
	if params.InitialTurnsPage != nil {
		if paginatedResume {
			page, pageErr := r.paginatedRunningResumeInitialTurnsPage(threadID, params.InitialTurnsPage, *activeTurn, loadedStatus)
			if pageErr != nil {
				return pageErr
			}
			response.InitialTurnsPage = page
		} else {
			page, pageErr := buildTurnsResponse(record, &ThreadTurnsListParams{
				ThreadID:      threadID,
				Limit:         params.InitialTurnsPage.Limit,
				SortDirection: params.InitialTurnsPage.SortDirection,
				ItemsView:     params.InitialTurnsPage.ItemsView,
			}, &turnsResponseOptions{ActiveTurn: activeTurn, LoadedStatus: loadedStatus})
			if pageErr != nil {
				return pageErr
			}
			response.InitialTurnsPage = page
		}
	}
	return nil
}

func (r *RuntimeRouter) paginatedRunningResumeInitialTurnsPage(threadID string, params *ThreadInitialPageParams, activeTurn Turn, loadedStatus ThreadStatus) (*TurnsPage, error) {
	options := &turnsResponseOptions{LoadedStatus: loadedStatus, HasLiveRunningThread: true}
	page, err := r.services.ThreadRouter.buildPaginatedResumeInitialTurnsPage(threadID, params, options)
	if err != nil {
		return nil, err
	}
	pageSize := threadTurnsDefaultLimit
	if params.Limit != nil {
		pageSize = *params.Limit
	}
	if pageSize < 1 {
		pageSize = 1
	}
	if pageSize > threadTurnsMaxLimit {
		pageSize = threadTurnsMaxLimit
	}
	sortDirection := params.SortDirection
	if sortDirection == "" {
		sortDirection = SortDesc
	}
	activeInPage := turnsPageHasTurn(page, activeTurn.ID)
	if sortDirection == SortDesc && !activeInPage {
		if pageSize == 1 {
			page.NextCursor = cloneString(page.BackwardsCursor)
			page.Data = []Turn{}
		} else {
			reservedLimit := pageSize - 1
			page, err = r.services.ThreadRouter.buildPaginatedResumeInitialTurnsPage(threadID, &ThreadInitialPageParams{
				Limit: &reservedLimit, SortDirection: params.SortDirection, ItemsView: params.ItemsView,
			}, options)
			if err != nil {
				return nil, err
			}
		}
	}
	activeItems := []Turn{activeTurn}
	applyTurnItemsView(activeItems, params.ItemsView)
	active := activeItems[0]
	page.Data = removeTurnByID(page.Data, active.ID)
	if sortDirection == SortDesc {
		page.Data = append([]Turn{active}, page.Data...)
	} else if activeInPage || (len(page.Data) < pageSize && page.NextCursor == nil) {
		page.Data = append(page.Data, active)
	}
	normalizeThreadTurnsStatus(page.Data, loadedStatus, true)
	return page, nil
}

func turnsPageHasTurn(page *TurnsPage, turnID string) bool {
	if page == nil {
		return false
	}
	for i := range page.Data {
		if page.Data[i].ID == turnID {
			return true
		}
	}
	return false
}

func removeTurnByID(turns []Turn, turnID string) []Turn {
	result := make([]Turn, 0, len(turns))
	for _, turn := range turns {
		if turn.ID != turnID {
			result = append(result, turn)
		}
	}
	return result
}

func (r *RuntimeRouter) markThreadResumeSessionStartSource(result any, request *Request) {
	if r == nil || request == nil {
		return
	}
	response, ok := result.(*ThreadResumeResponse)
	if !ok || response == nil || response.Thread == nil {
		return
	}
	var params ThreadResumeParams
	if err := request.DecodeParams(&params); err != nil {
		return
	}
	if threadID := strings.TrimSpace(params.ThreadID); threadID != "" && r.activeRuntimeTurnSnapshot(threadID) != nil {
		return
	}
	_ = r.markThreadPendingSessionStartSource(response.Thread.ID, SessionStartSourceResume)
}

func (r *RuntimeRouter) persistThreadRecordRuntimeWorkspaceRoots(threadID string, roots []string) error {
	threadID = strings.TrimSpace(threadID)
	if r == nil || threadID == "" || r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil {
		return nil
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, false)
	if err != nil || record == nil {
		return err
	}
	extra := cloneAnyMap(record.Metadata.Extra)
	if len(roots) > 0 && extra == nil {
		extra = map[string]any{}
	}
	if len(roots) > 0 {
		extra["runtime_workspace_roots"] = append([]string(nil), roots...)
	} else if len(extra) > 0 {
		delete(extra, "runtime_workspace_roots")
	} else {
		return nil
	}
	if runtimeRecordEphemeral(record) {
		record.Metadata.Extra = extra
		r.saveEphemeralThreadRecord(record)
		return nil
	}
	_, err = r.runtimeUpdateThreadMetadata(session.ThreadID(threadID), &session.MetadataPatch{Extra: extra}, true)
	return err
}

func (r *RuntimeRouter) validateTurnStartEnvironments(params *turn.TurnStartParams) error {
	if params == nil {
		return nil
	}
	return r.validateTurnEnvironmentSelections(params.Environments)
}

func (r *RuntimeRouter) validateThreadStartEnvironments(params *ThreadStartParams) error {
	if params == nil {
		return nil
	}
	return r.validateTurnEnvironmentSelections(params.Environments)
}

func (r *RuntimeRouter) validateTurnEnvironmentSelections(environments []map[string]any) error {
	if len(environments) == 0 {
		return nil
	}
	manager := r.requireEnvironment()
	for i := range environments {
		environmentID := selectionEnvironmentID(environments[i])
		if environmentID == "" {
			return jsonRPCInvalidRequest("environmentId is required")
		}
		cwd := firstNonEmpty(
			threadItemStringFromAnyMap(environments[i], "cwd"),
			threadItemStringFromAnyMap(environments[i], "CWD"),
		)
		cwd = strings.TrimSpace(cwd)
		if !isAbsoluteAppPath(cwd) {
			return jsonRPCInvalidRequest(fmt.Sprintf("invalid cwd for environment `%s`: path `%s` does not use absolute POSIX or Windows path syntax", environmentID, cwd))
		}
		if _, ok := manager.Record(environmentID); !ok {
			return jsonRPCInvalidRequest(fmt.Sprintf("unknown turn environment id `%s`", environmentID))
		}
		normalizedConfig, err := validateEnvironmentSelectionConfig(environmentID, environments[i])
		if err != nil {
			return jsonRPCInvalidRequest(fmt.Sprintf("invalid environment configuration for `%s`: %v", environmentID, err))
		}
		if normalizedConfig == nil {
			delete(environments[i], "config")
		} else {
			environments[i]["config"] = normalizedConfig
		}
	}
	return nil
}

func isAbsoluteAppPath(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if strings.HasPrefix(value, "/") || strings.HasPrefix(value, `\`) {
		return true
	}
	return len(value) >= 3 && asciiLetter(value[0]) && value[1] == ':' && (value[2] == '/' || value[2] == '\\')
}

func asciiLetter(value byte) bool {
	return (value >= 'A' && value <= 'Z') || (value >= 'a' && value <= 'z')
}

func turnStartSandboxPolicyPresent(policy any) bool {
	if policy == nil {
		return false
	}
	value := reflect.ValueOf(policy)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return !value.IsNil()
	default:
		return true
	}
}

func turnStartSettingsUpdateParams(params *turn.TurnStartParams) (*SettingsUpdateParams, bool) {
	if params == nil {
		return nil, false
	}
	update := &SettingsUpdateParams{ThreadID: strings.TrimSpace(params.ThreadID)}
	hasUpdate := false
	// Rust #44905: a supplied disabled-plugin list replaces the saved selection;
	// an empty list clears it.
	if params.DisabledPluginIDs != nil {
		update.DisabledPluginIDs = params.DisabledPluginIDs
		hasUpdate = true
	}
	if policy, ok := parseTurnApprovalPolicy(params.ApprovalPolicy); ok {
		value := string(policy)
		update.ApprovalPolicy = &value
		hasUpdate = true
	}
	if params.ApprovalsReviewer != nil && strings.TrimSpace(*params.ApprovalsReviewer) != "" {
		update.ApprovalsReviewer = cloneString(params.ApprovalsReviewer)
		hasUpdate = true
	}
	if sandboxPolicy, err := parseTurnSandboxPolicy(params.SandboxPolicy); err == nil && sandboxPolicy != nil {
		value := string(sandboxPolicy.Kind)
		update.SandboxPolicy = &value
		hasUpdate = true
	}
	if cwd := strings.TrimSpace(params.CWD); cwd != "" {
		update.CWD = &cwd
		hasUpdate = true
	}
	if modelID := strings.TrimSpace(params.Model); modelID != "" {
		update.Model = &modelID
		hasUpdate = true
	}
	if params.ServiceTierSet || params.ServiceTier != nil {
		update.ServiceTier = &ThreadExtraOptionalString{Set: true, Value: cloneString(params.ServiceTier)}
		hasUpdate = true
	}
	if params.Effort != nil {
		update.Effort = cloneString(params.Effort)
		hasUpdate = true
	}
	if params.Summary != nil {
		update.Summary = cloneString(params.Summary)
		hasUpdate = true
	}
	if params.CollaborationMode != nil {
		update.CollaborationMode = cloneAnyMap(params.CollaborationMode)
		hasUpdate = true
	}
	if params.Personality != nil {
		update.Personality = cloneString(params.Personality)
		hasUpdate = true
	}
	return update, hasUpdate
}

func threadResumeSettingsUpdateParams(params *ThreadResumeParams, threadID string) (*SettingsUpdateParams, bool) {
	if params == nil {
		return nil, false
	}
	update := &SettingsUpdateParams{ThreadID: strings.TrimSpace(threadID)}
	if update.ThreadID == "" {
		return nil, false
	}
	hasUpdate := false
	if cwd := stringPtrValue(params.CWD); cwd != "" {
		update.CWD = &cwd
		hasUpdate = true
	}
	if modelID := stringPtrValue(params.Model); modelID != "" {
		update.Model = &modelID
		hasUpdate = true
	}
	if params.ServiceTierSet || params.ServiceTier != nil {
		update.ServiceTier = &ThreadExtraOptionalString{Set: true, Value: cloneString(params.ServiceTier)}
		hasUpdate = true
	}
	if params.Personality != nil {
		update.Personality = cloneString(params.Personality)
		update.PersonalitySet = true
		hasUpdate = true
	}
	return update, hasUpdate
}

func (r *RuntimeRouter) applyTurnStartSettingsUpdate(params *SettingsUpdateParams) {
	if r == nil || params == nil {
		return
	}
	service := r.requireThreadExtras()
	if _, err := service.UpdateSettings(params); err != nil {
		return
	}
	if params.DisabledPluginIDs != nil {
		// Rust #44905: a turn-start override is a saved thread setting.
		_ = r.persistThreadSettingsUpdate(params)
	}
	if r.mcpRuntimes != nil {
		r.mcpRuntimes.invalidateThread(params.ThreadID)
	}
	r.prewarmMCPThread(params.ThreadID)
	if settings := service.Settings(params.ThreadID); settings != nil {
		r.notify(NotificationThreadSettingsUpdated, &SettingsUpdatedNotification{
			ThreadID:       strings.TrimSpace(params.ThreadID),
			ThreadSettings: *settings,
		})
	}
}

func (r *RuntimeRouter) prepareTurnStartParams(params *turn.TurnStartParams) error {
	if r == nil || params == nil || r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil {
		return nil
	}
	record, err := r.threadRecord(session.ThreadID(params.ThreadID), false, false)
	if err != nil {
		return err
	}
	if runtimeRecordIsSubagent(record) {
		// Rust 95aada11c4 (#38205): delegated sessions always run with the
		// `never` approval policy (the delegate config is pinned with
		// Constrained::allow_only(Never)). Approval-requiring commands, MCP
		// tool calls and escalations are denied within the delegate instead of
		// being forwarded to the parent session.
		params.ApprovalPolicy = sandbox.ApprovalNever
	}
	settings := r.threadSettingsForTurn(params.ThreadID)
	if strings.TrimSpace(params.CWD) == "" {
		params.CWD = firstNonEmpty(threadSettingsCWD(settings), strings.TrimSpace(record.Metadata.CWD))
	}
	if strings.TrimSpace(params.Model) == "" {
		params.Model = firstNonEmpty(threadSettingsModel(settings), strings.TrimSpace(record.Metadata.Model))
	}
	applyThreadSettingsToTurnStartParams(params, settings)
	applyThreadSettingsEnvironmentConfigToTurnStartParams(params, settings)
	applyCollaborationModeToTurnStartParams(params)
	// Rust #44655: activate this task's plugin selection for the runtime. A
	// pending settings change takes effect on the next task, not this one.
	r.activateThreadDisabledPluginIDs(params, record)
	if strings.TrimSpace(params.Originator) == "" {
		params.Originator = strings.TrimSpace(record.Metadata.Originator)
	}
	if params.Config == nil {
		params.Config = map[string]any{}
	}
	params.Config = mergeTurnConfigOverrides(threadRecordConfigOverrides(record), params.Config)
	if len(params.DynamicTools) == 0 {
		params.DynamicTools = dynamicToolsFromRecordMetadata(record.Metadata)
	}
	params.ExperimentalRawEvents = threadRecordExperimentalRawEvents(record)
	// Rust #44675: reload global AGENTS.md instructions at each turn boundary so
	// edits take effect during an active session.
	r.refreshThreadGlobalInstructions(params, record)
	if params.BaseInstructions == nil && strings.TrimSpace(record.Metadata.BaseInstructions) != "" && !r.baseInstructionsAreModelGenerated(record) {
		params.BaseInstructions = cloneString(&record.Metadata.BaseInstructions)
	} else if params.BaseInstructions == nil && boolFromMap(record.Metadata.Extra, "suppress_model_instructions") {
		empty := ""
		params.BaseInstructions = &empty
	}
	if strings.TrimSpace(providerFromTurnStart(params)) == "" && strings.TrimSpace(record.Metadata.ModelProvider) != "" {
		params.Config["model_provider"] = strings.TrimSpace(record.Metadata.ModelProvider)
	}
	// Rust #39153: restore the thread's persisted active permission profile on
	// cold resumes and forks so the thread does not fall back to the current
	// configured default.
	if params.Permissions == nil && strings.TrimSpace(record.Metadata.ActivePermissionProfile) != "" {
		value := strings.TrimSpace(record.Metadata.ActivePermissionProfile)
		params.Permissions = &value
	}
	return nil
}

// activateThreadDisabledPluginIDs records the plugin selection the admitted task
// runs with, preferring an explicit turn override and otherwise the thread's
// persisted selection (Rust #44655).
func (r *RuntimeRouter) activateThreadDisabledPluginIDs(params *turn.TurnStartParams, record *session.Record) {
	if r == nil || params == nil || r.services.ThreadExtras == nil {
		return
	}
	if strings.TrimSpace(params.ThreadID) == "" {
		return
	}
	var ids []string
	if params.DisabledPluginIDs != nil {
		ids = append([]string{}, (*params.DisabledPluginIDs)...)
	} else if record != nil {
		ids = stringSliceFromAny(record.Metadata.Extra["disabled_plugin_ids"])
	}
	r.services.ThreadExtras.SetActiveDisabledPluginIDs(params.ThreadID, ids)
}

func (r *RuntimeRouter) baseInstructionsAreModelGenerated(record *session.Record) bool {
	if record == nil || strings.TrimSpace(record.Metadata.BaseInstructions) == "" {
		return false
	}
	value := record.Metadata.BaseInstructionsProvenance
	if value != nil {
		return strings.EqualFold(strings.TrimSpace(value.Type), session.BaseInstructionsProvenanceModel)
	}
	info := r.modelInfoForRuntime(strings.TrimSpace(record.Metadata.Model))
	if info == nil {
		return false
	}
	personality := ""
	if overrides := threadRecordConfigOverrides(record); overrides != nil {
		personality = strings.TrimSpace(stringFromAny(firstMapValue(overrides, "personality")))
	}
	return record.Metadata.BaseInstructions == info.ModelInstructions(personality)
}

// applyThreadSettingsEnvironmentConfigToTurnStartParams applies permission-profile and
// workspace-root settings persisted by thread/settings.update to future turns, mirroring
// Rust's permission profile updates flowing into the next turn's environment config.
// Explicit turn-start values always win; the currently active turn keeps its own snapshot.
func applyThreadSettingsEnvironmentConfigToTurnStartParams(params *turn.TurnStartParams, settings *Settings) {
	if params == nil || settings == nil {
		return
	}
	if params.Permissions == nil && settings.ActivePermissionProfile != nil {
		params.Permissions = cloneString(settings.ActivePermissionProfile)
	}
	if !turnStartSandboxPolicyPresent(params.SandboxPolicy) && strings.TrimSpace(settings.SandboxPolicy) != "" {
		params.SandboxPolicy = settings.SandboxPolicy
	}
	if len(params.RuntimeWorkspaceRoots) == 0 && len(settings.RuntimeWorkspaceRoots) > 0 {
		params.RuntimeWorkspaceRoots = append([]string(nil), settings.RuntimeWorkspaceRoots...)
	}
}

func (r *RuntimeRouter) threadSettingsForTurn(threadID string) *Settings {
	if r == nil || r.services.ThreadExtras == nil {
		return nil
	}
	return r.services.ThreadExtras.Settings(threadID)
}

// threadDisabledPluginIDs returns the thread's saved disabled-plugin selection,
// preferring the live settings and falling back to the persisted record
// snapshot so resume/fork restore it (Rust #44905).
func (r *RuntimeRouter) threadDisabledPluginIDs(threadID string) []string {
	if r == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	// The active runtime uses the last admitted task's selection, so a pending
	// settings change cannot alter a running turn (Rust #44655).
	if r.services.ThreadExtras != nil {
		if active, ok := r.services.ThreadExtras.ActiveDisabledPluginIDs(threadID); ok {
			return active
		}
	}
	if settings := r.threadSettingsForTurn(threadID); settings != nil && settings.DisabledPluginIDs != nil {
		return append([]string{}, settings.DisabledPluginIDs...)
	}
	record, err := r.threadRecord(session.ThreadID(threadID), false, false)
	if err != nil || record == nil {
		return nil
	}
	return stringSliceFromAny(record.Metadata.Extra["disabled_plugin_ids"])
}

func applyThreadSettingsToTurnStartParams(params *turn.TurnStartParams, settings *Settings) {
	if params == nil || settings == nil {
		return
	}
	if !params.ServiceTierSet && params.ServiceTier == nil && settings.ServiceTier != nil {
		params.ServiceTier = cloneString(settings.ServiceTier)
	}
	if params.Effort == nil && settings.Effort != nil {
		params.Effort = cloneString(settings.Effort)
	}
	if params.Summary == nil && settings.Summary != nil {
		params.Summary = cloneString(settings.Summary)
	}
	if params.CollaborationMode == nil && settings.CollaborationMode != nil {
		params.CollaborationMode = cloneAnyMap(settings.CollaborationMode)
	}
	if params.Personality == nil && settings.Personality != nil {
		params.Personality = cloneString(settings.Personality)
		params.PersonalitySet = settings.PersonalitySet
	}
	// Rust #44905: a saved disabled-plugin selection is carried into the turn
	// unless the caller supplied its own list.
	if params.DisabledPluginIDs == nil && settings.DisabledPluginIDs != nil {
		values := append([]string{}, settings.DisabledPluginIDs...)
		params.DisabledPluginIDs = &values
	}
}

func applyCollaborationModeToTurnStartParams(params *turn.TurnStartParams) {
	if params == nil || params.CollaborationMode == nil {
		return
	}
	settings := collaborationModeSettings(params.CollaborationMode)
	if len(settings) == 0 {
		return
	}
	if modelID := strings.TrimSpace(stringFromAny(firstMapValue(settings, "model"))); modelID != "" {
		params.Model = modelID
	}
	if effort := strings.TrimSpace(stringFromAny(firstMapValue(settings, "reasoning_effort", "reasoningEffort"))); effort != "" {
		params.Effort = &effort
	}
}

func collaborationModeSettings(mode map[string]any) map[string]any {
	if mode == nil {
		return nil
	}
	raw := firstMapValue(mode, "settings")
	if settings, ok := raw.(map[string]any); ok {
		return settings
	}
	data, err := json.Marshal(raw)
	if err != nil || len(data) == 0 || string(data) == "null" {
		return nil
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil
	}
	return settings
}

func threadRecordConfigOverrides(record *session.Record) map[string]any {
	if record == nil || record.Metadata.Extra == nil {
		return nil
	}
	raw, ok := record.Metadata.Extra["config"]
	if !ok || raw == nil {
		return nil
	}
	if values, ok := raw.(map[string]any); ok {
		return cloneAnyMap(values)
	}
	data, err := json.Marshal(raw)
	if err != nil || len(data) == 0 || string(data) == "null" {
		return nil
	}
	var values map[string]any
	if err := json.Unmarshal(data, &values); err != nil {
		return nil
	}
	return cloneAnyMap(values)
}

func mergeTurnConfigOverrides(threadConfig map[string]any, turnConfig map[string]any) map[string]any {
	if len(threadConfig) == 0 && len(turnConfig) == 0 {
		return map[string]any{}
	}
	merged := map[string]any{}
	for key, value := range threadConfig {
		merged[key] = value
	}
	for key, value := range turnConfig {
		merged[key] = value
	}
	return merged
}

func threadSettingsCWD(settings *Settings) string {
	if settings == nil {
		return ""
	}
	return strings.TrimSpace(settings.CWD)
}

func threadSettingsModel(settings *Settings) string {
	if settings == nil {
		return ""
	}
	return strings.TrimSpace(settings.Model)
}

func dynamicToolsFromRecordMetadata(metadata session.Metadata) []turn.DynamicToolSpec {
	if len(metadata.DynamicTools) > 0 {
		data, err := json.Marshal(metadata.DynamicTools)
		if err == nil {
			var tools []turn.DynamicToolSpec
			if err := json.Unmarshal(data, &tools); err == nil {
				return tools
			}
		}
	}
	return dynamicToolsFromMetadata(metadata.Extra)
}

func dynamicToolsFromMetadata(extra map[string]any) []turn.DynamicToolSpec {
	if extra == nil {
		return nil
	}
	raw, ok := extra["dynamic_tools"]
	if !ok {
		raw = extra["dynamicTools"]
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var tools []turn.DynamicToolSpec
	if err := json.Unmarshal(data, &tools); err != nil {
		return nil
	}
	return tools
}

// effectiveConfigForSessionTelemetry resolves the effective config the
// session-start telemetry reads, or nil when it cannot be read.
func (r *RuntimeRouter) effectiveConfigForSessionTelemetry() *config.Config {
	if r == nil || r.services.Config == nil {
		return nil
	}
	read, err := r.services.Config.Read(&config.ConfigReadParams{})
	if err != nil || read == nil {
		return nil
	}
	return &config.Config{Values: read.Config}
}

func (r *RuntimeRouter) handleTurnSteer(request *Request) (*turn.TurnSteerResponse, error) {
	var params turn.TurnSteerParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := r.ensureDirectInputAllowed(request, params.ThreadID); err != nil {
		return nil, err
	}
	// Rust #44944: steer shares the retained-provider check with turn/start.
	if err := r.checkThreadModelProviderForID(params.ThreadID); err != nil {
		return nil, err
	}
	connectionID := request.normalizedConnectionID()
	createdAt := runtimeRouterNow(r).UTC()
	if err := validateTurnUserInputImageURLs(params.Input); err != nil {
		return nil, err
	}
	response, err := r.requireTurns().Steer(&params)
	if err != nil {
		r.emitCodexTurnSteerAnalyticsEvent(context.Background(), connectionID, &params, nil, telemetry.TurnSteerResultRejected, turnSteerAnalyticsRejectionReason(err), createdAt)
		return nil, turnSteerRuntimeError(err)
	}
	// Rust reports the steered user input like a turn start
	// (SessionTelemetry::user_prompt).
	r.emitUserPromptRecords(context.Background(), params.ThreadID, params.Prompt, params.Input)
	r.updateActiveTurnApprovalsReviewer(&params)
	if r.hasRuntimeThreadStore() {
		if item, ok := sessionItemFromTurnSteer(&params); ok {
			if _, err := r.runtimeAppendItem(session.ThreadID(params.ThreadID), item); err != nil {
				return nil, err
			}
			_ = r.appendRuntimeRollout(params.ThreadID, []session.Item{item}, item.CreatedAt)
			threadItem := BuildThreadItem(item)
			r.notify(NotificationItemStarted, &ItemStartedNotification{
				Item:        threadItemPayload(threadItem),
				ThreadID:    params.ThreadID,
				TurnID:      params.ExpectedTurnID,
				StartedAtMS: item.CreatedAt.UTC().UnixMilli(),
			})
			r.notify(NotificationItemCompleted, &ItemCompletedNotification{
				Item:          threadItemPayload(threadItem),
				ThreadID:      params.ThreadID,
				TurnID:        params.ExpectedTurnID,
				CompletedAtMS: item.CreatedAt.UTC().UnixMilli(),
			})
		}
	}
	noticeEnabled := r.imageResizeNoticeEnabledForSteer(&params)
	steerMetadata := r.steerClientMetadata(&params)
	if inputItems := inputItemsFromTurnSteerWithNotice(&params, noticeEnabled); len(inputItems) > 0 {
		if len(steerMetadata) > 0 {
			// Rust merges a steer's responsesapi_client_metadata into the turn's
			// metadata state, so the turn's later sampling steps keep reporting
			// it instead of falling back to the turn-start entries.
			r.rememberTurnSteerMetadata(params.ThreadID, params.ExpectedTurnID, steerMetadata)
		}
		if err := r.requireSteerMailbox().Enqueue(&turn.SteerEnqueueParams{
			ThreadID:       params.ThreadID,
			TurnID:         params.ExpectedTurnID,
			InputItems:     inputItems,
			ClientMetadata: steerMetadata,
		}); err != nil {
			return nil, err
		}
	}
	r.noteAcceptedTurnSteer(params.ThreadID, params.ExpectedTurnID)
	r.emitCodexTurnSteerAnalyticsEvent(context.Background(), connectionID, &params, stringPtrIfNotEmpty(response.TurnID), telemetry.TurnSteerResultAccepted, nil, createdAt)
	return response, nil
}

// rememberTurnSteerMetadata records a steer's request metadata on the active
// turn so every later sampling step reports it (Rust's
// TurnMetadataState::set_responsesapi_client_metadata merge).
func (r *RuntimeRouter) rememberTurnSteerMetadata(threadID string, turnID string, metadata map[string]string) {
	if r == nil || len(metadata) == 0 {
		return
	}
	r.threads.UpdateTurn(strings.TrimSpace(threadID), strings.TrimSpace(turnID), func(active *activeRuntimeTurn) {
		if active == nil || active.RunConfig == nil {
			return
		}
		merged := cloneStringMap(active.RunConfig.ClientMetadata)
		if merged == nil {
			merged = map[string]string{}
		}
		for key, value := range metadata {
			merged[key] = value
		}
		active.RunConfig.ClientMetadata = merged
	})
}

func (r *RuntimeRouter) updateActiveTurnApprovalsReviewer(params *turn.TurnSteerParams) {
	if r == nil || params == nil || params.ApprovalsReviewer == nil {
		return
	}
	reviewer := strings.TrimSpace(*params.ApprovalsReviewer)
	if reviewer == "" {
		return
	}
	cfg, err := r.effectiveConfigForTurn(&turn.TurnStartParams{ThreadID: params.ThreadID})
	if err != nil || cfg == nil {
		return
	}
	// Reuse the normal turn-start reviewer resolution so managed/model-required
	// restrictions continue to apply to the live update.
	updatedParams := &turn.TurnStartParams{ThreadID: params.ThreadID, ApprovalsReviewer: &reviewer}
	effectiveReviewer := turnApprovalsReviewerForTurn(cfg, updatedParams)
	r.threads.UpdateTurn(params.ThreadID, params.ExpectedTurnID, func(active *activeRuntimeTurn) {
		if active.Params == nil {
			active.Params = &turn.TurnStartParams{ThreadID: params.ThreadID}
		}
		active.Params.ApprovalsReviewer = &effectiveReviewer
		if active.RunConfig != nil {
			active.RunConfig.ApprovalsReviewer = effectiveReviewer
		}
	})
}

func (r *RuntimeRouter) handleTurnSettingsUpdate(request *Request) (*turn.TurnSettingsUpdateResponse, error) {
	var params turn.TurnSettingsUpdateParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, jsonRPCInvalidRequest(err.Error())
	}
	if params.ApprovalsReviewer != nil {
		r.updateActiveTurnApprovalsReviewer(&turn.TurnSteerParams{
			ThreadID:          params.ThreadID,
			ExpectedTurnID:    params.TurnID,
			ApprovalsReviewer: params.ApprovalsReviewer,
		})
	}
	r.updateActiveTurnRuntimeSettings(&params)
	return &turn.TurnSettingsUpdateResponse{}, nil
}

func (r *RuntimeRouter) updateActiveTurnRuntimeSettings(params *turn.TurnSettingsUpdateParams) {
	if r == nil || params == nil {
		return
	}
	r.threads.UpdateTurn(params.ThreadID, params.TurnID, func(active *activeRuntimeTurn) {
		if active == nil {
			return
		}
		if active.Params == nil {
			active.Params = &turn.TurnStartParams{ThreadID: params.ThreadID}
		}
		if params.Model != nil {
			active.Params.Model = strings.TrimSpace(*params.Model)
			if active.RunConfig != nil {
				active.RunConfig.Model = active.Params.Model
			}
		}
		if params.Effort != nil {
			active.Params.Effort = cloneString(params.Effort)
			if active.RunConfig != nil {
				active.RunConfig.ReasoningEffort = strings.TrimSpace(*params.Effort)
			}
		}
		if params.Summary != nil {
			active.Params.Summary = cloneString(params.Summary)
			if active.RunConfig != nil {
				active.RunConfig.ReasoningSummary = strings.TrimSpace(*params.Summary)
			}
		}
		if params.ServiceTier != nil {
			active.Params.ServiceTier = cloneString(params.ServiceTier)
			active.Params.ServiceTierSet = true
			if active.RunConfig != nil {
				active.RunConfig.ServiceTier = strings.TrimSpace(*params.ServiceTier)
			}
		}
	})
}

func turnSteerRuntimeError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, turn.ErrNoActiveTurnToSteer):
		return jsonRPCInvalidRequest("no active turn to steer")
	case errors.Is(err, turn.ErrExpectedTurnMismatch):
		return jsonRPCInvalidRequest(strings.TrimSpace(err.Error()))
	case errors.Is(err, turn.ErrEmptyTurnSteerInput):
		return jsonRPCInvalidRequest("input must not be empty")
	}
	message := strings.TrimSpace(err.Error())
	if strings.Contains(message, "expectedTurnId must not be empty") {
		return jsonRPCInvalidRequest("expectedTurnId must not be empty")
	}
	return err
}

func (r *RuntimeRouter) handleTurnInterrupt(request *Request) (*turn.TurnInterruptResponse, error) {
	var params turn.TurnInterruptParams
	requestedAtMS := uint64(time.Now().UnixMilli())
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if r.hasRuntimeThreadStore() && r.activeRuntimeTurnIsReview(params.ThreadID, params.TurnID) {
		if active, ok := r.cancelActiveRuntimeTurn(params.ThreadID, params.TurnID); ok {
			analytics := analyticsContextFromActiveRuntimeTurn(active)
			if analytics != nil {
				analytics.ExplicitClientInterruptRequestedAtMS = &requestedAtMS
			}
			r.finishReviewRuntimeInterrupted(params.ThreadID, params.TurnID, active.StartedAtMS, analytics)
			return &turn.TurnInterruptResponse{}, nil
		}
	}
	response, err := r.requireTurns().Interrupt(&params)
	if err != nil {
		return nil, turnInterruptRuntimeError(err)
	}
	if r.hasRuntimeThreadStore() {
		if active, ok := r.cancelActiveRuntimeTurnTracked(params.ThreadID, params.TurnID); ok {
			analytics := analyticsContextFromActiveRuntimeTurn(active)
			if analytics != nil {
				analytics.ExplicitClientInterruptRequestedAtMS = &requestedAtMS
			}
			// Rust #40511: run the Interrupt hook for an active top-level turn
			// before its interrupted abort event is emitted.
			r.runInterruptHook(active)
			// Match Rust app-server ordering: acknowledge turn/interrupt before
			// publishing the interrupted terminal lifecycle notifications.
			go func() {
				defer r.threads.TurnWorkerDone()
				r.finishTurnInterruptedAnalytics(params.ThreadID, params.TurnID, active.StartedAtMS, analytics)
			}()
		}
	}
	return response, nil
}

func turnInterruptRuntimeError(err error) error {
	if err == nil {
		return nil
	}
	message := strings.TrimSpace(err.Error())
	if strings.Contains(message, " is not active") {
		return jsonRPCInvalidRequest(message)
	}
	return err
}

func (r *RuntimeRouter) handleReviewStart(request *Request) (*review.StartResponse, error) {
	var params review.StartParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	// Rust #42602: detached review delivery is deprecated. Emit the
	// connection-scoped notice before validation so callers still see it when
	// the request is later rejected.
	if reviewDeliveryDetached(&params) {
		details := "Use thread/start followed by review/start with delivery \"inline\" for a separate review thread, or thread/fork followed by turn/start with your own review instructions."
		r.notifyToConnection(request.normalizedConnectionID(), NotificationDeprecationNotice, &DeprecationNoticeNotification{
			Summary: "review/start with delivery \"detached\" is deprecated and will be removed in a future release.",
			Details: &details,
		})
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	// Rust #44944: reviews run on the parent thread's retained provider route.
	if err := r.checkThreadModelProviderForID(params.ThreadID); err != nil {
		return nil, err
	}
	reviewThreadID, err := r.prepareDetachedReviewThread(request, &params)
	if err != nil {
		return nil, err
	}
	response, err := r.requireReviews().Start(&params)
	if err != nil {
		return nil, err
	}
	if reviewThreadID != "" {
		response.ReviewThreadID = reviewThreadID
	}
	if err := r.persistEnteredReviewMode(&params, response); err != nil {
		return nil, err
	}
	r.notifyEnteredReviewMode(&params, response)
	r.startReviewRuntimeAsync(&params, response, request.normalizedConnectionID())
	return response, nil
}

func (r *RuntimeRouter) prepareDetachedReviewThread(request *Request, params *review.StartParams) (string, error) {
	if r == nil || request == nil || params == nil || !reviewDeliveryDetached(params) {
		return "", nil
	}
	if r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil {
		return "", nil
	}
	parentID := session.ThreadID(strings.TrimSpace(params.ThreadID))
	parent, err := r.threadRecord(parentID, true, false)
	if err != nil {
		return "", threadReadError(string(parentID), err)
	}
	if threadUsesPaginatedHistory(parent) {
		return "", jsonRPCInvalidRequest("paginated threads do not support detached review")
	}
	if err := r.ensureDetachedReviewParentMaterialized(parentID); err != nil {
		return "", err
	}
	forkParams := ThreadForkParams{
		ThreadID:     string(parentID),
		ExcludeTurns: true,
		Model:        r.detachedReviewModelOverride(),
	}
	forkRequest, err := requestWithRuntimeParams(request, forkParams)
	if err != nil {
		return "", err
	}
	forkRequest.Method = MethodThreadFork
	result, err := r.handleThreadLifecycleRuntime(forkRequest)
	if err != nil {
		return "", err
	}
	response, ok := result.(*ThreadForkResponse)
	if !ok || response.Thread == nil {
		return "", nil
	}
	return response.Thread.ID, nil
}

func (r *RuntimeRouter) detachedReviewModelOverride() *string {
	return r.reviewModelOverride()
}

func (r *RuntimeRouter) reviewModelOverride() *string {
	if r == nil || r.services.Config == nil {
		return nil
	}
	read, err := r.requireConfig().Read(&config.ConfigReadParams{})
	if err != nil || read == nil {
		return nil
	}
	modelID := strings.TrimSpace(firstNonEmpty(stringFromMap(read.Config, "review_model"), stringFromMap(read.Config, "reviewModel")))
	return stringPtrIfNotEmpty(modelID)
}

func reviewDeliveryDetached(params *review.StartParams) bool {
	return params != nil && params.Delivery != nil && strings.TrimSpace(*params.Delivery) == "detached"
}

func (r *RuntimeRouter) ensureDetachedReviewParentMaterialized(threadID session.ThreadID) error {
	record, err := r.threadRecord(threadID, true, true)
	if err != nil {
		return threadReadError(string(threadID), err)
	}
	if !unmaterializedThread(record) {
		return nil
	}
	now := record.CreatedAt
	if now.IsZero() {
		now = r.services.ThreadRouter.now().UTC()
	}
	return r.services.ThreadRouter.createThreadRollout(record, now)
}

func (r *RuntimeRouter) notifyEnteredReviewMode(params *review.StartParams, response *review.StartResponse) {
	if r == nil || params == nil || response == nil {
		return
	}
	nowMS := time.Now().UTC().UnixMilli()
	threadID := strings.TrimSpace(response.ReviewThreadID)
	if threadID == "" {
		threadID = strings.TrimSpace(params.ThreadID)
	}
	turnID := strings.TrimSpace(response.Turn.ID)
	if turnID == "" {
		turnID = "review-" + strings.TrimSpace(params.ThreadID)
	}
	item := ThreadItem{
		ID:   turnID,
		Type: "enteredReviewMode",
		Text: review.UserFacingHintForTarget(params.Target.ToTarget()),
	}
	payload := threadItemPayload(item)
	r.notify(NotificationItemStarted, &ItemStartedNotification{
		Item:        payload,
		ThreadID:    threadID,
		TurnID:      turnID,
		StartedAtMS: nowMS,
	})
	r.notify(NotificationItemCompleted, &ItemCompletedNotification{
		Item:          payload,
		ThreadID:      threadID,
		TurnID:        turnID,
		CompletedAtMS: nowMS,
	})
}

func (r *RuntimeRouter) persistEnteredReviewMode(params *review.StartParams, response *review.StartResponse) error {
	if r == nil || params == nil || response == nil || !r.hasRuntimeThreadStore() {
		return nil
	}
	threadID := strings.TrimSpace(response.ReviewThreadID)
	if threadID == "" {
		threadID = strings.TrimSpace(params.ThreadID)
	}
	turnID := strings.TrimSpace(response.Turn.ID)
	if turnID == "" {
		turnID = "review-" + strings.TrimSpace(params.ThreadID)
	}
	hint := review.UserFacingHintForTarget(params.Target.ToTarget())
	createdAt := time.Now().UTC()
	item := session.Item{
		ID:        turnID,
		Type:      "enteredReviewMode",
		Text:      hint,
		CreatedAt: createdAt,
		Metadata: appTurnMetadata(turnID, map[string]any{
			"kind":   "review_enter",
			"review": hint,
		}),
	}
	if _, err := r.runtimeAppendItem(session.ThreadID(threadID), item); err != nil {
		return err
	}
	_ = r.appendRuntimeRollout(threadID, []session.Item{item}, createdAt)
	return nil
}

func (r *RuntimeRouter) dispatchThreadExtra(request *Request) (any, error) {
	switch request.Method {
	case MethodThreadGoalSet:
		var params GoalSetParams
		if err := request.DecodeParams(&params); err != nil {
			return nil, err
		}
		return r.setThreadGoal(&params, request.normalizedConnectionID())
	case MethodThreadGoalGet:
		var params GoalGetParams
		if err := request.DecodeParams(&params); err != nil {
			return nil, err
		}
		return r.getThreadGoal(&params)
	case MethodThreadGoalClear:
		var params GoalClearParams
		if err := request.DecodeParams(&params); err != nil {
			return nil, err
		}
		return r.clearThreadGoal(&params, request.normalizedConnectionID())
	case MethodThreadSettingsUpdate:
		var params SettingsUpdateParams
		if err := request.DecodeParams(&params); err != nil {
			return nil, err
		}
		if strings.TrimSpace(params.ThreadID) == "" {
			return nil, fmt.Errorf("%w: threadId is required", ErrInvalidThreadExtraRequest)
		}
		if err := r.ensureDirectInputAllowed(request, params.ThreadID); err != nil {
			return nil, err
		}
		if err := r.requireLoadedThreadForRuntimeOp(params.ThreadID); err != nil {
			return nil, err
		}
		if params.Permissions != nil && params.SandboxPolicy != nil {
			return nil, jsonRPCInvalidRequest("`permissions` cannot be combined with `sandboxPolicy`")
		}
		// Rust turn_processor build_thread_settings_overrides rejects an
		// explicit permission profile that managed requirements disallow.
		if params.Permissions != nil {
			cwd, cwdErr := r.threadShellCommandCWD(params.ThreadID)
			if cwdErr != nil {
				return nil, jsonRPCInvalidRequest("invalid thread settings override: " + cwdErr.Error())
			}
			cfg, cfgErr := r.effectiveConfigForTurn(&turn.TurnStartParams{ThreadID: params.ThreadID, CWD: cwd})
			if cfgErr != nil {
				return nil, jsonRPCInvalidRequest("invalid thread settings override: " + cfgErr.Error())
			}
			if err := rejectDisallowedPermissionProfileOverride(cfg, "invalid thread settings override", params.Permissions); err != nil {
				return nil, err
			}
		}
		service := r.requireThreadExtras()
		response, err := service.UpdateSettings(&params)
		if err != nil {
			return nil, err
		}
		if err := r.persistThreadSettingsUpdate(&params); err != nil {
			return nil, err
		}
		if r.mcpRuntimes != nil {
			r.mcpRuntimes.invalidateThread(params.ThreadID)
		}
		r.prewarmMCPThread(params.ThreadID)
		if settings := service.Settings(params.ThreadID); settings != nil {
			r.notify(NotificationThreadSettingsUpdated, &SettingsUpdatedNotification{
				ThreadID:       strings.TrimSpace(params.ThreadID),
				ThreadSettings: *settings,
			})
		}
		return response, nil
	case MethodThreadShellCommand:
		var params ShellCommandParams
		if err := request.DecodeParams(&params); err != nil {
			return nil, err
		}
		return r.handleThreadShellCommand(&params)
	case MethodThreadBackgroundTerminalsClean:
		var params BackgroundTerminalsCleanParams
		if err := request.DecodeParams(&params); err != nil {
			return nil, err
		}
		if err := params.Validate(); err != nil {
			return nil, err
		}
		if err := r.requireLoadedThreadForRuntimeOp(params.ThreadID); err != nil {
			return nil, err
		}
		return r.cleanBackgroundTerminals(&params)
	case MethodThreadBackgroundTerminalsList:
		var params BackgroundTerminalsListParams
		if err := request.DecodeParams(&params); err != nil {
			return nil, err
		}
		if err := params.Validate(); err != nil {
			return nil, err
		}
		if err := r.requireLoadedThreadForRuntimeOp(params.ThreadID); err != nil {
			return nil, err
		}
		return r.listBackgroundTerminals(&params)
	case MethodThreadBackgroundTerminalsTerminate:
		var params BackgroundTerminalsTerminateParams
		if err := request.DecodeParams(&params); err != nil {
			return nil, err
		}
		if err := params.Validate(); err != nil {
			return nil, err
		}
		if err := r.requireLoadedThreadForRuntimeOp(params.ThreadID); err != nil {
			return nil, err
		}
		return r.terminateBackgroundTerminal(&params)
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnknownMethod, request.Method)
	}
}

func (r *RuntimeRouter) persistThreadSettingsUpdate(params *SettingsUpdateParams) error {
	if r == nil || params == nil || (params.ApprovalPolicy == nil && params.Permissions == nil && params.DisabledPluginIDs == nil && params.CollaborationMode == nil) {
		return nil
	}
	threadID := session.ThreadID(strings.TrimSpace(params.ThreadID))
	record, err := r.threadRecord(threadID, true, true)
	if err != nil || record == nil {
		// The thread may only exist in-memory (e.g. before its first turn);
		// durable persistence is best-effort.
		return nil
	}
	record.Metadata.Extra = ensureRecordExtra(record.Metadata.Extra)
	configOverrides := threadRecordConfigOverrides(record)
	if configOverrides == nil {
		configOverrides = map[string]any{}
	}
	if params.ApprovalPolicy != nil && strings.TrimSpace(*params.ApprovalPolicy) != "" {
		policy := strings.TrimSpace(*params.ApprovalPolicy)
		record.Metadata.ApprovalPolicy = policy
		configOverrides["approval_policy"] = policy
	}
	if params.Permissions != nil && strings.TrimSpace(*params.Permissions) != "" {
		profile := strings.TrimSpace(*params.Permissions)
		record.Metadata.ActivePermissionProfile = profile
	}
	record.Metadata.Extra["config"] = configOverrides
	if params.DisabledPluginIDs != nil {
		// Rust #44905: persist the per-thread selection so resume and fork
		// restore it instead of falling back to the config default.
		record.Metadata.Extra["disabled_plugin_ids"] = append([]string{}, (*params.DisabledPluginIDs)...)
	}
	// Rust #41567: persist the thread-owned cwd on the settings snapshot so a
	// cold resume restores the thread's own working directory.
	settingsCWD := ""
	if r.services.ThreadExtras != nil {
		if settings := r.services.ThreadExtras.Settings(strings.TrimSpace(params.ThreadID)); settings != nil {
			settingsCWD = strings.TrimSpace(settings.CWD)
		}
	}
	if err := r.runtimeSaveThreadRecord(record); err != nil {
		return err
	}
	appliedPolicy := strings.TrimSpace(record.Metadata.ApprovalPolicy)
	return r.services.ThreadRouter.appendThreadSettingsAppliedWithSnapshot(
		threadID,
		appliedPolicy,
		settingsCWD,
		r.threadCollaborationModeSnapshot(params.ThreadID),
		runtimeRouterNow(r).UTC(),
	)
}

// threadCollaborationModeSnapshot serializes the thread's saved collaboration
// mode for the settings snapshot (Rust #45519: resume restores the saved mode
// from the latest matching ThreadSettingsApplied event).
func (r *RuntimeRouter) threadCollaborationModeSnapshot(threadID string) json.RawMessage {
	settings := r.threadSettingsForTurn(threadID)
	if settings == nil || settings.CollaborationMode == nil {
		return nil
	}
	data, err := json.Marshal(settings.CollaborationMode)
	if err != nil || len(data) == 0 || string(data) == "null" {
		return nil
	}
	return data
}

// applyThreadResumeCollaborationMode mirrors Rust #45519: a resume reports the
// effective collaboration mode - the live snapshot when the process still holds
// one, otherwise the mode restored from the rollout overlaid with the resumed
// session's effective model and reasoning effort - and restores it for the
// resumed session so its first turn keeps using it. The overlaying keeps the
// saved mode and its developer instructions (Rust's
// `with_updates(Some(model), Some(config.model_reasoning_effort), None)`), and
// the updated mode is written back to the thread-owned settings snapshot that
// the next resume restores from.
func (r *RuntimeRouter) applyThreadResumeCollaborationMode(result any, resumeConfig *config.Config) {
	response, ok := result.(*ThreadResumeResponse)
	if !ok || response == nil || response.Thread == nil {
		return
	}
	threadID := strings.TrimSpace(response.Thread.ID)
	if threadID == "" {
		return
	}
	var live *CollaborationMode
	if settings := r.threadSettingsForTurn(threadID); settings != nil {
		live = collaborationModeFromAnyMap(settings.CollaborationMode)
	}
	// A live thread reports its own effective mode; nothing is restored.
	if r.threadIsLoaded(threadID) {
		if live != nil {
			response.CollaborationMode = live
		}
		return
	}
	persisted := cloneCollaborationMode(response.CollaborationMode)
	if persisted == nil {
		if live != nil {
			response.CollaborationMode = live
		}
		return
	}
	effective := cloneCollaborationMode(persisted)
	if resumeConfig != nil {
		model, effort := resumeEffectiveModelAndEffort(resumeConfig, response.Model)
		effective = collaborationModeWithUpdates(persisted, model, effort)
		if response.ReasoningEffort == nil && effort != nil {
			response.ReasoningEffort = stringPtrIfNotEmpty(string(*effort))
		}
	}
	if effective == nil {
		return
	}
	response.CollaborationMode = cloneCollaborationMode(effective)
	restored := collaborationModeToAnyMap(effective)
	if restored == nil {
		return
	}
	if _, err := r.requireThreadExtras().UpdateSettings(&SettingsUpdateParams{
		ThreadID:          threadID,
		CollaborationMode: restored,
	}); err != nil {
		return
	}
	// Rust persists the resumed settings snapshot, so the next resume restores
	// the mode with the effective model and effort.
	_ = r.persistThreadSettingsUpdate(&SettingsUpdateParams{
		ThreadID:          threadID,
		CollaborationMode: restored,
	})
}

// resumeEffectiveModelAndEffort resolves the model and reasoning effort the
// resumed session will use, mirroring the values Rust applies to the restored
// collaboration mode and reports as `reasoning_effort`.
func resumeEffectiveModelAndEffort(cfg *config.Config, fallbackModel string) (string, *ReasoningEffort) {
	model := firstNonEmpty(stringConfigValue(cfg, "model"), strings.TrimSpace(fallbackModel))
	effortText := firstNonEmpty(
		stringConfigValue(cfg, "model_reasoning_effort"),
		stringConfigValue(cfg, "modelReasoningEffort"),
	)
	if effortText == "" {
		return model, nil
	}
	effort := ReasoningEffort(effortText)
	return model, &effort
}

// configRequirementsRead mirrors Rust's configRequirements/read (#45495): the
// response reports the effective login methods the running policy permits after
// managed requirements, the forced login method, and workspace restrictions. A
// host with no managed requirements still returns a requirements object when
// that policy restricts login methods, while the unrestricted default keeps
// `requirements: null`.
func (r *RuntimeRouter) configRequirementsRead() *config.ConfigRequirementsReadResponse {
	read := r.requireConfig().Requirements()
	if read == nil {
		read = &config.ConfigRequirementsReadResponse{}
	}
	var values map[string]any
	if r != nil && r.services.Config != nil {
		if cfgRead, err := r.services.Config.Read(&config.ConfigReadParams{}); err == nil && cfgRead != nil {
			values = cfgRead.Config
		}
	}
	cfg := &config.Config{Values: values, Requirements: read.Requirements}
	return &config.ConfigRequirementsReadResponse{
		Requirements: config.ProjectRequirementsWithAllowedLoginMethods(read.Requirements, cfg.AllowedLoginMethods()),
	}
}

// collaborationModeFromAnyMap converts a saved collaboration-mode document into
// the protocol type.
func collaborationModeFromAnyMap(mode map[string]any) *CollaborationMode {
	if mode == nil {
		return nil
	}
	data, err := json.Marshal(mode)
	if err != nil || len(data) == 0 {
		return nil
	}
	return collaborationModeFromRaw(data)
}

// collaborationModeToAnyMap converts the protocol type back into the saved
// settings shape.
func collaborationModeToAnyMap(mode *CollaborationMode) map[string]any {
	if mode == nil {
		return nil
	}
	data, err := json.Marshal(mode)
	if err != nil || len(data) == 0 {
		return nil
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil
	}
	return decoded
}

func (r *RuntimeRouter) cleanBackgroundTerminals(params *BackgroundTerminalsCleanParams) (*BackgroundTerminalsCleanResponse, error) {
	response, err := r.requireThreadExtras().CleanBackgroundTerminals(params)
	if err != nil {
		return nil, err
	}
	if r != nil && r.services.UnifiedExec != nil {
		r.services.UnifiedExec.TerminateAll(strings.TrimSpace(params.ThreadID))
	}
	return response, nil
}

func (r *RuntimeRouter) listBackgroundTerminals(params *BackgroundTerminalsListParams) (*BackgroundTerminalsListResponse, error) {
	base, err := r.requireThreadExtras().ListBackgroundTerminals(&BackgroundTerminalsListParams{ThreadID: params.ThreadID})
	if err != nil {
		return nil, err
	}
	terminals := append([]BackgroundTerminal(nil), base.Data...)
	if r != nil && r.services.UnifiedExec != nil {
		for _, process := range r.services.UnifiedExec.ListProcesses(strings.TrimSpace(params.ThreadID)) {
			terminals = append(terminals, BackgroundTerminal{
				ItemID:    process.ItemID,
				ProcessID: strconv.Itoa(process.ProcessID),
				Command:   process.Command,
				CWD:       process.CWD,
			})
		}
	}
	sort.Slice(terminals, func(i int, j int) bool { return terminals[i].ProcessID < terminals[j].ProcessID })
	page, next, err := PaginateBackgroundTerminals(terminals, params.Cursor, params.Limit)
	if err != nil {
		return nil, err
	}
	return &BackgroundTerminalsListResponse{Data: page, NextCursor: next}, nil
}

func (r *RuntimeRouter) terminateBackgroundTerminal(params *BackgroundTerminalsTerminateParams) (*BackgroundTerminalsTerminateResponse, error) {
	response, err := r.requireThreadExtras().TerminateBackgroundTerminal(params)
	if err != nil || response.Terminated || r == nil || r.services.UnifiedExec == nil {
		return response, err
	}
	processID, parseErr := strconv.Atoi(strings.TrimSpace(params.ProcessID))
	if parseErr != nil {
		return nil, jsonRPCInvalidRequest(fmt.Sprintf("invalid background terminal process id: %v", parseErr))
	}
	response.Terminated = r.services.UnifiedExec.TerminateProcess(strings.TrimSpace(params.ThreadID), processID)
	return response, nil
}

func (r *RuntimeRouter) setThreadGoal(params *GoalSetParams, connectionID string) (*GoalSetResponse, error) {
	if !r.goalsFeatureEnabled() {
		return nil, jsonRPCInvalidRequest("goals feature is disabled")
	}
	r.applyGoalTokenBudgetLimit(params)
	if err := validateGoalSetParamsRust(params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	// Rust #44944: an active goal can immediately inject an objective or start
	// an idle turn, so it is gated on the retained provider route. Pausing,
	// completing, or clearing a goal must stay possible after policy changes.
	resultingGoalStatus := GoalActive
	if params.Status != nil {
		resultingGoalStatus = *params.Status
	} else if status, ok := r.existingGoalStatus(params.ThreadID); ok {
		resultingGoalStatus = status
	}
	if resultingGoalStatus == GoalActive {
		if err := r.checkThreadModelProviderForID(params.ThreadID); err != nil {
			return nil, err
		}
	}
	if r != nil && r.services.StateRuntime != nil && r.services.ThreadRouter != nil && r.services.ThreadRouter.store != nil {
		response, existing, record, err := r.setStateThreadGoal(params)
		if err != nil {
			return nil, err
		}
		r.emitGoalNotification(connectionID, NotificationThreadGoalUpdated, &GoalUpdatedNotification{ThreadID: response.Goal.ThreadID, Goal: response.Goal})
		r.emitGoalAnalyticsEvent(context.Background(), connectionID, record, &response.Goal, goalAnalyticsEventKindForSet(existing, &response.Goal), nil)
		r.recordGoalSetMetrics(existing, &response.Goal)
		return response, nil
	}
	if r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil {
		var existingGoal *Goal
		if current, getErr := r.requireThreadExtras().GetGoal(&GoalGetParams{ThreadID: params.ThreadID}); getErr == nil && current != nil {
			existingGoal = current.Goal
		}
		response, err := r.requireThreadExtras().SetGoal(params)
		if err == nil && response != nil {
			r.emitGoalNotification(connectionID, NotificationThreadGoalUpdated, &GoalUpdatedNotification{ThreadID: response.Goal.ThreadID, Goal: response.Goal})
			r.recordGoalSetMetrics(existingGoal, &response.Goal)
		}
		return response, err
	}
	if _, ok := r.ephemeralThreadRecord(session.ThreadID(params.ThreadID), false); ok {
		return nil, jsonRPCInvalidRequest(fmt.Sprintf("ephemeral thread does not support goals: %s", params.ThreadID))
	}
	record, err := r.threadRecord(session.ThreadID(params.ThreadID), true, true)
	if err != nil {
		if errors.Is(err, session.ErrThreadNotFound) || strings.Contains(err.Error(), "thread not found") {
			return nil, jsonRPCInvalidRequest(fmt.Sprintf("thread not found: %s", strings.TrimSpace(params.ThreadID)))
		}
		return nil, err
	}
	existing, found, err := goalFromRecord(record)
	if err != nil {
		return nil, err
	}
	if !found {
		existing = nil
	}
	now := time.Now().UTC().Unix()
	if r.services.ThreadRouter.now != nil {
		now = r.services.ThreadRouter.now().UTC().Unix()
	}
	goal, err := buildGoalFromSetParams(params, existing, now)
	if err != nil {
		return nil, err
	}
	if record.Metadata.Extra == nil {
		record.Metadata.Extra = map[string]any{}
	}
	record.Metadata.Extra[threadGoalExtraKey] = goalRecordExtra(goal)
	record.UpdatedAt = time.Unix(now, 0).UTC()
	record.RecencyAt = record.UpdatedAt
	if strings.TrimSpace(record.Preview) == "" {
		record.Preview = goal.Objective
	}
	if err := r.runtimeSaveThreadRecord(record); err != nil {
		return nil, err
	}
	r.emitGoalNotification(connectionID, NotificationThreadGoalUpdated, &GoalUpdatedNotification{ThreadID: goal.ThreadID, Goal: goal})
	r.emitGoalAnalyticsEvent(context.Background(), connectionID, record, &goal, goalAnalyticsEventKindForSet(existing, &goal), nil)
	r.recordGoalSetMetrics(existing, &goal)
	r.applyGoalActiveRuntimeEffects(goal.ThreadID, goal)
	return &GoalSetResponse{Goal: goal}, nil
}

func (r *RuntimeRouter) getThreadGoal(params *GoalGetParams) (*GoalGetResponse, error) {
	if !r.goalsFeatureEnabled() {
		return nil, jsonRPCInvalidRequest("goals feature is disabled")
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if r != nil && r.services.StateRuntime != nil && r.services.ThreadRouter != nil && r.services.ThreadRouter.store != nil {
		return r.getStateThreadGoal(params)
	}
	if r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil {
		return r.requireThreadExtras().GetGoal(params)
	}
	if _, ok := r.ephemeralThreadRecord(session.ThreadID(params.ThreadID), false); ok {
		return nil, jsonRPCInvalidRequest(fmt.Sprintf("ephemeral thread does not support goals: %s", params.ThreadID))
	}
	record, err := r.threadRecord(session.ThreadID(params.ThreadID), true, true)
	if err != nil {
		if errors.Is(err, session.ErrThreadNotFound) || strings.Contains(err.Error(), "thread not found") {
			return nil, jsonRPCInvalidRequest(fmt.Sprintf("thread not found: %s", strings.TrimSpace(params.ThreadID)))
		}
		return nil, err
	}
	goal, found, err := goalFromRecord(record)
	if err != nil {
		return nil, err
	}
	if !found || goal == nil {
		return &GoalGetResponse{}, nil
	}
	cloned := cloneGoal(*goal)
	return &GoalGetResponse{Goal: &cloned}, nil
}

func (r *RuntimeRouter) clearThreadGoal(params *GoalClearParams, connectionID string) (*GoalClearResponse, error) {
	if !r.goalsFeatureEnabled() {
		return nil, jsonRPCInvalidRequest("goals feature is disabled")
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if r != nil && r.services.StateRuntime != nil && r.services.ThreadRouter != nil && r.services.ThreadRouter.store != nil {
		response, deleted, record, err := r.clearStateThreadGoal(params)
		if err != nil {
			return nil, err
		}
		if response.Cleared {
			r.emitGoalNotification(connectionID, NotificationThreadGoalCleared, &GoalClearedNotification{ThreadID: strings.TrimSpace(params.ThreadID)})
			r.emitGoalAnalyticsEvent(context.Background(), connectionID, record, deleted, telemetry.GoalEventKindCleared, nil)
		}
		return response, nil
	}
	if r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil {
		response, err := r.requireThreadExtras().ClearGoal(params)
		if err == nil && response != nil && response.Cleared {
			r.emitGoalNotification(connectionID, NotificationThreadGoalCleared, &GoalClearedNotification{ThreadID: strings.TrimSpace(params.ThreadID)})
		}
		return response, err
	}
	if _, ok := r.ephemeralThreadRecord(session.ThreadID(params.ThreadID), false); ok {
		return nil, jsonRPCInvalidRequest(fmt.Sprintf("ephemeral thread does not support goals: %s", params.ThreadID))
	}
	record, err := r.threadRecord(session.ThreadID(params.ThreadID), true, true)
	if err != nil {
		if errors.Is(err, session.ErrThreadNotFound) || strings.Contains(err.Error(), "thread not found") {
			return nil, jsonRPCInvalidRequest(fmt.Sprintf("thread not found: %s", strings.TrimSpace(params.ThreadID)))
		}
		return nil, err
	}
	goal, found, err := goalFromRecord(record)
	if err != nil {
		return nil, err
	}
	cleared := false
	if record.Metadata.Extra != nil {
		if _, ok := record.Metadata.Extra[threadGoalExtraKey]; ok {
			delete(record.Metadata.Extra, threadGoalExtraKey)
			cleared = true
		}
	}
	if cleared {
		now := time.Now().UTC()
		if r.services.ThreadRouter.now != nil {
			now = r.services.ThreadRouter.now().UTC()
		}
		record.UpdatedAt = now
		record.RecencyAt = now
		if err := r.runtimeSaveThreadRecord(record); err != nil {
			return nil, err
		}
		r.emitGoalNotification(connectionID, NotificationThreadGoalCleared, &GoalClearedNotification{ThreadID: strings.TrimSpace(params.ThreadID)})
		if found && goal != nil {
			r.emitGoalAnalyticsEvent(context.Background(), connectionID, record, goal, telemetry.GoalEventKindCleared, nil)
		}
	}
	return &GoalClearResponse{Cleared: cleared}, nil
}

// goalsFeatureEnabled mirrors Rust's Feature::Goals gate on the thread/goal/*
// protocol methods. The goals feature defaults to enabled, so a missing config
// or an unknown key resolves through the feature registry default.
func (r *RuntimeRouter) goalsFeatureEnabled() bool {
	if r == nil || r.services.Config == nil {
		return features.Enabled(nil, "goals")
	}
	read, err := r.services.Config.Read(&config.ConfigReadParams{})
	if err != nil || read == nil || read.Config == nil {
		return features.Enabled(nil, "goals")
	}
	return features.Enabled((&config.Config{Values: read.Config}).FeatureSettings(), "goals")
}

// SetDeferredGoalNotifications switches the router into the stream-transport
// ordering mode used by the stdio/socket JSON-line servers: thread/goal/*
// notifications emitted by the set/clear handlers are buffered per connection
// and flushed by the transport immediately after the response, mirroring
// Rust's send_response-before-emit ordering. Direct Handle consumers (in-process
// TUI, unit tests) keep immediate broadcast delivery.
func (r *RuntimeRouter) SetDeferredGoalNotifications(enabled bool) {
	if r == nil {
		return
	}
	r.deferredGoalMode.Store(enabled)
}

// emitGoalNotification delivers a goal notification immediately (default mode)
// or buffers it for the stream transport to flush after the response
// (deferred mode). It preserves the same memory/analytics/opt-out checks as
// notify.
func (r *RuntimeRouter) emitGoalNotification(connectionID string, method NotificationMethod, params any) {
	if r == nil {
		return
	}
	if !r.deferredGoalMode.Load() {
		r.notify(method, params)
		return
	}
	if r.isInternalMemoryNotification(params) {
		return
	}
	r.handleNotificationAnalytics(method, params)
	if r.notificationMethodOptedOut(method) {
		return
	}
	key := normalizeConnectionID(connectionID)
	if key == "" {
		key = "stdio"
	}
	notification := NewNotification(method, params)
	r.pendingGoalMu.Lock()
	defer r.pendingGoalMu.Unlock()
	if r.pendingGoalByConn == nil {
		r.pendingGoalByConn = map[string][]*Notification{}
	}
	r.pendingGoalByConn[key] = append(r.pendingGoalByConn[key], notification)
}

// FlushDeferredGoalNotifications returns and clears the buffered goal
// notifications for a connection. The stream transport calls it immediately
// after writing a response so notifications follow their response (Rust order).
func (r *RuntimeRouter) FlushDeferredGoalNotifications(connectionID string) []*Notification {
	if r == nil {
		return nil
	}
	key := normalizeConnectionID(connectionID)
	if key == "" {
		key = "stdio"
	}
	r.pendingGoalMu.Lock()
	defer r.pendingGoalMu.Unlock()
	pending := r.pendingGoalByConn[key]
	delete(r.pendingGoalByConn, key)
	return pending
}

// validateGoalSetParamsRust mirrors the Rust goal service validation messages
// and error codes (invalid_request / -32600) for objective and budget errors,
// before the shared params.Validate() handles the remaining structural checks.
func validateGoalSetParamsRust(params *GoalSetParams) error {
	if params == nil || strings.TrimSpace(params.ThreadID) == "" {
		return nil
	}
	if params.Objective != nil {
		objective := strings.TrimSpace(*params.Objective)
		if objective == "" {
			return jsonRPCInvalidRequest("goal objective must not be empty")
		}
		if len([]rune(objective)) > 4000 {
			return jsonRPCInvalidRequest("goal objective must be at most 4000 characters")
		}
	}
	if (params.TokenBudgetSet || params.TokenBudget != nil) && params.TokenBudget != nil && *params.TokenBudget <= 0 {
		return jsonRPCInvalidRequest("goal budgets must be positive when provided")
	}
	return nil
}

func (r *RuntimeRouter) dispatchRealtime(request *Request) (any, error) {
	switch request.Method {
	case MethodThreadRealtimeStart:
		var params realtime.StartParams
		if err := request.DecodeParams(&params); err != nil {
			return nil, err
		}
		if err := r.ensureRealtimeThread(params.ThreadID); err != nil {
			return nil, err
		}
		r.startRealtimeConversationAsync(params)
		return &realtime.StartResponse{}, nil
	case MethodThreadRealtimeAppendAudio:
		var params realtime.AppendAudioParams
		if err := request.DecodeParams(&params); err != nil {
			return nil, err
		}
		if err := r.ensureRealtimeThread(params.ThreadID); err != nil {
			return nil, err
		}
		if err := params.Validate(); err != nil {
			return nil, err
		}
		r.appendRealtimeAudioAsync(params)
		return &realtime.AppendAudioResponse{}, nil
	case MethodThreadRealtimeAppendText:
		var params realtime.AppendTextParams
		if err := request.DecodeParams(&params); err != nil {
			return nil, err
		}
		if err := r.ensureRealtimeThread(params.ThreadID); err != nil {
			return nil, err
		}
		if err := params.Validate(); err != nil {
			return nil, err
		}
		r.appendRealtimeTextAsync(params)
		return &realtime.AppendTextResponse{}, nil
	case MethodThreadRealtimeAppendSpeech:
		var params realtime.AppendSpeechParams
		if err := request.DecodeParams(&params); err != nil {
			return nil, err
		}
		if err := r.ensureRealtimeThread(params.ThreadID); err != nil {
			return nil, err
		}
		if err := params.Validate(); err != nil {
			return nil, err
		}
		r.appendRealtimeSpeechAsync(params)
		return &realtime.AppendSpeechResponse{}, nil
	case MethodThreadRealtimeStop:
		var params realtime.StopParams
		if err := request.DecodeParams(&params); err != nil {
			return nil, err
		}
		if err := r.ensureRealtimeThread(params.ThreadID); err != nil {
			return nil, err
		}
		if err := params.Validate(); err != nil {
			return nil, err
		}
		r.stopRealtimeConversationAsync(params)
		return &realtime.StopResponse{}, nil
	case MethodThreadRealtimeListVoices:
		var params realtime.ListVoicesParams
		if err := request.DecodeParams(&params); err != nil {
			return nil, err
		}
		return r.requireRealtime().ListVoices(&params), nil
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnknownMethod, request.Method)
	}
}

func (r *RuntimeRouter) ensureRealtimeThread(threadID string) error {
	if r == nil || strings.TrimSpace(threadID) == "" {
		return nil
	}
	if r.services.ThreadRouter != nil && r.services.ThreadRouter.store != nil {
		if _, err := r.threadRecord(session.ThreadID(threadID), false, false); err != nil {
			return err
		}
	}
	return nil
}

func (r *RuntimeRouter) notifyRealtime(notifications []realtime.Notification) {
	for i := range notifications {
		notification := &notifications[i]
		if notification.Method == realtime.NotificationClosed {
			r.recordVoiceSessionDuration(notification)
		}
		method, params, ok := realtimeNotificationPayload(notification)
		if !ok {
			continue
		}
		r.notify(method, params)
	}
}

func realtimeNotificationPayload(notification *realtime.Notification) (NotificationMethod, any, bool) {
	if notification == nil {
		return "", nil, false
	}
	switch notification.Method {
	case realtime.NotificationStarted:
		params, ok := notification.Params.(realtime.StartedNotification)
		if !ok {
			if ptr, ok := notification.Params.(*realtime.StartedNotification); ok && ptr != nil {
				params = *ptr
				ok = true
			}
		}
		if !ok {
			return "", nil, false
		}
		return NotificationThreadRealtimeStarted, &ThreadRealtimeStartedNotification{
			ThreadID:          params.ThreadID,
			RealtimeSessionID: cloneString(params.RealtimeSessionID),
			Version:           string(params.Version),
		}, true
	case realtime.NotificationItemAdded:
		params, ok := notification.Params.(realtime.ItemAddedNotification)
		if !ok {
			if ptr, ok := notification.Params.(*realtime.ItemAddedNotification); ok && ptr != nil {
				params = *ptr
				ok = true
			}
		}
		if !ok {
			return "", nil, false
		}
		return NotificationThreadRealtimeItemAdded, &ThreadRealtimeItemAddedNotification{ThreadID: params.ThreadID, Item: cloneAnyMap(params.Item)}, true
	case realtime.NotificationItemStarted:
		params, ok := notification.Params.(realtime.ItemStartedNotification)
		if !ok {
			if ptr, ok := notification.Params.(*realtime.ItemStartedNotification); ok && ptr != nil {
				params = *ptr
				ok = true
			}
		}
		if !ok {
			return "", nil, false
		}
		return NotificationThreadRealtimeItemStarted, &ThreadRealtimeItemStartedNotification{ThreadID: params.ThreadID, Item: params.Item}, true
	case realtime.NotificationItemCompleted:
		params, ok := notification.Params.(realtime.ItemCompletedNotification)
		if !ok {
			if ptr, ok := notification.Params.(*realtime.ItemCompletedNotification); ok && ptr != nil {
				params = *ptr
				ok = true
			}
		}
		if !ok {
			return "", nil, false
		}
		return NotificationThreadRealtimeItemCompleted, &ThreadRealtimeItemCompletedNotification{ThreadID: params.ThreadID, Item: params.Item}, true
	case realtime.NotificationItemTranscriptDelta:
		params, ok := notification.Params.(realtime.ItemTranscriptDeltaNotification)
		if !ok {
			if ptr, ok := notification.Params.(*realtime.ItemTranscriptDeltaNotification); ok && ptr != nil {
				params = *ptr
				ok = true
			}
		}
		if !ok {
			return "", nil, false
		}
		return NotificationThreadRealtimeItemTranscriptDelta, &ThreadRealtimeItemTranscriptDeltaNotification{
			ThreadID: params.ThreadID,
			ItemID:   params.ItemID,
			Delta:    params.Delta,
		}, true
	case realtime.NotificationTranscriptDelta:
		params, ok := notification.Params.(realtime.TranscriptDeltaNotification)
		if !ok {
			if ptr, ok := notification.Params.(*realtime.TranscriptDeltaNotification); ok && ptr != nil {
				params = *ptr
				ok = true
			}
		}
		if !ok {
			return "", nil, false
		}
		return NotificationThreadRealtimeTranscriptDelta, &ThreadRealtimeTranscriptDeltaNotification{
			ThreadID: params.ThreadID,
			Role:     params.Role,
			Delta:    params.Delta,
		}, true
	case realtime.NotificationTranscriptDone:
		params, ok := notification.Params.(realtime.TranscriptDoneNotification)
		if !ok {
			if ptr, ok := notification.Params.(*realtime.TranscriptDoneNotification); ok && ptr != nil {
				params = *ptr
				ok = true
			}
		}
		if !ok {
			return "", nil, false
		}
		return NotificationThreadRealtimeTranscriptDone, &ThreadRealtimeTranscriptDoneNotification{
			ThreadID: params.ThreadID,
			Role:     params.Role,
			Text:     params.Text,
		}, true
	case realtime.NotificationOutputAudioDelta:
		params, ok := notification.Params.(realtime.OutputAudioDeltaNotification)
		if !ok {
			if ptr, ok := notification.Params.(*realtime.OutputAudioDeltaNotification); ok && ptr != nil {
				params = *ptr
				ok = true
			}
		}
		if !ok {
			return "", nil, false
		}
		return NotificationThreadRealtimeOutputAudioDelta, &ThreadRealtimeOutputAudioDeltaNotification{
			ThreadID: params.ThreadID,
			Audio:    appserverRealtimeAudioChunk(&params.Audio),
		}, true
	case realtime.NotificationSDP:
		params, ok := notification.Params.(realtime.SDPNotification)
		if !ok {
			if ptr, ok := notification.Params.(*realtime.SDPNotification); ok && ptr != nil {
				params = *ptr
				ok = true
			}
		}
		if !ok {
			return "", nil, false
		}
		return NotificationThreadRealtimeSDP, &ThreadRealtimeSDPNotification{ThreadID: params.ThreadID, SDP: params.SDP}, true
	case realtime.NotificationError:
		params, ok := notification.Params.(realtime.ErrorNotification)
		if !ok {
			if ptr, ok := notification.Params.(*realtime.ErrorNotification); ok && ptr != nil {
				params = *ptr
				ok = true
			}
		}
		if !ok {
			return "", nil, false
		}
		return NotificationThreadRealtimeError, &ThreadRealtimeErrorNotification{ThreadID: params.ThreadID, Message: params.Message}, true
	case realtime.NotificationClosed:
		params, ok := notification.Params.(realtime.ClosedNotification)
		if !ok {
			if ptr, ok := notification.Params.(*realtime.ClosedNotification); ok && ptr != nil {
				params = *ptr
				ok = true
			}
		}
		if !ok {
			return "", nil, false
		}
		return NotificationThreadRealtimeClosed, &ThreadRealtimeClosedNotification{ThreadID: params.ThreadID, Reason: cloneString(params.Reason)}, true
	default:
		return "", nil, false
	}
}

func appserverRealtimeAudioChunk(chunk *realtime.AudioChunk) ThreadRealtimeAudioChunk {
	if chunk == nil {
		return ThreadRealtimeAudioChunk{}
	}
	return ThreadRealtimeAudioChunk{
		Data:              chunk.Data,
		ItemID:            cloneString(chunk.ItemID),
		NumChannels:       chunk.NumChannels,
		SampleRate:        chunk.SampleRate,
		SamplesPerChannel: cloneUint32(chunk.SamplesPerChannel),
	}
}

func cloneUint32(value *uint32) *uint32 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func (r *RuntimeRouter) handleExperimentalFeatureList(request *Request) (*features.FeatureListResponse, error) {
	var params features.FeatureListParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	settings, err := r.experimentalFeatureSettingsForList(&params)
	if err != nil {
		return nil, err
	}
	return r.requireFeatures().ListWithSettings(&params, settings)
}

func (r *RuntimeRouter) handleExperimentalFeatureSet(request *Request) (*features.FeatureEnablementSetResponse, error) {
	var params features.FeatureEnablementSetParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	response, err := r.requireFeatures().SetEnablement(&params)
	if err != nil {
		return nil, err
	}
	if r.services.Config != nil && response != nil {
		r.services.Config.SetFeatureEnablementDefaults(response.Enablement)
	}
	if r.services.Models != nil && r.services.Config != nil {
		// Rust #44392: apply the API-key model discovery policy after feature
		// enablement changes so live discovery requests honor user overrides.
		if read, err := r.services.Config.Read(&config.ConfigReadParams{}); err == nil && read != nil {
			settings := (&config.Config{Values: read.Config}).FeatureSettings()
			r.services.Models.SetAPIKeyModelDiscoveryEnabled(features.Enabled(settings, "api_key_model_discovery"))
		}
	}
	// Rust experimental_feature_enablement_set: clear the config-derived caches
	// and, when anything was set, reload the user config for the loaded threads.
	r.clearConfigDerivedCaches()
	if response != nil && len(response.Enablement) > 0 {
		r.reloadUserConfigForLoadedThreads()
	}
	return response, nil
}

func (r *RuntimeRouter) experimentalFeatureSettingsForList(params *features.FeatureListParams) (map[string]bool, error) {
	if r == nil || r.services.Config == nil {
		return nil, nil
	}
	readParams := &config.ConfigReadParams{}
	if params != nil && params.ThreadID != nil && strings.TrimSpace(*params.ThreadID) != "" {
		record, err := r.threadRecord(session.ThreadID(strings.TrimSpace(*params.ThreadID)), true, false)
		if err != nil {
			return nil, jsonRPCInvalidRequest(err.Error())
		}
		if cwd := strings.TrimSpace(record.Metadata.CWD); cwd != "" {
			readParams.CWD = &cwd
		}
	}
	read, err := r.services.Config.Read(readParams)
	if err != nil {
		return nil, err
	}
	if read == nil {
		return nil, nil
	}
	return (&config.Config{Values: read.Config}).FeatureSettings(), nil
}

func (r *RuntimeRouter) handleAppList(request *Request) (*apps.AppListResponse, error) {
	var params apps.AppListParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	service := r.requireApps()
	configValues, err := r.appListConfigValues(&params)
	if err != nil {
		return nil, err
	}
	service.SetConfigValues(configValues)
	if !features.Enabled((&config.Config{Values: configValues}).FeatureSettings(), "apps") {
		service.SetPluginConnectors(nil)
		service.SetDirectoryProviderWithKey(nil, "")
		service.SetAccessibleProviderWithKey(nil, "")
		return &apps.AppListResponse{Data: []apps.AppEntry{}, Apps: []apps.AppEntry{}, AllApps: []apps.AppEntry{}}, nil
	}
	if r.services.Plugins != nil {
		service.SetPluginConnectors(appPluginConnectorsFromCapabilities(r.services.Plugins.EnabledCapabilities()))
	} else {
		service.SetPluginConnectors(nil)
	}
	r.configureAppDirectoryProvider(service, configValues)
	r.configureAppAccessibleProvider(service)
	var lastNotified []apps.AppEntry
	if params.ForceRefetch {
		lastNotified = service.CachedListForNotification()
		if len(lastNotified) > 0 {
			r.notify(NotificationAppListUpdated, &apps.AppListUpdatedNotification{Data: lastNotified})
		}
	}
	response, err := service.List(&params)
	if err != nil {
		return nil, err
	}
	if response != nil {
		data := appListUpdatedNotificationData(response)
		if !reflect.DeepEqual(lastNotified, data) {
			r.notify(NotificationAppListUpdated, &apps.AppListUpdatedNotification{Data: data})
		}
	}
	return response, nil
}

func (r *RuntimeRouter) handleAppRead(request *Request) (*apps.AppsReadResponse, error) {
	var params apps.AppsReadParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if len(params.AppIDs) > 100 {
		return nil, &appReadInvalidParamsError{message: "app/read accepts at most 100 appIds"}
	}
	configValues, err := r.appListConfigValues(&apps.AppListParams{ThreadID: params.ThreadID})
	if err != nil {
		return nil, err
	}
	if !features.Enabled((&config.Config{Values: configValues}).FeatureSettings(), "apps") {
		return &apps.AppsReadResponse{Apps: []apps.ConnectorMetadata{}, MissingAppIDs: dedupeAppReadIDs(params.AppIDs)}, nil
	}
	service := r.requireApps()
	r.configureAppMetadataProvider(service, configValues)
	response, err := service.Read(&params)
	if err != nil {
		if errors.Is(err, apps.ErrInvalidAppRequest) {
			return nil, fmt.Errorf("%w: %s", ErrInvalidRequest, strings.TrimPrefix(err.Error(), apps.ErrInvalidAppRequest.Error()+": "))
		}
		return nil, fmt.Errorf("failed to read app metadata: %w", err)
	}
	return response, nil
}

func (r *RuntimeRouter) handleAppInstalled(request *Request) (*apps.AppsInstalledResponse, error) {
	var params apps.AppsInstalledParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	configValues, err := r.appListConfigValues(&apps.AppListParams{ThreadID: params.ThreadID})
	if err != nil {
		return nil, err
	}
	if !features.Enabled((&config.Config{Values: configValues}).FeatureSettings(), "apps") {
		return &apps.AppsInstalledResponse{Apps: []apps.InstalledApp{}}, nil
	}
	service := r.requireApps()
	service.SetConfigValues(configValues)
	r.configureAppAccessibleProvider(service)
	response, err := service.Installed(&params)
	usedFallback := false
	if err != nil && params.ForceRefresh {
		// Rust #43039: a failed forced refresh keeps the last working catalog
		// instead of publishing an empty one.
		fallback := params
		fallback.ForceRefresh = false
		if cached, cacheErr := service.Installed(&fallback); cacheErr == nil {
			response, err, usedFallback = cached, nil, true
		}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read installed app runtime state: %w", err)
	}
	// Rust #43039: with a thread, callable reflects the refreshed snapshot's
	// model-visible codex_apps tools eligible under the runtime's generic MCP
	// policy, not just effective app enablement. A fallback (failed refresh)
	// keeps the previous snapshot's callable state.
	if !usedFallback && len(response.Apps) > 0 && r.services.MCP != nil {
		threadID := ""
		if params.ThreadID != nil {
			threadID = strings.TrimSpace(*params.ThreadID)
		}
		cfg := &config.Config{Values: configValues}
		tools, connectors := r.mcpRuntimeInputsForServiceWithRequirements(threadID, cfg, r.services.MCP, nil, nil)
		callable := mcp.CodexAppsModelVisibleConnectorIDs(tools, connectors)
		for i := range response.Apps {
			app := &response.Apps[i]
			app.Callable = app.Enabled && callable[app.ID]
		}
	}
	return response, nil
}

func (r *RuntimeRouter) configureAppMetadataProvider(service *apps.AppService, values map[string]any) {
	if r == nil || service == nil || r.services.Config == nil {
		return
	}
	cfg := &config.Config{Values: values}
	codexHome := strings.TrimSpace(r.services.Config.CodexHome())
	if codexHome == "" {
		service.SetMetadataProvider(nil)
		return
	}
	resolved, err := r.resolveAuthWithLoginRestrictions(codexHome)
	if err != nil || resolved == nil {
		service.SetMetadataProvider(nil)
		return
	}
	token := appDirectoryAuthToken(&resolved.Auth)
	accountID := auth.AccountIDFromAuthForRestrictions(&resolved.Auth)
	if token == "" || accountID == "" {
		service.SetMetadataProvider(nil)
		return
	}
	productSKU := strings.TrimSpace(stringFromMap(values, "apps_mcp_product_sku"))
	provider := apps.NewChatGPTMetadataProvider(&apps.ChatGPTMetadataProviderOptions{
		BaseURL: cfg.ChatGPTBaseURL(),
		Headers: http.Header{
			"Authorization":      []string{"Bearer " + token},
			"ChatGPT-Account-ID": []string{accountID},
		},
		ProductSKU: productSKU,
		HTTPClient: r.httpClientForConfig(cfg),
	})
	service.SetMetadataProvider(provider)
}

type appReadInvalidParamsError struct {
	message string
}

func (e *appReadInvalidParamsError) Error() string {
	return e.message
}

func (e *appReadInvalidParamsError) Unwrap() error {
	return ErrInvalidRequest
}

func dedupeAppReadIDs(ids []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func (r *RuntimeRouter) appListConfigValues(params *apps.AppListParams) (map[string]any, error) {
	if r == nil || r.services.Config == nil {
		return nil, nil
	}
	if params != nil && params.ThreadID != nil && strings.TrimSpace(*params.ThreadID) != "" {
		threadID := strings.TrimSpace(*params.ThreadID)
		if _, err := r.threadRecord(session.ThreadID(threadID), true, false); err != nil {
			return nil, jsonRPCInvalidRequest(err.Error())
		}
		cfg := r.effectiveMCPConfigForThread(threadID)
		if cfg == nil {
			return nil, nil
		}
		return cloneAnyMapForRouter(cfg.Values), nil
	}
	read, err := r.services.Config.Read(&config.ConfigReadParams{})
	if err != nil {
		return nil, err
	}
	if read == nil {
		return nil, nil
	}
	return read.Config, nil
}

func appListUpdatedNotificationData(response *apps.AppListResponse) []apps.AppEntry {
	if response == nil {
		return nil
	}
	if response.AllApps != nil {
		return response.AllApps
	}
	if response.Apps != nil {
		return response.Apps
	}
	return response.Data
}

func appPluginConnectorsFromCapabilities(capabilities []plugin.CapabilitySummary) []apps.PluginConnector {
	out := make([]apps.PluginConnector, 0)
	seen := map[string]bool{}
	appendConnector := func(connector apps.PluginConnector, displayName string) {
		connector.ID = strings.TrimSpace(connector.ID)
		if connector.ID == "" {
			return
		}
		connector.PluginDisplayName = displayName
		key := connector.ID + "\x00" + displayName
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, connector)
	}
	for i := range capabilities {
		displayName := firstNonEmpty(capabilities[i].DisplayName, capabilities[i].Name, capabilities[i].ConfigName, capabilities[i].RemotePluginID)
		for _, app := range capabilities[i].Apps {
			appendConnector(appPluginConnectorFromSummary(app), displayName)
		}
		for _, template := range capabilities[i].AppTemplates {
			for _, connector := range appPluginConnectorsFromTemplate(template) {
				appendConnector(connector, displayName)
			}
		}
		for _, connectorID := range capabilities[i].AppConnectors {
			appendConnector(apps.PluginConnector{ID: connectorID}, displayName)
		}
	}
	return out
}

func appPluginConnectorFromSummary(app plugin.AppSummary) apps.PluginConnector {
	return apps.PluginConnector{
		ID:          app.ID,
		Name:        firstNonEmpty(app.DisplayName, app.Name, app.ID),
		Description: cloneStringPtrAppserver(app.Description),
		InstallURL:  cloneStringPtrAppserver(app.InstallURL),
	}
}

func appPluginConnectorsFromTemplate(template plugin.AppTemplateSummary) []apps.PluginConnector {
	name := firstNonEmpty(template.DisplayName, template.Name, template.TemplateID, template.ID)
	base := apps.PluginConnector{
		Name:        name,
		Description: cloneStringPtrAppserver(template.Description),
		LogoURL:     cloneStringPtrAppserver(template.LogoURL),
		LogoURLDark: cloneStringPtrAppserver(template.LogoURLDark),
	}
	out := make([]apps.PluginConnector, 0, 1+len(template.MaterializedAppIDs))
	if template.CanonicalConnectorID != nil {
		connector := base
		connector.ID = *template.CanonicalConnectorID
		out = append(out, connector)
	}
	for _, id := range template.MaterializedAppIDs {
		connector := base
		connector.ID = id
		out = append(out, connector)
	}
	return out
}

func (r *RuntimeRouter) configureAppDirectoryProvider(service *apps.AppService, values map[string]any) {
	if r == nil || service == nil || r.services.Config == nil {
		return
	}
	cfg := &config.Config{Values: values}
	codexHome := strings.TrimSpace(r.services.Config.CodexHome())
	if codexHome == "" {
		service.SetDirectoryProviderWithKey(nil, "")
		return
	}
	resolved, err := r.resolveAuthWithLoginRestrictions(codexHome)
	if err != nil || resolved == nil {
		service.SetDirectoryProviderWithKey(nil, "")
		return
	}
	token := appDirectoryAuthToken(&resolved.Auth)
	if token == "" {
		service.SetDirectoryProviderWithKey(nil, "")
		return
	}
	accountID := auth.AccountIDFromAuthForRestrictions(&resolved.Auth)
	userID := appDirectoryChatGPTUserID(&resolved.Auth)
	baseURL := cfg.ChatGPTBaseURL()
	key := strings.Join([]string{baseURL, accountID, userID, fmt.Sprintf("workspace=%t", accountID != "")}, "|")
	provider := apps.NewChatGPTDirectoryProvider(&apps.ChatGPTDirectoryProviderOptions{
		BaseURL: baseURL,
		Headers: http.Header{
			"Authorization": []string{"Bearer " + token},
		},
		HTTPClient:         r.httpClientForConfig(cfg),
		IsWorkspaceAccount: accountID != "",
	})
	service.SetDirectoryProviderWithKey(provider, key)
}

func (r *RuntimeRouter) configureAppAccessibleProvider(service *apps.AppService) {
	if r == nil || service == nil || r.services.MCP == nil {
		return
	}
	service.SetAccessibleProviderWithKey(&mcpAccessibleAppsProvider{service: r.services.MCP}, "mcp:codex_apps")
}

func appDirectoryAuthToken(snapshot *auth.AuthDotJSON) string {
	if snapshot == nil {
		return ""
	}
	switch snapshot.Mode() {
	case "chatgpt", "chatgptAuthTokens":
		return strings.TrimSpace(stringFromMap(snapshot.Tokens, "access_token"))
	case "personal-access-token":
		return strings.TrimSpace(snapshot.PersonalAccessToken)
	case "agent-identity":
		if token, ok := snapshot.AgentIdentity.(string); ok {
			return strings.TrimSpace(token)
		}
	}
	return ""
}

func appDirectoryChatGPTUserID(snapshot *auth.AuthDotJSON) string {
	if snapshot == nil {
		return ""
	}
	userID := strings.TrimSpace(stringFromMap(snapshot.Tokens, "chatgpt_user_id"))
	if userID != "" {
		return userID
	}
	return ""
}

func (r *RuntimeRouter) handleGetAuthStatus(request *Request) (*AuthStatusResponse, error) {
	var params AuthStatusParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if r.services.Misc != nil && r.services.Misc.HasAuthStatus() {
		return r.services.Misc.AuthStatus(&params), nil
	}
	return r.currentAuthStatus(&params)
}

func (r *RuntimeRouter) handleGetConversationSummary(request *Request) (*ConversationSummaryResponse, error) {
	var params ConversationSummaryParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.RolloutPath) != "" {
		return r.conversationSummaryFromRolloutPath(params.RolloutPath)
	}
	conversationID := params.LookupConversationID()
	if r.services.ThreadRouter != nil && r.services.ThreadRouter.store != nil && conversationID != "" {
		record, err := r.threadRecord(session.ThreadID(conversationID), true, true)
		if err != nil {
			return nil, err
		}
		path := r.services.ThreadRouter.threadRolloutPath(record)
		if path == "" {
			path = strings.TrimSpace(params.RolloutPath)
		}
		summary := conversationSummaryFromRecord(record, 4000)
		return &ConversationSummaryResponse{
			Summary:     summary,
			SummaryData: conversationSummaryDataFromRecord(record, path, 4000),
		}, nil
	}
	return r.requireMisc().ConversationSummary(&params)
}

func (r *RuntimeRouter) conversationSummaryFromRolloutPath(rawPath string) (*ConversationSummaryResponse, error) {
	if r == nil || r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil {
		return nil, fmt.Errorf("%w: rollout path queries are only supported with the local thread store", ErrInvalidMiscRequest)
	}
	path, err := r.resolveConversationSummaryRolloutPath(rawPath)
	if err != nil {
		return nil, err
	}
	record, err := rollout.RecordFromPath(path, false)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid rolloutPath: %w", ErrInvalidMiscRequest, err)
	}
	if err := r.runtimeSaveThreadRecord(record); err != nil {
		return nil, err
	}
	summary := conversationSummaryFromRecord(record, 4000)
	return &ConversationSummaryResponse{
		Summary:     summary,
		SummaryData: conversationSummaryDataFromRecord(record, path, 4000),
	}, nil
}

func (r *RuntimeRouter) resolveConversationSummaryRolloutPath(rawPath string) (string, error) {
	path := strings.TrimSpace(rawPath)
	if path == "" {
		return "", fmt.Errorf("%w: rolloutPath is required", ErrInvalidMiscRequest)
	}
	if !filepath.IsAbs(path) {
		codexHome := ""
		if r != nil && r.services.ThreadRouter != nil {
			codexHome = codexHomeFromSessionStore(r.services.ThreadRouter.store)
		}
		if strings.TrimSpace(codexHome) == "" {
			return "", fmt.Errorf("%w: rollout path queries are only supported with the local thread store", ErrInvalidMiscRequest)
		}
		path = filepath.Join(codexHome, path)
	}
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	if canonical, err := filepath.EvalSymlinks(path); err == nil {
		path = canonical
	}
	return filepath.Clean(path), nil
}

func (r *RuntimeRouter) threadRecord(threadID session.ThreadID, includeArchived bool, includeHistory bool) (*session.Record, error) {
	if r == nil || r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil {
		return nil, fmt.Errorf("%w: %s", session.ErrThreadNotFound, threadID)
	}
	if record, ok := r.ephemeralThreadRecord(threadID, includeHistory); ok {
		return record, nil
	}
	var record *session.Record
	var err error
	if liveThread := r.threads.LiveThread(threadID); liveThread != nil {
		record, err = liveThread.Read(includeArchived, includeHistory)
	} else {
		record, err = r.services.ThreadRouter.store.Read(threadID, includeArchived, includeHistory)
	}
	if err == nil {
		if includeHistory {
			r.attachRolloutTurnSnapshots(record)
		}
		return record, nil
	}
	if !errors.Is(err, session.ErrThreadNotFound) {
		return nil, err
	}
	record, repairErr := r.services.ThreadRouter.readThreadRecordFromRollout(threadID, includeArchived, true)
	if repairErr != nil {
		return nil, repairErr
	}
	if err := r.runtimeSaveThreadRecord(record); err != nil {
		return nil, err
	}
	if !includeHistory {
		record.Items = nil
	}
	return record, nil
}

func (r *RuntimeRouter) ephemeralThreadRecord(threadID session.ThreadID, includeHistory bool) (*session.Record, bool) {
	if r == nil {
		return nil, false
	}
	return r.threads.EphemeralRecord(threadID, includeHistory)
}

func (r *RuntimeRouter) saveEphemeralThreadRecord(record *session.Record) bool {
	return r != nil && r.threads.SaveEphemeralRecord(record)
}

func (r *RuntimeRouter) runtimeSaveThreadRecord(record *session.Record) error {
	if record == nil {
		return fmt.Errorf("%w: record is nil", session.ErrInvalidThreadID)
	}
	if r.saveEphemeralThreadRecord(record) {
		return nil
	}
	if r == nil || r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil {
		return fmt.Errorf("%w: %s", session.ErrThreadNotFound, record.ID)
	}
	if liveThread := r.threads.LiveThread(record.ID); liveThread != nil {
		return liveThread.Save(record)
	}
	return r.services.ThreadRouter.store.Save(record)
}

func (r *RuntimeRouter) runtimeUpdateThreadMetadata(threadID session.ThreadID, patch *session.MetadataPatch, includeArchived bool) (*session.Record, error) {
	if r == nil || r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil {
		return nil, fmt.Errorf("%w: %s", session.ErrThreadNotFound, threadID)
	}
	if liveThread := r.threads.LiveThread(threadID); liveThread != nil {
		return liveThread.UpdateMetadata(patch, includeArchived)
	}
	return r.services.ThreadRouter.store.UpdateMetadata(threadID, patch, includeArchived)
}

func (r *RuntimeRouter) runtimeAppendItem(threadID session.ThreadID, item session.Item) (*session.Record, error) {
	return r.runtimeAppendItems(threadID, []session.Item{item})
}

func (r *RuntimeRouter) runtimeAppendItems(threadID session.ThreadID, items []session.Item) (*session.Record, error) {
	r.markThreadMemoryPollutedOnExternalContext(string(threadID), items)
	if record, ok := r.appendEphemeralThreadItems(threadID, items); ok {
		return record, nil
	}
	if r == nil || r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil {
		return nil, fmt.Errorf("%w: %s", session.ErrThreadNotFound, threadID)
	}
	if liveThread := r.threads.LiveThread(threadID); liveThread != nil {
		return liveThread.AppendItems(items)
	}
	return r.services.ThreadRouter.store.AppendItems(threadID, items)
}

// markThreadMemoryPollutedOnExternalContext mirrors Rust
// mark_thread_memory_mode_polluted_if_external_context (#39791): standalone
// function_call_output items (no call id) are external context; when
// memories.disable_on_external_context is enabled, the thread memory mode is
// marked polluted.
func (r *RuntimeRouter) markThreadMemoryPollutedOnExternalContext(threadID string, items []session.Item) {
	if r == nil || r.services.StateRuntime == nil || len(items) == 0 {
		return
	}
	standalone := false
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item.Type), "function_call_output") && strings.TrimSpace(item.CallID) == "" {
			standalone = true
			break
		}
	}
	if !standalone {
		return
	}
	cfg := r.effectiveMCPConfigForThread(strings.TrimSpace(threadID))
	if cfg == nil || !cfg.Memories().DisableOnExternalContext {
		return
	}
	_, _ = r.services.StateRuntime.MarkThreadMemoryModePolluted(context.Background(), strings.TrimSpace(threadID))
}

func (r *RuntimeRouter) appendEphemeralThreadItems(threadID session.ThreadID, items []session.Item) (*session.Record, bool) {
	if r == nil {
		return nil, false
	}
	return r.threads.AppendEphemeralItems(threadID, items)
}

func runtimeRecordEphemeral(record *session.Record) bool {
	return record != nil && boolFromMap(record.Metadata.Extra, "ephemeral")
}

func cloneRuntimeSessionRecord(record *session.Record) *session.Record {
	if record == nil {
		return nil
	}
	clone := *record
	clone.Metadata = cloneRuntimeSessionMetadata(record.Metadata)
	clone.Items = cloneRuntimeSessionItems(record.Items)
	return &clone
}

func cloneRuntimeSessionMetadata(metadata session.Metadata) session.Metadata {
	clone := metadata
	clone.Git = cloneStringMap(metadata.Git)
	clone.DynamicTools = cloneRawMessages(metadata.DynamicTools)
	clone.SelectedCapabilityRoots = cloneRawMessages(metadata.SelectedCapabilityRoots)
	clone.ContextWindow = cloneRawMessage(metadata.ContextWindow)
	clone.TurnContext = cloneRawMessage(metadata.TurnContext)
	clone.WorldState = cloneRawMessage(metadata.WorldState)
	clone.Extra = cloneAnyMap(metadata.Extra)
	if len(metadata.RolloutTurns) > 0 {
		clone.RolloutTurns = append([]session.TurnSnapshot(nil), metadata.RolloutTurns...)
	}
	return clone
}

func cloneRuntimeSessionItems(items []session.Item) []session.Item {
	if len(items) == 0 {
		return nil
	}
	out := make([]session.Item, len(items))
	for i := range items {
		out[i] = cloneRuntimeSessionItem(items[i])
	}
	return out
}

func cloneRuntimeSessionItem(item session.Item) session.Item {
	clone := item
	if len(item.Content) > 0 {
		clone.Content = make([]session.ContentPart, len(item.Content))
		for i := range item.Content {
			clone.Content[i] = item.Content[i]
			clone.Content[i].Detail = cloneString(item.Content[i].Detail)
		}
	}
	clone.Data = cloneAnyMap(item.Data)
	clone.Raw = cloneRawMessage(item.Raw)
	clone.Metadata = cloneAnyMap(item.Metadata)
	return clone
}

func cloneRawMessage(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), value...)
}

func runtimeSessionItemPreviewText(item *session.Item) string {
	if item == nil {
		return ""
	}
	if text := strings.TrimSpace(item.Text); text != "" {
		return text
	}
	for i := range item.Content {
		if text := strings.TrimSpace(item.Content[i].Text); text != "" {
			return text
		}
	}
	return strings.TrimSpace(item.Name)
}

func (r *RuntimeRouter) attachRolloutTurnSnapshots(record *session.Record) {
	if r == nil || r.services.ThreadRouter == nil {
		return
	}
	r.services.ThreadRouter.attachRolloutTurnSnapshots(record)
}

func (r *RuntimeRouter) handleGitDiffToRemote(request *Request) (*GitDiffToRemoteResponse, error) {
	var params GitDiffToRemoteParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	return r.requireMisc().GitDiffToRemote(&params)
}

func (r *RuntimeRouter) handleFuzzyFileSearch(request *Request) (*FuzzyFileSearchResponse, error) {
	var params FuzzyFileSearchParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	return r.requireMisc().FuzzyFileSearch(nil, &params)
}

func (r *RuntimeRouter) handleFuzzyFileSearchSessionStart(request *Request) (*FuzzyFileSearchSessionStartResponse, error) {
	var params FuzzyFileSearchSessionStartParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	return r.requireMisc().FuzzyFileSearchSessionStart(&params)
}

func (r *RuntimeRouter) handleFuzzyFileSearchSessionUpdate(request *Request) (*FuzzyFileSearchSessionUpdateResponse, error) {
	var params FuzzyFileSearchSessionUpdateParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	response, err := r.requireMisc().FuzzyFileSearchSessionUpdate(nil, &params)
	if err != nil {
		return nil, err
	}
	if response.Notify {
		files := make([]any, 0, len(response.Files))
		for i := range response.Files {
			files = append(files, response.Files[i])
		}
		r.notify(NotificationFuzzyFileSearchSessionUpdated, &FuzzyFileSearchSessionUpdatedNotification{
			SessionID: response.SessionID,
			Query:     response.Query,
			Files:     files,
		})
		r.notify(NotificationFuzzyFileSearchSessionCompleted, &FuzzyFileSearchSessionCompletedNotification{
			SessionID: response.SessionID,
		})
	}
	return response, nil
}

func (r *RuntimeRouter) handleFuzzyFileSearchSessionStop(request *Request) (*FuzzyFileSearchSessionStopResponse, error) {
	var params FuzzyFileSearchSessionStopParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	return r.requireMisc().FuzzyFileSearchSessionStop(&params)
}

func (r *RuntimeRouter) handleHooksList(request *Request) (*HookListResponse, error) {
	var params HookListParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	registry := r.requireHooks().List(&params)
	discovery := r.configureHookDiscovery()
	discovered := discovery.Discover(&params, r.services.DefaultCWD)
	return MergeHookListResponses(registry, discovered), nil
}

func (r *RuntimeRouter) handleSkillsList(request *Request) (*SkillsListResponse, error) {
	var params SkillsListParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if len(params.CWDs) == 0 {
		params.CWDs = []string{r.threadStartDefaultCWD()}
	}
	requestedCWDs := append([]string(nil), params.CWDs...)
	loadParams := params
	if !r.localEnvironmentEnabled() {
		loadParams.CWDs = nil
	}
	response, err := r.requireSkills().List(&loadParams)
	if err != nil {
		return nil, err
	}
	// skills/list is threadless, so no per-thread plugin exclusion applies.
	pluginEntries, pluginErrors, err := r.pluginSkillEntriesAndErrorsForRuntime("")
	if err != nil {
		return nil, err
	}
	pluginCWDs := r.pluginEnabledCWDs(requestedCWDs)
	if len(pluginCWDs) == 0 {
		pluginEntries = nil
		pluginErrors = nil
	}
	pluginEntries, err = r.requireSkills().applyConfigEntries(pluginEntries, nil)
	if err != nil {
		return nil, err
	}
	for i := range pluginEntries {
		pluginEntries[i].Scope = "user"
		pluginEntries[i].ApplicableCWDs = append([]string(nil), pluginCWDs...)
	}
	entries := append(cloneSkills(response.Skills), pluginEntries...)
	entries = dedupeSkillsByPath(entries)
	sort.SliceStable(entries, func(i int, j int) bool {
		if skillScopeRank(entries[i].Scope) != skillScopeRank(entries[j].Scope) {
			return skillScopeRank(entries[i].Scope) < skillScopeRank(entries[j].Scope)
		}
		if entries[i].Name == entries[j].Name {
			return entries[i].Path < entries[j].Path
		}
		return entries[i].Name < entries[j].Name
	})
	errors := append(cloneSkillErrors(response.Errors), pluginErrors...)
	return skillsListResponse(entries, errors, requestedCWDs, nil), nil
}

func (r *RuntimeRouter) pluginEnabledCWDs(cwds []string) []string {
	if r == nil || r.services.WorkspaceCodexPluginsEnabled != nil && !*r.services.WorkspaceCodexPluginsEnabled {
		return nil
	}
	out := make([]string, 0, len(cwds))
	for _, cwd := range cwds {
		if r.pluginFeatureEnabledForCWD(cwd) {
			out = append(out, cwd)
		}
	}
	return out
}

func (r *RuntimeRouter) pluginFeatureEnabledForCWD(cwd string) bool {
	if r == nil || r.services.Config == nil || strings.TrimSpace(r.services.Config.CodexHome()) == "" {
		return true
	}
	cfg, err := config.LoadWithOptions(r.services.Config.CodexHome(), &config.LoadOptions{CWD: cwd})
	if err != nil || cfg == nil {
		return true
	}
	return features.Enabled(cfg.FeatureSettings(), "plugins")
}

func (r *RuntimeRouter) handleSkillsExtraRootsSet(request *Request) (*SkillsExtraRootsSetResponse, error) {
	var params SkillsExtraRootsSetParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	response, err := r.requireSkills().SetExtraRoots(&params)
	if err == nil {
		r.notify(NotificationSkillsChanged, &SkillsChangedNotification{})
	}
	return response, err
}

// applyGoalTokenBudgetLimit resolves the thread's effective
// goals.max_goal_token_budget configuration and attaches it to the goal set
// params so every persistence path defaults and validates budgets identically
// (mirrors the Rust GoalExtensionConfig.max_goal_token_budget plumbing).
func (r *RuntimeRouter) applyGoalTokenBudgetLimit(params *GoalSetParams) {
	if r == nil || params == nil || strings.TrimSpace(params.ThreadID) == "" {
		return
	}
	cfg := &config.Config{Values: map[string]any{}}
	record, err := r.threadRecord(session.ThreadID(params.ThreadID), true, false)
	if err == nil && record != nil {
		if loaded, loadErr := r.effectiveConfigForThreadStart(&ThreadStartParams{
			CWD: record.Metadata.CWD,
		}); loadErr == nil && loaded != nil {
			cfg = loaded
		}
	} else if r.services.Config != nil {
		if loaded, loadErr := config.LoadWithOptions(strings.TrimSpace(r.services.Config.CodexHome()), &config.LoadOptions{}); loadErr == nil && loaded != nil {
			cfg = loaded
		}
	}
	if goals, err := cfg.GoalsConfig(); err == nil && goals != nil {
		params.MaxGoalTokenBudget = cloneInt64PtrAppserver(goals.MaxGoalTokenBudget)
	}
}

func (r *RuntimeRouter) handleSkillsConfigWrite(request *Request) (*SkillsConfigWriteResponse, error) {
	var params SkillsConfigWriteParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	return r.requireSkills().WriteConfig(&params)
}

func (r *RuntimeRouter) handleMarketplaceAdd(request *Request) (*plugin.MarketplaceAddResponse, error) {
	var params plugin.MarketplaceAddParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	return r.requirePlugins().AddMarketplace(&params)
}

func (r *RuntimeRouter) handleMarketplaceRemove(request *Request) (*plugin.MarketplaceRemoveResponse, error) {
	var params plugin.MarketplaceRemoveParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(firstNonEmpty(params.MarketplaceName, params.Name))
	if name != "" {
		source, err := r.marketplaceRemoveConflictSource(name)
		if err != nil {
			return nil, err
		}
		if source != "" {
			return nil, fmt.Errorf("marketplace `%s` is also defined by the %s layer; update that configuration source instead of removing it", name, source)
		}
	}
	return r.requirePlugins().RemoveMarketplace(&params)
}

// marketplaceRemoveConflictSource returns a non-empty configuration source when
// the marketplace is defined by another enabled layer in the operation's config
// stack, so removing a user override cannot delete a snapshot still referenced
// by a lower-precedence layer (Rust #40683 remove_marketplace_sync).
func (r *RuntimeRouter) marketplaceRemoveConflictSource(marketplaceName string) (string, error) {
	if r == nil || r.services.Config == nil {
		return "", nil
	}
	return MarketplaceRemoveConflictSource(r.services.Config, marketplaceName)
}

// MarketplaceRemoveConflictSource returns a non-empty configuration source when
// the marketplace is defined by another enabled layer in the operation's config
// stack, so removing a user override cannot delete a snapshot still referenced
// by a lower-precedence layer (Rust #40683 remove_marketplace_sync).
func MarketplaceRemoveConflictSource(svc *config.ConfigService, marketplaceName string) (string, error) {
	if svc == nil {
		return "", nil
	}
	read, err := svc.Read(&config.ConfigReadParams{IncludeLayers: true})
	if err != nil || read == nil {
		return "", err
	}
	userConfigPath := config.ConfigPath(svc.CodexHome())
	for _, layer := range read.Layers {
		if layer.Name.Type == config.LayerSourceUser &&
			strings.EqualFold(strings.TrimSpace(layer.Name.File), strings.TrimSpace(userConfigPath)) &&
			layer.Name.Profile == nil {
			// The user override being removed is the layer we may clean up.
			continue
		}
		if layerDisabledReason(layer.DisabledReason) != "" {
			continue
		}
		cfg, ok := layer.Config.(map[string]any)
		if !ok {
			continue
		}
		marketplaces, ok := cfg["marketplaces"].(map[string]any)
		if !ok {
			continue
		}
		if _, defined := marketplaces[marketplaceName]; defined {
			return string(layer.Name.Type), nil
		}
	}
	return "", nil
}

func layerDisabledReason(reason *string) string {
	if reason == nil {
		return ""
	}
	return strings.TrimSpace(*reason)
}

func (r *RuntimeRouter) handleMarketplaceUpgrade(request *Request) (*plugin.MarketplaceUpgradeResponse, error) {
	var params plugin.MarketplaceUpgradeParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	response, err := r.requirePlugins().UpgradeMarketplace(&params)
	if err != nil {
		return nil, err
	}
	// Rust marketplace_upgrade refreshes the plugin consumers only when a root
	// actually changed.
	if response != nil && len(response.UpgradedRoots) > 0 {
		r.effectivePluginsChanged()
	}
	return response, nil
}

func (r *RuntimeRouter) handlePluginList(request *Request) (*plugin.PluginListResponse, error) {
	var params plugin.PluginListParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	// Custom embedders may construct a router without the default startup path.
	// Refresh the auth-dependent projection before returning the catalog.
	if r.services.Config != nil {
		r.configureMCPFromConfig()
	}
	// Rust #40954: the plugin catalog honors the effective config stack. When the
	// plugins feature is disabled it returns an empty catalog; when `cwds` are
	// omitted/empty the catalog config excludes project config.
	if r.pluginCatalogConfigDisabled(params.CWDs) {
		return &plugin.PluginListResponse{
			Marketplaces:          []plugin.PluginMarketplaceEntry{},
			MarketplaceLoadErrors: []plugin.MarketplaceLoadErrorInfo{},
			FeaturedPluginIDs:     []string{},
		}, nil
	}
	response := r.requirePlugins().List(&params)
	// Rust #40954/#41208: surface marketplaces declared in the effective config
	// stack for every requested cwd (project plugin/marketplace config) in
	// addition to the installed store. The first source wins for duplicate
	// marketplace names while installed/enabled state merges across repos.
	cwds := cleanStringSlice(params.CWDs)
	if len(cwds) == 0 {
		cwds = []string{""}
	}
	for _, cwd := range cwds {
		if r.services.Config == nil {
			continue
		}
		readParams := &config.ConfigReadParams{}
		if cwd != "" {
			readParams.CWD = &cwd
		}
		read, err := r.services.Config.Read(readParams)
		if err != nil || read == nil || pluginCatalogDisabledFromValues(read.Config) {
			continue
		}
		if entries, loadErrors := r.services.Plugins.ResolveConfigMarketplaces(read.Config); len(entries) > 0 || len(loadErrors) > 0 {
			response.Marketplaces = mergePluginMarketplaceEntries(response.Marketplaces, entries)
			response.MarketplaceLoadErrors = append(response.MarketplaceLoadErrors, loadErrors...)
		}
	}
	if r.services.Plugins.TargetCuratedMarketplace() == plugin.TargetCuratedOpenAIWithRemote {
		r.startInstalledRemotePluginSync()
	} else {
		r.maybeStartCuratedRepoSync(true)
	}
	return response, nil
}

// mergePluginMarketplaceEntries concatenates config-declared marketplaces onto
// the primary (installed) marketplace entries, keeping the first occurrence of a
// name so the installed/cached entry wins.
func mergePluginMarketplaceEntries(primary, extra []plugin.PluginMarketplaceEntry) []plugin.PluginMarketplaceEntry {
	if len(extra) == 0 {
		return primary
	}
	seen := make(map[string]bool, len(primary)+len(extra))
	merged := make([]plugin.PluginMarketplaceEntry, 0, len(primary)+len(extra))
	for _, entry := range primary {
		merged = append(merged, entry)
		if entry.Name != "" {
			seen[entry.Name] = true
		}
	}
	for _, entry := range extra {
		if entry.Name != "" && seen[entry.Name] {
			continue
		}
		merged = append(merged, entry)
		if entry.Name != "" {
			seen[entry.Name] = true
		}
	}
	return merged
}

// pluginCatalogConfigDisabled mirrors Rust PluginRequestProcessor::load_catalog_config
// (#40954): the catalog config is the effective config stack (system/user/runtime)
// without a discovered project when `cwds` are empty/omitted, and project-inclusive
// otherwise. It reports whether the plugins feature is disabled in that stack.
func (r *RuntimeRouter) pluginCatalogConfigDisabled(cwds []string) bool {
	read, err := r.pluginCatalogConfig(cwds)
	if err != nil || read == nil {
		return false
	}
	return pluginCatalogDisabledFromValues(read.Config)
}

// pluginCatalogConfig reads the effective config stack that drives the plugin
// catalog (Rust PluginRequestProcessor::load_catalog_config): non-project config
// when `cwds` are empty/omitted, and the appserver process CWD (with project
// config) when `cwds` are present.
func (r *RuntimeRouter) pluginCatalogConfig(cwds []string) (*config.ConfigReadResponse, error) {
	if r == nil || r.services.Config == nil {
		return nil, nil
	}
	params := &config.ConfigReadParams{}
	if len(cleanStringSlice(cwds)) > 0 {
		if cwd := r.threadStartDefaultCWD(); cwd != "" {
			params.CWD = &cwd
		}
	}
	return r.services.Config.Read(params)
}

// pluginCatalogDisabledFromValues reports whether the plugins feature is disabled
// in a config value map (Rust #40954 Feature::Plugins gate for the plugin catalog).
func pluginCatalogDisabledFromValues(values map[string]any) bool {
	return !features.Enabled((&config.Config{Values: values}).FeatureSettings(), "plugins")
}

func (r *RuntimeRouter) handlePluginInstalled(request *Request) (*plugin.PluginInstalledResponse, error) {
	var params plugin.PluginInstalledParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	response := r.requirePlugins().Installed(&params)
	// Rust #41208: honor per-repository plugin configuration by surfacing
	// marketplaces declared in the effective config stack for every requested cwd.
	for _, cwd := range cleanStringSlice(params.CWDs) {
		if r.services.Config == nil {
			break
		}
		readParams := &config.ConfigReadParams{}
		if cwd != "" {
			readParams.CWD = &cwd
		}
		read, err := r.services.Config.Read(readParams)
		if err != nil || read == nil || pluginCatalogDisabledFromValues(read.Config) {
			continue
		}
		if entries, loadErrors := r.services.Plugins.ResolveConfigMarketplaces(read.Config); len(entries) > 0 || len(loadErrors) > 0 {
			response.Marketplaces = mergePluginMarketplaceEntries(response.Marketplaces, entries)
			response.MarketplaceLoadErrors = append(response.MarketplaceLoadErrors, loadErrors...)
		}
	}
	return response, nil
}

// handlePluginReconcile exposes the plugin/reconcile JSON-RPC method. The Go
// port performs installed remote-plugin synchronization through the existing
// startup/background sync path rather than a separate Rust reconciler, so this
// currently acknowledges the request and returns no changed-plugin details.
func (r *RuntimeRouter) handlePluginReconcile(request *Request) (*plugin.PluginReconcileResponse, error) {
	var params plugin.PluginReconcileParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	before := map[string]plugin.PluginDetail{}
	if r != nil && r.services.Plugins != nil {
		for _, detail := range r.services.Plugins.InstalledDetails() {
			before[detail.Summary.ID] = detail
		}
	}
	refreshed := false
	var reconcileErr error
	if r != nil {
		refreshed, reconcileErr = r.reconcileInstalledRemotePlugins(context.Background())
	}
	if reconcileErr != nil {
		return nil, fmt.Errorf("failed to reconcile remote installed plugins: %w", reconcileErr)
	}
	failedRemotePluginIDs, failedMaterializationRemotePluginIDs := takeRemoteInstalledPluginSyncFailures()
	if failedRemotePluginIDs == nil {
		failedRemotePluginIDs = []string{}
	}
	if failedMaterializationRemotePluginIDs == nil {
		failedMaterializationRemotePluginIDs = []string{}
	}
	changed := []plugin.PluginReconcileChangedPlugin{}
	if r != nil && r.services.Plugins != nil {
		after := map[string]plugin.PluginDetail{}
		for _, detail := range r.services.Plugins.InstalledDetails() {
			after[detail.Summary.ID] = detail
		}
		ids := map[string]bool{}
		for id := range before {
			ids[id] = true
		}
		for id := range after {
			ids[id] = true
		}
		for id := range ids {
			previous, hadBefore := before[id]
			current, hasAfter := after[id]
			if hadBefore == hasAfter && pluginReconcileSignature(previous) == pluginReconcileSignature(current) {
				continue
			}
			detail := current
			if !hasAfter {
				detail = previous
			}
			changed = append(changed, plugin.PluginReconcileChangedPlugin{
				ID:        strings.TrimSpace(id),
				HasMCPS:   len(detail.MCPServers) > 0,
				HasApps:   len(detail.Apps) > 0 || len(detail.AppTemplates) > 0,
				HasHooks:  len(detail.Hooks) > 0,
				HasSkills: detail.Summary.HasSkills,
			})
		}
		sort.SliceStable(changed, func(i, j int) bool { return changed[i].ID < changed[j].ID })
	}
	if !refreshed && len(changed) == 0 {
		changed = []plugin.PluginReconcileChangedPlugin{}
	}
	if refreshed {
		for _, change := range changed {
			if change.HasHooks {
				if err := r.refreshPluginHookRuntimes(); err != nil {
					return nil, fmt.Errorf("failed to refresh plugin hook runtimes: %w", err)
				}
				break
			}
		}
	}
	return &plugin.PluginReconcileResponse{
		ChangedPlugins:                       changed,
		FailedRemotePluginIDs:                failedRemotePluginIDs,
		FailedMaterializationRemotePluginIDs: failedMaterializationRemotePluginIDs,
	}, nil
}

// refreshPluginHookRuntimes rebuilds the dynamically discovered plugin hook
// sources. Go hook runtimes are re-derived for every turn and hook/list call,
// so there is no persistent loaded-runtime cache to invalidate; this method
// exists to preserve the Rust refresh gate and to surface future load failures.
func (r *RuntimeRouter) refreshPluginHookRuntimes() error {
	if r == nil {
		return nil
	}
	_ = r.configureHookDiscovery()
	return nil
}

func pluginReconcileSignature(detail plugin.PluginDetail) string {
	data, _ := json.Marshal(struct {
		Enabled           bool
		HasSkills         bool
		RemotePluginID    string
		Availability      plugin.PluginAvailability
		AuthPolicy        plugin.PluginAuthPolicy
		DisplayNameTag    string
		InstallSuggestion bool
		Version           *string
		LocalVersion      *string
		DisabledReason    *plugin.PluginDisabledReason
		InstallPolicy     plugin.PluginInstallPolicy
		MCPServers        []string
		Apps              []plugin.AppSummary
		Templates         []plugin.AppTemplateSummary
		Hooks             []plugin.PluginHookSummary
	}{
		Enabled:           detail.Summary.Enabled,
		HasSkills:         detail.Summary.HasSkills,
		RemotePluginID:    detail.Summary.RemotePluginID,
		Availability:      detail.Summary.Availability,
		AuthPolicy:        detail.Summary.AuthPolicy,
		DisplayNameTag:    detail.Summary.PluginDisplayNameTag,
		InstallSuggestion: detail.Summary.InstallSuggestion,
		Version:           detail.Summary.Version,
		LocalVersion:      detail.Summary.LocalVersion,
		DisabledReason:    detail.Summary.DisabledReason,
		InstallPolicy:     detail.Summary.InstallPolicy,
		MCPServers:        detail.MCPServers,
		Apps:              detail.Apps,
		Templates:         detail.AppTemplates,
		Hooks:             detail.Hooks,
	})
	return string(data)
}

func (r *RuntimeRouter) handlePluginRead(request *Request) (*plugin.PluginReadResponse, error) {
	var params plugin.PluginReadParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	response, err := r.requirePlugins().Read(&params)
	if err != nil {
		return nil, err
	}
	r.applyPluginAppMcpRoutingPolicy(response)
	return response, nil
}

// applyPluginAppMcpRoutingPolicy applies the legacy plugin app/MCP routing policy
// to a plugin read result (Rust #41230): apps are omitted when unavailable, while
// MCP server alternatives are retained, even when no authentication mode is set.
func (r *RuntimeRouter) applyPluginAppMcpRoutingPolicy(response *plugin.PluginReadResponse) {
	if r == nil || response == nil {
		return
	}
	authMode := ""
	if resolved, err := r.resolveAuthWithLoginRestrictions(r.codexHomeForRollout()); err == nil && resolved != nil {
		authMode = strings.TrimSpace(resolved.Auth.AuthMode)
	}
	detail := &response.Plugin
	names, apps := plugin.ApplyAppMcpRoutingPolicy(authMode, detail.MCPServers, detail.Apps, detail.AppTemplates)
	detail.MCPServers = names
	detail.Apps = apps
}

func (r *RuntimeRouter) handlePluginSkillRead(request *Request) (*plugin.PluginSkillReadResponse, error) {
	var params plugin.PluginSkillReadParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	return r.requirePlugins().ReadSkill(&params)
}

func (r *RuntimeRouter) handlePluginShareSave(request *Request) (*plugin.PluginShareSaveResponse, error) {
	var params plugin.PluginShareSaveParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	response, err := r.requirePlugins().SaveShare(&params)
	if err != nil {
		return nil, err
	}
	// Rust plugin_share_save clears the plugin-derived caches.
	r.clearConfigDerivedCaches()
	return response, nil
}

func (r *RuntimeRouter) handlePluginShareUpdateTargets(request *Request) (*plugin.PluginShareUpdateTargetsResponse, error) {
	var params plugin.PluginShareUpdateTargetsParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	response, err := r.requirePlugins().UpdateShareTargets(&params)
	if err != nil {
		return nil, err
	}
	// Rust plugin_share_update_targets clears the plugin-derived caches.
	r.clearConfigDerivedCaches()
	return response, nil
}

func (r *RuntimeRouter) handlePluginShareList(request *Request) (*plugin.PluginShareListResponse, error) {
	var params plugin.PluginShareListParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	return r.requirePlugins().ListShares(&params), nil
}

func (r *RuntimeRouter) handlePluginShareCheckout(request *Request) (*plugin.PluginShareCheckoutResponse, error) {
	var params plugin.PluginShareCheckoutParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	response, err := r.requirePlugins().CheckoutShare(&params)
	if err != nil {
		return nil, err
	}
	// Rust plugin_share_checkout clears the plugin-derived caches.
	r.clearConfigDerivedCaches()
	return response, nil
}

func (r *RuntimeRouter) handlePluginShareDelete(request *Request) (*plugin.PluginShareDeleteResponse, error) {
	var params plugin.PluginShareDeleteParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	response, err := r.requirePlugins().DeleteShare(&params)
	if err != nil {
		return nil, err
	}
	// Rust plugin_share_delete clears the plugin-derived caches.
	r.clearConfigDerivedCaches()
	return response, nil
}

func (r *RuntimeRouter) handlePluginInstall(request *Request) (*plugin.PluginInstallResponse, error) {
	var params plugin.PluginInstallParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	service := r.requirePlugins()
	response, err := service.Install(&params)
	if err != nil {
		r.emitPluginInstallFailedAnalyticsEvent(context.Background(), request.normalizedConnectionID(), &params, err)
		return nil, err
	}
	// Rust #42593: a successful install changes the effective plugin set, so
	// refresh the plugin/skill caches and the MCP runtimes before the caller can
	// use the plugin's servers from threads loaded before the install.
	r.effectivePluginsChanged()
	if response != nil {
		if detail := pluginAnalyticsDetailByID(service, response.PluginID); detail != nil {
			r.emitPluginStateAnalyticsEvent(context.Background(), request.normalizedConnectionID(), telemetry.CodexPluginInstalledEventType, detail)
		}
	}
	return response, nil
}

func (r *RuntimeRouter) handlePluginUninstall(request *Request) (*plugin.PluginUninstallResponse, error) {
	var params plugin.PluginUninstallParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	service := r.requirePlugins()
	detail := pluginAnalyticsDetailByID(service, params.PluginID)
	response, err := service.Uninstall(&params)
	if err != nil {
		return nil, err
	}
	// Rust plugin_uninstall reloads the config and refreshes the plugin-derived
	// caches, MCP runtimes, and hook runtimes.
	r.effectivePluginsChanged()
	if detail != nil {
		r.emitPluginStateAnalyticsEvent(context.Background(), request.normalizedConnectionID(), telemetry.CodexPluginUninstalledEventType, detail)
	}
	return response, nil
}

func (r *RuntimeRouter) handleModelList(request *Request) (*model.ModelListResponse, error) {
	var params model.ModelListParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	return r.requireModels().List(&params)
}

func (r *RuntimeRouter) handleModelProviderCapabilitiesRead(request *Request) (*model.ProviderCapabilitiesReadResponse, error) {
	var params model.ProviderCapabilitiesReadParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	// Rust model_provider_capabilities_read loads the latest config, builds the
	// configured provider, and reports that provider's capabilities (the
	// catalog-derived values are only a fallback when no config is available).
	if r.services.Config != nil {
		if read, err := r.services.Config.Read(&config.ConfigReadParams{}); err == nil && read != nil {
			providerID := strings.TrimSpace(stringFromMap(read.Config, "model_provider"))
			if providerInfo, err := model.ProviderForConfigID(read.Config, providerID, strings.TrimSpace(stringFromMap(read.Config, "openai_base_url"))); err == nil && providerInfo != nil {
				capabilities := model.CreateRuntimeProviderForID(providerID, *providerInfo, nil).Capabilities()
				return &model.ProviderCapabilitiesReadResponse{
					NamespaceTools:  capabilities.NamespaceTools,
					ImageGeneration: capabilities.ImageGeneration,
					WebSearch:       capabilities.WebSearch,
				}, nil
			}
		}
	}
	return r.requireModels().ProviderCapabilities(&params), nil
}

func (r *RuntimeRouter) handlePermissionProfileList(request *Request) (*sandbox.PermissionProfileListResponse, error) {
	var params sandbox.PermissionProfileListParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if service, err := r.permissionProfileServiceForRequest(&params); err != nil {
		return nil, err
	} else if service != nil {
		return service.List(&params)
	}
	return r.requirePermissions().List(&params)
}

func (r *RuntimeRouter) permissionProfileServiceForRequest(params *sandbox.PermissionProfileListParams) (*sandbox.PermissionProfileService, error) {
	if r == nil || r.services.Config == nil {
		return nil, nil
	}
	readParams := &config.ConfigReadParams{}
	if params != nil && params.CWD != nil {
		readParams.CWD = cloneString(params.CWD)
	}
	read, err := r.services.Config.Read(readParams)
	if err != nil {
		return nil, err
	}
	summaries, err := config.PermissionProfileSummariesFromValues(read.Config)
	if err != nil {
		return nil, err
	}
	return sandbox.NewPermissionProfileService(summaries), nil
}

func (r *RuntimeRouter) handleCollaborationModeList(request *Request) (*CollaborationModeListResponse, error) {
	var params CollaborationModeListParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	return r.requireCollaboration().List(&params), nil
}

func (r *RuntimeRouter) handleMockExperimentalMethod(request *Request) (*MockExperimentalMethodResponse, error) {
	var params MockExperimentalMethodParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	return &MockExperimentalMethodResponse{Echoed: params.Value}, nil
}

func (r *RuntimeRouter) handleMCPServerOauthLogin(request *Request) (*mcp.MCPServerOauthLoginResponse, error) {
	var params mcp.MCPServerOauthLoginParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if params.ThreadID != nil {
		if err := r.ensureMCPThread(*params.ThreadID); err != nil {
			return nil, err
		}
	}
	return r.mcpServiceForThread(stringPtrValue(params.ThreadID), nil).OauthLogin(&params)
}

func (r *RuntimeRouter) handleMCPServerOauthCancel(request *Request) (*mcp.MCPServerOauthCancelResponse, error) {
	var params mcp.MCPServerOauthCancelParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if params.ThreadID != nil {
		if err := r.ensureMCPThread(*params.ThreadID); err != nil {
			return nil, err
		}
	}
	return r.mcpServiceForThread(stringPtrValue(params.ThreadID), nil).OauthCancel(&params)
}

func (r *RuntimeRouter) handleMCPServerRefresh(request *Request) (*mcp.MCPServerRefreshResponse, error) {
	if request != nil && request.Method == MethodConfigMCPServerReload {
		r.configureMCPFromConfig()
	}
	response := r.requireMCP().Refresh()
	if r.mcpRuntimes != nil {
		r.mcpRuntimes.refreshAll()
	}
	return response, nil
}

func (r *RuntimeRouter) configureMCPFromConfig() {
	if r == nil || r.services.Config == nil {
		return
	}
	if err := r.services.Config.ReloadRequirementsFromHome(); err != nil {
		slog.Warn("failed to reload managed MCP requirements", "error", err)
	}
	read, err := r.services.Config.Read(&config.ConfigReadParams{})
	if err != nil || read == nil {
		return
	}
	var snapshot *auth.AuthDotJSON
	var runtimeAuth *mcp.RuntimeAuth
	if resolved, resolveErr := r.resolveAuthWithLoginRestrictions(r.services.Config.CodexHome()); resolveErr == nil && resolved != nil {
		snapshot = &resolved.Auth
	} else if r.services.Account != nil {
		snapshot = r.services.Account.AuthSnapshot()
	}
	if snapshot != nil {
		runtimeAuth = mcp.RuntimeAuthFromSnapshot(snapshot)
	}
	if r.services.Plugins != nil {
		authMode := ""
		if snapshot != nil {
			authMode = snapshot.Mode()
		}
		providerID := firstNonEmpty(stringFromMap(read.Config, "model_provider"), stringFromMap(read.Config, "modelProvider"), model.OpenAIProviderID)
		if r.services.Plugins.SetRuntimeRoute(authMode, providerID) && r.services.Skills != nil {
			r.services.Skills.ClearCache()
		}
	}
	var requirements *config.ConfigRequirements
	if current := r.services.Config.Requirements(); current != nil {
		requirements = current.Requirements
	}
	r.requireMCP().ApplyRuntimeConfig(r.runtimeMCPConfig(read.Config, r.services.Config.CodexHome(), runtimeAuth, requirements))
	if snapshot != nil {
		httpClient := r.httpClientForConfig(&config.Config{Values: read.Config})
		r.requireMCP().SetTrustedAccess(mcp.ServiceTrustedAccessFromSnapshot(snapshot, r.chatGPTBaseURL(), httpClient))
	}
	r.mcpConfigManaged.Store(true)
	if r.mcpRuntimes != nil {
		r.mcpRuntimes.invalidateAll()
	}
	r.prewarmLoadedMCPThreads()
}

func (r *RuntimeRouter) runtimeMCPConfig(values map[string]any, codexHome string, runtimeAuth *mcp.RuntimeAuth, requirements *config.ConfigRequirements) *mcp.RuntimeConfig {
	return r.runtimeMCPConfigForThread("", values, codexHome, runtimeAuth, requirements)
}

// runtimeMCPConfigForThread builds the MCP runtime config for one thread. An
// empty thread ID builds the process-wide config; otherwise servers contributed
// by plugins the thread has disabled are excluded without changing shared
// plugin state (Rust #44655).
func (r *RuntimeRouter) runtimeMCPConfigForThread(threadID string, values map[string]any, codexHome string, runtimeAuth *mcp.RuntimeAuth, requirements *config.ConfigRequirements) *mcp.RuntimeConfig {
	base := mcp.RuntimeConfigFromValuesWithAuthAndRequirements(values, codexHome, runtimeAuth, requirements)
	if rawFeatures, ok := values["features"].(map[string]any); ok {
		if settings, _ := features.ResolveSettings(rawFeatures); features.Enabled(settings, "mcp_oauth_refresh_coordination") {
			for name, registration := range base.Servers {
				registration.Config.OAuthRefreshMode = mcp.McpOAuthRefreshCoordinated
				base.Servers[name] = registration
			}
		}
	}
	if r != nil {
		base.HTTPClient = r.httpClientForConfig(&config.Config{Values: values})
	}
	if r == nil || r.services.Plugins == nil {
		return base
	}
	contributions := r.filterDisabledPluginMCPContributions(threadID, r.services.Plugins.EnabledMCPServerContributions())
	overlays := make([]mcp.ConfigOverlay, 0, len(contributions))
	for _, contribution := range contributions {
		server := mcp.ServerConfigFromValues(contribution.Config)
		if server == nil {
			continue
		}
		if server.EffectiveAuth() == mcp.ServerAuthEMAAuth {
			// Plugin MCP declarations cannot select ema_auth; enterprise
			// authentication is configured in host policy (Rust #44832).
			continue
		}
		if server.Command != "" && server.CWD == "" {
			server.CWD = contribution.PluginRoot
		}
		overlays = append(overlays, mcp.ConfigOverlay{
			Name:              contribution.Name,
			Config:            *server,
			ContributorID:     contribution.PluginID,
			ContributionOrder: contribution.Order,
			Source:            mcp.CatalogSourcePlugin,
			PluginID:          contribution.PluginID,
			PluginDisplayName: contribution.PluginDisplayName,
			PluginHostRoot:    contribution.PluginRoot,
		})
	}
	if len(overlays) == 0 {
		return base
	}
	return mcp.NewManager(nil).RuntimeConfig(*base, overlays)
}

// filterDisabledPluginMCPContributions drops MCP servers contributed by plugins
// the thread has disabled. The selection is thread-scoped, so the shared plugin
// state is untouched (Rust #44655).
func (r *RuntimeRouter) filterDisabledPluginMCPContributions(threadID string, contributions []plugin.MCPServerContribution) []plugin.MCPServerContribution {
	if r == nil || len(contributions) == 0 {
		return contributions
	}
	disabled := r.threadDisabledPluginIDs(threadID)
	if len(disabled) == 0 {
		return contributions
	}
	blocked := make(map[string]struct{}, len(disabled))
	for _, id := range disabled {
		if id = strings.TrimSpace(id); id != "" {
			blocked[id] = struct{}{}
		}
	}
	if len(blocked) == 0 {
		return contributions
	}
	filtered := make([]plugin.MCPServerContribution, 0, len(contributions))
	for _, contribution := range contributions {
		if _, ok := blocked[strings.TrimSpace(contribution.PluginID)]; ok {
			continue
		}
		filtered = append(filtered, contribution)
	}
	return filtered
}

func (r *RuntimeRouter) handleMCPServerStatusList(request *Request) (*mcp.MCPListServerStatusResponse, error) {
	var params mcp.MCPListServerStatusParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if params.ThreadID != nil {
		if err := r.ensureMCPThread(*params.ThreadID); err != nil {
			return nil, err
		}
	}
	return r.mcpServiceForThread(stringPtrValue(params.ThreadID), nil).ListStatusChecked(&params)
}

func (r *RuntimeRouter) handleMCPServerResourceRead(request *Request) (*mcp.MCPResourceReadResponse, error) {
	var params mcp.MCPResourceReadParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if params.ThreadID != nil {
		if err := r.ensureMCPThread(*params.ThreadID); err != nil {
			return nil, err
		}
	}
	return r.mcpServiceForThread(stringPtrValue(params.ThreadID), nil).ReadResource(&params)
}

func (r *RuntimeRouter) handleMCPServerToolCall(request *Request) (*mcp.MCPToolCallResponse, error) {
	var params mcp.MCPToolCallParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := r.ensureMCPThread(params.ThreadID); err != nil {
		return nil, err
	}
	return r.mcpServiceForThread(params.ThreadID, nil).CallTool(&params)
}

func (r *RuntimeRouter) ensureMCPThread(threadID string) error {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" || r == nil || r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil {
		return nil
	}
	_, err := r.threadRecord(session.ThreadID(threadID), false, false)
	return err
}

func (r *RuntimeRouter) handleFSReadFile(request *Request) (*ReadFileResponse, error) {
	var params ReadFileParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	fs, err := r.localFS()
	if err != nil {
		return nil, err
	}
	return fs.ReadFile(&params)
}

func (r *RuntimeRouter) handleFSWriteFile(request *Request) (*WriteFileResponse, error) {
	var params WriteFileParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	fs, err := r.localFS()
	if err != nil {
		return nil, err
	}
	response, err := fs.WriteFile(&params)
	if err != nil {
		return nil, err
	}
	r.notifyFSChangedPath(params.Path)
	return response, nil
}

func (r *RuntimeRouter) handleFSCreateDirectory(request *Request) (*CreateDirectoryResponse, error) {
	var params CreateDirectoryParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	fs, err := r.localFS()
	if err != nil {
		return nil, err
	}
	response, err := fs.CreateDirectory(&params)
	if err != nil {
		return nil, err
	}
	r.notifyFSChangedPath(params.Path)
	return response, nil
}

func (r *RuntimeRouter) handleFSGetMetadata(request *Request) (*GetMetadataResponse, error) {
	var params GetMetadataParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	fs, err := r.localFS()
	if err != nil {
		return nil, err
	}
	return fs.GetMetadata(&params)
}

func (r *RuntimeRouter) handleFSReadDirectory(request *Request) (*ReadDirectoryResponse, error) {
	var params ReadDirectoryParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	fs, err := r.localFS()
	if err != nil {
		return nil, err
	}
	return fs.ReadDirectory(&params)
}

func (r *RuntimeRouter) handleFSRemove(request *Request) (*RemoveResponse, error) {
	var params RemoveParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	fs, err := r.localFS()
	if err != nil {
		return nil, err
	}
	response, err := fs.Remove(&params)
	if err != nil {
		return nil, err
	}
	r.notifyFSChangedPath(params.Path)
	return response, nil
}

func (r *RuntimeRouter) handleFSCopy(request *Request) (*CopyResponse, error) {
	var params CopyParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	fs, err := r.localFS()
	if err != nil {
		return nil, err
	}
	response, err := fs.Copy(&params)
	if err != nil {
		return nil, err
	}
	r.notifyFSChangedPath(params.DestinationPath)
	return response, nil
}

func (r *RuntimeRouter) notifyFSChangedPath(path string) {
	for _, changed := range r.requireFS().ChangedForPath(path) {
		if changed.notification == nil {
			continue
		}
		r.notifyToConnection(changed.connectionID, NotificationFSChanged, changed.notification)
	}
}

func (r *RuntimeRouter) handleFSWatch(request *Request) (*WatchResponse, error) {
	var params WatchParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	fs, err := r.localFS()
	if err != nil {
		return nil, err
	}
	return fs.WatchWithConnection(request.normalizedConnectionID(), &params)
}

func (r *RuntimeRouter) handleFSUnwatch(request *Request) (*UnwatchResponse, error) {
	var params UnwatchParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	fs, err := r.localFS()
	if err != nil {
		return nil, err
	}
	return fs.UnwatchWithConnection(request.normalizedConnectionID(), &params)
}

func (r *RuntimeRouter) handleRemoteControlEnable(request *Request) (*remotecontrol.EnableResponse, error) {
	var params *remotecontrol.EnableParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if params == nil {
		params = &remotecontrol.EnableParams{}
	}
	response, notification, err := r.requireRemote().EnableContext(context.Background(), params)
	if err != nil {
		return nil, err
	}
	r.notifyRemoteControlStatusChanged(notification)
	return response, nil
}

func (r *RuntimeRouter) handleRemoteControlDisable(request *Request) (*remotecontrol.DisableResponse, error) {
	var params *remotecontrol.DisableParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if params == nil {
		params = &remotecontrol.DisableParams{}
	}
	response, notification, err := r.requireRemote().DisableContext(context.Background(), params)
	if err != nil {
		return nil, err
	}
	r.notifyRemoteControlStatusChanged(notification)
	return response, nil
}

func (r *RuntimeRouter) handleRemoteControlPairingStart(request *Request) (*remotecontrol.PairingStartResponse, error) {
	var params remotecontrol.PairingStartParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	return r.requireRemote().StartPairingContext(context.Background(), &params)
}

func (r *RuntimeRouter) handleRemoteControlPairingStatus(request *Request) (*remotecontrol.PairingStatusResponse, error) {
	var params remotecontrol.PairingStatusParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	return r.requireRemote().PairingStatusContext(context.Background(), &params)
}

func (r *RuntimeRouter) handleRemoteControlClientsList(request *Request) (*remotecontrol.ClientsListResponse, error) {
	var params remotecontrol.ClientsListParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	return r.requireRemote().ListClientsContext(context.Background(), &params)
}

func (r *RuntimeRouter) handleRemoteControlClientsRevoke(request *Request) (*remotecontrol.ClientsRevokeResponse, error) {
	var params remotecontrol.ClientsRevokeParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	return r.requireRemote().RevokeClientContext(context.Background(), &params)
}

func (r *RuntimeRouter) handleEnvironmentAdd(request *Request) (*EnvironmentAddResponse, error) {
	var params EnvironmentAddParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	return r.requireEnvironment().Add(&params)
}

func (r *RuntimeRouter) handleEnvironmentInfo(request *Request) (*EnvironmentInfoResponse, error) {
	var params EnvironmentInfoParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	return r.requireEnvironment().Info(&params)
}

func (r *RuntimeRouter) handleEnvironmentStatus(request *Request) (*EnvironmentStatusResponse, error) {
	var params EnvironmentStatusParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	return r.requireEnvironment().Status(&params)
}

func (r *RuntimeRouter) handleWindowsSandboxSetupStart(request *Request) (*sandbox.WindowsSetupStartResponse, error) {
	var params sandbox.WindowsSetupStartParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	cwd, err := r.windowsSandboxSetupCWD(params.CWD)
	if err != nil {
		return nil, err
	}
	resolved, err := config.ResolveAllowedWindowsSandboxSetupMode(r.requireConfig().Requirements().Requirements, config.WindowsSandboxSetupMode(params.Mode))
	if err != nil {
		return nil, err
	}
	params.Mode = sandbox.WindowsSetupMode(resolved)
	params.CWD = &cwd
	setupRequest, err := r.windowsSandboxSetupRuntimeRequest(params.Mode, cwd)
	if err != nil {
		return nil, err
	}
	response, err := r.requireWindows().StartSetup(&params)
	if err != nil {
		return nil, err
	}
	if response.Started {
		go r.runWindowsSandboxSetupForConnection(request.normalizedConnectionID(), setupRequest)
	}
	return response, nil
}

func (r *RuntimeRouter) handleFeedbackUpload(request *Request) (*FeedbackUploadResponse, error) {
	var params FeedbackUploadParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	threadID := "feedback-" + request.ID.String()
	if params.ThreadID != nil && *params.ThreadID != "" {
		threadID = *params.ThreadID
	}
	clientTags := cloneStringMap(params.Tags)
	var feedbackMetadata feedbackTurnMetadata
	feedbackMetadataFound := false
	if params.ThreadID != nil && r.services.ThreadRouter != nil {
		if record, err := r.threadRecord(session.ThreadID(threadID), true, false); err == nil && record != nil {
			path := r.services.ThreadRouter.threadRolloutPath(record)
			var selectedTurnID *string
			if value, ok := clientTags["turn_id"]; ok {
				selectedTurnID = &value
			}
			feedbackMetadata, feedbackMetadataFound = feedbackTurnMetadataFromRollout(path, selectedTurnID)
		}
	}
	clientTags = applyFeedbackTurnMetadata(clientTags, feedbackMetadata, feedbackMetadataFound)
	// Rust #44325: report the effective rollout-derived prompt hash alongside the
	// thread, using the same `prompt_hash` tag that is uploaded.
	var promptHash *string
	if value, ok := clientTags["prompt_hash"]; ok && strings.TrimSpace(value) != "" {
		promptHash = &value
	}
	var logsOverride []byte
	var rolloutPaths []string
	if params.IncludeLogs {
		if r.services.LogDBHandler != nil {
			flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := r.services.LogDBHandler.Flush(flushCtx); err != nil {
				slog.Warn("failed to flush sqlite feedback logs", "thread_id", threadID, "error", err)
			}
			cancel()
		}
		feedbackThreadIDs := make([]string, 0)
		if params.ThreadID != nil && strings.TrimSpace(*params.ThreadID) != "" {
			descendants := []string(nil)
			if r.services.SpawnGraph != nil {
				if listed, err := r.services.SpawnGraph.ListThreadSpawnDescendants(threadID, nil); err != nil {
					slog.Warn("failed to list feedback agent subtree", "thread_id", threadID, "error", err)
				} else {
					descendants = listed
				}
			}
			feedbackThreadIDs = FeedbackThreadIDs(threadID, descendants)
		}
		if r.services.StateRuntime != nil && len(feedbackThreadIDs) > 0 {
			if logs, err := r.services.StateRuntime.QueryFeedbackLogsForThreads(context.Background(), feedbackThreadIDs); err != nil {
				slog.Warn("failed to query sqlite feedback logs", "thread_ids", strings.Join(feedbackThreadIDs, ", "), "error", err)
			} else if len(logs) > 0 {
				logsOverride = logs
			}
		}
		if r.services.ThreadRouter != nil {
			for _, feedbackThreadID := range feedbackThreadIDs {
				path, err := r.services.ThreadRouter.findThreadRolloutPath(session.ThreadID(feedbackThreadID), false)
				if err != nil {
					path, err = r.services.ThreadRouter.findThreadRolloutPath(session.ThreadID(feedbackThreadID), true)
				}
				if err == nil && strings.TrimSpace(path) != "" {
					rolloutPaths = append(rolloutPaths, path)
				}
			}
		}
	}
	snapshot := r.requireFeedback()
	snapshot.ThreadID = threadID
	snapshot.PrepareUpload(&FeedbackUploadOptions{
		Classification:      params.Classification,
		Reason:              params.Reason,
		ClientTags:          clientTags,
		IncludeLogs:         params.IncludeLogs,
		LogsOverride:        logsOverride,
		ExtraAttachmentPath: FeedbackAttachmentPaths(rolloutPaths, nil, threadID, nil, params.ExtraLogFiles),
	})
	return &FeedbackUploadResponse{ThreadID: threadID, PromptHash: promptHash}, nil
}

func (r *RuntimeRouter) handleConfigRead(request *Request) (*config.ConfigReadResponse, error) {
	var params config.ConfigReadParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	return r.requireConfig().Read(&params)
}

func (r *RuntimeRouter) handleConfigValueWrite(request *Request) (*config.ConfigWriteResponse, error) {
	var params config.ConfigValueWriteParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	response, err := r.requireConfig().WriteValue(&params)
	if err != nil {
		return nil, err
	}
	// Rust config_value_write runs handle_config_mutation after the write.
	r.clearConfigDerivedCaches()
	return response, nil
}

func (r *RuntimeRouter) handleConfigBatchWrite(request *Request) (*config.ConfigWriteResponse, error) {
	var params config.ConfigBatchWriteParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	response, err := r.requireConfig().BatchWrite(&params)
	if err != nil {
		return nil, err
	}
	r.applyConfigBatchWriteMutation(&params)
	return response, nil
}

func (r *RuntimeRouter) handleExternalAgentConfigDetect(request *Request) (*config.ExternalAgentConfigDetectResponse, error) {
	var params config.ExternalAgentConfigDetectParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	service := r.requireConfig()
	response := service.DetectExternalAgentConfig(&params)
	if err := service.RecordDetectedExternalSessionConnectors(&params, response.Items); err != nil {
		return nil, fmt.Errorf("failed to record detected connector candidates: %w", err)
	}
	return response, nil
}

func (r *RuntimeRouter) handleExternalAgentConfigImport(request *Request) (*config.ExternalAgentConfigImportResponse, error) {
	var params config.ExternalAgentConfigImportParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	response, completedResults := r.requireConfig().StartExternalAgentConfigImport(&params, true)
	if len(params.MigrationItems) > 0 {
		params.MigrationItems = append([]config.ExternalAgentConfigMigrationItem(nil), params.MigrationItems...)
		go r.completeExternalAgentConfigImport(params, response.ImportID, completedResults, request.normalizedConnectionID())
	}
	return response, nil
}

func (r *RuntimeRouter) completeExternalAgentConfigImport(params config.ExternalAgentConfigImportParams, importID string, completedResults []config.ExternalAgentConfigImportTypeResult, connectionID string) {
	sessionResults := make(chan []config.ExternalAgentConfigImportTypeResult, 1)
	pluginResults := make(chan []config.ExternalAgentConfigImportTypeResult, 1)
	go func() {
		if r.services.ExternalAgentSessionImporter != nil {
			sessionResults <- r.services.ExternalAgentSessionImporter(&params)
			return
		}
		sessionResults <- r.importExternalAgentSessions(&params)
	}()
	go func() {
		pluginParams := params
		pluginParams.MigrationItems = externalAgentImportItemsOfType(params.MigrationItems, config.MigrationPlugins)
		pluginResults <- r.requireConfig().ImportExternalAgentConfigItems(&pluginParams)
	}()

	completedResults = append(append([]config.ExternalAgentConfigImportTypeResult(nil), completedResults...), <-sessionResults...)
	completedResults = append(completedResults, <-pluginResults...)
	if externalAgentImportContainsType(params.MigrationItems, config.MigrationPlugins) && r.services.Plugins != nil {
		_ = r.services.Plugins.ReloadConfig()
	}
	notification := r.requireConfig().CompleteExternalAgentConfigImport(importID, &params, completedResults)
	r.emitExternalAgentConfigImportAnalytics(context.Background(), connectionID, &params, notification)
	r.notify(NotificationExternalAgentConfigImportCompleted, notification)
}

func externalAgentImportItemsOfType(items []config.ExternalAgentConfigMigrationItem, itemType config.MigrationItemType) []config.ExternalAgentConfigMigrationItem {
	out := make([]config.ExternalAgentConfigMigrationItem, 0)
	for _, item := range items {
		if item.ItemType == itemType {
			out = append(out, item)
		}
	}
	return out
}

func externalAgentImportContainsType(items []config.ExternalAgentConfigMigrationItem, itemType config.MigrationItemType) bool {
	for _, item := range items {
		if item.ItemType == itemType {
			return true
		}
	}
	return false
}

func (r *RuntimeRouter) handleLoginAccount(request *Request) (*auth.LoginAccountResponse, error) {
	var params auth.LoginAccountParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if auth.IsWorkloadIdentitySelected() {
		return nil, jsonRPCInvalidRequest("Configured external authentication is owned by the app-server host and cannot be changed through account RPCs.")
	}
	if err := r.validateLoginAccountParams(&params); err != nil {
		return nil, err
	}
	if params.Type == "chatgptAuthTokens" {
		r.cancelAllAccountLoginRuntimes()
		r.requireAccount().CancelActiveLogins()
	}
	response, err := r.requireAccount().Login(&params)
	if err != nil {
		return nil, err
	}
	if response != nil && response.LoginID != "" {
		if err := r.startAccountLoginRuntime(&params, response); err != nil {
			_, _ = r.requireAccount().CancelLogin(&auth.CancelLoginAccountParams{LoginID: response.LoginID})
			return nil, err
		}
	}
	r.applyChatGPTLoginConfig(params, response)
	if snapshot := authSnapshotFromLoginParams(&params); snapshot != nil {
		if codexHome := r.codexHomeForRollout(); codexHome != "" {
			if err := r.authStore(codexHome).Save(*snapshot); err != nil {
				return nil, err
			}
		}
		r.requireAccount().ApplyAuthSnapshot(snapshot)
		r.configureMCPFromConfig()
		r.maybeStartCuratedRepoSync(false)
		r.refreshPluginCachesAndMCPRuntimes()
		r.noteAuthChanged()
	}
	if response != nil && response.LoginID == "" {
		r.notify(NotificationAccountLoginCompleted, &auth.AccountLoginCompletedNotification{Success: true})
		r.notify(NotificationAccountUpdated, r.requireAccount().AccountUpdated())
	}
	return response, nil
}

func (r *RuntimeRouter) applyChatGPTLoginConfig(params auth.LoginAccountParams, response *auth.LoginAccountResponse) {
	if r == nil || r.services.Config == nil || response == nil || params.Type != auth.AccountChatGPT || response.Type != auth.AccountChatGPT || strings.TrimSpace(response.AuthURL) == "" {
		return
	}
	read, err := r.services.Config.Read(&config.ConfigReadParams{})
	if err != nil || read == nil {
		return
	}
	cfg := config.Config{Values: read.Config}
	workspaces := normalizedWorkspaceQueryList(cfg.ForcedChatGPTWorkspaceIDs())
	if len(workspaces) == 0 {
		return
	}
	parsed, err := url.Parse(response.AuthURL)
	if err != nil {
		return
	}
	query := parsed.Query()
	query.Set("allowed_workspace_id", strings.Join(workspaces, ","))
	parsed.RawQuery = query.Encode()
	response.AuthURL = parsed.String()
}

func normalizedWorkspaceQueryList(workspaces []string) []string {
	if len(workspaces) == 0 {
		return nil
	}
	out := make([]string, 0, len(workspaces))
	for _, workspace := range workspaces {
		trimmed := strings.TrimSpace(workspace)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func (r *RuntimeRouter) validateLoginAccountParams(params *auth.LoginAccountParams) error {
	if params == nil {
		return nil
	}
	if r == nil || r.services.Config == nil {
		return nil
	}
	read, err := r.services.Config.Read(&config.ConfigReadParams{})
	if err != nil || read == nil {
		return err
	}
	cfg := config.Config{Values: read.Config}
	switch params.Type {
	case auth.AccountAPIKey:
		if r.externalChatGPTAuthActive() {
			return fmt.Errorf(externalChatGPTAuthActiveMessage)
		}
		if cfg.ForcedLoginMethod() == config.ForcedLoginMethodChatGPT {
			return fmt.Errorf("API key login is disabled. Use ChatGPT login instead.")
		}
	case auth.AccountChatGPT, "chatgptDeviceCode":
		if r.externalChatGPTAuthActive() {
			return fmt.Errorf(externalChatGPTAuthActiveMessage)
		}
		if cfg.ForcedLoginMethod() == config.ForcedLoginMethodAPI {
			return fmt.Errorf("ChatGPT login is disabled. Use API key login instead.")
		}
	case "chatgptAuthTokens":
		if cfg.ForcedLoginMethod() == config.ForcedLoginMethodAPI {
			return fmt.Errorf("External ChatGPT auth is disabled. Use API key login instead.")
		}
		workspaces := cfg.ForcedChatGPTWorkspaceIDs()
		if len(workspaces) == 0 {
			return nil
		}
		if err := auth.EnsureWorkspaceAccountAllowed(workspaces, params.ChatGPTAccountID); err != nil {
			return fmt.Errorf("External auth must use one of workspace(s) %v, but received %q.", workspaces, strings.TrimSpace(params.ChatGPTAccountID))
		}
	}
	return nil
}

const externalChatGPTAuthActiveMessage = "External auth is active. Use account/login/start (chatgptAuthTokens) to update it or account/logout to clear it."

func (r *RuntimeRouter) externalChatGPTAuthActive() bool {
	if r == nil {
		return false
	}
	resolved, err := r.resolveAuthWithLoginRestrictions(r.codexHomeForRollout())
	return err == nil && resolved != nil && (&resolved.Auth).Mode() == "chatgptAuthTokens"
}

func authSnapshotFromLoginParams(params *auth.LoginAccountParams) *auth.AuthDotJSON {
	if params == nil {
		return nil
	}
	switch params.Type {
	case auth.AccountAPIKey:
		snapshot := auth.FromAPIKey(params.APIKey)
		return &snapshot
	case "chatgptAuthTokens":
		snapshot := auth.FromChatGPTAuthTokens(params.AccessToken, params.ChatGPTAccountID, params.ChatGPTPlanType)
		return &snapshot
	default:
		return nil
	}
}

func (r *RuntimeRouter) handleCancelLoginAccount(request *Request) (*auth.CancelLoginAccountResponse, error) {
	var params auth.CancelLoginAccountParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	response, err := r.requireAccount().CancelLogin(&params)
	if err != nil {
		return nil, err
	}
	if response != nil && response.Status == auth.CancelLoginCanceled {
		r.cancelAccountLoginRuntime(params.LoginID)
		message := "login canceled"
		r.notify(NotificationAccountLoginCompleted, &auth.AccountLoginCompletedNotification{LoginID: &params.LoginID, Success: false, Error: &message})
	}
	return response, nil
}

func (r *RuntimeRouter) accountOAuthOptions() *auth.OAuthOptions {
	if r != nil && r.services.AccountOAuthOptions != nil {
		copy := *r.services.AccountOAuthOptions
		return &copy
	}
	options := &auth.OAuthOptions{CodexHome: r.codexHomeForRollout(), StoreOptions: r.authStoreOptions()}
	if r.services.HTTPClient != nil {
		if client, ok := r.services.HTTPClient.(*http.Client); ok {
			options.HTTPClient = client
		}
	}
	if r.services.Config != nil {
		if read, err := r.services.Config.Read(&config.ConfigReadParams{}); err == nil && read != nil {
			cfg := &config.Config{Values: read.Config}
			options.ForcedWorkspaces = cfg.ForcedChatGPTWorkspaceIDs()
		}
	}
	return options
}

func (r *RuntimeRouter) startAccountLoginRuntime(params *auth.LoginAccountParams, response *auth.LoginAccountResponse) error {
	if r == nil || params == nil || response == nil || strings.TrimSpace(response.LoginID) == "" {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	loginID := strings.TrimSpace(response.LoginID)
	options := r.accountOAuthOptions()
	var done <-chan error
	switch params.Type {
	case auth.AccountChatGPT:
		server, err := auth.StartBrowserLogin(ctx, options)
		if err != nil {
			cancel()
			return err
		}
		response.AuthURL = server.AuthURL
		done = server.Done
	case "chatgptDeviceCode":
		code, err := auth.RequestDeviceCode(ctx, options)
		if err != nil {
			cancel()
			return err
		}
		response.VerificationURL = code.VerificationURL
		response.UserCode = code.UserCode
		result := make(chan error, 1)
		done = result
		go func() { result <- auth.CompleteDeviceCodeLogin(ctx, options, code) }()
	default:
		cancel()
		return nil
	}
	r.loginRuntimeMu.Lock()
	if r.loginRuntimeCancels == nil {
		r.loginRuntimeCancels = map[string]context.CancelFunc{}
	}
	r.loginRuntimeCancels[loginID] = cancel
	r.loginRuntimeMu.Unlock()
	go r.awaitAccountLoginRuntime(loginID, done)
	return nil
}

func (r *RuntimeRouter) awaitAccountLoginRuntime(loginID string, done <-chan error) {
	err := <-done
	r.loginRuntimeMu.Lock()
	_, owned := r.loginRuntimeCancels[loginID]
	delete(r.loginRuntimeCancels, loginID)
	r.loginRuntimeMu.Unlock()
	if !owned {
		return
	}
	if err != nil {
		message := err.Error()
		r.requireAccount().CompleteLogin(loginID, nil, message)
		r.notify(NotificationAccountLoginCompleted, &auth.AccountLoginCompletedNotification{LoginID: &loginID, Success: false, Error: &message})
		return
	}
	resolved, resolveErr := r.authStore(r.codexHomeForRollout()).Resolve()
	if resolveErr != nil || resolved == nil {
		message := "login completed but persisted auth could not be loaded"
		if resolveErr != nil {
			message = resolveErr.Error()
		}
		r.requireAccount().CompleteLogin(loginID, nil, message)
		r.notify(NotificationAccountLoginCompleted, &auth.AccountLoginCompletedNotification{LoginID: &loginID, Success: false, Error: &message})
		return
	}
	r.requireAccount().ApplyAuthSnapshot(&resolved.Auth)
	r.configureMCPFromConfig()
	r.maybeStartCuratedRepoSync(false)
	r.requireAccount().CompleteLogin(loginID, auth.AccountFromAuth(&resolved.Auth), "")
	r.noteAuthChanged()
	r.refreshPluginCachesAndMCPRuntimes()
	r.notify(NotificationAccountLoginCompleted, &auth.AccountLoginCompletedNotification{LoginID: &loginID, Success: true})
	r.notify(NotificationAccountUpdated, r.requireAccount().AccountUpdated())
}

func (r *RuntimeRouter) cancelAccountLoginRuntime(loginID string) {
	r.loginRuntimeMu.Lock()
	cancel := r.loginRuntimeCancels[strings.TrimSpace(loginID)]
	delete(r.loginRuntimeCancels, strings.TrimSpace(loginID))
	r.loginRuntimeMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (r *RuntimeRouter) cancelAllAccountLoginRuntimes() {
	if r == nil {
		return
	}
	r.loginRuntimeMu.Lock()
	cancels := make([]context.CancelFunc, 0, len(r.loginRuntimeCancels))
	for loginID, cancel := range r.loginRuntimeCancels {
		if cancel != nil {
			cancels = append(cancels, cancel)
		}
		delete(r.loginRuntimeCancels, loginID)
	}
	r.loginRuntimeMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (r *RuntimeRouter) handleAccountSessionsAdd(request *Request) (*auth.AccountSessionsResponse, error) {
	var params auth.AccountSessionsAddParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	return r.requireAccount().AddSession(&params)
}

func (r *RuntimeRouter) handleAccountSessionsList(request *Request) (*auth.AccountSessionsResponse, error) {
	var params auth.AccountSessionsListParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	return r.requireAccount().ListSessions(&params), nil
}

func (r *RuntimeRouter) handleAccountSessionsLogout(request *Request) (*auth.AccountSessionsResponse, error) {
	var params auth.AccountSessionsLogoutParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	response, err := r.requireAccount().LogoutSession(&params)
	if err != nil {
		return nil, err
	}
	r.configureMCPFromConfig()
	r.maybeStartCuratedRepoSync(false)
	r.refreshPluginCachesAndMCPRuntimes()
	r.noteAuthChanged()
	r.retireRemoteControlForAuthChange(context.Background())
	r.notify(NotificationAccountUpdated, r.requireAccount().AccountUpdated())
	return response, nil
}

func (r *RuntimeRouter) handleAccountSessionsSwitch(request *Request) (*auth.AccountSessionsResponse, error) {
	var params auth.AccountSessionsSwitchParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	response, err := r.requireAccount().SwitchSession(&params)
	if err != nil {
		return nil, err
	}
	r.configureMCPFromConfig()
	r.maybeStartCuratedRepoSync(false)
	r.refreshPluginCachesAndMCPRuntimes()
	r.noteAuthChanged()
	r.retireRemoteControlForAuthChange(context.Background())
	r.notify(NotificationAccountUpdated, r.requireAccount().AccountUpdated())
	return response, nil
}

func (r *RuntimeRouter) handleLogoutAccount(request *Request) (*auth.LogoutAccountResponse, error) {
	if auth.IsWorkloadIdentitySelected() {
		return nil, jsonRPCInvalidRequest("Configured external authentication is owned by the app-server host and cannot be changed through account RPCs.")
	}
	if len(request.Params) > 0 {
		var params struct{}
		if err := request.DecodeParams(&params); err != nil {
			return nil, err
		}
	}
	r.cancelAllAccountLoginRuntimes()
	r.requireAccount().CancelActiveLogins()
	response := r.requireAccount().Logout()
	if codexHome := r.codexHomeForRollout(); codexHome != "" {
		if _, err := auth.LogoutWithRevoke(context.Background(), codexHome, r.authStoreOptions()); err != nil {
			return nil, err
		}
	}
	r.configureMCPFromConfig()
	r.maybeStartCuratedRepoSync(false)
	r.refreshPluginCachesAndMCPRuntimes()
	r.noteAuthChanged()
	r.retireRemoteControlForAuthChange(context.Background())
	r.notify(NotificationAccountUpdated, r.requireAccount().AccountUpdated())
	return response, nil
}

func (r *RuntimeRouter) clearRecommendedPluginsCache() {
	if r == nil || r.services.Plugins == nil {
		return
	}
	r.services.Plugins.ClearRecommendedPluginsCache()
}

func (r *RuntimeRouter) effectivePluginsChanged() {
	if r == nil {
		return
	}
	// Rust on_effective_plugins_changed: clear the plugin/skill caches, then
	// refresh the config-derived MCP and hook runtimes of the loaded threads.
	r.clearConfigDerivedCaches()
	r.configureMCPFromConfig()
	r.mcpRuntimes.invalidateAll()
	if err := r.refreshPluginHookRuntimes(); err != nil {
		slog.Warn("failed to refresh hook runtimes after a plugin change", "error", err)
	}
}

func (r *RuntimeRouter) maybeStartCuratedRepoSync(force bool) {
	if r == nil || r.services.Plugins == nil || r.services.Config == nil {
		return
	}
	read, err := r.services.Config.Read(&config.ConfigReadParams{})
	if err != nil || read == nil {
		return
	}
	cfg := &config.Config{Values: read.Config}
	if !features.Enabled(cfg.FeatureSettings(), "plugins") || r.services.Plugins.TargetCuratedMarketplace() == plugin.TargetCuratedOpenAIWithRemote {
		return
	}
	if !force && !r.services.Plugins.HasConfiguredCuratedPlugins() {
		return
	}
	r.services.Plugins.StartCuratedRepoSync(r.effectivePluginsChanged)
}

func (r *RuntimeRouter) handleGetAccount(request *Request) (*auth.GetAccountResponse, error) {
	var params auth.GetAccountParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	codexHome := r.codexHomeForRollout()
	var snapshot *auth.AuthDotJSON
	if resolved, err := r.resolveAuthWithLoginRestrictions(codexHome); err == nil && resolved != nil {
		snapshot = &resolved.Auth
	} else if err != nil {
		return nil, err
	}
	if response, ok := r.providerAccountResponse(snapshot); ok {
		return response, nil
	}
	requiresOpenAIAuth := r.requiresOpenAIAuthForStatus()
	if !requiresOpenAIAuth {
		return &auth.GetAccountResponse{RequiresOpenAIAuth: false}, nil
	}
	if snapshot != nil {
		if params.RefreshToken {
			refreshed, _ := r.refreshManagedAuthForStatus(context.Background(), snapshot)
			if refreshed != nil {
				snapshot = refreshed
			}
		}
		if auth.RefreshFailureForAuth(codexHome, snapshot) != nil {
			return &auth.GetAccountResponse{RequiresOpenAIAuth: true}, nil
		}
		if err := r.hydratePersonalAccessTokenAccount(context.Background(), snapshot); err != nil {
			return nil, err
		}
		r.requireAccount().ApplyAuthSnapshot(snapshot)
	}
	response := r.requireAccount().GetAccount(&params)
	response.RequiresOpenAIAuth = true
	// Rust get_account: routing discovery failures surface as internal errors,
	// so a ChatGPT account read is never reported with unresolved routing.
	routing, err := r.workspaceRoutingForAccountRead(context.Background(), r.requiredChatGPTBaseURL(), snapshot)
	if err != nil {
		return nil, err
	}
	response.WorkspaceRouting = routing
	return response, nil
}

// requiredChatGPTBaseURL returns the managed `chatgpt_base_url` requirement the
// workspace routing resolver reconciles against the discovered backend.
func (r *RuntimeRouter) requiredChatGPTBaseURL() string {
	if r == nil || r.services.Config == nil {
		return ""
	}
	requirements := r.requireConfig().Requirements()
	if requirements == nil || requirements.Requirements == nil || requirements.Requirements.ChatgptBaseURL == nil {
		return ""
	}
	return strings.TrimSpace(*requirements.Requirements.ChatgptBaseURL)
}

func (r *RuntimeRouter) providerAccountResponse(snapshot *auth.AuthDotJSON) (*auth.GetAccountResponse, bool) {
	if r == nil || r.services.Config == nil {
		return nil, false
	}
	read, err := r.services.Config.Read(&config.ConfigReadParams{})
	if err != nil || read == nil {
		return nil, false
	}
	providerID := strings.TrimSpace(stringFromMap(read.Config, "model_provider"))
	providerInfo, err := model.ProviderForConfigID(read.Config, providerID, strings.TrimSpace(stringFromMap(read.Config, "openai_base_url")))
	if err != nil || providerInfo == nil {
		return nil, false
	}
	provider := model.CreateRuntimeProviderForID(providerID, *providerInfo, snapshot)
	state, err := provider.AccountState()
	if err != nil || state.RequiresOpenAIAuth {
		return nil, false
	}
	return &auth.GetAccountResponse{
		Account:            authAccountFromProviderAccount(state.Account),
		RequiresOpenAIAuth: false,
	}, true
}

func authAccountFromProviderAccount(account *model.ProviderAccount) *auth.Account {
	if account == nil {
		return nil
	}
	switch account.Type {
	case "api-key":
		return &auth.Account{Type: auth.AccountAPIKey}
	case "chatgpt":
		return &auth.Account{
			Type:     auth.AccountChatGPT,
			Email:    stringPtrIfNotEmpty(account.Email),
			PlanType: auth.PlanType(strings.TrimSpace(account.PlanType)),
		}
	case "amazon-bedrock":
		return &auth.Account{
			Type:                        auth.AccountAmazonBedrock,
			UsesCodexManagedCredentials: bedrockUsesCodexManagedCredentials(account.CredentialSource),
		}
	default:
		return nil
	}
}

func bedrockUsesCodexManagedCredentials(source string) bool {
	switch strings.TrimSpace(source) {
	case "codex-managed", "codexManaged", "apiKey":
		return true
	default:
		return false
	}
}

func (r *RuntimeRouter) hydratePersonalAccessTokenAccount(ctx context.Context, snapshot *auth.AuthDotJSON) error {
	if snapshot == nil || snapshot.Mode() != "personal-access-token" || auth.AccountFromAuth(snapshot) != nil {
		return nil
	}
	metadata, err := auth.LoadPersonalAccessTokenMetadata(ctx, snapshot.PersonalAccessToken)
	if err != nil {
		return err
	}
	snapshot.Tokens = map[string]any{
		"email":                      metadata.Email,
		"chatgpt_user_id":            metadata.ChatGPTUserID,
		"chatgpt_account_id":         metadata.ChatGPTAccountID,
		"chatgpt_plan_type":          metadata.ChatGPTPlanType,
		"chatgpt_account_is_fedramp": metadata.ChatGPTAccountFedRAMP,
	}
	return nil
}

func (r *RuntimeRouter) handleGetAccountRateLimits(request *Request) (*auth.GetAccountRateLimitsResponse, error) {
	if len(request.Params) > 0 {
		var params struct{}
		if err := request.DecodeParams(&params); err != nil {
			return nil, err
		}
	}
	snapshot, err := r.requireCodexBackendAuthForAccountRead("rate limits")
	if err != nil {
		return nil, err
	}
	client, err := r.accountBackendClient(snapshot)
	if err != nil {
		return nil, fmt.Errorf("failed to construct backend client: %w", err)
	}
	response, err := client.GetRateLimitsWithResetCredits(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to fetch codex rate limits: %w", err)
	}
	if response == nil || len(response.RateLimits) == 0 {
		return nil, errors.New("failed to fetch codex rate limits: no snapshots returned")
	}
	rateLimits, byLimitID := accountRateLimitsFromBackend(response.RateLimits)
	var resetCredits *auth.RateLimitResetCreditsSummary
	if response.RateLimitResetCredits != nil {
		resetCredits = &auth.RateLimitResetCreditsSummary{AvailableCount: response.RateLimitResetCredits.AvailableCount}
	}
	r.requireAccount().SetRateLimits(rateLimits, byLimitID, resetCredits)
	ordinary, accountID, upsell := accountBoundRateLimitExtras(response, snapshot)
	r.requireAccount().SetRateLimitExtras(ordinary, accountID, upsell)
	return r.requireAccount().RateLimits(), nil
}

func (r *RuntimeRouter) handleGetAccountTokenUsage(request *Request) (*auth.GetAccountTokenUsageResponse, error) {
	var params auth.GetAccountTokenUsageParams
	if len(request.Params) > 0 {
		if err := request.DecodeParams(&params); err != nil {
			return nil, err
		}
	}
	threadID := ""
	if params.ThreadID != nil {
		threadID = strings.TrimSpace(*params.ThreadID)
	}
	if params.ThreadID != nil && threadID == "" {
		return nil, fmt.Errorf("invalid thread id: thread id is required")
	}
	snapshot, err := r.requireCodexBackendAuthForAccountRead("token usage")
	if err != nil {
		return nil, err
	}
	client, err := r.accountBackendClient(snapshot)
	if err != nil {
		return nil, fmt.Errorf("failed to construct backend client: %w", err)
	}
	timeout := accountTokenUsageFetchTimeout()
	if threadID != "" {
		timeout = threadUsageFetchTimeout()
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if threadID != "" {
		usage, err := client.GetThreadUsage(ctx, threadID)
		if err != nil {
			var statusErr *chatgptapi.HTTPStatusError
			if errors.As(err, &statusErr) && (statusErr.StatusCode == http.StatusForbidden || statusErr.StatusCode == http.StatusNotFound) {
				return &auth.GetAccountTokenUsageResponse{}, nil
			}
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, errors.New("thread usage fetch timed out")
			}
			return nil, fmt.Errorf("failed to fetch thread usage: %w", err)
		}
		return &auth.GetAccountTokenUsageResponse{
			ThreadUsage: accountThreadUsageFromBackend(usage),
		}, nil
	}
	profile, err := client.GetTokenUsageProfile(ctx)
	if errors.Is(err, context.DeadlineExceeded) {
		return nil, errors.New("token usage profile fetch timed out")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to fetch token usage profile: %w", err)
	}
	usage := accountTokenUsageFromBackend(profile)
	r.requireAccount().SetTokenUsage(usage)
	return r.requireAccount().TokenUsage(), nil
}

func (r *RuntimeRouter) handleGetWorkspaceMessages(request *Request) (*auth.GetWorkspaceMessagesResponse, error) {
	if len(request.Params) > 0 {
		var params struct{}
		if err := request.DecodeParams(&params); err != nil {
			return nil, err
		}
	}
	snapshot, err := r.requireCodexBackendAuthForAccountRead("workspace messages")
	if err != nil {
		return nil, err
	}
	client, err := r.accountBackendClient(snapshot)
	if err != nil {
		return nil, fmt.Errorf("failed to construct backend client: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), accountWorkspaceMessagesFetchTimeout())
	defer cancel()
	messages, err := client.ListWorkspaceMessages(ctx)
	if errors.Is(err, context.DeadlineExceeded) {
		return nil, errors.New("workspace messages fetch timed out")
	}
	if err != nil {
		var statusErr *chatgptapi.HTTPStatusError
		if errors.As(err, &statusErr) && statusErr.IsStatus(http.StatusNotFound) {
			response := auth.GetWorkspaceMessagesResponse{FeatureEnabled: false, Messages: []auth.WorkspaceMessage{}}
			r.requireAccount().SetWorkspaceMessages(response)
			return r.requireAccount().WorkspaceMessages(), nil
		}
		return nil, fmt.Errorf("failed to fetch workspace messages: %w", err)
	}
	response, err := accountWorkspaceMessagesFromBackend(messages, true)
	if err != nil {
		return nil, err
	}
	r.requireAccount().SetWorkspaceMessages(response)
	return r.requireAccount().WorkspaceMessages(), nil
}

func (r *RuntimeRouter) accountBackendClient(snapshot *auth.AuthDotJSON) (*chatgptapi.CloudClient, error) {
	if snapshot == nil {
		return nil, errors.New("account auth is nil")
	}
	if err := r.hydratePersonalAccessTokenAccount(context.Background(), snapshot); err != nil {
		return nil, err
	}
	headers, err := model.AuthHeadersFromAuth(*snapshot)
	if err != nil {
		return nil, err
	}
	return chatgptapi.NewCloudClient(&chatgptapi.CloudClientOptions{
		BaseURL:    r.chatGPTBaseURL(),
		Headers:    headers.Headers,
		HTTPClient: r.accountHTTPClient(),
	}), nil
}

func accountTokenUsageFetchTimeout() time.Duration {
	return 10 * time.Second
}

func threadUsageFetchTimeout() time.Duration {
	return 60 * time.Second
}

func accountWorkspaceMessagesFetchTimeout() time.Duration {
	return time.Second
}

func (r *RuntimeRouter) requireCodexBackendAuthForAccountRead(resource string) (*auth.AuthDotJSON, error) {
	codexHome := r.codexHomeForRollout()
	resolved, err := r.resolveAuthWithLoginRestrictions(codexHome)
	if err != nil {
		return nil, err
	}
	if resolved == nil {
		return nil, invalidAccountRequest("codex account authentication required to read " + resource)
	}
	snapshot := &resolved.Auth
	if !authUsesCodexBackend(snapshot) {
		return nil, invalidAccountRequest("chatgpt authentication required to read " + resource)
	}
	return snapshot, nil
}

func authUsesCodexBackend(snapshot *auth.AuthDotJSON) bool {
	if snapshot == nil {
		return false
	}
	switch snapshot.Mode() {
	case "chatgpt", "chatgptAuthTokens", "personal-access-token", "agent-identity":
		return true
	default:
		return false
	}
}

func accountRateLimitsFromBackend(snapshots []chatgptapi.RateLimitSnapshot) (auth.RateLimitSnapshot, map[string]auth.RateLimitSnapshot) {
	byLimitID := make(map[string]auth.RateLimitSnapshot, len(snapshots))
	var selected auth.RateLimitSnapshot
	for index := range snapshots {
		converted := accountRateLimitFromBackend(&snapshots[index])
		limitID := "codex"
		if converted.LimitID != nil && strings.TrimSpace(*converted.LimitID) != "" {
			limitID = strings.TrimSpace(*converted.LimitID)
		}
		byLimitID[limitID] = converted
		if selected == (auth.RateLimitSnapshot{}) || limitID == "codex" {
			selected = converted
		}
	}
	return selected, byLimitID
}

func accountRateLimitFromBackend(snapshot *chatgptapi.RateLimitSnapshot) auth.RateLimitSnapshot {
	if snapshot == nil {
		return auth.RateLimitSnapshot{}
	}
	return auth.RateLimitSnapshot{
		LimitID:              cloneStringPtrAppserver(snapshot.LimitID),
		LimitName:            cloneStringPtrAppserver(snapshot.LimitName),
		Primary:              accountRateLimitWindowFromBackend(snapshot.Primary),
		Secondary:            accountRateLimitWindowFromBackend(snapshot.Secondary),
		Credits:              accountCreditsFromBackend(snapshot.Credits),
		IndividualLimit:      accountSpendLimitFromBackend(snapshot.IndividualLimit),
		PlanType:             accountPlanTypeFromBackend(snapshot.PlanType),
		RateLimitReachedType: accountRateLimitReachedTypeFromBackend(snapshot.RateLimitReachedType),
	}
}

func accountRateLimitWindowFromBackend(window *chatgptapi.RateLimitWindow) *auth.RateLimitWindow {
	if window == nil {
		return nil
	}
	return &auth.RateLimitWindow{
		UsedPercent:        roundedRateLimitPercent(window.UsedPercent),
		WindowDurationMins: cloneInt64PtrAppserver(window.WindowDurationMins),
		ResetsAt:           cloneInt64PtrAppserver(window.ResetsAt),
	}
}

func roundedRateLimitPercent(value float64) int32 {
	if math.IsNaN(value) {
		return 0
	}
	rounded := math.Round(value)
	if rounded > math.MaxInt32 {
		return math.MaxInt32
	}
	if rounded < math.MinInt32 {
		return math.MinInt32
	}
	return int32(rounded)
}

func accountCreditsFromBackend(credits *chatgptapi.CreditsSnapshot) *auth.CreditsSnapshot {
	if credits == nil {
		return nil
	}
	return &auth.CreditsSnapshot{
		HasCredits: credits.HasCredits,
		Unlimited:  credits.Unlimited,
		Balance:    cloneStringPtrAppserver(credits.Balance),
	}
}

func accountSpendLimitFromBackend(limit *chatgptapi.SpendControlLimitSnapshot) *auth.SpendControlLimitSnapshot {
	if limit == nil {
		return nil
	}
	return &auth.SpendControlLimitSnapshot{
		Limit:            limit.Limit,
		Used:             limit.Used,
		RemainingPercent: limit.RemainingPercent,
		ResetsAt:         limit.ResetsAt,
	}
}

func accountPlanTypeFromBackend(plan chatgptapi.PlanType) *auth.PlanType {
	switch plan {
	case chatgptapi.PlanFree:
		value := auth.PlanFree
		return &value
	case chatgptapi.PlanGo:
		value := auth.PlanGo
		return &value
	case chatgptapi.PlanPlus:
		value := auth.PlanPlus
		return &value
	case chatgptapi.PlanPro:
		value := auth.PlanPro
		return &value
	case chatgptapi.PlanProLite:
		value := auth.PlanProlite
		return &value
	case chatgptapi.PlanTeam:
		value := auth.PlanTeam
		return &value
	case chatgptapi.PlanSelfServeBusinessUsageBased:
		value := auth.PlanSelfServeBusinessUsageBased
		return &value
	case chatgptapi.PlanBusiness:
		value := auth.PlanBusiness
		return &value
	case chatgptapi.PlanEnt26:
		value := auth.PlanEnt26
		return &value
	case chatgptapi.PlanEnterpriseCbpAutomation:
		value := auth.PlanEnterpriseCBPAutomation
		return &value
	case chatgptapi.PlanEnterpriseCbpUsageBased:
		value := auth.PlanEnterpriseCBPUsageBased
		return &value
	case chatgptapi.PlanEnterprise:
		value := auth.PlanEnterprise
		return &value
	case chatgptapi.PlanEdu, chatgptapi.PlanEducation:
		value := auth.PlanEdu
		return &value
	case chatgptapi.PlanEduPlus:
		value := auth.PlanEduPlus
		return &value
	case chatgptapi.PlanEduPro:
		value := auth.PlanEduPro
		return &value
	default:
		value := auth.PlanUnknown
		return &value
	}
}

func accountRateLimitReachedTypeFromBackend(kind *chatgptapi.RateLimitReachedKind) *auth.RateLimitReachedType {
	if kind == nil {
		return nil
	}
	switch *kind {
	case chatgptapi.RateLimitReached:
		value := auth.RateLimitReached
		return &value
	case chatgptapi.WorkspaceOwnerCreditsDepleted:
		value := auth.WorkspaceOwnerCreditsDepleted
		return &value
	case chatgptapi.WorkspaceMemberCreditsDepleted:
		value := auth.WorkspaceMemberCreditsDepleted
		return &value
	case chatgptapi.WorkspaceOwnerUsageLimitReached:
		value := auth.WorkspaceOwnerUsageLimitReached
		return &value
	case chatgptapi.WorkspaceMemberUsageLimitReached:
		value := auth.WorkspaceMemberUsageLimitReached
		return &value
	default:
		return nil
	}
}

func cloneInt64PtrAppserver(value *int64) *int64 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func accountTokenUsageFromBackend(profile *chatgptapi.TokenUsageProfile) auth.GetAccountTokenUsageResponse {
	if profile == nil {
		return auth.GetAccountTokenUsageResponse{}
	}
	response := auth.GetAccountTokenUsageResponse{
		Summary: auth.AccountTokenUsageSummary{
			LifetimeTokens:        cloneInt64PtrAppserver(profile.Stats.LifetimeTokens),
			PeakDailyTokens:       cloneInt64PtrAppserver(profile.Stats.PeakDailyTokens),
			LongestRunningTurnSec: cloneInt64PtrAppserver(profile.Stats.LongestRunningTurnSec),
			CurrentStreakDays:     cloneInt64PtrAppserver(profile.Stats.CurrentStreakDays),
			LongestStreakDays:     cloneInt64PtrAppserver(profile.Stats.LongestStreakDays),
		},
	}
	if profile.Stats.DailyUsageBuckets != nil {
		response.DailyUsageBuckets = make([]auth.AccountTokenUsageDailyBucket, 0, len(*profile.Stats.DailyUsageBuckets))
		for _, bucket := range *profile.Stats.DailyUsageBuckets {
			response.DailyUsageBuckets = append(response.DailyUsageBuckets, auth.AccountTokenUsageDailyBucket{
				StartDate: bucket.StartDate,
				Tokens:    bucket.Tokens,
			})
		}
	}
	return response
}

func accountWorkspaceMessagesFromBackend(response *chatgptapi.WorkspaceMessagesResponse, featureEnabled bool) (auth.GetWorkspaceMessagesResponse, error) {
	out := auth.GetWorkspaceMessagesResponse{FeatureEnabled: featureEnabled, Messages: []auth.WorkspaceMessage{}}
	if response == nil {
		return out, nil
	}
	for _, message := range response.Messages {
		createdAt, err := workspaceMessageTimestamp(message.CreatedAt)
		if err != nil {
			return auth.GetWorkspaceMessagesResponse{}, err
		}
		archivedAt, err := workspaceMessageTimestamp(message.ArchivedAt)
		if err != nil {
			return auth.GetWorkspaceMessagesResponse{}, err
		}
		out.Messages = append(out.Messages, auth.WorkspaceMessage{
			MessageID:   message.MessageID,
			MessageType: workspaceMessageTypeFromBackend(message.MessageType),
			MessageBody: message.MessageBody,
			CreatedAt:   createdAt,
			ArchivedAt:  archivedAt,
		})
	}
	return out, nil
}

func workspaceMessageTimestamp(value *string) (*int64, error) {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(*value))
	if err != nil {
		return nil, fmt.Errorf("failed to parse workspace message timestamp `%s`: %w", strings.TrimSpace(*value), err)
	}
	unix := parsed.Unix()
	return &unix, nil
}

func workspaceMessageTypeFromBackend(value chatgptapi.WorkspaceMessageType) auth.WorkspaceMessageType {
	switch value {
	case chatgptapi.WorkspaceMessageHeadline:
		return auth.WorkspaceMessageHeadline
	case chatgptapi.WorkspaceMessageAnnouncement:
		return auth.WorkspaceMessageAnnouncement
	default:
		return auth.WorkspaceMessageUnknown
	}
}

func cloneStringPtrAppserver(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func (r *RuntimeRouter) handleConsumeResetCredit(request *Request) (*auth.ConsumeRateLimitResetCreditResponse, error) {
	var params auth.ConsumeRateLimitResetCreditParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.IdempotencyKey) == "" {
		return nil, invalidAccountRequest("idempotencyKey must not be empty")
	}
	resolved, err := r.resolveAuthWithLoginRestrictions(r.codexHomeForRollout())
	if err != nil {
		return nil, err
	}
	if resolved == nil {
		return nil, invalidAccountRequest("codex account authentication required for rate limit reset credits")
	}
	if !authUsesCodexBackend(&resolved.Auth) {
		return nil, invalidAccountRequest("chatgpt authentication required for rate limit reset credits")
	}
	client, err := r.accountBackendClient(&resolved.Auth)
	if err != nil {
		return nil, fmt.Errorf("failed to construct backend client: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), rateLimitResetRequestTimeout())
	defer cancel()
	response, err := client.ConsumeRateLimitResetCredit(ctx, params.IdempotencyKey)
	if errors.Is(err, context.DeadlineExceeded) {
		return nil, errors.New("rate limit reset consume timed out")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to consume rate limit reset: %w", err)
	}
	outcome, ok := backendResetCreditOutcome(response)
	if !ok {
		return nil, fmt.Errorf("failed to consume rate limit reset: unsupported outcome %q", responseCodeFromResetCredit(response))
	}
	return &auth.ConsumeRateLimitResetCreditResponse{Outcome: outcome}, nil
}

func backendResetCreditOutcome(response *chatgptapi.ConsumeRateLimitResetCreditResponse) (auth.ConsumeRateLimitResetCreditOutcome, bool) {
	if response == nil {
		return "", false
	}
	switch response.Code {
	case chatgptapi.ConsumeReset:
		return auth.ResetCreditOutcomeReset, true
	case chatgptapi.ConsumeNothingToReset:
		return auth.ResetCreditOutcomeNothingToReset, true
	case chatgptapi.ConsumeNoCredit:
		return auth.ResetCreditOutcomeNoCredit, true
	case chatgptapi.ConsumeAlreadyRedeemed:
		return auth.ResetCreditOutcomeAlreadyRedeemed, true
	default:
		return "", false
	}
}

func responseCodeFromResetCredit(response *chatgptapi.ConsumeRateLimitResetCreditResponse) chatgptapi.ConsumeRateLimitResetCreditCode {
	if response == nil {
		return ""
	}
	return response.Code
}

func (r *RuntimeRouter) handleSendAddCreditsNudgeEmail(request *Request) (*auth.SendAddCreditsNudgeEmailResponse, error) {
	var params auth.SendAddCreditsNudgeEmailParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	resolved, err := r.resolveAuthWithLoginRestrictions(r.codexHomeForRollout())
	if err != nil {
		return nil, err
	}
	if resolved == nil {
		return nil, invalidAccountRequest("codex account authentication required to notify workspace owner")
	}
	if !authUsesCodexBackend(&resolved.Auth) {
		return nil, invalidAccountRequest("chatgpt authentication required to notify workspace owner")
	}
	client, err := r.accountBackendClient(&resolved.Auth)
	if err != nil {
		return nil, fmt.Errorf("failed to construct backend client: %w", err)
	}
	err = client.SendAddCreditsNudgeEmail(context.Background(), backendCreditType(params.CreditType))
	if err == nil {
		return &auth.SendAddCreditsNudgeEmailResponse{Status: auth.AddCreditsNudgeEmailSent}, nil
	}
	var statusErr *chatgptapi.HTTPStatusError
	if errors.As(err, &statusErr) && statusErr.IsStatus(http.StatusTooManyRequests) {
		return &auth.SendAddCreditsNudgeEmailResponse{Status: auth.AddCreditsNudgeEmailCooldownActive}, nil
	}
	return nil, fmt.Errorf("failed to notify workspace owner: %w", err)
}

func backendCreditType(creditType auth.AddCreditsNudgeCreditType) chatgptapi.AddCreditsNudgeCreditType {
	switch creditType {
	case auth.AddCreditsNudgeUsageLimit:
		return chatgptapi.AddCreditsNudgeUsageLimit
	default:
		return chatgptapi.AddCreditsNudgeCredits
	}
}

func rateLimitResetRequestTimeout() time.Duration {
	const fallback = 10 * time.Second
	value := strings.TrimSpace(os.Getenv("CODEX_TEST_RATE_LIMIT_RESET_REQUEST_TIMEOUT_MS"))
	if value == "" {
		return fallback
	}
	millis, err := strconv.ParseInt(value, 10, 64)
	if err != nil || millis <= 0 {
		return fallback
	}
	return time.Duration(millis) * time.Millisecond
}

func (r *RuntimeRouter) accountHTTPClient() chatgptapi.HTTPDoer {
	if r != nil && r.services.AccountHTTP != nil {
		return r.services.AccountHTTP
	}
	return http.DefaultClient
}

func (r *RuntimeRouter) chatGPTBaseURL() string {
	if r == nil || r.services.Config == nil {
		return config.DefaultChatGPTBaseURL
	}
	read, err := r.services.Config.Read(&config.ConfigReadParams{})
	if err != nil || read == nil {
		return config.DefaultChatGPTBaseURL
	}
	cfg := &config.Config{Values: read.Config}
	return cfg.ChatGPTBaseURL()
}

type accountRequestError struct {
	message string
}

func invalidAccountRequest(message string) error {
	return &accountRequestError{message: message}
}

func (e *accountRequestError) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

func (e *accountRequestError) Unwrap() error {
	return ErrJSONRPCInvalidRequest
}

func (r *RuntimeRouter) handleProcessSpawn(request *Request) (*ProcessSpawnResponse, error) {
	var params ProcessSpawnParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := r.ensureLocalEnvironment(); err != nil {
		return nil, err
	}
	processOptions := &ProcessSpawnOptions{ConnectionID: request.normalizedConnectionID()}
	if r != nil && r.services.Config != nil {
		// Rust c9c6c0daa9: the active feature configuration is authoritative over
		// client-provided environment values.
		processOptions.ApplyPatchPreserveLineEndings = r.applyPatchPreserveLineEndingsFromConfig()
	}
	return r.requireProcesses().SpawnWithOptions(nil, &params, r.notify, processOptions)
}

func (r *RuntimeRouter) handleProcessWriteStdin(request *Request) (*ProcessWriteStdinResponse, error) {
	var params ProcessWriteStdinParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	return r.requireProcesses().WriteStdinWithConnection(request.normalizedConnectionID(), &params)
}

func (r *RuntimeRouter) handleProcessKill(request *Request) (*ProcessKillResponse, error) {
	var params ProcessKillParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	return r.requireProcesses().KillWithConnection(request.normalizedConnectionID(), &params)
}

func (r *RuntimeRouter) handleProcessResizePty(request *Request) (*ProcessResizePtyResponse, error) {
	var params ProcessResizePtyParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	return r.requireProcesses().ResizeWithConnection(request.normalizedConnectionID(), &params)
}

func (r *RuntimeRouter) applyPatchPreserveLineEndingsFromConfig() bool {
	if r == nil || r.services.Config == nil {
		return false
	}
	read, err := r.services.Config.Read(&config.ConfigReadParams{})
	if err != nil || read == nil {
		return false
	}
	cfg := &config.Config{Values: read.Config}
	return features.Enabled(cfg.FeatureSettings(), "apply_patch_preserve_line_endings")
}

func (r *RuntimeRouter) handleCommandExec(request *Request) (*CommandExecResponse, error) {
	var params CommandExecParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := r.ensureLocalEnvironment(); err != nil {
		return nil, err
	}
	options := &CommandExecOptions{ConnectionID: request.normalizedConnectionID()}
	if r != nil && r.services.Config != nil {
		options.PermissionProfileResolver = r.commandExecPermissionProfileResolver()
		if requirements := r.services.Config.Requirements(); requirements != nil {
			options.PermissionRequirements = requirements.Requirements
		}
		// Rust c9c6c0daa9: the active feature configuration is authoritative over
		// client-provided environment values.
		options.ApplyPatchPreserveLineEndings = r.applyPatchPreserveLineEndingsFromConfig()
		managedDenyRead, denyReadErr := r.commandExecManagedDenyReadEntries()
		if denyReadErr != nil {
			// Rust #44669: invalid managed denials fail the request instead of
			// being silently skipped.
			return nil, jsonRPCInvalidRequest(denyReadErr.Error())
		}
		options.ManagedDenyReadEntries = managedDenyRead
	}
	return r.requireCommandExec().ExecuteWithOptions(nil, &params, r.services.DefaultCWD, r.notify, options)
}

// commandExecManagedDenyReadEntries returns the effective config's managed
// requirements `[permissions.filesystem] deny_read` rules so request-specific
// command/exec sandbox policies cannot weaken them (#40004).
func (r *RuntimeRouter) commandExecManagedDenyReadEntries() ([]sandbox.FileSystemSandboxEntry, error) {
	if r == nil || r.services.Config == nil {
		return nil, nil
	}
	response := r.services.Config.Requirements()
	if response == nil || response.Requirements == nil || response.Requirements.Permissions == nil {
		return nil, nil
	}
	return managedDenyReadEntriesFromPerms(response.Requirements.Permissions)
}

func managedDenyReadEntriesFromPerms(perms map[string]any) ([]sandbox.FileSystemSandboxEntry, error) {
	filesystem, ok := perms["filesystem"].(map[string]any)
	if !ok {
		return nil, nil
	}
	raw, ok := filesystem["deny_read"]
	if !ok {
		return nil, nil
	}
	patterns, err := config.FilesystemDenyReadPatterns(raw)
	if err != nil {
		return nil, err
	}
	out := []sandbox.FileSystemSandboxEntry(nil)
	for _, pattern := range patterns {
		path, err := sandbox.ParseDenyReadPath(pattern)
		if err != nil {
			return nil, err
		}
		out = append(out, sandbox.FileSystemSandboxEntry{
			Path:   path,
			Access: sandbox.FileSystemAccessDeny,
		})
	}
	return out, nil
}

func (r *RuntimeRouter) commandExecPermissionProfileResolver() CommandExecPermissionProfileResolver {
	return func(profileID string, cwd string) (*CommandExecPermissionProfileResolution, error) {
		if r == nil || r.services.Config == nil {
			return commandExecBuiltinPermissionProfileResolver(profileID, cwd)
		}
		readParams := &config.ConfigReadParams{}
		if strings.TrimSpace(cwd) != "" {
			readParams.CWD = &cwd
		}
		read, err := r.services.Config.Read(readParams)
		if err != nil {
			return nil, err
		}
		values := map[string]any{}
		if read != nil && read.Config != nil {
			values = read.Config
		}
		resolved, err := (&config.Config{Values: values}).ResolveSandboxPermissionProfile(profileID, cwd)
		if err != nil {
			return nil, jsonRPCInvalidRequest(err.Error())
		}
		if resolved == nil || resolved.Profile == nil {
			return nil, jsonRPCInvalidRequest(fmt.Sprintf("permission profile %q not found", strings.TrimSpace(profileID)))
		}
		return &CommandExecPermissionProfileResolution{ID: resolved.ID, Profile: resolved.Profile, ProfileJSON: resolved.ProfileJSON}, nil
	}
}

func (r *RuntimeRouter) handleCommandExecWrite(request *Request) (*CommandExecWriteResponse, error) {
	var params CommandExecWriteParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	return r.requireCommandExec().WriteWithConnection(request.normalizedConnectionID(), &params)
}

func (r *RuntimeRouter) handleCommandExecTerminate(request *Request) (*CommandExecTerminateResponse, error) {
	var params CommandExecTerminateParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	return r.requireCommandExec().TerminateWithConnection(request.normalizedConnectionID(), &params)
}

func (r *RuntimeRouter) handleCommandExecResize(request *Request) (*CommandExecResizeResponse, error) {
	var params CommandExecResizeParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	return r.requireCommandExec().ResizeWithConnection(request.normalizedConnectionID(), &params)
}

func (r *RuntimeRouter) requireFS() *FSService {
	if r.services.FS == nil {
		r.services.FS = NewFSService()
	}
	r.configureFSChangedCallback()
	return r.services.FS
}

func (r *RuntimeRouter) localFS() (*FSService, error) {
	if r == nil {
		return nil, ErrLocalFilesystemNotConfigured
	}
	if !r.localEnvironmentEnabled() {
		return nil, ErrLocalFilesystemNotConfigured
	}
	return r.requireFS(), nil
}

func (r *RuntimeRouter) ensureLocalEnvironment() error {
	if r == nil || !r.localEnvironmentEnabled() {
		return ErrLocalEnvironmentNotConfigured
	}
	return nil
}

func (r *RuntimeRouter) localEnvironmentEnabled() bool {
	if r == nil {
		return false
	}
	if r.services.LocalEnvironmentEnabled != nil {
		return *r.services.LocalEnvironmentEnabled
	}
	return !execServerURLDisablesLocalFS(os.Getenv(CodexExecServerURLEnvVar))
}

func execServerURLDisablesLocalFS(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), "none")
}

func (r *RuntimeRouter) requireThreadExtras() *ThreadExtraService {
	if r == nil || r.services.ThreadExtras == nil {
		return NewThreadExtraService()
	}
	return r.services.ThreadExtras
}

func (r *RuntimeRouter) requireRealtime() *realtime.Manager {
	if r.services.Realtime == nil {
		r.services.Realtime = realtime.NewManager()
	}
	return r.services.Realtime
}

func (r *RuntimeRouter) requireRemote() *remotecontrol.Manager {
	if r.services.Remote == nil {
		r.services.Remote = remotecontrol.NewManager("codex", "")
	}
	return r.services.Remote
}

func (r *RuntimeRouter) requireEnvironment() *EnvironmentManager {
	if r.services.Environment == nil {
		r.services.Environment = NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "sh"}, "")
	}
	return r.services.Environment
}

func (r *RuntimeRouter) requireWindows() *sandbox.WindowsManager {
	if r.services.Windows == nil {
		r.services.Windows = sandbox.NewWindowsManager(sandbox.WindowsReadinessNotConfigured)
	}
	return r.services.Windows
}

func (r *RuntimeRouter) requireConfig() *config.ConfigService {
	if r == nil || r.config == nil {
		return config.NewConfigService("")
	}
	return r.config
}

func (r *RuntimeRouter) requireAccount() *auth.AccountManager {
	if r.services.Account == nil {
		r.services.Account = auth.NewAccountManager()
	}
	return r.services.Account
}

func (r *RuntimeRouter) requireHooks() *HookRegistry {
	if r.services.Hooks == nil {
		r.services.Hooks = NewHookRegistry()
	}
	return r.services.Hooks
}

func (r *RuntimeRouter) requireHooksDiscovery() *HookDiscoveryService {
	if r.services.HooksDiscovery == nil {
		codexHome := ""
		if r.services.Config != nil {
			codexHome = r.services.Config.CodexHome()
		}
		r.services.HooksDiscovery = NewHookDiscoveryService(codexHome)
	}
	if r.services.HooksDiscovery.Config == nil {
		r.services.HooksDiscovery.Config = r.services.Config
	}
	return r.services.HooksDiscovery
}

func (r *RuntimeRouter) hookStatesFromConfig() map[string]*HookState {
	if r == nil || r.services.Config == nil {
		return nil
	}
	read, err := r.services.Config.Read(&config.ConfigReadParams{})
	if err != nil || read == nil {
		return nil
	}
	hooksValue, ok := read.Config["hooks"].(map[string]any)
	if !ok {
		return nil
	}
	stateValue, ok := hooksValue["state"].(map[string]any)
	if !ok {
		return nil
	}
	states := make(map[string]*HookState, len(stateValue))
	for key, value := range stateValue {
		stateMap, ok := value.(map[string]any)
		if !ok {
			continue
		}
		state := &HookState{}
		if enabled, ok := stateMap["enabled"].(bool); ok {
			state.Enabled = &enabled
		}
		if hash, ok := stateMap["trusted_hash"].(string); ok {
			state.TrustedHash = &hash
		}
		if hash, ok := stateMap["trustedHash"].(string); ok {
			state.TrustedHash = &hash
		}
		states[key] = state
	}
	return states
}

func (r *RuntimeRouter) bypassHookTrustFromConfig() bool {
	if r == nil || r.services.Config == nil {
		return false
	}
	read, err := r.services.Config.Read(&config.ConfigReadParams{})
	if err != nil || read == nil {
		return false
	}
	value, ok := read.Config["bypass_hook_trust"].(bool)
	return ok && value
}

func (r *RuntimeRouter) configureHookDiscovery() *HookDiscoveryService {
	return r.configureHookDiscoveryForThread("")
}

// configureHookDiscoveryForThread filters plugin hook sources by the thread's
// disabled plugins. An empty thread ID keeps every source (Rust #44655).
func (r *RuntimeRouter) configureHookDiscoveryForThread(threadID string) *HookDiscoveryService {
	disabled := r.threadDisabledPluginIDs(threadID)
	r.servicesMu.Lock()
	base := r.requireHooksDiscovery()
	codexHome := base.CodexHome
	configService := base.Config
	if configService == nil {
		configService = r.services.Config
	}
	var pluginSources []plugin.HookSource
	if r.services.Plugins != nil {
		pluginSources = filterDisabledPluginHookSources(disabled, r.services.Plugins.EnabledHookSources())
	}
	r.servicesMu.Unlock()

	discovery := NewHookDiscoveryService(codexHome)
	discovery.Config = configService
	discovery.States = r.hookStatesFromConfig()
	discovery.BypassTrust = r.bypassHookTrustFromConfig()
	discovery.PluginHookSources = pluginSources
	return discovery
}

// filterDisabledPluginHookSources drops hook sources contributed by disabled
// plugins without changing shared plugin state (Rust #44655).
func filterDisabledPluginHookSources(disabled []string, sources []plugin.HookSource) []plugin.HookSource {
	if len(sources) == 0 || len(disabled) == 0 {
		return sources
	}
	blocked := make(map[string]struct{}, len(disabled))
	for _, id := range disabled {
		if id = strings.TrimSpace(id); id != "" {
			blocked[id] = struct{}{}
		}
	}
	if len(blocked) == 0 {
		return sources
	}
	filtered := make([]plugin.HookSource, 0, len(sources))
	for _, source := range sources {
		if _, ok := blocked[strings.TrimSpace(source.PluginID)]; ok {
			continue
		}
		filtered = append(filtered, source)
	}
	return filtered
}

func (r *RuntimeRouter) hookRunnerConfigured() bool {
	if r == nil {
		return false
	}
	r.servicesMu.Lock()
	defer r.servicesMu.Unlock()
	return r.services.HookRunner != nil
}

func (r *RuntimeRouter) requireHookRunner() *HookRunner {
	r.servicesMu.Lock()
	defer r.servicesMu.Unlock()
	if r.services.HookRunner == nil {
		r.services.HookRunner = NewHookRunner()
	}
	r.services.HookRunner.Notify = r.notify
	// Rust #39296/#39331: mcp_tool hooks execute through the session's shared
	// MCP runtime against the current connection set, without model-tool
	// approval or recursive hook dispatch. Unavailable servers fail
	// immediately instead of starting or reconnecting them.
	r.services.HookRunner.McpToolHookExecutor = r
	return r.services.HookRunner
}

func (r *RuntimeRouter) requireSkills() *SkillsService {
	if r.services.Skills == nil {
		options := &SkillsServiceOptions{}
		if r.services.Config != nil {
			options.Config = r.services.Config
			options.CodexHome = r.services.Config.CodexHome()
			options.IncludeDefaultRoots = strings.TrimSpace(options.CodexHome) != ""
		}
		r.services.Skills = NewSkillsServiceWithOptions(options)
	}
	return r.services.Skills
}

func (r *RuntimeRouter) requirePlugins() *plugin.PluginService {
	if r.services.Plugins == nil {
		r.services.Plugins = plugin.NewPluginService()
	}
	return r.services.Plugins
}

func (r *RuntimeRouter) requireModels() *model.ModelService {
	if r.services.Models == nil {
		r.services.Models = model.NewModelService(nil)
	}
	return r.services.Models
}

func (r *RuntimeRouter) requirePermissions() *sandbox.PermissionProfileService {
	if r.services.Permissions == nil {
		r.services.Permissions = sandbox.NewPermissionProfileService(nil)
	}
	return r.services.Permissions
}

func (r *RuntimeRouter) requireCollaboration() *CollaborationModeService {
	if r.services.Collaboration == nil {
		r.services.Collaboration = NewCollaborationModeService(nil)
	}
	return r.services.Collaboration
}

func (r *RuntimeRouter) requireMCP() *mcp.MCPService {
	if r.services.MCP == nil {
		r.services.MCP = mcp.NewMCPService(nil)
	}
	r.configureMCPService(r.services.MCP)
	return r.services.MCP
}

func (r *RuntimeRouter) configureMCPService(service *mcp.MCPService) {
	if r == nil || service == nil {
		return
	}
	reviewer := r.services.GuardianReviewer
	if reviewer == nil && r.services.Agent != nil {
		reviewer = r.ensureGuardianReviewer(r.services.Agent)
	}
	service.SetElicitationHandler(&appserverMCPElicitationHandler{
		broker:    r.requireServerRequests(),
		reviewer:  reviewer,
		authority: r.currentMCPElicitationAuthority,
		persist:   r.persistMCPToolApprovalAmendment,
		record:    r.recordMCPToolApprovalResolution,
	})
	service.SetProgressHandler(&appserverMCPProgressHandler{notify: r.notify})
	service.SetRootsProvider(mcp.MCPRootsProviderFunc(func(threadID string) []mcp.MCPRoot {
		return r.mcpRootsForThread(threadID)
	}))
	service.SetOAuthLoginCompletionHandler(&appserverMCPOAuthLoginCompletionHandler{
		notify:             r.notify,
		invalidateRuntimes: r.mcpRuntimes.invalidateAll,
	})
	service.SetOpenAIFormElicitationEnabled(r.anyConnectionMCPOpenAIFormElicitation())
	// Opted-in stdio servers receive auth change notifications (Rust #43428).
	service.SetAuthChangeSource(routerMCPAuthChangeSource{router: r})
	if handler, ok := service.ElicitationHandler().(*appserverMCPElicitationHandler); ok && r.anyConnectionMCPStandardFormInput() {
		// Rust 4b0e2a0bff: enable full-access form input only after the
		// session has started so required MCP servers cannot block startup
		// waiting for form input. The Go service is configured per turn start,
		// which is after session startup.
		handler.EnableFullAccessFormInput()
	}
}

func (r *RuntimeRouter) mcpServiceForThread(threadID string, cfg *config.Config) *mcp.MCPService {
	if service, managed := r.managedMCPServiceForThread(threadID, cfg); managed && service != nil {
		return service
	}
	return r.requireMCP()
}

func (r *RuntimeRouter) managedMCPServiceForThread(threadID string, cfg *config.Config) (*mcp.MCPService, bool) {
	threadID = strings.TrimSpace(threadID)
	if r == nil || threadID == "" || r.services.Config == nil || r.mcpRuntimes == nil {
		return nil, false
	}
	if cfg == nil {
		cfg = r.effectiveMCPConfigForThread(threadID)
	}
	if !r.mcpConfigManaged.Load() && !mcpConfigContainsThreadRuntime(cfg) && (r.services.Plugins == nil || len(r.services.Plugins.EnabledMCPServerContributions()) == 0) {
		return nil, false
	}
	authRevision, _ := r.authRevisionSnapshot(context.Background())
	runtimeConfig := func(cfg *config.Config) *mcp.RuntimeConfig {
		values := map[string]any{}
		if cfg != nil && cfg.Values != nil {
			values = cfg.Values
		}
		codexHome := strings.TrimSpace(r.services.Config.CodexHome())
		runtimeAuth := mcp.RuntimeAuthFromSnapshot(r.requireAccount().AuthSnapshot())
		config := r.runtimeMCPConfigForThread(threadID, values, codexHome, runtimeAuth, cfg.Requirements)
		// Rust #39335: attachment-scoped MCP servers are only enabled when
		// their environment is selected and available for the thread.
		config.AvailableEnvironment = append([]string(nil), selectedEnvironmentIDs(r.activeTurnParams(threadID))...)
		r.applyMCPPermissionAuthority(config, threadID, r.currentMCPElicitationAuthority(threadID, "", "").PermissionProfile)
		return config
	}
	service := r.mcpRuntimes.serviceForThread(threadID, cfg, authRevision, func(cfg *config.Config) *mcp.MCPService {
		service := mcp.NewMCPService(runtimeConfig(cfg))
		r.configureMCPService(service)
		return service
	}, func(service *mcp.MCPService, cfg *config.Config) {
		service.ApplyRuntimeConfig(runtimeConfig(cfg))
		r.configureMCPService(service)
	})
	if service == nil {
		return nil, true
	}
	return service, true
}

func (r *RuntimeRouter) prewarmMCPThread(threadID string) {
	threadID = strings.TrimSpace(threadID)
	if r == nil || threadID == "" || r.mcpRuntimes == nil {
		return
	}
	r.mcpRuntimes.schedulePrewarm(threadID, func() {
		service, managed := r.managedMCPServiceForThread(threadID, nil)
		if !managed || service == nil {
			return
		}
		_, err := service.ListStatusChecked(&mcp.MCPListServerStatusParams{
			ThreadID: stringPtrIfNotEmpty(threadID),
			Detail:   &mcp.MCPServerStatusDetail{Mode: mcp.MCPServerStatusDetailToolsAndAuthOnly},
		})
		if err != nil {
			slog.Warn("failed to prewarm thread MCP runtime", "thread_id", threadID, "error", err)
		}
		if !r.mcpRuntimes.isCurrent(threadID, service) {
			_ = service.Close()
		}
	})
}

func (r *RuntimeRouter) prewarmLoadedMCPThreads() {
	if r == nil {
		return
	}
	for _, threadID := range r.requireThreadStatus().LoadedThreadIDs() {
		r.prewarmMCPThread(threadID)
	}
}

func mcpConfigContainsThreadRuntime(cfg *config.Config) bool {
	if cfg == nil || cfg.Values == nil {
		return false
	}
	for _, key := range []string{"mcp_servers", "mcpServers"} {
		if servers, ok := cfg.Values[key].(map[string]any); ok && len(servers) > 0 {
			return true
		}
	}
	return cfg.Requirements != nil && (cfg.Requirements.MCPServers != nil || cfg.Requirements.Plugins != nil)
}

func (r *RuntimeRouter) effectiveMCPConfigForThread(threadID string) *config.Config {
	params := &turn.TurnStartParams{ThreadID: strings.TrimSpace(threadID)}
	if active := r.activeTurnParams(threadID); active != nil {
		params = active
	} else if r != nil && r.services.ThreadRouter != nil && r.services.ThreadRouter.store != nil {
		if record, err := r.threadRecord(session.ThreadID(threadID), true, false); err == nil && record != nil {
			settings := r.threadSettingsForTurn(threadID)
			params.CWD = firstNonEmpty(threadSettingsCWD(settings), strings.TrimSpace(record.Metadata.CWD))
			params.Model = firstNonEmpty(threadSettingsModel(settings), strings.TrimSpace(record.Metadata.Model))
			params.Config = threadRecordConfigOverrides(record)
			if strings.TrimSpace(providerFromTurnStart(params)) == "" && strings.TrimSpace(record.Metadata.ModelProvider) != "" {
				params.Config = mergeTurnConfigOverrides(params.Config, map[string]any{"model_provider": strings.TrimSpace(record.Metadata.ModelProvider)})
			}
		}
	}
	cfg, err := r.effectiveConfigForTurn(params)
	if err != nil || cfg == nil {
		return &config.Config{Values: map[string]any{}}
	}
	return cfg
}

func (r *RuntimeRouter) currentMCPElicitationAuthority(threadID, serverName, connectorID string) mcpElicitationAuthority {
	cfg := r.effectiveMCPConfigForThread(threadID)
	authority := mcpElicitationAuthority{
		ApprovalPolicy:        turnApprovalPolicyForTurn(cfg, nil),
		ApprovalsReviewer:     turnApprovalsReviewerForTurn(cfg, nil),
		AllowsMCPElicitations: granularMCPElicitationsAllowed(cfg, nil),
		// Rust #46066: only the root thread may present an interactive MCP
		// elicitation, so a subagent's request that needs user input is rejected
		// with the handoff guidance.
		AllowUserInteraction: !r.turnThreadIsSubagent(threadID),
	}
	settings := r.threadSettingsForTurn(threadID)
	cwd := ""
	params := &turn.TurnStartParams{ThreadID: strings.TrimSpace(threadID)}
	if settings != nil {
		cwd = strings.TrimSpace(settings.CWD)
		if model := strings.TrimSpace(threadSettingsModel(settings)); model != "" {
			params.Model = model
		}
		if policy, ok := parseTurnApprovalPolicy(settings.ApprovalPolicy); ok {
			authority.ApprovalPolicy = policy
		}
		if reviewer := strings.TrimSpace(settings.ApprovalsReviewer); reviewer != "" {
			authority.ApprovalsReviewer = reviewer
		}
		if settings.ActivePermissionProfile != nil && strings.TrimSpace(*settings.ActivePermissionProfile) != "" {
			params.Permissions = cloneString(settings.ActivePermissionProfile)
		} else if strings.TrimSpace(settings.SandboxPolicy) != "" {
			params.SandboxPolicy = settings.SandboxPolicy
		}
	}
	if active := r.activeTurnParams(threadID); active != nil {
		if cwd == "" {
			cwd = strings.TrimSpace(active.CWD)
		}
		if strings.TrimSpace(params.Model) == "" {
			params.Model = strings.TrimSpace(active.Model)
		}
		if settings == nil || strings.TrimSpace(settings.ApprovalPolicy) == "" {
			authority.ApprovalPolicy = turnApprovalPolicyForTurn(cfg, active)
		}
		if settings == nil || strings.TrimSpace(settings.ApprovalsReviewer) == "" {
			authority.ApprovalsReviewer = turnApprovalsReviewerForTurn(cfg, active)
		}
		authority.AllowsMCPElicitations = granularMCPElicitationsAllowed(cfg, active)
		if params.Permissions == nil && params.SandboxPolicy == nil {
			params.Permissions = cloneString(active.Permissions)
			params.SandboxPolicy = active.SandboxPolicy
		}
	}
	if resolution, err := turnSandboxPermissionProfile(cfg, cwd, params); err == nil && resolution != nil {
		authority.PermissionProfile = resolution.Profile
	}
	// Rust #40728: an MCP server's elicitation uses the authority published for
	// that server, not the thread-wide authority. A published runtime without an
	// entry for this server has no authority for it, so the elicitation is
	// declined (handled by the caller).
	if serverName = strings.TrimSpace(serverName); serverName != "" {
		if service := r.publishedMCPServiceForThread(threadID); service != nil && service.HasPublishedPermissionAuthority() {
			authority.ServerAuthorityPublished = true
			authority.PermissionProfile = nil
			if profile, ok := service.PermissionProfileForServer(serverName); ok {
				authority.PermissionProfile = profile
			}
		}
	}
	// Rust 2230d64464 (#38108): MCP tool call approvals resolve the reviewer
	// per server/per connector from the apps config layers, validated against
	// the managed approvals_reviewer requirements, before falling back to the
	// thread-level reviewer.
	authority.ApprovalsReviewer = mcpApprovalsReviewerForElicitation(cfg, authority.ApprovalsReviewer, serverName, connectorID, params.Model)
	return authority
}

// threadEnvironmentSelections returns the thread's currently selected turn
// environments, preferring the active turn and falling back to the persisted
// selection (Rust session turn_environments()).
func (r *RuntimeRouter) threadEnvironmentSelections(threadID string) []map[string]any {
	if r == nil {
		return nil
	}
	if params := r.activeTurnParams(threadID); params != nil && len(params.Environments) > 0 {
		return params.Environments
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" || r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil {
		return nil
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, false)
	if err != nil || record == nil {
		return nil
	}
	return environmentSelectionsFromAny(record.Metadata.Extra[runtimeEnvironmentSelectionsExtraKey])
}

// applyMCPPermissionAuthority publishes the per-server authority carried by a
// thread's MCP runtime (Rust #40728). Executor-attached servers use the
// authority of their selected environment; the controller-owned Apps server,
// local servers, and selected-plugin servers use the thread authority; a server
// whose authority cannot be resolved gets no entry, so its calls and
// elicitations are rejected rather than inheriting another owner's authority.
func (r *RuntimeRouter) applyMCPPermissionAuthority(runtime *mcp.RuntimeConfig, threadID string, threadProfile *sandbox.PermissionProfile) {
	if r == nil || runtime == nil {
		return
	}
	if threadProfile == nil {
		// Go's effective default sandbox when no profile is configured is
		// workspace-write. Publish it so authority resolution never fails closed
		// for a server that merely inherits the thread authority.
		defaultProfile := sandbox.WorkspaceWritePermissionProfile()
		threadProfile = &defaultProfile
	}
	runtime.PermissionProfile = threadProfile
	selections := r.threadEnvironmentSelections(threadID)
	environmentProfiles := map[string]*sandbox.PermissionProfile{}
	for _, selection := range selections {
		environmentID := selectionEnvironmentID(selection)
		if environmentID == "" {
			continue
		}
		state, err := environmentConfigStateFromAnyMap(selection)
		if err != nil || state.Kind != EnvironmentConfigReady {
			continue
		}
		if profile := environmentConfigPermissionProfile(state.Config); profile != nil {
			environmentProfiles[environmentID] = profile
		}
	}
	// Go keeps attachment-scoped servers usable before any environment is
	// selected (see mcpServerEnvironmentAvailable), so pre-attachment servers
	// inherit the thread authority instead of failing closed. Rust does not
	// offer those servers at all, so there is no Rust behavior to match here.
	if len(selections) == 0 && runtime.PermissionProfile != nil {
		for _, registration := range runtime.Servers {
			if !registration.Config.Enabled {
				continue
			}
			environmentID := registration.Config.EffectiveEnvironmentID()
			if environmentID == "" {
				continue
			}
			if _, ok := environmentProfiles[environmentID]; !ok {
				environmentProfiles[environmentID] = runtime.PermissionProfile
			}
		}
	}
	runtime.SetServerPermissionProfiles(environmentProfiles)
}

// publishedMCPServiceForThread returns the thread's current MCP runtime without
// building or refreshing it.
func (r *RuntimeRouter) publishedMCPServiceForThread(threadID string) *mcp.MCPService {
	if r == nil || r.mcpRuntimes == nil {
		return nil
	}
	return r.mcpRuntimes.publishedService(threadID)
}

// mcpApprovalsReviewerForElicitation mirrors Rust's
// mcp_approvals_reviewer_from_layers (codex-rs/core/src/connectors.rs): a
// model listed in auto_review.required_on_models always uses auto_review; for
// the codex-apps MCP server the connector-level approvals_reviewer (then the
// apps default) is used when the managed requirements permit it; otherwise the
// thread-level reviewer applies.
func mcpApprovalsReviewerForElicitation(cfg *config.Config, defaultReviewer, serverName, connectorID, model string) string {
	defaultReviewer = strings.TrimSpace(defaultReviewer)
	if defaultReviewer == "" {
		defaultReviewer = "user"
	}
	if autoReviewRequiredForModel(cfg, model) {
		return string(config.ApprovalsReviewerAutoReview)
	}
	if !mcp.IsCodexAppsMCPServerName(serverName) {
		return defaultReviewer
	}
	reviewer := connectorApprovalsReviewerFromConfig(cfg, connectorID)
	if reviewer == "" {
		return defaultReviewer
	}
	if !approvalsReviewerAllowedByRequirements(cfg, reviewer) {
		return defaultReviewer
	}
	return reviewer
}

// connectorApprovalsReviewerFromConfig reads the connector-level
// approvals_reviewer from the effective apps config (connector-specific entry,
// then the `_default` entry). Mirrors Rust apps_config_from_layer_stack.
func connectorApprovalsReviewerFromConfig(cfg *config.Config, connectorID string) string {
	if cfg == nil || cfg.Values == nil {
		return ""
	}
	raw, ok := cfg.Values["apps"].(map[string]any)
	if !ok {
		return ""
	}
	connectorID = strings.TrimSpace(connectorID)
	if connectorID != "" {
		if appValues, ok := raw[connectorID].(map[string]any); ok {
			if reviewer := firstNonEmpty(stringFromMap(appValues, "approvals_reviewer"), stringFromMap(appValues, "approvalsReviewer")); reviewer != "" {
				return reviewer
			}
		}
	}
	if defaults, ok := raw["_default"].(map[string]any); ok {
		return firstNonEmpty(stringFromMap(defaults, "approvals_reviewer"), stringFromMap(defaults, "approvalsReviewer"))
	}
	return ""
}

// approvalsReviewerAllowedByRequirements reports whether the candidate
// approvals_reviewer satisfies the managed allowed_approvals_reviewers
// requirements (Rust requirements.approvals_reviewer.can_set).
func approvalsReviewerAllowedByRequirements(cfg *config.Config, reviewer string) bool {
	reviewer = strings.TrimSpace(reviewer)
	if reviewer == "" {
		return false
	}
	if cfg == nil || cfg.Requirements == nil || len(cfg.Requirements.AllowedApprovalsReviewers) == 0 {
		return true
	}
	for _, allowed := range cfg.Requirements.AllowedApprovalsReviewers {
		if string(allowed) == reviewer {
			return true
		}
	}
	return false
}

// persistMCPToolApprovalAmendment applies the persistent MCP tool approval
// policy amendment chosen through "Allow and don't ask me again"
// (Rust maybe_persist_mcp_tool_approval in mcp_tool_call.rs). For the
// codex-apps server it writes apps.<connector_id>.tools.<tool>.approval_mode;
// for custom MCP servers it writes mcp_servers.<server>.tools.<tool>
// .approval_mode in the user config.
func (r *RuntimeRouter) persistMCPToolApprovalAmendment(request *mcp.MCPElicitationRequest, response *MCPElicitationRequestResponse) error {
	if r == nil || r.services.Config == nil || request == nil {
		return nil
	}
	meta, ok := requestMetaMap(request)
	if !ok {
		return fmt.Errorf("MCP tool approval metadata is missing")
	}
	toolName := strings.TrimSpace(stringFromMap(meta, "tool_name"))
	if toolName == "" {
		return fmt.Errorf("MCP tool approval metadata must include a non-empty tool_name")
	}
	serverName := mcpElicitationServerName(request)
	if serverName == "" {
		serverName = strings.TrimSpace(request.ServerName)
	}
	if serverName == "" {
		return fmt.Errorf("MCP tool approval is missing the server name")
	}
	keyPath := ""
	connectorID := strings.TrimSpace(stringFromMap(meta, "connector_id"))
	keyPath = mcpToolApprovalKeyPath(serverName, connectorID, r.mcpServerPluginID(serverName), toolName)
	if keyPath == "" {
		return fmt.Errorf("codex-apps MCP tool approval is missing connector_id")
	}
	_, err := r.services.Config.BatchWrite(&config.ConfigBatchWriteParams{
		Edits: []config.ConfigEdit{{
			KeyPath: keyPath,
			Value:   "approve",
		}},
	})
	return err
}

// mcpToolApprovalKeyPath mirrors Rust's maybe_persist_mcp_tool_approval target:
// codex-apps approvals are connector-scoped, plugin-contributed servers persist
// under the plugin's own config section (Rust #44655), and every other server
// uses the global mcp_servers section. An empty result means the request is
// missing required metadata.
func mcpToolApprovalKeyPath(serverName string, connectorID string, pluginID string, toolName string) string {
	if mcp.IsCodexAppsMCPServerName(serverName) {
		if strings.TrimSpace(connectorID) == "" {
			return ""
		}
		return "apps." + configKeyPathSegment(connectorID) + ".tools." + configKeyPathSegment(toolName) + ".approval_mode"
	}
	if pluginID = strings.TrimSpace(pluginID); pluginID != "" {
		return "plugins." + configKeyPathSegment(pluginID) + ".mcp_servers." + configKeyPathSegment(serverName) + ".tools." + configKeyPathSegment(toolName) + ".approval_mode"
	}
	return "mcp_servers." + configKeyPathSegment(serverName) + ".tools." + configKeyPathSegment(toolName) + ".approval_mode"
}

// mcpServerPluginID returns the plugin that contributes serverName, if any, so a
// plugin server's approval lands in the plugin's config section.
func (r *RuntimeRouter) mcpServerPluginID(serverName string) string {
	serverName = strings.TrimSpace(serverName)
	if r == nil || r.services.Plugins == nil || serverName == "" {
		return ""
	}
	for _, contribution := range r.services.Plugins.EnabledMCPServerContributions() {
		if strings.TrimSpace(contribution.Name) == serverName {
			return strings.TrimSpace(contribution.PluginID)
		}
	}
	return ""
}

// configKeyPathSegment escapes a config key path segment (quotes and dots) so
// it can be embedded in a dotted ConfigService key path.
func configKeyPathSegment(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, `"`, `\"`)
	if strings.ContainsAny(value, ".\"'") {
		return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
	}
	return value
}

// recordMCPToolApprovalResolution records the unified approval
// resolution-source telemetry for an MCP tool call approval resolved by the
// user through the client (Rust record_resolution in approvals.rs). Guardian
// resolutions are recorded through the guardian review analytics path instead;
// permission-hook resolutions are recorded through hook-run analytics.
func (r *RuntimeRouter) recordMCPToolApprovalResolution(request *mcp.MCPElicitationRequest, response *MCPElicitationRequestResponse) {
	if r == nil || request == nil || response == nil {
		return
	}
	if mcpElicitationApprovalKind(request) != "mcp_tool_call" {
		return
	}
	itemID := appserverMCPElicitationID(request)
	if itemID == "" {
		return
	}
	threadID := strings.TrimSpace(request.ThreadID)
	turnID := strings.TrimSpace(request.TurnID)
	if threadID == "" || turnID == "" {
		return
	}
	outcome := mcpToolApprovalFinalOutcome(response.Action)
	r.recordToolItemReviewSummary(threadID, turnID, itemID, toolItemReviewSummary{
		ReviewCount:          1,
		UserReviewCount:      1,
		FinalApprovalOutcome: outcome,
	})
}

func mcpToolApprovalFinalOutcome(action MCPElicitationAction) string {
	switch action {
	case MCPElicitationActionAccept:
		return telemetry.FinalApprovalOutcomeUserApproved
	case MCPElicitationActionDecline:
		return telemetry.FinalApprovalOutcomeUserDenied
	default:
		return telemetry.FinalApprovalOutcomeUserAborted
	}
}

func granularMCPElicitationsAllowed(cfg *config.Config, params *turn.TurnStartParams) bool {
	raw := any(nil)
	if cfg != nil && cfg.Values != nil {
		raw = cfg.Values["approval_policy"]
	}
	if params != nil && params.ApprovalPolicy != nil {
		raw = params.ApprovalPolicy
	}
	return granularMCPApprovalValue(raw)
}

func granularMCPApprovalValue(raw any) bool {
	values, ok := raw.(map[string]any)
	if !ok {
		return false
	}
	if nested, ok := values["granular"].(map[string]any); ok {
		values = nested
	}
	for _, key := range []string{"mcp_elicitations", "mcpElicitations"} {
		if allowed, ok := values[key].(bool); ok {
			return allowed
		}
	}
	return false
}

func (r *RuntimeRouter) mcpRootsForThread(threadID string) []mcp.MCPRoot {
	paths := r.mcpRootPathsForThread(threadID)
	if len(paths) == 0 {
		return nil
	}
	roots := make([]mcp.MCPRoot, 0, len(paths))
	for _, path := range paths {
		root := mcp.NewMCPFileRoot(path)
		if root != nil {
			roots = append(roots, *root)
		}
	}
	return roots
}

func (r *RuntimeRouter) mcpRootPathsForThread(threadID string) []string {
	selected := r.selectedCapabilityMCPRootPaths(threadID)
	if params := r.activeTurnParams(threadID); params != nil {
		paths := normalizedMCPRootPaths(r, params.RuntimeWorkspaceRoots)
		if len(paths) == 0 {
			paths = normalizedMCPRootPaths(r, []string{params.CWD})
		}
		if len(paths) > 0 {
			return mergeMCPRootPaths(paths, selected)
		}
	}
	threadID = strings.TrimSpace(threadID)
	if threadID != "" && r != nil && r.services.ThreadRouter != nil && r.services.ThreadRouter.store != nil {
		if record, err := r.threadRecord(session.ThreadID(threadID), true, false); err == nil && record != nil {
			paths := normalizedMCPRootPaths(r, stringSliceFromAny(record.Metadata.Extra["runtime_workspace_roots"]))
			if len(paths) == 0 {
				paths = normalizedMCPRootPaths(r, []string{record.Metadata.CWD})
			}
			if len(paths) > 0 {
				return mergeMCPRootPaths(paths, selected)
			}
		}
	}
	if r != nil {
		return mergeMCPRootPaths(normalizedMCPRootPaths(r, []string{r.services.DefaultCWD}), selected)
	}
	return selected
}

func (r *RuntimeRouter) selectedCapabilityMCPRootPaths(threadID string) []string {
	threadID = strings.TrimSpace(threadID)
	if r == nil || threadID == "" || r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil {
		return nil
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, false)
	if err != nil || record == nil {
		return nil
	}
	// Rust b43de77679 (#38067): merge persisted thread roots with the ready
	// attachment roots installed by environment readiness reports, keeping
	// thread roots first and hiding attachments that are not ready yet.
	status := r.inspectSelectedCapabilityRootsForThread(record)
	paths := make([]string, 0, len(status.ReadyRoots))
	for _, selected := range status.ReadyRoots {
		if selected.Location.Type != CapabilityRootLocationEnvironment {
			continue
		}
		environmentID := strings.TrimSpace(selected.Location.EnvironmentID)
		if environmentID != "" && environmentID != "local" {
			// Remote environment paths are not native paths for the primary MCP client,
			// regardless of whether the environment is currently connected.
			continue
		}
		if path := capabilityRootLocalPath(selected.Location.Path); strings.TrimSpace(path) != "" {
			paths = append(paths, path)
		}
	}
	return normalizedMCPRootPaths(r, paths)
}

// inspectSelectedCapabilityRootsForThread returns the merged selected
// capability roots for a thread: persisted thread roots first, then the ready
// attachment roots carried by Ready environment configurations and installed
// by environment readiness reports, deduplicated by root ID (Rust
// ThreadEnvironments::inspect_selected_capability_roots, #38067, #38521).
func (r *RuntimeRouter) inspectSelectedCapabilityRootsForThread(record *session.Record) SelectedCapabilityRootsStatus {
	threadRoots := threadSelectedCapabilityRoots(record)
	attachmentRoots := readyAttachmentRootsFromSelections(record)
	merged := combineSelectedCapabilityRoots(threadRoots, attachmentRoots)
	seen := make(map[string]struct{}, len(merged))
	unique := make([]SelectedCapabilityRoot, 0, len(merged))
	for _, root := range merged {
		if _, dup := seen[root.ID]; dup {
			continue
		}
		seen[root.ID] = struct{}{}
		unique = append(unique, root)
	}
	if r == nil || r.services.Environment == nil {
		return SelectedCapabilityRootsStatus{ReadyRoots: cloneSelectedCapabilityRoots(unique)}
	}
	return r.services.Environment.InspectSelectedCapabilityRoots(unique)
}

// threadSelectedCapabilityRoots decodes the persisted thread capability roots
// from a session record.
func threadSelectedCapabilityRoots(record *session.Record) []SelectedCapabilityRoot {
	if record == nil {
		return nil
	}
	roots := make([]SelectedCapabilityRoot, 0, len(record.Metadata.SelectedCapabilityRoots))
	for _, raw := range record.Metadata.SelectedCapabilityRoots {
		var selected SelectedCapabilityRoot
		if json.Unmarshal(raw, &selected) == nil {
			roots = append(roots, selected)
		}
	}
	return roots
}

// readyAttachmentRootsFromSelections collects the capability roots carried by
// Ready environment configurations persisted on the thread's selections.
// Pending and Failed attachments are excluded (#38684).
func readyAttachmentRootsFromSelections(record *session.Record) []SelectedCapabilityRoot {
	if record == nil || record.Metadata.Extra == nil {
		return nil
	}
	selections := environmentSelectionsFromAny(record.Metadata.Extra[runtimeEnvironmentSelectionsExtraKey])
	var roots []SelectedCapabilityRoot
	for _, selection := range selections {
		state, err := environmentConfigStateFromAnyMap(selection)
		if err != nil || state.Kind != EnvironmentConfigReady || state.Config == nil {
			continue
		}
		roots = append(roots, cloneSelectedCapabilityRoots(state.Config.SelectedCapabilityRoots)...)
	}
	return roots
}

func mergeMCPRootPaths(groups ...[]string) []string {
	var out []string
	seen := map[string]bool{}
	for _, group := range groups {
		for _, path := range group {
			key := strings.ToLower(filepath.Clean(strings.TrimSpace(path)))
			if key == "" || key == "." || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, path)
		}
	}
	return out
}

func (r *RuntimeRouter) activeTurnParams(threadID string) *turn.TurnStartParams {
	if r == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	active := r.threads.ActiveTurn(threadID)
	if active == nil || active.Params == nil {
		return nil
	}
	return cloneTurnStartParams(active.Params)
}

func (r *RuntimeRouter) activeRuntimeTurnSnapshot(threadID string) *Turn {
	return r.activeRuntimeTurnSnapshotWithItems(threadID, true)
}

// activeRuntimeTurnSnapshotMetadata returns the running turn without
// materializing or cloning its items (Rust #46510
// ThreadHistoryBuilder::active_turn_metadata_snapshot): the turn reports
// TurnItemsNotLoaded, which is what a metadata-only resume needs for its status
// check and its NotLoaded initial page.
func (r *RuntimeRouter) activeRuntimeTurnSnapshotMetadata(threadID string) *Turn {
	return r.activeRuntimeTurnSnapshotWithItems(threadID, false)
}

func (r *RuntimeRouter) activeRuntimeTurnSnapshotWithItems(threadID string, includeItems bool) *Turn {
	if r == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	active := r.threads.ActiveTurn(threadID)
	if active == nil || strings.TrimSpace(active.TurnID) == "" {
		return nil
	}
	turnID := strings.TrimSpace(active.TurnID)
	startedAtMS := active.StartedAtMS
	params := cloneTurnStartParams(active.Params)

	startedAt := time.UnixMilli(startedAtMS).UTC().Unix()
	createdAt := time.UnixMilli(startedAtMS).UTC()
	if startedAtMS == 0 {
		now := time.Now().UTC()
		startedAt = now.Unix()
		createdAt = now
	}
	turn := &Turn{
		ID:        turnID,
		ItemsView: TurnItemsNotLoaded,
		Status:    TurnStatusInProgress,
		StartedAt: &startedAt,
	}
	if !includeItems {
		return turn
	}
	items := []ThreadItem{}
	for _, item := range r.sessionItemsForTurn(turnID, params, nil, createdAt) {
		if sessionItemIsHiddenThreadItem(&item) {
			continue
		}
		items = append(items, BuildThreadItem(item))
	}
	turn.Items = items
	turn.ItemsView = TurnItemsFull
	return turn
}

func normalizedMCPRootPaths(r *RuntimeRouter, values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	base := ""
	if r != nil {
		base = strings.TrimSpace(r.services.DefaultCWD)
	}
	for _, value := range values {
		path := strings.TrimSpace(value)
		if path == "" {
			continue
		}
		if !filepath.IsAbs(path) && base != "" {
			path = filepath.Join(base, path)
		}
		path = filepath.Clean(path)
		if path == "." || seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	return out
}

func (r *RuntimeRouter) requireFeatures() *features.FeatureService {
	if r.services.Features == nil {
		r.services.Features = features.NewFeatureService(nil)
	}
	return r.services.Features
}

func (r *RuntimeRouter) requireApps() *apps.AppService {
	if r.services.Apps == nil {
		r.services.Apps = apps.NewAppService(nil)
	}
	return r.services.Apps
}

func (r *RuntimeRouter) requireTurns() *turn.TurnService {
	if r.services.Turns == nil {
		r.services.Turns = turn.NewTurnService()
	}
	return r.services.Turns
}

func (r *RuntimeRouter) requireSteerMailbox() *turn.SteerMailbox {
	r.servicesMu.Lock()
	defer r.servicesMu.Unlock()
	if r.services.SteerMailbox == nil {
		r.services.SteerMailbox = turn.NewSteerMailbox()
	}
	return r.services.SteerMailbox
}

func (r *RuntimeRouter) agentConfigured() bool {
	if r == nil {
		return false
	}
	r.servicesMu.Lock()
	defer r.servicesMu.Unlock()
	return r.services.Agent != nil
}

func (r *RuntimeRouter) agentSnapshot() model.AgentRunner {
	if r == nil {
		return nil
	}
	r.servicesMu.Lock()
	defer r.servicesMu.Unlock()
	return r.services.Agent
}

func (r *RuntimeRouter) requireAgent() model.AgentRunner {
	r.servicesMu.Lock()
	defer r.servicesMu.Unlock()
	if r.services.Agent == nil {
		r.services.Agent = &model.UnavailableAgentRunner{}
	}
	return r.services.Agent
}

func (r *RuntimeRouter) requireToolRouter(cwd string) (*tool.Router, error) {
	if r.services.ToolRouter != nil {
		return r.services.ToolRouter, nil
	}
	options := turn.DefaultToolRegistryOptions(cwd)
	options.CodexVersion = appServerVersion()
	options.UnifiedExec = r.services.UnifiedExec
	if r.services.CodeModeProvider != nil {
		options.CodeModeProvider = r.services.CodeModeProvider
	}
	options.CodeModeRuntime = r.codeModeRuntimeForThread("__default__")
	options.DisableCodeModeFallback = r.services.DisableCodeModeFallback
	options.EnableMCP = false
	options.EnableAgents = false
	router, err := turn.BuildToolRouter(options)
	if err != nil {
		return nil, err
	}
	r.services.ToolRouter = router
	return router, nil
}

func (r *RuntimeRouter) buildTurnRuntime(params *turn.TurnStartParams, turnID string) (*turn.Runtime, error) {
	return r.buildTurnRuntimeContext(context.Background(), params, turnID)
}

func (r *RuntimeRouter) buildTurnRuntimeContext(ctx context.Context, params *turn.TurnStartParams, turnID string) (*turn.Runtime, error) {
	if r.services.TurnRuntime != nil {
		return r.services.TurnRuntime, nil
	}
	cwd := firstNonEmpty(params.CWD, r.services.DefaultCWD)
	router, err := r.toolRouterForTurnContext(ctx, cwd, params, turnID)
	if err != nil {
		return nil, err
	}
	hooks := r.turnHookAdapter(params, turnID)
	agent := r.agentForAppTurn(params, turnID)
	includeToolInfo := false
	if cfg, err := r.effectiveConfigForTurn(params); err == nil && cfg != nil {
		// Rust features.tool_registry.turn_metadata_includes_tool_info: only a
		// gated turn reports the model-visible tool inventory.
		includeToolInfo = cfg.ToolRegistryTurnMetadataIncludesToolInfo()
	}
	return turn.NewRuntime(&turn.RuntimeOptions{
		Agent:                        agent,
		Router:                       router,
		Hooks:                        hooks,
		SteerMailbox:                 r.requireSteerMailbox(),
		ExecutedToolCalls:            r.executedToolCallRecorder(params.ThreadID),
		TurnMetadataIncludesToolInfo: includeToolInfo,
	}), nil
}

func (r *RuntimeRouter) executedToolCallRecorder(threadID string) *turn.ExecutedToolCallRecorder {
	if r == nil || strings.TrimSpace(threadID) == "" {
		return nil
	}
	r.executedToolCallsMu.Lock()
	defer r.executedToolCallsMu.Unlock()
	if r.executedToolCalls == nil {
		r.executedToolCalls = map[string]*turn.ExecutedToolCallRecorder{}
	}
	recorder := r.executedToolCalls[threadID]
	if recorder == nil {
		recorder = turn.NewExecutedToolCallRecorder()
		r.executedToolCalls[threadID] = recorder
	}
	return recorder
}

func (r *RuntimeRouter) deleteExecutedToolCallRecorder(threadID string) {
	if r == nil || strings.TrimSpace(threadID) == "" {
		return
	}
	r.executedToolCallsMu.Lock()
	delete(r.executedToolCalls, threadID)
	r.executedToolCallsMu.Unlock()
}

func (r *RuntimeRouter) codeModeRuntimeForThread(threadID string) *tool.CodeModeRuntime {
	if r == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		threadID = "__default__"
	}
	r.codeModeRuntimesMu.Lock()
	defer r.codeModeRuntimesMu.Unlock()
	if r.codeModeRuntimes == nil {
		r.codeModeRuntimes = map[string]*tool.CodeModeRuntime{}
	}
	runtime := r.codeModeRuntimes[threadID]
	if runtime == nil {
		runtime = tool.NewCodeModeRuntime(r.services.CodeModeProvider, r.services.DisableCodeModeFallback)
		r.codeModeRuntimes[threadID] = runtime
	}
	return runtime
}

func (r *RuntimeRouter) deleteCodeModeRuntime(threadID string) error {
	if r == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	r.codeModeRuntimesMu.Lock()
	runtime := r.codeModeRuntimes[threadID]
	delete(r.codeModeRuntimes, threadID)
	r.codeModeRuntimesMu.Unlock()
	if runtime != nil {
		return runtime.Close()
	}
	return nil
}

func (r *RuntimeRouter) codeModeInterruptEnabledForThread(threadID string) bool {
	if r == nil || r.services.Config == nil {
		return false
	}
	readParams := &config.ConfigReadParams{}
	cwd := ""
	if record, err := r.threadRecord(session.ThreadID(strings.TrimSpace(threadID)), false, false); err == nil && record != nil {
		cwd = record.Metadata.CWD
	}
	if cwd != "" {
		readParams.CWD = &cwd
	}
	read, err := r.services.Config.Read(readParams)
	if err != nil || read == nil {
		return false
	}
	return features.Enabled((&config.Config{Values: read.Config}).FeatureSettings(), "code_mode_interrupt")
}

func (r *RuntimeRouter) closeCodeModeRuntimes() error {
	if r == nil {
		return nil
	}
	r.codeModeRuntimesMu.Lock()
	runtimes := r.codeModeRuntimes
	r.codeModeRuntimes = map[string]*tool.CodeModeRuntime{}
	r.codeModeRuntimesMu.Unlock()
	var closeErr error
	for _, runtime := range runtimes {
		if runtime != nil {
			if err := runtime.Close(); err != nil && closeErr == nil {
				closeErr = err
			}
		}
	}
	return closeErr
}

func (r *RuntimeRouter) toolRouterForTurn(cwd string, params *turn.TurnStartParams, turnID string) (*tool.Router, error) {
	return r.toolRouterForTurnContext(context.Background(), cwd, params, turnID)
}

func (r *RuntimeRouter) toolRouterForTurnContext(ctx context.Context, cwd string, params *turn.TurnStartParams, turnID string) (*tool.Router, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	threadID := ""
	if params != nil {
		threadID = strings.TrimSpace(params.ThreadID)
	}
	enableCurrentTimeTool := false
	enableSleepTool := false
	var clockProvider tool.ClockProvider
	cfg, err := r.effectiveConfigForTurn(params)
	if err != nil {
		return nil, err
	}
	webSearchOptions, err := r.webSearchOptionsForTurn(cfg, params)
	if err != nil {
		return nil, err
	}
	imageGenerationOptions, err := r.imageGenerationOptionsForTurn(cfg, params)
	if err != nil {
		return nil, err
	}
	viewImageOptions := r.viewImageOptionsForTurn(cfg, params, cwd)
	requestUserInputModes := requestUserInputAvailableModesForTurn(cfg)
	requestUserInputDefaultMode := requestUserInputDefaultModeEnabled(cfg)
	waitForEnvironmentEnabled := cfg != nil && features.Enabled(cfg.FeatureSettings(), "deferred_executor")
	disableUpdatePlan := cfg != nil && !cfg.UpdatePlanEnabled()
	disableWaitAgent := cfg != nil && !cfg.WaitAgentEnabled()
	mcpService := r.mcpServiceForThread(threadID, cfg)
	requiredMCPServers := r.requiredMCPServersForTurn(threadID, cfg, params)
	requiredPlugins := r.requiredPluginsForTurn(threadID, cfg, params)
	candidates, mcpTools, mcpConnectors, err := r.prepareTurnToolInputs(ctx, threadID, cfg, mcpService, requiredMCPServers, requiredPlugins)
	if err != nil {
		return nil, err
	}
	permissionProfile, err := turnSandboxPermissionProfile(cfg, cwd, params)
	if err != nil {
		return nil, err
	}
	managedNetwork, err := r.managedNetworkForTurn(threadID, cwd, cfg, turnConfigOverrides(params))
	if err != nil {
		return nil, err
	}
	// Rust #41243: the sleep_tool feature gates clock.sleep registration via a
	// model_driven / always_on mode, and the current-time tool is registered for
	// a model that declares the clock capability even without the feature flag.
	turnModelInfo := (*model.ModelInfo)(nil)
	if params != nil {
		modelID := firstNonEmpty(strings.TrimSpace(params.Model), stringConfigValue(cfg, "model"))
		turnModelInfo = r.modelInfoForRuntimeWithConfig(modelID, cfg)
	}
	modelHasClock := false
	if turnModelInfo != nil {
		for _, supported := range turnModelInfo.ExperimentalSupportedTools {
			if supported == "clock" {
				modelHasClock = true
				break
			}
		}
	}
	if cfg != nil {
		enableCurrentTimeTool, enableSleepTool = decideClockToolEnablement(cfg, params, modelHasClock)
		if enableCurrentTimeTool {
			if reminder := cfg.CurrentTimeReminder(); reminder != nil && reminder.ClockSource == config.CurrentTimeSourceExternal {
				clockProvider = &appServerClockProvider{router: r}
			}
		}
	}
	var trustedPluginRoots plugin.TrustedPluginRoots
	if r.services.Plugins != nil && r.services.Config != nil {
		installed := r.services.Plugins.Installed(&plugin.PluginInstalledParams{})
		pluginIDs := make([]string, 0, len(installed.Plugins))
		for _, summary := range installed.Plugins {
			if summary.Enabled && summary.Installed && strings.TrimSpace(summary.ID) != "" {
				pluginIDs = append(pluginIDs, summary.ID)
			}
		}
		trustedPluginRoots = plugin.NewTrustedPluginRoots(r.services.Config.CodexHome(), pluginIDs)
	}
	executorSandboxContexts, err := r.executorSkillSandboxContextsForTurn(cfg, cwd, params)
	if err != nil {
		return nil, err
	}
	executorSkillProviders := r.executorSkillProviderForThreadWithSandbox(threadID, executorSandboxContexts)
	goalToolExecutors := r.goalToolExecutorsForTurn(cfg, threadID, strings.TrimSpace(turnID))
	if r != nil && r.services.ToolRouter != nil && viewImageOptions != nil {
		if err := r.services.ToolRouter.RegisterIfAbsent(tool.NewViewImageHandler(*viewImageOptions)); err != nil {
			return nil, err
		}
	}
	if r != nil && r.services.ToolRouter != nil && turn.SupportsLegacyShellCommand(selectedEnvironmentIDs(params)) && executorSkillProviders == nil && !requestUserInputDefaultMode && !waitForEnvironmentEnabled && !enableCurrentTimeTool && !enableSleepTool && !disableUpdatePlan && !disableWaitAgent && webSearchOptions == nil && imageGenerationOptions == nil && len(goalToolExecutors) == 0 && (params == nil || len(params.DynamicTools) == 0) && len(candidates) == 0 && len(mcpTools) == 0 {
		return r.services.ToolRouter, nil
	}
	options := turn.DefaultToolRegistryOptions(cwd)
	options.CodexVersion = appServerVersion()
	if table, ok := cfg.Values["shell_environment_policy"].(map[string]any); ok {
		options.Shell.ShellEnvironmentPolicy = cloneShellEnvironmentPolicy(table)
	}
	// Rust UnifiedExecProcessManager::new(config.background_terminal_max_timeout):
	// the configured background-terminal timeout caps unified-exec yields.
	if r.services.UnifiedExec != nil {
		r.services.UnifiedExec.SetMaxEmptyPollYieldTime(cfg.BackgroundTerminalMaxTimeoutMS())
	}
	options.UnifiedExec = r.services.UnifiedExec
	if r.services.CodeModeProvider != nil {
		options.CodeModeProvider = r.services.CodeModeProvider
	}
	options.CodeModeRuntime = r.codeModeRuntimeForThread(threadID)
	if r.services.Plugins != nil && r.services.Config != nil {
		options.PluginMetricsResolver = func(command []string, commandCWD string) *plugin.ResolvedPluginMetricsOperation {
			return trustedPluginRoots.ResolveMetricsOperation(command, commandCWD)
		}
		options.PluginMeasurementTracker = func(ctx context.Context, batch plugin.PluginMeasurementBatch) {
			client, ok := r.services.Analytics.(*telemetry.AnalyticsEventsClient)
			if !ok {
				return
			}
			rows := make([]telemetry.PluginMeasurementRow, 0, len(batch.Rows))
			for _, row := range batch.Rows {
				rows = append(rows, telemetry.PluginMeasurementRow{
					MeasurementName: row.MeasurementName,
					NumberValue:     row.NumberValue,
					Dimensions:      row.Dimensions,
				})
			}
			originator := ""
			if record, recordErr := r.threadRecord(session.ThreadID(threadID), true, false); recordErr == nil && record != nil {
				originator = strings.TrimSpace(record.Metadata.Originator)
			}
			client.TrackCodexPluginMeasurementsEvent(ctx, telemetry.CodexPluginMeasurementsInput{
				ThreadID:    threadID,
				TurnID:      strings.TrimSpace(turnID),
				PluginID:    batch.PluginID,
				ExecutionID: batch.ExecutionID,
				Operation:   batch.Operation,
				Originator:  originator,
				Rows:        rows,
			})
		}
	}
	if cfg != nil {
		options.CodeModeDefaultExecYieldTime = cfg.CodeModeDefaultExecYieldTime()
		options.CodeModeShowCellOverhead = cfg.CodeModeExperimentalShowCellOverhead()
	}
	options.DisableCodeModeFallback = r.services.DisableCodeModeFallback
	options.EnableUnifiedExec = cfg != nil && features.Enabled(cfg.FeatureSettings(), "unified_exec")
	options.OmitToolSearchSources = cfg != nil && features.Enabled(cfg.FeatureSettings(), "deferred_tool_world_state")
	options.EnableWaitForEnvironment = waitForEnvironmentEnabled
	options.EnvironmentWaiter = appServerEnvironmentWaiter{manager: r.services.Environment}
	options.SelectedEnvironmentIDs = selectedEnvironmentIDs(params)
	options.WaitForEnvironmentToolConfig = r.services.WaitForEnvironmentToolConfig
	// Rust #45124: the free-form async user message tool is available to root
	// agents when the model catalog advertises it or the
	// send_message_to_user_async feature is enabled; subagents never get it.
	freeformAsyncRootAgent := !r.turnThreadIsSubagent(threadID)
	freeformAsyncFeature := cfg != nil && features.Enabled(cfg.FeatureSettings(), "send_message_to_user_async")
	var catalogTools []string
	if turnModelInfo != nil {
		sendUserMessageAsyncAdded := false
		requestUserInputAsyncAdded := false
		for _, supported := range turnModelInfo.ExperimentalSupportedTools {
			if supported == tool.DefaultSendUserMessageAsyncToolName && !sendUserMessageAsyncAdded {
				options.ExperimentalSupportedTools = append(options.ExperimentalSupportedTools, supported)
				sendUserMessageAsyncAdded = true
			}
			if supported == tool.DefaultRequestUserInputAsyncToolName && !requestUserInputAsyncAdded {
				options.ExperimentalSupportedTools = append(options.ExperimentalSupportedTools, supported)
				requestUserInputAsyncAdded = true
			}
		}
		catalogTools = turnModelInfo.ExperimentalSupportedTools
		if turnModelInfo.ModelMessages != nil {
			options.ModelConfirmationPolicies = turnModelInfo.ModelMessages.ConfirmationPolicies
			if tools := turnModelInfo.ModelMessages.Tools; tools != nil && tools.SendUserMessageAsync != nil {
				options.SendUserMessageAsyncDescription = tools.SendUserMessageAsync.Description
			}
		}
	}
	if freeformAsyncToolAvailable(freeformAsyncRootAgent, catalogTools, freeformAsyncFeature) {
		options.ExperimentalSupportedTools = append(options.ExperimentalSupportedTools, tool.DefaultSendMessageToUserAsyncToolName)
	}
	// Rust omits the confirmation-policies request metadata for Guardian review
	// sessions (is_basic_session_source), so the actor tools are the main model's.
	options.SuppressActorConfirmationPolicies = guardianTurnStart(params)
	if options.Shell != nil && cfg != nil {
		options.Shell.Validation.AdditionalPermissionsAllowed = features.Enabled(cfg.FeatureSettings(), "exec_permission_approvals")
		if policy := strings.TrimSpace(stringConfigValue(cfg, "approval_policy")); policy != "" {
			options.Shell.Validation.ApprovalPolicy = sandbox.AskForApproval(policy)
		}
	}
	if options.Shell != nil && permissionProfile != nil && permissionProfile.Profile != nil {
		options.Shell.Validation.PermissionProfileID = strings.TrimSpace(permissionProfile.ID)
		options.Shell.Validation.PermissionProfile = permissionProfile.Profile
	}
	if options.ApplyPatch != nil && permissionProfile != nil && permissionProfile.Profile != nil {
		options.ApplyPatch.PermissionProfile = permissionProfile.Profile
		options.ApplyPatch.SandboxPolicy = nil
	}
	if cfg != nil {
		// Rust c9c6c0daa9: apply_patch_preserve_line_endings controls the
		// in-process tool mode and the rollout env var for child processes.
		preserveLineEndings := features.Enabled(cfg.FeatureSettings(), "apply_patch_preserve_line_endings")
		if options.ApplyPatch != nil {
			options.ApplyPatch.PreserveLineEndings = preserveLineEndings
		}
		if options.Shell != nil {
			options.Shell.PreserveLineEndings = preserveLineEndings
		}
	}
	approvalPolicy := turnApprovalPolicyForTurn(cfg, params)
	if options.Shell != nil {
		if managedNetwork != nil {
			options.Shell.ManagedNetworkResolver = func(environmentID string, remote bool) (*tool.ManagedNetworkResolution, error) {
				if remote {
					proxyConfig := managedNetwork.RemoteConfigSnapshot()
					remoteConfig, err := execserver.RemoteNetworkProxyConfigFromProxyConfig(proxyConfig)
					if err != nil {
						return nil, err
					}
					metadata := r.networkProxyAuditMetadataForThread(threadID, network.ProxyPolicyRequest{EnvironmentID: environmentID})
					timeout := 15 * time.Minute
					timeoutMS := uint64(timeout / time.Millisecond)
					launch := &execserver.RemoteNetworkProxyLaunchConfig{
						Proxy: remoteConfig,
						AuditMetadata: execserver.RemoteNetworkProxyAuditMetadata{
							ConversationID: metadata.ConversationID,
							AppVersion:     metadata.AppVersion,
							UserAccountID:  metadata.UserAccountID,
							AuthMode:       metadata.AuthMode,
							Originator:     metadata.Originator,
							UserEmail:      metadata.UserEmail,
							TerminalType:   metadata.TerminalType,
							Model:          metadata.Model,
							Slug:           metadata.Slug,
						},
						EnvironmentID:           stringPtrIfNotEmpty(environmentID),
						PolicyDecisionTimeoutMS: &timeoutMS,
					}
					return &tool.ManagedNetworkResolution{
						RemoteNetworkProxy: launch,
						NetworkPolicyDecider: network.ProxyPolicyDeciderFunc(func(ctx context.Context, request network.ProxyPolicyRequest) network.ProxyDecision {
							return r.networkApproval.decideForThread(ctx, threadID, request)
						}),
						NetworkPolicyDecisionTimeout: timeout,
					}, nil
				}
				env, sandboxContext, err := managedNetwork.PrepareForEnvironment(environmentID)
				if err != nil {
					return nil, err
				}
				return &tool.ManagedNetworkResolution{Env: env, ManagedNetwork: &sandboxContext}, nil
			}
		}
		if cfg != nil {
			options.Shell.MaxOutputTokens = cfg.ToolOutputTokenLimit()
			options.Shell.Validation.AllowLoginShell = cfg.AllowLoginShell()
			allowTTY := features.Enabled(cfg.FeatureSettings(), "unified_exec_tty")
			options.Shell.AllowTTY = &allowTTY
			options.Shell.Validation.WindowsSandboxLevel = windowsSandboxLevelForConfig(cfg)
			// Rust #46554: legacy Windows sandboxes always use a private desktop.
			options.Shell.Validation.WindowsSandboxPrivateDesktop = windowsSandboxPrivateDesktopForTurn()
		}
		if guardianTurnStart(params) {
			options.Shell.Validation.WindowsSandboxProxySettingsMode = execserver.WindowsSandboxProxySettingsPreserve
		}
		options.Shell.UnifiedExecEvents = r.runtimeUnifiedExecEventSink(threadID, strings.TrimSpace(turnID))
		options.Shell.UnifiedExecEnvironments = r.unifiedExecEnvironmentsForTurn(params)
		options.Shell.Validation.ApprovalPolicy = approvalPolicy
		if r.commandApprovalForSession(threadID) {
			options.Shell.Validation.PermissionsPreapproved = true
		}
		if r.serverRequestSinkConfigured() {
			modelID := strings.TrimSpace(params.Model)
			if modelID == "" {
				modelID = stringConfigValue(cfg, "model")
			}
			options.Shell.Approval = r.shellApprovalForTurn(threadID, strings.TrimSpace(turnID), r.modelIgnoresAllowPrefixRules(cfg, modelID))
			// The shell executor reports the decisions it never had to ask for
			// (Rust's tool_decision(Approved, Config) for a skipped requirement).
			options.Shell.DecisionSink = r.sessionTelemetryForThread(threadID)
		}
	}
	if options.ApplyPatch != nil && r.serverRequestSinkConfigured() && applyPatchApprovalRequiredForTurn(approvalPolicy) {
		if !r.fileChangeApprovalForSession(threadID) {
			options.ApplyPatch.Approval = r.applyPatchApprovalForTurn(threadID, strings.TrimSpace(turnID))
		}
	}
	if options.ApplyPatch != nil {
		// Patches the policy approves without a prompt report the same
		// config-approved decision the shell executor reports.
		options.ApplyPatch.DecisionSink = r.sessionTelemetryForThread(threadID)
	}
	options.EnableMCP = len(mcpTools) > 0
	options.MCPService = mcpService
	orchestratorSkillsEnabled := cfg == nil || cfg.OrchestratorSkillsEnabled()
	options.OrchestratorSkillsEnabled = &orchestratorSkillsEnabled
	options.SkillProviders = executorSkillProviders
	options.MCPTools = mcpTools
	options.MCPConnectors = mcpConnectors
	// Rust #46035: the tool exposure policy combines a Codex Apps connector's
	// omit_tools_from with its server's, using the effective [apps] config.
	if cfg != nil {
		options.AppsConfigValues = cfg.Values
	}
	// Rust mcp_tool_call.rs maybe_request_mcp_tool_approval: gate custom MCP
	// tool calls on their configured approval mode.
	options.MCPToolApproval = r.newAppserverMCPToolApprovalOptions(cfg, mcpService, threadID, turnID, approvalPolicy)
	// Rust maybe_request_codex_apps_auth_elicitation: enable the Codex Apps
	// connector URL elicitation flow when the feature is on and the approval
	// policy allows agent-initiated MCP elicitations.
	if options.EnableMCP && cfg != nil && features.Enabled(cfg.FeatureSettings(), "auth_elicitation") &&
		r.mcpElicitationsAllowedForApproval(cfg, approvalPolicy, r.activeTurnParams(threadID)) {
		options.MCPAuthElicitation = r.mcpAuthElicitationOptions(mcpService)
	}
	if params != nil {
		options.Model = strings.TrimSpace(params.Model)
	}
	if runtimeToolsUseOpenAIFileUpload(mcpTools) {
		runtimeAuth := mcp.RuntimeAuthFromSnapshot(r.requireAccount().AuthSnapshot())
		fileSystem := r.primaryTurnOpenAIFileSystem(params)
		if envFS, ok := fileSystem.(*environmentOpenAIFileSystem); ok &&
			permissionProfile != nil && permissionProfile.Profile != nil && permissionProfile.Profile.HasDenyReadEntries() {
			envFS.requiresSandbox = true
		}
		var grantedReadPaths []string
		if params != nil {
			grantedReadPaths = append(grantedReadPaths, params.RuntimeWorkspaceRoots...)
		}
		options.OpenAIFileRewriter = mcp.NewOpenAIFileRewriterWithOptions(mcp.OpenAIFileRewriterOptions{
			CWD:              primaryTurnEnvironmentCWD(params, cwd),
			Auth:             mcp.OpenAIFileAuthFromRuntimeAuth(runtimeAuth, cfg.ChatGPTBaseURL()),
			FileSystem:       fileSystem,
			HTTPClient:       r.httpClientForConfig(cfg),
			ReadPolicy:       openAIFileReadPolicy(fileSystem, permissionProfile),
			GrantedReadPaths: grantedReadPaths,
		})
	}
	options.EnableAgents = false
	if cfg != nil && r.services.ThreadRouter != nil && r.services.ThreadRouter.store != nil {
		agentsConfig, agentsErr := cfg.AgentsConfig(r.configBaseDirForAgents())
		if agentsErr != nil {
			return nil, agentsErr
		}
		// Rust load_agent_roles builds the catalog from the config layer stack
		// (declared `[agents.<name>]` roles plus `<config_folder>/agents`
		// discovery). Prefer that layered catalog when the layers define roles,
		// keeping the merged-table catalog as the fallback.
		if r.services.Config != nil {
			if layeredRoles, _ := r.services.Config.AgentRolesForCWD(cwd); len(layeredRoles) > 0 {
				agentsConfig.Roles = layeredRoles
			}
		}
		maxDepth := config.DefaultAgentMaxDepth
		if agentsConfig.MaxDepth != nil {
			maxDepth = *agentsConfig.MaxDepth
		}
		// Rust resolves the per-thread multi-agent version as
		// `multi_agent_version_for_model`: the MultiAgentV2 feature overrides
		// first, then the model's declared multi-agent version, then the stable
		// `multi_agent` (Collab) feature falls back to V1. `agents.enabled=false`
		// disables the whole surface regardless of model/features.
		threadRecord := (*session.Record)(nil)
		if record, recordErr := r.threadRecord(session.ThreadID(threadID), true, false); recordErr == nil && record != nil {
			threadRecord = record
		}
		version := r.runtimeMultiAgentVersionForTurn(threadID, cfg, agentsConfig)
		if version != "" {
			// Rust hides the V1 collab tool surface entirely for a thread whose
			// next spawn depth would exceed max_depth ("Agent depth limit
			// reached. Solve the task yourself."). V2 ignores max_depth and
			// relies on concurrency.
			currentDepth := 0
			if threadRecord != nil {
				currentDepth = threadRecord.Metadata.AgentDepth
			}
			depthLimited := agent.ExceedsThreadSpawnDepthLimit(currentDepth+1, maxDepth)
			canEnable := version != agent.VersionV1 || !depthLimited
			// Rust only exposes V2 collaboration tools to sub-agents whose
			// current model declares multi_agent_version v2
			// (collab_tools_enabled); root threads are always allowed.
			if canEnable && version == agent.VersionV2 && threadRecord != nil && (strings.TrimSpace(threadRecord.Metadata.AgentPath) != "" || threadRecord.Metadata.AgentDepth > 0) {
				canEnable = runtimeV2SubagentToolsEnabled(r, threadRecord, cfg)
			}
			if canEnable {
				options.EnableAgents = true
			}
		}
		if options.EnableAgents {
			maxThreads := agentsConfig.MaxConcurrentThreadsPerSession
			defaults := agent.SpawnDefaults{Model: agentsConfig.DefaultSubagentModel, ReasoningEffort: agentsConfig.DefaultSubagentReasoningEffort}
			if version == agent.VersionV2 {
				v2Config, configErr := cfg.MultiAgentV2Config(maxThreads)
				if configErr != nil {
					return nil, configErr
				}
				maxThreads = v2Config.MaxConcurrentThreadsPerSession
				options.AgentNamespace = v2Config.ToolNamespace
				options.AgentUsageHintText = v2Config.UsageHintText
				options.AgentWaitMin = v2Config.MinWaitTimeout
				options.AgentWaitMax = v2Config.MaxWaitTimeout
				options.AgentWaitDefault = v2Config.DefaultWaitTimeout
				options.AgentWaitConfigured = true
				options.AgentHideSpawnMetadata = v2Config.HideSpawnAgentMetadata
				options.AgentExposeSpawnModelOverrides = v2Config.ExposeSpawnAgentModelOverrides
				// Rust #46505: the active model's catalog supplies per-tool
				// Multi-Agent V2 description and parameter overrides.
				if turnModelInfo != nil {
					options.AgentToolOverrides = turn.MultiAgentToolOverridesFromCatalog(turnModelInfo.ModelMessages)
				}
				if v2Config.NonCodeModeOnly {
					options.AgentExposure = tool.ExposureDirectModelOnly
				} else {
					// Rust exposes V2 collaboration tools with ToolExposure::Direct
					// when non_code_mode_only is disabled; the Go default of
					// Discoverable would defer them behind tool search instead.
					options.AgentExposure = tool.ExposureModelVisible
				}
				options.DisableWaitAgent = options.DisableWaitAgent || !v2Config.WaitAgentEnabled
				defaults.DeveloperInstructions = v2Config.SubagentDeveloperInstructions
			}
			options.AgentVersion = version
			options.AgentController = newRuntimeAgentControllerForTurn(r, threadID, turnID, effectiveRootTurnID(params.RootTurnID, turnID, params.ParentTurnID, ""), params.TurnTrigger, cwd, maxThreads, version, params.Environments)
			if runtimeController, ok := options.AgentController.(*runtimeAgentController); ok {
				runtimeController.maxDepth = maxDepth
			}
			options.AgentRoles = agentsConfig.Roles
			options.AgentDefaults = defaults
		}
	}
	if params != nil && len(params.DynamicTools) > 0 {
		options.DynamicToolCaller = dynamicToolServerRequestCaller{broker: r.requireServerRequests()}
		options.DynamicTools = params.CloneDynamicTools()
	}
	options.ContextStatus = r.contextStatusForThread(threadID)
	options.NewContextWindow = r.newContextWindowRequesterForTurn(threadID, cfg)
	options.UserInputResponder = r.userInputResponderForTurn(threadID, strings.TrimSpace(turnID))
	// Rust build_mcp_tool_call_request_meta: MCP tool calls report the turn's
	// metadata document in `_meta`.
	options.MCPTurnMetadata = r.mcpTurnMetadataProvider(threadID, strings.TrimSpace(turnID))
	options.RequestUserInputAvailableModes = requestUserInputModes
	options.EnableCurrentTimeTool = enableCurrentTimeTool
	options.EnableSleepTool = enableSleepTool
	options.DisableUpdatePlan = disableUpdatePlan
	options.DisableWaitAgent = disableWaitAgent
	options.ClockProvider = clockProvider
	if features.Enabled(cfg.FeatureSettings(), "request_permissions_tool") {
		options.EnableRequestPermissions = true
		options.RequestPermissionsReviewer = r.requestPermissionsGuardianReviewer(threadID)
	}
	if len(candidates) > 0 && cfg != nil && features.Enabled(cfg.FeatureSettings(), "tool_suggest") {
		options.PluginInstallCandidates = candidates
		options.PluginInstallRecommendationContext = true
		// Rust #45806: install requests are root-only, so a delegated subagent's
		// call is rejected before it can prompt the user.
		options.NonRootAgent = r.turnThreadIsSubagent(threadID)
		options.PluginInstallRuntime = &pluginInstallRuntime{
			broker:   r.requireServerRequests(),
			plugins:  r.services.Plugins,
			apps:     r.requireApps(),
			config:   r.requireConfig(),
			threadID: threadID,
			turnID:   strings.TrimSpace(turnID),
			metrics:  r.services.TurnMetrics,
		}
	}
	options.WebSearch = webSearchOptions
	options.ImageGeneration = imageGenerationOptions
	options.ViewImage = viewImageOptions
	options.ThreadID = threadID
	options.TurnID = strings.TrimSpace(turnID)
	historyNotesTools := []tool.Executor(nil)
	if cfg != nil {
		contextManaged := r.experimentalContextManagementEligible(cfg, params)
		if tokenBudget, tokenErr := cfg.TokenBudgetConfig(); contextManaged || (tokenErr == nil && tokenBudget != nil && tokenBudget.UseHistoryNotesExtension) {
			historyNotesTools = r.historyNotesToolsForTurn(cfg, params, threadID)
		}
	}
	extraTools := append(goalToolExecutors, historyNotesTools...)
	options.ExtraTools = extraTools
	// Rust 97729885d4: expose the shared root-session ID to model-reachable
	// shell commands as CODEX_SESSION_ID. Fall back to the thread ID when the
	// session record is unavailable.
	options.SessionID = threadID
	if record, recordErr := r.threadRecord(session.ThreadID(threadID), true, false); recordErr == nil && record != nil && strings.TrimSpace(record.SessionID) != "" {
		options.SessionID = record.SessionID
	}
	return turn.BuildToolRouter(options)
}

// historyNotesToolsForTurn mirrors Rust HistoryNotesExtension::tools (#39827):
// the nine history/notes tools are exposed for token-budget sessions when the
// extension gate is enabled, the provider is OpenAI, and the auth uses the
// Codex backend. A backend/auth resolution failure disables the surface rather
// than failing the turn.
func (r *RuntimeRouter) historyNotesToolsForTurn(cfg *config.Config, params *turn.TurnStartParams, threadID string) []tool.Executor {
	backend, sessionID, agentName, ok := r.historyNotesBackendForTurn(cfg, params, threadID)
	if !ok {
		return nil
	}
	return historynotes.Tools(backend, sessionID, agentName)
}

// experimentalContextManagementEligible reports whether the experimental
// context-management config can activate token-budget context, history notes,
// and the new_context tool for this thread. It mirrors Rust's provider/backend
// and ChatGPT subscription restrictions without mutating the config.
func (r *RuntimeRouter) experimentalContextManagementEligible(cfg *config.Config, params *turn.TurnStartParams) bool {
	if r == nil || cfg == nil || !features.Enabled(cfg.FeatureSettings(), "context_management") || !cfg.ContextManagementConfig().ExperimentalMode {
		return false
	}
	modelProviderConfig, err := r.appTurnModelProviderConfig(cfg, params)
	if err != nil {
		return false
	}
	providerInfo, err := model.ProviderForConfigID(configValues(cfg), modelProviderConfig.ProviderID, stringConfigValue(cfg, "openai_base_url"))
	if err != nil || providerInfo == nil || !providerInfo.IsOpenAI() {
		return false
	}
	// Rust 6af345407d (#43147): experimental context activation also requires
	// the starting model to advertise supports_experimental_context. Omitted
	// metadata defaults to false, so an unresolved model stays ineligible.
	if info := r.modelInfoForRuntimeWithConfig(strings.TrimSpace(modelProviderConfig.Model), cfg); info == nil || !info.SupportsExperimentalContext {
		return false
	}
	resolved, err := r.resolveAuthWithLoginRestrictions(r.codexHomeForRollout())
	if err != nil || resolved == nil || (&resolved.Auth).BackendMode() != "chatgpt" {
		return false
	}
	account := auth.AccountFromAuth(&resolved.Auth)
	if account == nil {
		return false
	}
	switch account.PlanType {
	case auth.PlanPlus, auth.PlanPro, auth.PlanProlite:
		return true
	default:
		return false
	}
}

// historyNotesBackendForTurn resolves the history-notes backend for a thread,
// returning the backend, session id, and agent name. It returns ok=false when
// the extension gate, OpenAI provider, or chatgpt backend auth is unavailable.
func (r *RuntimeRouter) historyNotesBackendForTurn(cfg *config.Config, params *turn.TurnStartParams, threadID string) (*historynotes.Backend, string, string, bool) {
	if r == nil || cfg == nil {
		return nil, "", "", false
	}
	modelProviderConfig, err := r.appTurnModelProviderConfig(cfg, params)
	if err != nil {
		return nil, "", "", false
	}
	providerInfo, err := model.ProviderForConfigID(configValues(cfg), modelProviderConfig.ProviderID, stringConfigValue(cfg, "openai_base_url"))
	if err != nil || providerInfo == nil || !providerInfo.IsOpenAI() {
		return nil, "", "", false
	}
	resolved, err := r.resolveAuthWithLoginRestrictions(r.codexHomeForRollout())
	if err != nil || resolved == nil || (&resolved.Auth).BackendMode() != "chatgpt" {
		return nil, "", "", false
	}
	runtimeProvider := model.CreateRuntimeProviderForID(modelProviderConfig.ProviderID, *providerInfo, &resolved.Auth)
	apiProvider, err := runtimeProvider.APIProvider()
	if err != nil {
		return nil, "", "", false
	}
	authHeaders, err := runtimeProvider.APIAuth()
	if err != nil {
		return nil, "", "", false
	}
	sessionID := strings.TrimSpace(threadID)
	agentName := "root"
	if record, recordErr := r.threadRecord(session.ThreadID(threadID), true, false); recordErr == nil && record != nil {
		if strings.TrimSpace(record.SessionID) != "" {
			sessionID = strings.TrimSpace(record.SessionID)
		}
		if path := strings.TrimSpace(record.Metadata.AgentPath); path != "" {
			agentName = path
		}
	}
	backend := &historynotes.Backend{
		BaseURL: strings.TrimRight(strings.TrimSpace(apiProvider.BaseURL), "/"),
		ApplyAuth: func(request *http.Request, body []byte) error {
			_, err := authHeaders.ApplyRequest(context.Background(), request, body)
			return err
		},
		HTTPDoer: r.httpClientForConfig(cfg).Do,
	}
	return backend, sessionID, agentName, true
}

// requestPermissionsGuardianReviewer routes request_permissions calls through
// the shared Guardian approval path (Rust #38701). Turn cancellation while an
// automatic permission review is pending propagates through ctx.
func (r *RuntimeRouter) requestPermissionsGuardianReviewer(threadID string) tool.RequestPermissionsReviewer {
	return func(ctx context.Context, reviewThreadID string, turnID string, callID string, reason string, permissions map[string]any) (tool.RequestPermissionsDecision, error) {
		if r == nil {
			return tool.RequestPermissionsDecision{}, errors.New("request_permissions review is unavailable")
		}
		if strings.TrimSpace(reviewThreadID) != "" {
			threadID = strings.TrimSpace(reviewThreadID)
		}
		// Rust Session::request_approval: PermissionRequest hooks decide before
		// the Guardian review for a request_permissions call too.
		if verdict, ok := r.permissionRequestHookVerdict(ctx, threadID, turnID, callID, "request_permissions", nil, requestPermissionsHookToolInput(reason, permissions)); ok && verdict != nil {
			switch verdict.Kind {
			case HookPermissionRequestAllow:
				return tool.RequestPermissionsDecision{Approved: true}, nil
			case HookPermissionRequestDeny:
				denyReason := ""
				if verdict.Message != nil {
					denyReason = strings.TrimSpace(*verdict.Message)
				}
				return tool.RequestPermissionsDecision{Reason: denyReason}, nil
			}
		}
		reviewer := r.ensureGuardianReviewer(r.services.Agent)
		action := state.Action{
			Type:        "request_permissions",
			TurnID:      strings.TrimSpace(turnID),
			Reason:      strings.TrimSpace(reason),
			Permissions: permissions,
		}
		decision, reviewReason, err := reviewer.Review(ctx, threadID, turnID, callID, action)
		if err != nil {
			return tool.RequestPermissionsDecision{}, err
		}
		return tool.RequestPermissionsDecision{Approved: decision == state.DecisionApproved, Reason: reviewReason}, nil
	}
}

func primaryTurnEnvironmentCWD(params *turn.TurnStartParams, fallback string) string {
	if selection := primaryTurnEnvironmentSelection(params); selection != nil {
		if cwd := strings.TrimSpace(firstNonEmpty(
			threadItemStringFromAnyMap(selection, "cwd"),
			threadItemStringFromAnyMap(selection, "CWD"),
		)); cwd != "" {
			return cwd
		}
	}
	return strings.TrimSpace(fallback)
}

// turnEnvironmentWorkspaceRoots returns the captured primary selection's
// declared workspace roots, even when the attachment is still starting or has
// failed (Rust TurnEnvironmentSnapshot::primary_workspace_root_uris, #46568).
// The selection's cwd is the fallback when the client declared no roots, and the
// supplied cwd when the turn has no selections at all.
func turnEnvironmentWorkspaceRoots(params *turn.TurnStartParams, fallback string) []string {
	if params == nil || len(params.Environments) == 0 {
		return nil
	}
	selection := params.Environments[0]
	roots := stringSliceFromAny(firstNonNil(selection["workspaceRoots"], selection["workspace_roots"]))
	trimmed := make([]string, 0, len(roots)+1)
	for _, root := range roots {
		if root = strings.TrimSpace(root); root != "" {
			trimmed = append(trimmed, root)
		}
	}
	if len(trimmed) > 0 {
		return trimmed
	}
	if cwd := strings.TrimSpace(firstNonEmpty(
		threadItemStringFromAnyMap(selection, "cwd"),
		threadItemStringFromAnyMap(selection, "CWD"),
	)); cwd != "" {
		return []string{cwd}
	}
	if fallback = strings.TrimSpace(fallback); fallback != "" {
		return []string{fallback}
	}
	return nil
}

// turnEnvironmentSelectionPermissionProfileResolution returns the primary
// selected environment's own resolved profile (Rust
// TurnEnvironment::permission_profile_with_workspace_roots, #46568). The
// attachment supplies a fully materialized profile, so it is used as-is; a
// primary attachment without one falls through to the thread defaults.
func turnEnvironmentSelectionPermissionProfileResolution(params *turn.TurnStartParams) *config.SandboxPermissionProfileResolution {
	if params == nil || len(params.Environments) == 0 {
		return nil
	}
	state, err := environmentConfigStateFromAnyMap(params.Environments[0])
	if err != nil || state.Kind != EnvironmentConfigReady || state.Config == nil || state.Config.PermissionProfile == nil {
		return nil
	}
	profile := *state.Config.PermissionProfile
	raw := strings.TrimSpace(state.Config.PermissionProfileJSON)
	if raw == "" {
		encoded, err := sandbox.RuntimePermissionProfileJSON(profile)
		if err != nil {
			return nil
		}
		raw = encoded
	}
	return &config.SandboxPermissionProfileResolution{
		ID:             strings.TrimSpace(state.Config.ActivePermissionProfile),
		Profile:        &profile,
		ProfileJSON:    raw,
		WorkspaceRoots: append([]string(nil), state.Config.WorkspaceRoots...),
	}
}

// primaryTurnEnvironmentSelection returns the first environment selection that
// is usable for the turn. Pending and failed attachments stay out of turn
// environments and the primary environment fallback (#38684).
func primaryTurnEnvironmentSelection(params *turn.TurnStartParams) map[string]any {
	if params == nil {
		return nil
	}
	for _, selection := range params.Environments {
		state, err := environmentConfigStateFromAnyMap(selection)
		if err == nil && (state.Kind == EnvironmentConfigPending || state.Kind == EnvironmentConfigFailed) {
			continue
		}
		return selection
	}
	return nil
}

type turnMCPPreparation struct {
	tools      []mcp.RuntimeToolInfo
	connectors []mcp.RuntimeConnector
}

func (r *RuntimeRouter) prepareTurnToolInputs(ctx context.Context, threadID string, cfg *config.Config, mcpService *mcp.MCPService, requiredMCPServers []string, requiredPlugins []string) ([]plugin.DiscoverableInfo, []mcp.RuntimeToolInfo, []mcp.RuntimeConnector, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	// Both preparation branches consult the app catalog. Initialize its shared
	// service before either goroutine can observe the lazy service pointer.
	if r != nil {
		_ = r.requireApps()
	}
	recommendations := make(chan []plugin.DiscoverableInfo, 1)
	mcpPrepared := make(chan turnMCPPreparation, 1)
	go func() {
		var candidates []plugin.DiscoverableInfo
		if r != nil {
			candidates = pluginInstallRecommendationCandidates(r.pluginInstallCandidatesForTurnContext(threadID, ctx, cfg))
		}
		recommendations <- candidates
	}()
	go func() {
		tools, connectors := r.mcpRuntimeInputsForServiceWithRequirements(threadID, cfg, mcpService, requiredMCPServers, requiredPlugins)
		mcpPrepared <- turnMCPPreparation{tools: tools, connectors: connectors}
	}()

	var candidates []plugin.DiscoverableInfo
	select {
	case <-ctx.Done():
		return nil, nil, nil, ctx.Err()
	case candidates = <-recommendations:
	}
	var prepared turnMCPPreparation
	select {
	case <-ctx.Done():
		return nil, nil, nil, ctx.Err()
	case prepared = <-mcpPrepared:
	}
	return candidates, prepared.tools, prepared.connectors, nil
}

func runtimeToolsUseOpenAIFileUpload(tools []mcp.RuntimeToolInfo) bool {
	for i := range tools {
		if len(tools[i].OpenAIFileInputOptionalFields) > 0 {
			return true
		}
	}
	return false
}

func (r *RuntimeRouter) viewImageOptionsForTurn(cfg *config.Config, params *turn.TurnStartParams, cwd string) *tool.ViewImageOptions {
	if cfg != nil && !features.Enabled(cfg.FeatureSettings(), "view_image") {
		return nil
	}
	modelID := ""
	if params != nil {
		modelID = strings.TrimSpace(params.Model)
	}
	modelID = firstNonEmpty(modelID, stringConfigValue(cfg, "model"), defaultModelForAppTurn())
	info := r.modelInfoForRuntimeWithConfig(modelID, cfg)
	if !appModelInfoSupportsImageInput(info) {
		return nil
	}
	return &tool.ViewImageOptions{
		CWD:                      cwd,
		CanRequestOriginalDetail: info.SupportsImageDetailOriginal,
		IncludeEnvironmentID:     len(selectedEnvironmentIDs(params)) > 1,
	}
}

func (r *RuntimeRouter) configBaseDirForAgents() string {
	if r != nil && r.services.Config != nil {
		if home := strings.TrimSpace(r.services.Config.CodexHome()); home != "" {
			return home
		}
	}
	return processCWD()
}

func (r *RuntimeRouter) unifiedExecEnvironmentsForTurn(params *turn.TurnStartParams) []tool.UnifiedExecEnvironment {
	if r == nil || params == nil || len(params.Environments) == 0 || r.services.Environment == nil {
		return nil
	}
	// Resolve FromThread attachments against the thread's environment config
	// once at the attachment boundary (#38521, #38673, #38678). Owner-supplied
	// Ready configurations keep their canonical value; Pending and Failed
	// attachments stay out of turn environments (#38684).
	threadConfig := r.threadEnvironmentConfigForTurn(params)
	out := make([]tool.UnifiedExecEnvironment, 0, len(params.Environments))
	for _, selected := range params.Environments {
		environmentID := selectionEnvironmentID(selected)
		state, _ := resolveEnvironmentConfig(selected, threadConfig)
		if state.Kind == EnvironmentConfigPending || state.Kind == EnvironmentConfigFailed {
			continue
		}
		record, ok := r.services.Environment.Record(environmentID)
		if !ok || record == nil {
			continue
		}
		cwd := strings.TrimSpace(firstNonEmpty(
			threadItemStringFromAnyMap(selected, "cwd"),
			threadItemStringFromAnyMap(selected, "CWD"),
		))
		shellInfo := record.Shell
		platformOS := ""
		userHomeDir := ""
		if info, err := r.services.Environment.InfoContext(context.Background(), &EnvironmentInfoParams{EnvironmentID: environmentID}); err == nil && info != nil {
			shellInfo = info.Shell
			platformOS = strings.TrimSpace(info.PlatformOS)
			userHomeDir = strings.TrimSpace(info.UserHomeDir)
		} else if !record.InfoOverride {
			shellInfo = EnvironmentShellInfo{}
		}
		shellPath := strings.TrimSpace(shellInfo.Path)
		var environmentShell *tool.Shell
		if shellPath != "" {
			environmentShell = &tool.Shell{Type: tool.DetectShellType(shellPath), Path: shellPath}
		}
		environment := tool.UnifiedExecEnvironment{
			ID:            environmentID,
			CWD:           cwd,
			Shell:         environmentShell,
			PlatformOS:    platformOS,
			UserHomeDir:   userHomeDir,
			ExecServerURL: strings.TrimSpace(record.ExecServerURL),
			NoiseProvider: record.NoiseProvider,
		}
		if state.Config != nil {
			allowLoginShell := state.Config.AllowLoginShell
			environment.AllowLoginShell = &allowLoginShell
			environment.ShellEnvironmentPolicy = cloneShellEnvironmentPolicy(state.Config.ShellEnvironmentPolicy)
			environment.PermissionProfile = state.Config.PermissionProfile
			environment.PermissionProfileID = strings.TrimSpace(state.Config.ActivePermissionProfile)
			environment.PermissionProfileJSON = strings.TrimSpace(state.Config.PermissionProfileJSON)
		}
		out = append(out, environment)
	}
	return out
}

// threadEnvironmentConfigForTurn builds the thread-derived EnvironmentConfig
// used to resolve FromThread attachments: the login-shell policy, the turn's
// resolved permission profile, and the thread's persisted capability roots
// (Rust Session::turn_environment_config).
func (r *RuntimeRouter) threadEnvironmentConfigForTurn(params *turn.TurnStartParams) *EnvironmentConfig {
	if r == nil || params == nil {
		return nil
	}
	cfg, err := r.effectiveConfigForTurn(params)
	if err != nil || cfg == nil {
		return nil
	}
	config := &EnvironmentConfig{AllowLoginShell: cfg.AllowLoginShell()}
	if table, ok := cfg.Values["shell_environment_policy"].(map[string]any); ok {
		config.ShellEnvironmentPolicy = cloneShellEnvironmentPolicy(table)
	}
	cwd := firstNonEmpty(primaryTurnEnvironmentCWD(params, params.CWD), params.CWD, r.services.DefaultCWD)
	resolution, resolveErr := turnSandboxPermissionProfile(cfg, cwd, params)
	if resolveErr == nil && resolution != nil && resolution.Profile != nil {
		config.PermissionProfile = resolution.Profile
		config.ActivePermissionProfile = strings.TrimSpace(resolution.ID)
		if raw := strings.TrimSpace(resolution.ProfileJSON); raw != "" {
			config.PermissionProfileJSON = raw
		} else if raw, jsonErr := sandbox.RuntimePermissionProfileJSON(*resolution.Profile); jsonErr == nil {
			config.PermissionProfileJSON = raw
		}
	}
	if record, recordErr := r.threadRecord(session.ThreadID(params.ThreadID), true, false); recordErr == nil && record != nil {
		config.SelectedCapabilityRoots = threadSelectedCapabilityRoots(record)
	}
	config.WorkspaceRoots = append([]string(nil), params.RuntimeWorkspaceRoots...)
	if environmentCWD := firstNonEmpty(primaryTurnEnvironmentCWD(params, params.CWD), params.CWD); strings.TrimSpace(environmentCWD) != "" && len(config.WorkspaceRoots) == 0 {
		config.WorkspaceRoots = []string{environmentCWD}
	}
	return config
}

type appServerEnvironmentWaiter struct {
	manager *EnvironmentManager
}

func (w appServerEnvironmentWaiter) Status(ctx context.Context, environmentID string) (tool.EnvironmentStatus, string, error) {
	if w.manager == nil {
		return tool.EnvironmentStatusUnknown, "environment manager is unavailable", nil
	}
	status, err := w.manager.StatusContext(ctx, &EnvironmentStatusParams{EnvironmentID: environmentID})
	if err != nil {
		return tool.EnvironmentStatusUnknown, "", err
	}
	message := ""
	if status != nil && status.Error != nil {
		message = *status.Error
	}
	if status == nil {
		return tool.EnvironmentStatusUnknown, message, nil
	}
	return tool.EnvironmentStatus(status.Status), message, nil
}

func selectedEnvironmentIDs(params *turn.TurnStartParams) []string {
	if params == nil || len(params.Environments) == 0 {
		return nil
	}
	out := make([]string, 0, len(params.Environments))
	seen := map[string]bool{}
	for _, selected := range params.Environments {
		environmentID := strings.TrimSpace(firstNonEmpty(
			threadItemStringFromAnyMap(selected, "environmentId"),
			threadItemStringFromAnyMap(selected, "environment_id"),
		))
		if environmentID != "" && !seen[environmentID] {
			seen[environmentID] = true
			out = append(out, environmentID)
		}
	}
	return out
}

func (r *RuntimeRouter) serverRequestSinkConfigured() bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.requests != nil
}

func (r *RuntimeRouter) commandApprovalForSession(threadID string) bool {
	return r.approvalForSession(r.commandApprovals, threadID)
}

// writeStdinApproval mirrors Rust's terminal-input review (Rust #40978/#41328,
// unified_exec/stdin_approval.rs): when the write_stdin_approval feature is on,
// the terminal's retained launch permissions decide whether the write needs
// review, and a required review runs the PermissionRequest hooks, then the
// Guardian review, then the user approval request.
func (r *RuntimeRouter) writeStdinApproval(ctx context.Context, request *tool.WriteStdinApprovalRequest) error {
	if r == nil || request == nil {
		return nil
	}
	// Rust #41354: manual approvals shell-quote the proposed input, which cannot
	// preserve NUL bytes for an accurate review, so reject before any approval
	// request or write reaches the terminal.
	if strings.ContainsRune(request.Chars, '\x00') {
		return fmt.Errorf("terminal input contains a NUL byte and cannot be reviewed safely")
	}
	threadID := strings.TrimSpace(request.ThreadID)
	turnID := strings.TrimSpace(request.TurnID)
	cfg := r.effectiveWriteStdinConfig(threadID)
	settings := (&config.Config{Values: map[string]any{}}).FeatureSettings()
	if cfg != nil {
		settings = cfg.FeatureSettings()
	}
	if !features.Enabled(settings, "write_stdin_approval") {
		return nil
	}
	reviewer := r.approvalsReviewerForTurn(threadID, turnID)
	permissions, err := r.terminalWriteReviewRequirement(request, cfg)
	if err != nil {
		return err
	}
	// Ordinary input that still matches the retained permissions needs no
	// review unless the turn requires automatic review.
	if permissions == sandbox.SandboxPermissionsUseDefault && !reviewer.RoutesToGuardian() {
		return nil
	}
	action := terminalWriteApprovalAction(request, permissions)
	reason := terminalWriteApprovalReason(request, permissions)
	// Rust Session::request_approval: hooks decide first, before Guardian and
	// the user approval request.
	if verdict, ok := r.permissionRequestHookVerdict(ctx, threadID, turnID, strings.TrimSpace(request.CallID), "write_stdin", nil, terminalWriteHookToolInput(request)); ok && verdict != nil {
		switch verdict.Kind {
		case HookPermissionRequestAllow:
			return nil
		default:
			message := ""
			if verdict.Message != nil {
				message = strings.TrimSpace(*verdict.Message)
			}
			if message == "" {
				message = "A permission-request hook denied this terminal input."
			}
			return fmt.Errorf("%s", message)
		}
	}
	if reviewer.RoutesToGuardian() {
		outcome := r.reviewApprovalWithGuardian(ctx, threadID, turnID, strings.TrimSpace(request.CallID), action)
		if outcome.Approved {
			return nil
		}
		if reason := strings.TrimSpace(outcome.DenyReason); reason != "" {
			return fmt.Errorf("%s", reason)
		}
		return errors.New("writing input to this terminal was denied by automatic review")
	}
	params := &CommandExecutionRequestApprovalParams{
		ThreadID:      threadID,
		TurnID:        turnID,
		ItemID:        strings.TrimSpace(request.CallID),
		StartedAtMS:   uint64(time.Now().UTC().UnixMilli()),
		EnvironmentID: stringPtrIfNotEmpty(strings.TrimSpace(request.EnvironmentID)),
		Reason:        stringPtrIfNotEmpty(reason),
	}
	if params.ItemID == "" {
		params.ItemID = fmt.Sprintf("write-stdin-%d", request.ProcessID)
	}
	// Rust sends the user a command approval request whose command line names
	// the terminal session it writes to.
	command := fmt.Sprintf("write_stdin --session-id %d", request.ProcessID)
	params.Command = &command
	params.CommandActions = []map[string]any{{"type": "unknown", "command": command}}
	if request.AdditionalPermissions != nil {
		params.AdditionalPermissions = cloneAdditionalPermissionProfile(request.AdditionalPermissions)
	}
	var response CommandExecutionRequestApprovalResponse
	if err := r.requireServerRequests().Request(ctx, ServerRequestCommandExecutionApproval, params, &response); err != nil {
		return err
	}
	switch approvalDecisionString(response.Decision) {
	case string(CommandExecutionApprovalAccept), string(CommandExecutionApprovalAcceptForSession),
		string(CommandExecutionApprovalAcceptWithExecpolicyAmendment), string(CommandExecutionApprovalApplyNetworkPolicyAmendment):
		return nil
	default:
		return errors.New("Approval denied before writing terminal input.")
	}
}

// effectiveWriteStdinConfig resolves the config the terminal's thread runs
// under, so the approval feature flag and permission comparison use the same
// settings the terminal was started with.
func (r *RuntimeRouter) effectiveWriteStdinConfig(threadID string) *config.Config {
	if r == nil {
		return nil
	}
	params := &turn.TurnStartParams{ThreadID: threadID}
	if active := r.activeTurnParams(threadID); active != nil {
		cloned := *active
		params = &cloned
		params.ThreadID = threadID
	}
	if record, err := r.threadRecord(session.ThreadID(threadID), false, false); err == nil && record != nil {
		params.CWD = firstNonEmpty(strings.TrimSpace(params.CWD), strings.TrimSpace(record.Metadata.CWD))
	}
	cfg, err := r.effectiveConfigForTurn(params)
	if err != nil {
		return nil
	}
	return cfg
}

// terminalWriteReviewRequirement mirrors Rust
// TerminalPermissions::review_requirement for the subset Go models: a terminal
// whose launch policy still matches the current one needs no review, a bypassed
// or drifted terminal reports escalated permissions, and a terminal that
// retains extra grants reports the additional-permissions kind.
func (r *RuntimeRouter) terminalWriteReviewRequirement(request *tool.WriteStdinApprovalRequest, cfg *config.Config) (sandbox.SandboxPermissions, error) {
	if request == nil {
		return sandbox.SandboxPermissionsUseDefault, nil
	}
	launch := request.PermissionProfile
	if request.SandboxPermissions == sandbox.SandboxPermissionsRequireEscalated {
		if err := r.terminalWriteDriftError(request, nil); err != nil {
			return sandbox.SandboxPermissionsUseDefault, err
		}
		return sandbox.SandboxPermissionsRequireEscalated, nil
	}
	current, err := r.currentWriteStdinPermissionProfile(request, cfg)
	if err != nil {
		return sandbox.SandboxPermissionsUseDefault, err
	}
	if err := r.terminalWriteDriftError(request, current); err != nil {
		return sandbox.SandboxPermissionsUseDefault, err
	}
	// A profile that cannot be compared (no configuration service, or a launch
	// that did not record one) cannot prove drift, so only an explicit
	// escalation asks for review.
	if launch != nil && current != nil && !equalSandboxPermissionProfiles(launch, current) {
		return sandbox.SandboxPermissionsRequireEscalated, nil
	}
	if request.AdditionalPermissions != nil && (request.AdditionalPermissions.Network != nil || len(request.AdditionalPermissions.FileSystem) > 0) {
		return sandbox.SandboxPermissionsWithAdditionalPermissions, nil
	}
	return sandbox.SandboxPermissionsUseDefault, nil
}

// terminalWriteDriftError mirrors Rust TerminalPermissions::review_requirement's
// remaining fail-closed case (#46499): approval cannot retrofit denied-read
// restrictions onto a terminal whose launch policy no longer matches, so the
// caller must start a new terminal. An environment-owned network policy no
// longer fails closed here: the write is offered for escalation review, because
// the reviewer can weigh the bypass against the current restrictions.
func (r *RuntimeRouter) terminalWriteDriftError(request *tool.WriteStdinApprovalRequest, current *sandbox.PermissionProfile) error {
	if request == nil {
		return nil
	}
	bypassed := request.SandboxPermissions == sandbox.SandboxPermissionsRequireEscalated
	if current != nil && current.HasDenyReadEntries() && (bypassed || !equalSandboxPermissionProfiles(request.PermissionProfile, current)) {
		return errors.New("this terminal cannot enforce the current denied-read restrictions; start a new terminal")
	}
	return nil
}

// currentWriteStdinPermissionProfile resolves the permission profile the
// terminal's environment currently enforces.
func (r *RuntimeRouter) currentWriteStdinPermissionProfile(request *tool.WriteStdinApprovalRequest, cfg *config.Config) (*sandbox.PermissionProfile, error) {
	if cfg == nil {
		return nil, nil
	}
	cwd := ""
	if request != nil {
		cwd = strings.TrimSpace(request.CWD)
	}
	params := &turn.TurnStartParams{ThreadID: strings.TrimSpace(request.ThreadID), CWD: cwd}
	if active := r.activeTurnParams(strings.TrimSpace(request.ThreadID)); active != nil {
		// The comparison uses the terminal's own launch directory so the
		// resolved writable roots stay the same source as the launch policy;
		// only the selected profile may differ.
		params.Permissions = cloneString(active.Permissions)
		params.SandboxPolicy = active.SandboxPolicy
	}
	resolution, err := turnSandboxPermissionProfile(cfg, cwd, params)
	if err != nil || resolution == nil {
		return nil, nil
	}
	return resolution.Profile, nil
}

// terminalWriteApprovalAction builds Rust's ApprovalAction::WriteStdin for the
// review.
func terminalWriteApprovalAction(request *tool.WriteStdinApprovalRequest, permissions sandbox.SandboxPermissions) state.Action {
	if request == nil {
		return state.Action{Type: "write_stdin"}
	}
	processID := request.ProcessID
	action := state.Action{
		Type:               "write_stdin",
		EnvironmentID:      strings.TrimSpace(request.EnvironmentID),
		SessionID:          &processID,
		Chars:              request.Chars,
		CWD:                strings.TrimSpace(request.CWD),
		TurnID:             strings.TrimSpace(request.TurnID),
		SandboxPermissions: string(permissions),
	}
	tty := request.TTY
	action.TTY = &tty
	if request.AdditionalPermissions != nil {
		action.AdditionalPermissions = additionalPermissionsJSON(request.AdditionalPermissions)
	}
	return action
}

// terminalWriteApprovalReason mirrors Rust TerminalPermissions::approval_reason.
func terminalWriteApprovalReason(request *tool.WriteStdinApprovalRequest, permissions sandbox.SandboxPermissions) string {
	authority := "This terminal uses the current permissions."
	if request != nil {
		switch {
		case request.SandboxPermissions == sandbox.SandboxPermissionsRequireEscalated:
			authority = "This terminal was launched outside the sandbox, bypassing any managed network proxy."
		case request.PermissionProfile != nil && request.PermissionProfile.Disabled:
			authority = "This terminal runs without a filesystem sandbox."
		case permissions == sandbox.SandboxPermissionsWithAdditionalPermissions:
			authority = "This terminal retains additional permissions."
		case permissions == sandbox.SandboxPermissionsRequireEscalated:
			authority = "This terminal retains sandbox or network settings that differ from the current permissions."
		}
	}
	reason := "Send input to an existing terminal. " + authority +
		" The cwd is its launch directory; the terminal's current directory and state may have changed."
	if request != nil && request.AdditionalPermissions != nil {
		if grants := additionalPermissionsJSON(request.AdditionalPermissions); len(grants) > 0 {
			if encoded, err := json.Marshal(grants); err == nil {
				reason += " Retained grants: " + string(encoded) + "."
			}
		}
	}
	return reason
}

// terminalWriteHookToolInput mirrors Rust's WriteStdin PermissionRequest
// payload.
func terminalWriteHookToolInput(request *tool.WriteStdinApprovalRequest) map[string]any {
	if request == nil {
		return map[string]any{}
	}
	input := map[string]any{
		"session_id":     request.ProcessID,
		"chars":          request.Chars,
		"cwd":            strings.TrimSpace(request.CWD),
		"tty":            request.TTY,
		"environment_id": strings.TrimSpace(request.EnvironmentID),
	}
	if callID := strings.TrimSpace(request.CallID); callID != "" {
		input["approval_id"] = callID
	}
	if launchCallID := strings.TrimSpace(request.LaunchCallID); launchCallID != "" {
		input["parent_call_id"] = launchCallID
	}
	if permissions := strings.TrimSpace(string(request.SandboxPermissions)); permissions != "" {
		input["sandbox_permissions"] = permissions
	}
	return input
}

// equalSandboxPermissionProfiles compares the permission facts a terminal's
// policy is compared by (Rust FileSystemSandboxContext equality).
func equalSandboxPermissionProfiles(left *sandbox.PermissionProfile, right *sandbox.PermissionProfile) bool {
	if left == nil || right == nil {
		return left == right
	}
	if left.Disabled != right.Disabled || left.NetworkEnabled != right.NetworkEnabled {
		return false
	}
	if !equalSandboxPolicies(left.SandboxPolicy, right.SandboxPolicy) {
		return false
	}
	return equalSandboxDeniedReadEntries(left.DeniedReadEntries, right.DeniedReadEntries)
}

func equalSandboxPolicies(left *sandbox.SandboxPolicy, right *sandbox.SandboxPolicy) bool {
	if left == nil || right == nil {
		return left == right
	}
	if left.Kind != right.Kind || left.ExternalNetwork != right.ExternalNetwork ||
		left.NetworkAccess != right.NetworkAccess || left.ExcludeTmpdirEnvVar != right.ExcludeTmpdirEnvVar ||
		left.ExcludeSlashTmp != right.ExcludeSlashTmp || len(left.WritableRoots) != len(right.WritableRoots) {
		return false
	}
	for index := range left.WritableRoots {
		if left.WritableRoots[index] != right.WritableRoots[index] {
			return false
		}
	}
	return true
}

func equalSandboxDeniedReadEntries(left []sandbox.FileSystemSandboxEntry, right []sandbox.FileSystemSandboxEntry) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (r *RuntimeRouter) fileChangeApprovalForSession(threadID string) bool {
	return r.approvalForSession(r.fileApprovals, threadID)
}

func (r *RuntimeRouter) approvalForSession(cache map[string]struct{}, threadID string) bool {
	if r == nil {
		return false
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return false
	}
	r.approvalSessionsMu.RLock()
	defer r.approvalSessionsMu.RUnlock()
	_, ok := cache[threadID]
	return ok
}

func (r *RuntimeRouter) rememberCommandApprovalForSession(threadID string) {
	r.rememberApprovalForSession(r.commandApprovals, threadID)
}

func (r *RuntimeRouter) rememberFileChangeApprovalForSession(threadID string) {
	r.rememberApprovalForSession(r.fileApprovals, threadID)
}

func (r *RuntimeRouter) rememberApprovalForSession(cache map[string]struct{}, threadID string) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	r.approvalSessionsMu.Lock()
	defer r.approvalSessionsMu.Unlock()
	cache[threadID] = struct{}{}
}

func turnApprovalPolicyForTurn(cfg *config.Config, params *turn.TurnStartParams) sandbox.AskForApproval {
	if params != nil && params.ApprovalPolicy != nil {
		if policy, ok := parseTurnApprovalPolicy(params.ApprovalPolicy); ok {
			return policy
		}
	}
	if cfg != nil && cfg.Values != nil {
		if policy, ok := parseTurnApprovalPolicy(cfg.Values["approval_policy"]); ok {
			return policy
		}
	}
	return sandbox.ApprovalOnRequest
}

func parseTurnApprovalPolicy(raw any) (sandbox.AskForApproval, bool) {
	switch value := raw.(type) {
	case nil:
		return "", false
	case sandbox.AskForApproval:
		return value, value != ""
	case string:
		return normalizeTurnApprovalPolicy(value)
	case map[string]any:
		if text, ok := value["type"].(string); ok {
			return normalizeTurnApprovalPolicy(text)
		}
		if _, ok := value["granular"]; ok {
			return sandbox.ApprovalGranular, true
		}
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return "", false
		}
		var text string
		if err := json.Unmarshal(data, &text); err == nil {
			return normalizeTurnApprovalPolicy(text)
		}
		var object map[string]any
		if err := json.Unmarshal(data, &object); err == nil {
			return parseTurnApprovalPolicy(object)
		}
	}
	return "", false
}

func normalizeTurnApprovalPolicy(value string) (sandbox.AskForApproval, bool) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "_", "-")
	switch normalized {
	case string(sandbox.ApprovalNever):
		return sandbox.ApprovalNever, true
	case string(sandbox.ApprovalOnRequest), "onrequest":
		return sandbox.ApprovalOnRequest, true
	case string(sandbox.ApprovalUnlessTrusted), "unless-trusted", "unlesstrusted":
		return sandbox.ApprovalUnlessTrusted, true
	case string(sandbox.ApprovalGranular):
		return sandbox.ApprovalGranular, true
	default:
		return "", false
	}
}

func applyPatchApprovalRequiredForTurn(policy sandbox.AskForApproval) bool {
	return policy != sandbox.ApprovalNever
}

// modelIgnoresAllowPrefixRules mirrors Rust's AllowPrefixRules resolution
// (codex-rs/core/src/exec_policy/model_policy.rs and session/turn_context.rs,
// Rust e734a1a5c1): cyber-specialized models and models listed in
// auto_review.ignore_rules ignore saved allow-prefix rules, so every command
// approval is a one-time decision without a reusable policy amendment.
func (r *RuntimeRouter) modelIgnoresAllowPrefixRules(cfg *config.Config, modelID string) bool {
	modelID = strings.TrimSpace(modelID)
	if modelID != "" && cfg != nil && cfg.Requirements != nil && cfg.Requirements.AutoReview != nil {
		for _, model := range cfg.Requirements.AutoReview.IgnoreRules {
			if strings.TrimSpace(model) == modelID {
				return true
			}
		}
	}
	info := r.requireModels().Info(&model.ModelInfoReadParams{Model: modelID})
	return info != nil && info.ModelSpecialty == model.ModelSpecialtyCyber
}
func (r *RuntimeRouter) shellApprovalForTurn(threadID string, turnID string, ignoreAllowPrefixRules bool) tool.ShellApprovalFunc {
	return func(ctx context.Context, request *tool.ShellApprovalRequest) (tool.ShellApprovalDecision, error) {
		if request == nil || request.Request == nil {
			return tool.ShellApprovalDecision{}, nil
		}
		if r.commandApprovalForSession(threadID) {
			return tool.ShellApprovalDecision{Approved: true, AllowSession: true}, nil
		}
		invocation := request.Invocation
		itemID := ""
		if invocation != nil {
			itemID = strings.TrimSpace(invocation.CallID)
		}
		if itemID == "" {
			itemID = "command-" + safeIdentifier(turnID)
		}
		environmentID := "local"
		command := strings.TrimSpace(request.Request.HookCommand)
		cwd := strings.TrimSpace(request.Request.CWD)
		reason := strings.TrimSpace(request.Request.ApprovalReason)
		params := &CommandExecutionRequestApprovalParams{
			ThreadID:              strings.TrimSpace(threadID),
			TurnID:                strings.TrimSpace(turnID),
			ItemID:                itemID,
			StartedAtMS:           uint64(time.Now().UTC().UnixMilli()),
			EnvironmentID:         &environmentID,
			AdditionalPermissions: cloneAdditionalPermissionProfile(request.Request.AdditionalPermissions),
		}
		if command != "" {
			params.Command = &command
			params.CommandActions = []map[string]any{{"type": "unknown", "command": command}}
		}
		if cwd != "" {
			params.CWD = &cwd
		}
		if reason != "" {
			params.Reason = &reason
		}
		// Rust Session::request_approval: hooks decide first, before Guardian and
		// the user approval request. An allow approves the command; a deny
		// rejects it with the hook's message.
		if verdict, ok := r.permissionRequestHookVerdict(ctx, threadID, turnID, itemID, "Bash", nil, shellPermissionRequestToolInput(request.Request)); ok && verdict != nil {
			switch verdict.Kind {
			case HookPermissionRequestAllow:
				r.emitToolDecisionRecords(ctx, threadID, shellApprovalToolName(invocation), itemID,
					telemetry.ToolDecisionApproved, telemetry.ToolDecisionSourceConfig)
				return tool.ShellApprovalDecision{Approved: true}, nil
			case HookPermissionRequestDeny:
				reason := ""
				if verdict.Message != nil {
					reason = strings.TrimSpace(*verdict.Message)
				}
				r.emitToolDecisionRecords(ctx, threadID, shellApprovalToolName(invocation), itemID,
					telemetry.ToolDecisionDenied, telemetry.ToolDecisionSourceConfig)
				return tool.ShellApprovalDecision{DenyReason: reason}, nil
			}
		}
		// Rust Session::request_approval: an auto-review turn routes the command
		// through the Guardian review, which replaces the user approval request.
		if reviewer := r.approvalsReviewerForTurn(threadID, turnID); reviewer.RoutesToGuardian() {
			outcome := r.reviewApprovalWithGuardian(ctx, threadID, turnID, itemID, commandApprovalAction(request.Request))
			if outcome.Abort {
				r.emitToolDecisionRecords(ctx, threadID, shellApprovalToolName(invocation), itemID,
					telemetry.ToolDecisionAbort, telemetry.ToolDecisionSourceAutomatedReviewer)
				return tool.ShellApprovalDecision{}, fmt.Errorf("%s", outcome.DenyReason)
			}
			if outcome.Approved {
				r.emitToolDecisionRecords(ctx, threadID, shellApprovalToolName(invocation), itemID,
					telemetry.ToolDecisionApproved, telemetry.ToolDecisionSourceAutomatedReviewer)
				return tool.ShellApprovalDecision{Approved: true}, nil
			}
			r.emitToolDecisionRecords(ctx, threadID, shellApprovalToolName(invocation), itemID,
				telemetry.ToolDecisionDenied, telemetry.ToolDecisionSourceAutomatedReviewer)
			return tool.ShellApprovalDecision{DenyReason: outcome.DenyReason}, nil
		}
		// Rust e734a1a5c1: cyber-specialized models and models listed in
		// auto_review.ignore_rules get one-time decisions without proposing reusable
		// exec-policy amendments.
		if !ignoreAllowPrefixRules {
			if amendment := commandExecutionProposedExecPolicyAmendment(request.Request.PrefixRule); len(amendment) > 0 {
				params.ProposedExecPolicyAmendment = amendment
			}
		}
		var response CommandExecutionRequestApprovalResponse
		if err := r.requireServerRequests().Request(ctx, ServerRequestCommandExecutionApproval, params, &response); err != nil {
			return tool.ShellApprovalDecision{}, err
		}
		// The user's answer is the final resolution
		// (ApprovalResolutionSource::User).
		r.emitToolDecisionRecords(ctx, threadID, shellApprovalToolName(invocation), itemID,
			commandExecutionApprovalToolDecision(response.Decision), telemetry.ToolDecisionSourceUser)
		switch approvalDecisionString(response.Decision) {
		case string(CommandExecutionApprovalAccept):
			return tool.ShellApprovalDecision{Approved: true}, nil
		case string(CommandExecutionApprovalAcceptForSession):
			r.rememberCommandApprovalForSession(threadID)
			return tool.ShellApprovalDecision{Approved: true, AllowSession: true}, nil
		case string(CommandExecutionApprovalAcceptWithExecpolicyAmendment):
			// Rust 1bbfb5cfad: report the newly saved command prefix to the
			// model once after the tool completes instead of re-injecting the
			// full permissions instructions.
			if amendment := commandExecutionApprovalDecisionExecpolicyAmendment(response.Decision); len(amendment) > 0 {
				r.rememberExecPolicyAmendmentSaved(threadID, turnID, amendment)
			}
			return tool.ShellApprovalDecision{Approved: true}, nil
		case string(CommandExecutionApprovalApplyNetworkPolicyAmendment):
			if commandExecutionApprovalDecisionNetworkAction(response.Decision) == string(NetworkPolicyRuleAllow) {
				return tool.ShellApprovalDecision{Approved: true}, nil
			}
			return tool.ShellApprovalDecision{}, nil
		default:
			return tool.ShellApprovalDecision{}, nil
		}
	}
}

// shellApprovalToolName reports the tool the approval belongs to: the
// invocation's tool name, which Rust's ApprovalContext carries.
func shellApprovalToolName(invocation *tool.Invocation) tool.ToolName {
	if invocation == nil {
		return tool.ToolName{}
	}
	return invocation.ToolName
}

// commandExecutionApprovalToolDecision maps one command approval answer onto the
// opaque decision string Rust's ReviewDecision reports; an answer Go does not
// recognize reports nothing.
func commandExecutionApprovalToolDecision(raw any) string {
	switch decision := approvalDecisionString(raw); decision {
	case string(CommandExecutionApprovalAccept):
		return telemetry.ToolDecisionApproved
	case string(CommandExecutionApprovalAcceptForSession):
		return telemetry.ToolDecisionApprovedForSession
	case string(CommandExecutionApprovalAcceptWithExecpolicyAmendment):
		return telemetry.ToolDecisionApprovedWithAmendment
	case string(CommandExecutionApprovalDecline):
		return telemetry.ToolDecisionDenied
	case string(CommandExecutionApprovalCancel):
		return telemetry.ToolDecisionAbort
	case string(CommandExecutionApprovalApplyNetworkPolicyAmendment):
		if commandExecutionApprovalDecisionNetworkAction(raw) == string(NetworkPolicyRuleAllow) {
			return telemetry.ToolDecisionApprovedWithNetworkPolicyAllow
		}
		return telemetry.ToolDecisionDeniedWithNetworkPolicyDeny
	default:
		return ""
	}
}

func commandExecutionProposedExecPolicyAmendment(prefixRule []string) []string {
	out := make([]string, 0, len(prefixRule))
	for _, value := range prefixRule {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func (r *RuntimeRouter) applyPatchApprovalForTurn(threadID string, turnID string) tool.ApplyPatchApprovalFunc {
	return func(ctx context.Context, request *tool.ApplyPatchApprovalRequest) (tool.ApplyPatchApprovalDecision, error) {
		if request == nil {
			return tool.ApplyPatchApprovalDecision{}, nil
		}
		if r.fileChangeApprovalForSession(threadID) {
			return tool.ApplyPatchApprovalDecision{Approved: true, AllowSession: true}, nil
		}
		itemID := ""
		if request.Invocation != nil {
			itemID = strings.TrimSpace(request.Invocation.CallID)
		}
		if itemID == "" {
			itemID = "patch-" + safeIdentifier(turnID)
		}
		// Rust Session::request_approval: hooks decide first, before Guardian and
		// the user approval request.
		if verdict, ok := r.permissionRequestHookVerdict(ctx, threadID, turnID, itemID, "apply_patch", []string{"Write", "Edit"}, applyPatchPermissionRequestToolInput(request.Patch)); ok && verdict != nil {
			switch verdict.Kind {
			case HookPermissionRequestAllow:
				r.emitToolDecisionRecords(ctx, threadID, applyPatchApprovalToolName(request.Invocation), itemID,
					telemetry.ToolDecisionApproved, telemetry.ToolDecisionSourceConfig)
				return tool.ApplyPatchApprovalDecision{Approved: true}, nil
			case HookPermissionRequestDeny:
				reason := ""
				if verdict.Message != nil {
					reason = strings.TrimSpace(*verdict.Message)
				}
				r.emitToolDecisionRecords(ctx, threadID, applyPatchApprovalToolName(request.Invocation), itemID,
					telemetry.ToolDecisionDenied, telemetry.ToolDecisionSourceConfig)
				return tool.ApplyPatchApprovalDecision{DenyReason: reason}, nil
			}
		}
		// Rust Session::request_approval: an auto-review turn routes the patch
		// through the Guardian review, which replaces the user approval request.
		if reviewer := r.approvalsReviewerForTurn(threadID, turnID); reviewer.RoutesToGuardian() {
			outcome := r.reviewApprovalWithGuardian(ctx, threadID, turnID, itemID, applyPatchApprovalAction(request))
			if outcome.Abort {
				r.emitToolDecisionRecords(ctx, threadID, applyPatchApprovalToolName(request.Invocation), itemID,
					telemetry.ToolDecisionAbort, telemetry.ToolDecisionSourceAutomatedReviewer)
				return tool.ApplyPatchApprovalDecision{}, fmt.Errorf("%s", outcome.DenyReason)
			}
			if outcome.Approved {
				r.emitToolDecisionRecords(ctx, threadID, applyPatchApprovalToolName(request.Invocation), itemID,
					telemetry.ToolDecisionApproved, telemetry.ToolDecisionSourceAutomatedReviewer)
				return tool.ApplyPatchApprovalDecision{Approved: true}, nil
			}
			r.emitToolDecisionRecords(ctx, threadID, applyPatchApprovalToolName(request.Invocation), itemID,
				telemetry.ToolDecisionDenied, telemetry.ToolDecisionSourceAutomatedReviewer)
			return tool.ApplyPatchApprovalDecision{DenyReason: outcome.DenyReason}, nil
		}
		params := &FileChangeRequestApprovalParams{
			ThreadID:    strings.TrimSpace(threadID),
			TurnID:      strings.TrimSpace(turnID),
			ItemID:      itemID,
			StartedAtMS: uint64(time.Now().UTC().UnixMilli()),
		}
		var response FileChangeRequestApprovalResponse
		if err := r.requireServerRequests().Request(ctx, ServerRequestFileChangeApproval, params, &response); err != nil {
			return tool.ApplyPatchApprovalDecision{}, err
		}
		r.emitToolDecisionRecords(ctx, threadID, applyPatchApprovalToolName(request.Invocation), itemID,
			fileChangeApprovalToolDecision(response.Decision), telemetry.ToolDecisionSourceUser)
		switch approvalDecisionString(response.Decision) {
		case string(FileChangeApprovalAccept):
			return tool.ApplyPatchApprovalDecision{Approved: true}, nil
		case string(FileChangeApprovalAcceptForSession):
			r.rememberFileChangeApprovalForSession(threadID)
			return tool.ApplyPatchApprovalDecision{Approved: true, AllowSession: true}, nil
		default:
			return tool.ApplyPatchApprovalDecision{}, nil
		}
	}
}

// applyPatchApprovalToolName reports the tool a patch approval belongs to.
func applyPatchApprovalToolName(invocation *tool.Invocation) tool.ToolName {
	if invocation == nil {
		return tool.ToolName{}
	}
	return invocation.ToolName
}

// fileChangeApprovalToolDecision maps one file-change approval answer onto the
// opaque decision string Rust's ReviewDecision reports.
func fileChangeApprovalToolDecision(raw any) string {
	switch decision := approvalDecisionString(raw); decision {
	case string(FileChangeApprovalAccept):
		return telemetry.ToolDecisionApproved
	case string(FileChangeApprovalAcceptForSession):
		return telemetry.ToolDecisionApprovedForSession
	case string(FileChangeApprovalDecline):
		return telemetry.ToolDecisionDenied
	case string(FileChangeApprovalCancel):
		return telemetry.ToolDecisionAbort
	default:
		return ""
	}
}

func approvalDecisionString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case CommandExecutionApprovalDecision:
		return string(typed)
	case FileChangeApprovalDecision:
		return string(typed)
	default:
		if kind := commandExecutionApprovalDecisionObjectKind(typed); kind != "" {
			return kind
		}
		data, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		var text string
		if err := json.Unmarshal(data, &text); err == nil {
			return strings.TrimSpace(text)
		}
		var object map[string]any
		if err := json.Unmarshal(data, &object); err == nil {
			return commandExecutionApprovalDecisionMapKind(object)
		}
		return ""
	}
}

func commandExecutionApprovalDecisionObjectKind(value any) string {
	object, ok := commandExecutionApprovalDecisionObject(value)
	if !ok {
		return ""
	}
	return commandExecutionApprovalDecisionMapKind(object)
}

func commandExecutionApprovalDecisionMapKind(object map[string]any) string {
	if object == nil {
		return ""
	}
	if _, ok := object[string(CommandExecutionApprovalAcceptWithExecpolicyAmendment)]; ok {
		return string(CommandExecutionApprovalAcceptWithExecpolicyAmendment)
	}
	if _, ok := object[string(CommandExecutionApprovalApplyNetworkPolicyAmendment)]; ok {
		return string(CommandExecutionApprovalApplyNetworkPolicyAmendment)
	}
	return ""
}

func commandExecutionApprovalDecisionNetworkAction(value any) string {
	object, ok := commandExecutionApprovalDecisionObject(value)
	if !ok {
		return ""
	}
	payload, ok := object[string(CommandExecutionApprovalApplyNetworkPolicyAmendment)]
	if !ok || payload == nil {
		return ""
	}
	payloadMap, ok := commandExecutionApprovalDecisionObject(payload)
	if !ok {
		return ""
	}
	amendment := firstNonNil(payloadMap["network_policy_amendment"], payloadMap["networkPolicyAmendment"])
	amendmentMap, ok := commandExecutionApprovalDecisionObject(amendment)
	if !ok {
		return ""
	}
	return strings.TrimSpace(stringFromAny(amendmentMap["action"]))
}

func commandExecutionApprovalDecisionExecpolicyAmendment(value any) []string {
	object, ok := commandExecutionApprovalDecisionObject(value)
	if !ok {
		return nil
	}
	payload, ok := object[string(CommandExecutionApprovalAcceptWithExecpolicyAmendment)]
	if !ok || payload == nil {
		return nil
	}
	payloadMap, ok := commandExecutionApprovalDecisionObject(payload)
	if !ok {
		return nil
	}
	amendment := firstNonNil(payloadMap["execpolicy_amendment"], payloadMap["execpolicyAmendment"])
	if amendment == nil {
		return nil
	}
	if values, ok := amendment.([]string); ok {
		return append([]string(nil), values...)
	}
	if values, ok := amendment.([]any); ok {
		out := make([]string, 0, len(values))
		for _, value := range values {
			if token := strings.TrimSpace(stringFromAny(value)); token != "" {
				out = append(out, token)
			}
		}
		return out
	}
	return nil
}

func commandExecutionApprovalDecisionObject(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return typed, true
	default:
		return nil, false
	}
}

func requestUserInputAvailableModesForTurn(cfg *config.Config) []string {
	if requestUserInputDefaultModeEnabled(cfg) {
		return []string{"Default", "Plan"}
	}
	return []string{"Plan"}
}

func requestUserInputDefaultModeEnabled(cfg *config.Config) bool {
	return cfg != nil && features.Enabled(cfg.FeatureSettings(), "default_mode_request_user_input")
}

func turnSandboxPermissionProfile(cfg *config.Config, cwd string, params *turn.TurnStartParams) (*config.SandboxPermissionProfileResolution, error) {
	// Rust TurnContext::permission_profile (#46568): the primary selected
	// environment's own profile wins over the thread defaults (including an
	// explicit turn sandbox policy, which lands in the thread config).
	if resolution := turnEnvironmentSelectionPermissionProfileResolution(params); resolution != nil {
		return resolution, nil
	}
	return threadDefaultSandboxPermissionProfile(cfg, cwd, params)
}

// threadDefaultSandboxPermissionProfile resolves the thread-level profile for a
// turn: an explicit turn sandbox policy wins, otherwise the configured profile.
// The profile is materialized with the captured primary selection's workspace
// roots, so a starting or failed attachment still anchors project-root
// restrictions at its workspace (Rust #46568).
func threadDefaultSandboxPermissionProfile(cfg *config.Config, cwd string, params *turn.TurnStartParams) (*config.SandboxPermissionProfileResolution, error) {
	if params != nil && turnStartSandboxPolicyPresent(params.SandboxPolicy) {
		profileID, profile, err := turnSandboxPolicyPermissionProfile(params.SandboxPolicy)
		if err != nil {
			return nil, err
		}
		raw, err := sandbox.RuntimePermissionProfileJSON(*profile)
		if err != nil {
			return nil, err
		}
		return &config.SandboxPermissionProfileResolution{ID: profileID, Profile: profile, ProfileJSON: raw}, nil
	}
	profileID := ""
	if params != nil && params.Permissions != nil {
		profileID = strings.TrimSpace(*params.Permissions)
	}
	if cfg == nil {
		return nil, nil
	}
	// Rust TurnContext::permission_profile_for_environments (#46568): the thread
	// defaults are materialized with the captured primary selection's workspace
	// roots, including while the attachment is starting or after it failed.
	return cfg.ResolveSandboxPermissionProfileWithWorkspaceRoots(profileID, cwd, turnEnvironmentWorkspaceRoots(params, cwd))
}

func turnSandboxPolicyPermissionProfile(raw any) (string, *sandbox.PermissionProfile, error) {
	policy, err := parseTurnSandboxPolicy(raw)
	if err != nil {
		return "", nil, err
	}
	if policy == nil {
		return "", nil, nil
	}
	switch policy.Kind {
	case sandbox.SandboxDangerFullAccess:
		profile := sandbox.FullAccessPermissionProfile()
		return sandbox.BuiltInPermissionProfileDangerFullAccess, &profile, nil
	case sandbox.SandboxReadOnly:
		profile := sandbox.PermissionProfile{SandboxPolicy: policy, NetworkEnabled: policy.HasFullNetworkAccess()}
		return sandbox.BuiltInPermissionProfileReadOnly, &profile, nil
	case sandbox.SandboxWorkspaceWrite, "":
		policy.Kind = sandbox.SandboxWorkspaceWrite
		profile := sandbox.PermissionProfile{SandboxPolicy: policy, NetworkEnabled: policy.HasFullNetworkAccess()}
		return sandbox.BuiltInPermissionProfileWorkspace, &profile, nil
	default:
		profile := sandbox.PermissionProfile{SandboxPolicy: policy, NetworkEnabled: policy.HasFullNetworkAccess()}
		return string(policy.Kind), &profile, nil
	}
}

func parseTurnSandboxPolicy(raw any) (*sandbox.SandboxPolicy, error) {
	switch value := raw.(type) {
	case nil:
		return nil, nil
	case *sandbox.SandboxPolicy:
		if value == nil {
			return nil, nil
		}
		clone := *value
		clone.WritableRoots = append([]string(nil), value.WritableRoots...)
		return &clone, nil
	case sandbox.SandboxPolicy:
		clone := value
		clone.WritableRoots = append([]string(nil), value.WritableRoots...)
		return &clone, nil
	case string:
		mode, err := sandbox.ParseSandboxMode(value)
		if err != nil {
			return nil, err
		}
		return sandbox.SandboxPolicyFromMode(mode)
	case map[string]any:
		if mode := turnSandboxPolicyMode(value); mode != "" {
			normalized := make(map[string]any, len(value)+1)
			for key, entry := range value {
				normalized[key] = entry
			}
			if _, ok := normalized["type"]; !ok {
				normalized["type"] = mode
			}
			raw = normalized
		}
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var policy sandbox.SandboxPolicy
	if err := json.Unmarshal(data, &policy); err != nil {
		return nil, err
	}
	return &policy, nil
}

func turnSandboxPolicyMode(values map[string]any) string {
	for _, key := range []string{"mode", "sandboxMode", "sandbox_mode"} {
		if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (r *RuntimeRouter) mcpRuntimeInputsForTurn(threadID string, cfg *config.Config) ([]mcp.RuntimeToolInfo, []mcp.RuntimeConnector) {
	return r.mcpRuntimeInputsForService(threadID, cfg, r.mcpServiceForThread(threadID, cfg))
}

func (r *RuntimeRouter) mcpRuntimeInputsForService(threadID string, cfg *config.Config, service *mcp.MCPService) ([]mcp.RuntimeToolInfo, []mcp.RuntimeConnector) {
	return r.mcpRuntimeInputsForServiceWithRequired(threadID, cfg, service, nil)
}

func (r *RuntimeRouter) mcpRuntimeInputsForServiceWithRequired(threadID string, cfg *config.Config, service *mcp.MCPService, requiredServers []string) ([]mcp.RuntimeToolInfo, []mcp.RuntimeConnector) {
	return r.mcpRuntimeInputsForServiceWithRequirements(threadID, cfg, service, requiredServers, nil)
}

func (r *RuntimeRouter) mcpRuntimeInputsForServiceWithRequirements(threadID string, cfg *config.Config, service *mcp.MCPService, requiredServers []string, requiredPlugins []string) ([]mcp.RuntimeToolInfo, []mcp.RuntimeConnector) {
	if r == nil || service == nil {
		return nil, nil
	}
	// Rust #41199: the optional MCP startup grace is configurable; a zero value
	// disables the shared grace (servers wait for their own startup timeout).
	optionalStartupGrace := config.DefaultMCPOptionalStartupGrace
	if cfg != nil {
		optionalStartupGrace = cfg.MCPOptionalStartupGrace()
	}
	response, err := service.ListStatusChecked(&mcp.MCPListServerStatusParams{
		ThreadID:             stringPtrIfNotEmpty(strings.TrimSpace(threadID)),
		Detail:               &mcp.MCPServerStatusDetail{Mode: mcp.MCPServerStatusDetailToolsAndAuthOnly},
		RequiredServers:      append([]string(nil), requiredServers...),
		RequiredPlugins:      append([]string(nil), requiredPlugins...),
		NonBlockingOptional:  true,
		OptionalStartupGrace: optionalStartupGrace,
	})
	if err != nil || response == nil {
		return nil, nil
	}
	tools := mcp.RuntimeToolsFromStatuses(response.Data)
	if len(tools) == 0 {
		return nil, nil
	}
	if cfg != nil {
		for i := range tools {
			tools[i].OmitLegacyPrefix = cfg.OmitLegacyMCPToolPrefix(tools[i].ServerName)
		}
	}
	tools = r.annotateRuntimeMCPToolsWithPluginSources(tools)
	tools = filterCodexAppsRuntimeTools(tools, cfg)
	return tools, r.mcpRuntimeConnectorsForTurn(threadID, cfg)
}

// filterCodexAppsRuntimeTools ports Rust
// mcp_tool_exposure::filter_codex_apps_mcp_tools: a Codex Apps tool is exposed
// to the model only when it is model-visible, carries a connector id, and
// satisfies the app configuration's enablement policy (destructive/open-world
// hints included). Non-app tools are untouched.
func filterCodexAppsRuntimeTools(tools []mcp.RuntimeToolInfo, cfg *config.Config) []mcp.RuntimeToolInfo {
	if len(tools) == 0 {
		return tools
	}
	var evaluator *apps.AppToolPolicyEvaluator
	if cfg != nil {
		evaluator = apps.NewAppToolPolicyEvaluatorWithRequirements(
			apps.AppsConfigFromValues(cfg.Values),
			appsRequirementsForConfig(cfg),
		)
	}
	filtered := make([]mcp.RuntimeToolInfo, 0, len(tools))
	for _, tool := range tools {
		if !mcp.IsCodexAppsMCPServerName(tool.ServerName) {
			filtered = append(filtered, tool)
			continue
		}
		if !(&tool).IsModelVisible() {
			continue
		}
		connectorID := strings.TrimSpace(tool.ConnectorID)
		if connectorID == "" {
			continue
		}
		if evaluator != nil {
			annotations := tool.Tool.Annotations
			var destructiveHint, openWorldHint *bool
			if annotations != nil {
				destructiveHint = annotations.DestructiveHint
				openWorldHint = annotations.OpenWorldHint
			}
			policy := evaluator.Policy(apps.AppToolPolicyInput{
				ConnectorID:     connectorID,
				ToolName:        tool.Tool.Name,
				ToolTitle:       tool.Tool.Title,
				DestructiveHint: destructiveHint,
				OpenWorldHint:   openWorldHint,
			})
			if !policy.Enabled {
				continue
			}
		}
		filtered = append(filtered, tool)
	}
	return filtered
}

func (r *RuntimeRouter) requiredMCPServersForTurn(threadID string, cfg *config.Config, params *turn.TurnStartParams) []string {
	required := map[string]bool{}
	add := func(values ...string) {
		for _, value := range values {
			if value = strings.TrimSpace(value); value != "" {
				required[value] = true
			}
		}
	}
	if params != nil {
		add(mcpServerNamesFromTurnText(params.Prompt)...)
		for _, input := range params.Input {
			if strings.EqualFold(strings.TrimSpace(input.Type), "mention") {
				add(mcpServerNameFromMentionPath(input.Path))
			}
			add(mcpServerNamesFromTurnText(input.Text)...)
			if strings.EqualFold(strings.TrimSpace(input.Type), "skill") && strings.HasPrefix(strings.TrimSpace(input.Path), "skill://") {
				add(mcp.RuntimeCodexAppsMCPServerName)
			}
		}
	}

	capabilities := []plugin.CapabilitySummary(nil)
	if r != nil && r.services.Plugins != nil {
		capabilities = r.pluginCapabilitiesForThread(threadID)
		for _, capability := range plugin.CollectExplicitPluginMentions(pluginUserInputFromTurn(params), capabilities) {
			add(capability.MCPServers...)
		}
	}

	metadata := r.skillMetadataForMCPRequirements(threadID, params)
	selected := promptctx.CollectExplicitSkillMentions(&promptctx.ExplicitSkillMentionOptions{
		Inputs: skillMentionInputsFromTurn(params), Skills: metadata,
	})
	for _, skill := range selected {
		for _, dependency := range skill.Dependencies {
			if strings.EqualFold(strings.TrimSpace(dependency.Type), "mcp") {
				add(dependency.Value)
			}
		}
		if strings.EqualFold(strings.TrimSpace(skill.AuthorityKind), "orchestrator") {
			add(mcp.RuntimeCodexAppsMCPServerName)
		}
		for _, capability := range capabilities {
			if skill.PluginID == capability.ConfigName || skill.RemotePluginID == capability.RemotePluginID {
				add(capability.MCPServers...)
			}
		}
	}

	out := make([]string, 0, len(required))
	for name := range required {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// requiredPluginsForTurn returns the exact plugin IDs explicitly mentioned in
// the turn input. These IDs are resolved against selected-plugin MCP servers
// during catalog capture so a user-mentioned plugin finishes startup even when
// its server names are not yet known from the loaded plugin capabilities.
func (r *RuntimeRouter) requiredPluginsForTurn(threadID string, cfg *config.Config, params *turn.TurnStartParams) []string {
	if r == nil || r.services.Plugins == nil {
		return nil
	}
	if params == nil {
		return nil
	}
	mentioned := plugin.CollectExplicitPluginIDs(pluginUserInputFromTurn(params))
	if len(mentioned) == 0 {
		return nil
	}
	out := make([]string, 0, len(mentioned))
	for id := range mentioned {
		if id = strings.TrimSpace(id); id != "" {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

func (r *RuntimeRouter) skillMetadataForMCPRequirements(threadID string, params *turn.TurnStartParams) []promptctx.InstructionsSkillMetadata {
	if r == nil || r.services.Skills == nil {
		return nil
	}
	listParams := &SkillsListParams{}
	if params != nil {
		sessionConfig := &config.Config{Values: map[string]any{}}
		applyRuntimeConfigOverrides(sessionConfig, turnConfigOverrides(params))
		listParams.Config = skillConfigEntriesFromValues(sessionConfig.Values)
		if cwd := turnCWD(params); cwd != "" {
			listParams.CWDs = []string{cwd}
		}
	}
	response, err := r.services.Skills.List(listParams)
	if err != nil || response == nil {
		return nil
	}
	entries := cloneSkills(response.Skills)
	if r.services.WorkspaceCodexPluginsEnabled == nil || *r.services.WorkspaceCodexPluginsEnabled {
		if pluginEntries, pluginErr := r.pluginSkillEntriesForRuntime(threadID); pluginErr == nil {
			if pluginEntries, pluginErr = r.services.Skills.applyConfigEntries(pluginEntries, listParams.Config); pluginErr == nil {
				entries = append(entries, pluginEntries...)
			}
		}
	}
	if capabilityEntries, _, capabilityErr := r.selectedCapabilitySkillEntriesForRuntime(threadID); capabilityErr == nil {
		if capabilityEntries, capabilityErr = r.services.Skills.applyConfigEntries(capabilityEntries, listParams.Config); capabilityErr == nil {
			entries = append(entries, capabilityEntries...)
		}
	}
	metadata := promptSkillMetadataFromEntries(entries)
	custom, _ := r.customSkillMetadataForRuntime(context.Background(), "")
	return append(metadata, custom...)
}

func mcpServerNamesFromTurnText(text string) []string {
	const prefix = "mcp://"
	out := []string{}
	for remaining := text; ; {
		index := strings.Index(remaining, prefix)
		if index < 0 {
			break
		}
		remaining = remaining[index+len(prefix):]
		end := strings.IndexFunc(remaining, func(character rune) bool {
			return character == '/' || character == '\\' || character == ')' || character == ']' || character == '}' || character == '>' || character == '"' || character == '\'' || character == '`' || character == ',' || character == ';' || character == ':' || character == '?' || character == '#' || character == '&' || character == '=' || character == ' ' || character == '\t' || character == '\r' || character == '\n'
		})
		name := remaining
		if end >= 0 {
			name = remaining[:end]
			remaining = remaining[end:]
		} else {
			remaining = ""
		}
		if name = strings.TrimSpace(name); name != "" {
			out = append(out, name)
		}
		if remaining == "" {
			break
		}
	}
	return out
}

func mcpServerNameFromMentionPath(path string) string {
	path = strings.TrimSpace(path)
	if !strings.HasPrefix(path, "mcp://") {
		return ""
	}
	names := mcpServerNamesFromTurnText(path)
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

func (r *RuntimeRouter) mcpRuntimeConnectorsForTurn(threadID string, cfg *config.Config) []mcp.RuntimeConnector {
	if r == nil {
		return nil
	}
	appsByID := r.appsForExplicitMentions(threadID, cfg)
	if len(appsByID) == 0 {
		return nil
	}
	ids := make([]string, 0, len(appsByID))
	for id := range appsByID {
		if strings.TrimSpace(id) != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	connectors := make([]mcp.RuntimeConnector, 0, len(ids))
	for _, id := range ids {
		app := appsByID[id]
		connectors = append(connectors, mcp.RuntimeConnector{
			ID:      id,
			Name:    firstNonEmpty(strings.TrimSpace(app.Name), id),
			Enabled: appEnabledForRuntime(&app),
		})
	}
	return connectors
}

func (r *RuntimeRouter) annotateRuntimeMCPToolsWithPluginSources(tools []mcp.RuntimeToolInfo) []mcp.RuntimeToolInfo {
	if len(tools) == 0 {
		return nil
	}
	out := make([]mcp.RuntimeToolInfo, len(tools))
	copy(out, tools)
	var provenance *mcp.ConnectorPluginProvenance
	if r != nil && r.services.Plugins != nil {
		provenance = mcp.NewConnectorPluginProvenance()
		for _, connector := range appPluginConnectorsFromCapabilities(r.services.Plugins.EnabledCapabilities()) {
			provenance.Add(connector.ID, connector.PluginDisplayName)
		}
	}
	for i := range out {
		if out[i].ServerName != mcp.RuntimeCodexAppsMCPServerName || strings.TrimSpace(out[i].ConnectorID) == "" {
			continue
		}
		var provenanceNames []string
		if provenance != nil {
			provenanceNames = provenance.Names(out[i].ConnectorID)
		}
		names := mergeRuntimePluginDisplayNames(out[i].PluginDisplayNames, provenanceNames)
		mcp.AnnotateRuntimeToolWithPluginProvenance(&out[i], names)
	}
	return out
}

func mergeRuntimePluginDisplayNames(left []string, right []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(left)+len(right))
	for _, value := range append(append([]string(nil), left...), right...) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func (r *RuntimeRouter) contextStatusForThread(threadID string) func() compact.TokenStatus {
	return func() compact.TokenStatus {
		if r == nil || r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil || strings.TrimSpace(threadID) == "" {
			return compact.TokenStatus{}
		}
		record, err := r.threadRecord(session.ThreadID(threadID), true, false)
		if err != nil || record == nil {
			return compact.TokenStatus{}
		}
		return compactTokenStatusFromMetadata(record.Metadata.Extra)
	}
}

// newContextWindowRequesterForTurn mirrors Rust's Feature::TokenBudget gate for
// the new_context tool: the model can request a fresh context window only when
// the token-budget feature is enabled for the turn.
func (r *RuntimeRouter) newContextWindowRequesterForTurn(threadID string, cfg *config.Config) func() {
	if r == nil || cfg == nil || (!features.Enabled(cfg.FeatureSettings(), "token_budget") && !r.experimentalContextManagementEligible(cfg, nil)) {
		return nil
	}
	return func() {
		r.requestNewContextWindow(threadID)
	}
}

func (r *RuntimeRouter) requestNewContextWindow(threadID string) {
	if r == nil {
		return
	}
	r.newContextWindowMu.Lock()
	defer r.newContextWindowMu.Unlock()
	if r.newContextWindowReq == nil {
		r.newContextWindowReq = map[string]bool{}
	}
	r.newContextWindowReq[threadID] = true
}

func (r *RuntimeRouter) takeNewContextWindowRequest(threadID string) bool {
	if r == nil {
		return false
	}
	r.newContextWindowMu.Lock()
	defer r.newContextWindowMu.Unlock()
	requested := r.newContextWindowReq[threadID]
	delete(r.newContextWindowReq, threadID)
	return requested
}

// contextWindowIDForThread returns the stable model-visible context window ID
// for a thread, creating it on first use (#39847). It advances after compaction.
func (r *RuntimeRouter) contextWindowIDForThread(threadID string) string {
	if r == nil || strings.TrimSpace(threadID) == "" {
		return ""
	}
	r.contextWindowMu.Lock()
	defer r.contextWindowMu.Unlock()
	if r.contextWindowIDs == nil {
		r.contextWindowIDs = map[string]string{}
	}
	if id := r.contextWindowIDs[threadID]; id != "" {
		return id
	}
	id := newContextWindowID()
	r.contextWindowIDs[threadID] = id
	return id
}

// windowNumberForThread returns the zero-based context window number for a
// thread, creating it at 0 on first use and advancing on compaction (Rust
// #40987 Track window and fork positions in turn metadata).
func (r *RuntimeRouter) windowNumberForThread(threadID string) uint64 {
	if r == nil || strings.TrimSpace(threadID) == "" {
		return 0
	}
	r.contextWindowMu.Lock()
	defer r.contextWindowMu.Unlock()
	if r.windowNumbers == nil {
		r.windowNumbers = map[string]uint64{}
	}
	return r.windowNumbers[threadID]
}

func uint64PtrAppserver(value uint64) *uint64 {
	return &value
}

// advanceWindowNumber increments a thread's context window number after
// compaction, mirroring Rust window advancement (#40987).
func (r *RuntimeRouter) advanceWindowNumber(threadID string) {
	if r == nil || strings.TrimSpace(threadID) == "" {
		return
	}
	r.contextWindowMu.Lock()
	defer r.contextWindowMu.Unlock()
	if r.windowNumbers == nil {
		r.windowNumbers = map[string]uint64{}
	}
	r.windowNumbers[threadID]++
}

// advanceContextWindowID regenerates a thread's context window ID after
// compaction, mirroring Rust #39847 where the ID advances on compaction.
func (r *RuntimeRouter) advanceContextWindowID(threadID string) {
	if r == nil || strings.TrimSpace(threadID) == "" {
		return
	}
	r.contextWindowMu.Lock()
	defer r.contextWindowMu.Unlock()
	if r.contextWindowIDs == nil {
		r.contextWindowIDs = map[string]string{}
	}
	r.contextWindowIDs[threadID] = newContextWindowID()
}

func newContextWindowID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func (r *RuntimeRouter) userInputResponderForTurn(threadID string, turnID string) tool.UserInputResponder {
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	return func(ctx context.Context, args *tool.RequestUserInputArgs) (*tool.UserInputResponse, error) {
		if r == nil {
			return nil, fmt.Errorf("%w: runtime router is nil", ErrInvalidRequest)
		}
		if threadID == "" {
			return nil, fmt.Errorf("%w: threadId is required", ErrInvalidRequest)
		}
		if err := args.Normalize(); err != nil {
			return nil, err
		}
		// Rust TurnMetadataState::mark_user_input_requested_during_turn: the MCP
		// metadata reports that the model asked the user for input this turn.
		r.markTurnUserInputRequested(threadID, turnID)
		guard, notification := r.requireThreadStatus().NoteUserInputRequestedWithNotification(threadID)
		r.notifyThreadStatus(notification)
		defer func() {
			r.notifyThreadStatus(guard.Release())
		}()

		params := toolRequestUserInputParamsFromArgs(threadID, turnID, args)
		var response ToolRequestUserInputResponse
		if err := r.requireServerRequests().Request(ctx, ServerRequestToolUserInput, params, &response); err != nil {
			return nil, err
		}
		return toolUserInputResponseToToolResponse(&response), nil
	}
}

func toolRequestUserInputParamsFromArgs(threadID string, turnID string, args *tool.RequestUserInputArgs) *ToolRequestUserInputParams {
	if args == nil {
		args = &tool.RequestUserInputArgs{}
	}
	params := &ToolRequestUserInputParams{
		ThreadID:  strings.TrimSpace(threadID),
		TurnID:    strings.TrimSpace(turnID),
		ItemID:    "request-user-input-" + safeIdentifier(firstNonEmpty(turnID, threadID)),
		Questions: make([]ToolRequestUserInputQuestion, 0, len(args.Questions)),
	}
	if args.AutoResolutionMS != nil {
		value := uint64(*args.AutoResolutionMS)
		params.AutoResolutionMS = &value
	}
	for i := range args.Questions {
		params.Questions = append(params.Questions, toolRequestUserInputQuestionFromToolQuestion(&args.Questions[i]))
	}
	return params
}

func toolRequestUserInputQuestionFromToolQuestion(question *tool.UserInputQuestion) ToolRequestUserInputQuestion {
	if question == nil {
		return ToolRequestUserInputQuestion{}
	}
	out := ToolRequestUserInputQuestion{
		ID:       strings.TrimSpace(question.ID),
		Header:   strings.TrimSpace(question.Header),
		Question: strings.TrimSpace(question.Question),
		IsOther:  question.IsOther,
		IsSecret: question.IsSecret,
		Options:  make([]ToolRequestUserInputOption, 0, len(question.Options)),
	}
	for i := range question.Options {
		out.Options = append(out.Options, ToolRequestUserInputOption{
			Label:       question.Options[i].Label,
			Description: question.Options[i].Description,
		})
	}
	return out
}

func toolUserInputResponseToToolResponse(response *ToolRequestUserInputResponse) *tool.UserInputResponse {
	out := &tool.UserInputResponse{
		Answers:           map[string]string{},
		StructuredAnswers: map[string][]string{},
	}
	if response == nil {
		return out
	}
	for key, answer := range response.Answers {
		out.StructuredAnswers[key] = append([]string(nil), answer.Answers...)
		for _, value := range answer.Answers {
			value = strings.TrimSpace(value)
			if value != "" {
				out.Answers[key] = value
				break
			}
		}
		if _, ok := out.Answers[key]; !ok {
			out.Answers[key] = ""
		}
	}
	return out
}

type dynamicToolServerRequestCaller struct {
	broker *ServerRequestBroker
}

func (c dynamicToolServerRequestCaller) Request(ctx context.Context, method string, params any, target any) error {
	if c.broker == nil {
		return fmt.Errorf("%w: server request broker is nil", ErrInvalidRequest)
	}
	return c.broker.Request(ctx, ServerRequestMethod(method), params, target)
}

func (r *RuntimeRouter) externalAuthRefresh(ctx context.Context, request *model.ExternalAuthRefreshRequest) (*model.ExternalAuthRefreshResponse, error) {
	if request == nil {
		return nil, fmt.Errorf("%w: external auth refresh request is nil", ErrInvalidRequest)
	}
	params := &auth.ChatGPTAuthTokensRefreshParams{
		Reason: auth.ChatGPTAuthTokensRefreshReason(request.Reason),
	}
	if strings.TrimSpace(request.PreviousAccountID) != "" {
		value := strings.TrimSpace(request.PreviousAccountID)
		params.PreviousAccountID = &value
	}
	var response auth.ChatGPTAuthTokensRefreshResponse
	if err := r.requireServerRequests().Request(ctx, ServerRequestChatGPTAuthTokensRefresh, params, &response); err != nil {
		return nil, err
	}
	if strings.TrimSpace(response.AccessToken) == "" || strings.TrimSpace(response.ChatGPTAccountID) == "" {
		return nil, fmt.Errorf("%w: external auth refresh response omitted accessToken or chatgptAccountId", ErrInvalidRequest)
	}
	// Rust #39322: header credentials must stay bound to a validated ChatGPT
	// workspace. A refresh that returns a missing or disallowed account ID is
	// rejected without replacing the previously cached authentication.
	if r.services.Config != nil {
		if read, err := r.services.Config.Read(&config.ConfigReadParams{}); err == nil && read != nil {
			cfg := &config.Config{Values: read.Config}
			workspaces := cfg.ForcedChatGPTWorkspaceIDs()
			if err := auth.EnsureWorkspaceAccountAllowed(workspaces, response.ChatGPTAccountID); err != nil {
				return nil, fmt.Errorf("external auth refresh was rejected: %w", err)
			}
		}
	}
	snapshot := auth.FromChatGPTAuthTokens(response.AccessToken, response.ChatGPTAccountID, response.ChatGPTPlanType)
	if codexHome := r.codexHomeForRollout(); codexHome != "" {
		if err := r.authStore(codexHome).Save(snapshot); err != nil {
			return nil, err
		}
	}
	r.requireAccount().ApplyAuthSnapshot(&snapshot)
	r.configureMCPFromConfig()
	r.maybeStartCuratedRepoSync(false)
	r.noteAuthChanged()
	r.notify(NotificationAccountUpdated, r.requireAccount().AccountUpdated())
	return &model.ExternalAuthRefreshResponse{
		AccessToken:      response.AccessToken,
		ChatGPTAccountID: response.ChatGPTAccountID,
		ChatGPTPlanType:  response.ChatGPTPlanType,
	}, nil
}

const currentTimeRequestTimeout = 10 * time.Second

func (r *RuntimeRouter) requestCurrentTime(ctx context.Context, threadID string) (time.Time, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return time.Time{}, fmt.Errorf("%w: threadId is required", ErrInvalidRequest)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	requestCtx, cancel := context.WithTimeout(ctx, currentTimeRequestTimeout)
	defer cancel()
	connectionIDs, err := r.waitForCurrentTimeSubscriber(requestCtx, threadID)
	if err != nil {
		return time.Time{}, err
	}
	connectionID, err := requireSingleCurrentTimeConnection(connectionIDs)
	if err != nil {
		return time.Time{}, err
	}
	var response CurrentTimeReadResponse
	if err := r.requireServerRequests().RequestToConnection(requestCtx, connectionID, ServerRequestCurrentTimeRead, &CurrentTimeReadParams{ThreadID: threadID}, &response); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return time.Time{}, errors.New("current-time request timed out after 10s")
		}
		return time.Time{}, err
	}
	current := time.Unix(response.CurrentTimeAt, 0).UTC()
	if current.IsZero() {
		return time.Time{}, fmt.Errorf("%w: current-time response is outside the supported range", ErrInvalidRequest)
	}
	return current, nil
}

func (r *RuntimeRouter) waitForCurrentTimeSubscriber(ctx context.Context, threadID string) ([]string, error) {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		connectionIDs := r.subscribedConnectionIDsForThread(threadID)
		if len(connectionIDs) > 0 {
			return connectionIDs, nil
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, errors.New("timed out waiting for a client to subscribe to the thread after 10s")
			}
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func requireSingleCurrentTimeConnection(connectionIDs []string) (string, error) {
	if len(connectionIDs) == 1 {
		return connectionIDs[0], nil
	}
	return "", fmt.Errorf("expected exactly one client subscribed to the thread, found %d", len(connectionIDs))
}

// approvalsReviewerForTurn resolves the turn's approvals reviewer, which routes
// approvals either to the Guardian review or to the user
// (Rust ApprovalsReviewer / routes_approval_policy_to_guardian).
func (r *RuntimeRouter) approvalsReviewerForTurn(threadID string, turnID string) state.Reviewer {
	if r == nil {
		return state.ReviewerUser
	}
	active := r.activeRuntimeTurnStateSnapshot(strings.TrimSpace(threadID), strings.TrimSpace(turnID))
	if active == nil {
		return state.ReviewerUser
	}
	cfg, err := r.effectiveConfigForTurn(active.Params)
	if err != nil {
		return state.ReviewerUser
	}
	return state.ReviewerFromString(turnApprovalsReviewerForTurn(cfg, active.Params))
}

// guardianApprovalOutcome is the Guardian review's verdict for one approval
// (Rust request_reviewer_approval's ReviewDecision handling).
type guardianApprovalOutcome struct {
	Approved   bool
	DenyReason string
	Abort      bool
}

// reviewApprovalWithGuardian runs the turn's automatic approval review for one
// approval action. An unavailable reviewer or a failed review denies the action
// with its reason; an aborted review (a cancelled turn) is reported so the
// caller can refuse the action without a user prompt.
func (r *RuntimeRouter) reviewApprovalWithGuardian(ctx context.Context, threadID string, turnID string, itemID string, action state.Action) guardianApprovalOutcome {
	if r == nil {
		return guardianApprovalOutcome{DenyReason: "Auto-approval review is unavailable; the request was denied."}
	}
	reviewer := r.services.GuardianReviewer
	if reviewer == nil && r.services.Agent != nil {
		reviewer = r.ensureGuardianReviewer(r.services.Agent)
	}
	if reviewer == nil {
		return guardianApprovalOutcome{DenyReason: "Auto-approval review is unavailable; the request was denied."}
	}
	decision, reason, err := reviewer.Review(ctx, strings.TrimSpace(threadID), strings.TrimSpace(turnID), strings.TrimSpace(itemID), action)
	reason = strings.TrimSpace(reason)
	if err != nil {
		if reason == "" {
			reason = strings.TrimSpace(err.Error())
		}
		if reason == "" {
			reason = "Auto-approval review failed; the request was denied."
		}
		return guardianApprovalOutcome{DenyReason: reason}
	}
	switch decision {
	case state.DecisionApproved:
		return guardianApprovalOutcome{Approved: true}
	case state.DecisionAborted:
		abortReason := reason
		if abortReason == "" {
			abortReason = "automatic approval review was cancelled"
		}
		return guardianApprovalOutcome{Abort: true, DenyReason: abortReason}
	default:
		if reason == "" {
			reason = "Auto-approval review denied the request."
		}
		return guardianApprovalOutcome{DenyReason: reason}
	}
}

// requestPermissionsHookToolInput mirrors Rust's RequestPermissions permission
// payload: the hook input carries the reason and the requested permissions.
func requestPermissionsHookToolInput(reason string, permissions map[string]any) map[string]any {
	input := map[string]any{}
	if trimmed := strings.TrimSpace(reason); trimmed != "" {
		input["reason"] = trimmed
	}
	if len(permissions) > 0 {
		input["permissions"] = permissions
	}
	if len(input) == 0 {
		return nil
	}
	return input
}

// commandApprovalAction mirrors Rust's GuardianApprovalRequest::ExecCommand for
// the command-review prompt.
func commandApprovalAction(request *tool.ShellRequest) state.Action {
	action := state.Action{Type: "command", Source: state.CommandSourceShell}
	if request != nil {
		action.Command = strings.TrimSpace(request.HookCommand)
		action.CommandArgv = append([]string(nil), request.Command...)
		action.CWD = strings.TrimSpace(request.CWD)
		// Rust's framing reason is the approval request's reason; the command's
		// own justification travels in the action JSON.
		action.Reason = strings.TrimSpace(request.ApprovalReason)
		action.Justification = strings.TrimSpace(request.Justification)
		action.SandboxPermissions = string(sandbox.SandboxPermissionsUseDefault)
		if permissions := strings.TrimSpace(string(request.SandboxPermissions)); permissions != "" {
			action.SandboxPermissions = permissions
		}
		action.AdditionalPermissions = additionalPermissionsJSON(request.AdditionalPermissions)
		tty := request.TTY
		action.TTY = &tty
		if strings.TrimSpace(action.Command) == "" && len(request.Command) > 0 {
			action.Command = strings.Join(request.Command, " ")
		}
	}
	if request != nil && (request.UnifiedExecEventSink != nil || strings.TrimSpace(request.UnifiedExecTurnID) != "") {
		// The unified-exec runtime raises the same approval with its event sink
		// attached, which is what the guardian metric tag maps to
		// state.CommandSourceUnifiedExec.
		action.Source = state.CommandSourceUnifiedExec
	}
	return action
}

// additionalPermissionsJSON mirrors Rust's AdditionalPermissionProfile
// serialization for the subset Go models: an optional network switch and the
// legacy read/write roots form the canonical entry list degrades to.
func additionalPermissionsJSON(profile *sandbox.AdditionalPermissionProfile) map[string]any {
	if profile == nil {
		return nil
	}
	value := map[string]any{}
	if profile.Network != nil {
		value["network"] = map[string]any{"enabled": *profile.Network}
	}
	if len(profile.FileSystem) > 0 {
		value["file_system"] = map[string]any{"write": append([]string(nil), profile.FileSystem...)}
	}
	if len(value) == 0 {
		return nil
	}
	return value
}

// applyPatchApprovalAction mirrors Rust's GuardianApprovalRequest::ApplyPatch.
func applyPatchApprovalAction(request *tool.ApplyPatchApprovalRequest) state.Action {
	action := state.Action{Type: "apply_patch"}
	if request == nil {
		return action
	}
	action.CWD = strings.TrimSpace(request.CWD)
	action.Patch = request.Patch
	files := make([]string, 0, len(request.Changes))
	for _, change := range request.Changes {
		if path, ok := change["path"].(string); ok && strings.TrimSpace(path) != "" {
			files = append(files, strings.TrimSpace(path))
			continue
		}
		if path, ok := change["Path"].(string); ok && strings.TrimSpace(path) != "" {
			files = append(files, strings.TrimSpace(path))
		}
	}
	action.Files = files
	return action
}

// permissionRequestHookVerdict runs the turn's PermissionRequest hooks for one
// approval action (Rust Session::request_approval -> run_permission_request_hooks).
// It reports ok=false when no hook runner, active turn, or adapter is available,
// so the caller falls through to its normal approval flow; a nil verdict means
// the matching hooks declined to decide.
func (r *RuntimeRouter) permissionRequestHookVerdict(ctx context.Context, threadID string, turnID string, runIDSuffix string, toolName string, matcherAliases []string, toolInput any) (*HookPermissionRequestDecision, bool) {
	if r == nil || !r.hookRunnerConfigured() {
		return nil, false
	}
	active := r.activeRuntimeTurnStateSnapshot(strings.TrimSpace(threadID), strings.TrimSpace(turnID))
	if active == nil || active.Params == nil {
		return nil, false
	}
	adapter, _ := r.turnHookAdapter(active.Params, strings.TrimSpace(turnID)).(*ToolHookAdapter)
	if adapter == nil {
		return nil, false
	}
	decision, err := adapter.RunPermissionRequest(ctx, runIDSuffix, toolName, matcherAliases, toolInput)
	if err != nil {
		return nil, false
	}
	return decision, true
}

// applyPatchPermissionRequestToolInput mirrors Rust's ApplyPatch payload: the
// hook input carries the patch body under `command`.
func applyPatchPermissionRequestToolInput(patch string) map[string]any {
	if strings.TrimSpace(patch) == "" {
		return nil
	}
	return map[string]any{"command": patch}
}

// shellPermissionRequestToolInput mirrors Rust's PermissionRequestPayload::bash:
// the hook input carries the command and, when the caller supplied one, the
// justification as `description`.
func shellPermissionRequestToolInput(request *tool.ShellRequest) map[string]any {
	if request == nil {
		return nil
	}
	input := map[string]any{}
	if command := strings.TrimSpace(request.HookCommand); command != "" {
		input["command"] = command
	}
	if description := strings.TrimSpace(request.Justification); description != "" {
		input["description"] = description
	}
	if len(input) == 0 {
		return nil
	}
	return input
}

func (r *RuntimeRouter) turnHookAdapter(params *turn.TurnStartParams, turnID string) tool.HookRunner {
	if r == nil || params == nil || !r.hookRunnerConfigured() {
		return nil
	}
	hooks := r.hooksForCWD(params.CWD, params.ThreadID)
	adapter := NewToolHookAdapter(r.requireHookRunner(), hooks, params.ThreadID, turnID, params.CWD)
	// Rust reports the issuing step's model and permission mode to the pre/post
	// tool-use hooks (hook_runtime::run_pre_tool_use_hooks). Go captures the
	// turn's effective settings until the step-settings lane exists.
	adapter.Model, adapter.PermissionMode = r.hookTurnAttribution(params)
	return adapter
}

func (r *RuntimeRouter) startTurnRuntimeAsync(params *turn.TurnStartParams, response *turn.TurnStartResponse, connectionID string, trace *protocol.W3CTraceContext) {
	if r == nil || params == nil || response == nil {
		return
	}
	if r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil {
		return
	}
	paramsCopy := cloneTurnStartParams(params)
	if trace != nil {
		paramsCopy.Trace = trace
	}
	turnCopy := response.Turn
	ctx, cancel := context.WithCancel(context.Background())
	// The turn runs inside its thread's session loop span, so the step's sampling
	// request nests under it (Rust runs run_turn inside the session_loop span).
	if span := r.threadSessionSpan(params.ThreadID); span != nil {
		ctx = telemetry.WithSpan(ctx, span)
	}
	if err := r.registerTrackedActiveRuntimeTurn(params.ThreadID, response.Turn.ID, cancel, time.Now().UTC().UnixMilli(), paramsCopy); err != nil {
		cancel()
		r.emitTurnRuntimeError(params.ThreadID, response.Turn.ID, err)
		return
	}
	r.updateActiveRuntimeTurnAnalytics(params.ThreadID, response.Turn.ID, connectionID, nil)
	go func() {
		defer r.threads.TurnWorkerDone()
		runtime, err := r.buildTurnRuntimeContext(ctx, paramsCopy, response.Turn.ID)
		if err != nil {
			if ctx.Err() == nil {
				r.clearActiveRuntimeTurn(paramsCopy.ThreadID, response.Turn.ID)
				r.emitTurnRuntimeError(paramsCopy.ThreadID, response.Turn.ID, err)
			}
			return
		}
		if runtime == nil {
			r.clearActiveRuntimeTurn(paramsCopy.ThreadID, response.Turn.ID)
			return
		}
		r.runTurnRuntime(ctx, paramsCopy, &turnCopy, runtime, connectionID)
	}()
}

func (r *RuntimeRouter) requireThreadStatus() *ThreadStatusManager {
	if r == nil || r.threads == nil {
		return NewThreadStatusManager()
	}
	return r.threads.StatusManager()
}

func (r *RuntimeRouter) requireReviews() *review.Service {
	if r.services.Reviews == nil {
		r.services.Reviews = review.NewService()
	}
	return r.services.Reviews
}

func (r *RuntimeRouter) requireMisc() *MiscService {
	if r.services.Misc == nil {
		r.services.Misc = NewMiscService()
	}
	return r.services.Misc
}

func (r *RuntimeRouter) requireCommandExec() *CommandExecService {
	if r.services.CommandExec == nil {
		r.services.CommandExec = NewCommandExecService()
	}
	return r.services.CommandExec
}

func (r *RuntimeRouter) requireProcesses() *ProcessService {
	if r.services.Processes == nil {
		r.services.Processes = NewProcessService()
	}
	return r.services.Processes
}

func (r *RuntimeRouter) requireServerRequests() *ServerRequestBroker {
	if r.services.ServerRequests == nil {
		r.services.ServerRequests = NewServerRequestBroker()
		r.services.ServerRequests.SetSink(r.requests)
		r.services.ServerRequests.SetResolvedCallback(r.notifyServerRequestResolved)
		r.services.ServerRequests.SetResolvedResponseCallback(r.handleServerRequestResolvedResponse)
	}
	return r.services.ServerRequests
}

func (r *RuntimeRouter) requireFeedback() *FeedbackSnapshot {
	if r.services.Feedback == nil {
		r.services.Feedback = &FeedbackSnapshot{Diagnostics: NewFeedbackDiagnostics(nil)}
	}
	return r.services.Feedback
}

func isThreadMethod(method Method) bool {
	switch method {
	case MethodThreadStart, MethodThreadResume, MethodThreadFork, MethodThreadArchive,
		MethodThreadUnarchive, MethodThreadDelete, MethodThreadSetName, MethodThreadNameSet,
		MethodThreadIncrementElicitation, MethodThreadDecrementElicitation,
		MethodThreadIncrementElicitationLegacy, MethodThreadDecrementElicitationLegacy,
		MethodThreadUnsubscribe, MethodThreadMemoryModeSet, MethodMemoryReset,
		MethodThreadCompactStart, MethodThreadApproveGuardianDeniedAction,
		MethodThreadMetadataUpdate, MethodThreadSectionMove, MethodThreadList, MethodThreadRead,
		MethodThreadAttachmentAdd, MethodThreadAttachmentList, MethodThreadAttachmentRemove,
		MethodThreadSearch, MethodThreadLoadedList, MethodThreadItemsList,
		MethodThreadTurnsList, MethodThreadRevert,
		MethodThreadQueueAdd, MethodThreadQueueList, MethodThreadQueueUpdate,
		MethodThreadQueueDelete, MethodThreadQueueReorder, MethodThreadQueueStart,
		MethodThreadInjectItems:
		return true
	default:
		return false
	}
}

func isThreadExtraMethod(method Method) bool {
	switch method {
	case MethodThreadGoalSet, MethodThreadGoalGet, MethodThreadGoalClear,
		MethodThreadSettingsUpdate, MethodThreadShellCommand,
		MethodThreadBackgroundTerminalsClean, MethodThreadBackgroundTerminalsList,
		MethodThreadBackgroundTerminalsTerminate:
		return true
	default:
		return false
	}
}

func isRealtimeMethod(method Method) bool {
	switch method {
	case MethodThreadRealtimeStart, MethodThreadRealtimeAppendAudio,
		MethodThreadRealtimeAppendText, MethodThreadRealtimeAppendSpeech,
		MethodThreadRealtimeStop, MethodThreadRealtimeListVoices:
		return true
	default:
		return false
	}
}

func runtimeErrorCode(err error) int {
	var mcpRemoteErr *mcp.MCPRemoteError
	switch {
	case errors.Is(err, ErrUnknownMethod):
		return JSONRPCMethodNotFoundErrorCode
	case errors.As(err, &mcpRemoteErr) && mcpRemoteErr != nil && mcpRemoteErr.Code != 0:
		return int(mcpRemoteErr.Code)
	case errors.Is(err, model.ErrInvalidModelRequest),
		errors.Is(err, mcp.ErrInvalidMCPRequest),
		errors.Is(err, apps.ErrInvalidAppRequest),
		errors.Is(err, features.ErrInvalidFeatureRequest),
		errors.Is(err, plugin.ErrInvalidPluginRequest),
		errors.Is(err, sandbox.ErrInvalidPermissionProfileRequest),
		errors.Is(err, ErrInvalidEnvironmentRequest),
		errors.Is(err, ErrInvalidFeedbackRequest),
		errors.Is(err, ErrJSONRPCInvalidRequest):
		return JSONRPCInvalidRequestErrorCode
	case errors.Is(err, ErrInvalidRequest),
		errors.Is(err, ErrInvalidParams),
		errors.Is(err, ErrInvalidFSRequest),
		errors.Is(err, remotecontrol.ErrInvalidRequest),
		errors.Is(err, sandbox.ErrInvalidWindowsSandboxRequest),
		errors.Is(err, config.ErrInvalidConfigRequest),
		errors.Is(err, auth.ErrInvalidAccountRequest),
		errors.Is(err, turn.ErrInvalidTurnRequest),
		errors.Is(err, review.ErrInvalidRequest),
		errors.Is(err, ErrInvalidMiscRequest),
		errors.Is(err, ErrInvalidThreadExtraRequest),
		errors.Is(err, realtime.ErrInvalidRealtimeRequest),
		errors.Is(err, ErrInvalidHook),
		errors.Is(err, ErrInvalidSkillsRequest),
		errors.Is(err, session.ErrInvalidThreadID):
		return JSONRPCInvalidParamsErrorCode
	case errors.Is(err, session.ErrThreadNotFound):
		return -32004
	case errors.Is(err, session.ErrConflict):
		return -32009
	default:
		return JSONRPCInternalErrorCode
	}
}

// accountBoundRateLimitExtras ports the account-readiness check Rust applies in
// account_processor.rs before exposing account-bound CTA content: the ordinary
// usage decision and the backend banner are only reported when the usage read
// belongs to the active non-FedRAMP account (the account id is reported
// regardless).
func accountBoundRateLimitExtras(response *chatgptapi.RateLimitsWithResetCredits, snapshot *auth.AuthDotJSON) (*bool, *string, json.RawMessage) {
	if response == nil {
		return nil, nil, nil
	}
	authAccountID := auth.AccountIDFromAuthForRestrictions(snapshot)
	authUserID := auth.ChatGPTUserIDFromAuth(snapshot)
	matchesActiveAccount := !auth.IsFedrampAccount(snapshot) &&
		authAccountID != "" && response.AccountID != nil && *response.AccountID == authAccountID &&
		authUserID != "" && response.UserID != nil && *response.UserID == authUserID
	ordinary := response.OrdinaryUsageAllowed
	upsell := response.RateLimitUpsell
	if !matchesActiveAccount {
		ordinary = nil
		upsell = nil
	}
	return ordinary, response.AccountID, upsell
}
