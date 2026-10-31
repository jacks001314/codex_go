package config

import (
	"os"
	"path/filepath"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
)

// These shapes were recorded from codex 0.166 (Rust) failing the same document,
// so the messages, columns and caret widths below are the reference output.
func TestTOMLDiagnosticMatchesRustParseErrors(t *testing.T) {
	tests := []struct {
		name        string
		toml        string
		want        string
		wantSnippet string
	}{
		{
			name:        "unclosed table",
			toml:        "[broken\n",
			want:        "0: C:\\home\\config.toml:1:8: unclosed table, expected `]`\n1: TOML parse error at line 1, column 8\n     |\n   1 | [broken\n     |        ^\n   unclosed table, expected `]`",
			wantSnippet: "C:\\home\\config.toml:1:8: unclosed table, expected `]`\n  |\n1 | [broken\n  |        ^",
		},
		{
			name:        "two-digit line pads the gutter",
			toml:        "#c\n\n\n\n\n\n\n\n\n\n\na = \n",
			want:        "0: C:\\home\\config.toml:12:5: string values must be quoted, expected literal string\n1: TOML parse error at line 12, column 5\n      |\n   12 | a = \n      |     ^\n   string values must be quoted, expected literal string",
			wantSnippet: "C:\\home\\config.toml:12:5: string values must be quoted, expected literal string\n   |\n12 | a = \n   |     ^",
		},
		{
			name:        "folded bare value",
			toml:        "a = true false\n",
			want:        "0: C:\\home\\config.toml:1:5: string values must be quoted, expected literal string\n1: TOML parse error at line 1, column 5\n     |\n   1 | a = true false\n     |     ^^^^^^^^^^\n   string values must be quoted, expected literal string",
			wantSnippet: "C:\\home\\config.toml:1:5: string values must be quoted, expected literal string\n  |\n1 | a = true false\n  |     ^^^^^^^^^^",
		},
		{
			name:        "second decimal point",
			toml:        "a = 1.2.3\n",
			want:        "0: C:\\home\\config.toml:1:8: invalid float, expected nothing\n1: TOML parse error at line 1, column 8\n     |\n   1 | a = 1.2.3\n     |        ^^\n   invalid float, expected nothing",
			wantSnippet: "C:\\home\\config.toml:1:8: invalid float, expected nothing\n  |\n1 | a = 1.2.3\n  |        ^^",
		},
		{
			name:        "impossible month",
			toml:        "a = 2020-13-01\n",
			want:        "0: C:\\home\\config.toml:1:5: invalid date, expected month between 01 and 12\n1: TOML parse error at line 1, column 5\n     |\n   1 | a = 2020-13-01\n     |     ^^^^^^^^^^\n   invalid date, expected month between 01 and 12",
			wantSnippet: "C:\\home\\config.toml:1:5: invalid date, expected month between 01 and 12\n  |\n1 | a = 2020-13-01\n  |     ^^^^^^^^^^",
		},
		{
			name:        "hexadecimal overflow",
			toml:        "a = 0xFFFFFFFFFFFFFFFFFF\n",
			want:        "0: C:\\home\\config.toml:1:5: invalid type: integer `4722366482869645213695` as i128, expected any valid TOML value\n1: TOML parse error at line 1, column 5\n     |\n   1 | a = 0xFFFFFFFFFFFFFFFFFF\n     |     ^^^^^^^^^^^^^^^^^^^^\n   invalid type: integer `4722366482869645213695` as i128, expected any valid TOML value",
			wantSnippet: "C:\\home\\config.toml:1:5: invalid type: integer `4722366482869645213695` as i128, expected any valid TOML value\n  |\n1 | a = 0xFFFFFFFFFFFFFFFFFF\n  |     ^^^^^^^^^^^^^^^^^^^^",
		},
		{
			name:        "invalid escape",
			toml:        "a = \"\\q\"\n",
			want:        "0: C:\\home\\config.toml:1:7: missing escaped value, expected `b`, `e`, `f`, `n`, `r`, `\\`, `\"`, `x`, `u`, `U`\n1: TOML parse error at line 1, column 7\n     |\n   1 | a = \"\\q\"\n     |       ^\n   missing escaped value, expected `b`, `e`, `f`, `n`, `r`, `\\`, `\"`, `x`, `u`, `U`",
			wantSnippet: "C:\\home\\config.toml:1:7: missing escaped value, expected `b`, `e`, `f`, `n`, `r`, `\\`, `\"`, `x`, `u`, `U`\n  |\n1 | a = \"\\q\"\n  |       ^",
		},
		{
			name:        "short unicode escape",
			toml:        "a = \"\\u\"\n",
			want:        "0: C:\\home\\config.toml:1:8: too few unicode value digits, expected unicode hexadecimal value\n1: TOML parse error at line 1, column 8\n     |\n   1 | a = \"\\u\"\n     |        ^\n   too few unicode value digits, expected unicode hexadecimal value",
			wantSnippet: "C:\\home\\config.toml:1:8: too few unicode value digits, expected unicode hexadecimal value\n  |\n1 | a = \"\\u\"\n  |        ^",
		},
		{
			name:        "incomplete seconds",
			toml:        "a = 12:34:\n",
			want:        "0: C:\\home\\config.toml:1:5: invalid time, expected second\n1: TOML parse error at line 1, column 5\n     |\n   1 | a = 12:34:\n     |     ^^^^^^\n   invalid time, expected second",
			wantSnippet: "C:\\home\\config.toml:1:5: invalid time, expected second\n  |\n1 | a = 12:34:\n  |     ^^^^^^",
		},
		{
			name:        "truncated date",
			toml:        "a = 2020-01\n",
			want:        "0: C:\\home\\config.toml:1:5: invalid date, expected `-` (MM-DD)\n1: TOML parse error at line 1, column 5\n     |\n   1 | a = 2020-01\n     |     ^^^^^^^\n   invalid date, expected `-` (MM-DD)",
			wantSnippet: "C:\\home\\config.toml:1:5: invalid date, expected `-` (MM-DD)\n  |\n1 | a = 2020-01\n  |     ^^^^^^^",
		},
		{
			name:        "invalid second",
			toml:        "a = 2020-01-01T00:00:70Z\n",
			want:        "0: C:\\home\\config.toml:1:5: invalid time, expected second between 00 and 60\n1: TOML parse error at line 1, column 5\n     |\n   1 | a = 2020-01-01T00:00:70Z\n     |     ^^^^^^^^^^^^^^^^^^^^\n   invalid time, expected second between 00 and 60",
			wantSnippet: "C:\\home\\config.toml:1:5: invalid time, expected second between 00 and 60\n  |\n1 | a = 2020-01-01T00:00:70Z\n  |     ^^^^^^^^^^^^^^^^^^^^",
		},
		{
			name:        "dotted key duplicate",
			toml:        "a.b = 1\na.b = 2\n",
			want:        "0: C:\\home\\config.toml:2:3: duplicate key\n1: TOML parse error at line 2, column 3\n     |\n   2 | a.b = 2\n     |   ^\n   duplicate key",
			wantSnippet: "C:\\home\\config.toml:2:3: duplicate key\n  |\n2 | a.b = 2\n  |   ^",
		},
		{
			name:        "dotted key table duplicate",
			toml:        "[a]\nb.c = 1\n[a.b]\n",
			want:        "0: C:\\home\\config.toml:3:4: duplicate key\n1: TOML parse error at line 3, column 4\n     |\n   3 | [a.b]\n     |    ^\n   duplicate key",
			wantSnippet: "C:\\home\\config.toml:3:4: duplicate key\n  |\n3 | [a.b]\n  |    ^",
		},
		{
			name:        "key without value in inline table",
			toml:        "a = {b\n",
			want:        "0: C:\\home\\config.toml:1:7: unclosed inline table, expected `}`\n1: TOML parse error at line 1, column 7\n     |\n   1 | a = {b\n     |       ^\n   unclosed inline table, expected `}`",
			wantSnippet: "C:\\home\\config.toml:1:7: unclosed inline table, expected `}`\n  |\n1 | a = {b\n  |       ^",
		},
		{
			name:        "extending an integer",
			toml:        "a = 1\n[a.b]\n",
			want:        "0: C:\\home\\config.toml:2:2: cannot extend value of type integer with a dotted key\n1: TOML parse error at line 2, column 2\n     |\n   2 | [a.b]\n     |  ^\n   cannot extend value of type integer with a dotted key",
			wantSnippet: "C:\\home\\config.toml:2:2: cannot extend value of type integer with a dotted key\n  |\n2 | [a.b]\n  |  ^",
		},
		{
			name:        "empty inline table value",
			toml:        "a = {b = }\n",
			want:        "0: C:\\home\\config.toml:1:10: invalid literal string, expected `'`\n1: TOML parse error at line 1, column 10\n     |\n   1 | a = {b = }\n     |          ^\n   invalid literal string, expected `'`",
			wantSnippet: "C:\\home\\config.toml:1:10: invalid literal string, expected `'`\n  |\n1 | a = {b = }\n  |          ^",
		},
		{
			name:        "unclosed array table",
			toml:        "[[a]\n",
			want:        "0: C:\\home\\config.toml:1:5: unclosed array table, expected `]`\n1: TOML parse error at line 1, column 5\n     |\n   1 | [[a]\n     |     ^\n   unclosed array table, expected `]`",
			wantSnippet: "C:\\home\\config.toml:1:5: unclosed array table, expected `]`\n  |\n1 | [[a]\n  |     ^",
		},
		{
			name:        "empty key",
			toml:        "= 1\n",
			want:        "0: C:\\home\\config.toml:1:1: unquoted keys cannot be empty, expected letters, numbers, `-`, `_`\n1: TOML parse error at line 1, column 1\n     |\n   1 | = 1\n     | ^\n   unquoted keys cannot be empty, expected letters, numbers, `-`, `_`",
			wantSnippet: "C:\\home\\config.toml:1:1: unquoted keys cannot be empty, expected letters, numbers, `-`, `_`\n  |\n1 | = 1\n  | ^",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var values map[string]any
			err := toml.Unmarshal([]byte(tc.toml), &values)
			if err == nil {
				t.Fatalf("expected %q to fail to parse", tc.toml)
			}
			if got := TOMLDiagnostic(`C:\home\config.toml`, tc.toml, err).Error(); got != tc.want {
				t.Errorf("diagnostic mismatch\n got: %q\nwant: %q", got, tc.want)
			}
			snippet, ok := TOMLConfigSnippet(`C:\home\config.toml`, tc.toml, err)
			if !ok {
				t.Fatalf("expected a snippet for %q", tc.toml)
			}
			if snippet != tc.wantSnippet {
				t.Errorf("snippet mismatch\n got: %q\nwant: %q", snippet, tc.wantSnippet)
			}
		})
	}
}

