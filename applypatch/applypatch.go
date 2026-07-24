package applypatch

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const LarkGrammar = `start: begin_patch hunk+ end_patch
begin_patch: "*** Begin Patch" LF
end_patch: "*** End Patch" LF?

hunk: add_hunk | delete_hunk | update_hunk
add_hunk: "*** Add File: " filename LF add_line+
delete_hunk: "*** Delete File: " filename LF
update_hunk: "*** Update File: " filename LF change_move? change?

filename: /(.+)/
add_line: "+" /(.*)/ LF -> line

change_move: "*** Move to: " filename LF
change: (change_context | change_line)+ eof_line?
change_context: ("@@" | "@@ " /(.+)/) LF
change_line: ("+" | "-" | " ") /(.*)/ LF
eof_line: "*** End of File" LF

%import common.LF
`

var ErrInvalidPatch = errors.New("invalid apply_patch")

type ErrorKind string

const (
	ErrorKindGrammar ErrorKind = "grammar"
	ErrorKindApply   ErrorKind = "apply"
)

type ChangeKind string

const (
	ChangeAdd    ChangeKind = "add"
	ChangeDelete ChangeKind = "delete"
	ChangeUpdate ChangeKind = "update"
)

type Change struct {
	Kind        ChangeKind
	Path        string
	MovePath    string
	Content     string
	UnifiedDiff string
}

type Action struct {
	EnvironmentID string
	CWD           string
	Hunks         []Change
	Changes       map[string]Change
}

type ApplyOptions struct {
	CWD string
}

type ApplyResult struct {
	Updated []AppliedFile
	Changes []AppliedChange
}

type AppliedFile struct {
	Kind   ChangeKind
	Path   string
	Change AppliedChange
}

type AppliedChange struct {
	Kind               ChangeKind
	Path               string
	MovePath           string
	OldContent         string
	NewContent         string
	OverwrittenContent *string
}

type ToolSpec struct {
	Name        string
	Description string
	Format      FreeformToolFormat
}

type FreeformToolFormat struct {
	Type       string
	Syntax     string
	Definition string
}

func CreateFreeformTool(includeEnvironmentID bool) *ToolSpec {
	definition := LarkGrammar
	if includeEnvironmentID {
		definition = strings.Replace(
			definition,
			"start: begin_patch hunk+ end_patch",
			"start: begin_patch environment_id? hunk+ end_patch\nenvironment_id: \"*** Environment ID: \" filename LF",
			1,
		)
	}
	return &ToolSpec{
		Name:        "apply_patch",
		Description: "Use the `apply_patch` tool to edit files. This is a FREEFORM tool, so do not wrap the patch in JSON.",
		Format: FreeformToolFormat{
			Type:       "grammar",
			Syntax:     "lark",
			Definition: definition,
		},
	}
}

func Parse(input string) (*Action, error) {
	lines := splitLines(input)
	if len(lines) < 2 || lines[0] != "*** Begin Patch" {
		return nil, fmt.Errorf("%w: missing begin marker", ErrInvalidPatch)
	}
	action := &Action{Changes: make(map[string]Change)}
	i := 1
	if i < len(lines) && strings.HasPrefix(lines[i], "*** Environment ID: ") {
		action.EnvironmentID = strings.TrimPrefix(lines[i], "*** Environment ID: ")
		if strings.TrimSpace(action.EnvironmentID) == "" {
			return nil, fmt.Errorf("%w: empty environment id", ErrInvalidPatch)
		}
		i++
	}
	for i < len(lines) {
		line := lines[i]
		if line == "*** End Patch" {
			if len(action.Changes) == 0 {
				return nil, fmt.Errorf("%w: patch contains no hunks", ErrInvalidPatch)
			}
			return action, nil
		}
		switch {
		case strings.HasPrefix(line, "*** Add File: "):
			next, change, err := parseAdd(lines, i)
			if err != nil {
				return nil, err
			}
			action.addChange(change)
			i = next
		case strings.HasPrefix(line, "*** Delete File: "):
			path := strings.TrimPrefix(line, "*** Delete File: ")
			if err := validatePath(path); err != nil {
				return nil, err
			}
			action.addChange(Change{Kind: ChangeDelete, Path: path})
			i++
		case strings.HasPrefix(line, "*** Update File: "):
			next, change, err := parseUpdate(lines, i)
			if err != nil {
				return nil, err
			}
			action.addChange(change)
			i = next
		default:
			return nil, fmt.Errorf("%w: unexpected line %q", ErrInvalidPatch, line)
		}
	}
	return nil, fmt.Errorf("%w: missing end marker", ErrInvalidPatch)
}

