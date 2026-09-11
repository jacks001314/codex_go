package network

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// TestProxyBrokersPlaintextH2CInsideCONNECTTunnelLikeRust covers #44089's
// HTTP/2-cleartext support: an h2c client tunnel is served as HTTP/2 and each
// request is forwarded with URL-scoped credential substitution.
func TestProxyBrokersPlaintextH2CInsideCONNECTTunnelLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	const realValue = "vk-0123456789abcdef"
	seen := make(chan string, 2)
	handler := http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		seen <- request.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	})
	upstream := httptest.NewUnstartedServer(handler)
	upstream.Config.Handler = h2c.NewHandler(handler, &http2.Server{})
	upstream.Start()
	defer upstream.Close()
	upstreamURL, err := url.Parse(upstream.URL)
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

	transport := &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(_ context.Context, network, _ string, _ *tls.Config) (net.Conn, error) {
			conn, reader := dialTunnel(t, prepared.Env["HTTP_PROXY"], upstreamURL.Host)
			return &proxyBufferedConn{Conn: conn, reader: reader}, nil
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	request, _ := http.NewRequest(http.MethodGet, upstream.URL+"/v1/models", nil)
	request.Header.Set("Authorization", "Bearer "+dummy)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("h2c status = %d", response.StatusCode)
	}
	if authorization := <-seen; authorization != "Bearer "+realValue {
		t.Fatalf("h2c credential = %q, want real value", authorization)
	}
}
