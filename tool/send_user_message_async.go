package tool

import (
	"context"
	"fmt"
	"strings"
)

// DefaultSendUserMessageAsyncToolName is the tool key the app-server runtime
// uses to recognize async user message completions (#39319).
const DefaultSendUserMessageAsyncToolName = "send_user_message_async"

// defaultSendUserMessageAsyncDescription is the built-in tool description used
// when the model catalog does not supply a model-owned description (#41461).
const defaultSendUserMessageAsyncDescription = "Send a concise message that needs the user's attention during ongoing work. The tool returns immediately without ending the turn or waiting for a reply; any reply arrives asynchronously as a new user message.\nOnly use this tool to ask for missing information, preferences, constraints, clarification, or approval. The message should be concise, easy to read and understand, and at the right level of abstraction that is appropriate for the user and task at hand."

// SendUserMessageAsyncHandler mirrors Rust
// core/src/tools/handlers/send_user_message_async.rs (#39319/#39601): the
// model can send a concise, user-visible update or blocking question without
// ending the turn. The handler is DirectModelOnly so it stays out of code
// mode; the runtime emits the supplied text as an asynchronous agent message
// and the tool returns immediately.
type SendUserMessageAsyncHandler struct {
	// EmitAsyncMessage, when set, lets the app-server runtime emit the
	// message item. The tool surface stays testable without the runtime.
	EmitAsyncMessage func(message string)
	// Description overrides the built-in tool description from the model
	// catalog (#41461). A non-nil value (including an empty string) replaces
	// the built-in description; nil falls back to it.
	Description *string
}

type sendUserMessageAsyncArgs struct {
	Message string `json:"message"`
}

func (h *SendUserMessageAsyncHandler) Spec() Spec {
	return Spec{
		Name:        PlainName(DefaultSendUserMessageAsyncToolName),
		Description: sendUserMessageAsyncDescription(h.Description),
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"message": map[string]any{
					"type":        "string",
					"description": "The concise question to send to the user.",
				},
			},
			"required":             []string{"message"},
			"additionalProperties": false,
		},
		Exposure: ExposureDirectModelOnly,
	}
}

func sendUserMessageAsyncDescription(override *string) string {
	if override != nil {
		return *override
	}
	return defaultSendUserMessageAsyncDescription
}

func (h *SendUserMessageAsyncHandler) Execute(ctx context.Context, invocation *Invocation) (*Output, error) {
	_ = ctx
	if invocation == nil {
		return nil, fmt.Errorf("send_user_message_async handler received unsupported payload")
	}
	var args sendUserMessageAsyncArgs
	if err := invocation.DecodeArguments(&args); err != nil {
		return nil, err
	}
	message := strings.TrimSpace(args.Message)
	if message == "" {
		return nil, RespondToModel("message must not be empty")
	}
	if h.EmitAsyncMessage != nil {
		h.EmitAsyncMessage(message)
	}
	return &Output{
		Success: true,
		Body:    `{"accepted":true}`,
		Data: map[string]any{
			"accepted": true,
			"async_message": map[string]any{
				"message":  message,
				"delivery": "async",
			},
		},
	}, nil
}
