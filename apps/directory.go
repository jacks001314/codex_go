package apps

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const DefaultChatGPTDirectoryBaseURL = "https://chatgpt.com/backend-api"

type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type ChatGPTDirectoryProviderOptions struct {
	BaseURL            string
	Headers            http.Header
	HTTPClient         HTTPDoer
	IsWorkspaceAccount bool
}

type ChatGPTDirectoryProvider struct {
	baseURL            string
	headers            http.Header
	httpClient         HTTPDoer
	isWorkspaceAccount bool
}

func NewChatGPTDirectoryProvider(options *ChatGPTDirectoryProviderOptions) *ChatGPTDirectoryProvider {
	baseURL := DefaultChatGPTDirectoryBaseURL
	headers := http.Header{}
	client := HTTPDoer(&http.Client{Timeout: 60 * time.Second})
	isWorkspaceAccount := false
	if options != nil {
		if strings.TrimSpace(options.BaseURL) != "" {
			baseURL = options.BaseURL
		}
		headers = cloneHTTPHeaderApps(options.Headers)
		if options.HTTPClient != nil {
			client = options.HTTPClient
		}
		isWorkspaceAccount = options.IsWorkspaceAccount
	}
	return &ChatGPTDirectoryProvider{
		baseURL:            normalizeChatGPTDirectoryBaseURL(baseURL),
		headers:            headers,
		httpClient:         client,
		isWorkspaceAccount: isWorkspaceAccount,
	}
}

func (p *ChatGPTDirectoryProvider) CacheKey() string {
	if p == nil {
		return ""
	}
	return fmt.Sprintf("%s|workspace=%t", p.baseURL, p.isWorkspaceAccount)
}

func (p *ChatGPTDirectoryProvider) ListDirectoryApps(params *AppDirectoryListParams) (*AppDirectoryListResponse, error) {
	if p == nil {
		return &AppDirectoryListResponse{AllConnectorsLoaded: boolPtrApps(true)}, nil
	}
	apps, err := p.listDirectoryApps("/connectors/directory/list")
	if err != nil {
		return nil, err
	}
	if p.isWorkspaceAccount {
		workspaceApps, err := p.listDirectoryApps("/connectors/directory/list_workspace")
		if err == nil {
			apps = append(apps, workspaceApps...)
		}
	}
	merged := directoryAppsToEntries(apps)
	allLoaded := true
	return &AppDirectoryListResponse{Apps: merged, AllConnectorsLoaded: &allLoaded}, nil
}

func (p *ChatGPTDirectoryProvider) listDirectoryApps(path string) ([]directoryApp, error) {
	var out []directoryApp
	nextToken := ""
	for {
		query := url.Values{}
		query.Set("external_logos", "true")
		if strings.TrimSpace(nextToken) != "" {
			query.Set("token", strings.TrimSpace(nextToken))
		}
		var page directoryListResponse
		if err := p.doJSON(http.MethodGet, path+"?"+query.Encode(), &page); err != nil {
			return nil, err
		}
		for i := range page.Apps {
			if !page.Apps[i].hidden() {
				out = append(out, page.Apps[i])
			}
		}
		nextToken = strings.TrimSpace(firstNonEmptyAppString(page.NextToken, page.NextTokenLegacy))
		if nextToken == "" {
			break
		}
	}
	return out, nil
}

func (p *ChatGPTDirectoryProvider) doJSON(method string, path string, out any) error {
	target, err := url.Parse(strings.TrimRight(p.baseURL, "/") + path)
	if err != nil {
		return err
	}
	request, err := http.NewRequest(method, target.String(), nil)
	if err != nil {
		return err
	}
	for key, values := range p.headers {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	response, err := p.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 32<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("%s %s failed: %s; body=%s", method, target.String(), response.Status, strings.TrimSpace(string(body)))
	}
	if out == nil || len(strings.TrimSpace(string(body))) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode %s response: %w; body=%s", target.String(), err, string(body))
	}
	return nil
}

type directoryListResponse struct {
	Apps            []directoryApp `json:"apps"`
	NextToken       string         `json:"nextToken"`
	NextTokenLegacy string         `json:"next_token"`
}

type directoryApp struct {
	ID                  string            `json:"id"`
	Name                string            `json:"name"`
	Description         *string           `json:"description"`
	AppMetadata         *AppMetadata      `json:"appMetadata"`
	Branding            *AppBranding      `json:"branding"`
	Labels              map[string]string `json:"labels"`
	LogoURL             *string           `json:"logoUrl"`
	LogoURLDark         *string           `json:"logoUrlDark"`
	IconAssets          map[string]string `json:"iconAssets"`
	IconDarkAssets      map[string]string `json:"iconDarkAssets"`
	DistributionChannel *string           `json:"distributionChannel"`
	Visibility          *string           `json:"visibility"`
}

