package codemode

import (
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"codex_go/utils"
)

// Mirrors codex-rs/code-mode-runtime/src/output/output_collection_tests.rs
// (#57848).

func cmText(value string) ContentItem { return InputText(value) }

func cmImage(url string) ContentItem {
	detail := ImageDetailOriginal
	return InputImage(url, &detail)
}

func cmAudio(url string) ContentItem { return InputAudio(url) }

func cmCollect(items []ContentItem) *boundedOutput {
	output := newBoundedOutput()
	for _, item := range items {
		output.push(item)
	}
	return output
}

func retainedBytes(collector *boundedOutput) int {
	size := func(item *retainedItem) int {
		switch item.kind {
		case retainedContent:
			switch item.content.Type {
			case "input_text":
				return len(item.content.Text)
			case "input_image":
				return len(item.content.ImageURL)
			case "input_audio":
				return len(item.content.AudioURL)
			}
			return 0
		case retainedPartial:
			total := 0
			for _, block := range item.partial.blocks {
				total += len(block)
			}
			return total
		}
		return 0
	}
	if collector.open != nil {
		total := 0
		for i := range collector.open.items {
			total += size(&collector.open.items[i])
		}
		return total
	}
	total := 0
	for i := range collector.split.head {
		total += size(&collector.split.head[i])
	}
	for i := range collector.split.tail {
		total += size(&collector.split.tail[i])
	}
	return total
}

func TestAllEmptyWritesFitBeforeItemLimitCreatesGapLikeRust(t *testing.T) {
	items := make([]ContentItem, OutputMaxItems)
	for i := range items {
		items[i] = cmText("")
	}
	output := cmCollect(items)
	got := output.intoCollectedOutput()
	want := make([]ContentItem, OutputMaxItems)
	for i := range want {
		want[i] = cmText("")
	}
	if !reflect.DeepEqual(got.Items, want) {
		t.Fatalf("intact items = %d, want %d", len(got.Items), len(want))
	}
	if len(got.Gaps) != 0 {
		t.Fatalf("unexpected gaps = %#v", got.Gaps)
	}

	output.push(cmText("last"))
	snapshot := output.intoCollectedOutput()
	if len(snapshot.Items) != OutputMaxItems-1 {
		t.Fatalf("items = %d, want %d", len(snapshot.Items), OutputMaxItems-1)
	}
	if got := snapshot.Items[len(snapshot.Items)-1].Text; got != "last" {
		t.Fatalf("last item = %q", got)
	}
	if len(snapshot.Gaps) != 1 {
		t.Fatalf("gaps = %#v", snapshot.Gaps)
	}
	gap := snapshot.Gaps[0]
	if gap.Boundary.ItemIndex != 16_384 {
		t.Fatalf("gap index = %d, want 16384", gap.Boundary.ItemIndex)
	}
	if gap.Omitted.TextItems != 2 {
		t.Fatalf("gap text items = %d, want 2", gap.Omitted.TextItems)
	}
}

func TestItemPressureKeepsSplitHeadItemsSuffixLikeRust(t *testing.T) {
	half := strings.Repeat("x", BoundedOutputMaxBytes/2)
	retained := cmText(half + half)
	output := cmCollect([]ContentItem{cmText(half + "y" + half)})
	for i := 0; i < OutputMaxItems-1; i++ {
		output.push(cmText(""))
	}
	if got := output.intoCollectedOutput().Items[0]; !reflect.DeepEqual(got, retained) {
		t.Fatalf("first item = %q, want retained prefix", got.Text)
	}

	output.push(cmText(""))
	snapshot := output.intoCollectedOutput()
	if !reflect.DeepEqual(snapshot.Items[0], retained) {
		t.Fatalf("first item changed after eviction")
	}
	if len(snapshot.Items) != OutputMaxItems-1 {
		t.Fatalf("items = %d, want %d", len(snapshot.Items), OutputMaxItems-1)
	}
	if len(snapshot.Gaps) != 2 {
		t.Fatalf("gaps = %d, want 2", len(snapshot.Gaps))
	}
	if snapshot.Gaps[1].Boundary.ItemIndex != 1 {
		t.Fatalf("second gap index = %d, want 1", snapshot.Gaps[1].Boundary.ItemIndex)
	}
	if snapshot.Gaps[1].Omitted.TextItems != 2 {
		t.Fatalf("second gap text items = %d, want 2", snapshot.Gaps[1].Omitted.TextItems)
	}
}

