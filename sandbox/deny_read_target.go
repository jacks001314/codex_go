package sandbox

import (
	"errors"
	pathpkg "path"
	"strings"

	"codex_go/utils"

	"github.com/bmatcuk/doublestar/v4"
)

// Rust parity: codex-rs/protocol/src/permissions/target.rs and
// deny_read_validator.rs (#43939). Filesystem policies for remote execution
// must interpret paths, home directories, and temporary directories using the
// execution host's context instead of the controller's environment.

const (
	denyReadInvalidPathMessage       = "managed filesystem denial cannot be materialized safely"
	DenyReadShadowedMessage          = "managed filesystem denial is shadowed by a selected profile grant"
	denyReadTmpdirUnavailableMessage = "executor temporary-directory metadata is unavailable for managed filesystem denials"
	denyReadTmpdirInvalidMessage     = "executor temporary-directory metadata cannot be materialized safely"
)

// DenyReadContext carries the executor-owned path facts that a
// context-based deny-read matcher may consult. It mirrors Rust
// FileSystemSandboxPolicyContext; every path is absolute text in the
// executor's spelling. TemporaryDirectories is nil when the executor did not
// report temporary-directory metadata, matching Rust's Option and letting a
// :tmpdir denial fail closed instead of silently disappearing.
type DenyReadContext struct {
	CWD                  string
	WorkspaceRoots       []string
	UserHomeDir          string
	TemporaryDirectories *[]string
}

// DenyReadViolationKind mirrors Rust DenyReadViolation.
type DenyReadViolationKind string

const (
	DenyReadViolationMissingRequired DenyReadViolationKind = "missing_required_deny"
	DenyReadViolationReadablePath    DenyReadViolationKind = "readable_path"
)

// DenyReadViolation retains the caller's sourced constraint diagnostics.
type DenyReadViolation struct {
	Kind DenyReadViolationKind
	// Path is the first conflicting readable path for
	// DenyReadViolationReadablePath.
	Path string
}

// preparedDenyEntry is one executor-resolved denial. Literal paths keep their
// cleaned absolute spelling; globs keep their materialized pattern.
type preparedDenyEntry struct {
	path     string
	isGlob   bool
	rootDeny bool
}

// PreparedReadDenials mirrors Rust PreparedReadDenyMatcher built from an
// executor context: literal denials cover their subtree and globs use the
// executor's convention. Candidates whose convention differs from the
// executor's fail closed.
type PreparedReadDenials struct {
	convention utils.PathConvention
	entries    []preparedDenyEntry
}

// HasContextReadDenials mirrors Rust has_context_read_denials: a restricted
// policy carries a denial that applies to this executor. Symbolic :slash_tmp
// denials only apply on POSIX executors; an unknown convention keeps the
// restriction.
func HasContextReadDenials(entries []FileSystemSandboxEntry, context DenyReadContext) bool {
	convention, _ := denyReadContextConvention(context)
	for _, entry := range entries {
		if entry.Access != FileSystemAccessDeny {
			continue
		}
		if isSymbolicSlashTmpPath(entry.Path) && convention != "" && convention != utils.ConventionPosix {
			continue
		}
		return true
	}
	return false
}

// NewPreparedReadDenials mirrors Rust
// ReadDenyMatcher::try_new_with_context: it validates the executor context
// and returns (nil, nil) when the policy has no denial that applies here.
func NewPreparedReadDenials(entries []FileSystemSandboxEntry, context DenyReadContext) (*PreparedReadDenials, error) {
	return prepareReadDenials(entries, context, true)
}