func (a *Action) IsEmpty() bool {
	return a == nil || len(a.Hunks) == 0
}

func (a *Action) FilePaths() []string {
	if a == nil {
		return nil
	}
	seen := make(map[string]bool, len(a.Hunks))
	paths := make([]string, 0, len(a.Hunks))
	for _, change := range a.Hunks {
		if !seen[change.Path] {
			paths = append(paths, change.Path)
			seen[change.Path] = true
		}
		if change.Kind == ChangeUpdate && change.MovePath != "" {
			if !seen[change.MovePath] {
				paths = append(paths, change.MovePath)
				seen[change.MovePath] = true
			}
		}
	}
	return paths
}

func (a *Action) ToProtocol() map[string]Change {
	if a == nil {
		return nil
	}
	out := make(map[string]Change, len(a.Changes))
	for path, change := range a.Changes {
		out[path] = change
	}
	return out
}

func (a *Action) FillDeleteContent(options *ApplyOptions) error {
	if a == nil {
		return nil
	}
	cwd := "."
	if options != nil && strings.TrimSpace(options.CWD) != "" {
		cwd = options.CWD
	}
	for index := range a.Hunks {
		if a.Hunks[index].Kind != ChangeDelete || a.Hunks[index].Content != "" {
			continue
		}
		path, err := resolveWorkspacePath(cwd, a.Hunks[index].Path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read file to delete %s: %w", path, err)
		}
		a.Hunks[index].Content = string(data)
		a.Changes[a.Hunks[index].Path] = a.Hunks[index]
	}
	return nil
}

func (a *Action) Apply(options *ApplyOptions) (*ApplyResult, error) {
	if a == nil || len(a.Hunks) == 0 {
		return nil, fmt.Errorf("%w: patch contains no hunks", ErrInvalidPatch)
	}
	cwd := "."
	if options != nil && strings.TrimSpace(options.CWD) != "" {
		cwd = options.CWD
	}
	if err := a.preflight(cwd); err != nil {
		return nil, err
	}
	return a.applyCommitted(cwd)
}

func (a *Action) applyCommitted(cwd string) (*ApplyResult, error) {
	result := &ApplyResult{}
	for _, change := range a.Hunks {
		applied, err := applyChange(cwd, &change)
		if err != nil {
			return nil, err
		}
		result.Updated = append(result.Updated, *applied)
		result.Changes = append(result.Changes, applied.Change)
	}
	return result, nil
}

func (a *Action) preflight(cwd string) error {
	tempDir, err := os.MkdirTemp("", "codex-apply-patch-preflight-")
	if err != nil {
		return fmt.Errorf("failed to create apply_patch preflight workspace: %w", err)
	}
	defer os.RemoveAll(tempDir)

	shadowPaths := map[string]string{}
	nextShadowPath := 0
	shadowPathFor := func(name string) (string, error) {
		resolved, err := resolveWorkspacePath(cwd, name)
		if err != nil {
			return "", err
		}
		key := filepath.Clean(resolved)
		if existing, ok := shadowPaths[key]; ok {
			return existing, nil
		}
		nextShadowPath++
		shadow := filepath.Join("paths", fmt.Sprintf("%d", nextShadowPath))
		shadowPaths[key] = shadow
		return shadow, nil
	}
	for _, name := range a.FilePaths() {
		source, sourceErr := resolveWorkspacePath(cwd, name)
		if sourceErr != nil {
			return sourceErr
		}
		shadow, shadowErr := shadowPathFor(name)
		if shadowErr != nil {
			return shadowErr
		}
		info, statErr := os.Stat(source)
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil {
			return statErr
		}
		if !info.Mode().IsRegular() {
			continue
		}
		target, targetErr := resolveWorkspacePath(tempDir, shadow)
		if targetErr != nil {
			return targetErr
		}
		data, readErr := os.ReadFile(source)
		if readErr != nil {
			return readErr
		}
		if mkdirErr := os.MkdirAll(filepath.Dir(target), 0o755); mkdirErr != nil {
			return mkdirErr
		}
		if writeErr := os.WriteFile(target, data, info.Mode().Perm()); writeErr != nil {
			return writeErr
		}
	}
	shadowAction := &Action{Hunks: make([]Change, len(a.Hunks)), Changes: map[string]Change{}}
	for index, hunk := range a.Hunks {
		shadowAction.Hunks[index] = hunk
		shadow, shadowErr := shadowPathFor(hunk.Path)
		if shadowErr != nil {
			return shadowErr
		}
		shadowAction.Hunks[index].Path = shadow
		if strings.TrimSpace(hunk.MovePath) != "" {
			moveShadow, moveErr := shadowPathFor(hunk.MovePath)
			if moveErr != nil {
				return moveErr
			}
			shadowAction.Hunks[index].MovePath = moveShadow
		}
	}
	_, err = shadowAction.applyCommitted(tempDir)
	return err
}

