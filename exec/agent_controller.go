package exec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"codex_go/agent"
	"codex_go/cli"
	"codex_go/config"
	"codex_go/features"
	"codex_go/model"
	"codex_go/session"
	"codex_go/tool"
	"codex_go/turn"
)

const execDefaultAgentWait = 30 * time.Second

type execSubagentContext struct {
	ThreadID       string
	SessionID      string
	ParentThreadID string
	Nickname       string
	Role           string
	AgentPath      string
	Depth          int
	Version        agent.MultiAgentVersion
	Controller     agent.ToolController
}

type execMultiAgentTools struct {
	controller                agent.ToolController
	exposure                  tool.Exposure
	version                   agent.MultiAgentVersion
	namespace                 string
	roles                     map[string]agent.RoleConfig
	defaults                  agent.SpawnDefaults
	disableWait               bool
	ownTree                   bool
	waitDefault               time.Duration
	waitMin                   time.Duration
	waitMax                   time.Duration
	hideSpawnMetadata         bool
	exposeSpawnModelOverrides bool
	maxConcurrency            int
}

func execAgentSteerMailboxFromTools(req *Request, options *execMultiAgentTools) *turn.SteerMailbox {
	if req == nil || options == nil {
		return nil
	}
	controller, ok := options.controller.(*execAgentController)
	if !ok || controller == nil {
		return nil
	}
	return controller.shared().steerMailbox
}

func execAgentControllerFromTools(options *execMultiAgentTools) agent.ToolController {
	if options == nil {
		return nil
	}
	return options.controller
}

func execAgentExposureFromTools(options *execMultiAgentTools) tool.Exposure {
	if options == nil {
		return ""
	}
	return options.exposure
}

func execAgentRolesFromTools(options *execMultiAgentTools) map[string]agent.RoleConfig {
	if options == nil {
		return nil
	}
	return options.roles
}

func execAgentDefaultsFromTools(options *execMultiAgentTools) agent.SpawnDefaults {
	if options == nil {
		return agent.SpawnDefaults{}
	}
	return options.defaults
}

func execAgentWaitDisabledFromTools(options *execMultiAgentTools) bool {
	return options != nil && options.disableWait
}

func execAgentVersionFromTools(options *execMultiAgentTools) agent.MultiAgentVersion {
	if options == nil {
		return ""
	}
	return options.version
}

func execAgentNamespaceFromTools(options *execMultiAgentTools) string {
	if options == nil {
		return ""
	}
	return options.namespace
}

func execAgentWaitConfigFromTools(options *execMultiAgentTools) (time.Duration, time.Duration, time.Duration, bool, bool) {
	if options == nil {
		return 0, 0, 0, false, false
	}
	return options.waitDefault, options.waitMin, options.waitMax, options.hideSpawnMetadata, options.exposeSpawnModelOverrides
}

func execSessionID(req *Request, fallback string) string {
	if req != nil && req.subagent != nil {
		if value := strings.TrimSpace(req.subagent.SessionID); value != "" {
			return value
		}
	}
	return strings.TrimSpace(fallback)
}

func execParentThreadID(req *Request) string {
	if req == nil || req.subagent == nil {
		return ""
	}
	return strings.TrimSpace(req.subagent.ParentThreadID)
}

func execThreadSource(req *Request) string {
	if req != nil && req.subagent != nil {
		return "subagent"
	}
	// Rust's exec crate starts every user-facing session with
	// ThreadSource::User regardless of the exec subcommand (including review).
	return "user"
}

func execSessionSource(req *Request) string {
	if req != nil && req.subagent != nil {
		return "subagent:thread_spawn"
	}
	// Rust's exec crate uses SessionSource::Exec for all codex exec sessions.
	return "exec"
}

func execAgentOriginator(req *Request) string {
	if req != nil && req.subagent != nil {
		return "subagent"
	}
	if originator := strings.TrimSpace(os.Getenv("CODEX_INTERNAL_ORIGINATOR_OVERRIDE")); originator != "" {
		return originator
	}
	return "codex_cli_rs"
}

func execAgentNicknameForRequest(req *Request) string {
	if req == nil || req.subagent == nil {
		return ""
	}
	return strings.TrimSpace(req.subagent.Nickname)
}

func execAgentRoleForRequest(req *Request) string {
	if req == nil || req.subagent == nil {
		return ""
	}
	return strings.TrimSpace(req.subagent.Role)
}

func execAgentPathForRequest(req *Request) string {
	if req == nil || req.subagent == nil {
		return ""
	}
	return strings.TrimSpace(req.subagent.AgentPath)
}

func execMultiAgentVersionForRequest(req *Request) string {
	if req == nil {
		return ""
	}
	if version := knownExecMultiAgentVersion(req.multiAgentVersion); version != "" {
		return string(version)
	}
	if req.subagent != nil {
		return string(knownExecMultiAgentVersion(req.subagent.Version))
	}
	return ""
}

