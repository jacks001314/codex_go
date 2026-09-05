package network

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestProxySpecManagedRequirementsMergeUserDomainsLikeRust(t *testing.T) {
	settings := DefaultProxySettings()
	settings.SetAllowedDomains([]string{"api.example.com"})
	settings.SetDeniedDomains([]string{"blocked.example.com"})
	requirements := &ProxyRequirements{Domains: &ProxyDomainPermissions{Entries: []ProxyDomainPermissionEntry{
		{Pattern: "*.example.com", Permission: ProxyDomainAllow},
		{Pattern: "managed-blocked.example.com", Permission: ProxyDomainDeny},
	}}}
	spec, err := NewProxySpec(ProxyConfig{Network: settings}, requirements, true)
	if err != nil {
		t.Fatalf("NewProxySpec() error = %v", err)
	}
	got := spec.Config().Network
	if !reflect.DeepEqual(got.AllowedDomains(), []string{"*.example.com", "api.example.com"}) {
		t.Fatalf("allowed domains = %v", got.AllowedDomains())
	}
	if !reflect.DeepEqual(got.DeniedDomains(), []string{"managed-blocked.example.com", "blocked.example.com"}) {
		t.Fatalf("denied domains = %v", got.DeniedDomains())
	}
}

func TestProxySpecDisabledProfilePinsManagedDomainListsLikeRust(t *testing.T) {
	settings := DefaultProxySettings()
	settings.SetAllowedDomains([]string{"evil.com"})
	settings.SetDeniedDomains([]string{"more-blocked.example.com"})
	requirements := &ProxyRequirements{Domains: &ProxyDomainPermissions{Entries: []ProxyDomainPermissionEntry{
		{Pattern: "*.example.com", Permission: ProxyDomainAllow},
		{Pattern: "blocked.example.com", Permission: ProxyDomainDeny},
	}}}
	spec, err := NewProxySpec(ProxyConfig{Network: settings}, requirements, false)
	if err != nil {
		t.Fatalf("NewProxySpec() error = %v", err)
	}
	got := spec.Config().Network
	if !reflect.DeepEqual(got.AllowedDomains(), []string{"*.example.com"}) || !reflect.DeepEqual(got.DeniedDomains(), []string{"blocked.example.com"}) {
		t.Fatalf("network domains = %#v", got.Domains)
	}
}

func TestProxySpecManagedOnlyHardDeniesAndRejectsExecpolicyExpansionLikeRust(t *testing.T) {
	settings := DefaultProxySettings()
	settings.SetAllowedDomains([]string{"user.example.com"})
	spec, err := NewProxySpec(ProxyConfig{Network: settings}, &ProxyRequirements{
		Domains:                   &ProxyDomainPermissions{Entries: []ProxyDomainPermissionEntry{{Pattern: "managed.example.com", Permission: ProxyDomainAllow}}},
		ManagedAllowedDomainsOnly: true,
	}, true)
	if err != nil {
		t.Fatalf("NewProxySpec() error = %v", err)
	}
	config := spec.Config()
	if !spec.HardDenyAllowlistMisses() || !reflect.DeepEqual(config.Network.AllowedDomains(), []string{"managed.example.com"}) {
		t.Fatalf("spec = %#v", spec)
	}
	if _, err := spec.WithNetworkRules([]ProxyNetworkRule{{Host: "other.example.com", Permission: ProxyDomainAllow}}); err == nil {
		t.Fatal("WithNetworkRules() accepted an out-of-policy allow rule")
	}
}

func TestProxySpecManagedOnlyWithoutDomainsBlocksAllUserDomainsLikeRust(t *testing.T) {
	settings := DefaultProxySettings()
	settings.SetAllowedDomains([]string{"user.example.com"})
	spec, err := NewProxySpec(ProxyConfig{Network: settings}, &ProxyRequirements{ManagedAllowedDomainsOnly: true}, true)
	if err != nil {
		t.Fatalf("NewProxySpec() error = %v", err)
	}
	config := spec.Config()
	if got := config.Network.AllowedDomains(); got != nil {
		t.Fatalf("allowed domains = %v, want nil", got)
	}
}

