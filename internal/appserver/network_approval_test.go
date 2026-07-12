package appserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"codex_go/internal/config"
	"codex_go/internal/execpolicy"
	"codex_go/internal/network"
	"codex_go/internal/session"
	"codex_go/internal/tool"
	"codex_go/internal/turn"
)

func TestNetworkApprovalDeciderRequestsOnceAndCachesSessionLikeRust(t *testing.T) {
	router := newNetworkApprovalTestRouter(t, "on-request")
	var requests atomic.Int32
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		requests.Add(1)
		params, ok := request.Params.(*CommandExecutionRequestApprovalParams)
		if !ok {
			t.Errorf("params = %T", request.Params)
			return
		}
		contextValue, ok := params.NetworkApprovalContext.(*NetworkApprovalContext)
		if !ok || contextValue.Host != "example.test" || contextValue.Protocol != NetworkApprovalHTTPS {
			t.Errorf("network approval context = %#v", params.NetworkApprovalContext)
		}
		if params.ItemID != "network#local#https#example.test#443" || params.Command == nil || *params.Command != "network-access https://example.test:443" || len(params.ProposedNetworkPolicyAmendments) != 2 {
			t.Errorf("network approval params = %#v", params)
		}
		_, _ = router.requireServerRequests().Resolve(&Response{ID: request.ID, Result: map[string]any{"decision": string(CommandExecutionApprovalAcceptForSession)}})
	}))

	policyRequest := network.ProxyPolicyRequest{Protocol: network.ProxyProtocolHTTPSConnect, Host: "example.test", Port: 443, EnvironmentID: "local", Method: "CONNECT"}
	for range 2 {
		decision := router.networkApproval.Decide(context.Background(), policyRequest)
		if !decision.Allow {
			t.Fatalf("decision = %#v", decision)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("approval requests = %d, want 1", requests.Load())
	}
}

func TestNetworkApprovalDeciderCoalescesConcurrentHostPromptLikeRust(t *testing.T) {
	router := newNetworkApprovalTestRouter(t, "on-request")
	release := make(chan struct{})
	requestSeen := make(chan struct{})
	var requestSeenOnce sync.Once
	var requests atomic.Int32
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		requests.Add(1)
		requestSeenOnce.Do(func() { close(requestSeen) })
		<-release
		_, _ = router.requireServerRequests().Resolve(&Response{ID: request.ID, Result: map[string]any{"decision": string(CommandExecutionApprovalAccept)}})
	}))
	policyRequest := network.ProxyPolicyRequest{Protocol: network.ProxyProtocolHTTP, Host: "example.test", Port: 80, EnvironmentID: "local", Method: "GET"}
	results := make(chan network.ProxyDecision, 2)
	go func() { results <- router.networkApproval.Decide(context.Background(), policyRequest) }()
	<-requestSeen
	go func() { results <- router.networkApproval.Decide(context.Background(), policyRequest) }()
	time.Sleep(20 * time.Millisecond)
	close(release)
	for range 2 {
		if decision := <-results; !decision.Allow {
			t.Fatalf("decision = %#v", decision)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("approval requests = %d, want 1", requests.Load())
	}
}

func TestThreadScopedNetworkDeciderDoesNotBecomeAmbiguousAcrossThreadsLikeRust(t *testing.T) {
	router := newNetworkApprovalTestRouter(t, "on-request")
	home := router.services.DefaultCWD
	secondParams := &turn.TurnStartParams{ThreadID: "thread-network-2", CWD: home}
	if err := router.registerActiveRuntimeTurn("thread-network-2", "turn-network-2", func() {}, 1, secondParams); err != nil {
		t.Fatal(err)
	}
	defer router.clearActiveRuntimeTurn("thread-network-2", "turn-network-2")
	router.updateActiveRuntimeTurnAnalytics("thread-network-2", "turn-network-2", "connection-network-2", &appTurnRunConfig{ApprovalPolicy: "on-request"})
	var approvals atomic.Int32
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		approvals.Add(1)
		_, _ = router.requireServerRequests().Resolve(&Response{ID: request.ID, Result: map[string]any{"decision": string(CommandExecutionApprovalAccept)}})
	}))
	decision := router.networkApproval.decideForThread(context.Background(), "thread-network", network.ProxyPolicyRequest{
		Protocol: network.ProxyProtocolHTTP,
		Host:     "example.test",
		Port:     80,
	})
	if !decision.Allow || approvals.Load() != 1 {
		t.Fatalf("decision = %#v, approvals = %d", decision, approvals.Load())
	}
}

