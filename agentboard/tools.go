package agentboard

// Model tools over a caller-bound board. Results stay valid, bounded JSON.
//
// Rust parity: codex-rs/ext/agent-message-board/src/tools.rs,
// tools/spec.rs and tools/arguments.rs.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"codex_go/agent"
	"codex_go/tool"
)

const (
	// maxMessageBoardResponseBytes mirrors Rust's MAX_RESPONSE_BYTES. It applies
	// to direct and Code Mode calls alike, before the model's own allowance is
	// folded in by tool.Invocation.ResponseByteBudget.
	maxMessageBoardResponseBytes = 8_000
	// maxMessageBoardArgumentBytes mirrors Rust's 128 KiB raw-argument cap.
	maxMessageBoardArgumentBytes = 128 * 1024
	// messageBoardMutationReserveBytes mirrors Rust's 2048-byte reserve for
	// metadata, escaped channel names and agent paths before any write.
	messageBoardMutationReserveBytes = 2048
	// maxMessageBoardErrorChars mirrors Rust's model_error 512-character cap.
	maxMessageBoardErrorChars = 512
	// defaultPreviewChars mirrors Rust's default max_chars_per_post.
	defaultPreviewChars = 1_000
	// maxPreviewChars mirrors Rust's preview cap.
	maxPreviewChars = 20_000
)

// MessageBoardToolNames lists the collaboration tools in Rust's spec::NAMES
// order.
var MessageBoardToolNames = []string{
	"create_channel",
	"get_channels",
	"list_threads",
	"search_posts",
	"read_thread",
	"read_post",
	"subscribe",
	"unsubscribe",
	"post",
}

// messageBoardParallelTools mirrors Rust's supports_parallel_tool_calls: the
// read-only channel tools may run in parallel, mutations may not.
var messageBoardParallelTools = map[string]bool{
	"get_channels": true,
	"list_threads": true,
	"search_posts": true,
	"read_thread":  true,
	"read_post":    true,
}

// MessageBoardToolOverride carries one tool's catalog overrides (Rust
// MultiAgentToolMessages). Only the description applies: Rust warns and keeps
// the bundled parameters when a catalog supplies channel-tool parameters,
// because the collaboration tools' schemas cannot be overridden.
type MessageBoardToolOverride struct {
	Description *string
}

// NewMessageBoardTools builds the nine collaboration tools bound to one caller.
// The host supplies the namespace and its description; an empty namespace
// publishes plain function tools. The caller and its path must come from the
// host's authoritative tree metadata.
func NewMessageBoardTools(board Board, caller string, callerPath agent.AgentPath, namespace string, namespaceDescription string) []tool.Executor {
	return NewMessageBoardToolsWithOverrides(board, caller, callerPath, namespace, namespaceDescription, nil)
}

// NewMessageBoardToolsWithOverrides mirrors Rust's
// message_board_tools_with_descriptions.
func NewMessageBoardToolsWithOverrides(board Board, caller string, callerPath agent.AgentPath, namespace string, namespaceDescription string, overrides map[string]MessageBoardToolOverride) []tool.Executor {
	namespace = strings.TrimSpace(namespace)
	executors := make([]tool.Executor, 0, len(MessageBoardToolNames))
	for _, name := range MessageBoardToolNames {
		executor := &messageBoardTool{
			board:                board,
			caller:               caller,
			callerPath:           callerPath,
			name:                 name,
			namespace:            namespace,
			namespaceDescription: namespaceDescription,
		}
		if override, ok := overrides[name]; ok {
			executor.description = override.Description
		}
		executors = append(executors, executor)
	}
	return executors
}

type messageBoardTool struct {
	board                Board
	caller               string
	callerPath           agent.AgentPath
	name                 string
	namespace            string
	namespaceDescription string
	description          *string
}

type messageBoardToolSpec struct {
	description string
	fields      []string
	required    []string
}

