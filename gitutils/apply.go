// Package gitutils shells out to the system git binary for the operations the
// CLI exposes, mirroring Rust's codex-git-utils crate.
package gitutils

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"codex_go/envutil"
)

// SafeBareRepositoryConfig rejects implicitly discovered bare repositories
// while keeping repositories selected through GIT_DIR or --git-dir working.
const SafeBareRepositoryConfig = "safe.bareRepository=explicit"

// ApplyRequest mirrors Rust ApplyGitRequest.
type ApplyRequest struct {
	CWD       string
	Diff      string
	Revert    bool
	Preflight bool
}

// ApplyResult mirrors Rust ApplyGitResult.
type ApplyResult struct {
	ExitCode        int
	AppliedPaths    []string
	SkippedPaths    []string
	ConflictedPaths []string
	Stdout          string
	Stderr          string
	CommandForLog   string
}

// ApplyPatch writes the diff to a temporary file and runs `git apply --3way`,
// parsing the output into applied, skipped and conflicted paths (Rust
// codex_git_utils::apply_git_patch).
func ApplyPatch(req *ApplyRequest) (*ApplyResult, error) {
	if req == nil {
		return nil, fmt.Errorf("apply request is nil")
	}
	cwd := strings.TrimSpace(req.CWD)
	if cwd == "" {
		cwd = "."
	}
	gitRoot, err := ResolveGitRoot(cwd)
	if err != nil {
		return nil, err
	}
	tmpDir, err := os.MkdirTemp("", "codex-apply-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()
	patchPath := filepath.Join(tmpDir, "patch.diff")
	if err := os.WriteFile(patchPath, []byte(req.Diff), 0o600); err != nil {
		return nil, err
	}
	if req.Revert && !req.Preflight {
		// Staging is best effort: it only avoids index mismatches on revert.
		_ = StagePaths(gitRoot, req.Diff)
	}
	gitConfig := applyGitConfig()
	if req.Preflight {
		args := []string{"apply", "--check"}
		if req.Revert {
			args = append(args, "-R")
		}
		args = append(args, patchPath)
		code, stdout, stderr, err := runGit(gitRoot, gitConfig, args)
		if err != nil {
			return nil, err
		}
		applied, skipped, conflicted := ParseGitApplyOutput(stdout, stderr)
		return &ApplyResult{
			ExitCode:        code,
			AppliedPaths:    applied,
			SkippedPaths:    skipped,
			ConflictedPaths: conflicted,
			Stdout:          stdout,
			Stderr:          stderr,
			CommandForLog:   renderCommandForLog(gitRoot, gitConfig, args),
		}, nil
	}
	args := []string{"apply", "--3way"}
	if req.Revert {
		args = append(args, "-R")
	}
	args = append(args, patchPath)
	commandForLog := renderCommandForLog(gitRoot, gitConfig, args)
	code, stdout, stderr, err := runGit(gitRoot, gitConfig, args)
	if err != nil {
		return nil, err
	}
	applied, skipped, conflicted := ParseGitApplyOutput(stdout, stderr)
	return &ApplyResult{
		ExitCode:        code,
		AppliedPaths:    applied,
		SkippedPaths:    skipped,
		ConflictedPaths: conflicted,
		Stdout:          stdout,
		Stderr:          stderr,
		CommandForLog:   commandForLog,
	}, nil
}

// ResolveGitRoot returns the repository root for cwd through
// `git rev-parse --show-toplevel`.
func ResolveGitRoot(cwd string) (string, error) {
	code, stdout, stderr, err := runGit(cwd, nil, []string{"rev-parse", "--show-toplevel"})
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("not a git repository (exit %d): %s", code, stderr)
	}
	root := strings.TrimSpace(stdout)
	if root == "" {
		return "", fmt.Errorf("not a git repository: empty git root")
	}
	return root, nil
}

// StagePaths stages every existing path referenced by the diff (Rust
// codex_git_utils::stage_paths). Failures are ignored by the caller.
func StagePaths(gitRoot, diff string) error {
	var existing []string
	for _, path := range ExtractPathsFromPatch(diff) {
		if _, err := os.Lstat(filepath.Join(gitRoot, path)); err == nil {
			existing = append(existing, path)
		}
	}
	if len(existing) == 0 {
		return nil
	}
	args := append([]string{"add", "--"}, existing...)
	_, _, _, err := runGit(gitRoot, nil, args)
	return err
}

