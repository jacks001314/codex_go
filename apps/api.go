package apps

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

var ErrInvalidAppRequest = errors.New("invalid app request")

type AppListParams struct {
	Cursor       *string `json:"cursor,omitempty"`
	Limit        *uint32 `json:"limit,omitempty"`
	ThreadID     *string `json:"threadId,omitempty"`
	ForceRefetch bool    `json:"forceRefetch,omitempty"`
}

type AppsReadParams struct {
	AppIDs []string `json:"appIds"`
	// ThreadID, when provided, evaluates effective app configuration against
	// the loaded thread (Rust 7f928f6ddc).
	ThreadID     *string `json:"threadId"`
	IncludeTools bool    `json:"includeTools,omitempty"`
}

type AppsInstalledParams struct {
	ThreadID     *string `json:"threadId"`
	ForceRefresh bool    `json:"forceRefresh,omitempty"`
}

type InstalledApp struct {
	ID          string  `json:"id"`
	RuntimeName *string `json:"runtimeName"`
	Enabled     bool    `json:"enabled"`
	Callable    bool    `json:"callable"`
}

type AppsInstalledResponse struct {
	Apps []InstalledApp `json:"apps"`
}

func (r *AppsInstalledResponse) MarshalJSON() ([]byte, error) {
	values := append([]InstalledApp(nil), r.Apps...)
	if values == nil {
		values = []InstalledApp{}
	}
	return json.Marshal(struct {
		Apps []InstalledApp `json:"apps"`
	}{Apps: values})
}

type AppToolSummary struct {
	Name           string  `json:"name"`
	Title          *string `json:"title"`
	Description    string  `json:"description"`
	IsEnabled      bool    `json:"isEnabled"`
	DisabledReason *string `json:"disabledReason"`
	IsReadOnly     bool    `json:"isReadOnly"`
}

