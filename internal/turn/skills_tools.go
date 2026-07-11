package turn

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"codex_go/internal/mcp"
	"codex_go/internal/tool"
)

const (
	skillsToolNamespace                  = "skills"
	skillsToolListName                   = "list"
	skillsToolReadName                   = "read"
	orchestratorSkillMimeType            = "mcp/skill"
	maxSkillToolHandleBytes              = 2048
	maxSkillToolWarnings                 = 4
	maxSkillToolWarningBytes             = 256
	maxOrchestratorSkillNameChars        = 64
	maxOrchestratorQualifiedNameChars    = 128
	maxOrchestratorSkillPackageURIChars  = 1024
	maxOrchestratorSkillResourceURIChars = 2048
	maxOrchestratorSkillResourceBytes    = 1024 * 1024
	maxCatalogSkillDescriptionChars      = 1024
	skillDescriptionTruncatedSuffix      = "..."
)

type skillsToolAuthority struct {
	Kind string `json:"kind"`
}

type skillsListArgs struct {
	Authority skillsToolAuthority `json:"authority"`
}

type listedSkill struct {
	Authority    skillsToolAuthority `json:"authority"`
	Package      string              `json:"package"`
	Name         string              `json:"name"`
	Description  string              `json:"description"`
	MainResource string              `json:"main_resource"`
}

type skillsListResponse struct {
	Skills   []listedSkill `json:"skills"`
	Warnings []string      `json:"warnings"`
}

type skillsReadArgs struct {
	Authority skillsToolAuthority `json:"authority"`
	Package   string              `json:"package"`
	Resource  string              `json:"resource"`
}

type skillsReadResponse struct {
	Resource string `json:"resource"`
	Contents string `json:"contents"`
}

type skillsToolExecutor struct {
	spec       tool.Spec
	mcpService *mcp.MCPService
	threadID   string
	name       string
}

func registerSkillsTools(registry *tool.Registry, options *ToolRegistryOptions) error {
	if registry == nil || options == nil || options.MCPService == nil {
		return nil
	}
	list := newSkillsToolExecutor(options, skillsToolListName, "List enabled skills owned by the requested authority. Only orchestrator-owned skills are currently supported. Returns the opaque package and main-resource handles required by skills.read.")
	if err := registry.Register(list); err != nil {
		return err
	}
	read := newSkillsToolExecutor(options, skillsToolReadName, "Read one complete resource from an enabled skill. Pass the exact authority and package returned by skills.list; resource identifiers remain opaque and are routed to that authority.")
	return registry.Register(read)
}

func newSkillsToolExecutor(options *ToolRegistryOptions, name string, description string) *skillsToolExecutor {
	return &skillsToolExecutor{
		spec: tool.Spec{
			Name:                 tool.NamespacedName(skillsToolNamespace, name),
			Description:          description,
			InputSchema:          skillsToolInputSchema(name),
			Parallel:             true,
			NamespaceDescription: fmt.Sprintf("Tools in the %s namespace.", skillsToolNamespace),
		},
		mcpService: options.MCPService,
		threadID:   strings.TrimSpace(options.ThreadID),
		name:       name,
	}
}

func (e *skillsToolExecutor) Spec() tool.Spec {
	if e == nil {
		return tool.Spec{}
	}
	return e.spec
}

func (e *skillsToolExecutor) Execute(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
	if e == nil || invocation == nil {
		return nil, fmt.Errorf("%w: skills tool invocation is nil", tool.ErrToolInvalidCall)
	}
	if invocation.Payload.Kind != tool.PayloadFunction {
		return nil, tool.Fatal("skills." + e.name + " handler received unsupported payload")
	}
	switch e.name {
	case skillsToolListName:
		return e.executeList(invocation)
	case skillsToolReadName:
		return e.executeRead(invocation)
	default:
		return nil, tool.Fatal("unknown skills tool")
	}
}

