package agentboard

// SQLite-backed boards. The tree ID scopes every read and write.
//
// Rust parity: codex-rs/ext/agent-message-board/src/local.rs and its
// local/{queries,paging,lifecycle}.rs modules. Immediate transactions serialize
// mutations across independently opened handles; live handles in this process
// share one connection pool per database path. Accepted posts survive runtime
// unload and process restart, but cannot recreate a board after its root has
// been permanently deleted.
//
// The stored JSON mirrors Rust's serde shapes so a Rust binary can read a
// Go-written board: the `payload` column holds `{"metadata":...,"text":...}`,
// the `request` column holds serde's `PostRequest`, and a subscription target
// key holds serde's externally tagged `SubscriptionTarget`
// (`{"Channel":"work"}` or `{"Thread":"<uuid>"}`).

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"codex_go/agent"
	"codex_go/state"
)

// AgentMessageBoardDatabaseFile is the board database inside the SQLite home.
const AgentMessageBoardDatabaseFile = "agent_message_board_1.sqlite"

// localBoardTimestampFormat matches chrono's to_rfc3339 for a UTC timestamp, so
// a Rust binary reads a Go-written row byte-for-byte. The `-07:00` layout is
// required: Go's `Z07:00` would emit `Z`, while chrono's to_rfc3339 always
// writes the numeric offset (`+00:00`).
const localBoardTimestampFormat = "2006-01-02T15:04:05.999999999-07:00"

// agentMessageBoardSchema mirrors Rust's SCHEMA verbatim.
const agentMessageBoardSchema = `
CREATE TABLE IF NOT EXISTS deleted_boards (board TEXT PRIMARY KEY NOT NULL);
CREATE TABLE IF NOT EXISTS channels (
 board TEXT NOT NULL, name TEXT NOT NULL, name_search TEXT NOT NULL, created_at TEXT NOT NULL, timestamp INTEGER NOT NULL, author TEXT NOT NULL,
 PRIMARY KEY(board,name)
);
CREATE TABLE IF NOT EXISTS posts (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 board TEXT NOT NULL, id TEXT NOT NULL, channel TEXT NOT NULL, root TEXT NOT NULL,
 author TEXT NOT NULL, timestamp INTEGER NOT NULL, body_search TEXT NOT NULL,
 payload TEXT NOT NULL, request_id TEXT NOT NULL, request TEXT NOT NULL,
 UNIQUE(board,id), UNIQUE(board,request_id)
);
CREATE INDEX IF NOT EXISTS posts_board_channel ON posts(board,channel,seq);
CREATE INDEX IF NOT EXISTS posts_board_channel_timestamp ON posts(board,channel,timestamp,seq);
CREATE INDEX IF NOT EXISTS posts_roots_created ON posts(board,channel,timestamp,seq) WHERE id=root;
CREATE INDEX IF NOT EXISTS posts_board_root ON posts(board,root,seq);
CREATE INDEX IF NOT EXISTS posts_board_root_timestamp ON posts(board,root,timestamp,seq);
CREATE INDEX IF NOT EXISTS posts_board_timestamp ON posts(board,timestamp,seq);
CREATE TABLE IF NOT EXISTS subscriptions (
 board TEXT NOT NULL, target TEXT NOT NULL, agent TEXT NOT NULL,
 PRIMARY KEY(board,target,agent)
);
CREATE TABLE IF NOT EXISTS subscription_opt_outs (
 board TEXT NOT NULL, target TEXT NOT NULL, agent TEXT NOT NULL,
 PRIMARY KEY(board,target,agent)
);`

// localBoardPools shares one pool per canonical database path. Weak entries let
// the last board handle release its pool (Rust's POOLS plus Weak<SqlitePool>).
var localBoardPools = struct {
	mu    sync.Mutex
	pools map[string]*localBoardPool
}{pools: map[string]*localBoardPool{}}

type localBoardPool struct {
	db   *sql.DB
	refs int
}

// LocalAgentMessageBoard is the SQLite board for one agent tree.
type LocalAgentMessageBoard struct {
	identity string
	poolPath string
	db       *sql.DB
	host     Host
}

// storedPost mirrors Rust's StoredPost payload.
type storedPost struct {
	Metadata PostMetadata `json:"metadata"`
	Text     string       `json:"text"`
}

// OpenLocalBoard reopens the same board for a root, child or resumed runtime.
// The shared SQLite configuration preserves the host's connection and journal
// policy.
func OpenLocalBoard(ctx context.Context, sqlite state.SqliteConfig, identity string, host Host) (*LocalAgentMessageBoard, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(identity) == "" {
		return nil, invalid("invalid board session identity")
	}
	path, err := localBoardDatabasePath(sqlite)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := acquireLocalBoardPool(ctx, sqlite, path)
	if err != nil {
		return nil, err
	}
	return &LocalAgentMessageBoard{identity: identity, poolPath: path, db: db, host: host}, nil
}

