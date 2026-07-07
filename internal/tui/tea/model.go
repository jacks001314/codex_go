package tea

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"codex_go/internal/protocol"
	codextui "codex_go/internal/tui"
	bottompane "codex_go/internal/tui/bottom_pane"
)

const (
	defaultWidth          = 80
	defaultHeight         = 24
	defaultComposerHeight = 3
	minTranscriptHeight   = 3
)

// SubmitFunc lets the runtime layer attach prompt execution without coupling
// the terminal model to app or turn packages.
type SubmitFunc func(prompt string) bubbletea.Cmd

type SubmitRequest struct {
	Prompt      string
	Attachments []bottompane.ComposerAttachment
}

type SubmitRequestFunc func(request SubmitRequest) bubbletea.Cmd

type queuedSubmission struct {
	Request      SubmitRequest
	ParseCommand bool
}

type SessionActionFunc func(selection codextui.SessionSelection) (*codextui.SessionSummary, error)

type StatusMsg struct {
	Status string
}

type TurnCompletedMsg struct {
	ThreadID         string
	AssistantMessage string
	Err              error
}

type ThreadEventMsg struct {
	Event protocol.ThreadEvent
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
	Width              int
	Height             int
	NoAltScreen        bool
	Placeholder        string
	ModelPickerOptions []codextui.ModelPickerOption
	SessionPickerItems []codextui.SessionSummary
	SessionPickerCWD   string
	OnSubmit           SubmitFunc
	OnSubmitRequest    SubmitRequestFunc
	OnModalResponse    ModalResponseFunc
	OnSessionAction    SessionActionFunc
}

type Model struct {
	State *codextui.State

	transcript viewport.Model
	composer   textarea.Model

	width  int
	height int

	statusStyle lipgloss.Style
	footerStyle lipgloss.Style
	bottomStyle lipgloss.Style

	notice          string
	bottom          []string
	attachments     []bottompane.ComposerAttachment
	modal           *modalState
	modelPickerOpts []codextui.ModelPickerOption
	sessionItems    []codextui.SessionSummary
	sessionCWD      string
	onSubmit        SubmitFunc
	onSubmitRequest SubmitRequestFunc
	onModalResponse ModalResponseFunc
	onSessionAction SessionActionFunc
	submitted       []string
	submitRequests  []SubmitRequest
	queued          []queuedSubmission
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

	model := &Model{
		State:           state,
		transcript:      viewport.New(defaultWidth, defaultHeight-defaultComposerHeight-2),
		composer:        composer,
		statusStyle:     lipgloss.NewStyle().Bold(true),
		footerStyle:     lipgloss.NewStyle(),
		bottomStyle:     lipgloss.NewStyle(),
		modelPickerOpts: append([]codextui.ModelPickerOption(nil), options.ModelPickerOptions...),
		sessionItems:    append([]codextui.SessionSummary(nil), options.SessionPickerItems...),
		sessionCWD:      strings.TrimSpace(options.SessionPickerCWD),
		onSubmit:        options.OnSubmit,
		onSubmitRequest: options.OnSubmitRequest,
		onModalResponse: options.OnModalResponse,
		onSessionAction: options.OnSessionAction,
	}
	model.resize(firstPositive(options.Width, defaultWidth), firstPositive(options.Height, defaultHeight))
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
	return m.composer.Focus()
}

func (m *Model) Update(message bubbletea.Msg) (bubbletea.Model, bubbletea.Cmd) {
	if m == nil {
		return m, nil
	}
	switch msg := message.(type) {
	case bubbletea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
		return m, nil
	case StatusMsg:
		m.State.SetStatus(msg.Status)
		m.refreshTranscript()
		return m, nil
	case TurnCompletedMsg:
		m.applyTurnCompleted(msg)
		return m, m.submitNextQueued()
	case ThreadEventMsg:
		m.applyThreadEvent(msg.Event)
		return m, nil
	case StreamStartedMsg:
		return m, waitForStream(msg.Messages)
	case ModalRequestMsg:
		m.openModal(msg)
		return m, nil
	case ApprovalRequestMsg:
		m.openApprovalModal(msg)
		return m, nil
	case ElicitationRequestMsg:
		m.openElicitationModal(msg)
		return m, nil
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
		switch msg.Type {
		case bubbletea.KeyCtrlC, bubbletea.KeyCtrlD:
			return m, bubbletea.Quit
		}
		if m.modal != nil {
			return m, m.updateModal(msg)
		}
		switch msg.Type {
		case bubbletea.KeyEnter:
			if m.isTaskRunning() {
				return m, m.queueComposer(true)
			}
			return m, m.submitComposer()
		case bubbletea.KeyTab:
			if m.isTaskRunning() {
				return m, m.queueComposer(true)
			}
			if m.shouldSubmitOnTab() {
				return m, m.submitComposer()
			}
		}
	}

	var cmd bubbletea.Cmd
	m.transcript, cmd = m.transcript.Update(message)
	var composerCmd bubbletea.Cmd
	m.composer, composerCmd = m.composer.Update(message)
	return m, bubbletea.Batch(cmd, composerCmd)
}