func TestFittingItemsKeepTheirShapeLikeRust(t *testing.T) {
	items := []ContentItem{cmText("first"), cmImage("image"), cmAudio("audio")}
	output := cmCollect(items)
	if got := output.intoCollectedOutput(); !reflect.DeepEqual(got.Items, items) || len(got.Gaps) != 0 {
		t.Fatalf("collected = %#v", got)
	}
}

func TestRollingUnicodeSuffixSurvivesTinyWritesLikeRust(t *testing.T) {
	half := BoundedOutputMaxBytes / 2
	original := strings.Repeat("a", half-1) + strings.Repeat("😀", half) + "z"
	output := cmCollect([]ContentItem{cmText(original)})
	var lastBlock *byte
	for i := 0; i < 10_001; i++ {
		output.push(cmText("z"))
		if output.split == nil {
			t.Fatal("expected truncated output")
		}
		front := output.split.tail[0]
		if front.kind != retainedPartial {
			t.Fatalf("front item kind = %d, want partial", front.kind)
		}
		blocks := front.partial.blocks
		ptr := unsafe.StringData(blocks[len(blocks)-1])
		if lastBlock == nil {
			lastBlock = ptr
		} else if ptr != lastBlock {
			t.Fatalf("last block reallocated at write %d", i)
		}
		if retainedBytes(output) >= BoundedOutputMaxBytes+textBlockBytes {
			t.Fatalf("retained %d bytes at write %d", retainedBytes(output), i)
		}
	}
	head := floorCharBoundary(original, half)
	tail := ceilCharBoundary(original, len(original)-(BoundedOutputMaxBytes-head-10_001))
	snapshot := output.intoCollectedOutput()
	if len(snapshot.Items) != 10_002 {
		t.Fatalf("items = %d, want 10002", len(snapshot.Items))
	}
	want := original[:head] + original[tail:]
	if snapshot.Items[0].Text != want {
		t.Fatalf("first item = %d bytes, want %d", len(snapshot.Items[0].Text), len(want))
	}
	if len(snapshot.Gaps) != 1 {
		t.Fatalf("gaps = %d, want 1", len(snapshot.Gaps))
	}
	gap := snapshot.Gaps[0]
	if gap.Boundary != (utils.OutputBoundary{ItemIndex: 0, TextOffset: head}) {
		t.Fatalf("gap boundary = %#v", gap.Boundary)
	}
	if gap.Omitted.TextBytes != uint64(tail-head) {
		t.Fatalf("gap text bytes = %d, want %d", gap.Omitted.TextBytes, tail-head)
	}
}

func TestBytePressureReclaimsSpaceBorrowedByTheFirstItemLikeRust(t *testing.T) {
	half := BoundedOutputMaxBytes / 2
	suffix := cmText(strings.Repeat("z", half))
	first := cmText(strings.Repeat("a", half+8))
	output := cmCollect([]ContentItem{first})
	for i := 0; i < OutputMaxItems; i++ {
		output.push(cmText(""))
	}
	if got := output.intoCollectedOutput().Items[0]; !reflect.DeepEqual(got, first) {
		t.Fatalf("first item changed before pressure")
	}

	output.push(suffix)
	snapshot := output.intoCollectedOutput()
	if !reflect.DeepEqual(snapshot.Items[len(snapshot.Items)-1], suffix) {
		t.Fatalf("last item is not the suffix")
	}
	if len(snapshot.Gaps) != 1 {
		t.Fatalf("gaps = %d, want 1", len(snapshot.Gaps))
	}
	if snapshot.Items[0].Text != strings.Repeat("a", half) {
		t.Fatalf("first item = %d bytes, want %d", len(snapshot.Items[0].Text), half)
	}
	if snapshot.Gaps[0].Boundary.ItemIndex != 1 {
		t.Fatalf("gap index = %d, want 1", snapshot.Gaps[0].Boundary.ItemIndex)
	}
	if snapshot.Gaps[0].Omitted.TextBytes != 8 {
		t.Fatalf("gap text bytes = %d, want 8", snapshot.Gaps[0].Omitted.TextBytes)
	}
}

