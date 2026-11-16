package tui

import (
	"testing"
	"time"
)

// Rust #61731: per-grapheme color updates redraw individual terminal cells and
// break native bidi rendering, so headers with strong right-to-left characters
// or Arabic numbers render as one static dim span in logical order.
func TestBidiHeadersStayStaticDuringShimmerLikeRust(t *testing.T) {
	fg := RGB{R: 240, G: 240, B: 240}
	bg := RGB{R: 16, G: 16, B: 16}
	cases := []string{
		"שלום עולם",
		"Checking שלום (test.rs) 123",
		"Checking lines ١٢-٣٤",
		"مرحبا",
	}
	for _, text := range cases {
		t.Run(text, func(t *testing.T) {
			initial := SummaryShimmerSpansAt(text, 0, MotionAnimated, fg, bg, true)
			sweep := SummaryShimmerSpansAt(text, 1100*time.Millisecond, MotionAnimated, fg, bg, true)
			want := []SummaryShimmerSpan{{Text: text, Style: SummaryShimmerDim}}
			if !equalSummaryShimmerSpans(initial, want) {
				t.Fatalf("initial spans = %#v, want one static dim span", initial)
			}
			if !equalSummaryShimmerSpans(sweep, want) {
				t.Fatalf("sweep spans = %#v, want the initial spans", sweep)
			}
			shimmer := ShimmerText(text, MotionAnimated)
			if len(shimmer) != 1 || shimmer[0].Text != text || shimmer[0].Intensity != 0 {
				t.Fatalf("ShimmerText = %#v, want one static span", shimmer)
			}
			if reduced := ShimmerText(text, MotionReduced); len(reduced) != 1 || reduced[0].Text != text {
				t.Fatalf("reduced ShimmerText = %#v", reduced)
			}
		})
	}
}

// Left-to-right headers keep the animated sweep, and reduced motion still wins
// over the bidi guard (Rust checks motion first).
func TestLeftToRightHeadersKeepShimmerLikeRust(t *testing.T) {
	fg := RGB{R: 240, G: 240, B: 240}
	bg := RGB{R: 16, G: 16, B: 16}
	if ContainsRTLOrArabicNumber("Working") || ContainsRTLOrArabicNumber("Checking test.rs 123") {
		t.Fatal("left-to-right headers must not trip the bidi guard")
	}
	spans := SummaryShimmerSpansAt("Working", 1100*time.Millisecond, MotionAnimated, fg, bg, true)
	if len(spans) != len([]rune("Working")) {
		t.Fatalf("animated spans = %d, want one per grapheme", len(spans))
	}
	for _, span := range spans {
		if span.Style == SummaryShimmerDim {
			t.Fatalf("left-to-right span %q must animate, got dim", span.Text)
		}
	}
	reduced := SummaryShimmerSpansAt("שלום עולם", time.Second, MotionReduced, fg, bg, true)
	if len(reduced) != 1 || reduced[0].Style != SummaryShimmerPlain {
		t.Fatalf("reduced bidi spans = %#v, want plain text", reduced)
	}
}

func equalSummaryShimmerSpans(a, b []SummaryShimmerSpan) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}