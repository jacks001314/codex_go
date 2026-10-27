package appserver

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codex_go/mcp"
	"codex_go/model"
	"codex_go/session"
)

// newPredictionMCPForkRouter seeds a live parent thread whose history carries
// (or omits) an inherited Responses Lite `additional_tools` catalog.
func newPredictionMCPForkRouter(t *testing.T, model string, items []session.Item, extra map[string]any) (*RuntimeRouter, string) {
	t.Helper()
	home := t.TempDir()
	store := session.NewStore(home)
	now := time.Now().UTC()
	record := &session.Record{
		ID:        "thread-prediction-parent",
		SessionID: "thread-prediction-parent",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{CWD: home, Model: model, Extra: cloneAnyMap(extra)},
		Items:     append([]session.Item(nil), items...),
	}
	if err := store.Create(record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), ThreadStatus: NewThreadStatusManager()})
	t.Cleanup(func() { _ = router.Close() })
	if err := router.threads.RetainLiveThread(store, record); err != nil {
		t.Fatalf("RetainLiveThread() error = %v", err)
	}
	return router, string(record.ID)
}

func predictionMCPForkItems() []session.Item {
	// The recorded catalog keeps its request shape in Raw, which is what
	// session.InputItemFromItem replays into a history request.
	raw := []byte(`{"id":"at-parent","type":"additional_tools","role":"developer","tools":[{"type":"namespace","name":"functions","description":"Built-in tools","tools":[{"type":"custom","name":"exec","description":"Run a command with MCP helpers."}]},{"type":"function","name":"mcp__echo_service__echo"}]}`)
	return []session.Item{
		{ID: "at-parent", Type: "additional_tools", Role: "developer", CreatedAt: time.Now().UTC(), Raw: raw},
		{ID: "user-parent", Type: "message", Role: "user", Text: "Start with the echo service.", CreatedAt: time.Now().UTC()},
	}
}

func handlePredictionMCPFork(t *testing.T, router *RuntimeRouter, threadID string, disableMCP bool) *ThreadForkResponse {
	t.Helper()
	response := router.Handle(requestWithParams(t, IntID(2), MethodThreadFork, ThreadForkParams{
		ThreadID:                         threadID,
		Ephemeral:                        true,
		ExperimentalPredictionMode:       true,
		ExperimentalDisablePredictionMcp: disableMCP,
	}))
	if response.Error != nil {
		t.Fatalf("thread/fork error = %+v", response.Error)
	}
	result, ok := response.Result.(*ThreadForkResponse)
	if !ok || result.Thread == nil {
		t.Fatalf("thread/fork result = %#v", response.Result)
	}
	return result
}

func loadEphemeralForkRecord(t *testing.T, router *RuntimeRouter, forkID string) *session.Record {
	t.Helper()
	record, ok := router.threads.EphemeralRecord(session.ThreadID(forkID), true)
	if !ok || record == nil {
		t.Fatalf("ephemeral fork record %q is missing", forkID)
	}
	return record
}

func mcpUnavailableNoticeCount(record *session.Record) int {
	if record == nil {
		return 0
	}
	count := 0
	for i := range record.Items {
		if record.Items[i].Type == "message" && record.Items[i].Role == "developer" &&
			strings.Contains(record.Items[i].Text, "MCP tools are unavailable for this request.") {
			count++
		}
	}
	return count
}

// Rust #57287: `experimentalDisablePredictionMcp` is only meaningful with
// prediction mode, and the error names both flags.
func TestPredictionForkRejectsMCPDisableWithoutPredictionModeLikeRust(t *testing.T) {
	params := ThreadForkParams{ThreadID: "thread-1", ExperimentalDisablePredictionMcp: true}
	if err := params.Validate(); err == nil {
		t.Fatal("Validate() accepted MCP disablement without prediction mode")
	} else if err.Error() != "`experimentalDisablePredictionMcp` requires `experimentalPredictionMode: true`" {
		t.Fatalf("Validate() error = %q", err.Error())
	}

	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(session.NewStore(t.TempDir()))})
	t.Cleanup(func() { _ = router.Close() })
	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadFork, params))
	if response.Error == nil || response.Error.Message != "`experimentalDisablePredictionMcp` requires `experimentalPredictionMode: true`" {
		t.Fatalf("thread/fork error = %+v", response.Error)
	}
}

