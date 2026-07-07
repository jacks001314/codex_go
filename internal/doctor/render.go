package doctor

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

var humanGroups = []struct {
	title      string
	categories []string
}{
	{title: "Environment", categories: []string{"system", "runtime", "install", "search", "git", "terminal", "title", "state", "threads"}},
	{title: "Configuration", categories: []string{"config", "auth", "mcp", "sandbox"}},
	{title: "Updates", categories: []string{"updates"}},
	{title: "Connectivity", categories: []string{"network", "websocket", "reachability"}},
	{title: "Background Server", categories: []string{"app-server"}},
}

func RenderHuman(report *Report, opts *Options) string {
	if opts == nil {
		opts = &Options{}
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Codex Doctor %s\n\n", headerSuffix(report))
	notes := notesForReport(report)
	if len(notes) > 0 {
		out.WriteString("Notes\n")
		for _, note := range notes {
			writeNote(&out, note, opts)
		}
		out.WriteString(separator(opts))
		out.WriteString("\n\n")
	}
	for _, group := range humanGroups {
		checks := checksForGroup(report, group.categories)
		if len(checks) == 0 {
			continue
		}
		fmt.Fprintf(&out, "%s\n", group.title)
		for _, check := range checks {
			writeCheck(&out, check, opts)
		}
		out.WriteByte('\n')
	}
	unknownChecks := checksForUnknownGroup(report)
	if len(unknownChecks) > 0 {
		out.WriteString("Other\n")
		for _, check := range unknownChecks {
			writeCheck(&out, check, opts)
		}
		out.WriteByte('\n')
	}
	out.WriteString(separator(opts))
	out.WriteByte('\n')
	out.WriteString(summaryLine(report, opts))
	out.WriteByte('\n')
	if opts.Summary {
		out.WriteString("Run codex doctor without --summary for detailed diagnostics.\n")
		fmt.Fprintf(&out, "%-34s %s\n", "--all expand truncated lists", "--json redacted report")
	} else {
		fmt.Fprintf(&out, "%-34s %s\n", "--summary compact output", "--all expand truncated lists")
		out.WriteString("--json redacted report\n")
	}
	return out.String()
}

func headerSuffix(report *Report) string {
	version := "v"
	if report != nil {
		version += report.CodexVersion
	}
	runtimeCheck := findCheckByCategory(report, "runtime")
	if platform, ok := doctorDetailValue(runtimeCheck, "platform"); ok {
		return version + " | " + platform
	}
	return version
}

type doctorDisplayStatus string

const (
	doctorDisplayStatusOK      doctorDisplayStatus = "ok"
	doctorDisplayStatusUpdate  doctorDisplayStatus = "update"
	doctorDisplayStatusNote    doctorDisplayStatus = "note"
	doctorDisplayStatusWarning doctorDisplayStatus = "warning"
	doctorDisplayStatusFail    doctorDisplayStatus = "fail"
	doctorDisplayStatusIdle    doctorDisplayStatus = "idle"
)

type doctorNote struct {
	status  doctorDisplayStatus
	name    string
	summary string
}

func checksForGroup(report *Report, categories []string) []*DoctorCheck {
	if report == nil {
		return nil
	}
	allowed := map[string]bool{}
	for _, category := range categories {
		allowed[category] = true
	}
	out := make([]*DoctorCheck, 0, len(report.Checks))
	for _, check := range report.Checks {
		if check != nil && allowed[check.Category] {
			out = append(out, check)
		}
	}
	return out
}

func checksForUnknownGroup(report *Report) []*DoctorCheck {
	if report == nil {
		return nil
	}
	known := map[string]bool{}
	for _, group := range humanGroups {
		for _, category := range group.categories {
			known[category] = true
		}
	}
	out := make([]*DoctorCheck, 0)
	for _, check := range report.Checks {
		if check != nil && !known[check.Category] {
			out = append(out, check)
		}
	}
	return out
}

func writeCheck(out *strings.Builder, check *DoctorCheck, opts *Options) {
	fmt.Fprintf(out, "  %s %-12s %s\n", statusMarker(displayStatusForCheck(check), opts), check.Category, rowSummary(check))
	if opts.Summary {
		return
	}
	for _, detail := range check.Details {
		label, _ := splitDetail(detail)
		switch {
		case label == "PATH git entries":
			writePathEntries(out, check, "PATH git #", opts)
			continue
		case label == "PATH codex entries":
			writePathEntries(out, check, "PATH codex #", opts)
			continue
		case label == "feature flags enabled":
			writeFeatureFlags(out, check, opts)
			continue
		case isDoctorDatabaseLabel(label):
			writeDatabaseDetail(out, check, label)
			continue
		case isDoctorDatabaseIntegrityLabel(label):
			continue
		case label == "active rollout files":
			writeRolloutDetail(out, "active rollouts", detail)
			continue
		case label == "archived rollout files":
			writeRolloutDetail(out, "archived rollouts", detail)
			continue
		case strings.HasPrefix(label, "PATH git #") || strings.HasPrefix(label, "PATH codex #"):
			continue
		case label == "enabled feature flags" || label == "feature flag overrides":
			continue
		}
		writeDetail(out, check, detail)
	}
	for _, remedy := range issueRemedies(check) {
		fmt.Fprintf(out, "    -> %s\n", redactDoctorDetail(remedy))
	}
}

func writePathEntries(out *strings.Builder, check *DoctorCheck, prefix string, opts *Options) {
	entries := numberedDetailValues(check, prefix)
	if len(entries) == 0 {
		return
	}
	shown := len(entries)
	if opts == nil || !opts.All {
		shown = minInt(shown, 3)
	}
	fmt.Fprintf(out, "      %-24s %s\n", fmt.Sprintf("PATH entries (%d)", len(entries)), humanizeDoctorDetailValue(redactDoctorDetail(entries[0])))
	for _, entry := range entries[1:shown] {
		fmt.Fprintf(out, "      %-24s %s\n", "", humanizeDoctorDetailValue(redactDoctorDetail(entry)))
	}
	if shown < len(entries) {
		fmt.Fprintf(out, "      %-24s %s\n", "", "... (full list with --all)")
	}
}

func numberedDetailValues(check *DoctorCheck, prefix string) []string {
	if check == nil {
		return nil
	}
	values := []string{}
	for _, detail := range check.Details {
		label, value := splitDetail(detail)
		if strings.HasPrefix(label, prefix) {
			values = append(values, value)
		}
	}
	return values
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func writeFeatureFlags(out *strings.Builder, check *DoctorCheck, opts *Options) {
	enabledCount, ok := doctorDetailValue(check, "feature flags enabled")
	if !ok {
		return
	}
	overrides := doctorListItems(detailValueOrDefault(check, "feature flag overrides", "none"))
	hint := ""
	if (opts == nil || !opts.All) && !doctorDetailIsFalsy(enabledCount) && enabledCount != "0" {
		hint = " (full list with --all)"
	}
	fmt.Fprintf(out, "      %-24s %s\n", "feature flags", fmt.Sprintf("%s enabled | %d overridden%s", enabledCount, len(overrides), hint))
	if len(overrides) > 0 {
		writeListRow(out, "overrides", doctorOverrideNames(overrides), opts)
	}
	if opts != nil && opts.All {
		enabled := doctorListItems(detailValueOrDefault(check, "enabled feature flags", "none"))
		if len(enabled) > 0 {
			writeListRow(out, "enabled flags", enabled, opts)
		}
	}
}

func detailValueOrDefault(check *DoctorCheck, label string, fallback string) string {
	if value, ok := doctorDetailValue(check, label); ok {
		return value
	}
	return fallback
}

func writeListRow(out *strings.Builder, label string, items []string, opts *Options) {
	limit := len(items)
	if opts == nil || !opts.All {
		limit = minInt(limit, 7)
	}
	value := strings.Join(items[:limit], ", ")
	if limit < len(items) {
		if value != "" {
			value += ", "
		}
		value += "... (full list with --all)"
	}
	fmt.Fprintf(out, "      %-24s %s\n", label, value)
}

func doctorListItems(value string) []string {
	if doctorDetailIsFalsy(value) {
		return nil
	}
	items := []string{}
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			items = append(items, item)
		}
	}
	return items
}

