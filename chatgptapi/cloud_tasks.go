package chatgptapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const DefaultCloudTasksBaseURL = "https://chatgpt.com/backend-api"

type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type CloudTaskStatus string

const (
	CloudTaskStatusPending CloudTaskStatus = "pending"
	CloudTaskStatusReady   CloudTaskStatus = "ready"
	CloudTaskStatusApplied CloudTaskStatus = "applied"
	CloudTaskStatusError   CloudTaskStatus = "error"
)

type CloudAttemptStatus string

const (
	CloudAttemptStatusPending    CloudAttemptStatus = "pending"
	CloudAttemptStatusInProgress CloudAttemptStatus = "in-progress"
	CloudAttemptStatusCompleted  CloudAttemptStatus = "completed"
	CloudAttemptStatusFailed     CloudAttemptStatus = "failed"
	CloudAttemptStatusCancelled  CloudAttemptStatus = "cancelled"
	CloudAttemptStatusUnknown    CloudAttemptStatus = "unknown"
)

type AddCreditsNudgeCreditType string

const (
	AddCreditsNudgeCredits    AddCreditsNudgeCreditType = "credits"
	AddCreditsNudgeUsageLimit AddCreditsNudgeCreditType = "usage_limit"
)

type CloudDiffSummary struct {
	FilesChanged int64 `json:"files_changed"`
	LinesAdded   int64 `json:"lines_added"`
	LinesRemoved int64 `json:"lines_removed"`
}

type CloudTaskSummary struct {
	ID               string           `json:"id"`
	Title            string           `json:"title"`
	Status           CloudTaskStatus  `json:"status"`
	UpdatedAt        *time.Time       `json:"updated_at,omitempty"`
	EnvironmentID    *string          `json:"environment_id,omitempty"`
	EnvironmentLabel *string          `json:"environment_label,omitempty"`
	Summary          CloudDiffSummary `json:"summary"`
	IsReview         bool             `json:"is_review"`
	AttemptTotal     int64            `json:"attempt_total"`
}

type CloudTaskListPage struct {
	Tasks  []CloudTaskSummary `json:"tasks"`
	Cursor *string            `json:"cursor,omitempty"`
}

type CloudCreatedTask struct {
	ID string `json:"id"`
}

type HTTPStatusError struct {
	Method     string
	URL        string
	Status     string
	StatusCode int
	Body       string
}

func (e *HTTPStatusError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("%s %s failed: %s; body=%s", e.Method, e.URL, e.Status, strings.TrimSpace(e.Body))
}

func (e *HTTPStatusError) IsStatus(status int) bool {
	return e != nil && e.StatusCode == status
}

type CloudTaskText struct {
	Prompt           string             `json:"prompt"`
	Messages         []string           `json:"messages"`
	TurnID           *string            `json:"turn_id,omitempty"`
	SiblingTurnIDs   []string           `json:"sibling_turn_ids"`
	AttemptPlacement *int64             `json:"attempt_placement,omitempty"`
	AttemptStatus    CloudAttemptStatus `json:"attempt_status"`
}

type CloudTurnAttempt struct {
	TurnID           string             `json:"turn_id"`
	AttemptPlacement *int64             `json:"attempt_placement,omitempty"`
	CreatedAt        *time.Time         `json:"created_at,omitempty"`
	Status           CloudAttemptStatus `json:"status"`
	Diff             *string            `json:"diff,omitempty"`
	Messages         []string           `json:"messages"`
}

type CloudListTasksParams struct {
	Limit         int64
	TaskFilter    string
	EnvironmentID string
	Cursor        string
}

type CloudCreateTaskParams struct {
	EnvironmentID string
	Prompt        string
	Branch        string
	QAMode        bool
	BestOfN       int
}

type CloudClientOptions struct {
	BaseURL    string
	Headers    http.Header
	HTTPClient HTTPDoer
}

type CloudClient struct {
	baseURL    string
	pathStyle  cloudPathStyle
	headers    http.Header
	httpClient HTTPDoer
}