// The loader's own display, which `codex login status` prints, carries only the
// position line.
func TestConfigLoadErrorUsesRustMessageWording(t *testing.T) {
	const text = "[broken\n"
	var values map[string]any
	decodeErr := toml.Unmarshal([]byte(text), &values)
	if decodeErr == nil {
		t.Fatalf("expected a parse failure")
	}
	loadErr := newConfigLoadError(`C:\home\config.toml`, text, decodeErr)
	want := "C:\\home\\config.toml:1:8: unclosed table, expected " + tomlUnclosedTableMessage
	if got := loadErr.Error(); got != want {
		t.Errorf("load error mismatch\n got: %q\nwant: %q", got, want)
	}
	snippet, ok := TOMLSyntaxSnippetOf(loadErr)
	if !ok {
		t.Fatalf("expected a syntax snippet")
	}
	if snippet != want+"\n  |\n1 | [broken\n  |        ^" {
		t.Errorf("snippet mismatch\n got: %q", snippet)
	}
}

// ReadConfigDocumentFile keeps the document it parsed so callers can render the
// snippet, and reports the failure in Rust's wording.
func TestReadConfigDocumentFileReportsSyntaxDiagnostic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	const text = "[broken\n"
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	_, document, err := ReadConfigDocumentFile(path)
	if document != text {
		t.Errorf("document = %q, want %q", document, text)
	}
	if err == nil {
		t.Fatalf("expected a parse failure")
	}
	want := "0: " + path + ":1:8: unclosed table, expected " + tomlUnclosedTableMessage +
		"\n1: TOML parse error at line 1, column 8\n     |\n   1 | [broken\n     |        ^\n   unclosed table, expected " + tomlUnclosedTableMessage
	if got := err.Error(); got != want {
		t.Errorf("diagnostic mismatch\n got: %q\nwant: %q", got, want)
	}
}

// Failures that are not TOML syntax errors keep their own text.
func TestTOMLDiagnosticIgnoresOtherErrors(t *testing.T) {
	err := os.ErrNotExist
	if got := TOMLDiagnostic("config.toml", "", err); got != err {
		t.Errorf("TOMLDiagnostic changed an unrelated error: %v", got)
	}
	if snippet, ok := TOMLConfigSnippet("config.toml", "", err); ok || snippet != "" {
		t.Errorf("TOMLConfigSnippet reported an unrelated error: %q", snippet)
	}
}

const tomlUnclosedTableMessage = "`]`"
