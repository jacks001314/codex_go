package context

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Mirrors Rust #58454
// (guardian-context/src/preparation/source_tests.rs): elastic node_repl
// rendering keeps stored text, images, and the storage omission count, while
// fixed rendering still applies the rendered-byte budget.
func TestNodeReplElasticRenderingKeepsStoredTextImagesAndOmissionsLikeRust(t *testing.T) {
	const textBytes = 32_001
	const storageOmissions = 7
	text := strings.Repeat("x", textBytes)
	imageURL := "data:image/png;base64,original"
	responses := []nodeReplReviewResponse{{
		sequence:   1,
		provenance: "source",
		text:       text,
		imageURLs:  []string{imageURL},
	}}
	fixed := &NodeReplReviewEvidenceFragment{responses: responses, omittedResponses: storageOmissions}
	elastic := &NodeReplReviewEvidenceFragment{
		responses:        responses,
		omittedResponses: storageOmissions,
		budgeting:        GuardianContextBudgetingElastic,
	}

	if strings.Contains(fixed.Body(), text) {
		t.Fatal("fixed text layout kept the oversized response")
	}
	elasticBody := elastic.Body()
	if !strings.Contains(elasticBody, text) {
		t.Fatal("elastic text layout dropped the stored text")
	}
	if want := fmt.Sprintf(`<omitted node_repl_responses="%d" />`, storageOmissions); !strings.Contains(elasticBody, want) {
		t.Fatalf("elastic text omissions = %q, want %q", elasticBody, want)
	}

	if rendered := marshalNodeReplItems(t, fixed.MultimodalInputItems()); strings.Contains(rendered, imageURL) {
		t.Fatalf("fixed multimodal items kept the image of a dropped response: %s", rendered)
	}
	elasticItems := marshalNodeReplItems(t, elastic.MultimodalInputItems())
	if !strings.Contains(elasticItems, imageURL) {
		t.Fatalf("elastic multimodal items dropped the stored image: %s", elasticItems)
	}
	if want := fmt.Sprintf(`node_repl_responses=\"%d\"`, storageOmissions); !strings.Contains(elasticItems, want) {
		t.Fatalf("elastic multimodal omissions missing: %s", elasticItems)
	}
}

func marshalNodeReplItems(t *testing.T, items []map[string]any) string {
	t.Helper()
	encoded, err := json.Marshal(items)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return string(encoded)
}
