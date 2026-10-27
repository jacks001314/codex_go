package network

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/elazarl/goproxy"
)

// TestMITMRequestSignsAWSPlaceholderLikeRust mirrors
// aws_sigv4_tests.rs: a MITM hook re-signs an eligible placeholder request after
// policy authorization and returns the signed request for forwarding.
func TestMITMRequestSignsAWSPlaceholderLikeRust(t *testing.T) {
	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.ProxyURL = "http://127.0.0.1:0"
	settings.SocksURL = "http://127.0.0.1:0"
	settings.AllowLocalBinding = true
	settings.MITM = true
	settings.SetAllowedDomains([]string{"bucket.s3.amazonaws.com"})
	signing := &AWSSigV4Config{
		PlaceholderAccessKeyID: "MASKEDACCESSKEY",
		Region:                 "us-east-1",
		Service:                "s3",
		AccessKeyID:            ProxySecretSource{Kind: "env_var", Name: "CODEX_TEST_AWS_ACCESS_KEY"},
		SecretAccessKey:        ProxySecretSource{Kind: "env_var", Name: "CODEX_TEST_AWS_SECRET_KEY"},
		Port:                   443,
		MaxBodyBytes:           1024,
	}
	settings.MITMHooks = []ProxyMITMHookConfig{{
		Host: "bucket.s3.amazonaws.com",
		Match: ProxyMITMHookMatchConfig{
			Methods:      []string{http.MethodPut},
			PathPrefixes: []string{"/"},
		},
		Actions: ProxyMITMHookActionsConfig{AWSSigV4: signing},
	}}
	t.Setenv("CODEX_TEST_AWS_ACCESS_KEY", "AKIDEXAMPLE")
	t.Setenv("CODEX_TEST_AWS_SECRET_KEY", "wJalrXUtnFEMI")
	prepared := startHookedHostAliasProxy(t, settings, map[string]string{})
	request, err := http.NewRequest(http.MethodPut, "https://bucket.s3.amazonaws.com/key", strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", awsGuestAuthorization("s3", "host;x-amz-date"))
	request.Header.Set("X-Amz-Date", "20150830T123600Z")
	inner := request.Clone(context.WithValue(request.Context(), proxyMITMContextKey{}, proxyMITMRequestContext{
		host: "bucket.s3.amazonaws.com",
		port: 443,
	}))
	inner.URL.Scheme = "https"
	outgoing, response := prepared.server.handleHTTPRequest(inner, &goproxy.ProxyCtx{})
	if response != nil {
		body := ProxyBlockedTextResponse("")
		_ = body
		t.Fatalf("request was blocked: %v", response)
	}
	if outgoing == nil {
		t.Fatalf("missing outgoing request")
	}
	authorization := outgoing.Header.Get("Authorization")
	if !strings.HasPrefix(authorization, "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-date, Signature=") {
		t.Fatalf("unexpected authorization %q", authorization)
	}
	if outgoing.Header.Get("X-Amz-Content-Sha256") == "" {
		t.Fatalf("expected an S3 payload hash header")
	}
	if outgoing.ContentLength != 5 {
		t.Fatalf("unexpected body length %d", outgoing.ContentLength)
	}
}

// TestMITMRequestDeniesUnsupportedAWSSigningLikeRust proves a selected
// placeholder with an ambiguous scope goes through the hook denial path.
func TestMITMRequestDeniesUnsupportedAWSSigningLikeRust(t *testing.T) {
	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.ProxyURL = "http://127.0.0.1:0"
	settings.SocksURL = "http://127.0.0.1:0"
	settings.AllowLocalBinding = true
	settings.MITM = true
	settings.SetAllowedDomains([]string{"bucket.s3.amazonaws.com"})
	settings.MITMHooks = []ProxyMITMHookConfig{{
		Host: "bucket.s3.amazonaws.com",
		Match: ProxyMITMHookMatchConfig{
			Methods:      []string{http.MethodPut},
			PathPrefixes: []string{"/"},
		},
		Actions: ProxyMITMHookActionsConfig{AWSSigV4: &AWSSigV4Config{
			PlaceholderAccessKeyID: "MASKEDACCESSKEY",
			Region:                 "us-east-1",
			Service:                "s3",
			AccessKeyID:            ProxySecretSource{Kind: "env_var", Name: "CODEX_TEST_AWS_ACCESS_KEY"},
			SecretAccessKey:        ProxySecretSource{Kind: "env_var", Name: "CODEX_TEST_AWS_SECRET_KEY"},
			Port:                   443,
			MaxBodyBytes:           1024,
		}},
	}}
	t.Setenv("CODEX_TEST_AWS_ACCESS_KEY", "AKIDEXAMPLE")
	t.Setenv("CODEX_TEST_AWS_SECRET_KEY", "wJalrXUtnFEMI")
	prepared := startHookedHostAliasProxy(t, settings, map[string]string{})
	request, err := http.NewRequest(http.MethodPut, "https://bucket.s3.amazonaws.com/key", strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", strings.Replace(awsGuestAuthorization("s3", "host;x-amz-date"), "us-east-1", "eu-west-1", 1))
	request.Header.Set("X-Amz-Date", "20150830T123600Z")
	inner := request.Clone(context.WithValue(request.Context(), proxyMITMContextKey{}, proxyMITMRequestContext{
		host: "bucket.s3.amazonaws.com",
		port: 443,
	}))
	inner.URL.Scheme = "https"
	_, response := prepared.server.handleHTTPRequest(inner, &goproxy.ProxyCtx{})
	if response == nil {
		t.Fatalf("expected a denial response")
	}
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("unexpected status %d", response.StatusCode)
	}
}
