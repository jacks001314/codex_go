package network

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/things-go/go-socks5/statute"
	xproxy "golang.org/x/net/proxy"
)

func TestProxyManagedNetworkStartsForwardsAndClosesLikeRust(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("X-Upstream", "yes")
		_, _ = io.WriteString(w, request.Method+" "+request.URL.Path)
	}))
	defer upstream.Close()
	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.ProxyURL = "http://127.0.0.1:0"
	settings.SocksURL = "http://127.0.0.1:0"
	settings.SetAllowedDomains([]string{"127.0.0.1"})
	settings.AllowLocalBinding = true
	prepared, err := StartProxyManagedNetwork(context.Background(), ProxyConfig{Network: settings}, map[string]string{"BASE": "kept"})
	if err != nil {
		t.Fatalf("StartProxyManagedNetwork() error = %v", err)
	}
	if prepared.Env["BASE"] != "kept" || prepared.Env[ProxyActiveEnvKey] != "1" {
		t.Fatalf("prepared env = %#v", prepared.Env)
	}
	if len(prepared.SandboxContext.LoopbackPorts) != 2 || prepared.SandboxContext.LoopbackPorts[0] == 0 || prepared.SandboxContext.LoopbackPorts[1] == 0 {
		t.Fatalf("sandbox context = %#v", prepared.SandboxContext)
	}

	proxyURL, err := url.Parse(prepared.Env["HTTP_PROXY"])
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 3 * time.Second}
	response, err := client.Get(upstream.URL + "/through-http")
	if err != nil {
		t.Fatalf("proxied GET error = %v", err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || string(body) != "GET /through-http" || response.Header.Get("X-Upstream") != "yes" {
		t.Fatalf("proxied response = %d %q %#v", response.StatusCode, body, response.Header)
	}

	socksAddress := strings.TrimPrefix(prepared.Env["ALL_PROXY"], "socks5h://")
	socksResponse := socksHTTPGet(t, socksAddress, upstream.URL, "/through-socks")
	if !strings.Contains(socksResponse, "200 OK") || !strings.HasSuffix(socksResponse, "GET /through-socks") {
		t.Fatalf("SOCKS5 response = %q", socksResponse)
	}

	if err := prepared.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if conn, err := net.DialTimeout("tcp", proxyURL.Host, 100*time.Millisecond); err == nil {
		_ = conn.Close()
		t.Fatal("HTTP proxy still accepted connections after close")
	}
}

func TestProxyManagedNetworkReloadsPolicyWithoutChangingListenersLikeRust(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(w, request.Method)
	}))
	defer upstream.Close()
	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.ProxyURL = "http://127.0.0.1:0"
	settings.EnableSocks5 = false
	settings.AllowLocalBinding = true
	settings.SetAllowedDomains([]string{"127.0.0.1"})
	prepared, err := StartProxyManagedNetwork(context.Background(), ProxyConfig{Network: settings}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	proxyBefore := prepared.Env["HTTP_PROXY"]
	proxyURL, _ := url.Parse(proxyBefore)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 3 * time.Second}
	request, _ := http.NewRequest(http.MethodPost, upstream.URL, strings.NewReader("before"))
	response, err := client.Do(request)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("pre-reload request status=%v err=%v", responseStatus(response), err)
	}
	response.Body.Close()

	reloaded := settings
	reloaded.Mode = ProxyModeLimited
	if err := prepared.ReloadConfig(ProxyConfig{Network: reloaded}); err != nil {
		t.Fatalf("ReloadConfig() error = %v", err)
	}
	if prepared.Env["HTTP_PROXY"] != proxyBefore {
		t.Fatalf("proxy listener changed from %q to %q", proxyBefore, prepared.Env["HTTP_PROXY"])
	}
	request, _ = http.NewRequest(http.MethodPost, upstream.URL, strings.NewReader("after"))
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden || response.Header.Get("X-Proxy-Error") != "blocked-by-method-policy" {
		t.Fatalf("post-reload response = %d %#v", response.StatusCode, response.Header)
	}
}

func TestProxyManagedNetworkPreparesIsolatedEnvironmentListenersLikeRust(t *testing.T) {
	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.ProxyURL = "http://127.0.0.1:0"
	settings.EnableSocks5 = false
	settings.AllowLocalBinding = true
	settings.SetAllowedDomains([]string{"example.com"})
	audits := make(chan ProxyPolicyAuditEvent, 1)
	proxyConfig := ProxyConfig{
		Network:   settings,
		AuditSink: func(event ProxyPolicyAuditEvent) { audits <- event },
	}
	prepared, err := StartProxyManagedNetwork(context.Background(), proxyConfig, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()

	localEnv, localContext, err := prepared.PrepareForEnvironment("local")
	if err != nil {
		t.Fatal(err)
	}
	remoteEnv, remoteContext, err := prepared.PrepareForEnvironment("remote")
	if err != nil {
		t.Fatal(err)
	}
	rootProxy := prepared.EnvSnapshot()["HTTP_PROXY"]
	if localEnv["HTTP_PROXY"] == rootProxy || remoteEnv["HTTP_PROXY"] == rootProxy || localEnv["HTTP_PROXY"] == remoteEnv["HTTP_PROXY"] {
		t.Fatalf("environment proxies are not isolated: root=%q local=%q remote=%q", rootProxy, localEnv["HTTP_PROXY"], remoteEnv["HTTP_PROXY"])
	}
	for name, sandboxContext := range map[string]ProxyManagedNetworkSandboxContext{"local": localContext, "remote": remoteContext} {
		if len(sandboxContext.LoopbackPorts) != 1 || sandboxContext.LoopbackPorts[0] == 0 {
			t.Fatalf("%s sandbox context = %#v", name, sandboxContext)
		}
	}

	origin := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer origin.Close()
	remoteProxyURL, err := url.Parse(remoteEnv["HTTP_PROXY"])
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(remoteProxyURL)}, Timeout: 2 * time.Second}
	response, err := client.Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("remote environment status = %d", response.StatusCode)
	}
	select {
	case event := <-audits:
		if event.Request.EnvironmentID != "remote" {
			t.Fatalf("blocked environment id = %q", event.Request.EnvironmentID)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked environment audit was not observed")
	}
	if blocked := prepared.BlockedSnapshot(); len(blocked) != 1 || prepared.BlockedTotal() != 1 {
		t.Fatalf("aggregated blocked state = %#v total=%d", blocked, prepared.BlockedTotal())
	}
	reloaded := settings
	reloaded.SetAllowedDomains([]string{"127.0.0.1"})
	proxyConfig.Network = reloaded
	if err := prepared.ReloadConfig(proxyConfig); err != nil {
		t.Fatal(err)
	}
	if reloadedEnv, _, err := prepared.PrepareForEnvironment("remote"); err != nil || reloadedEnv["HTTP_PROXY"] != remoteEnv["HTTP_PROXY"] {
		t.Fatalf("remote environment listener changed during reload: env=%#v err=%v", reloadedEnv, err)
	}
	response, err = client.Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("remote environment did not receive reloaded policy: %d", response.StatusCode)
	}
	localProxyURL, _ := url.Parse(localEnv["HTTP_PROXY"])
	if err := prepared.Close(); err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{localProxyURL.Host, remoteProxyURL.Host} {
		if connection, err := net.DialTimeout("tcp", address, 100*time.Millisecond); err == nil {
			_ = connection.Close()
			t.Fatalf("environment proxy %s remained open after root close", address)
		}
	}
	if _, _, err := prepared.PrepareForEnvironment("after-close"); err == nil {
		t.Fatal("PrepareForEnvironment succeeded after close")
	}
}

