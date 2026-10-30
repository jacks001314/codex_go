package execpolicy

import "testing"

// TestExecutableLookupKeyUsesTargetPlatformLikeRust mirrors Rust #42113's
// platform-aware executable-name normalization.
func TestExecutableLookupKeyUsesTargetPlatformLikeRust(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		platform DangerousCommandPlatform
		want     string
	}{
		{name: "windows drive path", raw: `C:\Tools\Git.EXE`, platform: DangerousCommandPlatformWindows, want: "git"},
		{name: "windows forward slash", raw: `C:/Tools/PowerShell.exe`, platform: DangerousCommandPlatformWindows, want: "powershell"},
		{name: "windows drive relative", raw: `C:foo.CMD`, platform: DangerousCommandPlatformWindows, want: "foo"},
		{name: "windows unc path", raw: `\\server\share\rm.com`, platform: DangerousCommandPlatformWindows, want: "rm"},
		{name: "windows trailing separator", raw: `C:\Tools\`, platform: DangerousCommandPlatformWindows, want: ""},
		{name: "posix keeps case and suffix", raw: `/usr/bin/Git.EXE`, platform: DangerousCommandPlatformPosix, want: "Git.EXE"},
		{name: "posix does not split backslash", raw: `dir\Git.EXE`, platform: DangerousCommandPlatformPosix, want: `dir\Git.EXE`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := executableLookupKeyForPlatform(test.raw, test.platform); got != test.want {
				t.Fatalf("executableLookupKeyForPlatform(%q, %s) = %q, want %q", test.raw, test.platform, got, test.want)
			}
		})
	}
}
