package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	WaitForEnvironmentToolName                    = "wait_for_environment"
	DefaultWaitForEnvironmentToolDescription      = "Wait for a selected execution environment marked as `starting` to become available. Use this when the current task needs that environment's files, commands, or installed capabilities. Do not wait if the task can be completed using tools already available, such as connectors. Waiting may take several minutes and blocks other tool calls. If startup fails, continue without that environment."
	DefaultWaitForEnvironmentIDDescription        = "The exact environment ID marked as `starting` in `<environment_context>`."
	maxWaitForEnvironmentCombinedDescriptionBytes = 1024
	maxWaitForEnvironmentSerializedToolSpecBytes  = 1000
	defaultWaitForEnvironmentPollInterval         = time.Second
	// Rust #44277: keep the failure reason small while preserving a UTF-8
	// boundary.
	maxWaitForEnvironmentFailureBytes = 256
)

type WaitForEnvironmentToolConfig struct {
	ToolDescription          string
	EnvironmentIDDescription string
}

type EnvironmentStatus string

const (
	EnvironmentStatusReady        EnvironmentStatus = "ready"
	EnvironmentStatusPending      EnvironmentStatus = "pending"
	EnvironmentStatusDisconnected EnvironmentStatus = "disconnected"
	EnvironmentStatusUnknown      EnvironmentStatus = "unknown"
)

type EnvironmentWaiter interface {
	Status(context.Context, string) (EnvironmentStatus, string, error)
}

type WaitForEnvironmentHandler struct {
	waiter                 EnvironmentWaiter
	selected               map[string]struct{}
	toolDescription        string
	environmentDescription string
	pollInterval           time.Duration
}

func NewWaitForEnvironmentHandler(waiter EnvironmentWaiter, selectedEnvironmentIDs []string, config *WaitForEnvironmentToolConfig) *WaitForEnvironmentHandler {
	handler := &WaitForEnvironmentHandler{
		waiter:                 waiter,
		selected:               map[string]struct{}{},
		toolDescription:        DefaultWaitForEnvironmentToolDescription,
		environmentDescription: DefaultWaitForEnvironmentIDDescription,
		pollInterval:           defaultWaitForEnvironmentPollInterval,
	}
	for _, environmentID := range selectedEnvironmentIDs {
		if environmentID = strings.TrimSpace(environmentID); environmentID != "" {
			handler.selected[environmentID] = struct{}{}
		}
	}
	if config != nil && len(config.ToolDescription)+len(config.EnvironmentIDDescription) <= maxWaitForEnvironmentCombinedDescriptionBytes {
		handler.toolDescription = config.ToolDescription
		handler.environmentDescription = config.EnvironmentIDDescription
		if encoded, err := json.Marshal(handler.Spec()); err != nil || len(encoded) > maxWaitForEnvironmentSerializedToolSpecBytes {
			handler.toolDescription = DefaultWaitForEnvironmentToolDescription
			handler.environmentDescription = DefaultWaitForEnvironmentIDDescription
		}
	}
	return handler
}

func (h *WaitForEnvironmentHandler) Spec() Spec {
	return Spec{
		Name:        PlainName(WaitForEnvironmentToolName),
		Description: h.toolDescription,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"environment_id": map[string]any{
					"type":        "string",
					"description": h.environmentDescription,
				},
			},
			"required":             []string{"environment_id"},
			"additionalProperties": false,
		},
		Parallel: false,
	}
}

// environmentFailureMessage mirrors Rust's environment_failure (#44277): report
// the startup failure reason (bounded to 256 bytes at a UTF-8 boundary) so the
// model can explain problems such as an empty repository.
func environmentFailureMessage(environmentID, reason string) string {
	return fmt.Sprintf("Environment `%s` failed to start: %s", environmentID, truncateUTF8Bytes(reason, maxWaitForEnvironmentFailureBytes))
}

func truncateUTF8Bytes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
}

func (h *WaitForEnvironmentHandler) Execute(ctx context.Context, invocation *Invocation) (*Output, error) {
	if invocation == nil {
		return nil, fmt.Errorf("%w: invocation is nil", ErrToolInvalidCall)
	}
	var args struct {
		EnvironmentID string `json:"environment_id"`
	}
	if err := invocation.DecodeArguments(&args); err != nil {
		return nil, err
	}
	environmentID := strings.TrimSpace(args.EnvironmentID)
	if _, ok := h.selected[environmentID]; !ok || environmentID == "" {
		return nil, RespondToModel(fmt.Sprintf("environment `%s` is neither ready nor starting", environmentID))
	}
	if h.waiter == nil {
		return nil, RespondToModel(fmt.Sprintf("Environment `%s` failed to start and is unavailable. Continue without it.", environmentID))
	}
	pollInterval := h.pollInterval
	if pollInterval <= 0 {
		pollInterval = defaultWaitForEnvironmentPollInterval
	}
	for {
		status, message, err := h.waiter.Status(ctx, environmentID)
		if err != nil {
			return nil, RespondToModel(environmentFailureMessage(environmentID, err.Error()))
		}
		switch status {
		case EnvironmentStatusReady:
			data := map[string]any{"environment_id": environmentID, "status": "ready"}
			body, err := json.Marshal(data)
			if err != nil {
				return nil, err
			}
			return &Output{Success: true, Body: string(body), Data: data}, nil
		case EnvironmentStatusPending:
		case EnvironmentStatusUnknown:
			return nil, RespondToModel(fmt.Sprintf("environment `%s` is neither ready nor starting", environmentID))
		case EnvironmentStatusDisconnected:
			return nil, RespondToModel(environmentFailureMessage(environmentID, message))
		default:
			return nil, RespondToModel(environmentFailureMessage(environmentID, message))
		}
		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
