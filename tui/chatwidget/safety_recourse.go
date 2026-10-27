package chatwidget

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"unicode"

	codextui "codex_go/tui"
	historycell "codex_go/tui/history_cell"
)

// Turn-scoped safety recourse (Rust #57363/#57365
// chatwidget/safety_recourse.rs + safety_recourse/metadata.rs).

const (
	// SafetyRecourseViewID identifies the safer-rewrite picker.
	SafetyRecourseViewID = "safety-recourse"
	// SafetyRecourseStatusHeader is shown while the explanation is pending.
	SafetyRecourseStatusHeader = "Finding an explanation and safer ways to continue…"
)

// SafetyRecourseDomain is the policy domain of a blocked turn.
type SafetyRecourseDomain string

const (
	SafetyRecourseDomainBio   SafetyRecourseDomain = "bio"
	SafetyRecourseDomainCyber SafetyRecourseDomain = "cyber"
)

// SafetyRecourseSuggestion is one selectable safer follow-up.
type SafetyRecourseSuggestion struct {
	Label  string
	Prompt string
}

// SafetyRecourse is the validated explanation plus its safer suggestions.
type SafetyRecourse struct {
	Explanation string
	Suggestions []SafetyRecourseSuggestion
}

// PendingSafetyRecourse tracks the active turn's recourse while the block
// notice is displayed and the terminal policy error has not been handled yet.
type PendingSafetyRecourse struct {
	Domain            SafetyRecourseDomain
	Notice            historycell.SafetyAccessBlockCell
	Result            *SafetyRecourse
	DeferredError     string
	DeferredErrorKind string
}

// NewPendingSafetyRecourse builds the turn-scoped notice for a domain. The
// cyber copy follows the current Daybreak notice.
func NewPendingSafetyRecourse(domain SafetyRecourseDomain, notice codextui.DaybreakNotice) PendingSafetyRecourse {
	pending := PendingSafetyRecourse{Domain: domain}
	switch domain {
	case SafetyRecourseDomainCyber:
		pending.Notice = historycell.NewCyberPolicyErrorEvent(notice)
	default:
		pending.Notice = historycell.NewSafetyAccessBlockEvent()
	}
	return pending
}

// SafetyRecourseNotice builds the notice cell that matches the domain.
func SafetyRecourseNotice(domain SafetyRecourseDomain, notice codextui.DaybreakNotice) historycell.SafetyAccessBlockCell {
	return NewPendingSafetyRecourse(domain, notice).Notice
}

// IsBioPolicyError reports whether a terminal error message is the biological
// policy block (Rust turn_runtime::is_bio_policy_error).
func IsBioPolicyError(message string) bool {
	if isSafetyAccessBlockMessage(message) {
		return true
	}
	var response struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(message), &response); err != nil {
		return false
	}
	return response.Error.Code == "bio_policy" || isSafetyAccessBlockMessage(response.Error.Message)
}

func isSafetyAccessBlockMessage(message string) bool {
	return strings.HasPrefix(message, SafetyAccessBlockPrefix) ||
		strings.HasPrefix(message, "This content was flagged for possible biological risk.") ||
		strings.HasPrefix(message, "This request has been flagged for possible biological risk.")
}

// SafetyRecourseMatchesError reports whether a terminal error belongs to the
// pending recourse domain. Bio blocks are recognized from the message, while
// cyber blocks require the app-server error kind (Rust matches
// CodexErrorInfo::CyberPolicy).
func SafetyRecourseMatchesError(domain SafetyRecourseDomain, message, kind string) bool {
	switch domain {
	case SafetyRecourseDomainBio:
		return IsBioPolicyError(message)
	case SafetyRecourseDomainCyber:
		return strings.EqualFold(strings.TrimSpace(kind), "cyberPolicy")
	default:
		return false
	}
}

type safetyRecourseBlock struct {
	SchemaVersion *int    `json:"schema_version"`
	Domain        *string `json:"domain"`
	Status        *string `json:"status"`
}

type safetyRecourseSourceMessage struct {
	ID   *string `json:"id"`
	Kind *string `json:"kind"`
	Role *string `json:"role"`
}

type safetyRecourseSuggestionWire struct {
	Label      *string `json:"label"`
	PromptText *string `json:"prompt_text"`
}

type safetyRecourseWire struct {
	SchemaVersion *int                           `json:"schema_version"`
	Domain        *string                        `json:"domain"`
	Source        *string                        `json:"source"`
	SourceMessage *safetyRecourseSourceMessage   `json:"source_message"`
	Topic         *string                        `json:"topic"`
	Suggestions   []safetyRecourseSuggestionWire `json:"suggestions"`
}

