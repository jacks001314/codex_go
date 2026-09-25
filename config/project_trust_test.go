package config

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Rust parity: codex-rs/config/src/project_trust_tests.rs (#47620).

func trustProjects(entries map[string]any) map[string]any {
	projects := map[string]any{}
	for key, level := range entries {
		project := map[string]any{}
		if level != nil {
			project["trust_level"] = level
		}
		projects[key] = project
	}
	return projects
}

// assertTrustLookupOrder removes each matching entry in turn and asserts the
// next one wins, ending with no match.
func assertTrustLookupOrder(t *testing.T, lookup ProjectTrustLookup, order []string, levels map[string]any) {
	t.Helper()
	projects := trustProjects(levels)
	for _, key := range order {
		project, ok := lookup.ActiveProject(projects)
		if !ok {
			t.Fatalf("ActiveProject() after removing %v = no match, want %q", order[:len(order)], key)
		}
		want := levels[key]
		got, hasLevel := project["trust_level"]
		if want == nil {
			if hasLevel {
				t.Fatalf("active project for %q = %#v, want no trust level", key, project)
			}
		} else if got != want {
			t.Fatalf("active project trust level = %#v, want %#v", got, want)
		}
		delete(projects, key)
	}
	if project, ok := lookup.ActiveProject(projects); ok {
		t.Fatalf("ActiveProject() = %#v, want no match", project)
	}
	if project, ok := lookup.ActiveProject(nil); ok {
		t.Fatalf("ActiveProject(nil) = %#v, want no match", project)
	}
}

func TestPosixProjectTrustLookupPreservesOrderAndCaseLikeRust(t *testing.T) {
	canonicalCWD := "/repo/src"
	canonicalRoot := "/repo"
	lookup := ProjectTrustLookupFromPaths(false,
		ProjectTrustPath{Original: "/alias/src", Canonical: &canonicalCWD},
		&ProjectTrustPath{Original: "/alias", Canonical: &canonicalRoot},
	)
	wantKeys := "/repo/src,/alias/src,/repo,/alias"
	if got := strings.Join(lookup.Keys(), ","); got != wantKeys {
		t.Fatalf("lookup keys = %q, want %q", got, wantKeys)
	}
	assertTrustLookupOrder(t, lookup,
		[]string{"/repo/src", "/alias/src", "/repo", "/alias"},
		map[string]any{
			"/repo/src":  nil,
			"/alias/src": "trusted",
			"/repo":      "untrusted",
			"/alias":     "trusted",
		},
	)
	// POSIX matching stays case-sensitive.
	if project, ok := lookup.ActiveProject(trustProjects(map[string]any{"/Repo/src": "trusted"})); ok {
		t.Fatalf("case-different key matched on POSIX: %#v", project)
	}
}

func TestWindowsProjectTrustLookupPrefersExactThenSortedAliasesLikeRust(t *testing.T) {
	lookup := ProjectTrustLookupFromPaths(true,
		ProjectTrustPath{Original: `C:\Repo`},
		nil,
	)
	if got := strings.Join(lookup.Keys(), ","); got != `c:\repo` {
		t.Fatalf("windows lookup keys = %q", got)
	}
	assertTrustLookupOrder(t, lookup,
		[]string{`c:\repo`, `C:\REPO`, `C:\Repo`},
		map[string]any{
			`c:\repo`: "trusted",
			`C:\REPO`: "untrusted",
			`C:\Repo`: "trusted",
		},
	)
}

func TestProjectTrustLookupFromNativePathLikeRust(t *testing.T) {
	dir := t.TempDir()
	lookup := ProjectTrustLookupFromNativePath(dir)
	keys := lookup.Keys()
	// Lookup keys are normalized for the host convention, so a Windows path is
	// lowercased (Rust normalize_lookup_key).
	wantLast := strings.TrimSpace(dir)
	if runtime.GOOS == "windows" {
		wantLast = strings.ToLower(wantLast)
	}
	if len(keys) == 0 || strings.TrimSpace(keys[len(keys)-1]) != wantLast {
		t.Fatalf("native lookup keys = %#v, want %q last", keys, wantLast)
	}
	cfg := &Config{Values: map[string]any{"projects": trustProjects(map[string]any{dir: "trusted"})}}
	level, ok := cfg.ProjectTrustLevelForLookup(lookup)
	if !ok || level != "trusted" {
		t.Fatalf("ProjectTrustLevelForLookup() = %q, %v", level, ok)
	}

	// A cwd entry without a trust level still takes precedence, and reports no
	// explicit decision.
	cfg = &Config{Values: map[string]any{"projects": trustProjects(map[string]any{dir: nil})}}
	if level, ok := cfg.ProjectTrustLevelForLookup(lookup); ok || level != "" {
		t.Fatalf("entry without a trust level = %q, %v, want undecided", level, ok)
	}

	// ProjectTrustLevelForTarget keeps its target-level contract.
	if level, ok := ProjectTrustLevelForTarget(cfg.Values, dir); ok || level != "" {
		t.Fatalf("ProjectTrustLevelForTarget(no level) = %q, %v", level, ok)
	}
	if level, ok := ProjectTrustLevelForTarget(map[string]any{"projects": trustProjects(map[string]any{dir: "untrusted"})}, dir); !ok || level != "untrusted" {
		t.Fatalf("ProjectTrustLevelForTarget(untrusted) = %q, %v", level, ok)
	}
	if _, ok := ProjectTrustLevelForTarget(map[string]any{}, dir); ok {
		t.Fatal("ProjectTrustLevelForTarget(no projects) reported a decision")
	}
}

// TestProjectTrustLookupForTargetPrefersCwdOverRepositoryRootLikeRust pins the
// cwd-before-repository-root precedence of the portable lookup (#47620).
func TestProjectTrustLookupForTargetPrefersCwdOverRepositoryRootLikeRust(t *testing.T) {
	root := strings.TrimSpace(t.TempDir())
	cwd := filepath.Join(root, "src")
	lookup := ProjectTrustLookupForTarget(cwd, root)
	keys := lookup.Keys()
	if len(keys) < 2 {
		t.Fatalf("lookup keys = %#v, want the cwd and root spellings", keys)
	}
	if keys[0] != normalizeTrustKeyForTest(cwd) || keys[len(keys)-1] != normalizeTrustKeyForTest(root) {
		t.Fatalf("lookup keys = %#v, want the cwd keys before the root keys", keys)
	}
	cfg := &Config{Values: map[string]any{"projects": trustProjects(map[string]any{
		cwd:  "trusted",
		root: "untrusted",
	})}}
	// The cwd entry wins even though the repository root also matches.
	if level, ok := cfg.ProjectTrustLevelForLookup(lookup); !ok || level != "trusted" {
		t.Fatalf("cwd precedence = %q, %v, want trusted", level, ok)
	}
	delete(cfg.Values["projects"].(map[string]any), cwd)
	if level, ok := cfg.ProjectTrustLevelForLookup(lookup); !ok || level != "untrusted" {
		t.Fatalf("root fallback = %q, %v, want untrusted", level, ok)
	}
}

func normalizeTrustKeyForTest(path string) string {
	path = strings.TrimSpace(path)
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}