func Apply(input string, options *ApplyOptions) (*ApplyResult, error) {
	action, err := Parse(input)
	if err != nil {
		return nil, err
	}
	return action.Apply(options)
}

func Validate(input string) error {
	_, err := Parse(input)
	return err
}

func ClassifyError(err error) ErrorKind {
	if errors.Is(err, ErrInvalidPatch) {
		return ErrorKindGrammar
	}
	return ErrorKindApply
}

func FormatError(err error) string {
	if err == nil {
		return ""
	}
	if strings.Contains(err.Error(), "patch contains no hunks") {
		return "No files were modified."
	}
	return err.Error()
}

func (r *ApplyResult) Summary() string {
	if r == nil || len(r.Updated) == 0 {
		return "No files were modified.\n"
	}
	applied := append([]AppliedFile(nil), r.Updated...)
	sort.SliceStable(applied, func(i, j int) bool {
		left := changeKindPriority(applied[i].Kind)
		right := changeKindPriority(applied[j].Kind)
		if left == right {
			return applied[i].Path < applied[j].Path
		}
		return left < right
	})
	var builder strings.Builder
	builder.WriteString("Success. Updated the following files:\n")
	for _, file := range applied {
		builder.WriteString(file.StatusLetter())
		builder.WriteByte(' ')
		builder.WriteString(filepath.ToSlash(file.Path))
		builder.WriteByte('\n')
	}
	return builder.String()
}

func (f *AppliedFile) StatusLetter() string {
	if f == nil {
		return "M"
	}
	switch f.Kind {
	case ChangeAdd:
		return "A"
	case ChangeDelete:
		return "D"
	default:
		return "M"
	}
}

func changeKindPriority(kind ChangeKind) int {
	switch kind {
	case ChangeAdd:
		return 0
	case ChangeUpdate:
		return 1
	case ChangeDelete:
		return 2
	default:
		return 3
	}
}

func (a *Action) addChange(change Change) {
	if a.Changes == nil {
		a.Changes = make(map[string]Change)
	}
	a.Changes[change.Path] = change
	a.Hunks = append(a.Hunks, change)
}

func applyChange(cwd string, change *Change) (*AppliedFile, error) {
	if change == nil {
		return nil, fmt.Errorf("%w: nil change", ErrInvalidPatch)
	}
	switch change.Kind {
	case ChangeAdd:
		return applyAdd(cwd, change)
	case ChangeDelete:
		return applyDelete(cwd, change)
	case ChangeUpdate:
		return applyUpdate(cwd, change)
	default:
		return nil, fmt.Errorf("%w: unknown change kind %q", ErrInvalidPatch, change.Kind)
	}
}

func applyAdd(cwd string, change *Change) (*AppliedFile, error) {
	path, err := resolveWorkspacePath(cwd, change.Path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(change.Content), 0o600); err != nil {
		return nil, fmt.Errorf("failed to write file %s: %w", path, err)
	}
	return &AppliedFile{
		Kind: ChangeAdd,
		Path: change.Path,
		Change: AppliedChange{
			Kind:       ChangeAdd,
			Path:       change.Path,
			NewContent: change.Content,
		},
	}, nil
}

