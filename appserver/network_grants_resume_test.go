package appserver

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/execpolicy"
	"codex_go/network"
	"codex_go/sandbox"
	"codex_go/turn"
)

// Rust #53424 (`fc4921f487`, "Add regression coverage for network grants on cold
// thread resume"):
// app-server/tests/suite/v2/thread_resume_network_tests.rs
// `cold_resume_restores_live_proxy_grants_and_persists_new_approvals`.
//
// Clause-by-clause mapping against that test:
//
//	[41-53]   skip_if_remote + requirements.toml `[experimental_network]`
//	          (enabled, http_port/socks_port = 0, allow_upstream_proxy = false,
//	          allow_local_binding = true) plus the `managed.example` allow
//	          domain => coldResumeNetworkRequirements().
//	[55-71]   rules/default.rules carries
//	          `network_rule(host="127.0.0.2", protocol="http", decision="allow")`.
//	          The Go fixture persists it through the production path
//	          (`RuntimeRouter.persistNetworkPolicyAmendment`) and asserts the
//	          rendered line. The host is 127.0.0.1 because that is the loopback
//	          origin httptest serves on Windows; the rule is host-based.
//	[73-118]  first process: a read-only thread persists a turn, then shuts down
//	          => router #1 persists the amendment and is closed, so the grants
//	          that lived in its process are gone.
//	[119-141] second process: the process default now disagrees with the saved
//	          unnamed policy (`default_permissions = ":danger-full-access"`), and
//	          the thread is resumed => router #2 shares the codex home, and the
//	          resumed thread's saved policy travels as the turn params, which is
//	          what the turn runtime resolves before it builds the thread's proxy
//	          (`turn_runtime.go:14079` -> `managedNetworkForTurn`).
//	[141-166] the resumed thread reports read-only + on-request => asserted from
//	          `effectiveConfigForTurn`, `turnApprovalPolicyForTurn` and
//	          `turnSandboxPermissionProfile`.
//	[167-256] `remembered-domain` 0 approvals, `new-domain` 1 approval,
//	          `reuse-new-domain` 0 approvals through the live proxy => asserted
//	          against the live per-thread proxy: 200 through the proxy for the
//	          restored host with no prompt, exactly one prompt for the new host,
//	          and the persisted rule is picked up without another prompt. The one
//	          prompt's host and protocol are asserted from the request's
//	          `NetworkApprovalContext` ([222-228]).
//	[258-276] the rules file records the new host => asserted verbatim.
//
// NOT VERIFIED on this node: the sandbox half of the loopback probe
// (`managed_network_sandbox_probe`: "proxied request succeeded; direct access
// blocked"). The upstream test is `#[cfg(target_os = "linux")]`
// (thread_resume.rs:160) and the probe runs a host-local executable under
// unified exec inside a sandbox that isolates direct host access. Reason: this
// node is Windows and has no Linux sandbox, so the "direct access remains
// blocked" assertion cannot be produced here. The proxy half of the same
// behavior is covered by the request that succeeds through the proxy because
// the persisted grant was restored.
func TestRuntimeRouterColdResumeRestoresNetworkGrantsLikeRust(t *testing.T) {
	home := t.TempDir() // survives the process, like the Rust test's codex_home
	requirements := coldResumeNetworkRequirements()

	// --- first process: the read-only thread persists an allow amendment -------
	if err := os.WriteFile(config.ConfigPath(home), []byte("approval_policy = \"on-request\"\nsandbox_mode = \"read-only\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	first := coldResumeNetworkRouter(t, home, requirements)
	if err := first.persistNetworkPolicyAmendment(
		&NetworkPolicyAmendment{Host: "127.0.0.1", Action: NetworkPolicyRuleAllow},
		NetworkApprovalHTTP,
	); err != nil {
		t.Fatalf("persistNetworkPolicyAmendment() error = %v", err)
	}
	rulesPath := execpolicy.DefaultPolicyPath(home)
	if rules := coldResumeReadFile(t, rulesPath); !strings.Contains(rules, `network_rule(host="127.0.0.1", protocol="http", decision="allow"`) {
		t.Fatalf("rules = %q, want the persisted allow rule", rules)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// --- second process: cold resume, process default disagrees ----------------
	if err := os.WriteFile(config.ConfigPath(home), []byte("default_permissions = \":danger-full-access\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resumed := coldResumeNetworkRouter(t, home, requirements)
	threadID := "thread-cold-resume"
	turnID := "turn-cold-resume"
	// The saved unnamed policy of the resumed thread.
	resumedParams := &turn.TurnStartParams{
		ThreadID:       threadID,
		CWD:            home,
		ApprovalPolicy: string(sandbox.ApprovalOnRequest),
		SandboxPolicy:  string(sandbox.SandboxReadOnly),
	}
	cfg, err := resumed.effectiveConfigForTurn(resumedParams)
	if err != nil {
		t.Fatalf("effectiveConfigForTurn() error = %v", err)
	}
	if policy := turnApprovalPolicyForTurn(cfg, resumedParams); policy != sandbox.ApprovalOnRequest {
		t.Fatalf("resumed approval policy = %q, want on-request", policy)
	}
	profile, err := turnSandboxPermissionProfile(cfg, home, resumedParams)
	if err != nil || profile == nil || profile.Profile == nil || profile.Profile.Disabled {
		t.Fatalf("resumed permission profile = %#v, error = %v", profile, err)
	}
	if profile.Profile.SandboxPolicy == nil || profile.Profile.SandboxPolicy.Kind != sandbox.SandboxReadOnly {
		t.Fatalf("resumed sandbox policy = %#v, want read-only", profile.Profile.SandboxPolicy)
	}

	values := coldResumeNetworkValues()
	proxyConfig, shouldStart, err := resumed.buildManagedNetworkProxyConfigForCWD(values, home)
	if err != nil || !shouldStart {
		t.Fatalf("buildManagedNetworkProxyConfigForCWD() error = %v, shouldStart = %v", err, shouldStart)
	}
	if !coldResumeContains(proxyConfig.Network.AllowedDomains(), "127.0.0.1") {
		t.Fatalf("proxy allow domains = %v, want the persisted 127.0.0.1 grant", proxyConfig.Network.AllowedDomains())
	}
	// A host with no persisted rule is in neither list, so it still needs the
	// approval flow.
	if coldResumeContains(proxyConfig.Network.AllowedDomains(), "new-host.example") || coldResumeContains(proxyConfig.Network.DeniedDomains(), "new-host.example") {
		t.Fatalf("proxy domains allow=%v deny=%v, want new-host.example unlisted", proxyConfig.Network.AllowedDomains(), proxyConfig.Network.DeniedDomains())
	}

	// The live proxy the thread's turn runs behind.
	prepared, err := resumed.managedNetworkForTurn(threadID, home, &config.Config{Values: values})
	if err != nil {
		t.Fatalf("managedNetworkForTurn() error = %v", err)
	}
	if prepared == nil {
		t.Fatal("managedNetworkForTurn() returned no proxy")
	}
	live := prepared.RemoteConfigSnapshot()
	if !coldResumeContains(live.Network.AllowedDomains(), "127.0.0.1") {
		t.Fatalf("live proxy allow domains = %v, want the restored 127.0.0.1 grant", live.Network.AllowedDomains())
	}

	var (
		approvalMu        sync.Mutex
		approvalHosts     []string
		approvalProtocols []NetworkApprovalProtocol
	)
	resumed.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		params, ok := request.Params.(*CommandExecutionRequestApprovalParams)
		if !ok {
			t.Errorf("approval params = %T, want *CommandExecutionRequestApprovalParams", request.Params)
			_, _ = resumed.requireServerRequests().Resolve(&Response{ID: request.ID, Result: map[string]any{"decision": string(CommandExecutionApprovalDecline)}})
			return
		}
		contextValue, _ := params.NetworkApprovalContext.(*NetworkApprovalContext)
		host := ""
		var protocol NetworkApprovalProtocol
		if contextValue != nil {
			host = contextValue.Host
			protocol = contextValue.Protocol
		}
		approvalMu.Lock()
		approvalHosts = append(approvalHosts, host)
		approvalProtocols = append(approvalProtocols, protocol)
		approvalMu.Unlock()
		decision := map[string]any{
			string(CommandExecutionApprovalApplyNetworkPolicyAmendment): map[string]any{
				"network_policy_amendment": map[string]any{"host": host, "action": string(NetworkPolicyRuleAllow)},
			},
		}
		_, _ = resumed.requireServerRequests().Resolve(&Response{ID: request.ID, Result: map[string]any{"decision": decision}})
	}))
	if err := resumed.registerActiveRuntimeTurn(threadID, turnID, func() {}, 1, resumedParams); err != nil {
		t.Fatal(err)
	}
	resumed.updateActiveRuntimeTurnAnalytics(threadID, turnID, "connection-cold-resume", &appTurnRunConfig{Model: "gpt-test", Originator: "codex-test", ApprovalPolicy: string(sandbox.ApprovalOnRequest)})
	t.Cleanup(func() { resumed.clearActiveRuntimeTurn(threadID, turnID) })

	proxyURL, err := url.Parse(prepared.EnvSnapshot()["HTTP_PROXY"])
	if err != nil || proxyURL.Host == "" {
		t.Fatalf("managed proxy URL = %q, error = %v", prepared.EnvSnapshot()["HTTP_PROXY"], err)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 10 * time.Second}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("sandbox proxy response"))
	}))
	defer origin.Close()

	// remembered domain: the restored grant lets the live proxy serve it without
	// an approval prompt.
	response, err := client.Get(origin.URL)
	if err != nil {
		t.Fatalf("proxied request through the restored grant error = %v", err)
	}
	body := make([]byte, 64)
	read, _ := response.Body.Read(body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body[:read]), "sandbox proxy response") {
		t.Fatalf("proxied status = %d body = %q", response.StatusCode, string(body[:read]))
	}
	approvalMu.Lock()
	rememberedPrompts := len(approvalHosts)
	approvalMu.Unlock()
	if rememberedPrompts != 0 {
		t.Fatalf("approvals for the restored host = %d, want 0", rememberedPrompts)
	}

	// new domain: the live proxy asks the user exactly once.
	if response, requestErr := client.Get("http://new-host.example/"); requestErr == nil && response != nil {
		response.Body.Close()
	}
	approvalMu.Lock()
	hosts := append([]string(nil), approvalHosts...)
	protocols := append([]NetworkApprovalProtocol(nil), approvalProtocols...)
	approvalMu.Unlock()
	if len(hosts) != 1 || hosts[0] != "new-host.example" {
		t.Fatalf("approval hosts = %v, want exactly one prompt for new-host.example", hosts)
	}
	if protocols[0] != NetworkApprovalHTTP {
		t.Fatalf("approval protocol = %q, want http", protocols[0])
	}
	// The approved amendment is persisted, mirroring the Rust tail assertion.
	if rules := coldResumeReadFile(t, rulesPath); !strings.Contains(rules, `network_rule(host="new-host.example", protocol="http", decision="allow"`) {
		t.Fatalf("rules = %q, want the approved new-host rule", rules)
	}
	// reuse-new-domain: the amendment is live for this thread, so a repeat request
	// to the same host is served without another prompt.
	if response, requestErr := client.Get("http://new-host.example/"); requestErr == nil && response != nil {
		response.Body.Close()
	}
	approvalMu.Lock()
	reusedPrompts := len(approvalHosts)
	approvalMu.Unlock()
	if reusedPrompts != 1 {
		t.Fatalf("approvals after the amendment = %d, want 1 (no further prompt for new-host.example)", reusedPrompts)
	}
	// A later cold resume reads the persisted rule instead of asking again.
	afterResume, shouldStart, err := resumed.buildManagedNetworkProxyConfigForCWD(values, home)
	if err != nil || !shouldStart {
		t.Fatalf("rebuild error = %v, shouldStart = %v", err, shouldStart)
	}
	if !coldResumeContains(afterResume.Network.AllowedDomains(), "new-host.example") {
		t.Fatalf("allow domains after resume = %v, want the persisted new-host.example rule", afterResume.Network.AllowedDomains())
	}
}

