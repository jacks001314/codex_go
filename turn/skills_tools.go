package turn

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"codex_go/mcp"
	"codex_go/skillprovider"
	"codex_go/tool"
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
	maxSkillToolListPageEntries          = 20
	maxSkillToolResponseBytes            = 512 * 1024
	maxCatalogSkillDescriptionChars      = 1024
	skillDescriptionTruncatedSuffix      = "..."
	skillOversizedEntryWarning           = "Some skills were omitted because their metadata is too large."
)

type skillsToolAuthority struct {
	Kind string `json:"kind"`
	ID   string `json:"id,omitempty"`
}

type skillsListArgs struct {
	Authority skillsToolAuthority `json:"authority"`
	Cursor    *string             `json:"cursor,omitempty"`
}

type listedSkill struct {
	Authority    skillsToolAuthority `json:"authority"`
	Package      string              `json:"package"`
	Name         string              `json:"name"`
	Description  string              `json:"description"`
	MainResource string              `json:"main_resource"`
	PluginID     string              `json:"-"`
}

type skillsListResponse struct {
	Skills     []listedSkill `json:"skills"`
	Warnings   []string      `json:"warnings"`
	NextCursor *string       `json:"next_cursor"`
}

type skillsReadArgs struct {
	Authority skillsToolAuthority `json:"authority"`
	Package   string              `json:"package"`
	Resource  string              `json:"resource"`
	Cursor    *string             `json:"cursor,omitempty"`
}

type skillsReadResponse struct {
	Resource   string  `json:"resource"`
	Contents   string  `json:"contents"`
	SkillRoot  *string `json:"skill_root,omitempty"`
	NextCursor *string `json:"next_cursor"`
}

type skillsToolExecutor struct {
	spec       tool.Spec
	mcpService *mcp.MCPService
	threadID   string
	name       string
	catalog    *orchestratorSkillCatalogCache
	providers  *skillprovider.Registry
	executor   *executorSkillCatalogCache
	readMu     sync.Mutex
	read       *executorReadSnapshot
}

type executorReadSnapshot struct {
	authority skillsToolAuthority
	pkg       string
	result    skillprovider.ReadResult
}

type OrchestratorSkillMetadata struct {
	Package      string
	Name         string
	Description  string
	MainResource string
	PluginID     string
}

type OrchestratorSkillCatalog struct {
	Skills   []OrchestratorSkillMetadata
	Warnings []string
}

type orchestratorSkillCatalogCache struct {
	once    sync.Once
	catalog OrchestratorSkillCatalog
}

type executorSkillCatalogCache struct {
	mu      sync.Mutex
	catalog skillprovider.Catalog
}

func registerSkillsTools(registry *tool.Registry, options *ToolRegistryOptions) error {
	if registry == nil || options == nil {
		return nil
	}
	orchestratorAvailable := options.MCPService != nil && (options.OrchestratorSkillsEnabled == nil || *options.OrchestratorSkillsEnabled)
	if !orchestratorAvailable && options.SkillProviders == nil {
		return nil
	}
	catalog := &orchestratorSkillCatalogCache{}
	executor := &executorSkillCatalogCache{}
	list := newSkillsToolExecutor(options, skillsToolListName, "List skills owned by the requested authority. Returns the exact authority, package, and main_resource values required by skills.read. Pass next_cursor back as cursor to continue.", catalog, executor)
	if err := registry.Register(list); err != nil {
		return err
	}
	read := newSkillsToolExecutor(options, skillsToolReadName, "Read one page from a skill. Pass its provided package directly; root aliases are resolved automatically. Omit resource to read SKILL.md; to read another file, use the same package and pass the file's complete skill:// identifier as resource. For executor-backed skills, skill_root is the skill's absolute directory in the executor filesystem and can be used to locate bundled scripts. If the package is not provided, use skills.list to find it. Pass next_cursor back as cursor to continue.", catalog, executor)
	return registry.Register(read)
}

