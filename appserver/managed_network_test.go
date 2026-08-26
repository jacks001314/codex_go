package appserver

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/execpolicy"
	"codex_go/model"
	"codex_go/network"
	"codex_go/session"
	"codex_go/tool"
	"codex_go/turn"
)

func TestDefaultRuntimeRouterStartsManagedNetworkFromRequirementsWithoutUserConfigLikeRust(t *testing.T) {
	home := t.TempDir()
	enabled := true
	httpPort := uint16(0)
	socksPort := uint16(0)
	router := NewDefaultRuntimeRouterWithOptions(session.NewStore(home), home, &RuntimeRouterOptions{
		Requirements: &config.ConfigRequirements{Network: &config.NetworkRequirements{
			Enabled:   &enabled,
			HTTPPort:  &httpPort,
			SOCKSPort: &socksPort,
		}},
	})
	defer router.Close()
	if router.services.ManagedNetwork == nil || router.services.ManagedNetwork.Env[network.ProxyActiveEnvKey] != "1" {
		t.Fatalf("managed network = %#v", router.services.ManagedNetwork)
	}
	proxyURL, err := url.Parse(router.services.ManagedNetwork.Env["HTTP_PROXY"])
	if err != nil || proxyURL.Port() == "" || proxyURL.Port() == "0" {
		t.Fatalf("HTTP proxy URL = %q, err = %v", router.services.ManagedNetwork.Env["HTTP_PROXY"], err)
	}
}

func TestManagedNetworkBlockedObserverRecordsWithoutRequirementsLikeRust(t *testing.T) {
	previous := slog.Default()
	var logs strings.Builder
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(previous)

	router := &RuntimeRouter{}
	observer := router.managedNetworkBlockedObserver("", false)
	port := uint16(443)
	observer.OnBlockedRequest(context.Background(), network.ProxyBlockedRequest{
		Host: "blocked.example", Reason: "not_allowed", Protocol: "https", Port: &port, Timestamp: 42,
	})
	if !strings.Contains(logs.String(), "resource=network backend=managed_network_proxy protocol=https host=blocked.example") {
		t.Fatalf("violation log = %q", logs.String())
	}
}

func TestManagedOnlyRequirementsIgnoreUserAndExecpolicyExpansionWithoutApprovalLikeRust(t *testing.T) {
	home := t.TempDir()
	configBody := "approval_policy = \"on-request\"\nsandbox_mode = \"workspace-write\"\n" +
		"[network_proxy]\n" +
		"enabled = true\n" +
		"proxy_url = \"http://127.0.0.1:0\"\n" +
		"enable_socks5 = false\n" +
		"allowed_domains = [\"user.example.com\"]\n" +
		"mode = \"full\"\n"
	if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := execpolicy.AppendNetworkRule(execpolicy.DefaultPolicyPath(home), "execpolicy.example.com", "http", execpolicy.DecisionAllow, "test managed constraint fallback"); err != nil {
		t.Fatal(err)
	}
	enabled := true
	hardDeny := true
	allowLocal := true
	router := NewDefaultRuntimeRouterWithOptions(session.NewStore(home), home, &RuntimeRouterOptions{
		Requirements: &config.ConfigRequirements{Network: &config.NetworkRequirements{
			Enabled:                   &enabled,
			HTTPPort:                  uint16Ptr(0),
			SOCKSPort:                 uint16Ptr(0),
			ManagedAllowedDomainsOnly: &hardDeny,
			Domains:                   map[string]config.NetworkPermission{"127.0.0.1": config.NetworkAllow},
			AllowLocalBinding:         &allowLocal,
		}},
	})
	defer router.Close()
	if router.services.ManagedNetwork == nil {
		t.Fatal("managed network did not start")
	}
	var approvals atomic.Int32
	router.SetServerRequestSink(ServerRequestSinkFunc(func(*ServerRequest) { approvals.Add(1) }))
	proxyURL, err := url.Parse(router.services.ManagedNetwork.Env["HTTP_PROXY"])
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 2 * time.Second}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }))
	defer origin.Close()
	response, err := client.Get(origin.URL)
	if err != nil {
		t.Fatalf("managed allow request error = %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("managed allow status = %d", response.StatusCode)
	}
	for _, target := range []string{"http://user.example.com/", "http://execpolicy.example.com/"} {
		request, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
		blocked, requestErr := client.Do(request)
		if requestErr != nil {
			t.Fatalf("blocked request %s error = %v", target, requestErr)
		}
		blocked.Body.Close()
		if blocked.StatusCode != http.StatusForbidden {
			t.Fatalf("blocked request %s status = %d", target, blocked.StatusCode)
		}
	}
	if approvals.Load() != 0 {
		t.Fatalf("approval requests = %d, want 0", approvals.Load())
	}
}

