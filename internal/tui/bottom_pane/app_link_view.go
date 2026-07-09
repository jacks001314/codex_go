package bottompane

import (
	"net/url"
	"strings"

	"codex_go/internal/tui"
)

// Rust parity: codex-rs/tui/src/bottom_pane/app_link_view.rs.

const (
	AppLinkCodexAppsServerName = "codex_apps"

	appLinkCodexAppsMetaKey                    = "_codex_apps"
	appLinkConnectorAuthFailureMetaKey         = "connector_auth_failure"
	appLinkConnectorAuthFailureIsAuthFailure   = "is_auth_failure"
	appLinkConnectorAuthFailureConnectorID     = "connector_id"
	appLinkConnectorAuthFailureConnectorName   = "connector_name"
	appLinkDefaultFooterHint                   = "Use tab / up down to move, enter to select, esc to close"
	appLinkNewlyInstalledAppsMayTakeTime       = "Newly installed apps can take a few minutes to appear in /apps."
	appLinkAfterInstallUseDollarToInsertPrompt = "After installed, use $ to insert this app into the prompt."
)

type AppLinkScreen string

const (
	AppLinkScreenLink                AppLinkScreen = "link"
	AppLinkScreenInstallConfirmation AppLinkScreen = "install_confirmation"
)

type AppLinkSuggestionType string

const (
	AppLinkSuggestionInstall        AppLinkSuggestionType = "install"
	AppLinkSuggestionEnable         AppLinkSuggestionType = "enable"
	AppLinkSuggestionAuth           AppLinkSuggestionType = "auth"
	AppLinkSuggestionExternalAction AppLinkSuggestionType = "external_action"
)

type AppLinkElicitationTarget struct {
	ThreadID   string
	ServerName string
	RequestID  string
}

type AppLinkViewParams struct {
	AppID              string
	Title              string
	Description        *string
	Instructions       string
	URL                string
	IsInstalled        bool
	IsEnabled          bool
	SuggestReason      *string
	SuggestionType     *AppLinkSuggestionType
	ElicitationTarget  *AppLinkElicitationTarget
	LegacyAppNameAlias string
}

type AppLinkURLRequest struct {
	Meta          map[string]any
	Message       string
	URL           string
	ElicitationID string
}

type AppLinkEventKind string

const (
	AppLinkEventOpenURL            AppLinkEventKind = "open_url"
	AppLinkEventRefreshConnectors  AppLinkEventKind = "refresh_connectors"
	AppLinkEventSetAppEnabled      AppLinkEventKind = "set_app_enabled"
	AppLinkEventResolveElicitation AppLinkEventKind = "resolve_elicitation"
)

type AppLinkElicitationDecision string

const (
	AppLinkElicitationAccept  AppLinkElicitationDecision = "accept"
	AppLinkElicitationDecline AppLinkElicitationDecision = "decline"
)

type AppLinkEvent struct {
	Kind         AppLinkEventKind
	URL          string
	AppID        string
	Enabled      bool
	ForceRefetch bool
	ThreadID     string
	ServerName   string
	RequestID    string
	Decision     AppLinkElicitationDecision
}

type AppLinkView struct {
	AppName string
	URL     string

	AppID             string
	Title             string
	Description       *string
	Instructions      string
	IsInstalled       bool
	IsEnabled         bool
	SuggestReason     *string
	SuggestionType    *AppLinkSuggestionType
	ElicitationTarget *AppLinkElicitationTarget

	Screen         AppLinkScreen
	SelectedAction int

	complete bool
	events   []AppLinkEvent
}

func NewAppLinkView(params AppLinkViewParams) *AppLinkView {
	title := firstNonEmpty(params.Title, params.LegacyAppNameAlias, params.AppID)
	view := &AppLinkView{
		AppName:           title,
		URL:               params.URL,
		AppID:             params.AppID,
		Title:             title,
		Description:       cloneStringPtr(params.Description),
		Instructions:      params.Instructions,
		IsInstalled:       params.IsInstalled,
		IsEnabled:         params.IsEnabled,
		SuggestReason:     cloneStringPtr(params.SuggestReason),
		SuggestionType:    cloneSuggestionPtr(params.SuggestionType),
		ElicitationTarget: cloneAppLinkTarget(params.ElicitationTarget),
		Screen:            AppLinkScreenLink,
	}
	if view.AppID == "" {
		view.AppID = params.AppID
	}
	return view
}

