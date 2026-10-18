package markdown

import (
	"bytes"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	gmtext "github.com/yuin/goldmark/text"

	codextui "codex_go/tui"
)

// RenderClipboardHTML converts assistant Markdown into the HTML fragment used
// for rich-text clipboard destinations (Rust #42847 clipboard_html.rs). The
// source Markdown remains the plain-text representation; GFM extensions keep
// tables, strikethrough, and task lists intact.
func RenderClipboardHTML(source string) string {
	if source == "" {
		return ""
	}
	source = UnwrapMarkdownFences(source)
	data := []byte(source)
	md := goldmark.New(goldmark.WithExtensions(extension.GFM))
	parsed := md.Parser().Parse(gmtext.NewReader(data))
	document, ok := parsed.(*ast.Document)
	if !ok {
		return ""
	}
	clipboardHTMLTransformer{}.Transform(document, gmtext.NewReader(data), nil)
	var buf bytes.Buffer
	if err := md.Renderer().Render(&buf, data, document); err != nil {
		return ""
	}
	return buf.String()
}

// clipboardHTMLTransformer mirrors Rust's clipboard event filter: raw HTML is
// emitted as escaped text, images keep only their alt text, and non-web links
// remain readable as "label (destination)".
type clipboardHTMLTransformer struct{}

func (clipboardHTMLTransformer) Transform(document *ast.Document, reader gmtext.Reader, _ parser.Context) {
	var images []*ast.Image
	var links []*ast.Link
	var rawHTML []ast.Node
	_ = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch node := node.(type) {
		case *ast.Image:
			images = append(images, node)
		case *ast.Link:
			links = append(links, node)
		case *ast.RawHTML, *ast.HTMLBlock:
			rawHTML = append(rawHTML, node)
		}
		return ast.WalkContinue, nil
	})
	for _, image := range images {
		replaceClipboardNode(image)
	}
	for _, link := range links {
		destination := string(link.Destination)
		if _, ok := codextui.WebDestination(destination); ok {
			continue
		}
		replaceClipboardNode(link, ast.NewString([]byte(" ("+destination+")")))
	}
	for _, node := range rawHTML {
		text := clipboardRawHTMLText(node, reader.Source())
		if block, ok := node.(*ast.HTMLBlock); ok {
			paragraph := ast.NewParagraph()
			paragraph.AppendChild(paragraph, ast.NewString([]byte(text)))
			replaceClipboardNode(block, paragraph)
			continue
		}
		replaceClipboardNode(node, ast.NewString([]byte(text)))
	}
}

// replaceClipboardNode moves a node's children before it, appends optional
// inline text, and removes the original node.
func replaceClipboardNode(node ast.Node, extra ...ast.Node) {
	parent := node.Parent()
	if parent == nil {
		return
	}
	for child := node.FirstChild(); child != nil; {
		next := child.NextSibling()
		parent.InsertBefore(parent, node, child)
		child = next
	}
	for _, addition := range extra {
		parent.InsertBefore(parent, node, addition)
	}
	parent.RemoveChild(parent, node)
}

func clipboardRawHTMLText(node ast.Node, source []byte) string {
	switch node := node.(type) {
	case *ast.RawHTML:
		var text strings.Builder
		for index := 0; index < node.Segments.Len(); index++ {
			segment := node.Segments.At(index)
			text.Write(segment.Value(source))
		}
		return text.String()
	case *ast.HTMLBlock:
		return string(node.Text(source))
	default:
		return ""
	}
}
