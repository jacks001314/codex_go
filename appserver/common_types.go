package appserver

import "encoding/json"

type AutoReviewDecisionSource string

const AutoReviewDecisionSourceAgent AutoReviewDecisionSource = "agent"

type CapabilityRootLocationType string

const CapabilityRootLocationEnvironment CapabilityRootLocationType = "environment"

type CapabilityRootLocation struct {
	Type          CapabilityRootLocationType `json:"type"`
	EnvironmentID string                     `json:"environmentId"`
	Path          string                     `json:"path"`
}

type SelectedCapabilityRoot struct {
	ID       string                 `json:"id"`
	Location CapabilityRootLocation `json:"location"`
}

type ForcedChatgptWorkspaceIds any

type NonSteerableTurnKind string

const (
	NonSteerableTurnKindReview  NonSteerableTurnKind = "review"
	NonSteerableTurnKindCompact NonSteerableTurnKind = "compact"
)

type CodexErrorInfo any

type MemoryCitation struct {
	Entries   []MemoryCitationEntry `json:"entries"`
	ThreadIDs []string              `json:"threadIds"`
}

func (c *MemoryCitation) MarshalJSON() ([]byte, error) {
	entries := append([]MemoryCitationEntry(nil), c.Entries...)
	if entries == nil {
		entries = []MemoryCitationEntry{}
	}
	threadIDs := append([]string(nil), c.ThreadIDs...)
	if threadIDs == nil {
		threadIDs = []string{}
	}
	return json.Marshal(struct {
		Entries   []MemoryCitationEntry `json:"entries"`
		ThreadIDs []string              `json:"threadIds"`
	}{
		Entries:   entries,
		ThreadIDs: threadIDs,
	})
}

type MemoryCitationEntry struct {
	Path      string `json:"path"`
	LineStart int64  `json:"lineStart"`
	LineEnd   int64  `json:"lineEnd"`
	Note      string `json:"note"`
}

type NetworkApprovalProtocol string

const (
	NetworkApprovalHTTP      NetworkApprovalProtocol = "http"
	NetworkApprovalHTTPS     NetworkApprovalProtocol = "https"
	NetworkApprovalSocks5TCP NetworkApprovalProtocol = "socks5_tcp"
	NetworkApprovalSocks5UDP NetworkApprovalProtocol = "socks5_udp"
)

type NetworkApprovalContext struct {
	Host     string                  `json:"host"`
	Protocol NetworkApprovalProtocol `json:"protocol"`
}

type NetworkPolicyRuleAction string

const (
	NetworkPolicyRuleAllow NetworkPolicyRuleAction = "allow"
	NetworkPolicyRuleDeny  NetworkPolicyRuleAction = "deny"
)

type NetworkDomainPermission = NetworkPolicyRuleAction
type NetworkUnixSocketPermission = NetworkPolicyRuleAction

type NetworkPolicyAmendment struct {
	Host   string                  `json:"host"`
	Action NetworkPolicyRuleAction `json:"action"`
}

type RequestPermissionProfile = GrantedPermissionProfile

type ReviewDelivery string

const (
	ReviewDeliveryInline   ReviewDelivery = "inline"
	ReviewDeliveryDetached ReviewDelivery = "detached"
)

type ConversationTextRole string

const (
	ConversationTextUser      ConversationTextRole = "user"
	ConversationTextDeveloper ConversationTextRole = "developer"
	ConversationTextAssistant ConversationTextRole = "assistant"
)

type ConversationGitInfo struct {
	SHA       *string `json:"sha"`
	Branch    *string `json:"branch"`
	OriginURL *string `json:"origin_url"`
}

type ConversationSummary struct {
	ConversationID string               `json:"conversationId"`
	Path           string               `json:"path"`
	Preview        string               `json:"preview"`
	Timestamp      *string              `json:"timestamp"`
	UpdatedAt      *string              `json:"updatedAt"`
	ModelProvider  string               `json:"modelProvider"`
	CWD            string               `json:"cwd"`
	CLIVersion     string               `json:"cliVersion"`
	Source         SessionSource        `json:"source"`
	GitInfo        *ConversationGitInfo `json:"gitInfo"`
}

type ModeKind string

const (
	ModeKindPlan    ModeKind = "plan"
	ModeKindDefault ModeKind = "default"
)

