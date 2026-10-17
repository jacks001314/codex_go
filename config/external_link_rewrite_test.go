package config

import "testing"

func TestExternalRewritePreservesLinksLikeRust(t *testing.T) {
	content := "Claude: https://Claude.example/CLAUDE.md?product=Claude#Claude\n" +
		"[Claude](HTTPS://Claude.example/a_(Claude)/claude.md \"Claude\")Claude\n" +
		"<http://Claude.example/CLAUDE.md>，Claude\n" +
		"`file:///Claude/CLAUDE.md` Claude://Claude/CLAUDE.md\n" +
		"[Claude](mailto:Claude@example.com) [CLAUDE.md](./CLAUDE.md)\n" +
		"WWW.Claude.example/CLAUDE.md Claude\n" +
		"https://Claude.example/it's/CLAUDE.md Claude\n" +
		"<Claude@example.com> Claude@example.com support@Claude.example Claude\n" +
		"[Claude](/Claude/reference) [CLAUDE.md](./Claude/CLAUDE.md)\n" +
		"CLAUDE.md?section=details\n" +
		"[Claude](#Claude)\n" +
		"![Claude](https://example.com/Claude/CLAUDE.md \"Claude\")\n" +
		"[![logo][CLAUDE.md logo]](https://Claude.example/Claude)\n" +
		"[CLAUDE.md logo]: ./logo.svg\n" +
		"[Claude]: https://example.com/Claude/CLAUDE.md \"Claude\"\n"
	want := "Codex: https://Claude.example/CLAUDE.md?product=Claude#Claude\n" +
		"[Codex](HTTPS://Claude.example/a_(Claude)/claude.md \"Codex\")Codex\n" +
		"<http://Claude.example/CLAUDE.md>，Codex\n" +
		"`file:///Claude/CLAUDE.md` Claude://Claude/CLAUDE.md\n" +
		"[Codex](mailto:Claude@example.com) [AGENTS.md](./AGENTS.md)\n" +
		"WWW.Claude.example/CLAUDE.md Codex\n" +
		"https://Claude.example/it's/CLAUDE.md Codex\n" +
		"<Claude@example.com> Claude@example.com support@Claude.example Codex\n" +
		"[Codex](/Codex/reference) [AGENTS.md](./Codex/AGENTS.md)\n" +
		"AGENTS.md?section=details\n" +
		"[Codex](#Codex)\n" +
		"![Codex](https://example.com/Claude/CLAUDE.md \"Codex\")\n" +
		"[![logo][AGENTS.md logo]](https://Claude.example/Claude)\n" +
		"[AGENTS.md logo]: ./logo.svg\n" +
		"[Codex]: https://example.com/Claude/CLAUDE.md \"Codex\"\n"
	if got := rewriteClaudeTerms(content); got != want {
		t.Fatalf("rewriteClaudeTerms() =\n%s\nwant\n%s", got, want)
	}
}

func TestExternalRewritePreservesCursorLinks(t *testing.T) {
	content := "Cursor [Cursor](https://cursor.com/.cursorrules) [.cursorrules](./.cursorrules)"
	want := "Codex [Codex](https://cursor.com/.cursorrules) [AGENTS.md](./AGENTS.md)"
	if got := rewriteCursorTerms(content); got != want {
		t.Fatalf("rewriteCursorTerms() = %q, want %q", got, want)
	}
}
