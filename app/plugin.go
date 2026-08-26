package app

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"codex_go/appserver"
	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
	"codex_go/plugin"
)

type pluginCLIContext struct {
	codexHome string
	service   *plugin.PluginService
}

type pluginSelection struct {
	PluginName      string
	MarketplaceName string
	PluginID        string
}

type pluginListJSONOutput struct {
	Installed []pluginListJSONEntry `json:"installed"`
	Available []pluginListJSONEntry `json:"available"`
}

type pluginListJSONEntry struct {
	PluginID          string                     `json:"pluginId"`
	Name              string                     `json:"name"`
	MarketplaceName   string                     `json:"marketplaceName"`
	Version           *string                    `json:"version"`
	Installed         bool                       `json:"installed"`
	Enabled           bool                       `json:"enabled"`
	Source            plugin.PluginSource        `json:"source"`
	MarketplaceSource *pluginMarketplaceSource   `json:"marketplaceSource,omitempty"`
	InstallPolicy     plugin.PluginInstallPolicy `json:"installPolicy"`
	AuthPolicy        plugin.PluginAuthPolicy    `json:"authPolicy"`
}

type pluginMarketplaceSource struct {
	SourceType string `json:"sourceType"`
	Source     string `json:"source"`
}

type pluginAddJSONOutput struct {
	PluginID        string                  `json:"pluginId"`
	Name            string                  `json:"name"`
	MarketplaceName string                  `json:"marketplaceName"`
	Version         string                  `json:"version"`
	InstalledPath   string                  `json:"installedPath"`
	AuthPolicy      plugin.PluginAuthPolicy `json:"authPolicy"`
}

type pluginRemoveJSONOutput struct {
	PluginID        string `json:"pluginId"`
	Name            string `json:"name"`
	MarketplaceName string `json:"marketplaceName"`
}

type marketplaceListJSONOutput struct {
	Marketplaces []marketplaceListJSONEntry `json:"marketplaces"`
}

type marketplaceListJSONEntry struct {
	Name              string                   `json:"name"`
	Root              string                   `json:"root"`
	MarketplaceSource *pluginMarketplaceSource `json:"marketplaceSource,omitempty"`
}

func runPlugin(opts *cli.PluginOptions, stdout io.Writer) error {
	ctx := newPluginCLIContext(auth.DefaultCodexHome())
	switch opts.Action {
	case "add":
		return runPluginAdd(ctx, opts, stdout)
	case "list":
		return runPluginList(ctx, opts, stdout)
	case "remove":
		return runPluginRemove(ctx, opts, stdout)
	case "marketplace":
		return runPluginMarketplace(ctx, &opts.Marketplace, stdout)
	default:
		return fmt.Errorf("unknown plugin subcommand %s", opts.Action)
	}
}

func newPluginCLIContext(codexHome string) *pluginCLIContext {
	service := plugin.NewPluginService()
	service.SetCodexHome(codexHome)
	return &pluginCLIContext{codexHome: codexHome, service: service}
}

