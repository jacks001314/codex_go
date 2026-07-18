package windowssandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGatherAuditCandidatesIncludesPathEntries(t *testing.T) {
	tmp := t.TempDir()
	dirA := filepath.Join(tmp, "Tools")
	dirB := filepath.Join(tmp, "Bin")
	dirSpace := filepath.Join(tmp, "Program Files")
	for _, dir := range []string{dirA, dirB, dirSpace} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", dir, err)
		}
	}
	env := map[string]string{
		"PATH": dirA + string(os.PathListSeparator) + dirB + string(os.PathListSeparator) + dirSpace,
	}
	got := gatherAuditCandidates(tmp, env)
	for _, want := range []string{dirA, dirB, dirSpace} {
		if !containsCanonical(got, want) {
			t.Fatalf("gatherAuditCandidates() = %#v, want %q", got, want)
		}
	}
}

func TestWorldWritableAuditResultSamplesAndCountsExtra(t *testing.T) {
	var paths []string
	for i := 0; i < worldWritableAuditSampleLimit+3; i++ {
		paths = append(paths, filepath.Join("C:\\audit", string(rune('a'+i))))
	}
	result := worldWritableAuditResult(paths, true)
	if len(result.SamplePaths) != worldWritableAuditSampleLimit {
		t.Fatalf("SamplePaths len = %d, want %d", len(result.SamplePaths), worldWritableAuditSampleLimit)
	}
	if result.ExtraCount != 3 {
		t.Fatalf("ExtraCount = %d, want 3", result.ExtraCount)
	}
	if !result.FailedScan {
		t.Fatalf("FailedScan = false, want true")
	}
}
