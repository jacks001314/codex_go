package cloudbackend

import (
	"encoding/json"
	"errors"

	"codex_go/appserver"
	"codex_go/cloudclient"
)

// forwardEvent turns one attached-thread event into app-server notifications,
// keeping the last observed status for later reads.
func (c *Client) forwardEvent(threadID string, event cloudclient.Event) {
	if status, ok := event.ThreadStatusFor(threadID); ok {
		c.status[threadID] = status
		return
	}
	switch event.Kind {
	case cloudclient.EventServerRequest:
		id := threadID
		c.enqueue(Notification{
			Method: appserver.NotificationWarning,
			Params: appserver.WarningNotification{
				ThreadID: &id,
				Message:  "This cloud turn requested an unsupported interaction. Open it in another client to continue.",
			},
		})
	case cloudclient.EventNotification:
		update, ok, err := cloudclient.DecodeTranscriptEvent(threadID, event.Payload)
		if err != nil {
			c.enqueue(cloudErrorNotification(threadID, "The cloud sent an unsupported transcript event."))
			return
		}
		if !ok {
			return
		}
		c.forwardTranscript(threadID, update)
	}
}

func (c *Client) forwardTranscript(threadID string, event cloudclient.TranscriptEvent) {
	update := event.Update
	switch update.Kind {
	case cloudclient.TranscriptTurnStarted, cloudclient.TranscriptTurnCompleted:
		turn, err := cloudTurn(update.Turn)
		if err != nil {
			c.enqueue(cloudErrorNotification(threadID, "The cloud sent an unsupported transcript event."))
			return
		}
		if update.Kind == cloudclient.TranscriptTurnStarted {
			c.enqueue(Notification{
				Method: appserver.NotificationTurnStarted,
				Params: appserver.TurnStartedNotification{ThreadID: threadID, Turn: turn},
			})
			return
		}
		c.enqueue(Notification{
			Method: appserver.NotificationTurnCompleted,
			Params: appserver.TurnCompletedNotification{ThreadID: threadID, Turn: turn},
		})
	case cloudclient.TranscriptAgentMessageDelta:
		c.enqueue(Notification{
			Method: appserver.NotificationAgentMessageDelta,
			Params: appserver.AgentMessageDeltaNotification{
				ThreadID: threadID,
				TurnID:   update.Delta.TurnID,
				ItemID:   update.Delta.ItemID,
				Delta:    update.Delta.Delta,
			},
		})
	case cloudclient.TranscriptItemStarted:
		payload, ok := itemSummaryPayload(update.Item.Item)
		if !ok {
			return
		}
		c.enqueue(Notification{
			Method: appserver.NotificationItemStarted,
			Params: appserver.ItemStartedNotification{
				Item:        payload,
				ThreadID:    threadID,
				TurnID:      update.Item.TurnID,
				StartedAtMS: update.Item.AtMS,
			},
		})
	case cloudclient.TranscriptItemCompleted:
		payload, ok := itemSummaryPayload(update.Item.Item)
		if !ok {
			return
		}
		c.enqueue(Notification{
			Method: appserver.NotificationItemCompleted,
			Params: appserver.ItemCompletedNotification{
				Item:          payload,
				ThreadID:      threadID,
				TurnID:        update.Item.TurnID,
				CompletedAtMS: update.Item.AtMS,
			},
		})
	}
}

// cloudTurn converts a cloud turn summary; unsupported statuses fail so the
// backend reports the event as unusable instead of inventing a state.
func cloudTurn(summary cloudclient.TurnSummary) (appserver.Turn, error) {
	var status appserver.TurnStatus
	switch summary.Status {
	case cloudclient.TurnCompleted:
		status = appserver.TurnStatusCompleted
	case cloudclient.TurnInterrupted:
		status = appserver.TurnStatusInterrupted
	case cloudclient.TurnFailed:
		status = appserver.TurnStatusFailed
	case cloudclient.TurnInProgress:
		status = appserver.TurnStatusInProgress
	default:
		return appserver.Turn{}, errors.New("The cloud returned an unsupported turn status.")
	}
	turn := appserver.Turn{
		ID:          summary.ID,
		ItemsView:   appserver.TurnItemsNotLoaded,
		Status:      status,
		StartedAt:   summary.StartedAt,
		CompletedAt: summary.CompletedAt,
		DurationMS:  summary.DurationMS,
	}
	if summary.Error != nil {
		turn.Error = &appserver.TurnError{
			Message:           summary.Error.Message,
			AdditionalDetails: summary.Error.AdditionalDetails,
		}
	}
	return turn, nil
}

