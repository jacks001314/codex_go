package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"codex_go/model"
	"codex_go/session"
	"github.com/google/uuid"
)

var ErrInvalidThreadExtraRequest = errors.New("invalid thread request")

type GoalStatus string

const (
	GoalActive        GoalStatus = "active"
	GoalPaused        GoalStatus = "paused"
	GoalBlocked       GoalStatus = "blocked"
	GoalUsageLimited  GoalStatus = "usageLimited"
	GoalBudgetLimited GoalStatus = "budgetLimited"
	GoalComplete      GoalStatus = "complete"
)

type Goal struct {
	ThreadID        string     `json:"threadId"`
	GoalID          string     `json:"goalId,omitempty"`
	Objective       string     `json:"objective"`
	TokenBudget     *int64     `json:"tokenBudget,omitempty"`
	TokensUsed      int64      `json:"tokensUsed"`
	TimeUsedSeconds int64      `json:"timeUsedSeconds"`
	Status          GoalStatus `json:"status"`
	CreatedAt       int64      `json:"createdAt"`
	UpdatedAt       int64      `json:"updatedAt"`
}

func (g *Goal) MarshalJSON() ([]byte, error) {
	if g == nil {
		return []byte("null"), nil
	}
	return json.Marshal(struct {
		ThreadID        string     `json:"threadId"`
		Objective       string     `json:"objective"`
		Status          GoalStatus `json:"status"`
		TokenBudget     *int64     `json:"tokenBudget"`
		TokensUsed      int64      `json:"tokensUsed"`
		TimeUsedSeconds int64      `json:"timeUsedSeconds"`
		CreatedAt       int64      `json:"createdAt"`
		UpdatedAt       int64      `json:"updatedAt"`
	}{
		ThreadID:        g.ThreadID,
		Objective:       g.Objective,
		Status:          g.Status,
		TokenBudget:     cloneInt64PtrAppserver(g.TokenBudget),
		TokensUsed:      g.TokensUsed,
		TimeUsedSeconds: g.TimeUsedSeconds,
		CreatedAt:       g.CreatedAt,
		UpdatedAt:       g.UpdatedAt,
	})
}

type GoalSetParams struct {
	ThreadID           string      `json:"threadId"`
	Objective          *string     `json:"objective,omitempty"`
	TokenBudget        *int64      `json:"tokenBudget,omitempty"`
	TokenBudgetSet     bool        `json:"-"`
	Status             *GoalStatus `json:"status,omitempty"`
	MaxGoalTokenBudget *int64      `json:"-"`
}

func (p *GoalSetParams) UnmarshalJSON(data []byte) error {
	type goalSetParamsAlias GoalSetParams
	var decoded goalSetParamsAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if tokenBudgetRaw, ok := raw["tokenBudget"]; ok {
		decoded.TokenBudgetSet = true
		if strings.TrimSpace(string(tokenBudgetRaw)) == "null" {
			decoded.TokenBudget = nil
		} else {
			var tokenBudget int64
			if err := json.Unmarshal(tokenBudgetRaw, &tokenBudget); err != nil {
				return err
			}
			decoded.TokenBudget = &tokenBudget
		}
	}
	*p = GoalSetParams(decoded)
	return nil
}

func (p *GoalSetParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidThreadExtraRequest)
	}
	if p.Objective != nil && strings.TrimSpace(*p.Objective) == "" {
		return fmt.Errorf("%w: objective is required", ErrInvalidThreadExtraRequest)
	}
	if p.Objective != nil && len([]rune(strings.TrimSpace(*p.Objective))) > 4000 {
		return fmt.Errorf("%w: goal objective must be at most 4000 characters", ErrInvalidThreadExtraRequest)
	}
	if (p.TokenBudgetSet || p.TokenBudget != nil) && p.TokenBudget != nil && *p.TokenBudget <= 0 {
		return fmt.Errorf("%w: tokenBudget must be positive", ErrInvalidThreadExtraRequest)
	}
	if p.Status != nil && !validGoalStatus(*p.Status) {
		return fmt.Errorf("%w: unsupported goal status %q", ErrInvalidThreadExtraRequest, *p.Status)
	}
	return nil
}

type GoalSetResponse struct {
	Goal Goal `json:"goal"`
}

type GoalGetParams struct {
	ThreadID string `json:"threadId"`
}

func (p *GoalGetParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidThreadExtraRequest)
	}
	return nil
}

type GoalGetResponse struct {
	Goal *Goal `json:"goal"`
}

type GoalClearParams struct {
	ThreadID string `json:"threadId"`
}

func (p *GoalClearParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidThreadExtraRequest)
	}
	return nil
}

type GoalClearResponse struct {
	Cleared bool `json:"cleared"`
}

