package exec

// Rust parity: codex-rs/exec/src/lib.rs and the embedded application policy
// (app-server/src/in_process_bootstrap.rs): the run publishes the policy from its
// managed requirements and every client it builds enforces it.

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/network"
)

// execPolicyDoerStub records whether a request reached the transport.
type execPolicyDoerStub struct {
	attempts int
}

func (d *execPolicyDoerStub) Do(*http.Request) (*http.Response, error) {
	d.attempts++
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok"))}, nil
}

func execTestURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q) error = %v", raw, err)
	}
	return parsed
}

func TestExecRunPublishesApplicationNetworkPolicyLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte(`
[application.network]
[application.network.domains]
"allowed.example" = "allow"
"denied.example" = "deny"
`), 0o600); err != nil {
		t.Fatalf("write requirements.toml: %v", err)
	}
	cfg, err := config.LoadEffectiveWithOptions(home, nil)
	if err != nil {
		t.Fatalf("LoadEffectiveWithOptions() error = %v", err)
	}
	if cfg.Requirements == nil || cfg.Requirements.Application == nil {
		t.Fatalf("loaded requirements = %#v, want the managed application requirements", cfg.Requirements)
	}
	if controller := publishApplicationNetworkPolicy(cfg); controller == nil {
		t.Fatal("publishApplicationNetworkPolicy() returned no controller")
	}

	stub := &execPolicyDoerStub{}
	runner := &Runner{HTTPClient: stub}
	doer := runner.httpClientForConfig(cfg)
	response, err := doer.Do(&http.Request{URL: execTestURL(t, "https://allowed.example/v1")})
	if err != nil {
		t.Fatalf("allowed request error = %v", err)
	}
	_ = response.Body.Close()
	if stub.attempts != 1 {
		t.Fatalf("attempts = %d, want the allowed request to reach the transport", stub.attempts)
	}
	if _, err := doer.Do(&http.Request{URL: execTestURL(t, "https://denied.example/v1")}); !errors.Is(err, network.ErrNetworkPolicyDestination) {
		t.Fatalf("denied request error = %v", err)
	}
	if stub.attempts != 1 {
		t.Fatalf("attempts = %d, want the denied request rejected before connecting", stub.attempts)
	}

	// A run without a published policy keeps its client unchanged.
	unbound := &Runner{HTTPClient: &execPolicyDoerStub{}}
	if unchanged, ok := unbound.httpClientForConfig(&config.Config{Values: map[string]any{}}).(*execPolicyDoerStub); !ok || unchanged == nil {
		t.Fatal("an unpublished policy replaced the caller's client")
	}
}
