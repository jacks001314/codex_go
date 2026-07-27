package appserver

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"codex_go/auth"
	"codex_go/execpolicy"
	"codex_go/network"
	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/tool"
	"codex_go/turn"
)

type networkApprovalKey struct {
	threadID      string
	environmentID string
	protocol      network.ProxyProtocol
	host          string
	port          uint16
}

type pendingNetworkApproval struct {
	done         chan struct{}
	decision     network.ProxyDecision
	turnID       string
	connectionID string
	cancel       context.CancelFunc
}

type networkApprovalService struct {
	router  *RuntimeRouter
	mu      sync.Mutex
	pending map[networkApprovalKey]*pendingNetworkApproval
	allowed map[networkApprovalKey]struct{}
	denied  map[networkApprovalKey]struct{}
	saved   map[string][]networkRuleSavedFragment
	callsMu sync.Mutex
	calls   map[string]*activeNetworkApprovalCall
}

type activeNetworkApprovalCall struct {
	threadID string
	turnID   string
	callID   string
	cancel   context.CancelCauseFunc
	outcome  string
}

type networkRuleSavedFragment struct {
	host   string
	action NetworkPolicyRuleAction
}

type networkApprovalTurn struct {
	threadID     string
	turnID       string
	connectionID string
	params       *turn.TurnStartParams
	runConfig    *appTurnRunConfig
}

func newNetworkApprovalService(router *RuntimeRouter) *networkApprovalService {
	return &networkApprovalService{
		router:  router,
		pending: map[networkApprovalKey]*pendingNetworkApproval{},
		allowed: map[networkApprovalKey]struct{}{},
		denied:  map[networkApprovalKey]struct{}{},
		saved:   map[string][]networkRuleSavedFragment{},
		calls:   map[string]*activeNetworkApprovalCall{},
	}
}

func (s *networkApprovalService) Decide(ctx context.Context, request network.ProxyPolicyRequest) network.ProxyDecision {
	return s.decide(ctx, "", request)
}

func (s *networkApprovalService) decideForThread(ctx context.Context, threadID string, request network.ProxyPolicyRequest) network.ProxyDecision {
	return s.decide(ctx, strings.TrimSpace(threadID), request)
}

func (s *networkApprovalService) decide(ctx context.Context, threadID string, request network.ProxyPolicyRequest) network.ProxyDecision {
	if s == nil || s.router == nil {
		return network.AskProxyDecision(network.ProxyReasonNotAllowed)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	active := s.router.activeTurnForNetworkApprovalThread(threadID, request.EnvironmentID)
	if active == nil {
		return network.DenyProxyDecision(network.ProxyReasonNotAllowed)
	}
	if !s.router.networkApprovalAllowedForTurn(active) {
		return network.DenyProxyDecision(network.ProxyReasonNotAllowed)
	}
	key := networkApprovalKey{
		threadID:      active.threadID,
		environmentID: firstNonEmpty(strings.TrimSpace(request.EnvironmentID), "local"),
		protocol:      request.Protocol,
		host:          network.NormalizeProxyHost(request.Host),
		port:          request.Port,
	}

	s.mu.Lock()
	if _, ok := s.denied[key]; ok {
		s.mu.Unlock()
		s.recordUserDenialForThread(active.threadID)
		return network.DenyProxyDecision(network.ProxyReasonNotAllowed)
	}
	if _, ok := s.allowed[key]; ok {
		s.mu.Unlock()
		return network.AllowProxyDecision()
	}
	if pending := s.pending[key]; pending != nil {
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return network.DenyProxyDecision(network.ProxyReasonNotAllowed)
		case <-pending.done:
			return pending.decision
		}
	}
	approvalCtx, approvalCancel := context.WithCancel(ctx)
	pending := &pendingNetworkApproval{
		done:         make(chan struct{}),
		turnID:       active.turnID,
		connectionID: active.connectionID,
		cancel:       approvalCancel,
	}
	s.pending[key] = pending
	s.mu.Unlock()

	decision, cache := s.requestApproval(approvalCtx, active, key, request)
	approvalCancel()
	s.mu.Lock()
	if s.pending[key] != pending {
		decision = pending.decision
		s.mu.Unlock()
		return decision
	}
	switch cache {
	case NetworkPolicyRuleAllow:
		delete(s.denied, key)
		s.allowed[key] = struct{}{}
	case NetworkPolicyRuleDeny:
		delete(s.allowed, key)
		s.denied[key] = struct{}{}
	}
	pending.decision = decision
	delete(s.pending, key)
	close(pending.done)
	s.mu.Unlock()
	return decision
}

