package execserver

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

type capturedServerRequest struct {
	id     int64
	method string
}

func captureServerRequest(value any) capturedServerRequest {
	data, _ := json.Marshal(value)
	var request struct {
		ID     int64  `json:"id"`
		Method string `json:"method"`
	}
	_ = json.Unmarshal(data, &request)
	return capturedServerRequest{id: request.ID, method: request.Method}
}

func pendingServerRequestCount(sender *serverRequestSender) int {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	return len(sender.pending)
}

func TestServerRequestSenderMatchesOutOfOrderResponsesLikeRust(t *testing.T) {
	outgoing := make(chan capturedServerRequest, 2)
	sender := newServerRequestSender(func(value any) error {
		outgoing <- captureServerRequest(value)
		return nil
	})
	defer sender.close()
	type callResult struct {
		value string
		err   error
	}
	results := make(chan callResult, 2)
	for _, method := range []string{"slow", "fast"} {
		method := method
		go func() {
			var response struct {
				Value string `json:"value"`
			}
			err := sender.call(context.Background(), method, map[string]any{}, time.Second, &response)
			results <- callResult{value: response.Value, err: err}
		}()
	}
	first := <-outgoing
	second := <-outgoing
	requests := map[string]capturedServerRequest{first.method: first, second.method: second}
	if !sender.complete([]byte(`{"id":` + int64String(requests["fast"].id) + `,"result":{"value":"fast"}}`)) {
		t.Fatal("fast response was not accepted")
	}
	if !sender.complete([]byte(`{"id":` + int64String(requests["slow"].id) + `,"result":{"value":"slow"}}`)) {
		t.Fatal("slow response was not accepted")
	}
	seen := map[string]bool{}
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		seen[result.value] = true
	}
	if !seen["slow"] || !seen["fast"] || pendingServerRequestCount(sender) != 0 {
		t.Fatalf("results = %#v pending=%d", seen, pendingServerRequestCount(sender))
	}
}

func TestServerRequestSenderPreservesResponseBeforeCloseLikeRust(t *testing.T) {
	outgoing := make(chan capturedServerRequest, 1)
	sender := newServerRequestSender(func(value any) error {
		outgoing <- captureServerRequest(value)
		return nil
	})
	done := make(chan error, 1)
	go func() {
		var response map[string]string
		err := sender.call(context.Background(), "ordered", map[string]any{}, time.Second, &response)
		if err == nil && response["value"] != "accepted" {
			err = context.Canceled
		}
		done <- err
	}()
	request := <-outgoing
	if !sender.complete([]byte(`{"id":` + int64String(request.id) + `,"result":{"value":"accepted"}}`)) {
		t.Fatal("response was not accepted")
	}
	sender.close()
	if err := <-done; err != nil {
		t.Fatalf("call error = %v", err)
	}
}

func TestServerRequestSenderTimeoutAcceptsKnownLateAndRejectsUnknownLikeRust(t *testing.T) {
	outgoing := make(chan capturedServerRequest, 1)
	sender := newServerRequestSender(func(value any) error {
		outgoing <- captureServerRequest(value)
		return nil
	})
	defer sender.close()
	done := make(chan error, 1)
	go func() {
		done <- sender.call(context.Background(), "slow", map[string]any{}, 20*time.Millisecond, &map[string]any{})
	}()
	request := <-outgoing
	if err := <-done; err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("timeout error = %v", err)
	}
	if pendingServerRequestCount(sender) != 0 {
		t.Fatalf("pending after timeout = %d", pendingServerRequestCount(sender))
	}
	if !sender.complete([]byte(`{"id":` + int64String(request.id) + `,"result":null}`)) {
		t.Fatal("known late response was rejected")
	}
	if sender.complete([]byte(`{"id":` + int64String(request.id+1) + `,"result":null}`)) {
		t.Fatal("unknown future response was accepted")
	}
}

func TestServerRequestSenderBoundsAndDrainsOnCloseLikeRust(t *testing.T) {
	outgoing := make(chan capturedServerRequest, MaxInFlightServerRequests)
	sender := newServerRequestSender(func(value any) error {
		outgoing <- captureServerRequest(value)
		return nil
	})
	var workers sync.WaitGroup
	errorsCh := make(chan error, MaxInFlightServerRequests)
	for range MaxInFlightServerRequests {
		workers.Add(1)
		go func() {
			defer workers.Done()
			errorsCh <- sender.call(context.Background(), "pending", map[string]any{}, 30*time.Second, &map[string]any{})
		}()
	}
	for range MaxInFlightServerRequests {
		select {
		case <-outgoing:
		case <-time.After(time.Second):
			t.Fatal("pending server requests did not fill capacity")
		}
	}
	if pendingServerRequestCount(sender) != MaxInFlightServerRequests {
		t.Fatalf("pending count = %d", pendingServerRequestCount(sender))
	}
	err := sender.call(context.Background(), "overflow", map[string]any{}, time.Second, &map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "pending server request limit exceeded") {
		t.Fatalf("overflow error = %v", err)
	}
	sender.close()
	workers.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err == nil || !strings.Contains(err.Error(), "closed") {
			t.Fatalf("pending call error = %v", err)
		}
	}
	if pendingServerRequestCount(sender) != 0 {
		t.Fatalf("pending after close = %d", pendingServerRequestCount(sender))
	}
}

func TestServerConnectionStateMovesRequestsToReplacementSenderLikeRust(t *testing.T) {
	oldOutgoing := make(chan capturedServerRequest, 1)
	newOutgoing := make(chan capturedServerRequest, 1)
	oldSender := newServerRequestSender(func(value any) error {
		oldOutgoing <- captureServerRequest(value)
		return nil
	})
	newSender := newServerRequestSender(func(value any) error {
		newOutgoing <- captureServerRequest(value)
		return nil
	})
	server := NewServer()
	server.setConnectionState(nil, oldSender)
	oldDone := make(chan error, 1)
	go func() {
		oldDone <- oldSender.call(context.Background(), MethodNetworkPolicyRequest, map[string]any{}, time.Second, &map[string]any{})
	}()
	<-oldOutgoing
	server.setConnectionState(nil, newSender)
	if err := <-oldDone; err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("old request error = %v", err)
	}
	newDone := make(chan error, 1)
	go func() {
		newDone <- server.currentRequestSender().call(context.Background(), MethodNetworkPolicyRequest, map[string]any{}, time.Second, &map[string]any{})
	}()
	request := <-newOutgoing
	if !newSender.complete([]byte(`{"id":` + int64String(request.id) + `,"result":{}}`)) {
		t.Fatal("replacement sender rejected response")
	}
	if err := <-newDone; err != nil {
		t.Fatalf("replacement request error = %v", err)
	}
	server.setConnectionState(nil, nil)
}

func TestConsumeClientResponseDistinguishesRequestsAndUnknownIDsLikeRust(t *testing.T) {
	sender := newServerRequestSender(func(any) error { return nil })
	defer sender.close()
	ctx := withServerRequestSender(context.Background(), sender)
	if response, accepted := consumeClientResponse(ctx, []byte(`{"id":1,"method":"network/policyRequest","params":{}}`)); response || accepted {
		t.Fatalf("request classified as response: response=%t accepted=%t", response, accepted)
	}
	if response, accepted := consumeClientResponse(ctx, []byte(`{"id":99,"result":{}}`)); !response || accepted {
		t.Fatalf("unknown response classification: response=%t accepted=%t", response, accepted)
	}
}

func int64String(value int64) string {
	data, _ := json.Marshal(value)
	return string(data)
}
