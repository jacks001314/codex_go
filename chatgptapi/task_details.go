package chatgptapi

import (
	"encoding/json"
	"strings"
)

type RateLimitResetCreditsSummary struct {
	AvailableCount int64 `json:"available_count"`
}

type RateLimitsWithResetCredits struct {
	RateLimits            []RateLimitSnapshot
	RateLimitResetCredits *RateLimitResetCreditsSummary
}

type TokenUsageProfile struct {
	Stats TokenUsageProfileStats `json:"stats"`
}

type TokenUsageProfileStats struct {
	LifetimeTokens        *int64                          `json:"lifetime_tokens"`
	PeakDailyTokens       *int64                          `json:"peak_daily_tokens"`
	LongestRunningTurnSec *int64                          `json:"longest_running_turn_sec"`
	CurrentStreakDays     *int64                          `json:"current_streak_days"`
	LongestStreakDays     *int64                          `json:"longest_streak_days"`
	DailyUsageBuckets     *[]TokenUsageProfileDailyBucket `json:"daily_usage_buckets"`
}

type TokenUsageProfileDailyBucket struct {
	StartDate string `json:"start_date"`
	Tokens    int64  `json:"tokens"`
}

type WorkspaceMessagesResponse struct {
	Messages []WorkspaceMessage `json:"messages"`
}

type WorkspaceMessage struct {
	MessageID   string               `json:"message_id"`
	MessageType WorkspaceMessageType `json:"message_type"`
	MessageBody string               `json:"message_body"`
	CreatedAt   *string              `json:"created_at,omitempty"`
	ArchivedAt  *string              `json:"archived_at,omitempty"`
}

type WorkspaceMessageType string

const (
	WorkspaceMessageHeadline     WorkspaceMessageType = "headline"
	WorkspaceMessageAnnouncement WorkspaceMessageType = "announcement"
	WorkspaceMessageUnknown      WorkspaceMessageType = "unknown"
)

func (t *WorkspaceMessageType) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	switch WorkspaceMessageType(value) {
	case WorkspaceMessageHeadline, WorkspaceMessageAnnouncement:
		*t = WorkspaceMessageType(value)
	default:
		*t = WorkspaceMessageUnknown
	}
	return nil
}

type ConsumeRateLimitResetCreditCode string

const (
	ConsumeReset           ConsumeRateLimitResetCreditCode = "reset"
	ConsumeNothingToReset  ConsumeRateLimitResetCreditCode = "nothing_to_reset"
	ConsumeNoCredit        ConsumeRateLimitResetCreditCode = "no_credit"
	ConsumeAlreadyRedeemed ConsumeRateLimitResetCreditCode = "already_redeemed"
)

type ConsumeRateLimitResetCreditResponse struct {
	Code         ConsumeRateLimitResetCreditCode `json:"code"`
	WindowsReset int64                           `json:"windows_reset"`
}

type CodeTaskDetailsResponse struct {
	CurrentUserTurn      *CodeTaskTurn `json:"current_user_turn,omitempty"`
	CurrentAssistantTurn *CodeTaskTurn `json:"current_assistant_turn,omitempty"`
	CurrentDiffTaskTurn  *CodeTaskTurn `json:"current_diff_task_turn,omitempty"`
}

type CodeTaskTurn struct {
	ID               *string            `json:"id,omitempty"`
	AttemptPlacement *int64             `json:"attempt_placement,omitempty"`
	CreatedAt        any                `json:"created_at,omitempty"`
	TurnStatus       *string            `json:"turn_status,omitempty"`
	SiblingTurnIDs   []string           `json:"sibling_turn_ids"`
	InputItems       []CodeTaskTurnItem `json:"input_items"`
	OutputItems      []CodeTaskTurnItem `json:"output_items"`
	Worklog          *CodeTaskWorklog   `json:"worklog,omitempty"`
	Error            *CodeTaskTurnError `json:"error,omitempty"`
}

type CodeTaskTurnItem struct {
	Kind       string               `json:"type"`
	Role       *string              `json:"role,omitempty"`
	Content    []CodeTaskContent    `json:"content"`
	Diff       *string              `json:"diff,omitempty"`
	OutputDiff *CodeTaskDiffPayload `json:"output_diff,omitempty"`
	Extra      map[string]any       `json:"-"`
}

