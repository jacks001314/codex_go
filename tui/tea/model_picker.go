package tea

import (
	"strings"

	bubbletea "charm.land/bubbletea/v2"

	"codex_go/config"
	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
	"codex_go/tui/history_cell"
)

// modelPickerCatalogSnapshot captures one backend's picker catalog
// (Rust #63553 ModelCatalog is stored per backend).
type modelPickerCatalogSnapshot struct {
	applied        []codextui.ModelPickerOption
	provider       string
	byProvider     map[string][]codextui.ModelPickerOption
	unscoped       map[string]bool
	refreshPending bool
}

func cloneModelPickerProviderCatalogs(catalogs map[string][]codextui.ModelPickerOption) map[string][]codextui.ModelPickerOption {
	if catalogs == nil {
		return nil
	}
	cloned := make(map[string][]codextui.ModelPickerOption, len(catalogs))
	for provider, options := range catalogs {
		cloned[provider] = append([]codextui.ModelPickerOption(nil), options...)
	}
	return cloned
}

func cloneModelPickerUnscoped(unscoped map[string]bool) map[string]bool {
	if unscoped == nil {
		return nil
	}
	cloned := make(map[string]bool, len(unscoped))
	for provider, value := range unscoped {
		cloned[provider] = value
	}
	return cloned
}

// syncModelPickerCatalogBackend moves the applied catalog and its provider
// cache to the backend the TUI now displays, restoring that backend's own
// snapshot. A catalog request issued for the previous backend can no longer
// land here.
func (m *Model) syncModelPickerCatalogBackend() {
	if m == nil || m.modelPickerCatalogBackend == m.backendID {
		return
	}
	if m.modelPickerCatalogBackend.IsZero() {
		// The initial catalog belongs to the backend now displayed; adopt it
		// instead of stashing it under the unset identity.
		m.modelPickerCatalogBackend = m.backendID
		return
	}
	if m.modelPickerCatalogsStash == nil {
		m.modelPickerCatalogsStash = map[codextui.BackendID]modelPickerCatalogSnapshot{}
	}
	m.modelPickerCatalogsStash[m.modelPickerCatalogBackend] = modelPickerCatalogSnapshot{
		applied:        append([]codextui.ModelPickerOption(nil), m.modelPickerOpts...),
		provider:       m.modelPickerProvider,
		byProvider:     cloneModelPickerProviderCatalogs(m.modelPickerOptsByProvider),
		unscoped:       cloneModelPickerUnscoped(m.modelPickerUnscoped),
		refreshPending: m.modelCatalogRefreshPending,
	}
	if snapshot, ok := m.modelPickerCatalogsStash[m.backendID]; ok {
		m.modelPickerOpts = append([]codextui.ModelPickerOption(nil), snapshot.applied...)
		m.modelPickerProvider = snapshot.provider
		m.modelPickerOptsByProvider = cloneModelPickerProviderCatalogs(snapshot.byProvider)
		m.modelPickerUnscoped = cloneModelPickerUnscoped(snapshot.unscoped)
		m.modelCatalogRefreshPending = snapshot.refreshPending
		delete(m.modelPickerCatalogsStash, m.backendID)
	} else {
		m.modelPickerOpts = nil
		m.modelPickerProvider = ""
		m.modelPickerOptsByProvider = nil
		m.modelPickerUnscoped = nil
		m.modelCatalogRefreshPending = false
	}
	m.modelPickerCatalogBackend = m.backendID
}

