package tui

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// Rust parity: codex-rs/tui/src/git_action_directives.rs. Codex App directives
// embedded in assistant markdown are stripped from the visible response and,
// for Git receipts, collected as structured actions. Whitespace on unchanged
// lines is preserved because Markdown hard breaks and code can depend on it.

// GitActionDirective is the retained legacy alias used by earlier Go callers
// that only needed the action name.
type GitActionDirective string

const (
	GitActionCommit GitActionDirective = "commit"
	GitActionReview GitActionDirective = "review"
)

// GitActionKind selects the Git receipt action a directive requests.
type GitActionKind string

const (
	GitActionStage        GitActionKind = "stage"
	GitActionCommitKind   GitActionKind = "commit"
	GitActionCreateBranch GitActionKind = "create-branch"
	GitActionPush         GitActionKind = "push"
	GitActionCreatePR     GitActionKind = "create-pr"
)

// GitAction is one Git receipt parsed from an assistant response.
type GitAction struct {
	Kind    GitActionKind
	CWD     string
	Branch  string
	URL     string
	IsDraft bool
}

// ParsedAssistantMarkdown is the visible response plus its Git receipts.
type ParsedAssistantMarkdown struct {
	VisibleMarkdown string
	GitActions      []GitAction
}

// LastCreatedBranchCWD returns the working directory of the last
// `::git-create-branch` receipt, which is the branch a later push belongs to.
func (p ParsedAssistantMarkdown) LastCreatedBranchCWD() (string, bool) {
	for index := len(p.GitActions) - 1; index >= 0; index-- {
		if p.GitActions[index].Kind == GitActionCreateBranch {
			return p.GitActions[index].CWD, true
		}
	}
	return "", false
}

// ParseAssistantMarkdown strips assistant directives from markdown and returns
// the visible text with its deduplicated Git receipts. `cwd` shortens
// `::code-comment` file paths, matching Rust's `strip_prefix(cwd)`; an empty cwd
// leaves absolute paths alone.
func ParseAssistantMarkdown(markdown string, cwd string) ParsedAssistantMarkdown {
	gitActions := make([]GitAction, 0, 1)
	seen := map[GitAction]struct{}{}
	visibleLines := make([]string, 0, 8)
	for _, line := range markdownLines(markdown) {
		visibleLine, lineActions, ok := rewriteCodeCommentLine(line, cwd)
		if !ok {
			visibleLine, lineActions = StripLineDirectives(line)
		}
		for _, action := range lineActions {
			if _, duplicate := seen[action]; duplicate {
				continue
			}
			seen[action] = struct{}{}
			gitActions = append(gitActions, action)
		}
		if visibleLine != line {
			visibleLine = trimRightWhitespace(visibleLine)
		}
		visibleLines = append(visibleLines, visibleLine)
	}
	for len(visibleLines) > 0 && strings.TrimSpace(visibleLines[len(visibleLines)-1]) == "" {
		visibleLines = visibleLines[:len(visibleLines)-1]
	}
	return ParsedAssistantMarkdown{
		VisibleMarkdown: strings.Join(visibleLines, "\n"),
		GitActions:      gitActions,
	}
}

// markdownLines splits with Rust's `str::lines` semantics: lines end at '\n', a
// "\r\n" terminator contributes the '\r' to the terminator rather than the
// line, and a final line ending is optional.
func markdownLines(text string) []string {
	lines := strings.Split(text, "\n")
	trailingNewline := lines[len(lines)-1] == ""
	if trailingNewline {
		lines = lines[:len(lines)-1]
	}
	for index := range lines {
		if index < len(lines)-1 || trailingNewline {
			lines[index] = strings.TrimSuffix(lines[index], "\r")
		}
	}
	return lines
}

func trimRightWhitespace(text string) string {
	return strings.TrimRightFunc(text, unicode.IsSpace)
}

// rewriteCodeCommentLine renders a `::code-comment{...}` line as Markdown and
// returns the Git receipts that follow it on the same line. The second result
// is false when the line is not a well-formed code comment, so the caller falls
// back to plain directive stripping.
func rewriteCodeCommentLine(line string, cwd string) (string, []GitAction, bool) {
	content := strings.TrimLeft(line, " \t")
	indent := line[:len(line)-len(content)]
	directive, ok := ParseAssistantDirective(content, QuoteEscapingBackslash)
	if !ok || directive.Name != "code-comment" {
		return "", nil, false
	}
	suffix := content[len(directive.Raw):]
	title, hasTitle := directive.Attributes["title"]
	body, hasBody := directive.Attributes["body"]
	file, hasFile := directive.Attributes["file"]
	if !hasTitle || !hasBody || !hasFile {
		return "", nil, false
	}
	title = strings.TrimSpace(title)
	body = strings.TrimSpace(body)
	file = strings.TrimSpace(file)
	if title == "" || body == "" || file == "" {
		return "", nil, false
	}
	start := directiveInteger(directive, "start")
	if start < 1 {
		start = 1
	}
	end := directiveInteger(directive, "end")
	if end == 0 {
		end = start
	}
	if end < start {
		end = start
	}
	if !titleHasPriority(title) {
		if priority, ok := directiveIntegerInRange(directive, "priority", 0, 3); ok {
			title = fmt.Sprintf("[P%d] %s", priority, title)
		}
	}
	location := fmt.Sprintf("%s:%d", stripDirectivePathPrefix(file, cwd), start)
	if start != end {
		location = fmt.Sprintf("%s:%d-%d", stripDirectivePathPrefix(file, cwd), start, end)
	}
	suffix, actions := StripLineDirectives(suffix)
	return fmt.Sprintf("%s- %s — %s\n%s  %s%s", indent, title, location, indent, body, suffix), actions, true
}

