package chatwidget

import (
	"strings"

	"codex_go/apps"
	"codex_go/appserver"
	pluginapi "codex_go/plugin"
)

const (
	UserShellCommandHelpTitle = "Shell commands"
	UserShellCommandHelpHint  = "Type ! followed by a command to run it locally."
)

type SubmittedInputKind string

const (
	SubmittedInputText        SubmittedInputKind = "text"
	SubmittedInputLocalImage  SubmittedInputKind = "local_image"
	SubmittedInputRemoteImage SubmittedInputKind = "remote_image"
	SubmittedInputLocalAudio  SubmittedInputKind = "local_audio"
	SubmittedInputRemoteAudio SubmittedInputKind = "remote_audio"
	SubmittedInputSkill       SubmittedInputKind = "skill"
	SubmittedInputPlugin      SubmittedInputKind = "plugin"
	SubmittedInputApp         SubmittedInputKind = "app"
	SubmittedInputMention     SubmittedInputKind = "mention"
)

type SubmittedInputItem struct {
	Kind SubmittedInputKind
	Text string
	Path string
	URL  string
	Name string
}

type SubmissionMentionCatalog struct {
	Skills  []appserver.SkillsListEntry
	Plugins []pluginapi.PluginSummary
	Apps    []apps.AppEntry
}

type SubmissionOptions struct {
	SessionConfigured     bool
	CurrentModelHasImages bool
	CurrentModelHasAudio  bool
	AgentTurnRunning      bool
	ShellEscapePolicy     ShellEscapePolicy
	EffectiveModel        string
	RequireModel          bool
	MentionCatalog        SubmissionMentionCatalog
}

type SubmissionDecision struct {
	Accepted                bool
	QueueUntilConfigured    bool
	QueueAtFront            bool
	QueueDrain              QueueDrain
	ShellCommand            string
	RestoreBlockedImages    bool
	RestoreBlockedAudio     bool
	RestoreUnavailableModel bool
	ErrorMessage            string
	Items                   []SubmittedInputItem
	HistoryRecord           UserMessageHistoryRecord
	PendingSteer            *PendingSteer
	UserTurnPendingStart    bool
	InfoTitle               string
	InfoHint                string
}

func DecideUserMessageSubmission(message UserMessage, record UserMessageHistoryRecord, options SubmissionOptions) SubmissionDecision {
	if !options.SessionConfigured {
		return SubmissionDecision{
			Accepted:             true,
			QueueUntilConfigured: true,
			QueueAtFront:         true,
			QueueDrain:           QueueDrainStop,
			HistoryRecord:        recordOrText(record),
		}
	}
	if !message.HasContent() {
		return SubmissionDecision{QueueDrain: QueueDrainContinue, HistoryRecord: recordOrText(record)}
	}
	if (len(message.LocalImages) > 0 || len(message.RemoteImageURLs) > 0) && !options.CurrentModelHasImages {
		return SubmissionDecision{
			QueueDrain:           QueueDrainContinue,
			RestoreBlockedImages: true,
			HistoryRecord:        recordOrText(record),
		}
	}
	if (len(message.LocalAudio) > 0 || len(message.RemoteAudioURLs) > 0) && !options.CurrentModelHasAudio {
		return SubmissionDecision{QueueDrain: QueueDrainContinue, RestoreBlockedAudio: true, HistoryRecord: recordOrText(record)}
	}
	if options.ShellEscapePolicy == "" {
		options.ShellEscapePolicy = ShellEscapeAllow
	}
	text := message.Text
	if options.ShellEscapePolicy == ShellEscapeAllow && strings.HasPrefix(text, "!") {
		command := strings.TrimSpace(strings.TrimPrefix(text, "!"))
		if command == "" {
			return SubmissionDecision{
				Accepted:      true,
				QueueDrain:    QueueDrainContinue,
				HistoryRecord: recordOrText(record),
				InfoTitle:     UserShellCommandHelpTitle,
				InfoHint:      UserShellCommandHelpHint,
			}
		}
		return SubmissionDecision{
			Accepted:      true,
			QueueDrain:    QueueDrainStop,
			ShellCommand:  command,
			HistoryRecord: recordOrText(record),
		}
	}

	items := make([]SubmittedInputItem, 0, len(message.RemoteImageURLs)+len(message.LocalImages)+len(message.RemoteAudioURLs)+len(message.LocalAudio)+1)
	for _, url := range message.RemoteImageURLs {
		if strings.TrimSpace(url) != "" {
			items = append(items, SubmittedInputItem{Kind: SubmittedInputRemoteImage, URL: strings.TrimSpace(url)})
		}
	}
	for _, path := range message.LocalImages {
		if strings.TrimSpace(path) != "" {
			items = append(items, SubmittedInputItem{Kind: SubmittedInputLocalImage, Path: strings.TrimSpace(path)})
		}
	}
	for _, url := range message.RemoteAudioURLs {
		if strings.TrimSpace(url) != "" {
			items = append(items, SubmittedInputItem{Kind: SubmittedInputRemoteAudio, URL: strings.TrimSpace(url)})
		}
	}
	for _, path := range message.LocalAudio {
		if strings.TrimSpace(path) != "" {
			items = append(items, SubmittedInputItem{Kind: SubmittedInputLocalAudio, Path: strings.TrimSpace(path)})
		}
	}
	if text != "" {
		items = append(items, SubmittedInputItem{Kind: SubmittedInputText, Text: text})
	}
	items = appendSubmissionMentionItems(items, text, message.MentionBindings, options.MentionCatalog)
	if options.RequireModel && strings.TrimSpace(options.EffectiveModel) == "" {
		return SubmissionDecision{
			QueueDrain:              QueueDrainContinue,
			RestoreUnavailableModel: true,
			ErrorMessage:            ThreadModelUnavailableMessage,
			HistoryRecord:           recordOrText(record),
		}
	}
	var pendingSteer *PendingSteer
	if options.AgentTurnRunning {
		pendingSteer = &PendingSteer{
			UserMessage:   cloneUserMessage(message),
			HistoryRecord: recordOrText(record),
			CompareKey:    PendingSteerCompareKeyFromSubmittedItems(items),
		}
	}
	return SubmissionDecision{
		Accepted:             len(items) > 0,
		QueueDrain:           QueueDrainStop,
		Items:                items,
		HistoryRecord:        recordOrText(record),
		PendingSteer:         pendingSteer,
		UserTurnPendingStart: !options.AgentTurnRunning && len(items) > 0,
	}
}