func TestImageAdmissionUsesRemainingBytesLikeRust(t *testing.T) {
	half := OutputMaxItems / 2
	items := make([]ContentItem, half)
	for i := range items {
		items[i] = cmText("")
	}
	output := cmCollect(items)
	first := cmImage(strings.Repeat("i", BoundedOutputMaxImageBytes-2))
	last := cmImage("zz")
	for _, item := range []ContentItem{first, cmImage("xxx"), last} {
		output.push(item)
	}
	snapshot := output.intoCollectedOutput()
	if len(snapshot.Items) < 2 ||
		!reflect.DeepEqual(snapshot.Items[len(snapshot.Items)-2], first) ||
		!reflect.DeepEqual(snapshot.Items[len(snapshot.Items)-1], last) {
		t.Fatalf("tail = %#v", snapshot.Items[len(snapshot.Items)-2:])
	}
	if len(snapshot.Gaps) != 1 {
		t.Fatalf("gaps = %d, want 1", len(snapshot.Gaps))
	}
	if snapshot.Gaps[0].Boundary.ItemIndex != half+1 {
		t.Fatalf("gap index = %d, want %d", snapshot.Gaps[0].Boundary.ItemIndex, half+1)
	}
	if snapshot.Gaps[0].Omitted.ImageItems != 1 {
		t.Fatalf("gap image items = %d, want 1", snapshot.Gaps[0].Omitted.ImageItems)
	}

	for i := 0; i < OutputMaxItems; i++ {
		output.push(cmText(""))
	}
	full := cmImage(strings.Repeat("j", BoundedOutputMaxImageBytes))
	output.push(full)
	got := output.intoCollectedOutput()
	if !reflect.DeepEqual(got.Items[len(got.Items)-1], full) {
		t.Fatalf("full image was not admitted after eviction")
	}
}

func TestImageAdmissionReusesSpaceFreedBySameWriteLikeRust(t *testing.T) {
	half := OutputMaxItems / 2
	items := make([]ContentItem, half)
	for i := range items {
		items[i] = cmText("")
	}
	output := cmCollect(items)
	output.push(cmImage(strings.Repeat("i", BoundedOutputMaxImageBytes)))
	for i := 0; i < half-1; i++ {
		output.push(cmText(""))
	}
	replacement := cmImage("replacement")
	output.push(replacement)
	snapshot := output.intoCollectedOutput()
	if !reflect.DeepEqual(snapshot.Items[len(snapshot.Items)-1], replacement) {
		t.Fatalf("replacement image not admitted")
	}
	if len(snapshot.Gaps) != 1 {
		t.Fatalf("gaps = %d, want 1", len(snapshot.Gaps))
	}
	if snapshot.Gaps[0].Omitted.ImageItems != 1 {
		t.Fatalf("gap image items = %d, want 1", snapshot.Gaps[0].Omitted.ImageItems)
	}
}

func TestOneExtraTextByteKeepsLargeImageEitherOrderLikeRust(t *testing.T) {
	half := BoundedOutputMaxBytes / 2
	original := strings.Repeat("a", half) + "b" + strings.Repeat("c", half-1)
	retained := cmText(original[:half] + original[half+1:])
	largeImage := cmImage(strings.Repeat("i", BoundedOutputMaxImageBytes/2+1))
	for _, imageIndex := range []int{0, 1} {
		items := []ContentItem{cmText(original)}
		items = append(items[:imageIndex], append([]ContentItem{largeImage}, items[imageIndex:]...)...)
		items = append(items, cmText("z"))
		snapshot := cmCollect(items).intoCollectedOutput()
		expected := []ContentItem{retained, cmText("z")}
		expected = append(expected[:imageIndex], append([]ContentItem{largeImage}, expected[imageIndex:]...)...)
		if !reflect.DeepEqual(snapshot.Items, expected) {
			t.Fatalf("image index %d item count = %d, want %d", imageIndex, len(snapshot.Items), len(expected))
		}
		if len(snapshot.Gaps) != 1 {
			t.Fatalf("image index %d gaps = %d, want 1", imageIndex, len(snapshot.Gaps))
		}
		gap := snapshot.Gaps[0]
		if gap.Boundary != (utils.OutputBoundary{ItemIndex: 1 - imageIndex, TextOffset: BoundedOutputMaxBytes / 2}) {
			t.Fatalf("image index %d gap boundary = %#v", imageIndex, gap.Boundary)
		}
		if gap.Omitted.TextBytes != 1 {
			t.Fatalf("image index %d gap text bytes = %d, want 1", imageIndex, gap.Omitted.TextBytes)
		}
	}
}

