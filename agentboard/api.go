package agentboard

// Operations on one board. Requests describe intent independently of tool
// schemas.
//
// Rust parity: codex-rs/ext/agent-message-board/src/api.rs.

import (
	"context"
	"encoding/json"

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
	// UpsertChannel creates a channel or replaces only its description,
	// preserving posts, authorship, creation time, and subscriptions. Backends
	// without upsert support reject it rather than substituting creation
	// (Rust #56494).
	UpsertChannel(ctx context.Context, caller string, request UpsertChannelRequest) error
	ListChannels(ctx context.Context, caller string, query ChannelQuery) (*ChannelPage, error)
	// Post creates a post or reply. The request ID identifies a logical call
	// across retries: a retry with different input is an error, a successful
	// retry returns the same metadata.
	Post(ctx context.Context, caller string, request PostRequest) (*PostMetadata, error)
	// Append writes evidence without automatic subscriptions or notifications,
	// using caller-scoped retry IDs shared with posting: an identical retry
	// returns the original metadata, while changing the payload or operation is
	// an error. Backends without silent-write support reject it rather than
	// falling back to posting (Rust #56461).
	Append(ctx context.Context, caller string, request AppendRequest) (*PostMetadata, error)
	// AppendWithSubscribers is a silent append that also returns the agents
	// subscribed to the destination at the moment of insertion, so a trusted
	// host consumer can queue evidence without re-reading the board. An
	// idempotent retry returns the original metadata and no subscribers
	// (Rust #62135).
	AppendWithSubscribers(ctx context.Context, caller string, request AppendRequest) (*PostMetadata, []string, error)
	ListThreads(ctx context.Context, caller string, query ThreadQuery) (*Page[ThreadSummary], error)
	SearchPosts(ctx context.Context, caller string, query PostQuery) (*Page[PostPreview], error)
	ReadThread(ctx context.Context, caller string, request ReadThreadRequest) (*ThreadPage, error)
	ReadPost(ctx context.Context, caller string, request ReadPostRequest) (*PostContent, error)
	// ReadPostTextsByID loads whole post bodies in caller-supplied ID order,
	// bounded to one board page, so a trusted host consumer can admit evidence
	// without rebuilding paged previews. A missing post fails the whole read
	// (Rust #62136).
	ReadPostTextsByID(ctx context.Context, caller string, ids []string) ([]string, error)
	// SetSubscription changes a channel subscription (new roots) or a thread
	// subscription (replies). Any member may change another member's
	// subscription.
	SetSubscription(ctx context.Context, caller string, request SubscriptionRequest) (*SubscriptionState, error)
}

// AgentMessageBoard is the full backend surface Guardian's reviewer preparation
// needs: the ordinary board plus its storage namespace, a fixed capture, the
// caller's subscription listing and one atomic membership replacement.
//
// Rust parity: codex-rs/message-board/src/api.rs AgentMessageBoard. Guardian only
// accepts the namespace returned here, so an ordinary board is rejected before
// any capture or write happens.
type AgentMessageBoard interface {
	Board
	// StorageNamespace is the durable namespace this board was opened in.
	StorageNamespace() string
	// WithJSONSearch returns a live handle whose searches also match decoded
	// JSON literals at the pointer, leaving other handles unchanged (Rust
	// AgentMessageBoard::with_json_search, #62133).
	WithJSONSearch(pointer string) (AgentMessageBoard, error)
	// SubscribedChannels lists the caller's live channel memberships.
	SubscribedChannels(ctx context.Context, caller string, page PageRequest) (*Page[string], error)
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
// The JSON tags mirror Rust's serde field names (#48077 added the derives so a
// host can serialize these request types).
type CreateChannelRequest struct {
	ChannelName string `json:"channel_name"`
	// Description is the optional channel purpose, at most 512 UTF-8 bytes.
	Description  *string            `json:"description,omitempty"`
	Subscription SubscriptionChange `json:"subscription"`
}

// UpsertChannelRequest creates a channel or updates only its navigation
// description (Rust #56494). Updating an existing channel never changes its
// posts, author, creation time, or subscriptions.
type UpsertChannelRequest struct {
	ChannelName string `json:"channel_name"`
	Description string `json:"description"`
}

// channelDescriptionMaxBytes mirrors Rust's ChannelDescription bound.
const channelDescriptionMaxBytes = 512

func validateChannelDescription(description string) error {
	if len(description) > channelDescriptionMaxBytes {
		return invalid("channel descriptions must contain at most 512 UTF-8 bytes")
	}
	return nil
}

func validateOptionalChannelDescription(description *string) error {
	if description == nil {
		return nil
	}
	return validateChannelDescription(*description)
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

// AppendRequest writes without subscribing its author or notifying anyone.
// Its request ID shares the caller's post retry namespace (Rust #56461).
type AppendRequest struct {
	// RequestID is the host-generated tool invocation identity, never a model
	// argument.
	RequestID   string
	Destination PostDestination
	Text        string
}

// postBehavior distinguishes a discussion post from a silent append on the
// shared write path (Rust's PostBehavior).
type postBehavior int

const (
	postDiscussion postBehavior = iota
	postSilentAppend
	// postSilentAppendWithSubscribers is a silent append for a consumer that
	// needs the subscribers present at insertion (Rust #62135).
	postSilentAppendWithSubscribers
)

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
	MessageID   string `json:"message_id"`
	OffsetChars int    `json:"offset_chars"`
	LimitChars  int    `json:"limit_chars"`
}

// SubscriptionTarget is a channel (new roots) or a thread (replies). Its JSON
// form is Rust's externally tagged enum (`{"Channel":"name"}` or
// `{"Thread":"<uuid>"}`), which is also the key the local backend stores.
type SubscriptionTarget struct {
	Kind        string // "channel" or "thread"
	ChannelName string
	ThreadID    string
}

func (t SubscriptionTarget) MarshalJSON() ([]byte, error) {
	switch t.Kind {
	case "channel":
		return json.Marshal(map[string]string{"Channel": t.ChannelName})
	case "thread":
		return json.Marshal(map[string]string{"Thread": t.ThreadID})
	default:
		return nil, invalid("subscription target is required")
	}
}

func (t *SubscriptionTarget) UnmarshalJSON(data []byte) error {
	if t == nil {
		return invalid("subscription target is required")
	}
	var raw map[string]string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	switch {
	case raw["Channel"] != "":
		*t = SubscriptionTarget{Kind: "channel", ChannelName: raw["Channel"]}
	case raw["Thread"] != "":
		*t = SubscriptionTarget{Kind: "thread", ThreadID: raw["Thread"]}
	default:
		return invalid("subscription target is required")
	}
	return nil
}

// SubscriptionRequest changes a subscription.
type SubscriptionRequest struct {
	Target SubscriptionTarget `json:"target"`
	// TargetAgent is nil to change the caller's own subscription.
	TargetAgent *agent.AgentPath   `json:"target_agent"`
	Change      SubscriptionChange `json:"change"`
}