func (m *Model) openModelPicker() bubbletea.Cmd {
	m.syncModelPickerCatalogBackend()
	if message := m.modelPickerUnavailable(); message != "" {
		// Rust #63553 model_picker_unavailable: a provider save in flight or a
		// changed default makes this list unsafe to offer.
		m.notice = message
		m.refreshTranscript()
		return nil
	}
	options := m.modelPickerOptionsFor(m.modelListProvider())
	if len(options) == 0 && m.onListModels != nil {
		// Rust #63553 model_popups: an uncached provider opens a loading view
		// while its catalog is fetched, instead of offering bundled presets
		// that may not belong to the provider.
		m.openModal(ModalRequestMsg{
			ID:      "model-picker",
			Kind:    ModalKindPicker,
			Title:   "Select Model and Effort",
			Options: []ModalOption{{ID: "loading", Label: "Loading models\u2026", Disabled: true}},
		})
		if m.modal != nil {
			m.modal.modelPickerLoading = true
		}
		return m.fetchModelsForPicker()
	}
	if len(options) == 0 {
		options = codextui.BundledModelPickerOptions()
	}
	picker := codextui.NewModelPicker(options, m.State.Model)
	if picker == nil || len(picker.Options) == 0 {
		m.notice = "No models are available right now."
		return m.fetchModelsForPicker()
	}
	modalOptions := make([]ModalOption, 0, len(picker.Options))
	for i, option := range picker.Options {
		modalOptions = append(modalOptions, m.modelPickerModalOption(option, i))
	}
	m.openModal(ModalRequestMsg{
		ID:      "model-picker",
		Kind:    ModalKindPicker,
		Title:   "Select Model and Effort",
		Options: modalOptions,
	})
	if m.modal != nil {
		m.modal.modelPicker = picker
		m.modal.selected = picker.Selected
	}
	return m.fetchModelsForPicker()
}

// modelPickerUnavailable mirrors Rust #63553 ModelCatalog::provider_write and
// model_picker_unavailable: a save in flight blocks /model until it finishes,
// and a confirmed or possible switch leaves the untagged list out of date.
func (m *Model) modelPickerUnavailable() string {
	if m == nil {
		return ""
	}
	m.syncModelPickerCatalogBackend()
	if m.modelProviderWritePending[m.backendID] {
		return "Saving the default provider. Try /model after the save finishes."
	}
	if m.modelProviderWriteChanged[m.backendID] {
		return "The model list may be out of date. Restart the app server and TUI to refresh /model."
	}
	return ""
}

// markModelProviderWritePending holds the backend's provider_write lock
// (Rust #63553 write.rs).
func (m *Model) markModelProviderWritePending(backend codextui.BackendID) {
	if m == nil {
		return
	}
	if m.modelProviderWritePending == nil {
		m.modelProviderWritePending = map[codextui.BackendID]bool{}
	}
	m.modelProviderWritePending[backend] = true
}

// finishModelProviderWrite releases the backend's lock and, when the default
// may have changed, keeps its untagged catalog unsafe for the session.
func (m *Model) finishModelProviderWrite(backend codextui.BackendID, changed bool) {
	if m == nil {
		return
	}
	if m.modelProviderWritePending != nil {
		delete(m.modelProviderWritePending, backend)
	}
	if !changed {
		return
	}
	if m.modelProviderWriteChanged == nil {
		m.modelProviderWriteChanged = map[codextui.BackendID]bool{}
	}
	m.modelProviderWriteChanged[backend] = true
}

// fetchModelsForPicker asynchronously fetches the current model catalog from the
// app server so the picker does not rely on a stale startup catalog (Rust
// #41467). It returns nil when no catalog callback is configured.
func (m *Model) fetchModelsForPicker() bubbletea.Cmd {
	if m == nil || m.onListModels == nil {
		return nil
	}
	m.nextModelsRequestID++
	requestID := m.nextModelsRequestID
	m.pendingModelsRequestID = requestID
	m.pendingModelsRequestBackend = m.backendID
	cwd := m.modelListCWD()
	provider := m.modelListProvider()
	if m.onListModelsScoped != nil {
		scopedLoader := m.onListModelsScoped
		return func() bubbletea.Msg {
			options, scoped, err := scopedLoader(cwd, provider, true)
			return ModelsResultMsg{RequestID: requestID, Provider: provider, Options: options, Err: err, Scoped: scoped, ScopedKnown: true}
		}
	}
	return func() bubbletea.Msg {
		options, err := m.onListModels(cwd, provider, true)
		return ModelsResultMsg{RequestID: requestID, Provider: provider, Options: options, Err: err}
	}
}

