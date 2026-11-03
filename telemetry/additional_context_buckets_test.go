package telemetry

import (
	"sort"
	"testing"
)

func TestAdditionalContextValueBytesBucketsMatchRust(t *testing.T) {
	buckets := AdditionalContextValueBytesBuckets
	if len(buckets) != 512 {
		t.Fatalf("bucket count = %d, want 512", len(buckets))
	}
	if buckets[0] != 0 {
		t.Fatalf("first bucket = %v, want 0", buckets[0])
	}
	if buckets[len(buckets)-1] != 4194304 {
		t.Fatalf("last bucket = %v, want 4 MiB", buckets[len(buckets)-1])
	}
	if !sort.Float64sAreSorted(buckets) {
		t.Fatalf("buckets are not sorted")
	}
	found := false
	for i := 1; i < len(buckets); i++ {
		if buckets[i] == buckets[i-1] {
			t.Fatalf("duplicate bucket at %d: %v", i, buckets[i])
		}
		if buckets[i] == 4096 {
			found = true
		}
	}
	if !found {
		t.Fatalf("4 KiB truncation boundary missing from %v", buckets)
	}
}