func (t *messageBoardTool) Spec() tool.Spec {
	bundled := messageBoardSpecFor(t.name)
	description := bundled.description
	if t.description != nil {
		description = *t.description
	}
	name := tool.PlainName(t.name)
	namespaceDescription := ""
	if t.namespace != "" {
		name = tool.NamespacedName(t.namespace, t.name)
		namespaceDescription = t.namespaceDescription
	}
	return tool.Spec{
		Name:                 name,
		Description:          description,
		InputSchema:          messageBoardInputSchema(bundled),
		NamespaceDescription: namespaceDescription,
		Parallel:             messageBoardParallelTools[t.name],
	}
}

func messageBoardSpecFor(name string) messageBoardToolSpec {
	switch name {
	case "create_channel":
		return messageBoardToolSpec{
			description: "Create a channel where all agents in this collaboration can read and post messages. You are subscribed to new top-level posts by default.",
			fields:      []string{"channel_name", "subscribe"},
			required:    []string{"channel_name"},
		}
	case "get_channels":
		return messageBoardToolSpec{
			description: "List channels, most recently active first, or search by a case-insensitive part of the name. Creating a channel or posting in it counts as activity.",
			fields:      []string{"query", "recent_first", "limit", "cursor"},
		}
	case "list_threads":
		return messageBoardToolSpec{
			description: "List a channel's threads with previews of the first post and latest reply. New threads come first by default; sorting by activity brings threads with recent replies to the top.",
			fields:      []string{"channel_name", "sort", "recent_first", "limit", "cursor", "max_chars_per_post"},
			required:    []string{"channel_name"},
		}
	case "search_posts":
		return messageBoardToolSpec{
			description: "Search top-level posts and replies, newest first. Text matches are case-insensitive substrings; omit the query to see recent activity. You can narrow by channel, author, or posts after a message ID. Results are previews; read_post can retrieve the full text.",
			fields:      []string{"channel_name", "query", "after_message_id", "author", "limit", "cursor", "max_chars_per_post"},
		}
	case "read_thread":
		return messageBoardToolSpec{
			description: "Read a thread using its first post's message ID. Every page includes previews of the first post and the newest replies; the cursor advances through replies.",
			fields:      []string{"thread_id", "limit", "cursor", "max_chars_per_post"},
			required:    []string{"thread_id"},
		}
	case "read_post":
		return messageBoardToolSpec{
			description: "Read a post or reply by message ID, without needing its channel. Offsets count Unicode characters; continue at next_offset_chars while it is less than n_chars.",
			fields:      []string{"message_id", "offset_chars", "limit_chars"},
			required:    []string{"message_id"},
		}
	case "subscribe":
		return messageBoardToolSpec{
			description: "Subscribe yourself or another agent to a channel for new top-level posts, or to a thread for replies. Provide exactly one of channel_name or thread_id. Notifications only reach agents with a running turn; missed notifications are not saved.",
			fields:      []string{"channel_name", "thread_id", "target_agent"},
		}
	case "unsubscribe":
		return messageBoardToolSpec{
			description: "Unsubscribe yourself or another agent from a channel or thread. Provide exactly one of channel_name or thread_id. Posting again does not undo a thread unsubscribe. Agents explicitly named on a post can still receive that notification.",
			fields:      []string{"channel_name", "thread_id", "target_agent"},
		}
	case "post":
		return messageBoardToolSpec{
			description: "Start a thread in an existing or new channel, or reply using the first post's message ID as thread_id. Exactly one destination is required. Posting subscribes you to the thread unless you previously unsubscribed. agents_to_notify sends a one-time notification without subscribing recipients or starting idle agents. Returns metadata, not the post text.",
			fields:      []string{"text", "channel_name", "new_channel_name", "thread_id", "agents_to_notify"},
			required:    []string{"text"},
		}
	default:
		return messageBoardToolSpec{}
	}
}

func messageBoardInputSchema(spec messageBoardToolSpec) map[string]any {
	properties := make(map[string]any, len(spec.fields))
	for _, field := range spec.fields {
		properties[field] = messageBoardFieldSchema(field)
	}
	return map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             append([]string{}, spec.required...),
		"additionalProperties": false,
	}
}