func newSkillsToolExecutor(options *ToolRegistryOptions, name string, description string, catalog *orchestratorSkillCatalogCache, executor *executorSkillCatalogCache) *skillsToolExecutor {
	return &skillsToolExecutor{
		spec: tool.Spec{
			Name:        tool.NamespacedName(skillsToolNamespace, name),
			Description: description,
			InputSchema: skillsToolInputSchema(name),
			// Rust #52245: skills list/read are read-only and parallel-safe.
			Parallel:             true,
			NamespaceDescription: fmt.Sprintf("Tools in the %s namespace.", skillsToolNamespace),
		},
		mcpService: options.MCPService,
		threadID:   strings.TrimSpace(options.ThreadID),
		name:       name,
		catalog:    catalog,
		providers:  options.SkillProviders,
		executor:   executor,
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
		return e.executeList(ctx, invocation)
	case skillsToolReadName:
		return e.executeRead(ctx, invocation)
	default:
		return nil, tool.Fatal("unknown skills tool")
	}
}

func (e *skillsToolExecutor) executeList(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
	var args skillsListArgs
	if err := decodeStrictToolArgs(invocation, &args); err != nil {
		return nil, tool.RespondToModel(err.Error())
	}
	if err := validateSkillsToolAuthoritySelector(args.Authority); err != nil {
		return nil, err
	}
	listed, warnings := e.listedSkills(ctx, args.Authority.Kind)
	responseByteBudget := invocation.ResponseByteBudget(maxSkillToolResponseBytes)
	start, previousBudget, err := parseSkillListCursor(args.Cursor, listed, "skills.list")
	if err != nil {
		return nil, err
	}
	if start > len(listed) {
		return nil, tool.RespondToModel("skills.list cursor is invalid")
	}
	omittedOversized := false
	retained := make([]indexedListedSkill, 0, len(listed)-start)
	for index := len(listed) - 1; index >= start; index-- {
		var nextCursor *string
		if len(retained) > 0 {
			cursor := skillListCursorWithBudget(listed, index+1, responseByteBudget)
			nextCursor = &cursor
		}
		if skillEntryResponseBounded(listed[index], responseByteBudget, nextCursor) {
			retained = append(retained, indexedListedSkill{index: index, skill: listed[index]})
		} else {
			omittedOversized = true
		}
	}
	for left, right := 0, len(retained)-1; left < right; left, right = left+1, right-1 {
		retained[left], retained[right] = retained[right], retained[left]
	}
	providerWarnings := []string{}
	if args.Cursor == nil {
		providerWarnings = boundedSkillToolWarnings(warnings)
	}
	omissionWarning := ""
	if omittedOversized && (previousBudget == nil || *previousBudget != responseByteBudget) {
		omissionWarning = skillOversizedEntryWarning
	}
	end := maxSkillToolListPageEntries
	if end > len(retained) {
		end = len(retained)
	}
	for {
		response := skillsListResponse{Warnings: append([]string(nil), providerWarnings...)}
		for _, item := range retained[:end] {
			response.Skills = append(response.Skills, item.skill)
		}
		if omissionWarning != "" {
			response.Warnings = append(response.Warnings, omissionWarning)
		}
		if end < len(retained) {
			cursor := skillListCursorWithBudget(listed, retained[end-1].index+1, responseByteBudget)
			response.NextCursor = &cursor
		}
		if skillToolSerializedLen(response) <= responseByteBudget {
			return skillsToolJSONOutput(invocation, response)
		}
		if end > 1 {
			end--
		} else if len(providerWarnings) > 1 {
			providerWarnings = providerWarnings[:len(providerWarnings)-1]
		} else if len(response.Warnings) > 0 {
			response.Skills = nil
			cursor := skillListCursorWithBudget(listed, start, responseByteBudget)
			response.NextCursor = &cursor
			if skillToolSerializedLen(response) <= responseByteBudget {
				return skillsToolJSONOutput(invocation, response)
			}
			if omissionWarning != "" && len(providerWarnings) > 0 {
				response.Warnings = append([]string(nil), providerWarnings...)
				legacyCursor := skillListLegacyCursor(listed, start)
				response.NextCursor = &legacyCursor
				if skillToolSerializedLen(response) <= responseByteBudget {
					return skillsToolJSONOutput(invocation, response)
				}
			}
			return nil, tool.RespondToModel("skills.list response budget leaves no room for discovery warnings")
		} else {
			return nil, tool.RespondToModel("skill metadata is too large to list")
		}
	}
}