func responseStatus(response *http.Response) any {
	if response == nil {
		return nil
	}
	return response.StatusCode
}

func TestProxyManagedNetworkEnforcesLimitedMethodPolicy(t *testing.T) {
	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.EnableSocks5 = false
	settings.ProxyURL = "http://127.0.0.1:0"
	settings.Mode = ProxyModeLimited
	settings.SetAllowedDomains([]string{"example.com"})
	prepared, err := StartProxyManagedNetwork(context.Background(), ProxyConfig{Network: settings}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	proxyURL, _ := url.Parse(prepared.Env["HTTP_PROXY"])
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: time.Second}
	request, _ := http.NewRequest(http.MethodPost, "http://example.com/blocked", strings.NewReader("x"))
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden || response.Header.Get("X-Proxy-Error") != "blocked-by-method-policy" {
		t.Fatalf("blocked response = %d %#v", response.StatusCode, response.Header)
	}
}

func TestProxyServerHostPolicyDefaultsToDenyAndRequiresExplicitLocalAllowLikeRust(t *testing.T) {
	settings := DefaultProxySettings()
	settings.Enabled = true
	server := &ProxyServer{}
	setProxyServerSettingsForTest(server, settings)
	if reason := server.blockReason(http.MethodGet, ProxyProtocolHTTP, "8.8.8.8", 443); reason != ProxyReasonNotAllowed {
		t.Fatalf("empty allowlist reason = %q", reason)
	}
	settings.SetAllowedDomains([]string{"localhost"})
	setProxyServerSettingsForTest(server, settings)
	if reason := server.blockReason(http.MethodGet, ProxyProtocolHTTP, "localhost", 443); reason != "" {
		t.Fatalf("explicit localhost reason = %q", reason)
	}
	settings.SetAllowedDomains([]string{"*.localhost"})
	setProxyServerSettingsForTest(server, settings)
	if reason := server.blockReason(http.MethodGet, ProxyProtocolHTTP, "localhost", 443); reason != ProxyReasonNotAllowedLocal {
		t.Fatalf("wildcard localhost reason = %q", reason)
	}
}

func TestProxyServerCheckedDialRejectsNonPublicTargetUnlessLocalBindingEnabledLikeRust(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.SetAllowedDomains([]string{"127.0.0.1"})
	server := &ProxyServer{}
	setProxyServerSettingsForTest(server, settings)
	if conn, err := server.dialCheckedTarget(context.Background(), "tcp", listener.Addr().String()); err == nil {
		conn.Close()
		t.Fatal("checked dial allowed local target while local binding was disabled")
	}
	settings.AllowLocalBinding = true
	setProxyServerSettingsForTest(server, settings)
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, _ := listener.Accept()
		accepted <- conn
	}()
	conn, err := server.dialCheckedTarget(context.Background(), "tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if peer := <-accepted; peer != nil {
		peer.Close()
	}
}

func TestProxyPolicyFailsClosedWhenAllowlistedHostnameDoesNotResolveLikeRust(t *testing.T) {
	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.ProxyURL = "http://127.0.0.1:0"
	settings.EnableSocks5 = false
	settings.SetAllowedDomains([]string{"does-not-resolve.invalid"})
	prepared, err := StartProxyManagedNetwork(context.Background(), ProxyConfig{Network: settings}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	proxyURL, err := url.Parse(prepared.EnvSnapshot()["HTTP_PROXY"])
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 3 * time.Second}
	response, err := client.Get("http://does-not-resolve.invalid/")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden || response.Header.Get("X-Proxy-Error") != "blocked-by-allowlist" {
		t.Fatalf("unresolved allowlisted host response = %d %#v", response.StatusCode, response.Header)
	}
	blocked := prepared.BlockedSnapshot()
	if len(blocked) != 1 || blocked[0].Reason != ProxyReasonNotAllowedLocal {
		t.Fatalf("blocked requests = %#v", blocked)
	}
}