func TestOutputItemTypesHaveIndependentByteAllowancesLikeRust(t *testing.T) {
	items := []ContentItem{
		cmText(strings.Repeat("t", BoundedOutputMaxBytes)),
		cmImage(strings.Repeat("i", BoundedOutputMaxImageBytes)),
		cmAudio("a"),
	}
	snapshot := cmCollect(items).intoCollectedOutput()
	if !reflect.DeepEqual(snapshot.Items, items) || len(snapshot.Gaps) != 0 {
		t.Fatalf("collected = %#v", snapshot)
	}
}

func TestFailedDeliveryKeepsObservationUntilSuccessLikeRust(t *testing.T) {
	url := strings.Repeat("i", BoundedOutputMaxImageBytes)
	collector := newOutputCollector(OutputTruncationServer)
	collector.push(cmImage(url))
	if collector.tryDeliver(func(*outputCollector) bool { return false }) {
		t.Fatal("failed delivery reported success")
	}
	if !collector.tryDeliver(func(taken *outputCollector) bool {
		snapshot := taken.intoCollectedOutput()
		if len(snapshot.Items) != 1 || snapshot.Items[0].Type != "input_image" {
			t.Fatalf("retained items = %#v", snapshot.Items)
		}
		return true
	}) {
		t.Fatal("retry failed")
	}

	full := cmImage(strings.Repeat("j", BoundedOutputMaxImageBytes))
	collector.push(full)
	collector.push(cmText(strings.Repeat("x", 2*BoundedOutputMaxBytes)))
	expected := collector.intoCollectedOutput()
	if !reflect.DeepEqual(expected.Items[0], full) {
		t.Fatalf("expected the full image first")
	}
	if len(expected.Gaps) != 1 {
		t.Fatalf("gaps = %d, want 1", len(expected.Gaps))
	}
	if collector.tryDeliver(func(*outputCollector) bool { return false }) {
		t.Fatal("failed delivery reported success")
	}
	collector.push(cmText(""))
	expected.Items = append(expected.Items, cmText(""))
	if !collector.tryDeliver(func(taken *outputCollector) bool {
		if got := taken.intoCollectedOutput(); !reflect.DeepEqual(got, expected) {
			t.Fatalf("retried snapshot = %#v", got)
		}
		return true
	}) {
		t.Fatal("retry failed")
	}
	if got := collector.intoCollectedOutput(); len(got.Items) != 0 || len(got.Gaps) != 0 {
		t.Fatalf("collector not emptied: %#v", got)
	}
}

func TestRepeatedEvictionKeepsRecentWritesAroundImageLikeRust(t *testing.T) {
	half := strings.Repeat("p", BoundedOutputMaxBytes/2)
	image := cmImage(strings.Repeat("i", 2*BoundedOutputMaxBytes))
	output := cmCollect([]ContentItem{cmText(half), cmText("b"), image, cmText(half)})
	chunk := strings.Repeat("x", 64)
	writes := 2 * OutputMaxItems
	for i := 0; i < writes; i++ {
		output.push(cmText(chunk))
	}
	snapshot := output.intoCollectedOutput()
	recent := BoundedOutputMaxBytes / 2 / len(chunk)
	if len(snapshot.Items) != 2+recent {
		t.Fatalf("items = %d, want %d", len(snapshot.Items), 2+recent)
	}
	if !reflect.DeepEqual(snapshot.Items[0], cmText(half)) || !reflect.DeepEqual(snapshot.Items[1], image) {
		t.Fatalf("prefix = %#v", snapshot.Items[:2])
	}
	for _, item := range snapshot.Items[2:] {
		if item.Type != "input_text" || item.Text != chunk {
			t.Fatalf("recent item = %#v", item)
		}
	}
	if len(snapshot.Gaps) != 2 {
		t.Fatalf("gaps = %d, want 2", len(snapshot.Gaps))
	}
	before, after := snapshot.Gaps[0], snapshot.Gaps[1]
	if before.Boundary.ItemIndex != 1 || after.Boundary.ItemIndex != 2 {
		t.Fatalf("gap positions = %d, %d", before.Boundary.ItemIndex, after.Boundary.ItemIndex)
	}
	if before.Omitted.TextBytes != 1 {
		t.Fatalf("before gap text bytes = %d, want 1", before.Omitted.TextBytes)
	}
	if after.Omitted.TextBytes != uint64(writes*len(chunk)) {
		t.Fatalf("after gap text bytes = %d, want %d", after.Omitted.TextBytes, writes*len(chunk))
	}
	if after.Omitted.TextItems != uint64(1+writes-recent) {
		t.Fatalf("after gap text items = %d, want %d", after.Omitted.TextItems, 1+writes-recent)
	}
}
