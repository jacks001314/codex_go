package eventmap

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
)

type ContentKind string

const (
	ContentInputText  ContentKind = "input_text"
	ContentOutputText ContentKind = "output_text"
	ContentInputImage ContentKind = "input_image"
)

type ContentItem struct {
	Kind     ContentKind
	Text     string
	ImageURL string
	Detail   string
}

type ResponseItemKind string

const (
	ResponseMessage         ResponseItemKind = "message"
	ResponseReasoning       ResponseItemKind = "reasoning"
	ResponseWebSearchCall   ResponseItemKind = "web_search_call"
	ResponseImageGeneration ResponseItemKind = "image_generation_call"
	ResponseOther           ResponseItemKind = "other"
)

type ResponseItem struct {
	Kind            ResponseItemKind
	ID              string
	Role            string
	Phase           string
	Content         []ContentItem
	Summary         []string
	RawContent      []string
	WebSearchAction string
	ImageStatus     string
	RevisedPrompt   string
	ImageResult     string
}

type TurnItemKind string

const (
	TurnUserMessage     TurnItemKind = "user_message"
	TurnAgentMessage    TurnItemKind = "agent_message"
	TurnReasoning       TurnItemKind = "reasoning"
	TurnWebSearch       TurnItemKind = "web_search"
	TurnImageGeneration TurnItemKind = "image_generation"
	TurnHookPrompt      TurnItemKind = "hook_prompt"
)

type UserInputKind string

const (
	UserInputText  UserInputKind = "text"
	UserInputImage UserInputKind = "image"
)

type UserInput struct {
	Kind     UserInputKind
	Text     string
	ImageURL string
	Detail   string
}

type TurnItem struct {
	Kind          TurnItemKind
	ID            string
	UserContent   []UserInput
	AgentText     string
	Phase         string
	Summary       []string
	RawContent    []string
	Query         string
	Action        string
	Status        string
	RevisedPrompt string
	Result        string
	SavedPath     string
}

var contextualDeveloperPrefixes = []string{
	"<permissions instructions>",
	"<model_switch>",
	"<collaboration_mode>",
	"<multi_agent_mode>",
	"<realtime_conversation>",
	"<skills_instructions>",
	"<personality_spec>",
	"<token_budget>",
	"<context_window>",
	"<context_window_guidance>",
	"<rollout_budget>",
}

func IsContextualUserMessageContent(message []ContentItem) bool {
	for _, item := range message {
		if item.Kind != ContentInputText {
			continue
		}
		text := strings.TrimSpace(item.Text)
		if strings.HasPrefix(text, "<contextual_user") || strings.HasPrefix(text, "<current_time") || strings.HasPrefix(text, "<plugin_instructions") {
			return true
		}
	}
	return false
}

func IsContextualDevMessageContent(message []ContentItem) bool {
	for _, item := range message {
		if isContextualDevFragment(&item) {
			return true
		}
	}
	return false
}

func HasNonContextualDevMessageContent(message []ContentItem) bool {
	for _, item := range message {
		if !isContextualDevFragment(&item) {
			return true
		}
	}
	return false
}

func ParseTurnItem(item *ResponseItem) (*TurnItem, bool) {
	if item == nil {
		return nil, false
	}
	switch item.Kind {
	case ResponseMessage:
		switch item.Role {
		case "user":
			if hook, ok := parseVisibleHookPrompt(item); ok {
				return hook, true
			}
			return parseUserMessage(item)
		case "assistant":
			return parseAgentMessage(item), true
		default:
			return nil, false
		}
	case ResponseReasoning:
		return &TurnItem{Kind: TurnReasoning, ID: item.ID, Summary: append([]string(nil), item.Summary...), RawContent: append([]string(nil), item.RawContent...)}, true
	case ResponseWebSearchCall:
		return &TurnItem{Kind: TurnWebSearch, ID: item.ID, Action: webSearchAction(item.WebSearchAction), Query: webSearchQuery(item.WebSearchAction)}, true
	case ResponseImageGeneration:
		if item.ID == "" {
			return nil, false
		}
		return &TurnItem{Kind: TurnImageGeneration, ID: item.ID, Status: item.ImageStatus, RevisedPrompt: item.RevisedPrompt, Result: item.ImageResult}, true
	default:
		return nil, false
	}
}

