package appserver

import (
	"encoding/json"
	"strings"

	codexctx "codex_go/context"
	"codex_go/features"
	"codex_go/mcp"
	"codex_go/model"
	"codex_go/state"
	"codex_go/tool"
	"codex_go/turn"
)

// nodeReplReviewTextBlocks extracts the retained text blocks from a node_repl
// code-mode result (Rust #38397): encrypted blocks are excluded and an empty
// text result falls back to the structured content, mirroring the Rust
// McpHandler::on_tool_result_accepted.
func nodeReplReviewTextBlocks(output *tool.Output) []string {
	if output == nil || output.Data == nil {
		return nil
	}
	rawContent, ok := output.Data["content"].([]any)
	if !ok {
		return nil
	}
	blocks := make([]string, 0, len(rawContent))
	hasEncrypted := false
	for _, raw := range rawContent {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if nodeReplItemEncrypted(item) {
			hasEncrypted = true
			continue
		}
		if typeName, _ := item["type"].(string); typeName != "text" {
			continue
		}
		text, _ := item["text"].(string)
		if strings.TrimSpace(text) == "" {
			continue
		}
		blocks = append(blocks, text)
	}
	if len(blocks) == 0 && !hasEncrypted {
		if structured, ok := output.Data["structuredContent"]; ok && structured != nil {
			if text, err := json.Marshal(structured); err == nil {
				blocks = append(blocks, string(text))
			}
		}
	}
	return blocks
}

func nodeReplItemEncrypted(item map[string]any) bool {
	meta, _ := item["_meta"].(map[string]any)
	if meta == nil {
		return false
	}
	encrypted, _ := meta["codex/encryptedContent"].(bool)
	return encrypted
}

func nodeReplReviewImageURLs(output *tool.Output) []string {
	if output == nil || output.Data == nil {
		return nil
	}
	rawContent, ok := output.Data["content"].([]any)
	if !ok {
		return nil
	}
	var urls []string
	for _, raw := range rawContent {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		typeName, _ := item["type"].(string)
		if typeName != "image" {
			continue
		}
		if nodeReplItemEncrypted(item) {
			continue
		}
		url, _ := item["image_url"].(string)
		if strings.TrimSpace(url) == "" {
			url, _ = item["url"].(string)
		}
		if strings.TrimSpace(url) != "" {
			urls = append(urls, url)
		}
	}
	return urls
}

// recordNodeReplReviewEvidence captures accepted, successful node_repl
// code-mode results for Guardian review evidence (Rust #38397).
func (r *RuntimeRouter) recordNodeReplReviewEvidence(threadID, turnID string, execution *turn.ToolExecutionResult) {
	if r == nil || execution == nil || execution.Invocation == nil || execution.Output == nil {
		return
	}
	if execution.Invocation.Source != "code_mode" || !execution.Output.Success {
		return
	}
	if !r.nodeReplBackedInvocation(execution.Invocation) {
		return
	}
	if r.nodeReplEvidenceModeForTurn(strings.TrimSpace(threadID), turnID) == codexctx.NodeReplEvidenceDisabled {
		return
	}
	blocks := nodeReplReviewTextBlocks(execution.Output)
	imageURLs := nodeReplReviewImageURLs(execution.Output)
	if len(blocks) == 0 && len(imageURLs) == 0 {
		return
	}
	cellID, _ := execution.Invocation.Context[tool.CodeModeCellIDContextKey].(string)
	callID := strings.TrimSpace(execution.Invocation.CallID)
	evidence := r.nodeReplEvidenceForThread(strings.TrimSpace(threadID))
	evidence.RecordMultimodal(execution.Invocation.ToolName.Name, cellID, callID, blocks, imageURLs)
}

// nodeReplEvidenceModeForTurn resolves the reviewed turn's node_repl evidence
// mode from its model policy and feature flags (Rust's
// node_repl_review_evidence_mode). An empty turnID falls back to the thread's
// active turn, matching Rust's use of the running turn's context.
func (r *RuntimeRouter) nodeReplEvidenceModeForTurn(threadID, turnID string) codexctx.NodeReplReviewEvidenceMode {
	if r == nil {
		return codexctx.NodeReplEvidenceDisabled
	}
	threadID = strings.TrimSpace(threadID)
	if strings.TrimSpace(turnID) == "" {
		if active := r.threads.ActiveTurn(threadID); active != nil {
			turnID = active.TurnID
		}
	}
	active := r.activeRuntimeTurnStateSnapshot(threadID, turnID)
	if active == nil || active.Params == nil {
		return codexctx.NodeReplEvidenceDisabled
	}
	cfg, err := r.effectiveConfigForTurn(active.Params)
	if err != nil || cfg == nil {
		return codexctx.NodeReplEvidenceDisabled
	}
	modelInfo := r.modelInfoForRuntimeWithConfig(strings.TrimSpace(active.Params.Model), cfg)
	settings := cfg.FeatureSettings()
	return codexctx.NodeReplReviewEvidenceModeFor(
		nodeReplAutoReviewRequiredForModel(modelInfo),
		features.Enabled(settings, "guardian_enhanced_node_repl_transcripts"),
		features.Enabled(settings, "guardian_node_repl_transcript_images"),
	)
}

