package prompt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	codexshell "codex_go/shell"
)

const (
	InstructionsDefaultAgentsMDFilename = "AGENTS.md"
	InstructionsLocalAgentsMDFilename   = "AGENTS.override.md"
	InstructionsAgentsMDSeparator       = "\n\n--- project-doc ---\n\n"
)

type InstructionsProvenanceKind string

const (
	InstructionsProvenanceUser     InstructionsProvenanceKind = "user"
	InstructionsProvenanceProject  InstructionsProvenanceKind = "project"
	InstructionsProvenanceInternal InstructionsProvenanceKind = "internal"
)

type InstructionsEntry struct {
	Contents      string
	Provenance    InstructionsProvenanceKind
	SourcePath    string
	EnvironmentID string
	CWD           string
}

type LoadedInstructions struct {
	UserInstructions string
	Entries          []InstructionsEntry
}

func NewLoadedUserInstructions(contents string, path string) *LoadedInstructions {
	if strings.TrimSpace(contents) == "" {
		return &LoadedInstructions{}
	}
	return &LoadedInstructions{
		UserInstructions: contents,
		Entries:          []InstructionsEntry{{Contents: contents, Provenance: InstructionsProvenanceUser, SourcePath: path}},
	}
}

func InstructionsFromText(contents string) *LoadedInstructions {
	if strings.TrimSpace(contents) == "" {
		return &LoadedInstructions{}
	}
	return &LoadedInstructions{Entries: []InstructionsEntry{{Contents: contents, Provenance: InstructionsProvenanceInternal}}}
}

func (l *LoadedInstructions) IsEmpty() bool {
	if l == nil {
		return true
	}
	if strings.TrimSpace(l.UserInstructions) != "" {
		return false
	}
	for _, entry := range l.Entries {
		if strings.TrimSpace(entry.Contents) != "" {
			return false
		}
	}
	return true
}

func (l *LoadedInstructions) Text() string {
	if l == nil {
		return ""
	}
	if l.hasMultipleProjectEnvironments() {
		return l.environmentLabeledText()
	}
	return l.legacyText()
}

func (l *LoadedInstructions) legacyText() string {
	var b strings.Builder
	hasPrevious := false
	previousWasProject := false
	if strings.TrimSpace(l.UserInstructions) != "" {
		b.WriteString(l.UserInstructions)
		hasPrevious = true
	}
	for _, entry := range l.Entries {
		if strings.TrimSpace(entry.Contents) == "" || entry.Provenance == InstructionsProvenanceUser {
			continue
		}
		isProject := entry.Provenance == InstructionsProvenanceProject
		if hasPrevious {
			if isProject && !previousWasProject {
				b.WriteString(InstructionsAgentsMDSeparator)
			} else {
				b.WriteString("\n\n")
			}
		}
		b.WriteString(entry.Contents)
		hasPrevious = true
		previousWasProject = isProject
	}
	return b.String()
}

func (l *LoadedInstructions) environmentLabeledText() string {
	var b strings.Builder
	if strings.TrimSpace(l.UserInstructions) != "" {
		b.WriteString(l.UserInstructions)
	}
	for _, entry := range l.Entries {
		if strings.TrimSpace(entry.Contents) == "" || entry.Provenance != InstructionsProvenanceProject {
			continue
		}
		if b.Len() > 0 {
			b.WriteString(InstructionsAgentsMDSeparator)
		}
		b.WriteString("Environment: ")
		b.WriteString(entry.EnvironmentID)
		if entry.CWD != "" {
			b.WriteString(" (cwd: ")
			b.WriteString(entry.CWD)
			b.WriteString(")")
		}
		b.WriteString("\n")
		b.WriteString(entry.Contents)
	}
	return b.String()
}

func (l *LoadedInstructions) hasMultipleProjectEnvironments() bool {
	seen := map[string]bool{}
	for _, entry := range l.Entries {
		if entry.Provenance == InstructionsProvenanceProject && entry.EnvironmentID != "" {
			seen[entry.EnvironmentID] = true
		}
	}
	return len(seen) > 1
}

type InstructionsLoadConfig struct {
	CWD              string
	MaxBytes         int
	RootMarkers      []string
	FallbackNames    []string
	EnvironmentID    string
	UserInstructions string
	// DenyRead, when set, reports whether the active filesystem permissions
	// reject reading a discovered instruction path. A denied instruction file
	// fails loading instead of silently omitting it (Rust #39653).
	DenyRead func(path string) bool
}

