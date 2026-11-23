package state

import (
	"encoding/json"
	"strings"
	"testing"
)

// Rust #62829: an Elastic sender snapshot fits as many recent exchanges as the
// configured entry budget admits (and may exceed the fixed three) while marking
// evidence that no longer fits.
func TestGuardianSenderMessagesElasticRenderingLikeRust(t *testing.T) {
	fragment := GuardianSenderMessages{Source: "source-thread", Delivery: "delivery-1"}
	for index := 0; index < 6; index++ {
		user := "instruction-" + strings.Repeat("u", index+1)
		assistant := RootMessage{Kind: RootMessageAssistant, Text: "reply-" + strings.Repeat("a", index+1)}
		fragment.Messages = append(fragment.Messages, GuardianSenderExchange{User: &user, Assistant: &assistant})
	}
	big := &GuardianElasticBudgetConfig{Sections: map[GuardianElasticSection]GuardianElasticSectionLimits{
		GuardianElasticSectionSenderUserMessages: {MinTotalSectionTokens: 4000, MaxSectionEntryTokens: 4000},
	}}
	rendered := fragment.RenderElastic(big)
	if !strings.Contains(rendered, "instruction-u") {
		t.Fatalf("elastic snapshot dropped the oldest exchange: %q", rendered)
	}
	if got := strings.Count(rendered, "user: "); got < 4 {
		t.Fatalf("elastic snapshot carried %d exchanges, want more than the fixed three: %q", got, rendered)
	}
	bytes := GuardianElasticSenderTextEntryBytes(4000)
	encoded, err := json.Marshal(strings.TrimPrefix(strings.TrimSuffix(rendered, guardianSenderMessagesEnd), guardianSenderMessagesStart))
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded)-2 > bytes {
		t.Fatalf("elastic snapshot body = %d bytes, want at most %d", len(encoded)-2, bytes)
	}

	// A budget that cannot fit the framing renders no fragment at all.
	tiny := &GuardianElasticBudgetConfig{Sections: map[GuardianElasticSection]GuardianElasticSectionLimits{
		GuardianElasticSectionSenderUserMessages: {MinTotalSectionTokens: 1, MaxSectionEntryTokens: 1},
	}}
	if got := fragment.RenderElastic(tiny); got != "" {
		t.Fatalf("tiny budget rendered %q, want an omitted fragment", got)
	}

	// The fixed window still carries only the newest three exchanges.
	fixed := fragment.Render()
	if !strings.Contains(fixed, "Up to three recent") {
		t.Fatalf("fixed snapshot lost its header: %q", fixed)
	}
	if strings.Contains(fixed, "user: instruction-u\n") {
		t.Fatalf("fixed snapshot kept the oldest exchange: %q", fixed)
	}
	if !strings.Contains(fixed, "user: instruction-uuuuuu") {
		t.Fatalf("fixed snapshot lost the newest exchange: %q", fixed)
	}
}
