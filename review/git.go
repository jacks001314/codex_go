package review

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type GitDiffProvider struct {
	Dir string
}

type CommitEntry struct {
	Subject string
	SHA     string
}

func LocalBranches(dir string) (string, []string, error) {
	provider := &GitDiffProvider{Dir: dir}
	current, _ := provider.git("branch", "--show-current")
	output, err := provider.git("for-each-ref", "--format=%(refname:short)", "refs/heads")
	if err != nil {
		return "", nil, err
	}
	branches := []string{}
	for _, line := range strings.Split(output, "\n") {
		branch := strings.TrimSpace(line)
		if branch == "" {
			continue
		}
		branches = append(branches, branch)
	}
	return strings.TrimSpace(current), branches, nil
}

func RecentCommits(dir string, limit int) ([]CommitEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	provider := &GitDiffProvider{Dir: dir}
	output, err := provider.git("log", "--max-count="+strconv.Itoa(limit), "--format=%H%x00%s")
	if err != nil {
		return nil, err
	}
	entries := []CommitEntry{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\x00", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
			continue
		}
		entries = append(entries, CommitEntry{SHA: strings.TrimSpace(parts[0]), Subject: strings.TrimSpace(parts[1])})
	}
	return entries, nil
}

func (p *GitDiffProvider) Diff(target Target) (string, error) {
	switch target.Kind {
	case "uncommitted":
		return p.uncommittedDiff()
	case "base":
		return p.git("diff", "--no-ext-diff", "--binary", target.Base+"...HEAD")
	case "commit":
		return p.git("show", "--no-ext-diff", "--binary", "--format=medium", target.Commit)
	default:
		return "", nil
	}
}

func (p *GitDiffProvider) MergeBaseWithHead(branch string) (string, error) {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return "", nil
	}
	if _, err := p.git("rev-parse", "--show-toplevel"); err != nil {
		return "", err
	}
	head, ok, err := p.resolveGitRef("HEAD")
	if err != nil || !ok {
		return "", err
	}
	branchRef, ok, err := p.resolveGitRef(branch)
	if err != nil || !ok {
		return "", err
	}
	preferredRef := branchRef
	upstream, err := p.upstreamIfRemoteAhead(branch)
	if err != nil {
		return "", err
	}
	if upstream != "" {
		if upstreamRef, ok, err := p.resolveGitRef(upstream); err != nil {
			return "", err
		} else if ok {
			preferredRef = upstreamRef
		}
	}
	mergeBase, err := p.git("merge-base", head, preferredRef)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(mergeBase), nil
}

func (p *GitDiffProvider) resolveGitRef(ref string) (string, bool, error) {
	output, err := p.git("rev-parse", "--verify", ref)
	if err != nil {
		return "", false, nil
	}
	output = strings.TrimSpace(output)
	if output == "" {
		return "", false, nil
	}
	return output, true, nil
}

func (p *GitDiffProvider) upstreamIfRemoteAhead(branch string) (string, error) {
	upstream, err := p.git("rev-parse", "--abbrev-ref", "--symbolic-full-name", branch+"@{upstream}")
	if err != nil {
		return "", nil
	}
	upstream = strings.TrimSpace(upstream)
	if upstream == "" {
		return "", nil
	}
	counts, err := p.git("rev-list", "--left-right", "--count", branch+"..."+upstream)
	if err != nil {
		return "", nil
	}
	parts := strings.Fields(counts)
	if len(parts) < 2 {
		return "", nil
	}
	right, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || right <= 0 {
		return "", nil
	}
	return upstream, nil
}

func (p *GitDiffProvider) uncommittedDiff() (string, error) {
	unstaged, err := p.git("diff", "--no-ext-diff", "--binary")
	if err != nil {
		return "", err
	}
	staged, err := p.git("diff", "--cached", "--no-ext-diff", "--binary")
	if err != nil {
		return "", err
	}
	untracked, err := p.untrackedDiff()
	if err != nil {
		return "", err
	}
	var builder strings.Builder
	appendSection(&builder, unstaged)
	appendSection(&builder, staged)
	appendSection(&builder, untracked)
	return builder.String(), nil
}

func (p *GitDiffProvider) untrackedDiff() (string, error) {
	output, err := p.git("ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return "", err
	}
	if output == "" {
		return "", nil
	}
	var builder strings.Builder
	for _, name := range strings.Split(output, "\x00") {
		if name == "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(p.Dir, filepath.FromSlash(name)))
		if err != nil {
			return "", fmt.Errorf("failed to read untracked file %s: %w", name, err)
		}
		writeUntrackedFileDiff(&builder, name, data)
	}
	return builder.String(), nil
}

func (p *GitDiffProvider) git(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if p.Dir != "" {
		cmd.Dir = p.Dir
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = strings.TrimSpace(stdout.String())
		}
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("git %s failed: %s", strings.Join(args, " "), message)
	}
	return stdout.String(), nil
}

func appendSection(builder *strings.Builder, section string) {
	section = strings.TrimRight(section, "\n")
	if section == "" {
		return
	}
	if builder.Len() > 0 {
		builder.WriteString("\n\n")
	}
	builder.WriteString(section)
}

func writeUntrackedFileDiff(builder *strings.Builder, name string, data []byte) {
	if builder.Len() > 0 {
		builder.WriteString("\n\n")
	}
	builder.WriteString("diff --git a/")
	builder.WriteString(name)
	builder.WriteString(" b/")
	builder.WriteString(name)
	builder.WriteString("\nnew file mode 100644\n--- /dev/null\n+++ b/")
	builder.WriteString(name)
	builder.WriteString("\n@@\n")
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	for _, line := range strings.SplitAfter(text, "\n") {
		builder.WriteByte('+')
		builder.WriteString(strings.TrimSuffix(line, "\n"))
		builder.WriteByte('\n')
	}
	if len(data) == 0 {
		builder.WriteByte('+')
		builder.WriteByte('\n')
	}
}