func AppLinkViewParamsFromURLRequest(threadID string, serverName string, requestID string, request AppLinkURLRequest) (AppLinkViewParams, bool) {
	if serverName == AppLinkCodexAppsServerName {
		parsed, ok := validateAppLinkExternalURL(request.URL, true)
		if !ok {
			return AppLinkViewParams{}, false
		}
		return appLinkParamsFromCodexAppsAuthURLParts(threadID, serverName, requestID, request.Meta, request.Message, parsed.String(), request.ElicitationID)
	}
	parsed, ok := validateAppLinkExternalURL(request.URL, false)
	if !ok {
		return AppLinkViewParams{}, false
	}
	return appLinkParamsFromGenericURLParts(threadID, serverName, requestID, request.Message, parsed.String(), request.ElicitationID), true
}

func (v AppLinkView) Ready() bool {
	return v.AppName != "" && v.URL != ""
}

func (v *AppLinkView) ActionLabels() []string {
	if v == nil {
		return nil
	}
	if v.isAuthSuggestion() {
		if v.Screen == AppLinkScreenInstallConfirmation {
			return []string{"I already signed in", "Back"}
		}
		return []string{"Open sign-in URL", "Back"}
	}
	if v.isExternalActionSuggestion() {
		if v.Screen == AppLinkScreenInstallConfirmation {
			return []string{"I finished", "Back"}
		}
		return []string{"Open link", "Back"}
	}
	if v.Screen == AppLinkScreenInstallConfirmation {
		return []string{"I already Installed it", "Back"}
	}
	if v.IsInstalled {
		toggleLabel := "Enable app"
		if v.IsEnabled {
			toggleLabel = "Disable app"
		}
		return []string{"Manage on ChatGPT", toggleLabel, "Back"}
	}
	return []string{"Install on ChatGPT", "Back"}
}

func (v *AppLinkView) MoveSelectionPrev() {
	if v == nil || v.SelectedAction <= 0 {
		return
	}
	v.SelectedAction--
}

func (v *AppLinkView) MoveSelectionNext() {
	if v == nil {
		return
	}
	labels := v.ActionLabels()
	if len(labels) == 0 {
		return
	}
	if v.SelectedAction < len(labels)-1 {
		v.SelectedAction++
	}
}

func (v *AppLinkView) HandleKey(key string) {
	if v == nil || v.complete {
		return
	}
	switch normalizeAppLinkKey(key) {
	case "esc", "escape", "ctrl-c":
		v.Cancel()
	case "up", "backtab", "k", "left", "ctrl-h":
		v.MoveSelectionPrev()
	case "down", "tab", "j", "right", "ctrl-l":
		v.MoveSelectionNext()
	case "enter":
		v.ActivateSelectedAction()
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		index := int([]rune(normalizeAppLinkKey(key))[0] - '1')
		if index >= 0 && index < len(v.ActionLabels()) {
			v.SelectedAction = index
			v.ActivateSelectedAction()
		}
	}
}

func (v *AppLinkView) ActivateSelectedAction() {
	if v == nil {
		return
	}
	if v.isToolSuggestion() {
		switch v.suggestion() {
		case AppLinkSuggestionEnable:
			if v.Screen == AppLinkScreenLink {
				if v.SelectedAction == 0 {
					v.openExternalURL()
				} else if v.SelectedAction == 1 && v.IsInstalled {
					v.toggleEnabled()
				} else {
					v.declineToolSuggestion()
				}
			} else if v.SelectedAction == 0 {
				v.completeExternalFlowAndClose()
			} else {
				v.declineToolSuggestion()
			}
		case AppLinkSuggestionAuth, AppLinkSuggestionExternalAction, AppLinkSuggestionInstall:
			if v.Screen == AppLinkScreenLink {
				if v.SelectedAction == 0 {
					v.openExternalURL()
				} else {
					v.declineToolSuggestion()
				}
			} else if v.SelectedAction == 0 {
				v.completeExternalFlowAndClose()
			} else {
				v.declineToolSuggestion()
			}
		default:
			if v.SelectedAction == 0 {
				v.openExternalURL()
			} else {
				v.declineToolSuggestion()
			}
		}
		return
	}
	if v.Screen == AppLinkScreenLink {
		switch {
		case v.SelectedAction == 0:
			v.openExternalURL()
		case v.SelectedAction == 1 && v.IsInstalled:
			v.toggleEnabled()
		default:
			v.complete = true
		}
		return
	}
	if v.SelectedAction == 0 {
		v.completeExternalFlowAndClose()
	} else {
		v.backToLinkScreen()
	}
}

