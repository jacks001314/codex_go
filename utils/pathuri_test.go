package utils

import (
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestParseFileURIAndBasename(t *testing.T) {
	uri, err := Parse("file:///home/alice/a%20file.rs")
	if err != nil {
		t.Fatal(err)
	}
	if uri.EncodedPath() != "/home/alice/a%20file.rs" {
		t.Fatalf("encoded path = %q", uri.EncodedPath())
	}
	if base, ok := uri.Basename(); !ok || base != "a file.rs" {
		t.Fatalf("basename = %q %v", base, ok)
	}
	if uri.String() != "file:///home/alice/a%20file.rs" {
		t.Fatalf("string = %q", uri.String())
	}
}

func TestInferAndRenderNativePathString(t *testing.T) {
	cases := map[string]string{
		"file:///home/alice/a%20file.rs": "/home/alice/a file.rs",
		"file:///C:/Users/Alice/main.rs": `C:\Users\Alice\main.rs`,
		"file://server/share/main.rs":    `\\server\share\main.rs`,
	}
	for raw, want := range cases {
		uri, err := Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := uri.NativePathString(); got != want {
			t.Fatalf("%s native = %q want %q", raw, got, want)
		}
	}
}

func TestParentAncestorsJoinAndStartsWith(t *testing.T) {
	uri, _ := Parse("file:///workspace/src/lib.rs")
	parent, ok := uri.Parent()
	if !ok || parent.String() != "file:///workspace/src" {
		t.Fatalf("parent = %v %v", parent, ok)
	}
	ancestors := uri.Ancestors()
	got := make([]string, len(ancestors))
	for i := range ancestors {
		got[i] = ancestors[i].String()
	}
	want := []string{"file:///workspace/src/lib.rs", "file:///workspace/src", "file:///workspace", "file:///"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ancestors = %#v", got)
	}
	joined, err := uri.Join("../tests/test.rs")
	if err != nil {
		t.Fatal(err)
	}
	if joined.String() != "file:///workspace/src/tests/test.rs" {
		t.Fatalf("joined = %s", joined)
	}
	base, _ := Parse("file:///workspace")
	if !joined.StartsWith(base) {
		t.Fatalf("%s should start with %s", joined, base)
	}
	other, _ := Parse("file:///workspace-other")
	if joined.StartsWith(other) {
		t.Fatalf("%s should not start with %s", joined, other)
	}
}

func TestJoinWindowsPaths(t *testing.T) {
	base, _ := Parse("file:///C:/workspace/src")
	joined, err := base.Join(`D:\tmp\a\..\b`)
	if err != nil {
		t.Fatal(err)
	}
	if joined.String() != "file:///D:/tmp/b" {
		t.Fatalf("joined = %s", joined)
	}
	rootRelative, err := base.Join(`\Windows`)
	if err != nil {
		t.Fatal(err)
	}
	if rootRelative.String() != "file:///C:/Windows" {
		t.Fatalf("root relative = %s", rootRelative)
	}
	sameDrive, err := base.Join(`C:tmp`)
	if err != nil || sameDrive.String() != "file:///C:/workspace/src/tmp" {
		t.Fatalf("same-drive relative path = %v, %v", sameDrive, err)
	}
	if _, err := base.Join(`D:tmp`); err == nil {
		t.Fatalf("other-drive relative path should fail")
	}
}

func TestResolveExecutorPathPreservesForeignConventions(t *testing.T) {
	for _, test := range []struct {
		base string
		path string
		want string
	}{
		{base: "file:///home/alice/repo", path: "src/main.rs", want: "/home/alice/repo/src/main.rs"},
		{base: "file:///C:/Users/Alice%20Smith/repo", path: `src\main.rs`, want: `C:\Users\Alice Smith\repo\src\main.rs`},
		{base: "file:///C:/Users/Alice%20Smith/repo", path: `C:src\main.rs`, want: `C:\Users\Alice Smith\repo\src\main.rs`},
		{base: "file://server/share/repo", path: `src\main.rs`, want: `\\server\share\repo\src\main.rs`},
	} {
		got, err := ResolveExecutorPath(test.base, test.path)
		if err != nil || got.Value != test.want {
			t.Fatalf("ResolveExecutorPath(%q, %q) = %q, %v; want %q", test.base, test.path, got.Value, err, test.want)
		}
	}
}

func TestWindowsNamespacePathsNormalizeToCanonicalURIs(t *testing.T) {
	base, err := Parse("file:///C:/workspace")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		`\\?\D:\reports\report.pdf`:               "file:///D:/reports/report.pdf",
		`\\.\D:\reports\report.pdf`:               "file:///D:/reports/report.pdf",
		`\\?\UNC\server\share\reports\report.pdf`: "file://server/share/reports/report.pdf",
		`\\.\UNC\server\share\reports\report.pdf`: "file://server/share/reports/report.pdf",
	}
	for nativePath, want := range cases {
		joined, err := base.Join(nativePath)
		if err != nil {
			t.Fatalf("Join(%q) error = %v", nativePath, err)
		}
		if joined.String() != want {
			t.Fatalf("Join(%q) = %q, want %q", nativePath, joined.String(), want)
		}
		legacy := NewLegacyAppPathString(nativePath)
		converted, err := legacy.ToPathURI(ConventionWindows)
		if err != nil || converted.String() != want {
			t.Fatalf("ToPathURI(%q) = %v, %v", nativePath, converted, err)
		}
	}
}