// Rust #57287: MCP is disabled only for a prediction fork that inherits an
// `additional_tools` catalog from a Responses Lite parent, and the fork records
// the developer notice that MCP tools are unavailable.
func TestPredictionForkDisablesMCPOnlyForLiteInheritedCatalogLikeRust(t *testing.T) {
	tests := []struct {
		name         string
		model        string
		items        []session.Item
		disableMCP   bool
		wantDisabled bool
	}{
		{name: "lite parent with inherited catalog", model: "gpt-6.1-sol", items: predictionMCPForkItems(), disableMCP: true, wantDisabled: true},
		{name: "non-lite parent", model: "gpt-5.5", items: predictionMCPForkItems(), disableMCP: true},
		{name: "no inherited catalog", model: "gpt-6.1-sol", items: []session.Item{{Type: "message", Role: "user", Text: "hi"}}, disableMCP: true},
		{name: "flag unset", model: "gpt-6.1-sol", items: predictionMCPForkItems()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router, threadID := newPredictionMCPForkRouter(t, test.model, test.items, nil)
			result := handlePredictionMCPFork(t, router, threadID, test.disableMCP)
			record := loadEphemeralForkRecord(t, router, result.Thread.ID)
			if got := router.threadMCPServersDisabled(result.Thread.ID); got != test.wantDisabled {
				t.Fatalf("threadMCPServersDisabled = %v, want %v", got, test.wantDisabled)
			}
			notices := mcpUnavailableNoticeCount(record)
			if test.wantDisabled {
				if notices != 1 {
					t.Fatalf("MCP-unavailable notices = %d, want 1 (%#v)", notices, record.Items)
				}
				if !runtimeSessionRecordHasAdditionalTools(record) {
					t.Fatalf("fork lost the inherited parent declarations: %#v", record.Items)
				}
				last := record.Items[len(record.Items)-1]
				if last.Data["kind"] != predictionMCPUnavailableNoticeKind || last.Data["hiddenFromThread"] != true {
					t.Fatalf("notice metadata = %#v", last.Data)
				}
			} else if notices != 0 {
				t.Fatalf("unexpected MCP-unavailable notice in %#v", record.Items)
			}
		})
	}
}

// Rust #57287: the notice is gated on `prediction_preserve_parent_tools`; the
// marker still empties the MCP projection when the feature is off.
func TestPredictionForkMCPNoticeRequiresPreserveFeatureLikeRust(t *testing.T) {
	router, threadID := newPredictionMCPForkRouter(t, "gpt-6.1-sol", predictionMCPForkItems(), map[string]any{
		"config": map[string]any{"features.prediction_preserve_parent_tools": false},
	})
	result := handlePredictionMCPFork(t, router, threadID, true)
	if !router.threadMCPServersDisabled(result.Thread.ID) {
		t.Fatal("MCP disablement marker is missing")
	}
	record := loadEphemeralForkRecord(t, router, result.Thread.ID)
	if notices := mcpUnavailableNoticeCount(record); notices != 0 {
		t.Fatalf("MCP-unavailable notices = %d, want 0 when the feature is off", notices)
	}
}

// Rust #57287 / core mcp.rs `McpServersDisabled`: the projection carries no
// servers, apps, or connector selections while the parent keeps its own.
func TestPredictionForkMCPProjectionEmptyLikeRust(t *testing.T) {
	router, threadID := newPredictionMCPForkRouter(t, "gpt-6.1-sol", predictionMCPForkItems(), nil)
	values := map[string]any{
		"mcp_servers": map[string]any{"echo_service": map[string]any{"command": "echo"}},
	}
	home := t.TempDir()
	parent := router.runtimeMCPConfigForThread(mcp.NewMCPServerContributionContext(threadID, nil), values, home, nil, nil)
	if parent == nil || len(parent.Servers) == 0 {
		t.Fatalf("parent projection servers = %#v, want the configured server", parent)
	}

	result := handlePredictionMCPFork(t, router, threadID, true)
	fork := router.runtimeMCPConfigForThread(mcp.NewMCPServerContributionContext(result.Thread.ID, nil), values, home, nil, nil)
	if fork == nil {
		t.Fatal("fork projection is nil")
	}
	if len(fork.Servers) != 0 || fork.AppsEnabled || len(fork.ConnectorIDs) != 0 {
		t.Fatalf("fork projection = %#v, want no servers/apps/connectors", fork)
	}
}

