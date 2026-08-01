package sandbox

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

type FileSystemAccessMode string

const (
	FileSystemAccessRead  FileSystemAccessMode = "read"
	FileSystemAccessWrite FileSystemAccessMode = "write"
	FileSystemAccessDeny  FileSystemAccessMode = "deny"
)

type FileSystemSpecialPath struct {
	Kind    string  `json:"kind"`
	Path    string  `json:"path,omitempty"`
	Subpath *string `json:"subpath,omitempty"`
}

func (p *FileSystemSpecialPath) MarshalJSON() ([]byte, error) {
	switch p.Kind {
	case "project_roots":
		return json.Marshal(struct {
			Kind    string  `json:"kind"`
			Subpath *string `json:"subpath"`
		}{
			Kind:    p.Kind,
			Subpath: stringPtrIfNotEmpty(ptrStringValue(p.Subpath)),
		})
	case "unknown":
		return json.Marshal(struct {
			Kind    string  `json:"kind"`
			Path    string  `json:"path"`
			Subpath *string `json:"subpath"`
		}{
			Kind:    p.Kind,
			Path:    p.Path,
			Subpath: stringPtrIfNotEmpty(ptrStringValue(p.Subpath)),
		})
	default:
		return json.Marshal(struct {
			Kind string `json:"kind"`
		}{Kind: p.Kind})
	}
}

type FileSystemPath struct {
	Type    string                 `json:"type"`
	Path    string                 `json:"path,omitempty"`
	Pattern string                 `json:"pattern,omitempty"`
	Value   *FileSystemSpecialPath `json:"value,omitempty"`
}

func (p *FileSystemPath) MarshalJSON() ([]byte, error) {
	switch p.Type {
	case "glob_pattern":
		return json.Marshal(struct {
			Type    string `json:"type"`
			Pattern string `json:"pattern"`
		}{
			Type:    p.Type,
			Pattern: p.Pattern,
		})
	case "special":
		return json.Marshal(struct {
			Type  string                 `json:"type"`
			Value *FileSystemSpecialPath `json:"value"`
		}{
			Type:  p.Type,
			Value: p.Value,
		})
	default:
		pathType := p.Type
		if pathType == "" {
			pathType = "path"
		}
		return json.Marshal(struct {
			Type string `json:"type"`
			Path string `json:"path"`
		}{
			Type: pathType,
			Path: p.Path,
		})
	}
}

type FileSystemSandboxEntry struct {
	Path   FileSystemPath       `json:"path"`
	Access FileSystemAccessMode `json:"access"`
}

type AdditionalNetworkPermissions struct {
	Enabled *bool `json:"enabled"`
}

type AdditionalFileSystemPermissions struct {
	Read             []string                 `json:"read"`
	Write            []string                 `json:"write"`
	GlobScanMaxDepth *uint32                  `json:"globScanMaxDepth,omitempty"`
	Entries          []FileSystemSandboxEntry `json:"entries,omitempty"`
}

type RequestPermissionProfile struct {
	Network    *AdditionalNetworkPermissions    `json:"network"`
	FileSystem *AdditionalFileSystemPermissions `json:"fileSystem"`
}

func isSymbolicSlashTmpPath(path FileSystemPath) bool {
	return path.Type == "special" && path.Value != nil && strings.EqualFold(path.Value.Kind, "slash_tmp")
}

func supportsSymbolicSlashTmp() bool {
	return filepath.Separator == '/'
}

func ptrStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