func (e *skillsToolExecutor) executeList(invocation *tool.Invocation) (*tool.Output, error) {
	var args skillsListArgs
	if err := decodeStrictToolArgs(invocation, &args); err != nil {
		return nil, tool.RespondToModel(err.Error())
	}
	if err := validateSkillsToolAuthority(args.Authority); err != nil {
		return nil, err
	}
	catalog := e.orchestratorSkillCatalog()
	response := skillsListResponse{
		Skills:   catalog.skills,
		Warnings: boundedSkillToolWarnings(catalog.warnings),
	}
	return skillsToolJSONOutput(invocation, response)
}

func (e *skillsToolExecutor) executeRead(invocation *tool.Invocation) (*tool.Output, error) {
	var args skillsReadArgs
	if err := decodeStrictToolArgs(invocation, &args); err != nil {
		return nil, tool.RespondToModel(err.Error())
	}
	authority := args.Authority
	if err := validateSkillsToolAuthority(authority); err != nil {
		return nil, err
	}
	if err := validateSkillToolHandle("package", args.Package, maxSkillToolHandleBytes); err != nil {
		return nil, err
	}
	if err := validateSkillToolHandle("resource", args.Resource, maxSkillToolHandleBytes); err != nil {
		return nil, err
	}
	if !orchestratorResourceBelongsToPackage(args.Package, args.Resource) {
		return nil, tool.RespondToModel("skill package is not available from the requested authority")
	}
	if !e.orchestratorSkillPackageAvailable(args.Package) {
		return nil, tool.RespondToModel("skill package is not available from the requested authority")
	}
	response, err := e.mcpService.ReadResource(&mcp.MCPResourceReadParams{
		ThreadID: stringPtrIfNotEmpty(e.threadID),
		Server:   mcp.RuntimeCodexAppsMCPServerName,
		URI:      args.Resource,
	})
	if err != nil {
		return nil, tool.RespondToModel("failed to read skill resource")
	}
	contents, ok := matchingOrchestratorSkillText(response, args.Resource)
	if !ok {
		return nil, tool.RespondToModel("failed to read skill resource")
	}
	if len(contents) > maxOrchestratorSkillResourceBytes {
		return nil, tool.RespondToModel("failed to read skill resource")
	}
	return skillsToolJSONOutput(invocation, skillsReadResponse{
		Resource: args.Resource,
		Contents: contents,
	})
}

type orchestratorCatalog struct {
	skills   []listedSkill
	warnings []string
}

func (e *skillsToolExecutor) orchestratorSkillCatalog() orchestratorCatalog {
	if e == nil || e.mcpService == nil {
		return orchestratorCatalog{skills: []listedSkill{}, warnings: []string{}}
	}
	status := e.mcpService.ListStatus(&mcp.MCPListServerStatusParams{
		ThreadID: stringPtrIfNotEmpty(e.threadID),
		Detail:   &mcp.MCPServerStatusDetail{Mode: mcp.MCPServerStatusDetailFull},
	})
	out := orchestratorCatalog{skills: []listedSkill{}, warnings: []string{}}
	for _, server := range status.Data {
		if server.Name != mcp.RuntimeCodexAppsMCPServerName && server.Server.Name != mcp.RuntimeCodexAppsMCPServerName {
			continue
		}
		skipped := 0
		for _, resource := range server.Resources {
			if resource.MimeType != orchestratorSkillMimeType {
				continue
			}
			entry, ok := listedSkillFromOrchestratorResource(resource)
			if !ok {
				skipped++
				continue
			}
			out.skills = append(out.skills, entry)
		}
		if skipped > 0 {
			out.warnings = append(out.warnings, fmt.Sprintf("Skipped %d malformed orchestrator skill resources.", skipped))
		}
	}
	return out
}

func (e *skillsToolExecutor) orchestratorSkillPackageAvailable(pkg string) bool {
	catalog := e.orchestratorSkillCatalog()
	for _, skill := range catalog.skills {
		if skill.Package == pkg {
			return true
		}
	}
	return false
}

