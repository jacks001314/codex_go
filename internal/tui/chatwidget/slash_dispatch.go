package chatwidget

import (
	"strings"

	codextui "codex_go/internal/tui"
	bottompane "codex_go/internal/tui/bottom_pane"
	"codex_go/internal/turn"
)

type SlashDispatchKind string

const (
	SlashDispatchLocal       SlashDispatchKind = "local"
	SlashDispatchSubmit      SlashDispatchKind = "submit"
	SlashDispatchUnsupported SlashDispatchKind = "unsupported"
)

type SlashDispatchDecision struct {
	Kind    SlashDispatchKind
	Command string
	Args    string
}

type SlashCommandDispatchSource string

const (
	SlashCommandDispatchLive   SlashCommandDispatchSource = "live"
	SlashCommandDispatchQueued SlashCommandDispatchSource = "queued"
)

type QueuedSlashDispatchKind string

const (
	QueuedSlashDispatchSubmit          QueuedSlashDispatchKind = "submit"
	QueuedSlashDispatchUnrecognized    QueuedSlashDispatchKind = "unrecognized"
	QueuedSlashDispatchCommand         QueuedSlashDispatchKind = "command"
	QueuedSlashDispatchCommandWithArgs QueuedSlashDispatchKind = "command_with_args"
	QueuedSlashDispatchServiceTier     QueuedSlashDispatchKind = "service_tier"
)

type QueuedSlashDispatchDecision struct {
	Kind         QueuedSlashDispatchKind
	Command      codextui.Command
	CommandName  string
	Args         string
	TextElements []turn.TextElement
	ServiceTier  *bottompane.ServiceTierCommand
	Message      UserMessage
	InfoMessage  string
	QueueDrain   QueueDrain
}

const (
	SideStartingContextLabel        = "Side starting..."
	SideSlashCommandUnavailableHint = "Press Ctrl+C to return to the main thread first."
	GoalUsageHint                   = "Example: /goal improve benchmark coverage"
	GoalUsageText                   = "Usage: /goal <objective|clear|edit|pause|resume>"
	RawUsageText                    = "Usage: /raw [on|off]"
	UsageChatGPTLoginRequired       = "Sign in with ChatGPT to use /usage."
)

type PreparedSlashArgsActionKind string

const (
	PreparedSlashArgsDispatchBare           PreparedSlashArgsActionKind = "dispatch_bare"
	PreparedSlashArgsShowTokenActivity      PreparedSlashArgsActionKind = "show_token_activity"
	PreparedSlashArgsShowMcpVerbose         PreparedSlashArgsActionKind = "show_mcp_verbose"
	PreparedSlashArgsOpenKeymapDebug        PreparedSlashArgsActionKind = "open_keymap_debug"
	PreparedSlashArgsSetRawOutput           PreparedSlashArgsActionKind = "set_raw_output"
	PreparedSlashArgsRenameThread           PreparedSlashArgsActionKind = "rename_thread"
	PreparedSlashArgsSubmitPlanMessage      PreparedSlashArgsActionKind = "submit_plan_message"
	PreparedSlashArgsQueuePlanMessage       PreparedSlashArgsActionKind = "queue_plan_message"
	PreparedSlashArgsGoalDisabled           PreparedSlashArgsActionKind = "goal_disabled"
	PreparedSlashArgsGoalClear              PreparedSlashArgsActionKind = "goal_clear"
	PreparedSlashArgsGoalEdit               PreparedSlashArgsActionKind = "goal_edit"
	PreparedSlashArgsGoalPause              PreparedSlashArgsActionKind = "goal_pause"
	PreparedSlashArgsGoalResume             PreparedSlashArgsActionKind = "goal_resume"
	PreparedSlashArgsGoalSetDraft           PreparedSlashArgsActionKind = "goal_set_draft"
	PreparedSlashArgsGoalQueueBeforeSession PreparedSlashArgsActionKind = "goal_queue_before_session"
	PreparedSlashArgsStartSideConversation  PreparedSlashArgsActionKind = "start_side_conversation"
	PreparedSlashArgsReviewCustom           PreparedSlashArgsActionKind = "review_custom"
	PreparedSlashArgsResumeSession          PreparedSlashArgsActionKind = "resume_session"
	PreparedSlashArgsSandboxReadRoot        PreparedSlashArgsActionKind = "sandbox_read_root"
	PreparedSlashArgsPetDisabled            PreparedSlashArgsActionKind = "pet_disabled"
	PreparedSlashArgsSelectPet              PreparedSlashArgsActionKind = "select_pet"
	PreparedSlashArgsError                  PreparedSlashArgsActionKind = "error"
	PreparedSlashArgsInfo                   PreparedSlashArgsActionKind = "info"
)

