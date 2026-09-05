package markdown

import (
	"strings"
	"testing"

	"codex_go/utils"
)

func citationRenderedText(t *testing.T, source string, cwd string) string {
	t.Helper()
	rendered, err := RenderWithThemeCwd(source, 120, "", cwd)
	if err != nil {
		t.Fatalf("RenderWithThemeCwd(%q, %q) error = %v", source, cwd, err)
	}
	return utils.StripANSI(rendered)
}

func TestFileCitationDirectiveRendersAsLocalLinkPath(t *testing.T) {
	cases := []struct {
		name string
		path string
		cwd  string
		want string
	}{
		{"absolute", `/tmp/report.xlsx`, "", "/tmp/report.xlsx"},
		{"markdown significant", `/tmp/a*b*.txt`, "", "/tmp/a*b*.txt"},
		{"relative", `reports/final.xlsx`, "/repo", "reports/final.xlsx"},
		{"windows", `C:\Users\me\.codex\report.xlsx`, "", "C:/Users/me/.codex/report.xlsx"},
		{"percent literal", `reports/final%20report.xlsx`, "/repo", "reports/final%20report.xlsx"},
		{"location suffix", `/tmp/report#L10`, "", "/tmp/report:10"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := `:codex-file-citation{path="` + tc.path + `"}`
			clean := citationRenderedText(t, source, tc.cwd)
			if !strings.Contains(clean, tc.want) {
				t.Fatalf("citation %q missing %q:\n%s", source, tc.want, clean)
			}
			if strings.Contains(clean, "codex-file-citation") {
				t.Fatalf("directive text leaked into output:\n%s", clean)
			}
		})
	}
}

func TestFileCitationDirectivesStayLiteralInCodeAndHTML(t *testing.T) {
	citation := `:codex-file-citation{path="/tmp/report.xlsx" purpose="output"}`
	for i, source := range []string{
		"`" + citation + "`",
		"```text\n" + citation + "\n```\n",
	} {
		clean := citationRenderedText(t, source, "")
		if !strings.Contains(clean, citation) {
			t.Fatalf("case %d citation was rewritten inside literal markdown:\n%s", i, clean)
		}
	}
	// Unknown HTML wrappers are not parsed by the glamour renderer, but the
	// directive must never be converted into a clickable file link there.
	htmlRaw, err := RenderWithThemeCwd("<span title='"+citation+"'>", 120, "", "")
	if err != nil {
		t.Fatalf("RenderWithThemeCwd(html) error = %v", err)
	}
	if strings.Contains(htmlRaw, "\x1b]8;;") {
		t.Fatalf("citation inside HTML was converted to a hyperlink:\n%s", htmlRaw)
	}
}

func TestFileCitationDirectiveEscapedAndNestedStayLiteral(t *testing.T) {
	citation := `:codex-file-citation{path=/tmp/report.xlsx}`
	source := `\` + citation + ` and ` + citation
	clean := citationRenderedText(t, source, "")
	if !strings.Contains(clean, citation) || !strings.Contains(clean, "/tmp/report.xlsx") {
		t.Fatalf("escaped citation should stay literal while the second renders:\n%s", clean)
	}

	nested := `:unsupported{value="` + citation + `"}`
	if clean := citationRenderedText(t, nested, ""); !strings.Contains(clean, nested) {
		t.Fatalf("nested citation should stay literal:\n%s", clean)
	}
}

func TestFileCitationDirectiveUnquotedAndMultiple(t *testing.T) {
	source := `:codex-file-citation{path=/tmp/a.txt purpose=output} and :codex-file-citation{path="/tmp/b.txt"}`
	clean := citationRenderedText(t, source, "")
	for _, want := range []string{"/tmp/a.txt", "/tmp/b.txt"} {
		if !strings.Contains(clean, want) {
			t.Fatalf("citation missing %q:\n%s", want, clean)
		}
	}
	if strings.Contains(clean, "codex-file-citation") {
		t.Fatalf("unprocessed directive text leaked:\n%s", clean)
	}
}

func TestFileCitationParserSupportsLiteralAndBackslashQuoting(t *testing.T) {
	budget := 1 << 20
	literalPath, ok := parseFileCitationDirective(`:codex-file-citation{path="C:\repo\" purpose="output"}`, 0, &budget)
	if !ok || literalPath.attributes["path"] != `C:\repo\` {
		t.Fatalf("literal Windows trailing separator parsed = %#v ok=%v", literalPath, ok)
	}

	budget = 1 << 20
	escaped, ok := parseFileCitationDirective(`:codex-file-citation{path="/tmp/a\"b" label="team's \"report\""}`, 0, &budget)
	if !ok || escaped.attributes["path"] != `/tmp/a"b` || escaped.attributes["label"] != `team's "report"` {
		t.Fatalf("backslash quoting parsed = %#v ok=%v", escaped, ok)
	}

	budget = 1 << 20
	unquoted, ok := parseFileCitationDirective(":codex-file-citation{path=/tmp/a*b*.txt purpose=output}", 0, &budget)
	if !ok || unquoted.attributes["path"] != "/tmp/a*b*.txt" {
		t.Fatalf("unquoted path parsed = %#v ok=%v", unquoted, ok)
	}
}

func TestFileCitationDirectiveAfterLocalLinkSoftBreakRendersOnOwnLine(t *testing.T) {
	cwd := "/repo"
	source := "[first](<./first.txt>)\n:codex-file-citation{path=\"./second.txt\"}"
	clean := citationRenderedText(t, source, cwd)
	lines := strings.Split(strings.TrimSpace(clean), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if !strings.Contains(last, "./second.txt") {
		t.Fatalf("citation after soft break did not render on its own line:\n%s", clean)
	}
}

func TestFileCitationDirectiveWrapsClickableLocalLink(t *testing.T) {
	rendered, err := RenderWithThemeCwd(`:codex-file-citation{path="/repo/src/main.rs"}`, 60, "", "/repo")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, "\x1b]8;;file:///repo/src/main.rs\x07") {
		t.Fatalf("citation not wrapped in OSC-8 file hyperlink:\n%q", rendered)
	}
	clean := utils.StripANSI(rendered)
	if !strings.Contains(clean, "src/main.rs") || strings.Contains(clean, "codex-file-citation") {
		t.Fatalf("citation display wrong:\n%s", clean)
	}
}
