package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"unicode/utf16"
)

func MarshalString(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := writeASCIIJSON(&buf, decoded); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func writeASCIIJSON(buf *bytes.Buffer, value any) error {
	switch v := value.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if v {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case float64:
		buf.WriteString(strconv.FormatFloat(v, 'f', -1, 64))
	case string:
		writeASCIIJSONString(buf, v)
	case []any:
		buf.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeASCIIJSON(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]any:
		buf.WriteByte('{')
		i := 0
		for key, item := range v {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeASCIIJSONString(buf, key)
			buf.WriteByte(':')
			if err := writeASCIIJSON(buf, item); err != nil {
				return err
			}
			i++
		}
		buf.WriteByte('}')
	default:
		return fmt.Errorf("unsupported JSON value %T", value)
	}
	return nil
}

func writeASCIIJSONString(buf *bytes.Buffer, value string) {
	buf.WriteByte('"')
	for _, ch := range value {
		switch ch {
		case '\\':
			buf.WriteString(`\\`)
		case '"':
			buf.WriteString(`\"`)
		case '\b':
			buf.WriteString(`\b`)
		case '\f':
			buf.WriteString(`\f`)
		case '\n':
			buf.WriteString(`\n`)
		case '\r':
			buf.WriteString(`\r`)
		case '\t':
			buf.WriteString(`\t`)
		default:
			if ch < 0x20 {
				buf.WriteString(fmt.Sprintf(`\u%04x`, ch))
			} else if ch <= 0x7f {
				buf.WriteRune(ch)
			} else if ch <= 0xffff {
				buf.WriteString(fmt.Sprintf(`\u%04x`, ch))
			} else {
				r1, r2 := utf16.EncodeRune(ch)
				buf.WriteString(fmt.Sprintf(`\u%04x\u%04x`, r1, r2))
			}
		}
	}
	buf.WriteByte('"')
}
