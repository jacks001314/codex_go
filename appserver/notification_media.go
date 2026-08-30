package appserver

import "strings"

// omitMediaNotificationMethods are the app-server notification methods that carry
// model-facing item payloads which may include inline image/audio content
// (Rust #41416 omit_app_server_notification_media).
var omitMediaNotificationMethods = map[NotificationMethod]bool{
	NotificationItemStarted:              true,
	NotificationItemCompleted:            true,
	NotificationRawResponseItemCompleted: true,
}

// isOmitMediaNotificationMethod reports whether a notification carries an item
// payload eligible for media filtering.
func isOmitMediaNotificationMethod(method NotificationMethod) bool {
	return omitMediaNotificationMethods[method]
}

// withoutNotificationMedia removes inline image/audio content from the params
// carried by an item/started, item/completed, or rawResponseItem/completed
// notification. The model request still receives the media; only the app-server
// notification is filtered (Rust #41416).
func withoutNotificationMedia(params any) any {
	switch typed := params.(type) {
	case *ItemStartedNotification:
		typed.Item = filterThreadItemPayload(typed.Item)
	case *ItemCompletedNotification:
		typed.Item = filterThreadItemPayload(typed.Item)
	case *RawResponseItemCompletedNotification:
		typed.Item = withoutNotificationItemMedia(typed.Item)
	}
	return params
}

func filterThreadItemPayload(payload ThreadItemPayload) ThreadItemPayload {
	if filtered, ok := withoutNotificationItemMedia(map[string]any(payload)).(map[string]any); ok {
		return ThreadItemPayload(filtered)
	}
	return payload
}

// withoutNotificationItemMedia recursively strips inline image/audio content
// from an item payload map.
func withoutNotificationItemMedia(item any) any {
	switch value := item.(type) {
	case map[string]any:
		itemType := strings.TrimSpace(stringFromAny(value["type"]))
		if isMediaContentItemType(itemType) {
			return nil
		}
		// Strip a top-level image/audio attachment.
		if _, ok := value["image_url"]; ok {
			delete(value, "image_url")
			delete(value, "imageUrl")
		}
		if _, ok := value["audio_url"]; ok {
			delete(value, "audio_url")
			delete(value, "audioUrl")
		}
		for key, child := range value {
			value[key] = withoutNotificationItemMedia(child)
		}
		return value
	case []any:
		filtered := make([]any, 0, len(value))
		for _, child := range value {
			if childMap, ok := child.(map[string]any); ok {
				if isMediaContentItemType(strings.TrimSpace(stringFromAny(childMap["type"]))) {
					continue
				}
			}
			filtered = append(filtered, withoutNotificationItemMedia(child))
		}
		return filtered
	default:
		return item
	}
}

func isMediaContentItemType(itemType string) bool {
	switch itemType {
	case "input_image", "inputImage", "image", "input_audio", "inputAudio", "audio":
		return true
	default:
		return false
	}
}
