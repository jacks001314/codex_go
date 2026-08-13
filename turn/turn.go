package turn

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"codex_go/codexapi"
)

type Profile struct {
	BeforeFirstSamplingMS      uint64
	SamplingMS                 uint64
	BetweenSamplingOverheadMS  uint64
	ToolBlockingMS             uint64
	PendingIdleAfterSamplingMS uint64
	SamplingRequestCount       uint32
	SamplingRetryCount         uint32
	TotalMS                    uint64
}

type profilePhase string

const (
	phaseSampling     profilePhase = "sampling"
	phaseToolBlocking profilePhase = "tool_blocking"
)

type TimingState struct {
	mu             sync.Mutex
	startedAt      time.Time
	startedUnixMS  int64
	firstTokenAt   time.Time
	firstMessageAt time.Time
	profile        profileState
}

type profileState struct {
	startedAt                time.Time
	lastTransitionAt         time.Time
	activePhase              profilePhase
	seenSampling             bool
	beforeFirstSampling      time.Duration
	sampling                 time.Duration
	betweenSamplingOverhead  time.Duration
	toolBlocking             time.Duration
	pendingIdleAfterSampling time.Duration
	samplingRequestCount     uint32
	samplingRetryCount       uint32
	completed                *Profile
}

type TimingGuard struct {
	timing *TimingState
	phase  profilePhase
	active bool
}

func NewTimingState() *TimingState {
	return &TimingState{}
}

