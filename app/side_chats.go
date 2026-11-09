package app

import (
	"context"
	"strings"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/cli"
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
		page, err := store.List(session.ListOptions{
			PageSize:       sideChatListPageLimit,
			SortDirection:  session.SortAsc,
			Relation:       &session.RelationFilter{AttachedThreadsOf: session.ThreadID(parentThreadID)},
			IncludeHistory: false,
		})
		if err != nil {
			return nil, err
		}
		choices := make([]codextea.SideChatChoice, 0, len(page.Records))
		for index := range page.Records {
			choices = append(choices, embeddedSideChatChoice(page.Records[index]))
		}
		return choices, nil
	}
}

func embeddedSideChatChoice(record session.Record) codextea.SideChatChoice {
	choice := codextea.SideChatChoice{
		ThreadID: strings.TrimSpace(string(record.ID)),
		Name:     strings.TrimSpace(record.Title),
	}
	if choice.Name == "" {
		choice.Preview = firstSideChatPromptFromRecord(record)
		if choice.Preview == "" {
			choice.Preview = firstSideChatPromptLine(record.Preview)
		}
	}
	return choice
}

func firstSideChatPromptFromRecord(record session.Record) string {
	messages := interactiveSessionMessagesFromRecord(&record, reasoningProjectionChatWidget, false)
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
				choice := codextea.SideChatChoice{ThreadID: strings.TrimSpace(thread.ID)}
				if choice.ThreadID == "" {
					continue
				}
				choice.Preview = firstSideChatPromptLine(thread.Preview)
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
