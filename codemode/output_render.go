package codemode

import (
	"fmt"

	"codex_go/audioutil"
	"codex_go/utils"
)

// Renders a Code Mode observation under its own token budget (Rust
// code-mode-runtime `output/output_render.rs`, #57848/#57850).

// maxPositionedNotices keeps the notice buffer small: four full-width u64
// counts per notice add less than 1 KiB.
const maxPositionedNotices = 4

// renderOutput applies the observation's outer response budget. The terminal
// error participates in the same budget as script text because it was already
// pushed into the collected output; the response still carries its status
// separately.
//
// Collector omissions stay outside the token budget: the shared warning
// describes the retained text alone. The shared formatter moves collector
// boundaries along with their source content. If losses are too scattered for
// individual notices, they are reported as one observation-wide total.
func renderOutput(collected CollectedOutput, maxOutputTokens *int) []ContentItem {
	content := make([]utils.FunctionCallOutputContentItem, 0, len(collected.Items))
	boundaries := make([]utils.OutputBoundary, 0, len(collected.Gaps))
	for _, gap := range collected.Gaps {
		boundaries = append(boundaries, gap.Boundary)
	}
	for _, item := range collected.Items {
		switch item.Type {
		case "input_image":
			entry := utils.FunctionCallOutputContentItem{Kind: utils.ContentInputImage, ImageURL: item.ImageURL}
			if item.Detail != nil {
				detail := string(*item.Detail)
				entry.Detail = &detail
			}
			content = append(content, entry)
		case "input_audio":
			content = append(content, utils.FunctionCallOutputContentItem{Kind: utils.ContentInputAudio, AudioURL: item.AudioURL})
		default:
			content = append(content, utils.FunctionCallOutputContentItem{Kind: utils.ContentInputText, Text: item.Text})
		}
	}
	maxTokens := ProtocolDefaultMaxOutputTokensPerExecCall
	if maxOutputTokens != nil {
		maxTokens = *maxOutputTokens
	}
	// Core budgets mixed output in order. Keep that policy even if the
	// collector removed every image or audio item in the observation.
	hasMediaGap := false
	for _, gap := range collected.Gaps {
		if gap.Omitted.ImageItems > 0 || gap.Omitted.AudioItems > 0 {
			hasMediaGap = true
			break
		}
	}
	var formatted []utils.FunctionCallOutputContentItem
	if hasMediaGap {
		formatted = utils.MixedOutputAtTokenBoundaries(content, maxTokens, audioutil.EstimateAudioTokenCountUncached, boundaries)
	} else {
		formatted = utils.FormattedOutputAtTokenBoundaries(content, maxTokens, audioutil.EstimateAudioTokenCountUncached, boundaries)
	}
	return insertLossNotices(formatted, boundaries, collected.Gaps)
}

func insertLossNotices(formatted []utils.FunctionCallOutputContentItem, boundaries []utils.OutputBoundary, gaps []OutputGap) []ContentItem {
	if len(gaps) == 0 {
		out := make([]ContentItem, 0, len(formatted))
		for _, item := range formatted {
			out = append(out, intoCodeModeOutput(item))
		}
		return out
	}
	type position struct{ index, offset int }
	type notice struct {
		position position
		omitted  OutputOmission
	}
	// One extra slot detects when an observation-wide summary is needed.
	notices := make([]notice, 0, maxPositionedNotices+1)
	total := OutputOmission{}
	summaryPosition := position{index: len(formatted)}
	for i, gap := range gaps {
		total.combine(gap.Omitted)
		boundary := boundaries[i]
		// The end of a text item and the start of its successor are one
		// position.
		pos := position{index: boundary.ItemIndex, offset: boundary.TextOffset}
		if boundary.TextOffset != 0 && boundary.ItemIndex < len(formatted) &&
			formatted[boundary.ItemIndex].Kind == utils.ContentInputText &&
			boundary.TextOffset == len(formatted[boundary.ItemIndex].Text) {
			pos = position{index: boundary.ItemIndex + 1}
		}
		// The collector can split one original text item. Even an
		// observation-wide summary must keep its two fragments from reading as
		// contiguous text.
		if gap.Boundary.TextOffset != 0 {
			summaryPosition = pos
		}
		if len(notices) == maxPositionedNotices+1 {
			continue
		}
		if n := len(notices); n > 0 && notices[n-1].position == pos {
			notices[n-1].omitted.combine(gap.Omitted)
			continue
		}
		notices = append(notices, notice{position: pos, omitted: gap.Omitted})
	}
	scope := ""
	if len(notices) == maxPositionedNotices+1 {
		notices = []notice{{position: summaryPosition, omitted: total}}
		scope = "across this observation, "
	}
	// Drain from the end so earlier positions stay valid as text is split.
	reversed := make([]ContentItem, 0, len(formatted)+2*len(notices))
	for i := len(notices) - 1; i >= 0; i-- {
		n := notices[i]
		after := n.position.index + boolToInt(n.position.offset != 0)
		if after > len(formatted) {
			after = len(formatted)
		}
		for j := len(formatted) - 1; j >= after; j-- {
			reversed = append(reversed, intoCodeModeOutput(formatted[j]))
		}
		formatted = formatted[:after]
		if n.position.offset != 0 {
			text := formatted[n.position.index].Text
			if n.position.offset < len(text) {
				reversed = append(reversed, InputText(text[n.position.offset:]))
				formatted[n.position.index].Text = text[:n.position.offset]
			}
		}
		reversed = append(reversed, InputText(fmt.Sprintf(
			"[Code Mode session output limit reached: %somitted %d text bytes; discarded %d text segments, %d images, %d audio items]",
			scope, n.omitted.TextBytes, n.omitted.TextItems, n.omitted.ImageItems, n.omitted.AudioItems,
		)))
	}
	for i := len(formatted) - 1; i >= 0; i-- {
		reversed = append(reversed, intoCodeModeOutput(formatted[i]))
	}
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	return reversed
}

func intoCodeModeOutput(item utils.FunctionCallOutputContentItem) ContentItem {
	switch item.Kind {
	case utils.ContentInputImage:
		var detail *ImageDetail
		if item.Detail != nil {
			value := ImageDetail(*item.Detail)
			detail = &value
		}
		return InputImage(item.ImageURL, detail)
	case utils.ContentInputAudio:
		return InputAudio(item.AudioURL)
	}
	return InputText(item.Text)
}
