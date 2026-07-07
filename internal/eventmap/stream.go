package eventmap

import (
	"strings"
)

type FinalizedFacts struct {
	LastAgentMessage                string
	DefersMailboxDeliveryToNextTurn bool
	MemoryCitation                  string
}

func LastAssistantMessageFromItem(item *ResponseItem, planMode bool) (string, bool) {
	text, ok := RawAssistantOutputTextFromItem(item)
	if !ok || text == "" {
		return "", false
	}
	text = StripHiddenAssistantMarkup(text, planMode)
	if strings.TrimSpace(text) == "" {
		return "", false
	}
	return text, true
}

func CompletedItemDefersMailboxDeliveryToNextTurn(item *ResponseItem, planMode bool) bool {
	if item == nil {
		return false
	}
	if item.Kind == ResponseImageGeneration {
		return true
	}
	if item.Kind != ResponseMessage || item.Role != "assistant" || item.Phase == "commentary" {
		return false
	}
	_, ok := LastAssistantMessageFromItem(item, planMode)
	return ok
}

func ResponseInputToResponseItem(callID string, output string) ResponseItem {
	return ResponseItem{
		Kind:            ResponseOther,
		ID:              callID,
		WebSearchAction: "function_call_output",
		ImageResult:     output,
	}
}