func TestProxyPolicyDeciderOnlyOverridesNotAllowedAndEmitsAuditLikeRust(t *testing.T) {
	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.SetDeniedDomains([]string{"blocked.example"})
	var calls []ProxyPolicyRequest
	var events []ProxyPolicyAuditEvent
	server := &ProxyServer{
		environmentID: "remote-1",
		policyDecider: ProxyPolicyDeciderFunc(func(_ context.Context, request ProxyPolicyRequest) ProxyDecision {
			calls = append(calls, request)
			return AllowProxyDecision()
		}),
		auditSink: func(event ProxyPolicyAuditEvent) { events = append(events, event) },
	}
	setProxyServerSettingsForTest(server, settings)
	request := ProxyPolicyRequest{Protocol: ProxyProtocolHTTP, Host: "8.8.8.8", Port: 443, EnvironmentID: "remote-1", ClientAddr: "client", Method: http.MethodGet}
	decision := server.evaluateProxyPolicy(context.Background(), request)
	if !decision.Allow || len(calls) != 1 || len(events) != 1 {
		t.Fatalf("decider allow = %#v, calls = %#v, events = %#v", decision, calls, events)
	}
	if events[0].Decision != "allow" || events[0].Source != ProxyDecisionSourceDecider || events[0].Reason != ProxyReasonNotAllowed || !events[0].PolicyOverride {
		t.Fatalf("decider audit = %#v", events[0])
	}
	decision = server.evaluateProxyPolicy(context.Background(), ProxyPolicyRequest{Protocol: ProxyProtocolHTTP, Host: "blocked.example", Port: 80, Method: http.MethodGet})
	if decision.Allow || decision.Reason != ProxyReasonDenied || len(calls) != 1 {
		t.Fatalf("denylist decision = %#v, calls = %d", decision, len(calls))
	}
	decision = server.evaluateProxyPolicy(context.Background(), ProxyPolicyRequest{Protocol: ProxyProtocolHTTP, Host: "127.0.0.1", Port: 80, Method: http.MethodGet})
	if decision.Allow || decision.Reason != ProxyReasonNotAllowedLocal || len(calls) != 1 {
		t.Fatalf("local decision = %#v, calls = %d", decision, len(calls))
	}
}

func TestProxyPolicyDeciderAskPreservesAskDecisionLikeRust(t *testing.T) {
	settings := DefaultProxySettings()
	settings.Enabled = true
	server := &ProxyServer{
		policyDecider: ProxyPolicyDeciderFunc(func(context.Context, ProxyPolicyRequest) ProxyDecision {
			return AskProxyDecision(ProxyReasonNotAllowed)
		}),
	}
	setProxyServerSettingsForTest(server, settings)
	decision := server.evaluateProxyPolicy(context.Background(), ProxyPolicyRequest{Protocol: ProxyProtocolHTTP, Host: "8.8.8.8", Port: 80, Method: http.MethodGet})
	if decision.Allow || decision.Source != ProxyDecisionSourceDecider || decision.Decision != ProxyPolicyDecisionAsk || decision.Reason != ProxyReasonNotAllowed {
		t.Fatalf("ask decision = %#v", decision)
	}
}

func TestProxyServerBlockedRequestObserverSnapshotAndDrainLikeRust(t *testing.T) {
	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.SetDeniedDomains([]string{"blocked.example"})
	observed := make(chan ProxyBlockedRequest, 1)
	server := &ProxyServer{blocked: ProxyBlockedRequestObserverFunc(func(_ context.Context, request ProxyBlockedRequest) {
		observed <- request
	})}
	policy, err := buildProxyRuntimePolicy(settings)
	if err != nil {
		t.Fatal(err)
	}
	server.policy.Store(policy)
	decision := server.evaluateProxyPolicy(context.Background(), ProxyPolicyRequest{
		Protocol:   ProxyProtocolHTTP,
		Host:       "blocked.example",
		Port:       80,
		ClientAddr: "127.0.0.1:1234",
		Method:     http.MethodGet,
	})
	if decision.Allow || decision.Reason != ProxyReasonDenied {
		t.Fatalf("decision = %#v", decision)
	}
	entry := <-observed
	if entry.Host != "blocked.example" || entry.Reason != ProxyReasonDenied || entry.Protocol != string(ProxyProtocolHTTP) || entry.Port == nil || *entry.Port != 80 || entry.Timestamp <= 0 {
		t.Fatalf("blocked request = %#v", entry)
	}
	first := server.BlockedSnapshot()
	second := server.BlockedSnapshot()
	if len(first) != 1 || len(second) != 1 || server.BlockedTotal() != 1 {
		t.Fatalf("snapshot lengths = %d/%d, total = %d", len(first), len(second), server.BlockedTotal())
	}
	drained := server.DrainBlocked()
	if len(drained) != 1 || len(server.BlockedSnapshot()) != 0 || server.BlockedTotal() != 1 {
		t.Fatalf("drain = %d, remaining = %d, total = %d", len(drained), len(server.BlockedSnapshot()), server.BlockedTotal())
	}
}

func TestProxyServerBlockedRequestBufferKeepsRustWindow(t *testing.T) {
	settings := DefaultProxySettings()
	settings.Enabled = false
	server := &ProxyServer{}
	policy, err := buildProxyRuntimePolicy(settings)
	if err != nil {
		t.Fatal(err)
	}
	server.policy.Store(policy)
	for index := 0; index < maxProxyBlockedEvents+5; index++ {
		server.evaluateProxyPolicy(context.Background(), ProxyPolicyRequest{Protocol: ProxyProtocolHTTP, Host: fmt.Sprintf("example%d.com", index), Port: 80, Method: http.MethodGet})
	}
	blocked := server.DrainBlocked()
	if len(blocked) != maxProxyBlockedEvents || blocked[0].Host != "example5.com" || server.BlockedTotal() != maxProxyBlockedEvents+5 {
		t.Fatalf("blocked window = %d first=%q total=%d", len(blocked), blocked[0].Host, server.BlockedTotal())
	}
}

