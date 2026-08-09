package tui

import (
	"fmt"
	"strings"
	"time"

	"codex_go/eventmap"
)

type MessageRole string

const planModeDefaultReasoningEffort = "medium"

const (
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
	RoleSystem    MessageRole = "system"
	RoleHistory   MessageRole = "history"
)

type Message struct {
	Role    MessageRole
	Text    string
	RawText string
}

type Options struct {
	Model                   string
	ReasoningEffort         string
	PlanMode                bool
	PlanModeReasoningEffort string
	Personality             string
	Provider                string
	ApprovalPolicy          string
	Sandbox                 string
	ServiceTier             string
	CWD                     string
	Search                  bool
	NoAltScreen             bool
	CLIVersion              string
	AccountDisplay          string
	AgentsSummary           string
	HasChatGPTAccount       bool
}

type State struct {
	ThreadID                string
	ThreadName              string
	Model                   string
	ReasoningEffort         string
	PlanMode                bool
	PlanModeReasoningEffort string
	Personality             string
	Provider                string
	ApprovalPolicy          string
	Sandbox                 string
	ServiceTier             string
	CWD                     string
	Search                  bool
	NoAltScreen             bool
	Status                  string
	Messages                []Message
	TotalTokenUsage         TokenUsage
	LastTokenUsage          TokenUsage
	ModelContextWindow      *int64
	RateLimits              []RateLimitStatus
	RateLimitsLoaded        bool
	RateLimitsRefreshing    bool
	CLIVersion              string
	AccountDisplay          string
	AgentsSummary           string
	HasChatGPTAccount       bool
}

type RateLimitStatus struct {
	Label       string
	UsedPercent float64
	ResetsAt    *time.Time
	CapturedAt  time.Time
	Details     string
	Text        string
	IsText      bool
}

func NewState(options *Options) *State {
	state := &State{
		Status: "idle",
	}
	if options != nil {
		state.Model = strings.TrimSpace(options.Model)
		state.ReasoningEffort = strings.TrimSpace(options.ReasoningEffort)
		state.PlanMode = options.PlanMode
		state.PlanModeReasoningEffort = strings.TrimSpace(options.PlanModeReasoningEffort)
		state.Personality = strings.TrimSpace(options.Personality)
		state.Provider = strings.TrimSpace(options.Provider)
		state.ApprovalPolicy = strings.TrimSpace(options.ApprovalPolicy)
		state.Sandbox = strings.TrimSpace(options.Sandbox)
		state.ServiceTier = strings.TrimSpace(options.ServiceTier)
		state.CWD = strings.TrimSpace(options.CWD)
		state.Search = options.Search
		state.NoAltScreen = options.NoAltScreen
		state.CLIVersion = strings.TrimSpace(options.CLIVersion)
		state.AccountDisplay = strings.TrimSpace(options.AccountDisplay)
		state.AgentsSummary = strings.TrimSpace(options.AgentsSummary)
		state.HasChatGPTAccount = options.HasChatGPTAccount
	}
	return state
}

func (s *State) SetThreadID(threadID string) {
	if s != nil {
		s.ThreadID = strings.TrimSpace(threadID)
	}
}

func (s *State) SetThreadName(threadName string) {
	if s != nil {
		s.ThreadName = strings.TrimSpace(threadName)
	}
}

func (s *State) SetStatus(status string) {
	if s != nil {
		s.Status = firstNonEmpty(status, "idle")
	}
}