type GoalUpdatedNotification struct {
	ThreadID string  `json:"threadId"`
	TurnID   *string `json:"turnId,omitempty"`
	Goal     Goal    `json:"goal"`
}

func (n *GoalUpdatedNotification) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ThreadID string  `json:"threadId"`
		TurnID   *string `json:"turnId"`
		Goal     Goal    `json:"goal"`
	}{
		ThreadID: n.ThreadID,
		TurnID:   cloneStringPtrAppserver(n.TurnID),
		Goal:     cloneGoal(n.Goal),
	})
}

type GoalClearedNotification struct {
	ThreadID string `json:"threadId"`
}

const threadGoalExtraKey = "thread_goal"

type GoalStore struct {
	mu    sync.Mutex
	goals map[string]Goal
	now   func() time.Time
}

func NewGoalStore() *GoalStore {
	return &GoalStore{goals: map[string]Goal{}, now: time.Now}
}

func (s *GoalStore) SetClock(now func() time.Time) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLocked()
	if now == nil {
		s.now = time.Now
		return
	}
	s.now = now
}

func (s *GoalStore) ensureLocked() {
	if s.goals == nil {
		s.goals = map[string]Goal{}
	}
	if s.now == nil {
		s.now = time.Now
	}
}

func (s *GoalStore) Set(params *GoalSetParams) (*GoalSetResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, fmt.Errorf("%w: store is nil", ErrInvalidThreadExtraRequest)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLocked()
	now := s.now().UTC().Unix()
	existing, exists := s.goals[params.ThreadID]
	var existingPtr *Goal
	if exists {
		existing = cloneGoal(existing)
		existingPtr = &existing
	}
	goal, err := buildGoalFromSetParams(params, existingPtr, now)
	if err != nil {
		return nil, err
	}
	s.goals[params.ThreadID] = goal
	return &GoalSetResponse{Goal: goal}, nil
}

func (s *GoalStore) Get(params *GoalGetParams) (*GoalGetResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, fmt.Errorf("%w: store is nil", ErrInvalidThreadExtraRequest)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLocked()
	goal, ok := s.goals[params.ThreadID]
	if !ok {
		return &GoalGetResponse{}, nil
	}
	goal = cloneGoal(goal)
	return &GoalGetResponse{Goal: &goal}, nil
}

func (s *GoalStore) Clear(params *GoalClearParams) (*GoalClearResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, fmt.Errorf("%w: store is nil", ErrInvalidThreadExtraRequest)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLocked()
	_, existed := s.goals[params.ThreadID]
	delete(s.goals, params.ThreadID)
	return &GoalClearResponse{Cleared: existed}, nil
}

type SettingsUpdateParams struct {
	ThreadID              string                     `json:"threadId"`
	CWD                   *string                    `json:"cwd,omitempty"`
	ApprovalPolicy        *string                    `json:"approvalPolicy,omitempty"`
	ApprovalsReviewer     *string                    `json:"approvalsReviewer,omitempty"`
	SandboxPolicy         *string                    `json:"sandboxPolicy,omitempty"`
	Permissions           *string                    `json:"permissions,omitempty"`
	Model                 *string                    `json:"model,omitempty"`
	ServiceTier           *ThreadExtraOptionalString `json:"serviceTier,omitempty"`
	Effort                *string                    `json:"effort,omitempty"`
	Summary               *string                    `json:"summary,omitempty"`
	CollaborationMode     map[string]any             `json:"collaborationMode,omitempty"`
	MultiAgentMode        *string                    `json:"multiAgentMode,omitempty"`
	Personality           *string                    `json:"personality,omitempty"`
	PersonalitySet        bool                       `json:"-"`
	RuntimeWorkspaceRoots []string                   `json:"runtimeWorkspaceRoots,omitempty"`
	Extra                 map[string]string          `json:"extra,omitempty"`
}

func (p *SettingsUpdateParams) UnmarshalJSON(data []byte) error {
	type settingsUpdateParamsAlias SettingsUpdateParams
	var decoded settingsUpdateParamsAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if serviceTierRaw, ok := raw["serviceTier"]; ok {
		serviceTier := &ThreadExtraOptionalString{}
		if err := serviceTier.UnmarshalJSON(serviceTierRaw); err != nil {
			return err
		}
		decoded.ServiceTier = serviceTier
	}
	*p = SettingsUpdateParams(decoded)
	return nil
}

func (p *SettingsUpdateParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidThreadExtraRequest)
	}
	if p.Permissions != nil && p.SandboxPolicy != nil {
		return fmt.Errorf("%w: permissions and sandboxPolicy are mutually exclusive", ErrInvalidThreadExtraRequest)
	}
	for _, root := range p.RuntimeWorkspaceRoots {
		if !isAbsoluteAppPath(root) {
			return fmt.Errorf("%w: runtimeWorkspaceRoots must contain absolute paths: %s", ErrInvalidThreadExtraRequest, strings.TrimSpace(root))
		}
	}
	return nil
}

