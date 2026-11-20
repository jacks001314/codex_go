package agentboard

import (
	"context"
	"encoding/json"

	"codex_go/agent"
	"codex_go/tool"
)

const (
	guardianSubscribeDescription   = "Subscribe yourself to new evidence in this channel. Search or read its earlier posts separately."
	guardianUnsubscribeDescription = "Stop receiving new evidence from this channel. Evidence already admitted to the reviewer conversation is retained."
)

// guardianNilMessageID is the probe value Rust budgets with: a live subscription
// only learns its last post ID after the write, so the response is sized with a
// nil UUID.
var guardianNilMessageIDValue = "00000000-0000-0000-0000-000000000000"

// NewGuardianSubscriptionTools returns the two caller-scoped channel
// subscription tools (Rust guardian-v3 subscription_tools). They change only the
// acting reviewer's subscriptions: posting, other agents' subscriptions and
// channel management stay outside the reviewer surface.
func NewGuardianSubscriptionTools(board AgentMessageBoard, caller string, callerPath agent.AgentPath, namespace string, namespaceDescription string) []tool.Executor {
	if board == nil {
		return nil
	}
	return []tool.Executor{
		newGuardianSubscriptionTool(board, caller, callerPath, Subscribe, namespace, namespaceDescription),
		newGuardianSubscriptionTool(board, caller, callerPath, Unsubscribe, namespace, namespaceDescription),
	}
}

type guardianSubscriptionTool struct {
	board      AgentMessageBoard
	caller     string
	callerPath agent.AgentPath
	change     SubscriptionChange
	spec       tool.Spec
}

func newGuardianSubscriptionTool(board AgentMessageBoard, caller string, callerPath agent.AgentPath, change SubscriptionChange, namespace string, namespaceDescription string) *guardianSubscriptionTool {
	description := guardianSubscribeDescription
	if change == Unsubscribe {
		description = guardianUnsubscribeDescription
	}
	return &guardianSubscriptionTool{
		board:      board,
		caller:     caller,
		callerPath: callerPath,
		change:     change,
		spec: tool.Spec{
			Name:        tool.NamespacedName(namespace, string(change)),
			Description: description,
			InputSchema: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{"channel_name": map[string]any{"type": "string"}},
				"required":             []string{"channel_name"},
				"additionalProperties": false,
			},
			NamespaceDescription: namespaceDescription,
		},
	}
}

func (t *guardianSubscriptionTool) Spec() tool.Spec {
	if t == nil {
		return tool.Spec{}
	}
	return t.spec
}

// Execute changes only the caller's channel subscription and returns the board's
// state for it. The live response cannot be known before the write, so the
// largest possible answer is budgeted first.
func (t *guardianSubscriptionTool) Execute(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
	if t == nil || t.board == nil {
		return nil, tool.Fatal("guardian subscription tool has no board")
	}
	if invocation == nil || invocation.Payload.Kind != tool.PayloadFunction {
		return nil, tool.Fatal("guardian subscription tool requires a function payload")
	}
	if len(invocation.Payload.Arguments) > maxMessageBoardArgumentBytes {
		return nil, modelError("subscription arguments exceed 128 KiB")
	}
	var arguments struct {
		ChannelName *string `json:"channel_name"`
	}
	if err := invocation.DecodeStrictArguments(&arguments); err != nil {
		return nil, modelError(err.Error())
	}
	if arguments.ChannelName == nil || *arguments.ChannelName == "" {
		return nil, modelError("channel_name must be a non-empty string")
	}
	name := *arguments.ChannelName
	largest := SubscriptionState{
		ChannelName:   name,
		TargetAgent:   t.callerPath,
		Enabled:       t.change == Subscribe,
		LastMessageID: &guardianNilMessageIDValue,
	}
	encodedLargest, err := json.Marshal(largest)
	if err != nil {
		return nil, tool.Fatal(err.Error())
	}
	if len(encodedLargest) > invocation.ResponseByteBudget(maxMessageBoardResponseBytes) {
		return nil, modelError("subscription response exceeds the tool-output budget")
	}
	state, err := t.board.SetSubscription(ctx, t.caller, SubscriptionRequest{
		Target: SubscriptionTarget{Kind: "channel", ChannelName: name},
		Change: t.change,
	})
	if err != nil {
		return nil, modelError(err.Error())
	}
	if state == nil {
		return nil, tool.Fatal("message board returned no subscription state")
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return nil, tool.Fatal(err.Error())
	}
	return messageBoardJSONOutput(invocation, encoded), nil
}
