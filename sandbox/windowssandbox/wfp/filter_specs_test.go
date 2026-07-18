package wfp

import "testing"

func TestDefaultFilterSpecsMatchRustShape(t *testing.T) {
	specs := DefaultFilterSpecs()
	if len(specs) != 12 {
		t.Fatalf("DefaultFilterSpecs() len = %d, want 12", len(specs))
	}
	wantNames := []string{
		"codex_wfp_icmp_connect_v4",
		"codex_wfp_icmp_connect_v6",
		"codex_wfp_icmp_assign_v4",
		"codex_wfp_icmp_assign_v6",
		"codex_wfp_dns_53_v4",
		"codex_wfp_dns_53_v6",
		"codex_wfp_dns_853_v4",
		"codex_wfp_dns_853_v6",
		"codex_wfp_smb_445_v4",
		"codex_wfp_smb_445_v6",
		"codex_wfp_smb_139_v4",
		"codex_wfp_smb_139_v6",
	}
	for i, want := range wantNames {
		if specs[i].Name != want {
			t.Fatalf("spec[%d].Name = %q, want %q", i, specs[i].Name, want)
		}
		if len(specs[i].Conditions) != 2 || specs[i].Conditions[0].Kind != ConditionUser {
			t.Fatalf("spec[%d].Conditions = %#v, want user plus one scoped condition", i, specs[i].Conditions)
		}
	}
}

func TestFilterSpecKeysAndNamesAreUnique(t *testing.T) {
	specs := DefaultFilterSpecs()
	keys := map[string]bool{}
	names := map[string]bool{}
	for _, spec := range specs {
		if spec.Key == "" {
			t.Fatalf("spec %q has empty key", spec.Name)
		}
		if keys[spec.Key] {
			t.Fatalf("duplicate filter key %q", spec.Key)
		}
		keys[spec.Key] = true
		if names[spec.Name] {
			t.Fatalf("duplicate filter name %q", spec.Name)
		}
		names[spec.Name] = true
	}
}

func TestDefaultFilterSpecsReturnsDeepCopy(t *testing.T) {
	specs := DefaultFilterSpecs()
	specs[0].Name = "mutated"
	specs[0].Conditions[0].Kind = ConditionRemotePort

	again := DefaultFilterSpecs()
	if again[0].Name != "codex_wfp_icmp_connect_v4" {
		t.Fatalf("DefaultFilterSpecs() reused mutated spec name %q", again[0].Name)
	}
	if again[0].Conditions[0].Kind != ConditionUser {
		t.Fatalf("DefaultFilterSpecs() reused mutated condition %#v", again[0].Conditions[0])
	}
}
