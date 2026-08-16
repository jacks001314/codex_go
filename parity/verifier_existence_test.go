package parity

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestContractVerifiersExistInGoTestFiles is the static-layer audit for the
// contract inventory: every verifier named in contracts/manifest.json must
// correspond to an actual test function somewhere in the Go tree. The
// manifest's goPaths name the Go implementation (whose on-disk existence is
// checked by TestAlignmentContractInventoryIsTraceable); verifiers are
// cross-implementation checks that may live in parity/ or the owning
// package's tests. A renamed or deleted verifier must not silently detach a
// contract from its evidence.
func TestContractVerifiersExistInGoTestFiles(t *testing.T) {
	manifest := readAlignmentJSON[alignmentContractManifest](t, filepath.Join("contracts", "manifest.json"))
	available := collectGoTestFuncNames(t)
	for _, contract := range manifest.Contracts {
		names := verifierNames(contract.Verifier)
		if len(names) == 0 {
			t.Fatalf("contract %q has no verifier names", contract.ID)
		}
		for _, name := range names {
			if !available[name] {
				t.Errorf("contract %q verifier %q has no matching test function anywhere in the Go tree",
					contract.ID, name)
			}
		}
	}
}

var testFuncPattern = regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]+)\(t \*testing\.T\)`)

func collectGoTestFuncNames(t *testing.T) map[string]bool {
	t.Helper()
	available := map[string]bool{}
	err := filepath.WalkDir(filepath.Join(".."), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, match := range testFuncPattern.FindAllStringSubmatch(string(data), -1) {
			available[match[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir: %v", err)
	}
	return available
}

func verifierNames(verifier string) []string {
	var out []string
	for _, part := range strings.Split(verifier, ";") {
		if name := strings.TrimSpace(part); name != "" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
