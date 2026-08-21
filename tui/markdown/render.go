package markdown

import (
	"regexp"
	"strings"

	codextui "codex_go/tui"
	"codex_go/utils"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
	"github.com/muesli/termenv"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	gmtext "github.com/yuin/goldmark/text"
)

const defaultWidth = 80

const (
	codeBlockStartMarker = "CODEX_INTERNAL_CODE_BLOCK_START"
	codeBlockEndMarker   = "CODEX_INTERNAL_CODE_BLOCK_END"
)

type sourceCodeBlock struct {
	Code     string
	Language string
}

func Render(text string, width int) (string, error) {
	return RenderWithTheme(text, width, "")
}

func RenderWithTheme(text string, width int, themeID string) (string, error) {
	return RenderWithThemeCwd(text, width, themeID, "")
}

// RenderWithThemeCwd renders markdown for the TUI transcript, resolving local
// file links against cwd so they display the real target path instead of the
// label (Rust parity: markdown_render local file-link display). Passing an
// empty cwd renders absolute paths unchanged.
func RenderWithThemeCwd(text string, width int, themeID string, cwd string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", nil
	}
	if width <= 0 {
		width = defaultWidth
	}
	text = UnwrapMarkdownFences(text)
	text, localLinks, webLinks := rewriteLinksWithInfo(text, cwd)
	codeBlocks := collectSourceCodeBlocks(text)
	tables, renderText := detectSourceTables(text)
	style := codexMarkdownStyle()
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStyles(style),
		glamour.WithWordWrap(width),
		glamour.WithColorProfile(termenv.TrueColor),
		glamour.WithChromaFormatter("terminal16m"),
	)
	if err != nil {
		return "", err
	}
	out, err := renderer.Render(renderText)
	if err != nil {
		return "", err
	}
	out = restoreSourceCodeBlocks(out, codeBlocks, themeID)
	out = restoreRenderedTables(out, tables, width)
	out = annotateRenderedLineURLs(out)
	out = annotateWebLinkLabels(out, webLinks)
	out = annotateLocalFileLinks(out, localLinks, cwd)
	return strings.TrimRight(out, "\n"), nil
}

// markdownLinkRE matches simple markdown link spans "[label](dest)" (with an
// optional title) that are not necessarily preceded by "!" (image syntax).
var markdownLinkRE = regexp.MustCompile(`\[([^\]\n]*)\]\(([^()\s]+)(?:\s+[^)]*)?\)`)

// rewriteLocalFileLinks rewrites the label of every local file link in the
// source to the resolved target path before markdown rendering, so the
// transcript shows the real file target (optionally shortened against cwd)
// instead of the caller-provided label. Code blocks, inline code, and image
// syntax are left untouched by locating the code ranges in the parsed document
// and skipping any link match that falls inside them.
// webFileLink records a markdown link with a web destination so the rendered
// label can be wrapped in an OSC-8 hyperlink (Rust mark_buffer_hyperlinks).
type webFileLink struct {
	Label string
	URL   string
}

func rewriteLocalFileLinks(source string, cwd string) string {
	out, _, _ := rewriteLinksWithInfo(source, cwd)
	return out
}

// rewriteLinksWithInfo rewrites local file link labels to their resolved target
// paths and returns the rewritten source plus the annotations needed to add
// OSC-8 hyperlinks afterward (local file targets and web link labels).
func rewriteLinksWithInfo(source string, cwd string) (string, []localFileLink, []webFileLink) {
	if !strings.Contains(source, "](") {
		return source, nil, nil
	}
	data := []byte(source)
	document := goldmark.DefaultParser().Parse(gmtext.NewReader(data))
	codeRanges := collectCodeRanges(document)
	matches := markdownLinkRE.FindAllStringSubmatchIndex(source, -1)
	if len(matches) == 0 {
		return source, nil, nil
	}
	var sb strings.Builder
	var links []localFileLink
	var webLinks []webFileLink
	cursor := 0
	for _, m := range matches {
		// Skip matches inside code blocks/code spans so code text is preserved.
		if inCodeRange(m[0], codeRanges) {
			continue
		}
		// Skip image syntax "![...](...)".
		if m[0] > 0 && source[m[0]-1] == '!' {
			continue
		}
		dest := source[m[4]:m[5]]
		label := source[m[2]:m[3]]
		if isLocalPathLikeLink(dest) {
			display, ok := renderLocalLinkTarget(dest, cwd)
			if !ok {
				continue
			}
			// Render the resolved target as a code span (Rust parity: local file
			// links show the real path, not the markdown label).
			sb.WriteString(source[cursor:m[0]])
			sb.WriteString("`")
			sb.WriteString(display)
			sb.WriteString("`")
			cursor = m[1]
			links = append(links, localFileLink{Display: display, Dest: dest})
			continue
		}
		if webURL, ok := codextui.WebDestination(dest); ok {
			webLinks = append(webLinks, webFileLink{Label: label, URL: webURL})
		}
	}
	sb.WriteString(source[cursor:])
	return sb.String(), links, webLinks
}

