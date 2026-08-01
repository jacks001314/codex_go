package appserver

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"codex_go/session"
)

const (
	realtimeStartupContextHeader = "Startup context from Codex.\nThis is background context about recent work and machine/workspace layout. It may be incomplete or stale. Use it to inform responses, and do not repeat it back unless relevant."
	realtimeCurrentThreadBudget  = 1200
	realtimeRecentWorkBudget     = 2200
	realtimeWorkspaceBudget      = 1600
	realtimeNotesBudget          = 300
	realtimeTurnBudget           = 300
	realtimeMaxRecentThreads     = 40
	realtimeMaxRecentGroups      = 8
	realtimeMaxCurrentCWDAsks    = 8
	realtimeMaxOtherCWDAsks      = 5
	realtimeMaxAskRunes          = 240
	realtimeTreeMaxDepth         = 2
	realtimeDirectoryEntryLimit  = 20
)

var realtimeNoisyDirectoryNames = map[string]struct{}{
	".git": {}, ".next": {}, ".pytest_cache": {}, ".ruff_cache": {}, "__pycache__": {},
	"build": {}, "dist": {}, "node_modules": {}, "out": {}, "target": {},
}

func (r *RuntimeRouter) buildRealtimeStartupContext(record *session.Record) string {
	if record == nil {
		return ""
	}
	currentThread := buildRealtimeCurrentThreadSection(record.Items)
	recentWork := ""
	if r != nil && r.services.ThreadRouter != nil && r.services.ThreadRouter.store != nil {
		page, err := r.services.ThreadRouter.store.List(session.ListOptions{
			PageSize:       realtimeMaxRecentThreads,
			SortKey:        session.SortUpdatedAt,
			SortDirection:  session.SortDesc,
			IncludeHistory: true,
		})
		if err == nil && page != nil {
			recentWork = buildRealtimeRecentWorkSection(record.Metadata.CWD, page.Records)
		}
	}
	workspace := buildRealtimeWorkspaceSection(record.Metadata.CWD, realtimeUserHomeDir())
	if currentThread == "" && recentWork == "" && workspace == "" {
		return ""
	}

	parts := []string{realtimeStartupContextHeader}
	if section := formatRealtimeStartupSection("Current Thread", currentThread, realtimeCurrentThreadBudget); section != "" {
		parts = append(parts, section)
	}
	if section := formatRealtimeStartupSection("Recent Work", recentWork, realtimeRecentWorkBudget); section != "" {
		parts = append(parts, section)
	}
	if section := formatRealtimeStartupSection("Machine / Workspace Map", workspace, realtimeWorkspaceBudget); section != "" {
		parts = append(parts, section)
	}
	if section := formatRealtimeStartupSection("Notes", "Built at realtime startup from the current thread history, local thread metadata, and a bounded local workspace scan. This excludes repo memory instructions, AGENTS files, project-doc prompt blends, and memory summaries.", realtimeNotesBudget); section != "" {
		parts = append(parts, section)
	}
	return "<startup_context>\n" + strings.Join(parts, "\n\n") + "\n</startup_context>"
}

type realtimeContextTurn struct {
	user      []string
	assistant []string
}

