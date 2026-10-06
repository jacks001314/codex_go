package telemetry

import (
	"context"
	"testing"
	"time"
)

type recordingLogSink struct {
	records []OTLPLogRecord
}

func (s *recordingLogSink) Emit(record OTLPLogRecord) {
	s.records = append(s.records, record)
}

func logRecordFields(record OTLPLogRecord) map[string]string {
	fields := map[string]string{}
	for _, tag := range record.Attributes {
		fields[tag.Key] = tag.Value
	}
	return fields
}

// TestSkillInvocationEmitsLogWithoutPayloadsLikeRust mirrors Rust #49689: the
// codex.skill_invocation log carries skill/turn metadata and the session
// identity, never skill contents or resource paths.
func TestSkillInvocationEmitsLogWithoutPayloadsLikeRust(t *testing.T) {
	sink := &recordingLogSink{}
	session := NewSessionTelemetry(SessionTelemetryMetadata{
		ConversationID: "thread-1",
		AccountID:      "acct-1",
		Model:          "gpt-5.1",
		AppVersion:     "0.1.0",
		Originator:     "codex_cli_rs",
	})
	session.Logs = sink
	session.Clock = func() time.Time { return time.Unix(1700000000, 0) }
	session.SkillInvocation(context.Background(), SkillInvocationEvent{
		TurnID:         "turn-1",
		SkillName:      "build-skill",
		Scope:          "repo",
		PluginID:       "sample@openai-curated",
		InvocationType: SkillInvocationTypeExplicit,
	})
	if len(sink.records) != 1 {
		t.Fatalf("records = %#v", sink.records)
	}
	record := sink.records[0]
	if record.Target != OtelLogOnlyTarget || record.SeverityText != "INFO" {
		t.Fatalf("record = %#v", record)
	}
	fields := logRecordFields(record)
	want := map[string]string{
		"event.name":            "codex.skill_invocation",
		"conversation.id":       "thread-1",
		"turn.id":               "turn-1",
		"skill.name":            "build-skill",
		"skill.scope":           "repo",
		"skill.plugin_id":       "sample@openai-curated",
		"skill.invocation_type": "explicit",
		"model":                 "gpt-5.1",
		"user.account_id":       "acct-1",
		"app.version":           "0.1.0",
		"originator":            "codex_cli_rs",
	}
	for key, value := range want {
		if fields[key] != value {
			t.Fatalf("field %q = %q, want %q (record = %#v)", key, fields[key], value, fields)
		}
	}
	if _, present := fields["skill.contents"]; present {
		t.Fatalf("skill contents leaked: %#v", fields)
	}

	// An implicit invocation without scope or plugin omits those fields.
	sink.records = nil
	session.SkillInvocation(context.Background(), SkillInvocationEvent{
		TurnID:         "turn-2",
		SkillName:      "exec-skill",
		InvocationType: SkillInvocationTypeImplicit,
	})
	if len(sink.records) != 1 {
		t.Fatalf("records = %#v", sink.records)
	}
	fields = logRecordFields(sink.records[0])
	if _, present := fields["skill.scope"]; present {
		t.Fatalf("scope should be omitted: %#v", fields)
	}
	if _, present := fields["skill.plugin_id"]; present {
		t.Fatalf("plugin id should be omitted: %#v", fields)
	}
	if fields["skill.invocation_type"] != "implicit" {
		t.Fatalf("invocation type = %q", fields["skill.invocation_type"])
	}
}