func TestNetworkApprovalPolicyAmendmentPersistsAndOverlaysStartupLikeRust(t *testing.T) {
	router := newNetworkApprovalTestRouter(t, "on-request")
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		decision := map[string]any{
			string(CommandExecutionApprovalApplyNetworkPolicyAmendment): map[string]any{
				"network_policy_amendment": map[string]any{"host": "example.test", "action": string(NetworkPolicyRuleDeny)},
			},
		}
		_, _ = router.requireServerRequests().Resolve(&Response{ID: request.ID, Result: map[string]any{"decision": decision}})
	}))
	decision := router.networkApproval.Decide(context.Background(), network.ProxyPolicyRequest{Protocol: network.ProxyProtocolSocks5TCP, Host: "example.test", Port: 443, EnvironmentID: "local"})
	if decision.Allow || decision.Decision != network.ProxyPolicyDecisionDeny {
		t.Fatalf("decision = %#v", decision)
	}
	policyPath := execpolicy.DefaultPolicyPath(router.services.Config.CodexHome())
	policy, err := execpolicy.LoadPolicies([]string{policyPath})
	if err != nil {
		t.Fatalf("LoadPolicies() error = %v", err)
	}
	if len(policy.NetworkRules) != 1 || policy.NetworkRules[0].Host != "example.test" || policy.NetworkRules[0].Protocol != "socks5_tcp" || policy.NetworkRules[0].Decision != execpolicy.DecisionForbidden {
		t.Fatalf("network rules = %#v", policy.NetworkRules)
	}
	fragments := router.networkApproval.takeNetworkRulesSaved("thread-network", "turn-network")
	if len(fragments) != 1 || networkRuleSavedText(fragments[0]) != "Denied network rule saved in execpolicy (denylist): example.test" {
		t.Fatalf("saved network rule fragments = %#v", fragments)
	}
	proxyConfig := &network.ProxyConfig{Network: network.DefaultProxySettings()}
	proxyConfig.Network.SetAllowedDomains([]string{"example.test"})
	if err := applyExecPolicyNetworkRules(proxyConfig, router.services.Config.CodexHome()); err != nil {
		t.Fatalf("applyExecPolicyNetworkRules() error = %v", err)
	}
	if len(proxyConfig.Network.AllowedDomains()) != 0 || len(proxyConfig.Network.DeniedDomains()) != 1 || proxyConfig.Network.DeniedDomains()[0] != "example.test" {
		t.Fatalf("proxy domains allow=%v deny=%v", proxyConfig.Network.AllowedDomains(), proxyConfig.Network.DeniedDomains())
	}
}

func TestNetworkApprovalSavedRuleIsInjectedAfterToolLikeRust(t *testing.T) {
	router := newNetworkApprovalTestRouter(t, "on-request")
	router.networkApproval.rememberNetworkRuleSaved("thread-network", "turn-network", &NetworkPolicyAmendment{Host: "api.example.test", Action: NetworkPolicyRuleAllow})
	var persisted []session.Item
	postTool := router.networkApprovalPostToolInputItems("thread-network", "turn-network", nil, func(items []session.Item) {
		persisted = append(persisted, items...)
	})
	input := postTool(context.Background(), nil, nil)
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	want := "Allowed network rule saved in execpolicy (allowlist): api.example.test"
	if len(input) != 1 || !strings.Contains(string(data), want) || len(persisted) != 1 || persisted[0].Role != "developer" || persisted[0].Text != want {
		t.Fatalf("input=%s persisted=%#v", data, persisted)
	}
	if repeated := postTool(context.Background(), nil, nil); len(repeated) != 0 {
		t.Fatalf("saved rule was injected more than once: %#v", repeated)
	}
}

