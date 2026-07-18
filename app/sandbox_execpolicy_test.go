package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"codex_go/cli"
	"codex_go/config"
	"codex_go/sandbox"
	"codex_go/sandbox/windowssandbox"
)

func TestExecpolicyCheckCommand(t *testing.T) {
	dir := t.TempDir()
	policyPath := filepath.Join(dir, "policy.rules")
	body := `
prefix_rule(
    pattern = ["git", "push"],
    decision = "forbidden",
    justification = "pushing is blocked in this repo",
)
`
	if err := os.WriteFile(policyPath, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"execpolicy", "check", "--rules", policyPath, "git", "push", "origin"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("execpolicy check returned error: %v", err)
	}
	assertJSONValue(t, stdout.Bytes(), map[string]any{
		"decision": "forbidden",
		"matchedRules": []any{
			map[string]any{
				"prefixRuleMatch": map[string]any{
					"matchedPrefix": []any{"git", "push"},
					"decision":      "forbidden",
					"justification": "pushing is blocked in this repo",
				},
			},
		},
	})
}

func TestExecpolicyCheckCommandOmitsAbsentJustificationLikeRust(t *testing.T) {
	dir := t.TempDir()
	policyPath := filepath.Join(dir, "policy.rules")
	body := `
prefix_rule(
    pattern = ["git", "push"],
    decision = "forbidden",
)
`
	if err := os.WriteFile(policyPath, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"execpolicy", "check", "--rules", policyPath, "git", "push", "origin", "main"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("execpolicy check returned error: %v", err)
	}
	assertJSONValue(t, stdout.Bytes(), map[string]any{
		"decision": "forbidden",
		"matchedRules": []any{
			map[string]any{
				"prefixRuleMatch": map[string]any{
					"matchedPrefix": []any{"git", "push"},
					"decision":      "forbidden",
				},
			},
		},
	})
}

func assertJSONValue(t *testing.T, data []byte, want any) {
	t.Helper()
	var got any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal(%q) error = %v", string(data), err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("JSON = %#v, want %#v", got, want)
	}
}

