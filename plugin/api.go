package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrInvalidPluginRequest = errors.New("invalid plugin request")

type Marketplace struct {
	Name         string    `json:"name"`
	SourceURL    string    `json:"sourceUrl,omitempty"`
	SourceType   string    `json:"sourceType,omitempty"`
	RefName      *string   `json:"refName,omitempty"`
	LastRevision *string   `json:"lastRevision,omitempty"`
	SparsePaths  []string  `json:"sparsePaths,omitempty"`
	RootPath     string    `json:"rootPath"`
	AddedAt      time.Time `json:"addedAt"`
}

type MarketplaceInterface struct {
	DisplayName *string `json:"displayName"`
}

type PluginAvailability string

const (
	PluginAvailable       PluginAvailability = "AVAILABLE"
	PluginDisabledByAdmin PluginAvailability = "DISABLED_BY_ADMIN"
)

type PluginDisabledReason string

const (
	PluginDisabledByAdminReason  PluginDisabledReason = "disabled_by_admin"
	PluginPlanNotEligibleReason  PluginDisabledReason = "plan_not_eligible"
	PluginRequiredAppUnavailable PluginDisabledReason = "required_app_unavailable"
	PluginDisabledReasonUnknown  PluginDisabledReason = "unknown"
)

func (r *PluginDisabledReason) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	switch PluginDisabledReason(value) {
	case PluginDisabledByAdminReason, PluginPlanNotEligibleReason, PluginRequiredAppUnavailable, PluginDisabledReasonUnknown:
		*r = PluginDisabledReason(value)
	default:
		*r = PluginDisabledReasonUnknown
	}
	return nil
}

type PluginInstallPolicy string

const (
	InstallAllowed            PluginInstallPolicy = "AVAILABLE"
	InstallBlocked            PluginInstallPolicy = "NOT_AVAILABLE"
	InstallInstalledByDefault PluginInstallPolicy = "INSTALLED_BY_DEFAULT"
)

type PluginInstallPolicySource string

const (
	PluginInstallPolicySourceWorkspaceSetting     PluginInstallPolicySource = "WORKSPACE_SETTING"
	PluginInstallPolicySourceImplicitCanonicalApp PluginInstallPolicySource = "IMPLICIT_CANONICAL_APP"
)

type PluginAuthPolicy string

const (
	AuthNone      PluginAuthPolicy = "ON_USE"
	AuthOptional  PluginAuthPolicy = "ON_USE"
	AuthRequired  PluginAuthPolicy = "ON_INSTALL"
	AuthOnUse     PluginAuthPolicy = "ON_USE"
	AuthOnInstall PluginAuthPolicy = "ON_INSTALL"
)

type PluginInterface struct {
	DisplayName       *string  `json:"displayName,omitempty"`
	ShortDescription  *string  `json:"shortDescription,omitempty"`
	LongDescription   *string  `json:"longDescription,omitempty"`
	DeveloperName     *string  `json:"developerName,omitempty"`
	Category          string   `json:"category,omitempty"`
	Capabilities      []string `json:"capabilities"`
	WebsiteURL        *string  `json:"websiteUrl,omitempty"`
	PrivacyPolicyURL  *string  `json:"privacyPolicyUrl,omitempty"`
	TermsOfServiceURL *string  `json:"termsOfServiceUrl,omitempty"`
	DefaultPrompt     []string `json:"defaultPrompt,omitempty"`
	BrandColor        *string  `json:"brandColor,omitempty"`
	ComposerIcon      *string  `json:"composerIcon,omitempty"`
	ComposerIconURL   *string  `json:"composerIconUrl,omitempty"`
	Logo              *string  `json:"logo,omitempty"`
	LogoDark          *string  `json:"logoDark,omitempty"`
	LogoURL           *string  `json:"logoUrl,omitempty"`
	LogoURLDark       *string  `json:"logoUrlDark,omitempty"`
	Screenshots       []string `json:"screenshots"`
	ScreenshotURLs    []string `json:"screenshotUrls"`
	Icon              string   `json:"icon,omitempty"`
}

