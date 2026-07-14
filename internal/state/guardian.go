package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	ReviewerUser       Reviewer = "user"
	ReviewerAutoReview Reviewer = "auto_review"
	ReviewerLegacy     Reviewer = "guardian_subagent"

	ReviewTimeout                = 90 * time.Second
	MaxConsecutiveDenialsPerTurn = 3
	MaxRecentDenialsPerTurn      = 10
	AutoReviewDenialWindowSize   = 50
	DeniedActionApprovalPrefix   = "The user has manually approved a specific action that was previously `Rejected`."
)

var ErrInvalidGuardianRequest = errors.New("invalid guardian request")

type Reviewer string

func ReviewerFromString(value string) Reviewer {
	switch strings.TrimSpace(value) {
	case string(ReviewerAutoReview), string(ReviewerLegacy):
		return ReviewerAutoReview
	case string(ReviewerUser), "":
		return ReviewerUser
	default:
		return Reviewer(value)
	}
}

func (r *Reviewer) RoutesToGuardian() bool {
	return r != nil && *r == ReviewerAutoReview
}

type RiskLevel string

const (
	RiskLow      RiskLevel = "low"
	RiskMedium   RiskLevel = "medium"
	RiskHigh     RiskLevel = "high"
	RiskCritical RiskLevel = "critical"
)

type UserAuthorization string

const (
	AuthorizationUnknown UserAuthorization = "unknown"
	AuthorizationLow     UserAuthorization = "low"
	AuthorizationMedium  UserAuthorization = "medium"
	AuthorizationHigh    UserAuthorization = "high"
)

type Outcome string

const (
	OutcomeAllow Outcome = "allow"
	OutcomeDeny  Outcome = "deny"
)

type Status string

const (
	StatusInProgress Status = "in_progress"
	StatusApproved   Status = "approved"
	StatusDenied     Status = "denied"
	StatusTimedOut   Status = "timed_out"
	StatusAborted    Status = "aborted"
)

type DecisionSource string

const DecisionSourceAgent DecisionSource = "agent"

type CommandSource string

const (
	CommandSourceShell       CommandSource = "shell"
	CommandSourceUnifiedExec CommandSource = "unified_exec"
)

type Action struct {
	Type          string         `json:"type"`
	Source        CommandSource  `json:"source,omitempty"`
	Command       string         `json:"command,omitempty"`
	Program       string         `json:"program,omitempty"`
	Argv          []string       `json:"argv,omitempty"`
	CWD           string         `json:"cwd,omitempty"`
	Files         []string       `json:"files,omitempty"`
	Target        string         `json:"target,omitempty"`
	Host          string         `json:"host,omitempty"`
	Protocol      string         `json:"protocol,omitempty"`
	Port          uint16         `json:"port,omitempty"`
	Server        string         `json:"server,omitempty"`
	ToolName      string         `json:"toolName,omitempty"`
	ConnectorID   string         `json:"connectorId,omitempty"`
	ConnectorName string         `json:"connectorName,omitempty"`
	ToolTitle     string         `json:"toolTitle,omitempty"`
	Reason        string         `json:"reason,omitempty"`
	Permissions   map[string]any `json:"permissions,omitempty"`
	Extra         map[string]any `json:"extra,omitempty"`
}

func (a *Action) Validate() error {
	if a == nil {
		return fmt.Errorf("%w: action is required", ErrInvalidGuardianRequest)
	}
	switch a.Type {
	case "command":
		if a.Command == "" || a.CWD == "" {
			return fmt.Errorf("%w: command and cwd are required", ErrInvalidGuardianRequest)
		}
	case "execve":
		if a.Program == "" || a.CWD == "" {
			return fmt.Errorf("%w: program and cwd are required", ErrInvalidGuardianRequest)
		}
	case "apply_patch":
		if a.CWD == "" || len(a.Files) == 0 {
			return fmt.Errorf("%w: cwd and files are required", ErrInvalidGuardianRequest)
		}
	case "network_access":
		if a.Host == "" || a.Protocol == "" || a.Port == 0 {
			return fmt.Errorf("%w: host protocol and port are required", ErrInvalidGuardianRequest)
		}
	case "mcp_tool_call":
		if a.Server == "" || a.ToolName == "" {
			return fmt.Errorf("%w: server and toolName are required", ErrInvalidGuardianRequest)
		}
	case "request_permissions":
		if len(a.Permissions) == 0 {
			return fmt.Errorf("%w: permissions are required", ErrInvalidGuardianRequest)
		}
	default:
		return fmt.Errorf("%w: unsupported action %q", ErrInvalidGuardianRequest, a.Type)
	}
	return nil
}

