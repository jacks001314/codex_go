package config

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// maxCurProjectPathProbes and curProjectSeparators mirror Rust f344a80a3b
// (codex-rs/external-agent-migration/src/detect/sessions/cur.rs): resolving the
// working directory encoded in a Cursor project name probes a bounded set of
// path candidates instead of recursively walking directory trees.
const maxCurProjectPathProbes = 128

var curProjectSeparators = []string{"-", "_", ".", " ", "--", "..", "__", "  ", "+", "@", "&"}

func (s *ConfigService) detectExternalSessionMigration() (ExternalAgentConfigMigrationItem, bool) {
	return s.detectExternalSessionMigrationForSource(externalMigrationSourceClaude, nil)
}

func (s *ConfigService) detectExternalSessionMigrationForSource(source string, params *ExternalAgentConfigDetectParams) (ExternalAgentConfigMigrationItem, bool) {
	home := s.externalAgentHomeForSource(source)
	maxAgeDays := uint32(30)
	maxSessions := uint32(50)
	if params != nil && params.MaxSessionAgeDays != nil {
		maxAgeDays = *params.MaxSessionAgeDays
	}
	if params != nil && params.MaxSessions != nil {
		maxSessions = *params.MaxSessions
	}
	var sessions []SessionMigration
	if source == externalMigrationSourceCursor {
		sessions = discoverExternalCursorSessions(home, s.codexHome, maxAgeDays, maxSessions)
	} else {
		sessions = discoverExternalSessionsWithLimits(home, s.codexHome, maxAgeDays, maxSessions)
	}
	if len(sessions) == 0 {
		return ExternalAgentConfigMigrationItem{}, false
	}
	return ExternalAgentConfigMigrationItem{
		ItemType:    MigrationSessions,
		Description: "Migrate recent sessions from " + filepath.Join(home, "projects"),
		Details:     &MigrationDetails{Sessions: sessions},
	}, true
}

func discoverExternalSessions(home string) []SessionMigration {
	return discoverExternalSessionsWithLimits(home, "", 30, 50)
}

func discoverExternalSessionsWithLimits(home string, codexHome string, maxAgeDays uint32, maxSessions uint32) []SessionMigration {
	root := filepath.Join(strings.TrimSpace(home), "projects")
	type candidate struct {
		migration SessionMigration
		modified  time.Time
	}
	var candidates []candidate
	cutoff := time.Now().Add(-time.Duration(maxAgeDays) * 24 * time.Hour)
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry == nil || entry.IsDir() ||
			!strings.EqualFold(filepath.Ext(entry.Name()), ".jsonl") {
			return nil
		}
		info, statErr := entry.Info()
		if statErr != nil || info.ModTime().Before(cutoff) {
			return nil
		}
		if externalSessionImportIsCurrent(codexHome, path) {
			return nil
		}
		cwd, title, ok := externalClaudeSessionSummary(path)
		if !ok {
			return nil
		}
		migration := SessionMigration{
			Path:  path,
			CWD:   cwd,
			Title: stringPtrIfNotEmpty(title),
		}
		candidates = append(candidates, candidate{migration: migration, modified: info.ModTime()})
		return nil
	})
	sort.Slice(candidates, func(i, j int) bool {
		if !candidates[i].modified.Equal(candidates[j].modified) {
			return candidates[i].modified.After(candidates[j].modified)
		}
		return candidates[i].migration.Path < candidates[j].migration.Path
	})
	if uint32(len(candidates)) > maxSessions {
		candidates = candidates[:maxSessions]
	}
	sessions := make([]SessionMigration, len(candidates))
	for i := range candidates {
		sessions[i] = candidates[i].migration
	}
	return sessions
}

