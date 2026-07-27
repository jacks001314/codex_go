package execserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"codex_go/network"
)

type networkPolicyTestConnection struct {
	reads     chan []byte
	writes    chan []byte
	closed    chan struct{}
	closeOnce sync.Once
}

func newNetworkPolicyTestConnection(buffer int) *networkPolicyTestConnection {
	return &networkPolicyTestConnection{
		reads:  make(chan []byte, buffer),
		writes: make(chan []byte, buffer),
		closed: make(chan struct{}),
	}
}

func (c *networkPolicyTestConnection) Read(ctx context.Context) ([]byte, error) {
	select {
	case data := <-c.reads:
		return data, nil
	case <-c.closed:
		return nil, errors.New("connection closed")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *networkPolicyTestConnection) Write(ctx context.Context, data []byte) error {
	copyData := append([]byte(nil), data...)
	select {
	case c.writes <- copyData:
		return nil
	case <-c.closed:
		return errors.New("connection closed")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *networkPolicyTestConnection) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

func (c *networkPolicyTestConnection) CloseNow() error { return c.Close() }

func newNetworkPolicyTestClient(conn clientConnection) *Client {
	return &Client{
		conn:         conn,
		pending:      map[int64]chan clientCallResult{},
		sessions:     map[string]*clientProcessSession{},
		httpStreams:  map[string]*HTTPBodyStream{},
		inboundIDs:   map[int64]struct{}{},
		inboundSlots: make(chan struct{}, MaxInFlightServerRequests),
		done:         make(chan struct{}),
	}
}

func addNetworkPolicyTestSession(t *testing.T, client *Client, processID string, timeout time.Duration, decider network.ProxyPolicyDecider) *ProcessEventSubscription {
	t.Helper()
	session := &clientProcessSession{pending: map[uint64]ProcessEvent{}, policyDone: make(chan struct{})}
	subscription := &ProcessEventSubscription{client: client, processID: processID, session: session, notify: make(chan struct{}, 1)}
	session.subscription = subscription
	session.policy = &networkPolicyDecisionController{decider: decider, timeout: timeout}
	client.sessions[processID] = session
	return subscription
}

func invokeNetworkPolicyRequest(t *testing.T, client *Client, conn clientConnection, id int64, method string, params any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	if !client.admitInboundRequest(id) {
		t.Fatalf("request id %d was not admitted", id)
	}
	client.inboundSlots <- struct{}{}
	client.handleInboundRequest(conn, make(chan struct{}), id, method, raw)
	testConn := conn.(*networkPolicyTestConnection)
	select {
	case data := <-testConn.writes:
		var response map[string]any
		if err := json.Unmarshal(data, &response); err != nil {
			t.Fatal(err)
		}
		return response
	case <-time.After(time.Second):
		t.Fatal("network policy response was not written")
		return nil
	}
}

func TestClientNetworkPolicyDecisionsAndValidationLikeRust(t *testing.T) {
	for _, test := range []struct {
		name       string
		host       string
		decider    network.ProxyPolicyDecider
		wantType   string
		wantReason string
	}{
		{name: "allow", host: "allowed.example", decider: network.ProxyPolicyDeciderFunc(func(context.Context, network.ProxyPolicyRequest) network.ProxyDecision {
			return network.AllowProxyDecision()
		}), wantType: "allow"},
		{name: "deny", host: "denied.example", decider: network.ProxyPolicyDeciderFunc(func(context.Context, network.ProxyPolicyRequest) network.ProxyDecision {
			return network.DenyProxyDecision("blocked")
		}), wantType: "deny", wantReason: "blocked"},
		{name: "ask", host: "approval.example", decider: network.ProxyPolicyDeciderFunc(func(context.Context, network.ProxyPolicyRequest) network.ProxyDecision {
			return network.AskProxyDecision("approval")
		}), wantType: "ask", wantReason: "approval"},
		{name: "invalid-host", host: "invalid host", decider: network.ProxyPolicyDeciderFunc(func(context.Context, network.ProxyPolicyRequest) network.ProxyDecision {
			t.Fatal("invalid host reached decider")
			return network.AllowProxyDecision()
		}), wantType: "deny", wantReason: NetworkPolicyDenialReason},
	} {
		t.Run(test.name, func(t *testing.T) {
			conn := newNetworkPolicyTestConnection(1)
			client := newNetworkPolicyTestClient(conn)
			addNetworkPolicyTestSession(t, client, "policy-process", time.Second, test.decider)
			response := invokeNetworkPolicyRequest(t, client, conn, 0, MethodNetworkPolicyRequest, NetworkPolicyRequestParams{
				ProcessID: "policy-process",
				Request:   ExecServerNetworkPolicyRequest{Protocol: NetworkProtocolHTTPSConnect, Host: test.host, Port: 443},
			})
			result, _ := response["result"].(map[string]any)
			decision, _ := result["decision"].(map[string]any)
			if decision["type"] != test.wantType || (test.wantReason != "" && decision["reason"] != test.wantReason) {
				t.Fatalf("response = %#v", response)
			}
		})
	}
}

func TestClientNetworkPolicyUnknownMethodAndInvalidParamsLikeRust(t *testing.T) {
	conn := newNetworkPolicyTestConnection(2)
	client := newNetworkPolicyTestClient(conn)
	unknown := invokeNetworkPolicyRequest(t, client, conn, 1, "network/unknown", map[string]any{})
	invalid := invokeNetworkPolicyRequest(t, client, conn, 2, MethodNetworkPolicyRequest, map[string]any{"processId": "p", "request": map[string]any{"protocol": "ftp", "host": "example.com", "port": 21}})
	for _, test := range []struct {
		name     string
		response map[string]any
		code     float64
	}{
		{name: "unknown", response: unknown, code: -32601},
		{name: "invalid", response: invalid, code: -32602},
	} {
		errorValue, _ := test.response["error"].(map[string]any)
		if errorValue["code"] != test.code {
			t.Fatalf("%s response = %#v", test.name, test.response)
		}
	}
}

func TestClientNetworkPolicyTimeoutFailsClosedLikeRust(t *testing.T) {
	conn := newNetworkPolicyTestConnection(1)
	client := newNetworkPolicyTestClient(conn)
	cancelled := make(chan struct{})
	addNetworkPolicyTestSession(t, client, "timeout-process", 20*time.Millisecond, network.ProxyPolicyDeciderFunc(func(ctx context.Context, _ network.ProxyPolicyRequest) network.ProxyDecision {
		<-ctx.Done()
		close(cancelled)
		return network.AllowProxyDecision()
	}))
	response := invokeNetworkPolicyRequest(t, client, conn, 3, MethodNetworkPolicyRequest, NetworkPolicyRequestParams{
		ProcessID: "timeout-process",
		Request:   ExecServerNetworkPolicyRequest{Protocol: NetworkProtocolHTTP, Host: "slow.example", Port: 80},
	})
	result := response["result"].(map[string]any)
	decision := result["decision"].(map[string]any)
	if decision["type"] != "deny" || decision["reason"] != NetworkPolicyDenialReason {
		t.Fatalf("response = %#v", response)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("timed out policy decider was not cancelled")
	}
}

func TestClientNetworkPolicySubscriptionCloseCancelsPendingWithoutLateWriteLikeRust(t *testing.T) {
	conn := newNetworkPolicyTestConnection(1)
	client := newNetworkPolicyTestClient(conn)
	started := make(chan struct{})
	cancelled := make(chan struct{})
	subscription := addNetworkPolicyTestSession(t, client, "closing-process", time.Second, network.ProxyPolicyDeciderFunc(func(ctx context.Context, _ network.ProxyPolicyRequest) network.ProxyDecision {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return network.AllowProxyDecision()
	}))
	params, _ := json.Marshal(NetworkPolicyRequestParams{ProcessID: "closing-process", Request: ExecServerNetworkPolicyRequest{Protocol: NetworkProtocolHTTP, Host: "pending.example", Port: 80}})
	if !client.admitInboundRequest(4) {
		t.Fatal("request id was not admitted")
	}
	client.inboundSlots <- struct{}{}
	done := make(chan struct{})
	go func() {
		client.handleInboundRequest(conn, make(chan struct{}), 4, MethodNetworkPolicyRequest, params)
		close(done)
	}()
	<-started
	subscription.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("pending request did not stop after subscription close")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("pending decider was not cancelled")
	}
	select {
	case data := <-conn.writes:
		t.Fatalf("late decision wrote to old connection: %s", data)
	default:
	}
}

func TestClientNetworkPolicyCapacityOverflowFailsClosedLikeRust(t *testing.T) {
	conn := newNetworkPolicyTestConnection(MaxInFlightServerRequests + 1)
	client := newNetworkPolicyTestClient(conn)
	started := make(chan struct{}, MaxInFlightServerRequests)
	subscription := addNetworkPolicyTestSession(t, client, "capacity-process", 30*time.Second, network.ProxyPolicyDeciderFunc(func(ctx context.Context, _ network.ProxyPolicyRequest) network.ProxyDecision {
		started <- struct{}{}
		<-ctx.Done()
		return network.DenyProxyDecision(NetworkPolicyDenialReason)
	}))
	go client.readLoop(conn)
	for offset := 0; offset <= MaxInFlightServerRequests; offset++ {
		data, _ := json.Marshal(map[string]any{
			"id":     int64(100 + offset),
			"method": MethodNetworkPolicyRequest,
			"params": NetworkPolicyRequestParams{ProcessID: "capacity-process", Request: ExecServerNetworkPolicyRequest{Protocol: NetworkProtocolHTTPSConnect, Host: "pending.example", Port: 443}},
		})
		conn.reads <- data
	}
	for range MaxInFlightServerRequests {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("pending policy decisions did not fill capacity")
		}
	}
	select {
	case data := <-conn.writes:
		var response map[string]any
		if err := json.Unmarshal(data, &response); err != nil {
			t.Fatal(err)
		}
		if response["id"] != float64(100+MaxInFlightServerRequests) {
			t.Fatalf("overflow response = %#v", response)
		}
		decision := response["result"].(map[string]any)["decision"].(map[string]any)
		if decision["type"] != "deny" || decision["reason"] != NetworkPolicyDenialReason {
			t.Fatalf("overflow response = %#v", response)
		}
	case <-time.After(time.Second):
		t.Fatal("capacity overflow did not fail closed")
	}
	subscription.Close()
	_ = client.Close()
}