func messageBoardFieldSchema(field string) map[string]any {
	switch field {
	case "new_channel_name":
		return map[string]any{"type": "string", "description": "Create and subscribe to this channel."}
	case "subscribe":
		return map[string]any{"type": "boolean", "description": "Subscribe to new top-level posts. Default true."}
	case "author", "target_agent":
		return map[string]any{"type": "string", "description": "An absolute agent path or a reference relative to you."}
	case "recent_first":
		return map[string]any{"type": "boolean", "description": "Newest first by default."}
	case "limit":
		return map[string]any{"type": "integer", "minimum": 1, "description": "Maximum results, default 20; output budgets may return fewer. Continue with next_cursor."}
	case "offset_chars":
		return map[string]any{"type": "integer", "minimum": 0, "description": "Default 0."}
	case "limit_chars", "max_chars_per_post":
		return map[string]any{"type": "integer", "minimum": 1, "description": "Maximum characters; further capped by output budget. Defaults: limit_chars 20000, max_chars_per_post 1000."}
	case "sort":
		return map[string]any{"type": "string", "enum": []string{"created", "activity"}}
	case "agents_to_notify":
		return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Absolute agent paths or references relative to you; maximum 256."}
	case "cursor":
		return map[string]any{"type": "string", "description": "Opaque next_cursor from the same query. Keep filters and sorting unchanged. Concurrent posts may shift pages; omit the cursor to refresh."}
	default:
		return map[string]any{"type": "string"}
	}
}

func (t *messageBoardTool) Execute(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
	if t == nil || t.board == nil {
		return nil, tool.Fatal("message-board tool has no board")
	}
	if invocation == nil || invocation.Payload.Kind != tool.PayloadFunction {
		return nil, tool.Fatal("message-board tool requires a function payload")
	}
	if len(invocation.Payload.Arguments) > maxMessageBoardArgumentBytes {
		return nil, modelError("message-board arguments exceed 128 KiB")
	}
	budget := invocation.ResponseByteBudget(maxMessageBoardResponseBytes)
	result, err := t.execute(ctx, invocation.Payload.Arguments, invocation, budget)
	if err != nil {
		return nil, err
	}
	serialized, err := json.Marshal(result)
	if err != nil {
		return nil, modelError(err.Error())
	}
	if len(serialized) > budget {
		return nil, modelError("Result exceeds the output budget. Reduce limit or max_chars_per_post; use read_post with a smaller limit_chars for long posts.")
	}
	return messageBoardJSONOutput(invocation, serialized), nil
}

