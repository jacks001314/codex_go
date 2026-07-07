package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
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
	mu         sync.Mutex
	nextID     atomic.Uint64
	pending    map[string]*pendingServerRequest
	sink       ServerRequestSink
	onResolved func(request *ServerRequest)
}

type serverRequestResult struct {
	data []byte
	err  error
}

type pendingServerRequest struct {
	ch      chan *serverRequestResult
	request *ServerRequest
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
	sink := b.register(id, &pendingServerRequest{ch: ch, request: request})
	if sink == nil {
		b.unregister(id)
		return fmt.Errorf("%w: server request sink is not configured", ErrInvalidRequest)
	}
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
		return json.Unmarshal(result.data, target)
	}
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