type indexedListedSkill struct {
	index int
	skill listedSkill
}

func skillEntryResponseBounded(skill listedSkill, budget int, nextCursor *string) bool {
	response := skillsListResponse{Skills: []listedSkill{skill}}
	response.NextCursor = nextCursor
	return skillToolSerializedLen(response) <= budget
}

func skillListCursorWithBudget(value any, offset int, budget int) string {
	encoded, _ := json.Marshal(value)
	fingerprint := sha256.Sum256(encoded)
	return fmt.Sprintf("%x:%d:%d", fingerprint[:8], offset, budget)
}

func skillListLegacyCursor(value any, offset int) string {
	encoded, _ := json.Marshal(value)
	fingerprint := sha256.Sum256(encoded)
	return fmt.Sprintf("%x:%d", fingerprint[:8], offset)
}

// parseSkillListCursor accepts legacy fingerprint:offset cursors without a
// budget and new fingerprint:offset:budget cursors (Rust #40413).
func parseSkillListCursor(cursor *string, value any, toolName string) (int, *int, error) {
	if cursor == nil {
		return 0, nil, nil
	}
	raw := strings.TrimSpace(*cursor)
	canonical := raw
	var previousBudget *int
	parts := strings.Split(raw, ":")
	switch len(parts) {
	case 3:
		canonical = parts[0] + ":" + parts[1]
		budget, err := strconv.Atoi(parts[2])
		if err != nil || budget < 0 {
			return 0, nil, tool.RespondToModel(toolName + " cursor is invalid")
		}
		previousBudget = &budget
	case 2:
	default:
		return 0, nil, tool.RespondToModel(toolName + " cursor is invalid")
	}
	fingerprint, offsetText, ok := strings.Cut(canonical, ":")
	if !ok {
		return 0, nil, tool.RespondToModel(toolName + " cursor is invalid")
	}
	want := skillListLegacyCursor(value, 0)
	wantFingerprint, _, _ := strings.Cut(want, ":")
	if fingerprint != wantFingerprint {
		return 0, nil, tool.RespondToModel(toolName + " cursor is stale; restart from the first page")
	}
	offset, err := strconv.Atoi(offsetText)
	if err != nil || offset < 0 {
		return 0, nil, tool.RespondToModel(toolName + " cursor is invalid")
	}
	return offset, previousBudget, nil
}

