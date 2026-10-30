package codemode

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"codex_go/utils"
)

// Mirrors codex-rs/code-mode-runtime/src/output/output_render_tests.rs (#57848).

func rText(value string) ContentItem { return InputText(value) }

func intPtrLocal(value int) *int { return &value }

func TestMissingBudgetUsesProtocolDefaultLikeRust(t *testing.T) {
	limit := ProtocolDefaultMaxOutputTokensPerExecCall
	for _, c := range []struct {
		extra, count int
	}{{0, 2}, {1, 1}} {
		items := []ContentItem{rText("a"), rText(strings.Repeat("b", 4*limit-2+c.extra))}
		rendered := renderOutput(CollectedOutput{Items: items}, nil)
		if len(rendered) != c.count {
			t.Fatalf("extra %d rendered %d items, want %d", c.extra, len(rendered), c.count)
		}
		if withBudget := renderOutput(CollectedOutput{Items: items}, intPtrLocal(limit)); !reflect.DeepEqual(rendered, withBudget) {
			t.Fatalf("explicit default budget differs for extra %d", c.extra)
		}
	}
}

func TestTypedMediaRoundTripsWithAudioEstimateLikeRust(t *testing.T) {
	images := make([]ContentItem, 0, 5)
	for _, detail := range []ImageDetail{"", ImageDetailAuto, ImageDetailLow, ImageDetailHigh, ImageDetailOriginal} {
		var pointer *ImageDetail
		if detail != "" {
			value := detail
			pointer = &value
		}
		images = append(images, InputImage("img:one", pointer))
	}
	wav := "data:audio/wav;base64,UklGRiYAAABXQVZFZm10IBAAAAABAAEAwF0AAIC7AAACABAAZGF0YQIAAAAAAA=="
	items := append(append([]ContentItem{}, images...), InputAudio(wav))
	if got := renderOutput(CollectedOutput{Items: items}, intPtrLocal(1)); !reflect.DeepEqual(got, items) {
		t.Fatalf("budget 1 rendered %#v", got)
	}
	expected := append(append([]ContentItem{}, images...), rText("[omitted 1 audio items ...]"))
	if got := renderOutput(CollectedOutput{Items: items}, intPtrLocal(0)); !reflect.DeepEqual(got, expected) {
		t.Fatalf("budget 0 rendered %#v", got)
	}
}

func TestPriorMediaLossForcesMixedFormattingLikeRust(t *testing.T) {
	for _, counts := range []struct{ images, audio int }{{1, 0}, {0, 1}} {
		collected := CollectedOutput{
			Items: []ContentItem{rText("1234"), rText("5678")},
			Gaps: []OutputGap{{
				Boundary: utils.OutputBoundary{ItemIndex: 1},
				Omitted:  OutputOmission{TextBytes: 11, TextItems: 2, ImageItems: uint64(counts.images), AudioItems: uint64(counts.audio)},
			}},
		}
		notice := rText(fmt.Sprintf(
			"[Code Mode session output limit reached: omitted 11 text bytes; discarded 2 text segments, %d images, %d audio items]",
			counts.images, counts.audio,
		))
		expected := []ContentItem{rText("1234"), notice, rText("[omitted 1 text items ...]")}
		if got := renderOutput(collected, intPtrLocal(1)); !reflect.DeepEqual(got, expected) {
			t.Fatalf("images=%d rendered %#v", counts.images, got)
		}
	}
}

func TestAlternatingLostAudioAndEmptyTextYieldsOneNoticeLikeRust(t *testing.T) {
	count := 16_384
	collected := CollectedOutput{Items: make([]ContentItem, count), Gaps: make([]OutputGap, count)}
	for i := 0; i < count; i++ {
		collected.Items[i] = rText("")
		collected.Gaps[i] = OutputGap{
			Boundary: utils.OutputBoundary{ItemIndex: i},
			Omitted:  OutputOmission{AudioItems: 1},
		}
	}
	expected := []ContentItem{rText(
		"[Code Mode session output limit reached: omitted 0 text bytes; discarded 0 text segments, 0 images, 16384 audio items]",
	)}
	if got := renderOutput(collected, intPtrLocal(0)); !reflect.DeepEqual(got, expected) {
		t.Fatalf("rendered %d items: %#v", len(got), got)
	}
}

func TestScatteredLossIsSummarizedOnceLikeRust(t *testing.T) {
	items := make([]ContentItem, 128)
	gaps := make([]OutputGap, 128)
	for n := range items {
		items[n] = InputImage(fmt.Sprintf("img:%d", n), nil)
		gaps[n] = OutputGap{
			Boundary: utils.OutputBoundary{ItemIndex: n},
			Omitted:  OutputOmission{TextBytes: 5, TextItems: 1, ImageItems: 2, AudioItems: 3},
		}
	}
	collected := CollectedOutput{Items: items, Gaps: gaps}
	expected := append(append([]ContentItem{}, items...), rText(
		"[Code Mode session output limit reached: across this observation, omitted 640 text bytes; discarded 128 text segments, 256 images, 384 audio items]",
	))
	if got := renderOutput(collected, intPtrLocal(0)); !reflect.DeepEqual(got, expected) {
		t.Fatalf("rendered %d items, want %d", len(got), len(expected))
	}

	collected.Gaps = append(collected.Gaps, OutputGap{
		Boundary: utils.OutputBoundary{ItemIndex: len(items), TextOffset: len("left")},
		Omitted:  OutputOmission{TextBytes: 9},
	})
	collected.Items = append(collected.Items, rText("leftright"))
	expected = append(append([]ContentItem{}, items...),
		rText("left"),
		rText("[Code Mode session output limit reached: across this observation, omitted 649 text bytes; discarded 128 text segments, 256 images, 384 audio items]"),
		rText("right"),
	)
	if got := renderOutput(collected, intPtrLocal(16)); !reflect.DeepEqual(got, expected) {
		t.Fatalf("split rendered %d items, want %d", len(got), len(expected))
	}
}

