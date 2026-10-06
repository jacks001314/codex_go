package telemetry

import "context"

// SkillInvocationEvent mirrors codex-otel's SkillInvocationEvent: metadata for a
// detected skill invocation that never includes skill contents, resource paths,
// or tool payloads (Rust #49689).
type SkillInvocationEvent struct {
	TurnID         string
	SkillName      string
	Scope          string
	PluginID       string
	InvocationType string
}

// SkillInvocation emits Rust's `codex.skill_invocation` log record. Detection
// belongs to the caller; an invocation does not imply successful completion.
func (t *SessionTelemetry) SkillInvocation(ctx context.Context, event SkillInvocationEvent) {
	if t == nil {
		return
	}
	fields := map[string]string{
		"turn.id":               event.TurnID,
		"skill.name":            event.SkillName,
		"skill.invocation_type": event.InvocationType,
	}
	if event.Scope != "" {
		fields["skill.scope"] = event.Scope
	}
	if event.PluginID != "" {
		fields["skill.plugin_id"] = event.PluginID
	}
	t.LogEvent(ctx, "codex.skill_invocation", fields, nil)
}