func TestConfiguredManagedNetworkWithoutRequirementsDoesNotEnableApprovalFlowLikeRust(t *testing.T) {
	home := t.TempDir()
	configBody := "approval_policy = \"on-request\"\nsandbox_mode = \"workspace-write\"\n" +
		"[network_proxy]\n" +
		"enabled = true\n" +
		"proxy_url = \"http://127.0.0.1:0\"\n" +
		"enable_socks5 = false\n" +
		"mode = \"full\"\n"
	if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
		t.Fatal(err)
	}
	router := NewDefaultRuntimeRouter(session.NewStore(home), home)
	defer router.Close()
	var approvals atomic.Int32
	router.SetServerRequestSink(ServerRequestSinkFunc(func(*ServerRequest) { approvals.Add(1) }))
	proxyURL, err := url.Parse(router.services.ManagedNetwork.Env["HTTP_PROXY"])
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 2 * time.Second}
	response, err := client.Get("http://not-allowed.example/")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden || approvals.Load() != 0 {
		t.Fatalf("status = %d, approvals = %d", response.StatusCode, approvals.Load())
	}
}

func TestManagedNetworkBlockedObserverRejectsSingleToolCallLikeRust(t *testing.T) {
	home := t.TempDir()
	configBody := "sandbox_mode = \"workspace-write\"\n" +
		"[network_proxy]\n" +
		"enabled = true\n" +
		"proxy_url = \"http://127.0.0.1:0\"\n" +
		"socks_url = \"http://127.0.0.1:0\"\n" +
		"enable_socks5 = false\n" +
		"mode = \"full\"\n"
	if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
		t.Fatal(err)
	}
	enabled := true
	hardDeny := true
	router := NewDefaultRuntimeRouterWithOptions(session.NewStore(home), home, &RuntimeRouterOptions{
		Requirements: &config.ConfigRequirements{Network: &config.NetworkRequirements{
			Enabled:                   &enabled,
			HTTPPort:                  uint16Ptr(0),
			SOCKSPort:                 uint16Ptr(0),
			ManagedAllowedDomainsOnly: &hardDeny,
		}},
	})
	defer router.Close()
	proxyURL, err := url.Parse(router.services.ManagedNetwork.Env["HTTP_PROXY"])
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 2 * time.Second}
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("exec_command")}, func(ctx context.Context, _ *tool.Invocation) (*tool.Output, error) {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://blocked.example/", nil)
		response, requestErr := client.Do(request)
		if requestErr != nil {
			return nil, requestErr
		}
		defer response.Body.Close()
		return &tool.Output{Success: true, Body: response.Status}, nil
	})); err != nil {
		t.Fatal(err)
	}
	dispatcher := turn.NewToolDispatcher(&turn.ToolDispatcherOptions{
		Router:             tool.NewRouter(registry),
		ThreadID:           "thread-network-tool",
		TurnID:             "turn-network-tool",
		OnToolStarted:      router.runtimeToolStartedNotifier("thread-network-tool", "turn-network-tool", home, false),
		PostToolInputItems: router.networkApprovalPostToolInputItems("thread-network-tool", "turn-network-tool", nil, nil),
	})
	results, err := dispatcher.ExecuteToolItems(context.Background(), []model.AgentItem{{Type: "function_call", Name: "exec_command", CallID: "call-network-tool", Arguments: `{}`}})
	if err != nil {
		t.Fatalf("ExecuteToolItems() error = %v", err)
	}
	if len(results) != 1 || results[0].Output.Success || !strings.Contains(results[0].Output.Body, `Network access to "blocked.example" was blocked`) {
		t.Fatalf("tool results = %#v", results)
	}
	blocked := router.services.ManagedNetwork.BlockedSnapshot()
	if len(blocked) != 1 || blocked[0].Host != "blocked.example" || blocked[0].Decision != string(network.ProxyPolicyDecisionDeny) {
		t.Fatalf("blocked requests = %#v", blocked)
	}
}