func TestTextOnlySplitKeepsLossNoticeInsideRenderedTextLikeRust(t *testing.T) {
	out := newBoundedOutput()
	before := strings.Repeat("🦀", BoundedOutputMaxBytes/8)
	after := strings.Repeat("🎻", BoundedOutputMaxBytes/8)
	out.push(rText(before + "lost" + after))
	rendered := renderOutput(out.intoCollectedOutput(), intPtrLocal(2))
	if len(rendered) != 3 {
		t.Fatalf("rendered %d items, want 3", len(rendered))
	}
	prefix, notice, suffix := rendered[0], rendered[1], rendered[2]
	if !strings.HasPrefix(prefix.Text, "Warning: truncated output") || !strings.Contains(prefix.Text, "🦀") || strings.Contains(prefix.Text, "🎻") {
		t.Fatalf("prefix mismatch: %.80s", prefix.Text)
	}
	if notice.Text != "[Code Mode session output limit reached: omitted 4 text bytes; discarded 0 text segments, 0 images, 0 audio items]" {
		t.Fatalf("notice = %q", notice.Text)
	}
	if suffix.Text != "🎻" {
		t.Fatalf("suffix = %q", suffix.Text)
	}
}

func TestLossNoticesRemainInSourceOrderLikeRust(t *testing.T) {
	before := strings.Repeat("🦀", BoundedOutputMaxBytes/8)
	after := strings.Repeat("🎻", BoundedOutputMaxBytes/8)
	original := before + strings.Repeat("x", 10_000) + after
	image := InputImage("data:image/png;base64,AA==", imageDetailPtr(ImageDetailOriginal))
	audioNotice := rText("[Code Mode session output limit reached: omitted 0 text bytes; discarded 0 text segments, 0 images, 1 audio items]")
	imageNotice := rText("[Code Mode session output limit reached: omitted 0 text bytes; discarded 0 text segments, 1 images, 0 audio items]")
	for _, neighbor := range []struct {
		present bool
		value   string
		lost    int
	}{{false, "", 10000}, {true, "", 10000}, {true, "x", 10004}} {
		splitNotice := rText(fmt.Sprintf(
			"[Code Mode session output limit reached: omitted %d text bytes; discarded 0 text segments, 0 images, 0 audio items]",
			neighbor.lost,
		))
		out := newBoundedOutput()
		out.push(InputAudio(strings.Repeat("a", BoundedOutputMaxAudioBytes+1)))
		out.push(rText(original))
		out.push(InputImage(strings.Repeat("i", BoundedOutputMaxImageBytes+1), nil))
		out.push(image)
		if neighbor.present {
			out.push(rText(neighbor.value))
		}
		collected := out.intoCollectedOutput()
		positions := make([][2]int, 0, len(collected.Gaps))
		for _, gap := range collected.Gaps {
			positions = append(positions, [2]int{gap.Boundary.ItemIndex, gap.Boundary.TextOffset})
		}
		wantPositions := [][2]int{{0, 0}, {0, len(before)}, {1, 0}}
		if !reflect.DeepEqual(positions, wantPositions) {
			t.Fatalf("positions = %#v, want %#v", positions, wantPositions)
		}
		for _, budget := range []int{0, 1, 2, BoundedOutputMaxBytes} {
			rendered := renderOutput(collected, intPtrLocal(budget))
			switch budget {
			case 0:
				wantFirst := rText(fmt.Sprintf(
					"[Code Mode session output limit reached: omitted %d text bytes; discarded 0 text segments, 1 images, 1 audio items]",
					neighbor.lost,
				))
				if len(rendered) < 2 || !reflect.DeepEqual(rendered[0], wantFirst) || !reflect.DeepEqual(rendered[1], image) {
					t.Fatalf("budget 0 rendered %#v", rendered)
				}
			case 1:
				if len(rendered) < 4 || !reflect.DeepEqual(rendered[0], audioNotice) {
					t.Fatalf("budget 1 first notice = %#v", rendered[0])
				}
				want := rText(fmt.Sprintf(
					"[Code Mode session output limit reached: omitted %d text bytes; discarded 0 text segments, 1 images, 0 audio items]",
					neighbor.lost,
				))
				if !reflect.DeepEqual(rendered[2], want) || !reflect.DeepEqual(rendered[3], image) {
					t.Fatalf("budget 1 rendered %#v", rendered)
				}
			default:
				if len(rendered) < 6 {
					t.Fatalf("budget %d rendered %d items", budget, len(rendered))
				}
				if !reflect.DeepEqual(rendered[0], audioNotice) ||
					!reflect.DeepEqual(rendered[2], splitNotice) ||
					!reflect.DeepEqual(rendered[4], imageNotice) ||
					!reflect.DeepEqual(rendered[5], image) {
					t.Fatalf("budget %d notices out of order: %#v", budget, rendered)
				}
				left, right := rendered[1].Text, rendered[3].Text
				if !strings.HasPrefix(left, "🦀") || strings.Contains(left, "🎻") {
					t.Fatalf("left = %.80s", left)
				}
				if !strings.HasSuffix(right, "🎻") || strings.Contains(right, "🦀") {
					t.Fatalf("right = %.80s", right)
				}
				wantTail := 0
				if neighbor.present && neighbor.value == "x" {
					wantTail = 1
				}
				if len(rendered)-6 != wantTail {
					t.Fatalf("budget %d tail = %d, want %d", budget, len(rendered)-6, wantTail)
				}
			}
		}
	}
}

func imageDetailPtr(detail ImageDetail) *ImageDetail { return &detail }
