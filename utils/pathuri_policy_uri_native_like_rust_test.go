package utils

import "testing"

// Rust #41001 resolves filesystem policy entries as PathUri values so matching
// uses validated URI components instead of host-native comparisons.
func TestJoinDescendantUsesBasePathConventionLikeRust(t *testing.T) {
	for _, tc := range []struct {
		base     string
		relative string
		want     string
	}{
		{"file:///workspace", "docs/../public", "file:///workspace/public"},
		{"file:///C:/workspace", `docs\..\public`, "file:///C:/workspace/public"},
		{"file://server/share/workspace", `docs\..\public`, "file://server/share/workspace/public"},
	} {
		base, err := Parse(tc.base)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.base, err)
		}
		want, err := Parse(tc.want)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.want, err)
		}
		got, err := base.JoinDescendant(tc.relative)
		if err != nil {
			t.Fatalf("%s.JoinDescendant(%q): %v", tc.base, tc.relative, err)
		}
		if !got.Equal(want) {
			t.Fatalf("%s.JoinDescendant(%q) = %s, want %s", tc.base, tc.relative, got, want)
		}
	}
}

func TestJoinDescendantRejectsNonDescendantPathsLikeRust(t *testing.T) {
	for _, tc := range []struct {
		base string
		path string
	}{
		{"file:///workspace", "/workspace/docs"},
		{"file:///workspace", "../outside"},
		{"file:///C:/workspace", `\workspace\docs`},
		{"file:///C:/workspace", `C:\workspace\docs`},
		{"file:///C:/workspace", "C:docs"},
		{"file:///C:/workspace", `docs\file:stream`},
		{"file://server/share/workspace", `..\outside`},
	} {
		base, err := Parse(tc.base)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.base, err)
		}
		if got, err := base.JoinDescendant(tc.path); err == nil {
			t.Fatalf("%s.JoinDescendant(%q) = %s, want an error", tc.base, tc.path, got)
		}
	}
}

func TestStartsWithDecodesEncodedSegmentsLikeRust(t *testing.T) {
	for _, tc := range []struct {
		value string
		base  string
		want  bool
	}{
		{"file:///workspace/pri%76ate/file", "file:///workspace/%70rivate", true},
		{"file:///workspace/%ff/file", "file:///workspace/%FF", true},
		{"file:///workspace/plugin/%5C..%5Coutside", "file:///workspace/plugin", true},
	} {
		value, err := Parse(tc.value)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.value, err)
		}
		base, err := Parse(tc.base)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.base, err)
		}
		if got := value.StartsWith(base); got != tc.want {
			t.Fatalf("%s.StartsWith(%s) = %v, want %v", tc.value, tc.base, got, tc.want)
		}
	}
}

func TestOverlapsUsesLexicalContainmentLikeRust(t *testing.T) {
	for _, tc := range []struct {
		left  string
		right string
		want  bool
	}{
		{"file:///workspace", "file:///workspace/src", true},
		{"file:///C:/WORKSPACE", "file:///c:/workspace/src", true},
		{"file:///workspace/src", "file:///workspace/tests", false},
		{"file:///WORKSPACE", "file:///workspace/src", false},
	} {
		left, err := Parse(tc.left)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.left, err)
		}
		right, err := Parse(tc.right)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.right, err)
		}
		got, ok := left.Overlaps(right)
		if !ok || got != tc.want {
			t.Fatalf("%s.Overlaps(%s) = (%v,%v), want (%v,true)", tc.left, tc.right, got, ok, tc.want)
		}
		got, ok = right.Overlaps(left)
		if !ok || got != tc.want {
			t.Fatalf("%s.Overlaps(%s) = (%v,%v), want (%v,true)", tc.right, tc.left, got, ok, tc.want)
		}
	}

	// Go stores unrepresentable Windows native paths as opaque fallback URIs;
	// Rust's from_opaque_path_bytes is the same fail-closed carrier.
	windowsBase, err := Parse("file:///C:/workspace")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	opaque, err := windowsBase.Join(`\\.\COM1`)
	if err != nil {
		t.Fatalf("Join opaque path: %v", err)
	}
	if !opaque.IsOpaque() {
		t.Fatalf("Join(%q) = %s, want an opaque URI", `\\.\COM1`, opaque)
	}
	lexical, err := Parse("file:///workspace")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got, ok := opaque.Overlaps(opaque); !ok || !got {
		t.Fatalf("opaque.Overlaps(opaque) = (%v,%v), want (true,true)", got, ok)
	}
	if _, ok := opaque.Overlaps(lexical); ok {
		t.Fatal("opaque.Overlaps(lexical) reported ambiguous components as comparable")
	}
	if _, ok := lexical.Overlaps(opaque); ok {
		t.Fatal("lexical.Overlaps(opaque) reported ambiguous components as comparable")
	}
}

func TestLexicalDepthCountsValidatedSegmentsLikeRust(t *testing.T) {
	for _, tc := range []struct {
		value          string
		wantDepth      int
		wantComparable bool
	}{
		{"file:///", 0, true},
		{"file:///workspace////", 1, true},
		{"file:///workspace/%70rivate", 2, true},
		{"file:///workspace/private%2Fsecret", 0, false},
	} {
		uri, err := Parse(tc.value)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.value, err)
		}
		depth, ok := uri.LexicalDepth()
		if ok != tc.wantComparable || depth != tc.wantDepth {
			t.Fatalf("%s.LexicalDepth() = (%d,%v), want (%d,%v)", tc.value, depth, ok, tc.wantDepth, tc.wantComparable)
		}
	}
}