func (t *messageBoardTool) execute(ctx context.Context, raw string, invocation *tool.Invocation, budget int) (any, error) {
	pageLimit := func(limit *int) int {
		value := DefaultPageLimit
		if limit != nil {
			value = *limit
		}
		if value > MaxPageLimit {
			value = MaxPageLimit
		}
		return value
	}
	previewLimit := func(limit *int) int {
		value := defaultPreviewChars
		if limit != nil {
			value = *limit
		}
		if value > maxPreviewChars {
			value = maxPreviewChars
		}
		return value
	}
	direction := func(recentFirst *bool) SortDirection {
		if recentFirst == nil || *recentFirst {
			return NewestFirst
		}
		return OldestFirst
	}
	page := func(limit int, cursor *string, scale int) PageRequest {
		return PageRequest{Cursor: cursor, Limit: messageBoardNonZero(limit / scale)}
	}

	switch t.name {
	case "create_channel":
		var args createChannelArgs
		if err := decodeMessageBoardArgs(invocation, &args); err != nil {
			return nil, err
		}
		if err := t.checkMutationBudget(budget, 0); err != nil {
			return nil, err
		}
		subscription := Subscribe
		if args.Subscribe != nil && !*args.Subscribe {
			subscription = Unsubscribe
		}
		return messageBoardResult(t.board.CreateChannel(ctx, t.caller, CreateChannelRequest{
			ChannelName:  *args.ChannelName,
			Subscription: subscription,
		}))
	case "get_channels":
		var args getChannelsArgs
		if err := decodeMessageBoardArgs(invocation, &args); err != nil {
			return nil, err
		}
		limit := pageLimit(args.Limit)
		return messageBoardBoundedRead(budget, limit, func(scale int) (any, error) {
			return t.board.ListChannels(ctx, t.caller, ChannelQuery{
				Query:     args.Query,
				Direction: direction(args.RecentFirst),
				Page:      page(limit, args.Cursor, scale),
			})
		})
	case "list_threads":
		var args listThreadsArgs
		if err := decodeMessageBoardArgs(invocation, &args); err != nil {
			return nil, err
		}
		limit := pageLimit(args.Limit)
		maxCharsPerPost := previewLimit(args.MaxCharsPerPost)
		return messageBoardBoundedRead(budget, maxInt(limit, maxCharsPerPost), func(scale int) (any, error) {
			sort := ThreadSortCreated
			if args.Sort != nil {
				sort = ThreadSort(*args.Sort)
			}
			return t.board.ListThreads(ctx, t.caller, ThreadQuery{
				ChannelName:     *args.ChannelName,
				Sort:            sort,
				Direction:       direction(args.RecentFirst),
				MaxCharsPerPost: messageBoardNonZero(maxCharsPerPost / scale),
				Page:            page(limit, args.Cursor, scale),
			})
		})
	case "search_posts":
		var args searchPostsArgs
		if err := decodeMessageBoardArgs(invocation, &args); err != nil {
			return nil, err
		}
		author, err := t.resolveOptionalPath(args.Author)
		if err != nil {
			return nil, err
		}
		limit := pageLimit(args.Limit)
		maxCharsPerPost := previewLimit(args.MaxCharsPerPost)
		return messageBoardBoundedRead(budget, maxInt(limit, maxCharsPerPost), func(scale int) (any, error) {
			return t.board.SearchPosts(ctx, t.caller, PostQuery{
				ChannelName:     args.ChannelName,
				Query:           args.Query,
				AfterMessageID:  args.AfterMessageID,
				Author:          author,
				MaxCharsPerPost: messageBoardNonZero(maxCharsPerPost / scale),
				Page:            page(limit, args.Cursor, scale),
			})
		})
	case "read_thread":
		var args readThreadArgs
		if err := decodeMessageBoardArgs(invocation, &args); err != nil {
			return nil, err
		}
		limit := pageLimit(args.Limit)
		maxCharsPerPost := previewLimit(args.MaxCharsPerPost)
		return messageBoardBoundedRead(budget, maxInt(limit, maxCharsPerPost), func(scale int) (any, error) {
			return t.board.ReadThread(ctx, t.caller, ReadThreadRequest{
				ThreadID:        *args.ThreadID,
				MaxCharsPerPost: messageBoardNonZero(maxCharsPerPost / scale),
				Page:            page(limit, args.Cursor, scale),
			})
		})
	case "read_post":
		var args readPostArgs
		if err := decodeMessageBoardArgs(invocation, &args); err != nil {
			return nil, err
		}
		limitChars := maxReadPostChars
		if args.LimitChars != nil {
			limitChars = *args.LimitChars
		}
		if limitChars > maxReadPostChars {
			limitChars = maxReadPostChars
		}
		offset := 0
		if args.OffsetChars != nil {
			offset = *args.OffsetChars
		}
		return messageBoardBoundedRead(budget, limitChars, func(scale int) (any, error) {
			return t.board.ReadPost(ctx, t.caller, ReadPostRequest{
				MessageID:   *args.MessageID,
				OffsetChars: offset,
				LimitChars:  messageBoardNonZero(limitChars / scale),
			})
		})
	case "subscribe", "unsubscribe":
		var args subscriptionArgs
		if err := decodeMessageBoardArgs(invocation, &args); err != nil {
			return nil, err
		}
		targetPathBytes := 0
		if args.TargetAgent != nil {
			targetPathBytes = len(*args.TargetAgent)
		}
		if err := t.checkMutationBudget(budget, targetPathBytes); err != nil {
			return nil, err
		}
		var target SubscriptionTarget
		switch {
		case args.ChannelName != nil && args.ThreadID == nil:
			target = SubscriptionTarget{Kind: "channel", ChannelName: *args.ChannelName}
		case args.ChannelName == nil && args.ThreadID != nil:
			target = SubscriptionTarget{Kind: "thread", ThreadID: *args.ThreadID}
		default:
			return nil, modelError("Supply exactly one of channel_name or thread_id")
		}
		targetAgent, err := t.resolveOptionalPath(args.TargetAgent)
		if err != nil {
			return nil, err
		}
		change := Subscribe
		if t.name == "unsubscribe" {
			change = Unsubscribe
		}
		return messageBoardResult(t.board.SetSubscription(ctx, t.caller, SubscriptionRequest{
			Target:      target,
			TargetAgent: targetAgent,
			Change:      change,
		}))
	case "post":
		var args postArgs
		if err := decodeMessageBoardArgs(invocation, &args); err != nil {
			return nil, err
		}
		if err := t.checkMutationBudget(budget, 0); err != nil {
			return nil, err
		}
		var destination PostDestination
		switch {
		case args.ChannelName != nil && args.NewChannelName == nil && args.ThreadID == nil:
			destination = PostDestination{Kind: "channel", Name: *args.ChannelName}
		case args.ChannelName == nil && args.NewChannelName != nil && args.ThreadID == nil:
			destination = PostDestination{Kind: "new_channel", Name: *args.NewChannelName}
		case args.ChannelName == nil && args.NewChannelName == nil && args.ThreadID != nil:
			destination = PostDestination{Kind: "thread", ThreadID: *args.ThreadID}
		default:
			return nil, modelError("Supply exactly one of channel_name, new_channel_name or thread_id")
		}
		recipients := make([]agent.AgentPath, 0, len(args.AgentsToNotify))
		for _, reference := range args.AgentsToNotify {
			resolved, err := t.callerPath.ResolvePath(reference)
			if err != nil {
				return nil, modelError(err.Error())
			}
			recipients = append(recipients, resolved)
		}
		return messageBoardResult(t.board.Post(ctx, t.caller, PostRequest{
			RequestID:      messageBoardRequestID(invocation),
			Destination:    destination,
			Text:           *args.Text,
			AgentsToNotify: recipients,
		}))
	default:
		return nil, tool.Fatal("unknown message-board tool " + t.name)
	}
}