func (e *skillsToolExecutor) executeRead(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
	var args skillsReadArgs
	if err := decodeStrictToolArgs(invocation, &args); err != nil {
		return nil, tool.RespondToModel(err.Error())
	}
	responseByteBudget := invocation.ResponseByteBudget(maxSkillToolResponseBytes)
	authority := args.Authority
	if err := validateSkillsToolReadAuthority(authority); err != nil {
		return nil, err
	}
	if err := validateSkillToolHandle("package", args.Package, maxSkillToolHandleBytes); err != nil {
		return nil, err
	}
	entry, ok := e.availableSkillPackage(ctx, authority, args.Package)
	if !ok {
		return nil, tool.RespondToModel("skill package is not available from the requested authority")
	}
	resource := args.Resource
	if resource == "" {
		// Rust 69ae78291d (#38167): omitting resource reads the package's
		// SKILL.md directly without requiring a skills.list lookup.
		resource = entry.MainResource
	}
	if err := validateSkillToolHandle("resource", resource, maxSkillToolHandleBytes); err != nil {
		return nil, err
	}
	if !skillResourceBelongsToPackage(authority, args.Package, resource) {
		return nil, tool.RespondToModel("skill package is not available from the requested authority")
	}
	var result skillprovider.ReadResult
	var err error
	cached := false
	if authority.Kind == "executor" && args.Cursor != nil {
		e.readMu.Lock()
		if e.read != nil && e.read.authority == authority && e.read.pkg == args.Package && e.read.result.Resource == resource {
			result = e.read.result
			cached = true
		}
		e.readMu.Unlock()
	}
	if !cached {
		// A cached continuation reuses the retained executor snapshot instead
		// of rereading the whole resource for every page.
		switch {
		case authority.Kind == "orchestrator":
			var contents string
			contents, err = ReadOrchestratorSkillResource(e.mcpService, e.threadID, args.Package, resource)
			result = skillprovider.ReadResult{Resource: resource, Contents: contents}
		case e.providers != nil:
			result, err = e.providers.Read(ctx, skillprovider.ReadRequest{
				Authority: skillprovider.Authority{Kind: skillprovider.SourceExecutor, ID: authority.ID},
				PackageID: entry.Package,
				Resource:  resource,
			})
		default:
			err = errors.New("executor skill provider is not configured")
		}
	}
	if err != nil {
		return nil, tool.RespondToModel("failed to read skill resource")
	}
	if result.Resource != resource {
		return nil, tool.Fatal("skill provider returned a different resource")
	}
	start, cursorErr := parseSkillToolCursor(args.Cursor, result.Contents, "skills.read")
	if cursorErr != nil {
		return nil, cursorErr
	}
	if start > len(result.Contents) || !utf8.ValidString(result.Contents) || !isUTF8Boundary(result.Contents, start) {
		return nil, tool.RespondToModel("skills.read cursor is invalid")
	}
	var skillRoot *string
	if authority.Kind == "executor" {
		if root := executorSkillRoot(entry.MainResource); root != "" {
			skillRoot = stringPtrIfNotEmpty(root)
		}
	}
	response, pageErr := skillReadPage(result.Resource, result.Contents, skillRoot, start, responseByteBudget)
	if pageErr != nil {
		return nil, pageErr
	}
	if authority.Kind == "executor" && response.NextCursor != nil {
		e.readMu.Lock()
		e.read = &executorReadSnapshot{
			authority: authority,
			pkg:       args.Package,
			result:    result,
		}
		e.readMu.Unlock()
	}
	return skillsToolJSONOutput(invocation, response)
}

func (e *skillsToolExecutor) orchestratorSkillCatalog() OrchestratorSkillCatalog {
	if e == nil {
		return OrchestratorSkillCatalog{Skills: []OrchestratorSkillMetadata{}, Warnings: []string{}}
	}
	if e.catalog == nil {
		e.catalog = &orchestratorSkillCatalogCache{}
	}
	e.catalog.once.Do(func() {
		catalog, err := LoadOrchestratorSkillCatalog(e.mcpService, e.threadID)
		if err != nil {
			catalog.Warnings = append(catalog.Warnings, "orchestrator skills unavailable: "+err.Error())
		}
		e.catalog.catalog = catalog
	})
	return cloneOrchestratorSkillCatalog(e.catalog.catalog)
}

func LoadOrchestratorSkillCatalog(service *mcp.MCPService, threadID string) (OrchestratorSkillCatalog, error) {
	out := OrchestratorSkillCatalog{Skills: []OrchestratorSkillMetadata{}, Warnings: []string{}}
	if service == nil {
		return out, nil
	}
	status := service.ListStatus(&mcp.MCPListServerStatusParams{
		ThreadID: stringPtrIfNotEmpty(threadID),
		Detail:   &mcp.MCPServerStatusDetail{Mode: mcp.MCPServerStatusDetailFull},
	})
	for _, server := range status.Data {
		if server.Name != mcp.RuntimeCodexAppsMCPServerName && server.Server.Name != mcp.RuntimeCodexAppsMCPServerName {
			continue
		}
		if server.State == mcp.MCPServerFailed && server.Error != nil {
			return out, fmt.Errorf("failed to list orchestrator skill resources: %s", strings.TrimSpace(*server.Error))
		}
		skipped := 0
		seen := 0
		truncated := false
		for _, resource := range server.Resources {
			if resource.MimeType != orchestratorSkillMimeType {
				continue
			}
			if seen >= 100 {
				truncated = true
				break
			}
			seen++
			entry, ok := orchestratorSkillMetadataFromResource(resource)
			if !ok {
				skipped++
				continue
			}
			out.Skills = append(out.Skills, entry)
		}
		if truncated {
			out.Warnings = append(out.Warnings, "Orchestrator skill discovery was truncated at 100 skills or 10 resource pages.")
		}
		if skipped > 0 {
			out.Warnings = append(out.Warnings, fmt.Sprintf("Skipped %d malformed orchestrator skill resources.", skipped))
		}
	}
	return out, nil
}