type PreparedSlashArgsContext struct {
	Command                   codextui.Command
	Args                      string
	TextElements              []turn.TextElement
	Source                    SlashCommandDispatchSource
	HasCodexBackendAuth       bool
	CollaborationModesEnabled bool
	PlanModeAvailable         bool
	SessionConfigured         bool
	ThreadID                  string
	GoalsEnabled              bool
	RenameAllowed             bool
}

type PreparedSlashArgsDecision struct {
	Action                  PreparedSlashArgsActionKind
	Command                 codextui.Command
	Args                    string
	Message                 UserMessage
	ErrorMessage            string
	InfoMessage             string
	Hint                    string
	TokenActivityView       TokenActivityView
	RawOutputEnabled        *bool
	GoalStatus              string
	ThreadID                string
	SideContextLabel        string
	DrainPendingSubmission  bool
	ClearLiveGoalSubmission bool
	QueueDrain              QueueDrain
}

type SlashCommandGuardContext struct {
	Command                codextui.Command
	CommandName            string
	ActiveSideConversation bool
	ReviewMode             bool
	TaskRunning            bool
	ResumePendingStart     bool
	AgentTurnRunning       bool
}

type SlashCommandGuardDecision struct {
	Allowed                bool
	ErrorMessage           string
	DrainPendingSubmission bool
	RequestRedraw          bool
}

func DecideSlashDispatch(input string, localCommands map[string]bool) SlashDispatchDecision {
	input = strings.TrimSpace(input)
	if !strings.HasPrefix(input, "/") {
		return SlashDispatchDecision{Kind: SlashDispatchSubmit, Args: input}
	}
	withoutSlash := strings.TrimPrefix(input, "/")
	name, args, _ := strings.Cut(withoutSlash, " ")
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return SlashDispatchDecision{Kind: SlashDispatchSubmit, Args: input}
	}
	if localCommands != nil && localCommands[name] {
		return SlashDispatchDecision{Kind: SlashDispatchLocal, Command: name, Args: strings.TrimSpace(args)}
	}
	return SlashDispatchDecision{Kind: SlashDispatchUnsupported, Command: name, Args: strings.TrimSpace(args)}
}

func GuardSlashCommandDispatch(context SlashCommandGuardContext) SlashCommandGuardDecision {
	commandName := strings.TrimSpace(context.CommandName)
	if commandName == "" {
		commandName = strings.TrimPrefix(strings.TrimSpace(string(context.Command)), "/")
	}
	if commandName == "" {
		commandName = "unknown"
	}
	if context.ActiveSideConversation && !CommandAvailableInSideConversation(context.Command) {
		return SlashCommandGuardDecision{
			ErrorMessage:           "'/" + commandName + "' is unavailable in side conversations. " + SideSlashCommandUnavailableHint,
			DrainPendingSubmission: true,
		}
	}
	if context.ReviewMode && isSideConversationCommandName(context.Command, commandName) {
		return SlashCommandGuardDecision{
			ErrorMessage:           "'/" + commandName + "' is unavailable while code review is running.",
			DrainPendingSubmission: true,
		}
	}
	if SlashCommandBlockedByActiveTask(context) {
		return SlashCommandGuardDecision{
			ErrorMessage:           "'/" + commandName + "' is disabled while a task is in progress.",
			DrainPendingSubmission: true,
			RequestRedraw:          true,
		}
	}
	return SlashCommandGuardDecision{Allowed: true}
}

