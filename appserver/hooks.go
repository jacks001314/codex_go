package appserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrInvalidHook = errors.New("invalid hook")

type HookEventName string

const (
	HookEventPreToolUse        HookEventName = "preToolUse"
	HookEventPermissionRequest HookEventName = "permissionRequest"
	HookEventPostToolUse       HookEventName = "postToolUse"
	HookEventPreCompact        HookEventName = "preCompact"
	HookEventPostCompact       HookEventName = "postCompact"
	HookEventSessionStart      HookEventName = "sessionStart"
	HookEventSessionEnd        HookEventName = "sessionEnd"
	HookEventUserPromptSubmit  HookEventName = "userPromptSubmit"
	HookEventSubagentStart     HookEventName = "subagentStart"
	HookEventSubagentStop      HookEventName = "subagentStop"
	HookEventStop              HookEventName = "stop"
	HookEventInterrupt         HookEventName = "interrupt"
)

type HookHandlerType string

const (
	HookHandlerCommand HookHandlerType = "command"
	HookHandlerPrompt  HookHandlerType = "prompt"
	HookHandlerAgent   HookHandlerType = "agent"
	HookHandlerMCPTool HookHandlerType = "mcp_tool"
)

type HookExecutionMode string

const (
	HookExecutionSync  HookExecutionMode = "sync"
	HookExecutionAsync HookExecutionMode = "async"
)

type HookScope string

const (
	HookScopeThread HookScope = "thread"
	HookScopeTurn   HookScope = "turn"
)

type HookSource string

const (
	HookSourceSystem             HookSource = "system"
	HookSourceUser               HookSource = "user"
	HookSourceProject            HookSource = "project"
	HookSourceMDM                HookSource = "mdm"
	HookSourceSessionFlags       HookSource = "sessionFlags"
	HookSourcePlugin             HookSource = "plugin"
	HookSourceCloudRequirements  HookSource = "cloudRequirements"
	HookSourceCloudManagedConfig HookSource = "cloudManagedConfig"
	HookSourceLegacyConfigFile   HookSource = "legacyManagedConfigFile"
	HookSourceLegacyConfigMDM    HookSource = "legacyManagedConfigMdm"
	HookSourceUnknown            HookSource = "unknown"
)

type HookTrustStatus string

const (
	HookTrustManaged   HookTrustStatus = "managed"
	HookTrustUntrusted HookTrustStatus = "untrusted"
	HookTrustTrusted   HookTrustStatus = "trusted"
	HookTrustModified  HookTrustStatus = "modified"
)

type HookRunStatus string

const (
	HookRunRunning   HookRunStatus = "running"
	HookRunCompleted HookRunStatus = "completed"
	HookRunFailed    HookRunStatus = "failed"
	HookRunBlocked   HookRunStatus = "blocked"
	HookRunStopped   HookRunStatus = "stopped"
)

type HookOutputEntryKind string

const (
	HookOutputWarning  HookOutputEntryKind = "warning"
	HookOutputStop     HookOutputEntryKind = "stop"
	HookOutputFeedback HookOutputEntryKind = "feedback"
	HookOutputContext  HookOutputEntryKind = "context"
	HookOutputError    HookOutputEntryKind = "error"
)

type HookOutputEntry struct {
	Kind HookOutputEntryKind `json:"kind"`
	Text string              `json:"text"`
}

type HookPromptFragment struct {
	Text      string `json:"text"`
	HookRunID string `json:"hookRunId"`
}

type HookRunSummary struct {
	ID            string            `json:"id"`
	EventName     HookEventName     `json:"eventName"`
	HandlerType   HookHandlerType   `json:"handlerType"`
	ExecutionMode HookExecutionMode `json:"executionMode"`
	Scope         HookScope         `json:"scope"`
	SourcePath    string            `json:"sourcePath"`
	Source        HookSource        `json:"source"`
	DisplayOrder  int64             `json:"displayOrder"`
	Status        HookRunStatus     `json:"status"`
	StatusMessage *string           `json:"statusMessage"`
	StartedAt     int64             `json:"startedAt"`
	CompletedAt   *int64            `json:"completedAt"`
	DurationMS    *int64            `json:"durationMs"`
	Entries       []HookOutputEntry `json:"entries"`
}