type CodeTaskContent struct {
	ContentType *string
	Text        *string
	Raw         *string
}

func (c *CodeTaskContent) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err == nil {
		c.Raw = &raw
		return nil
	}
	var structured struct {
		ContentType *string `json:"content_type"`
		Text        *string `json:"text"`
	}
	if err := json.Unmarshal(data, &structured); err != nil {
		return err
	}
	c.ContentType = structured.ContentType
	c.Text = structured.Text
	return nil
}

type CodeTaskDiffPayload struct {
	Diff *string `json:"diff,omitempty"`
}

type CodeTaskWorklog struct {
	Messages []CodeTaskWorklogMessage `json:"messages"`
}

type CodeTaskWorklogMessage struct {
	Author  *CodeTaskAuthor         `json:"author,omitempty"`
	Content *CodeTaskWorklogContent `json:"content,omitempty"`
}

type CodeTaskAuthor struct {
	Role *string `json:"role,omitempty"`
}

type CodeTaskWorklogContent struct {
	Parts []CodeTaskContent `json:"parts"`
}

type CodeTaskTurnError struct {
	Code    *string `json:"code,omitempty"`
	Message *string `json:"message,omitempty"`
}

func (r *CodeTaskDetailsResponse) UnifiedDiff() (string, bool) {
	if r == nil {
		return "", false
	}
	for _, turn := range []*CodeTaskTurn{r.CurrentDiffTaskTurn, r.CurrentAssistantTurn} {
		if diff, ok := turn.unifiedDiff(); ok {
			return diff, true
		}
	}
	return "", false
}

func (r *CodeTaskDetailsResponse) AssistantTextMessages() []string {
	if r == nil {
		return nil
	}
	var out []string
	for _, turn := range []*CodeTaskTurn{r.CurrentDiffTaskTurn, r.CurrentAssistantTurn} {
		out = append(out, turn.messageTexts()...)
	}
	return out
}

func (r *CodeTaskDetailsResponse) UserTextPrompt() (string, bool) {
	if r == nil || r.CurrentUserTurn == nil {
		return "", false
	}
	return r.CurrentUserTurn.userPrompt()
}

func (r *CodeTaskDetailsResponse) AssistantErrorMessage() (string, bool) {
	if r == nil || r.CurrentAssistantTurn == nil || r.CurrentAssistantTurn.Error == nil {
		return "", false
	}
	return r.CurrentAssistantTurn.Error.summary()
}

func (t *CodeTaskTurn) UnmarshalJSON(data []byte) error {
	type rawTurn CodeTaskTurn
	var raw rawTurn
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.SiblingTurnIDs == nil {
		raw.SiblingTurnIDs = []string{}
	}
	if raw.InputItems == nil {
		raw.InputItems = []CodeTaskTurnItem{}
	}
	if raw.OutputItems == nil {
		raw.OutputItems = []CodeTaskTurnItem{}
	}
	*t = CodeTaskTurn(raw)
	return nil
}

func (w *CodeTaskWorklog) UnmarshalJSON(data []byte) error {
	type rawWorklog CodeTaskWorklog
	var raw rawWorklog
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.Messages == nil {
		raw.Messages = []CodeTaskWorklogMessage{}
	}
	*w = CodeTaskWorklog(raw)
	return nil
}

func (c *CodeTaskWorklogContent) UnmarshalJSON(data []byte) error {
	type rawContent CodeTaskWorklogContent
	var raw rawContent
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.Parts == nil {
		raw.Parts = []CodeTaskContent{}
	}
	*c = CodeTaskWorklogContent(raw)
	return nil
}

func (i *CodeTaskTurnItem) UnmarshalJSON(data []byte) error {
	type rawItem CodeTaskTurnItem
	var raw rawItem
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.Content == nil {
		raw.Content = []CodeTaskContent{}
	}
	*i = CodeTaskTurnItem(raw)
	return nil
}

