package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/internal/appserver"
	"codex_go/internal/appserverdaemon"
	"codex_go/internal/auth"
	"codex_go/internal/cli"
	codexexec "codex_go/internal/exec"
	"codex_go/internal/mcp"
	"codex_go/internal/protocol"
	"codex_go/internal/session"
	"codex_go/internal/tool"
	codextui "codex_go/internal/tui"
	bottompane "codex_go/internal/tui/bottom_pane"
	codextea "codex_go/internal/tui/tea"
	"codex_go/internal/turn"
)

const remoteAddressUsage = "expected `ws://host:port`, `wss://host:port`, `unix://`, or `unix://PATH`"

type interactiveSession struct {
	Root     cli.RootOptions
	Runner   *codexexec.Runner
	Reader   *bufio.Scanner
	Stdout   io.Writer
	Stderr   io.Writer
	ThreadID string
	UI       *codextui.State
}

type interactiveTurnRunner interface {
	RunContext(ctx context.Context, req *codexexec.Request, stdin io.Reader, stdout, stderr io.Writer) (*codexexec.Result, error)
}

type interactiveApprovalBroker struct {
	mu           sync.Mutex
	next         int
	pending      map[string]chan codextea.ModalResponse
	allowSession bool
}

type interactiveElicitationBroker struct {
	mu      sync.Mutex
	next    int
	pending map[string]chan codextea.ModalResponse
}

type interactiveUserInputBroker struct {
	mu      sync.Mutex
	next    int
	pending map[string]chan codextea.ModalResponse
}

func newInteractiveApprovalBroker() *interactiveApprovalBroker {
	return &interactiveApprovalBroker{pending: map[string]chan codextea.ModalResponse{}}
}

func newInteractiveElicitationBroker() *interactiveElicitationBroker {
	return &interactiveElicitationBroker{pending: map[string]chan codextea.ModalResponse{}}
}

func newInteractiveUserInputBroker() *interactiveUserInputBroker {
	return &interactiveUserInputBroker{pending: map[string]chan codextea.ModalResponse{}}
}

func (b *interactiveApprovalBroker) shellApprovalFunc(send func(bubbletea.Msg)) tool.ShellApprovalFunc {
	return func(ctx context.Context, request *tool.ShellApprovalRequest) (tool.ShellApprovalDecision, error) {
		if b == nil {
			return tool.ShellApprovalDecision{}, nil
		}
		if b.sessionApproved() {
			return tool.ShellApprovalDecision{Approved: true, AllowSession: true}, nil
		}
		if send == nil {
			return tool.ShellApprovalDecision{}, errors.New("approval UI is unavailable")
		}
		id, responses := b.registerRequest()
		send(codextea.ApprovalRequestMsg{
			ID:      id,
			Title:   "Run command?",
			Body:    interactiveShellApprovalBody(request),
			Command: interactiveShellApprovalCommand(request),
		})
		select {
		case response := <-responses:
			return b.approvalDecision(response), nil
		case <-ctx.Done():
			b.forgetRequest(id)
			return tool.ShellApprovalDecision{}, ctx.Err()
		}
	}
}

func (b *interactiveApprovalBroker) respond(response codextea.ModalResponse) {
	if b == nil {
		return
	}
	b.mu.Lock()
	ch := b.pending[response.ID]
	delete(b.pending, response.ID)
	b.mu.Unlock()
	if ch != nil {
		ch <- response
		close(ch)
	}
}

func (b *interactiveApprovalBroker) registerRequest() (string, <-chan codextea.ModalResponse) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.next++
	id := fmt.Sprintf("approval-%d", b.next)
	ch := make(chan codextea.ModalResponse, 1)
	b.pending[id] = ch
	return id, ch
}

func (b *interactiveApprovalBroker) forgetRequest(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.pending, id)
}

func (b *interactiveApprovalBroker) sessionApproved() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.allowSession
}

func (b *interactiveApprovalBroker) approvalDecision(response codextea.ModalResponse) tool.ShellApprovalDecision {
	if response.Cancelled {
		return tool.ShellApprovalDecision{}
	}
	switch response.OptionID {
	case "allow_once":
		return tool.ShellApprovalDecision{Approved: true}
	case "allow_session":
		b.mu.Lock()
		b.allowSession = true
		b.mu.Unlock()
		return tool.ShellApprovalDecision{Approved: true, AllowSession: true}
	default:
		return tool.ShellApprovalDecision{}
	}
}

