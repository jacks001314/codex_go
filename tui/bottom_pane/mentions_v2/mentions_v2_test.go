package mentionsv2

import (
	"reflect"
	"strings"
	"testing"

	"codex_go/filesearch"
	codextui "codex_go/tui"
	"codex_go/utils"
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

func TestGeneratedAppPluginMentionNameMatchesRust(t *testing.T) {
	const pluginName = "app-6a6b12e06c5c8191ac5d5252fa5f92c8"
	const fallback = "App-6a6b12e06c5c8191ac5d5252fa5f92c8"

	for _, displayName := range []string{"Postman", "foo-bar_baz", strings.Repeat("a", 64)} {
		if got := PluginMentionName(pluginName, displayName); got != displayName {
			t.Errorf("PluginMentionName(%q, %q) = %q, want display name", pluginName, displayName, got)
		}
	}
	for _, displayName := range []string{"", "Foo Bar", "Café", "PATH", "XDG_CONFIG_HOME", strings.Repeat("a", 65)} {
		if got := PluginMentionName(pluginName, displayName); got != fallback {
			t.Errorf("PluginMentionName(%q, %q) = %q, want %q", pluginName, displayName, got, fallback)
		}
	}
	for _, name := range []string{
		"1vault",
		"app-short",
		"app-6a6b12e06c5c8191ac5d5252fa5f92c8g",
		"app-6A6B12E06C5C8191AC5D5252FA5F92C8",
	} {
		want := TitleCasePluginName(name)
		if got := PluginMentionName(name, "Vault"); got != want {
			t.Errorf("PluginMentionName(%q, %q) = %q, want fallback %q", name, "Vault", got, want)
		}
	}

	candidate := PluginCandidate(PluginCapabilitySummary{
		ConfigName:  pluginName + "@test",
		DisplayName: "Postman",
	})
	if candidate.Selection.InsertText != "@Postman" || candidate.Selection.Path != "plugin://"+pluginName+"@test" {
		t.Fatalf("generated app candidate selection = %#v", candidate.Selection)
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
	if got := popup.CalculateRequiredHeight(48); got != PopupHeight {
		t.Fatalf("required height = %d, want %d", got, PopupHeight)
	}
	rows := RenderPopup(popup, 48, PopupHeight)
	if len(rows) != PopupHeight {
		t.Fatalf("rendered rows len = %d rows=%#v", len(rows), rows)
	}
	plain := utils.StripANSI(strings.Join(rows, "\n"))
	if !strings.Contains(plain, "› MCP Search") || !strings.Contains(plain, "Plugin") {
		t.Fatalf("selected rendered row missing:\n%s", plain)
	}
	if !strings.Contains(plain, "All Results") || !strings.Contains(plain, "Filesystem Only") || !strings.Contains(plain, "Plugins") {
		t.Fatalf("filter tabs missing:\n%s", plain)
	}
	if !strings.Contains(rows[len(rows)-1], "enter/tab insert") {
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
	if got := primaryTextWidth(row); got != 14 {
		t.Fatalf("primary display width = %d, want 14", got)
	}
	line := BuildLine(row, false, 20, primaryTextWidth(row))
	if width := codextui.DisplayWidth(utils.StripANSI(line)); width > 20 {
		t.Fatalf("line exceeds display width: %q width=%d", line, width)
	}
	if !strings.Contains(line, "\u2026") || strings.Contains(line, "...") {
		t.Fatalf("line should use Rust ellipsis truncation: %q", line)
	}
}

func TestMentionRenderStylesNamesTagsAndMatchesLikeRust(t *testing.T) {
	rows := []SearchResult{
		{
			DisplayName:  "Plugin",
			MentionType:  MentionTypePlugin,
			Selection:    ToolSelection("@Plugin", "plugin://plugin"),
			MatchIndices: []int{0, 1},
		},
		{
			DisplayName: "Skill",
			MentionType: MentionTypeSkill,
			Selection:   ToolSelection("$Skill", "skill://skill"),
		},
		{
			DisplayName: "Task",
			MentionType: MentionTypeTask,
			Selection:   ToolSelection("@Task", "thread://task"),
		},
		{
			DisplayName: "src/main.go",
			MentionType: MentionTypeFile,
			Selection:   FileSelection("src/main.go"),
		},
	}
	lines := RenderRows(rows, -1, 0, 80, 4, "no matches")
	if len(lines) != 4 {
		t.Fatalf("lines = %#v", lines)
	}
	if !strings.Contains(lines[0], accentSGR) || !strings.Contains(lines[0], ansiBold) {
		t.Fatalf("plugin name/match style = %q", lines[0])
	}
	if !strings.Contains(lines[1], ansiDim) {
		t.Fatalf("skill style = %q", lines[1])
	}
	if !strings.Contains(lines[2], ansiCyan) || !strings.Contains(lines[3], ansiCyan) {
		t.Fatalf("task/file cyan styles = %q %q", lines[2], lines[3])
	}
}

// TestMentionRenderBoundedPrimaryColumnKeepsFileIdentityLikeRust mirrors
// #46694's long_mentions_leave_room_for_distinguishing_parent_paths: a long
// off-screen name may size the primary column, but build_line caps it at half
// the content width so every file row keeps its distinguishing parent path.
func TestMentionRenderBoundedPrimaryColumnKeepsFileIdentityLikeRust(t *testing.T) {
	rows := []SearchResult{
		{
			DisplayName: "src/界.rs",
			MentionType: MentionTypeFile,
			Selection:   FileSelection("src/界.rs"),
		},
		{
			DisplayName: "tests/界.rs",
			MentionType: MentionTypeFile,
			Selection:   FileSelection("tests/界.rs"),
		},
		{
			DisplayName: "deep/a_very_long_filename_that_exceeds_the_column.rs",
			MentionType: MentionTypeFile,
			Selection:   FileSelection("deep/a_very_long_filename_that_exceeds_the_column.rs"),
		},
	}
	lines := RenderRows(rows, 0, 0, 40, 3, "no matches")
	if len(lines) != 3 {
		t.Fatalf("lines = %#v", lines)
	}
	for _, want := range []struct {
		line string
		text []string
	}{
		{line: lines[0], text: []string{"›", "界.rs", "src/", "File"}},
		{line: lines[1], text: []string{"界.rs", "tests/", "File"}},
		{line: lines[2], text: []string{"…", "deep/", "File"}},
	} {
		plain := utils.StripANSI(want.line)
		if width := codextui.DisplayWidth(plain); width != 40 {
			t.Fatalf("line width = %d, want 40: %q", width, plain)
		}
		for _, text := range want.text {
			if !strings.Contains(plain, text) {
				t.Fatalf("line %q missing %q", plain, text)
			}
		}
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
	// Rust's mentions footer renders the compact key labels (#46680).
	if !strings.Contains(line, "enter/tab insert") || !strings.Contains(line, "\u00b7") || strings.Contains(line, "[All Results]") {
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

func mentionFrameRows() []SearchResult {
	rows := make([]SearchResult, 0, 12)
	for index := 0; index < 10; index++ {
		rows = append(rows, SearchResult{
			DisplayName: "Plugin " + twoDigits(index),
			Description: "Tools and workflows",
			MentionType: MentionTypePlugin,
			Selection:   ToolSelection("plugin-"+formatInt(index), ""),
		})
	}
	for _, name := range []string{"src/界.rs", "src/plain.rs"} {
		rows = append(rows, SearchResult{
			DisplayName: name,
			MentionType: MentionTypeFile,
			Selection:   FileSelection(name),
		})
	}
	return rows
}

func twoDigits(value int) string {
	if value < 10 {
		return "0" + formatInt(value)
	}
	return formatInt(value)
}

func TestMentionPopupFixedFrameMatchesRust(t *testing.T) {
	results := mentionFrameRows()
	rows := renderPopupRows(results, 0, 0, "no matches", SearchModeResults, "", 80, PopupHeight)
	if len(rows) != PopupHeight {
		t.Fatalf("rows = %d, want %d", len(rows), PopupHeight)
	}
	plain := make([]string, len(rows))
	for index, row := range rows {
		plain[index] = utils.StripANSI(row)
	}
	if plain[0] != "  Mentions" || plain[1] != "  Files, directories, plugins, skills and tasks" {
		t.Fatalf("header = %#v", plain[:2])
	}
	if !strings.Contains(plain[3], "All Results") || !strings.Contains(plain[3], "Filesystem Only") || !strings.Contains(plain[3], "Plugins") {
		t.Fatalf("tabs = %q", plain[3])
	}
	if plain[5] != "  Type to search mentions" {
		t.Fatalf("query = %q", plain[5])
	}
	if !strings.Contains(plain[7], "› Plugin 00") {
		t.Fatalf("first result = %q", plain[7])
	}
	if plain[15] != "\u2193" {
		t.Fatalf("below indicator = %q", plain[15])
	}
	if !strings.HasPrefix(plain[16], "  enter/tab insert") {
		t.Fatalf("footer = %q", plain[16])
	}

	narrow := renderPopupRows(results, len(results)-1, 0, "no matches", SearchModeResults, "", 40, PopupHeight)
	plainNarrow := make([]string, len(narrow))
	for index, row := range narrow {
		plainNarrow[index] = utils.StripANSI(row)
	}
	if plainNarrow[6] != "\u2191" {
		t.Fatalf("above indicator = %q", plainNarrow[6])
	}
	if !strings.Contains(plainNarrow[14], "› plain.rs") || !strings.Contains(plainNarrow[14], "src/") {
		t.Fatalf("last result = %q frame=%#v", plainNarrow[14], plainNarrow)
	}
	if !strings.Contains(plainNarrow[16], "enter/tab insert") || !strings.Contains(plainNarrow[16], "\u2026") {
		t.Fatalf("narrow footer = %q", plainNarrow[16])
	}

	short := renderPopupRows(results, len(results)-1, 0, "no matches", SearchModeResults, "", 40, 7)
	if len(short) != 7 {
		t.Fatalf("short rows = %#v", short)
	}
	shortPlain := make([]string, len(short))
	for index, row := range short {
		shortPlain[index] = utils.StripANSI(row)
	}
	if shortPlain[0] != "  Mentions" || shortPlain[2] == "" || shortPlain[3] != "  Type to search mentions" {
		t.Fatalf("short frame = %#v", shortPlain)
	}
	if !strings.Contains(shortPlain[5], "› plain.rs") || !strings.HasPrefix(shortPlain[6], "  enter/tab insert") {
		t.Fatalf("short frame rows = %#v", shortPlain)
	}
}

func TestMentionPopupEmptyFilterClearsResultsAndKeepsFrame(t *testing.T) {
	rows := renderPopupRows(nil, -1, 0, "loading...", SearchModeFilesystemOnly, "new-query", 80, PopupHeight)
	if len(rows) != PopupHeight {
		t.Fatalf("rows = %d, want %d", len(rows), PopupHeight)
	}
	plain := make([]string, len(rows))
	for index, row := range rows {
		plain[index] = utils.StripANSI(row)
	}
	if plain[5] != "  new-query" {
		t.Fatalf("query = %q", plain[5])
	}
	if plain[7] != "  loading..." {
		t.Fatalf("empty message = %q", plain[7])
	}
	for index := 8; index < 16; index++ {
		if plain[index] != "" {
			t.Fatalf("stale result at row %d: %q", index, plain[index])
		}
	}
	if !strings.HasPrefix(plain[16], "  enter/tab insert") {
		t.Fatalf("footer = %q", plain[16])
	}
}

func rowNames(rows []SearchResult) []string {
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = row.DisplayName
	}
	return out
}

// TestSearchCatalogTaskCandidatesMatchRust mirrors the task arm of Rust
// build_search_catalog plus the task ordering/acceptance rules.
func TestSearchCatalogTaskCandidatesMatchRust(t *testing.T) {
	tasks := []codextui.TaskMention{{
		ThreadID: "task-123",
		Title:    "  Fix   the\nparser  ",
		CWD:      `D:\repo`,
		Snippet:  "snippet",
	}}
	candidates := BuildSearchCatalogWithTasks(nil, nil, tasks)
	if len(candidates) != 1 {
		t.Fatalf("candidates = %#v", candidates)
	}
	candidate := candidates[0]
	if candidate.DisplayName != "Fix the parser" || candidate.Label != "Fix the parser" {
		t.Fatalf("display name = %q label = %q", candidate.DisplayName, candidate.Label)
	}
	if candidate.Description != `D:\repo` || candidate.MentionType != MentionTypeTask {
		t.Fatalf("candidate = %#v", candidate)
	}
	wantTerms := []string{"Fix the parser", `D:\repo`, "snippet"}
	if !reflect.DeepEqual(candidate.SearchTerms, wantTerms) {
		t.Fatalf("search terms = %#v, want %#v", candidate.SearchTerms, wantTerms)
	}
	if candidate.Selection.Kind != SelectionTool || candidate.Selection.InsertText != "@Fix the parser" ||
		candidate.Selection.Path != "thread://task-123" {
		t.Fatalf("selection = %#v", candidate.Selection)
	}
	if MentionTypeTask.Label() != "Task" || MentionTypeTask.Tag() != "Task  " || MentionTypeTask.IsFilesystem() {
		t.Fatalf("task mention type metadata = %q/%q/%v", MentionTypeTask.Label(), MentionTypeTask.Tag(), MentionTypeTask.IsFilesystem())
	}
	// Rust builds skills, then plugins, then tasks.
	ordered := BuildSearchCatalogWithTasks(
		[]SkillMetadata{{Name: "imagegen", Path: "skill://imagegen"}},
		[]PluginCapabilitySummary{{ConfigName: "sample@market", DisplayName: "Sample"}},
		tasks,
	)
	if len(ordered) != 3 || ordered[0].MentionType != MentionTypeSkill ||
		ordered[1].MentionType != MentionTypePlugin || ordered[2].MentionType != MentionTypeTask {
		t.Fatalf("catalog order = %#v", ordered)
	}
	// A long title is clamped to MAX_TASK_TITLE_CHARS runes.
	long := strings.Repeat("x", codextui.MaxTaskTitleChars+5)
	clamped := BuildSearchCatalogWithTasks(nil, nil, []codextui.TaskMention{{ThreadID: "t", Title: long}})[0]
	if len([]rune(clamped.DisplayName)) != codextui.MaxTaskTitleChars {
		t.Fatalf("clamped title length = %d", len([]rune(clamped.DisplayName)))
	}
	// Task rows stay in provider order and are visible only in Results mode.
	rows := FilteredCandidates(candidates, nil, "", SearchModeResults, false)
	if len(rows) != 1 || rows[0].MentionType != MentionTypeTask {
		t.Fatalf("results rows = %#v", rows)
	}
	if FilteredCandidates(candidates, nil, "", SearchModeTools, false) != nil && len(FilteredCandidates(candidates, nil, "", SearchModeTools, false)) != 0 {
		t.Fatalf("tools mode should not accept task rows")
	}
	unsorted := []SearchResult{
		{DisplayName: "b", MentionType: MentionTypeTask},
		{DisplayName: "a", MentionType: MentionTypeTask},
	}
	sortRows(unsorted, "")
	if unsorted[0].DisplayName != "b" || unsorted[1].DisplayName != "a" {
		t.Fatalf("task rows reordered: %#v", unsorted)
	}
}