// modelListCWD is the working directory whose model catalog should be read
// (Rust #55682: an attached session's project replaces the launch directory).
func (m *Model) modelListCWD() string {
	if m == nil {
		return ""
	}
	if cwd := strings.TrimSpace(m.sessionCWD); cwd != "" {
		return cwd
	}
	if m.State != nil {
		return strings.TrimSpace(m.State.CWD)
	}
	return ""
}

// clearModelPickerCatalogs drops every cached provider catalog
// (Rust #63553 ModelCatalog::clear_models). The last applied list stays so the
// picker can still open while its refresh is in flight; the next response
// repopulates the per-provider cache.
func (m *Model) clearModelPickerCatalogs() {
	if m == nil {
		return
	}
	m.syncModelPickerCatalogBackend()
	m.modelPickerOptsByProvider = nil
}

// rememberModelPickerScope records whether a provider's catalog honored the
// provider scope (Rust #63553 model_catalog.provider).
func (m *Model) rememberModelPickerScope(provider string, scoped bool) {
	if m == nil {
		return
	}
	provider = strings.TrimSpace(provider)
	if scoped {
		delete(m.modelPickerUnscoped, provider)
		return
	}
	if m.modelPickerUnscoped == nil {
		m.modelPickerUnscoped = map[string]bool{}
	}
	m.modelPickerUnscoped[provider] = true
}

// rememberModelPickerOptions caches one provider catalog (Rust #63553
// ModelCatalog::set_models). The empty key is the app-server default catalog a
// new thread reads.
func (m *Model) rememberModelPickerOptions(provider string, options []codextui.ModelPickerOption) {
	if m == nil {
		return
	}
	if m.modelPickerOptsByProvider == nil {
		m.modelPickerOptsByProvider = map[string][]codextui.ModelPickerOption{}
	}
	m.modelPickerOptsByProvider[strings.TrimSpace(provider)] = append([]codextui.ModelPickerOption(nil), options...)
}

// selectModelPickerProvider switches the catalog every picker surface reads to
// a provider catalog (Rust #63553 ModelCatalog::select_provider and
// set_model_provider). Rust dismisses a stale model picker and reads the
// provider's own list, so an uncached provider must not keep showing the
// previous provider's models; the refresh flag fetches its catalog next.
func (m *Model) selectModelPickerProvider(provider string) {
	if m == nil {
		return
	}
	m.syncModelPickerCatalogBackend()
	provider = strings.TrimSpace(provider)
	if provider == m.modelPickerProvider {
		return
	}
	// Until a thread names its provider, the applied catalog is the server
	// default's unscoped list (the empty cache key). A thread on that same
	// default only labels the list; every other provider owns a catalog.
	labelOnly := m.modelPickerProvider == "" && strings.EqualFold(provider, strings.TrimSpace(m.defaultModelProvider))
	if !labelOnly {
		m.dismissStaleModelPicker()
	}
	m.modelPickerProvider = provider
	if cached, ok := m.modelPickerOptsByProvider[provider]; ok {
		m.modelPickerOpts = append([]codextui.ModelPickerOption(nil), cached...)
	} else if !labelOnly {
		m.modelPickerOpts = nil
		m.modelCatalogRefreshPending = true
	}
	if m.modelAccountUsesAPIKey() {
		// Rust #63553 refresh_account_scoped_models: API-key access metadata is
		// scoped to the thread and working directory, so a provider switch drops
		// the cached programs and reads the catalog again.
		m.clearModelPickerCyberPrograms(provider)
		m.modelCatalogRefreshPending = true
	}
	m.refreshModelPicker()
	m.refreshServiceTierCommands()
}

// modelAccountUsesAPIKey reports Rust's StatusAccountDisplay::ApiKey, which
// scopes catalog access metadata to the thread and working directory.
func (m *Model) modelAccountUsesAPIKey() bool {
	if m == nil || m.State == nil {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(m.State.AccountDisplay), "API key configured")
}

