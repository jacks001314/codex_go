package sandbox

import (
	"errors"
	"testing"
)

// Mirrors Rust #43939 target_tests.rs: read denials resolve against the
// executor's own paths and home, not the controller's environment.
func TestPreparedReadDenialsUseExecutorPathsLikeRust(t *testing.T) {
	for _, base := range []string{
		"/work/repo",
		"/Users/agent/repo",
		`C:\work\repo`,
		`//server/share/repo`,
	} {
		context := denyReadTestContext(base, base)
		entries := []FileSystemSandboxEntry{
			denyReadTestDeny(FileSystemPath{Type: "path", Path: base + "/private"}),
			denyReadTestDeny(FileSystemPath{Type: "glob_pattern", Pattern: "~/secrets/*.key"}),
			denyReadTestDeny(FileSystemPath{Type: "glob_pattern", Pattern: "nested/**/*.env"}),
		}
		matcher, err := NewPreparedReadDenials(entries, context)
		if err != nil {
			t.Fatalf("%s: NewPreparedReadDenials: %v", base, err)
		}
		if matcher == nil {
			t.Fatalf("%s: expected a prepared matcher", base)
		}
		for _, testCase := range []struct {
			path string
			want bool
		}{
			{base + "/private", true},
			{base + "/private/file", true},
			{base + "/private-other/file", false},
			{base + "/secrets/token.key", true},
			{base + "/secrets/nested/token.key", false},
			{base + "/nested/.env", true},
			{base + "/nested/deep/token.env", true},
			{base + "/public/file", false},
		} {
			if got := matcher.IsReadDenied(testCase.path); got != testCase.want {
				t.Errorf("%s: IsReadDenied(%q) = %v, want %v", base, testCase.path, got, testCase.want)
			}
		}
		windows := denyReadTestConvention(base) == "windows"
		if got := matcher.IsReadDenied(base + "/PRIVATE/file"); got != windows {
			t.Errorf("%s: case-folded denial = %v, want %v", base, got, windows)
		}
		foreign := `C:\foreign\path`
		if windows {
			foreign = "/foreign/path"
		}
		if !matcher.IsReadDenied(foreign) {
			t.Errorf("%s: foreign-convention candidate must be denied", base)
		}
	}
}

// Mirrors Rust #43939 managed_denials_reject_concrete_read_and_write_grants.
func TestValidateManagedDenyReadRejectsConcreteGrantsLikeRust(t *testing.T) {
	for _, base := range []string{
		"/work/repo",
		`C:\work\repo`,
		`//server/share/repo`,
	} {
		context := denyReadTestContext(base, "")
		private := base + "/private"
		required := []FileSystemSandboxEntry{denyReadTestDeny(FileSystemPath{Type: "path", Path: private})}
		selected := []FileSystemSandboxEntry{
			{Path: FileSystemPath{Type: "special", Value: &FileSystemSpecialPath{Kind: "root"}}, Access: FileSystemAccessRead},
			{Path: FileSystemPath{Type: "special", Value: &FileSystemSpecialPath{Kind: "project_roots"}}, Access: FileSystemAccessWrite},
			{Path: FileSystemPath{Type: "path", Path: base}, Access: FileSystemAccessWrite},
			required[0],
		}
		if err := ValidateManagedDenyRead(required, selected, context); err != nil {
			t.Fatalf("%s: unrelated workspace root must validate: %v", base, err)
		}
		for _, access := range []FileSystemAccessMode{FileSystemAccessRead, FileSystemAccessWrite} {
			carveout := append(append([]FileSystemSandboxEntry(nil), selected...), FileSystemSandboxEntry{
				Path:   FileSystemPath{Type: "path", Path: private + "/carveout"},
				Access: access,
			})
			err := ValidateManagedDenyRead(required, carveout, context)
			if err == nil || err.Error() != DenyReadShadowedMessage {
				t.Fatalf("%s/%s: got %v, want shadowed violation", base, access, err)
			}
		}
	}
}