func runPluginAdd(ctx *pluginCLIContext, opts *cli.PluginOptions, stdout io.Writer) error {
	selection, err := parsePluginSelection(opts.Plugin, opts.MarketplaceName)
	if err != nil {
		return err
	}
	installed, err := ctx.service.Install(&plugin.PluginInstallParams{
		PluginName:      selection.PluginName,
		MarketplaceName: selection.MarketplaceName,
	})
	if err != nil {
		return err
	}
	read, readErr := ctx.service.Read(&plugin.PluginReadParams{
		PluginName:      selection.PluginName,
		MarketplaceName: selection.MarketplaceName,
	})
	summary := plugin.PluginSummary{
		ID:              firstNonEmptyLocal(installed.PluginID, selection.PluginID),
		Name:            selection.PluginName,
		MarketplaceName: selection.MarketplaceName,
		AuthPolicy:      installed.AuthPolicy,
	}
	if readErr == nil {
		summary = read.Plugin.Summary
	}
	if summary.ID == "" {
		summary.ID = firstNonEmptyLocal(installed.PluginID, selection.PluginID)
	}
	if summary.Name == "" {
		summary.Name = selection.PluginName
	}
	if summary.MarketplaceName == "" {
		summary.MarketplaceName = selection.MarketplaceName
	}
	installedPath := firstNonEmptyLocal(summary.Source.Path, summary.Source.URL, summary.Source.Package)
	version := "local"
	if summary.LocalVersion != nil && strings.TrimSpace(*summary.LocalVersion) != "" {
		version = strings.TrimSpace(*summary.LocalVersion)
	}
	authPolicy := installed.AuthPolicy
	if authPolicy == "" {
		authPolicy = summary.AuthPolicy
	}
	if opts.JSON {
		output := pluginAddJSONOutput{
			PluginID:        summary.ID,
			Name:            summary.Name,
			MarketplaceName: summary.MarketplaceName,
			Version:         version,
			InstalledPath:   installedPath,
			AuthPolicy:      authPolicy,
		}
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(&output)
	}
	fmt.Fprintf(stdout, "Added plugin `%s` from marketplace `%s`.\n", summary.Name, summary.MarketplaceName)
	fmt.Fprintf(stdout, "Installed plugin root: %s\n", installedPath)
	return nil
}

func runPluginList(ctx *pluginCLIContext, opts *cli.PluginOptions, stdout io.Writer) error {
	list := ctx.service.List(&plugin.PluginListParams{IncludeInstalled: true})
	if err := pluginMarketplaceLoadError(list.MarketplaceLoadErrors, "failed to list marketplace plugins"); err != nil {
		return err
	}
	entries := filterPluginMarketplaceEntries(list.Marketplaces, opts.MarketplaceName)
	if opts.JSON {
		output := pluginListJSONOutput{}
		for _, marketplace := range entries {
			for _, summary := range marketplace.Plugins {
				entry := pluginListEntryFromSummary(summary)
				if entry.MarketplaceName == "" {
					entry.MarketplaceName = marketplace.Name
				}
				if summary.Installed {
					output.Installed = append(output.Installed, entry)
				} else if opts.Available {
					output.Available = append(output.Available, entry)
				}
			}
		}
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(&output)
	}
	if pluginMarketplaceEntriesPluginCount(entries) == 0 {
		if opts.MarketplaceName != "" {
			fmt.Fprintf(stdout, "No plugins found in marketplace `%s`.\n", opts.MarketplaceName)
			return nil
		}
		fmt.Fprintln(stdout, "No marketplace plugins found.")
		return nil
	}
	for index, marketplace := range entries {
		if len(marketplace.Plugins) == 0 {
			continue
		}
		if index > 0 {
			fmt.Fprintln(stdout)
		}
		fmt.Fprintf(stdout, "Marketplace `%s`\n", marketplace.Name)
		fmt.Fprintf(stdout, "%s\n\n", stringValueOrEmptyLocal(marketplace.Path))
		printPluginRows(stdout, marketplace.Plugins)
	}
	return nil
}

func runPluginRemove(ctx *pluginCLIContext, opts *cli.PluginOptions, stdout io.Writer) error {
	selection, err := parsePluginSelection(opts.Plugin, opts.MarketplaceName)
	if err != nil {
		return err
	}
	if _, err := ctx.service.Uninstall(&plugin.PluginUninstallParams{PluginID: selection.PluginID}); err != nil {
		return err
	}
	if opts.JSON {
		output := pluginRemoveJSONOutput{
			PluginID:        selection.PluginID,
			Name:            selection.PluginName,
			MarketplaceName: selection.MarketplaceName,
		}
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(&output)
	}
	fmt.Fprintf(stdout, "Removed plugin `%s` from marketplace `%s`.\n", selection.PluginName, selection.MarketplaceName)
	return nil
}