type ThreadExtraOptionalString struct {
	Set   bool
	Value *string
}

func (o *ThreadExtraOptionalString) UnmarshalJSON(data []byte) error {
	o.Set = true
	value := strings.TrimSpace(string(data))
	if value == "null" {
		o.Value = nil
		return nil
	}
	var decoded string
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	o.Value = &decoded
	return nil
}

func (o *ThreadExtraOptionalString) MarshalJSON() ([]byte, error) {
	if o == nil || !o.Set || o.Value == nil {
		return []byte("null"), nil
	}
	return json.Marshal(*o.Value)
}

type SettingsUpdateResponse struct{}

type Settings struct {
	CWD                     string         `json:"cwd"`
	ApprovalPolicy          string         `json:"approvalPolicy"`
	ApprovalsReviewer       string         `json:"approvalsReviewer"`
	SandboxPolicy           string         `json:"sandboxPolicy"`
	ActivePermissionProfile *string        `json:"activePermissionProfile,omitempty"`
	Model                   string         `json:"model"`
	ModelProvider           string         `json:"modelProvider"`
	ServiceTier             *string        `json:"serviceTier"`
	Effort                  *string        `json:"effort,omitempty"`
	Summary                 *string        `json:"summary,omitempty"`
	CollaborationMode       map[string]any `json:"collaborationMode,omitempty"`
	MultiAgentMode          string         `json:"multiAgentMode"`
	Personality             *string        `json:"personality,omitempty"`
	PersonalitySet          bool           `json:"-"`
	RuntimeWorkspaceRoots   []string       `json:"runtimeWorkspaceRoots,omitempty"`
}

func (s *Settings) MarshalJSON() ([]byte, error) {
	if s == nil {
		return []byte("null"), nil
	}
	return json.Marshal(struct {
		CWD                     string         `json:"cwd"`
		ApprovalPolicy          any            `json:"approvalPolicy"`
		ApprovalsReviewer       string         `json:"approvalsReviewer"`
		SandboxPolicy           any            `json:"sandboxPolicy"`
		ActivePermissionProfile any            `json:"activePermissionProfile"`
		Model                   string         `json:"model"`
		ModelProvider           string         `json:"modelProvider"`
		ServiceTier             *string        `json:"serviceTier"`
		Effort                  *string        `json:"effort"`
		Summary                 *string        `json:"summary"`
		CollaborationMode       map[string]any `json:"collaborationMode"`
		Personality             *string        `json:"personality"`
		RuntimeWorkspaceRoots   []string       `json:"runtimeWorkspaceRoots"`
	}{
		CWD:                     s.CWD,
		ApprovalPolicy:          threadSettingsApprovalPolicy(s.ApprovalPolicy),
		ApprovalsReviewer:       threadSettingsApprovalsReviewer(s.ApprovalsReviewer),
		SandboxPolicy:           threadSettingsSandboxPolicy(s.SandboxPolicy),
		ActivePermissionProfile: threadSettingsActivePermissionProfile(s.ActivePermissionProfile),
		Model:                   s.Model,
		ModelProvider:           s.ModelProvider,
		ServiceTier:             cloneString(s.ServiceTier),
		Effort:                  cloneString(s.Effort),
		Summary:                 cloneString(s.Summary),
		CollaborationMode:       threadSettingsCollaborationMode(s),
		Personality:             cloneString(s.Personality),
		RuntimeWorkspaceRoots:   append([]string(nil), s.RuntimeWorkspaceRoots...),
	})
}

func threadSettingsApprovalPolicy(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return "on-request"
	}
	return value
}

func threadSettingsApprovalsReviewer(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "user"
	}
	return value
}

func threadSettingsSandboxPolicy(value string) any {
	switch strings.TrimSpace(value) {
	case "dangerFullAccess", "danger-full-access", ":danger-full-access", "full-access":
		return map[string]any{"type": "dangerFullAccess"}
	case "readOnly", "read-only", ":read-only":
		return map[string]any{"type": "readOnly", "networkAccess": false}
	case "externalSandbox":
		return map[string]any{"type": "externalSandbox", "networkAccess": "disabled"}
	default:
		return map[string]any{
			"type":                "workspaceWrite",
			"writableRoots":       []string{},
			"networkAccess":       false,
			"excludeTmpdirEnvVar": false,
			"excludeSlashTmp":     false,
		}
	}
}