func LoadProjectInstructions(config InstructionsLoadConfig) (*LoadedInstructions, error) {
	loaded := &LoadedInstructions{UserInstructions: config.UserInstructions}
	if config.MaxBytes == 0 {
		if loaded.IsEmpty() {
			return nil, nil
		}
		return loaded, nil
	}
	paths, err := InstructionsAgentsMDPaths(config.CWD, config.RootMarkers, config.FallbackNames)
	if err != nil {
		return nil, err
	}
	remaining := config.MaxBytes
	if remaining < 0 {
		remaining = 1 << 30
	}
	for _, path := range paths {
		if remaining == 0 {
			break
		}
		if config.DenyRead != nil && config.DenyRead(path) {
			return nil, fmt.Errorf("failed to load AGENTS.md instructions: %s is not readable under the active filesystem permissions", path)
		}
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if len(data) > remaining {
			data = data[:remaining]
		}
		text := string(data)
		if strings.TrimSpace(text) != "" {
			loaded.Entries = append(loaded.Entries, InstructionsEntry{
				Contents:      text,
				Provenance:    InstructionsProvenanceProject,
				SourcePath:    path,
				EnvironmentID: config.EnvironmentID,
				CWD:           config.CWD,
			})
			remaining -= len(data)
		}
	}
	if loaded.IsEmpty() {
		return nil, nil
	}
	return loaded, nil
}

func InstructionsAgentsMDPaths(cwd string, rootMarkers []string, fallbackNames []string) ([]string, error) {
	if cwd == "" {
		return nil, nil
	}
	root := FindInstructionsProjectRoot(cwd, rootMarkers)
	dirs := instructionsPathFromRoot(root, cwd)
	names := InstructionsCandidateFilenames(fallbackNames)
	var paths []string
	for _, dir := range dirs {
		for _, name := range names {
			path := filepath.Join(dir, name)
			info, err := os.Stat(path)
			if err == nil && !info.IsDir() {
				paths = append(paths, path)
				break
			}
			if err != nil && !os.IsNotExist(err) {
				return nil, err
			}
		}
	}
	return paths, nil
}

func FindInstructionsProjectRoot(cwd string, markers []string) string {
	if len(markers) == 0 {
		markers = []string{".git"}
	}
	dir := filepath.Clean(cwd)
	for {
		for _, marker := range markers {
			if marker == "" {
				continue
			}
			if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return filepath.Clean(cwd)
		}
		dir = parent
	}
}

func InstructionsCandidateFilenames(fallbackNames []string) []string {
	names := []string{InstructionsLocalAgentsMDFilename, InstructionsDefaultAgentsMDFilename}
	seen := map[string]bool{InstructionsLocalAgentsMDFilename: true, InstructionsDefaultAgentsMDFilename: true}
	for _, name := range fallbackNames {
		if name == "" || seen[name] {
			continue
		}
		names = append(names, name)
		seen[name] = true
	}
	return names
}

type InstructionsManager struct {
	mu               sync.Mutex
	userInstructions string
	lastKey          string
	loaded           *LoadedInstructions
}

func NewInstructionsManager(userInstructions string) *InstructionsManager {
	return &InstructionsManager{userInstructions: strings.TrimSpace(userInstructions)}
}

func (m *InstructionsManager) Refresh(config InstructionsLoadConfig) error {
	config.UserInstructions = m.userInstructions
	key := config.CWD + "\x00" + strings.Join(config.RootMarkers, "\x00") + "\x00" + strings.Join(config.FallbackNames, "\x00") + "\x00" + config.EnvironmentID
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lastKey == key {
		return nil
	}
	loaded, err := LoadProjectInstructions(config)
	if err != nil {
		return err
	}
	m.lastKey = key
	m.loaded = loaded
	return nil
}

func (m *InstructionsManager) Loaded() *LoadedInstructions {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.loaded == nil {
		return nil
	}
	copied := *m.loaded
	copied.Entries = append([]InstructionsEntry(nil), m.loaded.Entries...)
	return &copied
}

type InstructionsSkillMetadata struct {
	Name                    string
	Scope                   string
	Path                    string
	LocatorPath             string
	LocatorKind             string
	Root                    string
	RootOrder               int
	HasRootOrder            bool
	Description             string
	ShortDescription        string
	RoutingMetadata         string
	PluginID                string
	RemotePluginID          string
	Contents                string
	AllowImplicitInvocation *bool
	AuthorityKind           string
	AuthorityID             string
	PackageID               string
	ResourceID              string
	Dependencies            []InstructionsSkillDependency
}

