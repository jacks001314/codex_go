package eventmap

import (
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

type ContentKind string

const (
	ContentInputText  ContentKind = "input_text"
	ContentOutputText ContentKind = "output_text"
	ContentInputImage ContentKind = "input_image"
	// ContentInputAudio classifies an audio content item (Rust
	// `ContentItem::InputAudio`). Go's content items carry no audio URL field
	// yet, so nothing in the pipeline produces this kind today; the audio
	// wrapper labels below still mirror Rust's `user_input_parts` filter.
	ContentInputAudio ContentKind = "input_audio"
)

type ContentItem struct {
	Kind     ContentKind
	Text     string
	ImageURL string
	// FileID is the uploaded-file form of an image reference (Rust
	// `ImageReference::File`); when set it replaces ImageURL on the wire.
	FileID string
	// AudioURL carries an inline audio data URL (Rust
	// `ContentItem::InputAudio`).
	AudioURL string
	Detail   string
}

// Wire renders the item in the shared content-part shape used by prompt
// fragments, world-state contributions and tool outputs (Rust #54776's
// `ContentItem` serde shape): the `type` tag first, then the reference the item
// actually carries (`image_url` or `file_id`), then the optional detail.
func (item ContentItem) Wire() map[string]any {
	switch item.Kind {
	case ContentInputImage:
		out := map[string]any{"type": string(ContentInputImage)}
		if item.FileID != "" {
			out["file_id"] = item.FileID
		} else if item.ImageURL != "" {
			out["image_url"] = item.ImageURL
		}
		if item.Detail != "" {
			out["detail"] = item.Detail
		}
		return out
	case ContentInputAudio:
		return map[string]any{"type": string(ContentInputAudio), "audio_url": item.AudioURL}
	case ContentOutputText:
		return map[string]any{"type": string(ContentOutputText), "text": item.Text}
	default:
		return map[string]any{"type": string(ContentInputText), "text": item.Text}
	}
}

// RustJSON renders the item in Rust's serde byte shape (Rust #54776). Field
// order and omission must match serde because the world-state hash covers these
// bytes.
func (item ContentItem) RustJSON() ([]byte, error) {
	type textItem struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type imageItem struct {
		Type     string `json:"type"`
		ImageURL string `json:"image_url,omitempty"`
		FileID   string `json:"file_id,omitempty"`
		Detail   string `json:"detail,omitempty"`
	}
	type audioItem struct {
		Type     string `json:"type"`
		AudioURL string `json:"audio_url"`
	}
	switch item.Kind {
	case ContentInputImage:
		imageURL, fileID := item.ImageURL, item.FileID
		// Rust's ImageReference is an untagged either/or; keep the file form
		// authoritative when both are set, matching Wire().
		if fileID != "" {
			imageURL = ""
		}
		return json.Marshal(imageItem{Type: string(ContentInputImage), ImageURL: imageURL, FileID: fileID, Detail: item.Detail})
	case ContentInputAudio:
		return json.Marshal(audioItem{Type: string(ContentInputAudio), AudioURL: item.AudioURL})
	case ContentOutputText:
		return json.Marshal(textItem{Type: string(ContentOutputText), Text: item.Text})
	default:
		return json.Marshal(textItem{Type: string(ContentInputText), Text: item.Text})
	}
}

// ContentItemsRustJSON serializes typed content in Rust's serde shape for
// world-state hashing: a single item serializes as that item (Rust fragments
// render exactly one), while several serialize as an array.
func ContentItemsRustJSON(items []ContentItem) []byte {
	if len(items) == 1 {
		if data, err := items[0].RustJSON(); err == nil {
			return data
		}
	}
	raw := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		if data, err := item.RustJSON(); err == nil {
			raw = append(raw, data)
		}
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return []byte("[]")
	}
	return data
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
	Kind    ResponseItemKind
	ID      string
	Role    string
	Phase   string
	Content []ContentItem
	// ContentItemKinds carries the harness-owned content classification aligned
	// with Content, so omitted unsupported media can be tagged (Rust #40277).
	ContentItemKinds []string
	Summary          []string
	RawContent       []string
	WebSearchAction  string
	ImageStatus      string
	RevisedPrompt    string
	ImageResult      string
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

// contextualDeveloperPrefixes mirrors Rust's CONTEXTUAL_DEVELOPER_PREFIXES
// (core/src/event_mapping.rs): a developer message starting with one of these is
// rollback-trimmable contextual evidence.
var contextualDeveloperPrefixes = []string{
	"<permissions instructions>",
	"Approved command prefix saved:",
	"<model_switch>",
	"<managed_developer_instructions>",
	"<persistent_mode>",
	"<apps_instructions>",
	"<collaboration_mode>",
	"<multi_agent_role>",
	"<multi_agent_mode>",
	"<environments_instructions>",
	"<git_attribution>",
	"<plugins_instructions>",
	"<realtime_conversation>",
	"<skills_instructions>",
	"<tools>",
	"<personality_spec>",
	"<token_budget>",
	"<context_window>",
	"<context_window_guidance>",
	"<rollout_budget>",
}

func IsContextualUserMessageContent(message []ContentItem) bool {
	for _, item := range message {
		if isContextualUserFragment(item) {
			return true
		}
	}
	return false
}

// contextualUserMarkedFragments lists Rust's `CONTEXTUAL_USER_FRAGMENT_MATCHERS`
// marker pairs (core/src/context/contextual_user_message.rs): a user-role
// message wrapped in any of these is hidden runtime context, not user
// authorization.
var contextualUserMarkedFragments = [][2]string{
	{"<user_instructions>", "</user_instructions>"},
	{"<environment_context>", "</environment_context>"},
	{"<agent_message_board_notification>", "</agent_message_board_notification>"},
	{"<skills_instructions>", "</skills_instructions>"},
	{"<user_shell_command>", "</user_shell_command>"},
	{"<turn_aborted>", "</turn_aborted>"},
	{"<subagent_notification>", "</subagent_notification>"},
	{"<recommended_plugins>", "</recommended_plugins>"},
}

// contextualUserWarningPrefixes are the legacy warning bodies Rust still
// recognizes as injected context.
var contextualUserWarningPrefixes = []string{
	"Warning: The maximum number of unified exec processes you can keep open is",
	"Warning: Your account was flagged for potentially high-risk cyber activity",
}

const (
	applyPatchExecCommandWarningPrefix = "Warning: apply_patch was requested via "
	applyPatchExecCommandWarningSuffix = "Use the apply_patch tool instead of exec_command."
)

// isContextualUserFragment mirrors Rust's `context::is_contextual_user_fragment`:
// a hook prompt, or any standard contextual user fragment.
func isContextualUserFragment(item ContentItem) bool {
	if item.Kind != ContentInputText {
		return false
	}
	return isHookPromptFragmentText(item.Text) || isStandardContextualUserText(item.Text)
}

// isStandardContextualUserText mirrors `is_standard_contextual_user_text`.
func isStandardContextualUserText(text string) bool {
	for _, markers := range contextualUserMarkedFragments {
		if matchesMarkedText(markers[0], markers[1], text) {
			return true
		}
	}
	if isAdditionalContextFragmentText(text) {
		return true
	}
	if isInternalModelContextFragmentText(text) {
		return true
	}
	trimmed := strings.TrimSpace(text)
	for _, prefix := range contextualUserWarningPrefixes {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return strings.HasPrefix(trimmed, applyPatchExecCommandWarningPrefix) &&
		strings.HasSuffix(trimmed, applyPatchExecCommandWarningSuffix)
}

// matchesMarkedText mirrors `matches_marked_text`: non-empty markers, the text
// trimmed at the start and case-insensitively prefixed by the opening marker and
// trimmed at the end and case-insensitively suffixed by the closing marker.
func matchesMarkedText(start, end, text string) bool {
	if start == "" || end == "" {
		return false
	}
	trimmedStart := strings.TrimLeftFunc(text, unicode.IsSpace)
	if len(trimmedStart) < len(start) || !strings.EqualFold(trimmedStart[:len(start)], start) {
		return false
	}
	trimmedEnd := strings.TrimRightFunc(trimmedStart, unicode.IsSpace)
	return len(trimmedEnd) >= len(end) && strings.EqualFold(trimmedEnd[len(trimmedEnd)-len(end):], end)
}

// isAdditionalContextFragmentText mirrors `AdditionalContextUserFragment`: the
// text is `<external_<key>>...<value>...</external_<key>>`.
func isAdditionalContextFragmentText(text string) bool {
	rest, ok := strings.CutPrefix(strings.TrimSpace(text), "<external_")
	if !ok {
		return false
	}
	key, valueAndClose, ok := strings.Cut(rest, ">")
	if !ok {
		return false
	}
	return strings.HasSuffix(valueAndClose, "</external_"+key+">")
}

// isInternalModelContextFragmentText mirrors `InternalModelContextFragment`: the
// legacy goal context, or `<codex_internal_context source="<valid>">...</codex_internal_context>`.
func isInternalModelContextFragmentText(text string) bool {
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "<goal_context>") && strings.HasSuffix(trimmed, "</goal_context>") {
		return true
	}
	rest, ok := strings.CutPrefix(trimmed, "<codex_internal_context")
	if !ok {
		return false
	}
	rest, ok = strings.CutPrefix(rest, " source=\"")
	if !ok {
		return false
	}
	source, bodyAndClose, ok := strings.Cut(rest, "\">")
	if !ok {
		return false
	}
	return isValidInternalModelSource(source) && strings.HasSuffix(bodyAndClose, "</codex_internal_context>")
}

// isValidInternalModelSource mirrors `is_valid_source`: a lowercase ASCII word.
func isValidInternalModelSource(source string) bool {
	if source == "" {
		return false
	}
	for index, ch := range source {
		if index == 0 {
			if ch < 'a' || ch > 'z' {
				return false
			}
			continue
		}
		if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '_' {
			return false
		}
	}
	return true
}

// isHookPromptFragmentText recognizes a hook prompt fragment.
func isHookPromptFragmentText(text string) bool {
	_, ok := parseHookPromptFragment(text)
	return ok
}

// hookPromptFragment is Rust's `HookPromptFragment`.
type hookPromptFragment struct {
	Text      string
	HookRunID string
}

// parseHookPromptFragment mirrors Rust's `parse_hook_prompt_fragment`: the
// trimmed text is a `<hook_prompt hook_run_id="...">text</hook_prompt>` element
// whose run id is non-empty.
func parseHookPromptFragment(text string) (hookPromptFragment, bool) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "<hook_prompt") {
		return hookPromptFragment{}, false
	}
	var parsed struct {
		XMLName   xml.Name `xml:"hook_prompt"`
		HookRunID string   `xml:"hook_run_id,attr"`
		Text      string   `xml:",chardata"`
	}
	if err := xml.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return hookPromptFragment{}, false
	}
	if strings.TrimSpace(parsed.HookRunID) == "" {
		return hookPromptFragment{}, false
	}
	return hookPromptFragment{Text: parsed.Text, HookRunID: parsed.HookRunID}, true
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