func threadSettingsActivePermissionProfile(value *string) any {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil
	}
	return map[string]any{
		"id":      strings.TrimSpace(*value),
		"extends": nil,
	}
}

func threadSettingsCollaborationMode(settings *Settings) map[string]any {
	out := map[string]any{}
	if settings != nil {
		out = cloneAnyMap(settings.CollaborationMode)
	}
	if out == nil {
		out = map[string]any{}
	}
	if mode := strings.TrimSpace(stringFromMap(out, "mode")); mode == "" {
		out["mode"] = string(ModeKindDefault)
	}
	if _, ok := out["settings"]; !ok || out["settings"] == nil {
		model := ""
		var effort *string
		if settings != nil {
			model = settings.Model
			effort = cloneString(settings.Effort)
		}
		out["settings"] = map[string]any{
			"model":                  model,
			"reasoning_effort":       effort,
			"developer_instructions": nil,
		}
	}
	return out
}

type SettingsUpdatedNotification struct {
	ThreadID       string   `json:"threadId"`
	ThreadSettings Settings `json:"threadSettings"`
}

type ShellCommandParams struct {
	ThreadID string `json:"threadId"`
	Command  string `json:"command"`
}

func (p *ShellCommandParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidThreadExtraRequest)
	}
	if strings.TrimSpace(p.Command) == "" {
		return fmt.Errorf("%w: command is required", ErrInvalidThreadExtraRequest)
	}
	return nil
}

type ShellCommandResponse struct{}

type BackgroundTerminalsCleanParams struct {
	ThreadID string `json:"threadId"`
}

func (p *BackgroundTerminalsCleanParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidThreadExtraRequest)
	}
	return nil
}

type BackgroundTerminalsCleanResponse struct{}

type BackgroundTerminalsListParams struct {
	ThreadID string  `json:"threadId"`
	Cursor   *string `json:"cursor,omitempty"`
	Limit    *uint32 `json:"limit,omitempty"`
}

func (p *BackgroundTerminalsListParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidThreadExtraRequest)
	}
	return nil
}

type BackgroundTerminal struct {
	ItemID     string   `json:"itemId"`
	ProcessID  string   `json:"processId"`
	Command    string   `json:"command"`
	CWD        string   `json:"cwd"`
	OSPID      *uint32  `json:"osPid,omitempty"`
	CPUPercent *float64 `json:"cpuPercent,omitempty"`
	RSSKB      *uint64  `json:"rssKb,omitempty"`
}

type BackgroundTerminalUpdateParams struct {
	ThreadID   string   `json:"threadId"`
	ProcessID  string   `json:"processId"`
	OSPID      *uint32  `json:"osPid,omitempty"`
	CPUPercent *float64 `json:"cpuPercent,omitempty"`
	RSSKB      *uint64  `json:"rssKb,omitempty"`
}

func (p *BackgroundTerminalUpdateParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidThreadExtraRequest)
	}
	if strings.TrimSpace(p.ProcessID) == "" {
		return fmt.Errorf("%w: processId is required", ErrInvalidThreadExtraRequest)
	}
	return nil
}

type BackgroundTerminalUpdateResponse struct {
	Updated bool `json:"updated"`
}

type BackgroundTerminalsListResponse struct {
	Data       []BackgroundTerminal `json:"data"`
	NextCursor *string              `json:"nextCursor"`
}

func (r *BackgroundTerminalsListResponse) MarshalJSON() ([]byte, error) {
	data := append([]BackgroundTerminal(nil), r.Data...)
	if data == nil {
		data = []BackgroundTerminal{}
	}
	return json.Marshal(struct {
		Data       []BackgroundTerminal `json:"data"`
		NextCursor *string              `json:"nextCursor"`
	}{
		Data:       data,
		NextCursor: cloneString(r.NextCursor),
	})
}

type BackgroundTerminalsTerminateParams struct {
	ThreadID  string `json:"threadId"`
	ProcessID string `json:"processId"`
}

func (p *BackgroundTerminalsTerminateParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidThreadExtraRequest)
	}
	if strings.TrimSpace(p.ProcessID) == "" {
		return fmt.Errorf("%w: processId is required", ErrInvalidThreadExtraRequest)
	}
	return nil
}

type BackgroundTerminalsTerminateResponse struct {
	Terminated bool `json:"terminated"`
}