type CollaborationSettings struct {
	Model                 string           `json:"model"`
	ReasoningEffort       *ReasoningEffort `json:"reasoning_effort"`
	DeveloperInstructions *string          `json:"developer_instructions"`
}

type CollaborationMode struct {
	Mode     ModeKind              `json:"mode"`
	Settings CollaborationSettings `json:"settings"`
}

type MultiAgentMode string

const (
	MultiAgentModeNone                MultiAgentMode = "none"
	MultiAgentModeExplicitRequestOnly MultiAgentMode = "explicitRequestOnly"
	MultiAgentModeProactive           MultiAgentMode = "proactive"
)

type Personality string

const (
	PersonalityNone      Personality = "none"
	PersonalityFriendly  Personality = "friendly"
	PersonalityPragmatic Personality = "pragmatic"
)

type ReasoningEffort string

type ReasoningSummary string

const (
	ReasoningSummaryAuto     ReasoningSummary = "auto"
	ReasoningSummaryConcise  ReasoningSummary = "concise"
	ReasoningSummaryDetailed ReasoningSummary = "detailed"
	ReasoningSummaryNone     ReasoningSummary = "none"
)

type Verbosity string

const (
	VerbosityLow    Verbosity = "low"
	VerbosityMedium Verbosity = "medium"
	VerbosityHigh   Verbosity = "high"
)

type InputModality string

const (
	InputModalityText  InputModality = "text"
	InputModalityImage InputModality = "image"
	InputModalityAudio InputModality = "audio"
)

type MessagePhase string

const (
	MessagePhaseCommentary  MessagePhase = "commentary"
	MessagePhaseFinalAnswer MessagePhase = "final_answer"
)

type RealtimeConversationVersion string

const (
	RealtimeConversationV1 RealtimeConversationVersion = "v1"
	RealtimeConversationV2 RealtimeConversationVersion = "v2"
	RealtimeConversationV3 RealtimeConversationVersion = "v3"
)

type RealtimeOutputModality string

const (
	RealtimeOutputText  RealtimeOutputModality = "text"
	RealtimeOutputAudio RealtimeOutputModality = "audio"
)

type RealtimeVoice string

type RealtimeVoicesList struct {
	V1        []RealtimeVoice `json:"v1"`
	V2        []RealtimeVoice `json:"v2"`
	DefaultV1 RealtimeVoice   `json:"defaultV1"`
	DefaultV2 RealtimeVoice   `json:"defaultV2"`
}

func (v *RealtimeVoicesList) MarshalJSON() ([]byte, error) {
	v1 := append([]RealtimeVoice(nil), v.V1...)
	if v1 == nil {
		v1 = []RealtimeVoice{}
	}
	v2 := append([]RealtimeVoice(nil), v.V2...)
	if v2 == nil {
		v2 = []RealtimeVoice{}
	}
	return json.Marshal(struct {
		V1        []RealtimeVoice `json:"v1"`
		V2        []RealtimeVoice `json:"v2"`
		DefaultV1 RealtimeVoice   `json:"defaultV1"`
		DefaultV2 RealtimeVoice   `json:"defaultV2"`
	}{
		V1:        v1,
		V2:        v2,
		DefaultV1: v.DefaultV1,
		DefaultV2: v.DefaultV2,
	})
}

type SandboxWorkspaceWrite struct {
	WritableRoots       []string `json:"writable_roots"`
	NetworkAccess       bool     `json:"network_access"`
	ExcludeTmpdirEnvVar bool     `json:"exclude_tmpdir_env_var"`
	ExcludeSlashTmp     bool     `json:"exclude_slash_tmp"`
}

func (s *SandboxWorkspaceWrite) MarshalJSON() ([]byte, error) {
	writableRoots := append([]string(nil), s.WritableRoots...)
	if writableRoots == nil {
		writableRoots = []string{}
	}
	return json.Marshal(struct {
		WritableRoots       []string `json:"writable_roots"`
		NetworkAccess       bool     `json:"network_access"`
		ExcludeTmpdirEnvVar bool     `json:"exclude_tmpdir_env_var"`
		ExcludeSlashTmp     bool     `json:"exclude_slash_tmp"`
	}{
		WritableRoots:       writableRoots,
		NetworkAccess:       s.NetworkAccess,
		ExcludeTmpdirEnvVar: s.ExcludeTmpdirEnvVar,
		ExcludeSlashTmp:     s.ExcludeSlashTmp,
	})
}
