package appserver

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/session"
	"codex_go/turn"
)

func TestMultiAgentRoleSnapshotMatchesRustFragmentHash(t *testing.T) {
	configured := "Configured root guidance."
	role, ok := resolveMultiAgentRoleInstructions(&configured, nil, "bundled", nil, false)
	if !ok {
		t.Fatal("configured role was dropped")
	}
	if role.Marked || role.render() != configured {
		t.Fatalf("configured role = %#v, want verbatim unmarked text", role)
	}
	if got, want := role.fingerprint(), session.TextWorldStateFragmentHash("developer", configured); got != want {
		t.Fatalf("configured fingerprint = %q, want %q", got, want)
	}

	catalog := "Catalog root guidance."
	catalogRole, ok := resolveMultiAgentRoleInstructions(nil, &catalog, "bundled", nil, false)
	if !ok {
		t.Fatal("catalog role was dropped")
	}
	if !catalogRole.Marked || !strings.HasPrefix(catalogRole.render(), multiAgentRoleOpenMarker) || !strings.HasSuffix(catalogRole.render(), multiAgentRoleCloseMarker) {
		t.Fatalf("catalog role = %#v, want marked text", catalogRole)
	}
	if got, want := catalogRole.fingerprint(), session.TextWorldStateFragmentHash("developer", catalogRole.render()); got != want {
		t.Fatalf("catalog fingerprint = %q, want %q", got, want)
	}
}

