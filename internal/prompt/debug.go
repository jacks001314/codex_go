package prompt

import (
	"strings"
)

type ContentItem struct {
	Type   string
	Text   string
	Image  string
	Detail *string
}

type ResponseItem struct {
	Type    string
	Role    string
	Content []ContentItem
}

type DebugPrompt struct {
	Input              []ResponseItem
	UseResponsesLite   bool
	BaseInstructions   string
	ParallelToolCalls  bool
	OutputSchemaStrict bool
	AvailableToolNames []string
}

func (p *DebugPrompt) FormattedInputForRequest() []ResponseItem {
	if p == nil {
		return nil
	}
	input := cloneItems(p.Input)
	if p.UseResponsesLite {
		StripImageDetails(input)
	}
	return input
}

func StripImageDetails(items []ResponseItem) {
	for i := range items {
		for j := range items[i].Content {
			if strings.EqualFold(items[i].Content[j].Type, "input_image") {
				items[i].Content[j].Detail = nil
			}
		}
	}
}

func BuildPromptInput(history []ResponseItem, userInput string) []ResponseItem {
	input := cloneItems(history)
	if strings.TrimSpace(userInput) != "" {
		input = append(input, ResponseItem{
			Type: "message",
			Role: "user",
			Content: []ContentItem{{
				Type: "input_text",
				Text: userInput,
			}},
		})
	}
	return input
}

func cloneItems(items []ResponseItem) []ResponseItem {
	out := make([]ResponseItem, len(items))
	for i := range items {
		out[i] = items[i]
		out[i].Content = append([]ContentItem(nil), items[i].Content...)
	}
	return out
}
