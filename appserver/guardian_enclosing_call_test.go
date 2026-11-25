package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	codexctx "codex_go/context"
	"codex_go/mcp"
	"codex_go/model"
	"codex_go/session"
	"codex_go/state"
)

func TestGuardianEnclosingCallRealNestedReview(t *testing.T) {
	for _, tc := range []struct {
		name                                    string
		enabled, live, duplicate, preflight     bool
		emptyDuplicate, custom, repeat, browser bool
		ownerTurn, outcome                      string
		want                                    bool
	}{
		{name: "nested allow", enabled: true, live: true, want: true},
		{name: "strict browser", enabled: true, live: true, browser: true, want: true},
		{name: "custom wrapper", enabled: true, live: true, custom: true, want: true},
		{name: "repeated review", enabled: true, live: true, repeat: true, want: true},
		{name: "empty duplicate identity", enabled: true, live: true, emptyDuplicate: true},
		{name: "nested deny", enabled: true, live: true, want: true, outcome: "deny"},
		{name: "nested timeout", enabled: true, live: true, want: true, outcome: "timeout"},
		{name: "default off", live: true},
		{name: "non nested", enabled: true},
		{name: "previous turn", enabled: true, live: true, ownerTurn: "previous"},
		{name: "duplicate identity", enabled: true, live: true, duplicate: true},
		{name: "wrapper preflight", enabled: true, live: true, preflight: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			cfg := fmt.Sprintf("approvals_reviewer = \"auto_review\"\n[features]\nguardian_nested_approval_context = %v\n", tc.enabled)
			if err := os.WriteFile(config.ConfigPath(home), []byte(cfg), 0600); err != nil {
				t.Fatal(err)
			}
			store := session.NewStore(t.TempDir())
			const threadID = "thread-node-repl"
			const turnID = "turn-node-repl"
			const args = "  {\"code\":\"await checkout(42)\"}  "
			now := time.Now().UTC()
			items := []session.Item{
				{ID: "earlier", CallID: "earlier", Type: "function_call", Name: "earlier_tool", Text: args, CreatedAt: now},
				{ID: "outer", CallID: "outer", Type: "function_call", Name: "js", Text: args, CreatedAt: now},
				{ID: "later", CallID: "later", Type: "custom_tool_call", Name: "later_tool", Text: args, CreatedAt: now},
			}
			outerTool, server, connector := "js", "node_repl", ""
			if tc.browser {
				outerTool, server, connector = "_js", "codex_apps", mcp.BrowserConnectorID
				items[1].Name = outerTool
			}
			if tc.custom {
				items[1].Type = "custom_tool_call"
			}
			if tc.emptyDuplicate {
				items = append(items, session.Item{ID: "empty", CallID: "outer", Type: "function_call", Name: "different", CreatedAt: now})
			}
			if tc.duplicate {
				items = append(items, session.Item{ID: "duplicate", CallID: "outer", Type: "function_call", Name: "different", Text: `{"code":"conflict()"}`, CreatedAt: now})
			}
			if err := store.Create(&session.Record{ID: session.ThreadID(threadID), SessionID: threadID, CreatedAt: now, UpdatedAt: now, Metadata: session.Metadata{CWD: t.TempDir()}, Items: items}); err != nil {
				t.Fatal(err)
			}
			router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), Config: config.NewConfigService(home)})
			t.Cleanup(func() { _ = router.Close() })
			var captured *model.AgentRequest
			reviews := 0
			reviewer := &modelGuardianReviewer{
				store: state.NewReviewStore(), transcriptSnapshot: router.guardianReviewTranscriptSnapshotForReview,
				agent: guardianAgentFunc(func(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
					captured = request
					reviews++
					if tc.outcome == "timeout" {
						<-ctx.Done()
						return nil, ctx.Err()
					}
					if tc.outcome == "deny" {
						return &model.AgentResponse{Message: `{"riskLevel":"high","userAuthorization":"none","outcome":"deny","rationale":"not authorized"}`}, nil
					}
					return &model.AgentResponse{ResponseID: fmt.Sprintf("review-response-%d", reviews), Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"allowed"}`}, nil
				}),
			}
			if tc.outcome == "timeout" {
				reviewer.timeout = 100 * time.Millisecond
			}
			service := mcp.NewMCPService(nil)
			if tc.live {
				ownerTurn := turnID
				if tc.ownerTurn != "" {
					ownerTurn = tc.ownerTurn
				}
				release := service.RegisterToolInvocation(server, "outer", mcp.MCPInvocationMetadata{ServerName: server, ToolName: outerTool, ConnectorID: connector, ThreadID: threadID, TurnID: ownerTurn})
				defer release()
			}
			handler := &appserverMCPElicitationHandler{service: service, reviewer: reviewer, authority: router.currentMCPElicitationAuthority}
			meta := nodeReplNestedReviewMeta()
			meta["callId"] = "outer"
			if tc.browser {
				meta["connector_id"] = connector
				meta["codex_strict_auto_review"] = true
				meta[mcp.MCPToolCodexAppsMetaKey] = map[string]any{"call_id": "outer"}
			}
			if tc.preflight {
				meta["tool_name"] = "js"
				meta["connector_id"] = "node_repl"
			}
			response, err := handler.HandleMCPElicitation(context.Background(), nodeReplNestedReviewRequest(server, meta))
			if err != nil || captured == nil {
				t.Fatalf("missing model review: %v %+v", err, response)
			}
			if tc.repeat {
				response, err = handler.HandleMCPElicitation(context.Background(), nodeReplNestedReviewRequest(server, meta))
				if err != nil || reviews != 2 {
					t.Fatalf("second review missing: %d %v", reviews, err)
				}
				if captured.PreviousResponseID != "review-response-1" {
					t.Fatalf("lost reviewer continuation: %q", captured.PreviousResponseID)
				}
			}
			prompt := captured.Prompt
			for _, raw := range captured.InputItems {
				data, _ := json.Marshal(raw)
				if strings.Contains(string(data), "Enclosing call for this review:") {
					t.Fatal("unexpected native delivery in this test")
				}
			}
			marker := ">>> APPROVAL REQUEST START\n"
			block := strings.Split(strings.Split(prompt, marker)[1], ">>> APPROVAL REQUEST END")[0]
			if strings.Contains(block, "Enclosing call for this review:") != tc.want {
				t.Fatalf("wrong context presence: %s", block)
			}
			if tc.want {
				line := strings.Split(strings.TrimPrefix(block, "Enclosing call for this review: "), "\n")[0]
				var got map[string]string
				if err := json.Unmarshal([]byte(line), &got); err != nil {
					t.Fatal(err)
				}
				if got["arguments"] != args || got["tool"] != "tool "+outerTool+" call" {
					t.Fatalf("wrong enclosing identity/arguments: %#v", got)
				}
			}
			var planned map[string]any
			if err := json.Unmarshal([]byte(strings.Split(block, "Planned action JSON:\n")[1]), &planned); err != nil {
				t.Fatal(err)
			}
			name := "browser.click"
			if tc.preflight {
				name = "js"
			}
			if planned["tool_name"] != name || planned["arguments"].(map[string]any)["selector"] != "#submit" {
				t.Fatalf("changed nested action: %#v", planned)
			}
			responseMeta := mapFromAny(response.Meta)
			switch tc.outcome {
			case "deny":
				if response.Action != mcp.MCPElicitationActionDecline || responseMeta["reason"] != state.RenderGuardianRejection("not authorized", state.GuardianRejectionInstructions()) {
					t.Fatalf("changed denial: %+v", response)
				}
			case "timeout":
				if response.Action != mcp.MCPElicitationActionDecline || responseMeta["timed_out"] != true || responseMeta["reason"] != state.GuardianTimeoutInstructions() {
					t.Fatalf("changed timeout: %+v", response)
				}
			default:
				if response.Action != mcp.MCPElicitationActionAccept {
					t.Fatalf("changed approval: %+v", response)
				}
			}
		})
	}
}

// Optional enclosing evidence must not change the already selected transcript
// or screenshots, even when the independent image cap is the limiting budget.
func TestGuardianEnclosingCallEvidencePriority(t *testing.T) {
	const arguments = `{"code":"` + "checkout();checkout();checkout();" + `"}`
	outerArguments := strings.Repeat(arguments, 50)
	action := state.Action{Type: "mcp_tool_call", Server: "node_repl", ToolName: "submit", Arguments: map[string]any{"amount": 42}}
	run := func(nested bool, image string, window int64) *model.AgentRequest {
		t.Helper()
		evidence := &codexctx.NodeReplReviewEvidence{}
		evidence.RecordMultimodal("js", "cell", "outer", []string{"screenshot evidence"}, []string{image})
		reviewer, requests := guardianEvidenceReviewer(evidence)
		reviewer.syncImageContextWindow = func(string, string) (int64, bool) { return window, true }
		reviewer.transcriptSnapshot = func(string) *guardianReviewTranscriptSnapshot {
			return &guardianReviewTranscriptSnapshot{profile: state.SynchronousContextProfile(), collected: []guardianCollectedTranscriptEntry{
				{callID: "outer", callMatches: 1, callArguments: outerArguments, capKind: guardianTranscriptCapTool,
					entry:    state.GuardianTranscriptEntry{Author: "tool", LineRole: "tool js call", Text: outerArguments},
					evidence: state.TranscriptEvidenceEntry{Kind: state.GuardianTranscriptTool, Text: outerArguments}},
				{capKind: guardianTranscriptCapTool, entry: state.GuardianTranscriptEntry{Author: "tool", LineRole: "tool read result", Text: "ordinary evidence"},
					evidence: state.TranscriptEvidenceEntry{Kind: state.GuardianTranscriptTool, Text: "ordinary evidence"}},
			}}
		}
		ctx := context.Background()
		if nested {
			ctx = withGuardianEnclosingInvocation(ctx, "outer")
		}
		if decision, _, err := reviewer.Review(ctx, "thread-budget", "turn-budget", "nested", action); err != nil || decision != state.DecisionApproved || len(*requests) != 1 {
			t.Fatalf("review failed: %s %v (%d requests)", decision, err, len(*requests))
		}
		return (*requests)[0]
	}
	probe := run(false, "screenshot", 1<<30)
	for _, tc := range []struct {
		name, image string
		window      int64
		want        bool
	}{
		{"roomy", "screenshot", 1 << 30, true},
		{"aggregate", "screenshot", guardianCompleteRequestTokens(probe.Prompt, probe.InputItems, guardianAssessmentOutputSchema(), 0) + guardianInputTokenMargin, false},
		{"image cap", "screenshot", guardianCompleteRequestTokens(probe.Prompt, probe.InputItems, guardianAssessmentOutputSchema(), 0) + guardianInputTokenMargin, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			baseline := run(false, tc.image, tc.window)
			nested := run(true, tc.image, tc.window)
			if len(imageInputItems(baseline.InputItems)) != 1 || len(imageInputItems(nested.InputItems)) != 1 {
				t.Fatal("enclosing evidence displaced the screenshot")
			}
			if strings.Contains(nested.Prompt, "Enclosing call for this review:") != tc.want {
				t.Fatalf("wrong enclosing admission: %s", nested.Prompt)
			}
			if !strings.Contains(nested.Prompt, "ordinary evidence") || !strings.Contains(nested.Prompt, `"amount": 42`) {
				t.Fatal("lost ordinary evidence or proposed action")
			}
			if !tc.want {
				before, _ := json.Marshal(baseline.InputItems)
				after, _ := json.Marshal(nested.InputItems)
				if baseline.Prompt != nested.Prompt || string(before) != string(after) {
					t.Fatal("optional context changed baseline evidence selection")
				}
			}
		})
	}
}
