package bottompane

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"codex_go/appserver"
	"codex_go/tui"
	"codex_go/utils"
)

// Rust parity subset: codex-rs/tui/src/bottom_pane/feedback_view.rs.

const (
	FeedbackBaseCLIBugIssueURL              = "https://github.com/openai/codex/issues/new?template=3-cli.yml"
	FeedbackInternalURL                     = "http://go/codex-feedback-internal"
	FeedbackLogsAttachmentFilename          = "codex-logs.log"
	FeedbackDoctorReportAttachmentFilename  = "codex-doctor-report.json"
	FeedbackDiagnosticsAttachmentFilename   = "codex-connectivity-diagnostics.txt"
	FeedbackWindowsSandboxLogFilename       = "windows-sandbox.log"
	feedbackGutter                          = "\u258c "
	feedbackBullet                          = "\u2022"
	feedbackDefaultPlaceholder              = "(optional) Write a short description to help us further"
	feedbackSafetyCheckPlaceholder          = "(optional) Share what was refused and why it should have been allowed"
	feedbackUploadLogsTitle                 = "Upload logs?"
	feedbackUploadLogsDescription           = "The following files will be sent:"
	feedbackUploadConsentYesDescription     = "Share the current Codex session logs and diagnostics with the team for troubleshooting."
	feedbackIssueThreadIDPrefix             = "  Or mention your thread ID "
	feedbackEmployeeFollowUpThreadURLPrefix = "https://go/codex-feedback/"
	feedbackPolicyURL                       = "https://help.openai.com/en/articles/5722486-how-your-data-is-used-to-improve-model-performance"
	feedbackEmployeeFAQURL                  = "https://go/codex-employee-feedback-faq"
	feedbackNoteHintLine                    = "Press enter to submit or esc to cancel"
	feedbackDisclosureScrollHint            = "pgup/pgdn scroll disclosure"
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
	// Audience selects the data-use disclosure copy (Rust #42798).
	Audience FeedbackAudience
	TextArea TextAreaState
	Complete bool
	// pasteBurst mirrors Rust #45116: rapid unbracketed paste input inserts
	// newlines, and Enter submits only once the paste burst window has lapsed.
	pasteBurst PasteBurst
	// introScroll and introPageHeight mirror Rust #42798: the disclosure
	// scrolls with PageUp/PageDown while the editor and hints stay pinned.
	introScroll     int
	introPageHeight int
}

func NewFeedbackNoteView(category FeedbackChoice, turnID string, includeLogs bool) *FeedbackNoteView {
	return NewFeedbackNoteViewWithAudience(category, turnID, includeLogs, FeedbackAudienceExternal)
}