func (s *AppToolSummary) UnmarshalJSON(data []byte) error {
	var decoded struct {
		Name           string  `json:"name"`
		Title          *string `json:"title"`
		Description    string  `json:"description"`
		IsEnabled      *bool   `json:"isEnabled"`
		DisabledReason *string `json:"disabledReason"`
		IsReadOnly     bool    `json:"isReadOnly"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	enabled := true
	if decoded.IsEnabled != nil {
		enabled = *decoded.IsEnabled
	}
	*s = AppToolSummary{
		Name:           decoded.Name,
		Title:          cloneStringPtr(decoded.Title),
		Description:    decoded.Description,
		IsEnabled:      enabled,
		DisabledReason: cloneStringPtr(decoded.DisabledReason),
		IsReadOnly:     decoded.IsReadOnly,
	}
	return nil
}

type ConnectorMetadata struct {
	ID             string           `json:"id"`
	Name           string           `json:"name"`
	Description    *string          `json:"description"`
	IconURL        *string          `json:"iconUrl"`
	ToolSummaries  []AppToolSummary `json:"toolSummaries"`
	ToolsRequested bool             `json:"-"`
}

func (m *ConnectorMetadata) MarshalJSON() ([]byte, error) {
	var tools []AppToolSummary
	if m.ToolsRequested {
		tools = append([]AppToolSummary(nil), m.ToolSummaries...)
	}
	return json.Marshal(struct {
		ID            string           `json:"id"`
		Name          string           `json:"name"`
		Description   *string          `json:"description"`
		IconURL       *string          `json:"iconUrl"`
		ToolSummaries []AppToolSummary `json:"toolSummaries"`
	}{
		ID: m.ID, Name: m.Name, Description: cloneStringPtr(m.Description), IconURL: cloneStringPtr(m.IconURL), ToolSummaries: tools,
	})
}

type AppsReadResponse struct {
	Apps          []ConnectorMetadata `json:"apps"`
	MissingAppIDs []string            `json:"missingAppIds"`
}

func (r *AppsReadResponse) MarshalJSON() ([]byte, error) {
	apps := append([]ConnectorMetadata(nil), r.Apps...)
	missing := append([]string(nil), r.MissingAppIDs...)
	if apps == nil {
		apps = []ConnectorMetadata{}
	}
	if missing == nil {
		missing = []string{}
	}
	return json.Marshal(struct {
		Apps          []ConnectorMetadata `json:"apps"`
		MissingAppIDs []string            `json:"missingAppIds"`
	}{Apps: apps, MissingAppIDs: missing})
}

type AppMetadataReadParams struct {
	AppIDs       []string
	IncludeTools bool
}

type AppMetadataReadResponse struct {
	Apps          []ConnectorMetadata
	MissingAppIDs []string
}

type AppMetadataProvider interface {
	ReadAppMetadata(params *AppMetadataReadParams) (*AppMetadataReadResponse, error)
}

type AppDirectoryListParams struct {
	ThreadID     string
	ForceRefetch bool
}

type AppDirectoryListResponse struct {
	Apps                []AppEntry
	AllConnectorsLoaded *bool
}

type AppDirectoryProvider interface {
	ListDirectoryApps(params *AppDirectoryListParams) (*AppDirectoryListResponse, error)
}

type AppAccessibleListParams struct {
	ThreadID     string
	ForceRefetch bool
}

type AppAccessibleListResponse struct {
	Apps           []AppEntry
	CodexAppsReady bool
}

type AppAccessibleProvider interface {
	ListAccessibleApps(params *AppAccessibleListParams) (*AppAccessibleListResponse, error)
}

type PluginConnector struct {
	ID                string
	Name              string
	Description       *string
	InstallURL        *string
	LogoURL           *string
	LogoURLDark       *string
	PluginDisplayName string
}

type IconAsset struct {
	URL       string `json:"url,omitempty"`
	MimeType  string `json:"mimeType,omitempty"`
	Theme     string `json:"theme,omitempty"`
	SizeBytes int64  `json:"sizeBytes,omitempty"`
}

type AppToolApproval string

const (
	AppToolApprovalAuto    AppToolApproval = "auto"
	AppToolApprovalPrompt  AppToolApproval = "prompt"
	AppToolApprovalWrites  AppToolApproval = "writes"
	AppToolApprovalApprove AppToolApproval = "approve"
)

// RestrictTo combines a parent and a requested approval policy without
// granting access beyond either policy (Rust AppToolApproval::restrict_to).
//
// Auto and Writes are incomparable: each can require approval for a tool the
// other would approve. Their conservative intersection is Prompt.
func (a AppToolApproval) RestrictTo(requested AppToolApproval) AppToolApproval {
	switch {
	case a == AppToolApprovalPrompt || requested == AppToolApprovalPrompt:
		return AppToolApprovalPrompt
	case a == AppToolApprovalApprove:
		return requested
	case requested == AppToolApprovalApprove:
		return a
	case a == AppToolApprovalAuto && requested == AppToolApprovalAuto:
		return AppToolApprovalAuto
	case a == AppToolApprovalWrites && requested == AppToolApprovalWrites:
		return AppToolApprovalWrites
	default:
		return AppToolApprovalPrompt
	}
}

type AppToolConfig struct {
	Enabled               *bool                `json:"enabled"`
	ApprovalMode          *AppToolApproval     `json:"approval_mode"`
	AnalyticsResultSource *AppToolResultSource `json:"analytics_result_source,omitempty"`
}

// AppToolResultSource describes opt-in analytics extraction for an app tool
// result (Rust #42164). Format is the result format; SourceType is the source
// kind emitted alongside each extracted ID.
type AppToolResultSource struct {
	Format     string `json:"format"`
	SourceType string `json:"type"`
}

type AppToolsConfig map[string]AppToolConfig

type AppsDefaultConfig struct {
	Enabled                  bool             `json:"enabled"`
	ApprovalsReviewer        *string          `json:"approvals_reviewer"`
	DestructiveEnabled       bool             `json:"destructive_enabled"`
	OpenWorldEnabled         bool             `json:"open_world_enabled"`
	DefaultToolsApprovalMode *AppToolApproval `json:"default_tools_approval_mode"`
}

type AppReview struct {
	Status string `json:"status"`
}

type AppBranding struct {
	Category          *string `json:"category"`
	Developer         *string `json:"developer"`
	Website           *string `json:"website"`
	PrivacyPolicy     *string `json:"privacyPolicy"`
	TermsOfService    *string `json:"termsOfService"`
	IsDiscoverableApp bool    `json:"isDiscoverableApp"`
}

type AppScreenshot struct {
	URL        *string `json:"url"`
	FileID     *string `json:"fileId"`
	UserPrompt string  `json:"userPrompt"`
}

type AppMetadata struct {
	Review                     *AppReview      `json:"review"`
	Categories                 []string        `json:"categories"`
	SubCategories              []string        `json:"subCategories"`
	SEODescription             *string         `json:"seoDescription"`
	Screenshots                []AppScreenshot `json:"screenshots"`
	Developer                  *string         `json:"developer"`
	Version                    *string         `json:"version"`
	VersionID                  *string         `json:"versionId"`
	VersionNotes               *string         `json:"versionNotes"`
	FirstPartyRequiresInstall  *bool           `json:"firstPartyRequiresInstall"`
	ShowInComposerWhenUnlinked *bool           `json:"showInComposerWhenUnlinked"`
}

type AppTemplateUnavailableReason string

const (
	AppTemplateUnavailableNotConfiguredForWorkspace AppTemplateUnavailableReason = "NOT_CONFIGURED_FOR_WORKSPACE"
	AppTemplateUnavailableNoActiveWorkspace         AppTemplateUnavailableReason = "NO_ACTIVE_WORKSPACE"
)

type AppEntry struct {
	ID                  string            `json:"id"`
	Name                string            `json:"name"`
	Description         *string           `json:"description"`
	InstallURL          *string           `json:"installUrl"`
	LogoURL             *string           `json:"logoUrl"`
	LogoURLDark         *string           `json:"logoUrlDark"`
	IconAssets          map[string]string `json:"iconAssets"`
	IconDarkAssets      map[string]string `json:"iconDarkAssets"`
	DistributionChannel *string           `json:"distributionChannel"`
	LightIcon           *IconAsset        `json:"lightIcon,omitempty"`
	DarkIcon            *IconAsset        `json:"darkIcon,omitempty"`
	Branding            any               `json:"branding"`
	AppMetadata         any               `json:"appMetadata"`
	Labels              []string          `json:"labels"`
	LabelMap            map[string]string `json:"-"`
	IsAccessible        bool              `json:"isAccessible"`
	IsEnabled           bool              `json:"isEnabled"`
	Enabled             bool              `json:"enabled,omitempty"`
	EnabledExplicit     bool              `json:"-"`
	PluginDisplayNames  []string          `json:"pluginDisplayNames"`
}

func (a *AppEntry) MarshalJSON() ([]byte, error) {
	var labels map[string]string
	if a.LabelMap != nil {
		labels = cloneStringMap(a.LabelMap)
	}
	iconAssets := cloneStringMap(a.IconAssets)
	iconDarkAssets := cloneStringMap(a.IconDarkAssets)
	pluginDisplayNames := append([]string(nil), a.PluginDisplayNames...)
	if pluginDisplayNames == nil {
		pluginDisplayNames = []string{}
	}
	return json.Marshal(struct {
		ID                  string            `json:"id"`
		Name                string            `json:"name"`
		Description         *string           `json:"description"`
		InstallURL          *string           `json:"installUrl"`
		LogoURL             *string           `json:"logoUrl"`
		LogoURLDark         *string           `json:"logoUrlDark"`
		IconAssets          map[string]string `json:"iconAssets"`
		IconDarkAssets      map[string]string `json:"iconDarkAssets"`
		DistributionChannel *string           `json:"distributionChannel"`
		Branding            any               `json:"branding"`
		AppMetadata         any               `json:"appMetadata"`
		Labels              map[string]string `json:"labels"`
		IsAccessible        bool              `json:"isAccessible"`
		IsEnabled           bool              `json:"isEnabled"`
		PluginDisplayNames  []string          `json:"pluginDisplayNames"`
	}{
		ID:                  a.ID,
		Name:                a.Name,
		Description:         a.Description,
		InstallURL:          a.InstallURL,
		LogoURL:             a.LogoURL,
		LogoURLDark:         a.LogoURLDark,
		IconAssets:          iconAssets,
		IconDarkAssets:      iconDarkAssets,
		DistributionChannel: a.DistributionChannel,
		Branding:            appBrandingForJSON(a.Branding),
		AppMetadata:         appMetadataForJSON(a.AppMetadata),
		Labels:              labels,
		IsAccessible:        a.IsAccessible,
		IsEnabled:           a.IsEnabled,
		PluginDisplayNames:  pluginDisplayNames,
	})
}

func (a *AppEntry) UnmarshalJSON(data []byte) error {
	type appEntryAlias AppEntry
	var raw struct {
		appEntryAlias
		Labels json.RawMessage `json:"labels"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*a = AppEntry(raw.appEntryAlias)
	a.Labels = nil
	a.LabelMap = nil
	if len(raw.Labels) == 0 || string(raw.Labels) == "null" {
		return nil
	}
	var labelMap map[string]string
	if err := json.Unmarshal(raw.Labels, &labelMap); err == nil {
		a.LabelMap = cloneStringMap(labelMap)
		return nil
	}
	var labels []string
	if err := json.Unmarshal(raw.Labels, &labels); err != nil {
		return err
	}
	a.Labels = append([]string(nil), labels...)
	return nil
}

type AppListResponse struct {
	Data       []AppEntry `json:"data"`
	NextCursor *string    `json:"nextCursor"`
	Apps       []AppEntry `json:"-"`
	AllApps    []AppEntry `json:"-"`
}

type AppListUpdatedNotification struct {
	Data []AppEntry `json:"data"`
}

func (n *AppListUpdatedNotification) MarshalJSON() ([]byte, error) {
	data := append([]AppEntry(nil), n.Data...)
	if data == nil {
		data = []AppEntry{}
	}
	return json.Marshal(struct {
		Data []AppEntry `json:"data"`
	}{Data: data})
}

func (r *AppListResponse) MarshalJSON() ([]byte, error) {
	data := append([]AppEntry(nil), r.Data...)
	if data == nil {
		data = []AppEntry{}
	}
	return json.Marshal(struct {
		Data       []AppEntry `json:"data"`
		NextCursor *string    `json:"nextCursor"`
	}{
		Data:       data,
		NextCursor: cloneStringPtr(r.NextCursor),
	})
}

type AppService struct {
	mu                     sync.Mutex
	apps                   []AppEntry
	directoryProvider      AppDirectoryProvider
	accessibleProvider     AppAccessibleProvider
	directoryProviderKey   string
	accessibleProviderKey  string
	pluginConnectors       []PluginConnector
	configValues           map[string]any
	directoryCache         []AppEntry
	directoryCacheValid    bool
	directoryAllLoaded     bool
	accessibleCaches       map[string]appAccessibleCacheEntry
	lastAccessibleCacheKey string
	metadataProvider       AppMetadataProvider
	metadataCache          map[string]ConnectorMetadata
}

type appAccessibleCacheEntry struct {
	apps  []AppEntry
	ready bool
}

func NewAppService(apps []AppEntry) *AppService {
	service := &AppService{}
	for _, app := range apps {
		service.Add(&app)
	}
	return service
}

func (s *AppService) Add(app *AppEntry) {
	if app == nil || strings.TrimSpace(app.ID) == "" {
		return
	}
	cloned := cloneApp(*app)
	s.mu.Lock()
	defer s.mu.Unlock()
	if cloned.Name == "" {
		cloned.Name = cloned.ID
	}
	for i := range s.apps {
		if s.apps[i].ID == cloned.ID {
			s.apps[i] = cloned
			return
		}
	}
	s.apps = append(s.apps, cloned)
}

func (s *AppService) SetDirectoryProvider(provider AppDirectoryProvider) {
	s.SetDirectoryProviderWithKey(provider, "")
}

func (s *AppService) SetDirectoryProviderWithKey(provider AppDirectoryProvider, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key = strings.TrimSpace(key)
	if s.directoryProviderKey == key && (s.directoryProvider == nil) == (provider == nil) {
		s.directoryProvider = provider
		return
	}
	s.directoryProvider = provider
	s.directoryProviderKey = key
	s.directoryCache = nil
	s.directoryCacheValid = false
	s.directoryAllLoaded = false
}

func (s *AppService) SetAccessibleProvider(provider AppAccessibleProvider) {
	s.SetAccessibleProviderWithKey(provider, "")
}

func (s *AppService) SetAccessibleProviderWithKey(provider AppAccessibleProvider, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key = strings.TrimSpace(key)
	if s.accessibleProviderKey == key && (s.accessibleProvider == nil) == (provider == nil) {
		s.accessibleProvider = provider
		return
	}
	s.accessibleProvider = provider
	s.accessibleProviderKey = key
	s.accessibleCaches = nil
	s.lastAccessibleCacheKey = ""
}

func (s *AppService) SetPluginConnectors(connectors []PluginConnector) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pluginConnectors = clonePluginConnectors(connectors)
}

