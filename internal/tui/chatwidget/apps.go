package chatwidget

import (
	"strings"

	appsapi "codex_go/internal/apps"
)

const AppsSelectionViewID = "connectors-selection"

const AppsActionOpenLink UsageMenuAction = "apps_open_link"

func AppsLoadingView() SelectionView {
	return SelectionView{
		ViewID:      AppsSelectionViewID,
		Title:       "Apps",
		Subtitle:    "Loading installed and available apps...",
		AllowCancel: true,
		Items: []SelectionItem{{
			Name:        "Loading apps...",
			Description: "This updates when the full list is ready.",
			Disabled:    true,
		}},
	}
}

func AppsErrorView(message string) SelectionView {
	return SelectionView{
		ViewID:      AppsSelectionViewID,
		Title:       "Apps",
		Subtitle:    strings.TrimSpace(message),
		AllowCancel: true,
		Items: []SelectionItem{{
			Name:            "Close",
			DismissOnSelect: true,
		}},
	}
}

func NewAppsCatalogView(response appsapi.AppListResponse) SelectionView {
	apps := appsCatalogEntries(response)
	items := make([]SelectionItem, 0, len(apps))
	for _, app := range apps {
		id := app.ID
		status := ConnectorStatusLabel(app)
		selectedDescription := status + ". App link unavailable."
		if app.InstallURL != nil {
			if app.IsAccessible {
				selectedDescription = status + ". Press Enter to open the app page to install, manage, or enable/disable this app."
			} else {
				selectedDescription = status + ". Press Enter to open the app page to install this app."
			}
		}
		items = append(items, SelectionItem{
			ID:                  id,
			Name:                AppDisplayLabel(app),
			Description:         ConnectorBriefDescription(app),
			SelectedDescription: selectedDescription,
			SearchValue:         AppDisplayLabel(app) + " " + app.ID,
			Action:              AppsActionOpenLink,
			DismissOnSelect:     app.InstallURL != nil,
		})
	}
	if len(items) == 0 {
		items = append(items, SelectionItem{Name: "No apps available", Disabled: true})
	}
	return SelectionView{
		ViewID:            AppsSelectionViewID,
		Title:             "Apps",
		HeaderLines:       []string{"Use $ to insert an installed app into your prompt.", InstalledAppsCountLine(apps)},
		FooterHint:        standardPopupHintLine,
		AllowCancel:       true,
		Searchable:        true,
		SearchPlaceholder: "Type to search apps",
		Items:             items,
	}
}

func InstalledAppsCountLine(apps []appsapi.AppEntry) string {
	installed := 0
	for _, app := range apps {
		if app.IsAccessible {
			installed++
		}
	}
	return "Installed " + intString(installed) + " of " + intString(len(apps)) + " available apps."
}

func AppBriefDescription(app appsapi.AppEntry) string {
	status := ConnectorStatusLabel(app)
	if desc := AppDescription(app); desc != "" {
		return status + " \u2022 " + desc
	}
	return status
}

func AppStatusLabel(app appsapi.AppEntry) string {
	return ConnectorStatusLabel(app)
}

func AppDescription(app appsapi.AppEntry) string {
	return strings.TrimSpace(stringPtrValueApps(app.Description))
}

func AppDisplayLabel(app appsapi.AppEntry) string {
	return app.Name
}

func appsCatalogEntries(response appsapi.AppListResponse) []appsapi.AppEntry {
	switch {
	case response.AllApps != nil:
		return append([]appsapi.AppEntry(nil), response.AllApps...)
	case response.Apps != nil:
		return append([]appsapi.AppEntry(nil), response.Apps...)
	default:
		return append([]appsapi.AppEntry(nil), response.Data...)
	}
}

func stringPtrValueApps(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
