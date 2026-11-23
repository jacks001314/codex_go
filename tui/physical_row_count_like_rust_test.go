package tui

import "testing"

// Rust #63151: a wide cluster that cannot fit in the last column needs its own
// row, so the unused column changes the row count for long URLs.
func TestPhysicalRowCountLikeRust(t *testing.T) {
	const width = 20
	// The two-column bullet places the first 界 in the last column; each
	// subsequent wrap gap adds the columns the naive width division misses.
	wideURL := "• https://test.org/界界界界界界界界界界x"
	if got := DisplayWidth(wideURL); got != 40 {
		t.Fatalf("DisplayWidth(wideURL) = %d, want 40", got)
	}
	if got := PhysicalRowCount(wideURL, width); got != 3 {
		t.Fatalf("PhysicalRowCount(wideURL) = %d, want 3 rows", got)
	}

	// A cluster that starts with a narrow character may straddle the edge.
	straddling := "aaaaaaaaaaaaaaaaaaaｶﾞ"
	if got := PhysicalRowCount(straddling, width); got != 2 {
		t.Fatalf("PhysicalRowCount(straddling) = %d, want 2 rows", got)
	}

	if got := PhysicalRowCount("", width); got != 1 {
		t.Fatalf("PhysicalRowCount(empty) = %d, want 1", got)
	}
	if got := PhysicalRowCount("abcdefghij", 10); got != 1 {
		t.Fatalf("PhysicalRowCount(exact width) = %d, want 1", got)
	}
	if got := PhysicalRowCount("abcdefghijk", 10); got != 2 {
		t.Fatalf("PhysicalRowCount(over width) = %d, want 2", got)
	}
	if got := PhysicalRowCount("界", 0); got != 1 {
		t.Fatalf("PhysicalRowCount(zero width) = %d, want 1", got)
	}
}
