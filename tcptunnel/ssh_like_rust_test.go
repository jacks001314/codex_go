package tcptunnel

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSSHDestinationRoutesOAuthAndLegacyLikeRust mirrors Rust's
// `routes_oauth_and_legacy_without_ambiguous_authority`: the OAuth route names
// the environment and published port, the legacy route carries no extension
// headers, and the bearer is always present.
func TestSSHDestinationRoutesOAuthAndLegacyLikeRust(t *testing.T) {
	oauth, err := ParseArgs([]string{"--environment-id", "ccarenv_test", "--port", "2222"})
	if err != nil {
		t.Fatalf("ParseArgs(oauth) error = %v", err)
	}
	destination, err := oauth.sshDestination()
	if err != nil {
		t.Fatalf("sshDestination(oauth) error = %v", err)
	}
	bearer, err := oauthBearer("fixture==")
	if err != nil {
		t.Fatalf("oauthBearer() error = %v", err)
	}
	request := destination.Target.request(requestSpec{headers: destination.Headers, bearer: bearer})
	if request.Method != http.MethodConnect {
		t.Fatalf("method = %q, want CONNECT", request.Method)
	}
	if request.Host != "codex-portals.api.openai.com:443" {
		t.Fatalf("authority = %q", request.Host)
	}
	if got := request.Header.Get("x-portals-environment-id"); got != "ccarenv_test" {
		t.Fatalf("environment header = %q", got)
	}
	if got := request.Header.Get("x-portals-port"); got != "2222" {
		t.Fatalf("port header = %q", got)
	}
	if got := request.Header.Get("x-portals-auth-mode"); got != "oauth" {
		t.Fatalf("auth mode header = %q", got)
	}
	if got := request.Header.Get("Authorization"); got != "Bearer fixture==" {
		t.Fatalf("authorization = %q", got)
	}

	defaults, err := ParseArgs([]string{"--environment-id", "env"})
	if err != nil {
		t.Fatalf("ParseArgs(defaults) error = %v", err)
	}
	destination, err = defaults.sshDestination()
	if err != nil {
		t.Fatalf("sshDestination(defaults) error = %v", err)
	}
	if got := destination.Headers.Get("x-portals-port"); got != "22" {
		t.Fatalf("default port header = %q", got)
	}

	legacy, err := ParseArgs([]string{"--token-file", "/private/token", "--target", "10.0.0.1:22"})
	if err != nil {
		t.Fatalf("ParseArgs(legacy) error = %v", err)
	}
	destination, err = legacy.sshDestination()
	if err != nil {
		t.Fatalf("sshDestination(legacy) error = %v", err)
	}
	if destination.Target.Authority != "10.0.0.1:22" || len(destination.Headers) != 0 {
		t.Fatalf("legacy destination = %#v", destination)
	}

	ipv6, err := ParseArgs([]string{"--token-file", "f", "--target", "[::1]:22"})
	if err != nil {
		t.Fatalf("ParseArgs(ipv6) error = %v", err)
	}
	if _, err := ipv6.sshDestination(); err == nil || err.Error() != "invalid legacy CONNECT target" {
		t.Fatalf("sshDestination(ipv6) error = %v", err)
	}

	zero, err := ParseArgs([]string{"--token-file", "f", "--target", "10.0.0.1:0"})
	if err != nil {
		t.Fatalf("ParseArgs(zero) error = %v", err)
	}
	if _, err := zero.sshDestination(); err == nil || err.Error() != "invalid legacy CONNECT target" {
		t.Fatalf("sshDestination(zero port) error = %v", err)
	}
}

// TestOAuthBearerMatchesRust checks the token charset, the trailing-padding
// allowance and the errors that must never echo the credential.
func TestOAuthBearerMatchesRust(t *testing.T) {
	for _, token := range []string{"", "=", "private=secret", "private\r\nvalue", "private secret"} {
		if _, err := oauthBearer(token); err == nil {
			t.Fatalf("oauthBearer(%q) returned no error", token)
		} else if strings.Contains(err.Error(), token) && token != "" {
			t.Fatalf("oauthBearer(%q) error echoes the token: %v", token, err)
		}
	}
	bearer, err := oauthBearer("fixture==")
	if err != nil || bearer != "Bearer fixture==" {
		t.Fatalf("oauthBearer(valid) = %q, %v", bearer, err)
	}
}

