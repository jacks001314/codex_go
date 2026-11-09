package app

import (
	"context"
	"strings"

	"codex_go/cloudclient"
	"codex_go/gitutil"
)

// Rust #60007: resolve the host project into credential-free GitHub checkout
// metadata. Local paths never become cloud executor cwd values or wire fields.

// resolveCloudProjectFromCWD reads the repository origin for cwd and returns
// the managed-environment project when it is a supported GitHub remote.
func resolveCloudProjectFromCWD(ctx context.Context, cwd string) *cloudclient.ProjectEnvironment {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return nil
	}
	origin, err := gitutil.Run(ctx, cwd, "remote", "get-url", "origin")
	if err != nil {
		return nil
	}
	return githubCloudProject(origin)
}

func githubCloudProject(origin string) *cloudclient.ProjectEnvironment {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return nil
	}
	// A relative local path such as github.com/org/repo is not a network origin.
	if !strings.Contains(origin, "://") {
		hostPart, _, ok := strings.Cut(origin, ":")
		if !ok {
			return nil
		}
		host := hostPart
		if idx := strings.LastIndex(host, "@"); idx >= 0 {
			host = host[idx+1:]
		}
		if !strings.EqualFold(host, "github.com") {
			return nil
		}
	}
	canonical, ok := gitutil.CanonicalizeGitRemoteURL(origin)
	if !ok {
		return nil
	}
	canonical = strings.ToLower(canonical)
	host, path, ok := strings.Cut(canonical, "/")
	if !ok {
		return nil
	}
	if host != "github.com" && !(strings.HasPrefix(origin, "ssh://") && host == "ssh.github.com:443") {
		return nil
	}
	owner, repository, ok := strings.Cut(path, "/")
	if !ok {
		return nil
	}
	for _, part := range []string{owner, repository} {
		if !validCloudProjectPart(part) {
			return nil
		}
	}
	return &cloudclient.ProjectEnvironment{
		Name:       repository,
		CWD:        "/workspace/" + owner + "/" + repository,
		OriginURLs: []string{"https://github.com/" + owner + "/" + repository},
	}
}

func validCloudProjectPart(part string) bool {
	if part == "" || part == "." || part == ".." {
		return false
	}
	for _, character := range part {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') ||
			character == '-' || character == '_' || character == '.' {
			continue
		}
		return false
	}
	return true
}