func PaginateBackgroundTerminals(terminals []BackgroundTerminal, cursor *string, limit *uint32) ([]BackgroundTerminal, *string, error) {
	start := 0
	if cursor != nil && *cursor != "" {
		index := sort.Search(len(terminals), func(i int) bool {
			return terminals[i].ProcessID >= *cursor
		})
		if index < len(terminals) && terminals[index].ProcessID == *cursor {
			start = index + 1
		}
	}
	pageSize := len(terminals)
	if limit != nil {
		requested := int(*limit)
		if requested < 1 {
			requested = 1
		}
		if requested < pageSize {
			pageSize = requested
		}
	}
	if start >= len(terminals) {
		return []BackgroundTerminal{}, nil, nil
	}
	end := start + pageSize
	if end > len(terminals) {
		end = len(terminals)
	}
	page := append([]BackgroundTerminal(nil), terminals[start:end]...)
	var next *string
	if end < len(terminals) {
		value := terminals[end-1].ProcessID
		next = &value
	}
	return page, next, nil
}

type ThreadExtraService struct {
	goals           *GoalStore
	mu              sync.Mutex
	settings        map[string]Settings
	terminals       map[string][]BackgroundTerminal
	terminalCancels map[string]context.CancelFunc
	shellHistory    map[string][]string
}

func NewThreadExtraService() *ThreadExtraService {
	return &ThreadExtraService{
		goals:           NewGoalStore(),
		settings:        map[string]Settings{},
		terminals:       map[string][]BackgroundTerminal{},
		terminalCancels: map[string]context.CancelFunc{},
		shellHistory:    map[string][]string{},
	}
}

func (s *ThreadExtraService) GoalStore() *GoalStore {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLocked()
	return s.goals
}

func (s *ThreadExtraService) ensureLocked() {
	if s.goals == nil {
		s.goals = NewGoalStore()
	}
	if s.settings == nil {
		s.settings = map[string]Settings{}
	}
	if s.terminals == nil {
		s.terminals = map[string][]BackgroundTerminal{}
	}
	if s.terminalCancels == nil {
		s.terminalCancels = map[string]context.CancelFunc{}
	}
	if s.shellHistory == nil {
		s.shellHistory = map[string][]string{}
	}
}

func (s *ThreadExtraService) SetGoal(params *GoalSetParams) (*GoalSetResponse, error) {
	return s.GoalStore().Set(params)
}

func (s *ThreadExtraService) GetGoal(params *GoalGetParams) (*GoalGetResponse, error) {
	return s.GoalStore().Get(params)
}

func (s *ThreadExtraService) ClearGoal(params *GoalClearParams) (*GoalClearResponse, error) {
	return s.GoalStore().Clear(params)
}

func (s *ThreadExtraService) UpdateSettings(params *SettingsUpdateParams) (*SettingsUpdateResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, fmt.Errorf("%w: service is nil", ErrInvalidThreadExtraRequest)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLocked()
	settings := s.settings[params.ThreadID]
	if params.CWD != nil {
		settings.CWD = *params.CWD
	}
	if params.ApprovalPolicy != nil {
		settings.ApprovalPolicy = *params.ApprovalPolicy
	}
	if params.ApprovalsReviewer != nil {
		settings.ApprovalsReviewer = *params.ApprovalsReviewer
	}
	if params.SandboxPolicy != nil {
		settings.SandboxPolicy = *params.SandboxPolicy
	}
	if params.Permissions != nil {
		settings.ActivePermissionProfile = cloneString(params.Permissions)
	}
	if params.Model != nil {
		settings.Model = *params.Model
	}
	if params.ServiceTier != nil && params.ServiceTier.Set {
		if params.ServiceTier.Value == nil {
			serviceTier := model.ServiceTierDefaultRequestValue
			settings.ServiceTier = &serviceTier
		} else {
			settings.ServiceTier = cloneString(params.ServiceTier.Value)
		}
	}
	if params.Effort != nil {
		settings.Effort = cloneString(params.Effort)
	}
	if params.Summary != nil {
		settings.Summary = cloneString(params.Summary)
	}
	if params.CollaborationMode != nil {
		settings.CollaborationMode = cloneAnyMap(params.CollaborationMode)
	}
	if params.MultiAgentMode != nil {
		settings.MultiAgentMode = *params.MultiAgentMode
	}
	if params.Personality != nil {
		settings.Personality = cloneString(params.Personality)
		settings.PersonalitySet = params.PersonalitySet
	}
	if params.RuntimeWorkspaceRoots != nil {
		settings.RuntimeWorkspaceRoots = append([]string(nil), params.RuntimeWorkspaceRoots...)
	}
	s.settings[params.ThreadID] = settings
	return &SettingsUpdateResponse{}, nil
}

func (s *ThreadExtraService) Settings(threadID string) *Settings {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLocked()
	settings := cloneSettings(s.settings[strings.TrimSpace(threadID)])
	return &settings
}