// coldResumeNetworkRequirements mirrors the Rust fixture's requirements.toml
// `[experimental_network]` table (enabled, http/socks port 0, no upstream
// proxy, local binding allowed) plus its `managed.example` allow domain.
func coldResumeNetworkRequirements() *config.NetworkRequirements {
	enabled := true
	allowUpstream := false
	allowLocal := true
	httpPort := uint16(0)
	socksPort := uint16(0)
	return &config.NetworkRequirements{
		Enabled:            &enabled,
		HTTPPort:           &httpPort,
		SOCKSPort:          &socksPort,
		AllowUpstreamProxy: &allowUpstream,
		AllowLocalBinding:  &allowLocal,
		Domains:            map[string]config.NetworkPermission{"managed.example": config.NetworkAllow},
	}
}

// coldResumeNetworkValues is the managed-network table of the resumed thread's
// config, matching the per-thread proxy shape the other managed-network tests
// use.
func coldResumeNetworkValues() map[string]any {
	return map[string]any{
		"approval_policy": string(sandbox.ApprovalOnRequest),
		"sandbox_mode":    string(sandbox.SandboxReadOnly),
		"features": map[string]any{"network_proxy": map[string]any{
			"enabled":             true,
			"proxy_url":           "http://127.0.0.1:0",
			"socks_url":           "http://127.0.0.1:0",
			"enable_socks5":       false,
			"allow_local_binding": true,
			"mode":                "full",
		}},
	}
}