func (e *skillsToolExecutor) orchestratorSkillPackageAvailable(pkg string) bool {
	catalog := e.orchestratorSkillCatalog()
	for _, skill := range catalog.Skills {
		if skill.Package == pkg {
			return true
		}
	}
	return false
}

func (e *skillsToolExecutor) executorSkillCatalog(ctx context.Context) skillprovider.Catalog {
	if e == nil || e.providers == nil {
		return skillprovider.Catalog{}
	}
	if e.executor == nil {
		e.executor = &executorSkillCatalogCache{}
	}
	catalog := e.providers.ListKind(ctx, skillprovider.SourceExecutor, skillprovider.ListQuery{})
	e.executor.mu.Lock()
	e.executor.catalog = catalog
	e.executor.mu.Unlock()
	// The provider owns per-root caching. Returning this request's catalog keeps
	// overlapping steps from reading a different step's selected roots (Rust
	// #50050).
	return cloneSkillProviderCatalog(catalog)
}

func cloneSkillProviderCatalog(catalog skillprovider.Catalog) skillprovider.Catalog {
	out := skillprovider.Catalog{Warnings: append([]string(nil), catalog.Warnings...)}
	out.Entries = make([]skillprovider.CatalogEntry, len(catalog.Entries))
	for i := range catalog.Entries {
		out.Entries[i] = catalog.Entries[i]
		out.Entries[i].Dependencies = append([]skillprovider.ToolDependency(nil), catalog.Entries[i].Dependencies...)
	}
	return out
}

func (e *skillsToolExecutor) listedSkills(ctx context.Context, kind string) ([]listedSkill, []string) {
	if kind == "orchestrator" {
		catalog := e.orchestratorSkillCatalog()
		listed := make([]listedSkill, 0, len(catalog.Skills))
		for _, skill := range catalog.Skills {
			listed = append(listed, listedSkill{
				Authority:    skillsToolAuthority{Kind: "orchestrator"},
				Package:      skill.Package,
				Name:         skill.Name,
				Description:  skill.Description,
				MainResource: skill.MainResource,
			})
		}
		return listed, catalog.Warnings
	}
	catalog := e.executorSkillCatalog(ctx)
	listed := make([]listedSkill, 0, len(catalog.Entries))
	for _, entry := range catalog.Entries {
		if !entry.Enabled || !entry.PromptVisible || entry.Authority.Kind != skillprovider.SourceExecutor {
			continue
		}
		if !isBoundedSkillToolHandle(entry.Authority.ID, maxSkillToolHandleBytes) || !isBoundedSkillToolHandle(entry.PackageID, maxSkillToolHandleBytes) || !isBoundedSkillToolHandle(entry.MainResource, maxSkillToolHandleBytes) {
			continue
		}
		listed = append(listed, listedSkill{
			Authority:    skillsToolAuthority{Kind: "executor", ID: entry.Authority.ID},
			Package:      entry.PackageID,
			Name:         truncateUTF8Bytes(entry.Name, maxOrchestratorQualifiedNameChars),
			Description:  truncateCatalogSkillDescription(entry.Description),
			MainResource: entry.MainResource,
		})
	}
	return listed, catalog.Warnings
}