func (r *HookRunSummary) MarshalJSON() ([]byte, error) {
	entries := append([]HookOutputEntry(nil), r.Entries...)
	if entries == nil {
		entries = []HookOutputEntry{}
	}
	return json.Marshal(struct {
		ID            string            `json:"id"`
		EventName     HookEventName     `json:"eventName"`
		HandlerType   HookHandlerType   `json:"handlerType"`
		ExecutionMode HookExecutionMode `json:"executionMode"`
		Scope         HookScope         `json:"scope"`
		SourcePath    string            `json:"sourcePath"`
		Source        HookSource        `json:"source"`
		DisplayOrder  int64             `json:"displayOrder"`
		Status        HookRunStatus     `json:"status"`
		StatusMessage *string           `json:"statusMessage"`
		StartedAt     int64             `json:"startedAt"`
		CompletedAt   *int64            `json:"completedAt"`
		DurationMS    *int64            `json:"durationMs"`
		Entries       []HookOutputEntry `json:"entries"`
	}{
		ID:            r.ID,
		EventName:     r.EventName,
		HandlerType:   r.HandlerType,
		ExecutionMode: r.ExecutionMode,
		Scope:         r.Scope,
		SourcePath:    r.SourcePath,
		Source:        r.Source,
		DisplayOrder:  r.DisplayOrder,
		Status:        r.Status,
		StatusMessage: r.StatusMessage,
		StartedAt:     r.StartedAt,
		CompletedAt:   r.CompletedAt,
		DurationMS:    r.DurationMS,
		Entries:       entries,
	})
}

type HookRunStartedNotification struct {
	ThreadID string         `json:"threadId"`
	TurnID   *string        `json:"turnId"`
	Run      HookRunSummary `json:"run"`
}

type HookRunCompletedNotification struct {
	ThreadID string         `json:"threadId"`
	TurnID   *string        `json:"turnId"`
	Run      HookRunSummary `json:"run"`
}

type HookMetadata struct {
	Key           string            `json:"key"`
	EventName     HookEventName     `json:"eventName"`
	HandlerType   HookHandlerType   `json:"handlerType"`
	ExecutionMode HookExecutionMode `json:"executionMode"`
	Matcher       *string           `json:"matcher"`
	Command       *string           `json:"command"`
	// MCP tool hooks (Rust #38705) target an MCP server tool with an
	// argument template expanded against the hook event input.
	Server        *string        `json:"server,omitempty"`
	Tool          *string        `json:"tool,omitempty"`
	Input         map[string]any `json:"input,omitempty"`
	TimeoutSec    int64          `json:"timeoutSec"`
	StatusMessage *string        `json:"statusMessage"`
	// AdditionalContextLimit mirrors Rust HookMetadata.additional_context_limit
	// (app-server hooks_list contract): nil means the default 2,500-token
	// spilling threshold; 0 disables spilling.
	AdditionalContextLimit *int64            `json:"additionalContextLimit,omitempty"`
	SourcePath             string            `json:"sourcePath"`
	Source                 HookSource        `json:"source"`
	PluginID               *string           `json:"pluginId"`
	DisplayOrder           int64             `json:"displayOrder"`
	Enabled                bool              `json:"enabled"`
	IsManaged              bool              `json:"isManaged"`
	CurrentHash            string            `json:"currentHash"`
	TrustStatus            HookTrustStatus   `json:"trustStatus"`
	BypassTrust            bool              `json:"-"`
	Env                    map[string]string `json:"-"`
}

func (m *HookMetadata) Validate() error {
	if m == nil {
		return fmt.Errorf("%w: metadata is nil", ErrInvalidHook)
	}
	if strings.TrimSpace(m.Key) == "" {
		return fmt.Errorf("%w: key is required", ErrInvalidHook)
	}
	if strings.TrimSpace(string(m.EventName)) == "" {
		return fmt.Errorf("%w: eventName is required", ErrInvalidHook)
	}
	if strings.TrimSpace(string(m.HandlerType)) == "" {
		return fmt.Errorf("%w: handlerType is required", ErrInvalidHook)
	}
	if strings.TrimSpace(m.SourcePath) == "" {
		return fmt.Errorf("%w: sourcePath is required", ErrInvalidHook)
	}
	return nil
}

