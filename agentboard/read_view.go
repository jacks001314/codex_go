package agentboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"codex_go/agent"
)

// ErrBoardReadViewReadOnly reports a mutation attempted on a fixed read view.
var ErrBoardReadViewReadOnly = errors.New("message board read view is read-only")

// ReadViewPosition is one insertion position inside a captured view: the
// database-wide post sequence and the caller-supplied request ID that produced
// it. Rust's `BoardReadView::appended_request_ids` returns `Vec<(i64, String)>`;
// the sequence is the cursor, not a timestamp.
type ReadViewPosition struct {
	Sequence  int64
	RequestID string
}

// BoardReadView is a fixed snapshot of one local board (Rust #59993). Capture
// reads the database's post sequence ceiling and the channel catalog, drops the
// read transaction immediately - nothing is held afterwards - and every later
// read is bounded by those two frozen values. Visibility follows the sequence,
// not the timestamp, so a row appended after the capture stays invisible even
// when it is backdated.
//
// Rust parity: codex-rs/message-board/src/read_view.rs and local/catalog.rs.
type BoardReadView struct {
	board   *LocalAgentMessageBoard
	cutoff  int64
	catalog []ChannelSummary
	names   map[string]struct{}
}

// boardReadScope is the frozen boundary a board query runs against: the post
// cutoff and the frozen channel roster. Board reads take a nil scope for the
// live board, which is Rust's `BoardMode::Fixed` carrying a `ReadView`.
type boardReadScope struct {
	cutoff   int64
	channels map[string]struct{}
}

// hasChannel reports whether the frozen catalog holds the channel. A nil scope
// is the live board and never filters.
func (s *boardReadScope) hasChannel(name string) bool {
	if s == nil {
		return true
	}
	_, ok := s.channels[name]
	return ok
}

// CaptureReadView freezes the board's catalog and its post cutoff.
func (b *LocalAgentMessageBoard) CaptureReadView(ctx context.Context) (*BoardReadView, error) {
	if b == nil {
		return nil, errors.New("message board is nil")
	}
	// seq is the database-wide AUTOINCREMENT key, so every future append is
	// above it (Rust `catalog::capture` reads the same unfiltered ceiling).
	var cutoff int64
	if err := b.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq),0) FROM posts").Scan(&cutoff); err != nil {
		return nil, err
	}
	catalog, err := b.captureChannelCatalog(ctx)
	if err != nil {
		return nil, err
	}
	names := make(map[string]struct{}, len(catalog))
	for _, channel := range catalog {
		names[channel.ChannelName] = struct{}{}
	}
	return &BoardReadView{board: b, cutoff: cutoff, catalog: catalog, names: names}, nil
}

