package model

import (
	"context"
	"errors"
	"strings"

	"codex_go/auth"
)

// Rust parity: core's client unauthorized handling (core/src/client.rs
// `handle_unauthorized`) plus codex-login's recovery plan
// (login/src/auth/manager.rs::UnauthorizedRecovery): the provider owns one
// recovery attempt per request, and the plan then advances through its steps
// (a managed recovery reloads the stored auth before refreshing the token).

// Provider recovery names mirror the values Rust reports for a provider-owned
// recovery: the retrying attempt reports them as its pending retry, and the
// provider emits its own protocol events instead of the recovery record.
const (
	providerRecoveryMode  = "provider"
	providerRecoveryPhase = "provider_refresh"
)

// authRecoveryState tracks one request's unauthorized recovery: Rust's
// provider-owned latch plus the plan position.
type authRecoveryState struct {
	providerAttempted bool
	// planIndex is Rust's UnauthorizedRecovery step cursor: each 401 attempts
	// the current step and advances the cursor when that step completes, so a
	// no-change reload is reported on the first 401 and the token refresh on
	// the next.
	planIndex int
}

// authRecoveryStep is one step of the recovery plan: the mode/step names it
// reports and the action, which also reports whether it changed the cached auth.
type authRecoveryStep struct {
	mode    auth.UnauthorizedRecoveryMode
	step    auth.UnauthorizedRecoveryStep
	attempt func(context.Context) (bool, error)
	// doneOnPermanent marks Rust's Reload step, whose Skipped outcome (the
	// account mismatch) moves the plan to Done; a failed refresh leaves the
	// cursor on the refresh step, matching Rust's `?`.
	doneOnPermanent bool
}

// recoverUnauthorizedAuth runs Rust's unauthorized handling for one 401: the
// provider-owned recovery at most once per request, then the plan's steps. It
// reports every step it attempts and returns the mode/step the retrying attempt
// reports (Rust's UnauthorizedRecoveryExecution).
func (r *ResponsesAgentRunner) recoverUnauthorizedAuth(ctx context.Context, state *authRecoveryState, debug authRecoveryDebug) (auth.UnauthorizedRecoveryMode, auth.UnauthorizedRecoveryStep, error) {
	if state == nil {
		state = &authRecoveryState{}
	}
	// The provider owns one recovery attempt per request (Rust's
	// `provider_auth_recovery_attempted`): a Bedrock credential refresh or a
	// workload-identity refresh recovers the provider's own credentials, and Rust
	// reports it as the pending retry without a recovery record.
	if !state.providerAttempted {
		state.providerAttempted = true
		if r.recoverProviderAuth(ctx) {
			return auth.UnauthorizedRecoveryMode(providerRecoveryMode), auth.UnauthorizedRecoveryStep(providerRecoveryPhase), nil
		}
	}

	steps := r.authRecoverySteps()
	if state.planIndex < len(steps) {
		step := steps[state.planIndex]
		changed, err := step.attempt(ctx)
		if err == nil {
			// Rust advances the cursor and retries with the reloaded auth; the
			// remaining steps run on the next 401.
			state.planIndex++
			r.recordAuthRecovery(ctx, step.mode, step.step, auth.AuthRecoveryOutcomeSucceeded, debug, &changed)
			return step.mode, step.step, nil
		}
		outcome := auth.AuthRecoveryOutcomeFailedTransient
		if auth.IsPermanentRefreshFailure(err) {
			outcome = auth.AuthRecoveryOutcomeFailedPermanent
			if step.doneOnPermanent {
				state.planIndex = len(steps)
			}
		}
		r.recordAuthRecovery(ctx, step.mode, step.step, outcome, debug, nil)
		return "", "", err
	}
	// The plan is exhausted (or was never available): Rust reports
	// recovery_not_run at the cursor's step and reason, then surfaces the
	// original 401.
	mode, stepName, reason := r.authRecoveryNotRunPosition(steps)
	r.recordAuthRecoveryWithReason(ctx, mode, stepName, auth.AuthRecoveryOutcomeNotRun, reason, debug, nil)
	return "", "", errNoAuthRecoveryStep
}

// errNoAuthRecoveryStep reports a 401 that ran no recovery step (Rust's
// recovery_not_run): the caller surfaces the original error instead of retrying.
var errNoAuthRecoveryStep = errors.New("no auth recovery step is available")

// authRecoveryNotRunPosition mirrors Rust UnauthorizedRecovery::mode_name /
// step_name / unavailable_reason for a 401 that ran no step.
func (r *ResponsesAgentRunner) authRecoveryNotRunPosition(steps []authRecoveryStep) (auth.UnauthorizedRecoveryMode, auth.UnauthorizedRecoveryStep, string) {
	if len(steps) > 0 {
		return steps[0].mode, auth.UnauthorizedRecoveryStepDone, "recovery_exhausted"
	}
	return auth.UnauthorizedRecoveryMode("none"), auth.UnauthorizedRecoveryStep("none"), r.authRecoveryUnavailableReason()
}

// authRecoveryUnavailableReason mirrors the parts of Rust's
// UnauthorizedRecovery::unavailable_reason Go can distinguish when no plan is
// available: an absent auth manager, a personal-access-token session, and a
// session that is not ChatGPT auth.
func (r *ResponsesAgentRunner) authRecoveryUnavailableReason() string {
	if r == nil || r.AuthSnapshot == nil {
		return "auth_manager_missing"
	}
	if r.AuthSnapshot.Mode() == "chatgptAuthTokens" {
		return "not_refreshable_auth"
	}
	if !authHasChatGPTAccount(r.AuthSnapshot) {
		return "not_chatgpt_auth"
	}
	// The remaining case is a managed session with no Codex home to reload the
	// stored credentials from.
	return "auth_manager_missing"
}