func (s *InputQueueState) QueueUserMessageBeforeSessionConfigured(message UserMessage, record UserMessageHistoryRecord) {
	if s == nil {
		return
	}
	s.QueuedUserMessages = append([]QueuedUserMessage{NewQueuedUserMessage(message, QueuedInputPlain)}, s.QueuedUserMessages...)
	s.QueuedUserMessageHistoryRecords = append([]UserMessageHistoryRecord{recordOrText(record)}, s.QueuedUserMessageHistoryRecords...)
}

func SubmitQueuedShellPrompt(message UserMessage) SubmissionDecision {
	return DecideUserMessageSubmission(message, UserMessageTextHistoryRecord(), SubmissionOptions{
		SessionConfigured:     true,
		CurrentModelHasImages: true,
		ShellEscapePolicy:     ShellEscapeAllow,
	})
}

func recordOrText(record UserMessageHistoryRecord) UserMessageHistoryRecord {
	if record.Kind == "" {
		return UserMessageTextHistoryRecord()
	}
	return record
}

const ThreadModelUnavailableMessage = "Thread model is unavailable. Wait for the thread to finish syncing or choose a model before sending input."

func appendSubmissionMentionItems(items []SubmittedInputItem, text string, bindings []string, catalog SubmissionMentionCatalog) []SubmittedInputItem {
	mentions := CollectToolMentions(text, nil)
	boundNames := map[string]bool{}
	selectedSkillPaths := map[string]bool{}
	selectedPluginIDs := map[string]bool{}
	selectedAppIDs := map[string]bool{}

	for _, binding := range bindings {
		name, path := parseSubmissionMentionBinding(binding)
		if name != "" {
			boundNames[name] = true
		}
		if path == "" || !IsSkillMentionPath(path) {
			continue
		}
		normalizedPath := NormalizeSkillMentionPath(path)
		for _, skill := range catalog.Skills {
			if NormalizeSkillMentionPath(skill.Path) != normalizedPath || selectedSkillPaths[normalizedPath] {
				continue
			}
			selectedSkillPaths[normalizedPath] = true
			items = append(items, SubmittedInputItem{
				Kind: SubmittedInputSkill,
				Name: strings.TrimSpace(skill.Name),
				Path: normalizedPath,
			})
			break
		}
	}

	for _, skill := range FindSkillMentions(mentions, catalog.Skills) {
		path := NormalizeSkillMentionPath(skill.Path)
		if path == "" || selectedSkillPaths[path] || boundNames[strings.TrimSpace(skill.Name)] {
			continue
		}
		selectedSkillPaths[path] = true
		items = append(items, SubmittedInputItem{
			Kind: SubmittedInputSkill,
			Name: strings.TrimSpace(skill.Name),
			Path: path,
		})
	}

	for _, binding := range bindings {
		name, path := parseSubmissionMentionBinding(binding)
		pluginID := strings.TrimSpace(strings.TrimPrefix(path, "plugin://"))
		if pluginID == "" || pluginID == path || selectedPluginIDs[pluginID] {
			continue
		}
		if name != "" {
			boundNames[name] = true
		}
		if plugin, ok := findSubmissionPlugin(catalog.Plugins, pluginID); ok {
			selectedPluginIDs[pluginID] = true
			items = append(items, SubmittedInputItem{
				Kind: SubmittedInputPlugin,
				Name: firstNonEmptyRequestID(PluginDisplayName(plugin), plugin.Name, plugin.ID, pluginID),
				Path: "plugin://" + pluginID,
			})
		}
	}

	for _, binding := range bindings {
		name, path := parseSubmissionMentionBinding(binding)
		appID := AppIDFromMentionPath(path)
		if appID == "" || selectedAppIDs[appID] {
			continue
		}
		if name != "" {
			boundNames[name] = true
		}
		if app, ok := findSubmissionApp(catalog.Apps, appID); ok {
			selectedAppIDs[appID] = true
			items = append(items, SubmittedInputItem{
				Kind: SubmittedInputApp,
				Name: strings.TrimSpace(app.Name),
				Path: "app://" + appID,
			})
		}
	}

	skillNamesLower := SkillNamesLower(catalog.Skills)
	for _, app := range FindAppMentions(mentions, catalog.Apps, skillNamesLower) {
		appID := strings.TrimSpace(app.ID)
		slug := appMentionSlug(app)
		if appID == "" || selectedAppIDs[appID] || boundNames[slug] {
			continue
		}
		selectedAppIDs[appID] = true
		items = append(items, SubmittedInputItem{
			Kind: SubmittedInputApp,
			Name: strings.TrimSpace(app.Name),
			Path: "app://" + appID,
		})
	}
	return items
}

