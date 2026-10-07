package execserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/network"
)

// prepareExecutorNetworkProxyForPrivateIPTest launches the executor-local
// managed network proxy for one exec request and guarantees teardown even when
// an assertion fails, so a leaked listener cannot distort sibling cases.
func prepareExecutorNetworkProxyForPrivateIPTest(t *testing.T, server *Server, env map[string]string) (*ExecParams, *network.PreparedProxyManagedNetwork) {
	t.Helper()
	preparedParams, prepared, policyCancel, err := server.prepareExecutorNetworkProxy(context.Background(), &ExecParams{
		ProcessID: "private-ips-process",
		EnvPolicy: &ExecEnvPolicy{Inherit: "none"},
		Env:       env,
		Sandbox:   json.RawMessage(`{}`),
		NetworkProxy: &RemoteNetworkProxyLaunchConfig{
			Proxy: RemoteNetworkProxyConfig{
				Enabled:            true,
				EnableSOCKS5:       false,
				AllowUpstreamProxy: true,
				AllowLocalBinding:  true,
				Mode:               string(network.ProxyModeFull),
				Domains:            map[string]string{"fd00::1": "allow"},
			},
		},
	})
	if err != nil {
		t.Fatalf("prepareExecutorNetworkProxy() error = %v", err)
	}
	t.Cleanup(func() {
		policyCancel()
		if prepared != nil {
			_ = prepared.Close()
		}
	})
	if prepared == nil {
		t.Fatal("prepared executor network proxy is nil")
	}
	return preparedParams, prepared
}

// TestPrepareExecutorNetworkProxyCarriesPrivateIPRoutingLikeRust covers Rust
// #48568 (`b8d5e3f12e`, exec-server/src/process_sandbox.rs:132/350/380): the
// executor's trusted startup routing
// (ExecServerRuntimeOptions::proxy_private_ips_via_upstream) is copied onto the
// managed network proxy launch, and the wire/remote launch config alone cannot
// enable it.
func TestPrepareExecutorNetworkProxyCarriesPrivateIPRoutingLikeRust(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("enabled=%v", enabled), func(t *testing.T) {
			server := NewServer()
			server.SetProxyPrivateIPsViaUpstream(enabled)
			// A session handoff must keep the startup routing bit (Rust keeps
			// the runtime options on the exec-server handler).
			if got := newSessionServerWithRuntimeOptions(nil, server).proxyPrivateIPsViaUpstreamValue(); got != enabled {
				t.Fatalf("session handoff lost the routing bit: %v, want %v", got, enabled)
			}
			_, prepared := prepareExecutorNetworkProxyForPrivateIPTest(t, server, nil)
			if got := prepared.RemoteConfigSnapshot().PrivateIPsViaUpstream; got != enabled {
				t.Fatalf("network proxy launch PrivateIPsViaUpstream = %v, want %v", got, enabled)
			}
		})
	}
}

// TestExecutorNetworkProxyRoutesPrivateIPsUpstreamWhenOptedInLikeRust is the
// end-to-end wiring check for Rust #48568: with the executor's opt-in, a
// permitted private destination really travels through the configured upstream
// proxy of the executor-local managed network proxy; without the opt-in the
// same request never reaches that upstream.
func TestExecutorNetworkProxyRoutesPrivateIPsUpstreamWhenOptedInLikeRust(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		mu.Lock()
		seen = append(seen, request.URL.Host)
		mu.Unlock()
		w.Header().Set("X-Upstream-Proxy", "yes")
		_, _ = io.WriteString(w, "upstream:"+request.URL.Path)
	}))
	defer upstream.Close()
	seenHosts := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}

	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("enabled=%v", enabled), func(t *testing.T) {
			mu.Lock()
			seen = nil
			mu.Unlock()
			server := NewServer()
			server.SetProxyPrivateIPsViaUpstream(enabled)
			preparedParams, _ := prepareExecutorNetworkProxyForPrivateIPTest(t, server, map[string]string{
				"HTTP_PROXY":  upstream.URL,
				"HTTPS_PROXY": upstream.URL,
			})
			proxyURL, err := url.Parse(preparedParams.Env["HTTP_PROXY"])
			if err != nil {
				t.Fatalf("parse executor proxy URL %q: %v", preparedParams.Env["HTTP_PROXY"], err)
			}
			client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 5 * time.Second}
			response, err := client.Get("http://[fd00::1]:8080/api/items")
			if enabled {
				if err != nil {
					t.Fatalf("opted-in GET error = %v", err)
				}
				body, _ := io.ReadAll(response.Body)
				response.Body.Close()
				if response.StatusCode != http.StatusOK || string(body) != "upstream:/api/items" || response.Header.Get("X-Upstream-Proxy") != "yes" {
					t.Fatalf("opted-in response = %d %q %#v", response.StatusCode, body, response.Header)
				}
				if hosts := seenHosts(); len(hosts) != 1 || !strings.Contains(hosts[0], "fd00::1") {
					t.Fatalf("upstream proxy saw %#v, want the private target", hosts)
				}
				return
			}
			if err == nil {
				body, _ := io.ReadAll(response.Body)
				response.Body.Close()
				if response.Header.Get("X-Upstream-Proxy") == "yes" || strings.Contains(string(body), "upstream:/api/items") {
					t.Fatalf("opted-out request was proxied upstream: %d %q %#v", response.StatusCode, body, response.Header)
				}
			}
			if hosts := seenHosts(); len(hosts) != 0 {
				t.Fatalf("upstream proxy saw %#v without the opt-in, want nothing", hosts)
			}
		})
	}
}
