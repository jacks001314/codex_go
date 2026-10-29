package mockmodel_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"codex_go/stability/harness/mockmodel"
)

func TestTextStreamsAndCapturesRequest(t *testing.T) {
	server := mockmodel.New(mockmodel.Text("hello mock"))
	defer server.Close()

	response, err := http.Post(server.URL()+"/v1/responses", "application/json", strings.NewReader(`{"model":"gpt-mock","stream":true,"input":"hi"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if got := response.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("content type = %q, want text/event-stream", got)
	}
	if !strings.Contains(string(body), "response.completed") {
		t.Fatalf("stream body missing completion: %s", body)
	}
	if !strings.Contains(string(body), "hello mock") {
		t.Fatalf("stream body missing text: %s", body)
	}

	requests := server.Requests()
	if len(requests) != 1 {
		t.Fatalf("captured %d requests, want 1", len(requests))
	}
	if requests[0].Path != "/v1/responses" {
		t.Fatalf("captured path = %q", requests[0].Path)
	}
	if !strings.Contains(string(requests[0].Body), `"stream":true`) {
		t.Fatalf("captured body = %s", requests[0].Body)
	}
}

func TestNonStreamReturnsJSON(t *testing.T) {
	server := mockmodel.New(mockmodel.Text("plain"))
	defer server.Close()

	response, err := http.Post(server.URL()+"/v1/responses", "application/json", strings.NewReader(`{"model":"gpt-mock","stream":false}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if got := response.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q, want application/json", got)
	}
	if !strings.Contains(string(body), "plain") {
		t.Fatalf("body missing text: %s", body)
	}
}

func TestGateBlocksUntilRelease(t *testing.T) {
	gate := mockmodel.NewGate()
	server := mockmodel.New(mockmodel.Text("gated").GateAt(2, gate))
	defer server.Close()

	done := make(chan error, 1)
	go func() {
		response, err := http.Post(server.URL()+"/v1/responses", "application/json", strings.NewReader(`{"stream":true}`))
		if err != nil {
			done <- err
			return
		}
		defer response.Body.Close()
		_, err = io.ReadAll(response.Body)
		done <- err
	}()

	select {
	case err := <-done:
		t.Fatalf("stream finished before gate release: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	gate.Release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("stream failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not finish after gate release")
	}
}

func TestAbortMidStreamEndsBody(t *testing.T) {
	server := mockmodel.New(mockmodel.AbortMidStream("aborted"))
	defer server.Close()

	response, err := http.Post(server.URL()+"/v1/responses", "application/json", strings.NewReader(`{"stream":true}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer response.Body.Close()
	if _, err := io.ReadAll(response.Body); err == nil {
		t.Fatal("expected an EOF error from the aborted stream")
	}
}

func TestErrorStatusIsReturned(t *testing.T) {
	server := mockmodel.New(mockmodel.Error(http.StatusTooManyRequests, `{"error":{"code":"rate_limit_exceeded"}}`))
	defer server.Close()

	response, err := http.Post(server.URL()+"/v1/responses", "application/json", strings.NewReader(`{"stream":true}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", response.StatusCode)
	}
}
