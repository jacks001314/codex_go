package eventmap

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestContextualDeveloperContent(t *testing.T) {
	content := []ContentItem{{Kind: ContentInputText, Text: "<token_budget>\nleft"}}
	if !IsContextualDevMessageContent(content) {
		t.Fatalf("IsContextualDevMessageContent() = false")
	}
	if HasNonContextualDevMessageContent(content) {
		t.Fatalf("HasNonContextualDevMessageContent() = true")
	}
	mixed := append(content, ContentItem{Kind: ContentInputText, Text: "real instructions"})
	if !HasNonContextualDevMessageContent(mixed) {
		t.Fatalf("HasNonContextualDevMessageContent(mixed) = false")
	}
}

func TestParseUserMessageSkipsImageLabels(t *testing.T) {
	item := &ResponseItem{
		Kind: ResponseMessage,
		Role: "user",
		Content: []ContentItem{
			{Kind: ContentInputText, Text: `<image name="one">`},
			{Kind: ContentInputImage, ImageURL: "data:image/png;base64,abc", Detail: "high"},
			{Kind: ContentInputText, Text: `</image>`},
			{Kind: ContentInputText, Text: "please inspect"},
		},
	}
	got, ok := ParseTurnItem(item)
	if !ok || got.Kind != TurnUserMessage {
		t.Fatalf("ParseTurnItem() = %#v/%v", got, ok)
	}
	want := []UserInput{
		{Kind: UserInputImage, ImageURL: "data:image/png;base64,abc", Detail: "high"},
		{Kind: UserInputText, Text: "please inspect"},
	}
	if !reflect.DeepEqual(got.UserContent, want) {
		t.Fatalf("UserContent = %#v, want %#v", got.UserContent, want)
	}
}

func TestParseAssistantReasoningWebSearchAndImage(t *testing.T) {
	assistant, ok := ParseTurnItem(&ResponseItem{
		Kind:    ResponseMessage,
		Role:    "assistant",
		ID:      "a1",
		Phase:   "final",
		Content: []ContentItem{{Kind: ContentOutputText, Text: "hello"}},
	})
	if !ok || assistant.Kind != TurnAgentMessage || assistant.AgentText != "hello" || assistant.Phase != "final" {
		t.Fatalf("assistant = %#v/%v", assistant, ok)
	}
	reasoning, ok := ParseTurnItem(&ResponseItem{Kind: ResponseReasoning, ID: "r1", Summary: []string{"s"}, RawContent: []string{"raw"}})
	if !ok || reasoning.Kind != TurnReasoning || reasoning.Summary[0] != "s" || reasoning.RawContent[0] != "raw" {
		t.Fatalf("reasoning = %#v/%v", reasoning, ok)
	}
	search, ok := ParseTurnItem(&ResponseItem{Kind: ResponseWebSearchCall, ID: "w1", WebSearchAction: "search: golang"})
	if !ok || search.Kind != TurnWebSearch || search.Query != "golang" {
		t.Fatalf("search = %#v/%v", search, ok)
	}
	image, ok := ParseTurnItem(&ResponseItem{Kind: ResponseImageGeneration, ID: "i1", ImageStatus: "completed", ImageResult: "abc"})
	if !ok || image.Kind != TurnImageGeneration || image.Status != "completed" {
		t.Fatalf("image = %#v/%v", image, ok)
	}
}

func TestContextualUserAndHookPrompt(t *testing.T) {
	if !IsContextualUserMessageContent([]ContentItem{{Kind: ContentInputText, Text: "<current_time>now</current_time>"}}) {
		t.Fatalf("IsContextualUserMessageContent() = false")
	}
	hook, ok := ParseTurnItem(&ResponseItem{Kind: ResponseMessage, Role: "user", ID: "h1", Content: []ContentItem{{Kind: ContentInputText, Text: "<hook_prompt>hi</hook_prompt>"}}})
	if !ok || hook.Kind != TurnHookPrompt {
		t.Fatalf("hook = %#v/%v", hook, ok)
	}
}

func TestRawAssistantOutputTextFromItem(t *testing.T) {
	text, ok := RawAssistantOutputTextFromItem(&ResponseItem{
		Kind: ResponseMessage,
		Role: "assistant",
		Content: []ContentItem{
			{Kind: ContentOutputText, Text: "a"},
			{Kind: ContentInputText, Text: "ignored"},
			{Kind: ContentOutputText, Text: "b"},
		},
	})
	if !ok || text != "ab" {
		t.Fatalf("RawAssistantOutputTextFromItem() = %q/%v", text, ok)
	}
}

func TestImageGenerationArtifactPathAndSave(t *testing.T) {
	home := t.TempDir()
	result := base64.StdEncoding.EncodeToString([]byte("png"))
	path, err := SaveImageGenerationResult(home, "session/1", "call:1", result)
	if err != nil {
		t.Fatalf("SaveImageGenerationResult() error = %v", err)
	}
	if filepath.Base(path) != "call_1.png" || !strings.Contains(path, "session_1") {
		t.Fatalf("path = %q", path)
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(bytes) != "png" {
		t.Fatalf("saved bytes = %q", bytes)
	}
}

func TestStripHiddenAssistantMarkup(t *testing.T) {
	got := StripHiddenAssistantMarkup("hello【cite】<proposed_plan>secret</proposed_plan> world", true)
	if got != "hello world" {
		t.Fatalf("StripHiddenAssistantMarkup() = %q", got)
	}
}

func TestStripHiddenAssistantMarkupRemovesRustMemoryAndWebCitations(t *testing.T) {
	text := "21°C\uE000cite\uE002turn7forecast0\uE001 and <oai-mem-citation>memory</oai-mem-citation>done"
	if got := StripHiddenAssistantMarkup(text, false); got != "21°C and done" {
		t.Fatalf("StripHiddenAssistantMarkup() = %q", got)
	}
}

func TestStripHiddenAssistantMarkupHidesUnterminatedCitations(t *testing.T) {
	text := "visible\uE000cite\uE002turn7forecast0"
	if got := StripHiddenAssistantMarkup(text, false); got != "visible" {
		t.Fatalf("StripHiddenAssistantMarkup() = %q", got)
	}
}
