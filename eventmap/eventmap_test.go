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
	// Mirrors Rust's CONTEXTUAL_USER_FRAGMENT_MATCHERS: every injected user
	// fragment is hidden runtime context.
	contextual := []string{
		"<user_instructions>follow</user_instructions>",
		"  <environment_context>\n<cwd>/repo</cwd>\n</environment_context>  ",
		"<agent_message_board_notification>board</agent_message_board_notification>",
		"<skills_instructions>skills</skills_instructions>",
		"<user_shell_command><command>ls</command></user_shell_command>",
		"<turn_aborted>interrupted</turn_aborted>",
		"<subagent_notification>agent done</subagent_notification>",
		"<recommended_plugins>plugins</recommended_plugins>",
		"<external_calendar>{\"a\":1}</external_calendar>",
		"<codex_internal_context source=\"goal\">\nbody\n</codex_internal_context>",
		"<goal_context>legacy goal</goal_context>",
		"Warning: The maximum number of unified exec processes you can keep open is 5",
		"Warning: Your account was flagged for potentially high-risk cyber activity",
		"Warning: apply_patch was requested via exec_command. Use the apply_patch tool instead of exec_command.",
	}
	for _, text := range contextual {
		if !IsContextualUserMessageContent([]ContentItem{{Kind: ContentInputText, Text: text}}) {
			t.Fatalf("IsContextualUserMessageContent(%q) = false, want true", text)
		}
	}
	// A developer-role fragment, plain text and a partially marked message are
	// not contextual user content.
	for _, text := range []string{
		"<current_time>now</current_time>",
		"<current_time_reminder>It is now.</current_time_reminder>",
		"hello",
		"<user_instructions>unclosed",
		"<codex_internal_context source=\"Bad\">body</codex_internal_context>",
		"Warning: apply_patch was requested via exec_command.",
	} {
		if IsContextualUserMessageContent([]ContentItem{{Kind: ContentInputText, Text: text}}) {
			t.Fatalf("IsContextualUserMessageContent(%q) = true, want false", text)
		}
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

// TestMemoryCitationBodies mirrors Rust
// codex_utils_stream_parser::citation::strip_citations collecting citation
// bodies, including the auto-close at EOF behavior.
func TestMemoryCitationBodies(t *testing.T) {
	text := "before <oai-mem-citation>first</oai-mem-citation> middle " +
		"<oai-mem-citation>second</oai-mem-citation> after"
	if got := MemoryCitationBodies(text); !reflect.DeepEqual(got, []string{"first", "second"}) {
		t.Fatalf("MemoryCitationBodies() = %#v", got)
	}
	if got := MemoryCitationBodies("visible <oai-mem-citation>unterminated"); !reflect.DeepEqual(got, []string{"unterminated"}) {
		t.Fatalf("unterminated MemoryCitationBodies() = %#v", got)
	}
	if got := MemoryCitationBodies("partial <oai-mem-"); got != nil {
		t.Fatalf("partial MemoryCitationBodies() = %#v", got)
	}
	if got := MemoryCitationBodies("no citations here"); got != nil {
		t.Fatalf("MemoryCitationBodies() = %#v", got)
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
	text := "昆明\uE200cite\uE202turn0forecast0\uE201 21°C\uE000cite\uE002turn7forecast0\uE001 and <oai-mem-citation>memory</oai-mem-citation>done"
	if got := StripHiddenAssistantMarkup(text, false); got != "昆明 21°C and done" {
		t.Fatalf("StripHiddenAssistantMarkup() = %q", got)
	}
}

func TestStripHiddenAssistantMarkupHidesUnterminatedCitations(t *testing.T) {
	text := "visible\uE200cite\uE202turn0forecast0"
	if got := StripHiddenAssistantMarkup(text, false); got != "visible" {
		t.Fatalf("StripHiddenAssistantMarkup() = %q", got)
	}
}