func TestNetworkApprovalForkCopiesOnlySessionApprovedHostsLikeRust(t *testing.T) {
	router := newNetworkApprovalTestRouter(t, "on-request")
	approved := networkApprovalKey{threadID: "source", environmentID: "local", protocol: network.ProxyProtocolHTTPSConnect, host: "allowed.test", port: 443}
	denied := networkApprovalKey{threadID: "source", environmentID: "local", protocol: network.ProxyProtocolHTTP, host: "denied.test", port: 80}
	router.networkApproval.mu.Lock()
	router.networkApproval.allowed[approved] = struct{}{}
	router.networkApproval.denied[denied] = struct{}{}
	router.networkApproval.mu.Unlock()
	router.networkApproval.syncApprovedHostsForFork("source", "target")
	approved.threadID = "target"
	denied.threadID = "target"
	router.networkApproval.mu.Lock()
	_, copiedApproval := router.networkApproval.allowed[approved]
	_, copiedDenial := router.networkApproval.denied[denied]
	router.networkApproval.mu.Unlock()
	if !copiedApproval || copiedDenial {
		t.Fatalf("copied approval=%t copied denial=%t", copiedApproval, copiedDenial)
	}
}

func TestNetworkApprovalNeverPolicyDoesNotPromptLikeRust(t *testing.T) {
	router := newNetworkApprovalTestRouter(t, "never")
	var requests atomic.Int32
	router.SetServerRequestSink(ServerRequestSinkFunc(func(*ServerRequest) { requests.Add(1) }))
	decision := router.networkApproval.Decide(context.Background(), network.ProxyPolicyRequest{Protocol: network.ProxyProtocolHTTP, Host: "example.test", Port: 80, EnvironmentID: "local", Method: "GET"})
	if decision.Allow || requests.Load() != 0 {
		t.Fatalf("decision = %#v, requests = %d", decision, requests.Load())
	}
}

func TestNetworkBlockedObserverCancelsSingleCommandWithRustPolicyMessage(t *testing.T) {
	router := newNetworkApprovalTestRouter(t, "on-request")
	var canceled error
	invocation := &tool.Invocation{
		CallID:   "call-network",
		ToolName: tool.PlainName("exec_command"),
		Cancel:   func(err error) { canceled = err },
	}
	router.networkApproval.registerActiveCall("thread-network", "turn-network", invocation)
	router.networkApproval.OnBlockedRequest(context.Background(), network.ProxyBlockedRequest{
		Host:     "blocked.example",
		Reason:   network.ProxyReasonDenied,
		Decision: string(network.ProxyPolicyDecisionDeny),
	})
	if canceled == nil || !strings.Contains(canceled.Error(), `Network access to "blocked.example" was blocked: domain is explicitly denied`) {
		t.Fatalf("cancel cause = %v", canceled)
	}
	output := &tool.Output{Success: true, Body: "command output"}
	post := router.networkApprovalPostToolInputItems("thread-network", "turn-network", nil, nil)
	post(context.Background(), invocation, output)
	if output.Success || output.Body == "command output" || output.Error == "" {
		t.Fatalf("output = %#v", output)
	}
}

func TestNetworkBlockedObserverDoesNotGuessAcrossConcurrentCommandsLikeRust(t *testing.T) {
	router := newNetworkApprovalTestRouter(t, "on-request")
	var cancellations atomic.Int32
	for _, callID := range []string{"call-1", "call-2"} {
		router.networkApproval.registerActiveCall("thread-network", "turn-network", &tool.Invocation{
			CallID:   callID,
			ToolName: tool.PlainName("exec_command"),
			Cancel:   func(error) { cancellations.Add(1) },
		})
	}
	router.networkApproval.OnBlockedRequest(context.Background(), network.ProxyBlockedRequest{
		Host:     "blocked.example",
		Reason:   network.ProxyReasonNotAllowed,
		Decision: string(network.ProxyPolicyDecisionDeny),
	})
	if cancellations.Load() != 0 {
		t.Fatalf("cancellations = %d", cancellations.Load())
	}
}

func TestThreadScopedBlockedObserverAttributesAcrossConcurrentThreadsLikeRust(t *testing.T) {
	router := newNetworkApprovalTestRouter(t, "on-request")
	var firstCanceled atomic.Int32
	var secondCanceled atomic.Int32
	router.networkApproval.registerActiveCall("thread-1", "turn-1", &tool.Invocation{CallID: "call-1", ToolName: tool.PlainName("exec_command"), Cancel: func(error) { firstCanceled.Add(1) }})
	router.networkApproval.registerActiveCall("thread-2", "turn-2", &tool.Invocation{CallID: "call-2", ToolName: tool.PlainName("exec_command"), Cancel: func(error) { secondCanceled.Add(1) }})
	router.networkApproval.onBlockedRequestForThread("thread-2", network.ProxyBlockedRequest{
		Host:     "blocked.example",
		Reason:   network.ProxyReasonDenied,
		Decision: string(network.ProxyPolicyDecisionDeny),
	})
	if firstCanceled.Load() != 0 || secondCanceled.Load() != 1 {
		t.Fatalf("cancellations = %d/%d", firstCanceled.Load(), secondCanceled.Load())
	}
}

