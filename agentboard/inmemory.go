package agentboard

// Tree-owned message boards for training. Agent handles share state; nothing is
// written to disk. Mutations and recipient selection are atomic, and host
// callbacks run outside the state lock. Opening a board prunes registry entries
// whose state has been released.
//
// Rust parity: codex-rs/ext/agent-message-board/src/in_memory.rs and
// in_memory/queries.rs.

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/cases"

	"codex_go/agent"
)

const (
	maxPostTextBytes        = 64 * 1024
	maxChannelNameBytes     = 128
	maxRequestIDBytes       = 512
	maxAgentsToNotify       = 256
	maxReadPostChars        = 20_000
	outputBudgetChars       = 20_000
	previewChars            = 150
	notificationFanoutLimit = 16
)

// InMemoryMessageBoards shares board state across a tree while giving each agent
// its own host handle.
type InMemoryMessageBoards struct {
	mu     sync.Mutex
	states map[string]*boardStateRef
}

type boardStateRef struct {
	state *boardState
	refs  int
}

// Open returns a board handle for an identity, creating or reusing the tree's
// shared state. Entries whose handles were released are pruned.
func (b *InMemoryMessageBoards) Open(identity string, host Host) *InMemoryBoard {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.states == nil {
		b.states = map[string]*boardStateRef{}
	}
	ref, ok := b.states[identity]
	if !ok || ref.refs == 0 {
		ref = &boardStateRef{state: newBoardState()}
		b.states[identity] = ref
	}
	ref.refs++
	return &InMemoryBoard{identity: identity, host: host, registry: b, ref: ref}
}

// Len reports how many live board identities the registry retains.
func (b *InMemoryMessageBoards) Len() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	count := 0
	for _, ref := range b.states {
		if ref.refs > 0 {
			count++
		}
	}
	return count
}

func (b *InMemoryMessageBoards) release(ref *boardStateRef) {
	if b == nil || ref == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if ref.refs > 0 {
		ref.refs--
	}
	if ref.refs == 0 {
		for identity, candidate := range b.states {
			if candidate == ref {
				delete(b.states, identity)
			}
		}
	}
}

// Close releases this handle; the shared state survives while other handles
// remain.
func (b *InMemoryBoard) Close() {
	if b == nil {
		return
	}
	b.registry.release(b.ref)
}

// InMemoryBoard is a board held in memory for one rollout.
type InMemoryBoard struct {
	identity string
	host     Host
	registry *InMemoryMessageBoards
	ref      *boardStateRef
}

type boardState struct {
	mu            sync.Mutex
	channels      map[string]*boardChannel
	posts         []*boardPost
	byID          map[string]int
	requests      map[string]int
	subscriptions map[SubscriptionTarget]map[string]SubscriptionChange
}

type boardChannel struct {
	summary ChannelSummary
	search  string
	posts   []int
	roots   []int
}

type boardPost struct {
	metadata    PostMetadata
	request     PostRequest
	search      string
	nChars      int
	replies     []int
	latestReply *int
}

func newBoardState() *boardState {
	return &boardState{
		channels:      map[string]*boardChannel{},
		byID:          map[string]int{},
		requests:      map[string]int{},
		subscriptions: map[SubscriptionTarget]map[string]SubscriptionChange{},
	}
}

// Identity returns the board's session identity.
func (b *InMemoryBoard) Identity() string {
	if b == nil {
		return ""
	}
	return b.identity
}

func (s *boardState) postIndex(id string) (int, error) {
	index, ok := s.byID[id]
	if !ok {
		return 0, invalid("post not found in this board")
	}
	return index, nil
}

func (s *boardState) threadIndex(id string) (int, error) {
	index, err := s.postIndex(id)
	if err != nil {
		return 0, err
	}
	if s.posts[index].metadata.ThreadID != id {
		return 0, invalid("thread_id must identify a top-level post")
	}
	return index, nil
}

func (s *boardState) key(index int) (int64, int) {
	return s.posts[index].metadata.CreatedAt.UnixMicro(), index
}

func (s *boardState) existingPost(caller string, request PostRequest) (*PostMetadata, bool, error) {
	index, ok := s.requests[requestKey(caller, request.RequestID)]
	if !ok {
		return nil, false, nil
	}
	post := s.posts[index]
	if !postRequestsEqual(post.request, request) {
		return nil, false, invalid("request ID was already used for a different post")
	}
	metadata := post.metadata
	return &metadata, true, nil
}

