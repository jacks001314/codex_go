package prompt

import "testing"

func TestFormattedInputStripsImageDetailsForLite(t *testing.T) {
	detail := "high"
	prompt := &DebugPrompt{
		UseResponsesLite: true,
		Input: []ResponseItem{{
			Type: "message",
			Role: "user",
			Content: []ContentItem{{
				Type:   "input_image",
				Image:  "data:image/png;base64,abc",
				Detail: &detail,
			}},
		}},
	}
	got := prompt.FormattedInputForRequest()
	if got[0].Content[0].Detail != nil {
		t.Fatalf("detail should be stripped: %#v", got)
	}
	if prompt.Input[0].Content[0].Detail == nil {
		t.Fatalf("original prompt should not be mutated")
	}
}

func TestBuildPromptInputAppendsUserInput(t *testing.T) {
	got := BuildPromptInput(nil, "hello")
	if len(got) != 1 || got[0].Role != "user" || got[0].Content[0].Text != "hello" {
		t.Fatalf("unexpected input: %#v", got)
	}
}
