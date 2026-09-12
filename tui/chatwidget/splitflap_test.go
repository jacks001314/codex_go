package chatwidget

import (
	"strings"
	"testing"
	"time"
)

func splitFlapFrame(board *SplitFlapBoard, milliseconds, width int) string {
	display := "› " + board.Target
	line := board.AnimateLine(display, width, time.Duration(milliseconds)*time.Millisecond)
	return strings.TrimRight(line, " ")
}

// TestSplitFlapFramesAssembleTranscript reproduces the Rust
// realtime_split_flap snapshot for a plain transcript cell.
func TestSplitFlapFramesAssembleTranscript(t *testing.T) {
	now := time.Now()
	board := NewSplitFlapBoard("user", "GATE 73", nil, 0, true, now)
	cases := []struct {
		milliseconds int
		want         string
	}{
		{0, "›"},
		{45, "› T"},
		{90, "› ATGG AG"},
		{180, "› GGTT ET GAT"},
		{285, "› GATE 73 TEG"},
		{675, "› GATE 73"},
	}
	for _, testCase := range cases {
		if got := splitFlapFrame(board, testCase.milliseconds, 16); got != testCase.want {
			t.Fatalf("%dms = %q, want %q", testCase.milliseconds, got, testCase.want)
		}
	}

	punctuated := NewSplitFlapBoard("user", "HELLO, WORLD?!", nil, 0, true, now)
	for _, milliseconds := range []int{90, 180} {
		if strings.Contains(splitFlapFrame(punctuated, milliseconds, 16), ",") {
			t.Fatalf("%dms masked punctuation was revealed early", milliseconds)
		}
	}
	if got := splitFlapFrame(punctuated, 285, 16); got != "› HELLO, WORLD?!" {
		t.Fatalf("285ms punctuated = %q", got)
	}
	if splitFlapFrame(board, 285, 16) == splitFlapFrame(board, 330, 16) {
		t.Fatal("runway did not advance between frames")
	}

	lowercase := NewSplitFlapBoard("user", "please keep going", nil, 0, true, now)
	for _, value := range lowercase.FlapSample {
		if value >= 'A' && value <= 'Z' {
			t.Fatalf("lowercase sample contains %q", string(value))
		}
	}
	for _, milliseconds := range []int{90, 285} {
		for _, r := range splitFlapFrame(lowercase, milliseconds, 40) {
			if r >= 'A' && r <= 'Z' {
				t.Fatalf("lowercase board rendered uppercase %q at %dms", r, milliseconds)
			}
		}
	}
}

func TestSplitFlapReducedMotionKeepsOriginalAndRequestsNoFrames(t *testing.T) {
	now := time.Now()
	board := NewSplitFlapBoard("user", "GATE 73", nil, 0, false, now)
	if got := board.AnimateLine("› GATE 73", 16, 0); got != "› GATE 73" {
		t.Fatalf("reduced motion line = %q", got)
	}
	if _, ok := board.AnimationTick(0); ok {
		t.Fatal("reduced motion requested an animation frame")
	}
}

func TestSplitFlapAppendedTranscriptKeepsPreviousTilesSettled(t *testing.T) {
	now := time.Now()
	first := NewSplitFlapBoard("user", "GATE", nil, 0, true, now)
	for index := range first.TileArrivals {
		first.TileArrivals[index] = first.TileArrivals[index].Add(-SplitFlapAnimationDuration)
	}
	appended := NewSplitFlapBoard("user", "GATE 73", first, 0, true, now)
	if !strings.HasPrefix(splitFlapFrame(appended, 0, 16), "› GATE ") {
		t.Fatalf("appended frame = %q", splitFlapFrame(appended, 0, 16))
	}
	if !appended.PhaseStartedAt.Equal(first.PhaseStartedAt) {
		t.Fatal("appended board did not inherit the phase")
	}
	if len(appended.TileArrivals) < 4 || appended.TileArrivals[0] != first.TileArrivals[0] {
		t.Fatal("appended board did not retain the previous tiles")
	}
}

func TestSplitFlapSlidingWindowAnimatesOnlyNewTiles(t *testing.T) {
	now := time.Now()
	text := strings.Repeat("A", 40)
	first := NewSplitFlapBoard("user", text, nil, 0, true, now)
	for index := range first.TileArrivals {
		first.TileArrivals[index] = first.TileArrivals[index].Add(-SplitFlapAnimationDuration)
	}
	shifted := NewSplitFlapBoard("user", text, first, 2, true, now)
	for index := 0; index < len(first.TileArrivals)-2; index++ {
		if shifted.TileArrivals[index] != first.TileArrivals[index+2] {
			t.Fatalf("tile %d was not retained across the slide", index)
		}
	}
	if len(shifted.TileArrivals) != len(first.TileArrivals) {
		t.Fatalf("slid tiles = %d, want %d", len(shifted.TileArrivals), len(first.TileArrivals))
	}
}

func TestSplitFlapReplacedSpeakerStartsFresh(t *testing.T) {
	now := time.Now()
	first := NewSplitFlapBoard("user", "GATE", nil, 0, true, now)
	cases := []struct {
		role            string
		target          string
		discardedPrefix int
	}{
		{"assistant", "GATE 73", 0},
		{"user", "OTHER", 0},
		{"user", "GATE", 1},
	}
	for _, testCase := range cases {
		board := NewSplitFlapBoard(testCase.role, testCase.target, first, testCase.discardedPrefix, true, now)
		if !board.PhaseStartedAt.Equal(board.StartedAt) {
			t.Fatalf("replaced window inherited a phase: %+v", testCase)
		}
		for _, arrival := range board.TileArrivals {
			if arrival.Before(board.StartedAt) {
				t.Fatalf("replaced window kept an old tile: %+v", testCase)
			}
		}
	}
}

func TestVoiceAmplitudeHistoryIsBoundedAndOrdered(t *testing.T) {
	var history VoiceAmplitudeHistory
	for _, level := range []int{1, 2, 3, 4, 9} {
		history.Push(level)
	}
	want := VoiceAmplitudeHistory{2, 3, 4, 5}
	if history != want {
		t.Fatalf("history = %v, want %v", history, want)
	}
}

func TestSplitFlapSettledStopsAnimating(t *testing.T) {
	now := time.Now()
	board := NewSplitFlapBoard("user", "GATE", nil, 0, true, now)
	if _, ok := board.AnimationTick(0); !ok {
		t.Fatal("fresh board should animate")
	}
	if _, ok := board.AnimationTick(SplitFlapAnimationDuration); ok {
		t.Fatal("settled board still animates")
	}
	if got := strings.TrimRight(board.AnimateLine("› GATE", 12, SplitFlapAnimationDuration), " "); got != "› GATE" {
		t.Fatalf("settled line = %q", got)
	}
}