// captureChannelCatalog snapshots every channel summary at capture time. The
// ordering is Rust's catalog order - most recent post activity first - so the
// frozen page keeps the newest-first direction without touching SQL.
func (b *LocalAgentMessageBoard) captureChannelCatalog(ctx context.Context) ([]ChannelSummary, error) {
	rows, err := b.db.QueryContext(ctx,
		"SELECT c.name,c.created_at,c.author,c.description,last.id AS last_message_id,"+
			"(SELECT COUNT(*) FROM posts p WHERE p.board=c.board AND p.channel=c.name) AS message_count "+
			"FROM channels c LEFT JOIN posts last ON last.seq=("+
			"SELECT p.seq FROM posts p WHERE p.board=c.board AND p.channel=c.name "+
			"ORDER BY p.timestamp DESC,p.seq DESC LIMIT 1) "+
			"WHERE c.board=? ORDER BY COALESCE(last.timestamp,c.timestamp) DESC,c.name DESC",
		b.identity)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	catalog := []ChannelSummary{}
	for rows.Next() {
		var name string
		var createdAt string
		var author string
		var description sql.NullString
		var messageCount int64
		var lastMessageID sql.NullString
		if err := rows.Scan(&name, &createdAt, &author, &description, &lastMessageID, &messageCount); err != nil {
			return nil, err
		}
		parsed, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, err
		}
		// A capture is read-only whatever access opened the board (Rust decodes
		// the catalog with BoardMode::FIXED_ACCESS).
		summary := ChannelSummary{
			Permissions:  &ChannelPermissions{Read: true},
			ChannelName:  name,
			CreatedAt:    parsed.UTC(),
			CreatedBy:    agent.AgentPath(author),
			MessageCount: int(messageCount),
		}
		if description.Valid {
			value := description.String
			summary.Description = &value
		}
		if lastMessageID.Valid {
			value := lastMessageID.String
			summary.LastMessageID = &value
		}
		catalog = append(catalog, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return catalog, nil
}

// scope returns the frozen boundary every board read uses.
func (v *BoardReadView) scope() *boardReadScope {
	if v == nil {
		return nil
	}
	return &boardReadScope{cutoff: v.cutoff, channels: v.names}
}

// Identity is the board identity the capture belongs to.
func (v *BoardReadView) Identity() string {
	if v == nil || v.board == nil {
		return ""
	}
	return v.board.Identity()
}

// StorageNamespace is the storage namespace shared by every handle of this
// capture.
func (v *BoardReadView) StorageNamespace() string {
	if v == nil || v.board == nil {
		return ""
	}
	return v.board.namespace
}

// Cutoff is the highest post sequence this view can see.
func (v *BoardReadView) Cutoff() int64 {
	if v == nil {
		return 0
	}
	return v.cutoff
}

// Channels is the frozen channel roster in name order.
func (v *BoardReadView) Channels() []string {
	if v == nil {
		return nil
	}
	names := make([]string, 0, len(v.catalog))
	for _, channel := range v.catalog {
		names = append(names, channel.ChannelName)
	}
	sort.Strings(names)
	return names
}

// ChannelCatalog is the frozen catalog in capture order, including the
// per-channel counts and last message IDs fixed at capture time.
func (v *BoardReadView) ChannelCatalog() []ChannelSummary {
	if v == nil {
		return nil
	}
	return append([]ChannelSummary{}, v.catalog...)
}

// ListChannels pages the frozen catalog. No SQL runs against the channels table
// because the catalog - not the live roster - defines membership, and a channel
// created after the capture never appears.
func (v *BoardReadView) ListChannels(ctx context.Context, caller string, query ChannelQuery) (*ChannelPage, error) {
	if v == nil {
		return nil, ErrBoardReadViewReadOnly
	}
	if _, err := v.board.host.AgentPath(ctx, caller); err != nil {
		return nil, err
	}
	offset, limit, err := boardWindow(query.Page)
	if err != nil {
		return nil, err
	}
	needle := ""
	if query.Query != nil {
		needle = foldCase(*query.Query)
	}
	ordered := make([]ChannelSummary, 0, len(v.catalog))
	if query.Direction == OldestFirst {
		for index := len(v.catalog) - 1; index >= 0; index-- {
			ordered = append(ordered, v.catalog[index])
		}
	} else {
		ordered = append(ordered, v.catalog...)
	}
	matched := make([]ChannelSummary, 0, len(ordered))
	for _, channel := range ordered {
		if !strings.Contains(foldCase(channel.ChannelName), needle) {
			continue
		}
		matched = append(matched, channel)
	}
	if uint64(offset) > uint64(len(matched)) {
		matched = matched[:0]
	} else {
		matched = matched[offset:]
	}
	if len(matched) > limit+1 {
		matched = matched[:limit+1]
	}
	page, err := finishBoardPage(offset, limit, matched)
	if err != nil {
		return nil, err
	}
	// A capture can never create a channel, edit metadata or manage permissions
	// (Rust ReadView::list_channels).
	return &ChannelPage{Page: *page, BoardPermissions: &BoardPermissions{}}, nil
}

// ListThreads lists a channel's threads within the capture.
func (v *BoardReadView) ListThreads(ctx context.Context, caller string, query ThreadQuery) (*Page[ThreadSummary], error) {
	if v == nil {
		return nil, ErrBoardReadViewReadOnly
	}
	return v.board.listThreadsAt(ctx, caller, query, v.scope())
}

// SearchPosts searches the posts visible at capture.
func (v *BoardReadView) SearchPosts(ctx context.Context, caller string, query PostQuery) (*Page[PostPreview], error) {
	if v == nil {
		return nil, ErrBoardReadViewReadOnly
	}
	return v.board.searchPostsAt(ctx, caller, query, v.scope())
}

// ReadThread reads one thread that was visible at capture.
func (v *BoardReadView) ReadThread(ctx context.Context, caller string, request ReadThreadRequest) (*ThreadPage, error) {
	if v == nil {
		return nil, ErrBoardReadViewReadOnly
	}
	return v.board.readThreadAt(ctx, caller, request, v.scope())
}

// ReadPost serves one post that was already visible at capture. Content is
// re-read from the live board because posts are immutable; only membership is
// decided by the frozen cutoff.
func (v *BoardReadView) ReadPost(ctx context.Context, caller string, request ReadPostRequest) (*PostContent, error) {
	if v == nil {
		return nil, ErrBoardReadViewReadOnly
	}
	return v.board.readPostAt(ctx, caller, request, v.scope())
}

// ReadPostTexts returns the whole bodies of the given positions, in the
// requested order. A position appended after the capture - and a deleted post -
// is reported as not found, and one page is the most a single call may ask for
// (Rust BoardReadView::read_post_texts).
func (v *BoardReadView) ReadPostTexts(ctx context.Context, caller string, positions []int64) ([]string, error) {
	if v == nil {
		return nil, ErrBoardReadViewReadOnly
	}
	if _, err := v.board.host.AgentPath(ctx, caller); err != nil {
		return nil, err
	}
	if len(positions) > DefaultPageLimit {
		return nil, invalid("requested posts exceed one board page")
	}
	texts := make([]string, 0, len(positions))
	for _, position := range positions {
		var payload string
		err := v.board.db.QueryRowContext(ctx,
			"SELECT payload FROM posts WHERE seq=? AND board=? AND seq<=?",
			position, v.board.identity, v.cutoff).Scan(&payload)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, invalid("post not found in this board view")
		}
		if err != nil {
			return nil, err
		}
		var post storedPost
		if err := json.Unmarshal([]byte(payload), &post); err != nil {
			return nil, err
		}
		texts = append(texts, post.Text)
	}
	return texts, nil
}

