package shell

import "testing"

func TestWinPathToWSL(t *testing.T) {
	cases := map[string]string{
		`C:\Temp\codex.zip`: "/mnt/c/Temp/codex.zip",
		"D:/Work/codex.tgz": "/mnt/d/Work/codex.tgz",
		"E:\\":              "/mnt/e",
	}
	for input, want := range cases {
		got, ok := WinPathToWSL(input)
		if !ok || got != want {
			t.Fatalf("WinPathToWSL(%q) = %q %v, want %q true", input, got, ok, want)
		}
	}
	if _, ok := WinPathToWSL("/home/user/codex"); ok {
		t.Fatalf("unix path should not map")
	}
}