// StripLineDirectives removes every `::git-*` directive from one line and
// returns the line's Git receipts.
func StripLineDirectives(line string) (string, []GitAction) {
	var visible strings.Builder
	actions := make([]GitAction, 0, 1)
	remaining := line
	for {
		start := strings.Index(remaining, "::git-")
		if start < 0 {
			break
		}
		visible.WriteString(remaining[:start])
		source := remaining[start:]
		if directive, ok := ParseAssistantDirective(source, QuoteEscapingLiteral); ok {
			if action, ok := parseGitAction(directive); ok {
				actions = append(actions, action)
			}
			remaining = source[len(directive.Raw):]
			continue
		}
		// A malformed directive is still hidden from the transcript; skip to the
		// first closing brace after the attribute block.
		if _, afterOpen, ok := strings.Cut(source, "{"); ok {
			if _, afterClose, ok := strings.Cut(afterOpen, "}"); ok {
				remaining = afterClose
				continue
			}
		}
		visible.WriteString(source)
		return visible.String(), actions
	}
	visible.WriteString(remaining)
	return visible.String(), actions
}

// directiveInteger returns the parsed integer attribute, or 0 when it is absent
// or malformed. A leading `P`/`p` (the rendered priority form) is ignored, as in
// Rust's `directive_integer`.
func directiveInteger(directive AssistantDirective, name string) int64 {
	value, ok := directive.Attributes[name]
	if !ok {
		return 0
	}
	parsed, err := strconv.ParseInt(strings.TrimLeft(strings.TrimSpace(value), "Pp"), 10, 64)
	if err != nil {
		return 0
	}
	return parsed
}

func directiveIntegerInRange(directive AssistantDirective, name string, low, high int64) (int64, bool) {
	if _, ok := directive.Attributes[name]; !ok {
		return 0, false
	}
	value := directiveInteger(directive, name)
	if value < low || value > high {
		return 0, false
	}
	return value, true
}

// titleHasPriority reports whether the title already carries a `[P0]`..`[P9]`
// prefix, in which case the numeric priority attribute must not add another.
func titleHasPriority(title string) bool {
	trimmed := strings.TrimLeftFunc(title, unicode.IsSpace)
	if len(trimmed) < 4 {
		return false
	}
	return trimmed[0] == '[' &&
		(trimmed[1] == 'P' || trimmed[1] == 'p') &&
		trimmed[2] >= '0' && trimmed[2] <= '9' &&
		trimmed[3] == ']'
}

// stripDirectivePathPrefix shortens a code-comment file path against the session
// working directory and renders it with forward slashes, mirroring Rust's
// `strip_prefix(cwd)` plus the display separator rewrite. Component comparison
// accepts either separator so foreign Windows paths shorten like a Windows host.
func stripDirectivePathPrefix(file string, cwd string) string {
	file = strings.ReplaceAll(file, `\`, "/")
	cwd = strings.ReplaceAll(cwd, `\`, "/")
	cwd = strings.TrimRight(cwd, "/")
	if cwd == "/" {
		return strings.TrimPrefix(file, "/")
	}
	if cwd == "" {
		return file
	}
	if file == cwd {
		return ""
	}
	if rest, ok := strings.CutPrefix(file, cwd+"/"); ok {
		return rest
	}
	return file
}

func parseGitAction(directive AssistantDirective) (GitAction, bool) {
	cwd, ok := directive.Attributes["cwd"]
	if !ok {
		return GitAction{}, false
	}
	action := GitAction{CWD: cwd}
	switch directive.Name {
	case "git-stage":
		action.Kind = GitActionStage
	case "git-commit":
		action.Kind = GitActionCommitKind
	case "git-create-branch", "git-push":
		branch, ok := directive.Attributes["branch"]
		if !ok {
			return GitAction{}, false
		}
		action.Branch = branch
		if directive.Name == "git-create-branch" {
			action.Kind = GitActionCreateBranch
		} else {
			action.Kind = GitActionPush
		}
	case "git-create-pr":
		branch, ok := directive.Attributes["branch"]
		if !ok {
			return GitAction{}, false
		}
		action.Kind = GitActionCreatePR
		action.Branch = branch
		action.URL = directive.Attributes["url"]
		action.IsDraft = directive.Attributes["isDraft"] == "true"
	default:
		return GitAction{}, false
	}
	return action, true
}
