package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"codex_go/gitutil"
	"codex_go/worktree"
)

// Rust parity: codex-rs/tui/src/worktree_browser.rs plus the presentation rules
// from chatwidget/worktree_picker.rs (#43286/#43279).
//
// Managed worktrees are discovered from the configured pool and classified by
// the thread that owns them. Ownership is a recorded binding, not activity or
// exclusion: an owner thread may be absent, unavailable, archived, or
// resumable, and only a resumable owner can be resumed from the browser.

// WorktreeBrowserRequest binds one browse request to the session that opened it
// so stale results can be discarded after a switch.
type WorktreeBrowserRequest struct {
	ID       string
	CWD      string
	ThreadID string
}

// WorktreeOwnerKind classifies a managed worktree's recorded owner.
type WorktreeOwnerKind int

const (
	// WorktreeOwnerNone means the pool recorded no owner thread.
	WorktreeOwnerNone WorktreeOwnerKind = iota
	// WorktreeOwnerUnavailable means an owner thread id is recorded but its
	// thread summary could not be resolved.
	WorktreeOwnerUnavailable
	// WorktreeOwnerArchived means the owner thread is archived.
	WorktreeOwnerArchived
	// WorktreeOwnerResumable means the owner thread can be resumed.
	WorktreeOwnerResumable
)

// WorktreeThreadSummary is the owner metadata the browser needs to present a
// worktree row.
type WorktreeThreadSummary struct {
	ID        string
	Title     string
	UpdatedAt int64
}

// WorktreeOwner records the classification of one managed worktree's owner.
type WorktreeOwner struct {
	Kind     WorktreeOwnerKind
	ThreadID string
	Summary  *WorktreeThreadSummary
}

// WorktreeBrowserEntry is one listed managed worktree.
type WorktreeBrowserEntry struct {
	Root  string
	CWD   string
	Owner WorktreeOwner
}

// WorktreeOwnerLookup is the owner-thread metadata supplied by the session
// source so the browser can classify an owner as archived or resumable.
type WorktreeOwnerLookup struct {
	ID        string
	Name      string
	Preview   string
	UpdatedAt int64
	Archived  bool
}

// WorktreeOwnerLookupFunc resolves a recorded owner thread id, returning false
// when its summary cannot be read.
type WorktreeOwnerLookupFunc func(threadID string) (WorktreeOwnerLookup, bool)

// worktreeOwnerTitleMaxChars bounds an owner title to Rust's 80-character limit
// (79 characters plus an ellipsis).
const worktreeOwnerTitleMaxChars = 80

// ManagedWorktreeRepositoryAvailable reports whether cwd lives inside a Git
// repository, the local prerequisite for managed-worktree operations
// (Rust #43120 get_git_repo_root).
func ManagedWorktreeRepositoryAvailable(cwd string) bool {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return false
	}
	root, err := gitutil.DiscoverGitRoot(context.Background(), cwd)
	return err == nil && strings.TrimSpace(root) != ""
}