func (s *AppService) SetConfigValues(values map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.configValues = cloneAnyMapApps(values)
}

func (s *AppService) ClearCache() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.directoryCache = nil
	s.directoryCacheValid = false
	s.directoryAllLoaded = false
	s.accessibleCaches = nil
	s.lastAccessibleCacheKey = ""
	s.metadataCache = nil
}

func (s *AppService) SetMetadataProvider(provider AppMetadataProvider) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.metadataProvider = provider
}

func (s *AppService) Read(params *AppsReadParams) (*AppsReadResponse, error) {
	if params == nil {
		params = &AppsReadParams{}
	}
	if len(params.AppIDs) > 100 {
		return nil, fmt.Errorf("%w: app/read accepts at most 100 appIds", ErrInvalidAppRequest)
	}
	ids := dedupeAppIDs(params.AppIDs)
	s.mu.Lock()
	provider := s.metadataProvider
	cached := make(map[string]ConnectorMetadata, len(s.metadataCache))
	for id, metadata := range s.metadataCache {
		cached[id] = cloneConnectorMetadata(metadata)
	}
	for _, app := range append(cloneApps(s.apps), s.directoryCache...) {
		if _, ok := cached[app.ID]; ok || strings.TrimSpace(app.ID) == "" {
			continue
		}
		cached[app.ID] = ConnectorMetadata{
			ID:          app.ID,
			Name:        app.Name,
			Description: cloneStringPtr(app.Description),
			IconURL:     cloneStringPtr(app.LogoURL),
		}
	}
	s.mu.Unlock()

	missing := make([]string, 0, len(ids))
	needFetch := make([]string, 0, len(ids))
	for _, id := range ids {
		metadata, ok := cached[id]
		if !ok || params.IncludeTools && !metadata.ToolsRequested && provider != nil {
			needFetch = append(needFetch, id)
		}
	}
	if len(needFetch) > 0 && provider != nil {
		response, err := provider.ReadAppMetadata(&AppMetadataReadParams{AppIDs: needFetch, IncludeTools: params.IncludeTools})
		if err != nil {
			return nil, err
		}
		if response != nil {
			for _, metadata := range response.Apps {
				metadata.ToolsRequested = params.IncludeTools
				cached[metadata.ID] = cloneConnectorMetadata(metadata)
			}
		}
		s.mu.Lock()
		if s.metadataCache == nil {
			s.metadataCache = map[string]ConnectorMetadata{}
		}
		for id, metadata := range cached {
			s.metadataCache[id] = cloneConnectorMetadata(metadata)
		}
		s.mu.Unlock()
	}

	apps := make([]ConnectorMetadata, 0, len(ids))
	for _, id := range ids {
		metadata, ok := cached[id]
		if !ok {
			missing = append(missing, id)
			continue
		}
		metadata.ToolsRequested = params.IncludeTools
		apps = append(apps, cloneConnectorMetadata(metadata))
	}
	return &AppsReadResponse{Apps: apps, MissingAppIDs: missing}, nil
}

