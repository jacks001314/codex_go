package tcptunnel

import (
	"net/url"
	"testing"
)

// TestProxyPolicyAndTargetAdmission mirrors Rust's
// `proxy_policy_and_target_admission` (tcp-tunnel/src/lib_tests.rs).
func TestProxyPolicyAndTargetAdmission(t *testing.T) {
	origins, err := ParseTrustedOrigins("\nhttps://proxy.example.org\n")
	if err != nil {
		t.Fatalf("ParseTrustedOrigins() error = %v", err)
	}
	if len(origins) != 1 || origins[0] != "https://proxy.example.org" {
		t.Fatalf("ParseTrustedOrigins() = %#v", origins)
	}
	proxy, err := url.Parse("https://proxy.example.org")
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	for _, target := range []string{"127.0.0.1:22", "[::1]:2222", "target.example:443"} {
		parsed, err := ParseProxyTarget(proxy, origins, target)
		if err != nil {
			t.Fatalf("ParseProxyTarget(%q) error = %v", target, err)
		}
		want := ProxyTarget{Host: "proxy.example.org", Port: 443, Authority: target}
		if *parsed != want {
			t.Fatalf("ParseProxyTarget(%q) = %#v, want %#v", target, *parsed, want)
		}
	}

	const ipv6 = "https://[::1]:8443"
	ipv6Proxy, err := url.Parse(ipv6)
	if err != nil {
		t.Fatalf("url.Parse(%q) error = %v", ipv6, err)
	}
	ipv6Origins, err := ParseTrustedOrigins(ipv6)
	if err != nil {
		t.Fatalf("ParseTrustedOrigins(%q) error = %v", ipv6, err)
	}
	parsed, err := ParseProxyTarget(ipv6Proxy, ipv6Origins, "target.example:22")
	if err != nil {
		t.Fatalf("ParseProxyTarget(ipv6) error = %v", err)
	}
	if want := (ProxyTarget{Host: "::1", Port: 8443, Authority: "target.example:22"}); *parsed != want {
		t.Fatalf("ParseProxyTarget(ipv6) = %#v, want %#v", *parsed, want)
	}

	for _, origin := range []string{
		"http://proxy.example.org",
		"https://proxy.example.org/path",
		"https://user@proxy.example.org",
		"https://proxy.example.org?query",
		"https://proxy.example.org#fragment",
	} {
		if _, err := ParseTrustedOrigins(origin); err == nil {
			t.Fatalf("ParseTrustedOrigins(%q) succeeded, want error", origin)
		}
		parsed, err := url.Parse(origin)
		if err != nil {
			continue
		}
		if _, err := ParseProxyTarget(parsed, origins, "127.0.0.1:22"); err == nil {
			t.Fatalf("ParseProxyTarget(%q) succeeded, want error", origin)
		}
	}

	for _, origin := range []string{"", "https://proxy.example.org:443", "https://proxy.example.org/"} {
		if _, err := ParseTrustedOrigins(origin); err == nil {
			t.Fatalf("ParseTrustedOrigins(%q) succeeded, want error", origin)
		}
	}

	for _, tc := range []struct{ proxy, target string }{
		{"https://evil.example", "127.0.0.1:22"},
		{"https://proxy.example.org.attacker.net", "127.0.0.1:22"},
		{"https://proxy.example.org", "127.0.0.1:0"},
		{"https://proxy.example.org", "target.example"},
		{"https://proxy.example.org", "user@target.example:22"},
		{"https://proxy.example.org", "target.example:65536"},
		{"https://proxy.example.org", "target.example:22/path"},
	} {
		parsed, err := url.Parse(tc.proxy)
		if err != nil {
			t.Fatalf("url.Parse(%q) error = %v", tc.proxy, err)
		}
		if _, err := ParseProxyTarget(parsed, origins, tc.target); err == nil {
			t.Fatalf("ParseProxyTarget(%q, %q) succeeded, want error", tc.proxy, tc.target)
		}
	}
}

func TestParseArgsMirrorsClapRequirements(t *testing.T) {
	args, err := ParseArgs([]string{
		"--proxy-url", "https://proxy.example.org",
		"--proxy-origins-file", "origins.txt",
		"--target=127.0.0.1:22",
		"--auth-token-stdin",
		"--auth-token-updates-stdin",
		"--connect-headers-stdin",
	})
	if err != nil {
		t.Fatalf("ParseArgs() error = %v", err)
	}
	if args.ListenAddr != DefaultListenAddr {
		t.Fatalf("ListenAddr = %q, want %q", args.ListenAddr, DefaultListenAddr)
	}
	if !args.AuthTokenStdin || !args.AuthTokenUpdatesStdin || !args.ConnectHeadersStdin {
		t.Fatalf("ParseArgs() = %#v", args)
	}

	for _, argv := range [][]string{
		{"--proxy-origins-file", "origins.txt", "--target", "127.0.0.1:22"},   // missing --proxy-url
		{"--proxy-url", "https://p.example", "--target", "127.0.0.1:22"},      // missing --proxy-origins-file
		{"--proxy-url", "https://p.example", "--proxy-origins-file", "o.txt"}, // missing --target
		{"--proxy-url", "https://p.example", "--proxy-origins-file", "o.txt", "--target", "127.0.0.1:22", "--auth-token-updates-stdin"},
		{"--proxy-url", "https://p.example", "--proxy-origins-file", "o.txt", "--target", "127.0.0.1:22", "--connect-headers-stdin"},
		{"--proxy-url", "https://p.example", "--proxy-origins-file", "o.txt", "--target", "127.0.0.1:22", "--listen-addr", "not-an-addr"},
		{"--bogus"},
	} {
		if _, err := ParseArgs(argv); err == nil {
			t.Fatalf("ParseArgs(%v) succeeded, want error", argv)
		}
	}
}
