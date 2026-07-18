package mentionsv2

import (
	"reflect"
	"strings"
	"testing"

	"codex_go/filesearch"
	codextui "codex_go/tui"
)

func TestSearchModeCycleLabelsAndAcceptsMatchRust(t *testing.T) {
	if SearchModeResults.Next() != SearchModeFilesystemOnly || SearchModeResults.Previous() != SearchModeTools {
		t.Fatalf("results cycle mismatch")
	}
	if SearchModeFilesystemOnly.Next() != SearchModeTools || SearchModeTools.Next() != SearchModeResults {
		t.Fatalf("next cycle mismatch")
	}
	if SearchModeTools.Previous() != SearchModeFilesystemOnly || SearchModeFilesystemOnly.Previous() != SearchModeResults {
		t.Fatalf("previous cycle mismatch")
	}
	if SearchModeTools.Label() != "Plugins" || SearchModeFilesystemOnly.Label() != "Filesystem Only" || SearchModeResults.Label() != "All Results" {
		t.Fatalf("labels mismatch")
	}
	if !SearchModeResults.Accepts(MentionTypeFile) || !SearchModeTools.Accepts(MentionTypePlugin) || SearchModeTools.Accepts(MentionTypeFile) || !SearchModeFilesystemOnly.Accepts(MentionTypeDirectory) || SearchModeFilesystemOnly.Accepts(MentionTypeSkill) {
		t.Fatalf("acceptance mismatch")
	}
}

func TestSearchCatalogPluginAndSkillCandidatesMatchRust(t *testing.T) {
	if got := PluginMentionName("mcp-search", "MCP Search"); got != "MCP-Search" {
		t.Fatalf("PluginMentionName display segments = %q", got)
	}
	if got := PluginMentionName("google_calendar", "Google Calendar"); got != "Google_Calendar" {
		t.Fatalf("PluginMentionName underscore = %q", got)
	}
	if got := PluginMentionName("sample", "Sample Plugin"); got != "Sample" {
		t.Fatalf("PluginMentionName fallback = %q", got)
	}
	if got := PluginMentionName("browser-use", "Browser Use"); got != "Browser-Use" {
		t.Fatalf("PluginMentionName title = %q", got)
	}

	plugin := PluginCapabilitySummary{
		ConfigName:      "mcp-search@market",
		DisplayName:     "MCP Search",
		HasSkills:       true,
		MCPServerNames:  []string{"one", "two"},
		AppConnectorIDs: []string{"app"},
	}
	if got := PluginDescription(plugin); got != "Plugin - skills - 2 MCP servers - 1 app" {
		t.Fatalf("PluginDescription = %q", got)
	}
	catalog := BuildSearchCatalog([]SkillMetadata{{
		Name:        "fix-tests",
		DisplayName: "Fix Tests",
		Description: "Repair failing tests",
		Path:        "skills/fix-tests/SKILL.md",
	}}, []PluginCapabilitySummary{plugin})
	if len(catalog) != 2 {
		t.Fatalf("catalog len = %d", len(catalog))
	}
	if skill := catalog[0]; skill.MentionType != MentionTypeSkill || skill.Selection.InsertText != "$fix-tests" || skill.Selection.Path != "skills/fix-tests/SKILL.md" || !reflect.DeepEqual(skill.SearchTerms, []string{"fix-tests", "Fix Tests"}) {
		t.Fatalf("skill candidate = %#v", skill)
	}
	if plug := catalog[1]; plug.MentionType != MentionTypePlugin || plug.Selection.InsertText != "@MCP-Search" || plug.Selection.Path != "plugin://mcp-search@market" {
		t.Fatalf("plugin candidate = %#v", plug)
	}
}