func externalClaudeSessionSummary(path string) (string, string, bool) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", false
	}
	defer file.Close()
	var cwd, customTitle, aiTitle, fallbackTitle string
	sawMessage, sawTimestamp := false, false
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		var row struct {
			Type        string          `json:"type"`
			CWD         string          `json:"cwd"`
			Timestamp   string          `json:"timestamp"`
			TimestampMS *int64          `json:"timestamp_ms"`
			IsMeta      bool            `json:"isMeta"`
			IsSidechain bool            `json:"isSidechain"`
			CustomTitle string          `json:"customTitle"`
			AITitle     string          `json:"aiTitle"`
			Message     json.RawMessage `json:"message"`
		}
		if json.Unmarshal(scanner.Bytes(), &row) != nil {
			continue
		}
		if cwd == "" {
			cwd = strings.TrimSpace(row.CWD)
		}
		switch row.Type {
		case "custom-title":
			if title := strings.TrimSpace(row.CustomTitle); title != "" {
				customTitle = title
			}
			continue
		case "ai-title":
			if title := strings.TrimSpace(row.AITitle); title != "" {
				aiTitle = title
			}
			continue
		case "user", "assistant":
		default:
			continue
		}
		if row.IsMeta || row.IsSidechain {
			continue
		}
		text := externalCursorMessageText(row.Message)
		if strings.TrimSpace(text) == "" {
			continue
		}
		sawMessage = true
		if row.TimestampMS != nil {
			sawTimestamp = true
		} else if _, parseErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(row.Timestamp)); parseErr == nil {
			sawTimestamp = true
		}
		if fallbackTitle == "" && row.Type == "user" {
			fallbackTitle = externalCursorUnwrapUserQuery(text)
			if len([]rune(fallbackTitle)) > 80 {
				fallbackTitle = string([]rune(fallbackTitle)[:80])
			}
		}
	}
	if cwd == "" || !filepath.IsAbs(cwd) || !sawMessage || !sawTimestamp {
		return "", "", false
	}
	if info, statErr := os.Stat(cwd); statErr != nil || !info.IsDir() {
		return "", "", false
	}
	title := customTitle
	if title == "" {
		title = aiTitle
	}
	if title == "" {
		title = fallbackTitle
	}
	return cwd, strings.TrimSpace(title), true
}

func discoverExternalCursorSessions(home string, codexHome string, maxAgeDays uint32, maxSessions uint32) []SessionMigration {
	type candidate struct {
		migration SessionMigration
		modified  time.Time
	}
	root := filepath.Join(strings.TrimSpace(home), "projects")
	cutoff := time.Now().Add(-time.Duration(maxAgeDays) * 24 * time.Hour)
	var candidates []candidate
	projects, _ := os.ReadDir(root)
	for _, project := range projects {
		if !project.IsDir() {
			continue
		}
		transcripts := filepath.Join(root, project.Name(), "agent-transcripts")
		fallbackCWD := externalCursorProjectCWD(project.Name())
		_ = filepath.WalkDir(transcripts, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry == nil {
				return nil
			}
			if entry.IsDir() && strings.EqualFold(entry.Name(), "subagents") {
				return filepath.SkipDir
			}
			if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".jsonl") {
				return nil
			}
			info, statErr := entry.Info()
			if statErr != nil || info.ModTime().Before(cutoff) {
				return nil
			}
			if externalSessionImportIsCurrent(codexHome, path) {
				return nil
			}
			cwd, title, ok := externalCursorSessionSummary(path, fallbackCWD)
			if !ok {
				return nil
			}
			candidates = append(candidates, candidate{migration: SessionMigration{Path: path, CWD: cwd, Title: stringPtrIfNotEmpty(title)}, modified: info.ModTime()})
			return nil
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if !candidates[i].modified.Equal(candidates[j].modified) {
			return candidates[i].modified.After(candidates[j].modified)
		}
		return candidates[i].migration.Path < candidates[j].migration.Path
	})
	if uint32(len(candidates)) > maxSessions {
		candidates = candidates[:maxSessions]
	}
	sessions := make([]SessionMigration, len(candidates))
	for i := range candidates {
		sessions[i] = candidates[i].migration
	}
	return sessions
}

func externalCursorSessionSummary(path string, fallbackCWD string) (string, string, bool) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", false
	}
	defer file.Close()
	var cwd, title string
	sawMessage := false
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		var row struct {
			Role        string          `json:"role"`
			CWD         string          `json:"cwd"`
			IsMeta      bool            `json:"isMeta"`
			IsSidechain bool            `json:"isSidechain"`
			Message     json.RawMessage `json:"message"`
		}
		if json.Unmarshal(scanner.Bytes(), &row) != nil || row.IsMeta || row.IsSidechain || (row.Role != "user" && row.Role != "assistant") {
			continue
		}
		text := externalCursorMessageText(row.Message)
		if strings.TrimSpace(text) == "" {
			continue
		}
		sawMessage = true
		if cwd == "" {
			cwd = strings.TrimSpace(row.CWD)
		}
		if title == "" && row.Role == "user" {
			title = externalCursorUnwrapUserQuery(text)
			if len([]rune(title)) > 80 {
				title = string([]rune(title)[:80])
			}
		}
	}
	if cwd == "" {
		cwd = fallbackCWD
	}
	if cwd == "" || !sawMessage {
		return "", "", false
	}
	return cwd, strings.TrimSpace(title), true
}

func externalCursorProjectCWD(encoded string) string {
	decoded, ok := decodeCurProjectPath(strings.TrimSpace(encoded))
	if !ok {
		return ""
	}
	return decoded
}