func TestProxySpecManagedAllowDoesNotOverrideUserDenyLikeRust(t *testing.T) {
	settings := DefaultProxySettings()
	settings.SetDeniedDomains([]string{"api.example.com"})
	spec, err := NewProxySpec(ProxyConfig{Network: settings}, &ProxyRequirements{
		Domains: &ProxyDomainPermissions{Entries: []ProxyDomainPermissionEntry{{Pattern: "api.example.com", Permission: ProxyDomainAllow}}},
	}, true)
	if err != nil {
		t.Fatalf("NewProxySpec() error = %v", err)
	}
	got := spec.Config().Network
	if got.AllowedDomains() != nil || !reflect.DeepEqual(got.DeniedDomains(), []string{"api.example.com"}) {
		t.Fatalf("network domains = %#v", got.Domains)
	}
}

func TestProxySpecAppliesPortsScalarsAndUnixSocketsLikeRust(t *testing.T) {
	enabled := false
	allowUpstream := false
	allowAllSockets := false
	allowLocal := true
	httpPort := uint16(43128)
	socksPort := uint16(43129)
	settings := DefaultProxySettings()
	settings.SetAllowUnixSockets([]string{"/tmp/user.sock"})
	spec, err := NewProxySpec(ProxyConfig{Network: settings}, &ProxyRequirements{
		Enabled:                        &enabled,
		HTTPPort:                       &httpPort,
		SOCKSPort:                      &socksPort,
		AllowUpstreamProxy:             &allowUpstream,
		DangerouslyAllowAllUnixSockets: &allowAllSockets,
		UnixSockets: &ProxyUnixSocketPermissions{Entries: map[string]ProxyUnixSocketPermission{
			"/tmp/managed.sock": ProxyUnixSocketAllow,
			"/tmp/denied.sock":  ProxyUnixSocketDeny,
		}},
		AllowLocalBinding: &allowLocal,
	}, true)
	if err != nil {
		t.Fatalf("NewProxySpec() error = %v", err)
	}
	got := spec.Config().Network
	if got.Enabled || got.ProxyURL != "http://127.0.0.1:43128" || got.SocksURL != "http://127.0.0.1:43129" || got.AllowUpstreamProxy || !got.AllowLocalBinding {
		t.Fatalf("network settings = %#v", got)
	}
	if !reflect.DeepEqual(got.AllowUnixSockets(), []string{"/tmp/managed.sock"}) {
		t.Fatalf("allow unix sockets = %v", got.AllowUnixSockets())
	}
}

func TestValidateProxyPolicyAgainstConstraintsMatchesRustSecurityBoundaries(t *testing.T) {
	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.SetAllowedDomains([]string{"api.example.com"})
	managed := []string{"*.example.com"}
	constraints := ProxyConstraints{AllowedDomains: &managed}
	if err := ValidateProxyPolicyAgainstConstraints(ProxyConfig{Network: settings}, constraints); err != nil {
		t.Fatalf("narrowed wildcard should be allowed: %v", err)
	}
	settings.SetAllowedDomains([]string{"**.example.com"})
	if err := ValidateProxyPolicyAgainstConstraints(ProxyConfig{Network: settings}, constraints); err == nil {
		t.Fatal("widened wildcard was accepted")
	}
	global := []string{"[*]"}
	if err := ValidateProxyPolicyAgainstConstraints(ProxyConfig{Network: settings}, ProxyConstraints{AllowedDomains: &global}); err == nil || !strings.Contains(err.Error(), "network.allowed_domains") {
		t.Fatalf("global wildcard error = %v", err)
	}
}

func TestSetAllowUnixSocketsPreservesExplicitDeniesLikeRust(t *testing.T) {
	settings := DefaultProxySettings()
	settings.UnixSockets = &ProxyUnixSocketPermissions{Entries: map[string]ProxyUnixSocketPermission{
		"/tmp/deny.sock": ProxyUnixSocketDeny,
		"/tmp/old.sock":  ProxyUnixSocketAllow,
	}}
	settings.SetAllowUnixSockets([]string{"/tmp/new.sock"})
	if settings.UnixSockets.Entries["/tmp/deny.sock"] != ProxyUnixSocketDeny || settings.UnixSockets.Entries["/tmp/old.sock"] != "" || settings.UnixSockets.Entries["/tmp/new.sock"] != ProxyUnixSocketAllow {
		t.Fatalf("unix sockets = %#v", settings.UnixSockets)
	}
}

