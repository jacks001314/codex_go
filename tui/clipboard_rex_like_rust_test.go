package tui

import "testing"

// Rust #62931: Rex is recognized through the shared terminal detection, and a
// successful native copy still forwards over OSC 52 so a client that attaches
// after Codex started receives the text.
func TestClipboardRexDetectionAndForwardingLikeRust(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{name: "term program", env: map[string]string{"TERM_PROGRAM": "rex"}, want: true},
		{name: "term program case", env: map[string]string{"TERM_PROGRAM": "Rex"}, want: true},
		{name: "xterm term", env: map[string]string{"TERM": "xterm-rex"}, want: true},
		{name: "plain terminal", env: map[string]string{"TERM": "xterm-256color"}, want: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := ClipboardEnvironmentFromMap(testCase.env, false).RexSession; got != testCase.want {
				t.Fatalf("RexSession = %v, want %v", got, testCase.want)
			}
		})
	}

	if !ClipboardForwardingEnabled(ClipboardEnvironment{RexSession: true}) {
		t.Fatal("a Rex session must forward after a native copy")
	}
	if ClipboardForwardingEnabled(ClipboardEnvironment{}) {
		t.Fatal("a plain terminal must not forward after a native copy")
	}

	calls := clipboardCopyCalls{}
	environment := ClipboardEnvironmentFromMap(map[string]string{"TERM_PROGRAM": "rex"}, false)
	lease, err := CopyToClipboardWith("hello", environment, calls.tmuxOK, calls.oscOK, calls.nativeOK, calls.wslOK)
	if err != nil || lease == nil {
		t.Fatalf("rex copy = lease %#v err %v", lease, err)
	}
	if calls.native != 1 || calls.osc != 1 || calls.tmux != 0 || calls.wsl != 0 {
		t.Fatalf("calls = %#v, want one native copy plus one OSC 52 forward", calls)
	}
}
