package idecontext

import (
	"fmt"
	"strings"
	"unicode"

	"codex_go/turn"
)

const (
	MaxActiveSelectionChars = 40000
	MaxOpenTabs             = 100
	MaxOpenTabsChars        = 20000
	PromptRequestBegin      = "## My request for Codex:"
)

type IdeContext struct {
	ActiveFile *ActiveFile      `json:"activeFile,omitempty"`
	OpenTabs   []FileDescriptor `json:"openTabs,omitempty"`
}

type ActiveFile struct {
	FileDescriptor
	Selection              Range   `json:"selection"`
	ActiveSelectionContent string  `json:"activeSelectionContent,omitempty"`
	Selections             []Range `json:"selections,omitempty"`
}

type FileDescriptor struct {
	Label string `json:"label"`
	Path  string `json:"path"`
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

type Position struct {
	Line      uint `json:"line"`
	Character uint `json:"character"`
}

type PromptContext struct {
	Selection string
	OpenTabs  []string
}

func (c PromptContext) HasContent() bool {
	return c.Selection != "" || len(c.OpenTabs) > 0
}

func ApplyIDEContextToUserInput(context *IdeContext, items *[]turn.TurnUserInput) bool {
	contextText, ok := RenderPromptContext(context)
	if !ok {
		return false
	}

	prefix := contextText + "\n" + PromptRequestBegin + "\n"
	if items == nil {
		return false
	}
	for i := range *items {
		if !isTextInput((*items)[i]) {
			continue
		}
		(*items)[i] = PrefixedTextInput(prefix, (*items)[i])
		return true
	}

	*items = append([]turn.TurnUserInput{{Type: "text", Text: prefix, TextElements: []turn.TextElement{}}}, *items...)
	return true
}

func HasPromptContext(context *IdeContext) bool {
	_, ok := RenderPromptContext(context)
	return ok
}

func ExtractPromptRequestWithOffset(message string) (string, int) {
	index := strings.LastIndex(message, PromptRequestBegin)
	if index < 0 {
		return message, 0
	}

	requestStart := index + len(PromptRequestBegin)
	request := message[requestStart:]
	trimmed := strings.TrimSpace(request)
	leadingTrimmedLen := len(request) - len(strings.TrimLeftFunc(request, unicode.IsSpace))
	return trimmed, requestStart + leadingTrimmedLen
}

func PrefixedTextInput(prefix string, item turn.TurnUserInput) turn.TurnUserInput {
	prefixLen := len(prefix)
	elements := make([]turn.TextElement, 0, len(item.TextElements))
	for _, element := range item.TextElements {
		element.ByteRange.Start += uint(prefixLen)
		element.ByteRange.End += uint(prefixLen)
		elements = append(elements, element)
	}
	item.Type = "text"
	item.Text = prefix + item.Text
	item.TextElements = elements
	return item
}

func RenderPromptContext(context *IdeContext) (string, bool) {
	if context == nil {
		return "", false
	}

	var section strings.Builder
	activeFile := context.ActiveFile
	if activeFile != nil {
		section.WriteString(fmt.Sprintf("\n## Active file: %s\n", activeFile.Path))
	}

	if activeFile != nil {
		selectedRanges := selectedNonEmptyRanges(*activeFile)
		if len(selectedRanges) > 0 && (activeFile.ActiveSelectionContent == "" || len(selectedRanges) > 1) {
			if len(selectedRanges) == 1 {
				section.WriteString("\n## Active selection range:\n")
			} else {
				section.WriteString("\n## Active selection ranges:\n")
			}
			for _, selectedRange := range selectedRanges {
				startLine := selectedRange.Start.Line + 1
				startColumn := selectedRange.Start.Character + 1
				endLine := selectedRange.End.Line + 1
				endColumn := selectedRange.End.Character + 1
				section.WriteString(fmt.Sprintf("- %s: line %d, column %d to line %d, column %d\n", activeFile.Path, startLine, startColumn, endLine, endColumn))
			}
		}
	}

	if activeFile != nil && activeFile.ActiveSelectionContent != "" {
		section.WriteString("\n## Active selection of the file:\n")
		selection, truncated := truncateStringChars(activeFile.ActiveSelectionContent, MaxActiveSelectionChars)
		section.WriteString(selection)
		if truncated {
			section.WriteString(fmt.Sprintf("\n[Selection truncated to %d characters.]\n", MaxActiveSelectionChars))
		}
	}

	if len(context.OpenTabs) > 0 {
		section.WriteString("\n## Open tabs:\n")
		renderedTabs := 0
		renderedTabChars := 0
		for _, tab := range context.OpenTabs {
			if renderedTabs >= MaxOpenTabs {
				break
			}
			tabLine := fmt.Sprintf("- %s: %s\n", tab.Label, tab.Path)
			if renderedTabChars+len(tabLine) > MaxOpenTabsChars {
				break
			}
			section.WriteString(tabLine)
			renderedTabs++
			renderedTabChars += len(tabLine)
		}

		if omittedTabs := len(context.OpenTabs) - renderedTabs; omittedTabs > 0 {
			section.WriteString(fmt.Sprintf("[%d open tabs omitted.]\n", omittedTabs))
		}
	}

	if section.Len() == 0 {
		return "", false
	}
	return "# Context from my IDE setup:\n" + section.String(), true
}

func selectedNonEmptyRanges(activeFile ActiveFile) []Range {
	ranges := activeFile.Selections
	if len(ranges) == 0 {
		ranges = []Range{activeFile.Selection}
	}
	out := make([]Range, 0, len(ranges))
	for _, selectedRange := range ranges {
		if selectedRange.Start != selectedRange.End {
			out = append(out, selectedRange)
		}
	}
	return out
}

func truncateStringChars(value string, maxChars int) (string, bool) {
	if maxChars < 0 {
		maxChars = 0
	}
	count := 0
	for index := range value {
		if count == maxChars {
			return value[:index], true
		}
		count++
	}
	return value, false
}

func isTextInput(input turn.TurnUserInput) bool {
	inputType := strings.TrimSpace(input.Type)
	if inputType == "text" {
		return true
	}
	return inputType == "" && (input.Text != "" || len(input.TextElements) > 0)
}