func TestManagedNetworkPoliciesAreIsolatedPerThreadLikeRust(t *testing.T) {
	home := t.TempDir()
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home), DefaultCWD: home})
	defer router.Close()
	baseNetwork := func(allowed []any) map[string]any {
		return map[string]any{
			"enabled":             true,
			"proxy_url":           "http://127.0.0.1:0",
			"socks_url":           "http://127.0.0.1:0",
			"enable_socks5":       false,
			"allow_local_binding": true,
			"allowed_domains":     allowed,
			"mode":                "full",
		}
	}
	allowedConfig := &config.Config{Values: map[string]any{
		"sandbox_mode":  "workspace-write",
		"network_proxy": baseNetwork([]any{"127.0.0.1"}),
	}}
	blockedConfig := &config.Config{Values: map[string]any{
		"sandbox_mode":  "workspace-write",
		"network_proxy": baseNetwork([]any{"example.com"}),
	}}
	allowedNetwork, err := router.managedNetworkForTurn("thread-allowed", home, allowedConfig)
	if err != nil {
		t.Fatal(err)
	}
	blockedNetwork, err := router.managedNetworkForTurn("thread-blocked", home, blockedConfig)
	if err != nil {
		t.Fatal(err)
	}
	if allowedNetwork == nil || blockedNetwork == nil || allowedNetwork == blockedNetwork || allowedNetwork.EnvSnapshot()["HTTP_PROXY"] == blockedNetwork.EnvSnapshot()["HTTP_PROXY"] {
		t.Fatalf("thread networks = %#v / %#v", allowedNetwork, blockedNetwork)
	}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }))
	defer origin.Close()
	statusThrough := func(prepared *network.PreparedProxyManagedNetwork) int {
		proxyURL, _ := url.Parse(prepared.EnvSnapshot()["HTTP_PROXY"])
		client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 2 * time.Second}
		response, requestErr := client.Get(origin.URL)
		if requestErr != nil {
			return 0
		}
		defer response.Body.Close()
		return response.StatusCode
	}
	if got := statusThrough(allowedNetwork); got != http.StatusOK {
		t.Fatalf("allowed thread status = %d", got)
	}
	if got := statusThrough(blockedNetwork); got != http.StatusForbidden {
		t.Fatalf("blocked thread status = %d", got)
	}
}

