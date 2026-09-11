package network

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func startPlaintextUpstream(t *testing.T) (string, chan string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	seen := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		request, err := http.ReadRequest(reader)
		if err != nil {
			return
		}
		seen <- request.Header.Get("Authorization")
		_, _ = io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		_, _ = io.Copy(conn, reader)
	}()
	return listener.Addr().String(), seen, func() { _ = listener.Close() }
}

func dialTunnel(t *testing.T, proxyURL string, target string) (net.Conn, *bufio.Reader) {
	t.Helper()
	parsed, err := url.Parse(proxyURL)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", parsed.Host, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	reader := bufio.NewReader(conn)
	if _, err := io.WriteString(conn, "CONNECT "+target+" HTTP/1.1\r\nHost: "+target+"\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	// Read the CONNECT response line and headers by hand: http.ReadResponse
	// would treat the tunnel payload as the response body.
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
	return conn, reader
}

func configuredPlaintextSettings(host string, schemeHost string, mode ProxyMode) ProxySettings {
	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.MITM = true
	settings.CredentialBroker = true
	settings.EnableSocks5 = false
	settings.ProxyURL = "http://127.0.0.1:0"
	settings.Mode = mode
	settings.SetAllowedDomains([]string{host})
	settings.CredentialProviders = map[string]CredentialProviderConfig{
		"vendor": {
			Env:         []string{"VENDOR_TOKEN"},
			Patterns:    []string{"vk-[a-z0-9]{16}"},
			URLPrefixes: []string{schemeHost},
			Auth:        []CredentialAuthMethod{CredentialAuthBearer},
		},
	}
	return settings
}

// TestProxyBrokersPlaintextUpgradeInsideCONNECTTunnelLikeRust covers #44089's
// upgraded-connection support: the handshake request is credential-substituted
// once, then the upgraded connection is relayed in both directions.
func TestProxyBrokersPlaintextUpgradeInsideCONNECTTunnelLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	const realValue = "vk-0123456789abcdef"
	upstreamAddress, seen, closeUpstream := startPlaintextUpstream(t)
	defer closeUpstream()
	upstreamURL, err := url.Parse("http://" + upstreamAddress)
	if err != nil {
		t.Fatal(err)
	}
	settings := configuredPlaintextSettings(upstreamURL.Hostname(), upstreamURL.Scheme+"://"+upstreamURL.Host, ProxyModeFull)
	prepared, err := StartProxyManagedNetwork(context.Background(), ProxyConfig{Network: settings}, map[string]string{"VENDOR_TOKEN": realValue})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	dummy := prepared.Env["VENDOR_TOKEN"]

	conn, reader := dialTunnel(t, prepared.Env["HTTP_PROXY"], upstreamAddress)
	defer conn.Close()
	if _, err := io.WriteString(conn, "GET /ws HTTP/1.1\r\nHost: "+upstreamAddress+"\r\nAuthorization: Bearer "+dummy+"\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade status = %d", response.StatusCode)
	}
	if authorization := <-seen; authorization != "Bearer "+realValue {
		t.Fatalf("upgrade handshake credential = %q, want real value", authorization)
	}
	if _, err := io.WriteString(conn, "ping"); err != nil {
		t.Fatal(err)
	}
	echo := make([]byte, 4)
	if _, err := io.ReadFull(reader, echo); err != nil {
		t.Fatal(err)
	}
	if string(echo) != "ping" {
		t.Fatalf("upgraded echo = %q", echo)
	}
}

// TestProxyLimitedModeRejectsOpaqueBrokeredTunnelLikeRust covers #44089's
// limited-mode rule: non-HTTP bytes through a brokered destination are rejected
// instead of being relayed unbrokered.
func TestProxyLimitedModeRejectsOpaqueBrokeredTunnelLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	const realValue = "vk-0123456789abcdef"
	upstreamAddress, _, closeUpstream := startPlaintextUpstream(t)
	defer closeUpstream()
	upstreamURL, err := url.Parse("http://" + upstreamAddress)
	if err != nil {
		t.Fatal(err)
	}
	settings := configuredPlaintextSettings(upstreamURL.Hostname(), upstreamURL.Scheme+"://"+upstreamURL.Host, ProxyModeLimited)
	prepared, err := StartProxyManagedNetwork(context.Background(), ProxyConfig{Network: settings}, map[string]string{"VENDOR_TOKEN": realValue})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()

	conn, reader := dialTunnel(t, prepared.Env["HTTP_PROXY"], upstreamAddress)
	defer conn.Close()
	if _, err := io.WriteString(conn, "not-http-at-all\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatalf("limited-mode rejection response error = %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("limited-mode opaque status = %d, want 403", response.StatusCode)
	}
}