// clearModelPickerCyberPrograms drops the catalog-advertised cyber programs
// from the applied and cached options for one provider.
func (m *Model) clearModelPickerCyberPrograms(provider string) {
	if m == nil {
		return
	}
	for i := range m.modelPickerOpts {
		m.modelPickerOpts[i].AvailableCyberPrograms = nil
	}
	provider = strings.TrimSpace(provider)
	cached, ok := m.modelPickerOptsByProvider[provider]
	if !ok {
		return
	}
	cached = append([]codextui.ModelPickerOption(nil), cached...)
	for i := range cached {
		cached[i].AvailableCyberPrograms = nil
	}
	if m.modelPickerOptsByProvider == nil {
		m.modelPickerOptsByProvider = map[string][]codextui.ModelPickerOption{}
	}
	m.modelPickerOptsByProvider[provider] = cached
}

// dismissStaleModelPicker closes a model picker bound to the previous
// provider (Rust #63553 dismiss_stale_model_picker). Its rows no longer
// describe the active thread's provider.
func (m *Model) dismissStaleModelPicker() {
	if m == nil || m.modal == nil || m.modal.kind != ModalKindPicker {
		return
	}
	switch m.modal.id {
	case "model-picker", "model-reasoning-picker", "advanced-reasoning-picker", "plan-reasoning-scope-picker":
		m.modal = nil
		m.notice = ""
	}
}

// consumeModelCatalogRefresh issues the catalog fetch a provider switch
// asked for (Rust #63553 refresh_account_scoped_models).
func (m *Model) consumeModelCatalogRefresh() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	m.syncModelPickerCatalogBackend()
	if !m.modelCatalogRefreshPending {
		return nil
	}
	m.modelCatalogRefreshPending = false
	return m.fetchModelsForPicker()
}

// modelPickerOptionsFor returns the cached catalog for provider. The last
// applied catalog stands in only for the provider it was read from, so a
// switch to an uncached provider does not offer another provider's models
// (Rust #63553 ModelCatalog::models_for).
func (m *Model) modelPickerOptionsFor(provider string) []codextui.ModelPickerOption {
	if m == nil {
		return nil
	}
	m.syncModelPickerCatalogBackend()
	provider = strings.TrimSpace(provider)
	if cached, ok := m.modelPickerOptsByProvider[provider]; ok {
		return append([]codextui.ModelPickerOption(nil), cached...)
	}
	if provider == m.modelPickerProvider {
		return append([]codextui.ModelPickerOption(nil), m.modelPickerOpts...)
	}
	return nil
}

// modelListProvider is the model provider whose catalog the picker should read
// (Rust #63553: the active thread can use a different provider from the
// app-server default). An empty provider keeps the configured default catalog
// for new threads.
func (m *Model) modelListProvider() string {
	if m == nil || m.State == nil {
		return ""
	}
	return strings.TrimSpace(m.State.Provider)
}

// applyModelsResult applies a fetched model catalog to the picker options and
// refreshes an open model picker in place (Rust #41467). Stale, failed, and
// empty responses are ignored.
func (m *Model) applyModelsResult(msg ModelsResultMsg) {
	if m == nil {
		return
	}
	m.syncModelPickerCatalogBackend()
	if msg.RequestID == 0 || msg.RequestID != m.pendingModelsRequestID {
		return
	}
	if !m.pendingModelsRequestBackend.IsZero() && m.pendingModelsRequestBackend != m.backendID {
		// The request belongs to a backend this TUI no longer displays.
		m.pendingModelsRequestID = 0
		m.pendingModelsRequestBackend = codextui.BackendID{}
		return
	}
	m.pendingModelsRequestID = 0
	m.pendingModelsRequestBackend = codextui.BackendID{}
	if msg.Err != nil || len(msg.Options) == 0 {
		// Rust on_models_loaded: a failed load replaces the loading view with an
		// error instead of leaving it spinning.
		if m.modal != nil && m.modal.modelPickerLoading && strings.TrimSpace(msg.Provider) == m.modelListProvider() {
			m.modal = nil
			m.notice = "Could not load models. Try /model again."
			m.refreshTranscript()
		}
		return
	}
	// Rust #63553: after a confirmed or possible default-provider switch the
	// server's untagged list may describe the old default, so it is never
	// applied again in this session.
	if m.modelProviderWriteChanged[m.backendID] && strings.TrimSpace(msg.Provider) == "" {
		return
	}
	// Rust #51253 (app_server_session bootstrap): the catalog the server hands
	// back is constrained by the server's requirements before the picker and the
	// service-tier commands read it, so an older server's shared Fast gate (and
	// a server that allows the independent Ultra Fast policy) both hold here.
	options := codextui.ConstrainServerServiceTierOptions(append([]codextui.ModelPickerOption(nil), msg.Options...), m.serviceTierRequirements)
	m.rememberModelPickerOptions(msg.Provider, options)
	if msg.ScopedKnown {
		// Rust #63553 records whether the catalog could be scoped; an unscoped
		// non-Cloud catalog keeps model choices on the current session.
		m.rememberModelPickerScope(msg.Provider, msg.Scoped)
	}
	// A response for another provider stays cached without replacing the
	// catalog the current thread reads (Rust #63553 keeps one catalog per
	// provider).
	if msg.Provider == m.modelListProvider() {
		m.modelPickerOpts = options
		m.modelPickerProvider = strings.TrimSpace(msg.Provider)
		m.refreshModelPicker()
		m.refreshServiceTierCommands()
	}
}