func TestCompileProxyDomainMatcherUsesGlobLibraryWithRustDomainExpansion(t *testing.T) {
	matcher, err := CompileProxyDomainMatcher([]string{"*.Example.COM.", "api-[0-9].openai.com"}, false)
	if err != nil {
		t.Fatalf("CompileProxyDomainMatcher() error = %v", err)
	}
	for _, host := range []string{"API.EXAMPLE.COM", "deep.api.example.com", "api-7.openai.com"} {
		if !matcher.Match(host) {
			t.Fatalf("matcher did not match %q", host)
		}
	}
	if matcher.Match("example.com") {
		t.Fatal("subdomains-only matcher matched the apex")
	}
	if _, err := CompileProxyDomainMatcher([]string{"**.[*]"}, true); err == nil {
		t.Fatal("global wildcard deny pattern was accepted")
	}
}

func TestApplyProxyHeaderInjections(t *testing.T) {
	request, _ := http.NewRequest(http.MethodPost, "https://api.example.com/console/v1/x", nil)
	ApplyProxyHeaderInjections(request, []ProxyHeaderInjection{{
		Host:         "api.example.com",
		Methods:      []string{"POST"},
		PathPrefixes: []string{"/console/v1"},
		Headers:      map[string]string{"x-statsig-change-source": "codex"},
	}})
	if got := request.Header.Get("x-statsig-change-source"); got != "codex" {
		t.Fatalf("header = %q, want codex", got)
	}

	request, _ = http.NewRequest(http.MethodGet, "https://other.example.com/console/v1", nil)
	ApplyProxyHeaderInjections(request, []ProxyHeaderInjection{{
		Host:    "api.example.com",
		Headers: map[string]string{"x-statsig-change-source": "codex"},
	}})
	if got := request.Header.Get("x-statsig-change-source"); got != "" {
		t.Fatalf("unmatched host header = %q", got)
	}

	request, _ = http.NewRequest(http.MethodGet, "https://api.example.com/console/v1", nil)
	ApplyProxyHeaderInjections(request, []ProxyHeaderInjection{{
		Host:    "api.example.com",
		Methods: []string{"POST"},
		Headers: map[string]string{"x-statsig-change-source": "codex"},
	}})
	if got := request.Header.Get("x-statsig-change-source"); got != "" {
		t.Fatalf("unmatched method header = %q", got)
	}
}

func TestNewProxySpecCarriesHeaderInjections(t *testing.T) {
	spec, err := NewProxySpec(ProxyConfig{}, &ProxyRequirements{HeaderInjections: []ProxyHeaderInjection{{
		Host:    "api.example.com",
		Methods: []string{"POST"},
		Headers: map[string]string{"x-statsig-change-source": "codex"},
	}}}, false)
	if err != nil {
		t.Fatalf("NewProxySpec error = %v", err)
	}
	if len(spec.constraints.HeaderInjections) != 1 || spec.constraints.HeaderInjections[0].Host != "api.example.com" {
		t.Fatalf("header injections = %#v", spec.constraints.HeaderInjections)
	}
	if got := spec.HeaderInjections(); len(got) != 1 || got[0].Host != "api.example.com" {
		t.Fatalf("HeaderInjections() = %#v", got)
	}
	original := ProxyConfig{HeaderInjections: []ProxyHeaderInjection{{
		Host:    "api.example.com",
		Headers: map[string]string{"x": "y"},
	}}}
	cloned := cloneProxyConfig(original)
	cloned.HeaderInjections[0].Headers["x"] = "changed"
	if original.HeaderInjections[0].Headers["x"] != "y" {
		t.Fatal("cloneProxyConfig should deep-copy header injections")
	}
}