func decodeCurProjectPath(encoded string) (string, bool) {
	path := ""
	if runtime.GOOS == "windows" {
		var rest string
		var ok bool
		path, rest, ok = decodeCurWindowsProjectDrive(encoded)
		if !ok {
			return "", false
		}
		encoded = rest
	} else {
		path = "/"
	}
	encoded = strings.TrimPrefix(encoded, "-")
	for _, component := range strings.Split(encoded, "-") {
		if component == "" || component == "." || component == ".." || strings.ContainsAny(component, `/\:`) {
			return "", false
		}
		path = filepath.Join(path, component)
	}

	matchedPath := ""
	probes := 0
	inspect := func(candidate string) bool {
		if probes >= maxCurProjectPathProbes {
			return false
		}
		probes++
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			if matchedPath != "" && matchedPath != candidate {
				return false
			}
			matchedPath = candidate
		}
		return true
	}
	if !inspect(path) {
		return "", false
	}

	for suffixLength := 2; suffixLength <= 4; suffixLength++ {
		parent := path
		suffix := make([]string, 0, suffixLength)
		for i := 0; i < suffixLength; i++ {
			component := filepath.Base(parent)
			if component == "." || component == string(filepath.Separator) || component == "" {
				break
			}
			suffix = append(suffix, component)
			next := filepath.Dir(parent)
			if next == parent {
				break
			}
			parent = next
		}
		if len(suffix) != suffixLength {
			break
		}
		reverseStrings(suffix)
		for _, separator := range curProjectSeparators {
			candidate := filepath.Join(parent, strings.Join(suffix, separator))
			if !inspect(candidate) {
				return "", false
			}
		}
	}

	ancestor := filepath.Dir(path)
	for {
		rightName := filepath.Base(ancestor)
		if rightName == "." || rightName == string(filepath.Separator) || rightName == "" {
			break
		}
		left := filepath.Dir(ancestor)
		leftName := filepath.Base(left)
		if leftName == "." || leftName == string(filepath.Separator) || leftName == "" {
			break
		}
		prefix := filepath.Dir(left)
		trailing, err := filepath.Rel(ancestor, path)
		if err != nil || trailing == "." || strings.HasPrefix(trailing, "..") {
			return "", false
		}
		for _, separator := range curProjectSeparators {
			mergedPrefix := filepath.Join(prefix, leftName+separator+rightName)
			if probes >= maxCurProjectPathProbes {
				return "", false
			}
			probes++
			if info, err := os.Stat(mergedPrefix); err != nil || !info.IsDir() {
				continue
			}
			if probes >= maxCurProjectPathProbes {
				return "", false
			}
			probes++
			candidate := filepath.Join(mergedPrefix, trailing)
			if info, err := os.Stat(candidate); err != nil || !info.IsDir() {
				return "", false
			}
			if matchedPath != "" && matchedPath != candidate {
				return "", false
			}
			matchedPath = candidate
		}
		ancestor = left
	}
	if matchedPath == "" {
		return "", false
	}
	return matchedPath, true
}

func decodeCurWindowsProjectDrive(encoded string) (string, string, bool) {
	encoded = strings.TrimSpace(encoded)
	if len(encoded) < 3 || encoded[1] != '-' {
		return "", "", false
	}
	drive := encoded[0]
	if !((drive >= 'a' && drive <= 'z') || (drive >= 'A' && drive <= 'Z')) {
		return "", "", false
	}
	return string(drive) + `:\`, encoded[2:], true
}

func reverseStrings(values []string) {
	for i, j := 0, len(values)-1; i < j; i, j = i+1, j-1 {
		values[i], values[j] = values[j], values[i]
	}
}

func externalCursorPathSlug(path string) string {
	path = strings.TrimLeft(path, `/\`)
	var slug strings.Builder
	for _, character := range path {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' {
			slug.WriteRune(character)
		} else {
			slug.WriteByte('-')
		}
	}
	return slug.String()
}

func externalCursorMessageText(raw json.RawMessage) string {
	var value struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	var text string
	if json.Unmarshal(value.Content, &text) == nil {
		return text
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(value.Content, &parts) != nil {
		return ""
	}
	var texts []string
	for _, part := range parts {
		if strings.TrimSpace(part.Text) != "" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n")
}

func externalCursorUnwrapUserQuery(text string) string {
	trimmed := strings.TrimSpace(text)
	start := strings.Index(trimmed, "<user_query>")
	end := strings.LastIndex(trimmed, "</user_query>")
	if start >= 0 && end > start && strings.TrimSpace(trimmed[end+len("</user_query>"):]) == "" {
		return strings.TrimSpace(trimmed[start+len("<user_query>") : end])
	}
	return trimmed
}