func TestProxyManagedNetworkRejectsAbsoluteFormHostMismatchLikeRust(t *testing.T) {
	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.EnableSocks5 = false
	settings.ProxyURL = "http://127.0.0.1:0"
	settings.SetAllowedDomains([]string{"raw.githubusercontent.com", "api.github.com"})
	prepared, err := StartProxyManagedNetwork(context.Background(), ProxyConfig{Network: settings}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	proxyURL, _ := url.Parse(prepared.Env["HTTP_PROXY"])
	conn, err := net.DialTimeout("tcp", proxyURL.Host, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	_, err = io.WriteString(conn, "GET http://raw.githubusercontent.com/openai/codex/main/README.md HTTP/1.1\r\nHost: api.github.com\r\nConnection: close\r\n\r\n")
	if err != nil {
		t.Fatal(err)
	}
	status, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, " 400 ") {
		t.Fatalf("absolute-form mismatch response = %q", status)
	}
}

func TestProxyHTTPValidationConnReplaysCONNECTBytesExactly(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	raw := []byte("CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n")
	wrapped := &proxyHTTPValidationConn{Conn: server, state: &proxyRawRequestState{}}
	go func() { _, _ = client.Write(raw) }()
	replayed := make([]byte, len(raw))
	if _, err := io.ReadFull(wrapped, replayed); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(replayed, raw) {
		t.Fatalf("replayed CONNECT = %q", replayed)
	}
	if wrapped.state.validationErr != nil {
		t.Fatalf("CONNECT validation error = %v", wrapped.state.validationErr)
	}
}

func TestProxyManagedNetworkForwardsSOCKS5UDPLikeRust(t *testing.T) {
	echo, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		buffer := make([]byte, 1024)
		for {
			n, peer, err := echo.ReadFromUDP(buffer)
			if err != nil {
				return
			}
			_, _ = echo.WriteToUDP(buffer[:n], peer)
		}
	}()

	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.ProxyURL = "http://127.0.0.1:0"
	settings.SocksURL = "http://127.0.0.1:0"
	settings.SetAllowedDomains([]string{"127.0.0.1"})
	settings.AllowLocalBinding = true
	prepared, err := StartProxyManagedNetwork(context.Background(), ProxyConfig{Network: settings}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()

	control, relay := socks5UDPAssociate(t, strings.TrimPrefix(prepared.Env["ALL_PROXY"], "socks5h://"))
	defer control.Close()
	udp, err := net.DialUDP("udp", nil, relay)
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	packet, err := statute.NewDatagram(echo.LocalAddr().String(), []byte("through-socks-udp"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := udp.Write(packet.Bytes()); err != nil {
		t.Fatal(err)
	}
	_ = udp.SetReadDeadline(time.Now().Add(3 * time.Second))
	buffer := make([]byte, 1024)
	n, err := udp.Read(buffer)
	if err != nil {
		t.Fatal(err)
	}
	response, err := statute.ParseDatagram(buffer[:n])
	if err != nil {
		t.Fatal(err)
	}
	if string(response.Data) != "through-socks-udp" {
		t.Fatalf("SOCKS5 UDP response = %q", response.Data)
	}
}

func TestProxyManagedNetworkRejectsSOCKS5UDPWhenDisabledLikeRust(t *testing.T) {
	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.EnableSocks5UDP = false
	settings.ProxyURL = "http://127.0.0.1:0"
	settings.SocksURL = "http://127.0.0.1:0"
	prepared, err := StartProxyManagedNetwork(context.Background(), ProxyConfig{Network: settings}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()

	conn := socks5Negotiate(t, strings.TrimPrefix(prepared.Env["ALL_PROXY"], "socks5h://"))
	defer conn.Close()
	if _, err := conn.Write([]byte{5, statute.CommandAssociate, 0, statute.ATYPIPv4, 0, 0, 0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	reply, err := statute.ParseReply(conn)
	if err != nil {
		t.Fatal(err)
	}
	if reply.Response != statute.RepCommandNotSupported {
		t.Fatalf("SOCKS5 UDP disabled reply = %d", reply.Response)
	}
}

func TestProxyManagedNetworkBlocksSOCKS5UDPInLimitedModeLikeRust(t *testing.T) {
	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.Mode = ProxyModeLimited
	settings.AllowLocalBinding = true
	settings.ProxyURL = "http://127.0.0.1:0"
	settings.SocksURL = "http://127.0.0.1:0"
	settings.SetAllowedDomains([]string{"127.0.0.1"})
	prepared, err := StartProxyManagedNetwork(context.Background(), ProxyConfig{Network: settings}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()

	control, relay := socks5UDPAssociate(t, strings.TrimPrefix(prepared.Env["ALL_PROXY"], "socks5h://"))
	defer control.Close()
	udp, err := net.DialUDP("udp", nil, relay)
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	packet, err := statute.NewDatagram("127.0.0.1:9", []byte("blocked"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := udp.Write(packet.Bytes()); err != nil {
		t.Fatal(err)
	}
	_ = udp.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
	if _, err := udp.Read(make([]byte, 1024)); err == nil {
		t.Fatal("limited mode unexpectedly relayed SOCKS5 UDP")
	}
}

func TestProxyPolicyUDPConnEvaluatesEveryDatagramLikeRust(t *testing.T) {
	listener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	underlying, err := net.DialUDP("udp", nil, listener.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer underlying.Close()
	settings := DefaultProxySettings()
	settings.Enabled = true
	var decisions int
	server := &ProxyServer{
		policyDecider: ProxyPolicyDeciderFunc(func(context.Context, ProxyPolicyRequest) ProxyDecision {
			decisions++
			return AllowProxyDecision()
		}),
	}
	setProxyServerSettingsForTest(server, settings)
	conn := &proxyPolicyUDPConn{Conn: underlying, server: server, host: "8.8.8.8", port: 53}
	for _, payload := range [][]byte{[]byte("one"), []byte("two")} {
		if _, err := conn.Write(payload); err != nil {
			t.Fatal(err)
		}
	}
	if decisions != 2 {
		t.Fatalf("UDP policy decisions = %d", decisions)
	}
	_ = listener.SetReadDeadline(time.Now().Add(time.Second))
	buffer := make([]byte, 16)
	for index, want := range []string{"one", "two"} {
		n, _, err := listener.ReadFromUDP(buffer)
		if err != nil {
			t.Fatal(err)
		}
		if string(buffer[:n]) != want {
			t.Fatalf("UDP payload %d = %q", index, buffer[:n])
		}
	}
}

func TestProxyManagedNetworkMITMEnforcesLimitedHTTPSMethodsLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(w, request.Method+" "+request.URL.Path)
	}))
	defer upstream.Close()
	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.Mode = ProxyModeLimited
	settings.MITM = true
	settings.EnableSocks5 = false
	settings.ProxyURL = "http://127.0.0.1:0"
	settings.SetAllowedDomains([]string{"127.0.0.1"})
	settings.AllowLocalBinding = true
	upstreamCAPath := filepath.Join(home, "upstream-ca.pem")
	upstreamCAPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: upstream.Certificate().Raw})
	if err := os.WriteFile(upstreamCAPath, upstreamCAPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	prepared, err := StartProxyManagedNetwork(context.Background(), ProxyConfig{Network: settings}, map[string]string{"SSL_CERT_FILE": upstreamCAPath})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	client := managedMITMHTTPClient(t, prepared)

	response, err := client.Get(upstream.URL + "/allowed")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || string(body) != "GET /allowed" {
		t.Fatalf("MITM GET response = %d %q", response.StatusCode, body)
	}

	request, _ := http.NewRequest(http.MethodPost, upstream.URL+"/blocked", strings.NewReader("x"))
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden || response.Header.Get("X-Proxy-Error") != "blocked-by-method-policy" {
		t.Fatalf("MITM POST response = %d %#v", response.StatusCode, response.Header)
	}
}

func TestProxyManagedNetworkMITMInspectsHTTP2StreamsLikeRust(t *testing.T) {
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("X-Upstream-Protocol", fmt.Sprintf("h%d", request.ProtoMajor))
		_, _ = io.WriteString(w, "h2-ok")
	}))
	upstream.EnableHTTP2 = true
	upstream.StartTLS()
	defer upstream.Close()

	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.ProxyURL = "http://127.0.0.1:0"
	settings.EnableSocks5 = false
	settings.Mode = ProxyModeLimited
	settings.AllowLocalBinding = true
	settings.MITM = true
	settings.SetAllowedDomains([]string{"127.0.0.1"})
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	upstreamCAPath := filepath.Join(home, "upstream-h2-ca.pem")
	upstreamCAPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: upstream.Certificate().Raw})
	if err := os.WriteFile(upstreamCAPath, upstreamCAPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	prepared, err := StartProxyManagedNetwork(context.Background(), ProxyConfig{Network: settings}, map[string]string{"SSL_CERT_FILE": upstreamCAPath})
	if err != nil {
		t.Fatalf("StartProxyManagedNetwork() error = %v", err)
	}
	defer prepared.Close()
	client := managedMITMHTTPClient(t, prepared)

	response, err := client.Get(upstream.URL + "/h2")
	if err != nil {
		t.Fatalf("HTTP/2 GET error = %v", err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ProtoMajor != 2 || response.Header.Get("X-Upstream-Protocol") != "h2" || string(body) != "h2-ok" {
		t.Fatalf("HTTP/2 response status=%d proto=%s upstream=%q body=%q", response.StatusCode, response.Proto, response.Header.Get("X-Upstream-Protocol"), body)
	}

	post, err := client.Post(upstream.URL+"/h2", "text/plain", strings.NewReader("blocked"))
	if err != nil {
		t.Fatalf("HTTP/2 POST error = %v", err)
	}
	defer post.Body.Close()
	if post.StatusCode != http.StatusForbidden || post.ProtoMajor != 2 {
		t.Fatalf("HTTP/2 blocked POST status=%d proto=%s", post.StatusCode, post.Proto)
	}
}

func TestProxyManagedNetworkMITMRejectsInnerHostMismatchLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	reached := make(chan struct{}, 1)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		reached <- struct{}{}
	}))
	defer upstream.Close()
	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.Mode = ProxyModeLimited
	settings.MITM = true
	settings.EnableSocks5 = false
	settings.ProxyURL = "http://127.0.0.1:0"
	settings.SetAllowedDomains([]string{"127.0.0.1"})
	prepared, err := StartProxyManagedNetwork(context.Background(), ProxyConfig{Network: settings}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	prepared.server.httpProxy.Tr.TLSClientConfig.InsecureSkipVerify = true // upstream must not be reached
	client := managedMITMHTTPClient(t, prepared)
	request, _ := http.NewRequest(http.MethodGet, upstream.URL+"/", nil)
	request.Host = "evil.example"
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "host mismatch") {
		t.Fatalf("MITM host mismatch response = %d %q", response.StatusCode, body)
	}
	select {
	case <-reached:
		t.Fatal("mismatched MITM request reached upstream")
	default:
	}
}