func (v *AppLinkView) Cancel() {
	if v == nil {
		return
	}
	if v.isToolSuggestion() {
		v.resolveElicitation(AppLinkElicitationDecline)
	}
	v.complete = true
}

func (v *AppLinkView) IsComplete() bool {
	return v != nil && v.complete
}

func (v *AppLinkView) TerminalTitleRequiresAction() bool {
	return v != nil && v.isToolSuggestion()
}

func (v *AppLinkView) DismissAppServerRequest(serverName string, requestID string) bool {
	if v == nil || v.ElicitationTarget == nil {
		return false
	}
	if v.ElicitationTarget.ServerName != serverName || v.ElicitationTarget.RequestID != requestID {
		return false
	}
	v.complete = true
	return true
}

func (v *AppLinkView) Events() []AppLinkEvent {
	if v == nil {
		return nil
	}
	return append([]AppLinkEvent(nil), v.events...)
}

func (v *AppLinkView) Rows(width int) []string {
	if v == nil {
		return nil
	}
	contentWidth := width
	if contentWidth <= 0 {
		contentWidth = 72
	}
	rows := v.contentLines(contentWidth)
	rows = append(rows, "")
	for index, label := range v.ActionLabels() {
		selected := index == v.SelectedAction
		row := tui.NumberedSelectionPrefix(index, selected) + label
		if selected {
			row = tui.RenderSelectedRow(row)
		}
		rows = append(rows, row)
	}
	rows = append(rows, appLinkDefaultFooterHint)
	return trimTrailingBlankRows(rows)
}

func (v *AppLinkView) contentLines(width int) []string {
	if v.Screen == AppLinkScreenInstallConfirmation {
		return v.installConfirmationLines(width)
	}
	return v.linkContentLines(width)
}

func (v *AppLinkView) linkContentLines(width int) []string {
	lines := []string{firstNonEmpty(v.Title, v.AppName, v.AppID)}
	if description := trimmedPtr(v.Description); description != "" {
		lines = appendWrappedAppLinkLines(lines, description, width)
	}
	lines = append(lines, "")
	if reason := trimmedPtr(v.SuggestReason); reason != "" {
		lines = appendWrappedAppLinkLines(lines, reason, width)
		lines = append(lines, "")
	}
	isBrowserAction := v.isBrowserActionSuggestion()
	if v.IsInstalled && !isBrowserAction {
		lines = appendWrappedAppLinkLines(lines, "Use $ to insert this app into the prompt.", width)
		lines = append(lines, "")
	}
	if isBrowserAction {
		lines = append(lines, "URL")
		lines = appendWrappedAppLinkLines(lines, v.URL, width)
		lines = append(lines, "")
	}
	instructions := strings.TrimSpace(v.Instructions)
	if instructions != "" {
		lines = appendWrappedAppLinkLines(lines, instructions, width)
		if !isBrowserAction {
			lines = appendWrappedAppLinkLines(lines, appLinkNewlyInstalledAppsMayTakeTime, width)
			if !v.IsInstalled {
				lines = appendWrappedAppLinkLines(lines, appLinkAfterInstallUseDollarToInsertPrompt, width)
			}
		}
		lines = append(lines, "")
	}
	return lines
}