func doctorOverrideNames(items []string) []string {
	names := make([]string, 0, len(items))
	for _, item := range items {
		name, _, ok := strings.Cut(item, "=")
		if ok {
			item = name
		}
		names = append(names, strings.TrimSpace(item))
	}
	return names
}

func isDoctorDatabaseLabel(label string) bool {
	switch label {
	case "state DB", "log DB", "goals DB", "memories DB":
		return true
	default:
		return false
	}
}

func isDoctorDatabaseIntegrityLabel(label string) bool {
	return strings.HasSuffix(label, " DB integrity")
}

func writeDatabaseDetail(out *strings.Builder, check *DoctorCheck, label string) {
	value, ok := doctorDetailValue(check, label)
	if !ok {
		return
	}
	value = humanizeDoctorDetailValue(redactDoctorDetail(value))
	if integrity, integrityOK := doctorDetailValue(check, label+" integrity"); integrityOK {
		value += " | integrity " + integrity
	}
	fmt.Fprintf(out, "      %-24s %s\n", label, value)
}

func writeRolloutDetail(out *strings.Builder, label string, detail string) {
	_, value := splitDetail(detail)
	if summary, ok := rolloutSummary(value); ok {
		value = summary
	}
	fmt.Fprintf(out, "      %-24s %s\n", label, value)
}