// AppendedRequestIDs returns one page of insertion positions after the given
// sequence, in ascending order, with the caller prefix stripped from the
// persisted retry key. Zero precedes the first post; a cursor beyond the
// capture is rejected rather than served from the live board.
func (v *BoardReadView) AppendedRequestIDs(ctx context.Context, caller string, channel string, after int64) ([]ReadViewPosition, error) {
	if v == nil {
		return nil, ErrBoardReadViewReadOnly
	}
	if _, err := v.board.host.AgentPath(ctx, caller); err != nil {
		return nil, err
	}
	if after < 0 || after > v.cutoff {
		return nil, invalid("post cursor is outside the captured board view")
	}
	rows, err := v.board.db.QueryContext(ctx,
		"SELECT seq, substr(request_id, instr(request_id, ':') + 1) FROM posts "+
			"WHERE board=? AND channel=? AND seq>? AND seq<=? ORDER BY seq LIMIT ?",
		v.board.identity, channel, after, v.cutoff, int64(DefaultPageLimit))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	positions := []ReadViewPosition{}
	for rows.Next() {
		var position ReadViewPosition
		if err := rows.Scan(&position.Sequence, &position.RequestID); err != nil {
			return nil, err
		}
		positions = append(positions, position)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return positions, nil
}

// CreateChannel is rejected: a read view never mutates the board.
func (v *BoardReadView) CreateChannel(context.Context, string, CreateChannelRequest) (*ChannelSummary, error) {
	return nil, ErrBoardReadViewReadOnly
}

// UpsertChannel is rejected: a read view never mutates the board.
func (v *BoardReadView) UpsertChannel(context.Context, string, UpsertChannelRequest) error {
	return ErrBoardReadViewReadOnly
}

// Post is rejected: a read view never mutates the board.
func (v *BoardReadView) Post(context.Context, string, PostRequest) (*PostMetadata, error) {
	return nil, ErrBoardReadViewReadOnly
}

// Append is rejected: a read view never mutates the board.
func (v *BoardReadView) Append(context.Context, string, AppendRequest) (*PostMetadata, error) {
	return nil, ErrBoardReadViewReadOnly
}

// SetSubscription is rejected: a read view never mutates the board.
func (v *BoardReadView) SetSubscription(context.Context, string, SubscriptionRequest) (*SubscriptionState, error) {
	return nil, ErrBoardReadViewReadOnly
}

// SetSubscriptions is rejected: a read view never mutates the board.
func (v *BoardReadView) SetSubscriptions(context.Context, string, BulkSubscriptionsRequest) (*BulkSubscriptionsState, error) {
	return nil, ErrBoardReadViewReadOnly
}