func (v *AppLinkView) installConfirmationLines(width int) []string {
	lines := []string{v.installConfirmationTitle(), ""}
	switch {
	case v.isAuthSuggestion():
		if v.ElicitationTarget != nil && v.ElicitationTarget.ServerName == AppLinkCodexAppsServerName {
			lines = appendWrappedAppLinkLines(lines, "Sign in to the app on ChatGPT in the browser window that just opened.", width)
		} else {
			lines = appendWrappedAppLinkLines(lines, "Complete authentication in the browser window that just opened.", width)
		}
		lines = appendWrappedAppLinkLines(lines, `Then return here and select "I already signed in".`, width)
	case v.isExternalActionSuggestion():
		lines = appendWrappedAppLinkLines(lines, "Complete the requested action in the browser window that just opened.", width)
		lines = appendWrappedAppLinkLines(lines, `Then return here and select "I finished".`, width)
	default:
		lines = appendWrappedAppLinkLines(lines, "Complete app setup on ChatGPT in the browser window that just opened.", width)
		lines = appendWrappedAppLinkLines(lines, `Sign in there if needed, then return here and select "I already Installed it".`, width)
	}
	lines = append(lines, "", v.urlLabel())
	lines = appendWrappedAppLinkLines(lines, v.URL, width)
	return lines
}

func (v *AppLinkView) installConfirmationTitle() string {
	if v.isAuthSuggestion() {
		if v.ElicitationTarget != nil && v.ElicitationTarget.ServerName == AppLinkCodexAppsServerName {
			return "Finish App Sign In"
		}
		return "Finish Authentication"
	}
	if v.isExternalActionSuggestion() {
		return "Finish in Browser"
	}
	return "Finish App Setup"
}

func (v *AppLinkView) urlLabel() string {
	if v.isAuthSuggestion() {
		return "Sign-in URL:"
	}
	if v.isExternalActionSuggestion() {
		return "Link:"
	}
	return "Setup URL:"
}

func (v *AppLinkView) isToolSuggestion() bool {
	return v != nil && v.ElicitationTarget != nil
}

func (v *AppLinkView) isAuthSuggestion() bool {
	return v != nil && v.isToolSuggestion() && v.suggestion() == AppLinkSuggestionAuth
}

func (v *AppLinkView) isExternalActionSuggestion() bool {
	return v != nil && v.isToolSuggestion() && v.suggestion() == AppLinkSuggestionExternalAction
}

func (v *AppLinkView) isBrowserActionSuggestion() bool {
	return v.isAuthSuggestion() || v.isExternalActionSuggestion()
}

func (v *AppLinkView) suggestion() AppLinkSuggestionType {
	if v == nil || v.SuggestionType == nil {
		return ""
	}
	return *v.SuggestionType
}

func (v *AppLinkView) resolveElicitation(decision AppLinkElicitationDecision) {
	if v == nil || v.ElicitationTarget == nil {
		return
	}
	v.events = append(v.events, AppLinkEvent{
		Kind:       AppLinkEventResolveElicitation,
		ThreadID:   v.ElicitationTarget.ThreadID,
		ServerName: v.ElicitationTarget.ServerName,
		RequestID:  v.ElicitationTarget.RequestID,
		Decision:   decision,
	})
}

func (v *AppLinkView) declineToolSuggestion() {
	v.resolveElicitation(AppLinkElicitationDecline)
	v.complete = true
}

func (v *AppLinkView) openExternalURL() {
	v.events = append(v.events, AppLinkEvent{Kind: AppLinkEventOpenURL, URL: v.URL})
	if !v.IsInstalled || v.isBrowserActionSuggestion() {
		v.Screen = AppLinkScreenInstallConfirmation
		v.SelectedAction = 0
	}
}

func (v *AppLinkView) completeExternalFlowAndClose() {
	shouldRefresh := v.ElicitationTarget == nil || v.ElicitationTarget.ServerName == AppLinkCodexAppsServerName
	if shouldRefresh {
		v.events = append(v.events, AppLinkEvent{Kind: AppLinkEventRefreshConnectors, ForceRefetch: true})
	}
	if v.isToolSuggestion() {
		v.resolveElicitation(AppLinkElicitationAccept)
	}
	v.complete = true
}

func (v *AppLinkView) backToLinkScreen() {
	v.Screen = AppLinkScreenLink
	v.SelectedAction = 0
}

func (v *AppLinkView) toggleEnabled() {
	v.IsEnabled = !v.IsEnabled
	v.events = append(v.events, AppLinkEvent{Kind: AppLinkEventSetAppEnabled, AppID: v.AppID, Enabled: v.IsEnabled})
	if v.isToolSuggestion() {
		v.resolveElicitation(AppLinkElicitationAccept)
		v.complete = true
	}
}

