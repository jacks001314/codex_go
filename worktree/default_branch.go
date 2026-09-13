package worktree

import (
	"bytes"
	"errors"
	"strings"
	"unicode/utf8"
)

// Rust parity: codex-rs/worktree/src/git.rs::default_worktree_base.

// worktreeDefaultBranch is one `git for-each-ref` line: the ref name and, for a
// symbolic ref, its target.
type worktreeDefaultBranch struct {
	name   string
	target string
}

// precedes mirrors Rust's min_by_key(|(name, _)| (!name.starts_with(b"refs/remotes/origin/"), *name)):
// an origin remote HEAD sorts before any other remote, then by ref name.
func (b worktreeDefaultBranch) precedes(other worktreeDefaultBranch) bool {
	originFirst := strings.HasPrefix(b.name, "refs/remotes/origin/")
	otherOriginFirst := strings.HasPrefix(other.name, "refs/remotes/origin/")
	if originFirst != otherOriginFirst {
		return originFirst
	}
	return b.name < other.name
}

// DefaultWorktreeBase resolves the repository's cached default branch without
// fetching: prefer a remote HEAD (origin first, then the smallest ref name),
// then the conventional origin/main, origin/master, main, or master refs. The
// returned value is a ref name; only the chosen base has to be valid UTF-8, so
// unrelated refs may contain arbitrary bytes.
func DefaultWorktreeBase(cwd string) (string, error) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return "", errors.New("the project's working directory is required")
	}
	output, err := gitOutput(cwd, "for-each-ref", "--format=%(refname) %(symref)", "refs/remotes", "refs/heads")
	if err != nil {
		return "", err
	}
	branches := make([]worktreeDefaultBranch, 0, 16)
	for _, line := range bytes.Split(output, []byte("\n")) {
		line = bytes.TrimSuffix(line, []byte("\r"))
		separator := bytes.IndexByte(line, ' ')
		if separator < 0 {
			continue
		}
		branches = append(branches, worktreeDefaultBranch{
			name:   string(line[:separator]),
			target: string(line[separator+1:]),
		})
	}

	base := ""
	head := -1
	for index := range branches {
		if !strings.HasSuffix(branches[index].name, "/HEAD") || branches[index].target == "" {
			continue
		}
		if head < 0 || branches[index].precedes(branches[head]) {
			head = index
		}
	}
	if head >= 0 {
		base = branches[head].target
	} else {
		for _, candidate := range []string{
			"refs/remotes/origin/main",
			"refs/remotes/origin/master",
			"refs/heads/main",
			"refs/heads/master",
		} {
			found := false
			for index := range branches {
				if branches[index].name == candidate {
					found = true
					break
				}
			}
			if found {
				base = candidate
				break
			}
		}
	}
	if base == "" {
		return "", errors.New("could not determine the project's default branch")
	}
	if !utf8.ValidString(base) {
		return "", errors.New("the project's default branch is not valid UTF-8")
	}
	return base, nil
}