// Mirrors Rust #43939: Core reports the first conflicting path even when a
// required entry is missing too, then falls back to the missing-entry report.
func TestDenyReadValidatorPreservesViolationPrecedenceLikeRust(t *testing.T) {
	base := "/work/repo"
	context := denyReadTestContext(base, "")
	required := []FileSystemSandboxEntry{denyReadTestDeny(FileSystemPath{Type: "path", Path: base + "/private"})}
	validator, err := NewDenyReadValidator(required, context)
	if err != nil {
		t.Fatalf("NewDenyReadValidator: %v", err)
	}
	violation := validator.Validate(nil, context)
	if violation == nil || violation.Kind != DenyReadViolationMissingRequired {
		t.Fatalf("violation = %+v, want missing required", violation)
	}
	selected := []FileSystemSandboxEntry{
		{Path: FileSystemPath{Type: "path", Path: base + "/private/child"}, Access: FileSystemAccessRead},
	}
	violation = validator.Validate(selected, context)
	if violation == nil || violation.Kind != DenyReadViolationReadablePath || violation.Path != base+"/private/child" {
		t.Fatalf("violation = %+v, want readable path first", violation)
	}
	if err := ValidateManagedDenyRead(required, nil, context); err == nil {
		t.Fatal("missing required denial must fail managed validation")
	}
}

// Mirrors Rust #43939 malformed_or_unresolvable_denials_fail_closed.
func TestPreparedReadDenialsFailClosedLikeRust(t *testing.T) {
	context := denyReadTestContext("/work/repo", "")
	for _, pattern := range []string{"~/secrets/**", "secrets/[z-a]", ""} {
		entries := []FileSystemSandboxEntry{denyReadTestDeny(FileSystemPath{Type: "glob_pattern", Pattern: pattern})}
		if _, err := NewPreparedReadDenials(entries, context); err == nil {
			t.Fatalf("pattern %q must fail closed", pattern)
		}
	}
	foreign := []FileSystemSandboxEntry{denyReadTestDeny(FileSystemPath{Type: "path", Path: `C:\private`})}
	if _, err := NewPreparedReadDenials(foreign, context); err == nil {
		t.Fatal("foreign-convention denial must fail closed")
	}
}

// Mirrors Rust #43939 slash_tmp_denials_follow_the_executor_convention.
func TestPreparedReadDenialsApplySlashTmpByConventionLikeRust(t *testing.T) {
	entries := []FileSystemSandboxEntry{denyReadTestDeny(FileSystemPath{Type: "special", Value: &FileSystemSpecialPath{Kind: "slash_tmp"}})}
	posix, err := NewPreparedReadDenials(entries, denyReadTestContext("/work", ""))
	if err != nil {
		t.Fatalf("POSIX preparation: %v", err)
	}
	if posix == nil || !posix.IsReadDenied("/tmp/file") {
		t.Fatal("POSIX :slash_tmp must deny /tmp contents")
	}
	windows, err := NewPreparedReadDenials(entries, denyReadTestContext(`C:\work`, ""))
	if err != nil {
		t.Fatalf("Windows preparation: %v", err)
	}
	if windows != nil {
		t.Fatal("Windows executor must ignore symbolic :slash_tmp")
	}
}

// Mirrors Rust #43939: temporary-directory denials need executor metadata.
func TestPreparedReadDenialsRequireTemporaryDirectoryMetadataLikeRust(t *testing.T) {
	entries := []FileSystemSandboxEntry{denyReadTestDeny(FileSystemPath{Type: "special", Value: &FileSystemSpecialPath{Kind: "tmpdir"}})}
	context := denyReadTestContext("/work", "")
	context.TemporaryDirectories = nil
	if _, err := NewPreparedReadDenials(entries, context); err == nil || err.Error() != denyReadTmpdirUnavailableMessage {
		t.Fatalf("missing metadata error = %v", err)
	}
	empty := []string{}
	context.TemporaryDirectories = &empty
	if _, err := NewPreparedReadDenials(entries, context); err == nil || err.Error() != denyReadTmpdirInvalidMessage {
		t.Fatalf("empty metadata error = %v", err)
	}
	directories := []string{"/var/tmp/executor"}
	context.TemporaryDirectories = &directories
	matcher, err := NewPreparedReadDenials(entries, context)
	if err != nil {
		t.Fatalf("preparation: %v", err)
	}
	if matcher == nil || !matcher.IsReadDenied("/var/tmp/executor/file") {
		t.Fatal(":tmpdir denial must deny the executor temporary directory")
	}
}

