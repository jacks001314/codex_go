package appserver

import (
	"encoding/json"
	"strings"

	codexctx "codex_go/context"
	"codex_go/features"
	"codex_go/model"
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
func (r *RuntimeRouter) recordNodeReplReviewEvidence(threadID string, execution *turn.ToolExecutionResult) {
	if r == nil || execution == nil || execution.Invocation == nil || execution.Output == nil {
		return
	}
	if execution.Invocation.Source != "code_mode" || !execution.Output.Success {
		return
	}
	namespace := strings.TrimRight(strings.TrimSpace(execution.Invocation.ToolName.Namespace), "_")
	if namespace != "node_repl" && namespace != "mcp__node_repl" {
		return
	}
	active := r.activeRuntimeTurnStateSnapshot(strings.TrimSpace(threadID), "")
	if active == nil || active.Params == nil {
		return
	}
	cfg, err := r.effectiveConfigForTurn(active.Params)
	if err != nil || cfg == nil {
		return
	}
	modelInfo := r.modelInfoForRuntimeWithConfig(strings.TrimSpace(active.Params.Model), cfg)
	nodeReplRequired := modelInfo != nil && modelInfo.NodeReplAutoReviewRequired
	enhancedEnabled := features.Enabled(cfg.FeatureSettings(), "guardian_enhanced_node_repl_transcripts")
	transcriptImages := features.Enabled(cfg.FeatureSettings(), "guardian_node_repl_transcript_images")
	if codexctx.NodeReplReviewEvidenceModeFor(nodeReplRequired, enhancedEnabled, transcriptImages) == codexctx.NodeReplEvidenceDisabled {
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

// nodeReplReviewedSequenceForModel is a convenience helper used by callers to
// derive a node_repl auto-review requirement for a model info.
func nodeReplAutoReviewRequiredForModel(info *model.ModelInfo) bool {
	return info != nil && info.NodeReplAutoReviewRequired
}