func prepareReadDenials(entries []FileSystemSandboxEntry, context DenyReadContext, strictContext bool) (*PreparedReadDenials, error) {
	if !HasContextReadDenials(entries, context) {
		return nil, nil
	}
	convention, ok := denyReadContextConvention(context)
	if !ok {
		return nil, errors.New(denyReadInvalidPathMessage)
	}
	if strictContext {
		if err := validateDenyReadContext(context, convention); err != nil {
			return nil, err
		}
	}
	prepared := &PreparedReadDenials{convention: convention}
	for _, entry := range entries {
		if entry.Access != FileSystemAccessDeny {
			continue
		}
		switch entry.Path.Type {
		case "", "path":
			path := strings.TrimSpace(entry.Path.Path)
			if !denyReadPathMatchesConvention(path, convention) {
				return nil, errors.New(denyReadInvalidPathMessage)
			}
			prepared.entries = append(prepared.entries, preparedDenyEntry{
				path: cleanDenyReadPath(path, convention),
			})
		case "glob_pattern":
			pattern, err := materializeDenyReadGlob(entry.Path.Pattern, context, convention)
			if err != nil {
				return nil, err
			}
			prepared.entries = append(prepared.entries, preparedDenyEntry{
				path:   pattern,
				isGlob: true,
			})
		case "special":
			denials, err := prepareSpecialDenyEntries(entry, context, convention)
			if err != nil {
				return nil, err
			}
			prepared.entries = append(prepared.entries, denials...)
		default:
			return nil, errors.New(denyReadInvalidPathMessage)
		}
	}
	return prepared, nil
}

func prepareSpecialDenyEntries(entry FileSystemSandboxEntry, context DenyReadContext, convention utils.PathConvention) ([]preparedDenyEntry, error) {
	if entry.Path.Value == nil {
		return nil, errors.New(denyReadInvalidPathMessage)
	}
	switch strings.ToLower(strings.TrimSpace(entry.Path.Value.Kind)) {
	case "slash_tmp":
		if convention != utils.ConventionPosix {
			return nil, errors.New(denyReadInvalidPathMessage)
		}
		return []preparedDenyEntry{{path: "/tmp"}}, nil
	case "root":
		return []preparedDenyEntry{{path: rootPathForCWD(context.CWD), rootDeny: true}}, nil
	case "current_working_directory":
		if !denyReadPathMatchesConvention(context.CWD, convention) {
			return nil, errors.New(denyReadInvalidPathMessage)
		}
		return []preparedDenyEntry{{path: cleanDenyReadPath(context.CWD, convention)}}, nil
	case "project_roots":
		subpath := ""
		if entry.Path.Value.Subpath != nil {
			subpath = *entry.Path.Value.Subpath
		}
		denials := make([]preparedDenyEntry, 0, len(context.WorkspaceRoots))
		for _, workspaceRoot := range context.WorkspaceRoots {
			path, err := joinDenyReadSubpath(workspaceRoot, subpath, convention)
			if err != nil {
				return nil, err
			}
			denials = append(denials, preparedDenyEntry{path: path})
		}
		if len(denials) == 0 {
			return nil, errors.New(denyReadInvalidPathMessage)
		}
		return denials, nil
	case "tmpdir":
		if context.TemporaryDirectories == nil {
			return nil, errors.New(denyReadTmpdirUnavailableMessage)
		}
		if len(*context.TemporaryDirectories) == 0 {
			return nil, errors.New(denyReadTmpdirInvalidMessage)
		}
		denials := make([]preparedDenyEntry, 0, len(*context.TemporaryDirectories))
		for _, directory := range *context.TemporaryDirectories {
			if !denyReadPathMatchesConvention(directory, convention) {
				return nil, errors.New(denyReadTmpdirInvalidMessage)
			}
			denials = append(denials, preparedDenyEntry{path: cleanDenyReadPath(directory, convention)})
		}
		return denials, nil
	default:
		return nil, errors.New(denyReadInvalidPathMessage)
	}
}