func (m *Model) View() string {
	if m == nil {
		return ""
	}
	m.ensureSize()
	m.refreshTranscript()

	sections := []string{
		m.statusStyle.Render(m.State.RenderStatusLine()),
		m.transcript.View(),
	}
	if bottom := m.renderBottomPane(); bottom != "" {
		sections = append(sections, m.bottomStyle.Render(bottom))
	}
	if modal := m.renderModal(); modal != "" {
		sections = append(sections, m.bottomStyle.Render(modal))
	} else {
		sections = append(sections, m.composer.View())
	}
	if strings.TrimSpace(m.notice) != "" {
		sections = append(sections, m.footerStyle.Render(m.notice))
	}
	sections = append(sections, m.footerStyle.Render("Enter send | Ctrl+C quit | /help commands"))
	return lipgloss.JoinVertical(lipgloss.Left, sections...)
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

func (m *Model) Size() (int, int) {
	if m == nil {
		return 0, 0
	}
	return m.width, m.height
}

func (m *Model) submitComposer() bubbletea.Cmd {
	input := strings.TrimSpace(m.composer.Value())
	m.composer.Reset()
	if input == "" && len(m.attachments) == 0 {
		return nil
	}
	if invocation, ok := codextui.ParseCommand(input); ok {
		return m.applyCommand(invocation)
	}
	request := SubmitRequest{
		Prompt:      input,
		Attachments: cloneComposerAttachments(m.attachments),
	}
	m.attachments = nil
	return m.submitRequest(request, false)
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
	m.State.AddMessage(codextui.RoleUser, displayPrompt)
	if m.onSubmit == nil && m.onSubmitRequest == nil {
		m.State.SetStatus("pending")
	} else {
		m.State.SetStatus("running")
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
	if input == "" && len(m.attachments) == 0 {
		return nil
	}
	request := SubmitRequest{
		Prompt:      input,
		Attachments: cloneComposerAttachments(m.attachments),
	}
	m.attachments = nil
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

func (m *Model) isTaskRunning() bool {
	return m != nil && m.State != nil && strings.EqualFold(strings.TrimSpace(m.State.Status), "running")
}

func (m *Model) isIdle() bool {
	return m != nil && m.State != nil && strings.EqualFold(strings.TrimSpace(m.State.Status), "idle")
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

func (m *Model) applyTurnCompleted(message TurnCompletedMsg) {
	if message.Err != nil {
		m.State.SetStatus("error")
		m.State.AddMessage(codextui.RoleSystem, "Error: "+message.Err.Error())
		m.notice = message.Err.Error()
		m.refreshTranscript()
		return
	}
	if strings.TrimSpace(message.ThreadID) != "" {
		m.State.SetThreadID(message.ThreadID)
	}
	if strings.TrimSpace(message.AssistantMessage) != "" {
		m.mergeAssistantFinal(message.AssistantMessage)
	}
	m.State.SetStatus("idle")
	m.notice = ""
	m.refreshTranscript()
}

func (m *Model) applyThreadEvent(event protocol.ThreadEvent) {
	switch event.Type {
	case "thread.started":
		m.State.SetThreadID(event.ThreadID)
		m.addBottomLine("Thread started")
	case "turn.started":
		m.State.SetStatus("running")
		m.addBottomLine("Turn started")
	case "item.started":
		m.applyItemStarted(event.Item)
	case "item.completed":
		m.applyItemCompleted(event.Item)
	case "item.delta":
		m.applyDelta(event.Delta)
	case "turn.completed":
		m.State.SetStatus("idle")
		m.addBottomLine("Turn completed")
	case "turn.failed", "error":
		message := "Unknown error"
		if event.Error != nil && strings.TrimSpace(event.Error.Message) != "" {
			message = strings.TrimSpace(event.Error.Message)
		}
		m.State.SetStatus("error")
		m.State.AddMessage(codextui.RoleSystem, "Error: "+message)
		m.notice = message
	}
	m.refreshTranscript()
}

func (m *Model) applyItemStarted(item *protocol.ThreadItem) {
	if item == nil {
		return
	}
	switch item.Type {
	case "tool_call":
		m.addBottomLine("Tool started: " + displayValue(item.ToolName, item.ID))
	case "agent_message":
		m.addBottomLine("Assistant started")
	}
}

func (m *Model) applyItemCompleted(item *protocol.ThreadItem) {
	if item == nil {
		return
	}
	switch item.Type {
	case "agent_message":
		m.mergeAssistantFinal(item.Text)
	case "tool_call":
		m.addBottomLine("Tool call: " + displayValue(item.ToolName, item.ID))
	case "tool_output":
		if request, ok := approvalRequestFromToolOutput(item); ok {
			m.addBottomLine("Approval required: " + displayValue(item.ToolName, item.ID))
			m.openApprovalModal(request)
			return
		}
		status := "completed"
		if item.Success != nil && !*item.Success {
			status = "failed"
		}
		m.addBottomLine("Tool " + status + ": " + displayValue(item.ToolName, item.ID))
	case "todo_list":
		m.addBottomLine("Plan updated")
	}
}

func (m *Model) applyDelta(delta *protocol.Delta) {
	if delta == nil {
		return
	}
	if delta.Text != "" {
		m.appendAssistantDelta(delta.Text)
		return
	}
	if strings.TrimSpace(delta.Input) != "" {
		m.addBottomLine("Tool input streaming")
	}
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
	switch invocation.Command {
	case codextui.CommandExit:
		return bubbletea.Quit
	case codextui.CommandHelp:
		m.State.AddMessage(codextui.RoleSystem, strings.TrimSpace(m.State.RenderHelp()))
		m.notice = ""
	case codextui.CommandStatus:
		m.State.AddMessage(codextui.RoleSystem, m.State.RenderStatusLine())
		m.notice = ""
	case codextui.CommandNew:
		m.State.ResetThread()
		m.notice = "Started a new local thread."
	case codextui.CommandClear:
		m.State.ClearMessages()
		m.notice = "Cleared visible transcript."
	case codextui.CommandModel:
		m.applyModelSetting(invocation.Args)
	case codextui.CommandResume:
		m.openSessionPicker(codextui.SessionPickerResume)
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
	case codextui.CommandApproval:
		m.applyApprovalSetting(invocation.Args)
	case codextui.CommandSandbox:
		m.applySandboxSetting(invocation.Args)
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
	m.transcript.SetContent(renderTranscript(m.State))
	m.transcript.GotoBottom()
}

func (m *Model) appendAssistantDelta(delta string) {
	if m == nil || delta == "" {
		return
	}
	index := len(m.State.Messages) - 1
	if index >= 0 && m.State.Messages[index].Role == codextui.RoleAssistant {
		m.State.Messages[index].Text += delta
		return
	}
	m.State.Messages = append(m.State.Messages, codextui.Message{Role: codextui.RoleAssistant, Text: delta})
}

func (m *Model) mergeAssistantFinal(text string) {
	if m == nil {
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	index := len(m.State.Messages) - 1
	if index >= 0 && m.State.Messages[index].Role == codextui.RoleAssistant {
		current := strings.TrimSpace(m.State.Messages[index].Text)
		switch {
		case current == text:
			return
		case strings.Contains(text, current):
			m.State.Messages[index].Text = text
			return
		}
	}
	m.State.Messages = append(m.State.Messages, codextui.Message{Role: codextui.RoleAssistant, Text: text})
}

func (m *Model) addBottomLine(line string) {
	if m == nil {
		return
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	m.bottom = append(m.bottom, line)
	if len(m.bottom) > 4 {
		m.bottom = m.bottom[len(m.bottom)-4:]
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

func renderTranscript(state *codextui.State) string {
	if state == nil || len(state.Messages) == 0 {
		return "No messages yet."
	}
	var builder strings.Builder
	for i, message := range state.Messages {
		if i > 0 {
			builder.WriteString("\n\n")
		}
		role := string(message.Role)
		if role == "" {
			role = string(codextui.RoleSystem)
		}
		builder.WriteString(roleTitle(role))
		builder.WriteString(":\n")
		builder.WriteString(indentLines(message.Text, "  "))
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