// ParseSafetyRecourseMetadata validates the desktop safety-recourse metadata
// contract (Rust safety_recourse/metadata.rs). It returns the domain and, once
// the server has finished preparing it, the validated recourse. A nil recourse
// with ok=true means the metadata announced a block that is still loading.
func ParseSafetyRecourseMetadata(metadata any) (SafetyRecourseDomain, *SafetyRecourse, bool) {
	object, ok := safetyRecourseObject(metadata)
	if !ok {
		return "", nil, false
	}
	raw, present := object["safety_recourse"]
	if !present || isJSONNull(raw) {
		block, ok := decodeStrict[safetyRecourseBlock](object["safety_block"])
		if !ok || block.SchemaVersion == nil || block.Domain == nil || block.Status == nil {
			return "", nil, false
		}
		domain, ok := parseSafetyRecourseDomain(*block.Domain)
		if !ok || *block.SchemaVersion != 1 || *block.Status != "loading" {
			return "", nil, false
		}
		return domain, nil, true
	}
	wire, ok := decodeStrict[safetyRecourseWire](raw)
	if !ok || wire.SchemaVersion == nil || *wire.SchemaVersion != 1 || wire.Domain == nil ||
		wire.Source == nil || wire.SourceMessage == nil || wire.Topic == nil ||
		wire.SourceMessage.ID == nil || wire.SourceMessage.Kind == nil || wire.SourceMessage.Role == nil {
		return "", nil, false
	}
	domain, ok := parseSafetyRecourseDomain(*wire.Domain)
	if !ok || len(wire.Suggestions) != 3 || !validSafetyRecourseText(*wire.SourceMessage.ID, 256) {
		return "", nil, false
	}
	bioLead, cyberLead, bioOffer, ok := safetyRecourseLead(
		*wire.Source,
		*wire.SourceMessage.Kind,
		*wire.SourceMessage.Role,
	)
	if !ok {
		return "", nil, false
	}
	concern, help, ok := safetyRecourseTopic(domain, *wire.Topic)
	if !ok {
		return "", nil, false
	}
	labels := make([]string, len(wire.Suggestions))
	for index, suggestion := range wire.Suggestions {
		if suggestion.Label != nil {
			labels[index] = strings.ToLower(strings.TrimSpace(*suggestion.Label))
		}
	}
	seenPrompts := map[string]bool{}
	suggestions := make([]SafetyRecourseSuggestion, 0, len(wire.Suggestions))
	for _, suggestion := range wire.Suggestions {
		if suggestion.Label == nil || suggestion.PromptText == nil {
			return "", nil, false
		}
		if !validSafetyRecourseText(*suggestion.Label, 80) || !validSafetyRecourseText(*suggestion.PromptText, 2000) {
			return "", nil, false
		}
		label := strings.TrimSpace(*suggestion.Label)
		prompt := strings.TrimSpace(*suggestion.PromptText)
		if seenPrompts[prompt] {
			return "", nil, false
		}
		seenPrompts[prompt] = true
		if safetyRecourseSuggestionUnsafe(domain, label, prompt, labels) {
			continue
		}
		suggestions = append(suggestions, SafetyRecourseSuggestion{Label: label, Prompt: prompt})
	}
	lead := bioLead
	offer := bioOffer
	if domain == SafetyRecourseDomainCyber {
		lead = cyberLead
		offer = safetyRecourseOverviewOffer
	}
	if offer == "" {
		offer = safetyRecourseOverviewOffer
	}
	return domain, &SafetyRecourse{
		Explanation: "We take extra caution " + lead + concern + ". " + offer + help + ".",
		Suggestions: suggestions,
	}, true
}

const safetyRecourseOverviewOffer = "We can still help with an overview of "

func safetyRecourseObject(metadata any) (map[string]json.RawMessage, bool) {
	switch value := metadata.(type) {
	case nil:
		return nil, false
	case map[string]json.RawMessage:
		return value, true
	case []byte:
		return decodeSafetyRecourseObject(value)
	case json.RawMessage:
		return decodeSafetyRecourseObject(value)
	case string:
		return decodeSafetyRecourseObject([]byte(value))
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, false
		}
		return decodeSafetyRecourseObject(encoded)
	}
}

func decodeSafetyRecourseObject(data []byte) (map[string]json.RawMessage, bool) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var object map[string]json.RawMessage
	if err := decoder.Decode(&object); err != nil || object == nil {
		return nil, false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, false
	}
	return object, true
}

