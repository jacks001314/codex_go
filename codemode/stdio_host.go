package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"

	"codex_go/tool"
)

type stdioHostServer struct {
	ctx    context.Context
	reader *FramedReader
	writer *FramedWriter

	writeMu      sync.Mutex
	mu           sync.Mutex
	sessions     map[SessionID]*stdioHostSession
	seen         map[SessionID]struct{}
	requests     map[RequestID]context.CancelFunc
	pending      map[DelegateRequestID]chan delegateHostResult
	nextDelegate atomic.Int64
	wg           sync.WaitGroup
}

type delegateHostResult struct {
	response DelegateResponse
	err      error
}

func RunStdioHost(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
	if ctx == nil {
		ctx = context.Background()
	}
	host := &stdioHostServer{
		ctx: ctx, reader: NewFramedReader(stdin), writer: NewFramedWriter(stdout),
		sessions: map[SessionID]*stdioHostSession{}, seen: map[SessionID]struct{}{},
		requests: map[RequestID]context.CancelFunc{}, pending: map[DelegateRequestID]chan delegateHostResult{},
	}
	if err := host.negotiate(); err != nil {
		return err
	}
	for {
		var message ClientToHost
		ok, err := host.reader.Read(&message)
		if err != nil {
			host.shutdown()
			return fmt.Errorf("failed to read code-mode client message: %w", err)
		}
		if !ok {
			host.shutdown()
			return nil
		}
		switch message.Type {
		case "operation/request":
			host.startRequest(message.ID, message.Request)
		case "operation/cancel":
			host.cancelRequest(message.ID)
		case "delegate/response":
			host.completeDelegate(message.DelegateID, message.DelegateResponse)
		case "connection/hello":
			host.shutdown()
			return errors.New("received a second code-mode client hello")
		}
	}
}

func (h *stdioHostServer) negotiate() error {
	var first ClientToHost
	ok, err := h.reader.Read(&first)
	if err != nil {
		return fmt.Errorf("failed to read code-mode client hello: %w", err)
	}
	if !ok {
		return nil
	}
	if first.Type != "connection/hello" || first.Hello == nil {
		_ = h.write(HandshakeRejected(InvalidHello("first message must be connection/hello")))
		return errors.New("first message must be connection/hello")
	}
	versions, _ := NewSupportedProtocolVersions(ProtocolV1)
	if !(&first.Hello.SupportedVersions).Contains(ProtocolV1) {
		return h.write(HandshakeRejected(NoCompatibleVersion(versions)))
	}
	hostCapabilities := CapabilitySet{}
	for _, capability := range first.Hello.RequiredCapabilities {
		if !(&hostCapabilities).Contains(capability) {
			return h.write(HandshakeRejected(MissingRequiredCapability(capability)))
		}
	}
	return h.write(HostHelloMessage(HostHello{SelectedVersion: ProtocolV1, Capabilities: hostCapabilities}))
}

func (h *stdioHostServer) startRequest(id RequestID, request *HostRequest) {
	requestCtx, cancel := context.WithCancel(h.ctx)
	h.mu.Lock()
	if _, exists := h.requests[id]; exists {
		h.mu.Unlock()
		cancel()
		_ = h.write(HostOperationResponse(id, ResultErr[HostResponse](fmt.Sprintf("duplicate code-mode request ID %d", id))))
		return
	}
	h.requests[id] = cancel
	h.mu.Unlock()
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		defer func() {
			h.mu.Lock()
			delete(h.requests, id)
			h.mu.Unlock()
			cancel()
		}()
		h.handleRequest(requestCtx, id, request)
	}()
}

