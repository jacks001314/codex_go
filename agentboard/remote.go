package agentboard

// Typed remote implementation of the board contract over the versioned HTTP API.
//
// Rust parity: codex-rs/agent-message-board-client (#48100). HTTP policy is
// supplied by the caller (the shared client with its proxy and CA policy); reads
// and writes never fall back to a private local board.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"codex_go/agent"
)

const (
	// remoteBoardMaxBody and remoteBoardMaxResponse are the service limits the
	// client enforces before sending and while reading.
	remoteBoardMaxBody     = 512 * 1024
	remoteBoardMaxResponse = 1024 * 1024
	// remoteBoardRequestTimeout bounds one request, matching Rust's 30-second
	// per-request timeout.
	remoteBoardRequestTimeout = 30 * time.Second
	// remoteBoardSetupDeadline bounds opening a notification stream and receiving
	// its readiness event.
	remoteBoardSetupDeadline = 30 * time.Second
	// remoteBoardNotificationPreviewChars is the preview bound a notification must
	// respect, so a notice never injects an unbounded body.
	remoteBoardNotificationPreviewChars = 150
)

// RemoteBoardHTTPDoer is the transport seam; the caller supplies the shared HTTP
// client so the board traffic observes the application network policy.
type RemoteBoardHTTPDoer interface {
	Do(request *http.Request) (*http.Response, error)
}

// RemoteBoardOptions configures a remote board.
type RemoteBoardOptions struct {
	HTTP     RemoteBoardHTTPDoer
	Endpoint string
	// Board is the session identity the endpoints are scoped to.
	Board string
	// Token is the host credential; it must be 32-4096 printable ASCII bytes.
	Token string
	// Clock reports the caller's configured time, which the board operations carry
	// as their timestamp. Nil uses the wall clock.
	Clock func(caller string) (time.Time, error)
}

// RemoteBoard is one session's board served by a host.
type RemoteBoard struct {
	http     RemoteBoardHTTPDoer
	boardURL *url.URL
	board    string
	token    string
	clock    func(caller string) (time.Time, error)
}

// ValidateRemoteBoardToken mirrors Rust's AccessToken::new: a credential is
// 32-4096 bytes of printable ASCII without whitespace.
func ValidateRemoteBoardToken(token string) error {
	if len(token) < 32 || len(token) > 4096 {
		return invalid("credentials must contain 32-4096 bytes")
	}
	for index := 0; index < len(token); index++ {
		if character := token[index]; character < 0x21 || character > 0x7e {
			return invalid("credential must be ASCII without whitespace")
		}
	}
	return nil
}