func TestRemoveProxyHopByHopRequestHeadersLikeRust(t *testing.T) {
	headers := http.Header{
		"Connection":          []string{"X-Hop, keep-alive"},
		"X-Hop":               []string{"remove"},
		"Keep-Alive":          []string{"timeout=5"},
		"Proxy-Authorization": []string{"Basic secret"},
		"X-Forwarded-For":     []string{"127.0.0.1"},
	}
	removeProxyHopByHopRequestHeaders(headers)
	for _, name := range []string{"Connection", "X-Hop", "Keep-Alive", "Proxy-Authorization"} {
		if headers.Get(name) != "" {
			t.Fatalf("hop-by-hop header %s survived: %#v", name, headers)
		}
	}
	if headers.Get("X-Forwarded-For") != "127.0.0.1" {
		t.Fatalf("forwarding header removed: %#v", headers)
	}
}

func TestProxyUnixSocketUnsupportedAndMethodGuardLikeRust(t *testing.T) {
	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.Mode = ProxyModeLimited
	server := &ProxyServer{}
	setProxyServerSettingsForTest(server, settings)
	request := httptest.NewRequest(http.MethodPost, "http://unix-socket/test", strings.NewReader("x"))
	request.Header.Set("X-Unix-Socket", "/tmp/test.sock")
	recorder := httptest.NewRecorder()
	server.serveHTTPProxy(recorder, request)
	if recorder.Code != http.StatusForbidden || recorder.Header().Get("X-Proxy-Error") != "blocked-by-method-policy" {
		t.Fatalf("unix socket method response = %d %#v", recorder.Code, recorder.Header())
	}
	if runtime.GOOS == "darwin" {
		return
	}
	settings.Mode = ProxyModeFull
	setProxyServerSettingsForTest(server, settings)
	request = httptest.NewRequest(http.MethodGet, "http://unix-socket/test", nil)
	request.Header.Set("X-Unix-Socket", "/tmp/test.sock")
	recorder = httptest.NewRecorder()
	server.serveHTTPProxy(recorder, request)
	if recorder.Code != http.StatusNotImplemented || !strings.Contains(recorder.Body.String(), "unix sockets unsupported") {
		t.Fatalf("unix socket unsupported response = %d %q", recorder.Code, recorder.Body.String())
	}
}

