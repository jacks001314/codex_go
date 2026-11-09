// Package gitutil runs git metadata commands with whole-process-tree
// termination on timeout or cancellation, mirroring Rust git-utils
// git_process.rs (Rust 3149fa4b99).
package gitutil

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"codex_go/envutil"
)

const defaultGitTimeout = 5 * time.Second

// SanitizeGitURL strips authentication credentials from a git remote URL
// (Rust #40713 SanitizedGitUrl): URL-style remotes drop their userinfo, and
// SCP-style remotes keep only the conventional `git` SSH user while removing
// all other usernames. The remote-helper prefix and escaped path are preserved.
func SanitizeGitURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("invalid git remote URL")
	}

	// Peel remote-helper prefixes (e.g. `ext::`, `-::`) without recursing.
	address := value
	for {
		transport, nested, ok := strings.Cut(address, "::")
		if !ok || transport == "" {
			break
		}
		valid := true
		for _, ch := range transport {
			if !(ch >= 'a' && ch <= 'z') && !(ch >= 'A' && ch <= 'Z') && !(ch >= '0' && ch <= '9') && ch != '+' && ch != '-' && ch != '.' {
				valid = false
				break
			}
		}
		if !valid {
			break
		}
		if address == value && strings.ContainsAny(nested, " \t\r\n") {
			return "", fmt.Errorf("invalid git remote URL")
		}
		address = nested
	}
	helperPrefix := value[:len(value)-len(address)]

	// URL-style scheme remotes with an authority: strip userinfo.
	if scheme, rest, ok := strings.Cut(address, "://"); ok {
		authorityEnd := len(rest)
		// Rust #60007: Git treats '?' and '#' as authority text on non-HTTP
		// transports, while HTTP(S) URLs end the authority at any of them.
		if scheme == "http" || scheme == "https" {
			if idx := strings.IndexAny(rest, "/?#"); idx >= 0 {
				authorityEnd = idx
			}
		} else if idx := strings.IndexByte(rest, '/'); idx >= 0 {
			authorityEnd = idx
		}
		authority := rest[:authorityEnd]
		path := rest[authorityEnd:]
		user := ""
		if u, p, found := strings.Cut(authority, "@"); found {
			user = u
			authority = p
		}
		// Preserve the conventional `git` SSH user; drop everything else.
		if user != "" && !(strings.EqualFold(scheme, "ssh") && user == "git") {
			user = ""
		}
		if user != "" {
			authority = user + "@" + authority
		}
		return helperPrefix + scheme + "://" + authority + path, nil
	}

	// SCP-style user@host:path.
	if idx := strings.Index(address, "@"); idx > 0 {
		user, hostAndPath := address[:idx], address[idx+1:]
		if strings.Contains(hostAndPath, ":") {
			if user == "git" {
				return helperPrefix + "git@" + hostAndPath, nil
			}
			return helperPrefix + hostAndPath, nil
		}
	}

	// Validate parseable URL forms; otherwise reject malformed remotes.
	if parsed, err := url.Parse(address); err == nil && parsed.Scheme != "" {
		return helperPrefix + address, nil
	}
	return "", fmt.Errorf("invalid git remote URL")
}

// Run executes `git args...` in cwd. On context cancellation or the default
// timeout the entire spawned process tree is terminated, not just the direct
// git process.
func Run(ctx context.Context, cwd string, args ...string) (string, error) {
	return RunWithTimeout(ctx, defaultGitTimeout, cwd, args...)
}

