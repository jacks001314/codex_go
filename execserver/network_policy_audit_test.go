package execserver

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNetworkPolicyDecisionNotificationValidateLikeRust(t *testing.T) {
	method := httpGetMethod()
	client := "10.0.0.5:1234"
	valid := NetworkPolicyDecisionNotification{
		ProcessID:      "proc-1",
		Timestamp:      "2026-08-15T10:00:00.000Z",
		Scope:          "domain",
		Decision:       "deny",
		Source:         "decider",
		Reason:         "not allowed",
		Protocol:       NetworkProtocolHTTP,
		Host:           "example.com",
		Port:           443,
		Method:         &method,
		Client:         &client,
		PolicyOverride: true,
	}
	if !valid.Validate() {
		t.Fatalf("valid notification rejected: %#v", valid)
	}

	cases := []NetworkPolicyDecisionNotification{
		{ProcessID: "", Timestamp: "t", Scope: "domain", Decision: "deny", Source: "decider", Protocol: NetworkProtocolHTTP, Host: "example.com", Port: 443},
		{ProcessID: "p", Timestamp: "t", Scope: "other", Decision: "deny", Source: "decider", Protocol: NetworkProtocolHTTP, Host: "example.com", Port: 443},
		{ProcessID: "p", Timestamp: "t", Scope: "domain", Decision: "maybe", Source: "decider", Protocol: NetworkProtocolHTTP, Host: "example.com", Port: 443},
		{ProcessID: "p", Timestamp: "t", Scope: "domain", Decision: "deny", Source: "unknown", Protocol: NetworkProtocolHTTP, Host: "example.com", Port: 443},
		{ProcessID: "p", Timestamp: "t", Scope: "domain", Decision: "deny", Source: "decider", Protocol: NetworkProtocolHTTP, Host: "", Port: 443},
		{ProcessID: "p", Timestamp: "t", Scope: "domain", Decision: "deny", Source: "decider", Protocol: "ftp", Host: "example.com", Port: 443},
		{ProcessID: "p", Timestamp: strings.Repeat("x", MaxNetworkPolicyTimestampBytes+1), Scope: "domain", Decision: "deny", Source: "decider", Protocol: NetworkProtocolHTTP, Host: "example.com", Port: 443},
	}
	for _, notification := range cases {
		if notification.Validate() {
			t.Fatalf("invalid notification accepted: %#v", notification)
		}
	}
}

func TestClientHandleNetworkPolicyDecisionNotificationLikeRust(t *testing.T) {
	method := httpGetMethod()
	valid := NetworkPolicyDecisionNotification{
		ProcessID: "proc-1",
		Timestamp: "2026-08-15T10:00:00.000Z",
		Scope:     "domain",
		Decision:  "allow",
		Source:    "baseline_policy",
		Protocol:  NetworkProtocolHTTP,
		Host:      "example.com",
		Port:      443,
		Method:    &method,
	}
	raw, err := json.Marshal(valid)
	if err != nil {
		t.Fatalf("Marshal error = %v", err)
	}
	client := &Client{sessions: map[string]*clientProcessSession{"proc-1": {}}}
	if err := client.handleNotification(MethodNetworkPolicyDecision, raw); err != nil {
		t.Fatalf("valid notification error = %v", err)
	}

	invalid := valid
	invalid.Scope = "other"
	raw, err = json.Marshal(invalid)
	if err != nil {
		t.Fatalf("Marshal error = %v", err)
	}
	if err := client.handleNotification(MethodNetworkPolicyDecision, raw); err == nil {
		t.Fatal("invalid notification should fail validation")
	}

	unknown := valid
	unknown.ProcessID = "missing"
	raw, err = json.Marshal(unknown)
	if err != nil {
		t.Fatalf("Marshal error = %v", err)
	}
	if err := client.handleNotification(MethodNetworkPolicyDecision, raw); err != nil {
		t.Fatalf("unknown-process notification should be dropped, got error = %v", err)
	}
}

func httpGetMethod() string {
	return "GET"
}
