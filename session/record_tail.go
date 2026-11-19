package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// RecordTailPage is a bounded read of one stored thread: the record metadata
// plus its newest item page, decoded without parsing the rest of the history.
type RecordTailPage struct {
	Record     *Record
	Items      []Item
	TotalItems int
	NextCursor string
}

// ReadTailPage reads a record's metadata and its newest pageSize items without
// decoding the whole stored item array. A thread keeps its items in one JSON
// array, so a resume that only projects the newest page otherwise pays to parse
// every stored item; Rust pages thread/items/list out of the history store
// instead of hydrating the whole rollout.
//
// ok is false when the record needs the full materialization path: an inherited
// history base, a JSON layout the scanner does not recognize, or ordinals that
// do not advance with the stored order. Callers then fall back to Read and
// ListItems, which remain the canonical implementation. Archived records are
// returned like Read(threadID, true, true); the caller decides whether a resume
// may proceed.
func (s *Store) ReadTailPage(threadID ThreadID, pageSize int) (*RecordTailPage, bool, error) {
	if s == nil || pageSize <= 0 {
		return nil, false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	path, err := s.Path(threadID)
	if err != nil {
		return nil, false, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, fmt.Errorf("%w: %s", ErrThreadNotFound, threadID)
	}
	if err != nil {
		return nil, false, err
	}
	fields, itemsSpan, ok := scanRecordFields(data)
	if !ok {
		return nil, false, nil
	}
	record, err := decodeRecordFields(data, fields)
	if err != nil {
		return nil, false, nil
	}
	if record.ID == "" {
		record.ID = threadID
	}
	if err := validateThreadID(record.ID); err != nil {
		return nil, false, err
	}
	normalizeRecordSection(record)
	if record.HistoryBase != nil {
		return nil, false, nil
	}
	spans, total, ok := scanArrayTail(data, itemsSpan, pageSize)
	if !ok {
		return nil, false, nil
	}
	items := make([]Item, 0, len(spans))
	firstIndex := total - len(spans)
	var previous uint64
	for offset, span := range spans {
		var item Item
		if err := json.Unmarshal(data[span.start:span.end], &item); err != nil {
			return nil, false, nil
		}
		ordinal := uint64(firstIndex + offset + 1)
		if item.CreatedAtOrdinal == 0 {
			item.CreatedAtOrdinal = ordinal
		}
		if item.CreatedAtOrdinal <= previous {
			return nil, false, nil
		}
		if item.UpdatedAtOrdinal == 0 {
			item.UpdatedAtOrdinal = item.CreatedAtOrdinal
		}
		previous = item.CreatedAtOrdinal
		items = append(items, item)
	}
	next := ""
	if total > len(items) && len(items) > 0 {
		next = fmt.Sprintf("%s:%d", ItemSortCreatedAtOrdinal, items[0].CreatedAtOrdinal)
	}
	return &RecordTailPage{Record: record, Items: items, TotalItems: total, NextCursor: next}, true, nil
}

// recordField is one top-level key/value span of the stored record object.
type recordField struct {
	key   jsonSpan
	value jsonSpan
}

type jsonSpan struct {
	start int
	end   int
}

// scanRecordFields splits the stored record object into its top-level members
// so the item array can be paged separately from the metadata.
func scanRecordFields(data []byte) ([]recordField, jsonSpan, bool) {
	i := skipJSONSpace(data, 0)
	if i >= len(data) || data[i] != '{' {
		return nil, jsonSpan{}, false
	}
	i++
	var fields []recordField
	var items jsonSpan
	itemsFound := false
	for {
		i = skipJSONSpace(data, i)
		if i >= len(data) {
			return nil, jsonSpan{}, false
		}
		switch data[i] {
		case '}':
			if !itemsFound {
				return nil, jsonSpan{}, false
			}
			return fields, items, true
		case ',':
			i++
			continue
		case '"':
		default:
			return nil, jsonSpan{}, false
		}
		keyStart := i
		keyEnd := skipJSONString(data, i+1)
		if keyEnd <= 0 {
			return nil, jsonSpan{}, false
		}
		i = skipJSONSpace(data, keyEnd)
		if i >= len(data) || data[i] != ':' {
			return nil, jsonSpan{}, false
		}
		i = skipJSONSpace(data, i+1)
		valueEnd := skipJSONValue(data, i)
		if valueEnd <= i {
			return nil, jsonSpan{}, false
		}
		value := jsonSpan{start: i, end: valueEnd}
		if bytes.Equal(data[keyStart:keyEnd], []byte(`"items"`)) {
			if itemsFound || data[i] != '[' {
				return nil, jsonSpan{}, false
			}
			itemsFound = true
			items = value
		} else {
			fields = append(fields, recordField{key: jsonSpan{start: keyStart, end: keyEnd}, value: value})
		}
		i = valueEnd
	}
}

// decodeRecordFields rebuilds the record object without its item array and
// decodes that metadata.
func decodeRecordFields(data []byte, fields []recordField) (*Record, error) {
	var buf bytes.Buffer
	for _, field := range fields {
		buf.Grow(field.key.end - field.key.start + field.value.end - field.value.start + 2)
	}
	buf.WriteByte('{')
	for index, field := range fields {
		if index > 0 {
			buf.WriteByte(',')
		}
		buf.Write(data[field.key.start:field.key.end])
		buf.WriteByte(':')
		buf.Write(data[field.value.start:field.value.end])
	}
	buf.WriteByte('}')
	var record Record
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		return nil, err
	}
	return &record, nil
}

