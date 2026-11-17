package app

import (
	"strings"
	"unicode"

	"codex_go/appserver"
	"codex_go/tui/chatwidget"
	"codex_go/tui/markdown"
)

// Rust parity: codex-rs/tui/src/app/sidebar_files.rs `item_entries`,
// `newest_item_entries` and `resolve` (#61920).
//
// The sidebar inventory lists files the conversation mentions: Markdown links in
// agent messages, plans and user text, completed file changes, and saved
// generated images. Entries resolve against the app-server workspace and keep
// their optional line/column target.

// resolveSidebarFileLink validates a link and resolves it against the
// workspace. An empty workspace keeps the raw path so an unresolved foreign
// path still lists (Rust `resolve`).
func resolveSidebarFileLink(cwd string, link markdown.SidebarFileLink) (markdown.SidebarFileLink, bool) {
	if link.Path == "" || len(link.Path) > 4096 || strings.ContainsFunc(link.Path, unicode.IsControl) {
		return link, false
	}
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return link, true
	}
	resolved, err := resolveRemotePreviewPath(link.Path, cwd)
	if err != nil || len(resolved) > 4096 {
		return link, false
	}
	link.Path = resolved
	return link, true
}

// sidebarItemFileLinks lists the files one completed thread item mentions, in
// item order. User messages that carry a realtime delegation prompt and
// incomplete patches/images contribute nothing.
func sidebarItemFileLinks(cwd string, item appserver.ThreadItem) []markdown.SidebarFileLink {
	var entries []markdown.SidebarFileLink
	found := func(link markdown.SidebarFileLink) {
		if resolved, ok := resolveSidebarFileLink(cwd, link); ok {
			entries = append(entries, resolved)
		}
	}
	addMarkdown := func(source string) {
		for _, link := range markdown.SidebarFileLinks(source) {
			found(link)
		}
	}
	switch remoteTUINormalizedThreadItemType(item.Type) {
	case "agentmessage", "plan":
		addMarkdown(item.Text)
	case "usermessage":
		if sidebarUserMessageIsDelegation(item) {
			return nil
		}
		for _, content := range item.Content {
			if strings.EqualFold(strings.TrimSpace(content.Type), "text") {
				addMarkdown(content.Text)
			}
		}
	case "filechange":
		if !strings.EqualFold(strings.TrimSpace(item.Status), "completed") {
			return nil
		}
		for _, change := range sidebarFileChanges(item) {
			switch strings.ToLower(strings.TrimSpace(change.Kind)) {
			case "delete":
				continue
			case "update":
				if path := strings.TrimSpace(change.MovePath); path != "" {
					found(markdown.SidebarFileLink{Path: path})
					continue
				}
				found(markdown.SidebarFileLink{Path: change.Path})
			case "add":
				found(markdown.SidebarFileLink{Path: change.Path})
			}
		}
	case "imagegeneration":
		if !strings.EqualFold(strings.TrimSpace(item.Status), "completed") {
			return nil
		}
		for _, path := range sidebarImageSavedPaths(item.Data) {
			found(markdown.SidebarFileLink{Path: path})
		}
	}
	return entries
}

// sidebarNewestItemFileLinks reverses one item's entries so the newest link in
// the item is considered first (Rust `newest_item_entries`).
func sidebarNewestItemFileLinks(cwd string, item appserver.ThreadItem) []markdown.SidebarFileLink {
	entries := sidebarItemFileLinks(cwd, item)
	for left, right := 0, len(entries)-1; left < right; left, right = left+1, right-1 {
		entries[left], entries[right] = entries[right], entries[left]
	}
	return entries
}

func sidebarUserMessageIsDelegation(item appserver.ThreadItem) bool {
	for _, content := range item.Content {
		if !strings.EqualFold(strings.TrimSpace(content.Type), "text") {
			continue
		}
		if _, ok := chatwidget.RealtimeDelegationInput(content.Text); ok {
			return true
		}
	}
	return false
}

type sidebarFileChange struct {
	Path     string
	Kind     string
	MovePath string
}

// sidebarFileChanges decodes an item's file-change list, which the app-server
// publishes as `changes: [{path, kind}]` where `kind` carries a move target for
// updates.
func sidebarFileChanges(item appserver.ThreadItem) []sidebarFileChange {
	raw, ok := item.Data["changes"]
	if !ok {
		return nil
	}
	var items []any
	switch typed := raw.(type) {
	case []map[string]any:
		items = make([]any, 0, len(typed))
		for _, entry := range typed {
			items = append(items, entry)
		}
	case []any:
		items = typed
	default:
		return nil
	}
	changes := make([]sidebarFileChange, 0, len(items))
	for _, entry := range items {
		change := sidebarFileChangeFromAny(entry)
		if change.Path == "" || change.Kind == "" {
			continue
		}
		changes = append(changes, change)
	}
	return changes
}

func sidebarFileChangeFromAny(value any) sidebarFileChange {
	entry, ok := value.(map[string]any)
	if !ok {
		return sidebarFileChange{}
	}
	change := sidebarFileChange{
		Path: strings.TrimSpace(sidebarStringFromAny(entry["path"])),
	}
	switch kind := entry["kind"].(type) {
	case string:
		change.Kind = strings.TrimSpace(kind)
	case map[string]any:
		change.Kind = strings.TrimSpace(sidebarStringFromAny(kind["type"]))
		change.MovePath = strings.TrimSpace(sidebarStringFromAny(kind["move_path"]))
		if change.MovePath == "" {
			change.MovePath = strings.TrimSpace(sidebarStringFromAny(kind["movePath"]))
		}
	}
	return change
}

// sidebarImageSavedPaths reads the saved image paths of a completed
// image-generation item, which the app-server publishes as `savedPath` (or
// `saved_path`) holding one path or a list.
func sidebarImageSavedPaths(data map[string]any) []string {
	raw, ok := data["savedPath"]
	if !ok {
		raw = data["saved_path"]
	}
	var paths []string
	switch typed := raw.(type) {
	case string:
		if path := strings.TrimSpace(typed); path != "" {
			paths = append(paths, path)
		}
	case []any:
		for _, entry := range typed {
			if path := strings.TrimSpace(sidebarStringFromAny(entry)); path != "" {
				paths = append(paths, path)
			}
		}
	}
	return paths
}

func sidebarStringFromAny(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}