func applyDelete(cwd string, change *Change) (*AppliedFile, error) {
	path, err := resolveWorkspacePath(cwd, change.Path)
	if err != nil {
		return nil, err
	}
	if info, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("Failed to delete file %s", path)
	} else if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("Failed to delete file %s: not a regular file", path)
	}
	data, _ := os.ReadFile(path)
	if err := os.Remove(path); err != nil {
		return nil, fmt.Errorf("Failed to delete file %s", path)
	}
	return &AppliedFile{
		Kind: ChangeDelete,
		Path: change.Path,
		Change: AppliedChange{
			Kind:       ChangeDelete,
			Path:       change.Path,
			OldContent: string(data),
		},
	}, nil
}

func applyUpdate(cwd string, change *Change) (*AppliedFile, error) {
	path, err := resolveWorkspacePath(cwd, change.Path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file to update %s: %w", path, err)
	}
	updated, err := applyUpdateDiffToContent(string(data), change)
	if err != nil {
		return nil, fmt.Errorf("%w in %s", err, path)
	}
	outPath := path
	outDisplayPath := change.Path
	if change.MovePath != "" {
		outPath, err = resolveWorkspacePath(cwd, change.MovePath)
		if err != nil {
			return nil, err
		}
		outDisplayPath = change.MovePath
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(outPath, []byte(updated), 0o600); err != nil {
		return nil, fmt.Errorf("failed to write file %s: %w", outPath, err)
	}
	if change.MovePath != "" && outPath != path {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("failed to remove original file %s: %w", path, err)
		}
	}
	return &AppliedFile{
		Kind: ChangeUpdate,
		Path: outDisplayPath,
		Change: AppliedChange{
			Kind:       ChangeUpdate,
			Path:       change.Path,
			MovePath:   change.MovePath,
			OldContent: string(data),
			NewContent: updated,
		},
	}, nil
}

func applyUnifiedDiffToContent(content string, diff string) (string, error) {
	chunks, err := parseUpdateChunks(diff)
	if err != nil {
		return "", err
	}
	if len(chunks) == 0 {
		return "", fmt.Errorf("%w: empty update hunk", ErrInvalidPatch)
	}
	current := content
	for _, chunk := range chunks {
		next, ok := replaceFirstChunk(current, &chunk)
		if !ok {
			return "", fmt.Errorf("failed to find expected lines:\n%s", chunk.Old)
		}
		current = next
	}
	return current, nil
}

func applyUpdateDiffToContent(content string, change *Change) (string, error) {
	if change != nil && change.MovePath != "" && strings.TrimSpace(change.UnifiedDiff) == "" {
		return content, nil
	}
	return applyUnifiedDiffToContent(content, change.UnifiedDiff)
}

func replaceFirstChunk(content string, chunk *updateChunk) (string, bool) {
	if chunk == nil {
		return content, false
	}
	if index := strings.Index(content, chunk.Old); index >= 0 {
		return content[:index] + chunk.New + content[index+len(chunk.Old):], true
	}
	oldNoFinalNewline := strings.TrimSuffix(chunk.Old, "\n")
	if oldNoFinalNewline == chunk.Old || oldNoFinalNewline == "" {
		return content, false
	}
	index := strings.LastIndex(content, oldNoFinalNewline)
	if index < 0 || index+len(oldNoFinalNewline) != len(content) {
		return content, false
	}
	return content[:index] + chunk.New, true
}

type updateChunk struct {
	Old string
	New string
}

func parseUpdateChunks(diff string) ([]updateChunk, error) {
	lines := splitLines(diff)
	var chunks []updateChunk
	var oldBuilder strings.Builder
	var newBuilder strings.Builder
	inChunk := false
	flush := func() {
		if !inChunk {
			return
		}
		chunks = append(chunks, updateChunk{Old: oldBuilder.String(), New: newBuilder.String()})
		oldBuilder.Reset()
		newBuilder.Reset()
		inChunk = false
	}
	for _, line := range lines {
		switch {
		case line == "*** End of File":
			continue
		case strings.HasPrefix(line, "@@"):
			flush()
			inChunk = true
		case strings.HasPrefix(line, "-"):
			if !inChunk {
				inChunk = true
			}
			oldBuilder.WriteString(strings.TrimPrefix(line, "-"))
			oldBuilder.WriteByte('\n')
		case strings.HasPrefix(line, "+"):
			if !inChunk {
				inChunk = true
			}
			newBuilder.WriteString(strings.TrimPrefix(line, "+"))
			newBuilder.WriteByte('\n')
		case strings.HasPrefix(line, " "):
			if !inChunk {
				inChunk = true
			}
			value := strings.TrimPrefix(line, " ")
			oldBuilder.WriteString(value)
			oldBuilder.WriteByte('\n')
			newBuilder.WriteString(value)
			newBuilder.WriteByte('\n')
		case strings.TrimSpace(line) == "":
			continue
		default:
			return nil, fmt.Errorf("%w: invalid update line %q", ErrInvalidPatch, line)
		}
	}
	flush()
	filtered := make([]updateChunk, 0, len(chunks))
	for _, chunk := range chunks {
		if chunk.Old == "" && chunk.New == "" {
			continue
		}
		filtered = append(filtered, chunk)
	}
	return filtered, nil
}