func (r *Runner) multiAgentToolsForRun(ctx context.Context, req *Request, cfg *config.Config, threadID string, turnID string, agentRunner model.AgentRunner) (*execMultiAgentTools, error) {
	if r == nil || req == nil || cfg == nil {
		return nil, nil
	}
	agentsConfig, err := cfg.AgentsConfig(r.CodexHome)
	if err != nil {
		return nil, err
	}
	modelsManager := execModelsManagerForAgent(agentRunner)
	version := execMultiAgentVersionForRun(req, cfg, agentsConfig, modelsManager)
	if version == "" {
		return nil, nil
	}
	req.multiAgentVersion = version
	// Rust hides the V1 collab tool surface for a thread whose next spawn depth
	// would exceed max_depth; V2 ignores max_depth and relies on concurrency.
	if version == agent.VersionV1 && req != nil && req.subagent != nil {
		maxDepth := config.DefaultAgentMaxDepth
		if agentsConfig.MaxDepth != nil {
			maxDepth = *agentsConfig.MaxDepth
		}
		if agent.ExceedsThreadSpawnDepthLimit(req.subagent.Depth+1, maxDepth) {
			return nil, nil
		}
	}

	maxAgents := agentsConfig.MaxConcurrentThreadsPerSession
	options := &execMultiAgentTools{
		exposure:  tool.ExposureModelVisible,
		version:   version,
		namespace: agent.MultiAgentV1Namespace,
		roles:     agentsConfig.Roles,
		defaults: agent.SpawnDefaults{
			Model:           agentsConfig.DefaultSubagentModel,
			ReasoningEffort: agentsConfig.DefaultSubagentReasoningEffort,
		},
		ownTree:        req.subagent == nil,
		waitDefault:    execDefaultAgentWait,
		maxConcurrency: agentsConfig.MaxConcurrentThreadsPerSession + 1,
	}
	if version == agent.VersionV2 {
		v2Config, err := cfg.MultiAgentV2Config(agentsConfig.MaxConcurrentThreadsPerSession)
		if err != nil {
			return nil, err
		}
		maxAgents = v2Config.MaxConcurrentThreadsPerSession - 1
		options.namespace = v2Config.ToolNamespace
		options.disableWait = !v2Config.WaitAgentEnabled
		options.waitDefault = v2Config.DefaultWaitTimeout
		options.waitMin = v2Config.MinWaitTimeout
		options.waitMax = v2Config.MaxWaitTimeout
		options.hideSpawnMetadata = v2Config.HideSpawnAgentMetadata
		options.exposeSpawnModelOverrides = v2Config.ExposeSpawnAgentModelOverrides
		options.maxConcurrency = v2Config.MaxConcurrentThreadsPerSession
		if v2Config.NonCodeModeOnly {
			options.exposure = tool.ExposureDirectModelOnly
		}
	}

	controller := agent.ToolController(nil)
	if req.subagent != nil {
		controller = req.subagent.Controller
		if scoped, ok := controller.(*execAgentController); ok && scoped != nil {
			scoped.setActiveTurn(turnID)
		}
	} else {
		rootController := newExecAgentController(r, ctx, req, threadID, maxAgents).(*execAgentController)
		rootController.setActiveTurn(turnID)
		rootController.multiAgentVersion = version
		rootController.modelsManager = modelsManager
		rootController.parentModel = execMultiAgentModelForRun(req, cfg, modelsManager)
		rootController.maxDepth = config.DefaultAgentMaxDepth
		if agentsConfig.MaxDepth != nil {
			rootController.maxDepth = *agentsConfig.MaxDepth
		}
		rootController.waitDefault = options.waitDefault
		rootController.waitMin = options.waitMin
		rootController.waitMax = options.waitMax
		controller = rootController
	}
	if controller == nil {
		return nil, nil
	}
	options.controller = controller
	return options, nil
}

func execMultiAgentVersionForRun(req *Request, cfg *config.Config, agentsConfig *config.AgentsConfig, modelsManager model.ModelsManager) agent.MultiAgentVersion {
	settings := map[string]bool{}
	if cfg != nil {
		settings = cfg.FeatureSettings()
	}
	if features.Enabled(settings, "multi_agent_v2") {
		return agent.VersionV2
	}
	if agentsConfig != nil && agentsConfig.Enabled != nil && !*agentsConfig.Enabled {
		return ""
	}
	if req != nil {
		if version := knownExecMultiAgentVersion(req.multiAgentVersion); version != "" {
			return version
		}
		if req.subagent != nil {
			if version := knownExecMultiAgentVersion(req.subagent.Version); version != "" {
				return version
			}
		}
	}
	if modelsManager == nil {
		modelsManager = model.NewStaticModelsManager(model.BundledModelsResponse())
	}
	modelID := execMultiAgentModelForRun(req, cfg, modelsManager)
	switch strings.ToLower(strings.TrimSpace(modelsManager.GetModelInfo(modelID, nil).MultiAgentVersion)) {
	case string(agent.VersionV2):
		return agent.VersionV2
	case string(agent.VersionV1):
		return agent.VersionV1
	case "disabled":
		return ""
	}
	if features.Enabled(settings, "multi_agent") {
		return agent.VersionV1
	}
	return ""
}

func execMultiAgentModelForRun(req *Request, cfg *config.Config, modelsManager model.ModelsManager) string {
	if req != nil && req.Exec.Subcommand == "review" {
		if value := stringConfigValue(cfg, "review_model"); value != "" {
			return value
		}
	}
	if req != nil {
		if value := firstNonEmpty(req.Exec.Shared.Model, req.Root.Shared.Model); value != "" {
			return value
		}
	}
	if value := stringConfigValue(cfg, "model"); value != "" {
		return value
	}
	if modelsManager == nil {
		modelsManager = model.NewStaticModelsManager(model.BundledModelsResponse())
	}
	return modelsManager.GetDefaultModel("", true, model.RefreshOffline)
}

func knownExecMultiAgentVersion(version agent.MultiAgentVersion) agent.MultiAgentVersion {
	switch version {
	case agent.VersionV1, agent.VersionV2:
		return version
	default:
		return ""
	}
}

func execModelsManagerForAgent(agentRunner model.AgentRunner) model.ModelsManager {
	responses, ok := agentRunner.(*model.ResponsesAgentRunner)
	if ok && responses != nil && responses.ModelsManager != nil {
		return responses.ModelsManager
	}
	return model.NewStaticModelsManager(model.BundledModelsResponse())
}

func execMultiAgentV2UsageHint(req *Request, options *execMultiAgentTools) string {
	if options == nil || options.version != agent.VersionV2 {
		return ""
	}
	identity := execMultiAgentV2RootUsageHint
	if req != nil && req.subagent != nil {
		identity = execMultiAgentV2SubagentUsageHint
	}
	parts := []string{
		identity,
		execMultiAgentV2SharedUsageHint,
	}
	// Rust 92b83e226d (#37189): present wait_agent polling guidance in the
	// developer instructions only when the tool is enabled.
	if !options.disableWait {
		parts = append(parts, execMultiAgentV2WaitAgentUsageHint)
	}
	parts = append(parts, fmt.Sprintf("There are %d available concurrency slots, meaning that up to %d agents can be active at once, including you.", options.maxConcurrency, options.maxConcurrency))
	if options.exposeSpawnModelOverrides {
		parts = append(parts, execMultiAgentV2ModelOverrideUsageHint)
	}
	return strings.Join(nonEmptyStrings(parts), "\n\n")
}

const execMultiAgentV2RootUsageHint = "You are `/root`, the primary agent in a team of agents collaborating to fulfill the user's goals.\n\n" +
	"At the start of your turn, you are the active agent.\n" +
	"You can spawn sub-agents to handle subtasks, and those sub-agents can spawn their own sub-agents.\n" +
	"All agents in the team, including the agents that you can assign tasks to, are equally intelligent and capable, and have access to the same set of tools.\n\n" +
	"You can use `spawn_agent` to create a new agent, `followup_task` to give an existing agent a new task and trigger a turn, and `send_message` to pass a message to a running agent without triggering a turn.\n" +
	"Child agents can also spawn their own sub-agents.\n" +
	"You can decide how much context you want to propagate to your sub-agents with the `fork_turns` parameter.\n\n" +
	"You will receive messages in the analysis channel in the form:\n" +
	"```\nMessage Type: MESSAGE | FINAL_ANSWER\nTask name: <recipient>\nSender: <author>\nPayload:\n<payload text>\n```\n" +
	"They may be addressed as to=/root"