type cloudPathStyle string

const (
	cloudPathStyleCodexAPI   cloudPathStyle = "codex-api"
	cloudPathStyleChatGPTAPI cloudPathStyle = "chatgpt-api"
)

func NewCloudClient(options *CloudClientOptions) *CloudClient {
	baseURL := DefaultCloudTasksBaseURL
	headers := http.Header{}
	var httpClient HTTPDoer = http.DefaultClient
	if options != nil {
		if strings.TrimSpace(options.BaseURL) != "" {
			baseURL = options.BaseURL
		}
		headers = cloneHTTPHeader(options.Headers)
		if options.HTTPClient != nil {
			httpClient = options.HTTPClient
		}
	}
	normalized := NormalizeCloudBaseURL(baseURL)
	if headers.Get("User-Agent") == "" {
		headers.Set("User-Agent", "codex-go")
	}
	return &CloudClient{
		baseURL:    normalized,
		pathStyle:  cloudPathStyleFromBaseURL(normalized),
		headers:    headers,
		httpClient: httpClient,
	}
}

func NormalizeCloudBaseURL(input string) string {
	value := strings.TrimRight(strings.TrimSpace(input), "/")
	if value == "" {
		value = DefaultCloudTasksBaseURL
	}
	if (strings.HasPrefix(value, "https://chatgpt.com") || strings.HasPrefix(value, "https://chat.openai.com")) && !strings.Contains(value, "/backend-api") {
		value += "/backend-api"
	}
	return value
}

func CloudTaskURL(baseURL string, taskID string) string {
	normalized := NormalizeCloudBaseURL(baseURL)
	switch {
	case strings.HasSuffix(normalized, "/backend-api"):
		root := strings.TrimSuffix(normalized, "/backend-api")
		return root + "/codex/tasks/" + url.PathEscape(taskID)
	case strings.HasSuffix(normalized, "/api/codex"):
		root := strings.TrimSuffix(normalized, "/api/codex")
		return root + "/codex/tasks/" + url.PathEscape(taskID)
	case strings.HasSuffix(normalized, "/codex"):
		return normalized + "/tasks/" + url.PathEscape(taskID)
	default:
		return normalized + "/codex/tasks/" + url.PathEscape(taskID)
	}
}

func ParseCloudTaskID(input string) (string, error) {
	value := strings.TrimSpace(input)
	if value == "" {
		return "", errors.New("cloud task id is empty")
	}
	if parsed, err := url.Parse(value); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		value = parsed.Path
	}
	value, _, _ = strings.Cut(value, "?")
	value, _, _ = strings.Cut(value, "#")
	value = strings.Trim(value, "/")
	if index := strings.LastIndex(value, "/"); index >= 0 {
		value = value[index+1:]
	}
	if value == "" {
		return "", errors.New("cloud task id is empty")
	}
	return value, nil
}

func (c *CloudClient) BaseURL() string {
	if c == nil {
		return ""
	}
	return c.baseURL
}

func (c *CloudClient) ListTasks(ctx context.Context, params *CloudListTasksParams) (*CloudTaskListPage, error) {
	if c == nil {
		return nil, errors.New("cloud client is nil")
	}
	query := url.Values{}
	if params != nil {
		if params.Limit > 0 {
			query.Set("limit", strconv.FormatInt(params.Limit, 10))
		}
		if strings.TrimSpace(params.TaskFilter) != "" {
			query.Set("task_filter", strings.TrimSpace(params.TaskFilter))
		}
		if strings.TrimSpace(params.Cursor) != "" {
			query.Set("cursor", strings.TrimSpace(params.Cursor))
		}
		if strings.TrimSpace(params.EnvironmentID) != "" {
			query.Set("environment_id", strings.TrimSpace(params.EnvironmentID))
		}
	}
	var response cloudTaskListResponse
	if err := c.doJSON(ctx, http.MethodGet, c.apiPath("tasks/list"), query, nil, &response); err != nil {
		return nil, err
	}
	return response.page(), nil
}

