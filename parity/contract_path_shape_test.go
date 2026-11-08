package parity

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestContractRustPathsAreRelativeToCodexRs freezes the shape the contract
// oracle relies on: every rustPaths entry is resolved as
// `filepath.Join(CODEX_RUST_ROOT, path)` where the root IS the codex-rs
// checkout. A `codex-rs/` prefix therefore silently doubles the directory and
// can hide a wrong path behind a same-named file elsewhere in the tree.
//
// The shape half always runs; the existence half is what
// TestAlignmentRustContractOraclePathsExist does when a checkout is available,
// so a missing CODEX_RUST_ROOT no longer means "nothing about paths is checked".
func TestContractRustPathsAreRelativeToCodexRs(t *testing.T) {
	manifest := readAlignmentJSON[alignmentContractManifest](t, filepath.Join("contracts", "manifest.json"))
	for _, contract := range manifest.Contracts {
		if len(contract.RustPaths) == 0 {
			t.Errorf("contract %q has no rust paths", contract.ID)
			continue
		}
		for _, path := range contract.RustPaths {
			switch {
			case strings.TrimSpace(path) == "":
				t.Errorf("contract %q has an empty rust path", contract.ID)
			case filepath.IsAbs(path) || strings.HasPrefix(path, "/"):
				t.Errorf("contract %q rust path %q is absolute; keep it relative to the codex-rs checkout", contract.ID, path)
			case strings.HasPrefix(path, "codex-rs/"):
				t.Errorf("contract %q rust path %q repeats the checkout directory; the oracle already joins it", contract.ID, path)
			case strings.Contains(path, "codex-rs/codex-rs"):
				t.Errorf("contract %q rust path %q repeats the checkout directory twice", contract.ID, path)
			case strings.HasPrefix(path, "~"):
				t.Errorf("contract %q rust path %q is home-relative", contract.ID, path)
			}
		}
	}
}