// Close releases this handle's claim on the shared pool.
func (b *LocalAgentMessageBoard) Close() {
	if b == nil || b.poolPath == "" {
		return
	}
	releaseLocalBoardPool(b.poolPath)
	b.db = nil
	b.poolPath = ""
}

// Identity is the board's session identity.
func (b *LocalAgentMessageBoard) Identity() string {
	if b == nil {
		return ""
	}
	return b.identity
}

func localBoardDatabasePath(sqlite state.SqliteConfig) (string, error) {
	home := strings.TrimSpace(sqlite.Home())
	if home == "" {
		return "", fmt.Errorf("agent message-board storage home is required")
	}
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	absolute, err := filepath.Abs(home)
	if err != nil {
		return "", err
	}
	return filepath.Join(absolute, AgentMessageBoardDatabaseFile), nil
}

// acquireLocalBoardPool returns the process-shared pool for a database path,
// creating it and applying the schema when it is not live.
func acquireLocalBoardPool(ctx context.Context, sqlite state.SqliteConfig, path string) (*sql.DB, error) {
	localBoardPools.mu.Lock()
	defer localBoardPools.mu.Unlock()
	for key, pool := range localBoardPools.pools {
		if pool.refs <= 0 {
			delete(localBoardPools.pools, key)
		}
	}
	if pool, ok := localBoardPools.pools[path]; ok {
		pool.refs++
		return pool.db, nil
	}
	db, err := sqlite.OpenReadWrite(ctx, path)
	if err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(ctx, agentMessageBoardSchema); err != nil {
		_ = db.Close()
		return nil, err
	}
	localBoardPools.pools[path] = &localBoardPool{db: db, refs: 1}
	return db, nil
}

func releaseLocalBoardPool(path string) {
	localBoardPools.mu.Lock()
	defer localBoardPools.mu.Unlock()
	pool, ok := localBoardPools.pools[path]
	if !ok {
		return
	}
	pool.refs--
	if pool.refs > 0 {
		return
	}
	delete(localBoardPools.pools, path)
	_ = pool.db.Close()
}

func closeLocalBoardPool(path string) {
	localBoardPools.mu.Lock()
	defer localBoardPools.mu.Unlock()
	if pool, ok := localBoardPools.pools[path]; ok {
		delete(localBoardPools.pools, path)
		_ = pool.db.Close()
	}
}

// DeleteLocalBoards permanently removes the boards owned by these roots,
// including their posts and subscriptions. A child's ID does not match its
// parent's board, so unload and archive must not call this.
func DeleteLocalBoards(ctx context.Context, sqlite state.SqliteConfig, roots []string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(roots) == 0 {
		return nil
	}
	home := strings.TrimSpace(sqlite.Home())
	if home == "" {
		return fmt.Errorf("agent message-board storage home is required")
	}
	rawPath := filepath.Join(home, AgentMessageBoardDatabaseFile)
	if _, err := os.Stat(rawPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	path, err := localBoardDatabasePath(sqlite)
	if err != nil {
		return err
	}
	db, err := sqlite.OpenReadWrite(ctx, path)
	if err != nil {
		if !state.IsSQLiteCorruptionError(err) {
			return err
		}
		// A corrupt database cannot be repaired in place: back it up and
		// recreate it so thread deletion still succeeds.
		closeLocalBoardPool(path)
		if _, backupErr := state.BackupDBFilesForFreshStart(&state.DBRecoveryStartupError{DatabasePath: path, Detail: err.Error()}, time.Now()); backupErr != nil {
			return backupErr
		}
		db, err = sqlite.OpenReadWrite(ctx, path)
		if err != nil {
			return err
		}
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, agentMessageBoardSchema); err != nil {
		return err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, "ROLLBACK")
		}
	}()
	for _, root := range roots {
		if _, err := conn.ExecContext(ctx, "INSERT OR IGNORE INTO deleted_boards (board) VALUES (?)", root); err != nil {
			return err
		}
		for _, statement := range []string{
			"DELETE FROM subscriptions WHERE board=?",
			"DELETE FROM subscription_opt_outs WHERE board=?",
			"DELETE FROM posts WHERE board=?",
			"DELETE FROM channels WHERE board=?",
		} {
			if _, err := conn.ExecContext(ctx, statement, root); err != nil {
				return err
			}
		}
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	committed = true
	return nil
}

// localBoardWrite is an immediate write transaction spanning one connection.
type localBoardWrite struct {
	conn *sql.Conn
}

