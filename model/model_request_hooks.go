package model

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
)

// SamplingKind mirrors Rust's SamplingKind: the request kinds whose
// PreModelRequest/PostModelRequest hook pairs stay distinct (Rust #61562).
type SamplingKind string

const (
	SamplingKindSampling             SamplingKind = "sampling"
	SamplingKindForegroundCompaction SamplingKind = "foreground_compaction"
	SamplingKindBackgroundCompaction SamplingKind = "background_compaction"
)

func (kind SamplingKind) normalized() SamplingKind {
	switch kind {
	case SamplingKindForegroundCompaction, SamplingKindBackgroundCompaction:
		return kind
	default:
		return SamplingKindSampling
	}
}

// ModelRequestHookEvent is one PreModelRequest/PostModelRequest milestone for
// a single provider attempt (Rust ModelRequestHookInput, #61562/#61587).
type ModelRequestHookEvent struct {
	Phase         string
	Status        string
	Kind          SamplingKind
	RequestID     string
	AttemptNumber int
	ResponseID    string
	Model         string
	TurnID        string
	Error         string
	Usage         *AgentUsage
}

// ModelRequestHook observes model request attempts. The Responses runner pairs
// one Pre and one Post event per attempt, keeping RequestID stable across
// retries and incrementing AttemptNumber per opened response stream.
type ModelRequestHook func(ctx context.Context, event ModelRequestHookEvent)

// modelRequestHooks owns one generation's hook lifecycle across retries.
type modelRequestHooks struct {
	hook      ModelRequestHook
	kind      SamplingKind
	requestID string
	attempts  int
	// active marks an outer attempt that owns the generation so a transport
	// fallback (WebSocket to HTTP) does not start a second hook pair. Rust
	// treats transport recovery inside one open() as the same attempt.
	active bool
}

func newModelRequestHooks(request *AgentRequest) *modelRequestHooks {
	if request == nil || request.OnModelRequest == nil {
		return nil
	}
	requestID := strings.TrimSpace(request.RequestID)
	if requestID == "" {
		requestID = uuid.NewString()
	}
	return &modelRequestHooks{
		hook:      request.OnModelRequest,
		kind:      request.SamplingKind.normalized(),
		requestID: requestID,
	}
}

// withModelRequestHookState clones the request once so the attempts of one
// Run share a request ID without mutating the caller's request.
func (request *AgentRequest) withModelRequestHookState() *AgentRequest {
	if request == nil {
		return nil
	}
	hooks := newModelRequestHooks(request)
	if hooks == nil {
		return request
	}
	clone := *request
	clone.RequestID = hooks.requestID
	clone.modelRequestHooks = hooks
	return &clone
}

func (request *AgentRequest) beginModelRequestAttempt(ctx context.Context, model string) *modelRequestAttempt {
	if request == nil || request.modelRequestHooks == nil {
		return nil
	}
	hooks := request.modelRequestHooks
	if hooks.active {
		return nil
	}
	hooks.attempts++
	hooks.active = true
	attempt := &modelRequestAttempt{
		ctx: ctx, hooks: hooks, number: hooks.attempts,
		model: strings.TrimSpace(model), turnID: strings.TrimSpace(request.TurnID),
	}
	hooks.hook(ctx, ModelRequestHookEvent{
		Phase:         "pre",
		Kind:          hooks.kind,
		RequestID:     hooks.requestID,
		AttemptNumber: attempt.number,
		Model:         attempt.model,
		TurnID:        attempt.turnID,
	})
	return attempt
}

// modelRequestAttempt is one opened response stream's hook lifecycle.
type modelRequestAttempt struct {
	ctx      context.Context
	hooks    *modelRequestHooks
	number   int
	model    string
	turnID   string
	finished bool
}

// finish reports the attempt's terminal outcome. The synchronous hook runs
// before the caller can account usage or release the terminal response event
// (Rust #61562). A nil attempt is a no-op so callers can stay branch-free.
func (attempt *modelRequestAttempt) finish(ctx context.Context, response *AgentResponse, err error) {
	if attempt == nil || attempt.finished {
		return
	}
	attempt.finished = true
	attempt.hooks.active = false
	event := ModelRequestHookEvent{
		Phase:         "post",
		Status:        "completed",
		Kind:          attempt.hooks.kind,
		RequestID:     attempt.hooks.requestID,
		AttemptNumber: attempt.number,
		Model:         attempt.model,
		TurnID:        attempt.turnID,
	}
	if response != nil {
		event.ResponseID = strings.TrimSpace(response.ResponseID)
		usage := response.Usage
		event.Usage = &usage
	}
	if err != nil {
		event.Error = err.Error()
		event.Status = "failed"
		if isModelRequestCancellation(ctx, err) {
			event.Status = "cancelled"
			event.Error = ""
		}
	} else if response != nil && (response.Preempted || response.Canceled) {
		event.Status = "cancelled"
	}
	hookCtx := ctx
	if hookCtx == nil {
		hookCtx = attempt.ctx
	}
	if attempt.hooks.hook != nil {
		attempt.hooks.hook(hookCtx, event)
	}
}

func isModelRequestCancellation(ctx context.Context, err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if errors.Is(err, errResponsesSamplingPreempted) {
		return true
	}
	return ctx != nil && ctx.Err() != nil
}

// heldTerminalResponsesStreamHandler defers response.completed until the
// attempt's post hooks finished (Rust #61562 response_delivery).
type heldTerminalResponsesStreamHandler struct {
	target ResponsesStreamHandler
	held   []*ResponsesStreamEvent
}

func newHeldTerminalResponsesStreamHandler(target ResponsesStreamHandler) *heldTerminalResponsesStreamHandler {
	if target == nil {
		return nil
	}
	return &heldTerminalResponsesStreamHandler{target: target}
}

func (handler *heldTerminalResponsesStreamHandler) handle(event *ResponsesStreamEvent) {
	if handler == nil {
		return
	}
	if event != nil && event.Kind == ResponsesStreamEventCompleted {
		handler.held = append(handler.held, event)
		return
	}
	handler.target(event)
}

func (handler *heldTerminalResponsesStreamHandler) flush() {
	if handler == nil || len(handler.held) == 0 {
		return
	}
	held := handler.held
	handler.held = nil
	for _, event := range held {
		handler.target(event)
	}
}