// refreshServiceTierCommands recomputes the slash-command service-tier entries
// for the current model from the model picker catalog (Rust #41467
// sync_service_tier_commands / set_model). It is called when the selected model
// changes or the catalog refreshes so the fast-mode / slash-command surface
// stays in sync with the active model's service tiers.
func (m *Model) refreshServiceTierCommands() {
	if m == nil || m.State == nil {
		return
	}
	m.syncModelPickerCatalogBackend()
	options := m.modelPickerOpts
	if len(options) == 0 {
		options = codextui.BundledModelPickerOptions()
	}
	for _, option := range options {
		if option.ID == m.State.Model {
			commands := serviceTierCommandsForCatalog(option)
			commands = serviceTierCommandsForPolicy(m.featureSettings, commands)
			m.serviceTierCommands = constrainServiceTierCommandsByRequirements(m.serviceTierRequirements, commands)
			m.refreshEffectiveServiceTier(option)
			return
		}
	}
}

// refreshEffectiveServiceTier recomputes the session tier after the model or
// catalog changed (Rust #50472 set_model -> refresh_effective_service_tier): a
// saved tier the new model does not advertise clears, and a model with no saved
// selection falls back to its catalog default.
func (m *Model) refreshEffectiveServiceTier(option codextui.ModelPickerOption) {
	if m == nil || m.State == nil {
		return
	}
	m.State.ServiceTier = codextui.EffectiveServiceTier(m.savedServiceTier, option, m.featureSettings)
}

// serviceTierCommandsForCatalog builds the slash-command entries from the
// model's catalog tier metadata, lowercasing each name and keeping the
// catalog's description (Rust #50472 service_tiers.rs
// current_model_service_tier_commands). Options without catalog definitions
// keep the ID-based fallback.
func serviceTierCommandsForCatalog(option codextui.ModelPickerOption) []bottompane.ServiceTierCommand {
	const defaultRequestValue = "default"
	if len(option.ServiceTierDefinitions) == 0 {
		return bottompane.ServiceTierCommandsFromIDs(option.ServiceTiers)
	}
	commands := make([]bottompane.ServiceTierCommand, 0, len(option.ServiceTierDefinitions))
	for _, tier := range option.ServiceTierDefinitions {
		id := strings.TrimSpace(tier.ID)
		if id == "" || id == defaultRequestValue {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(tier.Name))
		if name == "" {
			name = id
		}
		commands = append(commands, bottompane.ServiceTierCommand{
			ID:          id,
			Name:        name,
			Description: tier.Description,
		})
	}
	return commands
}