func (h *stdioHostServer) handleRequest(ctx context.Context, id RequestID, request *HostRequest) {
	if request == nil {
		_ = h.write(HostOperationResponse(id, ResultErr[HostResponse]("host request is nil")))
		return
	}
	switch request.Method {
	case "session/open":
		h.mu.Lock()
		_, active := h.sessions[request.SessionID]
		_, reused := h.seen[request.SessionID]
		if !active && !reused {
			h.sessions[request.SessionID] = newStdioHostSession(h, request.SessionID)
			h.seen[request.SessionID] = struct{}{}
		}
		h.mu.Unlock()
		if active || reused {
			_ = h.write(HostOperationResponse(id, ResultErr[HostResponse](fmt.Sprintf("code-mode session ID `%s` was reused", request.SessionID))))
			return
		}
		_ = h.write(HostOperationResponse(id, ResultOK(SessionReady(request.SessionID))))
	case "session/execute":
		session := h.session(request.SessionID)
		if session == nil {
			_ = h.write(HostOperationResponse(id, ResultErr[HostResponse](fmt.Sprintf("unknown code-mode session %s", request.SessionID))))
			return
		}
		if request.Request == nil {
			_ = h.write(HostOperationResponse(id, ResultErr[HostResponse]("session/execute request is required")))
			return
		}
		if err := request.Request.Validate(); err != nil {
			_ = h.write(HostOperationResponse(id, ResultErr[HostResponse]("invalid code-mode execute request: "+err.Error())))
			return
		}
		cellID := session.nextCellID()
		if err := h.write(HostOperationResponse(id, ResultOK(ExecutionStarted(cellID)))); err != nil {
			return
		}
		response := session.execute(ctx, cellID, request.Request)
		_ = h.write(InitialResponse(id, ResultOK(response)))
		if response.Variant != "Yielded" {
			_ = h.write(CellClosed(request.SessionID, cellID))
		}
	case "session/wait":
		session := h.session(request.SessionID)
		if session == nil {
			_ = h.write(HostOperationResponse(id, ResultErr[HostResponse](fmt.Sprintf("unknown code-mode session %s", request.SessionID))))
			return
		}
		outcome := session.wait(ctx, request.Wait, false)
		_ = h.write(HostOperationResponse(id, ResultOK(WaitCompleted(outcome))))
		if outcome.Response.Variant != "Yielded" {
			_ = h.write(CellClosed(request.SessionID, outcome.Response.CellID))
		}
	case "session/terminate":
		session := h.session(request.SessionID)
		if session == nil {
			_ = h.write(HostOperationResponse(id, ResultErr[HostResponse](fmt.Sprintf("unknown code-mode session %s", request.SessionID))))
			return
		}
		outcome := session.wait(ctx, &WaitRequest{CellID: request.CellID}, true)
		_ = h.write(HostOperationResponse(id, ResultOK(WaitCompleted(outcome))))
		_ = h.write(CellClosed(request.SessionID, request.CellID))
	case "session/shutdown":
		h.mu.Lock()
		session := h.sessions[request.SessionID]
		delete(h.sessions, request.SessionID)
		h.mu.Unlock()
		if session == nil {
			_ = h.write(HostOperationResponse(id, ResultErr[HostResponse](fmt.Sprintf("unknown code-mode session %s", request.SessionID))))
			return
		}
		session.shutdown(ctx)
		_ = h.write(HostOperationResponse(id, ResultOK(SessionClosed(request.SessionID))))
	default:
		_ = h.write(HostOperationResponse(id, ResultErr[HostResponse](fmt.Sprintf("unknown code mode host method %s", request.Method))))
	}
}

func (h *stdioHostServer) session(id SessionID) *stdioHostSession {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sessions[id]
}

