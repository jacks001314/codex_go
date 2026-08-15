package model

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"syscall"
	"testing"
	"time"
)

func TestIsResponsesConnectionFailureClassifiesTransportErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"dial refused", &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, true},
		{"dial wrapped", &url.Error{Op: "Post", URL: "https://example.com", Err: &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}}, true},
		{"no route", &net.OpError{Op: "dial", Err: syscall.EHOSTUNREACH}, true},
		{"timeout", &net.OpError{Op: "dial", Err: syscall.ETIMEDOUT}, true},
		{"read reset", &net.OpError{Op: "read", Err: syscall.ECONNRESET}, true},
		{"plain refused", syscall.ECONNREFUSED, true},
		{"http status error", errors.New("500"), false},
		{"context canceled", context.Canceled, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isResponsesConnectionFailure(tc.err); got != tc.want {
				t.Fatalf("isResponsesConnectionFailure(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestRunStreamingConnectionFailureKeepsRetryingWithoutBudget(t *testing.T) {
	var attempts int
	var retryEvents []ResponsesStreamEvent
	runner := &ResponsesAgentRunner{
		Provider: &APIProvider{
			Name:              "OpenAI",
			StreamMaxRetries:  2,
			StreamIdleTimeout: 30 * time.Second,
		},
		HTTPClient: connectionRetryHTTPDoer(func(req *http.Request) (*http.Response, error) {
			attempts++
			if attempts < 3 {
				return nil, &url.Error{Op: "Post", URL: "https://api.openai.com", Err: &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}}
			}
			return nil, errors.New("non-connection failure")
		}),
		StreamHandler: func(event *ResponsesStreamEvent) {
			if event.Kind == ResponsesStreamEventRetrying {
				retryEvents = append(retryEvents, *event)
			}
		},
	}
	// Connection failures are retried without consuming the budget; the first
	// non-connection failure then exhausts the bounded budget quickly.
	_, err := runner.runStreaming(context.Background(), &AgentRequest{ThreadID: "t", TurnID: "turn"}, &responsesAgentRequest{Stream: true})
	if err == nil {
		t.Fatal("runStreaming() error = nil, want non-nil after non-connection failure")
	}
	if attempts < 3 {
		t.Fatalf("attempts = %d, want at least 3 (connection retries + bounded retry)", attempts)
	}
	// The reconnecting message must have been emitted for connection failures.
	reconnecting := false
	for _, event := range retryEvents {
		if event.RetryError == "Reconnecting... waiting for network" {
			reconnecting = true
			if event.RetryDelay < initialConnectionRetryDelay {
				t.Fatalf("reconnect delay = %v, want >= %v", event.RetryDelay, initialConnectionRetryDelay)
			}
		}
	}
	if !reconnecting {
		t.Fatalf("retry events = %+v, want a Reconnecting event", retryEvents)
	}
}

func TestRunStreamingBedrockUsesBoundedBudget(t *testing.T) {
	var attempts int
	runner := &ResponsesAgentRunner{
		Provider: &APIProvider{
			Name:              AmazonBedrockProviderName,
			StreamMaxRetries:  1,
			StreamIdleTimeout: 30 * time.Second,
		},
		HTTPClient: connectionRetryHTTPDoer(func(req *http.Request) (*http.Response, error) {
			attempts++
			return nil, &url.Error{Op: "Post", URL: "https://bedrock.example", Err: &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}}
		}),
	}
	start := time.Now()
	_, err := runner.runStreaming(context.Background(), &AgentRequest{ThreadID: "t", TurnID: "turn"}, &responsesAgentRequest{Stream: true})
	if err == nil {
		t.Fatal("runStreaming() error = nil, want non-nil")
	}
	// Bedrock must NOT get the unbounded 5-60s reconnect window.
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("elapsed = %v, want bounded budget for Bedrock", elapsed)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2 (1 + 1 retry)", attempts)
	}
}

func TestRunStreamingUnboundedConnectionRetriesFeatureGateMatchesRust(t *testing.T) {
	var attempts int
	var retryEvents []ResponsesStreamEvent
	disabled := false
	runner := &ResponsesAgentRunner{
		Provider: &APIProvider{
			Name:              "OpenAI",
			StreamMaxRetries:  1,
			StreamIdleTimeout: 30 * time.Second,
		},
		UnboundedConnectionRetries: &disabled,
		HTTPClient: connectionRetryHTTPDoer(func(req *http.Request) (*http.Response, error) {
			attempts++
			return nil, &url.Error{Op: "Post", URL: "https://api.openai.com", Err: &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}}
		}),
		StreamHandler: func(event *ResponsesStreamEvent) {
			if event.Kind == ResponsesStreamEventRetrying {
				retryEvents = append(retryEvents, *event)
			}
		},
	}
	// With unbounded_connection_retries disabled, connection failures consume
	// the bounded stream retry budget exactly like Rust's fallback path.
	start := time.Now()
	_, err := runner.runStreaming(context.Background(), &AgentRequest{ThreadID: "t", TurnID: "turn"}, &responsesAgentRequest{Stream: true})
	if err == nil {
		t.Fatal("runStreaming() error = nil, want non-nil")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("elapsed = %v, want bounded budget when feature disabled", elapsed)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2 (1 + 1 bounded retry)", attempts)
	}
	for _, event := range retryEvents {
		if event.RetryError == "Reconnecting... waiting for network" {
			t.Fatalf("unexpected unbounded reconnect event when feature disabled: %+v", event)
		}
	}
}

type connectionRetryHTTPDoer func(*http.Request) (*http.Response, error)

func (f connectionRetryHTTPDoer) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}