func (b *interactiveElicitationBroker) mcpElicitationFunc(send func(bubbletea.Msg)) mcp.MCPElicitationHandlerFunc {
	return func(ctx context.Context, request *mcp.MCPElicitationRequest) (*mcp.MCPElicitationResponse, error) {
		if b == nil || request == nil {
			return &mcp.MCPElicitationResponse{Action: mcp.MCPElicitationActionCancel}, nil
		}
		if send == nil {
			return &mcp.MCPElicitationResponse{Action: mcp.MCPElicitationActionCancel}, nil
		}
		id, responses := b.registerRequest()
		send(codextea.ElicitationRequestMsg{
			ID:              id,
			ServerName:      request.ServerName,
			RequestID:       interactiveMCPRequestID(request),
			ThreadID:        request.ThreadID,
			TurnID:          request.TurnID,
			Message:         request.Message,
			URL:             request.URL,
			RequestedSchema: request.RequestedSchema,
			Meta:            interactiveMCPMetaMap(request.Meta),
		})
		if ctx == nil {
			ctx = context.Background()
		}
		select {
		case response := <-responses:
			return b.elicitationResponse(response), nil
		case <-ctx.Done():
			b.forgetRequest(id)
			return nil, ctx.Err()
		}
	}
}

func (b *interactiveElicitationBroker) respond(response codextea.ModalResponse) {
	if b == nil || response.Kind != codextea.ModalKindElicitation {
		return
	}
	b.mu.Lock()
	ch := b.pending[response.ID]
	delete(b.pending, response.ID)
	b.mu.Unlock()
	if ch != nil {
		ch <- response
		close(ch)
	}
}

func (b *interactiveElicitationBroker) registerRequest() (string, <-chan codextea.ModalResponse) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.next++
	id := fmt.Sprintf("elicitation-%d", b.next)
	ch := make(chan codextea.ModalResponse, 1)
	b.pending[id] = ch
	return id, ch
}

func (b *interactiveElicitationBroker) forgetRequest(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.pending, id)
}

func (b *interactiveElicitationBroker) elicitationResponse(response codextea.ModalResponse) *mcp.MCPElicitationResponse {
	if response.Cancelled || response.Elicitation == nil {
		return &mcp.MCPElicitationResponse{Action: mcp.MCPElicitationActionCancel}
	}
	result := &mcp.MCPElicitationResponse{
		Action:  mcpElicitationAction(response.Elicitation.Action),
		Content: cloneAnyMapApp(response.Elicitation.Content),
	}
	if strings.TrimSpace(response.Elicitation.Persist) != "" {
		result.Meta = map[string]any{"persist": strings.TrimSpace(response.Elicitation.Persist)}
	}
	return result
}

func mcpElicitationAction(action string) mcp.MCPElicitationAction {
	switch strings.TrimSpace(action) {
	case string(mcp.MCPElicitationActionAccept):
		return mcp.MCPElicitationActionAccept
	case string(mcp.MCPElicitationActionDecline):
		return mcp.MCPElicitationActionDecline
	default:
		return mcp.MCPElicitationActionCancel
	}
}

func (b *interactiveUserInputBroker) userInputResponder(send func(bubbletea.Msg)) tool.UserInputResponder {
	return func(ctx context.Context, args *tool.RequestUserInputArgs) (*tool.UserInputResponse, error) {
		if b == nil || args == nil {
			return &tool.UserInputResponse{Answers: map[string]string{}}, nil
		}
		if send == nil {
			return nil, errors.New("request_user_input UI is unavailable")
		}
		id, responses := b.registerRequest()
		send(codextea.RequestUserInputMsg{
			ID:               id,
			Questions:        interactiveUserInputQuestions(args.Questions),
			AutoResolutionMS: cloneIntPtrApp(args.AutoResolutionMS),
		})
		if ctx == nil {
			ctx = context.Background()
		}
		select {
		case response := <-responses:
			return b.userInputResponse(response), nil
		case <-ctx.Done():
			b.forgetRequest(id)
			return nil, ctx.Err()
		}
	}
}

func (b *interactiveUserInputBroker) respond(response codextea.ModalResponse) {
	if b == nil || response.Kind != codextea.ModalKindUserInput {
		return
	}
	b.mu.Lock()
	ch := b.pending[response.ID]
	delete(b.pending, response.ID)
	b.mu.Unlock()
	if ch != nil {
		ch <- response
		close(ch)
	}
}

func (b *interactiveUserInputBroker) registerRequest() (string, <-chan codextea.ModalResponse) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.next++
	id := fmt.Sprintf("user-input-%d", b.next)
	ch := make(chan codextea.ModalResponse, 1)
	b.pending[id] = ch
	return id, ch
}

func (b *interactiveUserInputBroker) forgetRequest(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.pending, id)
}

