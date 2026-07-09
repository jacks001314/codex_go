package markdownrender

type KeyValueRow struct {
	Key   string
	Value string
}

func RowsFromMap(values map[string]string) []KeyValueRow {
	out := make([]KeyValueRow, 0, len(values))
	for key, value := range values {
		out = append(out, KeyValueRow{Key: key, Value: value})
	}
	return out
}
