package appserver

import "testing"

// The runtime router forwards only methods isThreadMethod recognizes; a thread
// method missing from that list is answered as "unknown app-server method" even
// though the thread router implements it.
func TestIsThreadMethodCoversThreadReadStateAndSearchOccurrences(t *testing.T) {
	for _, method := range []Method{MethodThreadReadStateUpdate, MethodThreadSearchOccurrences} {
		if !isThreadMethod(method) {
			t.Fatalf("isThreadMethod(%q) = false", method)
		}
	}
}
