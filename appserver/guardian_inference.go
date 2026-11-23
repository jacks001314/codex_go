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
	"strings"

	"codex_go/auth"
	"codex_go/config"
	"codex_go/model"
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
