package app

import (
	"context"
	"errors"
	"strings"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/eventmap"
	"codex_go/session"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

// Rust resume_picker_transcript_preview.rs: load the newest few transcript
// lines for an expanded resume-picker row.
const (
	transcriptPreviewMaxLines  = 6
	transcriptPreviewPageSize  = 6
	transcriptPreviewPageLimit = 100
	transcriptPreviewScanLimit = 4 * transcriptPreviewPageLimit
)

// interactiveRemoteTranscriptPreviewHandler loads a session's transcript
// preview through the app server.
func interactiveRemoteTranscriptPreviewHandler(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) codextea.TranscriptPreviewFunc {
	return func(threadID string, cwd string) ([]codextui.TranscriptPreviewLine, error) {
		threadID = strings.TrimSpace(threadID)
		if threadID == "" {
			return nil, errors.New("transcript preview requires a thread id")
		}
		client, err := openRemoteSessionClient(ctx, endpoint)
		if err != nil {
			return nil, err
		}
		defer client.close()
		// Paginated history is preferred; a server without thread/items/list
		// falls back to the thread's loaded turns.
		if lines, err := remoteTranscriptPreviewFromItems(ctx, client, threadID, cwd); err == nil {
			return lines, nil
		}
		return remoteTranscriptPreviewFromThread(ctx, client, threadID, cwd)
	}
}

// remoteTranscriptPreviewFromItems pages backwards through thread/items/list
// until the preview is full or the bounded scan budget is exhausted (Rust
// ThreadHistoryMode::Paginated).
func remoteTranscriptPreviewFromItems(ctx context.Context, client *remoteAppServerTUIClient, threadID string, cwd string) ([]codextui.TranscriptPreviewLine, error) {
	lines := make([]codextui.TranscriptPreviewLine, 0, transcriptPreviewMaxLines)
	var cursor *string
	scanned := 0
	seenCursors := map[string]bool{}
	for {
		remaining := transcriptPreviewScanLimit - scanned
		if remaining <= 0 {
			break
		}
		pageSize := transcriptPreviewPageLimit
		if cursor == nil {
			pageSize = transcriptPreviewPageSize
		}
		if pageSize > remaining {
			pageSize = remaining
		}
		limit := pageSize
		sortDesc := appserver.SortDesc
		var response appserver.ThreadItemsListResponse
		var cursorParam *appserver.ThreadItemsListCursor
		if cursor != nil {
			cursorParam = &appserver.ThreadItemsListCursor{Opaque: *cursor}
		}
		params := appserver.ThreadItemsListParams{
			ThreadID:      threadID,
			Cursor:        cursorParam,
			Limit:         &limit,
			SortDirection: sortDesc,
		}
		if err := remoteSessionRequest(ctx, client, appserver.MethodThreadItemsList, params, &response); err != nil {
			return nil, err
		}
		scanned += len(response.Data)
		for _, entry := range response.Data {
			appendTranscriptPreviewItem(&lines, entry.Item, cwd)
			if len(lines) >= transcriptPreviewMaxLines {
				break
			}
		}
		if len(lines) >= transcriptPreviewMaxLines || scanned >= transcriptPreviewScanLimit {
			break
		}
		next := response.NextCursor
		if next == nil {
			break
		}
		nextCursor := strings.TrimSpace(*next)
		if nextCursor == "" || seenCursors[nextCursor] {
			break
		}
		seenCursors[nextCursor] = true
		cursor = &nextCursor
	}
	reverseTranscriptPreviewLines(lines)
	return lines, nil
}

// remoteTranscriptPreviewFromThread walks a legacy thread's loaded turns
// newest-first (Rust ThreadHistoryMode::Legacy after hydration).
func remoteTranscriptPreviewFromThread(ctx context.Context, client *remoteAppServerTUIClient, threadID string, cwd string) ([]codextui.TranscriptPreviewLine, error) {
	thread, err := remoteTUIReadThread(ctx, client, threadID, true)
	if err != nil {
		return nil, err
	}
	return transcriptPreviewFromTurns(thread.Turns, cwd), nil
}

// interactiveTranscriptPreviewHandler loads a session's transcript preview for
// the embedded TUI through the in-process router.
func interactiveTranscriptPreviewHandler() codextea.TranscriptPreviewFunc {
	return func(threadID string, cwd string) ([]codextui.TranscriptPreviewLine, error) {
		threadID = strings.TrimSpace(threadID)
		if threadID == "" {
			return nil, errors.New("transcript preview requires a thread id")
		}
		thread, err := localThreadReadTurns(newSessionStore(), threadID)
		if err != nil {
			return nil, err
		}
		return transcriptPreviewFromTurns(thread.Turns, cwd), nil
	}
}

// transcriptPreviewFromTurns walks a thread's loaded turns newest-first,
// returning the preview in chronological order.
func transcriptPreviewFromTurns(turns []appserver.Turn, cwd string) []codextui.TranscriptPreviewLine {
	lines := make([]codextui.TranscriptPreviewLine, 0, transcriptPreviewMaxLines)
	for turnIndex := len(turns) - 1; turnIndex >= 0 && len(lines) < transcriptPreviewMaxLines; turnIndex-- {
		items := turns[turnIndex].Items
		for itemIndex := len(items) - 1; itemIndex >= 0 && len(lines) < transcriptPreviewMaxLines; itemIndex-- {
			appendTranscriptPreviewItem(&lines, items[itemIndex], cwd)
		}
	}
	reverseTranscriptPreviewLines(lines)
	return lines
}

// appendTranscriptPreviewItem maps one persisted item to its preview lines.
func appendTranscriptPreviewItem(lines *[]codextui.TranscriptPreviewLine, item appserver.ThreadItem, cwd string) {
	switch remoteTUINormalizedThreadItemType(item.Type) {
	case "usermessage":
		appendTranscriptPreviewText(lines, codextui.TranscriptPreviewUser, remoteTUIThreadItemUserText(item), cwd)
	case "agentmessage":
		appendTranscriptPreviewText(lines, codextui.TranscriptPreviewAssistant, strings.TrimSpace(item.Text), cwd)
	}
}

// appendTranscriptPreviewText appends a message's newest nonblank lines while
// preserving assistant display rewrites (Rust append_transcript_preview_text).
func appendTranscriptPreviewText(lines *[]codextui.TranscriptPreviewLine, speaker codextui.TranscriptPreviewSpeaker, text string, cwd string) {
	if speaker == codextui.TranscriptPreviewAssistant {
		text = transcriptPreviewAssistantMarkdown(text)
	}
	remaining := transcriptPreviewMaxLines - len(*lines)
	if remaining <= 0 {
		return
	}
	raw := strings.Split(strings.TrimRight(text, "\r\n"), "\n")
	added := 0
	for index := len(raw) - 1; index >= 0 && added < remaining; index-- {
		line := strings.TrimSpace(raw[index])
		if line == "" {
			continue
		}
		*lines = append(*lines, codextui.TranscriptPreviewLine{Speaker: speaker, Text: line})
		added++
	}
}

// transcriptPreviewAssistantMarkdown applies the assistant display rewrite
// (hidden markup removal + inline visualizations) the preview shows.
func transcriptPreviewAssistantMarkdown(text string) string {
	visible := eventmap.StripHiddenAssistantMarkup(text, false)
	rewritten, _ := codextui.RewriteInlineVisualizations(visible, nil)
	return rewritten
}

// reverseTranscriptPreviewLines turns the newest-first accumulation into
// chronological order (Rust lines.reverse()).
func reverseTranscriptPreviewLines(lines []codextui.TranscriptPreviewLine) {
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
}

// interactiveRemoteSessionTranscriptHandler loads a session's full transcript
// for the resume picker's ctrl+t overlay (Rust PickerLoadRequest::Transcript).
func interactiveRemoteSessionTranscriptHandler(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint, showRawReasoning bool) codextea.SessionTranscriptFunc {
	return func(threadID string) ([]codextui.Message, error) {
		threadID = strings.TrimSpace(threadID)
		if threadID == "" {
			return nil, errors.New("session transcript requires a thread id")
		}
		client, err := openRemoteSessionClient(ctx, endpoint)
		if err != nil {
			return nil, err
		}
		defer client.close()
		thread, err := remoteTUIReadThread(ctx, client, threadID, true)
		if err != nil {
			return nil, err
		}
		// The pager transcript projects the thread's cells (thread_transcript.rs),
		// so its reasoning entries follow that projection rather than the
		// in-session chatwidget block.
		return remoteTUIThreadMessagesFromThread(thread, reasoningProjectionThreadTranscript, showRawReasoning), nil
	}
}

// interactiveSessionTranscriptHandler loads the embedded session's transcript
// from the local store.
func interactiveSessionTranscriptHandler(showRawReasoning bool) codextea.SessionTranscriptFunc {
	return func(threadID string) ([]codextui.Message, error) {
		threadID = strings.TrimSpace(threadID)
		if threadID == "" {
			return nil, errors.New("session transcript requires a thread id")
		}
		record, err := newSessionStore().Read(session.ThreadID(threadID), true, true)
		if err != nil {
			return nil, err
		}
		return interactiveSessionMessagesFromRecord(record, reasoningProjectionThreadTranscript, showRawReasoning), nil
	}
}
