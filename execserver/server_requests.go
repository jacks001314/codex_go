package execserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

const networkPolicyTransportTimeoutMargin = 5 * time.Second

type serverRequestResult struct {
	response clientResponse
	err      error
}

type serverRequestSender struct {
	mu      sync.Mutex
	write   func(any) error
	pending map[int64]chan serverRequestResult
	nextID  int64
	slots   chan struct{}
	done    chan struct{}
	closed  bool
}

type serverRequestSenderContextKey struct{}

func newServerRequestSender(write func(any) error) *serverRequestSender {
	return &serverRequestSender{
		write:   write,
		pending: map[int64]chan serverRequestResult{},
		nextID:  1,
		slots:   make(chan struct{}, MaxInFlightServerRequests),
		done:    make(chan struct{}),
	}
}

func withServerRequestSender(ctx context.Context, sender *serverRequestSender) context.Context {
	return context.WithValue(ctx, serverRequestSenderContextKey{}, sender)
}

func serverRequestSenderFromContext(ctx context.Context) *serverRequestSender {
	if ctx == nil {
		return nil
	}
	sender, _ := ctx.Value(serverRequestSenderContextKey{}).(*serverRequestSender)
	return sender
}

func (s *serverRequestSender) call(ctx context.Context, method string, params any, timeout time.Duration, target any) error {
	if s == nil {
		return errors.New("exec-server client connection is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		return fmt.Errorf("exec-server pending server request limit exceeded: %d", MaxInFlightServerRequests)
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("exec-server client connection is closed")
	}
	id := s.nextID
	s.nextID++
	responseCh := make(chan serverRequestResult, 1)
	s.pending[id] = responseCh
	write := s.write
	s.mu.Unlock()
	defer s.remove(id)

	if write == nil {
		return errors.New("exec-server client connection cannot send requests")
	}
	if err := write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return err
	}

	var timer <-chan time.Time
	var stop func() bool
	if timeout > 0 {
		t := time.NewTimer(timeout)
		timer = t.C
		stop = t.Stop
		defer stop()
	}
	select {
	case result := <-responseCh:
		if result.err != nil {
			return result.err
		}
		if result.response.Error != nil {
			return fmt.Errorf("exec-server client %s failed (%d): %s", method, result.response.Error.Code, result.response.Error.Message)
		}
		if target == nil {
			return nil
		}
		if err := json.Unmarshal(result.response.Result, target); err != nil {
			return fmt.Errorf("decode exec-server client %s response: %w", method, err)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer:
		return fmt.Errorf("exec-server client %s timed out after %s", method, timeout)
	case <-s.done:
		return errors.New("exec-server client connection is closed")
	}
}

func (s *serverRequestSender) remove(id int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	delete(s.pending, id)
	s.mu.Unlock()
}

// complete accepts both a live response and a late response to an ID this
// connection previously issued. Unknown or client-generated IDs are protocol
// errors, matching Rust's RpcServerRequestSender::complete contract.
func (s *serverRequestSender) complete(data []byte) bool {
	if s == nil {
		return false
	}
	var response clientResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return false
	}
	id, ok := clientResponseID(response.ID)
	if !ok || id <= 0 {
		return false
	}
	s.mu.Lock()
	responseCh := s.pending[id]
	delete(s.pending, id)
	knownLate := responseCh == nil && id < s.nextID
	s.mu.Unlock()
	if responseCh != nil {
		responseCh <- serverRequestResult{response: response}
		return true
	}
	return knownLate
}

func (s *serverRequestSender) close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	close(s.done)
	pending := s.pending
	s.pending = map[int64]chan serverRequestResult{}
	s.mu.Unlock()
	for _, responseCh := range pending {
		responseCh <- serverRequestResult{err: errors.New("exec-server client connection is closed")}
	}
}

func consumeClientResponse(ctx context.Context, data []byte) (bool, bool) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		return false, false
	}
	if _, hasMethod := envelope["method"]; hasMethod {
		return false, false
	}
	if _, hasID := envelope["id"]; !hasID {
		return false, false
	}
	_, hasResult := envelope["result"]
	_, hasError := envelope["error"]
	if !hasResult && !hasError {
		return false, false
	}
	sender := serverRequestSenderFromContext(ctx)
	return true, sender != nil && sender.complete(data)
}