func rolloutSummary(value string) (string, bool) {
	files, totalBytes, averageBytes, ok := rolloutFilesBytesAndAverage(value)
	if !ok {
		return "", false
	}
	return formatDoctorCount(files) + " files | " + formatDoctorBytes(totalBytes) + " (avg " + formatDoctorBytes(averageBytes) + ")", true
}

func rolloutFilesBytesAndAverage(value string) (uint64, uint64, uint64, bool) {
	filesPart, rest, ok := strings.Cut(value, " files, ")
	if !ok {
		return 0, 0, 0, false
	}
	totalBytesPart, rest, ok := strings.Cut(rest, " total bytes, ")
	if !ok {
		return 0, 0, 0, false
	}
	averageBytesPart, _, ok := strings.Cut(rest, " average bytes")
	if !ok {
		return 0, 0, 0, false
	}
	files, err := strconv.ParseUint(strings.TrimSpace(filesPart), 10, 64)
	if err != nil {
		return 0, 0, 0, false
	}
	totalBytes, err := strconv.ParseUint(strings.TrimSpace(totalBytesPart), 10, 64)
	if err != nil {
		return 0, 0, 0, false
	}
	averageBytes, err := strconv.ParseUint(strings.TrimSpace(averageBytesPart), 10, 64)
	if err != nil {
		return 0, 0, 0, false
	}
	return files, totalBytes, averageBytes, true
}

func writeDetail(out *strings.Builder, check *DoctorCheck, detail string) {
	label, value := splitDetail(redactDoctorDetail(detail))
	label = doctorDisplayLabel(label)
	value = humanizeDoctorDetailValue(value)
	issue := issueForDetailLabel(check, label)
	if issue != nil && issue.Measured != nil {
		value = humanizeDoctorDetailValue(redactDoctorDetail(*issue.Measured))
	}
	if issue != nil && issue.Expected != nil {
		value = value + " (expected " + redactDoctorDetail(*issue.Expected) + ")"
		fmt.Fprintf(out, "    > %-24s %s\n", label, value)
		return
	}
	fmt.Fprintf(out, "      %-24s %s\n", label, value)
}

func doctorDisplayLabel(label string) string {
	switch label {
	case "codex-linux-sandbox helper":
		return "linux helper"
	case "optional reachability failed":
		return "optional reachability"
	case "check for update on startup":
		return "startup update check"
	default:
		return label
	}
}