func TestSearchCatalogCandidateEdgeCasesMatchRust(t *testing.T) {
	skill := SkillCandidate(SkillMetadata{Name: "docs:review", Path: "skills/review/SKILL.md"})
	if skill.DisplayName != "review (docs)" || !reflect.DeepEqual(skill.SearchTerms, []string{"docs:review", "review (docs)"}) {
		t.Fatalf("plugin skill candidate = %#v", skill)
	}

	plugin := PluginCandidate(PluginCapabilitySummary{ConfigName: "sample@market"})
	if plugin.DisplayName != "" || plugin.Label != "" {
		t.Fatalf("empty plugin display should stay empty like Rust: %#v", plugin)
	}
	if !reflect.DeepEqual(plugin.SearchTerms, []string{"sample", "sample@market", "", "market"}) {
		t.Fatalf("empty plugin display search terms = %#v", plugin.SearchTerms)
	}
	if plugin.Selection.InsertText != "@Sample" || plugin.Selection.Path != "plugin://sample@market" {
		t.Fatalf("empty plugin display selection = %#v", plugin.Selection)
	}
	rows := FilteredCandidates([]Candidate{plugin}, nil, "", SearchModeTools, false)
	if len(rows) != 1 || rows[0].DisplayName != "" {
		t.Fatalf("filtered candidate normalized display unexpectedly: %#v", rows)
	}

	whitespaceDescription := PluginDescription(PluginCapabilitySummary{ConfigName: "docs", Description: "   "})
	if whitespaceDescription != "   " {
		t.Fatalf("whitespace plugin description = %q, want original whitespace", whitespaceDescription)
	}
}

func TestFilteredCandidatesSortFuzzyTermsAndFileMatchesMatchRust(t *testing.T) {
	candidates := []Candidate{
		{
			DisplayName: "Fix Tests",
			SearchTerms: []string{
				"fix-tests",
				"repair",
			},
			MentionType: MentionTypeSkill,
			Selection:   ToolSelection("$fix-tests", "skills/fix-tests/SKILL.md"),
		},
		{
			DisplayName: "MCP Search",
			SearchTerms: []string{
				"mcp-search",
			},
			MentionType: MentionTypePlugin,
			Selection:   ToolSelection("@MCP-Search", "plugin://mcp-search"),
		},
	}
	fileMatches := []filesearch.FileMatch{
		{Path: "src/a.go", MatchType: filesearch.MatchFile, Score: 1, Indices: []int{4}},
		{Path: "docs", MatchType: filesearch.MatchDirectory, Score: 9, Indices: []int{0}},
	}

	rows := FilteredCandidates(candidates, fileMatches, "", SearchModeResults, true)
	names := rowNames(rows)
	if !reflect.DeepEqual(names, []string{"MCP Search", "Fix Tests", "docs", "src/a.go"}) {
		t.Fatalf("empty filter order = %#v", names)
	}

	rows = FilteredCandidates(candidates, nil, "repair", SearchModeTools, false)
	if len(rows) != 1 || rows[0].DisplayName != "Fix Tests" || len(rows[0].MatchIndices) != 0 {
		t.Fatalf("search-term fallback rows = %#v", rows)
	}
	rows = FilteredCandidates(candidates, nil, "mc", SearchModeTools, false)
	if len(rows) != 1 || rows[0].DisplayName != "MCP Search" || len(rows[0].MatchIndices) == 0 {
		t.Fatalf("direct fuzzy rows = %#v", rows)
	}

	rows = FilteredCandidates(candidates, fileMatches, "go", SearchModeFilesystemOnly, true)
	names = rowNames(rows)
	if !reflect.DeepEqual(names, []string{"docs", "src/a.go"}) {
		t.Fatalf("filesystem rows = %#v", names)
	}
}

