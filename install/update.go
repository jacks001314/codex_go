package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"codex_go/shell"
)

const (
	DefaultGitHubLatestReleaseURL = "https://api.github.com/repos/jacks001314/codex_go/releases/latest"
	DefaultHomebrewCaskURL        = "https://formulae.brew.sh/api/cask/codex.json"
	DefaultNPMRegistryURL         = "https://registry.npmjs.org/@jacks001314%2fcodex-go"
	NPMPackageName                = "@jacks001314/codex-go"
)

type UpdateActionKind string

const (
	UpdateActionNPMGlobalLatest  UpdateActionKind = "npm-global-latest"
	UpdateActionBunGlobalLatest  UpdateActionKind = "bun-global-latest"
	UpdateActionPnpmGlobalLatest UpdateActionKind = "pnpm-global-latest"
	UpdateActionBrewUpgrade      UpdateActionKind = "brew-upgrade"
	UpdateActionStandaloneUnix   UpdateActionKind = "standalone-unix"
	UpdateActionStandaloneWin    UpdateActionKind = "standalone-windows"
)

type UpdateStatus string

const (
	UpdateStatusUpToDate        UpdateStatus = "up-to-date"
	UpdateStatusUpdateAvailable UpdateStatus = "update-available"
	UpdateStatusUnknown         UpdateStatus = "unknown"
	UpdateStatusUpdated         UpdateStatus = "updated"
)

type UpdateAction struct {
	Kind UpdateActionKind `json:"kind"`
}

type UpdateCheckOptions struct {
	Context        *InstallContext
	CurrentVersion string
	HTTPClient     *http.Client
	GitHubURL      string
	HomebrewURL    string
	NPMRegistryURL string
}

type UpdateResult struct {
	Status         UpdateStatus  `json:"status"`
	InstallMethod  string        `json:"installMethod"`
	CheckedOnline  bool          `json:"checkedOnline"`
	CurrentVersion string        `json:"currentVersion,omitempty"`
	LatestVersion  string        `json:"latestVersion,omitempty"`
	UpdateAction   *UpdateAction `json:"updateAction,omitempty"`
	UpdateCommand  []string      `json:"updateCommand,omitempty"`
	Message        string        `json:"message,omitempty"`
}

type RunUpdateOptions struct {
	Context *InstallContext
	Stdout  interface {
		Write([]byte) (int, error)
	}
	Stderr interface {
		Write([]byte) (int, error)
	}
	CommandRunner CommandRunner
}

type CommandRunner interface {
	Run(ctx context.Context, command string, args []string) error
}

type ExecCommandRunner struct {
	Stdout interface {
		Write([]byte) (int, error)
	}
	Stderr interface {
		Write([]byte) (int, error)
	}
}

func ActionFromContext(context *InstallContext) *UpdateAction {
	if context == nil {
		return nil
	}
	switch context.Method.Kind {
	case InstallNPM:
		return &UpdateAction{Kind: UpdateActionNPMGlobalLatest}
	case InstallBun:
		return &UpdateAction{Kind: UpdateActionBunGlobalLatest}
	case InstallPnpm:
		return &UpdateAction{Kind: UpdateActionPnpmGlobalLatest}
	default:
		return nil
	}
}

func (a *UpdateAction) CommandArgs() (string, []string) {
	if a == nil {
		return "", nil
	}
	switch a.Kind {
	case UpdateActionNPMGlobalLatest:
		return "npm", []string{"install", "-g", NPMPackageName + "@latest"}
	case UpdateActionBunGlobalLatest:
		return "bun", []string{"install", "-g", NPMPackageName + "@latest"}
	case UpdateActionPnpmGlobalLatest:
		return "pnpm", []string{"add", "-g", NPMPackageName + "@latest"}
	case UpdateActionBrewUpgrade:
		return "brew", []string{"upgrade", "--cask", "codex"}
	case UpdateActionStandaloneUnix:
		return "sh", []string{"-c", "curl -fsSL https://chatgpt.com/codex/install.sh | CODEX_NON_INTERACTIVE=1 sh"}
	case UpdateActionStandaloneWin:
		return "powershell", []string{"-ExecutionPolicy", "Bypass", "-c", "$env:CODEX_NON_INTERACTIVE=1; irm https://chatgpt.com/codex/install.ps1 | iex"}
	default:
		return "", nil
	}
}

func (a *UpdateAction) CommandLine() string {
	command, args := a.CommandArgs()
	if command == "" {
		return ""
	}
	parts := make([]string, 0, 1+len(args))
	parts = append(parts, command)
	parts = append(parts, args...)
	return joinCommandLine(parts)
}