type Assessment struct {
	RiskLevel         RiskLevel         `json:"riskLevel"`
	UserAuthorization UserAuthorization `json:"userAuthorization"`
	Outcome           Outcome           `json:"outcome"`
	Rationale         string            `json:"rationale"`
}

func (a *Assessment) Validate() error {
	if a == nil {
		return fmt.Errorf("%w: assessment is required", ErrInvalidGuardianRequest)
	}
	if a.RiskLevel == "" || a.UserAuthorization == "" || a.Outcome == "" {
		return fmt.Errorf("%w: assessment fields are required", ErrInvalidGuardianRequest)
	}
	if a.Outcome != OutcomeAllow && a.Outcome != OutcomeDeny {
		return fmt.Errorf("%w: unsupported outcome %q", ErrInvalidGuardianRequest, a.Outcome)
	}
	return nil
}

type Event struct {
	ID                string             `json:"id"`
	TargetItemID      string             `json:"targetItemId,omitempty"`
	TurnID            string             `json:"turnId"`
	StartedAtMS       int64              `json:"startedAtMs"`
	CompletedAtMS     *int64             `json:"completedAtMs,omitempty"`
	Status            Status             `json:"status"`
	RiskLevel         *RiskLevel         `json:"riskLevel,omitempty"`
	UserAuthorization *UserAuthorization `json:"userAuthorization,omitempty"`
	Rationale         string             `json:"rationale,omitempty"`
	DecisionSource    *DecisionSource    `json:"decisionSource,omitempty"`
	Action            Action             `json:"action"`
}

func NewInProgressEvent(id string, turnID string, targetItemID string, action Action, now time.Time) (*Event, error) {
	if err := action.Validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("%w: id is required", ErrInvalidGuardianRequest)
	}
	return &Event{
		ID:           id,
		TurnID:       turnID,
		TargetItemID: targetItemID,
		StartedAtMS:  unixMillis(now),
		Status:       StatusInProgress,
		Action:       action,
	}, nil
}

func (e *Event) Complete(assessment Assessment, now time.Time) (*Event, error) {
	if e == nil {
		return nil, fmt.Errorf("%w: event is nil", ErrInvalidGuardianRequest)
	}
	if err := assessment.Validate(); err != nil {
		return nil, err
	}
	completed := *e
	completedMS := unixMillis(now)
	completed.CompletedAtMS = &completedMS
	completed.RiskLevel = &assessment.RiskLevel
	completed.UserAuthorization = &assessment.UserAuthorization
	completed.Rationale = assessment.Rationale
	source := DecisionSourceAgent
	completed.DecisionSource = &source
	if assessment.Outcome == OutcomeAllow {
		completed.Status = StatusApproved
	} else {
		completed.Status = StatusDenied
	}
	return &completed, nil
}

func (e *Event) Timeout(now time.Time) *Event {
	if e == nil {
		return nil
	}
	completed := *e
	completedMS := unixMillis(now)
	completed.CompletedAtMS = &completedMS
	completed.Status = StatusTimedOut
	completed.Rationale = GuardianTimeoutMessage()
	return &completed
}

func (e *Event) Aborted(now time.Time, reason string) *Event {
	if e == nil {
		return nil
	}
	completed := *e
	completedMS := unixMillis(now)
	completed.CompletedAtMS = &completedMS
	completed.Status = StatusAborted
	completed.Rationale = reason
	return &completed
}

func (e *Event) Terminal() bool {
	return e != nil && e.Status != StatusInProgress
}

func GuardianTimeoutMessage() string {
	return "Auto-approval review timed out; the request was denied."
}

func GuardianRejectionMessage(event *Event) string {
	if event == nil {
		return "Auto-approval review denied the request."
	}
	if event.Rationale != "" {
		return event.Rationale
	}
	return "Auto-approval review denied the request."
}

type ReviewDecision string