func dedupeAppIDs(ids []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func cloneConnectorMetadata(metadata ConnectorMetadata) ConnectorMetadata {
	metadata.Description = cloneStringPtr(metadata.Description)
	metadata.IconURL = cloneStringPtr(metadata.IconURL)
	metadata.ToolSummaries = append([]AppToolSummary(nil), metadata.ToolSummaries...)
	for i := range metadata.ToolSummaries {
		metadata.ToolSummaries[i].Title = cloneStringPtr(metadata.ToolSummaries[i].Title)
		metadata.ToolSummaries[i].DisabledReason = cloneStringPtr(metadata.ToolSummaries[i].DisabledReason)
	}
	return metadata
}

func (s *AppService) List(params *AppListParams) (*AppListResponse, error) {
	if params == nil {
		params = &AppListParams{}
	}
	snapshot := s.snapshot()
	if snapshot.directoryProvider != nil || snapshot.accessibleProvider != nil || len(snapshot.pluginConnectors) > 0 || snapshot.configValues != nil {
		return s.listMerged(params, &snapshot)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	apps := make([]AppEntry, 0, len(s.apps))
	for _, app := range s.apps {
		apps = append(apps, cloneApp(app))
	}
	sort.SliceStable(apps, func(i int, j int) bool {
		if apps[i].Name != apps[j].Name {
			return apps[i].Name < apps[j].Name
		}
		return apps[i].ID < apps[j].ID
	})
	page, nextCursor, err := paginateApps(apps, params.Cursor, params.Limit)
	if err != nil {
		return nil, err
	}
	return &AppListResponse{Data: page, NextCursor: nextCursor, Apps: page, AllApps: cloneApps(apps)}, nil
}

func (s *AppService) Installed(params *AppsInstalledParams) (*AppsInstalledResponse, error) {
	if params == nil {
		params = &AppsInstalledParams{}
	}
	list, err := s.List(&AppListParams{ThreadID: params.ThreadID, ForceRefetch: params.ForceRefresh})
	if err != nil {
		return nil, err
	}
	values := make([]InstalledApp, 0, len(list.AllApps))
	for _, app := range list.AllApps {
		if !app.IsAccessible {
			continue
		}
		var runtimeName *string
		if name := strings.TrimSpace(app.Name); name != "" {
			runtimeName = &name
		}
		values = append(values, InstalledApp{
			ID:          app.ID,
			RuntimeName: runtimeName,
			Enabled:     app.IsEnabled,
			Callable:    app.IsEnabled && app.IsAccessible,
		})
	}
	sort.SliceStable(values, func(i, j int) bool { return values[i].ID < values[j].ID })
	return &AppsInstalledResponse{Apps: values}, nil
}

func (s *AppService) CachedListForNotification() []AppEntry {
	if s == nil {
		return nil
	}
	snapshot := s.snapshot()
	s.mu.Lock()
	directoryCacheValid := s.directoryCacheValid
	directoryCache := cloneApps(s.directoryCache)
	directoryAllLoaded := s.directoryAllLoaded
	accessibleCache, accessibleCacheValid, _ := s.accessibleCacheSnapshotLocked()
	s.mu.Unlock()
	if !directoryCacheValid && !accessibleCacheValid {
		return nil
	}

	directory := cloneApps(snapshot.staticApps)
	allLoaded := false
	if directoryCacheValid {
		allLoaded = directoryAllLoaded
		if len(directoryCache) == 0 {
			directory = cloneApps(snapshot.staticApps)
		} else {
			directory = mergeDirectorySnapshots(directoryCache, snapshot.staticApps)
		}
	}
	directory = MergePluginConnectors(directory, snapshot.pluginConnectors)

	accessible := accessibleStaticApps(snapshot.staticApps)
	if accessibleCacheValid {
		accessible = mergeAccessibleSnapshots(accessibleCache, accessible)
	}
	if allLoaded {
		accessible = filterAccessibleAppsForDirectory(accessible, directory)
	}
	list := MergeConnectors(directory, accessible)
	list = WithAppEnabledState(list, AppsConfigFromValues(snapshot.configValues), nil)
	if len(list) == 0 {
		return nil
	}
	return cloneApps(list)
}

type appServiceSnapshot struct {
	staticApps         []AppEntry
	directoryProvider  AppDirectoryProvider
	accessibleProvider AppAccessibleProvider
	pluginConnectors   []PluginConnector
	configValues       map[string]any
}

func (s *AppService) snapshot() appServiceSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return appServiceSnapshot{
		staticApps:         cloneApps(s.apps),
		directoryProvider:  s.directoryProvider,
		accessibleProvider: s.accessibleProvider,
		pluginConnectors:   clonePluginConnectors(s.pluginConnectors),
		configValues:       cloneAnyMapApps(s.configValues),
	}
}

func (s *AppService) listMerged(params *AppListParams, snapshot *appServiceSnapshot) (*AppListResponse, error) {
	threadID := ""
	if params.ThreadID != nil {
		threadID = strings.TrimSpace(*params.ThreadID)
	}
	directory, allLoaded, err := s.directoryAppsForList(snapshot.directoryProvider, &AppDirectoryListParams{
		ThreadID:     threadID,
		ForceRefetch: params.ForceRefetch,
	})
	if err != nil {
		return nil, err
	}
	if len(directory) == 0 {
		directory = cloneApps(snapshot.staticApps)
	} else {
		directory = mergeDirectorySnapshots(directory, snapshot.staticApps)
	}
	directory = MergePluginConnectors(directory, snapshot.pluginConnectors)
	accessible, _, err := s.accessibleAppsForList(snapshot.accessibleProvider, &AppAccessibleListParams{
		ThreadID:     threadID,
		ForceRefetch: params.ForceRefetch,
	}, snapshot.staticApps)
	if err != nil {
		return nil, err
	}
	if allLoaded {
		accessible = filterAccessibleAppsForDirectory(accessible, directory)
	}
	apps := MergeConnectors(directory, accessible)
	apps = WithAppEnabledState(apps, AppsConfigFromValues(snapshot.configValues), nil)
	page, nextCursor, err := paginateApps(apps, params.Cursor, params.Limit)
	if err != nil {
		return nil, err
	}
	return &AppListResponse{Data: page, NextCursor: nextCursor, Apps: page, AllApps: cloneApps(apps)}, nil
}

func (s *AppService) directoryAppsForList(provider AppDirectoryProvider, params *AppDirectoryListParams) ([]AppEntry, bool, error) {
	if provider == nil {
		return nil, false, nil
	}
	if params == nil {
		params = &AppDirectoryListParams{}
	}
	if !params.ForceRefetch {
		s.mu.Lock()
		if s.directoryCacheValid {
			apps := cloneApps(s.directoryCache)
			allLoaded := s.directoryAllLoaded
			s.mu.Unlock()
			return apps, allLoaded, nil
		}
		s.mu.Unlock()
	}
	response, err := provider.ListDirectoryApps(params)
	if err != nil {
		return nil, false, err
	}
	apps := []AppEntry{}
	allLoaded := true
	if response != nil {
		apps = cloneApps(response.Apps)
		if response.AllConnectorsLoaded != nil {
			allLoaded = *response.AllConnectorsLoaded
		}
	}
	s.mu.Lock()
	s.directoryCache = cloneApps(apps)
	s.directoryCacheValid = true
	s.directoryAllLoaded = allLoaded
	s.mu.Unlock()
	return apps, allLoaded, nil
}

func (s *AppService) accessibleAppsForList(provider AppAccessibleProvider, params *AppAccessibleListParams, staticApps []AppEntry) ([]AppEntry, bool, error) {
	accessible := accessibleStaticApps(staticApps)
	if provider == nil {
		return accessible, true, nil
	}
	if params == nil {
		params = &AppAccessibleListParams{}
	}
	var providerApps []AppEntry
	ready := false
	cacheKey := strings.TrimSpace(params.ThreadID)
	if !params.ForceRefetch {
		s.mu.Lock()
		if entry, ok := s.accessibleCaches[cacheKey]; ok {
			providerApps = cloneApps(entry.apps)
			ready = entry.ready
			s.mu.Unlock()
			return mergeAccessibleSnapshots(providerApps, accessible), ready, nil
		}
		s.mu.Unlock()
	}
	response, err := provider.ListAccessibleApps(params)
	if err != nil {
		return nil, false, err
	}
	if response != nil {
		providerApps = cloneApps(response.Apps)
		ready = response.CodexAppsReady
	}
	s.mu.Lock()
	if s.accessibleCaches == nil {
		s.accessibleCaches = map[string]appAccessibleCacheEntry{}
	}
	entry := appAccessibleCacheEntry{apps: cloneApps(providerApps)}
	if response != nil {
		entry.ready = response.CodexAppsReady
	}
	s.accessibleCaches[cacheKey] = entry
	s.lastAccessibleCacheKey = cacheKey
	s.mu.Unlock()
	return mergeAccessibleSnapshots(providerApps, accessible), ready, nil
}

func (s *AppService) accessibleCacheSnapshotLocked() ([]AppEntry, bool, bool) {
	if s == nil || len(s.accessibleCaches) == 0 {
		return nil, false, false
	}
	key := s.lastAccessibleCacheKey
	entry, ok := s.accessibleCaches[key]
	if !ok {
		for _, candidate := range s.accessibleCaches {
			entry = candidate
			ok = true
			break
		}
	}
	if !ok {
		return nil, false, false
	}
	return cloneApps(entry.apps), true, entry.ready
}

func cloneApp(app AppEntry) AppEntry {
	if app.EnabledExplicit {
		app.Enabled = app.IsEnabled
	} else if app.IsEnabled || !app.Enabled {
		app.Enabled = app.IsEnabled
	} else {
		app.IsEnabled = app.Enabled
	}
	app.Description = cloneStringPtr(app.Description)
	app.InstallURL = cloneStringPtr(app.InstallURL)
	app.LogoURL = cloneStringPtr(app.LogoURL)
	app.LogoURLDark = cloneStringPtr(app.LogoURLDark)
	app.DistributionChannel = cloneStringPtr(app.DistributionChannel)
	app.IconAssets = cloneStringMap(app.IconAssets)
	app.IconDarkAssets = cloneStringMap(app.IconDarkAssets)
	app.Labels = append([]string(nil), app.Labels...)
	app.LabelMap = cloneStringMap(app.LabelMap)
	app.PluginDisplayNames = append([]string(nil), app.PluginDisplayNames...)
	app.Branding = cloneAppAny(app.Branding)
	app.AppMetadata = cloneAppAny(app.AppMetadata)
	if app.LightIcon != nil {
		value := *app.LightIcon
		app.LightIcon = &value
	}
	if app.DarkIcon != nil {
		value := *app.DarkIcon
		app.DarkIcon = &value
	}
	return app
}

func paginateApps(apps []AppEntry, cursor *string, limit *uint32) ([]AppEntry, *string, error) {
	start := 0
	if cursor != nil {
		trimmedCursor := strings.TrimSpace(*cursor)
		idx, err := strconv.Atoi(trimmedCursor)
		if err == nil && idx >= 0 {
			start = idx
		} else {
			return nil, nil, fmt.Errorf("%w: invalid cursor: %s", ErrInvalidAppRequest, *cursor)
		}
	}
	if start >= len(apps) {
		return []AppEntry{}, nil, nil
	}
	effectiveLimit := len(apps)
	if limit != nil && *limit > 0 {
		effectiveLimit = int(*limit)
	}
	end := start + effectiveLimit
	if end > len(apps) {
		end = len(apps)
	}
	var next *string
	if end < len(apps) {
		value := strconv.Itoa(end)
		next = &value
	}
	return append([]AppEntry(nil), apps[start:end]...), next, nil
}

func cloneAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneAppAny(value any) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case map[string]any:
		return cloneAnyMap(typed)
	case AppBranding:
		return cloneAppBranding(&typed)
	case *AppBranding:
		return cloneAppBranding(typed)
	case AppMetadata:
		return cloneAppMetadata(&typed)
	case *AppMetadata:
		return cloneAppMetadata(typed)
	default:
		return value
	}
}