type HookErrorInfo struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

type HookListEntry struct {
	CWD                string          `json:"cwd"`
	Hooks              []HookMetadata  `json:"hooks"`
	Warnings           []string        `json:"warnings"`
	Errors             []HookErrorInfo `json:"errors"`
	RequiredLoadErrors []string        `json:"requiredLoadErrors,omitempty"`
}

func (e *HookListEntry) MarshalJSON() ([]byte, error) {
	hooks := append([]HookMetadata(nil), e.Hooks...)
	if hooks == nil {
		hooks = []HookMetadata{}
	}
	warnings := append([]string(nil), e.Warnings...)
	if warnings == nil {
		warnings = []string{}
	}
	errors := append([]HookErrorInfo(nil), e.Errors...)
	if errors == nil {
		errors = []HookErrorInfo{}
	}
	requiredLoadErrors := append([]string(nil), e.RequiredLoadErrors...)
	return json.Marshal(struct {
		CWD                string          `json:"cwd"`
		Hooks              []HookMetadata  `json:"hooks"`
		Warnings           []string        `json:"warnings"`
		Errors             []HookErrorInfo `json:"errors"`
		RequiredLoadErrors []string        `json:"requiredLoadErrors,omitempty"`
	}{
		CWD:                e.CWD,
		Hooks:              hooks,
		Warnings:           warnings,
		Errors:             errors,
		RequiredLoadErrors: requiredLoadErrors,
	})
}

type HookListParams struct {
	CWDs []string `json:"cwds,omitempty"`
}

type HookListResponse struct {
	Data []HookListEntry `json:"data"`
}

func (r *HookListResponse) MarshalJSON() ([]byte, error) {
	data := append([]HookListEntry(nil), r.Data...)
	if data == nil {
		data = []HookListEntry{}
	}
	return json.Marshal(struct {
		Data []HookListEntry `json:"data"`
	}{Data: data})
}

type HookRegistry struct {
	mu      sync.Mutex
	entries map[string]*HookListEntry
	runs    map[string]HookRunSummary
	now     func() time.Time
}

func NewHookRegistry() *HookRegistry {
	return &HookRegistry{
		entries: map[string]*HookListEntry{},
		runs:    map[string]HookRunSummary{},
		now:     time.Now,
	}
}

func (r *HookRegistry) SetClock(clock func() time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if clock == nil {
		r.now = time.Now
		return
	}
	r.now = clock
}

func (r *HookRegistry) Add(cwd string, metadata HookMetadata) error {
	if err := metadata.Validate(); err != nil {
		return err
	}
	if metadata.ExecutionMode == "" {
		// Rust 3aae5d885b: hooks/list reports executionMode with "sync" as the
		// default for compatibility.
		metadata.ExecutionMode = HookExecutionSync
	}
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return fmt.Errorf("%w: cwd is required", ErrInvalidHook)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.entries[cwd]
	if entry == nil {
		entry = &HookListEntry{CWD: cwd}
		r.entries[cwd] = entry
	}
	replaced := false
	for i := range entry.Hooks {
		if entry.Hooks[i].Key == metadata.Key {
			entry.Hooks[i] = cloneMetadata(metadata)
			replaced = true
			break
		}
	}
	if !replaced {
		entry.Hooks = append(entry.Hooks, cloneMetadata(metadata))
	}
	sortHooks(entry.Hooks)
	return nil
}

func (r *HookRegistry) AddWarning(cwd string, warning string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.ensureEntryLocked(cwd)
	entry.Warnings = append(entry.Warnings, warning)
}

func (r *HookRegistry) AddError(cwd string, err HookErrorInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.ensureEntryLocked(cwd)
	entry.Errors = append(entry.Errors, err)
}