// messageBoardResult maps a board error through the same 512-character model
// error Rust's encode() uses.
func messageBoardResult(result any, err error) (any, error) {
	if err != nil {
		return nil, modelError(err.Error())
	}
	return result, nil
}

func (t *messageBoardTool) resolveOptionalPath(reference *string) (*agent.AgentPath, error) {
	if reference == nil {
		return nil, nil
	}
	resolved, err := t.callerPath.ResolvePath(*reference)
	if err != nil {
		return nil, modelError(err.Error())
	}
	return &resolved, nil
}

func (t *messageBoardTool) checkMutationBudget(budget int, targetPathBytes int) error {
	// Reserve metadata, escaped channel names and agent paths before any write.
	if budget < messageBoardMutationReserveBytes+len(t.callerPath)+targetPathBytes {
		return modelError("Output budget is too small to acknowledge a message-board mutation; no change was made.")
	}
	return nil
}

// messageBoardBoundedRead tries the requested read first and halves the limits
// only when its serialized output is too large. Reissuing the read lets each
// backend generate a cursor for exactly the returned page. It stops once every
// limit has reached one, because another read would repeat the same request.
func messageBoardBoundedRead(budget int, largestLimit int, fetch func(scale int) (any, error)) (any, error) {
	scale := 1
	for {
		result, err := fetch(scale)
		if err != nil {
			return nil, modelError(err.Error())
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			return nil, modelError(err.Error())
		}
		if len(encoded) <= budget {
			return result, nil
		}
		if largestLimit/scale <= 1 {
			break
		}
		scale *= 2
	}
	return nil, modelError("The output budget is too small for this result's metadata.")
}

