package parity

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	candidateRustFrom = "85fc4def358b7df21883e72ae8dda43a0f572f32"
	candidateRustTo   = "6c108912eeacabfc82723bf44f8a23f6e2f86585"
	candidateGoStart  = "df0a351b4454c48b4ea17995117407aacab4acf4"
)

type alignmentBaseline struct {
	SchemaVersion int    `json:"schemaVersion"`
	FrozenAt      string `json:"frozenAt"`
	Plan          string `json:"plan"`
	Verified      struct {
		RustCommit     string `json:"rustCommit"`
		ParityManifest string `json:"parityManifest"`
	} `json:"verified"`
	Target struct {
		RustCommit    string `json:"rustCommit"`
		RustBranch    string `json:"rustBranch"`
		GoStartCommit string `json:"goStartCommit"`
	} `json:"target"`
	Requirements struct {
		MinimumSQLiteVersion                string   `json:"minimumSQLiteVersion"`
		ZeroExceptions                      bool     `json:"zeroExceptions"`
		RequiredPlatforms                   []string `json:"requiredPlatforms"`
		RequiredConsecutiveDifferentialRuns int      `json:"requiredConsecutiveDifferentialRuns"`
		RequiredConsecutiveSDKRuns          int      `json:"requiredConsecutiveSDKRuns"`
	} `json:"requirements"`
	CertificationReady bool `json:"certificationReady"`
}

type alignmentCommitLedger struct {
	SchemaVersion     int               `json:"schemaVersion"`
	RustFromExclusive string            `json:"rustFromExclusive"`
	RustToInclusive   string            `json:"rustToInclusive"`
	Commits           []alignmentCommit `json:"commits"`
}

type alignmentCommit struct {
	SHA      string   `json:"sha"`
	Subject  string   `json:"subject"`
	Kind     string   `json:"kind"`
	Domains  []string `json:"domains"`
	Status   string   `json:"status"`
	Evidence []string `json:"evidence"`
}

type alignmentDomainManifest struct {
	SchemaVersion    int               `json:"schemaVersion"`
	TargetRustCommit string            `json:"targetRustCommit"`
	Domains          []alignmentDomain `json:"domains"`
}

type alignmentDomain struct {
	ID         string   `json:"id"`
	Severity   string   `json:"severity"`
	Phase      string   `json:"phase"`
	Owner      string   `json:"owner"`
	Status     string   `json:"status"`
	Acceptance string   `json:"acceptance"`
	Evidence   []string `json:"evidence"`
}

type alignmentContractManifest struct {
	SchemaVersion    int                 `json:"schemaVersion"`
	TargetRustCommit string              `json:"targetRustCommit"`
	Contracts        []alignmentContract `json:"contracts"`
}

type alignmentContract struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	RustPaths []string `json:"rustPaths"`
	GoPaths   []string `json:"goPaths"`
	Phase     string   `json:"phase"`
	Status    string   `json:"status"`
	Verifier  string   `json:"verifier"`
}

func TestAlignmentBaselineIsFrozenAndTraceable(t *testing.T) {
	baseline := readAlignmentJSON[alignmentBaseline](t, "baseline.json")
	if baseline.SchemaVersion != 1 || baseline.FrozenAt == "" || baseline.Plan == "" {
		t.Fatalf("invalid alignment baseline header: %#v", baseline)
	}
	if baseline.Verified.RustCommit != candidateRustTo || baseline.Verified.ParityManifest != "parity.json" {
		t.Fatalf("verified baseline = %#v", baseline.Verified)
	}
	if baseline.Target.RustCommit != candidateRustTo || baseline.Target.RustBranch != "main" || baseline.Target.GoStartCommit != candidateGoStart {
		t.Fatalf("candidate target = %#v", baseline.Target)
	}
	if baseline.Requirements.MinimumSQLiteVersion != "3.51.3" || !baseline.Requirements.ZeroExceptions {
		t.Fatalf("candidate requirements = %#v", baseline.Requirements)
	}
	if len(baseline.Requirements.RequiredPlatforms) != 6 || baseline.Requirements.RequiredConsecutiveDifferentialRuns != 3 || baseline.Requirements.RequiredConsecutiveSDKRuns != 3 {
		t.Fatalf("candidate certification gates = %#v", baseline.Requirements)
	}
	plan := readRepoFile(t, baseline.Plan)
	readme := readRepoFile(t, "README.md")
	for name, data := range map[string][]byte{"plan": plan, "README": readme} {
		if !strings.Contains(string(data), candidateRustTo) {
			t.Fatalf("%s does not reference candidate Rust target %s", name, candidateRustTo)
		}
	}

	var certified parityManifest
	readJSONFile(t, filepath.Join("..", baseline.Verified.ParityManifest), &certified)
	if certified.RustUpstreamHead != baseline.Verified.RustCommit {
		t.Fatalf("certified parity manifest points at %q, verified baseline records %q", certified.RustUpstreamHead, baseline.Verified.RustCommit)
	}
}