const execMultiAgentV2SubagentUsageHint = "You are an agent in a team of agents collaborating to complete a task.\n\n" +
	"You can spawn sub-agents to handle subtasks, and those sub-agents can spawn their own sub-agents. All agents in the team, including the agents that you can assign tasks to, are equally intelligent and capable, and have access to the same set of tools.\n\n" +
	"You can use `spawn_agent` to create a new agent, `followup_task` to give an existing agent a new task and trigger a turn, and `send_message` to pass a message to a running agent.\n" +
	"Child agents can also spawn their own sub-agents.\n\n" +
	"When you provide a response in the final channel, that content is immediately delivered back to your parent agent.\n\n" +
	"You will receive messages in the analysis channel in the form:\n" +
	"```\nMessage Type: NEW_TASK | MESSAGE | FINAL_ANSWER\nTask name: <recipient>\nSender: <author>\nPayload:\n<payload text>\n```\n" +
	"You may also see them addressed as to=/root/..., which indicates your identity is /root/..."

const execMultiAgentV2SharedUsageHint = "Note that collaboration tools cannot be called from inside `functions.exec`. Call `spawn_agent`, `send_message`, `followup_task`, `wait_agent`, `interrupt_agent`, and `list_agents` only as direct tool calls using the recipient shown in their tool definitions, such as `to=functions.collaboration.spawn_agent`, since they are intentionally absent from the `functions.exec` `tools.*` namespace. Available tools in `functions.exec` are explicitly described with a `tools` namespace in the developer message.\n\n" +
	"All agents share the same directory. In detail:\n" +
	"- All agents have access to the same container and filesystem as you.\n" +
	"- All agents use the same current working directory.\n" +
	"- As a result, edits made by one agent are immediately visible to all other agents."

const execMultiAgentV2WaitAgentUsageHint = "When calling `wait_agent`, prefer longer waits (minutes) to avoid busy polling."

const execMultiAgentV2ModelOverrideUsageHint = "Full-history forks (`fork_turns` omitted or `\"all\"`) inherit the parent model and reasoning effort and do not accept overrides. Only set `model` or `reasoning_effort` when explicitly requested by the user, applicable `AGENTS.md` instructions, or skill instructions; when doing so, set `fork_turns` to `\"none\"` or a positive integer string."

func closeExecMultiAgentTools(options *execMultiAgentTools) {
	if options == nil || !options.ownTree {
		return
	}
	if controller, ok := options.controller.(*execAgentController); ok {
		controller.shutdown()
	}
}

type execAgentController struct {
	root              *execAgentController
	runner            *Runner
	ctx               context.Context
	base              Request
	parentID          string
	scopePath         string
	maxAgents         int
	depth             int
	maxDepth          int
	parentModel       string
	multiAgentVersion agent.MultiAgentVersion
	modelsManager     model.ModelsManager

	mu           sync.Mutex
	wg           sync.WaitGroup
	shuttingDown bool
	tasks        map[string]*execAgentTask
	nextName     int
	updates      chan struct{}
	mailboxes    map[string]chan string
	waitDefault  time.Duration
	waitMin      time.Duration
	waitMax      time.Duration
	activeTurnID string
	steerMailbox *turn.SteerMailbox
}

type execAgentTask struct {
	id              string
	taskName        string
	path            string
	nickname        string
	role            string
	depth           int
	status          agent.AgentMessageStatus
	cancel          context.CancelFunc
	generation      uint64
	activeTurnID    string
	pendingMessages []execAgentCommunication
	pendingFollowup []execAgentCommunication
}

type execAgentCommunication struct {
	author    string
	recipient string
	message   string
	trigger   bool
	plaintext bool
}

func newExecAgentController(runner *Runner, ctx context.Context, req *Request, parentID string, maxAgents int) agent.ToolController {
	if ctx == nil {
		ctx = context.Background()
	}
	base := Request{}
	depth := 0
	if req != nil {
		base = cloneExecAgentRequest(req)
		if req.subagent != nil {
			depth = req.subagent.Depth
		}
	}
	return &execAgentController{
		runner: runner, ctx: ctx, base: base, parentID: strings.TrimSpace(parentID),
		scopePath: "/root", maxAgents: maxAgents, depth: depth, maxDepth: -1,
		multiAgentVersion: agent.VersionV2, tasks: map[string]*execAgentTask{},
		updates: make(chan struct{}, 1), mailboxes: map[string]chan string{}, steerMailbox: turn.NewSteerMailbox(),
	}
}

func (c *execAgentController) shared() *execAgentController {
	if c != nil && c.root != nil {
		return c.root
	}
	return c
}

func (c *execAgentController) scoped(path, parentID string) *execAgentController {
	root := c.shared()
	return &execAgentController{root: root, parentID: strings.TrimSpace(parentID), scopePath: cleanExecAgentPath(path)}
}

func (c *execAgentController) setActiveTurn(turnID string) {
	s := c.shared()
	if s == nil {
		return
	}
	s.mu.Lock()
	c.activeTurnID = strings.TrimSpace(turnID)
	var task *execAgentTask
	pending := []execAgentCommunication{}
	if c != s {
		if task = s.taskLocked(c.parentID); task != nil {
			task.activeTurnID = strings.TrimSpace(turnID)
			pending = append(pending, task.pendingMessages...)
			pending = append(pending, task.pendingFollowup...)
			task.pendingMessages = nil
			task.pendingFollowup = nil
		}
	}
	if s.steerMailbox == nil {
		s.steerMailbox = turn.NewSteerMailbox()
	}
	mailbox := s.steerMailbox
	s.mu.Unlock()
	if task == nil || mailbox == nil || strings.TrimSpace(turnID) == "" || len(pending) == 0 {
		return
	}
	items := make([]any, 0, len(pending))
	for _, communication := range pending {
		if strings.TrimSpace(communication.message) != "" {
			items = append(items, execAgentCommunicationInputItem(communication))
		}
	}
	if len(items) == 0 {
		return
	}
	if mailbox.Enqueue(&turn.SteerEnqueueParams{ThreadID: task.id, TurnID: strings.TrimSpace(turnID), InputItems: items}) == nil {
		c.notifyMailboxFor(task.path, fmt.Sprintf("queued input delivered to %s", task.path))
	}
}