func runPluginMarketplace(ctx *pluginCLIContext, opts *cli.PluginMarketplaceOptions, stdout io.Writer) error {
	switch opts.Action {
	case "add":
		return runPluginMarketplaceAdd(ctx, opts, stdout)
	case "list":
		return runPluginMarketplaceList(ctx, opts, stdout)
	case "upgrade":
		return runPluginMarketplaceUpgrade(ctx, opts, stdout)
	case "remove":
		return runPluginMarketplaceRemove(ctx, opts, stdout)
	default:
		return fmt.Errorf("unknown plugin marketplace subcommand %s", opts.Action)
	}
}

func runPluginMarketplaceAdd(ctx *pluginCLIContext, opts *cli.PluginMarketplaceOptions, stdout io.Writer) error {
	outcome, err := ctx.service.AddMarketplace(&plugin.MarketplaceAddParams{
		Source:      opts.Source,
		RefName:     stringPtrIfNotEmptyLocal(opts.RefName),
		SparsePaths: append([]string(nil), opts.SparsePaths...),
	})
	if err != nil {
		return err
	}
	if opts.JSON {
		output := map[string]any{
			"marketplaceName": outcome.MarketplaceName,
			"installedRoot":   outcome.InstalledRoot,
			"alreadyAdded":    outcome.AlreadyAdded || outcome.AlreadyPresent,
		}
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(output)
	}
	if outcome.AlreadyAdded || outcome.AlreadyPresent {
		fmt.Fprintf(stdout, "Marketplace `%s` is already added from %s.\n", outcome.MarketplaceName, opts.Source)
	} else {
		fmt.Fprintf(stdout, "Added marketplace `%s` from %s.\n", outcome.MarketplaceName, opts.Source)
	}
	fmt.Fprintf(stdout, "Installed marketplace root: %s\n", outcome.InstalledRoot)
	return nil
}

func runPluginMarketplaceList(ctx *pluginCLIContext, opts *cli.PluginMarketplaceOptions, stdout io.Writer) error {
	list := ctx.service.List(&plugin.PluginListParams{IncludeInstalled: true})
	if err := pluginMarketplaceLoadError(list.MarketplaceLoadErrors, "failed to load marketplace(s)"); err != nil {
		return err
	}
	entries := filterPluginMarketplaceEntries(list.Marketplaces, "")
	if opts.JSON {
		output := marketplaceListJSONOutput{Marketplaces: make([]marketplaceListJSONEntry, 0, len(entries))}
		for _, marketplace := range entries {
			output.Marketplaces = append(output.Marketplaces, marketplaceListJSONEntry{
				Name:              marketplace.Name,
				Root:              stringValueOrEmptyLocal(marketplace.Path),
				MarketplaceSource: nil,
			})
		}
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(&output)
	}
	if len(entries) == 0 {
		fmt.Fprintln(stdout, "No plugin marketplaces in scope.")
		return nil
	}
	rows := make([][7]string, 0, len(entries))
	for _, marketplace := range entries {
		rows = append(rows, [7]string{marketplace.Name, stringValueOrEmptyLocal(marketplace.Path)})
	}
	printTable(stdout, []string{"MARKETPLACE", "ROOT"}, rows)
	return nil
}

func runPluginMarketplaceUpgrade(ctx *pluginCLIContext, opts *cli.PluginMarketplaceOptions, stdout io.Writer) error {
	outcome, err := ctx.service.UpgradeMarketplace(&plugin.MarketplaceUpgradeParams{MarketplaceName: stringPtrIfNotEmptyLocal(opts.Name)})
	if err != nil {
		return err
	}
	if opts.JSON {
		if err := pluginMarketplaceUpgradeError(outcome.Errors); err != nil {
			return err
		}
		output := map[string]any{
			"selectedMarketplaces": outcome.SelectedMarketplaces,
			"upgradedRoots":        outcome.UpgradedRoots,
			"errors":               outcome.Errors,
		}
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(output)
	}
	if err := pluginMarketplaceUpgradeError(outcome.Errors); err != nil {
		return err
	}
	if len(outcome.SelectedMarketplaces) == 0 {
		fmt.Fprintln(stdout, "No configured Git marketplaces to upgrade.")
		return nil
	}
	if len(outcome.UpgradedRoots) == 0 {
		if opts.Name != "" {
			fmt.Fprintf(stdout, "Marketplace `%s` is already up to date.\n", opts.Name)
		} else {
			fmt.Fprintln(stdout, "All configured Git marketplaces are already up to date.")
		}
		return nil
	}
	if opts.Name != "" {
		fmt.Fprintf(stdout, "Upgraded marketplace `%s` to the latest configured revision.\n", opts.Name)
	} else {
		fmt.Fprintf(stdout, "Upgraded %d marketplace(s).\n", len(outcome.UpgradedRoots))
	}
	for _, root := range outcome.UpgradedRoots {
		fmt.Fprintf(stdout, "Installed marketplace root: %s\n", root)
	}
	return nil
}