func decodeStrict[T any](raw json.RawMessage) (T, bool) {
	var value T
	if len(raw) == 0 {
		return value, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return value, false
	}
	return value, true
}

func isJSONNull(raw json.RawMessage) bool {
	return len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func parseSafetyRecourseDomain(value string) (SafetyRecourseDomain, bool) {
	switch value {
	case string(SafetyRecourseDomainBio):
		return SafetyRecourseDomainBio, true
	case string(SafetyRecourseDomainCyber):
		return SafetyRecourseDomainCyber, true
	default:
		return "", false
	}
}

func validSafetyRecourseText(value string, maxChars int) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed != "" && len([]rune(trimmed)) <= maxChars
}

func safetyRecourseLead(source, kind, role string) (bioLead, cyberLead, bioOffer string, ok bool) {
	switch source + "\x00" + kind + "\x00" + role {
	case "user_request\x00ordinary_message\x00user":
		return "with requests involving ", "with requests that could enable ", "", true
	case "assistant_response\x00ordinary_message\x00assistant":
		return "when a response would provide information about ", "when a response could enable ", "", true
	case "file_content\x00file_tool_call\x00assistant",
		"file_content\x00file_tool_result\x00tool":
		return "when content from a file involves ", "when content could enable ",
			"You can continue without this file or get an overview of ", true
	case "web_search_result\x00web_tool_call\x00assistant",
		"web_search_result\x00web_tool_result\x00tool":
		return "when a web search result contains information about ", "when content could enable ",
			"You can retry without web search or get an overview of ", true
	case "other_tool\x00other_tool_call\x00assistant",
		"other_tool\x00other_tool_result\x00tool":
		return "when content involves ", "when content could enable ", "", true
	}
	return "", "", "", false
}

func safetyRecourseTopic(domain SafetyRecourseDomain, topic string) (concern, help string, ok bool) {
	switch domain {
	case SafetyRecourseDomainBio:
		switch topic {
		case "biological_agent_access_and_safeguards":
			return "access to biological materials and related safety controls", "access processes and safeguards", true
		case "biological_agent_growth_or_production":
			return "growing or producing biological agents", "the science and safety considerations", true
		case "risky_biological_experimentation_or_modification":
			return "biological experimentation or modification that could pose safety risks", "the research goals and safety considerations", true
		case "harmful_biological_exposure_or_release":
			return "biological exposure or release that could cause harm", "the risks, prevention, and response", true
		case "risky_biological_activities":
			return "biological activities that could pose safety risks", "biological research and safety considerations", true
		}
	case SafetyRecourseDomainCyber:
		switch topic {
		case "unauthorized_account_or_system_access":
			return "access to accounts or systems beyond permitted scope", "account security and recovery", true
		case "high_risk_cybersecurity_tools":
			return "system disruption or the development of high-risk cyber tools", "system protection and incident recovery", true
		case "sensitive_data_or_private_communications":
			return "sensitive data collection or private communications monitoring", "data protection and privacy safeguards", true
		case "software_vulnerabilities_or_security_weaknesses":
			return "capability or technique development for bypassing security controls", "safe assessment and remediation", true
		case "risky_cybersecurity_activities":
			return "cyber activities that pose security or safety risks", "cybersecurity fundamentals and defensive best practices", true
		}
	}
	return "", "", false
}

// safetyRecourseSuggestionUnsafe reports whether the composer could not submit
// the rewrite safely. Such suggestions are dropped without rejecting the whole
// contract (Rust metadata::parse's continue branch).
func safetyRecourseSuggestionUnsafe(domain SafetyRecourseDomain, label, prompt string, labels []string) bool {
	if strings.ContainsFunc(label, unicode.IsControl) ||
		strings.ContainsAny(label, "\u2028\u2029") ||
		len(prompt) > 1000 ||
		strings.HasPrefix(prompt, "!") ||
		strings.HasPrefix(prompt, "/") ||
		codextui.ParseAsyncQuestionReplies(prompt) != nil ||
		containsToolMention(prompt) {
		return true
	}
	if domain == SafetyRecourseDomainCyber && strings.EqualFold(label, "Enable Daybreak") {
		return true
	}
	key := strings.ToLower(label)
	count := 0
	for _, candidate := range labels {
		if candidate == key {
			count++
		}
	}
	return count > 1
}

func containsToolMention(text string) bool {
	mentions := ExtractToolMentionsFromText(text)
	plugins := ExtractToolMentionsFromTextWithSigil(text, byte(codextui.PluginTextMentionSigil))
	return len(mentions.Names) > 0 || len(mentions.LinkedPaths) > 0 || len(plugins.LinkedPaths) > 0
}