func (s *networkApprovalService) requestApproval(ctx context.Context, active *networkApprovalTurn, key networkApprovalKey, request network.ProxyPolicyRequest) (network.ProxyDecision, NetworkPolicyRuleAction) {
	protocol := networkApprovalProtocol(request.Protocol)
	target := networkApprovalTarget(request.Protocol, request.Host, request.Port)
	approvalID := fmt.Sprintf("network#%s#%s#%s#%d", key.environmentID, networkApprovalProtocolKey(protocol), key.host, key.port)
	environmentID := key.environmentID
	reason := fmt.Sprintf("%s is not in the allowed_domains", request.Host)
	command := "network-access " + target
	params := &CommandExecutionRequestApprovalParams{
		ThreadID:               active.threadID,
		TurnID:                 active.turnID,
		ItemID:                 approvalID,
		StartedAtMS:            uint64(time.Now().UTC().UnixMilli()),
		EnvironmentID:          &environmentID,
		Reason:                 &reason,
		NetworkApprovalContext: &NetworkApprovalContext{Host: request.Host, Protocol: protocol},
		Command:                &command,
		CWD:                    stringPtrIfNotEmpty(firstNonEmpty(turnCWD(active.params), s.router.services.DefaultCWD)),
		ProposedNetworkPolicyAmendments: []map[string]any{
			{"host": request.Host, "action": string(NetworkPolicyRuleAllow)},
			{"host": request.Host, "action": string(NetworkPolicyRuleDeny)},
		},
	}
	var response CommandExecutionRequestApprovalResponse
	err := s.router.requireServerRequests().RequestToConnection(ctx, active.connectionID, ServerRequestCommandExecutionApproval, params, &response)
	if err != nil {
		return network.DenyProxyDecision(network.ProxyReasonNotAllowed), ""
	}
	switch approvalDecisionString(response.Decision) {
	case string(CommandExecutionApprovalAccept):
		return network.AllowProxyDecision(), ""
	case string(CommandExecutionApprovalAcceptForSession):
		return network.AllowProxyDecision(), NetworkPolicyRuleAllow
	case string(CommandExecutionApprovalAcceptWithExecpolicyAmendment):
		return network.AllowProxyDecision(), ""
	case string(CommandExecutionApprovalApplyNetworkPolicyAmendment):
		amendment, ok := networkPolicyAmendmentFromDecision(response.Decision)
		if !ok || network.NormalizeProxyHost(amendment.Host) != key.host {
			return network.DenyProxyDecision(network.ProxyReasonNotAllowed), ""
		}
		if err := s.router.persistNetworkPolicyAmendment(amendment, protocol); err != nil {
			slog.Warn("Failed to apply network policy amendment", "error", err)
		} else {
			s.rememberNetworkRuleSaved(active.threadID, active.turnID, amendment)
		}
		if amendment.Action == NetworkPolicyRuleAllow {
			return network.AllowProxyDecision(), NetworkPolicyRuleAllow
		}
		s.recordUserDenialForThread(active.threadID)
		return network.DenyProxyDecision(network.ProxyReasonNotAllowed), NetworkPolicyRuleDeny
	default:
		s.recordUserDenialForThread(active.threadID)
		return network.DenyProxyDecision(network.ProxyReasonNotAllowed), ""
	}
}

