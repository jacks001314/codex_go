package appserver

// Rust parity: codex-rs/core/src/guardian/inference.rs (#62588).
//
// A host can select a Guardian inference provider independently of the actor's
// provider. Guardian then resolves its preferred reviewer, credentials and
// endpoint from that provider, so reviews never borrow the actor's route. Go
// keeps the selection on RuntimeServices and builds the reviewer's own model
// runner from it; the reviewer pool uses that runner for every review session
// while the provider stays selected.

import (
	"fmt"
	"strings"

	"codex_go/auth"
	"codex_go/config"
	"codex_go/features"
	"codex_go/model"
	"codex_go/turn"
)

// GuardianInferenceProvider is the inference route a host selects for Guardian
// (Rust Config::guardian_model_provider / GuardianInferenceProvider).
type GuardianInferenceProvider struct {
	// ID is the provider id Guardian requests and telemetry report
	// (Rust GuardianInferenceProvider::id).
	ID string
	// Info is the resolved provider: endpoint, auth, retry and catalog settings.
	Info model.ProviderInfo
}

func (p GuardianInferenceProvider) normalized() (GuardianInferenceProvider, bool) {
	id := strings.TrimSpace(p.ID)
	if id == "" {
		return GuardianInferenceProvider{}, false
	}
	p.ID = id
	return p, true
}

// preferredReviewModel mirrors Rust
// ModelProviderInfo::approval_review_preferred_model: a provider that
// authenticates with an API key prefers Luna, while an OpenAI-auth provider
// prefers the auto-review model.
func (p GuardianInferenceProvider) preferredReviewModel() string {
	if strings.TrimSpace(p.Info.EnvKey) != "" || !p.Info.RequiresOpenAIAuth {
		return model.APIKeyApprovalReviewPreferredModel
	}
	return model.DefaultApprovalReviewPreferredModel
}

// guardianInferenceProvider reports the host-selected Guardian provider.
func (r *RuntimeRouter) guardianInferenceProvider() (GuardianInferenceProvider, bool) {
	if r == nil || r.services.GuardianModelProvider == nil {
		return GuardianInferenceProvider{}, false
	}
	return r.services.GuardianModelProvider.normalized()
}

// guardianInferenceAgent builds the reviewer's own model runner on the
// host-selected provider (Rust GuardianInferenceContext). A router without a
// Guardian provider reports no runner so the reviewer keeps the actor's route.
func (r *RuntimeRouter) guardianInferenceAgent(cfg *config.Config) (model.AgentRunner, error) {
	provider, ok := r.guardianInferenceProvider()
	if !ok {
		return nil, nil
	}
	codexHome := strings.TrimSpace(r.codexHomeForRollout())
	storeOptions := r.authStoreOptionsForOriginator("")
	var snapshot *auth.AuthDotJSON
	if codexHome != "" {
		resolved, err := r.resolveAuthWithLoginRestrictionsAndOptions(codexHome, storeOptions)
		if err != nil {
			return nil, err
		}
		if resolved != nil {
			snapshot = &resolved.Auth
		}
	}
	runtimeProvider := model.CreateRuntimeProviderWithResidency(
		provider.ID,
		provider.Info,
		snapshot,
		managedResidencyForConfig(cfg),
	)
	agent, err := model.NewResponsesAgentRunnerFromRuntimeProviderWithAuth(
		provider.ID,
		runtimeProvider,
		r.httpClientForConfig(cfg),
		codexHome,
		snapshot,
	)
	if err != nil {
		return nil, err
	}
	agent.StoreOptions = storeOptions
	if r.threads != nil {
		agent.ImageStore = r.threads.AttachmentStore()
	}
	agent.Metrics = r.services.TurnMetrics
	// Rust #62607: Guardian requests carry the selected provider's own API-key
	// access policy, independent of the actor's provider.
	agent.ApiKeyCyberAccessPrograms = model.ApiKeyCyberAccessProgramsFromConfig(
		cfg.FeatureSettings(),
		provider.ID,
		provider.Info,
	)
	return agent, nil
}

// guardianReviewProviderID reports the provider Guardian requests use: the
// host-selected provider when one is configured, the actor's otherwise.
func (r *RuntimeRouter) guardianReviewProviderID(actorProviderID string) string {
	if provider, ok := r.guardianInferenceProvider(); ok {
		return provider.ID
	}
	return strings.TrimSpace(actorProviderID)
}