func appBrandingForJSON(value any) *AppBranding {
	switch typed := value.(type) {
	case nil:
		return nil
	case AppBranding:
		return cloneAppBranding(&typed)
	case *AppBranding:
		return cloneAppBranding(typed)
	case map[string]any:
		if len(typed) == 0 {
			return nil
		}
		branding := &AppBranding{
			Category:          stringPtrFromAnyApps(typed["category"]),
			Developer:         stringPtrFromAnyApps(typed["developer"]),
			Website:           firstStringPtrFromAnyApps(typed["website"], typed["homepageUrl"]),
			PrivacyPolicy:     firstStringPtrFromAnyApps(typed["privacyPolicy"], typed["privacy_policy"]),
			TermsOfService:    firstStringPtrFromAnyApps(typed["termsOfService"], typed["terms_of_service"]),
			IsDiscoverableApp: boolFromAnyApps(typed["isDiscoverableApp"]),
		}
		if branding.Category == nil && branding.Developer == nil && branding.Website == nil && branding.PrivacyPolicy == nil && branding.TermsOfService == nil {
			if _, ok := typed["isDiscoverableApp"]; !ok {
				return nil
			}
		}
		return branding
	case map[string]string:
		if len(typed) == 0 {
			return nil
		}
		asAny := make(map[string]any, len(typed))
		for key, value := range typed {
			asAny[key] = value
		}
		return appBrandingForJSON(asAny)
	default:
		return nil
	}
}