type InstructionsSkillDependency struct {
	Type        string
	Value       string
	Description string
	Transport   string
	Command     string
	URL         string
}

func (s *InstructionsSkillMetadata) AllowsImplicitInvocation() bool {
	if s == nil || s.AllowImplicitInvocation == nil {
		return true
	}
	return *s.AllowImplicitInvocation
}

func BuildInstructionsSkillNameCounts(skills []InstructionsSkillMetadata) map[string]int {
	counts := make(map[string]int, len(skills))
	for _, skill := range skills {
		counts[skill.Name]++
	}
	return counts
}

type SkillMentionInput struct {
	Type string
	Text string
	Name string
	Path string
}

type ExplicitSkillMentionOptions struct {
	Inputs              []SkillMentionInput
	Skills              []InstructionsSkillMetadata
	ConnectorSlugCounts map[string]int
}

func CollectExplicitSkillMentions(options *ExplicitSkillMentionOptions) []InstructionsSkillMetadata {
	if options == nil || len(options.Inputs) == 0 || len(options.Skills) == 0 {
		return nil
	}
	skillNameCounts := BuildInstructionsSkillNameCounts(options.Skills)
	selected := make([]InstructionsSkillMetadata, 0)
	seenNames := map[string]bool{}
	seenPaths := map[string]bool{}
	blockedPlainNames := map[string]bool{}

	for i := range options.Inputs {
		input := options.Inputs[i]
		if input.Type != "skill" {
			continue
		}
		blockedPlainNames[input.Name] = true
		path := normalizeMentionSkillPath(input.Path)
		if path == "" || seenPaths[path] {
			continue
		}
		for _, skill := range options.Skills {
			if skillMentionPathSeen(skill, seenPaths) || !skillMatchesMentionPath(skill, path) {
				continue
			}
			selected = append(selected, skill)
			markSkillMentionPaths(skill, seenPaths)
			seenPaths[path] = true
			seenNames[skill.Name] = true
			break
		}
	}

	for i := range options.Inputs {
		input := options.Inputs[i]
		if input.Text == "" {
			continue
		}
		mentions := extractToolMentions(input.Text)
		if mentions.isEmpty() {
			continue
		}
		for _, skill := range options.Skills {
			if skillMentionPathSeen(skill, seenPaths) {
				continue
			}
			if skillMatchesAnyMentionPath(skill, mentions.skillPaths) {
				selected = append(selected, skill)
				markSkillMentionPaths(skill, seenPaths)
				seenNames[skill.Name] = true
			}
		}
		for _, skill := range options.Skills {
			if strings.TrimSpace(skill.Name) == "" || blockedPlainNames[skill.Name] || seenNames[skill.Name] {
				continue
			}
			if skillMentionPathSeen(skill, seenPaths) {
				continue
			}
			if !mentions.plainNames[skill.Name] {
				continue
			}
			if skillNameCounts[skill.Name] != 1 {
				continue
			}
			if options.ConnectorSlugCounts[strings.ToLower(skill.Name)] != 0 {
				continue
			}
			selected = append(selected, skill)
			markSkillMentionPaths(skill, seenPaths)
			seenNames[skill.Name] = true
		}
	}
	return selected
}

