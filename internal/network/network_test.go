package network

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNewSpecAppliesRequirements(t *testing.T) {
	enabled := true
	allowProxy := false
	spec, err := NewSpec(Config{
		Domains: map[string]DomainPermission{"user.example.com": DomainAllow},
	}, &Requirements{
		Enabled:            &enabled,
		HTTPPort:           4000,
		SocksPort:          5000,
		AllowUpstreamProxy: &allowProxy,
		AllowedDomains:     []string{"Managed.Example.com."},
		DeniedDomains:      []string{"blocked.example.com"},
	}, PermissionDisabled)
	if err != nil {
		t.Fatalf("NewSpec() error = %v", err)
	}
	config := spec.Config()
	if config.ProxyURL != "http://127.0.0.1:4000" || config.SocksURL != "http://127.0.0.1:5000" || !config.EnableSocks5 {
		t.Fatalf("proxy config = %#v", config)
	}
	if config.Domains["managed.example.com"] != DomainAllow || config.Domains["user.example.com"] != DomainAllow {
		t.Fatalf("allowed domains = %v", config.Domains)
	}
	if config.Domains["blocked.example.com"] != DomainDeny {
		t.Fatalf("denied domains = %v", config.Domains)
	}
}

func TestManagedHardDenyDisallowsAllowlistExpansion(t *testing.T) {
	_, err := NewSpec(Config{
		Domains: map[string]DomainPermission{"user.example.com": DomainAllow},
	}, &Requirements{
		AllowedDomains:            []string{"managed.example.com"},
		ManagedAllowedDomainsOnly: true,
	}, PermissionManaged)
	if err != nil {
		t.Fatalf("NewSpec() should drop user allowlist, got error %v", err)
	}
	spec, err := NewSpec(Config{}, &Requirements{
		AllowedDomains:            []string{"managed.example.com"},
		ManagedAllowedDomainsOnly: true,
	}, PermissionManaged)
	if err != nil {
		t.Fatalf("NewSpec(managed) error = %v", err)
	}
	if !spec.HardDenyAllowlistMisses() {
		t.Fatalf("HardDenyAllowlistMisses() = false, want true")
	}
	next, err := spec.WithExecPolicyNetworkRules([]NetworkRule{{Host: "other.example.com", Decision: DomainAllow}})
	if err == nil {
		t.Fatalf("WithExecPolicyNetworkRules(extra allow) = %#v, want constraint error", next)
	}
}

func TestWithExecPolicyNetworkRules(t *testing.T) {
	spec, err := NewSpec(DefaultConfig(), nil, PermissionDisabled)
	if err != nil {
		t.Fatalf("NewSpec() error = %v", err)
	}
	next, err := spec.WithExecPolicyNetworkRules([]NetworkRule{
		{Host: "API.EXAMPLE.COM.", Decision: DomainDeny},
	})
	if err != nil {
		t.Fatalf("WithExecPolicyNetworkRules() error = %v", err)
	}
	if next.Config().Domains["api.example.com"] != DomainDeny {
		t.Fatalf("domains = %v", next.Config().Domains)
	}
}

func TestHostAndPortFromNetworkAddr(t *testing.T) {
	cases := map[string]string{
		"http://127.0.0.1:4000": "127.0.0.1:4000",
		"http://localhost":      "localhost:3128",
		"bad":                   "127.0.0.1:3128",
	}
	for input, want := range cases {
		if got := HostAndPortFromNetworkAddr(input, 3128); got != want {
			t.Fatalf("HostAndPortFromNetworkAddr(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestMtimeReloaderChanged(t *testing.T) {
	now := time.Now()
	reloader := NewMtimeReloader([]LayerMTime{{Path: "a", MTime: now}})
	if reloader.SourceLabel() != "MtimeConfigReloader" {
		t.Fatalf("SourceLabel() = %q", reloader.SourceLabel())
	}
	if reloader.Changed([]LayerMTime{{Path: "a", MTime: now}}) {
		t.Fatalf("Changed(same) = true")
	}
	if !reloader.Changed([]LayerMTime{{Path: "a", MTime: now.Add(time.Second)}}) {
		t.Fatalf("Changed(modified) = false")
	}
}

func TestMergeDomainLists(t *testing.T) {
	got := mergeDomainLists([]string{"A.example.com.", "b.example.com"}, []string{"a.example.com", "c.example.com"})
	want := []string{"a.example.com", "b.example.com", "c.example.com"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mergeDomainLists() = %v, want %v", got, want)
	}
}

func TestValidateAgainstConstraintsMessage(t *testing.T) {
	enabled := false
	err := ValidateAgainstConstraints(&Config{Enabled: true}, &Constraints{Enabled: &enabled})
	if err == nil || !strings.Contains(err.Error(), "enabled") {
		t.Fatalf("ValidateAgainstConstraints() error = %v", err)
	}
}