func (c *CloudClient) GetTaskSummary(ctx context.Context, taskID string) (*CloudTaskSummary, error) {
	details, err := c.getTaskDetailsEnvelope(ctx, taskID)
	if err != nil {
		return nil, err
	}
	return details.summary(taskID), nil
}

func (c *CloudClient) GetTaskDiff(ctx context.Context, taskID string) (string, bool, error) {
	details, err := c.getTaskDetailsEnvelope(ctx, taskID)
	if err != nil {
		return "", false, err
	}
	diff, ok := details.Details().UnifiedDiff()
	return diff, ok, nil
}

func (c *CloudClient) GetTaskText(ctx context.Context, taskID string) (*CloudTaskText, error) {
	details, err := c.getTaskDetailsEnvelope(ctx, taskID)
	if err != nil {
		return nil, err
	}
	return details.taskText(), nil
}

func (c *CloudClient) ListSiblingAttempts(ctx context.Context, taskID string, turnID string) ([]CloudTurnAttempt, error) {
	if c == nil {
		return nil, errors.New("cloud client is nil")
	}
	parsedID, err := ParseCloudTaskID(taskID)
	if err != nil {
		return nil, err
	}
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return nil, errors.New("turn id is empty")
	}
	var response cloudSiblingTurnsResponse
	if err := c.doJSON(ctx, http.MethodGet, c.apiPath("tasks", url.PathEscape(parsedID), "turns", url.PathEscape(turnID), "sibling_turns"), nil, nil, &response); err != nil {
		return nil, err
	}
	attempts := response.attempts()
	sortCloudTurnAttempts(attempts)
	return attempts, nil
}

func (c *CloudClient) CreateTask(ctx context.Context, params *CloudCreateTaskParams) (*CloudCreatedTask, error) {
	if c == nil {
		return nil, errors.New("cloud client is nil")
	}
	if params == nil {
		return nil, errors.New("cloud create task params are nil")
	}
	environmentID := strings.TrimSpace(params.EnvironmentID)
	if environmentID == "" {
		return nil, errors.New("environment id is required")
	}
	prompt := strings.TrimSpace(params.Prompt)
	if prompt == "" {
		return nil, errors.New("prompt is required")
	}
	body := map[string]any{
		"new_task": map[string]any{
			"environment_id":             environmentID,
			"branch":                     strings.TrimSpace(params.Branch),
			"run_environment_in_qa_mode": params.QAMode,
		},
		"input_items": []map[string]any{
			{
				"type": "message",
				"role": "user",
				"content": []map[string]string{
					{
						"content_type": "text",
						"text":         prompt,
					},
				},
			},
		},
	}
	if params.BestOfN > 1 {
		body["metadata"] = map[string]any{"best_of_n": params.BestOfN}
	}
	var response cloudCreateTaskResponse
	if err := c.doJSON(ctx, http.MethodPost, c.apiPath("tasks"), nil, body, &response); err != nil {
		return nil, err
	}
	id := strings.TrimSpace(response.ID)
	if id == "" && response.Task != nil {
		id = strings.TrimSpace(response.Task.ID)
	}
	if id == "" {
		return nil, errors.New("cloud create task response did not include task id")
	}
	return &CloudCreatedTask{ID: id}, nil
}

func (c *CloudClient) SendAddCreditsNudgeEmail(ctx context.Context, creditType AddCreditsNudgeCreditType) error {
	if c == nil {
		return errors.New("cloud client is nil")
	}
	body := map[string]string{
		"credit_type": string(creditType),
	}
	return c.doJSON(ctx, http.MethodPost, c.apiPath("accounts", "send_add_credits_nudge_email"), nil, body, nil)
}