// NewRemoteBoard validates the endpoint and credential and appends the board path
// to the endpoint's optional path prefix.
func NewRemoteBoard(options RemoteBoardOptions) (*RemoteBoard, error) {
	if options.HTTP == nil {
		return nil, errors.New("message-board HTTP client is required")
	}
	parsed, err := url.Parse(strings.TrimSpace(options.Endpoint))
	if err != nil {
		return nil, fmt.Errorf("invalid message-board endpoint: %w", err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, errors.New("message-board endpoint must be an HTTP(S) URL")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("message-board endpoint must not contain a query or fragment")
	}
	if err := ValidateRemoteBoardToken(options.Token); err != nil {
		return nil, err
	}
	board := strings.TrimSpace(options.Board)
	if board == "" {
		return nil, errors.New("message-board session id is required")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/v1/boards/" + board
	clock := options.Clock
	if clock == nil {
		clock = func(string) (time.Time, error) { return time.Now().UTC(), nil }
	}
	return &RemoteBoard{http: options.HTTP, boardURL: parsed, board: board, token: options.Token, clock: clock}, nil
}

// Identity is the board's session identity.
func (b *RemoteBoard) Identity() string {
	if b == nil {
		return ""
	}
	return b.board
}

// CreateBoard creates the board and returns its session identity.
func (b *RemoteBoard) CreateBoard(ctx context.Context) (string, error) {
	response, err := b.request(ctx, http.MethodPut, "", nil)
	if err != nil {
		return "", err
	}
	payload, err := b.decode(response)
	if err != nil {
		return "", err
	}
	var identity string
	if err := json.Unmarshal(payload, &identity); err != nil {
		return "", fmt.Errorf("invalid board response: %w", err)
	}
	return identity, nil
}

// DeleteBoard removes the board.
func (b *RemoteBoard) DeleteBoard(ctx context.Context) error {
	response, err := b.request(ctx, http.MethodDelete, "", nil)
	if err != nil {
		return err
	}
	_, err = b.decode(response)
	return err
}

// RegisterMembers registers the session's agents; the returned session token is
// stable when the call is retried.
func (b *RemoteBoard) RegisterMembers(ctx context.Context, members map[string]string) (string, error) {
	body, err := json.Marshal(struct {
		Members map[string]string `json:"members"`
	}{Members: members})
	if err != nil {
		return "", err
	}
	response, err := b.request(ctx, http.MethodPost, "/members", body)
	if err != nil {
		return "", err
	}
	payload, err := b.decode(response)
	if err != nil {
		return "", err
	}
	var token string
	if err := json.Unmarshal(payload, &token); err != nil {
		return "", fmt.Errorf("invalid board response: %w", err)
	}
	// Rust deserializes the returned session token as an AccessToken, so a
	// malformed credential is rejected here instead of being used.
	if err := ValidateRemoteBoardToken(token); err != nil {
		return "", err
	}
	return token, nil
}

// CreateChannel creates a channel and optionally subscribes the caller.
func (b *RemoteBoard) CreateChannel(ctx context.Context, caller string, request CreateChannelRequest) (*ChannelSummary, error) {
	params := struct {
		ChannelName  string `json:"channel_name"`
		Subscription string `json:"subscription"`
	}{ChannelName: request.ChannelName, Subscription: remoteSubscriptionChange(request.Subscription)}
	var out ChannelSummary
	if err := b.call(ctx, caller, "create_channel", params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListChannels lists or searches a board's channels.
func (b *RemoteBoard) ListChannels(ctx context.Context, caller string, query ChannelQuery) (*Page[ChannelSummary], error) {
	params := struct {
		Query     *string           `json:"query"`
		Direction string            `json:"direction"`
		Page      remotePageRequest `json:"page"`
	}{Query: cloneStringPointer(query.Query), Direction: remoteDirection(query.Direction), Page: remotePage(query.Page)}
	var out Page[ChannelSummary]
	if err := b.call(ctx, caller, "list_channels", params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Post creates a post or reply. A clock failure only omits the timestamp, so the
// service can replay a post it already committed (Rust's retry rule).
func (b *RemoteBoard) Post(ctx context.Context, caller string, request PostRequest) (*PostMetadata, error) {
	destination, err := remoteDestination(request.Destination)
	if err != nil {
		return nil, err
	}
	notify := make([]string, 0, len(request.AgentsToNotify))
	for _, path := range request.AgentsToNotify {
		notify = append(notify, string(path))
	}
	params := struct {
		RequestID      string          `json:"request_id"`
		Destination    json.RawMessage `json:"destination"`
		Text           string          `json:"text"`
		AgentsToNotify []string        `json:"agents_to_notify"`
	}{RequestID: request.RequestID, Destination: destination, Text: request.Text, AgentsToNotify: notify}
	var out PostMetadata
	if err := b.call(ctx, caller, "post", params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListThreads lists a channel's threads.
func (b *RemoteBoard) ListThreads(ctx context.Context, caller string, query ThreadQuery) (*Page[ThreadSummary], error) {
	params := struct {
		ChannelName     string            `json:"channel_name"`
		Sort            string            `json:"sort"`
		Direction       string            `json:"direction"`
		Page            remotePageRequest `json:"page"`
		MaxCharsPerPost int               `json:"max_chars_per_post"`
	}{
		ChannelName:     query.ChannelName,
		Sort:            string(query.Sort),
		Direction:       remoteDirection(query.Direction),
		Page:            remotePage(query.Page),
		MaxCharsPerPost: remoteMaxCharsPerPost(query.MaxCharsPerPost),
	}
	var out Page[ThreadSummary]
	if err := b.call(ctx, caller, "list_threads", params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SearchPosts searches a board's posts.
func (b *RemoteBoard) SearchPosts(ctx context.Context, caller string, query PostQuery) (*Page[PostPreview], error) {
	params := struct {
		ChannelName     *string           `json:"channel_name"`
		Query           *string           `json:"query"`
		AfterMessageID  *string           `json:"after_message_id"`
		Author          *string           `json:"author"`
		Page            remotePageRequest `json:"page"`
		MaxCharsPerPost int               `json:"max_chars_per_post"`
	}{
		ChannelName:     cloneStringPointer(query.ChannelName),
		Query:           cloneStringPointer(query.Query),
		AfterMessageID:  cloneStringPointer(query.AfterMessageID),
		Author:          remoteAuthor(query.Author),
		Page:            remotePage(query.Page),
		MaxCharsPerPost: remoteMaxCharsPerPost(query.MaxCharsPerPost),
	}
	var out Page[PostPreview]
	if err := b.call(ctx, caller, "search_posts", params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ReadThread reads one thread by its root post id.
func (b *RemoteBoard) ReadThread(ctx context.Context, caller string, request ReadThreadRequest) (*ThreadPage, error) {
	params := struct {
		ThreadID        string            `json:"thread_id"`
		Page            remotePageRequest `json:"page"`
		MaxCharsPerPost int               `json:"max_chars_per_post"`
	}{
		ThreadID:        request.ThreadID,
		Page:            remotePage(request.Page),
		MaxCharsPerPost: remoteMaxCharsPerPost(request.MaxCharsPerPost),
	}
	var out ThreadPage
	if err := b.call(ctx, caller, "read_thread", params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ReadPost reads one bounded post.
func (b *RemoteBoard) ReadPost(ctx context.Context, caller string, request ReadPostRequest) (*PostContent, error) {
	var out PostContent
	if err := b.call(ctx, caller, "read_post", request, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetSubscription changes a channel or thread subscription.
func (b *RemoteBoard) SetSubscription(ctx context.Context, caller string, request SubscriptionRequest) (*SubscriptionState, error) {
	params := struct {
		Target      SubscriptionTarget `json:"target"`
		TargetAgent *agent.AgentPath   `json:"target_agent"`
		Change      string             `json:"change"`
	}{Target: request.Target, TargetAgent: request.TargetAgent, Change: remoteSubscriptionChange(request.Change)}
	var out SubscriptionState
	if err := b.call(ctx, caller, "set_subscription", params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// call sends one versioned operation. The timestamp travels only where the
// service expects it: creating a channel requires the caller's clock, a post may
// retry without one, and reads never carry one.
func (b *RemoteBoard) call(ctx context.Context, caller string, method string, params any, out any) error {
	if b == nil {
		return errors.New("message board is nil")
	}
	var timestamp *time.Time
	switch method {
	case "create_channel":
		at, err := b.clock(caller)
		if err != nil {
			return err
		}
		timestamp = &at
	case "post":
		if at, err := b.clock(caller); err == nil {
			timestamp = &at
		}
	}
	body, err := json.Marshal(struct {
		Caller    string     `json:"caller"`
		Timestamp *time.Time `json:"timestamp,omitempty"`
		Method    string     `json:"method"`
		Params    any        `json:"params"`
	}{Caller: caller, Timestamp: timestamp, Method: method, Params: params})
	if err != nil {
		return err
	}
	if len(body) > remoteBoardMaxBody {
		return invalid("board request exceeds the service limit")
	}
	response, err := b.request(ctx, http.MethodPost, "/call", body)
	if err != nil {
		return err
	}
	payload, err := b.decode(response)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("invalid board response: %w", err)
	}
	return nil
}

// request performs one authorized request against the board URL.
func (b *RemoteBoard) request(ctx context.Context, method string, suffix string, body []byte) (*http.Response, error) {
	if b == nil {
		return nil, errors.New("message board is nil")
	}
	requestCtx := ctx
	if requestCtx == nil {
		requestCtx = context.Background()
	}
	requestCtx, cancel := context.WithTimeout(requestCtx, remoteBoardRequestTimeout)
	defer cancel()
	target := *b.boardURL
	target.Path = b.boardURL.Path + suffix
	var reader io.Reader
	if body != nil {
		reader = strings.NewReader(string(body))
	}
	request, err := http.NewRequestWithContext(requestCtx, method, target.String(), reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+b.token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := b.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("board transport error: %w", err)
	}
	return response, nil
}

// decode reads a bounded response and maps a failure body to the same error kinds
// Rust's client uses: a server failure is transport, a client failure is invalid.
func (b *RemoteBoard) decode(response *http.Response) ([]byte, error) {
	if response == nil {
		return nil, errors.New("board response is missing")
	}
	defer response.Body.Close()
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		payload, err := io.ReadAll(io.LimitReader(response.Body, remoteBoardMaxResponse+1))
		if err != nil {
			return nil, fmt.Errorf("board transport error: %w", err)
		}
		if len(payload) > remoteBoardMaxResponse {
			return nil, errors.New("board response exceeds the service limit")
		}
		return payload, nil
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, remoteBoardMaxResponse+1))
	if err != nil {
		return nil, fmt.Errorf("board transport error: %w", err)
	}
	var failure struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(payload, &failure); err != nil {
		return nil, fmt.Errorf("invalid board error response (HTTP %d)", response.StatusCode)
	}
	message := failure.Code + ": " + failure.Message
	if response.StatusCode >= 500 {
		return nil, errors.New(message)
	}
	return nil, invalid(message)
}

// BoardNotification is one best-effort preview bound to the receiving turn.
type BoardNotification struct {
	Recipient string      `json:"recipient"`
	TurnID    string      `json:"turn_id"`
	Post      PostPreview `json:"post"`
}

// RemoteBoardNotifications is one active turn's live receiver.
type RemoteBoardNotifications struct {
	caller   string
	turnID   string
	reader   *bufio.Reader
	cancel   context.CancelFunc
	explicit bool
	closed   bool
}

// Notifications opens a live receiver and waits until the service confirms it is
// registered; setup has a 30-second deadline. Close it when the turn ends.
func (b *RemoteBoard) Notifications(ctx context.Context, caller string, turnID string) (*RemoteBoardNotifications, error) {
	if b == nil {
		return nil, errors.New("message board is nil")
	}
	body, err := json.Marshal(struct {
		Caller string `json:"caller"`
		TurnID string `json:"turn_id"`
	}{Caller: caller, TurnID: turnID})
	if err != nil {
		return nil, err
	}
	requestCtx, cancel := context.WithCancel(ctx)
	setupTimer := time.AfterFunc(remoteBoardSetupDeadline, cancel)
	response, err := b.notificationRequest(requestCtx, body)
	if err != nil {
		setupTimer.Stop()
		cancel()
		return nil, err
	}
	reader := bufio.NewReader(response.Body)
	event, data, err := readRemoteBoardEvent(reader)
	if err != nil {
		setupTimer.Stop()
		_ = response.Body.Close()
		cancel()
		return nil, fmt.Errorf("board notification stream: %w", err)
	}
	if event != "ready" {
		setupTimer.Stop()
		_ = response.Body.Close()
		cancel()
		return nil, errors.New("notification stream did not acknowledge readiness")
	}
	_ = data
	setupTimer.Stop()
	return &RemoteBoardNotifications{caller: caller, turnID: turnID, reader: reader, cancel: cancel}, nil
}

func (b *RemoteBoard) notificationRequest(ctx context.Context, body []byte) (*http.Response, error) {
	target := *b.boardURL
	target.Path = b.boardURL.Path + "/notifications"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+b.token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	response, err := b.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("board transport error: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, decodeErr := b.decode(response)
		if decodeErr == nil {
			decodeErr = errors.New("unexpected notification response")
		}
		return nil, decodeErr
	}
	return response, nil
}

// Next returns the next notification for this caller and turn, or nil when the
// stream ends. A notice for another agent or turn, or one whose preview exceeds
// the bound, is an error rather than something the host could inject.
func (n *RemoteBoardNotifications) Next(ctx context.Context) (*BoardNotification, error) {
	if n == nil {
		return nil, errors.New("notification receiver is nil")
	}
	for {
		if n.closed {
			return nil, nil
		}
		event, data, err := readRemoteBoardEvent(n.reader)
		if errors.Is(err, io.EOF) {
			n.close()
			return nil, nil
		}
		if err != nil {
			n.close()
			if ctx != nil && ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("board notification stream: %w", err)
		}
		// The frame byte bound is enforced before parsing, so an oversized data
		// payload can no longer arrive here.
		if event != "notification" {
			n.close()
			return nil, errors.New("invalid board notification")
		}
		var notice BoardNotification
		if err := json.Unmarshal(data, &notice); err != nil {
			n.close()
			return nil, fmt.Errorf("invalid board notification: %w", err)
		}
		if notice.Recipient != n.caller || notice.TurnID != n.turnID {
			n.close()
			return nil, errors.New("notification belongs to another agent or turn")
		}
		if utf8.RuneCountInString(notice.Post.TextPreview) > remoteBoardNotificationPreviewChars {
			n.close()
			return nil, errors.New("board notification preview exceeds the bound")
		}
		return &notice, nil
	}
}

// Close ends the receiver.
func (n *RemoteBoardNotifications) Close() {
	if n == nil {
		return
	}
	n.close()
}

func (n *RemoteBoardNotifications) close() {
	if n.closed {
		return
	}
	n.closed = true
	if n.cancel != nil {
		n.cancel()
	}
}

var (
	// errRemoteBoardFrameLimit is Rust's "board SSE frame exceeds the service
	// limit": the bound applies per frame, not per connection (#48190).
	errRemoteBoardFrameLimit = errors.New("board SSE frame exceeds the service limit")
	// errRemoteBoardFrameEncoding rejects malformed UTF-8 incrementally so the
	// decoder cannot retain it indefinitely.
	errRemoteBoardFrameEncoding = errors.New("board SSE frame is not valid UTF-8")
)

// remoteBoardFrameScanner enforces the service's per-frame byte bound and
// incremental UTF-8 validity on the wire bytes before the event parser sees
// them (Rust #48190).
type remoteBoardFrameScanner struct {
	frameBytes int
	lineEmpty  bool
	previousCR bool
	utf8       [utf8.UTFMax]byte
	utf8Len    int
}

func newRemoteBoardFrameScanner() *remoteBoardFrameScanner {
	// A frame starts as an empty line so its first blank line resets the count.
	return &remoteBoardFrameScanner{lineEmpty: true}
}

// push validates one wire byte and reports whether it completed a frame. Blank
// lines end a frame and reset the byte count, supporting LF, CR and CRLF across
// reads.
func (s *remoteBoardFrameScanner) push(value byte) (bool, error) {
	if value >= utf8.RuneSelf || s.utf8Len > 0 {
		if s.utf8Len >= len(s.utf8) {
			return false, errRemoteBoardFrameEncoding
		}
		s.utf8[s.utf8Len] = value
		s.utf8Len++
		prefix := s.utf8[:s.utf8Len]
		switch {
		case utf8.Valid(prefix):
			s.utf8Len = 0
		case !utf8.FullRune(prefix):
			// Only an incomplete code point (at most three bytes) may carry
			// across a read boundary.
		default:
			return false, errRemoteBoardFrameEncoding
		}
	}
	if s.previousCR && value != '\n' {
		if s.lineEmpty {
			s.frameBytes = 0
		}
		s.lineEmpty = true
	}
	s.frameBytes++
	if s.frameBytes > remoteBoardMaxBody {
		return false, errRemoteBoardFrameLimit
	}
	switch value {
	case '\n':
		frameEnded := s.lineEmpty
		if s.lineEmpty {
			s.frameBytes = 0
		}
		s.lineEmpty = true
		s.previousCR = false
		return frameEnded, nil
	case '\r':
		s.previousCR = true
		return false, nil
	default:
		s.lineEmpty = false
		s.previousCR = false
		return false, nil
	}
}

// readRemoteBoardEvent reads one server-sent event: its name and its data lines
// joined by newlines. io.EOF means the stream ended between events. Every wire
// byte is bounded and UTF-8 validated before the line is interpreted, and the
// caller closes the stream after an error, so neither oversized fields nor
// malformed encodings can accumulate in the parser.
func readRemoteBoardEvent(reader *bufio.Reader) (string, []byte, error) {
	if reader == nil {
		return "", nil, io.EOF
	}
	scanner := newRemoteBoardFrameScanner()
	event := ""
	var data []string
	for {
		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			if len(data) == 0 && event == "" {
				return "", nil, err
			}
			return event, []byte(strings.Join(data, "\n")), nil
		}
		for index := 0; index < len(line); index++ {
			if _, scanErr := scanner.push(line[index]); scanErr != nil {
				return "", nil, scanErr
			}
		}
		trimmed := strings.TrimRight(line, "\r\n")
		switch {
		case trimmed == "":
			if event != "" || len(data) > 0 {
				return event, []byte(strings.Join(data, "\n")), nil
			}
		case strings.HasPrefix(trimmed, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(trimmed, "event:"))
		case strings.HasPrefix(trimmed, "data:"):
			value := strings.TrimPrefix(trimmed, "data:")
			value = strings.TrimPrefix(value, " ")
			data = append(data, value)
		default:
			// Comments and other fields carry no board meaning.
		}
		if err != nil {
			return event, []byte(strings.Join(data, "\n")), nil
		}
	}
}

// remotePageRequest is Rust's PageRequest shape.
type remotePageRequest struct {
	Cursor *string `json:"cursor"`
	Limit  int     `json:"limit"`
}

func remotePage(page PageRequest) remotePageRequest {
	return remotePageRequest{Cursor: cloneStringPointer(page.Cursor), Limit: page.NormalizedLimit()}
}

// remoteDirection renders Rust's SortDirection variant names.
func remoteDirection(direction SortDirection) string {
	if direction == OldestFirst {
		return "OldestFirst"
	}
	return "NewestFirst"
}

// remoteSubscriptionChange renders Rust's SubscriptionChange variant names.
func remoteSubscriptionChange(change SubscriptionChange) string {
	if change == Unsubscribe {
		return "Unsubscribe"
	}
	return "Subscribe"
}

// remoteMaxCharsPerPost renders Rust's NonZeroU32 bound.
func remoteMaxCharsPerPost(value int) int {
	if value > 0 {
		return value
	}
	return previewChars
}

// remoteDestination renders Rust's externally tagged PostDestination enum.
func remoteDestination(destination PostDestination) (json.RawMessage, error) {
	switch strings.ToLower(strings.TrimSpace(destination.Kind)) {
	case "channel":
		if strings.TrimSpace(destination.Name) == "" {
			return nil, invalid("post destination is required")
		}
		return json.Marshal(map[string]string{"Channel": destination.Name})
	case "new_channel":
		if strings.TrimSpace(destination.Name) == "" {
			return nil, invalid("post destination is required")
		}
		return json.Marshal(map[string]string{"NewChannel": destination.Name})
	case "thread":
		if strings.TrimSpace(destination.ThreadID) == "" {
			return nil, invalid("post destination is required")
		}
		return json.Marshal(map[string]string{"Thread": destination.ThreadID})
	default:
		return nil, invalid("post destination is required")
	}
}

func remoteAuthor(path *agent.AgentPath) *string {
	if path == nil || strings.TrimSpace(string(*path)) == "" {
		return nil
	}
	value := string(*path)
	return &value
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

var _ Board = (*RemoteBoard)(nil)