func appMetadataForJSON(value any) *AppMetadata {
	switch typed := value.(type) {
	case nil:
		return nil
	case AppMetadata:
		return cloneAppMetadata(&typed)
	case *AppMetadata:
		return cloneAppMetadata(typed)
	case map[string]any:
		if len(typed) == 0 {
			return &AppMetadata{}
		}
		return &AppMetadata{
			Review:                     appReviewFromAny(typed["review"]),
			Categories:                 stringSliceFromAnyApps(typed["categories"]),
			SubCategories:              firstStringSliceFromAnyApps(typed["subCategories"], typed["sub_categories"]),
			SEODescription:             firstStringPtrFromAnyApps(typed["seoDescription"], typed["seo_description"]),
			Screenshots:                appScreenshotsFromAny(typed["screenshots"]),
			Developer:                  stringPtrFromAnyApps(typed["developer"]),
			Version:                    stringPtrFromAnyApps(typed["version"]),
			VersionID:                  firstStringPtrFromAnyApps(typed["versionId"], typed["version_id"]),
			VersionNotes:               firstStringPtrFromAnyApps(typed["versionNotes"], typed["version_notes"]),
			FirstPartyRequiresInstall:  firstBoolPtrFromAnyApps(typed["firstPartyRequiresInstall"], typed["first_party_requires_install"]),
			ShowInComposerWhenUnlinked: firstBoolPtrFromAnyApps(typed["showInComposerWhenUnlinked"], typed["show_in_composer_when_unlinked"]),
		}
	default:
		return nil
	}
}

