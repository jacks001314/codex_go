package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"codex_go/applypatch"
	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
	"codex_go/doctor"
	codexexec "codex_go/exec"
	"codex_go/execpolicy"
	"codex_go/execserver"
	"codex_go/features"
	"codex_go/install"
	"codex_go/mcp"
	"codex_go/model"
	codexnetwork "codex_go/network"
	"codex_go/rollout"
	"codex_go/sandbox"
	"codex_go/sandbox/windowssandbox"
	"codex_go/state"
)

const (
	chatGPTLoginDisabledMessage     = "ChatGPT login is disabled. Use API key login instead."
	apiKeyLoginDisabledMessage      = "API key login is disabled. Use ChatGPT login instead."
	accessTokenLoginDisabledMessage = "Access token login is disabled. Use API key login instead."
	loginCredentialSourceConflict   = "Choose one login credential source: --with-api-key or --with-access-token."
	apiKeyFlagUnsupportedMessage    = "The --api-key flag is no longer supported. Pipe the key instead, e.g. `printenv OPENAI_API_KEY | codex login --with-api-key`."
	apiKeyStdinTerminalMessage      = "--with-api-key expects the API key on stdin. Try piping it, e.g. `printenv OPENAI_API_KEY | codex login --with-api-key`."
	apiKeyStdinReadingMessage       = "Reading API key from stdin..."
	apiKeyStdinEmptyMessage         = "No API key provided via stdin."
	accessTokenStdinTerminalMessage = "--with-access-token expects the access token on stdin. Try piping it, e.g. `printenv CODEX_ACCESS_TOKEN | codex login --with-access-token`."
	accessTokenStdinReadingMessage  = "Reading access token from stdin..."
	accessTokenStdinEmptyMessage    = "No access token provided via stdin."
)

var (
	runWindowsSandboxProvisioningSetup = windowssandbox.RunElevatedProvisioningSetup
	createWindowsSandboxCommandArgs    = windowssandbox.CreateWindowsSandboxCommandArgsForPermissionProfile
	runWindowsSandboxWrapperExitCode   = windowssandbox.RunWindowsSandboxWrapperExitCode
	newCodexExecRunner                 = codexexec.NewRunner
)

type RunOptions struct {
	DispatchPaths *cli.DispatchPaths
}

func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	return RunWithOptions(ctx, args, stdin, stdout, stderr, nil)
}

func RunWithOptions(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, runOpts *RunOptions) error {
	if handled, err := runRootMeta(args, stdout); handled || err != nil {
		return err
	}
	parsed, err := cli.Parse(args)
	if err != nil {
		return err
	}
	if err := validateFeatureToggles(parsed.Root.EnableFeatures, parsed.Root.DisableFeatures); err != nil {
		return err
	}
	switch parsed.Command {
	case cli.CommandLogin:
		login := parsed.Login
		login.ConfigOverrides = append(append([]string(nil), parsed.Root.ConfigOverrides...), login.ConfigOverrides...)
		return runLogin(ctx, login, stdin, stdout, stderr)
	case cli.CommandLogout:
		logout := parsed.Login
		logout.ConfigOverrides = append(append([]string(nil), parsed.Root.ConfigOverrides...), logout.ConfigOverrides...)
		return runLogout(ctx, logout, stdout)
	case cli.CommandFeatures:
		return runFeatures(parsed.Features, parsed.Root, stdout, stderr)
	case cli.CommandMCP:
		return runMCP(ctx, &parsed.MCP, stdout)
	case cli.CommandPlugin:
		return runPlugin(&parsed.Plugin, stdout)
	case cli.CommandInteractive:
		return runInteractiveEntry(ctx, &parsed.Root, stdin, stdout, stderr)
	case cli.CommandExec:
		_, err := newCodexExecRunner(auth.DefaultCodexHome()).RunContext(ctx, &codexexec.Request{
			Root: parsed.Root,
			Exec: parsed.Exec,
		}, stdin, stdout, stderr)
		return err
	case cli.CommandReview:
		_, err := newCodexExecRunner(auth.DefaultCodexHome()).RunContext(ctx, &codexexec.Request{
			Root: parsed.Root,
			Exec: parsed.Exec,
		}, stdin, stdout, stderr)
		return err
	case cli.CommandCompletion:
		return runCompletion(parsed.Completion, stdout)
	case cli.CommandDebug:
		return runDebug(parsed.Debug, &parsed.Root, stdout)
	case cli.CommandDoctor:
		report, err := doctor.Run(&doctor.Options{
			JSON:     parsed.Doctor.JSON,
			Summary:  parsed.Doctor.Summary,
			All:      parsed.Doctor.All,
			NoColor:  parsed.Doctor.NoColor,
			ASCII:    parsed.Doctor.ASCII,
			Feedback: parsed.Doctor.Feedback,
			Root:     parsed.Root,
			DispatchPaths: func() *cli.DispatchPaths {
				if runOpts == nil || runOpts.DispatchPaths == nil {
					return nil
				}
				paths := *runOpts.DispatchPaths
				return &paths
			}(),
		}, stdout)
		if err != nil {
			return err
		}
		if report != nil && report.OverallStatus == doctor.CheckStatusFail {
			return &ExitError{Code: 1, Message: "codex doctor found failing checks", Silent: true}
		}
		return nil
	case cli.CommandApply:
		return runApply(parsed.Apply, parsed.Root, stdin, stdout)
	case cli.CommandMigrateRollouts:
		return runMigrateRollouts(parsed.MigrateRollouts, parsed.Root, stdout, stderr)
	case cli.CommandRemoteControl:
		return runRemoteControl(ctx, parsed.RemoteControl, stdout)
	case cli.CommandAppServer:
		return runAppServer(ctx, parsed.AppServer, &parsed.Root, stdout, stderr, stdin)
	case cli.CommandApp:
		return runDesktopApp(&parsed.App, stdout)
	case cli.CommandMCPServer:
		return runMCPServer(ctx, &parsed.MCPServer, &parsed.Root, stdin, stdout)
	case cli.CommandCloud:
		return runCloud(ctx, &parsed.Cloud, stdin, stdout)
	case cli.CommandResponsesAPIProxy:
		return runResponsesAPIProxy(ctx, &parsed.ResponsesAPIProxy, stdin, stdout, stderr)
	case cli.CommandStdioToUDS:
		return runStdioToUDS(&parsed.StdioToUDS, stdin, stdout)
	case cli.CommandExecServer:
		return runExecServer(ctx, &parsed.ExecServer, &parsed.Root, stdin, stdout)
	case cli.CommandSandbox:
		sandboxOpts := sandboxOptionsForRun(parsed)
		var dispatchPaths *cli.DispatchPaths
		if runOpts != nil && runOpts.DispatchPaths != nil {
			paths := *runOpts.DispatchPaths
			dispatchPaths = &paths
		}
		return runSandbox(ctx, &sandboxOpts, dispatchPaths, stdin, stdout, stderr)
	case cli.CommandExecpolicy:
		return runExecpolicy(&parsed.Execpolicy, stdout)
	case cli.CommandResume:
		return runSessionResume(&parsed.Session, &parsed.Root, stdout)
	case cli.CommandArchive:
		return runSessionArchive(&parsed.Session, &parsed.Root, stdout)
	case cli.CommandDelete:
		return runSessionDelete(&parsed.Session, &parsed.Root, stdin, stdout, stderr)
	case cli.CommandUnarchive:
		return runSessionUnarchive(&parsed.Session, &parsed.Root, stdout)
	case cli.CommandFork:
		return runSessionFork(&parsed.Session, &parsed.Root, stdout)
	case cli.CommandQueue:
		return runSessionQueue(&parsed.Queue, &parsed.Root, stdout)
	case cli.CommandAgents:
		return runAgentsCommandWithIO(ctx, &parsed.Agents, &parsed.Root, stdin, stdout)
	case cli.CommandUpdate:
		return runUpdate(ctx, &parsed.Update, stdout, stderr)
	default:
		return notImplemented(string(parsed.Command))
	}
}

func runRootMeta(args []string, stdout io.Writer) (bool, error) {
	if len(args) != 1 {
		return false, nil
	}
	switch args[0] {
	case "-h", "--help", "help":
		_, err := fmt.Fprint(stdout, rootHelpText())
		return true, err
	case "-V", "--version":
		_, err := fmt.Fprintf(stdout, "codex-cli %s\n", doctor.Version())
		return true, err
	default:
		return false, nil
	}
}

