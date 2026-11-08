package historycell

import (
	"strings"
	"testing"

	"codex_go/tui"
	"codex_go/utils"
)

// sgrState is the subset of SGR state a prompt row can carry. Tests compare the
// background because that is what the prompt surface is: a missing background is
// visible as a hole in the prompt block.
type sgrState struct {
	fg string
	bg string
}

func (s sgrState) apply(params []string) sgrState {
	for i := 0; i < len(params); i++ {
		switch params[i] {
		case "", "0":
			s = sgrState{}
		case "39":
			s.fg = ""
		case "49":
			s.bg = ""
		case "38", "48":
			start := i
			if i+1 < len(params) && params[i+1] == "5" && i+2 < len(params) {
				i += 2
			} else if i+1 < len(params) && params[i+1] == "2" && i+4 < len(params) {
				i += 4
			} else {
				continue
			}
			code := strings.Join(params[start:i+1], ";")
			if params[start] == "38" {
				s.fg = code
			} else {
				s.bg = code
			}
		}
	}
	return s
}

// sgrRuns splits one rendered row into visible runs and the SGR state each run
// is painted with.
type sgrRun struct {
	Text  string
	State sgrState
}

func sgrRuns(row string) []sgrRun {
	var out []sgrRun
	var pending string
	state := sgrState{}
	flush := func() {
		if pending != "" {
			out = append(out, sgrRun{Text: pending, State: state})
			pending = ""
		}
	}
	for i := 0; i < len(row); {
		if row[i] == 0x1b && i+1 < len(row) && row[i+1] == '[' {
			end := i + 2
			for end < len(row) && row[end] != 'm' {
				end++
			}
			if end >= len(row) {
				pending += row[i:]
				break
			}
			flush()
			state = state.apply(strings.Split(row[i+2:end], ";"))
			i = end + 1
			continue
		}
		pending += string(row[i])
		i++
	}
	flush()
	return out
}

func promptBackground(t *testing.T) string {
	t.Helper()
	state := sgrState{}
	base := tui.HistoryPromptSGR(tui.DetectStdoutColorLevel())
	state = state.apply(strings.Split(strings.TrimSuffix(strings.TrimPrefix(base, "\x1b["), "m"), ";"))
	return state.bg
}

// Mirrors Rust #59983 (`messages.rs` line.style(...).resolve_span_styles()): the
// prompt surface covers the whole row, so a bold/code/link run must not punch a
// hole in the prompt background for the text that follows it.
func TestTypedPromptMarkdownKeepsPromptSurfaceLikeRust(t *testing.T) {
	defer userPromptMarkdownTestColors(t)()
	want := promptBackground(t)
	if want == "" {
		t.Fatalf("test palette produced no prompt background")
	}
	for _, message := range []string{
		"**bold** and plain tail",
		"use `code` then plain",
		"see [guide](https://example.com/docs) after",
		"```go\nfmt.Println(1)\n```",
	} {
		rows := nonBlankPromptRows(NewUserPrompt(message, nil, nil, nil).DisplayLines(80))
		if len(rows) == 0 {
			t.Fatalf("%q rendered no rows", message)
		}
		for _, row := range rows {
			for _, run := range sgrRuns(row) {
				if strings.TrimSpace(run.Text) == "" {
					continue
				}
				if run.State.bg != want {
					t.Fatalf("%q: run %q painted over %q instead of the prompt surface %q\nrow=%q",
						message, run.Text, run.State.bg, want, row)
				}
			}
		}
	}
}

// Mirrors Rust #59983 (`markdown.rs` word_wrap_line_with_source): a prompt row
// must fit the transcript column even when the Markdown renderer keeps a long
// URL or code run intact, while continuation rows keep the two-column indent.
func TestTypedPromptMarkdownWrapsToViewportLikeRust(t *testing.T) {
	defer userPromptMarkdownTestColors(t)()
	cases := []string{
		"**hi** see https://example.test/" + strings.Repeat("a", 64),
		"`" + strings.Repeat("a", 58) + "`",
		strings.Repeat("prose that wraps ", 6),
	}
	for _, width := range []int{40, 80} {
		for _, message := range cases {
			rows := nonBlankPromptRows(NewUserPrompt(message, nil, nil, nil).DisplayLines(width))
			if len(rows) == 0 {
				t.Fatalf("width %d %q rendered no rows", width, message)
			}
			for index, row := range rows {
				if got := tui.DisplayWidth(row); got > width {
					t.Fatalf("width %d %q row %d is %d columns: %q", width, message, index, got, utils.StripANSI(row))
				}
				plain := utils.StripANSI(row)
				if index == 0 {
					if !strings.HasPrefix(plain, "\u203a ") {
						t.Fatalf("width %d %q first row = %q, want the prompt gutter", width, message, plain)
					}
					continue
				}
				if !strings.HasPrefix(plain, "  ") {
					t.Fatalf("width %d %q row %d = %q, want the two-column indent", width, message, index, plain)
				}
			}
		}
	}
}

// nonBlankPromptRows drops the painted blank rows that frame the prompt block.
func nonBlankPromptRows(lines []string) []string {
	rows := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(utils.StripANSI(line)) == "" {
			continue
		}
		rows = append(rows, line)
	}
	return rows
}
