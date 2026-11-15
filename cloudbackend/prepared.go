package cloudbackend

// Rust #61490: a Cloud task selection prepares a replacement attachment on a
// scoped history handle and commits it only after the new history has loaded.
// A failed or abandoned preparation leaves the displayed conversation, its
// draft, and its reply stream untouched.

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	"codex_go/appserver"
	"codex_go/cloudclient"
)

// preparedChannelSize bounds the queued prepare/history/commit/discard
// operations without blocking a caller that has not started waiting yet.
const preparedChannelSize = 8

// preparedEventLimit mirrors Rust PREPARED_EVENT_LIMIT: a commit buffers the
// events already queued on the provisional stream so they are replayed in order
// after the attachment becomes active.
const preparedEventLimit = 8

var errPreparedMissing = errors.New("Cloud resume commit requires its scoped history handle.")

type preparedRequestKind int

const (
	preparedPrepare preparedRequestKind = iota
	preparedHistory
	preparedCommit
	preparedDiscard
)

type preparedRequest struct {
	kind     preparedRequestKind
	threadID string
	method   appserver.Method
	params   json.RawMessage
	ctx      context.Context
	reply    chan requestReply
}

// preparedState is dispatch-goroutine state for one provisional attachment.
type preparedState struct {
	threadID         string
	settings         *appserver.ThreadResumeResponse
	opened           *openedAttachment
	prefix           []cloudclient.Event
	warning          *Notification
	canceledRecovery string
	canceledOpening  bool
}

// PreparedResume is a provisional Cloud attachment. Its Request only serves
// the prepared conversation's history, Commit installs the attachment, and
// Discard abandons it.
type PreparedResume struct {
	client   *Client
	threadID string

	committed bool
	discarded bool
}

// ThreadID reports the conversation the preparation targets.
func (p *PreparedResume) ThreadID() string {
	if p == nil {
		return ""
	}
	return p.threadID
}

// Request reads one history page from the provisional attachment. Only
// thread/turns/list and thread/items/list for the prepared conversation are
// accepted.
func (p *PreparedResume) Request(ctx context.Context, method appserver.Method, params any) (json.RawMessage, *RPCError) {
	if p == nil || p.client == nil {
		return nil, &RPCError{Code: internalErrorCode, Message: "Cloud resume preparation is unavailable"}
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		return nil, internalRPCError(err)
	}
	return p.client.preparedRequest(ctx, &preparedRequest{
		kind:     preparedHistory,
		threadID: p.threadID,
		method:   method,
		params:   cloneRawMessage(encoded),
	})
}

// Commit installs the prepared attachment before the next ordinary command.
func (p *PreparedResume) Commit(ctx context.Context) *RPCError {
	if p == nil || p.client == nil {
		return &RPCError{Code: internalErrorCode, Message: "Cloud resume preparation is unavailable"}
	}
	_, rpcErr := p.client.preparedRequest(ctx, &preparedRequest{
		kind:     preparedCommit,
		threadID: p.threadID,
	})
	if rpcErr == nil {
		p.committed = true
	}
	return rpcErr
}

// Discard abandons a preparation that was neither committed nor failed. It is
// safe to call after Commit and safe to call more than once.
func (p *PreparedResume) Discard() {
	if p == nil || p.client == nil || p.committed || p.discarded {
		return
	}
	p.discarded = true
	p.client.discardPreparedHandle(p.threadID)
}

// PrepareThreadResume opens a provisional attachment and returns the activation
// snapshot plus the scoped history handle. The attachment does not displace the
// displayed conversation until PreparedResume.Commit.
func (c *Client) PrepareThreadResume(ctx context.Context, params appserver.ThreadResumeParams) (*appserver.ThreadResumeResponse, *PreparedResume, *RPCError) {
	if c == nil {
		return nil, nil, &RPCError{Code: internalErrorCode, Message: "cloud client is unavailable"}
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		return nil, nil, internalRPCError(err)
	}
	result, rpcErr := c.preparedRequest(ctx, &preparedRequest{
		kind:     preparedPrepare,
		threadID: strings.TrimSpace(params.ThreadID),
		params:   encoded,
	})
	if rpcErr != nil {
		return nil, nil, rpcErr
	}
	var settings appserver.ThreadResumeResponse
	if err := json.Unmarshal(result, &settings); err != nil || settings.Thread == nil {
		return nil, nil, &RPCError{Code: internalErrorCode, Message: "Cloud resume did not return attachment settings."}
	}
	return &settings, &PreparedResume{client: c, threadID: strings.TrimSpace(settings.Thread.ID)}, nil
}

