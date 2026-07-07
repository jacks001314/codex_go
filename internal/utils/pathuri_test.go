package utils

import (
	"encoding/json"
	"path/filepath"
	"reflect"
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
	if joined.String() != "file:///workspace/tests/test.rs" {
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
	if _, err := base.Join(`D:tmp`); err == nil {
		t.Fatalf("drive relative path should fail")
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
	for _, raw := range []string{"https://example.com/file", "file:///tmp/file?version=1", "file:///tmp/file#L1", "file:///tmp/%00"} {
		if _, err := Parse(raw); err == nil {
			t.Fatalf("expected parse error for %s", raw)
		}
	}
}
