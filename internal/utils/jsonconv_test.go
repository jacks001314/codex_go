package utils

import "testing"

func TestJSONToTOMLValues(t *testing.T) {
	value, err := DecodeJSONToTOML([]byte(`{"outer":{"inner":2},"arr":[true,1],"empty":null,"float":1.25}`))
	if err != nil {
		t.Fatal(err)
	}
	if value.Kind != "table" {
		t.Fatalf("kind = %s", value.Kind)
	}
	table := value.Value.(map[string]TOMLValue)
	if table["empty"].Kind != "string" || table["empty"].Value != "" {
		t.Fatalf("null conversion = %#v", table["empty"])
	}
	if table["float"].Kind != "float" {
		t.Fatalf("float conversion = %#v", table["float"])
	}
	outer := table["outer"].Value.(map[string]TOMLValue)
	if outer["inner"].Kind != "integer" || outer["inner"].Value != int64(2) {
		t.Fatalf("nested conversion = %#v", outer["inner"])
	}
	arr := table["arr"]
	if got := (&arr).Render(); got != "[true, 1]" {
		t.Fatalf("array render = %q", got)
	}
}
