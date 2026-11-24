package agentboard

import (
	"errors"
	"strings"

	"codex_go/agent"
	"codex_go/tool"
)

// GuardianBoardToolNamespace is the Responses namespace for Guardian's live
// evidence tools (Rust guardian-v3 retrieval_tools).
const GuardianBoardToolNamespace = "guardian_board"

// GuardianLiveReviewDescription explains the live board a reviewer sees.
// Retrieval and subscription tools share it as their namespace description.
const GuardianLiveReviewDescription = "Live original evidence by topic. Search and read any channel. Subscribe to receive future evidence from that channel."

// GuardianEvidenceSearchPointer is the JSON pointer whose string value holds a
// function call's JSON-encoded arguments (Rust
// board_with_json_search("/original/payload/arguments")).
const GuardianEvidenceSearchPointer = "/original/payload/arguments"

// GuardianLiveRetrievalTools lists the reviewer's read-only tools in Rust's
// order (guardian-v3 retrieval_tools LIVE_READ_TOOLS). Subscriptions are listed
// but changed only through the two subscription tools.
var GuardianLiveRetrievalTools = []MessageBoardTool{
	MessageBoardToolGetChannels,
	MessageBoardToolListThreads,
	MessageBoardToolSearchPosts,
	MessageBoardToolReadThread,
	MessageBoardToolReadPost,
	MessageBoardToolSubscribedChannels,
}

// GuardianRetrievalBinding is the live board surface of one reviewer
// conversation (Rust guardian-v3 LiveRetrievalBinding). Reads search the live
// board, including channels the reviewer does not follow, so evidence routed
// after a review started stays visible; the only mutations are the reviewer's
// own channel subscriptions.
type GuardianRetrievalBinding struct {
	board      AgentMessageBoard
	caller     string
	callerPath agent.AgentPath
	inbox      *GuardianActorInbox
}

// NewGuardianRetrievalBinding rejects ordinary boards, even for the same root
// (Rust #60002), and enables decoded-JSON search on the live handle (#62133).
func NewGuardianRetrievalBinding(board AgentMessageBoard, caller string, callerPath agent.AgentPath) (*GuardianRetrievalBinding, error) {
	if board == nil || strings.TrimSpace(board.Identity()) == "" {
		return nil, errors.New("reviewer retrieval requires a message board")
	}
	if board.StorageNamespace() != GuardianBoardStorageNamespace {
		return nil, errors.New("reviewer retrieval requires a Guardian board")
	}
	live, err := board.WithJSONSearch(GuardianEvidenceSearchPointer)
	if err != nil {
		return nil, err
	}
	return &GuardianRetrievalBinding{board: live, caller: caller, callerPath: callerPath}, nil
}

// BindInbox attaches the caller's live reviewer inbox so the subscription
// tools record selections where routing reads them (Rust #63357).
func (b *GuardianRetrievalBinding) BindInbox(inbox *GuardianActorInbox) error {
	if b == nil || inbox == nil {
		return errors.New("reviewer retrieval requires a live inbox")
	}
	if inbox.Root() != b.board.Identity() || inbox.Actor() != b.caller {
		return errors.New("Guardian inbox belongs to another reviewer")
	}
	b.inbox = inbox
	return nil
}

// Board is the live, JSON-search-enabled handle shared with the subscription
// tools and the rest of the review.
func (b *GuardianRetrievalBinding) Board() AgentMessageBoard {
	if b == nil {
		return nil
	}
	return b.board
}

// ToolNames lists the namespaced tool names the isolated reviewer policy may
// allow (Rust LiveRetrievalBinding::tool_names).
func (b *GuardianRetrievalBinding) ToolNames() []string {
	names := make([]string, 0, len(GuardianLiveRetrievalTools)+2)
	for _, selected := range GuardianLiveRetrievalTools {
		name := tool.NamespacedName(GuardianBoardToolNamespace, string(selected))
		names = append(names, name.Key())
	}
	for _, change := range []SubscriptionChange{Subscribe, Unsubscribe} {
		name := tool.NamespacedName(GuardianBoardToolNamespace, string(change))
		names = append(names, name.Key())
	}
	return names
}

// Tools returns channel discovery, post search and read, the reviewer's own
// subscriptions, and the two subscription tools.
func (b *GuardianRetrievalBinding) Tools() []tool.Executor {
	if b == nil || b.board == nil {
		return nil
	}
	tools := NewMessageBoardToolsWithSelection(
		b.board,
		GuardianLiveRetrievalTools,
		b.caller,
		b.callerPath,
		GuardianBoardToolNamespace,
		GuardianLiveReviewDescription,
		nil,
	)
	return append(tools, NewGuardianSubscriptionToolsWithInbox(
		b.board,
		b.caller,
		b.callerPath,
		b.inbox,
		GuardianBoardToolNamespace,
		GuardianLiveReviewDescription,
	)...)
}
