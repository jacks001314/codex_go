package network

import "testing"

func TestProxyConfigFromEnvAndSelection(t *testing.T) {
	env := map[string]string{
		"HTTP_PROXY":  "http://user:pass@proxy.local:8080",
		"HTTPS_PROXY": "https://secure.local:8443",
		"ALL_PROXY":   "socks5://ignored.local:1080",
	}
	proxyConfig := ProxyUpstreamConfigFromEnv(env)
	if proxyConfig.HTTP == nil || proxyConfig.HTTP.Host != "proxy.local" || proxyConfig.HTTP.Username != "user" || proxyConfig.HTTP.Password != "pass" {
		t.Fatalf("http proxy = %#v", proxyConfig.HTTP)
	}
	if got := (&proxyConfig).ProxyForProtocol(true); got == nil || got.Host != "secure.local" {
		t.Fatalf("secure proxy = %#v", got)
	}
	if got := (&proxyConfig).ProxyForProtocol(false); got == nil || got.Host != "proxy.local" {
		t.Fatalf("plain proxy = %#v", got)
	}
}

func TestProxyForConnectFallbackOrder(t *testing.T) {
	env := map[string]string{
		"HTTP_PROXY": "proxy.local:8080",
		"ALL_PROXY":  "http://all.local:8080",
	}
	got := ProxyAddressForConnect(env)
	if got == nil || got.Host != "proxy.local" || got.Scheme != "http" {
		t.Fatalf("connect proxy = %#v", got)
	}
	env = map[string]string{"ALL_PROXY": "http://all.local:8080"}
	got = ProxyAddressForConnect(env)
	if got == nil || got.Host != "all.local" {
		t.Fatalf("all proxy = %#v", got)
	}
}

func TestReadProxyEnvIgnoresNonHTTP(t *testing.T) {
	env := map[string]string{
		"HTTPS_PROXY": "socks5://proxy.local:1080",
		"ALL_PROXY":   "http://all.local:8080",
	}
	if got := ReadProxyAddressEnv(env, []string{"HTTPS_PROXY"}); got != nil {
		t.Fatalf("non-http proxy should be ignored: %#v", got)
	}
	proxyConfig := ProxyUpstreamConfigFromEnv(env)
	if got := (&proxyConfig).ProxyForProtocol(true); got == nil || got.Host != "all.local" {
		t.Fatalf("secure fallback = %#v", got)
	}
}
