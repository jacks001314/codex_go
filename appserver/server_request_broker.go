package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"codex_go/sandbox"
)

var ErrServerRequestNotFound = fmt.Errorf("%w: server request not found", ErrInvalidRequest)

type ServerRequestSink interface {
	SendServerRequest(request *ServerRequest)
}

type TargetedServerRequestSink interface {
	SendServerRequestToConnection(connectionID string, request *ServerRequest)
}

type ServerRequestSinkFunc func(request *ServerRequest)

func (f ServerRequestSinkFunc) SendServerRequest(request *ServerRequest) {
	if f != nil {
		f(request)
	}
}

type ServerRequestBroker struct {
	mu          sync.Mutex
	nextID      atomic.Uint64
	pending     map[string]*pendingServerRequest
	sink        ServerRequestSink
	onRequested func(request *ServerRequest)
	onResolved  func(request *ServerRequest)
	onResponse  func(request *ServerRequest, response *Response)
}

type serverRequestResult struct {
	data []byte
	err  error
}

type pendingServerRequest struct {
	ch           chan *serverRequestResult
	request      *ServerRequest
	connectionID string
}

func NewServerRequestBroker() *ServerRequestBroker {
	return &ServerRequestBroker{pending: map[string]*pendingServerRequest{}}
}

func (b *ServerRequestBroker) SetSink(sink ServerRequestSink) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sink = sink
}

func (b *ServerRequestBroker) SetResolvedCallback(callback func(request *ServerRequest)) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.onResolved = callback
}

func (b *ServerRequestBroker) SetRequestedCallback(callback func(request *ServerRequest)) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.onRequested = callback
}

func (b *ServerRequestBroker) SetResolvedResponseCallback(callback func(request *ServerRequest, response *Response)) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.onResponse = callback
}

func (b *ServerRequestBroker) Request(ctx context.Context, method ServerRequestMethod, params any, target any) error {
	return b.RequestToConnection(ctx, "", method, params, target)
}

func (b *ServerRequestBroker) RequestToConnection(ctx context.Context, connectionID string, method ServerRequestMethod, params any, target any) error {
	if b == nil {
		return fmt.Errorf("%w: server request broker is nil", ErrInvalidRequest)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	id := b.nextRequestID()
	ch := make(chan *serverRequestResult, 1)
	request := &ServerRequest{ID: StringID(id), Method: method, Params: params}
	sink := b.register(id, &pendingServerRequest{ch: ch, request: request, connectionID: connectionID})
	if sink == nil {
		b.unregister(id)
		return fmt.Errorf("%w: server request sink is not configured", ErrInvalidRequest)
	}
	b.notifyRequested(request)
	if connectionID != "" {
		if targeted, ok := sink.(TargetedServerRequestSink); ok {
			targeted.SendServerRequestToConnection(connectionID, request)
		} else {
			sink.SendServerRequest(request)
		}
	} else {
		sink.SendServerRequest(request)
	}
	select {
	case <-ctx.Done():
		if entry := b.unregister(id); entry != nil {
			b.notifyResolved(entry.request)
		}
		return ctx.Err()
	case result := <-ch:
		b.unregister(id)
		if result == nil {
			return fmt.Errorf("%w: empty server request response", ErrInvalidRequest)
		}
		if result.err != nil {
			return result.err
		}
		if target == nil || len(result.data) == 0 {
			return nil
		}
		if err := json.Unmarshal(result.data, target); err != nil {
			return err
		}
		return normalizeServerRequestResponse(method, params, target)
	}
}

// RejectPending resolves every pending server request for the given connection
// (including non-targeted requests delivered through the shared sink) with an
// error so waiters fail promptly instead of hanging until their context
// deadline. Rust 38035 propagates MCP elicitation delivery failures the same
// way, dropping the pending-request guard immediately.
func (b *ServerRequestBroker) RejectPending(connectionID string, err error) int {
	if b == nil {
		return 0
	}
	connectionID = strings.TrimSpace(connectionID)
	b.mu.Lock()
	rejected := make([]*pendingServerRequest, 0, len(b.pending))
	for id, entry := range b.pending {
		if entry == nil {
			continue
		}
		if entry.connectionID != connectionID && entry.connectionID != "" {
			continue
		}
		rejected = append(rejected, entry)
		delete(b.pending, id)
	}
	b.mu.Unlock()
	if len(rejected) == 0 {
		return 0
	}
	for _, entry := range rejected {
		b.notifyResolved(entry.request)
		if entry.ch != nil {
			entry.ch <- &serverRequestResult{err: err}
		}
	}
	return len(rejected)
}

func normalizeServerRequestResponse(method ServerRequestMethod, params any, target any) error {
	if method != ServerRequestPermissionsApproval {
		return nil
	}
	request, ok := params.(*PermissionsRequestApprovalParams)
	if !ok || request == nil {
		return fmt.Errorf("%w: permissions approval params have type %T", ErrInvalidRequest, params)
	}
	response, ok := target.(*PermissionsRequestApprovalResponse)
	if !ok || response == nil {
		return fmt.Errorf("%w: permissions approval response target has type %T", ErrInvalidRequest, target)
	}
	if response.StrictAutoReview != nil && *response.StrictAutoReview && response.Scope == PermissionGrantScopeSession {
		response.Permissions = &GrantedPermissionProfile{}
		response.Scope = PermissionGrantScopeTurn
		strict := false
		response.StrictAutoReview = &strict
		return nil
	}

	requestedJSON, err := json.Marshal(request.Permissions)
	if err != nil {
		return fmt.Errorf("%w: encode requested permissions: %v", ErrInvalidRequest, err)
	}
	var requested sandbox.RequestPermissionProfile
	if err := json.Unmarshal(requestedJSON, &requested); err != nil {
		return fmt.Errorf("%w: decode requested permissions: %v", ErrInvalidRequest, err)
	}
	grantedJSON, err := json.Marshal(response.Permissions)
	if err != nil {
		return fmt.Errorf("%w: encode granted permissions: %v", ErrInvalidRequest, err)
	}
	var granted sandbox.RequestPermissionProfile
	if err := json.Unmarshal(grantedJSON, &granted); err != nil {
		return fmt.Errorf("%w: decode granted permissions: %v", ErrInvalidRequest, err)
	}
	intersected := sandbox.IntersectPermissionProfiles(requested, granted, request.CWD)
	intersectedJSON, err := json.Marshal(intersected)
	if err != nil {
		return fmt.Errorf("%w: encode intersected permissions: %v", ErrInvalidRequest, err)
	}
	var permissions GrantedPermissionProfile
	if err := json.Unmarshal(intersectedJSON, &permissions); err != nil {
		return fmt.Errorf("%w: decode intersected permissions: %v", ErrInvalidRequest, err)
	}
	response.Permissions = &permissions
	return nil
}

func (b *ServerRequestBroker) notifyRequested(request *ServerRequest) {
	callback := b.requestedCallback()
	if callback != nil {
		callback(request)
	}
}

func (b *ServerRequestBroker) requestedCallback() func(request *ServerRequest) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.onRequested
}