func TestWindowsNamespacePathsPreserveUnsafeFormsAsOpaque(t *testing.T) {
	base, err := Parse("file:///C:/workspace")
	if err != nil {
		t.Fatal(err)
	}
	for _, nativePath := range []string{
		`\\?\UNC\server`,
		`\\.\UNC\localhost\share\report.pdf`,
		`\\?\UNC\.\share\report.pdf`,
		`\\.\COM1`,
		`\\?\Volume{00000000-0000-0000-0000-000000000000}\report.pdf`,
	} {
		joined, err := base.Join(nativePath)
		if err != nil {
			t.Fatalf("Join(%q) error = %v", nativePath, err)
		}
		if !strings.HasPrefix(joined.String(), badPathURIPrefix) {
			t.Fatalf("Join(%q) = %q, want opaque URI", nativePath, joined.String())
		}
		if got := joined.NativePathString(); got != nativePath {
			t.Fatalf("opaque NativePathString(%q) = %q", nativePath, got)
		}
	}
}

func TestLegacyAppPathStringRoundTrips(t *testing.T) {
	path := NewLegacyAppPathString(`C:\workspace\file.rs`)
	convention, ok := path.InferAbsolutePathConvention()
	if !ok || convention != ConventionWindows {
		t.Fatalf("convention = %s %v", convention, ok)
	}
	uri, err := path.ToPathURI(convention)
	if err != nil {
		t.Fatal(err)
	}
	if uri.String() != "file:///C:/workspace/file.rs" {
		t.Fatalf("uri = %s", uri)
	}
	rendered, err := LegacyAppPathStringFromURI(uri, ConventionWindows)
	if err != nil {
		t.Fatal(err)
	}
	if rendered.Value != path.Value {
		t.Fatalf("rendered = %q", rendered.Value)
	}
}

func TestJSONAndNativeHostPath(t *testing.T) {
	uri, _ := Parse("file:///workspace/src/lib.rs")
	data, err := json.Marshal(uri)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `"file:///workspace/src/lib.rs"` {
		t.Fatalf("json = %s", data)
	}
	var decoded PathURI
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.String() != uri.String() {
		t.Fatalf("decoded = %s", decoded.String())
	}
	if _, err := FromHostNativePath("relative/path"); err == nil {
		t.Fatalf("relative host path should fail")
	}
	hostAbs := filepath.Clean(string(filepath.Separator) + "tmp")
	if filepath.IsAbs(hostAbs) {
		_, _ = FromHostNativePath(hostAbs)
	}
}

func TestRejectsUnsupportedMetadata(t *testing.T) {
	for _, raw := range []string{"https://example.com/file", "file://server:42/share", "file:///tmp/file?version=1", "file:///tmp/file#L1", "file:///tmp/%00"} {
		if _, err := Parse(raw); err == nil {
			t.Fatalf("expected parse error for %s", raw)
		}
	}
}

func TestHostNativePathRejectsForeignConvention(t *testing.T) {
	raw := "file:///usr/local/file.txt"
	if runtime.GOOS != "windows" {
		raw = "file:///C:/Users/Alice/file.txt"
	}
	uri, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if _, err := uri.HostNativePath(); err == nil {
		t.Fatalf("HostNativePath(%q) error = nil", raw)
	}
}

