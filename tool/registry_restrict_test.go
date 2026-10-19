package tool

import "testing"

func TestRegistryRestrictToToolsLikeRust(t *testing.T) {
	registry := NewRegistry()
	for _, name := range []string{"exec", "wait", "apply_patch"} {
		if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName(name)}, nil)); err != nil {
			t.Fatal(err)
		}
	}
	registry.RestrictToTools([]string{"exec", "functions.wait"})
	if _, ok := registry.Spec(PlainName("exec")); !ok {
		t.Fatal("allowed exec was removed")
	}
	if _, ok := registry.Spec(PlainName("wait")); !ok {
		t.Fatal("namespace-qualified allowed wait was removed")
	}
	if _, ok := registry.Spec(PlainName("apply_patch")); ok {
		t.Fatal("disallowed apply_patch survived")
	}
	registry.RestrictToTools([]string{})
	if len(registry.Names()) != 0 {
		t.Fatalf("empty allowlist kept tools: %#v", registry.Names())
	}
}