func cloneSettings(settings Settings) Settings {
	settings.ActivePermissionProfile = cloneString(settings.ActivePermissionProfile)
	settings.ServiceTier = cloneString(settings.ServiceTier)
	settings.Effort = cloneString(settings.Effort)
	settings.Summary = cloneString(settings.Summary)
	settings.CollaborationMode = cloneAnyMap(settings.CollaborationMode)
	settings.Personality = cloneString(settings.Personality)
	settings.RuntimeWorkspaceRoots = append([]string(nil), settings.RuntimeWorkspaceRoots...)
	return settings
}

func (s *ThreadExtraService) ShellCommand(params *ShellCommandParams) (*ShellCommandResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, fmt.Errorf("%w: service is nil", ErrInvalidThreadExtraRequest)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLocked()
	s.shellHistory[params.ThreadID] = append(s.shellHistory[params.ThreadID], params.Command)
	return &ShellCommandResponse{}, nil
}

func (s *ThreadExtraService) CleanBackgroundTerminals(params *BackgroundTerminalsCleanParams) (*BackgroundTerminalsCleanResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, fmt.Errorf("%w: service is nil", ErrInvalidThreadExtraRequest)
	}
	var cancels []context.CancelFunc
	s.mu.Lock()
	s.ensureLocked()
	for _, terminal := range s.terminals[params.ThreadID] {
		key := backgroundTerminalKey(params.ThreadID, terminal.ProcessID)
		if cancel := s.terminalCancels[key]; cancel != nil {
			cancels = append(cancels, cancel)
			delete(s.terminalCancels, key)
		}
	}
	delete(s.terminals, params.ThreadID)
	s.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	return &BackgroundTerminalsCleanResponse{}, nil
}

func (s *ThreadExtraService) ListBackgroundTerminals(params *BackgroundTerminalsListParams) (*BackgroundTerminalsListResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, fmt.Errorf("%w: service is nil", ErrInvalidThreadExtraRequest)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLocked()
	terminals := cloneBackgroundTerminals(s.terminals[params.ThreadID])
	sort.Slice(terminals, func(i int, j int) bool {
		return terminals[i].ProcessID < terminals[j].ProcessID
	})
	page, next, err := PaginateBackgroundTerminals(terminals, params.Cursor, params.Limit)
	if err != nil {
		return nil, err
	}
	return &BackgroundTerminalsListResponse{Data: page, NextCursor: next}, nil
}

func (s *ThreadExtraService) TerminateBackgroundTerminal(params *BackgroundTerminalsTerminateParams) (*BackgroundTerminalsTerminateResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, fmt.Errorf("%w: service is nil", ErrInvalidThreadExtraRequest)
	}
	s.mu.Lock()
	s.ensureLocked()
	terminals := s.terminals[params.ThreadID]
	filtered := terminals[:0]
	terminated := false
	var cancel context.CancelFunc
	for _, terminal := range terminals {
		if terminal.ProcessID == params.ProcessID {
			terminated = true
			key := backgroundTerminalKey(params.ThreadID, params.ProcessID)
			cancel = s.terminalCancels[key]
			delete(s.terminalCancels, key)
			continue
		}
		filtered = append(filtered, terminal)
	}
	s.terminals[params.ThreadID] = append([]BackgroundTerminal(nil), filtered...)
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return &BackgroundTerminalsTerminateResponse{Terminated: terminated}, nil
}

func (s *ThreadExtraService) AddBackgroundTerminal(threadID string, terminal *BackgroundTerminal) {
	s.AddBackgroundTerminalWithCancel(threadID, terminal, nil)
}

func (s *ThreadExtraService) AddBackgroundTerminalWithCancel(threadID string, terminal *BackgroundTerminal, cancel context.CancelFunc) {
	if s == nil || terminal == nil || strings.TrimSpace(threadID) == "" || strings.TrimSpace(terminal.ProcessID) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLocked()
	threadID = strings.TrimSpace(threadID)
	terminalCopy := *terminal
	terminalCopy = cloneBackgroundTerminal(&terminalCopy)
	terminalCopy.ItemID = strings.TrimSpace(terminalCopy.ItemID)
	terminalCopy.ProcessID = strings.TrimSpace(terminalCopy.ProcessID)
	key := backgroundTerminalKey(threadID, terminalCopy.ProcessID)
	if cancel != nil {
		s.terminalCancels[key] = cancel
	} else {
		delete(s.terminalCancels, key)
	}
	for i := range s.terminals[threadID] {
		if s.terminals[threadID][i].ProcessID == terminalCopy.ProcessID {
			s.terminals[threadID][i] = terminalCopy
			return
		}
	}
	s.terminals[threadID] = append(s.terminals[threadID], terminalCopy)
}