func (c *CloudClient) GetRateLimitsWithResetCredits(ctx context.Context) (*RateLimitsWithResetCredits, error) {
	if c == nil {
		return nil, errors.New("cloud client is nil")
	}
	var payload RateLimitStatusPayload
	if err := c.doJSON(ctx, http.MethodGet, c.apiPath("usage"), nil, nil, &payload); err != nil {
		return nil, err
	}
	return &RateLimitsWithResetCredits{
		RateLimits:            RateLimitSnapshotsFromPayload(&payload),
		RateLimitResetCredits: payload.RateLimitResetCredits,
	}, nil
}

func (c *CloudClient) ConsumeRateLimitResetCredit(ctx context.Context, idempotencyKey string) (*ConsumeRateLimitResetCreditResponse, error) {
	if c == nil {
		return nil, errors.New("cloud client is nil")
	}
	body := map[string]string{
		"redeem_request_id": strings.TrimSpace(idempotencyKey),
	}
	var response ConsumeRateLimitResetCreditResponse
	if err := c.doJSON(ctx, http.MethodPost, c.apiPath("rate-limit-reset-credits", "consume"), nil, body, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *CloudClient) GetTokenUsageProfile(ctx context.Context) (*TokenUsageProfile, error) {
	if c == nil {
		return nil, errors.New("cloud client is nil")
	}
	var profile TokenUsageProfile
	if err := c.doJSON(ctx, http.MethodGet, c.apiPath("profiles", "me"), nil, nil, &profile); err != nil {
		return nil, err
	}
	return &profile, nil
}

func (c *CloudClient) ListWorkspaceMessages(ctx context.Context) (*WorkspaceMessagesResponse, error) {
	if c == nil {
		return nil, errors.New("cloud client is nil")
	}
	var response WorkspaceMessagesResponse
	if err := c.doJSON(ctx, http.MethodGet, c.apiPath("workspace-messages"), nil, nil, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *CloudClient) getTaskDetailsEnvelope(ctx context.Context, taskID string) (*cloudTaskDetailsEnvelope, error) {
	if c == nil {
		return nil, errors.New("cloud client is nil")
	}
	parsedID, err := ParseCloudTaskID(taskID)
	if err != nil {
		return nil, err
	}
	var envelope cloudTaskDetailsEnvelope
	if err := c.doJSON(ctx, http.MethodGet, c.apiPath("tasks", url.PathEscape(parsedID)), nil, nil, &envelope); err != nil {
		return nil, err
	}
	return &envelope, nil
}

func (c *CloudClient) doJSON(ctx context.Context, method string, path string, query url.Values, body any, out any) error {
	target, err := url.Parse(c.baseURL + path)
	if err != nil {
		return err
	}
	if len(query) > 0 {
		target.RawQuery = query.Encode()
	}
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, target.String(), reader)
	if err != nil {
		return err
	}
	for key, values := range c.headers {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 32<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &HTTPStatusError{
			Method:     method,
			URL:        target.String(),
			Status:     response.Status,
			StatusCode: response.StatusCode,
			Body:       strings.TrimSpace(string(responseBody)),
		}
	}
	if out == nil {
		return nil
	}
	if len(bytes.TrimSpace(responseBody)) == 0 {
		return nil
	}
	if err := json.Unmarshal(responseBody, out); err != nil {
		return fmt.Errorf("decode %s response: %w; body=%s", target.String(), err, string(responseBody))
	}
	return nil
}

func (c *CloudClient) apiPath(parts ...string) string {
	prefix := "/api/codex"
	if c.pathStyle == cloudPathStyleChatGPTAPI {
		prefix = "/wham"
	}
	if len(parts) == 0 {
		return prefix
	}
	return prefix + "/" + strings.Join(parts, "/")
}

func cloudPathStyleFromBaseURL(baseURL string) cloudPathStyle {
	if strings.Contains(baseURL, "/backend-api") {
		return cloudPathStyleChatGPTAPI
	}
	return cloudPathStyleCodexAPI
}

type cloudCreateTaskResponse struct {
	ID   string             `json:"id"`
	Task *cloudTaskMetadata `json:"task,omitempty"`
}

type cloudTaskListResponse struct {
	Items  []cloudTaskListItem `json:"items"`
	Tasks  []cloudTaskListItem `json:"tasks"`
	Cursor *string             `json:"cursor,omitempty"`
}

type cloudTaskListItem struct {
	ID                string         `json:"id"`
	Title             string         `json:"title"`
	Status            string         `json:"status"`
	UpdatedAt         any            `json:"updated_at,omitempty"`
	CreatedAt         any            `json:"created_at,omitempty"`
	EnvironmentID     *string        `json:"environment_id,omitempty"`
	EnvironmentLabel  *string        `json:"environment_label,omitempty"`
	TaskStatusDisplay map[string]any `json:"task_status_display,omitempty"`
	Summary           map[string]any `json:"summary,omitempty"`
	IsReview          bool           `json:"is_review"`
	AttemptTotal      int64          `json:"attempt_total"`
}

func (r *cloudTaskListResponse) page() *CloudTaskListPage {
	if r == nil {
		return &CloudTaskListPage{Tasks: []CloudTaskSummary{}}
	}
	items := r.Items
	if len(items) == 0 && len(r.Tasks) > 0 {
		items = r.Tasks
	}
	tasks := make([]CloudTaskSummary, 0, len(items))
	for i := range items {
		tasks = append(tasks, *items[i].summary())
	}
	return &CloudTaskListPage{Tasks: tasks, Cursor: cloneStringPointer(r.Cursor)}
}

func (i *cloudTaskListItem) summary() *CloudTaskSummary {
	if i == nil {
		return &CloudTaskSummary{}
	}
	updatedAt := parseCloudTime(i.UpdatedAt)
	if updatedAt == nil {
		updatedAt = parseCloudTime(i.CreatedAt)
	}
	return &CloudTaskSummary{
		ID:               i.ID,
		Title:            firstNonEmpty(i.Title, i.ID),
		Status:           mapCloudTaskStatus(firstNonEmpty(i.Status, stringFromMap(i.TaskStatusDisplay, "status"))),
		UpdatedAt:        updatedAt,
		EnvironmentID:    cloneStringPointer(i.EnvironmentID),
		EnvironmentLabel: cloneStringPointer(i.EnvironmentLabel),
		Summary:          diffSummaryFromMaps(i.Summary, i.TaskStatusDisplay),
		IsReview:         i.IsReview,
		AttemptTotal:     i.AttemptTotal,
	}
}

type cloudTaskDetailsEnvelope struct {
	Task                 *cloudTaskMetadata         `json:"task,omitempty"`
	TaskStatusDisplay    map[string]any             `json:"task_status_display,omitempty"`
	CurrentUserTurn      *CodeTaskTurn              `json:"current_user_turn,omitempty"`
	CurrentAssistantTurn *CodeTaskTurn              `json:"current_assistant_turn,omitempty"`
	CurrentDiffTaskTurn  *CodeTaskTurn              `json:"current_diff_task_turn,omitempty"`
	Extra                map[string]json.RawMessage `json:"-"`
}

type cloudSiblingTurnsResponse struct {
	SiblingTurns []CodeTaskTurn `json:"sibling_turns"`
}

func (r *cloudSiblingTurnsResponse) attempts() []CloudTurnAttempt {
	if r == nil {
		return nil
	}
	attempts := make([]CloudTurnAttempt, 0, len(r.SiblingTurns))
	for i := range r.SiblingTurns {
		if attempt, ok := r.SiblingTurns[i].attempt(); ok {
			attempts = append(attempts, *attempt)
		}
	}
	return attempts
}

func sortCloudTurnAttempts(attempts []CloudTurnAttempt) {
	sort.SliceStable(attempts, func(i int, j int) bool {
		left := &attempts[i]
		right := &attempts[j]
		switch {
		case left.AttemptPlacement != nil && right.AttemptPlacement != nil:
			if *left.AttemptPlacement == *right.AttemptPlacement {
				return left.TurnID < right.TurnID
			}
			return *left.AttemptPlacement < *right.AttemptPlacement
		case left.AttemptPlacement != nil:
			return true
		case right.AttemptPlacement != nil:
			return false
		case left.CreatedAt != nil && right.CreatedAt != nil:
			if left.CreatedAt.Equal(*right.CreatedAt) {
				return left.TurnID < right.TurnID
			}
			return left.CreatedAt.Before(*right.CreatedAt)
		case left.CreatedAt != nil:
			return true
		case right.CreatedAt != nil:
			return false
		default:
			return left.TurnID < right.TurnID
		}
	})
}

type cloudTaskMetadata struct {
	ID               string         `json:"id"`
	Title            string         `json:"title"`
	Status           string         `json:"status"`
	UpdatedAt        any            `json:"updated_at,omitempty"`
	CreatedAt        any            `json:"created_at,omitempty"`
	EnvironmentID    *string        `json:"environment_id,omitempty"`
	EnvironmentLabel *string        `json:"environment_label,omitempty"`
	Summary          map[string]any `json:"summary,omitempty"`
	IsReview         bool           `json:"is_review"`
	AttemptTotal     int64          `json:"attempt_total"`
}

func (e *cloudTaskDetailsEnvelope) Details() *CodeTaskDetailsResponse {
	if e == nil {
		return &CodeTaskDetailsResponse{}
	}
	return &CodeTaskDetailsResponse{
		CurrentUserTurn:      e.CurrentUserTurn,
		CurrentAssistantTurn: e.CurrentAssistantTurn,
		CurrentDiffTaskTurn:  e.CurrentDiffTaskTurn,
	}
}

func (e *cloudTaskDetailsEnvelope) summary(fallbackID string) *CloudTaskSummary {
	if e == nil {
		return &CloudTaskSummary{ID: fallbackID, Status: CloudTaskStatusPending}
	}
	metadata := e.Task
	if metadata == nil {
		metadata = &cloudTaskMetadata{}
	}
	updatedAt := parseCloudTime(metadata.UpdatedAt)
	if updatedAt == nil {
		updatedAt = parseCloudTime(metadata.CreatedAt)
	}
	diff, _ := e.Details().UnifiedDiff()
	summary := diffSummaryFromMaps(metadata.Summary, e.TaskStatusDisplay)
	if summary == (CloudDiffSummary{}) && strings.TrimSpace(diff) != "" {
		summary = diffSummaryFromUnifiedDiff(diff)
	}
	status := firstNonEmpty(metadata.Status, stringFromMap(e.TaskStatusDisplay, "status"))
	return &CloudTaskSummary{
		ID:               firstNonEmpty(metadata.ID, fallbackID),
		Title:            firstNonEmpty(metadata.Title, metadata.ID, fallbackID),
		Status:           mapCloudTaskStatus(status),
		UpdatedAt:        updatedAt,
		EnvironmentID:    cloneStringPointer(metadata.EnvironmentID),
		EnvironmentLabel: cloneStringPointer(metadata.EnvironmentLabel),
		Summary:          summary,
		IsReview:         metadata.IsReview,
		AttemptTotal:     metadata.AttemptTotal,
	}
}

func (e *cloudTaskDetailsEnvelope) taskText() *CloudTaskText {
	details := e.Details()
	prompt, _ := details.UserTextPrompt()
	messages := details.AssistantTextMessages()
	out := &CloudTaskText{
		Prompt:         prompt,
		Messages:       append([]string(nil), messages...),
		SiblingTurnIDs: []string{},
		AttemptStatus:  CloudAttemptStatusUnknown,
	}
	for _, turn := range []*CodeTaskTurn{details.CurrentAssistantTurn, details.CurrentDiffTaskTurn, details.CurrentUserTurn} {
		if turn == nil {
			continue
		}
		if out.TurnID == nil && turn.ID != nil {
			out.TurnID = cloneStringPointer(turn.ID)
		}
		if out.AttemptPlacement == nil && turn.AttemptPlacement != nil {
			value := *turn.AttemptPlacement
			out.AttemptPlacement = &value
		}
		if len(out.SiblingTurnIDs) == 0 && len(turn.SiblingTurnIDs) > 0 {
			out.SiblingTurnIDs = append([]string(nil), turn.SiblingTurnIDs...)
		}
		if turn.TurnStatus != nil {
			out.AttemptStatus = mapCloudAttemptStatus(*turn.TurnStatus)
		}
	}
	return out
}

func parseCloudTime(value any) *time.Time {
	switch v := value.(type) {
	case nil:
		return nil
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return nil
		}
		if parsed, err := time.Parse(time.RFC3339Nano, trimmed); err == nil {
			return &parsed
		}
		if parsed, err := strconv.ParseFloat(trimmed, 64); err == nil {
			return unixFloatTime(parsed)
		}
	case float64:
		return unixFloatTime(v)
	case int64:
		parsed := time.Unix(v, 0).UTC()
		return &parsed
	case json.Number:
		if parsed, err := v.Float64(); err == nil {
			return unixFloatTime(parsed)
		}
	}
	return nil
}