func TestAlignmentCommitLedgerIsComplete(t *testing.T) {
	ledger := readAlignmentJSON[alignmentCommitLedger](t, "commits.json")
	if ledger.SchemaVersion != 1 || ledger.RustFromExclusive != candidateRustFrom || ledger.RustToInclusive != candidateRustTo {
		t.Fatalf("invalid commit ledger header: %#v", ledger)
	}
	if len(ledger.Commits) != 21 {
		t.Fatalf("candidate commit count = %d, want 21", len(ledger.Commits))
	}
	shaPattern := regexp.MustCompile(`^[0-9a-f]{40}$`)
	allowedStatus := map[string]bool{
		"audit_pending":          true,
		"implementation_pending": true,
		"regression_pending":     true,
		"complete":               true,
	}
	allowedKind := map[string]bool{"test": true, "runtime": true, "protocol": true, "persistence": true, "security": true, "release": true}
	seen := map[string]bool{}
	for _, commit := range ledger.Commits {
		if !shaPattern.MatchString(commit.SHA) || seen[commit.SHA] || commit.Subject == "" || !allowedKind[commit.Kind] || len(commit.Domains) == 0 || !allowedStatus[commit.Status] {
			t.Fatalf("invalid commit ledger entry: %#v", commit)
		}
		seen[commit.SHA] = true
		if commit.Status == "complete" && len(commit.Evidence) == 0 {
			t.Fatalf("complete commit has no evidence: %s", commit.SHA)
		}
	}
	if ledger.Commits[0].SHA != "3c7ae4a81204cbee45084b4fb43b3630c79550c9" || ledger.Commits[len(ledger.Commits)-1].SHA != candidateRustTo {
		t.Fatalf("candidate commit ledger boundary mismatch: first=%s last=%s", ledger.Commits[0].SHA, ledger.Commits[len(ledger.Commits)-1].SHA)
	}
}

func TestAlignmentCommitLedgerMatchesRustGitRange(t *testing.T) {
	root := rustSnapshotRoot(t)
	cmd := exec.Command("git", "-C", filepath.Dir(root), "rev-list", "--reverse", candidateRustFrom+".."+candidateRustTo)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-list candidate range: %v", err)
	}
	got := strings.Fields(string(output))
	ledger := readAlignmentJSON[alignmentCommitLedger](t, "commits.json")
	if len(got) != len(ledger.Commits) {
		t.Fatalf("Rust git range has %d commits, ledger has %d", len(got), len(ledger.Commits))
	}
	for i := range got {
		if got[i] != ledger.Commits[i].SHA {
			t.Fatalf("commit ledger mismatch at %d: got %s want %s", i, ledger.Commits[i].SHA, got[i])
		}
	}
}