func (h *stdioHostServer) cancelRequest(id RequestID) {
	h.mu.Lock()
	cancel := h.requests[id]
	h.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (h *stdioHostServer) requestDelegate(ctx context.Context, sessionID SessionID, request DelegateRequest) (DelegateResponse, error) {
	id := DelegateRequestID(h.nextDelegate.Add(1))
	response := make(chan delegateHostResult, 1)
	h.mu.Lock()
	if len(h.pending) >= MaxPendingDelegateCalls {
		h.mu.Unlock()
		return DelegateResponse{}, fmt.Errorf("code-mode host exceeded the limit of %d pending delegate calls", MaxPendingDelegateCalls)
	}
	h.pending[id] = response
	h.mu.Unlock()
	if err := h.write(DelegateRequestMessage(id, sessionID, request)); err != nil {
		h.mu.Lock()
		delete(h.pending, id)
		h.mu.Unlock()
		return DelegateResponse{}, err
	}
	select {
	case result := <-response:
		return result.response, result.err
	case <-ctx.Done():
		h.mu.Lock()
		delete(h.pending, id)
		h.mu.Unlock()
		_ = h.write(CancelDelegateRequest(id))
		return DelegateResponse{}, ctx.Err()
	}
}

func (h *stdioHostServer) completeDelegate(id DelegateRequestID, result *WireResult[DelegateResponse]) {
	h.mu.Lock()
	pending := h.pending[id]
	delete(h.pending, id)
	h.mu.Unlock()
	if pending == nil {
		return
	}
	response, err := result.IntoResult()
	pending <- delegateHostResult{response: response, err: err}
}

func (h *stdioHostServer) write(message any) error {
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	return h.writer.Write(message)
}

func (h *stdioHostServer) shutdown() {
	h.mu.Lock()
	requests := make([]context.CancelFunc, 0, len(h.requests))
	for _, cancel := range h.requests {
		requests = append(requests, cancel)
	}
	sessions := make([]*stdioHostSession, 0, len(h.sessions))
	for _, session := range h.sessions {
		sessions = append(sessions, session)
	}
	h.sessions = map[SessionID]*stdioHostSession{}
	h.mu.Unlock()
	for _, cancel := range requests {
		cancel()
	}
	for _, session := range sessions {
		session.shutdown(context.Background())
	}
	h.wg.Wait()
}

type stdioHostSession struct {
	host         *stdioHostServer
	id           SessionID
	registry     *tool.Registry
	exec         tool.Executor
	waitExecutor tool.Executor
	definitions  map[string]struct{}
	nextCell     atomic.Uint64
}

func newStdioHostSession(host *stdioHostServer, id SessionID) *stdioHostSession {
	registry := tool.NewRegistry()
	execExecutor, waitExecutor := tool.NewCodeModeExecutors(registry)
	return &stdioHostSession{host: host, id: id, registry: registry, exec: execExecutor, waitExecutor: waitExecutor, definitions: map[string]struct{}{}}
}

func (s *stdioHostSession) nextCellID() CellID {
	return NewCellID(fmt.Sprintf("cell-%d", s.nextCell.Add(1)))
}

func (s *stdioHostSession) execute(ctx context.Context, cellID CellID, request *ExecuteRequest) RuntimeResponse {
	allowed, err := s.registerDefinitions(request.EnabledTools)
	if err != nil {
		message := err.Error()
		return Result(cellID, nil, &message)
	}
	source := hostSourceWithOptions(request.Source, request.YieldTimeMS, request.MaxOutputTokens)
	invocation := &tool.Invocation{
		CallID:  request.ToolCallID,
		Payload: tool.Payload{Kind: tool.PayloadCustom, Input: source},
		Context: map[string]any{
			tool.CodeModeCellIDContextKey:       cellID.String(),
			tool.CodeModeEnabledToolsContextKey: allowed,
		},
	}
	invocation.Context["code_mode_notify"] = tool.CodeModeNotifyFunc(func(callID, text string) {
		_, _ = s.host.requestDelegate(ctx, s.id, NotifyRequest(callID, cellID, text))
	})
	output, execErr := s.exec.Execute(ctx, invocation)
	return hostRuntimeResponse(cellID, output, execErr)
}

func (s *stdioHostSession) wait(ctx context.Context, request *WaitRequest, terminate bool) WaitOutcome {
	if request == nil || strings.TrimSpace(request.CellID.String()) == "" {
		cellID := CellID("")
		if request != nil {
			cellID = request.CellID
		}
		message := "cell_id is required"
		return MissingCell(Result(cellID, nil, &message))
	}
	arguments, _ := json.Marshal(map[string]any{
		"cell_id": request.CellID.String(), "yield_time_ms": request.YieldTimeMS, "terminate": terminate,
	})
	output, err := s.waitExecutor.Execute(ctx, &tool.Invocation{CallID: "wait-" + request.CellID.String(), Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: string(arguments)}})
	if err != nil && strings.Contains(err.Error(), "not found") {
		message := err.Error()
		return MissingCell(Result(request.CellID, nil, &message))
	}
	response := hostRuntimeResponse(request.CellID, output, err)
	return LiveCell(response)
}

func (s *stdioHostSession) shutdown(ctx context.Context) {
	for value := uint64(1); value <= s.nextCell.Load(); value++ {
		cellID := NewCellID(fmt.Sprintf("cell-%d", value))
		_ = s.wait(ctx, &WaitRequest{CellID: cellID}, true)
	}
}

func (s *stdioHostSession) registerDefinitions(definitions []ProtocolToolDefinition) (map[string]struct{}, error) {
	allowed := make(map[string]struct{}, len(definitions))
	for _, definition := range definitions {
		name := tool.ToolName{Name: definition.ToolName.Name}
		if definition.ToolName.Namespace != nil {
			name.Namespace = *definition.ToolName.Namespace
		}
		allowed[name.Key()] = struct{}{}
		if _, exists := s.definitions[name.Key()]; exists {
			continue
		}
		spec, err := hostToolSpec(name, definition)
		if err != nil {
			return nil, err
		}
		executor := &stdioHostNestedExecutor{host: s.host, sessionID: s.id, spec: spec, kind: definition.Kind}
		if err := s.registry.Register(executor); err != nil {
			return nil, err
		}
		s.definitions[name.Key()] = struct{}{}
	}
	return allowed, nil
}