func TestMultiAgentModeSnapshotWireShapesMatchRust(t *testing.T) {
	cases := []struct {
		name string
		mode multiAgentModeValue
		want string
	}{
		{name: "none", mode: multiAgentModeValue{Kind: "none"}, want: `{"mode":null}`},
		{name: "proactive", mode: multiAgentModeValue{Kind: "proactive"}, want: `{"mode":"proactive"}`},
		{name: "explicit", mode: multiAgentModeValue{Kind: "explicitRequestOnly"}, want: `{"mode":"explicitRequestOnly"}`},
		{name: "custom", mode: multiAgentModeValue{Kind: "custom", Text: "policy"}, want: `{"mode":{"custom":"policy"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := json.Marshal(multiAgentModeSnapshot{Mode: tc.mode.rawMessage()})
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			if string(encoded) != tc.want {
				t.Fatalf("snapshot = %s, want %s", encoded, tc.want)
			}
		})
	}

	withHint, err := json.Marshal(multiAgentModeSnapshot{Mode: multiAgentModeValue{Kind: "custom", Text: "policy"}.rawMessage(), UsageHintHash: "abc"})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	const wantWithHint = `{"mode":{"custom":"policy"},"usage_hint_hash":"abc"}`
	if string(withHint) != wantWithHint {
		t.Fatalf("snapshot with hint = %s, want %s", withHint, wantWithHint)
	}
}

func TestMultiAgentModeSnapshotDecodesRustAndLegacyShapes(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want decodedMultiAgentMode
	}{
		{name: "absent", raw: ``, want: decodedMultiAgentMode{State: worldStateAbsent}},
		{name: "null mode", raw: `{"mode":null}`, want: decodedMultiAgentMode{State: worldStateKnown, Kind: "none"}},
		{name: "proactive", raw: `{"mode":"proactive"}`, want: decodedMultiAgentMode{State: worldStateKnown, Kind: "proactive"}},
		{name: "custom", raw: `{"mode":{"custom":"policy"},"usage_hint_hash":"abc"}`, want: decodedMultiAgentMode{State: worldStateKnown, Kind: "custom", Text: "policy", UsageHintHash: "abc"}},
		{name: "legacy", raw: `{"mode":"custom","text":"policy"}`, want: decodedMultiAgentMode{State: worldStateKnown, Kind: "custom", Text: "policy"}},
		{name: "unknown", raw: `{"mode":42}`, want: decodedMultiAgentMode{State: worldStateKnown, Kind: "none"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := decodeMultiAgentModeSnapshot(json.RawMessage(tc.raw)); got != tc.want {
				t.Fatalf("decode = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestMultiAgentUsageHintPersistsBareHashLikeRust(t *testing.T) {
	const threadID = "thread-multi-agent-usage-hint"
	router := newMultiAgentWorldStateTestRouter(t, threadID, "root_agent_usage_hint_text = \"Configured root guidance.\"\n")
	cfg := router.effectiveConfigForSessionTelemetry()
	item, err := router.multiAgentUsageHintInputItem(threadID, cfg)
	if err != nil {
		t.Fatalf("multiAgentUsageHintInputItem() error = %v", err)
	}
	texts := messageInputTextsForRole([]any{item}, "developer")
	if len(texts) != 1 || texts[0] != "Configured root guidance." {
		t.Fatalf("usage hint texts = %#v", texts)
	}
	if !inputItemsContainKindAndText([]any{item}, multiAgentUsageHintKind, "Configured root guidance.") {
		t.Fatalf("usage hint item = %#v, want kind %q", item, multiAgentUsageHintKind)
	}

	record, err := router.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("threadRecord() error = %v", err)
	}
	state, err := session.DecodeWorldState(record.Metadata.WorldState)
	if err != nil {
		t.Fatalf("DecodeWorldState() error = %v", err)
	}
	wantHash, _ := json.Marshal(session.TextWorldStateFragmentHash("developer", "Configured root guidance."))
	if string(state.MultiAgentUsageHint) != string(wantHash) {
		t.Fatalf("usage hint snapshot = %s, want bare hash %s", state.MultiAgentUsageHint, wantHash)
	}

	again, err := router.multiAgentUsageHintInputItem(threadID, cfg)
	if err != nil || again != nil {
		t.Fatalf("unchanged usage hint re-emitted: %#v, error = %v", again, err)
	}
}

func TestMultiAgentModeReemitsAfterUsageHintMigrationLikeRust(t *testing.T) {
	const threadID = "thread-multi-agent-mode-migration"
	router := newMultiAgentWorldStateTestRouter(t, threadID, "")
	cfg := router.effectiveConfigForSessionTelemetry()
	record, err := router.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("threadRecord() error = %v", err)
	}
	state, err := session.DecodeWorldState(record.Metadata.WorldState)
	if err != nil {
		t.Fatalf("DecodeWorldState() error = %v", err)
	}
	state.MultiAgentMode = json.RawMessage(`{"mode":"proactive","usage_hint_hash":"stale"}`)
	record.Metadata.WorldState, err = session.EncodeWorldState(state)
	if err != nil {
		t.Fatalf("EncodeWorldState() error = %v", err)
	}
	if err := router.runtimeSaveThreadRecord(record); err != nil {
		t.Fatalf("runtimeSaveThreadRecord() error = %v", err)
	}

	mode := string(MultiAgentModeProactive)
	params := &turn.TurnStartParams{ThreadID: threadID, MultiAgentMode: &mode}
	item, err := router.multiAgentModeInputItem(threadID, cfg, params)
	if err != nil {
		t.Fatalf("multiAgentModeInputItem() error = %v", err)
	}
	if !inputItemsContainKindAndText([]any{item}, multiAgentModeInstructionsKind, "Proactive multi-agent delegation is active.") {
		t.Fatalf("mode item = %#v, want re-emitted proactive mode", item)
	}
	again, err := router.multiAgentModeInputItem(threadID, cfg, params)
	if err != nil || again != nil {
		t.Fatalf("unchanged mode re-emitted: %#v, error = %v", again, err)
	}
}

func newMultiAgentWorldStateTestRouter(t *testing.T, threadID string, extraConfig string) *RuntimeRouter {
	t.Helper()
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	record := &session.Record{ID: session.ThreadID(threadID), SessionID: threadID, CreatedAt: now, UpdatedAt: now, RecencyAt: now}
	if err := store.Create(record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	home := t.TempDir()
	configText := "[features.multi_agent_v2]\nenabled = true\n" + extraConfig
	if err := os.WriteFile(config.ConfigPath(home), []byte(configText), 0o600); err != nil {
		t.Fatalf("WriteFile config error = %v", err)
	}
	return NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
	})
}