func (t *TimingState) MarkTurnStarted(now time.Time) int64 {
	if now.IsZero() {
		now = time.Now()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.startedAt = now
	t.startedUnixMS = now.UnixMilli()
	t.firstTokenAt = time.Time{}
	t.firstMessageAt = time.Time{}
	t.profile = profileState{startedAt: now, lastTransitionAt: now}
	return t.startedUnixMS
}

func (t *TimingState) StartedAtUnixSecs() (int64, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.startedUnixMS == 0 {
		return 0, false
	}
	return t.startedUnixMS / 1000, true
}

func (t *TimingState) CompletedAtAndDuration(now time.Time) (int64, int64, bool) {
	if now.IsZero() {
		now = time.Now()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.startedAt.IsZero() {
		return now.Unix(), 0, false
	}
	return now.Unix(), now.Sub(t.startedAt).Milliseconds(), true
}

func (t *TimingState) RecordTTFT(now time.Time) (time.Duration, bool) {
	if now.IsZero() {
		now = time.Now()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.startedAt.IsZero() || !t.firstTokenAt.IsZero() {
		return 0, false
	}
	t.firstTokenAt = now
	return now.Sub(t.startedAt), true
}

func (t *TimingState) RecordTTFM(now time.Time) (time.Duration, bool) {
	if now.IsZero() {
		now = time.Now()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.startedAt.IsZero() || !t.firstMessageAt.IsZero() {
		return 0, false
	}
	t.firstMessageAt = now
	return now.Sub(t.startedAt), true
}

func (t *TimingState) TimeToFirstToken() (time.Duration, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.startedAt.IsZero() || t.firstTokenAt.IsZero() {
		return 0, false
	}
	return t.firstTokenAt.Sub(t.startedAt), true
}

func (t *TimingState) BeginSampling(now time.Time) *TimingGuard {
	return t.beginPhase(phaseSampling, now)
}

func (t *TimingState) BeginToolBlocking(now time.Time) *TimingGuard {
	return t.beginPhase(phaseToolBlocking, now)
}

func (t *TimingState) RecordSamplingRetry() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.profile.completed == nil && !t.profile.startedAt.IsZero() {
		t.profile.samplingRetryCount++
	}
}

func (t *TimingState) CompleteProfile(now time.Time) Profile {
	if now.IsZero() {
		now = time.Now()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.profile.complete(now)
}

func (t *TimingState) beginPhase(phase profilePhase, now time.Time) *TimingGuard {
	if now.IsZero() {
		now = time.Now()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	active := t.profile.beginPhase(phase, now)
	return &TimingGuard{timing: t, phase: phase, active: active}
}

func (g *TimingGuard) Close() {
	g.CloseAt(time.Now())
}

func (g *TimingGuard) CloseAt(now time.Time) {
	if g == nil || !g.active {
		return
	}
	if now.IsZero() {
		now = time.Now()
	}
	g.timing.mu.Lock()
	defer g.timing.mu.Unlock()
	g.timing.profile.endPhase(now, g.phase)
	g.active = false
}

func (p *profileState) beginPhase(phase profilePhase, now time.Time) bool {
	if p.completed != nil || p.startedAt.IsZero() || p.activePhase != "" {
		return false
	}
	p.advance(now)
	if phase == phaseSampling {
		if p.seenSampling {
			p.betweenSamplingOverhead += p.pendingIdleAfterSampling
			p.pendingIdleAfterSampling = 0
		}
		p.seenSampling = true
		p.samplingRequestCount++
	}
	p.activePhase = phase
	return true
}

func (p *profileState) endPhase(now time.Time, phase profilePhase) {
	if p.completed != nil || p.activePhase != phase {
		return
	}
	p.advance(now)
	p.activePhase = ""
}

func (p *profileState) advance(now time.Time) {
	if p.lastTransitionAt.IsZero() {
		p.lastTransitionAt = now
		return
	}
	elapsed := now.Sub(p.lastTransitionAt)
	if elapsed < 0 {
		elapsed = 0
	}
	switch p.activePhase {
	case phaseSampling:
		p.sampling += elapsed
	case phaseToolBlocking:
		p.toolBlocking += elapsed
	default:
		if p.seenSampling {
			p.pendingIdleAfterSampling += elapsed
		} else {
			p.beforeFirstSampling += elapsed
		}
	}
	p.lastTransitionAt = now
}

func (p *profileState) complete(now time.Time) Profile {
	if p.completed != nil {
		return *p.completed
	}
	p.advance(now)
	profile := Profile{
		BeforeFirstSamplingMS:      durationMS(p.beforeFirstSampling),
		SamplingMS:                 durationMS(p.sampling),
		BetweenSamplingOverheadMS:  durationMS(p.betweenSamplingOverhead),
		ToolBlockingMS:             durationMS(p.toolBlocking),
		PendingIdleAfterSamplingMS: durationMS(p.pendingIdleAfterSampling),
		SamplingRequestCount:       p.samplingRequestCount,
		SamplingRetryCount:         p.samplingRetryCount,
		TotalMS:                    durationMS(now.Sub(p.startedAt)),
	}
	p.completed = &profile
	return profile
}

func durationMS(duration time.Duration) uint64 {
	if duration <= 0 {
		return 0
	}
	return uint64(duration.Milliseconds())
}

func cloneBoolPtr(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

type MetadataState struct {
	mu                           sync.RWMutex
	SessionID                    string
	ThreadID                     string
	ForkedFromThreadID           string
	ParentThreadID               string
	ParentTurnID                 string
	SubagentHeader               string
	SubagentKind                 string
	ThreadSource                 string
	TurnID                       string
	Sandbox                      string
	SandboxMode                  string
	Workspaces                   map[string]WorkspaceMetadata
	TurnStartedAtUnixMS          int64
	Extra                        map[string]string
	UserInputRequestedDuringTurn bool
}

type WorkspaceMetadata struct {
	AssociatedRemoteURLs map[string]string `json:"associated_remote_urls,omitempty"`
	LatestGitCommitHash  string            `json:"latest_git_commit_hash,omitempty"`
	HasChanges           *bool             `json:"has_changes,omitempty"`
}

func NewMetadataState(sessionID string, threadID string, turnID string) *MetadataState {
	return &MetadataState{
		SessionID:  sessionID,
		ThreadID:   threadID,
		TurnID:     turnID,
		Workspaces: map[string]WorkspaceMetadata{},
		Extra:      map[string]string{},
	}
}

func (m *MetadataState) SetTurnStartedAtUnixMS(value int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.TurnStartedAtUnixMS = value
}

func (m *MetadataState) MarkUserInputRequestedDuringTurn() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.UserInputRequestedDuringTurn = true
}

func (m *MetadataState) SetClientMetadata(values map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Extra = FilterClientMetadata(values)
}

func (m *MetadataState) WorkspaceKind() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.Extra["workspace_kind"]
}

func (m *MetadataState) AddWorkspace(path string, metadata WorkspaceMetadata) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Workspaces == nil {
		m.Workspaces = map[string]WorkspaceMetadata{}
	}
	m.Workspaces[path] = metadata
}

func (m *MetadataState) MetadataValue(model string, reasoningEffort string) map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()
	value := map[string]any{
		"session_id": m.SessionID,
		"thread_id":  m.ThreadID,
		"turn_id":    m.TurnID,
		"model":      model,
	}
	if reasoningEffort != "" {
		value["reasoning_effort"] = reasoningEffort
	}
	if m.ForkedFromThreadID != "" {
		value["forked_from_thread_id"] = m.ForkedFromThreadID
	}
	if m.ParentThreadID != "" {
		value["parent_thread_id"] = m.ParentThreadID
	}
	if m.ParentTurnID != "" {
		value["parent_turn_id"] = m.ParentTurnID
	}
	if m.SubagentHeader != "" {
		value["subagent_header"] = m.SubagentHeader
	}
	if m.SubagentKind != "" {
		value["subagent_kind"] = m.SubagentKind
	}
	if m.ThreadSource != "" {
		value["thread_source"] = m.ThreadSource
	}
	if m.Sandbox != "" {
		value["sandbox"] = m.Sandbox
	}
	if m.SandboxMode != "" {
		value["sandbox_mode"] = m.SandboxMode
	}
	if m.TurnStartedAtUnixMS != 0 {
		value["turn_started_at_unix_ms"] = m.TurnStartedAtUnixMS
	}
	if m.UserInputRequestedDuringTurn {
		value["user_input_requested_during_turn"] = true
	}
	if len(m.Workspaces) > 0 {
		value["workspaces"] = m.Workspaces
	}
	for key, extra := range m.Extra {
		value[key] = extra
	}
	return value
}

type ResponsesClientMetadataOptions struct {
	InstallationID             string
	SessionID                  string
	ThreadID                   string
	TurnID                     string
	WindowID                   string
	RequestKind                codexapi.ClientRequestKind
	ForkedFromThreadID         string
	ParentThreadID             string
	ParentTurnID               string
	SubagentHeader             string
	SubagentKind               string
	ThreadSource               string
	Sandbox                    string
	SandboxMode                string
	AutoReviewEnabled          *bool
	NodeReplAutoReviewRequired *bool
	NodeReplDisabled           *bool
	Extra                      map[string]string
	ResponsesAPIMetadata       map[string]string
	StartedAtMS                int64
	UseResponsesLite           bool
}

func BuildResponsesClientMetadata(options *ResponsesClientMetadataOptions) map[string]string {
	if options == nil {
		return nil
	}
	threadID := strings.TrimSpace(options.ThreadID)
	sessionID := strings.TrimSpace(options.SessionID)
	if sessionID == "" {
		sessionID = threadID
	}
	windowID := strings.TrimSpace(options.WindowID)
	if windowID == "" {
		windowID = threadID + ":1"
	}
	metadata := codexapi.NewClientMetadata(
		strings.TrimSpace(options.InstallationID),
		sessionID,
		threadID,
		windowID,
	)
	metadata.TurnID = strings.TrimSpace(options.TurnID)
	metadata.RequestKind = options.RequestKind
	if metadata.RequestKind == "" {
		metadata.RequestKind = codexapi.ClientRequestTurn
	}
	metadata.ForkedFromThreadID = strings.TrimSpace(options.ForkedFromThreadID)
	metadata.ParentThreadID = strings.TrimSpace(options.ParentThreadID)
	metadata.ParentTurnID = strings.TrimSpace(options.ParentTurnID)
	metadata.SubagentHeader = strings.TrimSpace(options.SubagentHeader)
	metadata.SubagentKind = strings.TrimSpace(options.SubagentKind)
	metadata.ThreadSource = strings.TrimSpace(options.ThreadSource)
	metadata.Sandbox = strings.TrimSpace(options.Sandbox)
	metadata.SandboxMode = strings.TrimSpace(options.SandboxMode)
	metadata.AutoReviewEnabled = cloneBoolPtr(options.AutoReviewEnabled)
	metadata.NodeReplAutoReviewRequired = cloneBoolPtr(options.NodeReplAutoReviewRequired)
	metadata.NodeReplDisabled = cloneBoolPtr(options.NodeReplDisabled)
	metadata.TurnStartedAtUnixMS = options.StartedAtMS
	metadata.Extra = FilterClientMetadata(options.Extra)
	metadata.ResponsesAPIMetadata = cloneStringMap(options.ResponsesAPIMetadata)
	clientMetadata := metadata.ClientMetadata()
	if options.UseResponsesLite {
		clientMetadata["ws_request_header_x_openai_internal_codex_responses_lite"] = "true"
	}
	return compactStringMap(clientMetadata)
}

func MergeClientMetadata(base map[string]string, overlay map[string]string) map[string]string {
	if len(base) == 0 && len(overlay) == 0 {
		return nil
	}
	out := make(map[string]string, len(base)+len(overlay))
	for key, value := range FilterClientMetadata(base) {
		out[key] = value
	}
	for key, value := range FilterClientMetadata(overlay) {
		out[key] = value
	}
	return out
}

type BudgetReminderConfig struct {
	ThresholdTokens int64
	Template        string
}

type BudgetState struct {
	mu              sync.Mutex
	reminderClaimed bool
}

func NewBudgetState() *BudgetState {
	return &BudgetState{}
}

func (b *BudgetState) MaybeReminder(tokensUntilCompaction *int64, config *BudgetReminderConfig) (string, bool) {
	if b == nil || config == nil || tokensUntilCompaction == nil {
		return "", false
	}
	if *tokensUntilCompaction > config.ThresholdTokens {
		return "", false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.reminderClaimed {
		return "", false
	}
	b.reminderClaimed = true
	template := config.Template
	if template == "" {
		template = "Token budget remaining: {tokens_until_compaction}"
	}
	return stringsReplaceToken(template, *tokensUntilCompaction), true
}

func FilterClientMetadata(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	reserved := codexapi.ClientReservedMetadataKeys()
	for key, value := range values {
		if key == "" || reserved[strings.ToLower(key)] || len(key) > 64 || len(value) > 512 {
			continue
		}
		out[key] = value
	}
	return out
}

func compactStringMap(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func stringsReplaceToken(template string, tokens int64) string {
	return fmt.Sprintf("%s", replaceAll(template, "{tokens_until_compaction}", fmt.Sprintf("%d", tokens)))
}

func replaceAll(value string, old string, new string) string {
	for {
		next := ""
		index := -1
		for i := 0; i+len(old) <= len(value); i++ {
			if value[i:i+len(old)] == old {
				index = i
				break
			}
		}
		if index < 0 {
			return value
		}
		next = value[:index] + new + value[index+len(old):]
		value = next
	}
}
