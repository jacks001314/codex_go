package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"codex_go/internal/compact"
)

type PlanStatus string

const (
	PlanPending    PlanStatus = "pending"
	PlanInProgress PlanStatus = "in_progress"
	PlanCompleted  PlanStatus = "completed"
)

type PlanItem struct {
	Step   string     `json:"step"`
	Status PlanStatus `json:"status"`
}

type UpdatePlanArgs struct {
	Explanation string     `json:"explanation,omitempty"`
	Plan        []PlanItem `json:"plan"`
}

func (a *UpdatePlanArgs) Validate() error {
	if a == nil {
		return fmt.Errorf("plan args are nil")
	}
	active := 0
	for _, item := range a.Plan {
		if strings.TrimSpace(item.Step) == "" {
			return fmt.Errorf("plan step is required")
		}
		switch item.Status {
		case PlanPending, PlanInProgress, PlanCompleted:
		default:
			return fmt.Errorf("invalid plan status %q", item.Status)
		}
		if item.Status == PlanInProgress {
			active++
		}
	}
	if active > 1 {
		return fmt.Errorf("at most one plan item may be in progress")
	}
	return nil
}

type PlanStore struct {
	mu          sync.Mutex
	explanation string
	plan        []PlanItem
	updatedAt   time.Time
	now         func() time.Time
}

func NewPlanStore() *PlanStore {
	return &PlanStore{now: time.Now}
}

func (s *PlanStore) SetClock(clock func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if clock == nil {
		s.now = time.Now
		return
	}
	s.now = clock
}

func (s *PlanStore) Update(args UpdatePlanArgs) error {
	if err := args.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.explanation = args.Explanation
	s.plan = append([]PlanItem(nil), args.Plan...)
	s.updatedAt = s.now().UTC()
	return nil
}

func (s *PlanStore) Snapshot() (string, []PlanItem, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.explanation, append([]PlanItem(nil), s.plan...), s.updatedAt
}

type PlanHandler struct {
	store *PlanStore
}

func NewPlanHandler(store *PlanStore) *PlanHandler {
	if store == nil {
		store = NewPlanStore()
	}
	return &PlanHandler{store: store}
}

func (h *PlanHandler) Spec() Spec {
	return Spec{
		Name:        PlainName("update_plan"),
		Description: "Updates the current task plan.",
		InputSchema: map[string]any{
			"required": []string{"plan"},
		},
	}
}

func (h *PlanHandler) Execute(ctx context.Context, invocation *Invocation) (*Output, error) {
	_ = ctx
	var args UpdatePlanArgs
	if err := invocation.DecodeArguments(&args); err != nil {
		return nil, err
	}
	if err := h.store.Update(args); err != nil {
		return nil, err
	}
	data := map[string]any{
		"planUpdate": true,
		"plan":       append([]PlanItem(nil), args.Plan...),
	}
	if strings.TrimSpace(args.Explanation) != "" {
		data["explanation"] = args.Explanation
	}
	return &Output{
		Success: true,
		Body:    "Plan updated",
		Data:    data,
	}, nil
}

type UserInputChoice struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

type UserInputQuestion struct {
	Header   string            `json:"header"`
	ID       string            `json:"id"`
	Question string            `json:"question"`
	IsOther  bool              `json:"isOther,omitempty"`
	IsSecret bool              `json:"isSecret,omitempty"`
	Options  []UserInputChoice `json:"options,omitempty"`
}

type RequestUserInputArgs struct {
	Questions        []UserInputQuestion `json:"questions"`
	AutoResolutionMS *int                `json:"autoResolutionMs,omitempty"`
}

func (a *RequestUserInputArgs) Normalize() error {
	if a == nil {
		return fmt.Errorf("request_user_input args are nil")
	}
	if len(a.Questions) == 0 || len(a.Questions) > 3 {
		return fmt.Errorf("request_user_input requires one to three questions")
	}
	for index := range a.Questions {
		q := &a.Questions[index]
		q.Header = strings.TrimSpace(q.Header)
		q.ID = strings.TrimSpace(q.ID)
		q.Question = strings.TrimSpace(q.Question)
		if q.ID == "" || q.Question == "" {
			return fmt.Errorf("question id and question are required")
		}
		if len(q.Header) > 12 {
			q.Header = q.Header[:12]
		}
		if len(q.Options) > 3 {
			q.Options = q.Options[:3]
		}
	}
	if a.AutoResolutionMS != nil && (*a.AutoResolutionMS < 60000 || *a.AutoResolutionMS > 240000) {
		return fmt.Errorf("autoResolutionMs must be between 60000 and 240000")
	}
	return nil
}

type UserInputResponse struct {
	Answers           map[string]string   `json:"answers"`
	StructuredAnswers map[string][]string `json:"structured_answers,omitempty"`
	TimedOut          bool                `json:"timed_out,omitempty"`
}

type UserInputResponder func(context.Context, *RequestUserInputArgs) (*UserInputResponse, error)

