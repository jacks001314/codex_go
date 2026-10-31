package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"codex_go/appserver"
	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
	codexnetwork "codex_go/network"
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
	applyPluginMarketplacePolicyFromHome(service, codexHome)
	if snapshot, err := auth.NewStore(codexHome).Load(); err == nil && snapshot != nil {
		service.SetRuntimeRoute(snapshot.AuthMode, "openai")
	}
	return &pluginCLIContext{codexHome: codexHome, service: service}
}

// applyPluginMarketplacePolicyFromHome mirrors the app-server policy snapshot
// for CLI plugin operations. If the merged config is invalid, the managed
// requirements are still honored and the empty config projection fails closed.
func applyPluginMarketplacePolicyFromHome(service *plugin.PluginService, codexHome string) {
	if service == nil {
		return
	}
	cfg, err := config.Load(codexHome)
	if err == nil && cfg != nil {
		var requirements *plugin.MarketplaceRequirements
		if cfg.Requirements != nil {
			requirements = cfg.Requirements.Marketplaces
		}
		service.SetMarketplacePolicy(requirements, cfg.Values)
		return
	}
	var requirements *plugin.MarketplaceRequirements
	if loaded, loadErr := config.LoadRequirementsFile(filepath.Join(codexHome, "requirements.toml")); loadErr == nil && loaded != nil {
		requirements = loaded.Marketplaces
	}
	service.SetMarketplacePolicy(requirements, nil)
}

func (ctx *pluginCLIContext) remoteOptionsForScope(scope string) (*plugin.RemoteCatalogOptions, error) {
	snapshot, err := auth.NewStore(ctx.codexHome).Load()
	if err != nil || snapshot == nil {
		return nil, fmt.Errorf("ChatGPT auth is required to load remote plugins")
	}
	token := ""
	if value, ok := snapshot.Tokens["access_token"].(string); ok {
		token = strings.TrimSpace(value)
	}
	if token == "" {
		return nil, fmt.Errorf("ChatGPT auth is required to load remote plugins")
	}
	cfg, err := config.Load(ctx.codexHome)
	if err != nil {
		return nil, err
	}
	baseURL := ""
	productSKU := ""
	if cfg != nil {
		baseURL = cfg.ChatGPTBaseURL()
		productSKU, _ = cfg.Values["apps_mcp_product_sku"].(string)
	}
	return &plugin.RemoteCatalogOptions{
		BaseURL:     baseURL,
		AccessToken: token,
		AccountID:   auth.AccountIDFromAuthForRestrictions(snapshot),
		ProductSKU:  strings.TrimSpace(productSKU),
		Scope:       scope,
		CodexHome:   ctx.codexHome,
		HTTPClient:  codexnetwork.NewHTTPClient(true, 0),
	}, nil
}

func (ctx *pluginCLIContext) remoteCatalogPlugins(scope string, force bool) ([]plugin.RemoteCatalogPlugin, error) {
	options, err := ctx.remoteOptionsForScope(scope)
	if err != nil {
		return nil, err
	}
	options.ForceRefresh = force
	return plugin.FetchRemoteCatalogPlugins(context.Background(), options)
}

func (ctx *pluginCLIContext) remotePluginForSelection(selection *pluginSelection) (*plugin.RemoteCatalogPlugin, *plugin.RemoteCatalogOptions, error) {
	if selection == nil {
		return nil, nil, nil
	}
	scope, ok := plugin.RemoteCatalogScopeForMarketplace(selection.MarketplaceName)
	if !ok {
		return nil, nil, nil
	}
	options, err := ctx.remoteOptionsForScope(scope)
	if err != nil {
		return nil, nil, err
	}
	plugins, err := plugin.FetchRemoteCatalogPlugins(context.Background(), options)
	if err != nil {
		return nil, nil, err
	}
	for index := range plugins {
		candidate := &plugins[index]
		if strings.EqualFold(strings.TrimSpace(candidate.Name), selection.PluginName) || strings.TrimSpace(candidate.ID) == strings.TrimSpace(selection.PluginID) {
			return candidate, options, nil
		}
	}
	return nil, nil, fmt.Errorf("plugin %q is not available in remote marketplace %q", selection.PluginName, selection.MarketplaceName)
}

func appendRemotePluginMarketplace(entries []plugin.PluginMarketplaceEntry, marketplace string, remote []plugin.RemoteCatalogPlugin, installed map[string]bool) []plugin.PluginMarketplaceEntry {
	if len(remote) == 0 {
		return entries
	}
	summaries := make([]plugin.PluginSummary, 0, len(remote))
	for _, item := range remote {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		summaries = append(summaries, plugin.PluginSummary{
			ID:                name + "@" + marketplace,
			Name:              name,
			DisplayName:       firstNonEmptyLocal(strings.TrimSpace(item.Release.DisplayName), name),
			Description:       strings.TrimSpace(item.Release.Description),
			MarketplaceName:   marketplace,
			RemotePluginID:    strings.TrimSpace(item.ID),
			Version:           item.Release.Version,
			LocalVersion:      item.Release.Version,
			Availability:      item.Availability,
			DisabledReason:    item.DisabledReason,
			EligiblePlanTypes: item.EligiblePlanTypes,
			InstallPolicy:     item.InstallationPolicy,
			AuthPolicy:        item.AuthenticationPolicy,
			Source:            plugin.PluginSource{Type: "remote"},
			Installed:         installed[item.ID],
			Enabled:           installed[item.ID],
			Keywords:          append([]string(nil), item.Release.Keywords...),
		})
	}
	if len(summaries) == 0 {
		return entries
	}
	return append(entries, plugin.PluginMarketplaceEntry{Name: marketplace, Plugins: summaries})
}

