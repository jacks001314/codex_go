package tui

import (
	"fmt"
	"strings"
)

type MessageRole string

const (
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
	RoleSystem    MessageRole = "system"
)

type Message struct {
	Role MessageRole
	Text string
}

type Options struct {
	Model                   string
	ReasoningEffort         string
	PlanMode                bool
	PlanModeReasoningEffort string
	Provider                string
	ApprovalPolicy          string
	Sandbox                 string
	Search                  bool
	NoAltScreen             bool
}

type State struct {
	ThreadID                string
	Model                   string
	ReasoningEffort         string
	PlanMode                bool
	PlanModeReasoningEffort string
	Provider                string
	ApprovalPolicy          string
	Sandbox                 string
	Search                  bool
	NoAltScreen             bool
	Status                  string
	Messages                []Message
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
		state.Provider = strings.TrimSpace(options.Provider)
		state.ApprovalPolicy = strings.TrimSpace(options.ApprovalPolicy)
		state.Sandbox = strings.TrimSpace(options.Sandbox)
		state.Search = options.Search
		state.NoAltScreen = options.NoAltScreen
	}
	return state
}

func (s *State) SetThreadID(threadID string) {
	if s != nil {
		s.ThreadID = strings.TrimSpace(threadID)
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

func (s *State) ClearMessages() {
	if s != nil {
		s.Messages = nil
	}
}

func (s *State) ResetThread() {
	if s != nil {
		s.ThreadID = ""
		s.Messages = nil
		s.Status = "idle"
	}
}

func (s *State) RenderWelcome() string {
	var builder strings.Builder
	builder.WriteString("Codex interactive session\n")
	builder.WriteString(s.RenderStatusLine())
	builder.WriteString("\n")
	builder.WriteString("Type /help for commands or /exit to quit.\n")
	return builder.String()
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
			role := string(message.Role)
			if role == "" {
				role = string(RoleSystem)
			}
			builder.WriteString(roleTitle(role))
			builder.WriteString(":\n")
			builder.WriteString(indentLines(message.Text, "  "))
			builder.WriteString("\n")
		}
	}
	builder.WriteString("----------------------------------------\n")
	builder.WriteString("Commands: /help /status /new /clear /model /resume /fork /archive /unarchive /delete /attach /image /url-image /clear-attachments /approval /sandbox /exit\n")
	return builder.String()
}

func (s *State) RenderHelp() string {
	return strings.Join([]string{
		"Codex TUI commands:",
		"  /help                 show this command list",
		"  /status               show current thread, model, approval and sandbox state",
		"  /new                  start a fresh local thread",
		"  /clear                clear the visible transcript",
		"  /model [MODEL]        show or set the model for following turns",
		"  /resume               choose a previous session to resume",
		"  /fork                 choose a previous session to fork",
		"  /archive              archive a previous session",
		"  /unarchive            unarchive a previous session",
		"  /delete               delete a previous session",
		"  /attach PATH          attach a file path to the next prompt",
		"  /image PATH           attach a local image path to the next prompt",
		"  /url-image URL        attach a remote image URL to the next prompt",
		"  /clear-attachments    remove pending prompt attachments",
		"  /approval [POLICY]    show or set approval policy: untrusted, on-request, never",
		"  /sandbox [PROFILE]    show or set sandbox profile",
		"  /exit                 quit",
	}, "\n") + "\n"
}

func (s *State) RenderSetting(name string, value string) string {
	return fmt.Sprintf("%s: %s\n", name, displayValue(value, "default"))
}

type Command string

const (
	CommandHelp             Command = "help"
	CommandStatus           Command = "status"
	CommandNew              Command = "new"
	CommandClear            Command = "clear"
	CommandModel            Command = "model"
	CommandResume           Command = "resume"
	CommandFork             Command = "fork"
	CommandArchive          Command = "archive"
	CommandUnarchive        Command = "unarchive"
	CommandDelete           Command = "delete"
	CommandAttach           Command = "attach"
	CommandImage            Command = "image"
	CommandURLImage         Command = "url-image"
	CommandClearAttachments Command = "clear-attachments"
	CommandApproval         Command = "approval"
	CommandSandbox          Command = "sandbox"
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
	case "/status":
		return &CommandInvocation{Command: CommandStatus, Args: args, Name: name}, true
	case "/new":
		return &CommandInvocation{Command: CommandNew, Args: args, Name: name}, true
	case "/clear":
		return &CommandInvocation{Command: CommandClear, Args: args, Name: name}, true
	case "/model":
		return &CommandInvocation{Command: CommandModel, Args: args, Name: name}, true
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
	case "/approval":
		return &CommandInvocation{Command: CommandApproval, Args: args, Name: name}, true
	case "/sandbox":
		return &CommandInvocation{Command: CommandSandbox, Args: args, Name: name}, true
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
