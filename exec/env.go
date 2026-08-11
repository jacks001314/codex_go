package exec

import (
	"path/filepath"
	"runtime"
	"strings"
)

const (
	ThreadIDEnvVar          = "CODEX_THREAD_ID"
	SessionIDEnvVar         = "CODEX_SESSION_ID"
	PermissionProfileEnvVar = "CODEX_PERMISSION_PROFILE"
)

type EnvPatternMode string

const (
	EnvPatternLiteral  EnvPatternMode = "literal"
	EnvPatternPrefix   EnvPatternMode = "prefix"
	EnvPatternSuffix   EnvPatternMode = "suffix"
	EnvPatternContains EnvPatternMode = "contains"
	EnvPatternWildcard EnvPatternMode = "wildcard"
)

type EnvVariablePattern struct {
	Mode  EnvPatternMode
	Value string
}

type EnvPolicy struct {
	Inherit               string
	InheritAll            bool
	IgnoreDefaultExcludes *bool
	IncludeOnly           []EnvVariablePattern
	Exclude               []EnvVariablePattern
	Set                   map[string]string
	Remove                []string
	CWD                   string
}

func CreateEnv(policy *EnvPolicy, threadID *string, vars map[string]string) map[string]string {
	if policy == nil {
		policy = &EnvPolicy{Inherit: "all"}
	}
	env := map[string]string{}
	switch strings.ToLower(strings.TrimSpace(policy.Inherit)) {
	case "none":
	case "core":
		for key, value := range vars {
			if isCoreEnv(key) {
				env[key] = value
			}
		}
	default:
		for key, value := range vars {
			env[key] = value
		}
	}
	ignoreDefaultExcludes := true
	if policy.IgnoreDefaultExcludes != nil {
		ignoreDefaultExcludes = *policy.IgnoreDefaultExcludes
	}
	if !ignoreDefaultExcludes {
		for key := range env {
			if wildcardMatchAny([]string{"*KEY*", "*SECRET*", "*TOKEN*"}, key, true) {
				delete(env, key)
			}
		}
	}
	for key := range env {
		if matchesAny(policy.Exclude, key) {
			delete(env, key)
		}
	}
	for _, key := range policy.Remove {
		removeKey(env, key)
	}
	for key, value := range policy.Set {
		env[key] = expandEnvValue(value, policy, env)
	}
	if len(policy.IncludeOnly) > 0 {
		for key := range env {
			if !matchesAny(policy.IncludeOnly, key) {
				delete(env, key)
			}
		}
	}
	if threadID != nil && strings.TrimSpace(*threadID) != "" {
		env[ThreadIDEnvVar] = strings.TrimSpace(*threadID)
	}
	if runtime.GOOS == "windows" && !hasEnvKey(env, "PATHEXT") {
		env["PATHEXT"] = ".COM;.EXE;.BAT;.CMD"
	}
	return env
}

func InjectPermissionProfile(env map[string]string, profileID *string) {
	if env == nil {
		return
	}
	removeKey(env, PermissionProfileEnvVar)
	if profileID != nil && strings.TrimSpace(*profileID) != "" {
		env[PermissionProfileEnvVar] = strings.TrimSpace(*profileID)
	}
}

// InjectSessionID exposes the shared root-session identity to model-reachable
// shell commands. The session ID is applied after the shell environment policy
// so the runtime-selected value is authoritative.
func InjectSessionID(env map[string]string, sessionID *string) {
	if env == nil {
		return
	}
	removeKey(env, SessionIDEnvVar)
	if sessionID != nil && strings.TrimSpace(*sessionID) != "" {
		env[SessionIDEnvVar] = strings.TrimSpace(*sessionID)
	}
}

func (p *EnvVariablePattern) Matches(key string) bool {
	if p == nil {
		return false
	}
	switch p.Mode {
	case EnvPatternPrefix:
		return strings.HasPrefix(key, p.Value)
	case EnvPatternSuffix:
		return strings.HasSuffix(key, p.Value)
	case EnvPatternContains:
		return strings.Contains(key, p.Value)
	case EnvPatternWildcard:
		return wildcardMatch(p.Value, key, false)
	default:
		return key == p.Value
	}
}

func matchesAny(patterns []EnvVariablePattern, key string) bool {
	for i := range patterns {
		if patterns[i].Matches(key) {
			return true
		}
	}
	return false
}

func removeKey(env map[string]string, key string) {
	if runtime.GOOS == "windows" {
		for existing := range env {
			if strings.EqualFold(existing, key) {
				delete(env, existing)
			}
		}
		return
	}
	delete(env, key)
}

func hasEnvKey(env map[string]string, key string) bool {
	for existing := range env {
		if strings.EqualFold(existing, key) {
			return true
		}
	}
	return false
}

func isCoreEnv(key string) bool {
	var core []string
	if runtime.GOOS == "windows" {
		core = []string{
			"PATH", "PATHEXT", "SHELL", "COMSPEC", "SYSTEMROOT", "SYSTEMDRIVE",
			"USERNAME", "USERDOMAIN", "USERPROFILE", "HOMEDRIVE", "HOMEPATH",
			"PROGRAMFILES", "PROGRAMFILES(X86)", "PROGRAMW6432", "PROGRAMDATA",
			"LOCALAPPDATA", "APPDATA", "TEMP", "TMP", "TMPDIR", "POWERSHELL", "PWSH",
		}
	} else {
		core = []string{"PATH", "SHELL", "TMPDIR", "TEMP", "TMP", "HOME", "LANG", "LC_ALL", "LC_CTYPE", "LOGNAME", "USER"}
	}
	for _, allowed := range core {
		if strings.EqualFold(allowed, key) {
			return true
		}
	}
	return false
}

func wildcardMatchAny(patterns []string, key string, caseInsensitive bool) bool {
	for _, pattern := range patterns {
		if wildcardMatch(pattern, key, caseInsensitive) {
			return true
		}
	}
	return false
}

func wildcardMatch(pattern string, value string, caseInsensitive bool) bool {
	if caseInsensitive {
		pattern = strings.ToLower(pattern)
		value = strings.ToLower(value)
	}
	ok, err := filepath.Match(pattern, value)
	if err == nil {
		return ok
	}
	return pattern == value
}

func expandEnvValue(value string, policy *EnvPolicy, env map[string]string) string {
	value = strings.ReplaceAll(value, "$CWD", policy.CWD)
	value = strings.ReplaceAll(value, "${CWD}", policy.CWD)
	if strings.Contains(value, "$PATH") {
		value = strings.ReplaceAll(value, "$PATH", env["PATH"])
	}
	if strings.Contains(value, "${PATH}") {
		value = strings.ReplaceAll(value, "${PATH}", env["PATH"])
	}
	if strings.Contains(value, "$PATH_SEP") {
		value = strings.ReplaceAll(value, "$PATH_SEP", string(filepath.ListSeparator))
	}
	return value
}