// IsReadDenied reports whether the executor-context denials reject path. A
// candidate using a foreign or unknown convention is denied, matching Rust
// is_read_denied_uri.
func (m *PreparedReadDenials) IsReadDenied(path string) bool {
	if m == nil {
		return false
	}
	candidateConvention, ok := denyReadAbsoluteConvention(path)
	if !ok || candidateConvention != m.convention {
		return true
	}
	cleaned := cleanDenyReadPath(path, m.convention)
	for _, entry := range m.entries {
		if entry.rootDeny {
			return true
		}
		if entry.isGlob {
			pattern := slashPathForConvention(entry.path, m.convention)
			target := slashPathForConvention(cleaned, m.convention)
			if m.convention == utils.ConventionWindows {
				pattern = strings.ToLower(pattern)
				target = strings.ToLower(target)
			}
			if matched, err := doublestar.Match(pattern, target); err == nil && matched {
				return true
			}
			continue
		}
		if pathWithinForConvention(cleaned, entry.path, m.convention) {
			return true
		}
	}
	return false
}

// DenyReadValidator mirrors Rust DenyReadValidator: the mandatory denial
// rules are prepared once with executor facts and reused whenever a caller
// replaces the selected profile.
type DenyReadValidator struct {
	required []FileSystemSandboxEntry
	matcher  *PreparedReadDenials
}

// NewDenyReadValidator mirrors Rust DenyReadValidator::new. It does not
// validate the whole context; it only prepares the strict matcher, so a
// malformed or unresolvable denial still fails construction.
func NewDenyReadValidator(required []FileSystemSandboxEntry, context DenyReadContext) (*DenyReadValidator, error) {
	matcher, err := prepareReadDenials(required, context, false)
	if err != nil {
		return nil, err
	}
	return &DenyReadValidator{
		required: append([]FileSystemSandboxEntry(nil), required...),
		matcher:  matcher,
	}, nil
}

// Validate mirrors Rust DenyReadValidator::validate: it preserves the
// entry-presence and raw readable-path checks. Symbolic entries are not
// expanded, and a readable concrete grant is reported before a missing
// required entry.
func (v *DenyReadValidator) Validate(selected []FileSystemSandboxEntry, context DenyReadContext) *DenyReadViolation {
	if v == nil {
		return nil
	}
	missingRequired := false
	for _, required := range v.required {
		if !containsFileSystemEntry(selected, required) {
			missingRequired = true
			break
		}
	}
	convention, haveConvention := denyReadContextConvention(context)
	for _, entry := range selected {
		if !entryAccessCanRead(entry.Access) {
			continue
		}
		if entry.Path.Type != "" && entry.Path.Type != "path" {
			continue
		}
		path := strings.TrimSpace(entry.Path.Path)
		candidateConvention, ok := denyReadAbsoluteConvention(path)
		if !ok || haveConvention && candidateConvention != convention {
			continue
		}
		if v.matcher.IsReadDenied(path) {
			return &DenyReadViolation{Kind: DenyReadViolationReadablePath, Path: path}
		}
	}
	if missingRequired {
		return &DenyReadViolation{Kind: DenyReadViolationMissingRequired}
	}
	return nil
}

// ValidateManagedDenyRead mirrors Rust
// FileSystemSandboxPolicy::validate_managed_deny_read. Every violation
// collapses to the shadowed message Rust reports to configuration callers.
func ValidateManagedDenyRead(required, selected []FileSystemSandboxEntry, context DenyReadContext) error {
	validator, err := NewDenyReadValidator(required, context)
	if err != nil {
		return err
	}
	if validator.Validate(selected, context) != nil {
		return errors.New(DenyReadShadowedMessage)
	}
	return nil
}

// CanReadPathWithContext mirrors Rust
// FileSystemSandboxPolicy::can_read_path: it checks exact-root read access
// using executor-owned paths. Deny-glob enforcement stays with
// PreparedReadDenials.
func CanReadPathWithContext(entries []FileSystemSandboxEntry, path string, context DenyReadContext) bool {
	convention, ok := denyReadContextConvention(context)
	if !ok {
		return false
	}
	if _, ok := denyReadAbsoluteConvention(path); !ok {
		return false
	}
	access := ResolveFileSystemEntryAccessForConvention(entries, path, context.CWD, convention)
	return entryAccessCanRead(access)
}

