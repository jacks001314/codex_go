package codemode

import (
	"context"
	"encoding/json"
	"time"

	codemodev1 "codex_go/codemode/grpc"
)

type grpcHeartbeatUpdate struct {
	session SessionID
	seconds uint32
}
type grpcHeartbeatWrite struct {
	message any
	done    chan error
}

// grpcHeartbeatWriter owns Send: normal output has priority over idle markers,
// and there is never a backlog of heartbeat frames behind a slow client.
// Transport returns on cancellation even if Send is flow-control blocked;
// returning the RPC then releases the blocked gRPC Send.
type grpcHeartbeatWriter struct {
	ctx     context.Context
	writes  chan grpcHeartbeatWrite
	updates chan grpcHeartbeatUpdate
	done    chan error
	stopped chan struct{}
}

func newGrpcHeartbeatWriter(ctx context.Context, send func(*codemodev1.FramedMessage) error) *grpcHeartbeatWriter {
	w := &grpcHeartbeatWriter{ctx: ctx, writes: make(chan grpcHeartbeatWrite), updates: make(chan grpcHeartbeatUpdate), done: make(chan error, 1), stopped: make(chan struct{})}
	go func() {
		err := w.run(send)
		w.done <- err
		close(w.stopped)
	}()
	return w
}
func (w *grpcHeartbeatWriter) write(message any) error {
	request := grpcHeartbeatWrite{message: message, done: make(chan error, 1)}
	select {
	case w.writes <- request:
	case <-w.ctx.Done():
		return w.ctx.Err()
	case <-w.stopped:
		return context.Canceled
	}
	select {
	case err := <-request.done:
		return err
	case <-w.ctx.Done():
		return w.ctx.Err()
	case <-w.stopped:
		return context.Canceled
	}
}
func (w *grpcHeartbeatWriter) configure(session SessionID, seconds uint32) {
	select {
	case w.updates <- grpcHeartbeatUpdate{session, seconds}:
	case <-w.ctx.Done():
	case <-w.stopped:
	}
}
func (w *grpcHeartbeatWriter) run(send func(*codemodev1.FramedMessage) error) error {
	type idle struct {
		interval time.Duration
		deadline time.Time
	}
	sessions := map[SessionID]idle{}
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	sendMessage := func(message any) error {
		if err := w.ctx.Err(); err != nil {
			return err
		}
		payload, err := json.Marshal(message)
		if err != nil {
			return err
		}
		return send(&codemodev1.FramedMessage{Payload: payload})
	}
	normal := func(request grpcHeartbeatWrite) error {
		err := sendMessage(request.message)
		request.done <- err
		// One shared Transport carries both event and callback output. Any normal
		// output resets the physical stream's idle deadlines, unlike an inbound request.
		now := time.Now()
		for id, state := range sessions {
			state.deadline = now.Add(state.interval)
			sessions[id] = state
		}
		return err
	}
	for {
		if err := w.ctx.Err(); err != nil {
			return err
		}
		// Match Rust's biased select: ready application output wins over a timer.
		select {
		case request := <-w.writes:
			if err := normal(request); err != nil {
				return err
			}
			continue
		default:
		}
		var due SessionID
		var deadline time.Time
		for id, state := range sessions {
			if deadline.IsZero() || state.deadline.Before(deadline) {
				due = id
				deadline = state.deadline
			}
		}
		var tick <-chan time.Time
		if !deadline.IsZero() {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(time.Until(deadline))
			tick = timer.C
		}
		select {
		case <-w.ctx.Done():
			return w.ctx.Err()
		case update := <-w.updates:
			if update.seconds == 0 {
				delete(sessions, update.session)
			} else {
				interval := time.Duration(update.seconds) * time.Second
				sessions[update.session] = idle{interval, time.Now().Add(interval)}
			}
		case request := <-w.writes:
			if err := normal(request); err != nil {
				return err
			}
		case <-tick:
			// A normal frame may have arrived with the tick. It still wins.
			select {
			case request := <-w.writes:
				if err := normal(request); err != nil {
					return err
				}
				continue
			default:
			}
			if err := sendMessage(HeartbeatMessage(due)); err != nil {
				return err
			}
			state := sessions[due]
			state.deadline = time.Now().Add(state.interval)
			sessions[due] = state
		}
	}
}