func TestClientNetworkPolicyDuplicateAndInvalidIDsCloseTransportLikeRust(t *testing.T) {
	for _, test := range []struct {
		name     string
		messages []map[string]any
	}{
		{name: "negative", messages: []map[string]any{{"id": -1, "method": MethodNetworkPolicyRequest, "params": map[string]any{}}}},
		{name: "string", messages: []map[string]any{{"id": "bad", "method": MethodNetworkPolicyRequest, "params": map[string]any{}}}},
		{name: "duplicate", messages: []map[string]any{
			{"id": 7, "method": MethodNetworkPolicyRequest, "params": NetworkPolicyRequestParams{ProcessID: "duplicate-process", Request: ExecServerNetworkPolicyRequest{Protocol: NetworkProtocolHTTP, Host: "pending.example", Port: 80}}},
			{"id": 7, "method": MethodNetworkPolicyRequest, "params": NetworkPolicyRequestParams{ProcessID: "duplicate-process", Request: ExecServerNetworkPolicyRequest{Protocol: NetworkProtocolHTTP, Host: "pending.example", Port: 80}}},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			conn := newNetworkPolicyTestConnection(len(test.messages))
			client := newNetworkPolicyTestClient(conn)
			if test.name == "duplicate" {
				addNetworkPolicyTestSession(t, client, "duplicate-process", time.Second, network.ProxyPolicyDeciderFunc(func(ctx context.Context, _ network.ProxyPolicyRequest) network.ProxyDecision {
					<-ctx.Done()
					return network.DenyProxyDecision(NetworkPolicyDenialReason)
				}))
			}
			for _, message := range test.messages {
				data, err := json.Marshal(message)
				if err != nil {
					t.Fatal(err)
				}
				conn.reads <- data
			}
			go client.readLoop(conn)
			select {
			case <-conn.closed:
			case <-time.After(time.Second):
				t.Fatalf("%s request id did not close transport", test.name)
			}
			client.mu.Lock()
			connected := client.conn != nil
			client.mu.Unlock()
			if connected {
				t.Fatalf("%s request id left transport connected", test.name)
			}
		})
	}
}

func TestClientNetworkPolicyReasonSanitizationLikeRust(t *testing.T) {
	for name, test := range map[string]struct {
		reason string
		want   string
	}{
		"empty":   {reason: "", want: network.ProxyReasonPolicyDenied},
		"control": {reason: "bad\nreason", want: NetworkPolicyDenialReason},
		"long":    {reason: fmt.Sprintf("%0*d", MaxNetworkPolicyReasonBytes+1, 1), want: NetworkPolicyDenialReason},
	} {
		t.Run(name, func(t *testing.T) {
			decision := execServerDecisionFromProxy(network.DenyProxyDecision(test.reason))
			if decision.Type != "deny" || decision.Reason != test.want {
				t.Fatalf("decision = %#v", decision)
			}
		})
	}
}