// ReadDenyGlobsWithContext mirrors Rust
// FileSystemSandboxPolicy::get_unreadable_globs_with_context: deny globs are
// resolved with the executor's convention and home.
func ReadDenyGlobsWithContext(entries []FileSystemSandboxEntry, context DenyReadContext) ([]string, error) {
	convention, ok := denyReadContextConvention(context)
	if !ok {
		return nil, errors.New(denyReadInvalidPathMessage)
	}
	var globs []string
	for _, entry := range entries {
		if entry.Access != FileSystemAccessDeny || entry.Path.Type != "glob_pattern" {
			continue
		}
		pattern, err := materializeDenyReadGlob(entry.Path.Pattern, context, convention)
		if err != nil {
			return nil, err
		}
		globs = append(globs, pattern)
	}
	return globs, nil
}

// validateDenyReadContext mirrors Rust alidate_context_paths: every fact the
// matcher will materialize must live in the executor's convention.
func validateDenyReadContext(context DenyReadContext, convention utils.PathConvention) error {
	if !denyReadPathMatchesConvention(context.CWD, convention) {
		return errors.New(denyReadInvalidPathMessage)
	}
	for _, workspaceRoot := range context.WorkspaceRoots {
		if !denyReadPathMatchesConvention(workspaceRoot, convention) {
			return errors.New(denyReadInvalidPathMessage)
		}
	}
	return nil
}

func materializeDenyReadGlob(pattern string, context DenyReadContext, convention utils.PathConvention) (string, error) {
	trimmed := strings.TrimSpace(pattern)
	if trimmed == "" {
		return "", errors.New(denyReadInvalidPathMessage)
	}
	if homeSuffix, ok := homeRelativeDenyReadSuffix(trimmed, convention); ok {
		home := strings.TrimSpace(context.UserHomeDir)
		if !denyReadPathMatchesConvention(home, convention) {
			return "", errors.New(denyReadInvalidPathMessage)
		}
		materialized, err := joinDenyReadSubpath(home, homeSuffix, convention)
		if err != nil {
			return "", err
		}
		trimmed = materialized
	} else if !denyReadPathMatchesConvention(baseDenyReadGlobPrefix(trimmed), convention) {
		if !denyReadPathMatchesConvention(context.CWD, convention) {
			return "", errors.New(denyReadInvalidPathMessage)
		}
		materialized, err := joinDenyReadSubpath(context.CWD, trimmed, convention)
		if err != nil {
			return "", err
		}
		trimmed = materialized
	}
	slash := slashPathForConvention(trimmed, convention)
	if !doublestar.ValidatePattern(slash) || denyReadGlobRangeIsInvalid(slash) {
		return "", errors.New(denyReadInvalidPathMessage)
	}
	return cleanDenyReadPath(trimmed, convention), nil
}