func (b *LocalAgentMessageBoard) beginWrite(ctx context.Context) (*localBoardWrite, error) {
	conn, err := b.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		_ = conn.Close()
		return nil, err
	}
	var deleted bool
	if err := conn.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM deleted_boards WHERE board=?)", b.identity).Scan(&deleted); err != nil {
		_, _ = conn.ExecContext(ctx, "ROLLBACK")
		_ = conn.Close()
		return nil, err
	}
	if deleted {
		_, _ = conn.ExecContext(ctx, "ROLLBACK")
		_ = conn.Close()
		return nil, invalid("the message board's root has been permanently deleted")
	}
	return &localBoardWrite{conn: conn}, nil
}

func (w *localBoardWrite) commit(ctx context.Context) error {
	if w == nil || w.conn == nil {
		return nil
	}
	_, err := w.conn.ExecContext(ctx, "COMMIT")
	if closeErr := w.conn.Close(); err == nil {
		err = closeErr
	}
	w.conn = nil
	return err
}

// rollback is a no-op after commit.
func (w *localBoardWrite) rollback(ctx context.Context) {
	if w == nil || w.conn == nil {
		return
	}
	_, _ = w.conn.ExecContext(ctx, "ROLLBACK")
	_ = w.conn.Close()
	w.conn = nil
}