func TestProxyManagedNetworkMITMAppliesHookActionsLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("PROXY_HOOK_SECRET", "real-hook-secret")
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(w, request.Header.Get("X-Injected")+"|"+request.Header.Get("X-Remove"))
	}))
	defer upstream.Close()
	secretEnv := "PROXY_HOOK_SECRET"
	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.MITM = true
	settings.EnableSocks5 = false
	settings.ProxyURL = "http://127.0.0.1:0"
	settings.SetAllowedDomains([]string{"127.0.0.1"})
	settings.AllowLocalBinding = true
	settings.MITMHooks = []ProxyMITMHookConfig{{
		Host: "127.0.0.1",
		Match: ProxyMITMHookMatchConfig{
			Methods:      []string{http.MethodPost},
			PathPrefixes: []string{"/hook"},
		},
		Actions: ProxyMITMHookActionsConfig{
			StripRequestHeaders: []string{"X-Remove"},
			InjectRequestHeaders: []ProxyInjectedHeaderConfig{{
				Name:         "X-Injected",
				SecretEnvVar: &secretEnv,
				Prefix:       "Bearer ",
			}},
		},
	}}
	prepared, err := StartProxyManagedNetwork(context.Background(), ProxyConfig{Network: settings}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	prepared.server.httpProxy.Tr.TLSClientConfig.InsecureSkipVerify = true // test-only upstream CA
	client := managedMITMHTTPClient(t, prepared)
	request, _ := http.NewRequest(http.MethodPost, upstream.URL+"/hook/allowed", strings.NewReader("x"))
	request.Header.Set("X-Remove", "must-disappear")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || string(body) != "Bearer real-hook-secret|" {
		t.Fatalf("hook response = %d %q", response.StatusCode, body)
	}

	request, _ = http.NewRequest(http.MethodGet, upstream.URL+"/hook/denied", nil)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden || response.Header.Get("X-Proxy-Error") != "blocked-by-mitm-hook" {
		t.Fatalf("hook miss response = %d %#v", response.StatusCode, response.Header)
	}
}

func TestProxyManagedNetworkCredentialBrokerVirtualizesAndInjectsOverMITMLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	const realKey = "sk-proj-real-openai-key"
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(w, request.Header.Get("Authorization"))
	}))
	defer upstream.Close()
	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.MITM = true
	settings.CredentialBroker = true
	settings.EnableSocks5 = false
	settings.ProxyURL = "http://127.0.0.1:0"
	settings.SetAllowedDomains([]string{"api.openai.com"})
	prepared, err := StartProxyManagedNetwork(context.Background(), ProxyConfig{Network: settings}, map[string]string{"OPENAI_API_KEY": realKey})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	dummyKey := prepared.Env["OPENAI_API_KEY"]
	if dummyKey == "" || dummyKey == realKey || prepared.Env[CredentialBrokerActiveEnvKey] != "1" {
		t.Fatalf("brokered env = %#v", prepared.Env)
	}
	prepared.server.httpProxy.Tr.TLSClientConfig.InsecureSkipVerify = true // test-only upstream CA
	upstreamAddress := upstream.Listener.Addr().String()
	prepared.server.httpProxy.Tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, upstreamAddress)
	}
	client := managedMITMHTTPClient(t, prepared)
	request, _ := http.NewRequest(http.MethodGet, "https://api.openai.com/v1/models", nil)
	request.Header.Set("Authorization", "Bearer "+dummyKey)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || string(body) != "Bearer "+realKey {
		t.Fatalf("broker response = %d %q", response.StatusCode, body)
	}
}