// ExtractPathsFromPatch collects the paths named by `diff --git` headers.
func ExtractPathsFromPatch(diff string) []string {
	seen := map[string]bool{}
	for _, raw := range strings.Split(diff, "\n") {
		line := strings.TrimSpace(raw)
		rest, ok := strings.CutPrefix(line, "diff --git ")
		if !ok {
			continue
		}
		a, b, ok := parseDiffGitPaths(rest)
		if !ok {
			continue
		}
		for _, candidate := range []struct{ raw, prefix string }{{a, "a/"}, {b, "b/"}} {
			if normalized := normalizeDiffPath(candidate.raw, candidate.prefix); normalized != "" {
				seen[normalized] = true
			}
		}
	}
	paths := make([]string, 0, len(seen))
	for path := range seen {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func normalizeDiffPath(raw, prefix string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "/dev/null" || trimmed == prefix+"dev/null" {
		return ""
	}
	if after, ok := strings.CutPrefix(trimmed, prefix); ok && after != "" {
		return after
	}
	return trimmed
}

func parseDiffGitPaths(rest string) (string, string, bool) {
	first, remaining, ok := readDiffGitToken(rest)
	if !ok {
		return "", "", false
	}
	second, _, ok := readDiffGitToken(remaining)
	if !ok {
		return "", "", false
	}
	return first, second, true
}

func readDiffGitToken(input string) (string, string, bool) {
	rest := strings.TrimLeft(input, " \t")
	quote := byte(0)
	if len(rest) > 0 && (rest[0] == '"' || rest[0] == '\'') {
		quote = rest[0]
		rest = rest[1:]
	}
	var out strings.Builder
	i := 0
	for i < len(rest) {
		c := rest[i]
		if quote != 0 {
			if c == quote {
				i++
				break
			}
			out.WriteByte(c)
			i++
			continue
		}
		if c == ' ' || c == '\t' {
			break
		}
		out.WriteByte(c)
		i++
	}
	if out.Len() == 0 && quote == 0 {
		return "", "", false
	}
	return out.String(), rest[i:], true
}

func applyGitConfig() []string {
	value := strings.TrimSpace(os.Getenv("CODEX_APPLY_GIT_CFG"))
	if value == "" {
		return nil
	}
	var config []string
	for _, pair := range strings.Split(value, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" || !strings.Contains(pair, "=") {
			continue
		}
		config = append(config, "-c", pair)
	}
	return config
}

func runGit(cwd string, gitConfig []string, args []string) (int, string, string, error) {
	full := make([]string, 0, len(gitConfig)+len(args)+2)
	full = append(full, gitConfig...)
	full = append(full, "-c", SafeBareRepositoryConfig)
	full = append(full, args...)
	command := exec.Command("git", full...)
	command.Dir = cwd
	envutil.SuppressConsoleWindow(command)
	var stdout, stderr strings.Builder
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	code := 0
	switch {
	case err == nil:
		code = 0
	case command.ProcessState != nil:
		code = command.ProcessState.ExitCode()
	default:
		return -1, stdout.String(), stderr.String(), err
	}
	return code, stdout.String(), stderr.String(), nil
}

func renderCommandForLog(cwd string, gitConfig []string, args []string) string {
	parts := []string{"git"}
	for _, arg := range gitConfig {
		parts = append(parts, quoteShell(arg))
	}
	for _, arg := range args {
		parts = append(parts, quoteShell(arg))
	}
	return fmt.Sprintf("(cd %s && %s)", quoteShell(cwd), strings.Join(parts, " "))
}

func quoteShell(value string) string {
	simple := value != ""
	for _, character := range value {
		switch {
		case character >= 'a' && character <= 'z':
		case character >= 'A' && character <= 'Z':
		case character >= '0' && character <= '9':
		case strings.ContainsRune("-_.:/@%+", character):
		default:
			simple = false
		}
	}
	if simple {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

var (
	appliedClean            = regexp.MustCompile(`(?i)^Applied patch(?: to)?\s+(?P<path>.+?)\s+cleanly\.?$`)
	appliedConflicts        = regexp.MustCompile(`(?i)^Applied patch(?: to)?\s+(?P<path>.+?)\s+with conflicts\.?$`)
	applyingWithRejects     = regexp.MustCompile(`(?i)^Applying patch\s+(?P<path>.+?)\s+with\s+\d+\s+rejects?\.{0,3}$`)
	checkingPatch           = regexp.MustCompile(`(?i)^Checking patch\s+(?P<path>.+?)\.\.\.$`)
	unmergedLine            = regexp.MustCompile(`(?i)^U\s+(?P<path>.+)$`)
	patchFailed             = regexp.MustCompile(`(?i)^error:\s+patch failed:\s+(?P<path>.+?)(?::\d+)?(?:\s|$)`)
	patchDoesNotApply       = regexp.MustCompile(`(?i)^error:\s+(?P<path>.+?):\s+patch does not apply$`)
	threeWayStart           = regexp.MustCompile(`(?i)^(?:Performing three-way merge|Falling back to three-way merge)\.\.\.$`)
	threeWayFailed          = regexp.MustCompile(`(?i)^Failed to perform three-way merge\.\.\.$`)
	fallbackDirect          = regexp.MustCompile(`(?i)^Falling back to direct application\.\.\.$`)
	lacksBlob               = regexp.MustCompile(`(?i)^(?:error: )?repository lacks the necessary blob to (?:perform|fall back on) 3-?way merge\.?$`)
	indexMismatch           = regexp.MustCompile(`(?i)^error:\s+(?P<path>.+?):\s+does not match index\b`)
	notInIndex              = regexp.MustCompile(`(?i)^error:\s+(?P<path>.+?):\s+does not exist in index\b`)
	alreadyExistsWorktree   = regexp.MustCompile(`(?i)^error:\s+(?P<path>.+?)\s+already exists in (?:the )?working directory\b`)
	fileExists              = regexp.MustCompile(`(?i)^error:\s+patch failed:\s+(?P<path>.+?)\s+File exists`)
	renamedDeleted          = regexp.MustCompile(`(?i)^error:\s+path\s+(?P<path>.+?)\s+has been renamed/deleted`)
	cannotApplyBinary       = regexp.MustCompile(`(?i)^error:\s+cannot apply binary patch to\s+['"]?(?P<path>.+?)['"]?\s+without full index line$`)
	binaryDoesNotApply      = regexp.MustCompile(`(?i)^error:\s+binary patch does not apply to\s+['"]?(?P<path>.+?)['"]?$`)
	binaryIncorrectResult   = regexp.MustCompile(`(?i)^error:\s+binary patch to\s+['"]?(?P<path>.+?)['"]?\s+creates incorrect result\b`)
	cannotReadCurrent       = regexp.MustCompile(`(?i)^error:\s+cannot read the current contents of\s+['"]?(?P<path>.+?)['"]?$`)
	skippedPatch            = regexp.MustCompile(`(?i)^Skipped patch\s+['"]?(?P<path>.+?)['"]\.$`)
	cannotMergeBinaryWarn   = regexp.MustCompile(`(?i)^warning:\s*Cannot merge binary files:\s+(?P<path>.+?)\s+\(ours\s+vs\.\s+theirs\)`)
)

// unescapeCString resolves the C-style escapes git uses when it quotes paths.
func unescapeCString(input string) string {
	var builder strings.Builder
	builder.Grow(len(input))
	runes := []rune(input)
	for i := 0; i < len(runes); i++ {
		character := runes[i]
		if character != '\\' {
			builder.WriteRune(character)
			continue
		}
		if i+1 >= len(runes) {
			builder.WriteRune('\\')
			break
		}
		i++
		next := runes[i]
		switch next {
		case 'n':
			builder.WriteRune('\n')
		case 'r':
			builder.WriteRune('\r')
		case 't':
			builder.WriteRune('\t')
		case 'b':
			builder.WriteRune('\b')
		case 'f':
			builder.WriteRune('\f')
		case 'a':
			builder.WriteRune('\a')
		case 'v':
			builder.WriteRune('\v')
		case '\\':
			builder.WriteRune('\\')
		case '"':
			builder.WriteRune('"')
		case '\'':
			builder.WriteRune('\'')
		case '0', '1', '2', '3', '4', '5', '6', '7':
			value := int(next - '0')
			for consumed := 0; consumed < 2 && i+1 < len(runes); consumed++ {
				following := runes[i+1]
				if following < '0' || following > '7' {
					break
				}
				value = value*8 + int(following-'0')
				i++
			}
			builder.WriteRune(rune(value))
		default:
			builder.WriteRune(next)
		}
	}
	return builder.String()
}

// addApplyPath normalises a captured path: git wraps paths containing special
// characters in single or double quotes, which must be stripped and unescaped.
func addApplyPath(set map[string]bool, raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	first := trimmed[0]
	last := trimmed[len(trimmed)-1]
	unquoted := trimmed
	if (first == '"' || first == '\'') && last == first && len(trimmed) >= 2 {
		unquoted = unescapeCString(trimmed[1 : len(trimmed)-1])
	}
	if unquoted == "" {
		return ""
	}
	set[unquoted] = true
	return unquoted
}

func applySubmatch(pattern *regexp.Regexp, line string) (string, bool) {
	match := pattern.FindStringSubmatch(line)
	if match == nil {
		return "", false
	}
	index := pattern.SubexpIndex("path")
	if index < 0 || index >= len(match) {
		return "", false
	}
	return match[index], true
}

// ParseGitApplyOutput splits `git apply` output into applied, skipped and
// conflicted paths, following Rust's parser (ported from VS Code).
func ParseGitApplyOutput(stdout, stderr string) ([]string, []string, []string) {
	applied := map[string]bool{}
	skipped := map[string]bool{}
	conflicted := map[string]bool{}
	var combined []string
	if stdout != "" {
		combined = append(combined, stdout)
	}
	if stderr != "" {
		combined = append(combined, stderr)
	}
	lastSeenPath := ""
	for _, raw := range strings.Split(strings.Join(combined, "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if path, ok := applySubmatch(checkingPatch, line); ok {
			lastSeenPath = path
			continue
		}
		if path, ok := applySubmatch(appliedClean, line); ok {
			if seen := addApplyPath(applied, path); seen != "" {
				delete(conflicted, seen)
				delete(skipped, seen)
				lastSeenPath = seen
			}
			continue
		}
		if path, ok := applySubmatch(appliedConflicts, line); ok {
			if seen := addApplyPath(conflicted, path); seen != "" {
				delete(applied, seen)
				delete(skipped, seen)
				lastSeenPath = seen
			}
			continue
		}
		if path, ok := applySubmatch(applyingWithRejects, line); ok {
			if seen := addApplyPath(conflicted, path); seen != "" {
				delete(applied, seen)
				delete(skipped, seen)
				lastSeenPath = seen
			}
			continue
		}
		if path, ok := applySubmatch(unmergedLine, line); ok {
			if seen := addApplyPath(conflicted, path); seen != "" {
				delete(applied, seen)
				delete(skipped, seen)
				lastSeenPath = seen
			}
			continue
		}
		if path, ok := applySubmatch(patchFailed, line); ok {
			addApplyPath(skipped, path)
			lastSeenPath = path
			continue
		}
		if path, ok := applySubmatch(patchDoesNotApply, line); ok {
			addApplyPath(skipped, path)
			lastSeenPath = path
			continue
		}
		if threeWayStart.MatchString(line) || fallbackDirect.MatchString(line) {
			continue
		}
		if threeWayFailed.MatchString(line) || lacksBlob.MatchString(line) {
			if lastSeenPath != "" {
				addApplyPath(skipped, lastSeenPath)
				delete(applied, lastSeenPath)
				delete(conflicted, lastSeenPath)
			}
			continue
		}
		handled := false
		for _, pattern := range []*regexp.Regexp{
			indexMismatch,
			notInIndex,
			alreadyExistsWorktree,
			fileExists,
			renamedDeleted,
			cannotApplyBinary,
			binaryDoesNotApply,
			binaryIncorrectResult,
			cannotReadCurrent,
			skippedPatch,
		} {
			path, ok := applySubmatch(pattern, line)
			if !ok {
				continue
			}
			if seen := addApplyPath(skipped, path); seen != "" {
				delete(applied, seen)
				delete(conflicted, seen)
				lastSeenPath = seen
			}
			handled = true
			break
		}
		if handled {
			continue
		}
		if path, ok := applySubmatch(cannotMergeBinaryWarn, line); ok {
			if seen := addApplyPath(conflicted, path); seen != "" {
				delete(applied, seen)
				delete(skipped, seen)
				lastSeenPath = seen
			}
			continue
		}
	}

	for path := range conflicted {
		delete(applied, path)
		delete(skipped, path)
	}
	for path := range applied {
		delete(skipped, path)
	}
	return sortedKeys(applied), sortedKeys(skipped), sortedKeys(conflicted)
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		if key == "" {
			continue
		}
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
