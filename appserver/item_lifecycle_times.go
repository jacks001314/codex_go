package appserver

import "strings"

// withItemLifecycleTimestamps flattens an item notification's lifecycle times
// onto the item payload (Rust #57172). The payload keys are always present and
// serialize as null when the time is unknown.
//
// Rust records an item's start time in per-turn timing state when it publishes
// ItemStarted and reuses that exact value on ItemCompleted. Mirroring that here
// keeps the started/completed payloads consistent even when the completion
// payload is rebuilt from persisted metadata that carries no start time.
func (r *RuntimeRouter) withItemLifecycleTimestamps(method NotificationMethod, params any) any {
	switch method {
	case NotificationItemStarted:
		switch notification := params.(type) {
		case *ItemStartedNotification:
			if notification != nil {
				notification.Item = itemPayloadWithLifecycleTimes(notification.Item, notification.StartedAtMS, 0)
				r.rememberItemStartedTime(notification.ThreadID, notification.TurnID, notification.Item)
			}
		case ItemStartedNotification:
			notification.Item = itemPayloadWithLifecycleTimes(notification.Item, notification.StartedAtMS, 0)
			r.rememberItemStartedTime(notification.ThreadID, notification.TurnID, notification.Item)
			return notification
		}
	case NotificationItemCompleted:
		switch notification := params.(type) {
		case *ItemCompletedNotification:
			if notification != nil {
				notification.Item = r.itemPayloadForCompletedNotification(notification)
			}
		case ItemCompletedNotification:
			notification.Item = r.itemPayloadForCompletedNotification(&notification)
			return notification
		}
	case NotificationTurnCompleted:
		if notification, ok := params.(*TurnCompletedNotification); ok && notification != nil {
			r.clearItemStartedTimesForTurn(notification.ThreadID, notification.Turn.ID)
		}
	}
	return params
}

// itemPayloadForCompletedNotification resolves the start time the item was
// announced with. Rust falls back to the completion time when no start was
// recorded, so a live completion never carries a null start.
func (r *RuntimeRouter) itemPayloadForCompletedNotification(notification *ItemCompletedNotification) ThreadItemPayload {
	startedAtMS := r.takeItemStartedTime(notification.ThreadID, notification.TurnID, itemPayloadID(notification.Item))
	if startedAtMS == 0 {
		startedAtMS = itemLifecycleMilli(notification.Item, "startedAtMs")
	}
	return itemPayloadWithLifecycleTimes(notification.Item, startedAtMS, notification.CompletedAtMS)
}

// itemPayloadWithLifecycleTimes copies the payload and replaces its lifecycle
// keys. A zero notification time falls back to a time the payload already
// carries, so completing an item keeps the start time recorded at its start.
func itemPayloadWithLifecycleTimes(payload ThreadItemPayload, startedAtMS int64, completedAtMS int64) ThreadItemPayload {
	out := make(ThreadItemPayload, len(payload)+2)
	for key, value := range payload {
		if key == "startedAtMs" || key == "completedAtMs" {
			continue
		}
		out[key] = value
	}
	if startedAtMS == 0 {
		startedAtMS = itemLifecycleMilli(payload, "startedAtMs")
	}
	if completedAtMS == 0 {
		completedAtMS = itemLifecycleMilli(payload, "completedAtMs")
	}
	out["startedAtMs"] = nullableMilliseconds(startedAtMS)
	out["completedAtMs"] = nullableMilliseconds(completedAtMS)
	return out
}

func nullableMilliseconds(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

func itemLifecycleMilli(payload ThreadItemPayload, key string) int64 {
	switch typed := payload[key].(type) {
	case int:
		return int64(typed)
	case int32:
		return int64(typed)
	case int64:
		return typed
	case uint64:
		return int64(typed)
	case float64:
		return int64(typed)
	default:
		return 0
	}
}

func itemPayloadID(payload ThreadItemPayload) string {
	id, _ := payload["id"].(string)
	return strings.TrimSpace(id)
}

func itemLifecycleKey(threadID string, turnID string, itemID string) string {
	return strings.TrimSpace(threadID) + "\x00" + strings.TrimSpace(turnID) + "\x00" + strings.TrimSpace(itemID)
}

// rememberItemStartedTime records the start time published on ItemStarted so
// the matching ItemCompleted reports the same startedAtMs (Rust #57172).
func (r *RuntimeRouter) rememberItemStartedTime(threadID string, turnID string, payload ThreadItemPayload) {
	if r == nil {
		return
	}
	itemID := itemPayloadID(payload)
	startedAtMS := itemLifecycleMilli(payload, "startedAtMs")
	if itemID == "" || startedAtMS == 0 {
		return
	}
	key := itemLifecycleKey(threadID, turnID, itemID)
	r.itemStartedTimesMu.Lock()
	if r.itemStartedTimes == nil {
		r.itemStartedTimes = map[string]int64{}
	}
	r.itemStartedTimes[key] = startedAtMS
	r.itemStartedTimesMu.Unlock()
}

func (r *RuntimeRouter) takeItemStartedTime(threadID string, turnID string, itemID string) int64 {
	if r == nil || itemID == "" {
		return 0
	}
	key := itemLifecycleKey(threadID, turnID, itemID)
	r.itemStartedTimesMu.Lock()
	startedAtMS := r.itemStartedTimes[key]
	delete(r.itemStartedTimes, key)
	r.itemStartedTimesMu.Unlock()
	return startedAtMS
}

// clearItemStartedTimesForTurn drops any start times a turn never completed,
// matching Rust's per-turn timing state lifetime.
func (r *RuntimeRouter) clearItemStartedTimesForTurn(threadID string, turnID string) {
	if r == nil {
		return
	}
	prefix := itemLifecycleKey(threadID, turnID, "")
	r.itemStartedTimesMu.Lock()
	for key := range r.itemStartedTimes {
		if strings.HasPrefix(key, prefix) {
			delete(r.itemStartedTimes, key)
		}
	}
	r.itemStartedTimesMu.Unlock()
}