func PendingSteerCompareKeyFromSubmittedItems(items []SubmittedInputItem) PendingSteerCompareKey {
	key := PendingSteerCompareKey{}
	for _, item := range items {
		switch item.Kind {
		case SubmittedInputLocalImage, SubmittedInputRemoteImage:
			key.ImageCount++
		case SubmittedInputSkill, SubmittedInputPlugin, SubmittedInputApp, SubmittedInputMention:
			continue
		default:
			key.Message += item.Text
		}
	}
	return key
}

func parseSubmissionMentionBinding(binding string) (string, string) {
	binding = strings.TrimSpace(binding)
	if binding == "" {
		return "", ""
	}
	for _, sep := range []string{"|", "\t", "="} {
		if left, right, ok := strings.Cut(binding, sep); ok {
			return normalizeSubmissionMentionName(left), strings.TrimSpace(right)
		}
	}
	if strings.Contains(binding, "://") {
		return mentionNameFromPath(binding), strings.TrimSpace(binding)
	}
	return normalizeSubmissionMentionName(binding), ""
}

func normalizeSubmissionMentionName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "$")
	name = strings.TrimPrefix(name, "@")
	return strings.TrimSpace(name)
}

func mentionNameFromPath(path string) string {
	path = strings.TrimSpace(path)
	switch {
	case strings.HasPrefix(path, "plugin://"):
		return strings.TrimSpace(strings.TrimPrefix(path, "plugin://"))
	case strings.HasPrefix(path, "app://"):
		return strings.TrimSpace(strings.TrimPrefix(path, "app://"))
	case strings.HasPrefix(path, "skill://"):
		path = strings.TrimPrefix(path, "skill://")
	}
	path = strings.TrimRight(path, `/\`)
	if idx := strings.LastIndexAny(path, `/\`); idx >= 0 {
		path = path[idx+1:]
	}
	path = strings.TrimSuffix(path, ".md")
	return strings.TrimSpace(strings.TrimSuffix(path, ".MD"))
}

func findSubmissionPlugin(plugins []pluginapi.PluginSummary, pluginID string) (pluginapi.PluginSummary, bool) {
	for _, plugin := range plugins {
		if strings.TrimSpace(plugin.ID) == pluginID || strings.TrimSpace(plugin.Name) == pluginID {
			return plugin, true
		}
	}
	return pluginapi.PluginSummary{}, false
}

func findSubmissionApp(appList []apps.AppEntry, appID string) (apps.AppEntry, bool) {
	for _, app := range appList {
		if strings.TrimSpace(app.ID) == appID && IsAppMentionable(app) {
			return app, true
		}
	}
	return apps.AppEntry{}, false
}

func cloneUserMessage(message UserMessage) UserMessage {
	return UserMessage{
		Text:            message.Text,
		LocalImages:     append([]string(nil), message.LocalImages...),
		RemoteImageURLs: append([]string(nil), message.RemoteImageURLs...),
		LocalAudio:      append([]string(nil), message.LocalAudio...),
		RemoteAudioURLs: append([]string(nil), message.RemoteAudioURLs...),
		TextElements:    cloneTextElements(message.TextElements),
		MentionBindings: append([]string(nil), message.MentionBindings...),
	}
}