func (e *skillsToolExecutor) availableSkillPackage(ctx context.Context, authority skillsToolAuthority, pkg string) (listedSkill, bool) {
	listed, _ := e.listedSkills(ctx, authority.Kind)
	if authority.Kind == "executor" {
		catalog := e.executorSkillCatalog(ctx)
		for _, entry := range catalog.Entries {
			if entry.Enabled && entry.Authority.Kind == skillprovider.SourceExecutor && entry.Authority.ID == authority.ID && skillPackageMatchesAlias(catalog.Entries, entry.PackageID, pkg) {
				return listedSkill{Authority: authority, Package: entry.PackageID, Name: entry.Name, Description: entry.Description, MainResource: entry.MainResource}, true
			}
		}
		return listedSkill{}, false
	}
	for _, entry := range listed {
		if skillPackageMatchesAlias(orchestratorCatalogAsSkillProviderEntries(e.orchestratorSkillCatalog()), entry.Package, pkg) {
			return entry, true
		}
	}
	return listedSkill{}, false
}

// skillPackageMatchesAlias accepts the complete package locator or the short
// alias rendered in the skills catalog under metadata pressure
// (Rust #38261). Aliases are derived from the leading URI segments and are
// stable for a single authority/kind list.
func skillPackageMatchesAlias(entries []skillprovider.CatalogEntry, packageID string, candidate string) bool {
	if packageID == "" || candidate == "" {
		return false
	}
	if packageID == candidate {
		return true
	}
	visible := make([]skillprovider.CatalogEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Enabled && entry.PromptVisible {
			visible = append(visible, entry)
		}
	}
	roots := skillAliasRoots(visible)
	prefix := "e"
	for _, entry := range visible {
		if entry.Authority.Kind == skillprovider.SourceOrchestrator {
			prefix = "o"
			break
		}
	}
	short := skillShortPackageLocator(packageID, roots, prefix)
	return short == candidate
}

func skillAliasRoots(entries []skillprovider.CatalogEntry) []string {
	seen := map[string]bool{}
	roots := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.PackageID == "" {
			continue
		}
		root := extensionSkillAliasRoot(entry.PackageID)
		if root == "" || seen[root] {
			continue
		}
		seen[root] = true
		roots = append(roots, root)
	}
	return roots
}

func skillShortPackageLocator(packageID string, roots []string, prefix string) string {
	for index, root := range roots {
		rootPrefix := strings.TrimSuffix(root, "/")
		relative, ok := strings.CutPrefix(packageID, rootPrefix)
		if !ok || relative == "" || !strings.HasPrefix(relative, "/") {
			continue
		}
		relative = strings.TrimPrefix(relative, "/")
		return fmt.Sprintf("%s%d/%s", prefix, index, relative)
	}
	return ""
}

func extensionSkillAliasRoot(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	scheme := ""
	trimmed := path
	if index := strings.Index(trimmed, "://"); index >= 0 {
		scheme = trimmed[:index+3]
		trimmed = trimmed[index+3:]
		if strings.HasPrefix(trimmed, "/") {
			scheme += "/"
			trimmed = strings.TrimLeft(trimmed, "/")
		}
	}
	segments := strings.Split(trimmed, "/")
	if len(segments) < 3 {
		return ""
	}
	return scheme + segments[0] + "/" + segments[1]
}

func orchestratorCatalogAsSkillProviderEntries(catalog OrchestratorSkillCatalog) []skillprovider.CatalogEntry {
	entries := make([]skillprovider.CatalogEntry, 0, len(catalog.Skills))
	for _, skill := range catalog.Skills {
		entries = append(entries, skillprovider.CatalogEntry{
			PackageID:     skill.Package,
			Authority:     skillprovider.Authority{Kind: skillprovider.SourceOrchestrator},
			Enabled:       true,
			PromptVisible: true,
			MainResource:  skill.MainResource,
		})
	}
	return entries
}

func executorSkillRoot(mainResource string) string {
	mainResource = strings.TrimSpace(mainResource)
	if mainResource == "" {
		return ""
	}
	if index := strings.LastIndex(mainResource, "/"); index >= 0 {
		return strings.TrimRight(mainResource[:index], "/")
	}
	return mainResource
}

