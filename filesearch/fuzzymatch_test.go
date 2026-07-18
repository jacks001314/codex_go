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

func TestFuzzyMatchNoMatch(t *testing.T) {
	if _, ok := FuzzyMatch("straße", "strasse"); ok {
		t.Fatal("expected no match for sharp-s expansion")
	}
}