type stdioHostNestedExecutor struct {
	host      *stdioHostServer
	sessionID SessionID
	spec      tool.Spec
	kind      ProtocolToolKind
}

func (e *stdioHostNestedExecutor) Spec() tool.Spec { return e.spec }

func (e *stdioHostNestedExecutor) Execute(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
	cellID := ""
	if invocation != nil && invocation.Context != nil {
		cellID, _ = invocation.Context[tool.CodeModeCellIDContextKey].(string)
	}
	input := json.RawMessage(invocation.Payload.Arguments)
	if e.kind == ProtocolToolKindFreeform {
		input, _ = json.Marshal(invocation.Payload.Input)
	}
	response, err := e.host.requestDelegate(ctx, e.sessionID, InvokeToolRequest(NestedToolCall{
		CellID: NewCellID(cellID), RuntimeToolCallID: invocation.CallID,
		ToolName:         ToolName{Name: invocation.ToolName.Name, Namespace: stringPointer(invocation.ToolName.Namespace)},
		ProtocolToolKind: e.kind, Input: input,
	}))
	if err != nil {
		return nil, err
	}
	if response.Type != "tool/result" {
		return nil, errors.New("code-mode client returned an invalid tool result")
	}
	var result map[string]any
	if err := json.Unmarshal(response.Result, &result); err != nil {
		return nil, err
	}
	success, _ := result["success"].(bool)
	body := fmt.Sprint(result["output"])
	if rawBody, ok := result["body"].(string); ok && strings.TrimSpace(rawBody) != "" {
		body = rawBody
	}
	errorText, _ := result["error"].(string)
	return &tool.Output{CallID: invocation.CallID, ToolName: invocation.ToolName, Success: success, Body: body, Error: errorText, Data: result}, nil
}

func hostToolSpec(name tool.ToolName, definition ProtocolToolDefinition) (tool.Spec, error) {
	spec := tool.Spec{Name: name, Description: definition.Description}
	if definition.Kind == ProtocolToolKindFreeform {
		var grammar struct {
			Syntax     string `json:"syntax"`
			Definition string `json:"definition"`
		}
		if err := json.Unmarshal(definition.InputSchema, &grammar); err != nil {
			return tool.Spec{}, err
		}
		spec.Freeform = &tool.FreeformSpec{Syntax: grammar.Syntax, Definition: grammar.Definition}
	} else if len(definition.InputSchema) > 0 {
		if err := json.Unmarshal(definition.InputSchema, &spec.InputSchema); err != nil {
			return tool.Spec{}, err
		}
	}
	if len(definition.OutputSchema) > 0 && string(definition.OutputSchema) != "null" {
		if err := json.Unmarshal(definition.OutputSchema, &spec.OutputSchema); err != nil {
			return tool.Spec{}, err
		}
	}
	return spec, nil
}

func hostSourceWithOptions(source string, yieldTimeMS *uint64, maxOutputTokens *int) string {
	if yieldTimeMS == nil && maxOutputTokens == nil {
		return source
	}
	options := map[string]any{}
	if yieldTimeMS != nil {
		options["yield_time_ms"] = *yieldTimeMS
	}
	if maxOutputTokens != nil {
		options["max_output_tokens"] = *maxOutputTokens
	}
	encoded, _ := json.Marshal(options)
	return "// @exec: " + string(encoded) + "\n" + source
}

func hostRuntimeResponse(cellID CellID, output *tool.Output, err error) RuntimeResponse {
	items := hostContentItems(output)
	if err != nil {
		message := err.Error()
		return Result(cellID, items, &message)
	}
	if output != nil && output.Data != nil {
		if running, _ := output.Data["running"].(bool); running {
			return Yielded(cellID, items)
		}
		if terminated, _ := output.Data["terminated"].(bool); terminated {
			return Terminated(cellID, items)
		}
	}
	return Result(cellID, items, nil)
}

func hostContentItems(output *tool.Output) []ContentItem {
	if output == nil {
		return nil
	}
	if output.Data != nil {
		if raw, ok := output.Data["content_items"]; ok {
			encoded, _ := json.Marshal(raw)
			var items []ContentItem
			if json.Unmarshal(encoded, &items) == nil {
				return items
			}
		}
	}
	if output.Body == "" {
		return nil
	}
	return []ContentItem{InputText(output.Body)}
}

func stringPointer(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
