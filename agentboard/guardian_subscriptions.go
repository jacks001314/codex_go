package agentboard

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"codex_go/agent"
	"codex_go/tool"
)

const (
	guardianSubscribeDescription   = "Subscribe yourself to an evidence channel in the captured catalog. This changes future evidence selection, without reading posts or authorizing any action."
	guardianUnsubscribeDescription = "Unsubscribe yourself from an evidence channel in the captured catalog. Evidence already admitted to the reviewer conversation is retained."
)

// ReviewerSubscriptions is a reviewer's fixed discovery view plus authority to
// change only its own subscriptions (Rust guardian-v3 selection.rs). Capture
// once after housekeeping reaches the review cutoff and reuse the same view for
// retrieval and delivery.
type ReviewerSubscriptions struct {
	board      AgentMessageBoard
	view       *BoardReadView
	caller     string
	callerPath agent.AgentPath
}

// CaptureReviewerSubscriptions reads the Guardian board and captures its
// catalog. Ordinary boards are rejected even when they belong to the same root
// (Rust #59995/#60002).
func CaptureReviewerSubscriptions(ctx context.Context, board AgentMessageBoard, caller string, callerPath agent.AgentPath) (*ReviewerSubscriptions, error) {
	if board == nil {
		return nil, errors.New("reviewer subscriptions require a message board")
	}
	if board.StorageNamespace() != GuardianBoardStorageNamespace {
		return nil, errors.New("reviewer subscriptions require the Guardian board")
	}
	view, err := board.CaptureReadView(ctx)
	if err != nil {
		return nil, err
	}
	return &ReviewerSubscriptions{board: board, view: view, caller: caller, callerPath: callerPath}, nil
}

// View is the captured catalog shared by retrieval and delivery planning.
func (s *ReviewerSubscriptions) View() *BoardReadView {
	if s == nil {
		return nil
	}
	return s.view
}

// Channels reads the caller's live membership from storage so independently
// invoked tools cannot leave a stale in-memory list, then keeps only channels
// that were visible when the view was captured.
func (s *ReviewerSubscriptions) Channels(ctx context.Context) ([]string, error) {
	if s == nil || s.board == nil || s.view == nil {
		return nil, errors.New("reviewer subscriptions are unavailable")
	}
	catalog := make(map[string]struct{}, len(s.view.Channels()))
	for _, name := range s.view.Channels() {
		catalog[name] = struct{}{}
	}
	subscribed, err := s.subscribedChannels(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(subscribed))
	for _, name := range subscribed {
		if _, ok := catalog[name]; ok {
			names = append(names, name)
		}
	}
	return names, nil
}

// Replace atomically replaces the caller's membership in the captured catalog
// after validating every selected name (Rust ReviewerSubscriptions::prepare).
// Channels outside the captured catalog keep their subscriptions and opt-outs.
// Selection is navigation, never authorization.
func (s *ReviewerSubscriptions) Replace(ctx context.Context, channels []string) ([]string, error) {
	if s == nil || s.board == nil || s.view == nil {
		return nil, errors.New("reviewer subscriptions are unavailable")
	}
	catalog := make(map[string]struct{}, len(s.view.Channels()))
	for _, name := range s.view.Channels() {
		catalog[name] = struct{}{}
	}
	seen := make(map[string]struct{}, len(channels))
	selection := make([]string, 0, len(channels))
	for _, name := range channels {
		if _, ok := catalog[name]; !ok {
			return nil, invalid("selected channels must be unique names from the captured catalog")
		}
		if _, ok := seen[name]; ok {
			return nil, invalid("selected channels must be unique names from the captured catalog")
		}
		seen[name] = struct{}{}
		selection = append(selection, name)
	}
	membership, err := s.guardianSelectionMembership(ctx, selection)
	if err != nil {
		return nil, err
	}
	state, err := s.board.SetSubscriptions(ctx, s.caller, BulkSubscriptionsRequest{Channels: membership})
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, nil
	}
	return state.Channels, nil
}

// Tools returns the two self-only channel subscription tools. They never reveal
// a later live post ID: responses use the captured catalog boundary.
func (s *ReviewerSubscriptions) Tools(namespace string, namespaceDescription string) []tool.Executor {
	if s == nil {
		return nil
	}
	namespace = strings.TrimSpace(namespace)
	return []tool.Executor{
		newGuardianSubscriptionTool(s, Subscribe, namespace, namespaceDescription),
		newGuardianSubscriptionTool(s, Unsubscribe, namespace, namespaceDescription),
	}
}

type guardianSubscriptionTool struct {
	subscriptions *ReviewerSubscriptions
	change        SubscriptionChange
	spec          tool.Spec
}

func newGuardianSubscriptionTool(subscriptions *ReviewerSubscriptions, change SubscriptionChange, namespace string, namespaceDescription string) *guardianSubscriptionTool {
	name := tool.PlainName(string(change))
	if namespace != "" {
		name = tool.NamespacedName(namespace, string(change))
	}
	description := guardianSubscribeDescription
	if change == Unsubscribe {
		description = guardianUnsubscribeDescription
	}
	return &guardianSubscriptionTool{
		subscriptions: subscriptions,
		change:        change,
		spec: tool.Spec{
			Name:        name,
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

func (t *guardianSubscriptionTool) Execute(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
	if t == nil || t.subscriptions == nil || t.subscriptions.view == nil {
		return nil, tool.Fatal("guardian subscription tool has no subscriptions")
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
	if arguments.ChannelName == nil || strings.TrimSpace(*arguments.ChannelName) == "" {
		return nil, modelError("channel_name must be a non-empty string")
	}
	name := *arguments.ChannelName
	var captured *ChannelSummary
	for _, channel := range t.subscriptions.view.ChannelCatalog() {
		if channel.ChannelName == name {
			copy := channel
			captured = &copy
			break
		}
	}
	if captured == nil {
		return nil, modelError("channel is outside the captured catalog")
	}
	response := SubscriptionState{
		ChannelName:   name,
		TargetAgent:   t.subscriptions.callerPath,
		Enabled:       t.change == Subscribe,
		LastMessageID: captured.LastMessageID,
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return nil, tool.Fatal(err.Error())
	}
	if len(encoded) > invocation.ResponseByteBudget(maxMessageBoardResponseBytes) {
		return nil, modelError("subscription response exceeds the tool-output budget")
	}
	if _, err := t.subscriptions.board.SetSubscription(ctx, t.subscriptions.caller, SubscriptionRequest{
		Target: SubscriptionTarget{Kind: "channel", ChannelName: name},
		Change: t.change,
	}); err != nil {
		return nil, modelError(err.Error())
	}
	return messageBoardJSONOutput(invocation, encoded), nil
}
