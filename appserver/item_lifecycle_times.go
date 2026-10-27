package appserver

// withItemLifecycleTimestamps flattens an item notification's lifecycle times
// onto the item payload (Rust #57172). The payload keys are always present and
// serialize as null when the time is unknown.
func withItemLifecycleTimestamps(method NotificationMethod, params any) any {
	switch method {
	case NotificationItemStarted:
		switch notification := params.(type) {
		case *ItemStartedNotification:
			if notification != nil {
				notification.Item = itemPayloadWithLifecycleTimes(notification.Item, notification.StartedAtMS, 0)
			}
		case ItemStartedNotification:
			notification.Item = itemPayloadWithLifecycleTimes(notification.Item, notification.StartedAtMS, 0)
			return notification
		}
	case NotificationItemCompleted:
		switch notification := params.(type) {
		case *ItemCompletedNotification:
			if notification != nil {
				notification.Item = itemPayloadWithLifecycleTimes(notification.Item, 0, notification.CompletedAtMS)
			}
		case ItemCompletedNotification:
			notification.Item = itemPayloadWithLifecycleTimes(notification.Item, 0, notification.CompletedAtMS)
			return notification
		}
	}
	return params
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
