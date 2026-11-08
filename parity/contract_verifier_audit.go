package parity

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Contract verifier audit.
//
// parity/contracts/manifest.json records, for every contract, the Go test
// functions that certify it. Nothing verifies that those names still exist:
// a rename, a deleted file, or a contract copied from a plan document with a
// "planned" verifier leaves the ledger claiming certification no test provides.
// The audit below resolves every registered name against the real tree so the
// drift shows up as a gate failure instead of an unnoticed ledger gap.
//
// It never hardcodes a contract id or a contract count: the same code runs on
// the 81-entry and the 136-entry manifests.

// contractVerifierAuditSkippedDirs are directories that never hold first-party
// Go sources. Local scratch space (the project mirror's own books), vendored
// trees and VCS metadata must not be able to satisfy or break a lookup.
var contractVerifierAuditSkippedDirs = map[string]bool{
	".git":         true,
	".syntropy":    true,
	"attachments":  true,
	"artifacts":    true,
	"vendor":       true,
	"node_modules": true,
}

// contractManifest mirrors the fields of parity/contracts/manifest.json the
// audit reads. Unknown fields are ignored on purpose so the gate keeps working
// when the manifest gains new keys.
type contractManifest struct {
	SchemaVersion int             `json:"schemaVersion"`
	Contracts     []contractEntry `json:"contracts"`
}

type contractEntry struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind"`
	Phase    string   `json:"phase"`
	Status   string   `json:"status"`
	GoPaths  []string `json:"goPaths"`
	Verifier string   `json:"verifier"`
}

// verifierNames splits the manifest's "Name;Name" verifier field.
func (c contractEntry) verifierNames() []string {
	names := make([]string, 0, 2)
	for _, part := range strings.Split(c.Verifier, ";") {
		if name := strings.TrimSpace(part); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// goFuncDeclPattern matches a top-level function declaration and captures its
// name and parameter list. Method declarations (`func (r *T) M(...)`) cannot
// match because a word is required directly after `func`, which keeps receiver
// parameter lists out of the index.
var goFuncDeclPattern = regexp.MustCompile(`(?m)^func[ \t]+([A-Za-z0-9_]+)[ \t]*\(([^)]*)\)`)

// contractVerifierAudit is one contract's audit result.
type contractVerifierAudit struct {
	ID             string
	Status         string
	GoPaths        []string
	Registered     int
	Hit            int
	Missing        []string
	MissingGoPaths []string
	Unregistered   bool
}

// loadContractManifest reads <root>/parity/contracts/manifest.json.
func loadContractManifest(root string) (*contractManifest, error) {
	path := filepath.Join(root, "parity", "contracts", "manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read contract manifest: %w", err)
	}
	var manifest contractManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("decode contract manifest: %w", err)
	}
	return &manifest, nil
}

// scanGoTestFunctions indexes every Go function in the tree whose signature
// takes a *testing.T, whatever package or file declares it.
func scanGoTestFunctions(root string) (map[string]bool, error) {
	found := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if contractVerifierAuditSkippedDirs[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, match := range goFuncDeclPattern.FindAllSubmatch(data, -1) {
			if !strings.Contains(string(match[2]), "*testing.T") {
				continue
			}
			found[string(match[1])] = true
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan Go sources under %s: %w", root, err)
	}
	return found, nil
}

// auditContractVerifiers resolves every registered verifier and every goPath of
// every contract against the tree at root. Results are ordered by contract id so
// the report is stable across runs.
func auditContractVerifiers(root string) ([]contractVerifierAudit, error) {
	manifest, err := loadContractManifest(root)
	if err != nil {
		return nil, err
	}
	declared, err := scanGoTestFunctions(root)
	if err != nil {
		return nil, err
	}
	results := make([]contractVerifierAudit, 0, len(manifest.Contracts))
	for _, contract := range manifest.Contracts {
		result := contractVerifierAudit{
			ID:      contract.ID,
			Status:  contract.Status,
			GoPaths: append([]string(nil), contract.GoPaths...),
		}
		names := contract.verifierNames()
		result.Registered = len(names)
		result.Unregistered = len(names) == 0
		for _, name := range names {
			if declared[name] {
				result.Hit++
				continue
			}
			result.Missing = append(result.Missing, name)
		}
		for _, goPath := range contract.GoPaths {
			trimmed := strings.TrimSpace(goPath)
			if trimmed == "" {
				continue
			}
			if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(trimmed))); statErr != nil {
				result.MissingGoPaths = append(result.MissingGoPaths, trimmed)
			}
		}
		results = append(results, result)
	}
	sort.Slice(results, func(i, j int) bool { return results[i].ID < results[j].ID })
	return results, nil
}
