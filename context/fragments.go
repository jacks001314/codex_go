package context

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	RoleDeveloper = "developer"
	RoleUser      = "user"
)

type Fragment interface {
	Role() string
	Markers() (string, string)
	Body() string
}

type RenderedFragment struct {
	Role    string
	Content string
}

type SimpleFragment struct {
	role     string
	openTag  string
	closeTag string
	body     string
}

func NewSimpleFragment(role string, openTag string, closeTag string, body string) *SimpleFragment {
	return &SimpleFragment{role: role, openTag: openTag, closeTag: closeTag, body: body}
}

func (f *SimpleFragment) Role() string {
	return f.role
}

func (f *SimpleFragment) Markers() (string, string) {
	return f.openTag, f.closeTag
}

func (f *SimpleFragment) Body() string {
	return f.body
}

func Render(fragment Fragment) *RenderedFragment {
	if fragment == nil {
		return nil
	}
	open, close := fragment.Markers()
	body := strings.Trim(fragment.Body(), "\n")
	content := body
	if open != "" || close != "" {
		content = strings.Join(nonEmpty([]string{open, body, close}), "\n")
	}
	return &RenderedFragment{
		Role:    fragment.Role(),
		Content: content,
	}
}

func RenderStandalone(fragment Fragment) *RenderedFragment {
	if fragment == nil {
		return nil
	}
	open, close := fragment.Markers()
	return &RenderedFragment{
		Role:    fragment.Role(),
		Content: open + fragment.Body() + close,
	}
}

func RenderMany(fragments []Fragment) []RenderedFragment {
	out := make([]RenderedFragment, 0, len(fragments))
	for _, fragment := range fragments {
		rendered := Render(fragment)
		if rendered != nil && strings.TrimSpace(rendered.Content) != "" {
			out = append(out, *rendered)
		}
	}
	return out
}

type PluginSummary struct {
	ConfigName  string
	DisplayName string
	HasSkills   bool
}

type AvailablePluginsInstructions struct {
	Plugins []PluginSummary
}

func NewAvailablePluginsInstructions(plugins []PluginSummary) *AvailablePluginsInstructions {
	if len(plugins) == 0 {
		return nil
	}
	return &AvailablePluginsInstructions{Plugins: append([]PluginSummary(nil), plugins...)}
}

func (p *AvailablePluginsInstructions) Role() string {
	return RoleDeveloper
}

func (p *AvailablePluginsInstructions) Markers() (string, string) {
	return "<plugins_instructions>", "</plugins_instructions>"
}

func (p *AvailablePluginsInstructions) Body() string {
	lines := []string{
		"## Plugins",
		"A plugin is a local bundle of skills, MCP servers, and apps.",
		"### How to use plugins",
		"- Skill naming: If a plugin contributes skills, those skill entries are prefixed with `plugin_name:` in the Skills list.",
		"- MCP naming: Plugin-provided MCP tools keep standard MCP identifiers such as `mcp__server__tool`; use tool provenance to tell which plugin they come from.",
		"- Trigger rules: If the user explicitly names a plugin, prefer capabilities associated with that plugin for that turn.",
		"- Relationship to capabilities: Plugins are not invoked directly. Use their underlying skills, MCP tools, and app tools to help solve the task.",
		"- Relevance: Determine what a plugin can help with from explicit user mention or from the plugin-associated skills, MCP tools, and apps exposed elsewhere in this turn.",
		"- Missing/blocked: If the user requests a plugin that does not have relevant callable capabilities for the task, say so briefly and continue with the best fallback.",
	}
	return strings.Join(lines, "\n")
}

type PluginInstructions struct {
	Text string
}

func NewPluginInstructions(text string) *PluginInstructions {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return &PluginInstructions{Text: text}
}

func (p *PluginInstructions) Role() string {
	return RoleDeveloper
}

func (p *PluginInstructions) Markers() (string, string) {
	return "", ""
}

func (p *PluginInstructions) Body() string {
	return p.Text
}

type SkillInstructions struct {
	Name                   string
	Path                   string
	Contents               string
	ExecutorResourceAccess *ExecutorSkillResourceAccess
}

type ExecutorSkillResourceAccess struct {
	AuthorityID  string
	Package      string
	MainResource string
}