func cloneAppBranding(value *AppBranding) *AppBranding {
	if value == nil {
		return nil
	}
	return &AppBranding{
		Category:          cloneStringPtr(value.Category),
		Developer:         cloneStringPtr(value.Developer),
		Website:           cloneStringPtr(value.Website),
		PrivacyPolicy:     cloneStringPtr(value.PrivacyPolicy),
		TermsOfService:    cloneStringPtr(value.TermsOfService),
		IsDiscoverableApp: value.IsDiscoverableApp,
	}
}

func cloneAppMetadata(value *AppMetadata) *AppMetadata {
	if value == nil {
		return nil
	}
	screenshots := append([]AppScreenshot(nil), value.Screenshots...)
	for i := range screenshots {
		screenshots[i].URL = cloneStringPtr(screenshots[i].URL)
		screenshots[i].FileID = cloneStringPtr(screenshots[i].FileID)
	}
	return &AppMetadata{
		Review:                     cloneAppReview(value.Review),
		Categories:                 append([]string(nil), value.Categories...),
		SubCategories:              append([]string(nil), value.SubCategories...),
		SEODescription:             cloneStringPtr(value.SEODescription),
		Screenshots:                screenshots,
		Developer:                  cloneStringPtr(value.Developer),
		Version:                    cloneStringPtr(value.Version),
		VersionID:                  cloneStringPtr(value.VersionID),
		VersionNotes:               cloneStringPtr(value.VersionNotes),
		FirstPartyRequiresInstall:  cloneBoolPtr(value.FirstPartyRequiresInstall),
		ShowInComposerWhenUnlinked: cloneBoolPtr(value.ShowInComposerWhenUnlinked),
	}
}

