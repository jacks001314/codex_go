package execpolicy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateLegacyAllowRulesIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "legacy.json")
	policy := filepath.Join(dir, "default.rules")
	if err := os.WriteFile(legacy, []byte(`{"commands":[["git","status"],["go","test"]]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	added, err := MigrateLegacyAllowRules(legacy, policy)
	if err != nil || added != 2 {
		t.Fatalf("first = %d, %v", added, err)
	}
	added, err = MigrateLegacyAllowRules(legacy, policy)
	if err != nil || added != 0 {
		t.Fatalf("second = %d, %v", added, err)
	}
	output, err := Check(&CheckOptions{Rules: []string{policy}, Command: []string{"git", "status"}})
	if err != nil || output.Decision == nil || *output.Decision != DecisionAllow {
		t.Fatalf("check = %#v, %v", output, err)
	}
}