func TestThreadUnloadClosesManagedNetworkAndNextTurnRestartsIt(t *testing.T) {
	home := t.TempDir()
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home), DefaultCWD: home})
	defer router.Close()
	cfg := &config.Config{Values: map[string]any{
		"sandbox_mode": "workspace-write",
		"network_proxy": map[string]any{
			"enabled":         true,
			"proxy_url":       "http://127.0.0.1:0",
			"enable_socks5":   false,
			"allowed_domains": []any{"127.0.0.1"},
			"mode":            "full",
		},
	}}
	first, err := router.managedNetworkForTurn("thread-network-unload", home, cfg)
	if err != nil {
		t.Fatal(err)
	}
	firstURL, err := url.Parse(first.EnvSnapshot()["HTTP_PROXY"])
	if err != nil {
		t.Fatal(err)
	}
	router.markThreadUnloaded("thread-network-unload")
	if _, ok := router.managedNetworks["thread-network-unload"]; ok {
		t.Fatal("thread managed network remained registered after unload")
	}
	if connection, err := net.DialTimeout("tcp", firstURL.Host, 100*time.Millisecond); err == nil {
		_ = connection.Close()
		t.Fatal("thread managed network listener remained open after unload")
	}

	second, err := router.managedNetworkForTurn("thread-network-unload", home, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if second == nil || second == first {
		t.Fatalf("thread network was not restarted: first=%#v second=%#v", first, second)
	}
	secondURL, err := url.Parse(second.EnvSnapshot()["HTTP_PROXY"])
	if err != nil {
		t.Fatal(err)
	}
	connection, err := net.DialTimeout("tcp", secondURL.Host, time.Second)
	if err != nil {
		t.Fatalf("restarted thread managed network is not listening: %v", err)
	}
	_ = connection.Close()
}

func uint16Ptr(value uint16) *uint16 {
	return &value
}

func TestDefaultRuntimeRouterStartsAndClosesManagedNetworkLikeRust(t *testing.T) {
	home := t.TempDir()
	configBody := "[network_proxy]\n" +
		"enabled = true\n" +
		"proxy_url = \"http://127.0.0.1:0\"\n" +
		"enable_socks5 = false\n" +
		"mode = \"full\"\n"
	if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
		t.Fatal(err)
	}
	router := NewDefaultRuntimeRouter(session.NewStore(home), home)
	managed := router.services.ManagedNetwork
	if managed == nil || managed.Env[network.ProxyActiveEnvKey] != "1" || len(managed.SandboxContext.LoopbackPorts) != 1 {
		t.Fatalf("managed network = %#v", managed)
	}
	proxyURL, err := url.Parse(managed.Env["HTTP_PROXY"])
	if err != nil || proxyURL.Port() == "" || proxyURL.Port() == "0" {
		t.Fatalf("HTTP proxy URL = %q, err = %v", managed.Env["HTTP_PROXY"], err)
	}
	if conn, err := net.DialTimeout("tcp", proxyURL.Host, time.Second); err != nil {
		t.Fatalf("managed HTTP proxy is not listening: %v", err)
	} else {
		_ = conn.Close()
	}
	if err := router.Close(); err != nil {
		t.Fatalf("router.Close() error = %v", err)
	}
	if conn, err := net.DialTimeout("tcp", proxyURL.Host, 100*time.Millisecond); err == nil {
		_ = conn.Close()
		t.Fatal("managed HTTP proxy remained open after router close")
	}
}

func TestDefaultRuntimeRouterStartsManagedNetworkFromPermissionProfileLikeRust(t *testing.T) {
	home := t.TempDir()
	configBody := "default_permissions = \"dev\"\n" +
		"[permissions.dev.network]\n" +
		"enabled = true\n" +
		"proxy_url = \"http://127.0.0.1:0\"\n" +
		"enable_socks5 = false\n" +
		"mode = \"full\"\n"
	if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
		t.Fatal(err)
	}
	router := NewDefaultRuntimeRouter(session.NewStore(home), home)
	defer router.Close()
	if router.services.ManagedNetwork == nil || router.services.ManagedNetwork.Env[network.ProxyActiveEnvKey] != "1" {
		t.Fatalf("managed network = %#v", router.services.ManagedNetwork)
	}
}

