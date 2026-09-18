package tcptunnel

import (
	"bufio"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestControlInputAcknowledgesBearerRenewalWithOrWithoutMetadata mirrors Rust's
// `control_input_acknowledges_bearer_renewal_with_or_without_metadata`.
func TestControlInputAcknowledgesBearerRenewalWithOrWithoutMetadata(t *testing.T) {
	cases := []struct {
		name             string
		input            string
		includesMetadata bool
	}{
		{name: "token only", input: "first-secret\nsecond-secret\n"},
		{
			name:             "metadata and token",
			input:            "[[\"x-test-route\",\"private-value\"]]\nfirst-secret\nsecond-secret\n",
			includesMetadata: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := newControlInput(bufio.NewReader(strings.NewReader(tc.input)), tc.includesMetadata)
			metadata := <-input.metadata
			if metadata.err != nil {
				t.Fatalf("metadata error = %v", metadata.err)
			}
			if tc.includesMetadata {
				if got := metadata.headers.Get("X-Test-Route"); got != "private-value" {
					t.Fatalf("metadata header = %q", got)
				}
			} else if len(metadata.headers) != 0 {
				t.Fatalf("metadata headers = %#v, want empty", metadata.headers)
			}
			initial := <-input.tokens
			if initial.err != nil || initial.auth != "Bearer first-secret" {
				t.Fatalf("initial token = %#v", initial)
			}
			auth := &authState{}
			auth.store(initial.auth)
			ready := make(chan struct{})
			close(ready)
			var output strings.Builder
			if err := updateAuthTokens(input.tokens, auth, ready, &output); err != nil {
				t.Fatalf("updateAuthTokens() error = %v", err)
			}
			if got := auth.load(); got != "Bearer second-secret" {
				t.Fatalf("latest auth = %q", got)
			}
			if got := output.String(); got != "AUTH_UPDATED\n" {
				t.Fatalf("output = %q", got)
			}
		})
	}
}

// TestClosedParentPipeFinishesEvenBeforeProxyReadiness mirrors Rust's
// `closed_parent_pipe_finishes_even_before_proxy_readiness`.
func TestClosedParentPipeFinishesEvenBeforeProxyReadiness(t *testing.T) {
	input := newControlInput(bufio.NewReader(strings.NewReader("")), false)
	metadata := <-input.metadata
	if metadata.err != nil {
		t.Fatalf("metadata error = %v", metadata.err)
	}
	auth := &authState{}
	auth.store("Bearer initial")
	ready := make(chan struct{})
	done := make(chan error, 1)
	var output strings.Builder
	go func() { done <- updateAuthTokens(input.tokens, auth, ready, &output) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("updateAuthTokens() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("updateAuthTokens did not finish on a closed pipe")
	}
	if output.String() != "" {
		t.Fatalf("output = %q, want empty", output.String())
	}
}

// TestInvalidControlInputStopsCredentialsWithoutExposingSecrets mirrors Rust's
// `invalid_control_input_stops_credentials_without_exposing_secrets`.
func TestInvalidControlInputStopsCredentialsWithoutExposingSecrets(t *testing.T) {
	oversizedHeader := fmt.Sprintf("[[\"x-test\",\"private-value%s\"]]\n", strings.Repeat("x", MaxConnectHeaderValueBytes))
	oversizedMetadata := strings.Repeat("x", MaxConnectMetadataBytes+1)
	pairs := make([]string, 0, MaxConnectHeaders+1)
	for index := 0; index <= MaxConnectHeaders; index++ {
		pairs = append(pairs, fmt.Sprintf("[\"x-test-%d\",\"private-value\"]", index))
	}
	oversizedCount := "[" + strings.Join(pairs, ",") + "]\n"

	invalidMetadata := []struct {
		name  string
		input string
	}{
		{"authorization", "[[\"authorization\",\"private-value\"]]\n"},
		{"forwarding", "[[\"x-forwarded-for\",\"private-value\"]]\n"},
		{"real IP", "[[\"x-real-ip\",\"private-value\"]]\n"},
		{"duplicate", "[[\"x-test\",\"one\"],[\"X-Test\",\"private-value\"]]\n"},
		{"invalid value", "[[\"x-test\",\"private-value\\nmore\"]]\n"},
		{"invalid JSON", "\"private-value\"\n"},
		{"missing newline", "[[\"x-test\",\"private-value\"]]"},
		{"value limit", oversizedHeader},
		{"metadata limit", oversizedMetadata},
		{"header count", oversizedCount},
	}
	for _, tc := range invalidMetadata {
		input := newControlInput(bufio.NewReader(strings.NewReader(tc.input+"private-secret\n")), true)
		metadata := <-input.metadata
		if metadata.err == nil {
			t.Fatalf("%s: metadata error = nil, want error", tc.name)
		}
		if strings.Contains(metadata.err.Error(), "private-") {
			t.Fatalf("%s: error leaks secret: %v", tc.name, metadata.err)
		}
		if _, ok := <-input.tokens; ok {
			t.Fatalf("%s: credential stream stayed open", tc.name)
		}
	}

	invalidTokens := []struct {
		name  string
		input string
	}{
		{"invalid bearer", " private-secret\x00 \n"},
		{"empty bearer", "\n"},
		{"bearer limit", strings.Repeat("x", MaxTokenBytes+1)},
	}
	for _, tc := range invalidTokens {
		input := newControlInput(bufio.NewReader(strings.NewReader(tc.input)), false)
		metadata := <-input.metadata
		if metadata.err != nil {
			t.Fatalf("%s: metadata error = %v", tc.name, metadata.err)
		}
		token, ok := <-input.tokens
		if !ok {
			t.Fatalf("%s: credential stream closed without an error", tc.name)
		}
		if token.err == nil {
			t.Fatalf("%s: token error = nil, want error", tc.name)
		}
		if strings.Contains(token.err.Error(), "private-") {
			t.Fatalf("%s: error leaks secret: %v", tc.name, token.err)
		}
		if _, ok := <-input.tokens; ok {
			t.Fatalf("%s: credential stream stayed open", tc.name)
		}
	}
}

func TestRunRequiresAuthTokenStdin(t *testing.T) {
	err := Run(context.Background(), &Args{
		ProxyURL:         "https://proxy.example.org",
		ProxyOriginsFile: "origins.txt",
		Target:           "127.0.0.1:22",
		ListenAddr:       DefaultListenAddr,
	}, strings.NewReader(""), ioDiscard{}, ioDiscard{})
	if err == nil || !strings.Contains(err.Error(), "--auth-token-stdin") {
		t.Fatalf("Run() error = %v, want the auth-token-stdin requirement", err)
	}
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }
