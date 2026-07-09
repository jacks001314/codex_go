package bottompane

import (
	"path/filepath"
	"strings"

	"codex_go/internal/appserver"
	"codex_go/internal/tui"
)

// Rust parity subset: codex-rs/tui/src/bottom_pane/feedback_view.rs.

const (
	FeedbackBaseCLIBugIssueURL              = "https://github.com/openai/codex/issues/new?template=3-cli.yml"
	FeedbackInternalURL                     = "http://go/codex-feedback-internal"
	FeedbackLogsAttachmentFilename          = "codex-logs.log"
	FeedbackDoctorReportAttachmentFilename  = "codex-doctor-report.json"
	FeedbackDiagnosticsAttachmentFilename   = "codex-connectivity-diagnostics.txt"
	FeedbackWindowsSandboxLogFilename       = "windows-sandbox.log"
	feedbackGutter                          = "\u2503 "
	feedbackBullet                          = "\u2022"
	feedbackDefaultPlaceholder              = "(optional) Write a short description to help us further"
	feedbackSafetyCheckPlaceholder          = "(optional) Share what was refused and why it should have been allowed"
	feedbackUploadLogsTitle                 = "Upload logs?"
	feedbackUploadLogsDescription           = "The following files will be sent:"
	feedbackUploadConsentYesDescription     = "Share the current Codex session logs and diagnostics with the team for troubleshooting."
	feedbackIssueThreadIDPrefix             = "  Or mention your thread ID "
	feedbackEmployeeFollowUpThreadURLPrefix = "https://go/codex-feedback/"
)

type FeedbackChoice string

const (
	FeedbackGoodResult  FeedbackChoice = "good_result"
	FeedbackBadResult   FeedbackChoice = "bad_result"
	FeedbackBug         FeedbackChoice = "bug"
	FeedbackSafetyCheck FeedbackChoice = "safety_check"
	FeedbackOther       FeedbackChoice = "other"
)

type FeedbackAudience string

const (
	FeedbackAudienceOpenAIEmployee FeedbackAudience = "openai_employee"
	FeedbackAudienceExternal       FeedbackAudience = "external"
)

type FeedbackSubmit struct {
	Category    FeedbackChoice
	Reason      *string
	TurnID      *string
	IncludeLogs bool
}

type FeedbackNoteView struct {
	Category    FeedbackChoice
	TurnID      string
	IncludeLogs bool
	TextArea    TextAreaState
	Complete    bool
}

func NewFeedbackNoteView(category FeedbackChoice, turnID string, includeLogs bool) *FeedbackNoteView {
	return &FeedbackNoteView{
		Category:    category,
		TurnID:      turnID,
		IncludeLogs: includeLogs,
		TextArea:    NewTextAreaState(""),
	}
}

func (v *FeedbackNoteView) Submit() FeedbackSubmit {
	if v == nil {
		return FeedbackSubmit{}
	}
	note := strings.TrimSpace(v.TextArea.Text)
	var reason *string
	if note != "" {
		reason = &note
	}
	var turnID *string
	if strings.TrimSpace(v.TurnID) != "" {
		turnID = &v.TurnID
	}
	v.Complete = true
	return FeedbackSubmit{
		Category:    v.Category,
		Reason:      reason,
		TurnID:      turnID,
		IncludeLogs: v.IncludeLogs,
	}
}

func (v *FeedbackNoteView) Cancel() {
	if v != nil {
		v.Complete = true
	}
}

func (v *FeedbackNoteView) Paste(text string) bool {
	if v == nil || text == "" {
		return false
	}
	v.TextArea.InsertString(text)
	return true
}

func (v *FeedbackNoteView) HandleKey(key string) (FeedbackSubmit, bool) {
	if v == nil {
		return FeedbackSubmit{}, false
	}
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "esc", "ctrl+c":
		v.Cancel()
		return FeedbackSubmit{}, false
	case "enter":
		return v.Submit(), true
	case "shift+enter", "ctrl+enter", "alt+enter":
		v.TextArea.InsertString("\n")
	default:
		v.TextArea.HandleKey(key)
	}
	return FeedbackSubmit{}, false
}

func (v *FeedbackNoteView) Rows(width int) []string {
	if v == nil {
		return nil
	}
	if width <= 0 {
		width = 1
	}
	title, placeholder := FeedbackTitleAndPlaceholder(v.Category)
	rows := []string{feedbackGutter + title}
	inputWidth := max(width-len(feedbackGutter), 1)
	inputLines := v.TextArea.WrappedLines(inputWidth)
	if v.TextArea.IsEmpty() {
		inputLines = tui.AdaptiveWrapLine(placeholder, tui.WrapOptions{Width: inputWidth, BreakWords: true})
	}
	for _, line := range inputLines {
		rows = append(rows, feedbackGutter+line)
	}
	rows = append(rows, "", StandardPopupHintLine)
	return rows
}

func (v *FeedbackNoteView) DesiredHeight(width int) int {
	return len(v.Rows(width))
}

func (v *FeedbackNoteView) CursorPosition(width int, height int) (int, int) {
	if v == nil {
		return 0, 0
	}
	inputWidth := max(width-len(feedbackGutter), 1)
	col, row := v.TextArea.CursorPosition(inputWidth, height)
	return col + len(feedbackGutter), row + 1
}

func FeedbackTitleAndPlaceholder(category FeedbackChoice) (string, string) {
	switch category {
	case FeedbackBadResult:
		return "Tell us more (bad result)", feedbackDefaultPlaceholder
	case FeedbackGoodResult:
		return "Tell us more (good result)", feedbackDefaultPlaceholder
	case FeedbackBug:
		return "Tell us more (bug)", feedbackDefaultPlaceholder
	case FeedbackSafetyCheck:
		return "Tell us more (safety check)", feedbackSafetyCheckPlaceholder
	default:
		return "Tell us more (other)", feedbackDefaultPlaceholder
	}
}

func FeedbackClassification(category FeedbackChoice) string {
	switch category {
	case FeedbackBadResult:
		return "bad_result"
	case FeedbackGoodResult:
		return "good_result"
	case FeedbackBug:
		return "bug"
	case FeedbackSafetyCheck:
		return "safety_check"
	default:
		return "other"
	}
}

func ShouldShowFeedbackConnectivityDetails(category FeedbackChoice, diagnostics *appserver.FeedbackDiagnostics) bool {
	return category != FeedbackGoodResult && diagnostics != nil && !diagnostics.IsEmpty()
}

type FeedbackSelectionParams struct {
	Title       string
	Subtitle    string
	FooterHint  string
	HeaderLines []string
	Items       []FeedbackSelectionItem
}

type FeedbackSelectionItem struct {
	Name        string
	Description string
	Category    FeedbackChoice
	IncludeLogs *bool
	Dismiss     bool
}

func FeedbackCategorySelectionParams() FeedbackSelectionParams {
	return FeedbackSelectionParams{
		Title: "How was this?",
		Items: []FeedbackSelectionItem{
			{Name: "bug", Description: "Crash, error message, hang, or broken UI/behavior.", Category: FeedbackBug, Dismiss: true},
			{Name: "bad result", Description: "Output was off-target, incorrect, incomplete, or unhelpful.", Category: FeedbackBadResult, Dismiss: true},
			{Name: "good result", Description: "Helpful, correct, high-quality, or delightful result worth celebrating.", Category: FeedbackGoodResult, Dismiss: true},
			{Name: "safety check", Description: "Benign usage blocked due to safety checks or refusals.", Category: FeedbackSafetyCheck, Dismiss: true},
			{Name: "other", Description: "Slowness, feature suggestion, UX feedback, or anything else.", Category: FeedbackOther, Dismiss: true},
		},
	}
}

func FeedbackDisabledParams() FeedbackSelectionParams {
	return FeedbackSelectionParams{
		Title:      "Sending feedback is disabled",
		Subtitle:   "This action is disabled by configuration.",
		FooterHint: StandardPopupHintLine,
		Items: []FeedbackSelectionItem{
			{Name: "Close", Dismiss: true},
		},
	}
}

