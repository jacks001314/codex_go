package auth

// Rust parity: codex-login's UnauthorizedRecoveryMode / UnauthorizedRecoveryStep
// (login/src/manager.rs) and the outcomes core's client passes to
// SessionTelemetry::record_auth_recovery when a request recovers from a 401.

// RefreshTokenAccountMismatchMessage mirrors codex-login's
// REFRESH_TOKEN_ACCOUNT_MISMATCH_MESSAGE: the permanent failure a reload reports
// when the stored credentials belong to another account.
const RefreshTokenAccountMismatchMessage = "Your access token could not be refreshed because you have since logged out or signed in to another account. Please sign in again."

// UnauthorizedRecoveryMode names the credential owner a recovery runs for.
type UnauthorizedRecoveryMode string

const (
	UnauthorizedRecoveryModeManaged  UnauthorizedRecoveryMode = "managed"
	UnauthorizedRecoveryModeExternal UnauthorizedRecoveryMode = "external"
)

// UnauthorizedRecoveryStep names one step of a recovery plan.
type UnauthorizedRecoveryStep string

const (
	UnauthorizedRecoveryStepReload          UnauthorizedRecoveryStep = "reload"
	UnauthorizedRecoveryStepRefreshToken    UnauthorizedRecoveryStep = "refresh_token"
	UnauthorizedRecoveryStepExternalRefresh UnauthorizedRecoveryStep = "external_refresh"
	UnauthorizedRecoveryStepDone            UnauthorizedRecoveryStep = "done"
)

// Auth-recovery outcomes mirror the values core's client reports.
const (
	AuthRecoveryOutcomeSucceeded       = "recovery_succeeded"
	AuthRecoveryOutcomeFailedPermanent = "recovery_failed_permanent"
	AuthRecoveryOutcomeFailedTransient = "recovery_failed_transient"
	AuthRecoveryOutcomeNotRun          = "recovery_not_run"
)