func NewSkillInstructions(name string, path string, contents string) *SkillInstructions {
	name = strings.TrimSpace(name)
	path = strings.TrimSpace(path)
	contents = strings.TrimSpace(contents)
	if name == "" || path == "" || contents == "" {
		return nil
	}
	return &SkillInstructions{Name: name, Path: path, Contents: contents}
}

func NewSkillInstructionsWithExecutorResourceAccess(name string, path string, contents string, access *ExecutorSkillResourceAccess) *SkillInstructions {
	fragment := NewSkillInstructions(name, path, contents)
	if fragment == nil || access == nil {
		return fragment
	}
	copy := *access
	copy.AuthorityID = strings.TrimSpace(copy.AuthorityID)
	copy.Package = strings.TrimSpace(copy.Package)
	copy.MainResource = strings.TrimSpace(copy.MainResource)
	if copy.AuthorityID != "" && copy.Package != "" && copy.MainResource != "" {
		fragment.ExecutorResourceAccess = &copy
	}
	return fragment
}

func (s *SkillInstructions) Role() string {
	return RoleUser
}

func (s *SkillInstructions) Markers() (string, string) {
	return "<skill>", "</skill>"
}

func (s *SkillInstructions) Body() string {
	resourceAccess := ""
	if s.ExecutorResourceAccess != nil {
		metadata := struct {
			Authority struct {
				Kind string `json:"kind"`
				ID   string `json:"id"`
			} `json:"authority"`
			Package      string `json:"package"`
			MainResource string `json:"main_resource"`
		}{Package: s.ExecutorResourceAccess.Package, MainResource: s.ExecutorResourceAccess.MainResource}
		metadata.Authority.Kind = "executor"
		metadata.Authority.ID = s.ExecutorResourceAccess.AuthorityID
		if encoded, err := json.Marshal(metadata); err == nil {
			resourceAccess = "\n<resource_access>" + string(encoded) + "</resource_access>"
		}
	}
	return fmt.Sprintf("\n<name>%s</name>\n<path>%s</path>%s\n%s\n", s.Name, s.Path, resourceAccess, s.Contents)
}

type AppInstructionsData struct {
	ID                 string
	Name               string
	Description        string
	InstallURL         string
	IsAccessible       bool
	IsEnabled          bool
	PluginDisplayNames []string
}

type AppInstructions struct {
	Data AppInstructionsData
}

func NewAppInstructions(data AppInstructionsData) *AppInstructions {
	data.ID = strings.TrimSpace(data.ID)
	if data.ID == "" {
		return nil
	}
	data.Name = strings.TrimSpace(data.Name)
	if data.Name == "" {
		data.Name = data.ID
	}
	data.Description = strings.TrimSpace(data.Description)
	data.InstallURL = strings.TrimSpace(data.InstallURL)
	data.PluginDisplayNames = cleanFragmentStrings(data.PluginDisplayNames)
	return &AppInstructions{Data: data}
}

func (a *AppInstructions) Role() string {
	return RoleUser
}

func (a *AppInstructions) Markers() (string, string) {
	return "<app>", "</app>"
}

func (a *AppInstructions) Body() string {
	if a == nil {
		return ""
	}
	lines := []string{
		fmt.Sprintf("<id>%s</id>", a.Data.ID),
		fmt.Sprintf("<name>%s</name>", a.Data.Name),
	}
	if a.Data.Description != "" {
		lines = append(lines, fmt.Sprintf("<description>%s</description>", a.Data.Description))
	}
	if a.Data.InstallURL != "" {
		lines = append(lines, fmt.Sprintf("<install_url>%s</install_url>", a.Data.InstallURL))
	}
	lines = append(lines,
		fmt.Sprintf("<is_accessible>%t</is_accessible>", a.Data.IsAccessible),
		fmt.Sprintf("<is_enabled>%t</is_enabled>", a.Data.IsEnabled),
	)
	if len(a.Data.PluginDisplayNames) > 0 {
		lines = append(lines, fmt.Sprintf("<plugin_display_names>%s</plugin_display_names>", strings.Join(a.Data.PluginDisplayNames, ", ")))
	}
	return "\n" + strings.Join(lines, "\n") + "\n"
}

// ImageResizeNoticeSource mirrors Rust's ImageResizeNoticeSource (fa5d5ae047,
// #37134).
type ImageResizeNoticeSource string