type RequestUserInputHandler struct {
	responder      UserInputResponder
	availableModes []string
}

func NewRequestUserInputHandler(responder UserInputResponder) *RequestUserInputHandler {
	return NewRequestUserInputHandlerWithModes(responder, nil)
}

func NewRequestUserInputHandlerWithModes(responder UserInputResponder, modes []string) *RequestUserInputHandler {
	return &RequestUserInputHandler{
		responder:      responder,
		availableModes: normalizeRequestUserInputAvailableModes(modes),
	}
}

func (h *RequestUserInputHandler) Spec() Spec {
	return Spec{Name: PlainName("request_user_input"), Description: requestUserInputToolDescription(h.availableModes)}
}

func normalizeRequestUserInputAvailableModes(modes []string) []string {
	if len(modes) == 0 {
		return []string{"Plan"}
	}
	out := make([]string, 0, len(modes))
	for _, mode := range modes {
		mode = strings.TrimSpace(mode)
		if mode != "" {
			out = append(out, mode)
		}
	}
	if len(out) == 0 {
		return []string{"Plan"}
	}
	return out
}

func requestUserInputToolDescription(modes []string) string {
	allowedModes := requestUserInputAllowedModesText(normalizeRequestUserInputAvailableModes(modes))
	return "Request user input for one to three short questions and wait for the response. Set autoResolutionMs, from 60000 to 240000 milliseconds, only when the question is useful but non-blocking and continuing with best judgment is acceptable if the user does not answer; omit it when explicit user input is required. This tool is only available in " + allowedModes + "."
}

func requestUserInputAllowedModesText(modes []string) string {
	switch len(modes) {
	case 0:
		return "no modes"
	case 1:
		return modes[0] + " mode"
	case 2:
		return modes[0] + " or " + modes[1] + " mode"
	default:
		return "modes: " + strings.Join(modes, ",")
	}
}

func (h *RequestUserInputHandler) Execute(ctx context.Context, invocation *Invocation) (*Output, error) {
	var args RequestUserInputArgs
	if err := invocation.DecodeArguments(&args); err != nil {
		return nil, err
	}
	if err := args.Normalize(); err != nil {
		return nil, err
	}
	response := &UserInputResponse{Answers: map[string]string{}}
	var err error
	if h.responder != nil {
		response, err = h.responder(ctx, &args)
		if err != nil {
			return nil, err
		}
	}
	body, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}
	return &Output{Success: true, Body: string(body)}, nil
}

type GetContextRemainingHandler struct {
	status func() compact.TokenStatus
}

func NewGetContextRemainingHandler(status func() compact.TokenStatus) *GetContextRemainingHandler {
	return &GetContextRemainingHandler{status: status}
}

func (h *GetContextRemainingHandler) Spec() Spec {
	return Spec{Name: PlainName("get_context_remaining"), Description: "Returns context tokens left before compaction."}
}

func (h *GetContextRemainingHandler) Execute(ctx context.Context, invocation *Invocation) (*Output, error) {
	_ = ctx
	_ = invocation
	status := compact.TokenStatus{}
	if h.status != nil {
		status = h.status()
	}
	payload := map[string]any{"tokens_left": status.TokensUntilCompaction}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &Output{Success: true, Body: string(body), Data: payload}, nil
}

type SleepHandler struct{}

func (h *SleepHandler) Spec() Spec {
	return Spec{Name: PlainName("sleep"), Description: "Sleeps for the requested duration."}
}

func (h *SleepHandler) Execute(ctx context.Context, invocation *Invocation) (*Output, error) {
	var args struct {
		DurationMS int `json:"duration_ms"`
	}
	if err := invocation.DecodeArguments(&args); err != nil {
		return nil, err
	}
	if args.DurationMS < 0 {
		return nil, fmt.Errorf("duration_ms must be non-negative")
	}
	timer := time.NewTimer(time.Duration(args.DurationMS) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return &Output{Success: true, Body: "ok"}, nil
	}
}

const (
	clockToolNamespace       = "clock"
	currentTimeToolName      = "curr_time"
	maxSleepToolDurationMS   = 12 * 60 * 60 * 1000
	formattedCurrentTimeSpec = "2006-01-02 15:04:05 UTC"
)

type ClockProvider interface {
	CurrentTime(ctx context.Context, threadID string) (time.Time, error)
	Sleep(ctx context.Context, threadID string, duration time.Duration) error
}

type systemClockProvider struct{}

func (systemClockProvider) CurrentTime(ctx context.Context, threadID string) (time.Time, error) {
	_ = threadID
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}
	return time.Now().UTC(), nil
}

