package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"codex_go/plugin"
)

// authenticatedMeasurementBundleFetcher resolves one installed GLOBAL plugin
// from the authenticated catalog without materializing any other plugin or
// publishing to the shared plugin catalog (Rust #46542).
type authenticatedMeasurementBundleFetcher struct {
	client interface {
		Do(*http.Request) (*http.Response, error)
	}
	baseURL   string
	token     string
	accountID string
}

func (f *authenticatedMeasurementBundleFetcher) FetchMeasurementBundle(ctx context.Context, target plugin.PluginMeasurementTarget) (*plugin.PluginMeasurementBundle, error) {
	if f == nil || f.client == nil || strings.TrimSpace(target.PluginName) == "" {
		return nil, nil
	}
	baseURL := strings.TrimRight(strings.TrimSpace(f.baseURL), "/")
	if baseURL == "" || strings.TrimSpace(f.token) == "" {
		return nil, nil
	}
	matches := make([]remoteInstalledPlugin, 0, 1)
	pageToken := ""
	for {
		endpoint, err := url.Parse(baseURL + "/ps/plugins/installed")
		if err != nil {
			return nil, fmt.Errorf("parse installed plugins endpoint: %w", err)
		}
		query := endpoint.Query()
		query.Set("scope", "GLOBAL")
		query.Set("includeDownloadUrls", "true")
		if pageToken != "" {
			query.Set("pageToken", pageToken)
		}
		endpoint.RawQuery = query.Encode()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("create installed plugins request: %w", err)
		}
		request.Header.Set("Authorization", "Bearer "+f.token)
		if strings.TrimSpace(f.accountID) != "" {
			request.Header.Set("ChatGPT-Account-ID", f.accountID)
		}
		response, err := f.client.Do(request)
		if err != nil {
			return nil, fmt.Errorf("fetch installed plugins: %w", err)
		}
		var page remoteInstalledPluginPage
		decodeErr := json.NewDecoder(response.Body).Decode(&page)
		_ = response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return nil, fmt.Errorf("fetch installed plugins: HTTP %d", response.StatusCode)
		}
		if decodeErr != nil {
			return nil, fmt.Errorf("decode installed plugins response: %w", decodeErr)
		}
		for _, candidate := range page.Plugins {
			if measurementCandidateMatches(candidate, target) {
				matches = append(matches, candidate)
			}
		}
		if page.Pagination.NextPageToken == nil || strings.TrimSpace(*page.Pagination.NextPageToken) == "" {
			break
		}
		pageToken = strings.TrimSpace(*page.Pagination.NextPageToken)
	}
	if len(matches) != 1 {
		return nil, nil
	}
	candidate := matches[0]
	version := strings.TrimSpace(pointerString(candidate.Release.Version))
	if version == "" || validateRemotePluginVersion(version) != nil {
		return nil, nil
	}
	bundleURL := strings.TrimSpace(pointerString(candidate.Release.BundleDownloadURL))
	if bundleURL == "" {
		return nil, nil
	}
	pluginID, err := plugin.NewPluginId(strings.TrimSpace(candidate.Name), remoteInstalledGlobalMarketplace)
	if err != nil {
		return nil, nil
	}
	remoteID := strings.TrimSpace(candidate.ID)
	name := strings.TrimSpace(candidate.Name)
	appManifest := append(json.RawMessage(nil), candidate.Release.AppManifest...)
	return &plugin.PluginMeasurementBundle{
		RemotePluginID: remoteID,
		PluginID:       pluginID,
		Version:        version,
		Materialize: func(ctx context.Context, home string) (plugin.TrustedPluginRoots, error) {
			bundle, err := downloadRemotePluginBundle(ctx, f.client, bundleURL)
			if err != nil {
				return plugin.TrustedPluginRoots{}, err
			}
			if _, err := installRemotePluginBundle(home, remoteInstalledGlobalMarketplace, name, version, remoteID, appManifest, bundle); err != nil {
				return plugin.TrustedPluginRoots{}, err
			}
			roots := plugin.NewTrustedPluginRoots(home, []string{pluginID.Key()})
			if roots.RootCount() == 0 {
				return plugin.TrustedPluginRoots{}, fmt.Errorf("plugin measurement bundle did not materialize verified roots")
			}
			return roots, nil
		},
	}, nil
}

func measurementCandidateMatches(candidate remoteInstalledPlugin, target plugin.PluginMeasurementTarget) bool {
	name := strings.TrimSpace(candidate.Name)
	if name == "" || strings.TrimSpace(candidate.ID) == "" {
		return false
	}
	if target.WindowsPathConvention {
		if !strings.EqualFold(name, strings.TrimSpace(target.PluginName)) {
			return false
		}
	} else if name != strings.TrimSpace(target.PluginName) {
		return false
	}
	if !candidate.Enabled || candidate.DisabledReason != nil {
		return false
	}
	if candidate.Availability != plugin.PluginAvailable {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(candidate.Scope), "GLOBAL")
}

func pointerString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