func (c *Client) preparedRequest(ctx context.Context, request *preparedRequest) (json.RawMessage, *RPCError) {
	if ctx == nil {
		ctx = context.Background()
	}
	request.ctx = ctx
	request.reply = make(chan requestReply, 1)
	select {
	case c.prepared <- request:
	case <-c.done:
		return nil, &RPCError{Code: internalErrorCode, Message: "cloud client is closed"}
	case <-ctx.Done():
		return nil, &RPCError{Code: internalErrorCode, Message: ctx.Err().Error()}
	}
	select {
	case reply := <-request.reply:
		return reply.result, reply.err
	case <-c.done:
		return nil, &RPCError{Code: internalErrorCode, Message: "cloud client is closed"}
	case <-ctx.Done():
		return nil, &RPCError{Code: internalErrorCode, Message: ctx.Err().Error()}
	}
}

// discardPreparedHandle abandons one preparation by thread id.
func (c *Client) discardPreparedHandle(threadID string) {
	if c == nil {
		return
	}
	_, _ = c.preparedRequest(context.Background(), &preparedRequest{
		kind:     preparedDiscard,
		threadID: strings.TrimSpace(threadID),
	})
}

func (c *Client) handlePreparedRequest(request *preparedRequest) {
	switch request.kind {
	case preparedPrepare:
		result, rpcErr := c.prepareThreadResume(request)
		request.reply <- requestReply{result: result, err: rpcErr}
	case preparedHistory:
		result, rpcErr := c.preparedHistory(request)
		request.reply <- requestReply{result: result, err: rpcErr}
	case preparedCommit:
		result, rpcErr := c.commitPreparedResume(request)
		request.reply <- requestReply{result: result, err: rpcErr}
	case preparedDiscard:
		c.discardPrepared()
		request.reply <- requestReply{result: json.RawMessage("null")}
	default:
		request.reply <- requestReply{err: &RPCError{Code: internalErrorCode, Message: "unknown Cloud resume operation"}}
	}
}

func (c *Client) prepareThreadResume(request *preparedRequest) (json.RawMessage, *RPCError) {
	var resume appserver.ThreadResumeParams
	if err := json.Unmarshal(request.params, &resume); err != nil {
		return nil, internalRPCError(err)
	}
	if rpcErr := validateCloudResumeParams(resume); rpcErr != nil {
		return nil, rpcErr
	}
	threadID := strings.TrimSpace(resume.ThreadID)
	// A new deliberate selection supersedes any provisional attachment.
	c.discardPrepared()
	canceledRoot, canceledOpening := c.cancelIdleRecovery()

	ctx := request.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	activation, err := waitWhileListening(c, ctx, func() (*cloudclient.ThreadActivation, error) {
		return c.source.Activate(ctx, threadID)
	})
	if err != nil {
		if canceledOpening {
			c.reportStreamLoss(canceledRoot)
		}
		return nil, internalRPCError(err)
	}
	_, settings, err := resolveThreadActivation(activation)
	if err != nil {
		if canceledOpening {
			c.reportStreamLoss(canceledRoot)
		}
		return nil, internalRPCError(err)
	}
	var opened *openedAttachment
	// A healthy existing attachment for this conversation is reused.
	if c.active == nil || c.active.threadID != threadID {
		opened, err = c.openAttachment(ctx, threadID)
		if err != nil {
			if canceledOpening {
				c.reportStreamLoss(canceledRoot)
			}
			return nil, internalRPCError(err)
		}
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		if opened != nil {
			opened.cancel()
		}
		return nil, internalRPCError(err)
	}
	c.preparedState = &preparedState{
		threadID:         threadID,
		settings:         settings,
		opened:           opened,
		canceledRecovery: canceledRoot,
		canceledOpening:  canceledOpening,
	}
	return encoded, nil
}

// validateCloudResumeParams mirrors resumeThread's accepted shape so a prepared
// resume and an ordinary resume reject the same unsupported fields.
func validateCloudResumeParams(resume appserver.ThreadResumeParams) *RPCError {
	supported := appserver.ThreadResumeParams{ThreadID: resume.ThreadID, ExcludeTurns: true}
	normalized := resume
	normalized.CWD = nil
	if !reflect.DeepEqual(normalized, supported) {
		return internalRPCError(errors.New("Cloud conversations resume by ID with saved settings. Set excludeTurns to true and load history separately."))
	}
	if strings.TrimSpace(resume.ThreadID) == "" {
		return internalRPCError(errors.New("threadId is required"))
	}
	return nil
}

func (c *Client) preparedHistory(request *preparedRequest) (json.RawMessage, *RPCError) {
	state := c.preparedState
	if state == nil || state.threadID != request.threadID {
		return nil, &RPCError{Code: invalidParamsCode, Message: errPreparedMissing.Error()}
	}
	if request.method != appserver.MethodThreadTurnsList && request.method != appserver.MethodThreadItemsList {
		return nil, &RPCError{Code: internalErrorCode, Message: "Only the prepared Cloud conversation's history may be read here."}
	}
	if !preparedHistoryTargets(request.method, request.params, state.threadID) {
		return nil, &RPCError{Code: internalErrorCode, Message: "Only the prepared Cloud conversation's history may be read here."}
	}
	ctx := request.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	// Page warnings belong to the preparation until it commits. Ordinary
	// requests keep their own warning marker between scoped pages.
	previousMarker := c.warnedPartialHistory
	var captured Notification
	c.warningSink = &captured
	result, rpcErr := c.handleRequest(ctx, request.method, request.params, cloudclient.SubmissionStartIfIdle, false)
	c.warningSink = nil
	c.warnedPartialHistory = previousMarker
	if rpcErr == nil && captured.Method != "" && state.warning == nil {
		state.warning = &captured
	}
	return result, rpcErr
}