func networkApprovalCallKey(threadID string, turnID string, callID string) string {
	return strings.TrimSpace(threadID) + "\x00" + strings.TrimSpace(turnID) + "\x00" + strings.TrimSpace(callID)
}

func (s *networkApprovalService) registerActiveCall(threadID string, turnID string, invocation *tool.Invocation) {
	if s == nil || invocation == nil || !isNetworkApprovalCommandInvocation(invocation) {
		return
	}
	callID := firstNonEmpty(strings.TrimSpace(invocation.CallID), invocation.ToolName.Key())
	key := networkApprovalCallKey(threadID, turnID, callID)
	s.callsMu.Lock()
	s.calls[key] = &activeNetworkApprovalCall{threadID: threadID, turnID: turnID, callID: callID, cancel: invocation.Cancel}
	s.callsMu.Unlock()
}

func (s *networkApprovalService) finishActiveCall(threadID string, turnID string, invocation *tool.Invocation) string {
	if s == nil || invocation == nil || !isNetworkApprovalCommandInvocation(invocation) {
		return ""
	}
	callID := firstNonEmpty(strings.TrimSpace(invocation.CallID), invocation.ToolName.Key())
	key := networkApprovalCallKey(threadID, turnID, callID)
	s.callsMu.Lock()
	call := s.calls[key]
	delete(s.calls, key)
	s.callsMu.Unlock()
	if call == nil {
		return ""
	}
	return call.outcome
}

func (s *networkApprovalService) clearActiveCallsForTurn(threadID string, turnID string) {
	if s == nil {
		return
	}
	s.callsMu.Lock()
	defer s.callsMu.Unlock()
	for key, call := range s.calls {
		if call.threadID == threadID && (turnID == "" || call.turnID == turnID) {
			delete(s.calls, key)
		}
	}
}

func (s *networkApprovalService) cancelPendingForTurn(threadID string, turnID string) {
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	if s == nil || threadID == "" {
		return
	}
	s.cancelPending(func(key networkApprovalKey, pending *pendingNetworkApproval) bool {
		return key.threadID == threadID && (turnID == "" || pending.turnID == turnID)
	})
}

func (s *networkApprovalService) cancelPendingForConnection(connectionID string) {
	connectionID = normalizeConnectionID(connectionID)
	if s == nil || connectionID == "" {
		return
	}
	s.cancelPending(func(_ networkApprovalKey, pending *pendingNetworkApproval) bool {
		return normalizeConnectionID(pending.connectionID) == connectionID
	})
}