func rootHelpText() string {
	return strings.Join([]string{
		"Codex CLI",
		"",
		"If no subcommand is specified, options will be forwarded to the interactive CLI.",
		"",
		"Usage: codex [OPTIONS] [PROMPT]",
		"       codex [OPTIONS] <COMMAND> [ARGS]",
		"",
		"Commands:",
		"  exec            Run Codex non-interactively [aliases: e]",
		"  review          Run a code review non-interactively",
		"  login           Manage login",
		"  logout          Remove stored authentication credentials",
		"  mcp             Manage external MCP servers for Codex",
		"  plugin          Manage Codex plugins",
		"  mcp-server      Start Codex as an MCP server (stdio)",
		"  app-server      [experimental] Run the app server or related tooling",
		"  remote-control  [experimental] Manage the app-server daemon with remote control enabled",
		"  app             Launch the Codex desktop app (opens the app installer if missing)",
		"  completion      Generate shell completion scripts",
		"  update          Update Codex to the latest version",
		"  doctor          Diagnose local Codex installation, config, auth, and runtime health",
		"  sandbox         Run commands within a Codex-provided sandbox",
		"  debug           Debugging tools",
		"  apply           Apply the latest diff produced by Codex agent as a `git apply` to your local working tree [aliases: a]",
		"  resume          Resume a previous interactive session (picker by default; use --last to continue the most recent)",
		"  archive         Archive a saved session by id or session name",
		"  delete          Permanently delete a saved session by id or session name",
		"  unarchive       Unarchive a saved session by id or session name",
		"  fork            Fork a previous interactive session (picker by default; use --last to fork the most recent)",
		"  cloud           [EXPERIMENTAL] Browse tasks from Codex Cloud and apply changes locally",
		"  exec-server     [EXPERIMENTAL] Run the standalone exec-server service",
		"  features        Inspect feature flags",
		"  help            Print this message or the help of the given subcommand(s)",
		"",
		"Options:",
		"  -h, --help      Print help (see a summary with '-h')",
		"  -V, --version   Print version",
	}, "\n") + "\n"
}

func runSandbox(ctx context.Context, opts *cli.SandboxOptions, dispatchPaths *cli.DispatchPaths, stdin io.Reader, stdout, stderr io.Writer) error {
	if opts.Setup {
		cmd := &sandbox.SetupCommand{
			Elevated:    opts.Elevated,
			User:        opts.User,
			CurrentUser: opts.CurrentUser,
			CodexHome:   opts.CodexHome,
		}
		if err := cmd.Validate(); err != nil {
			return err
		}
		if runtime.GOOS != "windows" {
			return errors.New("elevated Windows sandbox setup is only supported on Windows")
		}
		identity, err := sandbox.ResolveSetupIdentity(cmd, auth.DefaultCodexHome())
		if err != nil {
			return err
		}
		if err := runWindowsSandboxProvisioningSetup(&windowssandbox.SandboxSetupRequest{
			RealUser:  identity.RealUser,
			CodexHome: identity.CodexHome,
		}); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Windows elevated sandbox setup completed for %s at %s.\n", identity.RealUser, identity.CodexHome)
		return nil
	}
	if len(opts.Command) == 0 {
		return errors.New("sandbox requires COMMAND")
	}
	runReq := &sandbox.CommandRunRequest{
		PermissionProfile:     opts.PermissionProfile,
		ConfigProfile:         opts.ConfigProfile,
		CWD:                   opts.CWD,
		IncludeManagedConfig:  opts.IncludeManagedConfig,
		SandboxStateJSON:      opts.SandboxStateJSON,
		SandboxReadableRoots:  opts.SandboxReadableRoots,
		SandboxDisableNetwork: opts.SandboxDisableNetwork,
		AllowUnixSockets:      opts.AllowUnixSockets,
		LogDenials:            opts.LogDenials,
		CodexLinuxSandboxExe:  cli.LinuxSandboxExePath(dispatchPaths, ""),
		ConfigOverrides:       opts.ConfigOverrides,
		Command:               opts.Command,
	}
	runConfig, err := loadSandboxRunConfigForRunContext(ctx, opts)
	if err != nil {
		return err
	}
	runReq.UseLegacyLandlock = runConfig.UseLegacyLandlock
	resolved := runConfig.PermissionProfile
	if resolved != nil {
		runReq.PermissionProfile = ""
		runReq.ConfigProfile = ""
		runReq.ConfigOverrides = nil
		runReq.IncludeManagedConfig = false
		runReq.ResolvedPermissionProfile = resolved.Profile
		runReq.ResolvedPermissionProfileID = resolved.ID
		runReq.ResolvedPermissionProfileJSON = resolved.ProfileJSON
	}
	plan, err := sandbox.BuildCommandRunPlan(runReq)
	if err != nil {
		return err
	}
	if err := plan.UnsupportedError(); err != nil {
		return err
	}
	env := codexexec.CreateEnv(runConfig.EnvPolicy, nil, environmentMapFromEnviron(os.Environ()))
	if plan.PermissionProfileID != "" {
		codexexec.InjectPermissionProfile(env, &plan.PermissionProfileID)
	}
	if shouldUseWindowsSandbox(plan) {
		return runWindowsSandboxPlan(ctx, plan, env, stdin, stdout, stderr)
	}
	command := exec.CommandContext(ctx, plan.Command[0], plan.Command[1:]...)
	command.Stdin = stdin
	command.Stdout = stdout
	command.Stderr = stderr
	if plan.CWD != "" {
		command.Dir = filepath.Clean(plan.CWD)
	}
	command.Env = envSlice(env)
	return processExitError(command.Run())
}

func shouldUseWindowsSandbox(plan *sandbox.CommandRunPlan) bool {
	return runtime.GOOS == "windows" &&
		plan != nil &&
		plan.PermissionProfile != nil &&
		!plan.PermissionProfile.Disabled
}

func runWindowsSandboxPlan(ctx context.Context, plan *sandbox.CommandRunPlan, env map[string]string, stdin io.Reader, stdout, stderr io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	commandCWD, err := resolveWindowsSandboxCommandCWD(plan.CWD)
	if err != nil {
		return err
	}
	codexHome, err := absoluteWindowsSandboxPath(auth.DefaultCodexHome())
	if err != nil {
		return err
	}
	env = cloneStringMapLocal(env)
	args, err := createWindowsSandboxCommandArgs(windowssandbox.WindowsSandboxCommandArgsRequest{
		Command:             append([]string(nil), plan.Command...),
		CommandCWD:          commandCWD,
		WorkspaceRoots:      []string{commandCWD},
		Env:                 env,
		PermissionProfile:   plan.PermissionProfile,
		WindowsSandboxLevel: windowssandbox.WindowsSandboxLevelElevated,
		CodexHome:           codexHome,
	})
	if err != nil {
		return err
	}
	exitCode, err := runWindowsSandboxWrapperExitCode(args, stdin, stdout, stderr)
	if err != nil {
		return err
	}
	if exitCode != 0 {
		return silentExitCode(exitCode)
	}
	return nil
}

func projectMigratedRollouts(codexHome string, report *rollout.MigrationReport) error {
	if report == nil {
		return nil
	}
	config, err := state.SqliteConfigForCodexHome(codexHome)
	if err != nil {
		return err
	}
	for _, outcome := range report.Outcomes {
		if outcome.Status != rollout.MigrationStatusMigrated {
			continue
		}
		threadID := strings.TrimSpace(outcome.RolloutPath)
		meta, err := rollout.FirstSessionMeta(outcome.RolloutPath)
		if err != nil {
			return fmt.Errorf("read migrated rollout metadata %s: %w", outcome.RolloutPath, err)
		}
		threadID = strings.TrimSpace(meta.ID)
		if threadID == "" {
			continue
		}
		db, err := config.OpenThreadHistoryDB(context.Background())
		if err != nil {
			return fmt.Errorf("open thread history database: %w", err)
		}
		err = state.MaterializeThreadHistory(context.Background(), db, threadID, outcome.RolloutPath, 0, meta.SubagentHistoryStartOrdinal)
		db.Close()
		if err != nil {
			return fmt.Errorf("project thread history for %s: %w", threadID, err)
		}
	}
	return nil
}

func processExitError(err error) error {
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return silentExitCode(exitCodeFromExitError(exitErr))
	}
	return err
}

func silentExitCode(code int) error {
	if code <= 0 {
		code = 1
	}
	return &ExitError{Code: code, Silent: true}
}

func exitMessagef(format string, args ...any) error {
	return &ExitError{Code: 1, Message: fmt.Sprintf(format, args...)}
}

func resolveWindowsSandboxCommandCWD(cwd string) (string, error) {
	if strings.TrimSpace(cwd) == "" {
		resolved, err := os.Getwd()
		if err != nil {
			return "", err
		}
		return filepath.Clean(resolved), nil
	}
	return absoluteWindowsSandboxPath(cwd)
}

func absoluteWindowsSandboxPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("windows sandbox path is required")
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	resolved, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

func sandboxOptionsForRun(parsed *cli.Parsed) cli.SandboxOptions {
	if parsed == nil {
		return cli.SandboxOptions{}
	}
	opts := parsed.Sandbox
	if opts.ConfigProfile == "" {
		opts.ConfigProfile = parsed.Root.Shared.Profile
	}
	rootOverrides := rootConfigOverridesWithFeatureToggles(parsed.Root)
	opts.ConfigOverrides = append(append([]string(nil), rootOverrides...), opts.ConfigOverrides...)
	return opts
}

func rootConfigOverridesWithFeatureToggles(root cli.RootOptions) []string {
	overrides := append([]string(nil), root.ConfigOverrides...)
	for _, feature := range root.EnableFeatures {
		overrides = append(overrides, "features."+feature+"=true")
	}
	for _, feature := range root.DisableFeatures {
		overrides = append(overrides, "features."+feature+"=false")
	}
	return overrides
}