func messageBoardJSONOutput(invocation *tool.Invocation, body []byte) *tool.Output {
	text := string(body)
	return &tool.Output{
		CallID:   invocation.CallID,
		ToolName: invocation.ToolName,
		Success:  true,
		Body:     text,
		Data: map[string]any{
			"content_items": []any{map[string]any{"type": "input_text", "text": text}},
		},
	}
}

// messageBoardRequestID mirrors Rust's host-generated request ID: the turn ID,
// the call ID for a direct call, or the cell and runtime-call IDs for a Code
// Mode call.
func messageBoardRequestID(invocation *tool.Invocation) string {
	turnID := invocationContextString(invocation, "turn_id")
	callID := ""
	if invocation != nil {
		callID = invocation.CallID
	}
	if invocation.IsCodeModeCall() {
		callID = invocationContextString(invocation, tool.CodeModeCellIDContextKey) + ":" + callID
	}
	return turnID + ":" + callID
}

func invocationContextString(invocation *tool.Invocation, key string) string {
	if invocation == nil || invocation.Context == nil {
		return ""
	}
	value, _ := invocation.Context[key].(string)
	return strings.TrimSpace(value)
}

func decodeMessageBoardArgs(invocation *tool.Invocation, target any) error {
	if err := invocation.DecodeStrictArguments(target); err != nil {
		return modelError(err.Error())
	}
	if validatable, ok := target.(interface{ validate() error }); ok {
		if err := validatable.validate(); err != nil {
			return modelError(err.Error())
		}
	}
	return nil
}

// modelError mirrors Rust's model_error: the message is shown to the model,
// truncated to 512 characters.
func modelError(message string) *tool.FunctionCallError {
	runes := []rune(message)
	if len(runes) > maxMessageBoardErrorChars {
		message = string(runes[:maxMessageBoardErrorChars])
	}
	return tool.RespondToModel(message)
}

func messageBoardNonZero(value int) int {
	if value < 1 {
		return 1
	}
	return value
}

func maxInt(left int, right int) int {
	if left > right {
		return left
	}
	return right
}

// --- arguments (Rust tools/arguments.rs; every struct denies unknown fields) ---
//
// Rust's non-Option fields are pointers here so a missing field is rejected the
// way serde rejects it, and Rust's NonZeroU32/u32/Uuid/enum fields are plain Go
// values validated after decoding. serde's exact error text is not reproduced;
// the messages describe the same rejection.

type createChannelArgs struct {
	ChannelName *string `json:"channel_name"`
	Subscribe   *bool   `json:"subscribe"`
}

func (a *createChannelArgs) validate() error {
	return requireStringField("channel_name", a.ChannelName)
}

type getChannelsArgs struct {
	Query       *string `json:"query"`
	RecentFirst *bool   `json:"recent_first"`
	Limit       *int    `json:"limit"`
	Cursor      *string `json:"cursor"`
}

func (a *getChannelsArgs) validate() error {
	return requireNonZeroField("limit", a.Limit)
}

type listThreadsArgs struct {
	ChannelName     *string `json:"channel_name"`
	Sort            *string `json:"sort"`
	RecentFirst     *bool   `json:"recent_first"`
	Limit           *int    `json:"limit"`
	Cursor          *string `json:"cursor"`
	MaxCharsPerPost *int    `json:"max_chars_per_post"`
}

func (a *listThreadsArgs) validate() error {
	if err := requireStringField("channel_name", a.ChannelName); err != nil {
		return err
	}
	if a.Sort != nil {
		switch ThreadSort(*a.Sort) {
		case ThreadSortCreated, ThreadSortActivity:
		default:
			return fmt.Errorf("unknown variant `%s`, expected `created` or `activity`", *a.Sort)
		}
	}
	if err := requireNonZeroField("limit", a.Limit); err != nil {
		return err
	}
	return requireNonZeroField("max_chars_per_post", a.MaxCharsPerPost)
}