func (a *directoryApp) hidden() bool {
	return a != nil && a.Visibility != nil && *a.Visibility == "HIDDEN"
}

func directoryAppsToEntries(apps []directoryApp) []AppEntry {
	merged := make(map[string]directoryApp, len(apps))
	for i := range apps {
		app := apps[i]
		app.ID = strings.TrimSpace(app.ID)
		if app.ID == "" {
			continue
		}
		if existing, ok := merged[app.ID]; ok {
			merged[app.ID] = mergeDirectoryApp(existing, app)
			continue
		}
		merged[app.ID] = app
	}
	out := make([]AppEntry, 0, len(merged))
	for _, app := range merged {
		entry := directoryAppToEntry(&app)
		if entry.ID != "" {
			out = append(out, entry)
		}
	}
	sort.SliceStable(out, func(i int, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func mergeDirectoryApp(existing directoryApp, incoming directoryApp) directoryApp {
	if strings.TrimSpace(existing.Name) == "" && strings.TrimSpace(incoming.Name) != "" {
		existing.Name = incoming.Name
	}
	if incoming.Description != nil && strings.TrimSpace(*incoming.Description) != "" {
		existing.Description = cloneStringPtr(incoming.Description)
	}
	if existing.LogoURL == nil && incoming.LogoURL != nil {
		existing.LogoURL = cloneStringPtr(incoming.LogoURL)
	}
	if existing.LogoURLDark == nil && incoming.LogoURLDark != nil {
		existing.LogoURLDark = cloneStringPtr(incoming.LogoURLDark)
	}
	if len(existing.IconAssets) == 0 && len(incoming.IconAssets) > 0 {
		existing.IconAssets = cloneStringMap(incoming.IconAssets)
	}
	if len(existing.IconDarkAssets) == 0 && len(incoming.IconDarkAssets) > 0 {
		existing.IconDarkAssets = cloneStringMap(incoming.IconDarkAssets)
	}
	if existing.DistributionChannel == nil && incoming.DistributionChannel != nil {
		existing.DistributionChannel = cloneStringPtr(incoming.DistributionChannel)
	}
	if existing.Branding == nil && incoming.Branding != nil {
		existing.Branding = cloneAppBranding(incoming.Branding)
	}
	if existing.AppMetadata == nil && incoming.AppMetadata != nil {
		existing.AppMetadata = cloneAppMetadata(incoming.AppMetadata)
	}
	if existing.Labels == nil && incoming.Labels != nil {
		existing.Labels = cloneStringMap(incoming.Labels)
	}
	return existing
}

func directoryAppToEntry(app *directoryApp) AppEntry {
	if app == nil {
		return AppEntry{}
	}
	id := strings.TrimSpace(app.ID)
	name := strings.TrimSpace(app.Name)
	if name == "" {
		name = id
	}
	description := normalizeAppStringPtr(app.Description)
	installURL := ConnectorInstallURL(name, id)
	return AppEntry{
		ID:                  id,
		Name:                name,
		Description:         description,
		InstallURL:          &installURL,
		LogoURL:             cloneStringPtr(app.LogoURL),
		LogoURLDark:         cloneStringPtr(app.LogoURLDark),
		IconAssets:          cloneStringMap(app.IconAssets),
		IconDarkAssets:      cloneStringMap(app.IconDarkAssets),
		DistributionChannel: cloneStringPtr(app.DistributionChannel),
		Branding:            cloneAppBranding(app.Branding),
		AppMetadata:         cloneAppMetadata(app.AppMetadata),
		LabelMap:            cloneStringMap(app.Labels),
		IsAccessible:        false,
		IsEnabled:           true,
		Enabled:             true,
		PluginDisplayNames:  []string{},
	}
}

func normalizeChatGPTDirectoryBaseURL(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return DefaultChatGPTDirectoryBaseURL
	}
	value = strings.TrimRight(value, "/")
	if (strings.HasPrefix(value, "https://chatgpt.com") || strings.HasPrefix(value, "https://chat.openai.com")) && !strings.Contains(value, "/backend-api") {
		value += "/backend-api"
	}
	return value
}

func normalizeAppStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func cloneHTTPHeaderApps(headers http.Header) http.Header {
	out := http.Header{}
	for key, values := range headers {
		out[key] = append([]string(nil), values...)
	}
	return out
}

func firstNonEmptyAppString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