const (
	DecisionApproved ReviewDecision = "approved"
	DecisionDenied   ReviewDecision = "denied"
	DecisionTimedOut ReviewDecision = "timed_out"
	DecisionAborted  ReviewDecision = "aborted"
)

func DecisionFromEvent(event *Event) ReviewDecision {
	if event == nil {
		return DecisionDenied
	}
	switch event.Status {
	case StatusApproved:
		return DecisionApproved
	case StatusTimedOut:
		return DecisionTimedOut
	case StatusAborted:
		return DecisionAborted
	default:
		return DecisionDenied
	}
}

type CircuitBreakerAction struct {
	InterruptTurn      bool
	ConsecutiveDenials uint32
	RecentDenials      uint32
}

type CircuitBreaker struct {
	mu    sync.Mutex
	turns map[string]*turnState
}

type turnState struct {
	consecutiveDenials uint32
	recentDenials      []bool
	interruptTriggered bool
}

func NewCircuitBreaker() *CircuitBreaker {
	return &CircuitBreaker{turns: map[string]*turnState{}}
}

func (b *CircuitBreaker) ClearTurn(turnID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.turns, turnID)
}

func (b *CircuitBreaker) RecordDenial(turnID string) CircuitBreakerAction {
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.state(turnID)
	state.consecutiveDenials++
	recordRecent(state, true)
	recent := countRecentDenials(state)
	action := CircuitBreakerAction{ConsecutiveDenials: state.consecutiveDenials, RecentDenials: recent}
	if !state.interruptTriggered && (state.consecutiveDenials >= MaxConsecutiveDenialsPerTurn || recent >= MaxRecentDenialsPerTurn) {
		state.interruptTriggered = true
		action.InterruptTurn = true
	}
	return action
}

func (b *CircuitBreaker) RecordNonDenial(turnID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.state(turnID)
	state.consecutiveDenials = 0
	recordRecent(state, false)
}

func (b *CircuitBreaker) state(turnID string) *turnState {
	if b.turns == nil {
		b.turns = map[string]*turnState{}
	}
	state := b.turns[turnID]
	if state == nil {
		state = &turnState{}
		b.turns[turnID] = state
	}
	return state
}

type ReviewStore struct {
	mu      sync.Mutex
	events  map[string]*Event
	counter uint64
	now     func() time.Time
}

func NewReviewStore() *ReviewStore {
	return &ReviewStore{events: map[string]*Event{}, now: time.Now}
}

func (s *ReviewStore) SetClock(clock func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if clock == nil {
		s.now = time.Now
		return
	}
	s.now = clock
}

func (s *ReviewStore) NewReviewID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counter++
	return fmt.Sprintf("guardian-review-%d", s.counter)
}

func (s *ReviewStore) Start(turnID string, targetItemID string, action Action) (*Event, error) {
	id := s.NewReviewID()
	event, err := NewInProgressEvent(id, turnID, targetItemID, action, s.now())
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events[id] = event
	return cloneEvent(event), nil
}

func (s *ReviewStore) Complete(id string, assessment Assessment) (*Event, error) {
	s.mu.Lock()
	event := s.events[id]
	s.mu.Unlock()
	if event == nil {
		return nil, fmt.Errorf("%w: review not found", ErrInvalidGuardianRequest)
	}
	completed, err := event.Complete(assessment, s.now())
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.events[id] = completed
	s.mu.Unlock()
	return cloneEvent(completed), nil
}

func (s *ReviewStore) Timeout(id string) (*Event, error) {
	return s.finish(id, func(event *Event, now time.Time) *Event { return event.Timeout(now) })
}

func (s *ReviewStore) Abort(id string, reason string) (*Event, error) {
	return s.finish(id, func(event *Event, now time.Time) *Event { return event.Aborted(now, reason) })
}

func (s *ReviewStore) finish(id string, transition func(*Event, time.Time) *Event) (*Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	event := s.events[id]
	if event == nil {
		return nil, fmt.Errorf("%w: review not found", ErrInvalidGuardianRequest)
	}
	completed := transition(event, s.now())
	s.events[id] = completed
	return cloneEvent(completed), nil
}

func (s *ReviewStore) Get(id string) (*Event, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	event := s.events[id]
	if event == nil {
		return nil, false
	}
	return cloneEvent(event), true
}