// guardianModelsManager resolves the catalog belonging to the host-selected
// Guardian provider (Rust #62588 ThreadManager sharing one Guardian catalog
// between scoring and blocking review). It never blocks on a remote fetch: the
// caller schedules initialization in the background (#62627).
func (r *RuntimeRouter) guardianModelsManager(cfg *config.Config) model.ModelsManager {
	provider, ok := r.guardianInferenceProvider()
	if !ok {
		return nil
	}
	discovery := true
	if cfg != nil {
		discovery = features.Enabled(cfg.FeatureSettings(), "api_key_model_discovery")
	}
	key := fmt.Sprintf("%s|%s|%t", provider.ID, strings.TrimSpace(provider.Info.ModelCatalogURL), discovery)
	r.guardianModelsMu.Lock()
	cached := r.guardianModels[key]
	r.guardianModelsMu.Unlock()
	if cached != nil {
		return cached
	}
	codexHome := strings.TrimSpace(r.codexHomeForRollout())
	var snapshot *auth.AuthDotJSON
	if codexHome != "" {
		if resolved, err := r.resolveAuthWithLoginRestrictionsAndOptions(codexHome, r.authStoreOptionsForOriginator("")); err == nil && resolved != nil {
			snapshot = &resolved.Auth
		}
	}
	runtimeProvider := model.CreateRuntimeProviderWithResidency(
		provider.ID,
		provider.Info,
		snapshot,
		managedResidencyForConfig(cfg),
	)
	manager := runtimeProvider.ModelsManager(nil)
	model.SetAPIKeyModelDiscoveryEnabled(manager, discovery)
	r.guardianModelsMu.Lock()
	if r.guardianModels == nil {
		r.guardianModels = map[string]model.ModelsManager{}
	}
	if existing := r.guardianModels[key]; existing != nil {
		manager = existing
	} else {
		r.guardianModels[key] = manager
	}
	r.guardianModelsMu.Unlock()
	return manager
}

// retryGuardianCatalogInitializationInBackground mirrors Rust #62627
// LunaSampler::retry_catalog_initialization_in_background: at most one retry
// runs at a time and it never delays the observed tool call.
func (r *RuntimeRouter) retryGuardianCatalogInitializationInBackground(manager model.ModelsManager) bool {
	if r == nil || manager == nil || !manager.NeedsInitialization() {
		return false
	}
	r.guardianCatalogRetryMu.Lock()
	if r.guardianCatalogRetryInFlight {
		r.guardianCatalogRetryMu.Unlock()
		return false
	}
	r.guardianCatalogRetryInFlight = true
	r.guardianCatalogRetryMu.Unlock()
	go func() {
		defer func() {
			r.guardianCatalogRetryMu.Lock()
			r.guardianCatalogRetryInFlight = false
			r.guardianCatalogRetryMu.Unlock()
		}()
		manager.RawModelCatalog(model.RefreshOnline)
	}()
	return true
}

// guardianCatalogRecoveryDue reports whether an observation should schedule the
// background catalog retry (Rust #62627): the host selected a Guardian
// provider, its catalog needs initialization, automatic work may run (auto
// review outside full access), and requirements do not disable all Guardian
// scoring.
func (r *RuntimeRouter) guardianCatalogRecoveryDue(cfg *config.Config, params *turn.TurnStartParams, manager model.ModelsManager) bool {
	if r == nil || manager == nil || !manager.NeedsInitialization() {
		return false
	}
	if _, ok := r.guardianInferenceProvider(); !ok {
		return false
	}
	if guardianScoringDisabledByRequirements(cfg) {
		return false
	}
	if reviewer := turnApprovalsReviewerForTurn(cfg, params); !strings.EqualFold(reviewer, string(config.ApprovalsReviewerAutoReview)) {
		return false
	}
	return !turnIsFullAccess(cfg, firstNonEmpty(turnCWD(params), r.services.DefaultCWD), params)
}

// managedGuardianPolicyRequirement reports the managed `guardian_policy_config`
// requirement alone (Rust Config::managed_guardian_policy). A host-selected
// Guardian provider ignores policy text that came from the actor's
// configuration and reads the managed policy (or its provider catalog) instead.
func managedGuardianPolicyRequirement(cfg *config.Config) (string, bool) {
	if cfg == nil || cfg.Requirements == nil || cfg.Requirements.GuardianPolicyConfig == nil {
		return "", false
	}
	value := strings.TrimSpace(*cfg.Requirements.GuardianPolicyConfig)
	if value == "" {
		return "", false
	}
	return value, true
}
