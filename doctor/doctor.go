package doctor

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
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
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"codex_go/appserverdaemon"
	"codex_go/auth"
	"codex_go/cli"
	"codex_go/codexapi"
	"codex_go/config"
	"codex_go/features"
	"codex_go/install"
	"codex_go/mcp"
	"codex_go/model"
	"codex_go/network"
	"codex_go/rollout"
	"codex_go/sandbox"
	"codex_go/sandbox/windowssandbox"
	"codex_go/session"
	"codex_go/shell"

	"github.com/coder/websocket"
	"github.com/pelletier/go-toml/v2"
	_ "modernc.org/sqlite"
)

const defaultProviderReachabilityTimeout = 3 * time.Second
const responsesWebsocketsV2BetaHeaderValue = "responses_websockets=2026-02-06"
const websocketImmediateCloseGrace = 250 * time.Millisecond
const maxBackgroundProbeErrorChars = 120
const narrowTerminalColumns = 80
const narrowTerminalRows = 24

var buildVersion = "0.0.0"

var terminalColorEnvVarsForDoctor = []string{"COLORTERM", "NO_COLOR", "CLICOLOR", "CLICOLOR_FORCE", "FORCE_COLOR", "COLORFGBG"}
var tmuxOptionNamesForDoctor = []string{"extended-keys", "xterm-keys", "allow-passthrough", "set-clipboard", "focus-events"}

var mcpHTTPProbeURL = defaultMCPHTTPProbeURL
var probeBackgroundAppServerVersion = appserverdaemon.ProbeAppServerVersionOnSocket

type CheckStatus string

const (
	CheckStatusOK      CheckStatus = "ok"
	CheckStatusWarning CheckStatus = "warning"
	CheckStatusFail    CheckStatus = "fail"
)

type Report struct {
	SchemaVersion int            `json:"schemaVersion"`
	GeneratedAt   string         `json:"generatedAt"`
	OverallStatus CheckStatus    `json:"overallStatus"`
	CodexVersion  string         `json:"codexVersion"`
	Checks        []*DoctorCheck `json:"checks"`
}

type JSONReport struct {
	SchemaVersion int                         `json:"schemaVersion"`
	GeneratedAt   string                      `json:"generatedAt"`
	OverallStatus CheckStatus                 `json:"overallStatus"`
	CodexVersion  string                      `json:"codexVersion"`
	Checks        map[string]*JSONDoctorCheck `json:"checks"`
}

type JSONDoctorCheck struct {
	ID          string                      `json:"id"`
	Category    string                      `json:"category"`
	Status      CheckStatus                 `json:"status"`
	Summary     string                      `json:"summary"`
	Details     map[string]JSONDoctorDetail `json:"details"`
	Issues      []*JSONDoctorIssue          `json:"issues"`
	Notes       []string                    `json:"notes,omitempty"`
	Remediation *string                     `json:"remediation"`
	DurationMS  int64                       `json:"durationMs"`
}

type JSONDoctorIssue struct {
	Severity CheckStatus `json:"severity"`
	Cause    string      `json:"cause"`
	Measured *string     `json:"measured"`
	Expected *string     `json:"expected"`
	Remedy   *string     `json:"remedy"`
	Fields   []string    `json:"fields"`
}

type JSONDoctorDetail struct {
	one  string
	many []string
}

func (d JSONDoctorDetail) MarshalJSON() ([]byte, error) {
	if len(d.many) > 0 {
		return json.Marshal(d.many)
	}
	return json.Marshal(d.one)
}

type DoctorCheck struct {
	ID          string         `json:"id"`
	Category    string         `json:"category"`
	Status      CheckStatus    `json:"status"`
	Summary     string         `json:"summary"`
	Details     []string       `json:"details,omitempty"`
	Issues      []*DoctorIssue `json:"issues,omitempty"`
	Remediation *string        `json:"remediation,omitempty"`
	DurationMS  int64          `json:"durationMs"`
}

type DoctorIssue struct {
	Severity CheckStatus `json:"severity"`
	Cause    string      `json:"cause"`
	Measured *string     `json:"measured,omitempty"`
	Expected *string     `json:"expected,omitempty"`
	Remedy   *string     `json:"remedy,omitempty"`
	Fields   []string    `json:"fields,omitempty"`
}

type Options struct {
	JSON          bool
	Summary       bool
	All           bool
	NoColor       bool
	ASCII         bool
	CodexHome     string
	Root          cli.RootOptions
	DispatchPaths *cli.DispatchPaths
}

type Builder struct {
	now        func() time.Time
	currentExe func() (string, error)
	env        func(string) string
	httpClient *http.Client
}

func NewBuilder() *Builder {
	return &Builder{
		now:        time.Now,
		currentExe: os.Executable,
		env:        os.Getenv,
	}
}

func Run(opts *Options, stdout io.Writer) (*Report, error) {
	if opts == nil {
		opts = &Options{}
	}
	report := NewBuilder().Build(opts)
	if opts.JSON {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(JSONReportFromReport(report)); err != nil {
			return report, err
		}
		return report, nil
	}
	_, err := io.WriteString(stdout, RenderHuman(report, opts))
	return report, err
}

func (b *Builder) Build(opts *Options) *Report {
	if opts == nil {
		opts = &Options{}
	}
	codexHome := opts.CodexHome
	if codexHome == "" {
		codexHome = auth.DefaultCodexHome()
	}
	cwd := doctorCWD(opts)
	checks := []*DoctorCheck{
		b.timed(systemCheck),
		b.timed(func() *DoctorCheck { return diskCheck(codexHome, cwd) }),
		b.timed(endpointProtectionCheck),
		b.timed(func() *DoctorCheck { return installCheck(codexHome, !opts.Summary, b.currentExe) }),
		b.timed(func() *DoctorCheck { return runtimeCheckForDoctor(codexHome, b.currentExe) }),
		b.timed(searchCheck),
		b.timed(func() *DoctorCheck { return configCheck(codexHome, opts) }),
		b.timed(func() *DoctorCheck { return authCheck(codexHome, opts) }),
		b.timed(func() *DoctorCheck { return b.updatesCheck(codexHome, opts) }),
		b.timed(func() *DoctorCheck { return networkCheck(codexHome, opts) }),
		b.timed(func() *DoctorCheck { return websocketReachabilityCheck(codexHome, opts) }),
		b.timed(func() *DoctorCheck { return mcpCheck(codexHome, opts) }),
		b.timed(func() *DoctorCheck { return sandboxCheck(codexHome, opts) }),
		b.timed(func() *DoctorCheck { return terminalCheck(currentEnvMap(), opts) }),
		b.timed(func() *DoctorCheck { return gitCheck(cwd) }),
		b.timed(func() *DoctorCheck { return terminalTitleCheck(codexHome, opts) }),
		b.timed(func() *DoctorCheck { return stateCheck(codexHome, opts) }),
		b.timed(func() *DoctorCheck { return rolloutDBParityCheck(codexHome, opts) }),
		b.timed(func() *DoctorCheck { return backgroundServerCheck(codexHome) }),
		b.timed(func() *DoctorCheck { return b.providerReachabilityCheck(codexHome, opts) }),
	}
	checks = append(checks, desktopChecks()...)
	if runtime.GOOS == "windows" {
		checks = append(checks, b.timed(func() *DoctorCheck { return windowsDevDriveCheck(cwd) }))
	}
	return &Report{
		SchemaVersion: 1,
		GeneratedAt:   b.now().UTC().Format(time.RFC3339),
		OverallStatus: overallStatus(checks),
		CodexVersion:  Version(),
		Checks:        checks,
	}
}

func (b *Builder) timed(fn func() *DoctorCheck) *DoctorCheck {
	start := b.now()
	check := fn()
	if check == nil {
		check = NewCheck("unknown", "unknown", CheckStatusWarning, "check returned no result")
	}
	check.DurationMS = b.now().Sub(start).Milliseconds()
	return check
}

func Version() string {
	if value := strings.TrimSpace(os.Getenv("CODEX_GO_VERSION")); value != "" {
		return value
	}
	if value := strings.TrimSpace(buildVersion); value != "" {
		return value
	}
	return "0.0.0"
}

func NewCheck(id string, category string, status CheckStatus, summary string) *DoctorCheck {
	return &DoctorCheck{ID: id, Category: category, Status: status, Summary: summary}
}

func (c *DoctorCheck) Detail(detail string) *DoctorCheck {
	c.Details = append(c.Details, detail)
	return c
}

func (c *DoctorCheck) DetailsList(details []string) *DoctorCheck {
	c.Details = append(c.Details, details...)
	return c
}

func (c *DoctorCheck) Remediate(remediation string) *DoctorCheck {
	c.Remediation = &remediation
	return c
}

func (c *DoctorCheck) Issue(issue *DoctorIssue) *DoctorCheck {
	if issue != nil {
		c.Issues = append(c.Issues, issue)
	}
	return c
}

func NewIssue(severity CheckStatus, cause string) *DoctorIssue {
	return &DoctorIssue{Severity: severity, Cause: cause}
}

func (i *DoctorIssue) WithMeasured(value string) *DoctorIssue {
	i.Measured = &value
	return i
}

func (i *DoctorIssue) WithExpected(value string) *DoctorIssue {
	i.Expected = &value
	return i
}

func (i *DoctorIssue) WithRemedy(value string) *DoctorIssue {
	i.Remedy = &value
	return i
}

func (i *DoctorIssue) WithField(value string) *DoctorIssue {
	i.Fields = append(i.Fields, value)
	return i
}

func JSONReportFromReport(report *Report) *JSONReport {
	if report == nil {
		return &JSONReport{Checks: map[string]*JSONDoctorCheck{}}
	}
	checks := make(map[string]*JSONDoctorCheck, len(report.Checks))
	for _, check := range report.Checks {
		if check == nil {
			continue
		}
		checks[check.ID] = jsonDoctorCheckFromCheck(check)
	}
	return &JSONReport{
		SchemaVersion: report.SchemaVersion,
		GeneratedAt:   report.GeneratedAt,
		OverallStatus: report.OverallStatus,
		CodexVersion:  report.CodexVersion,
		Checks:        checks,
	}
}

func jsonDoctorCheckFromCheck(check *DoctorCheck) *JSONDoctorCheck {
	details, notes := structuredJSONDetails(check.Details)
	return &JSONDoctorCheck{
		ID:          check.ID,
		Category:    check.Category,
		Status:      check.Status,
		Summary:     check.Summary,
		Details:     details,
		Issues:      jsonDoctorIssuesFromIssues(check.Issues),
		Notes:       notes,
		Remediation: redactStringPtrDoctor(check.Remediation),
		DurationMS:  check.DurationMS,
	}
}

func jsonDoctorIssuesFromIssues(issues []*DoctorIssue) []*JSONDoctorIssue {
	out := make([]*JSONDoctorIssue, 0, len(issues))
	for _, issue := range issues {
		if issue == nil {
			continue
		}
		out = append(out, &JSONDoctorIssue{
			Severity: issue.Severity,
			Cause:    redactDoctorDetail(issue.Cause),
			Measured: redactStringPtrDoctor(issue.Measured),
			Expected: redactStringPtrDoctor(issue.Expected),
			Remedy:   redactStringPtrDoctor(issue.Remedy),
			Fields:   redactDoctorDetails(issue.Fields),
		})
	}
	return out
}

func structuredJSONDetails(details []string) (map[string]JSONDoctorDetail, []string) {
	structured := map[string]JSONDoctorDetail{}
	var notes []string
	for _, detail := range details {
		redacted := redactDoctorDetail(detail)
		key, value, ok := strings.Cut(redacted, ": ")
		if !ok || strings.TrimSpace(key) == "" {
			notes = append(notes, redacted)
			continue
		}
		key = strings.TrimSpace(key)
		value = jsonDetailValue(key, strings.TrimSpace(value))
		current, exists := structured[key]
		if !exists {
			structured[key] = JSONDoctorDetail{one: value}
			continue
		}
		if len(current.many) == 0 {
			current.many = []string{current.one, value}
			current.one = ""
		} else {
			current.many = append(current.many, value)
		}
		structured[key] = current
	}
	return structured, notes
}

func jsonDetailValue(key string, value string) string {
	switch key {
	case "VISUAL", "EDITOR", "PAGER", "GIT_PAGER", "GH_PAGER", "LESS":
		if !strings.EqualFold(value, "not set") {
			return "set"
		}
	}
	return value
}

func redactStringPtrDoctor(value *string) *string {
	if value == nil {
		return nil
	}
	clone := redactDoctorDetail(*value)
	return &clone
}

func redactDoctorDetails(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, redactDoctorDetail(value))
	}
	return out
}

func redactDoctorDetail(detail string) string {
	lower := strings.ToLower(detail)
	label := strings.SplitN(lower, ":", 2)[0]
	if strings.Contains(label, "env var") {
		return redactDoctorURLs(detail)
	}
	if _, value, ok := strings.Cut(detail, ": "); ok && isSafePresenceValueForDoctor(value) {
		return redactDoctorURLs(detail)
	}
	secretKeys := []string{
		"openai_api_key",
		"codex_api_key",
		"codex_access_token",
		"authorization",
		"bearer_token",
		"token",
		"secret",
	}
	for _, key := range secretKeys {
		if strings.Contains(lower, key) {
			name, _, ok := strings.Cut(detail, ":")
			if !ok {
				name = detail
			}
			return name + ": <redacted>"
		}
	}
	return redactDoctorURLs(detail)
}

func isSafePresenceValueForDoctor(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "false", "yes", "no", "present", "absent", "missing", "not set":
		return true
	default:
		return false
	}
}

