package network

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestPrivateIPUpstreamRoutingIsOptInLikeRust ports the routing table of the
// Rust test `network-proxy/src/upstream_tests.rs::private_ip_upstream_routing_is_opt_in`
// (Rust #48568): permitted RFC 1918, carrier-grade NAT, and IPv6 unique-local
// destinations only use the upstream proxy when the executor opted in, while
// loopback, link-local, and other special-use destinations always stay direct.
// It drives the real `http.Transport.Proxy` closure (and the CONNECT gate that
// shares `mayUseUpstreamProxyForAddress`) rather than a helper stand-in.
func TestPrivateIPUpstreamRoutingIsOptInLikeRust(t *testing.T) {
	const upstreamProxy = "http://127.0.0.1:43128"
	for _, enabled := range []bool{false, true} {
		settings := DefaultProxySettings()
		settings.Enabled = true
		settings.ProxyURL = "http://127.0.0.1:0"
		settings.EnableSocks5 = false
		settings.AllowUpstreamProxy = true
		settings.AllowLocalBinding = true
		prepared, err := StartProxyManagedNetwork(context.Background(), ProxyConfig{
			Network:               settings,
			PrivateIPsViaUpstream: enabled,
		}, map[string]string{"HTTP_PROXY": upstreamProxy, "HTTPS_PROXY": upstreamProxy})
		if err != nil {
			t.Fatalf("StartProxyManagedNetwork() error = %v", err)
		}
		cases := []struct {
			host     string
			useProxy bool
		}{
			{"10.0.0.1", enabled},
			{"172.16.0.1", enabled},
			{"192.168.0.1", enabled},
			{"100.68.58.50", enabled},
			{"fd00::1", enabled},
			{"::ffff:100.68.58.50", enabled},
			{"localhost", false},
			{"127.0.0.1", false},
			{"::1", false},
			{"169.254.169.254", false},
			{"fe80::1", false},
			{"0.0.0.0", false},
			{"224.0.0.1", false},
			{"192.0.2.1", false},
			{"240.0.0.1", false},
			{"example.com", true},
			{"100.63.255.255", true},
			{"100.128.0.0", true},
		}
		for _, testCase := range cases {
			address := net.JoinHostPort(testCase.host, "443")
			for _, scheme := range []string{"http", "https"} {
				request := &http.Request{URL: &url.URL{Scheme: scheme, Host: address}}
				got, err := prepared.server.httpProxy.Tr.Proxy(request)
				if err != nil {
					t.Fatalf("Proxy(%s) error = %v", address, err)
				}
				if (got != nil) != testCase.useProxy {
					t.Fatalf("host=%s enabled=%v secure=%v: upstream proxy = %v, want routed=%v", testCase.host, enabled, scheme == "https", got, testCase.useProxy)
				}
			}
			// The CONNECT path shares the same decision (Rust proxy_for_connect).
			if got := prepared.server.mayUseUpstreamProxyForAddress(address); got != testCase.useProxy {
				t.Fatalf("host=%s enabled=%v: mayUseUpstreamProxyForAddress = %v, want %v", testCase.host, enabled, got, testCase.useProxy)
			}
		}
		if err := prepared.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}
}

// TestPrivateIPUpstreamRoutingEndToEndLikeRust is the wiring check for
// Rust #48568: with the opt-in, a permitted private destination is really
// forwarded through the configured upstream proxy; without it the same request
// never reaches that proxy and is dialed directly.
func TestPrivateIPUpstreamRoutingEndToEndLikeRust(t *testing.T) {
	const target = "http://[fd00::1]:8080/api/items"
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

	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.ProxyURL = "http://127.0.0.1:0"
	settings.EnableSocks5 = false
	settings.AllowUpstreamProxy = true
	settings.AllowLocalBinding = true
	settings.SetAllowedDomains([]string{"fd00::1"})

	// Opted in: the private destination travels through the upstream proxy.
	prepared, err := StartProxyManagedNetwork(context.Background(), ProxyConfig{
		Network:               settings,
		PrivateIPsViaUpstream: true,
	}, map[string]string{"HTTP_PROXY": upstream.URL, "HTTPS_PROXY": upstream.URL})
	if err != nil {
		t.Fatalf("StartProxyManagedNetwork() error = %v", err)
	}
	proxyURL, err := url.Parse(prepared.EnvSnapshot()["HTTP_PROXY"])
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 5 * time.Second}
	response, err := client.Get(target)
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
	if err := prepared.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// Opted out: the upstream proxy must never see the private destination.
	prepared, err = StartProxyManagedNetwork(context.Background(), ProxyConfig{Network: settings}, map[string]string{"HTTP_PROXY": upstream.URL, "HTTPS_PROXY": upstream.URL})
	if err != nil {
		t.Fatalf("StartProxyManagedNetwork() error = %v", err)
	}
	defer prepared.Close()
	proxyURL, err = url.Parse(prepared.EnvSnapshot()["HTTP_PROXY"])
	if err != nil {
		t.Fatal(err)
	}
	client = &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 3 * time.Second}
	response, err = client.Get(target)
	if err == nil {
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.Header.Get("X-Upstream-Proxy") == "yes" {
			t.Fatalf("opted-out request was proxied upstream: %d %#v", response.StatusCode, response.Header)
		}
	}
	if hosts := seenHosts(); len(hosts) != 1 {
		t.Fatalf("upstream proxy saw %#v after the opted-out request, want the single opted-in request", hosts)
	}
}

