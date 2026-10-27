package appserver

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/session"
	"codex_go/state"
	"codex_go/utils"
)

const guardianNativeMultiAgentThreadID = "123e4567-e89b-42d3-a456-426614174130"

// guardianNativeSendMessageCall is the stored shape of a native MAv2 call: the
// raw response item carries ciphertext arguments the reviewer must receive
// unchanged.
func guardianNativeSendMessageCall(namespace string, arguments string) session.Item {
	raw := map[string]any{
		"type":                    "function_call",
		"call_id":                 "call-send",
		"name":                    "send_message",
		"arguments":               arguments,
		"encrypted_function_args": []any{"message"},
	}
	if namespace != "" {
		raw["namespace"] = namespace
	}
	data, _ := json.Marshal(raw)
	item := session.Item{
		ID:        "call-send",
		Type:      "function_call",
		Role:      "assistant",
		Name:      "send_message",
		CallID:    "call-send",
		Text:      arguments,
		CreatedAt: fixedTime(),
		Metadata:  map[string]any{"rawResponseItem": json.RawMessage(data)},
	}
	if namespace != "" {
		item.Namespace = namespace
	}
	return item
}

func guardianNativeMultiAgentItems() []session.Item {
	now := fixedTime()
	return []session.Item{
		{ID: "user-1", Type: "message", Role: "user", Text: "Please deploy.", CreatedAt: now},
		{
			ID:        "agent-1",
			Type:      "agent_message",
			CreatedAt: now,
			Metadata:  map[string]any{"rawResponseItem": json.RawMessage(encryptedAgentMessageRaw("/root", "/root/worker", "sealed-reply"))},
		},
		guardianNativeSendMessageCall("team", `{"target":"worker","message":"opaque-parent-reply"}`),
		{ID: "user-2", Type: "message", Role: "user", Text: "Only staging.", CreatedAt: now},
	}
}

func guardianNativeTestProfile(namespace string) state.ContextProfile {
	profile := state.SynchronousContextProfile()
	profile.Transcript.Options.NativeMultiAgentNamespace = namespace
	return profile
}

// Mirrors Rust #57448: the configured namespace keeps spawn_agent,
// send_message, followup_task, and interrupt_agent calls whole and in order,
// while the text transcript never renders the ciphertext arguments.
func TestGuardianTranscriptPreservesNativeMultiAgentCallsLikeRust(t *testing.T) {
	router := guardianProfileTestRouter(t, guardianNativeMultiAgentItems())
	selection := router.guardianReviewTranscriptSelectionWithProfile(guardianProfileTestThreadID, guardianNativeTestProfile("team"))
	if len(selection.Entries) != 4 {
		t.Fatalf("entries = %#v, want user, native message, native call, user", selection.Entries)
	}
	call := selection.Entries[2]
	if len(call.Native) == 0 || !strings.Contains(string(call.Native), "encrypted_function_args") {
		t.Fatalf("native call = %#v, want the recorded function call", call)
	}
	if call.LineRole != "tool send_message call" {
		t.Fatalf("native call role = %q", call.LineRole)
	}
	for _, line := range state.RenderGuardianTranscript(selection.Entries, state.GuardianTranscriptFormatJSON) {
		if strings.Contains(line, "opaque-parent-reply") {
			t.Fatalf("native call arguments reached the text transcript: %q", line)
		}
	}
	parts, costs, err := state.BuildPromptPartsWithSectionCosts(
		state.Action{Type: "command", Command: "ls", CWD: t.TempDir()},
		selection.Entries,
		state.BuildPromptOptions{TranscriptFormat: state.GuardianTranscriptFormatJSON},
	)
	if err != nil {
		t.Fatalf("BuildPromptPartsWithSectionCosts: %v", err)
	}
	nativeIndex := -1
	for index, part := range parts {
		if len(part.Native) > 0 && strings.Contains(string(part.Native), "encrypted_function_args") {
			nativeIndex = index
			break
		}
	}
	if nativeIndex < 0 || nativeIndex+1 >= len(parts) {
		t.Fatalf("parts = %#v, want the native call followed by its evidence output", parts)
	}
	paired := parts[nativeIndex+1]
	if len(paired.Native) == 0 || !strings.Contains(string(paired.Native), state.GuardianNativeEvidenceOutputText) ||
		!strings.Contains(string(paired.Native), `"function_call_output"`) || !strings.Contains(string(paired.Native), `"call-send"`) {
		t.Fatalf("paired output = %#v, want the evidence-only function_call_output", paired)
	}
	var transcriptCost state.GuardianPromptSectionCost
	for _, cost := range costs {
		if cost.Section == "conversation_transcript" {
			transcriptCost = cost
		}
	}
	// The transcript section also carries the rendered text lines, so the
	// native pair must contribute at least its own bytes to the section cost.
	if transcriptCost.TextBytes < len(call.Native)+len(paired.Native) || transcriptCost.TextBytes == len(call.Native) {
		t.Fatalf("transcript cost = %d, want the native call and its paired output", transcriptCost.TextBytes)
	}
}