func TestPopupFileSearchSelectionModesAndRenderingMatchRustCore(t *testing.T) {
	popup := NewPopup(nil)
	popup.SetQuery("go")
	if !popup.FileSearch.Waiting || popup.FileSearch.EmptyMessage() != "loading..." {
		t.Fatalf("waiting file search = %#v", popup.FileSearch)
	}
	popup.SetFileMatches("stale", []filesearch.FileMatch{{Path: "stale.go", MatchType: filesearch.MatchFile}})
	if len(popup.FileSearch.Matches) != 0 {
		t.Fatalf("stale matches should be ignored: %#v", popup.FileSearch.Matches)
	}
	matches := make([]filesearch.FileMatch, 0, MaxPopupRows+2)
	for idx := 0; idx < MaxPopupRows+2; idx++ {
		matches = append(matches, filesearch.FileMatch{Path: "src/file_" + formatInt(idx) + ".go", MatchType: filesearch.MatchFile, Score: idx})
	}
	popup.SetFileMatches("go", matches)
	if popup.FileSearch.Waiting || len(popup.FileSearch.Matches) != MaxPopupRows || popup.Selected != 0 {
		t.Fatalf("fresh matches state = %#v selected=%d", popup.FileSearch, popup.Selected)
	}
	if selection, ok := popup.SelectedSelection(); !ok || selection.Kind != SelectionFile || selection.Path != "src/file_7.go" {
		t.Fatalf("selected selection = %#v ok=%v", selection, ok)
	}
	popup.MoveDown()
	if popup.Selected != 1 {
		t.Fatalf("selected after down = %d", popup.Selected)
	}
	for i := 0; i < MaxPopupRows; i++ {
		popup.MoveDown()
	}
	if popup.Selected != 1 {
		t.Fatalf("selection should wrap, got %d", popup.Selected)
	}
	popup.NextSearchMode()
	popup.NextSearchMode()
	if popup.SearchMode != SearchModeTools || len(popup.Rows()) != 0 || popup.Selected != -1 {
		t.Fatalf("tools mode should filter files: mode=%s rows=%d selected=%d", popup.SearchMode, len(popup.Rows()), popup.Selected)
	}

	popup = NewPopup([]Candidate{{DisplayName: "MCP Search", MentionType: MentionTypePlugin, Selection: ToolSelection("@MCP-Search", "plugin://mcp-search")}})
	if _, ok := popup.SelectedSelection(); ok || popup.Selected != -1 {
		t.Fatalf("new popup should not preselect before query sync: selected=%d ok=%v", popup.Selected, ok)
	}
	popup.SetQuery("")
	rows := RenderPopup(popup, 48, 5)
	if len(rows) != 3 {
		t.Fatalf("rendered rows len = %d rows=%#v", len(rows), rows)
	}
	if !strings.Contains(rows[0], "\x1b[") || !strings.Contains(rows[0], "> MCP Search") || !strings.Contains(rows[0], "Plugin") {
		t.Fatalf("selected rendered row = %q", rows[0])
	}
	if !strings.Contains(rows[len(rows)-1], "[All Results]") {
		t.Fatalf("footer row = %q", rows[len(rows)-1])
	}
	if got := RenderRows(nil, -1, 0, 20, 4, "no matches"); !reflect.DeepEqual(got, []string{"  no matches"}) {
		t.Fatalf("empty rows = %#v", got)
	}
}

func TestMentionRenderUsesDisplayWidthTruncationMatchRust(t *testing.T) {
	row := SearchResult{
		DisplayName: "中文文件名很长",
		MentionType: MentionTypeSkill,
		Selection:   ToolSelection("$wide", "skills/wide/SKILL.md"),
	}
	line := BuildLine(row, false, 12, primaryTextWidth(row))
	if width := codextui.DisplayWidth(line); width > 12 {
		t.Fatalf("line exceeds display width: %q width=%d", line, width)
	}
	if !strings.Contains(line, "\u2026") || strings.Contains(line, "...") {
		t.Fatalf("line should use Rust ellipsis truncation: %q", line)
	}
}

func TestMentionRenderFileNameSplitMatchesRust(t *testing.T) {
	trailing := SearchResult{
		DisplayName: "src/",
		MentionType: MentionTypeDirectory,
		Selection:   FileSelection("src/"),
	}
	if primaryText(trailing) != "" || secondaryText(trailing) != "src/" {
		t.Fatalf("trailing separator split primary=%q secondary=%q", primaryText(trailing), secondaryText(trailing))
	}

	backslash := SearchResult{
		DisplayName: "src\\main.go",
		Description: "changed",
		MentionType: MentionTypeFile,
		Selection:   FileSelection("src\\main.go"),
	}
	if primaryText(backslash) != "main.go" || secondaryText(backslash) != "src\\  changed" {
		t.Fatalf("backslash split primary=%q secondary=%q", primaryText(backslash), secondaryText(backslash))
	}
}

func TestMentionFooterMatchesRustTextAndWidth(t *testing.T) {
	line := RenderFooter(80, SearchModeResults)
	if !strings.Contains(line, "Enter insert") || !strings.Contains(line, "\u00b7") || !strings.Contains(line, "[All Results]") {
		t.Fatalf("footer line = %q", line)
	}
	if width := codextui.DisplayWidth(line); width > 80 {
		t.Fatalf("footer exceeds width: %q width=%d", line, width)
	}

	narrow := RenderFooter(10, SearchModeResults)
	if width := codextui.DisplayWidth(narrow); width > 10 {
		t.Fatalf("narrow footer exceeds width: %q width=%d", narrow, width)
	}
}

func rowNames(rows []SearchResult) []string {
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = row.DisplayName
	}
	return out
}
