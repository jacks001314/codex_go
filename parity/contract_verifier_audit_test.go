package parity

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestContractVerifierAuditMatchesTree is the standing gate for the ledger's
// certification claims: every verifier named in
// parity/contracts/manifest.json must exist as a real Go test function, and
// every goPath must exist on disk. A contract whose verifier was renamed,
// deleted, or never landed fails here instead of quietly staying "complete".
//
// Contracts with no verifier are reported, not failed: an empty verifier is a
// ledger gap to schedule, not a broken gate.
func TestContractVerifierAuditMatchesTree(t *testing.T) {
	root, err := contractVerifierAuditRepoRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	results, err := auditContractVerifiers(root)
	if err != nil {
		t.Fatalf("audit contract verifiers: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("contract manifest under %s lists no contracts", root)
	}

	unregistered := 0
	missingNames := 0
	missingPaths := 0
	for _, result := range results {
		if result.Unregistered {
			unregistered++
			// A contract without a verifier cannot be certified by a test that
			// does not exist yet; report it so the gap stays visible.
			t.Logf("contract %s (status %s): no verifier registered — ledger gap, not a gate failure", result.ID, result.Status)
			continue
		}
		t.Logf("contract %s: registered=%d hit=%d missing=%d", result.ID, result.Registered, result.Hit, len(result.Missing))
		for _, name := range result.Missing {
			missingNames++
			t.Errorf("contract %s: verifier %s not found in %s", result.ID, name, describeContractGoPaths(result))
		}
		for _, goPath := range result.MissingGoPaths {
			missingPaths++
			t.Errorf("contract %s: goPath %s does not exist", result.ID, goPath)
		}
	}
	t.Logf("audited %d contracts under %s: %d without a verifier, %d missing verifier names, %d missing goPaths",
		len(results), root, unregistered, missingNames, missingPaths)
}

// contractVerifierAuditRepoRoot walks up from this test file to the repository
// root, which is the parent of the parity package directory.
func contractVerifierAuditRepoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot resolve the location of this test file")
	}
	return filepath.Dir(filepath.Dir(file)), nil
}

// describeContractGoPaths renders the contract's goPaths for a failure message so
// the reader knows where the missing verifier was expected to live.
func describeContractGoPaths(result contractVerifierAudit) string {
	if len(result.GoPaths) == 0 {
		return "(contract declares no goPaths)"
	}
	return strings.Join(result.GoPaths, ", ")
}