// Mirrors Rust #57448 gating: only MAv2 calls in the configured namespace under
// an enabled profile stay native; everything else keeps the text transcript.
func TestGuardianTranscriptNativeMultiAgentCallsRequireConfiguredNamespaceLikeRust(t *testing.T) {
	for _, test := range []struct {
		name      string
		namespace string
		call      session.Item
	}{
		{name: "feature off", call: guardianNativeSendMessageCall("team", `{"target":"worker"}`)},
		{name: "namespace mismatch", namespace: "collaboration", call: guardianNativeSendMessageCall("team", `{"target":"worker"}`)},
		{
			name:      "other tool",
			namespace: "team",
			call: session.Item{
				ID: "call-exec", Type: "function_call", Role: "assistant", Name: "exec_command",
				Namespace: "team", CallID: "call-exec", Text: `{"cmd":"ls"}`, CreatedAt: fixedTime(),
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := fixedTime()
			items := []session.Item{
				{ID: "user-1", Type: "message", Role: "user", Text: "Please deploy.", CreatedAt: now},
				test.call,
			}
			router := guardianProfileTestRouter(t, items)
			selection := router.guardianReviewTranscriptSelectionWithProfile(guardianProfileTestThreadID, guardianNativeTestProfile(test.namespace))
			for _, entry := range selection.Entries {
				if len(entry.Native) > 0 {
					t.Fatalf("entry kept native evidence: %#v", entry)
				}
			}
			found := false
			for _, entry := range selection.Entries {
				if strings.Contains(entry.LineRole, test.call.Name) && entry.Text != "" {
					found = true
				}
			}
			if !found {
				t.Fatalf("entries = %#v, want the text transcript entry", selection.Entries)
			}
		})
	}
}

// Mirrors Rust #57448: the default namespace used by MAv2 calls without an
// explicit one is `functions`.
func TestGuardianNativeMultiAgentCallDefaultsToFunctionsLikeRust(t *testing.T) {
	item := guardianNativeSendMessageCall("", `{"target":"worker"}`)
	if _, ok := guardianNativeMultiAgentCall(&item, "team"); ok {
		t.Fatal("implicit functions namespace matched a configured team namespace")
	}
	native, ok := guardianNativeMultiAgentCall(&item, "functions")
	if !ok || !strings.Contains(string(native), `"send_message"`) {
		t.Fatalf("guardianNativeMultiAgentCall(functions) = %q, %v", native, ok)
	}
}

// Mirrors Rust #57448: a native call over the tool-entry cap becomes the whole
// omission notice at the same position.
func TestGuardianTranscriptOmitsOversizedNativeMultiAgentCallLikeRust(t *testing.T) {
	profile := guardianNativeTestProfile("team")
	profile.Transcript.EntryLimits.ToolTokens = 10
	router := guardianProfileTestRouter(t, guardianNativeMultiAgentItems())
	selection := router.guardianReviewTranscriptSelectionWithProfile(guardianProfileTestThreadID, profile)
	call := selection.Entries[2]
	if len(call.Native) != 0 || call.Text != state.GuardianTranscriptOmissionNotice || call.LineRole != "tool send_message call" {
		t.Fatalf("oversized native call = %#v, want the whole omission notice", call)
	}
}

