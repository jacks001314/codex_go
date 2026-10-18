package appserver

import (
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	skillInjectionMetricName    = "codex.skill.injected"
	skillTurnDurationMetricName = "codex.skill.turn.duration_seconds"
)

// skillTurnMetricState tracks the plugins used by one turn so duration can be
// emitted once per plugin when the turn reaches a terminal state (Rust #40724).
type skillTurnMetricState struct {
	modelSlug       string
	reasoningEffort string
	startedAt       time.Time

	mu      sync.Mutex
	plugins map[string]struct{}
	failed  bool
}

func (r *RuntimeRouter) startSkillTurnMetrics(threadID string, turnID string, modelSlug string, reasoningEffort string, startedAt time.Time) {
	if r == nil || strings.TrimSpace(threadID) == "" || strings.TrimSpace(turnID) == "" {
		return
	}
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	state := &skillTurnMetricState{
		modelSlug:       firstNonEmpty(strings.TrimSpace(modelSlug), "unknown"),
		reasoningEffort: firstNonEmpty(strings.TrimSpace(reasoningEffort), "default"),
		startedAt:       startedAt,
		plugins:         map[string]struct{}{},
	}
	r.skillTurnMetricsMu.Lock()
	if r.skillTurnMetrics == nil {
		r.skillTurnMetrics = map[string]*skillTurnMetricState{}
	}
	r.skillTurnMetrics[skillTurnMetricKey(threadID, turnID)] = state
	r.skillTurnMetricsMu.Unlock()
}

func (r *RuntimeRouter) skillTurnMetricState(threadID string, turnID string) *skillTurnMetricState {
	if r == nil {
		return nil
	}
	r.skillTurnMetricsMu.Lock()
	defer r.skillTurnMetricsMu.Unlock()
	return r.skillTurnMetrics[skillTurnMetricKey(threadID, turnID)]
}

func (r *RuntimeRouter) recordSkillTurnPluginUsage(threadID string, turnID string, pluginID string) {
	state := r.skillTurnMetricState(threadID, turnID)
	if state == nil {
		return
	}
	pluginID = firstNonEmpty(strings.TrimSpace(pluginID), "unattributed")
	state.mu.Lock()
	state.plugins[pluginID] = struct{}{}
	state.mu.Unlock()
}

func (r *RuntimeRouter) markSkillTurnMetricsFailed(threadID string, turnID string) {
	state := r.skillTurnMetricState(threadID, turnID)
	if state == nil {
		return
	}
	state.mu.Lock()
	state.failed = true
	state.mu.Unlock()
}

// finishSkillTurnMetrics emits one duration histogram per plugin used by the
// turn. The state is removed even when no skills were invoked.
func (r *RuntimeRouter) finishSkillTurnMetrics(threadID string, turnID string, status TurnStatus) {
	if r == nil {
		return
	}
	key := skillTurnMetricKey(threadID, turnID)
	r.skillTurnMetricsMu.Lock()
	state := r.skillTurnMetrics[key]
	delete(r.skillTurnMetrics, key)
	r.skillTurnMetricsMu.Unlock()
	if state == nil {
		return
	}

	state.mu.Lock()
	plugins := make([]string, 0, len(state.plugins))
	for pluginID := range state.plugins {
		plugins = append(plugins, pluginID)
	}
	failed := state.failed
	state.mu.Unlock()
	if len(plugins) == 0 {
		return
	}
	sort.Strings(plugins)

	statusTag := string(status)
	switch status {
	case TurnStatusCompleted:
		statusTag = "completed"
	case TurnStatusFailed:
		statusTag = "error"
	case TurnStatusInterrupted:
		statusTag = "aborted"
	}
	if failed && status == TurnStatusCompleted {
		statusTag = "error"
	}
	durationSeconds := int(math.Ceil(time.Since(state.startedAt).Seconds()))
	if durationSeconds < 0 {
		durationSeconds = 0
	}
	metrics := r.services.SkillInjectionMetrics
	if metrics == nil {
		return
	}
	for _, pluginID := range plugins {
		tags := map[string]string{
			"plugin_id":        sanitizeMetricTagValue(pluginID),
			"model_slug":       sanitizeMetricTagValue(state.modelSlug),
			"reasoning_effort": sanitizeMetricTagValue(state.reasoningEffort),
			"status":           statusTag,
		}
		recordSkillTurnDuration(metrics, durationSeconds, tags)
	}
}

func recordSkillTurnDuration(metrics interface {
	Counter(string, int, map[string]string)
	RecordDuration(string, time.Duration, map[string]string)
}, durationSeconds int, tags map[string]string) {
	switch sink := metrics.(type) {
	case interface {
		Histogram(string, int, map[string]string)
	}:
		sink.Histogram(skillTurnDurationMetricName, durationSeconds, tags)
	case interface {
		RecordDurationSecondsWithDescription(string, string, time.Duration, map[string]string)
	}:
		sink.RecordDurationSecondsWithDescription(
			skillTurnDurationMetricName,
			"Skill turn duration in seconds.",
			time.Duration(durationSeconds)*time.Second,
			tags,
		)
	default:
		metrics.RecordDuration(skillTurnDurationMetricName, time.Duration(durationSeconds)*time.Second, tags)
	}
}

func (r *RuntimeRouter) skillTurnReasoningEffort(threadID string, turnID string, fallback string) string {
	if state := r.skillTurnMetricState(threadID, turnID); state != nil && strings.TrimSpace(state.reasoningEffort) != "" {
		return state.reasoningEffort
	}
	return firstNonEmpty(strings.TrimSpace(fallback), "default")
}

func (r *RuntimeRouter) skillTurnModelSlug(threadID string, turnID string, fallback string) string {
	if state := r.skillTurnMetricState(threadID, turnID); state != nil && strings.TrimSpace(state.modelSlug) != "" {
		return state.modelSlug
	}
	return firstNonEmpty(strings.TrimSpace(fallback), "unknown")
}

// emitSkillInjectionMetric records the Rust #40724 dimensions for explicit and
// implicit skill invocations.
func (r *RuntimeRouter) emitSkillInjectionMetric(threadID string, turnID string, skillName string, status string, pluginID string, modelID string, reasoningEffort string, invokeType string) {
	if r == nil || r.services.SkillInjectionMetrics == nil {
		return
	}
	r.services.SkillInjectionMetrics.Counter(skillInjectionMetricName, 1, map[string]string{
		"status":           status,
		"skill":            sanitizeMetricTagValue(skillName),
		"invoke_type":      invokeType,
		"plugin_id":        sanitizeMetricTagValue(firstNonEmpty(strings.TrimSpace(pluginID), "unattributed")),
		"model_slug":       sanitizeMetricTagValue(r.skillTurnModelSlug(threadID, turnID, modelID)),
		"reasoning_effort": sanitizeMetricTagValue(r.skillTurnReasoningEffort(threadID, turnID, reasoningEffort)),
	})
}

func skillTurnMetricKey(threadID string, turnID string) string {
	return strings.TrimSpace(threadID) + "\x00" + strings.TrimSpace(turnID)
}
