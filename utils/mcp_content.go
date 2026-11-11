package utils

import (
	"encoding/json"
	"fmt"
	"strings"
)

// DefaultImageDetail mirrors Rust's DEFAULT_IMAGE_DETAIL for MCP image blocks
// that do not publish a valid codex/imageDetail hint.
const DefaultImageDetail = "high"

const (
	mcpEncryptedContentMetaKey = "codex/encryptedContent"
	mcpImageDetailMetaKey      = "codex/imageDetail"
)

// FromMCPContentBlock projects one MCP ContentBlock into a model-visible tool
// output item. Text, image, and audio blocks become typed items; resource links
// and embedded resources are serialized as text so their payload is preserved
// (Rust #60665). Malformed blocks and unsupported discriminators return an
// error.
func FromMCPContentBlock(block any) (FunctionCallOutputContentItem, error) {
	object, ok := block.(map[string]any)
	if !ok {
		return FunctionCallOutputContentItem{}, fmt.Errorf("MCP content block must be an object")
	}
	contentType, _ := object["type"].(string)
	meta, _ := object["_meta"].(map[string]any)
	switch contentType {
	case "text":
		text, ok := object["text"].(string)
		if !ok {
			return FunctionCallOutputContentItem{}, fmt.Errorf("MCP text block is missing text")
		}
		if encrypted, _ := meta[mcpEncryptedContentMetaKey].(bool); encrypted {
			return FunctionCallOutputContentItem{
				Kind:             ContentEncryptedContent,
				EncryptedContent: text,
			}, nil
		}
		return FunctionCallOutputContentItem{Kind: ContentInputText, Text: text}, nil
	case "image":
		data, ok := object["data"].(string)
		if !ok {
			return FunctionCallOutputContentItem{}, fmt.Errorf("MCP image block is missing data")
		}
		detail := DefaultImageDetail
		if value, ok := meta[mcpImageDetailMetaKey].(string); ok && isImageDetail(value) {
			detail = value
		}
		return FunctionCallOutputContentItem{
			Kind:     ContentInputImage,
			ImageURL: mcpBlockDataURL(data, mcpBlockMimeType(object)),
			Detail:   &detail,
		}, nil
	case "audio":
		data, ok := object["data"].(string)
		if !ok {
			return FunctionCallOutputContentItem{}, fmt.Errorf("MCP audio block is missing data")
		}
		return FunctionCallOutputContentItem{
			Kind:     ContentInputAudio,
			AudioURL: mcpBlockDataURL(data, mcpBlockMimeType(object)),
		}, nil
	case "resource_link", "resource":
		encoded, err := json.Marshal(object)
		if err != nil {
			return FunctionCallOutputContentItem{}, err
		}
		return FunctionCallOutputContentItem{Kind: ContentInputText, Text: string(encoded)}, nil
	default:
		return FunctionCallOutputContentItem{}, fmt.Errorf("unsupported MCP content block type %q", contentType)
	}
}

func mcpBlockMimeType(block map[string]any) string {
	if value, ok := block["mimeType"].(string); ok {
		return value
	}
	if value, ok := block["mime_type"].(string); ok {
		return value
	}
	return ""
}

func mcpBlockDataURL(data string, mimeType string) string {
	if len(data) >= len("data:") && strings.EqualFold(data[:len("data:")], "data:") {
		return data
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return "data:" + mimeType + ";base64," + data
}

func isImageDetail(value string) bool {
	switch value {
	case "auto", "low", "high", "original":
		return true
	default:
		return false
	}
}