func unixFloatTime(seconds float64) *time.Time {
	if seconds <= 0 {
		return nil
	}
	whole := int64(seconds)
	nanos := int64((seconds - float64(whole)) * 1e9)
	parsed := time.Unix(whole, nanos).UTC()
	return &parsed
}

func mapCloudTaskStatus(value string) CloudTaskStatus {
	normalized := normalizeStatus(value)
	switch normalized {
	case "ready", "completed", "complete", "done", "success", "succeeded":
		return CloudTaskStatusReady
	case "applied":
		return CloudTaskStatusApplied
	case "error", "failed", "failure", "cancelled", "canceled":
		return CloudTaskStatusError
	default:
		return CloudTaskStatusPending
	}
}

func mapCloudAttemptStatus(value string) CloudAttemptStatus {
	normalized := normalizeStatus(value)
	switch normalized {
	case "pending", "queued":
		return CloudAttemptStatusPending
	case "in_progress", "in-progress", "running", "started":
		return CloudAttemptStatusInProgress
	case "completed", "complete", "done", "ready", "success", "succeeded":
		return CloudAttemptStatusCompleted
	case "failed", "failure", "error":
		return CloudAttemptStatusFailed
	case "cancelled", "canceled":
		return CloudAttemptStatusCancelled
	default:
		return CloudAttemptStatusUnknown
	}
}

