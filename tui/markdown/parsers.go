package markdown

import (
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// goldmark.DefaultParser() returns a fresh parser, with its own parser tables
// and matchers, on every call. Rust builds its Markdown parser once and reuses
// it for the lifetime of the session; transcript layout re-renders the same
// message on every streaming frame, so the scan parsers are process-wide.
// goldmark keeps per-parse state on the parse context, so sharing them is safe
// for concurrent use.
var (
	defaultMarkdownParser = goldmark.DefaultParser()
	gfmMarkdownParser     = goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser()
)
