package appserver

import (
	"encoding/json"
	"strings"

	"codex_go/internal/turn"
)

const remoteImageURLError = "remote image URLs are not supported; use an inline data URL instead"

func validateTurnUserInputImageURLs(inputs []turn.TurnUserInput) error {
	for i := range inputs {
		input := inputs[i]
		if !turnUserInputIsRemoteImageCandidate(input) {
			continue
		}
		if isRemoteImageURL(input.URL) {
			return jsonRPCInvalidRequest(remoteImageURLError)
		}
	}
	return nil
}

func turnUserInputIsRemoteImageCandidate(input turn.TurnUserInput) bool {
	if strings.TrimSpace(input.URL) == "" {
		return false
	}
	inputType := strings.TrimSpace(input.Type)
	return inputType == "" || strings.EqualFold(inputType, "image")
}

func validateResponseItemImageURLs(items []json.RawMessage) error {
	for i := range items {
		var value any
		if err := json.Unmarshal(items[i], &value); err != nil {
			return err
		}
		if valueContainsRemoteImageURL(value) {
			return jsonRPCInvalidRequest(remoteImageURLError)
		}
	}
	return nil
}

func valueContainsRemoteImageURL(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		if mapContainsRemoteImageURL(typed) {
			return true
		}
		for _, nested := range typed {
			if valueContainsRemoteImageURL(nested) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if valueContainsRemoteImageURL(nested) {
				return true
			}
		}
	}
	return false
}

func mapContainsRemoteImageURL(value map[string]any) bool {
	itemType := strings.TrimSpace(stringFromAny(value["type"]))
	switch itemType {
	case "image", "input_image", "inputImage":
	default:
		return false
	}
	for _, key := range []string{"image_url", "imageUrl", "url"} {
		if isRemoteImageURL(stringFromAny(value[key])) {
			return true
		}
	}
	return false
}

func isRemoteImageURL(imageURL string) bool {
	scheme, _, ok := strings.Cut(strings.TrimSpace(imageURL), ":")
	if !ok {
		return false
	}
	return strings.EqualFold(scheme, "http") || strings.EqualFold(scheme, "https")
}
