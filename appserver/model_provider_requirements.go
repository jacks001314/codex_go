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

// configLoadInvalidRequest mirrors Rust's config_load_error: the app-server
// surfaces configuration failures as invalid requests with a stable prefix.
func configLoadInvalidRequest(err error) error {
	if err == nil {
		return nil
	}
	return jsonRPCInvalidRequest("failed to load configuration: " + err.Error())
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
	requirements, err := r.managedModelProviderRequirements()
	if err != nil {
		return configLoadInvalidRequest(err)
	}
	if requirements == nil {
		return nil
	}
	route, ok := r.threadModelProviderRoute(record)
	if !ok {
		return nil
	}
	if requirements.ModelProvider != nil && *requirements.ModelProvider != route.providerID {
		return configLoadInvalidRequest(errModelProviderRequirementsChanged)
	}
	definition, hasDefinition := requirements.ModelProviders[route.providerID]
	if !hasDefinition {
		return nil
	}
	required, err := model.RequiredModelProviderDefinition(route.providerID, definition)
	if err != nil {
		return configLoadInvalidRequest(err)
	}
	if required != nil && !reflect.DeepEqual(*required, route.provider) {
		return configLoadInvalidRequest(errModelProviderRequirementsChanged)
	}
	return nil
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
	return r.checkThreadModelProvider(record)
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