const (
	ImageResizeNoticeSourceUserMessage ImageResizeNoticeSource = "user message"
	ImageResizeNoticeSourceToolOutput  ImageResizeNoticeSource = "tool output"
)

// ResizedImage mirrors Rust's ResizedImage: image numbering plus original and
// prepared dimensions.
type ResizedImage struct {
	ImageNumber    int
	ImageCount     int
	SourceWidth    int
	SourceHeight   int
	PreparedWidth  int
	PreparedHeight int
}

// ImageResizeNotice reports to the model when prompt images were resized,
// mirroring Rust's image_resize_notice feature fragment.
type ImageResizeNotice struct {
	Source        ImageResizeNoticeSource
	ResizedImages []ResizedImage
}

func NewImageResizeNotice(source ImageResizeNoticeSource, resized []ResizedImage) *ImageResizeNotice {
	return &ImageResizeNotice{Source: source, ResizedImages: append([]ResizedImage(nil), resized...)}
}

func (n *ImageResizeNotice) Role() string {
	return RoleDeveloper
}

func (n *ImageResizeNotice) Markers() (string, string) {
	return "<image_resize_notice>", "</image_resize_notice>"
}

func (n *ImageResizeNotice) Body() string {
	if n == nil {
		return ""
	}
	source := string(n.Source)
	if source == "" {
		source = string(ImageResizeNoticeSourceUserMessage)
	}
	lines := make([]string, 0, len(n.ResizedImages))
	for _, image := range n.ResizedImages {
		lines = append(lines, fmt.Sprintf(
			"Image %d of %d in the preceding %s was resized from %dx%d to %dx%d pixels.",
			image.ImageNumber, image.ImageCount, source,
			image.SourceWidth, image.SourceHeight, image.PreparedWidth, image.PreparedHeight,
		))
	}
	return "\n" + strings.Join(lines, "\n") + "\n"
}

const maxRecommendedPlugins = 50

type RecommendedPlugin struct {
	ID   string
	Name string
}

type RecommendedPluginsInstructions struct {
	Plugins []RecommendedPlugin
}

func NewRecommendedPluginsInstructions(plugins []RecommendedPlugin) *RecommendedPluginsInstructions {
	if len(plugins) == 0 {
		return nil
	}
	out := append([]RecommendedPlugin(nil), plugins...)
	if len(out) > maxRecommendedPlugins {
		out = out[:maxRecommendedPlugins]
	}
	return &RecommendedPluginsInstructions{Plugins: out}
}

func (p *RecommendedPluginsInstructions) Role() string {
	return RoleUser
}

func (p *RecommendedPluginsInstructions) Markers() (string, string) {
	return "<recommended_plugins>", "</recommended_plugins>"
}

func (p *RecommendedPluginsInstructions) Body() string {
	lines := []string{
		"Here is a list of plugins that are available but not installed. If the user's query would benefit from one of these plugins, use the `request_plugin_install` tool to suggest that they install it. Pass the parenthesized ID as `plugin_id`. For example, suggest the Google Drive plugin if the query could possibly be better answered with access to Google Drive.",
		"",
	}
	for _, plugin := range p.Plugins {
		if strings.TrimSpace(plugin.ID) == "" || strings.TrimSpace(plugin.Name) == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s (%s)", plugin.Name, plugin.ID))
	}
	return strings.Join(lines, "\n")
}

type PermissionsInstructions struct {
	SandboxMode     string
	ApprovalPolicy  string
	WritableRoots   []string
	NetworkEnabled  bool
	ExtraGuidelines []string
}

func (p *PermissionsInstructions) Role() string {
	return RoleDeveloper
}

func (p *PermissionsInstructions) Markers() (string, string) {
	return "<permissions>", "</permissions>"
}

func (p *PermissionsInstructions) Body() string {
	lines := []string{
		fmt.Sprintf("Filesystem sandbox: %s", valueOr(p.SandboxMode, "unknown")),
		fmt.Sprintf("Approval policy: %s", valueOr(p.ApprovalPolicy, "unknown")),
	}
	if len(p.WritableRoots) == 0 {
		lines = append(lines, "Writable roots: none")
	} else {
		roots := append([]string(nil), p.WritableRoots...)
		sort.Strings(roots)
		lines = append(lines, "Writable roots:")
		for _, root := range roots {
			lines = append(lines, "- "+root)
		}
	}
	if p.NetworkEnabled {
		lines = append(lines, "Network: enabled")
	} else {
		lines = append(lines, "Network: restricted")
	}
	lines = append(lines, p.ExtraGuidelines...)
	return strings.Join(lines, "\n")
}

func cleanFragmentStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

type CurrentTimeReminder struct {
	Now      time.Time
	Location string
}

func (c *CurrentTimeReminder) Role() string {
	return RoleDeveloper
}

func (c *CurrentTimeReminder) Markers() (string, string) {
	return "<current_time_reminder>", "</current_time_reminder>"
}

func (c *CurrentTimeReminder) Body() string {
	now := c.Now
	if now.IsZero() {
		now = time.Now()
	}
	return fmt.Sprintf("It is %s.", now.UTC().Format("2006-01-02 15:04:05 UTC"))
}

type AdditionalContextFragment struct {
	Key   string
	Value string
	role  string
}

func NewAdditionalContextFragment(role string, key string, value string) *AdditionalContextFragment {
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" || value == "" {
		return nil
	}
	if role == "" {
		role = RoleUser
	}
	return &AdditionalContextFragment{Key: key, Value: value, role: role}
}

func (a *AdditionalContextFragment) Role() string {
	if a == nil || a.role == "" {
		return RoleUser
	}
	return a.role
}

func (a *AdditionalContextFragment) Markers() (string, string) {
	return "<additional_context>", "</additional_context>"
}

func (a *AdditionalContextFragment) Body() string {
	if a == nil {
		return ""
	}
	return fmt.Sprintf("%s:\n%s", a.Key, a.Value)
}

type UserInstructions struct {
	Text string
}

func (u *UserInstructions) Role() string {
	return RoleDeveloper
}

func (u *UserInstructions) Markers() (string, string) {
	return "<user_instructions>", "</user_instructions>"
}

func (u *UserInstructions) Body() string {
	return u.Text
}

type ModelSwitchInstructions struct {
	From         string
	To           string
	Instructions string
}

func (m *ModelSwitchInstructions) Role() string {
	return RoleDeveloper
}

func (m *ModelSwitchInstructions) Markers() (string, string) {
	return "<model_switch>", "</model_switch>"
}

func (m *ModelSwitchInstructions) Body() string {
	if m.Instructions != "" {
		return fmt.Sprintf("\nThe user was previously using a different model. Please continue the conversation according to the following instructions:\n\n%s\n", m.Instructions)
	}
	if m.From == "" {
		return fmt.Sprintf("The active model is `%s`.", m.To)
	}
	return fmt.Sprintf("The active model changed from `%s` to `%s`.", m.From, m.To)
}

type PersonalitySpecInstructions struct {
	Spec string
}

func (p *PersonalitySpecInstructions) Role() string {
	return RoleDeveloper
}

func (p *PersonalitySpecInstructions) Markers() (string, string) {
	return "<personality_spec>", "</personality_spec>"
}

func (p *PersonalitySpecInstructions) Body() string {
	return fmt.Sprintf(" The user has requested a new communication style. Future messages should adhere to the following personality: \n%s ", p.Spec)
}

type TokenBudgetContext struct {
	Used      int
	Limit     int
	Remaining int
}

type ContextWindowGuidance struct {
	Message string
}

func (c *ContextWindowGuidance) Role() string {
	return RoleDeveloper
}

func (c *ContextWindowGuidance) Markers() (string, string) {
	return "<context_window_guidance>", "</context_window_guidance>"
}

func (c *ContextWindowGuidance) Body() string {
	if c == nil {
		return ""
	}
	return strings.TrimSpace(c.Message)
}

func (t *TokenBudgetContext) Role() string {
	return RoleDeveloper
}

func (t *TokenBudgetContext) Markers() (string, string) {
	return "<token_budget>", "</token_budget>"
}

func (t *TokenBudgetContext) Body() string {
	remaining := t.Remaining
	if remaining == 0 && t.Limit > 0 {
		remaining = t.Limit - t.Used
	}
	return fmt.Sprintf("Token budget: %d used, %d remaining, %d limit.", t.Used, remaining, t.Limit)
}

func nonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func valueOr(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