func TestSandboxSetupCurrentUser(t *testing.T) {
	t.Setenv("USERNAME", "alice")
	t.Setenv("CODEX_HOME", t.TempDir())
	called := false
	oldSetup := runWindowsSandboxProvisioningSetup
	runWindowsSandboxProvisioningSetup = func(req *windowssandbox.SandboxSetupRequest) error {
		called = true
		if req == nil || req.RealUser != "alice" || req.CodexHome == "" {
			t.Fatalf("setup request = %#v, want alice and codex home", req)
		}
		return nil
	}
	defer func() { runWindowsSandboxProvisioningSetup = oldSetup }()

	var stdout bytes.Buffer
	err := Run(context.Background(), []string{"sandbox", "setup", "--elevated", "--current-user"}, strings.NewReader(""), &stdout, &bytes.Buffer{})
	if runtime.GOOS != "windows" {
		if err == nil || !strings.Contains(err.Error(), "only supported on Windows") {
			t.Fatalf("sandbox setup error = %v, want non-Windows unsupported", err)
		}
		return
	}
	if err != nil {
		t.Fatalf("sandbox setup returned error: %v", err)
	}
	if !called {
		t.Fatalf("sandbox setup did not invoke Windows provisioning setup")
	}
	if !strings.Contains(stdout.String(), "Windows elevated sandbox setup completed for alice") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestSandboxSetupValidatesBeforePlatformCheck(t *testing.T) {
	err := Run(context.Background(), []string{"sandbox", "setup", "--current-user"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "`codex sandbox setup` currently requires --elevated") {
		t.Fatalf("sandbox setup missing elevated error = %v", err)
	}

	err = Run(context.Background(), []string{"sandbox", "setup", "--elevated"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "--user or --current-user is required") {
		t.Fatalf("sandbox setup missing user error = %v", err)
	}
}

func TestSandboxRunsCommand(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"sandbox", "-P", ":danger-full-access", "--", "go", "version"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("sandbox command returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "go version") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestSandboxFullAccessProfileRunsAndInjectsEnv(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	var stdout bytes.Buffer
	args := append([]string{"sandbox", "-P", ":danger-full-access", "--"}, sandboxEnvCommand("CODEX_PERMISSION_PROFILE")...)
	if err := Run(context.Background(), args, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("sandbox full access command returned error: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != ":danger-full-access" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestSandboxDefaultsLegacyConfigToReadOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(config.ConfigPath(home), []byte(`sandbox_mode = "danger-full-access"`+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	resolved, err := resolveSandboxPermissionProfileForRun(&cli.SandboxOptions{})
	if err != nil {
		t.Fatalf("resolveSandboxPermissionProfileForRun() error = %v", err)
	}
	if resolved == nil || resolved.ID != "read-only" || resolved.Profile == nil || resolved.Profile.Disabled {
		t.Fatalf("resolved = %#v, want read-only sandbox profile", resolved)
	}
}

func TestSandboxHonorsLegacySandboxModeCLIOverride(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())

	resolved, err := resolveSandboxPermissionProfileForRun(&cli.SandboxOptions{
		ConfigOverrides: []string{`sandbox_mode="danger-full-access"`},
	})
	if err != nil {
		t.Fatalf("resolveSandboxPermissionProfileForRun() error = %v", err)
	}
	if resolved == nil || resolved.ID != "danger-full-access" || resolved.Profile == nil || !resolved.Profile.Disabled {
		t.Fatalf("resolved = %#v, want danger-full-access legacy override", resolved)
	}
}

func TestSandboxRunConfigUsesLegacyLandlockFeature(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(config.ConfigPath(home), []byte("[features]\nuse_legacy_landlock = true\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	runConfig, err := loadSandboxRunConfigForRun(&cli.SandboxOptions{})
	if err != nil {
		t.Fatalf("loadSandboxRunConfigForRun() error = %v", err)
	}
	if !runConfig.UseLegacyLandlock {
		t.Fatalf("UseLegacyLandlock = false, want true")
	}
}

func TestSandboxIncludeManagedConfigLoadsManagedEnvPolicy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(config.ConfigPath(home), []byte(`default_permissions = ":danger-full-access"`+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}
	managed := `[shell_environment_policy.set]
CODEX_MANAGED_CONFIG = "loaded"
`
	if err := os.WriteFile(filepath.Join(home, "managed_config.toml"), []byte(managed), 0o600); err != nil {
		t.Fatalf("WriteFile managed config returned error: %v", err)
	}

	runConfig, err := loadSandboxRunConfigForRun(&cli.SandboxOptions{PermissionProfile: ":danger-full-access"})
	if err != nil {
		t.Fatalf("loadSandboxRunConfigForRun(no include) error = %v", err)
	}
	if runConfig.EnvPolicy.Set["CODEX_MANAGED_CONFIG"] != "" {
		t.Fatalf("managed env policy loaded without include: %#v", runConfig.EnvPolicy.Set)
	}

	runConfig, err = loadSandboxRunConfigForRun(&cli.SandboxOptions{
		PermissionProfile:    ":danger-full-access",
		IncludeManagedConfig: true,
	})
	if err != nil {
		t.Fatalf("loadSandboxRunConfigForRun(include) error = %v", err)
	}
	if runConfig.EnvPolicy.Set["CODEX_MANAGED_CONFIG"] != "loaded" {
		t.Fatalf("managed env policy = %#v, want loaded", runConfig.EnvPolicy.Set)
	}
}

func TestSandboxInheritsRootFeatureToggles(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	parsed, err := cli.Parse([]string{"--enable", "use_legacy_landlock", "sandbox", "--", "go", "version"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	opts := sandboxOptionsForRun(parsed)
	runConfig, err := loadSandboxRunConfigForRun(&opts)
	if err != nil {
		t.Fatalf("loadSandboxRunConfigForRun() error = %v", err)
	}
	if !runConfig.UseLegacyLandlock {
		t.Fatalf("UseLegacyLandlock = false, want root --enable to apply")
	}
}

func TestSandboxConfigOverrideWinsOverRootFeatureToggle(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	parsed, err := cli.Parse([]string{
		"--enable", "use_legacy_landlock",
		"sandbox",
		"-c", "features.use_legacy_landlock=false",
		"--",
		"go", "version",
	})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	opts := sandboxOptionsForRun(parsed)
	runConfig, err := loadSandboxRunConfigForRun(&opts)
	if err != nil {
		t.Fatalf("loadSandboxRunConfigForRun() error = %v", err)
	}
	if runConfig.UseLegacyLandlock {
		t.Fatalf("UseLegacyLandlock = true, want sandbox -c override to win")
	}
}

func TestSandboxRunConfigHonorsActiveDefaultPermissionProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	body := `default_permissions = "dev"

[permissions.dev.filesystem]
":minimal" = "read"

[permissions.dev.network]
enabled = true
`
	if err := os.WriteFile(config.ConfigPath(home), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	runConfig, err := loadSandboxRunConfigForRun(&cli.SandboxOptions{})
	if err != nil {
		t.Fatalf("loadSandboxRunConfigForRun() error = %v", err)
	}
	if runConfig.PermissionProfile == nil || runConfig.PermissionProfile.ID != "dev" || runConfig.PermissionProfile.Profile == nil || !runConfig.PermissionProfile.Profile.AllowsNetwork() {
		t.Fatalf("PermissionProfile = %#v, want active dev profile with network", runConfig.PermissionProfile)
	}
}

func TestSandboxHonorsRootConfigOverrides(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	var stdout bytes.Buffer
	args := append([]string{"-c", `default_permissions=":danger-full-access"`, "sandbox", "--"}, sandboxEnvCommand("CODEX_PERMISSION_PROFILE")...)
	if err := Run(context.Background(), args, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("sandbox root config override command returned error: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != ":danger-full-access" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestSandboxConfigOverridesRootConfigOverrides(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	var stdout bytes.Buffer
	args := append([]string{
		"-c", `default_permissions=":workspace"`,
		"sandbox",
		"-c", `default_permissions=":danger-full-access"`,
		"--",
	}, sandboxEnvCommand("CODEX_PERMISSION_PROFILE")...)
	if err := Run(context.Background(), args, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("sandbox command override returned error: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != ":danger-full-access" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestSandboxHonorsRootProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(config.ProfileConfigPath(home, "work"), []byte(`default_permissions = ":danger-full-access"`+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var stdout bytes.Buffer
	args := append([]string{"--profile", "work", "sandbox", "--"}, sandboxEnvCommand("CODEX_PERMISSION_PROFILE")...)
	if err := Run(context.Background(), args, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("sandbox root profile command returned error: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != ":danger-full-access" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestSandboxHonorsShellEnvironmentPolicy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	body := `default_permissions = ":danger-full-access"

[shell_environment_policy]
inherit = "core"

[shell_environment_policy.set]
CODEX_ENV_POLICY = "kept"
CODEX_PERMISSION_PROFILE = "from-policy"
`
	if err := os.WriteFile(config.ConfigPath(home), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var stdout bytes.Buffer
	args := append([]string{"sandbox", "--"}, sandboxEnvCommand("CODEX_ENV_POLICY")...)
	if err := Run(context.Background(), args, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("sandbox env policy command returned error: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != "kept" {
		t.Fatalf("CODEX_ENV_POLICY stdout = %q", stdout.String())
	}

	stdout.Reset()
	args = append([]string{"sandbox", "--"}, sandboxEnvCommand("CODEX_PERMISSION_PROFILE")...)
	if err := Run(context.Background(), args, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("sandbox permission env command returned error: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != ":danger-full-access" {
		t.Fatalf("CODEX_PERMISSION_PROFILE stdout = %q", stdout.String())
	}
}

func TestSandboxProfileOverridesRootProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(config.ProfileConfigPath(home, "work"), []byte(`default_permissions = ":workspace"`+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(work) error = %v", err)
	}
	if err := os.WriteFile(config.ProfileConfigPath(home, "local"), []byte(`default_permissions = ":danger-full-access"`+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(local) error = %v", err)
	}

	var stdout bytes.Buffer
	args := append([]string{"--profile", "work", "sandbox", "--profile", "local", "--"}, sandboxEnvCommand("CODEX_PERMISSION_PROFILE")...)
	if err := Run(context.Background(), args, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("sandbox explicit profile command returned error: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != ":danger-full-access" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunSandboxPreservesChildExitCode(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("CODEX_APP_SANDBOX_EXIT_HELPER", "1")

	err := runSandbox(context.Background(), &cli.SandboxOptions{
		PermissionProfile: ":danger-full-access",
		Command:           []string{os.Args[0], "-test.run=TestRunSandboxChildExitHelper"},
	}, nil, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})

	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 7 || !exitErr.Silent {
		t.Fatalf("runSandbox error = %+v, want silent exit code 7", err)
	}
	if ExitCode(err) != 7 || ShouldPrintError(err) {
		t.Fatalf("exit handling = code %d print %t, want code 7 silent", ExitCode(err), ShouldPrintError(err))
	}
}

func TestRunSandboxChildExitHelper(t *testing.T) {
	if os.Getenv("CODEX_APP_SANDBOX_EXIT_HELPER") != "1" {
		return
	}
	os.Exit(7)
}

func TestRunWindowsSandboxPlanPreservesWrapperExitCode(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows sandbox wrapper is only used on Windows")
	}
	t.Setenv("CODEX_HOME", t.TempDir())

	oldCreateArgs := createWindowsSandboxCommandArgs
	oldRunWrapper := runWindowsSandboxWrapperExitCode
	createWindowsSandboxCommandArgs = func(req windowssandbox.WindowsSandboxCommandArgsRequest) ([]string, error) {
		return []string{"wrapped-windows-sandbox"}, nil
	}
	runWindowsSandboxWrapperExitCode = func(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) (int, error) {
		if len(args) != 1 || args[0] != "wrapped-windows-sandbox" {
			t.Fatalf("wrapper args = %#v", args)
		}
		return 23, nil
	}
	defer func() {
		createWindowsSandboxCommandArgs = oldCreateArgs
		runWindowsSandboxWrapperExitCode = oldRunWrapper
	}()

	profile := sandbox.WorkspaceWritePermissionProfile()
	err := runWindowsSandboxPlan(context.Background(), &sandbox.CommandRunPlan{
		Command:           []string{"go", "version"},
		CWD:               t.TempDir(),
		PermissionProfile: &profile,
	}, map[string]string{}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})

	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 23 || !exitErr.Silent {
		t.Fatalf("runWindowsSandboxPlan error = %+v, want silent exit code 23", err)
	}
}

func TestSandboxWorkspaceProfileRequiresPlatformBackend(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	if runtime.GOOS == "windows" {
		oldCreateArgs := createWindowsSandboxCommandArgs
		oldRunWrapper := runWindowsSandboxWrapperExitCode
		calledCreate := false
		calledRun := false
		createWindowsSandboxCommandArgs = func(req windowssandbox.WindowsSandboxCommandArgsRequest) ([]string, error) {
			calledCreate = true
			if req.WindowsSandboxLevel != windowssandbox.WindowsSandboxLevelElevated {
				t.Fatalf("WindowsSandboxLevel = %q, want elevated", req.WindowsSandboxLevel)
			}
			if len(req.Command) != 2 || req.Command[0] != "go" || req.Command[1] != "version" {
				t.Fatalf("Command = %#v, want go version", req.Command)
			}
			if !filepath.IsAbs(req.CommandCWD) {
				t.Fatalf("CommandCWD = %q, want absolute", req.CommandCWD)
			}
			if len(req.WorkspaceRoots) != 1 || req.WorkspaceRoots[0] != req.CommandCWD {
				t.Fatalf("WorkspaceRoots = %#v, CommandCWD = %q", req.WorkspaceRoots, req.CommandCWD)
			}
			if req.PermissionProfile == nil || req.PermissionProfile.Disabled {
				t.Fatalf("PermissionProfile = %#v, want sandboxed profile", req.PermissionProfile)
			}
			if req.Env["CODEX_PERMISSION_PROFILE"] != ":workspace" {
				t.Fatalf("CODEX_PERMISSION_PROFILE = %q", req.Env["CODEX_PERMISSION_PROFILE"])
			}
			if req.CodexHome != os.Getenv("CODEX_HOME") {
				t.Fatalf("CodexHome = %q, want %q", req.CodexHome, os.Getenv("CODEX_HOME"))
			}
			return []string{"wrapped-windows-sandbox"}, nil
		}
		runWindowsSandboxWrapperExitCode = func(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) (int, error) {
			calledRun = true
			if len(args) != 1 || args[0] != "wrapped-windows-sandbox" {
				t.Fatalf("wrapper args = %#v", args)
			}
			_, err := io.WriteString(stdout, "windows sandbox ok")
			return 0, err
		}
		defer func() {
			createWindowsSandboxCommandArgs = oldCreateArgs
			runWindowsSandboxWrapperExitCode = oldRunWrapper
		}()

		var stdout bytes.Buffer
		err := Run(context.Background(), []string{"sandbox", "-P", ":workspace", "--", "go", "version"}, strings.NewReader(""), &stdout, &bytes.Buffer{})
		if err != nil {
			t.Fatalf("sandbox workspace windows error = %v", err)
		}
		if !calledCreate || !calledRun {
			t.Fatalf("calledCreate = %t, calledRun = %t", calledCreate, calledRun)
		}
		if stdout.String() != "windows sandbox ok" {
			t.Fatalf("stdout = %q", stdout.String())
		}
		return
	}
	err := Run(context.Background(), []string{"sandbox", "-P", ":workspace", "--", "go", "version"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if runtime.GOOS == "linux" {
		if err != nil && !strings.Contains(err.Error(), "executable file not found") && !strings.Contains(err.Error(), "bubblewrap is unavailable") {
			t.Fatalf("sandbox workspace linux error = %v", err)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), "requires platform sandbox support") {
		t.Fatalf("sandbox workspace error = %v", err)
	}
}

func sandboxEnvCommand(name string) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/c", "echo %" + name + "%"}
	}
	return []string{"sh", "-c", "printf \"$" + name + "\""}
}
