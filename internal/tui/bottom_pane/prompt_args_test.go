package bottompane

import "testing"

func TestParseSlashNameMatchesRust(t *testing.T) {
	tests := []struct {
		line       string
		name       string
		rest       string
		restOffset int
		ok         bool
	}{
		{line: "/model gpt-5 high", name: "model", rest: "gpt-5 high", restOffset: 7, ok: true},
		{line: "/clear", name: "clear", rest: "", restOffset: 6, ok: true},
		{line: "/sandbox-add-read-dir   C:/tmp", name: "sandbox-add-read-dir", rest: "C:/tmp", restOffset: 24, ok: true},
		{line: "/", ok: false},
		{line: "model", ok: false},
	}
	for _, tt := range tests {
		got := ParseSlashName(tt.line)
		if got.OK != tt.ok || got.Name != tt.name || got.Rest != tt.rest || got.RestOffset != tt.restOffset {
			t.Fatalf("ParseSlashName(%q) = %#v", tt.line, got)
		}
		if got.OK && got.Rest != "" && tt.line[got.RestOffset:] != got.Rest {
			t.Fatalf("rest offset %d does not point at %q in %q", got.RestOffset, got.Rest, tt.line)
		}
	}
}
