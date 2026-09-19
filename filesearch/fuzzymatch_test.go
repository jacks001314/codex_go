package filesearch

import (
	"math"
	"reflect"
	"testing"
)

func TestFuzzyMatchASCIIBasicIndices(t *testing.T) {
	got, ok := FuzzyMatch("hello", "hl")
	if !ok {
		t.Fatal("expected match")
	}
	if !reflect.DeepEqual(got.Indices, []int{0, 2}) || got.Score != -99 {
		t.Fatalf("match = %#v", got)
	}
}

func TestFuzzyMatchPrefersContiguous(t *testing.T) {
	contiguous, ok := FuzzyMatch("abc", "abc")
	if !ok {
		t.Fatal("expected contiguous match")
	}
	spread, ok := FuzzyMatch("a-b-c", "abc")
	if !ok {
		t.Fatal("expected spread match")
	}
	if contiguous.Score != -100 || spread.Score != -98 || contiguous.Score >= spread.Score {
		t.Fatalf("scores contiguous=%d spread=%d", contiguous.Score, spread.Score)
	}
}

func TestFuzzyMatchStartOfStringBonus(t *testing.T) {
	prefix, ok := FuzzyMatch("file_name", "file")
	if !ok {
		t.Fatal("expected prefix match")
	}
	middle, ok := FuzzyMatch("my_file_name", "file")
	if !ok {
		t.Fatal("expected middle match")
	}
	if prefix.Score != -100 || middle.Score != 0 {
		t.Fatalf("scores prefix=%d middle=%d", prefix.Score, middle.Score)
	}
}

func TestFuzzyMatchEmptyNeedle(t *testing.T) {
	got, ok := FuzzyMatch("anything", "")
	if !ok {
		t.Fatal("empty needle should match")
	}
	if len(got.Indices) != 0 || got.Score != math.MaxInt32 {
		t.Fatalf("empty match = %#v", got)
	}
}

func TestFuzzyMatchCaseInsensitive(t *testing.T) {
	got, ok := FuzzyMatch("FooBar", "foO")
	if !ok {
		t.Fatal("expected match")
	}
	if !reflect.DeepEqual(got.Indices, []int{0, 1, 2}) || got.Score != -100 {
		t.Fatalf("match = %#v", got)
	}
}

func TestFuzzyMatchUnicodeLowercaseExpansionDedupe(t *testing.T) {
	got, ok := FuzzyMatch("İ", "i\u0307")
	if !ok {
		t.Fatal("expected match")
	}
	if !reflect.DeepEqual(got.Indices, []int{0}) || got.Score != -100 {
		t.Fatalf("match = %#v", got)
	}
}

// Mirrors Rust fuzzy-match #45475: a match that begins inside a lowercase
// expansion must be scored from its actual lowercased position, so strings that
// lowercase identically do not receive a wrong prefix bonus or gap penalty.
func TestFuzzyMatchScoresWithinLowercaseExpansionLikeRust(t *testing.T) {
	query := "\u0307x"
	for _, tc := range []struct {
		haystack string
		indices  []int
	}{
		{"İx", []int{0, 1}},
		{"i\u0307x", []int{1, 2}},
		{"aİx", []int{1, 2}},
		{"ai\u0307x", []int{2, 3}},
	} {
		got, ok := FuzzyMatch(tc.haystack, query)
		if !ok {
			t.Fatalf("FuzzyMatch(%q, %q) did not match", tc.haystack, query)
		}
		if !reflect.DeepEqual(got.Indices, tc.indices) {
			t.Fatalf("FuzzyMatch(%q, %q) indices = %#v, want %#v", tc.haystack, query, got.Indices, tc.indices)
		}
		// The dot begins a contiguous match, but not a prefix, even when it
		// came from the expansion of 'İ'.
		if got.Score != 0 {
			t.Fatalf("FuzzyMatch(%q, %q) score = %d, want 0", tc.haystack, query, got.Score)
		}
	}
	// Each pair lowercases identically, so both members must rank the same.
	expanded, _ := FuzzyMatch("İx", query)
	alreadyLower, _ := FuzzyMatch("i\u0307x", query)
	if expanded.Score != alreadyLower.Score {
		t.Fatalf("identical lowercase forms ranked differently: %d vs %d", expanded.Score, alreadyLower.Score)
	}
}

func TestFuzzyMatchNoMatch(t *testing.T) {
	if _, ok := FuzzyMatch("straße", "strasse"); ok {
		t.Fatal("expected no match for sharp-s expansion")
	}
}
