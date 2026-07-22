package execserver

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNetworkPolicyRustJSONShapes(t *testing.T) {
	p := NetworkPolicyRequestParams{ProcessID: "proc-1", Request: ExecServerNetworkPolicyRequest{Protocol: NetworkProtocolHTTPSConnect, Host: "example.com", Port: 443}}
	b, _ := json.Marshal(p)
	if string(b) != `{"processId":"proc-1","request":{"protocol":"https_connect","host":"example.com","port":443}}` {
		t.Fatalf("params=%s", b)
	}
	for _, d := range []ExecServerNetworkPolicyDecision{AllowNetworkPolicyDecision(), DenyNetworkPolicyDecision("blocked"), AskNetworkPolicyDecision("approval")} {
		b, _ = json.Marshal(d)
		var got ExecServerNetworkPolicyDecision
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatal(err)
		}
	}
}
func TestRemoteProxyPolicyDecisionOptionOmittedByDefault(t *testing.T) {
	b, _ := json.Marshal(RemoteNetworkProxyConfig{})
	if strings.Contains(string(b), "requestPolicyDecisions") {
		t.Fatalf("unexpected field: %s", b)
	}
	b, _ = json.Marshal(RemoteNetworkProxyConfig{RequestPolicyDecisions: true})
	if !strings.Contains(string(b), `"requestPolicyDecisions":true`) {
		t.Fatalf("missing field: %s", b)
	}
}