// Mirrors Rust #43939: project-root denials materialize every workspace root.
func TestPreparedReadDenialsMaterializeProjectRootsLikeRust(t *testing.T) {
	subpath := "private"
	entries := []FileSystemSandboxEntry{denyReadTestDeny(FileSystemPath{
		Type:  "special",
		Value: &FileSystemSpecialPath{Kind: "project_roots", Subpath: &subpath},
	})}
	context := DenyReadContext{CWD: "/work/one", WorkspaceRoots: []string{"/work/one", "/work/two"}}
	matcher, err := NewPreparedReadDenials(entries, context)
	if err != nil {
		t.Fatalf("preparation: %v", err)
	}
	for _, path := range []string{"/work/one/private/file", "/work/two/private/file"} {
		if !matcher.IsReadDenied(path) {
			t.Fatalf("%s must be denied", path)
		}
	}
	context.WorkspaceRoots = nil
	if _, err := NewPreparedReadDenials(entries, context); err == nil {
		t.Fatal("project-root denial without workspace roots must fail closed")
	}
}

// Mirrors Rust #43939 can_read_path / get_unreadable_globs_with_context.
func TestContextReadAccessAndGlobResolutionLikeRust(t *testing.T) {
	context := denyReadTestContext("/work/repo", "/home/agent")
	entries := []FileSystemSandboxEntry{
		{Path: FileSystemPath{Type: "special", Value: &FileSystemSpecialPath{Kind: "root"}}, Access: FileSystemAccessRead},
		{Path: FileSystemPath{Type: "path", Path: "/work/repo/private"}, Access: FileSystemAccessDeny},
		{Path: FileSystemPath{Type: "glob_pattern", Pattern: "~/secrets/*.key"}, Access: FileSystemAccessDeny},
	}
	if !CanReadPathWithContext(entries, "/work/repo/public/file", context) {
		t.Fatal("root read entry must allow public reads")
	}
	if CanReadPathWithContext(entries, `C:\foreign\file`, context) {
		t.Fatal("foreign-convention path must not be readable")
	}
	if CanReadPathWithContext(entries, "/work/repo/file", DenyReadContext{}) {
		t.Fatal("unknown convention must fail closed")
	}
	globs, err := ReadDenyGlobsWithContext(entries, context)
	if err != nil {
		t.Fatalf("ReadDenyGlobsWithContext: %v", err)
	}
	if len(globs) != 1 || globs[0] != "/home/agent/secrets/*.key" {
		t.Fatalf("globs = %#v, want executor-home expansion", globs)
	}
	withoutHome := entries
	if _, err := ReadDenyGlobsWithContext(withoutHome, denyReadTestContext("/work/repo", "")); err == nil {
		t.Fatal("home-relative glob without executor home must fail closed")
	}
}

// Mirrors Rust #43939: no applicable denial means no matcher, and the shared
// validator is inert for an unrelated executor context.
func TestPreparedReadDenialsSkipInapplicableDenialsLikeRust(t *testing.T) {
	entries := []FileSystemSandboxEntry{{Path: FileSystemPath{Type: "path", Path: "/work/private"}, Access: FileSystemAccessRead}}
	matcher, err := NewPreparedReadDenials(entries, denyReadTestContext("/work", ""))
	if err != nil {
		t.Fatalf("preparation: %v", err)
	}
	if matcher != nil {
		t.Fatal("read-only entries must not produce a deny matcher")
	}
	validator, err := NewDenyReadValidator(nil, denyReadTestContext("/work", ""))
	if err != nil {
		t.Fatalf("NewDenyReadValidator: %v", err)
	}
	if violation := validator.Validate(nil, denyReadTestContext("/work", "")); violation != nil {
		t.Fatalf("empty requirement violation = %+v", violation)
	}
	if err := ValidateManagedDenyRead(nil, entries, denyReadTestContext("/work", "")); err != nil {
		t.Fatalf("no managed denial must not constrain: %v", err)
	}
	_ = errors.Is
}

func denyReadTestContext(cwd, home string) DenyReadContext {
	empty := []string{}
	return DenyReadContext{
		CWD:                  cwd,
		WorkspaceRoots:       []string{cwd},
		UserHomeDir:          home,
		TemporaryDirectories: &empty,
	}
}

func denyReadTestDeny(path FileSystemPath) FileSystemSandboxEntry {
	return FileSystemSandboxEntry{Path: path, Access: FileSystemAccessDeny}
}

func denyReadTestConvention(path string) string {
	inferred, ok := denyReadAbsoluteConvention(path)
	if !ok {
		return ""
	}
	return string(inferred)
}
