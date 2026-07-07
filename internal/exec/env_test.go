package exec

import (
	"runtime"
	"testing"
)

func TestCreateEnvInheritExcludeSetAndThreadID(t *testing.T) {
	threadID := "thread-1"
	env := CreateEnv(&EnvPolicy{
		InheritAll: true,
		Exclude:    []EnvVariablePattern{{Mode: EnvPatternPrefix, Value: "SECRET_"}},
		Set:        map[string]string{"CODEX_HOME": "$CWD/.codex", "PATH": "/custom$PATH_SEP$PATH"},
		CWD:        "/repo",
	}, &threadID, map[string]string{
		"PATH":       "/usr/bin",
		"SECRET_KEY": "redacted",
	})
	if env["SECRET_KEY"] != "" {
		t.Fatalf("SECRET_KEY inherited, env=%+v", env)
	}
	if env["CODEX_HOME"] != "/repo/.codex" {
		t.Fatalf("CODEX_HOME = %q", env["CODEX_HOME"])
	}
	if env[ThreadIDEnvVar] != "thread-1" {
		t.Fatalf("thread id env = %q", env[ThreadIDEnvVar])
	}
}

func TestCreateEnvIncludeOnly(t *testing.T) {
	env := CreateEnv(&EnvPolicy{
		IncludeOnly: []EnvVariablePattern{{Mode: EnvPatternLiteral, Value: "PATH"}},
	}, nil, map[string]string{"PATH": "/bin", "HOME": "/home/me"})
	wantLen := 1
	if runtime.GOOS == "windows" {
		wantLen = 2
		if env["PATHEXT"] != ".COM;.EXE;.BAT;.CMD" {
			t.Fatalf("PATHEXT = %q", env["PATHEXT"])
		}
	}
	if len(env) != wantLen || env["PATH"] != "/bin" {
		t.Fatalf("env = %+v, want only PATH", env)
	}
}

func TestCreateEnvCoreAndDefaultExcludes(t *testing.T) {
	ignoreDefaultExcludes := false
	env := CreateEnv(&EnvPolicy{
		Inherit:               "core",
		IgnoreDefaultExcludes: &ignoreDefaultExcludes,
		Set:                   map[string]string{"CODEX_PUBLIC": "ok"},
	}, nil, map[string]string{
		"PATH":       "/bin",
		"HOME":       "/home/me",
		"API_KEY":    "secret",
		"CUSTOM_VAR": "ignored",
	})
	if env["PATH"] != "/bin" {
		t.Fatalf("core env missing PATH: %+v", env)
	}
	if runtime.GOOS != "windows" && env["HOME"] != "/home/me" {
		t.Fatalf("core env missing HOME: %+v", env)
	}
	if _, ok := env["API_KEY"]; ok {
		t.Fatalf("API_KEY inherited despite default excludes: %+v", env)
	}
	if _, ok := env["CUSTOM_VAR"]; ok {
		t.Fatalf("CUSTOM_VAR inherited despite core mode: %+v", env)
	}
	if env["CODEX_PUBLIC"] != "ok" {
		t.Fatalf("set env missing: %+v", env)
	}
}

func TestCreateEnvAppliesIncludeOnlyAfterSet(t *testing.T) {
	env := CreateEnv(&EnvPolicy{
		Inherit:     "all",
		Set:         map[string]string{"KEEP_SET": "set", "DROP_SET": "set"},
		IncludeOnly: []EnvVariablePattern{{Mode: EnvPatternWildcard, Value: "KEEP*"}},
	}, nil, map[string]string{"KEEP_BASE": "base", "DROP_BASE": "base"})
	if env["KEEP_BASE"] != "base" || env["KEEP_SET"] != "set" {
		t.Fatalf("keep vars missing: %+v", env)
	}
	if _, ok := env["DROP_BASE"]; ok {
		t.Fatalf("DROP_BASE survived include_only: %+v", env)
	}
	if _, ok := env["DROP_SET"]; ok {
		t.Fatalf("DROP_SET survived include_only: %+v", env)
	}
}

func TestInjectPermissionProfile(t *testing.T) {
	env := map[string]string{PermissionProfileEnvVar: "old"}
	profile := "full-access"
	InjectPermissionProfile(env, &profile)
	if env[PermissionProfileEnvVar] != "full-access" {
		t.Fatalf("profile env = %q", env[PermissionProfileEnvVar])
	}
	InjectPermissionProfile(env, nil)
	if _, ok := env[PermissionProfileEnvVar]; ok {
		t.Fatalf("profile env still present: %+v", env)
	}
}

func TestVariablePatternMatches(t *testing.T) {
	cases := []struct {
		pattern EnvVariablePattern
		key     string
		want    bool
	}{
		{EnvVariablePattern{Mode: EnvPatternLiteral, Value: "PATH"}, "PATH", true},
		{EnvVariablePattern{Mode: EnvPatternPrefix, Value: "AWS_"}, "AWS_REGION", true},
		{EnvVariablePattern{Mode: EnvPatternSuffix, Value: "_TOKEN"}, "API_TOKEN", true},
		{EnvVariablePattern{Mode: EnvPatternContains, Value: "PROXY"}, "HTTPS_PROXY", true},
	}
	for i := range cases {
		if got := cases[i].pattern.Matches(cases[i].key); got != cases[i].want {
			t.Fatalf("case %d Matches() = %v, want %v", i, got, cases[i].want)
		}
	}
}
