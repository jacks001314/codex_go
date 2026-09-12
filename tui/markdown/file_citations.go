package markdown

import (
	"path/filepath"
	"strings"

	codextui "codex_go/tui"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	gmtext "github.com/yuin/goldmark/text"
)

// Rust parity: codex-rs/tui/src/markdown_render/file_citations.rs (#42650).
//
// Assistant messages may cite files with a structured
// `:codex-file-citation{path="..."}` directive instead of an ordinary markdown
// link. The citation path is literal: markdown-significant characters,
// unicode, Windows separators, and location suffixes must survive rendering,
// and code blocks, inline code, HTML, existing links/images, escaped text, and
// generic markdown must keep the directive text untouched.

const fileCitationName = "codex-file-citation"

type fileCitationSpan struct {
	start   int
	end     int
	display string
	dest    string
}

// rewriteFileCitations converts complete file-citation directives into the
// backticked local-path display used by ordinary local file links and returns
// the link annotations needed for OSC-8 hyperlinks. Incomplete or malformed
// directives stay literal.
func rewriteFileCitations(source string, cwd string) (string, []localFileLink) {
	if !strings.Contains(source, fileCitationName) {
		return source, nil
	}
	data := []byte(source)
	document := goldmark.DefaultParser().Parse(gmtext.NewReader(data))
	literalRanges := collectCitationLiteralRanges(document)
	spans := findFileCitationSpans(source, literalRanges, cwd)
	if len(spans) == 0 {
		return source, nil
	}

	var out strings.Builder
	links := make([]localFileLink, 0, len(spans))
	cursor := 0
	for _, span := range spans {
		if span.start < cursor {
			continue
		}
		out.WriteString(source[cursor:span.start])
		out.WriteString("`")
		out.WriteString(span.display)
		out.WriteString("`")
		cursor = span.end
		links = append(links, localFileLink{Display: span.display, Dest: span.dest})
	}
	out.WriteString(source[cursor:])
	return out.String(), links
}

func findFileCitationSpans(source string, literalRanges [][2]int, cwd string) []fileCitationSpan {
	var spans []fileCitationSpan
	scanBudget := len(source)*4 + 1
	skipUntil := 0
	for start := 0; start < len(source); start++ {
		if start < skipUntil {
			continue
		}
		if source[start] != ':' {
			continue
		}
		if start > 0 && (source[start-1] == ':' || escapedFileCitationByte(source, start-1)) {
			continue
		}
		if inCodeRange(start, literalRanges) {
			continue
		}
		directive, ok := parseFileCitationDirective(source, start, &scanBudget)
		if !ok {
			continue
		}
		if directive.end > skipUntil {
			skipUntil = directive.end
		}
		if directive.name != fileCitationName {
			continue
		}
		path, ok := directive.attributes["path"]
		if !ok || strings.TrimSpace(path) == "" {
			continue
		}
		dest, display, ok := citationDestinationAndDisplay(path, cwd)
		if !ok || display == "" {
			continue
		}
		span := fileCitationSpan{
			start:   start,
			end:     directive.end,
			display: display,
			dest:    dest,
		}
		spans = append(spans, span)
		skipUntil = directive.end
		start = directive.end - 1
	}
	return spans
}

// escapedFileCitationByte reports whether the byte at index is the last byte of
// an odd-length backslash run, which makes the following directive literal.
func escapedFileCitationByte(source string, index int) bool {
	count := 0
	for index >= 0 && source[index] == '\\' {
		count++
		index--
	}
	return count%2 == 1
}

type fileCitationDirective struct {
	name       string
	end        int
	attributes map[string]string
}

// parseFileCitationDirective parses `:{1,3}name{attr="value" ...}` at start.
// Citations prefer literal quoting (backslash is data) with backslash escaping
// as the fallback, matching the Rust scanner order for file citations.
func parseFileCitationDirective(source string, start int, budget *int) (fileCitationDirective, bool) {
	if start < 0 || start > len(source) {
		return fileCitationDirective{}, false
	}
	sub := source[start:]
	// Rust's scanner tries literal quoting first for file citations (backslash
	// is data) and escaped quoting first for other directives, sharing one scan
	// budget across offsets and modes.
	modes := []codextui.QuoteEscaping{codextui.QuoteEscapingLiteral, codextui.QuoteEscapingBackslash}
	if !strings.HasPrefix(strings.TrimLeft(sub, ":"), fileCitationName+"{") {
		modes = []codextui.QuoteEscaping{codextui.QuoteEscapingBackslash, codextui.QuoteEscapingLiteral}
	}
	for _, mode := range modes {
		directive, ok := codextui.ParseAssistantDirectiveWithBudget(sub, mode, budget)
		if !ok {
			continue
		}
		return fileCitationDirective{
			name:       directive.Name,
			end:        start + len(directive.Raw),
			attributes: directive.Attributes,
		}, true
	}
	return fileCitationDirective{}, false
}

// citationDestinationAndDisplay joins relative citation paths against cwd and
// splits location suffixes so citation paths stay literal while the ordinary
// local-link display rules still shorten and hyperlink them.
func citationDestinationAndDisplay(path string, cwd string) (string, string, bool) {
	pathText := path
	locationSuffix := ""
	if fragmentIndex := strings.LastIndex(pathText, "#"); fragmentIndex >= 0 {
		if normalized, ok := normalizeHashLocationSuffix(pathText[fragmentIndex:]); ok {
			pathText = pathText[:fragmentIndex]
			locationSuffix = normalized
		}
	}
	if locationSuffix == "" {
		if suffix, ok := extractColonLocationSuffix(pathText); ok {
			pathText = pathText[:len(pathText)-len(suffix)]
			locationSuffix = suffix
		}
	}
	if !isLocalPathLikeLink(pathText) {
		if cwd == "" {
			pathText = "./" + pathText
		} else {
			pathText = filepath.ToSlash(filepath.Join(filepath.FromSlash(cwd), filepath.FromSlash(pathText)))
		}
	}
	// Encode the URL-significant bytes once; renderLocalLinkTarget decodes
	// them back, which keeps a literal "%20" (and similar) visible verbatim.
	dest := strings.ReplaceAll(pathText, "%", "%25")
	dest = strings.ReplaceAll(dest, "#", "%23")
	dest = strings.ReplaceAll(dest, "?", "%3F")
	dest += locationSuffix
	display, ok := renderLocalLinkTarget(dest, cwd)
	return dest, display, ok
}

// collectCitationLiteralRanges returns byte ranges where directives must stay
// literal: code blocks/spans, raw HTML, HTML blocks, and existing link/image
// labels.
func collectCitationLiteralRanges(document ast.Node) [][2]int {
	var ranges [][2]int
	_ = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch block := node.(type) {
		case *ast.CodeBlock:
			ranges = appendCodeBlockRanges(ranges, block.Lines())
		case *ast.FencedCodeBlock:
			ranges = appendCodeBlockRanges(ranges, block.Lines())
		case *ast.CodeSpan:
			ranges = appendDescendantTextRanges(ranges, block)
		case *ast.HTMLBlock:
			ranges = appendCodeBlockRanges(ranges, block.Lines())
		case *ast.RawHTML:
			if block.Segments != nil {
				for i := 0; i < block.Segments.Len(); i++ {
					segment := block.Segments.At(i)
					ranges = append(ranges, [2]int{segment.Start, segment.Stop})
				}
			}
		case *ast.Link, *ast.Image:
			ranges = appendDescendantTextRanges(ranges, block)
		}
		return ast.WalkContinue, nil
	})
	return ranges
}