func TestDeniedNetworkPolicyMessageLikeRust(t *testing.T) {
	cases := map[string]string{
		network.ProxyReasonDenied:           "domain is explicitly denied by policy and cannot be approved from this prompt",
		network.ProxyReasonNotAllowed:       "domain is not on the allowlist for the current sandbox mode",
		network.ProxyReasonNotAllowedLocal:  "local/private network addresses are blocked by the sandbox policy",
		network.ProxyReasonMethodNotAllowed: "request method is blocked by the current network mode",
		network.ProxyReasonProxyDisabled:    "network proxy is disabled",
	}
	for reason, detail := range cases {
		got := deniedNetworkPolicyMessage(network.ProxyBlockedRequest{Host: "example.com", Reason: reason})
		if got != `Network access to "example.com" was blocked: `+detail+"." {
			t.Fatalf("reason %q message = %q", reason, got)
		}
	}
}

func TestManagedNetworkProxyRoutesAllowlistMissThroughAppServerApprovalLikeRust(t *testing.T) {
	home := t.TempDir()
	configBody := "approval_policy = \"on-request\"\nsandbox_mode = \"workspace-write\"\n" +
		"[network_proxy]\n" +
		"enabled = true\n" +
		"proxy_url = \"http://127.0.0.1:0\"\n" +
		"enable_socks5 = false\n" +
		"allow_local_binding = true\n" +
		"mode = \"full\"\n"
	if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
		t.Fatal(err)
	}
	router := NewDefaultRuntimeRouterWithOptions(session.NewStore(home), home, &RuntimeRouterOptions{
		Requirements: &config.ConfigRequirements{Network: &config.NetworkRequirements{}},
	})
	t.Cleanup(func() { _ = router.Close() })
	params := &turn.TurnStartParams{ThreadID: "thread-network-e2e", CWD: home}
	if err := router.registerActiveRuntimeTurn("thread-network-e2e", "turn-network-e2e", func() {}, 1, params); err != nil {
		t.Fatal(err)
	}
	var approvals atomic.Int32
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		approvals.Add(1)
		approval, ok := request.Params.(*CommandExecutionRequestApprovalParams)
		if !ok || approval.NetworkApprovalContext == nil {
			t.Errorf("approval params = %#v", request.Params)
		}
		_, _ = router.requireServerRequests().Resolve(&Response{ID: request.ID, Result: map[string]any{"decision": string(CommandExecutionApprovalAccept)}})
	}))
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("approved")) }))
	defer origin.Close()
	proxyURL, err := url.Parse(router.services.ManagedNetwork.Env["HTTP_PROXY"])
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	response, err := client.Get(origin.URL)
	if err != nil {
		t.Fatalf("approved proxied GET error = %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if approvals.Load() != 1 {
		t.Fatalf("approval requests = %d, want 1", approvals.Load())
	}
}

func newNetworkApprovalTestRouter(t *testing.T, approvalPolicy string) *RuntimeRouter {
	t.Helper()
	home := t.TempDir()
	configBody := "approval_policy = \"" + approvalPolicy + "\"\nsandbox_mode = \"workspace-write\"\n"
	if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home), DefaultCWD: home})
	params := &turn.TurnStartParams{ThreadID: "thread-network", CWD: home}
	if err := router.registerActiveRuntimeTurn("thread-network", "turn-network", func() {}, 1, params); err != nil {
		t.Fatal(err)
	}
	router.updateActiveRuntimeTurnAnalytics("thread-network", "turn-network", "connection-network", &appTurnRunConfig{Model: "gpt-test", Originator: "codex-test", ApprovalPolicy: approvalPolicy})
	t.Cleanup(func() {
		router.clearActiveRuntimeTurn("thread-network", "turn-network")
		_ = os.Remove(filepath.Join(home, "rules", "default.rules.lock"))
	})
	return router
}
