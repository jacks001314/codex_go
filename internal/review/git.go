package review

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type GitDiffProvider struct {
	Dir string
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
