package appserver

import "encoding/json"

type AbsolutePathBuf = string
type ThreadId = string
type GitSha = string
type RequestId = RequestID
type JsonValue = any

type AuthMode string

const (
	AuthModeAPIKey              AuthMode = "apikey"
	AuthModeChatGPT             AuthMode = "chatgpt"
	AuthModeChatGPTAuthTokens   AuthMode = "chatgptAuthTokens"
	AuthModeAgentIdentity       AuthMode = "agentIdentity"
	AuthModePersonalAccessToken AuthMode = "personalAccessToken"
	AuthModeBedrockAPIKey       AuthMode = "bedrockApiKey"
)

type AmazonBedrockCredentialSource string

const (
	AmazonBedrockCredentialSourceCodexManaged AmazonBedrockCredentialSource = "codexManaged"
	AmazonBedrockCredentialSourceAWSManaged   AmazonBedrockCredentialSource = "awsManaged"
)

type AutoCompactTokenLimitScope string

const (
	AutoCompactTokenLimitScopeTotal           AutoCompactTokenLimitScope = "total"
	AutoCompactTokenLimitScopeBodyAfterPrefix AutoCompactTokenLimitScope = "body_after_prefix"
)

type AgentMessageInputContent struct {
	Type             string `json:"type"`
	Text             string `json:"text,omitempty"`
	EncryptedContent string `json:"encrypted_content,omitempty"`
}

func (c *AgentMessageInputContent) MarshalJSON() ([]byte, error) {
	contentType := c.Type
	if contentType == "" {
		if c.EncryptedContent != "" {
			contentType = "encrypted_content"
		} else {
			contentType = "input_text"
		}
	}
	if contentType == "encrypted_content" {
		return json.Marshal(struct {
			Type             string `json:"type"`
			EncryptedContent string `json:"encrypted_content"`
		}{Type: contentType, EncryptedContent: c.EncryptedContent})
	}
	return json.Marshal(struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{Type: contentType, Text: c.Text})
}

type FunctionCallOutputContentItem struct {
	Type             string  `json:"type"`
	Text             string  `json:"text,omitempty"`
	ImageURL         string  `json:"image_url,omitempty"`
	Detail           *string `json:"detail,omitempty"`
	EncryptedContent string  `json:"encrypted_content,omitempty"`
}

func (c *FunctionCallOutputContentItem) MarshalJSON() ([]byte, error) {
	contentType := c.Type
	if contentType == "" {
		switch {
		case c.EncryptedContent != "":
			contentType = "encrypted_content"
		case c.ImageURL != "":
			contentType = "input_image"
		default:
			contentType = "input_text"
		}
	}
	switch contentType {
	case "encrypted_content":
		return json.Marshal(struct {
			Type             string `json:"type"`
			EncryptedContent string `json:"encrypted_content"`
		}{Type: contentType, EncryptedContent: c.EncryptedContent})
	case "input_image":
		return json.Marshal(struct {
			Type     string  `json:"type"`
			ImageURL string  `json:"image_url"`
			Detail   *string `json:"detail,omitempty"`
		}{Type: contentType, ImageURL: c.ImageURL, Detail: cloneString(c.Detail)})
	default:
		return json.Marshal(struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{Type: contentType, Text: c.Text})
	}
}

type FunctionCallOutputBody any

type ReasoningItemContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type ReasoningItemReasoningSummary struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type InternalChatMessageMetadataPassthrough struct {
	TurnID string `json:"turn_id,omitempty"`
}

type LocalShellStatus string

const (
	LocalShellCompleted  LocalShellStatus = "completed"
	LocalShellInProgress LocalShellStatus = "in_progress"
	LocalShellIncomplete LocalShellStatus = "incomplete"
)

type LocalShellExecAction struct {
	Command          []string          `json:"command"`
	TimeoutMS        *uint64           `json:"timeout_ms"`
	WorkingDirectory *string           `json:"working_directory"`
	Env              map[string]string `json:"env"`
	User             *string           `json:"user"`
}

func (a *LocalShellExecAction) MarshalJSON() ([]byte, error) {
	command := append([]string(nil), a.Command...)
	if command == nil {
		command = []string{}
	}
	var env map[string]string
	if a.Env != nil {
		env = make(map[string]string, len(a.Env))
		for key, value := range a.Env {
			env[key] = value
		}
	}
	return json.Marshal(struct {
		Command          []string          `json:"command"`
		TimeoutMS        *uint64           `json:"timeout_ms"`
		WorkingDirectory *string           `json:"working_directory"`
		Env              map[string]string `json:"env"`
		User             *string           `json:"user"`
	}{
		Command:          command,
		TimeoutMS:        a.TimeoutMS,
		WorkingDirectory: cloneString(a.WorkingDirectory),
		Env:              env,
		User:             cloneString(a.User),
	})
}

type LocalShellAction struct {
	Type string `json:"type"`
	LocalShellExecAction
}

func (a *LocalShellAction) MarshalJSON() ([]byte, error) {
	actionType := a.Type
	if actionType == "" {
		actionType = "exec"
	}
	execAction, err := a.LocalShellExecAction.MarshalJSON()
	if err != nil {
		return nil, err
	}
	var body map[string]any
	if err := json.Unmarshal(execAction, &body); err != nil {
		return nil, err
	}
	body["type"] = actionType
	return json.Marshal(body)
}

type InternalSessionSource string

const InternalSessionSourceMemoryConsolidation InternalSessionSource = "memory_consolidation"

type SubAgentSource any
