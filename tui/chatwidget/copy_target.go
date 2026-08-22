package chatwidget

import (
	"regexp"
	"strings"
)

const (
	CopyTargetPickerViewID = "copy-target-picker"
	CopyTargetWholeID      = "whole-response"
)

// CopyTarget is one selectable target produced by /copy (#39997): the whole
// response plus each fenced code block and nested blockquote from the latest
// assistant response.
type CopyTarget struct {
	ID          string
	Label       string
	Text        string
	Description string
}

var fencedCodeBlockRe = regexp.MustCompile("(?s)```([^\n]*)\n(.*?)\n```")

// CopyTargetsFromMarkdown extracts the /copy targets from an assistant markdown
// response, mirroring Rust's `/copy` picker target extraction (#39997). Target
// 0 is always the whole response, followed by each fenced code block (labelled
// by its language) and each blockquote, preserving source whitespace.
func CopyTargetsFromMarkdown(markdown string) []CopyTarget {
	markdown = strings.TrimSpace(markdown)
	if markdown == "" {
		return nil
	}
	out := make([]CopyTarget, 0, 1+8)
	out = append(out, CopyTarget{
		ID:          CopyTargetWholeID,
		Label:       "Whole response",
		Text:        markdown,
		Description: "Copy the entire response as Markdown.",
	})

	type span struct {
		start int
		end   int
		label string
	}
	type item struct {
		label string
		text  string
		start int
		end   int
	}
	var items []item

	// Fenced code blocks.
	for _, match := range fencedCodeBlockRe.FindAllStringSubmatchIndex(markdown, -1) {
		lang := languageFromFence(markdown[match[2]:match[3]])
		label := "Code block"
		if lang != "" {
			label += " (" + lang + ")"
		}
		items = append(items, item{
			label: label,
			text:  markdown[match[0]:match[1]],
			start: match[0],
			end:   match[1],
		})
	}

	// Contiguous blockquote lines (nested quotes preserved).
	lines := strings.Split(markdown, "\n")
	quoteStart := -1
	collectQuote := func(qStart, qEnd int) {
		if qStart < 0 || qEnd <= qStart {
			return
		}
		text := strings.TrimSpace(strings.Join(lines[qStart:qEnd], "\n"))
		if text == "" {
			return
		}
		items = append(items, item{label: "Blockquote", text: text, start: -1, end: -1})
	}
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimLeft(line, " \t"), ">") {
			if quoteStart < 0 {
				quoteStart = i
			}
		} else if quoteStart >= 0 {
			collectQuote(quoteStart, i)
			quoteStart = -1
		}
	}
	collectQuote(quoteStart, len(lines))

	for _, it := range items {
		out = append(out, CopyTarget{
			ID:          "target-" + sanitizeCopyTargetID(it.label),
			Label:       it.label,
			Text:        it.text,
			Description: previewCopyTarget(it.text),
		})
	}
	return out
}

func languageFromFence(lang string) string {
	lang = strings.TrimSpace(lang)
	if lang == "" {
		return ""
	}
	return strings.Fields(lang)[0]
}

func sanitizeCopyTargetID(label string) string {
	var b strings.Builder
	for i, r := range strings.ToLower(strings.TrimSpace(label)) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else if r == ' ' {
			if i > 0 && b.Len() > 0 {
				b.WriteRune('-')
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func previewCopyTarget(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	first := strings.SplitN(text, "\n", 2)[0]
	runes := []rune(first)
	if len(runes) > 80 {
		return string(runes[:77]) + "..."
	}
	return first
}

// NewCopyTargetPickerView builds the /copy target picker (#39997).
func NewCopyTargetPickerView(markdown string) SelectionView {
	targets := CopyTargetsFromMarkdown(markdown)
	items := make([]SelectionItem, 0, len(targets))
	for _, target := range targets {
		items = append(items, SelectionItem{
			ID:          target.ID,
			Name:        target.Label,
			Description: target.Description,
		})
	}
	if len(items) == 0 {
		items = append(items, SelectionItem{Name: "No copy targets", Disabled: true})
	}
	return SelectionView{
		ViewID:      CopyTargetPickerViewID,
		Title:       "Copy response as",
		FooterHint:  standardPopupHintLine,
		AllowCancel: true,
		Items:       items,
	}
}

// CopyTargetForID returns the target matching a picker option ID, or the whole
// response when the marker id is used.
func CopyTargetForID(targets []CopyTarget, id string) (CopyTarget, bool) {
	if id == CopyTargetWholeID {
		for _, target := range targets {
			if target.ID == CopyTargetWholeID {
				return target, true
			}
		}
	}
	for _, target := range targets {
		if target.ID == id || target.Label == id {
			return target, true
		}
	}
	return CopyTarget{}, false
}
