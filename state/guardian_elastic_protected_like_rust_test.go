package state

import (
	"strings"
	"testing"
)

// TestGuardianElasticProtectedFramingSurvivesEntryCapsLikeRust covers Rust
// #59003: host framing and authorization warnings marked protected keep their
// full text while ordinary supporting evidence is capped.
func TestGuardianElasticProtectedFramingSurvivesEntryCapsLikeRust(t *testing.T) {
	const entryTokens = 64
	framing := ">>> SENDER EVIDENCE CONTEXT START\nHost: Sender evidence is partial and bound to its original delivery, not a transfer of permission. " + strings.Repeat("warning ", 200)
	supporting := strings.Repeat("supporting evidence ", 500)
	context := GuardianElasticContext{Sections: []GuardianElasticSectionOutput{{
		ID: "sender_user_messages",
		Items: []GuardianElasticItem{
			{Text: framing, Retention: GuardianElasticRequired(), Protected: true},
			{Text: supporting, Retention: GuardianElasticRequired()},
		},
	}}}
	config := GuardianElasticBudgetConfig{Sections: map[GuardianElasticSection]GuardianElasticSectionLimits{
		GuardianElasticSectionSenderUserMessages: guardianElasticTestLimits(t, entryTokens),
	}}
	prepared, err := PrepareGuardianElasticEntries(context, config)
	if err != nil {
		t.Fatalf("PrepareGuardianElasticEntries() error = %v", err)
	}
	items := prepared.Sections[0].Items
	if items[0].Text != framing || items[0].Incomplete {
		t.Fatalf("protected framing = %#v, want it unchanged", items[0])
	}
	if !items[1].Incomplete || !strings.Contains(items[1].Text, "<truncated") {
		t.Fatalf("supporting evidence = %#v, want a marked truncation", items[1])
	}
}