// annotateWebLinkLabels wraps the rendered label of each markdown web link in an
// OSC-8 hyperlink to its destination (Rust mark_buffer_hyperlinks).
func annotateWebLinkLabels(rendered string, webLinks []webFileLink) string {
	if len(webLinks) == 0 {
		return rendered
	}
	lines := strings.Split(strings.ReplaceAll(rendered, "\r\n", "\n"), "\n")
	for i, line := range lines {
		for _, link := range webLinks {
			if link.Label == "" || !strings.Contains(line, link.Label) {
				continue
			}
			line = strings.ReplaceAll(line, link.Label, osc8FileLink(link.URL, link.Label))
		}
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}

// annotateLocalFileLinks wraps matching local file path text in each rendered
// line with an OSC-8 hyperlink pointing at the original file target, so file
// references are clickable (Rust trusted_file_destination).
func annotateLocalFileLinks(rendered string, links []localFileLink, cwd string) string {
	if len(links) == 0 {
		return rendered
	}
	lines := strings.Split(strings.ReplaceAll(rendered, "\r\n", "\n"), "\n")
	for i, line := range lines {
		for _, link := range links {
			if link.Display == "" || !strings.Contains(line, link.Display) {
				continue
			}
			fileURL := localLinkFileURL(link.Dest, cwd)
			if fileURL == "" {
				continue
			}
			line = strings.ReplaceAll(line, link.Display, osc8FileLink(fileURL, link.Display))
		}
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}

// osc8FileLink wraps text in an OSC-8 hyperlink to an absolute file URL.
func osc8FileLink(fileURL string, text string) string {
	return "\x1b]8;;" + fileURL + "\x07" + text + "\x1b]8;;\x07"
}

// collectCodeRanges returns the byte ranges of fenced/indented code blocks and
// inline code spans so link rewriting can leave code text untouched.
func collectCodeRanges(document ast.Node) [][2]int {
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
		}
		return ast.WalkContinue, nil
	})
	return ranges
}

func appendCodeBlockRanges(ranges [][2]int, lines *gmtext.Segments) [][2]int {
	if lines == nil {
		return ranges
	}
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		ranges = append(ranges, [2]int{seg.Start, seg.Stop})
	}
	return ranges
}

func appendDescendantTextRanges(ranges [][2]int, node ast.Node) [][2]int {
	_ = ast.Walk(node, func(child ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if text, ok := child.(*ast.Text); ok {
			ranges = append(ranges, [2]int{text.Segment.Start, text.Segment.Stop})
		}
		return ast.WalkContinue, nil
	})
	return ranges
}

func inCodeRange(pos int, ranges [][2]int) bool {
	for _, r := range ranges {
		if pos >= r[0] && pos < r[1] {
			return true
		}
	}
	return false
}

// annotateRenderedLineURLs wraps web URLs in each rendered line with OSC-8
// terminal hyperlinks so URLs in assistant messages are clickable, matching the
// Rust codex TUI behavior.
func annotateRenderedLineURLs(rendered string) string {
	if !strings.Contains(rendered, "http") {
		return rendered
	}
	lines := strings.Split(strings.ReplaceAll(rendered, "\r\n", "\n"), "\n")
	for i, line := range lines {
		lines[i] = codextui.AnnotateWebURLsInLine(line)
	}
	return strings.Join(lines, "\n")
}