func SlashCommandBlockedByActiveTask(context SlashCommandGuardContext) bool {
	return (!CommandAvailableDuringTask(context.Command) && context.TaskRunning) ||
		(context.Command == codextui.CommandResume && (context.ResumePendingStart || context.AgentTurnRunning))
}

func DispatchPreparedSlashArgs(context PreparedSlashArgsContext) PreparedSlashArgsDecision {
	args := context.Args
	trimmed := strings.TrimSpace(args)
	decision := PreparedSlashArgsDecision{
		Action:   PreparedSlashArgsDispatchBare,
		Command:  context.Command,
		Args:     args,
		ThreadID: strings.TrimSpace(context.ThreadID),
	}
	if context.Source == SlashCommandDispatchLive && context.Command != codextui.CommandGoal {
		decision.DrainPendingSubmission = true
	}
	if trimmed == "" || !CommandSupportsInlineArgs(context.Command) {
		return decision
	}

	switch context.Command {
	case codextui.CommandUsage:
		if !context.HasCodexBackendAuth {
			decision.Action = PreparedSlashArgsError
			decision.ErrorMessage = UsageChatGPTLoginRequired
			return decision
		}
		if view, ok := ParseTokenActivityView(trimmed); ok {
			decision.Action = PreparedSlashArgsShowTokenActivity
			decision.TokenActivityView = view
			return decision
		}
		decision.Action = PreparedSlashArgsError
		decision.ErrorMessage = "Usage: /usage [daily|weekly|cumulative]"
		return decision
	case codextui.CommandMcp:
		if strings.EqualFold(trimmed, "verbose") {
			decision.Action = PreparedSlashArgsShowMcpVerbose
			return decision
		}
		decision.Action = PreparedSlashArgsError
		decision.ErrorMessage = "Usage: /mcp [verbose]"
		return decision
	case codextui.CommandKeymap:
		if strings.EqualFold(trimmed, "debug") {
			decision.Action = PreparedSlashArgsOpenKeymapDebug
			return decision
		}
		decision.Action = PreparedSlashArgsError
		decision.ErrorMessage = "Usage: /keymap [debug]"
		return decision
	case codextui.CommandRaw:
		switch strings.ToLower(trimmed) {
		case "on":
			enabled := true
			decision.Action = PreparedSlashArgsSetRawOutput
			decision.RawOutputEnabled = &enabled
		case "off":
			enabled := false
			decision.Action = PreparedSlashArgsSetRawOutput
			decision.RawOutputEnabled = &enabled
		default:
			decision.Action = PreparedSlashArgsError
			decision.ErrorMessage = RawUsageText
		}
		return decision
	case codextui.CommandRename:
		if !context.RenameAllowed {
			decision.Action = PreparedSlashArgsError
			decision.ErrorMessage = "Thread rename is unavailable right now."
			return decision
		}
		name := normalizePreparedThreadName(args)
		if name == "" {
			decision.Action = PreparedSlashArgsError
			decision.ErrorMessage = "Thread name cannot be empty."
			return decision
		}
		decision.Action = PreparedSlashArgsRenameThread
		decision.Args = name
		return decision
	case codextui.CommandPlan:
		if !context.CollaborationModesEnabled {
			decision.Action = PreparedSlashArgsInfo
			decision.InfoMessage = "Collaboration modes are disabled."
			decision.Hint = "Enable collaboration modes to use /plan."
			return decision
		}
		if !context.PlanModeAvailable {
			decision.Action = PreparedSlashArgsInfo
			decision.InfoMessage = "Plan mode unavailable right now."
			return decision
		}
		decision.Message = UserMessage{
			Text:         args,
			TextElements: append([]turn.TextElement(nil), context.TextElements...),
		}
		if context.SessionConfigured {
			decision.Action = PreparedSlashArgsSubmitPlanMessage
		} else {
			decision.Action = PreparedSlashArgsQueuePlanMessage
		}
		return decision
	case codextui.CommandGoal:
		return dispatchPreparedGoalSlashArgs(context, decision, args, trimmed)
	case codextui.CommandSide:
		if decision.ThreadID == "" {
			decision.Action = PreparedSlashArgsError
			decision.ErrorMessage = "'/side' is unavailable before the session starts."
			return decision
		}
		decision.Action = PreparedSlashArgsStartSideConversation
		decision.SideContextLabel = SideStartingContextLabel
		decision.Message = UserMessage{
			Text:         args,
			TextElements: append([]turn.TextElement(nil), context.TextElements...),
		}
		return decision
	case codextui.CommandReview:
		decision.Action = PreparedSlashArgsReviewCustom
		decision.Args = args
		return decision
	case codextui.CommandResume:
		decision.Action = PreparedSlashArgsResumeSession
		decision.Args = args
		return decision
	case codextui.CommandSandboxReadRoot:
		decision.Action = PreparedSlashArgsSandboxReadRoot
		decision.Args = args
		return decision
	case codextui.CommandPets:
		if petDisableArg(trimmed) {
			decision.Action = PreparedSlashArgsPetDisabled
			return decision
		}
		decision.Action = PreparedSlashArgsSelectPet
		decision.Args = args
		return decision
	default:
		return decision
	}
}

