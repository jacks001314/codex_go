package model

import (
	"encoding/json"
	"testing"
)

// Mirrors Rust #41666 `GuardianModelPolicy::allows_initial_cua_call`: an
// explicit catalog value wins, and an omitted value keeps the allowance only
// for adaptive computer use.
func TestGuardianModelPolicyAllowsInitialCUACallLikeRust(t *testing.T) {
	adaptive := GuardianReviewModeAdaptive
	synchronous := GuardianReviewModeSynchronous
	disabled := GuardianReviewModeDisabled
	yes, no := true, false

	cases := []struct {
		name   string
		policy *GuardianModelPolicy
		want   bool
	}{
		{"nil policy", nil, false},
		{"omitted with adaptive", &GuardianModelPolicy{ComputerUse: &adaptive}, true},
		{"omitted with synchronous", &GuardianModelPolicy{ComputerUse: &synchronous}, false},
		{"omitted with disabled", &GuardianModelPolicy{ComputerUse: &disabled}, false},
		{"omitted without computer use", &GuardianModelPolicy{}, false},
		{"explicit true with synchronous", &GuardianModelPolicy{ComputerUse: &synchronous, InitialCUACall: &yes}, true},
		{"explicit false with adaptive", &GuardianModelPolicy{ComputerUse: &adaptive, InitialCUACall: &no}, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := testCase.policy.AllowsInitialCUACall(); got != testCase.want {
				t.Fatalf("AllowsInitialCUACall() = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestModelInfoAllowsInitialCUACallLikeRust(t *testing.T) {
	adaptive := GuardianReviewModeAdaptive
	if (*ModelInfo)(nil).AllowsInitialCUACall() {
		t.Fatal("nil model must not allow the first computer-use call")
	}
	if (&ModelInfo{}).AllowsInitialCUACall() {
		t.Fatal("a model without a Guardian policy must not allow the first computer-use call")
	}
	withPolicy := &ModelInfo{Guardian: &GuardianModelPolicy{ComputerUse: &adaptive}}
	if !withPolicy.AllowsInitialCUACall() {
		t.Fatal("adaptive computer use must allow the first computer-use call")
	}
}

func TestGuardianModelPolicyInitialCUACallJSONLikeRust(t *testing.T) {
	var policy GuardianModelPolicy
	if err := json.Unmarshal([]byte(`{"computer_use":"adaptive","initial_cua_call":false}`), &policy); err != nil {
		t.Fatalf("unmarshal policy: %v", err)
	}
	if policy.InitialCUACall == nil || *policy.InitialCUACall {
		t.Fatalf("initial_cua_call = %v, want false", policy.InitialCUACall)
	}
	if policy.AllowsInitialCUACall() {
		t.Fatal("an explicit false must override adaptive computer use")
	}
	encoded, err := json.Marshal(policy)
	if err != nil {
		t.Fatalf("marshal policy: %v", err)
	}
	if string(encoded) != `{"computer_use":"adaptive","initial_cua_call":false}` {
		t.Fatalf("marshaled policy = %s", encoded)
	}
	adaptive := GuardianReviewModeAdaptive
	omitted, err := json.Marshal(GuardianModelPolicy{ComputerUse: &adaptive})
	if err != nil {
		t.Fatalf("marshal policy without initial_cua_call: %v", err)
	}
	if string(omitted) != `{"computer_use":"adaptive"}` {
		t.Fatalf("omitted initial_cua_call = %s", omitted)
	}
}