// MemoryCitationBodies returns the body of every <oai-mem-citation> tag in
// text, in order, mirroring the citations vector collected by Rust
// codex_utils_stream_parser::citation::strip_citations. A tag that is not
// closed before EOF is auto-closed, so its buffered body is still returned; a
// partial opening tag at EOF contributes nothing.
func MemoryCitationBodies(text string) []string {
	const open = "<oai-mem-citation>"
	const close = "</oai-mem-citation>"
	var bodies []string
	for {
		start := strings.Index(text, open)
		if start < 0 {
			return bodies
		}
		rest := text[start+len(open):]
		end := strings.Index(rest, close)
		if end < 0 {
			return append(bodies, rest)
		}
		bodies = append(bodies, rest[:end])
		text = rest[end+len(close):]
	}
}

func parseUserMessage(item *ResponseItem) (*TurnItem, bool) {
	if IsContextualUserMessageContent(item.Content) {
		return nil, false
	}
	content := make([]UserInput, 0, len(item.Content))
	for _, index := range UserInputPartIndices(item.Content) {
		contentItem := item.Content[index]
		switch contentItem.Kind {
		case ContentInputText:
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
	// Rust's `parse_visible_hook_prompt_message` only admits a parseable hook
	// prompt with a non-empty run id; an attribute-less tag is an ordinary
	// message.
	if _, ok := parseHookPromptFragment(item.Content[0].Text); !ok {
		return nil, false
	}
	return &TurnItem{Kind: TurnHookPrompt, ID: item.ID, AgentText: strings.TrimSpace(item.Content[0].Text)}, true
}

func isContextualDevFragment(item *ContentItem) bool {
	if item == nil || item.Kind != ContentInputText {
		return false
	}
	trimmed := strings.TrimLeftFunc(item.Text, unicode.IsSpace)
	lower := strings.ToLower(trimmed)
	for _, prefix := range contextualDeveloperPrefixes {
		if strings.HasPrefix(lower, strings.ToLower(prefix)) {
			return true
		}
	}
	return false
}

// UserInputPartIndices returns the indices of the message's user-input parts,
// skipping the media wrapper labels that are not user text. It mirrors Rust's
// `user_input_parts` (core/src/event_mapping.rs:100), which yields the original
// indices so a history edit can carry the right content classifications.
func UserInputPartIndices(message []ContentItem) []int {
	indices := make([]int, 0, len(message))
	for index := range message {
		if message[index].Kind == ContentInputText && isMediaLabelText(message, index) {
			continue
		}
		indices = append(indices, index)
	}
	return indices
}

// isMediaLabelText reports whether the text at index is the wrapper label of the
// media item beside it: an image label wraps an `input_image` and an audio label
// wraps an `input_audio` (Rust's `is_image_*_tag_text` / `is_audio_*_tag_text`
// pairs, `protocol/src/models.rs`: `<image>`/`</image>`, `<audio>`/`</audio>`
// and the `<image name=` / `<audio name=` local variants).
func isMediaLabelText(items []ContentItem, index int) bool {
	// Rust matches the raw text: the label predicates are exact equality or the
	// local `<x name=` prefix, and no surrounding whitespace is stripped
	// (core/src/event_mapping.rs:106-121).
	text := items[index].Text
	if isImageOpenLabelText(text) && index+1 < len(items) && items[index+1].Kind == ContentInputImage {
		return true
	}
	if isImageCloseLabelText(text) && index > 0 && items[index-1].Kind == ContentInputImage {
		return true
	}
	if isAudioOpenLabelText(text) && index+1 < len(items) && items[index+1].Kind == ContentInputAudio {
		return true
	}
	if isAudioCloseLabelText(text) && index > 0 && items[index-1].Kind == ContentInputAudio {
		return true
	}
	return false
}

// isImageOpenLabelText mirrors Rust's `is_image_open_tag_text` (== "<image>") and
// `is_local_image_open_tag_text` (`<image name=` prefix, `>` suffix) in
// protocol/src/models.rs:1823-1838.
func isImageOpenLabelText(text string) bool {
	return text == "<image>" || isLocalOpenTagText(text, "<image name=")
}

// isImageCloseLabelText mirrors Rust's `is_image_close_tag_text`
// (protocol/src/models.rs:1836-1838).
func isImageCloseLabelText(text string) bool {
	return text == "</image>"
}

// isAudioOpenLabelText mirrors Rust's `is_audio_open_tag_text` (== "<audio>") and
// `is_local_audio_open_tag_text` (`<audio name=` prefix, `>` suffix) in
// protocol/src/models.rs:1858-1869.
func isAudioOpenLabelText(text string) bool {
	return text == "<audio>" || isLocalOpenTagText(text, "<audio name=")
}

// isAudioCloseLabelText mirrors Rust's `is_audio_close_tag_text`
// (protocol/src/models.rs:1871-1873).
func isAudioCloseLabelText(text string) bool {
	return text == "</audio>"
}

// isLocalOpenTagText mirrors `is_local_image_open_tag_text` /
// `is_local_audio_open_tag_text`: the local variant carries its label and path
// after the `<x name=` prefix and ends in `>` (protocol/src/models.rs:1823-1826,
// 1858-1861).
func isLocalOpenTagText(text string, prefix string) bool {
	rest, ok := strings.CutPrefix(text, prefix)
	return ok && strings.HasSuffix(rest, ">")
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
	// Rust hides memory citations and Web search citation controls before
	// rendering assistant text. OpenAI has used both E200 and E000 private-use
	// delimiters for Web citations; accept both wire forms. An unterminated
	// opening tag is hidden to EOF, matching the Rust stream parser's finish
	// behavior.
	text = stripInlineHiddenTag(text, "<oai-mem-citation>", "</oai-mem-citation>")
	text = stripInlineHiddenTag(text, "\uE200cite\uE202", "\uE201")
	text = stripInlineHiddenTag(text, "\uE000cite\uE002", "\uE001")
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

// StripCitations removes memory and web citation controls from exported text
// while leaving the stored answer unchanged.
func StripCitations(text string) string {
	return stripCitations(text)
}

func stripInlineHiddenTag(text string, open string, close string) string {
	for {
		start := strings.Index(text, open)
		if start < 0 {
			return text
		}
		rest := text[start+len(open):]
		end := strings.Index(rest, close)
		if end < 0 {
			return text[:start]
		}
		text = text[:start] + rest[end+len(close):]
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
