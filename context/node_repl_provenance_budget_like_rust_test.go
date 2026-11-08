package context

import (
	"fmt"
	"strings"
	"testing"
)

// Rust #59891: elastic preparation no longer keeps every captured response. A
// run of tiny responses is bounded by the protected provenance headers, which
// are charged in budget units (ApproxTokenCount+1 tokens at 4 bytes/token)
// instead of raw bytes, so the rendered suffix is a contiguous newest slice.
func TestNodeReplElasticBoundsProvenanceHeadersLikeRust(t *testing.T) {
	const responses = 10_000
	items := make([]nodeReplReviewResponse, 0, responses)
	for index := 1; index <= responses; index++ {
		items = append(items, nodeReplReviewResponse{
			sequence:   uint64(index),
			provenance: "tool=node_repl cell=cell call=call",
			text:       "ok",
		})
	}
	elastic := &NodeReplReviewEvidenceFragment{
		responses: items,
		budgeting: GuardianContextBudgetingElastic,
	}
	body := elastic.Body()

	rendered := strings.Count(body, "[REPL response ")
	if rendered == 0 || rendered >= responses {
		t.Fatalf("elastic body rendered %d of %d responses, want a bounded suffix", rendered, responses)
	}
	if !strings.Contains(body, fmt.Sprintf("<omitted node_repl_responses=\"%d\" />", responses-rendered)) {
		t.Fatalf("elastic body lost the omission count for %d responses", responses-rendered)
	}
	// The suffix is the newest slice: the last response is present, the first is not.
	if !strings.Contains(body, fmt.Sprintf("[REPL response %d ", responses)) {
		t.Fatal("elastic body dropped the newest response")
	}
	if strings.Contains(body, "[REPL response 1 ") {
		t.Fatal("elastic body kept the oldest response of an over-budget run")
	}
	// Header cost, not body cost, sets the bound: a header is ~40 bytes, so the
	// 32 KiB budget admits hundreds of responses rather than all ten thousand.
	if rendered > 1_000 {
		t.Fatalf("elastic body rendered %d responses, want the header budget to bound it", rendered)
	}
}

// The same elastic selection must not clip a single oversized answer: its body
// is required evidence, so only the header is charged and the text survives
// whole (Rust keeps the fixed layout byte-bounded; elastic does not).
func TestNodeReplElasticKeepsOversizedAnswerBodyLikeRust(t *testing.T) {
	text := strings.Repeat("y", 40_000)
	elastic := &NodeReplReviewEvidenceFragment{
		responses: []nodeReplReviewResponse{{sequence: 1, provenance: "tool=node_repl", text: text}},
		budgeting: GuardianContextBudgetingElastic,
	}
	body := elastic.Body()
	if !strings.Contains(body, text) {
		t.Fatal("elastic body dropped the oversized response text")
	}
	if strings.Contains(body, "<omitted node_repl_responses=") {
		t.Fatalf("elastic body omitted the only response: %s", body[:200])
	}
}

// Fixed budgeting keeps charging the full rendered cost of every response, so
// the same oversized text is still dropped.
func TestNodeReplFixedBudgetStillChargesBodyBytesLikeRust(t *testing.T) {
	text := strings.Repeat("y", 40_000)
	fixed := &NodeReplReviewEvidenceFragment{
		responses: []nodeReplReviewResponse{{sequence: 1, provenance: "tool=node_repl", text: text}},
	}
	if body := fixed.Body(); strings.Contains(body, text) {
		t.Fatal("fixed body kept a response larger than the rendered budget")
	}
}
