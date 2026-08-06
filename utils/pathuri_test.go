package utils

import (
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
