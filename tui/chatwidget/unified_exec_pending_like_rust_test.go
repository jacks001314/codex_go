package chatwidget

import (
	"reflect"
	"testing"
)

// Rust parity: chatwidget::tests::exec_flow pushes progress redraws split across
// output deltas and expects /ps to show the resolved visible rows.
func TestUnifiedExecPendingRowsFeedPSLikeRust(t *testing.T) {
	var state CommandLifecycleState
	state.TrackUnifiedExecProcessBegin("child-call", "child-call", "sleep 200")
	for _, chunk := range []string{
		"starting\n\x1b]0;Building\x07Warning\n10%\r50%\r\x1b[",
		"K\x1b]0;Done\x1b",
		"\\100%\r",
		"\nDone\ncleared\r\x1b[K",
	} {
		state.TrackUnifiedExecOutputChunk("child-call", chunk, MaxRecentChunks)
	}
	want := []string{"Warning", "100%", "Done"}
	if got := state.UnifiedExecProcesses[0].RecentLines(); !reflect.DeepEqual(got, want) {
		t.Fatalf("recent lines = %#v, want %#v", got, want)
	}
}

func TestUnifiedExecPendingRowResetsOnProcessBeginLikeRust(t *testing.T) {
	var state CommandLifecycleState
	state.TrackUnifiedExecProcessBegin("call-1", "proc-1", "build")
	state.TrackUnifiedExecOutputChunk("call-1", "partial 10%", MaxRecentChunks)
	state.TrackUnifiedExecProcessBegin("call-1", "proc-1", "build")
	if lines := state.UnifiedExecProcesses[0].RecentLines(); len(lines) != 0 {
		t.Fatalf("recent lines after restart = %#v", lines)
	}
	state.TrackUnifiedExecOutputChunk("call-1", "20%", MaxRecentChunks)
	if got, want := state.UnifiedExecProcesses[0].RecentLines(), []string{"20%"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("recent lines = %#v, want %#v", got, want)
	}
}