func runPluginAdd(ctx *pluginCLIContext, opts *cli.PluginOptions, stdout io.Writer) error {
	selection, err := parsePluginSelection(opts.Plugin, opts.MarketplaceName)
	if err != nil {
		return err
	}
	if remote, options, err := ctx.remotePluginForSelection(selection); err != nil {
		return err
	} else if remote != nil {
		appsNeedingAuth, err := plugin.InstallRemotePluginMutation(context.Background(), options, remote.ID)
		if err != nil {
			return err
		}
		detail, err := plugin.FetchRemotePluginDetail(context.Background(), options, remote.ID)
		if err != nil {
			return err
		}
		if detail.Release.BundleDownloadURL == nil || strings.TrimSpace(*detail.Release.BundleDownloadURL) == "" {
			return fmt.Errorf("remote plugin %q did not provide a bundle download URL", remote.Name)
		}
		version := stringValueOrEmptyLocal(detail.Release.Version)
		root, err := appserver.InstallRemotePluginBundle(context.Background(), codexnetwork.NewHTTPClient(true, 0), &appserver.RemotePluginBundleInstallParams{
			CodexHome:         ctx.codexHome,
			MarketplaceName:   selection.MarketplaceName,
			PluginName:        remote.Name,
			Version:           version,
			RemotePluginID:    remote.ID,
			BundleDownloadURL: *detail.Release.BundleDownloadURL,
			AppManifest:       detail.Release.AppManifest,
		})
		if err != nil {
			return err
		}
		summary := plugin.PluginSummary{
			ID:              remote.Name + "@" + selection.MarketplaceName,
			Name:            remote.Name,
			DisplayName:     firstNonEmptyLocal(detail.Release.DisplayName, remote.Release.DisplayName, remote.Name),
			Description:     firstNonEmptyLocal(detail.Release.Description, remote.Release.Description),
			MarketplaceName: selection.MarketplaceName,
			RemotePluginID:  remote.ID,
			Version:         detail.Release.Version,
			LocalVersion:    detail.Release.Version,
			Availability:    remote.Availability,
			InstallPolicy:   remote.InstallationPolicy,
			AuthPolicy:      remote.AuthenticationPolicy,
			Source:          plugin.PluginSource{Type: "local", Path: root},
			Keywords:        append([]string(nil), detail.Release.Keywords...),
		}
		ctx.service.ReplaceInstalledRemotePlugins(selection.MarketplaceName, []plugin.PluginDetail{{
			MarketplaceName: selection.MarketplaceName,
			MarketplaceRoot: root,
			Summary:         summary,
		}})
		installed, err := ctx.service.Install(&plugin.PluginInstallParams{PluginID: summary.ID})
		if err != nil {
			return err
		}
		if opts.JSON {
			output := pluginAddJSONOutput{
				PluginID:        firstNonEmptyLocal(installed.PluginID, summary.ID),
				Name:            summary.Name,
				MarketplaceName: selection.MarketplaceName,
				Version:         version,
				InstalledPath:   root,
				AuthPolicy:      summary.AuthPolicy,
			}
			encoder := json.NewEncoder(stdout)
			encoder.SetIndent("", "  ")
			if err := encoder.Encode(&output); err != nil {
				return err
			}
		} else {
			fmt.Fprintf(stdout, "Added plugin `%s` from marketplace `%s`.\n", summary.Name, selection.MarketplaceName)
			fmt.Fprintf(stdout, "Installed plugin root: %s\n", root)
		}
		if len(appsNeedingAuth) > 0 && !opts.JSON {
			fmt.Fprintf(stdout, "Apps needing authentication: %s\n", strings.Join(appsNeedingAuth, ", "))
		}
		return nil
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
	installedRemote := map[string]bool{}
	for _, marketplace := range list.Marketplaces {
		for _, summary := range marketplace.Plugins {
			if summary.Installed && strings.TrimSpace(summary.RemotePluginID) != "" {
				installedRemote[summary.RemotePluginID] = true
			}
		}
	}
	if scope, ok := plugin.RemoteCatalogScopeForMarketplace(opts.MarketplaceName); ok {
		remote, err := ctx.remoteCatalogPlugins(scope, false)
		if err != nil {
			return err
		}
		entries = appendRemotePluginMarketplace(entries, opts.MarketplaceName, remote, installedRemote)
	} else if opts.MarketplaceName == "" {
		// An unfiltered listing keeps the local curated catalog when the remote
		// directory is unavailable; an explicitly selected remote marketplace
		// surfaces the error above.
		if remote, err := ctx.remoteCatalogPlugins(plugin.RemoteCatalogScopeGlobal, false); err == nil {
			entries = appendRemotePluginMarketplace(entries, plugin.RemoteGlobalMarketplaceName, remote, installedRemote)
		}
	}
	writer := bufio.NewWriter(stdout)
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
		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(&output); err != nil {
			return pluginListOutputError(err)
		}
		return pluginListOutputError(writer.Flush())
	}
	var writeErr error
	writef := func(format string, args ...any) {
		if writeErr != nil {
			return
		}
		_, writeErr = fmt.Fprintf(writer, format, args...)
	}
	if pluginMarketplaceEntriesPluginCount(entries) == 0 {
		if opts.MarketplaceName != "" {
			writef("No plugins found in marketplace `%s`.\n", opts.MarketplaceName)
		} else {
			writef("No marketplace plugins found.\n")
		}
		return pluginListOutputError(firstNonNilError(writeErr, writer.Flush()))
	}
	for index, marketplace := range entries {
		if len(marketplace.Plugins) == 0 {
			continue
		}
		if index > 0 {
			writef("\n")
		}
		writef("Marketplace `%s`\n", marketplace.Name)
		if marketplace.Path == nil {
			writef("Remote catalog\n\n")
		} else {
			writef("%s\n\n", stringValueOrEmptyLocal(marketplace.Path))
		}
		if err := printPluginRows(writer, marketplace.Plugins); err != nil {
			return pluginListOutputError(err)
		}
	}
	return pluginListOutputError(firstNonNilError(writeErr, writer.Flush()))
}