func (s *State) AddMessage(role MessageRole, text string) {
	if s == nil {
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	s.Messages = append(s.Messages, Message{Role: role, Text: text})
}

func (s *State) AddHistoryLines(displayLines []string, rawLines []string) {
	if s == nil {
		return
	}
	display := strings.TrimRight(strings.Join(displayLines, "\n"), "\r\n")
	if strings.TrimSpace(display) == "" {
		return
	}
	raw := strings.TrimRight(strings.Join(rawLines, "\n"), "\r\n")
	s.Messages = append(s.Messages, Message{Role: RoleHistory, Text: display, RawText: raw})
}

func (s *State) ClearMessages() {
	if s != nil {
		s.Messages = nil
	}
}

func (s *State) ResetThread() {
	if s != nil {
		s.ThreadID = ""
		s.ThreadName = ""
		s.Messages = nil
		s.Status = "idle"
		s.TotalTokenUsage = TokenUsage{}
		s.LastTokenUsage = TokenUsage{}
		s.ModelContextWindow = nil
		s.RateLimits = nil
		s.RateLimitsLoaded = false
		s.RateLimitsRefreshing = false
	}
}

func (s *State) RenderWelcome() string {
	var builder strings.Builder
	builder.WriteString("gcode (Go)\n")
	builder.WriteString(s.RenderStatusCard())
	builder.WriteString("\n")
	if cwd := strings.TrimSpace(s.CWD); cwd != "" {
		builder.WriteString("Directory: ")
		builder.WriteString(cwd)
		builder.WriteString("\n")
	}
	builder.WriteString("Type /help for commands or /exit to quit.\n")
	return builder.String()
}

// RenderStatusCard mirrors the information density of the Rust CLI's /status
// panel while keeping RenderStatusLine compact for the footer.
func (s *State) RenderStatusCard() string {
	return s.RenderStatusCardWidth(80)
}

func (s *State) RenderStatusCardWidth(width int) string {
	if width < 44 {
		width = 44
	}
	if width > 100 {
		width = 100
	}
	if s == nil {
		s = NewState(nil)
	}
	availableInnerWidth := width - 4
	provider := statusModelProvider(s.Provider)
	reasoning := displayValue(s.EffectiveReasoningEffort(), "default")
	model := displayValue(s.Model, "default") + " (reasoning " + reasoning + ", summaries auto)"
	header := " >_ gcode"
	if version := strings.TrimSpace(s.CLIVersion); version != "" {
		header += " (v" + version + ")"
	}

	type statusFieldData struct {
		label            string
		value            string
		percentRemaining float64
		resets           string
		details          string
		window           bool
	}
	fields := []statusFieldData{{label: "Model", value: model}}
	if provider != "" {
		fields = append(fields, statusFieldData{label: "Model provider", value: provider})
	}
	fields = append(fields,
		statusFieldData{label: "Directory", value: displayValue(s.CWD, "-")},
		statusFieldData{label: "Permissions", value: statusPermissions(s.Sandbox, s.ApprovalPolicy)},
		statusFieldData{label: "Agents.md", value: displayValue(s.AgentsSummary, "<none>")},
	)
	if account := strings.TrimSpace(s.AccountDisplay); account != "" {
		fields = append(fields, statusFieldData{label: "Account", value: account})
	}
	if threadName := strings.TrimSpace(s.ThreadName); threadName != "" {
		fields = append(fields, statusFieldData{label: "Thread name", value: threadName})
	}
	collaborationMode := "Default"
	if s.PlanMode {
		collaborationMode = "Plan"
	}
	fields = append(fields, statusFieldData{label: "Collaboration mode", value: collaborationMode})
	if threadID := strings.TrimSpace(s.ThreadID); threadID != "" {
		fields = append(fields, statusFieldData{label: "Session", value: threadID})
	}

	usageFields := make([]statusFieldData, 0, 2+len(s.RateLimits))
	if !s.HasChatGPTAccount {
		usageFields = append(usageFields, statusFieldData{label: "Token usage", value: s.statusTokenUsage()})
	}
	if contextWindow := s.statusContextWindow(); contextWindow != "" {
		usageFields = append(usageFields, statusFieldData{label: "Context window", value: contextWindow})
	}
	if len(s.RateLimits) == 0 {
		limitsText := "data not available yet"
		if s.RateLimitsRefreshing {
			limitsText = "refresh requested; run /status again shortly."
		} else if s.RateLimitsLoaded {
			limitsText = "not available for this account"
		}
		usageFields = append(usageFields, statusFieldData{label: "Limits", value: limitsText})
	} else {
		stale := false
		for _, limit := range s.RateLimits {
			label := statusRateLimitLabel(limit.Label)
			if text := strings.TrimSpace(limit.Text); limit.IsText || text != "" {
				usageFields = append(usageFields, statusFieldData{label: label, value: text})
				continue
			}
			remaining := clampStatusPercent(100 - limit.UsedPercent)
			resets := ""
			if limit.ResetsAt != nil {
				resets = formatStatusReset(*limit.ResetsAt, limit.CapturedAt)
			}
			usageFields = append(usageFields, statusFieldData{
				label: label, value: statusRateLimitSummary(remaining), percentRemaining: remaining,
				resets: resets, details: strings.TrimSpace(limit.Details), window: true,
			})
			if !limit.CapturedAt.IsZero() && time.Since(limit.CapturedAt) > 15*time.Minute {
				stale = true
			}
		}
		if stale {
			warning := "limits may be stale - start new turn to refresh."
			if s.RateLimitsRefreshing {
				warning = "limits may be stale - run /status again shortly."
			}
			usageFields = append(usageFields, statusFieldData{label: "Warning", value: warning})
		}
	}

	labelWidth := DisplayWidth("Token usage")
	for _, item := range fields {
		if candidate := DisplayWidth(item.label); candidate > labelWidth {
			labelWidth = candidate
		}
	}
	for _, item := range usageFields {
		if candidate := DisplayWidth(item.label); candidate > labelWidth {
			labelWidth = candidate
		}
	}
	valueOffset := 1 + labelWidth + 1 + 3
	valueWidth := max(0, availableInnerWidth-valueOffset)
	for index := range fields {
		if fields[index].label == "Directory" && DisplayWidth(fields[index].value) > valueWidth {
			fields[index].value = CenterTruncatePath(fields[index].value, valueWidth)
		}
	}

	rows := []string{header, ""}
	providerLower := strings.ToLower(strings.TrimSpace(s.Provider))
	if providerLower == "" || strings.Contains(providerLower, "openai") || strings.Contains(providerLower, "codex") {
		rows = append(rows,
			"Visit https://chatgpt.com/codex/settings/usage for up-to-date",
			"information on rate limits and credits", "",
		)
	}
	for _, item := range fields {
		rows = append(rows, renderStatusField(item.label, item.value, labelWidth))
	}
	rows = append(rows, "")
	for _, item := range usageFields {
		value := item.value
		if item.window {
			full := renderStatusLimitProgressBar(item.percentRemaining) + " " + item.value
			if DisplayWidth(full) <= valueWidth {
				value = full
			}
		}
		line := renderStatusField(item.label, value, labelWidth)
		if item.resets != "" {
			reset := "(resets " + item.resets + ")"
			if DisplayWidth(line)+1+DisplayWidth(reset) <= availableInnerWidth {
				line += " " + reset
			} else {
				rows = append(rows, line)
				line = strings.Repeat(" ", valueOffset) + reset
			}
		}
		rows = append(rows, line)
		if item.details != "" {
			for _, detail := range wrapStatusText(item.details, max(1, valueWidth)) {
				rows = append(rows, strings.Repeat(" ", valueOffset)+detail)
			}
		}
	}

	contentWidth := 0
	for _, row := range rows {
		if candidate := DisplayWidth(row); candidate > contentWidth {
			contentWidth = candidate
		}
	}
	contentWidth = min(contentWidth, availableInnerWidth)
	border := "╭" + strings.Repeat("─", contentWidth+2) + "╮"
	out := []string{border}
	for _, row := range rows {
		row = truncateStatusRow(row, contentWidth)
		out = append(out, "│ "+row+strings.Repeat(" ", contentWidth-DisplayWidth(row))+" │")
	}
	out = append(out, "╰"+strings.Repeat("─", contentWidth+2)+"╯")
	return strings.Join(out, "\n")
}

func (s *State) statusTokenUsage() string {
	usage := s.TotalTokenUsage
	return fmt.Sprintf("%s total  (%s input + %s output)", formatStatusTokensCompact(usage.BlendedTotal()), formatStatusTokensCompact(usage.NonCachedInput()), formatStatusTokensCompact(usage.OutputTokens))
}

func (s *State) statusContextWindow() string {
	if s.ModelContextWindow == nil || *s.ModelContextWindow <= 0 {
		return ""
	}
	used := s.LastTokenUsage.TokensInContextWindow()
	return fmt.Sprintf("%d%% left (%s used / %s)", s.LastTokenUsage.PercentOfContextWindowRemaining(*s.ModelContextWindow), formatStatusTokensCompact(used), formatStatusTokensCompact(*s.ModelContextWindow))
}

func renderStatusField(label string, value string, labelWidth int) string {
	return " " + label + ":" + strings.Repeat(" ", 3+max(0, labelWidth-DisplayWidth(label))) + value
}

func truncateStatusRow(value string, width int) string {
	if DisplayWidth(value) <= width {
		return value
	}
	if width <= 1 {
		return TruncateToWidth(value, width)
	}
	return TruncateToWidth(value, width-1) + "…"
}

func statusModelProvider(provider string) string {
	provider = strings.TrimSpace(provider)
	switch strings.ToLower(provider) {
	case "", "openai", "codex", "openai-codex":
		return ""
	default:
		return provider
	}
}

func statusPermissions(sandbox string, approval string) string {
	sandbox = strings.TrimSpace(sandbox)
	approval = strings.TrimSpace(approval)
	if sandbox == "" {
		sandbox = "read-only"
	}
	if approval == "" {
		approval = "on-request"
	}
	approvalLabel := displayValue(approval, "default")
	if strings.EqualFold(approval, "on-request") {
		approvalLabel = "Ask for approval"
	}
	switch strings.ToLower(sandbox) {
	case "read-only", ":read-only":
		return "Read Only (" + approvalLabel + ")"
	case "workspace", "workspace-write", ":workspace", "auto":
		return "Workspace (" + approvalLabel + ")"
	case "danger-full-access", "full-access", ":danger-full-access":
		if strings.EqualFold(approval, "never") {
			return "Full Access"
		}
		return "No Sandbox (" + approvalLabel + ")"
	default:
		return "Custom (" + displayValue(sandbox, "default") + ", " + approvalLabel + ")"
	}
}

func formatStatusTokensCompact(value int64) string {
	if value <= 0 {
		return "0"
	}
	if value < 1000 {
		return fmt.Sprintf("%d", value)
	}
	scaled := float64(value) / 1_000
	suffix := "K"
	if value >= 1_000_000_000_000 {
		scaled, suffix = float64(value)/1_000_000_000_000, "T"
	} else if value >= 1_000_000_000 {
		scaled, suffix = float64(value)/1_000_000_000, "B"
	} else if value >= 1_000_000 {
		scaled, suffix = float64(value)/1_000_000, "M"
	}
	decimals := 0
	if scaled < 10 {
		decimals = 2
	} else if scaled < 100 {
		decimals = 1
	}
	formatted := fmt.Sprintf("%.*f", decimals, scaled)
	if strings.Contains(formatted, ".") {
		formatted = strings.TrimRight(strings.TrimRight(formatted, "0"), ".")
	}
	return formatted + suffix
}

func statusRateLimitLabel(label string) string {
	label = displayValue(strings.TrimSpace(label), "usage")
	lower := strings.ToLower(label)
	if strings.HasSuffix(lower, "limit") || lower == "limits" || lower == "credits" || lower == "warning" {
		return label
	}
	runes := []rune(label)
	if len(runes) > 0 {
		runes[0] = []rune(strings.ToUpper(string(runes[0])))[0]
	}
	return string(runes) + " limit"
}

func clampStatusPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func statusRateLimitSummary(percentRemaining float64) string {
	return fmt.Sprintf("%.0f%% left", percentRemaining)
}

func renderStatusLimitProgressBar(percentRemaining float64) string {
	filled := int(clampStatusPercent(percentRemaining)/100*20 + 0.5)
	return "[" + strings.Repeat("■", filled) + strings.Repeat("□", 20-filled) + "]"
}

func formatStatusReset(value time.Time, capturedAt time.Time) string {
	value = value.Local()
	if capturedAt.IsZero() {
		capturedAt = time.Now()
	}
	capturedAt = capturedAt.Local()
	if value.Year() == capturedAt.Year() && value.YearDay() == capturedAt.YearDay() {
		return value.Format("15:04")
	}
	return value.Format("15:04 on 2 Jan")
}

func wrapStatusText(value string, width int) []string {
	words := strings.Fields(value)
	if len(words) == 0 {
		return nil
	}
	lines := []string{words[0]}
	for _, word := range words[1:] {
		last := len(lines) - 1
		if DisplayWidth(lines[last])+1+DisplayWidth(word) <= width {
			lines[last] += " " + word
		} else {
			lines = append(lines, word)
		}
	}
	return lines
}

func (s *State) RenderPrompt() string {
	return "> "
}

func (s *State) RenderStatusLine() string {
	if s == nil {
		return "Thread: new | Status: idle | Model: default | Approval: default | Sandbox: default"
	}
	parts := []string{
		"Thread: " + displayValue(s.ThreadID, "new"),
		"Status: " + displayValue(s.Status, "idle"),
		"Model: " + displayValue(s.Model, "default"),
		"Approval: " + displayValue(s.ApprovalPolicy, "default"),
		"Sandbox: " + displayValue(s.Sandbox, "default"),
	}
	if s.PlanMode {
		parts = append(parts, "Mode: Plan")
	}
	if strings.TrimSpace(s.ReasoningEffort) != "" {
		parts = append(parts, "Reasoning: "+strings.TrimSpace(s.ReasoningEffort))
	}
	if strings.TrimSpace(s.PlanModeReasoningEffort) != "" {
		parts = append(parts, "Plan Reasoning: "+strings.TrimSpace(s.PlanModeReasoningEffort))
	}
	if strings.TrimSpace(s.Personality) != "" {
		parts = append(parts, "Personality: "+strings.TrimSpace(s.Personality))
	}
	if strings.TrimSpace(s.Provider) != "" {
		parts = append(parts, "Provider: "+strings.TrimSpace(s.Provider))
	}
	if s.Search {
		parts = append(parts, "Search: on")
	}
	if s.NoAltScreen {
		parts = append(parts, "Alt screen: off")
	}
	return strings.Join(parts, " | ")
}

func (s *State) EffectiveReasoningEffort() string {
	if s == nil {
		return ""
	}
	if s.PlanMode {
		if effort := strings.TrimSpace(s.PlanModeReasoningEffort); effort != "" {
			return effort
		}
		return planModeDefaultReasoningEffort
	}
	return strings.TrimSpace(s.ReasoningEffort)
}

func (s *State) ShouldPromptPlanReasoningScope(selectedModel string, selectedEffort string) bool {
	if s == nil || !s.PlanMode {
		return false
	}
	if strings.TrimSpace(selectedModel) == "" || strings.TrimSpace(selectedModel) != strings.TrimSpace(s.Model) {
		return false
	}
	selectedEffort = strings.TrimSpace(selectedEffort)
	if selectedEffort != s.EffectiveReasoningEffort() {
		return true
	}
	if strings.TrimSpace(s.PlanModeReasoningEffort) != "" && selectedEffort != strings.TrimSpace(s.ReasoningEffort) {
		return true
	}
	return false
}

func (s *State) RenderFrame() string {
	var builder strings.Builder
	builder.WriteString(s.RenderStatusLine())
	builder.WriteString("\n")
	builder.WriteString("----------------------------------------\n")
	if s == nil || len(s.Messages) == 0 {
		builder.WriteString("No messages yet.\n")
	} else {
		for _, message := range s.Messages {
			if message.Role == RoleHistory {
				builder.WriteString(strings.TrimRight(message.Text, "\r\n"))
				builder.WriteString("\n")
				continue
			}
			role := string(message.Role)
			if role == "" {
				role = string(RoleSystem)
			}
			text := message.Text
			if message.Role == RoleAssistant {
				text = eventmap.StripHiddenAssistantMarkup(text, false)
			}
			builder.WriteString(roleTitle(role))
			builder.WriteString(":\n")
			builder.WriteString(indentLines(text, "  "))
			builder.WriteString("\n")
		}
	}
	builder.WriteString("----------------------------------------\n")
	builder.WriteString("Commands: /help /keymap /status /usage /goal /statusline /title /debug-config /new /clear /copy /raw /diff /ps /stop /model /personality /permissions /approval /sandbox /experimental /mcp /skills /plugins /apps /review /rename /theme /pets /plan /side /btw /agent /subagents /ide /vim /import /hooks /memories /feedback /resume /fork /archive /unarchive /delete /attach /image /url-image /clear-attachments /editor /logout /quit /exit\n")
	return builder.String()
}

func (s *State) RenderHelp() string {
	return strings.Join([]string{
		"Codex TUI commands:",
		"  /help                 show this command list",
		"  /keymap [CMD]         show or edit Rust TUI key bindings",
		"  /status               show current thread, model, approval and sandbox state",
		"  /usage [VIEW]         show account usage or token activity: daily, weekly, cumulative",
		"  /goal [ACTION]        show or manage the current thread goal",
		"  /statusline [ITEMS]   configure which items appear in the status line",
		"  /title [ITEMS]        configure which items appear in the terminal title",
		"  /debug-config         show config layers and requirement sources for debugging",
		"  /new                  start a fresh local thread",
		"  /clear                clear the visible transcript",
		"  /copy                 copy the last agent response as markdown",
		"  /raw [on|off]         toggle raw scrollback mode for clean terminal selection",
		"  /diff                 show git diff, including untracked files",
		"  /ps                   list background terminals",
		"  /stop                 stop all background terminals",
		"  /model [MODEL]        show or set the model for following turns",
		"  /personality          choose a communication style for Codex",
		"  /permissions          open Rust-style model permissions menu",
		"  /approval [POLICY]    show or set approval policy: untrusted, on-request, never",
		"  /sandbox [PROFILE]    show or set sandbox profile",
		"  /experimental         toggle experimental features",
		"  /mcp [verbose]        list configured MCP tools",
		"  /skills               list or manage skills",
		"  /plugins              browse plugins",
		"  /apps                 manage apps",
		"  /review [PROMPT]      review current changes or custom instructions",
		"  /rename [NAME]        rename the current thread",
		"  /theme                choose a syntax highlighting theme",
		"  /pets                 choose or hide the terminal pet",
		"  /plan [PROMPT]        switch to Plan mode",
		"  /side [PROMPT]        start a side conversation",
		"  /agent                switch the active agent thread",
		"  /ide                  inspect IDE context",
		"  /vim                  toggle Vim mode for the composer",
		"  /import               import setup and recent chats from Claude Code",
		"  /hooks                view and manage lifecycle hooks",
		"  /memories             configure memory use and generation",
		"  /feedback             send logs to maintainers",
		"  /setup-default-sandbox set up elevated agent sandbox",
		"  /sandbox-add-read-dir PATH let the sandbox read a directory",
		"  /resume               choose a previous session to resume",
		"  /fork                 choose a previous session to fork",
		"  /archive              archive a previous session",
		"  /unarchive            unarchive a previous session",
		"  /delete               delete a previous session",
		"  /attach PATH          attach a file path to the next prompt",
		"  /image PATH           attach a local image path to the next prompt",
		"  /url-image URL        attach a remote image URL to the next prompt",
		"  /clear-attachments    remove pending prompt attachments",
		"  /editor               edit current draft in $VISUAL or $EDITOR",
		"  /logout               log out of Codex",
		"  /exit                 quit",
	}, "\n") + "\n"
}

func (s *State) RenderSetting(name string, value string) string {
	return fmt.Sprintf("%s: %s\n", name, displayValue(value, "default"))
}

type Command string

const (
	CommandHelp             Command = "help"
	CommandKeymap           Command = "keymap"
	CommandStatus           Command = "status"
	CommandUsage            Command = "usage"
	CommandGoal             Command = "goal"
	CommandStatusline       Command = "statusline"
	CommandTitle            Command = "title"
	CommandDebugConfig      Command = "debug-config"
	CommandNew              Command = "new"
	CommandInit             Command = "init"
	CommandCompact          Command = "compact"
	CommandClear            Command = "clear"
	CommandCopy             Command = "copy"
	CommandRaw              Command = "raw"
	CommandDiff             Command = "diff"
	CommandPs               Command = "ps"
	CommandStop             Command = "stop"
	CommandModel            Command = "model"
	CommandFast             Command = "fast"
	CommandPersonality      Command = "personality"
	CommandPlan             Command = "plan"
	CommandAgent            Command = "agent"
	CommandSide             Command = "side"
	CommandPermissions      Command = "permissions"
	CommandApproval         Command = "approval"
	CommandSandbox          Command = "sandbox"
	CommandExperimental     Command = "experimental"
	CommandReview           Command = "review"
	CommandRename           Command = "rename"
	CommandMention          Command = "mention"
	CommandSkills           Command = "skills"
	CommandHooks            Command = "hooks"
	CommandMcp              Command = "mcp"
	CommandApps             Command = "apps"
	CommandPlugins          Command = "plugins"
	CommandTheme            Command = "theme"
	CommandPets             Command = "pets"
	CommandIde              Command = "ide"
	CommandVim              Command = "vim"
	CommandAutoReview       Command = "approve"
	CommandMemories         Command = "memories"
	CommandFeedback         Command = "feedback"
	CommandApp              Command = "app"
	CommandImport           Command = "import"
	CommandElevateSandbox   Command = "setup-default-sandbox"
	CommandSandboxReadRoot  Command = "sandbox-add-read-dir"
	CommandRollout          Command = "rollout"
	CommandTestApproval     Command = "test-approval"
	CommandMemoryDrop       Command = "debug-m-drop"
	CommandMemoryUpdate     Command = "debug-m-update"
	CommandResume           Command = "resume"
	CommandFork             Command = "fork"
	CommandArchive          Command = "archive"
	CommandUnarchive        Command = "unarchive"
	CommandDelete           Command = "delete"
	CommandAttach           Command = "attach"
	CommandImage            Command = "image"
	CommandURLImage         Command = "url-image"
	CommandClearAttachments Command = "clear-attachments"
	CommandEditor           Command = "editor"
	CommandLogout           Command = "logout"
	CommandExit             Command = "exit"
	CommandUnknown          Command = "unknown"
)

type CommandInvocation struct {
	Command Command
	Args    string
	Name    string
}

func ParseCommand(input string) (*CommandInvocation, bool) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return nil, false
	}
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return nil, false
	}
	name := strings.ToLower(fields[0])
	if name == "exit" || name == "quit" {
		return &CommandInvocation{Command: CommandExit, Name: name}, true
	}
	if !strings.HasPrefix(name, "/") {
		return nil, false
	}
	args := ""
	if len(fields) > 1 {
		args = strings.TrimSpace(strings.TrimPrefix(trimmed, fields[0]))
	}
	switch name {
	case "/help", "/?":
		return &CommandInvocation{Command: CommandHelp, Args: args, Name: name}, true
	case "/keymap", "/keys":
		return &CommandInvocation{Command: CommandKeymap, Args: args, Name: name}, true
	case "/status":
		return &CommandInvocation{Command: CommandStatus, Args: args, Name: name}, true
	case "/usage":
		return &CommandInvocation{Command: CommandUsage, Args: args, Name: name}, true
	case "/goal":
		return &CommandInvocation{Command: CommandGoal, Args: args, Name: name}, true
	case "/statusline":
		return &CommandInvocation{Command: CommandStatusline, Args: args, Name: name}, true
	case "/title":
		return &CommandInvocation{Command: CommandTitle, Args: args, Name: name}, true
	case "/debug-config":
		return &CommandInvocation{Command: CommandDebugConfig, Args: args, Name: name}, true
	case "/new":
		return &CommandInvocation{Command: CommandNew, Args: args, Name: name}, true
	case "/init":
		return &CommandInvocation{Command: CommandInit, Args: args, Name: name}, true
	case "/compact":
		return &CommandInvocation{Command: CommandCompact, Args: args, Name: name}, true
	case "/clear":
		return &CommandInvocation{Command: CommandClear, Args: args, Name: name}, true
	case "/copy":
		return &CommandInvocation{Command: CommandCopy, Args: args, Name: name}, true
	case "/raw":
		return &CommandInvocation{Command: CommandRaw, Args: args, Name: name}, true
	case "/diff":
		return &CommandInvocation{Command: CommandDiff, Args: args, Name: name}, true
	case "/ps":
		return &CommandInvocation{Command: CommandPs, Args: args, Name: name}, true
	case "/stop", "/clean":
		return &CommandInvocation{Command: CommandStop, Args: args, Name: name}, true
	case "/model":
		return &CommandInvocation{Command: CommandModel, Args: args, Name: name}, true
	case "/fast":
		return &CommandInvocation{Command: CommandFast, Args: args, Name: name}, true
	case "/personality":
		return &CommandInvocation{Command: CommandPersonality, Args: args, Name: name}, true
	case "/plan":
		return &CommandInvocation{Command: CommandPlan, Args: args, Name: name}, true
	case "/agent", "/subagents":
		return &CommandInvocation{Command: CommandAgent, Args: args, Name: name}, true
	case "/side", "/btw":
		return &CommandInvocation{Command: CommandSide, Args: args, Name: name}, true
	case "/permissions":
		return &CommandInvocation{Command: CommandPermissions, Args: args, Name: name}, true
	case "/approval":
		return &CommandInvocation{Command: CommandApproval, Args: args, Name: name}, true
	case "/sandbox":
		return &CommandInvocation{Command: CommandSandbox, Args: args, Name: name}, true
	case "/experimental":
		return &CommandInvocation{Command: CommandExperimental, Args: args, Name: name}, true
	case "/review":
		return &CommandInvocation{Command: CommandReview, Args: args, Name: name}, true
	case "/rename":
		return &CommandInvocation{Command: CommandRename, Args: args, Name: name}, true
	case "/mention":
		return &CommandInvocation{Command: CommandMention, Args: args, Name: name}, true
	case "/skills":
		return &CommandInvocation{Command: CommandSkills, Args: args, Name: name}, true
	case "/hooks":
		return &CommandInvocation{Command: CommandHooks, Args: args, Name: name}, true
	case "/mcp":
		return &CommandInvocation{Command: CommandMcp, Args: args, Name: name}, true
	case "/apps":
		return &CommandInvocation{Command: CommandApps, Args: args, Name: name}, true
	case "/plugins":
		return &CommandInvocation{Command: CommandPlugins, Args: args, Name: name}, true
	case "/theme":
		return &CommandInvocation{Command: CommandTheme, Args: args, Name: name}, true
	case "/pets", "/pet":
		return &CommandInvocation{Command: CommandPets, Args: args, Name: name}, true
	case "/ide":
		return &CommandInvocation{Command: CommandIde, Args: args, Name: name}, true
	case "/vim":
		return &CommandInvocation{Command: CommandVim, Args: args, Name: name}, true
	case "/approve":
		return &CommandInvocation{Command: CommandAutoReview, Args: args, Name: name}, true
	case "/memories":
		return &CommandInvocation{Command: CommandMemories, Args: args, Name: name}, true
	case "/feedback":
		return &CommandInvocation{Command: CommandFeedback, Args: args, Name: name}, true
	case "/app":
		return &CommandInvocation{Command: CommandApp, Args: args, Name: name}, true
	case "/import":
		return &CommandInvocation{Command: CommandImport, Args: args, Name: name}, true
	case "/setup-default-sandbox":
		return &CommandInvocation{Command: CommandElevateSandbox, Args: args, Name: name}, true
	case "/sandbox-add-read-dir":
		return &CommandInvocation{Command: CommandSandboxReadRoot, Args: args, Name: name}, true
	case "/rollout":
		return &CommandInvocation{Command: CommandRollout, Args: args, Name: name}, true
	case "/test-approval":
		return &CommandInvocation{Command: CommandTestApproval, Args: args, Name: name}, true
	case "/debug-m-drop":
		return &CommandInvocation{Command: CommandMemoryDrop, Args: args, Name: name}, true
	case "/debug-m-update":
		return &CommandInvocation{Command: CommandMemoryUpdate, Args: args, Name: name}, true
	case "/resume":
		return &CommandInvocation{Command: CommandResume, Args: args, Name: name}, true
	case "/fork":
		return &CommandInvocation{Command: CommandFork, Args: args, Name: name}, true
	case "/archive":
		return &CommandInvocation{Command: CommandArchive, Args: args, Name: name}, true
	case "/unarchive":
		return &CommandInvocation{Command: CommandUnarchive, Args: args, Name: name}, true
	case "/delete":
		return &CommandInvocation{Command: CommandDelete, Args: args, Name: name}, true
	case "/attach":
		return &CommandInvocation{Command: CommandAttach, Args: args, Name: name}, true
	case "/image":
		return &CommandInvocation{Command: CommandImage, Args: args, Name: name}, true
	case "/url-image":
		return &CommandInvocation{Command: CommandURLImage, Args: args, Name: name}, true
	case "/clear-attachments":
		return &CommandInvocation{Command: CommandClearAttachments, Args: args, Name: name}, true
	case "/editor":
		return &CommandInvocation{Command: CommandEditor, Args: args, Name: name}, true
	case "/logout":
		return &CommandInvocation{Command: CommandLogout, Args: args, Name: name}, true
	case "/exit", "/quit":
		return &CommandInvocation{Command: CommandExit, Args: args, Name: name}, true
	default:
		return &CommandInvocation{Command: CommandUnknown, Args: args, Name: name}, true
	}
}

func ValidApprovalPolicy(value string) bool {
	switch strings.TrimSpace(value) {
	case "untrusted", "on-request", "never":
		return true
	default:
		return false
	}
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

func displayValue(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
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

func roleTitle(role string) string {
	role = strings.TrimSpace(role)
	if role == "" {
		return "System"
	}
	return strings.ToUpper(role[:1]) + role[1:]
}