func CheckForUpdate(ctx context.Context, opts *UpdateCheckOptions) (*UpdateResult, error) {
	if opts == nil {
		opts = &UpdateCheckOptions{}
	}
	installContext := opts.Context
	if installContext == nil {
		installContext = Current()
	}
	method := InstallOther
	if installContext != nil && installContext.Method.Kind != "" {
		method = installContext.Method.Kind
	}
	currentVersion := strings.TrimSpace(opts.CurrentVersion)
	action := ActionFromContext(installContext)
	result := &UpdateResult{
		Status:         UpdateStatusUnknown,
		InstallMethod:  string(method),
		CheckedOnline:  true,
		CurrentVersion: currentVersion,
		UpdateAction:   action,
	}
	if action != nil {
		command, args := action.CommandArgs()
		result.UpdateCommand = append([]string{command}, args...)
	}
	latest, err := FetchLatestVersion(ctx, opts)
	if err != nil {
		result.Message = err.Error()
		return result, err
	}
	result.LatestVersion = latest
	newer := IsNewerVersion(latest, currentVersion)
	switch {
	case newer == nil:
		result.Status = UpdateStatusUnknown
	case *newer:
		result.Status = UpdateStatusUpdateAvailable
	default:
		result.Status = UpdateStatusUpToDate
	}
	return result, nil
}

func OfflineUpdateStatus(context *InstallContext, currentVersion string) *UpdateResult {
	method := InstallOther
	if context != nil && context.Method.Kind != "" {
		method = context.Method.Kind
	}
	action := ActionFromContext(context)
	result := &UpdateResult{
		Status:         UpdateStatusUpToDate,
		InstallMethod:  string(method),
		CheckedOnline:  false,
		CurrentVersion: strings.TrimSpace(currentVersion),
		UpdateAction:   action,
	}
	if action != nil {
		command, args := action.CommandArgs()
		result.UpdateCommand = append([]string{command}, args...)
	}
	if IsSourceBuildVersion(currentVersion) {
		result.Status = UpdateStatusUnknown
		result.Message = "source build version cannot be checked offline"
	}
	return result
}

func RunUpdate(ctx context.Context, opts *RunUpdateOptions) (*UpdateResult, error) {
	if opts == nil {
		opts = &RunUpdateOptions{}
	}
	installContext := opts.Context
	if installContext == nil {
		installContext = Current()
	}
	result := OfflineUpdateStatus(installContext, "")
	action := ActionFromContext(installContext)
	if action == nil {
		result.Status = UpdateStatusUnknown
		result.Message = "could not detect the Codex installation method"
		return result, errors.New("could not detect the Codex installation method; please update manually: https://github.com/jacks001314/codex_go/releases/latest")
	}
	command, args := action.CommandArgs()
	if command == "" {
		result.Status = UpdateStatusUnknown
		result.Message = "update action does not have a command"
		return result, fmt.Errorf("update action %s does not have a command", action.Kind)
	}
	runner := opts.CommandRunner
	if runner == nil {
		runner = &ExecCommandRunner{Stdout: opts.Stdout, Stderr: opts.Stderr}
	}
	if err := runner.Run(ctx, command, args); err != nil {
		result.Status = UpdateStatusUnknown
		result.Message = err.Error()
		return result, fmt.Errorf("`%s` failed: %w", action.CommandLine(), err)
	}
	result.Status = UpdateStatusUpdated
	result.Message = "update ran successfully; restart Codex to use the new version"
	return result, nil
}

func (r *ExecCommandRunner) Run(ctx context.Context, command string, args []string) error {
	if runtime.GOOS == "windows" {
		return r.runWindowsUpdate(ctx, command, args)
	}
	actualCommand, actualArgs := updateCommandAndArgs(command, args)
	cmd := exec.CommandContext(ctx, actualCommand, actualArgs...)
	cmd.Stdout = r.Stdout
	cmd.Stderr = r.Stderr
	return cmd.Run()
}

