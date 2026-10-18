package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// PluginExtensions carries hosted-plugin declarations from installed-plugin
// discovery (Rust #47263). These declarations are presentation metadata, not
// behavior-determining plugin state.
type PluginExtensions struct {
	Entrypoints            *[]PluginEntrypoint    `json:"entrypoints"`
	SettingsEntrypoints    []PluginEntrypoint     `json:"settingsEntrypoints"`
	Settings               []PluginSettings       `json:"settings"`
	ThreadEntrypoints      []PluginEntrypoint     `json:"threadEntrypoints"`
	FileHandlers           []PluginEntrypoint     `json:"fileHandlers"`
	SearchMentionProviders []PluginSearchProvider `json:"searchMentionProviders"`
}

// PluginEntrypoint is the flattened presentation shared by every hosted plugin
// entrypoint. Type selects the variant-specific declarations that are emitted.
type PluginEntrypoint struct {
	Type        string             `json:"type"`
	AppID       string             `json:"appId"`
	ToolName    string             `json:"toolName"`
	Title       string             `json:"title"`
	ResourceURI string             `json:"resourceUri"`
	Icons       []PluginIcon       `json:"icons"`
	QuickAction *PluginQuickAction `json:"quickAction,omitempty"`
	SearchTerms []string           `json:"searchTerms,omitempty"`
	Extensions  []string           `json:"extensions,omitempty"`
}

// PluginIcon is an icon declaration attached to a hosted plugin entrypoint.
type PluginIcon struct {
	Src      string    `json:"src"`
	MimeType *string   `json:"mimeType"`
	Sizes    *[]string `json:"sizes"`
	Theme    *string   `json:"theme"`
}

// PluginQuickAction is the optional global-entrypoint action declaration.
type PluginQuickAction struct {
	Title  string                  `json:"title"`
	Icons  []PluginIcon            `json:"icons"`
	Target PluginQuickActionTarget `json:"target"`
}