// TestPrivateIPUpstreamRoutingPreservesDestinationPolicyLikeRust ports the Rust
// test `network-proxy/src/http_proxy.rs::private_ip_upstream_routing_preserves_destination_policy`
// (Rust #48568): allowlists and denylists still decide access once private-IP
// upstream routing is enabled, for both plain HTTP and CONNECT.
func TestPrivateIPUpstreamRoutingPreservesDestinationPolicyLikeRust(t *testing.T) {
	const target = "100.68.58.50"
	for _, testCase := range []struct {
		name          string
		allowlisted   bool
		denied        bool
		expectedError string
	}{
		{name: "denylist", allowlisted: true, denied: true, expectedError: "blocked-by-denylist"},
		{name: "allowlist", allowlisted: false, denied: false, expectedError: "blocked-by-allowlist"},
	} {
		settings := DefaultProxySettings()
		settings.Enabled = true
		settings.ProxyURL = "http://127.0.0.1:0"
		settings.EnableSocks5 = false
		settings.AllowUpstreamProxy = true
		settings.AllowLocalBinding = true
		if testCase.allowlisted {
			settings.SetAllowedDomains([]string{target})
		}
		if testCase.denied {
			settings.SetDeniedDomains([]string{target})
		}
		prepared, err := StartProxyManagedNetwork(context.Background(), ProxyConfig{
			Network:               settings,
			PrivateIPsViaUpstream: true,
		}, nil)
		if err != nil {
			t.Fatalf("StartProxyManagedNetwork() error = %v", err)
		}
		proxyURL, err := url.Parse(prepared.EnvSnapshot()["HTTP_PROXY"])
		if err != nil {
			t.Fatal(err)
		}

		// Plain HTTP.
		client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 3 * time.Second}
		response, err := client.Get("http://" + target + ":80/api/items")
		if err != nil {
			t.Fatalf("%s GET error = %v", testCase.name, err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusForbidden || response.Header.Get("X-Proxy-Error") != testCase.expectedError {
			t.Fatalf("%s GET response = %d %#v", testCase.name, response.StatusCode, response.Header)
		}

		// CONNECT.
		conn, err := net.DialTimeout("tcp", proxyURL.Host, 2*time.Second)
		if err != nil {
			t.Fatalf("%s CONNECT dial error = %v", testCase.name, err)
		}
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		if _, err := fmt.Fprintf(conn, "CONNECT %s:80 HTTP/1.1\r\nHost: %s:80\r\n\r\n", target, target); err != nil {
			t.Fatalf("%s CONNECT write error = %v", testCase.name, err)
		}
		reader := bufio.NewReader(conn)
		status, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("%s CONNECT status error = %v", testCase.name, err)
		}
		headers := map[string]string{}
		for {
			line, readErr := reader.ReadString('\n')
			if readErr != nil {
				t.Fatalf("%s CONNECT header error = %v", testCase.name, readErr)
			}
			if line == "\r\n" || line == "\n" {
				break
			}
			if key, value, ok := strings.Cut(strings.TrimSpace(line), ":"); ok {
				headers[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
			}
		}
		_ = conn.Close()
		if !strings.Contains(status, " 403 ") || headers["x-proxy-error"] != testCase.expectedError {
			t.Fatalf("%s CONNECT response = %q %#v", testCase.name, status, headers)
		}
		if err := prepared.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}
}