// constrainServiceTierCommandsByRequirements drops the service tiers the
// server's reported requirements do not allow (Rust #51253
// constrain_server_service_tiers). It is the command-list form of the catalog
// constraining: the startup `ServiceTierCommands` option is not derived from the
// picker catalog, so it is filtered with the same policy here.
func constrainServiceTierCommandsByRequirements(requirements *config.ConfigRequirementsReadResponse, commands []bottompane.ServiceTierCommand) []bottompane.ServiceTierCommand {
	tierEnabled, ok := codextui.ServiceTierPolicyFromRequirements(requirements)
	if !ok {
		return commands
	}
	filtered := make([]bottompane.ServiceTierCommand, 0, len(commands))
	for _, command := range commands {
		if tierEnabled(command.ID) {
			filtered = append(filtered, command)
		}
	}
	return filtered
}

// serviceTierCommandsForPolicy drops the service tiers the selected policies
// disable (Rust #51253 current_model_service_tier_commands: `.filter(|tier|
// self.config.features.service_tier_enabled(&tier.id))`). A tier follows the
// policy that owns it, so disabling fast_mode alone keeps Ultra Fast available
// and vice versa.
func serviceTierCommandsForPolicy(featureSettings map[string]bool, commands []bottompane.ServiceTierCommand) []bottompane.ServiceTierCommand {
	filtered := make([]bottompane.ServiceTierCommand, 0, len(commands))
	for _, command := range commands {
		if codextui.ServiceTierEnabled(featureSettings, command.ID) {
			filtered = append(filtered, command)
		}
	}
	return filtered
}

// serviceTierPolicyFeatureChanged mirrors Rust ChatWidget::set_feature_enabled
// (#51253, chatwidget/settings.rs): a Fast or Ultra Fast policy change refreshes
// the effective tier and the service-tier commands, while other features leave
// them alone.
func serviceTierPolicyFeatureChanged(before map[string]bool, after map[string]bool) bool {
	for _, key := range []string{codextui.FeatureKeyFastMode, codextui.FeatureKeyUltrafastMode} {
		beforeValue, beforeSet := before[key]
		afterValue, afterSet := after[key]
		if beforeSet != afterSet || beforeValue != afterValue {
			return true
		}
	}
	return false
}

// refreshModelPicker rebuilds an open model picker from the updated catalog,
// preserving the highlighted model.
func (m *Model) refreshModelPicker() {
	if m == nil || m.modal == nil {
		return
	}
	m.syncModelPickerCatalogBackend()
	if m.modal.modelPickerLoading {
		// Rust on_models_loaded dismisses the loading view and opens the fetched
		// presets in place.
		if len(m.modelPickerOpts) == 0 {
			return
		}
		m.modal.modelPickerLoading = false
		if picker := codextui.NewModelPicker(append([]codextui.ModelPickerOption(nil), m.modelPickerOpts...), m.State.Model); picker != nil {
			m.modal.modelPicker = picker
			m.modal.selected = picker.Selected
		}
	}
	// Rust #41467: apply accepted catalog updates to the new-thread default. If
	// the current model is no longer available, fall back to the catalog default
	// (or the first available model).
	if m.State != nil && !m.modelIsAvailable(m.State.Model) {
		fallback := ""
		for _, option := range m.modelPickerOpts {
			if option.IsDefault {
				fallback = option.ID
				break
			}
		}
		if fallback == "" && len(m.modelPickerOpts) > 0 {
			fallback = m.modelPickerOpts[0].ID
		}
		if fallback != "" {
			m.State.Model = fallback
		}
	}
	// Rust #41467: refresh an open reasoning submenu in place even when the model
	// picker modal has been replaced by the reasoning modal.
	if m.modal.modelReasoning != nil && m.modal.modelReasoning.Model.ID != "" {
		for _, option := range m.modelPickerOpts {
			if option.ID == m.modal.modelReasoning.Model.ID {
				if m.modal.advancedReasoning {
					option, _ = m.advancedReasoningModelOption(option)
				}
				if refreshed := codextui.NewModelReasoningPicker(option, m.State.EffectiveReasoningEffort()); refreshed != nil {
					m.modal.modelReasoning = refreshed
					m.modal.options = m.modelReasoningModalOptions(refreshed)
				}
				break
			}
		}
	}
	if m.modal.modelPicker == nil {
		return
	}
	picker := codextui.NewModelPicker(append([]codextui.ModelPickerOption(nil), m.modelPickerOpts...), m.State.Model)
	if picker == nil || len(picker.Options) == 0 {
		return
	}
	selected := m.modal.selected
	if selected >= len(picker.Options) {
		selected = len(picker.Options) - 1
	}
	m.modal.modelPicker = picker
	m.modal.selected = selected
	m.modal.modelPicker.Select(selected)
	modalOptions := make([]ModalOption, 0, len(picker.Options))
	for i, option := range picker.Options {
		modalOptions = append(modalOptions, m.modelPickerModalOption(option, i))
	}
	m.modal.options = modalOptions
}