func (t *CodeTaskTurn) unifiedDiff() (string, bool) {
	if t == nil {
		return "", false
	}
	for i := range t.OutputItems {
		if diff, ok := t.OutputItems[i].diffText(); ok {
			return diff, true
		}
	}
	return "", false
}

func (t *CodeTaskTurn) messageTexts() []string {
	if t == nil {
		return nil
	}
	var out []string
	for i := range t.OutputItems {
		item := &t.OutputItems[i]
		if item.Kind == "message" {
			out = append(out, item.textValues()...)
		}
	}
	if t.Worklog != nil {
		for i := range t.Worklog.Messages {
			message := &t.Worklog.Messages[i]
			if message.isAssistant() {
				out = append(out, message.textValues()...)
			}
		}
	}
	return out
}

func (t *CodeTaskTurn) userPrompt() (string, bool) {
	if t == nil {
		return "", false
	}
	var parts []string
	for i := range t.InputItems {
		item := &t.InputItems[i]
		if item.Kind != "message" {
			continue
		}
		if item.Role != nil && !strings.EqualFold(*item.Role, "user") {
			continue
		}
		parts = append(parts, item.textValues()...)
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, "\n\n"), true
}

func (t *CodeTaskTurn) attempt() (*CloudTurnAttempt, bool) {
	if t == nil || t.ID == nil || strings.TrimSpace(*t.ID) == "" {
		return nil, false
	}
	status := CloudAttemptStatusUnknown
	if t.TurnStatus != nil {
		status = mapCloudAttemptStatus(*t.TurnStatus)
	}
	var diff *string
	if text, ok := t.unifiedDiff(); ok {
		diff = &text
	}
	return &CloudTurnAttempt{
		TurnID:           strings.TrimSpace(*t.ID),
		AttemptPlacement: cloneInt64Pointer(t.AttemptPlacement),
		CreatedAt:        parseCloudTime(t.CreatedAt),
		Status:           status,
		Diff:             diff,
		Messages:         t.messageTexts(),
	}, true
}

func (i *CodeTaskTurnItem) textValues() []string {
	if i == nil {
		return nil
	}
	out := make([]string, 0, len(i.Content))
	for index := range i.Content {
		if text, ok := i.Content[index].text(); ok {
			out = append(out, text)
		}
	}
	return out
}

func (i *CodeTaskTurnItem) diffText() (string, bool) {
	if i == nil {
		return "", false
	}
	if i.Kind == "output_diff" && i.Diff != nil && *i.Diff != "" {
		return *i.Diff, true
	}
	if i.Kind == "pr" && i.OutputDiff != nil && i.OutputDiff.Diff != nil && *i.OutputDiff.Diff != "" {
		return *i.OutputDiff.Diff, true
	}
	return "", false
}

func (c *CodeTaskContent) text() (string, bool) {
	if c == nil {
		return "", false
	}
	if c.Raw != nil {
		if strings.TrimSpace(*c.Raw) == "" {
			return "", false
		}
		return *c.Raw, true
	}
	if c.ContentType != nil && strings.EqualFold(*c.ContentType, "text") && c.Text != nil && *c.Text != "" {
		return *c.Text, true
	}
	return "", false
}

func (m *CodeTaskWorklogMessage) isAssistant() bool {
	return m != nil && m.Author != nil && m.Author.Role != nil && strings.EqualFold(*m.Author.Role, "assistant")
}

func (m *CodeTaskWorklogMessage) textValues() []string {
	if m == nil || m.Content == nil {
		return nil
	}
	out := make([]string, 0, len(m.Content.Parts))
	for i := range m.Content.Parts {
		if text, ok := m.Content.Parts[i].text(); ok {
			out = append(out, text)
		}
	}
	return out
}

func (e *CodeTaskTurnError) summary() (string, bool) {
	if e == nil {
		return "", false
	}
	code := ""
	if e.Code != nil {
		code = *e.Code
	}
	message := ""
	if e.Message != nil {
		message = *e.Message
	}
	switch {
	case code == "" && message == "":
		return "", false
	case code == "":
		return message, true
	case message == "":
		return code, true
	default:
		return code + ": " + message, true
	}
}