func RawAssistantOutputTextFromItem(item *ResponseItem) (string, bool) {
	if item == nil || item.Kind != ResponseMessage || item.Role != "assistant" {
		return "", false
	}
	var b strings.Builder
	for _, content := range item.Content {
		if content.Kind == ContentOutputText {
			b.WriteString(content.Text)
		}
	}
	return b.String(), b.Len() > 0
}

func ImageGenerationArtifactPath(codexHome string, sessionID string, callID string) string {
	return filepath.Join(codexHome, "generated_images", sanitizePathPart(sessionID), sanitizePathPart(callID)+".png")
}

func SaveImageGenerationResult(codexHome string, sessionID string, callID string, result string) (string, error) {
	bytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(result))
	if err != nil {
		return "", err
	}
	path := ImageGenerationArtifactPath(codexHome, sessionID, callID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, bytes, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func StripHiddenAssistantMarkup(text string, planMode bool) string {
	text = stripCitations(text)
	if planMode {
		text = stripPlanBlocks(text)
	}
	return text
}

func parseUserMessage(item *ResponseItem) (*TurnItem, bool) {
	if IsContextualUserMessageContent(item.Content) {
		return nil, false
	}
	content := make([]UserInput, 0, len(item.Content))
	for i, contentItem := range item.Content {
		switch contentItem.Kind {
		case ContentInputText:
			if isImageLabelText(item.Content, i) {
				continue
			}
			content = append(content, UserInput{Kind: UserInputText, Text: contentItem.Text})
		case ContentInputImage:
			content = append(content, UserInput{Kind: UserInputImage, ImageURL: contentItem.ImageURL, Detail: contentItem.Detail})
		}
	}
	return &TurnItem{Kind: TurnUserMessage, ID: item.ID, UserContent: content}, true
}

func parseAgentMessage(item *ResponseItem) *TurnItem {
	var b strings.Builder
	for _, contentItem := range item.Content {
		if contentItem.Kind == ContentInputText || contentItem.Kind == ContentOutputText {
			b.WriteString(contentItem.Text)
		}
	}
	return &TurnItem{Kind: TurnAgentMessage, ID: item.ID, AgentText: b.String(), Phase: item.Phase}
}

func parseVisibleHookPrompt(item *ResponseItem) (*TurnItem, bool) {
	if len(item.Content) != 1 || item.Content[0].Kind != ContentInputText {
		return nil, false
	}
	text := strings.TrimSpace(item.Content[0].Text)
	if !strings.HasPrefix(text, "<hook_prompt") {
		return nil, false
	}
	return &TurnItem{Kind: TurnHookPrompt, ID: item.ID, AgentText: text}, true
}

func isContextualDevFragment(item *ContentItem) bool {
	if item == nil || item.Kind != ContentInputText {
		return false
	}
	trimmed := strings.TrimLeft(item.Text, " \t\r\n")
	lower := strings.ToLower(trimmed)
	for _, prefix := range contextualDeveloperPrefixes {
		if strings.HasPrefix(lower, strings.ToLower(prefix)) {
			return true
		}
	}
	return false
}

func isImageLabelText(items []ContentItem, index int) bool {
	text := strings.TrimSpace(items[index].Text)
	open := strings.HasPrefix(text, "<image") || strings.HasPrefix(text, "<local_image")
	close := text == "</image>" || text == "</local_image>"
	if open && index+1 < len(items) && items[index+1].Kind == ContentInputImage {
		return true
	}
	if close && index > 0 && items[index-1].Kind == ContentInputImage {
		return true
	}
	return false
}

func webSearchAction(action string) string {
	if action == "" {
		return "other"
	}
	return action
}

func webSearchQuery(action string) string {
	if strings.HasPrefix(action, "search:") {
		return strings.TrimSpace(strings.TrimPrefix(action, "search:"))
	}
	return ""
}

func sanitizePathPart(value string) string {
	var b strings.Builder
	for _, ch := range value {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' {
			b.WriteRune(ch)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "generated_image"
	}
	return b.String()
}

func stripCitations(text string) string {
	for {
		start := strings.Index(text, "【")
		if start < 0 {
			return text
		}
		end := strings.Index(text[start:], "】")
		if end < 0 {
			return text
		}
		text = text[:start] + text[start+end+len("】"):]
	}
}

func stripPlanBlocks(text string) string {
	for {
		start := strings.Index(text, "<proposed_plan>")
		if start < 0 {
			return text
		}
		end := strings.Index(text[start:], "</proposed_plan>")
		if end < 0 {
			return text[:start]
		}
		text = text[:start] + text[start+end+len("</proposed_plan>"):]
	}
}
