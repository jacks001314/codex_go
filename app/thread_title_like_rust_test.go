package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseGeneratedThreadTitleLikeRust(t *testing.T) {
	tests := []struct {
		name        string
		title       string
		want        string
		wantOK      bool
		response    string
		encodeTitle bool
	}{
		{name: "whitespace", title: "  Fix  \n\t login   errors  ", want: "Fix login errors", wantOK: true, encodeTitle: true},
		{name: "double quotes", title: `"Fix login errors!"`, want: "Fix login errors", wantOK: true, encodeTitle: true},
		{name: "single quotes", title: "'Fix login errors?'", want: "Fix login errors", wantOK: true, encodeTitle: true},
		{name: "backticks", title: "`Fix login errors.`", want: "Fix login errors", wantOK: true, encodeTitle: true},
		{name: "curly quotes", title: "“Fix login errors!”", want: "Fix login errors", wantOK: true, encodeTitle: true},
		{name: "dot net", title: ".NET migration.", want: ".NET migration", wantOK: true, encodeTitle: true},
		{name: "important", title: "!important styling!", want: "!important styling", wantOK: true, encodeTitle: true},
		{name: "empty response"},
		{name: "not json", response: "not json"},
		{name: "null", response: "null"},
		{name: "boolean", response: "true"},
		{name: "number", response: "42"},
		{name: "array", response: "[]"},
		{name: "title array", response: `["title"]`},
		{name: "plain string", response: `"plain title"`},
		{name: "missing title", response: "{}"},
		{name: "numeric title", response: `{"title":7}`},
		{name: "unknown field", response: `{"title":"valid","extra":true}`},
		{name: "empty title", response: `{"title":"  \t  "}`},
		{name: "trailing value", response: `{"title":"valid"} null`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			response := tc.response
			if tc.encodeTitle {
				encoded, err := json.Marshal(map[string]string{"title": tc.title})
				if err != nil {
					t.Fatalf("marshal title: %v", err)
				}
				response = string(encoded)
			}
			got, ok := parseGeneratedThreadTitle(response)
			if ok != tc.wantOK || got != tc.want {
				t.Fatalf("parseGeneratedThreadTitle(%s) = %q, %v; want %q, %v", response, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestParseGeneratedThreadTitleTruncatesByRuneLikeRust(t *testing.T) {
	want := strings.Repeat("🚀", threadTitleMaxChars)
	response, err := json.Marshal(map[string]string{"title": want + "x"})
	if err != nil {
		t.Fatalf("marshal title: %v", err)
	}
	got, ok := parseGeneratedThreadTitle(string(response))
	if !ok || got != want {
		t.Fatalf("parseGeneratedThreadTitle = %q, %v; want %q, true", got, ok, want)
	}
}