// PluginQuickActionTarget is currently limited to a tool invocation.
type PluginQuickActionTarget struct {
	Type      string          `json:"type"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// PluginSettings identifies the tools used to read and update plugin settings.
type PluginSettings struct {
	AppID          string `json:"appId"`
	ReadToolName   string `json:"readToolName"`
	UpdateToolName string `json:"updateToolName"`
}

// PluginSearchProvider describes a hosted plugin search mention provider.
type PluginSearchProvider struct {
	AppID    string                    `json:"appId"`
	ToolName string                    `json:"toolName"`
	LinkID   string                    `json:"linkId"`
	Title    string                    `json:"title"`
	Call     *PluginSearchProviderCall `json:"call"`
}

// PluginSearchProviderCall carries the invocation arguments and metadata for a
// search provider. Both values are arbitrary JSON values in the protocol.
type PluginSearchProviderCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Meta      json.RawMessage `json:"_meta"`
}

// ParsePluginExtensions decodes an installed-plugin extensions payload.
// Unsupported declarations are discarded as a whole so a future backend
// extension cannot invalidate the installed-plugin inventory (Rust #47263).
func ParsePluginExtensions(raw json.RawMessage) *PluginExtensions {
	if len(raw) == 0 || isJSONNull(raw) {
		return nil
	}
	var extensions PluginExtensions
	if err := json.Unmarshal(raw, &extensions); err != nil {
		return nil
	}
	return &extensions
}

func clonePluginExtensions(extensions *PluginExtensions) *PluginExtensions {
	if extensions == nil {
		return nil
	}
	data, err := json.Marshal(extensions)
	if err != nil {
		return nil
	}
	return ParsePluginExtensions(data)
}

func (e PluginExtensions) MarshalJSON() ([]byte, error) {
	entrypoints := e.Entrypoints
	if entrypoints != nil {
		values := append([]PluginEntrypoint{}, (*entrypoints)...)
		entrypoints = &values
	}
	return json.Marshal(struct {
		Entrypoints            *[]PluginEntrypoint    `json:"entrypoints"`
		SettingsEntrypoints    []PluginEntrypoint     `json:"settingsEntrypoints"`
		Settings               []PluginSettings       `json:"settings"`
		ThreadEntrypoints      []PluginEntrypoint     `json:"threadEntrypoints"`
		FileHandlers           []PluginEntrypoint     `json:"fileHandlers"`
		SearchMentionProviders []PluginSearchProvider `json:"searchMentionProviders"`
	}{
		Entrypoints:            entrypoints,
		SettingsEntrypoints:    nonNilPluginEntrypoints(e.SettingsEntrypoints),
		Settings:               nonNilPluginSettings(e.Settings),
		ThreadEntrypoints:      nonNilPluginEntrypoints(e.ThreadEntrypoints),
		FileHandlers:           nonNilPluginEntrypoints(e.FileHandlers),
		SearchMentionProviders: nonNilPluginSearchProviders(e.SearchMentionProviders),
	})
}

func (e *PluginExtensions) UnmarshalJSON(data []byte) error {
	if e == nil {
		return errors.New("plugin extensions receiver is nil")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw == nil {
		return errors.New("plugin extensions must be an object")
	}
	e.Entrypoints = nil
	if value, ok := raw["entrypoints"]; ok && !isJSONNull(value) {
		entrypoints, err := decodePluginEntrypoints(value)
		if err != nil {
			return err
		}
		e.Entrypoints = &entrypoints
	}
	var err error
	if e.SettingsEntrypoints, err = decodePluginEntrypointField(raw, "settingsEntrypoints", "settings_entrypoints"); err != nil {
		return err
	}
	if e.Settings, err = decodePluginSettingsField(raw, "settings"); err != nil {
		return err
	}
	if e.ThreadEntrypoints, err = decodePluginEntrypointField(raw, "threadEntrypoints", "thread_entrypoints"); err != nil {
		return err
	}
	if e.FileHandlers, err = decodePluginEntrypointField(raw, "fileHandlers", "file_handlers"); err != nil {
		return err
	}
	if e.SearchMentionProviders, err = decodePluginSearchProviderField(raw, "searchMentionProviders", "search_mention_providers"); err != nil {
		return err
	}
	return nil
}

func (e PluginEntrypoint) MarshalJSON() ([]byte, error) {
	payload := map[string]any{
		"type":        e.Type,
		"appId":       e.AppID,
		"toolName":    e.ToolName,
		"title":       e.Title,
		"resourceUri": e.ResourceURI,
		"icons":       nonNilPluginIcons(e.Icons),
	}
	switch strings.ToLower(strings.TrimSpace(e.Type)) {
	case "global":
		payload["type"] = "global"
		payload["quickAction"] = e.QuickAction
	case "settings":
		payload["type"] = "settings"
		payload["searchTerms"] = nonNilStrings(e.SearchTerms)
	case "thread":
		payload["type"] = "thread"
	case "file":
		payload["type"] = "file"
		payload["extensions"] = nonNilStrings(e.Extensions)
	default:
		return nil, fmt.Errorf("unsupported plugin entrypoint type %q", e.Type)
	}
	return json.Marshal(payload)
}

func (e *PluginEntrypoint) UnmarshalJSON(data []byte) error {
	if e == nil {
		return errors.New("plugin entrypoint receiver is nil")
	}
	raw, err := decodeJSONObject(data, "plugin entrypoint")
	if err != nil {
		return err
	}
	entrypointType, err := requiredString(raw, "type")
	if err != nil {
		return err
	}
	presentation, err := decodePluginEntrypointPresentation(raw)
	if err != nil {
		return err
	}
	*e = PluginEntrypoint{
		Type:        entrypointType,
		AppID:       presentation.AppID,
		ToolName:    presentation.ToolName,
		Title:       presentation.Title,
		ResourceURI: presentation.ResourceURI,
		Icons:       presentation.Icons,
	}
	switch strings.ToLower(strings.TrimSpace(entrypointType)) {
	case "global":
		value, ok := firstRawKey(raw, "quickAction", "quick_action")
		if !ok || isJSONNull(value) {
			return nil
		}
		var action PluginQuickAction
		if err := json.Unmarshal(value, &action); err != nil {
			return err
		}
		e.Type = "global"
		e.QuickAction = &action
	case "settings":
		value, ok := firstRawKey(raw, "searchTerms", "search_terms")
		if ok {
			terms, err := decodeStringList(value, "searchTerms")
			if err != nil {
				return err
			}
			e.SearchTerms = terms
		} else {
			e.SearchTerms = []string{}
		}
		e.Type = "settings"
	case "thread":
		e.Type = "thread"
	case "file":
		value, ok := raw["extensions"]
		if !ok {
			return errors.New("plugin file entrypoint is missing extensions")
		}
		extensions, err := decodeStringList(value, "extensions")
		if err != nil {
			return err
		}
		e.Extensions = extensions
		e.Type = "file"
	default:
		return fmt.Errorf("unsupported plugin entrypoint type %q", entrypointType)
	}
	return nil
}

func (a PluginQuickAction) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Title  string                  `json:"title"`
		Icons  []PluginIcon            `json:"icons"`
		Target PluginQuickActionTarget `json:"target"`
	}{
		Title:  a.Title,
		Icons:  nonNilPluginIcons(a.Icons),
		Target: a.Target,
	})
}

func (a *PluginQuickAction) UnmarshalJSON(data []byte) error {
	raw, err := decodeJSONObject(data, "plugin quick action")
	if err != nil {
		return err
	}
	title, err := requiredString(raw, "title")
	if err != nil {
		return err
	}
	iconsRaw, ok := raw["icons"]
	if !ok {
		return errors.New("plugin quick action is missing icons")
	}
	icons, err := decodePluginIcons(iconsRaw)
	if err != nil {
		return err
	}
	targetRaw, ok := raw["target"]
	if !ok {
		return errors.New("plugin quick action is missing target")
	}
	var target PluginQuickActionTarget
	if err := json.Unmarshal(targetRaw, &target); err != nil {
		return err
	}
	*a = PluginQuickAction{Title: title, Icons: icons, Target: target}
	return nil
}

func (t PluginQuickActionTarget) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type      string          `json:"type"`
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}{
		Type:      t.Type,
		Name:      t.Name,
		Arguments: t.Arguments,
	})
}

func (t *PluginQuickActionTarget) UnmarshalJSON(data []byte) error {
	raw, err := decodeJSONObject(data, "plugin quick action target")
	if err != nil {
		return err
	}
	targetType, err := requiredString(raw, "type")
	if err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(targetType), "tool") {
		return fmt.Errorf("unsupported plugin quick action target type %q", targetType)
	}
	name, err := requiredString(raw, "name")
	if err != nil {
		return err
	}
	arguments := raw["arguments"]
	if isJSONNull(arguments) {
		arguments = nil
	}
	*t = PluginQuickActionTarget{Type: "tool", Name: name, Arguments: arguments}
	return nil
}

func (s PluginSettings) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		AppID          string `json:"appId"`
		ReadToolName   string `json:"readToolName"`
		UpdateToolName string `json:"updateToolName"`
	}{AppID: s.AppID, ReadToolName: s.ReadToolName, UpdateToolName: s.UpdateToolName})
}

func (s *PluginSettings) UnmarshalJSON(data []byte) error {
	raw, err := decodeJSONObject(data, "plugin settings")
	if err != nil {
		return err
	}
	appID, err := requiredAliasedString(raw, "appId", "app_id")
	if err != nil {
		return err
	}
	readTool, err := requiredAliasedString(raw, "readToolName", "read_tool_name")
	if err != nil {
		return err
	}
	updateTool, err := requiredAliasedString(raw, "updateToolName", "update_tool_name")
	if err != nil {
		return err
	}
	*s = PluginSettings{AppID: appID, ReadToolName: readTool, UpdateToolName: updateTool}
	return nil
}

func (p PluginSearchProvider) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		AppID    string                    `json:"appId"`
		ToolName string                    `json:"toolName"`
		LinkID   string                    `json:"linkId"`
		Title    string                    `json:"title"`
		Call     *PluginSearchProviderCall `json:"call"`
	}{AppID: p.AppID, ToolName: p.ToolName, LinkID: p.LinkID, Title: p.Title, Call: p.Call})
}

func (p *PluginSearchProvider) UnmarshalJSON(data []byte) error {
	raw, err := decodeJSONObject(data, "plugin search provider")
	if err != nil {
		return err
	}
	appID, err := requiredAliasedString(raw, "appId", "app_id")
	if err != nil {
		return err
	}
	toolName, err := requiredAliasedString(raw, "toolName", "tool_name")
	if err != nil {
		return err
	}
	linkID, err := requiredAliasedString(raw, "linkId", "link_id")
	if err != nil {
		return err
	}
	title, err := requiredString(raw, "title")
	if err != nil {
		return err
	}
	var call *PluginSearchProviderCall
	if value, ok := raw["call"]; ok && !isJSONNull(value) {
		var decoded PluginSearchProviderCall
		if err := json.Unmarshal(value, &decoded); err != nil {
			return err
		}
		call = &decoded
	}
	*p = PluginSearchProvider{AppID: appID, ToolName: toolName, LinkID: linkID, Title: title, Call: call}
	return nil
}

func (c PluginSearchProviderCall) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
		Meta      json.RawMessage `json:"_meta"`
	}{Name: c.Name, Arguments: c.Arguments, Meta: c.Meta})
}

func (c *PluginSearchProviderCall) UnmarshalJSON(data []byte) error {
	raw, err := decodeJSONObject(data, "plugin search provider call")
	if err != nil {
		return err
	}
	name, err := requiredString(raw, "name")
	if err != nil {
		return err
	}
	arguments, ok := raw["arguments"]
	if !ok {
		return errors.New("plugin search provider call is missing arguments")
	}
	meta, ok := raw["_meta"]
	if !ok {
		return errors.New("plugin search provider call is missing _meta")
	}
	*c = PluginSearchProviderCall{Name: name, Arguments: arguments, Meta: meta}
	return nil
}

func decodePluginEntrypointPresentation(raw map[string]json.RawMessage) (PluginEntrypoint, error) {
	appID, err := requiredAliasedString(raw, "appId", "app_id")
	if err != nil {
		return PluginEntrypoint{}, err
	}
	toolName, err := requiredAliasedString(raw, "toolName", "tool_name")
	if err != nil {
		return PluginEntrypoint{}, err
	}
	title, err := requiredString(raw, "title")
	if err != nil {
		return PluginEntrypoint{}, err
	}
	resourceURI, err := requiredAliasedString(raw, "resourceUri", "resource_uri")
	if err != nil {
		return PluginEntrypoint{}, err
	}
	iconsRaw, ok := raw["icons"]
	if !ok {
		return PluginEntrypoint{}, errors.New("plugin entrypoint is missing icons")
	}
	icons, err := decodePluginIcons(iconsRaw)
	if err != nil {
		return PluginEntrypoint{}, err
	}
	return PluginEntrypoint{AppID: appID, ToolName: toolName, Title: title, ResourceURI: resourceURI, Icons: icons}, nil
}

func decodePluginEntrypointField(raw map[string]json.RawMessage, keys ...string) ([]PluginEntrypoint, error) {
	value, ok := firstRawKey(raw, keys...)
	if !ok {
		return []PluginEntrypoint{}, nil
	}
	if isJSONNull(value) {
		return nil, fmt.Errorf("plugin entrypoint field %s must be an array", keys[0])
	}
	return decodePluginEntrypoints(value)
}

func decodePluginEntrypoints(data json.RawMessage) ([]PluginEntrypoint, error) {
	var values []PluginEntrypoint
	if err := json.Unmarshal(data, &values); err != nil {
		return nil, err
	}
	if values == nil {
		return []PluginEntrypoint{}, nil
	}
	return values, nil
}

func decodePluginIcons(data json.RawMessage) ([]PluginIcon, error) {
	if isJSONNull(data) {
		return nil, errors.New("plugin icons must be an array")
	}
	var icons []PluginIcon
	if err := json.Unmarshal(data, &icons); err != nil {
		return nil, err
	}
	if icons == nil {
		icons = []PluginIcon{}
	}
	return icons, nil
}

func decodePluginSettingsField(raw map[string]json.RawMessage, key string) ([]PluginSettings, error) {
	value, ok := raw[key]
	if !ok {
		return []PluginSettings{}, nil
	}
	if isJSONNull(value) {
		return nil, errors.New("plugin settings must be an array")
	}
	var settings []PluginSettings
	if err := json.Unmarshal(value, &settings); err != nil {
		return nil, err
	}
	if settings == nil {
		settings = []PluginSettings{}
	}
	return settings, nil
}

func decodePluginSearchProviderField(raw map[string]json.RawMessage, keys ...string) ([]PluginSearchProvider, error) {
	value, ok := firstRawKey(raw, keys...)
	if !ok {
		return []PluginSearchProvider{}, nil
	}
	if isJSONNull(value) {
		return nil, fmt.Errorf("plugin search provider field %s must be an array", keys[0])
	}
	var providers []PluginSearchProvider
	if err := json.Unmarshal(value, &providers); err != nil {
		return nil, err
	}
	if providers == nil {
		providers = []PluginSearchProvider{}
	}
	return providers, nil
}

func decodeStringList(data json.RawMessage, name string) ([]string, error) {
	if isJSONNull(data) {
		return nil, fmt.Errorf("plugin %s must be an array", name)
	}
	var values []string
	if err := json.Unmarshal(data, &values); err != nil {
		return nil, err
	}
	if values == nil {
		values = []string{}
	}
	return values, nil
}

func decodeJSONObject(data json.RawMessage, name string) (map[string]json.RawMessage, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, fmt.Errorf("%s must be an object", name)
	}
	return raw, nil
}

func firstRawKey(raw map[string]json.RawMessage, keys ...string) (json.RawMessage, bool) {
	for _, key := range keys {
		if value, ok := raw[key]; ok {
			return value, true
		}
	}
	return nil, false
}

func requiredString(raw map[string]json.RawMessage, key string) (string, error) {
	value, ok := raw[key]
	if !ok {
		return "", fmt.Errorf("plugin extension is missing %s", key)
	}
	if isJSONNull(value) {
		return "", fmt.Errorf("plugin extension %s must be a string", key)
	}
	var text string
	if err := json.Unmarshal(value, &text); err != nil {
		return "", fmt.Errorf("plugin extension %s must be a string: %w", key, err)
	}
	return text, nil
}

func requiredAliasedString(raw map[string]json.RawMessage, keys ...string) (string, error) {
	value, ok := firstRawKey(raw, keys...)
	if !ok {
		return "", fmt.Errorf("plugin extension is missing %s", keys[0])
	}
	if isJSONNull(value) {
		return "", fmt.Errorf("plugin extension %s must be a string", keys[0])
	}
	var text string
	if err := json.Unmarshal(value, &text); err != nil {
		return "", fmt.Errorf("plugin extension %s must be a string: %w", keys[0], err)
	}
	return text, nil
}

func isJSONNull(raw json.RawMessage) bool {
	return strings.EqualFold(strings.TrimSpace(string(raw)), "null")
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func nonNilPluginIcons(values []PluginIcon) []PluginIcon {
	if values == nil {
		return []PluginIcon{}
	}
	return values
}

func nonNilPluginEntrypoints(values []PluginEntrypoint) []PluginEntrypoint {
	if values == nil {
		return []PluginEntrypoint{}
	}
	return values
}

func nonNilPluginSettings(values []PluginSettings) []PluginSettings {
	if values == nil {
		return []PluginSettings{}
	}
	return values
}

func nonNilPluginSearchProviders(values []PluginSearchProvider) []PluginSearchProvider {
	if values == nil {
		return []PluginSearchProvider{}
	}
	return values
}