// modelIsAvailable reports whether model is present in the refreshed catalog.
func (m *Model) modelIsAvailable(model string) bool {
	if strings.TrimSpace(model) == "" {
		return true
	}
	for _, option := range m.modelPickerOpts {
		if option.ID == model {
			return true
		}
	}
	return false
}

func (m *Model) openModelReasoningPicker(option codextui.ModelPickerOption) {
	picker := codextui.NewModelReasoningPicker(option, m.State.EffectiveReasoningEffort())
	if picker == nil || len(picker.Options) == 0 {
		m.State.Model = option.ID
		m.refreshServiceTierCommands()
		m.notice = strings.TrimSpace(m.State.RenderSetting("Model", m.State.Model))
		return
	}
	modalOptions := m.modelReasoningModalOptions(picker)
	m.openModal(ModalRequestMsg{
		ID:      "model-reasoning-picker",
		Kind:    ModalKindPicker,
		Title:   "Select Reasoning Level for " + option.ID,
		Body:    "Pick the reasoning effort for following turns.",
		Options: modalOptions,
	})
	if m.modal != nil {
		m.modal.modelReasoning = picker
		m.modal.selected = picker.Selected
		m.modal.advancedReasoning = false
	}
}

func (m *Model) advancedReasoningModelOption(option codextui.ModelPickerOption) (codextui.ModelPickerOption, bool) {
	choices := option.AdvancedReasoningEfforts()
	if len(choices) == 0 {
		return option, false
	}
	option.SupportedReasoningEfforts = choices
	option.DefaultReasoningEffort = choices[0].Effort
	return option, true
}

// openAdvancedReasoningPicker mirrors Rust open_advanced_reasoning_popup: the
// parent model row opens a Max/Ultra-only submenu where every final choice has
// the session-only shortcut.
func (m *Model) openAdvancedReasoningPicker(option codextui.ModelPickerOption) {
	option, ok := m.advancedReasoningModelOption(option)
	if !ok {
		m.openModelReasoningPicker(option)
		return
	}
	picker := codextui.NewModelReasoningPicker(option, m.State.EffectiveReasoningEffort())
	if picker == nil || len(picker.Options) == 0 {
		return
	}
	m.openModal(ModalRequestMsg{
		ID:      "advanced-reasoning-picker",
		Kind:    ModalKindPicker,
		Title:   "Advanced Reasoning",
		Body:    "\u26a0 Consumes usage limits faster",
		Options: m.modelReasoningModalOptions(picker),
	})
	if m.modal != nil {
		m.modal.modelReasoning = picker
		m.modal.selected = picker.Selected
		m.modal.advancedReasoning = true
	}
}

func (m *Model) openPlanReasoningScopePicker(option codextui.ModelPickerOption, effort string) {
	picker := codextui.NewPlanReasoningScopePicker(option, effort, m.State.PlanModeReasoningEffort)
	if picker == nil || len(picker.Options) == 0 {
		m.State.Model = option.ID
		m.State.PlanModeReasoningEffort = strings.TrimSpace(effort)
		m.refreshServiceTierCommands()
		m.notice = strings.TrimSpace(m.State.RenderSetting("Plan Reasoning", m.State.PlanModeReasoningEffort))
		return
	}
	modalOptions := make([]ModalOption, 0, len(picker.Options))
	for i, option := range picker.Options {
		modalOptions = append(modalOptions, ModalOption{
			ID:          string(option.Scope),
			Label:       option.Label,
			Description: option.Description,
			Shortcut:    modelPickerShortcut(i),
		})
	}
	body := "Choose where to apply " + codextui.ReasoningEffortLabel(effort) + " reasoning."
	if strings.TrimSpace(effort) == "" {
		body = "Choose where to apply the selected reasoning."
	}
	m.openModal(ModalRequestMsg{
		ID:      "plan-reasoning-scope-picker",
		Kind:    ModalKindPicker,
		Title:   codextui.PlanReasoningScopeTitle,
		Body:    body,
		Options: modalOptions,
	})
	if m.modal != nil {
		m.modal.planReasoningScope = picker
		m.modal.selected = picker.Selected
	}
}

