package retainedctx

import (
	"bytes"
	"encoding/json"
	"unicode/utf8"
)

// marshalNoEscape encodes a value without Go's default HTML escaping so the
// produced JSON follows serde_json's byte-level output for the same value.
func marshalNoEscape(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	out := buffer.Bytes()
	if len(out) > 0 && out[len(out)-1] == '\n' {
		out = out[:len(out)-1]
	}
	return append([]byte(nil), out...), nil
}

// marshalOrdered reproduces serde's `#[serde(flatten)]` layout for an ordered
// family record: the value's own fields plus `order` (always) and the optional
// `revision`/`inherited` members.
func marshalOrdered(value any, revision *ResponseItemID, inherited bool, order uint64) ([]byte, error) {
	raw, err := marshalNoEscape(value)
	if err != nil {
		return nil, err
	}
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	if revision != nil {
		encoded, err := marshalNoEscape(string(*revision))
		if err != nil {
			return nil, err
		}
		fields["revision"] = encoded
	}
	if inherited {
		fields["inherited"] = json.RawMessage("true")
	}
	encodedOrder, err := marshalNoEscape(order)
	if err != nil {
		return nil, err
	}
	fields["order"] = encodedOrder
	return marshalNoEscape(fields)
}

// unmarshalOrdered splits a flattened family record into its ordering metadata
// and the retained value.
func unmarshalOrdered[T any](data []byte, value *T) (*ResponseItemID, bool, uint64, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, false, 0, err
	}
	var revision *ResponseItemID
	if raw, ok := fields["revision"]; ok {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, false, 0, err
		}
		id := ResponseItemID(text)
		revision = &id
		delete(fields, "revision")
	}
	var inherited bool
	if raw, ok := fields["inherited"]; ok {
		if err := json.Unmarshal(raw, &inherited); err != nil {
			return nil, false, 0, err
		}
		delete(fields, "inherited")
	}
	var order uint64
	if raw, ok := fields["order"]; ok {
		if err := json.Unmarshal(raw, &order); err != nil {
			return nil, false, 0, err
		}
		delete(fields, "order")
	}
	rest, err := marshalNoEscape(fields)
	if err != nil {
		return nil, false, 0, err
	}
	if err := json.Unmarshal(rest, value); err != nil {
		return nil, false, 0, err
	}
	return revision, inherited, order, nil
}

// marshalList keeps an empty family as `[]` rather than `null`, matching serde.
func marshalList[T any](items []T) ([]json.RawMessage, error) {
	out := make([]json.RawMessage, 0, len(items))
	for i := range items {
		encoded, err := marshalNoEscape(items[i])
		if err != nil {
			return nil, err
		}
		out = append(out, encoded)
	}
	return out, nil
}

// floorCharBoundary mirrors Rust's `str::floor_char_boundary`: the greatest byte
// index not exceeding max that starts a UTF-8 rune.
func floorCharBoundary(text string, max int) int {
	if max >= len(text) {
		return len(text)
	}
	if max <= 0 {
		return 0
	}
	index := max
	for index > 0 && !utf8.RuneStart(text[index]) {
		index--
	}
	return index
}

// truncateToBoundary mirrors Rust's `String::truncate` at a char boundary.
func truncateToBoundary(text string, max int) string {
	return text[:floorCharBoundary(text, max)]
}

func uintPointer(value uint64) *uint64 {
	return &value
}
