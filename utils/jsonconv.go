package utils

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type TOMLValue struct {
	Kind  string
	Value any
}

func JSONToTOML(value any) TOMLValue {
	switch v := value.(type) {
	case nil:
		return TOMLValue{Kind: "string", Value: ""}
	case bool:
		return TOMLValue{Kind: "bool", Value: v}
	case json.Number:
		if i, err := strconv.ParseInt(v.String(), 10, 64); err == nil {
			return TOMLValue{Kind: "integer", Value: i}
		}
		if f, err := strconv.ParseFloat(v.String(), 64); err == nil {
			return TOMLValue{Kind: "float", Value: f}
		}
		return TOMLValue{Kind: "string", Value: v.String()}
	case float64:
		if v == float64(int64(v)) {
			return TOMLValue{Kind: "integer", Value: int64(v)}
		}
		return TOMLValue{Kind: "float", Value: v}
	case string:
		return TOMLValue{Kind: "string", Value: v}
	case []any:
		out := make([]TOMLValue, len(v))
		for i := range v {
			out[i] = JSONToTOML(v[i])
		}
		return TOMLValue{Kind: "array", Value: out}
	case map[string]any:
		out := make(map[string]TOMLValue, len(v))
		for key, item := range v {
			out[key] = JSONToTOML(item)
		}
		return TOMLValue{Kind: "table", Value: out}
	default:
		return TOMLValue{Kind: "string", Value: fmt.Sprint(value)}
	}
}

func DecodeJSONToTOML(data []byte) (TOMLValue, error) {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return TOMLValue{}, err
	}
	return JSONToTOML(value), nil
}

func (v *TOMLValue) Render() string {
	if v == nil {
		return `""`
	}
	switch v.Kind {
	case "string":
		return strconv.Quote(fmt.Sprint(v.Value))
	case "bool":
		if b, _ := v.Value.(bool); b {
			return "true"
		}
		return "false"
	case "integer", "float":
		return fmt.Sprint(v.Value)
	case "array":
		items, _ := v.Value.([]TOMLValue)
		parts := make([]string, len(items))
		for i := range items {
			parts[i] = (&items[i]).Render()
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case "table":
		table, _ := v.Value.(map[string]TOMLValue)
		keys := make([]string, 0, len(table))
		for key := range table {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		var b strings.Builder
		for i, key := range keys {
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(key)
			b.WriteString(" = ")
			item := table[key]
			b.WriteString((&item).Render())
		}
		return b.String()
	default:
		return strconv.Quote(fmt.Sprint(v.Value))
	}
}
