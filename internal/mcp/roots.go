package mcp

import (
	"net/url"
	"path/filepath"
	"strings"
)

type MCPRoot struct {
	URI  string `json:"uri"`
	Name string `json:"name,omitempty"`
}

type MCPRootsProvider interface {
	MCPRoots(threadID string) []MCPRoot
}

type MCPRootsProviderFunc func(threadID string) []MCPRoot

func (f MCPRootsProviderFunc) MCPRoots(threadID string) []MCPRoot {
	if f == nil {
		return nil
	}
	return f(threadID)
}

func NewMCPFileRoot(path string) *MCPRoot {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	name := filepath.Base(filepath.Clean(path))
	if name == "." || name == string(filepath.Separator) {
		name = filepath.Clean(path)
	}
	slashPath := filepath.ToSlash(filepath.Clean(path))
	if !strings.HasPrefix(slashPath, "/") {
		slashPath = "/" + slashPath
	}
	return &MCPRoot{
		URI:  (&url.URL{Scheme: "file", Path: slashPath}).String(),
		Name: name,
	}
}

func cloneMCPRoots(roots []MCPRoot) []MCPRoot {
	if len(roots) == 0 {
		return nil
	}
	out := make([]MCPRoot, 0, len(roots))
	seen := map[string]bool{}
	for _, root := range roots {
		uri := strings.TrimSpace(root.URI)
		if uri == "" || seen[uri] {
			continue
		}
		seen[uri] = true
		out = append(out, MCPRoot{URI: uri, Name: strings.TrimSpace(root.Name)})
	}
	return out
}
