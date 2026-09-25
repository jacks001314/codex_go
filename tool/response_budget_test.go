package tool

import (
	"strings"
	"testing"

	"codex_go/utils"
)

// Rust parity: codex-rs/utils/output-truncation with_serialization_allowance and
// codex-rs/tools/src/tool_call.rs ToolCall::response_byte_budget.
func TestWithSerializationAllowanceRoundsUpLikeRust(t *testing.T) {
	tests := []struct {
		policy utils.TruncationPolicy
		want   utils.TruncationPolicy
	}{
		{utils.BytesPolicy(10_000), utils.BytesPolicy(12_000)},
		{utils.BytesPolicy(1), utils.BytesPolicy(2)},
		{utils.TokensPolicy(10_000), utils.TokensPolicy(12_000)},
		{utils.TokensPolicy(1), utils.TokensPolicy(2)},
		{utils.BytesPolicy(0), utils.BytesPolicy(0)},
	}
	for _, test := range tests {
		if got := test.policy.WithSerializationAllowance(); got != test.want {
			t.Fatalf("%+v allowance = %+v, want %+v", test.policy, got, test.want)
		}
	}
}

func TestResponseByteBudgetLikeRust(t *testing.T) {
	const maxResponseBytes = 8_000
	tests := []struct {
		name       string
		invocation *Invocation
		want       int
	}{
		{
			name:       "no policy keeps the tool limit",
			invocation: &Invocation{},
			want:       maxResponseBytes,
		},
		{
			name:       "a small byte allowance binds",
			invocation: &Invocation{Truncation: &utils.TruncationPolicy{Mode: utils.PolicyBytes, Limit: 1_000}},
			want:       1_200,
		},
		{
			name:       "a byte allowance above the tool limit does not bind",
			invocation: &Invocation{Truncation: &utils.TruncationPolicy{Mode: utils.PolicyBytes, Limit: 10_000}},
			want:       maxResponseBytes,
		},
		{
			name:       "a token allowance converts to bytes before binding",
			invocation: &Invocation{Truncation: &utils.TruncationPolicy{Mode: utils.PolicyTokens, Limit: 1_000}},
			want:       4_800,
		},
		{
			name:       "code mode ignores the model allowance",
			invocation: &Invocation{Source: InvocationSourceCodeMode, Truncation: &utils.TruncationPolicy{Mode: utils.PolicyBytes, Limit: 1_000}},
			want:       maxResponseBytes,
		},
		{
			name:       "a plaintext collaboration call is direct",
			invocation: &Invocation{Source: "direct_plaintext_message", Truncation: &utils.TruncationPolicy{Mode: utils.PolicyBytes, Limit: 1_000}},
			want:       1_200,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.invocation.ResponseByteBudget(maxResponseBytes); got != test.want {
				t.Fatalf("budget = %d, want %d", got, test.want)
			}
		})
	}

	var missing *Invocation
	if got := missing.ResponseByteBudget(maxResponseBytes); got != maxResponseBytes {
		t.Fatalf("nil invocation budget = %d, want %d", got, maxResponseBytes)
	}
}

func TestDecodeStrictArgumentsRejectsUnknownAndTrailingJSON(t *testing.T) {
	type args struct {
		ChannelName string `json:"channel_name"`
	}
	invocation := func(raw string) *Invocation {
		return &Invocation{Payload: Payload{Kind: PayloadFunction, Arguments: raw}}
	}

	var decoded args
	if err := invocation(`{"channel_name":"work"}`).DecodeStrictArguments(&decoded); err != nil {
		t.Fatalf("valid arguments error = %v", err)
	}
	if decoded.ChannelName != "work" {
		t.Fatalf("decoded = %#v", decoded)
	}
	// An absent payload decodes to the caller's zero value, matching Rust's
	// empty object. (Go's json leaves absent fields untouched, so the tool
	// arguments are always freshly allocated.)
	decoded = args{}
	if err := invocation("").DecodeStrictArguments(&decoded); err != nil {
		t.Fatalf("empty arguments error = %v", err)
	}
	if decoded.ChannelName != "" {
		t.Fatalf("empty arguments decoded = %#v", decoded)
	}
	if err := invocation(`{"channel_name":"work","extra":1}`).DecodeStrictArguments(&args{}); err == nil {
		t.Fatal("unknown field was accepted")
	} else if !strings.Contains(err.Error(), "extra") {
		t.Fatalf("unknown field error = %v", err)
	}
	if err := invocation(`{"channel_name":"work"}{"channel_name":"other"}`).DecodeStrictArguments(&args{}); err == nil {
		t.Fatal("trailing JSON was accepted")
	}
	if err := (&Invocation{}).DecodeStrictArguments(nil); err == nil {
		t.Fatal("nil target was accepted")
	}
}
