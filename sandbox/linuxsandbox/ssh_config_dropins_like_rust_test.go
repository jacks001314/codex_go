//go:build linux

package linuxsandbox

import "testing"

// TestShouldMaskSSHConfigDropInsLikeRust pins Rust #61947: filesystem-restricted
// commands inherit an empty, read-only /etc/ssh/ssh_config.d unless the policy
// denies it, writes through it, or explicitly grants a path inside it.
func TestShouldMaskSSHConfigDropInsLikeRust(t *testing.T) {
	rootRead := &linuxFilesystemPolicy{Kind: "restricted", Entries: []linuxFilesystemEntry{
		{Access: "read", Special: linuxSpecialPath{Kind: "root"}},
	}}
	platformDefaults := &linuxFilesystemPolicy{Kind: "restricted", Entries: []linuxFilesystemEntry{
		{Access: "read", Special: linuxSpecialPath{Kind: "minimal"}},
	}}
	etcRead := &linuxFilesystemPolicy{Kind: "restricted", Entries: []linuxFilesystemEntry{
		{Access: "read", Path: "/etc"},
	}}
	denied := &linuxFilesystemPolicy{Kind: "restricted", Entries: []linuxFilesystemEntry{
		{Access: "deny", Path: "/etc"},
	}}
	readInsideDropIns := &linuxFilesystemPolicy{Kind: "restricted", Entries: []linuxFilesystemEntry{
		{Access: "read", Special: linuxSpecialPath{Kind: "root"}},
		{Access: "read", Path: "/etc/ssh/ssh_config.d/verbatim.conf"},
	}}
	readGlobInsideDropIns := &linuxFilesystemPolicy{Kind: "restricted", Entries: []linuxFilesystemEntry{
		{Access: "read", Special: linuxSpecialPath{Kind: "root"}},
		{Access: "read", Pattern: "/etc/ssh/ssh_config.d/*.conf"},
	}}
	writeInsideDropIns := &linuxFilesystemPolicy{Kind: "restricted", Entries: []linuxFilesystemEntry{
		{Access: "read", Special: linuxSpecialPath{Kind: "root"}},
		{Access: "write", Path: "/etc/ssh/ssh_config.d/private"},
	}}

	safe := func() bool { return true }
	tests := []struct {
		name            string
		policy          *linuxFilesystemPolicy
		unreadableRoots []string
		writableRoots   []string
		ancestorsSafe   func() bool
		want            bool
	}{
		{name: "root read masks", policy: rootRead, ancestorsSafe: safe, want: true},
		{name: "platform defaults mask", policy: platformDefaults, ancestorsSafe: safe, want: true},
		{name: "readable ancestor masks", policy: etcRead, ancestorsSafe: safe, want: true},
		{name: "no inherited read does not mask", policy: &linuxFilesystemPolicy{Kind: "restricted"}, ancestorsSafe: safe, want: false},
		{name: "denied ancestor does not mask", policy: rootRead, unreadableRoots: []string{"/etc"}, ancestorsSafe: safe, want: false},
		{name: "deny-only policy does not inherit", policy: denied, ancestorsSafe: safe, want: false},
		{name: "writable ancestor does not mask", policy: rootRead, writableRoots: []string{"/etc"}, ancestorsSafe: safe, want: false},
		{name: "writable descendant does not mask", policy: writeInsideDropIns, writableRoots: []string{"/etc/ssh/ssh_config.d/private"}, ancestorsSafe: safe, want: false},
		{name: "granted file inside drop-ins does not mask", policy: readInsideDropIns, ancestorsSafe: safe, want: false},
		{name: "granted glob inside drop-ins does not mask", policy: readGlobInsideDropIns, ancestorsSafe: safe, want: false},
		{name: "unsafe ancestors do not mask", policy: rootRead, ancestorsSafe: func() bool { return false }, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sshConfigDropInsMaskDecision(tt.policy, "/repo", tt.unreadableRoots, tt.writableRoots, tt.ancestorsSafe)
			if got != tt.want {
				t.Fatalf("sshConfigDropInsMaskDecision() = %v, want %v", got, tt.want)
			}
		})
	}
}
