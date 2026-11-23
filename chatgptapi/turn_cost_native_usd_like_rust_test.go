package chatgptapi

import (
	"encoding/json"
	"math"
	"testing"
)

// Rust #62810: the visible total is the credit-converted estimate plus native
// USD; either missing component, a negative amount, or an overflowing sum
// suppresses the total.
func TestVisibleTurnCostRequiresBothDollarComponentsLikeRust(t *testing.T) {
	value := func(micros int64) *int64 { return &micros }
	for _, tc := range []struct {
		name      string
		converted *int64
		native    *int64
		want      int64
		ok        bool
	}{
		{"native only after zero conversion", value(0), value(4668), 4668, true},
		{"both components", value(120), value(35), 155, true},
		{"zero native", value(120), value(0), 120, true},
		{"both zero", value(0), value(0), 0, true},
		{"missing converted", nil, value(35), 0, false},
		{"missing native", value(120), nil, 0, false},
		{"negative converted", value(-1), value(35), 0, false},
		{"negative native", value(120), value(-1), 0, false},
		{"overflow", value(math.MaxInt64), value(1), 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cost := &ChatgptTurnCost{EstimatedUsageUSDMicros: tc.converted, NativeUsageUSDMicros: tc.native}
			got, ok := cost.VisibleTotalUSDMicros()
			if ok != tc.ok || (ok && got != tc.want) {
				t.Fatalf("VisibleTotalUSDMicros() = (%d, %v), want (%d, %v)", got, ok, tc.want, tc.ok)
			}
		})
	}
}

// An omitted native field comes from a legacy backend and means zero; an
// explicit null means the backend cannot report native USD.
func TestTurnCostDistinguishesOmittedNativeUSDLikeRust(t *testing.T) {
	var legacy ChatgptTurnCost
	if err := json.Unmarshal([]byte(`{"turn_id":"turn-a","estimated_usage_usd_micros":120,"settled_response_ids":["resp-a"]}`), &legacy); err != nil {
		t.Fatalf("Unmarshal(legacy) error = %v", err)
	}
	if legacy.NativeUsageUSDMicros == nil || *legacy.NativeUsageUSDMicros != 0 {
		t.Fatalf("legacy native USD = %#v, want 0", legacy.NativeUsageUSDMicros)
	}
	if total, ok := legacy.VisibleTotalUSDMicros(); !ok || total != 120 {
		t.Fatalf("legacy total = (%d, %v)", total, ok)
	}

	var unavailable ChatgptTurnCost
	if err := json.Unmarshal([]byte(`{"turn_id":"turn-a","estimated_usage_usd_micros":120,"native_usage_usd_micros":null}`), &unavailable); err != nil {
		t.Fatalf("Unmarshal(null) error = %v", err)
	}
	if unavailable.NativeUsageUSDMicros != nil {
		t.Fatalf("null native USD = %#v, want nil", unavailable.NativeUsageUSDMicros)
	}
	if _, ok := unavailable.VisibleTotalUSDMicros(); ok {
		t.Fatal("null native USD produced a visible total")
	}
}