type searchPostsArgs struct {
	ChannelName     *string `json:"channel_name"`
	Query           *string `json:"query"`
	AfterMessageID  *string `json:"after_message_id"`
	Author          *string `json:"author"`
	Limit           *int    `json:"limit"`
	Cursor          *string `json:"cursor"`
	MaxCharsPerPost *int    `json:"max_chars_per_post"`
}

func (a *searchPostsArgs) validate() error {
	if err := requireUUIDField("after_message_id", a.AfterMessageID); err != nil {
		return err
	}
	if err := requireNonZeroField("limit", a.Limit); err != nil {
		return err
	}
	return requireNonZeroField("max_chars_per_post", a.MaxCharsPerPost)
}

type readThreadArgs struct {
	ThreadID        *string `json:"thread_id"`
	Limit           *int    `json:"limit"`
	Cursor          *string `json:"cursor"`
	MaxCharsPerPost *int    `json:"max_chars_per_post"`
}

func (a *readThreadArgs) validate() error {
	if err := requireStringField("thread_id", a.ThreadID); err != nil {
		return err
	}
	if err := requireUUIDField("thread_id", a.ThreadID); err != nil {
		return err
	}
	if err := requireNonZeroField("limit", a.Limit); err != nil {
		return err
	}
	return requireNonZeroField("max_chars_per_post", a.MaxCharsPerPost)
}

type readPostArgs struct {
	MessageID   *string `json:"message_id"`
	OffsetChars *int    `json:"offset_chars"`
	LimitChars  *int    `json:"limit_chars"`
}

func (a *readPostArgs) validate() error {
	if err := requireStringField("message_id", a.MessageID); err != nil {
		return err
	}
	if err := requireUUIDField("message_id", a.MessageID); err != nil {
		return err
	}
	if a.OffsetChars != nil && *a.OffsetChars < 0 {
		return unsignedFieldError("offset_chars", *a.OffsetChars)
	}
	return requireNonZeroField("limit_chars", a.LimitChars)
}

type subscriptionArgs struct {
	ChannelName *string `json:"channel_name"`
	ThreadID    *string `json:"thread_id"`
	TargetAgent *string `json:"target_agent"`
}

func (a *subscriptionArgs) validate() error {
	return requireUUIDField("thread_id", a.ThreadID)
}

type postArgs struct {
	Text           *string  `json:"text"`
	ChannelName    *string  `json:"channel_name"`
	NewChannelName *string  `json:"new_channel_name"`
	ThreadID       *string  `json:"thread_id"`
	AgentsToNotify []string `json:"agents_to_notify"`
}

func (a *postArgs) validate() error {
	if err := requireStringField("text", a.Text); err != nil {
		return err
	}
	return requireUUIDField("thread_id", a.ThreadID)
}

// requireStringField rejects a missing non-Option String field the way serde
// rejects it.
func requireStringField(name string, value *string) error {
	if value == nil {
		return fmt.Errorf("missing field `%s`", name)
	}
	return nil
}

// requireNonZeroField rejects a present NonZeroU32 field that is not positive.
func requireNonZeroField(name string, value *int) error {
	if value == nil {
		return nil
	}
	if *value < 1 {
		return fmt.Errorf("invalid value for `%s`: integer `%d`, expected a nonzero u32", name, *value)
	}
	return nil
}

// requireUUIDField rejects a present field that is not a UUID.
func requireUUIDField(name string, value *string) error {
	if value == nil {
		return nil
	}
	if _, err := uuid.Parse(*value); err != nil {
		return fmt.Errorf("invalid value for `%s`: string %q, expected a UUID", name, *value)
	}
	return nil
}

func unsignedFieldError(name string, value int) error {
	return fmt.Errorf("invalid value for `%s`: integer `%d`, expected u32", name, value)
}

var _ tool.Executor = (*messageBoardTool)(nil)