func (systemClockProvider) Sleep(ctx context.Context, threadID string, duration time.Duration) error {
	_ = threadID
	if ctx == nil {
		ctx = context.Background()
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type CurrentTimeHandler struct {
	provider ClockProvider
	threadID string
}

func NewCurrentTimeHandler(provider ClockProvider, threadID string) *CurrentTimeHandler {
	return &CurrentTimeHandler{provider: provider, threadID: strings.TrimSpace(threadID)}
}

func (h *CurrentTimeHandler) Spec() Spec {
	return Spec{
		Name:        NamespacedName(clockToolNamespace, currentTimeToolName),
		Description: "Return the current time in UTC.",
		InputSchema: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"additionalProperties": false,
		},
		NamespaceDescription: "Tools for reading and waiting on time.",
	}
}

func (h *CurrentTimeHandler) Execute(ctx context.Context, invocation *Invocation) (*Output, error) {
	_ = invocation
	current, err := h.clockProvider().CurrentTime(ctx, h.threadID)
	if err != nil {
		return nil, fmt.Errorf("failed to read current time: %w", err)
	}
	formatted := current.UTC().Format(formattedCurrentTimeSpec)
	return &Output{
		Success: true,
		Body:    "It is " + formatted + ".",
		Data:    map[string]any{"current_time": formatted},
	}, nil
}

func (h *CurrentTimeHandler) clockProvider() ClockProvider {
	if h != nil && h.provider != nil {
		return h.provider
	}
	return systemClockProvider{}
}

type ClockSleepHandler struct {
	provider ClockProvider
	threadID string
}

func NewClockSleepHandler(provider ClockProvider, threadID string) *ClockSleepHandler {
	return &ClockSleepHandler{provider: provider, threadID: strings.TrimSpace(threadID)}
}

func (h *ClockSleepHandler) Spec() Spec {
	return Spec{
		Name:        NamespacedName(clockToolNamespace, "sleep"),
		Description: "Pause execution for a specified duration. The sleep ends early when new input arrives for the active turn. Returns the elapsed wall-clock time.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"duration_ms": map[string]any{
					"type":        "number",
					"description": fmt.Sprintf("How long to sleep in milliseconds. Must be between 1 and %d.", maxSleepToolDurationMS),
				},
			},
			"required":             []string{"duration_ms"},
			"additionalProperties": false,
		},
		NamespaceDescription: "Tools for reading and waiting on time.",
	}
}

func (h *ClockSleepHandler) Execute(ctx context.Context, invocation *Invocation) (*Output, error) {
	var args struct {
		DurationMS uint64 `json:"duration_ms"`
	}
	if err := invocation.DecodeArguments(&args); err != nil {
		return nil, err
	}
	if args.DurationMS < 1 || args.DurationMS > maxSleepToolDurationMS {
		return nil, fmt.Errorf("duration_ms must be between 1 and %d", maxSleepToolDurationMS)
	}
	started := time.Now()
	if err := h.clockProvider().Sleep(ctx, h.threadID, time.Duration(args.DurationMS)*time.Millisecond); err != nil {
		return nil, fmt.Errorf("failed to sleep: %w", err)
	}
	return &Output{
		Success: true,
		Body:    fmt.Sprintf("Wall time: %.4f seconds\nSleep completed.", time.Since(started).Seconds()),
	}, nil
}

func (h *ClockSleepHandler) clockProvider() ClockProvider {
	if h != nil && h.provider != nil {
		return h.provider
	}
	return systemClockProvider{}
}

type CoreHandlerOptions struct {
	PlanStore                      *PlanStore
	ContextStatus                  func() compact.TokenStatus
	UserInputResponder             UserInputResponder
	RequestUserInputAvailableModes []string
	ClockProvider                  ClockProvider
	ThreadID                       string
	EnableCurrentTime              bool
	EnableClockSleep               bool
	EnableLegacySleep              bool
}

func RegisterCoreHandlers(registry *Registry, planStore *PlanStore, status func() compact.TokenStatus, responder UserInputResponder) error {
	return RegisterCoreHandlersWithOptions(registry, &CoreHandlerOptions{
		PlanStore:          planStore,
		ContextStatus:      status,
		UserInputResponder: responder,
		EnableLegacySleep:  true,
	})
}

func RegisterCoreHandlersWithOptions(registry *Registry, options *CoreHandlerOptions) error {
	if options == nil {
		options = &CoreHandlerOptions{}
	}
	handlers := []Executor{
		NewPlanHandler(options.PlanStore),
		NewRequestUserInputHandlerWithModes(options.UserInputResponder, options.RequestUserInputAvailableModes),
		NewGetContextRemainingHandler(options.ContextStatus),
	}
	if options.EnableLegacySleep {
		handlers = append(handlers, &SleepHandler{})
	}
	if options.EnableCurrentTime {
		handlers = append(handlers, NewCurrentTimeHandler(options.ClockProvider, options.ThreadID))
	}
	if options.EnableClockSleep {
		handlers = append(handlers, NewClockSleepHandler(options.ClockProvider, options.ThreadID))
	}
	for _, handler := range handlers {
		if err := registry.Register(handler); err != nil {
			return err
		}
	}
	return nil
}