// CreateChannel creates a channel and optionally subscribes the caller.
func (b *LocalAgentMessageBoard) CreateChannel(ctx context.Context, caller string, request CreateChannelRequest) (*ChannelSummary, error) {
	if err := validateChannelName(request.ChannelName); err != nil {
		return nil, err
	}
	author, err := b.host.AgentPath(ctx, caller)
	if err != nil {
		return nil, err
	}
	now, err := b.host.CurrentTime(ctx, caller)
	if err != nil {
		return nil, err
	}
	tx, err := b.beginWrite(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.rollback(ctx)
	if err := b.insertChannel(ctx, tx, request.ChannelName, author, now); err != nil {
		return nil, err
	}
	if request.Subscription == Subscribe {
		if err := b.subscribe(ctx, tx, SubscriptionTarget{Kind: "channel", ChannelName: request.ChannelName}, caller); err != nil {
			return nil, err
		}
	}
	summary, err := b.channelSummary(ctx, tx, request.ChannelName)
	if err != nil {
		return nil, err
	}
	if err := tx.commit(ctx); err != nil {
		return nil, err
	}
	return summary, nil
}

// Post creates a post or reply. Once started, the accepted write and its
// fanout finish even if the tool caller disconnects; delivery is attempted once
// and a failed notice never fails the committed post.
func (b *LocalAgentMessageBoard) Post(ctx context.Context, caller string, request PostRequest) (*PostMetadata, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	// The write must not be aborted by the caller's cancellation (Rust posts on
	// a detached task and awaits its handle).
	return b.postInner(context.WithoutCancel(ctx), caller, request)
}

func (b *LocalAgentMessageBoard) postInner(ctx context.Context, caller string, request PostRequest) (*PostMetadata, error) {
	if err := validatePostRequest(request); err != nil {
		return nil, err
	}
	author, err := b.host.AgentPath(ctx, caller)
	if err != nil {
		return nil, err
	}
	requestID := caller + ":" + request.RequestID
	requestJSON, err := encodeWirePostRequest(request)
	if err != nil {
		return nil, err
	}
	if existing, err := b.existingPost(ctx, b.db, requestID, requestJSON); err != nil {
		return nil, err
	} else if existing != nil {
		return &existing.Metadata, nil
	}
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
	tx, err := b.beginWrite(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.rollback(ctx)
	if existing, err := b.existingPost(ctx, tx.conn, requestID, requestJSON); err != nil {
		return nil, err
	} else if existing != nil {
		return &existing.Metadata, nil
	}
	id, err := newBoardPostID()
	if err != nil {
		return nil, err
	}
	channelName, root, target, err := b.resolvePostDestination(ctx, tx, request.Destination, author, now, id)
	if err != nil {
		return nil, err
	}
	subscribed, err := b.subscribedRecipients(ctx, tx, target)
	if err != nil {
		return nil, err
	}
	for _, recipient := range subscribed {
		recipients[recipient] = struct{}{}
	}
	delete(recipients, caller)
	post := storedPost{
		Metadata: PostMetadata{
			MessageID:   id,
			ChannelName: channelName,
			Author:      author,
			ThreadID:    root,
			CreatedAt:   now,
		},
		Text: request.Text,
	}
	payload, err := json.Marshal(post)
	if err != nil {
		return nil, err
	}
	if _, err := tx.conn.ExecContext(ctx,
		"INSERT INTO posts(board,id,channel,root,author,timestamp,body_search,payload,request_id,request) VALUES(?,?,?,?,?,?,?,?,?,?)",
		b.identity, id, channelName, root, string(author), now.UnixMicro(), foldCase(request.Text), string(payload), requestID, requestJSON,
	); err != nil {
		return nil, err
	}
	// Participation subscribes by default, without overriding an explicit opt-out.
	if err := b.subscribe(ctx, tx, SubscriptionTarget{Kind: "thread", ThreadID: root}, caller); err != nil {
		return nil, err
	}
	if err := tx.commit(ctx); err != nil {
		return nil, err
	}
	notifyRecipients(ctx, b.host, recipients, post.preview(previewChars))
	return &post.Metadata, nil
}

func (b *LocalAgentMessageBoard) resolvePostDestination(ctx context.Context, tx *localBoardWrite, destination PostDestination, author agent.AgentPath, now time.Time, id string) (string, string, SubscriptionTarget, error) {
	switch destination.Kind {
	case "channel":
		exists, err := b.channelExists(ctx, tx, destination.Name)
		if err != nil {
			return "", "", SubscriptionTarget{}, err
		}
		if !exists {
			return "", "", SubscriptionTarget{}, invalid("channel not found in this board")
		}
		return destination.Name, id, SubscriptionTarget{Kind: "channel", ChannelName: destination.Name}, nil
	case "new_channel":
		if err := validateChannelName(destination.Name); err != nil {
			return "", "", SubscriptionTarget{}, err
		}
		if err := b.insertChannel(ctx, tx, destination.Name, author, now); err != nil {
			return "", "", SubscriptionTarget{}, err
		}
		return destination.Name, id, SubscriptionTarget{Kind: "channel", ChannelName: destination.Name}, nil
	case "thread":
		post, err := b.loadPost(ctx, tx.conn, destination.ThreadID)
		if err != nil {
			return "", "", SubscriptionTarget{}, err
		}
		if post.Metadata.ThreadID != destination.ThreadID {
			return "", "", SubscriptionTarget{}, invalid("thread_id must identify a top-level post")
		}
		return post.Metadata.ChannelName, destination.ThreadID, SubscriptionTarget{Kind: "thread", ThreadID: destination.ThreadID}, nil
	default:
		return "", "", SubscriptionTarget{}, invalid("post destination is required")
	}
}

// SetSubscription changes a channel or thread subscription.
func (b *LocalAgentMessageBoard) SetSubscription(ctx context.Context, caller string, request SubscriptionRequest) (*SubscriptionState, error) {
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
	tx, err := b.beginWrite(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.rollback(ctx)
	var channelName string
	var root *string
	var last *string
	switch request.Target.Kind {
	case "channel":
		summary, err := b.channelSummary(ctx, tx, request.Target.ChannelName)
		if err != nil {
			return nil, err
		}
		channelName = request.Target.ChannelName
		last = summary.LastMessageID
	case "thread":
		post, err := b.loadPost(ctx, tx.conn, request.Target.ThreadID)
		if err != nil {
			return nil, err
		}
		if post.Metadata.ThreadID != request.Target.ThreadID {
			return nil, invalid("thread_id must identify a top-level post")
		}
		row := tx.conn.QueryRowContext(ctx, "SELECT id FROM posts WHERE board=? AND root=? ORDER BY timestamp DESC, seq DESC LIMIT 1", b.identity, request.Target.ThreadID)
		value := ""
		if err := row.Scan(&value); err != nil {
			return nil, err
		}
		root = &request.Target.ThreadID
		last = &value
		channelName = post.Metadata.ChannelName
	default:
		return nil, invalid("subscription target is required")
	}
	enabled := request.Change == Subscribe
	// Keep active subscriptions readable by older binaries. Opt-outs only
	// prevent implicit subscription when this agent participates again.
	statements := []string{
		"DELETE FROM subscription_opt_outs WHERE board=? AND target=? AND agent=?",
		"INSERT OR IGNORE INTO subscriptions(board,target,agent) VALUES(?,?,?)",
	}
	if request.Change != Subscribe {
		statements = []string{
			"DELETE FROM subscriptions WHERE board=? AND target=? AND agent=?",
			"INSERT OR IGNORE INTO subscription_opt_outs(board,target,agent) VALUES(?,?,?)",
		}
	}
	key, err := subscriptionTargetKey(request.Target)
	if err != nil {
		return nil, err
	}
	for _, statement := range statements {
		if _, err := tx.conn.ExecContext(ctx, statement, b.identity, key, targetAgent); err != nil {
			return nil, err
		}
	}
	if err := tx.commit(ctx); err != nil {
		return nil, err
	}
	return &SubscriptionState{
		ChannelName:   channelName,
		ThreadID:      root,
		TargetAgent:   targetPath,
		Enabled:       enabled,
		LastMessageID: last,
	}, nil
}

// ReadPost reads a bounded slice of one post; offsets count Unicode characters.
func (b *LocalAgentMessageBoard) ReadPost(ctx context.Context, caller string, request ReadPostRequest) (*PostContent, error) {
	if _, err := b.host.AgentPath(ctx, caller); err != nil {
		return nil, err
	}
	post, err := b.loadPost(ctx, b.db, request.MessageID)
	if err != nil {
		return nil, err
	}
	text := []rune(post.Text)
	offset := request.OffsetChars
	if offset > len(text) {
		offset = len(text)
	}
	if offset < 0 {
		offset = 0
	}
	limit := request.LimitChars
	if limit <= 0 || limit > maxReadPostChars {
		limit = maxReadPostChars
	}
	end := offset + limit
	if end > len(text) {
		end = len(text)
	}
	slice := string(text[offset:end])
	return &PostContent{
		PostMetadata:    post.Metadata,
		Text:            slice,
		NChars:          len(text),
		NextOffsetChars: offset + len([]rune(slice)),
	}, nil
}

func (b *LocalAgentMessageBoard) ListChannels(ctx context.Context, caller string, query ChannelQuery) (*Page[ChannelSummary], error) {
	if _, err := b.host.AgentPath(ctx, caller); err != nil {
		return nil, err
	}
	offset, limit, err := boardWindow(query.Page)
	if err != nil {
		return nil, err
	}
	order := boardOrder(query.Direction)
	search := ""
	if query.Query != nil {
		search = foldCase(*query.Query)
	}
	rows, err := b.db.QueryContext(ctx,
		"SELECT c.name FROM channels c WHERE c.board=? AND instr(c.name_search, ?)>0 "+
			"ORDER BY COALESCE((SELECT MAX(p.timestamp) FROM posts p WHERE p.board=c.board AND p.channel=c.name), c.timestamp) "+order+",c.name "+order+
			" LIMIT ? OFFSET ?",
		b.identity, search, int64(limit+1), int64(offset))
	if err != nil {
		return nil, err
	}
	names := []string{}
	for rows.Next() {
		name := ""
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return nil, err
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	channels := make([]ChannelSummary, 0, len(names))
	for _, name := range names {
		summary, err := b.channelSummaryByName(ctx, name)
		if err != nil {
			return nil, err
		}
		channels = append(channels, *summary)
	}
	return finishBoardPage(offset, limit, channels)
}

func (b *LocalAgentMessageBoard) ListThreads(ctx context.Context, caller string, query ThreadQuery) (*Page[ThreadSummary], error) {
	if _, err := b.host.AgentPath(ctx, caller); err != nil {
		return nil, err
	}
	exists, err := b.channelExistsName(ctx, query.ChannelName)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, invalid("channel not found in this board")
	}
	offset, limit, err := boardWindow(query.Page)
	if err != nil {
		return nil, err
	}
	order := boardOrder(query.Direction)
	sortExpression := "p.timestamp"
	if query.Sort == ThreadSortActivity {
		sortExpression = "(SELECT MAX(r.timestamp) FROM posts r WHERE r.board=p.board AND r.root=p.id)"
	}
	sqlText := "WITH page AS MATERIALIZED (SELECT p.board,p.id,p.payload,p.seq," + sortExpression + " AS sort_timestamp " +
		"FROM posts p WHERE p.board=? AND p.channel=? AND p.id=p.root ORDER BY sort_timestamp " + order + ",p.seq " + order +
		" LIMIT ? OFFSET ?) " +
		"SELECT p.payload, (SELECT COUNT(*) FROM posts r WHERE r.board=p.board AND r.root=p.id AND r.id<>r.root), " +
		"(SELECT r.payload FROM posts r WHERE r.board=p.board AND r.root=p.id AND r.id<>r.root ORDER BY r.timestamp DESC,r.seq DESC LIMIT 1) " +
		"FROM page p ORDER BY p.sort_timestamp " + order + ",p.seq " + order
	rows, err := b.db.QueryContext(ctx, sqlText, b.identity, query.ChannelName, int64(limit+1), int64(offset))
	if err != nil {
		return nil, err
	}
	type threadRow struct {
		root  storedPost
		count int
		last  *storedPost
	}
	fetched := []threadRow{}
	for rows.Next() {
		var payload string
		var count int64
		var last sql.NullString
		if err := rows.Scan(&payload, &count, &last); err != nil {
			_ = rows.Close()
			return nil, err
		}
		var root storedPost
		if err := json.Unmarshal([]byte(payload), &root); err != nil {
			_ = rows.Close()
			return nil, err
		}
		row := threadRow{root: root, count: int(count)}
		if last.Valid {
			var reply storedPost
			if err := json.Unmarshal([]byte(last.String), &reply); err != nil {
				_ = rows.Close()
				return nil, err
			}
			row.last = &reply
		}
		fetched = append(fetched, row)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	chars := threadPreviewChars(query.MaxCharsPerPost, minInt(len(fetched), limit))
	threads := make([]ThreadSummary, 0, len(fetched))
	for _, row := range fetched {
		activity := row.root.Metadata.CreatedAt
		var latest *PostPreview
		if row.last != nil {
			preview := row.last.preview(chars)
			latest = &preview
			if preview.CreatedAt.After(activity) {
				activity = preview.CreatedAt
			}
		}
		threads = append(threads, ThreadSummary{
			ThreadID:       row.root.Metadata.MessageID,
			RootPost:       row.root.preview(chars),
			ReplyCount:     row.count,
			LastActivityAt: activity,
			LatestReply:    latest,
		})
	}
	return finishBoardPage(offset, limit, threads)
}

func (b *LocalAgentMessageBoard) SearchPosts(ctx context.Context, caller string, query PostQuery) (*Page[PostPreview], error) {
	if _, err := b.host.AgentPath(ctx, caller); err != nil {
		return nil, err
	}
	offset, limit, err := boardWindow(query.Page)
	if err != nil {
		return nil, err
	}
	var after *[2]int64
	if query.AfterMessageID != nil {
		var timestamp int64
		var sequence int64
		row := b.db.QueryRowContext(ctx, "SELECT timestamp,seq FROM posts WHERE board=? AND id=?", b.identity, *query.AfterMessageID)
		if err := row.Scan(&timestamp, &sequence); err != nil {
			if err == sql.ErrNoRows {
				return nil, invalid("post not found in this board")
			}
			return nil, err
		}
		after = &[2]int64{timestamp, sequence}
	}
	statement := "SELECT payload FROM posts WHERE board=?"
	args := []any{b.identity}
	if query.ChannelName != nil {
		statement += " AND channel=?"
		args = append(args, *query.ChannelName)
	}
	if query.Author != nil {
		statement += " AND author=?"
		args = append(args, string(*query.Author))
	}
	if query.Query != nil {
		statement += " AND instr(body_search, ?)>0"
		args = append(args, foldCase(*query.Query))
	}
	if after != nil {
		statement += " AND (timestamp,seq)>(?,?)"
		args = append(args, after[0], after[1])
	}
	statement += " ORDER BY timestamp DESC,seq DESC LIMIT ? OFFSET ?"
	args = append(args, int64(limit+1), int64(offset))
	rows, err := b.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	posts, err := decodeStoredPosts(rows)
	if err != nil {
		return nil, err
	}
	chars := searchPreviewChars(query.MaxCharsPerPost, minInt(len(posts), limit))
	previews := make([]PostPreview, 0, len(posts))
	for _, post := range posts {
		previews = append(previews, post.preview(chars))
	}
	return finishBoardPage(offset, limit, previews)
}

func (b *LocalAgentMessageBoard) ReadThread(ctx context.Context, caller string, request ReadThreadRequest) (*ThreadPage, error) {
	if _, err := b.host.AgentPath(ctx, caller); err != nil {
		return nil, err
	}
	root, err := b.loadPost(ctx, b.db, request.ThreadID)
	if err != nil {
		return nil, err
	}
	if root.Metadata.ThreadID != request.ThreadID {
		return nil, invalid("thread_id must identify a top-level post")
	}
	offset, limit, err := boardWindow(request.Page)
	if err != nil {
		return nil, err
	}
	rows, err := b.db.QueryContext(ctx,
		"SELECT payload FROM posts WHERE board=? AND root=? AND id<>root ORDER BY timestamp DESC,seq DESC LIMIT ? OFFSET ?",
		b.identity, request.ThreadID, int64(limit+1), int64(offset))
	if err != nil {
		return nil, err
	}
	replies, err := decodeStoredPosts(rows)
	if err != nil {
		return nil, err
	}
	chars := minPositive(request.MaxCharsPerPost, outputBudgetChars/(minInt(len(replies), limit)+1))
	previews := make([]PostPreview, 0, len(replies))
	for _, reply := range replies {
		previews = append(previews, reply.preview(chars))
	}
	page, err := finishBoardPage(offset, limit, previews)
	if err != nil {
		return nil, err
	}
	return &ThreadPage{RootPost: root.preview(chars), Replies: *page}, nil
}

func (p storedPost) preview(maxChars int) PostPreview {
	text := []rune(p.Text)
	limit := maxChars
	if limit > len(text) {
		limit = len(text)
	}
	return PostPreview{
		PostMetadata: p.Metadata,
		TextPreview:  string(text[:limit]),
		NChars:       len(text),
		Truncated:    len(text) > maxChars,
	}
}

func decodeStoredPosts(rows *sql.Rows) ([]storedPost, error) {
	defer func() { _ = rows.Close() }()
	posts := []storedPost{}
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var post storedPost
		if err := json.Unmarshal([]byte(payload), &post); err != nil {
			return nil, err
		}
		posts = append(posts, post)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return posts, nil
}

func (b *LocalAgentMessageBoard) channelExists(ctx context.Context, tx *localBoardWrite, name string) (bool, error) {
	return channelExistsIn(ctx, tx.conn, b.identity, name)
}

func (b *LocalAgentMessageBoard) channelExistsName(ctx context.Context, name string) (bool, error) {
	return channelExistsIn(ctx, b.db, b.identity, name)
}

type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func channelExistsIn(ctx context.Context, querier queryRower, identity string, name string) (bool, error) {
	var exists bool
	if err := querier.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM channels WHERE board=? AND name=?)", identity, name).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

func (b *LocalAgentMessageBoard) insertChannel(ctx context.Context, tx *localBoardWrite, name string, author agent.AgentPath, now time.Time) error {
	result, err := tx.conn.ExecContext(ctx,
		"INSERT OR IGNORE INTO channels(board,name,name_search,created_at,timestamp,author) VALUES(?,?,?,?,?,?)",
		b.identity, name, foldCase(name), now.UTC().Format(localBoardTimestampFormat), now.UnixMicro(), string(author))
	if err != nil {
		return err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if inserted == 0 {
		return invalid("channel already exists")
	}
	return nil
}

func (b *LocalAgentMessageBoard) subscribe(ctx context.Context, tx *localBoardWrite, target SubscriptionTarget, caller string) error {
	key, err := subscriptionTargetKey(target)
	if err != nil {
		return err
	}
	_, err = tx.conn.ExecContext(ctx,
		"INSERT OR IGNORE INTO subscriptions(board,target,agent) SELECT ?1,?2,?3 WHERE NOT EXISTS(SELECT 1 FROM subscription_opt_outs WHERE board=?1 AND target=?2 AND agent=?3)",
		b.identity, key, caller)
	return err
}

func (b *LocalAgentMessageBoard) subscribedRecipients(ctx context.Context, tx *localBoardWrite, target SubscriptionTarget) ([]string, error) {
	key, err := subscriptionTargetKey(target)
	if err != nil {
		return nil, err
	}
	rows, err := tx.conn.QueryContext(ctx, "SELECT agent FROM subscriptions WHERE board=? AND target=?", b.identity, key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	agents := []string{}
	for rows.Next() {
		agentID := ""
		if err := rows.Scan(&agentID); err != nil {
			return nil, err
		}
		agents = append(agents, agentID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Strings(agents)
	return agents, nil
}

func (b *LocalAgentMessageBoard) loadPost(ctx context.Context, querier queryRower, id string) (storedPost, error) {
	var payload string
	err := querier.QueryRowContext(ctx, "SELECT payload FROM posts WHERE board=? AND id=?", b.identity, id).Scan(&payload)
	if err != nil {
		if err == sql.ErrNoRows {
			return storedPost{}, invalid("post not found in this board")
		}
		return storedPost{}, err
	}
	var post storedPost
	if err := json.Unmarshal([]byte(payload), &post); err != nil {
		return storedPost{}, err
	}
	return post, nil
}

func (b *LocalAgentMessageBoard) existingPost(ctx context.Context, querier queryRower, requestID string, request string) (*storedPost, error) {
	var payload string
	var stored string
	err := querier.QueryRowContext(ctx, "SELECT payload,request FROM posts WHERE board=? AND request_id=?", b.identity, requestID).Scan(&payload, &stored)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if stored != request {
		return nil, invalid("request ID was already used for a different post")
	}
	var post storedPost
	if err := json.Unmarshal([]byte(payload), &post); err != nil {
		return nil, err
	}
	return &post, nil
}

func (b *LocalAgentMessageBoard) channelSummary(ctx context.Context, tx *localBoardWrite, name string) (*ChannelSummary, error) {
	return channelSummaryIn(ctx, tx.conn, b.identity, name)
}

func (b *LocalAgentMessageBoard) channelSummaryByName(ctx context.Context, name string) (*ChannelSummary, error) {
	return channelSummaryIn(ctx, b.db, b.identity, name)
}

func channelSummaryIn(ctx context.Context, querier queryRower, identity string, name string) (*ChannelSummary, error) {
	var createdAt string
	var author string
	var messageCount int64
	var lastMessageID sql.NullString
	err := querier.QueryRowContext(ctx,
		"SELECT c.created_at,c.author,"+
			"(SELECT COUNT(*) FROM posts p WHERE p.board=c.board AND p.channel=c.name) AS message_count,"+
			"(SELECT p.id FROM posts p WHERE p.board=c.board AND p.channel=c.name ORDER BY p.timestamp DESC,p.seq DESC LIMIT 1) AS last_message_id "+
			"FROM channels c WHERE c.board=? AND c.name=?",
		identity, name).Scan(&createdAt, &author, &messageCount, &lastMessageID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, invalid("channel not found in this board")
		}
		return nil, err
	}
	parsed, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return nil, err
	}
	summary := &ChannelSummary{
		ChannelName:  name,
		CreatedAt:    parsed.UTC(),
		CreatedBy:    agent.AgentPath(author),
		MessageCount: int(messageCount),
	}
	if lastMessageID.Valid {
		value := lastMessageID.String
		summary.LastMessageID = &value
	}
	return summary, nil
}

// --- wire shapes (Rust serde compatibility) ---

type wirePostWireRequest struct {
	RequestID      string `json:"request_id"`
	Destination    any    `json:"destination"`
	Text           string `json:"text"`
	AgentsToNotify []any  `json:"agents_to_notify"`
}

func encodeWirePostRequest(request PostRequest) (string, error) {
	destination, err := postDestinationJSON(request.Destination)
	if err != nil {
		return "", err
	}
	recipients := make([]any, 0, len(request.AgentsToNotify))
	for _, path := range request.AgentsToNotify {
		recipients = append(recipients, string(path))
	}
	encoded, err := json.Marshal(wirePostWireRequest{
		RequestID:      request.RequestID,
		Destination:    destination,
		Text:           request.Text,
		AgentsToNotify: recipients,
	})
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// postDestinationJSON mirrors serde's externally tagged PostDestination enum.
func postDestinationJSON(destination PostDestination) (map[string]any, error) {
	switch destination.Kind {
	case "channel":
		return map[string]any{"Channel": destination.Name}, nil
	case "new_channel":
		return map[string]any{"NewChannel": destination.Name}, nil
	case "thread":
		return map[string]any{"Thread": destination.ThreadID}, nil
	default:
		return nil, invalid("post destination is required")
	}
}

// subscriptionTargetKey mirrors serde's externally tagged SubscriptionTarget,
// which Rust stores as the `target` column key.
func subscriptionTargetKey(target SubscriptionTarget) (string, error) {
	switch target.Kind {
	case "channel":
		return marshalWireJSON(map[string]any{"Channel": target.ChannelName})
	case "thread":
		return marshalWireJSON(map[string]any{"Thread": target.ThreadID})
	default:
		return "", invalid("subscription target is required")
	}
}

func marshalWireJSON(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// --- shared helpers ---

// validateChannelName mirrors Rust's validate_channel.
func validateChannelName(name string) error {
	if name == "" || len(name) > maxChannelNameBytes || strings.TrimSpace(name) != name || containsControlChar(name) {
		return invalid("channel names must contain 1–128 bytes without edge whitespace or control characters")
	}
	return nil
}

// newBoardPostID mirrors Rust's Uuid::now_v7 for board posts.
func newBoardPostID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

// boardWindow decodes Rust's bounded offset window: a base64url 4-byte
// big-endian offset cursor and a hard 50-item cap.
func boardWindow(request PageRequest) (uint32, int, error) {
	offset, err := decodePageCursor(request.Cursor)
	if err != nil {
		return 0, 0, err
	}
	limit := request.NormalizedLimit()
	if limit > MaxPageLimit {
		limit = MaxPageLimit
	}
	return offset, limit, nil
}

// finishBoardPage mirrors Rust's Window::finish: a one-item lookahead decides
// has_more and the next cursor.
func finishBoardPage[T any](offset uint32, limit int, results []T) (*Page[T], error) {
	hasMore := len(results) > limit
	if hasMore {
		results = results[:limit]
	}
	var nextCursor *string
	if hasMore {
		next := uint64(offset) + uint64(len(results))
		if next > 0xFFFFFFFF {
			return nil, invalid("cursor offset exceeds the board limit")
		}
		cursor := base64PageCursor(uint32(next))
		nextCursor = &cursor
	}
	return &Page[T]{Results: results, NextCursor: nextCursor}, nil
}

func boardOrder(direction SortDirection) string {
	if direction == OldestFirst {
		return "ASC"
	}
	return "DESC"
}

func minInt(left int, right int) int {
	if left < right {
		return left
	}
	return right
}

var _ Board = (*LocalAgentMessageBoard)(nil)