// featureEnablementFromRoot converts the CLI --enable/--disable feature toggles
// (and features.* config overrides) into the enablement map seeded into the
// app-server ConfigService so protocol-level feature gates apply at startup.
func featureEnablementFromRoot(root *cli.RootOptions) map[string]bool {
	if root == nil {
		return nil
	}
	enablement := map[string]bool{}
	for _, feature := range root.EnableFeatures {
		enablement[strings.TrimSpace(feature)] = true
	}
	for _, feature := range root.DisableFeatures {
		enablement[strings.TrimSpace(feature)] = false
	}
	for _, override := range root.ConfigOverrides {
		key, value, ok := strings.Cut(strings.TrimSpace(override), "=")
		if !ok || !strings.HasPrefix(key, "features.") {
			continue
		}
		parsed, err := strconv.ParseBool(strings.TrimSpace(value))
		if err != nil {
			continue
		}
		enablement[strings.TrimPrefix(key, "features.")] = parsed
	}
	if len(enablement) == 0 {
		return nil
	}
	return enablement
}

type sandboxRunConfig struct {
	PermissionProfile *config.SandboxPermissionProfileResolution
	EnvPolicy         *codexexec.EnvPolicy
	UseLegacyLandlock bool
}

func resolveSandboxPermissionProfileForRun(opts *cli.SandboxOptions) (*config.SandboxPermissionProfileResolution, error) {
	runConfig, err := loadSandboxRunConfigForRun(opts)
	if err != nil {
		return nil, err
	}
	return runConfig.PermissionProfile, nil
}

func loadSandboxRunConfigForRun(opts *cli.SandboxOptions) (*sandboxRunConfig, error) {
	return loadSandboxRunConfigForRunContext(context.Background(), opts)
}

func loadSandboxRunConfigForRunContext(ctx context.Context, opts *cli.SandboxOptions) (*sandboxRunConfig, error) {
	if opts == nil {
		opts = &cli.SandboxOptions{}
	}
	codexHome := auth.DefaultCodexHome()
	cloudConfigBundle, err := sandboxCloudConfigBundleForRun(ctx, codexHome, opts)
	if err != nil {
		return nil, err
	}
	managedConfigPath := ""
	if opts.IncludeManagedConfig {
		managedConfigPath = filepath.Join(codexHome, "managed_config.toml")
	}
	loaded, err := config.LoadEffectiveWithOptions(codexHome, &config.EffectiveOptions{
		Profile:              opts.ConfigProfile,
		CWD:                  opts.CWD,
		RawOverrides:         append([]string(nil), opts.ConfigOverrides...),
		IncludeManagedConfig: opts.IncludeManagedConfig,
		ManagedConfigPath:    managedConfigPath,
		CloudConfigBundle:    cloudConfigBundle,
	})
	if err != nil {
		return nil, err
	}
	usesLegacySandboxModeOverride, err := sandboxConfigOverridesUseLegacySandboxMode(opts.ConfigOverrides)
	if err != nil {
		return nil, err
	}
	if !sandboxConfigUsesPermissionProfiles(loaded) && !usesLegacySandboxModeOverride {
		if loaded.Values == nil {
			loaded.Values = map[string]any{}
		}
		loaded.Values["sandbox_mode"] = "read-only"
	}
	resolved, err := loaded.ResolveSandboxPermissionProfile(opts.PermissionProfile, opts.CWD)
	if err != nil {
		return nil, err
	}
	return &sandboxRunConfig{
		PermissionProfile: resolved,
		EnvPolicy:         sandboxEnvPolicyFromConfig(loaded, opts.CWD),
		UseLegacyLandlock: loaded.FeatureSettings()["use_legacy_landlock"],
	}, nil
}

func sandboxCloudConfigBundleForRun(ctx context.Context, codexHome string, opts *cli.SandboxOptions) (*config.CloudConfigLoader, error) {
	if opts == nil || strings.TrimSpace(opts.PermissionProfile) == "" || !opts.IncludeManagedConfig {
		return nil, nil
	}
	bootstrap, err := config.LoadEffectiveWithOptions(codexHome, &config.EffectiveOptions{
		Profile:              opts.ConfigProfile,
		CWD:                  opts.CWD,
		RawOverrides:         append([]string(nil), opts.ConfigOverrides...),
		IncludeManagedConfig: opts.IncludeManagedConfig,
		ManagedConfigPath:    filepath.Join(codexHome, "managed_config.toml"),
	})
	if err != nil {
		return nil, err
	}
	store := auth.NewStoreWithOptions(codexHome, authStoreOptionsFromLoadedConfig(bootstrap))
	snapshot, err := store.Load()
	if err != nil {
		return nil, err
	}
	if !sandboxCloudConfigEligibleAuth(snapshot) {
		return nil, nil
	}
	authHeaders, err := model.AuthHeadersFromAuth(*snapshot)
	if err != nil {
		return nil, err
	}
	return config.NewCloudConfigLoader(func() (*config.CloudConfigBundle, error) {
		loadCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		return config.LoadCloudConfigBundle(loadCtx, config.CloudConfigFetchOptions{
			CodexHome:     codexHome,
			BaseURL:       bootstrap.ChatGPTBaseURL(),
			ChatGPTUserID: auth.ChatGPTUserIDFromAuth(snapshot),
			AccountID:     auth.AccountIDFromAuthForRestrictions(snapshot),
			HTTPClient:    codexnetwork.NewHTTPClient(bootstrap.RespectSystemProxyEnabled(), 0),
			Authorize: func(requestCtx context.Context, request *http.Request) error {
				return authHeaders.Apply(requestCtx, request, nil)
			},
		})
	}), nil
}

func sandboxCloudConfigEligibleAuth(snapshot *auth.AuthDotJSON) bool {
	if snapshot == nil {
		return false
	}
	switch snapshot.Mode() {
	case "chatgpt", "chatgptAuthTokens", "personal-access-token", "agent-identity":
	default:
		return false
	}
	account := auth.AccountFromAuth(snapshot)
	if account == nil || account.Type != auth.AccountChatGPT {
		return false
	}
	return account.PlanType.IsBusinessLike() ||
		account.PlanType.IsEducationLike() ||
		account.PlanType == auth.PlanEnterprise
}

func sandboxEnvPolicyFromConfig(cfg *config.Config, cwd string) *codexexec.EnvPolicy {
	if cfg == nil || cfg.Values == nil {
		return codexexec.EnvPolicyFromShellEnvironmentPolicy(nil, cwd)
	}
	table, _ := cfg.Values["shell_environment_policy"].(map[string]any)
	return codexexec.EnvPolicyFromShellEnvironmentPolicy(table, cwd)
}

func sandboxConfigUsesPermissionProfiles(cfg *config.Config) bool {
	if cfg == nil || cfg.Values == nil {
		return false
	}
	_, ok := cfg.Values["default_permissions"]
	return ok
}

func sandboxConfigOverridesUseLegacySandboxMode(raw []string) (bool, error) {
	overrides, err := config.ParseOverrides(raw)
	if err != nil {
		return false, err
	}
	for _, override := range overrides {
		if override.Path == "sandbox_mode" {
			return true, nil
		}
	}
	return false, nil
}

func environmentMapFromEnviron(environ []string) map[string]string {
	env := make(map[string]string, len(environ))
	for _, entry := range environ {
		name, value, ok := strings.Cut(entry, "=")
		if !ok || name == "" {
			continue
		}
		env[name] = value
	}
	return env
}

func envSlice(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for key, value := range env {
		out = append(out, key+"="+value)
	}
	return out
}

