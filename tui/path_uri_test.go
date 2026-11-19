package tui

import "testing"

// Rust PathUri::basename (#62375): the terminal-title and status-line project
// name come from the last path segment, whichever path convention the app
// server used.
func TestPathURIBasenameKeepsServerPathConventionLikeRust(t *testing.T) {
	for _, test := range []struct {
		name   string
		path   string
		want   string
		wantOK bool
	}{
		{name: "posix", path: "/home/me/repo", want: "repo", wantOK: true},
		{name: "posix trailing separator", path: "/home/me/repo/", want: "repo", wantOK: true},
		{name: "windows", path: `C:\Users\me\repo`, want: "repo", wantOK: true},
		{name: "windows trailing separator", path: `C:\Users\me\repo\`, want: "repo", wantOK: true},
		{name: "unc share", path: `\\server\share`, want: "share", wantOK: true},
		{name: "drive root keeps the drive", path: `C:\`, want: "C:", wantOK: true},
		{name: "posix root has no basename", path: "/", wantOK: false},
		{name: "empty", path: "   ", wantOK: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, ok := PathURIBasename(test.path)
			if ok != test.wantOK || got != test.want {
				t.Fatalf("PathURIBasename(%q) = %q/%v, want %q/%v", test.path, got, ok, test.want, test.wantOK)
			}
		})
	}
}