func (c *execAgentController) SpawnAgent(ctx context.Context, args *agent.SpawnAgentArgs) (*agent.SpawnAgentResult, error) {
	s := c.shared()
	if s == nil || s.runner == nil {
		return nil, fmt.Errorf("agent runtime is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if args == nil {
		args = &agent.SpawnAgentArgs{}
	}
	prompt := execAgentString(args.Message)
	if prompt == "" && len(args.Items) == 0 {
		return nil, fmt.Errorf("message or items is required")
	}
	if err := c.resolveSpawnModelOverrides(args); err != nil {
		return nil, err
	}

	s.mu.Lock()
	if s.shuttingDown {
		s.mu.Unlock()
		return nil, fmt.Errorf("agent runtime is shutting down")
	}
	// Rust enforces max_depth for V1 threads only; V2 relies on concurrency.
	if s.multiAgentVersion == agent.VersionV1 && s.maxDepth >= 0 && c.depth+1 > s.maxDepth {
		s.mu.Unlock()
		return nil, agent.ErrAgentDepthLimitReached
	}
	if s.maxAgents >= 0 && len(s.tasks) >= s.maxAgents {
		s.mu.Unlock()
		return nil, agent.ErrAgentLimitReached
	}
	id := newThreadID()
	s.nextName++
	nickname := execAgentNickname(args.NicknameCandidates, s.nextName)
	taskName := strings.TrimSpace(args.TaskName)
	if taskName == "" {
		taskName = execAgentPathSegment(nickname)
	}
	path := cleanExecAgentPath(strings.TrimSuffix(firstNonEmpty(c.scopePath, "/root"), "/") + "/" + taskName)
	for _, existing := range s.tasks {
		if existing.path == path {
			s.mu.Unlock()
			return nil, fmt.Errorf("agent task %s already exists", path)
		}
	}
	task := &execAgentTask{
		id: id, taskName: taskName, path: path, nickname: nickname, role: strings.TrimSpace(args.ResolvedRole),
		depth: c.depth + 1, status: agent.AgentMessageStatus{Kind: agent.AgentMessageStatusPendingInit},
	}
	s.tasks[id] = task
	s.mu.Unlock()

	c.startTask(task, args, []execAgentCommunication{c.communication(task.path, prompt, true, args.Plaintext)}, false)
	return &agent.SpawnAgentResult{AgentID: id, TaskName: path, Nickname: &nickname}, nil
}

func (c *execAgentController) resolveSpawnModelOverrides(args *agent.SpawnAgentArgs) error {
	s := c.shared()
	if s == nil || args == nil || s.modelsManager == nil {
		return nil
	}
	requestedModel := ""
	if args.Model != nil {
		requestedModel = strings.TrimSpace(*args.Model)
	}
	selectedModel := requestedModel
	var selectedPreset *model.ModelPreset
	if requestedModel != "" {
		targetVersion := knownExecMultiAgentVersion(s.multiAgentVersion)
		if targetVersion == "" {
			targetVersion = agent.VersionV2
		}
		availableModels := s.modelsManager.ListModels(model.RefreshOffline)
		for i := range availableModels {
			candidate := &availableModels[i]
			if candidate.Model == requestedModel && candidate.MultiAgentVersion == string(targetVersion) {
				selectedPreset = candidate
				break
			}
		}
		if selectedPreset == nil {
			available := make([]string, 0, 5)
			for _, candidate := range availableModels {
				if candidate.MultiAgentVersion == string(targetVersion) {
					available = append(available, candidate.Model)
					if len(available) == 5 {
						break
					}
				}
			}
			return fmt.Errorf("Unknown model `%s` for spawn_agent. Available models: %s", requestedModel, strings.Join(available, ", "))
		}
	} else {
		selectedModel = strings.TrimSpace(s.parentModel)
	}
	if args.ReasoningEffort != nil && strings.TrimSpace(*args.ReasoningEffort) != "" {
		requestedEffort := strings.TrimSpace(*args.ReasoningEffort)
		info := s.modelsManager.GetModelInfo(selectedModel, nil)
		if !execContainsString(info.SupportedReasoningLevels, requestedEffort) {
			return fmt.Errorf(
				"Reasoning effort `%s` is not supported for model `%s`. Supported reasoning efforts: %s",
				requestedEffort,
				selectedModel,
				strings.Join(info.SupportedReasoningLevels, ", "),
			)
		}
	} else if selectedPreset != nil && strings.TrimSpace(selectedPreset.DefaultReasoningLevel) != "" {
		value := strings.TrimSpace(selectedPreset.DefaultReasoningLevel)
		args.ReasoningEffort = &value
	}
	if args.ServiceTier != nil && strings.TrimSpace(*args.ServiceTier) != "" {
		requestedTier := strings.TrimSpace(*args.ServiceTier)
		info := s.modelsManager.GetModelInfo(selectedModel, nil)
		resolvedTier := model.ServiceTierForRequest(&info, requestedTier)
		if resolvedTier == "" && requestedTier != model.ServiceTierDefaultRequestValue {
			supported := "none"
			if len(info.ServiceTiers) > 0 {
				supported = strings.Join(info.ServiceTiers, ", ")
			}
			return fmt.Errorf(
				"Service tier `%s` is not supported for model `%s`. Supported service tiers: %s",
				requestedTier,
				selectedModel,
				supported,
			)
		}
		if resolvedTier != "" {
			args.ServiceTier = &resolvedTier
		}
	}
	return nil
}

func execContainsString(values []string, target string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == strings.TrimSpace(target) {
			return true
		}
	}
	return false
}

func (c *execAgentController) SendInput(ctx context.Context, args *agent.SendInputArgs) (*agent.SendInputResult, error) {
	if args == nil || strings.TrimSpace(args.Target) == "" {
		return nil, fmt.Errorf("target is required")
	}
	message := execAgentString(args.Message)
	if message == "" && len(args.Items) == 0 {
		return nil, fmt.Errorf("message or items is required")
	}
	task := c.task(args.Target)
	if task == nil {
		return &agent.SendInputResult{}, nil
	}
	s := c.shared()
	s.mu.Lock()
	if task.status.Kind == agent.AgentMessageStatusRunning || task.status.Kind == agent.AgentMessageStatusPendingInit {
		if !args.Interrupt {
			s.mu.Unlock()
			return nil, fmt.Errorf("agent %s already has an active turn", task.id)
		}
		if task.cancel != nil {
			task.generation++
			task.cancel()
		}
	}
	s.mu.Unlock()
	spawnArgs := &agent.SpawnAgentArgs{Items: append([]any(nil), args.Items...), ForkTurns: execStringPointer("none")}
	c.startTask(task, spawnArgs, []execAgentCommunication{c.communication(task.path, message, true, false)}, true)
	return &agent.SendInputResult{SubmissionID: deterministicTurnID(firstNonEmpty(message, task.id))}, nil
}