func TestHostNativePathRejectsEncodedWindowsSeparators(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows file URI hardening is host-specific")
	}
	// Percent-encoded '\' must fail closed rather than be reinterpretted as a
	// segment boundary (Rust #40423).
	uri, err := Parse("file:///C:/Workspace/%5C%5Cevil")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if _, err := uri.HostNativePath(); err == nil {
		t.Fatalf("HostNativePath(%q) error = nil (encoded separator must fail closed)", uri.String())
	}

	// A percent-encoded drive colon is recognized as a Windows drive.
	encoded, err := Parse("file:///C%3A/Users/Alice/file.txt")
	if err != nil {
		t.Fatalf("Parse(%q) error = %v", "file:///C%3A/Users/Alice/file.txt", err)
	}
	got, err := encoded.HostNativePath()
	if err != nil {
		t.Fatalf("HostNativePath(%q) error = %v", encoded.String(), err)
	}
	if got != `C:\Users\Alice\file.txt` {
		t.Fatalf("HostNativePath = %q, want C:\\Users\\Alice\\file.txt", got)
	}
}

func TestWindowsPathURICaseInsensitiveEqualityAndContainment(t *testing.T) {
	// Windows drive-path URIs compare and contain ASCII-case-insensitively
	// (Rust 4cb8676d3a, #37129).
	a, err := Parse("file:///C:/Workspace/Src/lib.rs")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Parse("file:///c:/workspace/src/lib.rs")
	if err != nil {
		t.Fatal(err)
	}
	if !a.Equal(b) {
		t.Fatalf("%s should equal %s case-insensitively", a, b)
	}
	baseLower, _ := Parse("file:///c:/workspace")
	if !a.StartsWith(baseLower) {
		t.Fatalf("%s should start with %s case-insensitively", a, baseLower)
	}
	baseUpper, _ := Parse("file:///C:/WORKSPACE")
	if !a.StartsWith(baseUpper) {
		t.Fatalf("%s should start with %s case-insensitively", a, baseUpper)
	}
	// Host is still compared exactly.
	hostedA, _ := Parse("file://SERVER/share/File.txt")
	hostedB, _ := Parse("file://server/share/file.txt")
	if hostedA.Equal(hostedB) {
		t.Fatalf("host case must remain significant: %s vs %s", hostedA, hostedB)
	}

	// POSIX paths stay case-sensitive.
	posixA, _ := Parse("file:///Workspace/Src/lib.rs")
	posixB, _ := Parse("file:///workspace/src/lib.rs")
	if posixA.Equal(posixB) {
		t.Fatalf("POSIX equality must remain case-sensitive: %s vs %s", posixA, posixB)
	}
	if posixA.StartsWith(posixB) {
		t.Fatalf("POSIX containment must remain case-sensitive: %s vs %s", posixA, posixB)
	}

	// Percent-encoded native separators fail closed even for Windows.
	encoded, err := Parse("file:///C:/Workspace/%5C%5Cevil")
	if err == nil {
		plain, _ := Parse("file:///c:/workspace/\\\\evil")
		if encoded.Equal(plain) {
			t.Fatalf("percent-encoded separators must fail closed: %s vs %s", encoded, plain)
		}
	}
}

func TestPathURIOverlapsSemanticsLikeRust(t *testing.T) {
	parent, err := FromAbsoluteNativePath("/workspace", ConventionPosix)
	if err != nil {
		t.Fatalf("parent: %v", err)
	}
	child, err := FromAbsoluteNativePath("/workspace/a", ConventionPosix)
	if err != nil {
		t.Fatalf("child: %v", err)
	}
	sibling, err := FromAbsoluteNativePath("/workspace/b", ConventionPosix)
	if err != nil {
		t.Fatalf("sibling: %v", err)
	}
	disjoint, err := FromAbsoluteNativePath("/workspace/c", ConventionPosix)
	if err != nil {
		t.Fatalf("disjoint: %v", err)
	}
	overlap, ok := parent.Overlaps(child)
	if !ok || !overlap {
		t.Fatalf("parent.Overlaps(child) = (%v,%v), want (true,true)", overlap, ok)
	}
	overlap, ok = parent.Overlaps(parent)
	if !ok || !overlap {
		t.Fatalf("Overlaps(equal) = (%v,%v), want (true,true)", overlap, ok)
	}
	// /workspace/b and /workspace/c are neither ancestor nor descendant.
	overlap, ok = sibling.Overlaps(disjoint)
	if !ok || overlap {
		t.Fatalf("Overlaps(disjoint) = (%v,%v), want (false,true)", overlap, ok)
	}
}

