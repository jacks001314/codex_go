package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/appserver"
	"codex_go/model"
	"codex_go/session"
)

func TestSessionPickerFiltersSortsAndSelects(t *testing.T) {
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	items := []SessionSummary{
		{
			ThreadID:  "thread-old",
			Title:     "Investigate auth flow",
			CWD:       `D:\repo\a`,
			Branch:    "main",
			Provider:  "openai",
			CreatedAt: now.Add(-3 * time.Hour),
			UpdatedAt: now.Add(-2 * time.Hour),
		},
		{
			ThreadID:  "thread-new",
			Title:     "Resume picker redesign",
			CWD:       `D:\repo\a`,
			Branch:    "picker",
			Provider:  "openai",
			CreatedAt: now.Add(-90 * time.Minute),
			UpdatedAt: now.Add(-15 * time.Minute),
		},
		{
			ThreadID:  "thread-other-cwd",
			Title:     "Other workspace",
			CWD:       `D:\repo\b`,
			Provider:  "openai",
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ThreadID: "thread-archived",
			Title:    "Archived",
			CWD:      `D:\repo\a`,
			Archived: true,
		},
	}

	picker := NewSessionPickerState(SessionPickerResume, items, `D:\repo\a`)
	visible := picker.VisibleItems()
	if len(visible) != 2 {
		t.Fatalf("visible len = %d items=%#v", len(visible), visible)
	}
	if visible[0].ThreadID != "thread-new" {
		t.Fatalf("updated sort first = %s", visible[0].ThreadID)
	}

	picker.Query = "auth"
	visible = picker.VisibleItems()
	if len(visible) != 1 || visible[0].ThreadID != "thread-old" {
		t.Fatalf("query filter = %#v", visible)
	}
	selection, ok := picker.Selection()
	if !ok || selection.Kind != SessionSelectionResume || selection.Target.ThreadID != "thread-old" {
		t.Fatalf("selection = %#v ok=%v", selection, ok)
	}

	picker.Query = ""
	picker.ToggleFilter()
	if visible := picker.VisibleItems(); len(visible) != 3 {
		t.Fatalf("all cwd filter visible = %d", len(visible))
	}
}

func TestFormatAgentPickerItemNameMatchesRustRules(t *testing.T) {
	cases := []struct {
		name      string
		nickname  string
		role      string
		primary   bool
		wantLabel string
	}{
		{name: "primary", primary: true, wantLabel: "Main [default]"},
		{name: "nickname and role", nickname: "Scout", role: "review", wantLabel: "Scout [review]"},
		{name: "nickname only", nickname: "Scout", wantLabel: "Scout"},
		{name: "role only", role: "worker", wantLabel: "[worker]"},
		{name: "fallback", wantLabel: "Agent"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatAgentPickerItemName(tc.nickname, tc.role, tc.primary); got != tc.wantLabel {
				t.Fatalf("label = %q, want %q", got, tc.wantLabel)
			}
		})
	}
}

func TestSessionPickerActionFiltersAndSelectionKinds(t *testing.T) {
	items := []SessionSummary{
		{ThreadID: "active", Title: "Active", CWD: `D:\repo\a`},
		{ThreadID: "archived", Title: "Archived", CWD: `D:\repo\a`, Archived: true},
	}

	archive := NewSessionPickerState(SessionPickerArchive, items, `D:\repo\a`)
	if visible := archive.VisibleItems(); len(visible) != 1 || visible[0].ThreadID != "active" {
		t.Fatalf("archive visible = %#v", visible)
	}
	if selection, ok := archive.Selection(); !ok || selection.Kind != SessionSelectionArchive {
		t.Fatalf("archive selection = %#v ok=%v", selection, ok)
	}

	unarchive := NewSessionPickerState(SessionPickerUnarchive, items, `D:\repo\a`)
	if visible := unarchive.VisibleItems(); len(visible) != 1 || visible[0].ThreadID != "archived" {
		t.Fatalf("unarchive visible = %#v", visible)
	}
	if selection, ok := unarchive.Selection(); !ok || selection.Kind != SessionSelectionUnarchive {
		t.Fatalf("unarchive selection = %#v ok=%v", selection, ok)
	}

	deletePicker := NewSessionPickerState(SessionPickerDelete, items, `D:\repo\a`)
	if visible := deletePicker.VisibleItems(); len(visible) != 2 {
		t.Fatalf("delete visible = %#v", visible)
	}
	if selection, ok := deletePicker.Selection(); !ok || selection.Kind != SessionSelectionDelete {
		t.Fatalf("delete selection = %#v ok=%v", selection, ok)
	}
}

