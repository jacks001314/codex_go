package app

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/cli"
	"codex_go/rollout"
	"codex_go/session"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

// Rust #60319: the sidebar chooser discovers saved side chats from their
// attached-thread records and shows the latest user prompt beneath each title.

const (
	sideChatListPageLimit = 100
	sideChatListMaxPages  = 10
	sideChatItemsPageSize = 16
	sideChatItemsMaxPages = 4
)

func interactiveEmbeddedListSideChats(store *session.Store) codextea.SideChatListFunc {
	return func(parentThreadID string) ([]codextea.SideChatChoice, error) {
		parentThreadID = strings.TrimSpace(parentThreadID)
		if store == nil || parentThreadID == "" {
			return nil, nil
		}
		records := make([]session.Record, 0, sideChatListPageLimit)
		cursor := ""
		for pageIndex := 0; pageIndex < sideChatListMaxPages; pageIndex++ {
			page, err := store.List(session.ListOptions{
				PageSize:      sideChatListPageLimit,
				Cursor:        cursor,
				SortDirection: session.SortAsc,
				Relation:      &session.RelationFilter{AttachedThreadsOf: session.ThreadID(parentThreadID)},
			})
			if err != nil {
				return nil, err
			}
			for _, summary := range page.Records {
				record := embeddedSideChatLocalRecord(store, summary)
				if record == nil {
					continue
				}
				if embeddedAttachedSideChat(*record) {
					records = append(records, *record)
				}
			}
			if strings.TrimSpace(page.NextCursor) == "" {
				break
			}
			cursor = page.NextCursor
		}
		choices := make([]codextea.SideChatChoice, 0, len(records))
		entries := embeddedSideChatNameEntries(store, records)
		for index := range records {
			choice := embeddedSideChatChoice(records[index])
			if entry, ok := entries[choice.ThreadID]; ok && embeddedInheritedSideChatName(records[index], choice.Name, entry) {
				choice.Name = ""
				choice.Preview = embeddedSideChatPreview(records[index])
			}
			choices = append(choices, choice)
		}
		return choices, nil
	}
}

// embeddedAttachedSideChat keeps only saved attached side chats. Rust reads the
// child's own policy and boundary checkpoint before listing it.
func embeddedAttachedSideChat(record session.Record) bool {
	if record.AttachedThreadParentID == "" {
		return false
	}
	_, ok := sideChatContextCheckpoint(record.Items)
	return ok
}

// embeddedSideChatLocalRecord loads only the child's own rollout records.
// Reading the materialized store record would prepend inherited paginated
// history, which must not label or qualify the side chat (Rust
// side_context_checkpoint walks the child rollout).
func embeddedSideChatLocalRecord(store *session.Store, summary session.Record) *session.Record {
	record, err := store.Read(summary.ID, true, false)
	if err != nil || record == nil {
		return nil
	}
	codexHome := filepath.Dir(store.Root())
	// Forks inherit the source's rollout_path in their metadata, so locate the
	// child's own rollout by id instead of trusting that field.
	path, err := rollout.FindThreadPath(codexHome, string(record.ID), record.Archived)
	if err != nil {
		return nil
	}
	if path == "" {
		return nil
	}
	local, err := rollout.RecordFromPath(path, record.Archived)
	if err != nil || local == nil {
		return nil
	}
	local.Title = record.Title
	return local
}

func embeddedSideChatChoice(record session.Record) codextea.SideChatChoice {
	choice := codextea.SideChatChoice{
		ThreadID: strings.TrimSpace(string(record.ID)),
		Name:     strings.TrimSpace(record.Title),
	}
	if choice.Name == "" {
		choice.Preview = embeddedSideChatPreview(record)
	}
	return choice
}

func embeddedSideChatPreview(record session.Record) string {
	if len(record.Items) == 0 {
		return firstSideChatPromptLine(record.Preview)
	}
	// Rust reads the preview from the child's own visible history. An attached
	// fork inherits the parent's summary preview, which must not label it.
	return firstSideChatPromptFromRecord(record)
}

func embeddedSideChatNameEntries(store *session.Store, records []session.Record) map[string]rollout.SessionIndexEntry {
	if store == nil || len(records) == 0 {
		return nil
	}
	threadIDs := make(map[string]struct{}, len(records))
	for _, record := range records {
		if strings.TrimSpace(record.Title) != "" {
			threadIDs[strings.TrimSpace(string(record.ID))] = struct{}{}
		}
	}
	if len(threadIDs) == 0 {
		return nil
	}
	entries, err := rollout.FindThreadNameEntriesByIDs(filepath.Dir(store.Root()), threadIDs)
	if err != nil {
		return nil
	}
	return entries
}

// embeddedInheritedSideChatName reports whether a saved chat still carries the
// name it inherited at fork. Older servers wrote that name during the fork,
// before the side-chat boundary; a later write is an explicit rename (Rust
// side_chat_history).
func embeddedInheritedSideChatName(record session.Record, name string, entry rollout.SessionIndexEntry) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	if strings.TrimSpace(entry.ThreadName) != name {
		return false
	}
	opened, ok := sideChatContextCheckpoint(record.Items)
	if !ok {
		return false
	}
	written, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(entry.UpdatedAt))
	if err != nil {
		return false
	}
	return !written.After(opened)
}

func firstSideChatPromptFromRecord(record session.Record) string {
	items := record.Items
	if index, _, ok := sideChatContextCheckpointIndex(items); ok {
		items = items[index+1:]
	}
	messages := interactiveSessionMessagesFromItems(items, record.Metadata.CWD, reasoningProjectionChatWidget, false)
	for _, message := range messages {
		if message.Role != codextui.RoleUser {
			continue
		}
		if prompt := firstSideChatPromptLine(message.UserPrompt); prompt != "" {
			return prompt
		}
		if prompt := firstSideChatPromptLine(message.Text); prompt != "" {
			return prompt
		}
	}
	return ""
}