func (s *networkApprovalService) cancelPending(matches func(networkApprovalKey, *pendingNetworkApproval) bool) {
	if s == nil || matches == nil {
		return
	}
	denied := network.DenyProxyDecision(network.ProxyReasonNotAllowed)
	var cancels []context.CancelFunc
	s.mu.Lock()
	for key, pending := range s.pending {
		if pending == nil || !matches(key, pending) {
			continue
		}
		delete(s.pending, key)
		pending.decision = denied
		close(pending.done)
		if pending.cancel != nil {
			cancels = append(cancels, pending.cancel)
		}
	}
	s.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (s *networkApprovalService) clearThread(threadID string) {
	if s == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	s.cancelPendingForTurn(threadID, "")
	s.mu.Lock()
	for key := range s.allowed {
		if key.threadID == threadID {
			delete(s.allowed, key)
		}
	}
	for key := range s.denied {
		if key.threadID == threadID {
			delete(s.denied, key)
		}
	}
	for key := range s.saved {
		if strings.HasPrefix(key, threadID+"\x00") {
			delete(s.saved, key)
		}
	}
	s.mu.Unlock()
	s.clearActiveCallsForTurn(threadID, "")
}

func (s *networkApprovalService) recordUserDenialForThread(threadID string) {
	if s == nil {
		return
	}
	s.callsMu.Lock()
	defer s.callsMu.Unlock()
	var selected *activeNetworkApprovalCall
	for _, call := range s.calls {
		if threadID != "" && call.threadID != threadID {
			continue
		}
		if selected != nil {
			return
		}
		selected = call
	}
	if selected != nil {
		selected.outcome = "rejected by user"
	}
}

func (s *networkApprovalService) OnBlockedRequest(_ context.Context, blocked network.ProxyBlockedRequest) {
	s.onBlockedRequestForThread("", blocked)
}

func (s *networkApprovalService) onBlockedRequestForThread(threadID string, blocked network.ProxyBlockedRequest) {
	if s == nil || blocked.Decision != string(network.ProxyPolicyDecisionDeny) {
		return
	}
	message := deniedNetworkPolicyMessage(blocked)
	if message == "" {
		return
	}
	s.callsMu.Lock()
	var call *activeNetworkApprovalCall
	for _, active := range s.calls {
		if threadID != "" && active.threadID != threadID {
			continue
		}
		if call != nil {
			s.callsMu.Unlock()
			return
		}
		call = active
	}
	if call == nil {
		s.callsMu.Unlock()
		return
	}
	if call.outcome == "" {
		call.outcome = message
	}
	outcome := call.outcome
	cancel := call.cancel
	s.callsMu.Unlock()
	if cancel != nil {
		cancel(tool.RespondToModel(outcome))
	}
}

func deniedNetworkPolicyMessage(blocked network.ProxyBlockedRequest) string {
	host := strings.TrimSpace(blocked.Host)
	if host == "" {
		return "Network access was blocked by policy."
	}
	detail := "request is blocked by network policy"
	switch blocked.Reason {
	case network.ProxyReasonDenied:
		detail = "domain is explicitly denied by policy and cannot be approved from this prompt"
	case network.ProxyReasonNotAllowed:
		detail = "domain is not on the allowlist for the current sandbox mode"
	case network.ProxyReasonNotAllowedLocal:
		detail = "local/private network addresses are blocked by the sandbox policy"
	case network.ProxyReasonMethodNotAllowed:
		detail = "request method is blocked by the current network mode"
	case network.ProxyReasonProxyDisabled:
		detail = "network proxy is disabled"
	}
	return fmt.Sprintf("Network access to %q was blocked: %s.", host, detail)
}

func isNetworkApprovalCommandInvocation(invocation *tool.Invocation) bool {
	if invocation == nil {
		return false
	}
	switch invocation.ToolName.Key() {
	case "shell_command", "exec_command", "write_stdin":
		return true
	default:
		return false
	}
}

func networkApprovalTurnKey(threadID string, turnID string) string {
	return strings.TrimSpace(threadID) + "\x00" + strings.TrimSpace(turnID)
}

func (s *networkApprovalService) rememberNetworkRuleSaved(threadID string, turnID string, amendment *NetworkPolicyAmendment) {
	if s == nil || amendment == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := networkApprovalTurnKey(threadID, turnID)
	s.saved[key] = append(s.saved[key], networkRuleSavedFragment{host: amendment.Host, action: amendment.Action})
}

func (s *networkApprovalService) takeNetworkRulesSaved(threadID string, turnID string) []networkRuleSavedFragment {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := networkApprovalTurnKey(threadID, turnID)
	fragments := append([]networkRuleSavedFragment(nil), s.saved[key]...)
	delete(s.saved, key)
	return fragments
}

func (s *networkApprovalService) syncApprovedHostsForFork(sourceThreadID string, targetThreadID string) {
	if s == nil || strings.TrimSpace(sourceThreadID) == "" || strings.TrimSpace(targetThreadID) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.allowed {
		if key.threadID != sourceThreadID {
			continue
		}
		clone := key
		clone.threadID = targetThreadID
		s.allowed[clone] = struct{}{}
	}
}

func networkRuleSavedText(fragment networkRuleSavedFragment) string {
	action := "Allowed"
	listName := "allowlist"
	if fragment.action == NetworkPolicyRuleDeny {
		action = "Denied"
		listName = "denylist"
	}
	return fmt.Sprintf("%s network rule saved in execpolicy (%s): %s", action, listName, fragment.host)
}

func (r *RuntimeRouter) networkApprovalPostToolInputItems(threadID string, turnID string, base turn.ToolPostExecutionInputItems, appendSessionItems func([]session.Item)) turn.ToolPostExecutionInputItems {
	if r == nil || r.networkApproval == nil {
		return base
	}
	return func(ctx context.Context, invocation *tool.Invocation, output *tool.Output) []any {
		if outcome := r.networkApproval.finishActiveCall(threadID, turnID, invocation); outcome != "" && output != nil {
			output.Success = false
			output.Body = outcome
			output.Error = outcome
		}
		items := []any{}
		if base != nil {
			items = append(items, base(ctx, invocation, output)...)
		}
		fragments := r.networkApproval.takeNetworkRulesSaved(threadID, turnID)
		if len(fragments) == 0 {
			return items
		}
		createdAt := time.Now().UTC()
		if output != nil && !output.CompletedAt.IsZero() {
			createdAt = output.CompletedAt.UTC()
		}
		sessionItems := make([]session.Item, 0, len(fragments))
		for index, fragment := range fragments {
			text := networkRuleSavedText(fragment)
			items = append(items, modelInputTextMessage("developer", text))
			sessionItems = append(sessionItems, session.Item{
				ID:        fmt.Sprintf("network-rule-saved-%s-%d", safeIdentifier(turnID), index+1),
				Type:      "message",
				Role:      "developer",
				Text:      text,
				CreatedAt: createdAt,
				Metadata:  appTurnMetadata(turnID, map[string]any{"kind": "network_rule_saved"}),
			})
		}
		if appendSessionItems != nil {
			appendSessionItems(sessionItems)
		}
		return items
	}
}

func (r *RuntimeRouter) activeTurnForNetworkApproval(environmentID string) *networkApprovalTurn {
	return r.activeTurnForNetworkApprovalThread("", environmentID)
}

func (r *RuntimeRouter) activeTurnForNetworkApprovalThread(threadID string, environmentID string) *networkApprovalTurn {
	if r == nil {
		return nil
	}
	environmentID = strings.TrimSpace(environmentID)
	var selected *activeRuntimeTurn
	for _, active := range r.threads.ActiveTurns() {
		if active == nil || strings.TrimSpace(active.TurnID) == "" {
			continue
		}
		if threadID != "" && active.ThreadID != threadID {
			continue
		}
		if environmentID != "" && environmentID != "local" && !turnSelectsEnvironment(active.Params, environmentID) {
			continue
		}
		if selected != nil {
			return nil
		}
		selected = active
	}
	if selected == nil {
		return nil
	}
	return &networkApprovalTurn{
		threadID:     selected.ThreadID,
		turnID:       selected.TurnID,
		connectionID: selected.ConnectionID,
		params:       cloneTurnStartParams(selected.Params),
		runConfig:    selected.RunConfig,
	}
}

func turnSelectsEnvironment(params *turn.TurnStartParams, environmentID string) bool {
	if params == nil {
		return false
	}
	for _, selected := range params.Environments {
		if strings.TrimSpace(firstNonEmpty(networkSelectionString(selected, "environmentId"), networkSelectionString(selected, "environment_id"), networkSelectionString(selected, "id"))) == environmentID {
			return true
		}
	}
	return false
}

func networkSelectionString(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}

func (r *RuntimeRouter) networkApprovalAllowedForTurn(active *networkApprovalTurn) bool {
	if r == nil || active == nil {
		return false
	}
	cfg, err := r.effectiveConfigForTurn(active.params)
	if err != nil {
		return false
	}
	if turnApprovalPolicyForTurn(cfg, active.params) == sandbox.ApprovalNever {
		return false
	}
	profile, err := turnSandboxPermissionProfile(cfg, firstNonEmpty(turnCWD(active.params), r.services.DefaultCWD), active.params)
	return err == nil && profile != nil && profile.Profile != nil && !profile.Profile.Disabled
}

func networkApprovalProtocol(protocol network.ProxyProtocol) NetworkApprovalProtocol {
	switch protocol {
	case network.ProxyProtocolHTTPSConnect:
		return NetworkApprovalHTTPS
	case network.ProxyProtocolSocks5TCP:
		return NetworkApprovalSocks5TCP
	case network.ProxyProtocolSocks5UDP:
		return NetworkApprovalSocks5UDP
	default:
		return NetworkApprovalHTTP
	}
}

func networkApprovalProtocolKey(protocol NetworkApprovalProtocol) string {
	switch protocol {
	case NetworkApprovalHTTPS:
		return "https"
	case NetworkApprovalSocks5TCP:
		return "socks5-tcp"
	case NetworkApprovalSocks5UDP:
		return "socks5-udp"
	default:
		return "http"
	}
}

func networkApprovalTarget(protocol network.ProxyProtocol, host string, port uint16) string {
	return fmt.Sprintf("%s://%s:%d", networkApprovalProtocolKey(networkApprovalProtocol(protocol)), host, port)
}

func networkPolicyAmendmentFromDecision(value any) (*NetworkPolicyAmendment, bool) {
	object, ok := commandExecutionApprovalDecisionObject(value)
	if !ok {
		return nil, false
	}
	payload, ok := commandExecutionApprovalDecisionObject(object[string(CommandExecutionApprovalApplyNetworkPolicyAmendment)])
	if !ok {
		return nil, false
	}
	amendment, ok := commandExecutionApprovalDecisionObject(firstNonNil(payload["network_policy_amendment"], payload["networkPolicyAmendment"]))
	if !ok {
		return nil, false
	}
	host, _ := amendment["host"].(string)
	action, _ := amendment["action"].(string)
	parsed := &NetworkPolicyAmendment{Host: host, Action: NetworkPolicyRuleAction(action)}
	if network.NormalizeProxyHost(parsed.Host) == "" || (parsed.Action != NetworkPolicyRuleAllow && parsed.Action != NetworkPolicyRuleDeny) {
		return nil, false
	}
	return parsed, true
}

func (r *RuntimeRouter) persistNetworkPolicyAmendment(amendment *NetworkPolicyAmendment, protocol NetworkApprovalProtocol) error {
	if r == nil || amendment == nil || r.services.Config == nil {
		return fmt.Errorf("managed network config is unavailable")
	}
	host := network.NormalizeProxyHost(amendment.Host)
	if host == "" {
		return fmt.Errorf("network policy amendment host is empty")
	}
	decision := execpolicy.DecisionAllow
	verb := "Allow"
	if amendment.Action == NetworkPolicyRuleDeny {
		decision = execpolicy.DecisionForbidden
		verb = "Deny"
	}
	policyProtocol := string(protocol)
	justificationProtocol := policyProtocol
	if protocol == NetworkApprovalHTTPS {
		policyProtocol = "https"
		justificationProtocol = "https_connect"
	}
	return execpolicy.AppendNetworkRule(
		execpolicy.DefaultPolicyPath(r.services.Config.CodexHome()),
		host,
		policyProtocol,
		decision,
		fmt.Sprintf("%s %s access to %s", verb, justificationProtocol, host),
	)
}

func applyExecPolicyNetworkRules(proxyConfig *network.ProxyConfig, codexHome string) error {
	if proxyConfig == nil {
		return nil
	}
	rules, err := loadExecPolicyNetworkRules(codexHome)
	if err != nil {
		return err
	}
	for _, rule := range rules {
		proxyConfig.Network.UpsertDomainPermission(rule.Host, rule.Permission, network.NormalizeProxyHost)
	}
	return nil
}

func loadExecPolicyNetworkRules(codexHome string) ([]network.ProxyNetworkRule, error) {
	if strings.TrimSpace(codexHome) == "" {
		return nil, nil
	}
	path := execpolicy.DefaultPolicyPath(codexHome)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	policy, err := execpolicy.LoadPolicies([]string{path})
	if err != nil {
		return nil, err
	}
	rules := make([]network.ProxyNetworkRule, 0, len(policy.NetworkRules))
	for _, rule := range policy.NetworkRules {
		switch rule.Decision {
		case execpolicy.DecisionAllow:
			rules = append(rules, network.ProxyNetworkRule{Host: rule.Host, Permission: network.ProxyDomainAllow})
		case execpolicy.DecisionForbidden:
			rules = append(rules, network.ProxyNetworkRule{Host: rule.Host, Permission: network.ProxyDomainDeny})
		}
	}
	return rules, nil
}

func (r *RuntimeRouter) networkProxyAuditMetadata(request network.ProxyPolicyRequest) network.ProxyAuditMetadata {
	return r.networkProxyAuditMetadataForThread("", request)
}

func (r *RuntimeRouter) networkProxyAuditMetadataForThread(threadID string, request network.ProxyPolicyRequest) network.ProxyAuditMetadata {
	metadata := network.ProxyAuditMetadata{AppVersion: appServerBuildVersion()}
	active := r.activeTurnForNetworkApprovalThread(strings.TrimSpace(threadID), request.EnvironmentID)
	if active != nil {
		metadata.ConversationID = active.threadID
		if active.runConfig != nil {
			metadata.Model = active.runConfig.Model
			metadata.Slug = active.runConfig.Model
			metadata.Originator = active.runConfig.Originator
		}
		if info := r.clientInfoForConnection(active.connectionID); info.Name != "" {
			metadata.TerminalType = info.Name
		}
	}
	if r != nil && r.services.Account != nil {
		snapshot := r.services.Account.AuthSnapshot()
		if snapshot != nil {
			metadata.AuthMode = snapshot.Mode()
			metadata.UserAccountID = auth.AccountIDFromAuthForRestrictions(snapshot)
			if account := auth.AccountFromAuth(snapshot); account != nil && account.Email != nil {
				metadata.UserEmail = *account.Email
			}
		}
	}
	return metadata
}

func (r *RuntimeRouter) clientInfoForConnection(connectionID string) ClientInfo {
	if r == nil {
		return ClientInfo{}
	}
	r.clientInfoMu.RLock()
	defer r.clientInfoMu.RUnlock()
	return r.clientInfo[normalizeConnectionID(connectionID)]
}

func appServerBuildVersion() string {
	return appServerVersion()
}

func emitNetworkProxyAuditEvent(event network.ProxyPolicyAuditEvent) {
	scope := "domain"
	if event.Reason == network.ProxyReasonMethodNotAllowed || event.Reason == network.ProxyReasonProxyDisabled || event.Reason == network.ProxyReasonUnixSocketUnsupported {
		scope = "non_domain"
	}
	slog.Info("codex.network_proxy.policy_decision",
		"event.name", "codex.network_proxy.policy_decision",
		"event.timestamp", time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		"conversation.id", event.Metadata.ConversationID,
		"app.version", event.Metadata.AppVersion,
		"auth_mode", event.Metadata.AuthMode,
		"originator", event.Metadata.Originator,
		"user.account_id", event.Metadata.UserAccountID,
		"user.email", event.Metadata.UserEmail,
		"terminal.type", event.Metadata.TerminalType,
		"model", event.Metadata.Model,
		"slug", event.Metadata.Slug,
		"network.policy.scope", scope,
		"network.policy.decision", event.Decision,
		"network.policy.source", event.Source,
		"network.policy.reason", event.Reason,
		"network.transport.protocol", event.Request.Protocol,
		"server.address", event.Request.Host,
		"server.port", event.Request.Port,
		"http.request.method", firstNonEmpty(event.Request.Method, "none"),
		"client.address", firstNonEmpty(event.Request.ClientAddr, "unknown"),
		"network.policy.override", event.PolicyOverride,
	)
}