func TestSessionPickerRenderDensityExpansionAndPaging(t *testing.T) {
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	items := []SessionSummary{}
	for i := 0; i < SessionPickerLoadNearThreshold+2; i++ {
		items = append(items, SessionSummary{
			ThreadID:  "thread-" + FormatInt(int64(i)),
			Title:     "Session " + FormatInt(int64(i)),
			Path:      "rollout-" + FormatInt(int64(i)) + ".jsonl",
			CWD:       `D:\work\codex\project`,
			UpdatedAt: now.Add(-time.Duration(i) * time.Minute),
		})
	}
	picker := NewSessionPickerState(SessionPickerFork, items, "")
	rows := picker.RenderRows(60, now)
	if !strings.Contains(strings.Join(rows, "\n"), "Session 0") {
		t.Fatalf("comfortable rows = %#v", rows)
	}
	picker.ToggleExpanded("thread-0")
	if rows := strings.Join(picker.RenderRows(80, now), "\n"); !strings.Contains(rows, "Thread: thread-0") {
		t.Fatalf("expanded rows = %s", rows)
	}
	picker.ToggleDensity()
	if rows := picker.RenderRows(40, now); len(rows) != len(items) || !strings.Contains(rows[0], "now") {
		t.Fatalf("dense rows = %#v", rows)
	}
	picker.Select(len(items) - SessionPickerLoadNearThreshold - 1)
	if !picker.NeedsNextPage() {
		t.Fatal("expected next page threshold")
	}
}

func TestLoadSessionSummariesFromStoreFiltersSortsAndLimits(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	cwd := `D:\repo\a`
	records := []*session.Record{
		{
			ID:        "thread-active",
			Title:     "Active",
			CreatedAt: now.Add(-3 * time.Hour),
			UpdatedAt: now.Add(-20 * time.Minute),
			RecencyAt: now.Add(-20 * time.Minute),
			Metadata: session.Metadata{
				CWD:           cwd,
				Source:        "cli",
				ModelProvider: "openai",
				Git:           map[string]string{"branch": "main"},
			},
		},
		{
			ID:        "thread-exec",
			Title:     "Exec",
			CreatedAt: now.Add(-2 * time.Hour),
			UpdatedAt: now.Add(-5 * time.Minute),
			RecencyAt: now.Add(-5 * time.Minute),
			Metadata:  session.Metadata{CWD: cwd, Source: "exec", ModelProvider: "openai"},
		},
		{
			ID:        "thread-archived",
			Title:     "Archived",
			Archived:  true,
			CreatedAt: now.Add(-4 * time.Hour),
			UpdatedAt: now.Add(-30 * time.Minute),
			RecencyAt: now.Add(-30 * time.Minute),
			Metadata:  session.Metadata{CWD: cwd, Source: "vscode", ModelProvider: "openai"},
		},
		{
			ID:        "thread-other-cwd",
			Title:     "Other",
			CreatedAt: now,
			UpdatedAt: now,
			RecencyAt: now,
			Metadata:  session.Metadata{CWD: `D:\repo\b`, Source: "cli", ModelProvider: "openai"},
		},
	}
	for _, record := range records {
		if err := store.Save(record); err != nil {
			t.Fatalf("Save(%s) error = %v", record.ID, err)
		}
	}

	summaries, err := LoadSessionSummariesFromStore(store, SessionSourceOptions{
		IncludeArchived: true,
		CWD:             cwd,
		Limit:           2,
	})
	if err != nil {
		t.Fatalf("LoadSessionSummariesFromStore() error = %v", err)
	}
	if len(summaries) != 2 || summaries[0].ThreadID != "thread-active" || summaries[1].ThreadID != "thread-archived" {
		t.Fatalf("summaries = %#v", summaries)
	}
	if summaries[0].Branch != "main" || summaries[0].Provider != "openai" || !strings.Contains(summaries[0].Path, "thread-active.json") {
		t.Fatalf("active summary = %#v", summaries[0])
	}

	summaries, err = LoadSessionSummariesFromStore(store, SessionSourceOptions{
		IncludeNonInteractive: true,
		CWD:                   cwd,
		Limit:                 1,
	})
	if err != nil {
		t.Fatalf("LoadSessionSummariesFromStore(non-interactive) error = %v", err)
	}
	if len(summaries) != 1 || summaries[0].ThreadID != "thread-exec" {
		t.Fatalf("non-interactive summaries = %#v", summaries)
	}
}