func TestPathURILexicalDepthAndJoinDescendantLikeRust(t *testing.T) {
	base, err := FromAbsoluteNativePath("/workspace", ConventionPosix)
	if err != nil {
		t.Fatalf("base: %v", err)
	}
	if depth, ok := base.LexicalDepth(); !ok || depth != 1 {
		t.Fatalf("LexicalDepth = (%d,%v), want (1,true)", depth, ok)
	}
	child, err := base.JoinDescendant("a/b")
	if err != nil {
		t.Fatalf("JoinDescendant(a/b): %v", err)
	}
	if !child.StartsWith(base) {
		t.Fatalf("JoinDescendant result %v does not start with base %v", child, base)
	}
	if _, err := base.JoinDescendant("/escape"); err == nil {
		t.Fatal("JoinDescendant(/escape) = nil error, want absolute path rejected")
	}
	if _, err := base.JoinDescendant("../../escape"); err == nil {
		t.Fatal("JoinDescendant(../../escape) = nil error, want escaping path rejected")
	}
}

// TestLegacyAppPathStringInfersUNCRootsLikeRust pins Rust #49424: two leading
// separators select Windows UNC or namespace syntax, including forward and mixed
// slashes, while a single leading slash stays POSIX.
func TestLegacyAppPathStringInfersUNCRootsLikeRust(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  PathConvention
	}{
		{value: `//server/share/project`, want: ConventionWindows},
		{value: `/\server\share`, want: ConventionWindows},
		{value: `\\server\share`, want: ConventionWindows},
		{value: `C:\workspace\file.rs`, want: ConventionWindows},
		{value: "/workspace/file.rs", want: ConventionPosix},
	} {
		path := NewLegacyAppPathString(tc.value)
		got, ok := path.InferAbsolutePathConvention()
		if !ok || got != tc.want {
			t.Fatalf("InferAbsolutePathConvention(%q) = %q %v, want %q", tc.value, got, ok, tc.want)
		}
	}
	relative := NewLegacyAppPathString("relative/path")
	if _, ok := relative.InferAbsolutePathConvention(); ok {
		t.Fatalf("relative path inferred a convention")
	}
	unc := NewLegacyAppPathString(`//server/share/project`)
	uri, err := unc.ToPathURI(ConventionWindows)
	if err != nil {
		t.Fatal(err)
	}
	if uri.String() != "file://server/share/project" {
		t.Fatalf("uri = %s", uri)
	}
}

// TestOpaquePathConventionInferenceLikeRust pins Rust #49388: an opaque UTF-16LE
// path keeps its Windows convention when it starts with a forward slash, and a
// bare leading slash still designates a POSIX root.
func TestOpaquePathConventionInferenceLikeRust(t *testing.T) {
	for _, native := range []string{`//server/share/project`, `/\server\share`, `C:\workspace\file`} {
		uri, err := windowsOpaquePathURI(native)
		if err != nil {
			t.Fatalf("windowsOpaquePathURI(%q) error = %v", native, err)
		}
		if !uri.IsOpaque() {
			t.Fatalf("windowsOpaquePathURI(%q) is not opaque", native)
		}
		if convention, ok := uri.InferConvention(); !ok || convention != ConventionWindows {
			t.Fatalf("InferConvention(%q) = %q %v, want %s", native, convention, ok, ConventionWindows)
		}
	}
	raw := badPathURIPrefix + base64.RawURLEncoding.EncodeToString([]byte("/tmp/file"))
	posix, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if convention, ok := posix.InferConvention(); !ok || convention != ConventionPosix {
		t.Fatalf("opaque POSIX convention = %q %v", convention, ok)
	}
}

