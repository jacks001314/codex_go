package utils

import "testing"

func TestStoresRetrievesAndEvictsLeastRecentlyUsed(t *testing.T) {
	cache := New[string, int](2)
	cache.Insert("a", 1)
	cache.Insert("b", 2)
	if got, ok := cache.Get("a"); !ok || got != 1 {
		t.Fatalf("a = %d %v", got, ok)
	}
	cache.Insert("c", 3)
	if _, ok := cache.Get("b"); ok {
		t.Fatalf("expected b to be evicted")
	}
	if got, ok := cache.Get("a"); !ok || got != 1 {
		t.Fatalf("a after eviction = %d %v", got, ok)
	}
}

func TestGetOrInsertAndTryWithCapacity(t *testing.T) {
	if TryWithCapacity[string, int](0) != nil {
		t.Fatalf("zero capacity should return nil")
	}
	cache := New[string, int](1)
	calls := 0
	if got := cache.GetOrInsertWith("k", func() int { calls++; return 7 }); got != 7 {
		t.Fatalf("first get = %d", got)
	}
	if got := cache.GetOrInsertWith("k", func() int { calls++; return 8 }); got != 7 {
		t.Fatalf("cached get = %d", got)
	}
	if calls != 1 {
		t.Fatalf("calls = %d", calls)
	}
}

func TestRemoveClearAndDigest(t *testing.T) {
	cache := New[string, int](2)
	cache.Insert("a", 1)
	if old, ok := cache.Remove("a"); !ok || old != 1 {
		t.Fatalf("remove = %d %v", old, ok)
	}
	cache.Insert("b", 2)
	cache.Clear()
	if cache.Len() != 0 {
		t.Fatalf("len = %d", cache.Len())
	}
	digest := SHA1Digest([]byte("abc"))
	if digest[0] != 0xa9 || digest[19] != 0x9d {
		t.Fatalf("digest = %x", digest)
	}
}