func (c *execAgentController) WaitAgent(ctx context.Context, args *agent.WaitAgentArgs) (*agent.WaitAgentResult, error) {
	if args == nil {
		args = &agent.WaitAgentArgs{}
	}
	timeout := execDefaultAgentWait
	if args.TimeoutMS != nil {
		timeout = time.Duration(*args.TimeoutMS) * time.Millisecond
	}
	if timeout < 0 {
		timeout = 0
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		statuses, hasFinal := c.statuses(args.Targets)
		if hasFinal {
			return &agent.WaitAgentResult{Status: statuses}, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			statuses, _ = c.statuses(args.Targets)
			return &agent.WaitAgentResult{Status: statuses, TimedOut: true}, nil
		case <-c.shared().updates:
		}
	}
}

func (c *execAgentController) ResumeAgent(ctx context.Context, args *agent.ResumeAgentArgs) (*agent.ResumeAgentResult, error) {
	if args == nil || strings.TrimSpace(args.ID) == "" {
		return nil, fmt.Errorf("id is required")
	}
	s := c.shared()
	if s != nil && s.multiAgentVersion == agent.VersionV1 && s.maxDepth >= 0 && c.depth+1 > s.maxDepth {
		return nil, agent.ErrAgentDepthLimitReached
	}
	task := c.task(args.ID)
	if task == nil {
		return &agent.ResumeAgentResult{Status: agent.AgentMessageStatus{Kind: agent.AgentMessageStatusNotFound}}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	status := task.status
	s.mu.Unlock()
	return &agent.ResumeAgentResult{Status: status}, nil
}

func (c *execAgentController) CloseAgent(_ context.Context, args *agent.CloseAgentArgs) (*agent.CloseAgentResult, error) {
	if args == nil || strings.TrimSpace(args.Target) == "" {
		return nil, fmt.Errorf("target is required")
	}
	task := c.task(args.Target)
	if task == nil {
		return &agent.CloseAgentResult{PreviousStatus: agent.AgentMessageStatus{Kind: agent.AgentMessageStatusNotFound}}, nil
	}
	s := c.shared()
	s.mu.Lock()
	previous := task.status
	task.generation++
	if task.cancel != nil {
		task.cancel()
	}
	task.status = agent.AgentMessageStatus{Kind: agent.AgentMessageStatusShutdown}
	s.mu.Unlock()
	c.notify()
	return &agent.CloseAgentResult{PreviousStatus: previous}, nil
}

func (c *execAgentController) startTask(task *execAgentTask, args *agent.SpawnAgentArgs, communications []execAgentCommunication, resume bool) {
	s := c.shared()
	childCtx, cancel := context.WithCancel(s.ctx)
	s.mu.Lock()
	if s.shuttingDown {
		task.status = agent.AgentMessageStatus{Kind: agent.AgentMessageStatusShutdown}
		s.mu.Unlock()
		cancel()
		c.notify()
		return
	}
	task.generation++
	generation := task.generation
	task.cancel = cancel
	task.status = agent.AgentMessageStatus{Kind: agent.AgentMessageStatusRunning}
	pending := append([]execAgentCommunication(nil), task.pendingMessages...)
	task.pendingMessages = nil
	s.wg.Add(1)
	s.mu.Unlock()
	c.notify()

	go func() {
		defer s.wg.Done()
		childReq := cloneExecAgentRequest(&s.base)
		childReq.Input = nil
		childReq.AdditionalInputItems = append([]any(nil), args.Items...)
		for _, communication := range append(pending, communications...) {
			if strings.TrimSpace(communication.message) == "" {
				continue
			}
			childReq.AdditionalInputItems = append(childReq.AdditionalInputItems, execAgentCommunicationInputItem(communication))
		}
		childReq.Exec.Prompt = ""
		childReq.Exec.OutputSchema = ""
		childReq.Exec.LastMessageFile = ""
		childReq.Exec.JSON = false
		childReq.Exec.StreamAssistantDeltas = false
		childReq.Exec.Subcommand = ""
		childReq.Exec.Resume = cli.ExecResumeOptions{}
		// Child runs own their event stream. Reusing the root handler leaks child
		// function-call lifecycle into the parent SDK stream, unlike Rust which
		// exposes only the collaboration lifecycle on the parent thread.
		childReq.InternalEventHandler = nil
		if args.Model != nil && strings.TrimSpace(*args.Model) != "" {
			childReq.Exec.Shared.Model = strings.TrimSpace(*args.Model)
		}
		if args.ReasoningEffort != nil && strings.TrimSpace(*args.ReasoningEffort) != "" {
			childReq.Exec.Shared.ModelReasoningEffort = strings.TrimSpace(*args.ReasoningEffort)
		}
		if args.ServiceTier != nil && strings.TrimSpace(*args.ServiceTier) != "" {
			childReq.Exec.ConfigOverrides = append(childReq.Exec.ConfigOverrides, fmt.Sprintf("service_tier=%q", strings.TrimSpace(*args.ServiceTier)))
		}
		if args.ForkContext || execForkTurns(args.ForkTurns) != "none" {
			childReq.AdditionalInputItems = append(c.parentInputItems(args.ForkTurns), childReq.AdditionalInputItems...)
		}
		if resume {
			childReq.Exec.Subcommand = "resume"
			childReq.Exec.Resume = cli.ExecResumeOptions{SessionID: task.id}
		}
		childController := c.scoped(task.path, task.id)
		childController.depth = task.depth
		childReq.subagent = &execSubagentContext{
			ThreadID: task.id, SessionID: s.parentID, ParentThreadID: c.parentID,
			Nickname: task.nickname, Role: task.role, AgentPath: task.path,
			Depth: task.depth, Version: s.multiAgentVersion,
			Controller: childController,
		}
		childRunner := *s.runner
		childRunner.ToolRouter = nil
		childRunner.UnifiedExec = tool.NewUnifiedExecManager()
		result, err := childRunner.RunContext(childCtx, &childReq, strings.NewReader(""), io.Discard, io.Discard)
		status := agent.AgentMessageStatus{Kind: agent.AgentMessageStatusCompleted}
		if err != nil {
			status = agent.AgentMessageStatus{Kind: agent.AgentMessageStatusErrored, Message: err.Error()}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				status.Kind = agent.AgentMessageStatusInterrupted
			}
		} else if result != nil {
			status.Message = result.LastMessage
		}
		s.mu.Lock()
		if task.generation != generation {
			s.mu.Unlock()
			return
		}
		if task.status.Kind != agent.AgentMessageStatusShutdown {
			task.status = status
		}
		task.cancel = nil
		task.activeTurnID = ""
		followups := append([]execAgentCommunication(nil), task.pendingFollowup...)
		task.pendingFollowup = nil
		s.mu.Unlock()
		c.notify()
		c.deliverCompletion(task, status)
		if len(followups) > 0 && status.Kind != agent.AgentMessageStatusShutdown {
			c.startTask(task, &agent.SpawnAgentArgs{TaskName: task.taskName, ForkTurns: execStringPointer("none")}, followups, true)
		}
	}()
}