// runWindowsUpdate resolves and executes a Windows update command. It runs from
// a temporary directory so a project-local command or package-manager config
// cannot influence the updater once the user has accepted the prompt, and it
// resolves the command using only absolute PATH entries (Rust #40422).
func (r *ExecCommandRunner) runWindowsUpdate(ctx context.Context, command string, args []string) error {
	resolved, err := resolveWindowsUpdateCommandFromPath(command)
	if err != nil {
		return err
	}
	var execCommand string
	var execArgs []string
	if strings.EqualFold(strings.TrimSpace(command), "powershell") {
		// Run PowerShell directly so the installer's PowerShell metacharacters
		// are not re-parsed by a batch shim.
		execCommand = resolved
		execArgs = args
	} else {
		// Package-manager commands on Windows are .cmd/.bat shims; route the
		// resolved command through cmd.exe /C so PATHEXT batch semantics apply.
		execCommand = "cmd"
		execArgs = []string{"/C", joinCommandLine(append([]string{resolved}, args...))}
	}
	updateDir, err := os.MkdirTemp("", "codex-update")
	if err != nil {
		return err
	}
	defer os.RemoveAll(updateDir)
	cmd := exec.CommandContext(ctx, execCommand, execArgs...)
	cmd.Dir = updateDir
	cmd.Stdout = r.Stdout
	cmd.Stderr = r.Stderr
	return cmd.Run()
}

// resolveWindowsUpdateCommandFromPath resolves command by searching only the
// absolute PATH entries (with PATHEXT extension resolution), mirroring Rust
// #40422 resolve_windows_update_command_from_path. A relative-only PATH is
// rejected so a project-local command/config cannot influence the updater.
func resolveWindowsUpdateCommandFromPath(command string) (string, error) {
	pathEnv := os.Getenv("PATH")
	if strings.TrimSpace(pathEnv) == "" {
		return "", errors.New("PATH is not set")
	}
	abs := make([]string, 0, 8)
	for _, entry := range filepath.SplitList(pathEnv) {
		if filepath.IsAbs(entry) {
			abs = append(abs, entry)
		}
	}
	if len(abs) == 0 {
		return "", fmt.Errorf("Could not find an absolute update command `%s` on PATH. Please update manually: https://developers.openai.com/codex/cli/", command)
	}
	exts := pathExtList()
	for _, dir := range abs {
		if full := findExecutableInDir(dir, command, exts); full != "" {
			return full, nil
		}
	}
	return "", fmt.Errorf("could not find update command `%s` on PATH", command)
}