// Mirrors Rust #57448: the paired evidence-only output counts toward retention,
// so a call that fits alone is still dropped when the pair exceeds the budget.
func TestGuardianTranscriptNativeMultiAgentBudgetIncludesPairedOutputLikeRust(t *testing.T) {
	call := guardianNativeSendMessageCall("team", `{"target":"worker","message":"evidence"}`)
	native, ok := guardianNativeMultiAgentCall(&call, "team")
	if !ok {
		t.Fatal("call was not recognized as native evidence")
	}
	callOnlyTokens := utils.ApproxTokenCount(string(native))
	pairTokens := state.GuardianNativeFunctionCallTokens(native)
	if pairTokens <= callOnlyTokens {
		t.Fatalf("paired tokens = %d, want a charge above the call alone (%d)", pairTokens, callOnlyTokens)
	}
	now := fixedTime()
	items := []session.Item{
		{ID: "user-1", Type: "message", Role: "user", Text: "Please deploy.", CreatedAt: now},
		call,
	}
	router := guardianProfileTestRouter(t, items)

	callOnly := guardianNativeTestProfile("team")
	callOnly.Retention.MaxToolTranscriptTokens = callOnlyTokens
	if selection := router.guardianReviewTranscriptSelectionWithProfile(guardianProfileTestThreadID, callOnly); selection.OmissionNote != state.GuardianTranscriptOmissionNotice {
		t.Fatalf("selection = %#v, want the call and its paired output evicted together", selection)
	}

	withPair := guardianNativeTestProfile("team")
	withPair.Retention.MaxToolTranscriptTokens = pairTokens
	selection := router.guardianReviewTranscriptSelectionWithProfile(guardianProfileTestThreadID, withPair)
	if len(selection.Entries) != 2 || len(selection.Entries[1].Native) == 0 || selection.OmissionNote != "" {
		t.Fatalf("selection = %#v, want the redundant pair retained", selection)
	}
}

// Mirrors Rust #57448's turn-scoped resolution: the namespace is preserved only
// for an enabled feature on a MultiAgent V2 turn.
func TestGuardianNativeMultiAgentNamespaceResolvesFeatureAndVersionLikeRust(t *testing.T) {
	for _, test := range []struct {
		name       string
		config     string
		version    string
		want       string
	}{
		{
			name:    "v2 enabled",
			config:  "[features]\nguardian_native_multi_agent_history = true\n[features.multi_agent_v2]\ntool_namespace = \"team\"\n",
			version: "v2",
			want:    "team",
		},
		{
			name:    "feature off",
			config:  "[features.multi_agent_v2]\ntool_namespace = \"team\"\n",
			version: "v2",
			want:    "",
		},
		{
			name:    "v1 turn",
			config:  "[features]\nguardian_native_multi_agent_history = true\n[features.multi_agent_v2]\ntool_namespace = \"team\"\n",
			version: "v1",
			want:    "",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			store := session.NewStore(t.TempDir())
			now := fixedTime()
			if err := store.Create(&session.Record{
				ID:        session.ThreadID(guardianNativeMultiAgentThreadID),
				SessionID: guardianNativeMultiAgentThreadID,
				CreatedAt: now,
				UpdatedAt: now,
				RecencyAt: now,
				Metadata:  session.Metadata{Source: "cli", ModelProvider: "openai", MultiAgentVersion: test.version},
			}); err != nil {
				t.Fatalf("store.Create: %v", err)
			}
			if err := os.WriteFile(config.ConfigPath(home), []byte(test.config), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}
			router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), Config: config.NewConfigService(home)})
			if got := router.guardianNativeMultiAgentNamespace(guardianNativeMultiAgentThreadID); got != test.want {
				t.Fatalf("guardianNativeMultiAgentNamespace() = %q, want %q", got, test.want)
			}
		})
	}
}