func buildRealtimeCurrentThreadSection(items []session.Item) string {
	turns := make([]realtimeContextTurn, 0)
	current := realtimeContextTurn{}
	for index := range items {
		item := &items[index]
		text := strings.TrimSpace(realtimeSessionItemText(item))
		if text == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(item.Role)) {
		case "user":
			if isRealtimeContextualUserText(text) {
				continue
			}
			if len(current.user) > 0 || len(current.assistant) > 0 {
				turns = append(turns, current)
				current = realtimeContextTurn{}
			}
			current.user = append(current.user, text)
		case "assistant":
			if len(current.user) == 0 && len(current.assistant) == 0 {
				continue
			}
			current.assistant = append(current.assistant, text)
		default:
			if strings.EqualFold(item.Type, "agentMessage") && (len(current.user) > 0 || len(current.assistant) > 0) {
				author := strings.TrimSpace(item.Name)
				current.assistant = append(current.assistant, fmt.Sprintf("Agent message from %s:\n%s", author, text))
			}
		}
	}
	if len(current.user) > 0 || len(current.assistant) > 0 {
		turns = append(turns, current)
	}
	if len(turns) == 0 {
		return ""
	}

	lines := []string{"Most recent user/assistant turns from this exact thread. Use them for continuity when responding."}
	remaining := realtimeCurrentThreadBudget - realtimeApproxTokenCount(strings.Join(lines, "\n"))
	retained := 0
	for sourceIndex := len(turns) - 1; sourceIndex >= 0 && remaining > 0; sourceIndex-- {
		index := len(turns) - 1 - sourceIndex
		turnLines := make([]string, 0, 7)
		if index == 0 {
			turnLines = append(turnLines, "### Latest turn")
		} else {
			turnLines = append(turnLines, fmt.Sprintf("### Previous turn %d", index))
		}
		turnValue := turns[sourceIndex]
		if len(turnValue.user) > 0 {
			turnLines = append(turnLines, "User:", strings.Join(turnValue.user, "\n\n"))
		}
		if len(turnValue.assistant) > 0 {
			turnLines = append(turnLines, "", "Assistant:", strings.Join(turnValue.assistant, "\n\n"))
		}
		turnBudget := realtimeTurnBudget
		if remaining < turnBudget {
			turnBudget = remaining
		}
		turnText := truncateRealtimeTextToTokenBudget(strings.Join(turnLines, "\n"), turnBudget)
		turnTokens := realtimeApproxTokenCount(turnText)
		if turnTokens == 0 {
			continue
		}
		lines = append(lines, "", turnText)
		remaining -= turnTokens
		retained++
	}
	if retained == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func realtimeSessionItemText(item *session.Item) string {
	if item == nil {
		return ""
	}
	if item.Text != "" {
		return item.Text
	}
	parts := make([]string, 0, len(item.Content))
	for _, part := range item.Content {
		if strings.TrimSpace(part.Text) != "" {
			parts = append(parts, part.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func isRealtimeContextualUserText(text string) bool {
	for _, marker := range []string{
		"<user_instructions>", "<environment_context>", "<additional_context>", "<skill_instructions>",
		"<user_shell_command>", "<turn_aborted>", "<subagent_notification>", "<internal_model_context>",
		"<recommended_plugins>", "<hook_prompt>",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

type realtimeWorkGroup struct {
	path    string
	records []session.Record
}

func buildRealtimeRecentWorkSection(currentCWD string, records []session.Record) string {
	currentGroup := realtimeGitRoot(currentCWD)
	if currentGroup == "" {
		currentGroup = realtimeAbsolutePath(currentCWD)
	}
	groupsByPath := map[string][]session.Record{}
	for _, record := range records {
		group := realtimeGitRoot(record.Metadata.CWD)
		if group == "" {
			group = realtimeAbsolutePath(record.Metadata.CWD)
		}
		if group == "" {
			continue
		}
		groupsByPath[group] = append(groupsByPath[group], record)
	}
	groups := make([]realtimeWorkGroup, 0, len(groupsByPath))
	for path, entries := range groupsByPath {
		sort.Slice(entries, func(i, j int) bool { return entries[i].UpdatedAt.After(entries[j].UpdatedAt) })
		groups = append(groups, realtimeWorkGroup{path: path, records: entries})
	}
	sort.Slice(groups, func(i, j int) bool {
		leftCurrent, rightCurrent := realtimeSamePath(groups[i].path, currentGroup), realtimeSamePath(groups[j].path, currentGroup)
		if leftCurrent != rightCurrent {
			return leftCurrent
		}
		leftTime, rightTime := groups[i].records[0].UpdatedAt, groups[j].records[0].UpdatedAt
		if !leftTime.Equal(rightTime) {
			return leftTime.After(rightTime)
		}
		return groups[i].path < groups[j].path
	})
	if len(groups) > realtimeMaxRecentGroups {
		groups = groups[:realtimeMaxRecentGroups]
	}
	sections := make([]string, 0, len(groups))
	for _, group := range groups {
		if section := formatRealtimeWorkGroup(currentGroup, group); section != "" {
			sections = append(sections, section)
		}
	}
	return strings.Join(sections, "\n\n")
}

func formatRealtimeWorkGroup(currentGroup string, group realtimeWorkGroup) string {
	if len(group.records) == 0 {
		return ""
	}
	latest := group.records[0]
	label := "Directory"
	if realtimeGitRoot(latest.Metadata.CWD) != "" {
		label = "Git repo"
	}
	lines := []string{
		fmt.Sprintf("### %s: %s", label, group.path),
		fmt.Sprintf("Recent sessions: %d", len(group.records)),
		fmt.Sprintf("Latest activity: %s", realtimeRFC3339(latest.UpdatedAt)),
	}
	if branch := strings.TrimSpace(latest.Metadata.Git["branch"]); branch != "" {
		lines = append(lines, "Latest branch: "+branch)
	}
	lines = append(lines, "", "User asks:")
	maxAsks := realtimeMaxOtherCWDAsks
	if realtimeSamePath(group.path, currentGroup) {
		maxAsks = realtimeMaxCurrentCWDAsks
	}
	seen := map[string]struct{}{}
	for _, record := range group.records {
		ask := realtimeFirstUserMessage(&record)
		ask = strings.Join(strings.Fields(ask), " ")
		key := record.Metadata.CWD + ":" + ask
		if ask == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		ask = truncateRealtimeAsk(ask)
		lines = append(lines, fmt.Sprintf("- %s: %s", record.Metadata.CWD, ask))
		if len(seen) == maxAsks {
			break
		}
	}
	if len(lines) <= 5 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func realtimeFirstUserMessage(record *session.Record) string {
	if record == nil {
		return ""
	}
	for index := range record.Items {
		item := &record.Items[index]
		if strings.EqualFold(item.Role, "user") {
			text := strings.TrimSpace(realtimeSessionItemText(item))
			if text != "" && !isRealtimeContextualUserText(text) {
				return text
			}
		}
	}
	return strings.TrimSpace(record.Preview)
}

func truncateRealtimeAsk(value string) string {
	runes := []rune(value)
	if len(runes) <= realtimeMaxAskRunes {
		return value
	}
	return string(runes[:realtimeMaxAskRunes-3]) + "..."
}

func buildRealtimeWorkspaceSection(cwd string, userRoot string) string {
	cwd = realtimeAbsolutePath(cwd)
	if cwd == "" {
		return ""
	}
	gitRoot := realtimeGitRoot(cwd)
	cwdTree := renderRealtimeTree(cwd)
	gitRootTree := []string(nil)
	if gitRoot != "" && !realtimeSamePath(gitRoot, cwd) {
		gitRootTree = renderRealtimeTree(gitRoot)
	}
	userRoot = realtimeAbsolutePath(userRoot)
	userRootTree := []string(nil)
	if userRoot != "" && !realtimeSamePath(userRoot, cwd) && !realtimeSamePath(userRoot, gitRoot) {
		userRootTree = renderRealtimeTree(userRoot)
	}
	if len(cwdTree) == 0 && gitRoot == "" && len(userRootTree) == 0 {
		return ""
	}
	lines := []string{
		"Current working directory: " + cwd,
		"Working directory name: " + realtimeFileName(cwd),
	}
	if gitRoot != "" {
		lines = append(lines, "Git root: "+gitRoot, "Git project: "+realtimeFileName(gitRoot))
	}
	if userRoot != "" {
		lines = append(lines, "User root: "+userRoot)
	}
	if len(cwdTree) > 0 {
		lines = append(lines, "", "Working directory tree:")
		lines = append(lines, cwdTree...)
	}
	if len(gitRootTree) > 0 {
		lines = append(lines, "", "Git root tree:")
		lines = append(lines, gitRootTree...)
	}
	if len(userRootTree) > 0 {
		lines = append(lines, "", "User root tree:")
		lines = append(lines, userRootTree...)
	}
	return strings.Join(lines, "\n")
}

func renderRealtimeTree(root string) []string {
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil
	}
	lines := []string{}
	collectRealtimeTreeLines(root, 0, &lines)
	return lines
}

func collectRealtimeTreeLines(dir string, depth int, lines *[]string) {
	if depth >= realtimeTreeMaxDepth {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	filtered := entries[:0]
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if _, noisy := realtimeNoisyDirectoryNames[name]; noisy {
			continue
		}
		filtered = append(filtered, entry)
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].IsDir() != filtered[j].IsDir() {
			return filtered[i].IsDir()
		}
		return filtered[i].Name() < filtered[j].Name()
	})
	limit := len(filtered)
	if limit > realtimeDirectoryEntryLimit {
		limit = realtimeDirectoryEntryLimit
	}
	for _, entry := range filtered[:limit] {
		indent := strings.Repeat("  ", depth)
		suffix := ""
		if entry.IsDir() {
			suffix = "/"
		}
		*lines = append(*lines, fmt.Sprintf("%s- %s%s", indent, entry.Name(), suffix))
		if entry.IsDir() {
			collectRealtimeTreeLines(filepath.Join(dir, entry.Name()), depth+1, lines)
		}
	}
	if len(filtered) > realtimeDirectoryEntryLimit {
		*lines = append(*lines, fmt.Sprintf("%s- ... %d more entries", strings.Repeat("  ", depth), len(filtered)-realtimeDirectoryEntryLimit))
	}
}

func formatRealtimeStartupSection(title string, body string, budget int) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	heading := "## " + title + "\n"
	bodyBudget := budget - realtimeApproxTokenCount(heading)
	if bodyBudget <= 0 {
		return ""
	}
	body = truncateRealtimeTextToTokenBudget(body, bodyBudget)
	if body == "" {
		return ""
	}
	return heading + body
}

func truncateRealtimeTextToTokenBudget(text string, budget int) string {
	truncationBudget := budget
	for {
		candidate := truncateRealtimeMiddleTokens(text, truncationBudget)
		candidateTokens := realtimeApproxTokenCount(candidate)
		if candidateTokens <= budget {
			return candidate
		}
		excess := candidateTokens - budget
		if excess < 1 {
			excess = 1
		}
		nextBudget := truncationBudget - excess
		if nextBudget <= 0 {
			candidate = truncateRealtimeMiddleTokens(text, 0)
			if realtimeApproxTokenCount(candidate) <= budget {
				return candidate
			}
			return ""
		}
		truncationBudget = nextBudget
	}
}

func truncateRealtimeMiddleTokens(text string, maxTokens int) string {
	if text == "" {
		return ""
	}
	maxBytes := maxTokens * 4
	if maxTokens > 0 && len(text) <= maxBytes {
		return text
	}
	if maxBytes == 0 {
		return fmt.Sprintf("…%d tokens truncated…", realtimeApproxTokenCount(text))
	}
	leftBudget, rightBudget := maxBytes/2, maxBytes-maxBytes/2
	prefixEnd := 0
	for index, value := range text {
		end := index + utf8.RuneLen(value)
		if end > leftBudget {
			break
		}
		prefixEnd = end
	}
	tailTarget := len(text) - rightBudget
	if tailTarget < 0 {
		tailTarget = 0
	}
	suffixStart := len(text)
	for index := range text {
		if index >= tailTarget {
			suffixStart = index
			break
		}
	}
	if suffixStart < prefixEnd {
		suffixStart = prefixEnd
	}
	removedTokens := (len(text) - maxBytes + 3) / 4
	return text[:prefixEnd] + fmt.Sprintf("…%d tokens truncated…", removedTokens) + text[suffixStart:]
}

func realtimeApproxTokenCount(text string) int {
	return (len(text) + 3) / 4
}

func realtimeGitRoot(path string) string {
	path = realtimeAbsolutePath(path)
	if path == "" {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
			return path
		}
		parent := filepath.Dir(path)
		if parent == path {
			return ""
		}
		path = parent
	}
}

func realtimeAbsolutePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(absolute)
}

func realtimeSamePath(left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}

func realtimeFileName(path string) string {
	name := filepath.Base(filepath.Clean(path))
	if name == "." || name == string(filepath.Separator) || name == "" {
		return path
	}
	return name
}

func realtimeUserHomeDir() string {
	home, _ := os.UserHomeDir()
	return home
}

func realtimeRFC3339(value time.Time) string {
	if value.IsZero() {
		value = time.Now().UTC()
	}
	return value.UTC().Format("2006-01-02T15:04:05+00:00")
}
