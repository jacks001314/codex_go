package appserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"codex_go/auth"
	"codex_go/config"
	"codex_go/features"
	"codex_go/plugin"
)

const (
	remoteInstalledGlobalMarketplace          = "openai-curated-remote"
	remoteInstalledUserMarketplace            = "created-by-me-remote"
	remoteInstalledWorkspaceMarketplace       = "workspace-directory"
	remoteInstalledWorkspaceSharedMarketplace = "workspace-shared-with-me"
)

var remoteInstalledPluginSyncs = struct {
	sync.Mutex
	inFlight map[string]struct{}
}{inFlight: map[string]struct{}{}}

type remoteInstalledPluginPage struct {
	Plugins    []remoteInstalledPlugin `json:"plugins"`
	Pagination struct {
		NextPageToken *string `json:"next_page_token"`
	} `json:"pagination"`
}

type remoteInstalledPlugin struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	Scope              string   `json:"scope"`
	Discoverability    string   `json:"discoverability"`
	Enabled            bool     `json:"enabled"`
	DisabledSkillNames []string `json:"disabled_skill_names"`
	Release            struct {
		Version           *string         `json:"version"`
		DisplayName       string          `json:"display_name"`
		Description       string          `json:"description"`
		BundleDownloadURL *string         `json:"bundle_download_url"`
		AppManifest       json.RawMessage `json:"app_manifest"`
	} `json:"release"`
}

func (r *RuntimeRouter) startInstalledRemotePluginSync() {
	if r == nil || r.services.Plugins == nil || r.services.Config == nil {
		return
	}
	read, err := r.services.Config.Read(&config.ConfigReadParams{})
	if err != nil || read == nil {
		return
	}
	cfg := &config.Config{Values: read.Config}
	if !features.Enabled(cfg.FeatureSettings(), "plugins") || !features.Enabled(cfg.FeatureSettings(), "remote_plugin") {
		return
	}
	if r.services.WorkspaceCodexPluginsEnabled != nil && !*r.services.WorkspaceCodexPluginsEnabled {
		return
	}
	codexHome := strings.TrimSpace(r.services.Config.CodexHome())
	if codexHome == "" {
		return
	}
	snapshot := r.accountAuthSnapshot(codexHome)
	token := appDirectoryAuthToken(snapshot)
	if token == "" {
		return
	}
	accountID := auth.AccountIDFromAuthForRestrictions(snapshot)
	baseURL := strings.TrimRight(cfg.ChatGPTBaseURL(), "/")
	client := r.accountHTTPClient()
	service := r.services.Plugins
	if !markRemoteInstalledPluginSyncInFlight(codexHome) {
		return
	}
	go func() {
		defer clearRemoteInstalledPluginSyncInFlight(codexHome)
		detailsByMarketplace, ok := fetchInstalledRemotePluginDetails(context.Background(), client, baseURL, token, accountID, codexHome)
		if !ok {
			return
		}
		for _, marketplaceName := range []string{
			remoteInstalledGlobalMarketplace,
			remoteInstalledUserMarketplace,
			remoteInstalledWorkspaceMarketplace,
			remoteInstalledWorkspaceSharedMarketplace,
		} {
			service.ReplaceInstalledRemotePlugins(marketplaceName, detailsByMarketplace[marketplaceName])
		}
		r.effectivePluginsChanged()
	}()
}

func markRemoteInstalledPluginSyncInFlight(codexHome string) bool {
	key := filepath.Clean(codexHome)
	remoteInstalledPluginSyncs.Lock()
	defer remoteInstalledPluginSyncs.Unlock()
	if _, ok := remoteInstalledPluginSyncs.inFlight[key]; ok {
		return false
	}
	remoteInstalledPluginSyncs.inFlight[key] = struct{}{}
	return true
}

func clearRemoteInstalledPluginSyncInFlight(codexHome string) {
	key := filepath.Clean(codexHome)
	remoteInstalledPluginSyncs.Lock()
	delete(remoteInstalledPluginSyncs.inFlight, key)
	remoteInstalledPluginSyncs.Unlock()
}

func (r *RuntimeRouter) accountAuthSnapshot(codexHome string) *auth.AuthDotJSON {
	if r != nil && r.services.Account != nil {
		if snapshot := r.services.Account.AuthSnapshot(); snapshot != nil {
			return snapshot
		}
	}
	resolved, err := r.resolveAuthWithLoginRestrictions(codexHome)
	if err != nil || resolved == nil {
		return nil
	}
	return &resolved.Auth
}

