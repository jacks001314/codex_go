package disk

import "testing"

func TestFreeBytesReportsSpace(t *testing.T) {
	free, err := FreeBytes(t.TempDir())
	if err != nil {
		t.Skipf("free space unavailable on this platform: %v", err)
	}
	if free == 0 {
		t.Fatal("free space reported as zero")
	}
}