func listedSkillFromOrchestratorResource(resource mcp.MCPResource) (listedSkill, bool) {
	uri, ok := validatedOrchestratorSkillURI(resource.URI, maxOrchestratorSkillPackageURIChars)
	if !ok {
		return listedSkill{}, false
	}
	meta := skillResourceMetaMap(resource.Meta)
	skillName, ok := normalizedOrchestratorSkillLabel(metaString(meta, "skill_name"), maxOrchestratorSkillNameChars)
	if !ok {
		return listedSkill{}, false
	}
	name := skillName
	if metaString(meta, "source") != "user" {
		pluginName, ok := normalizedOrchestratorSkillLabel(metaString(meta, "plugin_name"), maxOrchestratorSkillNameChars)
		if !ok {
			return listedSkill{}, false
		}
		name = pluginName + ":" + skillName
		if utf8.RuneCountInString(name) > maxOrchestratorQualifiedNameChars {
			return listedSkill{}, false
		}
	}
	description, ok := normalizedOrchestratorSkillDescription(resource.Description)
	if !ok {
		return listedSkill{}, false
	}
	mainResource := strings.TrimRight(uri, "/") + "/SKILL.md"
	if !isBoundedSkillToolHandle(uri, maxSkillToolHandleBytes) || !isBoundedSkillToolHandle(mainResource, maxSkillToolHandleBytes) {
		return listedSkill{}, false
	}
	return listedSkill{
		Authority:    skillsToolAuthority{Kind: "orchestrator"},
		Package:      uri,
		Name:         name,
		Description:  truncateCatalogSkillDescription(description),
		MainResource: mainResource,
	}, true
}

func matchingOrchestratorSkillText(response *mcp.MCPResourceReadResponse, resource string) (string, bool) {
	if response == nil {
		return "", false
	}
	for _, content := range response.Contents {
		if content.URI == resource && content.Blob == "" {
			return content.Text, true
		}
	}
	return "", false
}

func validateSkillsToolAuthority(authority skillsToolAuthority) error {
	if authority.Kind != "orchestrator" {
		return tool.RespondToModel("unknown variant `" + authority.Kind + "`, expected `orchestrator`")
	}
	return nil
}

func validateSkillToolHandle(name string, value string, maxBytes int) error {
	if isBoundedSkillToolHandle(value, maxBytes) {
		return nil
	}
	return tool.RespondToModel(fmt.Sprintf("%s must be non-empty, contain no control characters, and be at most %d bytes", name, maxBytes))
}

func isBoundedSkillToolHandle(value string, maxBytes int) bool {
	if value == "" || len(value) > maxBytes {
		return false
	}
	for _, ch := range value {
		if unicode.IsControl(ch) {
			return false
		}
	}
	return true
}

func boundedSkillToolWarnings(warnings []string) []string {
	out := make([]string, 0, maxSkillToolWarnings)
	for _, warning := range warnings {
		if len(out) >= maxSkillToolWarnings {
			break
		}
		out = append(out, truncateUTF8Bytes(warning, maxSkillToolWarningBytes))
	}
	return out
}

func truncateCatalogSkillDescription(description string) string {
	runes := []rune(description)
	if len(runes) <= maxCatalogSkillDescriptionChars {
		return description
	}
	prefixChars := maxCatalogSkillDescriptionChars - len([]rune(skillDescriptionTruncatedSuffix))
	if prefixChars < 0 {
		prefixChars = 0
	}
	return string(runes[:prefixChars]) + skillDescriptionTruncatedSuffix
}

func truncateUTF8Bytes(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	for maxBytes > 0 && !utf8.RuneStart(value[maxBytes]) {
		maxBytes--
	}
	return value[:maxBytes]
}