func (b *ServerRequestBroker) Resolve(response *Response) (bool, error) {
	if b == nil || response == nil || response.ID.IsZero() {
		return false, nil
	}
	id := response.ID.String()
	entry := b.pendingEntry(id)
	if entry == nil || entry.ch == nil {
		return false, nil
	}
	b.notifyResolved(entry.request)
	b.notifyResolvedResponse(entry.request, response)
	if response.Error != nil {
		entry.ch <- &serverRequestResult{err: fmt.Errorf("server request failed: code=%d message=%s", response.Error.Code, response.Error.Message)}
		return true, nil
	}
	data, err := json.Marshal(response.Result)
	if err != nil {
		entry.ch <- &serverRequestResult{err: err}
		return true, nil
	}
	entry.ch <- &serverRequestResult{data: data}
	return true, nil
}

func (b *ServerRequestBroker) ReplayThread(threadID string) int {
	threadID = strings.TrimSpace(threadID)
	if b == nil || threadID == "" {
		return 0
	}
	requests, sink := b.pendingRequestsForThread(threadID)
	if sink == nil {
		return 0
	}
	for _, request := range requests {
		sink.SendServerRequest(request)
	}
	return len(requests)
}

func (b *ServerRequestBroker) nextRequestID() string {
	return fmt.Sprintf("server-request-%d", b.nextID.Add(1))
}

func (b *ServerRequestBroker) register(id string, entry *pendingServerRequest) ServerRequestSink {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.pending == nil {
		b.pending = map[string]*pendingServerRequest{}
	}
	b.pending[id] = entry
	return b.sink
}

func (b *ServerRequestBroker) unregister(id string) *pendingServerRequest {
	b.mu.Lock()
	defer b.mu.Unlock()
	entry := b.pending[id]
	delete(b.pending, id)
	return entry
}

func (b *ServerRequestBroker) pendingEntry(id string) *pendingServerRequest {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.pending[id]
}

func (b *ServerRequestBroker) pendingRequestsForThread(threadID string) ([]*ServerRequest, ServerRequestSink) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.pending) == 0 || b.sink == nil {
		return nil, b.sink
	}
	ids := make([]string, 0, len(b.pending))
	for id, entry := range b.pending {
		if entry == nil || entry.request == nil {
			continue
		}
		if serverRequestThreadID(entry.request) == threadID {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	requests := make([]*ServerRequest, 0, len(ids))
	for _, id := range ids {
		if entry := b.pending[id]; entry != nil && entry.request != nil {
			request := *entry.request
			requests = append(requests, &request)
		}
	}
	return requests, b.sink
}

func (b *ServerRequestBroker) notifyResolved(request *ServerRequest) {
	callback := b.resolvedCallback()
	if callback != nil {
		callback(request)
	}
}

func (b *ServerRequestBroker) resolvedCallback() func(request *ServerRequest) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.onResolved
}

func (b *ServerRequestBroker) notifyResolvedResponse(request *ServerRequest, response *Response) {
	callback := b.resolvedResponseCallback()
	if callback != nil {
		callback(request, response)
	}
}

func (b *ServerRequestBroker) resolvedResponseCallback() func(request *ServerRequest, response *Response) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.onResponse
}