func TestSessionSummariesFromAppServerThreadsAndListParams(t *testing.T) {
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	branch := "picker"
	path := `D:\codex\sessions\thread-remote.json`
	name := "Remote Session"
	recency := now.Unix()
	threads := []appserver.Thread{{
		ID:            "thread-remote",
		Preview:       "preview",
		ModelProvider: "openai",
		CreatedAt:     now.Add(-2 * time.Hour).Unix(),
		UpdatedAt:     now.Add(-90 * time.Minute).Unix(),
		RecencyAt:     &recency,
		Path:          &path,
		CWD:           `D:\repo\a`,
		GitInfo:       &appserver.GitInfo{Branch: &branch},
		Name:          &name,
	}}

	summaries := SessionSummariesFromAppServerThreads(threads, true)
	if len(summaries) != 1 {
		t.Fatalf("summaries len = %d", len(summaries))
	}
	got := summaries[0]
	if got.ThreadID != "thread-remote" || got.Title != "Remote Session" || got.Path != path || got.Branch != branch || !got.Archived || !got.UpdatedAt.Equal(now) {
		t.Fatalf("summary = %#v", got)
	}

	params := AppServerThreadListParamsForSessionPicker(SessionSourceOptions{
		CWD:           `D:\repo\a`,
		Search:        "auth",
		ModelProvider: "openai",
		Limit:         12,
	})
	if params.Limit == nil || *params.Limit != 12 || params.Archived == nil || *params.Archived {
		t.Fatalf("params limit/archive = %#v", params)
	}
	if params.CWD == nil || len(params.CWD.Values) != 1 || params.CWD.Values[0] != `D:\repo\a` {
		t.Fatalf("params cwd = %#v", params.CWD)
	}
	if params.SearchTerm == nil || *params.SearchTerm != "auth" || len(params.ModelProviders) != 1 || params.ModelProviders[0] != "openai" {
		t.Fatalf("params filters = %#v", params)
	}
	if len(params.SourceKinds) != 2 || params.SourceKinds[0] != appserver.ThreadSourceKindCli || params.SourceKinds[1] != appserver.ThreadSourceKindVsCode {
		t.Fatalf("params source kinds = %#v", params.SourceKinds)
	}
}

