package model

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Mirrors Rust #62095's
// AmazonBedrockModelProvider::normalize_completed_output_item: Mantle's
// reasoning prefix omits empty `annotations` on assistant commentary, so the
// completed item must drop an empty (but present) annotations array while
// keeping nonempty annotations, final answers, and Bedrock Runtime untouched.
func TestBedrockMantleNormalizesCommentaryAnnotationsLikeRust(t *testing.T) {
	newItem := func(phase string) *AgentItem {
		empty := []any{}
		cited := []any{map[string]any{
			"type": "url_citation", "url": "https://example.org", "title": "Source",
			"start_index": 0, "end_index": 6,
		}}
		return &AgentItem{
			ID:   "msg_progress",
			Type: "agent_message",
			Text: "Checking.\nSource",
			Data: map[string]any{
				"phase": phase,
				"outputContent": []responsesAgentContentBlock{
					{Type: "output_text", Text: "Checking.", Annotations: &empty},
					{Type: "output_text", Text: "Source", Annotations: &cited},
				},
			},
		}
	}

	mantle := newItem("commentary")
	NormalizeCompletedOutputItemForProvider(AmazonBedrockProviderID, mantle)
	blocks, ok := mantle.Data["outputContent"].([]responsesAgentContentBlock)
	if !ok || len(blocks) != 2 {
		t.Fatalf("mantle commentary blocks = %#v", mantle.Data["outputContent"])
	}
	if blocks[0].Annotations != nil {
		t.Fatalf("mantle commentary kept empty annotations: %#v", *blocks[0].Annotations)
	}
	if blocks[1].Annotations == nil || len(*blocks[1].Annotations) != 1 {
		t.Fatalf("mantle commentary lost nonempty annotations: %#v", blocks[1].Annotations)
	}
	encoded, err := json.Marshal(mantle)
	if err != nil {
		t.Fatalf("Marshal mantle commentary error = %v", err)
	}
	if strings.Contains(string(encoded), `"text":"Checking.","annotations":[]`) {
		t.Fatalf("mantle commentary still replays empty annotations: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"type":"url_citation"`) {
		t.Fatalf("mantle commentary lost cited annotations: %s", encoded)
	}

	answer := newItem("")
	NormalizeCompletedOutputItemForProvider(AmazonBedrockProviderID, answer)
	answerBlocks := answer.Data["outputContent"].([]responsesAgentContentBlock)
	if answerBlocks[0].Annotations == nil || len(*answerBlocks[0].Annotations) != 0 {
		t.Fatalf("final answer annotations changed: %#v", answerBlocks[0].Annotations)
	}

	runtime := newItem("commentary")
	NormalizeCompletedOutputItemForProvider(AmazonBedrockRuntimeProviderID, runtime)
	runtimeBlocks := runtime.Data["outputContent"].([]responsesAgentContentBlock)
	if runtimeBlocks[0].Annotations == nil || len(*runtimeBlocks[0].Annotations) != 0 {
		t.Fatalf("runtime commentary annotations changed: %#v", runtimeBlocks[0].Annotations)
	}

	openai := newItem("commentary")
	NormalizeCompletedOutputItemForProvider(OpenAIProviderName, openai)
	openAIBlocks := openai.Data["outputContent"].([]responsesAgentContentBlock)
	if openAIBlocks[0].Annotations == nil {
		t.Fatal("non-Bedrock provider should not normalize commentary annotations")
	}
}

const bedrockCommentaryStreamFixture = `{"type":"response.output_item.done","item":{"id":"msg_progress","type":"message","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"Checking.","annotations":[],"logprobs":[]},{"type":"output_text","text":"Source","annotations":[{"type":"url_citation","url":"https://example.org","title":"Source","start_index":0,"end_index":6}]}]}}`

const bedrockReasoningStreamFixture = `{"type":"response.output_item.done","item":{"id":"rs_plan","type":"reasoning","summary":[{"type":"summary_text","text":"plan"}],"encrypted_content":"enc"}}`

const bedrockFunctionCallStreamFixture = `{"type":"response.output_item.done","item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"exec","arguments":"{}"}}`

const bedrockAnswerStreamFixture = `{"type":"response.output_item.done","item":{"id":"msg_answer","type":"message","role":"assistant","content":[{"type":"output_text","text":"Done.","annotations":[],"logprobs":[]}]}}`

func bedrockCompletedStreamFixture(output string) string {
	return `{"type":"response.completed","response":{"id":"resp_1","output":[` + output + `],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`
}

// Mirrors the replay half of Rust #62095's
// bedrock_function_continuation_replays_assistant_commentary: the streamed
// commentary recorded for history keeps its reasoning/function-call ordering,
// drops empty annotations on Mantle only, and leaves the final answer intact.
func TestBedrockMantleStreamReplaysCommentaryWithoutEmptyAnnotationsLikeRust(t *testing.T) {
	output := strings.Join([]string{
		`{"id":"msg_progress","type":"message","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"Checking.","annotations":[],"logprobs":[]},{"type":"output_text","text":"Source","annotations":[{"type":"url_citation","url":"https://example.org","title":"Source","start_index":0,"end_index":6}]}]}`,
		`{"id":"rs_plan","type":"reasoning","summary":[{"type":"summary_text","text":"plan"}],"encrypted_content":"enc"}`,
		`{"id":"fc_1","type":"function_call","call_id":"call_1","name":"exec","arguments":"{}"}`,
		`{"id":"msg_answer","type":"message","role":"assistant","content":[{"type":"output_text","text":"Done.","annotations":[],"logprobs":[]}]}`,
	}, ",")
	stream := responsesSSE(
		bedrockCommentaryStreamFixture,
		bedrockReasoningStreamFixture,
		bedrockFunctionCallStreamFixture,
		bedrockAnswerStreamFixture,
		bedrockCompletedStreamFixture(output),
	)

	response, err := parseResponsesStream(context.Background(), strings.NewReader(stream), &AgentRequest{Model: "gpt-6.2-sol"}, AmazonBedrockProviderID, nil)
	if err != nil {
		t.Fatalf("parseResponsesStream(mantle) error = %v", err)
	}
	if len(response.Items) != 4 {
		t.Fatalf("mantle items = %#v", response.Items)
	}
	if response.Items[0].ID != "msg_progress" || response.Items[1].ID != "rs_plan" || response.Items[2].ID != "fc_1" || response.Items[3].ID != "msg_answer" {
		t.Fatalf("mantle item order = %#v", response.Items)
	}
	commentary := replayedAgentItemJSON(t, &response.Items[0])
	if strings.Contains(commentary, `"text":"Checking.","annotations":[]`) {
		t.Fatalf("mantle replay kept empty annotations: %s", commentary)
	}
	if !strings.Contains(commentary, `"type":"url_citation"`) {
		t.Fatalf("mantle replay lost cited annotations: %s", commentary)
	}
	answer := replayedAgentItemJSON(t, &response.Items[3])
	if !strings.Contains(answer, `"annotations":[]`) {
		t.Fatalf("mantle replay changed the final answer: %s", answer)
	}

	runtimeResponse, err := parseResponsesStream(context.Background(), strings.NewReader(stream), &AgentRequest{Model: "gpt-6.2-sol"}, AmazonBedrockRuntimeProviderID, nil)
	if err != nil {
		t.Fatalf("parseResponsesStream(runtime) error = %v", err)
	}
	runtimeCommentary := replayedAgentItemJSON(t, &runtimeResponse.Items[0])
	if !strings.Contains(runtimeCommentary, `"annotations":[]`) {
		t.Fatalf("runtime replay should keep empty annotations: %s", runtimeCommentary)
	}
}

func replayedAgentItemJSON(t *testing.T, item *AgentItem) string {
	t.Helper()
	encoded, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("Marshal replayed item error = %v", err)
	}
	return string(encoded)
}
