package provisionservice

import (
	"os"
	"testing"
)

func TestValidateServiceFamilyHintAcceptsPackagedFamilies(t *testing.T) {
	for _, family := range []string{
		"OpenAI.Codex_3k8sg7r9htsxt",
		"OpenAI.CodexBeta_jabp31b5fhs74",
	} {
		if err := ValidateServiceFamilyHint(family); err != nil {
			t.Fatalf("ValidateServiceFamilyHint(%q) error = %v", family, err)
		}
	}
}

func TestValidateServiceFamilyHintRejectsPathsAndMalformedFamilies(t *testing.T) {
	for _, family := range []string{
		"",
		"_3k8sg7r9htsxt",
		"OpenAI.Codex_bad",
		`OpenAI.Codex_3k8sg7r9htsxt\child`,
		`..\OpenAI.Codex_3k8sg7r9htsxt`,
		"OpenAI.Codex_extra_3k8sg7r9htsxt",
		"OpenAI.Codex\x00_3k8sg7r9htsxt",
	} {
		if err := ValidateServiceFamilyHint(family); err == nil {
			t.Fatalf("ValidateServiceFamilyHint(%q) must fail", family)
		}
	}
}

func TestServiceIdentityUsesValidatedPackageFamilyHint(t *testing.T) {
	family, ok, err := currentPackageFamily()
	if err != nil {
		t.Fatalf("currentPackageFamily() error = %v", err)
	}
	if ok {
		t.Skipf("test process already belongs to package family %q", family)
	}
	t.Setenv(PackageFamilyEnv, "OpenAI.Codex_3k8sg7r9htsxt")
	name, err := ServiceName()
	if err != nil {
		t.Fatalf("ServiceName() error = %v", err)
	}
	if name != "CodexSandboxService.OpenAI.Codex" {
		t.Fatalf("ServiceName() = %q", name)
	}
	pipe, err := ServicePipeName()
	if err != nil {
		t.Fatalf("ServicePipeName() error = %v", err)
	}
	if pipe != PipeName+".OpenAI.Codex_3k8sg7r9htsxt" {
		t.Fatalf("ServicePipeName() = %q", pipe)
	}
}

func TestServiceIdentityFallsBackToLegacyLookup(t *testing.T) {
	if _, ok, err := currentPackageFamily(); err != nil || ok {
		if err != nil {
			t.Fatalf("currentPackageFamily() error = %v", err)
		}
		t.Skip("test process belongs to a package family")
	}
	previous, hadPrevious := os.LookupEnv(PackageFamilyEnv)
	_ = os.Unsetenv(PackageFamilyEnv)
	t.Cleanup(func() {
		if hadPrevious {
			_ = os.Setenv(PackageFamilyEnv, previous)
			return
		}
		_ = os.Unsetenv(PackageFamilyEnv)
	})
	name, err := ServiceName()
	if err != nil {
		t.Fatalf("ServiceName() error = %v", err)
	}
	if name != "CodexSandboxService" {
		t.Fatalf("ServiceName() = %q", name)
	}
	pipe, err := ServicePipeName()
	if err != nil {
		t.Fatalf("ServicePipeName() error = %v", err)
	}
	if pipe != PipeName {
		t.Fatalf("ServicePipeName() = %q", pipe)
	}
}

func TestServiceIdentityRejectsMalformedHint(t *testing.T) {
	if _, ok, err := currentPackageFamily(); err != nil || ok {
		if err != nil {
			t.Fatalf("currentPackageFamily() error = %v", err)
		}
		t.Skip("test process belongs to a package family")
	}
	t.Setenv(PackageFamilyEnv, `..\OpenAI.Codex_3k8sg7r9htsxt`)
	if _, err := ServiceName(); err == nil {
		t.Fatal("malformed routing hint must be rejected")
	}
	if _, err := ServicePipeName(); err == nil {
		t.Fatal("malformed routing hint must be rejected")
	}
}