// recoverProviderAuth runs the provider-owned credential recovery (at most once
// per request, guarded by the caller) and reports whether it recovered.
func (r *ResponsesAgentRunner) recoverProviderAuth(ctx context.Context) bool {
	if err := r.refreshBedrockAWSCredentials(ctx); err == nil && r.bedrockAuthRecoveryAttempted {
		return true
	}
	if err := r.refreshWorkloadIdentityAuth(ctx); err == nil {
		return true
	}
	return false
}

// authRecoverySteps reports the recovery plan Go shares with Rust's, chosen by
// the credential owner the runner holds: an external source refreshes through the
// provider callback, a managed ChatGPT session reloads the stored auth and then
// refreshes the token.
func (r *ResponsesAgentRunner) authRecoverySteps() []authRecoveryStep {
	if r == nil {
		return nil
	}
	if step, ok := r.externalAuthRecoveryStep(); ok {
		return []authRecoveryStep{step}
	}
	if r.managedAuthRecoveryAvailable() {
		return []authRecoveryStep{
			{
				mode: auth.UnauthorizedRecoveryModeManaged,
				step: auth.UnauthorizedRecoveryStepReload,
				attempt: func(ctx context.Context) (bool, error) {
					return r.reloadStoredChatGPTAuth(ctx)
				},
				doneOnPermanent: true,
			},
			{
				mode: auth.UnauthorizedRecoveryModeManaged,
				step: auth.UnauthorizedRecoveryStepRefreshToken,
				attempt: func(ctx context.Context) (bool, error) {
					return true, r.refreshManagedChatGPTAuth(ctx)
				},
			},
		}
	}
	return nil
}

// externalAuthRecoveryStep reports the external auth source's refresh step, if
// the runner has one: the external ChatGPT callback or the provider's command
// auth (Rust's external bearer auth source).
func (r *ResponsesAgentRunner) externalAuthRecoveryStep() (authRecoveryStep, bool) {
	if r == nil {
		return authRecoveryStep{}, false
	}
	if r.AuthSnapshot != nil && r.AuthSnapshot.Mode() == "chatgptAuthTokens" && r.ExternalAuthRefresh != nil {
		return authRecoveryStep{
			mode: auth.UnauthorizedRecoveryModeExternal,
			step: auth.UnauthorizedRecoveryStepExternalRefresh,
			attempt: func(ctx context.Context) (bool, error) {
				return true, r.refreshExternalChatGPTAuth(ctx)
			},
		}, true
	}
	if r.Provider != nil && r.Provider.Auth != nil {
		return authRecoveryStep{
			mode: auth.UnauthorizedRecoveryModeExternal,
			step: auth.UnauthorizedRecoveryStepExternalRefresh,
			attempt: func(ctx context.Context) (bool, error) {
				return true, r.refreshProviderCommandAuth(ctx)
			},
		}, true
	}
	return authRecoveryStep{}, false
}

// managedAuthRecoveryAvailable reports whether the runner holds a managed
// ChatGPT session it can refresh.
func (r *ResponsesAgentRunner) managedAuthRecoveryAvailable() bool {
	return r != nil && r.AuthSnapshot != nil && authHasChatGPTAccount(r.AuthSnapshot) &&
		r.AuthSnapshot.Mode() != "chatgptAuthTokens" && strings.TrimSpace(r.CodexHome) != ""
}

// reloadStoredChatGPTAuth mirrors codex-login's Reload step: re-read the stored
// auth and adopt it when it still belongs to the account this process runs as. A
// missing or mismatched account is the permanent account-mismatch failure Rust
// reports, which stops the plan.
func (r *ResponsesAgentRunner) reloadStoredChatGPTAuth(ctx context.Context) (bool, error) {
	if r == nil || r.AuthSnapshot == nil {
		return false, permanentRefreshFailure("no auth is available to reload")
	}
	expectedAccountID := accountIDFromMap(r.AuthSnapshot.Tokens)
	if strings.TrimSpace(expectedAccountID) == "" {
		return false, permanentRefreshFailure(auth.RefreshTokenAccountMismatchMessage)
	}
	stored, err := auth.NewStoreWithOptions(r.CodexHome, r.StoreOptions).Load()
	if err != nil || stored == nil {
		return false, permanentRefreshFailure(auth.RefreshTokenAccountMismatchMessage)
	}
	if strings.TrimSpace(accountIDFromMap(stored.Tokens)) != strings.TrimSpace(expectedAccountID) {
		return false, permanentRefreshFailure(auth.RefreshTokenAccountMismatchMessage)
	}
	previous := authCredentialFingerprint(r.AuthSnapshot)
	if err := r.applyRefreshedAuth(stored); err != nil {
		return false, err
	}
	return authCredentialFingerprint(r.AuthSnapshot) != previous, nil
}

// permanentRefreshFailure builds the permanent refresh failure Rust returns for
// an unrecoverable auth state.
func permanentRefreshFailure(message string) error {
	return &auth.RefreshTokenFailedError{Reason: auth.RefreshTokenFailedOther, Message: message}
}
