package prompt

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestCharacterNgramSkillSelectorMatchesRustFixtures(t *testing.T) {
	tests := []struct {
		name, query string
		documents   []SkillSelectionDocument
		want        []int
	}{
		{"word forms", "create a presentation", []SkillSelectionDocument{{ID: 1, Name: "presentations", Description: "Create visual decks."}, {ID: 2, Name: "spreadsheets", Description: "Analyze tabular data."}}, []int{1}},
		{"typo", "repair my postgrez database", []SkillSelectionDocument{{ID: 1, Name: "postgresql", Description: "Manage a relational database."}, {ID: 2, Name: "postscript", Description: "Render printable documents."}}, []int{1, 2}},
		{"cjk", "帮我制作演示文稿", []SkillSelectionDocument{{ID: 1, Name: "演示文稿", Description: "创建幻灯片。"}, {ID: 2, Name: "电子表格", Description: "分析表格数据。"}}, []int{1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := SelectSkillsCharacterNgram(test.query, test.documents, 20)
			if !reflect.DeepEqual(got.CandidateIDs, test.want) {
				t.Fatalf("ids = %v, want %v", got.CandidateIDs, test.want)
			}
		})
	}
}

func TestFieldedBM25SkillSelectorMatchesRustFixtures(t *testing.T) {
	documents := []SkillSelectionDocument{{ID: 1, Name: "review-helper", Description: "Review code and prose."}, {ID: 2, Name: "terraform-review", Description: "Review Terraform infrastructure."}, {ID: 3, Name: "document-review", Description: "Review Word documents."}}
	if got := SelectSkillsFieldedBM25("review terraform", documents, 20).CandidateIDs; !reflect.DeepEqual(got, []int{2, 3, 1}) {
		t.Fatalf("rare term ids = %v", got)
	}
	documents = []SkillSelectionDocument{{ID: 1, Name: "slides", Description: "Create presentations."}, {ID: 2, Name: "presentations", Description: "Create and edit slides."}}
	if got := SelectSkillsFieldedBM25("slides", documents, 20).CandidateIDs; !reflect.DeepEqual(got, []int{1, 2}) {
		t.Fatalf("field weight ids = %v", got)
	}
	if got := SelectSkillsFieldedBM25("render a video", []SkillSelectionDocument{{ID: 1, Name: "spreadsheets", Description: "Analyze tabular data."}}, 20).CandidateIDs; len(got) != 0 {
		t.Fatalf("unmatched ids = %v", got)
	}
}

func TestSkillSelectorsReportBoundedInputsLikeRust(t *testing.T) {
	documents := make([]SkillSelectionDocument, skillSelectorMaxCandidates+1)
	for i := range documents {
		documents[i] = SkillSelectionDocument{ID: i, Name: fmt.Sprintf("candidate-%d", i), Description: "match"}
	}
	query := strings.Repeat("match ", skillSelectorMaxQueryBytes)
	for name, selection := range map[string]CheapSkillSelection{"ngram": SelectSkillsCharacterNgram(query, documents, 20), "bm25": SelectSkillsFieldedBM25(query, documents, 20)} {
		if !selection.QueryTruncated || !selection.CandidateSetTruncated || len(selection.CandidateIDs) != 20 {
			t.Fatalf("%s selection = %#v", name, selection)
		}
	}
}

func TestMultiQueryLexicalSkillSelectorMatchesRustFixtures(t *testing.T) {
	documents := []SkillSelectionDocument{
		{ID: 1, Name: "rust-format", Description: "Format Rust source code."},
		{ID: 2, Name: "rust-lint", Description: "Fix Rust source lint errors."},
		{ID: 3, Name: "rust-review", Description: "Review Rust source code."},
		{ID: 4, Name: "ci-fix", Description: "Diagnose failing GitHub Actions checks."},
	}
	selection := SelectSkillsMultiQueryLexical("format and review Rust source code, and then diagnose failing GitHub Actions checks", documents, 4)
	firstThree := selection.CandidateIDs[:min(3, len(selection.CandidateIDs))]
	if !containsSkillID(firstThree, 1) || !containsSkillID(firstThree, 4) {
		t.Fatalf("ids = %v", selection.CandidateIDs)
	}
	single := "create presentations"
	documents = []SkillSelectionDocument{{ID: 1, Name: "presentations", Description: "Create visual decks."}, {ID: 2, Name: "spreadsheets", Description: "Analyze tabular data."}}
	if got, want := SelectSkillsMultiQueryLexical(single, documents, 20), SelectSkillsWeightedLexical(single, documents, 20); !reflect.DeepEqual(got, want) {
		t.Fatalf("single query = %#v, want %#v", got, want)
	}
	if got, want := skillSelectorQueryViews("format code and then fix tests; write a summary"), []string{"format code and then fix tests; write a summary", "format code", "fix tests", "write a summary"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("views = %v, want %v", got, want)
	}
}

func containsSkillID(ids []int, want int) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func TestRoutingCardExactSkillSelectorMatchesRustPriorities(t *testing.T) {
	docs := []SkillSelectionDocument{{ID: 1, Name: "deploy", ShortDescription: "release app", Dependencies: "terraform"}, {ID: 2, Name: "review", Description: "review source"}, {ID: 3, Name: "the", Description: "stop word name"}}
	if got := SelectSkillsRoutingCardLexical("terraform", docs, 10).CandidateIDs; !reflect.DeepEqual(got, []int{1}) {
		t.Fatalf("dependencies=%v", got)
	}
	if got := SelectSkillsRoutingCardLexical("review", docs, 10).CandidateIDs; !reflect.DeepEqual(got, []int{2}) {
		t.Fatalf("name=%v", got)
	}
	if got := SelectSkillsRoutingCardLexical("the", docs, 10).CandidateIDs; !reflect.DeepEqual(got, []int{3}) {
		t.Fatalf("exact stop word name=%v", got)
	}
}

func TestSkillSelectorCharacterRoutingCardUsesInterfaceAndDependencies(t *testing.T) {
	documents := []SkillSelectionDocument{
		{ID: 1, Name: "content-tools", Description: "Prepare visual content.", RoutingMetadata: "Create an animated pet spritesheet."},
		{ID: 2, Name: "team-communication", Description: "Share updates with the team.", Dependencies: "slack Post messages to team channels."},
		{ID: 3, Name: "documents", Description: "Write project updates."},
	}
	if got := SelectSkillsCharacterRoutingCard("animated pet spritesheet", documents, 20).CandidateIDs; len(got) == 0 || got[0] != 1 {
		t.Fatalf("interface routing candidates = %#v, want 1 first", got)
	}
	if got := SelectSkillsCharacterRoutingCard("post this update in Slack", documents, 20).CandidateIDs; len(got) == 0 || got[0] != 2 {
		t.Fatalf("dependency routing candidates = %#v, want 2 first", got)
	}
	if documents[0].ShortDescription != "" || documents[0].RoutingMetadata == "" {
		t.Fatalf("selector mutated documents: %#v", documents)
	}
}