// scanArrayTail returns the spans of the last limit array elements and the
// array's element count.
func scanArrayTail(data []byte, span jsonSpan, limit int) ([]jsonSpan, int, bool) {
	if span.start >= span.end || data[span.start] != '[' {
		return nil, 0, false
	}
	i := span.start + 1
	total := 0
	tail := make([]jsonSpan, 0, limit)
	for {
		i = skipJSONSpace(data, i)
		if i >= span.end {
			return nil, 0, false
		}
		switch data[i] {
		case ']':
			return tail, total, true
		case ',':
			i++
			continue
		}
		end := skipJSONValue(data, i)
		if end <= i || end > span.end {
			return nil, 0, false
		}
		total++
		tail = append(tail, jsonSpan{start: i, end: end})
		if len(tail) > limit {
			tail = tail[1:]
		}
		i = end
	}
}

// skipJSONValue returns the offset just past the value starting at i, or -1
// when the value does not terminate inside data.
func skipJSONValue(data []byte, i int) int {
	if i < 0 || i >= len(data) {
		return -1
	}
	switch data[i] {
	case '"':
		return skipJSONString(data, i+1)
	case '{', '[':
		depth := 0
		for i < len(data) {
			switch data[i] {
			case '"':
				i = skipJSONString(data, i+1)
				if i <= 0 {
					return -1
				}
				continue
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					return i + 1
				}
			}
			i++
		}
		return -1
	default:
		for i < len(data) {
			switch data[i] {
			case ',', '}', ']', ' ', '\n', '\r', '\t':
				return i
			}
			i++
		}
		return i
	}
}

// skipJSONString returns the offset just past the closing quote of a string
// whose body starts at i, or -1 when it does not terminate inside data.
func skipJSONString(data []byte, i int) int {
	for i < len(data) {
		offset := bytes.IndexByte(data[i:], '"')
		if offset < 0 {
			return -1
		}
		end := i + offset
		backslashes := 0
		for k := end - 1; k >= 0 && data[k] == '\\'; k-- {
			backslashes++
		}
		if backslashes%2 == 0 {
			return end + 1
		}
		i = end + 1
	}
	return -1
}

func skipJSONSpace(data []byte, i int) int {
	for i < len(data) {
		switch data[i] {
		case ' ', '\n', '\r', '\t':
			i++
		default:
			return i
		}
	}
	return i
}