// Rust #57287 world_state.rs: inside the inherited window the fork does not
// re-declare tools; a new window re-derives the current catalog.
func TestIncrementalToolCatalogPreservesParentDeclarationsLikeRust(t *testing.T) {
	router, _ := newResponsesLiteDeclarationRouter(t, "thread-preserve")
	created := time.Now().UTC()
	definitions := incrementalCatalogLikeRust("Run a command.")
	instructions := "Use the available tools to help the user."
	if start, err := router.incrementalToolCatalogForTurn("thread-preserve", nil, definitions, instructions, "turn-1", created); err != nil || !start.Active {
		t.Fatalf("window start result = %#v, err = %v", start, err)
	}
	history := incrementalHistoryItems(t, router, "thread-preserve")
	refreshed := incrementalCatalogLikeRust("Run a command with MCP helpers.",
		map[string]any{"type": "function", "name": "mcp__echo_service__echo"})

	preserved, err := router.incrementalToolCatalogForTurnWithModelMessages(
		"thread-preserve", history, refreshed, instructions, "gpt-5.4", "turn-2", created, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if !preserved.Active || preserved.Prefix || len(preserved.Items) != 0 || len(preserved.SessionItems) != 0 {
		t.Fatalf("preserved turn = %#v, want no declarations inside the inherited window", preserved)
	}

	refreshedResult, err := router.incrementalToolCatalogForTurnWithModelMessages(
		"thread-preserve", history, refreshed, instructions, "gpt-5.4", "turn-3", created, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if !incrementalResultDeclaresCatalog(t, refreshedResult) {
		t.Fatalf("unpreserved turn = %#v, want the changed catalog", refreshedResult)
	}

	if err := router.resetContextWindow("thread-preserve", "turn-4"); err != nil {
		t.Fatalf("resetContextWindow error = %v", err)
	}
	replacementHistory := incrementalHistoryItems(t, router, "thread-preserve")
	replacement, err := router.incrementalToolCatalogForTurnWithModelMessages(
		"thread-preserve", replacementHistory, refreshed, instructions, "gpt-5.4", "turn-5", created, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if !replacement.Active || !replacement.Prefix || len(replacement.Items) == 0 {
		t.Fatalf("new window = %#v, want a re-declared catalog", replacement)
	}
	if !incrementalResultDeclaresCatalog(t, replacement) {
		t.Fatalf("new window = %#v, want a re-declared catalog", replacement)
	}
}

func incrementalResultDeclaresCatalog(t *testing.T, result incrementalToolCatalogResult) bool {
	t.Helper()
	for _, item := range result.Items {
		if incrementalInputType(t, item) == "additional_tools" {
			return true
		}
	}
	return false
}

// Rust #57287 thread_sample.rs: a sample that disables MCP inherits the
// parent's declarations, sends no new catalog, and appends the notice.
func TestThreadSampleDisablesMCPWithoutRedeclaringToolsLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home)
	record := &session.Record{
		ID: "thread-sample-mcp",
		Metadata: session.Metadata{
			CWD:          home,
			Model:        "gpt-6.1-sol",
			RolloutTurns: []session.TurnSnapshot{{ID: "turn-latest", Status: "completed"}},
		},
		Items: predictionMCPForkItems(),
	}
	if err := store.Create(record); err != nil {
		t.Fatal(err)
	}
	agent := &threadSampleRecordingAgent{response: &model.AgentResponse{ResponseID: "resp-sample"}}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), Agent: agent, DefaultCWD: home})
	t.Cleanup(func() { _ = router.Close() })
	if err := router.threads.RetainLiveThread(store, record); err != nil {
		t.Fatal(err)
	}
	initializeThreadSampleConnection(t, router, "conn-1")

	for _, disableMCP := range []bool{true, false} {
		response := router.Handle(&Request{
			JSONRPC:      "2.0",
			ID:           IntID(1),
			Method:       MethodThreadSample,
			ConnectionID: "conn-1",
			Params: mustJSON(t, ThreadSampleParams{
				ThreadID:       "thread-sample-mcp",
				ExpectedTurnID: "turn-latest",
				Prompt:         "Predict the next message.",
				DisableMCP:     disableMCP,
			}),
		})
		if response.Error != nil {
			t.Fatalf("thread/sample error = %+v", response.Error)
		}
		agent.mu.Lock()
		request := agent.request
		agent.mu.Unlock()
		if request == nil {
			t.Fatal("sample agent request is missing")
		}
		notices := 0
		declarations := 0
		for _, item := range request.InputItems {
			object, _ := item.(map[string]any)
			switch {
			case object["type"] == "additional_tools":
				declarations++
			case object["type"] == "message" && object["role"] == "developer":
				if encoded, err := json.Marshal(object); err == nil &&
					strings.Contains(string(encoded), "MCP tools are unavailable for this request.") {
					notices++
				}
			}
		}
		if declarations != 1 {
			t.Fatalf("disableMCP=%v: inherited declarations = %d, want exactly the parent's", disableMCP, declarations)
		}
		if disableMCP {
			if notices != 1 {
				t.Fatalf("disableMCP=true: notices = %d, want 1", notices)
			}
			if len(request.Tools) != 0 {
				t.Fatalf("disableMCP=true: re-declared tools = %#v, want none", request.Tools)
			}
			continue
		}
		if notices != 0 {
			t.Fatalf("disableMCP=false: notices = %d, want 0", notices)
		}
	}
}
