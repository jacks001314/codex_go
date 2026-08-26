package chatwidget

import "strings"

const (
	ChatWidgetDefaultModelDisplayName = "loading"
	ChatWidgetModeDefault             = "default"
	ChatWidgetFeatureApps             = "apps"
	ChatWidgetFeaturePreventIdleSleep = "prevent_idle_sleep"
)

var ChatWidgetPlaceholders = []string{
	"Explain this codebase",
	"Summarize recent commits",
	"Implement {feature}",
	"Find and fix a bug in @filename",
	"Write tests for @filename",
	"Improve documentation in @filename",
	"Run /review on my current changes",
	"Use /skills to list available skills",
}

var ChatWidgetSidePlaceholders = []string{
	"Check recently modified functions for compatibility",
	"How many files have been modified?",
	"Will this algorithm scale well?",
}

type ChatWidgetConfig struct {
	Model               string
	CWD                 string
	SessionConfigured   bool
	CollaborationMode   string
	Personality         Personality
	FeatureSettings     map[string]bool
	Connectors          ChatWidgetConnectors
	HasChatGPTAccount   bool
	PlanType            string
	HasCodexBackendAuth bool
	IsFirstRun          bool
	InitialUserMessage  *UserMessage
}

type ChatWidgetSnapshot struct {
	Config                      ChatWidgetConfig
	Status                      StatusState
	InputQueue                  InputQueueState
	Notifications               NotificationState
	SessionHeader               ChatSessionHeader
	Runtime                     TurnRuntimeState
	ConnectorsState             ConnectorsState
	NormalPlaceholderText       string
	SidePlaceholderText         string
	CurrentCollaborationMode    string
	PlaceholderSessionHeader    bool
	ShowWelcomeBanner           bool
	ConnectorsEnabled           bool
	TokenActivityCommandEnabled bool
	PreventIdleSleep            bool
	CurrentCWD                  string
}

func NewChatWidgetSnapshot(config ChatWidgetConfig) ChatWidgetSnapshot {
	config = cloneChatWidgetConfig(config)
	model := strings.TrimSpace(config.Model)
	config.Model = model
	headerModel := model
	if headerModel == "" {
		headerModel = ChatWidgetDefaultModelDisplayName
	}
	mode := strings.TrimSpace(config.CollaborationMode)
	if mode == "" {
		mode = ChatWidgetModeDefault
	}
	streaming := NewChatStreamingState(80)
	runtime := TurnRuntimeState{Streaming: *streaming}
	runtime.ChatGPTPlanType = config.PlanType
	runtime.UpdateTaskRunningState()
	return ChatWidgetSnapshot{
		Config:                      config,
		Status:                      NewStatusState(),
		SessionHeader:               NewChatSessionHeader(headerModel),
		Runtime:                     runtime,
		NormalPlaceholderText:       ChatWidgetPlaceholder(0),
		SidePlaceholderText:         ChatWidgetSidePlaceholder(0),
		CurrentCollaborationMode:    mode,
		PlaceholderSessionHeader:    !config.SessionConfigured,
		ShowWelcomeBanner:           config.IsFirstRun,
		ConnectorsEnabled:           config.FeatureEnabled(ChatWidgetFeatureApps) && config.HasChatGPTAccount,
		TokenActivityCommandEnabled: config.HasCodexBackendAuth,
		PreventIdleSleep:            config.FeatureEnabled(ChatWidgetFeaturePreventIdleSleep),
		CurrentCWD:                  strings.TrimSpace(config.CWD),
	}
}

func (c ChatWidgetConfig) FeatureEnabled(name string) bool {
	if c.FeatureSettings == nil {
		return false
	}
	return c.FeatureSettings[strings.TrimSpace(name)]
}

func ChatWidgetPlaceholder(index int) string {
	return valueAtWrapped(ChatWidgetPlaceholders, index)
}

func ChatWidgetSidePlaceholder(index int) string {
	return valueAtWrapped(ChatWidgetSidePlaceholders, index)
}

func cloneChatWidgetConfig(config ChatWidgetConfig) ChatWidgetConfig {
	if config.FeatureSettings != nil {
		cloned := make(map[string]bool, len(config.FeatureSettings))
		for key, value := range config.FeatureSettings {
			cloned[key] = value
		}
		config.FeatureSettings = cloned
	}
	if config.InitialUserMessage != nil {
		message := *config.InitialUserMessage
		config.InitialUserMessage = &message
	}
	return config
}

func valueAtWrapped(values []string, index int) string {
	if len(values) == 0 {
		return ""
	}
	if index < 0 {
		index = 0
	}
	return values[index%len(values)]
}