func (r *HookRegistry) List(params *HookListParams) *HookListResponse {
	if params == nil {
		params = &HookListParams{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	filter := map[string]bool{}
	for _, cwd := range params.CWDs {
		if strings.TrimSpace(cwd) != "" {
			filter[strings.TrimSpace(cwd)] = true
		}
	}
	keys := make([]string, 0, len(r.entries))
	for key := range r.entries {
		if len(filter) > 0 && !filter[key] {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	data := make([]HookListEntry, 0, len(keys))
	for _, key := range keys {
		data = append(data, cloneEntry(*r.entries[key]))
	}
	return &HookListResponse{Data: data}
}

func (r *HookRegistry) Start(threadID string, turnID *string, metadata HookMetadata, mode HookExecutionMode, scope HookScope) (*HookRunStartedNotification, error) {
	if err := metadata.Validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(threadID) == "" {
		return nil, fmt.Errorf("%w: threadId is required", ErrInvalidHook)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	startedAt := r.now().UTC().UnixMilli()
	run := HookRunSummary{
		ID:            metadata.Key + "-" + fmt.Sprint(startedAt),
		EventName:     metadata.EventName,
		HandlerType:   metadata.HandlerType,
		ExecutionMode: mode,
		Scope:         scope,
		SourcePath:    metadata.SourcePath,
		Source:        metadata.Source,
		DisplayOrder:  metadata.DisplayOrder,
		Status:        HookRunRunning,
		StatusMessage: cloneString(metadata.StatusMessage),
		StartedAt:     startedAt,
		Entries:       []HookOutputEntry{},
	}
	r.runs[run.ID] = run
	return &HookRunStartedNotification{ThreadID: threadID, TurnID: cloneString(turnID), Run: run}, nil
}

func (r *HookRegistry) Complete(threadID string, turnID *string, runID string, status HookRunStatus, entries []HookOutputEntry, message *string) (*HookRunCompletedNotification, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, ok := r.runs[runID]
	if !ok {
		return nil, fmt.Errorf("%w: unknown run %q", ErrInvalidHook, runID)
	}
	completedAt := r.now().UTC().UnixMilli()
	duration := completedAt - run.StartedAt
	run.Status = status
	run.StatusMessage = cloneString(message)
	run.CompletedAt = &completedAt
	run.DurationMS = &duration
	run.Entries = append([]HookOutputEntry(nil), entries...)
	r.runs[runID] = run
	return &HookRunCompletedNotification{ThreadID: threadID, TurnID: cloneString(turnID), Run: run}, nil
}

func (r *HookRegistry) ensureEntryLocked(cwd string) *HookListEntry {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		cwd = "."
	}
	entry := r.entries[cwd]
	if entry == nil {
		entry = &HookListEntry{CWD: cwd}
		r.entries[cwd] = entry
	}
	return entry
}

func sortHooks(hooks []HookMetadata) {
	sort.SliceStable(hooks, func(i int, j int) bool {
		if hooks[i].DisplayOrder == hooks[j].DisplayOrder {
			return hooks[i].Key < hooks[j].Key
		}
		return hooks[i].DisplayOrder < hooks[j].DisplayOrder
	})
}

func cloneEntry(entry HookListEntry) HookListEntry {
	entry.Hooks = append([]HookMetadata(nil), entry.Hooks...)
	for i := range entry.Hooks {
		entry.Hooks[i] = cloneMetadata(entry.Hooks[i])
	}
	entry.Warnings = append([]string(nil), entry.Warnings...)
	entry.Errors = append([]HookErrorInfo(nil), entry.Errors...)
	entry.RequiredLoadErrors = append([]string(nil), entry.RequiredLoadErrors...)
	return entry
}

func cloneMetadata(metadata HookMetadata) HookMetadata {
	metadata.Matcher = cloneString(metadata.Matcher)
	metadata.Command = cloneString(metadata.Command)
	metadata.StatusMessage = cloneString(metadata.StatusMessage)
	metadata.PluginID = cloneString(metadata.PluginID)
	if metadata.Env != nil {
		env := make(map[string]string, len(metadata.Env))
		for key, value := range metadata.Env {
			env[key] = value
		}
		metadata.Env = env
	}
	return metadata
}
