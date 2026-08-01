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

	"codex_go/config"
	"codex_go/execpolicy"
	"codex_go/network"
	"codex_go/session"
	"codex_go/state"
	"codex_go/tool"
	"codex_go/turn"
)

type networkGuardianReviewerFunc func(context.Context, string, string, string, state.Action) (state.ReviewDecision, string, error)

func (f networkGuardianReviewerFunc) Review(ctx context.Context, threadID, turnID, targetItemID string, action state.Action) (state.ReviewDecision, string, error) {
	return f(ctx, threadID, turnID, targetItemID, action)
}

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

func TestRemoteGuardianNetworkDecisionsAreScopedPerRequestAndEnvironmentLikeRust(t *testing.T) {
	const (
		threadID    = "thread-remote-guardian"
		host        = "network.test"
		remoteID    = "remote-primary"
		remoteCWD   = "/remote/workspace"
		localCWD    = "/local/workspace"
		sessionCall = "remote-guardian-session-approval"
		probeCall   = "remote-guardian-session-probe"
	)
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("approval_policy = \"on-request\"\nsandbox_mode = \"workspace-write\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	environments := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, localCWD)
	if _, err := environments.Add(&EnvironmentAddParams{EnvironmentID: remoteID, ExecServerURL: "ws://remote.invalid"}); err != nil {
		t.Fatal(err)
	}
	if err := environments.SetInfo(remoteID, EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, remoteCWD); err != nil {
		t.Fatal(err)
	}
	if err := environments.SetInfo("local", EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, localCWD); err != nil {
		t.Fatal(err)
	}

	type reviewRecord struct {
		threadID       string
		turnID         string
		targetItemID   string
		environmentID  string
		environmentCWD string
		action         state.Action
	}
	type guardianCase struct {
		callID         string
		command        string
		environmentID  string
		environmentCWD string
		approved       bool
	}
	decisions := map[string]state.ReviewDecision{
		"remote-guardian-deny":       state.DecisionDenied,
		"remote-guardian-allow":      state.DecisionApproved,
		"remote-guardian-deny-again": state.DecisionDenied,
		"local-guardian-deny":        state.DecisionDenied,
	}
	rationales := map[string]string{
		"remote-guardian-deny":       "The first remote request must be denied.",
		"remote-guardian-allow":      "This remote request is safe to allow once.",
		"remote-guardian-deny-again": "A previous remote approval must not approve a later request.",
		"local-guardian-deny":        "A remote approval must not approve the local environment.",
	}
	var router *RuntimeRouter
	var reviews []reviewRecord
	reviewer := networkGuardianReviewerFunc(func(_ context.Context, gotThreadID, turnID, targetItemID string, action state.Action) (state.ReviewDecision, string, error) {
		trigger, ok := action.Extra["trigger"].(guardianNetworkAccessTrigger)
		if !ok {
			t.Fatalf("network trigger = %#v", action.Extra["trigger"])
		}
		params := router.activeTurnParams(gotThreadID)
		if params == nil || len(params.Environments) != 1 {
			t.Fatalf("Guardian environment params = %#v", params)
		}
		environment := params.Environments[0]
		reviews = append(reviews, reviewRecord{
			threadID:       gotThreadID,
			turnID:         turnID,
			targetItemID:   targetItemID,
			environmentID:  stringFromAny(environment["environmentId"]),
			environmentCWD: stringFromAny(environment["cwd"]),
			action:         action,
		})
		decision, ok := decisions[trigger.CallID]
		if !ok {
			t.Fatalf("unexpected Guardian call id %q", trigger.CallID)
		}
		return decision, rationales[trigger.CallID], nil
	})
	router = NewRuntimeRouter(RuntimeServices{
		Config:           config.NewConfigService(home),
		DefaultCWD:       home,
		Environment:      environments,
		GuardianReviewer: reviewer,
	})
	defer router.Close()

	var userPrompts atomic.Int32
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		userPrompts.Add(1)
		params, ok := request.Params.(*CommandExecutionRequestApprovalParams)
		if !ok || params.EnvironmentID == nil || *params.EnvironmentID != remoteID || params.NetworkApprovalContext == nil {
			t.Errorf("session approval params = %#v", request.Params)
		}
		_, _ = router.requireServerRequests().Resolve(&Response{ID: request.ID, Result: map[string]any{
			"decision": string(CommandExecutionApprovalAcceptForSession),
		}})
	}))

	currentTurnID := ""
	beginTurn := func(turnID, reviewerName, environmentID, environmentCWD string) {
		t.Helper()
		if currentTurnID != "" {
			router.clearActiveRuntimeTurn(threadID, currentTurnID)
		}
		currentTurnID = turnID
		params := &turn.TurnStartParams{
			ThreadID:          threadID,
			CWD:               home,
			ApprovalsReviewer: &reviewerName,
			Environments: []map[string]any{{
				"environmentId": environmentID,
				"cwd":           environmentCWD,
			}},
		}
		if err := router.registerActiveRuntimeTurn(threadID, turnID, func() {}, 1, params); err != nil {
			t.Fatal(err)
		}
		router.updateActiveRuntimeTurnAnalytics(threadID, turnID, "connection-remote-guardian", &appTurnRunConfig{
			ApprovalPolicy:    "on-request",
			ApprovalsReviewer: reviewerName,
		})
	}
	defer func() {
		if currentTurnID != "" {
			router.clearActiveRuntimeTurn(threadID, currentTurnID)
		}
	}()

	runRequest := func(callID, command, reviewerName, environmentID, environmentCWD string) (network.ProxyDecision, string) {
		t.Helper()
		turnID := "turn-" + callID
		beginTurn(turnID, reviewerName, environmentID, environmentCWD)
		arguments, err := json.Marshal(tool.ExecCommandArgs{Cmd: command, EnvironmentID: environmentID})
		if err != nil {
			t.Fatal(err)
		}
		invocation := &tool.Invocation{
			CallID:   callID,
			ToolName: tool.PlainName("exec_command"),
			Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: string(arguments)},
		}
		router.networkApproval.registerActiveCall(threadID, turnID, invocation)
		decision := router.networkApproval.decideForThread(context.Background(), threadID, network.ProxyPolicyRequest{
			Protocol:      network.ProxyProtocolHTTP,
			Host:          host,
			Port:          80,
			EnvironmentID: environmentID,
			ExecutionID:   callID,
			Method:        "GET",
		})
		outcome := router.networkApproval.finishActiveCall(threadID, turnID, invocation)
		return decision, outcome
	}

	cases := []guardianCase{
		{callID: "remote-guardian-deny", command: "printf REMOTE_GUARDIAN_DENY", environmentID: remoteID, environmentCWD: remoteCWD},
		{callID: "remote-guardian-allow", command: "printf REMOTE_GUARDIAN_ALLOW", environmentID: remoteID, environmentCWD: remoteCWD, approved: true},
		{callID: "remote-guardian-deny-again", command: "printf REMOTE_GUARDIAN_DENY_AGAIN", environmentID: remoteID, environmentCWD: remoteCWD},
	}
	for _, testCase := range cases {
		decision, outcome := runRequest(testCase.callID, testCase.command, "auto_review", testCase.environmentID, testCase.environmentCWD)
		if decision.Allow != testCase.approved {
			t.Fatalf("%s decision = %#v", testCase.callID, decision)
		}
		if testCase.approved && outcome != "" {
			t.Fatalf("%s outcome = %q", testCase.callID, outcome)
		}
		if !testCase.approved && outcome != rationales[testCase.callID] {
			t.Fatalf("%s outcome = %q", testCase.callID, outcome)
		}
	}

	if decision, outcome := runRequest(sessionCall, "printf REMOTE_GUARDIAN_SESSION", "user", remoteID, remoteCWD); !decision.Allow || outcome != "" {
		t.Fatalf("session approval decision=%#v outcome=%q", decision, outcome)
	}
	localCase := guardianCase{callID: "local-guardian-deny", command: "printf LOCAL_GUARDIAN_DENY", environmentID: "local", environmentCWD: localCWD}
	if decision, outcome := runRequest(localCase.callID, localCase.command, "auto_review", "local", localCWD); decision.Allow || outcome != rationales[localCase.callID] {
		t.Fatalf("local decision=%#v outcome=%q", decision, outcome)
	}
	if decision, outcome := runRequest(probeCall, "printf REMOTE_GUARDIAN_SESSION_PROBE", "user", remoteID, remoteCWD); !decision.Allow || outcome != "" {
		t.Fatalf("session probe decision=%#v outcome=%q", decision, outcome)
	}
	beginTurn("turn-unknown-execution", "user", remoteID, remoteCWD)
	unknownInvocation := &tool.Invocation{CallID: "known-call", ToolName: tool.PlainName("exec_command"), Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"cmd":"printf KNOWN","environment_id":"remote-primary"}`}}
	router.networkApproval.registerActiveCall(threadID, "turn-unknown-execution", unknownInvocation)
	unknownDecision := router.networkApproval.decideForThread(context.Background(), threadID, network.ProxyPolicyRequest{
		Protocol: network.ProxyProtocolHTTP, Host: host, Port: 80, EnvironmentID: remoteID, ExecutionID: "unknown-call",
	})
	_ = router.networkApproval.finishActiveCall(threadID, "turn-unknown-execution", unknownInvocation)
	if unknownDecision.Allow {
		t.Fatalf("unknown execution reused a session approval: %#v", unknownDecision)
	}

	if userPrompts.Load() != 1 {
		t.Fatalf("user network prompts = %d, want only the explicit session approval", userPrompts.Load())
	}
	if len(reviews) != 4 {
		t.Fatalf("Guardian reviews = %d, want 4", len(reviews))
	}
	router.networkApproval.mu.Lock()
	allowedKeys := make([]networkApprovalKey, 0, len(router.networkApproval.allowed))
	for key := range router.networkApproval.allowed {
		allowedKeys = append(allowedKeys, key)
	}
	deniedCount := len(router.networkApproval.denied)
	pendingCount := len(router.networkApproval.pending)
	router.networkApproval.mu.Unlock()
	if len(allowedKeys) != 1 || allowedKeys[0].threadID != threadID || allowedKeys[0].environmentID != remoteID || allowedKeys[0].host != host || allowedKeys[0].protocol != network.ProxyProtocolHTTP || allowedKeys[0].port != 80 || deniedCount != 0 || pendingCount != 0 {
		t.Fatalf("network approval cache allowed=%#v denied=%d pending=%d", allowedKeys, deniedCount, pendingCount)
	}
	expectedCases := append(append([]guardianCase(nil), cases...), localCase)
	for index, review := range reviews {
		expected := expectedCases[index]
		if review.threadID != threadID || review.turnID != "turn-"+expected.callID || review.targetItemID != "" || review.environmentID != expected.environmentID || review.environmentCWD != expected.environmentCWD {
			t.Fatalf("review %d context = %#v", index, review)
		}
		action := review.action
		if action.Type != "network_access" || action.Host != host || action.Protocol != "http" || action.Port != 80 || action.Target != "http://"+host+":80" {
			t.Fatalf("review %d action = %#v", index, action)
		}
		trigger, ok := action.Extra["trigger"].(guardianNetworkAccessTrigger)
		if !ok {
			t.Fatalf("review %d trigger = %#v", index, action.Extra["trigger"])
		}
		wantCommand := []string{"/bin/sh", "-c", expected.command}
		if trigger.CallID != expected.callID || trigger.ToolName != "exec_command" || trigger.CWD != expected.environmentCWD || trigger.SandboxPermissions != "use_default" || trigger.TTY || strings.Join(trigger.Command, "\x00") != strings.Join(wantCommand, "\x00") {
			t.Fatalf("review %d trigger = %#v, want command %#v", index, trigger, wantCommand)
		}
		if trigger.AdditionalPermissions != nil || trigger.Justification != nil {
			t.Fatalf("review %d optional trigger fields = %#v", index, trigger)
		}
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

func TestNetworkApprovalCoalescedWaiterCancellationDoesNotCancelOwnerLikeRust(t *testing.T) {
	router := newNetworkApprovalTestRouter(t, "on-request")
	requestSeen := make(chan *ServerRequest, 1)
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		requestSeen <- request
	}))
	policyRequest := network.ProxyPolicyRequest{Protocol: network.ProxyProtocolHTTP, Host: "example.test", Port: 80, EnvironmentID: "local", Method: "GET"}
	ownerResult := make(chan network.ProxyDecision, 1)
	go func() { ownerResult <- router.networkApproval.Decide(context.Background(), policyRequest) }()
	request := <-requestSeen

	waiterCtx, cancelWaiter := context.WithCancel(context.Background())
	waiterResult := make(chan network.ProxyDecision, 1)
	go func() { waiterResult <- router.networkApproval.Decide(waiterCtx, policyRequest) }()
	cancelWaiter()
	if decision := <-waiterResult; decision.Allow {
		t.Fatalf("cancelled waiter decision = %#v", decision)
	}
	select {
	case decision := <-ownerResult:
		t.Fatalf("owner completed before approval response: %#v", decision)
	default:
	}
	if resolved, err := router.requireServerRequests().Resolve(&Response{ID: request.ID, Result: map[string]any{"decision": string(CommandExecutionApprovalAccept)}}); err != nil || !resolved {
		t.Fatalf("Resolve() = %t, %v", resolved, err)
	}
	if decision := <-ownerResult; !decision.Allow {
		t.Fatalf("owner decision = %#v", decision)
	}
}

func TestNetworkApprovalTurnTerminalCancelsPendingAndIgnoresLateResponseLikeRust(t *testing.T) {
	router := newNetworkApprovalTestRouter(t, "on-request")
	requestSeen := make(chan *ServerRequest, 1)
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		requestSeen <- request
	}))
	result := make(chan network.ProxyDecision, 1)
	go func() {
		result <- router.networkApproval.Decide(context.Background(), network.ProxyPolicyRequest{
			Protocol: network.ProxyProtocolHTTPSConnect,
			Host:     "late.example",
			Port:     443,
		})
	}()
	request := <-requestSeen
	router.clearActiveRuntimeTurn("thread-network", "turn-network")
	select {
	case decision := <-result:
		if decision.Allow {
			t.Fatalf("terminal decision = %#v", decision)
		}
	case <-time.After(time.Second):
		t.Fatal("pending approval was not cancelled at turn terminal")
	}
	if resolved, err := router.requireServerRequests().Resolve(&Response{ID: request.ID, Result: map[string]any{"decision": string(CommandExecutionApprovalAcceptForSession)}}); err != nil || resolved {
		t.Fatalf("late Resolve() = %t, %v", resolved, err)
	}
	key := networkApprovalKey{threadID: "thread-network", environmentID: "local", protocol: network.ProxyProtocolHTTPSConnect, host: "late.example", port: 443}
	router.networkApproval.mu.Lock()
	_, allowed := router.networkApproval.allowed[key]
	_, denied := router.networkApproval.denied[key]
	_, pending := router.networkApproval.pending[pendingNetworkApprovalKey{networkApprovalKey: key, turnID: "turn-network"}]
	router.networkApproval.mu.Unlock()
	if allowed || denied || pending {
		t.Fatalf("terminal approval leaked state: allowed=%t denied=%t pending=%t", allowed, denied, pending)
	}
}

func TestNetworkApprovalConnectionCloseCancelsOnlyMatchingPendingLikeRust(t *testing.T) {
	router := newNetworkApprovalTestRouter(t, "on-request")
	home := router.services.DefaultCWD
	if err := router.registerActiveRuntimeTurn("thread-network-2", "turn-network-2", func() {}, 1, &turn.TurnStartParams{ThreadID: "thread-network-2", CWD: home}); err != nil {
		t.Fatal(err)
	}
	defer router.clearActiveRuntimeTurn("thread-network-2", "turn-network-2")
	router.updateActiveRuntimeTurnAnalytics("thread-network-2", "turn-network-2", "connection-network-2", &appTurnRunConfig{ApprovalPolicy: "on-request"})
	requests := make(chan *ServerRequest, 2)
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) { requests <- request }))
	firstResult := make(chan network.ProxyDecision, 1)
	secondResult := make(chan network.ProxyDecision, 1)
	go func() {
		firstResult <- router.networkApproval.decideForThread(context.Background(), "thread-network", network.ProxyPolicyRequest{Protocol: network.ProxyProtocolHTTP, Host: "first.example", Port: 80})
	}()
	go func() {
		secondResult <- router.networkApproval.decideForThread(context.Background(), "thread-network-2", network.ProxyPolicyRequest{Protocol: network.ProxyProtocolHTTP, Host: "second.example", Port: 80})
	}()
	var secondRequest *ServerRequest
	for range 2 {
		request := <-requests
		params := request.Params.(*CommandExecutionRequestApprovalParams)
		if params.ThreadID == "thread-network-2" {
			secondRequest = request
		}
	}
	router.ConnectionClosed("connection-network")
	select {
	case decision := <-firstResult:
		if decision.Allow {
			t.Fatalf("closed connection decision = %#v", decision)
		}
	case <-time.After(time.Second):
		t.Fatal("connection close did not cancel matching approval")
	}
	select {
	case decision := <-secondResult:
		t.Fatalf("other connection completed early: %#v", decision)
	default:
	}
	if secondRequest == nil {
		t.Fatal("second connection request was not observed")
	}
	if resolved, err := router.requireServerRequests().Resolve(&Response{ID: secondRequest.ID, Result: map[string]any{"decision": string(CommandExecutionApprovalAccept)}}); err != nil || !resolved {
		t.Fatalf("Resolve(second) = %t, %v", resolved, err)
	}
	if decision := <-secondResult; !decision.Allow {
		t.Fatalf("other connection decision = %#v", decision)
	}
}

func TestNetworkApprovalConcurrentDifferentHostsResolveIndependentlyLikeRust(t *testing.T) {
	router := newNetworkApprovalTestRouter(t, "on-request")
	requests := make(chan *ServerRequest, 2)
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) { requests <- request }))
	results := map[string]chan network.ProxyDecision{
		"allow.example": make(chan network.ProxyDecision, 1),
		"deny.example":  make(chan network.ProxyDecision, 1),
	}
	for host, result := range results {
		host := host
		result := result
		go func() {
			result <- router.networkApproval.Decide(context.Background(), network.ProxyPolicyRequest{Protocol: network.ProxyProtocolHTTP, Host: host, Port: 80, EnvironmentID: "local"})
		}()
	}
	requestByHost := map[string]*ServerRequest{}
	for range 2 {
		request := <-requests
		params := request.Params.(*CommandExecutionRequestApprovalParams)
		requestByHost[params.NetworkApprovalContext.(*NetworkApprovalContext).Host] = request
	}
	if resolved, err := router.requireServerRequests().Resolve(&Response{ID: requestByHost["deny.example"].ID, Result: map[string]any{"decision": string(CommandExecutionApprovalDecline)}}); err != nil || !resolved {
		t.Fatalf("Resolve(deny) = %t, %v", resolved, err)
	}
	if decision := <-results["deny.example"]; decision.Allow {
		t.Fatalf("deny decision = %#v", decision)
	}
	select {
	case decision := <-results["allow.example"]:
		t.Fatalf("allow host completed with deny host: %#v", decision)
	default:
	}
	if resolved, err := router.requireServerRequests().Resolve(&Response{ID: requestByHost["allow.example"].ID, Result: map[string]any{"decision": string(CommandExecutionApprovalAccept)}}); err != nil || !resolved {
		t.Fatalf("Resolve(allow) = %t, %v", resolved, err)
	}
	if decision := <-results["allow.example"]; !decision.Allow {
		t.Fatalf("allow decision = %#v", decision)
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

func TestNetworkApprovalFailedAllowAmendmentDeniesAndPromptsAgainLikeRust(t *testing.T) {
	router := newNetworkApprovalTestRouter(t, "on-request")
	home := router.services.Config.CodexHome()
	if err := os.WriteFile(filepath.Join(home, "rules"), []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile(rules blocker) error = %v", err)
	}
	var requests atomic.Int32
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		requests.Add(1)
		decision := map[string]any{
			string(CommandExecutionApprovalApplyNetworkPolicyAmendment): map[string]any{
				"network_policy_amendment": map[string]any{"host": "failed.example", "action": string(NetworkPolicyRuleAllow)},
			},
		}
		_, _ = router.requireServerRequests().Resolve(&Response{ID: request.ID, Result: map[string]any{"decision": decision}})
	}))
	invocation := &tool.Invocation{CallID: "call-failed-amendment", ToolName: tool.PlainName("exec_command")}
	router.networkApproval.registerActiveCall("thread-network", "turn-network", invocation)
	policyRequest := network.ProxyPolicyRequest{Protocol: network.ProxyProtocolHTTPSConnect, Host: "failed.example", Port: 443, EnvironmentID: "local"}
	for attempt := 0; attempt < 2; attempt++ {
		decision := router.networkApproval.Decide(context.Background(), policyRequest)
		if decision.Allow || decision.Decision != network.ProxyPolicyDecisionDeny {
			t.Fatalf("attempt %d decision = %#v, want deny", attempt+1, decision)
		}
	}
	if requests.Load() != 2 {
		t.Fatalf("approval requests = %d, want a new prompt after each failed amendment", requests.Load())
	}
	key := networkApprovalKey{threadID: "thread-network", environmentID: "local", protocol: network.ProxyProtocolHTTPSConnect, host: "failed.example", port: 443}
	router.networkApproval.mu.Lock()
	_, allowed := router.networkApproval.allowed[key]
	_, denied := router.networkApproval.denied[key]
	router.networkApproval.mu.Unlock()
	if allowed || denied {
		t.Fatalf("failed amendment was cached: allowed=%t denied=%t", allowed, denied)
	}
	if fragments := router.networkApproval.takeNetworkRulesSaved("thread-network", "turn-network"); len(fragments) != 0 {
		t.Fatalf("failed amendment emitted saved fragments: %#v", fragments)
	}
	if outcome := router.networkApproval.finishActiveCall("thread-network", "turn-network", invocation); !strings.Contains(outcome, "blocked by policy") {
		t.Fatalf("active call outcome = %q", outcome)
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
