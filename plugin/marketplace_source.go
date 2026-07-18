package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type MarketplaceSourceKind string

const (
	MarketplaceSourceGit   MarketplaceSourceKind = "git"
	MarketplaceSourceLocal MarketplaceSourceKind = "local"
)

type ParsedMarketplaceSource struct {
	Kind    MarketplaceSourceKind
	URL     string
	Path    string
	RefName *string
}

func ParseMarketplaceSource(source string, explicitRef *string) (*ParsedMarketplaceSource, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil, fmt.Errorf("%w: marketplace source must not be empty", ErrInvalidPluginRequest)
	}
	baseSource, parsedRef := splitMarketplaceSourceRef(source)
	refName := cloneStringPtr(explicitRef)
	if refName == nil {
		refName = parsedRef
	}
	if looksLikeLocalMarketplacePath(baseSource) {
		if refName != nil {
			return nil, fmt.Errorf("%w: --ref is only supported for git marketplace sources", ErrInvalidPluginRequest)
		}
		path, err := resolveLocalMarketplaceSourcePath(baseSource)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("%w: failed to resolve local marketplace source path: %v", ErrInvalidPluginRequest, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("%w: local marketplace source must be a directory, not a file", ErrInvalidPluginRequest)
		}
		return &ParsedMarketplaceSource{Kind: MarketplaceSourceLocal, Path: path}, nil
	}
	if isSSHMarketplaceGitURL(baseSource) || isMarketplaceGitURL(baseSource) {
		return &ParsedMarketplaceSource{Kind: MarketplaceSourceGit, URL: normalizeMarketplaceGitURL(baseSource), RefName: refName}, nil
	}
	if looksLikeGithubMarketplaceShorthand(baseSource) {
		return &ParsedMarketplaceSource{Kind: MarketplaceSourceGit, URL: "https://github.com/" + baseSource + ".git", RefName: refName}, nil
	}
	return nil, fmt.Errorf("%w: invalid marketplace source format; expected owner/repo, a git URL, or a local marketplace path", ErrInvalidPluginRequest)
}

func (s *ParsedMarketplaceSource) Display() string {
	if s == nil {
		return ""
	}
	switch s.Kind {
	case MarketplaceSourceLocal:
		return s.Path
	case MarketplaceSourceGit:
		if s.RefName != nil && strings.TrimSpace(*s.RefName) != "" {
			return s.URL + "#" + strings.TrimSpace(*s.RefName)
		}
		return s.URL
	default:
		return ""
	}
}

func (s *ParsedMarketplaceSource) NameCandidate() string {
	if s == nil {
		return ""
	}
	if s.Kind == MarketplaceSourceLocal {
		return s.Path
	}
	return s.URL
}

func splitMarketplaceSourceRef(source string) (string, *string) {
	if base, refName, ok := strings.Cut(source, "#"); ok {
		return base, nonEmptyMarketplaceRef(refName)
	}
	if !strings.Contains(source, "://") && !isSSHMarketplaceGitURL(source) {
		if base, refName, ok := strings.Cut(source, "@"); ok {
			return base, nonEmptyMarketplaceRef(refName)
		}
	}
	return source, nil
}

func nonEmptyMarketplaceRef(refName string) *string {
	refName = strings.TrimSpace(refName)
	if refName == "" {
		return nil
	}
	return &refName
}

func normalizeMarketplaceGitURL(value string) string {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	if strings.HasPrefix(value, "https://github.com/") && !strings.HasSuffix(value, ".git") {
		return value + ".git"
	}
	return value
}

func looksLikeLocalMarketplacePath(source string) bool {
	return filepath.IsAbs(source) ||
		looksLikeWindowsMarketplaceAbsolutePath(source) ||
		strings.HasPrefix(source, "./") ||
		strings.HasPrefix(source, ".\\") ||
		strings.HasPrefix(source, "../") ||
		strings.HasPrefix(source, "..\\") ||
		strings.HasPrefix(source, "~/") ||
		source == "." ||
		source == ".."
}

func looksLikeWindowsMarketplaceAbsolutePath(source string) bool {
	bytes := []byte(source)
	if len(bytes) >= 3 && ((bytes[0] >= 'a' && bytes[0] <= 'z') || (bytes[0] >= 'A' && bytes[0] <= 'Z')) && bytes[1] == ':' && (bytes[2] == '\\' || bytes[2] == '/') {
		return true
	}
	return strings.HasPrefix(source, `\\`)
}

func resolveLocalMarketplaceSourcePath(source string) (string, error) {
	path := expandMarketplaceTildePath(source)
	if !filepath.IsAbs(path) && !looksLikeWindowsMarketplaceAbsolutePath(path) {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return "", fmt.Errorf("%w: failed to resolve local marketplace source path: %v", ErrInvalidPluginRequest, err)
		}
		path = absolute
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		if _, statErr := os.Stat(path); statErr == nil {
			return filepath.Clean(path), nil
		}
		return "", fmt.Errorf("%w: failed to resolve local marketplace source path: %v", ErrInvalidPluginRequest, err)
	}
	return resolved, nil
}

func expandMarketplaceTildePath(source string) string {
	if !strings.HasPrefix(source, "~/") {
		return source
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return source
	}
	return filepath.Join(home, strings.TrimPrefix(source, "~/"))
}

func isSSHMarketplaceGitURL(source string) bool {
	return strings.HasPrefix(source, "ssh://") || (strings.HasPrefix(source, "git@") && strings.Contains(source, ":"))
}

func isMarketplaceGitURL(source string) bool {
	return strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://")
}

func looksLikeGithubMarketplaceShorthand(source string) bool {
	parts := strings.Split(source, "/")
	return len(parts) == 2 && isGithubMarketplaceSegment(parts[0]) && isGithubMarketplaceSegment(parts[1])
}

func isGithubMarketplaceSegment(segment string) bool {
	if segment == "" {
		return false
	}
	for _, r := range segment {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func safeMarketplaceDirName(marketplaceName string) (string, error) {
	safe := sanitize(marketplaceName)
	safe = strings.Trim(safe, ".")
	if safe == "" || safe == ".." {
		return "", fmt.Errorf("%w: marketplace name %q cannot be used as an install directory", ErrInvalidPluginRequest, marketplaceName)
	}
	return safe, nil
}