func (s *ThreadExtraService) UpdateBackgroundTerminal(params *BackgroundTerminalUpdateParams) (*BackgroundTerminalUpdateResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, fmt.Errorf("%w: service is nil", ErrInvalidThreadExtraRequest)
	}
	threadID := strings.TrimSpace(params.ThreadID)
	processID := strings.TrimSpace(params.ProcessID)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLocked()
	for i := range s.terminals[threadID] {
		if s.terminals[threadID][i].ProcessID != processID {
			continue
		}
		if params.OSPID != nil {
			s.terminals[threadID][i].OSPID = cloneUint32Ptr(params.OSPID)
		}
		if params.CPUPercent != nil {
			s.terminals[threadID][i].CPUPercent = cloneFloat64Ptr(params.CPUPercent)
		}
		if params.RSSKB != nil {
			s.terminals[threadID][i].RSSKB = cloneUint64PtrAppserver(params.RSSKB)
		}
		return &BackgroundTerminalUpdateResponse{Updated: true}, nil
	}
	return &BackgroundTerminalUpdateResponse{Updated: false}, nil
}

func (s *ThreadExtraService) RemoveBackgroundTerminal(threadID string, processID string) bool {
	if s == nil || strings.TrimSpace(threadID) == "" || strings.TrimSpace(processID) == "" {
		return false
	}
	s.mu.Lock()
	s.ensureLocked()
	threadID = strings.TrimSpace(threadID)
	processID = strings.TrimSpace(processID)
	terminals := s.terminals[threadID]
	filtered := terminals[:0]
	removed := false
	for _, terminal := range terminals {
		if terminal.ProcessID == processID {
			removed = true
			continue
		}
		filtered = append(filtered, terminal)
	}
	delete(s.terminalCancels, backgroundTerminalKey(threadID, processID))
	s.terminals[threadID] = append([]BackgroundTerminal(nil), filtered...)
	s.mu.Unlock()
	return removed
}

func backgroundTerminalKey(threadID string, processID string) string {
	return strings.TrimSpace(threadID) + "\x00" + strings.TrimSpace(processID)
}

func (s *ThreadExtraService) SetBackgroundTerminals(threadID string, terminals []BackgroundTerminal) {
	if s == nil || strings.TrimSpace(threadID) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLocked()
	s.terminals[threadID] = cloneBackgroundTerminals(terminals)
}

