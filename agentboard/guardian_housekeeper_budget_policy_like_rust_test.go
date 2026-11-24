package agentboard

import (
	"context"
	"testing"

	"codex_go/utils"
)

// TestGuardianHousekeeperResolvesRoutingInputBudgetLikeRust mirrors Rust
// #62372: the routing batch budget comes from the model's TruncationPolicy
// (policy.byte_budget), while an explicit byte cap still wins.
func TestGuardianHousekeeperResolvesRoutingInputBudgetLikeRust(t *testing.T) {
	_, board, delivery, root, _ := guardianInboxFixture(t)
	decide := func(context.Context, GuardianRoutingBatch) (GuardianRoutingDecision, error) {
		return GuardianRoutingDecision{}, nil
	}
	cases := []struct {
		name    string
		options GuardianHousekeeperOptions
		want    int
	}{
		{name: "tokens", options: GuardianHousekeeperOptions{InputBudget: utils.TokensPolicy(1000)}, want: 4000},
		{name: "bytes", options: GuardianHousekeeperOptions{InputBudget: utils.BytesPolicy(2048)}, want: 2048},
		{name: "explicit cap wins", options: GuardianHousekeeperOptions{MaxInputBytes: 128, InputBudget: utils.TokensPolicy(1000)}, want: 128},
		{name: "unset", options: GuardianHousekeeperOptions{}, want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			housekeeper, err := NewGuardianHousekeeperWithOptions(board, root, delivery, decide, tc.options)
			if err != nil {
				t.Fatalf("NewGuardianHousekeeperWithOptions() error = %v", err)
			}
			if got := housekeeper.MaxInputBytes(); got != tc.want {
				t.Fatalf("MaxInputBytes() = %d, want %d", got, tc.want)
			}
		})
	}
}