// codexMarkdownStyle returns the glamour style config used for the TUI
// transcript. It aligns the markdown rendering with the Rust codex TUI
// (codex-rs/tui/src/markdown_render.rs): bold/italic/headings/code/links are
// styled instead of being emitted as literal markers, blockquotes use a box
// drawing indent, and tables use box drawing separators.
func codexMarkdownStyle() ansi.StyleConfig {
	style := styles.ASCIIStyleConfig
	zeroMargin := uint(0)
	style.Document.Margin = &zeroMargin

	style.H1 = ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Bold: styleBool(true), Underline: styleBool(true)}}
	style.H2 = ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Bold: styleBool(true)}}
	style.H3 = ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Bold: styleBool(true), Italic: styleBool(true)}}
	style.H4 = ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Italic: styleBool(true)}}
	style.H5 = ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Italic: styleBool(true)}}
	style.H6 = ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Italic: styleBool(true)}}
	style.Strong = ansi.StylePrimitive{Bold: styleBool(true)}
	style.Emph = ansi.StylePrimitive{Italic: styleBool(true)}
	style.Code = ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Color: styleString("cyan")}}
	style.Strikethrough = ansi.StylePrimitive{CrossedOut: styleBool(true)}
	style.Link = ansi.StylePrimitive{Color: styleString("cyan"), Underline: styleBool(true)}
	style.BlockQuote = ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{Color: styleString("green")},
		Indent:         styleUint(1),
		IndentToken:    styleString("\u2502 "),
	}
	style.Table = ansi.StyleTable{
		CenterSeparator: styleString("\u2502"),
		ColumnSeparator: styleString("\u2502"),
		RowSeparator:    styleString("\u2500"),
	}

	// Disable glamour's own code block styling; the Go TUI applies its own
	// syntax highlighting via HighlightCodeANSI so the theme is honored.
	style.CodeBlock.Theme = ""
	style.CodeBlock.Chroma = nil
	style.CodeBlock.StyleBlock = ansi.StyleBlock{}
	style.CodeBlock.BlockPrefix = codeBlockStartMarker + "\n"
	style.CodeBlock.BlockSuffix = "\n" + codeBlockEndMarker

	return style
}

func styleBool(value bool) *bool       { return &value }
func styleString(value string) *string { return &value }
func styleUint(value uint) *uint       { return &value }

func collectSourceCodeBlocks(source string) []sourceCodeBlock {
	data := []byte(source)
	document := goldmark.DefaultParser().Parse(gmtext.NewReader(data))
	blocks := []sourceCodeBlock{}
	_ = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch block := node.(type) {
		case *ast.FencedCodeBlock:
			blocks = append(blocks, sourceCodeBlock{
				Code:     string(block.Lines().Value(data)),
				Language: codeLanguageToken(string(block.Language(data))),
			})
		case *ast.CodeBlock:
			blocks = append(blocks, sourceCodeBlock{Code: string(block.Lines().Value(data))})
		}
		return ast.WalkContinue, nil
	})
	return blocks
}

func codeLanguageToken(language string) string {
	language = strings.TrimSpace(language)
	if language == "" {
		return ""
	}
	if index := strings.IndexAny(language, ", \t"); index >= 0 {
		language = language[:index]
	}
	return strings.TrimSpace(language)
}

func restoreSourceCodeBlocks(rendered string, blocks []sourceCodeBlock, themeID string) string {
	if len(blocks) == 0 || !renderedContainsCodeBlockMarkers(rendered, len(blocks)) {
		return rendered
	}
	lines := strings.Split(strings.ReplaceAll(rendered, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	blockIndex := 0
	skippingRenderedCode := false
	for _, line := range lines {
		plain := utils.StripANSI(line)
		if markerIndex := strings.Index(plain, codeBlockStartMarker); markerIndex >= 0 && blockIndex < len(blocks) {
			indent := plain[:markerIndex]
			highlighted := codextui.HighlightCodeANSI(blocks[blockIndex].Code, blocks[blockIndex].Language, themeID)
			if highlighted == "" {
				out = append(out, indent)
			} else {
				for _, codeLine := range strings.Split(strings.ReplaceAll(highlighted, "\r\n", "\n"), "\n") {
					out = append(out, indent+codeLine)
				}
			}
			blockIndex++
			skippingRenderedCode = true
			continue
		}
		if skippingRenderedCode {
			if strings.Contains(plain, codeBlockEndMarker) {
				skippingRenderedCode = false
			}
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func renderedContainsCodeBlockMarkers(rendered string, expected int) bool {
	starts := 0
	ends := 0
	for _, line := range strings.Split(strings.ReplaceAll(rendered, "\r\n", "\n"), "\n") {
		plain := utils.StripANSI(line)
		if strings.Contains(plain, codeBlockStartMarker) {
			starts++
		}
		if strings.Contains(plain, codeBlockEndMarker) {
			ends++
		}
	}
	return starts == expected && ends == expected
}