func TestAlignmentDomainsCannotOverclaimCompletion(t *testing.T) {
	manifest := readAlignmentJSON[alignmentDomainManifest](t, "domains.json")
	if manifest.SchemaVersion != 1 || manifest.TargetRustCommit != candidateRustTo || len(manifest.Domains) == 0 {
		t.Fatalf("invalid domain manifest header: %#v", manifest)
	}
	allowedStatus := map[string]bool{"missing": true, "partial": true, "equivalent": true, "complete": true, "not_applicable": true}
	allowedSeverity := map[string]bool{"P0": true, "P1": true, "P2": true, "P3": true}
	phasePattern := regexp.MustCompile(`^P[0-8]$`)
	seen := map[string]bool{}
	open := 0
	for _, domain := range manifest.Domains {
		if domain.ID == "" || seen[domain.ID] || !allowedSeverity[domain.Severity] || !phasePattern.MatchString(domain.Phase) || domain.Owner == "" || domain.Acceptance == "" || !allowedStatus[domain.Status] {
			t.Fatalf("invalid alignment domain: %#v", domain)
		}
		seen[domain.ID] = true
		switch domain.Status {
		case "complete", "equivalent", "not_applicable":
			if len(domain.Evidence) == 0 {
				t.Fatalf("closed domain %q has no evidence", domain.ID)
			}
		default:
			open++
		}
	}
	_ = open
}

func TestAlignmentCertificationReadinessIsDerivedFromEvidence(t *testing.T) {
	baseline := readAlignmentJSON[alignmentBaseline](t, "baseline.json")
	commits := readAlignmentJSON[alignmentCommitLedger](t, "commits.json")
	domains := readAlignmentJSON[alignmentDomainManifest](t, "domains.json")
	contracts := readAlignmentJSON[alignmentContractManifest](t, filepath.Join("contracts", "manifest.json"))

	ready := true
	for _, commit := range commits.Commits {
		if commit.Status != "complete" || len(commit.Evidence) == 0 {
			ready = false
		}
	}
	for _, domain := range domains.Domains {
		switch domain.Status {
		case "complete", "equivalent", "not_applicable":
			if len(domain.Evidence) == 0 {
				ready = false
			}
		default:
			ready = false
		}
	}
	for _, contract := range contracts.Contracts {
		if contract.Status != "complete" || contract.Verifier == "pending" {
			ready = false
		}
	}
	if baseline.CertificationReady != ready {
		t.Fatalf("certificationReady=%v, derived readiness=%v", baseline.CertificationReady, ready)
	}
}

func TestAlignmentContractInventoryIsTraceable(t *testing.T) {
	manifest := readAlignmentJSON[alignmentContractManifest](t, filepath.Join("contracts", "manifest.json"))
	if manifest.SchemaVersion != 1 || manifest.TargetRustCommit != candidateRustTo || len(manifest.Contracts) == 0 {
		t.Fatalf("invalid contract manifest header: %#v", manifest)
	}
	allowedStatus := map[string]bool{"missing": true, "partial": true, "complete": true}
	seen := map[string]bool{}
	for _, contract := range manifest.Contracts {
		if contract.ID == "" || seen[contract.ID] || contract.Kind == "" || len(contract.RustPaths) == 0 || len(contract.GoPaths) == 0 || contract.Phase == "" || !allowedStatus[contract.Status] || contract.Verifier == "" {
			t.Fatalf("invalid alignment contract: %#v", contract)
		}
		seen[contract.ID] = true
		for _, path := range contract.GoPaths {
			if _, err := os.Stat(filepath.Join("..", filepath.FromSlash(path))); err != nil {
				t.Fatalf("Go contract path %s for %s: %v", path, contract.ID, err)
			}
		}
		if contract.Status == "complete" && contract.Verifier == "pending" {
			t.Fatalf("complete contract %q has no verifier", contract.ID)
		}
	}
}

func TestAlignmentRustContractOraclePathsExist(t *testing.T) {
	root := rustSnapshotRoot(t)
	manifest := readAlignmentJSON[alignmentContractManifest](t, filepath.Join("contracts", "manifest.json"))
	for _, contract := range manifest.Contracts {
		for _, path := range contract.RustPaths {
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); err != nil {
				t.Fatalf("Rust contract path %s for %s: %v", path, contract.ID, err)
			}
		}
	}
}

func readAlignmentJSON[T any](t *testing.T, path string) T {
	t.Helper()
	var value T
	readJSONFile(t, path, &value)
	return value
}

func readJSONFile(t *testing.T, path string, target any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatalf("Unmarshal(%s): %v", path, err)
	}
}

func readRepoFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", filepath.FromSlash(path)))
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	return data
}
