package app

import (
	"context"
	"errors"
	"strings"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	codextea "codex_go/tui/tea"
)

const (
	transcriptExportNoConversation = "No active conversation to export."
	transcriptExportNoContent      = "No conversation content to export."
)

// interactiveRemoteTranscriptExportHandler renders the active conversation as
// Markdown for /export against the app server (Rust transcript_export.rs).
func interactiveRemoteTranscriptExportHandler(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) codextea.TranscriptExportFunc {
	return func(threadID string) (string, error) {
		threadID = strings.TrimSpace(threadID)
		if threadID == "" {
			return "", errors.New(transcriptExportNoConversation)
		}
		client, err := openRemoteSessionClient(ctx, endpoint)
		if err != nil {
			return "", err
		}
		defer client.close()
		thread, err := remoteTUIReadThread(ctx, client, threadID, true)
		if err != nil {
			return "", err
		}
		markdown := transcriptExportMarkdown(thread.Turns)
		if markdown == "" {
			return "", errors.New(transcriptExportNoContent)
		}
		return markdown, nil
	}
}

// interactiveTranscriptExportHandler renders the embedded session's transcript
// through the in-process app-server router.
func interactiveTranscriptExportHandler() codextea.TranscriptExportFunc {
	return func(threadID string) (string, error) {
		threadID = strings.TrimSpace(threadID)
		if threadID == "" {
			return "", errors.New(transcriptExportNoConversation)
		}
		thread, err := localThreadReadTurns(newSessionStore(), threadID)
		if err != nil {
			return "", err
		}
		markdown := transcriptExportMarkdown(thread.Turns)
		if markdown == "" {
			return "", errors.New(transcriptExportNoContent)
		}
		return markdown, nil
	}
}

// transcriptExportMarkdown renders the persisted turns as the complete Markdown
// transcript Rust produces: a header plus User/Assistant/Plan/Reasoning sections
// and indented Activity lines, skipping hidden review prompts and the review
// markers themselves (transcript_export.rs render_markdown_transcript /
// visible_export_items). It returns "" when there is nothing to export.
func transcriptExportMarkdown(turns []appserver.Turn) string {
	var builder strings.Builder
	builder.WriteString("# Codex conversation\n")
	reviewMode := false
	for _, turn := range turns {
		for _, item := range turn.Items {
			switch remoteTUINormalizedThreadItemType(item.Type) {
			case "enteredreviewmode":
				reviewMode = true
				continue
			case "exitedreviewmode":
				reviewMode = false
				continue
			}
			if reviewMode || remoteTUIThreadItemIsReviewUserMessage(item) {
				continue
			}
			heading, text, indent, ok := transcriptExportSection(item)
			if !ok || strings.TrimSpace(text) == "" {
				continue
			}
			builder.WriteString("\n## ")
			builder.WriteString(heading)
			builder.WriteString("\n\n")
			for _, line := range strings.Split(strings.TrimRight(text, "\r\n"), "\n") {
				if indent {
					builder.WriteString("    ")
				}
				builder.WriteString(line)
				builder.WriteByte('\n')
			}
		}
	}
	if builder.String() == "# Codex conversation\n" {
		return ""
	}
	return builder.String()
}

// transcriptExportSection maps one persisted item to its Markdown heading and
// body, mirroring Rust's per-cell heading choice.
func transcriptExportSection(item appserver.ThreadItem) (heading string, text string, indent bool, ok bool) {
	switch remoteTUINormalizedThreadItemType(item.Type) {
	case "usermessage":
		return "User", remoteTUIThreadItemUserText(item), false, true
	case "agentmessage", "assistantmessage":
		return "Assistant", strings.TrimSpace(item.Text), false, true
	case "plan":
		return "Plan", strings.TrimSpace(item.Text), false, true
	case "reasoning":
		return "Reasoning", remoteTUIThreadItemReasoningText(item), false, true
	default:
		text := strings.TrimSpace(item.Text)
		if text == "" {
			text = remoteTUIThreadItemToolText(item)
		}
		return "Activity", text, true, true
	}
}
