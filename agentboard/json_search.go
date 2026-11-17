package agentboard

import (
	"encoding/json"
	"strings"
)

// matchesPostJSON mirrors Rust `local/json_search.rs::matches_post`: a query
// matches the folded payload text, any decoded JSON string/key/scalar in the
// stored payload, or a JSON string found at the configured pointer. Function
// call arguments are stored as a JSON-encoded string, so the pointer value is
// decoded once more before matching.
func matchesPostJSON(text string, pointer string, needle string) bool {
	if strings.Contains(foldCase(text), needle) {
		return true
	}
	var post any
	if err := json.Unmarshal([]byte(text), &post); err != nil {
		return false
	}
	if containsJSONLiteral(post, needle) {
		return true
	}
	raw, ok := jsonPointerString(post, pointer)
	if !ok {
		return false
	}
	var arguments any
	if err := json.Unmarshal([]byte(raw), &arguments); err != nil {
		return false
	}
	return containsJSONLiteral(arguments, needle)
}

// jsonSearchPostPage mirrors Rust `local/json_search.rs::page`: keep the posts
// whose decoded JSON matches the needle, skip the cursor offset, and retain one
// lookahead post so the caller can derive has_more.
func jsonSearchPostPage(posts []storedPost, pointer string, needle string, offset uint32, limit int) []storedPost {
	skipped := uint32(0)
	matched := make([]storedPost, 0, minInt(len(posts), limit+1))
	for _, post := range posts {
		if !matchesPostJSON(post.Text, pointer, needle) {
			continue
		}
		if skipped < offset {
			skipped++
			continue
		}
		matched = append(matched, post)
		if len(matched) > limit {
			break
		}
	}
	return matched
}

// containsJSONLiteral mirrors Rust `contains_literal`: strings match folded
// text, object keys and values are searched, and numbers/bools/null compare
// against their JSON spelling.
func containsJSONLiteral(value any, needle string) bool {
	switch typed := value.(type) {
	case string:
		return strings.Contains(foldCase(typed), needle)
	case []any:
		for _, item := range typed {
			if containsJSONLiteral(item, needle) {
				return true
			}
		}
		return false
	case map[string]any:
		for key, item := range typed {
			if strings.Contains(foldCase(key), needle) || containsJSONLiteral(item, needle) {
				return true
			}
		}
		return false
	case nil:
		return strings.Contains("null", needle)
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return false
		}
		return strings.Contains(foldCase(string(encoded)), needle)
	}
}

// jsonPointerString resolves an RFC 6901 JSON pointer to a string value.
func jsonPointerString(value any, pointer string) (string, bool) {
	pointer = strings.TrimSpace(pointer)
	if pointer == "" || pointer[0] != '/' {
		return "", false
	}
	current := value
	for _, raw := range strings.Split(pointer[1:], "/") {
		token := strings.ReplaceAll(strings.ReplaceAll(raw, "~1", "/"), "~0", "~")
		object, ok := current.(map[string]any)
		if !ok {
			return "", false
		}
		current, ok = object[token]
		if !ok {
			return "", false
		}
	}
	text, ok := current.(string)
	return text, ok
}