// coldResumeNetworkRouter builds the router a fresh process would run with: the
// same codex home (config.toml plus the persisted network rules) and the
// managed-network requirements the process started with.
func coldResumeNetworkRouter(t *testing.T, home string, requirements *config.NetworkRequirements) *RuntimeRouter {
	t.Helper()
	router := NewRuntimeRouter(RuntimeServices{
		Config:                     config.NewConfigService(home),
		DefaultCWD:                 home,
		ManagedNetworkRequirements: requirements,
	})
	t.Cleanup(func() { _ = router.Close() })
	return router
}

func coldResumeReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	return string(data)
}

func coldResumeContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// TestRuntimeRouterColdResumeEmptyHomeGrantsNothingLikeRust is the negative
// control from lane B's G-3 case (update/r90b_b4_2026_10_16.md): a cold resume
// against a home without persisted network rules must grant nothing. The rest of
// B's duplicate file (same name as the case above) was dropped in favour of the
// richer G-3 regression already on main.
func TestRuntimeRouterColdResumeEmptyHomeGrantsNothingLikeRust(t *testing.T) {
	empty := &network.ProxyConfig{Network: network.DefaultProxySettings()}
	if err := applyExecPolicyNetworkRules(empty, filepath.Join(t.TempDir(), "codex-home")); err != nil {
		t.Fatalf("applyExecPolicyNetworkRules(empty home) error = %v", err)
	}
	if len(empty.Network.AllowedDomains()) != 0 || len(empty.Network.DeniedDomains()) != 0 {
		t.Fatalf("empty home granted allow=%#v deny=%#v", empty.Network.AllowedDomains(), empty.Network.DeniedDomains())
	}
}