func DecideQueuedSlashPrompt(queued QueuedUserMessage, flags bottompane.BuiltinCommandFlags, serviceTierCommands []bottompane.ServiceTierCommand, pendingOrRunning bool, modalActive bool) QueuedSlashDispatchDecision {
	message := queued.UserMessage
	if queued.Action != QueuedInputParseSlash {
		return QueuedSlashDispatchDecision{Kind: QueuedSlashDispatchSubmit, Message: message, QueueDrain: QueueDrainStop}
	}

	name, rest, restOffset, ok := parseQueuedSlashName(message.Text)
	if !ok || strings.Contains(name, "/") {
		return QueuedSlashDispatchDecision{Kind: QueuedSlashDispatchSubmit, Message: message, QueueDrain: QueueDrainStop}
	}

	command, ok := bottompane.FindSlashCommand(name, flags, serviceTierCommands)
	if !ok {
		return QueuedSlashDispatchDecision{
			Kind:        QueuedSlashDispatchUnrecognized,
			CommandName: name,
			InfoMessage: `Unrecognized command '/` + name + `'. Type "/" for a list of supported commands.`,
			QueueDrain:  QueueDrainContinue,
		}
	}

	if strings.TrimSpace(rest) == "" {
		if command.Kind == bottompane.SlashCommandItemServiceTier {
			return QueuedSlashDispatchDecision{
				Kind:        QueuedSlashDispatchServiceTier,
				CommandName: command.Name,
				ServiceTier: cloneServiceTierCommand(command.ServiceTier),
				QueueDrain:  QueueDrainContinue,
			}
		}
		return QueuedSlashDispatchDecision{
			Kind:        QueuedSlashDispatchCommand,
			Command:     command.Command,
			CommandName: command.Name,
			QueueDrain:  QueuedCommandDrainResult(command.Command, pendingOrRunning, modalActive),
		}
	}

	if command.Kind != bottompane.SlashCommandItemBuiltin || !CommandSupportsInlineArgs(command.Command) {
		return QueuedSlashDispatchDecision{Kind: QueuedSlashDispatchSubmit, Message: message, QueueDrain: QueueDrainStop}
	}

	trimmedStart := strings.TrimLeft(rest, " \t\r\n")
	leadingTrimmed := len(rest) - len(trimmedStart)
	trimmedRest := strings.TrimRight(trimmedStart, " \t\r\n")
	return QueuedSlashDispatchDecision{
		Kind:         QueuedSlashDispatchCommandWithArgs,
		Command:      command.Command,
		CommandName:  command.Name,
		Args:         trimmedRest,
		TextElements: SlashCommandArgsElements(trimmedRest, restOffset+leadingTrimmed, message.TextElements),
		Message: UserMessage{
			Text:            trimmedRest,
			LocalImages:     append([]string(nil), message.LocalImages...),
			RemoteImageURLs: append([]string(nil), message.RemoteImageURLs...),
			TextElements:    SlashCommandArgsElements(trimmedRest, restOffset+leadingTrimmed, message.TextElements),
			MentionBindings: append([]string(nil), message.MentionBindings...),
		},
		QueueDrain: QueuedCommandDrainResult(command.Command, pendingOrRunning, modalActive),
	}
}

