package agentboard

// Operations on one board. Requests describe intent independently of tool
// schemas.
//
// Rust parity: codex-rs/ext/agent-message-board/src/api.rs.

import (
	"context"

	"codex_go/agent"
)

// DefaultPageLimit mirrors Rust's PageRequest default limit.
const DefaultPageLimit = 20

// MaxPageLimit mirrors Rust's page() hard cap.
const MaxPageLimit = 50

// Board stores discussions and subscriptions for one agent tree.
//
// Every operation validates caller membership. Implementations enforce hard
// input/output limits and own atomic subscription changes, posting and
// recipient selection, excluding the post author even when explicitly targeted.
type Board interface {
	// Identity is the board's session identity.
	Identity() string

	CreateChannel(ctx context.Context, caller string, request CreateChannelRequest) (*ChannelSummary, error)
	ListChannels(ctx context.Context, caller string, query ChannelQuery) (*Page[ChannelSummary], error)
	// Post creates a post or reply. The request ID identifies a logical call
	// across retries: a retry with different input is an error, a successful
	// retry returns the same metadata.
	Post(ctx context.Context, caller string, request PostRequest) (*PostMetadata, error)
	ListThreads(ctx context.Context, caller string, query ThreadQuery) (*Page[ThreadSummary], error)
	SearchPosts(ctx context.Context, caller string, query PostQuery) (*Page[PostPreview], error)
	ReadThread(ctx context.Context, caller string, request ReadThreadRequest) (*ThreadPage, error)
	ReadPost(ctx context.Context, caller string, request ReadPostRequest) (*PostContent, error)
	// SetSubscription changes a channel subscription (new roots) or a thread
	// subscription (replies). Any member may change another member's
	// subscription.
	SetSubscription(ctx context.Context, caller string, request SubscriptionRequest) (*SubscriptionState, error)
}

// PageRequest is a cursor plus a limit.
type PageRequest struct {
	Cursor *string
	Limit  int
}

// NormalizedLimit returns the effective limit with Rust's default.
func (r PageRequest) NormalizedLimit() int {
	if r.Limit <= 0 {
		return DefaultPageLimit
	}
	return r.Limit
}

// SortDirection is the page ordering.
type SortDirection string

const (
	NewestFirst SortDirection = "newest_first"
	OldestFirst SortDirection = "oldest_first"
)

// SubscriptionChange is an explicit subscribe or unsubscribe.
type SubscriptionChange string

const (
	Subscribe   SubscriptionChange = "subscribe"
	Unsubscribe SubscriptionChange = "unsubscribe"
)

// CreateChannelRequest creates a channel and optionally subscribes the caller.
type CreateChannelRequest struct {
	ChannelName  string
	Subscription SubscriptionChange
}

// ChannelQuery lists or searches channels.
type ChannelQuery struct {
	Query     *string
	Direction SortDirection
	Page      PageRequest
}

// PostDestination identifies exactly one destination; discussion threads use
// their root post's ID.
type PostDestination struct {
	Kind string // "channel", "new_channel" or "thread"
	Name string // channel or new-channel name
	// ThreadID is the root post's message ID for a thread destination.
	ThreadID string

	// ExactlyOne returns the destination for a channel or thread.
}

// PostRequest posts to one destination.
type PostRequest struct {
	// RequestID is the host-generated tool invocation identity, never a model
	// argument.
	RequestID      string
	Destination    PostDestination
	Text           string
	AgentsToNotify []agent.AgentPath
}

// ThreadSort selects the thread ordering key.
type ThreadSort string

const (
	ThreadSortCreated  ThreadSort = "created"
	ThreadSortActivity ThreadSort = "activity"
)

// ThreadQuery lists a channel's threads.
type ThreadQuery struct {
	ChannelName     string
	Sort            ThreadSort
	Direction       SortDirection
	Page            PageRequest
	MaxCharsPerPost int
}

// PostQuery searches posts.
type PostQuery struct {
	ChannelName     *string
	Query           *string
	AfterMessageID  *string
	Author          *agent.AgentPath
	Page            PageRequest
	MaxCharsPerPost int
}

// ReadThreadRequest reads a thread by its root post ID.
type ReadThreadRequest struct {
	ThreadID        string
	Page            PageRequest
	MaxCharsPerPost int
}

// ReadPostRequest reads one post. Offsets and lengths count Unicode scalar
// values, not bytes.
type ReadPostRequest struct {
	MessageID   string
	OffsetChars int
	LimitChars  int
}

// SubscriptionTarget is a channel (new roots) or a thread (replies).
type SubscriptionTarget struct {
	Kind        string // "channel" or "thread"
	ChannelName string
	ThreadID    string
}

// SubscriptionRequest changes a subscription.
type SubscriptionRequest struct {
	Target SubscriptionTarget
	// TargetAgent is nil to change the caller's own subscription.
	TargetAgent *agent.AgentPath
	Change      SubscriptionChange
}
