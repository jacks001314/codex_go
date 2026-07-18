package appserverdaemon

import (
	"fmt"
	"strings"
)

type AppServerPath struct {
	value string
}

func FromAppServerPath(path string) *AppServerPath {
	return &AppServerPath{value: path}
}

func FromAbsolutePath(path string) (*AppServerPath, bool) {
	if !IsAbsoluteAppServerPath(path) {
		return nil, false
	}
	return FromAppServerPath(path), true
}

func (p *AppServerPath) String() string {
	if p == nil {
		return ""
	}
	return p.value
}

func (p *AppServerPath) AsString() string {
	return p.String()
}

func (p *AppServerPath) Components() []string {
	if p == nil {
		return nil
	}
	separators := func(r rune) bool {
		if IsWindowsAbsolutePath(p.value) {
			return r == '/' || r == '\\'
		}
		return r == '/'
	}
	parts := strings.FieldsFunc(p.value, separators)
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func (p *AppServerPath) Join(segment string) *AppServerPath {
	if p == nil {
		return FromAppServerPath(segment)
	}
	if IsWindowsAbsolutePath(p.value) {
		base := strings.TrimRight(p.value, `/\`)
		return FromAppServerPath(fmt.Sprintf(`%s\%s`, base, segment))
	}
	base := strings.TrimRight(p.value, `/`)
	return FromAppServerPath(fmt.Sprintf("%s/%s", base, segment))
}

func IsAbsoluteAppServerPath(path string) bool {
	return strings.HasPrefix(path, "/") || IsWindowsAbsolutePath(path)
}

func IsWindowsAbsolutePath(path string) bool {
	if len(path) >= 3 {
		b := []byte(path)
		if asciiAlpha(b[0]) && b[1] == ':' && (b[2] == '\\' || b[2] == '/') {
			return true
		}
	}
	return strings.HasPrefix(path, `\\`) || strings.HasPrefix(path, "//")
}

func asciiAlpha(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}
