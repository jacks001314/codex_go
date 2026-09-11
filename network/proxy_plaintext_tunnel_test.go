package network

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	xproxy "golang.org/x/net/proxy"
)

func TestCredentialDestinationHostRequiresHTTPInterception(t *testing.T) {
	provider := ConfiguredCredentialProvider("vendor", CredentialProviderConfig{
		Env:         []string{"VENDOR_TOKEN"},
		Patterns:    []string{"vk-[a-z0-9]{16}"},
		URLPrefixes: []string{"http://127.0.0.1:8080"},
		Auth:        []CredentialAuthMethod{CredentialAuthBearer},
	})
	broker := NewProxyCredentialBrokerWithProviders(true, []*ProxyCredentialProvider{provider})
	if !broker.HostRequiresHTTPInterception("127.0.0.1", 8080) {
		t.Fatal("plaintext destination did not require HTTP interception")
	}
	if broker.HostRequiresHTTPInterception("127.0.0.1", 8081) {
		t.Fatal("wrong port required HTTP interception")
	}
	if broker.HostRequiresHTTPInterception("other.example", 8080) {
		t.Fatal("wrong host required HTTP interception")
	}
	if broker.HostRequiresMITM("127.0.0.1") {
		t.Fatal("plaintext destination must not require TLS MITM")
	}
}