func resolveWorkspacePath(cwd string, path string) (string, error) {
	if err := validatePath(path); err != nil {
		return "", err
	}
	if strings.TrimSpace(cwd) == "" {
		cwd = "."
	}
	clean := filepath.Clean(path)
	full := clean
	if !filepath.IsAbs(clean) {
		full = filepath.Join(cwd, clean)
	}
	absFull, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	return absFull, nil
}

func parseAdd(lines []string, start int) (int, Change, error) {
	path := strings.TrimPrefix(lines[start], "*** Add File: ")
	if err := validatePath(path); err != nil {
		return start, Change{}, err
	}
	var content strings.Builder
	i := start + 1
	for i < len(lines) {
		line := lines[i]
		if isHunkOrEnd(line) {
			break
		}
		if !strings.HasPrefix(line, "+") {
			return start, Change{}, fmt.Errorf("%w: add file lines must start with +", ErrInvalidPatch)
		}
		content.WriteString(strings.TrimPrefix(line, "+"))
		content.WriteByte('\n')
		i++
	}
	if i == start+1 {
		return start, Change{}, fmt.Errorf("%w: add file hunk requires content", ErrInvalidPatch)
	}
	return i, Change{Kind: ChangeAdd, Path: path, Content: content.String()}, nil
}

func parseUpdate(lines []string, start int) (int, Change, error) {
	path := strings.TrimPrefix(lines[start], "*** Update File: ")
	if err := validatePath(path); err != nil {
		return start, Change{}, err
	}
	i := start + 1
	movePath := ""
	if i < len(lines) && strings.HasPrefix(lines[i], "*** Move to: ") {
		movePath = strings.TrimPrefix(lines[i], "*** Move to: ")
		if err := validatePath(movePath); err != nil {
			return start, Change{}, err
		}
		i++
	}
	var diff strings.Builder
	for i < len(lines) {
		line := lines[i]
		if isHunkOrEnd(line) {
			break
		}
		if line == "*** End of File" || strings.HasPrefix(line, "@@") || strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") || strings.HasPrefix(line, " ") {
			diff.WriteString(line)
			diff.WriteByte('\n')
			i++
			continue
		}
		return start, Change{}, fmt.Errorf("%w: invalid update line %q", ErrInvalidPatch, line)
	}
	if chunks, err := parseUpdateChunks(diff.String()); err != nil {
		return start, Change{}, err
	} else if len(chunks) == 0 && movePath == "" {
		return start, Change{}, fmt.Errorf("%w: update file hunk for path %q is empty", ErrInvalidPatch, path)
	}
	return i, Change{Kind: ChangeUpdate, Path: path, MovePath: movePath, UnifiedDiff: diff.String()}, nil
}

func splitLines(input string) []string {
	input = strings.ReplaceAll(input, "\r\n", "\n")
	input = strings.ReplaceAll(input, "\r", "\n")
	input = strings.TrimSuffix(input, "\n")
	if input == "" {
		return nil
	}
	return strings.Split(input, "\n")
}

func isHunkOrEnd(line string) bool {
	return line == "*** End Patch" ||
		strings.HasPrefix(line, "*** Add File: ") ||
		strings.HasPrefix(line, "*** Delete File: ") ||
		strings.HasPrefix(line, "*** Update File: ")
}

func validatePath(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("%w: empty path", ErrInvalidPatch)
	}
	clean := filepath.Clean(path)
	if clean == "." {
		return fmt.Errorf("%w: empty path", ErrInvalidPatch)
	}
	return nil
}