func pluginListOutputError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, syscall.EPIPE) || errors.Is(err, io.ErrClosedPipe) {
		return nil
	}
	return err
}

func firstNonNilError(values ...error) error {
	for _, err := range values {
		if err != nil {
			return err
		}
	}
	return nil
}

func runPluginRemove(ctx *pluginCLIContext, opts *cli.PluginOptions, stdout io.Writer) error {
	selection, err := parsePluginSelection(opts.Plugin, opts.MarketplaceName)
	if err != nil {
		return err
	}
	if remote, options, err := ctx.remotePluginForSelection(selection); err != nil {
		return err
	} else if remote != nil {
		if err := plugin.UninstallRemotePluginMutation(context.Background(), options, remote.ID); err != nil {
			return err
		}
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
		// Rust surfaces the marketplace-source diagnostics as-is; the plugin
		// request wrapper only exists inside the app-server RPC payload.
		return pluginMarketplaceSourceError(err)
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

// pluginMarketplaceSourceError drops the ErrInvalidPluginRequest wrapper so the
// CLI prints the bare reason Rust reports.
func pluginMarketplaceSourceError(err error) error {
	if !errors.Is(err, plugin.ErrInvalidPluginRequest) {
		return err
	}
	prefix := plugin.ErrInvalidPluginRequest.Error() + ": "
	message := strings.TrimPrefix(err.Error(), prefix)
	if message == err.Error() {
		return err
	}
	return errors.New(message)
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
	// Rust #42149: the CLI upgrades the Git marketplaces selected from its
	// effective configuration (including non-user layers and -c overrides) and
	// reloads with the same loader before activation.
	cwd, _ := os.Getwd()
	load := func() (map[string]any, error) {
		loaded, err := config.LoadEffective(ctx.codexHome, opts.ConfigOverrides, nil, nil, cwd)
		if err != nil {
			return nil, err
		}
		return loaded.Values, nil
	}
	values, err := load()
	if err != nil {
		return err
	}
	outcome, err := ctx.service.UpgradeConfiguredMarketplaces(values, opts.Name, load)
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
	var details strings.Builder
	for _, item := range errors {
		fmt.Fprintf(&details, "Failed to upgrade marketplace `%s`: %s\n", item.MarketplaceName, item.Message)
	}
	fmt.Fprintf(&details, "%d upgrade failure(s) occurred.", len(errors))
	return fmt.Errorf("%s", details.String())
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

func printPluginRows(stdout io.Writer, rows []plugin.PluginSummary) error {
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
	var writeErr error
	writef := func(format string, args ...any) {
		if writeErr != nil {
			return
		}
		_, writeErr = fmt.Fprintf(stdout, format, args...)
	}
	for i, header := range headers {
		if i > 0 {
			writef("  ")
		}
		writef("%-*s", widths[i], header)
	}
	writef("\n")
	for _, row := range converted {
		for i, cell := range row {
			if i > 0 {
				writef("  ")
			}
			writef("%-*s", widths[i], cell)
		}
		writef("\n")
	}
	return writeErr
}

func firstNonEmptyLocal(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
