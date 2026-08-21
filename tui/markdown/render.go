package markdown

import (
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
	text = strings.TrimSpace(text)
	if text == "" {
		return "", nil
	}
	if width <= 0 {
		width = defaultWidth
	}
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
	return strings.TrimRight(out, "\n"), nil
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