// ListManagedWorktreeEntries mirrors Rust worktree_browser::list: the managed
// linked checkouts of the repository containing cwd, each carrying an owner
// thread id when the pool recorded one.
func ListManagedWorktreeEntries(settings worktree.WorktreeSettings, cwd string) ([]WorktreeBrowserEntry, error) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		cwd = "."
	}
	manager := worktree.NewWorktreeManager(settings)
	listed, err := manager.List(cwd)
	if err != nil {
		return nil, err
	}
	entries := make([]WorktreeBrowserEntry, 0, len(listed))
	for _, checkout := range listed {
		entry := WorktreeBrowserEntry{
			Root:  checkout.Root,
			CWD:   checkout.CWD,
			Owner: WorktreeOwner{Kind: WorktreeOwnerNone},
		}
		if id, ownerErr := manager.Owner(checkout.Root); ownerErr == nil && strings.TrimSpace(id) != "" {
			entry.Owner = WorktreeOwner{Kind: WorktreeOwnerUnavailable, ThreadID: strings.TrimSpace(id)}
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// ResolveWorktreeOwnerSummaries mirrors the owner-resolution phase of Rust
// worktree_browser::fetch: every unavailable owner is looked up once and
// classified as archived or resumable with a bounded title. Entries whose
// owner cannot be resolved stay unavailable.
func ResolveWorktreeOwnerSummaries(entries []WorktreeBrowserEntry, lookup WorktreeOwnerLookupFunc) []WorktreeBrowserEntry {
	if len(entries) == 0 {
		return entries
	}
	for i := range entries {
		entry := &entries[i]
		if entry.Owner.Kind != WorktreeOwnerUnavailable {
			continue
		}
		if lookup == nil {
			continue
		}
		resolved, ok := lookup(entry.Owner.ThreadID)
		if !ok {
			continue
		}
		id := strings.TrimSpace(resolved.ID)
		if id == "" {
			id = entry.Owner.ThreadID
		}
		kind := WorktreeOwnerResumable
		if resolved.Archived {
			kind = WorktreeOwnerArchived
		}
		summary := WorktreeThreadSummary{
			ID:        id,
			Title:     WorktreeOwnerTitle(resolved),
			UpdatedAt: resolved.UpdatedAt,
		}
		entry.Owner = WorktreeOwner{Kind: kind, ThreadID: id, Summary: &summary}
	}
	return entries
}

// WorktreeOwnerTitle mirrors Rust's owner-title fallback and 80-character
// truncation: the thread name, else its first non-empty preview line, else
// "Untitled conversation".
func WorktreeOwnerTitle(lookup WorktreeOwnerLookup) string {
	title := strings.TrimSpace(lookup.Name)
	if title == "" {
		title = firstNonEmptyWorktreePreviewLine(lookup.Preview)
	}
	if title == "" {
		title = "Untitled conversation"
	}
	runes := []rune(title)
	if len(runes) > worktreeOwnerTitleMaxChars {
		title = string(runes[:worktreeOwnerTitleMaxChars-1]) + "\u2026"
	}
	return title
}

func firstNonEmptyWorktreePreviewLine(preview string) string {
	for _, line := range strings.Split(strings.ReplaceAll(preview, "\r\n", "\n"), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// WorktreeBrowserSubtitle mirrors Rust's list subtitle.
func WorktreeBrowserSubtitle(entries []WorktreeBrowserEntry) string {
	if len(entries) == 0 {
		return "No worktrees in this repository's configured pool"
	}
	return "Select a worktree to resume, copy its path, or delete it"
}

// WorktreeBrowserRow returns the list row for an entry: its display name, its
// description, and the search value. Mirrors Rust's item mapping.
func WorktreeBrowserRow(entry WorktreeBrowserEntry, now int64) (name string, description string, searchValue string) {
	switch entry.Owner.Kind {
	case WorktreeOwnerArchived, WorktreeOwnerResumable:
		title := entry.CWD
		updated := ""
		if entry.Owner.Summary != nil {
			title = entry.Owner.Summary.Title
			updated = "updated " + WorktreeUpdatedAgo(entry.Owner.Summary.UpdatedAt, now) + " \u00b7 "
		}
		if entry.Owner.Kind == WorktreeOwnerArchived {
			updated = "Archived \u00b7 " + updated
		}
		name = title
		description = updated + entry.CWD
	case WorktreeOwnerUnavailable:
		name = entry.CWD
		description = "Owner thread unavailable"
	default:
		name = entry.CWD
		description = "No attached thread"
	}
	return name, description, name + " " + entry.CWD
}

// WorktreeUpdatedAgo mirrors Rust worktree_updated_ago.
func WorktreeUpdatedAgo(updatedAt int64, now int64) string {
	seconds := now - updatedAt
	if seconds < 0 {
		seconds = 0
	}
	switch {
	case seconds < 60:
		return "just now"
	case seconds < 3_600:
		return fmt.Sprintf("%dm ago", seconds/60)
	case seconds < 86_400:
		return fmt.Sprintf("%dh ago", seconds/3_600)
	default:
		return fmt.Sprintf("%dd ago", seconds/86_400)
	}
}

// WorktreeBrowserActionKind identifies one worktree action.
type WorktreeBrowserActionKind int

const (
	WorktreeActionResume WorktreeBrowserActionKind = iota
	WorktreeActionCopy
	WorktreeActionRemove
)

// WorktreeBrowserActionItem is one rendered action row of the worktree
// actions view.
type WorktreeBrowserActionItem struct {
	Name           string
	Description    string
	Disabled       bool
	DisabledReason string
	Action         WorktreeBrowserAction
}

// WorktreeBrowserAction is the action a row dispatches.
type WorktreeBrowserAction struct {
	Kind     WorktreeBrowserActionKind
	ThreadID string
	Path     string
}

// WorktreeActionsTitle mirrors Rust's actions-view title.
func WorktreeActionsTitle(entry WorktreeBrowserEntry) string {
	if entry.Owner.Summary != nil &&
		(entry.Owner.Kind == WorktreeOwnerArchived || entry.Owner.Kind == WorktreeOwnerResumable) {
		return "Worktree: " + entry.Owner.Summary.Title
	}
	return "Worktree"
}

// WorktreeBrowserActionItems mirrors Rust's show_managed_worktree_actions: the
// owner can only be resumed when it is resumable, the working directory can
// always be copied, and deleting is refused while the requesting session lives
// inside the worktree it would remove.
func WorktreeBrowserActionItems(entry WorktreeBrowserEntry, requestCWD string) []WorktreeBrowserActionItem {
	items := make([]WorktreeBrowserActionItem, 0, 3)
	if entry.Owner.Kind == WorktreeOwnerResumable {
		threadID := strings.TrimSpace(entry.Owner.ThreadID)
		if entry.Owner.Summary != nil && strings.TrimSpace(entry.Owner.Summary.ID) != "" {
			threadID = strings.TrimSpace(entry.Owner.Summary.ID)
		}
		items = append(items, WorktreeBrowserActionItem{
			Name:   "Resume owner thread",
			Action: WorktreeBrowserAction{Kind: WorktreeActionResume, ThreadID: threadID},
		})
	}
	items = append(items, WorktreeBrowserActionItem{
		Name:   "Copy working directory",
		Action: WorktreeBrowserAction{Kind: WorktreeActionCopy, Path: entry.CWD},
	})
	canDelete := !WorktreePathWithin(entry.Root, requestCWD)
	deleteItem := WorktreeBrowserActionItem{
		Name:   "Delete worktree",
		Action: WorktreeBrowserAction{Kind: WorktreeActionRemove, Path: entry.Root},
	}
	if !canDelete {
		deleteItem.Disabled = true
		deleteItem.DisabledReason = "Switch to another checkout before deleting this one"
	}
	return append(items, deleteItem)
}

// WorktreeDeleteConfirmationItems mirrors Rust's
// confirm_managed_worktree_removal: Cancel is the first (default) row and
// deleting keeps thread history.
func WorktreeDeleteConfirmationItems(root string) []WorktreeBrowserActionItem {
	return []WorktreeBrowserActionItem{
		{Name: "Cancel"},
		{
			Name:        "Delete worktree",
			Description: "Keeps thread history; may disrupt other sessions",
			Action:      WorktreeBrowserAction{Kind: WorktreeActionRemove, Path: root},
		},
	}
}

// RemoveManagedWorktree mirrors Rust worktree_browser::remove using the
// safety-checked manager path (refuses the current checkout and ignored files).
func RemoveManagedWorktree(settings worktree.WorktreeSettings, sourceCWD string, root string) error {
	return worktree.NewWorktreeManager(settings).RemoveManaged(sourceCWD, root)
}

// worktreePathWithin reports whether child is root or inside root, using the
// same component-wise semantics as Rust's Path::starts_with.
func WorktreePathWithin(root string, child string) bool {
	root = filepath.Clean(strings.TrimSpace(root))
	child = filepath.Clean(strings.TrimSpace(child))
	if root == "" || child == "" {
		return false
	}
	if root == child {
		return true
	}
	relative, err := filepath.Rel(root, child)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