func appLinkParamsFromCodexAppsAuthURLParts(threadID string, serverName string, requestID string, meta map[string]any, message string, externalURL string, elicitationID string) (AppLinkViewParams, bool) {
	authFailure, ok := mapFromAny(meta[appLinkCodexAppsMetaKey])
	if !ok {
		return AppLinkViewParams{}, false
	}
	authFailure, ok = mapFromAny(authFailure[appLinkConnectorAuthFailureMetaKey])
	if !ok || boolFromAny(authFailure[appLinkConnectorAuthFailureIsAuthFailure]) != true {
		return AppLinkViewParams{}, false
	}
	appID := firstNonEmpty(strings.TrimSpace(stringFromAny(authFailure[appLinkConnectorAuthFailureConnectorID])), elicitationID)
	title := firstNonEmpty(strings.TrimSpace(stringFromAny(authFailure[appLinkConnectorAuthFailureConnectorName])), appID)
	suggestion := AppLinkSuggestionAuth
	return AppLinkViewParams{
		AppID:             appID,
		Title:             title,
		Instructions:      "Sign in to this app in your browser, then return here.",
		URL:               externalURL,
		IsInstalled:       true,
		IsEnabled:         true,
		SuggestReason:     stringPtr(message),
		SuggestionType:    &suggestion,
		ElicitationTarget: &AppLinkElicitationTarget{ThreadID: threadID, ServerName: serverName, RequestID: requestID},
	}, true
}

func appLinkParamsFromGenericURLParts(threadID string, serverName string, requestID string, message string, externalURL string, elicitationID string) AppLinkViewParams {
	description := "Server: " + serverName
	suggestion := AppLinkSuggestionExternalAction
	return AppLinkViewParams{
		AppID:             elicitationID,
		Title:             "Action required",
		Description:       &description,
		Instructions:      "Complete the requested action in your browser, then return here.",
		URL:               externalURL,
		IsInstalled:       true,
		IsEnabled:         true,
		SuggestReason:     stringPtr(message),
		SuggestionType:    &suggestion,
		ElicitationTarget: &AppLinkElicitationTarget{ThreadID: threadID, ServerName: serverName, RequestID: requestID},
	}
}

func validateAppLinkExternalURL(raw string, requireChatGPTHost bool) (*url.URL, bool) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return nil, false
	}
	if parsed.User != nil && (parsed.User.Username() != "" || hasPassword(parsed.User)) {
		return nil, false
	}
	if requireChatGPTHost && !isAllowedChatGPTAuthHost(parsed.Hostname()) {
		return nil, false
	}
	return parsed, true
}

func isAllowedChatGPTAuthHost(host string) bool {
	host = strings.ToLower(host)
	return host == "chatgpt.com" ||
		host == "chatgpt-staging.com" ||
		strings.HasSuffix(host, ".chatgpt.com") ||
		strings.HasSuffix(host, ".chatgpt-staging.com")
}

func appendWrappedAppLinkLines(lines []string, text string, width int) []string {
	if width <= 0 {
		width = 72
	}
	wrapped := tui.AdaptiveWrapLine(text, tui.WrapOptions{Width: width, BreakWords: true})
	return append(lines, wrapped...)
}

func normalizeAppLinkKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.ReplaceAll(key, "+", "-")
	return key
}

func trimTrailingBlankRows(rows []string) []string {
	for len(rows) > 0 && strings.TrimSpace(rows[len(rows)-1]) == "" {
		rows = rows[:len(rows)-1]
	}
	return rows
}

func trimmedPtr(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func stringPtr(value string) *string {
	return &value
}

func cloneStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func cloneSuggestionPtr(value *AppLinkSuggestionType) *AppLinkSuggestionType {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func cloneAppLinkTarget(value *AppLinkElicitationTarget) *AppLinkElicitationTarget {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func mapFromAny(value any) (map[string]any, bool) {
	mapped, ok := value.(map[string]any)
	return mapped, ok
}

func boolFromAny(value any) bool {
	boolean, _ := value.(bool)
	return boolean
}

func stringFromAny(value any) string {
	text, _ := value.(string)
	return text
}

func hasPassword(user *url.Userinfo) bool {
	_, ok := user.Password()
	return ok
}