func skillResourceMetaMap(value any) map[string]any {
	if value == nil {
		return nil
	}
	if meta, ok := value.(map[string]any); ok {
		return meta
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
}

func metaString(meta map[string]any, key string) string {
	if meta == nil {
		return ""
	}
	value, _ := meta[key].(string)
	return value
}

func normalizedOrchestratorSkillLabel(value string, maxChars int) (string, bool) {
	value, ok := normalizedOrchestratorSingleLine(value, maxChars)
	if !ok || value == "" {
		return "", false
	}
	if strings.ContainsAny(value, "&<>") {
		return "", false
	}
	return value, true
}

func normalizedOrchestratorSkillDescription(value string) (string, bool) {
	value = strings.Join(strings.Fields(value), " ")
	for _, ch := range value {
		if unicode.IsControl(ch) {
			return "", false
		}
	}
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(value), true
}

func normalizedOrchestratorSingleLine(value string, maxChars int) (string, bool) {
	value = strings.Join(strings.Fields(value), " ")
	if utf8.RuneCountInString(value) > maxChars {
		return "", false
	}
	for _, ch := range value {
		if unicode.IsControl(ch) {
			return "", false
		}
	}
	return value, true
}

func validatedOrchestratorSkillURI(raw string, maxChars int) (string, bool) {
	if utf8.RuneCountInString(raw) > maxChars {
		return "", false
	}
	for _, ch := range raw {
		if unicode.IsControl(ch) || unicode.IsSpace(ch) || ch == '<' || ch == '>' {
			return "", false
		}
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil {
		return "", false
	}
	if parsed.Scheme != "skill" || parsed.String() != raw || parsed.Host == "" || parsed.User != nil || parsed.Port() != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", false
	}
	segments := strings.Split(strings.TrimPrefix(parsed.Path, "/"), "/")
	if len(segments) == 0 {
		return "", false
	}
	for _, segment := range segments {
		if segment == "" {
			return "", false
		}
	}
	return raw, true
}

func orchestratorResourceBelongsToPackage(pkg string, resource string) bool {
	packageURI, ok := validatedOrchestratorSkillURI(pkg, maxOrchestratorSkillPackageURIChars)
	if !ok {
		return false
	}
	resourceURI, ok := validatedOrchestratorSkillURI(resource, maxOrchestratorSkillResourceURIChars)
	if !ok {
		return false
	}
	packageURL, _ := url.Parse(packageURI)
	resourceURL, _ := url.Parse(resourceURI)
	if packageURL.Scheme != resourceURL.Scheme || packageURL.Host != resourceURL.Host {
		return false
	}
	packageSegments := strings.Split(strings.TrimPrefix(packageURL.Path, "/"), "/")
	resourceSegments := strings.Split(strings.TrimPrefix(resourceURL.Path, "/"), "/")
	if len(resourceSegments) <= len(packageSegments) {
		return false
	}
	for i := range packageSegments {
		if packageSegments[i] != resourceSegments[i] {
			return false
		}
	}
	return true
}

func decodeStrictToolArgs(invocation *tool.Invocation, target any) error {
	if invocation == nil || target == nil {
		return fmt.Errorf("invalid tool invocation")
	}
	raw := strings.TrimSpace(invocation.Payload.Arguments)
	if raw == "" {
		raw = "{}"
	}
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("invalid trailing JSON")
	}
	return nil
}

func skillsToolJSONOutput(invocation *tool.Invocation, value any) (*tool.Output, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, tool.Fatal("failed to serialize skills." + invocation.ToolName.Name + " output")
	}
	return &tool.Output{
		CallID:   invocation.CallID,
		ToolName: invocation.ToolName,
		Success:  true,
		Body:     string(body),
		Data: map[string]any{
			"content_items": []any{map[string]any{"type": "input_text", "text": string(body)}},
		},
	}, nil
}

func skillsToolInputSchema(name string) map[string]any {
	authority := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"kind": map[string]any{"type": "string", "enum": []string{"orchestrator"}},
		},
		"required": []string{"kind"},
	}
	properties := map[string]any{"authority": authority}
	required := []string{"authority"}
	if name == skillsToolReadName {
		properties["package"] = map[string]any{"type": "string"}
		properties["resource"] = map[string]any{"type": "string"}
		required = append(required, "package", "resource")
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
		"required":             required,
	}
}

func stringPtrIfNotEmpty(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
