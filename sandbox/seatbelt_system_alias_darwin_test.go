//go:build darwin

package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Keep the incoming paths in the system-alias namespace. Only the expected
// result is canonical; normalizing the fixture before the call hides the bug.
func TestSeatbeltSystemAliasesPreserveTrustedHomeAndAncestors(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if seatbeltAllowedSymlinkHomeCovers(filepath.Join(cwd, "nested"), "") {
		t.Fatal("an unset trusted home implicitly covered the working directory")
	}
	for _, alias := range []string{"/tmp", "/var/tmp"} {
		t.Run(strings.TrimPrefix(alias, "/"), func(t *testing.T) {
			physical, err := filepath.EvalSymlinks(alias)
			if err != nil {
				t.Fatal(err)
			}
			if physical == alias {
				t.Skipf("%s is not a system alias on this host", alias)
			}
			root, err := os.MkdirTemp(alias, "codex-seatbelt-alias-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.RemoveAll(root) })
			target := filepath.Join(root, "real", "nested")
			if err := os.MkdirAll(target, 0700); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(root, "visualizations")
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			canonicalRoot := filepath.Join(physical, filepath.Base(root))
			canonicalTarget := filepath.Join(canonicalRoot, "real", "nested")
			profile := WorkspaceWritePermissionProfile()
			policy := profile.LegacySandboxPolicy()
			policy.ExcludeSlashTmp, policy.ExcludeTmpdirEnvVar = true, true
			profile.SandboxPolicy = policy
			if _, _, err := buildSeatbeltPolicy(link, &profile, nil, ""); err == nil {
				t.Fatal("untrusted nested writable symlink was accepted")
			}
			_, parameters, err := buildSeatbeltPolicy(link, &profile, nil, canonicalRoot)
			if err != nil {
				t.Fatalf("aliased root under canonical trusted home: %v", err)
			}
			found := false
			for _, parameter := range parameters {
				if strings.HasPrefix(parameter.Name, "WRITABLE_ROOT_") && parameter.Value == canonicalTarget {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing canonical target %q in %#v", canonicalTarget, parameters)
			}
			// Coverage must not accept a lexical sibling such as home-other.
			if seatbeltAllowedSymlinkHomeCovers(root+"-other/visualizations", canonicalRoot) {
				t.Fatal("trusted home covered a sibling path")
			}
			metadata := filepath.Join(root, ".git")
			if err := os.Symlink(target, metadata); err != nil {
				t.Fatal(err)
			}
			ancestors := seatbeltProtectedAncestors(policy.GetWritableRootsWithCWD(root))
			for _, want := range []string{root, filepath.Join(canonicalRoot, "real")} {
				present := false
				for _, ancestor := range ancestors {
					if ancestor == want {
						present = true
					}
				}
				if !present {
					t.Fatalf("ancestors %#v missing %q", ancestors, want)
				}
			}
		})
	}
}