func NewFeedbackNoteViewWithAudience(category FeedbackChoice, turnID string, includeLogs bool, audience FeedbackAudience) *FeedbackNoteView {
	return &FeedbackNoteView{
		Category:        category,
		TurnID:          turnID,
		IncludeLogs:     includeLogs,
		Audience:        audience,
		TextArea:        NewTextAreaState(""),
		introPageHeight: 1,
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
	v.pasteBurst.ClearAfterExplicitPaste()
	return true
}

func (v *FeedbackNoteView) HandleKey(key string) (FeedbackSubmit, bool) {
	return v.HandleKeyAt(key, time.Now())
}

// HandleKeyAt is the time-aware key entry point the paste-burst suppression
// needs (Rust #45116 `handle_key_event_at`). Callers that do not care about
// burst timing can keep using HandleKey.
func (v *FeedbackNoteView) HandleKeyAt(key string, now time.Time) (FeedbackSubmit, bool) {
	if v == nil {
		return FeedbackSubmit{}, false
	}
	normalized := strings.ToLower(strings.TrimSpace(key))
	switch normalized {
	case "pageup", "pgup":
		v.introScroll = max(v.introScroll-v.introPageHeight, 0)
		return FeedbackSubmit{}, false
	case "pagedown", "pgdown", "pgdn":
		v.introScroll += v.introPageHeight
		return FeedbackSubmit{}, false
	case "esc", "ctrl+c":
		v.Cancel()
		return FeedbackSubmit{}, false
	case "enter":
		if v.pasteBurst.DirectInsertNewlineShouldInsert(now) {
			v.pasteBurst.ExtendWindow(now)
			v.TextArea.InsertString("\n")
			return FeedbackSubmit{}, false
		}
		return v.Submit(), true
	case "shift+enter", "ctrl+enter", "alt+enter":
		v.TextArea.InsertString("\n")
		v.pasteBurst.ClearAfterExplicitPaste()
	case "tab":
		inBurst := v.pasteBurst.DirectInsertNewlineShouldInsert(now)
		v.TextArea.InsertString("\t")
		if inBurst {
			v.pasteBurst.ExtendWindow(now)
		}
	default:
		if strings.HasPrefix(normalized, "paste:") {
			v.Paste(strings.TrimPrefix(key, "paste:"))
			return FeedbackSubmit{}, false
		}
		if isPlainFeedbackCharKey(key) {
			_, pasteLike := v.pasteBurst.OnPlainCharNoHold(now)
			v.TextArea.HandleKey(key)
			if pasteLike {
				v.pasteBurst.ExtendWindow(now)
			}
			return FeedbackSubmit{}, false
		}
		v.TextArea.HandleKey(key)
		v.pasteBurst.ClearAfterExplicitPaste()
	}
	return FeedbackSubmit{}, false
}

// isPlainFeedbackCharKey reports whether key is a single printable character
// (Rust's `!has_ctrl_or_alt(modifiers)` plain-char arm). Named keys such as
// "backspace" or "left" are handled by the text area without burst tracking.
func isPlainFeedbackCharKey(key string) bool {
	if key == "" || utf8.RuneCountInString(key) != 1 {
		return false
	}
	r, _ := utf8.DecodeRuneInString(key)
	return r != utf8.RuneError && !unicode.IsControl(r)
}

// FeedbackDisclosureURL returns the audience-specific data-use policy link the
// note view attaches to its disclosure (Rust #42798).
func FeedbackDisclosureURL(audience FeedbackAudience) string {
	if audience == FeedbackAudienceOpenAIEmployee {
		return feedbackEmployeeFAQURL
	}
	return feedbackPolicyURL
}

func (v *FeedbackNoteView) Rows(width int) []string {
	return v.RowsWithHeight(width, 0)
}

// RowsWithHeight lays the note view out in an optional fixed height. A zero
// height renders every row; a positive height mirrors Rust #42798 by pinning
// the editor and the submit/cancel hints while the disclosure scrolls.
func (v *FeedbackNoteView) RowsWithHeight(width, height int) []string {
	if v == nil {
		return nil
	}
	if width <= 0 {
		width = 1
	}
	inputLines := v.renderedInputLines(width)
	intro := v.introLines(width, false)
	if height <= 0 {
		rows := append([]string{}, intro...)
		rows = append(rows, strings.TrimRight(feedbackGutter, " "))
		for _, line := range inputLines {
			rows = append(rows, strings.TrimRight(feedbackGutter+line, " "))
		}
		return append(rows, "", feedbackNoteHintLine)
	}
	// Four rows leave room for one disclosure row, so drop the title first.
	if height == 4 {
		intro = v.introLines(width, true)
	}
	footer := 1
	if height > 4 {
		footer = 2
	}
	inputHeight := min(min(max(len(inputLines), 1), 8)+1, max(height-footer-1, 2), height)
	introHeight := max(height-inputHeight-footer, 0)
	maxScroll := max(len(intro)-introHeight, 0)
	v.introScroll = min(max(v.introScroll, 0), maxScroll)
	if introHeight > 0 {
		v.introPageHeight = introHeight
	}
	rows := []string{}
	for offset := 0; offset < introHeight && v.introScroll+offset < len(intro); offset++ {
		rows = append(rows, intro[v.introScroll+offset])
	}
	rows = append(rows, strings.TrimRight(feedbackGutter, " "))
	for index := 0; index < inputHeight-1; index++ {
		line := ""
		if index < len(inputLines) {
			line = inputLines[index]
		}
		rows = append(rows, strings.TrimRight(feedbackGutter+line, " "))
	}
	if footer == 2 {
		spacer := ""
		if maxScroll > 0 {
			spacer = feedbackDisclosureScrollHint
		}
		rows = append(rows, spacer)
	}
	return append(rows, feedbackNoteHintLine)
}

// introLines renders the title plus the audience-specific data-use disclosure
// wrapped with the note gutter (Rust #42798 intro_lines).
func (v *FeedbackNoteView) introLines(width int, disclosureOnly bool) []string {
	if width <= 0 {
		width = 1
	}
	lines := []string{}
	if !disclosureOnly {
		title, _ := FeedbackTitleAndPlaceholder(v.Category)
		lines = append(lines, feedbackGutter+title, strings.TrimRight(feedbackGutter, " "))
	}
	logical := []string{
		"Your data may be used to improve our models and products",
		"By submitting feedback, you agree that OpenAI can use your feedback for safety purposes and internal model training, as explained in more detail here. Please avoid sharing tented information under NDA, or privileged, HR-related, or sensitive personal information.",
	}
	if v.Audience != FeedbackAudienceOpenAIEmployee {
		logical = []string{"Your feedback can be used to improve ChatGPT. Learn more."}
	}
	options := tui.WrapOptions{
		Width:            width,
		InitialIndent:    feedbackGutter,
		SubsequentIndent: feedbackGutter,
		BreakWords:       true,
	}
	for _, line := range logical {
		lines = append(lines, tui.AdaptiveWrapLine(line, options)...)
	}
	return lines
}

func (v *FeedbackNoteView) renderedInputLines(width int) []string {
	inputWidth := max(width-len(feedbackGutter), 1)
	if v.TextArea.IsEmpty() {
		_, placeholder := FeedbackTitleAndPlaceholder(v.Category)
		return tui.AdaptiveWrapLine(placeholder, tui.WrapOptions{Width: inputWidth, BreakWords: true})
	}
	return v.TextArea.WrappedLines(inputWidth)
}

func (v *FeedbackNoteView) DesiredHeight(width int) int {
	if v == nil {
		return 0
	}
	if width <= 0 {
		width = 1
	}
	inputHeight := min(max(len(v.renderedInputLines(width)), 1), 8) + 1
	return len(v.introLines(width, false)) + inputHeight + 2
}

func (v *FeedbackNoteView) CursorPosition(width int, height int) (int, int) {
	if v == nil {
		return 0, 0
	}
	inputWidth := max(width-len(feedbackGutter), 1)
	col, row := v.TextArea.CursorPosition(inputWidth, height)
	return col + len(feedbackGutter), row + len(v.introLines(width, false)) + 1
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
			prefix + " You can share this in #codex-feedback:",
			"",
			"  " + issueURL,
			"",
			"  Sentry Feedback ID: " + threadID,
			"  Sentry URL: " + feedbackEmployeeFollowUpThreadURLPrefix + threadID,
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
	return utils.CrossPlatformBase(path)
}