func (b *interactiveUserInputBroker) userInputResponse(response codextea.ModalResponse) *tool.UserInputResponse {
	if response.Cancelled || response.UserInput == nil {
		return &tool.UserInputResponse{Answers: map[string]string{}}
	}
	return &tool.UserInputResponse{
		Answers:           cloneStringMapApp(response.UserInput.Answers),
		StructuredAnswers: cloneStringSlicesMapApp(response.UserInput.AnswerLists),
		TimedOut:          response.UserInput.TimedOut,
	}
}

func interactiveUserInputQuestions(questions []tool.UserInputQuestion) []codextui.RequestUserInputQuestion {
	out := make([]codextui.RequestUserInputQuestion, 0, len(questions))
	for _, question := range questions {
		options := make([]codextui.RequestUserInputChoice, 0, len(question.Options))
		for _, option := range question.Options {
			options = append(options, codextui.RequestUserInputChoice{
				Label:       option.Label,
				Description: option.Description,
			})
		}
		out = append(out, codextui.RequestUserInputQuestion{
			Header:   question.Header,
			ID:       question.ID,
			Question: question.Question,
			Options:  options,
		})
	}
	return out
}

func cloneStringMapApp(values map[string]string) map[string]string {
	if values == nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func cloneStringSlicesMapApp(values map[string][]string) map[string][]string {
	if values == nil {
		return map[string][]string{}
	}
	out := make(map[string][]string, len(values))
	for key, value := range values {
		out[key] = append([]string(nil), value...)
	}
	return out
}

func cloneIntPtrApp(value *int) *int {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func interactiveMCPRequestID(request *mcp.MCPElicitationRequest) string {
	if request == nil {
		return ""
	}
	if strings.TrimSpace(request.ElicitationID) != "" {
		return strings.TrimSpace(request.ElicitationID)
	}
	raw := strings.TrimSpace(string(request.ID))
	raw = strings.Trim(raw, `"`)
	return raw
}

func interactiveMCPMetaMap(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneAnyMapApp(typed)
	default:
		return nil
	}
}

func cloneAnyMapApp(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func interactiveShellApprovalBody(request *tool.ShellApprovalRequest) string {
	if request == nil || request.Request == nil {
		return "Command requested elevated permissions."
	}
	req := request.Request
	lines := []string{}
	if strings.TrimSpace(req.ApprovalReason) != "" {
		lines = append(lines, "Reason: "+strings.TrimSpace(req.ApprovalReason))
	}
	if strings.TrimSpace(req.Justification) != "" {
		lines = append(lines, "Justification: "+strings.TrimSpace(req.Justification))
	}
	if strings.TrimSpace(req.CWD) != "" {
		lines = append(lines, "Working directory: "+strings.TrimSpace(req.CWD))
	}
	if req.SandboxPermissions != "" {
		lines = append(lines, "Sandbox permissions: "+string(req.SandboxPermissions))
	}
	if len(req.PrefixRule) > 0 {
		lines = append(lines, "Persistable prefix: "+strings.Join(req.PrefixRule, " "))
	}
	if len(lines) == 0 {
		return "Command requested elevated permissions."
	}
	return strings.Join(lines, "\n")
}

func interactiveShellApprovalCommand(request *tool.ShellApprovalRequest) string {
	if request == nil || request.Request == nil {
		return ""
	}
	req := request.Request
	if strings.TrimSpace(req.HookCommand) != "" {
		return strings.TrimSpace(req.HookCommand)
	}
	return strings.Join(req.Command, " ")
}

func runInteractive(ctx context.Context, root *cli.RootOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	if root == nil {
		root = &cli.RootOptions{}
	}
	cleanRoot := *root
	cleanRoot.Prompt = normalizeInteractivePrompt(cleanRoot.Prompt)
	root = &cleanRoot
	if err := guardInteractiveDumbTerminal(stdin, stderr, os.Getenv("TERM")); err != nil {
		return interactiveFatalExit(stderr, err.Error())
	}
	remoteEndpoint, err := resolveInteractiveRemoteEndpoint(root)
	if err != nil {
		return interactiveFatalExit(stderr, err.Error())
	}
	if remoteEndpoint != nil {
		if strings.TrimSpace(root.Prompt) != "" {
			return runInteractiveRemotePrompt(ctx, root, remoteEndpoint, stdout, stderr)
		}
		if shouldRunInteractiveTUI(stdin, stdout) {
			return runInteractiveRemoteTUI(ctx, root, remoteEndpoint, stdin, stdout)
		}
		return errors.New("interactive remote app-server TUI requires a real terminal")
	}
	if strings.TrimSpace(root.Prompt) != "" {
		return runInteractivePrompt(ctx, root, stdin, stdout, stderr)
	}
	if shouldRunInteractiveTUI(stdin, stdout) {
		return runInteractiveTUI(ctx, root, stdin, stdout)
	}
	session := &interactiveSession{
		Root:   *root,
		Runner: codexexec.NewRunner(auth.DefaultCodexHome()),
		Reader: bufio.NewScanner(stdin),
		Stdout: stdout,
		Stderr: stderr,
		UI:     interactiveUIState(root),
	}
	return session.Run(ctx)
}

func shouldRunInteractiveTUI(stdin io.Reader, stdout io.Writer) bool {
	return isRealTerminal(stdin) && isRealTerminal(stdout)
}

func isRealTerminal(value any) bool {
	file, ok := value.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func runInteractiveTUI(ctx context.Context, root *cli.RootOptions, stdin io.Reader, stdout io.Writer) error {
	state := interactiveUIState(root)
	runner := codexexec.NewRunner(auth.DefaultCodexHome())
	approvalBroker := newInteractiveApprovalBroker()
	elicitationBroker := newInteractiveElicitationBroker()
	userInputBroker := newInteractiveUserInputBroker()
	options := codextea.Options{
		NoAltScreen:        root != nil && root.Shared.NoAltScreen,
		SessionPickerItems: interactiveSessionPickerItems(root),
		SessionPickerCWD:   interactiveSessionPickerCWD(root),
		OnSessionAction:    interactiveSessionActionHandler(root),
		OnSubmitRequest: func(request codextea.SubmitRequest) bubbletea.Cmd {
			return interactiveTurnCommandWithRequest(ctx, root, runner, state, request, approvalBroker, elicitationBroker, userInputBroker)
		},
		OnModalResponse: func(response codextea.ModalResponse) bubbletea.Cmd {
			approvalBroker.respond(response)
			elicitationBroker.respond(response)
			userInputBroker.respond(response)
			return nil
		},
	}
	_, err := codextea.Run(ctx, state, options, stdin, stdout)
	return err
}

func interactiveSessionPickerItems(root *cli.RootOptions) []codextui.SessionSummary {
	items, err := codextui.LoadSessionSummariesFromStore(newSessionStore(), codextui.SessionSourceOptions{
		CWD:             interactiveSessionPickerCWD(root),
		IncludeArchived: true,
	})
	if err != nil {
		return nil
	}
	return items
}

func interactiveSessionActionHandler(root *cli.RootOptions) codextea.SessionActionFunc {
	return func(selection codextui.SessionSelection) (*codextui.SessionSummary, error) {
		store := newSessionStore()
		threadID := session.ThreadID(strings.TrimSpace(selection.Target.ThreadID))
		if threadID == "" {
			return nil, errors.New("session action requires a thread id")
		}
		switch selection.Kind {
		case codextui.SessionSelectionFork:
			record, err := store.Fork(threadID, session.ForkOptions{Mode: session.ForkAll})
			if err != nil {
				return nil, err
			}
			return firstSessionSummary(store, record), nil
		case codextui.SessionSelectionArchive:
			if err := store.Archive(threadID); err != nil {
				return nil, err
			}
			return nil, nil
		case codextui.SessionSelectionUnarchive:
			record, err := store.Unarchive(threadID)
			if err != nil {
				return nil, err
			}
			return firstSessionSummary(store, record), nil
		case codextui.SessionSelectionDelete:
			threadIDs, err := store.SubtreeThreadIDs(threadID)
			if err != nil {
				return nil, err
			}
			for _, id := range session.DeleteOrderForSubtree(threadIDs) {
				if err := store.Delete(id); err != nil && !errors.Is(err, session.ErrThreadNotFound) {
					return nil, err
				}
			}
			return nil, nil
		default:
			return nil, nil
		}
	}
}

func firstSessionSummary(store *session.Store, record *session.Record) *codextui.SessionSummary {
	if record == nil {
		return nil
	}
	summaries := codextui.SessionSummariesFromRecords(store, []session.Record{*record})
	if len(summaries) == 0 {
		return nil
	}
	return &summaries[0]
}

func interactiveSessionPickerCWD(root *cli.RootOptions) string {
	if root == nil {
		return ""
	}
	return strings.TrimSpace(root.Shared.CWD)
}

func interactiveTurnCommand(ctx context.Context, root *cli.RootOptions, runner interactiveTurnRunner, state *codextui.State, prompt string, approvalBroker *interactiveApprovalBroker, elicitationBroker *interactiveElicitationBroker, userInputBroker *interactiveUserInputBroker) bubbletea.Cmd {
	return interactiveTurnCommandWithRequest(ctx, root, runner, state, codextea.SubmitRequest{Prompt: prompt}, approvalBroker, elicitationBroker, userInputBroker)
}

func interactiveTurnCommandWithRequest(ctx context.Context, root *cli.RootOptions, runner interactiveTurnRunner, state *codextui.State, request codextea.SubmitRequest, approvalBroker *interactiveApprovalBroker, elicitationBroker *interactiveElicitationBroker, userInputBroker *interactiveUserInputBroker) bubbletea.Cmd {
	return func() bubbletea.Msg {
		messages := make(chan bubbletea.Msg, 256)
		go runInteractiveTurn(ctx, root, runner, state, request, messages, approvalBroker, elicitationBroker, userInputBroker)
		return codextea.StreamStartedMsg{Messages: messages}
	}
}

func runInteractiveTurn(ctx context.Context, root *cli.RootOptions, runner interactiveTurnRunner, state *codextui.State, request codextea.SubmitRequest, messages chan<- bubbletea.Msg, approvalBroker *interactiveApprovalBroker, elicitationBroker *interactiveElicitationBroker, userInputBroker *interactiveUserInputBroker) {
	defer close(messages)
	send := func(message bubbletea.Msg) {
		select {
		case messages <- message:
		case <-ctx.Done():
		}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if root == nil {
		root = &cli.RootOptions{}
	}
	if runner == nil {
		send(codextea.TurnCompletedMsg{Err: errors.New("interactive runner is nil")})
		return
	}
	if concrete, ok := runner.(*codexexec.Runner); ok && approvalBroker != nil {
		previousApproval := concrete.ShellApproval
		concrete.ShellApproval = approvalBroker.shellApprovalFunc(send)
		defer func() {
			concrete.ShellApproval = previousApproval
		}()
	}
	if concrete, ok := runner.(*codexexec.Runner); ok && elicitationBroker != nil {
		previousElicitation := concrete.MCPElicitation
		concrete.MCPElicitation = elicitationBroker.mcpElicitationFunc(send)
		defer func() {
			concrete.MCPElicitation = previousElicitation
		}()
	}
	if concrete, ok := runner.(*codexexec.Runner); ok && userInputBroker != nil {
		previousUserInput := concrete.UserInput
		concrete.UserInput = userInputBroker.userInputResponder(send)
		defer func() {
			concrete.UserInput = previousUserInput
		}()
	}
	inputs := interactiveSubmitInputs(request)
	prompt := strings.TrimSpace(request.Prompt)
	if len(inputs) > 0 && prompt != "" {
		inputs = append(inputs, turn.TurnUserInput{Type: "text", Text: prompt})
		prompt = ""
	}
	turnRoot := *root
	turnRoot.Shared = interactiveSharedOptionsFromState(turnRoot.Shared, state)
	execOpts := cli.ExecOptions{
		Prompt: strings.TrimSpace(prompt),
		Shared: turnRoot.Shared,
		Color:  "auto",
		JSON:   true,
	}
	if state != nil && strings.TrimSpace(state.ThreadID) != "" {
		execOpts.Subcommand = "resume"
		execOpts.Resume = cli.ExecResumeOptions{
			SessionID: state.ThreadID,
			Prompt:    strings.TrimSpace(prompt),
		}
		execOpts.Prompt = ""
	}
	streamWriter := newInteractiveStreamEventWriter(send)
	result, err := runner.RunContext(ctx, &codexexec.Request{
		Root:  turnRoot,
		Exec:  execOpts,
		Input: inputs,
	}, strings.NewReader(""), streamWriter, io.Discard)
	streamWriter.Flush()
	if err != nil {
		send(codextea.TurnCompletedMsg{Err: err})
		return
	}
	msg := codextea.TurnCompletedMsg{}
	if result != nil {
		msg.ThreadID = result.ThreadID
		msg.AssistantMessage = result.LastMessage
	}
	send(msg)
}

func interactiveSubmitInputs(request codextea.SubmitRequest) []turn.TurnUserInput {
	if len(request.Attachments) == 0 {
		return nil
	}
	inputs := make([]turn.TurnUserInput, 0, len(request.Attachments))
	for _, attachment := range request.Attachments {
		switch attachment.Kind {
		case bottompane.AttachmentImage:
			if path := strings.TrimSpace(attachment.Path); path != "" {
				inputs = append(inputs, turn.TurnUserInput{Type: "localImage", Path: path})
			}
		case bottompane.AttachmentRemoteImage:
			if url := strings.TrimSpace(attachment.URL); url != "" {
				inputs = append(inputs, turn.TurnUserInput{Type: "image", URL: url})
			}
		default:
			if path := strings.TrimSpace(attachment.Path); path != "" {
				inputs = append(inputs, turn.TurnUserInput{Type: "text", Text: "Attached file: " + path})
			}
		}
	}
	return inputs
}

type interactiveStreamEventWriter struct {
	mu     sync.Mutex
	buffer []byte
	send   func(bubbletea.Msg)
}

func newInteractiveStreamEventWriter(send func(bubbletea.Msg)) *interactiveStreamEventWriter {
	return &interactiveStreamEventWriter{send: send}
}

func (w *interactiveStreamEventWriter) Write(data []byte) (int, error) {
	if w == nil {
		return len(data), nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buffer = append(w.buffer, data...)
	for {
		index := bytes.IndexByte(w.buffer, '\n')
		if index < 0 {
			break
		}
		line := bytes.TrimSpace(w.buffer[:index])
		w.buffer = append([]byte(nil), w.buffer[index+1:]...)
		w.emitLine(line)
	}
	return len(data), nil
}

func (w *interactiveStreamEventWriter) Flush() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	line := bytes.TrimSpace(w.buffer)
	w.buffer = nil
	w.emitLine(line)
}

func (w *interactiveStreamEventWriter) emitLine(line []byte) {
	if len(line) == 0 || w.send == nil {
		return
	}
	var event protocol.ThreadEvent
	if err := json.Unmarshal(line, &event); err != nil {
		w.send(codextea.ThreadEventMsg{Event: protocol.ErrorEvent("failed to parse stream event: " + err.Error())})
		return
	}
	w.send(codextea.ThreadEventMsg{Event: event})
}

func interactiveSharedOptionsFromState(base cli.SharedOptions, state *codextui.State) cli.SharedOptions {
	if state == nil {
		return base
	}
	base.Model = state.Model
	base.ModelReasoningEffort = state.EffectiveReasoningEffort()
	base.ApprovalPolicy = state.ApprovalPolicy
	base.Sandbox = state.Sandbox
	base.Search = state.Search
	base.NoAltScreen = state.NoAltScreen
	return base
}

func interactiveUIState(root *cli.RootOptions) *codextui.State {
	if root == nil {
		return codextui.NewState(nil)
	}
	return codextui.NewState(&codextui.Options{
		Model:           root.Shared.Model,
		ReasoningEffort: root.Shared.ModelReasoningEffort,
		ApprovalPolicy:  root.Shared.ApprovalPolicy,
		Sandbox:         root.Shared.Sandbox,
		Search:          root.Shared.Search,
		NoAltScreen:     root.Shared.NoAltScreen,
	})
}

func interactiveFatalExit(stderr io.Writer, message string) error {
	if stderr == nil {
		stderr = io.Discard
	}
	fmt.Fprintf(stderr, "ERROR: %s\n", message)
	return silentExitCode(1)
}

const interactiveDumbTerminalNoTTYMessage = "TERM is set to \"dumb\". Refusing to start the interactive TUI because no terminal is available for a confirmation prompt (stdin/stderr is not a TTY). Run in a supported terminal or unset TERM."
const interactiveDumbTerminalRefusedMessage = "Refusing to start the interactive TUI because TERM is set to \"dumb\". Run in a supported terminal or unset TERM."

func guardInteractiveDumbTerminal(stdin io.Reader, stderr io.Writer, term string) error {
	if strings.TrimSpace(term) != "dumb" {
		return nil
	}
	if !isSessionTerminal(stdin) || !isSessionTerminal(stderr) {
		return errors.New(interactiveDumbTerminalNoTTYMessage)
	}
	if stderr == nil {
		stderr = io.Discard
	}
	fmt.Fprintln(stderr, "WARNING: TERM is set to \"dumb\". Codex's interactive TUI may not work in this terminal.")
	fmt.Fprintln(stderr, "Continue anyway? [y/N]: ")
	reader := bufio.NewReader(stdin)
	answer, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	answer = strings.TrimSpace(answer)
	if strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes") {
		return nil
	}
	return errors.New(interactiveDumbTerminalRefusedMessage)
}

func runInteractivePrompt(ctx context.Context, root *cli.RootOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	execOpts := cli.ExecOptions{
		Prompt: root.Prompt,
		Shared: root.Shared,
		Color:  "auto",
	}
	_, err := codexexec.NewRunner(auth.DefaultCodexHome()).RunContext(ctx, &codexexec.Request{
		Root: *root,
		Exec: execOpts,
	}, stdin, stdout, stderr)
	return err
}

func normalizeInteractivePrompt(prompt string) string {
	if prompt == "" {
		return ""
	}
	prompt = strings.ReplaceAll(prompt, "\r\n", "\n")
	return strings.ReplaceAll(prompt, "\r", "\n")
}

func resolveInteractiveRemoteEndpoint(root *cli.RootOptions) (*appserverdaemon.RemoteAppServerEndpoint, error) {
	if root == nil {
		return nil, nil
	}
	var endpoint *appserverdaemon.RemoteAppServerEndpoint
	if strings.TrimSpace(root.Remote) != "" {
		parsed, err := resolveInteractiveRemoteAddress(root.Remote)
		if err != nil {
			return nil, err
		}
		endpoint = parsed
	}
	if strings.TrimSpace(root.RemoteAuthEnv) == "" {
		return endpoint, nil
	}
	if endpoint == nil {
		return nil, errors.New("`--remote-auth-token-env` requires `--remote`.")
	}
	if endpoint.Kind != appserverdaemon.RemoteEndpointWebSocket || !appserverdaemon.WebSocketURLSupportsAuthToken(endpoint.WebSocketURL) {
		return nil, errors.New("`--remote-auth-token-env` requires a `wss://` or loopback `ws://` remote.")
	}
	authToken, err := readRemoteAuthTokenFromEnvVar(root.RemoteAuthEnv)
	if err != nil {
		return nil, err
	}
	endpoint.AuthToken = &authToken
	return endpoint, nil
}

func resolveInteractiveRemoteAddress(addr string) (*appserverdaemon.RemoteAppServerEndpoint, error) {
	if strings.HasPrefix(addr, "unix://") {
		socketPath := strings.TrimPrefix(addr, "unix://")
		if socketPath == "" {
			return appserverdaemon.NewUnixSocketEndpoint(appserver.AppServerControlSocketPath(auth.DefaultCodexHome())), nil
		}
		abs, err := absoluteInteractiveRemoteSocketPath(socketPath)
		if err != nil {
			return nil, err
		}
		return appserverdaemon.NewUnixSocketEndpoint(abs), nil
	}

	parsed, err := url.Parse(addr)
	if err != nil || !isValidInteractiveRemoteWebSocketURL(parsed) {
		return nil, invalidInteractiveRemoteAddress(addr)
	}
	return appserverdaemon.NewWebSocketEndpoint(normalizeInteractiveRemoteWebSocketURL(parsed), nil), nil
}

func absoluteInteractiveRemoteSocketPath(path string) (string, error) {
	clean := filepath.Clean(path)
	if filepath.IsAbs(clean) {
		return clean, nil
	}
	abs, err := filepath.Abs(clean)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func isValidInteractiveRemoteWebSocketURL(parsed *url.URL) bool {
	if parsed == nil {
		return false
	}
	if parsed.Scheme != "ws" && parsed.Scheme != "wss" {
		return false
	}
	if parsed.Hostname() == "" || parsed.Port() == "" {
		return false
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return false
	}
	return parsed.RawQuery == "" && parsed.Fragment == ""
}

func normalizeInteractiveRemoteWebSocketURL(parsed *url.URL) string {
	host := parsed.Hostname()
	port := parsed.Port()
	normalizedHost := net.JoinHostPort(host, port)
	if isDefaultWebSocketPort(parsed.Scheme, port) {
		normalizedHost = host
		if strings.Contains(host, ":") {
			normalizedHost = "[" + host + "]"
		}
	}
	return (&url.URL{
		Scheme: parsed.Scheme,
		Host:   normalizedHost,
		Path:   "/",
	}).String()
}

func isDefaultWebSocketPort(scheme string, port string) bool {
	return (scheme == "ws" && port == "80") || (scheme == "wss" && port == "443")
}

func invalidInteractiveRemoteAddress(addr string) error {
	return fmt.Errorf("invalid remote address `%s`; %s", addr, remoteAddressUsage)
}

func readRemoteAuthTokenFromEnvVar(envVarName string) (string, error) {
	return readRemoteAuthTokenFromEnvVarWith(envVarName, os.LookupEnv)
}

func readRemoteAuthTokenFromEnvVarWith(envVarName string, getVar func(string) (string, bool)) (string, error) {
	raw, ok := getVar(envVarName)
	if !ok {
		return "", fmt.Errorf("environment variable `%s` is not set", envVarName)
	}
	authToken := strings.TrimSpace(raw)
	if authToken == "" {
		return "", fmt.Errorf("environment variable `%s` is empty", envVarName)
	}
	return authToken, nil
}

func (s *interactiveSession) Run(ctx context.Context) error {
	if s == nil {
		return errors.New("interactive session is nil")
	}
	if s.Runner == nil {
		s.Runner = codexexec.NewRunner(auth.DefaultCodexHome())
	}
	if s.Reader == nil {
		return errors.New("interactive reader is nil")
	}
	if s.Stdout == nil {
		s.Stdout = io.Discard
	}
	if s.Stderr == nil {
		s.Stderr = io.Discard
	}
	if s.UI == nil {
		root := s.Root
		s.UI = interactiveUIState(&root)
	}
	if _, err := fmt.Fprint(s.Stdout, s.UI.RenderWelcome()); err != nil {
		return err
	}
	for {
		if _, err := fmt.Fprint(s.Stdout, s.UI.RenderPrompt()); err != nil {
			return err
		}
		if !s.Reader.Scan() {
			break
		}
		input := strings.TrimSpace(s.Reader.Text())
		if input == "" {
			continue
		}
		handled, exit, err := s.HandleCommand(input)
		if err != nil {
			return err
		}
		if exit {
			break
		}
		if handled {
			continue
		}
		if err := s.RunTurn(ctx, input); err != nil {
			return err
		}
	}
	if err := s.Reader.Err(); err != nil {
		return err
	}
	return nil
}

func (s *interactiveSession) HandleCommand(input string) (handled bool, exit bool, err error) {
	invocation, ok := codextui.ParseCommand(input)
	if !ok {
		return false, false, nil
	}
	if s.UI == nil {
		root := s.Root
		s.UI = interactiveUIState(&root)
	}
	switch invocation.Command {
	case codextui.CommandExit:
		return true, true, nil
	case codextui.CommandHelp:
		_, err = fmt.Fprint(s.Stdout, s.UI.RenderHelp())
	case codextui.CommandStatus:
		_, err = fmt.Fprintln(s.Stdout, s.UI.RenderStatusLine())
	case codextui.CommandNew:
		s.ThreadID = ""
		s.UI.ResetThread()
		_, err = fmt.Fprintln(s.Stdout, "Started a new local thread.")
	case codextui.CommandClear:
		s.UI.ClearMessages()
		_, err = fmt.Fprintln(s.Stdout, "Cleared visible transcript.")
	case codextui.CommandModel:
		err = s.handleModelCommand(invocation.Args)
	case codextui.CommandApproval:
		err = s.handleApprovalCommand(invocation.Args)
	case codextui.CommandSandbox:
		err = s.handleSandboxCommand(invocation.Args)
	default:
		_, err = fmt.Fprintf(s.Stdout, "Unknown command %s. Type /help for commands.\n", invocation.Name)
	}
	return true, false, err
}

func (s *interactiveSession) handleModelCommand(args string) error {
	if strings.TrimSpace(args) != "" {
		value := strings.TrimSpace(args)
		s.Root.Shared.Model = value
		s.UI.Model = value
	}
	_, err := fmt.Fprint(s.Stdout, s.UI.RenderSetting("Model", s.UI.Model))
	return err
}

func (s *interactiveSession) handleApprovalCommand(args string) error {
	if strings.TrimSpace(args) != "" {
		value := strings.TrimSpace(args)
		if !codextui.ValidApprovalPolicy(value) {
			_, err := fmt.Fprintln(s.Stdout, "Approval must be one of untrusted, on-request, never.")
			return err
		}
		s.Root.Shared.ApprovalPolicy = value
		s.UI.ApprovalPolicy = value
	}
	_, err := fmt.Fprint(s.Stdout, s.UI.RenderSetting("Approval", s.UI.ApprovalPolicy))
	return err
}

func (s *interactiveSession) handleSandboxCommand(args string) error {
	if strings.TrimSpace(args) != "" {
		value := strings.TrimSpace(args)
		s.Root.Shared.Sandbox = value
		s.UI.Sandbox = value
	}
	_, err := fmt.Fprint(s.Stdout, s.UI.RenderSetting("Sandbox", s.UI.Sandbox))
	return err
}

func (s *interactiveSession) RunTurn(ctx context.Context, prompt string) error {
	if s.UI == nil {
		root := s.Root
		s.UI = interactiveUIState(&root)
	}
	s.UI.AddMessage(codextui.RoleUser, prompt)
	s.UI.SetStatus("running")
	if _, err := fmt.Fprintln(s.Stdout, s.UI.RenderStatusLine()); err != nil {
		return err
	}
	execOpts := cli.ExecOptions{
		Prompt: prompt,
		Shared: s.Root.Shared,
		Color:  "auto",
	}
	if s.ThreadID != "" {
		execOpts.Subcommand = "resume"
		execOpts.Resume = cli.ExecResumeOptions{
			SessionID: s.ThreadID,
			Prompt:    prompt,
		}
		execOpts.Prompt = ""
	}
	result, err := s.Runner.RunContext(ctx, &codexexec.Request{
		Root: s.Root,
		Exec: execOpts,
	}, strings.NewReader(""), s.Stdout, s.Stderr)
	if err != nil {
		s.UI.SetStatus("error")
		return err
	}
	if result != nil && result.ThreadID != "" {
		s.ThreadID = result.ThreadID
		s.UI.SetThreadID(result.ThreadID)
	}
	if result != nil && result.LastMessage != "" {
		s.UI.AddMessage(codextui.RoleAssistant, result.LastMessage)
	}
	s.UI.SetStatus("idle")
	return nil
}

func isInteractiveExitCommand(input string) bool {
	invocation, ok := codextui.ParseCommand(input)
	return ok && invocation.Command == codextui.CommandExit
}
