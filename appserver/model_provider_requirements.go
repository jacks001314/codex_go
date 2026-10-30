package appserver

import (
	"context"
	"errors"
	"reflect"
	"strings"

	"codex_go/config"
	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

// errModelProviderRequirementsChanged mirrors Rust's ModelProviderRequirementsChanged
// (#44944). Existing threads retain the model provider route they started with;
// a managed policy change invalidates that route until Codex restarts.
var errModelProviderRequirementsChanged = errors.New(
	"Your organization's required model provider settings changed. Restart Codex to apply them; this request was not sent",
)

// threadModelProviderRoute is the provider selection and definition a live
// thread was admitted with (Rust keeps the resolved thread Config for the same
// purpose).
type threadModelProviderRoute struct {
	providerID string
	provider   model.ProviderInfo
}

// captureThreadModelProviderRouteByID resolves a thread record and records its
// retained provider route.
func (r *RuntimeRouter) captureThreadModelProviderRouteByID(threadID string) {
	threadID = strings.TrimSpace(threadID)
	if r == nil || threadID == "" {
		return
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil || record == nil {
		return
	}
	r.captureThreadModelProviderRoute(threadID, record)
}

// ModelProviderErrorKind identifies the recoverable provider failures Rust
// shares between resume and inference admission (#57840).
type ModelProviderErrorKind string

const (
	// ModelProviderMismatch means the chat's provider is not the one the
	// current configuration selects.
	ModelProviderMismatch ModelProviderErrorKind = "modelProviderMismatch"
	// ModelProviderNotConfigured means the chat's provider no longer exists in
	// the configured or built-in provider catalog.
	ModelProviderNotConfigured ModelProviderErrorKind = "modelProviderNotConfigured"
	// ModelProviderPolicyConflict means managed requirements select or define a
	// different provider than this chat uses.
	ModelProviderPolicyConflict ModelProviderErrorKind = "modelProviderPolicyConflict"
)

// ModelProviderError mirrors Rust's `ModelProviderError` protocol type
// (#57840): a structured JSON-RPC error body clients can act on instead of
// parsing prose. Unknown errors keep the client's normal fallback.
type ModelProviderError struct {
	Kind                  ModelProviderErrorKind
	ModelProvider         string
	CurrentModelProvider  string
	RequiredModelProvider string
}

func (e *ModelProviderError) Error() string {
	if e == nil {
		return ""
	}
	switch e.Kind {
	case ModelProviderMismatch:
		return "This chat uses " + e.ModelProvider + ", but your current provider is " + e.CurrentModelProvider + ". Switch to " + e.ModelProvider + " to continue."
	case ModelProviderNotConfigured:
		return "This chat uses model provider \u201c" + e.ModelProvider + "\u201d, which is no longer configured. Restore this provider\u2019s configuration to continue."
	case ModelProviderPolicyConflict:
		if e.ModelProvider != e.RequiredModelProvider {
			return "This chat uses model provider \u201c" + e.ModelProvider + "\u201d, but your organization requires \u201c" + e.RequiredModelProvider + "\u201d. Contact your administrator or start a new chat using the required provider."
		}
		return "This chat\u2019s model provider settings no longer match your organization\u2019s requirements. Contact your administrator or start a new chat using the current provider settings."
	default:
		return "Model provider unavailable"
	}
}

// JSONRPCErrorData returns the structured `error.data` body Rust serializes for
// the provider failure.
func (e *ModelProviderError) JSONRPCErrorData() map[string]any {
	if e == nil {
		return nil
	}
	data := map[string]any{"type": string(e.Kind), "modelProvider": e.ModelProvider}
	switch e.Kind {
	case ModelProviderMismatch:
		data["currentModelProvider"] = e.CurrentModelProvider
	case ModelProviderPolicyConflict:
		data["requiredModelProvider"] = e.RequiredModelProvider
	}
	return data
}

// configLoadInvalidRequest mirrors Rust's config_load_error: recoverable
// provider failures surface as invalid requests carrying structured data,
// everything else keeps the stable configuration prefix (#57840).
func configLoadInvalidRequest(err error) error {
	if err == nil {
		return nil
	}
	var providerErr *ModelProviderError
	if errors.As(err, &providerErr) {
		return &configProviderError{provider: providerErr}
	}
	return jsonRPCInvalidRequest("failed to load configuration: " + err.Error())
}

// configProviderError keeps Rust's provider error message (without the
// configuration prefix) while preserving the invalid-request wire code and the
// structured error data.
type configProviderError struct {
	provider *ModelProviderError
}

func (e *configProviderError) Error() string {
	if e == nil || e.provider == nil {
		return ""
	}
	return e.provider.Error()
}

func (e *configProviderError) Unwrap() []error {
	if e == nil || e.provider == nil {
		return []error{ErrJSONRPCInvalidRequest}
	}
	return []error{e.provider, ErrJSONRPCInvalidRequest}
}

func (e *configProviderError) Is(target error) bool {
	return target == ErrJSONRPCInvalidRequest || target == ErrInvalidRequest
}

func (e *configProviderError) JSONRPCErrorData() map[string]any {
	if e == nil || e.provider == nil {
		return nil
	}
	return e.provider.JSONRPCErrorData()
}

// managedModelProviderRequirements loads managed requirements independently of
// user, project, system-defaults, and thread configuration (#44944).
func (r *RuntimeRouter) managedModelProviderRequirements() (*config.ConfigRequirements, error) {
	if r == nil || r.services.Config == nil {
		return nil, nil
	}
	home := strings.TrimSpace(r.services.Config.CodexHome())
	if home == "" {
		return nil, nil
	}
	return config.LoadManagedRequirements(home, nil)
}

// captureThreadModelProviderRoute records the provider route a thread starts
// with so later managed requirement changes can be detected.
func (r *RuntimeRouter) captureThreadModelProviderRoute(threadID string, record *session.Record) {
	if r == nil || record == nil || r.services.ThreadRouter == nil || r.services.ThreadRouter.threads == nil {
		return
	}
	tid := strings.TrimSpace(threadID)
	if tid == "" {
		tid = string(record.ID)
	}
	if tid == "" {
		return
	}
	params := &turn.TurnStartParams{ThreadID: tid, Config: threadRecordConfigOverrides(record)}
	if cwd := strings.TrimSpace(record.Metadata.CWD); cwd != "" {
		params.CWD = cwd
	}
	if modelID := strings.TrimSpace(record.Metadata.Model); modelID != "" {
		params.Model = modelID
	}
	cfg, err := r.effectiveConfigForTurn(params)
	if err != nil || cfg == nil {
		return
	}
	providerID := firstNonEmpty(
		strings.TrimSpace(record.Metadata.ModelProvider),
		providerFromTurnStart(params),
		stringConfigValue(cfg, "model_provider"),
		stringConfigValue(cfg, "modelProvider"),
		model.OpenAIProviderID,
	)
	provider, err := model.ProviderForConfigID(configValues(cfg), providerID, stringConfigValue(cfg, "openai_base_url"))
	if err != nil || provider == nil {
		return
	}
	r.services.ThreadRouter.threads.SetModelProviderRoute(session.ThreadID(tid), threadModelProviderRoute{
		providerID: providerID,
		provider:   *provider,
	})
}

func (r *RuntimeRouter) threadModelProviderRoute(record *session.Record) (threadModelProviderRoute, bool) {
	if r == nil || record == nil || r.services.ThreadRouter == nil || r.services.ThreadRouter.threads == nil {
		return threadModelProviderRoute{}, false
	}
	if route, ok := r.services.ThreadRouter.threads.ModelProviderRoute(record.ID); ok {
		return route, true
	}
	// Threads that were admitted before the route registry existed fall back to
	// the current route so the check stays available.
	r.captureThreadModelProviderRoute(string(record.ID), record)
	return r.services.ThreadRouter.threads.ModelProviderRoute(record.ID)
}

// checkThreadModelProvider rejects input to a thread whose retained provider
// route no longer matches managed policy (Rust #44944).
func (r *RuntimeRouter) checkThreadModelProvider(record *session.Record) error {
	if r == nil || record == nil {
		return nil
	}
	route, ok := r.threadModelProviderRoute(record)
	if !ok {
		return nil
	}
	return r.checkModelProviderRoute(route)
}

// checkModelProviderRoute validates one retained provider route against the
// current managed requirements.
func (r *RuntimeRouter) checkModelProviderRoute(route threadModelProviderRoute) error {
	requirements, err := r.managedModelProviderRequirements()
	if err != nil {
		return configLoadInvalidRequest(err)
	}
	if requirements == nil {
		return nil
	}
	if required := requirements.ModelProvider; required != nil && *required != route.providerID {
		return configLoadInvalidRequest(&ModelProviderError{
			Kind:                  ModelProviderPolicyConflict,
			ModelProvider:         route.providerID,
			RequiredModelProvider: *required,
		})
	}
	definition, hasDefinition := requirements.ModelProviders[route.providerID]
	if !hasDefinition {
		return nil
	}
	required, err := model.RequiredModelProviderDefinitionWithBaseURL(route.providerID, definition, route.provider.BaseURL)
	if err != nil {
		return configLoadInvalidRequest(err)
	}
	if required != nil && !reflect.DeepEqual(*required, route.provider) {
		// Rust reports a changed definition as a policy conflict against the
		// same provider, which renders the generic requirement-mismatch text
		// unless managed requirements name another provider.
		requiredProvider := route.providerID
		if requirements.ModelProvider != nil {
			requiredProvider = *requirements.ModelProvider
		}
		return configLoadInvalidRequest(&ModelProviderError{
			Kind:                  ModelProviderPolicyConflict,
			ModelProvider:         route.providerID,
			RequiredModelProvider: requiredProvider,
		})
	}
	return nil
}

// captureStartupModelProviderRoute records the provider the app server started
// with so later requirement changes can be detected even when model/list is
// answered from a warm catalog.
func (r *RuntimeRouter) captureStartupModelProviderRoute() {
	if r == nil {
		return
	}
	r.startupModelProviderRouteAttempted = true
	cfg, err := r.effectiveConfigForTurn(&turn.TurnStartParams{})
	if err != nil || cfg == nil {
		return
	}
	providerID := firstNonEmpty(
		stringConfigValue(cfg, "model_provider"),
		stringConfigValue(cfg, "modelProvider"),
		model.OpenAIProviderID,
	)
	provider, err := model.ProviderForConfigID(configValues(cfg), providerID, stringConfigValue(cfg, "openai_base_url"))
	if err != nil || provider == nil {
		return
	}
	r.startupModelProviderRoute = threadModelProviderRoute{providerID: providerID, provider: *provider}
	r.startupModelProviderRouteSet = true
}

// checkStartupModelProvider rejects catalog access when the retained startup
// provider no longer satisfies current requirements.
func (r *RuntimeRouter) checkStartupModelProvider() error {
	if r == nil || !r.startupModelProviderRouteSet {
		return nil
	}
	return r.checkModelProviderRoute(r.startupModelProviderRoute)
}

// checkThreadModelProviderForID resolves the thread record and applies the
// retained-provider check. Threads that cannot be loaded are left to the
// caller's own validation.
func (r *RuntimeRouter) checkThreadModelProviderForID(threadID string) error {
	if r == nil || strings.TrimSpace(threadID) == "" {
		return nil
	}
	record, err := r.threadRecord(session.ThreadID(strings.TrimSpace(threadID)), true, true)
	if err != nil || record == nil {
		return nil
	}
	return r.checkThreadModelProviderForInput(record)
}

// existingGoalStatus reports the goal status a set request would leave in
// place when the request itself does not change the status.
func (r *RuntimeRouter) existingGoalStatus(threadID string) (GoalStatus, bool) {
	threadID = strings.TrimSpace(threadID)
	if r == nil || threadID == "" {
		return "", false
	}
	if r.services.StateRuntime != nil && r.services.ThreadRouter != nil && r.services.ThreadRouter.store != nil {
		goal, err := r.services.StateRuntime.GetThreadGoal(context.Background(), threadID)
		if err != nil || goal == nil {
			return "", false
		}
		return apiGoalFromState(goal).Status, true
	}
	if r.services.ThreadRouter != nil && r.services.ThreadRouter.store != nil {
		record, err := r.threadRecord(session.ThreadID(threadID), true, true)
		if err != nil || record == nil {
			return "", false
		}
		goal, found, err := goalFromRecord(record)
		if err != nil || !found || goal == nil {
			return "", false
		}
		return goal.Status, true
	}
	return "", false
}

// checkThreadModelProviderForInput mirrors Rust's
// `ConfigManager::check_thread_model_provider_for_input` (#57840): managed
// policy still admits the thread's provider, that provider is still
// configured, and it is the provider the current configuration selects.
func (r *RuntimeRouter) checkThreadModelProviderForInput(record *session.Record) error {
	if err := r.checkThreadModelProvider(record); err != nil {
		return err
	}
	if r == nil || record == nil {
		return nil
	}
	route, ok := r.threadModelProviderRoute(record)
	if !ok {
		return nil
	}
	cfg, err := r.configForThreadRecord(record, nil)
	if err != nil || cfg == nil {
		return nil
	}
	// Rust compares against the latest configuration, not the thread-owned
	// settings, so a thread cannot keep itself admitted.
	current := r.latestConfiguredModelProvider(record)
	if current == "" {
		return nil
	}
	providers, provErr := model.ProvidersFromConfigForCWD(configValues(cfg), stringConfigValue(cfg, "openai_base_url"), "")
	if provErr != nil {
		return configLoadInvalidRequest(provErr)
	}
	if _, ok := providers[route.providerID]; !ok {
		return configLoadInvalidRequest(&ModelProviderError{Kind: ModelProviderNotConfigured, ModelProvider: route.providerID})
	}
	if route.providerID != current {
		return configLoadInvalidRequest(&ModelProviderError{
			Kind:                 ModelProviderMismatch,
			ModelProvider:        route.providerID,
			CurrentModelProvider: current,
		})
	}
	return nil
}

// validateResumeModelProvider mirrors Rust's `ConfigManager::load_for_resume`
// (#57840): the provider saved on the thread and the provider the resume asks
// for must both satisfy managed policy, stay configured, and match the provider
// the current configuration selects. Validation runs before any resume override
// rewrites the thread's settings.
func (r *RuntimeRouter) validateResumeModelProvider(record *session.Record, params *ThreadResumeParams) error {
	if r == nil || record == nil {
		return nil
	}
	var requestConfig map[string]any
	requestedOverride := ""
	if params != nil {
		requestConfig = params.Config
		if params.ModelProvider != nil {
			requestedOverride = strings.TrimSpace(*params.ModelProvider)
		}
	}
	if requestedOverride == "" {
		if value, ok := threadConfigStringValue(requestConfig, "model_provider"); ok {
			requestedOverride = value
		}
	}
	cfg, err := r.configForThreadRecord(record, requestConfig)
	if err != nil {
		return configLoadInvalidRequest(err)
	}
	if cfg == nil {
		return nil
	}
	// Rust's `load_for_resume` resolves the selection before the saved provider
	// is applied as a resume override.
	current := r.latestConfiguredModelProvider(record)
	if current == "" {
		return configLoadInvalidRequest(errors.New("model provider requirements are unavailable"))
	}
	requested := firstNonEmpty(requestedOverride, current)
	saved := firstNonEmpty(strings.TrimSpace(record.Metadata.ModelProvider), requested)
	requirements, reqErr := r.managedModelProviderRequirements()
	if reqErr != nil {
		return configLoadInvalidRequest(reqErr)
	}
	providers, provErr := model.ProvidersFromConfigForCWD(configValues(cfg), stringConfigValue(cfg, "openai_base_url"), "")
	if provErr != nil {
		return configLoadInvalidRequest(provErr)
	}
	for _, provider := range []string{saved, requested} {
		if requirements != nil && requirements.ModelProvider != nil && *requirements.ModelProvider != provider {
			return configLoadInvalidRequest(&ModelProviderError{
				Kind:                  ModelProviderPolicyConflict,
				ModelProvider:         provider,
				RequiredModelProvider: *requirements.ModelProvider,
			})
		}
		if _, ok := providers[provider]; !ok {
			return configLoadInvalidRequest(&ModelProviderError{Kind: ModelProviderNotConfigured, ModelProvider: provider})
		}
		if provider != current {
			return configLoadInvalidRequest(&ModelProviderError{
				Kind:                 ModelProviderMismatch,
				ModelProvider:        provider,
				CurrentModelProvider: current,
			})
		}
	}
	return nil
}

// configForThreadRecord resolves a thread's effective configuration, optionally
// applying a resume request's raw overrides on top of the thread-owned ones.
func (r *RuntimeRouter) configForThreadRecord(record *session.Record, requestConfig map[string]any) (*config.Config, error) {
	if r == nil || record == nil {
		return nil, nil
	}
	merged := threadRecordConfigOverrides(record)
	if merged == nil {
		merged = map[string]any{}
	}
	for key, value := range requestConfig {
		merged[key] = value
	}
	params := &turn.TurnStartParams{ThreadID: string(record.ID), Config: merged}
	if cwd := strings.TrimSpace(record.Metadata.CWD); cwd != "" {
		params.CWD = cwd
	}
	return r.effectiveConfigForTurn(params)
}

// configuredModelProviderID reports the provider the configuration selects.
func configuredModelProviderID(cfg *config.Config) string {
	return firstNonEmpty(
		strings.TrimSpace(stringConfigValue(cfg, "model_provider")),
		strings.TrimSpace(stringConfigValue(cfg, "modelProvider")),
		model.OpenAIProviderID,
	)
}

// latestConfiguredModelProvider resolves the provider the current user/project
// configuration selects, ignoring thread- and request-owned overrides.
func (r *RuntimeRouter) latestConfiguredModelProvider(record *session.Record) string {
	if r == nil {
		return ""
	}
	params := &turn.TurnStartParams{}
	if record != nil {
		params.CWD = strings.TrimSpace(record.Metadata.CWD)
	}
	cfg, err := r.effectiveConfigForTurn(params)
	if err != nil || cfg == nil {
		return ""
	}
	return configuredModelProviderID(cfg)
}