func interactiveEmbeddedReadSideChatPrompts(store *session.Store) codextea.SideChatPromptFunc {
	return func(threadIDs []string) map[string]string {
		prompts := make(map[string]string, len(threadIDs))
		if store == nil {
			return prompts
		}
		for _, threadID := range threadIDs {
			threadID = strings.TrimSpace(threadID)
			if threadID == "" {
				continue
			}
			record, err := store.Read(session.ThreadID(threadID), true, true)
			if err != nil {
				continue
			}
			prompts[threadID] = firstSideChatPromptFromRecord(*record)
		}
		return prompts
	}
}

func interactiveEmbeddedOpenSideChat(store *session.Store) codextea.SideOpenFunc {
	return func(parentThreadID, sideThreadID string) (codextea.AgentThreadSwitchResponse, error) {
		return interactiveLocalSwitchAgentThread(store, sideThreadID)
	}
}

func interactiveRemoteListSideChats(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) codextea.SideChatListFunc {
	return func(parentThreadID string) ([]codextea.SideChatChoice, error) {
		parentThreadID = strings.TrimSpace(parentThreadID)
		if parentThreadID == "" {
			return nil, nil
		}
		client, err := openRemoteSessionClient(ctx, endpoint)
		if err != nil {
			return nil, err
		}
		defer client.close()
		archived := false
		limit := sideChatListPageLimit
		var cursor *string
		choices := make([]codextea.SideChatChoice, 0, sideChatListPageLimit)
		for page := 0; page < sideChatListMaxPages; page++ {
			params := appserver.ThreadListParams{
				AttachedToThreadID: &parentThreadID,
				Archived:           &archived,
				UseStateDBOnly:     true,
				ModelProviders:     []string{},
				SortDirection:      appserver.SortAsc,
				Limit:              &limit,
			}
			if cursor != nil {
				params.Cursor = cursor
			}
			response, err := remoteThreadListWithCwdFallback(ctx, client, params)
			if err != nil {
				return nil, err
			}
			if response == nil {
				break
			}
			for _, thread := range response.Data {
				if thread.ThreadSource == nil || strings.TrimSpace(string(*thread.ThreadSource)) != codextea.AttachedSideChatThreadSource {
					continue
				}
				choice := codextea.SideChatChoice{ThreadID: strings.TrimSpace(thread.ID)}
				if choice.ThreadID == "" {
					continue
				}
				if thread.Name != nil {
					choice.Name = strings.TrimSpace(*thread.Name)
				}
				choice.Preview = firstSideChatPromptLine(thread.Preview)
				if choice.Name != "" {
					choice.Preview = ""
				}
				choices = append(choices, choice)
			}
			if response.NextCursor == nil || strings.TrimSpace(*response.NextCursor) == "" {
				break
			}
			cursor = response.NextCursor
		}
		return choices, nil
	}
}

func interactiveRemoteReadSideChatPrompts(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) codextea.SideChatPromptFunc {
	return func(threadIDs []string) map[string]string {
		prompts := make(map[string]string, len(threadIDs))
		client, err := openRemoteSessionClient(ctx, endpoint)
		if err != nil {
			return prompts
		}
		defer client.close()
		for _, threadID := range threadIDs {
			threadID = strings.TrimSpace(threadID)
			if threadID == "" {
				continue
			}
			if prompt := remoteSideChatPrompt(ctx, client, threadID); prompt != "" {
				prompts[threadID] = prompt
			}
		}
		return prompts
	}
}

func remoteSideChatPrompt(ctx context.Context, client *remoteAppServerTUIClient, threadID string) string {
	limit := sideChatItemsPageSize
	var cursor *appserver.ThreadItemsListCursor
	for page := 0; page < sideChatItemsMaxPages; page++ {
		params := appserver.ThreadItemsListParams{
			ThreadID:      threadID,
			Limit:         &limit,
			SortDirection: appserver.SortAsc,
		}
		if cursor != nil {
			params.Cursor = cursor
		}
		var response appserver.ThreadItemsListResponse
		if err := remoteSessionRequest(ctx, client, appserver.MethodThreadItemsList, params, &response); err != nil {
			return ""
		}
		for _, entry := range response.Data {
			if prompt := sideChatPromptFromThreadItem(entry.Item); prompt != "" {
				return prompt
			}
		}
		if response.NextCursor == nil || strings.TrimSpace(*response.NextCursor) == "" {
			return ""
		}
		cursor = &appserver.ThreadItemsListCursor{Opaque: strings.TrimSpace(*response.NextCursor)}
	}
	return ""
}

func sideChatPromptFromThreadItem(item appserver.ThreadItem) string {
	itemType := strings.ToLower(strings.TrimSpace(item.Type))
	role := strings.ToLower(strings.TrimSpace(item.Role))
	if itemType != "usermessage" && itemType != "user_message" && role != "user" {
		return ""
	}
	text := strings.TrimSpace(item.Text)
	if text == "" {
		for _, part := range item.Content {
			if strings.TrimSpace(part.Text) != "" {
				text = part.Text
				break
			}
		}
	}
	return firstSideChatPromptLine(text)
}

func interactiveRemoteOpenSideChat(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint, root *cli.RootOptions) codextea.SideOpenFunc {
	return func(parentThreadID, sideThreadID string) (codextea.AgentThreadSwitchResponse, error) {
		return interactiveRemoteSwitchAgentThread(ctx, endpoint, root, sideThreadID)
	}
}

func firstSideChatPromptLine(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if index := strings.IndexByte(text, '\n'); index >= 0 {
		text = text[:index]
	}
	return strings.TrimSpace(text)
}