func TestProxyManagedNetworkPlaintextCredentialInjectionRequiresExplicitOptInLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	const realKey = "sk-proj-real-plaintext-key"
	seen := make(chan string, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		seen <- request.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.MITM = true
	settings.CredentialBroker = true
	settings.EnableSocks5 = false
	settings.ProxyURL = "http://127.0.0.1:0"
	settings.SetAllowedDomains([]string{"api.openai.com"})
	prepared, err := StartProxyManagedNetwork(context.Background(), ProxyConfig{Network: settings}, map[string]string{"OPENAI_API_KEY": realKey})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	dummyKey := prepared.Env["OPENAI_API_KEY"]
	upstreamAddress := upstream.Listener.Addr().String()
	prepared.server.httpProxy.Tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, upstreamAddress)
	}
	proxyURL, _ := url.Parse(prepared.Env["HTTP_PROXY"])
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 5 * time.Second}
	request, _ := http.NewRequest(http.MethodGet, "http://api.openai.com/v1/models", nil)
	request.Header.Set("Authorization", "Bearer "+dummyKey)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if authorization := <-seen; authorization != "Bearer "+dummyKey {
		t.Fatalf("plaintext credential injected without opt-in: %q", authorization)
	}
	reloaded := prepared.server.runtimePolicy().settings
	reloaded.DangerouslyAllowPlaintextCredentialInjection = true
	setProxyServerSettingsForTest(prepared.server, reloaded)
	request, _ = http.NewRequest(http.MethodGet, "http://api.openai.com/v1/models", nil)
	request.Header.Set("Authorization", "Bearer "+dummyKey)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if authorization := <-seen; authorization != "Bearer "+realKey {
		t.Fatalf("plaintext credential was not injected after opt-in: %q", authorization)
	}
}

