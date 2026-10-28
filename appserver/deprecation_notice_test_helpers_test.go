package appserver

import "strings"

// countDeprecationNoticesForConnection and deprecationNoticeForConnection keep
// the connection-scoped notice assertions shared by the paginated-history
// tests.
func countDeprecationNoticesForConnection(sink *targetedNotificationTestSink, connectionID string) int {
	if sink == nil {
		return 0
	}
	count := 0
	for _, record := range sink.List() {
		if record.notification == nil || record.notification.Method != NotificationDeprecationNotice {
			continue
		}
		if strings.TrimSpace(record.connectionID) == strings.TrimSpace(connectionID) {
			count++
		}
	}
	return count
}

func deprecationNoticeForConnection(sink *targetedNotificationTestSink, connectionID string) *DeprecationNoticeNotification {
	if sink == nil {
		return nil
	}
	for _, record := range sink.List() {
		if record.notification == nil || record.notification.Method != NotificationDeprecationNotice {
			continue
		}
		if strings.TrimSpace(record.connectionID) != strings.TrimSpace(connectionID) {
			continue
		}
		if params, ok := record.notification.Params.(*DeprecationNoticeNotification); ok {
			return params
		}
	}
	return nil
}