func (c *execAgentController) communication(recipient, message string, trigger bool, plaintext bool) execAgentCommunication {
	return execAgentCommunication{
		author:    cleanExecAgentPath(firstNonEmpty(c.scopePath, "/root")),
		recipient: cleanExecAgentPath(recipient),
		message:   strings.TrimSpace(message),
		trigger:   trigger,
		plaintext: plaintext,
	}
}

func execAgentCommunicationInputItem(communication execAgentCommunication) map[string]any {
	messageType := "MESSAGE"
	if communication.trigger {
		messageType = "NEW_TASK"
	}
	envelope := fmt.Sprintf(
		"Message Type: %s\nTask name: %s\nSender: %s\nPayload:\n",
		messageType,
		cleanExecAgentPath(communication.recipient),
		cleanExecAgentPath(communication.author),
	)
	if communication.plaintext {
		return map[string]any{
			"type":      "agent_message",
			"author":    cleanExecAgentPath(communication.author),
			"recipient": cleanExecAgentPath(communication.recipient),
			"content": []any{
				map[string]any{"type": "input_text", "text": envelope + communication.message},
			},
		}
	}
	return map[string]any{
		"type":      "agent_message",
		"author":    cleanExecAgentPath(communication.author),
		"recipient": cleanExecAgentPath(communication.recipient),
		"content": []any{
			map[string]any{"type": "input_text", "text": envelope},
			map[string]any{"type": "encrypted_content", "encrypted_content": communication.message},
		},
	}
}

func (c *execAgentController) parentInputItems(forkTurns *string) []any {
	s := c.shared()
	store := session.NewStore(filepath.Join(s.runner.CodexHome, "sessions"))
	record, err := store.Read(session.ThreadID(c.parentID), true, true)
	if err != nil || record == nil {
		return nil
	}
	mode := execForkTurns(forkTurns)
	if mode == "none" {
		return nil
	}
	if mode != "all" {
		count, parseErr := strconv.Atoi(mode)
		if parseErr != nil || count <= 0 {
			return nil
		}
		record = execLastTurnsRecord(record, count)
	}
	items := session.InputItemsFromRecord(record, &session.HistoryBuildOptions{IncludeToolOutputs: true, CWD: record.Metadata.CWD})
	return stripParentAgentMessages(items)
}