func ReadOrchestratorSkillResource(service *mcp.MCPService, threadID string, pkg string, resource string) (string, error) {
	if service == nil {
		return "", errors.New("session MCP resource client is not configured")
	}
	if !orchestratorResourceBelongsToPackage(pkg, resource) {
		return "", errors.New("orchestrator skill resource does not match its package")
	}
	response, err := service.ReadResource(&mcp.MCPResourceReadParams{
		ThreadID: stringPtrIfNotEmpty(threadID),
		Server:   mcp.RuntimeCodexAppsMCPServerName,
		URI:      resource,
	})
	if err != nil {
		return "", fmt.Errorf("failed to read orchestrator skill resource %s: %w", resource, err)
	}
	contents, ok := matchingOrchestratorSkillText(response, resource)
	if !ok {
		return "", fmt.Errorf("orchestrator skill resource %s did not return matching text contents", resource)
	}
	if len(contents) > maxOrchestratorSkillResourceBytes {
		return "", fmt.Errorf("orchestrator skill resource %s exceeds the %d-byte read limit", resource, maxOrchestratorSkillResourceBytes)
	}
	return contents, nil
}

func orchestratorSkillMetadataFromResource(resource mcp.MCPResource) (OrchestratorSkillMetadata, bool) {
	entry, ok := listedSkillFromOrchestratorResource(resource)
	if !ok {
		return OrchestratorSkillMetadata{}, false
	}
	return OrchestratorSkillMetadata{
		Package:      entry.Package,
		Name:         entry.Name,
		Description:  entry.Description,
		MainResource: entry.MainResource,
		PluginID:     entry.PluginID,
	}, true
}