func cloneStringMapLocal(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func runExecpolicy(opts *cli.ExecpolicyOptions, stdout io.Writer) error {
	switch opts.Action {
	case "check":
		output, err := execpolicy.Check(&execpolicy.CheckOptions{
			Rules:                  append([]string(nil), opts.Rules...),
			Command:                append([]string(nil), opts.Command...),
			Pretty:                 opts.Pretty,
			ResolveHostExecutables: opts.ResolveHostExecutables,
		})
		if err != nil {
			return err
		}
		rendered, err := execpolicy.Render(output, opts.Pretty)
		if err != nil {
			return err
		}
		_, err = io.WriteString(stdout, rendered+"\n")
		return err
	default:
		return fmt.Errorf("unknown execpolicy subcommand %s", opts.Action)
	}
}

func runMCPServer(ctx context.Context, opts *cli.MCPServerOptions, root *cli.RootOptions, stdin io.Reader, stdout io.Writer) error {
	if opts == nil {
		opts = &cli.MCPServerOptions{}
	}
	// Rust #39657: the standalone MCP server is deprecated; keep serving after
	// the warning.
	fmt.Fprintln(os.Stderr, "warning: `codex mcp-server` is deprecated and will be removed in a future release.")
	codexHome := auth.DefaultCodexHome()
	if auth.IsWorkloadIdentitySelected() {
		return errors.New("workload identity is not supported by `codex mcp-server`")
	}
	if _, err := config.LoadEffectiveWithOptions(codexHome, mcpServerConfigLoadOptions(opts, root)); err != nil {
		return err
	}
	return mcp.ServeStdio(ctx, &mcp.StdioServerOptions{
		Runner: newCodexMCPRunner(codexHome, mcpServerRootOptions(root)),
	}, stdin, stdout)
}

func mcpServerConfigLoadOptions(opts *cli.MCPServerOptions, root *cli.RootOptions) *config.EffectiveOptions {
	strictConfig := opts != nil && opts.StrictConfig
	loadOpts := &config.EffectiveOptions{StrictConfig: strictConfig}
	if root != nil {
		loadOpts.RawOverrides = append(loadOpts.RawOverrides, root.ConfigOverrides...)
		loadOpts.EnableFeatures = append(loadOpts.EnableFeatures, root.EnableFeatures...)
		loadOpts.DisableFeatures = append(loadOpts.DisableFeatures, root.DisableFeatures...)
		loadOpts.StrictConfig = loadOpts.StrictConfig || root.StrictConfig
	}
	return loadOpts
}

func mcpServerRootOptions(root *cli.RootOptions) cli.RootOptions {
	if root == nil {
		return cli.RootOptions{}
	}
	out := *root
	out.ConfigOverrides = append([]string(nil), root.ConfigOverrides...)
	out.EnableFeatures = append([]string(nil), root.EnableFeatures...)
	out.DisableFeatures = append([]string(nil), root.DisableFeatures...)
	out.Shared.Images = append([]string(nil), root.Shared.Images...)
	out.Shared.AddDirs = append([]string(nil), root.Shared.AddDirs...)
	return out
}

func runStdioToUDS(opts *cli.StdioToUDSOptions, stdin io.Reader, stdout io.Writer) error {
	if opts.SocketPath == "" {
		return errors.New("stdio-to-uds requires SOCKET_PATH")
	}
	return bridgeStdioToUDS(opts.SocketPath, stdin, stdout)
}

func isWindowsNamedPipePath(path string) bool {
	path = strings.TrimSpace(path)
	return strings.HasPrefix(strings.ToLower(path), `\\.\pipe\`)
}

func runExecServer(ctx context.Context, opts *cli.ExecServerOptions, root *cli.RootOptions, stdin io.Reader, stdout io.Writer) error {
	strictConfig := opts.StrictConfig
	var rootConfigOverrides []string
	if root != nil {
		strictConfig = strictConfig || root.StrictConfig
		rootConfigOverrides = append(rootConfigOverrides, root.ConfigOverrides...)
	}
	if strings.TrimSpace(opts.Remote) != "" {
		return runExecServerRemote(ctx, opts, rootConfigOverrides, strictConfig, stdin)
	}
	if forward := strings.TrimSpace(opts.Forward); forward != "" && forward != "forward" {
		// Rust #39249: register an existing WebSocket exec-server as a
		// remote environment and forward complete payloads unchanged.
		environmentID := strings.TrimSpace(opts.EnvironmentID)
		if environmentID == "" {
			environmentID = "forward"
		}
		codexHome := auth.DefaultCodexHome()
		loadedConfig, err := config.LoadEffectiveWithOptions(codexHome, &config.EffectiveOptions{
			RawOverrides: rootConfigOverrides,
			StrictConfig: strictConfig,
		})
		if err != nil {
			return err
		}
		return execserver.ForwardServer(ctx, &execserver.ForwardOptions{
			ConnectURL:    forward,
			EnvironmentID: environmentID,
			Name:          strings.TrimSpace(opts.Name),
			HTTPClient:    codexnetwork.NewHTTPClient(loadedConfig.RespectSystemProxyEnabled(), 0),
		})
	}
	loadedConfig, err := config.LoadEffectiveWithOptions(auth.DefaultCodexHome(), &config.EffectiveOptions{
		RawOverrides: rootConfigOverrides,
		StrictConfig: strictConfig,
	})
	if err != nil {
		return err
	}
	listenURL := opts.Listen
	if !opts.ListenSet {
		listenURL = execserver.DefaultListenURL
	}
	httpClient := codexnetwork.NewHTTPClient(loadedConfig.RespectSystemProxyEnabled(), 0)
	return execserver.NewServerWithHTTPClient(httpClient).ServeTransport(ctx, listenURL, stdin, stdout)
}

func runExecServerRemote(ctx context.Context, opts *cli.ExecServerOptions, rootConfigOverrides []string, strictConfig bool, stdin io.Reader) error {
	baseURL := strings.TrimRight(strings.TrimSpace(opts.Remote), "/")
	if baseURL == "" {
		return errors.New("environment registry base URL is required")
	}
	environmentID := strings.TrimSpace(opts.EnvironmentID)
	if environmentID == "" {
		return errors.New("environment id is required for remote exec-server registration")
	}
	codexHome := auth.DefaultCodexHome()
	loadedConfig, err := config.LoadEffectiveWithOptions(codexHome, &config.EffectiveOptions{
		RawOverrides: rootConfigOverrides,
		StrictConfig: strictConfig,
	})
	if err != nil {
		return err
	}
	if opts.UseAgentIdentityAuth {
		accessToken := strings.TrimSpace(os.Getenv(auth.CodexAccessTokenEnv))
		if accessToken == "" {
			return errors.New("CODEX_ACCESS_TOKEN is required when --use-agent-identity-auth is set")
		}
		headers := http.Header{}
		headers.Set("Authorization", "Bearer "+accessToken)
		return runExecServerRemoteWithParentLifetime(ctx, stdin, opts.ExitOnStdinClose, execserver.RemoteEnvironmentConfig{
			BaseURL:       baseURL,
			EnvironmentID: environmentID,
			Name:          strings.TrimSpace(opts.Name),
			AuthHeaders:   headers,
			HTTPClient:    codexnetwork.NewHTTPClient(loadedConfig.RespectSystemProxyEnabled(), 0),
		})
	}
	storeOptions := authStoreOptionsFromLoadedConfig(loadedConfig)
	resolved, err := auth.NewStoreWithOptions(codexHome, storeOptions).Resolve()
	if err != nil {
		return err
	}
	if resolved == nil {
		return errors.New("remote exec-server registration requires ChatGPT authentication or API key authentication; run `codex login` or set CODEX_API_KEY")
	}
	mode := resolved.Auth.BackendMode()
	if mode != "chatgpt" && mode != "api-key" {
		return errors.New("remote exec-server registration requires ChatGPT authentication or API key authentication; Agent Identity auth requires --use-agent-identity-auth")
	}
	if mode == "api-key" {
		if err := validateExecServerAPIKeyRemoteHost(baseURL); err != nil {
			return err
		}
	}
	headers, err := execServerRemoteAuthHeaders(&resolved.Auth)
	if err != nil {
		return err
	}
	var resolveHeaders func(context.Context) (http.Header, error)
	if resolved.Source == auth.WorkloadIdentitySource {
		// Rust #38610: remote exec-server registry requests resolve managed
		// credentials asynchronously so workload identity tokens stay fresh
		// across re-registrations instead of using a one-time static header.
		store := auth.NewStoreWithOptions(codexHome, storeOptions)
		resolveHeaders = func(ctx context.Context) (http.Header, error) {
			fresh, err := store.Resolve()
			if err != nil {
				return nil, err
			}
			return execServerRemoteAuthHeaders(&fresh.Auth)
		}
	}
	return runExecServerRemoteWithParentLifetime(ctx, stdin, opts.ExitOnStdinClose, execserver.RemoteEnvironmentConfig{
		BaseURL:            baseURL,
		EnvironmentID:      environmentID,
		Name:               strings.TrimSpace(opts.Name),
		AuthHeaders:        headers,
		ResolveAuthHeaders: resolveHeaders,
		HTTPClient:         codexnetwork.NewHTTPClient(loadedConfig.RespectSystemProxyEnabled(), 0),
	})
}

func runExecServerRemoteWithParentLifetime(ctx context.Context, stdin io.Reader, exitOnStdinClose bool, cfg execserver.RemoteEnvironmentConfig) error {
	if !exitOnStdinClose {
		return execserver.RunRemoteEnvironment(ctx, cfg)
	}
	runCtx, cancel := execServerRemoteParentContext(ctx, stdin)
	defer cancel()
	return execserver.RunRemoteEnvironment(runCtx, cfg)
}

func execServerRemoteParentContext(ctx context.Context, stdin io.Reader) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithCancel(ctx)
	go func() {
		if stdin != nil {
			_, _ = io.Copy(io.Discard, stdin)
		}
		cancel()
	}()
	return runCtx, cancel
}

func execServerRemoteAuthHeaders(snapshot *auth.AuthDotJSON) (http.Header, error) {
	headers := http.Header{}
	if snapshot == nil {
		return headers, errors.New("remote exec-server registration requires ChatGPT authentication or API key authentication; run `codex login` or set CODEX_API_KEY")
	}
	switch snapshot.BackendMode() {
	case "api-key":
		apiKey := strings.TrimSpace(snapshot.OpenAIAPIKey)
		if apiKey == "" {
			return headers, errors.New("remote exec-server registration requires ChatGPT authentication or API key authentication; run `codex login` or set CODEX_API_KEY")
		}
		headers.Set("Authorization", "Bearer "+apiKey)
	case "chatgpt":
		accessToken := remoteAuthTokenString(snapshot, "access_token")
		if accessToken == "" {
			return headers, errors.New("remote exec-server registration requires ChatGPT authentication or API key authentication; run `codex login` or set CODEX_API_KEY")
		}
		headers.Set("Authorization", "Bearer "+accessToken)
		if accountID := auth.AccountIDFromAuthForRestrictions(snapshot); accountID != "" {
			headers.Set("ChatGPT-Account-ID", accountID)
		}
	default:
		return headers, errors.New("remote exec-server registration requires ChatGPT authentication or API key authentication; Agent Identity auth requires --use-agent-identity-auth")
	}
	return headers, nil
}

func validateExecServerAPIKeyRemoteHost(baseURL string) error {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("invalid remote exec-server registration URL: %w", err)
	}
	host := parsed.Hostname()
	if strings.TrimSpace(host) == "" {
		return errors.New("remote exec-server registration URL must include a host")
	}
	isLoopback := strings.EqualFold(host, "localhost")
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		isLoopback = true
	}
	lowerHost := strings.ToLower(host)
	isOpenAIHost := lowerHost == "openai.com" || lowerHost == "openai.org" ||
		strings.HasSuffix(lowerHost, ".openai.com") || strings.HasSuffix(lowerHost, ".openai.org")
	isAllowed := false
	switch parsed.Scheme {
	case "https":
		isAllowed = isLoopback || isOpenAIHost
	case "http":
		isAllowed = isLoopback
	}
	if !isAllowed {
		return errors.New("remote exec-server API-key authentication is restricted to HTTPS openai.com and openai.org hosts and subdomains or loopback hosts")
	}
	return nil
}

func writeFileCreatingParent(path string, data []byte) error {
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	return os.WriteFile(path, data, 0o600)
}

func runApply(opts cli.ApplyOptions, root cli.RootOptions, stdin io.Reader, stdout io.Writer) error {
	patch := opts.Patch
	if strings.TrimSpace(patch) == "" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return err
		}
		patch = string(data)
	}
	if strings.TrimSpace(patch) == "" {
		return errors.New("apply requires a patch argument or patch text on stdin")
	}
	cwd := root.Shared.CWD
	if cwd == "" {
		cwd = "."
	}
	result, err := applypatch.Apply(patch, &applypatch.ApplyOptions{CWD: cwd})
	if err != nil {
		return err
	}
	_, err = io.WriteString(stdout, result.Summary())
	return err
}

func runMigrateRollouts(opts cli.MigrateRolloutsOptions, root cli.RootOptions, stdout io.Writer, stderr io.Writer) error {
	codexHome := auth.DefaultCodexHome()
	report, err := rollout.MigrateRollouts(codexHome, rollout.MigrationOptions{
		Apply:           opts.Apply,
		ThreadIDs:       opts.Threads,
		MaxMibPerSecond: opts.MaxMibPerSecond,
	})
	if err != nil {
		return err
	}
	if opts.Apply {
		// Mirrors Rust migrate_one_rollout: after publishing the paginated
		// rollout, materialize its SQLite thread-history projection so the
		// app-server reads it from the durable projection rather than
		// re-projecting on demand. The rollout package stays storage-only;
		// the projection lives here to avoid an import cycle.
		if err := projectMigratedRollouts(codexHome, report); err != nil {
			return err
		}
	}
	if opts.JSON {
		data, marshalErr := rollout.RenderJSONReport(report)
		if marshalErr != nil {
			return marshalErr
		}
		_, err = stdout.Write(append(data, '\n'))
		if err != nil {
			return err
		}
	} else {
		printMigrateRolloutsHumanReport(report, opts, stdout)
	}
	for _, outcome := range report.Outcomes {
		if outcome.Status == rollout.MigrationStatusFailed {
			return &ExitError{Code: 1, Message: "one or more rollout migrations failed", Silent: true}
		}
	}
	return nil
}

func printMigrateRolloutsHumanReport(report *rollout.MigrationReport, opts cli.MigrateRolloutsOptions, stdout io.Writer) {
	counts := map[rollout.MigrationStatus]int{}
	for _, outcome := range report.Outcomes {
		counts[outcome.Status]++
	}
	completion := "Scan complete"
	if opts.Apply {
		completion = "Migration complete"
	}
	fmt.Fprintf(stdout, "%s.\n", completion)
	migrated := "eligible"
	if opts.Apply {
		migrated = "migrated"
	}
	fmt.Fprintf(stdout, "Scanned %d rollout(s): %d %s, %d already paginated, %d skipped (%d empty, %d busy), %d failed.\n",
		len(report.Outcomes),
		counts[rollout.MigrationStatusEligible]+counts[rollout.MigrationStatusMigrated],
		migrated,
		counts[rollout.MigrationStatusAlreadyPaginated],
		counts[rollout.MigrationStatusSkippedEmpty]+counts[rollout.MigrationStatusSkippedBusy],
		counts[rollout.MigrationStatusSkippedEmpty],
		counts[rollout.MigrationStatusSkippedBusy],
		counts[rollout.MigrationStatusFailed],
	)
	if !opts.Apply && counts[rollout.MigrationStatusEligible] > 0 {
		fmt.Fprintln(stdout, "Run `codex migrate-rollouts --apply` to migrate eligible sessions.")
	}
	if !opts.Verbose {
		return
	}
	for _, outcome := range report.Outcomes {
		threadID := "unknown"
		if outcome.ThreadID != nil && strings.TrimSpace(*outcome.ThreadID) != "" {
			threadID = strings.TrimSpace(*outcome.ThreadID)
		}
		if outcome.Message != nil {
			fmt.Fprintf(stdout, "%s\t%s\t%s\n", rollout.FormatOutcomeStatus(outcome.Status), threadID, *outcome.Message)
		} else {
			fmt.Fprintf(stdout, "%s\t%s\n", rollout.FormatOutcomeStatus(outcome.Status), threadID)
		}
	}
}

func runAppServer(ctx context.Context, opts cli.AppServerOptions, root *cli.RootOptions, stdout io.Writer, stderr io.Writer, stdin io.Reader) error {
	if len(opts.Subcommand) > 0 {
		switch opts.Subcommand[0] {
		case "daemon":
			return runAppServerDaemon(ctx, opts.Daemon, stdout)
		case "proxy":
			socketPath := opts.Proxy.SocketPath
			if socketPath == "" {
				socketPath = appserver.AppServerControlSocketPath(auth.DefaultCodexHome())
			}
			return runStdioToUDS(&cli.StdioToUDSOptions{SocketPath: socketPath}, stdin, stdout)
		case "generate-ts":
			if err := appserver.GenerateTypeScript(&appserver.SchemaGenerateOptions{
				OutDir:       opts.Generate.OutDir,
				Prettier:     opts.Generate.Prettier,
				Experimental: opts.Generate.Experimental,
			}); err != nil {
				return err
			}
			fmt.Fprintf(stdout, "Wrote app-server TypeScript protocol bindings to %s\n", opts.Generate.OutDir)
			return nil
		case "generate-json-schema":
			if err := appserver.GenerateJSONSchema(&appserver.SchemaGenerateOptions{
				OutDir:       opts.Generate.OutDir,
				Experimental: opts.Generate.Experimental,
			}); err != nil {
				return err
			}
			fmt.Fprintf(stdout, "Wrote app-server JSON Schema bundle to %s\n", opts.Generate.OutDir)
			return nil
		case "generate-internal-json-schema":
			if err := appserver.GenerateJSONSchema(&appserver.SchemaGenerateOptions{
				OutDir:   opts.Generate.OutDir,
				Internal: true,
			}); err != nil {
				return err
			}
			fmt.Fprintf(stdout, "Wrote internal app-server JSON Schema artifacts to %s\n", opts.Generate.OutDir)
			return nil
		default:
			return fmt.Errorf("unknown app-server subcommand %s", opts.Subcommand[0])
		}
	}
	serverCtx, cancelServer := context.WithCancel(ctx)
	defer cancelServer()
	// Detached Windows pid-managed app servers receive graceful shutdown
	// through CODEX_DAEMON_SHUTDOWN_FILE (Rust #42364); the watcher is a no-op
	// unless that env var is set.
	go appserverdaemon.WatchDaemonShutdownRequest(serverCtx, cancelServer)
	codexHome := auth.DefaultCodexHome()
	loadedConfig, err := loadAppServerConfig(codexHome, opts, root)
	if err != nil {
		return err
	}
	requirements, err := appServerRequirements(codexHome, loadedConfig)
	if err != nil {
		return err
	}
	remoteControlDisabledByRequirements := appServerRemoteControlDisabledByRequirements(requirements)
	runtimeOptions, err := appServerRuntimeOptionsFromCLI(opts, loadedConfig)
	if err != nil {
		return err
	}
	runtimeOptions.FeatureEnablement = featureEnablementFromRoot(root)
	runtimeOptions.Requirements = requirements
	runtimeOptions.EnableLogDB = true
	runtimeOptions.RemoteControlDisabledByRequirements = remoteControlDisabledByRequirements
	runtimeOptions.RemoteControlBackendEnabled = true
	runtimeOptions.RemoteControlURL = loadedConfig.ChatGPTBaseURL()
	if installationID, err := install.ResolveInstallationID(codexHome); err == nil {
		runtimeOptions.RemoteControlInstallationID = installationID
	}
	if runtimeOptions.RemoteControlStartupMode == appserver.RemoteControlStartupEnabledEphemeral && remoteControlDisabledByRequirements {
		return errors.New("remote control is disabled by managed requirements")
	}
	listen := strings.TrimSpace(opts.Listen)
	if listen == "" || listen == "stdio://" || opts.Stdio {
		server := appserver.NewDefaultStdioServer(&appserver.StdioOptions{
			CodexHome:      codexHome,
			RuntimeOptions: runtimeOptions,
		})
		return server.Serve(stdin, stdout)
	}
	if strings.HasPrefix(listen, "unix://") {
		return appserver.ServeUnixSocket(serverCtx, &appserver.UnixSocketOptions{
			CodexHome:      codexHome,
			Listen:         listen,
			RuntimeOptions: runtimeOptions,
		})
	}
	if strings.HasPrefix(listen, "ws://") {
		return appserver.ServeWebSocket(serverCtx, &appserver.WebSocketOptions{
			CodexHome:      codexHome,
			Listen:         listen,
			Auth:           webSocketAuthSettingsFromCLI(opts),
			Ready:          stderr,
			RuntimeOptions: runtimeOptions,
		})
	}
	if listen == "off" {
		if remoteControlDisabledByRequirements {
			return errors.New("no transport configured; remote control disabled by managed requirements")
		}
		if runtimeOptions.RemoteControlStartupMode == appserver.RemoteControlStartupEnabledEphemeral {
			if appServerStateDBAvailable(codexHome) {
				return runAppServerRemoteControlOnly(serverCtx, codexHome, runtimeOptions)
			}
			return errors.New("no transport configured; remote control disabled because sqlite state db is unavailable")
		}
		if runtimeOptions.RemoteControlStartupMode == appserver.RemoteControlStartupResolvePersisted {
			enabled, err := appServerPersistedRemoteControlEnabled(serverCtx, codexHome, loadedConfig)
			if err == nil && enabled {
				return runAppServerRemoteControlOnly(serverCtx, codexHome, runtimeOptions)
			}
		}
		return errors.New("no transport configured; use --listen or enable remote control")
	}
	return fmt.Errorf("unsupported app-server listen address %s", opts.Listen)
}

func loadAppServerConfig(codexHome string, opts cli.AppServerOptions, root *cli.RootOptions) (*config.Config, error) {
	strictConfig := opts.StrictConfig
	loadOpts := &config.EffectiveOptions{StrictConfig: strictConfig}
	if root != nil {
		loadOpts.RawOverrides = append(loadOpts.RawOverrides, root.ConfigOverrides...)
		loadOpts.EnableFeatures = append(loadOpts.EnableFeatures, root.EnableFeatures...)
		loadOpts.DisableFeatures = append(loadOpts.DisableFeatures, root.DisableFeatures...)
		loadOpts.Profile = root.Shared.Profile
		loadOpts.CWD = root.Shared.CWD
		loadOpts.StrictConfig = loadOpts.StrictConfig || root.StrictConfig
	}
	return config.LoadEffectiveWithOptions(codexHome, loadOpts)
}

func appServerRequirements(codexHome string, loadedConfig *config.Config) (*config.ConfigRequirements, error) {
	var requirements *config.ConfigRequirements
	if allow, ok, err := appServerAllowRemoteControlFromEffectiveConfig(loadedConfig); err != nil {
		return nil, err
	} else if ok {
		requirements = &config.ConfigRequirements{AllowRemoteControl: &allow}
	}
	managed, err := appServerRequirementsFromFile(filepath.Join(codexHome, "requirements.toml"))
	if err != nil {
		return nil, err
	}
	if managed != nil {
		if requirements != nil && managed.AllowRemoteControl == nil && requirements.AllowRemoteControl != nil {
			allow := *requirements.AllowRemoteControl
			managed.AllowRemoteControl = &allow
		}
		requirements = managed
	}
	return requirements, nil
}

func appServerRequirementsFromFile(path string) (*config.ConfigRequirements, error) {
	return config.LoadRequirementsFile(path)
}

func appServerAllowRemoteControlFromEffectiveConfig(loadedConfig *config.Config) (bool, bool, error) {
	if loadedConfig == nil || loadedConfig.Values == nil {
		return false, false, nil
	}
	requirements, ok := loadedConfig.Values["requirements"].(map[string]any)
	if !ok {
		return false, false, nil
	}
	return appServerAllowRemoteControlFromMap(requirements, false)
}

func appServerAllowRemoteControlFromMap(values map[string]any, topLevel bool) (bool, bool, error) {
	if values == nil {
		return false, false, nil
	}
	keys := []string{"allow_remote_control", "allowRemoteControl"}
	if !topLevel {
		keys = []string{"allow_remote_control", "allowRemoteControl"}
	}
	for _, key := range keys {
		value, ok := values[key]
		if !ok {
			continue
		}
		allow, ok := value.(bool)
		if !ok {
			return false, false, fmt.Errorf("requirements.allow_remote_control must be a boolean")
		}
		return allow, true, nil
	}
	return false, false, nil
}

func appServerRemoteControlDisabledByRequirements(requirements *config.ConfigRequirements) bool {
	return requirements != nil && requirements.AllowRemoteControl != nil && !*requirements.AllowRemoteControl
}

func appServerRuntimeOptionsFromCLI(opts cli.AppServerOptions, loadedConfig *config.Config) (*appserver.RuntimeRouterOptions, error) {
	disabledByEnv := appserver.TakeRemoteControlDisabledEnv()
	mode := appserver.RemoteControlStartupResolvePersisted
	switch {
	case opts.RemoteControl:
		mode = appserver.RemoteControlStartupEnabledEphemeral
	case disabledByEnv:
		mode = appserver.RemoteControlStartupDisabledEphemeral
	}
	options := &appserver.RuntimeRouterOptions{
		RemoteControlStartupMode: mode,
		AnalyticsDefaultEnabled:  opts.AnalyticsDefaultEnabled,
	}
	if loadedConfig != nil {
		options.CodeModeHostEnabled = features.Enabled(loadedConfig.FeatureSettings(), "code_mode_host")
	}
	hostURL := strings.TrimSpace(opts.CodeModeHostURL)
	if hostURL != "" {
		if loadedConfig == nil || !features.Enabled(loadedConfig.FeatureSettings(), "code_mode_host") {
			return nil, errors.New("remote code-mode host requires the code_mode_host feature to be enabled")
		}
		options.CodeModeHostURL = hostURL
		options.CodeModeHostHTTPClient = codexnetwork.NewHTTPClient(loadedConfig.RespectSystemProxyEnabled(), 0)
		// Rust's explicitly selected WebSocket provider never falls back to the local host.
		options.DisableCodeModeInProcessFallback = true
	} else if loadedConfig != nil {
		options.DisableCodeModeInProcessFallback = loadedConfig.DisableCodeModeInProcessFallback()
	}
	return options, nil
}

func webSocketAuthSettingsFromCLI(opts cli.AppServerOptions) *appserver.WebSocketAuthSettings {
	return &appserver.WebSocketAuthSettings{
		Mode:                appserver.WebSocketAuthMode(strings.TrimSpace(opts.WSAuth)),
		TokenFile:           opts.WSTokenFile,
		TokenSHA256:         opts.WSTokenSHA256,
		SharedSecretFile:    opts.WSSharedSecretFile,
		Issuer:              opts.WSIssuer,
		Audience:            opts.WSAudience,
		MaxClockSkewSeconds: uint64Value(opts.WSMaxClockSkewSeconds),
	}
}

func uint64Value(value *uint64) uint64 {
	if value == nil {
		return 0
	}
	return *value
}

func runAppServerDaemon(ctx context.Context, opts cli.AppServerDaemonOptions, stdout io.Writer) error {
	runner := appserverdaemon.NewLifecycleRunnerForCodexHome(auth.DefaultCodexHome(), "")
	var output any
	var err error
	switch opts.Action {
	case "bootstrap":
		output, err = runner.Bootstrap(&appserverdaemon.BootstrapOptions{RemoteControlEnabled: opts.RemoteControl})
	case "start":
		output, err = runner.Run(appserverdaemon.LifecycleStart)
	case "restart":
		output, err = runner.Run(appserverdaemon.LifecycleRestart)
	case "enable-remote-control":
		output, err = runner.SetRemoteControl(appserverdaemon.RemoteControlEnabled)
	case "disable-remote-control":
		output, err = runner.SetRemoteControl(appserverdaemon.RemoteControlDisabled)
	case "stop":
		output, err = runner.Run(appserverdaemon.LifecycleStop)
	case "version":
		output, err = runner.Run(appserverdaemon.LifecycleVersion)
	case "pid-update-loop":
		return appserverdaemon.RunPIDUpdateLoop(ctx, runner, nil)
	default:
		return fmt.Errorf("unknown app-server daemon subcommand %s", opts.Action)
	}
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	return encoder.Encode(output)
}

func runDesktopApp(opts *cli.AppOptions, stdout io.Writer) error {
	workspace := "."
	if opts != nil && strings.TrimSpace(opts.Path) != "" {
		workspace = opts.Path
	}
	abs, err := filepath.Abs(workspace)
	if err == nil {
		workspace = abs
	}
	payload := map[string]any{
		"workspace": workspace,
		"status":    "ready",
	}
	if opts != nil && strings.TrimSpace(opts.DownloadURL) != "" {
		payload["downloadUrl"] = opts.DownloadURL
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(payload)
}

func runUpdate(ctx context.Context, opts *cli.UpdateOptions, stdout, stderr io.Writer) error {
	context := install.Current()
	if opts != nil && opts.JSON {
		payload := install.OfflineUpdateStatus(context, doctor.Version())
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(payload)
	}
	result, err := install.RunUpdate(ctx, &install.RunUpdateOptions{
		Context: context,
		Stdout:  stdout,
		Stderr:  stderr,
	})
	if err != nil {
		return err
	}
	if result != nil && result.Message != "" {
		fmt.Fprintln(stdout, result.Message)
	}
	return nil
}

func runDebug(opts cli.DebugOptions, root *cli.RootOptions, stdout io.Writer) error {
	switch opts.Subcommand {
	case "models":
		_ = opts.BundledModels
		encoder := json.NewEncoder(stdout)
		return encoder.Encode(model.BundledModelsResponse())
	case "app-server":
		return runDebugAppServer(&opts, stdout)
	case "prompt-input":
		codexHome := auth.DefaultCodexHome()
		cwd := ""
		var rawOverrides, enableFeatures, disableFeatures []string
		images := append([]string(nil), opts.Images...)
		if root != nil {
			cwd = strings.TrimSpace(root.Shared.CWD)
			rawOverrides = append(rawOverrides, root.ConfigOverrides...)
			enableFeatures = append(enableFeatures, root.EnableFeatures...)
			disableFeatures = append(disableFeatures, root.DisableFeatures...)
			images = append(append([]string(nil), root.Shared.Images...), images...)
		}
		if cwd == "" {
			cwd, _ = os.Getwd()
		}
		cfg, err := config.LoadEffective(codexHome, rawOverrides, enableFeatures, disableFeatures, cwd)
		if err != nil {
			return err
		}
		skills, err := appserver.BuildHostSkillsPromptContext(&appserver.HostSkillsPromptOptions{
			CodexHome: codexHome,
			CWD:       cwd,
			Config:    cfg,
			Prompt:    opts.Prompt,
		})
		if err != nil {
			return err
		}
		items := make([]any, 0, len(skills.InputItems)+2)
		if strings.TrimSpace(skills.Instructions) != "" {
			items = append(items, debugPromptMessage("developer", []map[string]any{{"type": "input_text", "text": skills.Instructions}}))
		}
		items = append(items, skills.InputItems...)
		content := make([]map[string]any, 0, len(images)+1)
		for _, image := range images {
			if image = strings.TrimSpace(image); image != "" {
				content = append(content, map[string]any{"type": "input_image", "image_url": image})
			}
		}
		if opts.Prompt != "" {
			content = append(content, map[string]any{"type": "input_text", "text": opts.Prompt})
		}
		if len(content) > 0 {
			items = append(items, debugPromptMessage("user", content))
		}
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(items)
	case "trace-reduce":
		output, _, err := rollout.ReduceTraceBundle(&rollout.TraceReduceOptions{
			BundleDir: opts.TraceBundle,
			Output:    opts.TraceOutput,
		})
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, output)
		return nil
	case "clear-memories":
		return runDebugClearMemories(stdout)
	case "config":
		return runDebugConfig(stdout)
	default:
		return fmt.Errorf("unknown debug subcommand %s", opts.Subcommand)
	}
}

func debugPromptMessage(role string, content []map[string]any) map[string]any {
	return map[string]any{
		"type":    "message",
		"role":    role,
		"content": content,
	}
}

func runDebugAppServer(opts *cli.DebugOptions, stdout io.Writer) error {
	switch opts.AppServerAction {
	case "send-message-v2":
		params := map[string]any{
			"prompt": opts.AppServerMessage,
		}
		data, err := json.Marshal(params)
		if err != nil {
			return err
		}
		request := &appserver.Request{
			JSONRPC: "2.0",
			ID:      appserver.IntID(1),
			Method:  appserver.MethodThreadStart,
			Params:  data,
		}
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(request)
	default:
		return fmt.Errorf("unknown debug app-server subcommand %s", opts.AppServerAction)
	}
}

func runDebugClearMemories(stdout io.Writer) error {
	codexHome := auth.DefaultCodexHome()
	memoriesPath := filepath.Join(codexHome, "memories")
	clearedMemoryDir, err := clearDirectoryContents(memoriesPath)
	if err != nil {
		return err
	}
	removedFiles := []string{}
	for _, name := range []string{"memories.sqlite", "memory.sqlite", "state.sqlite"} {
		path := filepath.Join(codexHome, name)
		if err := os.Remove(path); err == nil {
			removedFiles = append(removedFiles, path)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if len(removedFiles) > 0 {
		fmt.Fprintf(stdout, "Cleared memory state from %s.", strings.Join(removedFiles, ", "))
	} else {
		fmt.Fprintf(stdout, "No memories db found under %s.", codexHome)
	}
	if clearedMemoryDir {
		fmt.Fprintf(stdout, " Cleared memory directories under %s.\n", memoriesPath)
	} else {
		fmt.Fprintf(stdout, " No memory directories found under %s.\n", memoriesPath)
	}
	return nil
}

func runDebugConfig(stdout io.Writer) error {
	response, err := config.NewConfigService(auth.DefaultCodexHome()).Read(&config.ConfigReadParams{IncludeLayers: true})
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(response)
}

func clearDirectoryContents(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		target := filepath.Join(path, entry.Name())
		if err := os.RemoveAll(target); err != nil {
			return false, err
		}
	}
	return true, nil
}

func validateFeatureToggles(enable, disable []string) error {
	for _, feature := range enable {
		if err := features.Validate(feature); err != nil {
			return err
		}
	}
	for _, feature := range disable {
		if err := features.Validate(feature); err != nil {
			return err
		}
	}
	return nil
}

func runLogin(ctx context.Context, opts cli.LoginOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	codexHome := auth.DefaultCodexHome()
	loadedConfig, err := config.LoadEffective(codexHome, opts.ConfigOverrides, nil, nil)
	if err != nil {
		return exitMessagef("Error loading configuration: %v", err)
	}
	authStoreOptions := authStoreOptionsFromLoadedConfig(loadedConfig)
	store := auth.NewStoreWithOptions(codexHome, authStoreOptions)
	if opts.WithAPIKey && opts.WithAccessToken {
		return exitMessagef(loginCredentialSourceConflict)
	}
	switch {
	case opts.Action == "status":
		current, err := store.Resolve()
		if err != nil {
			return exitMessagef("Error checking login status: %v", err)
		}
		if current == nil {
			fmt.Fprintln(stdout, "Not logged in")
			return &ExitError{Code: 1, Silent: true}
		}
		switch (&current.Auth).Mode() {
		case "api-key":
			fmt.Fprintf(stdout, "Logged in using an API key - %s\n", auth.SafeFormatLoginKey(current.Auth.OpenAIAPIKey))
		case "personal-access-token":
			fmt.Fprintln(stdout, "Logged in using personal access token")
		case "agent-identity":
			fmt.Fprintln(stdout, "Logged in using access token")
		case "chatgpt":
			fmt.Fprintln(stdout, "Logged in using ChatGPT")
		case "bedrock-api-key":
			fmt.Fprintln(stdout, "Logged in using Amazon Bedrock API key")
		default:
			fmt.Fprintf(stdout, "Logged in using %s\n", (&current.Auth).Mode())
		}
		return nil
	case opts.APIKey != nil:
		return exitMessagef(apiKeyFlagUnsupportedMessage)
	case opts.WithAPIKey:
		// Rust 2994f545a7 (#37132): requirements.toml allowlists plus the
		// forced login method jointly decide whether a login method is usable.
		if !loadedConfig.IsLoginMethodAllowed(config.ForcedLoginMethodAPI) {
			return exitMessagef(apiKeyLoginDisabledMessage)
		}
		secret, err := readLoginSecret(stdin, stderr, apiKeyStdinTerminalMessage, apiKeyStdinReadingMessage, apiKeyStdinEmptyMessage)
		if err != nil {
			return err
		}
		if err := store.Save(auth.FromAPIKey(secret)); err != nil {
			return exitMessagef("Error logging in: %v", err)
		}
		fmt.Fprintln(stdout, auth.LoginFlowSuccessMessage)
		return nil
	case opts.WithAccessToken:
		if !loadedConfig.IsLoginMethodAllowed(config.ForcedLoginMethodChatGPT) {
			return exitMessagef(accessTokenLoginDisabledMessage)
		}
		secret, err := readLoginSecret(stdin, stderr, accessTokenStdinTerminalMessage, accessTokenStdinReadingMessage, accessTokenStdinEmptyMessage)
		if err != nil {
			return err
		}
		snapshot, err := authFromCodexAccessToken(ctx, secret, loadedConfig)
		if err != nil {
			return exitMessagef("Error logging in with access token: %v", err)
		}
		if err := store.Save(snapshot); err != nil {
			return exitMessagef("Error logging in with access token: %v", err)
		}
		fmt.Fprintln(stdout, auth.LoginFlowSuccessMessage)
		return nil
	case opts.DeviceAuth:
		if !loadedConfig.IsLoginMethodAllowed(config.ForcedLoginMethodChatGPT) {
			return exitMessagef(chatGPTLoginDisabledMessage)
		}
		clearExistingAuthBeforeLogin(ctx, codexHome, authStoreOptions)
		if err := auth.RunDeviceCodeLogin(ctx, &auth.OAuthOptions{
			CodexHome:        codexHome,
			Issuer:           opts.IssuerBaseURL,
			ClientID:         opts.ClientID,
			DevicePrompt:     stdout,
			ForcedWorkspaces: loadedConfig.EffectiveChatGPTWorkspaces(),
			StoreOptions:     authStoreOptions,
		}); err != nil {
			return exitMessagef("Error logging in with device code: %v", err)
		}
		fmt.Fprintln(stdout, auth.LoginFlowSuccessMessage)
		return nil
	default:
		if !loadedConfig.IsLoginMethodAllowed(config.ForcedLoginMethodChatGPT) {
			return exitMessagef(chatGPTLoginDisabledMessage)
		}
		clearExistingAuthBeforeLogin(ctx, codexHome, authStoreOptions)
		server, err := auth.StartBrowserLogin(ctx, &auth.OAuthOptions{
			CodexHome:        codexHome,
			Issuer:           opts.IssuerBaseURL,
			ClientID:         opts.ClientID,
			OpenBrowser:      true,
			ForcedWorkspaces: loadedConfig.EffectiveChatGPTWorkspaces(),
			StoreOptions:     authStoreOptions,
		})
		if err != nil {
			return exitMessagef("Error logging in: %v", err)
		}
		fmt.Fprintln(stdout, auth.LoginFlowServerStartMessage(server.Port, server.AuthURL))
		err = <-server.Done
		if err == nil {
			fmt.Fprintln(stdout, auth.LoginFlowSuccessMessage)
		}
		if err != nil {
			return exitMessagef("Error logging in: %v", err)
		}
		return nil
	}
}

func clearExistingAuthBeforeLogin(ctx context.Context, codexHome string, storeOptions *auth.StoreOptions) {
	_, _ = auth.LogoutWithRevoke(ctx, codexHome, storeOptions)
}

func authFromCodexAccessToken(ctx context.Context, accessToken string, cfg *config.Config) (auth.AuthDotJSON, error) {
	token := strings.TrimSpace(accessToken)
	if strings.HasPrefix(token, "at-") {
		metadata, err := auth.LoadPersonalAccessTokenMetadata(ctx, token)
		if err != nil {
			return auth.AuthDotJSON{}, err
		}
		if err := auth.EnsureWorkspaceAccountAllowed(cfg.ForcedChatGPTWorkspaceIDs(), metadata.ChatGPTAccountID); err != nil {
			return auth.AuthDotJSON{}, err
		}
		return auth.FromAccessToken(token), nil
	}
	snapshot := auth.FromCodexAccessToken(token)
	if err := auth.EnsureWorkspaceAccountAllowed(cfg.ForcedChatGPTWorkspaceIDs(), auth.AccountIDFromAuthForRestrictions(&snapshot)); err != nil {
		return auth.AuthDotJSON{}, err
	}
	return snapshot, nil
}

func authStoreOptionsFromLoadedConfig(loaded *config.Config) *auth.StoreOptions {
	options := auth.StoreOptionsFromConfig(loaded.CLIAuthCredentialsStoreMode(), loaded.SecretAuthStorageEnabled())
	options.WorkloadIdentity = &auth.WorkloadIdentityAuthOptions{
		ChatGPTBaseURL: loaded.ChatGPTBaseURL(),
	}
	return options
}

func authStoreOptionsFromConfig(codexHome string, overrides []string) (*auth.StoreOptions, error) {
	loaded, err := config.LoadEffective(codexHome, overrides, nil, nil)
	if err != nil {
		return nil, exitMessagef("Error loading configuration: %v", err)
	}
	return authStoreOptionsFromLoadedConfig(loaded), nil
}

func runLogout(ctx context.Context, opts cli.LoginOptions, stdout io.Writer) error {
	codexHome := auth.DefaultCodexHome()
	authStoreOptions, err := authStoreOptionsFromConfig(codexHome, opts.ConfigOverrides)
	if err != nil {
		return err
	}
	removed, err := auth.LogoutWithRevoke(ctx, codexHome, authStoreOptions)
	if err != nil {
		return exitMessagef("Error logging out: %v", err)
	}
	if removed {
		fmt.Fprintln(stdout, "Successfully logged out")
	} else {
		fmt.Fprintln(stdout, "Not logged in")
	}
	return nil
}

func runFeatures(opts cli.FeatureOptions, root cli.RootOptions, stdout io.Writer, stderr io.Writer) error {
	switch opts.Action {
	case "list":
		values := features.Defaults()
		// Rust #38581: load through the shared cloud-aware loader so managed
		// feature requirements are reflected in the reported state without
		// rewriting the user's config.toml.
		effective, err := config.LoadEffectiveWithOptions(auth.DefaultCodexHome(), &config.EffectiveOptions{
			RawOverrides:         root.ConfigOverrides,
			EnableFeatures:       root.EnableFeatures,
			DisableFeatures:      root.DisableFeatures,
			IncludeManagedConfig: true,
		})
		if err != nil {
			return err
		}
		for key, enabled := range effective.FeatureSettings() {
			values[key] = enabled
		}
		for _, spec := range features.Sorted() {
			fmt.Fprintf(stdout, "%-38s  %-18s  %t\n", spec.Key, spec.Stage, values[spec.Key])
		}
		return nil
	case "enable", "disable":
		if err := features.Validate(opts.Feature); err != nil {
			return err
		}
		enabled := opts.Action == "enable"
		if err := config.SetFeature(auth.DefaultCodexHome(), opts.Feature, enabled); err != nil {
			return err
		}
		// Mirrors Rust cli/src/main.rs maybe_print_under_development_feature_warning:
		// enabling an under-development feature prints a suppression hint.
		if enabled && features.StageFor(opts.Feature) == features.StageUnderDevelopment && !underDevelopmentWarningSuppressed(root) {
			fmt.Fprintf(stderr, "Under-development features enabled: %s. Under-development features are incomplete and may behave unpredictably. To suppress this warning, set `suppress_unstable_features_warning = true` in %s.\n",
				opts.Feature, config.ConfigPath(auth.DefaultCodexHome()))
		}
		fmt.Fprintf(stdout, "%s feature %s\n", title(opts.Action), opts.Feature)
		return nil
	default:
		return fmt.Errorf("unknown features subcommand %s", opts.Action)
	}
}

func underDevelopmentWarningSuppressed(root cli.RootOptions) bool {
	effective, err := config.LoadEffectiveWithOptions(auth.DefaultCodexHome(), &config.EffectiveOptions{
		RawOverrides:    root.ConfigOverrides,
		EnableFeatures:  root.EnableFeatures,
		DisableFeatures: root.DisableFeatures,
	})
	if err != nil {
		return false
	}
	return effective.SuppressUnstableFeaturesWarning()
}

func runCompletion(opts cli.CompletionOptions, stdout io.Writer) error {
	script, err := cli.GenerateCompletion(opts.Shell, "codex")
	if err != nil {
		return err
	}
	_, err = io.WriteString(stdout, script)
	return err
}

func readSecret(stdin io.Reader) (string, error) {
	var builder strings.Builder
	scanner := bufio.NewScanner(stdin)
	for scanner.Scan() {
		builder.WriteString(scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return strings.TrimSpace(builder.String()), nil
}

func readLoginSecret(stdin io.Reader, stderr io.Writer, terminalMessage, readingMessage, emptyMessage string) (string, error) {
	if isTerminalReader(stdin) {
		fmt.Fprintln(stderr, terminalMessage)
		return "", silentExitCode(1)
	}
	fmt.Fprintln(stderr, readingMessage)
	secret, err := readSecret(stdin)
	if err != nil {
		return "", exitMessagef("Failed to read stdin: %v", err)
	}
	if secret == "" {
		fmt.Fprintln(stderr, emptyMessage)
		return "", silentExitCode(1)
	}
	return secret, nil
}

func isTerminalReader(reader io.Reader) bool {
	terminal, ok := reader.(interface{ IsTerminal() bool })
	return ok && terminal.IsTerminal()
}

func notImplemented(name string) error {
	return fmt.Errorf("unknown command %s", name)
}

func title(value string) string {
	if value == "" {
		return value
	}
	return strings.ToUpper(value[:1]) + value[1:]
}