func cloneAppReview(value *AppReview) *AppReview {
	if value == nil {
		return nil
	}
	return &AppReview{Status: value.Status}
}

func appReviewFromAny(value any) *AppReview {
	switch typed := value.(type) {
	case nil:
		return nil
	case AppReview:
		return cloneAppReview(&typed)
	case *AppReview:
		return cloneAppReview(typed)
	case map[string]any:
		return &AppReview{Status: stringFromAnyApps(typed["status"])}
	default:
		return nil
	}
}

func appScreenshotsFromAny(value any) []AppScreenshot {
	switch typed := value.(type) {
	case nil:
		return nil
	case []AppScreenshot:
		out := append([]AppScreenshot(nil), typed...)
		for i := range out {
			out[i].URL = cloneStringPtr(out[i].URL)
			out[i].FileID = cloneStringPtr(out[i].FileID)
		}
		return out
	case []any:
		out := make([]AppScreenshot, 0, len(typed))
		for _, item := range typed {
			if screenshot, ok := appScreenshotFromAny(item); ok {
				out = append(out, screenshot)
			}
		}
		return out
	default:
		return nil
	}
}

func appScreenshotFromAny(value any) (AppScreenshot, bool) {
	switch typed := value.(type) {
	case AppScreenshot:
		typed.URL = cloneStringPtr(typed.URL)
		typed.FileID = cloneStringPtr(typed.FileID)
		return typed, true
	case *AppScreenshot:
		if typed == nil {
			return AppScreenshot{}, false
		}
		clone := *typed
		clone.URL = cloneStringPtr(typed.URL)
		clone.FileID = cloneStringPtr(typed.FileID)
		return clone, true
	case map[string]any:
		return AppScreenshot{
			URL:        stringPtrFromAnyApps(typed["url"]),
			FileID:     firstStringPtrFromAnyApps(typed["fileId"], typed["file_id"]),
			UserPrompt: stringFromAnyApps(typed["userPrompt"]),
		}, true
	default:
		return AppScreenshot{}, false
	}
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneBoolPtr(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func firstStringPtrFromAnyApps(values ...any) *string {
	for _, value := range values {
		if text := stringPtrFromAnyApps(value); text != nil {
			return text
		}
	}
	return nil
}

func stringPtrFromAnyApps(value any) *string {
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		return cloneStringPtr(&typed)
	case *string:
		return cloneStringPtr(typed)
	default:
		text := fmt.Sprint(typed)
		return cloneStringPtr(&text)
	}
}

func stringFromAnyApps(value any) string {
	if text := stringPtrFromAnyApps(value); text != nil {
		return *text
	}
	return ""
}

func firstStringSliceFromAnyApps(values ...any) []string {
	for _, value := range values {
		if out := stringSliceFromAnyApps(value); out != nil {
			return out
		}
	}
	return nil
}

func stringSliceFromAnyApps(value any) []string {
	switch typed := value.(type) {
	case nil:
		return nil
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			out = append(out, stringFromAnyApps(item))
		}
		return out
	default:
		return nil
	}
}

func firstBoolPtrFromAnyApps(values ...any) *bool {
	for _, value := range values {
		if out := boolPtrFromAnyApps(value); out != nil {
			return out
		}
	}
	return nil
}

func boolPtrFromAnyApps(value any) *bool {
	switch typed := value.(type) {
	case nil:
		return nil
	case bool:
		return cloneBoolPtr(&typed)
	case *bool:
		return cloneBoolPtr(typed)
	default:
		return nil
	}
}

func boolFromAnyApps(value any) bool {
	if out := boolPtrFromAnyApps(value); out != nil {
		return *out
	}
	return false
}
