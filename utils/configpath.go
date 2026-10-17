package utils

import (
	"net"
	"runtime"
	"strings"
	"unicode/utf8"
)

// NativePathConvention returns the path grammar of the current host.
func NativePathConvention() PathConvention {
	if runtime.GOOS == "windows" {
		return ConventionWindows
	}
	return ConventionPosix
}

// HomeRelativeSuffix reports whether path starts with the home marker for the
// supplied convention and returns the suffix after "~". Named-user paths such
// as "~someone/private" are not home-relative.
func (c PathConvention) HomeRelativeSuffix(path string) (string, bool) {
	suffix, ok := strings.CutPrefix(path, "~")
	if !ok {
		return "", false
	}
	if suffix == "" || strings.HasPrefix(suffix, "/") ||
		c == ConventionWindows && strings.HasPrefix(suffix, `\`) {
		return suffix, true
	}
	return "", false
}

// ResolveConfigPath resolves configuration text using only the supplied path
// convention, base, and home facts. Relative inputs require a base, while
// home-relative inputs require a home directory.
func ResolveConfigPath(input string, convention PathConvention, base *PathURI, home *PathURI) (string, error) {
	resolved, err := ResolveConfigPathURI(input, convention, base, home)
	if err != nil {
		return "", err
	}
	return resolved.ToConfigPathString(convention)
}

// ResolveConfigPathURI resolves and validates configuration text as a path URI
// using only the supplied facts. Trailing separators are normalized while root
// separators remain represented by the URI itself.
func ResolveConfigPathURI(input string, convention PathConvention, base *PathURI, home *PathURI) (*PathURI, error) {
	if err := ValidateConfigPathText(input, convention); err != nil {
		return nil, err
	}
	legacy := NewLegacyAppPathString(input)
	var (
		resolved *PathURI
		err      error
	)
	if suffix, ok := convention.HomeRelativeSuffix(input); ok {
		if home == nil {
			return nil, &LegacyError{
				Reason:     "cannot resolve home-relative path without an executor home",
				Path:       input,
				Convention: convention,
			}
		}
		if err := home.ValidateConfigPath(convention); err != nil {
			return nil, err
		}
		if containsGlobMetacharacter(input) {
			if err := home.ValidateGlobDirectory(convention); err != nil {
				return nil, err
			}
		}
		suffix = strings.TrimLeft(suffix, `/\`)
		resolved, err = home.Join(suffix)
	} else {
		resolved, err = legacy.ToPathURI(convention)
		if err != nil {
			if base == nil {
				return nil, err
			}
			if err := base.ValidateConfigPath(convention); err != nil {
				return nil, err
			}
			if containsGlobMetacharacter(input) {
				if err := base.ValidateGlobDirectory(convention); err != nil {
					return nil, err
				}
			}
			resolved, err = base.Join(input)
		}
	}
	if err != nil {
		return nil, err
	}
	if err := resolved.ValidateConfigPath(convention); err != nil {
		return nil, err
	}
	return resolved.Join(".")
}

// ToConfigPathString renders a resolved configuration URI using the owning
// executor's convention. UNC share roots keep their historical trailing
// separator so they cannot be mistaken for drive-relative paths.
func (u *PathURI) ToConfigPathString(convention PathConvention) (string, error) {
	rendered, err := LegacyAppPathStringFromURI(u, convention)
	if err != nil {
		return "", err
	}
	value := rendered.Value
	if u != nil && u.Host() != "" {
		if _, hasParent := u.Parent(); !hasParent && !strings.HasSuffix(value, `\`) {
			value += `\`
		}
	}
	return value, nil
}

// ValidateGlobDirectory checks that a literal directory can be inserted into a
// glob without changing its meaning. POSIX backslashes are rejected because
// they could escape the following pattern character.
func (u *PathURI) ValidateGlobDirectory(convention PathConvention) error {
	if err := u.ValidateConfigPath(convention); err != nil {
		return err
	}
	value, err := u.ToConfigPathString(convention)
	if err != nil {
		return err
	}
	if containsGlobMetacharacter(value) || convention == ConventionPosix && strings.Contains(value, `\`) {
		return &LegacyError{
			Reason:     "unsupported configuration path",
			Path:       value,
			Convention: convention,
		}
	}
	return nil
}

// ValidateConfigPath checks that a resolved URI has an unambiguous, lossless
// native spelling in the owning executor's convention.
func (u *PathURI) ValidateConfigPath(convention PathConvention) error {
	inferred, ok := u.InferConvention()
	if !ok || inferred != convention {
		path := ""
		if u != nil {
			path = u.String()
		}
		return &LegacyError{
			Reason:     "path uses an incompatible path convention",
			Path:       path,
			Convention: convention,
		}
	}
	if _, ok := u.LexicalDepth(); !ok {
		return &ParseError{Reason: "invalid file URI path", Path: u.String()}
	}
	if host := u.Host(); host != "" && !safeUNCConfigHost(host) {
		return &LegacyError{
			Reason:     "unsupported configuration path",
			Path:       u.String(),
			Convention: convention,
		}
	}
	decoded := u.DecodedPathBytes()
	if decoded == nil || strings.ContainsRune(string(decoded), 0) || !utf8.Valid(decoded) {
		return &ParseError{Reason: "invalid file URI path", Path: u.String()}
	}
	value, err := u.ToConfigPathString(convention)
	if err != nil {
		return err
	}
	return ValidateConfigPathText(value, convention)
}

// ValidateConfigPathText rejects configuration spellings that could change
// targets during later native conversion. It does not resolve paths or glob
// syntax.
func ValidateConfigPathText(input string, convention PathConvention) error {
	nativeInput := input
	if normalized, ok := NormalizeWindowsDevicePath(input); ok {
		nativeInput = normalized
	}
	hasWindowsComponentColon := false
	if convention == ConventionWindows {
		for index, segment := range PathSegments(convention, nativeInput) {
			if index == 0 && len(segment) >= 2 &&
				isASCIIAlpha(segment[0]) && segment[1] == ':' {
				segment = segment[2:]
			}
			if strings.Contains(segment, ":") {
				hasWindowsComponentColon = true
				break
			}
		}
	}
	mixedHomeSeparators := false
	if convention == ConventionWindows {
		if suffix, ok := convention.HomeRelativeSuffix(input); ok {
			mixedHomeSeparators =
				strings.HasPrefix(suffix, "/") && strings.HasPrefix(strings.TrimLeft(suffix, "/"), `\`) ||
					strings.HasPrefix(suffix, `\`) && strings.HasPrefix(strings.TrimLeft(suffix, `\`), "/")
		}
	}
	bareWindowsDrive := convention == ConventionWindows && len(nativeInput) == 2 &&
		isASCIIAlpha(nativeInput[0]) && nativeInput[1] == ':'
	deviceNamespace := convention == ConventionWindows &&
		(strings.HasPrefix(input, `\\?\`) || strings.HasPrefix(input, `\\.\`))
	if strings.ContainsRune(input, 0) || hasWindowsComponentColon ||
		mixedHomeSeparators || bareWindowsDrive || deviceNamespace {
		return &LegacyError{
			Reason:     "unsupported configuration path",
			Path:       input,
			Convention: convention,
		}
	}
	return nil
}

func containsGlobMetacharacter(path string) bool {
	return strings.ContainsAny(path, `*?[]{}`)
}

func isASCIIAlpha(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func safeUNCConfigHost(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	if net.ParseIP(strings.Trim(host, "[]")) != nil {
		return true
	}
	for _, character := range host {
		if character > 0x7f {
			return false
		}
	}
	lower := strings.ToLower(host)
	if strings.HasPrefix(lower, "0x") {
		hexOnly := true
		for _, character := range lower[2:] {
			if !strings.ContainsRune("0123456789abcdef", character) {
				hexOnly = false
				break
			}
		}
		if hexOnly {
			return false
		}
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || label == "." || label == ".." {
			return false
		}
		for _, character := range label {
			if character >= 'a' && character <= 'z' ||
				character >= 'A' && character <= 'Z' ||
				character >= '0' && character <= '9' ||
				character == '-' || character == '_' {
				continue
			}
			return false
		}
	}
	return true
}