func TestThemePickerDiscoversPreviewAndCancelRestore(t *testing.T) {
	options := DiscoverThemeOptions(
		[]string{"github_dark", "solarized-light"},
		[]string{`D:\codex\themes\team.tmTheme`, `D:\codex\themes\notes.txt`},
	)
	if len(options) != 3 {
		t.Fatalf("options len = %d options=%#v", len(options), options)
	}

	picker := NewThemePicker(options, "github_dark")
	if picker.PreviewThemeID() != "github_dark" {
		t.Fatalf("initial preview = %q", picker.PreviewThemeID())
	}
	picker.Move(1)
	preview := picker.PreviewThemeID()
	if preview == "github_dark" {
		t.Fatal("expected moved preview theme")
	}
	if got := picker.Cancel(); got != "github_dark" || picker.PreviewThemeID() != "github_dark" {
		t.Fatalf("cancel restore got=%q preview=%q", got, picker.PreviewThemeID())
	}
	picker.Move(1)
	confirmed := picker.Confirm()
	if confirmed == "github_dark" {
		t.Fatal("expected confirmed new theme")
	}

	layout := ComputeThemePreviewLayout(120)
	if !layout.Wide || layout.ListWidth < ThemePreviewListMinWidth || layout.SideWidth < ThemePreviewWideMinWidth {
		t.Fatalf("wide layout = %#v", layout)
	}
	if narrow := ComputeThemePreviewLayout(70); narrow.Wide {
		t.Fatalf("narrow layout = %#v", narrow)
	}
	if rows := ThemePreviewRows(48); len(rows) == 0 || !strings.Contains(strings.Join(rows, "\n"), "summarize") {
		t.Fatalf("preview rows = %#v", rows)
	}
	if rows := NarrowThemePreviewRows(32); len(rows) == 0 || !strings.Contains(strings.Join(rows, "\n"), "greet") {
		t.Fatalf("narrow preview rows = %#v", rows)
	}
	rendered := strings.Join(picker.RenderRows(80), "\n")
	if strings.Contains(rendered, "Github Dark - Built in") || !strings.Contains(rendered, "github_dark") {
		t.Fatalf("theme rows should use Rust-style names:\n%s", rendered)
	}
}