func preparedHistoryTargets(method appserver.Method, params json.RawMessage, threadID string) bool {
	switch method {
	case appserver.MethodThreadTurnsList:
		var list appserver.ThreadTurnsListParams
		if err := json.Unmarshal(params, &list); err != nil {
			return false
		}
		return strings.TrimSpace(list.ThreadID) == threadID
	case appserver.MethodThreadItemsList:
		var list appserver.ThreadItemsListParams
		if err := json.Unmarshal(params, &list); err != nil {
			return false
		}
		return strings.TrimSpace(list.ThreadID) == threadID
	default:
		return false
	}
}

func (c *Client) commitPreparedResume(request *preparedRequest) (json.RawMessage, *RPCError) {
	state := c.preparedState
	if state == nil || state.threadID != request.threadID {
		return nil, &RPCError{Code: invalidParamsCode, Message: errPreparedMissing.Error()}
	}
	if state.opened != nil {
		prefix, err := probePreparedStream(state.opened)
		if err != nil {
			c.discardPrepared()
			return nil, internalRPCError(err)
		}
		state.prefix = prefix
		c.installReplacement(state)
	} else if c.active == nil || c.active.threadID != state.threadID {
		// The reused attachment ended while the history was loading.
		c.discardPrepared()
		return nil, &RPCError{Code: invalidParamsCode, Message: errPreparedMissing.Error()}
	}
	c.loaded = state.settings
	if state.warning != nil {
		c.warnedPartialHistory = state.threadID
		c.enqueue(*state.warning)
	} else {
		c.warnedPartialHistory = ""
	}
	c.preparedState = nil
	return json.RawMessage("null"), nil
}

// installReplacement makes a provisionally opened stream the active
// attachment and replays the buffered prefix in order.
func (c *Client) installReplacement(state *preparedState) {
	opened := state.opened
	if opened == nil {
		return
	}
	threadID := state.threadID
	delete(c.status, threadID)
	c.resetIdleRecovery()
	c.recovery.installed = time.Now()
	c.active = &attachment{threadID: threadID, events: opened.events, errs: opened.errs, cancel: opened.cancel}
	c.supersedeStreamLosses(threadID)
	c.notifyStreamReconnected(threadID)
	if opened.first != nil {
		c.forwardEvent(threadID, *opened.first)
	}
	for _, event := range state.prefix {
		c.forwardEvent(threadID, event)
	}
	state.opened = nil
}

// probePreparedStream buffers a short prefix of events already queued on the
// provisional stream and fails when the stream closed before committing.
func probePreparedStream(opened *openedAttachment) ([]cloudclient.Event, error) {
	prefix := make([]cloudclient.Event, 0, preparedEventLimit)
	for {
		if len(prefix) >= preparedEventLimit {
			return prefix, nil
		}
		select {
		case event, ok := <-opened.events:
			if !ok {
				return nil, errors.New("Cloud conversation activated but its stream did not connect. Reopen it before sending.")
			}
			prefix = append(prefix, event)
			continue
		default:
		}
		select {
		case err, ok := <-opened.errs:
			if !ok || err != nil {
				return nil, errors.New("Cloud conversation activated but its stream did not connect. Reopen it before sending.")
			}
			opened.errs = nil
		default:
		}
		return prefix, nil
	}
}

func (c *Client) discardPrepared() {
	state := c.preparedState
	if state == nil {
		return
	}
	c.preparedState = nil
	if state.opened != nil {
		state.opened.cancel()
	}
	if state.canceledOpening {
		c.reportStreamLoss(state.canceledRecovery)
	}
}

// pendingUnloadsPrepared reports whether a request unloads the provisional
// conversation (Rust run_worker's `unloads`).
func (c *Client) pendingUnloadsPrepared(pending *pendingRequest) bool {
	state := c.preparedState
	if pending == nil || state == nil {
		return false
	}
	switch pending.method {
	case appserver.MethodThreadArchive:
		var params appserver.ThreadArchiveParams
		return json.Unmarshal(pending.params, &params) == nil && strings.TrimSpace(params.ThreadID) == state.threadID
	case appserver.MethodThreadUnsubscribe:
		var params appserver.ThreadUnsubscribeParams
		return json.Unmarshal(pending.params, &params) == nil && strings.TrimSpace(params.ThreadID) == state.threadID
	default:
		return false
	}
}