// TestPathURIIdentityKeyLikeRust mirrors the identity use Rust #51482 makes of
// PathUri as HashSet keys: equivalent Windows case and separator spellings must
// collapse to one key, the host stays significant, POSIX paths stay
// case-sensitive, and literal percent/fragment characters stay distinct.
func TestPathURIIdentityKeyLikeRust(t *testing.T) {
	key := func(raw string) string {
		t.Helper()
		uri, ok := InferredPathURI(raw)
		if !ok {
			t.Fatalf("InferredPathURI(%q) = %v, %v; want a URI", raw, uri, ok)
		}
		return uri.IdentityKey()
	}

	// Windows case and separator spellings share one identity key.
	backslash := mustInferredPathURI(t, `C:\Skills\Demo\SKILL.md`)
	for _, raw := range []string{
		"C:/SKILLS/DEMO/skill.md",
		"file:///c:/skills/demo/skill.md",
	} {
		if got, want := key(raw), backslash.IdentityKey(); got != want {
			t.Fatalf("IdentityKey(%q) = %q, want %q", raw, got, want)
		}
	}

	// The host remains significant for UNC share identities.
	hostedA := key("file://SERVER/share/File.txt")
	hostedB := key("file://server/share/file.txt")
	if hostedA == hostedB {
		t.Fatalf("host case must stay significant: %q vs %q", hostedA, hostedB)
	}

	// POSIX paths stay case-sensitive.
	if a, b := key("file:///Workspace/Src/lib.rs"), key("file:///workspace/src/lib.rs"); a == b {
		t.Fatalf("POSIX identity must stay case-sensitive: %q vs %q", a, b)
	}

	// Windows and POSIX spellings never share an identity by accident.
	if a, b := key("file:///C:/Skills/Demo/SKILL.md"), key("file:///C:/skills/demo/SKILL.md"); a != b {
		t.Fatalf("Windows identity keys should be equal: %q vs %q", a, b)
	}

	// IdentityKey must agree with Equal for every pair above.
	uris := []string{
		"C:/SKILLS/DEMO/skill.md",
		"file:///c:/skills/demo/skill.md",
		"file:///C:/Skills/Demo/SKILL.md",
		"file:///Workspace/Src/lib.rs",
		"file:///workspace/src/lib.rs",
	}
	for _, left := range uris {
		for _, right := range uris {
			leftURI, _ := InferredPathURI(left)
			rightURI, _ := InferredPathURI(right)
			sameKey := leftURI.IdentityKey() == rightURI.IdentityKey()
			if sameKey != leftURI.Equal(rightURI) {
				t.Fatalf("IdentityKey disagrees with Equal for %q vs %q", left, right)
			}
		}
	}
}

// TestPathURIIdentityKeyPreservesLiteralCharactersLikeRust covers Rust #51482's
// "preserve literal spaces, `%`, and `#` in native filenames" rule: a literal
// percent escape and a literal fragment character stay distinct identities.
func TestPathURIIdentityKeyPreservesLiteralCharactersLikeRust(t *testing.T) {
	percent := mustInferredPathURI(t, `/tmp/demo skill%23/SKILL.md`)
	fragment := mustInferredPathURI(t, `/tmp/demo skill#/SKILL.md`)
	if percent.IdentityKey() == fragment.IdentityKey() {
		t.Fatalf("literal %%23 and # filenames must stay distinct: %q", percent.IdentityKey())
	}
	if percent.Equal(fragment) {
		t.Fatalf("%s must not equal %s", percent, fragment)
	}
}

// TestInferredPathURILikeRust mirrors Rust #51482's
// `PathUri::from_host_native_path(..).or_else(|| LegacyAppPathString::from_string(..).to_inferred_path_uri())`
// chain: a `file:` URI wins, then a host path, then an absolute foreign
// spelling; text with no URI representation reports false so callers keep their
// own comparison.
func TestInferredPathURILikeRust(t *testing.T) {
	for _, value := range []string{
		"file:///tmp/demo/SKILL.md",
		"/tmp/demo/SKILL.md",
		`C:\Project\.agents\skills\Demo\SKILL.md`,
		"C:/Project/.agents/skills/Demo/SKILL.md",
	} {
		if uri, ok := InferredPathURI(value); !ok || uri == nil {
			t.Fatalf("InferredPathURI(%q) = %v, %v; want a URI", value, uri, ok)
		}
	}
	for _, value := range []string{"", "   ", "environment://local/skills/demo", "relative/SKILL.md"} {
		if uri, ok := InferredPathURI(value); ok {
			t.Fatalf("InferredPathURI(%q) = %v; want no URI", value, uri)
		}
	}

	// A foreign Windows spelling and the equivalent `file:` locator share one
	// identity, which is what makes disabled-path and mention matching work.
	foreign, ok := PathIdentityKey(`C:\Project\.agents\skills\Demo\SKILL.md`)
	if !ok {
		t.Fatal("PathIdentityKey(foreign Windows path) = false")
	}
	locator, ok := PathIdentityKey("file:///c:/project/.agents/skills/demo/skill.md")
	if !ok {
		t.Fatal("PathIdentityKey(file locator) = false")
	}
	if foreign != locator {
		t.Fatalf("foreign identity %q != locator identity %q", foreign, locator)
	}
	if _, ok := PathIdentityKey("environment://local/skills/demo"); ok {
		t.Fatal("environment locator must not produce a path identity")
	}
}

// mustInferredPathURI converts a native spelling into its inferred path URI,
// failing the test when the convention cannot be resolved.
func mustInferredPathURI(t *testing.T, native string) *PathURI {
	t.Helper()
	legacy := NewLegacyAppPathString(native)
	uri, ok := legacy.ToInferredPathURI()
	if !ok || uri == nil {
		t.Fatalf("ToInferredPathURI(%q) = %v, %v; want a URI", native, uri, ok)
	}
	return uri
}
