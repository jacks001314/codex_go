package utils

import "testing"

func TestResolveConfigPathUsesSuppliedConventionBaseAndHome(t *testing.T) {
	tests := []struct {
		name       string
		convention PathConvention
		base       string
		home       string
		input      string
		want       string
	}{
		{
			name:       "posix home",
			convention: ConventionPosix,
			base:       "file:///work/project",
			home:       "file:///home/user",
			input:      "~/a b/雪%/",
			want:       "/home/user/a b/雪%",
		},
		{
			name:       "windows relative",
			convention: ConventionWindows,
			base:       "file:///C:/work/project",
			home:       "file:///C:/Users/user",
			input:      `..\private\`,
			want:       `C:\work\private`,
		},
		{
			name:       "windows drive-relative",
			convention: ConventionWindows,
			base:       "file:///C:/work/project",
			home:       "file:///C:/Users/user",
			input:      `C:private\*.key`,
			want:       `C:\work\project\private\*.key`,
		},
		{
			name:       "windows unc root",
			convention: ConventionWindows,
			base:       "file:///C:/work",
			home:       "file:///C:/Users/user",
			input:      `\\server\share`,
			want:       `\\server\share\`,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			base, err := Parse(testCase.base)
			if err != nil {
				t.Fatalf("Parse(base) error = %v", err)
			}
			home, err := Parse(testCase.home)
			if err != nil {
				t.Fatalf("Parse(home) error = %v", err)
			}
			got, err := ResolveConfigPath(testCase.input, testCase.convention, base, home)
			if err != nil {
				t.Fatalf("ResolveConfigPath(%q) error = %v", testCase.input, err)
			}
			if got != testCase.want {
				t.Fatalf("ResolveConfigPath(%q) = %q, want %q", testCase.input, got, testCase.want)
			}
		})
	}
}

func TestResolveConfigPathRejectsAmbiguousFacts(t *testing.T) {
	posixBase, err := Parse("file:///base")
	if err != nil {
		t.Fatalf("Parse(base) error = %v", err)
	}
	windowsBase, err := Parse("file:///C:/base")
	if err != nil {
		t.Fatalf("Parse(windows base) error = %v", err)
	}
	tests := []struct {
		name       string
		input      string
		convention PathConvention
		base       *PathURI
		home       *PathURI
	}{
		{name: "nul", input: "nul\x00", convention: ConventionPosix, base: posixBase},
		{name: "missing home", input: "~/private", convention: ConventionPosix, base: posixBase},
		{name: "bare drive", input: "C:", convention: ConventionWindows, base: windowsBase},
		{name: "mixed home separators", input: `~/\private`, convention: ConventionWindows, base: windowsBase},
		{name: "stream syntax", input: `C:\private\file:stream`, convention: ConventionWindows, base: windowsBase},
		{name: "device namespace", input: `\\.\COM1`, convention: ConventionWindows, base: windowsBase},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := ResolveConfigPath(testCase.input, testCase.convention, testCase.base, testCase.home); err == nil {
				t.Fatalf("ResolveConfigPath(%q) succeeded, want an error", testCase.input)
			}
		})
	}
}

func TestResolveConfigPathRejectsGlobMetacharactersInUsedFacts(t *testing.T) {
	base, err := Parse("file:///C:/work/sam[1]")
	if err != nil {
		t.Fatalf("Parse(base) error = %v", err)
	}
	for _, input := range []string{"private/*.key", "~/private/*.key"} {
		if _, err := ResolveConfigPath(input, ConventionWindows, base, base); err == nil {
			t.Fatalf("ResolveConfigPath(%q) succeeded with an unsafe base, want an error", input)
		}
	}
	if got, err := ResolveConfigPath(`C:\private\*.key`, ConventionWindows, base, base); err != nil {
		t.Fatalf("absolute glob rejected by an unused base: %v", err)
	} else if got != `C:\private\*.key` {
		t.Fatalf("absolute glob = %q", got)
	}
}

func TestValidateGlobDirectoryRejectsPatternSyntax(t *testing.T) {
	for _, raw := range []string{"file:///home/sam[1]", "file:///C:/Users/sam%5B1%5D"} {
		path, err := Parse(raw)
		if err != nil {
			t.Fatalf("Parse(%q) error = %v", raw, err)
		}
		convention, _ := path.InferConvention()
		if err := path.ValidateGlobDirectory(convention); err == nil {
			t.Fatalf("ValidateGlobDirectory(%q) succeeded, want an error", raw)
		}
	}
	path, err := Parse("file:///home/sam%5Cname")
	if err != nil {
		t.Fatalf("Parse(posix backslash) error = %v", err)
	}
	if err := path.ValidateGlobDirectory(ConventionPosix); err == nil {
		t.Fatal("ValidateGlobDirectory accepted a POSIX backslash")
	}
}
