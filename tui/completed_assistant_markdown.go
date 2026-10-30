package tui

// NormalizeCompletedAssistantMarkdown reproduces the visible-markdown
// normalization Rust applies to a completed assistant response in
// `git_action_directives::parse_assistant_markdown` (#48549, #42640): assistant
// directives such as `::git-*` receipts and `::code-comment` blocks are removed
// from the visible text, the response is split with `str::lines` semantics, and
// trailing whitespace-only lines are dropped. Every other byte is preserved
// verbatim, so the trailing whitespace that Markdown hard breaks and code
// padding depend on survives copying and `/copy`.
func NormalizeCompletedAssistantMarkdown(text string) string {
	return NormalizeCompletedAssistantMarkdownForCWD(text, "")
}

// NormalizeCompletedAssistantMarkdownForCWD is the cwd-aware form used when the
// caller knows the session working directory, which lets a `::code-comment`
// file path shorten the way Rust's `strip_prefix(cwd)` does.
func NormalizeCompletedAssistantMarkdownForCWD(text string, cwd string) string {
	if text == "" {
		return ""
	}
	return ParseAssistantMarkdown(FollowupLabels(text), cwd).VisibleMarkdown
}
