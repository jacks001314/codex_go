package sandboxpath

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestWritableRootCaseVariantMetadataLikeRust mirrors the case-variant
// regression Rust #54250 added as
// `seatbelt_protects_case_variants_of_metadata`: on a case-insensitive
// filesystem `.GIT` is the same directory as `.git`, so a write through the
// uppercase spelling must stay refused while a sibling workspace file remains
// writable, both for a metadata directory that already exists (either casing)
// and for one a patch would still have to create.
//
// Rust leans on the Seatbelt kernel to resolve the alias. The Go port has no
// Seatbelt: the alias is resolved by the case-insensitive containment
// comparison `PathWithin` performs (`filepath.Rel` is case-insensitive on
// Windows), so the `.git` read-only carveout covers `.GIT/config` and `.GIT`
// itself. Filesystems without the alias are skipped, exactly like the Rust
// fixture returns early when `<tmp>/WORKSPACE` is not a directory.
func TestWritableRootCaseVariantMetadataLikeRust(t *testing.T) {
	for _, name := range DefaultProtectedMetadataNames() {
		upper := strings.ToUpper(name)
		for _, existingCase := range []string{"absent", "lowercase", "uppercase"} {
			for _, rootName := range []string{"workspace", "WORKSPACE"} {
				t.Run(name+"_"+existingCase+"_"+rootName, func(t *testing.T) {
					temp := t.TempDir()
					workspace := filepath.Join(temp, "workspace")
					if err := os.MkdirAll(workspace, 0o755); err != nil {
						t.Fatalf("MkdirAll(%q) error = %v", workspace, err)
					}
					if _, err := os.Stat(filepath.Join(temp, "WORKSPACE")); err != nil {
						t.Skip("the reported aliases require a case-insensitive filesystem")
					}
					if existingCase != "absent" {
						directory := filepath.Join(workspace, name)
						if existingCase == "uppercase" {
							directory = filepath.Join(workspace, upper)
						}
						if err := os.MkdirAll(directory, 0o755); err != nil {
							t.Fatalf("MkdirAll(%q) error = %v", directory, err)
						}
						if err := os.WriteFile(filepath.Join(directory, "config"), []byte("# original\n"), 0o644); err != nil {
							t.Fatalf("WriteFile(%q) error = %v", filepath.Join(directory, "config"), err)
						}
					}

					roots := WritableRootsWithProtectedSubpaths([]string{filepath.Join(temp, rootName)})
					writable := func(path string) bool {
						for index := range roots {
							if roots[index].IsPathWritable(path) {
								return true
							}
						}
						return false
					}

					// Control: an ordinary workspace file stays writable so the
					// protected assertion below cannot pass by blanket refusal.
					if !writable(filepath.Join(workspace, "notes.txt")) {
						t.Fatalf("IsPathWritable(%q) = false, want the sibling workspace file writable", filepath.Join(workspace, "notes.txt"))
					}
					// Protected: the uppercase alias is read-only in both its
					// existing and its not-yet-created form.
					if writable(filepath.Join(workspace, upper, "config")) {
						t.Fatalf("IsPathWritable(%q) = true, want the case variant of %q to stay read-only", filepath.Join(workspace, upper, "config"), name)
					}
					if writable(filepath.Join(workspace, upper)) {
						t.Fatalf("IsPathWritable(%q) = true, want the case variant of %q to stay read-only", filepath.Join(workspace, upper), name)
					}
				})
			}
		}
	}
}

// TestProtectedMetadataNameGateCaseRules pins the gate itself: Rust compares
// the first path component with `==`
// (`protocol::WritableRoot::path_contains_protected_metadata_name`,
// permissions.rs PROTECTED_METADATA_PATH_NAMES) because Seatbelt resolves the
// alias in the kernel. Go has no Seatbelt kernel boundary in this predicate, so
// Windows and case-insensitive macOS filesystems refuse the alias directly;
// otherwise a missing `.git` would let `.GIT` create the real metadata directory.
func TestProtectedMetadataNameGateCaseRules(t *testing.T) {
	temp := t.TempDir()
	workspace := filepath.Join(temp, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", workspace, err)
	}
	if _, err := os.Stat(filepath.Join(temp, "WORKSPACE")); err != nil {
		t.Skip("the reported aliases require a case-insensitive filesystem")
	}
	bare := WritableRoot{Root: workspace, ProtectedMetadataNames: DefaultProtectedMetadataNames()}
	if bare.IsPathWritable(filepath.Join(workspace, ".git", "config")) {
		t.Fatalf("IsPathWritable(%q) = true, want the exact metadata name refused by the name gate", filepath.Join(workspace, ".git", "config"))
	}
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		if bare.IsPathWritable(filepath.Join(workspace, ".GIT", "config")) {
			t.Fatal("case-insensitive path identity must refuse metadata case variants")
		}
		return
	}
	if !bare.IsPathWritable(filepath.Join(workspace, ".GIT", "config")) {
		t.Fatal("the name gate mirrors Rust's case-sensitive comparison, so without a carveout the case variant is writable")
	}
}