func TestThemePickerDiscoversCustomThemeDirectoryAndSubtitle(t *testing.T) {
	dir := t.TempDir()
	themesDir := filepath.Join(dir, "themes")
	if err := os.MkdirAll(themesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(themesDir, "team-dark.tmTheme"), []byte("placeholder"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(themesDir, "notes.txt"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}

	paths := DiscoverCustomThemePaths(themesDir)
	if len(paths) != 1 || filepath.Base(paths[0]) != "team-dark.tmTheme" {
		t.Fatalf("custom paths = %#v", paths)
	}
	options := DiscoverThemeOptions([]string{"dracula"}, paths)
	if len(options) != 2 {
		t.Fatalf("custom options = %#v", options)
	}
	custom := options[1]
	if custom.ID != "team-dark" || custom.Source != ThemeSourceCustom || ThemePickerDisplayName(custom) != "team-dark (custom)" {
		t.Fatalf("custom option = %#v", custom)
	}

	home, err := os.UserHomeDir()
	if err == nil && strings.TrimSpace(home) != "" {
		subtitle := ThemePickerSubtitle(filepath.Join(home, ".codex", "themes"), 240)
		if !strings.Contains(subtitle, "Custom .tmTheme files") || !strings.Contains(subtitle, filepath.Join("~", ".codex", "themes")) {
			t.Fatalf("subtitle = %q", subtitle)
		}
	}
	if subtitle := ThemePickerSubtitle(themesDir, 20); subtitle != ThemePreviewFallbackSubtitle {
		t.Fatalf("narrow subtitle = %q", subtitle)
	}
}

func TestModelPickerFiltersHiddenAndSelectsCurrent(t *testing.T) {
	options := ModelPickerOptionsFromPresets([]model.ModelPreset{
		{Model: "hidden", Name: "Hidden", Visibility: model.VisibilityHide, Priority: 0},
		{
			Model:                    "listed",
			Name:                     "Listed",
			Description:              "Shown in picker",
			Visibility:               model.VisibilityList,
			IsDefault:                true,
			Priority:                 1,
			DefaultReasoningLevel:    "medium",
			SupportedReasoningLevels: []string{"low", "medium", "high"},
		},
		{Model: "visible", Name: "Visible", Visibility: model.VisibilityVisible, Priority: 2},
	})
	if len(options) != 2 {
		t.Fatalf("options len = %d options=%#v", len(options), options)
	}
	if !options[0].NeedsReasoningPicker() || options[0].DefaultReasoning() != "medium" {
		t.Fatalf("listed reasoning = %#v", options[0])
	}
	picker := NewModelPicker(options, "visible")
	selected, ok := picker.SelectedModel()
	if !ok || selected.ID != "visible" || !selected.IsCurrent {
		t.Fatalf("selection = %#v ok=%v", selected, ok)
	}
	rows := strings.Join(picker.RenderRows(80), "\n")
	for _, want := range []string{"Listed", "Shown in picker", "Visible", "current"} {
		if !strings.Contains(rows, want) {
			t.Fatalf("rows missing %q:\n%s", want, rows)
		}
	}
	if strings.Contains(rows, "Hidden") {
		t.Fatalf("hidden model rendered:\n%s", rows)
	}
}

func TestModelReasoningPickerSelectsCurrentThenDefault(t *testing.T) {
	option := ModelPickerOptionsFromPresets([]model.ModelPreset{
		{
			Model:                    "gpt-reasoning",
			Name:                     "GPT Reasoning",
			Visibility:               model.VisibilityList,
			DefaultReasoningLevel:    "medium",
			SupportedReasoningLevels: []string{"low", "medium", "high"},
		},
	})[0]

	picker := NewModelReasoningPicker(option, "high")
	if got, ok := picker.SelectedEffort(); !ok || got.Effort != "high" {
		t.Fatalf("selected effort = %#v ok=%v, want high", got, ok)
	}
	picker.Move(1)
	if got, ok := picker.SelectedEffort(); !ok || got.Effort != "low" {
		t.Fatalf("wrapped selected effort = %#v ok=%v, want low", got, ok)
	}

	picker = NewModelReasoningPicker(option, "")
	if got, ok := picker.SelectedEffort(); !ok || got.Effort != "medium" {
		t.Fatalf("default selected effort = %#v ok=%v, want medium", got, ok)
	}
	high, ok := picker.EffortByID("high")
	if !ok || high.Label != "High" {
		t.Fatalf("high option = %#v ok=%v", high, ok)
	}
}

func TestPlanReasoningScopePickerAndStatePrompt(t *testing.T) {
	state := NewState(&Options{
		Model:           "gpt-reasoning",
		ReasoningEffort: "medium",
		PlanMode:        true,
	})
	if got := state.EffectiveReasoningEffort(); got != "medium" {
		t.Fatalf("effective reasoning = %q, want medium", got)
	}
	if state.ShouldPromptPlanReasoningScope("gpt-reasoning", "medium") {
		t.Fatal("same effective reasoning without override should not prompt")
	}
	if !state.ShouldPromptPlanReasoningScope("gpt-reasoning", "high") {
		t.Fatal("changed Plan reasoning should prompt")
	}

	state.PlanModeReasoningEffort = "high"
	if got := state.EffectiveReasoningEffort(); got != "high" {
		t.Fatalf("plan effective reasoning = %q, want high", got)
	}
	if !state.ShouldPromptPlanReasoningScope("gpt-reasoning", "high") {
		t.Fatal("selected Plan override that differs from global should prompt")
	}

	picker := NewPlanReasoningScopePicker(ModelPickerOption{ID: "gpt-reasoning"}, "high", "medium")
	if len(picker.Options) != 2 {
		t.Fatalf("scope options = %#v", picker.Options)
	}
	if picker.Options[0].Scope != PlanReasoningScopePlanOnly || !strings.Contains(picker.Options[0].Description, "high reasoning") {
		t.Fatalf("plan only option = %#v", picker.Options[0])
	}
	if picker.Options[1].Scope != PlanReasoningScopeAllModes || !strings.Contains(picker.Options[1].Description, "user-chosen Plan override") {
		t.Fatalf("all modes option = %#v", picker.Options[1])
	}
}

func TestRequestUserInputStateCommitsOptionsAndFreeform(t *testing.T) {
	state, err := NewRequestUserInputState([]RequestUserInputQuestion{
		{
			Header:   "Long header value",
			ID:       "scope",
			Question: "Where should this apply?",
			Options:  []RequestUserInputChoice{{Label: "Plan"}, {Label: "All"}, {Label: "Later"}, {Label: "Extra"}},
		},
		{
			ID:       "notes",
			Question: "Any notes?",
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewRequestUserInputState() error = %v", err)
	}
	question, _ := state.CurrentQuestion()
	if question.Header != "Long header " || len(question.Options) != 3 {
		t.Fatalf("normalized question = %#v", question)
	}
	if done := state.CommitAnswer("All"); done {
		t.Fatal("first answer should advance to second question")
	}
	state.AppendDraft("ship it")
	if done := state.CommitAnswer(state.Draft); !done {
		t.Fatal("second answer should complete")
	}
	answers := state.ResponseAnswers()
	if answers["scope"] != "All" || answers["notes"] != "ship it" {
		t.Fatalf("answers = %#v", answers)
	}
	answerLists := state.ResponseAnswerLists()
	if got := answerLists["scope"]; len(got) != 1 || got[0] != "All" {
		t.Fatalf("scope answer list = %#v", got)
	}
	if got := answerLists["notes"]; len(got) != 1 || got[0] != "ship it" {
		t.Fatalf("notes answer list = %#v", got)
	}
}

func TestRequestUserInputStateCapturesOptionNotesAndUnanswered(t *testing.T) {
	state, err := NewRequestUserInputState([]RequestUserInputQuestion{
		{
			ID:       "scope",
			Question: "Where should this apply?",
			Options:  []RequestUserInputChoice{{Label: "Plan"}, {Label: "All"}},
		},
		{
			ID:       "notes",
			Question: "Any notes?",
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewRequestUserInputState() error = %v", err)
	}
	state.BeginNotes()
	state.AppendDraft("include tests")
	if done := state.CommitOptionAnswer("All", state.Draft); done {
		t.Fatal("first answer should advance")
	}
	if done := state.CommitFreeformAnswer(""); !done {
		t.Fatal("second answer should finish")
	}
	if got := state.UnansweredCount(); got != 1 {
		t.Fatalf("unanswered count = %d, want 1", got)
	}
	answerLists := state.ResponseAnswerLists()
	if got := answerLists["scope"]; len(got) != 2 || got[0] != "All" || got[1] != "user_note: include tests" {
		t.Fatalf("scope answer list = %#v", got)
	}
	if got := answerLists["notes"]; len(got) != 0 {
		t.Fatalf("notes answer list = %#v", got)
	}
}

func TestRequestUserInputStatePreservesOtherAndMasksSecretDraft(t *testing.T) {
	state, err := NewRequestUserInputState([]RequestUserInputQuestion{{
		ID:       "secret",
		Question: "Enter token?",
		IsOther:  true,
		IsSecret: true,
	}}, nil)
	if err != nil {
		t.Fatalf("NewRequestUserInputState() error = %v", err)
	}
	question, _ := state.CurrentQuestion()
	if !question.IsOther || !question.IsSecret {
		t.Fatalf("question flags = %#v", question)
	}
	state.AppendDraft("s3cr3t")
	body := state.RenderBody(80)
	if strings.Contains(body, "s3cr3t") || !strings.Contains(body, "Answer: ******") {
		t.Fatalf("secret body was not masked:\n%s", body)
	}
}