func normalizeStatus(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	return value
}

func diffSummaryFromMaps(primary map[string]any, fallback map[string]any) CloudDiffSummary {
	return CloudDiffSummary{
		FilesChanged: firstInt64FromMaps("files_changed", primary, fallback),
		LinesAdded:   firstInt64FromMaps("lines_added", primary, fallback),
		LinesRemoved: firstInt64FromMaps("lines_removed", primary, fallback),
	}
}

func diffSummaryFromUnifiedDiff(diff string) CloudDiffSummary {
	var summary CloudDiffSummary
	seenFiles := map[string]struct{}{}
	for _, line := range strings.Split(strings.ReplaceAll(diff, "\r\n", "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				seenFiles[parts[3]] = struct{}{}
			}
		case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
			continue
		case strings.HasPrefix(line, "+"):
			summary.LinesAdded++
		case strings.HasPrefix(line, "-"):
			summary.LinesRemoved++
		}
	}
	summary.FilesChanged = int64(len(seenFiles))
	return summary
}

func firstInt64FromMaps(key string, maps ...map[string]any) int64 {
	for _, values := range maps {
		if values == nil {
			continue
		}
		if parsed, ok := int64FromAny(values[key]); ok {
			return parsed
		}
	}
	return 0
}

func int64FromAny(value any) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		return int64(v), true
	case json.Number:
		parsed, err := v.Int64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func stringFromMap(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneInt64Pointer(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneHTTPHeader(headers http.Header) http.Header {
	cloned := http.Header{}
	for key, values := range headers {
		cloned[key] = append([]string(nil), values...)
	}
	return cloned
}