// TestSafeTunnelErrorSanitizesLikeRust keeps only the HTTP status of a rejected
// CONNECT.
func TestSafeTunnelErrorSanitizesLikeRust(t *testing.T) {
	sanitized := safeTunnelError(errors.New("peer supplied secret"))
	if strings.Contains(sanitized.Error(), "secret") {
		t.Fatalf("safeTunnelError leaked peer text: %v", sanitized)
	}
	if got := safeTunnelError(rejection(403)).Error(); !strings.Contains(got, "403") {
		t.Fatalf("safeTunnelError(rejection) = %q", got)
	}
	if got := safeTunnelError(tunnelTimeout()).Error(); got != "SSH tunnel setup timed out" {
		t.Fatalf("safeTunnelError(timeout) = %q", got)
	}
	if err := safeTunnelError(nil); err != nil {
		t.Fatalf("safeTunnelError(nil) = %v", err)
	}
}

// TestReadTokenFileIsOneUseLikeRust mirrors Rust's private token-file contract:
// bounded size, regular file, removed before the token is returned.
func TestReadTokenFileIsOneUseLikeRust(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "token")
	if err := os.WriteFile(path, []byte("fixture=="), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	token, err := readTokenFile(path)
	if err != nil {
		t.Fatalf("readTokenFile() error = %v", err)
	}
	if token != "fixture==" {
		t.Fatalf("token = %q", token)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("token file still exists after read: %v", err)
	}

	oversized := filepath.Join(directory, "oversized")
	if err := os.WriteFile(oversized, bytes.Repeat([]byte("a"), MaxTokenBytes+1), 0o600); err != nil {
		t.Fatalf("WriteFile(oversized) error = %v", err)
	}
	if _, err := readTokenFile(oversized); err == nil || err.Error() != "invalid private token file" {
		t.Fatalf("readTokenFile(oversized) error = %v", err)
	}
	// Rust opens the handle first; on Windows a directory cannot be opened the
	// same way, so the failure surfaces while opening instead of in metadata.
	if _, err := readTokenFile(directory); err == nil {
		t.Fatalf("readTokenFile(directory) returned no error")
	} else if err.Error() != "invalid private token file" && !strings.Contains(err.Error(), "opening private token file") {
		t.Fatalf("readTokenFile(directory) error = %v", err)
	}
	if _, err := readTokenFile(filepath.Join(directory, "missing")); err == nil || !strings.Contains(err.Error(), "opening private token file") {
		t.Fatalf("readTokenFile(missing) error = %v", err)
	}
}

// TestRunSSHProxyRefusesBeforeLoadingCredentials mirrors Rust's
// `rejects_bad_origin_and_route_before_loading_credentials`.
func TestRunSSHProxyRefusesBeforeLoadingCredentials(t *testing.T) {
	cases := [][]string{
		{"--environment-id", "bad\r\nvalue"},
		{"--environment-id", "valid", "--proxy-url", "https://codex-portals.api.openai.com.attacker.net"},
		{"--environment-id", "valid", "--proxy-url", "https://codex-portals.api.openai.com:4443"},
	}
	for _, argv := range cases {
		args, err := ParseArgs(argv)
		if err != nil {
			t.Fatalf("ParseArgs(%v) error = %v", argv, err)
		}
		lookups := 0
		var stdout bytes.Buffer
		err = RunSSHProxy(context.Background(), args, func(context.Context) (string, error) {
			lookups++
			return "fixture", nil
		}, strings.NewReader(""), &stdout)
		if err == nil {
			t.Fatalf("RunSSHProxy(%v) returned no error", argv)
		}
		if lookups != 0 {
			t.Fatalf("RunSSHProxy(%v) loaded credentials before route validation", argv)
		}
		if stdout.Len() != 0 {
			t.Fatalf("RunSSHProxy(%v) wrote %q", argv, stdout.String())
		}
	}
}