func cloneOrchestratorSkillCatalog(catalog OrchestratorSkillCatalog) OrchestratorSkillCatalog {
	return OrchestratorSkillCatalog{
		Skills:   append([]OrchestratorSkillMetadata(nil), catalog.Skills...),
		Warnings: append([]string(nil), catalog.Warnings...),
	}
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
		PluginID:     strings.TrimSpace(metaString(meta, "plugin_id")),
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

func validateSkillsToolAuthoritySelector(authority skillsToolAuthority) error {
	if authority.ID != "" {
		return tool.RespondToModel("unknown field `id`")
	}
	if authority.Kind != "orchestrator" && authority.Kind != "executor" {
		return tool.RespondToModel("unknown variant `" + authority.Kind + "`, expected `orchestrator` or `executor`")
	}
	return nil
}

func validateSkillsToolReadAuthority(authority skillsToolAuthority) error {
	switch authority.Kind {
	case "orchestrator":
		if authority.ID != "" {
			return tool.RespondToModel("unknown field `id`")
		}
	case "executor":
		if err := validateSkillToolHandle("authority.id", authority.ID, maxSkillToolHandleBytes); err != nil {
			return err
		}
	default:
		return tool.RespondToModel("unknown variant `" + authority.Kind + "`, expected `orchestrator` or `executor`")
	}
	return nil
}

func skillResourceBelongsToPackage(authority skillsToolAuthority, pkg string, resource string) bool {
	if authority.Kind == "orchestrator" {
		return orchestratorResourceBelongsToPackage(pkg, resource)
	}
	packageURI, ok := validatedExecutorSkillURI(pkg, authority.ID)
	if !ok {
		return false
	}
	resourceURI, ok := validatedExecutorSkillURI(resource, authority.ID)
	if !ok {
		return false
	}
	prefix := strings.TrimRight(packageURI, "/") + "/"
	relative := strings.TrimPrefix(resourceURI, prefix)
	if relative == resourceURI || relative == "" {
		return false
	}
	for _, segment := range strings.Split(relative, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func validatedExecutorSkillURI(raw string, authorityID string) (string, bool) {
	if !isBoundedSkillToolHandle(raw, maxSkillToolHandleBytes) || strings.TrimSpace(authorityID) == "" {
		return "", false
	}
	prefix := "skill://" + authorityID + "/"
	if !strings.HasPrefix(raw, prefix) || strings.ContainsAny(raw, "?#") {
		return "", false
	}
	segments := strings.Split(strings.TrimPrefix(raw, prefix), "/")
	if len(segments) == 0 {
		return "", false
	}
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return "", false
		}
	}
	return raw, true
}

func skillToolCursor(value any, offset int) string {
	encoded, _ := json.Marshal(value)
	fingerprint := sha256.Sum256(encoded)
	return fmt.Sprintf("%x:%d", fingerprint[:8], offset)
}

func parseSkillToolCursor(cursor *string, value any, toolName string) (int, error) {
	if cursor == nil {
		return 0, nil
	}
	raw := strings.TrimSpace(*cursor)
	fingerprint, offsetText, ok := strings.Cut(raw, ":")
	if !ok {
		return 0, tool.RespondToModel(toolName + " cursor is invalid")
	}
	want := skillToolCursor(value, 0)
	wantFingerprint, _, _ := strings.Cut(want, ":")
	if fingerprint != wantFingerprint {
		return 0, tool.RespondToModel(toolName + " cursor is stale; restart from the first page")
	}
	var offset int
	if _, err := fmt.Sscanf(offsetText, "%d", &offset); err != nil || offset < 0 {
		return 0, tool.RespondToModel(toolName + " cursor is invalid")
	}
	return offset, nil
}

func skillToolSerializedLen(value any) int {
	encoded, err := json.Marshal(value)
	if err != nil {
		return maxSkillToolResponseBytes + 1
	}
	return len(encoded)
}

func skillReadPage(resource string, contents string, skillRoot *string, start int, maxResponseBytes int) (skillsReadResponse, error) {
	complete := skillsReadResponse{Resource: resource, Contents: contents[start:], SkillRoot: cloneStringPtr(skillRoot)}
	if skillToolSerializedLen(complete) <= maxResponseBytes {
		return complete, nil
	}
	lower := start
	upper := len(contents)
	var best *skillsReadResponse
	for lower < upper {
		end := lower + (upper-lower+1)/2
		for end > lower && !isUTF8Boundary(contents, end) {
			end--
		}
		if end == lower {
			break
		}
		cursor := skillToolCursor(contents, end)
		candidate := skillsReadResponse{Resource: resource, Contents: contents[start:end], SkillRoot: cloneStringPtr(skillRoot), NextCursor: &cursor}
		if skillToolSerializedLen(candidate) <= maxResponseBytes {
			lower = end
			best = &candidate
		} else {
			upper = end - 1
			for upper > lower && !isUTF8Boundary(contents, upper) {
				upper--
			}
		}
	}
	if best != nil {
		return *best, nil
	}
	return skillsReadResponse{}, tool.RespondToModel("skills.read response budget leaves no room for contents")
}

func isUTF8Boundary(value string, offset int) bool {
	return offset == 0 || offset == len(value) || offset > 0 && offset < len(value) && utf8.RuneStart(value[offset])
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

// decodeStrictToolArgs delegates to the shared deny-unknown-fields decoder so
// every Rust `deny_unknown_fields` argument struct decodes identically across
// tool families.
func decodeStrictToolArgs(invocation *tool.Invocation, target any) error {
	return invocation.DecodeStrictArguments(target)
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
	authoritySelector := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"kind": map[string]any{"type": "string", "enum": []string{"orchestrator", "executor"}},
		},
		"required": []string{"kind"},
	}
	properties := map[string]any{"authority": authoritySelector, "cursor": map[string]any{"type": "string"}}
	required := []string{"authority"}
	if name == skillsToolReadName {
		properties["authority"] = map[string]any{
			"oneOf": []any{
				map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"kind": map[string]any{"const": "orchestrator"}}, "required": []string{"kind"}},
				map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"kind": map[string]any{"const": "executor"}, "id": map[string]any{"type": "string"}}, "required": []string{"kind", "id"}},
			},
		}
		properties["package"] = map[string]any{"type": "string"}
		properties["resource"] = map[string]any{"type": "string"}
		required = append(required, "package")
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