func homeRelativeDenyReadSuffix(pattern string, convention utils.PathConvention) (string, bool) {
	if strings.HasPrefix(pattern, "~/") {
		return pattern[2:], true
	}
	if convention == utils.ConventionWindows && strings.HasPrefix(pattern, `~\`) {
		return pattern[2:], true
	}
	return "", false
}

// baseDenyReadGlobPrefix returns the literal directory before the first glob
// metacharacter so convention inference ignores the pattern body.
func baseDenyReadGlobPrefix(pattern string) string {
	index := strings.IndexAny(pattern, denyReadGlobMetacharacters)
	if index < 0 {
		return pattern
	}
	return pattern[:index]
}

func joinDenyReadSubpath(base, subpath string, convention utils.PathConvention) (string, error) {
	base = strings.TrimSpace(base)
	if !denyReadPathMatchesConvention(base, convention) {
		return "", errors.New(denyReadInvalidPathMessage)
	}
	if subpath == "" {
		return cleanDenyReadPath(base, convention), nil
	}
	if convention == utils.ConventionWindows {
		subpath = strings.ReplaceAll(subpath, `\`, "/")
	}
	joined := strings.TrimRight(strings.ReplaceAll(base, `\`, "/"), "/") + "/" + strings.TrimLeft(subpath, "/")
	cleaned := cleanDenyReadPath(joined, convention)
	if !denyReadPathMatchesConvention(cleaned, convention) {
		return "", errors.New(denyReadInvalidPathMessage)
	}
	return cleaned, nil
}

func denyReadContextConvention(context DenyReadContext) (utils.PathConvention, bool) {
	return denyReadAbsoluteConvention(context.CWD)
}

// cleanDenyReadPath cleans absolute executor text without losing a UNC root.
// `utils.LexicalClean` routes through `path.Clean`, which collapses the leading
// `//` that identifies a UNC share.
func cleanDenyReadPath(path string, convention utils.PathConvention) string {
	if convention != utils.ConventionWindows {
		return utils.LexicalClean(path, convention)
	}
	slashed := strings.ReplaceAll(strings.TrimSpace(path), `\`, "/")
	if strings.HasPrefix(slashed, "//") {
		slashed = "/" + pathpkg.Clean("/"+strings.TrimLeft(slashed, "/"))
	} else {
		slashed = pathpkg.Clean(slashed)
	}
	return strings.ReplaceAll(slashed, "/", `\`)
}

// denyReadGlobRangeIsInvalid reports a reversed character-class range such as
// `[z-a]`, which Rust's glob parser rejects but doublestar treats as a class
// that never matches.
func denyReadGlobRangeIsInvalid(pattern string) bool {
	for index := 0; index < len(pattern); index++ {
		if pattern[index] != '[' {
			if pattern[index] == '\\' {
				index++
			}
			continue
		}
		index++
		if index < len(pattern) && (pattern[index] == '!' || pattern[index] == '^') {
			index++
		}
		for index < len(pattern) && pattern[index] != ']' {
			low := pattern[index]
			if low == '\\' {
				index++
				if index >= len(pattern) {
					return true
				}
				low = pattern[index]
			}
			if index+2 < len(pattern) && pattern[index+1] == '-' && pattern[index+2] != ']' {
				high := pattern[index+2]
				if high == '\\' {
					index++
					if index+1 >= len(pattern) {
						return true
					}
					high = pattern[index+1]
				}
				if high < low {
					return true
				}
				index += 2
			}
			index++
		}
	}
	return false
}

func denyReadAbsoluteConvention(path string) (utils.PathConvention, bool) {
	legacy := utils.NewLegacyAppPathString(strings.TrimSpace(path))
	return legacy.InferAbsolutePathConvention()
}

func denyReadPathMatchesConvention(path string, convention utils.PathConvention) bool {
	inferred, ok := denyReadAbsoluteConvention(path)
	return ok && inferred == convention
}

func entryAccessCanRead(access FileSystemAccessMode) bool {
	return access == FileSystemAccessRead || access == FileSystemAccessWrite
}

func containsFileSystemEntry(entries []FileSystemSandboxEntry, target FileSystemSandboxEntry) bool {
	for _, entry := range entries {
		if sameFileSystemEntry(entry, target) {
			return true
		}
	}
	return false
}

func sameFileSystemEntry(a, b FileSystemSandboxEntry) bool {
	if a.Access != b.Access {
		return false
	}
	if a.Path.Type != b.Path.Type {
		return false
	}
	switch a.Path.Type {
	case "glob_pattern":
		return a.Path.Pattern == b.Path.Pattern
	case "special":
		if a.Path.Value == nil || b.Path.Value == nil {
			return a.Path.Value == nil && b.Path.Value == nil
		}
		return strings.EqualFold(a.Path.Value.Kind, b.Path.Value.Kind) &&
			a.Path.Value.Path == b.Path.Value.Path &&
			ptrStringValue(a.Path.Value.Subpath) == ptrStringValue(b.Path.Value.Subpath)
	default:
		left, leftOK := utils.PathIdentityKey(a.Path.Path)
		right, rightOK := utils.PathIdentityKey(b.Path.Path)
		if leftOK && rightOK {
			return left == right
		}
		return a.Path.Path == b.Path.Path
	}
}
