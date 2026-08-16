package exec

// This file re-exports the shell environment policy surface from
// codex_go/execpolicy (the leaf package mirroring Rust's codex-execpolicy
// crate) so existing codexexec.EnvPolicy / codexexec.CreateEnv callers keep
// working. New code should import codex_go/execpolicy directly when it must
// not pull in the exec package's higher-level dependencies.

import "codex_go/execpolicy"

type EnvPatternMode = execpolicy.EnvPatternMode

const (
	EnvPatternLiteral  = execpolicy.EnvPatternLiteral
	EnvPatternPrefix   = execpolicy.EnvPatternPrefix
	EnvPatternSuffix   = execpolicy.EnvPatternSuffix
	EnvPatternContains = execpolicy.EnvPatternContains
	EnvPatternWildcard = execpolicy.EnvPatternWildcard
)

const (
	ThreadIDEnvVar          = execpolicy.ThreadIDEnvVar
	SessionIDEnvVar         = execpolicy.SessionIDEnvVar
	PermissionProfileEnvVar = execpolicy.PermissionProfileEnvVar
)

type EnvVariablePattern = execpolicy.EnvVariablePattern

type EnvPolicy = execpolicy.EnvPolicy

// EnvPolicyFromShellEnvironmentPolicy converts a config shell_environment_policy
// table into an EnvPolicy (see execpolicy.EnvPolicyFromShellEnvironmentPolicy).
func EnvPolicyFromShellEnvironmentPolicy(table map[string]any, cwd string) *EnvPolicy {
	return execpolicy.EnvPolicyFromShellEnvironmentPolicy(table, cwd)
}

// CreateEnv builds a command environment from a policy (see
// execpolicy.CreateEnv).
func CreateEnv(policy *EnvPolicy, threadID *string, vars map[string]string) map[string]string {
	return execpolicy.CreateEnv(policy, threadID, vars)
}

// InjectPermissionProfile sets the permission profile env var (see
// execpolicy.InjectPermissionProfile).
func InjectPermissionProfile(env map[string]string, profileID *string) {
	execpolicy.InjectPermissionProfile(env, profileID)
}

// InjectSessionID sets the session id env var (see execpolicy.InjectSessionID).
func InjectSessionID(env map[string]string, sessionID *string) {
	execpolicy.InjectSessionID(env, sessionID)
}