func requestKey(caller string, requestID string) string {
	return caller + "\x00" + requestID
}

func postRequestsEqual(left, right PostRequest) bool {
	if left.RequestID != right.RequestID ||
		left.Destination != right.Destination ||
		left.Text != right.Text ||
		len(left.AgentsToNotify) != len(right.AgentsToNotify) {
		return false
	}
	for i := range left.AgentsToNotify {
		if left.AgentsToNotify[i] != right.AgentsToNotify[i] {
			return false
		}
	}
	return true
}

func (s *boardState) insertChannel(name string, author agent.AgentPath, now time.Time) error {
	if err := validateChannelName(name); err != nil {
		return err
	}
	if _, ok := s.channels[name]; ok {
		return invalid("channel already exists")
	}
	s.channels[name] = &boardChannel{
		summary: ChannelSummary{
			ChannelName: name,
			CreatedAt:   now,
			CreatedBy:   author,
		},
		search: foldCase(name),
	}
	return nil
}

func (s *boardState) subscribe(target SubscriptionTarget, caller string) {
	subscribers, ok := s.subscriptions[target]
	if !ok {
		subscribers = map[string]SubscriptionChange{}
		s.subscriptions[target] = subscribers
	}
	if _, ok := subscribers[caller]; !ok {
		subscribers[caller] = Subscribe
	}
}