func TestDefaultRuntimeRouterHotReloadsManagedNetworkConfigAndExecpolicyLikeRust(t *testing.T) {
	home := t.TempDir()
	configBody := "[network_proxy]\n" +
		"enabled = true\n" +
		"proxy_url = \"http://127.0.0.1:0\"\n" +
		"enable_socks5 = false\n" +
		"allow_local_binding = true\n" +
		"allowed_domains = [\"127.0.0.1\"]\n" +
		"mode = \"full\"\n"
	if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
		t.Fatal(err)
	}
	router := NewDefaultRuntimeRouter(session.NewStore(home), home)
	defer router.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(w, request.Method)
	}))
	defer origin.Close()
	proxyURL, err := url.Parse(router.services.ManagedNetwork.Env["HTTP_PROXY"])
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 2 * time.Second}
	postStatus := func() int {
		request, _ := http.NewRequest(http.MethodPost, origin.URL, strings.NewReader("x"))
		response, requestErr := client.Do(request)
		if requestErr != nil {
			return 0
		}
		defer response.Body.Close()
		return response.StatusCode
	}
	if status := postStatus(); status != http.StatusOK {
		t.Fatalf("initial POST status = %d", status)
	}
	limited := strings.Replace(configBody, "mode = \"full\"", "mode = \"limited\"", 1)
	if err := os.WriteFile(config.ConfigPath(home), []byte(limited), 0o600); err != nil {
		t.Fatal(err)
	}
	waitForManagedNetworkStatus(t, postStatus, http.StatusForbidden)

	if err := execpolicy.AppendNetworkRule(execpolicy.DefaultPolicyPath(home), "127.0.0.1", "http", execpolicy.DecisionForbidden, "Deny http access to 127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	getStatus := func() int {
		response, requestErr := client.Get(origin.URL)
		if requestErr != nil {
			return 0
		}
		defer response.Body.Close()
		return response.StatusCode
	}
	waitForManagedNetworkStatus(t, getStatus, http.StatusForbidden)
}

func TestThreadManagedNetworkHotReloadsProjectConfigLayerLikeRust(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	if err := os.Mkdir(filepath.Join(project, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	dotCodex := filepath.Join(project, ".gcode")
	if err := os.Mkdir(dotCodex, 0o700); err != nil {
		t.Fatal(err)
	}
	homeConfig := fmt.Sprintf("[projects.%q]\ntrust_level = \"trusted\"\n", project)
	if err := os.WriteFile(config.ConfigPath(home), []byte(homeConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	projectConfigPath := filepath.Join(dotCodex, "config.toml")
	projectConfig := func(allowed string) string {
		return "[network_proxy]\n" +
			"enabled = true\n" +
			"proxy_url = \"http://127.0.0.1:0\"\n" +
			"enable_socks5 = false\n" +
			"allow_local_binding = true\n" +
			fmt.Sprintf("allowed_domains = [%q]\n", allowed) +
			"mode = \"full\"\n"
	}
	if err := os.WriteFile(projectConfigPath, []byte(projectConfig("127.0.0.1")), 0o600); err != nil {
		t.Fatal(err)
	}

	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home), DefaultCWD: home})
	defer router.Close()
	cfg, err := config.LoadWithOptions(home, &config.LoadOptions{CWD: project})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := router.managedNetworkForTurn("thread-project-reload", project, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if prepared == nil || router.managedNetworkReload == nil {
		t.Fatalf("prepared network or reload watcher missing: %#v / %#v", prepared, router.managedNetworkReload)
	}

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer origin.Close()
	proxyURL, err := url.Parse(prepared.EnvSnapshot()["HTTP_PROXY"])
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 2 * time.Second}
	status := func() int {
		response, requestErr := client.Get(origin.URL)
		if requestErr != nil {
			return 0
		}
		defer response.Body.Close()
		return response.StatusCode
	}
	if got := status(); got != http.StatusOK {
		t.Fatalf("initial project proxy status = %d", got)
	}
	if err := os.WriteFile(projectConfigPath, []byte(projectConfig("example.com")), 0o600); err != nil {
		t.Fatal(err)
	}
	waitForManagedNetworkStatus(t, status, http.StatusForbidden)
}

func waitForManagedNetworkStatus(t *testing.T, request func() int, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := request(); got == want {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("managed network status did not become %d", want)
}