type FeedbackUploadConsentOptions struct {
	Category                  FeedbackChoice
	RolloutPath               string
	AutoReviewRolloutFilename string
	IncludeWindowsSandboxLog  bool
	Diagnostics               *appserver.FeedbackDiagnostics
}

func FeedbackUploadConsentParams(options FeedbackUploadConsentOptions) FeedbackSelectionParams {
	include := true
	exclude := false
	header := FeedbackUploadConsentHeaderRows(options)
	return FeedbackSelectionParams{
		FooterHint:  StandardPopupHintLine,
		HeaderLines: header,
		Items: []FeedbackSelectionItem{
			{
				Name:        "Yes",
				Description: feedbackUploadConsentYesDescription,
				Category:    options.Category,
				IncludeLogs: &include,
				Dismiss:     true,
			},
			{
				Name:        "No",
				Category:    options.Category,
				IncludeLogs: &exclude,
				Dismiss:     true,
			},
		},
	}
}

func FeedbackUploadConsentHeaderRows(options FeedbackUploadConsentOptions) []string {
	rows := []string{
		feedbackUploadLogsTitle,
		"",
		feedbackUploadLogsDescription,
		"  " + feedbackBullet + " " + FeedbackLogsAttachmentFilename,
		"  " + feedbackBullet + " " + FeedbackDoctorReportAttachmentFilename,
	}
	if options.IncludeWindowsSandboxLog {
		rows = append(rows, "  "+feedbackBullet+" "+FeedbackWindowsSandboxLogFilename)
	}
	if name := baseName(options.RolloutPath); name != "" {
		rows = append(rows, "  "+feedbackBullet+" "+name)
	}
	if options.AutoReviewRolloutFilename != "" {
		rows = append(rows, "  "+feedbackBullet+" "+options.AutoReviewRolloutFilename)
	}
	if options.Diagnostics != nil && !options.Diagnostics.IsEmpty() {
		rows = append(rows, "  "+feedbackBullet+" "+FeedbackDiagnosticsAttachmentFilename)
	}
	if ShouldShowFeedbackConnectivityDetails(options.Category, options.Diagnostics) {
		rows = append(rows, "", "Connectivity diagnostics")
		for _, diagnostic := range options.Diagnostics.Items() {
			rows = append(rows, "  - "+diagnostic.Headline)
			for _, detail := range diagnostic.Details {
				rows = append(rows, "    - "+detail)
			}
		}
	}
	return rows
}

func FeedbackSuccessRows(category FeedbackChoice, includeLogs bool, threadID string, audience FeedbackAudience) []string {
	prefix := feedbackBullet + " Feedback uploaded."
	if !includeLogs {
		prefix = feedbackBullet + " Feedback recorded (no logs)."
	}
	issueURL := FeedbackIssueURLForCategory(category, threadID, audience)
	switch {
	case issueURL != "" && audience == FeedbackAudienceOpenAIEmployee:
		return []string{
			prefix + " Please report this in #codex-feedback:",
			"",
			"  " + issueURL,
			"",
			"  Share this and add some info about your problem:",
			"    " + feedbackEmployeeFollowUpThreadURLPrefix + threadID,
		}
	case issueURL != "":
		return []string{
			prefix + " Please open an issue using the following URL:",
			"",
			"  " + issueURL,
			"",
			feedbackIssueThreadIDPrefix + threadID + " in an existing issue.",
		}
	default:
		return []string{
			prefix + " Thanks for the feedback!",
			"",
			"  Thread ID: " + threadID,
		}
	}
}

func FeedbackIssueURLForCategory(category FeedbackChoice, threadID string, audience FeedbackAudience) string {
	switch category {
	case FeedbackBug, FeedbackBadResult, FeedbackSafetyCheck, FeedbackOther:
		if audience == FeedbackAudienceOpenAIEmployee {
			return FeedbackInternalURL
		}
		return FeedbackBaseCLIBugIssueURL + "&steps=Uploaded%20thread:%20" + threadID
	default:
		return ""
	}
}

func baseName(path string) string {
	if path == "" {
		return ""
	}
	name := filepath.Base(path)
	if name == "." || name == string(filepath.Separator) {
		return ""
	}
	return name
}