func skillMentionPaths(skill InstructionsSkillMetadata) []string {
	values := []string{
		normalizeMentionSkillPath(skill.Path),
		normalizeMentionSkillPath(skill.LocatorPath),
	}
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func skillMatchesMentionPath(skill InstructionsSkillMetadata, path string) bool {
	path = normalizeMentionSkillPath(path)
	if path == "" {
		return false
	}
	for _, candidate := range skillMentionPaths(skill) {
		if candidate == path {
			return true
		}
	}
	return false
}

func skillMatchesAnyMentionPath(skill InstructionsSkillMetadata, paths map[string]bool) bool {
	if len(paths) == 0 {
		return false
	}
	for _, candidate := range skillMentionPaths(skill) {
		if paths[candidate] {
			return true
		}
	}
	return false
}

func skillMentionPathSeen(skill InstructionsSkillMetadata, seen map[string]bool) bool {
	if len(seen) == 0 {
		return false
	}
	for _, candidate := range skillMentionPaths(skill) {
		if seen[candidate] {
			return true
		}
	}
	return false
}

func markSkillMentionPaths(skill InstructionsSkillMetadata, seen map[string]bool) {
	for _, candidate := range skillMentionPaths(skill) {
		seen[candidate] = true
	}
}

func DetectImplicitSkillInvocationForCommand(skills []InstructionsSkillMetadata, command string, workdir string) *InstructionsSkillMetadata {
	workdir = canonicalizeImplicitSkillPath(workdir)
	tokens := codexshell.SplitCommandLine(command)
	if codexshell.IsPowerShellReadCommand(command) {
		if powerShellTokens := codexshell.TokenizePowerShellCommand(command); len(powerShellTokens) > 0 {
			tokens = powerShellTokens
		}
	}

	byScriptsDir := make(map[string]InstructionsSkillMetadata, len(skills))
	bySkillDocPath := make(map[string]InstructionsSkillMetadata, len(skills))
	for _, skill := range skills {
		if skill.Path == "" || strings.Contains(skill.Path, "://") {
			continue
		}
		docPath := canonicalizeImplicitSkillPath(skill.Path)
		bySkillDocPath[docPath] = skill
		scriptsDir := canonicalizeImplicitSkillPath(filepath.Join(filepath.Dir(skill.Path), "scripts"))
		byScriptsDir[scriptsDir] = skill
	}

	if scriptToken := implicitSkillScriptToken(tokens); scriptToken != "" {
		scriptPath := canonicalizeImplicitSkillPath(resolveImplicitSkillPath(workdir, scriptToken))
		for path := scriptPath; ; path = filepath.Dir(path) {
			if skill, ok := byScriptsDir[path]; ok {
				copied := skill
				return &copied
			}
			parent := filepath.Dir(path)
			if parent == path {
				break
			}
		}
	}

	for _, path := range codexshell.ReadPaths(tokens) {
		candidatePath := canonicalizeImplicitSkillPath(resolveImplicitSkillPath(workdir, path))
		if skill, ok := bySkillDocPath[candidatePath]; ok {
			copied := skill
			return &copied
		}
	}
	if path := powershellGetContentSkillPath(command); path != "" {
		candidatePath := canonicalizeImplicitSkillPath(resolveImplicitSkillPath(workdir, path))
		if skill, ok := bySkillDocPath[candidatePath]; ok {
			copied := skill
			return &copied
		}
	}
	return nil
}

// powershellGetContentSkillPath recognizes the PowerShell `Get-Content`
// forms added by Rust #38228 without requiring POSIX shlex tokenization:
// `Get-Content <path>`, `Get-Content -Raw <path>`, and quoted paths containing
// spaces. Windows backslashes are preserved.
func powershellGetContentSkillPath(command string) string {
	arguments := strings.TrimSpace(command)
	if !strings.HasPrefix(arguments, "Get-Content ") {
		return ""
	}
	arguments = strings.TrimSpace(strings.TrimPrefix(arguments, "Get-Content "))
	if rest, ok := strings.CutPrefix(arguments, "-Raw "); ok {
		arguments = strings.TrimSpace(rest)
	}
	if arguments == "" || strings.HasPrefix(arguments, "-") {
		return ""
	}
	if strings.HasPrefix(arguments, `"`) {
		rest := strings.TrimPrefix(arguments, `"`)
		end := strings.IndexByte(rest, '"')
		if end < 0 {
			return ""
		}
		path := rest[:end]
		trailing := rest[end+1:]
		if path == "" || strings.TrimSpace(trailing) != "" {
			return ""
		}
		return path
	}
	path := arguments
	if index := strings.IndexFunc(arguments, func(r rune) bool { return r == ' ' || r == '\t' || r == '\r' || r == '\n' }); index >= 0 {
		path = arguments[:index]
		trailing := arguments[index:]
		if strings.TrimSpace(trailing) != "" {
			return ""
		}
	}
	if path == "" {
		return ""
	}
	return path
}

func implicitSkillScriptToken(tokens []string) string {
	if len(tokens) == 0 {
		return ""
	}
	runner := strings.ToLower(filepath.Base(tokens[0]))
	runner = strings.TrimSuffix(runner, ".exe")
	switch runner {
	case "python", "python3", "bash", "zsh", "sh", "node", "deno", "ruby", "perl", "pwsh":
	default:
		return ""
	}
	for _, token := range tokens[1:] {
		if token == "--" || strings.HasPrefix(token, "-") {
			continue
		}
		lower := strings.ToLower(token)
		for _, extension := range []string{".py", ".sh", ".js", ".ts", ".rb", ".pl", ".ps1"} {
			if strings.HasSuffix(lower, extension) {
				return token
			}
		}
		return ""
	}
	return ""
}

func resolveImplicitSkillPath(workdir string, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(workdir, path)
}

func canonicalizeImplicitSkillPath(path string) string {
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	return path
}

type toolMentions struct {
	skillPaths map[string]bool
	plainNames map[string]bool
}

func (m *toolMentions) isEmpty() bool {
	return m == nil || len(m.skillPaths) == 0 && len(m.plainNames) == 0
}

func extractToolMentions(text string) *toolMentions {
	mentions := &toolMentions{skillPaths: map[string]bool{}, plainNames: map[string]bool{}}
	bytes := []byte(text)
	for index := 0; index < len(bytes); {
		if bytes[index] == '[' {
			name, path, end, ok := parseLinkedToolMention(text, bytes, index)
			if ok {
				if !isCommonEnvVarMention(name) && !isNonSkillResourcePath(path) {
					if normalized := normalizeMentionSkillPath(path); normalized != "" {
						mentions.skillPaths[normalized] = true
					}
				}
				index = end
				continue
			}
		}
		if bytes[index] != '$' {
			index++
			continue
		}
		nameStart := index + 1
		if nameStart >= len(bytes) || !isMentionNameChar(bytes[nameStart]) {
			index++
			continue
		}
		nameEnd := nameStart + 1
		for nameEnd < len(bytes) && isMentionNameChar(bytes[nameEnd]) {
			nameEnd++
		}
		name := text[nameStart:nameEnd]
		if !isCommonEnvVarMention(name) {
			mentions.plainNames[name] = true
		}
		index = nameEnd
	}
	return mentions
}

func parseLinkedToolMention(text string, bytes []byte, start int) (string, string, int, bool) {
	if start+1 >= len(bytes) || bytes[start] != '[' || bytes[start+1] != '$' {
		return "", "", start + 1, false
	}
	nameStart := start + 2
	if nameStart >= len(bytes) || !isMentionNameChar(bytes[nameStart]) {
		return "", "", start + 1, false
	}
	nameEnd := nameStart + 1
	for nameEnd < len(bytes) && isMentionNameChar(bytes[nameEnd]) {
		nameEnd++
	}
	if nameEnd >= len(bytes) || bytes[nameEnd] != ']' {
		return "", "", start + 1, false
	}
	pathStart := nameEnd + 1
	for pathStart < len(bytes) && isASCIIWhitespace(bytes[pathStart]) {
		pathStart++
	}
	if pathStart >= len(bytes) || bytes[pathStart] != '(' {
		return "", "", start + 1, false
	}
	pathEnd := pathStart + 1
	for pathEnd < len(bytes) && bytes[pathEnd] != ')' {
		pathEnd++
	}
	if pathEnd >= len(bytes) || bytes[pathEnd] != ')' {
		return "", "", start + 1, false
	}
	path := strings.TrimSpace(text[pathStart+1 : pathEnd])
	if path == "" {
		return "", "", start + 1, false
	}
	return text[nameStart:nameEnd], path, pathEnd + 1, true
}

func isMentionNameChar(value byte) bool {
	return value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9' ||
		value == '_' || value == '-' || value == ':'
}

func isCommonEnvVarMention(name string) bool {
	switch strings.ToUpper(name) {
	case "PATH", "HOME", "USER", "SHELL", "PWD", "TMPDIR", "TEMP", "TMP", "LANG", "TERM", "XDG_CONFIG_HOME":
		return true
	default:
		return false
	}
}

func isNonSkillResourcePath(path string) bool {
	return strings.HasPrefix(path, "app://") || strings.HasPrefix(path, "mcp://") || strings.HasPrefix(path, "plugin://")
}

func normalizeMentionSkillPath(path string) string {
	path = strings.TrimPrefix(path, "skill://")
	return path
}

func isASCIIWhitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\v' || b == '\f'
}

func instructionsPathFromRoot(root string, cwd string) []string {
	root = filepath.Clean(root)
	cwd = filepath.Clean(cwd)
	var dirs []string
	for cursor := cwd; ; cursor = filepath.Dir(cursor) {
		dirs = append(dirs, cursor)
		if cursor == root {
			break
		}
		parent := filepath.Dir(cursor)
		if parent == cursor {
			break
		}
	}
	for i, j := 0, len(dirs)-1; i < j; i, j = i+1, j-1 {
		dirs[i], dirs[j] = dirs[j], dirs[i]
	}
	return dirs
}
