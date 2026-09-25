package state

import (
	"errors"
	"fmt"
	"strings"
)

// Rust parity: codex-rs/guardian-context/src/permissions.rs.
//
// The host resolves the reviewed environment and its filesystem policy; this
// section only formats that evidence, and never resolves or relaxes a
// restriction. Denied paths and globs are evidence strings, not filesystem paths
// the reviewer may resolve.

// PermissionContext is the reviewed environment's active-policy evidence.
type PermissionContext struct {
	// EnvironmentID is the reviewed environment's selection id, when the host
	// resolved a specific one rather than the parent turn's profile.
	EnvironmentID *string
	DeniedPaths   []string
	DeniedGlobs   []string
}

const (
	// permissionContextStart carries Rust's leading newline: the marker opens a
	// new paragraph inside the message that carries it.
	permissionContextStart = "\n>>> PARENT TURN PERMISSION CONTEXT START\n"
	permissionContextEnd   = ">>> PARENT TURN PERMISSION CONTEXT END\n"
	// maxAsyncPermissionBytes keeps async permission evidence below one thousand
	// estimated tokens.
	maxAsyncPermissionBytes = 3_000
	permissionContextNoDeny = " has no explicit denied-read paths/globs.\n"
	permissionContextDeny   = " denies reading these paths/globs. These are policy restrictions; do not approve escalation whose purpose is to read them.\n"
)

// ErrPermissionContextTooLarge reports that an asynchronous reviewer would have
// to drop restrictions, so the caller must fall back to synchronous review.
var ErrPermissionContextTooLarge = errors.New("permission context exceeds the asynchronous evidence limit")

// Empty reports whether the context carries no evidence at all. Rust contributes
// no section for an empty context.
func (p *PermissionContext) Empty() bool {
	if p == nil {
		return true
	}
	return p.EnvironmentID == nil && len(p.DeniedPaths) == 0 && len(p.DeniedGlobs) == 0
}

// RenderPermissionContextBody mirrors Rust's `PermissionContext::body`.
func (p *PermissionContext) RenderPermissionContextBody() string {
	if p == nil {
		return ""
	}
	entries := make([]string, 0, len(p.DeniedPaths)+len(p.DeniedGlobs))
	for _, path := range p.DeniedPaths {
		entries = append(entries, fmt.Sprintf("- path `%s`", path))
	}
	for _, glob := range p.DeniedGlobs {
		entries = append(entries, fmt.Sprintf("- glob `%s`", glob))
	}
	scope := "The parent turn's active permission profile"
	if p.EnvironmentID != nil {
		scope = fmt.Sprintf("The active permission profile for environment %q", *p.EnvironmentID)
	}
	if len(entries) == 0 {
		return scope + permissionContextNoDeny
	}
	return scope + permissionContextDeny + strings.Join(entries, "\n") + "\n"
}

// PermissionContextSectionItems mirrors `PermissionContextSection::contribute`
// for the synchronous reviewer: nothing when there is no evidence, otherwise the
// marked section. Every item ends with a newline, so a caller may concatenate
// them.
func PermissionContextSectionItems(context *PermissionContext) []string {
	items, err := permissionContextSectionItems(context, false)
	if err != nil {
		return nil
	}
	return items
}

// AsyncPermissionContextSectionItems mirrors the asynchronous rule: evidence
// below one thousand estimated tokens is delivered, and anything larger is an
// error so the async scorer falls back to synchronous review instead of dropping
// restrictions.
func AsyncPermissionContextSectionItems(context *PermissionContext) ([]string, error) {
	return permissionContextSectionItems(context, true)
}

func permissionContextSectionItems(context *PermissionContext, async bool) ([]string, error) {
	if context.Empty() {
		return nil, nil
	}
	body := context.RenderPermissionContextBody()
	if async && len(body) > maxAsyncPermissionBytes {
		return nil, ErrPermissionContextTooLarge
	}
	return []string{permissionContextStart, body, permissionContextEnd}, nil
}
