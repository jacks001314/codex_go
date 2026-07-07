package remotecontrol

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestRemoteControlWebsocketLoopConnectsAndForwardsRemoteClient(t *testing.T) {
	manager := NewManager("codex", "installation-id")
	manager.Enable(&EnableParams{Ephemeral: true})
	clientConnCh := make(chan *websocket.Conn, 1)
	loop := NewRemoteControlWebsocketLoop(manager, &RemoteControlWebsocketLoopOptions{
		StatusPollInterval:        time.Millisecond,
		ConnectionShutdownTimeout: 100 * time.Millisecond,
		ReconnectDelay: func(reconnectAttempt *uint64) (time.Duration, bool) {
			return time.Millisecond, false
		},
		Connect: func(ctx context.Context, options *RemoteControlWebsocketConnectOptions) (*websocket.Conn, *http.Response, error) {
			clientConn, serverConn := connectedRemoteControlWebsocketPair(t)
			select {
			case clientConnCh <- clientConn:
			case <-ctx.Done():
				_ = clientConn.CloseNow()
				_ = serverConn.CloseNow()
				return nil, nil, ctx.Err()
			}
			return serverConn, nil, nil
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- loop.Run(ctx)
	}()

	var clientConn *websocket.Conn
	select {
	case clientConn = <-clientConnCh:
	case <-time.After(2 * time.Second):
		t.Fatal("remote control websocket loop did not connect")
	}

	streamID := StreamID("stream-1")
	seqID := uint64(1)
	writeClientEnvelope(t, clientConn, &ClientEnvelope{
		Type:     ClientEventClientMessage,
		Message:  []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"remote","version":"1"},"capabilities":{"experimentalApi":true}}}`),
		ClientID: "client-1",
		StreamID: &streamID,
		SeqID:    &seqID,
	})

	opened := readTransportEvent(t, loop.TransportEvents())
	if opened.Type != RemoteClientConnectionOpened || opened.ClientID != "client-1" || opened.StreamID != streamID {
		t.Fatalf("opened event = %+v", opened)
	}
	incoming := readTransportEvent(t, loop.TransportEvents())
	if incoming.Type != RemoteClientIncomingMessage || incoming.ConnectionID != opened.ConnectionID {
		t.Fatalf("incoming event = %+v, opened = %+v", incoming, opened)
	}
	if status := manager.StatusChanged(); status.Status != StatusConnected {
		t.Fatalf("manager status = %+v, want connected", status)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("loop returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("remote control websocket loop did not stop")
	}
}

func TestRemoteControlWebsocketLoopAuthChangeWakesReconnectBackoff(t *testing.T) {
	manager := NewManager("codex", "installation-id")
	manager.Enable(&EnableParams{Ephemeral: true})
	var resetCount atomic.Int64
	manager.ConfigureBackend(&ManagerBackendOptions{AuthRecoveryReset: func() {
		resetCount.Add(1)
	}})
	var authRevision atomic.Uint64
	var connectCount atomic.Int64
	connectAttempts := make(chan int64, 4)
	delayAttempts := make(chan uint64, 4)
	loop := NewRemoteControlWebsocketLoop(manager, &RemoteControlWebsocketLoopOptions{
		StatusPollInterval:        time.Millisecond,
		ConnectionShutdownTimeout: 100 * time.Millisecond,
		AuthRevision: func(context.Context) (uint64, error) {
			return authRevision.Load(), nil
		},
		ReconnectDelay: func(reconnectAttempt *uint64) (time.Duration, bool) {
			if reconnectAttempt != nil {
				delayAttempts <- *reconnectAttempt
				*reconnectAttempt = *reconnectAttempt + 1
			}
			return time.Hour, false
		},
		Connect: func(context.Context, *RemoteControlWebsocketConnectOptions) (*websocket.Conn, *http.Response, error) {
			attempt := connectCount.Add(1)
			connectAttempts <- attempt
			return nil, nil, fmt.Errorf("connect failed")
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- loop.Run(ctx)
	}()

	select {
	case attempt := <-connectAttempts:
		if attempt != 1 {
			t.Fatalf("first connect attempt = %d", attempt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("remote control websocket loop did not attempt first connect")
	}
	select {
	case attempt := <-delayAttempts:
		if attempt != 0 {
			t.Fatalf("first reconnect attempt = %d, want 0", attempt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("remote control websocket loop did not enter reconnect backoff")
	}
	if status := manager.StatusChanged(); status.Status != StatusErrored {
		t.Fatalf("manager status after failed connect = %+v, want errored", status)
	}

	authRevision.Add(1)

	select {
	case attempt := <-connectAttempts:
		if attempt != 2 {
			t.Fatalf("second connect attempt = %d", attempt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("auth revision change did not wake reconnect backoff")
	}
	select {
	case attempt := <-delayAttempts:
		if attempt != 0 {
			t.Fatalf("reconnect attempt after auth change = %d, want reset to 0", attempt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("remote control websocket loop did not enter second reconnect backoff")
	}
	if got := resetCount.Load(); got != 1 {
		t.Fatalf("auth recovery resets after auth change = %d, want 1", got)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("loop returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("remote control websocket loop did not stop")
	}
}

func TestRemoteControlWebsocketLoopAuthRevisionDuringConnectWakesWithoutRecoveryChange(t *testing.T) {
	manager := NewManager("codex", "installation-id")
	manager.Enable(&EnableParams{Ephemeral: true})
	var authRevision atomic.Uint64
	var connectCount atomic.Int64
	connectAttempts := make(chan int64, 4)
	delayAttempts := make(chan struct{}, 4)
	loop := NewRemoteControlWebsocketLoop(manager, &RemoteControlWebsocketLoopOptions{
		StatusPollInterval:        time.Millisecond,
		ConnectionShutdownTimeout: 100 * time.Millisecond,
		AuthRevision: func(context.Context) (uint64, error) {
			return authRevision.Load(), nil
		},
		ReconnectDelay: func(*uint64) (time.Duration, bool) {
			delayAttempts <- struct{}{}
			return time.Hour, false
		},
		Connect: func(context.Context, *RemoteControlWebsocketConnectOptions) (*websocket.Conn, *http.Response, error) {
			attempt := connectCount.Add(1)
			connectAttempts <- attempt
			if attempt == 1 {
				authRevision.Store(1)
			}
			return nil, nil, fmt.Errorf("connect failed")
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- loop.Run(ctx)
	}()

	select {
	case attempt := <-connectAttempts:
		if attempt != 1 {
			t.Fatalf("first connect attempt = %d", attempt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("remote control websocket loop did not attempt first connect")
	}
	select {
	case <-delayAttempts:
	case <-time.After(2 * time.Second):
		t.Fatal("remote control websocket loop did not enter reconnect backoff")
	}
	select {
	case attempt := <-connectAttempts:
		if attempt != 2 {
			t.Fatalf("second connect attempt = %d", attempt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("auth revision changed during connect did not wake reconnect")
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("loop returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("remote control websocket loop did not stop")
	}
}

func TestRemoteControlWebsocketLoopRecoveryRevisionIsMarkedSeen(t *testing.T) {
	manager := NewManager("codex", "installation-id")
	manager.Enable(&EnableParams{Ephemeral: true})
	var recoveryChanged atomic.Bool
	recoveryChanged.Store(true)
	manager.ConfigureBackend(&ManagerBackendOptions{AuthRecoveryChanged: func() bool {
		if recoveryChanged.Load() {
			recoveryChanged.Store(false)
			return true
		}
		return false
	}})
	var authRevision atomic.Uint64
	var connectCount atomic.Int64
	connectAttempts := make(chan int64, 4)
	delayAttempts := make(chan struct{}, 4)
	loop := NewRemoteControlWebsocketLoop(manager, &RemoteControlWebsocketLoopOptions{
		StatusPollInterval:        time.Millisecond,
		ConnectionShutdownTimeout: 100 * time.Millisecond,
		AuthRevision: func(context.Context) (uint64, error) {
			return authRevision.Load(), nil
		},
		ReconnectDelay: func(*uint64) (time.Duration, bool) {
			delayAttempts <- struct{}{}
			return time.Hour, false
		},
		Connect: func(context.Context, *RemoteControlWebsocketConnectOptions) (*websocket.Conn, *http.Response, error) {
			attempt := connectCount.Add(1)
			connectAttempts <- attempt
			if attempt == 1 {
				authRevision.Store(1)
			}
			return nil, nil, fmt.Errorf("connect failed")
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- loop.Run(ctx)
	}()

	select {
	case attempt := <-connectAttempts:
		if attempt != 1 {
			t.Fatalf("first connect attempt = %d", attempt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("remote control websocket loop did not attempt first connect")
	}
	select {
	case <-delayAttempts:
	case <-time.After(2 * time.Second):
		t.Fatal("remote control websocket loop did not enter reconnect backoff")
	}
	select {
	case attempt := <-connectAttempts:
		t.Fatalf("recovery auth revision woke reconnect as attempt %d", attempt)
	case <-time.After(100 * time.Millisecond):
	}

	authRevision.Store(2)
	select {
	case attempt := <-connectAttempts:
		if attempt != 2 {
			t.Fatalf("second connect attempt = %d", attempt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("racing auth revision did not wake reconnect")
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("loop returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("remote control websocket loop did not stop")
	}
}

func TestRemoteControlWebsocketLoopMarksOnlyRecoveryAuthRevisionSeen(t *testing.T) {
	var authRevision atomic.Uint64
	loop := NewRemoteControlWebsocketLoop(NewManager("codex", "installation-id"), &RemoteControlWebsocketLoopOptions{
		AuthRevision: func(context.Context) (uint64, error) {
			return authRevision.Load(), nil
		},
	})
	revision := uint64(0)
	known := true

	authRevision.Store(1)
	loop.markSingleRecoveryAuthChangeSeen(context.Background(), &revision, &known)
	if revision != 1 {
		t.Fatalf("revision after recovery mark = %d, want 1", revision)
	}
	if loop.observeAuthRevision(context.Background(), &revision, &known) {
		t.Fatal("recovery's own auth revision should not wake reconnect loop")
	}
}

func TestRemoteControlWebsocketLoopPreservesRacingAuthRevision(t *testing.T) {
	var authRevision atomic.Uint64
	loop := NewRemoteControlWebsocketLoop(NewManager("codex", "installation-id"), &RemoteControlWebsocketLoopOptions{
		AuthRevision: func(context.Context) (uint64, error) {
			return authRevision.Load(), nil
		},
	})
	revision := uint64(0)
	known := true

	authRevision.Store(2)
	loop.markSingleRecoveryAuthChangeSeen(context.Background(), &revision, &known)
	if revision != 0 {
		t.Fatalf("revision after racing recovery mark = %d, want unchanged 0", revision)
	}
	if !loop.observeAuthRevision(context.Background(), &revision, &known) {
		t.Fatal("racing auth revision should wake reconnect loop")
	}
	if revision != 2 {
		t.Fatalf("revision after observing racing change = %d, want 2", revision)
	}
}