func pathExtList() []string {
	raw := strings.TrimSpace(os.Getenv("PATHEXT"))
	if raw == "" {
		return []string{".exe", ".cmd", ".bat", ".com"}
	}
	parts := strings.Split(raw, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func findExecutableInDir(dir, command string, exts []string) string {
	if strings.ContainsAny(command, `/\`) {
		if executableExists(command) {
			return command
		}
		return ""
	}
	candidate := filepath.Join(dir, command)
	if filepath.Ext(command) != "" {
		if executableExists(candidate) {
			return candidate
		}
		return ""
	}
	for _, ext := range exts {
		if executableExists(candidate + ext) {
			return candidate + ext
		}
	}
	return ""
}

func executableExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// normalizeForWSL maps a Windows absolute path to its WSL mount path when
// running under WSL, mirroring Rust cli/src/wsl_paths.rs normalize_for_wsl.
// It is a variable so tests can simulate a WSL environment on any host.
var normalizeForWSL = shell.NormalizeForWSL

// updateCommandAndArgs resolves the process command and arguments for an
// update action, mirroring Rust cli/src/main.rs (run_update):
//   - on Windows the command runs via cmd.exe /C so .CMD/.BAT resolve with
//     PATHEXT semantics (PowerShell runs directly);
//   - on non-Windows the command path and every argument are normalized for
//     WSL (cli/src/wsl_paths.rs), so a Windows path carried by a WSL-managed
//     install (e.g. C:\...\codex.exe) resolves to its /mnt/<drive>/... mount
//     path before execution.
func updateCommandAndArgs(command string, args []string) (string, []string) {
	if runtime.GOOS == "windows" {
		if command != "powershell" {
			return "cmd", []string{"/C", joinCommandLine(append([]string{command}, args...))}
		}
		return command, append([]string(nil), args...)
	}
	return normalizedUpdateCommand(command, args)
}

// normalizedUpdateCommand applies the WSL path normalization to the update
// command path and each argument (Rust cli/src/main.rs non-Windows branch).
func normalizedUpdateCommand(command string, args []string) (string, []string) {
	normalized := make([]string, len(args))
	for i, arg := range args {
		normalized[i] = normalizeForWSL(arg)
	}
	return normalizeForWSL(command), normalized
}

func FetchLatestVersion(ctx context.Context, opts *UpdateCheckOptions) (string, error) {
	if opts == nil {
		opts = &UpdateCheckOptions{}
	}
	installContext := opts.Context
	if installContext == nil {
		installContext = Current()
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	switch {
	case installContext != nil && (installContext.Method.Kind == InstallNPM || installContext.Method.Kind == InstallBun || installContext.Method.Kind == InstallPnpm):
		latest, err := fetchLatestGitHubReleaseVersion(ctx, client, firstNonEmpty(opts.GitHubURL, DefaultGitHubLatestReleaseURL))
		if err != nil {
			return "", err
		}
		npmURL := firstNonEmpty(opts.NPMRegistryURL, DefaultNPMRegistryURL)
		var packageInfo npmPackageInfo
		if err := getJSON(ctx, client, npmURL, &packageInfo); err != nil {
			return "", err
		}
		if err := packageInfo.ensureVersionReady(latest); err != nil {
			return "", err
		}
		return latest, nil
	default:
		return fetchLatestGitHubReleaseVersion(ctx, client, firstNonEmpty(opts.GitHubURL, DefaultGitHubLatestReleaseURL))
	}
}

func ExtractVersionFromLatestTag(tag string) (string, error) {
	trimmed := strings.TrimSpace(tag)
	for _, prefix := range []string{"go-v", "v"} {
		if version, ok := strings.CutPrefix(trimmed, prefix); ok && strings.TrimSpace(version) != "" {
			return strings.TrimSpace(version), nil
		}
	}
	return "", fmt.Errorf("failed to parse latest tag name %q", tag)
}

func IsNewerVersion(latest string, current string) *bool {
	latestVersion, latestOK := parsePlainVersion(latest)
	currentVersion, currentOK := parsePlainVersion(current)
	if !latestOK || !currentOK {
		return nil
	}
	value := compareVersionTriplet(latestVersion, currentVersion) > 0
	return &value
}

func IsSourceBuildVersion(version string) bool {
	parsed, ok := parsePlainVersion(version)
	return ok && parsed == [3]uint64{}
}

func fetchLatestGitHubReleaseVersion(ctx context.Context, client *http.Client, url string) (string, error) {
	var info struct {
		TagName string `json:"tag_name"`
	}
	if err := getJSON(ctx, client, url, &info); err != nil {
		return "", err
	}
	return ExtractVersionFromLatestTag(info.TagName)
}

func getJSON(ctx context.Context, client *http.Client, url string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("GET %s failed: %s", url, response.Status)
	}
	decoder := json.NewDecoder(response.Body)
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode %s: %w", url, err)
	}
	return nil
}

type npmPackageInfo struct {
	DistTags map[string]string                `json:"dist-tags"`
	Versions map[string]npmPackageVersionInfo `json:"versions"`
}

type npmPackageVersionInfo struct {
	Dist *npmPackageDist `json:"dist"`
}

type npmPackageDist struct {
	Tarball   string `json:"tarball"`
	Integrity string `json:"integrity"`
}

func (p *npmPackageInfo) ensureVersionReady(version string) error {
	version = strings.TrimSpace(version)
	if p == nil {
		return errors.New("npm package metadata is nil")
	}
	latest, ok := p.DistTags["latest"]
	if !ok || latest == "" {
		return errors.New("npm package is missing latest dist-tag")
	}
	if latest != version {
		return fmt.Errorf("npm latest dist-tag points to %s, expected GitHub release %s", latest, version)
	}
	info, ok := p.Versions[version]
	if !ok {
		return fmt.Errorf("npm package version %s is missing", version)
	}
	if info.Dist == nil {
		return fmt.Errorf("npm package version %s is missing dist metadata", version)
	}
	if strings.TrimSpace(info.Dist.Tarball) == "" {
		return fmt.Errorf("npm package version %s is missing dist.tarball", version)
	}
	if strings.TrimSpace(info.Dist.Integrity) == "" {
		return fmt.Errorf("npm package version %s is missing dist.integrity", version)
	}
	return nil
}

func parsePlainVersion(value string) ([3]uint64, bool) {
	parts := strings.Split(strings.TrimSpace(value), ".")
	if len(parts) != 3 {
		return [3]uint64{}, false
	}
	var parsed [3]uint64
	for i, part := range parts {
		if part == "" || strings.ContainsAny(part, "-+ ") {
			return [3]uint64{}, false
		}
		n, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return [3]uint64{}, false
		}
		parsed[i] = n
	}
	return parsed, true
}

func compareVersionTriplet(a [3]uint64, b [3]uint64) int {
	for i := 0; i < 3; i++ {
		if a[i] > b[i] {
			return 1
		}
		if a[i] < b[i] {
			return -1
		}
	}
	return 0
}

func joinCommandLine(parts []string) string {
	quoted := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			quoted = append(quoted, `""`)
			continue
		}
		if strings.ContainsAny(part, " \t\r\n\"'|&;$()<>") {
			quoted = append(quoted, strconv.Quote(part))
			continue
		}
		quoted = append(quoted, part)
	}
	return strings.Join(quoted, " ")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