// RunWithTimeout is Run with an explicit timeout.
func RunWithTimeout(ctx context.Context, timeout time.Duration, cwd string, args ...string) (string, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	newCommand := func() *exec.Cmd {
		cmd := exec.Command("git", args...)
		cmd.Dir = cwd
		cmd.Stdin = nil
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		// Rust c4513cb982: git helper processes must not inherit Codex launch
		// context (OPENAI_FEDERATION_RULE_ID / OPENAI_IDENTITY_TOKEN_FILE).
		envutil.ScrubCommandEnv(cmd)
		return cmd
	}
	tree, err := startGitTree(newCommand(), newCommand)
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	done := make(chan error, 1)
	go func() { done <- tree.wait() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		if err != nil {
			detail := strings.TrimSpace(stderr.String())
			if detail != "" {
				return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, detail)
			}
			return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return strings.TrimSpace(stdout.String()), nil
	case <-ctx.Done():
		tree.kill()
		<-done
		return "", ctx.Err()
	case <-timer.C:
		tree.kill()
		<-done
		return "", fmt.Errorf("git %s timed out", strings.Join(args, " "))
	}
}

// IsCancellation reports whether err came from the caller's context or the
// command timeout.
func IsCancellation(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "timed out")
}

// CanonicalizeGitRemoteURL mirrors Rust git-utils
// canonicalize_git_remote_url (#60007): reduce a remote to "host/path",
// dropping credentials, default ports, query strings, fragments and the .git
// suffix. GitHub paths are lowercased; other hosts keep their path case.
func CanonicalizeGitRemoteURL(value string) (string, bool) {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	value = trimGitSuffix(value)
	if value == "" {
		return "", false
	}

	if scheme, rest, ok := strings.Cut(value, "://"); ok {
		defaultPort := ""
		switch scheme {
		case "git":
			defaultPort = "9418"
		case "http":
			defaultPort = "80"
		case "https":
			defaultPort = "443"
		case "ssh":
			defaultPort = "22"
		default:
			return "", false
		}
		if idx := strings.IndexAny(rest, "?#"); idx >= 0 {
			rest = rest[:idx]
		}
		hostPart, path, ok := strings.Cut(rest, "/")
		if !ok {
			return "", false
		}
		return canonicalizeGitRemoteHostPath(hostPart, path, defaultPort)
	}

	if hostPart, path, ok := parseSCPLikeRemote(value); ok {
		return canonicalizeGitRemoteHostPath(hostPart, path, "")
	}

	hostPart, path, ok := strings.Cut(value, "/")
	if !ok {
		return "", false
	}
	return canonicalizeGitRemoteHostPath(hostPart, path, "")
}

func parseSCPLikeRemote(remote string) (string, string, bool) {
	if slash := strings.IndexByte(remote, '/'); slash >= 0 {
		if colon := strings.IndexByte(remote, ':'); colon < 0 || slash < colon {
			return "", "", false
		}
	}
	hostPart, path, ok := strings.Cut(remote, ":")
	if !ok || hostPart == "" || path == "" {
		return "", "", false
	}
	return hostPart, path, true
}

func canonicalizeGitRemoteHostPath(hostPart, path, defaultPort string) (string, bool) {
	if idx := strings.LastIndex(hostPart, "@"); idx >= 0 {
		hostPart = hostPart[idx+1:]
	}
	host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(hostPart), "/"))
	if defaultPort != "" {
		if idx := strings.LastIndex(host, ":"); idx >= 0 && host[idx+1:] == defaultPort {
			host = host[:idx]
		}
	}
	if host == "" {
		return "", false
	}

	path = trimGitSuffix(strings.Trim(strings.TrimSpace(path), "/"))
	components := make([]string, 0, 3)
	for _, component := range strings.Split(path, "/") {
		if component != "" {
			components = append(components, component)
		}
	}
	if len(components) < 2 {
		return "", false
	}
	owner, repo := components[0], components[1]
	if owner == "." || owner == ".." || repo == "." || repo == ".." {
		return "", false
	}
	joined := strings.Join(components, "/")
	if host == "github.com" {
		joined = strings.ToLower(joined)
	}
	return host + "/" + joined, true
}

func trimGitSuffix(value string) string {
	return strings.TrimSuffix(value, ".git")
}
