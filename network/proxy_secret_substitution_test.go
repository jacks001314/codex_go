package network

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/elazarl/goproxy"
)

const testBodyPlaceholder = "PLACEHOLDER_0123456789"

func bodySubstitutionSigner(t *testing.T) *BodySecretSubstitution {
	t.Helper()
	config := &BodySecretSubstitutionConfig{
		Replacements: []BodySecretReplacement{{
			Placeholder: testBodyPlaceholder,
			Secret:      ProxySecretSource{Kind: "env_var", Name: "CODEX_TEST_BODY_SECRET"},
		}},
		Port:         443,
		MaxBodyBytes: 1024,
	}
	substitution, err := config.Compile(func(name string) (string, bool) {
		if name == "CODEX_TEST_BODY_SECRET" {
			return "real-body-secret", true
		}
		return "", false
	}, func(string) (string, error) { return "", nil })
	if err != nil {
		t.Fatalf("compile substitution: %v", err)
	}
	return substitution
}

func bodySubstitutionRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	parsed, err := url.Parse("https://bucket.s3.amazonaws.com/key")
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPut, parsed.String(), strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.URL = parsed
	request.RequestURI = ""
	request.Header.Set("Content-Length", strconv.Itoa(len(body)))
	return request
}

func TestBodySubstitutionReplacesOnlyOriginalBytes(t *testing.T) {
	substitution := bodySubstitutionSigner(t)
	request := bodySubstitutionRequest(t, `{"token":"`+testBodyPlaceholder+`"}`)
	if err := substitution.Apply(request, true); err != nil {
		t.Fatalf("apply: %v", err)
	}
	body, err := collectProxyBodyFromReader(request.Body, 4096)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != `{"token":"real-body-secret"}` {
		t.Fatalf("body = %q", body)
	}
	if request.ContentLength != int64(len(body)) {
		t.Fatalf("content length = %d", request.ContentLength)
	}
	if got := request.Header.Get("Content-Length"); got != strconv.Itoa(len(body)) {
		t.Fatalf("content-length header = %q", got)
	}
}

func TestBodySubstitutionRejectsIntegrityProtectedPayloads(t *testing.T) {
	substitution := bodySubstitutionSigner(t)
	for _, header := range [][2]string{
		{"Content-Encoding", "gzip"},
		{"Content-MD5", "abc"},
		{"Digest", "sha-256=abc"},
		{"X-Amz-Content-Sha256", "abc"},
		{"X-Amz-Checksum-Crc32", "abc"},
		{"Trailer", "x-check"},
	} {
		request := bodySubstitutionRequest(t, testBodyPlaceholder)
		request.Header.Set(header[0], header[1])
		if err := substitution.Apply(request, true); err == nil {
			t.Fatalf("expected rejection for %s", header[0])
		}
	}
	request := bodySubstitutionRequest(t, testBodyPlaceholder)
	if err := substitution.Apply(request, false); err == nil {
		t.Fatalf("expected rejection when original framing was unsupported")
	}
}

func TestBodySubstitutionRejectsMismatchedContentLength(t *testing.T) {
	substitution := bodySubstitutionSigner(t)
	request := bodySubstitutionRequest(t, testBodyPlaceholder)
	request.Header.Set("Content-Length", "1")
	if err := substitution.Apply(request, true); err == nil {
		t.Fatalf("expected content length rejection")
	}
}

func TestBodySubstitutionConfigValidation(t *testing.T) {
	valid := func() *BodySecretSubstitutionConfig {
		return &BodySecretSubstitutionConfig{
			Replacements: []BodySecretReplacement{{
				Placeholder: testBodyPlaceholder,
				Secret:      ProxySecretSource{Kind: "env_var", Name: "SECRET"},
			}},
			Port:         443,
			MaxBodyBytes: 1024,
		}
	}
	if err := valid().Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	short := valid()
	short.Replacements[0].Placeholder = "short"
	if err := short.Validate(); err == nil {
		t.Fatalf("expected short placeholder rejection")
	}
	overlap := valid()
	overlap.Replacements = append(overlap.Replacements, BodySecretReplacement{
		Placeholder: testBodyPlaceholder[:20],
		Secret:      ProxySecretSource{Kind: "env_var", Name: "SECRET"},
	})
	if err := overlap.Validate(); err == nil {
		t.Fatalf("expected overlapping placeholder rejection")
	}
	tooLarge := valid()
	tooLarge.MaxBodyBytes = 16*1024*1024 + 1
	if err := tooLarge.Validate(); err == nil {
		t.Fatalf("expected limit rejection")
	}
	empty := valid()
	empty.Replacements = nil
	if err := empty.Validate(); err == nil {
		t.Fatalf("expected empty bindings rejection")
	}
}

func TestBodySubstitutionRejectsSecretContainingPlaceholder(t *testing.T) {
	config := &BodySecretSubstitutionConfig{
		Replacements: []BodySecretReplacement{{
			Placeholder: testBodyPlaceholder,
			Secret:      ProxySecretSource{Kind: "env_var", Name: "SECRET"},
		}},
		Port:         443,
		MaxBodyBytes: 1024,
	}
	if _, err := config.Compile(func(string) (string, bool) {
		return "real-" + testBodyPlaceholder, true
	}, func(string) (string, error) { return "", nil }); err == nil {
		t.Fatalf("expected secret containing placeholder rejection")
	}
}

// TestMITMRequestSubstitutesBodyBeforeSigningLikeRust proves the documented
// order: body substitution runs before AWS signing, so the signature covers the
// substituted payload.
func TestMITMRequestSubstitutesBodyBeforeSigningLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("CODEX_TEST_AWS_ACCESS_KEY", "AKIDEXAMPLE")
	t.Setenv("CODEX_TEST_AWS_SECRET_KEY", "wJalrXUtnFEMI")
	t.Setenv("CODEX_TEST_BODY_SECRET", "real-body-secret")
	placeholder := testBodyPlaceholder
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
		Actions: ProxyMITMHookActionsConfig{
			AWSSigV4: &AWSSigV4Config{
				PlaceholderAccessKeyID: "MASKEDACCESSKEY",
				Region:                 "us-east-1",
				Service:                "s3",
				AccessKeyID:            ProxySecretSource{Kind: "env_var", Name: "CODEX_TEST_AWS_ACCESS_KEY"},
				SecretAccessKey:        ProxySecretSource{Kind: "env_var", Name: "CODEX_TEST_AWS_SECRET_KEY"},
				Port:                   443,
				MaxBodyBytes:           1024,
			},
			BodySecretSubstitution: &BodySecretSubstitutionConfig{
				Replacements: []BodySecretReplacement{{
					Placeholder: placeholder,
					Secret:      ProxySecretSource{Kind: "env_var", Name: "CODEX_TEST_BODY_SECRET"},
				}},
				Port:         443,
				MaxBodyBytes: 1024,
			},
		},
	}}
	prepared := startHookedHostAliasProxy(t, settings, map[string]string{})
	request, err := http.NewRequest(http.MethodPut, "https://bucket.s3.amazonaws.com/key", strings.NewReader("token="+placeholder))
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
		t.Fatalf("request was blocked: %v", response)
	}
	body, err := collectProxyBodyFromReader(outgoing.Body, 1024)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "token=real-body-secret" {
		t.Fatalf("body = %q", body)
	}
	sum := sha256.Sum256(body)
	if got := outgoing.Header.Get("X-Amz-Content-Sha256"); got != hex.EncodeToString(sum[:]) {
		t.Fatalf("payload hash = %q", got)
	}
}