func redactDoctorURLs(detail string) string {
	parts := strings.FieldsFunc(detail, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	if len(parts) == 0 {
		return detail
	}
	out := detail
	for _, part := range parts {
		redacted := redactDoctorURLToken(part)
		if redacted != part {
			out = strings.ReplaceAll(out, part, redacted)
		}
	}
	return out
}

func redactDoctorURLToken(token string) string {
	schemeEnd := strings.Index(token, "://")
	if schemeEnd < 0 {
		return token
	}
	suffixStart := len(token)
	for suffixStart > schemeEnd+3 {
		switch token[suffixStart-1] {
		case ' ', '\t', '\n', '\r', '.', ',', ';', ':', ')', ']':
			suffixStart--
		default:
			goto suffixDone
		}
	}
suffixDone:
	body := token[:suffixStart]
	suffix := token[suffixStart:]
	schemePrefixEnd := schemeEnd + len("://")
	rest := body[schemePrefixEnd:]
	authorityOffset := strings.IndexAny(rest, "/?#")
	authorityEnd := len(body)
	if authorityOffset >= 0 {
		authorityEnd = schemePrefixEnd + authorityOffset
	}
	authority := body[schemePrefixEnd:authorityEnd]
	if at := strings.LastIndex(authority, "@"); at >= 0 {
		authority = authority[at+1:]
	}
	path := body[authorityEnd:]
	if cut := strings.IndexAny(path, "?#"); cut >= 0 {
		path = path[:cut]
	}
	path = redactDoctorURLPath(path)
	return body[:schemePrefixEnd] + authority + path + suffix
}

func redactDoctorURLPath(path string) string {
	segments := strings.Split(path, "/")
	var nonEmpty []string
	for _, segment := range segments {
		if segment != "" {
			nonEmpty = append(nonEmpty, segment)
		}
	}
	if len(nonEmpty) <= 1 {
		return path
	}
	return "/" + nonEmpty[0] + "/<redacted>"
}

func systemCheck() *DoctorCheck {
	osLanguage := osLanguageForDoctor(currentEnvMap())
	details := []string{
		"os: " + runtime.GOOS,
		"os type: " + runtime.GOOS,
		"os version: unknown",
		"os language: " + firstNonEmptyDoctor(osLanguage, "unavailable"),
		"arch: " + runtime.GOARCH,
		"go: " + runtime.Version(),
	}
	for _, key := range []string{"LANG", "LC_ALL", "LC_CTYPE", "TERM", "SHELL", "ComSpec", "WT_SESSION"} {
		if value := os.Getenv(key); strings.TrimSpace(value) != "" {
			details = append(details, key+": "+redactEnvValue(key, value))
		}
	}
	for _, key := range []string{"VISUAL", "EDITOR"} {
		value := strings.TrimSpace(os.Getenv(key))
		if value == "" {
			value = "not set"
		}
		details = append(details, key+": "+redactEnvValue(key, value))
	}
	for _, key := range []string{"PAGER", "GIT_PAGER", "GH_PAGER", "LESS"} {
		if value := os.Getenv(key); strings.TrimSpace(value) != "" {
			details = append(details, key+": "+redactEnvValue(key, value))
		}
	}
	summary := "OS language unavailable"
	if osLanguage != "" {
		summary = "OS language " + osLanguage
	}
	return NewCheck(
		"system.environment",
		"system",
		CheckStatusOK,
		summary,
	).DetailsList(details)
}

func runtimeCheck() *DoctorCheck {
	return runtimeCheckForDoctor(auth.DefaultCodexHome(), os.Executable)
}

func runtimeCheckForDoctor(codexHome string, currentExe func() (string, error)) *DoctorCheck {
	exe, err := currentExeForDoctor(currentExe)
	installContext := doctorInstallContextForDoctor(exe, codexHome)
	details := []string{
		"version: " + Version(),
		"platform: " + runtime.GOOS + "-" + runtime.GOARCH,
		"install method: " + describeInstallContextForDoctor(installContext),
		"commit: " + buildCommitForDoctor(),
	}
	summary := fmt.Sprintf("running %s on %s-%s", installMethodNameForDoctor(installContext), runtime.GOOS, runtime.GOARCH)
	if err != nil || strings.TrimSpace(exe) == "" {
		details = append(details, "current executable: none")
	} else {
		if abs, absErr := filepath.Abs(exe); absErr == nil {
			exe = abs
		}
		details = append(details, "current executable: "+exe)
	}
	return NewCheck("runtime.provenance", "runtime", CheckStatusOK, summary).DetailsList(details)
}

func installCheck(codexHome string, showDetails bool, currentExe func() (string, error)) *DoctorCheck {
	exe, err := currentExeForDoctor(currentExe)
	details := []string{}
	if err != nil || strings.TrimSpace(exe) == "" {
		details = append(details, "current executable: none")
	} else {
		if abs, absErr := filepath.Abs(exe); absErr == nil {
			exe = abs
		}
		details = append(details, "current executable: "+exe)
	}

	inheritedManagedEnv := inheritedManagedEnvForCargoBinary(exe)
	ctx := doctorInstallContextForDoctor(exe, codexHome)
	details = append(details, "install context: "+describeInstallContextForDoctor(ctx))
	if inheritedManagedEnv {
		details = append(details, "ignored inherited package-manager launch env for cargo-built binary")
	}
	details = append(details,
		fmt.Sprintf("managed by npm: %t", doctorManagedByNPM(exe)),
		fmt.Sprintf("managed by bun: %t", managedEnvSetForDoctor("CODEX_MANAGED_BY_BUN")),
		fmt.Sprintf("managed by pnpm: %t", managedEnvSetForDoctor("CODEX_MANAGED_BY_PNPM")),
	)
	pushEnvPathDetailForDoctor(&details, "managed package root", "CODEX_MANAGED_PACKAGE_ROOT")

	pathEntries := codexPathEntriesForDoctor()
	status := CheckStatusOK
	summary := "installation looks consistent"
	var remediation string
	if len(pathEntries) > 1 {
		details = append(details, fmt.Sprintf("PATH codex entries: %d", len(pathEntries)))
	}
	if showDetails || len(pathEntries) > 1 {
		for index, path := range pathEntries {
			details = append(details, fmt.Sprintf("PATH codex #%d: %s", index+1, path))
		}
	}

	if doctorManagedByNPM(exe) {
		switch rootCheck := npmGlobalRootCheckForDoctor(); rootCheck.Kind {
		case npmRootCheckMatch:
			details = append(details, "npm update target: "+rootCheck.PackageRoot)
		case npmRootCheckMismatch:
			status = CheckStatusFail
			summary = "npm install -g @jacks001314/codex-go@latest would update a different install"
			remediation = fmt.Sprintf("Fix PATH or npm prefix so the running package root (%s) matches the npm global package root (%s).", rootCheck.RunningPackageRoot, rootCheck.NPMPackageRoot)
			details = append(details,
				"running package root: "+rootCheck.RunningPackageRoot,
				"npm package root: "+rootCheck.NPMPackageRoot,
			)
		case npmRootCheckMissingPackageRoot:
			status = maxCheckStatus(status, CheckStatusWarning)
			summary = "npm-managed launch is missing package-root provenance"
			remediation = "Reinstall or update Codex so the JS shim provides CODEX_MANAGED_PACKAGE_ROOT."
		case npmRootCheckNpmUnavailable:
			status = maxCheckStatus(status, CheckStatusWarning)
			summary = "npm-managed launch could not inspect npm global root"
			details = append(details, "npm root -g failed: "+rootCheck.Error)
		}
	}
	check := NewCheck("installation", "install", status, summary).DetailsList(details)
	if remediation != "" {
		check.Remediate(remediation)
	}
	return check
}

func searchCheck() *DoctorCheck {
	ctx := install.Current()
	command := ctx.RGCommand()
	provider := searchProviderForDoctor(ctx, command)
	details := []string{
		"search command: " + command,
		"search provider: " + provider,
	}
	if strings.ContainsAny(command, `/\`) {
		readiness, err := searchCommandPathReadiness(command)
		if err == nil {
			return NewCheck("runtime.search", "search", CheckStatusOK, "search is OK ("+provider+")").
				DetailsList(append(details, "search command readiness: "+readiness))
		}
		return NewCheck("runtime.search", "search", CheckStatusWarning, "search command path is not executable").
			DetailsList(details).
			Detail("search command readiness: " + err.Error()).
			Remediate("Install ripgrep or repair the bundled Codex package.")
	}
	output, err := exec.Command(command, "--version").Output()
	if err != nil {
		return NewCheck("runtime.search", "search", CheckStatusWarning, "search command could not be verified").
			DetailsList(details).
			Detail("search command readiness: " + err.Error()).
			Remediate("Install ripgrep or make sure rg is on PATH.")
	}
	version := strings.TrimSpace(strings.SplitN(string(output), "\n", 2)[0])
	return NewCheck("runtime.search", "search", CheckStatusOK, "search is OK ("+provider+")").DetailsList(append(details, "search command readiness: "+version))
}

func searchCommandPathReadiness(command string) (string, error) {
	info, err := os.Stat(command)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("path is a directory")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("file is not executable")
	}
	return "file exists and is executable", nil
}

func osLanguageForDoctor(env map[string]string) string {
	for _, key := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		value := strings.TrimSpace(env[key])
		if value == "" || strings.EqualFold(value, "C") || strings.EqualFold(value, "POSIX") {
			continue
		}
		if before, _, ok := strings.Cut(value, "."); ok {
			value = before
		}
		value = strings.ReplaceAll(value, "_", "-")
		if value != "" {
			return value
		}
	}
	return ""
}

func firstNonEmptyDoctor(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func buildCommitForDoctor() string {
	for _, key := range []string{"CODEX_BUILD_COMMIT", "GIT_COMMIT"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return "unknown"
}

func currentExeForDoctor(fn func() (string, error)) (string, error) {
	if fn != nil {
		return fn()
	}
	return os.Executable()
}

func doctorInstallContextForDoctor(currentExe string, codexHome string) *install.InstallContext {
	if inheritedManagedEnvForCargoBinary(currentExe) {
		return &install.InstallContext{Method: install.InstallMethod{Kind: install.InstallOther}}
	}
	return install.FromExe(
		runtime.GOOS == "darwin",
		currentExe,
		managedEnvSetForDoctor("CODEX_MANAGED_BY_PNPM"),
		doctorManagedByNPM(currentExe),
		managedEnvSetForDoctor("CODEX_MANAGED_BY_BUN"),
		codexHome,
	)
}

func installMethodNameForDoctor(ctx *install.InstallContext) string {
	if ctx == nil {
		return "local build"
	}
	switch ctx.Method.Kind {
	case install.InstallStandalone:
		return "standalone"
	case install.InstallNPM:
		return "npm"
	case install.InstallBun:
		return "bun"
	case install.InstallPnpm:
		return "pnpm"
	case install.InstallBrew:
		return "brew"
	default:
		return "local build"
	}
}

func describeInstallContextForDoctor(ctx *install.InstallContext) string {
	if ctx == nil {
		return "other"
	}
	switch ctx.Method.Kind {
	case install.InstallStandalone:
		platform := "unknown"
		if ctx.Method.Platform != nil {
			platform = string(*ctx.Method.Platform)
		}
		if ctx.PackageLayout != nil {
			return fmt.Sprintf(
				"standalone (%s, package %s, bin %s, resources %s, path %s)",
				platform,
				ctx.PackageLayout.PackageDir,
				ctx.PackageLayout.BinDir,
				displayOptionalPathForDoctor(ctx.PackageLayout.ResourcesDir),
				displayOptionalPathForDoctor(ctx.PackageLayout.PathDir),
			)
		}
		return fmt.Sprintf(
			"standalone (%s, release %s, resources %s)",
			platform,
			ctx.Method.ReleaseDir,
			displayOptionalPathForDoctor(ctx.Method.ResourcesDir),
		)
	case install.InstallNPM:
		return describeMethodWithPackageLayoutForDoctor("npm", ctx.PackageLayout)
	case install.InstallBun:
		return describeMethodWithPackageLayoutForDoctor("bun", ctx.PackageLayout)
	case install.InstallPnpm:
		return describeMethodWithPackageLayoutForDoctor("pnpm", ctx.PackageLayout)
	case install.InstallBrew:
		return describeMethodWithPackageLayoutForDoctor("brew", ctx.PackageLayout)
	default:
		return describeMethodWithPackageLayoutForDoctor("other", ctx.PackageLayout)
	}
}

func describeMethodWithPackageLayoutForDoctor(method string, layout *install.CodexPackageLayout) string {
	if layout == nil {
		return method
	}
	return fmt.Sprintf(
		"%s (package %s, bin %s, resources %s, path %s)",
		method,
		layout.PackageDir,
		layout.BinDir,
		displayOptionalPathForDoctor(layout.ResourcesDir),
		displayOptionalPathForDoctor(layout.PathDir),
	)
}

func displayOptionalPathForDoctor(path *string) string {
	if path == nil {
		return "none"
	}
	return *path
}

func searchProviderForDoctor(ctx *install.InstallContext, command string) string {
	if ctx == nil || strings.TrimSpace(command) == "" {
		return "system"
	}
	if ctx.PackageLayout != nil && ctx.PackageLayout.PathDir != nil {
		if pathStartsWith(command, *ctx.PackageLayout.PathDir) {
			return "bundled"
		}
	}
	if ctx.Method.Kind == install.InstallStandalone && ctx.Method.ResourcesDir != nil {
		if pathStartsWith(command, *ctx.Method.ResourcesDir) {
			return "bundled"
		}
	}
	return "system"
}

func pathStartsWith(path string, root string) bool {
	if strings.TrimSpace(path) == "" || strings.TrimSpace(root) == "" {
		return false
	}
	cleanPath, pathErr := filepath.Abs(path)
	cleanRoot, rootErr := filepath.Abs(root)
	if pathErr != nil || rootErr != nil {
		return false
	}
	rel, err := filepath.Rel(cleanRoot, cleanPath)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func configCheck(codexHome string, opts *Options) *DoctorCheck {
	if opts == nil {
		opts = &Options{}
	}
	cfg, err := config.LoadEffectiveWithOptions(codexHome, &config.EffectiveOptions{
		Profile:         opts.Root.Shared.Profile,
		CWD:             opts.Root.Shared.CWD,
		RawOverrides:    append([]string(nil), opts.Root.ConfigOverrides...),
		EnableFeatures:  append([]string(nil), opts.Root.EnableFeatures...),
		DisableFeatures: append([]string(nil), opts.Root.DisableFeatures...),
	})
	details := []string{"config.toml: " + config.ConfigPath(codexHome)}
	if err != nil {
		return NewCheck("config.load", "config", CheckStatusFail, "config could not be loaded").
			DetailsList(details).
			Detail("error: " + err.Error()).
			Remediate("Fix the reported config error, then rerun codex doctor.")
	}
	modelName := stringConfigValueForDoctor(cfg, "model")
	if modelName == "" {
		modelName = "<default>"
	}
	details = []string{
		"CODEX_HOME: " + codexHome,
		"cwd: " + doctorCWD(opts),
		"model: " + modelName,
		"model provider: " + effectiveProviderIDForDoctor(opts, cfg),
		"log dir: " + logDirForDoctor(codexHome, cfg),
		"sqlite home: " + sqliteHomeForDoctor(codexHome, opts),
		fmt.Sprintf("mcp servers: %d", len(mcpServersFromConfig(cfg))),
	}
	featureFlagDetails(cfg, &details)
	configTomlDetailsForDoctor(codexHome, &details)
	return NewCheck("config.load", "config", CheckStatusOK, "config loaded").DetailsList(details)
}

func logDirForDoctor(codexHome string, cfg *config.Config) string {
	if value := stringConfigValueForDoctor(cfg, "log_dir"); value != "" {
		return value
	}
	return filepath.Join(codexHome, "log")
}

func sqliteHomeForDoctor(codexHome string, opts *Options) string {
	if raw := strings.TrimSpace(os.Getenv("CODEX_SQLITE_HOME")); raw != "" {
		if filepath.IsAbs(raw) {
			return raw
		}
		return filepath.Join(doctorCWD(opts), raw)
	}
	return codexHome
}

func configTomlDetailsForDoctor(codexHome string, details *[]string) {
	if details == nil {
		return
	}
	path := config.ConfigPath(codexHome)
	*details = append(*details, "config.toml: "+path)
	contents, err := os.ReadFile(path)
	switch {
	case err == nil:
		var parsed map[string]any
		if parseErr := toml.Unmarshal(contents, &parsed); parseErr != nil {
			*details = append(*details, "config.toml parse: "+parseErr.Error())
		} else {
			*details = append(*details, "config.toml parse: ok")
		}
	case errors.Is(err, os.ErrNotExist):
		*details = append(*details, "config.toml: missing")
	default:
		*details = append(*details, "config.toml read: "+err.Error())
	}
}

func authCheck(codexHome string, opts *Options) *DoctorCheck {
	storeOptions, cfgErr := authStoreOptionsForDoctor(codexHome, opts)
	store := auth.NewStoreWithOptions(codexHome, storeOptions)
	mode := auth.AuthCredentialsStoreFile
	if storeOptions != nil && storeOptions.Mode != "" {
		mode = storeOptions.Mode
	}
	details := []string{
		"auth storage mode: " + doctorAuthStorageModeLabel(mode),
		"auth file: " + store.Path(),
	}
	if cfgErr != nil {
		details = append(details, "auth storage config error: "+cfgErr.Error())
	}
	envAuthVars := presentAuthEnvVars()
	if len(envAuthVars) > 0 {
		details = append(details, "auth env vars present: "+strings.Join(envAuthVars, ", "))
	}
	if cfg, err := loadEffectiveConfigForDoctor(codexHome, opts); err == nil {
		if check := providerSpecificAuthCheckForDoctor(cfg, opts, details); check != nil {
			return check
		}
	}

	stored, err := store.Load()
	if err != nil {
		return NewCheck("auth.credentials", "auth", CheckStatusFail, "stored credentials could not be read").
			Detail(err.Error()).
			Remediate("Fix auth storage access or run codex login again.")
	}
	if stored == nil {
		if len(envAuthVars) > 0 {
			return NewCheck("auth.credentials", "auth", CheckStatusOK, "auth is provided by environment").
				DetailsList(details)
		}
		return NewCheck("auth.credentials", "auth", CheckStatusFail, "no Codex credentials were found").
			DetailsList(details).
			Remediate("Run codex login or provide an API key through a supported auth env var.")
	}
	details = append(details,
		"stored auth mode: "+storedAuthModeForDoctor(stored),
		fmt.Sprintf("stored API key: %t", strings.TrimSpace(stored.OpenAIAPIKey) != ""),
		fmt.Sprintf("stored ChatGPT tokens: %t", stored.Tokens != nil),
		fmt.Sprintf("stored agent identity: %t", stored.AgentIdentity != nil),
	)
	authIssues := storedAuthIssuesForDoctor(stored, envVarPresent)
	for _, issue := range authIssues {
		details = append(details, "stored auth issue: "+issue)
	}
	status := CheckStatusOK
	summary := "auth is configured"
	if len(authIssues) > 0 && len(envAuthVars) == 0 {
		status = CheckStatusFail
		summary = "stored credentials are incomplete"
	} else if len(authIssues) > 0 {
		status = CheckStatusWarning
		summary = "auth is provided by environment, but stored credentials are incomplete"
	} else if len(envAuthVars) > 1 {
		status = CheckStatusWarning
		summary = "auth is configured, but multiple auth env vars are present"
	}
	check := NewCheck("auth.credentials", "auth", status, summary).DetailsList(details)
	if status == CheckStatusFail {
		check.Remediate("Run codex login again or provide a supported auth env var.")
	}
	return check
}

func doctorAuthStorageModeLabel(mode auth.AuthCredentialsStoreMode) string {
	switch mode {
	case auth.AuthCredentialsStoreKeyring:
		return "Keyring"
	case auth.AuthCredentialsStoreAuto:
		return "Auto"
	case auth.AuthCredentialsStoreEphemeral:
		return "Ephemeral"
	default:
		return "File"
	}
}

func presentAuthEnvVars() []string {
	var present []string
	for _, key := range []string{auth.OpenAIAPIKeyEnv, auth.CodexAPIKeyEnv, auth.CodexAccessTokenEnv} {
		if strings.TrimSpace(os.Getenv(key)) != "" {
			present = append(present, key)
		}
	}
	return present
}

func providerSpecificAuthCheckForDoctor(cfg *config.Config, opts *Options, details []string) *DoctorCheck {
	providerID := effectiveProviderIDForDoctor(opts, cfg)
	provider, err := model.ProviderForConfigID(configValuesForDoctor(cfg), providerID, stringConfigValueForDoctor(cfg, "openai_base_url"))
	if err != nil || provider == nil {
		return nil
	}
	details = append(details, fmt.Sprintf("model provider requires OpenAI auth: %t", provider.RequiresOpenAIAuth))
	if provider.RequiresOpenAIAuth {
		return nil
	}
	envKey := strings.TrimSpace(provider.EnvKey)
	if envKey != "" {
		if strings.TrimSpace(os.Getenv(envKey)) != "" {
			details = append(details, fmt.Sprintf("provider auth env var: %s (present)", envKey))
			return NewCheck("auth.credentials", "auth", CheckStatusOK, "auth is provided by the active model provider").
				DetailsList(details)
		}
		details = append(details, fmt.Sprintf("provider auth env var: %s (missing)", envKey))
		remediation := strings.TrimSpace(provider.EnvKeyInstructions)
		if remediation == "" {
			remediation = fmt.Sprintf("Set %s for the active model provider.", envKey)
		}
		return NewCheck("auth.credentials", "auth", CheckStatusFail, "active model provider auth env var is missing").
			DetailsList(details).
			Remediate(remediation)
	}
	return NewCheck("auth.credentials", "auth", CheckStatusOK, "OpenAI auth is not required for the active model provider").
		DetailsList(details)
}

func storedAuthModeForDoctor(snapshot *auth.AuthDotJSON) string {
	switch storedAuthModeValueForDoctor(snapshot) {
	case "api-key":
		return "api_key"
	case "chatgpt":
		return "chatgpt"
	case "chatgptAuthTokens":
		return "chatgpt_auth_tokens"
	case "chatgpt-auth-tokens":
		return "chatgpt_auth_tokens"
	case "agent-identity":
		return "agent_identity"
	case "personal-access-token":
		return "personal_access_token"
	case "bedrock-api-key":
		return "bedrock_api_key"
	default:
		return snapshot.Mode()
	}
}

func storedAuthModeValueForDoctor(snapshot *auth.AuthDotJSON) string {
	if snapshot == nil {
		return "chatgpt"
	}
	if mode := snapshot.Mode(); mode != "unknown" {
		return mode
	}
	return "chatgpt"
}

func storedAuthIssuesForDoctor(snapshot *auth.AuthDotJSON, envPresent func(string) bool) []string {
	if snapshot == nil {
		return []string{"ChatGPT auth is missing token data", "ChatGPT auth is missing refresh metadata"}
	}
	switch storedAuthModeValueForDoctor(snapshot) {
	case "api-key":
		storedKeyPresent := strings.TrimSpace(snapshot.OpenAIAPIKey) != ""
		envKeyPresent := envPresent != nil && (envPresent(auth.OpenAIAPIKeyEnv) || envPresent(auth.CodexAPIKeyEnv))
		if !storedKeyPresent && !envKeyPresent {
			return []string{"API key auth is missing an API key"}
		}
	case "chatgptAuthTokens", "chatgpt-auth-tokens":
		issues := []string{}
		if snapshot.Tokens == nil {
			issues = append(issues, "external ChatGPT auth is missing token data")
		} else {
			if strings.TrimSpace(stringFromDoctorAny(snapshot.Tokens["access_token"])) == "" {
				issues = append(issues, "external ChatGPT auth is missing an access token")
			}
			if strings.TrimSpace(chatGPTAccountIDFromDoctorTokens(snapshot.Tokens)) == "" {
				issues = append(issues, "external ChatGPT auth is missing a ChatGPT account id")
			}
		}
		if strings.TrimSpace(snapshot.LastRefresh) == "" {
			issues = append(issues, "external ChatGPT auth is missing refresh metadata")
		}
		return issues
	case "agent-identity":
		if !agentIdentityHasAuthMaterialForDoctor(snapshot.AgentIdentity) {
			return []string{"agent identity auth is missing an agent identity token"}
		}
	case "personal-access-token":
		if strings.TrimSpace(snapshot.PersonalAccessToken) == "" {
			return []string{"personal access token auth is missing a personal access token"}
		}
	case "bedrock-api-key":
		if snapshot.BedrockAPIKey == nil {
			return []string{"Bedrock API key auth is missing a Bedrock API key"}
		}
	default:
		issues := []string{}
		if snapshot.Tokens == nil {
			issues = append(issues, "ChatGPT auth is missing token data")
		} else {
			if strings.TrimSpace(stringFromDoctorAny(snapshot.Tokens["access_token"])) == "" {
				issues = append(issues, "ChatGPT auth is missing an access token")
			}
			if strings.TrimSpace(stringFromDoctorAny(snapshot.Tokens["refresh_token"])) == "" {
				issues = append(issues, "ChatGPT auth is missing a refresh token")
			}
		}
		if strings.TrimSpace(snapshot.LastRefresh) == "" {
			issues = append(issues, "ChatGPT auth is missing refresh metadata")
		}
		return issues
	}
	return nil
}

func chatGPTAccountIDFromDoctorTokens(tokens map[string]any) string {
	if tokens == nil {
		return ""
	}
	if value := strings.TrimSpace(stringFromDoctorAny(tokens["account_id"])); value != "" {
		return value
	}
	if value := strings.TrimSpace(stringFromDoctorAny(tokens["chatgpt_account_id"])); value != "" {
		return value
	}
	idToken, _ := tokens["id_token"].(map[string]any)
	return stringFromDoctorAny(idToken["chatgpt_account_id"])
}

func agentIdentityHasAuthMaterialForDoctor(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(typed) != ""
	case *auth.AgentIdentityAuthRecord:
		return typed != nil && strings.TrimSpace(typed.AgentRuntimeID) != "" && strings.TrimSpace(typed.AgentPrivateKey) != ""
	case auth.AgentIdentityAuthRecord:
		return strings.TrimSpace(typed.AgentRuntimeID) != "" && strings.TrimSpace(typed.AgentPrivateKey) != ""
	case map[string]any:
		runtimeID := firstNonEmptyString(
			stringFromDoctorAny(typed["agent_runtime_id"]),
			stringFromDoctorAny(typed["agentRuntimeId"]),
		)
		privateKey := firstNonEmptyString(
			stringFromDoctorAny(typed["agent_private_key"]),
			stringFromDoctorAny(typed["agentPrivateKey"]),
		)
		token := firstNonEmptyString(
			stringFromDoctorAny(typed["token"]),
			stringFromDoctorAny(typed["access_token"]),
			stringFromDoctorAny(typed["agent_identity_token"]),
		)
		return (strings.TrimSpace(runtimeID) != "" && strings.TrimSpace(privateKey) != "") || strings.TrimSpace(token) != ""
	default:
		return false
	}
}

func stringFromDoctorAny(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []byte:
		return string(typed)
	case fmt.Stringer:
		return typed.String()
	default:
		return fmt.Sprint(typed)
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

type providerAuthReachabilityMode string

const (
	providerAuthReachabilityNotRequired providerAuthReachabilityMode = "not_required"
	providerAuthReachabilityAPIKey      providerAuthReachabilityMode = "api_key"
	providerAuthReachabilityChatGPT     providerAuthReachabilityMode = "chatgpt"
)

type reachabilityPlan struct {
	Description string
	Endpoints   []*reachabilityEndpoint
	// httpClient carries the route-aware probe client built from the effective
	// config (Rust #38918 carries an HttpClientFactory on the plan).
	httpClient *http.Client
}

type reachabilityEndpoint struct {
	Label         string
	URL           string
	Required      bool
	RouteProbeURL *string
}

type routeProbeOutcome struct {
	Status         CheckStatus
	Message        string
	TransportError bool
}

func (m providerAuthReachabilityMode) description() string {
	switch m {
	case providerAuthReachabilityNotRequired:
		return "provider auth"
	case providerAuthReachabilityAPIKey:
		return "API key auth"
	case providerAuthReachabilityChatGPT:
		return "ChatGPT auth"
	default:
		return "provider auth"
	}
}

func (b *Builder) providerReachabilityCheck(codexHome string, opts *Options) *DoctorCheck {
	plan, err := providerReachabilityPlan(codexHome, opts)
	if err != nil {
		return NewCheck("network.provider_reachability", "reachability", CheckStatusWarning, "active provider endpoint could not be resolved").
			Detail("error: " + err.Error()).
			Remediate("Fix provider configuration, then rerun codex doctor.")
	}
	return b.runProviderReachabilityCheck(plan)
}

func providerReachabilityPlan(codexHome string, opts *Options) (*reachabilityPlan, error) {
	cfg, err := loadEffectiveConfigForDoctor(codexHome, opts)
	if err != nil {
		return nil, err
	}
	providerID := effectiveProviderIDForDoctor(opts, cfg)
	provider, err := model.ProviderForConfigID(configValuesForDoctor(cfg), providerID, stringConfigValueForDoctor(cfg, "openai_base_url"))
	if err != nil {
		return nil, err
	}
	storeOptions := auth.StoreOptionsFromConfig(cfg.CLIAuthCredentialsStoreMode(), cfg.SecretAuthStorageEnabled())
	resolved, _ := auth.NewStoreWithOptions(codexHome, storeOptions).Resolve()
	mode := providerAuthReachabilityModeFromAuth(provider.RequiresOpenAIAuth, envVarPresent, resolved)
	providerBaseURL, err := activeProviderBaseURLForDoctor(providerID, provider, resolved)
	if err != nil {
		return nil, err
	}
	plan := providerReachabilityPlanFromParts(
		mode,
		providerID,
		provider.Name,
		providerBaseURL,
		provider.QueryParams,
		provider.IsAmazonBedrock(),
		stringConfigValueForDoctor(cfg, "chatgpt_base_url"),
	)
	plan.httpClient = routeAwareProbeHTTPClient(cfg)
	return plan, nil
}

func activeProviderBaseURLForDoctor(providerID string, provider *model.ProviderInfo, resolved *auth.ResolvedAuth) (string, error) {
	if provider == nil {
		return "", nil
	}
	if !provider.IsAmazonBedrock() {
		return provider.BaseURL, nil
	}
	var snapshot *auth.AuthDotJSON
	if resolved != nil {
		snapshot = &resolved.Auth
	}
	runtimeProvider := model.CreateRuntimeProviderForID(providerID, *provider, snapshot)
	if baseURL, err := runtimeProvider.RuntimeBaseURL(); err == nil && strings.TrimSpace(baseURL) != "" {
		return baseURL, nil
	} else if err != nil {
		return "", err
	}
	return provider.BaseURL, nil
}

func providerAuthReachabilityModeFromAuth(requiresOpenAIAuth bool, envPresent func(string) bool, resolved *auth.ResolvedAuth) providerAuthReachabilityMode {
	if !requiresOpenAIAuth {
		return providerAuthReachabilityNotRequired
	}
	if envPresent(auth.OpenAIAPIKeyEnv) || envPresent(auth.CodexAPIKeyEnv) {
		return providerAuthReachabilityAPIKey
	}
	if envPresent(auth.CodexAccessTokenEnv) {
		return providerAuthReachabilityChatGPT
	}
	if resolved == nil {
		return providerAuthReachabilityChatGPT
	}
	switch (&resolved.Auth).Mode() {
	case "api-key", "bedrock-api-key":
		return providerAuthReachabilityAPIKey
	case "chatgpt", "chatgptAuthTokens", "chatgpt-auth-tokens", "agent-identity", "personal-access-token":
		return providerAuthReachabilityChatGPT
	default:
		return providerAuthReachabilityChatGPT
	}
}

func providerReachabilityPlanFromParts(mode providerAuthReachabilityMode, providerID string, providerName string, providerBaseURL string, queryParams map[string]string, isAmazonBedrock bool, chatGPTBaseURL string) *reachabilityPlan {
	if strings.TrimSpace(chatGPTBaseURL) == "" {
		chatGPTBaseURL = "https://chatgpt.com/backend-api/"
	}
	routeProbeURL := providerRouteProbeURL(mode, providerName, providerBaseURL, queryParams, isAmazonBedrock)
	endpoints := []*reachabilityEndpoint{}
	switch mode {
	case providerAuthReachabilityAPIKey:
		baseURL := strings.TrimSpace(providerBaseURL)
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		endpoints = append(endpoints, &reachabilityEndpoint{
			Label:         providerID + " API",
			URL:           baseURL,
			Required:      true,
			RouteProbeURL: routeProbeURL,
		})
	case providerAuthReachabilityChatGPT:
		endpoints = append(endpoints, &reachabilityEndpoint{
			Label:    "ChatGPT",
			URL:      chatGPTBaseURL,
			Required: true,
		})
	case providerAuthReachabilityNotRequired:
		if strings.TrimSpace(providerBaseURL) != "" {
			endpoints = append(endpoints, &reachabilityEndpoint{
				Label:         providerID + " API",
				URL:           providerBaseURL,
				Required:      true,
				RouteProbeURL: routeProbeURL,
			})
		}
	}
	return &reachabilityPlan{Description: mode.description(), Endpoints: endpoints}
}

func providerRouteProbeURL(mode providerAuthReachabilityMode, providerName string, providerBaseURL string, queryParams map[string]string, isAmazonBedrock bool) *string {
	baseURL := strings.TrimSpace(providerBaseURL)
	if baseURL == "" && mode == providerAuthReachabilityAPIKey {
		baseURL = "https://api.openai.com/v1"
	}
	if baseURL == "" || !shouldProbeModelsRoute(providerName, baseURL, isAmazonBedrock) {
		return nil
	}
	value := providerURLForPath(baseURL, "models", queryParams)
	return &value
}

func shouldProbeModelsRoute(providerName string, baseURL string, isAmazonBedrock bool) bool {
	return !isAmazonBedrock && !model.IsAzureResponsesProvider(providerName, baseURL)
}

func providerURLForPath(baseURL string, path string, queryParams map[string]string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	cleanPath := strings.TrimLeft(strings.TrimSpace(path), "/")
	out := base
	if cleanPath != "" {
		out += "/" + cleanPath
	}
	if len(queryParams) == 0 {
		return out
	}
	keys := make([]string, 0, len(queryParams))
	for key := range queryParams {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", key, queryParams[key]))
	}
	separator := "?"
	if strings.Contains(out, "?") {
		separator = "&"
	}
	return out + separator + strings.Join(parts, "&")
}

func (b *Builder) runProviderReachabilityCheck(plan *reachabilityPlan) *DoctorCheck {
	if plan == nil {
		plan = &reachabilityPlan{Description: providerAuthReachabilityChatGPT.description()}
	}
	details := []string{"reachability mode: " + plan.Description}
	if len(plan.Endpoints) == 0 {
		details = append(details, "active provider endpoint: none configured")
		return NewCheck("network.provider_reachability", "reachability", CheckStatusOK, "active provider has no HTTP endpoint to probe").DetailsList(details)
	}
	client := b.providerHTTPClient()
	if plan != nil && plan.httpClient != nil {
		client = plan.httpClient
	}
	requiredFailures := 0
	warnings := 0
	var issues []*DoctorIssue
	ctx := context.Background()
	for _, endpoint := range plan.Endpoints {
		if endpoint == nil {
			continue
		}
		status, err := httpProbeURL(ctx, client, endpoint.URL)
		if err != nil {
			requirement := "optional"
			if endpoint.Required {
				requirement = "required"
				requiredFailures++
			} else {
				warnings++
			}
			details = append(details, fmt.Sprintf("%s base URL: %s %s (%s)", endpoint.Label, endpoint.URL, err.Error(), requirement))
			continue
		}
		details = append(details, fmt.Sprintf("%s base URL: %s reachable (%s)", endpoint.Label, endpoint.URL, status))
		if endpoint.RouteProbeURL == nil {
			continue
		}
		outcome := providerRouteProbe(ctx, client, *endpoint.RouteProbeURL)
		switch outcome.Status {
		case CheckStatusOK:
			details = append(details, fmt.Sprintf("%s route probe: %s route exists (%s)", endpoint.Label, *endpoint.RouteProbeURL, outcome.Message))
		case CheckStatusWarning:
			warnings++
			details = append(details, fmt.Sprintf("%s route probe: %s returned %s (warning)", endpoint.Label, *endpoint.RouteProbeURL, outcome.Message))
		case CheckStatusFail:
			requiredFailures++
			if outcome.TransportError {
				details = append(details, fmt.Sprintf("%s route probe: %s %s (required)", endpoint.Label, *endpoint.RouteProbeURL, outcome.Message))
				issues = append(issues, NewIssue(CheckStatusFail, "provider route probe could not connect - verify network access to the provider API").
					WithMeasured(fmt.Sprintf("%s %s", *endpoint.RouteProbeURL, outcome.Message)).
					WithExpected("GET /models completes").
					WithRemedy("Check proxy, VPN, firewall, DNS, and custom CA configuration.").
					WithField("route probe"))
			} else {
				details = append(details, fmt.Sprintf("%s route probe: %s returned %s (required)", endpoint.Label, *endpoint.RouteProbeURL, outcome.Message))
				issues = append(issues, NewIssue(CheckStatusFail, "provider base URL route returned 404 - verify the configured API prefix").
					WithMeasured(fmt.Sprintf("%s returned %s", *endpoint.RouteProbeURL, outcome.Message)).
					WithExpected("GET /models returns 2xx, 401, or 403").
					WithRemedy("Set base_url to the provider API root, for example https://api.openai.com/v1").
					WithField("route probe"))
			}
		}
	}
	status, summary := providerReachabilityOutcome(requiredFailures, warnings)
	check := NewCheck("network.provider_reachability", "reachability", status, summary).DetailsList(details)
	for _, issue := range issues {
		check.Issue(issue)
	}
	if status != CheckStatusOK {
		check.Remediate("Check proxy, VPN, firewall, DNS, and custom CA configuration.")
	}
	return check
}

func providerReachabilityOutcome(requiredFailures int, warnings int) (CheckStatus, string) {
	switch {
	case requiredFailures == 0 && warnings == 0:
		return CheckStatusOK, "active provider endpoints are reachable over HTTP"
	case requiredFailures == 0:
		return CheckStatusWarning, "provider endpoint checks returned warnings"
	default:
		return CheckStatusFail, "one or more required provider endpoints are unreachable over HTTP"
	}
}

func (b *Builder) providerHTTPClient() *http.Client {
	if b != nil && b.httpClient != nil {
		return b.httpClient
	}
	return &http.Client{Timeout: defaultProviderReachabilityTimeout}
}

// routeAwareProbeHTTPClient builds the provider probe client, matching Rust's
// route-aware pool used by codex doctor (#38918): the effective
// respect_system_proxy feature decides whether the system proxy is honored,
// and custom CA env vars (CODEX_CA_CERTIFICATE / SSL_CERT_FILE) extend the
// system roots. An invalid custom CA bundle leaves the system roots intact
// (Rust with_legacy_custom_ca_fallback preserves the transport-default pool).
func routeAwareProbeHTTPClient(cfg *config.Config) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyFromEnvironment
	if cfg == nil || !cfg.RespectSystemProxyEnabled() {
		transport.Proxy = nil
	}
	transport.TLSClientConfig = &tls.Config{
		RootCAs:    probeRootCAsForDoctor(),
		MinVersion: tls.VersionTLS12,
	}
	return &http.Client{Timeout: defaultProviderReachabilityTimeout, Transport: transport}
}

// probeRootCAsForDoctor extends the system certificate pool with custom CA
// bundles pointed at by CODEX_CA_CERTIFICATE / SSL_CERT_FILE. Unreadable or
// unparseable bundles are ignored so the system roots remain usable.
func probeRootCAsForDoctor() *x509.CertPool {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	for _, key := range []string{"CODEX_CA_CERTIFICATE", "SSL_CERT_FILE"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			if contents, readErr := os.ReadFile(value); readErr == nil {
				// Invalid bundles leave the pool unchanged (system-root fallback).
				_ = network.AppendCertsFromPEMNormalized(pool, contents)
			}
		}
	}
	return pool
}

func httpProbeURL(ctx context.Context, client *http.Client, rawURL string) (string, error) {
	return httpProbeURLWithMethod(ctx, client, http.MethodHead, rawURL)
}

func httpGetProbeStatus(ctx context.Context, client *http.Client, rawURL string) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, fmt.Errorf("request could not be built")
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, classifyHTTPProbeError(err)
	}
	defer response.Body.Close()
	return response.StatusCode, nil
}

func httpProbeURLWithMethod(ctx context.Context, client *http.Client, method string, rawURL string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("request could not be built")
	}
	response, err := client.Do(request)
	if err != nil {
		return "", classifyHTTPProbeError(err)
	}
	defer response.Body.Close()
	return fmt.Sprintf("HTTP %d", response.StatusCode), nil
}

func providerRouteProbe(ctx context.Context, client *http.Client, rawURL string) *routeProbeOutcome {
	status, err := httpGetProbeStatus(ctx, client, rawURL)
	if err != nil {
		return &routeProbeOutcome{Status: CheckStatusFail, Message: err.Error(), TransportError: true}
	}
	message := fmt.Sprintf("HTTP %d", status)
	switch {
	case (status >= 200 && status < 300) || status == http.StatusUnauthorized || status == http.StatusForbidden:
		return &routeProbeOutcome{Status: CheckStatusOK, Message: message}
	case status == http.StatusNotFound:
		return &routeProbeOutcome{Status: CheckStatusFail, Message: message}
	default:
		return &routeProbeOutcome{Status: CheckStatusWarning, Message: message}
	}
}

func classifyHTTPProbeError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("request timed out")
	}
	if isTLSProbeError(err) {
		return errors.New("TLS handshake or certificate validation failed")
	}
	if isProxyAuthenticationProbeError(err) {
		return errors.New("proxy authentication required")
	}
	if isInvalidProxyConfigProbeError(err) {
		return errors.New("invalid proxy configuration")
	}
	if isUnsupportedProxySchemeProbeError(err) {
		return errors.New("unsupported proxy configuration")
	}
	if isProxyResolutionProbeError(err) {
		return errors.New("proxy resolution failed")
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		if urlErr.Timeout() {
			return errors.New("request timed out")
		}
	}
	if isConnectProbeError(err) {
		return errors.New("connect failed")
	}
	return errors.New("request failed")
}

// isTLSProbeError mirrors Rust RouteFailureClass::TlsError
// (route_aware_client_pool failure_class): TLS handshake failures and
// certificate validation failures, including proxies that answer HTTPS with
// plain HTTP.
func isTLSProbeError(err error) bool {
	if err == nil {
		return false
	}
	var certificateErr *tls.CertificateVerificationError
	if errors.As(err, &certificateErr) {
		return true
	}
	var recordErr *tls.RecordHeaderError
	if errors.As(err, &recordErr) {
		return true
	}
	var unknownAuthority x509.UnknownAuthorityError
	if errors.As(err, &unknownAuthority) {
		return true
	}
	var hostnameErr x509.HostnameError
	if errors.As(err, &hostnameErr) {
		return true
	}
	var invalidCertificateErr x509.CertificateInvalidError
	if errors.As(err, &invalidCertificateErr) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "tls:") ||
		strings.Contains(message, "x509:") ||
		strings.Contains(message, "certificate signed by unknown authority") ||
		strings.Contains(message, "certificate verify") ||
		strings.Contains(message, "handshake failure") ||
		strings.Contains(message, "server gave http response to https client")
}

// isProxyAuthenticationProbeError mirrors Rust
// RouteFailureClass::ProxyAuthenticationRequired: HTTP 407 from the proxy or a
// proxyconnect 407 failure.
func isProxyAuthenticationProbeError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "proxy authentication required") {
		return true
	}
	return strings.Contains(message, "407") && strings.Contains(message, "proxy")
}

// isInvalidProxyConfigProbeError mirrors Rust
// RouteFailureClass::InvalidProxyConfig: malformed proxy URLs or addresses.
func isInvalidProxyConfigProbeError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "invalid proxy") ||
		strings.Contains(message, "malformed proxy") ||
		(strings.Contains(message, "missing port") && strings.Contains(message, "proxy"))
}

// isUnsupportedProxySchemeProbeError mirrors Rust
// RouteFailureClass::UnsupportedProxyScheme.
func isUnsupportedProxySchemeProbeError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unsupported proxy scheme") ||
		strings.Contains(message, "unsupported protocol scheme")
}

// isProxyResolutionProbeError mirrors Rust RouteFailureClass::ResolverError:
// the proxy address itself fails to resolve.
func isProxyResolutionProbeError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	if !strings.Contains(message, "proxy") {
		return false
	}
	return strings.Contains(message, "no such host") ||
		strings.Contains(message, "server misbehaving") ||
		strings.Contains(message, "no route to host")
}

// isConnectProbeError mirrors Rust RouteAwareRequestError::is_connect: TCP
// connection establishment failures.
func isConnectProbeError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "connection refused") ||
		strings.Contains(message, "connection reset") ||
		strings.Contains(message, "connection closed") ||
		strings.Contains(message, "no route to host") ||
		strings.Contains(message, "network is unreachable")
}

func authStoreOptionsForDoctor(codexHome string, opts *Options) (*auth.StoreOptions, error) {
	cfg, err := loadEffectiveConfigForDoctor(codexHome, opts)
	if err != nil {
		return auth.StoreOptionsFromConfig("", false), err
	}
	return auth.StoreOptionsFromConfig(cfg.CLIAuthCredentialsStoreMode(), cfg.SecretAuthStorageEnabled()), nil
}

func loadEffectiveConfigForDoctor(codexHome string, opts *Options) (*config.Config, error) {
	effective := &config.EffectiveOptions{}
	if opts != nil {
		effective.Profile = opts.Root.Shared.Profile
		effective.CWD = opts.Root.Shared.CWD
		effective.RawOverrides = append([]string(nil), opts.Root.ConfigOverrides...)
		effective.EnableFeatures = append([]string(nil), opts.Root.EnableFeatures...)
		effective.DisableFeatures = append([]string(nil), opts.Root.DisableFeatures...)
	}
	return config.LoadEffectiveWithOptions(codexHome, effective)
}

func effectiveProviderIDForDoctor(opts *Options, cfg *config.Config) string {
	if opts != nil {
		if value := strings.TrimSpace(opts.Root.Shared.OSSProvider); value != "" {
			return value
		}
		if opts.Root.Shared.OSS {
			return model.OllamaOSSProviderID
		}
	}
	if value := stringConfigValueForDoctor(cfg, "model_provider"); value != "" {
		return value
	}
	return model.OpenAIProviderID
}

func stringConfigValueForDoctor(cfg *config.Config, key string) string {
	if cfg == nil || cfg.Values == nil {
		return ""
	}
	value, _ := cfg.Values[key].(string)
	return strings.TrimSpace(value)
}

func configValuesForDoctor(cfg *config.Config) map[string]any {
	if cfg == nil || cfg.Values == nil {
		return map[string]any{}
	}
	return cfg.Values
}

func envVarPresent(key string) bool {
	return strings.TrimSpace(os.Getenv(key)) != ""
}

func featureFlagDetails(cfg *config.Config, details *[]string) {
	if details == nil {
		return
	}
	defaults := features.Defaults()
	settings := map[string]bool{}
	if cfg != nil {
		settings = cfg.FeatureSettings()
	}
	enabled := make([]string, 0, len(defaults))
	overrides := make([]string, 0)
	for _, spec := range features.Sorted() {
		effective := features.Enabled(settings, spec.Key)
		if effective {
			enabled = append(enabled, spec.Key)
		}
		if effective != spec.DefaultEnabled {
			overrides = append(overrides, fmt.Sprintf("%s=%t", spec.Key, effective))
		}
	}
	*details = append(*details,
		fmt.Sprintf("feature flags enabled: %d", len(enabled)),
		"enabled feature flags: "+doctorDisplayList(enabled),
		"feature flag overrides: "+doctorDisplayList(overrides),
	)
	if cfg != nil {
		for _, usage := range cfg.LegacyFeatureUsages() {
			*details = append(*details, fmt.Sprintf("legacy feature flag: %s -> %s", usage.Alias, usage.Feature))
		}
	}
}

func doctorDisplayList(items []string) string {
	if len(items) == 0 {
		return "none"
	}
	return strings.Join(items, ", ")
}

const versionCacheFileName = "version.json"

type versionCacheInfo struct {
	LatestVersion    string `json:"latest_version"`
	LastCheckedAt    string `json:"last_checked_at"`
	DismissedVersion string `json:"dismissed_version"`
}

type npmRootCheckKind string

const (
	npmRootCheckMatch              npmRootCheckKind = "match"
	npmRootCheckMismatch           npmRootCheckKind = "mismatch"
	npmRootCheckMissingPackageRoot npmRootCheckKind = "missing_package_root"
	npmRootCheckNpmUnavailable     npmRootCheckKind = "npm_unavailable"
)

type npmRootCheck struct {
	Kind               npmRootCheckKind
	PackageRoot        string
	RunningPackageRoot string
	NPMPackageRoot     string
	Error              string
}

var runNPMRootCommandForDoctor = defaultRunNPMRootCommandForDoctor
var runCodexPathEntriesCommandForDoctor = defaultRunCodexPathEntriesCommandForDoctor

func (b *Builder) updatesCheck(codexHome string, opts *Options) *DoctorCheck {
	cfg, err := loadEffectiveConfigForDoctor(codexHome, opts)
	if err != nil {
		return NewCheck("updates.status", "updates", CheckStatusWarning, "update config could not be loaded").
			Detail("update config error: " + err.Error())
	}
	exe := ""
	if b != nil && b.currentExe != nil {
		if path, exeErr := b.currentExe(); exeErr == nil {
			exe = path
		}
	}
	if exe == "" {
		if path, exeErr := os.Executable(); exeErr == nil {
			exe = path
		}
	}
	installContext := doctorInstallContextForDoctor(exe, codexHome)
	details := []string{
		fmt.Sprintf("check for update on startup: %t", checkForUpdateOnStartup(cfg)),
		"update action: " + updateActionLabel(installContext),
	}
	pushCachedVersionDetails(&details, filepath.Join(codexHome, versionCacheFileName))

	status := CheckStatusOK
	summary := "update configuration is locally consistent"
	var remediation string
	if doctorManagedByNPM(exe) {
		switch rootCheck := npmGlobalRootCheckForDoctor(); rootCheck.Kind {
		case npmRootCheckMatch:
			details = append(details, "npm update target: "+rootCheck.PackageRoot)
		case npmRootCheckMismatch:
			status = CheckStatusFail
			summary = "update would target a different npm install"
			details = append(details,
				"running package root: "+rootCheck.RunningPackageRoot,
				"npm package root: "+rootCheck.NPMPackageRoot,
			)
			remediation = fmt.Sprintf("Fix PATH or npm prefix so the running package root (%s) matches the npm global package root (%s).", rootCheck.RunningPackageRoot, rootCheck.NPMPackageRoot)
		case npmRootCheckMissingPackageRoot:
			status = maxCheckStatus(status, CheckStatusWarning)
			summary = "npm update target could not be proven"
			remediation = "Reinstall or update Codex so the JS shim provides CODEX_MANAGED_PACKAGE_ROOT."
		case npmRootCheckNpmUnavailable:
			status = maxCheckStatus(status, CheckStatusWarning)
			summary = "npm update target could not be inspected"
			details = append(details, "npm root -g failed: "+rootCheck.Error)
		}
	}
	ctx, cancel := contextWithTimeoutForDoctor(5 * time.Second)
	defer cancel()
	result, err := install.CheckForUpdate(ctx, &install.UpdateCheckOptions{
		Context:        installContext,
		CurrentVersion: Version(),
		HTTPClient:     b.providerHTTPClient(),
	})
	if result != nil {
		if result.LatestVersion != "" {
			details = append(details, "latest version: "+result.LatestVersion)
		}
		if result.Status != "" {
			details = append(details, "latest version status: "+latestVersionStatusText(result.Status))
		}
	}
	if err != nil {
		status = maxCheckStatus(status, CheckStatusWarning)
		details = append(details, "latest version probe: "+err.Error())
	}
	check := NewCheck("updates.status", "updates", status, summary).DetailsList(details)
	if remediation != "" {
		check.Remediate(remediation)
	}
	return check
}

func contextWithTimeoutForDoctor(timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), timeout)
}

func checkForUpdateOnStartup(cfg *config.Config) bool {
	if cfg == nil || cfg.Values == nil {
		return true
	}
	value, ok := cfg.Values["check_for_update_on_startup"].(bool)
	if !ok {
		return true
	}
	return value
}

func updateActionLabel(context *install.InstallContext) string {
	action := install.ActionFromContext(context)
	if action == nil {
		return "manual or unknown"
	}
	return action.CommandLine()
}

func pushCachedVersionDetails(details *[]string, path string) {
	*details = append(*details, "version cache: "+path)
	bytes, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			*details = append(*details, "version cache: missing")
		} else {
			*details = append(*details, "version cache read: "+err.Error())
		}
		return
	}
	var info versionCacheInfo
	if err := json.Unmarshal(bytes, &info); err != nil {
		*details = append(*details, "version cache parse: "+err.Error())
		return
	}
	if strings.TrimSpace(info.LatestVersion) != "" {
		*details = append(*details, "cached latest version: "+strings.TrimSpace(info.LatestVersion))
	}
	if strings.TrimSpace(info.LastCheckedAt) != "" {
		*details = append(*details, "last checked at: "+strings.TrimSpace(info.LastCheckedAt))
	}
	if strings.TrimSpace(info.DismissedVersion) != "" {
		*details = append(*details, "dismissed version: "+strings.TrimSpace(info.DismissedVersion))
	}
}

func latestVersionStatusText(status install.UpdateStatus) string {
	switch status {
	case install.UpdateStatusUpdateAvailable:
		return "newer version is available"
	case install.UpdateStatusUpToDate, install.UpdateStatusUpdated:
		return "current version is not older"
	case install.UpdateStatusUnknown:
		return "unknown"
	default:
		return string(status)
	}
}

func managedEnvSetForDoctor(name string) bool {
	_, ok := os.LookupEnv(name)
	return ok
}

func doctorManagedByNPM(currentExe string) bool {
	return managedEnvSetForDoctor("CODEX_MANAGED_BY_NPM") && !inheritedManagedEnvForCargoBinary(currentExe)
}

func inheritedManagedEnvForCargoBinary(currentExe string) bool {
	if !managedEnvSetForDoctor("CODEX_MANAGED_BY_NPM") && !managedEnvSetForDoctor("CODEX_MANAGED_BY_BUN") && !managedEnvSetForDoctor("CODEX_MANAGED_BY_PNPM") {
		return false
	}
	if strings.TrimSpace(currentExe) == "" {
		return false
	}
	parts := strings.FieldsFunc(filepath.ToSlash(filepath.Clean(currentExe)), func(r rune) bool { return r == '/' })
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "target" && (parts[i+1] == "debug" || parts[i+1] == "release") {
			return true
		}
	}
	return false
}

func npmGlobalRootCheckForDoctor() npmRootCheck {
	runningPackageRoot, ok := os.LookupEnv("CODEX_MANAGED_PACKAGE_ROOT")
	if !ok {
		return npmRootCheck{Kind: npmRootCheckMissingPackageRoot}
	}
	output, err := runNPMRootCommandForDoctor()
	if err != nil {
		return npmRootCheck{Kind: npmRootCheckNpmUnavailable, Error: err.Error()}
	}
	npmRoot := ""
	for _, line := range strings.Split(output, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			npmRoot = trimmed
			break
		}
	}
	if npmRoot == "" {
		return npmRootCheck{Kind: npmRootCheckNpmUnavailable, Error: "empty output from npm root -g"}
	}
	return compareNpmPackageRootsForDoctor(runningPackageRoot, npmRoot)
}

func codexPathEntriesForDoctor() []string {
	output, err := runCodexPathEntriesCommandForDoctor()
	if err != nil {
		return nil
	}
	entries := []string{}
	for _, line := range strings.Split(output, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			entries = append(entries, trimmed)
		}
	}
	return entries
}

func defaultRunCodexPathEntriesCommandForDoctor() (string, error) {
	program := "which"
	args := []string{"-a", "codex"}
	if runtime.GOOS == "windows" {
		program = "where"
		args = []string{"codex"}
	}
	return runDoctorCommandOutput(program, args...)
}

func compareNpmPackageRootsForDoctor(runningPackageRoot string, npmRoot string) npmRootCheck {
	npmPackageRoot := filepath.Join(npmRoot, "@jacks001314", "codex-go")
	if normalizePathForDoctorCompare(runningPackageRoot) == normalizePathForDoctorCompare(npmPackageRoot) {
		return npmRootCheck{Kind: npmRootCheckMatch, PackageRoot: npmPackageRoot}
	}
	return npmRootCheck{
		Kind:               npmRootCheckMismatch,
		RunningPackageRoot: runningPackageRoot,
		NPMPackageRoot:     npmPackageRoot,
	}
}

func normalizePathForDoctorCompare(path string) string {
	normalized := filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(normalized); err == nil {
		normalized = resolved
	}
	normalized = filepath.ToSlash(normalized)
	if runtime.GOOS == "windows" {
		normalized = strings.ToLower(normalized)
	}
	return normalized
}

func defaultRunNPMRootCommandForDoctor() (string, error) {
	program := "npm"
	if runtime.GOOS == "windows" {
		program = "npm.cmd"
	}
	return runDoctorCommandOutput(program, "root", "-g")
}

func runDoctorCommandOutput(program string, args ...string) (string, error) {
	output, err := exec.Command(program, args...).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			stderr := strings.TrimSpace(string(exitErr.Stderr))
			if stderr != "" {
				return "", fmt.Errorf("%s", stderr)
			}
		}
		return "", err
	}
	return string(output), nil
}

func pushEnvPathDetailForDoctor(details *[]string, label string, name string) {
	value, ok := os.LookupEnv(name)
	if !ok {
		*details = append(*details, label+": not set")
		return
	}
	*details = append(*details, label+": "+value)
}

func maxCheckStatus(current CheckStatus, next CheckStatus) CheckStatus {
	if current == CheckStatusFail || next == CheckStatusFail {
		return CheckStatusFail
	}
	if current == CheckStatusWarning || next == CheckStatusWarning {
		return CheckStatusWarning
	}
	return CheckStatusOK
}

func mcpCheck(codexHome string, opts *Options) *DoctorCheck {
	cfg, err := loadEffectiveConfigForDoctor(codexHome, opts)
	if err != nil {
		return NewCheck("mcp.config", "mcp", CheckStatusWarning, "MCP config could not be loaded").
			Detail("MCP config error: " + err.Error())
	}
	return mcpCheckFromServers(mcpServersFromConfig(cfg), currentEnvMap(), doctorCWD(opts))
}

func mcpCheckFromServers(servers map[string]mcp.ServerConfig, env map[string]string, cwd string) *DoctorCheck {
	if len(servers) == 0 {
		return NewCheck("mcp.config", "mcp", CheckStatusOK, "no MCP servers configured")
	}
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)

	disabled := 0
	stdioCount := 0
	httpCount := 0
	missingInputs := []string{}
	unreachableRequiredHTTP := []string{}
	unreachableOptionalHTTP := []string{}
	for _, name := range names {
		server := servers[name]
		disabledServer := !server.Enabled || strings.TrimSpace(server.DisabledReason) != ""
		if disabledServer {
			disabled++
		}
		if strings.TrimSpace(server.URL) != "" {
			httpCount++
			if disabledServer {
				continue
			}
			missingInputs = append(missingInputs, missingMCPHTTPInputs(name, &server, env)...)
			if _, err := mcpHTTPProbeURL(server.URL); err != nil {
				detail := fmt.Sprintf("%s: %s (%s)", name, server.URL, err.Error())
				if server.Required {
					unreachableRequiredHTTP = append(unreachableRequiredHTTP, detail)
				} else {
					unreachableOptionalHTTP = append(unreachableOptionalHTTP, detail)
				}
			}
			continue
		}
		stdioCount++
		if disabledServer {
			continue
		}
		missingInputs = append(missingInputs, missingMCPStdioInputs(name, &server, env)...)
		command := strings.TrimSpace(server.Command)
		if command == "" || !mcpServerIsLocalEnvironment(&server) {
			continue
		}
		if err := stdioCommandResolves(command, server.CWD, server.Env); err != nil {
			issue := fmt.Sprintf("%s: stdio command %q is not resolvable (%v)", name, server.Command, err)
			missingInputs = append(missingInputs, issue)
		}
	}

	details := []string{
		fmt.Sprintf("configured servers: %d", len(servers)),
		fmt.Sprintf("disabled servers: %d", disabled),
	}
	if stdioCount > 0 {
		details = append(details, fmt.Sprintf("stdio servers: %d", stdioCount))
	}
	if httpCount > 0 {
		details = append(details, fmt.Sprintf("streamable_http servers: %d", httpCount))
	}
	details = append(details, missingInputs...)
	for _, detail := range unreachableRequiredHTTP {
		details = append(details, "required reachability failed: "+detail)
	}
	for _, detail := range unreachableOptionalHTTP {
		details = append(details, "optional reachability failed: "+detail)
	}
	requiredMissing := false
	for _, name := range names {
		if !servers[name].Required {
			continue
		}
		prefix := name + ":"
		for _, missing := range missingInputs {
			if strings.HasPrefix(missing, prefix) {
				requiredMissing = true
				break
			}
		}
	}
	status := CheckStatusOK
	summary := "MCP configuration is locally consistent"
	if requiredMissing || len(unreachableRequiredHTTP) > 0 {
		status = CheckStatusFail
		summary = "MCP configuration has failing required inputs or reachability"
	} else if len(missingInputs) > 0 || len(unreachableOptionalHTTP) > 0 {
		status = CheckStatusWarning
		summary = "MCP configuration has optional issues"
	}
	check := NewCheck("mcp.config", "mcp", status, summary).DetailsList(details)
	if status != CheckStatusOK {
		check.Remediate("Set the missing MCP env vars or disable the affected server.")
	}
	return check
}

func defaultMCPHTTPProbeURL(rawURL string) (string, error) {
	client := &http.Client{Timeout: defaultProviderReachabilityTimeout}
	ctx := context.Background()
	status, headErr := httpProbeURL(ctx, client, rawURL)
	if headErr == nil {
		return status, nil
	}
	status, getErr := httpProbeURLWithMethod(ctx, client, http.MethodGet, rawURL)
	if getErr == nil {
		return status, nil
	}
	return "", fmt.Errorf("HEAD %s; GET %s", headErr.Error(), getErr.Error())
}

func missingMCPHTTPInputs(name string, server *mcp.ServerConfig, env map[string]string) []string {
	missing := []string{}
	if server == nil {
		return missing
	}
	if key := strings.TrimSpace(server.BearerTokenEnvVar); key != "" && strings.TrimSpace(env[key]) == "" {
		missing = append(missing, fmt.Sprintf("%s: bearer token env var %s is not set", name, key))
	}
	headerEnvVars := make([]string, 0, len(server.EnvHTTPHeaders))
	for _, envVar := range server.EnvHTTPHeaders {
		envVar = strings.TrimSpace(envVar)
		if envVar != "" {
			headerEnvVars = append(headerEnvVars, envVar)
		}
	}
	sort.Strings(headerEnvVars)
	for _, envVar := range headerEnvVars {
		if strings.TrimSpace(env[envVar]) == "" {
			missing = append(missing, fmt.Sprintf("%s: header env var %s is not set", name, envVar))
		}
	}
	return missing
}

func missingMCPStdioInputs(name string, server *mcp.ServerConfig, env map[string]string) []string {
	missing := []string{}
	if server == nil {
		return missing
	}
	localServer := mcpServerIsLocalEnvironment(server)
	if strings.TrimSpace(server.Command) == "" {
		missing = append(missing, fmt.Sprintf("%s: stdio command is empty", name))
	}
	for key := range server.Env {
		if strings.TrimSpace(key) == "" {
			missing = append(missing, fmt.Sprintf("%s: empty env key %s", name, key))
		}
	}
	if !localServer {
		serverCWD := strings.TrimSpace(server.CWD)
		if serverCWD == "" {
			missing = append(missing, fmt.Sprintf("%s: remote stdio requires an explicit cwd", name))
		} else if !mcpRemoteCWDIsAbsolute(serverCWD) {
			missing = append(missing, fmt.Sprintf("%s: remote stdio cwd is not absolute (%s)", name, serverCWD))
		}
	} else if serverCWD := strings.TrimSpace(server.CWD); serverCWD != "" {
		if _, err := os.Stat(serverCWD); err != nil {
			missing = append(missing, fmt.Sprintf("%s: cwd does not exist (%s)", name, serverCWD))
		}
	}
	for _, envVar := range server.EnvVars {
		envName := strings.TrimSpace(envVar.Name)
		if envName == "" {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(envVar.Source), "remote") {
			if localServer {
				missing = append(missing, fmt.Sprintf("%s: env_vars entry `%s` uses source `remote`, which requires remote MCP stdio", name, envName))
			}
			continue
		}
		if strings.TrimSpace(env[envName]) == "" {
			missing = append(missing, fmt.Sprintf("%s: env var %s is not set", name, envName))
		}
	}
	return missing
}

func mcpServerIsLocalEnvironment(server *mcp.ServerConfig) bool {
	if server == nil {
		return true
	}
	environmentID := strings.TrimSpace(server.EnvironmentID)
	return environmentID == "" || environmentID == "local"
}

func stdioCommandResolves(command string, cwd string, serverEnv map[string]string) error {
	commandPath := filepath.Clean(command)
	if filepath.IsAbs(commandPath) {
		return executablePathExists(commandPath)
	}
	if hasPathComponents(command) {
		base := strings.TrimSpace(cwd)
		if base == "" {
			if current, err := os.Getwd(); err == nil {
				base = current
			} else {
				base = "."
			}
		}
		return executablePathExists(filepath.Join(base, command))
	}
	pathEnv := ""
	if serverEnv != nil {
		pathEnv = serverEnv["PATH"]
	}
	if pathEnv == "" {
		pathEnv = os.Getenv("PATH")
	}
	if pathEnv == "" {
		return fmt.Errorf("PATH is not set")
	}
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, command)
		if executablePathExists(candidate) == nil {
			return nil
		}
		if runtime.GOOS == "windows" && filepath.Ext(command) == "" {
			for _, ext := range windowsPathExts() {
				if executablePathExists(candidate+ext) == nil {
					return nil
				}
			}
		}
	}
	return fmt.Errorf("not found on PATH")
}

func hasPathComponents(command string) bool {
	return strings.ContainsAny(command, `/\`)
}

func executablePathExists(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("path is not a file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%s is not executable", path)
	}
	return nil
}

func windowsPathExts() []string {
	value := os.Getenv("PATHEXT")
	if value == "" {
		value = ".COM;.EXE;.BAT;.CMD"
	}
	parts := strings.Split(value, ";")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func mcpRemoteCWDIsAbsolute(cwd string) bool {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return false
	}
	return strings.HasPrefix(cwd, "/") ||
		strings.HasPrefix(cwd, `\\`) ||
		windowsAbsolutePath(cwd)
}

func windowsAbsolutePath(path string) bool {
	if len(path) < 3 {
		return false
	}
	drive := path[0]
	return ((drive >= 'A' && drive <= 'Z') || (drive >= 'a' && drive <= 'z')) &&
		path[1] == ':' &&
		(path[2] == '\\' || path[2] == '/')
}

func mcpServersFromConfig(cfg *config.Config) map[string]mcp.ServerConfig {
	out := map[string]mcp.ServerConfig{}
	if cfg == nil || cfg.Values == nil {
		return out
	}
	rawServers, ok := cfg.Values["mcp_servers"].(map[string]any)
	if !ok {
		return out
	}
	for name, raw := range rawServers {
		table, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		server := mcpServerConfigFromValues(name, table)
		out[name] = *server
	}
	applyMCPServerRequirementsForDoctor(out, mcpServerRequirementsFromConfigForDoctor(cfg), "requirements (config)")
	return out
}

func mcpServerConfigFromValues(name string, table map[string]any) *mcp.ServerConfig {
	_ = name
	server := &mcp.ServerConfig{Enabled: true}
	if enabled, ok := table["enabled"].(bool); ok {
		server.Enabled = enabled
	}
	if required, ok := table["required"].(bool); ok {
		server.Required = required
	}
	server.DisabledReason = stringConfigValueFromAny(table["disabled_reason"])
	server.EnvironmentID = stringConfigValueFromAny(table["environment_id"])
	if url := stringConfigValueFromAny(table["url"]); url != "" {
		server.URL = url
		server.BearerTokenEnvVar = stringConfigValueFromAny(table["bearer_token_env_var"])
		server.HTTPHeaders = stringMapConfigValueFromAny(table["http_headers"])
		server.EnvHTTPHeaders = stringMapConfigValueFromAny(table["env_http_headers"])
		server.OAuthClientID = stringConfigValueFromAny(table["oauth_client_id"])
		server.OAuthResource = stringConfigValueFromAny(table["oauth_resource"])
		return server
	}
	server.Command = stringConfigValueFromAny(table["command"])
	server.Args = stringSliceConfigValueFromAny(table["args"])
	server.Env = stringMapConfigValueFromAny(table["env"])
	server.EnvVars = mcpEnvVarsConfigValueFromAny(table["env_vars"])
	server.CWD = stringConfigValueFromAny(table["cwd"])
	return server
}

type mcpServerRequirementForDoctor struct {
	CommandExact   string
	URLExact       string
	CommandMatcher *mcpCommandRequirementForDoctor
	URLMatcher     *mcpValueMatcherForDoctor
}

type mcpCommandRequirementForDoctor struct {
	Executable string
	Args       []mcpValueMatcherForDoctor
}

type mcpValueMatcherForDoctor struct {
	Kind       string
	Value      string
	Expression string
}

func mcpServerRequirementsFromConfigForDoctor(cfg *config.Config) map[string]mcpServerRequirementForDoctor {
	if cfg == nil || cfg.Values == nil {
		return nil
	}
	requirements, ok := cfg.Values["requirements"].(map[string]any)
	if !ok {
		return nil
	}
	rawServers, ok := requirements["mcp_servers"].(map[string]any)
	if !ok {
		rawServers, ok = requirements["mcpServers"].(map[string]any)
	}
	if !ok {
		return nil
	}
	out := make(map[string]mcpServerRequirementForDoctor, len(rawServers))
	for name, raw := range rawServers {
		table, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		requirement, ok := mcpServerRequirementFromTableForDoctor(table)
		if ok {
			out[name] = requirement
		}
	}
	return out
}

func mcpServerRequirementFromTableForDoctor(table map[string]any) (mcpServerRequirementForDoctor, bool) {
	identity, ok := table["identity"].(map[string]any)
	if !ok {
		return mcpServerRequirementForDoctor{}, false
	}
	if rawCommand, ok := identity["command"]; ok {
		switch command := rawCommand.(type) {
		case string:
			command = strings.TrimSpace(command)
			if command != "" {
				return mcpServerRequirementForDoctor{CommandExact: command}, true
			}
		case map[string]any:
			matcher := mcpCommandRequirementFromTableForDoctor(command)
			if matcher.Executable != "" {
				return mcpServerRequirementForDoctor{CommandMatcher: &matcher}, true
			}
		}
	}
	if rawURL, ok := identity["url"]; ok {
		switch urlValue := rawURL.(type) {
		case string:
			urlValue = strings.TrimSpace(urlValue)
			if urlValue != "" {
				return mcpServerRequirementForDoctor{URLExact: urlValue}, true
			}
		case map[string]any:
			matcher, ok := mcpValueMatcherFromTableForDoctor(urlValue)
			if ok {
				return mcpServerRequirementForDoctor{URLMatcher: &matcher}, true
			}
		}
	}
	return mcpServerRequirementForDoctor{}, false
}

func mcpCommandRequirementFromTableForDoctor(table map[string]any) mcpCommandRequirementForDoctor {
	matcher := mcpCommandRequirementForDoctor{
		Executable: stringConfigValueFromAny(table["executable"]),
	}
	if args, ok := table["args"].([]any); ok {
		for _, raw := range args {
			argTable, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			argMatcher, ok := mcpValueMatcherFromTableForDoctor(argTable)
			if ok {
				matcher.Args = append(matcher.Args, argMatcher)
			}
		}
	}
	return matcher
}

func mcpValueMatcherFromTableForDoctor(table map[string]any) (mcpValueMatcherForDoctor, bool) {
	kind := stringConfigValueFromAny(table["match"])
	switch kind {
	case "exact", "prefix":
		value := stringConfigValueFromAny(table["value"])
		if value == "" {
			return mcpValueMatcherForDoctor{}, false
		}
		return mcpValueMatcherForDoctor{Kind: kind, Value: value}, true
	case "regex":
		expression := stringConfigValueFromAny(table["expression"])
		if expression == "" {
			return mcpValueMatcherForDoctor{}, false
		}
		return mcpValueMatcherForDoctor{Kind: kind, Expression: expression}, true
	default:
		return mcpValueMatcherForDoctor{}, false
	}
}

func applyMCPServerRequirementsForDoctor(servers map[string]mcp.ServerConfig, requirements map[string]mcpServerRequirementForDoctor, disabledReason string) {
	if requirements == nil {
		return
	}
	for name, server := range servers {
		requirement, ok := requirements[name]
		if ok && requirement.matches(&server) {
			server.DisabledReason = ""
		} else {
			server.Enabled = false
			server.DisabledReason = disabledReason
		}
		servers[name] = server
	}
}

func (r mcpServerRequirementForDoctor) matches(server *mcp.ServerConfig) bool {
	if server == nil {
		return false
	}
	if strings.TrimSpace(server.URL) != "" {
		if r.URLExact != "" {
			return server.URL == r.URLExact
		}
		if r.URLMatcher != nil {
			return r.URLMatcher.matches(server.URL)
		}
		return false
	}
	if r.CommandExact != "" {
		return server.Command == r.CommandExact
	}
	if r.CommandMatcher != nil {
		if server.Command != r.CommandMatcher.Executable || len(server.Args) != len(r.CommandMatcher.Args) {
			return false
		}
		for index, matcher := range r.CommandMatcher.Args {
			if !matcher.matches(server.Args[index]) {
				return false
			}
		}
		return true
	}
	return false
}

func (m mcpValueMatcherForDoctor) matches(candidate string) bool {
	switch m.Kind {
	case "exact":
		return candidate == m.Value
	case "prefix":
		return strings.HasPrefix(candidate, m.Value)
	case "regex":
		regex, err := regexp.Compile("^(?:" + m.Expression + ")$")
		return err == nil && regex.MatchString(candidate)
	default:
		return false
	}
}

func stringConfigValueFromAny(value any) string {
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return ""
}

func stringSliceConfigValueFromAny(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func stringMapConfigValueFromAny(value any) map[string]string {
	table, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]string{}
	for key, raw := range table {
		if text, ok := raw.(string); ok {
			out[key] = text
		}
	}
	return out
}

func mcpEnvVarsConfigValueFromAny(value any) []mcp.EnvVar {
	switch typed := value.(type) {
	case []mcp.EnvVar:
		return append([]mcp.EnvVar(nil), typed...)
	case []string:
		out := make([]mcp.EnvVar, 0, len(typed))
		for _, name := range typed {
			if name = strings.TrimSpace(name); name != "" {
				out = append(out, mcp.EnvVar{Name: name})
			}
		}
		return out
	case []any:
		out := make([]mcp.EnvVar, 0, len(typed))
		for _, item := range typed {
			switch entry := item.(type) {
			case string:
				if name := strings.TrimSpace(entry); name != "" {
					out = append(out, mcp.EnvVar{Name: name})
				}
			case map[string]any:
				name := stringConfigValueFromAny(entry["name"])
				if name == "" {
					continue
				}
				out = append(out, mcp.EnvVar{Name: name, Source: stringConfigValueFromAny(entry["source"])})
			case map[string]string:
				name := strings.TrimSpace(entry["name"])
				if name == "" {
					continue
				}
				out = append(out, mcp.EnvVar{Name: name, Source: strings.TrimSpace(entry["source"])})
			}
		}
		return out
	default:
		return nil
	}
}

func sandboxCheck(codexHome string, opts *Options) *DoctorCheck {
	cfg, err := loadEffectiveConfigForDoctor(codexHome, opts)
	if err != nil {
		return NewCheck("sandbox.helpers", "sandbox", CheckStatusWarning, "sandbox config could not be loaded").
			Detail("sandbox config error: " + err.Error())
	}
	check := sandboxCheckFromConfig(cfg, doctorDispatchPaths(opts))
	check = applyWindowsSandboxDoctorDiagnostics(check, cfg, codexHome, doctorCWD(opts))
	return check
}

// applyWindowsSandboxDoctorDiagnostics reports the configured Windows sandbox
// backend, whether denied-read restrictions are active, and any recorded
// elevated-sandbox setup failure (Rust #39290).
func applyWindowsSandboxDoctorDiagnostics(check *DoctorCheck, cfg *config.Config, codexHome string, cwd string) *DoctorCheck {
	if check == nil {
		check = NewCheck("sandbox.helpers", "sandbox", CheckStatusOK, "sandbox configuration is readable")
	}
	values := configValuesForDoctor(cfg)
	level := strings.TrimSpace(stringConfigValueFromAny(values["windows_sandbox"]))
	if level == "" {
		level = "default"
	}
	check = check.Detail("windows sandbox level: " + level)
	restrictions := "inactive"
	if resolution, resolveErr := cfg.ResolveSandboxPermissionProfile("", cwd); resolveErr == nil && resolution != nil && resolution.Profile != nil && resolution.Profile.HasDenyReadEntries() {
		restrictions = "active"
	}
	check = check.Detail("denied-read restrictions: " + restrictions)
	if report, readErr := windowssandbox.ReadSetupErrorReport(codexHome); readErr == nil && report != nil {
		message := strings.TrimSpace(report.Message)
		if message == "" {
			message = string(report.Code)
		}
		check.Status = CheckStatusWarning
		check.Summary = "Windows sandbox setup failed"
		check = check.
			Detail("windows sandbox setup error: " + message).
			Remediate("Re-run the Windows sandbox setup or restart the elevated sandbox service; contact your administrator if the failure persists.")
	}
	return check
}

func sandboxCheckFromConfig(cfg *config.Config, dispatchPaths *cli.DispatchPaths) *DoctorCheck {
	policy := sandboxPolicyFromConfig(cfg)
	details := []string{
		"approval policy: " + approvalPolicyFromConfig(cfg),
		"filesystem sandbox: " + string(policy.Kind),
		"network sandbox: " + networkSandboxPolicyFromConfig(policy),
	}
	status := CheckStatusOK
	summary := "sandbox configuration is readable"
	linuxHelper := ""
	execveWrapper := ""
	if dispatchPaths != nil {
		linuxHelper = dispatchPaths.CodexLinuxSandboxExe
		execveWrapper = dispatchPaths.MainExecveWrapperExe
	}
	pushSandboxHelperDetail(&details, "codex-linux-sandbox helper", linuxHelper)
	pushSandboxHelperDetail(&details, "execve wrapper helper", execveWrapper)
	if strings.TrimSpace(linuxHelper) != "" && !doctorPathExists(linuxHelper) {
		status = CheckStatusWarning
		summary = "Linux sandbox helper path does not exist"
	}
	return NewCheck("sandbox.helpers", "sandbox", status, summary).DetailsList(details)
}

func doctorDispatchPaths(opts *Options) *cli.DispatchPaths {
	if opts == nil || opts.DispatchPaths == nil {
		return nil
	}
	paths := *opts.DispatchPaths
	return &paths
}

func sandboxPolicyFromConfig(cfg *config.Config) *sandbox.SandboxPolicy {
	values := configValuesForDoctor(cfg)
	modeText := stringConfigValueFromAny(values["sandbox_mode"])
	mode := sandbox.SandboxReadOnly
	if modeText != "" {
		if parsed, err := sandbox.ParseSandboxMode(modeText); err == nil {
			mode = parsed
		}
	}
	policy, err := sandbox.SandboxPolicyFromMode(mode)
	if err != nil {
		policy = sandbox.NewReadOnlyPolicy()
	}
	if workspace, ok := values["sandbox_workspace_write"].(map[string]any); ok && policy.Kind == sandbox.SandboxWorkspaceWrite {
		policy.WritableRoots = stringSliceConfigValueFromAny(workspace["writable_roots"])
		if network, ok := workspace["network_access"].(bool); ok {
			policy.NetworkAccess = network
		}
		if exclude, ok := workspace["exclude_tmpdir_env_var"].(bool); ok {
			policy.ExcludeTmpdirEnvVar = exclude
		}
		if exclude, ok := workspace["exclude_slash_tmp"].(bool); ok {
			policy.ExcludeSlashTmp = exclude
		}
	}
	return policy
}

func approvalPolicyFromConfig(cfg *config.Config) string {
	value := stringConfigValueFromAny(configValuesForDoctor(cfg)["approval_policy"])
	switch value {
	case "", string(sandbox.ApprovalOnRequest), "on-failure":
		return "OnRequest"
	case string(sandbox.ApprovalNever):
		return "Never"
	case string(sandbox.ApprovalUnlessTrusted):
		return "UnlessTrusted"
	case string(sandbox.ApprovalGranular):
		return "Granular"
	default:
		return value
	}
}

func networkSandboxPolicyFromConfig(policy *sandbox.SandboxPolicy) string {
	if policy != nil && policy.HasFullNetworkAccess() {
		return "enabled"
	}
	return "restricted"
}

func pushSandboxHelperDetail(details *[]string, label string, path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		*details = append(*details, label+": none")
		return
	}
	*details = append(*details, label+": "+path)
}

func doctorPathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

type backgroundSocketStatus string

const (
	backgroundSocketNotRunning         backgroundSocketStatus = "not running"
	backgroundSocketRunning            backgroundSocketStatus = "running"
	backgroundSocketStaleOrUnreachable backgroundSocketStatus = "stale or unreachable"
)

type backgroundSocketProbe struct {
	Status  backgroundSocketStatus
	Version string
	Error   string
}

func backgroundServerCheck(codexHome string) *DoctorCheck {
	paths := appserverdaemon.PathsForCodexHome(codexHome)
	stateDir := filepath.Join(codexHome, appserverdaemon.StateDirName)
	details := []string{"daemon state dir: " + stateDir}
	pushDoctorFileDetail(&details, "settings", paths.SettingsFile)
	pushDoctorFileDetail(&details, "pid file", paths.PIDFile)
	pushDoctorFileDetail(&details, "update-loop pid file", paths.UpdatePIDFile)
	details = append(details, "control socket: "+paths.SocketPath)
	status := backgroundSocketStatusForPath(paths.SocketPath)
	details = append(details, "status: "+string(status.Status))
	if status.Version != "" {
		details = append(details, "app-server version: "+status.Version)
	} else if status.Error != "" {
		details = append(details, "app-server version: unavailable ("+status.Error+")")
	}
	details = append(details, "mode: "+backgroundServerMode(paths.SettingsFile))

	checkStatus := CheckStatusOK
	summary := "background server is not running"
	if status.Status == backgroundSocketRunning {
		summary = "background server is running"
	} else if status.Status == backgroundSocketStaleOrUnreachable {
		checkStatus = CheckStatusWarning
		summary = "background server socket is stale or unreachable"
	}
	check := NewCheck("app_server.status", "app-server", checkStatus, summary).DetailsList(details)
	if checkStatus == CheckStatusWarning {
		check.Remediate("Run codex app-server daemon version for more details.")
	}
	return check
}

func pushDoctorFileDetail(details *[]string, label string, path string) {
	info, err := os.Stat(path)
	switch {
	case err == nil && info.Mode().IsRegular():
		*details = append(*details, fmt.Sprintf("%s: %s (file)", label, path))
	case err == nil:
		*details = append(*details, fmt.Sprintf("%s: %s (not a file)", label, path))
	case errors.Is(err, os.ErrNotExist):
		*details = append(*details, fmt.Sprintf("%s: %s (missing)", label, path))
	default:
		*details = append(*details, fmt.Sprintf("%s: %s (%v)", label, path, err))
	}
}

func backgroundSocketStatusForPath(path string) backgroundSocketProbe {
	_, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return backgroundSocketProbe{Status: backgroundSocketNotRunning}
	}
	if err != nil {
		return backgroundSocketProbe{Status: backgroundSocketStaleOrUnreachable, Error: conciseBackgroundProbeError(err, path)}
	}
	version, err := probeBackgroundAppServerVersion(path, appserverdaemon.ControlSocketProbeTimeout)
	if err != nil {
		return backgroundSocketProbe{Status: backgroundSocketStaleOrUnreachable, Error: conciseBackgroundProbeError(err, path)}
	}
	return backgroundSocketProbe{Status: backgroundSocketRunning, Version: version}
}

func conciseBackgroundProbeError(err error, socketPath string) string {
	if err == nil {
		return "unknown error"
	}
	message := strings.ReplaceAll(err.Error(), socketPath, "control socket")
	message = strings.Join(strings.Fields(message), " ")
	if message == "" {
		return "unknown error"
	}
	runes := []rune(message)
	if len(runes) > maxBackgroundProbeErrorChars {
		return string(runes[:maxBackgroundProbeErrorChars]) + "..."
	}
	return message
}

func backgroundServerMode(settingsPath string) string {
	info, err := os.Stat(settingsPath)
	if err == nil && info.Mode().IsRegular() {
		return "persistent"
	}
	return "ephemeral"
}

func networkCheck(codexHome string, opts *Options) *DoctorCheck {
	env := currentEnvMap()
	cfg, _ := loadEffectiveConfigForDoctor(codexHome, opts)
	presentProxyVars := []string{}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy"} {
		if value, ok := env[key]; ok && strings.TrimSpace(value) != "" {
			presentProxyVars = append(presentProxyVars, key)
		}
	}
	details := []string{}
	if len(presentProxyVars) == 0 {
		details = append(details, "proxy env vars: none")
	} else {
		details = append(details, "proxy env vars present: "+strings.Join(presentProxyVars, ", "))
	}
	if cfg != nil {
		// Rust network::check reports the effective proxy policy even when no
		// proxy env var is set, so users can see why a configured proxy is or
		// is not used. macOS system proxy state stays platform-gated (N/A on
		// the Windows host; documented in doctor output).
		respectSystemProxy := "disabled"
		if cfg.RespectSystemProxyEnabled() {
			respectSystemProxy = "enabled"
		}
		details = append(details, "respect system proxy: "+respectSystemProxy)
		if managedProxyConfiguredForDoctor(cfg) {
			details = append(details, "managed proxy: configured")
		} else {
			details = append(details, "managed proxy: not configured")
		}
	}
	status := CheckStatusOK
	summary := "network-related environment looks readable"
	for _, key := range []string{"CODEX_CA_CERTIFICATE", "SSL_CERT_FILE"} {
		if value, ok := env[key]; ok && strings.TrimSpace(value) != "" {
			caStatus, caSummary, detail := customCAEnvDetail(key, value)
			details = append(details, detail)
			if caStatus == CheckStatusWarning {
				status = CheckStatusWarning
				summary = caSummary
			}
		}
	}
	return NewCheck("network.env", "network", status, summary).DetailsList(details)
}

// managedProxyConfiguredForDoctor reports whether the effective config defines
// a managed permissions network (Rust config.permissions.network.is_some()).
func managedProxyConfiguredForDoctor(cfg *config.Config) bool {
	if cfg == nil || cfg.Values == nil {
		return false
	}
	permissions, ok := cfg.Values["permissions"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = permissions["network"]
	return ok
}

func websocketReachabilityCheck(codexHome string, opts *Options) *DoctorCheck {
	cfg, err := loadEffectiveConfigForDoctor(codexHome, opts)
	if err != nil {
		return NewCheck("network.websocket_reachability", "websocket", CheckStatusWarning, "Responses WebSocket config could not be loaded").
			Detail("config error: " + err.Error())
	}
	providerID := effectiveProviderIDForDoctor(opts, cfg)
	provider, err := model.ProviderForConfigID(configValuesForDoctor(cfg), providerID, stringConfigValueForDoctor(cfg, "openai_base_url"))
	if err != nil {
		return NewCheck("network.websocket_reachability", "websocket", CheckStatusWarning, "Responses WebSocket provider setup failed").
			Detail("provider setup failed: " + err.Error())
	}
	var snapshot *auth.AuthDotJSON
	storeOptions := auth.StoreOptionsFromConfig(cfg.CLIAuthCredentialsStoreMode(), cfg.SecretAuthStorageEnabled())
	if resolved, resolveErr := auth.NewStoreWithOptions(codexHome, storeOptions).Resolve(); resolveErr == nil && resolved != nil {
		snapshot = &resolved.Auth
	}
	return websocketReachabilityCheckFromProvider(providerID, provider, snapshot)
}

func websocketReachabilityCheckFromProvider(providerID string, provider *model.ProviderInfo, snapshot *auth.AuthDotJSON) *DoctorCheck {
	if provider == nil {
		return NewCheck("network.websocket_reachability", "websocket", CheckStatusWarning, "Responses WebSocket provider setup failed").
			Detail("provider setup failed: provider is nil")
	}
	wireAPI := string(provider.WireAPI)
	if wireAPI == "" {
		wireAPI = string(model.WireAPIResponses)
	}
	details := []string{
		"model provider: " + providerID,
		"provider name: " + provider.Name,
		"wire API: " + wireAPI,
		fmt.Sprintf("supports websockets: %t", provider.SupportsWebsockets),
	}
	pushProxyEnvSummary(&details, currentEnvMap())
	if !provider.SupportsWebsockets {
		return NewCheck("network.websocket_reachability", "websocket", CheckStatusOK, "Responses WebSocket is not enabled for the active provider").
			DetailsList(details)
	}
	details = append(details, fmt.Sprintf("connect timeout: %d ms", provider.EffectiveWebsocketConnectTimeout().Milliseconds()))
	runtimeProvider := model.CreateRuntimeProviderForID(providerID, *provider, snapshot)
	apiProvider, err := runtimeProvider.APIProvider()
	if err != nil {
		return websocketProbeWarning("Responses WebSocket provider setup failed", details, "provider setup failed: "+err.Error())
	}
	authMode := "none"
	if snapshot != nil {
		authMode = snapshot.Mode()
	}
	details = append(details, "auth mode: "+authMode)
	codexProvider := codexapi.Provider{
		Name:        apiProvider.Name,
		BaseURL:     apiProvider.BaseURL,
		QueryParams: apiProvider.QueryParams,
		Headers:     apiProvider.Headers,
	}
	endpoint, err := codexProvider.WebsocketURLForPath("responses")
	if err != nil {
		return websocketProbeWarning("Responses WebSocket endpoint could not be built", details, "endpoint build failed: "+err.Error())
	}
	details = append(details, "endpoint: "+endpoint)
	details = append(details, websocketDNSAddressFamilyDetails(endpoint)...)
	headers, err := websocketProbeHeaders(context.Background(), endpoint, apiProvider.Headers, runtimeProvider)
	if err != nil {
		return websocketProbeWarning("Responses WebSocket auth could not be resolved", details, "auth resolution failed: "+err.Error())
	}
	timeout := provider.EffectiveWebsocketConnectTimeout()
	probeCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	conn, response, err := websocket.Dial(probeCtx, endpoint, &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		summary := "Responses WebSocket failed; HTTPS fallback may still work"
		detail := websocketHandshakeErrorDetail(response, err)
		if errors.Is(probeCtx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			summary = "Responses WebSocket timed out; HTTPS fallback may still work"
			detail = "handshake timed out"
		}
		return websocketProbeWarning(summary, details, detail)
	}
	status := http.StatusSwitchingProtocols
	responseHeaders := http.Header{}
	if response != nil {
		status = response.StatusCode
		responseHeaders = response.Header
	}
	details = append(details,
		fmt.Sprintf("handshake result: HTTP %d", status),
		fmt.Sprintf("reasoning header: %t", websocketHeaderPresent(responseHeaders, "x-reasoning-included")),
		fmt.Sprintf("server model present: %t", websocketHeaderPresent(responseHeaders, "openai-model", "x-openai-model")),
	)
	immediateClose, readErr := websocketImmediateCloseForDoctor(conn, websocketImmediateCloseGrace)
	_ = conn.Close(websocket.StatusNormalClosure, "doctor probe completed")
	if readErr != nil {
		return websocketProbeWarning("Responses WebSocket failed; HTTPS fallback may still work", details, "handshake stream error: failed to read websocket probe event: "+readErr.Error())
	}
	if immediateClose != nil {
		details = append(details,
			"immediate close code: "+immediateClose.Code,
			"immediate close reason: "+immediateClose.Reason,
		)
		return NewCheck("network.websocket_reachability", "websocket", CheckStatusWarning, "Responses WebSocket closed immediately after handshake").
			DetailsList(details).
			Remediate("Check proxy, VPN, firewall, DNS, custom CA, and WebSocket policy support.")
	}
	return NewCheck("network.websocket_reachability", "websocket", CheckStatusOK, "Responses WebSocket handshake succeeded").
		DetailsList(details)
}

type websocketImmediateClose struct {
	Code   string
	Reason string
}

func websocketDNSAddressFamilyDetails(endpoint string) []string {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil
	}
	host := parsed.Hostname()
	if strings.TrimSpace(host) == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultProviderReachabilityTimeout)
	defer cancel()
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return []string{"DNS: lookup failed (" + err.Error() + ")"}
	}
	ipv4Count := 0
	ipv6Count := 0
	firstFamily := "none"
	for index, address := range addresses {
		family := "IPv6"
		if address.IP.To4() != nil {
			family = "IPv4"
			ipv4Count++
		} else {
			ipv6Count++
		}
		if index == 0 {
			firstFamily = family
		}
	}
	return []string{fmt.Sprintf("DNS: %d IPv4, %d IPv6, first %s", ipv4Count, ipv6Count, firstFamily)}
}

func websocketImmediateCloseForDoctor(conn *websocket.Conn, grace time.Duration) (*websocketImmediateClose, error) {
	if conn == nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	_, _, err := conn.Read(ctx)
	if err == nil {
		return nil, nil
	}
	var closeErr websocket.CloseError
	if errors.As(err, &closeErr) {
		return &websocketImmediateClose{
			Code:   strconv.Itoa(int(closeErr.Code)),
			Reason: closeErr.Reason,
		}, nil
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return nil, nil
	}
	return nil, err
}

func websocketProbeHeaders(ctx context.Context, endpoint string, providerHeaders http.Header, runtimeProvider model.RuntimeProvider) (http.Header, error) {
	headers := cloneHTTPHeaderForDoctor(providerHeaders)
	if runtimeProvider == nil {
		return headers, nil
	}
	authHeaders, err := runtimeProvider.APIAuth()
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header = headers
	if err := authHeaders.Apply(ctx, request, nil); err != nil {
		return nil, err
	}
	request.Header.Set(codexapi.ClientOpenAIBetaHeader, responsesWebsocketsV2BetaHeaderValue)
	return request.Header.Clone(), nil
}

func cloneHTTPHeaderForDoctor(values http.Header) http.Header {
	cloned := http.Header{}
	for key, value := range values {
		cloned[key] = append([]string(nil), value...)
	}
	return cloned
}

func websocketProbeWarning(summary string, details []string, errorDetail string) *DoctorCheck {
	details = append(details, errorDetail)
	return NewCheck("network.websocket_reachability", "websocket", CheckStatusWarning, summary).
		DetailsList(details).
		Remediate("Check proxy, VPN, firewall, DNS, custom CA, and WebSocket policy support.")
}

func websocketHandshakeErrorDetail(response *http.Response, err error) string {
	if response != nil {
		statusText := strings.TrimSpace(http.StatusText(response.StatusCode))
		if statusText == "" {
			statusText = strings.TrimSpace(response.Status)
		}
		return fmt.Sprintf("handshake API error: %d %s", response.StatusCode, statusText)
	}
	if err != nil {
		return "handshake transport error: " + err.Error()
	}
	return "handshake transport error"
}

func websocketHeaderPresent(headers http.Header, names ...string) bool {
	for _, name := range names {
		if values, ok := headers[http.CanonicalHeaderKey(name)]; ok && len(values) > 0 {
			return true
		}
	}
	return false
}

func pushProxyEnvSummary(details *[]string, env map[string]string) {
	present := []string{}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "WS_PROXY", "WSS_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy", "ws_proxy", "wss_proxy"} {
		if strings.TrimSpace(env[key]) != "" {
			present = append(present, key)
		}
	}
	if len(present) == 0 {
		*details = append(*details, "proxy env vars: none")
		return
	}
	*details = append(*details, "proxy env vars present: "+strings.Join(present, ", "))
}

func terminalCheck(env map[string]string, opts *Options) *DoctorCheck {
	if env == nil {
		env = currentEnvMap()
	}
	stdoutIsTerminal := fileIsTerminal(os.Stdout)
	info := shell.Detect(&shell.MapEnvironment{Values: env})
	inputs := &terminalCheckInputs{
		Info:                  info,
		Env:                   env,
		NoColorFlag:           opts != nil && opts.NoColor,
		StdinIsTerminal:       fileIsTerminal(os.Stdin),
		StdoutIsTerminal:      stdoutIsTerminal,
		StderrIsTerminal:      fileIsTerminal(os.Stderr),
		StreamSupportsColor:   terminalStreamSupportsColor(env, stdoutIsTerminal),
		TerminalSize:          detectTerminalSizeForDoctor(os.Stdout, stdoutIsTerminal),
		WindowsConsoleDetails: windowsConsoleDetailsForDoctor(),
	}
	if info != nil && info.Multiplexer != nil && info.Multiplexer.Name == shell.MultiplexerTmux {
		inputs.TmuxDetails = tmuxDiagnosticDetailsForDoctor()
	}
	return terminalCheckFromInputs(inputs)
}

type terminalCheckInputs struct {
	Info                  *shell.TerminalInfo
	Env                   map[string]string
	NoColorFlag           bool
	StdinIsTerminal       bool
	StdoutIsTerminal      bool
	StderrIsTerminal      bool
	StreamSupportsColor   bool
	TerminalSize          terminalSizeProbe
	TmuxDetails           []string
	WindowsConsoleDetails []string
}

type terminalSizeProbe struct {
	Columns int
	Rows    int
	Err     string
}

func terminalCheckFromInputs(inputs *terminalCheckInputs) *DoctorCheck {
	if inputs == nil {
		inputs = &terminalCheckInputs{}
	}
	env := inputs.Env
	if env == nil {
		env = map[string]string{}
	}
	info := inputs.Info
	if info == nil {
		info = &shell.TerminalInfo{Name: shell.TerminalUnknown}
	}
	details := []string{"terminal: " + terminalNameForDoctor(info)}
	if value := terminalStringPtr(info.TermProgram); value != "" {
		details = append(details, "TERM_PROGRAM: "+value)
	}
	if value := terminalStringPtr(info.Version); value != "" {
		details = append(details, "terminal version: "+value)
	}
	if value := terminalStringPtr(info.Term); value != "" {
		details = append(details, "TERM: "+value)
	} else if value := strings.TrimSpace(env["TERM"]); value != "" {
		details = append(details, "TERM: "+value)
	}
	if info.Multiplexer != nil {
		details = append(details, "multiplexer: "+terminalMultiplexerNameForDoctor(info.Multiplexer))
	}
	details = append(details,
		fmt.Sprintf("stdin is terminal: %t", inputs.StdinIsTerminal),
		fmt.Sprintf("stdout is terminal: %t", inputs.StdoutIsTerminal),
		fmt.Sprintf("stderr is terminal: %t", inputs.StderrIsTerminal),
		terminalSizeDetailForDoctor(inputs.TerminalSize),
	)
	pushTerminalEnvValues(&details, env, []string{"COLUMNS", "LINES"})
	details = append(details, "color output: "+terminalColorOutputSummary(inputs))
	pushTerminalEnvValues(&details, env, terminalColorEnvVarsForDoctor)
	terminfoWarning := pushTerminfoDetails(&details, env)
	if locale := effectiveTerminalLocale(env); locale != "" {
		details = append(details, "effective locale: "+locale)
	}
	pushTerminalPresenceDetails(&details, env)
	details = append(details, inputs.TmuxDetails...)
	details = append(details, inputs.WindowsConsoleDetails...)

	issues := []*DoctorIssue{}
	if term := strings.TrimSpace(env["TERM"]); term == "dumb" || info.Name == shell.TerminalDumb {
		issues = append(issues, NewIssue(CheckStatusFail, "TERM=dumb - colors and cursor control are disabled").
			WithMeasured("TERM=dumb").
			WithExpected("TERM=xterm-256color or another real terminal type").
			WithRemedy("set TERM to a real value, for example xterm-256color").
			WithField("TERM"))
	}
	if locale := effectiveTerminalLocale(env); locale != "" && isNonUTF8Locale(locale) {
		issues = append(issues, NewIssue(CheckStatusWarning, "locale is not UTF-8 - unicode glyphs may render incorrectly").
			WithMeasured(locale).
			WithExpected("UTF-8 locale, for example en_US.UTF-8").
			WithRemedy("export LANG=en_US.UTF-8 or another UTF-8 locale").
			WithField("effective locale"))
	}
	if terminfoWarning {
		issues = append(issues, NewIssue(CheckStatusFail, "TERMINFO unreadable - terminal capabilities are unknown").
			WithExpected("readable terminfo file or directory").
			WithRemedy("check that $TERMINFO points to a readable directory").
			WithField("TERMINFO").
			WithField("TERMINFO_DIRS entry"))
	}
	issues = append(issues, terminalSizeIssuesForDoctor(inputs.TerminalSize, env)...)

	status := CheckStatusOK
	summary := "terminal metadata was detected"
	for _, issue := range issues {
		status = maxCheckStatus(status, issue.Severity)
	}
	if len(issues) > 0 {
		summary = issues[0].Cause
	}
	check := NewCheck("terminal.env", "terminal", status, summary).DetailsList(details)
	for _, issue := range issues {
		check.Issue(issue)
	}
	return check
}

func fileIsTerminal(file *os.File) bool {
	if file == nil {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func terminalSizeDetailForDoctor(size terminalSizeProbe) string {
	if size.Err != "" {
		return "terminal size: unavailable (" + size.Err + ")"
	}
	return fmt.Sprintf("terminal size: %dx%d", size.Columns, size.Rows)
}

func pushTerminalEnvValues(details *[]string, env map[string]string, names []string) {
	for _, key := range names {
		value, ok := env[key]
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			*details = append(*details, key+": present")
			continue
		}
		*details = append(*details, key+": "+value)
	}
}

func terminalColorOutputSummary(inputs *terminalCheckInputs) string {
	env := inputs.Env
	if env == nil {
		env = map[string]string{}
	}
	noColorFlag := inputs.NoColorFlag
	_, noColorEnv := env["NO_COLOR"]
	term := strings.TrimSpace(env["TERM"])
	if !noColorFlag && !noColorEnv && term != "dumb" && inputs.StdoutIsTerminal && inputs.StreamSupportsColor {
		return "enabled"
	}
	reason := "disabled"
	switch {
	case noColorFlag:
		reason = "--no-color"
	case noColorEnv:
		reason = "NO_COLOR"
	case term == "dumb":
		reason = "TERM=dumb"
	case !inputs.StdoutIsTerminal:
		reason = "stdout is not a terminal"
	case !inputs.StreamSupportsColor:
		reason = "terminal color support not detected"
	}
	return "disabled (" + reason + ")"
}

func terminalStreamSupportsColor(env map[string]string, stdoutIsTerminal bool) bool {
	if !stdoutIsTerminal {
		return false
	}
	if terminalEnvFlagEnabled(env, "FORCE_COLOR") || terminalEnvFlagEnabled(env, "CLICOLOR_FORCE") {
		return true
	}
	if value := strings.TrimSpace(env["COLORTERM"]); value != "" {
		return true
	}
	if runtime.GOOS == "windows" {
		if _, ok := env["WT_SESSION"]; ok {
			return true
		}
		if _, ok := env["ANSICON"]; ok {
			return true
		}
		if strings.EqualFold(strings.TrimSpace(env["ConEmuANSI"]), "ON") {
			return true
		}
	}
	term := strings.ToLower(strings.TrimSpace(env["TERM"]))
	if term == "" || term == "dumb" {
		return false
	}
	for _, marker := range []string{"color", "ansi", "xterm", "screen", "tmux", "rxvt", "linux", "vt100"} {
		if strings.Contains(term, marker) {
			return true
		}
	}
	return false
}

func terminalEnvFlagEnabled(env map[string]string, name string) bool {
	value, ok := env[name]
	if !ok {
		return false
	}
	value = strings.TrimSpace(strings.ToLower(value))
	return value != "" && value != "0" && value != "false"
}

func terminalSizeIssuesForDoctor(size terminalSizeProbe, env map[string]string) []*DoctorIssue {
	issues := []*DoctorIssue{}
	if size.Err == "" {
		if size.Columns > 0 && size.Columns < narrowTerminalColumns {
			issues = append(issues, NewIssue(CheckStatusWarning, fmt.Sprintf("width %d cols - output may wrap (recommended >=80)", size.Columns)).
				WithMeasured(fmt.Sprintf("%d x %d", size.Columns, size.Rows)).
				WithExpected(fmt.Sprintf(">= %d columns", narrowTerminalColumns)).
				WithRemedy("resize the window to at least 80 columns").
				WithField("terminal size"))
		}
		if size.Rows > 0 && size.Rows < narrowTerminalRows {
			issues = append(issues, NewIssue(CheckStatusWarning, fmt.Sprintf("height %d rows - content may scroll off (recommended >=24)", size.Rows)).
				WithMeasured(fmt.Sprintf("%d x %d", size.Columns, size.Rows)).
				WithExpected(fmt.Sprintf(">= %d rows", narrowTerminalRows)).
				WithRemedy("resize the window to at least 24 rows").
				WithField("terminal size"))
		}
	}
	if columns, ok := intEnvValue(env, "COLUMNS"); ok && columns > 0 && columns < narrowTerminalColumns {
		issues = append(issues, NewIssue(CheckStatusWarning, fmt.Sprintf("COLUMNS=%d - output may wrap (recommended >=80)", columns)).
			WithMeasured(fmt.Sprintf("%d columns", columns)).
			WithExpected(fmt.Sprintf(">= %d columns", narrowTerminalColumns)).
			WithRemedy("resize the window to at least 80 columns").
			WithField("COLUMNS"))
	}
	if rows, ok := intEnvValue(env, "LINES"); ok && rows > 0 && rows < narrowTerminalRows {
		issues = append(issues, NewIssue(CheckStatusWarning, fmt.Sprintf("LINES=%d - content may scroll off (recommended >=24)", rows)).
			WithMeasured(fmt.Sprintf("%d rows", rows)).
			WithExpected(fmt.Sprintf(">= %d rows", narrowTerminalRows)).
			WithRemedy("resize the window to at least 24 rows").
			WithField("LINES"))
	}
	return issues
}

func tmuxDiagnosticDetailsForDoctor() []string {
	details := []string{}
	if value := tmuxDisplayMessageForDoctor("#{client_termtype}"); value != "" {
		details = append(details, "tmux client termtype: "+value)
	}
	if value := tmuxDisplayMessageForDoctor("#{client_termname}"); value != "" {
		details = append(details, "tmux client termname: "+value)
	}
	for _, option := range tmuxOptionNamesForDoctor {
		value := tmuxOptionValueForDoctor(option)
		if value == "" {
			value = "unavailable"
		}
		details = append(details, "tmux "+option+": "+value)
	}
	return details
}

func tmuxOptionValueForDoctor(option string) string {
	output, err := exec.Command("tmux", "show-options", "-gqv", option).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func tmuxDisplayMessageForDoctor(format string) string {
	output, err := exec.Command("tmux", "display-message", "-p", format).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func terminalMultiplexerNameForDoctor(multiplexer *shell.Multiplexer) string {
	if multiplexer == nil {
		return ""
	}
	name := string(multiplexer.Name)
	if multiplexer.Version != nil && strings.TrimSpace(*multiplexer.Version) != "" {
		return name + " " + strings.TrimSpace(*multiplexer.Version)
	}
	return name
}

func terminalNameForDoctor(info *shell.TerminalInfo) string {
	if info == nil {
		return "unknown"
	}
	switch info.Name {
	case shell.TerminalAppleTerminal:
		return "Apple Terminal"
	case shell.TerminalGhostty:
		return "Ghostty"
	case shell.TerminalIterm2:
		return "iTerm2"
	case shell.TerminalWarp:
		return "Warp"
	case shell.TerminalVSCode:
		return "VS Code"
	case shell.TerminalWezTerm:
		return "WezTerm"
	case shell.TerminalKitty:
		return "kitty"
	case shell.TerminalAlacritty:
		return "Alacritty"
	case shell.TerminalKonsole:
		return "Konsole"
	case shell.TerminalGnomeTerminal:
		return "GNOME Terminal"
	case shell.TerminalVTE:
		return "VTE"
	case shell.TerminalWindowsTerminal:
		return "Windows Terminal"
	case shell.TerminalDumb:
		return "dumb"
	default:
		return "unknown"
	}
}

func terminalStringPtr(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func effectiveTerminalLocale(env map[string]string) string {
	for _, key := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if value := strings.TrimSpace(env[key]); value != "" {
			return value
		}
	}
	return ""
}

func pushTerminalPresenceDetails(details *[]string, env map[string]string) {
	for _, key := range []string{"SSH_TTY", "SSH_CONNECTION", "SSH_CLIENT", "MOSH_IP", "WSL_DISTRO_NAME", "WSL_INTEROP", "VSCODE_INJECTION", "VSCODE_IPC_HOOK_CLI", "WAYLAND_DISPLAY", "DISPLAY", "WT_SESSION"} {
		if _, ok := env[key]; ok {
			*details = append(*details, key+": present")
		}
	}
}

func pushTerminfoDetails(details *[]string, env map[string]string) bool {
	warning := false
	if path := strings.TrimSpace(env["TERMINFO"]); path != "" {
		status, statusWarning := terminalPathReadiness(path)
		*details = append(*details, "TERMINFO: "+path+" ("+status+")")
		warning = warning || statusWarning
	}
	if paths, ok := env["TERMINFO_DIRS"]; ok && strings.TrimSpace(paths) != "" {
		for _, path := range filepath.SplitList(paths) {
			path = strings.TrimSpace(path)
			if path == "" {
				continue
			}
			status, statusWarning := terminalPathReadiness(path)
			*details = append(*details, "TERMINFO_DIRS entry: "+path+" ("+status+")")
			warning = warning || statusWarning
		}
	} else if _, ok := env["TERMINFO_DIRS"]; ok {
		*details = append(*details, "TERMINFO_DIRS: present")
	}
	return warning
}

func terminalPathReadiness(path string) (string, bool) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "missing", true
		}
		return err.Error(), true
	}
	if info.IsDir() {
		entries, err := os.ReadDir(path)
		if err != nil {
			return "dir unreadable: " + err.Error(), true
		}
		_ = entries
		return "dir", false
	}
	if info.Mode().IsRegular() {
		file, err := os.Open(path)
		if err != nil {
			return "file unreadable: " + err.Error(), true
		}
		_ = file.Close()
		return "file", false
	}
	return "not a file or directory", true
}

func intEnvValue(env map[string]string, key string) (int64, bool) {
	value := strings.TrimSpace(env[key])
	if value == "" {
		return 0, false
	}
	var out int64
	if _, err := fmt.Sscanf(value, "%d", &out); err != nil {
		return 0, false
	}
	return out, true
}

func isNonUTF8Locale(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return false
	}
	return !strings.Contains(value, "utf-8") && !strings.Contains(value, "utf8")
}

const gitCommandTimeout = 2 * time.Second

type gitCheckInputs struct {
	SelectedGit     string
	GitCandidates   []string
	GitVersion      string
	GitExecPath     string
	GitBuildOptions string
	RepoRoot        string
	GitEntry        string
	Branch          string
	CoreFSMonitor   string
}

type parsedGitVersion struct {
	Major int
	Minor int
	Patch int
}

func gitCheck(cwd string) *DoctorCheck {
	selectedGit, _ := exec.LookPath("git")
	candidates := gitCandidatesFromPath(os.Getenv("PATH"))
	repoRoot := gitRepoRootForDoctor(cwd)
	inputs := &gitCheckInputs{
		SelectedGit:   selectedGit,
		GitCandidates: candidates,
		RepoRoot:      repoRoot,
	}
	if repoRoot != "" {
		inputs.GitEntry = gitEntrySummary(repoRoot)
	}
	if selectedGit != "" {
		inputs.GitVersion = gitOutputForDoctor(selectedGit, cwd, "--version")
		inputs.GitExecPath = gitOutputForDoctor(selectedGit, cwd, "--exec-path")
		inputs.GitBuildOptions = gitOutputForDoctor(selectedGit, cwd, "version", "--build-options")
		inputs.Branch = gitOutputForDoctor(selectedGit, cwd, "rev-parse", "--abbrev-ref", "HEAD")
		inputs.CoreFSMonitor = gitOutputForDoctor(selectedGit, cwd, "config", "--get", "core.fsmonitor")
	}
	return gitCheckFromInputs(inputs)
}

func gitCheckFromInputs(inputs *gitCheckInputs) *DoctorCheck {
	if inputs == nil {
		inputs = &gitCheckInputs{}
	}
	details := []string{}
	if strings.TrimSpace(inputs.SelectedGit) != "" {
		details = append(details, "selected git: "+inputs.SelectedGit)
	} else {
		details = append(details, "selected git: not found")
	}
	details = append(details, fmt.Sprintf("PATH git entries: %d", len(inputs.GitCandidates)))
	for index, path := range inputs.GitCandidates {
		details = append(details, fmt.Sprintf("PATH git #%d: %s", index+1, path))
	}
	pushOptionalDoctorDetail(&details, "git version", inputs.GitVersion)
	pushOptionalDoctorDetail(&details, "git exec path", inputs.GitExecPath)
	pushOptionalDoctorDetail(&details, "git build options", inputs.GitBuildOptions)
	if strings.TrimSpace(inputs.RepoRoot) != "" {
		details = append(details, "repo detected: true")
		details = append(details, "repo root: "+inputs.RepoRoot)
	} else {
		details = append(details, "repo detected: false")
	}
	pushOptionalDoctorDetail(&details, ".git entry", inputs.GitEntry)
	pushOptionalDoctorDetail(&details, "git branch", normalizedGitBranch(inputs.Branch))
	pushOptionalDoctorDetail(&details, "core.fsmonitor", inputs.CoreFSMonitor)

	check := NewCheck("git.environment", "git", CheckStatusOK, gitSummary(inputs)).DetailsList(details)
	switch {
	case strings.TrimSpace(inputs.SelectedGit) != "" && strings.TrimSpace(inputs.GitVersion) == "":
		check.Status = CheckStatusWarning
		check.Summary = "Git executable found but could not be run"
		check.Issue(NewIssue(CheckStatusWarning, "Git executable was found on PATH but did not return a version").
			WithExpected("git --version succeeds").
			WithRemedy("Fix the selected Git executable or PATH so Codex can inspect Git metadata.").
			WithField("git version").
			WithField("selected git"))
	case strings.TrimSpace(inputs.SelectedGit) == "" && strings.TrimSpace(inputs.RepoRoot) != "":
		check.Status = CheckStatusWarning
		check.Summary = "Git repository detected but git executable was not found"
		check.Issue(NewIssue(CheckStatusWarning, "Git repository detected but git executable was not found").
			WithExpected("git available on PATH").
			WithRemedy("Install Git or fix PATH so Codex can inspect repository metadata.").
			WithField("selected git"))
	case oldWindowsGitWarning(inputs.GitVersion, runtime.GOOS == "windows") != "":
		cause := oldWindowsGitWarning(inputs.GitVersion, runtime.GOOS == "windows")
		check.Status = CheckStatusWarning
		check.Summary = cause
		measured := strings.TrimSpace(inputs.GitVersion)
		if measured == "" {
			measured = "unknown"
		}
		check.Issue(NewIssue(CheckStatusWarning, cause).
			WithMeasured(measured).
			WithExpected("current Git for Windows").
			WithRemedy("Update Git for Windows or the bundled Git executable Codex resolves first.").
			WithField("git version").
			WithField("selected git"))
	}
	return check
}

func doctorCWD(opts *Options) string {
	if opts != nil {
		if cwd := strings.TrimSpace(opts.Root.Shared.CWD); cwd != "" {
			return cwd
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}

func gitCandidatesFromPath(pathValue string) []string {
	if strings.TrimSpace(pathValue) == "" {
		return nil
	}
	seen := map[string]bool{}
	candidates := []string{}
	for _, dir := range filepath.SplitList(pathValue) {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		for _, name := range gitExecutableNames() {
			candidate := filepath.Join(dir, name)
			info, err := os.Stat(candidate)
			if err != nil || info.IsDir() {
				continue
			}
			clean := filepath.Clean(candidate)
			key := strings.ToLower(clean)
			if runtime.GOOS != "windows" {
				key = clean
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			candidates = append(candidates, clean)
		}
	}
	return candidates
}

func gitExecutableNames() []string {
	if runtime.GOOS == "windows" {
		return []string{"git.exe", "git.cmd", "git.bat", "git"}
	}
	return []string{"git"}
}

func gitOutputForDoctor(gitPath string, cwd string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), gitCommandTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, gitPath, args...)
	command.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	if strings.TrimSpace(cwd) != "" {
		command.Dir = cwd
	}
	output, err := command.Output()
	if err != nil {
		return ""
	}
	return normalizedCommandOutput(output)
}

func normalizedCommandOutput(output []byte) string {
	lines := []string{}
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "; ")
}

func gitRepoRootForDoctor(cwd string) string {
	if strings.TrimSpace(cwd) == "" {
		cwd = "."
	}
	dir, err := filepath.Abs(cwd)
	if err != nil {
		dir = filepath.Clean(cwd)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func gitEntrySummary(repoRoot string) string {
	entry := filepath.Join(repoRoot, ".git")
	info, err := os.Stat(entry)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "missing"
		}
		return "unreadable (" + err.Error() + ")"
	}
	switch {
	case info.IsDir():
		return "directory"
	case info.Mode().IsRegular():
		bytes, err := os.ReadFile(entry)
		if err == nil {
			if path, ok := strings.CutPrefix(strings.TrimSpace(string(bytes)), "gitdir:"); ok {
				return "file -> " + strings.TrimSpace(path)
			}
		}
		return "file"
	default:
		return "other"
	}
}

func gitSummary(inputs *gitCheckInputs) string {
	if inputs == nil {
		return "git executable not found"
	}
	if version := strings.TrimSpace(inputs.GitVersion); version != "" {
		return version
	}
	if strings.TrimSpace(inputs.SelectedGit) != "" {
		return "git executable found; version unavailable"
	}
	return "git executable not found"
}

func pushOptionalDoctorDetail(details *[]string, label string, value string) {
	if strings.TrimSpace(value) != "" {
		*details = append(*details, label+": "+strings.TrimSpace(value))
	}
}

func normalizedGitBranch(branch string) string {
	branch = strings.TrimSpace(branch)
	if branch == "HEAD" {
		return "detached HEAD"
	}
	return branch
}

func oldWindowsGitWarning(version string, isWindows bool) string {
	if !isWindows {
		return ""
	}
	version = strings.TrimSpace(version)
	if version == "" {
		return ""
	}
	if strings.Contains(strings.ToLower(version), "msysgit") {
		return "old msysgit installation may corrupt Windows TUI rendering"
	}
	parsed, ok := parseGitVersion(version)
	if !ok {
		return ""
	}
	if parsed.Major < 2 || (parsed.Major == 2 && parsed.Minor <= 34) {
		return "old Git for Windows may corrupt Windows TUI rendering"
	}
	return ""
}

func parseGitVersion(version string) (parsedGitVersion, bool) {
	version = strings.TrimSpace(version)
	if !strings.HasPrefix(version, "git version ") {
		return parsedGitVersion{}, false
	}
	version = strings.TrimPrefix(version, "git version ")
	if version == "" {
		return parsedGitVersion{}, false
	}
	numeric := strings.Fields(version)
	if len(numeric) == 0 {
		return parsedGitVersion{}, false
	}
	base := strings.SplitN(numeric[0], ".windows.", 2)[0]
	parts := strings.Split(base, ".")
	if len(parts) < 2 {
		return parsedGitVersion{}, false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return parsedGitVersion{}, false
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return parsedGitVersion{}, false
	}
	patch := 0
	if len(parts) > 2 {
		patch, err = strconv.Atoi(parts[2])
		if err != nil {
			return parsedGitVersion{}, false
		}
	}
	return parsedGitVersion{Major: major, Minor: minor, Patch: patch}, true
}

var defaultTerminalTitleItems = []string{"activity", "project-name"}

const projectTitleMaxChars = 24

type terminalTitleInputs struct {
	ConfiguredItems *[]string
	CWD             string
	ProjectRoot     *projectTitleRoot
}

type projectTitleRoot struct {
	Source string
	Path   string
}

func terminalTitleCheck(codexHome string, opts *Options) *DoctorCheck {
	cwd := doctorCWD(opts)
	cfg, err := loadEffectiveConfigForDoctor(codexHome, opts)
	if err != nil {
		return NewCheck("terminal.title", "title", CheckStatusWarning, "terminal title config could not be loaded").
			Detail("terminal title config error: " + err.Error())
	}
	return terminalTitleCheckFromInputs(&terminalTitleInputs{
		ConfiguredItems: configuredTerminalTitleItems(cfg),
		CWD:             cwd,
		ProjectRoot:     terminalTitleProjectRoot(codexHome, cwd, opts),
	})
}

func terminalTitleCheckFromInputs(inputs *terminalTitleInputs) *DoctorCheck {
	if inputs == nil {
		inputs = &terminalTitleInputs{}
	}
	source := "default"
	items := append([]string(nil), defaultTerminalTitleItems...)
	invalidItems := []string{}
	if inputs.ConfiguredItems != nil {
		if len(*inputs.ConfiguredItems) == 0 {
			source = "disabled"
			items = nil
		} else {
			source = "configured"
			items, invalidItems = parseTerminalTitleItems(*inputs.ConfiguredItems)
		}
	}

	itemText := "none"
	if len(items) > 0 {
		itemText = strings.Join(items, ", ")
	}
	details := []string{
		"terminal title source: " + source,
		"terminal title items: " + itemText,
		fmt.Sprintf("terminal title activity: %t", terminalTitleActivityEnabled(items)),
	}
	if len(invalidItems) > 0 {
		details = append(details, "terminal title invalid items: "+strings.Join(invalidItems, ", "))
	}
	if terminalTitleProjectSelected(items) {
		projectSource, projectValue := terminalTitleProjectCandidate(inputs.ProjectRoot, inputs.CWD)
		details = append(details, "terminal title project source: "+projectSource)
		if projectValue != "" {
			details = append(details, "terminal title project value: "+projectValue)
		}
	}

	status := CheckStatusOK
	summary := "terminal title " + source
	if len(invalidItems) > 0 {
		status = CheckStatusWarning
		summary = "terminal title " + source + " with invalid items"
	}
	check := NewCheck("terminal.title", "title", status, summary).DetailsList(details)
	if len(invalidItems) > 0 {
		check.Issue(NewIssue(CheckStatusWarning, "terminal title configuration contains unknown item identifiers").
			WithMeasured(strings.Join(invalidItems, ", ")).
			WithExpected("known terminal title item identifiers").
			WithRemedy("Remove or replace the unknown entries in [tui].terminal_title.").
			WithField("terminal title invalid items"))
	}
	return check
}

func configuredTerminalTitleItems(cfg *config.Config) *[]string {
	if cfg == nil || cfg.Values == nil {
		return nil
	}
	tui, ok := cfg.Values["tui"].(map[string]any)
	if !ok {
		return nil
	}
	value, ok := tui["terminal_title"]
	if !ok {
		return nil
	}
	items := stringListConfigValueForDoctor(value)
	return &items
}

func stringListConfigValueForDoctor(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	case string:
		return []string{typed}
	default:
		return nil
	}
}

func parseTerminalTitleItems(raw []string) ([]string, []string) {
	items := []string{}
	invalidItems := []string{}
	invalidSeen := map[string]bool{}
	for _, item := range raw {
		if id := terminalTitleItemID(item); id != "" {
			items = append(items, id)
			continue
		}
		if invalidSeen[item] {
			continue
		}
		invalidSeen[item] = true
		invalidItems = append(invalidItems, strconv.Quote(item))
	}
	return items, invalidItems
}

func terminalTitleItemID(item string) string {
	switch item {
	case "app-name":
		return "app-name"
	case "project-name", "project":
		return "project-name"
	case "current-dir":
		return "current-dir"
	case "activity", "spinner":
		return "activity"
	case "run-state", "status":
		return "run-state"
	case "thread-title", "thread":
		return "thread-title"
	case "git-branch":
		return "git-branch"
	case "context-remaining":
		return "context-remaining"
	case "context-used", "context-usage":
		return "context-used"
	case "five-hour-limit":
		return "five-hour-limit"
	case "weekly-limit":
		return "weekly-limit"
	case "codex-version":
		return "codex-version"
	case "used-tokens":
		return "used-tokens"
	case "total-input-tokens":
		return "total-input-tokens"
	case "total-output-tokens":
		return "total-output-tokens"
	case "thread-id", "session-id":
		return "thread-id"
	case "fast-mode":
		return "fast-mode"
	case "model", "model-name":
		return "model"
	case "model-with-reasoning":
		return "model-with-reasoning"
	case "reasoning":
		return "reasoning"
	case "task-progress":
		return "task-progress"
	default:
		return ""
	}
}

func terminalTitleActivityEnabled(items []string) bool {
	for _, item := range items {
		if item == "activity" || item == "spinner" {
			return true
		}
	}
	return false
}

func terminalTitleProjectSelected(items []string) bool {
	for _, item := range items {
		if item == "project-name" || item == "project" {
			return true
		}
	}
	return false
}

func terminalTitleProjectRoot(codexHome string, cwd string, opts *Options) *projectTitleRoot {
	if root := gitRepoRootForDoctor(cwd); root != "" {
		return &projectTitleRoot{Source: "git repo root", Path: root}
	}
	if root := terminalTitleProjectRootFromConfigLayers(codexHome, cwd, opts); root != nil {
		return root
	}
	return nil
}

func terminalTitleProjectRootFromConfigLayers(codexHome string, cwd string, opts *Options) *projectTitleRoot {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return nil
	}
	service := config.NewConfigService(codexHome)
	if opts != nil && strings.TrimSpace(opts.Root.Shared.Profile) != "" {
		service.SetProfile(opts.Root.Shared.Profile)
	}
	read, err := service.Read(&config.ConfigReadParams{IncludeLayers: true, CWD: &cwd})
	if err != nil {
		return nil
	}
	for _, layer := range read.Layers {
		if layer.Name.Type != config.LayerSourceProject {
			continue
		}
		dotCodexFolder := strings.TrimSpace(layer.Name.DotCodexFolder)
		if dotCodexFolder == "" {
			continue
		}
		return &projectTitleRoot{Source: "project config", Path: filepath.Dir(dotCodexFolder)}
	}
	return nil
}

func terminalTitleProjectCandidate(root *projectTitleRoot, cwd string) (string, string) {
	if root != nil && strings.TrimSpace(root.Path) != "" {
		source := strings.TrimSpace(root.Source)
		if source == "" {
			source = "project config"
		}
		return source, truncateTitlePart(pathDisplayName(root.Path))
	}
	return "cwd", truncateTitlePart(pathDisplayName(cwd))
}

func pathDisplayName(path string) string {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" || path == "." {
		return path
	}
	base := filepath.Base(path)
	if base == "." || base == string(filepath.Separator) {
		return path
	}
	return base
}

func truncateTitlePart(value string) string {
	runes := []rune(value)
	if len(runes) <= projectTitleMaxChars || projectTitleMaxChars <= 3 {
		return value
	}
	return string(runes[:projectTitleMaxChars-3]) + "..."
}

func customCAEnvDetail(name string, value string) (CheckStatus, string, string) {
	path := strings.TrimSpace(value)
	info, err := os.Stat(path)
	switch {
	case err != nil:
		return CheckStatusWarning, "custom CA env var points at an unreadable path", fmt.Sprintf("%s: %s (%v)", name, path, err)
	case info.IsDir():
		return CheckStatusWarning, "custom CA env var does not point at a file", fmt.Sprintf("%s: not a file %s", name, path)
	}
	file, err := os.Open(path)
	if err != nil {
		return CheckStatusWarning, "custom CA env var points at an unreadable file", fmt.Sprintf("%s: %s (%v)", name, path, err)
	}
	defer file.Close()
	buffer := []byte{0}
	if _, err := file.Read(buffer); err != nil && !errors.Is(err, io.EOF) {
		return CheckStatusWarning, "custom CA env var points at an unreadable file", fmt.Sprintf("%s: %s (%v)", name, path, err)
	}
	return CheckStatusOK, "", fmt.Sprintf("%s: readable file %s", name, path)
}

func stateCheck(codexHome string, opts *Options) *DoctorCheck {
	cfg, _ := loadEffectiveConfigForDoctor(codexHome, opts)
	logDir := logDirForDoctor(codexHome, cfg)
	sqliteHome := sqliteHomeForDoctor(codexHome, opts)
	details := []string{}
	pushPathReadinessDetail(&details, "CODEX_HOME", codexHome)
	pushPathReadinessDetail(&details, "log dir", logDir)
	pushPathReadinessDetail(&details, "sqlite home", sqliteHome)

	integrityFailures := []string{}
	for _, dbPath := range runtimeDBPathsForDoctor(sqliteHome) {
		pushPathReadinessDetail(&details, dbPath.Label, dbPath.Path)
		sqliteIntegrityDetailForDoctor(&details, &integrityFailures, dbPath.Label, dbPath.Path)
	}
	rolloutStatsDetailsForDoctor(&details, codexHome)
	standaloneReleaseCacheDetailsForDoctor(&details)

	status := CheckStatusOK
	summary := "state paths and databases are inspectable"
	if len(integrityFailures) > 0 {
		status = CheckStatusFail
		summary = "state database integrity check failed"
	}
	check := NewCheck("state.paths", "state", status, summary).DetailsList(details)
	if status == CheckStatusFail {
		check.Remediate("Move the damaged SQLite database aside, then restart the interactive CLI or app server so it can rebuild that runtime database from saved data. Other entry points may not rebuild automatically.")
	}
	return check
}

func pushPathReadinessDetail(details *[]string, label string, path string) {
	info, err := os.Stat(path)
	switch {
	case err == nil:
		kind := "other"
		if info.IsDir() {
			kind = "dir"
		} else if info.Mode().IsRegular() {
			kind = "file"
		}
		*details = append(*details, fmt.Sprintf("%s: %s (%s)", label, path, kind))
	case errors.Is(err, os.ErrNotExist):
		*details = append(*details, fmt.Sprintf("%s: %s (missing)", label, path))
	default:
		*details = append(*details, fmt.Sprintf("%s: %s (%s)", label, path, err.Error()))
	}
}

type runtimeDBPathForDoctor struct {
	Label string
	Path  string
}

func runtimeDBPathsForDoctor(sqliteHome string) []runtimeDBPathForDoctor {
	return []runtimeDBPathForDoctor{
		{Label: "state DB", Path: filepath.Join(sqliteHome, "state_5.sqlite")},
		{Label: "log DB", Path: filepath.Join(sqliteHome, "logs_2.sqlite")},
		{Label: "goals DB", Path: filepath.Join(sqliteHome, "goals_1.sqlite")},
		{Label: "memories DB", Path: filepath.Join(sqliteHome, "memories_1.sqlite")},
	}
}

func sqliteIntegrityDetailForDoctor(details *[]string, integrityFailures *[]string, label string, path string) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		*details = append(*details, fmt.Sprintf("%s integrity: skipped (missing)", label))
		return
	}
	result, err := sqliteIntegrityCheckForDoctor(path)
	if err != nil {
		message := fmt.Sprintf("%s integrity: %s", label, err.Error())
		*integrityFailures = append(*integrityFailures, message)
		*details = append(*details, message)
		return
	}
	if len(result) == 0 {
		message := fmt.Sprintf("%s integrity: empty result", label)
		*integrityFailures = append(*integrityFailures, message)
		*details = append(*details, message)
		return
	}
	allOK := true
	for _, row := range result {
		if row != "ok" {
			allOK = false
			break
		}
	}
	if allOK {
		*details = append(*details, fmt.Sprintf("%s integrity: ok", label))
		return
	}
	message := fmt.Sprintf("%s integrity: %s", label, strings.Join(result, "; "))
	*integrityFailures = append(*integrityFailures, message)
	*details = append(*details, message)
}

func sqliteIntegrityCheckForDoctor(path string) ([]string, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query("PRAGMA integrity_check")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		value := ""
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

type rolloutStatsForDoctor struct {
	Files      uint64
	TotalBytes uint64
	Error      string
}

func rolloutStatsDetailsForDoctor(details *[]string, codexHome string) {
	active := collectRolloutStatsForDoctor(filepath.Join(codexHome, "sessions"))
	archived := collectRolloutStatsForDoctor(filepath.Join(codexHome, "archived_sessions"))
	pushRolloutStatsDetailForDoctor(details, "active rollout files", active)
	pushRolloutStatsDetailForDoctor(details, "archived rollout files", archived)
}

func pushRolloutStatsDetailForDoctor(details *[]string, label string, stats rolloutStatsForDoctor) {
	if stats.Error != "" {
		*details = append(*details, fmt.Sprintf("%s: scan failed (%s)", label, stats.Error))
		return
	}
	average := uint64(0)
	if stats.Files > 0 {
		average = stats.TotalBytes / stats.Files
	}
	*details = append(*details, fmt.Sprintf("%s: %d files, %d total bytes, %d average bytes", label, stats.Files, stats.TotalBytes, average))
}

func collectRolloutStatsForDoctor(root string) rolloutStatsForDoctor {
	stats := rolloutStatsForDoctor{}
	collectRolloutStatsInnerForDoctor(root, &stats)
	return stats
}

func collectRolloutStatsInnerForDoctor(path string, stats *rolloutStatsForDoctor) {
	if stats == nil || stats.Error != "" {
		return
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		stats.Error = err.Error()
		return
	}
	for _, entry := range entries {
		fullPath := filepath.Join(path, entry.Name())
		info, err := entry.Info()
		if err != nil {
			stats.Error = err.Error()
			return
		}
		if info.IsDir() {
			collectRolloutStatsInnerForDoctor(fullPath, stats)
			continue
		}
		if info.Mode().IsRegular() && isRolloutFileForDoctor(fullPath) {
			stats.Files++
			stats.TotalBytes += uint64(info.Size())
		}
	}
}

func isRolloutFileForDoctor(path string) bool {
	return filepath.Ext(path) == ".jsonl" && strings.HasPrefix(filepath.Base(path), "rollout-")
}

func standaloneReleaseCacheDetailsForDoctor(details *[]string) {
	context := install.Current()
	if context == nil || context.Method.Kind != install.InstallStandalone || strings.TrimSpace(context.Method.ReleaseDir) == "" {
		return
	}
	releasesDir := filepath.Dir(context.Method.ReleaseDir)
	entries, err := os.ReadDir(releasesDir)
	if err != nil {
		return
	}
	*details = append(*details, fmt.Sprintf("standalone release cache: %d entries in %s", len(entries), releasesDir))
}

func threadInventoryCheck(codexHome string) *DoctorCheck {
	store := session.NewStore(filepath.Join(codexHome, "threads"))
	page, err := store.List(session.ListOptions{IncludeHistory: false})
	details := []string{"thread store: " + store.Root()}
	if err != nil {
		return NewCheck("state.threads", "threads", CheckStatusWarning, "thread inventory could not be read").
			DetailsList(details).
			Detail("thread store error: " + err.Error())
	}
	rolloutPage, rolloutErr := rollout.ListThreads(codexHome, rollout.ListOptions{PageSize: 1000})
	if rolloutErr == nil {
		details = append(details, fmt.Sprintf("rollout threads: %d", len(rolloutPage.Items)))
	} else {
		details = append(details, "rollout inventory error: "+rolloutErr.Error())
	}
	details = append(details, fmt.Sprintf("thread records: %d", len(page.Records)))
	if rolloutErr != nil {
		return NewCheck("state.threads", "threads", CheckStatusWarning, "rollout inventory could not be read").DetailsList(details)
	}
	return NewCheck("state.threads", "threads", CheckStatusOK, "thread inventory readable").DetailsList(details)
}

func overallStatus(checks []*DoctorCheck) CheckStatus {
	status := CheckStatusOK
	for _, check := range checks {
		if check == nil {
			continue
		}
		if check.Status == CheckStatusFail {
			return CheckStatusFail
		}
		if check.Status == CheckStatusWarning {
			status = CheckStatusWarning
		}
	}
	return status
}

func sortedAnyKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return []string{"none"}
	}
	return keys
}

func currentEnvMap() map[string]string {
	out := map[string]string{}
	for _, item := range os.Environ() {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			out[key] = value
		}
	}
	return out
}

func redactEnvValue(key string, value string) string {
	upper := strings.ToUpper(key)
	if strings.Contains(upper, "TOKEN") || strings.Contains(upper, "KEY") || strings.Contains(upper, "SECRET") {
		return "present"
	}
	return value
}