func issueForDetailLabel(check *DoctorCheck, label string) *DoctorIssue {
	if check == nil {
		return nil
	}
	for _, issue := range check.Issues {
		for _, field := range issue.Fields {
			if field == label || doctorDisplayLabel(field) == label {
				return issue
			}
		}
	}
	return nil
}

func issueRemedies(check *DoctorCheck) []string {
	if check == nil {
		return nil
	}
	seen := map[string]bool{}
	remedies := []string{}
	for _, issue := range check.Issues {
		if issue.Remedy == nil || seen[*issue.Remedy] {
			continue
		}
		seen[*issue.Remedy] = true
		remedies = append(remedies, *issue.Remedy)
	}
	return remedies
}

func humanizeDoctorDetailValue(value string) string {
	if doctorLooksLikePath(value) {
		return shortenDoctorPathPrefix(value)
	}
	if timestamp, ok := humanizeDoctorTimestamp(value); ok {
		return timestamp
	}
	return value
}

func doctorLooksLikePath(value string) bool {
	return strings.HasPrefix(value, "/") || strings.HasPrefix(value, "~/") || strings.HasPrefix(value, "./") || strings.HasPrefix(value, "../")
}

func shortenDoctorPathPrefix(value string) string {
	path, suffix, ok := strings.Cut(value, " (")
	if ok {
		suffix = " (" + suffix
	}
	shortened := homeShortenedDoctorPath(path)
	shortened = middleTruncateDoctorString(shortened, 48)
	return shortened + suffix
}