// CountBackgroundTerminals returns the number of background terminals still
// tracked for the thread (mirrors the Rust turn-completion running-process
// metric source).
func (s *ThreadExtraService) CountBackgroundTerminals(threadID string) int {
	threadID = strings.TrimSpace(threadID)
	if s == nil || threadID == "" {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLocked()
	return len(s.terminals[threadID])
}

func validGoalStatus(status GoalStatus) bool {
	switch status {
	case GoalActive, GoalPaused, GoalBlocked, GoalUsageLimited, GoalBudgetLimited, GoalComplete:
		return true
	default:
		return false
	}
}

func buildGoalFromSetParams(params *GoalSetParams, existing *Goal, now int64) (Goal, error) {
	if err := params.Validate(); err != nil {
		return Goal{}, err
	}
	threadID := strings.TrimSpace(params.ThreadID)
	goalID := ""
	objective := ""
	status := GoalActive
	var tokenBudget *int64
	var tokensUsed int64
	var timeUsedSeconds int64
	createdAt := now
	if existing != nil {
		goalID = strings.TrimSpace(existing.GoalID)
		objective = strings.TrimSpace(existing.Objective)
		status = existing.Status
		if status == "" {
			status = GoalActive
		}
		tokenBudget = cloneInt64(existing.TokenBudget)
		tokensUsed = existing.TokensUsed
		timeUsedSeconds = existing.TimeUsedSeconds
		createdAt = existing.CreatedAt
		if createdAt == 0 {
			createdAt = now
		}
	}
	if params.Objective != nil {
		objective = strings.TrimSpace(*params.Objective)
	}
	if objective == "" {
		return Goal{}, fmt.Errorf("%w: objective is required", ErrInvalidThreadExtraRequest)
	}
	if params.TokenBudgetSet || params.TokenBudget != nil {
		if params.TokenBudget != nil {
			tokenBudget = cloneInt64(params.TokenBudget)
		} else {
			// A null budget reset uses the configured maximum as the default
			// budget (mirrors Rust GoalTokenBudgetUpdate::Set(None) semantics).
			tokenBudget = cloneInt64(params.MaxGoalTokenBudget)
		}
	} else if existing == nil && tokenBudget == nil {
		// New goals default to the configured maximum token budget.
		tokenBudget = cloneInt64(params.MaxGoalTokenBudget)
	}
	if params.Status != nil {
		status = *params.Status
	}
	if tokenBudget != nil && params.MaxGoalTokenBudget != nil && *tokenBudget > *params.MaxGoalTokenBudget {
		return Goal{}, fmt.Errorf("%w: goal token budget %d exceeds the maximum allowed goal token budget of %d", ErrInvalidThreadExtraRequest, *tokenBudget, *params.MaxGoalTokenBudget)
	}
	if goalID == "" {
		goalID = uuid.NewString()
	}
	goal := Goal{
		ThreadID:        threadID,
		GoalID:          goalID,
		Objective:       objective,
		TokenBudget:     tokenBudget,
		TokensUsed:      tokensUsed,
		TimeUsedSeconds: timeUsedSeconds,
		Status:          status,
		CreatedAt:       createdAt,
		UpdatedAt:       now,
	}
	return applyGoalBudgetStatus(goal), nil
}

func applyGoalBudgetStatus(goal Goal) Goal {
	if goal.TokenBudget != nil && *goal.TokenBudget > 0 && goal.TokensUsed >= *goal.TokenBudget {
		goal.Status = GoalBudgetLimited
	}
	if goal.Status == "" {
		goal.Status = GoalActive
	}
	return goal
}

func goalFromRecord(record *session.Record) (*Goal, bool, error) {
	if record == nil || record.Metadata.Extra == nil {
		return nil, false, nil
	}
	raw, ok := record.Metadata.Extra[threadGoalExtraKey]
	if !ok || raw == nil {
		return nil, false, nil
	}
	goal, err := goalFromAny(raw)
	if err != nil {
		return nil, true, err
	}
	if goal.ThreadID == "" {
		goal.ThreadID = string(record.ID)
	}
	goal.ThreadID = strings.TrimSpace(goal.ThreadID)
	goal.GoalID = strings.TrimSpace(goal.GoalID)
	if goal.GoalID == "" {
		goal.GoalID = uuid.NewString()
	}
	goal.Objective = strings.TrimSpace(goal.Objective)
	goal.Status = GoalStatus(strings.TrimSpace(string(goal.Status)))
	if goal.Status == "" {
		goal.Status = GoalActive
	}
	if goal.Objective == "" || !validGoalStatus(goal.Status) {
		return nil, true, fmt.Errorf("%w: invalid stored goal", ErrInvalidThreadExtraRequest)
	}
	goal = applyGoalBudgetStatus(goal)
	return &goal, true, nil
}

func goalFromAny(raw any) (Goal, error) {
	switch value := raw.(type) {
	case Goal:
		return cloneGoal(value), nil
	case *Goal:
		if value == nil {
			return Goal{}, fmt.Errorf("%w: stored goal is nil", ErrInvalidThreadExtraRequest)
		}
		return cloneGoal(*value), nil
	case json.RawMessage:
		var goal Goal
		if err := json.Unmarshal(value, &goal); err != nil {
			return Goal{}, err
		}
		return goal, nil
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return Goal{}, err
		}
		var goal Goal
		if err := json.Unmarshal(data, &goal); err != nil {
			return Goal{}, err
		}
		return goal, nil
	}
}

func cloneGoal(goal Goal) Goal {
	goal.TokenBudget = cloneInt64(goal.TokenBudget)
	return goal
}

func goalRecordExtra(goal Goal) map[string]any {
	return map[string]any{
		"threadId":        strings.TrimSpace(goal.ThreadID),
		"goalId":          strings.TrimSpace(goal.GoalID),
		"objective":       strings.TrimSpace(goal.Objective),
		"status":          goal.Status,
		"tokenBudget":     cloneInt64(goal.TokenBudget),
		"tokensUsed":      goal.TokensUsed,
		"timeUsedSeconds": goal.TimeUsedSeconds,
		"createdAt":       goal.CreatedAt,
		"updatedAt":       goal.UpdatedAt,
	}
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneBackgroundTerminal(terminal *BackgroundTerminal) BackgroundTerminal {
	if terminal == nil {
		return BackgroundTerminal{}
	}
	cloned := *terminal
	cloned.OSPID = cloneUint32Ptr(cloned.OSPID)
	cloned.CPUPercent = cloneFloat64Ptr(cloned.CPUPercent)
	cloned.RSSKB = cloneUint64PtrAppserver(cloned.RSSKB)
	return cloned
}

func cloneBackgroundTerminals(terminals []BackgroundTerminal) []BackgroundTerminal {
	if len(terminals) == 0 {
		return nil
	}
	cloned := make([]BackgroundTerminal, 0, len(terminals))
	for i := range terminals {
		cloned = append(cloned, cloneBackgroundTerminal(&terminals[i]))
	}
	return cloned
}

func cloneUint32Ptr(value *uint32) *uint32 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneFloat64Ptr(value *float64) *float64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneUint64PtrAppserver(value *uint64) *uint64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneAnyMap(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