// itemSummaryPayload renders the item types the cloud conversation announces.
// Other item kinds stay unreported.
func itemSummaryPayload(item cloudclient.ItemSummary) (appserver.ThreadItemPayload, bool) {
	threadItem, ok := threadItemFromSummary(item)
	if !ok {
		return nil, false
	}
	encoded, err := json.Marshal(&threadItem)
	if err != nil {
		return nil, false
	}
	payload := appserver.ThreadItemPayload{}
	if err := json.Unmarshal(encoded, &payload); err != nil {
		return nil, false
	}
	return payload, true
}

func threadItemFromSummary(item cloudclient.ItemSummary) (appserver.ThreadItem, bool) {
	var threadItem appserver.ThreadItem
	switch item.Kind {
	case cloudclient.ItemUserMessage:
		content := cloudUserContent(item.User)
		data := map[string]any{"content": content}
		if item.User.ClientID != nil {
			data["clientId"] = *item.User.ClientID
		}
		threadItem = appserver.ThreadItem{ID: item.User.ID, Type: "userMessage", Data: data}
	case cloudclient.ItemAgentMessage:
		data := map[string]any{}
		if item.Agent.HasQuestions {
			questions := make([]any, 0, len(item.Agent.Questions))
			for _, question := range item.Agent.Questions {
				entry := map[string]any{"title": question.Title, "options": nil}
				if question.Options != nil {
					entry["options"] = append([]string(nil), (*question.Options)...)
				}
				questions = append(questions, entry)
			}
			data["questions"] = questions
		}
		threadItem = appserver.ThreadItem{ID: item.Agent.ID, Type: "agentMessage", Text: item.Agent.Text, Data: data}
	default:
		return appserver.ThreadItem{}, false
	}
	return threadItem, true
}

// cloudUserContent renders the announced parts in order. A typed image part
// keeps its `{"type":"image"}` shape instead of being flattened into text; text
// parts carry an empty placeholder list, as before.
func cloudUserContent(user cloudclient.UserMessageSummary) []any {
	if len(user.Content) > 0 {
		content := make([]any, 0, len(user.Content))
		for _, part := range user.Content {
			if part.Kind == cloudclient.UserMessageImage {
				entry := map[string]any{"type": "image"}
				if part.Image.FileID != "" {
					entry["fileId"] = part.Image.FileID
				} else {
					entry["url"] = part.Image.URL
				}
				if part.Detail != nil {
					entry["detail"] = part.Detail.String()
				}
				content = append(content, entry)
				continue
			}
			content = append(content, map[string]any{
				"type":          "text",
				"text":          part.Text,
				"text_elements": []any{},
			})
		}
		return content
	}
	content := make([]any, 0, len(user.Text))
	for _, text := range user.Text {
		content = append(content, map[string]any{
			"type":          "text",
			"text":          text,
			"text_elements": []any{},
		})
	}
	return content
}

func cloudErrorNotification(threadID string, message string) Notification {
	return Notification{
		Method: appserver.NotificationError,
		Params: appserver.ErrorNotification{
			ThreadID: threadID,
			TurnID:   "",
			Error:    appserver.TurnError{Message: message},
		},
	}
}

func cloudStreamEndedNotification(threadID string) Notification {
	notification := cloudErrorNotification(threadID, "Cloud stream ended. Reopen the conversation to reconnect.")
	notification.streamDisconnect = true
	return notification
}