func CommandSupportsInlineArgs(command codextui.Command) bool {
	switch command {
	case codextui.CommandReview,
		codextui.CommandRename,
		codextui.CommandPlan,
		codextui.CommandGoal,
		codextui.CommandIde,
		codextui.CommandKeymap,
		codextui.CommandMcp,
		codextui.CommandRaw,
		codextui.CommandUsage,
		codextui.CommandPets,
		codextui.CommandSide,
		codextui.CommandResume,
		codextui.CommandSandboxReadRoot:
		return true
	default:
		return false
	}
}

func CommandAvailableInSideConversation(command codextui.Command) bool {
	switch command {
	case codextui.CommandCopy,
		codextui.CommandRaw,
		codextui.CommandDiff,
		codextui.CommandMention,
		codextui.CommandStatus,
		codextui.CommandUsage,
		codextui.CommandIde:
		return true
	default:
		return false
	}
}

func CommandAvailableDuringTask(command codextui.Command) bool {
	switch command {
	case codextui.CommandNew,
		codextui.CommandArchive,
		codextui.CommandDelete,
		codextui.CommandFork,
		codextui.CommandInit,
		codextui.CommandCompact,
		codextui.CommandKeymap,
		codextui.CommandVim,
		codextui.CommandElevateSandbox,
		codextui.CommandSandboxReadRoot,
		codextui.CommandExperimental,
		codextui.CommandMemories,
		codextui.CommandImport,
		codextui.CommandReview,
		codextui.CommandPlan,
		codextui.CommandClear,
		codextui.CommandLogout,
		codextui.CommandMemoryDrop,
		codextui.CommandMemoryUpdate,
		codextui.CommandTheme,
		codextui.CommandPets:
		return false
	default:
		return true
	}
}

func QueuedCommandDrainResult(command codextui.Command, pendingOrRunning bool, modalActive bool) QueueDrain {
	if pendingOrRunning || modalActive {
		return QueueDrainStop
	}
	switch command {
	case codextui.CommandIde,
		codextui.CommandStatus,
		codextui.CommandUsage,
		codextui.CommandDebugConfig,
		codextui.CommandPs,
		codextui.CommandStop,
		codextui.CommandMemoryDrop,
		codextui.CommandMemoryUpdate,
		codextui.CommandMcp,
		codextui.CommandApps,
		codextui.CommandPlugins,
		codextui.CommandRollout,
		codextui.CommandCopy,
		codextui.CommandRaw,
		codextui.CommandVim,
		codextui.CommandDiff,
		codextui.CommandApp,
		codextui.CommandRename,
		codextui.CommandTestApproval:
		return QueueDrainContinue
	default:
		return QueueDrainStop
	}
}

func SlashCommandArgsElements(rest string, restOffset int, textElements []turn.TextElement) []turn.TextElement {
	if rest == "" || len(textElements) == 0 {
		return nil
	}
	out := make([]turn.TextElement, 0, len(textElements))
	restLen := len(rest)
	for _, elem := range textElements {
		elemStart := int(elem.ByteRange.Start)
		elemEnd := int(elem.ByteRange.End)
		if elemEnd <= restOffset {
			continue
		}
		start := elemStart - restOffset
		if start < 0 {
			start = 0
		}
		end := elemEnd - restOffset
		if start >= restLen {
			continue
		}
		if end > restLen {
			end = restLen
		}
		if start >= end {
			continue
		}
		elem.ByteRange = turn.ByteRange{Start: uint(start), End: uint(end)}
		out = append(out, elem)
	}
	return out
}