func containsControlChar(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// foldCase mirrors caseless::default_case_fold_str for search matching.
func foldCase(value string) string {
	return cases.Fold().String(value)
}

func invalid(message string) error {
	return &InvalidRequestError{Message: message}
}

// InvalidRequestError mirrors Rust's CodexErr::InvalidRequest for board
// validation failures.
type InvalidRequestError struct {
	Message string
}

func (e *InvalidRequestError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// IsInvalidRequest reports whether err is a board validation failure.
func IsInvalidRequest(err error) bool {
	var target *InvalidRequestError
	return errors.As(err, &target)
}

// CreateChannel creates a channel and optionally subscribes the caller.
func (b *InMemoryBoard) CreateChannel(ctx context.Context, caller string, request CreateChannelRequest) (*ChannelSummary, error) {
	author, err := b.host.AgentPath(ctx, caller)
	if err != nil {
		return nil, err
	}
	now, err := b.host.CurrentTime(ctx, caller)
	if err != nil {
		return nil, err
	}
	state := b.ref.state
	state.mu.Lock()
	defer state.mu.Unlock()
	if err := state.insertChannel(request.ChannelName, author, now); err != nil {
		return nil, err
	}
	if request.Subscription == Subscribe {
		state.subscribe(SubscriptionTarget{Kind: "channel", ChannelName: request.ChannelName}, caller)
	}
	summary := state.channels[request.ChannelName].summary
	return &summary, nil
}

// Post creates a post or reply and fans the notification out to recipients.
func (b *InMemoryBoard) Post(ctx context.Context, caller string, request PostRequest) (*PostMetadata, error) {
	if err := validatePostRequest(request); err != nil {
		return nil, err
	}
	author, err := b.host.AgentPath(ctx, caller)
	if err != nil {
		return nil, err
	}
	state := b.ref.state
	state.mu.Lock()
	if existing, ok, err := state.existingPost(caller, request); err != nil {
		state.mu.Unlock()
		return nil, err
	} else if ok {
		state.mu.Unlock()
		return existing, nil
	}
	state.mu.Unlock()

	recipients := map[string]struct{}{}
	for _, path := range request.AgentsToNotify {
		resolved, err := b.host.ResolveAgent(ctx, path)
		if err != nil {
			return nil, err
		}
		recipients[resolved] = struct{}{}
	}
	now, err := b.host.CurrentTime(ctx, caller)
	if err != nil {
		return nil, err
	}

	state.mu.Lock()
	if existing, ok, err := state.existingPost(caller, request); err != nil {
		state.mu.Unlock()
		return nil, err
	} else if ok {
		state.mu.Unlock()
		return existing, nil
	}
	id, err := newBoardPostID()
	if err != nil {
		state.mu.Unlock()
		return nil, err
	}
	channelName, root, target, err := state.resolveDestination(request.Destination, author, now, caller, id)
	if err != nil {
		state.mu.Unlock()
		return nil, err
	}
	if subscribers, ok := state.subscriptions[target]; ok {
		for member, change := range subscribers {
			if change == Subscribe {
				recipients[member] = struct{}{}
			}
		}
	}
	delete(recipients, caller)
	metadata := PostMetadata{
		MessageID:   id,
		ChannelName: channelName,
		Author:      author,
		ThreadID:    root,
		CreatedAt:   now,
	}
	index := len(state.posts)
	state.requests[requestKey(caller, request.RequestID)] = index
	state.byID[id] = index
	state.posts = append(state.posts, &boardPost{
		metadata: metadata,
		request:  request,
		search:   foldCase(request.Text),
		nChars:   len([]rune(request.Text)),
	})
	channel := state.channels[channelName]
	newest := channel.summary.LastMessageID == nil || state.keyGreater(index, *channel.summary.LastMessageID)
	channel.posts = append(channel.posts, index)
	channel.summary.MessageCount++
	if newest {
		messageID := id
		channel.summary.LastMessageID = &messageID
	}
	if root == id {
		channel.roots = append(channel.roots, index)
	} else {
		rootIndex := state.byID[root]
		thread := state.posts[rootIndex]
		replyNewest := thread.latestReply == nil || state.keyGreater(index, state.posts[*thread.latestReply].metadata.MessageID)
		thread.replies = append(thread.replies, index)
		if replyNewest {
			replyIndex := index
			thread.latestReply = &replyIndex
		}
	}
	state.subscribe(SubscriptionTarget{Kind: "thread", ThreadID: root}, caller)
	if len(recipients) == 0 {
		// Rust #48072: a post with no recipients skips the notification preview.
		state.mu.Unlock()
		return &metadata, nil
	}
	preview := state.posts[index].preview(previewChars)
	state.mu.Unlock()

	notifyRecipients(ctx, b.host, recipients, preview)
	return &metadata, nil
}

func (s *boardState) keyGreater(index int, messageID string) bool {
	other, ok := s.byID[messageID]
	if !ok {
		return true
	}
	created, sequence := s.key(index)
	otherCreated, otherSequence := s.key(other)
	if created != otherCreated {
		return created > otherCreated
	}
	return sequence > otherSequence
}

func (s *boardState) resolveDestination(destination PostDestination, author agent.AgentPath, now time.Time, caller string, id string) (string, string, SubscriptionTarget, error) {
	switch destination.Kind {
	case "channel":
		if _, ok := s.channels[destination.Name]; !ok {
			return "", "", SubscriptionTarget{}, invalid("channel not found in this board")
		}
		return destination.Name, id, SubscriptionTarget{Kind: "channel", ChannelName: destination.Name}, nil
	case "new_channel":
		if err := s.insertChannel(destination.Name, author, now); err != nil {
			return "", "", SubscriptionTarget{}, err
		}
		s.subscribe(SubscriptionTarget{Kind: "channel", ChannelName: destination.Name}, caller)
		return destination.Name, id, SubscriptionTarget{Kind: "channel", ChannelName: destination.Name}, nil
	case "thread":
		index, err := s.threadIndex(destination.ThreadID)
		if err != nil {
			return "", "", SubscriptionTarget{}, err
		}
		root := s.posts[index]
		return root.metadata.ChannelName, destination.ThreadID, SubscriptionTarget{Kind: "thread", ThreadID: destination.ThreadID}, nil
	default:
		return "", "", SubscriptionTarget{}, invalid("post destination is required")
	}
}

func validatePostRequest(request PostRequest) error {
	if request.Text == "" || len(request.Text) > maxPostTextBytes ||
		request.RequestID == "" || len(request.RequestID) > maxRequestIDBytes ||
		len(request.AgentsToNotify) > maxAgentsToNotify {
		return invalid("post text, request ID or recipient count exceeds the board limits")
	}
	return nil
}

func notifyRecipients(ctx context.Context, host Host, recipients map[string]struct{}, preview PostPreview) {
	ids := make([]string, 0, len(recipients))
	for id := range recipients {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	semaphore := make(chan struct{}, notificationFanoutLimit)
	var wait sync.WaitGroup
	for _, id := range ids {
		wait.Add(1)
		semaphore <- struct{}{}
		go func(recipient string) {
			defer wait.Done()
			defer func() { <-semaphore }()
			// Delivery failures are reported to the caller's logs by the host.
			_, _ = host.Notify(ctx, recipient, preview)
		}(id)
	}
	wait.Wait()
}

func (p *boardPost) preview(maxChars int) PostPreview {
	text := []rune(p.request.Text)
	limit := maxChars
	if limit > len(text) {
		limit = len(text)
	}
	return PostPreview{
		PostMetadata: p.metadata,
		TextPreview:  string(text[:limit]),
		NChars:       p.nChars,
		Truncated:    p.nChars > maxChars,
	}
}

// SetSubscription changes a channel or thread subscription.
func (b *InMemoryBoard) SetSubscription(ctx context.Context, caller string, request SubscriptionRequest) (*SubscriptionState, error) {
	callerPath, err := b.host.AgentPath(ctx, caller)
	if err != nil {
		return nil, err
	}
	targetPath := callerPath
	if request.TargetAgent != nil {
		targetPath = *request.TargetAgent
	}
	targetAgent, err := b.host.ResolveAgent(ctx, targetPath)
	if err != nil {
		return nil, err
	}
	state := b.ref.state
	state.mu.Lock()
	defer state.mu.Unlock()
	var channelName string
	var threadID *string
	var lastMessageID *string
	switch request.Target.Kind {
	case "channel":
		channel, ok := state.channels[request.Target.ChannelName]
		if !ok {
			return nil, invalid("channel not found in this board")
		}
		channelName = request.Target.ChannelName
		lastMessageID = channel.summary.LastMessageID
	case "thread":
		index, err := state.threadIndex(request.Target.ThreadID)
		if err != nil {
			return nil, err
		}
		post := state.posts[index]
		last := index
		if post.latestReply != nil && state.keyGreater(*post.latestReply, post.metadata.MessageID) {
			last = *post.latestReply
		}
		thread := post.metadata.MessageID
		threadID = &thread
		channelName = post.metadata.ChannelName
		messageID := state.posts[last].metadata.MessageID
		lastMessageID = &messageID
	default:
		return nil, invalid("subscription target is required")
	}
	subscribers, ok := state.subscriptions[request.Target]
	if !ok {
		subscribers = map[string]SubscriptionChange{}
		state.subscriptions[request.Target] = subscribers
	}
	subscribers[targetAgent] = request.Change
	return &SubscriptionState{
		ChannelName:   channelName,
		ThreadID:      threadID,
		TargetAgent:   targetPath,
		Enabled:       request.Change == Subscribe,
		LastMessageID: lastMessageID,
	}, nil
}

// ReadPost reads a bounded slice of one post; offsets count Unicode characters.
func (b *InMemoryBoard) ReadPost(ctx context.Context, caller string, request ReadPostRequest) (*PostContent, error) {
	if _, err := b.host.AgentPath(ctx, caller); err != nil {
		return nil, err
	}
	state := b.ref.state
	state.mu.Lock()
	defer state.mu.Unlock()
	index, err := state.postIndex(request.MessageID)
	if err != nil {
		return nil, err
	}
	post := state.posts[index]
	offset := request.OffsetChars
	if offset > post.nChars {
		offset = post.nChars
	}
	if offset < 0 {
		offset = 0
	}
	limit := request.LimitChars
	if limit <= 0 || limit > maxReadPostChars {
		limit = maxReadPostChars
	}
	text := []rune(post.request.Text)
	end := offset + limit
	if end > len(text) {
		end = len(text)
	}
	slice := string(text[offset:end])
	return &PostContent{
		PostMetadata:    post.metadata,
		Text:            slice,
		NChars:          post.nChars,
		NextOffsetChars: offset + len([]rune(slice)),
	}, nil
}

// ListChannels lists or searches channels, most recently active first by
// default.
func (b *InMemoryBoard) ListChannels(ctx context.Context, caller string, query ChannelQuery) (*Page[ChannelSummary], error) {
	if _, err := b.host.AgentPath(ctx, caller); err != nil {
		return nil, err
	}
	search := ""
	if query.Query != nil {
		search = foldCase(*query.Query)
	}
	state := b.ref.state
	state.mu.Lock()
	defer state.mu.Unlock()
	channels := make([]*boardChannel, 0, len(state.channels))
	for _, channel := range state.channels {
		if !strings.Contains(channel.search, search) {
			continue
		}
		channels = append(channels, channel)
	}
	sort.SliceStable(channels, func(i int, j int) bool {
		leftActivity := channels[i].summary.CreatedAt.UnixMicro()
		if channels[i].summary.LastMessageID != nil {
			if index, ok := state.byID[*channels[i].summary.LastMessageID]; ok {
				leftActivity, _ = state.key(index)
			}
		}
		rightActivity := channels[j].summary.CreatedAt.UnixMicro()
		if channels[j].summary.LastMessageID != nil {
			if index, ok := state.byID[*channels[j].summary.LastMessageID]; ok {
				rightActivity, _ = state.key(index)
			}
		}
		if leftActivity != rightActivity {
			return leftActivity < rightActivity
		}
		return channels[i].summary.ChannelName < channels[j].summary.ChannelName
	})
	if query.Direction == NewestFirst {
		reverse(channels)
	}
	page, err := pageItems(query.Page, channels)
	if err != nil {
		return nil, err
	}
	results := make([]ChannelSummary, 0, len(page.Results))
	for _, channel := range page.Results {
		results = append(results, channel.summary)
	}
	return &Page[ChannelSummary]{Results: results, NextCursor: page.NextCursor}, nil
}

// ListThreads lists a channel's threads.
func (b *InMemoryBoard) ListThreads(ctx context.Context, caller string, query ThreadQuery) (*Page[ThreadSummary], error) {
	if _, err := b.host.AgentPath(ctx, caller); err != nil {
		return nil, err
	}
	state := b.ref.state
	state.mu.Lock()
	defer state.mu.Unlock()
	channel, ok := state.channels[query.ChannelName]
	if !ok {
		return nil, invalid("channel not found in this board")
	}
	roots := append([]int(nil), channel.roots...)
	sort.SliceStable(roots, func(i int, j int) bool {
		leftCreated, leftSequence := state.key(roots[i])
		rightCreated, rightSequence := state.key(roots[j])
		leftTimestamp := leftCreated
		rightTimestamp := rightCreated
		if query.Sort == ThreadSortActivity {
			if reply := state.posts[roots[i]].latestReply; reply != nil {
				replyCreated, _ := state.key(*reply)
				if replyCreated > leftTimestamp {
					leftTimestamp = replyCreated
				}
			}
			if reply := state.posts[roots[j]].latestReply; reply != nil {
				replyCreated, _ := state.key(*reply)
				if replyCreated > rightTimestamp {
					rightTimestamp = replyCreated
				}
			}
		}
		if leftTimestamp != rightTimestamp {
			return leftTimestamp < rightTimestamp
		}
		return leftSequence < rightSequence
	})
	if query.Direction == NewestFirst {
		reverse(roots)
	}
	page, err := pageItems(query.Page, roots)
	if err != nil {
		return nil, err
	}
	chars := threadPreviewChars(query.MaxCharsPerPost, len(page.Results))
	results := make([]ThreadSummary, 0, len(page.Results))
	for _, index := range page.Results {
		root := state.posts[index]
		var latest *PostPreview
		lastActivity := root.metadata.CreatedAt
		if root.latestReply != nil {
			reply := state.posts[*root.latestReply]
			preview := reply.preview(chars)
			latest = &preview
			if reply.metadata.CreatedAt.After(lastActivity) {
				lastActivity = reply.metadata.CreatedAt
			}
		}
		results = append(results, ThreadSummary{
			ThreadID:       root.metadata.MessageID,
			RootPost:       root.preview(chars),
			ReplyCount:     len(root.replies),
			LastActivityAt: lastActivity,
			LatestReply:    latest,
		})
	}
	return &Page[ThreadSummary]{Results: results, NextCursor: page.NextCursor}, nil
}

func threadPreviewChars(requested int, count int) int {
	if count < 1 {
		count = 1
	}
	return minPositive(requested, outputBudgetChars/(2*count))
}

// SearchPosts searches top-level posts and replies, newest first.
func (b *InMemoryBoard) SearchPosts(ctx context.Context, caller string, query PostQuery) (*Page[PostPreview], error) {
	if _, err := b.host.AgentPath(ctx, caller); err != nil {
		return nil, err
	}
	state := b.ref.state
	state.mu.Lock()
	defer state.mu.Unlock()
	var after *[2]int64
	if query.AfterMessageID != nil {
		index, err := state.postIndex(*query.AfterMessageID)
		if err != nil {
			return nil, err
		}
		created, sequence := state.key(index)
		after = &[2]int64{created, int64(sequence)}
	}
	var search *string
	if query.Query != nil {
		folded := foldCase(*query.Query)
		search = &folded
	}
	var posts []int
	if query.ChannelName != nil {
		if channel, ok := state.channels[*query.ChannelName]; ok {
			posts = append(posts, channel.posts...)
		}
	} else {
		for index := range state.posts {
			posts = append(posts, index)
		}
	}
	filtered := posts[:0]
	for _, index := range posts {
		post := state.posts[index]
		if query.Author != nil && *query.Author != post.metadata.Author {
			continue
		}
		if search != nil && !strings.Contains(post.search, *search) {
			continue
		}
		if after != nil {
			created, sequence := state.key(index)
			if created < after[0] || (created == after[0] && int64(sequence) <= after[1]) {
				continue
			}
		}
		filtered = append(filtered, index)
	}
	posts = filtered
	sort.SliceStable(posts, func(i int, j int) bool {
		leftCreated, leftSequence := state.key(posts[i])
		rightCreated, rightSequence := state.key(posts[j])
		if leftCreated != rightCreated {
			return leftCreated > rightCreated
		}
		return leftSequence > rightSequence
	})
	page, err := pageItems(query.Page, posts)
	if err != nil {
		return nil, err
	}
	chars := searchPreviewChars(query.MaxCharsPerPost, len(page.Results))
	results := make([]PostPreview, 0, len(page.Results))
	for _, index := range page.Results {
		results = append(results, state.posts[index].preview(chars))
	}
	return &Page[PostPreview]{Results: results, NextCursor: page.NextCursor}, nil
}

func searchPreviewChars(requested int, count int) int {
	if count < 1 {
		count = 1
	}
	return minPositive(requested, outputBudgetChars/count)
}

// ReadThread reads a thread: the root post plus a page of newest replies.
func (b *InMemoryBoard) ReadThread(ctx context.Context, caller string, request ReadThreadRequest) (*ThreadPage, error) {
	if _, err := b.host.AgentPath(ctx, caller); err != nil {
		return nil, err
	}
	state := b.ref.state
	state.mu.Lock()
	defer state.mu.Unlock()
	index, err := state.threadIndex(request.ThreadID)
	if err != nil {
		return nil, err
	}
	root := state.posts[index]
	replies := append([]int(nil), root.replies...)
	sort.SliceStable(replies, func(i int, j int) bool {
		leftCreated, leftSequence := state.key(replies[i])
		rightCreated, rightSequence := state.key(replies[j])
		if leftCreated != rightCreated {
			return leftCreated > rightCreated
		}
		return leftSequence > rightSequence
	})
	page, err := pageItems(request.Page, replies)
	if err != nil {
		return nil, err
	}
	chars := minPositive(request.MaxCharsPerPost, outputBudgetChars/(len(page.Results)+1))
	results := make([]PostPreview, 0, len(page.Results))
	for _, replyIndex := range page.Results {
		results = append(results, state.posts[replyIndex].preview(chars))
	}
	return &ThreadPage{
		RootPost: root.preview(chars),
		Replies:  Page[PostPreview]{Results: results, NextCursor: page.NextCursor},
	}, nil
}

func minPositive(requested int, budget int) int {
	limit := requested
	if limit <= 0 || limit > budget {
		limit = budget
	}
	if limit < 1 {
		limit = 1
	}
	return limit
}

func reverse[T any](values []T) {
	for i, j := 0, len(values)-1; i < j; i, j = i+1, j-1 {
		values[i], values[j] = values[j], values[i]
	}
}

// pageItems applies Rust's page(): a base64url 4-byte big-endian offset cursor,
// a hard 50-item cap and a one-item lookahead for has_more.
func pageItems[T any](request PageRequest, results []T) (*Page[T], error) {
	offset, limit, err := boardWindow(request)
	if err != nil {
		return nil, err
	}
	if int(offset) > len(results) {
		offset = uint32(len(results))
	}
	return finishBoardPage(offset, limit, results[offset:])
}

// decodePageCursor decodes Rust's Window::new cursor: four base64url-encoded
// big-endian offset bytes, or zero when the query has no cursor.
func decodePageCursor(cursor *string) (uint32, error) {
	if cursor == nil {
		return 0, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(*cursor))
	if err != nil || len(decoded) != 4 {
		return 0, invalid("invalid cursor")
	}
	return binary.BigEndian.Uint32(decoded), nil
}

func base64PageCursor(offset uint32) string {
	encoded := make([]byte, 4)
	binary.BigEndian.PutUint32(encoded, offset)
	return base64.RawURLEncoding.EncodeToString(encoded)
}

var _ Board = (*InMemoryBoard)(nil)