func stripParentAgentMessages(items []any) []any {
	filtered := make([]any, 0, len(items))
	for _, item := range items {
		if raw, ok := item.(map[string]any); ok && strings.TrimSpace(execStringFromAny(raw["type"])) == "agent_message" {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func (c *execAgentController) task(target string) *execAgentTask {
	s := c.shared()
	s.mu.Lock()
	defer s.mu.Unlock()
	return c.taskLocked(strings.TrimSpace(target))
}

func (c *execAgentController) taskLocked(target string) *execAgentTask {
	s := c.shared()
	if task := s.tasks[target]; task != nil {
		return task
	}
	canonical := cleanExecAgentPath(target)
	if !strings.HasPrefix(strings.TrimSpace(target), "/") {
		canonical = cleanExecAgentPath(strings.TrimSuffix(firstNonEmpty(c.scopePath, "/root"), "/") + "/" + target)
	}
	for _, task := range s.tasks {
		if task.path == canonical {
			return task
		}
	}
	return nil
}

func (c *execAgentController) statuses(targets []string) (map[string]agent.AgentMessageStatus, bool) {
	s := c.shared()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(targets) == 0 {
		targets = make([]string, 0, len(s.tasks))
		for id := range s.tasks {
			targets = append(targets, id)
		}
	}
	statuses := make(map[string]agent.AgentMessageStatus, len(targets))
	hasFinal := len(targets) == 0
	for _, raw := range targets {
		id := strings.TrimSpace(raw)
		task := c.taskLocked(id)
		status := agent.AgentMessageStatus{Kind: agent.AgentMessageStatusNotFound}
		if task != nil {
			status = task.status
		}
		statuses[id] = status
		if status.IsFinal() {
			hasFinal = true
		}
	}
	return statuses, hasFinal
}

func (c *execAgentController) notify() {
	s := c.shared()
	select {
	case s.updates <- struct{}{}:
	default:
	}
}

func (c *execAgentController) notifyMailbox(message string) {
	c.notifyMailboxFor(c.scopePath, message)
}

func (c *execAgentController) notifyMailboxFor(scopePath string, message string) {
	if strings.TrimSpace(message) == "" {
		return
	}
	s := c.shared()
	if s == nil {
		return
	}
	s.mu.Lock()
	mailbox := s.activityMailboxLocked(scopePath)
	s.mu.Unlock()
	select {
	case mailbox <- message:
	default:
	}
}

func (c *execAgentController) activityMailboxLocked(scopePath string) chan string {
	s := c.shared()
	key := cleanExecAgentPath(firstNonEmpty(scopePath, "/root"))
	if s.mailboxes == nil {
		s.mailboxes = map[string]chan string{}
	}
	mailbox := s.mailboxes[key]
	if mailbox == nil {
		mailbox = make(chan string, 128)
		s.mailboxes[key] = mailbox
	}
	return mailbox
}

func (c *execAgentController) deliverCompletion(task *execAgentTask, status agent.AgentMessageStatus) {
	if c == nil || task == nil {
		return
	}
	s := c.shared()
	if s == nil {
		return
	}
	if knownExecMultiAgentVersion(s.multiAgentVersion) != agent.VersionV2 {
		c.notifyMailbox(agent.FormatSubagentNotificationMessage(task.path, status))
		return
	}
	message, ok := agent.FormatInterAgentCompletionMessage(cleanExecAgentPath(c.scopePath), task.path, status)
	if !ok {
		c.notifyMailbox("Wait completed.")
		return
	}
	s.mu.Lock()
	threadID := strings.TrimSpace(c.parentID)
	turnID := strings.TrimSpace(c.activeTurnID)
	mailbox := s.steerMailbox
	s.mu.Unlock()
	if mailbox != nil && threadID != "" && turnID != "" {
		_ = mailbox.Enqueue(&turn.SteerEnqueueParams{
			ThreadID: threadID,
			TurnID:   turnID,
			InputItems: []any{execAgentCompletionInputItem(
				cleanExecAgentPath(c.scopePath),
				task.path,
				message,
			)},
		})
	}
	c.notifyMailbox("Wait completed.")
}

func execAgentCompletionInputItem(recipient string, author string, message string) map[string]any {
	return map[string]any{
		"type":      "agent_message",
		"author":    cleanExecAgentPath(author),
		"recipient": cleanExecAgentPath(recipient),
		"content": []any{
			map[string]any{"type": "input_text", "text": message},
		},
	}
}

func (c *execAgentController) SendMessage(ctx context.Context, args *agent.SendMessageArgs) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if args == nil || strings.TrimSpace(args.Target) == "" || strings.TrimSpace(args.Message) == "" {
		return fmt.Errorf("target and message are required")
	}
	if c.canonicalTarget(args.Target) == "/root" {
		return c.sendMessageToRoot(args.Message, args.Plaintext)
	}
	s := c.shared()
	s.mu.Lock()
	task := c.taskLocked(args.Target)
	if task == nil {
		s.mu.Unlock()
		return fmt.Errorf("agent %s not found", args.Target)
	}
	if task.status.Kind == agent.AgentMessageStatusShutdown {
		s.mu.Unlock()
		return fmt.Errorf("agent %s is shut down", args.Target)
	}
	communication := c.communication(task.path, args.Message, false, args.Plaintext)
	if (task.status.Kind == agent.AgentMessageStatusRunning || task.status.Kind == agent.AgentMessageStatusPendingInit) && strings.TrimSpace(task.activeTurnID) != "" {
		threadID, turnID, mailbox := task.id, task.activeTurnID, s.steerMailbox
		s.mu.Unlock()
		if mailbox == nil {
			return fmt.Errorf("agent %s is unavailable", args.Target)
		}
		if err := mailbox.Enqueue(&turn.SteerEnqueueParams{ThreadID: threadID, TurnID: turnID, InputItems: []any{execAgentCommunicationInputItem(communication)}}); err != nil {
			return err
		}
		c.notifyMailboxFor(task.path, fmt.Sprintf("message from %s queued for %s", communication.author, task.path))
		return nil
	}
	task.pendingMessages = append(task.pendingMessages, communication)
	s.mu.Unlock()
	return nil
}

func (c *execAgentController) canonicalTarget(target string) string {
	target = strings.TrimSpace(target)
	if strings.HasPrefix(target, "/") {
		return cleanExecAgentPath(target)
	}
	return cleanExecAgentPath(strings.TrimSuffix(firstNonEmpty(c.scopePath, "/root"), "/") + "/" + target)
}

func (c *execAgentController) sendMessageToRoot(message string, plaintext bool) error {
	s := c.shared()
	if s == nil {
		return fmt.Errorf("agent runtime is unavailable")
	}
	s.mu.Lock()
	turnID := s.activeTurnID
	mailbox := s.steerMailbox
	threadID := s.parentID
	s.mu.Unlock()
	if mailbox == nil || strings.TrimSpace(threadID) == "" || strings.TrimSpace(turnID) == "" {
		return fmt.Errorf("root agent is unavailable")
	}
	communication := c.communication("/root", message, false, plaintext)
	if err := mailbox.Enqueue(&turn.SteerEnqueueParams{
		ThreadID:   threadID,
		TurnID:     turnID,
		InputItems: []any{execAgentCommunicationInputItem(communication)},
	}); err != nil {
		return err
	}
	c.notifyMailboxFor("/root", fmt.Sprintf("message from %s queued for /root", communication.author))
	return nil
}

func (c *execAgentController) FollowupTask(ctx context.Context, args *agent.FollowupTaskArgs) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if args == nil || strings.TrimSpace(args.Target) == "" || strings.TrimSpace(args.Message) == "" {
		return fmt.Errorf("target and message are required")
	}
	s := c.shared()
	s.mu.Lock()
	task := c.taskLocked(args.Target)
	if task == nil {
		s.mu.Unlock()
		return fmt.Errorf("agent %s not found", args.Target)
	}
	switch task.status.Kind {
	case agent.AgentMessageStatusRunning, agent.AgentMessageStatusPendingInit:
		communication := c.communication(task.path, args.Message, true, args.Plaintext)
		if strings.TrimSpace(task.activeTurnID) != "" {
			threadID, turnID, mailbox := task.id, task.activeTurnID, s.steerMailbox
			s.mu.Unlock()
			if mailbox == nil {
				return fmt.Errorf("agent %s is unavailable", args.Target)
			}
			if err := mailbox.Enqueue(&turn.SteerEnqueueParams{ThreadID: threadID, TurnID: turnID, InputItems: []any{execAgentCommunicationInputItem(communication)}}); err != nil {
				return err
			}
			c.notifyMailboxFor(task.path, fmt.Sprintf("follow-up from %s queued for %s", communication.author, task.path))
			return nil
		}
		task.pendingFollowup = append(task.pendingFollowup, communication)
		s.mu.Unlock()
		return nil
	case agent.AgentMessageStatusShutdown:
		s.mu.Unlock()
		return fmt.Errorf("agent %s is shut down", args.Target)
	}
	s.mu.Unlock()
	c.startTask(task, &agent.SpawnAgentArgs{TaskName: task.taskName, ForkTurns: execStringPointer("none")}, []execAgentCommunication{c.communication(task.path, args.Message, true, args.Plaintext)}, true)
	return nil
}

func (c *execAgentController) WaitForActivity(ctx context.Context, args *agent.WaitForActivityArgs) (*agent.WaitForActivityResult, error) {
	s := c.shared()
	timeout := s.waitDefault
	if timeout == 0 && s.waitMin == 0 && s.waitMax == 0 {
		timeout = agent.MultiAgentV2DefaultWait
	}
	if args != nil && args.TimeoutMS != nil {
		timeout = time.Duration(*args.TimeoutMS) * time.Millisecond
	}
	minWait, maxWait := s.waitMin, s.waitMax
	if minWait == 0 && maxWait == 0 && s.waitDefault == 0 {
		minWait, maxWait = agent.MultiAgentV2MinWait, agent.MultiAgentV2MaxWait
	}
	if timeout < minWait || timeout > maxWait {
		return nil, fmt.Errorf("timeout_ms must be between %d and %d", minWait.Milliseconds(), maxWait.Milliseconds())
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	s.mu.Lock()
	mailbox := s.activityMailboxLocked(c.scopePath)
	s.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case message := <-mailbox:
		return &agent.WaitForActivityResult{Message: message}, nil
	case <-timer.C:
		return &agent.WaitForActivityResult{TimedOut: true}, nil
	}
}

func (c *execAgentController) InterruptAgent(ctx context.Context, args *agent.InterruptAgentArgs) (*agent.InterruptAgentResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if args == nil || strings.TrimSpace(args.Target) == "" {
		return nil, fmt.Errorf("target is required")
	}
	s := c.shared()
	s.mu.Lock()
	task := c.taskLocked(args.Target)
	if task == nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("agent %s not found", strings.TrimSpace(args.Target))
	}
	previous := task.status
	task.generation++
	if task.cancel != nil {
		task.cancel()
	}
	task.cancel = nil
	task.activeTurnID = ""
	task.status = agent.AgentMessageStatus{Kind: agent.AgentMessageStatusInterrupted}
	s.mu.Unlock()
	c.notify()
	c.notifyMailbox("Wait completed.")
	return &agent.InterruptAgentResult{PreviousStatus: agent.V2AgentStatusValue(previous)}, nil
}