type NotificationMethod string

const (
	NotificationReviewStarted   NotificationMethod = "item/autoApprovalReview/started"
	NotificationReviewCompleted NotificationMethod = "item/autoApprovalReview/completed"
)

type Notification struct {
	Method NotificationMethod `json:"method"`
	Params any                `json:"params"`
}

type ApprovalReview struct {
	ID                string             `json:"id"`
	TargetItemID      string             `json:"targetItemId,omitempty"`
	Status            Status             `json:"status"`
	RiskLevel         *RiskLevel         `json:"riskLevel,omitempty"`
	UserAuthorization *UserAuthorization `json:"userAuthorization,omitempty"`
	Rationale         string             `json:"rationale,omitempty"`
	Action            Action             `json:"action"`
}

type ReviewStartedNotification struct {
	ThreadID string         `json:"threadId"`
	TurnID   string         `json:"turnId"`
	Review   ApprovalReview `json:"review"`
}

type ReviewCompletedNotification struct {
	ThreadID string         `json:"threadId"`
	TurnID   string         `json:"turnId"`
	Review   ApprovalReview `json:"review"`
}

func NotificationFromEvent(threadID string, event *Event) Notification {
	review := ApprovalReview{
		ID:                event.ID,
		TargetItemID:      event.TargetItemID,
		Status:            event.Status,
		RiskLevel:         event.RiskLevel,
		UserAuthorization: event.UserAuthorization,
		Rationale:         event.Rationale,
		Action:            event.Action,
	}
	if event.Status == StatusInProgress {
		return Notification{Method: NotificationReviewStarted, Params: ReviewStartedNotification{ThreadID: threadID, TurnID: event.TurnID, Review: review}}
	}
	return Notification{Method: NotificationReviewCompleted, Params: ReviewCompletedNotification{ThreadID: threadID, TurnID: event.TurnID, Review: review}}
}

func ParseAssessment(data []byte) (*Assessment, error) {
	var assessment Assessment
	if err := json.Unmarshal(data, &assessment); err != nil {
		return nil, err
	}
	if err := assessment.Validate(); err != nil {
		return nil, err
	}
	return &assessment, nil
}

func BuildPrompt(action Action, transcript []string) (string, error) {
	if err := action.Validate(); err != nil {
		return "", err
	}
	var builder strings.Builder
	builder.WriteString("Review the planned action and decide whether to allow it.\n\n")
	builder.WriteString("Action:\n")
	data, err := json.MarshalIndent(action, "", "  ")
	if err != nil {
		return "", err
	}
	builder.Write(data)
	if len(transcript) > 0 {
		builder.WriteString("\n\nRecent transcript:\n")
		for _, line := range transcript {
			if strings.TrimSpace(line) == "" {
				continue
			}
			builder.WriteString("- ")
			builder.WriteString(strings.TrimSpace(line))
			builder.WriteByte('\n')
		}
	}
	return builder.String(), nil
}

func recordRecent(state *turnState, denied bool) {
	state.recentDenials = append(state.recentDenials, denied)
	if len(state.recentDenials) > AutoReviewDenialWindowSize {
		state.recentDenials = state.recentDenials[1:]
	}
}

func countRecentDenials(state *turnState) uint32 {
	var count uint32
	for _, denied := range state.recentDenials {
		if denied {
			count++
		}
	}
	return count
}

func cloneEvent(event *Event) *Event {
	if event == nil {
		return nil
	}
	cloned := *event
	cloned.Action.Files = append([]string(nil), event.Action.Files...)
	cloned.Action.Argv = append([]string(nil), event.Action.Argv...)
	if event.Action.Permissions != nil {
		cloned.Action.Permissions = map[string]any{}
		for key, value := range event.Action.Permissions {
			cloned.Action.Permissions[key] = value
		}
	}
	if event.Action.Extra != nil {
		cloned.Action.Extra = map[string]any{}
		for key, value := range event.Action.Extra {
			cloned.Action.Extra[key] = value
		}
	}
	return &cloned
}

func unixMillis(t time.Time) int64 {
	if t.IsZero() {
		t = time.Now().UTC()
	}
	return t.UTC().UnixNano() / int64(time.Millisecond)
}