// guardianNodeReplResultTokenLimit mirrors Rust's node_repl_result_token_limit
// (#57176): enhanced node_repl transcripts admit the larger per-entry output
// cap, while a disabled evidence mode keeps the ordinary tool entry cap.
func (r *RuntimeRouter) guardianNodeReplResultTokenLimit(threadID string) int {
	return guardianNodeReplResultTokenLimitForMode(r.nodeReplEvidenceModeForTurn(threadID, ""))
}

// guardianNodeReplResultTokenLimitForMode holds Rust's
// node_repl_result_token_limit branch: the enhanced cap applies whenever any
// node_repl evidence mode is enabled.
func guardianNodeReplResultTokenLimitForMode(mode codexctx.NodeReplReviewEvidenceMode) int {
	if mode == codexctx.NodeReplEvidenceDisabled {
		return state.MaxToolEntryTokens
	}
	return codexctx.GuardianMaxNodeReplToolResultTokens
}

func (r *RuntimeRouter) nodeReplBackedInvocation(invocation *tool.Invocation) bool {
	if invocation == nil {
		return false
	}
	namespace := strings.TrimRight(strings.TrimSpace(invocation.ToolName.Namespace), "_")
	if isNodeReplBackedNamespace(namespace) {
		return true
	}
	connectorID := ""
	if r != nil && r.services.ToolRouter != nil {
		connectorID, _ = r.services.ToolRouter.MCPConnectorInfo(invocation)
	}
	server := strings.TrimPrefix(namespace, "mcp__")
	return mcp.IsNodeReplBackedConnector(server, connectorID)
}

// isNodeReplBackedNamespace reports whether the tool namespace routes through a
// Node REPL-backed MCP server (node_repl or cua_repl), mirroring Rust
// codex_protocol::mcp::is_node_repl_backed_server (#40257).
func isNodeReplBackedNamespace(namespace string) bool {
	switch namespace {
	case "node_repl", "cua_repl", "mcp__node_repl", "mcp__cua_repl":
		return true
	default:
		return false
	}
}

func (r *RuntimeRouter) nodeReplEvidenceForThread(threadID string) *codexctx.NodeReplReviewEvidence {
	r.nodeReplEvidenceMu.Lock()
	defer r.nodeReplEvidenceMu.Unlock()
	if r.nodeReplEvidence == nil {
		r.nodeReplEvidence = map[string]*codexctx.NodeReplReviewEvidence{}
	}
	evidence := r.nodeReplEvidence[threadID]
	if evidence == nil {
		evidence = &codexctx.NodeReplReviewEvidence{}
		r.nodeReplEvidence[threadID] = evidence
	}
	return evidence
}

// guardianReviewNodeReplEvidence returns a renderable snapshot of node_repl
// evidence admitted since the given reviewed sequence.
func (r *RuntimeRouter) guardianReviewNodeReplEvidence(threadID string, reviewedSequence uint64) *codexctx.NodeReplReviewEvidenceFragment {
	if r == nil {
		return nil
	}
	r.nodeReplEvidenceMu.Lock()
	evidence := r.nodeReplEvidence[strings.TrimSpace(threadID)]
	r.nodeReplEvidenceMu.Unlock()
	if evidence == nil {
		return nil
	}
	return evidence.SnapshotSince(reviewedSequence)
}

// clearNodeReplReviewEvidence drops retained node_repl evidence when a thread
// is rolled back (Rust #38397).
func (r *RuntimeRouter) clearNodeReplReviewEvidence(threadID string) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	r.nodeReplEvidenceMu.Lock()
	evidence := r.nodeReplEvidence[threadID]
	delete(r.nodeReplEvidence, threadID)
	r.nodeReplEvidenceMu.Unlock()
	if evidence != nil {
		evidence.Clear()
	}
}

// nodeReplReviewedSequenceForModel derives a node_repl auto-review requirement
// for a model info, honoring a model-provided Guardian computer-use policy over
// the legacy node_repl_auto_review_required bit (Rust #42744).
func nodeReplAutoReviewRequiredForModel(info *model.ModelInfo) bool {
	return info != nil && info.ComputerUseReviewRequired()
}