func TestPlaintextTunnelRequestAllowedLikeRust(t *testing.T) {
	const host = "127.0.0.1"
	const port = 8080
	allowed := []byte("GET /models HTTP/1.1\r\nHost: 127.0.0.1:8080\r\n\r\n")
	if !plaintextTunnelRequestAllowed(allowed, host, port) {
		t.Fatal("plain HTTP/1.1 request should be brokerable")
	}
	upgrade := []byte("GET /ws HTTP/1.1\r\nHost: 127.0.0.1:8080\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
	if kind, ok := classifyPlaintextTunnelRequest(upgrade, host, port); !ok || kind != plaintextTunnelRequestUpgrade {
		t.Fatalf("upgrade classification = %v/%v, want upgrade", kind, ok)
	}
	if plaintextTunnelRequestAllowed(upgrade, host, port) {
		t.Fatal("upgrade request is not a plain request")
	}
	cases := map[string][]byte{
		"h2 preface": []byte("PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"),
		"connect":    []byte("CONNECT 127.0.0.1:8080 HTTP/1.1\r\nHost: 127.0.0.1:8080\r\n\r\n"),
		"other host": []byte("GET / HTTP/1.1\r\nHost: evil.example\r\n\r\n"),
		"wrong port": []byte("GET / HTTP/1.1\r\nHost: 127.0.0.1:9999\r\n\r\n"),
		"not http":   []byte("garbage\r\n\r\n"),
	}
	for name, header := range cases {
		if plaintextTunnelRequestAllowed(header, host, port) {
			t.Fatalf("%s should not be brokerable", name)
		}
	}
}

// TestProxyBrokersPlaintextHTTPInsideCONNECTTunnelLikeRust is the end-to-end
// check for #44089/#44077: a configured loopback HTTP destination is
// intercepted as plaintext HTTP inside a CONNECT tunnel and the real credential
// is substituted for the dummy.
func TestProxyBrokersPlaintextHTTPInsideCONNECTTunnelLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	const realValue = "vk-0123456789abcdef"
	seen := make(chan string, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		seen <- request.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	upstreamHost, upstreamPort, err := net.SplitHostPort(upstreamURL.Host)
	if err != nil {
		t.Fatal(err)
	}
	upstreamPortNumber, err := parseCredentialPort(upstreamPort)
	if err != nil {
		t.Fatal(err)
	}

	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.MITM = true
	settings.CredentialBroker = true
	settings.EnableSocks5 = false
	settings.ProxyURL = "http://127.0.0.1:0"
	settings.SetAllowedDomains([]string{upstreamHost})
	settings.CredentialProviders = map[string]CredentialProviderConfig{
		"vendor": {
			Env:         []string{"VENDOR_TOKEN"},
			Patterns:    []string{"vk-[a-z0-9]{16}"},
			URLPrefixes: []string{upstreamURL.Scheme + "://" + upstreamURL.Host},
			Auth:        []CredentialAuthMethod{CredentialAuthBearer},
		},
	}
	prepared, err := StartProxyManagedNetwork(context.Background(), ProxyConfig{Network: settings}, map[string]string{"VENDOR_TOKEN": realValue})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	dummy := prepared.Env["VENDOR_TOKEN"]
	if dummy == "" || dummy == realValue {
		t.Fatalf("virtualized env = %q", dummy)
	}
	if !prepared.server.runtimePolicy().broker.HostRequiresHTTPInterception(upstreamHost, upstreamPortNumber) {
		t.Fatal("broker did not require plaintext HTTP interception")
	}

	proxyURL, err := url.Parse(prepared.Env["HTTP_PROXY"])
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", proxyURL.Host, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	reader := bufio.NewReader(conn)
	target := net.JoinHostPort(upstreamHost, upstreamPort)
	if _, err := io.WriteString(conn, "CONNECT "+target+" HTTP/1.1\r\nHost: "+target+"\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	statusLine, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(statusLine, "200") {
		t.Fatalf("CONNECT response = %q, %v", statusLine, err)
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
	if _, err := io.WriteString(conn, "GET /v1/models HTTP/1.1\r\nHost: "+target+"\r\nAuthorization: Bearer "+dummy+"\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("response status = %d", response.StatusCode)
	}
	if authorization := <-seen; authorization != "Bearer "+realValue {
		t.Fatalf("plaintext tunnel credential = %q, want real value", authorization)
	}
}

// TestProxyBrokersPlaintextHTTPOverSOCKS5LikeRust covers the SOCKS5 half of the
// brokered plaintext tunnel (#44089/#44077).
func TestProxyBrokersPlaintextHTTPOverSOCKS5LikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	const realValue = "vk-0123456789abcdef"
	seen := make(chan string, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		seen <- request.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	upstreamHost, upstreamPort, err := net.SplitHostPort(upstreamURL.Host)
	if err != nil {
		t.Fatal(err)
	}
	upstreamPortNumber, err := parseCredentialPort(upstreamPort)
	if err != nil {
		t.Fatal(err)
	}

	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.MITM = true
	settings.CredentialBroker = true
	settings.ProxyURL = "http://127.0.0.1:0"
	settings.SocksURL = "http://127.0.0.1:0"
	settings.SetAllowedDomains([]string{upstreamHost})
	settings.CredentialProviders = map[string]CredentialProviderConfig{
		"vendor": {
			Env:         []string{"VENDOR_TOKEN"},
			Patterns:    []string{"vk-[a-z0-9]{16}"},
			URLPrefixes: []string{upstreamURL.Scheme + "://" + upstreamURL.Host},
			Auth:        []CredentialAuthMethod{CredentialAuthBearer},
		},
	}
	prepared, err := StartProxyManagedNetwork(context.Background(), ProxyConfig{Network: settings}, map[string]string{"VENDOR_TOKEN": realValue})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	dummy := prepared.Env["VENDOR_TOKEN"]
	if dummy == "" || dummy == realValue {
		t.Fatalf("virtualized env = %q", dummy)
	}
	if !prepared.server.runtimePolicy().broker.HostRequiresHTTPInterception(upstreamHost, upstreamPortNumber) {
		t.Fatal("broker did not require plaintext HTTP interception")
	}
	socksAddress := strings.TrimPrefix(prepared.Env["ALL_PROXY"], "socks5h://")
	dialer, err := xproxy.SOCKS5("tcp", socksAddress, nil, &net.Dialer{Timeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{
		DialContext: func(_ context.Context, network, address string) (net.Conn, error) {
			return dialer.Dial(network, address)
		},
		DisableKeepAlives: true,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	request, _ := http.NewRequest(http.MethodGet, "http://"+upstreamURL.Host+"/v1/models", nil)
	request.Header.Set("Authorization", "Bearer "+dummy)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("response status = %d", response.StatusCode)
	}
	if authorization := <-seen; authorization != "Bearer "+realValue {
		t.Fatalf("SOCKS5 plaintext tunnel credential = %q, want real value", authorization)
	}
}
