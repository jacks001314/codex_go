package network

import "testing"

func TestEnvironmentNetworkPolicyApplyToPreservesInheritedDenials(t *testing.T) {
	controller := &Config{
		Enabled: true,
		Domains: map[string]DomainPermission{
			"blocked.example": DomainDeny,
			"allowed.example": DomainAllow,
		},
	}
	owner := &EnvironmentNetworkPolicy{
		Domains: map[string]DomainPermission{
			"owner.example": DomainAllow,
		},
	}
	owner.ApplyTo(controller)
	if controller.Domains["owner.example"] != DomainAllow {
		t.Fatalf("owner domain not applied: %+v", controller.Domains)
	}
	if controller.Domains["blocked.example"] != DomainDeny {
		t.Fatalf("inherited denial not preserved: %+v", controller.Domains)
	}
}

func TestNewSpecForEnvironmentRejectsDisabledManagedNetwork(t *testing.T) {
	_, err := NewSpecForEnvironment(Config{}, nil, PermissionDisabled, &EnvironmentNetworkPolicy{}, nil)
	if err == nil {
		t.Fatal("disabled managed network should be rejected")
	}

	spec, err := NewSpecForEnvironment(Config{Enabled: true}, nil, PermissionManaged, &EnvironmentNetworkPolicy{
		Domains: map[string]DomainPermission{"owner.example": DomainAllow},
	}, []NetworkRule{{Host: "decided.example", Decision: DomainAllow}})
	if err != nil {
		t.Fatalf("NewSpecForEnvironment error = %v", err)
	}
	if spec.Config().Domains["owner.example"] != DomainAllow {
		t.Fatalf("owner domain missing: %+v", spec.Config().Domains)
	}
	if spec.Config().Domains["decided.example"] != DomainAllow {
		t.Fatalf("exec rule domain missing: %+v", spec.Config().Domains)
	}
}