func fetchInstalledRemotePluginDetails(ctx context.Context, client interface {
	Do(*http.Request) (*http.Response, error)
}, baseURL string, token string, accountID string, codexHome string) (map[string][]plugin.PluginDetail, bool) {
	installed := map[string]remoteInstalledPlugin{}
	for _, scope := range []string{"GLOBAL", "USER", "WORKSPACE"} {
		pageToken := ""
		for {
			endpoint, err := url.Parse(baseURL + "/ps/plugins/installed")
			if err != nil {
				return nil, false
			}
			query := endpoint.Query()
			query.Set("scope", scope)
			query.Set("includeDownloadUrls", "true")
			if pageToken != "" {
				query.Set("pageToken", pageToken)
			}
			endpoint.RawQuery = query.Encode()
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
			if err != nil {
				return nil, false
			}
			request.Header.Set("Authorization", "Bearer "+token)
			if strings.TrimSpace(accountID) != "" {
				request.Header.Set("ChatGPT-Account-ID", accountID)
			}
			response, err := client.Do(request)
			if err != nil {
				return nil, false
			}
			var page remoteInstalledPluginPage
			decodeErr := json.NewDecoder(response.Body).Decode(&page)
			_ = response.Body.Close()
			if response.StatusCode < 200 || response.StatusCode >= 300 || decodeErr != nil {
				return nil, false
			}
			for _, candidate := range page.Plugins {
				if strings.TrimSpace(candidate.ID) != "" && strings.TrimSpace(candidate.Name) != "" {
					marketplaceName := remoteInstalledMarketplaceForPlugin(scope, candidate)
					installed[marketplaceName+"\x00"+candidate.ID] = candidate
				}
			}
			if page.Pagination.NextPageToken == nil || strings.TrimSpace(*page.Pagination.NextPageToken) == "" {
				break
			}
			pageToken = strings.TrimSpace(*page.Pagination.NextPageToken)
		}
	}
	keys := make([]string, 0, len(installed))
	for key := range installed {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	detailsByMarketplace := map[string][]plugin.PluginDetail{}
	installedNamesByMarketplace := map[string]map[string]struct{}{}
	for _, key := range keys {
		candidate := installed[key]
		marketplaceName := strings.SplitN(key, "\x00", 2)[0]
		name := strings.TrimSpace(candidate.Name)
		if installedNamesByMarketplace[marketplaceName] == nil {
			installedNamesByMarketplace[marketplaceName] = map[string]struct{}{}
		}
		installedNamesByMarketplace[marketplaceName][name] = struct{}{}
		root, err := ensureRemoteInstalledPluginCache(ctx, client, codexHome, marketplaceName, candidate)
		if err != nil || root == "" || !candidate.Enabled {
			continue
		}
		detailsByMarketplace[marketplaceName] = append(detailsByMarketplace[marketplaceName], plugin.PluginDetail{
			MarketplaceName: marketplaceName,
			MarketplaceRoot: root,
			Summary: plugin.PluginSummary{
				Name:            name,
				DisplayName:     firstNonEmpty(strings.TrimSpace(candidate.Release.DisplayName), name),
				Description:     strings.TrimSpace(candidate.Release.Description),
				MarketplaceName: marketplaceName,
				RemotePluginID:  strings.TrimSpace(candidate.ID),
				HasSkills:       remotePluginCacheHasSkills(root),
				Installed:       true,
				Enabled:         true,
			},
		})
	}
	if err := removeStaleRemoteInstalledPluginCaches(codexHome, installedNamesByMarketplace); err != nil {
		return nil, false
	}
	return detailsByMarketplace, true
}

func remoteInstalledMarketplaceForPlugin(requestedScope string, candidate remoteInstalledPlugin) string {
	scope := strings.ToUpper(strings.TrimSpace(candidate.Scope))
	if scope == "" {
		scope = requestedScope
	}
	switch scope {
	case "USER":
		return remoteInstalledUserMarketplace
	case "WORKSPACE":
		if discoverability := strings.ToUpper(strings.TrimSpace(candidate.Discoverability)); discoverability == "PRIVATE" || discoverability == "UNLISTED" {
			return remoteInstalledWorkspaceSharedMarketplace
		}
		return remoteInstalledWorkspaceMarketplace
	default:
		return remoteInstalledGlobalMarketplace
	}
}

func remotePluginCacheHasSkills(root string) bool {
	entries, err := os.ReadDir(filepath.Join(root, "skills"))
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() {
			if info, err := os.Stat(filepath.Join(root, "skills", entry.Name(), SkillFilename)); err == nil && !info.IsDir() {
				return true
			}
		}
	}
	if info, err := os.Stat(filepath.Join(root, "skills", SkillFilename)); err == nil && !info.IsDir() {
		return true
	}
	return false
}
