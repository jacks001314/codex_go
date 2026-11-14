package cloudbackend

import (
	"codex_go/turn"
	"encoding/json"
	"testing"
)

func TestCloudServiceTierSelectionLikeRust(t *testing.T) {
	for _, tc := range []struct {
		tier       string
		valid, set bool
		want       string
	}{
		{"", true, false, ""}, {`,"serviceTier":null`, true, true, ""},
		{`,"serviceTier":"default"`, true, true, ""},
		{`,"serviceTier":"ultrafast"`, true, true, "ultrafast"},
		{`,"serviceTier":"flex"`, true, true, "flex"},
		{`,"serviceTier":"bogus"`, false, false, ""},
	} {
		var params turn.TurnStartParams
		if err := json.Unmarshal([]byte(`{"threadId":"t","clientUserMessageId":"m","turnTrigger":"user","input":[{"type":"text","text":"hello"}]`+tc.tier+"}"), &params); err != nil {
			t.Fatal(err)
		}
		got, err := cloudMessage(params)
		if (err == nil) != tc.valid {
			t.Errorf("tier=%s error=%v", tc.tier, err)
			continue
		}
		if !tc.valid {
			continue
		}
		if got.ServiceTierSet != tc.set {
			t.Errorf("tier=%s set=%v", tc.tier, got.ServiceTierSet)
		}
		if tc.want == "" {
			if got.ServiceTier != nil {
				t.Errorf("tier=%s got=%v", tc.tier, *got.ServiceTier)
			}
		} else if got.ServiceTier == nil || *got.ServiceTier != tc.want {
			t.Errorf("tier=%s value=%v", tc.tier, got.ServiceTier)
		}
		if tc.set {
			params.TurnTrigger = "automatic"
			if _, err := cloudMessage(params); err == nil {
				t.Errorf("accepted non-user tier %s", tc.tier)
			}
		}
	}
}
