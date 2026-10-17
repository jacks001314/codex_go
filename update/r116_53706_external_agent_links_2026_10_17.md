# Round 116 · #53706 external agent link preservation

Rust source: `cb7091b180`.

## Landed

- External-agent imports now rewrite source-agent terminology only outside
  recognized URLs and email addresses.
- Scheme URLs, `www.` links, bare domain/path links, `mailto:` destinations,
  and email addresses are copied verbatim. Markdown labels, titles, fragments,
  and reference definitions still migrate normally.
- Source instruction filename references continue to migrate to `AGENTS.md`,
  including relative paths and `?query` / `:line` / `:line:column` suffixes.
- The same rewrite layer covers Claude and Cursor imports.
- Added regression coverage for URLs with balanced parentheses and apostrophes,
  file/application schemes, Unicode surrounding text, Markdown images/reference
  links, relative paths, email addresses, and Cursor `.cursorrules` links.

## Verification

```text
go test ./config -run ExternalRewrite -count=1  # ok
go test ./config -count=1                       # ok
go vet ./config                                 # ok
go build ./...                                  # ok
```