func homeShortenedDoctorPath(path string) string {
	home := os.Getenv("HOME")
	if home == "" {
		return path
	}
	home = strings.TrimRight(strings.ReplaceAll(home, `\`, `/`), "/")
	normalizedPath := strings.ReplaceAll(path, `\`, `/`)
	if normalizedPath == home {
		return "~"
	}
	if strings.HasPrefix(normalizedPath, home+"/") {
		return "~/" + strings.TrimPrefix(normalizedPath, home+"/")
	}
	return path
}

func middleTruncateDoctorString(value string, maxChars int) string {
	runes := []rune(value)
	if len(runes) <= maxChars {
		return value
	}
	const marker = "..."
	if maxChars <= len(marker) {
		return string(runes[:maxChars])
	}
	headLen := maxChars / 2
	tailLen := maxChars - headLen - len(marker)
	if tailLen < 0 {
		tailLen = 0
	}
	return string(runes[:headLen]) + marker + string(runes[len(runes)-tailLen:])
}

func humanizeDoctorTimestamp(value string) (string, bool) {
	if len(value) < 17 || !strings.HasSuffix(value, "Z") {
		return "", false
	}
	date, timePart, ok := strings.Cut(value, "T")
	if !ok || len(timePart) < 5 {
		return "", false
	}
	return date + " " + timePart[:5] + " UTC", true
}

func writeNote(out *strings.Builder, note *doctorNote, opts *Options) {
	if note == nil {
		return
	}
	fmt.Fprintf(out, "   %s %-12s %s\n", statusMarker(note.status, opts), note.name, redactDoctorDetail(note.summary))
}

func rowSummary(check *DoctorCheck) string {
	if check == nil {
		return ""
	}
	if check.Status != CheckStatusOK && len(check.Issues) > 0 {
		return redactDoctorDetail(check.Issues[0].Cause)
	}
	if check.Status != CheckStatusOK && check.Remediation != nil {
		return check.Summary + " - " + redactDoctorDetail(*check.Remediation)
	}
	return displaySummary(check)
}

func displaySummary(check *DoctorCheck) string {
	if check == nil {
		return ""
	}
	switch check.Category {
	case "system":
		if value, ok := doctorDetailValue(check, "os language"); ok {
			return value
		}
	case "runtime":
		if executable, ok := doctorDetailValue(check, "current executable"); ok && (strings.Contains(executable, "/target/debug/") || strings.Contains(executable, `\target\debug\`)) {
			return "local debug build"
		}
		if value, ok := doctorDetailValue(check, "install method"); ok {
			return value
		}
	case "install":
		if check.Status == CheckStatusOK {
			return "consistent"
		}
	case "search":
		if check.Status == CheckStatusOK {
			readiness, readinessOK := doctorDetailValue(check, "search command readiness")
			provider, providerOK := doctorDetailValue(check, "search provider")
			command, commandOK := doctorDetailValue(check, "search command")
			if readinessOK && providerOK && commandOK {
				return readiness + " (" + provider + ", `" + command + "`)"
			}
		}
	case "git":
		if value, ok := doctorDetailValue(check, "git version"); ok {
			return value
		}
		if value, ok := doctorDetailValue(check, "selected git"); ok {
			return value
		}
	case "terminal":
		if summary := terminalDisplaySummary(check); summary != "" {
			return summary
		}
	case "title":
		if summary := titleDisplaySummary(check); summary != "" {
			return summary
		}
	case "state":
		if check.Status == CheckStatusOK && stateDatabasesHealthy(check) {
			return "databases healthy"
		}
	case "config":
		if check.Status == CheckStatusOK {
			return "loaded"
		}
	case "mcp":
		if summary := mcpDisplaySummary(check); summary != "" {
			return summary
		}
	case "sandbox":
		if summary := sandboxDisplaySummary(check); summary != "" {
			return summary
		}
	case "network":
		if value, ok := doctorDetailValue(check, "proxy env vars"); ok {
			if value == "none" {
				return "no proxy env vars"
			}
			return "proxy env vars present"
		}
	case "websocket":
		if summary := websocketDisplaySummary(check); summary != "" {
			return summary
		}
	case "app-server":
		if summary := appServerDisplaySummary(check); summary != "" {
			return summary
		}
	}
	return check.Summary
}

func terminalDisplaySummary(check *DoctorCheck) string {
	parts := []string{}
	if terminal, ok := doctorDetailValue(check, "terminal"); ok {
		if version, versionOK := doctorDetailValue(check, "terminal version"); versionOK {
			parts = append(parts, terminal+" "+version)
		} else {
			parts = append(parts, terminal)
		}
	}
	if multiplexer, ok := doctorDetailValue(check, "multiplexer"); ok {
		parts = append(parts, multiplexer)
	}
	if term, ok := doctorDetailValue(check, "TERM"); ok {
		parts = append(parts, "TERM="+term)
	}
	return strings.Join(parts, " | ")
}

func titleDisplaySummary(check *DoctorCheck) string {
	source, sourceOK := doctorDetailValue(check, "terminal title source")
	project, projectOK := doctorDetailValue(check, "terminal title project value")
	switch {
	case sourceOK && projectOK:
		return source + " | project " + project
	case sourceOK:
		return source
	default:
		return ""
	}
}

func stateDatabasesHealthy(check *DoctorCheck) bool {
	for _, label := range []string{"state DB integrity", "log DB integrity", "goals DB integrity", "memories DB integrity"} {
		value, ok := doctorDetailValue(check, label)
		if !ok || value != "ok" {
			return false
		}
	}
	return true
}

func mcpDisplaySummary(check *DoctorCheck) string {
	count, ok := doctorDetailValue(check, "configured servers")
	if !ok {
		return ""
	}
	disabled, ok := doctorDetailValue(check, "disabled servers")
	if !ok {
		disabled = "0"
	}
	transports := []string{}
	for _, detail := range check.Details {
		label, value := splitDetail(detail)
		transport, ok := strings.CutSuffix(label, " servers")
		if !ok || transport == "configured" || transport == "disabled" {
			continue
		}
		transports = append(transports, value+" "+transport)
	}
	if len(transports) == 0 {
		return count + " servers | " + disabled + " disabled"
	}
	return count + " server (" + strings.Join(transports, ", ") + ") | " + disabled + " disabled"
}

func sandboxDisplaySummary(check *DoctorCheck) string {
	approval, approvalOK := doctorDetailValue(check, "approval policy")
	filesystem, filesystemOK := doctorDetailValue(check, "filesystem sandbox")
	network, networkOK := doctorDetailValue(check, "network sandbox")
	if !approvalOK || !filesystemOK || !networkOK {
		return ""
	}
	return filesystem + " fs + " + sandboxNetworkNoteValue(network) + " network | approval " + approval
}

func websocketDisplaySummary(check *DoctorCheck) string {
	status, statusOK := doctorDetailValue(check, "handshake result")
	if !statusOK {
		status, statusOK = doctorDetailValue(check, "handshake status")
	}
	timeout, timeoutOK := doctorDetailValue(check, "connect timeout")
	if !statusOK || !timeoutOK {
		return ""
	}
	timeout = strings.ReplaceAll(timeout, "000 ms", "s")
	timeout = strings.ReplaceAll(timeout, " ms", "ms")
	return "connected (" + status + ") | " + timeout + " timeout"
}

func appServerDisplaySummary(check *DoctorCheck) string {
	if check.Category == "app-server" {
		status, statusOK := doctorDetailValue(check, "status")
		mode, modeOK := doctorDetailValue(check, "mode")
		if statusOK && modeOK {
			return status + " (" + mode + " mode)"
		}
	}
	return ""
}

func statusMarker(status doctorDisplayStatus, opts *Options) string {
	if opts != nil && opts.ASCII {
		switch status {
		case doctorDisplayStatusOK:
			return "[ok]"
		case doctorDisplayStatusUpdate:
			return "[up]"
		case doctorDisplayStatusNote, doctorDisplayStatusWarning:
			return "[!!]"
		case doctorDisplayStatusFail:
			return "[XX]"
		case doctorDisplayStatusIdle:
			return "[--]"
		default:
			return "[??]"
		}
	}
	switch status {
	case doctorDisplayStatusOK:
		return "OK "
	case doctorDisplayStatusUpdate:
		return "UP "
	case doctorDisplayStatusNote, doctorDisplayStatusWarning:
		return "!! "
	case doctorDisplayStatusFail:
		return "XX "
	case doctorDisplayStatusIdle:
		return "-- "
	default:
		return "?? "
	}
}

func displayStatusForCheck(check *DoctorCheck) doctorDisplayStatus {
	if check == nil {
		return doctorDisplayStatusNote
	}
	if check.Category == "app-server" && check.Status == CheckStatusOK {
		if status, ok := doctorDetailValue(check, "status"); ok && status == "not running" {
			return doctorDisplayStatusIdle
		}
	}
	switch check.Status {
	case CheckStatusOK:
		return doctorDisplayStatusOK
	case CheckStatusWarning:
		return doctorDisplayStatusWarning
	case CheckStatusFail:
		return doctorDisplayStatusFail
	default:
		return doctorDisplayStatusNote
	}
}

func separator(opts *Options) string {
	if opts != nil && opts.ASCII {
		return strings.Repeat("-", 60)
	}
	return strings.Repeat("-", 60)
}

func summaryLine(report *Report, opts *Options) string {
	var okCount int
	var idleCount int
	var warningCount int
	var failCount int
	notes := notesForReport(report)
	overall := CheckStatus("")
	if report != nil {
		overall = report.OverallStatus
		for _, check := range report.Checks {
			switch displayStatusForCheck(check) {
			case doctorDisplayStatusOK:
				okCount++
			case doctorDisplayStatusIdle:
				idleCount++
			case doctorDisplayStatusWarning:
				warningCount++
			case doctorDisplayStatusFail:
				failCount++
			}
		}
	}
	sep := " | "
	parts := []string{fmt.Sprintf("%d ok", okCount)}
	if idleCount > 0 {
		parts = append(parts, fmt.Sprintf("%d idle", idleCount))
	}
	if len(notes) > 0 {
		parts = append(parts, fmt.Sprintf("%d notes", len(notes)))
	}
	parts = append(parts, fmt.Sprintf("%d warn", warningCount), fmt.Sprintf("%d fail", failCount))
	return fmt.Sprintf("%s %s", strings.Join(parts, sep), doctorOverallStatusLabel(overall))
}

func doctorOverallStatusLabel(status CheckStatus) string {
	switch status {
	case CheckStatusWarning:
		return "degraded"
	case CheckStatusFail:
		return "failed"
	case CheckStatusOK:
		return "ok"
	default:
		return string(status)
	}
}

func splitDetail(detail string) (string, string) {
	label, value, ok := strings.Cut(detail, ":")
	if !ok {
		return detail, ""
	}
	return strings.TrimSpace(label), strings.TrimSpace(value)
}

func notesForReport(report *Report) []*doctorNote {
	if report == nil {
		return nil
	}
	notes := []*doctorNote{}
	if note := updateNote(findCheckByCategory(report, "updates"), report); note != nil {
		notes = append(notes, note)
	}
	if note := rolloutNote(findCheckByCategory(report, "state")); note != nil {
		notes = append(notes, note)
	}
	if note := sandboxNote(findCheckByCategory(report, "sandbox")); note != nil {
		notes = append(notes, note)
	}
	notes = append(notes, nonOKNotes(report)...)
	if note := authReachabilityNote(report); note != nil {
		notes = append(notes, note)
	}
	return notes
}

func findCheckByCategory(report *Report, category string) *DoctorCheck {
	if report == nil {
		return nil
	}
	for _, check := range report.Checks {
		if check != nil && check.Category == category {
			return check
		}
	}
	return nil
}

func updateNote(check *DoctorCheck, report *Report) *doctorNote {
	if check == nil {
		return nil
	}
	status, ok := doctorDetailValue(check, "latest version status")
	if !ok || !strings.Contains(status, "newer version is available") {
		return nil
	}
	latest, ok := doctorDetailValue(check, "latest version")
	if !ok {
		if cached, cachedOK := doctorDetailValue(check, "cached latest version"); cachedOK {
			latest = cached
			ok = true
		}
	}
	if !ok {
		latest = "newer version"
	}
	current := ""
	if report != nil {
		current = report.CodexVersion
	}
	parenthetical := "current " + current
	if dismissed, dismissedOK := doctorDetailValue(check, "dismissed version"); dismissedOK && !doctorDetailIsFalsy(dismissed) {
		parenthetical += ", dismissed " + dismissed
	}
	return &doctorNote{
		status:  doctorDisplayStatusUpdate,
		name:    "updates",
		summary: latest + " available (" + parenthetical + ")",
	}
}

func rolloutNote(check *DoctorCheck) *doctorNote {
	if check == nil {
		return nil
	}
	active, ok := doctorDetailValue(check, "active rollout files")
	if !ok {
		return nil
	}
	files, bytes, ok := rolloutFilesAndBytes(active)
	if !ok || (files < 1000 && bytes < 1024*1024*1024) {
		return nil
	}
	return &doctorNote{
		status:  doctorDisplayStatusWarning,
		name:    "rollouts",
		summary: formatDoctorCount(files) + " active files | " + formatDoctorBytes(bytes) + " on disk",
	}
}

func sandboxNote(check *DoctorCheck) *doctorNote {
	if check == nil {
		return nil
	}
	filesystem, filesystemOK := doctorDetailValue(check, "filesystem sandbox")
	network, networkOK := doctorDetailValue(check, "network sandbox")
	if !filesystemOK || !networkOK {
		return nil
	}
	normalizedNetwork := sandboxNetworkNoteValue(network)
	if filesystem == "restricted" && normalizedNetwork == "restricted" {
		return nil
	}
	return &doctorNote{
		status:  doctorDisplayStatusWarning,
		name:    "sandbox",
		summary: "filesystem " + filesystem + " | network " + normalizedNetwork,
	}
}

func nonOKNotes(report *Report) []*doctorNote {
	if report == nil {
		return nil
	}
	notes := []*doctorNote{}
	for _, check := range report.Checks {
		if check == nil || (check.Status != CheckStatusWarning && check.Status != CheckStatusFail) {
			continue
		}
		notes = append(notes, &doctorNote{
			status:  displayStatusForCheck(check),
			name:    check.Category,
			summary: actionableNoteSummary(check),
		})
	}
	return notes
}

func actionableNoteSummary(check *DoctorCheck) string {
	if check == nil {
		return ""
	}
	if len(check.Issues) > 0 {
		return issueSummary(check)
	}
	if check.Remediation != nil {
		return check.Summary + " - " + *check.Remediation
	}
	return check.Summary
}

func issueSummary(check *DoctorCheck) string {
	if check == nil || len(check.Issues) == 0 {
		return ""
	}
	if len(check.Issues) == 1 {
		return check.Issues[0].Cause
	}
	causes := make([]string, 0, 2)
	for i, issue := range check.Issues {
		if i >= 2 {
			break
		}
		causes = append(causes, issue.Cause)
	}
	return fmt.Sprintf("%d issues - %s", len(check.Issues), strings.Join(causes, "; "))
}

func authReachabilityNote(report *Report) *doctorNote {
	websocket := findCheckByCategory(report, "websocket")
	reachability := findCheckByCategory(report, "reachability")
	if websocket == nil || reachability == nil {
		return nil
	}
	authMode, authOK := doctorDetailValue(websocket, "auth mode")
	reachabilityMode, reachabilityOK := doctorDetailValue(reachability, "reachability mode")
	if !authOK || !reachabilityOK {
		return nil
	}
	if strings.Contains(strings.ToLower(authMode), "chatgpt") && strings.Contains(strings.ToLower(reachabilityMode), "api key") {
		return &doctorNote{
			status:  doctorDisplayStatusWarning,
			name:    "auth",
			summary: "mixed auth signals: ChatGPT login plus API key env var; HTTP reachability uses API-key mode",
		}
	}
	return nil
}

func doctorDetailValue(check *DoctorCheck, label string) (string, bool) {
	if check == nil {
		return "", false
	}
	for _, detail := range check.Details {
		detailLabel, value := splitDetail(detail)
		if detailLabel == label {
			return value, true
		}
	}
	return "", false
}

func doctorDetailIsFalsy(value string) bool {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "", "false", "none", "not set", "unknown", "missing", "absent", "no", "-":
		return true
	default:
		return false
	}
}

func rolloutFilesAndBytes(value string) (uint64, uint64, bool) {
	filesPart, rest, ok := strings.Cut(value, " files, ")
	if !ok {
		return 0, 0, false
	}
	totalBytesPart, _, ok := strings.Cut(rest, " total bytes")
	if !ok {
		return 0, 0, false
	}
	files, err := strconv.ParseUint(strings.TrimSpace(filesPart), 10, 64)
	if err != nil {
		return 0, 0, false
	}
	totalBytes, err := strconv.ParseUint(strings.TrimSpace(totalBytesPart), 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return files, totalBytes, true
}

func formatDoctorBytes(bytes uint64) string {
	const kib = 1024.0
	const mib = kib * 1024.0
	const gib = mib * 1024.0
	value := float64(bytes)
	switch {
	case value >= gib:
		return fmt.Sprintf("%.2f GB", value/gib)
	case value >= mib:
		return fmt.Sprintf("%.2f MB", value/mib)
	case value >= kib:
		return fmt.Sprintf("%.2f KB", value/kib)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

func formatDoctorCount(count uint64) string {
	digits := strconv.FormatUint(count, 10)
	if len(digits) <= 3 {
		return digits
	}
	parts := []string{}
	for len(digits) > 3 {
		parts = append([]string{digits[len(digits)-3:]}, parts...)
		digits = digits[:len(digits)-3]
	}
	parts = append([]string{digits}, parts...)
	return strings.Join(parts, ",")
}

func sandboxNetworkNoteValue(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "restricted":
		return "restricted"
	case "false", "unrestricted", "danger-full-access", "full":
		return "unrestricted"
	default:
		return value
	}
}