func runPluginMarketplaceRemove(ctx *pluginCLIContext, opts *cli.PluginMarketplaceOptions, stdout io.Writer) error {
	name := strings.TrimSpace(opts.Name)
	if name != "" {
		if source, err := appserver.MarketplaceRemoveConflictSource(config.NewConfigService(ctx.codexHome), name); err != nil {
			return err
		} else if source != "" {
			return fmt.Errorf("marketplace `%s` is also defined by the %s layer; update that configuration source instead of removing it", name, source)
		}
	}
	outcome, err := ctx.service.RemoveMarketplace(&plugin.MarketplaceRemoveParams{MarketplaceName: opts.Name})
	if err != nil {
		return err
	}
	if opts.JSON {
		output := map[string]any{
			"marketplaceName": outcome.MarketplaceName,
			"installedRoot":   outcome.InstalledRoot,
		}
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(output)
	}
	fmt.Fprintf(stdout, "Removed marketplace `%s`.\n", outcome.MarketplaceName)
	if outcome.InstalledRoot != nil {
		fmt.Fprintf(stdout, "Removed installed marketplace root: %s\n", *outcome.InstalledRoot)
	}
	return nil
}

func filterPluginMarketplaceEntries(entries []plugin.PluginMarketplaceEntry, marketplaceName string) []plugin.PluginMarketplaceEntry {
	marketplaceName = strings.TrimSpace(marketplaceName)
	out := make([]plugin.PluginMarketplaceEntry, 0, len(entries))
	seen := map[string]bool{}
	for _, entry := range entries {
		if marketplaceName != "" && entry.Name != marketplaceName {
			continue
		}
		key := entry.Name + "\x00" + stringValueOrEmptyLocal(entry.Path)
		if seen[key] {
			continue
		}
		seen[key] = true
		cloned := entry
		cloned.Plugins = append([]plugin.PluginSummary(nil), entry.Plugins...)
		sort.SliceStable(cloned.Plugins, func(i int, j int) bool {
			return cloned.Plugins[i].ID < cloned.Plugins[j].ID
		})
		out = append(out, cloned)
	}
	sort.SliceStable(out, func(i int, j int) bool {
		if out[i].Name == out[j].Name {
			return stringValueOrEmptyLocal(out[i].Path) < stringValueOrEmptyLocal(out[j].Path)
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func pluginMarketplaceEntriesPluginCount(entries []plugin.PluginMarketplaceEntry) int {
	count := 0
	for _, entry := range entries {
		count += len(entry.Plugins)
	}
	return count
}

func pluginListEntryFromSummary(summary plugin.PluginSummary) pluginListJSONEntry {
	return pluginListJSONEntry{
		PluginID:        summary.ID,
		Name:            summary.Name,
		MarketplaceName: summary.MarketplaceName,
		Version:         summary.LocalVersion,
		Installed:       summary.Installed,
		Enabled:         summary.Enabled,
		Source:          summary.Source,
		InstallPolicy:   summary.InstallPolicy,
		AuthPolicy:      summary.AuthPolicy,
	}
}

func pluginMarketplaceLoadError(errors []plugin.MarketplaceLoadErrorInfo, context string) error {
	if len(errors) == 0 {
		return nil
	}
	lines := make([]string, 0, len(errors))
	for _, loadErr := range errors {
		lines = append(lines, fmt.Sprintf("- `%s`: %s", loadErr.MarketplacePath, loadErr.Message))
	}
	sort.Strings(lines)
	return fmt.Errorf("%s:\n%s", context, strings.Join(lines, "\n"))
}

func pluginMarketplaceUpgradeError(errors []plugin.MarketplaceUpgradeErrorInfo) error {
	if len(errors) == 0 {
		return nil
	}
	return fmt.Errorf("%d upgrade failure(s) occurred.", len(errors))
}

func stringValueOrEmptyLocal(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func parsePluginSelection(value string, marketplaceName string) (*pluginSelection, error) {
	pluginName := strings.TrimSpace(value)
	marketplaceName = strings.TrimSpace(marketplaceName)
	if pluginName == "" {
		return nil, fmt.Errorf("plugin requires PLUGIN")
	}
	if strings.Contains(pluginName, "@") {
		pluginKey := pluginName
		index := strings.LastIndex(pluginKey, "@")
		if index <= 0 || index == len(pluginKey)-1 {
			return nil, fmt.Errorf("invalid plugin key `%s`; expected <plugin>@<marketplace>", value)
		}
		pluginPart := pluginKey[:index]
		marketplacePart := pluginKey[index+1:]
		if err := validatePluginSegment(pluginPart, "plugin name"); err != nil {
			return nil, fmt.Errorf("%s in `%s`", err, value)
		}
		if err := validatePluginSegment(marketplacePart, "marketplace name"); err != nil {
			return nil, fmt.Errorf("%s in `%s`", err, value)
		}
		if marketplaceName != "" && marketplaceName != marketplacePart {
			return nil, fmt.Errorf("plugin id `%s` belongs to marketplace `%s`, but --marketplace specified `%s`", value, marketplacePart, marketplaceName)
		}
		pluginName = pluginPart
		marketplaceName = marketplacePart
	}
	if marketplaceName == "" {
		return nil, fmt.Errorf("plugin requires --marketplace unless passed as <plugin>@<marketplace>")
	}
	if err := validatePluginSegment(pluginName, "plugin name"); err != nil {
		return nil, err
	}
	if err := validatePluginSegment(marketplaceName, "marketplace name"); err != nil {
		return nil, err
	}
	return &pluginSelection{
		PluginName:      pluginName,
		MarketplaceName: marketplaceName,
		PluginID:        pluginName + "@" + marketplaceName,
	}, nil
}

func validatePluginSegment(segment string, kind string) error {
	if segment == "" {
		return fmt.Errorf("invalid %s: must not be empty", kind)
	}
	for _, r := range segment {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			continue
		}
		return fmt.Errorf("invalid %s: only ASCII letters, digits, `_`, and `-` are allowed", kind)
	}
	return nil
}

func printPluginRows(stdout io.Writer, rows []plugin.PluginSummary) {
	headers := []string{"PLUGIN", "STATUS", "VERSION", "PATH"}
	widths := []int{len(headers[0]), len(headers[1]), len(headers[2]), len(headers[3])}
	converted := make([][4]string, 0, len(rows))
	for _, summary := range rows {
		status := "not installed"
		if summary.Installed && summary.Enabled {
			status = "installed, enabled"
		} else if summary.Installed {
			status = "installed, disabled"
		}
		version := ""
		if summary.LocalVersion != nil {
			version = *summary.LocalVersion
		}
		path := firstNonEmptyLocal(summary.Source.Path, summary.Source.URL, summary.Source.Package)
		row := [4]string{summary.ID, status, version, path}
		for i, cell := range row {
			if len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
		converted = append(converted, row)
	}
	for i, header := range headers {
		if i > 0 {
			fmt.Fprint(stdout, "  ")
		}
		fmt.Fprintf(stdout, "%-*s", widths[i], header)
	}
	fmt.Fprintln(stdout)
	for _, row := range converted {
		for i, cell := range row {
			if i > 0 {
				fmt.Fprint(stdout, "  ")
			}
			fmt.Fprintf(stdout, "%-*s", widths[i], cell)
		}
		fmt.Fprintln(stdout)
	}
}

func firstNonEmptyLocal(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