func parseQueuedSlashName(text string) (string, string, int, bool) {
	if !strings.HasPrefix(text, "/") {
		return "", "", 0, false
	}
	nameEnd := len(text)
	for index, r := range text {
		if index == 0 {
			continue
		}
		if r == ' ' || r == '\t' || r == '\r' || r == '\n' {
			nameEnd = index
			break
		}
	}
	name := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(text[:nameEnd], "/")))
	if name == "" {
		return "", "", 0, false
	}
	if nameEnd >= len(text) {
		return name, "", len(text), true
	}
	return name, text[nameEnd+1:], nameEnd + 1, true
}

func cloneServiceTierCommand(command *bottompane.ServiceTierCommand) *bottompane.ServiceTierCommand {
	if command == nil {
		return nil
	}
	clone := *command
	return &clone
}

func dispatchPreparedGoalSlashArgs(context PreparedSlashArgsContext, decision PreparedSlashArgsDecision, args string, trimmed string) PreparedSlashArgsDecision {
	decision.DrainPendingSubmission = false
	if !context.GoalsEnabled {
		decision.Action = PreparedSlashArgsGoalDisabled
		decision.ClearLiveGoalSubmission = context.Source == SlashCommandDispatchLive
		return decision
	}
	switch strings.ToLower(trimmed) {
	case "clear":
		return preparedGoalControlDecision(decision, context, PreparedSlashArgsGoalClear, "clear")
	case "edit":
		decision.Action = PreparedSlashArgsGoalEdit
		decision.ClearLiveGoalSubmission = context.Source == SlashCommandDispatchLive
		return decision
	case "pause":
		return preparedGoalControlDecision(decision, context, PreparedSlashArgsGoalPause, "paused")
	case "resume":
		return preparedGoalControlDecision(decision, context, PreparedSlashArgsGoalResume, "active")
	}
	decision.Message = UserMessage{
		Text:         args,
		TextElements: append([]turn.TextElement(nil), context.TextElements...),
	}
	if strings.TrimSpace(context.ThreadID) == "" {
		if context.Source == SlashCommandDispatchLive {
			decision.Action = PreparedSlashArgsGoalQueueBeforeSession
			decision.Message.Text = "/goal " + args
			decision.ClearLiveGoalSubmission = true
			return decision
		}
		decision.Action = PreparedSlashArgsInfo
		decision.InfoMessage = GoalUsageText
		decision.Hint = "The session must start before you can set a goal."
		return decision
	}
	decision.Action = PreparedSlashArgsGoalSetDraft
	decision.ThreadID = strings.TrimSpace(context.ThreadID)
	return decision
}

func preparedGoalControlDecision(decision PreparedSlashArgsDecision, context PreparedSlashArgsContext, action PreparedSlashArgsActionKind, status string) PreparedSlashArgsDecision {
	if strings.TrimSpace(context.ThreadID) == "" {
		decision.Action = PreparedSlashArgsInfo
		decision.InfoMessage = GoalUsageText
		decision.Hint = "The session must start before you can change a goal."
		decision.ClearLiveGoalSubmission = context.Source == SlashCommandDispatchLive
		return decision
	}
	decision.Action = action
	decision.GoalStatus = status
	decision.ThreadID = strings.TrimSpace(context.ThreadID)
	decision.ClearLiveGoalSubmission = context.Source == SlashCommandDispatchLive
	return decision
}

func normalizePreparedThreadName(value string) string {
	fields := strings.Fields(value)
	return strings.Join(fields, " ")
}

func petDisableArg(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "disable", "disabled", "hide", "hidden", "off", "none":
		return true
	default:
		return false
	}
}

func isSideConversationCommandName(command codextui.Command, commandName string) bool {
	if command != codextui.CommandSide {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(commandName)) {
	case "side", "btw":
		return true
	default:
		return false
	}
}
