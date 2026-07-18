package utils

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

const FileScheme = "file"

func CrossPlatformBase(value string) string {
	value = strings.TrimRight(value, `/\`)
	if value == "" {
		return ""
	}
	if index := strings.LastIndexAny(value, `/\`); index >= 0 {
		return value[index+1:]
	}
	return value
}

func CrossPlatformSlash(value string) string {
	return path.Clean(strings.ReplaceAll(value, `\`, `/`))
}

func CrossPlatformRelative(root string, value string) (string, bool) {
	root = CrossPlatformSlash(root)
	value = CrossPlatformSlash(value)
	if root == "." || value == "." {
		return "", false
	}
	if strings.EqualFold(root, value) {
		return ".", true
	}
	prefix := strings.TrimSuffix(root, "/") + "/"
	if len(value) <= len(prefix) || !strings.EqualFold(value[:len(prefix)], prefix) {
		return "", false
	}
	return value[len(prefix):], true
}

type PathConvention string

const (
	ConventionPosix   PathConvention = "posix"
	ConventionWindows PathConvention = "windows"
)

type PathURI struct {
	url *url.URL
}

type ParseError struct {
	Reason string
	Path   string
}

func (e *ParseError) Error() string {
	if e == nil {
		return ""
	}
	if e.Path == "" {
		return e.Reason
	}
	return fmt.Sprintf("%s: %s", e.Reason, e.Path)
}

func Parse(raw string) (*PathURI, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != FileScheme {
		return nil, &ParseError{Reason: "unsupported path URI scheme `" + parsed.Scheme + "`"}
	}
	if parsed.User != nil {
		return nil, &ParseError{Reason: "credentials are not allowed in path URIs"}
	}
	if parsed.Port() != "" {
		return nil, &ParseError{Reason: "ports are not allowed in path URIs"}
	}
	if parsed.RawQuery != "" {
		return nil, &ParseError{Reason: "query parameters are not allowed in path URIs"}
	}
	if parsed.Fragment != "" {
		return nil, &ParseError{Reason: "fragments are not allowed in path URIs"}
	}
	if parsed.Host == "localhost" || parsed.Host == "LOCALHOST" {
		parsed.Host = ""
	}
	if strings.Contains(strings.ToLower(parsed.EscapedPath()), "%00") {
		return nil, &ParseError{Reason: "invalid file URI path", Path: raw}
	}
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	return &PathURI{url: parsed}, nil
}

func FromAbsoluteNativePath(nativePath string, convention PathConvention) (*PathURI, error) {
	switch convention {
	case ConventionPosix:
		if !strings.HasPrefix(nativePath, "/") {
			return nil, &ParseError{Reason: "path is not absolute", Path: nativePath}
		}
		return fromSegments("", splitAndClean(nativePath, ConventionPosix, true), strings.HasSuffix(nativePath, "/"))
	case ConventionWindows:
		return fromWindowsPath(nativePath)
	default:
		return nil, &ParseError{Reason: "unknown path convention", Path: string(convention)}
	}
}

func FromHostNativePath(nativePath string) (*PathURI, error) {
	if !filepath.IsAbs(nativePath) {
		return nil, &ParseError{Reason: "path is not absolute", Path: nativePath}
	}
	if runtime.GOOS == "windows" {
		return FromAbsoluteNativePath(nativePath, ConventionWindows)
	}
	return FromAbsoluteNativePath(filepath.ToSlash(nativePath), ConventionPosix)
}

func (u *PathURI) String() string {
	if u == nil || u.url == nil {
		return ""
	}
	return u.url.String()
}

func (u *PathURI) MarshalJSON() ([]byte, error) {
	return json.Marshal(u.String())
}

func (u *PathURI) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	parsed, err := Parse(raw)
	if err != nil {
		return err
	}
	*u = *parsed
	return nil
}

func (u *PathURI) EncodedPath() string {
	if u == nil || u.url == nil {
		return ""
	}
	return u.url.EscapedPath()
}

func (u *PathURI) Host() string {
	if u == nil || u.url == nil {
		return ""
	}
	return u.url.Host
}

func (u *PathURI) Basename() (string, bool) {
	segments := u.segments()
	for i := len(segments) - 1; i >= 0; i-- {
		if segments[i] == "" {
			continue
		}
		decoded, err := url.PathUnescape(segments[i])
		if err != nil {
			return segments[i], true
		}
		return decoded, true
	}
	return "", false
}

func (u *PathURI) InferConvention() (PathConvention, bool) {
	if u == nil || u.url == nil {
		return "", false
	}
	if u.url.Host != "" {
		return ConventionWindows, true
	}
	for _, segment := range u.segments() {
		if segment == "" {
			continue
		}
		if isWindowsDriveSegment(segment) {
			return ConventionWindows, true
		}
		return ConventionPosix, true
	}
	return ConventionPosix, true
}

func (u *PathURI) NativePathString() string {
	convention, ok := u.InferConvention()
	if !ok {
		return u.String()
	}
	rendered, err := LegacyAppPathStringFromURI(u, convention)
	if err != nil {
		return u.String()
	}
	return rendered.Value
}

func (u *PathURI) HostNativePath() (string, error) {
	convention := ConventionPosix
	if runtime.GOOS == "windows" {
		convention = ConventionWindows
	}
	inferred, ok := u.InferConvention()
	if !ok || inferred != convention {
		return "", &ParseError{Reason: "path URI uses a foreign path convention", Path: u.String()}
	}
	rendered, err := LegacyAppPathStringFromURI(u, convention)
	if err != nil {
		return "", &ParseError{Reason: "path URI is not valid on this host", Path: u.String()}
	}
	if !filepath.IsAbs(rendered.Value) {
		return "", &ParseError{Reason: "path is not absolute", Path: rendered.Value}
	}
	return rendered.Value, nil
}

func (u *PathURI) Parent() (*PathURI, bool) {
	if u == nil || u.url == nil {
		return nil, false
	}
	convention, ok := u.InferConvention()
	if !ok {
		return nil, false
	}
	segments := nonEmptySegments(u.segments())
	anchorDepth := 0
	if convention == ConventionWindows {
		anchorDepth = 1
	}
	if len(segments) <= anchorDepth {
		return nil, false
	}
	segments = segments[:len(segments)-1]
	parent, err := fromSegments(u.url.Host, segments, false)
	if err != nil {
		return nil, false
	}
	return parent, true
}

func (u *PathURI) Ancestors() []*PathURI {
	out := []*PathURI{}
	for current := u; current != nil; {
		out = append(out, current.Clone())
		parent, ok := current.Parent()
		if !ok {
			break
		}
		current = parent
	}
	return out
}

func (u *PathURI) StartsWith(base *PathURI) bool {
	if u == nil || base == nil {
		return false
	}
	if u.String() == base.String() {
		return true
	}
	if u.Host() != base.Host() {
		return false
	}
	segments := nonEmptySegments(u.segments())
	baseSegments := nonEmptySegments(base.segments())
	if len(baseSegments) > len(segments) {
		return false
	}
	convention, _ := u.InferConvention()
	for i := range baseSegments {
		decoded, err := url.PathUnescape(segments[i])
		if err != nil {
			return false
		}
		if strings.Contains(decoded, "/") || (convention == ConventionWindows && strings.Contains(decoded, `\`)) {
			return false
		}
		if segments[i] != baseSegments[i] {
			return false
		}
	}
	return true
}

func (u *PathURI) Join(nativePath string) (*PathURI, error) {
	if strings.ContainsRune(nativePath, '\x00') {
		return nil, &ParseError{Reason: "invalid file URI path", Path: nativePath}
	}
	if nativePath == "" {
		return u.Clone(), nil
	}
	convention, ok := u.InferConvention()
	if !ok {
		return nil, &ParseError{Reason: "invalid file URI path", Path: u.String()}
	}
	if absolute, err := FromAbsoluteNativePath(nativePath, convention); err == nil {
		return absolute, nil
	}
	if convention == ConventionWindows && isDriveRelative(nativePath) {
		return nil, &ParseError{Reason: "invalid file URI path", Path: nativePath}
	}
	segments := nonEmptySegments(u.segments())
	anchorDepth := 0
	if convention == ConventionWindows {
		anchorDepth = 1
	}
	if len(segments) > anchorDepth && !strings.HasSuffix(u.url.EscapedPath(), "/") {
		segments = segments[:len(segments)-1]
	}
	relative := nativePath
	if convention == ConventionWindows {
		relative = strings.ReplaceAll(relative, `\`, "/")
		if strings.HasPrefix(relative, "/") && !strings.HasPrefix(relative, "//") {
			segments = segments[:anchorDepth]
			relative = strings.TrimLeft(relative, "/")
		}
	}
	for _, component := range strings.Split(relative, "/") {
		switch component {
		case "", ".":
			continue
		case "..":
			if len(segments) > anchorDepth {
				segments = segments[:len(segments)-1]
			}
		default:
			segments = append(segments, component)
		}
	}
	return fromSegments(u.url.Host, segments, strings.HasSuffix(relative, "/"))
}

func (u *PathURI) Clone() *PathURI {
	if u == nil || u.url == nil {
		return nil
	}
	clone := *u.url
	return &PathURI{url: &clone}
}

func (u *PathURI) segments() []string {
	if u == nil || u.url == nil {
		return nil
	}
	escaped := strings.TrimPrefix(u.url.EscapedPath(), "/")
	if escaped == "" {
		return nil
	}
	return strings.Split(escaped, "/")
}

func fromWindowsPath(nativePath string) (*PathURI, error) {
	normalized := strings.ReplaceAll(nativePath, `\`, "/")
	bytes := []byte(normalized)
	if len(bytes) >= 3 && pathURIASCIIAlpha(bytes[0]) && bytes[1] == ':' && bytes[2] == '/' {
		return fromSegments("", splitAndClean(normalized, ConventionWindows, false), strings.HasSuffix(normalized, "/"))
	}
	if strings.HasPrefix(normalized, "//") {
		rest := strings.TrimPrefix(normalized, "//")
		parts := strings.Split(rest, "/")
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			return nil, &ParseError{Reason: "path is not absolute", Path: nativePath}
		}
		host := parts[0]
		return fromSegments(host, cleanSegments(parts[1:]), strings.HasSuffix(normalized, "/"))
	}
	return nil, &ParseError{Reason: "path is not absolute", Path: nativePath}
}

func fromSegments(host string, segments []string, trailingSlash bool) (*PathURI, error) {
	escaped := make([]string, 0, len(segments))
	for _, segment := range segments {
		if segment == "" {
			continue
		}
		escaped = append(escaped, escapeSegment(segment))
	}
	escapedPath := "/" + strings.Join(escaped, "/")
	if trailingSlash && !strings.HasSuffix(escapedPath, "/") {
		escapedPath += "/"
	}
	if len(escaped) == 0 && trailingSlash {
		escapedPath = "/"
	}
	raw := "file://"
	if host != "" {
		raw += host
	}
	raw += escapedPath
	return Parse(raw)
}

func splitAndClean(nativePath string, convention PathConvention, trimLeading bool) []string {
	pathText := nativePath
	if convention == ConventionWindows {
		pathText = strings.ReplaceAll(pathText, `\`, "/")
	}
	if trimLeading {
		pathText = strings.TrimPrefix(pathText, "/")
	}
	return cleanSegments(strings.Split(pathText, "/"))
}

func cleanSegments(segments []string) []string {
	out := make([]string, 0, len(segments))
	for _, segment := range segments {
		switch segment {
		case "", ".":
			continue
		case "..":
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
		default:
			out = append(out, segment)
		}
	}
	return out
}

func nonEmptySegments(segments []string) []string {
	out := make([]string, 0, len(segments))
	for _, segment := range segments {
		if segment != "" {
			out = append(out, segment)
		}
	}
	return out
}

func escapeSegment(segment string) string {
	return (&url.URL{Path: segment}).EscapedPath()
}

func isWindowsDriveSegment(segment string) bool {
	decoded, err := url.PathUnescape(segment)
	if err != nil {
		decoded = segment
	}
	bytes := []byte(decoded)
	return len(bytes) == 2 && pathURIASCIIAlpha(bytes[0]) && bytes[1] == ':'
}

func isDriveRelative(nativePath string) bool {
	bytes := []byte(nativePath)
	return len(bytes) >= 2 && pathURIASCIIAlpha(bytes[0]) && bytes[1] == ':' && (len(bytes) == 2 || (bytes[2] != '\\' && bytes[2] != '/'))
}

func pathURIASCIIAlpha(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
}

func PathSegments(convention PathConvention, pathText string) []string {
	if convention == ConventionWindows {
		return strings.FieldsFunc(pathText, func(ch rune) bool { return ch == '/' || ch == '\\' })
	}
	return strings.Split(pathText, "/")
}

func LexicalClean(pathText string, convention PathConvention) string {
	if convention == ConventionWindows {
		pathText = strings.ReplaceAll(pathText, `\`, "/")
		cleaned := path.Clean(pathText)
		return strings.ReplaceAll(cleaned, "/", `\`)
	}
	return path.Clean(pathText)
}