func modelPickerLabel(option codextui.ModelPickerOption) string {
	label := strings.TrimSpace(option.Label)
	if label == "" {
		label = option.ID
	}
	// Rust #51235: model rows carry "(current)" and nothing else; the catalog
	// default is still tracked by ModelPickerOption.IsDefault for preselection.
	if option.IsCurrent {
		label += " (current)"
	}
	return label
}

func modelPickerShortcut(index int) string {
	if index >= 0 && index < 9 {
		return string(rune('1' + index))
	}
	return ""
}

// ModelCatalogResultMsg carries the hidden-inclusive model catalog used to
// resolve backend-authorized model switches (the Reserve model is picker-hidden).
type ModelCatalogResultMsg struct {
	RequestID uint64
	Options   []codextui.ModelPickerOption
	Err       error
}

// fetchModelCatalog asynchronously fetches the hidden-inclusive catalog. It
// returns nil when no catalog callback is configured.
func (m *Model) fetchModelCatalog() bubbletea.Cmd {
	if m == nil || m.onListModels == nil {
		return nil
	}
	m.nextModelCatalogRequestID++
	requestID := m.nextModelCatalogRequestID
	m.pendingModelCatalogRequestID = requestID
	cwd := m.modelListCWD()
	provider := m.modelListProvider()
	return func() bubbletea.Msg {
		options, err := m.onListModels(cwd, provider, true)
		return ModelCatalogResultMsg{RequestID: requestID, Options: options, Err: err}
	}
}

// applyModelCatalogResult stores the hidden-inclusive catalog. Stale, failed,
// and empty responses are ignored.
func (m *Model) applyModelCatalogResult(msg ModelCatalogResultMsg) {
	if m == nil || msg.RequestID == 0 || msg.RequestID != m.pendingModelCatalogRequestID {
		return
	}
	m.pendingModelCatalogRequestID = 0
	if msg.Err != nil || len(msg.Options) == 0 {
		return
	}
	// Rust #51253: the hidden-inclusive catalog is constrained the same way as
	// the picker catalog (the Reserve model is picker-hidden but still resolves
	// through this list).
	m.modelCatalogOpts = codextui.ConstrainServerServiceTierOptions(append([]codextui.ModelPickerOption(nil), msg.Options...), m.serviceTierRequirements)
	m.warnIfChatGPTModelMissingFromCatalog()
}

// warnIfChatGPTModelMissingFromCatalog mirrors Rust #60270: an explicitly
// selected CLI model that the ChatGPT catalog does not list is probably a typo,
// so the transcript explains it instead of silently falling back.
func (m *Model) warnIfChatGPTModelMissingFromCatalog() {
	if m == nil || m.State == nil || m.warnedMissingChatGPTModel || len(m.modelCatalogOpts) == 0 {
		return
	}
	model := strings.TrimSpace(m.State.Model)
	if model == "" || !m.hasChatGPTAccount || !strings.EqualFold(strings.TrimSpace(m.State.Provider), "openai") {
		return
	}
	for _, option := range m.modelCatalogOpts {
		if strings.TrimSpace(option.ID) == model {
			return
		}
	}
	m.warnedMissingChatGPTModel = true
	m.applyHistoryCell(historycell.NewWarningEvent(
		"Your ChatGPT model list doesn't include `" + model + "`. Check the spelling or use /model to choose another model.",
	))
}
