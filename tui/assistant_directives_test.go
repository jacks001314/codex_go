package tui

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseAssistantDirectiveGitReceiptsWithUnquotedAttributes(t *testing.T) {
	raw := `::git-create-pr{cwd="/repo\" branch="feature/report" isDraft=true}`
	directive, ok := ParseAssistantDirective(raw, QuoteEscapingLiteral)
	if !ok {
		t.Fatal("git receipt did not parse")
	}
	if directive.Name != "git-create-pr" || directive.Raw != raw {
		t.Fatalf("directive = %#v", directive)
	}
	// Literal escaping lets the quote close `cwd="/repo\"`, so cwd is `/repo\`
	// and the following `" branch="` becomes the branch key.
	if !reflect.DeepEqual(directive.Attributes, map[string]string{
		"branch":  `feature/report`,
		"cwd":     `/repo\`,
		"isDraft": "true",
	}) {
		t.Fatalf("attributes = %#v", directive.Attributes)
	}
}

func TestParseAssistantDirectiveTripleColonCommentsWithEscapedQuotes(t *testing.T) {
	raw := `:::code-comment{title="Fix class" body="Keep \"px-${size}\" literal." file="C:\Users\me\app.ts" start=10 priority=2}`
	source := raw + " remaining text"
	directive, ok := ParseAssistantDirective(source, QuoteEscapingBackslash)
	if !ok {
		t.Fatal("code comment did not parse")
	}
	want := map[string]string{
		"body":     `Keep "px-${size}" literal.`,
		"file":     `C:\Users\me\app.ts`,
		"priority": "2",
		"start":    "10",
		"title":    "Fix class",
	}
	if directive.Name != "code-comment" || directive.Raw != raw {
		t.Fatalf("directive = %#v", directive)
	}
	if !reflect.DeepEqual(directive.Attributes, want) {
		t.Fatalf("attributes = %#v, want %#v", directive.Attributes, want)
	}
}

func TestParseAssistantDirectivePreservesSingleQuotedValues(t *testing.T) {
	raw := `:artifact{path='Quarterly Report.xlsx' label='team\'s report' sheet='Revenue' range='A1:D8'}`
	directive, ok := ParseAssistantDirective(raw, QuoteEscapingBackslash)
	if !ok {
		t.Fatal("artifact directive did not parse")
	}
	want := map[string]string{
		"label": "team's report",
		"path":  "Quarterly Report.xlsx",
		"range": "A1:D8",
		"sheet": "Revenue",
	}
	if directive.Name != "artifact" || directive.Raw != raw {
		t.Fatalf("directive = %#v", directive)
	}
	if !reflect.DeepEqual(directive.Attributes, want) {
		t.Fatalf("attributes = %#v, want %#v", directive.Attributes, want)
	}
}

func TestParseAssistantDirectiveFollowupLabelsLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		raw   string
		label string
	}{
		{`:codex-followup[Add **revenue charts**]{prompt="Add a chart"}`, `Add **revenue charts**`},
		{`:codex-followup[Inspect [nested [labels]]]{prompt=action}`, `Inspect [nested [labels]]`},
		{":codex-followup[Format `Sheet1`]{prompt=format}", "Format `Sheet1`"},
		{`:codex-followup[Inspect \] and \[ brackets]{prompt=action}`, `Inspect \] and \[ brackets`},
		{":codex-followup[Type a ` character]{prompt=action}", "Type a ` character"},
	} {
		directive, ok := ParseAssistantDirective(testCase.raw, QuoteEscapingBackslash)
		if !ok {
			t.Fatalf("follow-up did not parse: %q", testCase.raw)
		}
		if directive.Name != "codex-followup" || directive.Label != testCase.label || directive.Raw != testCase.raw {
			t.Fatalf("directive = %#v, want label %q", directive, testCase.label)
		}
		if directive.Attributes["prompt"] == "" {
			t.Fatalf("directive attributes = %#v, want prompt", directive.Attributes)
		}
	}
}

func TestParseAssistantDirectiveRejectsLabelsOutsideFollowups(t *testing.T) {
	for _, source := range []string{
		`:git-push[label]{cwd="/repo"}`,
		":codex-followup[unfinished",
		":codex-followup[line\nbreak]{prompt=action}",
	} {
		if directive, ok := ParseAssistantDirective(source, QuoteEscapingBackslash); ok {
			t.Fatalf("parse(%q) = %#v, want rejection", source, directive)
		}
	}
}

func TestParseAssistantDirectiveRejectsAmbiguousOrIncomplete(t *testing.T) {
	for _, source := range []string{
		`::git-push{cwd="/repo" cwd="/other"}`,
		`::git-push{cwd="/repo" branch="feature"`,
		"::::code-comment{title=comment}",
		":artifact{path=/tmp/a path=:artifact{path=/tmp/b}}",
		":artifact{invalid:key=:artifact{path=/tmp/a}}",
		":artifact{path=/tmp/a\nnext=value}",
	} {
		if directive, ok := ParseAssistantDirective(source, QuoteEscapingBackslash); ok {
			t.Fatalf("parse(%q) = %#v, want rejection", source, directive)
		}
	}
}

func TestParseAssistantDirectiveMalformedRetriesExhaustBudget(t *testing.T) {
	source := "codex-file-citation " + strings.Repeat(":a{k=", 16000) + " x bad}"
	remaining := len(source) * 4
	attempts := 0
	for offset := 0; offset < len(source); offset++ {
		if source[offset] != ':' {
			continue
		}
		if remaining == 0 {
			break
		}
		if directive, ok := ParseAssistantDirectiveWithBudget(source[offset:], QuoteEscapingLiteral, &remaining); ok {
			t.Fatalf("malformed directive parsed: %#v", directive)
		}
		attempts++
	}
	if attempts > 8 {
		t.Fatalf("retried %d long malformed values", attempts)
	}
	if remaining != 0 {
		t.Fatalf("remaining budget = %d, want 0", remaining)
	}
}

func TestParseAssistantDirectiveBudgetCountsInspectedValuesOnly(t *testing.T) {
	tail := strings.Repeat("x", 16000)
	raw := ":artifact{path=report.xlsx}"
	source := raw + tail
	remaining := 64
	withBudget, ok := ParseAssistantDirectiveWithBudget(source, QuoteEscapingLiteral, &remaining)
	withoutBudget, okWithout := ParseAssistantDirective(raw, QuoteEscapingLiteral)
	if !ok || !okWithout || !reflect.DeepEqual(withBudget, withoutBudget) {
		t.Fatalf("with budget = %#v ok=%v, without = %#v ok=%v", withBudget, ok, withoutBudget, okWithout)
	}
	for _, truncated := range []string{
		`:artifact{path="` + tail,
		`:artifact{path=` + tail,
	} {
		remaining := 64
		if directive, ok := ParseAssistantDirectiveWithBudget(truncated, QuoteEscapingLiteral, &remaining); ok {
			t.Fatalf("truncated directive parsed: %#v", directive)
		}
		if remaining != 0 {
			t.Fatalf("remaining budget = %d, want 0", remaining)
		}
	}
}