func TestProxyManagedNetworkSOCKS5UsesMITMForBrokeredHTTPSLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	const realKey = "sk-proj-real-socks-key"
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(w, request.Header.Get("Authorization"))
	}))
	defer upstream.Close()
	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.MITM = true
	settings.CredentialBroker = true
	settings.ProxyURL = "http://127.0.0.1:0"
	settings.SocksURL = "http://127.0.0.1:0"
	settings.SetAllowedDomains([]string{"api.openai.com"})
	prepared, err := StartProxyManagedNetwork(context.Background(), ProxyConfig{Network: settings}, map[string]string{"OPENAI_API_KEY": realKey})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	dummyKey := prepared.Env["OPENAI_API_KEY"]
	prepared.server.httpProxy.Tr.TLSClientConfig.InsecureSkipVerify = true // test-only upstream CA
	upstreamAddress := upstream.Listener.Addr().String()
	prepared.server.httpProxy.Tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, upstreamAddress)
	}
	roots := managedMITMRootPool(t, prepared)
	socksAddress := strings.TrimPrefix(prepared.Env["ALL_PROXY"], "socks5h://")
	dialer, err := xproxy.SOCKS5("tcp", socksAddress, nil, &net.Dialer{Timeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{
		DialContext: func(_ context.Context, network, address string) (net.Conn, error) {
			return dialer.Dial(network, address)
		},
		TLSClientConfig:   &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		DisableKeepAlives: true,
	}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	request, _ := http.NewRequest(http.MethodGet, "https://api.openai.com/v1/models", nil)
	request.Header.Set("Authorization", "Bearer "+dummyKey)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	transport.CloseIdleConnections()
	if response.StatusCode != http.StatusOK || string(body) != "Bearer "+realKey {
		t.Fatalf("SOCKS5 broker response = %d %q", response.StatusCode, body)
	}
}

func TestProxyManagedNetworkSOCKS5LimitedModeMITMEnforcesHTTPSMethodsLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(w, request.Method+" "+request.URL.Path)
	}))
	defer upstream.Close()
	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.Mode = ProxyModeLimited
	settings.MITM = true
	settings.ProxyURL = "http://127.0.0.1:0"
	settings.SocksURL = "http://127.0.0.1:0"
	settings.AllowLocalBinding = true
	settings.SetAllowedDomains([]string{"limited.test"})
	prepared, err := StartProxyManagedNetwork(context.Background(), ProxyConfig{Network: settings}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	prepared.server.httpProxy.Tr.TLSClientConfig.InsecureSkipVerify = true // test-only upstream CA
	upstreamAddress := upstream.Listener.Addr().String()
	prepared.server.httpProxy.Tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, upstreamAddress)
	}
	roots := managedMITMRootPool(t, prepared)
	socksAddress := strings.TrimPrefix(prepared.Env["ALL_PROXY"], "socks5h://")
	dialer, err := xproxy.SOCKS5("tcp", socksAddress, nil, &net.Dialer{Timeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{
		DialContext: func(_ context.Context, network, address string) (net.Conn, error) {
			return dialer.Dial(network, address)
		},
		TLSClientConfig:   &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		DisableKeepAlives: true,
	}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	response, err := client.Get("https://limited.test/allowed")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || string(body) != "GET /allowed" {
		t.Fatalf("limited SOCKS5 GET response = %d %q", response.StatusCode, body)
	}
	request, _ := http.NewRequest(http.MethodPost, "https://limited.test/blocked", strings.NewReader("x"))
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	transport.CloseIdleConnections()
	if response.StatusCode != http.StatusForbidden || response.Header.Get("X-Proxy-Error") != "blocked-by-method-policy" {
		t.Fatalf("limited SOCKS5 POST response = %d %#v", response.StatusCode, response.Header)
	}
}

func TestProxyManagedNetworkSOCKS5BrokeredHostForwardsServerFirstOpaqueProtocolLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	accepted := make(chan struct{})
	go func() {
		conn, err := upstream.Accept()
		if err != nil {
			return
		}
		close(accepted)
		defer conn.Close()
		_, _ = io.WriteString(conn, "server-first-opaque")
	}()
	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.MITM = true
	settings.CredentialBroker = true
	settings.ProxyURL = "http://127.0.0.1:0"
	settings.SocksURL = "http://127.0.0.1:0"
	settings.SetAllowedDomains([]string{"127.0.0.1"})
	settings.AllowLocalBinding = true
	prepared, err := StartProxyManagedNetwork(context.Background(), ProxyConfig{Network: settings}, map[string]string{
		"GH_HOST":             "127.0.0.1",
		"GH_ENTERPRISE_TOKEN": "ghp_real_enterprise_token",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	if !prepared.server.runtimePolicy().broker.HostRequiresMITM("127.0.0.1") {
		t.Fatalf("enterprise credential was not bound: %#v", prepared.Env)
	}
	socksAddress := strings.TrimPrefix(prepared.Env["ALL_PROXY"], "socks5h://")
	dialer, err := xproxy.SOCKS5("tcp", socksAddress, nil, &net.Dialer{Timeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := dialer.Dial("tcp", upstream.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	message, err := io.ReadAll(conn)
	if err != nil {
		select {
		case <-accepted:
			t.Fatalf("opaque upstream connected but response was not relayed: %v", err)
		default:
			t.Fatalf("opaque upstream was not connected: %v", err)
		}
	}
	if string(message) != "server-first-opaque" {
		t.Fatalf("opaque SOCKS5 response = %q", message)
	}
}

func TestProxyManagedNetworkHTTPBrokeredConnectForwardsServerFirstOpaqueProtocolLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	go func() {
		conn, err := upstream.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.WriteString(conn, "http-server-first-opaque")
	}()
	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.MITM = true
	settings.CredentialBroker = true
	settings.EnableSocks5 = false
	settings.ProxyURL = "http://127.0.0.1:0"
	settings.SetAllowedDomains([]string{"127.0.0.1"})
	settings.AllowLocalBinding = true
	prepared, err := StartProxyManagedNetwork(context.Background(), ProxyConfig{Network: settings}, map[string]string{
		"GH_HOST":             "127.0.0.1",
		"GH_ENTERPRISE_TOKEN": "ghp_real_enterprise_token",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	proxyURL, _ := url.Parse(prepared.Env["HTTP_PROXY"])
	conn, err := net.DialTimeout("tcp", proxyURL.Host, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", upstream.Addr(), upstream.Addr()); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	status, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(status, " 200 ") {
		t.Fatalf("CONNECT status = %q, err = %v", status, err)
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\r\n" {
			break
		}
	}
	message, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(message) != "http-server-first-opaque" {
		t.Fatalf("opaque HTTP CONNECT response = %q", message)
	}
}

func socksHTTPGet(t *testing.T, socksAddress string, upstreamURL string, path string) string {
	t.Helper()
	upstream, err := url.Parse(upstreamURL)
	if err != nil {
		t.Fatal(err)
	}
	host, portText, err := net.SplitHostPort(upstream.Host)
	if err != nil {
		t.Fatal(err)
	}
	port, err := net.LookupPort("tcp", portText)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", socksAddress, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
		t.Fatal(err)
	}
	method := make([]byte, 2)
	if _, err := io.ReadFull(conn, method); err != nil || method[1] != 0 {
		t.Fatalf("SOCKS5 method = %v, err = %v", method, err)
	}
	ip := net.ParseIP(host).To4()
	request := []byte{5, 1, 0, 1, ip[0], ip[1], ip[2], ip[3], 0, 0}
	binary.BigEndian.PutUint16(request[len(request)-2:], uint16(port))
	if _, err := conn.Write(request); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(conn, reply); err != nil || reply[1] != 0 {
		t.Fatalf("SOCKS5 connect reply = %v, err = %v", reply, err)
	}
	if _, err := io.WriteString(conn, "GET "+path+" HTTP/1.1\r\nHost: "+upstream.Host+"\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	response, err := io.ReadAll(bufio.NewReader(conn))
	if err != nil {
		t.Fatal(err)
	}
	return string(response)
}

func socks5Negotiate(t *testing.T, address string) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	method := make([]byte, 2)
	if _, err := io.ReadFull(conn, method); err != nil || method[0] != 5 || method[1] != 0 {
		conn.Close()
		t.Fatalf("SOCKS5 method = %v, err = %v", method, err)
	}
	return conn
}

func managedMITMHTTPClient(t *testing.T, prepared *PreparedProxyManagedNetwork) *http.Client {
	t.Helper()
	roots := managedMITMRootPool(t, prepared)
	proxyURL, err := url.Parse(prepared.Env["HTTPS_PROXY"])
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{
		Transport: &http.Transport{
			Proxy:             http.ProxyURL(proxyURL),
			ForceAttemptHTTP2: true,
			TLSClientConfig:   &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		},
		Timeout: 5 * time.Second,
	}
}

func setProxyServerSettingsForTest(server *ProxyServer, settings ProxySettings) {
	var hooks ProxyMITMHooksByHost
	var broker *ProxyCredentialBroker
	if policy := server.policy.Load(); policy != nil {
		hooks = policy.mitmHooks
		broker = policy.broker
	}
	if broker == nil {
		broker = NewProxyCredentialBroker(settings.CredentialBroker)
	}
	server.policy.Store(&proxyRuntimePolicy{settings: settings, mitmHooks: hooks, broker: broker})
}

func managedMITMRootPool(t *testing.T, prepared *PreparedProxyManagedNetwork) *x509.CertPool {
	t.Helper()
	bundle, err := os.ReadFile(prepared.Env["SSL_CERT_FILE"])
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(bundle) {
		t.Fatal("managed MITM trust bundle did not contain a certificate")
	}
	return roots
}

func socks5UDPAssociate(t *testing.T, address string) (net.Conn, *net.UDPAddr) {
	t.Helper()
	conn := socks5Negotiate(t, address)
	if _, err := conn.Write([]byte{5, statute.CommandAssociate, 0, statute.ATYPIPv4, 0, 0, 0, 0, 0, 0}); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	reply, err := statute.ParseReply(conn)
	if err != nil {
		conn.Close()
		t.Fatal(err)
	}
	if reply.Response != statute.RepSuccess {
		conn.Close()
		t.Fatalf("SOCKS5 UDP associate reply = %d", reply.Response)
	}
	host := reply.BndAddr.FQDN
	if host == "" {
		host = reply.BndAddr.IP.String()
	}
	relay, err := net.ResolveUDPAddr("udp", net.JoinHostPort(host, strconv.Itoa(reply.BndAddr.Port)))
	if err != nil {
		conn.Close()
		t.Fatal(err)
	}
	return conn, relay
}