func (c *execAgentController) ListAgents(ctx context.Context, args *agent.ListAgentsArgs) (*agent.ListAgentsResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	prefix := ""
	if args != nil && args.PathPrefix != nil {
		prefix = cleanExecAgentPath(*args.PathPrefix)
	}
	s := c.shared()
	s.mu.Lock()
	result := &agent.ListAgentsResult{Agents: []agent.ListedAgent{}}
	if prefix == "" || strings.HasPrefix("/root", prefix) {
		result.Agents = append(result.Agents, agent.ListedAgent{AgentName: "/root", AgentStatus: "running"})
	}
	for _, task := range s.tasks {
		if prefix != "" && !strings.HasPrefix(task.path, prefix) {
			continue
		}
		result.Agents = append(result.Agents, agent.ListedAgent{AgentName: task.path, AgentStatus: agent.V2AgentStatusValue(task.status)})
	}
	s.mu.Unlock()
	sort.Slice(result.Agents, func(i, j int) bool { return result.Agents[i].AgentName < result.Agents[j].AgentName })
	return result, nil
}

func (c *execAgentController) shutdown() {
	s := c.shared()
	if s == nil {
		return
	}
	s.mu.Lock()
	s.shuttingDown = true
	for _, task := range s.tasks {
		task.generation++
		if task.cancel != nil {
			task.cancel()
		}
		task.cancel = nil
		if !task.status.IsFinal() {
			task.status = agent.AgentMessageStatus{Kind: agent.AgentMessageStatusShutdown}
		}
	}
	s.mu.Unlock()
	s.wg.Wait()
}

func execForkTurns(value *string) string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return "all"
	}
	return strings.ToLower(strings.TrimSpace(*value))
}

func execLastTurnsRecord(record *session.Record, count int) *session.Record {
	if record == nil || count <= 0 || len(record.Metadata.RolloutTurns) <= count {
		return record
	}
	turns := record.Metadata.RolloutTurns[len(record.Metadata.RolloutTurns)-count:]
	selected := make(map[string]bool, len(turns))
	for _, snapshot := range turns {
		selected[snapshot.ID] = true
	}
	cloned := *record
	cloned.Items = make([]session.Item, 0, len(record.Items))
	for _, item := range record.Items {
		turnID := ""
		if item.Metadata != nil {
			turnID, _ = item.Metadata["turnId"].(string)
			if turnID == "" {
				turnID, _ = item.Metadata["turn_id"].(string)
			}
		}
		if selected[turnID] {
			cloned.Items = append(cloned.Items, item)
		}
	}
	cloned.Metadata.RolloutTurns = append([]session.TurnSnapshot(nil), turns...)
	return &cloned
}

func execAgentPrompt(message string, queued []string) string {
	parts := make([]string, 0, 1+len(queued))
	if strings.TrimSpace(message) != "" {
		parts = append(parts, strings.TrimSpace(message))
	}
	for _, value := range queued {
		if strings.TrimSpace(value) != "" {
			parts = append(parts, "<inter_agent_message>\n"+strings.TrimSpace(value)+"\n</inter_agent_message>")
		}
	}
	return strings.Join(parts, "\n\n")
}

func cleanExecAgentPath(value string) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), "\\", "/")
	if value == "" {
		return ""
	}
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == '/' })
	return "/" + strings.Join(parts, "/")
}

func execStringPointer(value string) *string { return &value }

func cloneExecAgentRequest(req *Request) Request {
	if req == nil {
		return Request{}
	}
	cloned := *req
	cloned.Root.ConfigOverrides = append([]string(nil), req.Root.ConfigOverrides...)
	cloned.Root.EnableFeatures = append([]string(nil), req.Root.EnableFeatures...)
	cloned.Root.DisableFeatures = append([]string(nil), req.Root.DisableFeatures...)
	cloned.Root.Shared.Images = append([]string(nil), req.Root.Shared.Images...)
	cloned.Root.Shared.AddDirs = append([]string(nil), req.Root.Shared.AddDirs...)
	cloned.Exec.ConfigOverrides = append([]string(nil), req.Exec.ConfigOverrides...)
	cloned.Exec.SubArgs = append([]string(nil), req.Exec.SubArgs...)
	cloned.Exec.Shared.Images = append([]string(nil), req.Exec.Shared.Images...)
	cloned.Exec.Shared.AddDirs = append([]string(nil), req.Exec.Shared.AddDirs...)
	cloned.Input = append([]turn.TurnUserInput(nil), req.Input...)
	cloned.AdditionalInputItems = append([]any(nil), req.AdditionalInputItems...)
	return cloned
}

func execAgentNickname(candidates []string, ordinal int) string {
	for _, candidate := range candidates {
		if value := strings.TrimSpace(candidate); value != "" {
			return value
		}
	}
	return fmt.Sprintf("Agent %d", ordinal)
}

func execAgentPathSegment(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.NewReplacer(" ", "_", "/", "_", "\\", "_").Replace(value)
	if value == "" {
		return "agent"
	}
	return value
}

func execAgentString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

var _ agent.ToolController = (*execAgentController)(nil)
var _ agent.V2ToolController = (*execAgentController)(nil)