func (i *PluginInterface) UnmarshalJSON(data []byte) error {
	var raw struct {
		DisplayName            *string         `json:"displayName"`
		DisplayNameSnake       *string         `json:"display_name"`
		ShortDescription       *string         `json:"shortDescription"`
		ShortDescriptionSnake  *string         `json:"short_description"`
		LongDescription        *string         `json:"longDescription"`
		LongDescriptionSnake   *string         `json:"long_description"`
		DeveloperName          *string         `json:"developerName"`
		DeveloperNameSnake     *string         `json:"developer_name"`
		Category               string          `json:"category"`
		Capabilities           []string        `json:"capabilities"`
		WebsiteURL             *string         `json:"websiteUrl"`
		WebsiteURLSnake        *string         `json:"website_url"`
		PrivacyPolicyURL       *string         `json:"privacyPolicyUrl"`
		PrivacyPolicyURLSnake  *string         `json:"privacy_policy_url"`
		TermsOfServiceURL      *string         `json:"termsOfServiceUrl"`
		TermsOfServiceURLSnake *string         `json:"terms_of_service_url"`
		DefaultPrompt          json.RawMessage `json:"defaultPrompt"`
		DefaultPromptSnake     json.RawMessage `json:"default_prompt"`
		DefaultPromptsSnake    json.RawMessage `json:"default_prompts"`
		BrandColor             *string         `json:"brandColor"`
		BrandColorSnake        *string         `json:"brand_color"`
		ComposerIcon           *string         `json:"composerIcon"`
		ComposerIconSnake      *string         `json:"composer_icon"`
		ComposerIconURL        *string         `json:"composerIconUrl"`
		ComposerIconURLSnake   *string         `json:"composer_icon_url"`
		Logo                   *string         `json:"logo"`
		LogoDark               *string         `json:"logoDark"`
		LogoDarkSnake          *string         `json:"logo_dark"`
		LogoURL                *string         `json:"logoUrl"`
		LogoURLSnake           *string         `json:"logo_url"`
		LogoURLDark            *string         `json:"logoUrlDark"`
		LogoURLDarkSnake       *string         `json:"logo_url_dark"`
		Screenshots            []string        `json:"screenshots"`
		ScreenshotURLs         []string        `json:"screenshotUrls"`
		ScreenshotURLsSnake    []string        `json:"screenshot_urls"`
		Icon                   string          `json:"icon"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	i.DisplayName = firstPluginInterfaceStringPtr(raw.DisplayName, raw.DisplayNameSnake)
	i.ShortDescription = firstPluginInterfaceStringPtr(raw.ShortDescription, raw.ShortDescriptionSnake)
	i.LongDescription = firstPluginInterfaceStringPtr(raw.LongDescription, raw.LongDescriptionSnake)
	i.DeveloperName = firstPluginInterfaceStringPtr(raw.DeveloperName, raw.DeveloperNameSnake)
	i.Category = strings.TrimSpace(raw.Category)
	i.Capabilities = trimPluginInterfaceStrings(raw.Capabilities)
	i.WebsiteURL = firstPluginInterfaceStringPtr(raw.WebsiteURL, raw.WebsiteURLSnake)
	i.PrivacyPolicyURL = firstPluginInterfaceStringPtr(raw.PrivacyPolicyURL, raw.PrivacyPolicyURLSnake)
	i.TermsOfServiceURL = firstPluginInterfaceStringPtr(raw.TermsOfServiceURL, raw.TermsOfServiceURLSnake)
	if prompts, ok := pluginInterfacePromptValues(raw.DefaultPromptsSnake); ok {
		i.DefaultPrompt = prompts
	} else if prompts, ok := pluginInterfacePromptValues(raw.DefaultPrompt); ok {
		i.DefaultPrompt = prompts
	} else if prompts, ok := pluginInterfacePromptValues(raw.DefaultPromptSnake); ok {
		i.DefaultPrompt = prompts
	} else {
		i.DefaultPrompt = nil
	}
	i.BrandColor = firstPluginInterfaceStringPtr(raw.BrandColor, raw.BrandColorSnake)
	i.ComposerIcon = firstPluginInterfaceStringPtr(raw.ComposerIcon, raw.ComposerIconSnake)
	i.ComposerIconURL = firstPluginInterfaceStringPtr(raw.ComposerIconURL, raw.ComposerIconURLSnake)
	i.Logo = firstPluginInterfaceStringPtr(raw.Logo)
	i.LogoDark = firstPluginInterfaceStringPtr(raw.LogoDark, raw.LogoDarkSnake)
	i.LogoURL = firstPluginInterfaceStringPtr(raw.LogoURL, raw.LogoURLSnake)
	i.LogoURLDark = firstPluginInterfaceStringPtr(raw.LogoURLDark, raw.LogoURLDarkSnake)
	i.Screenshots = trimPluginInterfaceStrings(raw.Screenshots)
	i.ScreenshotURLs = trimPluginInterfaceStrings(firstPluginInterfaceStringSlice(raw.ScreenshotURLs, raw.ScreenshotURLsSnake))
	i.Icon = strings.TrimSpace(raw.Icon)
	return nil
}

func (i *PluginInterface) MarshalJSON() ([]byte, error) {
	capabilities := append([]string(nil), i.Capabilities...)
	if capabilities == nil {
		capabilities = []string{}
	}
	defaultPrompt := append([]string(nil), i.DefaultPrompt...)
	screenshots := append([]string(nil), i.Screenshots...)
	if screenshots == nil {
		screenshots = []string{}
	}
	screenshotURLs := append([]string(nil), i.ScreenshotURLs...)
	if screenshotURLs == nil {
		screenshotURLs = []string{}
	}
	var category *string
	if strings.TrimSpace(i.Category) != "" {
		value := i.Category
		category = &value
	}
	return json.Marshal(struct {
		DisplayName       *string  `json:"displayName"`
		ShortDescription  *string  `json:"shortDescription"`
		LongDescription   *string  `json:"longDescription"`
		DeveloperName     *string  `json:"developerName"`
		Category          *string  `json:"category"`
		Capabilities      []string `json:"capabilities"`
		WebsiteURL        *string  `json:"websiteUrl"`
		PrivacyPolicyURL  *string  `json:"privacyPolicyUrl"`
		TermsOfServiceURL *string  `json:"termsOfServiceUrl"`
		DefaultPrompt     []string `json:"defaultPrompt"`
		BrandColor        *string  `json:"brandColor"`
		ComposerIcon      *string  `json:"composerIcon"`
		ComposerIconURL   *string  `json:"composerIconUrl"`
		Logo              *string  `json:"logo"`
		LogoDark          *string  `json:"logoDark"`
		LogoURL           *string  `json:"logoUrl"`
		LogoURLDark       *string  `json:"logoUrlDark"`
		Screenshots       []string `json:"screenshots"`
		ScreenshotURLs    []string `json:"screenshotUrls"`
	}{
		DisplayName:       i.DisplayName,
		ShortDescription:  i.ShortDescription,
		LongDescription:   i.LongDescription,
		DeveloperName:     i.DeveloperName,
		Category:          category,
		Capabilities:      capabilities,
		WebsiteURL:        i.WebsiteURL,
		PrivacyPolicyURL:  i.PrivacyPolicyURL,
		TermsOfServiceURL: i.TermsOfServiceURL,
		DefaultPrompt:     defaultPrompt,
		BrandColor:        i.BrandColor,
		ComposerIcon:      i.ComposerIcon,
		ComposerIconURL:   i.ComposerIconURL,
		Logo:              i.Logo,
		LogoDark:          i.LogoDark,
		LogoURL:           i.LogoURL,
		LogoURLDark:       i.LogoURLDark,
		Screenshots:       screenshots,
		ScreenshotURLs:    screenshotURLs,
	})
}

func firstPluginInterfaceStringPtr(values ...*string) *string {
	for _, value := range values {
		if value == nil {
			continue
		}
		trimmed := strings.TrimSpace(*value)
		if trimmed == "" {
			continue
		}
		return &trimmed
	}
	return nil
}

func firstPluginInterfaceStringSlice(values ...[]string) []string {
	for _, value := range values {
		if len(value) > 0 {
			return value
		}
	}
	return nil
}

func trimPluginInterfaceStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func pluginInterfacePromptValues(raw json.RawMessage) ([]string, bool) {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return nil, false
	}
	if text == "null" {
		return nil, true
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		return trimPluginInterfaceStrings(list), true
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return trimPluginInterfaceStrings([]string{single}), true
	}
	return nil, false
}

type PluginSource struct {
	Type     string  `json:"type"`
	Path     string  `json:"path,omitempty"`
	URL      string  `json:"url,omitempty"`
	RefName  *string `json:"refName,omitempty"`
	SHA      *string `json:"sha,omitempty"`
	Package  string  `json:"package,omitempty"`
	Version  *string `json:"version,omitempty"`
	Registry *string `json:"registry,omitempty"`
}

func (s *PluginSource) MarshalJSON() ([]byte, error) {
	if s == nil {
		return []byte("null"), nil
	}
	switch strings.TrimSpace(s.Type) {
	case "local":
		return json.Marshal(struct {
			Type string `json:"type"`
			Path string `json:"path"`
		}{
			Type: "local",
			Path: s.Path,
		})
	case "git":
		return json.Marshal(struct {
			Type    string  `json:"type"`
			URL     string  `json:"url"`
			Path    *string `json:"path"`
			RefName *string `json:"refName"`
			SHA     *string `json:"sha"`
		}{
			Type:    "git",
			URL:     s.URL,
			Path:    stringPtrIfNotEmpty(s.Path),
			RefName: cloneStringPtr(s.RefName),
			SHA:     cloneStringPtr(s.SHA),
		})
	case "npm":
		return json.Marshal(struct {
			Type     string  `json:"type"`
			Package  string  `json:"package"`
			Version  *string `json:"version"`
			Registry *string `json:"registry"`
		}{
			Type:     "npm",
			Package:  s.Package,
			Version:  cloneStringPtr(s.Version),
			Registry: cloneStringPtr(s.Registry),
		})
	case "remote":
		return json.Marshal(struct {
			Type string `json:"type"`
		}{Type: "remote"})
	default:
		if strings.TrimSpace(s.Path) != "" {
			return json.Marshal(struct {
				Type string `json:"type"`
				Path string `json:"path"`
			}{
				Type: "local",
				Path: s.Path,
			})
		}
		return json.Marshal(struct {
			Type string `json:"type"`
		}{Type: "remote"})
	}
}

type PluginSummary struct {
	ID                               string                     `json:"id"`
	Name                             string                     `json:"name"`
	DisplayName                      string                     `json:"displayName"`
	Description                      string                     `json:"description,omitempty"`
	MarketplaceName                  string                     `json:"marketplaceName,omitempty"`
	RemotePluginID                   string                     `json:"remotePluginId,omitempty"`
	Version                          *string                    `json:"version"`
	LocalVersion                     *string                    `json:"localVersion"`
	ShareContext                     *PluginShareContext        `json:"shareContext"`
	Availability                     PluginAvailability         `json:"availability"`
	DisabledReason                   *PluginDisabledReason      `json:"disabledReason"`
	EligiblePlanTypes                *[]string                  `json:"eligiblePlanTypes"`
	InstallPolicy                    PluginInstallPolicy        `json:"installPolicy"`
	InstallPolicySource              *PluginInstallPolicySource `json:"installPolicySource"`
	MustShowInstallationInterstitial *bool                      `json:"mustShowInstallationInterstitial"`
	AuthPolicy                       PluginAuthPolicy           `json:"authPolicy"`
	Interface                        *PluginInterface           `json:"interface,omitempty"`
	Source                           PluginSource               `json:"source"`
	HasSkills                        bool                       `json:"hasSkills"`
	MCPServers                       []string                   `json:"mcpServers,omitempty"`
	AppConnectors                    []string                   `json:"appConnectors,omitempty"`
	Installed                        bool                       `json:"installed"`
	InstalledAt                      *int64                     `json:"installedAt"`
	Enabled                          bool                       `json:"enabled"`
	InstallSuggestion                bool                       `json:"installSuggestion,omitempty"`
	PluginDisplayNameTag             string                     `json:"pluginDisplayNameTag,omitempty"`
	Keywords                         []string                   `json:"keywords"`
}

func (s PluginSummary) MarshalJSON() ([]byte, error) {
	keywords := append([]string(nil), s.Keywords...)
	if keywords == nil {
		keywords = []string{}
	}
	return json.Marshal(struct {
		ID                               string                     `json:"id"`
		Name                             string                     `json:"name"`
		RemotePluginID                   *string                    `json:"remotePluginId"`
		Version                          *string                    `json:"version"`
		LocalVersion                     *string                    `json:"localVersion"`
		ShareContext                     *PluginShareContext        `json:"shareContext"`
		Availability                     PluginAvailability         `json:"availability"`
		DisabledReason                   *PluginDisabledReason      `json:"disabledReason"`
		EligiblePlanTypes                *[]string                  `json:"eligiblePlanTypes"`
		InstallPolicy                    PluginInstallPolicy        `json:"installPolicy"`
		InstallPolicySource              *PluginInstallPolicySource `json:"installPolicySource"`
		MustShowInstallationInterstitial *bool                      `json:"mustShowInstallationInterstitial"`
		AuthPolicy                       PluginAuthPolicy           `json:"authPolicy"`
		Interface                        *PluginInterface           `json:"interface"`
		Source                           PluginSource               `json:"source"`
		Installed                        bool                       `json:"installed"`
		InstalledAt                      *int64                     `json:"installedAt"`
		Enabled                          bool                       `json:"enabled"`
		Keywords                         []string                   `json:"keywords"`
	}{
		ID:                               s.ID,
		Name:                             s.Name,
		RemotePluginID:                   stringPtrIfNotEmpty(s.RemotePluginID),
		Version:                          cloneStringPtr(s.Version),
		LocalVersion:                     cloneStringPtr(s.LocalVersion),
		ShareContext:                     cloneSharePtr(s.ShareContext),
		Availability:                     s.Availability,
		DisabledReason:                   clonePluginDisabledReasonPtr(s.DisabledReason),
		EligiblePlanTypes:                cloneStringSlicePtr(s.EligiblePlanTypes),
		InstallPolicy:                    s.InstallPolicy,
		InstallPolicySource:              clonePluginInstallPolicySourcePtr(s.InstallPolicySource),
		MustShowInstallationInterstitial: cloneBoolPtr(s.MustShowInstallationInterstitial),
		AuthPolicy:                       s.AuthPolicy,
		Interface:                        clonePluginInterfacePtr(s.Interface),
		Source:                           clonePluginSource(s.Source),
		Installed:                        s.Installed,
		InstalledAt:                      cloneInt64Ptr(s.InstalledAt),
		Enabled:                          s.Enabled,
		Keywords:                         keywords,
	})
}

type PluginDetail struct {
	MarketplaceName string               `json:"marketplaceName"`
	MarketplacePath *string              `json:"marketplacePath"`
	Summary         PluginSummary        `json:"summary"`
	ShareURL        *string              `json:"shareUrl"`
	Description     *string              `json:"description"`
	Skills          []PluginSkill        `json:"skills"`
	Hooks           []PluginHookSummary  `json:"hooks"`
	Apps            []AppSummary         `json:"apps"`
	AppTemplates    []AppTemplateSummary `json:"appTemplates"`
	MCPServers      []string             `json:"mcpServers"`
	ManifestPath    string               `json:"manifestPath,omitempty"`
	MarketplaceRoot string               `json:"marketplaceRoot,omitempty"`
}

func (d *PluginDetail) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		MarketplaceName string               `json:"marketplaceName"`
		MarketplacePath *string              `json:"marketplacePath"`
		Summary         PluginSummary        `json:"summary"`
		ShareURL        *string              `json:"shareUrl"`
		Description     *string              `json:"description"`
		Skills          []PluginSkill        `json:"skills"`
		Hooks           []PluginHookSummary  `json:"hooks"`
		Apps            []AppSummary         `json:"apps"`
		AppTemplates    []AppTemplateSummary `json:"appTemplates"`
		MCPServers      []string             `json:"mcpServers"`
	}{
		MarketplaceName: d.MarketplaceName,
		MarketplacePath: d.MarketplacePath,
		Summary:         d.Summary,
		ShareURL:        d.ShareURL,
		Description:     d.Description,
		Skills:          pluginSkillsForJSON(d.Skills),
		Hooks:           pluginHooksForJSON(d.Hooks),
		Apps:            appSummariesForJSON(d.Apps),
		AppTemplates:    appTemplateSummariesForJSON(d.AppTemplates),
		MCPServers:      stringSliceForJSON(d.MCPServers),
	})
}

type PluginSkill struct {
	Name             string          `json:"name"`
	Description      string          `json:"description"`
	ShortDescription *string         `json:"shortDescription"`
	Interface        *SkillInterface `json:"interface,omitempty"`
	Path             *string         `json:"path"`
	Enabled          bool            `json:"enabled"`
}

func (s *PluginSkill) MarshalJSON() ([]byte, error) {
	if s == nil {
		return []byte("null"), nil
	}
	return json.Marshal(struct {
		Name             string          `json:"name"`
		Description      string          `json:"description"`
		ShortDescription *string         `json:"shortDescription"`
		Interface        *SkillInterface `json:"interface"`
		Path             *string         `json:"path"`
		Enabled          bool            `json:"enabled"`
	}{
		Name:             s.Name,
		Description:      s.Description,
		ShortDescription: cloneStringPtr(s.ShortDescription),
		Interface:        cloneSkillInterfacePtr(s.Interface),
		Path:             cloneStringPtr(s.Path),
		Enabled:          s.Enabled,
	})
}

type EnabledSkillRoot struct {
	PluginID        string
	RemotePluginID  string
	PluginNamespace string
	Root            string
}

type SkillInterface struct {
	DisplayName      string  `json:"displayName,omitempty"`
	ShortDescription string  `json:"shortDescription,omitempty"`
	IconSmall        *string `json:"iconSmall,omitempty"`
	IconLarge        *string `json:"iconLarge,omitempty"`
	BrandColor       *string `json:"brandColor,omitempty"`
	DefaultPrompt    *string `json:"defaultPrompt,omitempty"`
}

type PluginHookSummary struct {
	Key       string `json:"key"`
	EventName string `json:"eventName,omitempty"`
	Enabled   bool   `json:"enabled"`
}

func (h *PluginHookSummary) MarshalJSON() ([]byte, error) {
	if h == nil {
		return []byte("null"), nil
	}
	return json.Marshal(struct {
		Key       string `json:"key"`
		EventName string `json:"eventName"`
	}{
		Key:       h.Key,
		EventName: h.EventName,
	})
}

type HookSource struct {
	PluginID           string
	PluginRoot         string
	PluginDataRoot     string
	SourcePath         string
	SourceRelativePath string
}

type AppSummary struct {
	ID          string  `json:"id"`
	Name        string  `json:"name,omitempty"`
	DisplayName string  `json:"displayName"`
	Description *string `json:"description,omitempty"`
	InstallURL  *string `json:"installUrl,omitempty"`
	Category    *string `json:"category,omitempty"`
}

func (a *AppSummary) UnmarshalJSON(data []byte) error {
	var raw struct {
		ID               string  `json:"id"`
		Name             string  `json:"name"`
		DisplayName      string  `json:"displayName"`
		DisplayNameSnake string  `json:"display_name"`
		Description      *string `json:"description"`
		InstallURL       *string `json:"installUrl"`
		InstallURLSnake  *string `json:"install_url"`
		Category         *string `json:"category"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	a.ID = strings.TrimSpace(raw.ID)
	a.Name = strings.TrimSpace(raw.Name)
	a.DisplayName = strings.TrimSpace(firstNonEmpty(raw.DisplayName, raw.DisplayNameSnake, raw.Name, raw.ID))
	a.Description = cloneTrimmedStringPtr(raw.Description)
	a.InstallURL = cloneTrimmedStringPtr(firstStringPtr(raw.InstallURL, raw.InstallURLSnake))
	a.Category = cloneTrimmedStringPtr(raw.Category)
	return nil
}

func (a *AppSummary) MarshalJSON() ([]byte, error) {
	name := strings.TrimSpace(a.Name)
	if name == "" {
		name = a.DisplayName
	}
	return json.Marshal(struct {
		ID          string  `json:"id"`
		Name        string  `json:"name"`
		Description *string `json:"description"`
		InstallURL  *string `json:"installUrl"`
		Category    *string `json:"category"`
	}{
		ID:          a.ID,
		Name:        name,
		Description: a.Description,
		InstallURL:  a.InstallURL,
		Category:    a.Category,
	})
}

type AppTemplateSummary struct {
	ID                   string   `json:"id,omitempty"`
	TemplateID           string   `json:"templateId,omitempty"`
	Name                 string   `json:"name,omitempty"`
	DisplayName          string   `json:"displayName,omitempty"`
	Description          *string  `json:"description,omitempty"`
	Category             *string  `json:"category,omitempty"`
	CanonicalConnectorID *string  `json:"canonicalConnectorId,omitempty"`
	LogoURL              *string  `json:"logoUrl,omitempty"`
	LogoURLDark          *string  `json:"logoUrlDark,omitempty"`
	MaterializedAppIDs   []string `json:"materializedAppIds,omitempty"`
	Reason               *string  `json:"reason,omitempty"`
}

func (a *AppTemplateSummary) UnmarshalJSON(data []byte) error {
	var raw struct {
		ID                         string   `json:"id"`
		TemplateID                 string   `json:"templateId"`
		TemplateIDSnake            string   `json:"template_id"`
		Name                       string   `json:"name"`
		DisplayName                string   `json:"displayName"`
		DisplayNameSnake           string   `json:"display_name"`
		Description                *string  `json:"description"`
		Category                   *string  `json:"category"`
		CanonicalConnectorID       *string  `json:"canonicalConnectorId"`
		CanonicalConnectorIDSnake  *string  `json:"canonical_connector_id"`
		LogoURL                    *string  `json:"logoUrl"`
		LogoURLSnake               *string  `json:"logo_url"`
		LogoURLDark                *string  `json:"logoUrlDark"`
		LogoURLDarkSnake           *string  `json:"logo_url_dark"`
		MaterializedAppIDs         []string `json:"materializedAppIds"`
		MaterializedAppIDsSnake    []string `json:"materialized_app_ids"`
		MaterializedAppIDsAltSnake []string `json:"materialized_appids"`
		Reason                     *string  `json:"reason"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	a.ID = strings.TrimSpace(raw.ID)
	a.TemplateID = strings.TrimSpace(firstNonEmpty(raw.TemplateID, raw.TemplateIDSnake, raw.ID))
	a.Name = strings.TrimSpace(raw.Name)
	a.DisplayName = strings.TrimSpace(firstNonEmpty(raw.DisplayName, raw.DisplayNameSnake, raw.Name, raw.TemplateID, raw.ID))
	a.Description = cloneTrimmedStringPtr(raw.Description)
	a.Category = cloneTrimmedStringPtr(raw.Category)
	a.CanonicalConnectorID = cloneTrimmedStringPtr(firstStringPtr(raw.CanonicalConnectorID, raw.CanonicalConnectorIDSnake))
	a.LogoURL = cloneTrimmedStringPtr(firstStringPtr(raw.LogoURL, raw.LogoURLSnake))
	a.LogoURLDark = cloneTrimmedStringPtr(firstStringPtr(raw.LogoURLDark, raw.LogoURLDarkSnake))
	a.MaterializedAppIDs = trimPluginInterfaceStrings(firstNonEmptyStringSlice(raw.MaterializedAppIDs, raw.MaterializedAppIDsSnake, raw.MaterializedAppIDsAltSnake))
	a.Reason = cloneTrimmedStringPtr(raw.Reason)
	return nil
}

func (a *AppTemplateSummary) MarshalJSON() ([]byte, error) {
	templateID := strings.TrimSpace(a.TemplateID)
	if templateID == "" {
		templateID = a.ID
	}
	name := strings.TrimSpace(a.Name)
	if name == "" {
		name = a.DisplayName
	}
	materializedAppIDs := append([]string(nil), a.MaterializedAppIDs...)
	if materializedAppIDs == nil {
		materializedAppIDs = []string{}
	}
	return json.Marshal(struct {
		TemplateID           string   `json:"templateId"`
		Name                 string   `json:"name"`
		Description          *string  `json:"description"`
		Category             *string  `json:"category"`
		CanonicalConnectorID *string  `json:"canonicalConnectorId"`
		LogoURL              *string  `json:"logoUrl"`
		LogoURLDark          *string  `json:"logoUrlDark"`
		MaterializedAppIDs   []string `json:"materializedAppIds"`
		Reason               *string  `json:"reason"`
	}{
		TemplateID:           templateID,
		Name:                 name,
		Description:          a.Description,
		Category:             a.Category,
		CanonicalConnectorID: a.CanonicalConnectorID,
		LogoURL:              a.LogoURL,
		LogoURLDark:          a.LogoURLDark,
		MaterializedAppIDs:   materializedAppIDs,
		Reason:               a.Reason,
	})
}

type MarketplaceAddParams struct {
	Name        string   `json:"name,omitempty"`
	URL         string   `json:"url,omitempty"`
	Source      string   `json:"source,omitempty"`
	RefName     *string  `json:"refName,omitempty"`
	SparsePaths []string `json:"sparsePaths,omitempty"`
}

func (p *MarketplaceAddParams) MarshalJSON() ([]byte, error) {
	if p == nil {
		return []byte("null"), nil
	}
	return json.Marshal(struct {
		Source      string    `json:"source"`
		RefName     *string   `json:"refName"`
		SparsePaths *[]string `json:"sparsePaths"`
	}{
		Source:      firstNonEmpty(p.Source, p.URL),
		RefName:     cloneStringPtr(p.RefName),
		SparsePaths: optionalStringSlicePtrForJSON(p.SparsePaths),
	})
}

type MarketplaceAddResponse struct {
	MarketplaceName string      `json:"marketplaceName"`
	InstalledRoot   string      `json:"installedRoot"`
	AlreadyAdded    bool        `json:"alreadyAdded"`
	Marketplace     Marketplace `json:"marketplace"`
	AlreadyPresent  bool        `json:"alreadyPresent"`
}

func (r *MarketplaceAddResponse) MarshalJSON() ([]byte, error) {
	alreadyAdded := r.AlreadyAdded || r.AlreadyPresent
	return json.Marshal(struct {
		MarketplaceName string `json:"marketplaceName"`
		InstalledRoot   string `json:"installedRoot"`
		AlreadyAdded    bool   `json:"alreadyAdded"`
	}{
		MarketplaceName: r.MarketplaceName,
		InstalledRoot:   r.InstalledRoot,
		AlreadyAdded:    alreadyAdded,
	})
}

type MarketplaceRemoveParams struct {
	MarketplaceName string `json:"marketplaceName,omitempty"`
	Name            string `json:"name,omitempty"`
}

func (p *MarketplaceRemoveParams) MarshalJSON() ([]byte, error) {
	if p == nil {
		return []byte("null"), nil
	}
	return json.Marshal(struct {
		MarketplaceName string `json:"marketplaceName"`
	}{
		MarketplaceName: firstNonEmpty(p.MarketplaceName, p.Name),
	})
}

type MarketplaceRemoveResponse struct {
	MarketplaceName string  `json:"marketplaceName"`
	InstalledRoot   *string `json:"installedRoot"`
	Removed         bool    `json:"removed"`
}

func (r *MarketplaceRemoveResponse) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		MarketplaceName string  `json:"marketplaceName"`
		InstalledRoot   *string `json:"installedRoot"`
	}{
		MarketplaceName: r.MarketplaceName,
		InstalledRoot:   cloneStringPtr(r.InstalledRoot),
	})
}

type MarketplaceUpgradeParams struct {
	MarketplaceName *string `json:"marketplaceName,omitempty"`
}

func (p *MarketplaceUpgradeParams) MarshalJSON() ([]byte, error) {
	if p == nil {
		return []byte("null"), nil
	}
	return json.Marshal(struct {
		MarketplaceName *string `json:"marketplaceName"`
	}{
		MarketplaceName: cloneStringPtr(p.MarketplaceName),
	})
}

type MarketplaceUpgradeResponse struct {
	SelectedMarketplaces []string                      `json:"selectedMarketplaces"`
	UpgradedRoots        []string                      `json:"upgradedRoots"`
	Errors               []MarketplaceUpgradeErrorInfo `json:"errors"`
	ErrorMap             map[string]string             `json:"errorMap,omitempty"`
}

type MarketplaceUpgradeErrorInfo struct {
	MarketplaceName string `json:"marketplaceName"`
	Message         string `json:"message"`
}

func (r *MarketplaceUpgradeResponse) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		SelectedMarketplaces []string                      `json:"selectedMarketplaces"`
		UpgradedRoots        []string                      `json:"upgradedRoots"`
		Errors               []MarketplaceUpgradeErrorInfo `json:"errors"`
	}{
		SelectedMarketplaces: stringSliceForJSON(r.SelectedMarketplaces),
		UpgradedRoots:        stringSliceForJSON(r.UpgradedRoots),
		Errors:               marketplaceUpgradeErrorsForJSON(r.Errors),
	})
}

type PluginListParams struct {
	CWDs             []string `json:"cwds,omitempty"`
	MarketplaceKinds []string `json:"marketplaceKinds,omitempty"`
	IncludeInstalled bool     `json:"includeInstalled,omitempty"`
	ForceRefetch     bool     `json:"forceRefetch,omitempty"`
}

func (p *PluginListParams) MarshalJSON() ([]byte, error) {
	if p == nil {
		return []byte("null"), nil
	}
	return json.Marshal(struct {
		CWDs             []string `json:"cwds,omitempty"`
		MarketplaceKinds []string `json:"marketplaceKinds,omitempty"`
		IncludeInstalled bool     `json:"includeInstalled,omitempty"`
		ForceRefetch     bool     `json:"forceRefetch,omitempty"`
	}{
		CWDs:             optionalStringSliceForJSON(p.CWDs),
		MarketplaceKinds: optionalStringSliceForJSON(p.MarketplaceKinds),
		IncludeInstalled: p.IncludeInstalled,
		ForceRefetch:     p.ForceRefetch,
	})
}

type PluginListMarketplaceKind string

const (
	PluginListMarketplaceLocal        PluginListMarketplaceKind = "local"
	PluginListMarketplaceVertical     PluginListMarketplaceKind = "vertical"
	PluginListMarketplaceWorkspace    PluginListMarketplaceKind = "workspace-directory"
	PluginListMarketplaceSharedWithMe PluginListMarketplaceKind = "shared-with-me"
	PluginListMarketplaceCreatedByMe  PluginListMarketplaceKind = "created-by-me-remote"
)

type PluginListResponse struct {
	Marketplaces          []PluginMarketplaceEntry   `json:"marketplaces"`
	MarketplaceLoadErrors []MarketplaceLoadErrorInfo `json:"marketplaceLoadErrors"`
	FeaturedPluginIDs     []string                   `json:"featuredPluginIds"`
	Plugins               []PluginSummary            `json:"plugins,omitempty"`
}

func (r *PluginListResponse) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Marketplaces          []PluginMarketplaceEntry   `json:"marketplaces"`
		MarketplaceLoadErrors []MarketplaceLoadErrorInfo `json:"marketplaceLoadErrors"`
		FeaturedPluginIDs     []string                   `json:"featuredPluginIds"`
	}{
		Marketplaces:          pluginMarketplaceEntriesForJSON(r.Marketplaces),
		MarketplaceLoadErrors: marketplaceLoadErrorsForJSON(r.MarketplaceLoadErrors),
		FeaturedPluginIDs:     stringSliceForJSON(r.FeaturedPluginIDs),
	})
}

type PluginMarketplaceEntry struct {
	Name      string          `json:"name"`
	Path      *string         `json:"path"`
	Interface any             `json:"interface"`
	Plugins   []PluginSummary `json:"plugins"`
}

func (e *PluginMarketplaceEntry) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Name      string                `json:"name"`
		Path      *string               `json:"path"`
		Interface *MarketplaceInterface `json:"interface"`
		Plugins   []PluginSummary       `json:"plugins"`
	}{
		Name:      e.Name,
		Path:      e.Path,
		Interface: marketplaceInterfaceForJSON(e.Interface),
		Plugins:   pluginSummariesForJSON(e.Plugins),
	})
}

type MarketplaceLoadErrorInfo struct {
	MarketplacePath string `json:"marketplacePath"`
	Message         string `json:"message"`
}

type PluginInstalledParams struct {
	CWDs                         []string `json:"cwds,omitempty"`
	InstallSuggestionPluginNames []string `json:"installSuggestionPluginNames,omitempty"`
}

func (p *PluginInstalledParams) MarshalJSON() ([]byte, error) {
	if p == nil {
		return []byte("null"), nil
	}
	return json.Marshal(struct {
		CWDs                         *[]string `json:"cwds"`
		InstallSuggestionPluginNames *[]string `json:"installSuggestionPluginNames"`
	}{
		CWDs:                         optionalStringSlicePtrForJSON(p.CWDs),
		InstallSuggestionPluginNames: optionalStringSlicePtrForJSON(p.InstallSuggestionPluginNames),
	})
}

type PluginInstalledResponse struct {
	Marketplaces          []PluginMarketplaceEntry   `json:"marketplaces"`
	MarketplaceLoadErrors []MarketplaceLoadErrorInfo `json:"marketplaceLoadErrors"`
	Plugins               []PluginSummary            `json:"plugins,omitempty"`
}

// PluginReconcileParams mirrors Rust PluginReconcileParams (#41949). The
// optional reason is recorded with the reconciliation attempt.
type PluginReconcileParams struct {
	Reason *string `json:"reason,omitempty"`
}

// PluginReconcileChangedPlugin describes runtime categories affected by a
// plugin bundle, enablement, or removal change.
type PluginReconcileChangedPlugin struct {
	ID        string `json:"id"`
	HasMCPS   bool   `json:"hasMcps"`
	HasApps   bool   `json:"hasApps"`
	HasHooks  bool   `json:"hasHooks"`
	HasSkills bool   `json:"hasSkills"`
}

// PluginReconcileResponse reports plugins changed by a reconciliation pass and
// remote-plugin failures observed during that pass.
type PluginReconcileResponse struct {
	ChangedPlugins                       []PluginReconcileChangedPlugin `json:"changedPlugins"`
	FailedRemotePluginIDs                []string                       `json:"failedRemotePluginIds"`
	FailedMaterializationRemotePluginIDs []string                       `json:"failedMaterializationRemotePluginIds"`
}

func (r *PluginInstalledResponse) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Marketplaces          []PluginMarketplaceEntry   `json:"marketplaces"`
		MarketplaceLoadErrors []MarketplaceLoadErrorInfo `json:"marketplaceLoadErrors"`
	}{
		Marketplaces:          pluginMarketplaceEntriesForJSON(r.Marketplaces),
		MarketplaceLoadErrors: marketplaceLoadErrorsForJSON(r.MarketplaceLoadErrors),
	})
}

type PluginReadParams struct {
	MarketplaceName       string `json:"marketplaceName,omitempty"`
	MarketplacePath       string `json:"marketplacePath,omitempty"`
	RemoteMarketplaceName string `json:"remoteMarketplaceName,omitempty"`
	PluginName            string `json:"pluginName"`
	RemotePluginID        string `json:"remotePluginId,omitempty"`
}

func (p *PluginReadParams) MarshalJSON() ([]byte, error) {
	if p == nil {
		return []byte("null"), nil
	}
	return json.Marshal(struct {
		MarketplacePath       *string `json:"marketplacePath"`
		RemoteMarketplaceName *string `json:"remoteMarketplaceName"`
		PluginName            string  `json:"pluginName"`
	}{
		MarketplacePath:       stringPtrIfNotEmpty(p.MarketplacePath),
		RemoteMarketplaceName: stringPtrIfNotEmpty(p.RemoteMarketplaceName),
		PluginName:            p.PluginName,
	})
}

type PluginReadResponse struct {
	Plugin PluginDetail `json:"plugin"`
}

type PluginSkillReadParams struct {
	RemoteMarketplaceName string `json:"remoteMarketplaceName"`
	RemotePluginID        string `json:"remotePluginId"`
	SkillName             string `json:"skillName"`
}

type PluginSkillReadResponse struct {
	Contents *string `json:"contents"`
	Markdown string  `json:"markdown,omitempty"`
}

func (r *PluginSkillReadResponse) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Contents *string `json:"contents"`
	}{
		Contents: cloneStringPtr(r.Contents),
	})
}

type PluginInstallParams struct {
	PluginID              string `json:"pluginId,omitempty"`
	PluginName            string `json:"pluginName,omitempty"`
	MarketplacePath       string `json:"marketplacePath,omitempty"`
	MarketplaceName       string `json:"marketplaceName,omitempty"`
	RemoteMarketplaceName string `json:"remoteMarketplaceName,omitempty"`
	// InstallAttemptID lets clients correlate a remote plugin installation
	// request with a specific installation attempt (Rust 89a335ed50).
	InstallAttemptID string `json:"installAttemptId,omitempty"`
}

func (p *PluginInstallParams) MarshalJSON() ([]byte, error) {
	if p == nil {
		return []byte("null"), nil
	}
	remoteMarketplaceName := p.RemoteMarketplaceName
	if remoteMarketplaceName == "" {
		remoteMarketplaceName = p.MarketplaceName
	}
	pluginName := p.PluginName
	if pluginName == "" {
		pluginName = pluginNameFromID(p.PluginID)
	}
	return json.Marshal(struct {
		MarketplacePath       *string `json:"marketplacePath"`
		RemoteMarketplaceName *string `json:"remoteMarketplaceName"`
		InstallAttemptID      *string `json:"installAttemptId"`
		PluginName            string  `json:"pluginName"`
	}{
		MarketplacePath:       stringPtrIfNotEmpty(p.MarketplacePath),
		RemoteMarketplaceName: stringPtrIfNotEmpty(remoteMarketplaceName),
		InstallAttemptID:      stringPtrIfNotEmpty(p.InstallAttemptID),
		PluginName:            pluginName,
	})
}

type PluginInstallResponse struct {
	PluginID        string           `json:"pluginId,omitempty"`
	AuthPolicy      PluginAuthPolicy `json:"authPolicy"`
	AppsNeedingAuth []AppSummary     `json:"appsNeedingAuth"`
}

func (r *PluginInstallResponse) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		AuthPolicy      PluginAuthPolicy `json:"authPolicy"`
		AppsNeedingAuth []AppSummary     `json:"appsNeedingAuth"`
	}{
		AuthPolicy:      r.AuthPolicy,
		AppsNeedingAuth: appSummariesForJSON(r.AppsNeedingAuth),
	})
}

type PluginUninstallParams struct {
	PluginID string `json:"pluginId"`
}

type PluginUninstallResponse struct{}

type PluginSharePrincipal struct {
	Type          string `json:"type,omitempty"`
	ID            string `json:"id,omitempty"`
	PrincipalType string `json:"principalType,omitempty"`
	PrincipalID   string `json:"principalId,omitempty"`
	Role          string `json:"role,omitempty"`
	Name          string `json:"name,omitempty"`
}

func (p *PluginSharePrincipal) MarshalJSON() ([]byte, error) {
	principalType := strings.TrimSpace(firstNonEmpty(p.PrincipalType, p.Type))
	principalID := strings.TrimSpace(firstNonEmpty(p.PrincipalID, p.ID))
	return json.Marshal(struct {
		PrincipalType string `json:"principalType"`
		PrincipalID   string `json:"principalId"`
		Role          string `json:"role"`
		Name          string `json:"name"`
	}{
		PrincipalType: principalType,
		PrincipalID:   principalID,
		Role:          strings.TrimSpace(p.Role),
		Name:          strings.TrimSpace(p.Name),
	})
}

type PluginShareDiscoverability string

const (
	PluginShareDiscoverabilityListed   PluginShareDiscoverability = "LISTED"
	PluginShareDiscoverabilityUnlisted PluginShareDiscoverability = "UNLISTED"
	PluginShareDiscoverabilityPrivate  PluginShareDiscoverability = "PRIVATE"
)

type PluginShareUpdateDiscoverability string

const (
	PluginShareUpdateDiscoverabilityUnlisted PluginShareUpdateDiscoverability = "UNLISTED"
	PluginShareUpdateDiscoverabilityPrivate  PluginShareUpdateDiscoverability = "PRIVATE"
	PluginShareUpdateDiscoverabilityListed   PluginShareUpdateDiscoverability = "LISTED"
)

type PluginSharePrincipalType string

const (
	PluginSharePrincipalUser      PluginSharePrincipalType = "user"
	PluginSharePrincipalGroup     PluginSharePrincipalType = "group"
	PluginSharePrincipalWorkspace PluginSharePrincipalType = "workspace"
)

type PluginSharePrincipalRole string

const (
	PluginSharePrincipalReader PluginSharePrincipalRole = "reader"
	PluginSharePrincipalEditor PluginSharePrincipalRole = "editor"
	PluginSharePrincipalOwner  PluginSharePrincipalRole = "owner"
)

type PluginShareTargetRole string

const (
	PluginShareTargetReader PluginShareTargetRole = "reader"
	PluginShareTargetEditor PluginShareTargetRole = "editor"
)

type PluginShareTarget struct {
	PrincipalType PluginSharePrincipalType `json:"principalType"`
	PrincipalID   string                   `json:"principalId"`
	Role          PluginShareTargetRole    `json:"role"`
}

type PluginShareContext struct {
	RemotePluginID        string                 `json:"remotePluginId"`
	RemoteVersion         *string                `json:"remoteVersion"`
	Discoverability       *string                `json:"discoverability"`
	ShareURL              *string                `json:"shareUrl"`
	CreatorAccountUserID  *string                `json:"creatorAccountUserId"`
	CreatorName           *string                `json:"creatorName"`
	SharePrincipals       []PluginSharePrincipal `json:"sharePrincipals"`
	Principals            []PluginSharePrincipal `json:"principals,omitempty"`
	CanPublishToWorkspace *bool                  `json:"canPublishToWorkspace,omitempty"`
}

func (c *PluginShareContext) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		RemotePluginID        string                 `json:"remotePluginId"`
		RemoteVersion         *string                `json:"remoteVersion"`
		Discoverability       *string                `json:"discoverability"`
		ShareURL              *string                `json:"shareUrl"`
		CreatorAccountUserID  *string                `json:"creatorAccountUserId"`
		CreatorName           *string                `json:"creatorName"`
		SharePrincipals       []PluginSharePrincipal `json:"sharePrincipals"`
		CanPublishToWorkspace *bool                  `json:"canPublishToWorkspace"`
	}{
		RemotePluginID:        c.RemotePluginID,
		RemoteVersion:         c.RemoteVersion,
		Discoverability:       c.Discoverability,
		ShareURL:              c.ShareURL,
		CreatorAccountUserID:  c.CreatorAccountUserID,
		CreatorName:           c.CreatorName,
		SharePrincipals:       pluginSharePrincipalsPtrForJSON(c.SharePrincipals),
		CanPublishToWorkspace: cloneBoolPtr(c.CanPublishToWorkspace),
	})
}

type PluginShareSaveParams struct {
	PluginPath      string                 `json:"pluginPath,omitempty"`
	RemotePluginID  string                 `json:"remotePluginId,omitempty"`
	Discoverability string                 `json:"discoverability,omitempty"`
	Targets         []PluginSharePrincipal `json:"targets,omitempty"`
	ShareTargets    []PluginSharePrincipal `json:"shareTargets,omitempty"`
}

func (p *PluginShareSaveParams) MarshalJSON() ([]byte, error) {
	if p == nil {
		return []byte("null"), nil
	}
	targets, hasTargets := pluginShareTargetsForParams(p.ShareTargets, p.Targets)
	return json.Marshal(struct {
		PluginPath      string               `json:"pluginPath"`
		RemotePluginID  *string              `json:"remotePluginId"`
		Discoverability *string              `json:"discoverability"`
		ShareTargets    *[]PluginShareTarget `json:"shareTargets"`
	}{
		PluginPath:      p.PluginPath,
		RemotePluginID:  stringPtrIfNotEmpty(p.RemotePluginID),
		Discoverability: stringPtrIfNotEmpty(p.Discoverability),
		ShareTargets:    shareTargetsPtrForOptionalJSON(targets, hasTargets),
	})
}

type PluginShareSaveResponse struct {
	RemotePluginID        string `json:"remotePluginId"`
	ShareURL              string `json:"shareUrl"`
	CanPublishToWorkspace *bool  `json:"canPublishToWorkspace"`
}

type PluginShareUpdateTargetsParams struct {
	RemotePluginID  string                 `json:"remotePluginId"`
	Discoverability string                 `json:"discoverability"`
	Targets         []PluginSharePrincipal `json:"targets,omitempty"`
	ShareTargets    []PluginSharePrincipal `json:"shareTargets"`
}

func (p *PluginShareUpdateTargetsParams) MarshalJSON() ([]byte, error) {
	if p == nil {
		return []byte("null"), nil
	}
	targets, _ := pluginShareTargetsForParams(p.ShareTargets, p.Targets)
	if targets == nil {
		targets = []PluginShareTarget{}
	}
	return json.Marshal(struct {
		RemotePluginID  string              `json:"remotePluginId"`
		Discoverability string              `json:"discoverability"`
		ShareTargets    []PluginShareTarget `json:"shareTargets"`
	}{
		RemotePluginID:  p.RemotePluginID,
		Discoverability: p.Discoverability,
		ShareTargets:    targets,
	})
}

type PluginShareUpdateTargetsResponse struct {
	Discoverability string                 `json:"discoverability,omitempty"`
	Principals      []PluginSharePrincipal `json:"principals,omitempty"`
}

func (r *PluginShareUpdateTargetsResponse) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Discoverability string                 `json:"discoverability"`
		Principals      []PluginSharePrincipal `json:"principals"`
	}{
		Discoverability: r.Discoverability,
		Principals:      pluginSharePrincipalsForJSON(r.Principals),
	})
}

type PluginShareListParams struct{}

type PluginShareListResponse struct {
	Data  []PluginShareListItem `json:"data"`
	Items []PluginShareContext  `json:"items,omitempty"`
}

func (r *PluginShareListResponse) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Data []PluginShareListItem `json:"data"`
	}{
		Data: pluginShareListItemsForJSON(r.Data),
	})
}

type PluginShareListItem struct {
	Plugin          PluginSummary `json:"plugin"`
	LocalPluginPath *string       `json:"localPluginPath"`
}

type PluginShareCheckoutParams struct {
	RemotePluginID string `json:"remotePluginId"`
}

type PluginShareCheckoutResponse struct {
	RemotePluginID  string  `json:"remotePluginId"`
	PluginID        string  `json:"pluginId"`
	PluginName      string  `json:"pluginName"`
	PluginPath      string  `json:"pluginPath"`
	MarketplaceName string  `json:"marketplaceName"`
	MarketplacePath string  `json:"marketplacePath"`
	RemoteVersion   *string `json:"remoteVersion"`
	LocalPath       string  `json:"localPath,omitempty"`
}

func (r *PluginShareCheckoutResponse) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		RemotePluginID  string  `json:"remotePluginId"`
		PluginID        string  `json:"pluginId"`
		PluginName      string  `json:"pluginName"`
		PluginPath      string  `json:"pluginPath"`
		MarketplaceName string  `json:"marketplaceName"`
		MarketplacePath string  `json:"marketplacePath"`
		RemoteVersion   *string `json:"remoteVersion"`
	}{
		RemotePluginID:  r.RemotePluginID,
		PluginID:        r.PluginID,
		PluginName:      r.PluginName,
		PluginPath:      r.PluginPath,
		MarketplaceName: r.MarketplaceName,
		MarketplacePath: r.MarketplacePath,
		RemoteVersion:   cloneStringPtr(r.RemoteVersion),
	})
}

type PluginShareDeleteParams struct {
	RemotePluginID string `json:"remotePluginId"`
}

type PluginShareDeleteResponse struct{}

type PluginShareBackend interface {
	SaveShare(*PluginShareSaveParams) (*PluginShareSaveResponse, error)
}

type PluginService struct {
	mu                            sync.Mutex
	marketplaces                  map[string]Marketplace
	plugins                       map[string]PluginDetail
	shares                        map[string]PluginShareContext
	now                           func() time.Time
	marketplaceInstallRoot        string
	marketplaceMaterializer       MarketplaceMaterializer
	marketplacePluginMaterializer MarketplacePluginMaterializer
	marketplaceRevision           MarketplaceRevisionResolver
	marketplaceConfigPath         string
	suggestedProvider             SuggestedPluginProvider
	suggestedProviderKey          string
	suggestedCache                *SuggestedPluginList
	shareBackend                  PluginShareBackend
	codexHome                     string
	authMode                      string
	modelProviderID               string
	targetCuratedMarketplace      TargetCuratedMarketplace
	curatedSyncInFlight           bool
}

func (s *PluginService) SetShareBackend(backend PluginShareBackend) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.shareBackend = backend
	s.mu.Unlock()
}

func NewPluginService() *PluginService {
	return &PluginService{
		marketplaces:                  map[string]Marketplace{},
		plugins:                       map[string]PluginDetail{},
		shares:                        map[string]PluginShareContext{},
		now:                           time.Now,
		marketplaceInstallRoot:        defaultMarketplaceInstallRoot(),
		marketplaceMaterializer:       &GitMarketplaceMaterializer{},
		marketplacePluginMaterializer: &GitMarketplaceMaterializer{},
		marketplaceRevision:           &GitMarketplaceRevisionResolver{},
		targetCuratedMarketplace:      TargetCuratedOpenAI,
	}
}

func (s *PluginService) SetClock(clock func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if clock == nil {
		s.now = time.Now
		return
	}
	s.now = clock
}

func (s *PluginService) SetMarketplaceInstallRoot(root string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	root = strings.TrimSpace(root)
	if root == "" {
		root = defaultMarketplaceInstallRoot()
	}
	s.marketplaceInstallRoot = root
}

func (s *PluginService) SetMarketplaceMaterializer(materializer MarketplaceMaterializer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if materializer == nil {
		materializer = &GitMarketplaceMaterializer{}
	}
	s.marketplaceMaterializer = materializer
}

func (s *PluginService) SetMarketplacePluginMaterializer(materializer MarketplacePluginMaterializer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if materializer == nil {
		materializer = &GitMarketplaceMaterializer{}
	}
	s.marketplacePluginMaterializer = materializer
}

func (s *PluginService) SetMarketplaceRevisionResolver(resolver MarketplaceRevisionResolver) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if resolver == nil {
		resolver = &GitMarketplaceRevisionResolver{}
	}
	s.marketplaceRevision = resolver
}

func (s *PluginService) AddMarketplace(params *MarketplaceAddParams) (*MarketplaceAddResponse, error) {
	if params == nil {
		return nil, fmt.Errorf("%w: params are nil", ErrInvalidPluginRequest)
	}
	name := strings.TrimSpace(params.Name)
	source := strings.TrimSpace(firstNonEmpty(params.Source, params.URL))
	parsedSource, err := ParseMarketplaceSource(source, params.RefName)
	if err != nil {
		return nil, err
	}
	if len(params.SparsePaths) > 0 && parsedSource.Kind != MarketplaceSourceGit {
		return nil, fmt.Errorf("%w: --sparse is only supported for git marketplace sources", ErrInvalidPluginRequest)
	}
	if name == "" {
		name = marketplaceNameFromURL(parsedSource.NameCandidate())
	}
	if name == "" {
		return nil, fmt.Errorf("%w: marketplace name and source are required", ErrInvalidPluginRequest)
	}
	if IsReservedMarketplaceName(name) {
		return nil, fmt.Errorf("marketplace `%s` is reserved and cannot be added from this source", name)
	}
	safeName, err := safeMarketplaceDirName(name)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	installRoot := s.marketplaceInstallRoot
	materializer := s.marketplaceMaterializer
	configPath := s.marketplaceConfigPath
	now := s.now
	if existing, ok := s.marketplaces[name]; ok {
		s.mu.Unlock()
		return &MarketplaceAddResponse{
			MarketplaceName: name,
			InstalledRoot:   existing.RootPath,
			AlreadyAdded:    true,
			Marketplace:     existing,
			AlreadyPresent:  true,
		}, nil
	}
	s.mu.Unlock()

	rootPath := filepath.Join(installRoot, safeName)
	if parsedSource.Kind == MarketplaceSourceLocal {
		rootPath = parsedSource.Path
	}
	if parsedSource.Kind == MarketplaceSourceGit && materializer != nil {
		if err := materializer.MaterializeMarketplace(parsedSource, append([]string(nil), params.SparsePaths...), rootPath); err != nil {
			return nil, err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.marketplaces[name]; ok {
		return &MarketplaceAddResponse{
			MarketplaceName: name,
			InstalledRoot:   existing.RootPath,
			AlreadyAdded:    true,
			Marketplace:     existing,
			AlreadyPresent:  true,
		}, nil
	}
	marketplace := Marketplace{
		Name:        name,
		SourceURL:   parsedSource.Display(),
		SourceType:  string(parsedSource.Kind),
		RefName:     cloneStringPtr(parsedSource.RefName),
		SparsePaths: append([]string(nil), params.SparsePaths...),
		RootPath:    filepath.Clean(rootPath),
		AddedAt:     now().UTC(),
	}
	if err := recordMarketplaceConfig(configPath, &marketplace); err != nil {
		return nil, err
	}
	s.marketplaces[name] = marketplace
	return &MarketplaceAddResponse{
		MarketplaceName: name,
		InstalledRoot:   marketplace.RootPath,
		Marketplace:     marketplace,
	}, nil
}

func (s *PluginService) RemoveMarketplace(params *MarketplaceRemoveParams) (*MarketplaceRemoveResponse, error) {
	if params == nil {
		return nil, fmt.Errorf("%w: params are nil", ErrInvalidPluginRequest)
	}
	name := strings.TrimSpace(firstNonEmpty(params.MarketplaceName, params.Name))
	if name == "" {
		return nil, fmt.Errorf("%w: marketplace name is required", ErrInvalidPluginRequest)
	}
	s.mu.Lock()
	configPath := s.marketplaceConfigPath
	installRoot := s.marketplaceInstallRoot
	marketplace, existed := s.marketplaces[name]
	delete(s.marketplaces, name)
	for id, detail := range s.plugins {
		if detail.Summary.MarketplaceName == name {
			delete(s.plugins, id)
		}
	}
	s.mu.Unlock()
	if err := removeMarketplaceConfig(configPath, name); err != nil {
		return nil, err
	}
	if err := removeInstalledPluginConfigsForMarketplace(configPath, name); err != nil {
		return nil, err
	}
	if err := removeMaterializedMarketplacePlugins(installRoot, name); err != nil {
		return nil, err
	}
	var installedRoot *string
	if existed {
		installedRoot = &marketplace.RootPath
	}
	return &MarketplaceRemoveResponse{MarketplaceName: name, InstalledRoot: installedRoot, Removed: existed}, nil
}

func (s *PluginService) UpgradeMarketplace(params *MarketplaceUpgradeParams) (*MarketplaceUpgradeResponse, error) {
	if params == nil {
		params = &MarketplaceUpgradeParams{}
	}
	s.mu.Lock()
	marketplaces := make([]Marketplace, 0, len(s.marketplaces))
	for _, marketplace := range s.marketplaces {
		marketplaces = append(marketplaces, marketplace)
	}
	materializer := s.marketplaceMaterializer
	resolver := s.marketplaceRevision
	s.mu.Unlock()

	selected := make([]string, 0, len(marketplaces))
	roots := make([]string, 0, len(marketplaces))
	var upgradeErrors []MarketplaceUpgradeErrorInfo
	updatedMarketplaces := map[string]Marketplace{}
	targetMarketplace := ""
	if params.MarketplaceName != nil {
		targetMarketplace = strings.TrimSpace(*params.MarketplaceName)
	}
	for _, marketplace := range marketplaces {
		name := marketplace.Name
		if targetMarketplace != "" && targetMarketplace != name {
			continue
		}
		if marketplace.SourceType != string(MarketplaceSourceGit) {
			continue
		}
		selected = append(selected, name)
		source := &ParsedMarketplaceSource{
			Kind:    MarketplaceSourceGit,
			URL:     marketplaceConfigSource(&marketplace),
			RefName: cloneStringPtr(marketplace.RefName),
		}
		revision := ""
		if resolver != nil {
			resolved, err := resolver.MarketplaceRevision(source)
			if err != nil {
				upgradeErrors = append(upgradeErrors, MarketplaceUpgradeErrorInfo{MarketplaceName: name, Message: err.Error()})
				continue
			}
			revision = strings.TrimSpace(resolved)
		}
		if installedRevision := installedMarketplaceRevision(marketplace.RootPath); revision != "" && installedRevision == revision {
			continue
		}
		if upgrader, ok := materializer.(MarketplaceUpgrader); ok {
			if err := upgrader.UpgradeMarketplace(source, marketplace.SparsePaths, marketplace.RootPath); err != nil {
				upgradeErrors = append(upgradeErrors, MarketplaceUpgradeErrorInfo{MarketplaceName: name, Message: err.Error()})
				continue
			}
		} else if materializer != nil {
			if err := materializer.MaterializeMarketplace(source, marketplace.SparsePaths, marketplace.RootPath); err != nil {
				upgradeErrors = append(upgradeErrors, MarketplaceUpgradeErrorInfo{MarketplaceName: name, Message: err.Error()})
				continue
			}
		}
		if revision != "" {
			// Rust #39595: keep marketplace upgrade state out of config.toml;
			// the activated revision lives in .codex-marketplace-install.json.
			if err := writeMarketplaceInstallState(marketplace.RootPath, revision); err != nil {
				upgradeErrors = append(upgradeErrors, MarketplaceUpgradeErrorInfo{MarketplaceName: name, Message: err.Error()})
				continue
			}
		}
		updatedMarketplaces[name] = marketplace
		roots = append(roots, marketplace.RootPath)
	}
	if targetMarketplace != "" && len(selected) == 0 {
		return nil, fmt.Errorf("%w: marketplace `%s` is not configured as a Git marketplace", ErrInvalidPluginRequest, targetMarketplace)
	}
	sort.Strings(selected)
	sort.Strings(roots)
	s.mu.Lock()
	for name, marketplace := range updatedMarketplaces {
		s.marketplaces[name] = marketplace
	}
	s.mu.Unlock()
	upgradeErrors = append(upgradeErrors, s.clearUninstalledMaterializedPluginsAfterMarketplaceUpgrade(updatedMarketplaces)...)
	upgradeErrors = append(upgradeErrors, s.refreshInstalledPluginsAfterMarketplaceUpgrade(updatedMarketplaces)...)
	sort.SliceStable(upgradeErrors, func(i int, j int) bool {
		return upgradeErrors[i].MarketplaceName < upgradeErrors[j].MarketplaceName
	})
	return &MarketplaceUpgradeResponse{SelectedMarketplaces: selected, UpgradedRoots: roots, Errors: upgradeErrors}, nil
}

func (s *PluginService) clearUninstalledMaterializedPluginsAfterMarketplaceUpgrade(marketplaces map[string]Marketplace) []MarketplaceUpgradeErrorInfo {
	if s == nil || len(marketplaces) == 0 {
		return nil
	}
	names := map[string]bool{}
	for _, marketplace := range marketplaces {
		if strings.TrimSpace(marketplace.Name) != "" {
			names[marketplace.Name] = true
		}
	}
	if len(names) == 0 {
		return nil
	}
	s.mu.Lock()
	installRoot := s.marketplaceInstallRoot
	installed := map[string]map[string]bool{}
	for _, detail := range s.plugins {
		if !detail.Summary.Installed || !names[detail.Summary.MarketplaceName] {
			continue
		}
		pluginName := sanitize(detail.Summary.Name)
		if pluginName == "" {
			continue
		}
		installedByName := installed[detail.Summary.MarketplaceName]
		if installedByName == nil {
			installedByName = map[string]bool{}
			installed[detail.Summary.MarketplaceName] = installedByName
		}
		installedByName[pluginName] = true
	}
	s.mu.Unlock()
	installRoot = strings.TrimSpace(installRoot)
	if installRoot == "" {
		return nil
	}
	var upgradeErrors []MarketplaceUpgradeErrorInfo
	for name := range names {
		root := filepath.Join(installRoot, InstalledMarketplacePluginsDir, sanitize(name))
		entries, err := os.ReadDir(root)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			upgradeErrors = append(upgradeErrors, MarketplaceUpgradeErrorInfo{MarketplaceName: name, Message: err.Error()})
			continue
		}
		installedByName := installed[name]
		for _, entry := range entries {
			if installedByName != nil && installedByName[entry.Name()] {
				continue
			}
			if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
				upgradeErrors = append(upgradeErrors, MarketplaceUpgradeErrorInfo{MarketplaceName: name, Message: err.Error()})
			}
		}
	}
	return upgradeErrors
}

func (s *PluginService) refreshInstalledPluginsAfterMarketplaceUpgrade(marketplaces map[string]Marketplace) []MarketplaceUpgradeErrorInfo {
	if s == nil || len(marketplaces) == 0 {
		return nil
	}
	names := map[string]bool{}
	targets := make([]Marketplace, 0, len(marketplaces))
	for _, marketplace := range marketplaces {
		names[marketplace.Name] = true
		targets = append(targets, marketplace)
	}
	s.mu.Lock()
	installed := make([]PluginDetail, 0, len(s.plugins))
	for _, detail := range s.plugins {
		if detail.Summary.Installed && names[detail.Summary.MarketplaceName] {
			installed = append(installed, cloneDetail(detail))
		}
	}
	configPath := s.marketplaceConfigPath
	s.mu.Unlock()
	if len(installed) == 0 {
		return nil
	}
	loadedDetails, loadErrors := loadMarketplacePlugins(targets)
	upgradeErrors := make([]MarketplaceUpgradeErrorInfo, 0, len(loadErrors))
	for _, loadError := range loadErrors {
		upgradeErrors = append(upgradeErrors, MarketplaceUpgradeErrorInfo{
			MarketplaceName: marketplaceNameForLoadError(targets, loadError.MarketplacePath),
			Message:         loadError.Message,
		})
	}
	loadedByID := map[string]PluginDetail{}
	loadedByName := map[string]PluginDetail{}
	for _, detail := range loadedDetails {
		if detail.Summary.ID != "" {
			loadedByID[detail.Summary.ID] = cloneDetail(detail)
		}
		if detail.Summary.Name != "" {
			loadedByName[pluginID(detail.Summary.Name, detail.Summary.MarketplaceName)] = cloneDetail(detail)
		}
	}
	for _, installedDetail := range installed {
		refreshed, ok := loadedByID[installedDetail.Summary.ID]
		if !ok {
			refreshed, ok = loadedByName[pluginID(installedDetail.Summary.Name, installedDetail.Summary.MarketplaceName)]
		}
		if !ok {
			upgradeErrors = append(upgradeErrors, MarketplaceUpgradeErrorInfo{
				MarketplaceName: installedDetail.Summary.MarketplaceName,
				Message:         fmt.Sprintf("installed plugin %q not found after marketplace upgrade", installedDetail.Summary.ID),
			})
			continue
		}
		materialized, err := s.materializeMarketplacePluginDetailForUpgrade(&refreshed)
		if err != nil {
			upgradeErrors = append(upgradeErrors, MarketplaceUpgradeErrorInfo{
				MarketplaceName: installedDetail.Summary.MarketplaceName,
				Message:         err.Error(),
			})
			continue
		}
		refreshed = *materialized
		refreshed.Summary.Installed = true
		refreshed.Summary.Enabled = installedDetail.Summary.Enabled
		if refreshed.Summary.ID == "" {
			refreshed.Summary.ID = installedDetail.Summary.ID
		}
		if err := recordInstalledPluginConfig(configPath, &refreshed); err != nil {
			upgradeErrors = append(upgradeErrors, MarketplaceUpgradeErrorInfo{
				MarketplaceName: refreshed.Summary.MarketplaceName,
				Message:         err.Error(),
			})
			continue
		}
		if refreshed.Summary.ID != installedDetail.Summary.ID {
			if err := removeInstalledPluginConfig(configPath, installedDetail.Summary.ID); err != nil {
				upgradeErrors = append(upgradeErrors, MarketplaceUpgradeErrorInfo{
					MarketplaceName: refreshed.Summary.MarketplaceName,
					Message:         err.Error(),
				})
				continue
			}
		}
		s.mu.Lock()
		delete(s.plugins, installedDetail.Summary.ID)
		s.plugins[refreshed.Summary.ID] = cloneDetail(refreshed)
		s.mu.Unlock()
	}
	return upgradeErrors
}

func marketplaceNameForLoadError(marketplaces []Marketplace, marketplacePath string) string {
	cleanPath := cleanPluginPathForCompare(marketplacePath)
	for _, marketplace := range marketplaces {
		root := cleanPluginPathForCompare(marketplace.RootPath)
		if root != "" && (cleanPath == root || strings.HasPrefix(cleanPath, root+string(filepath.Separator))) {
			return marketplace.Name
		}
	}
	return ""
}

func (s *PluginService) AddPlugin(detail PluginDetail) {
	s.mu.Lock()
	defer s.mu.Unlock()
	summary := detail.Summary
	summary.ID = strings.TrimSpace(summary.ID)
	summary.Name = strings.TrimSpace(summary.Name)
	summary.DisplayName = strings.TrimSpace(summary.DisplayName)
	summary.MarketplaceName = strings.TrimSpace(summary.MarketplaceName)
	summary.RemotePluginID = strings.TrimSpace(summary.RemotePluginID)
	if summary.ID == "" {
		summary.ID = pluginID(summary.Name, summary.MarketplaceName)
	}
	if summary.DisplayName == "" {
		summary.DisplayName = summary.Name
	}
	if summary.Availability == "" {
		summary.Availability = PluginAvailable
	}
	if summary.InstallPolicy == "" {
		summary.InstallPolicy = InstallAllowed
	}
	if summary.AuthPolicy == "" {
		summary.AuthPolicy = AuthNone
	}
	if !summary.Enabled && !summary.Installed {
		summary.Enabled = true
	}
	if summary.Keywords == nil {
		summary.Keywords = []string{}
	}
	detail.Summary = summary
	detail.MarketplaceName = strings.TrimSpace(detail.MarketplaceName)
	if detail.MarketplaceName == "" {
		detail.MarketplaceName = summary.MarketplaceName
	}
	if detail.Description == nil && summary.Description != "" {
		value := summary.Description
		detail.Description = &value
	}
	s.plugins[summary.ID] = cloneDetail(detail)
}

func (s *PluginService) ReplaceInstalledRemotePlugins(marketplaceName string, details []PluginDetail) {
	if s == nil {
		return
	}
	marketplaceName = strings.TrimSpace(marketplaceName)
	s.mu.Lock()
	for id, detail := range s.plugins {
		if detail.Summary.MarketplaceName == marketplaceName && strings.TrimSpace(detail.Summary.RemotePluginID) != "" {
			delete(s.plugins, id)
		}
	}
	s.mu.Unlock()
	for _, detail := range details {
		s.AddPlugin(detail)
	}
}

func (s *PluginService) List(params *PluginListParams) *PluginListResponse {
	if params == nil {
		params = &PluginListParams{}
	}
	s.mu.Lock()
	storedDetails := make([]PluginDetail, 0, len(s.plugins))
	for _, detail := range s.plugins {
		storedDetails = append(storedDetails, cloneDetail(detail))
	}
	marketplaces := s.marketplaceListLocked()
	target := s.targetCuratedMarketplace
	s.mu.Unlock()
	loadedDetails, loadErrors := loadMarketplacePlugins(marketplaces)
	details := routePluginDetails(mergePluginDetails(storedDetails, loadedDetails), target)
	plugins := make([]PluginSummary, 0, len(details))
	for _, detail := range details {
		if !params.IncludeInstalled && detail.Summary.Installed {
			continue
		}
		plugins = append(plugins, cloneSummary(detail.Summary))
	}
	sort.SliceStable(plugins, func(i int, j int) bool {
		return plugins[i].ID < plugins[j].ID
	})
	return &PluginListResponse{
		Marketplaces:          marketplaceEntries(marketplaces, plugins),
		MarketplaceLoadErrors: loadErrors,
		FeaturedPluginIDs:     []string{},
		Plugins:               plugins,
	}
}

func (s *PluginService) Installed(params *PluginInstalledParams) *PluginInstalledResponse {
	if params == nil {
		params = &PluginInstalledParams{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	plugins := make([]PluginSummary, 0)
	for _, detail := range s.plugins {
		if detail.Summary.Installed && pluginEligibleForCuratedTarget(detail.Summary.ID, detail.Summary.MarketplaceName, s.targetCuratedMarketplace) {
			plugins = append(plugins, cloneSummary(detail.Summary))
		}
	}
	for _, name := range params.InstallSuggestionPluginNames {
		plugins = append(plugins, PluginSummary{
			ID:                "suggestion:" + name,
			Name:              name,
			DisplayName:       name,
			Availability:      PluginAvailable,
			InstallPolicy:     InstallAllowed,
			AuthPolicy:        AuthNone,
			Source:            PluginSource{Type: "suggestion"},
			InstallSuggestion: true,
			Keywords:          []string{},
		})
	}
	plugins = routePluginSummaries(plugins, s.targetCuratedMarketplace)
	sort.SliceStable(plugins, func(i int, j int) bool {
		return plugins[i].ID < plugins[j].ID
	})
	return &PluginInstalledResponse{
		Marketplaces:          marketplaceEntries(s.marketplaceListLocked(), plugins),
		MarketplaceLoadErrors: []MarketplaceLoadErrorInfo{},
		Plugins:               plugins,
	}
}

// InstalledDetails returns deep copies of every installed plugin detail stored
// by the service, sorted by plugin ID. It is used by runtime reconciliation to
// diff bundle and enablement changes without leaking mutable plugin state.
func (s *PluginService) InstalledDetails() []PluginDetail {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	details := make([]PluginDetail, 0, len(s.plugins))
	for _, detail := range s.plugins {
		if detail.Summary.Installed && strings.TrimSpace(detail.Summary.ID) != "" {
			details = append(details, cloneDetail(detail))
		}
	}
	sort.SliceStable(details, func(i, j int) bool { return details[i].Summary.ID < details[j].Summary.ID })
	return details
}

func (s *PluginService) EnabledCapabilities() []CapabilitySummary {
	if s == nil {
		return nil
	}
	details := s.enabledPluginDetailsSnapshot()
	details = s.materializePluginDetailsBestEffort(details)
	out := make([]CapabilitySummary, 0, len(details))
	for _, detail := range details {
		out = append(out, capabilityFromDetail(&detail))
	}
	sort.SliceStable(out, func(i int, j int) bool {
		return out[i].ConfigName < out[j].ConfigName
	})
	return out
}

func (s *PluginService) materializePluginDetailsBestEffort(details []PluginDetail) []PluginDetail {
	out := make([]PluginDetail, 0, len(details))
	for _, detail := range details {
		out = append(out, s.materializePluginDetailBestEffort(detail))
	}
	return out
}

func (s *PluginService) materializePluginDetailBestEffort(detail PluginDetail) PluginDetail {
	if s == nil || !pluginDetailNeedsMarketplaceMaterialization(&detail) {
		return detail
	}
	materialized, err := s.materializeMarketplacePluginDetail(&detail)
	if err != nil || materialized == nil {
		return detail
	}
	refreshed := *materialized
	refreshed.Summary.Installed = detail.Summary.Installed
	refreshed.Summary.Enabled = detail.Summary.Enabled
	if refreshed.Summary.ID == "" {
		refreshed.Summary.ID = detail.Summary.ID
	}
	s.mu.Lock()
	if detail.Summary.ID != "" && detail.Summary.ID != refreshed.Summary.ID {
		delete(s.plugins, detail.Summary.ID)
	}
	if refreshed.Summary.ID != "" {
		s.plugins[refreshed.Summary.ID] = cloneDetail(refreshed)
	}
	s.mu.Unlock()
	return refreshed
}

func (s *PluginService) DiscoverableInstallCandidates() []DiscoverableInfo {
	return s.DiscoverableInstallCandidatesContext(context.Background())
}

func (s *PluginService) DiscoverableInstallCandidatesContext(ctx context.Context) []DiscoverableInfo {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	storedDetails := make([]PluginDetail, 0, len(s.plugins))
	for _, detail := range s.plugins {
		storedDetails = append(storedDetails, cloneDetail(detail))
	}
	marketplaces := s.marketplaceListLocked()
	target := s.targetCuratedMarketplace
	s.mu.Unlock()
	loadedDetails, _ := loadMarketplacePlugins(marketplaces)
	details := routePluginDetails(mergePluginDetails(storedDetails, loadedDetails), target)
	details = s.materializeInstalledDiscoverableDetailsBestEffort(details)
	if endpointCandidates, ok := s.suggestedDiscoverableCandidates(ctx, details); ok {
		return endpointCandidates
	}
	out := make([]DiscoverableInfo, 0, len(details))
	for _, detail := range details {
		summary := detail.Summary
		if summary.InstallPolicy == InstallBlocked || summary.ID == "" {
			continue
		}
		if summary.Installed {
			out = append(out, discoverableConnectorCandidatesFromDetail(&detail)...)
			continue
		}
		out = append(out, discoverablePluginCandidateFromDetail(&detail))
	}
	out = dedupeDiscoverableCandidates(out)
	sort.SliceStable(out, func(i int, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].ID < out[j].ID
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func (s *PluginService) materializeInstalledDiscoverableDetailsBestEffort(details []PluginDetail) []PluginDetail {
	out := make([]PluginDetail, 0, len(details))
	for _, detail := range details {
		if detail.Summary.Installed && detail.Summary.Enabled {
			detail = s.materializePluginDetailBestEffort(detail)
		}
		out = append(out, detail)
	}
	return out
}

func discoverablePluginCandidateFromDetail(detail *PluginDetail) DiscoverableInfo {
	if detail == nil {
		return DiscoverableInfo{}
	}
	summary := detail.Summary
	return DiscoverableInfo{
		ID:              summary.ID,
		RemotePluginID:  summary.RemotePluginID,
		Name:            firstNonEmpty(summary.DisplayName, summary.Name, summary.ID),
		Description:     summary.Description,
		ToolType:        "plugin",
		HasSkills:       summary.HasSkills,
		MCPServerNames:  append([]string(nil), summary.MCPServers...),
		AppConnectorIDs: append([]string(nil), summary.AppConnectors...),
	}
}

func discoverableConnectorCandidatesFromDetail(detail *PluginDetail) []DiscoverableInfo {
	if detail == nil || !detail.Summary.Enabled {
		return nil
	}
	pluginDisplayNames := discoverablePluginDisplayNames(detail)
	out := make([]DiscoverableInfo, 0, len(detail.Apps)+len(detail.AppTemplates))
	for _, app := range detail.Apps {
		id := strings.TrimSpace(app.ID)
		if id == "" {
			continue
		}
		out = append(out, DiscoverableInfo{
			ID:                 id,
			Name:               firstNonEmpty(app.DisplayName, app.Name, id),
			Description:        stringValuePtr(app.Description),
			ToolType:           "connector",
			InstallURL:         stringValuePtr(app.InstallURL),
			PluginDisplayNames: append([]string(nil), pluginDisplayNames...),
			AppConnectorIDs:    []string{id},
		})
	}
	for _, template := range detail.AppTemplates {
		out = append(out, discoverableConnectorCandidatesFromTemplate(template, pluginDisplayNames)...)
	}
	return out
}

func discoverablePluginDisplayNames(detail *PluginDetail) []string {
	if detail == nil {
		return nil
	}
	name := strings.TrimSpace(firstNonEmpty(detail.Summary.DisplayName, detail.Summary.Name, detail.Summary.ID))
	if name == "" {
		return nil
	}
	return []string{name}
}

func discoverableConnectorCandidatesFromTemplate(template AppTemplateSummary, pluginDisplayNames []string) []DiscoverableInfo {
	name := firstNonEmpty(template.DisplayName, template.Name, template.TemplateID, template.ID)
	description := stringValuePtr(template.Description)
	ids := make([]string, 0, 1+len(template.MaterializedAppIDs))
	if template.CanonicalConnectorID != nil {
		ids = append(ids, *template.CanonicalConnectorID)
	}
	ids = append(ids, template.MaterializedAppIDs...)
	out := make([]DiscoverableInfo, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		candidateName := firstNonEmpty(name, id)
		out = append(out, DiscoverableInfo{
			ID:                 id,
			Name:               candidateName,
			Description:        description,
			ToolType:           "connector",
			InstallURL:         pluginConnectorInstallURL(candidateName, id),
			PluginDisplayNames: append([]string(nil), pluginDisplayNames...),
			AppConnectorIDs:    []string{id},
		})
	}
	return out
}

func pluginConnectorInstallURL(name string, connectorID string) string {
	return "https://chatgpt.com/apps/" + pluginConnectorMentionSlugFromName(name) + "/" + strings.TrimSpace(connectorID)
}

func pluginConnectorMentionSlugFromName(name string) string {
	name = strings.TrimSpace(name)
	var builder strings.Builder
	builder.Grow(len(name))
	for _, ch := range name {
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' {
			if ch >= 'A' && ch <= 'Z' {
				ch += 'a' - 'A'
			}
			builder.WriteRune(ch)
			continue
		}
		builder.WriteByte('-')
	}
	slug := strings.Trim(builder.String(), "-")
	if slug == "" {
		return "app"
	}
	return slug
}

func dedupeDiscoverableCandidates(candidates []DiscoverableInfo) []DiscoverableInfo {
	seen := map[string]int{}
	out := make([]DiscoverableInfo, 0, len(candidates))
	for _, candidate := range candidates {
		key := strings.TrimSpace(candidate.ToolType) + "\x00" + strings.TrimSpace(candidate.ID)
		if strings.TrimSpace(candidate.ID) == "" {
			continue
		}
		if index, ok := seen[key]; ok {
			out[index] = mergeDiscoverableCandidate(out[index], candidate)
			continue
		}
		seen[key] = len(out)
		out = append(out, candidate)
	}
	return out
}

func mergeDiscoverableCandidate(existing DiscoverableInfo, candidate DiscoverableInfo) DiscoverableInfo {
	if strings.TrimSpace(existing.Name) == "" {
		existing.Name = candidate.Name
	}
	if strings.TrimSpace(existing.Description) == "" {
		existing.Description = candidate.Description
	}
	if strings.TrimSpace(existing.InstallURL) == "" {
		existing.InstallURL = candidate.InstallURL
	}
	if strings.TrimSpace(existing.RemotePluginID) == "" {
		existing.RemotePluginID = candidate.RemotePluginID
	}
	existing.HasSkills = existing.HasSkills || candidate.HasSkills
	existing.PluginDisplayNames = mergeDiscoverableStringSlices(existing.PluginDisplayNames, candidate.PluginDisplayNames)
	existing.MCPServerNames = mergeDiscoverableStringSlices(existing.MCPServerNames, candidate.MCPServerNames)
	existing.AppConnectorIDs = mergeDiscoverableStringSlices(existing.AppConnectorIDs, candidate.AppConnectorIDs)
	return existing
}

func mergeDiscoverableStringSlices(left []string, right []string) []string {
	out := make([]string, 0, len(left)+len(right))
	seen := map[string]bool{}
	for _, values := range [][]string{left, right} {
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" || seen[value] {
				continue
			}
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func (s *PluginService) Read(params *PluginReadParams) (*PluginReadResponse, error) {
	if params == nil {
		params = &PluginReadParams{}
	}
	s.mu.Lock()
	storedDetails := make([]PluginDetail, 0, len(s.plugins))
	for _, detail := range s.plugins {
		storedDetails = append(storedDetails, cloneDetail(detail))
	}
	marketplaces := s.marketplaceListLocked()
	target := s.targetCuratedMarketplace
	s.mu.Unlock()
	loadedDetails, _ := loadMarketplacePlugins(marketplaces)
	for _, detail := range routePluginDetails(mergePluginDetails(storedDetails, loadedDetails), target) {
		if response := readPluginDetailResponse(detail, params); response != nil {
			materialized, err := s.materializeMarketplacePluginDetail(&response.Plugin)
			if err != nil {
				return nil, err
			}
			response.Plugin = *materialized
			return response, nil
		}
	}
	return nil, fmt.Errorf("plugin not found")
}

func (s *PluginService) ReadSkill(params *PluginSkillReadParams) (*PluginSkillReadResponse, error) {
	if params == nil {
		params = &PluginSkillReadParams{}
	}
	normalized := *params
	normalized.RemoteMarketplaceName = strings.TrimSpace(normalized.RemoteMarketplaceName)
	normalized.RemotePluginID = strings.TrimSpace(normalized.RemotePluginID)
	normalized.SkillName = strings.TrimSpace(normalized.SkillName)
	if normalized.RemotePluginID == "" || normalized.SkillName == "" {
		return nil, fmt.Errorf("%w: remotePluginId and skillName are required", ErrInvalidPluginRequest)
	}
	if contents, ok, err := s.readInstalledPluginSkill(&normalized); err != nil {
		return nil, err
	} else if ok {
		return &PluginSkillReadResponse{Contents: &contents, Markdown: contents}, nil
	}
	contents := "# " + normalized.SkillName + "\n"
	return &PluginSkillReadResponse{Contents: &contents, Markdown: contents}, nil
}

func (s *PluginService) readInstalledPluginSkill(params *PluginSkillReadParams) (string, bool, error) {
	if s == nil || params == nil {
		return "", false, nil
	}
	s.mu.Lock()
	storedDetails := make([]PluginDetail, 0, len(s.plugins))
	for _, detail := range s.plugins {
		storedDetails = append(storedDetails, cloneDetail(detail))
	}
	marketplaces := s.marketplaceListLocked()
	target := s.targetCuratedMarketplace
	s.mu.Unlock()
	loadedDetails, _ := loadMarketplacePlugins(marketplaces)
	for _, detail := range routePluginDetails(mergePluginDetails(storedDetails, loadedDetails), target) {
		if !pluginDetailMatchesSkillRead(&detail, params) {
			continue
		}
		root := pluginRootForSkillRead(&detail)
		if root != "" {
			if contents, ok := readPluginSkillFile(root, params.SkillName); ok {
				return contents, true, nil
			}
		}
		if !pluginDetailNeedsMarketplaceMaterialization(&detail) {
			continue
		}
		materialized, err := s.materializeMarketplacePluginDetail(&detail)
		if err != nil {
			return "", false, err
		}
		root = pluginRootForSkillRead(materialized)
		if root == "" {
			continue
		}
		if contents, ok := readPluginSkillFile(root, params.SkillName); ok {
			return contents, true, nil
		}
	}
	return "", false, nil
}

func pluginDetailMatchesSkillRead(detail *PluginDetail, params *PluginSkillReadParams) bool {
	if detail == nil || params == nil {
		return false
	}
	marketplace := strings.TrimSpace(params.RemoteMarketplaceName)
	if marketplace != "" && detail.Summary.MarketplaceName != marketplace {
		return false
	}
	remoteID := strings.TrimSpace(params.RemotePluginID)
	if remoteID == "" {
		return false
	}
	if detail.Summary.RemotePluginID == remoteID || detail.Summary.ID == remoteID {
		return true
	}
	return marketplace != "" && detail.Summary.Name == remoteID
}

func pluginRootForSkillRead(detail *PluginDetail) string {
	if detail == nil {
		return ""
	}
	if root := pluginRootFromManifestPath(detail.ManifestPath); root != "" {
		return root
	}
	sourcePath := strings.TrimSpace(detail.Summary.Source.Path)
	if sourcePath == "" {
		return ""
	}
	if detail.Summary.Source.Type == "local" || filepath.IsAbs(sourcePath) {
		return filepath.Clean(sourcePath)
	}
	return ""
}

func pluginExecutionRoot(detail *PluginDetail) string {
	if detail == nil {
		return ""
	}
	if root := pluginRootFromManifestPath(detail.ManifestPath); root != "" {
		return root
	}
	sourcePath := strings.TrimSpace(detail.Summary.Source.Path)
	if sourcePath != "" && (detail.Summary.Source.Type == "local" || filepath.IsAbs(sourcePath)) {
		return filepath.Clean(sourcePath)
	}
	return strings.TrimSpace(detail.MarketplaceRoot)
}

func readPluginSkillFile(pluginRoot string, skillName string) (string, bool) {
	pluginRoot = strings.TrimSpace(pluginRoot)
	skillName = strings.TrimSpace(skillName)
	if pluginRoot == "" || skillName == "" {
		return "", false
	}
	skillsRoot := filepath.Join(pluginRoot, "skills")
	candidates := []string{
		filepath.Join(skillsRoot, skillName, "SKILL.md"),
		filepath.Join(skillsRoot, "SKILL.md"),
	}
	for _, candidate := range candidates {
		if contents, ok := readPluginSkillCandidate(candidate, skillName); ok {
			return contents, true
		}
	}
	var found string
	_ = filepath.WalkDir(skillsRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil || found != "" {
			return err
		}
		if d.IsDir() {
			if path != skillsRoot && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "SKILL.md" {
			return nil
		}
		if contents, ok := readPluginSkillCandidate(path, skillName); ok {
			found = contents
			return filepath.SkipAll
		}
		return nil
	})
	return found, found != ""
}

func readPluginSkillCandidate(path string, skillName string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	if filepath.Base(filepath.Dir(path)) == skillName || pluginSkillFrontmatterName(string(data)) == skillName {
		return string(data), true
	}
	return "", false
}

func pluginSkillFrontmatterName(contents string) string {
	contents = strings.ReplaceAll(strings.ReplaceAll(strings.TrimPrefix(contents, "\ufeff"), "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(contents, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return ""
	}
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			return ""
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(key) != "name" {
			continue
		}
		return strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return ""
}

func (s *PluginService) EnabledHookSources() []HookSource {
	if s == nil {
		return nil
	}
	details := s.enabledPluginDetailsSnapshot()
	details = s.materializePluginDetailsBestEffort(details)
	sources := make([]HookSource, 0, len(details))
	for _, detail := range details {
		pluginRoot := pluginExecutionRoot(&detail)
		if pluginRoot == "" {
			continue
		}
		sourcePath := filepath.Join(pluginRoot, "hooks", "hooks.json")
		if detail.Hooks != nil && len(detail.Hooks) == 0 {
			continue
		}
		if detail.Summary.ID == "" {
			continue
		}
		sources = append(sources, HookSource{
			PluginID:           detail.Summary.ID,
			PluginRoot:         pluginRoot,
			PluginDataRoot:     filepath.Join(pluginRoot, "data"),
			SourcePath:         sourcePath,
			SourceRelativePath: filepath.ToSlash(filepath.Join("hooks", "hooks.json")),
		})
	}
	sort.SliceStable(sources, func(i int, j int) bool {
		return sources[i].PluginID < sources[j].PluginID
	})
	return sources
}

func (s *PluginService) EnabledSkillRoots() []EnabledSkillRoot {
	if s == nil {
		return nil
	}
	details := s.enabledPluginDetailsSnapshot()
	details = s.materializePluginDetailsBestEffort(details)
	roots := make([]EnabledSkillRoot, 0, len(details))
	for _, detail := range details {
		if !detail.Summary.HasSkills && len(detail.Skills) == 0 {
			continue
		}
		pluginRoot := pluginExecutionRoot(&detail)
		if pluginRoot == "" || detail.Summary.ID == "" {
			continue
		}
		roots = append(roots, EnabledSkillRoot{
			PluginID:        detail.Summary.ID,
			RemotePluginID:  detail.Summary.RemotePluginID,
			PluginNamespace: firstNonEmpty(detail.Summary.Name, detail.Summary.ID),
			Root:            filepath.Join(pluginRoot, "skills"),
		})
	}
	sort.SliceStable(roots, func(i int, j int) bool {
		return roots[i].PluginID < roots[j].PluginID
	})
	return roots
}

func (s *PluginService) Install(params *PluginInstallParams) (*PluginInstallResponse, error) {
	if params == nil {
		params = &PluginInstallParams{}
	}
	id := strings.TrimSpace(params.PluginID)
	if id == "" {
		if strings.TrimSpace(params.PluginName) == "" {
			return nil, fmt.Errorf("%w: plugin id or plugin name is required", ErrInvalidPluginRequest)
		}
		localSource := strings.TrimSpace(params.MarketplaceName) != "" || strings.TrimSpace(params.MarketplacePath) != ""
		remoteSource := strings.TrimSpace(params.RemoteMarketplaceName) != ""
		if localSource == remoteSource {
			return nil, fmt.Errorf("%w: plugin/install requires exactly one of marketplacePath or remoteMarketplaceName", ErrInvalidPluginRequest)
		}
		if remoteSource {
			id = pluginID(params.PluginName, params.RemoteMarketplaceName)
		} else {
			id = pluginID(params.PluginName, params.MarketplaceName)
		}
	}
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("%w: plugin id or plugin name is required", ErrInvalidPluginRequest)
	}
	s.mu.Lock()
	if detail, ok := s.plugins[id]; ok {
		if !s.pluginEligibleForRuntimeLocked(detail) {
			s.mu.Unlock()
			return nil, fmt.Errorf("%w: plugin %q is not available for the active authentication mode", ErrInvalidPluginRequest, id)
		}
		configPath := s.marketplaceConfigPath
		s.mu.Unlock()
		if detail.Summary.InstallPolicy == InstallBlocked {
			return nil, fmt.Errorf("%w: plugin %q is not available for install", ErrInvalidPluginRequest, id)
		}
		if pluginDetailNeedsMarketplaceMaterialization(&detail) {
			materialized, err := s.materializeMarketplacePluginDetail(&detail)
			if err != nil {
				return nil, err
			}
			detail = *materialized
			if detail.Summary.InstallPolicy == InstallBlocked {
				return nil, fmt.Errorf("%w: plugin %q is not available for install", ErrInvalidPluginRequest, detail.Summary.ID)
			}
		}
		detail.Summary.Installed = true
		detail.Summary.Enabled = true
		if detail.Summary.ID == "" {
			detail.Summary.ID = id
		}
		if err := recordInstalledPluginConfig(configPath, &detail); err != nil {
			return nil, err
		}
		s.mu.Lock()
		if id != detail.Summary.ID {
			delete(s.plugins, id)
		}
		s.plugins[detail.Summary.ID] = detail
		s.mu.Unlock()
		return &PluginInstallResponse{PluginID: detail.Summary.ID, AuthPolicy: detail.Summary.AuthPolicy, AppsNeedingAuth: appsNeedingAuthForInstall(&detail)}, nil
	}
	marketplaces := s.marketplaceListLocked()
	target := s.targetCuratedMarketplace
	s.mu.Unlock()
	loadedDetails, _ := loadMarketplacePlugins(marketplaces)
	for _, detail := range routePluginDetails(loadedDetails, target) {
		if !pluginInstallRequestMatchesDetail(params, id, &detail) {
			continue
		}
		materialized, err := s.materializeMarketplacePluginDetail(&detail)
		if err != nil {
			return nil, err
		}
		detail = *materialized
		if detail.Summary.InstallPolicy == InstallBlocked {
			return nil, fmt.Errorf("%w: plugin %q is not available for install", ErrInvalidPluginRequest, detail.Summary.ID)
		}
		detail.Summary.Installed = true
		detail.Summary.Enabled = true
		if detail.Summary.ID == "" {
			detail.Summary.ID = pluginID(detail.Summary.Name, detail.Summary.MarketplaceName)
		}
		s.mu.Lock()
		configPath := s.marketplaceConfigPath
		s.mu.Unlock()
		if err := recordInstalledPluginConfig(configPath, &detail); err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.plugins[detail.Summary.ID] = cloneDetail(detail)
		s.mu.Unlock()
		return &PluginInstallResponse{PluginID: detail.Summary.ID, AuthPolicy: detail.Summary.AuthPolicy, AppsNeedingAuth: appsNeedingAuthForInstall(&detail)}, nil
	}
	return nil, fmt.Errorf("%w: plugin %q not found", ErrInvalidPluginRequest, id)
}

// HydrateRecommendedPluginMetadata mirrors Rust #39143: fetch the selected
// recommended plugin's details before presenting an install request, using
// them to verify availability and populate connector metadata. It returns
// (detail, true) when the plugin is available, (nil, false) when the plugin is
// no longer available, and an error when its metadata cannot be verified.
func (s *PluginService) HydrateRecommendedPluginMetadata(remotePluginID string) (*PluginDetail, bool, error) {
	remotePluginID = strings.TrimSpace(remotePluginID)
	if remotePluginID == "" {
		return nil, false, fmt.Errorf("%w: remote plugin id is required", ErrInvalidPluginRequest)
	}
	if s == nil {
		return nil, false, fmt.Errorf("%w: plugin service is nil", ErrInvalidPluginRequest)
	}
	s.mu.Lock()
	var found *PluginDetail
	for _, detail := range s.plugins {
		if strings.TrimSpace(detail.Summary.RemotePluginID) == remotePluginID ||
			strings.TrimSpace(detail.Summary.ID) == remotePluginID ||
			strings.TrimSpace(detail.Summary.Name) == remotePluginID {
			copyDetail := cloneDetail(detail)
			found = &copyDetail
			break
		}
	}
	if found != nil {
		s.mu.Unlock()
		materialized, err := s.materializeMarketplacePluginDetail(found)
		if err != nil {
			return nil, false, err
		}
		if materialized == nil || materialized.Summary.InstallPolicy == InstallBlocked {
			return nil, false, nil
		}
		return materialized, true, nil
	}
	marketplaces := s.marketplaceListLocked()
	target := s.targetCuratedMarketplace
	s.mu.Unlock()
	loadedDetails, _ := loadMarketplacePlugins(marketplaces)
	for _, detail := range routePluginDetails(loadedDetails, target) {
		if strings.TrimSpace(detail.Summary.RemotePluginID) != remotePluginID &&
			strings.TrimSpace(detail.Summary.ID) != remotePluginID &&
			strings.TrimSpace(detail.Summary.Name) != remotePluginID {
			continue
		}
		materialized, err := s.materializeMarketplacePluginDetail(&detail)
		if err != nil {
			return nil, false, err
		}
		if materialized == nil {
			return nil, false, nil
		}
		if materialized.Summary.InstallPolicy == InstallBlocked {
			return nil, false, nil
		}
		return materialized, true, nil
	}
	return nil, false, nil
}

func (s *PluginService) materializeMarketplacePluginDetail(detail *PluginDetail) (*PluginDetail, error) {
	return s.materializeMarketplacePluginDetailWithOptions(detail, false)
}

func (s *PluginService) materializeMarketplacePluginDetailForUpgrade(detail *PluginDetail) (*PluginDetail, error) {
	return s.materializeMarketplacePluginDetailWithOptions(detail, true)
}

func (s *PluginService) materializeMarketplacePluginDetailWithOptions(detail *PluginDetail, force bool) (*PluginDetail, error) {
	if detail == nil {
		return nil, fmt.Errorf("%w: plugin detail is required", ErrInvalidPluginRequest)
	}
	if !pluginDetailNeedsMarketplaceMaterialization(detail) {
		cloned := cloneDetail(*detail)
		return &cloned, nil
	}
	source, pluginSubdir, err := parsedMarketplacePluginSource(&detail.Summary.Source)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	installRoot := s.marketplaceInstallRoot
	materializer := s.marketplacePluginMaterializer
	s.mu.Unlock()
	if materializer == nil {
		return nil, fmt.Errorf("%w: marketplace plugin materializer is not configured", ErrInvalidPluginRequest)
	}
	destination := marketplacePluginInstallDestination(installRoot, detail.Summary.MarketplaceName, detail.Summary.Name)
	pluginSubdir = marketplacePluginSubdirForMaterialization(pluginSubdir, destination)
	pluginRoot := materializedMarketplacePluginRoot(destination, pluginSubdir)
	plugin := marketplaceManifestPlugin{
		Name:         detail.Summary.Name,
		Source:       marketplacePluginSourceFromSummary(&detail.Summary.Source, pluginSubdir),
		Interface:    clonePluginInterfacePtr(detail.Summary.Interface),
		Keywords:     append([]string(nil), detail.Summary.Keywords...),
		Apps:         cloneAppSummaries(detail.Apps),
		AppTemplates: cloneAppTemplateSummaries(detail.AppTemplates),
	}
	detailFromManifest := func(manifest *pluginManifestFile) PluginDetail {
		marketplacePath := ""
		if detail.MarketplacePath != nil {
			marketplacePath = *detail.MarketplacePath
		}
		materialized := marketplacePluginDetailFromManifest(
			detail.Summary.Name,
			detail.Summary.MarketplaceName,
			detail.MarketplaceRoot,
			marketplacePath,
			pluginRoot,
			plugin,
			manifest,
		)
		materialized.Summary.Source.Path = pluginRoot
		materialized.Summary.Source.Type = detail.Summary.Source.Type
		materialized.Summary.Source.URL = detail.Summary.Source.URL
		materialized.Summary.Source.RefName = cloneStringPtr(detail.Summary.Source.RefName)
		materialized.Summary.Source.SHA = cloneStringPtr(detail.Summary.Source.SHA)
		return materialized
	}
	if !force {
		if manifest := readPluginManifestForRoot(pluginRoot); manifest != nil {
			materialized := detailFromManifest(manifest)
			return &materialized, nil
		}
	}
	if force {
		if err := os.RemoveAll(destination); err != nil {
			return nil, fmt.Errorf("failed to refresh materialized marketplace plugin %s: %w", destination, err)
		}
	}
	if err := materializer.MaterializeMarketplacePlugin(source, destination); err != nil {
		return nil, err
	}
	manifest := readPluginManifestForRoot(pluginRoot)
	if manifest == nil {
		return nil, fmt.Errorf("%w: materialized marketplace plugin %q has no supported plugin manifest", ErrInvalidPluginRequest, detail.Summary.ID)
	}
	materialized := detailFromManifest(manifest)
	return &materialized, nil
}

func parsedMarketplacePluginSource(source *PluginSource) (*ParsedMarketplaceSource, string, error) {
	if source == nil {
		return nil, "", fmt.Errorf("%w: marketplace plugin source is required", ErrInvalidPluginRequest)
	}
	sourceType := strings.TrimSpace(source.Type)
	if sourceType == "" || sourceType == "local" {
		return nil, "", fmt.Errorf("%w: marketplace plugin source must be remote", ErrInvalidPluginRequest)
	}
	refName := cloneStringPtr(source.RefName)
	if refName == nil {
		refName = cloneStringPtr(source.SHA)
	}
	parsed, err := ParseMarketplaceSource(source.URL, refName)
	if err != nil {
		return nil, "", fmt.Errorf("%w: invalid marketplace plugin source: %v", ErrInvalidPluginRequest, err)
	}
	if parsed.Kind != MarketplaceSourceGit {
		return nil, "", fmt.Errorf("%w: marketplace plugin source %q is not supported", ErrInvalidPluginRequest, sourceType)
	}
	return parsed, strings.TrimSpace(source.Path), nil
}

func marketplacePluginSourceFromSummary(source *PluginSource, pluginSubdir string) marketplacePluginSource {
	if source == nil {
		return marketplacePluginSource{}
	}
	return marketplacePluginSource{
		Source: strings.TrimSpace(source.Type),
		URL:    strings.TrimSpace(source.URL),
		Ref:    cloneStringPtr(source.RefName),
		SHA:    cloneStringPtr(source.SHA),
		Path:   strings.TrimSpace(pluginSubdir),
	}
}

func marketplacePluginInstallDestination(installRoot string, marketplaceName string, pluginName string) string {
	return filepath.Join(
		strings.TrimSpace(installRoot),
		InstalledMarketplacePluginsDir,
		sanitize(marketplaceName),
		sanitize(pluginName),
	)
}

func materializedMarketplacePluginRoot(destination string, pluginSubdir string) string {
	pluginSubdir = strings.TrimSpace(pluginSubdir)
	if pluginSubdir == "" {
		return filepath.Clean(destination)
	}
	return resolveMarketplacePluginPath(destination, pluginSubdir)
}

func marketplacePluginSubdirForMaterialization(pluginSubdir string, destination string) string {
	pluginSubdir = strings.TrimSpace(pluginSubdir)
	if pluginSubdir == "" || !filepath.IsAbs(pluginSubdir) {
		return pluginSubdir
	}
	destination = filepath.Clean(strings.TrimSpace(destination))
	pluginRoot := filepath.Clean(pluginSubdir)
	rel, err := filepath.Rel(destination, pluginRoot)
	if err != nil || rel == "" || filepath.IsAbs(rel) {
		return pluginSubdir
	}
	if rel == "." {
		return ""
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return pluginSubdir
	}
	return rel
}

func (s *PluginService) Uninstall(params *PluginUninstallParams) (*PluginUninstallResponse, error) {
	if params == nil {
		params = &PluginUninstallParams{}
	}
	pluginID := strings.TrimSpace(params.PluginID)
	if pluginID == "" {
		return nil, fmt.Errorf("%w: pluginId is required", ErrInvalidPluginRequest)
	}
	s.mu.Lock()
	configPath := s.marketplaceConfigPath
	installRoot := s.marketplaceInstallRoot
	detail, detailOK := s.plugins[pluginID]
	materializedRoot := ""
	if detailOK {
		materializedRoot = materializedMarketplacePluginDestinationForUninstall(installRoot, &detail)
	}
	s.mu.Unlock()
	if err := removeInstalledPluginConfig(configPath, pluginID); err != nil {
		return nil, err
	}
	if materializedRoot != "" {
		if err := os.RemoveAll(materializedRoot); err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if detail, ok := s.plugins[pluginID]; ok {
		detail.Summary.Installed = false
		detail.Summary.Enabled = false
		s.plugins[pluginID] = detail
	}
	return &PluginUninstallResponse{}, nil
}

func materializedMarketplacePluginDestinationForUninstall(installRoot string, detail *PluginDetail) string {
	if detail == nil {
		return ""
	}
	sourceType := strings.TrimSpace(detail.Summary.Source.Type)
	if sourceType == "" || sourceType == "local" {
		return ""
	}
	marketplaceName := strings.TrimSpace(detail.Summary.MarketplaceName)
	pluginName := strings.TrimSpace(detail.Summary.Name)
	if strings.TrimSpace(installRoot) == "" || marketplaceName == "" || pluginName == "" {
		return ""
	}
	return marketplacePluginInstallDestination(installRoot, marketplaceName, pluginName)
}

func appsNeedingAuthForInstall(detail *PluginDetail) []AppSummary {
	if detail == nil || detail.Summary.AuthPolicy != AuthOnInstall {
		return []AppSummary{}
	}
	return cloneAppSummaries(detail.Apps)
}

func (s *PluginService) SaveShare(params *PluginShareSaveParams) (*PluginShareSaveResponse, error) {
	if params == nil {
		params = &PluginShareSaveParams{}
	}
	remoteID := strings.TrimSpace(params.RemotePluginID)
	if remoteID == "" {
		remoteID = sanitize(params.PluginPath)
	}
	if remoteID == "" {
		return nil, fmt.Errorf("%w: remotePluginId or pluginPath is required", ErrInvalidPluginRequest)
	}
	targets := params.ShareTargets
	if len(targets) == 0 {
		targets = params.Targets
	}
	targets = normalizePluginSharePrincipals(targets)
	discoverability := strings.TrimSpace(params.Discoverability)
	normalizedParams := &PluginShareSaveParams{
		PluginPath:      strings.TrimSpace(params.PluginPath),
		RemotePluginID:  remoteID,
		Discoverability: discoverability,
		ShareTargets:    append([]PluginSharePrincipal(nil), targets...),
	}
	s.mu.Lock()
	backend := s.shareBackend
	s.mu.Unlock()
	response := &PluginShareSaveResponse{
		RemotePluginID: remoteID,
		ShareURL:       "https://chatgpt.com/codex/plugins/" + remoteID,
	}
	if backend != nil {
		remoteResponse, err := backend.SaveShare(normalizedParams)
		if err != nil {
			return nil, err
		}
		if remoteResponse == nil {
			return nil, fmt.Errorf("%w: plugin share backend returned an empty response", ErrInvalidPluginRequest)
		}
		response = &PluginShareSaveResponse{
			RemotePluginID:        firstNonEmpty(strings.TrimSpace(remoteResponse.RemotePluginID), remoteID),
			ShareURL:              strings.TrimSpace(remoteResponse.ShareURL),
			CanPublishToWorkspace: cloneBoolPtr(remoteResponse.CanPublishToWorkspace),
		}
		remoteID = response.RemotePluginID
	}
	shareURL := response.ShareURL
	s.mu.Lock()
	defer s.mu.Unlock()
	s.shares[remoteID] = PluginShareContext{
		RemotePluginID:        remoteID,
		Discoverability:       stringPtrIfNotEmpty(discoverability),
		ShareURL:              stringPtrIfNotEmpty(shareURL),
		SharePrincipals:       append([]PluginSharePrincipal(nil), targets...),
		Principals:            append([]PluginSharePrincipal(nil), targets...),
		CanPublishToWorkspace: cloneBoolPtr(response.CanPublishToWorkspace),
	}
	return response, nil
}

func (s *PluginService) UpdateShareTargets(params *PluginShareUpdateTargetsParams) (*PluginShareUpdateTargetsResponse, error) {
	if params == nil {
		params = &PluginShareUpdateTargetsParams{}
	}
	remoteID := strings.TrimSpace(params.RemotePluginID)
	saveParams := PluginShareSaveParams{
		RemotePluginID:  remoteID,
		Discoverability: params.Discoverability,
		Targets:         append([]PluginSharePrincipal(nil), params.Targets...),
		ShareTargets:    append([]PluginSharePrincipal(nil), params.ShareTargets...),
	}
	if _, err := s.SaveShare(&saveParams); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	share := s.shares[remoteID]
	discoverability := ""
	if share.Discoverability != nil {
		discoverability = *share.Discoverability
	}
	return &PluginShareUpdateTargetsResponse{Discoverability: discoverability, Principals: append([]PluginSharePrincipal(nil), share.SharePrincipals...)}, nil
}

func (s *PluginService) ListShares(params *PluginShareListParams) *PluginShareListResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	contexts := make([]PluginShareContext, 0, len(s.shares))
	items := make([]PluginShareListItem, 0, len(s.shares))
	for _, share := range s.shares {
		cloned := cloneShare(share)
		contexts = append(contexts, cloned)
		items = append(items, PluginShareListItem{
			Plugin: PluginSummary{
				ID:            cloned.RemotePluginID,
				Name:          cloned.RemotePluginID,
				DisplayName:   cloned.RemotePluginID,
				Availability:  PluginAvailable,
				InstallPolicy: InstallAllowed,
				AuthPolicy:    AuthNone,
				ShareContext:  &cloned,
				Source:        PluginSource{Type: "remote"},
				Enabled:       true,
				Keywords:      []string{},
			},
		})
	}
	sort.SliceStable(contexts, func(i int, j int) bool {
		return contexts[i].RemotePluginID < contexts[j].RemotePluginID
	})
	sort.SliceStable(items, func(i int, j int) bool {
		return items[i].Plugin.ID < items[j].Plugin.ID
	})
	return &PluginShareListResponse{Data: items, Items: contexts}
}

func (s *PluginService) CheckoutShare(params *PluginShareCheckoutParams) (*PluginShareCheckoutResponse, error) {
	if params == nil {
		params = &PluginShareCheckoutParams{}
	}
	remoteID := strings.TrimSpace(params.RemotePluginID)
	if remoteID == "" {
		return nil, fmt.Errorf("%w: remotePluginId is required", ErrInvalidPluginRequest)
	}
	path := "remote-plugins/" + sanitize(remoteID)
	return &PluginShareCheckoutResponse{
		RemotePluginID:  remoteID,
		PluginID:        remoteID,
		PluginName:      remoteID,
		PluginPath:      path,
		MarketplaceName: "remote",
		MarketplacePath: "remote",
		LocalPath:       path,
	}, nil
}

func (s *PluginService) DeleteShare(params *PluginShareDeleteParams) (*PluginShareDeleteResponse, error) {
	if params == nil {
		params = &PluginShareDeleteParams{}
	}
	remoteID := strings.TrimSpace(params.RemotePluginID)
	if remoteID == "" {
		return nil, fmt.Errorf("%w: remotePluginId is required", ErrInvalidPluginRequest)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.shares, remoteID)
	return &PluginShareDeleteResponse{}, nil
}

func (s *PluginService) marketplaceListLocked() []Marketplace {
	marketplaces := make([]Marketplace, 0, len(s.marketplaces))
	for _, marketplace := range s.marketplaces {
		marketplaces = append(marketplaces, marketplace)
	}
	curated := s.implicitCuratedMarketplaceLocked()
	marketplaces = appendImplicitCuratedMarketplace(marketplaces, curated)
	marketplaces = filterReservedMarketplaceSpoofs(marketplaces, curated)
	marketplaces = routeMarketplaces(marketplaces, s.targetCuratedMarketplace)
	sort.SliceStable(marketplaces, func(i int, j int) bool {
		return marketplaces[i].Name < marketplaces[j].Name
	})
	return marketplaces
}

// filterReservedMarketplaceSpoofs drops marketplaces that claim names reserved
// for managed/remote marketplaces unless they live at the expected managed
// path (Rust #39165). The implicit curated marketplace is appended at the
// managed path before this filter runs, so it survives; user-configured or
// repository spoofs at other paths are rejected.
func filterReservedMarketplaceSpoofs(marketplaces []Marketplace, curated *Marketplace) []Marketplace {
	if len(marketplaces) == 0 {
		return marketplaces
	}
	var managedPath string
	if curated != nil {
		managedPath = filepath.Clean(curated.RootPath)
	}
	out := make([]Marketplace, 0, len(marketplaces))
	for _, marketplace := range marketplaces {
		if IsReservedMarketplaceName(marketplace.Name) &&
			marketplace.Name != OpenAIRemoteCuratedMarketplaceName &&
			(managedPath == "" || filepath.Clean(marketplace.RootPath) != managedPath) {
			continue
		}
		out = append(out, marketplace)
	}
	return out
}

func marketplaceEntries(marketplaces []Marketplace, plugins []PluginSummary) []PluginMarketplaceEntry {
	if len(marketplaces) == 0 {
		return []PluginMarketplaceEntry{{Name: "", Plugins: append([]PluginSummary(nil), plugins...)}}
	}
	byMarketplace := map[string][]PluginSummary{}
	for _, summary := range plugins {
		byMarketplace[summary.MarketplaceName] = append(byMarketplace[summary.MarketplaceName], cloneSummary(summary))
	}
	entries := make([]PluginMarketplaceEntry, 0, len(marketplaces)+len(byMarketplace))
	seen := map[string]bool{}
	for _, marketplace := range marketplaces {
		path := marketplace.RootPath
		seen[marketplace.Name] = true
		entries = append(entries, PluginMarketplaceEntry{
			Name:    marketplace.Name,
			Path:    &path,
			Plugins: byMarketplace[marketplace.Name],
		})
	}
	for marketplaceName, summaries := range byMarketplace {
		if marketplaceName == "" || seen[marketplaceName] {
			continue
		}
		entries = append(entries, PluginMarketplaceEntry{Name: marketplaceName, Plugins: summaries})
	}
	sort.SliceStable(entries, func(i int, j int) bool { return entries[i].Name < entries[j].Name })
	return entries
}

func mergePluginDetails(primary []PluginDetail, discovered []PluginDetail) []PluginDetail {
	byID := map[string]PluginDetail{}
	for _, detail := range discovered {
		if detail.Summary.ID == "" {
			continue
		}
		byID[detail.Summary.ID] = cloneDetail(detail)
	}
	for _, detail := range primary {
		if detail.Summary.ID == "" {
			continue
		}
		if catalogDetail, ok := byID[detail.Summary.ID]; ok {
			byID[detail.Summary.ID] = mergeConfiguredPluginDetail(catalogDetail, detail)
		} else {
			byID[detail.Summary.ID] = cloneDetail(detail)
		}
	}
	out := make([]PluginDetail, 0, len(byID))
	for _, detail := range byID {
		out = append(out, detail)
	}
	sort.SliceStable(out, func(i int, j int) bool {
		return out[i].Summary.ID < out[j].Summary.ID
	})
	return out
}

func mergeConfiguredPluginDetail(catalog PluginDetail, configured PluginDetail) PluginDetail {
	merged := cloneDetail(catalog)
	merged.Summary.Installed = configured.Summary.Installed
	merged.Summary.Enabled = configured.Summary.Enabled
	if configured.Summary.RemotePluginID != "" {
		merged.Summary.RemotePluginID = configured.Summary.RemotePluginID
	}
	if configured.Summary.Source.Path != "" || configured.ManifestPath != "" {
		merged = cloneDetail(configured)
	}
	return merged
}

func readPluginDetailResponse(detail PluginDetail, params *PluginReadParams) *PluginReadResponse {
	pluginName := strings.TrimSpace(params.PluginName)
	remotePluginID := strings.TrimSpace(params.RemotePluginID)
	remoteMarketplaceName := strings.TrimSpace(params.RemoteMarketplaceName)
	marketplaceName := strings.TrimSpace(params.MarketplaceName)
	marketplacePath := strings.TrimSpace(params.MarketplacePath)
	if pluginName != "" && detail.Summary.Name != pluginName {
		return nil
	}
	if remotePluginID != "" && detail.Summary.RemotePluginID != remotePluginID && detail.Summary.ID != remotePluginID {
		return nil
	}
	marketplaceName = firstNonEmpty(remoteMarketplaceName, marketplaceName)
	if marketplaceName != "" && detail.Summary.MarketplaceName != marketplaceName {
		return nil
	}
	if marketplacePath != "" && !pluginDetailMatchesMarketplacePath(&detail, marketplacePath) {
		return nil
	}
	cloned := cloneDetail(detail)
	if cloned.MarketplaceName == "" {
		cloned.MarketplaceName = cloned.Summary.MarketplaceName
	}
	if cloned.MarketplacePath == nil && marketplacePath != "" {
		value := marketplacePath
		cloned.MarketplacePath = &value
	}
	return &PluginReadResponse{Plugin: cloned}
}

func pluginInstallRequestMatchesDetail(params *PluginInstallParams, id string, detail *PluginDetail) bool {
	if detail == nil {
		return false
	}
	if strings.TrimSpace(params.PluginID) != "" {
		return detail.Summary.ID == strings.TrimSpace(params.PluginID)
	}
	if strings.TrimSpace(params.PluginName) != "" && detail.Summary.Name != strings.TrimSpace(params.PluginName) {
		return false
	}
	if strings.TrimSpace(params.RemoteMarketplaceName) != "" && detail.Summary.MarketplaceName != strings.TrimSpace(params.RemoteMarketplaceName) {
		return false
	}
	if strings.TrimSpace(params.MarketplaceName) != "" && detail.Summary.MarketplaceName != strings.TrimSpace(params.MarketplaceName) {
		return false
	}
	if strings.TrimSpace(params.MarketplacePath) != "" {
		return pluginDetailMatchesMarketplacePath(detail, params.MarketplacePath)
	}
	if strings.TrimSpace(id) != "" {
		return detail.Summary.ID == strings.TrimSpace(id)
	}
	return false
}

func pluginDetailMatchesMarketplacePath(detail *PluginDetail, value string) bool {
	value = cleanPluginPathForCompare(value)
	if detail == nil || value == "" {
		return false
	}
	candidates := []string{
		detail.MarketplaceRoot,
	}
	if detail.MarketplacePath != nil {
		candidates = append(candidates, *detail.MarketplacePath, filepath.Dir(*detail.MarketplacePath))
	}
	if detail.ManifestPath != "" {
		pluginRoot := pluginRootFromManifestPath(detail.ManifestPath)
		candidates = append(candidates, pluginRoot, filepath.Dir(pluginRoot))
	}
	for _, candidate := range candidates {
		candidate = cleanPluginPathForCompare(candidate)
		if candidate != "" && (candidate == value || strings.EqualFold(candidate, value)) {
			return true
		}
	}
	return false
}

func cleanPluginPathForCompare(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return filepath.Clean(value)
}

func CapabilityToSummary(capability CapabilitySummary, marketplace string) PluginSummary {
	name := firstNonEmpty(capability.Name, capability.ConfigName)
	return PluginSummary{
		ID:              pluginID(name, marketplace),
		Name:            name,
		DisplayName:     firstNonEmpty(capability.DisplayName, name),
		Description:     capability.Description,
		MarketplaceName: marketplace,
		RemotePluginID:  capability.RemotePluginID,
		Availability:    PluginAvailable,
		InstallPolicy:   InstallAllowed,
		AuthPolicy:      AuthNone,
		Source:          PluginSource{Type: "local"},
		HasSkills:       capability.HasSkills,
		MCPServers:      append([]string(nil), capability.MCPServers...),
		AppConnectors:   append([]string(nil), capability.AppConnectors...),
		Keywords:        []string{},
	}
}

func capabilityFromDetail(detail *PluginDetail) CapabilitySummary {
	if detail == nil {
		return CapabilitySummary{}
	}
	summary := detail.Summary
	return CapabilitySummary{
		Name:           summary.Name,
		ConfigName:     summary.ID,
		DisplayName:    firstNonEmpty(summary.DisplayName, summary.Name),
		RemotePluginID: summary.RemotePluginID,
		Description:    summary.Description,
		HasSkills:      summary.HasSkills,
		MCPServers:     append([]string(nil), summary.MCPServers...),
		AppConnectors:  append([]string(nil), summary.AppConnectors...),
		Apps:           cloneAppSummaries(detail.Apps),
		AppTemplates:   cloneAppTemplateSummaries(detail.AppTemplates),
	}
}

func pluginID(name string, marketplace string) string {
	name = strings.TrimSpace(name)
	marketplace = strings.TrimSpace(marketplace)
	if name == "" {
		return ""
	}
	if marketplace == "" {
		return name
	}
	return name + "@" + marketplace
}

func marketplaceNameFromURL(value string) string {
	value = strings.TrimSuffix(strings.TrimSpace(value), "/")
	if value == "" {
		return ""
	}
	if index := strings.LastIndexAny(value, `/\`); index >= 0 {
		value = value[index+1:]
	}
	value = strings.TrimSuffix(value, ".git")
	return sanitize(value)
}

func sanitize(value string) string {
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-.")
	if out == "" {
		return "plugin"
	}
	return out
}

func pluginRootFromManifestPath(path string) string {
	clean := filepath.Clean(strings.TrimSpace(path))
	if filepath.Base(clean) == AgentPluginManifestRelativePath && filepath.Base(filepath.Dir(clean)) != ".codex-plugin" && filepath.Base(filepath.Dir(clean)) != ".claude-plugin" && filepath.Base(filepath.Dir(clean)) != ".cursor-plugin" {
		return filepath.Dir(clean)
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	dir := filepath.Dir(path)
	if filepath.Base(dir) == ".codex-plugin" {
		return filepath.Dir(dir)
	}
	return dir
}

func cloneDetail(detail PluginDetail) PluginDetail {
	detail.Summary = cloneSummary(detail.Summary)
	detail.Skills = pluginSkillsForJSON(detail.Skills)
	detail.Hooks = append([]PluginHookSummary(nil), detail.Hooks...)
	detail.Apps = cloneAppSummaries(detail.Apps)
	detail.AppTemplates = cloneAppTemplateSummaries(detail.AppTemplates)
	detail.MCPServers = append([]string(nil), detail.MCPServers...)
	if detail.MarketplacePath != nil {
		value := *detail.MarketplacePath
		detail.MarketplacePath = &value
	}
	if detail.ShareURL != nil {
		value := *detail.ShareURL
		detail.ShareURL = &value
	}
	if detail.Description != nil {
		value := *detail.Description
		detail.Description = &value
	}
	return detail
}

func cloneSummary(summary PluginSummary) PluginSummary {
	summary.MCPServers = append([]string(nil), summary.MCPServers...)
	summary.AppConnectors = append([]string(nil), summary.AppConnectors...)
	summary.Source = clonePluginSource(summary.Source)
	if summary.Keywords == nil {
		summary.Keywords = []string{}
	} else {
		summary.Keywords = append([]string(nil), summary.Keywords...)
	}
	if summary.LocalVersion != nil {
		value := *summary.LocalVersion
		summary.LocalVersion = &value
	}
	if summary.Version != nil {
		value := *summary.Version
		summary.Version = &value
	}
	summary.InstallPolicySource = clonePluginInstallPolicySourcePtr(summary.InstallPolicySource)
	summary.MustShowInstallationInterstitial = cloneBoolPtr(summary.MustShowInstallationInterstitial)
	summary.DisabledReason = clonePluginDisabledReasonPtr(summary.DisabledReason)
	summary.EligiblePlanTypes = cloneStringSlicePtr(summary.EligiblePlanTypes)
	summary.InstalledAt = cloneInt64Ptr(summary.InstalledAt)
	if summary.ShareContext != nil {
		value := cloneShare(*summary.ShareContext)
		summary.ShareContext = &value
	}
	if summary.Interface != nil {
		value := clonePluginInterface(*summary.Interface)
		summary.Interface = &value
	}
	return summary
}

func clonePluginDisabledReasonPtr(value *PluginDisabledReason) *PluginDisabledReason {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneStringSlicePtr(value *[]string) *[]string {
	if value == nil {
		return nil
	}
	cloned := append([]string{}, (*value)...)
	return &cloned
}

func cloneInt64Ptr(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func clonePluginInterface(value PluginInterface) PluginInterface {
	value.DisplayName = cloneStringPtr(value.DisplayName)
	value.ShortDescription = cloneStringPtr(value.ShortDescription)
	value.LongDescription = cloneStringPtr(value.LongDescription)
	value.DeveloperName = cloneStringPtr(value.DeveloperName)
	value.Capabilities = append([]string(nil), value.Capabilities...)
	value.WebsiteURL = cloneStringPtr(value.WebsiteURL)
	value.PrivacyPolicyURL = cloneStringPtr(value.PrivacyPolicyURL)
	value.TermsOfServiceURL = cloneStringPtr(value.TermsOfServiceURL)
	value.DefaultPrompt = append([]string(nil), value.DefaultPrompt...)
	value.BrandColor = cloneStringPtr(value.BrandColor)
	value.ComposerIcon = cloneStringPtr(value.ComposerIcon)
	value.ComposerIconURL = cloneStringPtr(value.ComposerIconURL)
	value.Logo = cloneStringPtr(value.Logo)
	value.LogoDark = cloneStringPtr(value.LogoDark)
	value.LogoURL = cloneStringPtr(value.LogoURL)
	value.LogoURLDark = cloneStringPtr(value.LogoURLDark)
	value.Screenshots = append([]string(nil), value.Screenshots...)
	value.ScreenshotURLs = append([]string(nil), value.ScreenshotURLs...)
	return value
}

func clonePluginInterfacePtr(value *PluginInterface) *PluginInterface {
	if value == nil {
		return nil
	}
	clone := clonePluginInterface(*value)
	return &clone
}

func marketplaceInterfaceForJSON(value any) *MarketplaceInterface {
	switch typed := value.(type) {
	case nil:
		return nil
	case MarketplaceInterface:
		return cloneMarketplaceInterface(&typed)
	case *MarketplaceInterface:
		return cloneMarketplaceInterface(typed)
	case map[string]any:
		return &MarketplaceInterface{DisplayName: stringPtrFromAnyPlugin(typed["displayName"])}
	default:
		return nil
	}
}

func cloneMarketplaceInterface(value *MarketplaceInterface) *MarketplaceInterface {
	if value == nil {
		return nil
	}
	return &MarketplaceInterface{DisplayName: cloneStringPtr(value.DisplayName)}
}

func cloneSkillInterface(value SkillInterface) SkillInterface {
	value.IconSmall = cloneStringPtr(value.IconSmall)
	value.IconLarge = cloneStringPtr(value.IconLarge)
	value.BrandColor = cloneStringPtr(value.BrandColor)
	value.DefaultPrompt = cloneStringPtr(value.DefaultPrompt)
	return value
}

func cloneSkillInterfacePtr(value *SkillInterface) *SkillInterface {
	if value == nil {
		return nil
	}
	clone := cloneSkillInterface(*value)
	return &clone
}

func stringSliceForJSON(values []string) []string {
	out := append([]string(nil), values...)
	if out == nil {
		return []string{}
	}
	return out
}

func optionalStringSliceForJSON(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string(nil), values...)
}

func optionalStringSlicePtrForJSON(values []string) *[]string {
	if values == nil {
		return nil
	}
	out := append([]string(nil), values...)
	return &out
}

func pluginSummariesForJSON(values []PluginSummary) []PluginSummary {
	if values == nil {
		return []PluginSummary{}
	}
	out := make([]PluginSummary, len(values))
	for i := range values {
		out[i] = cloneSummary(values[i])
	}
	return out
}

func pluginSkillsForJSON(values []PluginSkill) []PluginSkill {
	if values == nil {
		return []PluginSkill{}
	}
	out := make([]PluginSkill, len(values))
	for i := range values {
		out[i] = values[i]
		if values[i].Path != nil {
			path := *values[i].Path
			out[i].Path = &path
		}
		if values[i].Interface != nil {
			value := cloneSkillInterface(*values[i].Interface)
			out[i].Interface = &value
		}
	}
	return out
}

func pluginHooksForJSON(values []PluginHookSummary) []PluginHookSummary {
	out := append([]PluginHookSummary(nil), values...)
	if out == nil {
		return []PluginHookSummary{}
	}
	return out
}

func appSummariesForJSON(values []AppSummary) []AppSummary {
	out := cloneAppSummaries(values)
	if out == nil {
		return []AppSummary{}
	}
	return out
}

func appTemplateSummariesForJSON(values []AppTemplateSummary) []AppTemplateSummary {
	out := cloneAppTemplateSummaries(values)
	if out == nil {
		return []AppTemplateSummary{}
	}
	return out
}

func pluginMarketplaceEntriesForJSON(values []PluginMarketplaceEntry) []PluginMarketplaceEntry {
	if values == nil {
		return []PluginMarketplaceEntry{}
	}
	out := make([]PluginMarketplaceEntry, len(values))
	for i := range values {
		out[i] = values[i]
		if values[i].Path != nil {
			path := *values[i].Path
			out[i].Path = &path
		}
		out[i].Plugins = pluginSummariesForJSON(values[i].Plugins)
	}
	return out
}

func pluginShareListItemsForJSON(values []PluginShareListItem) []PluginShareListItem {
	if values == nil {
		return []PluginShareListItem{}
	}
	out := make([]PluginShareListItem, len(values))
	for i := range values {
		out[i] = values[i]
		out[i].Plugin = cloneSummary(values[i].Plugin)
		if values[i].LocalPluginPath != nil {
			path := *values[i].LocalPluginPath
			out[i].LocalPluginPath = &path
		}
	}
	return out
}

func pluginSharePrincipalsForJSON(values []PluginSharePrincipal) []PluginSharePrincipal {
	out := append([]PluginSharePrincipal(nil), values...)
	if out == nil {
		return []PluginSharePrincipal{}
	}
	return out
}

func pluginSharePrincipalsPtrForJSON(values []PluginSharePrincipal) []PluginSharePrincipal {
	if values == nil {
		return nil
	}
	return append([]PluginSharePrincipal(nil), values...)
}

func normalizePluginSharePrincipals(values []PluginSharePrincipal) []PluginSharePrincipal {
	if values == nil {
		return nil
	}
	out := make([]PluginSharePrincipal, 0, len(values))
	for _, value := range values {
		out = append(out, PluginSharePrincipal{
			PrincipalType: strings.TrimSpace(firstNonEmpty(value.PrincipalType, value.Type)),
			PrincipalID:   strings.TrimSpace(firstNonEmpty(value.PrincipalID, value.ID)),
			Role:          strings.TrimSpace(value.Role),
			Name:          strings.TrimSpace(value.Name),
		})
	}
	return out
}

func pluginShareTargetsForParams(primary []PluginSharePrincipal, fallback []PluginSharePrincipal) ([]PluginShareTarget, bool) {
	if primary != nil {
		return pluginShareTargetsForJSON(primary), true
	}
	if fallback != nil {
		return pluginShareTargetsForJSON(fallback), true
	}
	return nil, false
}

func pluginShareTargetsForJSON(values []PluginSharePrincipal) []PluginShareTarget {
	if values == nil {
		return nil
	}
	out := make([]PluginShareTarget, 0, len(values))
	for _, value := range values {
		out = append(out, PluginShareTarget{
			PrincipalType: PluginSharePrincipalType(strings.TrimSpace(firstNonEmpty(value.PrincipalType, value.Type))),
			PrincipalID:   strings.TrimSpace(firstNonEmpty(value.PrincipalID, value.ID)),
			Role:          PluginShareTargetRole(strings.TrimSpace(value.Role)),
		})
	}
	return out
}

func shareTargetsForOptionalJSON(values []PluginShareTarget, ok bool) []PluginShareTarget {
	if !ok {
		return nil
	}
	if values == nil {
		return []PluginShareTarget{}
	}
	return values
}

func shareTargetsPtrForOptionalJSON(values []PluginShareTarget, ok bool) *[]PluginShareTarget {
	if !ok {
		return nil
	}
	if values == nil {
		values = []PluginShareTarget{}
	}
	out := append([]PluginShareTarget(nil), values...)
	return &out
}

func marketplaceLoadErrorsForJSON(values []MarketplaceLoadErrorInfo) []MarketplaceLoadErrorInfo {
	out := append([]MarketplaceLoadErrorInfo(nil), values...)
	if out == nil {
		return []MarketplaceLoadErrorInfo{}
	}
	return out
}

func marketplaceUpgradeErrorsForJSON(values []MarketplaceUpgradeErrorInfo) []MarketplaceUpgradeErrorInfo {
	out := append([]MarketplaceUpgradeErrorInfo(nil), values...)
	if out == nil {
		return []MarketplaceUpgradeErrorInfo{}
	}
	return out
}

func clonePluginSource(value PluginSource) PluginSource {
	value.RefName = cloneStringPtr(value.RefName)
	value.SHA = cloneStringPtr(value.SHA)
	value.Version = cloneStringPtr(value.Version)
	value.Registry = cloneStringPtr(value.Registry)
	return value
}

func cloneAppSummaries(apps []AppSummary) []AppSummary {
	if apps == nil {
		return nil
	}
	out := make([]AppSummary, len(apps))
	for i := range apps {
		out[i] = apps[i]
		out[i].Description = cloneStringPtr(apps[i].Description)
		out[i].InstallURL = cloneStringPtr(apps[i].InstallURL)
		out[i].Category = cloneStringPtr(apps[i].Category)
	}
	return out
}

func cloneAppTemplateSummaries(templates []AppTemplateSummary) []AppTemplateSummary {
	if templates == nil {
		return nil
	}
	out := make([]AppTemplateSummary, len(templates))
	for i := range templates {
		out[i] = templates[i]
		out[i].Description = cloneStringPtr(templates[i].Description)
		out[i].Category = cloneStringPtr(templates[i].Category)
		out[i].CanonicalConnectorID = cloneStringPtr(templates[i].CanonicalConnectorID)
		out[i].LogoURL = cloneStringPtr(templates[i].LogoURL)
		out[i].LogoURLDark = cloneStringPtr(templates[i].LogoURLDark)
		out[i].MaterializedAppIDs = append([]string(nil), templates[i].MaterializedAppIDs...)
		out[i].Reason = cloneStringPtr(templates[i].Reason)
	}
	return out
}

func cloneShare(share PluginShareContext) PluginShareContext {
	if share.RemoteVersion != nil {
		value := *share.RemoteVersion
		share.RemoteVersion = &value
	}
	if share.Discoverability != nil {
		value := *share.Discoverability
		share.Discoverability = &value
	}
	if share.ShareURL != nil {
		value := *share.ShareURL
		share.ShareURL = &value
	}
	if share.CreatorAccountUserID != nil {
		value := *share.CreatorAccountUserID
		share.CreatorAccountUserID = &value
	}
	if share.CreatorName != nil {
		value := *share.CreatorName
		share.CreatorName = &value
	}
	share.CanPublishToWorkspace = cloneBoolPtr(share.CanPublishToWorkspace)
	share.SharePrincipals = append([]PluginSharePrincipal(nil), share.SharePrincipals...)
	share.Principals = append([]PluginSharePrincipal(nil), share.Principals...)
	return share
}

func cloneSharePtr(share *PluginShareContext) *PluginShareContext {
	if share == nil {
		return nil
	}
	clone := cloneShare(*share)
	return &clone
}

func stringPtrIfNotEmpty(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func stringPtrFromAnyPlugin(value any) *string {
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		return stringPtrIfNotEmpty(typed)
	case *string:
		if typed == nil {
			return nil
		}
		return stringPtrIfNotEmpty(*typed)
	default:
		return nil
	}
}

func cloneTrimmedStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	return stringPtrIfNotEmpty(*value)
}

func firstStringPtr(values ...*string) *string {
	for _, value := range values {
		if value == nil {
			continue
		}
		if strings.TrimSpace(*value) != "" {
			return value
		}
	}
	return nil
}

func cloneStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneBoolPtr(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func clonePluginInstallPolicySourcePtr(value *PluginInstallPolicySource) *PluginInstallPolicySource {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstNonEmptyStringSlice(values ...[]string) []string {
	for _, value := range values {
		if len(trimPluginInterfaceStrings(value)) != 0 {
			return value
		}
	}
	return nil
}
