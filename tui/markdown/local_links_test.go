package markdown

import (
	"strings"
	"testing"

	"codex_go/utils"
)

func TestIsLocalPathLikeLink(t *testing.T) {
	local := []string{
		"file:///home/user/x",
		"/abs/path",
		"~/home/path",
		"./rel/path",
		"../up/path",
		`\\server\share`,
		"C:/Users/x",
		"c:\\Users\\x",
		"//server/share",
	}
	for _, d := range local {
		if !isLocalPathLikeLink(d) {
			t.Fatalf("expected %q to be a local path link", d)
		}
	}
	web := []string{
		"https://example.com/x",
		"http://example.com",
		"mailto:a@b.com",
		"src/main.rs",
	}
	for _, d := range web {
		if isLocalPathLikeLink(d) {
			t.Fatalf("expected %q NOT to be a local path link", d)
		}
	}
}

func TestNormalizeHashLocationSuffix(t *testing.T) {
	cases := map[string]string{
		"#L10":         ":10",
		"#L10C3":       ":10:3",
		"#L10-L14":     ":10-14",
		"#L10C3-L14C8": ":10:3-14:8",
	}
	for in, want := range cases {
		got, ok := normalizeHashLocationSuffix(in)
		if !ok || got != want {
			t.Fatalf("normalizeHashLocationSuffix(%q) = %q,%v want %q", in, got, ok, want)
		}
	}
	if _, ok := normalizeHashLocationSuffix("#section"); ok {
		t.Fatalf("non-location fragment should not normalize")
	}
}

func TestRenderLocalLinkTarget(t *testing.T) {
	cases := []struct {
		dest string
		cwd  string
		want string
	}{
		{"./src/main.rs", "", "./src/main.rs"},
		{"../up/foo.rs", "", "../up/foo.rs"},
		{"/qax/dev/codex_go/src/main.rs", "/qax/dev/codex_go", "src/main.rs"},
		{"/qax/dev/codex_go/src/main.rs", "", "/qax/dev/codex_go/src/main.rs"},
		{"/etc/hosts", "/qax/dev/codex_go", "/etc/hosts"},
		{"./src/main.rs#L10", "", "./src/main.rs:10"},
		{"./src/main.rs#L10C3", "", "./src/main.rs:10:3"},
	}
	for _, c := range cases {
		got, ok := renderLocalLinkTarget(c.dest, c.cwd)
		if !ok {
			t.Fatalf("renderLocalLinkTarget(%q,%q) not ok", c.dest, c.cwd)
		}
		if got != c.want {
			t.Fatalf("renderLocalLinkTarget(%q,%q) = %q want %q", c.dest, c.cwd, got, c.want)
		}
	}
}

func TestRenderWithThemeCwdLocalFileLinks(t *testing.T) {
	source := "See [main](./src/main.rs) and [server](/qax/dev/codex_go/src/server.rs) for details."
	rendered, err := RenderWithThemeCwd(source, 80, "", "/qax/dev/codex_go")
	if err != nil {
		t.Fatal(err)
	}
	clean := utils.StripANSI(rendered)
	for _, want := range []string{"./src/main.rs", "src/server.rs"} {
		if !strings.Contains(clean, want) {
			t.Fatalf("local link display missing %q:\n%s", want, clean)
		}
	}
	if strings.Contains(clean, "[main]") || strings.Contains(clean, "[server]") {
		t.Fatalf("local link label leaked into output:\n%s", clean)
	}
}

func TestRenderWithThemeCwdLocalFileLinksInTable(t *testing.T) {
	source := "| File |\n|---|\n| [a](./src/a.rs) |\n| [b](/qax/dev/codex_go/src/b.go) |"
	rendered, err := RenderWithThemeCwd(source, 60, "", "/qax/dev/codex_go")
	if err != nil {
		t.Fatal(err)
	}
	clean := utils.StripANSI(rendered)
	for _, want := range []string{"./src/a.rs", "src/b.go"} {
		if !strings.Contains(clean, want) {
			t.Fatalf("table cell local link display missing %q:\n%s", want, clean)
		}
	}
	if strings.Contains(clean, "[a]") || strings.Contains(clean, "[b]") {
		t.Fatalf("table cell local link label leaked:\n%s", clean)
	}
}

func TestRewriteLocalFileLinksSkipsCodeBlocksAndImages(t *testing.T) {
	source := "```\n[code](./src/x.go)\n```\n\n![img](./img.png)\n\n[link](./real.go)"
	got := rewriteLocalFileLinks(source, "")
	clean := utils.StripANSI(got)
	// Code block link must remain untouched.
	if !strings.Contains(clean, "[code](./src/x.go)") {
		t.Fatalf("code block link was rewritten:\n%s", clean)
	}
	// Image syntax must remain untouched.
	if !strings.Contains(clean, "![img](./img.png)") {
		t.Fatalf("image link was rewritten:\n%s", clean)
	}
	if strings.Contains(clean, "![real.go]") {
		t.Fatalf("image alt text was not preserved:\n%s", clean)
	}
	if !strings.Contains(clean, "`./real.go`") {
		t.Fatalf("plain link label not rewritten to path:\n%s", clean)
	}
}

func TestRewriteLocalFileLinksLeavesWebLinks(t *testing.T) {
	source := "Visit [site](https://example.com/x) please."
	got := rewriteLocalFileLinks(source, "")
	if !strings.Contains(got, "[site](https://example.com/x)") {
		t.Fatalf("web link was rewritten:\n%s", got)
	}
}

func TestRewriteLocalFileLinksSkipsInlineAndIndentedCode(t *testing.T) {
	source := "Use `[inline](./x.go)` here.\n\n    [indented](./y.go)"
	got := rewriteLocalFileLinks(source, "")
	if !strings.Contains(got, "`[inline](./x.go)`") {
		t.Fatalf("inline code link was rewritten:\n%s", got)
	}
	// Indented code block must keep the link text unchanged.
	if !strings.Contains(got, "[indented](./y.go)") {
		t.Fatalf("indented code link was rewritten:\n%s", got)
	}
}
